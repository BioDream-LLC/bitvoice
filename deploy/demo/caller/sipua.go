package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// demo is the 911 caller and the call-taker on the demo server: an ESInet stand-in sends a call into
// BitVoice (as an NG911 core would: urn:service:sos, SDP and PIDF-LO), and a phone registered as the
// call-taker extension answers it. Both play their side's lines as tones (tone.go).
type demo struct {
	log    *slog.Logger
	pbx    sip.Peer
	esinet *sip.Stack // the ESInet stand-in, at the address the ESInet trunk names
	bot    *sip.Stack // the call-taker's phone
	ext    string
	secret string
	answer time.Duration // how long the call-taker lets it ring (a visitor's phone may answer first)

	mu  sync.Mutex
	cur *run // the call under way, if any
}

// run is one invented call.
type run struct {
	s        int
	callID   string
	answered chan struct{} // the call-taker answered (closed once)
	done     chan struct{} // the call is over (closed once)
	ansOnce  sync.Once
	doneOnce sync.Once
	botMedia *net.UDPConn
	botDlg   *dialog
}

type dialog struct {
	callID, local, remote, target string
	seq                           uint32
	media                         netip.AddrPort
}

func (r *run) finish() { r.doneOnce.Do(func() { close(r.done) }) }

func newDemo(log *slog.Logger, pbx netip.AddrPort, esinetAddr, ext, secret string) (*demo, error) {
	d := &demo{log: log, pbx: sip.Peer{Transport: "UDP", Addr: pbx}, ext: ext, secret: secret, answer: 8 * time.Second}
	d.esinet = sip.NewStack(d.esinetHandle)
	d.esinet.Host = "127.0.0.1"
	if err := d.esinet.Listen(esinetAddr); err != nil {
		return nil, fmt.Errorf("ESInet stand-in: %w", err)
	}
	d.bot = sip.NewStack(d.botHandle)
	d.bot.Host = "127.0.0.1"
	if err := d.bot.Listen("127.0.0.1:0"); err != nil {
		return nil, fmt.Errorf("call-taker phone: %w", err)
	}
	return d, nil
}

func (d *demo) serve(ctx context.Context) {
	d.esinet.Serve(ctx)
	d.bot.Serve(ctx)
	go func() { <-ctx.Done(); d.esinet.Close(); d.bot.Close() }()
	go d.keepRegistered(ctx)
}

