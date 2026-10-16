package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/lost"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The NENA i3 web services (NENA-STA-010.3f, their OpenAPI definitions in the NENA911 repositories):
// the Policy Store, Discrepancy Reporting, Geocode and MSAG conversion, the identity-searchable
// Additional Data Repository, the Service/Agency Locator, Dequeue Registration, the Test Call
// Generator and the BCF's Bad Actor service. Each lives under /i3/<service>/v1 (its Versions also
// under /i3/<service>/Versions), answers only the addresses in Core services' AllowFrom, and is off
// until switched on there.

type i3Docs struct {
	mu       sync.Mutex
	policies *policyIndex
}

type policyIndex struct {
	byKey map[string]i3.Policy
}

func (s *Server) coreDocPath(name string) string {
	return filepath.Join(s.DataDir, "core", name+".json")
}

func (s *Server) loadCore(name string, v any) error {
	b, err := os.ReadFile(s.coreDocPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Server) saveCore(name string, v any) error {
	if s.DataDir == "" {
		return errors.New("this server has no data folder")
	}
	if err := os.MkdirAll(filepath.Dir(s.coreDocPath(name)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", " ")
	tmp := s.coreDocPath(name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.coreDocPath(name))
}

// i3Gate: the service is on and the asker is in AllowFrom.
func (s *Server) i3Gate(w http.ResponseWriter, r *http.Request, on func(config.Core) bool) (config.Config, bool) {
	cfg := s.config()
	if !on(cfg.System.Core) {
		http.NotFound(w, r)
		return cfg, false
	}
	if !allowed(cfg.System.Core.AllowFrom, r.RemoteAddr) {
		i3logError(w, http.StatusForbidden, "not allowed")
		return cfg, false
	}
	return cfg, true
}

func (s *Server) versions(algs []string) map[string]any {
	v := map[string]any{"major": 3, "minor": 0, "vendor": "BioDream LLC"}
	if len(algs) > 0 {
		v["serviceInfo"] = map[string]any{"requiredAlgorithms": algs}
	}
	return map[string]any{"versions": []any{v}, "fingerprint": "BitVoice " + s.Version}
}

// page is the limit and start (1-based) of a list.
func page[T any](r *http.Request, all []T) ([]T, int) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	start, _ := strconv.Atoi(q.Get("start"))
	if start < 1 {
		start = 1
	}
	if start > len(all) {
		return []T{}, len(all)
	}
	out := all[start-1:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, len(all)
}

// jcard is a contact as a jCard (RFC 7095) string: a name and, when given, an email and a phone.
func jcard(contact string) string {
	props := []any{[]any{"version", map[string]any{}, "text", "4.0"}}
	name := strings.TrimSpace(contact)
	var email, tel string
	for _, f := range strings.FieldsFunc(contact, func(r rune) bool { return r == ',' || r == ';' }) {
		f = strings.TrimSpace(f)
		switch {
		case strings.Contains(f, "@"):
			email = f
		case strings.Trim(f, "+0123456789 -().") == "" && len(f) > 4:
			tel = f
		default:
			name = f
		}
	}
	props = append(props, []any{"fn", map[string]any{}, "text", name})
	if email != "" {
		props = append(props, []any{"email", map[string]any{}, "text", email})
	}
	if tel != "" {
		props = append(props, []any{"tel", map[string]any{}, "uri", "tel:" + strings.Map(func(r rune) rune {
			if r == '+' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, tel)})
	}
	b, _ := json.Marshal([]any{"vcard", props})
	return string(b)
}

func versionsRoute(s *Server, base string, on func(config.Core) bool, algs []string) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); ok {
			writeJSON(w, 200, s.versions(algs))
		}
	}
	s.mux.HandleFunc("GET "+base+"/Versions", h)
	s.mux.HandleFunc("GET "+base+"/v1/Versions", h)
}

// ---------------------------------------------------------------- Policy Store (3.3.1)

// StoredPolicy is a policy as kept: the JWS exactly as it came, and what it says.
type StoredPolicy struct {
	JWS      i3.FlatJWS `json:"jws"`
	Owner    string     `json:"owner"`
	Type     string     `json:"type"`
	Queue    string     `json:"queue,omitempty"`
	ID       string     `json:"id,omitempty"`
	Expires  string     `json:"expires,omitempty"`
	Signer   string     `json:"signer"`
	Chained  bool       `json:"chained"`
	Modified time.Time  `json:"modified"`
	From     string     `json:"from"`
}

func (p StoredPolicy) key() string { return p.Type + " " + p.Queue + p.ID }

func (s *Server) storedPolicies() []StoredPolicy {
	var out []StoredPolicy
	_ = s.loadCore("policies", &out)
	return out
}

func (s *Server) savePolicies(ps []StoredPolicy) error {
	if err := s.saveCore("policies", ps); err != nil {
		return err
	}
	s.i3docs.mu.Lock()
	s.i3docs.policies = nil
	s.i3docs.mu.Unlock()
	return nil
}

// PolicyFor is the ESRP's lookup of a stored routing policy (the engine's Policies hook).
func (s *Server) PolicyFor(policyType, queue, id string) (i3.Policy, bool) {
	s.i3docs.mu.Lock()
	idx := s.i3docs.policies
	if idx == nil {
		idx = &policyIndex{byKey: map[string]i3.Policy{}}
		for _, sp := range s.storedPolicies() {
			if p, err := policyOf(sp); err == nil {
				idx.byKey[strings.ToLower(sp.key())] = p
			}
		}
		s.i3docs.policies = idx
	}
	s.i3docs.mu.Unlock()
	p, ok := idx.byKey[strings.ToLower(policyType+" "+queue+id)]
	if ok && p.Expired(time.Now()) {
		return i3.Policy{}, false
	}
	return p, ok
}

func policyOf(sp StoredPolicy) (i3.Policy, error) {
	raw, err := decodeB64(sp.JWS.Payload)
	if err != nil {
		return i3.Policy{}, err
	}
	var p i3.Policy
	return p, json.Unmarshal(raw, &p)
}

// policyAlgs: the i3 algorithm (EdDSA); the others the server can check are taken as well, since a
// PCA may issue ECDSA or RSA certificates, and the standard library has no Ed448.
var policyAlgs = []string{"EdDSA"}

// takePolicy checks and keeps a policy JWS. It reports the Policy Store's status code on refusal.
func (s *Server) takePolicy(cfg config.Config, body []byte, from string, replace *StoredPolicy) (StoredPolicy, int, error) {
	compact, alg, err := i3.ReadJWS(body)
	if err != nil {
		return StoredPolicy{}, 437, err
	}
	if alg == "none" || alg == "" {
		return StoredPolicy{}, 438, errors.New("a policy must be signed (the Versions entry point lists the algorithms)")
	}
	signed, err := i3.VerifyJWS(compact, s.pcaPool(cfg), time.Now())
	if err != nil {
		return StoredPolicy{}, 434, err
	}
	p, err := i3.ParsePolicy(signed.Payload, time.Now())
	if err != nil {
		var pe *i3.PolicyError
		if errors.As(err, &pe) {
			return StoredPolicy{}, pe.Code, err
		}
		return StoredPolicy{}, 437, err
	}
	owner := p.PolicyOwner
	if u, err := url.Parse(owner); err == nil && u.Host != "" {
		owner = u.Hostname()
	} else if _, h, ok := strings.Cut(owner, "@"); ok {
		owner = h
	}
	if signed.Signer != nil && signed.Signer.VerifyHostname(owner) != nil && !strings.EqualFold(signed.Subject(), owner) {
		return StoredPolicy{}, 434, fmt.Errorf("the policy is signed by %s, not by its owner %s", signed.Subject(), owner)
	}
	flat, _ := i3.Flat(compact)
	sp := StoredPolicy{JWS: flat, Owner: p.PolicyOwner, Type: p.PolicyType, Queue: p.PolicyQueueName, ID: p.PolicyID, Expires: p.PolicyExpirationTime,
		Signer: signed.Subject(), Chained: signed.Chained, Modified: time.Now().UTC(), From: from}
	s.i3Mu.Lock()
	defer s.i3Mu.Unlock()
	all := s.storedPolicies()
	at := -1
	for i, o := range all {
		if strings.EqualFold(o.key(), sp.key()) && strings.EqualFold(o.Owner, sp.Owner) {
			at = i
		}
	}
	switch {
	case replace == nil && at >= 0:
		return StoredPolicy{}, 437, errors.New("a policy of that type for that queue or ID is stored already: update it (PUT)")
	case replace != nil && at < 0:
		return StoredPolicy{}, 404, errors.New("no such policy")
	case replace != nil && !(strings.EqualFold(replace.Owner, sp.Owner) && strings.EqualFold(replace.key(), sp.key())):
		return StoredPolicy{}, 437, errors.New("the policy sent is not the one named")
	}
	if at >= 0 {
		all[at] = sp
	} else {
		all = append(all, sp)
	}
	if err := s.savePolicies(all); err != nil {
		return StoredPolicy{}, 454, err
	}
	return sp, 0, nil
}

func policyMatch(q url.Values, p StoredPolicy) bool {
	f := func(k, v string) bool { w := q.Get(k); return w == "" || strings.EqualFold(w, v) }
	return f("policyOwner", p.Owner) && f("policyType", p.Type) && f("policyQueueName", p.Queue) && f("policyId", p.ID)
}

func (s *Server) policyStoreRoutes() {
	on := func(c config.Core) bool { return c.PolicyStore }
	const base = "/i3/PolicyStore/v1"
	versionsRoute(s, "/i3/PolicyStore", on, policyAlgs)
	s.mux.HandleFunc("GET "+base+"/Policies", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		q := r.URL.Query()
		if q.Get("policyOwner")+q.Get("policyType")+q.Get("policyQueueName")+q.Get("policyId") == "" {
			i3logError(w, 454, "name the policy: at least one of policyOwner, policyType, policyQueueName, policyId")
			return
		}
		if t := q.Get("policyType"); t != "" && !slices.Contains(i3.PolicyTypes, t) {
			i3logError(w, 451, "unknown policy type")
			return
		}
		var found []i3.FlatJWS
		for _, p := range s.storedPolicies() {
			if policyMatch(q, p) {
				found = append(found, p.JWS)
			}
		}
		if len(found) == 0 {
			i3logError(w, 404, "no such policy")
			return
		}
		items, total := page(r, found)
		writeJSON(w, 200, map[string]any{"count": len(items), "totalCount": total, "policies": items})
	})
	s.mux.HandleFunc("POST "+base+"/Policies", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			i3logError(w, 454, "the policy is larger than 1 MB")
			return
		}
		if _, code, err := s.takePolicy(cfg, body, clientAddr(r), nil); err != nil {
			i3logError(w, code, err.Error())
			return
		}
		w.WriteHeader(201)
	})
	s.mux.HandleFunc("PUT "+base+"/Policies", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		q := r.URL.Query()
		if q.Get("policyOwner") == "" || q.Get("policyType") == "" {
			i3logError(w, 452, "name the policy's owner and type")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			i3logError(w, 454, "the policy is larger than 1 MB")
			return
		}
		named := StoredPolicy{Owner: q.Get("policyOwner"), Type: q.Get("policyType"), Queue: q.Get("policyQueueName"), ID: q.Get("policyId")}
		if _, code, err := s.takePolicy(cfg, body, clientAddr(r), &named); err != nil {
			if code == 459 {
				code = 460 // the update's own code for a bad expiration time
			}
			i3logError(w, code, err.Error())
			return
		}
		w.WriteHeader(200)
	})
	s.mux.HandleFunc("DELETE "+base+"/Policies", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		q := r.URL.Query()
		if q.Get("policyOwner") == "" || q.Get("policyType") == "" {
			i3logError(w, 452, "name the policy's owner and type")
			return
		}
		if code := s.deletePolicy(q); code != 200 {
			i3logError(w, code, "no such policy")
			return
		}
		w.WriteHeader(200)
	})
	s.mux.HandleFunc("GET "+base+"/PolicyEnums", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		q := r.URL.Query()
		if !slices.Contains(i3.PolicyTypes, q.Get("policyType")) {
			i3logError(w, 451, "unknown policy type")
			return
		}
		since := time.Time{}
		if v := q.Get("policiesUpdatedSince"); v != "" {
			since, _ = i3.ParseTimestamp(v)
		}
		var found []map[string]any
		for _, p := range s.storedPolicies() {
			if !policyMatch(q, p) || p.Modified.Before(since) {
				continue
			}
			exp := p.Expires
			if exp == "" {
				exp = "9999-12-31T23:59:59Z" // never: the field is required
			}
			e := map[string]any{"policyType": p.Type, "policyOwner": p.Owner, "policyExpirationTime": exp, "policyLastModificationTime": p.Modified.Format(time.RFC3339)}
			if p.Queue != "" {
				e["policyQueueName"] = p.Queue
			}
			if p.ID != "" {
				e["policyId"] = p.ID
			}
			found = append(found, e)
		}
		if len(found) == 0 {
			i3logError(w, 404, "no such policy")
			return
		}
		items, total := page(r, found)
		writeJSON(w, 200, map[string]any{"count": len(items), "totalCount": total, "policyEnums": items})
	})
}

