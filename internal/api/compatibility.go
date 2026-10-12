package api

import (
	"net/http"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// Compatibility: what BitVoice is built and tested to work with, and what has actually worked here.

type compatEntry struct {
	Kind      string `json:"kind"` // phone, adapter, softphone, browser, provider, door
	Vendor    string `json:"vendor"`
	Models    string `json:"models"`
	Provision bool   `json:"provision"` // BitVoice can set it up automatically
	Notes     string `json:"notes"`
}

// tested is the list kept with each release: the device families BitVoice's provisioning templates,
// codec support and test suites cover.
var tested = []compatEntry{
	{"phone", "Yealink", "T3x, T4x, T5x desk phones", true, "Provisioned over HTTPS; BLF keys, paging auto-answer and G.722."},
	{"phone", "Poly (Polycom)", "VVX, Edge E series", true, "Provisioned over HTTPS; paging uses Alert-Info auto-answer."},
	{"phone", "Grandstream", "GXP, GRP desk phones", true, "Provisioned over HTTPS."},
	{"adapter", "Grandstream", "HT801, HT802, HT812, HT814 analog adapters", true, "Set the extension's device to Analog adapter port: G.711 only, which faxes need."},
	{"phone", "Snom", "D7xx desk phones", true, "Provisioned over HTTPS."},
	{"softphone", "Any SIP softphone", "Linphone, Zoiper, MicroSIP, Bria", false, "UDP, TCP or TLS on the SIP port, with the extension number and SIP password."},
	{"browser", "Web browsers", "Chrome, Edge, Firefox, Safari", false, "The browser phone and staff phone, with WebRTC (tested in Chromium)."},
	{"door", "2N, Akuvox, Fanvil", "SIP door stations", false, "Set the extension's device to Door station; open by key code or the controller's HTTP address."},
	{"adapter", "Algo, CyberData", "SIP paging speakers and adapters", false, "Set the device to Overhead speaker, or listen to a paging group's multicast address."},
	{"provider", "Any SIP trunk", "Registration or IP authentication", false, "G.711 everywhere; G.722 where the provider offers it. Calls at once can be limited per trunk."},
	{"provider", "Tools for Asterisk", "AMI and ARI clients", false, "Wallboards, CRM click-to-dial and monitoring that use AMI or ARI."},
}

// vendorOf guesses a vendor from a User-Agent.
func vendorOf(ua string) string {
	l := strings.ToLower(ua)
	for _, v := range []struct{ key, name string }{
		{"yealink", "Yealink"}, {"poly", "Poly (Polycom)"}, {"grandstream", "Grandstream"}, {"snom", "Snom"},
		{"linphone", "Linphone"}, {"zoiper", "Zoiper"}, {"microsip", "MicroSIP"}, {"bria", "Bria"}, {"2n", "2N"},
		{"akuvox", "Akuvox"}, {"fanvil", "Fanvil"}, {"algo", "Algo"}, {"cyberdata", "CyberData"},
		{"bitvoice web phone", "BitVoice browser phone"}, {"bitvoice call generator", "BitVoice call generator"},
	} {
		if strings.Contains(l, v.key) {
			return v.name
		}
	}
	return ""
}

func (s *Server) compatibilityRoutes() {
	s.handle("GET /api/compatibility", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		type seen struct {
			store.SeenDevice
			Vendor string `json:"vendor"`
		}
		var here []seen
		for _, d := range s.Store.SeenDevices() {
			here = append(here, seen{d, vendorOf(d.UserAgent)})
		}
		if here == nil {
			here = []seen{}
		}
		writeJSON(w, 200, map[string]any{"tested": tested, "seen": here, "trunks": s.Engine.TrunkStatus()})
	})
}
