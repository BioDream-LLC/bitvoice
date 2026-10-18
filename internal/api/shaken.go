package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// stirCert serves the STI certificate of a trunk that signs its calls and names no address of its own.
func (s *Server) stirCert(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutSuffix(r.PathValue("file"), ".pem")
	cfg := s.config()
	for _, t := range cfg.Trunks {
		if ok && t.ID == id && t.Shaken.Sign && t.Shaken.X5U == "" && t.Shaken.Cert != "" {
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			w.Header().Set("Cache-Control", "max-age=3600")
			_, _ = w.Write([]byte(t.Shaken.Cert))
			return
		}
	}
	http.NotFound(w, r)
}

// WebBase is this server's https:// address for the outside world: its public address (or name) and
// the HTTPS port; empty when no public address is set.
func (s *Server) WebBase() string {
	host := s.publicAddress()
	port := int(s.HTTPSPort.Load())
	if host == "" || port == 0 {
		return ""
	}
	if port == 443 {
		return "https://" + host
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port))
}
