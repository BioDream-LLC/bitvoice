package ice

import (
	"context"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Browsers hide their host addresses behind random mDNS names (name.local) until a page has
// permission to use the microphone; such a candidate is resolved with a one-shot multicast DNS query
// (RFC 6762 5.1). Asked from a port other than 5353, responders answer by unicast to it (6.7).

var mdnsGroup = netip.MustParseAddrPort("224.0.0.251:5353")

// IsMDNS says whether a candidate address is an mDNS name.
func IsMDNS(host string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), ".local")
}

// ErrMDNS is an mDNS name nobody answered for.
var ErrMDNS = errors.New("ice: nobody answered for the mDNS name")

// ResolveMDNS finds the IPv4 address of an mDNS name.
func ResolveMDNS(ctx context.Context, name string) (netip.Addr, error) {
	name = strings.TrimSuffix(name, ".")
	if !IsMDNS(name) || len(name) > 253 {
		return netip.Addr{}, errors.New("ice: not an mDNS name")
	}
	// The system's resolver first: macOS asks its mDNS responder, as Linux does with nss-mdns.
	if addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", name); err == nil && len(addrs) > 0 {
		return addrs[0].Unmap(), nil
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	id := uint16(rand.Uint32())
	q := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(q, id)
	binary.BigEndian.PutUint16(q[4:], 1) // one question
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return netip.Addr{}, errors.New("ice: a bad mDNS name")
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 1, 0x80, 1) // type A, class IN with the unicast-response bit
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	buf := make([]byte, 1500)
	for try := 0; try < 3 && time.Now().Before(deadline); try++ {
		conn.WriteToUDPAddrPort(q, mdnsGroup)
		wait := time.Now().Add(400 * time.Millisecond)
		if wait.After(deadline) {
			wait = deadline
		}
		conn.SetReadDeadline(wait)
		for {
			n, _, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				break
			}
			if ip, ok := answerA(buf[:n], id, name); ok {
				return ip, nil
			}
		}
	}
	return netip.Addr{}, ErrMDNS
}

// answerA reads an A record for name out of a DNS response.
func answerA(b []byte, id uint16, name string) (netip.Addr, bool) {
	if len(b) < 12 || b[2]&0x80 == 0 {
		return netip.Addr{}, false
	}
	if got := binary.BigEndian.Uint16(b); got != id && got != 0 { // mDNS responders may answer with ID 0
		return netip.Addr{}, false
	}
	qd, an := int(binary.BigEndian.Uint16(b[4:])), int(binary.BigEndian.Uint16(b[6:]))+int(binary.BigEndian.Uint16(b[8:]))+int(binary.BigEndian.Uint16(b[10:]))
	off := 12
	for range qd {
		_, o, ok := readName(b, off)
		if !ok || o+4 > len(b) {
			return netip.Addr{}, false
		}
		off = o + 4
	}
	for range an {
		n, o, ok := readName(b, off)
		if !ok || o+10 > len(b) {
			return netip.Addr{}, false
		}
		typ := binary.BigEndian.Uint16(b[o:])
		l := int(binary.BigEndian.Uint16(b[o+8:]))
		o += 10
		if o+l > len(b) {
			return netip.Addr{}, false
		}
		if typ == 1 && l == 4 && strings.EqualFold(n, name) {
			return netip.AddrFrom4([4]byte(b[o : o+4])), true
		}
		off = o + l
	}
	return netip.Addr{}, false
}

// readName reads a (possibly compressed) DNS name.
func readName(b []byte, off int) (string, int, bool) {
	var labels []string
	end := -1
	for hops := 0; hops < 20; hops++ {
		if off >= len(b) {
			return "", 0, false
		}
		l := int(b[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(labels, "."), end, true
		case l&0xC0 == 0xC0:
			if off+1 >= len(b) {
				return "", 0, false
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(b[off:]) & 0x3FFF)
		default:
			if off+1+l > len(b) {
				return "", 0, false
			}
			labels = append(labels, string(b[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, false
}
