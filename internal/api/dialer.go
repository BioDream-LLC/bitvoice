package api

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func registerCampaigns(s *Server) {
	register(s, collection[config.Campaign]{path: "campaigns", noun: "campaign",
		list:  func(c *config.Config) *[]config.Campaign { return &c.Campaigns },
		id:    func(x *config.Campaign) *string { return &x.ID },
		label: func(x *config.Campaign) string { return x.Name }})
}

// maxContacts is the most contacts one upload takes.
const maxContacts = 200000

func (s *Server) dialerRoutes() {
	campaign := func(w http.ResponseWriter, r *http.Request) *config.Campaign {
		cfg := s.config()
		k := cfg.FindCampaign(r.PathValue("id"))
		if k == nil {
			writeError(w, 404, "no such campaign")
		}
		return k
	}
	// What every campaign is doing now, and how its contacts stand.
	s.handle("GET /api/dialer", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		type row struct {
			ID      string         `json:"id"`
			States  map[string]int `json:"states"`
			Results map[string]int `json:"results"`
			Ringing int            `json:"ringing"`
			Ratio   float64        `json:"ratio"`
			Abandon float64        `json:"abandonRate"`
			Open    bool           `json:"open"`
			Free    int            `json:"freeAgents"`
		}
		out := []row{}
		if s.Engine != nil {
			for _, st := range s.Engine.DialerStatuses() {
				states, results, _ := s.Store.DialContactCounts(st.Campaign)
				out = append(out, row{ID: st.Campaign, States: states, Results: results, Ringing: st.Ringing, Ratio: st.Ratio,
					Abandon: st.AbandonRate, Open: st.Open, Free: st.FreeAgents})
			}
		}
		writeJSON(w, 200, out)
	})
	// A list of contacts, as CSV: a column named number (or phone), optionally name, and any others
	// (shown to the agent). Numbers on the do-not-call list are kept but never called.
	s.handle("POST /api/campaigns/{id}/contacts", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		k := campaign(w, r)
		if k == nil {
			return
		}
		cr := csv.NewReader(io.LimitReader(r.Body, 64<<20))
		cr.FieldsPerRecord = -1
		cr.TrimLeadingSpace = true
		head, err := cr.Read()
		if err != nil {
			writeError(w, 422, "send a CSV file with a header row (number, name, …)")
			return
		}
		numCol, nameCol := -1, -1
		for i, h := range head {
			switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))) {
			case "number", "phone", "phone number", "telephone", "mobile":
				if numCol < 0 {
					numCol = i
				}
			case "name", "full name", "contact":
				if nameCol < 0 {
					nameCol = i
				}
			}
		}
		if numCol < 0 {
			writeError(w, 422, "the CSV needs a column named number (or phone)")
			return
		}
		var cs []store.DialContact
		bad, dnc := 0, 0
		seen := map[string]bool{}
		for line := 2; ; line++ {
			rec, err := cr.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				writeError(w, 422, "line "+strconv.Itoa(line)+" of the CSV: "+err.Error())
				return
			}
			if numCol >= len(rec) {
				bad++
				continue
			}
			n := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' || r == '+' {
					return r
				}
				return -1
			}, rec[numCol])
			if len(strings.TrimPrefix(n, "+")) < 3 || len(n) > 20 || seen[n] {
				bad++
				continue
			}
			seen[n] = true
			c := store.DialContact{ID: config.NewID(), Campaign: k.ID, Number: n, State: "pending", Fields: map[string]string{}}
			for i, v := range rec {
				switch {
				case i == numCol:
				case i == nameCol:
					c.Name = strings.TrimSpace(v)
				case i < len(head) && strings.TrimSpace(v) != "":
					c.Fields[strings.TrimSpace(head[i])] = strings.TrimSpace(v)
				}
			}
			if s.Store.OnDNC(n) {
				c.State, c.Result = "dnc", "on the do-not-call list"
				dnc++
			}
			cs = append(cs, c)
			if len(cs) > maxContacts {
				writeError(w, 422, "at most 200,000 contacts at once")
				return
			}
		}
		if err := s.Store.AddDialContacts(cs); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "campaign.contacts", k.Name+": "+strconv.Itoa(len(cs)))
		writeJSON(w, 200, map[string]int{"added": len(cs), "skipped": bad, "doNotCall": dnc})
	})
	s.handle("GET /api/campaigns/{id}/contacts", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		k := campaign(w, r)
		if k == nil {
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		cs, err := s.Store.DialContacts(k.ID, r.URL.Query().Get("state"), max(offset, 0), limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		if cs == nil {
			cs = []store.DialContact{}
		}
		writeJSON(w, 200, cs)
	})
	s.handle("DELETE /api/campaigns/{id}/contacts", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		k := campaign(w, r)
		if k == nil {
			return
		}
		if err := s.Store.DeleteDialContacts(k.ID, r.URL.Query().Get("state")); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "campaign.contacts.delete", k.Name)
		w.WriteHeader(http.StatusNoContent)
	})
	// Preview: the next contact for an agent, then call it or skip it.
	s.handle("POST /api/campaigns/{id}/next", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		k := campaign(w, r)
		if k == nil {
			return
		}
		var in struct {
			Agent string `json:"agent"`
		}
		_ = readJSON(r, &in)
		ct, ok := s.Engine.PreviewNext(k.ID, in.Agent)
		if !ok {
			writeError(w, 404, "no contact is due")
			return
		}
		writeJSON(w, 200, ct)
	})
	s.handle("POST /api/campaigns/{id}/contacts/{contact}/call", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		k := campaign(w, r)
		if k == nil {
			return
		}
		var in struct {
			Agent string `json:"agent"`
			Skip  bool   `json:"skip"`
		}
		if err := readJSON(r, &in); err != nil || (!in.Skip && in.Agent == "") {
			writeError(w, 400, "say which agent's phone calls (agent)")
			return
		}
		id, err := s.Engine.PreviewCall(k.ID, r.PathValue("contact"), in.Agent, in.Skip)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"callId": id})
	})
	// The message machines and unanswered people hear.
	s.handle("GET /api/campaigns/{id}/message", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Greeting("campaign-" + r.PathValue("id"))
		if err != nil {
			writeError(w, 404, "no message recorded")
			return
		}
		serveWAV(w, r, "message.wav", b)
	})
	s.handle("PUT /api/campaigns/{id}/message", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if campaign(w, r) == nil {
			return
		}
		s.putAudio(w, r, u, "campaign-"+r.PathValue("id"), "campaign.message")
	})
	s.handle("DELETE /api/campaigns/{id}/message", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.SetGreeting("campaign-"+r.PathValue("id"), nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// The do-not-call list.
	s.handle("GET /api/dnc", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		list, err := s.Store.DNC(5000)
		if err != nil {
			writeErr(w, err)
			return
		}
		if list == nil {
			list = []store.DNCEntry{}
		}
		if q := store.NormalNumber(r.URL.Query().Get("number")); q != "" {
			writeJSON(w, 200, map[string]bool{"listed": s.Store.OnDNC(q)})
			return
		}
		writeJSON(w, 200, list)
	})
	s.handle("POST /api/dnc", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Numbers []string `json:"numbers"`
		}
		if err := readJSON(r, &in); err != nil || len(in.Numbers) == 0 {
			writeError(w, 400, "send the numbers")
			return
		}
		if len(in.Numbers) > 100000 {
			writeError(w, 422, "at most 100,000 numbers at once")
			return
		}
		if err := s.Store.AddDNC("added by "+u.Username, in.Numbers...); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "dnc.add", strconv.Itoa(len(in.Numbers))+" numbers")
		writeJSON(w, 200, map[string]any{"added": len(in.Numbers), "at": time.Now().UTC()})
	})
	s.handle("DELETE /api/dnc/{number}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.RemoveDNC(r.PathValue("number")); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "dnc.remove", r.PathValue("number"))
		w.WriteHeader(http.StatusNoContent)
	})
}
