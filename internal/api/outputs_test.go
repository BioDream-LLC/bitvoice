package api

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/snmp"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func freeUDPPort(t *testing.T) int {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func TestSNMPAndCDROutputs(t *testing.T) {
	s, ts := newServer(t)
	s.Log = slog.New(slog.DiscardHandler)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunSNMP(ctx)
	go s.RunCDROutputs(ctx)

	udp, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer udp.Close()
	tcp, _ := net.Listen("tcp", "127.0.0.1:0")
	defer tcp.Close()
	traps, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer traps.Close()
	port := freeUDPPort(t)

	_, cfg := admin.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	sys["snmp"] = map[string]any{"enabled": true, "listen": "127.0.0.1:" + strconv.Itoa(port), "community": "public", "allowFrom": []string{}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 422 || !hasProblem(out, "system.snmp.community") || !hasProblem(out, "system.snmp.allowFrom") {
		t.Fatalf("weak SNMP %d %v", code, out)
	}
	sys["snmp"] = map[string]any{"enabled": true, "listen": "127.0.0.1:" + strconv.Itoa(port), "community": "watchtower", "allowFrom": []string{"127.0.0.1"},
		"contact": "noc@example.com", "traps": []map[string]any{{"host": traps.LocalAddr().String(), "community": "trapsecret"}}}
	sys["cdrOutputs"] = map[string]any{"csv": true, "syslog": map[string]any{"host": "127.0.0.1", "port": udp.LocalAddr().(*net.UDPAddr).Port, "transport": "udp", "facility": 17}}
	if code, out := admin.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("settings %d %v", code, out)
	}

	// SNMP: the agent answers a walk of the system group and BitVoice's values.
	deadline := time.Now().Add(15 * time.Second)
	var got map[string]string
	for time.Now().Before(deadline) {
		if got = snmpWalk(t, "127.0.0.1:"+strconv.Itoa(port), "watchtower", "1.3.6.1.2.1.1"); len(got) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.HasPrefix(got["1.3.6.1.2.1.1.1.0"], "BitVoice") || got["1.3.6.1.2.1.1.4.0"] != "noc@example.com" {
		t.Fatalf("system group %v", got)
	}
	if v := snmpWalk(t, "127.0.0.1:"+strconv.Itoa(port), "watchtower", "1.3.6.1.4.1.8072.9999.9999.7.1"); v["1.3.6.1.4.1.8072.9999.9999.7.1.1.0"] != "test" {
		t.Fatalf("version scalar %v", v)
	}
	if v := snmpWalk(t, "127.0.0.1:"+strconv.Itoa(port), "guess", "1.3.6.1.2.1.1"); len(v) != 0 {
		t.Fatal("answered a wrong community")
	}
	// A trap for an emergency call.
	s.Bus.Publish("emergency.call", map[string]any{"number": "911", "extension": "100"})
	// Other traps (the agent starting, say) may come first on a slow machine: read until this one.
	buf := make([]byte, 2048)
	_ = traps.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		n, _, err := traps.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("no trap for the emergency call: %v", err)
		}
		if strings.Contains(string(buf[:n]), "911") {
			if !strings.Contains(string(buf[:n]), "trapsecret") {
				t.Fatalf("trap without its community %q", buf[:n])
			}
			break
		}
	}
	if code, b := rawGet(t, admin, ts.URL+"/api/snmp/mib"); code != 200 || !strings.Contains(b, "bvTrunkDown") {
		t.Fatal("MIB")
	}

	// Call records: syslog (UDP, then TCP with octet counting) and the day's CSV file.
	rec := store.CDR{ID: "c-1", Start: time.Now().UTC(), End: time.Now().UTC(), Direction: "outbound", From: "100", FromName: "Front",
		To: "=cmd|'/c calc'!A1", Disposition: "answered", Duration: 61, Billsec: 60, Ext: "100"}
	s.Bus.Publish("call.logged", rec)
	_ = udp.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, _, err := udp.ReadFromUDP(buf)
	if err != nil || !strings.HasPrefix(string(buf[:n]), "<142>1 ") || !strings.Contains(string(buf[:n]), `"id":"c-1"`) {
		t.Fatalf("syslog %q %v", buf[:n], err)
	}
	var code int
	var b string
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if code, b = rawGet(t, admin, ts.URL+"/api/cdr/csv/"+rec.Start.Format(time.DateOnly)); strings.Contains(b, "ANSWERED") {
			break
		}
	}
	if code != 200 ||
		!strings.HasPrefix(b, "accountcode,src,dst") || !strings.Contains(b, "ANSWERED") || !strings.Contains(b, "'=cmd") {
		t.Fatalf("csv %d %s", code, b)
	}
	sys["cdrOutputs"] = map[string]any{"syslog": map[string]any{"host": "127.0.0.1", "port": tcp.Addr().(*net.TCPAddr).Port, "transport": "tcp"}}
	_, cfg2 := admin.do("GET", "/api/settings", nil)
	cfg2["system"].(map[string]any)["cdrOutputs"] = sys["cdrOutputs"]
	if code, out := admin.do("PUT", "/api/settings", cfg2); code != 200 {
		t.Fatalf("tcp syslog %v", out)
	}
	// The output connects once the settings are in: publish until it has (a slow machine takes longer).
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
			}
			s.Bus.Publish("call.logged", store.CDR{ID: "c-2", Start: time.Now(), End: time.Now(), Direction: "inbound", Disposition: "busy"})
		}
	}()
	c, err := tcp.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, _ := bufio.NewReader(c).ReadString('}')
	if sp := strings.IndexByte(line, ' '); sp < 0 || !strings.Contains(line, "<134>1 ") || !strings.Contains(line, `"id":"c-2"`) {
		t.Fatalf("tcp syslog %q", line)
	}

	// An external PostgreSQL table, when a test database is given.
	pgURL := os.Getenv("BITVOICE_TEST_POSTGRES")
	if pgURL == "" {
		return
	}
	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	defer pcancel()
	table := "cdr_test_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
	var o cdrOut
	defer o.close()
	if err := o.toPostgres(pctx, config.CDRPostgres{URL: pgURL, Table: table}, rec); err != nil {
		t.Fatal(err)
	}
	_ = o.toPostgres(pctx, config.CDRPostgres{URL: pgURL, Table: table}, rec) // the same call again: kept once
	db, _ := sql.Open("pgx", pgURL)
	defer db.Close()
	defer db.Exec("DROP TABLE " + table)
	var count int
	var disp string
	if err := db.QueryRowContext(pctx, "SELECT count(*), max(disposition) FROM "+table).Scan(&count, &disp); err != nil || count != 1 || disp != "ANSWERED" {
		t.Fatalf("postgres %d %s %v", count, disp, err)
	}
}

func rawGet(t *testing.T, c *client, url string) (int, string) {
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// snmpWalk walks a subtree with GETNEXT, as snmpwalk does, returning names and printable values.
func snmpWalk(t *testing.T, addr, community, root string) map[string]string {
	out := map[string]string{}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	at := snmp.MustOID(root)
	for range 50 {
		_, _ = c.Write(snmp.GetNextRequest(community, at))
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 4096)
		n, err := c.Read(buf)
		if err != nil {
			return out
		}
		vb, ok := snmp.FirstVarBind(buf[:n])
		if !ok || !strings.HasPrefix(vb.Name.String()+".", root+".") {
			return out
		}
		out[vb.Name.String()] = vb.Value.Text()
		at = vb.Name
	}
	return out
}
