package api

import (
	"bufio"
	"context"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/serial"
)

var testLayout = []config.ALIField{{Name: "callback", Line: 1, Col: 5, Len: 14}, {Name: "class", Line: 2, Col: 1, Len: 4}, {Name: "name", Line: 3, Col: 1},
	{Name: "street", Line: 4, Col: 1}, {Name: "city", Line: 5, Col: 1, Len: 20}, {Name: "state", Line: 5, Col: 21, Len: 2}}

const testRecord = "\x02001 (717) 555-0100\r\nRESD\r\nJOHNSON PAT\r\n123 N MARKET ST\r\nHARRISBURG          PA\r\n\x03"

// aliController answers each request (STX, ten digits, ETX) with a record for that number.
func aliController(t *testing.T, rw interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}, got chan<- string) {
	r := bufio.NewReader(rw)
	for {
		req, err := r.ReadString(0x03)
		if err != nil {
			return
		}
		got <- req
		if strings.Contains(req, "7175550100") {
			rw.Write([]byte(testRecord))
		} else {
			rw.Write([]byte("\x02NO RECORD FOUND\r\n\x03"))
		}
	}
}

func checkRecord(t *testing.T, rec i3.ALIRecord, err error) {
	t.Helper()
	if err != nil || rec.Name != "JOHNSON PAT" || rec.Class != "RESD" || rec.Location.Civic.Street != "MARKET" || rec.Location.Civic.City != "HARRISBURG" {
		t.Fatalf("%+v %v", rec, err)
	}
}

// The ALI link over TCP and over a serial port (a pseudo-terminal standing in for the cable), and
// the spill to CAD out of a serial port.
func TestALILink(t *testing.T) {
	s, _ := newServer(t)
	set := func(l config.ALILink, spill string) {
		_, rev := s.Store.Config()
		if _, _, err := s.Store.Update("test", "ali", rev, func(c *config.Config) error { c.System.PSAP.ALILink, c.System.PSAP.ALIAddress = l, spill; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reqs := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go aliController(t, c, reqs)
		}
	}()
	set(config.ALILink{Address: ln.Addr().String(), Request: `\x02{pos2}{ani10}\x03`, Layout: testLayout}, "")
	rec, err := s.QueryALI(context.Background(), "+17175550100", "3")
	checkRecord(t, rec, err)
	if r := <-reqs; r != "\x0203"+"7175550100\x03" {
		t.Fatalf("request %q", r)
	}
	if _, err := s.QueryALI(context.Background(), "+17175559999", "3"); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Fatalf("an unknown number: %v", err)
	}
	<-reqs

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return
	}
	m, dev, err := serial.PTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	go aliController(t, m, reqs)
	set(config.ALILink{Address: "serial:" + dev + "?baud=9600", Layout: testLayout, TimeoutS: 2}, "")
	rec, err = s.QueryALI(context.Background(), "7175550100", "")
	checkRecord(t, rec, err)
	if r := <-reqs; r != "\x027175550100\x03" {
		t.Fatalf("serial request %q", r)
	}

	// The spill to CAD, out of another serial port.
	cad, cdev, err := serial.PTY()
	if err != nil {
		t.Fatal(err)
	}
	defer cad.Close()
	// Hold the port's own end open as a cable would: a pseudo-terminal whose end nobody has open
	// fails reads at once (EIO), which a real serial port never does.
	hold, err := os.OpenFile(cdev, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	got := make(chan string, 1)
	go func() { b, _ := bufio.NewReader(cad).ReadString(0x03); got <- b }()
	lat, lon := 40.26, -76.88
	if err := sendALI("serial:"+cdev+"?baud=19200", i3.ALI{Position: "101", Callback: "+17175550100", Time: time.Now(), Location: &i3.Location{Lat: &lat, Lon: &lon}}); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if !strings.HasPrefix(b, "\x02POS 101") || !strings.Contains(b, "(717) 555-0100") {
			t.Fatalf("spill %q", b)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no spill on the serial port")
	}
}
