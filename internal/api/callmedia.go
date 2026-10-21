package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/media"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	// Recording: BitVoice recorded it from the caller's live video (not a file the caller sent).
	Recording bool `json:"recording,omitempty"`
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
	if a := s.publicAddress(); a != "" {
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
	s.mux.HandleFunc("GET /v/{token}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
		if _, caller, _, ok := s.Engine.VideoToken(r.PathValue("token")); !ok || !caller {
			http.Error(w, "This link has expired. Call 911 again if you need help.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=()")
		io.WriteString(w, callerPage)
	}))
	s.mux.HandleFunc("GET /v-app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, callerScript)
	})
	s.mux.HandleFunc("POST /whip/{token}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	s.mux.HandleFunc("DELETE /whip/{token}/{res}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
		if !s.Engine.EndWHIP(r.PathValue("token"), r.PathValue("res")) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	// CAD and units watch by the viewing link (no sign-in; the token is the key).
	s.mux.HandleFunc("POST /whep/{token}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	s.mux.HandleFunc("DELETE /whep/{token}/{id}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
		if _, caller, _, ok := s.Engine.VideoToken(r.PathValue("token")); !ok || caller || !s.Engine.EndWHEP(r.PathValue("id")) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	// CAD fetches a call's photos and clips by the viewing token, as the EIDO names them.
	s.mux.HandleFunc("GET /m/{token}/{n}", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
		call, caller, _, ok := s.Engine.VideoToken(r.PathValue("token"))
		n, err := strconv.Atoi(r.PathValue("n"))
		if !ok || caller || err != nil || !s.serveMedia(w, r, call, n, Exposure{Who: "viewing link from " + clientAddr(r), Role: "link"}) {
			http.NotFound(w, r)
		}
	}))
	s.mux.HandleFunc("POST /v/{token}/upload", s.tokenGuard(func(w http.ResponseWriter, r *http.Request) {
		call, caller, _, ok := s.Engine.VideoToken(r.PathValue("token"))
		if !ok || !caller {
			http.Error(w, "This link has expired.", http.StatusNotFound)
			return
		}
		s.takeUpload(w, r, call)
	}))

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
		from := cfg.System.PSAP.VideoFrom
		var to string
		for _, c := range s.Engine.Calls() {
			if c.ID == r.PathValue("id") {
				to = c.From
			}
		}
		if problem := videoTextProblem(from, to); problem != "" {
			out["problem"] = problem
		} else {
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
		if !s.serveMedia(w, r, id, n, Exposure{Who: firstNonEmpty(u.Name, u.Username), Role: "call-taker"}) {
			writeError(w, 404, "no such file")
		}
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
	if !mediaTypeOK(ct) {
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

// mediaTypeOK: the kinds of file a caller may send. Only pictures and video a browser shows as
// such: no SVG or other type that carries script.
func mediaTypeOK(ct string) bool {
	switch ct {
	case "video/x-ivf": // BitVoice's own recordings of live video
		return true
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/heic", "image/heif", "image/avif":
		return true
	}
	return strings.HasPrefix(ct, "video/") && !strings.ContainsAny(ct, "+;")
}

// serveMedia sends one of a call's files and counts who saw it. The file came from outside: it is
// served in a sandbox, never sniffed, and as a download unless it is a picture or video.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, call string, n int, x Exposure) bool {
	if !callIDRe.MatchString(call) {
		return false
	}
	ix, _ := s.readIndex(call)
	for _, it := range ix.Items {
		if it.N != n {
			continue
		}
		x.Call, x.Item = call, strconv.Itoa(n)
		path := filepath.Join(s.mediaDir(call), strconv.Itoa(n))
		ct, disp := it.Type, "inline"
		if it.Recording && r.URL.Query().Get("view") == "1" {
			// a copy a browser plays, the same pictures in another container; the original stays
			vp, vct, err := s.viewingCopy(path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return true
			}
			path, ct = vp, vct
		}
		s.logExposure(x)
		if !mediaTypeOK(ct) {
			ct, disp = "application/octet-stream", "attachment"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'")
		w.Header().Set("Content-Disposition", disp+"; filename=\""+strings.Map(func(r rune) rune {
			if r == '"' || r == '\\' || r < 32 || r == 127 {
				return -1
			}
			return r
		}, it.Name)+"\"")
		w.Header().Set("X-Content-SHA256", it.SHA256)
		http.ServeFile(w, r, path)
		return true
	}
	return false
}

// recordCallVideo keeps a caller's live video as a file of the call: written as it comes, hashed as
// it is written, and listed with the photos and clips when it ends.
func (s *Server) recordCallVideo(call string) (io.Writer, func(frames int, err error)) {
	if s.DataDir == "" || !callIDRe.MatchString(call) {
		return nil, nil
	}
	if err := os.MkdirAll(s.mediaDir(call), 0o700); err != nil {
		s.Log.Error("911 caller video not recorded", "call", call, "err", err)
		return nil, nil
	}
	f, err := os.CreateTemp(s.mediaDir(call), "recording-*")
	if err != nil {
		s.Log.Error("911 caller video not recorded", "call", call, "err", err)
		return nil, nil
	}
	h := sha256.New()
	started := time.Now().UTC()
	return io.MultiWriter(f, h), func(frames int, werr error) {
		size, _ := f.Seek(0, io.SeekCurrent)
		cerr := f.Sync()
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		if werr == nil {
			werr = cerr
		}
		if frames == 0 || werr != nil {
			os.Remove(f.Name())
			if werr != nil {
				s.Log.Error("911 caller video recording failed", "call", call, "err", werr)
			}
			return
		}
		item := MediaItem{Name: "live video " + started.Format("15-04-05") + ".ivf", Type: "video/x-ivf", Size: size,
			SHA256: hex.EncodeToString(h.Sum(nil)), Received: time.Now().UTC(), Recording: true}
		item.Taken = started
		mediaMu.Lock()
		defer mediaMu.Unlock()
		ix, err := s.readIndex(call)
		if err == nil {
			item.N = len(ix.Items) + 1
			err = os.Rename(f.Name(), filepath.Join(s.mediaDir(call), strconv.Itoa(item.N)))
		}
		if err == nil {
			if ix.From == "" {
				for _, c := range s.Engine.Calls() {
					if c.ID == call {
						ix.From = c.From
					}
				}
			}
			ix.Call = call
			ix.Items = append(ix.Items, item)
			err = s.writeIndex(ix)
		}
		if err != nil {
			os.Remove(f.Name())
			s.Log.Error("911 caller video recording not kept", "call", call, "err", err)
			return
		}
		s.Log.Info("911 caller video recorded", "call", call, "frames", frames, "bytes", size, "sha256", item.SHA256)
		s.Bus.Publish("call.media", map[string]any{"call": call, "item": item})
	}
}

var viewMu sync.Mutex

// viewingCopy makes (once) a copy of a recording a browser plays: the pictures are copied into
// WebM (VP8) or MP4 (H.264) by ffmpeg, not converted. Without ffmpeg there is none; the original
// still downloads.
func (s *Server) viewingCopy(orig string) (string, string, error) {
	viewMu.Lock()
	defer viewMu.Unlock()
	for _, x := range []struct{ ext, ct string }{{".webm", "video/webm"}, {".mp4", "video/mp4"}} {
		if _, err := os.Stat(orig + ".view" + x.ext); err == nil {
			return orig + ".view" + x.ext, x.ct, nil
		}
	}
	ff := s.config().System.FFmpeg
	if !media.FFmpegHas(ff) {
		return "", "", errors.New("install ffmpeg to play recordings here; the original downloads as it is")
	}
	if ff == "" {
		ff = "ffmpeg"
	}
	head := make([]byte, 12)
	f, err := os.Open(orig)
	if err != nil {
		return "", "", err
	}
	io.ReadFull(f, head)
	f.Close()
	ext, ct := ".webm", "video/webm"
	if string(head[8:12]) == "H264" {
		ext, ct = ".mp4", "video/mp4"
	}
	out := orig + ".view" + ext
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "ivf", "-i", orig,
		"-c", "copy", "-movflags", "+faststart", out+".tmp"+ext).CombinedOutput()
	if err != nil {
		os.Remove(out + ".tmp" + ext)
		return "", "", fmt.Errorf("the recording could not be copied for playing: %s", strings.TrimSpace(string(b)))
	}
	return out, ct, os.Rename(out+".tmp"+ext, out)
}

// tokenGuard keeps the token links from being guessed: an address that keeps presenting tokens that
// do not exist is turned away for a while, as for failed sign-ins. A real token is never slowed.
func (s *Server) tokenGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r).String()
		if _, _, _, ok := s.Engine.VideoToken(r.PathValue("token")); ok {
			h(w, r)
			return
		}
		if s.throttled(ip) {
			http.Error(w, "too many attempts: wait a few minutes", http.StatusTooManyRequests)
			return
		}
		s.loginFailed(ip)
		h(w, r) // answers "expired" or not found as before
	}
}

// videoTextProblem says why a caller-video link cannot be texted (from: the 911 settings' texting number;
// to: the caller's number), or "" when it can: the call-taker then reads the link out instead.
func videoTextProblem(from, to string) string {
	switch {
	case from == "":
		return "no texting number is set for video links (911 settings, Caller video): read the link to the caller"
	case to == "" || to == "anonymous":
		return "the caller sent no number to text the link to: read the link to the caller"
	}
	return ""
}
