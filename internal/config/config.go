// Package config defines BitVoice's configuration: every setting the web interface edits. The whole
// system is one document, so it can be exported, kept in git, diffed and imported (configuration as code),
// and every change is a revision that can be undone.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schema is the configuration format version.
const Schema = 1

// Config is the whole system configuration.
type Config struct {
	Schema         int             `json:"schema"`
	System         System          `json:"system"`
	Extensions     []Extension     `json:"extensions"`
	RingGroups     []RingGroup     `json:"ringGroups"`
	Queues         []Queue         `json:"queues"`
	Faxes          []FaxBox        `json:"faxes"`
	PageGroups     []PageGroup     `json:"pageGroups"`
	Conferences    []Conference    `json:"conferences"`
	Devices        []Device        `json:"devices"`
	Schedules      []Schedule      `json:"schedules"`
	Menus          []Menu          `json:"menus"`
	Sites          []Site          `json:"sites"`
	Receptionists  []Receptionist  `json:"receptionists"`
	Scripts        []Script        `json:"scripts"`
	Trunks         []Trunk         `json:"trunks"`
	InboundRoutes  []InboundRoute  `json:"inboundRoutes"`
	OutboundRoutes []OutboundRoute `json:"outboundRoutes"`
	Security       Security        `json:"security"`
}

// System holds settings for the whole installation.
type System struct {
	Name     string `json:"name"`
	Purpose  string `json:"purpose"` // office, contact-centre, 911
	Country  string `json:"country"` // ISO 3166 alpha-2, drives tones and emergency numbers
	Timezone string `json:"timezone"`
	Language string `json:"language"`

	// PublicAddress is advertised in SIP and SDP. Empty means work it out per peer.
	PublicAddress string `json:"publicAddress"`
	SIPPort       int    `json:"sipPort"`
	RTPMin        int    `json:"rtpMin"`
	RTPMax        int    `json:"rtpMax"`
	// Codecs phones may use, best first (PCMU, PCMA, G722, OPUS, G729, G726-32, AAL2-G726-32, G726-16,
	// GSM). BitVoice converts between any two of them.
	Codecs []string `json:"codecs"`
	// EchoNumber reaches the echo test; ToneNumber plays a test tone.
	EchoNumber string `json:"echoNumber"`
	// AgentLogin and AgentPause are dialled by queue agents: log in or out of every queue, pause or resume.
	AgentLogin string `json:"agentLogin"`
	AgentPause string `json:"agentPause"`
	// CallLinks let people call from a web page, a QR code or a website button.
	CallLinks []CallLink `json:"callLinks"`
	// Brands name the company's outgoing numbers: the name, logo and reason shown where the carrier
	// and the called phone support it, and how each number's reputation is watched.
	Brands []Brand `json:"brands"`
	// Cluster is how phones and providers reach servers that share one database.
	Cluster ClusterSettings `json:"cluster"`
	// Texting sends and receives text messages from the business numbers, through texting providers.
	Texting Texting `json:"texting"`
	// Screening judges calls from outside: verified caller ID (STIR/SHAKEN) and blocked numbers.
	Screening Screening `json:"screening"`
	// VIPs go to the front of every queue they call (after any VIP already waiting).
	VIPs []VIP `json:"vips"`
	// HotDesk is how people sign in at shared desk phones (extensions marked hot desk).
	HotDesk HotDeskCodes `json:"hotDesk"`
	// VoicemailNumber is dialled to hear your messages.
	VoicemailNumber string `json:"voicemailNumber"`
	// Codes are the feature codes for forwarding, do not disturb, follow-me, pickup, intercom and speed dials.
	Codes FeatureCodes `json:"codes"`
	// SpeedDials are the company's short codes, dialled after the speed dial code (*0).
	SpeedDials []SpeedDial `json:"speedDials"`
	// ParkNumber parks a call when it is transferred there; the nine numbers after it are the slots.
	ParkNumber string `json:"parkNumber"`
	// ParkSeconds before a parked call rings whoever parked it.
	ParkSeconds int `json:"parkSeconds"`
	// EmergencyNumbers are always routed, to the emergency route, from every extension.
	EmergencyNumbers []string `json:"emergencyNumbers"`
	// Provisioning serves phones their settings over HTTPS.
	Provisioning Provisioning `json:"provisioning"`
	// Email is the mail server BitVoice sends voicemail through.
	Email Email `json:"email"`
	// Radio is the radio dispatch console profile.
	Radio Radio `json:"radio"`
	// Core is the NG911 core services profile (ECRF/LoST, ESRP).
	Core Core `json:"core"`
	// PSAP is the 911 profile's settings: who this answering point is and where CAD hears of calls.
	PSAP PSAP `json:"psap"`
	// FaxMail is the mailbox read for email to fax.
	FaxMail FaxMail `json:"faxMail"`
	// FaxPaper is the page size faxes are drawn on: letter or a4 (empty: by country).
	FaxPaper string `json:"faxPaper"`
	// Archive copies backups, recordings and voicemail to cloud storage.
	Archive Archive `json:"archive"`
	// AI is an optional language model for voicemail transcripts and summaries.
	AI AI `json:"ai"`
	// Compat speaks Asterisk's AMI and ARI, for tools written for Asterisk.
	Compat Compat `json:"compat"`
	// SSO signs people in to the web interface through an OpenID Connect identity provider.
	SSO SSO `json:"sso"`
	// CRM connects a customer system: who a caller is, a link to open them, and every call logged.
	CRM CRM `json:"crm"`
	// Metrics pushes measurements to an OpenTelemetry collector (Prometheus scrapes /metrics instead).
	Metrics Metrics `json:"metrics"`
	// Costs prices calls (rates are on each trunk) and alerts on budgets and runaway spend.
	Costs Costs `json:"costs"`
	// Recording decides which calls are recorded.
	Recording Recording `json:"recording"`
	// Retention deletes call records and voicemail after a while.
	Retention Retention `json:"retention"`
	// Audio cleans up sound: background noise and uneven levels.
	Audio Audio `json:"audio"`
	// Mobile is how BitVoice wakes its mobile apps for incoming calls.
	Mobile Mobile `json:"mobile"`
	// EmergencyMode, when on, sends every incoming call to one place (an evacuation, a closure, an
	// incident): one switch instead of changing every route.
	EmergencyMode EmergencyMode `json:"emergencyMode"`
	// GitOps keeps the configuration in step with a file in a Git repository.
	GitOps GitOps `json:"gitops"`
	// Vault is how secret fields written as references (aws-sm:..., azure-kv:...) are read.
	Vault Vault `json:"vault"`
	// EmergencyNotify lists extensions alerted when anyone dials an emergency number (Kari's Law).
	EmergencyNotify []string `json:"emergencyNotify"`
	// EmergencyEmail is told too, by email (a security desk, a facilities team).
	EmergencyEmail []string `json:"emergencyEmail"`
	// SendLocation sends the caller's civic address with emergency calls (PIDF-LO, RFC 4119 and 6442),
	// for providers that take it.
	SendLocation bool `json:"sendLocation"`
	// VideoOff refuses video in calls (audio only). By default video passes between phones that both
	// offer it, as it is, without conversion.
	VideoOff bool `json:"videoOff,omitempty"`
}

// Provisioning lets desk phones fetch their configuration from BitVoice, protected by a user name and
// password the phones are given once (usually through DHCP option 66 or the phone's web page).
type Provisioning struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Transport the phones register with: udp, tcp or tls.
	Transport string `json:"transport"`
}

// Email is an SMTP server for outgoing mail.
type Email struct {
	Host     string `json:"host"`
	Port     int    `json:"port"` // 587 (STARTTLS) or 465 (TLS)
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

// Archive is off-site storage: an S3 bucket or an Azure Blob container.
type Archive struct {
	Target     string       `json:"target"` // "", s3 or azure
	S3         ArchiveS3    `json:"s3"`
	Azure      ArchiveAzure `json:"azure"`
	Recordings bool         `json:"recordings"`
	Voicemail  bool         `json:"voicemail"`
}

// ArchiveS3 is an S3 bucket. Empty keys use the instance role (EC2 or ECS), which is better.
type ArchiveS3 struct {
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Endpoint  string `json:"endpoint"` // S3-compatible services only
}

// ArchiveAzure is a container reached with a SAS URL.
type ArchiveAzure struct {
	ContainerSAS string `json:"containerSas"`
	Prefix       string `json:"prefix"`
}

// AI connects a language model over the OpenAI-compatible API (OpenRouter, or a local server). Nothing is
// sent anywhere unless Enabled is set.
type AI struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"` // empty: OpenRouter
	Key     string `json:"key"`
	Model   string `json:"model"` // must accept audio for transcripts, e.g. google/gemini-2.5-flash
	// Voicemail transcribes and summarises new messages.
	Voicemail bool `json:"voicemail"`
	// Calls transcribes recorded calls, who said what, with a summary and action items, so calls can
	// be searched by what was said. Only recorded calls: recording (and telling callers) comes first.
	Calls bool `json:"calls"`
	// Briefings write a short summary for whoever a call is transferred to (or handed to by the AI
	// receptionist), from what was said so far, shown on their browser phone and sent to their desk phone.
	Briefings bool `json:"briefings"`
	// Insights adds, to each call transcript, how the caller's mood moved and moments a supervisor may
	// want to hear, each with what was said. Evidence for a person, never a score.
	Insights bool `json:"insights"`
	// AudioModel, when set, turns speech into text instead of Model (which may then be text-only, such
	// as meta-llama/llama-3.3-70b-instruct, with google/gemini-2.5-flash for audio).
	AudioModel string `json:"audioModel"`
	// SpeechModel and Voice turn text into speech for the receptionist (an OpenAI-compatible
	// /audio/speech service at the same address), such as tts-1 and alloy.
	SpeechModel string `json:"speechModel"`
	Voice       string `json:"voice"`
}

// Receptionist is an AI receptionist: it answers calls, says it is automated, answers questions from
// approved information, takes messages and transfers to a person. Pressing 0 or saying "person"
// always reaches the person destination.
type Receptionist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Greeting  string `json:"greeting"`  // what it says first
	Knowledge string `json:"knowledge"` // approved facts: hours, address, services
	// Person is where callers go to reach a human; MessageBox is the mailbox messages are left in.
	Person     Destination `json:"person"`
	MessageBox string      `json:"messageBox"`
}

// backToAssistant follows the hops a call takes without the caller choosing anything (a schedule's
// open and closed branches, a menu's no-key timeout) and says how it reaches an AI receptionist or a
// script before any person rings: "through the schedule Hours", or "" when it never does.
func (c *Config) backToAssistant(d Destination) string {
	seen := map[string]bool{}
	var walk func(d Destination, via string) string
	walk = func(d Destination, via string) string {
		key := d.Type + ":" + d.Target
		if seen[key] {
			return ""
		}
		seen[key] = true
		switch d.Type {
		case "receptionist", "script":
			if via == "" {
				return "directly"
			}
			return via
		case "schedule":
			s := c.FindSchedule(d.Target)
			if s == nil {
				return ""
			}
			w := "through the schedule " + s.Name
			if r := walk(s.Open, w); r != "" {
				return r
			}
			return walk(s.Closed, w)
		case "menu":
			m := c.FindMenu(d.Target)
			if m == nil || m.Timeout == nil {
				return ""
			}
			return walk(*m.Timeout, "through the menu "+m.Name+" (when the caller presses nothing)")
		}
		return ""
	}
	return walk(d, "")
}

// FindReceptionist returns a receptionist by ID, or nil.
func (c *Config) FindReceptionist(id string) *Receptionist {
	for i := range c.Receptionists {
		if c.Receptionists[i].ID == id {
			return &c.Receptionists[i]
		}
	}
	return nil
}

// FaxBox is a fax number BitVoice answers itself: each fax received becomes a PDF, kept on the Faxes
// page and sent to the email addresses.
type FaxBox struct {
	ID     string   `json:"id"`
	Number string   `json:"number"` // what routes and people dial to reach it
	Name   string   `json:"name"`
	Email  []string `json:"email"`
	// Station is the identity BitVoice's fax machine gives the sender (CSI): usually the fax number
	// in full, as the sender's machine shows and prints it.
	Station string `json:"station"`
	// Senders may send faxes from this number by email, besides the addresses in Email: whole
	// addresses, or @example.com for anyone at a domain.
	Senders []string `json:"senders"`
}

// Radio holds the radio dispatch console: the radio channels BitVoice talks to through radio-over-IP
// gateways, and the number a dispatcher's phone (or browser) dials to join the console.
type Radio struct {
	Console  string         `json:"console"` // the number dispatchers dial; empty turns the console off
	Channels []RadioChannel `json:"channels"`
	// P25 is this console's identity on a P25 system, for talkgroup channels (ISSI/CSSI).
	P25 P25Console `json:"p25"`
	// Record keeps every transmission on every channel, heard or sent, as a recording and a line
	// in the radio log.
	Record bool `json:"record"`
	// CAD gets radio data as it happens: each event POSTed as JSON to CADURL (with CADSecret as a
	// bearer token when set): a radio talking (radio.unit), an emergency (radio.emergency), and
	// each finished transmission (radio.transmission).
	CADURL    string `json:"cadUrl"`
	CADSecret string `json:"cadSecret"`
}

// P25Console names the console subsystem on the P25 system: the system's WACN and system ID,
// this subsystem's RFSS ID, the unit ID dispatchers transmit as, and the SIP port it listens on.
type P25Console struct {
	WACN     string `json:"wacn"`     // 5 hex digits
	System   string `json:"system"`   // 3 hex digits
	RFSS     int    `json:"rfss"`     // 1-254
	Unit     int    `json:"unit"`     // 1-16777215
	SIPPort  int    `json:"sipPort"`  // the SIP port for the ISSI (default 5062)
	Console  bool   `json:"console"`  // send the CSSI console PTT word with each request
	Priority int    `json:"priority"` // transmit priority 1-4 (default 4)
	// Keys are the P25 traffic keys (TIA-102.AAAD) the console decrypts with, and a channel may
	// transmit with. Their values are never sent back by the API.
	Keys []P25Key `json:"keys"`
	// Private (unit-to-unit) calls go to the RFSS that is home to the radios (default: the first
	// talkgroup's home), and are transmitted with UnitTxKey (empty: clear).
	UnitHome     string `json:"unitHome"`
	UnitHomeRFSS int    `json:"unitHomeRfss"`
	UnitTxKey    string `json:"unitTxKey"`
}

// P25Key is one traffic key: its algorithm, its key ID on the P25 system, and the key variable in
// hexadecimal.
type P25Key struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Alg   string `json:"alg"`   // aes256, des or tdes
	KeyID int    `json:"keyId"` // 0-65535
	Key   string `json:"key"`   // hex: 64 digits for AES-256, 16 for DES, 48 for triple DES
}

// P25KeyDigits is how many hex digits a key of an algorithm has (0: unknown algorithm).
func P25KeyDigits(alg string) int {
	switch alg {
	case "aes256":
		return 64
	case "des":
		return 16
	case "tdes":
		return 48
	}
	return 0
}

// RadioChannel is one radio channel or talkgroup on a RoIP gateway, which sends the channel's
// receive audio as RTP to Port here and takes transmit audio as RTP at Gateway (G.711 µ-law, keying
// the radio on voice, as RoIP gateways do).
type RadioChannel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Port    int    `json:"port"`    // the UDP port here the gateway sends to
	Gateway string `json:"gateway"` // host:port the gateway listens on for transmit audio
	Color   string `json:"color"`   // a tile colour on the console
	// Kind is "roip" (a gateway, Port and Gateway above; the default) or "p25" (a talkgroup over
	// the ISSI: Group at the home RFSS Home, an RTP Port here).
	Kind     string `json:"kind"`
	Group    int    `json:"group"`    // P25 talkgroup ID, 1-65535
	Home     string `json:"home"`     // the home RFSS's SIP address, host:port
	HomeRFSS int    `json:"homeRfss"` // the home RFSS's ID
	// TxKey is the traffic key (a P25Key's ID) dispatchers transmit with; empty transmits clear.
	TxKey string `json:"txKey"`
}

