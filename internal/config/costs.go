package config

import (
	"crypto/x509"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/sms"
	"math"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// Rate is what a provider charges for calls to numbers starting with Prefix.
type Rate struct {
	// Prefix is the start of the number as sent to the provider, digits only ("1900", "44"); empty for
	// every number no other rate covers.
	Prefix string `json:"prefix"`
	// Name says what it is: "UK mobiles", "Premium rate".
	Name string `json:"name"`
	// PerMinute is the price of a minute, in the system currency.
	PerMinute float64 `json:"perMinute"`
	// Setup is charged once on each answered call.
	Setup float64 `json:"setup"`
	// Increment is the billing step in seconds (60 for per minute, 6 or 1); 60 when unset.
	Increment int `json:"increment"`
	// Minimum is the shortest charge in seconds (often 60, or 0).
	Minimum int `json:"minimum"`
}

// Costs is cost tracking: the currency, budgets and the runaway-spend alarm.
type Costs struct {
	// Currency is an ISO 4217 code shown with every amount; USD when unset.
	Currency string `json:"currency"`
	// Budgets alert by email (and in the web interface) at 80% and 100% of a month's spend.
	Budgets []Budget `json:"budgets"`
	// HourlyLimit raises an alarm when outgoing calls cost more than this within an hour: the usual
	// sign of toll fraud. 0 for none.
	HourlyLimit float64 `json:"hourlyLimit"`
	// AlertEmail receives the alarm and budget alerts besides any budget's own address.
	AlertEmail string `json:"alertEmail"`
}

// Budget is a month's spend limit for everything or for one department.
type Budget struct {
	ID         string  `json:"id"`
	Department string  `json:"department"` // empty for every call
	Monthly    float64 `json:"monthly"`
	Email      string  `json:"email"`
}

// RateFor finds the trunk's rate for a number: the longest prefix that matches.
func (t Trunk) RateFor(number string) (Rate, bool) {
	d := digitsOnly(number)
	best, found := Rate{}, false
	for _, r := range t.Rates {
		if strings.HasPrefix(d, r.Prefix) && (!found || len(r.Prefix) > len(best.Prefix)) {
			best, found = r, true
		}
	}
	return best, found
}

// Charge is what a call of billsec answered seconds costs at this rate, rounded to 4 decimals.
func (r Rate) Charge(billsec int) float64 {
	if billsec <= 0 {
		return 0
	}
	inc := r.Increment
	if inc <= 0 {
		inc = 60
	}
	secs := max(billsec, r.Minimum)
	secs = (secs + inc - 1) / inc * inc
	return math.Round((r.Setup+r.PerMinute*float64(secs)/60)*10000) / 10000
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && !strings.ContainsAny(s, "\r\n,;")
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func (c *Config) validateCosts(p *Problems) {
	k := &c.System.Costs
	k.Currency = strings.ToUpper(strings.TrimSpace(k.Currency))
	if k.Currency != "" && !currencyCode.MatchString(k.Currency) {
		p.add("system.costs.currency", "use a three-letter currency code such as USD, EUR or GBP")
	}
	if k.HourlyLimit < 0 {
		p.add("system.costs.hourlyLimit", "use 0 for no alarm, or an amount")
	}
	k.AlertEmail = strings.TrimSpace(k.AlertEmail)
	if k.AlertEmail != "" && !validEmail(k.AlertEmail) {
		p.add("system.costs.alertEmail", "enter an email address")
	}
	seen := map[string]bool{}
	for i := range k.Budgets {
		b := &k.Budgets[i]
		f := fmt.Sprintf("system.costs.budgets[%d].", i)
		b.Department, b.Email = strings.TrimSpace(b.Department), strings.TrimSpace(b.Email)
		if b.ID == "" {
			b.ID = NewID()
		}
		if b.Monthly <= 0 {
			p.add(f+"monthly", "enter the month's budget")
		}
		key := strings.ToLower(b.Department)
		if seen[key] {
			p.add(f+"department", "there is already a budget for this")
		}
		seen[key] = true
		if b.Email != "" && !validEmail(b.Email) {
			p.add(f+"email", "enter an email address")
		}
	}
	for ti := range c.Trunks {
		t := &c.Trunks[ti]
		if t.InboundPerMinute < 0 {
			p.add(fmt.Sprintf("trunks[%d].inboundPerMinute", ti), "use 0 or a price")
		}
		pre := map[string]bool{}
		for i := range t.Rates {
			r := &t.Rates[i]
			f := fmt.Sprintf("trunks[%d].rates[%d].", ti, i)
			r.Prefix, r.Name = strings.TrimSpace(r.Prefix), strings.TrimSpace(r.Name)
			if digitsOnly(r.Prefix) != r.Prefix {
				p.add(f+"prefix", "use digits only, as the number is sent to the provider (no + or spaces)")
			}
			if pre[r.Prefix] {
				p.add(f+"prefix", "another rate already starts with these digits")
			}
			pre[r.Prefix] = true
			if r.PerMinute < 0 || r.Setup < 0 {
				p.add(f+"perMinute", "use 0 or a price")
			}
			if r.Increment < 0 || r.Increment > 3600 {
				p.add(f+"increment", "use 1 to 3600 seconds (60 when empty)")
			}
			if r.Minimum < 0 || r.Minimum > 3600 {
				p.add(f+"minimum", "use 0 to 3600 seconds")
			}
		}
	}
}

// ConsentFor is how a recorded call with this outside number is to be treated: the matching rule
// (longest prefix), else the notice setting. "never" means it is not recorded.
func (r Recording) ConsentFor(number string) (action, rule string) {
	d := digitsOnly(number)
	best := -1
	for i, x := range r.Rules {
		if d != "" && strings.HasPrefix(d, x.Prefix) && (best < 0 || len(x.Prefix) > len(r.Rules[best].Prefix)) {
			best = i
		}
	}
	if best >= 0 {
		return r.Rules[best].Action, r.Rules[best].Name
	}
	if r.Notice == "" {
		return "off", ""
	}
	return r.Notice, ""
}

// consentRank orders recording actions from the most to the least protective of the people on a call.
var consentRank = map[string]int{"never": 4, "announce": 3, "beep": 2, "off": 1}

// ConsentForParties chooses how a recorded call is treated when its parties are under different rules (a caller
// in a place that needs every party's consent, an office in a place that needs one): the most protective of the
// rules that apply to any of the numbers wins. It returns the action and, for each number, the rule that applied.
func (r Recording) ConsentForParties(numbers ...string) (action string, applied []string) {
	best := ""
	for _, n := range numbers {
		if digitsOnly(n) == "" {
			continue
		}
		a, rule := r.ConsentFor(n)
		if rule == "" {
			rule = "no rule, so the usual notice"
		}
		applied = append(applied, n+" ("+rule+": "+a+")")
		if consentRank[a] > consentRank[best] {
			best = a
		}
	}
	if best == "" {
		best, _ = r.ConsentFor("")
	}
	return best, applied
}

func (c *Config) validateConsent(p *Problems) {
	r := &c.System.Recording
	switch r.Notice {
	case "", "off", "announce", "beep":
	default:
		p.add("system.recording.notice", "choose off, announce or beep")
	}
	seen := map[string]bool{}
	for i := range r.Rules {
		x := &r.Rules[i]
		f := fmt.Sprintf("system.recording.rules[%d].", i)
		x.Prefix, x.Name = strings.TrimPrefix(strings.TrimSpace(x.Prefix), "+"), strings.TrimSpace(x.Name)
		if x.Prefix == "" || digitsOnly(x.Prefix) != x.Prefix {
			p.add(f+"prefix", "use the digits the numbers start with, such as 1415 or 49")
		} else if seen[x.Prefix] {
			p.add(f+"prefix", "another rule already covers these digits")
		}
		seen[x.Prefix] = true
		switch x.Action {
		case "announce", "beep", "off", "never":
		default:
			p.add(f+"action", "choose announce, beep, off or never")
		}
	}
}

func (c *Config) validateVIPs(p *Problems) {
	seen := map[string]bool{}
	for i := range c.System.VIPs {
		v := &c.System.VIPs[i]
		f := fmt.Sprintf("system.vips[%d].", i)
		v.Number = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(v.Number))
		v.Name = strings.TrimSpace(v.Name)
		d := strings.TrimPrefix(v.Number, "+")
		if len(d) < 3 || digitsOnly(d) != d {
			p.add(f+"number", "enter the caller's number")
		} else if seen[digitsOnly(d)] {
			p.add(f+"number", "this number is already a VIP")
		}
		seen[digitsOnly(d)] = true
	}
	for i := range c.Queues {
		if q := &c.Queues[i]; q.StickyDays < 0 || q.StickyDays > 365 {
			p.add(fmt.Sprintf("queues[%d].stickyDays", i), "use 1 to 365 days, or 0 for 30")
		}
	}
}

// IsVIP says whether a caller's number is on the VIP list (+1 212 555 0100 and 2125550100 match).
func (c *Config) IsVIP(number string) (VIP, bool) {
	for _, v := range c.System.VIPs {
		if SameNumber(v.Number, number) {
			return v, true
		}
	}
	return VIP{}, false
}

// SameNumber says whether two numbers are the same line: equal digits, or the same last ten when
// both have ten or more.
func SameNumber(a, b string) bool {
	da, db := digitsOnly(a), digitsOnly(b)
	if da == "" || db == "" {
		return false
	}
	if da == db {
		return true
	}
	return len(da) >= 10 && len(db) >= 10 && da[len(da)-10:] == db[len(db)-10:]
}

func (c *Config) validateScreening(p *Problems) {
	s := &c.System.Screening
	choose := func(f string, v *string, def string, ok ...string) {
		if *v == "" {
			*v = def
		}
		for _, o := range ok {
			if *v == o {
				return
			}
		}
		p.add(f, "choose %s", strings.Join(ok, ", "))
	}
	choose("system.screening.failed", &s.Failed, "label", "allow", "label", "block")
	choose("system.screening.unverified", &s.Unverified, "allow", "allow", "label")
	choose("system.screening.anonymous", &s.Anonymous, "allow", "allow", "label", "block")
	s.Label = strings.TrimSpace(s.Label)
	if len(s.Label) > 20 || strings.ContainsAny(s.Label, "\"<>\r\n") {
		p.add("system.screening.label", "use up to 20 characters, without quotes or angle brackets")
	}
	for name, list := range map[string][]ScreenNumber{"blocked": s.Blocked, "allowed": s.Allowed} {
		for i := range list {
			n := &list[i]
			n.Number = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(n.Number))
			body := strings.TrimSuffix(strings.TrimPrefix(n.Number, "+"), "*")
			if len(body) < 3 || digitsOnly(body) != body {
				p.add(fmt.Sprintf("system.screening.%s[%d].number", name, i), "enter a number, or its first digits and *")
			}
		}
	}
	if r := strings.TrimSpace(s.STIRoots); r != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(r)) {
			p.add("system.screening.stiRoots", "paste the certificates in PEM form (-----BEGIN CERTIFICATE-----)")
		}
	}
}

