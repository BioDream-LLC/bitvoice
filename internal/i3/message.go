package i3

import (
	"bytes"
	"mime"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"
)

// Additional Data (RFC 7852): who provides the caller's service, what kind it is, the device, the
// subscriber, and comments. Each block comes in the INVITE (a cid: reference to a body part) or by
// reference (an https: URI to fetch).
type Provider struct {
	Name     string `json:"name,omitempty"`     // DataProviderString
	ID       string `json:"id,omitempty"`       // ProviderID
	Type     string `json:"type,omitempty"`     // TypeOfProvider: Telecom Provider, Client, ...
	Contact  string `json:"contact,omitempty"`  // ContactURI: how the PSAP reaches the provider
	Language string `json:"language,omitempty"` // Language
}

type Service struct {
	Environment string   `json:"environment,omitempty"` // Business or Residence
	Types       []string `json:"types,omitempty"`       // wireless, POTS, VOIP, ...
	Mobility    string   `json:"mobility,omitempty"`    // Fixed, Mobile, Nomadic, Unknown
}

type Device struct {
	Class  string `json:"class,omitempty"` // DeviceClassification
	Maker  string `json:"maker,omitempty"`
	Model  string `json:"model,omitempty"`
	ID     string `json:"id,omitempty"`     // UniqueDeviceID
	IDType string `json:"idType,omitempty"` // IMEI, MEID, MAC, ...
}

type Subscriber struct {
	Name     string   `json:"name,omitempty"`
	Phones   []string `json:"phones,omitempty"`
	Address  string   `json:"address,omitempty"`
	Language string   `json:"language,omitempty"`
}

// Additional is every Additional Data block a call brought.
type Additional struct {
	Providers  []Provider  `json:"providers,omitempty"`
	Service    *Service    `json:"service,omitempty"`
	Device     *Device     `json:"device,omitempty"`
	Subscriber *Subscriber `json:"subscriber,omitempty"`
	Comments   []string    `json:"comments,omitempty"`
	References []string    `json:"references,omitempty"` // blocks by reference, not fetched yet
}

// Empty reports no Additional Data at all.
func (a Additional) Empty() bool {
	return len(a.Providers) == 0 && a.Service == nil && a.Device == nil && a.Subscriber == nil && len(a.Comments) == 0 && len(a.References) == 0
}

