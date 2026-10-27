// Package r2 is MFC-R2 signalling for one circuit of an E1: line signalling on the A and B bits of
// timeslot 16 (ITU-T Q.421, digital version) and register signalling by compelled multifrequency
// tones (Q.441): the caller's end (forward) sends the number dialled (DNIS), its category and its
// number (ANI); the called end (backward) asks for each in turn and answers whether the call is
// accepted. Countries change which signal means what; Variant holds those assignments.
//
// A Chan is driven by what its IO hears (Bits, Tone) and by its user (Dial, Accept, Reject, Answer,
// Hangup); it says what happens through Events. Everything runs on one goroutine per channel.
package r2

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Line signals: the A and B bits (in the ABCD nibble, A=8 B=4); C and D are the variant's fixed bits.
const (
	casIdle          = 0x8 // 10
	casBlock         = 0xC // 11
	casSeize         = 0x0 // 00
	casSeizeAck      = 0xC // 11
	casClearBack     = 0xC // 11
	casForcedRelease = 0x0 // 00
	casClearForward  = 0x8 // 10
	casAnswer        = 0x4 // 01
	casR2Bits        = 0xC
)

// Causes a call ends with.
type Cause int

const (
	Normal Cause = iota
	Busy
	Congestion
	Unallocated
	OutOfOrder
	NumberChanged
	NoAnswer
	ForcedRelease
	Glare
	ProtocolError
)

var causeNames = map[Cause]string{Normal: "normal clearing", Busy: "busy", Congestion: "congestion", Unallocated: "unallocated number",
	OutOfOrder: "line out of order", NumberChanged: "number changed", NoAnswer: "no answer", ForcedRelease: "forced release",
	Glare: "both ends seized the circuit at once", ProtocolError: "protocol error"}

func (c Cause) String() string { return causeNames[c] }

// Categories of caller (Group II), as BitVoice names them.
const (
	CategoryNational = iota
	CategoryNationalPriority
	CategoryInternational
	CategoryInternationalPriority
	CategoryCollect
	CategoryTest
	CategoryPayPhone
)

// Timers (ms, as Q.441 and common practice set them).
var (
	SeizeTimeout   = 2 * time.Second // seized, no seize acknowledge
	BackCycle      = 2 * time.Second // backward: waiting for the forward signal after asking
	BackResume     = 150 * time.Millisecond
	ForwardSafety  = 30 * time.Second // forward: waiting for the backward side to time out
	AnswerTimeout  = 2 * time.Minute  // accepted, not answered
	AnswerDelay    = 150 * time.Millisecond
	CallDown       = 3 * time.Second // clear forward sent, the far end never back to idle
	ToneOffTimeout = 5 * time.Second // compelled: a tone not answered at all
)

// IO is what a channel does to the line.
type IO interface {
	SetBits(abcd int) // the four signalling bits sent
	SetTone(n int)    // the MF signal sent (forward set when dialling out, backward when answering; 0 for none)
}

// Events is what a channel tells its user. Each is called on the channel's goroutine.
type Events struct {
	Offered      func(ani, dnis string, category int, restricted bool) // backward: accept or reject it
	Accepted     func(charge bool)                                     // forward: the far end accepted the call
	Answered     func()
	Disconnected func(Cause) // the far end ended the call, or it failed; Hangup completes it
	Ended        func()      // the circuit is idle again
	Blocked      func(bool)  // the far end blocked or unblocked the circuit
}

type direction int

const (
	dirNone direction = iota
	dirForward
	dirBackward
)

type mfGroup int

const (
	gNone mfGroup = iota
	gBackInit
	gA
	gB
	gC
	gI // forward: answering Group A
	gII
	gIII
)

type mfState int

const (
	mfOff mfState = iota
	mfSeizeAckTxd
	mfDNISRqTxd
	mfCategoryRqTxd
	mfANIRqTxd
	mfChgGIITxd
	mfAcceptedTxd
	mfDisconnectTxd
	mfDNISTxd
	mfDNISEndTxd
	mfWaitingTimeout
	mfCategoryTxd
	mfANITxd
	mfANIEndTxd
)

type lineState int

