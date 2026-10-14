// Package provision writes desk phones' configuration files, in each maker's own format, so a phone
// plugged in and pointed at BitVoice sets itself up with its extension.
package provision

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"text/template"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Phone is what a template needs for one phone.
type Phone struct {
	MAC       string
	Label     string
	Display   string
	User      string // the extension number, also the SIP user and auth name
	Password  string
	Server    string
	Port      int
	Transport string // udp, tcp, tls
	Voicemail string // the voicemail access number
	SRTP      bool
	Timezone  string
	// Book is the company directory: listed in the file itself (Snom), or fetched from BookURL.
	Book    []Entry
	BookURL string // https://user:password@host:port/provision, where phonebook files are served
	// Backup is a second server sharing this one's data, which the phone registers with when the
	// first does not answer (empty when there is none); BackupPort is its port.
	Backup     string
	BackupPort int
	// SRV: Server is a DNS name whose SRV records list every server; the phone finds them itself and
	// moves to the next when one does not answer.
	SRV bool
}

// Entry is one line of the phones' company directory.
type Entry struct {
	Name, Number, Company, Group string
}

// Book is the directory to give phones, and where they fetch it; Backup is a second server
// (host:port) for the phones to fall back on.
type Book struct {
	Entries []Entry
	URL     string
	Backup  string
	// SRVName, when set, is given to phones instead of an address: a DNS name listing every server.
	SRVName string
}

// File is a generated configuration file.
type File struct {
	Name        string
	ContentType string
	Body        []byte
}

// Files returns the file names a vendor's phone with this MAC asks for (both cases where phones differ).
func Names(vendor, mac string) []string {
	up := strings.ToUpper(mac)
	switch vendor {
	case "yealink":
		return []string{mac + ".cfg", up + ".cfg"}
	case "polycom":
		return []string{mac + ".cfg", up + ".cfg", mac + "-bitvoice.cfg"}
	case "grandstream":
		return []string{"cfg" + mac + ".xml", "cfg" + mac}
	case "snom":
		return []string{mac + ".xml", up + ".xml"}
	case "fanvil", "sangoma":
		return []string{mac + ".cfg", up + ".cfg"}
	case "cisco": // Cisco 3PCC (multiplatform) phones and SPA adapters
		return []string{mac + ".xml", up + ".xml", "spa" + mac + ".xml"}
	case "mitel": // Mitel 6800 and 6900 (formerly Aastra)
		return []string{up + ".cfg", mac + ".cfg"}
	case "algo":
		return []string{"algop" + mac + ".conf"}
	case "cyberdata":
		return []string{mac + ".xml", up + ".xml"}
	}
	return nil
}

// Tested says whether a vendor's file has been tried on the maker's own devices; the others are written
// from the maker's provisioning documentation.
func Tested(vendor string) bool {
	switch vendor {
	case "yealink", "polycom", "grandstream", "snom":
		return true
	}
	return false
}

// Lookup finds the phone a requested file name belongs to and renders the file. ok is false when the
// name is not one of a configured phone's files.
func Lookup(cfg *config.Config, name, server string, port, tlsPort int) (File, bool) {
	return LookupWith(cfg, name, server, port, tlsPort, Book{})
}

// LookupWith is Lookup with the company directory, for the phones that show one.
func LookupWith(cfg *config.Config, name, server string, port, tlsPort int, book Book) (File, bool) {
	if f, ok := phonebookFile(name, book.Entries); ok {
		return f, true
	}
	for _, d := range cfg.Devices {
		for _, n := range Names(d.Vendor, d.MAC) {
			if n != name {
				continue
			}
			x := cfg.FindExtension(d.Extension)
			if x == nil {
				return File{}, false
			}
			p := Phone{MAC: d.MAC, Label: d.Label, Display: x.Name, User: x.Number, Password: x.Secret, Server: server,
				Port: port, Transport: cfg.System.Provisioning.Transport, Voicemail: cfg.System.VoicemailNumber,
				SRTP: x.SecureMedia, Timezone: cfg.System.Timezone, Book: book.Entries, BookURL: book.URL}
			if p.Label == "" {
				p.Label = x.Number + " " + x.Name
			}
			if book.SRVName != "" {
				p.Server, p.Port, p.SRV = book.SRVName, 0, true
			} else if h, port, ok := strings.Cut(book.Backup, ":"); ok && h != "" {
				p.Backup = h
				fmt.Sscanf(port, "%d", &p.BackupPort)
				if p.Transport == "tls" && p.BackupPort != 0 {
					p.BackupPort++ // each server takes TLS on the port after its SIP port
				}
			}
			if p.Transport == "tls" && tlsPort != 0 && !p.SRV {
				p.Port = tlsPort
			}
			if d.Vendor == "polycom" && name != d.MAC+"-bitvoice.cfg" {
				return render(d.Vendor+"-master", p, "application/xml", name), true
			}
			if d.Vendor == "sangoma" {
				d.Vendor = "fanvil" // Sangoma's S-series take the same file
			}
			ct := "text/plain; charset=utf-8"
			if strings.HasSuffix(name, ".xml") || d.Vendor == "grandstream" || d.Vendor == "polycom" {
				ct = "application/xml"
			}
			return render(d.Vendor, p, ct, name), true
		}
	}
	return File{}, false
}

