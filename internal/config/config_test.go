package config

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCatchesProblems(t *testing.T) {
	c := Default()
	c.Extensions = []Extension{
		{Number: "100", Name: "Front desk"},
		{Number: "100", Name: "Duplicate"},
		{Number: "1", Name: "Too short"},
		{Number: "911", Name: "Reserved"},
	}
	c.RingGroups = []RingGroup{{Number: "600", Name: "Sales", Members: []string{"100", "555"}}}
	c.OutboundRoutes = []OutboundRoute{{Name: "US", Patterns: []string{"NXXNXXXXXX", "bad!"}, Trunks: []string{"nope"}}}
	err := c.Validate()
	var p Problems
	if !errors.As(err, &p) {
		t.Fatalf("want Problems, got %v", err)
	}
	text := p.Error()
	for _, want := range []string{"already used", "2 to 8 digits", "reserved", "no extension 555", "dial pattern", "no such trunk"} {
		if !strings.Contains(text, want) && !strings.Contains(text, "use digits, X") {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if c.Extensions[0].Secret == "" || len(c.Extensions[0].Secret) != 20 || c.Extensions[0].ID == "" {
		t.Errorf("defaults not filled: %+v", c.Extensions[0])
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, number string
		want            bool
	}{
		{"NXXNXXXXXX", "2125551212", true},
		{"NXXNXXXXXX", "1125551212", false},
		{"1NXXNXXXXXX", "12125551212", true},
		{"011.", "01144123", true},
		{"011.", "011", false},
		{"9[2-9]XX", "9555", true},
		{"9[2-9]XX", "9155", false},
		{"911", "911", true},
		{"911", "9111", false},
		{"+.", "+4420", true},
		{"[13579]Z", "39", true},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pattern, c.number); got != c.want {
			t.Errorf("%s ~ %s = %v", c.pattern, c.number, got)
		}
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := Default()
	c.Extensions = []Extension{{Number: "100", Name: "A"}}
	d := c.Clone()
	d.Extensions[0].Name = "B"
	if c.Extensions[0].Name != "A" {
		t.Fatal("clone shares extensions")
	}
}

func TestScheduleIsOpen(t *testing.T) {
	s := Schedule{Hours: []OpenHours{{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:30"}},
		Holidays: []Holiday{{Date: "2026-12-25"}}}
	at := func(v string) time.Time { x, _ := time.Parse("2006-01-02 15:04", v); return x }
	cases := map[string]bool{
		"2026-10-07 09:00": true,  // Wednesday opening
		"2026-10-07 17:29": true,  // just before closing
		"2026-10-07 17:30": false, // closed at the end time
		"2026-10-07 08:59": false,
		"2026-10-10 12:00": false, // Saturday
		"2026-12-25 12:00": false, // holiday on a Friday
	}
	for when, want := range cases {
		if got := s.IsOpen(at(when)); got != want {
			t.Errorf("%s: open %v, want %v", when, got, want)
		}
	}
}

func TestCountryDefaults(t *testing.T) {
	if !MatchPattern("0Z.", "02079460000") || MatchPattern("0Z.", "0044207946") {
		t.Fatal("national pattern")
	}
	if n := EmergencyNumbersFor("gb"); n[0] != "999" {
		t.Fatal(n)
	}
	if n := EmergencyNumbersFor("DE"); len(n) != 1 || n[0] != "112" {
		t.Fatal(n)
	}
}

func TestSplitTrunkHost(t *testing.T) {
	for in, want := range map[string]string{
		"sip:sip.example.com:5080": "sip.example.com 5080", "sip.example.com": "sip.example.com 5060",
		" SIP:acct@sip.example.com;transport=udp ": "sip.example.com 5060", "[2001:db8::1]:5062": "2001:db8::1 5062",
		"2001:db8::1": "2001:db8::1 5060", "10.0.0.1:5070": "10.0.0.1 5070",
	} {
		h, p := splitTrunkHost(in, 5060)
		if got := h + " " + strconv.Itoa(p); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestHeaderFieldsValidated(t *testing.T) {
	c := Default()
	c.Extensions = []Extension{{Number: "100", Name: "A", CallerID: "call me"}}
	c.Trunks = []Trunk{{Name: "T", Host: "sip.example.com", Username: "a b>", FromDomain: "x y"}}
	err := c.Validate()
	for _, f := range []string{"extensions[0].callerId", "trunks[0].username", "trunks[0].fromDomain"} {
		if err == nil || !strings.Contains(err.Error(), f) {
			t.Errorf("%s not refused: %v", f, err)
		}
	}
	c.Extensions[0].CallerID = "+12125550100"
	c.Trunks[0].Username, c.Trunks[0].FromDomain = "acct-1", ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNumbersAndMailValidated(t *testing.T) {
	c := Default()
	c.RingGroups = []RingGroup{{ID: "g", Number: "600", Name: "Front", Strategy: "all", Members: []string{}, RingSeconds: -5}}
	c.System.Email = Email{Host: "smtp.example.com:587", Port: 70000, From: "a@example.com\r\nBcc: x@evil.test"}
	c.Security.FailBanWindowSecs = 0
	c.Security.FailBanMinutes = -1
	c.Trunks = []Trunk{{ID: "t", Name: "T", Host: "sip.example.com", MaxChannels: -1}}
	err := c.Validate()
	for _, f := range []string{"ringGroups[0].ringSeconds", "system.email.port", "system.email.host", "system.email.from",
		"security.failBanWindowSecs", "security.failBanMinutes", "trunks[0].maxChannels"} {
		if err == nil || !strings.Contains(err.Error(), f+":") {
			t.Errorf("%s not reported: %v", f, err)
		}
	}
}

func TestOvernightHours(t *testing.T) {
	s := Schedule{Hours: []OpenHours{{Days: []int{5}, Start: "22:00", End: "06:00"}}} // Friday night
	at := func(day, hhmm string) bool {
		tm, err := time.Parse("2006-01-02 15:04", day+" "+hhmm)
		if err != nil {
			t.Fatal(err)
		}
		return s.IsOpen(tm)
	}
	// 2026-10-09 is a Friday.
	for _, c := range []struct {
		day, at string
		open    bool
	}{{"2026-10-09", "21:59", false}, {"2026-10-09", "22:00", true}, {"2026-10-10", "05:59", true},
		{"2026-10-10", "06:00", false}, {"2026-10-10", "22:30", false}, {"2026-10-08", "23:00", false}} {
		if got := at(c.day, c.at); got != c.open {
			t.Errorf("%s %s: open=%v, want %v", c.day, c.at, got, c.open)
		}
	}
}

func TestPrependValidated(t *testing.T) {
	c := Default()
	c.Trunks = []Trunk{{ID: "t", Name: "T", Host: "sip.example.com"}}
	c.OutboundRoutes = []OutboundRoute{{Name: "R", Patterns: []string{"9."}, Trunks: []string{"t"}, Prepend: "1>@evil"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "outboundRoutes[0].prepend:") {
		t.Fatalf("prepend accepted: %v", err)
	}
	c.OutboundRoutes[0].Prepend = "+44"
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "prepend") {
		t.Fatal(err)
	}
}

func TestNoNullLists(t *testing.T) {
	c := Default()
	c.Trunks = []Trunk{{ID: "t", Name: "T", Host: "sip.example.com"}}
	_ = c.Validate()
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "null") {
		t.Fatalf("null in %s", b)
	}
}

func TestAllowFromValidated(t *testing.T) {
	c := Default()
	c.Trunks = []Trunk{{ID: "t", Name: "T", Host: "sip.example.com", AllowFrom: []string{"203.0.113.0/24", "198.51.100.7", "0.0.0.0/0", "carrier.example"}}}
	err := c.Validate()
	for _, f := range []string{"allowFrom[2]", "allowFrom[3]"} {
		if err == nil || !strings.Contains(err.Error(), f+":") {
			t.Errorf("%s not refused: %v", f, err)
		}
	}
	if err != nil && (strings.Contains(err.Error(), "allowFrom[0]") || strings.Contains(err.Error(), "allowFrom[1]")) {
		t.Errorf("good entries refused: %v", err)
	}
}

// A receptionist's way out must reach people: never itself, another receptionist or a script.
func TestReceptionistPersonIsAPerson(t *testing.T) {
	for _, typ := range []string{"receptionist", "script"} {
		c := Default()
		c.Receptionists = []Receptionist{{ID: "r1", Name: "Front", Person: Destination{Type: typ, Target: "r1"}}}
		var p Problems
		if !errors.As(c.Validate(), &p) || !strings.Contains(p.Error(), "must reach one") {
			t.Errorf("%s accepted as the person: %v", typ, p)
		}
	}
}

func TestP25ChannelValidation(t *testing.T) {
	c := Default()
	c.System.Radio = Radio{Console: "800", Channels: []RadioChannel{{Name: "TG", Kind: "p25", Port: 46000, Group: 819, Home: "10.0.2.25:5060", HomeRFSS: 3}},
		P25: P25Console{WACN: "BEE07"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "3 hexadecimal digits") || !strings.Contains(err.Error(), "RFSS ID") {
		t.Fatalf("want the console identity checked: %v", err)
	}
	c.System.Radio.P25 = P25Console{WACN: "BEE07", System: "AD5", RFSS: 2, Unit: 4097, SIPPort: c.System.SIPPort}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "phones use this port") {
		t.Fatalf("want the ISSI port kept off the phones' port: %v", err)
	}
	c.System.Radio.P25.SIPPort = 0
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.System.Radio.P25.SIPPort != 5062 || c.System.Radio.P25.Priority != 4 {
		t.Fatalf("defaults %+v", c.System.Radio.P25)
	}
}

func TestReceptionistNeverLoopsBack(t *testing.T) {
	c := Default()
	c.Extensions = append(c.Extensions, Extension{Number: "201", Name: "Desk", Secret: "Desk-secret-2026-ok"})
	c.Receptionists = []Receptionist{{ID: "ai", Name: "Ava", Person: Destination{Type: "schedule", Target: "hrs"}}}
	c.Menus = []Menu{{ID: "m", Name: "Main", TimeoutSeconds: 5, Repeats: 1, Timeout: &Destination{Type: "receptionist", Target: "ai"}}}
	c.Schedules = []Schedule{{ID: "hrs", Name: "Hours", Hours: []OpenHours{{Days: []int{1}, Start: "09:00", End: "17:00"}},
		Open: Destination{Type: "extension", Target: "201"}, Closed: Destination{Type: "menu", Target: "m"}}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "come back to an AI receptionist through the menu Main") {
		t.Fatalf("want the loop through the schedule and menu refused: %v", err)
	}
	c.Menus[0].Timeout = &Destination{Type: "voicemail", Target: "201"}
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "come back") {
		t.Fatal(err)
	}
}

func TestConsentForParties(t *testing.T) {
	r := Recording{Notice: "off", Rules: []ConsentRule{{Prefix: "1415", Name: "California", Action: "announce"}, {Prefix: "1512", Name: "Texas", Action: "off"}}}
	if a, applied := r.ConsentForParties("+15125550100", "+14155550123"); a != "announce" || len(applied) != 2 {
		t.Fatalf("%s %v", a, applied)
	}
	if a, _ := r.ConsentForParties("+15125550100", ""); a != "off" {
		t.Fatalf("%s", a)
	}
}

// A speech-to-text service may stand in for the model; it must be https, or plain http only within
// the building (this computer or a private network address).
func TestTranscribeService(t *testing.T) {
	for _, tc := range []struct {
		ai AI
		ok bool
	}{
		{AI{Enabled: true}, false},
		{AI{Enabled: true, TranscribeURL: "http://127.0.0.1:8178/inference"}, true},
		{AI{Enabled: true, TranscribeURL: "http://192.168.1.50:8178/inference"}, true},
		{AI{Enabled: true, TranscribeURL: "http://localhost:8178/inference"}, true},
		{AI{Enabled: true, TranscribeURL: "https://api.openai.com/v1/audio/transcriptions", TranscribeModel: "whisper-1"}, true},
		{AI{Enabled: true, TranscribeURL: "http://whisper.example.com/inference"}, false},
		{AI{Enabled: true, TranscribeURL: "http://8.8.8.8/inference"}, false},
		{AI{Enabled: true, LocalSpeech: true}, true},
		{AI{Enabled: true, LocalSpeech: true, LocalModel: "small"}, true},
		{AI{Enabled: true, LocalSpeech: true, LocalModel: "huge"}, false},
		{AI{Enabled: true, LocalSpeech: true, TranscribeURL: "http://127.0.0.1:8178/inference"}, false},
	} {
		c := Default()
		c.System.AI = tc.ai
		if err := c.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc.ai, err)
		}
	}
}

// What carries 911 callers' details or a secret to CAD goes by https, or plain http only within this
// computer: newly set, anything else is refused; one already in the configuration still loads (and
// other changes save) but is listed as unsafe, so nothing is sent to it.
func TestCADAddressesSecure(t *testing.T) {
	for _, tc := range []struct {
		u  string
		ok bool
	}{
		{"https://cad.example.gov/eido", true},
		{"http://127.0.0.1:9450/eido", true},
		{"http://localhost:9450/eido", true},
		{"http://[::1]:9450/eido", true},
		{"http://192.168.1.50:9450/eido", false},
		{"http://cad.example.gov/eido", false},
	} {
		c := Default()
		c.System.PSAP.EIDOURL, c.System.Radio.CADURL = tc.u, tc.u
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", tc.u, err)
		}
		err := c.CheckChange(Default())
		if bad := err != nil && strings.Contains(err.Error(), "eidoUrl") && strings.Contains(err.Error(), "cadUrl"); bad == tc.ok {
			t.Errorf("%s: %v", tc.u, err)
		}
		if err := c.CheckChange(c); err != nil {
			t.Errorf("%s already set: %v", tc.u, err)
		}
		if got := len(UnsafeCADAddresses(c)); got != map[bool]int{true: 0, false: 2}[tc.ok] {
			t.Errorf("%s unsafe %d", tc.u, got)
		}
	}
	for a, off := range map[string]bool{"": false, "127.0.0.1:5001": false, "localhost:5001": false, "[::1]:5001": false,
		"serial:/dev/ttyUSB0?baud=9600": false, "192.168.1.50:5001": true, "cad.example.gov:5001": true} {
		if ALIOffComputer(a) != off {
			t.Errorf("ALI %q off computer: want %v", a, off)
		}
	}
}