const (
	lsIdle lineState = iota
	lsBlocked
	lsSeizeTxd
	lsSeizeAckRxd
	lsAcceptRxd
	lsAnswerRxdMFPending
	lsAnswerRxd
	lsClearBackRxd
	lsForcedReleaseRxd
	lsClearBackToneRxd
	lsClearFwdTxd
	lsClearBackAfterClearFwdRxd
	lsSeizeAckTxd
	lsAnswerTxd
	lsClearFwdRxd
	lsClearBackTxd
	lsDoubleSeizure
)

type callState int

const (
	csIdle callState = iota
	csDialling
	csCollecting
	csOffered
	csAccepted
	csAnswered
	csDisconnected
)

// Chan is one MFC-R2 circuit.
type Chan struct {
	v   *Variant
	io  IO
	ev  Events
	in  chan func()
	ctx chan struct{}

	// MaxDNIS ends the number after this many digits (0: wait for the end signal or the timeout);
	// MaxANI is how many digits of the caller's number to ask for.
	MaxDNIS int
	MaxANI  int

	casRead   int
	line      lineState
	call      callState
	dir       direction
	group     mfGroup
	state     mfState
	writeTone int
	readTone  int

	dnis, ani     []byte
	dnisIdx       int
	aniIdx        int
	aniGiven      bool // forward: the caller's number may be sent
	restricted    bool
	category      int // the Group II signal
	categorySent  bool
	kd            int  // China: the KD (Group II) signal received, which says how to read Group B
	kaPriority    bool // China: the caller's KA said priority
	timers        map[string]*time.Timer
	timerGen      map[string]int
	answeredFlag  bool
	disconnectReq bool
	answerPending bool // answered while the accept was still being signalled
}

// New makes a channel; Start sets it idle.
func New(v *Variant, io IO, ev Events) *Chan {
	c := &Chan{v: v, io: io, ev: ev, in: make(chan func(), 64), ctx: make(chan struct{}), MaxANI: 15,
		timers: map[string]*time.Timer{}, timerGen: map[string]int{}, casRead: -1}
	go c.loop()
	return c
}

func (c *Chan) loop() {
	for {
		select {
		case f := <-c.in:
			f()
		case <-c.ctx:
			for _, t := range c.timers {
				t.Stop()
			}
			return
		}
	}
}

func (c *Chan) do(f func()) {
	select {
	case c.in <- f:
	case <-c.ctx:
	}
}

// Close stops the channel.
func (c *Chan) Close() {
	select {
	case <-c.ctx:
	default:
		close(c.ctx)
	}
}

// Start puts the circuit idle.
func (c *Chan) Start() { c.do(c.setIdle) }

// Bits takes the signalling bits the far end sends.
func (c *Chan) Bits(abcd int) { c.do(func() { c.handleCAS(abcd & casR2Bits) }) }

// Tone takes a change in the MF signal heard: n starting, 0 for silence.
func (c *Chan) Tone(n int) { c.do(func() { c.handleTone(n) }) }

// Forward says whether the channel is placing a call (and so hears backward signals).
func (c *Chan) Forward() bool {
	ch := make(chan bool, 1)
	c.do(func() { ch <- c.dir == dirForward })
	select {
	case v := <-ch:
		return v
	case <-c.ctx:
		return false
	}
}

// ErrBusy is returned when the circuit cannot place a call now.
var ErrBusy = errors.New("r2: the circuit is not idle")

// Dial places a call: seize, then the number, the category and the caller's number as asked.
// ani "" with restricted sends the variant's "caller's number withheld" signal.
func (c *Chan) Dial(ani, dnis string, category int, restricted bool) error {
	res := make(chan error, 1)
	c.do(func() { res <- c.dial(ani, dnis, category, restricted) })
	select {
	case err := <-res:
		return err
	case <-c.ctx:
		return ErrBusy
	}
}

// Accept accepts an offered call (with charge or not); Answer then answers it.
func (c *Chan) Accept(charge bool) { c.do(func() { c.accept(charge) }) }

// Reject refuses an offered call with a cause (busy, unallocated, congestion, out of order).
func (c *Chan) Reject(cause Cause) { c.do(func() { c.reject(cause) }) }