// PhonebookNames are the directory files phones fetch: Yealink's remote phonebook, Grandstream's
// phonebook.xml and Polycom's directory (one for every phone, or the phone's own).
func phonebookFile(name string, book []Entry) (File, bool) {
	var b bytes.Buffer
	switch {
	case name == "phonebook-yealink.xml":
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<YealinkIPPhoneDirectory>\n")
		for _, e := range book {
			fmt.Fprintf(&b, "  <DirectoryEntry><Name>%s</Name><Telephone>%s</Telephone></DirectoryEntry>\n", xmlEscape(e.Name), xmlEscape(e.Number))
		}
		b.WriteString("</YealinkIPPhoneDirectory>\n")
	case name == "phonebook.xml":
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<AddressBook>\n")
		for _, e := range book {
			fmt.Fprintf(&b, "  <Contact><FirstName>%s</FirstName><LastName></LastName>%s<Phone><phonenumber>%s</phonenumber><accountindex>0</accountindex></Phone></Contact>\n",
				xmlEscape(e.Name), company(e.Company), xmlEscape(e.Number))
		}
		b.WriteString("</AddressBook>\n")
	case strings.HasSuffix(name, "-directory.xml") && len(name) == len("000000000000-directory.xml") && hexMAC(strings.TrimSuffix(name, "-directory.xml")):
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<directory>\n  <item_list>\n")
		for _, e := range book {
			fmt.Fprintf(&b, "    <item><fn>%s</fn><ln></ln><ct>%s</ct></item>\n", xmlEscape(e.Name), xmlEscape(e.Number))
		}
		b.WriteString("  </item_list>\n</directory>\n")
	default:
		return File{}, false
	}
	return File{Name: name, ContentType: "application/xml", Body: b.Bytes()}, true
}

func company(c string) string {
	if c == "" {
		return ""
	}
	return "<Company>" + xmlEscape(c) + "</Company>"
}