func (s *Server) deletePolicy(q url.Values) int {
	s.i3Mu.Lock()
	defer s.i3Mu.Unlock()
	all := s.storedPolicies()
	n := len(all)
	all = slices.DeleteFunc(all, func(p StoredPolicy) bool { return policyMatch(q, p) })
	if len(all) == n {
		return 404
	}
	if s.savePolicies(all) != nil {
		return 454
	}
	return 200
}

// ---------------------------------------------------------------- Discrepancy Reporting (3.7)

// DR is a discrepancy report, received (about this server's services) or filed (about another's).
type DR struct {
	ID         string          `json:"id"`        // discrepancyReportId
	Direction  string          `json:"direction"` // received or filed
	Report     map[string]any  `json:"report"`    // as sent
	At         time.Time       `json:"at"`
	From       string          `json:"from,omitempty"`
	To         string          `json:"to,omitempty"` // filed: the DR service it went to
	Status     string          `json:"status"`       // open, resolved
	Resolution map[string]any  `json:"resolution,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"` // filed: the other side's answer
	Error      string          `json:"error,omitempty"`
}

func (s *Server) drs() []DR {
	var out []DR
	_ = s.loadCore("discrepancies", &out)
	return out
}

// reportTypesHere are the DR types about services BitVoice runs; others are "not ours" (470).
var reportTypesHere = []string{"PolicyStoreDiscrepancyReport", "LostDiscrepancyReport", "BcfDiscrepancyReport", "LoggingDiscrepancyReport",
	"CallTakerDiscrepancyReport", "SipDiscrepancyReport", "PermissionsDiscrepancyReport", "GisDiscrepancyReport", "LisDiscrepancyReport",
	"PolicyDiscrepancyReport", "CallTransferDiscrepancyReport", "McsDiscrepancyReport", "EsrpDiscrepancyReport", "AdrDiscrepancyReport",
	"NetworkDiscrepancyReport", "TestCallDiscrepancyReport", "LogSignatureCertificateDiscrepancyReport"}

