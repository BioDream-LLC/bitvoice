package sip

import (
	"strconv"
	"sync"
	"time"
)

// Transactions (RFC 3261 section 17), simplified where BitVoice is always the same kind of element: a
// registrar and back-to-back user agent. Retransmission timers run only on UDP; TCP is reliable.

// ServerTx is a server transaction: one received request and the responses to it.
type ServerTx struct {
	stack *Stack
	key   string
	Req   *Message
	From  Peer

	mu        sync.Mutex
	last      *Message
	acked     chan struct{}
	ackOnce   sync.Once
	terminate sync.Once
	// NoAck is called when a 2xx to an INVITE was never acknowledged (the caller has gone).
	NoAck func()
}

func txKey(branch, method string) string { return branch + "|" + method }

func (s *Stack) receiveRequest(in *Incoming) {
	m := in.Msg
	v, _ := ParseVia(m.Get("Via"))
	branch := ""
	if v != nil {
		branch = v.Branch()
	}
	if m.Method == ACK {
		s.mu.Lock()
		// ACK to a non-2xx final response shares the INVITE's branch and belongs to that transaction.
		if tx := s.servers[txKey(branch, INVITE)]; tx != nil && tx.lastCode() >= 300 {
			s.mu.Unlock()
			tx.ackOnce.Do(func() { close(tx.acked) })
			return
		}
		// ACK to a 2xx is end to end; stop the 2xx retransmission and pass it up.
		n, _ := m.CSeq()
		inv := s.invites[m.CallID()+"|"+strconv.FormatUint(uint64(n), 10)]
		s.mu.Unlock()
		if inv != nil {
			inv.ackOnce.Do(func() { close(inv.acked) })
		}
		s.dispatch(nil, in)
		return
	}
	key := txKey(branch, m.Method)
	s.mu.Lock()
	if tx, ok := s.servers[key]; ok && branch != "" {
		s.mu.Unlock()
		// A retransmission: answer with the last response, if there is one yet.
		if last := tx.lastResponse(); last != nil {
			_ = s.Send(last, tx.From)
		}
		return
	}
	tx := &ServerTx{stack: s, key: key, Req: m, From: ResponseTarget(m, in.From), acked: make(chan struct{})}
	if branch != "" {
		s.servers[key] = tx
	}
	s.mu.Unlock()
	s.dispatch(tx, in)
}

func (s *Stack) dispatch(tx *ServerTx, in *Incoming) {
	if s.handler == nil {
		if tx != nil {
			tx.Respond(NewResponse(in.Msg, 501, ""))
		}
		return
	}
	go s.handler(tx, in)
}

func (t *ServerTx) lastResponse() *Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

func (t *ServerTx) lastCode() int {
	if l := t.lastResponse(); l != nil {
		return l.StatusCode
	}
	return 0
}

// Branch returns the transaction's branch.
func (t *ServerTx) Branch() string {
	v, err := ParseVia(t.Req.Get("Via"))
	if err != nil {
		return ""
	}
	return v.Branch()
}

// Respond sends a response and runs the timers RFC 3261 asks for.
func (t *ServerTx) Respond(r *Message) {
	t.mu.Lock()
	if t.last != nil && t.last.StatusCode >= 200 {
		t.mu.Unlock()
		return // already final
	}
	t.last = r
	t.mu.Unlock()
	_ = t.stack.Send(r, t.From)
	if r.StatusCode < 200 {
		return
	}
	s := t.stack
	if t.Req.Method != INVITE {
		// Timer J: absorb retransmissions of the request for 64*T1 (zero on TCP).
		wait := s.expire()
		if t.From.Reliable() {
			wait = 0
		}
		time.AfterFunc(wait, t.done)
		return
	}
	if r.StatusCode < 300 {
		n, _ := t.Req.CSeq()
		ik := t.Req.CallID() + "|" + strconv.FormatUint(uint64(n), 10)
		s.mu.Lock()
		s.invites[ik] = t
		s.mu.Unlock()
		go t.retransmit(r, true, func() {
			s.mu.Lock()
			if s.invites[ik] == t {
				delete(s.invites, ik)
			}
			s.mu.Unlock()
		})
		return
	}
	go t.retransmit(r, false, nil)
}

// retransmit resends a final INVITE response until it is acknowledged (timers G and H, or the 2xx rule of
// RFC 3261 13.3.1.4).
func (t *ServerTx) retransmit(r *Message, success bool, cleanup func()) {
	s := t.stack
	defer func() {
		if cleanup != nil {
			cleanup()
		}
		t.done()
	}()
	interval := s.T1
	deadline := time.NewTimer(s.expire())
	defer deadline.Stop()
	for {
		var tick <-chan time.Time
		var timer *time.Timer
		if !t.From.Reliable() {
			timer = time.NewTimer(interval)
			tick = timer.C
		}
		select {
		case <-t.acked:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-deadline.C:
			if timer != nil {
				timer.Stop()
			}
			if success && t.NoAck != nil {
				t.NoAck()
			}
			return
		case <-tick:
			_ = s.Send(r, t.From)
			interval *= 2
			if interval > s.T2 {
				interval = s.T2
			}
		}
	}
}

