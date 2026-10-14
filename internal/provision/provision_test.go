package provision

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func testConfig(vendor string) *config.Config {
	c := config.Default()
	c.Extensions = []config.Extension{{Number: "101", Name: `Dr "Lee" & Co`, Secret: "secret<>&123456", Enabled: true}}
	c.System.Provisioning = config.Provisioning{Enabled: true, Transport: "tls"}
	c.Devices = []config.Device{{MAC: "00:15:65:AA:bb:01", Vendor: vendor, Extension: "101"}}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return &c
}

func TestEveryVendor(t *testing.T) {
	for _, v := range config.Vendors {
		cfg := testConfig(v)
		name := Names(v, "001565aabb01")[0]
		f, ok := Lookup(cfg, name, "192.0.2.10", 5060, 5061)
		if !ok {
			t.Fatalf("%s: %s not found", v, name)
		}
		body := string(f.Body)
		if v == "polycom" {
			if !strings.Contains(body, `CONFIG_FILES="001565aabb01-bitvoice.cfg"`) {
				t.Fatalf("polycom master: %s", body)
			}
			f, _ = Lookup(cfg, "001565aabb01-bitvoice.cfg", "192.0.2.10", 5060, 5061)
			body = string(f.Body)
		}
		speaker := v == "algo" || v == "cyberdata" // paging speakers have no mailbox
		if !strings.Contains(body, "192.0.2.10") || !strings.Contains(body, "5061") || (!speaker && !strings.Contains(body, "*97")) || !strings.Contains(body, "101") {
			t.Fatalf("%s: server, TLS port or voicemail missing:\n%s", v, body)
		}
		if f.ContentType == "application/xml" {
			if err := xml.Unmarshal(f.Body, new(struct{ XMLName xml.Name })); err != nil {
				t.Fatalf("%s: not well-formed XML: %v\n%s", v, err, body)
			}
			if strings.Contains(body, "secret<>") {
				t.Fatalf("%s: password not escaped", v)
			}
		}
	}
	if _, ok := Lookup(testConfig("yealink"), "../001565aabb01.cfg", "h", 1, 2); ok {
		t.Fatal("odd name served")
	}
	if _, ok := Lookup(testConfig("yealink"), "001565aabb02.cfg", "h", 1, 2); ok {
		t.Fatal("unknown phone served")
	}
}

// With a second server, phones that can hold two are given it to fall back on.
func TestBackupServer(t *testing.T) {
	for v, want := range map[string]string{"yealink": "account.1.sip_server.2.address = 192.0.2.11\naccount.1.sip_server.2.port = 5061",
		"fanvil": "SIP1 Backup Addr        :192.0.2.11\nSIP1 Backup Port        :5061", "cisco": "<Alternate_Proxy_1_>192.0.2.11:5061</Alternate_Proxy_1_>",
		"mitel": "sip line1 backup proxy ip: 192.0.2.11\nsip line1 backup proxy port: 5061", "algo": "sip.proxy2 = 192.0.2.11:5061",
		"polycom": `reg.1.server.2.address="192.0.2.11" reg.1.server.2.port="5061"`, "grandstream": "<P2312>192.0.2.11:5061</P2312>"} {
		cfg := testConfig(v)
		name := Names(v, "001565aabb01")[0]
		if v == "polycom" {
			name = "001565aabb01-bitvoice.cfg"
		}
		f, _ := LookupWith(cfg, name, "192.0.2.10", 5060, 5061, Book{Backup: "192.0.2.11:5060"})
		if !strings.Contains(string(f.Body), want) {
			t.Errorf("%s: no backup server:\n%s", v, f.Body)
		}
		if f.ContentType == "application/xml" {
			if err := xml.Unmarshal(f.Body, new(struct{ XMLName xml.Name })); err != nil {
				t.Errorf("%s: %v", v, err)
			}
		}
		f, _ = LookupWith(cfg, name, "192.0.2.10", 5060, 5061, Book{})
		if strings.Contains(string(f.Body), "192.0.2.11") || strings.Contains(string(f.Body), "server.2") {
			t.Errorf("%s: a backup without a second server", v)
		}
	}
}

// With one DNS name for every server, phones are given the name and look the servers up (SRV).
func TestSRVName(t *testing.T) {
	for v, want := range map[string][]string{
		"yealink":     {"account.1.sip_server.1.address = sip.example.com", "account.1.sip_server.1.port = 0"},
		"polycom":     {`reg.1.server.1.address="sip.example.com" reg.1.server.1.port="0"`},
		"grandstream": {"<P47>sip.example.com</P47>", "<P103>1</P103>"},
		"snom":        {`<user_host idx="1" perm="">sip.example.com;transport=tls</user_host>`},
	} {
		cfg := testConfig(v)
		name := Names(v, "001565aabb01")[0]
		if v == "polycom" {
			name = "001565aabb01-bitvoice.cfg"
		}
		f, _ := LookupWith(cfg, name, "192.0.2.10", 5060, 5061, Book{SRVName: "sip.example.com", Backup: "192.0.2.11:5060"})
		for _, w := range want {
			if !strings.Contains(string(f.Body), w) {
				t.Errorf("%s: missing %q:\n%s", v, w, f.Body)
			}
		}
		if strings.Contains(string(f.Body), "192.0.2.11") {
			t.Errorf("%s: a backup address as well as the name", v)
		}
	}
}
