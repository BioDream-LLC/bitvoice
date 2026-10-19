package pbx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// NENA i3 state events (NENA-STA-010.3 2.4.1, 2.4.2 and 4.2.1.3), over SIP SUBSCRIBE and NOTIFY
// (RFC 6665):
//
//   - As a subscriber (System.Core.WatchStates), the ESRP subscribes to each PSAP's
//     emergency-QueueState and emergency-ServiceState over the PSAP's trunk. The policy rules'
//     queue and service state conditions use what the PSAPs say, and a PSAP that says it cannot
//     take calls is passed over for its overflow (never leaving a call with no PSAP at all). A PSAP
//     that does not answer is Unreachable, as the standard says a subscriber must assume.
//   - As a notifier (System.Core.States), other elements may subscribe to this server's
//     emergency-ElementState, emergency-ServiceState and emergency-QueueState (its incoming queue),
//     from the addresses in AllowFrom only (603 Decline otherwise).

const (
	evQueueState   = "emergency-queuestate"
	evServiceState = "emergency-servicestate"
	evElementState = "emergency-elementstate"
	stateSubExpiry = 3600
)

// PSAPState is what a PSAP has said of its queue and service.
type PSAPState struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	// Queue is its queue's state (Active, Inactive, Disabled, Full, Standby, ResourceExhausted, or
	// Unreachable when it does not answer), with its length and most calls.
	Queue    string    `json:"queue,omitempty"`
	Length   int       `json:"length"`
	Max      int       `json:"max"`
	QueueAt  time.Time `json:"queueAt,omitzero"`
	Service  string    `json:"service,omitempty"` // Normal, Unstaffed, Down, ...
	Reason   string    `json:"reason,omitempty"`
	Posture  string    `json:"posture,omitempty"` // its security posture, when it keeps one
	Domain   string    `json:"domain,omitempty"`  // its service identifier
	ServedAt time.Time `json:"serviceAt,omitzero"`
	// Subscriptions says how each subscription stands: active, refused (with the answer), no answer.
	Subscriptions map[string]string `json:"subscriptions"`
}

type stateSub struct {
	psap      string // the PSAP's URI
	event     string // evQueueState or evServiceState
	callID    string
	localTag  string
	localURI  string
	remoteURI string
	remoteTag string
	target    string
	routeSet  []string
	peer      sip.Peer
	seq       uint32
	expires   time.Time
	retry     time.Time // when to try again after a failure
	status    string
	busy      bool // a SUBSCRIBE is in flight
}

type stateWatch struct {
	mu    sync.Mutex
	subs  map[string]*stateSub  // by Call-ID
	known map[string]*PSAPState // by the PSAP's URI, lower case
	// notifier side: what was last sent, to send only changes (and one a minute regardless)
	lastQueue   string
	lastService string
}

// PSAPStates is what each PSAP listed has said, in the order they are listed.
func (e *Engine) PSAPStates() []PSAPState {
	cfg := e.Config()
	e.watch.mu.Lock()
	defer e.watch.mu.Unlock()
	out := []PSAPState{}
	for _, ps := range cfg.System.Core.PSAPs {
		st := PSAPState{URI: ps.URI, Name: ps.Name, Subscriptions: map[string]string{}}
		if k := e.watch.known[strings.ToLower(ps.URI)]; k != nil {
			st = *k
			st.Name = ps.Name
			st.Subscriptions = map[string]string{}
		}
		for _, s := range e.watch.subs {
			if strings.EqualFold(s.psap, ps.URI) {
				st.Subscriptions[s.event] = s.status
			}
		}
		out = append(out, st)
	}
	return out
}

