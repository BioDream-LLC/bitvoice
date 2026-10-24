package ldap

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Filters encode as RFC 4511 section 4.5.1.7 lays them out (each expected value read field by field
// against it; TestOpenLDAP checks a real server takes them).
func TestFilterEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"(uid=bob)":           "a30a 0403756964 0403626f62",
		"(objectClass=*)":     "870b 6f626a656374436c617373",
		"(&(uid=a)(!(cn=b)))": "a015 a308040375696404016 1 a209a3070402636e040162",
		"(cn=Al*ce*)":         "a40e 0402636e 3008 8002416c 81026365",
		"(cn=a\\2a)":          "a308 0402636e 0402612a",
		"(memberOf:1.2.840.113556.1.4.1941:=cn=x)": "a929 8117312e322e3834302e3131333535362e312e342e31393431 82086d656d6265724f66 8304636e3d78",
	} {
		e, err := compileFilter(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		got := hex.EncodeToString(e.encode())
		if got != strings.ReplaceAll(want, " ", "") {
			t.Errorf("%s:\n got  %s\n want %s", in, got, strings.ReplaceAll(want, " ", ""))
		}
	}
	for _, bad := range []string{"(uid=bob", "uid=(", "(&)", "(=x)", "(cn=a\\2)", "((uid=a))"} {
		if _, err := compileFilter(bad); err == nil {
			t.Errorf("%q compiled", bad)
		}
	}
	if got := EscapeFilter("a*(b)\\c\x00"); got != `a\2a\28b\29\5cc\00` {
		t.Errorf("escape %q", got)
	}
	// An escaped value cannot add conditions to the filter it is put in.
	e, _ := compileFilter("(uid=" + EscapeFilter("x)(uid=*") + ")")
	if e.tag != classContext|constructed|3 || string(e.children[1].value) != "x)(uid=*" {
		t.Fatalf("injection was not kept inside the value: %x", e.encode())
	}
}

func TestBERRoundTrip(t *testing.T) {
	for _, n := range []int64{0, 1, 127, 128, 255, 256, -1, -128, -129, 1 << 40} {
		v, err := decodeInt(encodeInt(n))
		if err != nil || v != n {
			t.Errorf("%d -> %x -> %d %v", n, encodeInt(n), v, err)
		}
	}
	big := octets(strings.Repeat("x", 70000))
	e, err := readElement(bytes.NewReader(sequence(big, integer(5)).encode()))
	if err != nil || len(e.children) != 2 || len(e.children[0].value) != 70000 {
		t.Fatalf("long lengths: %v", err)
	}
	if _, err := readElement(bytes.NewReader([]byte{0x30, 0x84, 0x7f, 0xff, 0xff, 0xff})); err == nil {
		t.Fatal("a huge length was taken")
	}
	if _, err := parseElement(0x30, []byte{0x04, 0x10, 'a'}, 0); err == nil {
		t.Fatal("a child running past its parent was taken")
	}
}

func TestEmptyPasswordIsNotABind(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			time.Sleep(time.Second)
			c.Close()
		}
	}()
	c, err := Dial(context.Background(), Options{URL: "ldap://" + ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Bind("uid=bob,dc=x", ""); err != ErrEmptyPassword {
		t.Fatalf("an empty password: %v", err)
	}
}

// Against OpenLDAP (~/.bvtest/ldap): BITVOICE_TEST_LDAP=127.0.0.1:1389, BITVOICE_TEST_LDAPS=127.0.0.1:1636,
// BITVOICE_TEST_LDAP_CA=~/.bvtest/ldap/certs/ca.crt.
func TestOpenLDAP(t *testing.T) {
	plain, secure, caFile := os.Getenv("BITVOICE_TEST_LDAP"), os.Getenv("BITVOICE_TEST_LDAPS"), os.Getenv("BITVOICE_TEST_LDAP_CA")
	if plain == "" {
		t.Skip("set BITVOICE_TEST_LDAP to the OpenLDAP test server")
	}
	pool := x509.NewCertPool()
	if b, err := os.ReadFile(caFile); err == nil {
		pool.AppendCertsFromPEM(b)
	}
	tc := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	ctx := context.Background()
	for _, o := range []Options{{URL: "ldap://" + plain, StartTLS: true, TLS: tc}, {URL: "ldaps://" + secure, TLS: tc}} {
		c, err := Dial(ctx, o)
		if err != nil {
			t.Fatalf("%s: %v", o.URL, err)
		}
		if !c.Secure() {
			t.Fatalf("%s is not encrypted", o.URL)
		}
		if err := c.Bind("cn=admin,dc=example,dc=org", "admin-password"); err != nil {
			t.Fatal(err)
		}
		// 1,203 people: past the server's size limit of 1,000, so only paging finds them all.
		all, err := c.Search("ou=people,dc=example,dc=org", ScopeSubtree, "(objectClass=inetOrgPerson)", []string{"uid"}, 250, 0)
		if err != nil || len(all) != 1203 {
			t.Fatalf("paged search: %d %v", len(all), err)
		}
		some, err := c.Search("ou=people,dc=example,dc=org", ScopeSubtree, "(objectClass=inetOrgPerson)", []string{"uid"}, 250, 10)
		if err != nil || len(some) != 10 {
			t.Fatalf("a limit: %d %v", len(some), err)
		}
		bob, err := c.Search("dc=example,dc=org", ScopeSubtree, "(&(objectClass=inetOrgPerson)(uid="+EscapeFilter("bob")+"))", []string{"displayName", "telephoneNumber", "mail"}, 0, 0)
		if err != nil || len(bob) != 1 || bob[0].Get("telephonenumber") != "202" || bob[0].DN != "uid=bob,ou=people,dc=example,dc=org" {
			t.Fatalf("bob %+v %v", bob, err)
		}
		g, err := c.Search("cn=sales,ou=groups,dc=example,dc=org", ScopeBase, "", []string{"member"}, 0, 0)
		if err != nil || len(g) != 1 || len(g[0].All("member")) != 2 {
			t.Fatalf("group %+v %v", g, err)
		}
		if _, err := c.Search("dc=nowhere", ScopeSubtree, "", nil, 0, 0); !IsCode(err, NoSuchObject) {
			t.Fatalf("a base that is not there: %v", err)
		}
		// A person signs in with their own password; a wrong one is invalid credentials.
		if err := c.Bind("uid=bob,ou=people,dc=example,dc=org", "bob-password"); err != nil {
			t.Fatal(err)
		}
		if err := c.Bind("uid=bob,ou=people,dc=example,dc=org", "wrong"); !IsCode(err, InvalidCredentials) {
			t.Fatalf("a wrong password: %v", err)
		}
		c.Close()
	}
	// A certificate that is not trusted is refused.
	if _, err := Dial(ctx, Options{URL: "ldaps://" + secure}); err == nil {
		t.Fatal("an untrusted certificate was taken")
	}
}
