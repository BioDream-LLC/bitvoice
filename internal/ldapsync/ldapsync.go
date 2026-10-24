// Package ldapsync reads people and groups from the company directory (internal/ldap) and works out
// what that means for the phone system: extensions to make, update, turn off and on, ring groups'
// members, and who may sign in as what.
package ldapsync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/ldap"
)

// Person is someone in the directory.
type Person struct {
	DN       string   `json:"dn"`
	Login    string   `json:"login"`
	Name     string   `json:"name"`
	Email    string   `json:"email,omitempty"`
	Number   string   `json:"number,omitempty"` // the extension number worked out, "" for none
	Raw      string   `json:"raw,omitempty"`    // the number as the directory has it
	Groups   []string `json:"-"`                // DNs of the groups the person is in (memberOf)
	Disabled bool     `json:"disabled,omitempty"`
}

// Dial connects and binds as the reading account.
func Dial(ctx context.Context, d config.LDAP) (*ldap.Conn, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if d.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(d.CACert)) {
			return nil, errors.New("the certificate authority is not PEM")
		}
		tc.RootCAs = pool
	}
	c, err := ldap.Dial(ctx, ldap.Options{URL: d.URL, StartTLS: d.StartTLS, TLS: tc, Timeout: 15 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := c.Bind(d.BindDN, d.BindPassword); err != nil {
		c.Close()
		return nil, fmt.Errorf("the reading account could not sign in: %w", err)
	}
	return c, nil
}

// numberOf works out the extension number from the directory's: its digits, the last NumberDigits.
func numberOf(raw string, keep int) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsDigit(r) && r < 128 {
			b.WriteRune(r)
		}
	}
	n := b.String()
	if keep > 0 && len(n) > keep {
		n = n[len(n)-keep:]
	}
	if len(n) < 2 || len(n) > 8 {
		return ""
	}
	return n
}

func attrs(d config.LDAP) []string {
	return []string{d.LoginAttr, d.NameAttr, d.EmailAttr, d.NumberAttr, "memberOf", "userAccountControl", "cn"}
}

func personOf(d config.LDAP, e ldap.Entry) Person {
	p := Person{DN: e.DN, Login: e.Get(d.LoginAttr), Name: e.Get(d.NameAttr), Email: e.Get(d.EmailAttr), Raw: e.Get(d.NumberAttr), Groups: e.All("memberOf")}
	if p.Name == "" {
		p.Name = e.Get("cn")
	}
	if p.Name == "" {
		p.Name = p.Login
	}
	p.Number = numberOf(p.Raw, d.NumberDigits)
	if uac := e.Get("userAccountControl"); uac != "" {
		var v int
		fmt.Sscan(uac, &v)
		p.Disabled = v&2 != 0 // ACCOUNTDISABLE
	}
	return p
}

