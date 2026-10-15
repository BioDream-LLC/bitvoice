package pbx

import (
	"strings"
	"testing"
	"time"
)

// A video link is for 911 calls; the link's token sends and the viewing token watches; the live
// cap protects the calls; a call that ends refuses new live video but keeps the link a while.
func TestCallVideoLinks(t *testing.T) {
	s := placeSOS(t, "")
	v, err := s.e.VideoLink(s.id)
	if err != nil || len(v.Token) < 20 || len(v.ViewToken) < 20 || v.Token == v.ViewToken || v.Live {
		t.Fatalf("%+v %v", v, err)
	}
	again, _ := s.e.VideoLink(s.id)
	if again.Token != v.Token {
		t.Fatal("a second link was made for the same call")
	}
	if call, caller, up, ok := s.e.VideoToken(v.Token); !ok || !caller || !up || call != s.id {
		t.Fatalf("caller token %v %v %v %v", call, caller, up, ok)
	}
	if _, caller, _, ok := s.e.VideoToken(v.ViewToken); !ok || caller {
		t.Fatal("the viewing token can send")
	}
	if _, _, _, ok := s.e.VideoToken("not-a-token"); ok {
		t.Fatal("a made-up token works")
	}
	if _, _, err := s.e.WHIP(v.ViewToken, []byte("v=0"), "127.0.0.1"); err == nil {
		t.Fatal("the viewing token sent video")
	}
	if _, _, err := s.e.WHIP(v.Token, []byte("not sdp"), "127.0.0.1"); err == nil {
		t.Fatal("took a broken offer")
	}
	if _, _, err := s.e.WHEP(s.id, true, []byte("v=0"), "127.0.0.1", "Pat"); err == nil || !strings.Contains(err.Error(), "not sharing") {
		t.Fatalf("watching with nothing live: %v", err)
	}
	// The cap: with it at 0 live sources allowed... the smallest cap is 1, so fill it with a fake source.
	cfg := s.e.Config()
	c2 := *cfg
	c2.System.PSAP.VideoStreams = 1
	s.e.Apply(c2)
	s.e.video.mu.Lock()
	other := &callVideo{call: &Call{ID: "x"}, viewers: map[string]*videoViewer{}, src: &videoSource{}}
	s.e.video.byCall["x"] = other
	s.e.video.mu.Unlock()
	if _, _, err := s.e.WHIP(v.Token, []byte("v=0\r\n"), "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "limit protects the calls") {
		t.Fatalf("over the cap: %v", err)
	}
	s.e.video.mu.Lock()
	delete(s.e.video.byCall, "x")
	s.e.video.mu.Unlock()
}

// A call that is not a 911 call gets no video link.
func TestNoVideoOnOrdinaryCalls(t *testing.T) {
	s := placeCall(t, "", false)
	if _, err := s.e.VideoLink(s.id); err != ErrNotVideoCall {
		t.Fatalf("%v", err)
	}
}

// A source that stops sending is ended, and the call's screen says so.
func TestVideoSourceGoesQuiet(t *testing.T) {
	old := videoSilence
	videoSilence = 300 * time.Millisecond
	defer func() { videoSilence = old }()
	s := placeSOS(t, "")
	v, _ := s.e.VideoLink(s.id)
	s.e.video.mu.Lock()
	cv := s.e.video.byTok[v.Token]
	src := &videoSource{since: time.Now()}
	src.last.Store(time.Now().UnixMilli())
	cv.src = src
	s.e.video.mu.Unlock()
	go s.e.watchSource(cv, src)
	waitFor(t, "the quiet source was not ended", func() bool { x := s.e.VideoFor(s.id); return x != nil && !x.Live })
}
