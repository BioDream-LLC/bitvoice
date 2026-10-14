// Command bitvoice-dahdi joins this computer's telephony cards (DAHDI) to BitVoice. Run it on the
// same computer as BitVoice, as a user that may open /dev/dahdi:
//
//	bitvoice-dahdi -token-file /etc/bitvoice/cards.token
//
// It reads its ports from BitVoice (Settings, Telephony cards), registers each phone port as a device
// of its extension, answers the card trunk for the line ports, and reports each port's state back.
// When the ports change in BitVoice it starts again with the new ones.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/biodream-llc/bitvoice/internal/dahdi"
)

var version = "dev"

func main() {
	server := flag.String("server", "https://127.0.0.1:8443", "BitVoice's web interface on this computer")
	token := flag.String("token", "", "the token from Settings, Telephony cards (or -token-file)")
	tokenFile := flag.String("token-file", "", "a file holding the token")
	ca := flag.String("ca", "", "BitVoice's certificate (its data folder's tls/cert.pem) to check it by; without it the loopback connection is not checked")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*server, *token, *tokenFile, *ca, log); err != nil {
		log.Error("bitvoice-dahdi stopped", "err", err)
		os.Exit(1)
	}
}

func run(server, token, tokenFile, ca string, log *slog.Logger) error {
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return errors.New("give the token from Settings, Telephony cards, with -token or -token-file")
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" {
		return errors.New("-server must be BitVoice's https address on this computer")
	}
	if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
		return errors.New("bitvoice-dahdi runs on the same computer as BitVoice: use https://127.0.0.1:PORT")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("%s holds no certificate", ca)
		}
		tc.RootCAs = pool
	} else {
		// The connection never leaves this computer (checked above).
		tc.InsecureSkipVerify = true
	}
	c := &client{base: strings.TrimSuffix(server, "/"), token: token, http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tc}}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var cur *dahdi.Bridge
	var curCfg dahdi.Config
	stopBridge := func() {
		if cur != nil {
			cur.Close()
			cur = nil
		}
	}
	defer stopBridge()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		cfg, err := c.config()
		switch {
		case err != nil:
			log.Warn("cannot read the ports from BitVoice", "err", err)
		case cur == nil || !reflect.DeepEqual(cfg, curCfg):
			stopBridge()
			b, err := dahdi.New(ctx, cfg, nil, log)
			if err != nil {
				log.Error("bridge not started", "err", err)
				break
			}
			cur, curCfg = b, cfg
			log.Info("telephony cards on", "ports", len(cfg.Channels))
		}
		if cur != nil {
			if err := c.status(cur.Status()); err != nil {
				log.Warn("cannot report to BitVoice", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

type client struct {
	base, token string
	http        *http.Client
}

func (c *client) do(method, path string, body any, out any) error {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BitVoice", "1")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *client) config() (dahdi.Config, error) {
	var cfg dahdi.Config
	err := c.do("GET", "/api/cards/bridge", nil, &cfg)
	return cfg, err
}

func (c *client) status(ch map[string]string) error {
	return c.do("POST", "/api/cards/status", map[string]any{"channels": ch, "version": version}, nil)
}
