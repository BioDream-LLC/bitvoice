package pbx

import (
	"sync"
	"testing"
	"time"
)

type fakeApps struct {
	mu    sync.Mutex
	exts  map[string]bool
	woken []string
	wake  chan struct{}
}

func (f *fakeApps) HasDevices(ext string) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.exts[ext] }
func (f *fakeApps) Wake(ext string, c CallInfo) {
	f.mu.Lock()
	f.woken = append(f.woken, ext+" "+c.From)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// An extension whose only phone is the app: the call rings, the app is woken, connects, and is rung.
func TestMobileAppIsWokenAndRung(t *testing.T) {
	apps := &fakeApps{exts: map[string]bool{"101": true}, wake: make(chan struct{}, 1)}
	e, rec := startEngine(t, testConfig(), func(e *Engine) { e.Mobile = apps })
	a := registered(t, e, "100")[0]
	done := make(chan int, 1)
	go func() { _, code := a.dial("101"); done <- code }()
	select {
	case <-apps.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("the app was never woken")
	}
	if apps.woken[0] != "101 100" {
		t.Fatalf("woken %v", apps.woken)
	}
	// The app connects a moment later, as an app does once woken.
	time.Sleep(300 * time.Millisecond)
	app := registered(t, e, "101")[0]
	ring := app.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the app was not rung after connecting")
	}
	d := app.answer(ring)
	if code := <-done; code != 200 {
		t.Fatalf("caller got %d", code)
	}
	app.send(d.request("BYE"))
	if r := rec.last(t); r.Disposition != "answered" || r.To != "101" {
		t.Fatalf("CDR %+v", r)
	}
}

// Without apps, an extension with no phone is still unavailable at once.
func TestNoAppsNoWait(t *testing.T) {
	e, _ := startEngine(t, testConfig(), func(e *Engine) { e.Mobile = &fakeApps{exts: map[string]bool{}} })
	a := registered(t, e, "100")[0]
	if _, code := a.dial("101"); code != 480 {
		t.Fatalf("got %d", code)
	}
}
