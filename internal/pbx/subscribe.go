package pbx

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A subscription is a phone asking to be told about a mailbox (RFC 3265 SUBSCRIBE, RFC 3842
// message-summary): it lights the message-waiting lamp.
type subscription struct {
	mu        sync.Mutex
	event     string // message-summary (voicemail lamp) or dialog (busy lamp field)
	box       string // the mailbox, or the extension watched
	version   int    // dialog-info document version
	lastState string // the dialog state last sent
	callID    string
	localTag  string
	localURI  string
	remoteURI string
	target    string
	routeSet  []string
	peer      sip.Peer
	expires   time.Time
	seq       uint32
}

const (
	minSubscribe = 60
	maxSubscribe = 3600
)

// subscribe handles a SUBSCRIBE outside a dialog.
func (e *Engine) subscribe(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	ev := strings.ToLower(strings.TrimSpace(strings.SplitN(m.Get("Event"), ";", 2)[0]))
	if ev != "message-summary" && ev != "dialog" {
		r := sip.NewResponse(m, 489, "Bad Event")
		r.Set("Allow-Events", "message-summary, dialog")
		tx.Respond(r)
		return
	}
	cfg := e.Config()
	lookup := func(user string) (string, bool) {
		x := cfg.FindExtension(user)
		if x == nil || !x.Enabled {
			return "", false
		}
		return x.Secret, true
	}
	result, user := e.auth.Verify(m.Get("Authorization"), sip.SUBSCRIBE, lookup)
	ip := in.From.Addr.Addr()
	switch result {
	case sip.AuthOK:
		e.bans.succeed(ip)
	case sip.AuthBad:
		e.bans.fail(ip, "failed subscription as "+user)
		e.reg.challenge(tx, m, false)
		return
	case sip.AuthStale:
		e.reg.challenge(tx, m, true)
		return
	default:
		e.reg.challenge(tx, m, false)
		return
	}
	box := user
	if ev == "dialog" {
		// Busy lamp: any extension may watch any other.
		u, err := sip.ParseURI(m.RequestURI)
		if err != nil || cfg.FindExtension(u.User) == nil {
			tx.Respond(sip.NewResponse(m, 404, "No Such Extension"))
			return
		}
		box = u.User
	} else if u, err := sip.ParseURI(m.RequestURI); err == nil && u.User != "" && u.User != user {
		// Phones subscribe to their own mailbox, addressed by number or by the voicemail access code.
		if u.User != cfg.System.VoicemailNumber {
			tx.Respond(sip.NewResponse(m, 403, "Not Your Mailbox"))
			return
		}
	}
	x := cfg.FindExtension(box)
	if ev == "message-summary" && (x == nil || !x.Voicemail.Enabled) {
		tx.Respond(sip.NewResponse(m, 404, "No Mailbox"))
		return
	}
	if _, err := sip.ParseAddress(m.Get("From")); err != nil {
		tx.Respond(sip.NewResponse(m, 400, "Bad From"))
		return
	}
	contact, _ := sip.ParseAddress(m.Get("Contact"))
	target := m.RequestURI
	if contact != nil && contact.URI != nil {
		target = contact.URI.String()
	}
	tag := sip.NewTag()
	to, err := sip.ParseAddress(m.Get("To"))
	if err != nil {
		tx.Respond(sip.NewResponse(m, 400, "Bad To"))
		return
	}
	to.Params.Set("tag", tag)
	s := &subscription{event: ev, box: box, callID: m.CallID(), localTag: tag, localURI: to.String(), remoteURI: m.Get("From"),
		target: target, routeSet: m.GetAll("Record-Route"), peer: in.From}
	e.refreshSubscription(tx, m, s)
}

// refreshSubscription answers a SUBSCRIBE (new, refresh or Expires: 0) and sends the NOTIFY it calls for.
func (e *Engine) refreshSubscription(tx *sip.ServerTx, m *sip.Message, s *subscription) {
	exp := maxSubscribe
	if v := m.Get("Expires"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			exp = n
		}
	}
	if exp != 0 && exp < minSubscribe {
		r := sip.NewResponse(m, 423, "Interval Too Brief")
		r.Set("Min-Expires", strconv.Itoa(minSubscribe))
		tx.Respond(r)
		return
	}
	exp = min(exp, maxSubscribe)
	key := legKey(s.callID, s.localTag)
	s.mu.Lock()
	s.expires = time.Now().Add(time.Duration(exp) * time.Second)
	s.mu.Unlock()
	e.mu.Lock()
	if exp == 0 {
		delete(e.subs, key)
	} else {
		e.subs[key] = s
	}
	e.mu.Unlock()

	r := sip.NewResponse(m, 200, "")
	r.Set("To", s.localURI)
	r.Set("Expires", strconv.Itoa(exp))
	r.Set("Contact", e.localContact(s.peer, ""))
	tx.Respond(r)
	if s.event == "dialog" {
		go e.sendDialog(s, e.extensionState(s.box), true)
		return
	}
	if e.Mail != nil {
		fresh, old := e.Mail.VoicemailCounts(s.box)
		go e.sendMWI(s, fresh, old)
	}
}

