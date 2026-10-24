package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/script"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// collection describes one list in the configuration that the API exposes as a REST resource.
type collection[T any] struct {
	path  string // URL segment, e.g. extensions
	noun  string // for change summaries, e.g. extension
	list  func(*config.Config) *[]T
	id    func(*T) *string
	label func(*T) string
	// redact hides secrets from users who are not administrators.
	redact func(*T)
	// keep copies fields the client left empty (passwords) from the stored item, so editing a name never
	// changes a phone's password by accident.
	keep func(old, item *T)
}

// itemResponse carries an item and the configuration revision it belongs to, for optimistic locking.
type listResponse[T any] struct {
	Items []T `json:"items"`
	Rev   int `json:"rev"`
}

type itemResponse[T any] struct {
	Item T   `json:"item"`
	Rev  int `json:"rev"`
}

// expectRev reads the If-Match header: the revision the client last saw.
func expectRev(r *http.Request) int {
	n, _ := strconv.Atoi(r.Header.Get("If-Match"))
	return n
}

func register[T any](s *Server, c collection[T]) {
	s.noteSchema(c.path, reflect.TypeOf((*T)(nil)).Elem())
	base := "/api/" + c.path
	s.handle("GET "+base, false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, rev := s.Store.Config()
		items := *c.list(&cfg)
		if items == nil {
			items = []T{}
		}
		if u.Role != "admin" && c.redact != nil {
			for i := range items {
				c.redact(&items[i])
			}
		}
		writeJSON(w, 200, listResponse[T]{Items: items, Rev: rev})
	})
	s.handle("GET "+base+"/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, rev := s.Store.Config()
		for _, it := range *c.list(&cfg) {
			if *c.id(&it) == r.PathValue("id") {
				if u.Role != "admin" && c.redact != nil {
					c.redact(&it)
				}
				writeJSON(w, 200, itemResponse[T]{Item: it, Rev: rev})
				return
			}
		}
		writeError(w, http.StatusNotFound, "not found")
	})
	s.handle("POST "+base, true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var item T
		if err := readJSON(r, &item); err != nil {
			writeErr(w, err)
			return
		}
		*c.id(&item) = ""
		var saved T
		prefix := ""
		cfg, rev, err := s.Store.Update(u.Username, "Added "+c.noun+" "+c.label(&item), expectRev(r), func(cfg *config.Config) error {
			l := c.list(cfg)
			*l = append(*l, item)
			prefix = listPath(cfg, l, len(*l)-1)
			return nil
		})
		if err != nil {
			writeErr(w, scopeProblems(err, prefix))
			return
		}
		l := *c.list(&cfg)
		saved = l[len(l)-1]
		s.audit(r, u.Username, c.noun+".create", c.label(&saved))
		writeJSON(w, http.StatusCreated, itemResponse[T]{Item: saved, Rev: rev})
	})
	s.handle("PUT "+base+"/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		var item T
		if err := readJSON(r, &item); err != nil {
			writeErr(w, err)
			return
		}
		*c.id(&item) = id
		prefix := ""
		cfg, rev, err := s.Store.Update(u.Username, "Changed "+c.noun+" "+c.label(&item), expectRev(r), func(cfg *config.Config) error {
			l := c.list(cfg)
			for i := range *l {
				if *c.id(&(*l)[i]) == id {
					if c.keep != nil {
						c.keep(&(*l)[i], &item)
					}
					(*l)[i] = item
					prefix = listPath(cfg, l, i)
					return nil
				}
			}
			return errNotFound
		})
		if err != nil {
			writeErr(w, scopeProblems(err, prefix))
			return
		}
		for _, x := range *c.list(&cfg) {
			if *c.id(&x) == id {
				item = x
			}
		}
		s.audit(r, u.Username, c.noun+".update", c.label(&item))
		writeJSON(w, 200, itemResponse[T]{Item: item, Rev: rev})
	})
	s.handle("DELETE "+base+"/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		label := id
		var after config.Config // as validation saw it, so problem indexes match
		_, rev, err := s.Store.Update(u.Username, "Removed "+c.noun, expectRev(r), func(cfg *config.Config) error {
			l := c.list(cfg)
			for i := range *l {
				if *c.id(&(*l)[i]) == id {
					label = c.label(&(*l)[i])
					*l = append((*l)[:i], (*l)[i+1:]...)
					after = cfg.Clone()
					return nil
				}
			}
			return errNotFound
		})
		if err != nil {
			writeErr(w, stillUsed(err, c.noun+" "+label, &after))
			return
		}
		s.audit(r, u.Username, c.noun+".delete", label)
		writeJSON(w, 200, map[string]int{"rev": rev})
	})
}

