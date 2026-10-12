package importer

import "testing"

func TestFreePBXCSV(t *testing.T) {
	csv := "\xef\xbb\xbfextension,name,secret,voicemail,vmpwd,email,outboundcid\n" +
		"101,Front Desk,abcdefghijklmnop,default,1234,desk@example.com,\"\"\"Acme\"\" <2125550100>\"\n" +
		"102,Dr Lee,short,novm,,,\n" +
		"x9,Bad,,,,,\n"
	r, err := Parse("extensions.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Extensions) != 2 || len(r.Warnings) != 2 {
		t.Fatalf("%+v", r)
	}
	a, b := r.Extensions[0], r.Extensions[1]
	if a.Secret != "abcdefghijklmnop" || !a.Voicemail.Enabled || a.Voicemail.PIN != "1234" || a.CallerID != "2125550100" || a.NoAnswer == nil {
		t.Fatalf("101: %+v", a)
	}
	if b.Secret != "" || b.Voicemail.Enabled {
		t.Fatalf("102: %+v", b)
	}
}

const pjsip = `
[transport-udp]
type=transport
protocol=udp

[endpoint-tmpl](!)
type=endpoint
context=from-internal
disallow=all
allow=ulaw

[201](endpoint-tmpl)
auth=201-auth
aors=201
callerid="Reception" <201>
mailboxes=201@default

[201-auth]
type=auth
auth_type=userpass
username=201
password=a-long-sip-password

[201]
type=aor
max_contacts=2

[carrier]
type=endpoint
outbound_auth=carrier-auth
aors=carrier

[carrier-auth]
type=auth
username=acct77
password=trunkpw

[carrier]
type=aor
contact=sip:sip.carrier.example:5080

[carrier-reg]
type=registration
endpoint=carrier
outbound_auth=carrier-auth
server_uri=sip:sip.carrier.example:5080
client_uri=sip:acct77@sip.carrier.example
`

func TestPJSIP(t *testing.T) {
	r, err := Parse("pjsip.conf", []byte(pjsip))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Extensions) != 1 || len(r.Trunks) != 1 {
		t.Fatalf("%+v", r)
	}
	x, tr := r.Extensions[0], r.Trunks[0]
	if x.Number != "201" || x.Name != "Reception" || x.Secret != "a-long-sip-password" || !x.Voicemail.Enabled {
		t.Fatalf("extension %+v", x)
	}
	if tr.Host != "sip.carrier.example" || tr.Port != 5080 || !tr.Register || tr.Username != "acct77" || tr.Password != "trunkpw" {
		t.Fatalf("trunk %+v", tr)
	}
}

func TestSIPConf(t *testing.T) {
	conf := `[general]
register => acct:pw@sip.example.net/100

[301]
type=friend
host=dynamic
secret=another-long-secret
callerid="Sales" <301>

[provider]
type=peer
host=sip.example.net
defaultuser=acct
secret=pw
`
	r, err := Parse("sip.conf", []byte(conf))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Extensions) != 1 || r.Extensions[0].Name != "Sales" || len(r.Trunks) != 1 || !r.Trunks[0].Register {
		t.Fatalf("%+v", r)
	}
	if _, err := Parse("notes.txt", []byte("hello")); err == nil {
		t.Fatal("unknown file accepted")
	}
}

func TestCallerNameAndLoneRegistration(t *testing.T) {
	for in, want := range map[string]string{`"Front <Desk>" <200>`: "Front <Desk>", `Front Desk <200>`: "Front Desk", `Lee`: "Lee", `"" <201>`: ""} {
		if got := callerName(in); got != want {
			t.Errorf("callerName(%q) = %q, want %q", in, got, want)
		}
	}
	r, err := Parse("pjsip.conf", []byte("[carrier]\ntype=registration\nserver_uri=sip:sip.carrier.example:5070\nclient_uri=sip:acct9@sip.carrier.example\noutbound_auth=carrier-auth\n\n[carrier-auth]\ntype=auth\nusername=acct9\npassword=pw\n"))
	if err != nil || len(r.Trunks) != 1 || r.Trunks[0].Host != "sip.carrier.example" || r.Trunks[0].Port != 5070 || r.Trunks[0].Username != "acct9" || !r.Trunks[0].Register {
		t.Fatalf("%+v %v", r.Trunks, err)
	}
}

func Test3CXUsers(t *testing.T) {
	csv := "\xef\xbb\xbfNumber,FirstName,LastName,EmailAddress,MobileNumber,OutboundCallerID,AuthID,AuthPassword,VMEnable,VMPIN\r\n" +
		"100,Ann,Smith,ann@example.com,,+1 212 555 0100,100,a-long-3cx-password,1,4321\r\n" +
		"101,Bob,,,0711111,,101,short,0,\r\n" +
		"x9,Bad,,,,,,,,\r\n"
	r, err := Parse("users.csv", []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "3CX users CSV" || len(r.Extensions) != 2 {
		t.Fatalf("%s %d", r.Format, len(r.Extensions))
	}
	a, b := r.Extensions[0], r.Extensions[1]
	if a.Name != "Ann Smith" || a.Email != "ann@example.com" || a.CallerID != "+12125550100" || a.Secret != "a-long-3cx-password" ||
		!a.Voicemail.Enabled || a.Voicemail.PIN != "4321" || a.NoAnswer == nil {
		t.Fatalf("ann %+v", a)
	}
	if b.Name != "Bob" || b.Voicemail.Enabled || b.Secret != "" {
		t.Fatalf("bob %+v", b)
	}
	if len(r.Warnings) != 4 { // bad number, short password, mobile, extensions only
		t.Fatalf("warnings %v", r.Warnings)
	}
}
