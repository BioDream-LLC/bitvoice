// The built-in manual. Each article is short markdown (headings, paragraphs, lists, `code`, **bold**,
// [links](#/route)); `route` ties it to the screen whose help button opens it.

export interface Article { id: string; title: string; route?: string; keys: string; body: string }

export const manual: Article[] = [
  {
    id: 'start', title: 'Getting started', route: '/', keys: 'setup first steps overview begin dashboard home',
    body: `BitVoice is a complete phone system in one program. Everything is set up here, in the browser.

## The first hour
1. **Extensions**: one per person or desk. Each has a number and a SIP password. [Open Extensions](#/extensions)
2. **Phones**: connect a desk phone, softphone or the browser phone to each extension. [Open Phones](#/phones)
3. **Trunk**: your phone company account, so you can call outside numbers and be called. [Open Trunks](#/trunks)
4. **Call routing**: where calls from outside go (a person, a ring group, a menu), and which trunk outside calls use. [Open Call routing](#/routes)
5. **Health** tells you what is still missing, emergency calling first. [Open Health](#/health)

## Where it runs
BitVoice runs on Linux, macOS and Windows. On Windows, double-click \`bitvoice.exe\`: keep the window it opens while you use BitVoice (closing it stops BitVoice), and when Windows asks whether it may use the network, allow private networks so phones can connect. Its data is in \`%APPDATA%\\BitVoice\`, on macOS in \`~/Library/Application Support/BitVoice\`, on Linux in \`~/.config/BitVoice\`, or wherever \`-data\` says.

## Finding your way
- Press **⌘K** (Ctrl+K) anywhere to jump to a screen, or type an extension's name or number to open it.
- Every screen has a **Help** link at the top right that opens its page of this manual.
- **Changes** keeps every version of the configuration: you can see what changed and undo it.`,
  },
  {
    id: 'extensions', title: 'Extensions', route: '/extensions', keys: 'user person desk number sip password device door speaker analog',
    body: `An extension is a number for a person or a desk. Phones sign in with the extension number and its **SIP password**.

## Fields worth knowing
- **911 location**: building, floor and room. It is sent with emergency calls, so fill it in for every desk.
- **Ring for**: how long phones ring before **If nobody answers** happens (voicemail by default).
- **Can call outside numbers**: off keeps the phone to internal calls and emergency numbers.
- **Encrypt calls to this phone**: media encryption (SRTP). Use it with TLS so the keys stay private.

## Connected device
- **Phone**: a desk phone, softphone or app.
- **Door station**: an intercom. Whoever answers presses the key code, or **Open door** on Live calls. With a door controller address, BitVoice calls that address (2N, Axis, Akuvox, relay boards); without one, the code is sent to the station, which opens the door itself.
- **Overhead speaker**: calls to it are answered at once and heard over the speaker.
- **Analog adapter port**: an old phone, fax or alarm panel on an adapter. Calls use G.711 only, which faxes need.`,
  },
  {
    id: 'phones', title: 'Connecting phones', route: '/phones', keys: 'provisioning yealink polycom poly grandstream snom mac auto configure softphone',
    body: `Any SIP phone or app works: point it at this server's address and port 5060, with the extension number as user and its SIP password.

## Automatic set-up (provisioning)
Add the phone's MAC address on **Phones** and choose its extension. Yealink, Poly, Grandstream (including HT analog adapters) and Snom phones then fetch their settings from BitVoice over HTTPS: set the provisioning address shown on the screen in the phone, or in your DHCP server (option 66).

## The browser phone
The phone icon at the bottom of the screen makes this browser a phone for an extension, with a microphone.

## Staff phone
Open \`#/phone\` on any computer or mobile browser: people sign in with their extension and SIP password, without an administrator account.`,
  },
  {
    id: 'mobile', title: 'Mobile apps', keys: 'iphone android app push pair qr ios callkit',
    body: `The BitVoice apps for iPhone and Android make a phone an extension's phone, ringing like a desk phone even when the app is closed.

1. In [Settings](#/settings), **Mobile apps**: choose **Push relay** (BioDream's, with hosting or support, or your own \`bvpush\`) and enter its address, key ID and secret, or **Own apps** with your Apple and Google credentials. Set **Address the apps use** to this server's https address from anywhere.
2. Open the extension, press **Add a phone**, and scan the QR code in the app. The code works once, for ten minutes.

When a call comes in, BitVoice wakes the app; it connects, rings on the phone's own call screen, and is answered like any other phone of the extension. The extension's page lists its paired phones, when each was last seen and woken, and any push problem; **Unpair** stops a phone ringing.`,
  },
  {
    id: 'trunks', title: 'Trunks', route: '/trunks', keys: 'carrier provider sip trunk line register channels codecs',
    body: `A trunk is your account with a phone company (a SIP provider). It carries calls to and from outside numbers.

- **Register**: most providers need it; BitVoice signs in every few minutes and Health shows whether it worked.
- **Also accept calls from**: extra provider addresses or networks that send calls in.
- **Calls at once**: the limit your plan allows. Calls beyond it go to the next trunk in the route.
- **Codecs**: leave it on *same as Settings*, or choose what this provider supports, best first.

Calls from outside arrive through **Call routing**, by the number dialled.`,
  },
  {
    id: 'routes', title: 'Call routing', route: '/routes', keys: 'inbound outbound did number pattern dial plan route prepend emergency',
    body: `## Incoming
Each incoming route matches the number that was called (or any number) and sends the call to a destination: an extension, ring group, queue, menu, schedule, script or voicemail. Numbers match in any form: +1 212 555 0100, 2125550100 and 12125550100 are the same.

## Outgoing
Outgoing routes match what people dial, by pattern, and send it to trunks in order: if the first is down or full, the next is tried.

- \`X\` is any digit, \`Z\` 1 to 9, \`N\` 2 to 9, \`[1-5]\` one of a set, and a final \`.\` one or more further digits. \`NXXNXXXXXX\` is a ten-digit North American number.
- **Remove** takes digits off the front; **Then add in front** puts some on (a country code).

## Emergency numbers
The emergency numbers always work. A route that names the number exactly is used first; with none, every working trunk is tried. Health warns when no trunk can carry them.`,
  },
  {
    id: 'ringgroups', title: 'Ring groups', route: '/ring-groups', keys: 'hunt group ring all one after another sales team',
    body: `A ring group is one number that rings several extensions: **all at once**, or **one after another** in the order listed (move members with the arrows, or Alt+↑ and Alt+↓). Calls nobody answers go to **If nobody answers**.`,
  },
  {
    id: 'queues', title: 'Queues', route: '/queues', keys: 'sticky vip priority returning caller acd contact centre agents waiting longest idle wallboard',
    body: `A queue holds callers in order, with music, and rings the next free agent.

- **Longest idle** sends the call to the agent who has waited longest; **round robin** takes turns; **ring all** rings every free agent.
- **Wrap-up** gives an agent time after a call before the next.
- **Longest wait**: callers who wait longer go to the queue's timeout destination. 0 waits for ever.

The **Queues** screen is a live wallboard: who is waiting, for how long, and each agent's state.

## Agents
Agents dial \`*45\` to log in or out of every queue, and \`*46\` to pause (a break) and resume; paused and logged-out agents are not offered calls. The wallboard has the same buttons, and shows why an agent paused.

## Supervisors
On [Live calls](#/calls), **Supervise** rings a supervisor's phone; when they answer they **listen** (heard by nobody), **whisper** (heard by the agent only) or **barge** (heard by both), and can switch between them during the call.

## Sticky queues and VIPs
Switch on **Sticky** in a queue and a returning caller (one answered in the last 30 days, or as many as you set) is offered first to the agent who answered them last time, if that agent is free; if they do not answer, the queue goes on as usual. **VIP callers**, at the foot of Queues, go to the front of every queue they call, after any VIP already waiting, and are marked VIP on the board.`,
  },
  {
    id: 'menus', title: 'Menus (auto attendant)', route: '/menus', keys: 'ivr press 1 prompt greeting keypad auto attendant',
    body: `A menu answers, plays a prompt and sends the caller where they press: 1 for sales, 2 for support. Record the prompt in the browser, or upload one. Callers can also type an extension number if you allow it. The prompt repeats a few times; callers who press nothing then go to the menu's no-key destination, or hear goodbye.`,
  },
  {
    id: 'schedules', title: 'Schedules', route: '/schedules', keys: 'opening hours holiday closed night service time zone',
    body: `A schedule sends calls one way when you are open and another when you are closed. Hours may cross midnight (22:00 to 06:00). **Holidays** count as closed all day. The time zone is the one in Settings.`,
  },
  {
    id: 'scripts', title: 'Scripts (JavaScript and Lua)', route: '/scripts', keys: 'code javascript lua route logic api program',
    body: `A script decides where a call goes when the choices above are not enough. It runs for at most 200 ms; if it fails or takes too long, the call goes to the script's **fallback**, so a mistake never loses a call.

## What a script sees
\`call\` has: \`from\`, \`fromName\`, \`to\`, \`direction\` (inbound or internal), \`trunk\`, \`weekday\` (0 is Sunday), \`hour\`, \`minute\`, \`date\` (YYYY-MM-DD).

## What it returns
A destination: \`{type, target}\` where type is \`extension\`, \`ringgroup\`, \`queue\`, \`menu\`, \`schedule\`, \`voicemail\` or \`hangup\`, and target is its number. Returning nothing uses the fallback. \`log("text")\` writes to the script's run list.

\`\`\`
if (call.from.startsWith("212")) return { type: "ringgroup", target: "600" }
if (call.weekday === 0) return { type: "voicemail", target: "100" }
\`\`\`

**Test** runs the script with a call you describe, without placing one.`,
  },
  {
    id: 'voicemail', title: 'Voicemail', route: '/voicemail', keys: 'messages mailbox greeting pin email transcript',
    body: `Each extension can have a mailbox. Callers hear its greeting (record one in the browser or from the phone), then leave a message. Messages show here and on the phone's message light, and can be emailed. With AI switched on (Settings), messages are written out and summarised. **Sound clean-up** (Settings) removes background noise as messages are saved.`,
  },
  {
    id: 'recording', title: 'Call recording and card payments', keys: 'record pci pause resume card payment compliance retention',
    body: `Recording is set in [Settings](#/settings): off, every call, or calls of extensions marked **Record calls**. Recordings play from **Call history**.

## Card payments (PCI DSS)
Before a caller reads out a card number, the person on the extension presses \`*7\`; \`*8\` resumes. **Pause recording** on Live calls does the same. The paused stretch is silent in the recording and listed in the call history.

## How long things are kept
Settings sets how many days recordings, call history and voicemail are kept. Anything under a [legal hold](#/holds) is kept whatever its age. Recording laws differ: some places need everyone on the call to agree.`,
  },
  {
    id: 'metrics', title: 'Metrics: Prometheus, OpenTelemetry and Grafana', route: '/settings', keys: 'metrics prometheus grafana opentelemetry otel otlp monitoring dashboard scrape observability',
    body: `**Prometheus** scrapes \`/metrics\` on the web address. Make an API token under Administrators (a viewer token is enough) and give it to Prometheus as its bearer token:

\`\`\`
scrape_configs:
  - job_name: bitvoice
    scheme: https
    authorization: { credentials: bv_... }
    static_configs: [{ targets: ['pbx.example.com'] }]
\`\`\`

It reads calls in progress (by state), calls ended (by direction and outcome), talk time, phones registered, each trunk's state and round trip, callers waiting and agents in each queue, sound packets received and lost, a histogram of call quality scores (MOS), and callbacks booked. Counters start again from zero when BitVoice restarts, as Prometheus expects.

**OpenTelemetry**: in Settings, Metrics, enter a collector's OTLP/HTTP address (and a header such as Authorization, with its value) to have the same measurements pushed every minute (or as often as you set, from 15 seconds). **Push now** checks the saved settings.

**Grafana dashboard** downloads a ready-made dashboard: import it in Grafana and pick your Prometheus data source.`,
  },
  {
    id: 'crm', title: 'CRM: caller cards and call logging', route: '/settings', keys: 'crm salesforce hubspot zoho pipedrive webhook screen pop caller card lookup log calls integration',
    body: `Connect a customer system in [Settings](#/settings), CRM. It works with any system that has a web address, without code written for each product.

## Caller cards
While a call from outside rings, Live calls shows a card: who it is (from your CRM, or the extension's name), how often they have called in 90 days, any [callback](#/callbacks) they are waiting for, and the summary of their last transcribed call. The browser phone shows a shorter card to the person answering: who it is, past calls and waiting callbacks, but not other people's call summaries.

For the name, set **Lookup address**: BitVoice asks it (GET) with the caller's number in place of {number}, sending **Lookup header** with its value (a vault reference works), and expects JSON with name, company, url and notes; 404 means unknown. Answers are kept for five minutes. **Open in CRM link** puts a link to the caller on the card. Use **Look it up** to try a number before saving.

## Call logging
Set **Call log address** and **Signing secret**, and every call is sent there as it ends (event call.ended), again with its summary, action items and transcript once transcribed (call.summarized), and each booked callback (callback.new). Each POST carries X-BitVoice-Event, X-BitVoice-Timestamp and X-BitVoice-Signature: sha256= followed by the HMAC-SHA256, with the secret, of the timestamp, a dot and the body. A failed delivery is tried four times over about a minute; if it still fails, Health says so. **Send a test event** checks the address and the secret.`,
  },
  {
    id: 'servers', title: 'Several servers as one phone system', route: '/servers', keys: 'servers cluster failover high availability redundancy second server backup shared database several servers shared address virtual ip vip floating ip dns srv takeover handover standby one at a time',
    body: `Two or more BitVoice servers that share one PostgreSQL database work as one phone system:
- A call reaches a phone **whichever server it is registered with**: ring groups, queues, transfers to an extension and calls from provider lines all ring phones on every server.
- **Settings** changed on one server apply on all within five seconds; users, contacts, call history, recordings' transcripts and texts are shared.
- **Busy lamps** (BLF) show phones on every server ringing and busy; **parking** slots are shared, so a call parked on one server is picked up from a phone on another; **paging** reaches phones on every server; **texts** reach desk phones wherever they are registered.
- A queue agent on a call on one server counts as busy on the others, and a provider line's channel limit counts calls on every server.
- **Live calls** on each server lists the calls on the others too (read-only: hang up or listen in on the server that has the call).
- The call record is written once, by the server that has the caller, and a call is recorded there too.

**Setting it up**
1. Move the first server's data into PostgreSQL: Settings, **Database**.
2. Start each other server with the same database: \`bitvoice -database postgres://…\` (or the BITVOICE_DATABASE environment variable). It joins at once; **Servers** lists it.
3. On **Servers**, check where each server is reached (its address and SIP port on the network the servers share), and give each a name.
4. Tell phones how to find the servers (below).
5. Provider lines (below).

**How phones find the servers.** Phones BitVoice sets up (Yealink, Polycom, Grandstream) are given another running server as their backup by themselves. For the others, Snom and phones set up by hand, use one of these on **Servers**:
- **A DNS name for every server**: a name such as pbx.example.com with SRV records (_sip._udp, _sip._tcp, _sips._tcp) listing each server. Phones BitVoice sets up are given the name, and Servers checks the records against the running servers, lists what is missing and gives the records to create, ready to copy.
- **A shared address**: an unused address on the servers' network, such as 192.168.1.50/24. One running server holds it at a time and tells the network at once (gratuitous ARP); if it stops, the next server takes the address within seconds, carries on its calls and answers its phones at the same address, without them registering again. It needs Linux and the NET_ADMIN and NET_RAW permissions (in a container: \`--cap-add NET_ADMIN --cap-add NET_RAW\`), and the servers on one network. Restart each server after setting it.

**Provider lines.** In each trunk, **Shared between servers** says how:
- **Each server uses it**: every server registers and places its own calls. For providers that allow several registrations on one account, or none.
- **One server at a time**: one server registers and carries the line; the others show it as **Standby, held by** that server and send their calls out through it. If it stops, the next server registers within seconds. For providers that allow one registration per account.

**When a server stops**
- **Calls carry on.** Each server keeps a note of its calls in the database; when one stops, a running server picks up each call within seconds, moves the sound to itself and tells both ends (a re-INVITE). There is a gap of a few seconds in the sound. A planned stop (an upgrade, a restart) hands calls over at once, with no gap.
- Its phones carry on through their backup server, the DNS name or the shared address.
- Health and **Servers** show it as stopped; when it comes back it takes nothing back from the server that took over.

**Between servers.** The servers reach each other at their SIP port over TCP, or TLS when they have a certificate (each accepts only the certificate the other published, and the sound between them is then encrypted with SRTP), and send sound to each other's media ports (UDP, the range in Settings). Calls between servers are signed with a secret they share in the database, and only accepted from a running member at its own address. A firewall between them must let those ports through.

**Try it first: the lab.** BitVoice comes with a lab that runs two servers, their database and test phones on one computer with Docker, each on an address of its own: \`deploy/lab/run.sh up\` builds and starts it and sets the shared address; \`run.sh call\` calls between phones on different servers; \`run.sh failover\` kills the server carrying a call through the shared address and checks the call carries on (the sound comes back in about five seconds); \`run.sh impaired\` calls from a phone on a bad link (delay, jitter and 5% loss) and checks the call quality BitVoice reports; \`run.sh down\` removes it. The servers' pages are at http://127.0.0.1:18501 and :18502 (user admin).`,
  },
  {
    id: 'database', title: 'Where the data is kept (SQLite or PostgreSQL)', route: '/settings', keys: 'database sqlite postgresql postgres backup data storage failover several servers reporting power bi grafana move',
    body: `BitVoice keeps its settings and their saved versions, users, contacts, call records, the audit log, texts, notes and transcripts in a database. Recordings and voicemail stay as sound files in the data folder.

**This server's own file** (SQLite, the default): a single file, \`bitvoice.db\` in the data folder, with nothing to install. Back up the data folder and you have everything.

**PostgreSQL**: for several BitVoice servers sharing one set of data, or for reports in tools such as Power BI, Grafana or Metabase. In Settings, **Database**, **Move the data into PostgreSQL**: make an empty database and a user who owns it, enter its address (postgres://user:password@host:5432/database), press **Test**, then **Copy the data and restart on PostgreSQL**. Everything is copied in one go (all of it or none of it) and BitVoice restarts on it; the old file stays where it was. Moving back into the server's own file works the same way. Moves are refused while calls are in progress.

Servers sharing a PostgreSQL database see each other's settings changes within five seconds. Each still answers its own phones and calls; for a second server to take over a first one's phones, the phones need both addresses (or one address that moves).

The database can also be named when BitVoice starts, with \`-database postgres://…\` or the BITVOICE_DATABASE environment variable; it is then changed there, not in Settings.

**Older installations**: on the first start of this version, BitVoice copies its old JSON files into the database and moves them into \`pre-database\` in the data folder.`,
  },
  {
    id: 'teams', title: 'Microsoft Teams (Direct Routing)', route: '/trunks', keys: 'teams microsoft direct routing sbc session border controller office 365 pstn gateway powershell New-CsOnlinePSTNGateway certificate',
    body: `BitVoice can be the session border controller (SBC) for a Microsoft Teams tenant, so Teams users call BitVoice extensions and your phone lines, and BitVoice rings Teams users.

**What you need**: a public name for this server in a domain your Microsoft 365 tenant has verified (sbc.example.com), port 5061 open both ways to Microsoft's networks 52.112.0.0/14 and 52.120.0.0/14, and a certificate for that name from a public authority. Teams users need a Phone System licence.

**Steps**
1. Settings, **Certificate**: install the certificate and its private key (PEM). It is used at once, for this interface and for SIP over TLS, and BitVoice presents it to Microsoft.
2. Trunks, **Add**, start from **Microsoft Teams**: enter the server's public name. The drawer says whether the certificate covers it.
3. Copy the **PowerShell** the drawer shows and run it as a Teams administrator: it adds the SBC (New-CsOnlinePSTNGateway), a voice route and policy, and a dial plan rule so short extension numbers reach BitVoice as dialled; then give each Teams user the policy and their number.
4. To ring Teams users from BitVoice, add an outbound route for their numbers through the Teams trunk.

BitVoice sends Microsoft OPTIONS every minute with the SBC name in Contact, as Microsoft requires, and tries sip, sip2 and sip3.pstnhub.microsoft.com in turn. Media is always encrypted (SRTP). Health warns when the certificate does not cover the name, is self-signed or ends soon, and when Microsoft stops answering.

This follows Microsoft's published SBC requirements and is tested against a simulated Microsoft proxy; BitVoice is not on Microsoft's list of certified SBCs.`,
  },
  {
    id: 'reputation', title: 'Caller ID reputation and branded calls', route: '/reputation', keys: 'reputation spam likely scam likely labelled caller id branded calling rich call data rcd cnam logo call reason 607 608 answer rate',
    body: `Phone companies run spam filters that can label a business number "Spam likely", or block it, and then hardly anyone answers. **Caller ID reputation** watches each number you call out from, from your own calls, and says in plain words how it is doing:
- **Answered**: the share of calls answered, and what it was the period before. A fall by half, or under 20%, is the usual sign of a new label.
- **Hung up at once**: answered calls that ended within 10 seconds.
- **Refused as spam**: calls a carrier's filter refused (SIP 608) or that the person marked unwanted (607). One 608 means the number is labelled.
- Heavy use: more than about 150 calls a day from one number is what robocallers do, and filters notice.

Numbers with fewer than 20 calls are shown as too few to judge.

**Your outgoing numbers** gives each number a **name** (sent as the caller's name), a **reason** for calling and a **logo** (an https:// image). On a trunk with **Send branded caller ID** on, the logo and reason go out as rich call data (RFC 9796 Call-Info); carriers that support branded calling show them on the called phone. Most phones show the name from the caller-name (CNAM) database instead, which your provider sets: ask them. **Watch** puts a warning on Health when a watched number starts to look labelled.

The page also says what to do when a number is labelled.`,
  },
  {
    id: 'calendar', title: 'Calendars and do not disturb', route: '/extensions', keys: 'calendar outlook google meeting busy out of office time off holiday do not disturb dnd forward presence away',
    body: `An extension can follow its person's calendar: during meetings and time off, its calls go to voicemail, or are forwarded (to another extension, or a mobile), instead of ringing a phone nobody will answer. Ring groups ring the others meanwhile, and in queues the agent shows as **Away**.

In the extension, **Calendar and do not disturb**, paste the calendar's private address:
- **Outlook**: Settings, Calendar, Shared calendars, Publish a calendar; choose the calendar and "Can view all details", then copy the ICS link.
- **Google Calendar**: Settings, the calendar on the left, then **Secret address in iCal format**.

**Check** reads it at once and lists the next busy times. BitVoice reads each calendar every five minutes. Time off (all-day events, and Outlook's "out of office") always counts; meetings count when **Meetings count as busy** is on; events marked free never count, nor do cancelled ones. Recurring meetings, with skipped and moved occurrences, are understood. If a calendar cannot be read, Health says so.

**Do not disturb now** stops the extension ringing until switched off, whatever the calendar says. The extensions list shows who is away and until when.

The private address works like a password: anyone with it can read that calendar, so it is kept with the other secrets.`,
  },
  {
    id: 'insight', title: 'Call insight for supervisors', route: '/quality', keys: 'insight coaching sentiment mood talk time talk ratio interruptions supervisor quality monitoring contact centre',
    body: `For contact centres: evidence about how calls went, for a supervisor to listen to, never an automatic score.

In a call's **Transcript**, **How it went** shows, measured from the recording's sound alone: how much each side talked, each side's longest stretch, how often each started talking over the other, how long both spoke at once, and the quiet (a long silence is often a hold or dead air).

With **Note how each transcribed call went** on (Settings, AI), the AI also reads the transcript and says how the caller seemed at the start and the end, and lists up to six moments: **Went well**, **Look at** (talking over the caller, a long hold, an unclear answer), **Not answered** (a question the caller asked that got no answer) and **Promised** (something someone said they would do), each with the time and the words said, so you can go straight to it. An administrator can ask for it on any transcribed call with **Look at this call**.

On [Call quality](#/quality), **Conversations, by who took them** sums the transcribed calls of the period per person: their share of the talking, their longest stretch, how often they talked over the caller, quiet time, calls that ended with the caller upset, and the moments flagged. Use it to choose calls to listen to, not to rank people.`,
  },
  {
    id: 'calllinks', title: 'Call links and website calling', route: '/call-links', keys: 'call link website call button qr code click to call browser web call no app',
    body: `A call link is a web address that rings your business from any browser: no phone number, no app. Put it in an email signature, print its **QR code** on a flyer or a shop window, or paste the **website button** into your site.

On [Call links](#/call-links), add a link, choose where its calls go (an extension, a group, a queue, a menu, the AI receptionist, a schedule or voicemail), and save: the page shows its address and QR code. The caller sees the title and words you set, gives their name if you ask for it, allows the microphone and presses **Call now**; the keypad works for menus. Whoever answers sees **Web:** and the caller's name.

Switch **Open** off to close the line (the page says so). **Calls at once** limits how many people can call through one link (5 unless set). The address holds a long random part so it cannot be guessed; repeated wrong addresses are blocked like failed sign-ins. Callers' microphones need the page to be served over HTTPS, so give out the HTTPS address.`,
  },
  {
    id: 'timeline', title: 'Customer timeline', route: '/timeline', keys: 'timeline customer history notes everything one view contact calls texts voicemail',
    body: `**Timeline** shows everything with one phone number over the last year, newest first: calls (who took them, how long, and the summary when the call was transcribed, with the recording and transcript), voicemails with their summaries, texts both ways, callbacks asked for, and the team's **notes**. A number matches however it is written.

Open it from a caller card (**Timeline**), from Contacts, from an incoming call in Call history, from a text conversation, or type a number at the top. Administrators can add a note (it shows on the timeline for everyone) and remove one.`,
  },
  {
    id: 'texts', title: 'Business texting (SMS)', route: '/texts', keys: 'sms text message texting textmagic 10dlc stop opt out reply provider',
    body: `Send and receive text messages from your business numbers on **Texts**. Texts go through a texting provider. Built in: TextMagic, Twilio, SignalWire, Telnyx, Bandwidth, Vonage, Plivo, Flowroute and VoIP.ms, plus **Your own webhook** for any other service or a gateway of your own. Any number of provider accounts can be used at once: each number says which one carries it.

## Setting up
1. In the provider's portal, find the account's API details. The form asks for exactly what that provider calls them (Twilio: Account SID and auth token; Telnyx: an API key; Bandwidth: API user, password, account ID and messaging application ID; SignalWire: also your space address) and says where to find them.
2. On Texts, **Texting setup**, add the provider with them, and save. BitVoice shows an address for received texts: set it in the provider as where received messages are sent (its callback, webhook or message URL). For VoIP.ms, add \`?from={FROM}&to={TO}&message={MESSAGE}&id={ID}\` to the end. Delivery receipts sent to the same address are ignored. BitVoice must be reachable from the internet there.
3. Add each number you text from (with its country code), the provider that carries it, and the extensions to tell by email when a text comes in.

## US 10DLC
US carriers block texts from ordinary business numbers that are not registered. Register your brand and then a campaign with your texting provider; the checklist on Texts tracks where they stand, and warns when your sample message lacks the opt-out words carriers expect.

## Conversations
Each conversation shows who it is (from Contacts), unread texts, and the thread. Enter sends; the counter shows how many texts a long message takes. When someone replies **STOP**, nothing more can be sent to them from that number until they reply **START**. Received texts also appear as a notice in BitVoice.`,
  },
  {
    id: 'briefing', title: 'Transfer briefings', route: '/settings', keys: 'warm transfer briefing summary whisper handover hand over caller repeat themselves ai',
    body: `So a caller never has to repeat themselves: with **Brief whoever a call is transferred to** on (Settings, AI), when a call is transferred, BitVoice writes a two-sentence summary of the call so far (who is calling, what they want, anything already promised) for the person it goes to. It appears on their browser phone with the ringing call, on [Live calls](#/calls), and as a text on their desk phone's screen (for phones that show SIP messages).

It works the same when the AI receptionist hands a caller to a person: the briefing comes from what the caller told the assistant.

It needs the AI service and a speech-to-text model (Settings, AI), and the call's audio so far (up to five minutes) is sent to that service to be written out. A summary takes a few seconds, so it may appear just after the phone starts ringing.`,
  },
  {
    id: 'spam', title: 'Spam protection and STIR/SHAKEN', route: '/settings', keys: 'spam robocall block number blocklist stir shaken attestation verstat spoof spoofed caller id withheld anonymous label',
    body: `In the US and Canada, the provider a call starts with signs its caller ID (**STIR/SHAKEN**): **A** when it knows the caller may use the number, **B** when it knows the caller but not the number, **C** when it only passed the call on. Your provider either tells BitVoice its verdict (verstat) or passes the signed Identity header on, which BitVoice then checks itself: the signature, that it is under a minute old, and that it names the numbers of this call. The verdict shows as **Verified**, **Partly verified**, **Unverified** or **Not verified** on Live calls and in Call history.

In Settings, **Spam protection**, choose what happens when a caller ID fails verification (ring, ring labelled, or refuse), when it is not verified at all, and when the number is withheld. A labelled call rings with **Spam?** (or your own label) before the caller's name. **Blocked numbers** are refused; an entry ending in * blocks a range (+1900* is every US premium-rate number). **Always allowed** numbers are never refused or labelled. In Call history, the block button beside a caller adds them to the list; refused calls are listed as **Blocked**.

Within an hour of an emergency call from any extension, no call is refused (blocked ones ring labelled instead), in case it is the emergency service calling back.

To check certificates against the STI authorities, paste their certificates under **STI certificate authorities**; without them, BitVoice checks that the call was signed with the certificate it names but not who issued that certificate.`,
  },
  {
    id: 'consent', title: 'Recording consent', route: '/settings', keys: 'recording consent notice announce beep two party all party law gdpr california this call is recorded',
    body: `Some places let a call be recorded when one person on it knows; others need everyone to be told, or to agree. In Settings, **Call recording**, choose how people on a recorded call are told:
- **No notice**.
- **Announce**: as the call is answered, both sides hear the notice (record it in the same place, for example "This call is recorded for training and quality"), and recording starts only after it has played. Until one is recorded, three beeps are played, and Health says so.
- **Beep**: a short tone to both sides every 15 seconds while the call is recorded.

**Rules** pick by the outside number (the caller on calls in, the number dialled on calls out), longest match first: for example numbers starting 1415 (California) **Announce**, 49 (Germany) **Never record**, everything else as above. A rule's **No notice** records without one; **Never record** leaves those calls out of recording altogether. Calls between extensions follow the main setting.

Recording can also be paused during a call with the pause code (card payments). None of this is legal advice: check the laws where you and your callers are.`,
  },
  {
    id: 'hotdesk', title: 'Hot desking', route: '/phones', keys: 'hot desk hot desking shared desk phone sign in sign out hoteling hotelling flexible',
    body: `A hot desk is a shared phone that becomes whoever signs in at it. Make one by switching on **Hot desk** in the extension the phone is set up as (the desk's own extension, such as 900 Desk 3).

At the phone, dial **\*33**, wait for the tone, enter your extension and **#**, wait for the tone, then your voicemail PIN and **#**. A rising tone means you are signed in; a falling tone means it was refused (five wrong tries lock the desk for ten minutes). From then on, calls to your extension ring this desk as well as your other phones, and calls you make from it show your number and name and count as yours on Costs. Signing in at another desk moves you there. **\*34** signs you out.

Emergency calls from a hot desk always carry the desk's own site, floor and room, not your usual ones.

On [Phones](#/phones), **Hot desks** shows who is signed in where; administrators can sign someone in (without their PIN) or out. The codes, and a time each day when everyone is signed out, are in Settings.`,
  },
  {
    id: 'contacts', title: 'Contacts on every phone', route: '/contacts', keys: 'contacts directory phonebook address book csv import export yealink polycom grandstream snom caller name',
    body: `**Contacts** is the company directory: the suppliers, clients and other offices people call. Every phone lists it beside the extensions, and when one of these numbers rings in, phones and the caller card show the contact's name (when the provider sent none). A number matches however it is written: +1 212 555 0100 and 2125550100 are the same line.

## Adding contacts
**Add contact** for one at a time, with as many numbers (work, mobile, home, other) as they have. **Import** takes a spreadsheet saved as CSV whose first row names the columns: name, company, work (or phone), mobile, home, other, email, notes. BitVoice checks the whole file first and says which lines are wrong; a contact with the same name as one there is updated rather than added twice, and **Replace every contact** starts the list afresh. **Export** downloads the list as CSV.

## On desk phones
Phones set up by [Phones](#/phones) (provisioning) get the directory with no more setup:
- **Yealink**: a remote phonebook named Company, refreshed every hour.
- **Grandstream**: the phonebook download, every hour.
- **Polycom**: the directory file, read when the phone starts.
- **Snom**: the list is written into the phone's settings, so it updates when the phone next checks them.

## In the browser phone
**Contacts** under the keypad finds a name or number and puts it ready to call.`,
  },
  {
    id: 'costs', title: 'Costs and budgets', route: '/costs', keys: 'cost costs spend money budget rates prices toll fraud billing department alarm',
    body: `BitVoice prices each call from what your provider charges, and shows the month's spend by department, person and trunk, with the most expensive calls.

## Prices
On [Trunks](#/trunks), open a trunk and its **Call prices**. Each price covers numbers that start with some digits, as the number is sent to the provider (digits only, so 44 for the UK, 1900 for US premium rate); the longest match wins, and a price with no digits covers every other number. Give the price per minute, any charge per answered call, the billing step (60 seconds for whole minutes, 6 or 1 for finer) and any minimum. **Calls in, per minute** is for toll-free numbers you pay to receive. **What would it cost** checks a number before anyone dials it. The currency is set on Costs.

A call's cost is fixed when it ends, with the prices of the time; changing a price does not change past calls. Costs says how many answered calls out had no price.

## Departments and budgets
Give extensions a **Department** (in the extension). On Costs, add a budget for every call or for one department: an alert appears in BitVoice, and is emailed to the budget's address and the alert address, at 80% and again at 100% of the month.

## The alarm
**Calls out cost more than this in an hour** raises an alarm when outgoing calls pass that amount within any hour. A sudden run of expensive calls is the usual sign of toll fraud (a stolen phone password): check Call history, then disable the extension or the trunk.`,
  },
  {
    id: 'quality', title: 'Call quality', route: '/quality', keys: 'mos jitter packet loss choppy robotic sound quality diagnosis site provider network',
    body: `How calls sounded over the last day, week, month or 90 days, from the sound BitVoice itself received on each side of each call. Phones are grouped by their [site](#/sites) (or "no site"), and calls from outside by the provider's line they came in on.

Each place shows its calls, its average score (MOS, out of 5: above 4 is good, under 3.6 sounds poor), the share of sound packets that never arrived, and jitter (uneven delay). Below that, BitVoice says what is wrong in plain words, for example "Packet loss on Head office's network: 3.2% of the sound never arrived, mostly between 14:00 and 16:00", with what to check. The bar chart shows the sound lost by hour of the day.

If every place loses sound at once, the cause is what they share: the internet link or the provider. Places with fewer than three calls are not judged. Per-call quality is also in [Call history](#/history) (the Quality column) and, while calls are live, on [Live calls](#/calls).`,
  },
  {
    id: 'callbacks', title: 'Callbacks', route: '/callbacks', keys: 'call back ring back request receptionist',
    body: `People who asked to be rung back. An AI receptionist books one when a caller asks: it collects their name, the number to call (offering the one they are calling from), a good time and what it is about, reads them back, and books it only when the caller agrees. It never promises an exact time. Administrators can also add callbacks by hand.

A new callback shows an alert on every open screen, and is emailed to the owner of the receptionist's **Take messages for** mailbox when that extension has an email address and email is set up in Settings.

To ring back, choose your extension and press **Ring back**: your phone rings first, and when you pick up BitVoice calls the number for you, with your normal routes and caller ID. Then add a note if you like and press **Done** (or **Cancel it**). Finished callbacks are kept below the open ones and can be opened again.`,
  },
  {
    id: 'holds', title: 'Legal holds', route: '/holds', keys: 'retention litigation ediscovery keep preserve',
    body: `A legal hold keeps call records, recordings and voicemail for the numbers and dates it names, or everything, and stops anyone deleting them, until it is released. Held calls show a lock in Call history. Releasing a hold makes what it kept subject to the retention settings again; released holds stay listed.`,
  },
  {
    id: 'conferences', title: 'Conference rooms', route: '/conferences', keys: 'conference meeting bridge confbridge meetme dial-in pin leader moderator mute lock room record',
    body: `A conference room is a number people dial to all talk together. Staff dial it from their desks; outside callers reach it through an incoming route or a menu option that points at the room.

## Setting up a room
1. Open [Conference rooms](#/conferences) and press **Add conference room**.
2. Give it a **number** (any free number, 500 by default) and a **name**.
3. Optionally set a **PIN**. Callers hear a tone, enter the PIN and press #. Three wrong PINs and the call ends.
4. Optionally set a **leader PIN**, different from the PIN. Whoever enters it joins as a leader.

## Choices
- **Wait for the leader**: everyone else hears the hold tone until a leader joins. When the last leader leaves, the room closes and everyone is hung up. This needs a leader PIN.
- **Members join muted**: for briefings and lectures. Leaders talk; members listen. A member presses 1 to ask to speak, and a leader unmutes them from this page.
- **Record the room**: the whole room is kept as one recording in [Call history](#/history), unless recording is off for the whole system.
- **Tones when people join and leave**: on by default.
- **Most people at once**: 50 by default, up to 200.

## Keys during a call
- \`1\` mutes and unmutes yourself (in a room where members are muted, it asks to speak).
- \`2\`, for a leader, locks the room so nobody new can join, and unlocks it.

## While a room is in use
The page shows each room in use: who is in, who is talking (the green light), who is muted, who has asked to speak, and who is still waiting for the leader. An administrator can mute or unmute anyone, remove someone, and lock or unlock the room.`,
  },
  {
    id: 'codes', title: 'Feature codes, forwarding and speed dials', route: '/codes', keys: 'feature codes star codes forward call forwarding do not disturb dnd follow me find me pickup intercom speed dial',
    body: `Feature codes are what people dial on their phones to change their own settings and reach features. They work on every phone, desk phone or softphone, with nothing set up on the phone itself. A rising tone means done; a falling tone means refused.

## Forwarding
- \`*72\` forwards every call. Dial it, hear a tone, then dial the number and #. Or dial the number straight after the code: \`*722125550199\`.
- \`*90\` forwards calls that come while you are on a call. \`*52\` forwards calls you do not answer within your ring time.
- \`*73\`, \`*91\` and \`*53\` cancel them.
Forwarding to an outside number needs the extension to be allowed outside calls. Forwarding is live state, not configuration: setting it makes no revision.

## Do not disturb
\`*78\` on, \`*79\` off, \`*76\` switches. With do not disturb on, calls go to voicemail, or to the extension's no-answer destination. Emergency call-backs still ring.

## Follow-me
\`*21\` switches follow-me on and off, for extensions that have follow-me numbers set (on the extension, or in the staff portal).

## Answering someone else's call
- \`*8\` answers the oldest call ringing anyone in your **pickup group** (set on each extension).
- \`**\` and an extension answers a call ringing that extension: \`**101\`.

## Intercom
\`*80\` and an extension calls it with auto-answer: its phone answers on the speaker, and both sides talk. An extension can refuse intercom; then the call rings as usual.

## Speed dials
\`*0\` and a code dials a saved number: \`*05\`. The company's speed dials are on this page; each person can have their own on their extension or in the staff portal, and theirs win.

## Other numbers people dial
- \`*43\` the **echo test**: say something and hear it back, to check sound both ways.
- \`*97\` **voicemail**: hear your messages. From another phone, enter the mailbox and its PIN.
- \`70\` **park**: transfer a call to 70 and it waits in the first free slot (71 to 79), shown on the Dashboard; dial the slot from any phone to pick it up. Unclaimed, it rings back whoever parked it.
- \`*45\` and \`*46\`: queue agents log in or out, and pause or resume.
- \`*33\` and \`*34\`: sign in and out at a hot desk.
- During a call, \`*7\` **pauses the recording** and \`*8\` resumes it (card payments).
- During a call with a door station, the door's code (\`*1\` by default) **opens the door**.
- The emergency numbers (911, 933 in the US) always work, from every extension.

## Changing the codes
Every code can be changed here, or switched off with the word \`off\`. Two features cannot share a code. The echo, voicemail and park numbers are in [Settings](#/settings).`,
  },
  {
    id: 'tenants', title: 'Hosting other organisations', route: '/tenants', keys: 'tenants organisations organizations multi-tenant hosted hosting reseller provider domain',
    body: `One BitVoice can host several organisations. Each one is a phone system of its own: its own extensions, numbers, trunks, routes, administrators, voicemail, recordings and call history. No organisation can see another's. They share only this server's SIP port and media ports.

## Adding an organisation
1. Open [Organisations](#/tenants) and press **Add organisation**.
2. Give its **name** and a **short name** (lowercase letters, digits and dashes). The short name is part of its web address, \`/t/acme/\`, and of its phones' sign-ins, \`acme.100\`. It cannot be changed later.
3. Optionally give its **own domain**, such as \`pbx.acme.example\`, pointed at this server. Phones register to that domain, and its administrators open it in a browser.
4. Set its **first administrator**: a username and a password of at least 12 characters.
5. Optionally set **limits**.

## How calls find the right organisation
- A phone registering to the organisation's domain belongs to it.
- Without a domain, a phone signs in with the short name in front of the extension: user \`acme.100\`.
- A call from a provider goes to the organisation whose trunk it comes from and whose incoming route has the number dialled, so several organisations can share one provider.

## Limits
Extensions, calls at once, storage for recordings and voicemail, and AI requests a day. 0 is no limit. Emergency calls are never refused: an organisation at its call limit, or suspended, can still call 911 and be called back.

## Opening an organisation
**Open** signs you in to its web interface as the provider, without its own account. Its administrators sign in at its own address with their own accounts. They never see this server's own screens: updates, the database, the other servers, the certificate, the SIP trace or this list.

## Suspending and removing
**Suspended** stops sign-ins and every call except emergency calls; nothing is deleted. **Remove** stops the organisation and moves its folder aside (\`tenants/.removed-<name>-<time>\`), so a mistake can be undone by hand.`,
  },
  {
    id: 'paging', title: 'Paging and overhead speakers', route: '/paging', keys: 'intercom announce overhead multicast speaker',
    body: `Dial a paging number to speak through every phone in the group at once: phones answer on their speakers by themselves (some need auto-answer allowed in their settings).

**Multicast** also sends the page to a multicast address and port, as G.711 µ-law, which overhead speakers and many desk phones can listen to without a call each. A group may have only a multicast address.`,
  },
  {
    id: 'emergency', title: 'Emergency calls', keys: '911 933 112 999 karis law ray baums location notify',
    body: `Emergency numbers (Settings, set by country) always go out, from every extension, even ones that cannot call outside numbers. Add your buildings under [Sites](#/sites) with their street address, and give every extension its site, floor and room, so responders know where to go. With **Send the caller's address** on (Settings), emergency calls carry it to the provider as PIDF-LO. A phone registering from outside its site's networks is a remote worker: their phone page asks where they are, and that address is used instead (alerts say when it was not confirmed). **Notify** lists extensions that are told when anyone dials an emergency number (Kari's Law in the US); every open BitVoice screen shows an alert too, and the addresses under **Also email** get an email. Dial \`933\` in the US to test the address your provider has.

## Emergency mode
A different thing: one switch that sends every incoming call to one place (an evacuation, a closure, an incident). Set where calls go, an optional code such as \`*55\` and the extensions allowed to dial it in [Settings](#/settings); switch it on the [Dashboard](#/), or by dialling the code (rising tones for on, a low tone for off). A red bar shows on every screen while it is on.`,
  },
  {
    id: 'security', title: 'Security', route: '/security', keys: 'ban fraud audit password two-step totp tls',
    body: `- Addresses that keep failing to sign in are blocked for a while (Settings, Protection). Blocked addresses are listed here and can be unblocked.
- The **audit log** records every administrative action and who did it.
- Turn on **two-step sign-in** for your account (Administrators): a code from an authenticator app.
- Premium-rate numbers are refused unless allowed.`,
  },
  {
    id: 'sso', title: 'Single sign-on', keys: 'oidc entra azure google okta keycloak login provider groups',
    body: `In [Settings](#/settings), **Single sign-on** lets people sign in with their work account through any OpenID Connect provider.

1. Register an application with the provider, with the **Redirect address** shown in Settings.
2. Copy its issuer address, client ID and client secret into Settings (the provider buttons fill in the issuer).
3. Name the provider **groups** whose members are administrators and viewers.

The account here is made at first sign-in, and the provider's groups set the role at every sign-in. With **Also allow passwords** off, passwords work only on this computer, so a provider outage never locks you out.`,
  },
  {
    id: 'compat', title: 'Asterisk compatibility (AMI and ARI)', keys: 'ami ari asterisk manager rest crm wallboard click to dial originate',
    body: `Tools written for Asterisk can control BitVoice (Settings, **Asterisk compatibility**). They see calls as channels named like \`PJSIP/100-...\`.

## AMI
The manager interface on TCP port 5038 (this computer only by default). Supported: Login (plain or MD5 challenge), Ping, Events, CoreShowChannels, Status, PJSIPShowEndpoints, ExtensionState, QueueStatus, Originate, Redirect, Hangup, Logoff, and the Newchannel, Newstate and Hangup events.

## ARI
At \`/ari/\` with basic authentication or \`?api_key=user:secret\`: \`GET /ari/channels\`, \`POST /ari/channels?endpoint=PJSIP/100&extension=5551234\` (ring 100, then call the number), \`DELETE /ari/channels/{id}\`, \`POST /ari/channels/{id}/redirect?endpoint=PJSIP/102\`, \`/ari/endpoints\`, \`/ari/bridges\`, \`/ari/asterisk/info\`, and the \`/ari/events\` WebSocket. Stasis media applications are not offered: use Scripts.

Logins are **read only** unless marked **Can change calls**.`,
  },
  {
    id: 'codecs', title: 'Codecs and sound', keys: 'g722 hd voice g711 pcmu pcma opus g729 codec noise clean',
    body: `Settings lists the codecs phones may use, best first. **G.722** is HD voice; **G.711** (µ-law in North America and Japan, A-law elsewhere) is standard quality. **Opus** is HD voice for apps and browsers and copes well with lost packets; **G.729** uses a fifth of G.711's bandwidth, for thin links and carriers that ask for it. BitVoice converts between any two of these when two parties differ, so everyone can talk to everyone; when both use the same codec the audio passes untouched. Converting costs processor time: per call, about 0.3 ms of one core every 20 ms for Opus and 0.12 ms for G.729 (some 60 and 170 converted calls per core), against almost nothing for G.711 and G.722. G.729 is sent without Annex B (no silence frames).

**Sound clean-up** turns down steady background noise and evens out levels, for voicemail and, if you like, during calls.`,
  },
  {
    id: 'automation', title: 'Configuration as code', keys: 'api token terraform opentofu openapi git gitops automation script ci',
    body: `Everything the screens do goes through BitVoice's REST API, described for tools at [/api/openapi.json](/api/openapi.json).

- **API tokens** ([Administrators](#/users)): make one per program, read only or able to change everything, and send it as \`Authorization: Bearer bv_...\`. It is shown once; revoke it when no longer needed.
- **Terraform or OpenTofu**: the repository's \`deploy/terraform\` shows extensions, ring groups and routes managed with the generic REST provider.
- **Git** ([Settings](#/settings), Configuration from Git): BitVoice reads the configuration from a file in a repository every few minutes and shows what differs, or makes the changes. Export the starting file with **Export for Git**, which leaves out passwords; BitVoice keeps its own.`,
  },
  {
    id: 'updates', title: 'Updates', route: '/updates', keys: 'upgrade version install rollback release signed',
    body: `Upload the new BitVoice program (and its \`.sig\` file) and press **Install**. BitVoice restarts on the new version; by default it waits until no calls are in progress. The version you left is kept: **Go back** returns to it in one click. A new version that fails to start twice is replaced by the previous one by itself. Release builds install only versions signed by BioDream.`,
  },
  {
    id: 'loadtest', title: 'Call generator', route: '/loadtest', keys: 'load test stress regression capacity',
    body: `The call generator places real calls with real audio into BitVoice from this computer, to see how many calls it carries and how well, or to check after a change that calls still go where they should. It only calls numbers inside BitVoice (never trunks or emergency numbers). **Answer as** lets it answer calls to an extension itself. Results show answers, failures by reason, answer times, audio lost and an estimated audio score (MOS).`,
  },
  {
    id: 'changes', title: 'Changes, backups and import', route: '/changes', keys: 'revision undo restore export import backup freepbx asterisk',
    body: `Every saved change is a revision. **What changed** shows the difference; **Restore** shows what restoring would change and asks first. **Export** downloads the whole configuration; **Import** previews a file's changes before applying them. [Import](#/import) also reads FreePBX and Asterisk configurations. [Archive](#/archive) copies backups, recordings and voicemail to Amazon S3 or Azure.`,
  },
  {
    id: 'ai', title: 'AI receptionist, voicemail triage and asking', route: '/receptionists', keys: 'ai assistant receptionist triage urgent ask plain words model speech',
    body: `AI features use the AI service set in [Settings](#/settings): an OpenAI-compatible service (OpenRouter by default) or one you run yourself. What they need is sent there, so choose a service you trust with calls; for a 911 centre, its own government cloud counts as local.

## AI receptionist
Choose a receptionist as where calls go: a route, a menu option or an extension's **If nobody answers**. It says it is automated, answers only from **Approved information**, takes a message, books a callback (see [Callbacks](#/callbacks)), or transfers to **A person is**. Pressing **0** or asking for a person always reaches someone, and so does any problem with the AI service or the speech model: callers are never stuck with it.

## Models
**Model** does the text work: summaries, the receptionist's replies and asking in plain words. Turning speech into text needs a model that takes audio; set it as **Speech-to-text model** (for example google/gemini-2.5-flash on OpenRouter) to use a text-only model for the rest, such as meta-llama/llama-3.3-70b-instruct.

## Voicemail triage
With **Transcribe and summarise voicemail** on, each message is also marked urgent, normal or low priority, with its kind (sales, support, billing...). Urgent messages raise an alert on every administrator's screen.

## Ask in plain words
On [Changes](#/changes), describe a change ("send sales calls to voicemail after 5 pm"). The AI proposes it, BitVoice checks it like any other change and shows exactly what would change. Nothing changes until you press **Apply**, and it becomes a revision you can restore. Passwords are never sent to the AI.`,
  },
  {
    id: 'flow', title: 'Call flow', route: '/flow', keys: 'diagram flow chart routing map edit drag where calls go simulate simulator try a call test path',
    body: `## Try a call
Enter who calls (an extension, or an outside number), what they dial and when, and press **Try it**. BitVoice follows the call exactly as it would for real, without ringing anything: the incoming route, schedules at that time, routing scripts (they run, in their sandbox), menus, groups and queues, and what happens if nobody answers. The path this call takes is marked; other ways on (a menu's keys, a schedule's other side) can be opened. Green means it works, amber means look at it (a phone not connected, a script falling back), red means the call fails there. Tick **Assume every phone is connected** to see the plan as designed rather than as it is right now; **Arrives on** picks the trunk for routes that only take one.

## The diagram
The diagram shows where calls from outside go, box by box: each incoming route, then the menus, groups, queues, schedules, scripts and people it leads to.

Click a box (or Tab to it and press Enter) to change where calls go from it: a route's destination, an extension's or group's **If nobody answers**, a schedule's open and closed destinations, a menu's keys (add or remove keys right there), a script's fallback or a receptionist's person. Saving checks the change like any other and makes a revision. **Open its full editor** goes to the item's own page. **Add incoming route** adds a route without leaving the diagram.`,
  },
  {
    id: 'sites', title: 'Sites and office 911', route: '/sites', keys: 'site building address 911 emergency location remote worker floor room network e911 karis ray baum',
    body: `A site is a building with its street address and the networks its phones use. Give every extension its site, floor and room (on the extension), and emergency calls carry that location to the 911 centre.

## Remote workers
A phone that registers from outside every site's networks is a remote worker. On its first emergency-capable registration from a new place, its user is asked to confirm their address on their phone page; emergency calls then carry that address instead.

## On-site alerts
With **Email when someone calls 911** set in Settings, the people named there get the caller's name, extension, site, floor and room as soon as a call to an emergency number starts, so someone can meet the responders at the door.`,
  },
  {
    id: 'calls', title: 'Live calls', route: '/calls', keys: 'live active current calls hang up transfer listen whisper barge supervise door pause recording',
    body: `Calls in progress, updated as they happen. Administrators can hang up a call, open a door station's door, pause and resume a call's recording for card numbers (the pause is marked in the recording), and supervise an agent: **Listen** (silent), **Whisper** (only the agent hears you) or **Barge** (everyone hears you).`,
  },
  {
    id: 'history', title: 'Call history', route: '/history', keys: 'cdr call records history log export csv recording transcript search said summary action items',
    body: `Every finished call: who called whom, when, for how long and how it ended. Sort the columns, play recordings (a pause for card numbers is marked), and download the list as CSV. How long records are kept is set in Settings, Call recording; a [legal hold](#/holds) keeps them longer.

## Transcripts and search
With AI on and **Transcribe recorded calls** switched on (Settings, AI), every recorded call is written out, with a short summary and its action items: what someone agreed or was asked to do. Each side of the call is written out separately from the recording, so the transcript shows who said what, and when. The transcript button next to a recording opens it; administrators can also transcribe a single call, or transcribe one again.

**Search what was said** finds calls by words from their transcripts, summaries and action items. Only recorded calls are transcribed. A transcript is deleted with its recording, and reading or searching transcripts is noted in the audit log, like playing a recording.`,
  },
  {
    id: 'health', title: 'Health', route: '/health', keys: 'health status problems warnings disk certificate trunk registration vault',
    body: `Whether the system is ready for calls: the phone service, phones that are not connected, trunks, whether emergency calls can get out, extensions without an emergency location, menus without a recorded prompt, the time zone, how long recordings are kept, and secrets a key vault cannot read. Each line says what is wrong and links to where to fix it.`,
  },
  {
    id: 'trace', title: 'SIP trace', route: '/trace', keys: 'sip trace debug messages ladder invite register packet capture',
    body: `The SIP messages BitVoice sends and receives, live, for finding out why a phone will not register or a call fails. Filter by number, address or Call-ID.`,
  },
  {
    id: 'archive', title: 'Archive', route: '/archive', keys: 'archive backup s3 azure blob offsite copy recordings',
    body: `Copies backups, recordings and voicemail to Amazon S3 or Azure Blob Storage on a schedule. Keys can be references to a key vault (see Settings, Key vaults).`,
  },
  {
    id: 'import', title: 'Import', route: '/import', keys: 'import migrate freepbx asterisk 3cx cisco cucm avaya ip office mitel move from replace hunt group hunt pilot line group ring group route pattern bat tar',
    body: `Brings extensions, ring groups, queues and trunks over from FreePBX, Asterisk or 3CX; and people, hunt and ring groups, SIP trunks, incoming numbers and dial patterns from Cisco, Avaya and Mitel. Upload the exported files, several at once (a .zip or .tar of them works too); BitVoice shows exactly what would change before anything is applied, and the import becomes a revision you can undo on [Changes](#/changes).

Where each export is:
- **FreePBX:** Admin → Bulk Handler → Export → Extensions (CSV).
- **Asterisk:** pjsip.conf or sip.conf.
- **3CX:** Users → Export (CSV).
- **Cisco Unified CM:** Bulk Administration → Phones → Export Phones, all details; or Users → Export Users. The phones' first line becomes the extension, with the line's alerting name or label as its name.
- **Avaya IP Office:** Manager → File → Import/Export → Export, Users.
- **Avaya Communication Manager:** \`list station\` saved as CSV; virtual stations and VDNs are left out.
- **Mitel MiVoice Business:** System Data Management → User and Device Configuration → Export.

**Groups and routes.** Upload them with the people file, so each group's members are found by number or by name:
- **Cisco Unified CM:** Bulk Administration → Import/Export → Export, with Line Group, Hunt List, Hunt Pilot, SIP Trunk and Route Pattern ticked; upload the .tar as it comes. Each hunt pilot becomes a ring group with the members of its hunt list's line groups, Top Down and Circular as one after another, Broadcast as everyone at once, the RNA reversion timeout as the ring time. Route patterns become outbound routes: \`9.[2-9]XXXXXX\` with PreDot is pattern \`9[2-9]XXXXXX\` dropping the 9; \`!\` becomes a final dot. Patterns using \`@\` (the national plan) are listed for writing by hand.
- **Avaya IP Office:** export Hunt Group as well as Users (both are CSV with no header row). Group mode is everyone at once; Hunt, Rotary and Longest Waiting ring one after another.
- **Avaya Communication Manager:** \`list hunt-group\` saved as CSV, with the members in a Members column or a separate members file.
- **Mitel MiVoice Business:** Ring Groups and Ring Group Assignment (Cascade is one after another, Ring All everyone at once), and Hunt Groups.

Trunks come in switched off and without passwords, as exports do not carry them. Routes go out on the imported trunk of the same name; a route whose trunk is not here is left out and said. Rotating and longest-idle ringing is closer to a queue than a ring group: the preview says so, so you can make it one.

These exports hold no SIP passwords, so each extension gets a new one; voicemail PINs, names and email addresses come over when the export has them. Cisco, Avaya H.323/DCP and Mitel phones run their maker's own software and cannot be set up by BitVoice: the import says how many were listed. Cisco phones can be re-flashed with Cisco's multiplatform (SIP) firmware; Avaya J100 and Mitel 6800/6900 phones can register to BitVoice by hand in SIP mode.`,
  },
  {
    id: 'video', title: 'Video calls', route: '/settings', keys: 'video calls camera h264 h.264 vp8 video phone face to face picture',
    body: `When both phones in a call send video (video desk phones, softphones, video doorbells), the picture passes between them through BitVoice. Nothing to set up: it is on unless **Settings → Video calls** is switched off.

- BitVoice relays the video as it is, on a port of its own for each phone, so the phones never learn each other's addresses. It never converts video: the two need a codec in common (nearly every video phone has H.264). When they number the same codec differently, BitVoice renumbers it on the way.
- The picture is encrypted (SRTP) to each phone that encrypts its sound.
- A phone's request for a fresh picture (after lost packets) reaches the other phone, so the picture recovers in a moment.
- A camera switched on during a call (a softphone's video button) works too: the other phone is asked first, and the picture starts when it says yes.
- A phone without video, or a call to an outside number, gets sound only. On hold or while parked the picture pauses, and comes back when the call does. After a transfer it carries on only when the new party sends video too.
- Video uses far more of the network than sound (about 0.5 to 2 Mbit/s each way per call): allow for it on slow links and in the media port range.`,
  },
  {
    id: 'psap', title: '911 calls (the 911 profile)', route: '/psap', keys: '911 psap ng911 i3 esinet location pidf-lo held rebid ali spill eido cad nena log answer time call-taker',
    body: `BitVoice answers 911 calls as a PSAP's call-handling system: calls from the emergency services network (the ESInet, NENA i3) arrive with the caller's location and the network's data, the call-taker sees them at once, and CAD is told when the call is answered.

**Setting up.**
1. On [Trunks](#/trunks), add the trunk from your ESInet provider (or the NG911 core's ESRP) and switch on **From the ESInet**.
2. In [Call routing](#/routes), send its calls to your call-takers: a queue, or a ring group.
3. On **911 calls**, enter your agency ID (a domain, like \`psap.county.example.gov\`) and what CAD takes: the address and port its ALI listener is on, and its EIDO address.

**When a 911 call rings**, it shows on **911 calls** with:
- **the address** the network sent (civic location), and **the position** with how sure it is (within so many metres), with a link to a map;
- **the provider**, the kind of service (wireless, VoIP, residence, business), the device and the subscriber, when the call brought them (Additional Data, RFC 7852);
- anything that went wrong reading it.

Location comes in the call (by value), or as a reference BitVoice fetches with HELD. A call with a reference has **Rebid location**: BitVoice asks again, for a caller who is moving or whose first fix was rough.

**When a call-taker answers**, CAD gets, each on its own so a CAD that is down never holds up the call:
- **the ALI spill**: the text block CAD systems read (callback number, class of service, address, position and uncertainty, provider), sent over TCP;
- **the EIDO**, the incident document of NENA-STA-024, by HTTPS POST (with a bearer token if set). **EIDO** on a call shows it.

**The i3 log** keeps the NENA i3 events of each call (call start, answer, location, EIDO sent), one file a day. **Answer times** shows how many calls were answered within your goal (NENA: 90% within 15 seconds, 95% within 20), the abandoned ones, and each call-taker's average.

What is not here yet: text to 911 (RTT and SMS), TTY, and transfer to another PSAP with the location carried along.
`,
  },
  {
    id: 'core', title: 'NG911 Core Services (ECRF, ESRP, LVF, BCF)', route: '/core', keys: 'ng911 core services ecrf lost esrp routing boundaries gis geojson psap overflow default policy rules bcf border control denial of service lvf address validation centerline address points',
    body: `For a state or regional 911 authority that runs its own NG911 core: BitVoice answers LoST as the ECRF, and routes each 911 call as the ESRP to the PSAP that serves the caller.

**Service areas.** Export your boundaries from GIS as GeoJSON polygons (PSAP boundaries for \`urn:service:sos\`; police, fire or EMS areas for \`urn:service:sos.police\` and the others) and upload them on **Core services**. A polygon's \`name\`, \`uri\` and \`number\` properties are read; give a default PSAP URI for those without one. **Edit** an area to add the cities or counties it serves, for callers whose location is a civic address only. **Try a place** shows which area answers a position or a city, as a LoST query would.

**The ECRF.** Switch on **Answer LoST queries** and list the addresses that may ask (your ESInet): other elements send findService to \`/lost\` (RFC 5222) and get the PSAP's URI. A specific service (sos.police) is answered from its own areas first, then from sos.

**The ESRP.** List the PSAPs: each one's URI (as the areas name it), the trunk that reaches it and its overflow PSAP, and choose the default PSAP. Then send the ESInet's incoming 911 calls to **ESRP** in [Call routing](#/routes). Each call goes to the PSAP for its location (from this server's areas, or an **outside ECRF** by LoST), with its location and the service it asked for carried on; when that PSAP does not take it, to its overflow; without a location, or from a place no area covers, to the default. Each decision is in the i3 log on [911 calls](#/psap).

**Policy rules.** Once the ECRF has picked a PSAP, the rules under **Policy rules** are checked in order and the first whose conditions all hold decides: send the call to another PSAP, or refuse it as busy. A rule can look at the service (fire calls only, say), the PSAP the ECRF chose, a schedule (while the night shift's centre is closed), calls with no location, calls the BCF marked suspicious, and the trunk the call came in on. Each decision is in the i3 log as a RouteLogEvent naming the rule.

**Border control (BCF).** With **Check calls from the ESInet** on, every call from an ESInet trunk is checked first: addresses under **Always refuse** are turned away, and so is an address that starts more calls a minute, or holds more calls at once, than you allow (a telephony denial-of-service attack sends floods of calls from a few sources). Refused calls get 503 with Retry-After. Turn on **let the call through marked suspicious** to send such calls on instead and let a policy rule decide (to a centre that screens them, or refused); a real caller in a flood is then not lost. **Border control: latest** lists what was refused or marked.

**Address validation (LVF).** Upload your 911 GIS address data as GeoJSON, in the NENA GIS data model (NENA-STA-006): road centerlines with \`FromAddr_L\`, \`ToAddr_L\`, \`Parity_L\`, \`IncMuni_L\` and the rest, and site/structure address points with \`Add_Number\`, \`St_Name\`, \`Inc_Muni\`. A LoST query with \`validateLocation="true"\` then gets the address's valid, invalid and unchecked parts in its answer (RFC 5222), so carriers and location servers can correct an address before a 911 call ever uses it. **Check an address** does the same here: an exact address point is the best match, then a street segment whose range on the right side of the road holds the number (odd and even sides respected); spellings like Street and St, North and N are treated alike.

Not here yet: the BCF's media checks (it limits and denies by address; it does not inspect SIP or media for attacks), similar-address suggestions for invalid ones, and spatial checks of a position against the address data.
`,
  },
  {
    id: 'radio', title: 'Radio dispatch console', route: '/radio', keys: 'radio console dispatch roip ptt push to talk channel talkgroup patch lmr gateway p25 issi cssi rfss imbe wacn unit encryption aes des key private call unit to unit alert tone warble emergency recording radio log cad',
    body: `Dispatchers talk on radio channels from the same headset as their phone calls. BitVoice reaches the radios through radio-over-IP (RoIP) gateways; the radio network itself stays with whoever runs it.

**Setting up.** Under **Console setup**, choose a console number (800, say) and add each channel: its name, the port on this server the gateway sends the channel's audio to, and the gateway's address and port for the audio going out. On the gateway: RTP, G.711 µ-law, to this server's address and that port; key the radio on voice (VOX). Each channel uses its port and the next one, outside the range phone calls use.

**Joining.** A dispatcher dials the console number from their desk phone, or presses **Join with the browser phone**. They then hear every channel's traffic mixed together; **Hearing / Muted** on a channel takes it out of their headset. The lamp on a channel lights while it is receiving.

**Talking.** Hold a channel's **Hold to talk** button (or focus it and hold the space bar): your voice goes to that channel only, and the channel shows **TX** with your extension. One dispatcher talks on a channel at a time; the button is greyed for the others while someone is talking. Nothing is sent to a channel while nobody is talking, so a voice-keyed radio is never held on the air.

**Patching.** Press **Patch** on one channel, then **Patch here** on another: the two hear each other (a fire channel and a mutual-aid channel, say) until you press **Unpatch**.

**P25 talkgroups.** A channel can also be a P25 talkgroup, joined directly over the ISSI (TIA-102.BACA, the interface between P25 subsystems; with **Send the CSSI console PTT word** on, BitVoice adds the console subsystem's word to each request). Choose **A P25 talkgroup (ISSI)**, give the talkgroup ID, the SIP address and ID of the RFSS that is home to the talkgroup, and a port for its voice. Under **This console on the P25 system**, enter the WACN and system ID, the RFSS ID your P25 administrator gave this console, the unit ID dispatchers transmit as, and the SIP port the ISSI uses here; the P25 system must know this server as an RFSS at that address. The tile shows **P25** while the group call is up (**P25 down** and the reason when it is not), and the unit ID of the radio talking, in red with EMERGENCY when it is an emergency call. **Hold to talk** asks the home RFSS for the channel (**Asking for the channel…**); your voice goes once it grants it, and if it refuses (someone with a higher priority has it) the tile says so. Voice is converted to and from IMBE, the P25 vocoder, in BitVoice.

**Encrypted talkgroups.** Under **Encryption keys**, add each traffic key your radio shop gives you: a name, the algorithm (AES-256, DES-OFB or triple DES), the key ID and the key itself in hex (TIA-102.AAAD). A key is never shown again once saved, not even to administrators, and leaving the field empty keeps it. The console hears an encrypted transmission when it has the key with that algorithm and key ID: the tile shows a lock and the algorithm next to the talking unit. Without the key the transmission is muted, never played as noise, and the tile says **no key: muted**. To transmit encrypted on a talkgroup, choose a key under **Transmit** for that channel; the tile then shows **TX AES-256** (or the algorithm you chose). A full configuration export (Settings, an administrator's backup) carries the keys, so keep those files as safe as the keys.

**Private calls.** With the console on a P25 system, **Call a radio** in your position's bar calls one radio by its unit ID (turn on **Emergency** to call it as an emergency). The call becomes a tile of its own, **Private call: unit 197**, with **Hold to talk** like any channel and **Hang up**. Radios can call the console too, on the unit ID dispatchers transmit as: the call appears as a tile marked **called in** (in red with **EMERGENCY** when the radio called as one), every dispatcher hears it, and it goes when either side hangs up. Under **Private calls** in the console's P25 settings, give the SIP address and ID of the RFSS that is home to the radios (empty uses the first talkgroup's home) and, if private calls should be encrypted, the key to send them with.

**Alert tones.** **Send an alert tone…** on a channel sends one of: **Alert 1**, a steady 1000 Hz tone for 3 seconds; **Alert 2**, a warble between 1000 and 1500 Hz; **Alert 3**, 1000 Hz pulses; or a short **Beep**. The tone holds the channel while it plays (the tile shows **tone** and your extension), every dispatcher monitoring the channel hears it go out, and on a P25 talkgroup it goes once the home RFSS grants the channel.

**Emergencies.** When a radio transmits as an emergency (its emergency button), its tile turns red with **EMERGENCY** and the unit ID, the server log notes it, and CAD is told at once (below).

**Radio log and recording.** Turn on **Record every transmission** under **Recording and CAD**: each transmission on each channel, heard or sent, is kept as a recording with a line in the **Radio log**: when, the channel, who (the radio's unit, or the dispatcher's extension), how long, and whether it was an emergency, an alert tone, encrypted or a private call. A heard transmission starts with the first speech and ends 1.5 seconds after the last word. Find a unit, dispatcher or channel with the search box, and press **Play** to listen. Recordings are kept as long as call recordings are (Settings, Recording).

**Radio data to CAD.** Give CAD's address under **Send radio events to CAD at** (and a shared secret if CAD wants one; it is sent as a bearer token and never shown again). BitVoice then POSTs each event as JSON, \`{"type": ..., "agency": ..., "data": {...}}\`: **radio.unit** when a radio starts talking on a channel (its unit ID, WACN, system, the channel, emergency, encryption), **radio.emergency** for an emergency (tried three times if CAD does not answer), and **radio.transmission** for each finished transmission as it appears in the radio log. CAD can also read the log itself with \`GET /api/radio/log\` (filters: \`channel\`, \`q\`, \`since\`, \`until\`). A CAD that is down never holds up the console. P25 radios' GPS positions are not part of this: they travel over P25 packet data (location services), not over the ISSI's voice.

Not here yet: P25 Phase 2 (TDMA) voice in its own half-rate vocoder. A Phase 2 system works with the console when its ISSI talkgroups carry full-rate (Phase 1) voice or its transcoder converts them, which is common: ask your P25 administrator. A system sending native half-rate voice over the ISSI does not work yet; the half-rate vocoder (AMBE+2) is patented until 2028 and needs a licence from its owner or a hardware vocoder chip.
`,
  },
  {
    id: 'fax', title: 'Fax: sending and receiving, fax machines and analogue phones', route: '/faxes', keys: 'fax t38 t.38 fax to email email to fax send fax outbox imap poppler libreoffice pdf fax number receive analogue analog ata gateway fxs fxo isdn pri bri patton audiocodes grandstream ht802 modem',
    body: `Fax machines, analogue phones, door phones and old lines connect through a SIP gateway: an ATA (Grandstream HT series, for one or two analogue phones or a fax), or a bigger gateway (Patton SmartNode, AudioCodes MediaPack and Mediant) for many analogue ports, an ISDN line (BRI/PRI) or old analogue lines (FXO). Each port registers to BitVoice as an extension, like a phone.

**Receiving faxes without a fax machine.** On **Faxes**, add a fax number (300, say), with the email addresses each fax goes to. Then in [Call routing](#/routes) send a phone number's calls to it. BitVoice answers as a fax machine, switches the call to T.38 and takes the fax; each fax becomes a PDF, kept on Faxes (**View**, **PDF**) and emailed. A fax that breaks off part way keeps the pages that arrived and says so. This needs a provider that supports T.38 (most do); a fax sent as sound cannot be received this way.

**Sending a fax.** On **Faxes**, **Send a fax**: choose the fax number it goes from, type the number to send to, write a note if you like (it becomes the first page) and add documents. Text files and pictures (PNG, JPEG, GIF) work everywhere. PDF needs poppler on the server (\`apt install poppler-utils\`, \`apk add poppler-utils\`, \`brew install poppler\`); Word, Excel, OpenDocument and RTF need LibreOffice as well. The page says which the server can do. Every page gets a header line, as fax machines print it: the time, the fax number's name, its identity and the page count. Pages are US letter in North America and A4 elsewhere, from the country in Settings.

BitVoice calls the number over your outbound routes, as an extension would, and sends at the fastest rate the line carries (14,400 bit/s, slower if the line is poor). Faxes from one fax number go one at a time. A busy number, or one that does not answer, is tried again five minutes later, three times in all. Sent faxes are listed with the received ones: **Sent**, **Waiting to send**, **Failed** (with **Try again**), or **Part sent** when the line broke after some pages.

**Email to fax.** Below the list, set up a mailbox for BitVoice to read (IMAP over TLS, port 993; for Google and Microsoft mailboxes use an app password), then **Test** and **Save**. Anyone allowed to send writes to that mailbox with the fax number in the subject (\`Fax 212 555 0199\`), or to a plus address (\`fax+2125550199@example.com\`) if the mail server takes them. Attachments become the pages and the email's own words the cover note (the signature and quoted mail are left out). It goes from the fax number that lists the sender: its **Email each fax to** addresses may always send, and **May also send faxes by email** adds others (whole addresses, or \`@example.com\` for a domain). The sender gets an email when the fax has gone through, or why it could not.

Email is easy to forge, so BitVoice faxes a message only when your mail server found it really came from the sender's domain (DKIM, SPF or DMARC, in the Authentication-Results line it adds). Other messages are dropped without a reply. Switch **Accept mail the sender's domain did not vouch for** on only for a mailbox no outside mail can reach.

**Fax machines on gateways.** BitVoice carries fax over T.38, the fax standard for the internet: when the gateway hears a fax tone it switches the call to T.38, BitVoice asks the other side (your provider, or another gateway) to switch too, and passes the fax between them. If the other side cannot do T.38, the call stays on G.711 and the fax goes as sound, which works on a good network. Live calls show **fax (T.38)** while a fax is going through.

For reliable faxes:
- Use T.38 on the gateway and choose a provider that supports it.
- Make the port's extension an **Analog adapter port** (Extensions, Kind): BitVoice then offers it only G.711, which faxes sent as sound need.
- On the gateway's fax port: Fax Mode T.38, only PCMU or PCMA, line echo canceller off, call waiting off, and a fixed, high jitter buffer.`,
  },
  {
    id: 'users', title: 'Administrators', route: '/users', keys: 'users administrators accounts viewer admin password two step sso',
    body: `People who sign in to this screen. Administrators change anything; viewers look without changing. Each person can turn on two-step sign-in with an authenticator app. With single sign-on (Settings) people sign in with the organisation's account, and an account is made for them the first time.`,
  },
  {
    id: 'settings', title: 'Settings', route: '/settings', keys: 'settings system codecs sso ai recording retention mobile push vault gitops sound',
    body: `Everything about the system as a whole: name, country and time zone, codecs, call recording and how long things are kept, sound clean-up, single sign-on, AMI and ARI for other programs, AI, the mobile apps' push service, key vaults, configuration from Git, and the emergency email. Passwords and keys are never shown again once saved; leave them empty to keep them.`,
  },
]

export const articleFor = (path: string) => manual.find((a) => a.route === path)