func (c *Config) validateTexting(p *Problems) {
	x := &c.System.Texting
	ids := map[string]bool{}
	for i := range x.Providers {
		v := &x.Providers[i]
		f := fmt.Sprintf("system.texting.providers[%d].", i)
		if v.ID == "" {
			v.ID = NewID()
		}
		ids[v.ID] = true
		v.Name, v.Username, v.BaseURL = strings.TrimSpace(v.Name), strings.TrimSpace(v.Username), strings.TrimSpace(v.BaseURL)
		if v.Name == "" {
			p.add(f+"name", "enter a name")
		}
		v.Account, v.App = strings.TrimSpace(v.Account), strings.TrimSpace(v.App)
		k, ok := sms.KindOf(v.Kind)
		if !ok {
			p.add(f+"kind", "choose a provider")
		}
		if ok && k.Username != "" && v.Username == "" {
			p.add(f+"username", "%s", "enter the "+lowerLabel(k.Username))
		}
		if ok && v.Key == "" && v.Kind != "webhook" {
			p.add(f+"key", "%s", "enter the "+lowerLabel(k.Key))
		}
		if ok && k.Account != "" && v.Account == "" {
			p.add(f+"account", "%s", "enter the "+lowerLabel(k.Account))
		}
		if ok && k.App != "" && v.App == "" {
			p.add(f+"app", "%s", "enter the "+lowerLabel(k.App))
		}
		if ok && k.BaseRequired && v.BaseURL == "" {
			p.add(f+"baseUrl", "%s", "enter the "+lowerLabel(strings.SplitN(k.Base, " (", 2)[0]))
		}
		if v.HookSecret == "" {
			v.HookSecret = NewID() + NewID()
		}
		if v.BaseURL != "" {
			if u, err := url.Parse(v.BaseURL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
				p.add(f+"baseUrl", "use a web address starting with https://")
			}
		}
	}
	seen := map[string]bool{}
	for i := range x.Numbers {
		n := &x.Numbers[i]
		f := fmt.Sprintf("system.texting.numbers[%d].", i)
		n.Number = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(n.Number))
		if !strings.HasPrefix(n.Number, "+") || len(n.Number) < 8 || digitsOnly(n.Number[1:]) != n.Number[1:] {
			p.add(f+"number", "enter the number with its country code, such as +12125550100")
		} else if seen[n.Number] {
			p.add(f+"number", "this number is already listed")
		}
		seen[n.Number] = true
		if !ids[n.Provider] {
			p.add(f+"provider", "choose the provider that carries this number")
		}
		for _, e := range n.Notify {
			if c.FindExtension(e) == nil {
				p.add(f+"notify", "no extension %s", e)
			}
		}
	}
	for _, s := range []*string{&x.TenDLC.Brand, &x.TenDLC.Campaign} {
		if *s == "" {
			*s = "not-started"
		}
	}
	for f, v := range map[string]string{"brand": x.TenDLC.Brand, "campaign": x.TenDLC.Campaign} {
		if v != "not-started" && v != "submitted" && v != "approved" {
			p.add("system.texting.tenDlc."+f, "choose not started, submitted or approved")
		}
	}
}