func hexMAC(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// cfgValue keeps a Yealink value on one line.
func cfgValue(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

var funcs = template.FuncMap{
	"x": xmlEscape,
	"v": cfgValue,
	"num": func(b bool) int {
		if b {
			return 1
		}
		return 0
	},
	"yltransport": func(t string) int {
		switch t {
		case "tcp":
			return 1
		case "tls":
			return 2
		}
		return 0
	},
	"pltransport": func(t string) string {
		switch t {
		case "tcp":
			return "TCPOnly"
		case "tls":
			return "TLS"
		}
		return "UDPOnly"
	},
	"gstransport": func(t string) int {
		switch t {
		case "tcp":
			return 1
		case "tls":
			return 2
		}
		return 0
	},
	"trimscheme": func(u string) string { return strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://") },
	"fvtransport": func(t string) int {
		switch t {
		case "tcp":
			return 1
		case "tls":
			return 3
		}
		return 0
	},
	"mitransport": func(t string) int {
		switch t {
		case "tcp":
			return 2
		case "tls":
			return 4
		}
		return 1
	},
	"yesno": func(b bool) string {
		if b {
			return "Yes"
		}
		return "No"
	},
	"snomtransport": func(t string) string {
		if t == "udp" {
			return ""
		}
		return ";transport=" + t
	},
}

var templates = template.Must(template.New("").Funcs(funcs).Parse(`
{{define "yealink"}}#!version:1.0.0.1
## Written by BitVoice for {{.MAC}}. Changes made on the phone are replaced at the next check.
account.1.enable = 1
account.1.label = {{v .Label}}
account.1.display_name = {{v .Display}}
account.1.auth_name = {{.User}}
account.1.user_name = {{.User}}
account.1.password = {{v .Password}}
account.1.sip_server.1.address = {{.Server}}
account.1.sip_server.1.port = {{.Port}}
account.1.sip_server.1.transport_type = {{yltransport .Transport}}
{{- if .Backup}}
account.1.sip_server.2.address = {{.Backup}}
account.1.sip_server.2.port = {{.BackupPort}}
account.1.sip_server.2.transport_type = {{yltransport .Transport}}
{{- end}}
account.1.subscribe_mwi = 1
account.1.subscribe_mwi_to_vm = 1
account.1.srtp_encryption = {{num .SRTP}}
voice_mail.number.1 = {{.Voicemail}}
{{- if .BookURL}}
features.remote_phonebook.enable = 1
features.remote_phonebook.flash_time = 3600
remote_phonebook.data.1.name = Company
remote_phonebook.data.1.url = {{v .BookURL}}/phonebook-yealink.xml
{{- end}}
{{end}}
{{define "polycom-master"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}} -->
<APPLICATION APP_FILE_PATH="sip.ld" CONFIG_FILES="{{.MAC}}-bitvoice.cfg" />
{{end}}
{{define "polycom"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}} -->
<polycomConfig>
  <reg reg.1.address="{{.User}}" reg.1.label="{{x .Label}}" reg.1.displayName="{{x .Display}}"
    reg.1.auth.userId="{{.User}}" reg.1.auth.password="{{x .Password}}"
    reg.1.server.1.address="{{.Server}}" reg.1.server.1.port="{{.Port}}" reg.1.server.1.transport="{{pltransport .Transport}}"{{if .Backup}}
    reg.1.server.2.address="{{.Backup}}" reg.1.server.2.port="{{.BackupPort}}" reg.1.server.2.transport="{{pltransport .Transport}}"{{end}} />
  <msg msg.mwi.1.subscribe="{{.User}}" msg.mwi.1.callBackMode="contact" msg.mwi.1.callBack="{{.Voicemail}}" />
  <sec sec.srtp.enable="{{num .SRTP}}" sec.srtp.offer="{{num .SRTP}}" />
</polycomConfig>
{{end}}
{{define "grandstream"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}} -->
<gs_provision version="1">
  <mac>{{.MAC}}</mac>
  <config version="1">
    <P271>1</P271>
    <P270>{{x .Label}}</P270>
    <P47>{{.Server}}{{if not .SRV}}:{{.Port}}{{end}}</P47>
{{- if .SRV}}
    <P103>1</P103>
{{- end}}
{{- if .Backup}}
    <P2312>{{.Backup}}:{{.BackupPort}}</P2312>
{{- end}}
    <P35>{{.User}}</P35>
    <P36>{{.User}}</P36>
    <P34>{{x .Password}}</P34>
    <P3>{{x .Display}}</P3>
    <P33>{{.Voicemail}}</P33>
    <P130>{{gstransport .Transport}}</P130>
    <P183>{{num .SRTP}}</P183>
{{- if .BookURL}}
    <P330>3</P330>
    <P331>{{x (trimscheme .BookURL)}}</P331>
    <P332>60</P332>
{{- end}}
  </config>
</gs_provision>
{{end}}
{{define "snom"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}} -->
<settings>
  <phone-settings>
    <user_active idx="1" perm="">on</user_active>
    <user_realname idx="1" perm="">{{x .Display}}</user_realname>
    <user_name idx="1" perm="">{{.User}}</user_name>
    <user_pname idx="1" perm="">{{.User}}</user_pname>
    <user_pass idx="1" perm="">{{x .Password}}</user_pass>
    <user_host idx="1" perm="">{{.Server}}{{if not .SRV}}:{{.Port}}{{end}}{{snomtransport .Transport}}</user_host>
    <user_mailbox idx="1" perm="">{{.Voicemail}}</user_mailbox>
    <user_srtp idx="1" perm="">{{if .SRTP}}on{{else}}off{{end}}</user_srtp>
  </phone-settings>
{{- if .Book}}
  <tbook e="2" complete="true">
{{- range $i, $e := .Book}}
    <item context="active" type="none" fav="false" mod="false" index="{{$i}}"><name>{{x $e.Name}}</name><number>{{x $e.Number}}</number>{{if $e.Company}}<organization>{{x $e.Company}}</organization>{{end}}</item>
{{- end}}
  </tbook>
{{- end}}
</settings>
{{end}}
{{define "fanvil"}}<<VOIP CONFIG FILE>>Version:2.0002
# Written by BitVoice for {{.MAC}}. Changes made on the phone are replaced at the next check.

<SIP CONFIG MODULE>
--SIP Line List--  :
SIP1 Phone Number       :{{.User}}
SIP1 Display Name       :{{v .Display}}
SIP1 Sip Name           :{{v .Label}}
SIP1 Register Addr      :{{.Server}}
SIP1 Register Port      :{{.Port}}
SIP1 Register User      :{{.User}}
SIP1 Register Pswd      :{{v .Password}}
SIP1 Register TTL       :3600
SIP1 Enable Reg         :1
{{- if .Backup}}
SIP1 Backup Addr        :{{.Backup}}
SIP1 Backup Port        :{{.BackupPort}}
{{- end}}
SIP1 Transport Protocol :{{fvtransport .Transport}}
SIP1 MWI Num            :{{.Voicemail}}
SIP1 Subscribe MWI      :1
SIP1 RTP Encrypt        :{{num .SRTP}}
{{- if .SRV}}
SIP1 DNS Mode           :2
{{- end}}
<<END OF FILE>>
{{end}}
{{define "cisco"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}} -->
<flat-profile>
  <Line_Enable_1_>Yes</Line_Enable_1_>
  <Short_Name_1_>{{x .Label}}</Short_Name_1_>
  <Display_Name_1_>{{x .Display}}</Display_Name_1_>
  <User_ID_1_>{{.User}}</User_ID_1_>
  <Auth_ID_1_>{{.User}}</Auth_ID_1_>
  <Use_Auth_ID_1_>Yes</Use_Auth_ID_1_>
  <Password_1_>{{x .Password}}</Password_1_>
  <Proxy_1_>{{.Server}}{{if not .SRV}}:{{.Port}}{{end}}</Proxy_1_>
  <Use_DNS_SRV_1_>{{yesno .SRV}}</Use_DNS_SRV_1_>
{{- if .Backup}}
  <Alternate_Proxy_1_>{{.Backup}}:{{.BackupPort}}</Alternate_Proxy_1_>
{{- end}}
  <Register_1_>Yes</Register_1_>
  <SIP_Transport_1_>{{if eq .Transport "tcp"}}TCP{{else if eq .Transport "tls"}}TLS{{else}}UDP{{end}}</SIP_Transport_1_>
  <Voice_Mail_Number>{{.Voicemail}}</Voice_Mail_Number>
  <Secure_Call_Serv>{{yesno .SRTP}}</Secure_Call_Serv>
</flat-profile>
{{end}}
{{define "mitel"}}# Written by BitVoice for {{.MAC}}. Changes made on the phone are replaced at the next check.
sip line1 auth name: {{.User}}
sip line1 user name: {{.User}}
sip line1 password: {{v .Password}}
sip line1 display name: {{v .Display}}
sip line1 screen name: {{v .Label}}
sip line1 proxy ip: {{.Server}}
sip line1 proxy port: {{if .SRV}}0{{else}}{{.Port}}{{end}}
sip line1 registrar ip: {{.Server}}
sip line1 registrar port: {{if .SRV}}0{{else}}{{.Port}}{{end}}
{{- if .Backup}}
sip line1 backup proxy ip: {{.Backup}}
sip line1 backup proxy port: {{.BackupPort}}
sip line1 backup registrar ip: {{.Backup}}
sip line1 backup registrar port: {{.BackupPort}}
{{- end}}
sip line1 vmail: {{.Voicemail}}
sip transport protocol: {{mitransport .Transport}}
sip srtp mode: {{num .SRTP}}
{{end}}
{{define "algo"}}# Written by BitVoice for {{.MAC}}: an Algo paging speaker, strobe or adapter.
sip.proxy = {{.Server}}{{if not .SRV}}:{{.Port}}{{end}}
sip.u1.auth = {{.User}}
sip.u1.user = {{.User}}
sip.u1.pwd = {{v .Password}}
sip.u1.disp = {{v .Display}}
sip.transport = {{if eq .Transport "tcp"}}tcp{{else if eq .Transport "tls"}}tls{{else}}udp{{end}}
{{- if .Backup}}
sip.proxy2 = {{.Backup}}:{{.BackupPort}}
{{- end}}
sip.srtp = {{if .SRTP}}optional{{else}}disabled{{end}}
{{end}}
{{define "cyberdata"}}<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by BitVoice for {{.MAC}}: a CyberData speaker, intercom or paging adapter. -->
<specific>
  <SIPSettings>
    <SIPOperation>1</SIPOperation>
    <SIPServer>{{.Server}}</SIPServer>
    <RemoteSIPPort>{{if .SRV}}5060{{else}}{{.Port}}{{end}}</RemoteSIPPort>
{{- if .Backup}}
    <BackupSIPServer1>{{.Backup}}</BackupSIPServer1>
{{- end}}
    <SIPUserID>{{.User}}</SIPUserID>
    <SIPAuthID>{{.User}}</SIPAuthID>
    <SIPAuthPassword>{{x .Password}}</SIPAuthPassword>
    <SIPRegister>1</SIPRegister>
    <SIPTransport>{{if eq .Transport "tcp"}}1{{else if eq .Transport "tls"}}2{{else}}0{{end}}</SIPTransport>
  </SIPSettings>
</specific>
{{end}}`))

func render(name string, p Phone, contentType, file string) File {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, p); err != nil {
		panic(fmt.Sprintf("provision template %s: %v", name, err)) // the templates are fixed: a bug, not input
	}
	return File{Name: file, ContentType: contentType, Body: bytes.TrimLeft(b.Bytes(), "\n")}
}