// subscriptionRequest handles a request inside a subscription dialog: a refresh or an unsubscribe.
func (e *Engine) subscriptionRequest(tx *sip.ServerTx, in *sip.Incoming, s *subscription) {
	m := in.Msg
	if m.Method != sip.SUBSCRIBE {
		if tx != nil {
			tx.Respond(sip.NewResponse(m, 200, ""))
		}
		return
	}
	e.refreshSubscription(tx, m, s)
}

// notifyMWI tells every phone subscribed to a mailbox its new and old counts.
func (e *Engine) notifyMWI(box string, fresh, old int) {
	now := time.Now()
	e.mu.Lock()
	var subs []*subscription
	for k, s := range e.subs {
		s.mu.Lock()
		expired := now.After(s.expires)
		s.mu.Unlock()
		if expired {
			delete(e.subs, k)
			continue
		}
		if s.box == box && s.event == "message-summary" {
			subs = append(subs, s)
		}
	}
	e.mu.Unlock()
	for _, s := range subs {
		go e.sendMWI(s, fresh, old)
	}
}

func (e *Engine) sendMWI(s *subscription, fresh, old int) {
	waiting := "no"
	if fresh > 0 {
		waiting = "yes"
	}
	body := fmt.Sprintf("Messages-Waiting: %s\r\nMessage-Account: sip:%s@%s\r\nVoice-Message: %d/%d (0/0)\r\n",
		waiting, s.box, e.stack.LocalHost(s.peer), fresh, old)
	e.sendNotify(s, "application/simple-message-summary", body)
}

// extensionState is an extension's dialog state for busy lamps: terminated (idle), early (ringing) or
// confirmed (on a call).
func (e *Engine) extensionState(ext string) string {
	busy, ringing := e.busyExtensions()
	switch {
	case busy[ext]:
		return "confirmed"
	case ringing[ext]:
		return "early"
	}
	return "terminated"
}

// sendDialog sends an RFC 4235 dialog-info document, unless the state is unchanged (force sends anyway).
func (e *Engine) sendDialog(s *subscription, state string, force bool) {
	s.mu.Lock()
	if !force && s.lastState == state {
		s.mu.Unlock()
		return
	}
	s.lastState = state
	v := s.version
	s.version++
	s.mu.Unlock()
	entity := "sip:" + s.box + "@" + e.stack.LocalHost(s.peer)
	body := fmt.Sprintf(`<?xml version="1.0"?>`+"\n"+`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" version="%d" state="full" entity="%s">`, v, entity)
	if state != "terminated" {
		dir := "recipient"
		body += fmt.Sprintf(`<dialog id="%s" direction="%s"><state>%s</state></dialog>`, s.box, dir, state)
	}
	body += "</dialog-info>\n"
	e.sendNotify(s, "application/dialog-info+xml", body)
}

// presenceChanged re-sends busy lamp state to every watcher whose extension changed.
func (e *Engine) presenceChanged() {
	e.presenceMu.Lock()
	defer e.presenceMu.Unlock()
	now := time.Now()
	e.mu.Lock()
	var subs []*subscription
	for _, s := range e.subs {
		if s.event == "dialog" && now.Before(s.expires) {
			subs = append(subs, s)
		}
	}
	e.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	busy, ringing := e.busyExtensions()
	for _, s := range subs {
		state := "terminated"
		if busy[s.box] {
			state = "confirmed"
		} else if ringing[s.box] {
			state = "early"
		}
		e.sendDialog(s, state, false)
	}
}

func (e *Engine) sendNotify(s *subscription, contentType, body string) {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	left := int(time.Until(s.expires).Seconds())
	s.mu.Unlock()
	state := fmt.Sprintf("active;expires=%d", max(left, 0))
	if left <= 0 {
		state = "terminated;reason=timeout"
	}
	m := &sip.Message{IsRequest: true, Method: sip.NOTIFY, RequestURI: s.target}
	for _, r := range s.routeSet {
		m.Add("Route", r)
	}
	m.Set("From", s.localURI)
	m.Set("To", s.remoteURI)
	m.Set("Call-ID", s.callID)
	m.Set("CSeq", fmt.Sprintf("%d NOTIFY", seq))
	m.Set("Contact", e.localContact(s.peer, ""))
	m.Set("Event", s.event)
	m.Set("Subscription-State", state)
	m.Set("Content-Type", contentType)
	m.Body = []byte(body)
	tx := e.stack.Request(m, s.peer)
	for r := range tx.Responses {
		if r.StatusCode == 481 || r.StatusCode >= 400 && r.StatusCode != 408 && r.StatusCode != 491 {
			e.mu.Lock()
			delete(e.subs, legKey(s.callID, s.localTag))
			e.mu.Unlock()
		}
	}
}