// Answer answers an accepted call.
func (c *Chan) Answer() { c.do(c.answer) }

// Hangup ends the call (or completes the far end's ending of it).
func (c *Chan) Hangup() { c.do(c.hangup) }

// --- timers

func (c *Chan) after(name string, d time.Duration, f func()) {
	c.cancel(name)
	c.timerGen[name]++
	gen := c.timerGen[name]
	c.timers[name] = time.AfterFunc(d, func() {
		c.do(func() {
			if c.timerGen[name] == gen && c.timers[name] != nil {
				delete(c.timers, name)
				f()
			}
		})
	})
}

func (c *Chan) cancel(name string) {
	if t := c.timers[name]; t != nil {
		t.Stop()
		delete(c.timers, name)
	}
	c.timerGen[name]++
}

func (c *Chan) cancelAll() {
	for n := range c.timers {
		c.cancel(n)
	}
}

// --- line signals

func (c *Chan) setCAS(abcd int) { c.io.SetBits(abcd | c.v.CDBits) }

func (c *Chan) init() {
	c.cancelAll()
	c.dnis, c.ani = nil, nil
	c.dnisIdx, c.aniIdx = 0, 0
	c.restricted, c.aniGiven = false, false
	c.category, c.kd, c.kaPriority = 0, 0, false
	c.categorySent = false
	c.line, c.call, c.dir = lsIdle, csIdle, dirNone
	c.offMF()
	c.group = gNone
	c.writeTone, c.readTone = 0, 0
	c.answeredFlag, c.disconnectReq, c.answerPending = false, false, false
}

func (c *Chan) setIdle() {
	c.init()
	c.setCAS(casIdle)
}

func (c *Chan) offMF() {
	if c.state == mfOff {
		return
	}
	c.cancel("backCycle")
	if c.writeTone != 0 {
		c.io.SetTone(0)
		c.writeTone = 0
	}
	c.state = mfOff
}

func (c *Chan) disconnected(cause Cause) {
	c.call = csDisconnected
	if c.ev.Disconnected != nil {
		c.ev.Disconnected(cause)
	}
}

func (c *Chan) ended() {
	c.setIdle()
	if c.ev.Ended != nil {
		c.ev.Ended()
	}
}

func (c *Chan) protocolError() {
	dbg("PROTO", c.line, c.state, c.group, c.readTone, c.writeTone)
	// As OpenR2 does: report the call down and return to idle (after a clear forward when we seized).
	wasForward := c.dir == dirForward
	if c.call != csIdle {
		c.disconnected(ProtocolError)
	}
	if wasForward && c.line != lsIdle && c.line != lsClearFwdTxd {
		c.sendClearForward()
		return
	}
	c.ended()
}