// watchLoop keeps the subscriptions to the PSAPs' states up, and sends this server's own queue
// state once a minute to its subscribers (the standard's default force rate).
func (e *Engine) watchLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	n := 0
	for {
		e.watchTick()
		if n%12 == 0 {
			e.statesChanged(true)
		}
		n++
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) watchTick() {
	cfg := e.Config()
	if cfg == nil {
		return
	}
	core := cfg.System.Core
	now := time.Now()
	e.watch.mu.Lock()
	if e.watch.subs == nil {
		e.watch.subs, e.watch.known = map[string]*stateSub{}, map[string]*PSAPState{}
	}
	listed := map[string]bool{}
	if core.WatchStates {
		for _, ps := range core.PSAPs {
			listed[strings.ToLower(ps.URI)] = true
		}
	}
	var gone, due []*stateSub
	for id, s := range e.watch.subs {
		if !listed[strings.ToLower(s.psap)] {
			delete(e.watch.subs, id)
			if s.status == "active" {
				gone = append(gone, s)
			}
		}
	}
	for k := range e.watch.known {
		if !listed[k] {
			delete(e.watch.known, k)
		}
	}
	for _, ps := range core.PSAPs {
		if !core.WatchStates {
			break
		}
		for _, ev := range []string{evQueueState, evServiceState} {
			var s *stateSub
			for _, x := range e.watch.subs {
				if strings.EqualFold(x.psap, ps.URI) && x.event == ev {
					s = x
				}
			}
			if s == nil {
				s = &stateSub{psap: ps.URI, event: ev, callID: sip.NewCallID(), localTag: sip.NewTag(), status: "subscribing"}
				e.watch.subs[s.callID] = s
			}
			switch {
			case s.busy:
			case s.status == "active" && time.Until(s.expires) > stateSubExpiry*time.Second/4:
			case s.status != "active" && s.status != "subscribing" && now.Before(s.retry):
			default:
				s.busy = true
				due = append(due, s)
			}
		}
	}
	e.watch.mu.Unlock()
	for _, s := range gone {
		go e.sendStateSubscribe(cfg, s, 0)
	}
	for _, s := range due {
		go e.sendStateSubscribe(cfg, s, stateSubExpiry)
	}
}

// sendStateSubscribe sends (or refreshes, or with expires 0 ends) one subscription to a PSAP.
func (e *Engine) sendStateSubscribe(cfg *config.Config, s *stateSub, expires int) {
	done := func(status string, retry time.Duration) {
		e.watch.mu.Lock()
		s.busy, s.status = false, status
		s.retry = time.Now().Add(retry)
		if status != "active" {
			// A fresh dialog next time.
			s.remoteTag, s.target, s.routeSet = "", "", nil
		}
		if status == "no answer" {
			k := e.knownLocked(s.psap)
			if s.event == evQueueState {
				k.Queue, k.QueueAt = "Unreachable", time.Now()
			} else {
				k.Service, k.Reason, k.ServedAt = "Unreachable", "no answer to SUBSCRIBE", time.Now()
			}
		}
		e.watch.mu.Unlock()
		e.publish("core.states", nil)
	}
	var ps *config.CorePSAP
	for i := range cfg.System.Core.PSAPs {
		if strings.EqualFold(cfg.System.Core.PSAPs[i].URI, s.psap) {
			ps = &cfg.System.Core.PSAPs[i]
		}
	}
	var t *config.Trunk
	if ps != nil {
		t = cfg.FindTrunk(ps.Trunk)
	}
	if t == nil || !t.Enabled {
		done("no trunk", time.Minute)
		return
	}
	peer, err := e.trunkPeer(t)
	if err != nil {
		done("no answer", time.Minute)
		return
	}
	e.watch.mu.Lock()
	s.seq++
	seq := s.seq
	if s.remoteTag == "" {
		host := t.Host
		if t.Port != 0 {
			host += ":" + strconv.Itoa(t.Port)
		}
		s.target = "sip:" + uriUser(s.psap, "sos") + "@" + host
		s.localURI = "<" + e.queueURI(cfg) + ">;tag=" + s.localTag
		s.remoteURI = "<" + s.psap + ">"
		s.peer = peer
	}
	m := &sip.Message{IsRequest: true, Method: sip.SUBSCRIBE, RequestURI: s.target}
	for _, r := range s.routeSet {
		m.Add("Route", r)
	}
	to := s.remoteURI
	if s.remoteTag != "" {
		to += ";tag=" + s.remoteTag
	}
	m.Set("From", s.localURI)
	m.Set("To", to)
	m.Set("Call-ID", s.callID)
	m.Set("CSeq", strconv.FormatUint(uint64(seq), 10)+" SUBSCRIBE")
	m.Set("Contact", e.localContact(s.peer, "esrp"))
	m.Set("Event", stateEventName(s.event))
	m.Set("Expires", strconv.Itoa(expires))
	m.Set("Accept", stateContentType(s.event))
	peerNow := s.peer
	e.watch.mu.Unlock()
	r := finalOf(e.stack.Request(m, peerNow))
	if expires == 0 {
		return
	}
	switch {
	case r == nil || r.StatusCode == 408:
		done("no answer", time.Minute)
	case r.StatusCode >= 200 && r.StatusCode < 300:
		exp := stateSubExpiry
		if v, err := strconv.Atoi(strings.TrimSpace(r.Get("Expires"))); err == nil && v > 0 {
			exp = min(v, stateSubExpiry)
		}
		e.watch.mu.Lock()
		if toAddr, err := sip.ParseAddress(r.Get("To")); err == nil && toAddr.Tag() != "" {
			s.remoteTag = toAddr.Tag()
		}
		if ct, err := sip.ParseAddress(r.Get("Contact")); err == nil && ct.URI != nil {
			s.target = ct.URI.String()
		}
		if rr := r.GetAll("Record-Route"); len(rr) > 0 {
			s.routeSet = append([]string(nil), rr...)
			slices.Reverse(s.routeSet)
		}
		s.expires = time.Now().Add(time.Duration(exp) * time.Second)
		s.busy, s.status = false, "active"
		e.watch.mu.Unlock()
		e.publish("core.states", nil)
	case r.StatusCode == 481:
		done("ended", 0) // the PSAP forgot it: start a new one at once
	default:
		done(fmt.Sprintf("refused (%d %s)", r.StatusCode, r.Reason), 10*time.Minute)
	}
}

