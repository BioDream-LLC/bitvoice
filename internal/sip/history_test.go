package sip

import (
	"strings"
	"testing"
)

func parseMsg(t *testing.T, headers string) *Message {
	t.Helper()
	raw := "INVITE sip:300@pbx.example.com SIP/2.0\r\nVia: SIP/2.0/UDP 192.0.2.1;branch=z9hG4bK1\r\nFrom: <sip:+12125550100@carrier>;tag=a\r\n" +
		"To: <sip:300@pbx.example.com>\r\nCall-ID: x\r\nCSeq: 1 INVITE\r\n" + headers + "Content-Length: 0\r\n\r\n"
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// RFC 7044's own example (section 9.1 style) and what carriers and Microsoft Teams send.
func TestForwardOfHistoryInfo(t *testing.T) {
	// Called 2125550199, which forwarded (busy, cause 486) to 300.
	m := parseMsg(t, "History-Info: <sip:+12125550199@carrier;user=phone>;index=1,\r\n"+
		" <sip:300@pbx.example.com;cause=486>;index=1.1;rc=1\r\n")
	f, ok := ForwardOf(m)
	if !ok || f.Original != "+12125550199" || f.From != "+12125550199" || f.Reason != "user-busy" || f.Hops != 1 {
		t.Fatalf("%+v %v", f, ok)
	}
	// Two forwards, the reason in a Reason header inside the URI, and history privacy.
	m = parseMsg(t, "History-Info: <sip:a@x>;index=1\r\nHistory-Info: <sip:b@x?Reason=SIP%3Bcause%3D408%3Btext%3D%22Timeout%22>;index=1.1;rc=1\r\n"+
		"History-Info: <sip:300@pbx.example.com?Reason=SIP%3Bcause%3D302&Privacy=history>;index=1.1.1;mp=1.1\r\n")
	f, ok = ForwardOf(m)
	if !ok || f.Original != "a" || f.From != "b" || f.Reason != "unconditional" || !f.Hidden || f.Hops != 2 {
		t.Fatalf("%+v %v", f, ok)
	}
	// Entries out of order are read in index order.
	m = parseMsg(t, "History-Info: <sip:300@p;cause=408>;index=1.1, <sip:orig@x>;index=1\r\n")
	if f, ok := ForwardOf(m); !ok || f.From != "orig" || f.Reason != "no-answer" {
		t.Fatalf("%+v %v", f, ok)
	}
	// One entry only: no forward.
	if _, ok := ForwardOf(parseMsg(t, "History-Info: <sip:300@p>;index=1\r\n")); ok {
		t.Fatal("one entry read as a forward")
	}
}

func TestForwardOfDiversion(t *testing.T) {
	m := parseMsg(t, "Diversion: <sip:2125550199@carrier>;reason=user-busy;counter=1;privacy=full,\r\n <sip:2125550100@carrier>;reason=unconditional\r\n")
	f, ok := ForwardOf(m)
	if !ok || f.From != "2125550199" || f.Reason != "user-busy" || f.Original != "2125550100" || !f.Hidden || f.Hops != 2 {
		t.Fatalf("%+v %v", f, ok)
	}
	// History-Info wins over Diversion when both come.
	m = parseMsg(t, "Diversion: <sip:old@x>;reason=no-answer\r\nHistory-Info: <sip:new@x>;index=1,<sip:300@p;cause=302>;index=1.1;rc=1\r\n")
	if f, _ := ForwardOf(m); f.From != "new" || f.Reason != "unconditional" {
		t.Fatalf("%+v", f)
	}
}

// What BitVoice sends parses back to the forward it described, and keeps the entries a call came in
// with (RFC 7044 section 10.1: a proxy adds to the history, never rewrites it).
func TestHistoryInfoRoundTrip(t *testing.T) {
	from := &URI{Scheme: "sip", User: "201", Host: "pbx.example.com"}
	to := &URI{Scheme: "sip", User: "+12125550111", Host: "carrier.example.net", Params: Params{{Name: "user", Value: "phone"}}}
	hi := HistoryInfo(nil, from, to, "no-answer")
	if strings.Join(hi, ", ") != "<sip:201@pbx.example.com>;index=1, <sip:+12125550111@carrier.example.net;user=phone;cause=408>;index=1.1;rc=1" {
		t.Fatalf("%q", hi)
	}
	m := parseMsg(t, "History-Info: "+strings.Join(hi, ", ")+"\r\n")
	if f, ok := ForwardOf(m); !ok || f.From != "201" || f.Reason != "no-answer" {
		t.Fatalf("%+v %v", f, ok)
	}
	// A call that came in forwarded once, forwarded on again.
	in := parseMsg(t, "History-Info: <sip:orig@carrier>;index=1, <sip:300@pbx.example.com;cause=486>;index=1.1;rc=1\r\n")
	hi = HistoryInfo(ParseHistoryInfo(in), &URI{Scheme: "sip", User: "300", Host: "pbx.example.com"}, to, "unconditional")
	if len(hi) != 3 || !strings.HasPrefix(hi[0], "<sip:orig@carrier>;index=1") || !strings.Contains(hi[2], ";index=1.1.1;rc=1.1") || !strings.Contains(hi[2], "cause=302") {
		t.Fatalf("%q", hi)
	}
	m = parseMsg(t, "History-Info: "+strings.Join(hi, ", ")+"\r\n")
	if f, ok := ForwardOf(m); !ok || f.Original != "orig" || f.From != "300" || f.Reason != "unconditional" || f.Hops != 2 {
		t.Fatalf("%+v %v", f, ok)
	}
	for _, r := range []string{"unconditional", "user-busy", "no-answer", "deflection", "do-not-disturb"} {
		if CauseReason(ReasonCause(r)) != r {
			t.Errorf("%s does not round-trip", r)
		}
	}
}
