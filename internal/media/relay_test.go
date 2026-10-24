package media

import "testing"

// A changed range applies to the next endpoint handed out.
func TestPoolSetRange(t *testing.T) {
	p := &Pool{Host: "127.0.0.1", Min: 35400, Max: 35420}
	a, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p.SetRange(35500, 35520)
	b, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.Port() < 35400 || a.Port() >= 35420 || b.Port() < 35500 || b.Port() >= 35520 {
		t.Fatalf("ports %d then %d", a.Port(), b.Port())
	}
}