// addBlock reads one Additional Data document into a.
func (a *Additional) addBlock(purpose string, b []byte) {
	root, err := parseXML(b)
	if err != nil {
		return
	}
	p := strings.ToLower(purpose)
	switch {
	case strings.HasSuffix(p, "providerinfo") || root.find("EmergencyCallData.ProviderInfo") != nil:
		pi := Provider{Name: root.val("DataProviderString"), ID: root.val("ProviderID"), Type: root.val("TypeOfProvider"),
			Contact: root.val("ContactURI"), Language: root.val("Language")}
		if pi.Contact == "" {
			if u := root.find("DataProviderContact"); u != nil {
				pi.Contact = firstNonEmpty(u.val("uri"), u.val("text"))
			}
		}
		if pi != (Provider{}) {
			a.Providers = append(a.Providers, pi)
		}
	case strings.HasSuffix(p, "serviceinfo") || root.find("EmergencyCallData.ServiceInfo") != nil:
		s := &Service{Environment: root.val("ServiceEnvironment"), Mobility: root.val("ServiceMobility")}
		for _, t := range root.all("ServiceType") {
			if v := strings.TrimSpace(t.text); v != "" {
				s.Types = append(s.Types, v)
			}
		}
		a.Service = s
	case strings.HasSuffix(p, "deviceinfo") || root.find("EmergencyCallData.DeviceInfo") != nil:
		d := &Device{Class: root.val("DeviceClassification"), Maker: root.val("DeviceMfgr"), Model: root.val("DeviceModelNr"),
			ID: root.val("UniqueDeviceID")}
		if u := root.find("UniqueDeviceID"); u != nil {
			d.IDType = u.attrs["TypeOfDeviceID"]
		}
		a.Device = d
	case strings.HasSuffix(p, "subscriberinfo") || root.find("EmergencyCallData.SubscriberInfo") != nil:
		s := &Subscriber{}
		if fn := root.find("fn"); fn != nil {
			s.Name = strings.TrimSpace(collectText(fn))
		}
		for _, t := range root.all("tel") {
			if v := strings.TrimSpace(collectText(t)); v != "" {
				s.Phones = append(s.Phones, strings.TrimPrefix(v, "tel:"))
			}
		}
		if ad := root.find("adr"); ad != nil {
			var parts []string
			for _, k := range []string{"street", "locality", "region", "code", "country"} {
				if v := ad.val(k); v != "" {
					parts = append(parts, v)
				}
			}
			s.Address = strings.Join(parts, ", ")
		}
		s.Language = root.val("lang")
		a.Subscriber = s
	case strings.HasSuffix(p, "comment") || root.find("EmergencyCallData.Comment") != nil:
		for _, c := range root.all("Comment") {
			if v := strings.TrimSpace(c.text); v != "" {
				a.Comments = append(a.Comments, v)
			}
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Header gives the SIP headers BitVoice reads from an INVITE.
type Header interface {
	Get(string) string
	GetAll(string) []string
}

// Call is everything an emergency INVITE carried beyond the call itself.
type Call struct {
	SDP         []byte    // the session description, for the call itself
	Location    *Location // by value
	LocationRaw []byte    // the PIDF-LO as it came, to pass on with a transfer
	LocationURI string    // by reference (https:, sip:, pres:), to fetch and fetch again (rebid)
	Routing     bool      // Geolocation-Routing: yes
	Additional  Additional
	Service     string // the requested service URN (urn:service:sos, urn:service:sos.police, ...)
	Problems    []string
}

var (
	geoURI    = regexp.MustCompile(`<([^>]+)>`)
	purposeRe = regexp.MustCompile(`(?i)purpose\s*=\s*"?([A-Za-z0-9.]+)"?`)
)

// FromInvite reads an INVITE's location, Additional Data and session description. body is the
// message body and contentType its Content-Type.
func FromInvite(h Header, requestURI, contentType string, body []byte) Call {
	c := Call{}
	if strings.HasPrefix(strings.ToLower(requestURI), "urn:service:sos") {
		c.Service = requestURI
	}
	parts := map[string][]byte{} // Content-ID (without <>) -> body
	var pidfs [][]byte
	mt, params, err := mime.ParseMediaType(contentType)
	switch {
	case err == nil && strings.HasPrefix(mt, "multipart/"):
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				break
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(p); err != nil {
				break
			}
			b := buf.Bytes()
			ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if id := strings.Trim(textproto.MIMEHeader(p.Header).Get("Content-ID"), "<> "); id != "" {
				parts[id] = b
			}
			switch {
			case ct == "application/sdp" && c.SDP == nil:
				c.SDP = b
			case ct == "application/pidf+xml":
				pidfs = append(pidfs, b)
			}
		}
	case mt == "application/sdp" || mt == "":
		c.SDP = body
	}
	// Geolocation (RFC 6442): cid: names a body part; anything else is a reference to fetch.
	for _, g := range h.GetAll("Geolocation") {
		for _, m := range geoURI.FindAllStringSubmatch(g, -1) {
			u := m[1]
			if id, ok := strings.CutPrefix(u, "cid:"); ok {
				if b, ok := parts[id]; ok {
					pidfs = append([][]byte{b}, pidfs...)
				} else {
					c.Problems = append(c.Problems, "the location the call points to (cid:"+id+") is not in it")
				}
			} else if c.LocationURI == "" {
				c.LocationURI = u
			}
		}
	}
	c.Routing = strings.EqualFold(strings.TrimSpace(h.Get("Geolocation-Routing")), "yes")
	for _, b := range pidfs {
		loc, err := ParsePIDF(b)
		if err != nil {
			c.Problems = append(c.Problems, "location: "+err.Error())
			continue
		}
		if c.Location == nil {
			c.Location, c.LocationRaw = &loc, b
		} else {
			merge(c.Location, loc)
		}
	}
	// Additional Data (RFC 7852): Call-Info with purpose EmergencyCallData.*.
	for _, ci := range h.GetAll("Call-Info") {
		for _, item := range splitHeader(ci) {
			pm := purposeRe.FindStringSubmatch(item)
			if pm == nil || !strings.HasPrefix(strings.ToLower(pm[1]), "emergencycalldata") {
				continue
			}
			m := geoURI.FindStringSubmatch(item)
			if m == nil {
				continue
			}
			if id, ok := strings.CutPrefix(m[1], "cid:"); ok {
				if b, ok := parts[id]; ok {
					c.Additional.addBlock(pm[1], b)
				}
			} else {
				c.Additional.References = append(c.Additional.References, pm[1]+" "+m[1])
			}
		}
	}
	return c
}

// merge fills what one location lacks from another (a civic address and a GPS fix in two tuples).
func merge(into *Location, from Location) {
	if into.Civic == nil {
		into.Civic = from.Civic
	}
	if !into.HasPoint() && from.HasPoint() {
		into.Lat, into.Lon, into.Radius, into.Shape, into.Polygon = from.Lat, from.Lon, from.Radius, from.Shape, from.Polygon
	}
}

// splitHeader splits a header with several comma-separated values, keeping commas inside <> and "".
func splitHeader(v string) []string {
	var out []string
	depth, quoted, start := 0, false, 0
	for i, r := range v {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '<' && !quoted:
			depth++
		case r == '>' && !quoted:
			depth--
		case r == ',' && depth == 0 && !quoted:
			out = append(out, strings.TrimSpace(v[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(v[start:]))
}
