package sip

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"
)

func testCert(t *testing.T) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bitvoice-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// A request over TLS gets its response on the same connection, and Via says TLS.
func TestTLSTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 1)
	srv := NewStack(func(tx *ServerTx, in *Incoming) {
		if tx == nil {
			return
		}
		got <- in.From.Transport
		tx.Respond(NewResponse(in.Msg, 200, ""))
	})
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenTLS("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(t)}}); err != nil {
		t.Fatal(err)
	}
	srv.Serve(ctx)
	defer srv.Close()

	cli := NewStack(func(*ServerTx, *Incoming) {})
	cli.TLSClient = &tls.Config{InsecureSkipVerify: true} // the test certificate is self-signed
	if err := cli.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	cli.Serve(ctx)
	defer cli.Close()

	m := &Message{IsRequest: true, Method: OPTIONS, RequestURI: "sip:127.0.0.1;transport=tls"}
	m.Set("From", "<sip:a@127.0.0.1>;tag=1")
	m.Set("To", "<sip:127.0.0.1>")
	m.Set("Call-ID", NewCallID())
	m.Set("CSeq", "1 OPTIONS")
	peer := Peer{Transport: "TLS", Addr: netip.MustParseAddrPort("127.0.0.1:" + itoa(srv.TLSPort()))}
	tx := cli.Request(m, peer)
	var final *Message
	for r := range tx.Responses {
		final = r
	}
	if final == nil || final.StatusCode != 200 {
		t.Fatalf("no 200 over TLS: %v", final)
	}
	if tr := <-got; tr != "TLS" {
		t.Fatalf("server saw %s", tr)
	}
	if v, _ := ParseVia(final.Get("Via")); v == nil || v.Transport != "TLS" {
		t.Fatalf("via %q", final.Get("Via"))
	}
}