// People reads everyone the user filter picks.
func People(c *ldap.Conn, d config.LDAP) ([]Person, error) {
	es, err := c.Search(d.BaseDN, ldap.ScopeSubtree, d.UserFilter, attrs(d), 500, 50000)
	if err != nil {
		return nil, err
	}
	var out []Person
	for _, e := range es {
		p := personOf(d, e)
		if p.Login != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// group finds a group by DN or name (cn).
func group(c *ldap.Conn, d config.LDAP, name string) (ldap.Entry, error) {
	want := []string{"member", "uniqueMember", "memberUid", "cn"}
	if strings.Contains(name, "=") {
		es, err := c.Search(name, ldap.ScopeBase, "", want, 0, 1)
		if err == nil && len(es) == 1 {
			return es[0], nil
		}
		if err != nil && !ldap.IsCode(err, ldap.NoSuchObject) {
			return ldap.Entry{}, err
		}
		return ldap.Entry{}, fmt.Errorf("there is no group %s", name)
	}
	es, err := c.Search(d.BaseDN, ldap.ScopeSubtree, "(&(cn="+ldap.EscapeFilter(name)+")(|(objectClass=group)(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=posixGroup)))", want, 0, 2)
	if err != nil {
		return ldap.Entry{}, err
	}
	if len(es) == 0 {
		return ldap.Entry{}, fmt.Errorf("there is no group named %s", name)
	}
	if len(es) > 1 {
		return ldap.Entry{}, fmt.Errorf("more than one group is named %s: give its DN", name)
	}
	return es[0], nil
}

// members are the logins of a group's people: on Active Directory nested groups included (the
// LDAP_MATCHING_RULE_IN_CHAIN), elsewhere the group's member, uniqueMember and memberUid values.
func members(c *ldap.Conn, d config.LDAP, g ldap.Entry, byDN map[string]Person) ([]string, error) {
	var out []string
	if d.Kind == "ad" {
		es, err := c.Search(d.BaseDN, ldap.ScopeSubtree, "(&"+d.UserFilter+"(memberOf:1.2.840.113556.1.4.1941:="+ldap.EscapeFilter(g.DN)+"))", []string{d.LoginAttr}, 500, 50000)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if l := e.Get(d.LoginAttr); l != "" {
				out = append(out, l)
			}
		}
		return out, nil
	}
	for _, dn := range append(g.All("member"), g.All("uniqueMember")...) {
		if p, ok := byDN[strings.ToLower(dn)]; ok {
			out = append(out, p.Login)
		}
	}
	out = append(out, g.All("memberUid")...)
	return out, nil
}

// Report is what a sync did (or would do).
type Report struct {
	At         time.Time `json:"at"`
	People     int       `json:"people"`
	WithNumber int       `json:"withNumber"`
	Created    []string  `json:"created"`
	Updated    []string  `json:"updated"`
	Disabled   []string  `json:"disabled"`
	Enabled    []string  `json:"enabled"`
	RingGroups []string  `json:"ringGroups"`
	Problems   []string  `json:"problems"`
	Error      string    `json:"error,omitempty"`
	Changed    bool      `json:"changed"`
}

// NewReport is an empty report.
func NewReport() Report {
	return Report{At: time.Now().UTC(), Created: []string{}, Updated: []string{}, Disabled: []string{}, Enabled: []string{}, RingGroups: []string{}, Problems: []string{}}
}

// Sync reads the directory and changes cfg to match it (cfg is a copy the caller saves when Changed).
func Sync(ctx context.Context, cfg *config.Config) Report {
	r := NewReport()
	people, groups, err := Fetch(ctx, cfg.System.LDAP, &r)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	Apply(cfg, people, groups, &r)
	return r
}

// Fetch reads the people and the members of the ring groups' directory groups.
func Fetch(ctx context.Context, d config.LDAP, r *Report) ([]Person, map[string][]string, error) {
	c, err := Dial(ctx, d)
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	people, err := People(c, d)
	if err != nil {
		return nil, nil, errors.New("reading people: " + err.Error())
	}
	groupMembers := map[string][]string{}
	byDN := map[string]Person{}
	for _, p := range people {
		byDN[strings.ToLower(p.DN)] = p
	}
	for _, m := range d.RingGroups {
		g, err := group(c, d, m.Group)
		if err != nil {
			r.Problems = append(r.Problems, "ring group "+m.RingGroup+": "+err.Error())
			continue
		}
		ms, err := members(c, d, g, byDN)
		if err != nil {
			r.Problems = append(r.Problems, "ring group "+m.RingGroup+": "+err.Error())
			continue
		}
		groupMembers[m.Group] = ms
	}
	return people, groupMembers, nil
}

// Apply changes cfg to match the people and group members read, writing what it did into r.
func Apply(cfg *config.Config, people []Person, groupMembers map[string][]string, r *Report) {
	d := cfg.System.LDAP
	r.People = len(people)
	active := map[string]Person{}
	for _, p := range people {
		if p.Number != "" {
			r.WithNumber++
		} else if p.Raw != "" {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: the number %q is not 2 to 8 digits (set how many digits to keep)", p.Login, p.Raw))
		}
		if !p.Disabled {
			active[strings.ToLower(p.Login)] = p
		}
	}
	if d.SyncExtensions {
		taken := map[string]bool{}
		for _, x := range cfg.Extensions {
			taken[x.Number] = true
		}
		used := map[string]string{} // number -> login of whoever has it in this sync
		logins := make([]string, 0, len(active))
		for l := range active {
			logins = append(logins, l)
		}
		sort.Strings(logins)
		for _, l := range logins {
			p := active[l]
			if p.Number == "" {
				continue
			}
			if other, dup := used[p.Number]; dup {
				r.Problems = append(r.Problems, fmt.Sprintf("%s and %s both have number %s: only %s got it", other, p.Login, p.Number, other))
				continue
			}
			used[p.Number] = p.Login
			i := slices.IndexFunc(cfg.Extensions, func(x config.Extension) bool { return strings.EqualFold(x.LDAP, p.Login) })
			if i < 0 {
				// Not made from the directory yet: an extension of that number is taken over only when
				// it is plainly the same person (same name or email); otherwise it is left alone.
				j := slices.IndexFunc(cfg.Extensions, func(x config.Extension) bool { return x.Number == p.Number })
				if j >= 0 {
					x := &cfg.Extensions[j]
					if x.LDAP != "" || !(strings.EqualFold(x.Name, p.Name) || p.Email != "" && strings.EqualFold(x.Email, p.Email)) {
						r.Problems = append(r.Problems, fmt.Sprintf("%s: extension %s exists and is someone else's (%s); left alone", p.Login, p.Number, x.Name))
						continue
					}
					x.LDAP = p.Login
					r.Updated = append(r.Updated, x.Number+" "+x.Name+" (now from the directory)")
					r.Changed = true
					i = j // and brought up to date below
				}
			}
			if i < 0 {
				cfg.Extensions = append(cfg.Extensions, config.Extension{ID: config.NewID(), Number: p.Number, Name: p.Name, Email: p.Email,
					Secret: config.NewSecret(), Enabled: true, Outbound: true, RingSeconds: 25, LDAP: p.Login,
					Voicemail: config.Voicemail{Enabled: true, PIN: pin()}})
				taken[p.Number] = true
				r.Created = append(r.Created, p.Number+" "+p.Name)
				r.Changed = true
				continue
			}
			x := &cfg.Extensions[i]
			var what []string
			if x.Name != p.Name && p.Name != "" {
				x.Name = p.Name
				what = append(what, "name")
			}
			if x.Email != p.Email {
				x.Email = p.Email
				what = append(what, "email")
			}
			if x.Number != p.Number {
				if taken[p.Number] {
					r.Problems = append(r.Problems, fmt.Sprintf("%s's number became %s, which another extension has; kept %s", p.Login, p.Number, x.Number))
				} else {
					delete(taken, x.Number)
					renumber(cfg, x.Number, p.Number)
					x.Number = p.Number
					taken[p.Number] = true
					what = append(what, "number")
				}
			}
			if !x.Enabled && d.DisableMissing {
				x.Enabled = true
				r.Enabled = append(r.Enabled, x.Number+" "+x.Name)
				r.Changed = true
			}
			if len(what) > 0 {
				r.Updated = append(r.Updated, x.Number+" "+x.Name+" ("+strings.Join(what, ", ")+")")
				r.Changed = true
			}
		}
		if d.DisableMissing {
			for i := range cfg.Extensions {
				x := &cfg.Extensions[i]
				if x.LDAP == "" || !x.Enabled {
					continue
				}
				if p, ok := active[strings.ToLower(x.LDAP)]; !ok || p.Number == "" {
					x.Enabled = false
					r.Disabled = append(r.Disabled, x.Number+" "+x.Name)
					r.Changed = true
				}
			}
		}
	}
	for _, m := range d.RingGroups {
		ms, ok := groupMembers[m.Group]
		g := cfg.FindRingGroup(m.RingGroup)
		if !ok || g == nil {
			continue
		}
		var nums []string
		for _, l := range ms {
			for _, x := range cfg.Extensions {
				if strings.EqualFold(x.LDAP, l) && x.Enabled && !slices.Contains(nums, x.Number) {
					nums = append(nums, x.Number)
				}
			}
		}
		sort.Strings(nums)
		if len(nums) == 0 {
			// A ring group needs someone: an empty directory group leaves it as it was.
			r.Problems = append(r.Problems, fmt.Sprintf("ring group %s: nobody in %s has an extension; left as it was", g.Number, m.Group))
			continue
		}
		if !slices.Equal(nums, g.Members) {
			g.Members = nums
			r.RingGroups = append(r.RingGroups, fmt.Sprintf("%s %s: %d members", g.Number, g.Name, len(nums)))
			r.Changed = true
		}
	}
}

// renumber points ring groups at an extension's new number.
func renumber(cfg *config.Config, from, to string) {
	for i := range cfg.RingGroups {
		for j, m := range cfg.RingGroups[i].Members {
			if m == from {
				cfg.RingGroups[i].Members[j] = to
			}
		}
	}
}

// pin is a voicemail PIN for a new extension; the person changes it from their phone or the portal.
func pin() string {
	s := config.NewSecret()
	var b strings.Builder
	for _, c := range s {
		b.WriteByte(byte('0' + int(c)%10))
		if b.Len() == 6 {
			break
		}
	}
	return b.String()
}

// ErrNotAllowed is someone whose password is right but who is in no group allowed to sign in.
var ErrNotAllowed = errors.New("not in a group allowed to use BitVoice")

// ErrWrongPassword is a sign-in name or password the directory refused.
var ErrWrongPassword = errors.New("that username and password do not match")

// SignIn checks a person's directory password and returns them with the role their groups give:
// admin or viewer.
func SignIn(ctx context.Context, d config.LDAP, login, password string) (Person, string, error) {
	login = strings.TrimSpace(login)
	if login == "" || password == "" || len(login) > 200 {
		return Person{}, "", ErrWrongPassword
	}
	c, err := Dial(ctx, d)
	if err != nil {
		return Person{}, "", err
	}
	defer c.Close()
	es, err := c.Search(d.BaseDN, ldap.ScopeSubtree, "(&"+d.UserFilter+"("+d.LoginAttr+"="+ldap.EscapeFilter(login)+"))", attrs(d), 0, 2)
	if err != nil {
		return Person{}, "", err
	}
	if len(es) != 1 {
		return Person{}, "", ErrWrongPassword // nobody, or two people of that name: never guess
	}
	p := personOf(d, es[0])
	if p.Disabled {
		return Person{}, "", ErrWrongPassword
	}
	if err := c.Bind(p.DN, password); err != nil {
		if ldap.IsCode(err, ldap.InvalidCredentials) {
			return Person{}, "", ErrWrongPassword
		}
		return Person{}, "", err
	}
	// The groups the person is in: memberOf where the directory keeps it, and the groups naming them.
	if err := c.Bind(d.BindDN, d.BindPassword); err != nil {
		return Person{}, "", err
	}
	groups := map[string]bool{}
	for _, g := range p.Groups {
		groups[strings.ToLower(g)] = true
	}
	groupFilter := "(|(member=" + ldap.EscapeFilter(p.DN) + ")(uniqueMember=" + ldap.EscapeFilter(p.DN) + ")(memberUid=" + ldap.EscapeFilter(p.Login) + "))"
	if d.Kind == "ad" {
		// Active Directory: every group the person is in, through nested groups too.
		groupFilter = "(member:1.2.840.113556.1.4.1941:=" + ldap.EscapeFilter(p.DN) + ")"
	}
	gs, err := c.Search(d.BaseDN, ldap.ScopeSubtree, groupFilter, []string{"cn"}, 500, 5000)
	if err == nil {
		for _, g := range gs {
			groups[strings.ToLower(g.DN)] = true
		}
	}
	in := func(list []string) bool {
		for _, want := range list {
			want = strings.ToLower(strings.TrimSpace(want))
			for g := range groups {
				if g == want || strings.HasPrefix(g, "cn="+want+",") {
					return true
				}
			}
		}
		return false
	}
	switch {
	case in(d.AdminGroups):
		return p, "admin", nil
	case len(d.ViewerGroups) == 0 || in(d.ViewerGroups):
		return p, "viewer", nil
	}
	return p, "", ErrNotAllowed
}