func (e *Engine) knownLocked(uri string) *PSAPState {
	k := e.watch.known[strings.ToLower(uri)]
	if k == nil {
		k = &PSAPState{URI: uri}
		e.watch.known[strings.ToLower(uri)] = k
	}
	return k
}

// stateNotify takes a NOTIFY for one of the PSAP subscriptions; false when it is not one of them.
func (e *Engine) stateNotify(tx *sip.ServerTx, m *sip.Message) bool {
	e.watch.mu.Lock()
	s := e.watch.subs[m.CallID()]
	e.watch.mu.Unlock()
	if s == nil {
		return false
	}
	respond := func(code int, reason string) {
		if tx != nil {
			tx.Respond(sip.NewResponse(m, code, reason))
		}
	}
	ev := strings.ToLower(strings.TrimSpace(strings.SplitN(m.Get("Event"), ";", 2)[0]))
	if ev != s.event {
		respond(489, "Bad Event")
		return true
	}
	var body struct {
		QueueState *struct {
			QueueURI       string  `json:"queueUri"`
			QueueLength    float64 `json:"queueLength"`
			QueueMaxLength int     `json:"queueMaxLength"`
			State          string  `json:"state"`
		} `json:"queueState"`
		Service *struct {
			Name      string `json:"name"`
			Domain    string `json:"domain"`
			ServiceID string `json:"serviceId"`
		} `json:"service"`
		ServiceState *struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"serviceState"`
		SecurityPosture *struct {
			Posture string `json:"posture"`
		} `json:"securityPosture"`
	}
	if len(m.Body) > 0 {
		if err := json.Unmarshal(m.Body, &body); err != nil {
			respond(400, "Bad Body")
			return true
		}
	}
	now := time.Now()
	e.watch.mu.Lock()
	k := e.knownLocked(s.psap)
	switch {
	case ev == evQueueState && body.QueueState != nil && body.QueueState.State != "":
		q := body.QueueState
		k.Queue, k.Length, k.Max, k.QueueAt = q.State, int(q.QueueLength), q.QueueMaxLength, now
	case ev == evServiceState && body.ServiceState != nil && body.ServiceState.State != "":
		k.Service, k.Reason, k.ServedAt = body.ServiceState.State, body.ServiceState.Reason, now
		if body.Service != nil {
			k.Domain = firstNonEmptyStr(body.Service.ServiceID, body.Service.Domain)
		}
		if body.SecurityPosture != nil {
			k.Posture = body.SecurityPosture.Posture
		}
	}
	state := strings.ToLower(m.Get("Subscription-State"))
	if strings.HasPrefix(state, "terminated") {
		s.status, s.retry, s.remoteTag, s.target, s.routeSet = "ended", now.Add(5*time.Second), "", "", nil
	}
	e.watch.mu.Unlock()
	respond(200, "")
	e.publish("core.states", nil)
	return true
}

// watchedQueue is a PSAP queue's state as its NOTIFYs gave it ("" when not known).
func (e *Engine) watchedQueue(uri string) string {
	e.watch.mu.Lock()
	defer e.watch.mu.Unlock()
	if k := e.watch.known[strings.ToLower(uri)]; k != nil {
		return k.Queue
	}
	return ""
}

// watchedService is a service's state (and security posture) by the PSAP's URI, its host, or the
// service identifier it gave ("" when not known).
func (e *Engine) watchedService(service string, posture bool) string {
	service = strings.ToLower(strings.TrimSpace(service))
	if service == "" {
		return ""
	}
	e.watch.mu.Lock()
	defer e.watch.mu.Unlock()
	for key, k := range e.watch.known {
		host := key
		if u, err := sip.ParseURI(k.URI); err == nil {
			host = strings.ToLower(u.Host)
		}
		if service == key || service == host || service == strings.ToLower(k.Domain) {
			if posture {
				return k.Posture
			}
			return k.Service
		}
	}
	return ""
}

// cannotTake says why a PSAP's own state rules it out for a call now ("" when it can take one).
func (e *Engine) cannotTake(cfg *config.Config, ps *config.CorePSAP) string {
	if !cfg.System.Core.WatchStates {
		return ""
	}
	e.watch.mu.Lock()
	k := e.watch.known[strings.ToLower(ps.URI)]
	var q, svc string
	length, maxLen := 0, 0
	if k != nil {
		q, svc, length, maxLen = k.Queue, k.Service, k.Length, k.Max
	}
	e.watch.mu.Unlock()
	switch q {
	case "Inactive", "Disabled", "Full", "ResourceExhausted", "Unreachable":
		return "its queue says " + q
	}
	if q != "" && maxLen > 0 && length >= maxLen {
		return fmt.Sprintf("its queue holds %d of %d", length, maxLen)
	}
	switch svc {
	case "Unstaffed", "ScheduledMaintenanceDown", "Overloaded", "GoingDown", "Down", "Unreachable":
		return "its service says " + svc
	}
	return ""
}

// --- this server as a notifier --------------------------------------------------------------------

func stateEventName(ev string) string {
	switch ev {
	case evQueueState:
		return "emergency-QueueState"
	case evServiceState:
		return "emergency-ServiceState"
	}
	return "emergency-ElementState"
}

func stateContentType(ev string) string {
	switch ev {
	case evQueueState:
		return "application/EmergencyCallData.queuestate+json"
	case evServiceState:
		return "application/EmergencyCallData.ServiceState+json"
	}
	return "application/EmergencyCallData.ElementState+json"
}

// subscribeState answers another element's SUBSCRIBE to this server's own state.
func (e *Engine) subscribeState(tx *sip.ServerTx, in *sip.Incoming, ev string) {
	m := in.Msg
	cfg := e.Config()
	core := cfg.System.Core
	if !core.States {
		r := sip.NewResponse(m, 489, "Bad Event")
		r.Set("Allow-Events", "message-summary, dialog")
		tx.Respond(r)
		return
	}
	src := in.From.Addr.Addr().Unmap()
	ok := false
	for _, a := range core.AllowFrom {
		if p, err := netip.ParsePrefix(a); err == nil && p.Contains(src) {
			ok = true
		} else if ip, err := netip.ParseAddr(a); err == nil && ip.Unmap() == src {
			ok = true
		}
	}
	if !ok {
		tx.Respond(sip.NewResponse(m, 603, "Decline"))
		return
	}
	if ev == evQueueState {
		u, err := sip.ParseURI(m.RequestURI)
		q, qerr := sip.ParseURI(e.queueURI(cfg))
		if err != nil || qerr != nil || !strings.EqualFold(u.User, q.User) {
			tx.Respond(sip.NewResponse(m, 488, "Not A Queue Of This ESRP"))
			return
		}
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
	to, err := sip.ParseAddress(m.Get("To"))
	if err != nil {
		tx.Respond(sip.NewResponse(m, 400, "Bad To"))
		return
	}
	tag := sip.NewTag()
	to.Params.Set("tag", tag)
	s := &subscription{event: ev, callID: m.CallID(), localTag: tag, localURI: to.String(), remoteURI: m.Get("From"),
		target: target, routeSet: m.GetAll("Record-Route"), peer: in.From}
	e.refreshSubscription(tx, m, s)
}

// ownQueue is this server's incoming queue: the ESInet calls not yet answered, its most, and its state.
func (e *Engine) ownQueue(cfg *config.Config) (length, maxLen int, state string) {
	maxLen = cfg.System.Core.QueueMax
	if maxLen == 0 {
		maxLen = 20
	}
	for _, c := range e.allCalls() {
		c.mu.Lock()
		waiting := c.esinet && !c.ended
		c.mu.Unlock()
		select {
		case <-c.answered:
			waiting = false
		default:
		}
		if waiting {
			length++
		}
	}
	state = "Active"
	if length >= maxLen {
		state = "Full"
	}
	return length, maxLen, state
}

// ownService is this server's service state: Overloaded at its call limit, else Normal.
func (e *Engine) ownService() (state, reason string) {
	if why := e.overLimit("inbound"); why != "" {
		return "Overloaded", why
	}
	return "Normal", ""
}

// stateBody is the NOTIFY body for one of this server's state events.
func (e *Engine) stateBody(ev string) []byte {
	cfg := e.Config()
	host := cfg.System.Core.Source
	if host == "" {
		if u, err := sip.ParseURI(e.queueURI(cfg)); err == nil {
			host = u.Host
		}
	}
	var v any
	switch ev {
	case evQueueState:
		n, mx, st := e.ownQueue(cfg)
		v = map[string]any{"queueState": map[string]any{"queueUri": e.queueURI(cfg), "queueLength": n, "queueMaxLength": mx, "state": st}}
	case evServiceState:
		st, why := e.ownService()
		name := "PSAP"
		if len(cfg.System.Core.PSAPs) > 0 {
			name = "ESRP" // it routes calls on to PSAPs
		}
		v = map[string]any{"service": map[string]any{"name": name, "domain": host, "serviceId": host},
			"serviceState": map[string]any{"state": st, "reason": why}}
	default:
		v = map[string]any{"elementState": map[string]any{"elementDomain": host, "elementId": host, "state": "Normal", "reason": ""}}
	}
	b, _ := json.Marshal(v)
	return b
}

// statesChanged sends this server's queue and service state to their subscribers when it changed
// (or regardless, when force), at most once a second for each (the default throttle).
func (e *Engine) statesChanged(force bool) {
	cfg := e.Config()
	if cfg == nil || !cfg.System.Core.States {
		return
	}
	q, s := string(e.stateBody(evQueueState)), string(e.stateBody(evServiceState))
	e.watch.mu.Lock()
	sendQ, sendS := force || q != e.watch.lastQueue, force || s != e.watch.lastService
	e.watch.lastQueue, e.watch.lastService = q, s
	e.watch.mu.Unlock()
	if !sendQ && !sendS {
		return
	}
	now := time.Now()
	e.mu.Lock()
	var subs []*subscription
	for _, sub := range e.subs {
		if now.Before(sub.expires) && (sendQ && sub.event == evQueueState || sendS && sub.event == evServiceState || force && sub.event == evElementState) {
			subs = append(subs, sub)
		}
	}
	e.mu.Unlock()
	for _, sub := range subs {
		sub.mu.Lock()
		wait := time.Second - now.Sub(sub.lastSent)
		if force || wait <= 0 {
			sub.lastSent = now
			sub.mu.Unlock()
			go e.sendStateNotify(sub)
			continue
		}
		if sub.held { // one NOTIFY already waits, and will carry the state as it is then
			sub.mu.Unlock()
			continue
		}
		sub.held = true
		sub.mu.Unlock()
		go func() {
			time.Sleep(wait)
			sub.mu.Lock()
			sub.held, sub.lastSent = false, time.Now()
			sub.mu.Unlock()
			e.sendStateNotify(sub)
		}()
	}
}

func (e *Engine) sendStateNotify(s *subscription) {
	e.sendNotifyEvent(s, stateEventName(s.event), stateContentType(s.event), string(e.stateBody(s.event)))
}
