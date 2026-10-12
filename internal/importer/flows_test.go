package importer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func tarOf(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for name, body := range files {
		w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		w.Write([]byte(body))
	}
	w.Close()
	return b.Bytes()
}

func groupNamed(r Result, num string) (rgFound bool, members string, strategy string) {
	for _, g := range r.RingGroups {
		if g.Number == num {
			return true, strings.Join(g.Members, ","), g.Strategy
		}
	}
	return false, "", ""
}

// Cisco UCM: a BAT export (.tar) with users, line groups, hunt lists, hunt pilots, SIP trunks and
// route patterns; a hunt pilot's members come through its hunt list's line groups.
func TestCiscoFlows(t *testing.T) {
	data := tarOf(t, map[string]string{
		"users.csv": "USER ID,FIRST NAME,LAST NAME,TELEPHONE NUMBER\njdoe,Jane,Doe,1001\nbsmith,Bob,Smith,1002\nclee,Cam,Lee,1003\n",
		"Line Group.csv": "LINE GROUP NAME,RNA REVERSION TIMEOUT,DISTRIBUTION ALGORITHM,DIRECTORY NUMBER 1,PARTITION 1,DIRECTORY NUMBER 2,PARTITION 2\n" +
			"Sales_LG,12,Top Down,1001,Internal_PT,1002,Internal_PT\nSupport_LG,10,Broadcast,1003,Internal_PT,,\n",
		"Hunt List.csv":  "HUNT LIST NAME,DESCRIPTION,LINE GROUP 1,LINE GROUP 2\nSales_HL,Sales,Sales_LG,\nSupport_HL,Support,Support_LG,Sales_LG\n",
		"Hunt Pilot.csv": "HUNT PILOT,ROUTE PARTITION,DESCRIPTION,HUNT LIST,ALERTING NAME\n5000,Internal_PT,Sales line,Sales_HL,Sales\n5001,Internal_PT,Support line,Support_HL,Support\n5002,,Broken,Missing_HL,Broken\n",
		"SIP Trunk.csv":  "DEVICE NAME,DESCRIPTION,DESTINATION ADDRESS 1,DESTINATION PORT 1\nITSP_Trunk,Carrier,sip.carrier.example,5060\n",
		"Route Pattern.csv": "ROUTE PATTERN,ROUTE PARTITION,GATEWAY OR ROUTE LIST,DISCARD DIGITS,PREFIX DIGITS (OUTGOING CALLS)\n" +
			"9.[2-9]XXXXXX,PSTN_PT,ITSP_Trunk,PreDot,\n9.1[2-9]XX[2-9]XXXXXX,PSTN_PT,ITSP_Trunk,PreDot,\n9.011!,PSTN_PT,ITSP_Trunk,PreDot,\n9.@,PSTN_PT,ITSP_Trunk,PreDot,\n",
	})
	r, err := ParseFiles([]File{{Name: "export.tar", Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Extensions) != 3 {
		t.Fatalf("extensions %d", len(r.Extensions))
	}
	if ok, m, s := groupNamed(r, "5000"); !ok || m != "1001,1002" || s != "sequential" {
		t.Fatalf("5000: %v %q %q", ok, m, s)
	}
	if ok, m, s := groupNamed(r, "5001"); !ok || m != "1003,1001,1002" || s != "all" {
		t.Fatalf("5001: %v %q %q", ok, m, s)
	}
	if ok, _, _ := groupNamed(r, "5002"); ok {
		t.Fatal("a pilot with a missing hunt list came in")
	}
	if len(r.Trunks) != 1 || r.Trunks[0].Host != "sip.carrier.example" || r.Trunks[0].Port != 0 || r.Trunks[0].Enabled {
		t.Fatalf("trunks %+v", r.Trunks)
	}
	want := map[string]int{"9[2-9]XXXXXX": 1, "91[2-9]XX[2-9]XXXXXX": 1, "9011.": 1}
	if len(r.OutboundRoutes) != 3 {
		t.Fatalf("routes %+v", r.OutboundRoutes)
	}
	for _, o := range r.OutboundRoutes {
		if want[o.Patterns[0]] != o.Strip || o.Trunks[0] != "ITSP_Trunk" {
			t.Fatalf("route %+v", o)
		}
	}
	all := strings.Join(r.Warnings, "\n")
	for _, w := range []string{"Missing_HL", "@ (the national numbering plan)", "rings 2 line groups"} {
		if !strings.Contains(all, w) {
			t.Errorf("no warning about %q in:\n%s", w, all)
		}
	}
}

// Avaya IP Office: the users export and the hunt group export, which has no header row and names
// members by user name.
func TestIPOfficeHuntGroups(t *testing.T) {
	users := File{Name: "user.csv", Data: []byte("Full Name,Extension,Name\nJane Doe,201,Jane\nBob Smith,202,Bob\n")}
	hg := File{Name: "huntgroup.csv", Data: []byte("Main,200,Jane Doe|Bob Smith,Group,1,0,1\nSales,300,Bob Smith|Nobody,Rotary,1,0,0\n")}
	r, err := ParseFiles([]File{users, hg})
	if err != nil {
		t.Fatal(err)
	}
	if ok, m, s := groupNamed(r, "200"); !ok || m != "201,202" || s != "all" {
		t.Fatalf("200: %v %q %q (%v)", ok, m, s, r.Warnings)
	}
	if ok, m, s := groupNamed(r, "300"); !ok || m != "202" || s != "sequential" {
		t.Fatalf("300: %v %q %q", ok, m, s)
	}
	all := strings.Join(r.Warnings, "\n")
	if !strings.Contains(all, `"Nobody"`) || !strings.Contains(all, "round robin") {
		t.Fatalf("warnings: %s", all)
	}
}

// Avaya Communication Manager hunt groups (list hunt-group, with a member list export) and
// Mitel MiVoice Business ring groups with their assignment form, in a zip.
func TestCMAndMitelFlows(t *testing.T) {
	cm := File{Name: "hunt-groups.csv", Data: []byte("Group Number,Group Name,Group Extension,Group Type,Members\n1,Billing,3100,ucd-mia,3001 3002\n2,Front desk,3200,ddc,3003\n")}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("mitel/Ring Groups.csv")
	f.Write([]byte("Ring Group Directory Number,Name,Ring Group Type,Cascade Timer\n4100,Warehouse,Cascade,12\n4200,Office,Ring All,\n"))
	f, _ = zw.Create("mitel/Ring Group Assignment.csv")
	f.Write([]byte("Ring Group Directory Number,Member Directory Number\n4100,4001\n4100,4002\n4200,4003\n"))
	f, _ = zw.Create("mitel/readme.txt")
	f.Write([]byte("ignored"))
	zw.Close()
	r, err := ParseFiles([]File{cm, {Name: "mitel.zip", Data: zb.Bytes()}})
	if err != nil {
		t.Fatal(err)
	}
	for num, want := range map[string]string{"3100": "3001,3002|sequential", "3200": "3003|sequential", "4100": "4001,4002|sequential", "4200": "4003|all"} {
		ok, m, s := groupNamed(r, num)
		if !ok || m+"|"+s != want {
			t.Errorf("%s: %v %q, want %q", num, ok, m+"|"+s, want)
		}
	}
	for _, g := range r.RingGroups {
		if g.Number == "4100" && g.RingSeconds != 12 {
			t.Errorf("4100 rings %d", g.RingSeconds)
		}
	}
}

func TestCiscoPattern(t *testing.T) {
	for in, want := range map[string]string{"9.XXXX": "9XXXX/1", "9.1[2-9]XX!": "91[2-9]XX./1", "[2-9]XXX": "[2-9]XXX/0", "9.011!#": "9011./1"} {
		p, s, why := ciscoPattern(in, "PreDot")
		got := p + "/" + string(rune('0'+s))
		if why != "" || got != want {
			t.Errorf("%s: %s (%s), want %s", in, got, why, want)
		}
	}
	for _, bad := range []string{"9.@", "9.1X?", "+1!"} {
		if _, _, why := ciscoPattern(bad, ""); why == "" {
			t.Errorf("%s accepted", bad)
		}
	}
}
