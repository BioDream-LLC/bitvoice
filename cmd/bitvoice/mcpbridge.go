package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// mcpBridge is `bitvoice mcp`: an MCP server over stdio for assistants that start a program (Claude
// Desktop, and others), passing each message to a BitVoice's /mcp over HTTPS with an API token. It
// keeps nothing and decides nothing: every check is the server's.
func mcpBridge(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bitvoice mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("url", os.Getenv("BITVOICE_URL"), "the BitVoice to use, such as https://pbx.example.com:8443")
	tokenFile := fs.String("token-file", "", "a file holding the API token (else the BITVOICE_TOKEN environment variable)")
	pin := fs.String("pin", os.Getenv("BITVOICE_PIN"), "the SHA-256 of BitVoice's certificate, for one that is its own (self-signed); printed when it is needed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *server == "" {
		fmt.Fprintln(stderr, "bitvoice mcp: give -url, the address of your BitVoice (https://...)")
		return 2
	}
	token := strings.TrimSpace(os.Getenv("BITVOICE_TOKEN"))
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(stderr, "bitvoice mcp: cannot read the token file:", err)
			return 1
		}
		token = strings.TrimSpace(string(b))
	}
	if !strings.HasPrefix(token, "bv_") {
		fmt.Fprintln(stderr, "bitvoice mcp: give an API token (BITVOICE_TOKEN, or -token-file), made on Administrators, API tokens")
		return 2
	}
	endpoint := strings.TrimRight(*server, "/") + "/mcp"
	if !strings.HasPrefix(endpoint, "https://") {
		fmt.Fprintln(stderr, "bitvoice mcp: the address must start with https://")
		return 2
	}
	client := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{TLSClientConfig: pinnedTLS(*pin, stderr)}}
	in := bufio.NewReader(stdin)
	out := bufio.NewWriter(stdout)
	for {
		line, err := in.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if reply := forwardMCP(client, endpoint, token, line, stderr); reply != nil {
				out.Write(reply)
				out.WriteByte('\n')
				out.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr, "bitvoice mcp:", err)
				return 1
			}
			return 0
		}
	}
}

// forwardMCP posts one message and returns the answer to write (nil for a notification), or a
// JSON-RPC error of its own when the server cannot be reached.
func forwardMCP(c *http.Client, endpoint, token string, msg []byte, stderr io.Writer) []byte {
	var head struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(msg, &head)
	fail := func(text string) []byte {
		fmt.Fprintln(stderr, "bitvoice mcp:", text)
		if head.ID == nil {
			return nil
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": head.ID, "error": map[string]any{"code": -32000, "message": text}})
		return b
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(msg))
	if err != nil {
		return fail(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(req)
	if err != nil {
		return fail("BitVoice cannot be reached: " + err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fail("the answer could not be read: " + err.Error())
	}
	if resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if !json.Valid(body) {
		return fail(fmt.Sprintf("BitVoice answered %d with something that is not JSON", resp.StatusCode))
	}
	if resp.StatusCode >= 400 && head.ID != nil {
		// The server's error has no id when it refused the request itself (a token, MCP off):
		// answer the client's request with it.
		var e map[string]any
		if json.Unmarshal(body, &e) == nil && e["id"] == nil {
			e["id"] = head.ID
			body, _ = json.Marshal(e)
		}
	}
	// One message per line on stdio: no newline inside it.
	var flat bytes.Buffer
	if err := json.Compact(&flat, body); err != nil {
		return fail("the answer is not JSON")
	}
	return flat.Bytes()
}

// pinnedTLS checks the certificate as usual or, with a pin, accepts exactly the certificate whose
// SHA-256 it is. Without a pin, a certificate that cannot be checked has its pin printed.
func pinnedTLS(pin string, stderr io.Writer) *tls.Config {
	want := strings.ToLower(strings.NewReplacer(":", "", "sha256/", "", " ", "").Replace(pin))
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // checked below: by the system's roots, or by the pin
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			got := hex.EncodeToString(sum[:])
			if want != "" {
				if got != want {
					return fmt.Errorf("the certificate is not the pinned one (it is %s)", got)
				}
				return nil
			}
			opts := x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: x509.NewCertPool()}
			for _, c := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(c)
			}
			if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
				fmt.Fprintf(stderr, "bitvoice mcp: the certificate cannot be checked (%v). If this is your BitVoice with its own certificate, pass -pin %s\n", err, got)
				return err
			}
			return nil
		},
	}
}
