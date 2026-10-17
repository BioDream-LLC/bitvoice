package importer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// A FreePBX 17 backup as Backup & Restore writes one: module JSON as BackupBase::addConfigs leaves it
// (rows as PDO::FETCH_ASSOC gives them, numbers as JSON numbers where MySQL returns them), the
// Devices.sql mysqldump Core asks for, and voicemail.conf. The rows are invented; the shapes, table
// and column names are FreePBX's (core, ringgroups, queues and voicemail release/17.0).
const coreJSON = `{
    "module": "core",
    "version": "17.0.19.15",
    "configs": {
        "Users": {
            "users": [
                {"extension": "100", "password": "", "name": "Front Desk", "voicemail": "default", "ringtimer": 0,
                 "noanswer": "", "recording": "", "outboundcid": "\"Front Desk\" <3125550100>", "sipname": "", "mohclass": "default"},
                {"extension": "101", "password": "", "name": "Dana Ortiz", "voicemail": "default", "ringtimer": 25,
                 "noanswer": "", "recording": "", "outboundcid": "", "sipname": "", "mohclass": "default"},
                {"extension": "102", "password": "", "name": "Sam Lee", "voicemail": "novm", "ringtimer": 0,
                 "noanswer": "", "recording": "", "outboundcid": "", "sipname": "", "mohclass": "default"},
                {"extension": "*55", "password": "", "name": "Bad", "voicemail": "novm", "ringtimer": 0}
            ],
            "astdbConfigs": {"100": {"cidname": "Front Desk"}}
        },
        "Devices": [],
        "Trunks": {
            "trunks": [
                {"trunkid": 1, "tech": "pjsip", "channelid": "carrier", "name": "Carrier", "outcid": "3125550100",
                 "keepcid": "off", "maxchans": "4", "failscript": "", "dialoutprefix": "", "usercontext": "",
                 "provider": "", "disabled": "off", "continue": "off"},
                {"trunkid": 2, "tech": "sip", "channelid": "oldcarrier", "name": "Old carrier", "outcid": "",
                 "keepcid": "off", "maxchans": "", "disabled": "on", "continue": "off"},
                {"trunkid": 3, "tech": "custom", "channelid": "Local/$OUTNUM$@x", "name": "Custom", "disabled": "off"}
            ],
            "techTables": {
                "pjsip": [
                    {"id": "1", "keyword": "sip_server", "data": "sip.carrier.example", "flags": 0},
                    {"id": "1", "keyword": "sip_server_port", "data": "5060", "flags": 0},
                    {"id": "1", "keyword": "username", "data": "acct42", "flags": 0},
                    {"id": "1", "keyword": "secret", "data": "carrier-secret-1", "flags": 0},
                    {"id": "1", "keyword": "registration", "data": "send", "flags": 0},
                    {"id": "1", "keyword": "transport", "data": "0.0.0.0-udp", "flags": 0},
                    {"id": "1", "keyword": "codecs", "data": "ulaw,alaw,g722", "flags": 0},
                    {"id": "1", "keyword": "from_domain", "data": "carrier.example", "flags": 0}
                ]
            },
            "dialpatterns": []
        },
        "Routing": [
            {"route_id": 1, "name": "National", "outcid": "", "outcid_mode": "", "password": "", "emergency_route": "",
             "intracompany_route": "", "mohclass": "default", "time_group_id": null, "dest": "",
             "patterns": [
                {"route_id": 1, "match_pattern_prefix": "9", "match_pattern_pass": "1NXXNXXXXXX", "match_cid": "", "prepend_digits": ""},
                {"route_id": 1, "match_pattern_prefix": "9", "match_pattern_pass": "NXXNXXXXXX", "match_cid": "", "prepend_digits": "1"}
             ],
             "trunks": ["1", "2"], "notification": []},
            {"route_id": 2, "name": "Emergency", "password": "", "emergency_route": "YES",
             "patterns": [{"route_id": 2, "match_pattern_prefix": "", "match_pattern_pass": "911", "match_cid": "", "prepend_digits": ""}],
             "trunks": {"0": "1"}, "notification": []}
        ],
        "Did": [
            {"cidnum": "", "extension": "3125550100", "destination": "ext-group,600,1", "privacyman": 0, "description": "Main number"},
            {"cidnum": "", "extension": "3125550101", "destination": "from-did-direct,101,1", "description": ""},
            {"cidnum": "", "extension": "3125550102", "destination": "ext-queues,400,1", "description": "Support"},
            {"cidnum": "", "extension": "3125550103", "destination": "ivr-1,s,1", "description": "Menu"},
            {"cidnum": "", "extension": "", "destination": "app-blackhole,hangup,1", "description": ""}
        ],
        "features": {},
        "settings": {"AMPMGRUSER": "admin"}
    },
    "files": [{"type": "sql", "filename": "Devices.sql", "pathto": "/tmp/Devices_dump", "base": ""}],
    "dirs": [],
    "dependencies": []
}`

