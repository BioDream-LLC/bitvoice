// Package cloud uploads BitVoice's backups, recordings and voicemail to Amazon S3 or Azure Blob Storage
// over their REST APIs, with no cloud SDK: AWS Signature Version 4 is written here on crypto/hmac and
// crypto/sha256, and Azure uploads use a container SAS URL.
package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials are AWS access keys (a session token with temporary ones).
type Credentials struct {
	AccessKey, SecretKey, Token string
	Expires                     time.Time
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// uriEncode is SigV4's encoding: unreserved characters stay, everything else is %XX; slashes stay in paths.
func uriEncode(s string, path bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && path:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Sign adds AWS Signature Version 4 headers to a request. payloadHash is the hex SHA-256 of the body.
func Sign(r *http.Request, c Credentials, region, service, payloadHash string, now time.Time) {
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	r.Header.Set("X-Amz-Date", amzDate)
	if c.Token != "" {
		r.Header.Set("X-Amz-Security-Token", c.Token)
	}
	if service == "s3" {
		r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	// Canonical headers: host plus every header set so far, lower-cased and sorted.
	headers := map[string]string{"host": host}
	for k, v := range r.Header {
		headers[strings.ToLower(k)] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")
	// Canonical query string: sorted by key, then value.
	q := r.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			pairs = append(pairs, uriEncode(k, false)+"="+uriEncode(v, false))
		}
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if service == "s3" {
		path = uriEncode(r.URL.Path, true) // S3 encodes the path once
	}
	canon := strings.Join([]string{r.Method, path, strings.Join(pairs, "&"), canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canon))
	k := hmacSHA256([]byte("AWS4"+c.SecretKey), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	r.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", c.AccessKey, scope, signed, sig))
}

// Target is where uploads go.
type Target interface {
	Put(ctx context.Context, name string, body []byte, contentType string) error
	Describe() string
}

// S3 is an Amazon S3 bucket. Empty keys use the EC2 or ECS instance role.
type S3 struct {
	Region, Bucket, Prefix string
	Endpoint               string // optional: an S3-compatible service
	Creds                  Credentials
	HTTP                   *http.Client
}

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Describe names the target for the GUI.
func (s *S3) Describe() string { return "s3://" + s.Bucket + "/" + s.Prefix }

// Put uploads an object.
func (s *S3) Put(ctx context.Context, name string, body []byte, contentType string) error {
	creds := s.Creds
	if creds.AccessKey == "" {
		c, err := InstanceCredentials(ctx, s.client())
		if err != nil {
			_ = err // the metadata service is simply absent off AWS; its error helps nobody
			return errors.New("enter an access key and secret key: this computer is not an AWS server with an instance role")
		}
		creds = c
	}
	host := s.Bucket + ".s3." + s.Region + ".amazonaws.com"
	base := "https://" + host
	if s.Endpoint != "" {
		base = strings.TrimRight(s.Endpoint, "/") + "/" + s.Bucket
	}
	key := strings.TrimLeft(strings.TrimSuffix(s.Prefix, "/")+"/"+name, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/"+uriEncode(key, true), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	Sign(req, creds, s.Region, "s3", sha256Hex(body), time.Now())
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("S3 answered %d: %s", resp.StatusCode, s3Error(msg))
	}
	return nil
}

func s3Error(xmlBody []byte) string {
	s := string(xmlBody)
	if i := strings.Index(s, "<Message>"); i >= 0 {
		if j := strings.Index(s[i:], "</Message>"); j > 0 {
			return s[i+9 : i+j]
		}
	}
	return strings.TrimSpace(s)
}

// InstanceCredentials reads the instance role's temporary keys: the ECS task role when running in ECS,
// otherwise EC2's instance metadata service (IMDSv2, with its session token).
func InstanceCredentials(ctx context.Context, hc *http.Client) (Credentials, error) {
	var c Credentials
	var out struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      time.Time
	}
	get := func(u string, hdr map[string]string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resp, err := hc.Do(req.WithContext(ctx2))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("metadata answered %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	}
	const imds = "http://169.254.169.254/latest"
	treq, _ := http.NewRequestWithContext(ctx, http.MethodPut, imds+"/api/token", nil)
	treq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tresp, err := hc.Do(treq.WithContext(tctx))
	if err != nil {
		return c, err
	}
	tok, _ := io.ReadAll(io.LimitReader(tresp.Body, 4096))
	tresp.Body.Close()
	h := map[string]string{"X-aws-ec2-metadata-token": string(tok)}
	role, err := get(imds+"/meta-data/iam/security-credentials/", h)
	if err != nil {
		return c, err
	}
	name := strings.TrimSpace(strings.SplitN(string(role), "\n", 2)[0])
	if name == "" {
		return c, errors.New("this instance has no IAM role")
	}
	b, err := get(imds+"/meta-data/iam/security-credentials/"+url.PathEscape(name), h)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return c, err
	}
	return Credentials{AccessKey: out.AccessKeyID, SecretKey: out.SecretAccessKey, Token: out.Token, Expires: out.Expiration}, nil
}

// AzureBlob is an Azure Storage container reached with a SAS URL (Storage account > Containers > Shared
// access tokens, with create and write permission).
type AzureBlob struct {
	ContainerSAS string // https://account.blob.core.windows.net/container?sv=...&sig=...
	Prefix       string
	HTTP         *http.Client
}

// Describe names the target for the GUI without the secret part of the URL.
func (a *AzureBlob) Describe() string {
	u, err := url.Parse(a.ContainerSAS)
	if err != nil {
		return "Azure Blob"
	}
	return u.Scheme + "://" + u.Host + u.Path + "/" + a.Prefix
}

// Put uploads a block blob.
func (a *AzureBlob) Put(ctx context.Context, name string, body []byte, contentType string) error {
	u, err := url.Parse(a.ContainerSAS)
	if err != nil || u.Scheme != "https" || u.RawQuery == "" {
		return errors.New("the container address must be an https SAS URL")
	}
	key := strings.TrimLeft(strings.TrimSuffix(a.Prefix, "/")+"/"+name, "/")
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", "2023-11-03")
	req.Header.Set("Content-Type", contentType)
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Azure answered %d: %s", resp.StatusCode, s3Error(msg))
	}
	return nil
}