func (s *Server) respondingAgency(cfg config.Config) string { return agencyID(cfg) }

func (s *Server) discrepancyRoutes() {
	on := func(c config.Core) bool { return c.Discrepancy }
	const base = "/i3/DiscrepancyReports/v1"
	versionsRoute(s, "/i3/DiscrepancyReports", on, nil)
	s.mux.HandleFunc("POST "+base+"/Reports", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		var rep map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&rep); err != nil {
			i3logError(w, 454, "the report is not JSON")
			return
		}
		for _, k := range []string{"resolutionUri", "reportType", "discrepancyReportSubmitTimeStamp", "discrepancyReportId", "reportingAgencyName", "reportingContactJcard", "problemSeverity"} {
			if v, _ := rep[k].(string); strings.TrimSpace(v) == "" {
				i3logError(w, 454, "the report has no "+k)
				return
			}
		}
		if !slices.Contains(reportTypesHere, rep["reportType"].(string)) {
			i3logError(w, 470, "this server does not run that service")
			return
		}
		d := DR{ID: rep["discrepancyReportId"].(string), Direction: "received", Report: rep, At: time.Now().UTC(), From: clientAddr(r), Status: "open"}
		s.i3Mu.Lock()
		all := s.drs()
		for _, o := range all {
			if o.Direction == "received" && o.ID == d.ID && strings.EqualFold(fmt.Sprint(o.Report["reportingAgencyName"]), fmt.Sprint(rep["reportingAgencyName"])) {
				s.i3Mu.Unlock()
				i3logError(w, 454, "a report with that ID was filed already")
				return
			}
		}
		all = append(all, d)
		err := s.saveCore("discrepancies", all)
		s.i3Mu.Unlock()
		if err != nil {
			i3logError(w, 454, "the report could not be kept")
			return
		}
		s.Bus.Publish("i3.discrepancy", map[string]any{"id": d.ID, "type": rep["reportType"], "from": rep["reportingAgencyName"]})
		writeJSON(w, 201, map[string]any{"respondingAgencyName": s.respondingAgency(cfg), "respondingContactJcard": jcard(cfg.System.Core.DRContact),
			"responseEstimatedReturnTime": time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339), "responseComments": "Received; you will have a resolution at your resolution URI."})
	})
	find := func(w http.ResponseWriter, r *http.Request) (DR, bool) {
		q := r.URL.Query()
		for _, d := range s.drs() {
			if d.Direction == "received" && d.ID == q.Get("discrepancyReportId") {
				if !strings.EqualFold(fmt.Sprint(d.Report["reportingAgencyName"]), q.Get("agencyName")) {
					i3logError(w, 471, "that report is another agency's")
					return d, false
				}
				return d, true
			}
		}
		i3logError(w, 473, "no report with that ID")
		return DR{}, false
	}
	s.mux.HandleFunc("GET "+base+"/Resolutions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		d, ok := find(w, r)
		if !ok {
			return
		}
		if d.Resolution == nil {
			i3logError(w, 475, "no resolution yet")
			return
		}
		writeJSON(w, 200, d.Resolution)
	})
	s.mux.HandleFunc("GET "+base+"/StatusUpdates", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		d, ok := find(w, r)
		if !ok {
			return
		}
		if d.Resolution != nil {
			i3logError(w, 474, "resolved already: see Resolutions")
			return
		}
		writeJSON(w, 200, map[string]any{"respondingAgencyName": s.respondingAgency(cfg), "respondingContactJcard": jcard(cfg.System.Core.DRContact),
			"responseEstimatedReturnTime": d.At.Add(72 * time.Hour).Format(time.RFC3339), "statusComments": "Open: being looked into."})
	})
	// Resolutions of reports this server filed, sent by the agencies that looked into them.
	s.mux.HandleFunc("POST "+base+"/Resolutions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		var res map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&res); err != nil {
			i3logError(w, 454, "the resolution is not JSON")
			return
		}
		id, _ := res["discrepancyReportId"].(string)
		s.i3Mu.Lock()
		defer s.i3Mu.Unlock()
		all := s.drs()
		for i, d := range all {
			if d.Direction == "filed" && d.ID == id {
				if host := hostOf(d.To); host != "" && !strings.EqualFold(host, fmt.Sprint(res["respondingAgencyName"])) && !strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(fmt.Sprint(res["respondingAgencyName"]))) && clientHost(r) != host {
					i3logError(w, 472, "the report was not filed with that agency")
					return
				}
				all[i].Resolution, all[i].Status = res, "resolved"
				if s.saveCore("discrepancies", all) != nil {
					i3logError(w, 454, "the resolution could not be kept")
					return
				}
				s.Bus.Publish("i3.discrepancy", map[string]any{"id": id, "resolved": res["resolution"]})
				w.WriteHeader(201)
				return
			}
		}
		i3logError(w, 473, "no report with that ID was filed here")
	})
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Hostname()
}

func clientHost(r *http.Request) string {
	h := clientAddr(r)
	if i := strings.LastIndex(h, ":"); i > 0 && strings.Count(h, ":") == 1 {
		return h[:i]
	}
	return h
}

// fileDR files a discrepancy report with another agency's DR service and keeps it.
func (s *Server) fileDR(ctx context.Context, cfg config.Config, to, self string, rep map[string]any) (DR, error) {
	id := config.NewID()
	rep["discrepancyReportId"] = id
	rep["discrepancyReportSubmitTimeStamp"] = time.Now().UTC().Format(time.RFC3339)
	rep["reportingAgencyName"] = agencyID(cfg)
	rep["reportingContactJcard"] = jcard(cfg.System.Core.DRContact)
	rep["resolutionUri"] = self + "/i3/DiscrepancyReports/v1"
	d := DR{ID: id, Direction: "filed", Report: rep, At: time.Now().UTC(), To: to, Status: "open"}
	b, _ := json.Marshal(rep)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(to, "/")+"/Reports", strings.NewReader(string(b)))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		var resp *http.Response
		resp, err = s.i3Client(cfg).Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode != 201 {
				err = fmt.Errorf("the DR service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			} else if json.Valid(body) {
				d.Response = body
			}
		}
	}
	if err != nil {
		d.Error = err.Error()
	}
	s.i3Mu.Lock()
	all := append(s.drs(), d)
	serr := s.saveCore("discrepancies", all)
	s.i3Mu.Unlock()
	if err == nil {
		err = serr
	}
	return d, err
}

