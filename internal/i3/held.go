package i3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Dereference fetches a location by reference: an https: URI is asked with HELD (RFC 5985, as RFC
// 6753 has it for references), and a server that answers a plain GET with a PIDF-LO is taken too.
// A rebid is the same request made again during the call.
func Dereference(ctx context.Context, client *http.Client, uri string) (Location, []byte, error) {
	if !strings.HasPrefix(strings.ToLower(uri), "https:") && !strings.HasPrefix(strings.ToLower(uri), "http:") {
		return Location{}, nil, fmt.Errorf("a location reference by %s cannot be fetched here; only https", strings.SplitN(uri, ":", 2)[0])
	}
	req := `<?xml version="1.0"?>` + "\n" +
		`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held" responseTime="emergencyDispatch">` +
		`<locationType exact="false">any</locationType></locationRequest>`
	return held(ctx, client, uri, req)
}

// LocateNumber asks a location server (an ALI database or a LIS that speaks HELD) where a telephone
// number is: a HELD request naming the device by its number (RFC 6155), for a 911 call that came with
// a callback number and no location, as legacy calls do.
func LocateNumber(ctx context.Context, client *http.Client, server, number string) (Location, []byte, error) {
	if !strings.HasPrefix(strings.ToLower(server), "https:") && !strings.HasPrefix(strings.ToLower(server), "http:") {
		return Location{}, nil, errors.New("the location server's address must be https")
	}
	n := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' {
			return r
		}
		return -1
	}, number)
	if len(strings.TrimPrefix(n, "+")) < 7 {
		return Location{}, nil, errors.New("there is no callback number to look up")
	}
	req := `<?xml version="1.0"?>` + "\n" +
		`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held" responseTime="emergencyDispatch">` +
		`<locationType exact="false">any</locationType>` +
		`<device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><tel>` + n + `</tel></device></locationRequest>`
	return held(ctx, client, server, req)
}

// held makes one HELD location request and reads the PIDF-LO out of the answer.
func held(ctx context.Context, client *http.Client, uri, req string) (Location, []byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, strings.NewReader(req))
	if err != nil {
		return Location{}, nil, err
	}
	r.Header.Set("Content-Type", "application/held+xml;charset=utf-8")
	r.Header.Set("Accept", "application/held+xml, application/pidf+xml")
	resp, err := client.Do(r)
	if err != nil {
		return Location{}, nil, fmt.Errorf("the location server did not answer: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Location{}, nil, err
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		// Some servers serve the document itself to a GET.
		g, _ := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		g.Header.Set("Accept", "application/pidf+xml")
		if resp2, err := client.Do(g); err == nil {
			defer resp2.Body.Close()
			b, _ = io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
			resp = resp2
		}
	}
	if resp.StatusCode/100 != 2 {
		return Location{}, nil, fmt.Errorf("the location server said %s", resp.Status)
	}
	if root, err := parseXML(b); err == nil {
		if e := root.find("error"); e != nil && root.find("presence") == nil {
			return Location{}, nil, errors.New("the location server could not locate the caller: " + firstNonEmpty(e.attrs["code"], collectText(e)))
		}
	}
	// The PIDF-LO is inside the HELD response (or is the whole answer); the reader finds it either
	// way. What is kept to pass on is the presence document alone, when it can be cut out whole.
	doc := b
	if m := presenceRe.FindIndex(b); m != nil {
		if end := bytes.LastIndex(b, []byte("presence>")); end > m[0] {
			doc = b[m[0] : end+len("presence>")]
		}
	}
	loc, err := ParsePIDF(b)
	return loc, doc, err
}

var presenceRe = regexp.MustCompile(`<([A-Za-z0-9_]+:)?presence[\s>]`)