// Core holds the NG911 Core Services profile: the ECRF answering LoST queries from the service
// boundaries kept on the server (uploaded on the Core services page).
type Core struct {
	ECRF   bool   `json:"ecrf"`   // answer LoST at /lost
	Source string `json:"source"` // this ECRF's name in answers; the server's name when empty
	// AllowFrom is the addresses and ranges (the ESInet) that may query; LoST answers nobody else.
	AllowFrom []string `json:"allowFrom"`
	// ESRP: calls sent to the "esrp" destination go to the PSAP serving the caller's location.
	PSAPs []CorePSAP `json:"psaps"`
	// Default is the PSAP URI for calls with no location, or one no area covers (the policy's
	// default route, by NENA practice the PSAP that covers the incoming trunk's area).
	Default string `json:"default"`
	// LoSTURL is an outside ECRF to ask; empty uses this server's own boundaries.
	LoSTURL string `json:"lostUrl"`
	// Rules is the ESRP's policy (NENA's policy routing function): checked in order once the ECRF
	// has chosen a PSAP; the first rule whose conditions all hold decides.
	Rules []CoreRule `json:"rules"`
	// BCF is the border control function at the ESInet edge.
	BCF BCF `json:"bcf"`
}

// CoreRule is one policy routing rule. Empty conditions match everything.
type CoreRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Conditions.
	Services   []string `json:"services"`   // service URNs (urn:service:sos.fire covers its sub-services)
	PSAPs      []string `json:"psaps"`      // the PSAP the ECRF chose (URIs)
	Schedule   string   `json:"schedule"`   // a schedule's ID
	WhenOpen   bool     `json:"whenOpen"`   // with a schedule: match while it is open (else while closed)
	NoLocation bool     `json:"noLocation"` // only calls that brought no location
	Suspicious bool     `json:"suspicious"` // only calls the BCF marked suspicious
	Trunks     []string `json:"trunks"`     // only calls that came in on these trunks (IDs)
	// Action: "route" to a PSAP (To, with its overflow chain), or "reject" (busy).
	Action string `json:"action"`
	To     string `json:"to"`
}

// BCF guards the ESInet edge: calls from ESInet trunks are checked before anything else. A source
// that goes over a limit is refused, or (Mark) let through marked suspicious for policy to handle.
type BCF struct {
	Enabled bool `json:"enabled"`
	// PerMinute is the most new calls one address may start in a minute (0: no limit).
	PerMinute int `json:"perMinute"`
	// Concurrent is the most calls one address may have at once (0: no limit).
	Concurrent int `json:"concurrent"`
	// Deny is addresses and ranges whose calls are always refused.
	Deny []string `json:"deny"`
	// Mark lets calls over a limit through, marked suspicious, instead of refusing them.
	Mark bool `json:"mark"`
}

// CorePSAP is a PSAP the ESRP can send calls to: the URI the ECRF answers with, the trunk that
// reaches it, and where its calls go when it cannot take them.
type CorePSAP struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Trunk    string `json:"trunk"`    // trunk ID
	Overflow string `json:"overflow"` // another PSAP's URI, tried when this one is busy or down
}

// PSAP holds the 911 profile: the answering point's identity in NENA i3 terms, and the outputs
// that tell CAD about each emergency call when a call-taker answers it.
type PSAP struct {
	AgencyID  string `json:"agencyId"`  // a domain naming the agency, like psap.county.example.gov
	ElementID string `json:"elementId"` // this system's own name in i3 logs; the host name when empty
	// ALI spill: the text block sent to CAD over TCP for each answered call (host:port of CAD's listener).
	ALIAddress string `json:"aliAddress"`
	// EIDO: the incident document POSTed to CAD (https URL), with a bearer token if CAD wants one.
	EIDOURL   string `json:"eidoUrl"`
	EIDOToken string `json:"eidoToken"`
	// AnswerTarget is the answer time goal in seconds (NENA: 90% within 15 s); 15 when empty.
	AnswerTarget int `json:"answerTarget"`
}

// FaxMail is the mailbox BitVoice reads for email to fax: a message to it with a fax number in
// the subject (or after a + in the address, fax+2125550199@example.com) is faxed, its attachments
// and text as the pages, from the fax number whose people sent it.
type FaxMail struct {
	Host     string `json:"host"`     // IMAP server; empty turns email to fax off
	Port     int    `json:"port"`     // 993 (TLS)
	Username string `json:"username"` // the mailbox's sign-in
	Password string `json:"password"`
	Folder   string `json:"folder"` // INBOX unless set
	// AnySender skips the check that the sending domain's mail server vouched for the message
	// (the receiving server's DKIM, SPF or DMARC result). Off: unverified mail is refused.
	AnySender bool `json:"anySender"`
}

// Device is a desk phone BitVoice configures, identified by its MAC address.
// Site is a building or office with its civic address, and the networks phones there use: a phone
// registering from elsewhere is a remote worker, whose own address is used for emergency calls.
type Site struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Country    string   `json:"country"` // ISO 3166 alpha-2
	State      string   `json:"state"`   // A1: state or province
	County     string   `json:"county"`  // A2
	City       string   `json:"city"`    // A3
	Street     string   `json:"street"`  // RD with its suffix, such as Main Street
	Number     string   `json:"number"`  // HNO and HNS, such as 12A
	PostalCode string   `json:"postalCode"`
	Networks   []string `json:"networks"` // CIDRs phones at this site register from
}

// Address is a civic address for 911.
type Address struct {
	Country    string `json:"country"`
	State      string `json:"state"`
	City       string `json:"city"`
	Street     string `json:"street"`
	Number     string `json:"number"`
	PostalCode string `json:"postalCode"`
	Detail     string `json:"detail"` // floor, apartment
}

// FindSite returns a site by ID, or nil.
func (c *Config) FindSite(id string) *Site {
	for i := range c.Sites {
		if c.Sites[i].ID == id {
			return &c.Sites[i]
		}
	}
	return nil
}

func (c *Config) validateSites(p *Problems) {
	for i := range c.Sites {
		s := &c.Sites[i]
		f := fmt.Sprintf("sites[%d]", i)
		if s.ID == "" {
			s.ID = NewID()
		}
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			p.add(f+".name", "name the site")
		}
		s.Country = strings.ToUpper(strings.TrimSpace(s.Country))
		if len(s.Country) != 2 {
			p.add(f+".country", "use the two-letter country code, such as US")
		}
		for _, k := range []struct{ field, v, msg string }{
			{"street", s.Street, "enter the street"}, {"number", s.Number, "enter the building number"}, {"city", s.City, "enter the city"},
		} {
			if strings.TrimSpace(k.v) == "" {
				p.add(f+"."+k.field, "%s", k.msg)
			}
		}
		for j, n := range s.Networks {
			if _, err := netip.ParsePrefix(strings.TrimSpace(n)); err != nil {
				p.add(fmt.Sprintf("%s.networks[%d]", f, j), "use a network such as 10.1.0.0/16")
			}
		}
	}
	for i, x := range c.Extensions {
		if x.Site != "" && c.FindSite(x.Site) == nil {
			p.add(fmt.Sprintf("extensions[%d].site", i), "no such site")
		}
	}
	for i, a := range c.System.EmergencyEmail {
		if _, err := mail.ParseAddress(a); err != nil || strings.ContainsAny(a, "\r\n") {
			p.add(fmt.Sprintf("system.emergencyEmail[%d]", i), "%s is not an email address", a)
		}
	}
}

type Device struct {
	ID        string `json:"id"`
	MAC       string `json:"mac"`    // twelve lowercase hex digits
	Vendor    string `json:"vendor"` // yealink, polycom, grandstream, snom
	Model     string `json:"model"`
	Extension string `json:"extension"`
	Label     string `json:"label"`
}

// Vendors BitVoice can provision.
var Vendors = []string{"yealink", "polycom", "grandstream", "snom"}

// NormalizeMAC returns twelve lowercase hex digits, or "" when s is not a MAC address.
func NormalizeMAC(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r == ':' || r == '-' || r == '.' || r == ' ':
		default:
			return ""
		}
	}
	if b.Len() != 12 {
		return ""
	}
	return b.String()
}

// Recording settings. Recording laws differ: some places need every party's consent.
type Recording struct {
	// Mode is off, all (every answered call) or selected (calls to or from extensions marked Record).
	Mode string `json:"mode"`
	// KeepDays deletes recordings older than this; 0 keeps them.
	KeepDays int `json:"keepDays"`
	// PauseCode and ResumeCode, pressed by someone on an extension during a recorded call, pause and resume
	// the recording (card payments, for PCI DSS). The caller is not recorded meanwhile.
	PauseCode  string `json:"pauseCode"`
	ResumeCode string `json:"resumeCode"`
	// Notice is how the people on a recorded call are told: off, announce (the recorded notice is
	// played to both sides as the call is answered, and recording starts after it) or beep (a short
	// tone to both sides every 15 seconds while it records).
	Notice string `json:"notice"`
	// Rules choose by the outside number, longest match first: for places whose law asks for more
	// (every party's consent) or less, or numbers never to record.
	Rules []ConsentRule `json:"rules"`
}

// ConsentRule is how calls to or from numbers starting with Prefix are treated when recorded.
type ConsentRule struct {
	Prefix string `json:"prefix"` // digits, as written in E.164 without the +: 1415 for San Francisco
	Name   string `json:"name"`   // California
	Action string `json:"action"` // announce, beep, off (no notice) or never (not recorded)
}

// Retention deletes old records; 0 keeps them. Anything under a legal hold is kept whatever its age.
type Retention struct {
	CallRecordDays int `json:"callRecordDays"`
	VoicemailDays  int `json:"voicemailDays"`
}

// Extension is a user's phone number on the system.
type Extension struct {
	ID       string `json:"id"`
	Number   string `json:"number"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Secret   string `json:"secret"`
	Enabled  bool   `json:"enabled"`
	Location string `json:"location"` // more detail for 911: wing, desk, landmarks
	// Site, Floor and Room are the extension's dispatchable location (RAY BAUM'S Act), sent with
	// emergency calls as a civic address (PIDF-LO) when the phone is at the site.
	Site string `json:"site"` // a site's ID
	// Calendar reads the person's calendar so meetings and time off send calls elsewhere.
	Calendar ExtCalendar `json:"calendar"`
	// HotDesk makes this extension a shared desk phone: people sign in at it with their own
	// extension and voicemail PIN, and it rings and calls as them until they sign out.
	HotDesk bool `json:"hotDesk"`
	// Department groups the extension's call costs ("Sales").
	Department string `json:"department"`
	Floor      string `json:"floor"`
	Room       string `json:"room"`
	// CallerID is the external number presented on outbound calls; empty uses the trunk's.
	CallerID string `json:"callerId"`
	// RingSeconds before an unanswered call goes to NoAnswer.
	RingSeconds int          `json:"ringSeconds"`
	NoAnswer    *Destination `json:"noAnswer,omitempty"`
	// Outbound enables calls to outside numbers.
	Outbound bool `json:"outbound"`
	// SecureMedia offers encrypted media (SRTP) when calling this extension's phones. Phones that offer
	// SRTP themselves always get it.
	SecureMedia bool `json:"secureMedia"`
	// Record records this extension's calls when recording mode is "selected".
	Record bool `json:"record"`
	// Voicemail is the extension's mailbox.
	Voicemail Voicemail `json:"voicemail"`
	// Kind is what is connected: "" or phone, door (a door station), speaker (an overhead paging
	// speaker: calls to it are answered at once, one way), analog (a port on an analog adapter: G.711 only,
	// which faxes and old phones need).
	Kind string `json:"kind"`
	// Door is how a door station's door is opened; only for kind door.
	Door Door `json:"door"`
	// FollowMe rings more numbers (a mobile, another desk) when the extension's own phones are not
	// answered, before its no-answer destination.
	FollowMe FollowMe `json:"followMe"`
	// PickupGroup: anyone in the same group can answer this extension's ringing calls with the pickup
	// code (*8). Empty for none.
	PickupGroup string `json:"pickupGroup,omitempty"`
	// NoIntercom refuses intercom calls to this extension (they ring as ordinary calls instead).
	NoIntercom bool `json:"noIntercom,omitempty"`
	// SpeedDials are this person's own short codes, dialled after the speed dial code (*0). They win
	// over the company's speed dials with the same code.
	SpeedDials []SpeedDial `json:"speedDials,omitempty"`
}

// FollowMe is where else an extension's calls ring when its phones are not answered.
type FollowMe struct {
	Enabled bool `json:"enabled"`
	// Numbers are rung after the extension's own phones: extensions or outside numbers (outside ones
	// need the extension to be allowed outbound calls).
	Numbers []string `json:"numbers"`
	// Strategy: sequential (each number in turn, the default) or together (all of them at once).
	Strategy string `json:"strategy"`
	// RingSeconds each number (or all together) rings.
	RingSeconds int `json:"ringSeconds"`
	// Confirm makes whoever answers on an outside number press 1 to take the call, so a mobile's own
	// voicemail never takes it.
	Confirm bool `json:"confirm"`
}

// SpeedDial is a short code for a number: dial the speed dial code and then Code.
type SpeedDial struct {
	Code   string `json:"code"` // 1 to 3 digits
	Number string `json:"number"`
	Name   string `json:"name"`
}

// Conference is a room people dial into and all talk together.
type Conference struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
	// PIN is asked before joining; empty lets anyone who dials the number in.
	PIN string `json:"pin"`
	// LeaderPIN joins as a leader. With WaitForLeader the others wait, hearing music, until a leader
	// is in; when the last leader leaves, the room closes.
	LeaderPIN     string `json:"leaderPin"`
	WaitForLeader bool   `json:"waitForLeader"`
	// MuteMembers joins everyone but leaders muted (a briefing or a lecture); a member presses * to
	// ask to speak, and a leader unmutes them from the web interface.
	MuteMembers bool `json:"muteMembers"`
	// MaxMembers in the room at once (default 50).
	MaxMembers int `json:"maxMembers"`
	// Record records the room as one recording.
	Record bool `json:"record"`
	// Quiet leaves out the tones played when someone joins or leaves.
	Quiet bool `json:"quiet"`
}

// cleanDial drops the spaces, dashes, dots and brackets people write numbers with.
func cleanDial(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', '\u00a0':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}

// FindConference returns the conference room with a number, or nil.
func (c *Config) FindConference(number string) *Conference {
	for i := range c.Conferences {
		if c.Conferences[i].Number == number {
			return &c.Conferences[i]
		}
	}
	return nil
}

// FeatureCodes are what people dial on their phones to change their own settings and reach features
// ("off" turns one off). Codes followed by a number (directed pickup, intercom, speed dial) are prefixes.
type FeatureCodes struct {
	ForwardAll         string `json:"forwardAll"`         // *72, then the number and #
	ForwardAllOff      string `json:"forwardAllOff"`      // *73
	ForwardBusy        string `json:"forwardBusy"`        // *90
	ForwardBusyOff     string `json:"forwardBusyOff"`     // *91
	ForwardNoAnswer    string `json:"forwardNoAnswer"`    // *52
	ForwardNoAnswerOff string `json:"forwardNoAnswerOff"` // *53
	DNDOn              string `json:"dndOn"`              // *78
	DNDOff             string `json:"dndOff"`             // *79
	DNDToggle          string `json:"dndToggle"`          // *76
	FollowMe           string `json:"followMe"`           // *21 switches follow-me on and off
	Pickup             string `json:"pickup"`             // *8 answers a call ringing in your pickup group
	DirectedPickup     string `json:"directedPickup"`     // ** then the extension
	Intercom           string `json:"intercom"`           // *80 then the extension
	SpeedDial          string `json:"speedDial"`          // *0 then the code
}

// Door opens a door from a call with a door station: the person answering presses Code, or the button on
// the screen, and BitVoice calls the door controller's URL (2N, Axis, Akuvox, a relay board). With no URL,
// the key presses go through to the door station, which opens the door itself.
type Door struct {
	Code     string `json:"code"`     // keys pressed during the call, such as *1
	URL      string `json:"url"`      // http(s) address that opens the door
	Method   string `json:"method"`   // GET or POST
	Username string `json:"username"` // basic authentication for the URL, if it needs it
	Password string `json:"password"`
}

// Voicemail is a mailbox's settings.
type Voicemail struct {
	Enabled bool `json:"enabled"`
	// PIN is asked for when messages are collected from a phone other than the extension's own. Digits.
	PIN string `json:"pin"`
	// MaxSeconds is the longest message a caller can leave.
	MaxSeconds int `json:"maxSeconds"`
	// Email sends each new message to the extension's email address.
	Email bool `json:"email"`
}

// RingGroup rings several extensions.
type RingGroup struct {
	ID          string       `json:"id"`
	Number      string       `json:"number"`
	Name        string       `json:"name"`
	Strategy    string       `json:"strategy"` // all or sequential
	Members     []string     `json:"members"`  // extension numbers
	RingSeconds int          `json:"ringSeconds"`
	NoAnswer    *Destination `json:"noAnswer,omitempty"`
}

// Trunk is a SIP connection to a carrier or another system.
type Trunk struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Transport string `json:"transport"` // udp or tcp
	Username  string `json:"username"`
	AuthUser  string `json:"authUser"`
	Password  string `json:"password"`
	Register  bool   `json:"register"`
	// FromDomain overrides the domain in From on outbound calls; empty uses Host.
	FromDomain  string `json:"fromDomain"`
	CallerID    string `json:"callerId"`
	MaxChannels int    `json:"maxChannels"` // 0 for no limit
	// Rates price calls out through this trunk, by the number's first digits; InboundPerMinute prices
	// calls in (toll-free numbers).
	Rates            []Rate  `json:"rates"`
	InboundPerMinute float64 `json:"inboundPerMinute"`
	// AllowFrom lists extra addresses that may send calls in on this trunk (carriers often use several).
	AllowFrom []string `json:"allowFrom"`
	// SecureMedia offers SRTP on calls out through this trunk.
	SecureMedia bool `json:"secureMedia"`
	// Teams makes this trunk Microsoft Teams Direct Routing, with BitVoice as the SBC: TLS to
	// Microsoft's three SIP proxies in turn, SRTP, and Contact and From carrying SBCName.
	Teams bool `json:"teams"`
	// SBCName is this server's public name, as given to New-CsOnlinePSTNGateway -Fqdn (a name in a
	// domain the Microsoft 365 tenant has verified), and covered by the TLS certificate.
	SBCName string `json:"sbcName"`
	// Shared says how servers sharing one database use this trunk: "" (each server registers the
	// account and sends its own calls) or "one" (one server at a time registers it and carries every
	// server's calls on it; another takes over if it stops). Use "one" for providers that allow a
	// single registration or a single address.
	Shared string `json:"shared"`
	// RichCallData sends the caller ID's brand (logo and call reason, RFC 9796 Call-Info) on calls out,
	// for carriers that pass it to the called phone.
	RichCallData bool `json:"richCallData"`
	// Codecs this trunk may use, best first; empty uses the system's list.
	Codecs []string `json:"codecs"`
	// ESInet marks a trunk from the emergency services network: calls on it are 911 calls, with
	// their location and Additional Data shown to the call-taker.
	ESInet bool `json:"esinet"`
}

// InboundRoute sends calls arriving on trunks to a destination.
type InboundRoute struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	DID         string      `json:"did"`   // empty matches any number
	Trunk       string      `json:"trunk"` // trunk ID, empty for any
	Destination Destination `json:"destination"`
}

// OutboundRoute sends dialled numbers matching a pattern to trunks, in failover order.
type OutboundRoute struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"` // dial patterns: X N Z digits, [1-5] sets, . for "one or more"
	Strip    int      `json:"strip"`    // digits removed from the front
	Prepend  string   `json:"prepend"`  // digits added to the front after stripping
	Trunks   []string `json:"trunks"`   // trunk IDs in order
}

