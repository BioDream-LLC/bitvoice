package pbx

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// fakeAI speaks a short tone, hears "what are your hours" first and "a human please" next, and
// answers from the knowledge list.
func fakeAI(t *testing.T) (*httptest.Server, *atomic.Int32) {
	return fakeAIWith(t, `{"say":"We are open nine to five.","action":"answer"}`, "What are your hours?", "A human please.")
}

// fakeAIWith answers every chat turn with reply, and hears the caller say heard, in turn (the last one
// again after that).
func fakeAIWith(t *testing.T, reply string, heard ...string) (*httptest.Server, *atomic.Int32) {
	var turns, spoke atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/audio/speech") {
			spoke.Add(1)
			pcm := make([]int16, 4000)
			for i := range pcm {
				pcm[i] = int16(3000 * math.Sin(float64(i)/3))
			}
			_, _ = w.Write(media.EncodeWAV(pcm))
			return
		}
		var in struct {
			Messages []struct {
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		answer := reply
		for _, m := range in.Messages {
			for _, p := range m.Content {
				if p.Type == "input_audio" {
					answer = heard[min(int(turns.Add(1))-1, len(heard)-1)]
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &spoke
}

func TestAIReceptionist(t *testing.T) {
	ai, spoke := fakeAI(t)
	cfg := testConfig()
	cfg.System.AI = config.AI{Enabled: true, BaseURL: ai.URL, Model: "m", SpeechModel: "tts-1"}
	cfg.Receptionists = []config.Receptionist{{ID: "r1", Name: "Front", Knowledge: "Open 9 to 5.", Person: config.Destination{Type: "extension", Target: "102"}}}
	cfg.FindExtension("101").NoAnswer = &config.Destination{Type: "receptionist", Target: "r1"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "102")
	caller, person := ps[0], ps[1]
	d, code := caller.dial("101")
	if d == nil {
		t.Fatalf("not answered: %d", code)
	}
	// Two turns: speak (a loud tone), then go quiet so the receptionist knows we finished.
	var seq uint16
	for turn := 0; turn < 2; turn++ {
		time.Sleep(900 * time.Millisecond) // the receptionist speaks
		speakFor(caller, d, 600*time.Millisecond)
		quiet := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(quiet) {
			pkt := make([]byte, 12, 172)
			pkt[0] = 0x80
			seq++
			pkt[2], pkt[3] = byte(seq>>8), byte(seq)
			for j := 0; j < 160; j++ {
				pkt = append(pkt, 0xFF) // µ-law silence
			}
			_, _ = caller.rtp.WriteToUDPAddrPort(pkt, d.media)
			time.Sleep(20 * time.Millisecond)
		}
	}
	if person.waitRing(5*time.Second) == nil {
		t.Fatalf("the person was not rung (the receptionist spoke %d times)", spoke.Load())
	}
	if spoke.Load() < 3 {
		t.Fatalf("spoke %d times", spoke.Load())
	}
}

// Pressing 0 reaches the person at once, and so does an AI service that is down.
func TestReceptionistAlwaysReachesAPerson(t *testing.T) {
	cfg := testConfig()
	cfg.System.AI = config.AI{Enabled: true, BaseURL: "http://127.0.0.1:9", Model: "m", SpeechModel: "tts-1"}
	cfg.Receptionists = []config.Receptionist{{ID: "r1", Name: "Front", Person: config.Destination{Type: "extension", Target: "102"}}}
	cfg.FindExtension("101").NoAnswer = &config.Destination{Type: "receptionist", Target: "r1"}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "102")
	if d, _ := ps[0].dial("101"); d == nil {
		t.Fatal("not answered")
	}
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("a broken AI kept the caller from a person")
	}
}

// The receptionist books a callback the caller confirmed, then says goodbye and hangs up.
func TestReceptionistBooksCallback(t *testing.T) {
	ai, _ := fakeAIWith(t, `{"say":"Thanks, someone will call you.","action":"callback","callback":{"name":"Ana Diaz","number":"call me on +1 (212) 555-0123","when":"tomorrow morning","reason":"a quote"}}`, "Please call me back tomorrow.", "Yes, book it.")
	cfg := testConfig()
	cfg.System.AI = config.AI{Enabled: true, BaseURL: ai.URL, Model: "m", SpeechModel: "tts-1"}
	cfg.Receptionists = []config.Receptionist{{ID: "r1", Name: "Front", Person: config.Destination{Type: "extension", Target: "102"}}}
	cfg.FindExtension("101").NoAnswer = &config.Destination{Type: "receptionist", Target: "r1"}
	e, _ := startEngine(t, cfg)
	got := make(chan store.Callback, 1)
	e.Callback = func(c store.Callback) error { got <- c; return nil }
	ps := registered(t, e, "100", "102")
	caller := ps[0]
	d, code := caller.dial("101")
	if d == nil {
		t.Fatalf("not answered: %d", code)
	}
	var seq uint16
	for turn := 0; turn < 2; turn++ { // asks for a callback, then says yes to the read-back
		time.Sleep(900 * time.Millisecond)
		speakFor(caller, d, 600*time.Millisecond)
		quiet := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(quiet) {
			pkt := make([]byte, 12, 172)
			pkt[0] = 0x80
			seq++
			pkt[2], pkt[3] = byte(seq>>8), byte(seq)
			for j := 0; j < 160; j++ {
				pkt = append(pkt, 0xFF)
			}
			_, _ = caller.rtp.WriteToUDPAddrPort(pkt, d.media)
			time.Sleep(20 * time.Millisecond)
		}
	}
	select {
	case c := <-got:
		if c.Name != "Ana Diaz" || c.Number != "+12125550123" || c.When != "tomorrow morning" || c.Reason != "a quote" || c.Receptionist != "Front" {
			t.Fatalf("callback %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no callback was booked")
	}
	caller.waitBye(t, 5*time.Second)
}

func TestWantsPerson(t *testing.T) {
	for s, want := range map[string]bool{
		"Person.":                          true,
		"a human please":                   true,
		"Can I talk to someone?":           true,
		"I want to speak to a real person": true,
		"operator":                         true,
		"put me through to somebody":       true,
		"Can someone call me back?":        false,
		"Is somebody there on Saturday?":   false,
		"What are your hours?":             false,
		"Has anyone found my keys?":        false,
	} {
		if got := wantsPerson.MatchString(s); got != want {
			t.Errorf("%q: got %v", s, got)
		}
	}
	if !sayYes.MatchString("Yes, that's right") || sayYes.MatchString("Tuesday") || !sayNo.MatchString("no, the number is wrong") {
		t.Error("yes/no")
	}
}
