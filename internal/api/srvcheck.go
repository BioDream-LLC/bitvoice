package api

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// SRVCheck is what DNS says about the servers' shared name, against the servers that are running.
type SRVCheck struct {
	Name    string      `json:"name"`
	Records []SRVRecord `json:"records"`
	// Missing are running servers the name does not list; Extra are listed addresses no server has.
	Missing []string `json:"missing"`
	Extra   []string `json:"extra"`
	Error   string   `json:"error,omitempty"`
	// Suggested are the records to create, for every running server, in zone-file form.
	Suggested []string  `json:"suggested"`
	At        time.Time `json:"at"`
}

// SRVRecord is one target the name lists.
type SRVRecord struct {
	Service  string   `json:"service"` // _sip._udp, _sip._tcp, _sips._tcp
	Target   string   `json:"target"`
	Port     int      `json:"port"`
	Priority int      `json:"priority"`
	Weight   int      `json:"weight"`
	Addrs    []string `json:"addrs"`
}

// resolver is the DNS the check asks (a fake in tests).
type resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

var srvLookup resolver = net.DefaultResolver

type srvCache struct {
	mu   sync.Mutex
	last SRVCheck
}

// checkSRV looks the name up (cached a minute unless fresh is asked for).
func (s *Server) checkSRV(ctx context.Context, fresh bool) (SRVCheck, bool) {
	cfg := s.config()
	name := cfg.System.Cluster.Name
	if name == "" || s.Cluster == nil {
		return SRVCheck{}, false
	}
	s.srv.mu.Lock()
	if !fresh && s.srv.last.Name == name && time.Since(s.srv.last.At) < time.Minute {
		out := s.srv.last
		s.srv.mu.Unlock()
		return out, true
	}
	s.srv.mu.Unlock()
	out := SRVCheck{Name: name, Records: []SRVRecord{}, Missing: []string{}, Extra: []string{}, At: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listed := map[string]bool{} // ip:port
	for _, svc := range []struct{ service, proto string }{{"sip", "udp"}, {"sip", "tcp"}, {"sips", "tcp"}} {
		_, recs, err := srvLookup.LookupSRV(ctx, svc.service, svc.proto, name)
		if err != nil {
			continue
		}
		for _, r := range recs {
			rec := SRVRecord{Service: "_" + svc.service + "._" + svc.proto, Target: strings.TrimSuffix(r.Target, "."), Port: int(r.Port),
				Priority: int(r.Priority), Weight: int(r.Weight), Addrs: []string{}}
			if ips, err := srvLookup.LookupHost(ctx, rec.Target); err == nil {
				rec.Addrs = ips
				for _, ip := range ips {
					p := rec.Port
					if svc.service == "sips" {
						p-- // each server takes TLS on the port after its SIP port
					}
					listed[net.JoinHostPort(ip, strconv.Itoa(p))] = true
				}
			}
			out.Records = append(out.Records, rec)
		}
	}
	if len(out.Records) == 0 {
		out.Error = "DNS has no SRV records for " + name + " (_sip._udp, _sip._tcp or _sips._tcp)"
	}
	have := map[string]bool{}
	for i, n := range s.Cluster.Nodes() {
		if !n.Alive {
			continue
		}
		host, port, err := net.SplitHostPort(n.SIPAddr)
		if err != nil {
			continue
		}
		have[n.SIPAddr] = true
		if !listed[n.SIPAddr] {
			out.Missing = append(out.Missing, n.Name+" ("+n.SIPAddr+")")
		}
		p, _ := strconv.Atoi(port)
		target := strings.ToLower(strings.ReplaceAll(n.Name, " ", "-")) + "." + name
		pr := 10 + i*10
		out.Suggested = append(out.Suggested,
			"_sip._udp."+name+". 300 IN SRV "+strconv.Itoa(pr)+" 50 "+port+" "+target+".",
			"_sip._tcp."+name+". 300 IN SRV "+strconv.Itoa(pr)+" 50 "+port+" "+target+".",
			"_sips._tcp."+name+". 300 IN SRV "+strconv.Itoa(pr)+" 50 "+strconv.Itoa(p+1)+" "+target+".",
			target+". 300 IN A "+host)
	}
	for a := range listed {
		if !have[a] {
			out.Extra = append(out.Extra, a)
		}
	}
	slices.Sort(out.Extra)
	s.srv.mu.Lock()
	s.srv.last = out
	s.srv.mu.Unlock()
	return out, true
}

func (s *Server) srvRoutes() {
	s.handle("GET /api/servers/dns", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out, ok := s.checkSRV(r.Context(), r.URL.Query().Get("fresh") == "1")
		if !ok {
			writeJSON(w, 200, map[string]any{"configured": false})
			return
		}
		writeJSON(w, 200, map[string]any{"configured": true, "check": out})
	})
}

// srvProblems warns when the shared name misses a running server.
func (s *Server) srvProblems(ctx context.Context, add func(id, level, title, detail, link string)) {
	out, ok := s.checkSRV(ctx, false)
	switch {
	case !ok:
	case out.Error != "":
		add("dns-srv", "warn", "Phones cannot find the servers by "+out.Name, out.Error+". Servers lists the records to create.", "/servers")
	case len(out.Missing) > 0:
		add("dns-srv", "warn", out.Name+" does not list every server", "Phones that look the servers up by name will not find "+strings.Join(out.Missing, ", ")+". Servers lists the records to add.", "/servers")
	}
}