// SMSNumberOf finds one of our texting numbers.
func (c *Config) SMSNumberOf(n string) *SMSNumber {
	for i := range c.System.Texting.Numbers {
		if SameNumber(c.System.Texting.Numbers[i].Number, n) {
			return &c.System.Texting.Numbers[i]
		}
	}
	return nil
}

// SMSProviderOf finds a texting provider by ID.
func (c *Config) SMSProviderOf(id string) *SMSProvider {
	for i := range c.System.Texting.Providers {
		if c.System.Texting.Providers[i].ID == id {
			return &c.System.Texting.Providers[i]
		}
	}
	return nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]{8,64}$`)

func (c *Config) validateCallLinks(p *Problems) {
	slugs := map[string]bool{}
	for i := range c.System.CallLinks {
		l := &c.System.CallLinks[i]
		f := fmt.Sprintf("system.callLinks[%d].", i)
		if l.ID == "" {
			l.ID = NewID()
		}
		l.Name, l.Title, l.Intro = strings.TrimSpace(l.Name), strings.TrimSpace(l.Title), strings.TrimSpace(l.Intro)
		if l.Slug == "" {
			l.Slug = strings.ToLower(NewID() + NewID())
		}
		if !slugRe.MatchString(l.Slug) {
			p.add(f+"slug", "use 8 to 64 lower-case letters, digits or dashes")
		} else if slugs[l.Slug] {
			p.add(f+"slug", "another link already has this address")
		}
		slugs[l.Slug] = true
		if l.Name == "" {
			p.add(f+"name", "enter a name")
		}
		if l.Title == "" {
			l.Title = "Call " + c.System.Name
		}
		if len(l.Title) > 80 || len(l.Intro) > 300 {
			p.add(f+"title", "keep the title under 80 characters and the words under 300")
		}
		if l.MaxCalls < 0 || l.MaxCalls > 100 {
			p.add(f+"maxCalls", "use 1 to 100, or 0 for 5")
		}
		switch l.Destination.Type {
		case "extension", "ringgroup", "queue", "menu", "voicemail", "receptionist", "schedule", "echo":
		default:
			p.add(f+"destination", "choose where calls go: an extension, a group, a queue, a menu, a receptionist, a schedule, voicemail or the echo test")
		}
	}
}

// FindCallLink finds a call link by its address.
func (c *Config) FindCallLink(slug string) *CallLink {
	for i := range c.System.CallLinks {
		if c.System.CallLinks[i].Slug == slug {
			return &c.System.CallLinks[i]
		}
	}
	return nil
}

func (c *Config) validateCalendars(p *Problems) {
	for i := range c.Extensions {
		x := &c.Extensions[i]
		k := &x.Calendar
		f := fmt.Sprintf("extensions[%d].calendar.", i)
		k.Secret, k.ForwardTo = strings.TrimSpace(k.Secret), strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(k.ForwardTo))
		if k.Secret != "" {
			u, err := url.Parse(strings.Replace(k.Secret, "webcal://", "https://", 1))
			if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
				p.add(f+"secret", "paste the calendar's private address (it starts with https:// or webcal://)")
			}
		}
		switch k.WhenBusy {
		case "", "voicemail", "ring":
		case "forward":
			if k.ForwardTo == "" {
				p.add(f+"forwardTo", "enter where calls go: an extension or a phone number")
			} else if c.FindExtension(k.ForwardTo) == nil && !x.Outbound {
				p.add(f+"forwardTo", "this extension may not call outside, so it cannot forward to an outside number")
			}
		default:
			p.add(f+"whenBusy", "choose voicemail, forward or ring")
		}
	}
}

// BrandFor finds the brand of an outgoing number, matching its digits.
func (s *System) BrandFor(number string) *Brand {
	d := digitsOnly(number)
	if d == "" {
		return nil
	}
	for i := range s.Brands {
		if SameNumber(digitsOnly(s.Brands[i].Number), d) {
			return &s.Brands[i]
		}
	}
	return nil
}

func (c *Config) validateBrands(p *Problems) {
	seen := map[string]bool{}
	for i := range c.System.Brands {
		b := &c.System.Brands[i]
		f := fmt.Sprintf("system.brands[%d].", i)
		b.Number, b.Name, b.Logo, b.Reason = strings.TrimSpace(b.Number), strings.TrimSpace(b.Name), strings.TrimSpace(b.Logo), strings.TrimSpace(b.Reason)
		d := digitsOnly(b.Number)
		switch {
		case len(d) < 6:
			p.add(f+"number", "enter the full outgoing number, such as +12125550100")
		case seen[d]:
			p.add(f+"number", "this number is listed twice")
		}
		seen[d] = true
		if len([]rune(b.Name)) > 32 {
			p.add(f+"name", "keep the name to 32 characters: longer names are cut off on phones")
		}
		if strings.ContainsAny(b.Name+b.Reason, "<>\"\r\n") {
			p.add(f+"name", "the name and reason may not contain < > or quotes")
		}
		if b.Logo != "" {
			if u, err := url.Parse(b.Logo); err != nil || u.Scheme != "https" || u.Host == "" {
				p.add(f+"logo", "the logo must be an https:// address the carrier can fetch")
			}
		}
		if len([]rune(b.Reason)) > 64 {
			p.add(f+"reason", "keep the reason to 64 characters")
		}
	}
}

func (c *Config) validateCluster(p *Problems) {
	k := &c.System.Cluster
	k.Name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(k.Name), "."))
	k.SharedAddress, k.Interface = strings.TrimSpace(k.SharedAddress), strings.TrimSpace(k.Interface)
	if k.Name != "" && (!hostnameRe.MatchString(k.Name) || !strings.Contains(k.Name, ".")) {
		p.add("system.cluster.name", "enter a DNS name such as sip.example.com")
	}
	if k.SharedAddress != "" {
		pr, err := netip.ParsePrefix(k.SharedAddress)
		switch {
		case err != nil:
			p.add("system.cluster.sharedAddress", "enter the address with its network size, such as 192.0.2.50/24")
		case pr.Addr().IsLoopback() || pr.Addr().IsUnspecified() || pr.Addr().IsMulticast():
			p.add("system.cluster.sharedAddress", "use an address on the network the phones reach")
		case pr.Addr().Is4() && pr.Bits() < 8, pr.Addr().Is6() && pr.Bits() < 48:
			p.add("system.cluster.sharedAddress", "the network size looks wrong: use the one the servers' own addresses have")
		}
	}
	if k.Interface != "" && !regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`).MatchString(k.Interface) {
		p.add("system.cluster.interface", "enter an interface name such as eth0")
	}
}

// lowerLabel lowers a field label for a sentence, keeping ID, API and SID as they are.
func lowerLabel(s string) string {
	w := strings.Fields(s)
	for i, x := range w {
		if x != "ID" && x != "API" && x != "SID" {
			w[i] = strings.ToLower(x)
		}
	}
	return strings.Join(w, " ")
}