// PageGroup is a number that makes every member's phone answer on its speaker and play the caller: an
// announcement to an office or a warehouse.
type PageGroup struct {
	ID      string   `json:"id"`
	Number  string   `json:"number"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
	// Multicast also sends the page, as G.711 µ-law RTP, to this multicast address and port
	// (224.0.0.0/4), which overhead speakers and many desk phones can listen to without a call each.
	Multicast string `json:"multicast"`
}

// FindPageGroup returns the paging group with a number, or nil.
func (c *Config) FindPageGroup(number string) *PageGroup {
	for i := range c.PageGroups {
		if c.PageGroups[i].Number == number {
			return &c.PageGroups[i]
		}
	}
	return nil
}

// Queue holds callers until an agent is free (a contact-centre queue, ACD).
type Queue struct {
	ID     string   `json:"id"`
	Number string   `json:"number"`
	Name   string   `json:"name"`
	Agents []string `json:"agents"` // extension numbers
	// Strategy picks who rings: longest-idle (the agent free longest), round-robin, or ring-all.
	Strategy string `json:"strategy"`
	// RingSeconds is how long one agent is rung before the next is tried.
	RingSeconds int `json:"ringSeconds"`
	// WrapUpSeconds gives an agent time after a queue call before the next one.
	WrapUpSeconds int `json:"wrapUpSeconds"`
	// MaxWaitSeconds sends a caller to Timeout after waiting this long; 0 waits until they hang up.
	MaxWaitSeconds int `json:"maxWaitSeconds"`
	// Timeout is voicemail or hangup.
	Timeout *Destination `json:"timeout,omitempty"`
	// Sticky offers a returning caller first to the agent who last answered them (within StickyDays,
	// 30 when unset), if that agent is free; if they do not answer, the queue goes on as usual.
	Sticky     bool `json:"sticky"`
	StickyDays int  `json:"stickyDays"`
}

// CallLink is a web address (and QR code, and website button) that calls a destination from a
// browser: no phone number, no app.
type CallLink struct {
	ID      string `json:"id"`
	Name    string `json:"name"`    // Support line
	Slug    string `json:"slug"`    // the unguessable part of the address
	Enabled bool   `json:"enabled"` // off: the address says the line is closed
	// Title and Intro are what the caller sees: "Call Acme support", "We answer 9 to 5".
	Title string `json:"title"`
	Intro string `json:"intro"`
	// AskName asks the caller's name first (shown to whoever answers).
	AskName     bool        `json:"askName"`
	Destination Destination `json:"destination"`
	// MaxCalls at once through this link; 5 when unset.
	MaxCalls int `json:"maxCalls"`
}

// Texting is business text messaging: the providers, and which of our numbers each one carries.
type Texting struct {
	Providers []SMSProvider `json:"providers"`
	Numbers   []SMSNumber   `json:"numbers"`
	// TenDLC tracks US 10DLC registration (the brand and campaign carriers require for texting from
	// ordinary US numbers), done in the provider's dashboard.
	TenDLC TenDLC `json:"tenDlc"`
}

// SMSProvider is one texting service account.
type SMSProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`     // textmagic, twilio, signalwire, telnyx, bandwidth, vonage, plivo, flowroute, voipms, webhook
	Username string `json:"username"` // the account's user name
	Key      string `json:"key"`      // the API key
	// HookSecret is the secret part of the address the provider posts received texts to.
	HookSecret string `json:"hookSecret"`
	// BaseURL overrides the provider's API address (a sandbox); empty for the real one. SignalWire's
	// space address and a webhook's address go here too.
	BaseURL string `json:"baseUrl"`
	// Account and App are a provider's account id and application id (Bandwidth).
	Account string `json:"account,omitempty"`
	App     string `json:"app,omitempty"`
}

// SMSNumber is one of our numbers that sends and receives texts.
type SMSNumber struct {
	Number   string `json:"number"`   // +12125550100
	Provider string `json:"provider"` // the SMSProvider ID that carries it
	Name     string `json:"name"`     // Main line
	// Notify are the extensions told of each text received (by email, when they have one).
	Notify []string `json:"notify"`
}

// TenDLC records where 10DLC registration stands, for the checklist on Texts.
type TenDLC struct {
	Brand    string `json:"brand"`    // not-started, submitted, approved
	Campaign string `json:"campaign"` // not-started, submitted, approved
	UseCase  string `json:"useCase"`  // customer care, appointment reminders ...
	Sample   string `json:"sample"`   // a sample message, with opt-out words
}

// Screening is spam protection for calls from outside.
type Screening struct {
	// Failed is what happens when the caller ID fails verification: allow, label (default) or block.
	Failed string `json:"failed"`
	// Unverified is for calls with no verification or only attestation C: allow (default) or label.
	Unverified string `json:"unverified"`
	// Anonymous is for withheld numbers: allow (default), label or block.
	Anonymous string `json:"anonymous"`
	// Label goes before the caller's name on phones when a call is labelled ("Spam?" when unset).
	Label string `json:"label"`
	// Blocked numbers are refused (a number, or digits then * for every number starting so);
	// Allowed ones are never blocked or labelled.
	Blocked []ScreenNumber `json:"blocked"`
	Allowed []ScreenNumber `json:"allowed"`
	// STIRoots are the STI certificate authorities (PEM) a caller's certificate must chain to when the
	// call carries a signed Identity header; empty checks the signature only.
	STIRoots string `json:"stiRoots"`
}

// ScreenNumber is a number (or a prefix ending in *) on a block or allow list.
type ScreenNumber struct {
	Number string    `json:"number"`
	Note   string    `json:"note"`
	Added  time.Time `json:"added,omitzero"`
	By     string    `json:"by,omitempty"`
}

// Matches says whether a caller's number is this entry.
func (s ScreenNumber) Matches(number string) bool {
	if p, ok := strings.CutSuffix(s.Number, "*"); ok {
		d := digitsOnly(number)
		return p != "" && strings.HasPrefix(d, digitsOnly(p))
	}
	return SameNumber(s.Number, number)
}

// VIP is a caller who goes to the front of every queue.
type VIP struct {
	Number string `json:"number"`
	Name   string `json:"name"`
}

// Script decides a call's destination in JavaScript or Lua. When it fails, runs too long or has no
// opinion, the call goes to Fallback: a script can never lose a call.
type Script struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Language string      `json:"language"` // js or lua
	Source   string      `json:"source"`
	Fallback Destination `json:"fallback"`
}

// FindScript returns the script with an ID, or nil.
func (c *Config) FindScript(id string) *Script {
	for i := range c.Scripts {
		if c.Scripts[i].ID == id {
			return &c.Scripts[i]
		}
	}
	return nil
}

// ScriptCheck compiles a script; set by the script package so config stays free of interpreters.
var ScriptCheck func(language, source string) error

// Menu is an auto attendant: callers hear a recorded prompt ("press 1 for sales") and choose with a key.
type Menu struct {
	ID      string       `json:"id"`
	Number  string       `json:"number"` // optional: dial it from an extension
	Name    string       `json:"name"`
	Options []MenuOption `json:"options"`
	// DialExtensions lets callers type an extension number at the prompt.
	DialExtensions bool `json:"dialExtensions"`
	// TimeoutSeconds to wait for a key after the prompt.
	TimeoutSeconds int `json:"timeoutSeconds"`
	// Repeats is how many times the prompt plays before giving up.
	Repeats int `json:"repeats"`
	// Timeout is where callers who press nothing go; nil says goodbye.
	Timeout *Destination `json:"timeout,omitempty"`
}

// MenuOption is one key of a menu.
type MenuOption struct {
	Key         string      `json:"key"` // 0-9, * or #
	Label       string      `json:"label"`
	Destination Destination `json:"destination"`
}

// FindMenu returns the menu with an ID, or nil.
func (c *Config) FindMenu(id string) *Menu {
	for i := range c.Menus {
		if c.Menus[i].ID == id {
			return &c.Menus[i]
		}
	}
	return nil
}

// FindMenuNumber returns the menu with a dialable number, or nil.
func (c *Config) FindMenuNumber(number string) *Menu {
	for i := range c.Menus {
		if c.Menus[i].Number != "" && c.Menus[i].Number == number {
			return &c.Menus[i]
		}
	}
	return nil
}

// Schedule sends calls one way during opening hours and another way outside them (and on holidays).
type Schedule struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Hours    []OpenHours `json:"hours"`
	Holidays []Holiday   `json:"holidays"`
	Open     Destination `json:"open"`
	Closed   Destination `json:"closed"`
}

// OpenHours is a time range on some weekdays, in the system time zone. Days are 0 (Sunday) to 6.
type OpenHours struct {
	Days  []int  `json:"days"`
	Start string `json:"start"` // HH:MM
	End   string `json:"end"`   // HH:MM; at or before Start, the hours run past midnight into the next day
}

// Holiday is a closed day.
type Holiday struct {
	Date string `json:"date"` // YYYY-MM-DD
	Name string `json:"name"`
}

// IsOpen reports whether the schedule is open at t (already in the right time zone).
func (s *Schedule) IsOpen(t time.Time) bool {
	day := t.Format("2006-01-02")
	for _, h := range s.Holidays {
		if h.Date == day {
			return false
		}
	}
	now := t.Format("15:04")
	today, yesterday := int(t.Weekday()), (int(t.Weekday())+6)%7
	for _, h := range s.Hours {
		if h.End > h.Start {
			if slices.Contains(h.Days, today) && now >= h.Start && now < h.End {
				return true
			}
			continue
		}
		// Overnight (22:00 to 06:00): open from Start on the listed day until End the next morning.
		if (slices.Contains(h.Days, today) && now >= h.Start) || (slices.Contains(h.Days, yesterday) && now < h.End) {
			return true
		}
	}
	return false
}

// FindSchedule returns the schedule with an ID, or nil.
func (c *Config) FindSchedule(id string) *Schedule {
	for i := range c.Schedules {
		if c.Schedules[i].ID == id {
			return &c.Schedules[i]
		}
	}
	return nil
}

// Location returns the system time zone, UTC when unset or unknown.
func (s *System) Location() *time.Location {
	if s.Timezone != "" {
		if l, err := time.LoadLocation(s.Timezone); err == nil {
			return l
		}
	}
	return time.UTC
}

// Destination is where a call goes.
type Destination struct {
	Type   string `json:"type"`   // extension, ringgroup, queue, menu, schedule, script, voicemail, echo, hangup, fax, esrp, conference
	Target string `json:"target"` // a number for extension and ringgroup
	// Via is set only inside the engine: the extension whose own forwarding a "forward" destination is.
	Via string `json:"via,omitempty"`
}

// Security holds abuse protection.
type Security struct {
	// FailBanThreshold failed registrations from one address within FailBanWindowSecs ban it for
	// FailBanMinutes.
	FailBanThreshold  int      `json:"failBanThreshold"`
	FailBanWindowSecs int      `json:"failBanWindowSecs"`
	FailBanMinutes    int      `json:"failBanMinutes"`
	TrustedNetworks   []string `json:"trustedNetworks"` // never banned
	// BlockPremium refuses calls to premium-rate and high-fraud destinations unless a route says otherwise.
	BlockPremium bool `json:"blockPremium"`
	// MaxCallsPerExtension bounds concurrent calls (a compromised phone cannot run up a bill).
	MaxCallsPerExtension int `json:"maxCallsPerExtension"`
}