func (c *Chan) handleCAS(cas int) {
	if cas == c.casRead {
		return
	}
	c.casRead = cas
	switch c.line {
	case lsIdle:
		switch cas {
		case casBlock:
			c.line = lsBlocked
			if c.ev.Blocked != nil {
				c.ev.Blocked(true)
			}
		case casSeize:
			c.incoming()
		}
	case lsBlocked:
		if cas == casIdle {
			c.line = lsIdle
			if c.ev.Blocked != nil {
				c.ev.Blocked(false)
			}
		}
	case lsSeizeAckTxd, lsAnswerTxd:
		if cas == casClearForward {
			c.line = lsClearFwdRxd
			c.offMF()
			c.disconnected(Normal)
			if c.disconnectReq {
				c.ended()
			}
		} else {
			c.protocolError()
		}
	case lsSeizeTxd:
		switch cas {
		case casSeizeAck:
			c.cancel("seize")
			if c.disconnectReq {
				c.sendClearForward()
				return
			}
			c.line = lsSeizeAckRxd
			c.group = gI
			c.sendDNIS(0)
		case casSeize:
			c.line = lsDoubleSeizure
			c.cancel("seize")
			c.disconnected(Glare)
		default:
			c.protocolError()
		}
	case lsDoubleSeizure:
		if cas == casClearForward || cas == casIdle {
			c.ended()
		}
	case lsClearBackTxd:
		if cas == casClearForward {
			c.ended()
		}
	case lsAcceptRxd:
		switch {
		case cas == casAnswer:
			c.cancel("answer")
			c.line, c.call = lsAnswerRxd, csAnswered
			c.offMF()
			c.answeredFlag = true
			if c.ev.Answered != nil {
				c.ev.Answered()
			}
		case cas == casClearBack:
			c.line = lsClearBackRxd
			c.disconnected(Normal)
		case cas == casForcedRelease:
			c.line = lsForcedReleaseRxd
			c.disconnected(ForcedRelease)
		default:
			c.protocolError()
		}
	case lsSeizeAckRxd:
		switch cas {
		case casAnswer:
			c.line = lsAnswerRxdMFPending // answered before the accept tone ended
		case casForcedRelease:
			c.line = lsForcedReleaseRxd
			c.disconnected(ForcedRelease)
		default:
			c.protocolError()
		}
	case lsAnswerRxdMFPending, lsAnswerRxd:
		switch cas {
		case casClearBack:
			c.line = lsClearBackRxd
			c.disconnected(Normal)
		case casForcedRelease:
			c.line = lsForcedReleaseRxd
			c.disconnected(ForcedRelease)
		default:
			c.protocolError()
		}
	case lsClearBackToneRxd, lsClearFwdTxd, lsClearBackAfterClearFwdRxd:
		if cas == casIdle {
			c.ended()
		} else if c.line == lsClearFwdTxd && (cas == casClearBack || cas == casForcedRelease) {
			c.line = lsClearBackAfterClearFwdRxd
		}
	}
}

// --- calls in (backward)

func (c *Chan) incoming() {
	c.line, c.call, c.dir = lsSeizeAckTxd, csCollecting, dirBackward
	c.state, c.group = mfSeizeAckTxd, gBackInit
	c.setCAS(casSeizeAck)
}

func (c *Chan) prepareTone(n int) {
	dbg("SEND", n)
	if c.writeTone == n {
		return
	}
	c.io.SetTone(n)
	c.writeTone = n
	if n != 0 && c.dir == dirBackward {
		c.after("backCycle", BackCycle, c.backCycleExpired)
	}
}

func (c *Chan) silence() {
	if c.writeTone != 0 {
		c.io.SetTone(0)
		c.writeTone = 0
	}
}

func (c *Chan) dnisComplete() bool { return c.MaxDNIS > 0 && len(c.dnis) >= c.MaxDNIS }

func isDigit(n int) bool { return n >= 1 && n <= 10 }

func digitOf(n int) byte {
	if n == 10 {
		return '0'
	}
	return byte('0' + n)
}

func signalOf(d byte) int {
	if d == '0' {
		return 10
	}
	return int(d - '0')
}

func (c *Chan) requestCategory() {
	if c.v.ACategory != 0 {
		c.group = gA
		c.state = mfCategoryRqTxd
		c.prepareTone(c.v.ACategory)
		return
	}
	c.group = gC
	c.state = mfCategoryRqTxd
	c.prepareTone(c.v.ACategoryAndGC)
}

func (c *Chan) requestChangeToG2() {
	tone := c.v.AChangeToG2
	if c.group == gC {
		tone = c.v.CChangeToG2
	}
	c.group, c.state = gB, mfChgGIITxd
	c.prepareTone(tone)
}

func (c *Chan) requestNextDNIS() {
	tone := c.v.ANextDNIS
	if c.group == gC {
		tone = c.v.CNextDNISChangeToGA
	}
	c.group, c.state = gA, mfDNISRqTxd
	c.prepareTone(tone)
}

func (c *Chan) receiveDNIS(n int) {
	switch {
	case isDigit(n):
		c.dnis = append(c.dnis, digitOf(n))
		if c.dnisComplete() {
			if len(c.dnis) == 1 || !c.v.GetANIFirst {
				c.requestCategory()
			} else {
				c.requestChangeToG2()
			}
		} else if len(c.dnis) == 1 && c.v.GetANIFirst {
			c.requestCategory()
		} else {
			c.requestNextDNIS()
		}
	case n == c.v.INoMoreDNIS && n != 0:
		if len(c.dnis) == 0 || !c.v.GetANIFirst {
			c.requestCategory()
		} else {
			c.requestChangeToG2()
		}
	default:
		c.protocolError()
	}
}