// i3Client is how this server calls other agencies' i3 services: it trusts the system's roots, the
// PSAP Credentialing Agency's (Core services) and its own certificate, and presents its own.
func (s *Server) i3Client(cfg config.Config) *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if r := strings.TrimSpace(cfg.System.Core.PCARoots); r != "" {
		pool.AppendCertsFromPEM([]byte(r))
	}
	tc := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if c, err := s.signingCert(); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
			pool.AddCert(leaf)
		}
		tc.Certificates = []tls.Certificate{c}
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment}}
}

// resolveDR answers a received report: the resolution goes to the reporter's resolution URI.
func (s *Server) resolveDR(ctx context.Context, cfg config.Config, id, resolution, comments, who string) (DR, error) {
	s.i3Mu.Lock()
	all := s.drs()
	at := -1
	for i, d := range all {
		if d.Direction == "received" && d.ID == id {
			at = i
		}
	}
	if at < 0 {
		s.i3Mu.Unlock()
		return DR{}, errors.New("no such report")
	}
	d := all[at]
	s.i3Mu.Unlock()
	res := map[string]any{"respondingAgencyName": s.respondingAgency(cfg), "respondingContactJcard": jcard(cfg.System.Core.DRContact), "respondingAgentId": who,
		"discrepancyReportId": d.ID, "reportingAgencyName": d.Report["reportingAgencyName"], "problemService": firstNonEmpty(fmt.Sprint(d.Report["problemService"]), fmt.Sprint(d.Report["reportType"])),
		"responseTime": time.Now().UTC().Format(time.RFC3339), "resolution": resolution, "responseComments": comments}
	if a, ok := d.Report["reportingAgentId"].(string); ok && a != "" {
		res["reportingAgentId"] = a
	}
	var err error
	if u, _ := d.Report["resolutionUri"].(string); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		b, _ := json.Marshal(res)
		req, rerr := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(u, "/")+"/Resolutions", strings.NewReader(string(b)))
		if rerr == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, derr := s.i3Client(cfg).Do(req)
			if derr != nil {
				err = derr
			} else {
				resp.Body.Close()
				if resp.StatusCode != 201 {
					err = fmt.Errorf("the reporter's DR service answered %d", resp.StatusCode)
				}
			}
		} else {
			err = rerr
		}
	}
	s.i3Mu.Lock()
	defer s.i3Mu.Unlock()
	all = s.drs()
	for i := range all {
		if all[i].Direction == "received" && all[i].ID == id {
			all[i].Resolution, all[i].Status = res, "resolved"
			if err != nil {
				all[i].Error = "kept here; not delivered: " + err.Error()
			}
			d = all[i]
		}
	}
	if serr := s.saveCore("discrepancies", all); serr != nil {
		return d, serr
	}
	return d, err
}

// ---------------------------------------------------------------- Bad Actor (BCF, 5.2.1.6)

func (s *Server) badActorRoutes() {
	on := func(c config.Core) bool { return c.BCF.Enabled && c.BCF.BadActors }
	versionsRoute(s, "/i3/BadActor", on, nil)
	s.mux.HandleFunc("POST /i3/BadActor/v1/BadActors", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		var id string
		if json.Unmarshal(b, &id) != nil {
			id = strings.TrimSpace(string(b))
		}
		_, err := s.Engine.ReportBadActor(id, clientAddr(r))
		switch {
		case errors.Is(err, pbx.ErrUnknownSource):
			i3logError(w, 433, "no such source ID")
		case errors.Is(err, pbx.ErrAlreadyReported):
			i3logError(w, 432, "already reported")
		case err != nil:
			i3logError(w, 454, err.Error())
		default:
			w.WriteHeader(201)
		}
	})
}

// ---------------------------------------------------------------- Geocode and MSAG conversion

func readPIDFBody(w http.ResponseWriter, r *http.Request) (i3.Location, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		i3logError(w, 454, "too large")
		return i3.Location{}, false
	}
	var doc string
	if json.Unmarshal(b, &doc) != nil {
		doc = string(b)
	}
	loc, err := i3.ParsePIDF([]byte(doc))
	if err != nil {
		i3logError(w, 454, "the body is not a PIDF-LO: "+err.Error())
		return loc, false
	}
	return loc, true
}

// answerObject writes a conversion's answer: JSON, or XML when the caller asks for it.
func answerObject(w http.ResponseWriter, r *http.Request, root, field, value string) {
	if strings.Contains(r.Header.Get("Accept"), "xml") && !strings.Contains(r.Header.Get("Accept"), "json") {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<%s><%s>%s</%s></%s>", root, field, xmlEscape(value), field, root)
		return
	}
	writeJSON(w, 200, map[string]string{field: value})
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) conversionRoutes() {
	gcs := func(c config.Core) bool { return c.Geocode }
	versionsRoute(s, "/i3/Gcs", gcs, nil)
	s.mux.HandleFunc("POST /i3/Gcs/v1/Geocode", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, gcs); !ok {
			return
		}
		loc, ok := readPIDFBody(w, r)
		if !ok {
			return
		}
		a := s.addresses()
		if a == nil || loc.Civic == nil {
			i3logError(w, 468, "no address found")
			return
		}
		pt, ok := a.Geocode(civicTokens(*loc.Civic))
		if !ok {
			i3logError(w, 468, "no address found")
			return
		}
		answerObject(w, r, "GeodeticData", "pidfLoGeo", i3.PointPIDF("pres:geocode@"+elementID(s.config()), pt.Lat, pt.Lon, time.Now()))
	})
	s.mux.HandleFunc("POST /i3/Gcs/v1/ReverseGeocode", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, gcs); !ok {
			return
		}
		loc, ok := readPIDFBody(w, r)
		if !ok {
			return
		}
		a := s.addresses()
		if a == nil || !loc.HasPoint() {
			i3logError(w, 468, "no address found")
			return
		}
		pt, ok := a.Reverse(*loc.Lat, *loc.Lon, 150)
		if !ok {
			i3logError(w, 468, "no address within 150 m")
			return
		}
		answerObject(w, r, "CivicAddress", "pidfLoAddress", i3.CivicPIDF("pres:geocode@"+elementID(s.config()), civicOfPoint(pt), time.Now()))
	})
	mcs := func(c config.Core) bool { return c.MSAG }
	versionsRoute(s, "/i3/Mcs", mcs, nil)
	s.mux.HandleFunc("POST /i3/Mcs/v1/PidfloToMsag", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, mcs); !ok {
			return
		}
		loc, ok := readPIDFBody(w, r)
		if !ok {
			return
		}
		a := s.addresses()
		if a == nil || loc.Civic == nil {
			i3logError(w, 468, "no address found")
			return
		}
		m, ok := a.ToMSAG(civicTokens(*loc.Civic))
		if !ok {
			i3logError(w, 468, "no MSAG address for it (the centerlines carry no MSAG community there)")
			return
		}
		answerObject(w, r, "MsagData", "msagAddress", m)
	})
	s.mux.HandleFunc("POST /i3/Mcs/v1/MsagToPidfLo", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, mcs); !ok {
			return
		}
		b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		var msag string
		if json.Unmarshal(b, &msag) != nil {
			msag = string(b)
		}
		a := s.addresses()
		if a == nil {
			i3logError(w, 468, "no address found")
			return
		}
		c, ok := a.FromMSAG(msag)
		if !ok {
			i3logError(w, 468, "no address found")
			return
		}
		answerObject(w, r, "PidfLoData", "pidfLoAddress", i3.CivicPIDF("pres:msag@"+elementID(s.config()), civicFromTokens(c), time.Now()))
	})
}

