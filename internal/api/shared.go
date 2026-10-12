package api

import (
	"context"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/vip"
)

// sharedState is the shared address as this server sees it, for the Servers page and Health.
type sharedState struct {
	mu      sync.Mutex
	Address string    `json:"address"`
	Held    bool      `json:"held"`
	Holder  string    `json:"holder"`
	Error   string    `json:"error,omitempty"`
	Since   time.Time `json:"since,omitzero"`
}

func (s *Server) sharedStatus() sharedState {
	s.shared.mu.Lock()
	defer s.shared.mu.Unlock()
	return sharedState{Address: s.shared.Address, Held: s.shared.Held, Holder: s.shared.Holder, Error: s.shared.Error, Since: s.shared.Since}
}

// RunSharedAddress takes the shared address while this server holds its lease, and lets it go when
// it does not; the server taking it adopts the phones registered through it.
func (s *Server) RunSharedAddress(ctx context.Context) {
	if s.Cluster == nil {
		return
	}
	var held *vip.Address
	release := func() {
		if held != nil {
			if err := held.Release(); err != nil {
				s.Log.Warn("could not let go of the shared address", "err", err)
			}
			held = nil
		}
	}
	defer release()
	var lastAdopt time.Time
	t := time.NewTicker(250 * time.Millisecond) // take the address the moment the lease is ours
	defer t.Stop()
	for {
		cfg := s.config()
		k := cfg.System.Cluster
		node, mine := s.Cluster.TrunkOwner(cluster.AddressLease)
		st := sharedState{Address: k.SharedAddress, Holder: s.Cluster.NodeName(node)}
		if k.SharedAddress == "" {
			release()
		} else if a, err := vip.Parse(k.SharedAddress, k.Interface); err != nil {
			st.Error = err.Error()
		} else if mine && held == nil {
			if err := a.Take(); err != nil {
				st.Error = err.Error()
			} else {
				held = &a
				n := s.Engine.Adopt(s.Cluster.Orphans(ctx))
				s.Log.Info("took the shared address", "address", k.SharedAddress, "interface", a.Interface, "phones adopted", n)
				s.Bus.Publish("servers.changed", map[string]any{})
			}
		} else if mine && held != nil && time.Since(lastAdopt) >= 2*time.Second {
			// Phones of a server that stopped only just now: it is marked stopped a moment after the
			// lease passes, so keep looking while holding the address.
			lastAdopt = time.Now()
			if n := s.Engine.Adopt(s.Cluster.Orphans(ctx)); n > 0 {
				s.Log.Info("adopted phones registered through the shared address", "phones", n)
			}
		} else if !mine && held != nil && node != "" && node != s.Cluster.Self().ID {
			// Another server holds the lease now (a late beat here alone is not a reason to let go).
			release()
			s.Log.Info("let go of the shared address", "to", st.Holder)
		}
		st.Held = held != nil
		s.shared.mu.Lock()
		if st.Held && !s.shared.Held {
			st.Since = time.Now()
		} else {
			st.Since = s.shared.Since
		}
		s.shared.Address, s.shared.Held, s.shared.Holder, s.shared.Error, s.shared.Since = st.Address, st.Held, st.Holder, st.Error, st.Since
		s.shared.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// HoldsSharedAddress says whether this server holds the shared address now.
func (s *Server) HoldsSharedAddress() bool { return s.sharedStatus().Held }
