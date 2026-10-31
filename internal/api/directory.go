package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"slices"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/provision"
	"github.com/biodream-llc/bitvoice/internal/store"
)

var numberKinds = []string{"work", "mobile", "home", "other"}

// checkContact tidies a contact and lists what is wrong with it.
func checkContact(c *store.DirContact, prefix string) config.Problems {
	var p config.Problems
	add := func(f, m string) { p = append(p, config.Problem{Field: prefix + f, Message: m}) }
	c.Name, c.Company, c.Email = oneLine(strings.TrimSpace(c.Name), ""), strings.TrimSpace(c.Company), strings.TrimSpace(c.Email)
	if c.Name == "" {
		add("name", "enter a name")
	} else if len(c.Name) > 80 {
		add("name", "use 80 characters or fewer")
	}
	if len(c.Notes) > 2000 {
		add("notes", "use 2000 characters or fewer")
	}
	if c.Email != "" {
		if a, err := mail.ParseAddress(c.Email); err != nil || a.Address != c.Email {
			add("email", "enter an email address")
		}
	}
	nums := c.Numbers[:0]
	for _, n := range c.Numbers {
		n.Number = strings.TrimSpace(n.Number)
		if n.Number != "" {
			nums = append(nums, n)
		}
	}
	c.Numbers = nums
	if len(c.Numbers) == 0 {
		add("numbers", "enter at least one number")
	}
	for i := range c.Numbers {
		n := &c.Numbers[i]
		n.Number = strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "").Replace(n.Number)
		if n.Kind == "" {
			n.Kind = "work"
		}
		if !slices.Contains(numberKinds, n.Kind) {
			add(fmt.Sprintf("numbers[%d].kind", i), "choose work, mobile, home or other")
		}
		if !dialable(n.Number) {
			add(fmt.Sprintf("numbers[%d].number", i), "enter a phone number: digits, with + * # allowed")
		}
	}
	return p
}

