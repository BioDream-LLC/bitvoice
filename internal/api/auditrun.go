package api

import (
	"context"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// RunAudit writes what the call engine does on its own (blocking an address) into the audit log, next to
// the administrators' actions.
func (s *Server) RunAudit(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if ev.Type != "security.banned" {
				continue
			}
			m, _ := ev.Data.(map[string]any)
			ip, _ := m["ip"].(string)
			reason, _ := m["reason"].(string)
			s.Store.Audit(store.AuditEntry{Actor: "system", Action: "security.ban", Detail: reason, From: ip})
			s.Bus.Publish("audit.added", map[string]string{"action": "security.ban"})
		}
	}
}