// Default returns the configuration of a fresh installation.
func Default() Config {
	return Config{
		Schema: Schema,
		System: System{
			Name: "BitVoice", Purpose: "office", Country: "US", Language: "en",
			SIPPort: 5060, RTPMin: 10000, RTPMax: 20000, EchoNumber: "*43", VoicemailNumber: "*97", ParkNumber: "70", ParkSeconds: 120,
			EmergencyNumbers: []string{"911", "933"},
			Audio:            Audio{Voicemail: true},
		},
		Extensions:     []Extension{},
		RingGroups:     []RingGroup{},
		Trunks:         []Trunk{},
		InboundRoutes:  []InboundRoute{},
		OutboundRoutes: []OutboundRoute{},
		Security: Security{FailBanThreshold: 10, FailBanWindowSecs: 60, FailBanMinutes: 60, BlockPremium: true,
			MaxCallsPerExtension: 4, TrustedNetworks: []string{"127.0.0.0/8", "::1/128"}},
	}
}

// Clone deep-copies a configuration.
func (c Config) Clone() Config {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	var out Config
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	fillNilSlices(&out)
	if len(out.System.Codecs) == 0 {
		out.System.Codecs = slices.Clone(DefaultCodecs) // configurations from before codecs were chosen
	}
	return out
}

// NewID returns a short random identifier.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewSecret returns a SIP password that resists guessing: 20 characters from an unambiguous alphabet.
func NewSecret() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

