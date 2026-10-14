package config

import (
	"net/netip"
	"strings"
)

// Hotel holds the hospitality features: wake-up calls, guest rooms, and the link to the hotel's
// property management system (PMS), which checks guests in and out and is told of wake-up calls,
// room status from housekeeping and outside call charges.
type Hotel struct {
	// WakeUpTries is how many times an unanswered wake-up call is tried (3 when 0), RetryMinutes apart
	// (5 when 0). Unanswered after the last, the reception extension (Notify) is rung, when set.
	WakeUpTries        int    `json:"wakeUpTries"`
	WakeUpRetryMinutes int    `json:"wakeUpRetryMinutes"`
	Notify             string `json:"notify"`
	// Vacant rooms (no guest checked in) may call only extensions and emergency numbers.
	BarVacant bool `json:"barVacant"`
	// On check-out: delete the room's voicemail, cancel its wake-up call, take its do not disturb and
	// forwarding off.
	CheckOutClear bool `json:"checkOutClear"`
	PMS           PMS  `json:"pms"`
}

// PMS is the link to the hotel system. Kind fias connects to a FIAS interface (Oracle Opera and the
// many systems that speak it) over TCP; api lets the hotel system call BitVoice's REST API with a
// key (Settings, Automation) and, when ChargeURL is set, receives call charges as JSON.
type PMS struct {
	Kind string `json:"kind"` // "", fias or api
	// Host and Port of the FIAS interface (the hotel system listens; BitVoice connects).
	Host string `json:"host"`
	Port int    `json:"port"`
	// ChargeCalls posts each outside call from a checked-in room to the guest's bill.
	ChargeCalls bool `json:"chargeCalls"`
	// ChargeURL receives charges as JSON (api); FIAS sends them on the link.
	ChargeURL string `json:"chargeUrl"`
	// Markup is added to the provider's cost of a call before it is charged, in percent.
	Markup int `json:"markup"`
}

func (c *Config) validateHotel(p *Problems) {
	h := &c.System.Hotel
	if h.WakeUpTries < 0 || h.WakeUpTries > 10 {
		p.add("system.hotel.wakeUpTries", "try 1 to 10 times")
	}
	if h.WakeUpRetryMinutes < 0 || h.WakeUpRetryMinutes > 60 {
		p.add("system.hotel.wakeUpRetryMinutes", "retry every 1 to 60 minutes")
	}
	if h.Notify != "" && c.FindExtension(h.Notify) == nil {
		p.add("system.hotel.notify", "no extension %s", h.Notify)
	}
	pm := &h.PMS
	if pm.Markup < 0 || pm.Markup > 1000 {
		p.add("system.hotel.pms.markup", "the markup is 0 to 1000 percent")
	}
	pm.Host = strings.TrimSpace(pm.Host)
	switch pm.Kind {
	case "":
	case "fias":
		if _, err := netip.ParseAddr(pm.Host); err != nil && !hostRe.MatchString(pm.Host) {
			p.add("system.hotel.pms.host", "enter the hotel system's address")
		}
		if pm.Port == 0 {
			pm.Port = 5010
		}
		if pm.Port < 1 || pm.Port > 65535 {
			p.add("system.hotel.pms.port", "enter the FIAS port, such as 5010")
		}
	case "api":
		if pm.ChargeURL != "" && !strings.HasPrefix(pm.ChargeURL, "https://") && !strings.HasPrefix(pm.ChargeURL, "http://") {
			p.add("system.hotel.pms.chargeUrl", "use an http:// or https:// address")
		}
	default:
		p.add("system.hotel.pms.kind", "choose none, fias or api")
	}
}

// WakeUpTries is the number of tries, with the default.
func (h Hotel) Tries() int {
	if h.WakeUpTries == 0 {
		return 3
	}
	return h.WakeUpTries
}

// RetryMinutes is the time between tries, with the default.
func (h Hotel) RetryMinutes() int {
	if h.WakeUpRetryMinutes == 0 {
		return 5
	}
	return h.WakeUpRetryMinutes
}
