package pbx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// TestPhoneReplay sends calls shaped exactly as different phones and softphones send them (testdata/phones:
// compact headers, user=phone, Route sets, Remote-Party-ID, ICE attributes, Opus first, odd ptimes) and
// checks each is answered by the echo test in G.711 and gets its audio back.
func TestPhoneReplay(t *testing.T) {
	files, _ := filepath.Glob("testdata/phones/*.sip")
	if len(files) == 0 {
		t.Fatal("no recordings")
	}
	e, _ := startEngine(t, testConfig())
	p := registered(t, e, "100")[0]
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, _ := os.ReadFile(f)
			text := strings.NewReplacer(
				"{{host}}", "127.0.0.1", "{{port}}", strconv.Itoa(e.SIPPort()),
				"{{me}}", "127.0.0.1:"+strconv.Itoa(p.stack.Port()),
				"{{rtp}}", strconv.Itoa(p.rtpPort()), "{{rtcp}}", strconv.Itoa(p.rtpPort()+1),
				"{{rand}}", sip.NewTag(),
			).Replace(string(raw))
			// The recordings carry a placeholder length; give the real one, as the phone would.
			head, body, _ := strings.Cut(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
			var lines []string
			for _, l := range strings.Split(head, "\n") {
				if k := strings.ToLower(strings.SplitN(l, ":", 2)[0]); k != "content-length" && k != "l" {
					lines = append(lines, l)
				}
			}
			body = strings.ReplaceAll(strings.TrimRight(body, "\n")+"\n", "\n", "\r\n")
			lines = append(lines, "Content-Length: "+strconv.Itoa(len(body)))
			text = strings.Join(lines, "\r\n") + "\r\n\r\n" + body
			m, err := sip.Parse([]byte(text))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			m.Del("Via") // the test stack adds its own
			m.Del("Route")
			r := p.send(m)
			if r == nil || r.StatusCode != 200 {
				t.Fatalf("answer: %v", r)
			}
			ans, err := sdp.Parse(r.Body)
			if err != nil || ans.Audio() == nil {
				t.Fatalf("answer SDP: %v", err)
			}
			if c := ans.Audio().Codecs()[0].Name; c != "PCMU" && c != "PCMA" {
				t.Fatalf("answered with %s", c)
			}
			to := r.Get("To")
			ct, _ := sip.ParseAddress(r.Get("Contact"))
			d := &dialog{p: p, callID: m.CallID(), localURI: m.Get("From"), remoteURI: to, target: ct.URI.String(), seq: 1,
				media: mediaOf(r.Body)}
			n, _ := r.CSeq()
			ack := d.request(sip.ACK)
			ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
			_ = p.stack.Stateless(ack, p.pbx)
			d.seq = n
			p.drainRTP()
			d.sendRTP(3)
			if !p.gotRTP(time.Second) {
				t.Fatal("no echo")
			}
			if r := p.send(d.request("BYE")); r == nil || r.StatusCode != 200 {
				t.Fatalf("BYE: %v", r)
			}
		})
	}
}