func (c *Chan) receiveANI(n int) {
	next := c.v.ANextANI
	if c.v.CNextANI != 0 {
		next = c.v.CNextANI
	}
	finish := func() {
		if !c.v.GetANIFirst || c.dnisComplete() {
			c.requestChangeToG2()
		} else {
			c.requestNextDNIS()
		}
	}
	switch {
	case n == 0 || isDigit(n):
		if n != 0 {
			c.ani = append(c.ani, digitOf(n))
		}
		if n == 0 || c.MaxANI > len(c.ani) || (c.v.China && len(c.ani) < 20) {
			c.state = mfANIRqTxd
			c.prepareTone(next)
		} else {
			finish()
		}
	case n == c.v.INoMoreANI || n == c.v.IANIRestricted:
		if n == c.v.IANIRestricted {
			c.restricted = true
		}
		finish()
	default:
		c.protocolError()
	}
}

// handleTone takes a change in what is heard.
func (c *Chan) handleTone(n int) {
	dbg("TONE", n, c.state, c.group, c.readTone, c.writeTone)
	if c.state == mfOff {
		return
	}
	if n != 0 {
		if c.readTone == n {
			return
		}
		if c.readTone != 0 {
			c.protocolError()
			return
		}
		c.readTone = n
		if c.dir == dirBackward {
			c.forwardTone(n)
		} else if c.dir == dirForward {
			c.cancel("fwdSafety")
			c.silence() // compelled: the backward signal heard, stop ours
		}
		return
	}
	if c.readTone == 0 {
		return
	}
	prev := c.readTone
	c.readTone = 0
	if c.dir == dirBackward {
		c.forwardSilence()
	} else if c.dir == dirForward {
		c.backwardSignal(prev)
	}
}

func (c *Chan) forwardTone(n int) {
	c.cancel("backCycle")
	switch c.group {
	case gBackInit:
		if c.state != mfSeizeAckTxd {
			c.protocolError()
			return
		}
		if c.v.GetANIFirst && c.v.ANICanComeFirst {
			c.receiveANI(n)
		} else {
			c.receiveDNIS(n)
		}
	case gA, gC:
		switch c.state {
		case mfDNISRqTxd:
			c.receiveDNIS(n)
		case mfCategoryRqTxd:
			c.category = n
			if c.v.China {
				c.kaPriority = n == c.v.KAPriority || n == 10 // KA 8 and 10: priority (regular, free)
				c.receiveANI(0)                               // A-1s ask for the caller's number until I-15
				return
			}
			if c.MaxANI > 0 {
				c.receiveANI(0)
			} else {
				c.requestChangeToG2()
			}
		case mfANIRqTxd:
			c.receiveANI(n)
		default:
			c.protocolError()
		}
	case gB:
		if c.state != mfChgGIITxd {
			c.protocolError()
			return
		}
		if c.v.China {
			c.kd, c.category = n, n
		}
		c.offer()
	default:
		c.protocolError()
	}
}

func (c *Chan) offer() {
	if c.call != csCollecting {
		c.protocolError()
		return
	}
	c.call = csOffered
	cat := c.v.categoryOf(c.category)
	if c.v.China && c.kaPriority && cat == CategoryNational {
		cat = CategoryNationalPriority
	}
	if c.ev.Offered != nil {
		c.ev.Offered(string(c.ani), string(c.dnis), cat, c.restricted)
	}
}

func (c *Chan) forwardSilence() {
	c.silence()
	if c.state == mfAcceptedTxd && (c.group == gA || c.group == gB) {
		c.offMF()
		c.call = csAccepted
		// The forward end needs a moment to hear our tone stop before the answer bits.
		c.after("answerDelay", AnswerDelay, func() {
			if c.answerPending {
				c.answerPending = false
				c.answer()
			}
		})
	}
}