// keepRegistered keeps the call-taker's phone registered: every minute, since the demo's nightly
// rebuild forgets every phone.
func (d *demo) keepRegistered(ctx context.Context) {
	ok := false
	for {
		code := d.register()
		if (code == 200) != ok {
			ok = code == 200
			d.log.Info("call-taker phone", "extension", d.ext, "registered", ok, "answer", code)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func (d *demo) contact() string {
	return "<sip:" + d.ext + "@127.0.0.1:" + strconv.Itoa(d.bot.Port()) + ">"
}

func (d *demo) register() int {
	m := &sip.Message{IsRequest: true, Method: sip.REGISTER, RequestURI: "sip:127.0.0.1"}
	m.Set("From", "<sip:"+d.ext+"@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:"+d.ext+"@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 REGISTER")
	m.Set("Contact", d.contact())
	m.Set("Expires", "300")
	r := final(d.bot.Request(m.Clone(), d.pbx))
	if r != nil && r.StatusCode == 401 {
		ch, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
		if err != nil {
			return r.StatusCode
		}
		cr, err := sip.Answer(ch, d.ext, d.secret, sip.REGISTER, m.RequestURI)
		if err != nil {
			return r.StatusCode
		}
		m.Set("CSeq", "2 REGISTER")
		m.Set("Authorization", cr.String())
		r = final(d.bot.Request(m, d.pbx))
	}
	if r == nil {
		return 0
	}
	return r.StatusCode
}

func final(tx *sip.ClientTx) *sip.Message {
	var f *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			f = r
		}
	}
	return f
}

// call places scenario s and returns when it is over.
func (d *demo) call(ctx context.Context, s int) error {
	sc := scenarios[s]
	callerRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer callerRTP.Close()
	r := &run{s: s, callID: sip.NewCallID(), answered: make(chan struct{}), done: make(chan struct{})}
	d.mu.Lock()
	if d.cur != nil {
		d.mu.Unlock()
		return errors.New("a demo call is already under way")
	}
	d.cur = r
	d.mu.Unlock()
	defer func() {
		r.finish()
		d.mu.Lock()
		d.cur = nil
		d.mu.Unlock()
	}()

	port := callerRTP.LocalAddr().(*net.UDPAddr).Port
	sd := sdp.New("127.0.0.1", port, uint64(time.Now().Unix()), []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	body := "--B\r\nContent-Type: application/sdp\r\n\r\n" + string(sd) +
		"\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.demo>\r\n\r\n" + pidf(sc) + "\r\n--B--\r\n"
	from := "<sip:" + sc.caller + "@esinet.demo;user=phone>;tag=" + sip.NewTag()
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: sc.service}
	inv.Set("From", from)
	inv.Set("To", "<"+sc.service+">")
	inv.Set("Call-ID", r.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(d.esinet.Port())+">")
	inv.Set("P-Asserted-Identity", "<sip:"+sc.caller+"@esinet.demo;user=phone>")
	inv.Set("Geolocation", "<cid:loc@esinet.demo>")
	inv.Set("Geolocation-Routing", "yes")
	inv.Set("Content-Type", "multipart/mixed;boundary=B")
	inv.Body = []byte(body)
	d.log.Info("demo 911 call", "scenario", sc.name, "caller", sc.caller)
	tx := d.esinet.Request(inv, d.pbx)
	var ok *sip.Message
	for resp := range tx.Responses {
		if resp.StatusCode >= 200 {
			ok = resp
		}
	}
	if ok == nil || ok.StatusCode != 200 {
		code := 0
		if ok != nil {
			code = ok.StatusCode
		}
		return fmt.Errorf("the call was not answered (%d)", code)
	}
	ct, _ := sip.ParseAddress(ok.Get("Contact"))
	if ct == nil {
		return errors.New("the answer has no Contact")
	}
	dl := &dialog{callID: r.callID, local: from, remote: ok.Get("To"), target: ct.URI.String(), seq: 1, media: mediaOf(ok.Body)}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: dl.target}
	ack.Set("From", dl.local)
	ack.Set("To", dl.remote)
	ack.Set("Call-ID", dl.callID)
	ack.Set("CSeq", "1 ACK")
	_ = d.esinet.Stateless(ack, d.pbx)

	callerSegs, _, end := timeline(s)
	go sendSide(callerRTP, dl.media, callerSegs, r.done)
	go drain(callerRTP, r.done)
	select {
	case <-time.After(end):
		bye := &sip.Message{IsRequest: true, Method: sip.BYE, RequestURI: dl.target}
		bye.Set("From", dl.local)
		bye.Set("To", dl.remote)
		bye.Set("Call-ID", dl.callID)
		bye.Set("CSeq", "2 BYE")
		final(d.esinet.Request(bye, d.pbx))
	case <-r.done: // BitVoice ended it (a visitor hung up)
	case <-ctx.Done():
	}
	return nil
}

