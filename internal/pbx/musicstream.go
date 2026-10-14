package pbx

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/media/mp3"
)

// musicStream plays one internet radio address to everyone on hold with its class: one connection,
// however many callers, opened when the first caller is put on hold and closed a while after the last
// is taken off.
type musicStream struct {
	url string
	mu  sync.Mutex
	// listeners each get the audio as it comes, up to a few seconds ahead.
	listeners map[int]*streamListener
	next      int
	live      bool // audio arrived recently
	lastAudio time.Time
	cancel    context.CancelFunc
	idleSince time.Time
	err       string
}

type streamListener struct {
	mu  sync.Mutex
	buf []int16
}

const (
	streamAhead = 3 * media.Rate // samples a listener may fall behind before older audio is dropped
	streamIdle  = 30 * time.Second
)

var streamClient = &http.Client{}

// StreamStatus is how a class's stream is doing, for the Sounds page.
type StreamStatus struct {
	Class     string `json:"class"`
	URL       string `json:"url"`
	Listening int    `json:"listening"`
	Live      bool   `json:"live"`
	Error     string `json:"error,omitempty"`
}

// MusicStreams reports the streams in use.
func (e *Engine) MusicStreams() []StreamStatus {
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	var out []StreamStatus
	for class, s := range e.streams {
		s.mu.Lock()
		out = append(out, StreamStatus{Class: class, URL: s.url, Listening: len(s.listeners), Live: s.live && time.Since(s.lastAudio) < 5*time.Second, Error: s.err})
		s.mu.Unlock()
	}
	return out
}

// listen joins a class's stream and returns what PlayStream asks for next, and a function to leave.
// While the stream gives no audio, fallback is asked instead (the class's tracks), then silence.
func (e *Engine) listen(class, url string, fallback func() []int16) (func() []int16, func()) {
	e.streamMu.Lock()
	if e.streams == nil {
		e.streams = map[string]*musicStream{}
	}
	s := e.streams[class]
	if s != nil && s.url != url {
		s.stop()
		s = nil
	}
	if s == nil {
		s = &musicStream{url: url, listeners: map[int]*streamListener{}}
		e.streams[class] = s
	}
	e.streamMu.Unlock()
	l := &streamListener{}
	s.mu.Lock()
	id := s.next
	s.next++
	s.listeners[id] = l
	if s.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		go e.runStream(ctx, class, s)
	}
	s.mu.Unlock()
	var fromFiles []int16
	next := func() []int16 {
		l.mu.Lock()
		if len(l.buf) > 0 {
			out := l.buf
			l.buf = nil
			l.mu.Unlock()
			fromFiles = nil
			return out
		}
		l.mu.Unlock()
		s.mu.Lock()
		live := s.live && time.Since(s.lastAudio) < 3*time.Second
		s.mu.Unlock()
		if live {
			return make([]int16, 160) // a gap in the stream: a moment of silence, not the tone
		}
		// The stream is down: the tracks play, a piece at a time so the stream takes over when back.
		if len(fromFiles) == 0 && fallback != nil {
			fromFiles = fallback()
		}
		if len(fromFiles) == 0 {
			return nil
		}
		n := min(len(fromFiles), media.Rate/2)
		out := fromFiles[:n]
		fromFiles = fromFiles[n:]
		return out
	}
	leave := func() {
		s.mu.Lock()
		delete(s.listeners, id)
		if len(s.listeners) == 0 {
			s.idleSince = time.Now()
		}
		s.mu.Unlock()
	}
	return next, leave
}

func (s *musicStream) stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
}

// runStream keeps the connection while anyone listens, reconnecting with a growing pause.
func (e *Engine) runStream(ctx context.Context, class string, s *musicStream) {
	backoff := 2 * time.Second
	for {
		err := e.readStream(ctx, s)
		s.mu.Lock()
		s.live = false
		if err != nil && ctx.Err() == nil {
			s.err = err.Error()
		}
		idle := len(s.listeners) == 0 && time.Since(s.idleSince) > streamIdle
		s.mu.Unlock()
		if ctx.Err() != nil || idle {
			e.streamMu.Lock()
			if e.streams[class] == s {
				delete(e.streams, class)
			}
			e.streamMu.Unlock()
			s.stop()
			return
		}
		if err != nil {
			e.Log.Info("hold music stream interrupted", "class", class, "err", err.Error())
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (e *Engine) readStream(ctx context.Context, s *musicStream) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "BitVoice")
	req.Header.Set("Icy-MetaData", "0") // the audio alone, without song titles mixed in
	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &streamStatus{resp.StatusCode}
	}
	// The listeners are checked every second; with nobody for a while, the connection is closed.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.mu.Lock()
			idle := len(s.listeners) == 0 && time.Since(s.idleSince) > streamIdle
			s.mu.Unlock()
			if idle {
				cancel()
				return
			}
		}
	}()
	st := mp3.NewStream(io.LimitReader(resp.Body, 1<<40))
	var rs media.Resampler
	for {
		pcm, rate, ch, err := st.Next()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		out := rs.Push(pcm, rate, ch)
		if len(out) == 0 {
			continue
		}
		s.mu.Lock()
		s.live, s.lastAudio, s.err = true, time.Now(), ""
		ls := make([]*streamListener, 0, len(s.listeners))
		for _, l := range s.listeners {
			ls = append(ls, l)
		}
		s.mu.Unlock()
		for _, l := range ls {
			l.mu.Lock()
			l.buf = append(l.buf, out...)
			if len(l.buf) > streamAhead {
				l.buf = l.buf[len(l.buf)-streamAhead:]
			}
			l.mu.Unlock()
		}
	}
}

type streamStatus struct{ code int }

func (s *streamStatus) Error() string { return "the stream answered " + http.StatusText(s.code) }