func (c *Chan) backCycleExpired() {
	if c.v.INoMoreDNIS == 0 && c.group == gA && (c.state == mfDNISRqTxd || c.state == mfANIRqTxd) {
		// The forward side has run out of digits, and this variant has no signal to say so: it went
		// quiet, and we time out and go on, with a short tone (it hears no tone off from us).
		c.after("backResume", BackResume, c.silence)
		if !c.v.ANICanComeFirst && !c.v.GetANIFirst {
			c.requestCategory()
		} else {
			c.requestChangeToG2()
		}
		return
	}
	c.protocolError()
}

func (c *Chan) accept(charge bool) {
	if c.call != csOffered || c.dir != dirBackward {
		return
	}
	tone := c.v.BAcceptCharge
	if !charge && c.v.BAcceptNoCharge != 0 {
		tone = c.v.BAcceptNoCharge
	}
	if c.group == gA {
		tone = c.v.AAddressCompleteCharge
	}
	c.state = mfAcceptedTxd
	c.prepareTone(tone)
}

func (c *Chan) reject(cause Cause) {
	if c.call != csOffered || c.dir != dirBackward {
		return
	}
	if c.v.China {
		tone := kbCongestion // local KD: busy or congestion are one signal
		switch {
		case cause == Unallocated || cause == NumberChanged:
			tone = kbVacant
		case cause == Busy && !localKD(c.kd):
			tone = kbBusyLocal
		}
		c.state = mfDisconnectTxd
		c.disconnectReq = true
		c.prepareTone(tone)
		return
	}
	tone := c.v.BOutOfOrder
	switch cause {
	case Busy:
		tone = c.v.BBusy
	case Congestion:
		tone = c.v.BCongestion
	case Unallocated:
		tone = c.v.BUnallocated
	case NumberChanged:
		tone = c.v.BNumberChanged
		if tone == 0 {
			tone = c.v.BUnallocated
		}
	case OutOfOrder:
	default:
		tone = c.v.BCongestion
	}
	if tone == 0 {
		tone = c.v.BCongestion
	}
	c.state = mfDisconnectTxd
	c.disconnectReq = true
	c.prepareTone(tone)
}

func (c *Chan) answer() {
	if c.dir != dirBackward {
		return
	}
	if c.call == csOffered || (c.call == csAccepted && c.timers["answerDelay"] != nil) {
		c.answerPending = true
		return
	}
	if c.call != csAccepted {
		return
	}
	c.setCAS(casAnswer)
	c.call, c.line = csAnswered, lsAnswerTxd
	c.answeredFlag = true
}

// --- calls out (forward)

func (c *Chan) dial(ani, dnis string, category int, restricted bool) error {
	if c.call != csIdle || c.line != lsIdle || c.casRead != casIdle {
		return ErrBusy
	}
	for i := 0; i < len(dnis); i++ {
		if dnis[i] < '0' || dnis[i] > '9' {
			return errors.New("r2: the number may only hold digits")
		}
	}
	c.dnis = []byte(dnis)
	c.ani = nil
	c.aniGiven = true
	for i := 0; i < len(ani); i++ {
		if ani[i] < '0' || ani[i] > '9' {
			c.aniGiven = false
		}
	}
	if c.aniGiven {
		c.ani = []byte(ani)
	}
	if ani == "" && restricted {
		c.aniGiven = false
	}
	c.restricted = restricted
	c.category = c.v.categoryTone(category)
	c.dnisIdx, c.aniIdx = 0, 0
	c.call, c.dir, c.line = csDialling, dirForward, lsSeizeTxd
	c.state = mfSeizeAckTxd // until the first tone goes
	c.setCAS(casSeize)
	c.after("seize", SeizeTimeout, func() {
		c.disconnected(ProtocolError)
		c.sendClearForward()
	})
	return nil
}