func (t *ServerTx) done() {
	t.terminate.Do(func() {
		t.stack.mu.Lock()
		if t.stack.servers[t.key] == t {
			delete(t.stack.servers, t.key)
		}
		t.stack.mu.Unlock()
	})
}

// FindInvite returns the INVITE server transaction a CANCEL refers to (same branch), or nil.
func (s *Stack) FindInvite(cancel *Message) *ServerTx {
	v, err := ParseVia(cancel.Get("Via"))
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servers[txKey(v.Branch(), INVITE)]
}

// ClientTx is a client transaction: a request BitVoice sent and the responses to it.
type ClientTx struct {
	stack *Stack
	key   string
	Req   *Message
	To    Peer
	// Responses delivers every response, provisional and final. It is closed when the transaction ends; a
	// timeout or transport failure is delivered as a synthetic 408 or 503 first.
	Responses chan *Message

	mu          sync.Mutex
	provisional bool
	final       bool
	cancelWant  bool
	cancelSent  bool
	ack         *Message
	closed      bool
	stop        chan struct{}
	stopOnce    sync.Once
}

// Request starts a client transaction. A top Via with a fresh branch is added; the rest of the request must
// already be complete.
func (s *Stack) Request(req *Message, to Peer) *ClientTx {
	branch := NewBranch()
	via := &Via{Transport: to.Transport, Host: s.LocalHost(to), Port: s.LocalPort(to.Transport),
		Params: Params{{Name: "branch", Value: branch}, {Name: "rport", Flag: true}}}
	req.Prepend("Via", via.String())
	if req.Get("Max-Forwards") == "" {
		req.Set("Max-Forwards", "70")
	}
	return s.start(req, to, branch)
}

// requestSameBranch starts a transaction for a request whose Via is already set (CANCEL).
func (s *Stack) requestSameBranch(req *Message, to Peer, branch string) *ClientTx {
	return s.start(req, to, branch)
}

func (s *Stack) start(req *Message, to Peer, branch string) *ClientTx {
	_, method := req.CSeq()
	tx := &ClientTx{stack: s, key: txKey(branch, method), Req: req, To: to,
		Responses: make(chan *Message, 32), stop: make(chan struct{})}
	s.mu.Lock()
	s.clients[tx.key] = tx
	s.mu.Unlock()
	if err := s.Send(req, to); err != nil {
		go tx.finish(NewResponse(req, 503, "Transport Failure"))
		return tx
	}
	go tx.run()
	return tx
}

func (tx *ClientTx) run() {
	s := tx.stack
	interval := s.T1
	deadline := time.NewTimer(s.expire())
	defer deadline.Stop()
	for {
		var tick <-chan time.Time
		var timer *time.Timer
		if !tx.To.Reliable() {
			timer = time.NewTimer(interval)
			tick = timer.C
		}
		select {
		case <-tx.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-deadline.C:
			if timer != nil {
				timer.Stop()
			}
			tx.mu.Lock()
			prov := tx.provisional
			tx.mu.Unlock()
			// An INVITE that is ringing has no deadline of its own: the TU decides how long to ring.
			if !(prov && tx.Req.Method == INVITE) {
				tx.finish(NewResponse(tx.Req, 408, "Request Timeout"))
			}
			return
		case <-tick:
			tx.mu.Lock()
			prov := tx.provisional
			tx.mu.Unlock()
			if prov && tx.Req.Method == INVITE {
				// Timer A stops at the first provisional response (RFC 3261 17.1.1.2).
				return
			}
			_ = s.Send(tx.Req, tx.To)
			interval *= 2
			if interval > s.T2 || prov {
				// Timer E runs at T2 once a provisional response has arrived (17.1.2.2).
				interval = s.T2
			}
		}
	}
}

func (s *Stack) receiveResponse(in *Incoming) {
	m := in.Msg
	v, err := ParseVia(m.Get("Via"))
	if err != nil {
		return
	}
	_, method := m.CSeq()
	key := txKey(v.Branch(), method)
	s.mu.Lock()
	tx := s.clients[key]
	s.mu.Unlock()
	if tx == nil {
		return // stray response: a proxy would forward it, a B2BUA drops it
	}
	tx.handle(m)
}