// ---------------------------------------------------------------- Identity-searchable ADR (4.11.1)

// ADRRecord is Additional Data kept for a caller: one block (a ServiceInfo, SubscriberInfo, ...).
type ADRRecord struct {
	ID      string    `json:"id"`
	Caller  string    `json:"caller"` // tel:+17175550100 or sip:alice@example.com
	Type    string    `json:"type"`   // EmergencyCallData.SubscriberInfo
	XML     string    `json:"xml"`
	Added   time.Time `json:"added"`
	AddedBy string    `json:"addedBy"`
}

func (s *Server) adrRecords() []ADRRecord {
	var out []ADRRecord
	_ = s.loadCore("adr", &out)
	return out
}

func sameCaller(a, b string) bool {
	norm := func(u string) string {
		u = strings.ToLower(strings.TrimSpace(strings.Trim(u, "<>")))
		u = strings.TrimPrefix(strings.TrimPrefix(u, "sips:"), "sip:")
		u = strings.TrimPrefix(u, "tel:")
		u, _, _ = strings.Cut(u, ";")
		if user, host, ok := strings.Cut(u, "@"); ok && strings.Trim(user, "+0123456789-") == "" {
			_ = host
			u = user
		}
		return strings.Map(func(r rune) rune {
			if r == '-' || r == '.' || r == ' ' || r == '(' || r == ')' {
				return -1
			}
			return r
		}, u)
	}
	return norm(a) != "" && norm(a) == norm(b)
}

func (s *Server) adrRoutes() {
	on := func(c config.Core) bool { return c.ADR }
	versionsRoute(s, "/i3/Adr", on, nil)
	s.mux.HandleFunc("GET /i3/Adr/v1/AdditionalData", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		caller := r.URL.Query().Get("callerUri")
		var found []ADRRecord
		for _, rec := range s.adrRecords() {
			if sameCaller(rec.Caller, caller) {
				found = append(found, rec)
			}
		}
		if caller == "" || len(found) == 0 {
			i3logError(w, 404, "no Additional Data for that caller")
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "json") {
			base := s.WebBase()
			if base == "" {
				base = "https://" + r.Host
			}
			_ = cfg
			writeJSON(w, 200, base+"/i3/Adr/v1/AdditionalData/"+found[0].ID)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, found[0].XML)
	})
	s.mux.HandleFunc("GET /i3/Adr/v1/AdditionalData/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3Gate(w, r, on); !ok {
			return
		}
		for _, rec := range s.adrRecords() {
			if rec.ID == r.PathValue("id") {
				w.Header().Set("Content-Type", "application/"+rec.Type+"+xml")
				io.WriteString(w, rec.XML)
				return
			}
		}
		i3logError(w, 404, "no such block")
	})
}

// ---------------------------------------------------------------- Service/Agency Locator (4.15)

// LocatorRecord is an agency or service as the locator gives it out.
type LocatorRecord struct {
	RecordID                 string   `json:"recordId"`
	ServiceAgencyID          string   `json:"serviceAgencyId"`
	ServiceAgencyName        string   `json:"serviceAgencyName"`
	ServiceAgencyJcard       string   `json:"serviceAgencyJcard"`
	ServiceAgencyTypes       []string `json:"serviceAgencyTypes"`
	EmergencySipInterfaceURI string   `json:"emergencySipInterfaceUri,omitempty"`
	AdminLineURI             string   `json:"adminLineUri,omitempty"`
	LoggerURI                string   `json:"loggerUri"`
	EidoInterfaceURI         string   `json:"eidoInterfaceUri,omitempty"`
	SvcStateURI              string   `json:"svcStateUri,omitempty"`
	DscRptURI                string   `json:"dscRptUri,omitempty"`
	HeadJcard                string   `json:"headJcard,omitempty"`
	OnDutySuperJcard         string   `json:"onDutySuperJcard,omitempty"`
	Names                    []string `json:"names,omitempty"` // the names it is found by
}

func (s *Server) locatorRecords(cfg config.Config) []LocatorRecord {
	var out []LocatorRecord
	_ = s.loadCore("locator", &out)
	base := s.WebBase()
	if a := cfg.System.PSAP.AgencyID; a != "" {
		own := LocatorRecord{RecordID: "self", ServiceAgencyID: a, ServiceAgencyName: firstNonEmpty(cfg.System.Name, a),
			ServiceAgencyJcard: jcard(firstNonEmpty(cfg.System.Core.DRContact, cfg.System.Name)), ServiceAgencyTypes: []string{"psap"},
			Names: []string{firstNonEmpty(cfg.System.Name, a), a}}
		if base != "" {
			own.LoggerURI = base + "/i3log"
			if cfg.System.Core.Discrepancy {
				own.DscRptURI = base + "/i3/DiscrepancyReports/v1"
			}
		}
		if cfg.System.PSAP.EIDOURL != "" {
			own.EidoInterfaceURI = base + "/eido"
		}
		out = append([]LocatorRecord{own}, out...)
	}
	return out
}

func (s *Server) locatorRoutes() {
	on := func(c config.Core) bool { return c.Locator }
	const base = "/i3/ServiceAgencyLocator/v1"
	versionsRoute(s, "/i3/ServiceAgencyLocator", on, nil)
	s.mux.HandleFunc("GET "+base+"/LocatorRecordUris", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		name := r.URL.Query().Get("serviceAgencyName")
		root := s.WebBase()
		if root == "" {
			root = "https://" + r.Host
		}
		var found []map[string]any
		for _, rec := range s.locatorRecords(cfg) {
			hit := strings.EqualFold(rec.ServiceAgencyName, name) || strings.EqualFold(rec.ServiceAgencyID, name)
			for _, n := range rec.Names {
				hit = hit || strings.EqualFold(n, name)
			}
			if hit {
				u := root + base + "/LocatorRecords/" + rec.RecordID
				// "locatorRecordUri" too: the published YAML requires it, though the text names it uri
				found = append(found, map[string]any{"uri": u, "locatorRecordUri": u, "serviceAgencyName": rec.ServiceAgencyName, "uriType": "RecordUri"})
			}
		}
		if len(found) == 0 {
			i3logError(w, 404, "no agency or service by that name")
			return
		}
		items, total := page(r, found)
		writeJSON(w, 200, map[string]any{"count": len(items), "totalCount": total, "locatorUris": items})
	})
	s.mux.HandleFunc("GET "+base+"/LocatorRecords/{id}", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		for _, rec := range s.locatorRecords(cfg) {
			if rec.RecordID == r.PathValue("id") {
				rec.Names = nil
				writeJSON(w, 200, rec)
				return
			}
		}
		i3logError(w, 404, "no such record")
	})
	s.mux.HandleFunc("GET "+base+"/NameSets", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		root := s.WebBase()
		if root == "" {
			root = "https://" + r.Host
		}
		var sets []map[string]any
		for _, rec := range s.locatorRecords(cfg) {
			sets = append(sets, map[string]any{"locatorUri": root + base + "/LocatorRecords/" + rec.RecordID, "names": append([]string{rec.ServiceAgencyName}, rec.Names...)})
		}
		if start, _ := strconv.Atoi(r.URL.Query().Get("start")); start > len(sets) {
			i3logError(w, 441, "beyond the names there are")
			return
		}
		if len(sets) == 0 {
			i3logError(w, 404, "no names")
			return
		}
		items, total := page(r, sets)
		writeJSON(w, 200, map[string]any{"count": len(items), "totalCount": total, "nameSets": items})
	})
}

