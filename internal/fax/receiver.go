package fax

import (
	"errors"
	"sync"
	"time"
)

// Page is one received page: its T.4 data as it came (most significant bit first) and how it was
// coded.
type Page struct {
	Data   []byte
	Params Params
}

// Result is a finished reception.
type Result struct {
	Pages    []Page
	RemoteID string // the sender's TSI, usually its fax number
	Rate     int    // bit/s of the last pages, as the sender's DCS chose (0 before any DCS)
	Err      error  // nil when the sender finished normally (EOP and DCN)
}

const (
	stWaitDCS = iota
	stTraining
	stWaitPage
	stPage
	stWaitDCN
	stDone
)

// Receiver is BitVoice's fax machine answering a call: it sends CED and its capabilities (DIS),
// takes the sender's choices (DCS), confirms training (CFR), takes each page and confirms it (MCF),
// until the sender says it is done (EOP, DCN). Send carries each IFP packet to the far end; the
// caller feeds every IFP packet received to Handle.
type Receiver struct {
	ID   string
	Send func(IFP)

	mu         sync.Mutex
	state      int
	params     Params
	frame      []byte
	page       []byte
	pages      []Page
	remote     string
	rate       int
	tcfBytes   int
	tcfZeros   int
	lastReply  []byte
	disTries   int
	lastHeard  time.Time
	done       chan struct{}
	err        error
	trainTimer *time.Timer
	stopped    bool
}

// NewReceiver makes a receiver whose identity (CSI) is id, usually its fax number.
func NewReceiver(id string, send func(IFP)) *Receiver {
	return &Receiver{ID: id, Send: send, done: make(chan struct{}), lastHeard: time.Now()}
}

// Done is closed when the reception has finished, well or not.
func (r *Receiver) Done() <-chan struct{} { return r.done }

// Result is what was received.
func (r *Receiver) Result() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Result{Pages: append([]Page(nil), r.pages...), RemoteID: r.remote, Rate: r.rate, Err: r.err}
}

// Start answers: CED, then the capabilities; they are sent again every few seconds until the sender
// answers, and the reception fails when nothing is heard for a while.
func (r *Receiver) Start() {
	r.Send(IFP{Indicator: true, Kind: IndCED})
	time.AfterFunc(3*time.Second, r.sendDIS)
	go r.watch()
}

func (r *Receiver) sendDIS() {
	r.mu.Lock()
	if r.state != stWaitDCS || r.stopped {
		r.mu.Unlock()
		return
	}
	r.disTries++
	if r.disTries > 6 {
		r.mu.Unlock()
		r.finish(errors.New("the other side never answered as a fax machine"))
		return
	}
	r.mu.Unlock()
	r.frames(frame(false, fcfCSI, idInfo(r.ID)...), frame(true, fcfDIS, disInfo()...))
	time.AfterFunc(4500*time.Millisecond, r.sendDIS)
}

// watch ends a reception that has gone silent (the sender hung up or the line broke).
func (r *Receiver) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
			r.mu.Lock()
			quiet := time.Since(r.lastHeard)
			st := r.state
			r.mu.Unlock()
			if quiet > 40*time.Second || st != stWaitDCS && quiet > 20*time.Second {
				r.finish(errors.New("the fax stopped part way"))
				return
			}
		}
	}
}

// frames sends HDLC frames over V.21: the preamble, then each frame with its end mark.
func (r *Receiver) frames(fs ...[]byte) {
	r.Send(IFP{Indicator: true, Kind: IndV21Preamble})
	for i, f := range fs {
		end := FieldHDLCFCSOK
		if i == len(fs)-1 {
			end = FieldHDLCFCSOKSigEnd
		}
		// T.38 carries each octet in the order its bits go down the line: the reverse of how HDLC
		// (and these frames) number them.
		r.Send(IFP{Kind: DataV21, Fields: []Field{{Type: FieldHDLCData, Data: reverseBits(f)}, {Type: end}}})
	}
}

func (r *Receiver) reply(fcf byte) {
	f := frame(true, fcf)
	r.mu.Lock()
	r.lastReply = f
	r.mu.Unlock()
	// A short pause, as a fax machine leaves before answering (T.30 5.3.2).
	time.AfterFunc(100*time.Millisecond, func() { r.frames(f) })
}

