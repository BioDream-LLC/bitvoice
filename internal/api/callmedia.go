package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/evidence"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Caller video and imagery for 911 calls. The caller opens a link (no app, no sign-in) and shares
// live video (WHIP) or sends photos and clips; each file is kept as it came, hashed on arrival
// (SHA-256) and read for its time and GPS (DATA/callmedia/<call>/). Call-takers see everything
// blurred and muted until they choose to reveal it, and every reveal is logged (the exposure log)
// for the centre's wellness programme. Files are kept MediaDays (0: until deleted), held calls kept.

// MediaItem is one photo or clip a caller sent.
type MediaItem struct {
	N        int       `json:"n"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	Received time.Time `json:"received"`
	evidence.Meta
}

type mediaIndex struct {
	Call  string      `json:"call"`
	From  string      `json:"from"`
	Held  bool        `json:"held"`
	Items []MediaItem `json:"items"`
}

// Exposure is one look at a call's video or imagery.
type Exposure struct {
	At   time.Time `json:"at"`
	Call string    `json:"call"`
	Item string    `json:"item"` // "live" or the file's number
	Who  string    `json:"who"`
	Role string    `json:"role,omitempty"` // call-taker, or "link" (CAD or a unit watching by link)
}

var mediaMu sync.Mutex

var callIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)

func (s *Server) mediaDir(call string) string { return filepath.Join(s.DataDir, "callmedia", call) }

func (s *Server) readIndex(call string) (mediaIndex, error) {
	var ix mediaIndex
	b, err := os.ReadFile(filepath.Join(s.mediaDir(call), "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mediaIndex{Call: call, Items: []MediaItem{}}, nil
	}
	if err != nil {
		return ix, err
	}
	err = json.Unmarshal(b, &ix)
	if ix.Items == nil {
		ix.Items = []MediaItem{}
	}
	return ix, err
}

func (s *Server) writeIndex(ix mediaIndex) error {
	b, _ := json.MarshalIndent(ix, "", " ")
	tmp := filepath.Join(s.mediaDir(ix.Call), "index.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.mediaDir(ix.Call), "index.json"))
}

func (s *Server) logExposure(x Exposure) {
	if s.DataDir == "" {
		return
	}
	x.At = time.Now().UTC()
	b, _ := json.Marshal(x)
	mediaMu.Lock()
	defer mediaMu.Unlock()
	dir := filepath.Join(s.DataDir, "callmedia")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "exposure.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

func (s *Server) exposures(from, to time.Time) []Exposure {
	f, err := os.Open(filepath.Join(s.DataDir, "callmedia", "exposure.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Exposure
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var x Exposure
		if json.Unmarshal(sc.Bytes(), &x) == nil && !x.At.Before(from) && x.At.Before(to) {
			out = append(out, x)
		}
	}
	return out
}

// mediaHost is the address a browser reaches media on: the public address when there is one, else
// the host the request came to.
func (s *Server) mediaHostFor(r *http.Request) string {
	if a := s.config().System.PublicAddress; a != "" {
		return a
	}
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		h = r.Host
	}
	return strings.Trim(h, "[]")
}

func readSDP(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/sdp" {
		http.Error(w, "send the offer as application/sdp", http.StatusUnsupportedMediaType)
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		http.Error(w, "the offer is too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return b, true
}

func (s *Server) videoLinkURL(cfg config.Config, r *http.Request, token string) string {
	base := strings.TrimRight(cfg.System.PSAP.VideoLinkBase, "/")
	if base == "" {
		base = "https://" + r.Host
	}
	return base + "/v/" + token
}

func (s *Server) callMediaRoutes() {
	// The caller's page and its script: no sign-in; the token in the link is the key.
	s.mux.HandleFunc("GET /v/{token}", func(w http.ResponseWriter, r *http.Request) {
		if _, caller, _, ok := s.Engine.VideoToken(r.PathValue("token")); !ok || !caller {
			http.Error(w, "This link has expired. Call 911 again if you need help.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=()")
		io.WriteString(w, callerPage)
	})
	s.mux.HandleFunc("GET /v-app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, callerScript)
	})
	s.mux.HandleFunc("POST /whip/{token}", func(w http.ResponseWriter, r *http.Request) {
		offer, ok := readSDP(w, r)
		if !ok {
			return
		}
		ans, res, err := s.Engine.WHIP(r.PathValue("token"), offer, s.mediaHostFor(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/whip/"+r.PathValue("token")+"/"+res)
		w.WriteHeader(http.StatusCreated)
		w.Write(ans)
	})
	s.mux.HandleFunc("DELETE /whip/{token}/{res}", func(w http.ResponseWriter, r *http.Request) {
		if !s.Engine.EndWHIP(r.PathValue("token"), r.PathValue("res")) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// CAD and units watch by the viewing link (no sign-in; the token is the key).
	s.mux.HandleFunc("POST /whep/{token}", func(w http.ResponseWriter, r *http.Request) {
		offer, ok := readSDP(w, r)
		if !ok {
			return
		}
		call, caller, _, ok := s.Engine.VideoToken(r.PathValue("token"))
		if !ok || caller {
			http.NotFound(w, r)
			return
		}
		who := "viewing link from " + clientAddr(r)
		ans, id, err := s.Engine.WHEP(r.PathValue("token"), false, offer, s.mediaHostFor(r), who)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		s.logExposure(Exposure{Call: call, Item: "live", Who: who, Role: "link"})
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/whep/"+r.PathValue("token")+"/"+id)
		w.WriteHeader(http.StatusCreated)
		w.Write(ans)
	})
	s.mux.HandleFunc("DELETE /whep/{token}/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, caller, _, ok := s.Engine.VideoToken(r.PathValue("token")); !ok || caller || !s.Engine.EndWHEP(r.PathValue("id")) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	s.mux.HandleFunc("POST /v/{token}/upload", func(w http.ResponseWriter, r *http.Request) {
		call, caller, _, ok := s.Engine.VideoToken(r.PathValue("token"))
		if !ok || !caller {
			http.Error(w, "This link has expired.", http.StatusNotFound)
			return
		}
		s.takeUpload(w, r, call)
	})

	// Call-takers: send the link, watch, see the files, hold them, the exposure report.
	s.handle("POST /api/calls/{id}/video/link", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		v, err := s.Engine.VideoLink(r.PathValue("id"))
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		cfg := s.config()
		link := s.videoLinkURL(cfg, r, v.Token)
		out := map[string]any{"url": link, "viewUrl": strings.Replace(link, "/v/"+v.Token, "/whep/"+v.ViewToken, 1), "sent": false}
		if from := cfg.System.PSAP.VideoFrom; from != "" {
			var to string
			for _, c := range s.Engine.Calls() {
				if c.ID == r.PathValue("id") {
					to = c.From
				}
			}
			body := "911: to show us what is happening, open this link. Your video goes only to 911. " + link
			if _, err := s.sendText(r.Context(), from, to, body, u.Username); err != nil {
				out["problem"] = "the text was not sent (" + err.Error() + "): read the link to the caller"
			} else {
				out["sent"] = true
			}
		}
		s.audit(r, u.Username, "psap.video.link", r.PathValue("id"))
		writeJSON(w, 200, out)
	})
	s.handle("POST /api/calls/{id}/whep", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		offer, ok := readSDP(w, r)
		if !ok {
			return
		}
		who := firstNonEmpty(u.Name, u.Username)
		ans, id, err := s.Engine.WHEP(r.PathValue("id"), true, offer, s.mediaHostFor(r), who)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "./api/whep/"+id)
		w.WriteHeader(http.StatusCreated)
		w.Write(ans)
	})
	s.handle("DELETE /api/whep/{id}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.Engine.EndWHEP(r.PathValue("id"))
		w.WriteHeader(204)
	})
	s.handle("POST /api/calls/{id}/video/reveal", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var b struct {
			Item string `json:"item"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b)
		if b.Item == "" {
			b.Item = "live"
		}
		s.logExposure(Exposure{Call: r.PathValue("id"), Item: b.Item, Who: firstNonEmpty(u.Name, u.Username), Role: "call-taker"})
		w.WriteHeader(204)
	})
	s.handle("GET /api/calls/{id}/media", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !callIDRe.MatchString(r.PathValue("id")) {
			writeError(w, 404, "no such call")
			return
		}
		ix, err := s.readIndex(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, ix)
	})
	s.handle("GET /api/calls/{id}/media/{n}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		n, err := strconv.Atoi(r.PathValue("n"))
		if !callIDRe.MatchString(id) || err != nil {
			writeError(w, 404, "no such file")
			return
		}
		ix, _ := s.readIndex(id)
		for _, it := range ix.Items {
			if it.N == n {
				s.logExposure(Exposure{Call: id, Item: strconv.Itoa(n), Who: firstNonEmpty(u.Name, u.Username), Role: "call-taker"})
				w.Header().Set("Content-Type", it.Type)
				w.Header().Set("Content-Disposition", "inline; filename=\""+strings.ReplaceAll(it.Name, "\"", "")+"\"")
				w.Header().Set("X-Content-SHA256", it.SHA256)
				http.ServeFile(w, r, filepath.Join(s.mediaDir(id), strconv.Itoa(n)))
				return
			}
		}
		writeError(w, 404, "no such file")
	})
	s.handle("POST /api/calls/{id}/media/hold", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		id := r.PathValue("id")
		var b struct {
			Hold bool `json:"hold"`
		}
		if !callIDRe.MatchString(id) || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b) != nil {
			writeError(w, 400, "send {\"hold\": true}")
			return
		}
		mediaMu.Lock()
		ix, err := s.readIndex(id)
		if err == nil && len(ix.Items) == 0 {
			err = errors.New("this call has no photos or clips")
		}
		if err == nil {
			ix.Held = b.Hold
			err = s.writeIndex(ix)
		}
		mediaMu.Unlock()
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "psap.media.hold", fmt.Sprintf("%s %v", id, b.Hold))
		writeJSON(w, 200, ix)
	})
	s.handle("GET /api/psap/exposure", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		from, to := time.Now().AddDate(0, 0, -30), time.Now().Add(time.Minute)
		if t, err := time.Parse("2006-01-02", r.URL.Query().Get("from")); err == nil {
			from = t
		}
		if t, err := time.Parse("2006-01-02", r.URL.Query().Get("to")); err == nil {
			to = t.AddDate(0, 0, 1)
		}
		xs := s.exposures(from, to)
		type row struct {
			Who   string    `json:"who"`
			Looks int       `json:"looks"`
			Calls int       `json:"calls"`
			Last  time.Time `json:"last"`
		}
		by := map[string]*row{}
		calls := map[string]map[string]bool{}
		for _, x := range xs {
			rw := by[x.Who]
			if rw == nil {
				rw = &row{Who: x.Who}
				by[x.Who], calls[x.Who] = rw, map[string]bool{}
			}
			rw.Looks++
			calls[x.Who][x.Call] = true
			rw.Calls = len(calls[x.Who])
			if x.At.After(rw.Last) {
				rw.Last = x.At
			}
		}
		rows := make([]row, 0, len(by))
		for _, rw := range by {
			rows = append(rows, *rw)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Looks > rows[j].Looks })
		writeJSON(w, 200, map[string]any{"from": from, "to": to, "people": rows})
	})
}