// ---------------------------------------------------------------- Dequeue Registration (4.2.1.4)

// DequeueReg is a registration to take a queue's calls.
type DequeueReg struct {
	Queue      string    `json:"queueUri"`
	Dequeuer   string    `json:"dequeuerUri"`
	Preference int       `json:"dequeuePreference"`
	Expires    time.Time `json:"expires"`
	From       string    `json:"from"`
}

func (s *Server) dequeueRegs() []DequeueReg {
	var all []DequeueReg
	_ = s.loadCore("dequeue", &all)
	now := time.Now()
	return slices.DeleteFunc(all, func(d DequeueReg) bool { return now.After(d.Expires) })
}

// DequeuersFor is the engine's Dequeuers hook: the live takers of a queue, most preferred first.
func (s *Server) DequeuersFor(queue string) []pbx.Dequeuer {
	if !s.config().System.Core.Dequeue {
		return nil
	}
	var out []pbx.Dequeuer
	for _, d := range s.dequeueRegs() {
		if strings.EqualFold(d.Queue, queue) {
			out = append(out, pbx.Dequeuer{URI: d.Dequeuer, Preference: d.Preference})
		}
	}
	// dequeuePreference 1 to 5: 5 is the most preferred (NENA-STA-010.3 section 4.2.1.4)
	slices.SortStableFunc(out, func(a, b pbx.Dequeuer) int { return b.Preference - a.Preference })
	return out
}

func (s *Server) dequeueRoutes() {
	on := func(c config.Core) bool { return c.Dequeue }
	versionsRoute(s, "/i3/DequeueRegistration", on, nil)
	s.mux.HandleFunc("PUT /i3/DequeueRegistration/v1/Registrations", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		var in struct {
			QueueURI          string `json:"queueUri"`
			DequeuerURI       string `json:"dequeuerUri"`
			ExpirationTime    *int   `json:"expirationTime"`
			DequeuePreference *int   `json:"dequeuePreference"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil || in.QueueURI == "" || in.DequeuerURI == "" || in.ExpirationTime == nil {
			i3logError(w, 400, "send queueUri, dequeuerUri and expirationTime")
			return
		}
		queues := []string{pbxQueueURI(cfg)}
		for _, ps := range cfg.System.Core.PSAPs {
			queues = append(queues, ps.URI)
		}
		if !slices.ContainsFunc(queues, func(q string) bool { return strings.EqualFold(q, in.QueueURI) }) {
			i3logError(w, 456, "not a queue of this server")
			return
		}
		pref := 3
		if in.DequeuePreference != nil {
			pref = *in.DequeuePreference
			if pref < 1 || pref > 5 {
				i3logError(w, 457, "dequeuePreference is 1 to 5")
				return
			}
		}
		maxS := cfg.System.Core.MaxDequeueS
		if maxS == 0 {
			maxS = 86400
		}
		exp := *in.ExpirationTime
		if exp < 0 || exp > maxS {
			i3logError(w, 458, fmt.Sprintf("registrations last %d seconds at most here", maxS))
			return
		}
		s.i3Mu.Lock()
		all := slices.DeleteFunc(s.dequeueRegs(), func(d DequeueReg) bool {
			return strings.EqualFold(d.Queue, in.QueueURI) && strings.EqualFold(d.Dequeuer, in.DequeuerURI)
		})
		if exp > 0 {
			all = append(all, DequeueReg{Queue: in.QueueURI, Dequeuer: in.DequeuerURI, Preference: pref, Expires: time.Now().Add(time.Duration(exp) * time.Second), From: clientAddr(r)})
		}
		err := s.saveCore("dequeue", all)
		s.i3Mu.Unlock()
		if err != nil {
			i3logError(w, 454, "the registration could not be kept")
			return
		}
		writeJSON(w, 200, map[string]any{"expirationTime": exp})
	})
}

func pbxQueueURI(cfg config.Config) string {
	if q := cfg.System.Core.QueueURI; q != "" {
		return q
	}
	host := firstNonEmpty(cfg.System.Core.Source, cfg.System.PublicAddress, "esrp.invalid")
	return "sip:sos@" + host
}

// ---------------------------------------------------------------- Test Call Generator (4.6.17)

// TestCallRequest is a PSAP's request for test calls.
type TestCallRequest struct {
	PSAPID     string               `json:"psapId"`
	PSAPURI    string               `json:"psapUri"`
	Location   string               `json:"location"`
	Every      int                  `json:"frequency"` // minutes
	DRLimit    int                  `json:"discrepancyRateLimit"`
	Start      time.Time            `json:"startDate"`
	End        time.Time            `json:"endDate"`
	Conditions json.RawMessage      `json:"testConditions,omitempty"`
	Last       time.Time            `json:"last,omitzero"`
	Results    []pbx.TestCallResult `json:"results,omitempty"` // the latest, newest first
	From       string               `json:"from"`
}

func (s *Server) testCallRequests() []TestCallRequest {
	var out []TestCallRequest
	_ = s.loadCore("testcalls", &out)
	return out
}

// psapByID: the ESRP's PSAP an agency ID names (its URI's host, or its name).
func psapByID(cfg config.Config, id string) *config.CorePSAP {
	for i, ps := range cfg.System.Core.PSAPs {
		if strings.EqualFold(ps.Name, id) || strings.EqualFold(ps.URI, id) || strings.EqualFold(hostOf(strings.Replace(ps.URI, "sip:", "sip://", 1)), id) {
			return &cfg.System.Core.PSAPs[i]
		}
		if _, h, ok := strings.Cut(ps.URI, "@"); ok && strings.EqualFold(strings.Split(h, ";")[0], id) {
			return &cfg.System.Core.PSAPs[i]
		}
	}
	return nil
}

func (s *Server) testCallRoutes() {
	on := func(c config.Core) bool { return c.TestCalls }
	versionsRoute(s, "/i3/TestCall", on, nil)
	h := func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3Gate(w, r, on)
		if !ok {
			return
		}
		var in struct {
			Location             string          `json:"location"`
			Frequency            int             `json:"frequency"`
			DiscrepancyRateLimit int             `json:"discrepancyRateLimit"`
			StartDate            string          `json:"startDate"`
			EndDate              string          `json:"endDate"`
			TestConditions       json.RawMessage `json:"testConditions"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			i3logError(w, 442, "the request is not JSON")
			return
		}
		ps := psapByID(cfg, r.PathValue("psapId"))
		start, e1 := i3.ParseTimestamp(in.StartDate)
		end, e2 := i3.ParseTimestamp(in.EndDate)
		_, e3 := i3.ParsePIDF([]byte(in.Location))
		switch {
		case ps == nil:
			i3logError(w, 442, "not a PSAP this ESRP sends calls to")
			return
		case e1 != nil || e2 != nil || !end.After(start):
			i3logError(w, 442, "startDate and endDate are timestamps, the end after the start")
			return
		case e3 != nil:
			i3logError(w, 442, "location is a PIDF-LO")
			return
		case in.Frequency < 1:
			i3logError(w, 442, "frequency is the minutes between calls: 1 or more")
			return
		case in.Frequency < 5 || end.Sub(start) > 366*24*time.Hour:
			i3logError(w, 458, "this generator sends a test call every 5 minutes at most, for a year at most")
			return
		}
		req := TestCallRequest{PSAPID: r.PathValue("psapId"), PSAPURI: ps.URI, Location: in.Location, Every: in.Frequency, DRLimit: in.DiscrepancyRateLimit,
			Start: start, End: end, Conditions: in.TestConditions, From: clientAddr(r)}
		s.i3Mu.Lock()
		all := slices.DeleteFunc(s.testCallRequests(), func(o TestCallRequest) bool { return strings.EqualFold(o.PSAPID, req.PSAPID) })
		all = append(all, req)
		err := s.saveCore("testcalls", all)
		s.i3Mu.Unlock()
		if err != nil {
			i3logError(w, 454, "the request could not be kept")
			return
		}
		w.WriteHeader(200)
	}
	// The standard's text names it SendTestCalls (errata 14 of 010.3f); its YAML, SendCallRequests.
	s.mux.HandleFunc("PUT /i3/TestCall/v1/SendTestCalls/{psapId}", h)
	s.mux.HandleFunc("PUT /i3/TestCall/v1/SendCallRequests/{psapId}", h)
}