func (r *Receiver) finish(err error) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	r.state = stDone
	if err != nil && len(r.pages) > 0 && r.err == nil {
		err = errors.New(err.Error() + " after " + itoa(len(r.pages)) + " pages")
	}
	r.err = err
	if r.trainTimer != nil {
		r.trainTimer.Stop()
	}
	r.mu.Unlock()
	close(r.done)
}

// Stop ends the reception from outside (the call ended).
func (r *Receiver) Stop() {
	r.mu.Lock()
	st := r.state
	r.mu.Unlock()
	if st == stWaitDCN {
		r.finish(nil) // every page was confirmed; only the goodbye was missing
		return
	}
	r.finish(errors.New("the call ended before the fax was finished"))
}

// Handle takes one IFP packet from the far end.
func (r *Receiver) Handle(p IFP) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.lastHeard = time.Now()
	r.mu.Unlock()
	if p.Indicator {
		return // tones and training signals carry nothing for a receiving terminal
	}
	for _, f := range p.Fields {
		switch f.Type {
		case FieldHDLCData:
			r.mu.Lock()
			r.frame = append(r.frame, reverseBits(f.Data)...)
			r.mu.Unlock()
		case FieldHDLCFCSOK, FieldHDLCFCSOKSigEnd:
			r.mu.Lock()
			fr := r.frame
			r.frame = nil
			r.mu.Unlock()
			r.onFrame(fr)
		case FieldHDLCFCSBad, FieldHDLCFCSBadSigEnd:
			r.mu.Lock()
			r.frame = nil
			r.mu.Unlock()
		case FieldT4Data:
			r.onT4(f.Data, false)
		case FieldT4SigEnd:
			r.onT4(f.Data, true)
		}
	}
}

func (r *Receiver) onT4(data []byte, end bool) {
	r.mu.Lock()
	switch r.state {
	case stTraining: // TCF: a stretch of zeros the sender sends at the page rate to test the line
		r.tcfBytes += len(data)
		for _, b := range data {
			if b == 0 {
				r.tcfZeros++
			}
		}
		if !end {
			r.mu.Unlock()
			return
		}
		ok := r.tcfBytes == 0 || r.tcfZeros*10 >= r.tcfBytes*9
		r.trainDone()
		r.mu.Unlock()
		if ok {
			r.reply(fcfCFR)
		} else {
			r.reply(fcfFTT)
		}
		return
	case stWaitPage, stPage:
		r.state = stPage
		r.page = append(r.page, data...)
	}
	r.mu.Unlock()
}

// trainDone moves on from training: pages come next. Called with r.mu held.
func (r *Receiver) trainDone() {
	r.state = stWaitPage
	if r.trainTimer != nil {
		r.trainTimer.Stop()
		r.trainTimer = nil
	}
}

func (r *Receiver) onFrame(f []byte) {
	if len(f) < 3 {
		return
	}
	fcf, info := f[2]&^1, f[3:]
	r.mu.Lock()
	switch fcf {
	case fcfTSI:
		r.remote = parseID(info)
		r.mu.Unlock()
	case fcfDCS:
		r.params = parseDCS(info)
		r.rate = dcsRate(info)
		r.state, r.tcfBytes, r.tcfZeros = stTraining, 0, 0
		// With localTCF the far gateway checks training itself and sends no TCF: confirm anyway.
		r.trainTimer = time.AfterFunc(3*time.Second, func() {
			r.mu.Lock()
			if r.state != stTraining || r.tcfBytes > 0 {
				r.mu.Unlock()
				return
			}
			r.trainDone()
			r.mu.Unlock()
			r.reply(fcfCFR)
		})
		r.mu.Unlock()
	case fcfMPS, fcfEOP, fcfEOM:
		if len(r.page) > 0 {
			r.pages = append(r.pages, Page{Data: r.page, Params: r.params})
			r.page = nil
		}
		switch fcf {
		case fcfMPS:
			r.state = stWaitPage
		case fcfEOM:
			r.state = stWaitDCS
		case fcfEOP:
			r.state = stWaitDCN
		}
		r.mu.Unlock()
		r.reply(fcfMCF)
	case fcfCRP: // the sender did not understand: say it again
		last := r.lastReply
		r.mu.Unlock()
		if last != nil {
			r.frames(last)
		}
	case fcfDCN:
		st := r.state
		r.mu.Unlock()
		if st == stWaitDCN {
			r.finish(nil)
		} else {
			r.finish(errors.New("the sender gave up"))
		}
	default:
		r.mu.Unlock()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
