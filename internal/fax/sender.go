package fax

import (
	"errors"
	"sync"
	"time"
)

// A rate the sender can try, fastest first (the fallback order T.30 machines use).
type rate struct {
	bps         int
	dcs         [4]bool // DCS bits 11 to 14
	need        int     // the DIS bit that says the receiver has this modem: 14 V.17, 11 V.29, 12 V.27 ter
	long, short int     // training indicators (V.17 has a short training for pages)
	data        int
}

var rates = []rate{
	{14400, [4]bool{false, false, false, true}, 14, IndV17_14400Long, IndV17_14400Short, DataV17_14400},
	{12000, [4]bool{false, true, false, true}, 14, IndV17_12000Long, IndV17_12000Short, DataV17_12000},
	{9600, [4]bool{true, false, false, true}, 14, IndV17_9600Long, IndV17_9600Short, DataV17_9600},
	{9600, [4]bool{true, false, false, false}, 11, IndV29_9600, IndV29_9600, DataV29_9600},
	{7200, [4]bool{true, true, false, true}, 14, IndV17_7200Long, IndV17_7200Short, DataV17_7200},
	{7200, [4]bool{true, true, false, false}, 11, IndV29_7200, IndV29_7200, DataV29_7200},
	{4800, [4]bool{false, true, false, false}, 12, IndV27_4800, IndV27_4800, DataV27_4800},
	{2400, [4]bool{false, false, false, false}, 12, IndV27_2400, IndV27_2400, DataV27_2400},
}

// dcsRate is the bit rate a DCS chose, from its bits 11 to 14 (0 when they name no rate BitVoice
// knows). The two 9,600 and 7,200 codes (V.17 and V.29) give the same bit rate.
func dcsRate(dcs []byte) int {
	for _, r := range rates {
		if r.dcs == [4]bool{bit(dcs, 11), bit(dcs, 12), bit(dcs, 13), bit(dcs, 14)} {
			return r.bps
		}
	}
	return 0
}

// minScan is the receiver's minimum scan line time in milliseconds, from DIS bits 21 to 23.
func minScan(dis []byte) int {
	v := 0
	for i, n := range []int{21, 22, 23} {
		if bit(dis, n) {
			v |= 1 << uint(i)
		}
	}
	switch v {
	case 7:
		return 0
	case 1:
		return 5
	case 2:
		return 10
	case 4:
		return 40
	}
	return 20 // the T.30 default, and the safe choice for codes this sender does not use
}

// SendResult is a finished transmission.
type SendResult struct {
	Pages    int    // pages the receiver confirmed
	RemoteID string // the receiver's CSI, usually its fax number
	Rate     int    // bit/s the pages went at
	Err      error  // nil when every page was confirmed
}

// Sender is BitVoice's fax machine calling out: it waits for the receiver's capabilities (DIS),
// chooses the fastest rate both have (DCS), trains (TCF), falls back to slower rates when training
// fails, sends each page and waits for it to be confirmed (MCF), and says goodbye (DCN). Send
// carries each IFP packet to the far end; every IFP packet received goes to Handle.
type Sender struct {
	ID    string
	Pages []Bitmap
	Send  func(IFP)

	mu      sync.Mutex
	frame   []byte
	in      chan []byte
	done    chan struct{}
	result  SendResult
	stopped bool
	stop    chan struct{}
}

// NewSender makes a sender with identity (TSI) id for these pages.
func NewSender(id string, pages []Bitmap, send func(IFP)) *Sender {
	return &Sender{ID: id, Pages: pages, Send: send, in: make(chan []byte, 16), done: make(chan struct{}), stop: make(chan struct{})}
}

// Done is closed when the transmission has finished, well or not.
func (s *Sender) Done() <-chan struct{} { return s.done }

// Result is what was sent.
func (s *Sender) Result() SendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// Start begins: the calling tone, then the T.30 procedure.
func (s *Sender) Start() { go s.run() }

// Stop ends the transmission from outside (the call ended).
func (s *Sender) Stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stop)
	}
	s.mu.Unlock()
}