// esinetHandle answers what BitVoice sends the ESInet stand-in: the end of a demo call, and anything
// else politely.
func (d *demo) esinetHandle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	switch m.Method {
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		d.mu.Lock()
		if d.cur != nil && d.cur.callID == m.CallID() {
			d.cur.finish()
		}
		d.mu.Unlock()
	case sip.INVITE:
		tx.Respond(sip.NewResponse(m, 488, "Not Here")) // a re-INVITE: keep the media as it is
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// botHandle is the call-taker's phone: it answers the demo call after a few rings, plays the
// call-taker's lines, and refuses anything else.
func (d *demo) botHandle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return // ACK
	}
	m := in.Msg
	to, _ := sip.ParseAddress(m.Get("To"))
	switch m.Method {
	case sip.INVITE:
		d.mu.Lock()
		r := d.cur
		d.mu.Unlock()
		if to != nil && to.Tag() != "" { // a re-INVITE inside the call: same media
			if r != nil && r.botMedia != nil {
				if a := mediaOf(m.Body); a.IsValid() && r.botDlg != nil {
					r.botDlg.media = a
				}
				ok := sip.NewResponse(m, 200, "")
				ok.Set("Contact", d.contact())
				ok.Set("Content-Type", "application/sdp")
				ok.Body = botSDP(r.botMedia)
				tx.Respond(ok)
				return
			}
			tx.Respond(sip.NewResponse(m, 481, ""))
			return
		}
		tag := sip.NewTag()
		if r == nil {
			busy := sip.NewResponse(m, 486, "Only Takes Demo Calls")
			busy.Set("To", m.Get("To")+";tag="+tag)
			tx.Respond(busy)
			return
		}
		ring := sip.NewResponse(m, 180, "")
		ring.Set("To", m.Get("To")+";tag="+tag)
		tx.Respond(ring)
		go d.botAnswer(tx, m, tag, r)
	case sip.CANCEL:
		tx.Respond(sip.NewResponse(m, 200, ""))
		if inv := d.bot.FindInvite(m); inv != nil {
			r := sip.NewResponse(inv.Req, 487, "")
			inv.Respond(r)
		}
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

func (d *demo) botAnswer(tx *sip.ServerTx, m *sip.Message, tag string, r *run) {
	select {
	case <-time.After(d.answer):
	case <-r.done:
		return
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	ok := sip.NewResponse(m, 200, "")
	toTagged := m.Get("To") + ";tag=" + tag
	ok.Set("To", toTagged)
	ok.Set("Contact", d.contact())
	ok.Set("Content-Type", "application/sdp")
	ok.Body = botSDP(conn)
	ct, _ := sip.ParseAddress(m.Get("Contact"))
	dl := &dialog{callID: m.CallID(), local: toTagged, remote: m.Get("From"), media: mediaOf(m.Body)}
	if ct != nil {
		dl.target = ct.URI.String()
	}
	d.mu.Lock()
	r.botMedia, r.botDlg = conn, dl
	d.mu.Unlock()
	tx.Respond(ok)
	r.ansOnce.Do(func() { close(r.answered) })
	_, takerSegs, _ := timeline(r.s)
	go func() {
		<-r.done
		conn.Close()
	}()
	go drain(conn, r.done)
	sendSide(conn, dl.media, takerSegs, r.done)
}

func botSDP(c *net.UDPConn) []byte {
	return sdp.New("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, uint64(time.Now().Unix()), []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
}

func mediaOf(body []byte) netip.AddrPort {
	s, err := sdp.Parse(body)
	if err != nil || s.Audio() == nil {
		return netip.AddrPort{}
	}
	a, err := netip.ParseAddr(s.Connection(s.Audio()))
	if err != nil {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(a, uint16(s.Audio().Port))
}

// sendSide sends one side's sound as G.711 μ-law every 20 ms until done: tones for its lines and
// silence between them, so BitVoice always hears the side (a side that goes quiet for a minute ends
// a call).
func sendSide(c *net.UDPConn, to netip.AddrPort, segs []segment, done <-chan struct{}) {
	if !to.IsValid() {
		return
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	ssrc := binary.BigEndian.Uint32(b[:])
	pkt := make([]byte, 12+160)
	pkt[0], pkt[1] = 0x80, 0
	binary.BigEndian.PutUint32(pkt[8:12], ssrc)
	start := time.Now()
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-done:
			return
		case <-t.C:
		}
		// The frame due now, by the clock: a late tick sends what is due, so the sound keeps its time.
		frame := int(time.Since(start) / (20 * time.Millisecond))
		binary.BigEndian.PutUint16(pkt[2:4], uint16(i))
		binary.BigEndian.PutUint32(pkt[4:8], uint32(frame*160))
		for j := range 160 {
			pkt[12+j] = media.LinearToUlaw(sample(segs, frame*160+j))
		}
		if _, err := c.WriteToUDPAddrPort(pkt, to); err != nil && strings.Contains(err.Error(), "closed") {
			return
		}
	}
}

// drain reads (and drops) what BitVoice sends a side.
func drain(c *net.UDPConn, done <-chan struct{}) {
	buf := make([]byte, 2048)
	for {
		select {
		case <-done:
			return
		default:
		}
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := c.ReadFromUDP(buf); err != nil && !isTimeout(err) {
			return
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// pidf is the caller's location as an NG911 core passes it on: a civic address and a point.
func pidf(sc scenario) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gml="http://www.opengis.net/gml" xmlns:gs="http://www.opengis.net/pidflo/1.0"` +
		` xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" entity="pres:` + sc.caller + `@esinet.demo">
 <tuple id="demo"><status><gp:geopriv><gp:location-info>
 <ca:civicAddress><ca:country>US</ca:country><ca:A1>` + sc.st + `</ca:A1><ca:A3>` + sc.city + `</ca:A3><ca:HNO>` + sc.hno + `</ca:HNO><ca:RD>` + sc.rd + `</ca:RD><ca:STS>` + sc.sts + `</ca:STS></ca:civicAddress>
 <gs:Circle srsName="urn:ogc:def:crs:EPSG::4326"><gml:pos>` + sc.lat + ` ` + sc.lon + `</gml:pos><gs:radius uom="urn:ogc:def:uom:EPSG::9001">30</gs:radius></gs:Circle>
 </gp:location-info><gp:usage-rules/><gp:method>GPS</gp:method></gp:geopriv></status><timestamp>` + time.Now().UTC().Format(time.RFC3339) + `</timestamp></tuple></presence>`
}