// RunTestCalls places the test calls PSAPs asked for, when due.
func (s *Server) RunTestCalls(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.testCallsDue(ctx, time.Now())
		}
	}
}

func (s *Server) testCallsDue(ctx context.Context, now time.Time) {
	if !s.config().System.Core.TestCalls {
		return
	}
	for _, req := range s.testCallRequests() {
		if now.Before(req.Start) || now.After(req.End) || !req.Last.IsZero() && now.Sub(req.Last) < time.Duration(req.Every)*time.Minute {
			continue
		}
		s.runTestCall(ctx, req.PSAPID)
	}
}

// runTestCall places one test call for a request now, and keeps the result.
func (s *Server) runTestCall(ctx context.Context, psapID string) (pbx.TestCallResult, error) {
	var req *TestCallRequest
	for _, r := range s.testCallRequests() {
		if strings.EqualFold(r.PSAPID, psapID) {
			r := r
			req = &r
		}
	}
	if req == nil {
		return pbx.TestCallResult{}, errors.New("no such request")
	}
	res, err := s.Engine.PlaceTestCall(ctx, req.PSAPURI, []byte(req.Location))
	if err != nil {
		res.Problem, res.At, res.PSAP = err.Error(), time.Now().UTC(), req.PSAPURI
	}
	s.i3Mu.Lock()
	all := s.testCallRequests()
	for i := range all {
		if strings.EqualFold(all[i].PSAPID, psapID) {
			all[i].Last = time.Now()
			all[i].Results = append([]pbx.TestCallResult{res}, all[i].Results...)
			if len(all[i].Results) > 20 {
				all[i].Results = all[i].Results[:20]
			}
		}
	}
	_ = s.saveCore("testcalls", all)
	s.i3Mu.Unlock()
	s.Bus.Publish("i3.testcall", res)
	return res, err
}

// ---------------------------------------------------------------- the Core services page