var (
	numberRe  = regexp.MustCompile(`^[0-9]{2,8}$`)
	featureRe = regexp.MustCompile(`^\*[0-9]{1,6}$`)
	// prefixRe is a code dialled before a number: ** or *80.
	prefixRe  = regexp.MustCompile(`^\*[0-9*]{1,4}$`)
	pinRe     = regexp.MustCompile(`^[0-9]{4,10}$`)
	speedRe   = regexp.MustCompile(`^[0-9]{1,3}$`)
	dialRe    = regexp.MustCompile(`^\+?[0-9]{2,20}$`)
	didRe     = regexp.MustCompile(`^\+?[0-9]{2,20}$`)
	patternRe = regexp.MustCompile(`^\+?([0-9XNZ*#]|\[[0-9-]+\])+\.?$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
)

// Problem is one validation failure, with the field it concerns so the GUI can mark it.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Field + ": " + p.Message }

// Problems is a list of validation failures and is an error when non-empty.
type Problems []Problem

func (p Problems) Error() string {
	parts := make([]string, len(p))
	for i, x := range p {
		parts[i] = x.String()
	}
	return strings.Join(parts, "; ")
}

func (p *Problems) add(field, format string, args ...any) {
	*p = append(*p, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
}

// Validate checks the whole configuration and fills in defaults. It returns nil or Problems.
func (c *Config) Validate() error {
	fillNilSlices(c)
	var p Problems
	s := &c.System
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		p.add("system.name", "give the system a name")
	}
	if !slices.Contains([]string{"office", "contact-centre", "911"}, s.Purpose) {
		p.add("system.purpose", "choose office, contact-centre or 911")
	}
	if s.SIPPort < 1 || s.SIPPort > 65535 {
		p.add("system.sipPort", "must be a port number between 1 and 65535")
	}
	if s.RTPMin < 1024 || s.RTPMax > 65535 || s.RTPMax-s.RTPMin < 100 {
		p.add("system.rtpMin", "media ports must be a range of at least 100 between 1024 and 65535")
	}
	if s.SIPPort >= s.RTPMin && s.SIPPort <= s.RTPMax {
		p.add("system.sipPort", "SIP port is inside the media port range")
	}
	if len(c.System.Codecs) == 0 {
		c.System.Codecs = slices.Clone(DefaultCodecs)
	}
	checkCodecs(&p, "system.codecs", c.System.Codecs, false)
	c.validateCompat(&p)
	c.validateSSO(&p)
	c.validateCRM(&p)
	c.validateMetrics(&p)
	c.validateCosts(&p)
	c.validateHotDesk(&p)
	c.validateConsent(&p)
	c.validateVIPs(&p)
	c.validateScreening(&p)
	c.validateTexting(&p)
	c.validateCallLinks(&p)
	c.validateCalendars(&p)
	c.validateBrands(&p)
	c.validateCluster(&p)
	c.validateMobile(&p)
	c.validateGitOps(&p)
	c.validateSites(&p)
	if s.PublicAddress != "" {
		if _, err := netip.ParseAddr(s.PublicAddress); err != nil && !hostRe.MatchString(s.PublicAddress) {
			p.add("system.publicAddress", "must be an IP address or host name")
		}
	}
	if s.EchoNumber != "" && !featureRe.MatchString(s.EchoNumber) && !numberRe.MatchString(s.EchoNumber) {
		p.add("system.echoNumber", "use digits, optionally starting with *")
	}
	if s.VoicemailNumber != "" && !featureRe.MatchString(s.VoicemailNumber) && !numberRe.MatchString(s.VoicemailNumber) {
		p.add("system.voicemailNumber", "use digits, optionally starting with *")
	} else if s.VoicemailNumber != "" && s.VoicemailNumber == s.EchoNumber {
		p.add("system.voicemailNumber", "the echo test already uses %s", s.EchoNumber)
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			p.add("system.timezone", "unknown time zone %s", s.Timezone)
		}
	}
	switch s.Recording.Mode {
	case "":
		s.Recording.Mode = "off"
	case "off", "all", "selected":
	default:
		p.add("system.recording.mode", "choose off, all or selected")
	}
	if s.Recording.KeepDays < 0 || s.Recording.KeepDays > 3650 {
		p.add("system.recording.keepDays", "keep recordings 0 (for ever) to 3650 days")
	}
	if s.Recording.PauseCode == "" {
		s.Recording.PauseCode = "*7"
	}
	if s.Recording.ResumeCode == "" {
		s.Recording.ResumeCode = "*8"
	}
	if !dtmfCode.MatchString(s.Recording.PauseCode) {
		p.add("system.recording.pauseCode", "use 1 to 6 keys: digits, * and #")
	}
	if !dtmfCode.MatchString(s.Recording.ResumeCode) {
		p.add("system.recording.resumeCode", "use 1 to 6 keys: digits, * and #")
	} else if s.Recording.ResumeCode == s.Recording.PauseCode {
		p.add("system.recording.resumeCode", "use a different code from pausing")
	}
	for f, d := range map[string]int{"callRecordDays": s.Retention.CallRecordDays, "voicemailDays": s.Retention.VoicemailDays} {
		if d < 0 || d > 36500 {
			p.add("system.retention."+f, "use 0 to keep everything, or 1 to 36500 days")
		}
	}
	for i, n := range s.EmergencyNumbers {
		if !regexp.MustCompile(`^[0-9]{2,5}$`).MatchString(n) {
			p.add(fmt.Sprintf("system.emergencyNumbers[%d]", i), "emergency numbers are 2 to 5 digits")
		}
	}

	parkNumbers := map[string]bool{}
	if pn, err := strconv.Atoi(s.ParkNumber); err == nil && s.ParkNumber != "" {
		for i := 0; i <= 9; i++ {
			parkNumbers[strconv.Itoa(pn+i)] = true
		}
	} else if s.ParkNumber != "" {
		p.add("system.parkNumber", "the park number is digits, such as 70")
	}
	if s.ParkSeconds < 0 || s.ParkSeconds > 3600 {
		p.add("system.parkSeconds", "a call can stay parked 0 to 3600 seconds")
	}
	type claimed struct{ field, owner string }
	numbers := map[string]claimed{} // number -> who has it, for uniqueness across everything dialable
	claim := func(field, number, owner string) {
		if prev, ok := numbers[number]; ok {
			// Both sides hear about it, so whichever of the two is being edited shows the problem.
			p.add(field, "number %s is already used by %s", number, prev.owner)
			p.add(prev.field, "number %s is also used by %s", number, owner)
			return
		}
		if sysField, what := reservedBy(s, parkNumbers, number); sysField != "" {
			// Say it on both sides: on the item, and on the setting in Settings that holds the number.
			p.add(field, "number %s is the %s number (Settings)", number, what)
			p.add(sysField, "%s is already %s", number, owner)
			return
		}
		numbers[number] = claimed{field, owner}
	}
	ids := map[string]bool{}
	checkID := func(field string, id *string) {
		if *id == "" {
			*id = NewID()
		}
		if ids[*id] {
			p.add(field, "duplicate id %s", *id)
		}
		ids[*id] = true
	}

	for _, ac := range []struct {
		field string
		v     *string
		def   string
		what  string
	}{{"system.agentLogin", &c.System.AgentLogin, "*45", "the agent log in code"}, {"system.agentPause", &c.System.AgentPause, "*46", "the agent pause code"},
		{"system.hotDesk.login", &c.System.HotDesk.Login, "*33", "the hot desk sign-in code"}, {"system.hotDesk.logout", &c.System.HotDesk.Logout, "*34", "the hot desk sign-out code"}} {
		if *ac.v == "" {
			*ac.v = ac.def
		}
		if *ac.v == "off" {
			continue
		}
		if !featureRe.MatchString(*ac.v) {
			p.add(ac.field, "use a feature code such as %s, or off", ac.def)
		} else {
			claim(ac.field, *ac.v, ac.what)
		}
	}
	if code := strings.TrimSpace(c.System.EmergencyMode.Code); code != "" {
		c.System.EmergencyMode.Code = code
		if !featureRe.MatchString(code) {
			p.add("system.emergencyMode.code", "use a feature code such as *55")
		} else {
			claim("system.emergencyMode.code", code, "the emergency mode switch")
		}
	}
	for i := range c.Extensions {
		e := &c.Extensions[i]
		f := fmt.Sprintf("extensions[%d]", i)
		checkID(f+".id", &e.ID)
		if !numberRe.MatchString(e.Number) {
			p.add(f+".number", "extension numbers are 2 to 8 digits")
		} else {
			claim(f+".number", e.Number, strings.TrimSpace("extension "+e.Number+" "+e.Name))
		}
		e.Name = strings.TrimSpace(e.Name)
		checkDevice(&p, f, e)
		if e.Name == "" {
			p.add(f+".name", "enter a name")
		}
		e.CallerID = strings.TrimSpace(e.CallerID)
		if e.CallerID != "" && !callerIDRe.MatchString(e.CallerID) {
			p.add(f+".callerId", "a caller ID is a phone number: digits, optionally starting with +")
		}
		if e.Secret == "" {
			e.Secret = NewSecret()
		}
		if len(e.Secret) < 12 {
			p.add(f+".secret", "SIP passwords must be at least 12 characters (generate one)")
		}
		if e.RingSeconds == 0 {
			e.RingSeconds = 25
		}
		if e.RingSeconds < 5 || e.RingSeconds > 300 {
			p.add(f+".ringSeconds", "ring time must be 5 to 300 seconds")
		}
		if e.Voicemail.MaxSeconds == 0 {
			e.Voicemail.MaxSeconds = 180
		}
		if e.Voicemail.MaxSeconds < 10 || e.Voicemail.MaxSeconds > 900 {
			p.add(f+".voicemail.maxSeconds", "messages can be 10 to 900 seconds long")
		}
		if e.Voicemail.PIN != "" && !regexp.MustCompile(`^[0-9]{4,10}$`).MatchString(e.Voicemail.PIN) {
			p.add(f+".voicemail.pin", "the PIN is 4 to 10 digits")
		}
		if a, err := mail.ParseAddress(e.Email); e.Email != "" && (err != nil || a.Address != strings.TrimSpace(e.Email) || strings.ContainsAny(e.Email, "\r\n,;")) {
			p.add(f+".email", "not an email address")
		}
	}
	for i := range c.RingGroups {
		g := &c.RingGroups[i]
		f := fmt.Sprintf("ringGroups[%d]", i)
		checkID(f+".id", &g.ID)
		if !numberRe.MatchString(g.Number) {
			p.add(f+".number", "ring group numbers are 2 to 8 digits")
		} else {
			claim(f+".number", g.Number, "ring group "+g.Name)
		}
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if g.Strategy == "" {
			g.Strategy = "all"
		}
		if g.Strategy != "all" && g.Strategy != "sequential" {
			p.add(f+".strategy", "choose all or sequential")
		}
		if g.RingSeconds == 0 {
			g.RingSeconds = 25
		}
		if g.RingSeconds < 5 || g.RingSeconds > 300 {
			p.add(f+".ringSeconds", "ring time must be 5 to 300 seconds")
		}
		if len(g.Members) == 0 {
			p.add(f+".members", "add at least one member")
		}
	}
	for i := range c.PageGroups {
		g := &c.PageGroups[i]
		f := fmt.Sprintf("pageGroups[%d]", i)
		checkID(f+".id", &g.ID)
		if !numberRe.MatchString(g.Number) {
			p.add(f+".number", "paging numbers are 2 to 8 digits")
		} else {
			claim(f+".number", g.Number, "paging group "+g.Name)
		}
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if len(g.Members) == 0 && strings.TrimSpace(g.Multicast) == "" {
			p.add(f+".members", "add at least one phone, or a multicast group")
		}
	}
	for i := range c.Conferences {
		r := &c.Conferences[i]
		f := fmt.Sprintf("conferences[%d]", i)
		checkID(f+".id", &r.ID)
		if !numberRe.MatchString(r.Number) {
			p.add(f+".number", "conference numbers are 2 to 8 digits")
		} else {
			claim(f+".number", r.Number, "conference room "+r.Name)
		}
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			p.add(f+".name", "enter a name")
		}
		for _, pin := range []struct{ field, v string }{{".pin", r.PIN}, {".leaderPin", r.LeaderPIN}} {
			if pin.v != "" && !pinRe.MatchString(pin.v) {
				p.add(f+pin.field, "a PIN is 4 to 10 digits")
			}
		}
		if r.PIN != "" && r.PIN == r.LeaderPIN {
			p.add(f+".leaderPin", "the leader PIN must differ from the PIN everyone else uses")
		}
		if r.WaitForLeader && r.LeaderPIN == "" {
			p.add(f+".leaderPin", "set a leader PIN, or the room would wait for a leader who can never join")
		}
		if r.MaxMembers == 0 {
			r.MaxMembers = 50
		}
		if r.MaxMembers < 2 || r.MaxMembers > 200 {
			p.add(f+".maxMembers", "a room holds 2 to 200 people")
		}
	}
	checkSpeedDials := func(field string, list []SpeedDial) {
		seen := map[string]bool{}
		for j := range list {
			sd := &list[j]
			g := fmt.Sprintf("%s[%d]", field, j)
			sd.Code, sd.Number, sd.Name = strings.TrimSpace(sd.Code), cleanDial(sd.Number), strings.TrimSpace(sd.Name)
			switch {
			case !speedRe.MatchString(sd.Code):
				p.add(g+".code", "a speed dial code is 1 to 3 digits")
			case seen[sd.Code]:
				p.add(g+".code", "code %s is used twice", sd.Code)
			}
			seen[sd.Code] = true
			if !dialRe.MatchString(sd.Number) {
				p.add(g+".number", "enter the number to call: digits, optionally starting with +")
			}
		}
	}
	checkSpeedDials("system.speedDials", c.System.SpeedDials)
	for i := range c.Extensions {
		x := &c.Extensions[i]
		f := fmt.Sprintf("extensions[%d]", i)
		checkSpeedDials(f+".speedDials", x.SpeedDials)
		x.PickupGroup = strings.TrimSpace(x.PickupGroup)
		if len(x.PickupGroup) > 40 {
			p.add(f+".pickupGroup", "keep the pickup group name under 40 characters")
		}
		fm := &x.FollowMe
		if fm.Strategy == "" {
			fm.Strategy = "sequential"
		}
		if fm.Strategy != "sequential" && fm.Strategy != "together" {
			p.add(f+".followMe.strategy", "choose sequential or together")
		}
		if fm.RingSeconds == 0 {
			fm.RingSeconds = 20
		}
		if fm.RingSeconds < 5 || fm.RingSeconds > 120 {
			p.add(f+".followMe.ringSeconds", "each number rings 5 to 120 seconds")
		}
		for j := range fm.Numbers {
			fm.Numbers[j] = cleanDial(fm.Numbers[j])
			n := fm.Numbers[j]
			switch {
			case !dialRe.MatchString(n):
				p.add(fmt.Sprintf("%s.followMe.numbers[%d]", f, j), "enter a number: digits, optionally starting with +")
			case n == x.Number:
				p.add(fmt.Sprintf("%s.followMe.numbers[%d]", f, j), "follow-me already rings this extension first")
			case c.FindExtension(n) == nil && !x.Outbound:
				p.add(fmt.Sprintf("%s.followMe.numbers[%d]", f, j), "%s is an outside number, and this extension may not call outside numbers", n)
			}
		}
		if fm.Enabled && len(fm.Numbers) == 0 {
			p.add(f+".followMe.numbers", "add a number to follow you to, or switch follow-me off")
		}
		if len(fm.Numbers) > 10 {
			p.add(f+".followMe.numbers", "follow-me rings at most 10 numbers")
		}
	}
	codes := &c.System.Codes
	for _, fc := range []struct {
		field  string
		v      *string
		def    string
		what   string
		prefix bool
	}{
		{"system.codes.forwardAll", &codes.ForwardAll, "*72", "the forward-all-calls code", false},
		{"system.codes.forwardAllOff", &codes.ForwardAllOff, "*73", "the cancel-forwarding code", false},
		{"system.codes.forwardBusy", &codes.ForwardBusy, "*90", "the forward-when-busy code", false},
		{"system.codes.forwardBusyOff", &codes.ForwardBusyOff, "*91", "the cancel-forward-when-busy code", false},
		{"system.codes.forwardNoAnswer", &codes.ForwardNoAnswer, "*52", "the forward-when-not-answered code", false},
		{"system.codes.forwardNoAnswerOff", &codes.ForwardNoAnswerOff, "*53", "the cancel-forward-when-not-answered code", false},
		{"system.codes.dndOn", &codes.DNDOn, "*78", "the do-not-disturb code", false},
		{"system.codes.dndOff", &codes.DNDOff, "*79", "the do-not-disturb-off code", false},
		{"system.codes.dndToggle", &codes.DNDToggle, "*76", "the do-not-disturb switch", false},
		{"system.codes.followMe", &codes.FollowMe, "*21", "the follow-me switch", false},
		{"system.codes.pickup", &codes.Pickup, "*8", "the call pickup code", false},
		{"system.codes.directedPickup", &codes.DirectedPickup, "**", "the directed pickup code", true},
		{"system.codes.intercom", &codes.Intercom, "*80", "the intercom code", true},
		{"system.codes.speedDial", &codes.SpeedDial, "*0", "the speed dial code", true},
	} {
		*fc.v = strings.TrimSpace(*fc.v)
		if *fc.v == "" {
			*fc.v = fc.def
		}
		if *fc.v == "off" {
			continue
		}
		if fc.prefix {
			if !prefixRe.MatchString(*fc.v) {
				p.add(fc.field, "use a code such as %s, or off", fc.def)
			}
			continue
		}
		if !featureRe.MatchString(*fc.v) {
			p.add(fc.field, "use a feature code such as %s, or off", fc.def)
		} else {
			claim(fc.field, *fc.v, fc.what)
		}
	}
	for i := range c.Faxes {
		x := &c.Faxes[i]
		f := fmt.Sprintf("faxes[%d]", i)
		checkID(f+".id", &x.ID)
		if !numberRe.MatchString(x.Number) {
			p.add(f+".number", "fax numbers are 2 to 8 digits")
		} else {
			claim(f+".number", x.Number, "fax "+x.Name)
		}
		x.Name, x.Station = strings.TrimSpace(x.Name), strings.TrimSpace(x.Station)
		if x.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if len(x.Station) > 20 || strings.Trim(x.Station, "+0123456789 ") != "" {
			p.add(f+".station", "up to 20 digits, spaces and +, as fax machines show it")
		}
		for j, m := range x.Email {
			if _, err := mail.ParseAddress(m); err != nil || !strings.Contains(m, "@") {
				p.add(fmt.Sprintf("%s.email[%d]", f, j), "enter an email address")
			}
		}
		for j := range x.Senders {
			s := strings.ToLower(strings.TrimSpace(x.Senders[j]))
			x.Senders[j] = s
			if strings.HasPrefix(s, "@") {
				if len(s) < 4 || !strings.Contains(s[1:], ".") || strings.ContainsAny(s[1:], "@ ,;") {
					p.add(fmt.Sprintf("%s.senders[%d]", f, j), "a domain looks like @example.com")
				}
			} else if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
				p.add(fmt.Sprintf("%s.senders[%d]", f, j), "enter an email address, or @example.com for a whole domain")
			}
		}
	}
	if fm := &c.System.FaxMail; fm.Host != "" {
		fm.Host = strings.TrimSpace(fm.Host)
		if fm.Port == 0 {
			fm.Port = 993
		}
		if fm.Port < 1 || fm.Port > 65535 {
			p.add("system.faxMail.port", "a port is 1 to 65535; IMAP over TLS is 993")
		}
		if strings.ContainsAny(fm.Host, " /:@") {
			p.add("system.faxMail.host", "enter the mail server's name, like imap.example.com")
		}
		if fm.Username == "" {
			p.add("system.faxMail.username", "enter the mailbox's sign-in")
		}
		if strings.ContainsAny(fm.Folder, "\r\n\"") {
			p.add("system.faxMail.folder", "a folder name has no quotes or line breaks")
		}
	}
	for i, a := range c.System.Core.AllowFrom {
		a = strings.TrimSpace(a)
		c.System.Core.AllowFrom[i] = a
		if _, err := netip.ParsePrefix(a); err != nil {
			if _, err := netip.ParseAddr(a); err != nil {
				p.add(fmt.Sprintf("system.core.allowFrom[%d]", i), "an address or a range, like 10.20.0.0/16")
			}
		}
	}
	{
		r := &c.System.Radio
		sipPort := c.System.SIPPort
		if r.CADURL = strings.TrimSpace(r.CADURL); r.CADURL != "" && !strings.HasPrefix(r.CADURL, "https://") && !strings.HasPrefix(r.CADURL, "http://") {
			p.add("system.radio.cadUrl", "CAD's https address that takes the radio events")
		}
		r.Console = strings.TrimSpace(r.Console)
		if r.Console != "" && !regexp.MustCompile(`^[0-9*#]{2,8}$`).MatchString(r.Console) {
			p.add("system.radio.console", "2 to 8 digits, * or #")
		} else if r.Console != "" {
			claim("system.radio.console", r.Console, "the radio console")
		}
		ports := map[int]bool{}
		p25 := false
		checkP25 := func() {
			if strings.TrimSpace(r.P25.UnitHome) != "" {
				p25 = true // private calls need the console's identity too
			}
			if !p25 {
				return
			}
			c := &r.P25
			hex := regexp.MustCompile(`^[0-9a-fA-F]+$`)
			if len(c.WACN) != 5 || !hex.MatchString(c.WACN) {
				p.add("system.radio.p25.wacn", "the WACN: 5 hexadecimal digits, like BEE07")
			}
			if len(c.System) != 3 || !hex.MatchString(c.System) {
				p.add("system.radio.p25.system", "the system ID: 3 hexadecimal digits, like AD5")
			}
			if c.RFSS < 1 || c.RFSS > 254 {
				p.add("system.radio.p25.rfss", "this console's RFSS ID, 1 to 254")
			}
			if c.Unit < 1 || c.Unit > 0xffffff {
				p.add("system.radio.p25.unit", "the unit ID dispatchers transmit as, 1 to 16777215")
			}
			if c.SIPPort == 0 {
				c.SIPPort = 5062
			}
			if c.SIPPort < 1024 || c.SIPPort > 65535 {
				p.add("system.radio.p25.sipPort", "a port from 1024 to 65535")
			} else if c.SIPPort == sipPort || c.SIPPort == sipPort+1 {
				p.add("system.radio.p25.sipPort", "phones use this port (and SIP over TLS the next one); choose another")
			}
			if c.Priority == 0 {
				c.Priority = 4
			}
			if c.Priority < 1 || c.Priority > 4 {
				p.add("system.radio.p25.priority", "1 to 4")
			}
			seen := map[string]bool{}
			for i := range c.Keys {
				k := &c.Keys[i]
				f := fmt.Sprintf("system.radio.p25.keys[%d]", i)
				if k.ID == "" {
					k.ID = NewID()
				}
				k.Name = strings.TrimSpace(k.Name)
				k.Key = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(k.Key), " ", ""))
				n := P25KeyDigits(k.Alg)
				switch {
				case n == 0:
					p.add(f+".alg", "AES-256, DES or triple DES")
				case len(k.Key) != n || !hex.MatchString(k.Key):
					p.add(f+".key", "%d hexadecimal digits for this algorithm", n)
				}
				if k.KeyID < 0 || k.KeyID > 0xffff {
					p.add(f+".keyId", "a key ID from 0 to 65535")
				}
				if id := k.Alg + "/" + strconv.Itoa(k.KeyID); seen[id] {
					p.add(f+".keyId", "another key has this algorithm and key ID")
				} else {
					seen[id] = true
				}
			}
			if c.UnitHome = strings.TrimSpace(c.UnitHome); c.UnitHome != "" {
				if _, port, err := net.SplitHostPort(c.UnitHome); err != nil || port == "" {
					p.add("system.radio.p25.unitHome", "the SIP address and port of the radios' home RFSS, like 10.0.2.25:5060")
				}
				if c.UnitHomeRFSS < 1 || c.UnitHomeRFSS > 254 {
					p.add("system.radio.p25.unitHomeRfss", "the RFSS ID, 1 to 254")
				}
			}
			if c.UnitTxKey != "" {
				found := false
				for _, k := range c.Keys {
					found = found || k.ID == c.UnitTxKey
				}
				if !found {
					p.add("system.radio.p25.unitTxKey", "choose one of the console's keys, or transmit clear")
				}
			}
			for i, ch := range r.Channels {
				if ch.TxKey == "" {
					continue
				}
				found := false
				for _, k := range c.Keys {
					found = found || k.ID == ch.TxKey
				}
				if !found {
					p.add(fmt.Sprintf("system.radio.channels[%d].txKey", i), "choose one of the console's keys, or transmit clear")
				}
			}
		}
		for i := range r.Channels {
			ch := &r.Channels[i]
			f := fmt.Sprintf("system.radio.channels[%d]", i)
			checkID(f+".id", &ch.ID)
			ch.Name, ch.Gateway = strings.TrimSpace(ch.Name), strings.TrimSpace(ch.Gateway)
			if ch.Name == "" {
				p.add(f+".name", "name the channel")
			}
			if ch.Port < 1024 || ch.Port > 65534 || ch.Port%2 != 0 {
				p.add(f+".port", "an even port from 1024 to 65534 (RTCP takes the next one)")
			} else if ports[ch.Port] {
				p.add(f+".port", "another channel uses this port")
			} else if ch.Port >= c.System.RTPMin && ch.Port <= c.System.RTPMax+1 {
				p.add(f+".port", "this port is in the range calls use; choose one outside it")
			}
			ports[ch.Port] = true
			switch ch.Kind {
			case "", "roip":
				ch.Kind = "roip"
				if _, port, err := net.SplitHostPort(ch.Gateway); err != nil || port == "" {
					p.add(f+".gateway", "the gateway's address and port, like 10.0.0.40:5000")
				}
			case "p25":
				ch.Gateway = ""
				if ch.Group < 1 || ch.Group > 65535 {
					p.add(f+".group", "a talkgroup ID from 1 to 65535")
				}
				if _, port, err := net.SplitHostPort(strings.TrimSpace(ch.Home)); err != nil || port == "" {
					p.add(f+".home", "the home RFSS's SIP address and port, like 10.0.2.25:5060")
				}
				if ch.HomeRFSS < 1 || ch.HomeRFSS > 254 {
					p.add(f+".homeRfss", "the home RFSS's ID, 1 to 254")
				}
				p25 = true
			default:
				p.add(f+".kind", "roip or p25")
			}
		}
		checkP25()
	}
	uris := map[string]bool{}
	for i := range c.System.Core.PSAPs {
		ps := &c.System.Core.PSAPs[i]
		f := fmt.Sprintf("system.core.psaps[%d]", i)
		ps.URI, ps.Name = strings.TrimSpace(ps.URI), strings.TrimSpace(ps.Name)
		if !strings.HasPrefix(ps.URI, "sip:") && !strings.HasPrefix(ps.URI, "sips:") && !strings.HasPrefix(ps.URI, "tel:") {
			p.add(f+".uri", "a PSAP is named by a sip: URI, as the ECRF answers with it")
		} else if uris[ps.URI] {
			p.add(f+".uri", "this PSAP is listed twice")
		}
		uris[ps.URI] = true
		if c.FindTrunk(ps.Trunk) == nil {
			p.add(f+".trunk", "choose the trunk that reaches this PSAP")
		}
	}
	for i, ps := range c.System.Core.PSAPs {
		if ps.Overflow != "" && !uris[ps.Overflow] {
			p.add(fmt.Sprintf("system.core.psaps[%d].overflow", i), "overflow goes to another PSAP in this list")
		}
	}
	if d := c.System.Core.Default; d != "" && !uris[d] {
		p.add("system.core.default", "the default PSAP must be one in the list")
	}
	if u := c.System.Core.LoSTURL; u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		p.add("system.core.lostUrl", "enter the ECRF's https address")
	}
	if c.System.Core.ECRF && len(c.System.Core.AllowFrom) == 0 {
		p.add("system.core.allowFrom", "say which addresses may ask (your ESInet), so LoST is not open to everyone")
	}
	for i := range c.System.Core.Rules {
		ru := &c.System.Core.Rules[i]
		f := fmt.Sprintf("system.core.rules[%d]", i)
		checkID(f+".id", &ru.ID)
		ru.Name = strings.TrimSpace(ru.Name)
		if ru.Name == "" {
			p.add(f+".name", "name the rule")
		}
		for j, s := range ru.Services {
			ru.Services[j] = strings.ToLower(strings.TrimSpace(s))
			if !strings.HasPrefix(ru.Services[j], "urn:service:") {
				p.add(fmt.Sprintf("%s.services[%d]", f, j), "a service URN, like urn:service:sos.fire")
			}
		}
		for j, u := range ru.PSAPs {
			if !uris[u] {
				p.add(fmt.Sprintf("%s.psaps[%d]", f, j), "a PSAP in the list above")
			}
		}
		if ru.Schedule != "" && c.FindSchedule(ru.Schedule) == nil {
			p.add(f+".schedule", "choose one of the schedules")
		}
		for j, id := range ru.Trunks {
			if c.FindTrunk(id) == nil {
				p.add(fmt.Sprintf("%s.trunks[%d]", f, j), "choose a trunk")
			}
		}
		switch ru.Action {
		case "route":
			if !uris[ru.To] {
				p.add(f+".to", "send it to a PSAP in the list above")
			}
		case "reject":
			ru.To = ""
		default:
			p.add(f+".action", "route or reject")
		}
	}
	{
		b := &c.System.Core.BCF
		if b.PerMinute < 0 || b.PerMinute > 100000 {
			p.add("system.core.bcf.perMinute", "0 (no limit) to 100000")
		}
		if b.Concurrent < 0 || b.Concurrent > 100000 {
			p.add("system.core.bcf.concurrent", "0 (no limit) to 100000")
		}
		for i, a := range b.Deny {
			a = strings.TrimSpace(a)
			b.Deny[i] = a
			if _, err := netip.ParsePrefix(a); err != nil {
				if _, err := netip.ParseAddr(a); err != nil {
					p.add(fmt.Sprintf("system.core.bcf.deny[%d]", i), "an address or a range, like 203.0.113.0/24")
				}
			}
		}
	}
	{
		ps := &c.System.PSAP
		ps.AgencyID, ps.ALIAddress, ps.EIDOURL = strings.TrimSpace(ps.AgencyID), strings.TrimSpace(ps.ALIAddress), strings.TrimSpace(ps.EIDOURL)
		if ps.AgencyID != "" && (strings.ContainsAny(ps.AgencyID, " /@:") || !strings.Contains(ps.AgencyID, ".")) {
			p.add("system.psap.agencyId", "an agency ID is a domain, like psap.county.example.gov")
		}
		if ps.ALIAddress != "" {
			if _, port, err := net.SplitHostPort(ps.ALIAddress); err != nil || port == "" {
				p.add("system.psap.aliAddress", "enter CAD's address and port, like 10.0.0.20:4000")
			}
		}
		if ps.EIDOURL != "" && !strings.HasPrefix(ps.EIDOURL, "https://") && !strings.HasPrefix(ps.EIDOURL, "http://") {
			p.add("system.psap.eidoUrl", "enter the https address CAD takes EIDOs at")
		}
		if ps.AnswerTarget < 0 || ps.AnswerTarget > 120 {
			p.add("system.psap.answerTarget", "seconds, 1 to 120")
		}
	}
	if fp := c.System.FaxPaper; fp != "" && fp != "letter" && fp != "a4" {
		p.add("system.faxPaper", "choose letter or A4")
	}
	for i := range c.Queues {
		q := &c.Queues[i]
		f := fmt.Sprintf("queues[%d]", i)
		checkID(f+".id", &q.ID)
		if !numberRe.MatchString(q.Number) {
			p.add(f+".number", "queue numbers are 2 to 8 digits")
		} else {
			claim(f+".number", q.Number, "queue "+q.Name)
		}
		q.Name = strings.TrimSpace(q.Name)
		if q.Name == "" {
			p.add(f+".name", "enter a name")
		}
		switch q.Strategy {
		case "":
			q.Strategy = "longest-idle"
		case "longest-idle", "round-robin", "ring-all":
		default:
			p.add(f+".strategy", "choose longest-idle, round-robin or ring-all")
		}
		if q.RingSeconds == 0 {
			q.RingSeconds = 15
		}
		if q.RingSeconds < 5 || q.RingSeconds > 120 {
			p.add(f+".ringSeconds", "ring each agent 5 to 120 seconds")
		}
		if q.WrapUpSeconds < 0 || q.WrapUpSeconds > 600 {
			p.add(f+".wrapUpSeconds", "wrap-up is 0 to 600 seconds")
		}
		if q.MaxWaitSeconds < 0 || q.MaxWaitSeconds > 7200 {
			p.add(f+".maxWaitSeconds", "maximum wait is 0 (no limit) to 7200 seconds")
		}
		if len(q.Agents) == 0 {
			p.add(f+".agents", "add at least one agent")
		}
	}
	extNumbers := map[string]bool{}
	for _, e := range c.Extensions {
		extNumbers[e.Number] = true
	}
	if a := &c.System.AI; a.Enabled {
		if strings.TrimSpace(a.Model) == "" {
			p.add("system.ai.model", "choose a model")
		}
		if a.BaseURL != "" && !strings.HasPrefix(a.BaseURL, "https://") && !strings.HasPrefix(a.BaseURL, "http://127.0.0.1") && !strings.HasPrefix(a.BaseURL, "http://localhost") {
			p.add("system.ai.baseUrl", "use https, or http only to a model on this computer")
		}
	}
	switch ar := &c.System.Archive; ar.Target {
	case "":
	case "s3":
		if !regexp.MustCompile(`^[a-z]{2}(-gov)?-[a-z]+-[0-9]$`).MatchString(ar.S3.Region) {
			p.add("system.archive.s3.region", "an AWS region looks like us-east-1")
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(ar.S3.Bucket) {
			p.add("system.archive.s3.bucket", "not a valid bucket name")
		}
		if (ar.S3.AccessKey == "") != (ar.S3.SecretKey == "") {
			p.add("system.archive.s3.secretKey", "enter both keys, or neither to use the instance role")
		}
	case "azure":
		if !strings.HasPrefix(ar.Azure.ContainerSAS, "https://") || !strings.Contains(ar.Azure.ContainerSAS, "sig=") {
			p.add("system.archive.azure.containerSas", "paste the container's SAS URL (https://…?…&sig=…)")
		}
	default:
		p.add("system.archive.target", "choose S3, Azure or none")
	}
	if em := &c.System.Email; em.Host != "" {
		em.Host = strings.TrimSpace(em.Host)
		if em.Port == 0 {
			em.Port = 587
		}
		if em.Port < 1 || em.Port > 65535 {
			p.add("system.email.port", "port must be 1 to 65535")
		}
		if !validHost(em.Host) {
			p.add("system.email.host", "enter a host name or IP address, without a port")
		}
		// The address goes into mail headers, so it must parse as exactly one address and carry no line breaks.
		if a, err := mail.ParseAddress(em.From); err != nil || strings.ContainsAny(em.From, "\r\n") || !strings.Contains(a.Address, "@") {
			p.add("system.email.from", "enter the address mail comes from, like phones@example.com")
		}
	}
	pv := &c.System.Provisioning
	if pv.Transport == "" {
		pv.Transport = "udp"
	}
	if pv.Transport != "udp" && pv.Transport != "tcp" && pv.Transport != "tls" {
		p.add("system.provisioning.transport", "choose udp, tcp or tls")
	}
	if pv.Enabled {
		if pv.Username == "" {
			pv.Username = "phones"
		}
		if pv.Password == "" {
			pv.Password = NewSecret()
		}
		if len(pv.Password) < 12 {
			p.add("system.provisioning.password", "the provisioning password must be at least 12 characters")
		}
	}
	macs := map[string]bool{}
	for i := range c.Devices {
		d := &c.Devices[i]
		f := fmt.Sprintf("devices[%d]", i)
		checkID(f+".id", &d.ID)
		if m := NormalizeMAC(d.MAC); m == "" {
			p.add(f+".mac", "a MAC address is twelve hex digits, as printed under the phone")
		} else if macs[m] {
			p.add(f+".mac", "another phone already has this MAC address")
		} else {
			d.MAC, macs[m] = m, true
		}
		if !slices.Contains(Vendors, d.Vendor) {
			p.add(f+".vendor", "choose the phone's maker")
		}
		if !extNumbers[d.Extension] {
			p.add(f+".extension", "choose the extension this phone is for")
		}
	}
	for i, g := range c.PageGroups {
		if g.Multicast != "" {
			ap, err := netip.ParseAddrPort(strings.TrimSpace(g.Multicast))
			switch {
			case err != nil:
				p.add(fmt.Sprintf("pageGroups[%d].multicast", i), "use a multicast address and port, such as 239.0.1.10:5000")
			case !ap.Addr().IsMulticast():
				p.add(fmt.Sprintf("pageGroups[%d].multicast", i), "%s is not a multicast address (224.0.0.0 to 239.255.255.255)", ap.Addr())
			case ap.Port() < 1024:
				p.add(fmt.Sprintf("pageGroups[%d].multicast", i), "use a port of 1024 or more")
			default:
				c.PageGroups[i].Multicast = ap.String()
			}
		}
		for j, m := range g.Members {
			if !extNumbers[m] {
				p.add(fmt.Sprintf("pageGroups[%d].members[%d]", i, j), "no extension %s", m)
			}
		}
	}
	for i, q := range c.Queues {
		for j, m := range q.Agents {
			if !extNumbers[m] {
				p.add(fmt.Sprintf("queues[%d].agents[%d]", i, j), "no extension %s", m)
			}
		}
	}
	for i, g := range c.RingGroups {
		for j, m := range g.Members {
			if !extNumbers[m] {
				p.add(fmt.Sprintf("ringGroups[%d].members[%d]", i, j), "no extension %s", m)
			}
		}
	}

	trunkIDs := map[string]bool{}
	for i := range c.Trunks {
		t := &c.Trunks[i]
		f := fmt.Sprintf("trunks[%d]", i)
		checkID(f+".id", &t.ID)
		trunkIDs[t.ID] = true
		t.Name = strings.TrimSpace(t.Name)
		if t.Name == "" {
			p.add(f+".name", "enter a name")
		}
		t.Host, t.Port = splitTrunkHost(t.Host, t.Port)
		if _, err := netip.ParseAddr(t.Host); err != nil && !hostRe.MatchString(t.Host) {
			p.add(f+".host", "enter the provider's host name or IP address, like sip.provider.com")
		}
		if t.Port == 0 {
			t.Port = 5060
		}
		if t.MaxChannels < 0 {
			p.add(f+".maxChannels", "use 0 for no limit, or a positive number")
		}
		checkCodecs(&p, f+".codecs", t.Codecs, true)
		for j, a := range t.AllowFrom {
			af := fmt.Sprintf("%s.allowFrom[%d]", f, j)
			if pr, err := netip.ParsePrefix(a); err == nil {
				// A network this wide would let anyone on the internet pass as the provider.
				if (pr.Addr().Is4() && pr.Bits() < 16) || (pr.Addr().Is6() && pr.Bits() < 32) {
					p.add(af, "%s is too wide: use the provider's own addresses or networks", a)
				}
			} else if _, err := netip.ParseAddr(a); err != nil {
				p.add(af, "%s is not an IP address or network like 203.0.113.0/24", a)
			}
		}
		t.SBCName = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(t.SBCName), "."))
		if t.Teams {
			// Direct Routing is TLS on 5061 with SRTP, never registration.
			if t.Host == "" {
				t.Host = "sip.pstnhub.microsoft.com"
			}
			if t.Port == 0 {
				t.Port = 5061
			}
			t.Register = false
			if t.Transport != "tls" {
				p.add(f+".transport", "Teams Direct Routing needs TLS")
			}
			if !t.SecureMedia {
				p.add(f+".secureMedia", "Teams Direct Routing needs encrypted media (SRTP): turn on Encrypt media")
			}
			switch {
			case t.SBCName == "":
				p.add(f+".sbcName", "enter this server's public name, as given to Microsoft (for example sbc.example.com)")
			case !hostnameRe.MatchString(t.SBCName) || !strings.Contains(t.SBCName, "."):
				p.add(f+".sbcName", "enter a full public name such as sbc.example.com, not an address")
			case strings.HasSuffix(t.SBCName, ".onmicrosoft.com"):
				p.add(f+".sbcName", "Microsoft does not accept .onmicrosoft.com names for an SBC: use a name in your own domain")
			}
		}
		if t.Port < 1 || t.Port > 65535 {
			p.add(f+".port", "use a port from 1 to 65535")
		}
		if t.Transport == "" {
			t.Transport = "udp"
		}
		if t.Shared != "" && t.Shared != "one" {
			p.add(f+".shared", "choose each server, or one server at a time")
		}
		if t.Transport != "udp" && t.Transport != "tcp" && t.Transport != "tls" {
			p.add(f+".transport", "choose udp, tcp or tls")
		}
		if t.Register && t.Username == "" {
			p.add(f+".username", "registration needs a username")
		}
		// These go into SIP headers and URIs; anything outside a SIP user part would break them.
		for name, v := range map[string]*string{"username": &t.Username, "authUser": &t.AuthUser, "callerId": &t.CallerID} {
			*v = strings.TrimSpace(*v)
			if *v != "" && !sipUserRe.MatchString(*v) {
				p.add(f+"."+name, "use letters, digits and . - _ + ! ~ * ' ( ) only")
			}
		}
		t.FromDomain = strings.TrimSpace(t.FromDomain)
		if t.FromDomain != "" {
			if _, err := netip.ParseAddr(t.FromDomain); err != nil && !hostRe.MatchString(t.FromDomain) {
				p.add(f+".fromDomain", "enter a domain name, like sip.provider.com")
			}
		}
	}

	checkDest := func(field string, d *Destination) {
		switch d.Type {
		case "extension":
			if d.Target == "" {
				p.add(field, "choose an extension")
			} else if !extNumbers[d.Target] {
				p.add(field, "no extension %s", d.Target)
			}
		case "ringgroup":
			found := false
			for _, g := range c.RingGroups {
				found = found || g.Number == d.Target
			}
			if !found {
				p.add(field, "no ring group %s", d.Target)
			}
		case "script":
			if c.FindScript(d.Target) == nil {
				p.add(field, "no script %s", d.Target)
			}
		case "menu":
			if c.FindMenu(d.Target) == nil {
				p.add(field, "no menu %s", d.Target)
			}
		case "receptionist":
			if c.FindReceptionist(d.Target) == nil {
				p.add(field, "no receptionist %s", d.Target)
			}
		case "schedule":
			if c.FindSchedule(d.Target) == nil {
				p.add(field, "no schedule %s", d.Target)
			}
		case "queue":
			found := false
			for _, q := range c.Queues {
				found = found || q.Number == d.Target
			}
			if !found {
				p.add(field, "no queue %s", d.Target)
			}
		case "voicemail":
			found := false
			for _, e := range c.Extensions {
				found = found || (e.Number == d.Target && e.Voicemail.Enabled)
			}
			if !found {
				p.add(field, "extension %s has no voicemail switched on", d.Target)
			}
		case "fax":
			if c.FindFax(d.Target) == nil {
				p.add(field, "no fax %s", d.Target)
			}
		case "conference":
			if c.FindConference(d.Target) == nil {
				p.add(field, "no conference room %s", d.Target)
			}
		case "esrp":
			if len(c.System.Core.PSAPs) == 0 {
				p.add(field, "list the PSAPs on Core services before routing to the ESRP")
			}
		case "echo", "hangup":
		default:
			p.add(field, "choose where calls go")
		}
	}
	for i := range c.Extensions {
		if d := c.Extensions[i].NoAnswer; d != nil {
			checkDest(fmt.Sprintf("extensions[%d].noAnswer", i), d)
		}
	}
	for i := range c.Receptionists {
		r := &c.Receptionists[i]
		f := fmt.Sprintf("receptionists[%d]", i)
		if r.ID == "" {
			r.ID = NewID()
		}
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			p.add(f+".name", "name the receptionist")
		}
		if len(r.Knowledge) > 8000 {
			p.add(f+".knowledge", "keep the approved information under 8000 characters")
		}
		switch r.Person.Type {
		case "":
			p.add(f+".person", "choose where callers who want a person go")
		case "receptionist", "script":
			// The way out must reach people, never another assistant or code that might send it back.
			p.add(f+".person", "choose an extension, ring group, queue or voicemail: callers who want a person must reach one")
		default:
			checkDest(f+".person", &r.Person)
			if via := c.backToAssistant(r.Person); via != "" {
				p.add(f+".person", "callers who want a person would come back to an AI receptionist %s: choose a destination that rings people", via)
			}
		}
		if r.MessageBox != "" {
			ok := false
			for _, e := range c.Extensions {
				ok = ok || (e.Number == r.MessageBox && e.Voicemail.Enabled)
			}
			if !ok {
				p.add(f+".messageBox", "choose an extension with voicemail")
			}
		}
	}
	if em := &c.System.EmergencyMode; em.On || em.Destination.Type != "" {
		checkDest("system.emergencyMode.destination", &em.Destination)
	}
	for i, n := range c.System.EmergencyMode.Allowed {
		if !extNumbers[n] {
			p.add(fmt.Sprintf("system.emergencyMode.allowed[%d]", i), "no extension %s", n)
		}
	}
	for i := range c.RingGroups {
		if d := c.RingGroups[i].NoAnswer; d != nil {
			checkDest(fmt.Sprintf("ringGroups[%d].noAnswer", i), d)
		}
	}
	for i := range c.Menus {
		m := &c.Menus[i]
		f := fmt.Sprintf("menus[%d]", i)
		checkID(f+".id", &m.ID)
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if m.Number != "" {
			if !numberRe.MatchString(m.Number) {
				p.add(f+".number", "menu numbers are 2 to 8 digits, or empty")
			} else {
				claim(f+".number", m.Number, "menu "+m.Name)
			}
		}
		if m.TimeoutSeconds == 0 {
			m.TimeoutSeconds = 5
		}
		if m.TimeoutSeconds < 1 || m.TimeoutSeconds > 60 {
			p.add(f+".timeoutSeconds", "wait 1 to 60 seconds for a key")
		}
		if m.Repeats == 0 {
			m.Repeats = 2
		}
		if m.Repeats < 1 || m.Repeats > 5 {
			p.add(f+".repeats", "play the prompt 1 to 5 times")
		}
		keys := map[string]bool{}
		for j := range m.Options {
			o := &m.Options[j]
			of := fmt.Sprintf("%s.options[%d]", f, j)
			if len(o.Key) != 1 || !strings.Contains("0123456789*#", o.Key) {
				p.add(of+".key", "a key is 0 to 9, * or #")
			} else if keys[o.Key] {
				p.add(of+".key", "key %s is used twice", o.Key)
			}
			keys[o.Key] = true
		}
	}
	hhmm := regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$|^24:00$`)
	for i := range c.Schedules {
		s := &c.Schedules[i]
		f := fmt.Sprintf("schedules[%d]", i)
		checkID(f+".id", &s.ID)
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			p.add(f+".name", "enter a name")
		}
		for j, h := range s.Hours {
			hf := fmt.Sprintf("%s.hours[%d]", f, j)
			if !hhmm.MatchString(h.Start) || !hhmm.MatchString(h.End) {
				p.add(hf, "enter times as HH:MM")
			} else if h.End == h.Start {
				p.add(hf, "opening and closing times are the same")
			}
			if len(h.Days) == 0 {
				p.add(hf+".days", "choose at least one day")
			}
			for _, d := range h.Days {
				if d < 0 || d > 6 {
					p.add(hf+".days", "days are Sunday (0) to Saturday (6)")
				}
			}
		}
		for j, h := range s.Holidays {
			if _, err := time.Parse("2006-01-02", h.Date); err != nil {
				p.add(fmt.Sprintf("%s.holidays[%d].date", f, j), "dates are YYYY-MM-DD")
			}
		}
		if s.Open.Type == "schedule" || s.Closed.Type == "schedule" {
			p.add(f, "a schedule cannot lead to another schedule")
		}
		checkDest(f+".open", &s.Open)
		checkDest(f+".closed", &s.Closed)
	}
	for i := range c.Scripts {
		s := &c.Scripts[i]
		f := fmt.Sprintf("scripts[%d]", i)
		checkID(f+".id", &s.ID)
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if s.Language != "js" && s.Language != "lua" {
			p.add(f+".language", "choose JavaScript or Lua")
		} else if ScriptCheck != nil {
			if err := ScriptCheck(s.Language, s.Source); err != nil {
				p.add(f+".source", "%s", err.Error())
			}
		}
		if len(s.Source) > 64<<10 {
			p.add(f+".source", "scripts can be up to 64 KB")
		}
		if s.Fallback.Type == "script" {
			p.add(f+".fallback", "the fallback cannot be another script")
		} else {
			checkDest(f+".fallback", &s.Fallback)
		}
	}
	for i := range c.Menus {
		m := &c.Menus[i]
		for j := range m.Options {
			if m.Options[j].Destination.Type == "menu" && m.Options[j].Destination.Target == m.ID {
				continue // a key that starts the menu again is fine
			}
			checkDest(fmt.Sprintf("menus[%d].options[%d].destination", i, j), &m.Options[j].Destination)
		}
		if m.Timeout != nil {
			checkDest(fmt.Sprintf("menus[%d].timeout", i), m.Timeout)
		}
	}
	for i := range c.Queues {
		if d := c.Queues[i].Timeout; d != nil {
			f := fmt.Sprintf("queues[%d].timeout", i)
			if d.Type != "voicemail" && d.Type != "hangup" {
				p.add(f, "after the wait, callers go to a voicemail box or are told goodbye")
			} else {
				checkDest(f, d)
			}
		}
	}
	for i := range c.InboundRoutes {
		r := &c.InboundRoutes[i]
		f := fmt.Sprintf("inboundRoutes[%d]", i)
		checkID(f+".id", &r.ID)
		if r.DID != "" && !didRe.MatchString(r.DID) {
			p.add(f+".did", "a DID is the number callers dial, digits only")
		}
		if r.Trunk != "" && !trunkIDs[r.Trunk] {
			p.add(f+".trunk", "no such trunk")
		}
		checkDest(f+".destination", &r.Destination)
	}
	for i := range c.System.CallLinks {
		if d := &c.System.CallLinks[i].Destination; d.Type != "" {
			checkDest(fmt.Sprintf("system.callLinks[%d].destination", i), d)
		}
	}
	for i := range c.OutboundRoutes {
		r := &c.OutboundRoutes[i]
		f := fmt.Sprintf("outboundRoutes[%d]", i)
		checkID(f+".id", &r.ID)
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if len(r.Patterns) == 0 {
			p.add(f+".patterns", "add at least one dial pattern")
		}
		for j, pat := range r.Patterns {
			if !patternRe.MatchString(pat) {
				p.add(fmt.Sprintf("%s.patterns[%d]", f, j), "use digits, X (any digit), N (2-9), Z (1-9), [1-5] and a final . for more digits")
			}
		}
		if len(r.Trunks) == 0 {
			p.add(f+".trunks", "choose at least one trunk")
		}
		for j, t := range r.Trunks {
			if !trunkIDs[t] {
				p.add(fmt.Sprintf("%s.trunks[%d]", f, j), "no such trunk")
			}
		}
		if r.Strip < 0 || r.Strip > 10 {
			p.add(f+".strip", "strip 0 to 10 digits")
		}
		if r.Prepend != "" && !regexp.MustCompile(`^\+?[0-9*#]{1,15}$`).MatchString(r.Prepend) {
			p.add(f+".prepend", "digits only, optionally starting with +")
		}
	}
	for i, n := range c.Security.TrustedNetworks {
		if _, err := netip.ParsePrefix(n); err != nil {
			p.add(fmt.Sprintf("security.trustedNetworks[%d]", i), "use CIDR notation like 192.168.1.0/24")
		}
	}
	if c.Security.FailBanThreshold < 3 {
		p.add("security.failBanThreshold", "at least 3 failures")
	}
	if w := c.Security.FailBanWindowSecs; w < 10 || w > 86400 {
		p.add("security.failBanWindowSecs", "10 seconds to a day")
	}
	if m := c.Security.FailBanMinutes; m < 1 || m > 525600 {
		p.add("security.failBanMinutes", "1 minute to a year")
	}
	if c.Security.MaxCallsPerExtension < 1 {
		p.add("security.maxCallsPerExtension", "at least 1")
	}
	if len(p) > 0 {
		return p
	}
	return nil
}