// takeUpload keeps one photo or clip from the caller's page, as it came, with its hash.
func (s *Server) takeUpload(w http.ResponseWriter, r *http.Request, call string) {
	cfg := s.config()
	if s.DataDir == "" {
		http.Error(w, "this server keeps no files", http.StatusServiceUnavailable)
		return
	}
	maxMB := cfg.System.PSAP.UploadMB
	if maxMB == 0 {
		maxMB = 200
	}
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "video/") {
		http.Error(w, "send a photo or a video", http.StatusUnsupportedMediaType)
		return
	}
	mediaMu.Lock()
	ix, err := s.readIndex(call)
	n := len(ix.Items) + 1
	mediaMu.Unlock()
	if err != nil {
		http.Error(w, "the files cannot be read now", http.StatusInternalServerError)
		return
	}
	if n > 100 {
		http.Error(w, "this link has taken as many files as it can", http.StatusTooManyRequests)
		return
	}
	if err := os.MkdirAll(s.mediaDir(call), 0o700); err != nil {
		http.Error(w, "the file cannot be kept now", http.StatusInternalServerError)
		return
	}
	tmp, err := os.CreateTemp(s.mediaDir(call), "upload-*")
	if err != nil {
		http.Error(w, "the file cannot be kept now", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), http.MaxBytesReader(w, r.Body, int64(maxMB)<<20))
	if err != nil {
		tmp.Close()
		http.Error(w, fmt.Sprintf("the file is larger than %d MB", maxMB), http.StatusRequestEntityTooLarge)
		return
	}
	meta := evidence.Read(tmp, size)
	tmp.Close()
	item := MediaItem{Name: name, Type: ct, Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), Received: time.Now().UTC(), Meta: meta}
	mediaMu.Lock()
	defer mediaMu.Unlock()
	ix, err = s.readIndex(call)
	if err == nil {
		item.N = len(ix.Items) + 1
		if ix.From == "" {
			for _, c := range s.Engine.Calls() {
				if c.ID == call {
					ix.From = c.From
				}
			}
		}
		err = os.Rename(tmp.Name(), filepath.Join(s.mediaDir(call), strconv.Itoa(item.N)))
	}
	if err == nil {
		ix.Call = call
		ix.Items = append(ix.Items, item)
		err = s.writeIndex(ix)
	}
	if err != nil {
		http.Error(w, "the file cannot be kept now", http.StatusInternalServerError)
		return
	}
	s.Log.Info("911 caller sent a file", "call", call, "type", ct, "bytes", size, "sha256", item.SHA256)
	s.Bus.Publish("call.media", map[string]any{"call": call, "item": item})
	writeJSON(w, 201, map[string]any{"sha256": item.SHA256, "n": item.N})
}