func (s *Server) routes() {
	register(s, collection[config.Extension]{path: "extensions", noun: "extension",
		list:   func(c *config.Config) *[]config.Extension { return &c.Extensions },
		id:     func(x *config.Extension) *string { return &x.ID },
		label:  func(x *config.Extension) string { return x.Number + " " + x.Name },
		redact: func(x *config.Extension) { x.Secret, x.Voicemail.PIN, x.Door.Password = "", "", "" },
		keep: func(old, x *config.Extension) {
			if x.Secret == "" {
				x.Secret = old.Secret
			}
			if x.Voicemail.PIN == "" {
				x.Voicemail.PIN = old.Voicemail.PIN
			}
		}})
	register(s, collection[config.RingGroup]{path: "ring-groups", noun: "ring group",
		list:  func(c *config.Config) *[]config.RingGroup { return &c.RingGroups },
		id:    func(x *config.RingGroup) *string { return &x.ID },
		label: func(x *config.RingGroup) string { return x.Number + " " + x.Name }})
	register(s, collection[config.PageGroup]{path: "page-groups", noun: "paging group",
		list:  func(c *config.Config) *[]config.PageGroup { return &c.PageGroups },
		id:    func(x *config.PageGroup) *string { return &x.ID },
		label: func(x *config.PageGroup) string { return x.Number + " " + x.Name }})
	register(s, collection[config.Conference]{path: "conferences", noun: "conference room",
		list:  func(c *config.Config) *[]config.Conference { return &c.Conferences },
		id:    func(x *config.Conference) *string { return &x.ID },
		label: func(x *config.Conference) string { return x.Number + " " + x.Name },
		redact: func(x *config.Conference) {
			if x.PIN != "" {
				x.PIN = "••••"
			}
			if x.LeaderPIN != "" {
				x.LeaderPIN = "••••"
			}
		}})
	register(s, collection[config.Directory]{path: "directories", noun: "directory",
		list:  func(c *config.Config) *[]config.Directory { return &c.Directories },
		id:    func(x *config.Directory) *string { return &x.ID },
		label: func(x *config.Directory) string { return x.Name }})
	registerFlow(s)
	registerCampaigns(s)
	register(s, collection[config.DialIn]{path: "dial-ins", noun: "dial-in",
		list:  func(c *config.Config) *[]config.DialIn { return &c.DialIns },
		id:    func(x *config.DialIn) *string { return &x.ID },
		label: func(x *config.DialIn) string { return x.Name },
		redact: func(x *config.DialIn) {
			if x.PIN != "" {
				x.PIN = "••••"
			}
		},
		keep: func(old, x *config.DialIn) {
			if x.PIN == "••••" {
				x.PIN = old.PIN
			}
		}})
	register(s, collection[config.Queue]{path: "queues", noun: "queue",
		list:  func(c *config.Config) *[]config.Queue { return &c.Queues },
		id:    func(x *config.Queue) *string { return &x.ID },
		label: func(x *config.Queue) string { return x.Number + " " + x.Name }})
	register(s, collection[config.FaxBox]{path: "fax-boxes", noun: "fax",
		list:  func(c *config.Config) *[]config.FaxBox { return &c.Faxes },
		id:    func(x *config.FaxBox) *string { return &x.ID },
		label: func(x *config.FaxBox) string { return x.Number + " " + x.Name }})
	register(s, collection[config.Script]{path: "scripts", noun: "script",
		list:  func(c *config.Config) *[]config.Script { return &c.Scripts },
		id:    func(x *config.Script) *string { return &x.ID },
		label: func(x *config.Script) string { return x.Name }})
	s.handle("POST /api/scripts/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Language string      `json:"language"`
			Source   string      `json:"source"`
			Call     script.Call `json:"call"`
			// A call script: the keys the caller presses (one entry for each collect), and whether
			// someone answers when it dials.
			Mode   string   `json:"mode"`
			Keys   []string `json:"keys"`
			Answer bool     `json:"answer"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		start := time.Now()
		if in.Mode == "call" {
			cfg, _ := s.Store.Config()
			sim := &script.Simulator{Keys: in.Keys, Answer: in.Answer, Exists: func(d script.Destination) bool {
				switch d.Type {
				case "number":
					if cfg.FindExtension(d.Target) != nil || cfg.FindRingGroup(d.Target) != nil || cfg.FindQueue(d.Target) != nil {
						return true
					}
					for _, r := range cfg.OutboundRoutes {
						for _, p := range r.Patterns {
							if config.MatchPattern(p, d.Target) {
								return true
							}
						}
					}
					return false
				case "voicemail":
					x := cfg.FindExtension(d.Target)
					return x != nil && x.Voicemail.Enabled
				}
				return cfg.DestinationExists(config.Destination{Type: d.Type, Target: d.Target})
			}}
			o, err := script.RunCall(in.Language, in.Source, in.Call, sim, 10*time.Second)
			out := map[string]any{"steps": nonNilStrings(o.Steps), "log": nonNilStrings(o.Log), "ended": o.Ended,
				"millis": float64(time.Since(start).Microseconds()) / 1000}
			if o.Next != nil {
				out["type"], out["target"] = o.Next.Type, o.Next.Target
			}
			if err != nil {
				out["error"] = err.Error()
			}
			writeJSON(w, 200, out)
			return
		}
		d, err := script.Route(in.Language, in.Source, in.Call)
		out := map[string]any{"type": d.Type, "target": d.Target, "log": d.Log, "millis": float64(time.Since(start).Microseconds()) / 1000}
		if err != nil {
			out["error"] = err.Error()
		}
		writeJSON(w, 200, out)
	})
	register(s, collection[config.Menu]{path: "menus", noun: "menu",
		list:  func(c *config.Config) *[]config.Menu { return &c.Menus },
		id:    func(x *config.Menu) *string { return &x.ID },
		label: func(x *config.Menu) string { return x.Name }})
	register(s, collection[config.Schedule]{path: "schedules", noun: "schedule",
		list:  func(c *config.Config) *[]config.Schedule { return &c.Schedules },
		id:    func(x *config.Schedule) *string { return &x.ID },
		label: func(x *config.Schedule) string { return x.Name }})
	register(s, collection[config.Device]{path: "devices", noun: "phone",
		list:  func(c *config.Config) *[]config.Device { return &c.Devices },
		id:    func(x *config.Device) *string { return &x.ID },
		label: func(x *config.Device) string { return x.Vendor + " " + x.MAC }})
	register(s, collection[config.Trunk]{path: "trunks", noun: "trunk",
		list:   func(c *config.Config) *[]config.Trunk { return &c.Trunks },
		id:     func(x *config.Trunk) *string { return &x.ID },
		label:  func(x *config.Trunk) string { return x.Name },
		redact: func(x *config.Trunk) { x.Password, x.Shaken.Key = "", "" },
		keep: func(old, x *config.Trunk) {
			if x.Password == "" {
				x.Password = old.Password
			}
			if x.Shaken.Key == "" {
				x.Shaken.Key = old.Shaken.Key
			}
		}})
	register(s, collection[config.Receptionist]{path: "receptionists", noun: "receptionist",
		list:  func(c *config.Config) *[]config.Receptionist { return &c.Receptionists },
		id:    func(x *config.Receptionist) *string { return &x.ID },
		label: func(x *config.Receptionist) string { return x.Name }})
	register(s, collection[config.Site]{path: "sites", noun: "site",
		list:  func(c *config.Config) *[]config.Site { return &c.Sites },
		id:    func(x *config.Site) *string { return &x.ID },
		label: func(x *config.Site) string { return x.Name }})
	register(s, collection[config.InboundRoute]{path: "inbound-routes", noun: "inbound route",
		list:  func(c *config.Config) *[]config.InboundRoute { return &c.InboundRoutes },
		id:    func(x *config.InboundRoute) *string { return &x.ID },
		label: func(x *config.InboundRoute) string { return x.Name }})
	register(s, collection[config.OutboundRoute]{path: "outbound-routes", noun: "outbound route",
		list:  func(c *config.Config) *[]config.OutboundRoute { return &c.OutboundRoutes },
		id:    func(x *config.OutboundRoute) *string { return &x.ID },
		label: func(x *config.OutboundRoute) string { return x.Name }})

	s.handle("GET /api/settings", false, s.getSettings)
	s.handle("PUT /api/settings", true, s.putSettings)
	s.handle("GET /api/extensions/next-number", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		writeJSON(w, 200, map[string]string{"number": cfg.NextExtensionNumber(), "secret": config.NewSecret()})
	})
	s.handle("POST /api/outbound-routes/order", true, s.reorderRoutes)

	s.handle("GET /api/users", true, s.listUsers)
	s.handle("POST /api/users", true, s.putUser)
	s.handle("PUT /api/users/{username}", true, s.putUser)
	s.handle("DELETE /api/users/{username}", true, s.deleteUser)
	s.handle("PUT /api/auth/password", false, s.changeOwnPassword)

	s.liveRoutes()
	s.voicemailRoutes()
	s.soundRoutes()
	s.agiRoutes()
	s.ldapRoutes()
	s.hotelRoutes()
	s.chatRoutes()
	s.snmpRoutes()
	s.deskRoutes()
	s.flowRoutes()
	s.dialerRoutes()
	s.inboxRoutes()
	s.iceRoutes()
	s.cardsRoutes()
	s.cdrOutputRoutes()
	s.importRoutes()
	s.phoneRoutes()
	s.browserPhoneRoutes()
	s.archiveRoutes()
	s.emailRoutes()
	s.faxRoutes()
	s.psapRoutes()
	s.psapReportRoutes()
	s.coreRoutes()
	s.lvfRoutes()
	s.radioRoutes()
	s.mapRoutes()
	s.lisRoutes()
	s.i3logRoutes()
	s.callMediaRoutes()
	s.i3StdRoutes()
	s.aliRoutes()
	s.reportLayoutRoutes()
	s.radioLogRoutes()
	s.mfaRoutes()
}

type settings struct {
	System   config.System   `json:"system"`
	Security config.Security `json:"security"`
	Rev      int             `json:"rev"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, u store.User) {
	cfg, rev := s.Store.Config()
	if u.Role != "admin" {
		// Every secret, by the same rule Git exports and the change history use, so a new secret
		// setting is hidden from viewers without anyone having to remember to list it here.
		config.StripSecrets(&cfg)
	}
	hideRadioSecrets(&cfg.System)
	writeJSON(w, 200, settings{cfg.System, cfg.Security, rev})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, u store.User) {
	var in settings
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	var changed []string
	cfg, rev, err := s.Store.Update(u.Username, "Changed system settings", in.Rev, func(c *config.Config) error {
		if in.System.Provisioning.Password == "" {
			in.System.Provisioning.Password = c.System.Provisioning.Password
		}
		if in.System.Radio.CADSecret == "" && in.System.Radio.CADURL == c.System.Radio.CADURL {
			in.System.Radio.CADSecret = c.System.Radio.CADSecret
		}
		for i := range in.System.Radio.P25.Keys {
			k := &in.System.Radio.P25.Keys[i]
			for _, old := range c.System.Radio.P25.Keys {
				if k.Key == "" && k.ID != "" && old.ID == k.ID && old.Alg == k.Alg {
					k.Key = old.Key
				}
			}
		}
		// An empty key keeps the saved one, while a model still uses it: with no model and no audio
		// model nothing does, so saving them empty removes the key too (the only way to take it out).
		if in.System.AI.Key == "" && (in.System.AI.Model != "" || in.System.AI.AudioModel != "") {
			in.System.AI.Key = c.System.AI.Key
		}
		if in.System.AI.TranscribeKey == "" && in.System.AI.TranscribeURL == c.System.AI.TranscribeURL {
			in.System.AI.TranscribeKey = c.System.AI.TranscribeKey
		}
		if in.System.Archive.S3.SecretKey == "" && in.System.Archive.S3.AccessKey == c.System.Archive.S3.AccessKey {
			in.System.Archive.S3.SecretKey = c.System.Archive.S3.SecretKey
		}
		if in.System.Email.Password == "" && in.System.Email.Username == c.System.Email.Username {
			in.System.Email.Password = c.System.Email.Password
		}
		if in.System.PSAP.EIDOToken == "" && in.System.PSAP.EIDOURL == c.System.PSAP.EIDOURL {
			in.System.PSAP.EIDOToken = c.System.PSAP.EIDOToken
		}
		if in.System.FaxMail.Password == "" && in.System.FaxMail.Username == c.System.FaxMail.Username {
			in.System.FaxMail.Password = c.System.FaxMail.Password
		}
		if in.System.Archive.Azure.ContainerSAS == "" {
			in.System.Archive.Azure.ContainerSAS = c.System.Archive.Azure.ContainerSAS
		}
		if in.System.Mobile.RelaySecret == "" && in.System.Mobile.RelayKey == c.System.Mobile.RelayKey {
			in.System.Mobile.RelaySecret = c.System.Mobile.RelaySecret
		}
		if in.System.Mobile.APNs.Key == "" && in.System.Mobile.APNs.KeyID == c.System.Mobile.APNs.KeyID {
			in.System.Mobile.APNs.Key = c.System.Mobile.APNs.Key
		}
		if in.System.Vault.AWSSecretKey == "" && in.System.Vault.AWSAccessKey == c.System.Vault.AWSAccessKey {
			in.System.Vault.AWSSecretKey = c.System.Vault.AWSSecretKey
		}
		if in.System.Vault.AzureClientSecret == "" && in.System.Vault.AzureClientID == c.System.Vault.AzureClientID {
			in.System.Vault.AzureClientSecret = c.System.Vault.AzureClientSecret
		}
		if in.System.GitOps.Token == "" && in.System.GitOps.URL == c.System.GitOps.URL {
			in.System.GitOps.Token = c.System.GitOps.Token
		}
		if in.System.Mobile.FCM == "" {
			in.System.Mobile.FCM = c.System.Mobile.FCM
		}
		if in.System.SSO.ClientSecret == "" && in.System.SSO.ClientID == c.System.SSO.ClientID {
			in.System.SSO.ClientSecret = c.System.SSO.ClientSecret
		}
		for i, t := range in.System.ICE.TURN {
			for _, old := range c.System.ICE.TURN {
				if t.Password == "" && old.Server == t.Server && old.Username == t.Username {
					in.System.ICE.TURN[i].Password = old.Password
				}
			}
		}
		if in.System.LDAP.BindPassword == "" && in.System.LDAP.BindDN == c.System.LDAP.BindDN {
			in.System.LDAP.BindPassword = c.System.LDAP.BindPassword
		}
		for i, nu := range in.System.Compat.Users {
			for _, old := range c.System.Compat.Users {
				if nu.Secret == "" && strings.EqualFold(old.Username, nu.Username) {
					in.System.Compat.Users[i].Secret = old.Secret
				}
			}
		}
		changed = append(changedFields("system", c.System, in.System), changedFields("security", c.Security, in.Security)...)
		c.System, c.Security = in.System, in.Security
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, u.Username, "settings.update", strings.Join(changed, ", "))
	hideRadioSecrets(&cfg.System)
	writeJSON(w, 200, settings{cfg.System, cfg.Security, rev})
}

// hideRadioSecrets blanks the P25 traffic keys and CAD's secret: they are never sent back, not
// even to administrators; one entered once stays on the server (empty on save keeps it).
func hideRadioSecrets(s *config.System) {
	s.Radio.CADSecret = ""
	keys := make([]config.P25Key, len(s.Radio.P25.Keys))
	for i, k := range s.Radio.P25.Keys {
		k.Key = ""
		keys[i] = k
	}
	s.Radio.P25.Keys = keys
}

func (s *Server) reorderRoutes(w http.ResponseWriter, r *http.Request, u store.User) {
	var ids []string
	if err := readJSON(r, &ids); err != nil {
		writeErr(w, err)
		return
	}
	_, rev, err := s.Store.Update(u.Username, "Reordered outbound routes", expectRev(r), func(c *config.Config) error {
		if len(ids) != len(c.OutboundRoutes) {
			return fmt.Errorf("send every route id exactly once")
		}
		byID := map[string]config.OutboundRoute{}
		for _, rt := range c.OutboundRoutes {
			byID[rt.ID] = rt
		}
		out := make([]config.OutboundRoute, 0, len(ids))
		for _, id := range ids {
			rt, ok := byID[id]
			if !ok {
				return fmt.Errorf("unknown route %s", id)
			}
			out = append(out, rt)
			delete(byID, id)
		}
		c.OutboundRoutes = out
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"rev": rev})
}

type userRequest struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, u store.User) {
	writeJSON(w, 200, map[string]any{"items": s.Store.Users()})
}

func (s *Server) putUser(w http.ResponseWriter, r *http.Request, u store.User) {
	var in userRequest
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if name := r.PathValue("username"); name != "" {
		in.Username = name
	} else if _, exists := s.Store.User(in.Username); exists {
		writeErr(w, config.Problems{{Field: "username", Message: "that username is taken"}})
		return
	}
	if in.Username == u.Username && in.Password != "" {
		// Your own password changes only with the current one (Change your password), so a browser left
		// signed in cannot be used to take the account over.
		writeErr(w, config.Problems{{Field: "password", Message: "change your own password under Change your password, with your current one"}})
		return
	}
	before, existed := s.Store.User(in.Username)
	if err := s.Store.PutUser(store.User{Username: in.Username, Name: in.Name, Role: in.Role}, in.Password); err != nil {
		writeErr(w, err)
		return
	}
	saved, _ := s.Store.User(in.Username)
	if existed && (before.Role != saved.Role || in.Password != "") {
		// A new role or password applies at once: the account signs in again everywhere.
		s.endSessionsFor(saved.Username)
		if saved.Username == u.Username {
			s.startSession(w, r, saved)
		}
	} else {
		s.refreshSessionsFor(saved)
	}
	s.audit(r, u.Username, "user.save", in.Username)
	writeJSON(w, 200, saved)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, u store.User) {
	name := r.PathValue("username")
	if name == u.Username {
		writeError(w, http.StatusBadRequest, "you cannot remove your own account")
		return
	}
	if err := s.Store.DeleteUser(name); err != nil {
		writeErr(w, err)
		return
	}
	s.endSessionsFor(name)
	s.audit(r, u.Username, "user.delete", name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if _, ok := s.Store.Authenticate(u.Username, in.Current); !ok {
		writeErr(w, config.Problems{{Field: "current", Message: "that is not your current password"}})
		return
	}
	full, _ := s.Store.User(u.Username)
	if err := s.Store.PutUser(full, in.New); err != nil {
		writeErr(w, err)
		return
	}
	// Other browsers signed in with the old password are signed out; this one stays.
	s.endSessionsFor(u.Username)
	s.startSession(w, r, full)
	s.audit(r, u.Username, "password.change", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// changedFields names the top-level JSON fields that differ between two structs (secrets by name only).
func changedFields(prefix string, before, after any) []string {
	var a, b map[string]json.RawMessage
	ja, _ := json.Marshal(before)
	jb, _ := json.Marshal(after)
	_ = json.Unmarshal(ja, &a)
	_ = json.Unmarshal(jb, &b)
	var out []string
	for k, v := range b {
		if string(a[k]) != string(v) {
			out = append(out, prefix+"."+k)
		}
	}
	slices.Sort(out)
	return out
}

// listPath names item i of a list inside the configuration as validation does: "ringGroups[2]".
func listPath(cfg *config.Config, list any, i int) string {
	v := reflect.ValueOf(cfg).Elem()
	target := reflect.ValueOf(list).Pointer()
	for f := 0; f < v.NumField(); f++ {
		if v.Field(f).Kind() == reflect.Slice && v.Field(f).Addr().Pointer() == target {
			name, _, _ := strings.Cut(v.Type().Field(f).Tag.Get("json"), ",")
			return fmt.Sprintf("%s[%d]", name, i)
		}
	}
	return ""
}

// scopeProblems makes validation problems about the item being saved relative to it ("number", not
// "queues[2].number"), so the editor marks the right field. Problems elsewhere that the save caused (the
// same number on a menu, say) keep their full path, which no field of this editor matches by accident.
func scopeProblems(err error, prefix string) error {
	var p config.Problems
	if prefix == "" || !errors.As(err, &p) {
		return err
	}
	out := make(config.Problems, 0, len(p))
	for _, x := range p {
		if rest, ok := strings.CutPrefix(x.Field, prefix+"."); ok {
			x.Field = rest
		} else if x.Field == prefix {
			x.Field = ""
		} else {
			x.Field = "other:" + x.Field
		}
		out = append(out, x)
	}
	return out
}

// stillUsed turns the validation failure of a removal into one sentence naming what still uses the item:
// "extension 101 is still used by ring group Front, menu Main menu. Change those first."
func stillUsed(err error, what string, cfg *config.Config) error {
	var p config.Problems
	if !errors.As(err, &p) {
		return err
	}
	var users []string
	seen := map[string]bool{}
	for _, x := range p {
		name := ownerName(cfg, x.Field)
		if !seen[name] {
			seen[name] = true
			users = append(users, name)
		}
	}
	return fmt.Errorf("%s is still used by %s. Change those first, then remove it", what, strings.Join(users, ", "))
}

// ownerName names the item a validation field belongs to: "ringGroups[0].members[1]" is "ring group Front".
func ownerName(cfg *config.Config, field string) string {
	list, rest, _ := strings.Cut(field, "[")
	idx, _, _ := strings.Cut(rest, "]")
	i, _ := strconv.Atoi(idx)
	pick := func(n int, f func(int) string) string {
		if i >= 0 && i < n {
			return f(i)
		}
		return field
	}
	switch list {
	case "extensions":
		return pick(len(cfg.Extensions), func(i int) string { return "extension " + cfg.Extensions[i].Number })
	case "ringGroups":
		return pick(len(cfg.RingGroups), func(i int) string { return "ring group " + cfg.RingGroups[i].Name })
	case "queues":
		return pick(len(cfg.Queues), func(i int) string { return "queue " + cfg.Queues[i].Name })
	case "faxes":
		return pick(len(cfg.Faxes), func(i int) string { return "fax " + cfg.Faxes[i].Name })
	case "pageGroups":
		return pick(len(cfg.PageGroups), func(i int) string { return "paging group " + cfg.PageGroups[i].Name })
	case "directories":
		return pick(len(cfg.Directories), func(i int) string { return "directory " + cfg.Directories[i].Name })
	case "campaigns":
		return pick(len(cfg.Campaigns), func(i int) string { return "campaign " + cfg.Campaigns[i].Name })
	case "audioStreams":
		return pick(len(cfg.AudioStreams), func(i int) string { return "audio stream " + cfg.AudioStreams[i].Name })
	case "toggles":
		return pick(len(cfg.Toggles), func(i int) string { return "toggle " + cfg.Toggles[i].Name })
	case "pinSets":
		return pick(len(cfg.PINSets), func(i int) string { return "PIN set " + cfg.PINSets[i].Name })
	case "lookups":
		return pick(len(cfg.Lookups), func(i int) string { return "caller ID lookup " + cfg.Lookups[i].Name })
	case "dialIns":
		return pick(len(cfg.DialIns), func(i int) string { return "dial-in " + cfg.DialIns[i].Name })
	case "conferences":
		return pick(len(cfg.Conferences), func(i int) string { return "conference room " + cfg.Conferences[i].Name })
	case "menus":
		return pick(len(cfg.Menus), func(i int) string { return "menu " + cfg.Menus[i].Name })
	case "schedules":
		return pick(len(cfg.Schedules), func(i int) string { return "schedule " + cfg.Schedules[i].Name })
	case "scripts":
		return pick(len(cfg.Scripts), func(i int) string { return "script " + cfg.Scripts[i].Name })
	case "inboundRoutes":
		return pick(len(cfg.InboundRoutes), func(i int) string { return "incoming route " + cfg.InboundRoutes[i].Name })
	case "outboundRoutes":
		return pick(len(cfg.OutboundRoutes), func(i int) string { return "outgoing route " + cfg.OutboundRoutes[i].Name })
	case "receptionists":
		return pick(len(cfg.Receptionists), func(i int) string { return "receptionist " + cfg.Receptionists[i].Name })
	case "sites":
		return pick(len(cfg.Sites), func(i int) string { return "site " + cfg.Sites[i].Name })
	case "devices":
		return pick(len(cfg.Devices), func(i int) string { return "phone " + cfg.Devices[i].MAC })
	}
	if strings.HasPrefix(field, "system.") {
		return "Settings"
	}
	return field
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
