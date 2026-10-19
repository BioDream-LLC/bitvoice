package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/importer"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// maxImport is the most an upload may hold: a whole FreePBX backup, sound files and call history
// included (a small office's is 70 MB; only its configuration is read, see importer).
const maxImport = 512 << 20

// readImport takes what was uploaded: one file as the body (named by ?name=), or several as a
// multipart form (field "file").
func readImport(w http.ResponseWriter, r *http.Request) ([]importer.File, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImport)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil { // the rest goes to a temporary file
			return nil, fmt.Errorf("the files are larger than 512 MB together")
		}
		defer r.MultipartForm.RemoveAll()
		var out []importer.File
		for _, fh := range r.MultipartForm.File["file"] {
			f, err := fh.Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, importer.File{Name: fh.Filename, Data: b})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("choose a file to import")
		}
		return out, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("the file is larger than 512 MB")
	}
	return []importer.File{{Name: r.URL.Query().Get("name"), Data: data}}, nil
}

func (s *Server) importRoutes() {
	// preview reads uploaded files and says what they would add, marking what already exists here.
	s.handle("POST /api/import/preview", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		files, err := readImport(w, r)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		var res importer.Result
		if len(files) == 1 {
			res, err = importer.Parse(files[0].Name, files[0].Data)
			if err != nil {
				// A single file may still be an archive or a call-flow file.
				if r2, err2 := importer.ParseFiles(files); err2 == nil {
					res, err = r2, nil
				}
			} else if len(res.Extensions)+len(res.Trunks) == 0 {
				res, err = importer.ParseFiles(files)
			}
		} else {
			res, err = importer.ParseFiles(files)
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		cfg, _ := s.Store.Config()
		exists := map[string]bool{}
		taken := func(n string) bool {
			return cfg.FindExtension(n) != nil || cfg.FindRingGroup(n) != nil || cfg.FindQueue(n) != nil
		}
		for _, x := range res.Extensions {
			if taken(x.Number) {
				exists[x.Number] = true
			}
		}
		for _, g := range res.RingGroups {
			if taken(g.Number) {
				exists["group:"+g.Number] = true
			}
		}
		for _, q := range res.Queues {
			if taken(q.Number) {
				exists["queue:"+q.Number] = true
			}
		}
		for _, t := range res.Trunks {
			for _, have := range cfg.Trunks {
				if have.Name == t.Name || have.Host == t.Host && have.Username == t.Username {
					exists["trunk:"+t.Name] = true
				}
			}
		}
		for _, in := range res.InboundRoutes {
			for _, have := range cfg.InboundRoutes {
				if have.DID == in.DID {
					exists["in:"+in.DID] = true
				}
			}
		}
		for _, o := range res.OutboundRoutes {
			for _, have := range cfg.OutboundRoutes {
				for _, p := range have.Patterns {
					if p == o.Patterns[0] {
						exists["out:"+p] = true
					}
				}
			}
		}
		writeJSON(w, 200, map[string]any{"result": res, "exists": exists})
	})
	// apply adds what was chosen in one change, skipping anything already here.
	s.handle("POST /api/import/apply", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Extensions     []config.Extension     `json:"extensions"`
			Trunks         []config.Trunk         `json:"trunks"`
			RingGroups     []config.RingGroup     `json:"ringGroups"`
			Queues         []config.Queue         `json:"queues"`
			InboundRoutes  []config.InboundRoute  `json:"inboundRoutes"`
			OutboundRoutes []config.OutboundRoute `json:"outboundRoutes"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		added, skipped := 0, 0
		var notes []string
		what := fmt.Sprintf("Imported %d extensions, %d trunks, %d groups, %d queues and %d routes", len(in.Extensions), len(in.Trunks), len(in.RingGroups),
			len(in.Queues), len(in.InboundRoutes)+len(in.OutboundRoutes))
		_, _, err := s.Store.Update(u.Username, what, 0, func(c *config.Config) error {
			added, skipped, notes = 0, 0, nil
			taken := func(n string) bool {
				return c.FindExtension(n) != nil || c.FindRingGroup(n) != nil || c.FindQueue(n) != nil
			}
			for _, x := range in.Extensions {
				if taken(x.Number) {
					skipped++
					continue
				}
				x.ID = ""
				c.Extensions = append(c.Extensions, x)
				added++
			}
			trunkID := map[string]string{} // by name, existing and new
			for _, t := range c.Trunks {
				trunkID[t.Name] = t.ID
			}
			for _, t := range in.Trunks {
				if _, dup := trunkID[t.Name]; dup {
					skipped++
					continue
				}
				t.ID = config.NewID()
				trunkID[t.Name] = t.ID
				c.Trunks = append(c.Trunks, t)
				added++
			}
			// Groups and queues go in first, their overflows after, as each may overflow to the other.
			type overflow struct {
				what, number string
				d            *config.Destination
				set          func(*config.Destination)
			}
			var overflows []overflow
			for _, q := range in.Queues {
				if taken(q.Number) {
					skipped++
					continue
				}
				var as []string
				for _, a := range q.Agents {
					if c.FindExtension(a) != nil {
						as = append(as, a)
					} else {
						notes = append(notes, fmt.Sprintf("queue %s: %s is not an extension here, left out", q.Number, a))
						delete(q.Penalties, a)
					}
				}
				if len(as) == 0 {
					notes = append(notes, fmt.Sprintf("queue %s has no agents here, not added", q.Number))
					skipped++
					continue
				}
				q.Agents, q.ID = as, ""
				to := q.Timeout
				q.Timeout = nil
				c.Queues = append(c.Queues, q)
				if to != nil {
					n := q.Number
					overflows = append(overflows, overflow{"queue", n, to, func(d *config.Destination) { c.FindQueue(n).Timeout = d }})
				}
				added++
			}
			for _, g := range in.RingGroups {
				if taken(g.Number) {
					skipped++
					continue
				}
				// Members that are not extensions here are left out, and said.
				var ms []string
				for _, m := range g.Members {
					if c.FindExtension(m) != nil {
						ms = append(ms, m)
					} else {
						notes = append(notes, fmt.Sprintf("group %s: %s is not an extension here, left out", g.Number, m))
					}
				}
				if len(ms) == 0 {
					notes = append(notes, fmt.Sprintf("group %s has no members here, not added", g.Number))
					skipped++
					continue
				}
				g.Members, g.ID = ms, ""
				if g.Strategy == "" {
					g.Strategy = "all"
				}
				if g.RingSeconds == 0 {
					g.RingSeconds = 20
				}
				to := g.NoAnswer
				g.NoAnswer = nil
				c.RingGroups = append(c.RingGroups, g)
				if to != nil {
					n := g.Number
					overflows = append(overflows, overflow{"group", n, to, func(d *config.Destination) { c.FindRingGroup(n).NoAnswer = d }})
				}
				added++
			}
			for _, o := range overflows {
				if d, ok := importedDestination(c, *o.d); ok {
					o.set(&d)
				} else {
					notes = append(notes, fmt.Sprintf("%s %s: its overflow %s is not here; set it by hand", o.what, o.number, o.d.Target))
				}
			}
			for _, o := range in.OutboundRoutes {
				dup := false
				for _, have := range c.OutboundRoutes {
					for _, p := range have.Patterns {
						dup = dup || p == o.Patterns[0]
					}
				}
				if dup {
					skipped++
					continue
				}
				var ids []string
				for _, name := range o.Trunks {
					if id, ok := trunkID[name]; ok {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					notes = append(notes, fmt.Sprintf("route %s: no trunk named %s here, not added", o.Patterns[0], strings.Join(o.Trunks, ", ")))
					skipped++
					continue
				}
				o.ID, o.Trunks = "", ids
				c.OutboundRoutes = append(c.OutboundRoutes, o)
				added++
			}
			for _, ir := range in.InboundRoutes {
				dup := false
				for _, have := range c.InboundRoutes {
					dup = dup || have.DID == ir.DID
				}
				d, ok := importedDestination(c, ir.Destination)
				if dup || !ok {
					if !ok {
						notes = append(notes, fmt.Sprintf("calls to %s go to %s, which is not here: not added", ir.DID, ir.Destination.Target))
					}
					skipped++
					continue
				}
				ir.ID, ir.Destination = "", d
				c.InboundRoutes = append(c.InboundRoutes, ir)
				added++
			}
			return nil
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "config.import", fmt.Sprintf("%d added, %d skipped", added, skipped))
		if notes == nil {
			notes = []string{}
		}
		writeJSON(w, 200, map[string]any{"added": added, "skipped": skipped, "notes": notes})
	})
}

// importedDestination is an imported destination as it stands here: a number is found among the
// extensions, groups and queues (whatever type the other system gave it), voicemail needs its
// extension, and hanging up needs nothing.
func importedDestination(c *config.Config, d config.Destination) (config.Destination, bool) {
	switch d.Type {
	case "hangup":
		return config.Destination{Type: "hangup"}, true
	case "voicemail":
		if x := c.FindExtension(d.Target); x != nil && x.Voicemail.Enabled {
			return config.Destination{Type: "voicemail", Target: d.Target}, true
		}
		return d, false
	}
	return destinationFor(c, d.Target)
}

// destinationFor is where a number goes here: an extension, a ring group or a queue.
func destinationFor(c *config.Config, n string) (config.Destination, bool) {
	switch {
	case c.FindExtension(n) != nil:
		return config.Destination{Type: "extension", Target: n}, true
	case c.FindRingGroup(n) != nil:
		return config.Destination{Type: "ringgroup", Target: n}, true
	case c.FindQueue(n) != nil:
		return config.Destination{Type: "queue", Target: n}, true
	}
	return config.Destination{}, false
}
