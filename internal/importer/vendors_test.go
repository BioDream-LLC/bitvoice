package importer

import (
	"strings"
	"testing"
)

// Each vendor's export is recognised by its header and becomes extensions; what cannot come over
// is said.
func TestVendorExports(t *testing.T) {
	cases := []struct {
		name, file, format string
		want               map[string]string // number -> name
		warn               string
	}{
		{"cisco phones", "Device Name,Description,Device Type,Directory Number 1,Line Text Label 1,Alerting Name 1\n" +
			"SEP001122334455,Reception,Cisco 8845,2001,Reception,Front Desk\n" +
			"SEP001122334466,Second line,Cisco 8845,2001,,\n" +
			"SEPAABBCCDDEEFF,Spare,Cisco 7841,,,\n" +
			"SEP665544332211,Office,Cisco 7841,2002,Pat Jones,\n",
			"Cisco Unified CM phones export (BAT)", map[string]string{"2001": "Front Desk", "2002": "Pat Jones"}, "3 phones are listed"},
		{"cisco users", "USER ID,FIRST NAME,LAST NAME,TELEPHONE NUMBER,MAIL ID,PIN\njdoe,Jo,Doe,3001,jo@example.com,123456\n",
			"Cisco Unified CM users export (BAT)", map[string]string{"3001": "Jo Doe"}, "SIP passwords"},
		{"ip office", "Name,Full Name,Extension,Voicemail Code,Voicemail Email\nalex,Alex Kim,201,4321,alex@example.com\nbad,Bad,12345678901,,\n",
			"Avaya IP Office users export", map[string]string{"201": "Alex Kim"}, "not an extension number"},
		{"communication manager", "Ext,Port,Name,Type,Move\n40001,S00001,Lobby,9611SIP,no\n40002,S00002,,virtual,no\n40003,S00003,\"Lee, Sam\",2420,no\n",
			"Avaya Communication Manager station list", map[string]string{"40001": "Lobby", "40003": "Lee, Sam"}, "virtual station"},
		{"mitel", "\ufeffFirst Name,Last Name,Number,Device Type,MAC Address,Email Address,Voice Mail Passcode\nRiley,Ng,5001,6930,08000f112233,riley@example.com,2468\n",
			"Mitel MiVoice Business users and devices export", map[string]string{"5001": "Riley Ng"}, "Mitel phones"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Parse("export.csv", []byte(c.file))
			if err != nil {
				t.Fatal(err)
			}
			if r.Format != c.format {
				t.Fatalf("format %q", r.Format)
			}
			got := map[string]string{}
			for _, x := range r.Extensions {
				got[x.Number] = x.Name
				if !x.Voicemail.Enabled || x.Secret != "" {
					t.Fatalf("%s: mailbox %v, secret %q", x.Number, x.Voicemail.Enabled, x.Secret)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for n, name := range c.want {
				if got[n] != name {
					t.Fatalf("%s: %q, want %q", n, got[n], name)
				}
			}
			if !strings.Contains(strings.Join(r.Warnings, "\n"), c.warn) {
				t.Fatalf("warnings %q lack %q", r.Warnings, c.warn)
			}
		})
	}
	r, _ := Parse("x.csv", []byte("USER ID,FIRST NAME,LAST NAME,TELEPHONE NUMBER,MAIL ID,PIN\nu,A,B,3002,not-an-email,12\n"))
	if x := r.Extensions[0]; x.Email != "" || x.Voicemail.PIN != "" {
		t.Fatalf("kept a bad email or PIN: %+v", x)
	}
}
