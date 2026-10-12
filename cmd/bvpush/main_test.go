package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/push"
)

type recorder struct{ got []push.Message }

func (r *recorder) Send(_ context.Context, m push.Message) error {
	r.got = append(r.got, m)
	if m.Token == "gone" {
		return push.ErrGone
	}
	return nil
}

func TestRelay(t *testing.T) {
	file := filepath.Join(t.TempDir(), "servers.json")
	_ = os.WriteFile(file, []byte(`{"inst-1":"a long enough secret"}`), 0o600)
	rec := &recorder{}
	h := &relay{sender: rec, log: slog.New(slog.NewTextHandler(io.Discard, nil)), file: file}
	if err := h.load(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()
	good := &push.Relay{URL: srv.URL, KeyID: "inst-1", Secret: "a long enough secret"}
	if err := good.Send(ctx, push.Message{Platform: "ios", Token: "abc", CallID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || rec.got[0].CallID != "c1" || rec.got[0].TTL == 0 {
		t.Fatalf("forwarded %+v", rec.got)
	}
	if err := good.Send(ctx, push.Message{Platform: "ios", Token: "gone"}); err != push.ErrGone {
		t.Fatalf("gone: %v", err)
	}
	for _, r := range []*push.Relay{{URL: srv.URL, KeyID: "inst-1", Secret: "wrong secret here"}, {URL: srv.URL, KeyID: "nobody", Secret: "a long enough secret"}} {
		if err := r.Send(ctx, push.Message{Token: "x"}); err == nil {
			t.Fatalf("%s accepted", r.KeyID)
		}
	}
	if len(rec.got) != 2 {
		t.Fatal("an unauthorised push was forwarded")
	}
}
