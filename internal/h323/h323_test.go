package h323

import (
	"encoding/hex"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func vector(t *testing.T, name string) []byte {
	t.Helper()
	h, err := os.ReadFile("testdata/" + name + ".hex")
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(h)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The vectors were encoded by an independent ASN.1 PER encoder (asn1tools 0.166.0) from the
// H.225.0 version 7 and H.245 modules, with what real gateways put in a Setup: an H.245 address,
// vendor non-standard data, several alias kinds, call services, and Fast Connect proposals of G.729,
// G.723.1 and both G.711 laws.
func TestDecodesAnotherEncodersSetup(t *testing.T) {
	u, err := decodeUU(vector(t, "setup"))
	if err != nil {
		t.Fatal(err)
	}
	if u.body != bodySetup || strings.Join(u.callerAlias, ",") != "4105551234,Front Desk" || strings.Join(u.calledAlias, ",") != "201" {
		t.Fatalf("%+v", u)
	}
	if hex.EncodeToString(u.callID) != "6465666768696a6b6c6d6e6f70717273" || len(u.confID) != 16 || u.confID[15] != 15 {
		t.Fatalf("ids %x %x", u.callID, u.confID)
	}
	if len(u.fastStart) != 7 {
		t.Fatalf("%d proposals", len(u.fastStart))
	}
	c := &call{tr: &trunk{t: Trunk{Codecs: []string{"PCMU", "PCMA"}}}}
	if !c.takeProposals(u.fastStart) || c.codec != g711Alaw64k || c.peerRTP != netip.MustParseAddrPort("10.0.0.7:5004") || c.props[0].number != 5 || c.props[1].number != 6 {
		t.Fatalf("took %d at %v (%+v)", c.codec, c.peerRTP, c.props)
	}
	c = &call{tr: &trunk{t: Trunk{Codecs: []string{"PCMU"}}}}
	if !c.takeProposals(u.fastStart) || c.codec != g711Ulaw64k || c.props[1].number != 8 {
		t.Fatal("a µ-law-only trunk did not take the µ-law pair")
	}
	for i, b := range u.fastStart[:3] {
		if _, err := decodeOLC(b); err == nil {
			t.Errorf("proposal %d (not G.711) was taken", i)
		}
	}
}

func TestDecodesAnotherEncodersAnswers(t *testing.T) {
	u, err := decodeUU(vector(t, "connect"))
	if err != nil || u.body != bodyConnect || len(u.fastStart) != 2 {
		t.Fatalf("%+v %v", u, err)
	}
	c := &call{}
	c.learn(u.fastStart)
	if c.peerRTP != netip.MustParseAddrPort("10.0.0.9:6000") || c.codec != g711Ulaw64k {
		t.Fatalf("learned %v %d", c.peerRTP, c.codec)
	}
	u, err = decodeUU(vector(t, "release"))
	if err != nil || u.body != bodyReleaseComplete || u.reason != reasonDestinationRejection || sipFor(u.reason, nil) != 603 {
		t.Fatalf("%+v %v", u, err)
	}
}

// What BitVoice writes reads back; the same messages were also decoded by the independent decoder.
func TestRoundTrip(t *testing.T) {
	eng := netip.MustParseAddrPort("10.1.2.3:40000")
	tx := encodeOLC(&olc{number: 1, codec: g711Ulaw64k, frames: 20, session: 1, control: netip.MustParseAddrPort("10.1.2.3:40001")})
	rx := encodeOLC(&olc{number: 2, reverse: true, codec: g711Ulaw64k, frames: 20, session: 1, media: eng})
	b := encodeUU(&uuie{body: bodySetup, callerAlias: []string{"100", "Ann Smith"}, calledAlias: []string{"2125550199"}, confID: make([]byte, 16), callID: []byte("0123456789abcdef"), fastStart: [][]byte{tx, rx}})
	u, err := decodeUU(b)
	if err != nil || u.body != bodySetup || strings.Join(u.callerAlias, ",") != "100,Ann Smith" || u.calledAlias[0] != "2125550199" || string(u.callID) != "0123456789abcdef" || len(u.fastStart) != 2 {
		t.Fatalf("%+v %v", u, err)
	}
	o, err := decodeOLC(u.fastStart[1])
	if err != nil || !o.reverse || o.media != eng || o.number != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	for _, body := range []int{bodyCallProceeding, bodyAlerting, bodyConnect, bodyReleaseComplete} {
		u, err := decodeUU(encodeUU(&uuie{body: body, callID: make([]byte, 16), confID: make([]byte, 16), reason: reasonAdaptiveBusy, fastStart: [][]byte{tx}}))
		if err != nil || u.body != body {
			t.Errorf("body %d: %+v %v", body, u, err)
		}
	}
	m := newQ931(q931Setup, 0x1234, false, &uuie{body: bodyCallProceeding, callID: make([]byte, 16)})
	m.ies[ieCalled] = numberIE("201")
	p, err := parseQ931(m.bytes())
	if err != nil || p.callRef != 0x1234 || p.msgType != q931Setup || number(p.ies[ieCalled]) != "201" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := p.uu(); err != nil {
		t.Fatal(err)
	}
}

func FuzzDecode(f *testing.F) {
	for _, n := range []string{"setup", "connect", "release"} {
		h, _ := os.ReadFile("testdata/" + n + ".hex")
		b, _ := hex.DecodeString(strings.TrimSpace(string(h)))
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if u, err := decodeUU(b); err == nil {
			for _, o := range u.fastStart {
				decodeOLC(o)
			}
		}
		decodeOLC(b)
		parseQ931(b)
	})
}

// RawSetup is a Setup carrying an outside encoder's user information (the testdata vector), for
// tests that call BitVoice as another vendor's gateway would.
func TestExportRawSetup(t *testing.T) {
	b := RawSetup(0x55, vector(t, "setup"))
	m, err := parseQ931(b[4:])
	if err != nil || m.msgType != q931Setup || m.callRef != 0x55 {
		t.Fatal(m, err)
	}
}
