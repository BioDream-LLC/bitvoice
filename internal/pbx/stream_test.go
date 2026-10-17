package pbx

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/audiosocket"
	"github.com/biodream-llc/bitvoice/internal/config"
)

// fakeBot is an AudioSocket server, as a voice bot written for Asterisk is.
type fakeBot struct {
	ln    net.Listener
	conns chan net.Conn
}

func newBot(t *testing.T) *fakeBot {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBot{ln: ln, conns: make(chan net.Conn, 4)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.conns <- c
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return b
}

func (b *fakeBot) accept(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	select {
	case c := <-b.conns:
		return c, bufio.NewReader(c)
	case <-time.After(15 * time.Second):
		t.Fatal("the bot was never called")
		return nil, nil
	}
}

func readUntil(t *testing.T, r *bufio.Reader, c net.Conn, kind byte) audiosocket.Frame {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		f, err := audiosocket.ReadFrame(r)
		if err != nil {
			t.Fatalf("waiting for frame %#x: %v", kind, err)
		}
		if f.Kind == kind {
			return f
		}
	}
}

// A call to an audio stream reaches the bot with the call's UUID; the caller's sound and key presses
// reach the bot, the bot's sound reaches the caller, and the bot hanging up ends the call.
func TestAudioSocketStream(t *testing.T) {
	bot := newBot(t)
	cfg := testConfig()
	cfg.AudioStreams = []config.AudioStream{{ID: "bot", Name: "Voice bot", Number: "470", Address: bot.ln.Addr().String(),
		Fallback: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	d, code := ps[0].dial("470")
	if code != 200 {
		t.Fatalf("stream answered %d", code)
	}
	c, r := bot.accept(t)
	defer c.Close()
	if f := readUntil(t, r, c, audiosocket.KindUUID); len(f.Payload) != 16 {
		t.Fatalf("UUID frame of %d bytes", len(f.Payload))
	}
	go d.sendRTP(50)
	if f := readUntil(t, r, c, audiosocket.KindAudio); len(f.Payload) != 320 {
		t.Fatalf("audio frame of %d bytes, want 320 (20 ms of 8 kHz 16-bit)", len(f.Payload))
	}
	// The bot speaks: a second of a tone.
	pcm := make([]byte, 320)
	for i := range 160 {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(8000*(i%16-8)/8)))
	}
	for range 10 {
		c.Write(audiosocket.AppendFrame(nil, audiosocket.KindAudio, pcm))
	}
	ps[0].drainRTP()
	if !ps[0].gotRTP(time.Second) {
		t.Fatal("the bot's sound did not reach the caller")
	}
	var seq uint16 = 1
	d.press(&seq, "5")
	if f := readUntil(t, r, c, audiosocket.KindDTMF); string(f.Payload) != "5" {
		t.Fatalf("key %q", f.Payload)
	}
	c.Write(audiosocket.AppendFrame(nil, audiosocket.KindHangup, nil))
	ps[0].waitBye(t, 3*time.Second)
}

// A bot that cannot be reached, or breaks off mid-call, never drops the caller: the fallback rings.
func TestAudioSocketFallback(t *testing.T) {
	bot := newBot(t)
	addr := bot.ln.Addr().String()
	cfg := testConfig()
	cfg.AudioStreams = []config.AudioStream{{ID: "bot", Name: "Voice bot", Number: "470", Address: addr,
		Fallback: config.Destination{Type: "extension", Target: "101"}},
		{ID: "gone", Name: "Gone", Number: "471", Address: "127.0.0.1:1", Fallback: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	if _, code := ps[0].dial("471"); code != 200 {
		t.Fatalf("answered %d", code)
	}
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("an unreachable bot did not hand the caller to the fallback")
	}
	ps[0].drainRTP()
	d, _ := ps[0].dial("470")
	c, r := bot.accept(t)
	readUntil(t, r, c, audiosocket.KindUUID)
	c.Close() // broken off, no hang-up
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("a bot that broke off did not hand the caller to the fallback")
	}
	_ = d
}

// External media (ARI's): an AudioSocket pipe joined to a call between two people hears them both, and
// taking it out leaves the two talking.
func TestAttachMedia(t *testing.T) {
	bot := newBot(t)
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	go func() {
		if tx := ps[1].waitRing(3 * time.Second); tx != nil {
			ps[1].answer(tx)
		}
	}()
	d, code := ps[0].dial("101")
	if code != 200 {
		t.Fatalf("call %d", code)
	}
	ep, err := e.NewMediaEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	pipe, err := audiosocket.Dial(context.Background(), bot.ln.Addr().String(), "ext1", ep)
	if err != nil {
		t.Fatal(err)
	}
	ep.Start()
	c, r := bot.accept(t)
	defer c.Close()
	readUntil(t, r, c, audiosocket.KindUUID)
	detach, err := e.AttachMedia(e.Calls()[0].ID, ep, "listen")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.AttachMedia(e.Calls()[0].ID, ep, "listen"); err == nil {
		t.Fatal("a second attachment was accepted")
	}
	go d.sendRTP(60)
	if f := readUntil(t, r, c, audiosocket.KindAudio); len(f.Payload) != 320 {
		t.Fatalf("frame %d", len(f.Payload))
	}
	detach()
	pipe.Close()
	readUntil(t, r, c, audiosocket.KindHangup)
	if len(e.Calls()) != 1 {
		t.Fatal("taking the media out ended the call")
	}
}