// Handle takes one IFP packet from the far end.
func (s *Sender) Handle(p IFP) {
	if p.Indicator {
		return
	}
	for _, f := range p.Fields {
		switch f.Type {
		case FieldHDLCData:
			s.mu.Lock()
			s.frame = append(s.frame, reverseBits(f.Data)...)
			s.mu.Unlock()
		case FieldHDLCFCSOK, FieldHDLCFCSOKSigEnd:
			s.mu.Lock()
			fr := s.frame
			s.frame = nil
			s.mu.Unlock()
			if len(fr) >= 3 {
				select {
				case s.in <- fr:
				default:
				}
			}
		case FieldHDLCFCSBad, FieldHDLCFCSBadSigEnd:
			s.mu.Lock()
			s.frame = nil
			s.mu.Unlock()
		}
	}
}

var errStopped = errors.New("the call ended before the fax was sent")

// next waits for the next frame from the receiver.
func (s *Sender) next(wait time.Duration) ([]byte, error) {
	select {
	case f := <-s.in:
		return f, nil
	case <-time.After(wait):
		return nil, nil
	case <-s.stop:
		return nil, errStopped
	}
}

func (s *Sender) finish(err error) {
	s.mu.Lock()
	s.result.Err = err
	s.mu.Unlock()
	close(s.done)
}

// command sends frames and waits for one of the wanted answers, saying it again up to three times
// when nothing comes (T.30 timer T4).
func (s *Sender) command(fs [][]byte, then func(), want ...byte) (byte, []byte, error) {
	for try := 0; try < 3; try++ {
		sendFrames(s.Send, fs...)
		if then != nil {
			then()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			f, err := s.next(time.Until(deadline))
			if err != nil {
				return 0, nil, err
			}
			if f == nil {
				break
			}
			fcf := f[2] &^ 1
			if fcf == fcfDCN {
				return fcf, nil, errors.New("the receiving machine hung up")
			}
			if fcf == fcfCRP { // it did not understand: say it again at once
				break
			}
			for _, w := range want {
				if fcf == w {
					return fcf, f[3:], nil
				}
			}
		}
	}
	return 0, nil, errors.New("the receiving machine stopped answering")
}

// x marks a frame as sent by the station that received DIS (T.30 5.3.6.1).
func x(fcf byte) byte { return fcf | 1 }

func (s *Sender) run() {
	s.Send(IFP{Indicator: true, Kind: IndCNG})
	// Phase B: wait for the receiver's capabilities (T.30 timer T1, 35 s).
	var dis []byte
	deadline := time.Now().Add(35 * time.Second)
	for dis == nil {
		if time.Now().After(deadline) {
			s.finish(errors.New("no fax machine answered"))
			return
		}
		f, err := s.next(time.Until(deadline))
		if err != nil {
			s.finish(err)
			return
		}
		if f == nil {
			continue
		}
		switch f[2] &^ 1 {
		case fcfCSI:
			s.mu.Lock()
			s.result.RemoteID = parseID(f[3:])
			s.mu.Unlock()
		case fcfDIS:
			dis = append([]byte(nil), f[3:]...)
		}
	}
	if !bit(dis, 10) {
		s.finish(errors.New("the other machine cannot receive faxes"))
		return
	}
	fine := bit(dis, 15) && len(s.Pages) > 0 && s.Pages[0].Fine // fine only when the pages are
	scan := minScan(dis)

	i := 0
	train := func() (rate, error) {
		for ; i < len(rates); i++ {
			r := rates[i]
			if !bit(dis, r.need) {
				continue
			}
			dcs := make([]byte, 3)
			setBit(dcs, 10) // receive
			for k, on := range r.dcs {
				if on {
					setBit(dcs, 11+k)
				}
			}
			if fine {
				setBit(dcs, 15)
			}
			code := map[int]int{0: 7, 5: 1, 10: 2, 20: 0, 40: 4}[scan] // the scan time used, as DCS codes it
			for k, n := range []int{21, 22, 23} {
				if code&(1<<uint(k)) != 0 {
					setBit(dcs, n)
				}
			}
			fcf, _, err := s.command([][]byte{frame(false, x(fcfTSI), idInfo(s.ID)...), frame(true, x(fcfDCS), dcs...)},
				func() { s.tcf(r) }, fcfCFR, fcfFTT)
			if err != nil {
				return r, err
			}
			if fcf == fcfCFR {
				return r, nil
			}
			// FTT: the line did not carry this rate; try the next slower one.
		}
		return rate{}, errors.New("the line was too poor for any fax rate")
	}
	r, err := train()
	if err != nil {
		s.finish(err)
		return
	}
	s.mu.Lock()
	s.result.Rate = r.bps
	s.mu.Unlock()

	for n := 0; n < len(s.Pages); {
		p := s.Pages[n]
		if p.Fine && !fine { // the receiver takes standard resolution only: every other line
			half := p
			half.Rows = nil
			for y := 0; y < len(p.Rows); y += 2 {
				half.Rows = append(half.Rows, p.Rows[y])
			}
			p = half
		} else if !p.Fine && fine { // a standard page in a fine document: each line twice
			double := p
			double.Rows = nil
			for _, row := range p.Rows {
				double.Rows = append(double.Rows, row, row)
			}
			p = double
		}
		minBits := scan * r.bps / 1000
		data := EncodeMH(p, minBits)
		post := byte(fcfMPS)
		if n == len(s.Pages)-1 {
			post = fcfEOP
		}
		if err := s.page(r, data); err != nil {
			s.finish(err)
			return
		}
		fcf, _, err := s.command([][]byte{frame(true, x(post))}, nil, fcfMCF, fcfRTP, fcfRTN)
		if err != nil {
			s.finish(err)
			return
		}
		switch fcf {
		case fcfMCF, fcfRTP:
			n++
			s.mu.Lock()
			s.result.Pages = n
			s.mu.Unlock()
			if fcf == fcfRTP && n < len(s.Pages) { // confirmed, but retrain before the next page
				if r, err = train(); err != nil {
					s.finish(err)
					return
				}
			}
		case fcfRTN: // not good enough: retrain (slower if need be) and send the page again
			i++
			if r, err = train(); err != nil {
				s.finish(err)
				return
			}
		}
	}
	sendFrames(s.Send, frame(true, x(fcfDCN)))
	s.finish(nil)
}

