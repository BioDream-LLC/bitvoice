<div align="center">

<img src="docs/assets/bitvoice-logo.svg" alt="" width="84" height="84">

<h1><img src="docs/assets/wordmark.svg" alt="BitVoice" width="470"></h1>

### An open-source, single-binary alternative to FreePBX and Asterisk

**A complete, Apache-2.0 licensed phone system for offices, contact centres and hosted providers, run
100% from its web interface.** No Asterisk or FreeSWITCH underneath. No configuration files to edit. No
paid modules. No licence meter.

**One 37 MB file. 47 MB of memory carrying four live calls and eight phones.** Double-click it and a
browser opens on a setup wizard; a few minutes later a phone rings.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg?logo=go&logoColor=white)](https://go.dev) [![Release](https://img.shields.io/badge/release-v0.1.5-success.svg)](#download) [![Platforms](https://img.shields.io/badge/platforms-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)](#download) [![CI](https://github.com/BioDream-LLC/bitvoice/actions/workflows/ci.yml/badge.svg)](https://github.com/BioDream-LLC/bitvoice/actions/workflows/ci.yml)

### Download v0.1.5

<a href="https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-linux-amd64.tar.gz"><img src="docs/assets/buttons/download-linux.svg" alt="Download BitVoice for Linux, x86-64" width="240"></a>&nbsp;<a href="https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-darwin-arm64.tar.gz"><img src="docs/assets/buttons/download-macos.svg" alt="Download BitVoice for macOS, Apple Silicon" width="240"></a>&nbsp;<a href="https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-windows-amd64.zip"><img src="docs/assets/buttons/download-windows.svg" alt="Download BitVoice for Windows, x64" width="240"></a>

<a href="https://biodream-llc.github.io/bitvoice/manual/"><img src="docs/assets/buttons/manual.svg" alt="Read the BitVoice manual" width="240"></a>

<sub>One file, nothing to install. [ARM64 Linux and Windows, Intel Macs and checksums](#download)</sub>

[**Quick start**](#quick-start) · [**Everything it does**](#everything-it-does) · [**Public safety (lab)**](#public-safety-lab-and-design-partner-track) ·
[**What it looks like**](#what-it-looks-like) · [**Manual**](https://biodream-llc.github.io/bitvoice/manual/) ·
[**Manual (PDF)**](docs/manual/bitvoice-manual.pdf) · [**FAQ**](#faq)

</div>

---

## What is BitVoice?

**BitVoice is a phone system packaged like an appliance:** one binary, one built-in web interface, no
outside runtime, and still a REST API and configuration in Git for those who want them. Underneath is its
own Go engine: SIP and IAX2, RTP and SRTP, a media engine with codec conversion, and WebRTC. Calls arrive,
wait in queues, ring phones and browsers, are recorded and logged, and reach the people who answer them.

**100% in the web interface.** That is the first rule of the project. Setup, extensions, phones, routing,
queues, trunks, updates and rollback, diagnostics: every setting has a screen, and a feature that can only be
reached through a file or a command line is treated as a bug. Every screen has a chapter in the manual and
automated browser tests. Telecom administrators run it without being developers; developers automate the
same screens through the API.

It runs as:

- **A phone system** for offices and businesses: extensions, ring groups, queues, menus, voicemail to
  email, recording, conference rooms, forwarding and follow-me, a staff portal, desk-phone set-up, a
  browser phone, SIP and IAX2 trunks, and an import from FreePBX, Asterisk, 3CX, Cisco, Avaya and Mitel.
- **A hosted phone service:** many organisations on one server, each with its own extensions, numbers,
  administrators and web address, and limits on what each may use.
- **A contact centre:** queues with a live wallboard, callbacks, VIP and sticky routing, supervisors
  listening in, transcripts and summaries, caller cards from your CRM.

**Built to a 911 reliability bar.** Routing scripts and integrations can never lose or hold up a call: each
has a time limit and a fixed outcome when it fails. Emergency calls from every extension always go out,
with the caller's building, floor and room.

**Two missions.** BitVoice is built for two jobs, and neither is a side project:

1. **A standalone phone system that beats FreePBX, Asterisk and their rivals.** Everything above, with no
   911 parts needed or shown: a business that only wants a phone system never sees them.
2. **The call-handling side of BioDream's open-source CAD, bitdispatcher.** The same engine carries a 911
   answering-point profile, a P25 radio dispatch console and NG911 Core Services, and hands the CAD what it
   needs through the standards: EIDO incidents with links to the caller's photos and video, ALI spill,
   PIDF-LO location, live video by WHEP, radio emergencies and unit positions, the i3 log. They are built and
   tested against recorded captures, NENA's published schemas, sample data and stand-ins, **not certified and
   not yet tried with a live ESInet or P25 system**. They stay off the menu until switched on in Settings; see
   [Public safety: lab and design-partner track](#public-safety-lab-and-design-partner-track).

<img src="docs/assets/architecture.svg" alt="BitVoice sits between phone carriers, the ESInet, radio systems and phones on one side, and the people answering, CAD, records and Teams or CRMs on the other. In the middle it routes, queues, records, logs and converts codecs." width="100%">

## Everything it does

Eleven areas of the phone system, itemised; the public-safety lab track [follows](#public-safety-lab-and-design-partner-track).
Where something has only been tested against stand-ins rather than real equipment, [it says so](#how-it-is-tested).

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/calls.svg" width="168" height="40" alt="Phone system"></th><th align="left" valign="middle">Everything an office phone system does</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Extensions</b> with voicemail, call recording, an emergency location (site, floor and room) and what happens when nobody answers</td></tr>
<tr><td colspan="2"><b>Ring groups</b> and <b>auto-attendant menus</b>, with extensions dialled directly from the menu</td></tr>
<tr><td colspan="2"><b>Schedules</b>: opening hours and holidays, with a different destination for open and closed</td></tr>
<tr><td colspan="2"><b>Voicemail</b>: greetings recorded in the browser, <code>*97</code>, message-waiting lamps, the message emailed with its recording</td></tr>
<tr><td colspan="2">Hold, <b>blind and attended transfer</b>, call parking, <b>paging</b> and overhead speakers, busy lamps (BLF)</td></tr>
<tr><td colspan="2"><b>SIP trunks</b> over UDP, TCP or TLS, with registration, channel limits and failover between trunks</td></tr>
<tr><td colspan="2"><b>IAX2 trunks</b> (RFC 5456) to Asterisk and FreePBX servers and IAX2 providers, registering either way, with call tokens; tested against Asterisk 23</td></tr>
<tr><td colspan="2"><b>Call routing</b> by dial pattern, with prefixes added or digits removed, and 911 always allowed from every extension</td></tr>
<tr><td colspan="2"><b>Office 911</b> for every profile: Kari's Law notification, RAY BAUM'S Act building, floor and room, remote workers asked to confirm where they are</td></tr>
<tr><td colspan="2"><b>Routing scripts in JavaScript or Lua</b>, sandboxed and time-limited, with a fixed fallback if a script fails</td></tr>
<tr><td colspan="2">A <b>call flow diagram</b> of where calls go, and a <b>call-path simulator</b>: pick a caller, number and time and see the exact route, without ringing anything</td></tr>
<tr><td colspan="2"><b>Video calls</b> between phones that both send video, relayed and encrypted like the sound</td></tr>
<tr><td colspan="2"><b>Hot desking</b> (<code>*33</code>), a company directory on every phone, calendar-driven do not disturb from Outlook and Google</td></tr>
<tr><td colspan="2"><b>Conference rooms</b> with a PIN and a leader PIN, wait for the leader, members joining muted (press 1 to ask to speak), lock, recording, and a live view to mute or remove people</td></tr>
<tr><td colspan="2"><b>Forwarding</b> always, when busy and when not answered (<code>*72</code>, <code>*90</code>, <code>*52</code>), <b>do not disturb</b> (<code>*78</code>, <code>*79</code>, <code>*76</code>) and <b>follow-me</b> (<code>*21</code>) with press 1 to take the call, so a mobile's voicemail never answers it</td></tr>
<tr><td colspan="2"><b>Call pickup</b> for a group (<code>*8</code>) or one phone (<code>**</code>), <b>intercom</b> with auto-answer (<code>*80</code>), company and personal <b>speed dials</b> (<code>*0</code>); every code can be changed on one page</td></tr>
<tr><td colspan="2">A <b>staff portal</b>: people sign in with their extension and voicemail PIN to set do not disturb, forwarding, follow-me and speed dials, hear their voicemail, record their greeting and see their calls</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/contact.svg" width="168" height="40" alt="Contact centre"></th><th align="left" valign="middle">Queues, agents and supervisors</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Queues</b>: longest idle, round robin or ring all, wrap-up time, longest wait, and a <b>live wallboard</b></td></tr>
<tr><td colspan="2">Agents log in and pause from their phones (<code>*45</code>, <code>*46</code>) or the screen</td></tr>
<tr><td colspan="2"><b>Sticky routing</b> back to the agent a caller spoke to last, and <b>VIP callers</b> to the front of every queue</td></tr>
<tr><td colspan="2"><b>Callbacks</b> booked by callers or by staff, alerted on every open screen</td></tr>
<tr><td colspan="2">Supervisors <b>listen, whisper and barge</b> on live calls</td></tr>
<tr><td colspan="2">A <b>customer timeline</b>: every call, recording, transcript, voicemail, text and note for one number</td></tr>
<tr><td colspan="2"><b>CRM</b> caller cards and automatic call logging with any system that has a web address</td></tr>
<tr><td colspan="2"><b>Call links</b>: a web address, QR code or website button that rings you from any browser, no app and no number</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/ai.svg" width="168" height="40" alt="AI"></th><th align="left" valign="middle">With the AI service you choose, or one you run yourself</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>AI receptionists</b> that say they are automated, answer from facts you approved, take messages, book callbacks and always reach a person. A set-up that would loop a caller back to an assistant is refused</td></tr>
<tr><td colspan="2"><b>Transcripts and summaries</b> of recorded calls and voicemails, and search across what was said</td></tr>
<tr><td colspan="2"><b>Transfer briefings</b>: a two-sentence summary for whoever a call is transferred to, so callers never repeat themselves</td></tr>
<tr><td colspan="2"><b>Ask in plain words</b> ("forward sales to my mobile after 5 pm"), shown as a proposed change you approve</td></tr>
<tr><td colspan="2">Any OpenAI-compatible service, OpenRouter by default; for a 911 centre, its own government cloud counts as local</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/phones.svg" width="168" height="40" alt="Phones"></th><th align="left" valign="middle">Any SIP phone, a browser, or a mobile</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Automatic set-up</b> for Yealink, Poly, Grandstream (and HT adapters) and Snom over HTTPS, by MAC address or DHCP option 66</td></tr>
<tr><td colspan="2">A <b>browser phone</b> (WebRTC) built on our own ICE, DTLS and SRTP, docked on every screen, with BitVoice's own <b>TURN server</b> for networks that block UDP</td></tr>
<tr><td colspan="2"><b>SIP over WebSocket</b> for WebRTC softphones made with JsSIP, SIP.js and the like</td></tr>
<tr><td colspan="2">A <b>staff phone</b> page for anyone without a desk phone, signed in with the extension's own password</td></tr>
<tr><td colspan="2"><b>iPhone and Android apps</b> that ring like a desk phone even when closed, paired by QR code</td></tr>
<tr><td colspan="2"><b>Microsoft Teams Direct Routing</b>: BitVoice as the tenant's SBC, Teams users and extensions calling each other</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/messaging.svg" width="168" height="40" alt="Fax and texts"></th><th align="left" valign="middle">Fax, analogue lines and business texting</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>T.38 fax relay</b> with G.711 fallback through fax gateways and providers</td></tr>
<tr><td colspan="2"><b>Fax numbers BitVoice answers itself</b>: every fax kept as a PDF and emailed</td></tr>
<tr><td colspan="2"><b>Faxes sent</b> from the web interface or by emailing a document</td></tr>
<tr><td colspan="2">Analogue phones, fax machines, door phones and ISDN through SIP gateways: Grandstream HT, Patton SmartNode, AudioCodes; or telephony cards in the server: analogue ports, T1/E1 PRI and BRI ISDN (caller names, redirecting numbers, transfers handed to the phone company, advice of charge), E&amp;M trunks with Feature Group D, MFC-R2 in nine country variants, and SS7 (ISUP, ITU and ANSI)</td></tr>
<tr><td colspan="2"><b>Business texting</b> from your numbers through TextMagic, Twilio, SignalWire, Telnyx, Bandwidth, Vonage, Plivo, Flowroute, VoIP.ms or your own webhook, several at once, with a guided US 10DLC registration</td></tr>
<tr><td colspan="2">One <b>Inbox</b> for the chat box on your website, <b>WhatsApp</b> (templates included), <b>Facebook Messenger</b> and texts</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/codecs.svg" width="168" height="40" alt="Codecs and media"></th><th align="left" valign="middle">Our own media engine</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>G.711, G.722, Opus, G.729, G.726 (all four rates), AMR and AMR-WB, iLBC, Speex and GSM</b>, converted between any two, so a browser on Opus talks to a carrier on G.729 or an old gateway on G.726; G.723.1 too, through ffmpeg when you allow it</td></tr>
<tr><td colspan="2"><b>SRTP</b> to every phone that encrypts, and SIP over TLS</td></tr>
<tr><td colspan="2">Recording in stereo, a <b>pause for card numbers</b> (<code>*7</code> and <code>*8</code>, PCI DSS), retention, <b>legal holds</b>, and <b>recording consent</b> announcements by region</td></tr>
<tr><td colspan="2">The <b>IMBE</b> P25 voice codec: decoded with code from GopherTrunk and encoded by our own encoder</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/insight.svg" width="168" height="40" alt="Insight"></th><th align="left" valign="middle">Quality, costs and evidence</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Call quality</b> per site and per provider (MOS, jitter, loss) with a <b>diagnosis in plain words</b></td></tr>
<tr><td colspan="2"><b>Call insight</b> for supervisors: talk ratio, talking over, silence, measured from the sound, never an automatic score</td></tr>
<tr><td colspan="2"><b>Costs</b> by department, person and trunk, from your provider's prices, with budgets</td></tr>
<tr><td colspan="2"><b>Spam protection</b> from STIR/SHAKEN results, block lists and caller reputation; <b>branded calling</b> and a reputation watch on your own numbers</td></tr>
<tr><td colspan="2"><b>Call history</b> with recordings, CSV export and search</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/security.svg" width="168" height="40" alt="Security"></th><th align="left" valign="middle">Locked down by default</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Fail-to-ban</b> for addresses that keep failing, and <b>toll-fraud limits</b>: premium-rate numbers refused unless allowed</td></tr>
<tr><td colspan="2"><b>Two-step sign-in</b> with an authenticator app, and <b>single sign-on</b> through any OpenID Connect provider</td></tr>
<tr><td colspan="2">An <b>audit log</b> of every administrative action, and API tokens that are shown once</td></tr>
<tr><td colspan="2">Secrets from <b>AWS Secrets Manager or Azure Key Vault</b>; cryptography only from Go's standard library, never written here</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/operations.svg" width="168" height="40" alt="Operations"></th><th align="left" valign="middle">Running it for real</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Several servers as one phone system</b> over a shared PostgreSQL database: calls carry on when a server stops, and a shared address moves to a running one</td></tr>
<tr><td colspan="2"><b>Hosted organisations</b>: many phone systems on one server, each with its own data (its own folder on SQLite, its own schema on PostgreSQL), web address or domain and administrators; limits on extensions, calls at once, storage and AI use; suspend without deleting. Emergency calls are never refused</td></tr>
<tr><td colspan="2"><b>SQLite</b> on one server, <b>PostgreSQL</b> for several</td></tr>
<tr><td colspan="2"><b>Health</b> page saying what is missing, emergency calling first; a live <b>SIP trace</b>; a <b>call generator</b> that measures what your hardware carries</td></tr>
<tr><td colspan="2"><b>Updates</b> uploaded in the browser, installed when no calls are up, with <b>Go back</b> in one click and an automatic rollback if the new version fails to start</td></tr>
<tr><td colspan="2"><b>Every change is a revision</b>: see what changed, restore with a preview, export and import</td></tr>
<tr><td colspan="2"><b>Prometheus</b> and <b>OpenTelemetry</b> metrics</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/migration.svg" width="168" height="40" alt="Moving in"></th><th align="left" valign="middle">Bring your old system with you</th></tr></thead>
<tbody>
<tr><td colspan="2">Import from <b>FreePBX</b> and <b>Asterisk</b> (pjsip.conf, sip.conf), <b>3CX</b>, <b>Cisco Unified CM</b>, <b>Avaya IP Office and Communication Manager</b> and <b>Mitel MiVoice Business</b></td></tr>
<tr><td colspan="2">Every import shows exactly what would change first, and becomes a revision you can undo</td></tr>
<tr><td colspan="2"><b>Asterisk AMI and ARI</b> compatibility, so tools written for Asterisk keep working</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/cloud.svg" width="168" height="40" alt="Clouds and code"></th><th align="left" valign="middle">Automation and clouds</th></tr></thead>
<tbody>
<tr><td colspan="2">A <b>REST API</b> behind every screen, described at <code>/api/openapi.json</code></td></tr>
<tr><td colspan="2"><b>Configuration from Git</b>, and a <b>Terraform</b> example with the generic REST provider</td></tr>
<tr><td colspan="2"><b>AWS and Azure</b> images and templates; recordings, voicemail and backups copied to <b>S3 or Azure Blob Storage</b> (our own SigV4 and Entra code, no cloud SDKs)</td></tr>
</tbody></table>

## Public safety: lab and design-partner track

The engine that runs the phone system also carries three public-safety profiles. **They are not certified
for live 911 or radio dispatch, and they have not yet run against a live ESInet, P25 RFSS or PSAP CAD.** They
are tested against a real ISSI capture, NENA sample data and stand-ins (see [How it is tested](#how-it-is-tested)).
They stay off the menu until **Public-safety profiles (lab)** is switched on in Settings, and their screens say
they are a lab track.

**We are looking for design partners:** a PSAP, a 911 authority or a radio shop willing to run BitVoice in a
lab or in shadow mode beside their current equipment. One real integration is worth more to us than any
feature on this list. [Open an issue](https://github.com/BioDream-LLC/bitvoice/issues) titled "Design partner" and we will get in touch.

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/psap.svg" width="168" height="40" alt="911 profile"></th><th align="left" valign="middle">NG911 call handling for a PSAP</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>NENA i3 calls</b> from the ESInet with the caller's <b>PIDF-LO location</b> and the network's additional data, shown to the call-taker as the call rings</td></tr>
<tr><td colspan="2"><b>ALI spill</b> over TCP and <b>EIDO</b> over HTTPS to any CAD when a call is answered</td></tr>
<tr><td colspan="2">Location <b>rebids</b> and <b>transfers between PSAPs</b> with the data kept</td></tr>
<tr><td colspan="2">The <b>NENA i3 log</b>, and <b>answer-time reports</b> by call-taker and hour against your goal, as CSV too</td></tr>
<tr><td colspan="2"><b>TTY</b> (deaf callers' text telephones) decoded and answered on every 911 call, <b>real-time text</b> (RFC 4103) and <b>text to 911</b> from a text control centre (MSRP, over TLS too), in one conversation on the call</td></tr>
<tr><td colspan="2"><b>Caller video and photos</b> by a link, no app: live camera (WHIP), photos, clips and <b>recordings</b> of the live video kept with their <b>SHA-256</b>, time and GPS, and named in the EIDO to CAD; opened <b>blurred</b> until revealed, with an <b>exposure</b> report for the wellness programme; CAD and units watch by link (WHEP)</td></tr>
<tr><td colspan="2">A <b>map</b> of the caller's position and its uncertainty, from your own tiles (works offline)</td></tr>
<tr><td colspan="2"><b>Legacy 911</b>: CAMA trunks on telephony cards, and location looked up by callback number for calls that bring none, by HELD or the old <b>ALI link</b> (network or serial port, read by your provider's layout); ALI spill to CAD over the network or a serial port</td></tr>
<tr><td colspan="2"><b>Report layouts</b>: lay out your state's form (rows by day, week, month, hour or call-taker; your columns and headings) and run it as a table, a spreadsheet or a page to print</td></tr>
<tr><td colspan="2"><b>Test calls</b> (urn:service:test.sos) answered by themselves, as NENA i3 section 9 asks</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/radio.svg" width="168" height="40" alt="Radio console"></th><th align="left" valign="middle">Radio dispatch on the same headset as the phones</th></tr></thead>
<tbody>
<tr><td colspan="2"><b>Radio-over-IP channels</b> through gateways (JPS, Omnitronics, Cisco, Telex and others): hold to talk, monitoring, patches</td></tr>
<tr><td colspan="2"><b>P25 talkgroups over the ISSI</b>: the console joins a talkgroup's home RFSS, asks for the floor, shows the talking unit and its emergency flag</td></tr>
<tr><td colspan="2"><b>Encrypted talkgroups</b> (TIA-102.AAAD): AES-256, DES-OFB and triple DES. Keys are never shown again once saved; without a key the audio is muted, never noise</td></tr>
<tr><td colspan="2"><b>Private calls</b>: a dispatcher calls a radio by unit ID, and radios call the console</td></tr>
<tr><td colspan="2"><b>Alert tones</b>: steady, warble, pulsed and beep</td></tr>
<tr><td colspan="2">Every transmission <b>recorded in a radio log</b> with unit, dispatcher, tone, emergency and encryption, played back from the log</td></tr>
<tr><td colspan="2"><b>Emergencies stay raised</b> until a dispatcher acknowledges them; <b>simulselect</b>; <b>radio names</b> (Engine 5, not 1205); <b>radio positions</b> from your radio data gateway on a map</td></tr>
<tr><td colspan="2"><b>Radio data to CAD</b>: who is talking where, emergencies and each transmission, as JSON</td></tr>
</tbody></table>

<table width="100%">
<thead><tr><th align="left" valign="middle" width="176"><img src="docs/assets/chips/core.svg" width="168" height="40" alt="NG911 core"></th><th align="left" valign="middle">NG911 Core Services for a 911 authority</th></tr></thead>
<tbody>
<tr><td colspan="2">An <b>ESRP</b> with policy rules by service, PSAP, schedule and missing or suspicious location, with overflow and a default PSAP</td></tr>
<tr><td colspan="2">An <b>ECRF</b> answering <b>LoST</b> (RFC 5222) from your service boundaries</td></tr>
<tr><td colspan="2">An <b>LVF</b> validating civic addresses against NENA address points and road centrelines</td></tr>
<tr><td colspan="2">A <b>BCF</b> at the ESInet edge: calls per minute and at once per source, deny lists, <b>STIR/SHAKEN</b> checks, suspicious calls marked for the policy rules</td></tr>
<tr><td colspan="2">A <b>LIS</b> answering HELD by telephone number, a <b>forest guide</b> for places outside your areas, and an <b>i3 logging service</b> that takes signed log events, checked against the <b>PSAP Credentialing Agency</b></td></tr>
<tr><td colspan="2">The <b>NENA i3 web services</b>: a <b>Policy Store</b> whose signed routing policies the ESRP runs, <b>discrepancy reports</b>, <b>bad actors</b>, <b>geocode</b> and <b>MSAG</b> conversion, an <b>ADR</b>, the <b>service/agency locator</b>, <b>dequeue registration</b> and a <b>test call generator</b>, each checked against NENA's published definitions</td></tr>
</tbody></table>

**Radio console**: radio channels and P25 talkgroups beside the phones, with the radio log.

<img src="docs/assets/screens/radio.png" alt="The radio console with EMS, Fire dispatch and Police 1 channels, hold to talk and patch buttons, and the radio log with recorded transmissions." width="100%">

---

## What it looks like

Everything below is the web interface inside the binary, showing a sample clinic with phones registered and
calls in progress.

**Dashboard**: calls now, phones online, trunks, and what needs attention, in plain words.

<img src="docs/assets/screens/dashboard.png" alt="The dashboard for a sample clinic: four calls now, eight of eight phones online, two of two trunks up, a chart of calls in the last 24 hours and the calls happening now." width="100%">

**Live calls**: every call in progress, with the codec, packet loss, jitter and an audio score for each side.

<img src="docs/assets/screens/calls.png" alt="Live calls: four calls into the Appointments queue, each showing both sides with codec, loss, jitter and a quality score of 4.4." width="100%">

**Queues**: a live wallboard with callers waiting, the longest wait and each agent's state.

<img src="docs/assets/screens/queues.png" alt="The Appointments queue with one caller waiting, three answered, and three agents on calls." width="100%">

**Call flow**: where calls from outside go, step by step, with a simulator that shows the route a call would take.

<img src="docs/assets/screens/flow.png" alt="The call flow diagram: the main number goes to clinic hours, then the main menu, then the Appointments queue, Billing, the clinical team and the front office, each ending at voicemail." width="100%">

**Call history**: every finished call, with recordings, quality and search across what was said.

<img src="docs/assets/screens/history.png" alt="Call history listing answered calls with their talk time and quality scores." width="100%">

**NG911 Core Services**: the ECRF, the ESRP's PSAPs and policy rules.

<img src="docs/assets/screens/core.png" alt="The core services screen with the ECRF and ESRP settings and two PSAPs, East and West." width="100%">

---

## Download

**Version 0.1.4**: one binary, nothing to install. Each archive holds the program, the licence, the notice
and the manual as a PDF.

| Platform | Architecture | Download |
|---|---|---|
| **Windows** | x64 (Intel/AMD) | [`bitvoice-v0.1.5-windows-amd64.zip`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-windows-amd64.zip) |
| **Windows** | ARM64 | [`bitvoice-v0.1.5-windows-arm64.zip`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-windows-arm64.zip) |
| **macOS** | Apple Silicon | [`bitvoice-v0.1.5-darwin-arm64.tar.gz`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-darwin-arm64.tar.gz) |
| **macOS** | Intel | [`bitvoice-v0.1.5-darwin-amd64.tar.gz`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-darwin-amd64.tar.gz) |
| **Linux** | x86-64 | [`bitvoice-v0.1.5-linux-amd64.tar.gz`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-linux-amd64.tar.gz) |
| **Linux** | ARM64 / aarch64 | [`bitvoice-v0.1.5-linux-arm64.tar.gz`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/bitvoice-v0.1.5-linux-arm64.tar.gz) |

**On Windows, unzip and double-click `bitvoice.exe`.** A window opens and your browser opens on the setup
wizard. Keep the window open while you use BitVoice: closing it stops it. Windows asks once whether BitVoice
may use the network; allow it on private networks so phones can connect. The data is kept in
`%APPDATA%\BitVoice`. The x64 build runs the full test suite on Windows Server; the ARM64 build is compiled
the same way but has not been run on an ARM Windows machine. BitVoice does not install itself as a Windows
service yet.

**Verify what you downloaded** against
[`SHA256SUMS`](https://github.com/BioDream-LLC/bitvoice/releases/download/v0.1.5/SHA256SUMS):

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
```

**Signed releases.** From v0.1.3, every program carries BioDream's release key and comes with its
signature beside it in the archive (`bitvoice.sig`, or `bitvoice.exe.sig` on Windows). A release build's **Updates** page installs only programs signed
with that key, so a forged or damaged file is refused. The public key, for checking a signature yourself
(ed25519, base64):

```
M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY=
```

v0.1.2 and earlier carry no key: their Updates page accepts any program, signed or not. Upgrade to v0.1.3
or later by hand once to get the check.

<details>
<summary><b>Other ways to install</b></summary>

```sh
# Go 1.26 or newer
go install github.com/biodream-llc/bitvoice/cmd/bitvoice@latest

# From source
git clone https://github.com/BioDream-LLC/bitvoice && cd bitvoice && make build
```

The binaries are pure Go with no C libraries, so they run on any Linux, including Alpine and distroless
images. `deploy/cloud` has a systemd unit and cloud-init file; `deploy/lab` runs several servers as one
phone system in Docker on one computer.

</details>

## Quick start

```sh
# 1. Start it. The web interface is inside the binary.
./bitvoice

# 2. A browser opens on the setup wizard (or open https://127.0.0.1:8443). HTTPS only: until you install
#    your own certificate, the browser warns once about BitVoice's self-signed one; continue to the site.
#    Name your organisation, choose the administrator's password, add your first phone.

# 3. Point a SIP phone at this computer, port 5060, with the extension's number and password.
#    Or press the phone button at the bottom of any screen to use the browser as a phone. Dial *43 for the echo test.
```

Data lives in your user configuration folder (`BitVoice`), or wherever `-data` says. Other computers reach
the web interface over HTTPS on port 8443. `./bitvoice -h` lists the options; every one of them is also a
setting in the web interface.

## How it is tested

- **On every push** ([CI](https://github.com/BioDream-LLC/bitvoice/actions/workflows/ci.yml)): the Go tests
  on Linux with the race detector on both SQLite and PostgreSQL, on Windows and on macOS; and the browser
  tests below. Fuzzing covers the SIP, SDP, STUN and SRTP parsers.
- **Every setting has a screen**, checked on every push: a test walks the whole configuration and fails if any
  setting is not named in the web interface, so nothing can quietly become file-only or API-only.
- **Browser tests in Chromium** that drive every screen the way a person would: the setup wizard, every
  form, real **WebRTC calls** between browser phones and the call engine with sound checked in both
  directions, a fax sent, and the radio console joined with the browser phone.
- **P25** checked byte for byte against a real ISSI capture (from NIST's public-domain ISSI tester), and the
  encryption against the standard's own worked examples.
- **NG911** checked against NENA sample data and stand-ins for the ESInet, ESRP and CAD.
- **SIP trunks** checked against a real Asterisk 23.5 playing the carrier, with another Asterisk as a desk phone:
  registration with digest authentication; calls in to the main number and out through the trunk, with sound
  (no loss, MOS 4.4); DTMF both ways (RFC 4733); the caller's number and name; busy and failed numbers passed
  back as such. Real carriers are not yet tried.
- **IAX2** checked against a real Asterisk 23: registration, calls both ways with sound, busy; **GSM** decoded
  sample for sample as ffmpeg does, and **G.726** streams exchanged with ffmpeg.

### Capacity

Measured with BitVoice's own call generator (Health, **Call generator**) on an Apple M4 desktop (10 cores,
16 GB), v0.1.5: one extension calls another, which the generator answers and sends the audio back, G.711
with media relayed through BitVoice, 45 seconds a call. The generator runs inside the same process, so each
call is carried three times over (caller, PBX, answerer): the PBX alone needs less.

| Calls at once | Answered | Set-up time (p95) | Audio loss | MOS | CPU (peak, of one core) | Memory |
|---|---|---|---|---|---|---|
| 50 | 50 of 50 | 3.9 ms | 0% | 4.41 | 81% | 61 MB |
| 100 | 100 of 100 | 3.4 ms | 0% | 4.41 | 137% | 76 MB |
| 200 | 200 of 200 | 4.8 ms | 0% | 4.41 | 240% | 111 MB |

200 is the generator's own ceiling, not BitVoice's. Your numbers depend on the codec, conversion,
recording and the network: run the generator on your own server before you go live.

The **iPhone app** is tested in the iOS Simulator against a real BitVoice: paired by link, rung by a call,
answered and hung up (`mobile/ios/BitVoiceUITests`). The phone's own call screen and push wake-up need a
real phone and are not yet tried on one; the Android app is built but not yet run.

**Status, plainly.** The phone system is working. The 911, radio console and NG911 Core Services profiles
are built and tested against stand-ins and real captures, **not yet against a live ESInet, P25 system or
PSAP CAD, and not certified for live 911 or public-safety use.** P25 Phase 2 half-rate voice (AMBE+2) is
not included: it is patented until 2028. Phase 2 systems whose ISSI talkgroups carry full-rate voice work
today.

## Documentation

| | |
|---|---|
| [**Website**](https://bitvoice.org) | Features, the FreePBX comparison, how it is tested, downloads; built from this README and the manual by `make site` |
| [**Manual**](https://biodream-llc.github.io/bitvoice/manual/) | 75 chapters in six parts, with 63 diagrams, step-by-step set-up and reference tables. Also `docs/manual/index.html` in a checkout |
| [**Manual (PDF)**](docs/manual/bitvoice-manual.pdf) | The same, about 90 pages with a contents page, for printing; also in every release archive |
| **Help in the program** | Every screen has a Help button that opens its article; <kbd>⌘K</kbd> searches screens and articles |
| **API** | `/api/openapi.json` on any running BitVoice describes every call the web interface makes |

## FAQ

<details>
<summary><b>Is BitVoice really free? What is the licence?</b></summary>

Yes. **Apache License 2.0**: commercial use, modification, distribution and private use are all permitted,
with no fee, no paid modules, and no per-extension, per-agent or per-position pricing. See [LICENSE](LICENSE)
and [NOTICE](NOTICE). BioDream LLC earns from hosting and support subscriptions only.

</details>

<details>
<summary><b>Is it built on Asterisk or FreeSWITCH?</b></summary>

No. The SIP stack, RTP, SRTP, the media engine, WebRTC (ICE, DTLS and SRTP), the P25 ISSI and the IMBE
encoder are BitVoice's own Go code. The only outside code is six pinned, pure-Go libraries listed in
[NOTICE](NOTICE): the JavaScript and Lua engines, the SQLite and PostgreSQL drivers, and the Opus and G.729
codecs, plus GopherTrunk's IMBE decoder.

</details>

<details>
<summary><b>Can I move from FreePBX, Asterisk, 3CX, Cisco, Avaya or Mitel?</b></summary>

Yes. Upload the exported files on **Import**; BitVoice shows exactly what would change before anything is
applied, and the import is a revision you can undo. Tools that talk to Asterisk's AMI or ARI can control
BitVoice too.

</details>

<details>
<summary><b>Does it do what FreePBX does?</b></summary>

The everyday features, yes: extensions, ring groups, queues, menus, schedules, voicemail, recording, paging,
parking, conference rooms, forwarding, do not disturb, follow-me, call pickup, intercom, speed dials, a staff
portal, SIP, IAX2 and H.323 trunks, Skinny (SCCP) phones and MGCP gateways, day/night toggles, PIN sets,
caller ID lookup, video conference rooms (follow the speaker, or a grid of everyone), an outbound dialer, MP3 and streamed hold music, Let's Encrypt
certificates, AudioSocket voice bots and ARI external media, and hosting several organisations on one server. Feature codes match the usual FreePBX ones (<code>*72</code>, <code>*78</code>,
<code>*8</code> and so on) and can be changed. Telephony cards (DAHDI analogue ports; T1/E1 PRI and BRI; E&amp;M,
MFC-R2 and SS7) work through <code>bitvoice-dahdi</code>, a separate program in the Linux download. G.723.1 is
converted through ffmpeg when you switch that on.

</details>

<details>
<summary><b>Can I host phone systems for several customers on one server?</b></summary>

Yes. **Organisations** adds one: it gets its own empty phone system, web address (<code>/t/name/</code> or
its own domain) and administrators, and cannot see any other. Phones register to its domain or as
<code>name.100</code>; calls from a shared provider account reach the organisation whose numbers they are for.
Each can be limited (extensions, calls at once, storage, AI use) or suspended.

</details>

<details>
<summary><b>Can a 911 centre use it today?</b></summary>

Not for live calls yet. The 911 profile is built and tested against stand-ins and NENA sample data, but it
has not run on a real ESInet or been certified. We are looking for PSAPs and 911 authorities willing to run
it in a lab or in shadow mode beside their current equipment.

</details>

<details>
<summary><b>Does the radio console work with my P25 system?</b></summary>

It joins talkgroups over the ISSI as a console subsystem, with AES-256, DES or triple DES encryption,
private calls and alert tones. It has been tested against a real ISSI capture and stand-ins, not yet a live
RFSS. Phase 2 systems work when their ISSI talkgroups carry full-rate (Phase 1) voice, which is common;
native half-rate voice is not supported because its vocoder is patented until 2028. Radio-over-IP gateways
connect any other radio system.

</details>

<details>
<summary><b>Do I need a database server?</b></summary>

No. One server keeps everything in a SQLite file. For several servers working as one phone system, point
them at one PostgreSQL database.

</details>

<details>
<summary><b>Does my call data go anywhere?</b></summary>

No, unless you switch on a feature that sends it: an AI service you choose, a texting provider, an archive
bucket, a CRM. Each is off until you set it up, and each says what it sends.

</details>

<details>
<summary><b>Who is behind it?</b></summary>

BioDream LLC, which also makes [Perfuse](https://github.com/BioDream-LLC/perfuse), the open-source healthcare
integration engine. Hosting and support subscriptions are available for organisations that want them.

</details>

## Contributions are not accepted

BitVoice is developed and maintained by BioDream LLC, and it stays that way. **Pull requests will not be
reviewed or merged.** This software is held to the 911 reliability bar, and that standard is easy to state and
slow to enforce across contributors.

**Bug reports are wanted.** [Open an issue](https://github.com/BioDream-LLC/bitvoice/issues) with the output of
`bitvoice -version` and what happened. The Apache licence still lets you take a copy, change it and publish
the result: what is closed is the path back into this repository, not what you may do with your own copy.

**Security problems:** please report them privately through this repository's Security tab ("Report a
vulnerability"), not in a public issue.

### Building it yourself

```sh
make check build     # Go tests and the binary in bin/
make web             # rebuild the web interface (needs Node and pnpm; the built copy is committed)
make e2e             # browser tests, including WebRTC calls in Chromium
```

## Licence

[Apache License 2.0](LICENSE). Copyright 2026 BioDream LLC. See [NOTICE](NOTICE).

BitVoice is not affiliated with or endorsed by Sangoma (Asterisk, FreePBX), 3CX, Cisco, Avaya, Mitel,
Microsoft, NENA, TIA or any radio manufacturer. Those names are used only to describe what BitVoice works
with. Project 25 standards are published by the Telecommunications Industry Association.

<div align="center">

<details>
<summary><b>Keywords</b></summary>

<sub><b>Phone system:</b> open source PBX · IP PBX · VoIP PBX · SIP server · business phone system · cloud PBX · self-hosted phone system · Asterisk alternative · FreePBX alternative · 3CX alternative · FreeSWITCH alternative · Go PBX · Golang SIP · WebRTC softphone · auto attendant · IVR · voicemail to email · call recording · ring groups · hunt groups · call parking · paging · BLF · provisioning · Yealink · Poly · Grandstream · Snom</sub><br><br>
<sub><b>Contact centre:</b> ACD · call queues · wallboard · callbacks · supervisor barge · whisper · call transcripts · call summaries · CRM integration · screen pop · click to dial · call links · website calling</sub><br><br>
<sub><b>911:</b> NG911 · NENA i3 · PSAP · call handling equipment · CPE · ESInet · PIDF-LO · ALI · EIDO · CAD integration · i3 logging · Kari's Law · RAY BAUM'S Act · ESRP · ECRF · LoST · LVF · BCF · NG911 core services</sub><br><br>
<sub><b>Radio:</b> radio dispatch console · P25 · Project 25 · ISSI · CSSI · RoIP · radio over IP · talkgroups · IMBE · P25 encryption · AES-256 · unit to unit · alert tones · radio logging</sub><br><br>
<sub><b>Media and fax:</b> Opus · G.729 · G.722 · G.711 · transcoding · SRTP · SIP TLS · T.38 · fax to email · email to fax · SMS · 10DLC · Microsoft Teams Direct Routing · SBC</sub><br><br>
<sub><b>Deployment:</b> single binary · Go · Apache 2.0 · SQLite · PostgreSQL · high availability · active-active · Prometheus · OpenTelemetry · Terraform · AWS · Azure · on-premise · air-gapped</sub>
</details>

</div>