func (s *Server) i3StdRoutes() {
	s.policyStoreRoutes()
	s.discrepancyRoutes()
	s.badActorRoutes()
	s.conversionRoutes()
	s.adrRoutes()
	s.locatorRoutes()
	s.dequeueRoutes()
	s.testCallRoutes()

	s.handle("GET /api/core/policies", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []map[string]any{}
		for _, sp := range s.storedPolicies() {
			p, err := policyOf(sp)
			item := map[string]any{"owner": sp.Owner, "type": sp.Type, "queue": sp.Queue, "id": sp.ID, "expires": sp.Expires, "signer": sp.Signer,
				"chained": sp.Chained, "modified": sp.Modified, "from": sp.From, "key": sp.key()}
			if err == nil {
				item["rules"] = p.PolicyRules
				item["expired"] = p.Expired(time.Now())
			}
			out = append(out, item)
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("POST /api/core/policies", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			JWS    string          `json:"jws"`
			Policy json.RawMessage `json:"policy"` // or a policy to sign with this server's certificate
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		cfg := s.config()
		body := []byte(in.JWS)
		if len(in.Policy) > 0 {
			cert, err := s.signingCert()
			if err != nil {
				writeError(w, 409, "this server has no certificate to sign with (Settings › Certificates): upload a JWS signed by the policy's owner instead")
				return
			}
			compact, err := i3.SignJWS(in.Policy, cert)
			if err != nil {
				writeError(w, 409, err.Error())
				return
			}
			body = []byte(compact)
		}
		replace := (*StoredPolicy)(nil)
		if c, _, err := i3.ReadJWS(body); err == nil {
			if signed, err := i3.VerifyJWS(c, nil, time.Now()); err == nil {
				if p, err := i3.ParsePolicy(signed.Payload, time.Now()); err == nil {
					for _, o := range s.storedPolicies() {
						if strings.EqualFold(o.Owner, p.PolicyOwner) && strings.EqualFold(o.key(), p.PolicyType+" "+p.PolicyQueueName+p.PolicyID) {
							o := o
							replace = &o
						}
					}
				}
			}
		}
		sp, code, err := s.takePolicy(cfg, body, "uploaded by "+u.Username, replace)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s (i3 status %d)", err.Error(), code))
			return
		}
		s.audit(r, u.Username, "core.policy.stored", sp.Owner+" "+sp.key())
		writeJSON(w, 200, map[string]any{"key": sp.key(), "owner": sp.Owner})
	})
	s.handle("DELETE /api/core/policies", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if code := s.deletePolicy(r.URL.Query()); code != 200 {
			writeError(w, 404, "no such policy")
			return
		}
		s.audit(r, u.Username, "core.policy.deleted", r.URL.RawQuery)
		w.WriteHeader(204)
	})
	s.handle("GET /api/core/discrepancies", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		all := s.drs()
		slices.SortFunc(all, func(a, b DR) int { return b.At.Compare(a.At) })
		if all == nil {
			all = []DR{}
		}
		writeJSON(w, 200, map[string]any{"items": all})
	})
	s.handle("POST /api/core/discrepancies/{id}/resolve", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Resolution string `json:"resolution"`
			Comments   string `json:"comments"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Resolution == "" {
			writeError(w, 400, "choose a resolution")
			return
		}
		d, err := s.resolveDR(r.Context(), s.config(), r.PathValue("id"), in.Resolution, in.Comments, u.Username)
		if err != nil && d.ID == "" {
			writeError(w, 404, err.Error())
			return
		}
		s.audit(r, u.Username, "core.discrepancy.resolved", d.ID+" "+in.Resolution)
		writeJSON(w, 200, d)
	})
	s.handle("POST /api/core/discrepancies", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			To     string         `json:"to"`
			Report map[string]any `json:"report"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if !strings.HasPrefix(in.To, "https://") && !strings.HasPrefix(in.To, "http://") {
			writeError(w, 400, "the other agency's DR service address (https://.../DiscrepancyReports/v1)")
			return
		}
		if t, _ := in.Report["reportType"].(string); t == "" {
			writeError(w, 400, "choose what the report is about")
			return
		}
		if sev, _ := in.Report["problemSeverity"].(string); sev == "" {
			in.Report["problemSeverity"] = "Minor"
		}
		in.Report["reportingAgentId"] = u.Username
		d, err := s.fileDR(r.Context(), s.config(), in.To, s.selfBase(r), in.Report)
		s.audit(r, u.Username, "core.discrepancy.filed", d.ID+" to "+in.To)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, d)
	})
	s.handle("GET /api/core/badactors", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.BadActors()})
	})
	s.handle("DELETE /api/core/badactors/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !s.Engine.ClearBadActor(r.PathValue("id")) {
			writeError(w, 404, "not reported")
			return
		}
		s.audit(r, u.Username, "core.badactor.cleared", r.PathValue("id"))
		w.WriteHeader(204)
	})
	s.handle("GET /api/core/dequeue", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		all := s.dequeueRegs()
		if all == nil {
			all = []DequeueReg{}
		}
		writeJSON(w, 200, map[string]any{"items": all})
	})
	s.handle("GET /api/core/adr", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		all := s.adrRecords()
		if all == nil {
			all = []ADRRecord{}
		}
		writeJSON(w, 200, map[string]any{"items": all})
	})
	s.handle("POST /api/core/adr", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in ADRRecord
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Caller, in.XML = strings.TrimSpace(in.Caller), strings.TrimSpace(in.XML)
		if !strings.HasPrefix(in.Caller, "tel:") && !strings.HasPrefix(in.Caller, "sip:") {
			writeError(w, 400, "the caller as tel:+number or sip:user@domain")
			return
		}
		types, blocks := i3.BodyParts("application/"+in.Type+"+xml", []byte(in.XML))
		if !strings.HasPrefix(in.Type, "EmergencyCallData.") || len(types) == 0 || len(blocks) == 0 {
			writeError(w, 400, "an Additional Data block (EmergencyCallData.ServiceInfo, .SubscriberInfo, ...) in XML")
			return
		}
		in.ID, in.Added, in.AddedBy = config.NewID(), time.Now().UTC(), u.Username
		s.i3Mu.Lock()
		all := slices.DeleteFunc(s.adrRecords(), func(o ADRRecord) bool { return sameCaller(o.Caller, in.Caller) && o.Type == in.Type })
		all = append(all, in)
		err := s.saveCore("adr", all)
		s.i3Mu.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "core.adr.added", in.Caller+" "+in.Type)
		writeJSON(w, 200, in)
	})
	s.handle("DELETE /api/core/adr/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.i3Mu.Lock()
		all := s.adrRecords()
		n := len(all)
		all = slices.DeleteFunc(all, func(o ADRRecord) bool { return o.ID == r.PathValue("id") })
		err := s.saveCore("adr", all)
		s.i3Mu.Unlock()
		if n == len(all) {
			writeError(w, 404, "no such record")
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
	s.handle("GET /api/core/locator", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		all := s.locatorRecords(s.config())
		if all == nil {
			all = []LocatorRecord{}
		}
		writeJSON(w, 200, map[string]any{"items": all})
	})
	s.handle("POST /api/core/locator", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in LocatorRecord
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if strings.TrimSpace(in.ServiceAgencyID) == "" || strings.TrimSpace(in.ServiceAgencyName) == "" || len(in.ServiceAgencyTypes) == 0 {
			writeError(w, 400, "an agency ID (a domain), a name and at least one type")
			return
		}
		if !strings.HasPrefix(in.LoggerURI, "https://") {
			writeError(w, 400, "the agency's logging service, https://...")
			return
		}
		if in.ServiceAgencyJcard == "" {
			in.ServiceAgencyJcard = jcard(in.ServiceAgencyName)
		}
		in.RecordID = config.NewID()
		s.i3Mu.Lock()
		var all []LocatorRecord
		_ = s.loadCore("locator", &all)
		all = slices.DeleteFunc(all, func(o LocatorRecord) bool { return strings.EqualFold(o.ServiceAgencyID, in.ServiceAgencyID) })
		all = append(all, in)
		err := s.saveCore("locator", all)
		s.i3Mu.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, in)
	})
	s.handle("DELETE /api/core/locator/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.i3Mu.Lock()
		var all []LocatorRecord
		_ = s.loadCore("locator", &all)
		n := len(all)
		all = slices.DeleteFunc(all, func(o LocatorRecord) bool { return o.RecordID == r.PathValue("id") })
		err := s.saveCore("locator", all)
		s.i3Mu.Unlock()
		if n == len(all) {
			writeError(w, 404, "no such record")
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
	s.handle("GET /api/core/testcalls", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		all := s.testCallRequests()
		if all == nil {
			all = []TestCallRequest{}
		}
		writeJSON(w, 200, map[string]any{"items": all})
	})
	s.handle("POST /api/core/testcalls/{psap}/run", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		res, err := s.runTestCall(r.Context(), r.PathValue("psap"))
		if err != nil && res.PSAP == "" {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, res)
	})
	s.handle("DELETE /api/core/testcalls/{psap}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.i3Mu.Lock()
		all := s.testCallRequests()
		n := len(all)
		all = slices.DeleteFunc(all, func(o TestCallRequest) bool { return strings.EqualFold(o.PSAPID, r.PathValue("psap")) })
		err := s.saveCore("testcalls", all)
		s.i3Mu.Unlock()
		if n == len(all) {
			writeError(w, 404, "no such request")
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(204)
	})
}

// signingCert is this server's own certificate (Settings › Certificates), to sign its own policies.
func (s *Server) signingCert() (tls.Certificate, error) {
	if s.Certs == nil {
		return tls.Certificate{}, errors.New("no certificate")
	}
	c, err := s.Certs.Get(nil) // a self-signed one too: its policies then are not traced to the PCA
	if err != nil || c == nil || len(c.Certificate) == 0 {
		return tls.Certificate{}, errors.New("no certificate installed")
	}
	return *c, nil
}

func decodeB64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// civicTokens is a civic address as the LVF reads it (PIDF-LO tokens).
func civicTokens(c i3.Civic) lost.Civic {
	out := lost.Civic{}
	for k, v := range map[string]string{"country": c.Country, "A1": c.State, "A2": c.County, "A3": c.City, "HNO": c.Number, "HNS": c.NumberSuf,
		"PRD": c.PreDir, "RD": c.Street, "STS": c.StreetType, "POD": c.PostDir, "PC": c.PostalCode, "LOC": c.Location} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func civicFromTokens(c lost.Civic) i3.Civic {
	return i3.Civic{Country: firstNonEmpty(c["country"], "US"), State: c["A1"], County: c["A2"], City: c["A3"], Number: c["HNO"], NumberSuf: c["HNS"],
		PreDir: c["PRD"], Street: c["RD"], StreetType: c["STS"], PostDir: c["POD"], PostalCode: c["PC"]}
}

func civicOfPoint(p lost.Point) i3.Civic {
	return i3.Civic{Country: "US", State: p.State, County: p.County, City: p.City, Number: strconv.Itoa(p.Number), NumberSuf: p.NumberSuf,
		PreDir: p.PreDir, Street: p.Name, StreetType: p.Type, PostDir: p.PostDir, PostalCode: p.Postal}
}

// selfBase is this server's address for others to reach its i3 services: its public address, else
// the address the request came in on.
func (s *Server) selfBase(r *http.Request) string {
	if b := s.WebBase(); b != "" {
		return b
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}