// tcf sends the training check: 1.5 seconds of zeros at the chosen rate, after the long training.
func (s *Sender) tcf(r rate) {
	time.Sleep(75 * time.Millisecond) // T.30: 75 ms between the V.21 frames and the high-speed signal
	s.Send(IFP{Indicator: true, Kind: r.long})
	s.stream(r, make([]byte, r.bps*3/2/8))
}

// page sends one page of T.4 data at the chosen rate.
func (s *Sender) page(r rate, data []byte) error {
	time.Sleep(75 * time.Millisecond)
	s.Send(IFP{Indicator: true, Kind: r.short})
	s.stream(r, data)
	time.Sleep(75 * time.Millisecond)
	select {
	case <-s.stop:
		return errStopped
	default:
		return nil
	}
}

// stream sends data paced at the modem's rate, so the far gateway's buffer never overflows, and
// ends the signal.
func (s *Sender) stream(r rate, data []byte) {
	const tick = 40 * time.Millisecond
	chunk := r.bps / 8 * int(tick/time.Millisecond) / 1000
	if chunk > 300 {
		chunk = 300
	}
	next := time.Now()
	for len(data) > 0 {
		n := chunk
		if n > len(data) {
			n = len(data)
		}
		t := FieldT4Data
		if n == len(data) {
			t = FieldT4SigEnd
		}
		s.Send(IFP{Kind: r.data, Fields: []Field{{Type: t, Data: data[:n]}}})
		data = data[n:]
		next = next.Add(tick)
		select {
		case <-s.stop:
			return
		case <-time.After(time.Until(next)):
		}
	}
	s.Send(IFP{Indicator: true, Kind: IndNoSignal})
}

// sendFrames sends HDLC frames over V.21: the preamble, then each frame with its end mark. T.38
// carries each octet in the order its bits go down the line: the reverse of how HDLC numbers them.
func sendFrames(send func(IFP), fs ...[]byte) {
	send(IFP{Indicator: true, Kind: IndV21Preamble})
	for i, f := range fs {
		end := FieldHDLCFCSOK
		if i == len(fs)-1 {
			end = FieldHDLCFCSOKSigEnd
		}
		send(IFP{Kind: DataV21, Fields: []Field{{Type: FieldHDLCData, Data: reverseBits(f)}, {Type: end}}})
	}
}