const ringgroupsJSON = `{"module": "ringgroups", "version": "17.0.3", "configs": {
    "groups": [
        {"grpnum": "600", "strategy": "ringall", "grptime": "20", "grppre": "", "grplist": "100-101-3125559999#",
         "annmsg_id": null, "postdest": "ext-local,vmu100,1", "description": "Reception", "alertinfo": ""},
        {"grpnum": "601", "strategy": "hunt", "grptime": "15", "grplist": "101-102", "postdest": "app-blackhole,hangup,1", "description": "Sales"}
    ],
    "settings": {}}}`

const queuesJSON = `{"module": "queues", "version": "17.0.1", "configs": {
    "settings": {}, "features": {}, "kvstore": {},
    "tables": {
        "queues_config": [
            {"extension": "400", "descr": "Support", "grppre": "", "alertinfo": "", "ringing": 0, "maxwait": "300",
             "password": "", "ivr_id": "none", "dest": "ext-group,601,1", "cwignore": 0}
        ],
        "queues_details": [
            {"id": "400", "keyword": "strategy", "data": "rrmemory", "flags": 0},
            {"id": "400", "keyword": "timeout", "data": "15", "flags": 0},
            {"id": "400", "keyword": "wrapuptime", "data": "10", "flags": 0},
            {"id": "400", "keyword": "member", "data": "Local/101@from-queue/n,0", "flags": 0},
            {"id": "400", "keyword": "member", "data": "Local/102@from-queue/n,3", "flags": 1}
        ]
    }}}`

