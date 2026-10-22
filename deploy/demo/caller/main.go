// Command bitvoice-demo-911 makes the public demo's 911 calls: it stands in for an NG911 core (the
// ESInet) and sends BitVoice an invented 911 call every few minutes, with a caller's location; a
// call-taker's phone, registered as an extension, answers it; both sides play their lines as tones,
// and a stand-in speech service writes those tones out as the lines (tone.go). BitVoice treats the
// call like any 911 call: rings, routing trail, map, live transcript, EIDO and the rest to CAD.
//
// It is also the demo's radio system (radio.go): a stand-in P25 home RFSS whose invented units talk
// now and then on the console's talkgroup, sometimes with the emergency button, and report positions.
//
// Demo only: never run it beside a real PSAP. It talks only to 127.0.0.1.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	pbx := flag.String("pbx", "127.0.0.1:5060", "BitVoice's SIP address")
	esinet := flag.String("esinet", "127.0.0.1:5190", "the ESInet stand-in's SIP address: the ESInet trunk's host and port")
	stt := flag.String("stt", "127.0.0.1:8179", "the stand-in speech service's address (AI > speech service: http://ADDR/inference)")
	ext := flag.String("extension", "190", "the call-taker's extension")
	secretFile := flag.String("secret-file", "", "the call-taker extension's SIP password, in a file (or $CREDENTIALS_DIRECTORY/bot-secret)")
	every := flag.Duration("every", 10*time.Minute, "how often a demo 911 call is made")
	rfssAddr := flag.String("rfss", "127.0.0.1:5192", "the stand-in P25 home RFSS's SIP address (the radio channel's home); empty: no radio")
	radioEvery := flag.Duration("radio-every", 3*time.Minute, "how often, on average, a unit talks on the radio")
	apiURL := flag.String("api", "https://127.0.0.1", "BitVoice's web address here, for the units' positions")
	radioTokenFile := flag.String("radio-token-file", "", "a token for the units' positions (or $CREDENTIALS_DIRECTORY/radio-token); none: no positions")
	first := flag.Duration("first", 2*time.Minute, "when the first one is made")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	path := *secretFile
	if path == "" {
		path = filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "bot-secret")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Error("no call-taker password", "err", err)
		os.Exit(1)
	}
	addr, err := netip.ParseAddrPort(*pbx)
	if err != nil {
		log.Error("-pbx", "err", err)
		os.Exit(2)
	}
	d, err := newDemo(log, addr, *esinet, *ext, strings.TrimSpace(string(b)))
	if err != nil {
		log.Error("start", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d.serve(ctx)

	// The stand-in speech service, and POST /call to make a call now.
	mux := http.NewServeMux()
	mux.Handle("/", sttHandler())
	next := make(chan struct{}, 1)
	mux.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		select {
		case next <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	})
	ln, err := net.Listen("tcp", *stt)
	if err != nil {
		log.Error("speech service", "err", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() { <-ctx.Done(); _ = srv.Close() }()

	if *rfssAddr != "" {
		r, err := newRFSS(log, *rfssAddr)
		if err != nil {
			log.Error("radio", "err", err)
			os.Exit(1)
		}
		r.serve(ctx)
		var api *apiClient
		tf := *radioTokenFile
		if tf == "" {
			tf = filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "radio-token")
		}
		if b, err := os.ReadFile(tf); err == nil {
			api = newAPI(strings.TrimRight(*apiURL, "/"), strings.TrimSpace(string(b)))
		}
		go radioTraffic(ctx, log, r, *radioEvery, api)
	}

	wait := *first
	for s := 0; ; s = (s + 1) % len(scenarios) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-next:
		}
		wait = *every
		if err := d.call(ctx, s); err != nil {
			log.Warn("demo 911 call", "scenario", scenarios[s].name, "err", err)
		}
	}
}
