package pbx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/msrp"
)

func selfSigned(t *testing.T, name string) tls.Certificate {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

// Text to 911 over TLS (msrps, RFC 4975 section 14): each end's certificate is the one its SDP
// named; a TCC presenting another certificate gets no session.
func TestTextTo911OverTLS(t *testing.T) {
	own := selfSigned(t, "psap.test")
	tccCert := selfSigned(t, "tcc.test")
	for _, tc := range []struct {
		name      string
		tccActive bool
		present   tls.Certificate // what the TCC actually presents
		ok        bool
	}{
		{"TCC connects", true, tccCert, true},
		{"BitVoice connects", false, tccCert, true},
		{"wrong certificate", true, selfSigned(t, "evil.test"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCarrier(t)
			cfg := trunkConfig(c.stack.Port())
			cfg.Trunks[0].ESInet, cfg.Trunks[0].Register = true, false
			cfg.Trunks[0].AllowFrom = []string{"127.0.0.1"}
			cfg.InboundRoutes[0].DID = ""
			e, _ := startEngine(t, cfg, func(e *Engine) {
				e.TLS = &tls.Config{Certificates: []tls.Certificate{own}}
				e.TLSClient = &tls.Config{Certificates: []tls.Certificate{own}}
			})
			fp := "a=fingerprint:" + msrp.Fingerprint(tccCert.Certificate[0]) + "\r\n"
			lines := "m=message 9 TCP/TLS/MSRP *\r\na=accept-types:text/plain\r\na=path:msrps://127.0.0.1:9/tcc1;tcp\r\na=setup:active\r\n" + fp
			var ln net.Listener
			if !tc.tccActive {
				var err error
				ln, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{tc.present}, ClientAuth: tls.RequireAnyClientCert,
					VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error { return nil }})
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				port := ln.Addr().(*net.TCPAddr).Port
				lines = "m=message " + strconv.Itoa(port) + " TCP/TLS/MSRP *\r\na=accept-types:text/plain\r\na=path:msrps://" + ln.Addr().String() + "/tcc1;tcp\r\na=setup:passive\r\n" + fp
			}
			id, ans := textSession(t, e, c, lines)
			path, _ := ans.Media[0].Attr("path")
			ownFP, _ := ans.Media[0].Attr("fingerprint")
			if !msrp.Secure(path) || ans.Media[0].Proto != "TCP/TLS/MSRP" || !msrp.FingerprintMatches(ownFP, own.Certificate[0]) {
				t.Fatalf("answer: path %q proto %q fingerprint %q", path, ans.Media[0].Proto, ownFP)
			}
			checkOwn := func(cs tls.ConnectionState) error {
				if !msrp.FingerprintMatches(ownFP, cs.PeerCertificates[0].Raw) {
					t.Error("BitVoice's certificate is not the one it named")
				}
				return nil
			}
			var tcc *msrp.Session
			var conn net.Conn
			if tc.tccActive {
				hp, _ := msrp.HostPort(path)
				cc, err := tls.Dial("tcp", hp, &tls.Config{Certificates: []tls.Certificate{tc.present}, InsecureSkipVerify: true, VerifyConnection: checkOwn})
				if err == nil {
					err = cc.Handshake()
				}
				if !tc.ok {
					if err == nil {
						// TLS 1.3: the client learns of a refused certificate on its first read
						cc.SetReadDeadline(time.Now().Add(15 * time.Second))
						_, err = cc.Read(make([]byte, 1))
					}
					if err == nil {
						t.Fatal("a TCC with another certificate got a session")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				conn = cc
				tcc = msrp.New("msrps://127.0.0.1:9/tcc1;tcp", path)
				go tcc.Serve(conn)
				tcc.Wait(time.Second)
				if err := tcc.Bind(); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = ln.(interface{ Addr() net.Addr })
				cc, err := ln.Accept()
				if err != nil {
					t.Fatal(err)
				}
				tconn := cc.(*tls.Conn)
				if err := tconn.Handshake(); err != nil {
					t.Fatal(err)
				}
				checkOwn(tconn.ConnectionState())
				tcc = msrp.New("msrps://"+ln.Addr().String()+"/tcc1;tcp", path)
				go tcc.Serve(tconn)
				tcc.Wait(time.Second)
			}
			if err := tcc.Send("help, over TLS"); err != nil {
				t.Fatal(err)
			}
			if txt := waitText(t, e, id, 1); txt[0].Text != "help, over TLS" {
				t.Fatalf("%+v", txt)
			}
			tcc.Close()
		})
	}
}