// MatchPattern reports whether a dialled number matches a dial pattern (FreePBX-style: X any digit, N 2-9,
// Z 1-9, [..] a set, a final . for one or more further characters).
func MatchPattern(pattern, number string) bool {
	i := 0
	for j := 0; j < len(pattern); j++ {
		c := pattern[j]
		if c == '.' {
			return i < len(number)
		}
		if i >= len(number) {
			return false
		}
		d := number[i]
		switch c {
		case 'X':
			if d < '0' || d > '9' {
				return false
			}
		case 'N':
			if d < '2' || d > '9' {
				return false
			}
		case 'Z':
			if d < '1' || d > '9' {
				return false
			}
		case '[':
			end := strings.IndexByte(pattern[j:], ']')
			if end < 0 || !inSet(pattern[j+1:j+end], d) {
				return false
			}
			j += end
		default:
			if c != d {
				return false
			}
		}
		i++
	}
	return i == len(number)
}

func inSet(set string, d byte) bool {
	for k := 0; k < len(set); k++ {
		if k+2 < len(set) && set[k+1] == '-' {
			if d >= set[k] && d <= set[k+2] {
				return true
			}
			k += 2
			continue
		}
		if set[k] == d {
			return true
		}
	}
	return false
}

// FindExtension returns the extension with a number, or nil.
func (c *Config) FindExtension(number string) *Extension {
	for i := range c.Extensions {
		if c.Extensions[i].Number == number {
			return &c.Extensions[i]
		}
	}
	return nil
}

