package pbx

import (
	"math"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Radio channel recording: every transmission on every channel, heard or sent, becomes a recording
// and a line in the radio log (who, when, how long, emergency, encryption), the way a dispatch
// centre's logging recorder keeps radio traffic next to its calls.

// RadioLog stores the radio log. *store.Store implements it.
type RadioLog interface {
	AddTransmission(store.Transmission) error
}

// How a heard transmission is cut: it starts with the first packet loud enough to be speech and
// ends after rxHangTime without one; neither kind runs longer than maxTransmission.
const (
	rxHangTime      = 1500 * time.Millisecond
	maxTransmission = 10 * time.Minute
	voiceLevel      = 300 // rms of a 20 ms packet that counts as someone talking
)

// xmit is a transmission being recorded.
type xmit struct {
	t         store.Transmission
	track     *media.Track
	lastVoice time.Time
}

// chRec is a channel's recorder: at most one heard and one sent transmission at a time.
type chRec struct {
	rx, tx *xmit
}

// rmsOf is the level of an RTP packet's audio.
func rmsOf(dec *media.Decoders, pkt []byte) float64 {
	if len(pkt) <= 12 || !media.Transcodable(int(pkt[1]&0x7f)) {
		return 0
	}
	s := dec.Narrowband(int(pkt[1]&0x7f), pkt[12:])
	if len(s) == 0 {
		return 0
	}
	var e float64
	for _, v := range s {
		e += float64(v) * float64(v)
	}
	return math.Sqrt(e / float64(len(s)))
}

// recordChannel hooks a channel's endpoint to the recorder: what it receives is heard, what the
// console sends it is sent.
func (e *Engine) recordChannel(r *radio, id string, ep *media.Endpoint) {
	var rxDec, txDec media.Decoders
	ep.Record(func(pkt []byte) { e.radioAudio(r, id, false, pkt, &rxDec) })
	ep.RecordSent(func(pkt []byte) { e.radioAudio(r, id, true, pkt, &txDec) })
}

func (e *Engine) radioAudio(r *radio, id string, sent bool, pkt []byte, dec *media.Decoders) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.record {
		return
	}
	ch := r.channels[id]
	if ch == nil {
		return
	}
	if ch.rec == nil {
		ch.rec = &chRec{}
	}
	now := time.Now()
	slot := &ch.rec.rx
	if sent {
		slot = &ch.rec.tx
		if ch.tx == "" {
			return // the console sends silence to a P25 talkgroup between transmissions
		}
	}
	loud := rmsOf(dec, pkt) >= voiceLevel
	x := *slot
	if x == nil {
		if !loud && !sent {
			return
		}
		x = &xmit{track: media.NewTrack(now.Add(-20*time.Millisecond), int(maxTransmission/time.Second)),
			t: store.Transmission{ID: config.NewID(), Channel: id, ChannelName: ch.cfg.Name, Start: now.UTC(), Private: ch.private}}
		if sent {
			x.t.Direction = "tx"
			if p := r.positions[ch.tx]; p != nil {
				x.t.Dispatcher = p.ext
			}
		} else {
			x.t.Direction = "rx"
		}
		*slot = x
	}
	if loud {
		x.lastVoice = now
	}
	r.noteTransmission(ch, x)
	x.track.Packet(pkt)
}

// noteTransmission adds what the channel knows now: the talking unit, emergency, encryption, a tone.
func (r *radio) noteTransmission(ch *radioChannel, x *xmit) {
	if c := ch.p25; c != nil && x.t.Direction == "rx" && c.talker != "" {
		x.t.Unit = strings.TrimSuffix(c.talker, " EMERGENCY")
		x.t.UnitName = c.talkerName
		x.t.Emergency = x.t.Emergency || strings.HasSuffix(c.talker, " EMERGENCY")
		x.t.Encryption = c.enc
	}
	if c := ch.p25; c != nil && x.t.Direction == "tx" {
		x.t.Encryption = c.txEnc
	}
	if ch.private && x.t.Unit == "" {
		x.t.Unit = itoaInt(int(ch.unit))
	}
	if ch.emergency {
		x.t.Emergency = true
	}
	if ch.tone && x.t.Direction == "tx" {
		x.t.Tone = ch.toneKind
	}
}

// radioRecorder ends transmissions that are over and saves them, until the engine stops.
func (e *Engine) radioRecorder(r *radio) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-e.ctx.Done():
			e.flushRadio(r, true)
			return
		case <-tick.C:
			e.flushRadio(r, false)
		}
	}
}

// flushRadio saves every finished transmission (all of them when stopping).
func (e *Engine) flushRadio(r *radio, all bool) {
	now := time.Now()
	var done []*xmit
	r.mu.Lock()
	for _, ch := range r.channels {
		if ch.rec == nil {
			continue
		}
		if x := ch.rec.rx; x != nil && (all || now.Sub(x.lastVoice) > rxHangTime || now.Sub(x.t.Start) > maxTransmission) {
			done, ch.rec.rx = append(done, x), nil
		}
		if x := ch.rec.tx; x != nil && (all || ch.tx == "" || now.Sub(x.t.Start) > maxTransmission) {
			done, ch.rec.tx = append(done, x), nil
		}
	}
	r.mu.Unlock()
	for _, x := range done {
		e.saveTransmission(x, now)
	}
}

func (e *Engine) saveTransmission(x *xmit, now time.Time) {
	pcm := x.track.Samples()
	if x.t.Direction == "rx" { // the hang time after the last word is not kept
		end := x.lastVoice.Add(200 * time.Millisecond)
		if n := int(end.Sub(x.t.Start).Seconds() * media.Rate); n > 0 && n < len(pcm) {
			pcm = pcm[:n]
		}
		now = end
	}
	if len(pcm) < media.Rate/10 {
		return // under a tenth of a second: a click, not a transmission
	}
	x.t.End = now.UTC()
	x.t.Seconds = math.Round(float64(len(pcm))/media.Rate*10) / 10
	if e.Recordings != nil {
		if name, err := e.Recordings.AddRecording("radio-"+x.t.Channel+"-"+x.t.Direction, x.t.Start, media.EncodeWAV(pcm)); err == nil {
			x.t.Recording = name
		} else {
			e.Log.Error("could not save a radio recording", "channel", x.t.ChannelName, "err", err)
		}
	}
	if l, ok := e.Recorder.(RadioLog); ok {
		if err := l.AddTransmission(x.t); err != nil {
			e.Log.Error("could not save to the radio log", "channel", x.t.ChannelName, "err", err)
		}
	}
	e.publish("radio.transmission", x.t)
}