// RunCallMedia deletes photos, clips and exposure entries older than MediaDays (held calls kept).
func (s *Server) RunCallMedia(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.sweepCallMedia(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) sweepCallMedia(now time.Time) {
	days := s.config().System.PSAP.MediaDays
	if days <= 0 || s.DataDir == "" {
		return
	}
	cut := now.AddDate(0, 0, -days)
	dirs, _ := os.ReadDir(filepath.Join(s.DataDir, "callmedia"))
	mediaMu.Lock()
	defer mediaMu.Unlock()
	for _, d := range dirs {
		if !d.IsDir() || !callIDRe.MatchString(d.Name()) {
			continue
		}
		ix, err := s.readIndex(d.Name())
		if err != nil || ix.Held || len(ix.Items) == 0 {
			continue
		}
		last := ix.Items[len(ix.Items)-1].Received
		if last.Before(cut) {
			os.RemoveAll(s.mediaDir(d.Name()))
			s.Log.Info("911 caller files deleted at the end of their retention", "call", d.Name())
		}
	}
	// the exposure log: entries older than the retention go
	p := filepath.Join(s.DataDir, "callmedia", "exposure.jsonl")
	keep := s.exposures(cut, now.Add(time.Hour))
	var b strings.Builder
	for _, x := range keep {
		j, _ := json.Marshal(x)
		b.Write(append(j, '\n'))
	}
	if _, err := os.Stat(p); err == nil {
		_ = os.WriteFile(p, []byte(b.String()), 0o600)
	}
}

func clientAddr(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
