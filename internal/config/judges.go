package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// FraudGuard judges every call out of the country before it is placed (the toll-fraud rulebook,
// internal/reason): it lets it through, tells the administrator, asks for a PIN or refuses it, and says why.
// Emergency calls are never stopped.
type FraudGuard struct {
	On bool `json:"on"`
	// Schedule is the business hours (a schedule's ID): a first call abroad outside them asks for a PIN.
	// Empty leaves hours out of it.
	Schedule string `json:"schedule"`
	// Burst calls abroad from one extension within 10 minutes are refused (default 5).
	Burst int `json:"burst"`
	// AllowedCountries are calling codes calls may always go to ("44", "1876"): a first call there is not unusual.
	AllowedCountries []string `json:"allowedCountries"`
	// PINSet is asked for when the guard wants a PIN (a PIN set's ID); with none, such a call is refused.
	PINSet string `json:"pinSet"`
	// Exceptions excuse an extension until a day (travel, a project abroad). Many calls in a short time and an
	// attack in progress are never excused: a stolen password looks approved too.
	Exceptions []FraudException `json:"exceptions"`
}

// FraudException excuses one extension's calls abroad until the end of a day.
type FraudException struct {
	Ext   string `json:"ext"`
	Until string `json:"until"` // YYYY-MM-DD, in the system's time zone, inclusive
	Note  string `json:"note"`
}

// BurstOrDefault is Burst, or 5.
func (f FraudGuard) BurstOrDefault() int {
	if f.Burst > 0 {
		return f.Burst
	}
	return 5
}

// Excused reports an exception covering ext on the day of t.
func (f FraudGuard) Excused(ext string, t time.Time) bool {
	day := t.Format("2006-01-02")
	for _, x := range f.Exceptions {
		if x.Ext == ext && x.Until >= day {
			return true
		}
	}
	return false
}

// TrunkJudging decides, every 15 seconds, whether each trunk is used in its place in the outbound routes or
// tried after the others (the trunk-health rulebook, internal/reason). A trunk is never taken out altogether.
type TrunkJudging struct {
	Off bool `json:"off"`
	// Failures calls in a row failing on the provider's side, within WindowMinutes, mean it is refusing calls.
	Failures      int `json:"failures"`
	WindowMinutes int `json:"windowMinutes"`
}

// FailuresOrDefault is Failures, or 3.
func (j TrunkJudging) FailuresOrDefault() int {
	if j.Failures > 0 {
		return j.Failures
	}
	return 3
}

// WindowOrDefault is the window, or 10 minutes.
func (j TrunkJudging) WindowOrDefault() time.Duration {
	if j.WindowMinutes > 0 {
		return time.Duration(j.WindowMinutes) * time.Minute
	}
	return 10 * time.Minute
}

func (c *Config) validateJudges(p *Problems) {
	f := &c.Security.FraudGuard
	if f.Schedule != "" && c.FindSchedule(f.Schedule) == nil {
		p.add("security.fraudGuard.schedule", "choose one of the schedules, or none")
	}
	if f.PINSet != "" && c.FindPINSet(f.PINSet) == nil {
		p.add("security.fraudGuard.pinSet", "choose one of the PIN sets, or none")
	}
	if f.Burst < 0 || f.Burst > 1000 {
		p.add("security.fraudGuard.burst", "use a number of calls from 1 to 1000, or 0 for 5")
	}
	if f.AllowedCountries == nil {
		f.AllowedCountries = []string{}
	}
	if f.Exceptions == nil {
		f.Exceptions = []FraudException{}
	}
	for i, cc := range f.AllowedCountries {
		cc = strings.TrimPrefix(strings.TrimSpace(cc), "+")
		f.AllowedCountries[i] = cc
		if cc == "" || digitsOnly(cc) != cc || len(cc) > 4 {
			p.add(fmt.Sprintf("security.fraudGuard.allowedCountries[%d]", i), "use a country calling code, such as 44 or 1876")
		}
	}
	for i := range f.Exceptions {
		x := &f.Exceptions[i]
		x.Ext, x.Note = strings.TrimSpace(x.Ext), strings.TrimSpace(x.Note)
		at := fmt.Sprintf("security.fraudGuard.exceptions[%d].", i)
		if c.FindExtension(x.Ext) == nil {
			p.add(at+"ext", "choose one of the extensions")
		}
		if _, err := time.Parse("2006-01-02", x.Until); err != nil {
			p.add(at+"until", "choose the last day it applies")
		}
	}
	j := &c.System.TrunkJudging
	if j.Failures < 0 || j.Failures > 50 {
		p.add("system.trunkJudging.failures", "use 1 to 50 calls, or 0 for 3")
	}
	if j.WindowMinutes < 0 || j.WindowMinutes > 120 {
		p.add("system.trunkJudging.windowMinutes", "use 1 to 120 minutes, or 0 for 10")
	}
}