func (tx *ClientTx) handle(m *Message) {
	tx.mu.Lock()
	if tx.final {
		// Retransmitted final response: re-ACK an INVITE.
		ack := tx.ack
		tx.mu.Unlock()
		if ack != nil {
			_ = tx.stack.Send(ack, tx.To)
		}
		return
	}
	if m.StatusCode < 200 {
		tx.provisional = true
		sendCancel := tx.cancelWant && !tx.cancelSent
		if sendCancel {
			tx.cancelSent = true
		}
		tx.mu.Unlock()
		if sendCancel {
			tx.sendCancel()
		}
		tx.deliver(m)
		return
	}
	tx.final = true
	tx.mu.Unlock()
	tx.stopOnce.Do(func() { close(tx.stop) })
	if tx.Req.Method == INVITE && m.StatusCode >= 300 {
		ack := tx.buildNon2xxAck(m)
		tx.mu.Lock()
		tx.ack = ack
		tx.mu.Unlock()
		_ = tx.stack.Send(ack, tx.To)
	}
	tx.deliver(m)
	tx.closeResponses()
	// Linger to absorb retransmissions of the final response (timers D and K).
	linger := tx.stack.expire()
	if tx.To.Reliable() {
		linger = 0
	}
	time.AfterFunc(linger, tx.remove)
}

func (tx *ClientTx) deliver(m *Message) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return
	}
	select {
	case tx.Responses <- m:
	default:
	}
}

func (tx *ClientTx) closeResponses() {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if !tx.closed {
		tx.closed = true
		close(tx.Responses)
	}
}

func (tx *ClientTx) finish(synthetic *Message) {
	tx.mu.Lock()
	if tx.final {
		tx.mu.Unlock()
		return
	}
	tx.final = true
	tx.mu.Unlock()
	tx.stopOnce.Do(func() { close(tx.stop) })
	tx.deliver(synthetic)
	tx.closeResponses()
	tx.remove()
}

func (tx *ClientTx) remove() {
	tx.stack.mu.Lock()
	if tx.stack.clients[tx.key] == tx {
		delete(tx.stack.clients, tx.key)
	}
	tx.stack.mu.Unlock()
}

// buildNon2xxAck builds the hop-by-hop ACK for a failure response (RFC 3261 17.1.1.3).
func (tx *ClientTx) buildNon2xxAck(resp *Message) *Message {
	r := tx.Req
	ack := &Message{IsRequest: true, Method: ACK, RequestURI: r.RequestURI}
	ack.Add("Via", r.Get("Via"))
	for _, rt := range r.GetAll("Route") {
		ack.Add("Route", rt)
	}
	ack.Set("Max-Forwards", "70")
	ack.Set("From", r.Get("From"))
	ack.Set("To", resp.Get("To"))
	ack.Set("Call-ID", r.CallID())
	n, _ := r.CSeq()
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	return ack
}

// SetAck records the ACK the TU sent for a 2xx so retransmitted 2xx responses are re-acknowledged.
func (tx *ClientTx) SetAck(ack *Message) {
	tx.mu.Lock()
	tx.ack = ack
	tx.mu.Unlock()
}

// Cancel cancels a pending INVITE. If no provisional response has arrived yet the CANCEL is sent when one
// does (RFC 3261 9.1); if the INVITE has already completed, Cancel does nothing.
func (tx *ClientTx) Cancel() {
	tx.mu.Lock()
	if tx.final || tx.cancelSent {
		tx.cancelWant = true
		tx.mu.Unlock()
		return
	}
	tx.cancelWant = true
	if !tx.provisional {
		tx.mu.Unlock()
		// No provisional yet. If none ever comes, the INVITE times out on its own; give it a bounded wait.
		go func() {
			time.Sleep(tx.stack.expire())
			tx.finish(NewResponse(tx.Req, 487, "Request Terminated"))
		}()
		return
	}
	tx.cancelSent = true
	tx.mu.Unlock()
	tx.sendCancel()
}

func (tx *ClientTx) sendCancel() {
	r := tx.Req
	c := &Message{IsRequest: true, Method: CANCEL, RequestURI: r.RequestURI}
	c.Add("Via", r.Get("Via"))
	for _, rt := range r.GetAll("Route") {
		c.Add("Route", rt)
	}
	c.Set("Max-Forwards", "70")
	c.Set("From", r.Get("From"))
	c.Set("To", r.Get("To"))
	c.Set("Call-ID", r.CallID())
	n, _ := r.CSeq()
	c.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" CANCEL")
	v, _ := ParseVia(r.Get("Via"))
	ct := tx.stack.requestSameBranch(c, tx.To, v.Branch())
	go func() {
		for range ct.Responses {
		}
	}()
}