// FindRingGroup returns the ring group with a number, or nil.
func (c *Config) FindRingGroup(number string) *RingGroup {
	for i := range c.RingGroups {
		if c.RingGroups[i].Number == number {
			return &c.RingGroups[i]
		}
	}
	return nil
}

// FindQueue returns the queue with a number, or nil.
// FindFax finds a fax box by its number.
func (c *Config) FindFax(number string) *FaxBox {
	for i := range c.Faxes {
		if c.Faxes[i].Number == number {
			return &c.Faxes[i]
		}
	}
	return nil
}

func (c *Config) FindQueue(number string) *Queue {
	for i := range c.Queues {
		if c.Queues[i].Number == number {
			return &c.Queues[i]
		}
	}
	return nil
}

// FindTrunk returns the trunk with an ID, or nil.
func (c *Config) FindTrunk(id string) *Trunk {
	for i := range c.Trunks {
		if c.Trunks[i].ID == id {
			return &c.Trunks[i]
		}
	}
	return nil
}

// NextExtensionNumber suggests the next free extension number.
func (c *Config) NextExtensionNumber() string {
	used := map[string]bool{}
	for _, e := range c.Extensions {
		used[e.Number] = true
	}
	for _, g := range c.RingGroups {
		used[g.Number] = true
	}
	for n := 100; n < 100000; n++ {
		s := fmt.Sprint(n)
		if !used[s] {
			return s
		}
	}
	return ""
}

// emergencyNumbers are the numbers people dial for police, fire and ambulance, by country. Every
// phone system must route them. 112 works in the EU and on most mobile networks.
var emergencyNumbers = map[string][]string{
	"US": {"911", "933"}, "CA": {"911"}, "MX": {"911"},
	"GB": {"999", "112"}, "IE": {"999", "112"},
	"AU": {"000", "112"}, "NZ": {"111"},
	"IN": {"112", "100", "101", "102"}, "JP": {"110", "119"}, "CN": {"110", "119", "120"},
	"BR": {"190", "192", "193"}, "ZA": {"10111", "112"},
}

// EmergencyNumbersFor returns a country's emergency numbers (112 for Europe and countries not listed).
func EmergencyNumbersFor(country string) []string {
	if n, ok := emergencyNumbers[strings.ToUpper(country)]; ok {
		return append([]string(nil), n...)
	}
	return []string{"112"}
}

var (
	callerIDRe = regexp.MustCompile(`^\+?[0-9]{2,20}$`)
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	// sipUserRe is the unreserved and user-unreserved characters of RFC 3261 25.1, plus the escape %.
	sipUserRe = regexp.MustCompile(`^[A-Za-z0-9.\-_!~*'()&=+$,;?/%]{1,64}$`)
)

// NationalPatterns are the outbound dial patterns setup creates for a country's ordinary numbers.
func NationalPatterns(country string) []string {
	switch strings.ToUpper(country) {
	case "US", "CA":
		return []string{"NXXNXXXXXX", "1NXXNXXXXXX"}
	}
	// Most of the world dials 0 then the area code for national numbers. International calls (00 or 011)
	// are left out on purpose: they are what toll fraud runs up, so an administrator adds them knowingly.
	return []string{"0Z."}
}

// splitTrunkHost accepts what people paste from a provider's welcome email ("sip:sip.example.com:5080",
// "sip.example.com:5080", "[2001:db8::1]:5060", "sips:host") and returns the bare host and the port.
func splitTrunkHost(h string, port int) (string, int) {
	h = strings.TrimSpace(h)
	low := strings.ToLower(h)
	for _, s := range []string{"sips:", "sip:", "udp://", "tcp://", "tls://"} {
		if strings.HasPrefix(low, s) {
			h = h[len(s):]
			break
		}
	}
	if i := strings.IndexAny(h, ";/?"); i >= 0 {
		h = h[:i]
	}
	if at := strings.LastIndex(h, "@"); at >= 0 {
		h = h[at+1:]
	}
	if host, p, err := net.SplitHostPort(h); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return host, n
		}
	}
	return strings.Trim(h, "[]"), port
}

// validHost reports whether h is an IP address or a DNS host name (no port, scheme or spaces).
func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return hostRe.MatchString(h)
}

// reservedBy names the system setting that uses a number, if any.
func reservedBy(s *System, park map[string]bool, number string) (field, what string) {
	switch {
	case number == s.EchoNumber:
		return "system.echoNumber", "echo test"
	case number == s.VoicemailNumber:
		return "system.voicemailNumber", "voicemail"
	case park[number]:
		return "system.parkNumber", "call park"
	case slices.Contains(s.EmergencyNumbers, number):
		return "system.emergencyNumbers", "emergency"
	}
	return "", ""
}

// Codecs BitVoice knows, by the names SDP uses.
var KnownCodecs = []string{"PCMU", "PCMA", "G722", "OPUS", "G729", "G726-32", "G726-16", "AAL2-G726-32", "GSM"}

// DefaultCodecs is the list a new system starts with: wideband first between phones that have it, G.711
// for everything else. Opus and G.729 are opt-in: BitVoice converts them, at some processor cost.
var DefaultCodecs = []string{"G722", "PCMU", "PCMA"}

// checkCodecs validates a codec list: known names, no repeats, and at least one BitVoice can convert, so
// prompts, voicemail and calls to other parties always work. An empty list is allowed where it means
// "the default".
func checkCodecs(p *Problems, field string, list []string, emptyOK bool) {
	if len(list) == 0 {
		if !emptyOK {
			p.add(field, "choose at least one codec")
		}
		return
	}
	seen := map[string]bool{}
	for i, c := range list {
		u := strings.ToUpper(strings.TrimSpace(c))
		list[i] = u
		if !slices.Contains(KnownCodecs, u) {
			p.add(fmt.Sprintf("%s[%d]", field, i), "unknown codec %q; use %s", c, strings.Join(KnownCodecs, ", "))
			continue
		}
		if seen[u] {
			p.add(fmt.Sprintf("%s[%d]", field, i), "%s is listed twice", u)
		}
		seen[u] = true
	}
}

// Compat is Asterisk compatibility: AMI (the manager protocol) and ARI (the REST interface).
type Compat struct {
	AMI bool `json:"ami"`
	// AMIListen is where AMI listens; by default only this computer (127.0.0.1:5038).
	AMIListen string `json:"amiListen"`
	// ARI is served under /ari on the web interface's ports.
	ARI   bool         `json:"ari"`
	Users []CompatUser `json:"users"`
}

// CompatUser is an AMI or ARI login, as in manager.conf and ari.conf.
type CompatUser struct {
	Username string `json:"username"`
	Secret   string `json:"secret"`
	// Write allows actions that change calls (originate, redirect, hang up); without it, read only.
	Write bool `json:"write"`
}

var compatUserRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (c *Config) validateCompat(p *Problems) {
	x := &c.System.Compat
	if x.AMIListen == "" {
		x.AMIListen = "127.0.0.1:5038"
	}
	host, port, err := net.SplitHostPort(x.AMIListen)
	if n, perr := strconv.Atoi(port); err != nil || perr != nil || n < 1 || n > 65535 {
		p.add("system.compat.amiListen", "use an address and port, such as 127.0.0.1:5038")
	} else if host != "" {
		if _, err := netip.ParseAddr(host); err != nil {
			p.add("system.compat.amiListen", "use an IP address (or nothing, for every address) before the port")
		}
	}
	if (x.AMI || x.ARI) && len(x.Users) == 0 {
		p.add("system.compat.users", "add a login, or nothing can connect")
	}
	seen := map[string]bool{}
	for i, u := range x.Users {
		f := fmt.Sprintf("system.compat.users[%d]", i)
		x.Users[i].Username = strings.TrimSpace(u.Username)
		u = x.Users[i]
		if !compatUserRe.MatchString(u.Username) {
			p.add(f+".username", "letters, digits, dot, dash and underscore, starting with a letter or digit")
		} else if seen[strings.ToLower(u.Username)] {
			p.add(f+".username", "%s is listed twice", u.Username)
		}
		seen[strings.ToLower(u.Username)] = true
		if len(u.Secret) < 12 {
			p.add(f+".secret", "use at least 12 characters: these logins can place calls")
		}
	}
}

