// Command bvpush is the push relay: BitVoice servers send it wake-ups for their mobile apps, signed with a
// per-server secret, and it forwards them to Apple and Google with the app's credentials, which never
// leave it. BioDream runs one for its customers; anyone can run their own with their own apps.
//
//	bvpush -listen :8443 -cert cert.pem -key key.pem -servers servers.json \
//	       -apns-key AuthKey.p8 -apns-key-id KEYID -apns-team TEAMID -apns-topic llc.biodream.bitvoice \
//	       -fcm service-account.json
//
// servers.json maps each server's key ID to its secret: {"inst-1": "a long random secret"}.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/biodream-llc/bitvoice/internal/push"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bvpush:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("bvpush", flag.ContinueOnError)
	listen := fs.String("listen", ":8443", "address to listen on")
	cert := fs.String("cert", "", "TLS certificate (PEM); empty serves plain HTTP behind a TLS proxy")
	key := fs.String("key", "", "TLS key (PEM)")
	servers := fs.String("servers", "servers.json", "file mapping server key IDs to secrets")
	apnsKey := fs.String("apns-key", "", "Apple .p8 key file")
	apnsID := fs.String("apns-key-id", "", "Apple key ID")
	apnsTeam := fs.String("apns-team", "", "Apple team ID")
	apnsTopic := fs.String("apns-topic", "", "the iOS app's bundle ID")
	sandbox := fs.Bool("apns-sandbox", false, "use Apple's development service")
	fcm := fs.String("fcm", "", "Firebase service account JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var p push.Platforms
	if *apnsKey != "" {
		b, err := os.ReadFile(*apnsKey)
		if err != nil {
			return err
		}
		p.IOS = &push.APNs{KeyID: *apnsID, TeamID: *apnsTeam, Topic: *apnsTopic, Key: string(b), Sandbox: *sandbox}
	}
	if *fcm != "" {
		b, err := os.ReadFile(*fcm)
		if err != nil {
			return err
		}
		p.Android = &push.FCM{ServiceAccount: string(b)}
	}
	if p.IOS == nil && p.Android == nil {
		return errors.New("set up Apple (-apns-key) or Google (-fcm), or both")
	}
	h := &relay{sender: p, log: log, file: *servers}
	if err := h.load(); err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	go h.reloadOnHUP(ctx)
	log.Info("push relay listening", "address", *listen)
	var err error
	if *cert != "" {
		err = srv.ListenAndServeTLS(*cert, *key)
	} else {
		err = srv.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type relay struct {
	sender push.Sender
	log    *slog.Logger
	file   string
	mu     sync.RWMutex
	keys   map[string]string
	rate   sync.Map // key -> *bucket
}

func (h *relay) load() error {
	b, err := os.ReadFile(h.file)
	if err != nil {
		return err
	}
	var keys map[string]string
	if err := json.Unmarshal(b, &keys); err != nil {
		return fmt.Errorf("%s: %w", h.file, err)
	}
	for k, s := range keys {
		if len(s) < 16 {
			return fmt.Errorf("%s: the secret for %s is shorter than 16 characters", h.file, k)
		}
	}
	h.mu.Lock()
	h.keys = keys
	h.mu.Unlock()
	return nil
}

// reloadOnHUP rereads the server list on SIGHUP, so servers are added without a restart.
func (h *relay) reloadOnHUP(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if err := h.load(); err != nil {
				h.log.Warn("server list not reloaded", "err", err)
			} else {
				h.log.Info("server list reloaded")
			}
		}
	}
}

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// allow is a token bucket per server: 20 pushes a second, bursts of 100.
func (h *relay) allow(key string) bool {
	v, _ := h.rate.LoadOrStore(key, &bucket{tokens: 100, last: time.Now()})
	b := v.(*bucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(100, b.tokens+now.Sub(b.last).Seconds()*20)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (h *relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/push" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	key, err := push.Verify(r, body, func(k string) (string, bool) {
		h.mu.RLock()
		defer h.mu.RUnlock()
		s, ok := h.keys[k]
		return s, ok
	}, time.Now())
	if err != nil {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	if !h.allow(key) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	var m struct {
		push.Message
		TTLSeconds int `json:"ttlSeconds"`
	}
	if err := json.Unmarshal(body, &m); err != nil || m.Token == "" {
		http.Error(w, "bad message", http.StatusBadRequest)
		return
	}
	m.TTL = time.Duration(min(max(m.TTLSeconds, 5), 120)) * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch err := h.sender.Send(ctx, m.Message); {
	case errors.Is(err, push.ErrGone):
		w.WriteHeader(http.StatusGone)
	case err != nil:
		h.log.Warn("push failed", "server", key, "platform", m.Platform, "err", err)
		http.Error(w, "push failed", http.StatusBadGateway)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}
