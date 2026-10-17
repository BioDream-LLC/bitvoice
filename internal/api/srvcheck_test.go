package api

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/db"
	"github.com/biodream-llc/bitvoice/internal/dbtest"
)

type fakeDNS map[string][]*net.SRV

func (f fakeDNS) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	r, ok := f["_"+service+"._"+proto+"."+name]
	if !ok {
		return "", nil, errors.New("no such name")
	}
	return "", r, nil
}

func (f fakeDNS) LookupHost(ctx context.Context, host string) ([]string, error) {
	return map[string][]string{"a.sip.example.com": {"10.0.0.1"}}[host], nil
}

// The shared name is checked against the running servers: one it misses is named, with the records to add.
func TestSRVCheck(t *testing.T) {
	spec := dbtest.Spec(t)
	if spec == "" {
		t.Skip("set BITVOICE_TEST_POSTGRES")
	}
	s, _ := newServer(t)
	d, err := db.Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, _ := cluster.New(d, t.TempDir(), "1", "10.0.0.1:5060")
	b, _ := cluster.New(d, t.TempDir(), "1", "10.0.0.2:5060")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx, func() cluster.State { return cluster.State{} })
	go b.Run(ctx, func() cluster.State { return cluster.State{} })
	// Each server announces itself on its first beat; wait until a has heard b.
	for end := time.Now().Add(15 * time.Second); len(a.Nodes()) < 2; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("server a sees %d servers", len(a.Nodes()))
		}
	}
	s.Cluster = a
	_, rev := s.Store.Config()
	s.Store.Update("t", "name", rev, func(c *config.Config) error { c.System.Cluster.Name = "sip.example.com"; return nil })
	old := srvLookup
	defer func() { srvLookup = old }()
	srvLookup = fakeDNS{"_sip._udp.sip.example.com": {{Target: "a.sip.example.com.", Port: 5060, Priority: 10, Weight: 50}}}
	out, ok := s.checkSRV(context.Background(), true)
	if !ok || out.Error != "" || len(out.Records) != 1 || len(out.Missing) != 1 || !strings.Contains(out.Missing[0], "10.0.0.2:5060") {
		t.Fatalf("check %+v", out)
	}
	if len(out.Suggested) != 8 || !strings.Contains(strings.Join(out.Suggested, "\n"), "IN A 10.0.0.2") {
		t.Fatalf("suggested %v", out.Suggested)
	}
	srvLookup = fakeDNS{}
	if out, _ := s.checkSRV(context.Background(), true); !strings.Contains(out.Error, "no SRV records") {
		t.Fatalf("no records: %+v", out)
	}
}
