package pbx

import (
	"encoding/xml"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Emergency location: where a caller is, for the 911 centre (sent with the call as PIDF-LO) and for the
// people alerted on site. A phone registering from one of its site's networks is at the site; from
// anywhere else it is a remote worker, and the address they confirmed is used.

// Place is a caller's location as far as BitVoice knows it.
type Place struct {
	Address config.Address `json:"address"`
	Floor   string         `json:"floor,omitempty"`
	Room    string         `json:"room,omitempty"`
	Site    string         `json:"site,omitempty"` // the site's name, when at a site
	Remote  bool           `json:"remote"`         // registered from away from the site
	// Confirmed is false when the caller is away and has not confirmed where (the site's address is used,
	// and the alert says so).
	Confirmed bool   `json:"confirmed"`
	Text      string `json:"text"` // one line, for alerts
}

// RemoteAddresses gives an extension's confirmed home or remote address; the API server implements it.
type RemoteAddresses interface {
	RemoteAddress(ext string) (config.Address, time.Time, bool)
}

// onSite reports whether an address is on one of a site's networks; a site with no networks counts
// every phone as on site.
func onSite(s *config.Site, ip netip.Addr) bool {
	if s == nil || len(s.Networks) == 0 {
		return true
	}
	ip = ip.Unmap()
	for _, n := range s.Networks {
		if p, err := netip.ParsePrefix(strings.TrimSpace(n)); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// RemoteFor reports whether an extension's phone at this address is away from its site.
func (e *Engine) RemoteFor(ext string, ip netip.Addr) bool {
	cfg := e.Config()
	x := cfg.FindExtension(ext)
	if x == nil || x.Site == "" {
		return false
	}
	return !onSite(cfg.FindSite(x.Site), ip)
}

// placeOf works out where an extension's caller is, from the address their phone sends from.
func (e *Engine) placeOf(cfg *config.Config, x *config.Extension, from netip.Addr) Place {
	var pl Place
	site := cfg.FindSite(x.Site)
	if site != nil {
		pl.Site = site.Name
		pl.Address = config.Address{Country: site.Country, State: site.State, City: site.City, Street: site.Street,
			Number: site.Number, PostalCode: site.PostalCode, Detail: x.Location}
		pl.Floor, pl.Room = x.Floor, x.Room
		pl.Confirmed = true
	}
	if site != nil && !onSite(site, from) {
		pl.Remote, pl.Confirmed = true, false
		if e.Remote != nil {
			if a, at, ok := e.Remote.RemoteAddress(x.Number); ok && time.Since(at) < 90*24*time.Hour {
				pl.Address, pl.Floor, pl.Room, pl.Site, pl.Confirmed = a, "", "", "", true
			}
		}
	}
	pl.Text = placeText(pl, x)
	return pl
}

func placeText(pl Place, x *config.Extension) string {
	a := pl.Address
	if a.Street == "" && a.City == "" {
		if x.Location != "" {
			return x.Location
		}
		return "location not set"
	}
	parts := []string{strings.TrimSpace(a.Number + " " + a.Street)}
	if pl.Floor != "" {
		parts = append(parts, "floor "+pl.Floor)
	}
	if pl.Room != "" {
		parts = append(parts, "room "+pl.Room)
	}
	if a.Detail != "" {
		parts = append(parts, a.Detail)
	}
	parts = append(parts, strings.TrimSpace(a.City+" "+a.State+" "+a.PostalCode))
	s := strings.Join(parts, ", ")
	switch {
	case pl.Remote && pl.Confirmed:
		s = "working remotely at " + s
	case pl.Remote:
		s = "AWAY FROM THE OFFICE, address not confirmed; office: " + s
	case pl.Site != "":
		s = pl.Site + ", " + s
	}
	return s
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// pidf is the location as a PIDF-LO document (RFC 4119, civic address per RFC 5139 and 5491).
func pidf(pl Place, entity string, now time.Time) []byte {
	a := pl.Address
	var b strings.Builder
	el := func(tag, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "<ca:%s>%s</ca:%s>", tag, xmlText(strings.TrimSpace(v)), tag)
		}
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
		`<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" `+
		`xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" xmlns:dm="urn:ietf:params:xml:ns:pidf:data-model" entity="%s">`,
		xmlText(entity))
	b.WriteString(`<dm:device id="bitvoice"><gp:geopriv><gp:location-info><ca:civicAddress xml:lang="en">`)
	el("country", a.Country)
	el("A1", a.State)
	el("A3", a.City)
	el("RD", a.Street)
	el("HNO", a.Number)
	el("FLR", pl.Floor)
	el("ROOM", pl.Room)
	el("LOC", a.Detail)
	el("PC", a.PostalCode)
	b.WriteString(`</ca:civicAddress></gp:location-info><gp:usage-rules/>`)
	method := "Manual"
	if !pl.Remote {
		method = "Derived" // from where the phone is plugged in
	}
	fmt.Fprintf(&b, `<gp:method>%s</gp:method></gp:geopriv><dm:timestamp>%s</dm:timestamp></dm:device></presence>`,
		method, now.UTC().Format(time.RFC3339))
	return []byte(b.String())
}

// withLocation turns an INVITE's SDP body into multipart/mixed with the PIDF-LO, and adds the
// Geolocation header that points at it (RFC 6442).
func withLocation(sdpBody []byte, loc []byte, host string) (contentType string, body []byte, geo string) {
	boundary := "bitvoice-" + fmt.Sprintf("%x", time.Now().UnixNano())
	cid := fmt.Sprintf("loc-%x@%s", time.Now().UnixNano(), host)
	var b strings.Builder
	fmt.Fprintf(&b, "--%s\r\nContent-Type: application/sdp\r\n\r\n%s\r\n", boundary, sdpBody)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: application/pidf+xml\r\nContent-ID: <%s>\r\n\r\n%s\r\n", boundary, cid, loc)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return "multipart/mixed;boundary=" + boundary, []byte(b.String()), "<cid:" + cid + ">"
}
