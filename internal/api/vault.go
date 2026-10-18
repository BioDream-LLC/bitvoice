package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cloud"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Secrets from cloud vaults: secret fields may hold aws-sm: or azure-kv: references, resolved here for
// everything that uses the configuration (the engine, mail, AI, archive, sign-in, push, Git).

type vaultState struct {
	mu     sync.Mutex
	vault  *cloud.Vault
	made   config.Vault
	failed map[string]string
}

func (s *Server) vault(v config.Vault) *cloud.Vault {
	s.vaults.mu.Lock()
	defer s.vaults.mu.Unlock()
	if s.vaults.vault == nil || s.vaults.made != v {
		s.vaults.vault = &cloud.Vault{Auth: cloud.VaultAuth{
			AWS:         cloud.Credentials{AccessKey: v.AWSAccessKey, SecretKey: v.AWSSecretKey},
			AzureTenant: v.AzureTenant, AzureClient: v.AzureClientID, AzureSecret: v.AzureClientSecret}}
		s.vaults.made = v
	}
	return s.vaults.vault
}

// Resolved is a configuration with its vault references replaced by the secrets they name. Fields
// that cannot be read are left empty and reported on the Health screen.
func (s *Server) Resolved(c config.Config) config.Config {
	out := c.Clone()
	v := s.vault(c.System.Vault)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	failed := config.ResolveSecrets(&out, cloud.IsReference, func(ref string) (string, error) { return v.Resolve(ctx, ref) })
	msgs := map[string]string{}
	for f, err := range failed {
		msgs[f] = err.Error()
		s.Log.Warn("a secret could not be read from its vault", "field", f, "err", err)
	}
	s.vaults.mu.Lock()
	s.vaults.failed = msgs
	s.vaults.mu.Unlock()
	return out
}

// config is the stored configuration with secrets resolved, for code that uses it.
// publicAddress is the address this server is reached at from outside: its own in a cluster whose
// servers each have one (Servers), else the system's (Settings).
func (s *Server) publicAddress() string {
	if s.Engine != nil {
		return s.Engine.PublicAddress(s.config())
	}
	return s.config().System.PublicAddress
}

func (s *Server) config() config.Config {
	c, _ := s.Store.Config()
	return s.Resolved(c)
}

func (s *Server) vaultRoutes() {
	// Check one reference without saving it.
	s.handle("POST /api/vault/check", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Ref string `json:"ref"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if !cloud.IsReference(in.Ref) {
			writeErr(w, config.Problems{{Field: "ref", Message: "write aws-sm:<region>:<secret> or azure-kv:<vault>/<secret>"}})
			return
		}
		cfg, _ := s.Store.Config()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		val, err := s.vault(cfg.System.Vault).Resolve(ctx, in.Ref)
		if err != nil {
			writeErr(w, config.Problems{{Field: "ref", Message: err.Error()}})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "length": len(val)}) // never the secret itself
	})
}

// vaultProblems are the secrets that could not be read, sorted, for Health.
func (s *Server) vaultProblems() []string {
	s.vaults.mu.Lock()
	defer s.vaults.mu.Unlock()
	var out []string
	for f, m := range s.vaults.failed {
		out = append(out, f+": "+m)
	}
	sort.Strings(out)
	return out
}