// The mysqldump Core makes of devices, sip, dahdi and iax (mysqldump 10.19, MariaDB 10.x), shortened.
const devicesSQL = "-- MariaDB dump 10.19  Distrib 10.5.22-MariaDB, for Linux (x86_64)\n" +
	"--\n-- Host: localhost    Database: asterisk\n" +
	"/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n" +
	"DROP TABLE IF EXISTS `devices`;\n" +
	"CREATE TABLE `devices` (\n" +
	"  `id` varchar(20) NOT NULL DEFAULT '',\n" +
	"  `tech` varchar(10) NOT NULL DEFAULT '',\n" +
	"  `dial` varchar(255) NOT NULL DEFAULT '',\n" +
	"  `devicetype` varchar(5) NOT NULL DEFAULT '',\n" +
	"  `user` varchar(50) DEFAULT NULL,\n" +
	"  `description` varchar(50) DEFAULT NULL,\n" +
	"  `emergency_cid` varchar(100) DEFAULT NULL,\n" +
	"  `hint_override` varchar(100) DEFAULT NULL,\n" +
	"  PRIMARY KEY (`id`),\n" +
	"  KEY `tech` (`tech`)\n" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;\n" +
	"LOCK TABLES `devices` WRITE;\n" +
	"/*!40000 ALTER TABLE `devices` DISABLE KEYS */;\n" +
	"INSERT INTO `devices` VALUES ('100','pjsip','PJSIP/100','fixed','100','Front Desk','',NULL),('101','pjsip','PJSIP/101','fixed','101','Dana Ortiz','',NULL),('102','iax2','IAX2/102','fixed','102','Sam Lee','',NULL);\n" +
	"/*!40000 ALTER TABLE `devices` ENABLE KEYS */;\n" +
	"UNLOCK TABLES;\n" +
	"DROP TABLE IF EXISTS `sip`;\n" +
	"CREATE TABLE `sip` (\n" +
	"  `id` varchar(20) NOT NULL DEFAULT '-1',\n" +
	"  `keyword` varchar(30) NOT NULL DEFAULT '',\n" +
	"  `data` varchar(255) NOT NULL,\n" +
	"  `flags` int(1) NOT NULL DEFAULT 0,\n" +
	"  PRIMARY KEY (`id`,`keyword`)\n" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n" +
	"LOCK TABLES `sip` WRITE;\n" +
	"INSERT INTO `sip` VALUES ('100','account','100',40),('100','callerid','Front Desk <100>',41),('100','secret','e1f2a3b4c5d6e7f8a9b0',2)," +
	"('100','media_encryption','sdes',30),('101','secret','short',2),('101','callerid','Dana \\'D\\' Ortiz <101>',41)," +
	"('tr-peer-2','host','old.carrier.example',3),('tr-peer-2','username','oldacct',4),('tr-peer-2','secret','old-carrier-secret',5)," +
	"('tr-peer-2','fromdomain','old.carrier.example',6),('tr-reg-2','register','oldacct:old-carrier-secret@old.carrier.example',0);\n" +
	"UNLOCK TABLES;\n" +
	"DROP TABLE IF EXISTS `dahdi`;\n" +
	"CREATE TABLE `dahdi` (\n  `id` varchar(20) NOT NULL DEFAULT '-1',\n  `keyword` varchar(30) NOT NULL DEFAULT '',\n" +
	"  `data` varchar(255) NOT NULL DEFAULT '',\n  `flags` int(1) NOT NULL DEFAULT 0\n) ENGINE=InnoDB;\n" +
	"-- Dump completed on 2026-10-01 10:00:00\n"

const voicemailConf = `[general]
format=wav49|wav
attach=yes
[zonemessages]
eastern=America/New_York|'vm-received' Q 'digits/at' IMp
[default]
100 => 4321,Front Desk,frontdesk@example.com,,attach=yes|saycid=no|envelope=no|delete=no
101 => 0000,Dana Ortiz,,,attach=no
`