// sameNumber says whether two numbers are the same line: equal digits, or the same last ten when
// both have ten or more (+1 212 555 0100 and 2125550100).
func sameNumber(a, b string) bool {
	da, db := digitsOf(a), digitsOf(b)
	if da == "" || db == "" {
		return false
	}
	if da == db {
		return true
	}
	return len(da) >= 10 && len(db) >= 10 && da[len(da)-10:] == db[len(db)-10:]
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// directoryContact finds the shared contact with this number.
func (s *Server) directoryContact(number string) *store.DirContact {
	for _, c := range s.Store.Directory() {
		for _, n := range c.Numbers {
			if sameNumber(n.Number, number) {
				return &c
			}
		}
	}
	return nil
}

// NameFor is the directory name for a caller's number, for caller ID on phones; "" when unknown.
func (s *Server) NameFor(number string) string {
	if c := s.directoryContact(number); c != nil {
		return c.Name
	}
	return ""
}

// phonebook is everything the phones list: extensions, then shared contacts.
// phonePresence lists the other extensions a phone can watch and pick up for: their name, number and
// whether they are free, on a call or ringing now (plus away). The phone's own extension is left out.
func (s *Server) phonePresence(self string) []map[string]any {
	// The engine's presence (as the staff chat shows it): ringing, on a call, away, or offline when no
	// phone of the extension is connected.
	cfg := s.config()
	names := map[string]string{"available": "free", "on-call": "busy", "ringing": "ringing", "away": "away", "offline": "offline"}
	out := []map[string]any{}
	for _, c := range s.Engine.Colleagues(&cfg) {
		if c.Extension == self {
			continue
		}
		state := names[c.State]
		if state == "" {
			state = "free"
		}
		out = append(out, map[string]any{"name": c.Name, "number": c.Extension, "state": state})
	}
	return out
}

func (s *Server) phonebook(cfg config.Config) []provision.Entry {
	var out []provision.Entry
	for _, x := range cfg.Extensions {
		if x.Enabled && x.Kind != "door" {
			out = append(out, provision.Entry{Name: x.Name, Number: x.Number, Group: "Extensions"})
		}
	}
	for _, c := range s.Store.Directory() {
		for _, n := range c.Numbers {
			name := c.Name
			if len(c.Numbers) > 1 {
				name += " (" + n.Kind + ")"
			}
			out = append(out, provision.Entry{Name: name, Number: n.Number, Company: c.Company, Group: "Contacts"})
		}
	}
	return out
}

// csvSafe keeps a spreadsheet from running a cell as a formula: one starting with = + - @ (or a tab or
// return) gets a leading apostrophe. Numbers starting with + are written as they are only when they
// are a plain phone number.
func csvSafe(s string) string {
	if s == "" || !strings.ContainsAny(s[:1], "=+-@\t\r") {
		return s
	}
	if s[0] == '+' && dialable(s) {
		return s
	}
	return "'" + s
}

var csvHeader = []string{"name", "company", "work", "mobile", "home", "other", "email", "notes"}

func (s *Server) directoryRoutes() {
	s.handle("GET /api/directory", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Store.Directory()})
	})
	s.handle("POST /api/directory", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var c store.DirContact
		if err := readJSON(r, &c); err != nil {
			writeErr(w, err)
			return
		}
		c.ID, c.Source, c.SourceID = "", "", ""
		if p := checkContact(&c, ""); len(p) > 0 {
			writeErr(w, p)
			return
		}
		out, err := s.Store.PutContact(c)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "directory.add", Detail: out.Name, From: clientIP(r).String()})
		s.Bus.Publish("directory.changed", nil)
		writeJSON(w, 201, out)
	})
	s.handle("PUT /api/directory/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var c store.DirContact
		if err := readJSON(r, &c); err != nil {
			writeErr(w, err)
			return
		}
		c.ID = r.PathValue("id")
		if s.refuseSynced(w, c.ID) {
			return
		}
		c.Source, c.SourceID = "", ""
		if p := checkContact(&c, ""); len(p) > 0 {
			writeErr(w, p)
			return
		}
		out, err := s.Store.PutContact(c)
		if errors.Is(err, store.ErrNoContact) {
			writeError(w, 404, "no such contact")
			return
		} else if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "directory.change", Detail: out.Name, From: clientIP(r).String()})
		s.Bus.Publish("directory.changed", nil)
		writeJSON(w, 200, out)
	})
	s.handle("DELETE /api/directory/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.refuseSynced(w, r.PathValue("id")) {
			return
		}
		if err := s.Store.DeleteContact(r.PathValue("id")); errors.Is(err, store.ErrNoContact) {
			writeError(w, 404, "no such contact")
			return
		} else if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "directory.remove", Detail: r.PathValue("id"), From: clientIP(r).String()})
		s.Bus.Publish("directory.changed", nil)
		w.WriteHeader(204)
	})
	s.handle("GET /api/directory.csv", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="contacts.csv"`)
		cw := csv.NewWriter(w)
		cw.Write(csvHeader)
		for _, c := range s.Store.Directory() {
			row := map[string]string{"name": c.Name, "company": c.Company, "email": c.Email, "notes": c.Notes}
			for _, n := range c.Numbers {
				if row[n.Kind] == "" {
					row[n.Kind] = n.Number
				}
			}
			rec := make([]string, len(csvHeader))
			for i, h := range csvHeader {
				rec[i] = csvSafe(row[h])
			}
			cw.Write(rec)
		}
		cw.Flush()
	})
	// Import a CSV: a header row naming the columns (name, company, work, mobile, home, other, email,
	// notes; "phone" is taken as work). ?replace=1 drops the contacts there are first; ?check=1 only
	// says what would happen.
	s.handle("POST /api/directory/import", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cr := csv.NewReader(io.LimitReader(r.Body, 5<<20))
		cr.FieldsPerRecord = -1
		cr.TrimLeadingSpace = true
		head, err := cr.Read()
		if err != nil {
			writeError(w, 400, "the file has no header row")
			return
		}
		col := map[string]int{}
		for i, h := range head {
			h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
			if h == "phone" || h == "number" || h == "telephone" {
				h = "work"
			}
			col[h] = i
		}
		if _, ok := col["name"]; !ok {
			writeError(w, 400, "the header row needs a name column")
			return
		}
		var in []store.DirContact
		var p config.Problems
		for line := 2; ; line++ {
			rec, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeError(w, 400, fmt.Sprintf("line %d: %v", line, err))
				return
			}
			get := func(k string) string {
				if i, ok := col[k]; ok && i < len(rec) {
					return strings.TrimSpace(rec[i])
				}
				return ""
			}
			c := store.DirContact{Name: get("name"), Company: get("company"), Email: get("email"), Notes: get("notes")}
			for _, k := range numberKinds {
				if v := get(k); v != "" {
					c.Numbers = append(c.Numbers, store.PhoneNumber{Kind: k, Number: v})
				}
			}
			if c.Name == "" && len(c.Numbers) == 0 {
				continue // a blank line
			}
			for _, x := range checkContact(&c, "") {
				p = append(p, config.Problem{Field: fmt.Sprintf("line %d", line), Message: x.Field + ": " + x.Message})
			}
			in = append(in, c)
			if len(in) > 20000 {
				writeError(w, 400, "use 20,000 contacts or fewer")
				return
			}
		}
		if len(p) > 0 {
			if len(p) > 20 {
				p = append(p[:20], config.Problem{Field: "more", Message: fmt.Sprintf("and %d more", len(p)-20)})
			}
			writeErr(w, p)
			return
		}
		replace := r.URL.Query().Get("replace") == "1"
		if r.URL.Query().Get("check") == "1" {
			writeJSON(w, 200, map[string]any{"contacts": len(in), "replace": replace})
			return
		}
		added, updated, err := s.Store.ImportContacts(in, replace)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.Store.Audit(store.AuditEntry{Actor: u.Username, Action: "directory.import", Detail: fmt.Sprintf("%d added, %d updated, replace %v", added, updated, replace), From: clientIP(r).String()})
		s.Bus.Publish("directory.changed", nil)
		writeJSON(w, 200, map[string]any{"added": added, "updated": updated})
	})
}

// refuseSynced answers 409 for a contact kept in step with Microsoft 365 or Google Workspace: a
// change here would be undone at the next sync, so it is made there.
func (s *Server) refuseSynced(w http.ResponseWriter, id string) bool {
	for _, c := range s.Store.Directory() {
		if c.ID == id && c.Source != "" {
			from := map[string]string{"microsoft365": "Microsoft 365", "google": "Google Workspace"}[c.Source]
			if from == "" {
				from = c.Source
			}
			writeError(w, http.StatusConflict, c.Name+" is kept in step with "+from+": change it there")
			return true
		}
	}
	return false
}