func (c *Chan) sendDNIS(offset int) {
	switch {
	case offset < 0:
		c.dnisIdx += offset
		if c.dnisIdx < 0 {
			c.dnisIdx = 0
		}
	case offset == 1:
		c.dnisIdx++
	}
	if c.dnisIdx < len(c.dnis) {
		c.state = mfDNISTxd
		c.prepareTone(signalOf(c.dnis[c.dnisIdx]))
		return
	}
	if c.v.INoMoreDNIS != 0 && c.state != mfDNISEndTxd && c.state != mfWaitingTimeout {
		c.state = mfDNISEndTxd
		c.prepareTone(c.v.INoMoreDNIS)
		return
	}
	// No signal for "no more digits": the backward side times out.
	c.state = mfWaitingTimeout
	c.after("fwdSafety", ForwardSafety, c.protocolError)
}

func (c *Chan) sendCategory() {
	c.state = mfCategoryTxd
	if c.v.China && c.group == gI {
		// A-6: KA, the caller's class, then the caller's number on A-1s.
		c.categorySent = true
		ka := c.v.KAOrdinary
		if c.category == c.v.IINationalPriority {
			ka = c.v.KAPriority
		}
		c.prepareTone(ka)
		return
	}
	c.categorySent = true
	c.prepareTone(c.category)
}

func (c *Chan) sendANI() {
	if c.state == mfANIEndTxd && (c.v.ANextDNIS == c.v.ANextANI || c.v.ANextDNIS == c.v.CNextANI) {
		c.group = gI
		c.sendDNIS(1)
		return
	}
	switch {
	case c.v.IANIRestricted != 0 && !c.aniGiven:
		c.state = mfANIEndTxd
		c.prepareTone(c.v.IANIRestricted)
	case c.aniIdx < len(c.ani):
		c.state = mfANITxd
		c.prepareTone(signalOf(c.ani[c.aniIdx]))
		c.aniIdx++
	case c.v.INoMoreANI != 0 && (!c.restricted || c.v.IANIRestricted == 0):
		// No more digits; a variant with no "withheld" signal (China) ends a withheld number the same way.
		c.state = mfANIEndTxd
		c.prepareTone(c.v.INoMoreANI)
	case c.v.IANIRestricted != 0 && c.restricted:
		c.state = mfANIEndTxd
		c.prepareTone(c.v.IANIRestricted)
	default:
		c.state = mfWaitingTimeout
		c.after("fwdSafety", ForwardSafety, c.protocolError)
	}
}

// backwardSignal acts on a backward signal once it has ended (compelled signalling).
func (c *Chan) backwardSignal(n int) {
	switch c.group {
	case gI:
		c.groupA(n)
	case gII:
		c.groupB(n)
	case gIII:
		c.groupC(n)
	default:
		c.protocolError()
	}
}

func (c *Chan) dnisRequest(n int) bool {
	v := c.v
	switch {
	case n == v.ANextDNIS:
		c.sendDNIS(1)
	case n == v.ADNISMinus1 && n != 0:
		c.sendDNIS(-1)
	case n == v.ADNISMinus2 && n != 0:
		c.sendDNIS(-2)
	case n == v.ADNISMinus3 && n != 0:
		c.sendDNIS(-3)
	case n == v.AAllDNISAgain && n != 0:
		c.dnisIdx = 0
		c.sendDNIS(0)
	default:
		return false
	}
	return true
}

func (c *Chan) groupA(n int) {
	v := c.v
	catTone := v.ACategory
	if catTone == 0 {
		catTone = v.ACategoryAndGC
	}
	switch {
	case c.categorySent && n == v.ANextANI && n != 0 && (c.state == mfCategoryTxd || c.state == mfANITxd):
		// The caller's number is being asked for: China asks for it with A-1, as for the number dialled.
		c.sendANI()
	case c.dnisRequest(n):
	case c.categorySent && n == v.ANextANI && n != 0:
		c.sendANI()
	case n == catTone && n != 0:
		if catTone == v.ACategoryAndGC && v.ACategory == 0 {
			c.group = gIII
		}
		c.sendCategory()
	case n == v.AChangeToG2:
		c.group = gII
		c.sendCategory()
	case n == v.AAddressCompleteCharge && n != 0:
		c.acceptedTone(true)
	case n == v.ACongestion:
		c.line = lsClearBackToneRxd
		c.disconnected(Congestion)
	case n == v.AUnallocated && n != 0:
		c.line = lsClearBackToneRxd
		c.disconnected(Unallocated)
	default:
		c.protocolError()
	}
}