// SSO is single sign-on with OpenID Connect (Microsoft Entra ID, Google, Okta, Keycloak and others).
type SSO struct {
	Enabled bool `json:"enabled"`
	// Label is the sign-in button's text ("Sign in with Microsoft").
	Label  string `json:"label"`
	Issuer string `json:"issuer"`
	// ClientID and ClientSecret are from the application registered with the provider.
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// RedirectURL is the address the provider sends people back to; empty works it out from the browser's
	// address (http(s)://this-host/api/auth/sso/callback).
	RedirectURL string `json:"redirectUrl"`
	// GroupsClaim names the token claim listing the person's groups (groups, roles).
	GroupsClaim string `json:"groupsClaim"`
	// AdminGroups make someone an administrator; ViewerGroups a viewer. With no viewer groups, anyone the
	// provider signs in who is not an administrator becomes a viewer.
	AdminGroups  []string `json:"adminGroups"`
	ViewerGroups []string `json:"viewerGroups"`
	// PasswordSignIn keeps the username and password form; off, only SSO signs people in (the local
	// administrator can still sign in from this computer, so a broken provider never locks everyone out).
	PasswordSignIn bool `json:"passwordSignIn"`
}

func (c *Config) validateSSO(p *Problems) {
	s := &c.System.SSO
	s.Issuer, s.ClientID, s.RedirectURL = strings.TrimSpace(s.Issuer), strings.TrimSpace(s.ClientID), strings.TrimSpace(s.RedirectURL)
	if s.GroupsClaim == "" {
		s.GroupsClaim = "groups"
	}
	if s.Label == "" {
		s.Label = "Sign in with single sign-on"
	}
	if !s.Enabled {
		return
	}
	if u, err := url.Parse(s.Issuer); err != nil || u.Host == "" || (u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		p.add("system.sso.issuer", "use the provider's https address, such as https://login.microsoftonline.com/<tenant>/v2.0")
	}
	if s.ClientID == "" {
		p.add("system.sso.clientId", "copy the application (client) ID from the provider")
	}
	if s.RedirectURL != "" {
		if u, err := url.Parse(s.RedirectURL); err != nil || u.Host == "" || !strings.HasSuffix(u.Path, "/api/auth/sso/callback") {
			p.add("system.sso.redirectUrl", "use this server's address followed by /api/auth/sso/callback")
		}
	}
	if len(s.AdminGroups) == 0 {
		p.add("system.sso.adminGroups", "name at least one group whose members are administrators")
	}
}

var dtmfCode = regexp.MustCompile(`^[0-9*#]{1,6}$`)

// checkDevice validates what an extension says is connected to it.
func checkDevice(p *Problems, f string, e *Extension) {
	if e.Kind == "phone" {
		e.Kind = ""
	}
	switch e.Kind {
	case "", "door", "speaker", "analog":
	default:
		p.add(f+".kind", "choose phone, door, speaker or analog")
	}
	if e.Kind != "door" {
		e.Door = Door{}
		return
	}
	d := &e.Door
	d.Code, d.URL = strings.TrimSpace(d.Code), strings.TrimSpace(d.URL)
	if d.Code == "" {
		d.Code = "*1"
	}
	if !dtmfCode.MatchString(d.Code) {
		p.add(f+".door.code", "use 1 to 6 keys: digits, * and #")
	}
	if d.URL != "" {
		u, err := url.Parse(d.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			p.add(f+".door.url", "use the door controller's http:// or https:// address")
		} else if u.User != nil {
			p.add(f+".door.url", "put the username and password in their own boxes, not in the address")
		}
	}
	d.Method = strings.ToUpper(d.Method)
	if d.Method == "" {
		d.Method = "GET"
	}
	if d.Method != "GET" && d.Method != "POST" && d.Method != "PUT" {
		p.add(f+".door.method", "choose GET, POST or PUT")
	}
}

// Audio is sound clean-up: noise suppression and level control.
type Audio struct {
	// Voicemail cleans messages and recorded greetings as they are saved.
	Voicemail bool `json:"voicemail"`
	// Calls cleans what each party sends during calls, as it passes through (G.711 calls; about 16 ms
	// more delay).
	Calls bool `json:"calls"`
}

// Mobile is push for the BitVoice apps. A push only wakes the app; the call itself then reaches it as it
// reaches the browser phone.
type Mobile struct {
	// Push is "" (off), relay (through a push relay such as BioDream's) or direct (this server holds the
	// Apple and Google credentials of apps you build yourself).
	Push        string `json:"push"`
	RelayURL    string `json:"relayUrl"`
	RelayKey    string `json:"relayKey"`
	RelaySecret string `json:"relaySecret"`
	APNs        APNs   `json:"apns"`
	// FCM is the Firebase service account JSON key, for Android.
	FCM string `json:"fcm"`
	// ServerURL is the address the apps use to reach this server from anywhere; empty uses the address
	// the browser had when the app was paired.
	ServerURL string `json:"serverUrl"`
}

// APNs is Apple push with a token key.
type APNs struct {
	KeyID   string `json:"keyId"`
	TeamID  string `json:"teamId"`
	Topic   string `json:"topic"` // the app's bundle ID
	Key     string `json:"key"`   // the .p8 file
	Sandbox bool   `json:"sandbox"`
}

func (c *Config) validateMobile(p *Problems) {
	m := &c.System.Mobile
	httpsURL := func(field, v string) {
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			p.add(field, "use an https:// address")
		}
	}
	if m.ServerURL != "" {
		m.ServerURL = strings.TrimRight(strings.TrimSpace(m.ServerURL), "/")
		httpsURL("system.mobile.serverUrl", m.ServerURL)
	}
	switch m.Push {
	case "":
	case "relay":
		httpsURL("system.mobile.relayUrl", m.RelayURL)
		if m.RelayKey == "" {
			p.add("system.mobile.relayKey", "enter the key ID you were given")
		}
		if len(m.RelaySecret) < 16 {
			p.add("system.mobile.relaySecret", "enter the secret you were given (16 characters or more)")
		}
	case "direct":
		if m.APNs.Key == "" && m.FCM == "" {
			p.add("system.mobile.push", "add Apple or Google credentials, or both")
		}
		if m.APNs.Key != "" {
			if !strings.Contains(m.APNs.Key, "PRIVATE KEY") {
				p.add("system.mobile.apns.key", "paste the whole .p8 file")
			}
			if m.APNs.KeyID == "" || m.APNs.TeamID == "" || m.APNs.Topic == "" {
				p.add("system.mobile.apns.keyId", "Apple needs the key ID, team ID and the app's bundle ID")
			}
		}
		if m.FCM != "" {
			var sa struct {
				ProjectID   string `json:"project_id"`
				ClientEmail string `json:"client_email"`
			}
			if json.Unmarshal([]byte(m.FCM), &sa) != nil || sa.ProjectID == "" || sa.ClientEmail == "" {
				p.add("system.mobile.fcm", "paste the whole service account JSON file from Firebase")
			}
		}
	default:
		p.add("system.mobile.push", "choose off, relay or direct")
	}
}

// EmergencyMode reroutes every incoming call while it is on.
type EmergencyMode struct {
	On          bool        `json:"on"`
	Destination Destination `json:"destination"`
	// Code, dialled from one of the Allowed extensions, switches the mode on or off (from a phone, when
	// nobody can reach a computer).
	Code    string    `json:"code"`
	Allowed []string  `json:"allowed"`
	Since   time.Time `json:"since,omitzero"`
	By      string    `json:"by,omitempty"`
}

// GitOps reads the configuration from a file in a Git repository (its raw address), so changes are made
// by commits and reviewed like code. Secrets stay here: the file leaves them out.
type GitOps struct {
	URL string `json:"url"`
	// Token is sent as a bearer token (a read-only deploy or access token).
	Token string `json:"token"`
	// EveryMinutes between checks.
	EveryMinutes int `json:"everyMinutes"`
	// Apply makes the file's changes; off, they are only reported.
	Apply bool `json:"apply"`
}

func (c *Config) validateGitOps(p *Problems) {
	g := &c.System.GitOps
	g.URL = strings.TrimSpace(g.URL)
	if g.URL == "" {
		return
	}
	if u, err := url.Parse(g.URL); err != nil || (u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || u.Host == "" {
		p.add("system.gitops.url", "use the file's https:// raw address")
	}
	if g.EveryMinutes == 0 {
		g.EveryMinutes = 5
	}
	if g.EveryMinutes < 1 || g.EveryMinutes > 1440 {
		p.add("system.gitops.everyMinutes", "check every 1 to 1440 minutes")
	}
}

// Vault is access to cloud secret stores. Leave the AWS keys empty to use the machine's instance role,
// and the Azure app empty to use the machine's managed identity.
type Vault struct {
	AWSAccessKey      string `json:"awsAccessKey"`
	AWSSecretKey      string `json:"awsSecretKey"`
	AzureTenant       string `json:"azureTenant"`
	AzureClientID     string `json:"azureClientId"`
	AzureClientSecret string `json:"azureClientSecret"`
}

// CRM connects BitVoice to a customer system (or anything with a web address) without code for each
// product: a lookup for caller pop-ups, a link that opens the caller there, and a webhook that logs calls.
type CRM struct {
	// LookupURL is asked who a number is when a call rings (GET, {number} replaced, URL-encoded). It
	// answers JSON: {"name": "...", "company": "...", "url": "...", "notes": "..."}; 404 means unknown.
	LookupURL string `json:"lookupUrl"`
	// LookupHeader and LookupSecret are sent with each lookup (Authorization and "Bearer abc", say).
	LookupHeader string `json:"lookupHeader"`
	LookupSecret string `json:"lookupSecret"`
	// OpenURL is a link that shows the caller in the CRM ({number} replaced), such as
	// https://crm.example.com/search?phone={number}.
	OpenURL string `json:"openUrl"`
	// LogURL receives every call when it ends (and again with its summary once it is transcribed), as
	// a signed JSON POST.
	LogURL string `json:"logUrl"`
	// LogSecret signs what is sent to LogURL: X-BitVoice-Signature is sha256=HMAC(secret, timestamp.body).
	LogSecret string `json:"logSecret"`
}

func (c *Config) validateCRM(p *Problems) {
	m := &c.System.CRM
	m.LookupURL, m.OpenURL, m.LogURL = strings.TrimSpace(m.LookupURL), strings.TrimSpace(m.OpenURL), strings.TrimSpace(m.LogURL)
	m.LookupHeader = strings.TrimSpace(m.LookupHeader)
	web := func(f, v string, needNumber bool) {
		if v == "" {
			return
		}
		u, err := url.Parse(strings.ReplaceAll(v, "{number}", "0"))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			p.add(f, "use a web address starting with https://")
			return
		}
		if needNumber && !strings.Contains(v, "{number}") {
			p.add(f, "put {number} where the caller's number goes")
		}
	}
	web("system.crm.lookupUrl", m.LookupURL, true)
	web("system.crm.openUrl", m.OpenURL, true)
	web("system.crm.logUrl", m.LogURL, false)
	if m.LookupHeader != "" && !headerName.MatchString(m.LookupHeader) {
		p.add("system.crm.lookupHeader", "use a header name such as Authorization or X-Api-Key")
	}
	if m.LookupHeader == "" && m.LookupSecret != "" {
		p.add("system.crm.lookupHeader", "name the header the secret goes in, such as Authorization")
	}
	if m.LogURL != "" && m.LogSecret == "" {
		p.add("system.crm.logSecret", "set a secret, so the receiver can check each call came from this system")
	}
}

// ClusterSettings: one name for every server, and one address that moves to a running server.
type ClusterSettings struct {
	// Name is a DNS name whose SRV records (_sip._udp, _sip._tcp, _sips._tcp) list every server.
	// Phones BitVoice sets up are given this name, and look the servers up themselves.
	Name string `json:"name"`
	// SharedAddress (Linux) is an address with its prefix (192.0.2.50/24) held by one running server
	// at a time; another takes it within seconds if that server stops. Phones and providers that take
	// only one address use it. Interface names the network interface; empty finds the one whose
	// network holds the address.
	SharedAddress string `json:"sharedAddress"`
	Interface     string `json:"interface"`
}

// Brand is how one outgoing number presents itself.
type Brand struct {
	Number string `json:"number"`
	Name   string `json:"name"`   // shown as the caller's name (up to 32 characters)
	Logo   string `json:"logo"`   // https address of a square image
	Reason string `json:"reason"` // why we call, e.g. "Appointment reminder"
	Watch  bool   `json:"watch"`  // warn when the number looks labelled as spam
}

// ExtCalendar is an extension's calendar: its private iCalendar address (Outlook: Settings, Calendar,
// Shared calendars, Publish; Google: Settings, the calendar, Secret address in iCal format) and what
// happens to calls while it says the person is busy.
type ExtCalendar struct {
	Secret string `json:"secret"` // the private .ics address (it works as a password: keep it private)
	// WhenBusy is voicemail (default), forward (to ForwardTo) or ring (show it, ring as usual).
	WhenBusy  string `json:"whenBusy"`
	ForwardTo string `json:"forwardTo"`
	// IgnoreMeetings counts only time off (all-day events, out of office) as busy, not meetings.
	IgnoreMeetings bool `json:"ignoreMeetings"`
}

// HotDeskCodes are the feature codes and the nightly sign-out for hot desks.
type HotDeskCodes struct {
	Login  string `json:"login"`  // *33 when unset; "off" for none
	Logout string `json:"logout"` // *34 when unset; "off" for none
	// SignOutAt signs everyone out every day at this time (HH:MM, the system time zone); empty for never.
	SignOutAt string `json:"signOutAt"`
}

// Metrics is where measurements go besides /metrics.
type Metrics struct {
	// OTLPURL is an OpenTelemetry collector's OTLP/HTTP address (https://otel.example.com:4318);
	// /v1/metrics is added. Empty: no push.
	OTLPURL string `json:"otlpUrl"`
	// OTLPHeader and OTLPSecret are sent with each push (Authorization and "Bearer abc", say).
	OTLPHeader string `json:"otlpHeader"`
	OTLPSecret string `json:"otlpSecret"`
	// IntervalSeconds between pushes; 60 when unset, at least 15.
	IntervalSeconds int `json:"intervalSeconds"`
}

func (c *Config) validateMetrics(p *Problems) {
	m := &c.System.Metrics
	m.OTLPURL, m.OTLPHeader = strings.TrimSpace(m.OTLPURL), strings.TrimSpace(m.OTLPHeader)
	if m.OTLPURL != "" {
		if u, err := url.Parse(m.OTLPURL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			p.add("system.metrics.otlpUrl", "use a web address starting with https://")
		}
	}
	if m.OTLPHeader != "" && !headerName.MatchString(m.OTLPHeader) {
		p.add("system.metrics.otlpHeader", "use a header name such as Authorization")
	}
	if m.OTLPHeader == "" && m.OTLPSecret != "" {
		p.add("system.metrics.otlpHeader", "name the header the secret goes in, such as Authorization")
	}
	if m.IntervalSeconds != 0 && (m.IntervalSeconds < 15 || m.IntervalSeconds > 3600) {
		p.add("system.metrics.intervalSeconds", "use 15 to 3600 seconds")
	}
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var clockRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (c *Config) validateHotDesk(p *Problems) {
	h := &c.System.HotDesk
	h.SignOutAt = strings.TrimSpace(h.SignOutAt)
	if h.SignOutAt != "" && !clockRe.MatchString(h.SignOutAt) {
		p.add("system.hotDesk.signOutAt", "use a time such as 23:00, or leave it empty")
	}
	for i, x := range c.Extensions {
		if x.HotDesk && x.Kind != "" {
			p.add(fmt.Sprintf("extensions[%d].hotDesk", i), "a door phone, speaker or analog line cannot be a hot desk")
		}
	}
}
