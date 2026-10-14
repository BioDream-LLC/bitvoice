package compat

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/audiosocket"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// MediaPBX is what external media needs from the engine (the real engine has it; test fakes may not).
type MediaPBX interface {
	NewMediaEndpoint() (*media.Endpoint, error)
	AttachMedia(call string, ep *media.Endpoint, mode string) (func(), error)
}

// external is an ARI external media channel: an endpoint sending a call's audio to a program, by RTP
// or AudioSocket, until it is added to a bridge (a BitVoice call) or hung up.
type external struct {
	id, name string
	ep       *media.Endpoint
	pipe     *audiosocket.Pipe
	detach   func()
	created  time.Time
	vars     map[string]any
	format   string
}

func (x *external) close() {
	if x.detach != nil {
		x.detach()
	}
	if x.pipe != nil {
		x.pipe.Close()
	}
	x.ep.Close()
}

type externals struct {
	mu sync.Mutex
	m  map[string]*external
}

func (a *ARI) ext() *externals {
	a.extOnce.Do(func() { a.exts = &externals{m: map[string]*external{}} })
	return a.exts
}

func (x *external) ari() ariChannel {
	return ariChannel{ID: x.id, Name: x.name, State: "Up", Dialplan: ariDialplan{Context: "default", Exten: "s", Priority: 1},
		CreationTime: x.created.Format("2006-01-02T15:04:05.000-0700"), Language: "en", ChannelVars: x.vars}
}