func (c *Chan) groupC(n int) {
	v := c.v
	switch n {
	case v.CNextANI:
		c.sendANI()
	case v.CChangeToG2:
		c.group = gII
		c.sendCategory()
	case v.CNextDNISChangeToGA:
		c.group = gI
		c.sendDNIS(1)
	case v.CCongestion:
		c.line = lsClearBackToneRxd
		c.disconnected(Congestion)
	default:
		c.protocolError()
	}
}

func (c *Chan) groupB(n int) {
	v := c.v
	if v.China {
		// What KB means depends on the KD this side sent.
		local := localKD(c.category)
		cause := ProtocolError
		switch {
		case n == kbIdle:
			c.acceptedTone(true)
			return
		case n == kbIdleCallerCt && local:
			c.acceptedTone(false)
			return
		case n == kbVacant:
			cause = Unallocated
		case n == kbCongestion && local:
			cause = Busy // called subscriber busy or congestion: a busy line to the caller
		case n == kbCongestion:
			cause = Congestion
		case (n == kbBusyLocal || n == kbBusyToll) && !local:
			cause = Busy
		}
		if cause == ProtocolError {
			c.protocolError()
			return
		}
		c.line = lsClearBackToneRxd
		c.offMF()
		c.disconnected(cause)
		return
	}
	switch {
	case n == v.BAcceptCharge:
		c.acceptedTone(true)
	case n == v.BAcceptNoCharge && n != 0, n == v.BSIT && n != 0:
		c.acceptedTone(false)
	default:
		cause := ProtocolError
		switch n {
		case v.BBusy:
			cause = Busy
		case v.BCongestion:
			cause = Congestion
		case v.BUnallocated:
			cause = Unallocated
		case v.BOutOfOrder:
			cause = OutOfOrder
		}
		if n == v.BNumberChanged && n != 0 {
			cause = NumberChanged
		}
		if cause == ProtocolError {
			c.protocolError()
			return
		}
		c.line = lsClearBackToneRxd
		c.offMF()
		c.disconnected(cause)
	}
}

func (c *Chan) acceptedTone(charge bool) {
	if c.line == lsAnswerRxdMFPending {
		c.line = lsAcceptRxd
		if c.ev.Accepted != nil {
			c.ev.Accepted(charge)
		}
		c.line, c.call = lsAnswerRxd, csAnswered
		c.offMF()
		c.answeredFlag = true
		if c.ev.Answered != nil {
			c.ev.Answered()
		}
		return
	}
	c.line = lsAcceptRxd
	c.offMF()
	c.call = csAccepted
	c.after("answer", AnswerTimeout, func() { c.disconnected(NoAnswer) })
	if c.ev.Accepted != nil {
		c.ev.Accepted(charge)
	}
}

// --- clearing

func (c *Chan) sendClearForward() {
	c.after("callDown", CallDown, c.ended)
	c.line = lsClearFwdTxd
	c.offMF()
	c.setCAS(casClearForward)
}

func (c *Chan) hangup() {
	if c.call == csIdle && c.line == lsIdle {
		return
	}
	if c.dir == dirBackward {
		switch {
		case c.call == csOffered:
			c.reject(Congestion)
		case c.line == lsClearFwdRxd:
			c.ended()
		case c.line == lsSeizeAckTxd && c.state == mfDisconnectTxd:
			// rejected already: the forward side clears
		default:
			c.line = lsClearBackTxd
			c.offMF()
			c.setCAS(casClearBack)
		}
		return
	}
	// Forward: clear forward, unless still waiting for the seize acknowledge.
	switch c.line {
	case lsSeizeTxd:
		c.disconnectReq = true
	case lsClearFwdTxd:
	case lsDoubleSeizure:
		c.setCAS(casClearForward)
	default:
		c.sendClearForward()
	}
}

var dbgOn = os.Getenv("R2GODEBUG") != ""

func dbg(a ...any) {
	if dbgOn {
		fmt.Fprintln(os.Stderr, append([]any{time.Now().Format("05.000")}, a...)...)
	}
}