// callingCodes are the country calling codes by ISO country, and the Caribbean and other places that share +1
// but are other countries (a favourite of toll fraud: they look like national calls from North America).
var callingCodes = map[string]string{
	"US": "1", "CA": "1", "GB": "44", "IE": "353", "FR": "33", "DE": "49", "ES": "34", "IT": "39", "PT": "351",
	"NL": "31", "BE": "32", "LU": "352", "CH": "41", "AT": "43", "DK": "45", "SE": "46", "NO": "47", "FI": "358",
	"IS": "354", "PL": "48", "CZ": "420", "SK": "421", "HU": "36", "RO": "40", "BG": "359", "GR": "30", "HR": "385",
	"SI": "386", "EE": "372", "LV": "371", "LT": "370", "MT": "356", "CY": "357", "AU": "61", "NZ": "64", "JP": "81",
	"KR": "82", "CN": "86", "HK": "852", "SG": "65", "IN": "91", "ZA": "27", "MX": "52", "BR": "55", "AR": "54",
	"CL": "56", "CO": "57", "PE": "51", "IL": "972", "AE": "971", "SA": "966", "TR": "90", "PH": "63", "MY": "60",
	"TH": "66", "ID": "62", "VN": "84", "EG": "20", "NG": "234", "KE": "254", "UA": "380", "RU": "7",
}

// otherNANP are +1 area codes of other countries.
var otherNANP = []string{"242", "246", "264", "268", "284", "345", "441", "473", "649", "658", "664", "721", "758",
	"767", "784", "809", "829", "849", "868", "869", "876"}

// CallingCode is the system's own country calling code ("1", "44"), or "".
func (s System) CallingCode() string { return callingCodes[strings.ToUpper(s.Country)] }

// Abroad reports whether a number, as sent to a provider, leaves the country, and the calling code it goes to
// ("44", "1876"). It understands +, 00 and (in North America) 011, and the +1 places that are other countries.
func (s System) Abroad(number string) (bool, string) {
	home := s.CallingCode()
	n := strings.TrimSpace(number)
	intl := ""
	switch {
	case strings.HasPrefix(n, "+"):
		intl = n[1:]
	case home == "1" && strings.HasPrefix(n, "011"):
		intl = n[3:]
	case home != "1" && strings.HasPrefix(n, "00"):
		intl = n[2:]
	case home == "1":
		d := strings.TrimPrefix(n, "1")
		if len(d) == 10 && slices.Contains(otherNANP, d[:3]) {
			return true, "1" + d[:3]
		}
		return false, ""
	default:
		return false, ""
	}
	intl = digitsOnly(intl)
	if intl == "" {
		return false, ""
	}
	if strings.HasPrefix(intl, "1") && len(intl) >= 4 && slices.Contains(otherNANP, intl[1:4]) {
		return true, "1" + intl[1:4]
	}
	code := ""
	for _, cc := range callingCodes {
		if strings.HasPrefix(intl, cc) && len(cc) > len(code) {
			code = cc
		}
	}
	if code == "" {
		code = intl[:min(3, len(intl))] // a code not in the table: its first digits stand for it
	}
	return code != home, code
}