func tgz(t *testing.T, files map[string]string, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range order {
		body, dir := files[name], strings.HasSuffix(name, "/")
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if dir {
			h = &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !dir {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func freepbxBackup(t *testing.T) []byte {
	files := map[string]string{
		"modulejson/":                         "",
		"files/":                              "",
		"metadata.json":                       `{"date":1790000000,"backupInfo":{"backup_name":"nightly"},"frameworkversion":"17.0.19"}`,
		"modulejson/Core.json":                coreJSON,
		"modulejson/Ringgroups.json":          ringgroupsJSON,
		"modulejson/Queues.json":              queuesJSON,
		"modulejson/Voicemail.json":           `{"module":"voicemail","configs":{"voicemailConf":{},"mailboxData":{}}}`,
		"files/tmp/Devices_dump/Devices.sql":  devicesSQL,
		"files/etc/asterisk/voicemail.conf":   voicemailConf,
		"files/var/lib/asterisk/sounds/x.wav": "RIFF....",
		"files/tmp/Cdr_dump/Cdr.sql":          "INSERT INTO `cdr` VALUES ('2026-10-01','x');\n",
		"modulejson/Framework.json":           `{"module":"framework","configs":[]}`,
	}
	return tgz(t, files, []string{"modulejson/", "files/", "modulejson/Core.json", "modulejson/Ringgroups.json", "modulejson/Queues.json",
		"modulejson/Voicemail.json", "modulejson/Framework.json", "files/tmp/Devices_dump/Devices.sql", "files/etc/asterisk/voicemail.conf",
		"files/var/lib/asterisk/sounds/x.wav", "files/tmp/Cdr_dump/Cdr.sql", "metadata.json"})
}

const fixture = "testdata/20261001-100000-1790000000-17.0.19-1234567.tar.gz"

// The fixture file (used by the API test and the browser test too) is this backup;
// BITVOICE_WRITE_FIXTURE=1 writes it again after the backup above is changed.
func TestFreePBXFixture(t *testing.T) {
	want := freepbxBackup(t)
	if os.Getenv("BITVOICE_WRITE_FIXTURE") != "" {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(fixture, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := gunzip(have)
	b, _ := gunzip(want)
	if !bytes.Equal(a, b) {
		t.Fatal("testdata's backup is not the one this test builds: run with BITVOICE_WRITE_FIXTURE=1")
	}
}

func TestFreePBXBackup(t *testing.T) {
	r, err := ParseFiles([]File{{Name: filepath.Base(fixture), Data: freepbxBackup(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "FreePBX backup (FreePBX 17.0.19)" {
		t.Errorf("format %q", r.Format)
	}
	ext := map[string]config.Extension{}
	for _, x := range r.Extensions {
		ext[x.Number] = x
	}
	if len(r.Extensions) != 3 {
		t.Fatalf("extensions %+v", r.Extensions)
	}
	fd := ext["100"]
	if fd.Name != "Front Desk" || fd.Secret != "e1f2a3b4c5d6e7f8a9b0" || fd.CallerID != "3125550100" || !fd.SecureMedia ||
		!fd.Voicemail.Enabled || fd.Voicemail.PIN != "4321" || fd.Email != "frontdesk@example.com" || !fd.Voicemail.Email ||
		fd.NoAnswer == nil || fd.NoAnswer.Type != "voicemail" {
		t.Errorf("100: %+v", fd)
	}
	if d := ext["101"]; d.Secret != "" || d.RingSeconds != 25 || !d.Voicemail.Enabled || d.Voicemail.PIN != "0000" || d.Voicemail.Email {
		t.Errorf("101: %+v", d)
	}
	if s := ext["102"]; s.Voicemail.Enabled || s.NoAnswer != nil {
		t.Errorf("102: %+v", s)
	}

	if len(r.Trunks) != 2 {
		t.Fatalf("trunks %+v", r.Trunks)
	}
	c := r.Trunks[0]
	if c.Name != "Carrier" || c.Host != "sip.carrier.example" || c.Port != 5060 || c.Username != "acct42" || c.Password != "carrier-secret-1" ||
		!c.Register || !c.Enabled || c.MaxChannels != 4 || c.CallerID != "3125550100" || c.FromDomain != "carrier.example" ||
		strings.Join(c.Codecs, ",") != "PCMU,PCMA,G722" {
		t.Errorf("carrier %+v", c)
	}
	o := r.Trunks[1]
	if o.Name != "Old carrier" || o.Enabled || o.Host != "old.carrier.example" || o.Username != "oldacct" || o.Password != "old-carrier-secret" || !o.Register {
		t.Errorf("old carrier %+v", o)
	}

	if len(r.OutboundRoutes) != 3 {
		t.Fatalf("outbound %+v", r.OutboundRoutes)
	}
	n1, n2, em := r.OutboundRoutes[0], r.OutboundRoutes[1], r.OutboundRoutes[2]
	if n1.Name != "National (1)" || n1.Patterns[0] != "91NXXNXXXXXX" || n1.Strip != 1 || n1.Prepend != "" || strings.Join(n1.Trunks, ",") != "Carrier,Old carrier" {
		t.Errorf("national 1 %+v", n1)
	}
	if n2.Patterns[0] != "9NXXNXXXXXX" || n2.Strip != 1 || n2.Prepend != "1" {
		t.Errorf("national 2 %+v", n2)
	}
	if em.Name != "Emergency" || em.Patterns[0] != "911" || em.Strip != 0 || em.Trunks[0] != "Carrier" {
		t.Errorf("emergency %+v", em)
	}

	if len(r.RingGroups) != 2 {
		t.Fatalf("groups %+v", r.RingGroups)
	}
	g := r.RingGroups[0]
	if g.Number != "600" || g.Name != "Reception" || g.Strategy != "all" || g.RingSeconds != 20 || strings.Join(g.Members, ",") != "100,101" ||
		g.NoAnswer == nil || *g.NoAnswer != (config.Destination{Type: "voicemail", Target: "100"}) {
		t.Errorf("600 %+v", g)
	}
	if s := r.RingGroups[1]; s.Strategy != "sequential" || s.NoAnswer != nil {
		t.Errorf("601 %+v", s)
	}

	if len(r.Queues) != 1 {
		t.Fatalf("queues %+v", r.Queues)
	}
	q := r.Queues[0]
	if q.Number != "400" || q.Name != "Support" || q.Strategy != "round-robin" || q.RingSeconds != 15 || q.WrapUpSeconds != 10 ||
		q.MaxWaitSeconds != 300 || strings.Join(q.Agents, ",") != "101,102" || q.Penalties["102"] != 3 ||
		q.Timeout != nil {
		t.Errorf("queue %+v", q)
	}

	in := map[string]config.InboundRoute{}
	for _, x := range r.InboundRoutes {
		in[x.DID] = x
	}
	if len(in) != 4 || in["3125550100"].Destination != (config.Destination{Type: "ringgroup", Target: "600"}) || in["3125550100"].Name != "Main number" ||
		in["3125550101"].Destination != (config.Destination{Type: "extension", Target: "101"}) ||
		in["3125550102"].Destination != (config.Destination{Type: "queue", Target: "400"}) ||
		in[""].Destination.Type != "hangup" {
		t.Errorf("inbound %+v", r.InboundRoutes)
	}

	all := strings.Join(r.Warnings, "\n")
	for _, w := range []string{`"*55" is not a number`, "101: its SIP password is shorter", "102 is a IAX2 device", "Custom is a custom trunk",
		"ivr-1,s,1", "outside number 3125559999", "queue 400: after the longest wait FreePBX sends callers to ext-group,601,1"} {
		if !strings.Contains(all, w) {
			t.Errorf("no warning %q in:\n%s", w, all)
		}
	}
}

// A legacy (FreePBX 13) backup: a manifest and a mysqldump of the asterisk database, gzipped.
func TestFreePBXLegacyBackup(t *testing.T) {
	sql := "CREATE TABLE `users` (\n  `extension` varchar(20) NOT NULL DEFAULT '',\n  `password` varchar(20) DEFAULT NULL,\n" +
		"  `name` varchar(50) DEFAULT NULL,\n  `voicemail` varchar(50) DEFAULT NULL,\n  `ringtimer` int(3) DEFAULT NULL,\n" +
		"  `outboundcid` varchar(50) DEFAULT NULL\n) ENGINE=MyISAM;\n" +
		"INSERT INTO `users` VALUES ('200','','Lobby','novm',0,''),('201','','O''Brien','novm',0,NULL);\n" +
		"CREATE TABLE `sip` (\n  `id` varchar(20) NOT NULL,\n  `keyword` varchar(30) NOT NULL,\n  `data` varchar(255) NOT NULL,\n  `flags` int(1) NOT NULL\n);\n" +
		"INSERT INTO `sip` VALUES ('200','secret','abcdefghijklmnopqrst',2);\n" +
		"CREATE TABLE `trunks` (\n  `trunkid` int(11) NOT NULL,\n  `name` varchar(50) NOT NULL,\n  `tech` varchar(20) NOT NULL,\n" +
		"  `outcid` varchar(40) NOT NULL,\n  `maxchans` varchar(20),\n  `disabled` varchar(4)\n);\n" +
		"INSERT INTO `trunks` VALUES (1,'ITSP','sip','',NULL,'off');\n" +
		"INSERT INTO `sip` VALUES ('tr-peer-1','host','itsp.example',0);\n" +
		"CREATE TABLE `outbound_routes` (\n  `route_id` int(11) NOT NULL,\n  `name` varchar(40),\n  `password` varchar(30)\n);\n" +
		"INSERT INTO `outbound_routes` VALUES (1,'Out',''),(2,'Local','');\n" +
		"CREATE TABLE `outbound_route_patterns` (\n  `route_id` int(11) NOT NULL,\n  `match_pattern_prefix` varchar(60),\n" +
		"  `match_pattern_pass` varchar(60),\n  `match_cid` varchar(60),\n  `prepend_digits` varchar(100)\n);\n" +
		"INSERT INTO `outbound_route_patterns` VALUES (1,'9','.','',''),(2,'','NXXXXXX','','312');\n" +
		"CREATE TABLE `outbound_route_trunks` (\n  `route_id` int(11),\n  `trunk_id` int(11),\n  `seq` int(11)\n);\n" +
		"INSERT INTO `outbound_route_trunks` VALUES (1,1,0),(2,1,0);\n" +
		"CREATE TABLE `outbound_route_sequence` (\n  `route_id` int(11),\n  `seq` int(11)\n);\n" +
		"INSERT INTO `outbound_route_sequence` VALUES (1,1),(2,0);\n" +
		"CREATE TABLE `incoming` (\n  `cidnum` varchar(20),\n  `extension` varchar(50),\n  `destination` varchar(50),\n  `description` varchar(80)\n);\n" +
		"INSERT INTO `incoming` (`cidnum`, `extension`, `destination`, `description`) VALUES ('','5550200','from-did-direct,200,1','Lobby line');\n"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(sql))
	zw.Close()
	data := tgz(t, map[string]string{"manifest": `{"pbx_version":"13.0.197","fpbx_cdrdb":"asteriskcdrdb"}`, "mysql-2.sql.gz": gz.String(), "astdb": "{}"},
		[]string{"manifest", "astdb", "mysql-2.sql.gz"})
	r, err := Parse("20201001-000000-1601510400-13.0.197-1.tgz", data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "FreePBX legacy backup (FreePBX 13.0.197)" || len(r.Extensions) != 2 || r.Extensions[1].Name != "O'Brien" ||
		r.Extensions[0].Secret != "abcdefghijklmnopqrst" {
		t.Fatalf("%s %+v", r.Format, r.Extensions)
	}
	if len(r.Trunks) != 1 || r.Trunks[0].Host != "itsp.example" {
		t.Errorf("trunks %+v", r.Trunks)
	}
	// Routes keep FreePBX's order: Local (seq 0) before Out.
	if len(r.OutboundRoutes) != 2 || r.OutboundRoutes[0].Name != "Local" || r.OutboundRoutes[0].Prepend != "312" || r.OutboundRoutes[1].Patterns[0] != "9." ||
		r.OutboundRoutes[1].Strip != 1 {
		t.Errorf("routes %+v", r.OutboundRoutes)
	}
	if len(r.InboundRoutes) != 1 || r.InboundRoutes[0].Destination.Target != "200" || r.InboundRoutes[0].Name != "Lobby line" {
		t.Errorf("inbound %+v", r.InboundRoutes)
	}
}

// A real backup, when one is at hand: BITVOICE_TEST_FREEPBX_BACKUP=/path/to/backup.tar.gz.
func TestRealFreePBXBackup(t *testing.T) {
	p := os.Getenv("BITVOICE_TEST_FREEPBX_BACKUP")
	if p == "" {
		t.Skip("BITVOICE_TEST_FREEPBX_BACKUP is not set")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseFiles([]File{{Name: filepath.Base(p), Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d extensions, %d trunks, %d groups, %d queues, %d in, %d out", r.Format, len(r.Extensions), len(r.Trunks),
		len(r.RingGroups), len(r.Queues), len(r.InboundRoutes), len(r.OutboundRoutes))
	for _, w := range r.Warnings {
		t.Log("warning:", w)
	}
}

func FuzzMysqlDump(f *testing.F) {
	f.Add([]byte(devicesSQL))
	f.Add([]byte("CREATE TABLE `users` (\n `a` int\n);\nINSERT INTO `users` VALUES ('x\\\\',NULL),('y''z',2);"))
	f.Fuzz(func(t *testing.T, data []byte) {
		b := &fpbxBackup{tables: map[string][]fpbxRow{}}
		_ = b.addDump(data)
	})
}