// externalMedia answers POST /ari/channels/externalMedia: encapsulation rtp (format ulaw or alaw,
// over UDP) or audiosocket (format slin, over TCP).
func (a *ARI) externalMedia(w http.ResponseWriter, r *http.Request, u *config.CompatUser) {
	mp, ok := a.PBX.(MediaPBX)
	if !ok {
		ariError(w, http.StatusNotImplemented, "external media is not available here")
		return
	}
	q := r.URL.Query()
	host := q.Get("external_host")
	if h, p, err := net.SplitHostPort(host); err != nil || h == "" || p == "" {
		ariError(w, http.StatusBadRequest, "external_host is required, as host:port")
		return
	}
	enc := strings.ToLower(q.Get("encapsulation"))
	if enc == "" {
		enc = "rtp"
	}
	format := strings.ToLower(q.Get("format"))
	id := q.Get("channelId")
	if id == "" {
		id = config.NewID() + config.NewID()
	}
	ep, err := mp.NewMediaEndpoint()
	if err != nil {
		ariError(w, http.StatusServiceUnavailable, "no media ports free")
		return
	}
	x := &external{id: id, ep: ep, created: time.Now(), format: format}
	switch enc {
	case "rtp":
		if t := strings.ToLower(q.Get("transport")); t != "" && t != "udp" {
			ep.Close()
			ariError(w, http.StatusBadRequest, "RTP external media goes over UDP")
			return
		}
		pt := 0
		switch format {
		case "ulaw", "":
			x.format = "ulaw"
		case "alaw":
			pt = 8
		default:
			ep.Close()
			ariError(w, http.StatusBadRequest, "for RTP, BitVoice sends format ulaw or alaw")
			return
		}
		h, p, _ := net.SplitHostPort(host)
		port, _ := strconv.Atoi(p)
		ep.SetPayloadType(pt)
		ep.SetRemote(h, port)
		ep.Start()
		local := "127.0.0.1"
		if c, err := net.Dial("udp", host); err == nil {
			local = c.LocalAddr().(*net.UDPAddr).IP.String()
			c.Close()
		}
		x.name = "UnicastRTP/" + host + "-" + id[:8]
		x.vars = map[string]any{"UNICASTRTP_LOCAL_ADDRESS": local, "UNICASTRTP_LOCAL_PORT": strconv.Itoa(ep.Port())}
	case "audiosocket":
		if t := strings.ToLower(q.Get("transport")); t != "" && t != "tcp" {
			ep.Close()
			ariError(w, http.StatusBadRequest, "AudioSocket goes over TCP")
			return
		}
		if format != "" && format != "slin" {
			ep.Close()
			ariError(w, http.StatusBadRequest, "AudioSocket carries format slin (8 kHz)")
			return
		}
		x.format = "slin"
		uuid := q.Get("data")
		if uuid == "" {
			uuid = id
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		pipe, err := audiosocket.Dial(ctx, host, strings.ReplaceAll(uuid, "-", ""), ep)
		cancel()
		if err != nil {
			ep.Close()
			ariError(w, http.StatusBadGateway, "could not reach "+host+": "+err.Error())
			return
		}
		ep.Start()
		x.pipe = pipe
		x.name = "AudioSocket/" + host + "-" + id[:8]
		go func() {
			<-pipe.Done()
			a.dropExternal(id)
		}()
	default:
		ep.Close()
		ariError(w, http.StatusBadRequest, "encapsulation is rtp or audiosocket")
		return
	}
	es := a.ext()
	es.mu.Lock()
	if old := es.m[id]; old != nil {
		es.mu.Unlock()
		x.close()
		ariError(w, http.StatusConflict, "a channel with that id exists")
		return
	}
	es.m[id] = x
	es.mu.Unlock()
	// One not added to a bridge within a minute is closed, so nothing is left streaming by mistake.
	time.AfterFunc(time.Minute, func() {
		es.mu.Lock()
		idle := es.m[id] == x && x.detach == nil
		es.mu.Unlock()
		if idle {
			a.dropExternal(id)
		}
	})
	a.audit(u, "ari.externalMedia", x.name)
	ariJSON(w, x.ari())
}

func (a *ARI) dropExternal(id string) {
	es := a.ext()
	es.mu.Lock()
	x := es.m[id]
	delete(es.m, id)
	es.mu.Unlock()
	if x != nil {
		x.close()
	}
}

func (a *ARI) findExternal(id string) *external {
	es := a.ext()
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.m[id]
}

// addChannel answers POST /ari/bridges/{call}/addChannel?channel=…: an external media channel joins the
// call, hearing both parties; role=listener keeps it unheard (for transcription), anything else lets
// both parties hear it.
func (a *ARI) addChannel(w http.ResponseWriter, r *http.Request, u *config.CompatUser, call string) {
	mp, ok := a.PBX.(MediaPBX)
	if !ok {
		ariError(w, http.StatusNotImplemented, "external media is not available here")
		return
	}
	q := r.URL.Query()
	var x *external
	for _, id := range strings.Split(q.Get("channel"), ",") {
		if x = a.findExternal(strings.TrimSpace(id)); x != nil {
			break
		}
	}
	if x == nil {
		ariError(w, http.StatusBadRequest, "channel must name an external media channel (POST /ari/channels/externalMedia); calls' own channels are in their bridge already")
		return
	}
	if x.detach != nil {
		ariError(w, http.StatusConflict, "that channel is in a bridge already")
		return
	}
	mode := "barge"
	if strings.EqualFold(q.Get("role"), "listener") || strings.EqualFold(q.Get("role"), "listen") {
		mode = "listen"
	}
	detach, err := mp.AttachMedia(call, x.ep, mode)
	if err != nil {
		ariError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.ext().mu.Lock()
	x.detach = detach
	a.ext().mu.Unlock()
	a.audit(u, "ari.addChannel", x.name+" to "+call+" ("+mode+")")
	w.WriteHeader(http.StatusNoContent)
}

// removeChannel answers POST /ari/bridges/{call}/removeChannel?channel=…
func (a *ARI) removeChannel(w http.ResponseWriter, r *http.Request, u *config.CompatUser) {
	x := a.findExternal(r.URL.Query().Get("channel"))
	if x == nil || x.detach == nil {
		ariError(w, http.StatusUnprocessableEntity, "that channel is not in a bridge")
		return
	}
	x.detach()
	a.ext().mu.Lock()
	x.detach = nil
	a.ext().mu.Unlock()
	a.audit(u, "ari.removeChannel", x.name)
	w.WriteHeader(http.StatusNoContent)
}
