// The built-in manual. Each article is short markdown (headings, paragraphs, lists, `code`, **bold**,
// [links](#/route)); `route` ties it to the screen whose help button opens it.

export interface Article { id: string; title: string; route?: string; keys: string; body: string }

/** The manual's parts, in reading order: Help groups the chapters by them, and the printed manual numbers
 * its chapters through them. Every article is in exactly one part. */
export const parts: { title: string; intro: string; ids: string[] }[] = [
  { title: 'Getting started', intro: 'From a new install to a working phone system: extensions, phones, a phone company, where calls go, emergency calling from your offices, and how to tell everything is ready.',
    ids: ['start', 'extensions', 'phones', 'trunks', 'routes', 'emergency', 'sites', 'health', 'settings'] },
  { title: 'Where calls go', intro: 'The building blocks callers move through: groups and queues, menus and schedules, scripts and the AI receptionist, voicemail, and the diagram that shows it all.',
    ids: ['flow', 'ringgroups', 'queues', 'sounds', 'menus', 'directory', 'dialin', 'schedules', 'scripts', 'ai', 'voicemail', 'callbacks', 'calllinks'] },
  { title: 'Everyday calling', intro: 'What people do on their phones every day, and what they set for themselves: forwarding, follow-me, pickup, conference rooms, paging, hot desks, faxes and texts.',
    ids: ['codes', 'portal', 'conferences', 'paging', 'hotdesk', 'hotel', 'contacts', 'calendar', 'mobile', 'video', 'fax', 'texts', 'briefing'] },
  { title: 'Customers and insight', intro: 'Knowing who called and how it went: the live calls and history, the customer timeline, CRMs, recordings and consent, quality, costs and caller ID reputation.',
    ids: ['calls', 'history', 'timeline', 'crm', 'insight', 'recording', 'consent', 'quality', 'costs', 'reputation', 'spam'] },
  { title: 'Running BitVoice', intro: 'Keeping it safe and running: administrators and sign-in, security, changes and updates, several servers, hosted organisations, codecs, Teams, importing, automation and the tools for finding faults.',
    ids: ['users', 'sso', 'security', 'changes', 'updates', 'servers', 'database', 'tenants', 'codecs', 'teams', 'import', 'automation', 'compat', 'archive', 'holds', 'metrics', 'loadtest', 'trace'] },
  { title: 'Public safety: the lab track', intro: 'The 911 answering-point profile, the radio dispatch console and NG911 Core Services. They are a lab and design-partner track: built and tested against recorded captures, sample data and stand-ins, but not certified for live 911 or radio dispatch and not yet tried with a live ESInet, P25 system or CAD. They are off the menu until switched on in Settings. Emergency calls from your own phones (Getting started) are not part of this track: they are part of every phone system.',
    ids: ['psap', 'radio', 'core'] },
]

export const manual: Article[] = [
  {
    id: 'start', title: 'Getting started', route: '/', keys: 'setup first steps overview begin dashboard home',
    body: `BitVoice is a complete phone system in one program: the exchange that phones sign in to, the menus and queues callers move through, voicemail, recording, conference rooms, faxes, and the connections to phone companies. Everything is set up in the browser, on the screens this manual describes. There is no configuration file to edit and nothing else to install.

::figure overview

## What you need
- **A computer to run it on**: a small office server, a virtual machine, a cloud instance, or for a first look your own laptop. Linux, macOS and Windows all work. A Raspberry Pi 4 runs a small office.
- **Phones**: desk phones from Yealink, Poly, Grandstream or Snom are set up automatically; any other SIP phone or softphone works with its own settings; and every browser is a phone too.
- **A trunk** (an account with a phone company) to call outside numbers and be called. You can start without one: extensions call each other and the echo test.

## The first start
Start BitVoice and open the address it prints (on a desktop it opens the browser itself). The web interface is **HTTPS only**, on this computer too. Until you install a certificate of your own (Settings, Certificate), BitVoice uses one it made itself, so the browser warns once: choose to continue to the site. The **setup wizard** asks four things: the organisation's name and country, the first administrator's password, the first extensions, and your phone company if you have one. The country sets the emergency numbers, the tones callers hear and the way phone numbers are written.

> Choose the country carefully. It decides which numbers are emergency numbers (911 and 933 in the US, 112 and 999 elsewhere), and emergency numbers always work from every extension.

## The first hour
::figure first-hour

1. **Extensions**: one per person or desk. Each has a number and a SIP password. [Open Extensions](#/extensions)
2. **Phones**: connect a desk phone, softphone or the browser phone to each extension. [Open Phones](#/phones)
3. **Trunk**: your phone company account, so you can call outside numbers and be called. [Open Trunks](#/trunks)
4. **Call routing**: where calls from outside go (a person, a ring group, a menu), and which trunk outside calls use. [Open Call routing](#/routes)
5. **Health** tells you what is still missing, emergency calling first. [Open Health](#/health)

Then make a test call: from any phone dial \`*43\`, the echo test. You hear yourself back, which proves sound works both ways.

## Where it runs
BitVoice runs on Linux, macOS and Windows.

| System | How to start it | Where its data is |
|---|---|---|
| Linux | \`./bitvoice\` (or the systemd service in the release) | \`~/.config/BitVoice\` |
| macOS | \`./bitvoice\` | \`~/Library/Application Support/BitVoice\` |
| Windows | double-click \`bitvoice.exe\` | \`%APPDATA%\\BitVoice\` |

On Windows, keep the window it opens while you use BitVoice (closing it stops BitVoice), and when Windows asks whether it may use the network, allow private networks so phones can connect. \`-data\` puts the data anywhere else; \`-https\` and \`-sip\` choose the addresses and ports.

## The ports it uses
| Port | What for |
|---|---|
| 5060 UDP and TCP | SIP: phones and trunks |
| 5061 TCP | SIP over TLS (encrypted signalling) |
| 4569 UDP | IAX2 trunks, only once one is set up |
| 10000–20000 UDP | the sound of calls (RTP), set in Settings |
| 8443 TCP | this web interface, HTTPS only (plain HTTP to it is sent on to HTTPS) |

If another phone system already has 5060, BitVoice takes the next free port and Health says so.

## Everything in the web interface
Every setting has a screen, and every screen has a chapter here. Nothing needs a configuration file, a command line or a second tool. For automation, the same screens are a REST API, and the configuration can follow a file in Git ([Configuration as code](#automation)), but neither is ever required.

## What BitVoice is for
BitVoice is first a phone system for offices and contact centres, and a hosted phone service for providers: everything in the first five parts of this manual. The 911 answering-point profile, the radio dispatch console and NG911 Core Services are a separate **lab and design-partner track** ([Public safety](#psap)): they are off the menu until switched on in Settings, and not certified for live use. Emergency calls from your own phones are not part of that track; they work on every phone system from the first start.

## Finding your way
- Press **⌘K** (Ctrl+K) anywhere to jump to a screen, or type an extension's name or number to open it.
- Every screen has a **Help** link at the top right that opens its chapter of this manual.
- **Changes** keeps every version of the configuration: you can see what changed and undo it.
- Three themes (Midnight, Dark and Light) are at the foot of the menu.`,
  },
  {
    id: 'extensions', title: 'Extensions', route: '/extensions', keys: 'user person desk number sip password device door speaker analog',
    body: `An extension is a number for a person or a desk. Phones sign in with the extension number and its **SIP password**; calls to the number ring every phone signed in as it, at once.

::figure internal-call

## Adding an extension
1. Open [Extensions](#/extensions) and press **Add extension**. The next free number and a strong SIP password are filled in.
2. Enter the person's **name** (shown on other phones when they call) and **email** (for voicemail by email).
3. Fill in the **911 location**: the site, floor and room. It is sent with emergency calls.
4. Choose what happens **If nobody answers**: voicemail by default, or another extension, a group or a menu.
5. Save. The extension can sign in at once.

## The fields
| Field | What it does |
|---|---|
| Number | 2 to 8 digits; no other extension, group, queue, room or feature code may have it |
| SIP password | what the phone signs in with; at least 12 characters; **Generate** makes a new one |
| Ring for | seconds before **If nobody answers** happens (25 by default) |
| Outside caller ID | the number shown on calls out; empty uses the trunk's |
| Can call outside numbers | off keeps the phone to internal calls and emergency numbers |
| Record calls | when Settings records only selected extensions |
| Hot desk | a shared phone that becomes whoever signs in at it |
| Encrypt calls to this phone | media encryption (SRTP); use it with TLS so the keys stay private |
| Department | groups this extension's call costs |

> Fill in the 911 location for every desk. An emergency call carries it to the answering point, and the RAY BAUM'S Act requires a dispatchable location for calls from offices in the US.

## Follow-me, pickup and intercom
Each person's own call handling is in its own box on the extension:

- **Follow-me** rings more numbers when the phones here are not answered: a mobile, another desk. They ring one after another, or all together, for their own ring time. With **Outside numbers press 1 to take the call** on, whoever answers on a mobile presses 1 first, so the mobile's own voicemail never takes the call. See [Feature codes](#codes).
- **Pickup group**: anyone in the same group answers this extension's ringing calls by dialling \`*8\`.
- **Allow intercom**: \`*80\` and this number answers on the speaker by itself. Switch it off for people who never want that.
- **Own speed dials**: short codes for this person only, dialled after \`*0\`.

## Forwarding right now
Below the settings, **Forwarding right now** shows and changes what the person set from their phone or the staff portal: do not disturb, and forwarding of every call, of calls while busy, and of calls not answered. These are live state, not configuration: changing them makes no revision.

## What is connected
- **Phone**: a desk phone, softphone or app.
- **Door station**: an intercom at a door. Whoever answers presses the key code, or **Open door** on Live calls. With a door controller address, BitVoice calls that address (2N, Axis, Akuvox, relay boards); without one, the code is sent to the station, which opens the door itself.
- **Overhead speaker**: calls to it are answered at once and heard over the speaker.
- **Analog adapter port**: an old phone, fax or alarm panel on an adapter. Calls use G.711 only, which faxes need.

## Connected devices
An extension that is open shows every device signed in as it: the phone's make and model, how it connects (UDP, TCP, TLS or the browser) and since when. The address and port to give a phone are shown there too.`,
  },
  {
    id: 'phones', title: 'Connecting phones', route: '/phones', keys: 'provisioning yealink polycom poly grandstream snom mac auto configure softphone',
    body: `Any SIP phone or app works: point it at this server's address and port 5060, with the extension number as user and its SIP password. Desk phones from the four big makers are set up automatically instead, so nobody types a password into a phone's keypad.

::figure provisioning

## Automatic set-up (provisioning)
1. On [Phones](#/phones) press **Add phone** and enter the phone's **MAC address** (on a sticker underneath) and the **extension** it belongs to.
2. Choose the make: Yealink, Poly, Grandstream (including the HT analog adapters), Snom, Fanvil, Cisco, Mitel, Sangoma, Algo or CyberData.
3. Tell the phone where BitVoice is: in your DHCP server, set **option 66** to the provisioning address shown on the screen, so every phone on the network finds it by itself; or type the same address in the phone's own web page, under auto provisioning.
4. Restart the phone. It fetches its file over HTTPS, signs in, and its line keys show who is on the phone.

**HTTPS or plain HTTP.** Phones fetch their file over HTTPS when they can. Some older phones cannot use HTTPS at all, and many do not trust the certificate BitVoice made itself; for those, the screen also shows a plain \`http://\` address on the same port. Plain HTTP is only for these files: every other page still goes to HTTPS. Over plain HTTP the phone's SIP password crosses the network unencrypted, so keep it to a trusted office network, or install a certificate from an authority and switch on **HTTPS only for phones**.

The file carries the extension, its password, the time zone, the codecs, busy lamp keys and the address of this server. Provisioning is protected by a user name and password the phones are given once.

| Make | Models tested | What it gets |
|---|---|---|
| Yealink | T3x, T4x, T5x | account, BLF keys, time, codecs |
| Poly | VVX, Edge E | the same, in Poly's own format |
| Grandstream | GRP, GXP, HT802/814 | account per port on adapters |
| Snom | D7xx | account, keys, time |

These makes are provisioned too, with files written from each maker's documented format but **not yet tried on their devices** (the Phones screen marks them untested):

| Make | Models | File the device asks for |
|---|---|---|
| Fanvil | X and V series | its MAC address .cfg |
| Cisco | 6800, 7800, 8800 multiplatform (3PCC), SPA adapters | its MAC address .xml (enterprise firmware needs the multiplatform firmware first) |
| Mitel (Aastra) | 6800, 6900 SIP | its MAC address .cfg, in capitals |
| Sangoma | S-series | the Fanvil-format file |
| Algo | paging speakers, strobes, adapters | algop and its MAC address .conf |
| CyberData | speakers, intercoms, paging adapters | its MAC address .xml |

Each gets its account, the server (and the backup server, with several servers), the transport and encryption, and the voicemail number where it has one. Set Algo and CyberData speakers' extensions to **Overhead speaker** so calls to them are answered at once.

## Phones without provisioning
Softphones and other makes take the same three things: server (this computer's address, port 5060, or 5061 with TLS), user (the extension number) and password (its SIP password). Health lists phones that tried a wrong password; after a few tries the address is banned for a while (see [Security](#security)).

## The browser phone
The phone icon at the bottom of every screen makes this browser a phone for an extension, with your microphone and speakers. It uses WebRTC: the sound is encrypted end to end, and it works through most firewalls without any setting.

## The staff phone
Open \`#/phone\` on any computer or mobile browser: people sign in with their extension and SIP password, without an administrator account. It is a full phone: dial, answer, hold, transfer and the keypad. For their own forwarding and voicemail they use the staff portal at \`#/me\` (see [The staff portal](#portal)).

## The mobile apps
The BitVoice apps for iPhone and Android are paired from the extension's page with a QR code. Calls wake the app through Apple's and Google's push services, so it rings even when it is closed.`,
  },
  {
    id: 'mobile', title: 'Mobile apps', keys: 'iphone android app push pair qr ios callkit',
    body: `The BitVoice apps for iPhone and Android make a mobile phone one of an extension's phones. It rings like a desk phone, on the phone's own call screen, even when the app is closed.

::figure mobile

## Setting up push
A closed app is woken for calls by a push message. In [Settings](#/settings), **Mobile apps**, choose one:
| Push | What you need |
|---|---|
| **Push relay** | BioDream's relay (with hosting or support) or your own \`bvpush\`: its **Relay address**, **Key ID** and **Secret** |
| **Own apps** | your own builds: for Apple, the **Team ID**, **Bundle ID**, **Key ID** and **Key (.p8)**; for Google, the **Firebase service account (JSON)** |

Set **Address the apps use** to this server's https address as it is reached from anywhere, not only the office.

> With push off, the app rings only while it is open.

## Pairing a phone
1. Open the extension and press **Add a phone**.
2. Scan the QR code in the app. The code works once, for ten minutes.

## When a call comes in
BitVoice wakes the app; it connects, rings on the phone's own call screen, and is answered like any other phone of the extension: ring groups and queues ring it with the extension's other phones. The extension's page lists its paired phones, whether each **Can be woken for calls**, when it was last seen and last woken, and any push problem. **Unpair** stops a phone ringing at once, for a lost phone.`,
  },
  {
    id: 'trunks', title: 'Trunks', route: '/trunks', keys: 'carrier provider sip trunk line register channels codecs',
    body: `A trunk is your account with a phone company. It carries calls to and from outside numbers. Most are SIP trunks; an Asterisk or FreePBX server, or an IAX2 provider, can be reached with IAX2 instead.

::figure trunk

## Adding a SIP trunk
1. On [Trunks](#/trunks) press **Add trunk**.
2. Enter the **provider server** your phone company gave you (\`sip.provider.com\`), and the port if it is not 5060.
3. Enter the **username** and **password**, and switch on **Register** if the provider says to (most do).
4. Save. Within a few seconds the trunk shows **registered**; if not, the reason is shown beside it.
5. Add an **incoming route** for your number and an **outgoing route** that uses this trunk ([Call routing](#routes)).

| Field | What it does |
|---|---|
| Transport | UDP (usual), TCP, or TLS for encrypted signalling |
| Register | sign in every few minutes, so the provider knows where to send calls |
| Authentication username | only when the provider gives a separate one |
| From domain | the domain in the caller's address, when the provider asks for one |
| Caller ID | the number shown on calls out; an extension's own caller ID wins |
| Also accept calls from | the provider's other addresses or networks that send calls in |
| Calls at once | the limit your plan allows; calls beyond it go to the next trunk in the route |
| Encrypt media | SRTP, for providers that support it |
| Codecs | leave it on *same as Settings*, or choose what this provider supports, best first |
| Rates | what calls cost by number prefix, for [Costs](#costs) |

> A trunk that accepts calls by address alone should list only the provider's own addresses. Networks wider than a /16 are refused: they would let anyone on the internet pass as your provider.

## Cloud providers
**Start from** fills in Microsoft Teams Direct Routing, Amazon Chime SDK Voice Connector or Azure Communication Services, with a note on what to set on their side. Teams is described in its own chapter ([Microsoft Teams](#teams)).

## IAX2 trunks to Asterisk and FreePBX
Choose **IAX2** as the protocol to join another Asterisk or FreePBX server, or an IAX2 provider. IAX2 carries signalling and sound on one UDP port, 4569, which makes it easy through firewalls.

::figure iax2

- **Server address**: the other server. Leave it empty when the other server registers to BitVoice instead (a branch office behind a changing address); then the username and password are what it registers with.
- **Username and password**: the same on both sides. BitVoice answers and sends MD5 challenges, and handles call tokens.
- **Register**: sign in to the other server, so it can send calls here.

On the Asterisk side, a peer like this is all it takes:

\`\`\`
[bitvoice]
type=friend
host=dynamic
secret=the shared password
context=from-bitvoice
\`\`\`

IAX2 port 4569 opens only when the first IAX2 trunk is added, so an Asterisk running on the same computer keeps it until then.

## What the status means
| Status | Meaning |
|---|---|
| registered | signed in; calls can come in |
| ready | does not register, or IAX2 waiting for calls |
| unreachable | the server's name is not in DNS, or nothing answers |
| failed | the provider refused the sign-in; the reason is shown |
| standby | servers share this trunk one at a time and another holds it |`,
  },
  {
    id: 'routes', title: 'Call routing', route: '/routes', keys: 'inbound outbound did number pattern dial plan route prepend emergency',
    body: `Call routing decides two things: where calls from outside go, by the number they dialled, and which trunk calls to outside numbers use.

::figure call-path

## Incoming routes
Each incoming route matches the number that was called (or any number) and sends the call to a destination: an extension, ring group, queue, menu, schedule, script, conference room, AI receptionist or voicemail. Numbers match in any form: +1 212 555 0100, 2125550100 and 12125550100 are the same.

- A route for one **number** wins over a route for any number.
- A route can be limited to one **trunk**, for numbers that two providers both send.
- With **emergency mode** on (Settings), every incoming call goes to one place instead, whatever the routes say: an evacuation, a closure, an incident.

## Outgoing routes
Outgoing routes match what people dial, by pattern, and send it to trunks in order: if the first is down or full, the next is tried.

::figure outbound-routes

| Pattern | Matches |
|---|---|
| \`X\` | any digit |
| \`Z\` | 1 to 9 |
| \`N\` | 2 to 9 |
| \`[1-5]\` | one of a set |
| \`.\` at the end | one or more further digits |
| \`NXXNXXXXXX\` | a ten-digit North American number |
| \`1NXXNXXXXXX\` | the same with 1 in front |
| \`011.\` | an international call from the US |

- **Remove** takes digits off the front (a 9 dialled for an outside line); **Then add in front** puts some on (a country code).
- The order matters: the first route whose pattern matches is used. Drag routes to change it.
- Premium numbers (900 and the like) are refused unless a route names them.

## Emergency numbers
The emergency numbers always work. A route that names the number exactly is used first; with none, every working trunk is tried in turn. A call to them is never refused for an extension's limits, a hosted organisation's limits or a missing route. Health warns when no trunk can carry them.

> Test emergency routing with your provider's test number (933 in the US) rather than 911. 933 reads back the address the call carries.`,
  },
  {
    id: 'ringgroups', title: 'Ring groups', route: '/ring-groups', keys: 'hunt group ring all one after another sales team',
    body: `A ring group is one number that rings several extensions: a sales team, the front desk and its back-up, everyone in the workshop.

::figure ring-strategies

## Ringing strategies
- **All at once**: every member's phones ring together, and the first to answer takes the call. The others stop ringing.
- **One after another**: each member rings for the group's ring time, in the order listed. Move members with the arrows, or Alt+↑ and Alt+↓.

Members who are away (in a meeting on their calendar, or with do not disturb on) are skipped; the group rings the others. Members with the mobile app are woken by a push and rung too.

## If nobody answers
When nobody answers within the ring time, the call goes to **If nobody answers**: a member's voicemail, a queue, a menu, another group. With nothing set, it rings until the caller gives up.

## Groups, queues or pickup?
| Use | When |
|---|---|
| A ring group | a few people, any of whom can take the call, and the call should ring now |
| A queue | callers may have to wait for someone free, and should hear their place |
| A pickup group | each person has their own calls, but colleagues may answer for them |`,
  },
  {
    id: 'queues', title: 'Queues', route: '/queues', keys: 'sticky vip priority returning caller acd contact centre agents waiting longest idle wallboard fewest calls linear random weighted penalty penalties position order weight announce place in line expected wait',
    body: `A queue holds callers in order, with music, and rings the next free agent. It is the heart of a contact centre, and useful in any office where calls come faster than people can take them.

::figure queue

## How calls are offered
| Strategy | Who is rung |
|---|---|
| Longest idle | the free agent who has waited longest since their last call |
| Round robin | each free agent in turn, carrying on from the last one |
| Ring all | every free agent at once |
| Fewest calls | the free agent who took the fewest calls today |
| In order (linear) | always from the top of the list: the first free agent |
| Random | any free agent, by chance |
| Weighted random | by chance, agents with a lower penalty much more often |

- **Order**: the agents' list is in order; move them up and down. In order and Round robin follow it.
- **Penalties** rank agents (0 to 100, 0 when empty): calls are offered to the free agents with the lowest penalty, and an agent with a higher one is rung only when nobody with a lower one is free. Use them for a first line and an overflow line in one queue.
- **Weight** ranks queues that share agents: while a queue with a higher weight has someone waiting, its agents take its callers first.
- **Wrap-up** gives an agent time after a call before the next.
- **Longest wait**: callers who wait longer go to the queue's timeout destination. 0 waits for ever.
- **Hold music**: callers hear the queue's music (any class from [Sounds](#/sounds)), the system's, or the queue tone.
- **Tell waiting callers** their place in line and the expected wait (the average wait of answered callers today, in minutes), every so many seconds, in the queue's language. The words come from the queue prompts in [Sounds](#/sounds); until those are recorded, nothing is said.

The **Queues** screen is a live wallboard: who is waiting, for how long, and each agent's state.

## Agents
Agents dial \`*45\` to log in or out of every queue, and \`*46\` to pause (a break) and resume; paused and logged-out agents are not offered calls. The wallboard has the same buttons, and shows why an agent paused.

| State | Meaning |
|---|---|
| available | free; will be offered the next caller |
| ringing | being offered a call |
| busy | on a call |
| wrap-up | just finished; the wrap-up time is running |
| paused | on a break, with the reason given |
| away | in a meeting or on time off, from their calendar |
| logged out | not taking queue calls at all |

## Supervisors
On [Live calls](#/calls), **Supervise** rings a supervisor's phone; when they answer they **listen** (heard by nobody), **whisper** (heard by the agent only) or **barge** (heard by both), and can switch between them during the call.

## Sticky queues and VIPs
Switch on **Sticky** in a queue and a returning caller (one answered in the last 30 days, or as many as you set) is offered first to the agent who answered them last time, if that agent is free; if they do not answer, the queue goes on as usual. **VIP callers**, at the foot of Queues, go to the front of every queue they call, after any VIP already waiting, and are marked VIP on the board.`,
  },
  {
    id: 'sounds', title: 'Sounds: prompts, languages and hold music', route: '/sounds', keys: 'sounds prompts recordings language languages spanish french german italian portuguese dutch polish translate speech text to speech tts music on hold moh hold music track shuffle class',
    body: `[Sounds](#/sounds) holds everything BitVoice says or plays to callers, apart from greetings and menu prompts, which are recorded where they are used.

## Prompts, in every language
A **prompt** is one thing BitVoice says: "Please enter your PIN", "Your place in line is", the digits it reads out. The dial-by-name directory, dial-in (DISA), wake-up calls and queue announcements speak with them. BitVoice has the words of every prompt in English, Spanish, French, German, Italian, Portuguese, Dutch and Polish; numbers are read digit by digit, which sounds the same way in each.

- **Callers hear** sets the language for everyone. A queue, a dial-in number, a directory or an extension can choose another.
- Each prompt can be **recorded in the browser**, **uploaded** (any audio file; it is turned into telephone audio) or **made by the speech service**: with a speech model set in Settings, AI, one button makes every prompt of a language from its words. Prompts already recorded are kept unless you ask to replace them.
- **Add a language** for any other language (a code such as sv, ja or pt-BR); its prompts are recorded or uploaded, with the English words shown as a guide.
- A prompt missing in a language is played in the default language, then in English; with none recorded at all, callers hear a short tone, so every feature works before a single prompt is recorded.

## Hold music
Hold music is grouped in **classes** (Calm, Jazz, Sales offers…), each a list of tracks played one after another, from the start or **shuffled**. Upload tracks of up to fifteen minutes in any format the browser reads.

| Who hears it | Which music |
|---|---|
| someone put on hold | the music chosen on the extension of the person holding, else the default |
| a parked caller | the music of the person who parked them, else the default |
| a caller waiting in a queue | the queue's music, else the default |
| a caller waiting for a conference leader | the default |

**Default for everyone** can be the hold tone, so nothing changes until you choose music. Choosing **the hold tone** on a queue or extension keeps the tone there.

> Only play music you have the rights to play to callers: most recorded music needs a licence for on-hold use.`,
  },
  {
    id: 'directory', title: 'Dial-by-name directory', route: '/directories', keys: 'directory dial by name company directory spell name keypad 411 staff directory spoken name',
    body: `A [dial-by-name directory](#/directories) lets callers reach someone without knowing their number: they spell the first letters of the name on the keypad (2 for ABC, 3 for DEF, up to 9 for WXYZ) and are put through.

1. **Add directory**, name it, and give it a number (411, say) or leave the number empty and use it as a menu option.
2. Choose what callers spell: the **last name** (the usual), the **first name**, or **either**.
3. Choose **who is listed**, or nobody to list everyone with a phone.

Callers hear "Please enter the first three letters of the person's name". One match is put through at once. Several are read out, eight at a time: each person's name, then "to call this person, press 1", 2 and so on, with 9 for more names. No match: "No one matches", and they try again (three tries).

**Spoken names**: record each person's name on their extension (**Spoken name**: record in the browser or upload a file), and the directory says it; until then it reads out the extension number. The directory's own words come from the directory prompts in [Sounds](#/sounds), in its language.`,
  },
  {
    id: 'dialin', title: 'Dial-in (DISA)', route: '/dial-in', keys: 'disa dial in direct inward system access call in pin call out through the office road warrior mobile caller id',
    body: `A [dial-in](#/dial-in) (DISA, "direct inward system access") lets someone call in from outside, prove who they are, and call out again through the office: the call goes out with the office's caller ID, its routes and its rates, as if they were at their desk.

1. **Add dial-in**, and set a **PIN** (4 to 10 digits), the numbers allowed to use it (**Only from these numbers**), or both. One of the two is required.
2. Choose the extension the calls are **made as**: its outside-call permission, caller ID and outgoing routes apply. It must be allowed outside calls.
3. Give it a number to dial inside, and send a phone number to it on [Call routing](#/routes) (choose the dial-in as the destination).

Callers hear "Please enter your PIN, then press hash", then "Please enter the number to call, then press hash", and are put through. The words come from the dial-in prompts in [Sounds](#/sounds).

> A dial-in is a door to your phone bill. Three wrong PINs end the call, every attempt (refused number, wrong PIN, connected call) is logged and shown in Security, and calls follow the extension's limits. Use a long PIN, list callers' numbers where you can (caller ID can be faked, so keep the PIN too), and keep the extension's outside calls to what is needed.`,
  },
  {
    id: 'hotel', title: 'Hotel and wake-up calls', route: '/hotel', keys: 'hotel wake up wake-up alarm morning call guest room check in check out pms property management fias opera housekeeping room status charges hospitality markup',
    body: `## Wake-up calls
Anyone can book a wake-up call: guests, night shifts, people on call. From their own phone they dial \`*62\`: BitVoice says the one set (if any), then they enter the time as four digits (\`0630\`), or press star to cancel it. On [Hotel](#/hotel) an administrator books one for any extension, once or **every day**.

At the time, the phone rings with the system's name; answered, it hears "Good morning. This is your wake-up call." (the wake-up prompt in [Sounds](#/sounds), in the guest's or extension's language). Unanswered, it is tried again (3 tries, 5 minutes apart, both set under Settings on the page), and after the last the **reception** extension can be rung. With several servers, only one makes wake-up calls.

## Guest rooms
Switch on **Guest room** on the extension of each room's phone. On Hotel each room shows its guest, wake-up call and room status, with **Check in** and **Check out** buttons.

- **Bar vacant rooms**: rooms with nobody checked in call only extensions and emergency numbers. Emergency calls always work.
- **Clear on check-out**: deletes the room's voicemail and cancels its wake-up call, forwarding and do not disturb, ready for the next guest.
- A guest's language (from the check-in) is the language the room's prompts are said in.
- **Room status**: housekeeping dial \`*68\` and a digit from the room's phone (\`*683\`); the hotel system decides what each digit means.

## The hotel system (PMS)
- **FIAS**: BitVoice connects to the FIAS interface of the hotel system (Oracle Opera and the many systems that speak FIAS) at its address and port. It receives check-ins, room moves, check-outs and wake-up bookings, and sends wake-up results, room status and call charges. While the link is down, what it would send waits and goes when it is back; the page says whether it is linked.
- **REST API**: the hotel system calls \`POST /api/hotel/rooms/<room>/checkin\` (name, account, language), \`/checkout\`, and \`PUT /api/wakeups/<room>\`, with an API key from Settings, Automation. Charges are posted as JSON to the address you give.
- **Charge calls**: answered outside calls from occupied rooms go on the guest's bill, at the trunk's rates (from [Costs](#/costs)) plus the **markup**.`,
  },
  {
    id: 'menus', title: 'Menus (auto attendant)', route: '/menus', keys: 'ivr press 1 prompt greeting keypad auto attendant',
    body: `A menu (an auto attendant) answers, plays a prompt and sends the caller where they press: 1 for sales, 2 for support, 0 for reception.

::figure menu

## Setting up a menu
1. On [Menus](#/menus) press **Add menu** and give it a name (and a number, if staff should be able to dial it).
2. Add **options**: a key, a label, and where the key goes.
3. Record the **prompt** in the browser, or upload a recording in any format (it is converted to telephone sound). Say every option: "For sales press 1, for support press 2."
4. Choose what happens when the caller presses nothing: the **timeout destination**, after the prompt has played the number of **repeats** you set.
5. Point an incoming route (or a schedule) at the menu.

## Choices
- **Callers may dial an extension**: callers who know the number type it during the prompt.
- **Wait for a key**: seconds after the prompt before it repeats.
- A key can go to another menu, for a second level ("for billing questions press 1").

> Keep menus short: four options at most, the most common first, and always a way to a person. Callers who are lost hang up.

## Prompts in other voices
With AI switched on (Settings), a prompt can be written as text and read by the AI voice instead of recorded.`,
  },
  {
    id: 'schedules', title: 'Schedules', route: '/schedules', keys: 'opening hours holiday closed night service time zone',
    body: `A schedule sends calls one way when you are open and another when you are closed. Point an incoming route at a schedule, and the schedule at the menu (open) and voicemail (closed).

::figure schedule

## Open hours
Each line of hours is a set of days and a time from and to. Several lines are allowed: Monday to Friday 9:00 to 17:00, and Saturday 10:00 to 13:00. Hours may cross midnight (22:00 to 06:00) for night shifts.

## Holidays
**Holidays** count as closed all day, whatever the hours say. Add each date with a name; the name is shown on [Call flow](#flow) when you try a call on that day.

## Time zone
The time zone is the one in Settings. When the clocks change, schedules follow the local time.

> Try a call on [Call flow](#flow) with a time and date (a holiday, a Saturday night) to see which side of the schedule it takes, before it matters.`,
  },
  {
    id: 'scripts', title: 'Scripts (JavaScript and Lua)', route: '/scripts', keys: 'code javascript lua route logic api program',
    body: `A script decides where a call goes when menus, schedules and groups are not enough: callers from one area to one office, VIP numbers straight to a person, a decision from your own data. Scripts are written in JavaScript or Lua on [Scripts](#/scripts).

::figure scripts

## Safe by design
- A script runs for at most 200 ms. If it fails or takes too long, the call goes to the script's **fallback**, so a mistake never loses a call.
- It runs in a sandbox: it cannot place calls, reach the network or the disk. It only chooses.
- Emergency calls never run scripts.

## What a script sees
\`call\` has:
| Field | What it is |
|---|---|
| \`from\`, \`fromName\` | the caller's number and name |
| \`to\` | the number dialled |
| \`direction\` | \`inbound\` or \`internal\` |
| \`trunk\` | the trunk it came in on |
| \`weekday\`, \`hour\`, \`minute\` | the time now (0 is Sunday) |
| \`date\` | YYYY-MM-DD |

## What it returns
A destination, \`{type, target}\`: type is \`extension\`, \`ringgroup\`, \`queue\`, \`menu\`, \`schedule\`, \`voicemail\` or \`hangup\`, and target is its number. Returning nothing uses the fallback. \`log("text")\` writes to the script's run list.

### JavaScript
\`\`\`
if (call.from.startsWith("212")) {
  log("New York caller")
  return { type: "ringgroup", target: "600" }
}
if (call.weekday === 0 || call.weekday === 6) return { type: "voicemail", target: "100" }
\`\`\`

### Lua
\`\`\`
if string.sub(call.from, 1, 3) == "212" then
  log("New York caller")
  return { type = "ringgroup", target = "600" }
end
if call.weekday == 0 or call.weekday == 6 then
  return { type = "voicemail", target = "100" }
end
\`\`\`

## Using a script
Point an incoming route, a menu key or a schedule at the script, as at any destination. **Test** runs it with a call you describe, without placing one; the run list shows each real run with its decision, time taken and any error. The [call flow](#flow) runs scripts too when you try a call.`,
  },
  {
    id: 'voicemail', title: 'Voicemail', route: '/voicemail', keys: 'messages mailbox greeting pin email transcript',
    body: `Each extension can have a mailbox. Callers who are not answered hear its greeting and leave a message; the person hears it on their phone, reads it on the screen, or gets it by email.

::figure voicemail

## Switching it on
On the extension, switch on **Voicemail**. **If nobody answers** is then set to the mailbox. Give it a **PIN** (4 to 10 digits): it opens the mailbox from another phone, and signs the person in to the staff portal and at hot desks.

| Setting | What it does |
|---|---|
| Longest message | 10 to 900 seconds (180 by default) |
| Email new messages | to the extension's email address, with the message attached |
| Greeting | recorded in the browser, uploaded, or recorded from the phone; without one, callers hear a beep |

## Listening
- From the extension's own phone, dial \`*97\`: messages play newest first; keys replay, delete or keep each one.
- From any other phone, dial \`*97\`, enter the mailbox number and its PIN.
- On [Voicemail](#/voicemail) (administrators) or in the staff portal (the person): play, mark heard, delete.
- The phone's message light is on while there are new messages.

## With AI
With **Transcribe and summarise voicemail** on (Settings, AI), each message is written out, summarised in a line, and marked urgent, normal or low priority with its kind (sales, support, billing). Urgent messages raise an alert on every administrator's screen. See [AI](#ai).

## Sound clean-up
**Sound clean-up** (Settings) removes steady background noise and evens out levels as messages are saved, so a message from a car or a busy street is easier to hear.

> Messages under a [legal hold](#holds) cannot be deleted, by anyone, until the hold is released.`,
  },
  {
    id: 'recording', title: 'Call recording and card payments', keys: 'record pci pause resume card payment compliance retention',
    body: `Recording is set in [Settings](#/settings), **Call recording**: off, every call, or only the calls of extensions marked **Record calls**. Recordings play from [Call history](#/history), and with AI on they are written out and summarised.

::figure recording

## Telling people
Some places need everyone on a call to be told it is recorded: **Announce**, **Beep** and rules by region are in [Recording consent](#consent).

## Card payments (PCI DSS)
Card numbers must not be kept in recordings. Before a caller reads one out, the person on the extension presses \`*7\`; \`*8\` resumes. **Pause recording** on [Live calls](#/calls) does the same. The paused stretch is silent in the recording and listed in the call history, so an auditor can see where and for how long.

## Conference rooms
A room with **Record the room** on is kept as one recording of the whole room ([Conference rooms](#conferences)).

## How long things are kept
Settings sets how many days recordings, call history and voicemail are kept; older ones are deleted within the hour. Anything under a [legal hold](#holds) is kept whatever its age. The [archive](#archive) can keep copies elsewhere.

Administrators and viewers can play recordings and read transcripts; only administrators can transcribe a call, or transcribe it again. Playing a recording or reading its transcript is noted in the audit log.

> None of this is legal advice: recording laws differ by country and state. Check the laws where you and your callers are.`,
  },
  {
    id: 'metrics', title: 'Metrics: Prometheus, OpenTelemetry, Grafana, SNMP and call records', route: '/settings', keys: 'snmp mib trap zabbix prtg librenms nagios solarwinds syslog siem csv master.csv cdr postgres database billing reports metrics prometheus grafana opentelemetry otel otlp monitoring dashboard scrape observability',
    body: `**Prometheus** scrapes \`/metrics\` on the web address. Make an API token under Administrators (a viewer token is enough) and give it to Prometheus as its bearer token:

::figure metrics

\`\`\`
scrape_configs:
  - job_name: bitvoice
    scheme: https
    authorization: { credentials: bv_... }
    static_configs: [{ targets: ['pbx.example.com'] }]
\`\`\`

It reads calls in progress (by state), calls ended (by direction and outcome), talk time, phones registered, each trunk's state and round trip, callers waiting and agents in each queue, sound packets received and lost, a histogram of call quality scores (MOS), and callbacks booked. Counters start again from zero when BitVoice restarts, as Prometheus expects.

**OpenTelemetry**: in Settings, Metrics, enter a collector's OTLP/HTTP address (and a header such as Authorization, with its value) to have the same measurements pushed every minute (or as often as you set, from 15 seconds). **Push now** checks the saved settings.

**Grafana dashboard** downloads a ready-made dashboard: import it in Grafana and pick your Prometheus data source.

## SNMP
For monitoring systems that speak SNMP (Zabbix, PRTG, LibreNMS, Nagios, SolarWinds), switch on the **SNMP agent** in Settings. It answers SNMPv2c GET, GETNEXT and GETBULK, read only, on UDP port 161 (or another you choose: port 161 needs the right to open low ports on Linux), and only from the networks you list. Choose a **community** that is not public or private: it is a password that crosses the network in clear.

It serves the standard system group (sysDescr, sysUpTime, sysContact, sysName, sysLocation) and BitVoice's own values under the **base OID**: calls in progress and ringing, phones registered, trunks up and set up, callers waiting, calls ended, talk seconds, lost packets, average call quality, a trunk table (name, up or down, latency) and a queue table (waiting, agents, answered, abandoned). Download **BITVOICE-MIB** beside the settings for your monitoring to load. BioDream LLC has no enterprise number yet, so the default base OID is in Net-SNMP's experimental space; put one under your own enterprise number to keep it stable.

**Traps** (SNMPv2) go to each receiver you list when a trunk goes down or comes back, an emergency call is made, or a server of a cluster stops answering. SNMPv3 is not supported yet.

## Call records elsewhere
Beside Call history, each record can go, as its call ends, to:
- **Syslog** (RFC 5424, as JSON) over UDP, TCP or TLS, for a log server or a SIEM.
- **A CSV file a day**, with Asterisk's Master.csv columns, so the reports and billing tools made for Asterisk read it. Download the files at the foot of [Call history](#/history). Cells that a spreadsheet would run as a formula are written safely.
- **A PostgreSQL table** in another database, made the first time with Asterisk's \`cdr\` columns (calldate, clid, src, dst, duration, billsec, disposition, uniqueid…) plus BitVoice's direction, trunk, cost and MOS. Each call is written once.

**Send a test record** tries the syslog server and the database with the settings saved.`,
  },
  {
    id: 'crm', title: 'CRM: caller cards and call logging', route: '/settings', keys: 'crm salesforce hubspot zoho pipedrive webhook screen pop caller card lookup log calls integration',
    body: `Connect a customer system in [Settings](#/settings), CRM. It works with any system that has a web address, without code written for each product.

::figure crm

## Caller cards
While a call from outside rings, Live calls shows a card: who it is (from your CRM, or the extension's name), how often they have called in 90 days, any [callback](#/callbacks) they are waiting for, and the summary of their last transcribed call. The browser phone shows a shorter card to the person answering: who it is, past calls and waiting callbacks, but not other people's call summaries.

For the name, set **Lookup address**: BitVoice asks it (GET) with the caller's number in place of {number}, sending **Lookup header** with its value (a vault reference works), and expects JSON with name, company, url and notes; 404 means unknown. Answers are kept for five minutes. **Open in CRM link** puts a link to the caller on the card. Use **Look it up** to try a number before saving.

## Call logging
Set **Call log address** and **Signing secret**, and every call is sent there as it ends (event call.ended), again with its summary, action items and transcript once transcribed (call.summarized), and each booked callback (callback.new). Each POST carries X-BitVoice-Event, X-BitVoice-Timestamp and X-BitVoice-Signature: sha256= followed by the HMAC-SHA256, with the secret, of the timestamp, a dot and the body. A failed delivery is tried four times over about a minute; if it still fails, Health says so. **Send a test event** checks the address and the secret.`,
  },
  {
    id: 'servers', title: 'Several servers as one phone system', route: '/servers', keys: 'servers cluster failover high availability redundancy second server backup shared database several servers shared address virtual ip vip floating ip dns srv takeover handover standby one at a time',
    body: `Two or more BitVoice servers that share one PostgreSQL database work as one phone system:

::figure cluster

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

**Try it first: the lab.** BitVoice comes with a lab that runs two servers, their database and test phones on one computer with Docker, each on an address of its own: \`deploy/lab/run.sh up\` builds and starts it and sets the shared address; \`run.sh call\` calls between phones on different servers; \`run.sh failover\` kills the server carrying a call through the shared address and checks the call carries on (the sound comes back in about five seconds); \`run.sh impaired\` calls from a phone on a bad link (delay, jitter and 5% loss) and checks the call quality BitVoice reports; \`run.sh down\` removes it. The servers' pages are at https://127.0.0.1:18501 and :18502 (user admin).`,
  },
  {
    id: 'database', title: 'Where the data is kept (SQLite or PostgreSQL)', route: '/settings', keys: 'database sqlite postgresql postgres backup data storage failover several servers reporting power bi grafana move',
    body: `BitVoice keeps its settings and their saved versions, users, contacts, call records, the audit log, texts, notes and transcripts in a database. Recordings and voicemail stay as sound files in the data folder.

::figure database

**This server's own file** (SQLite, the default): a single file, \`bitvoice.db\` in the data folder, with nothing to install. Back up the data folder and you have everything.

**PostgreSQL**: for several BitVoice servers sharing one set of data, or for reports in tools such as Power BI, Grafana or Metabase. In Settings, **Database**, **Move the data into PostgreSQL**: make an empty database and a user who owns it, enter its address (postgres://user:password@host:5432/database), press **Test**, then **Copy the data and restart on PostgreSQL**. Everything is copied in one go (all of it or none of it) and BitVoice restarts on it; the old file stays where it was. Moving back into the server's own file works the same way. Moves are refused while calls are in progress.

Servers sharing a PostgreSQL database see each other's settings changes within five seconds. Each still answers its own phones and calls; for a second server to take over a first one's phones, the phones need both addresses (or one address that moves).

The database can also be named when BitVoice starts, with \`-database postgres://…\` or the BITVOICE_DATABASE environment variable; it is then changed there, not in Settings.

**Older installations**: on the first start of this version, BitVoice copies its old JSON files into the database and moves them into \`pre-database\` in the data folder.`,
  },
  {
    id: 'teams', title: 'Microsoft Teams (Direct Routing)', route: '/trunks', keys: 'teams microsoft direct routing sbc session border controller office 365 pstn gateway powershell New-CsOnlinePSTNGateway certificate',
    body: `BitVoice can be the session border controller (SBC) for a Microsoft Teams tenant, so Teams users call BitVoice extensions and your phone lines, and BitVoice rings Teams users.

::figure teams

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

::figure calendar

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

::figure insight

In a call's **Transcript**, **How it went** shows, measured from the recording's sound alone: how much each side talked, each side's longest stretch, how often each started talking over the other, how long both spoke at once, and the quiet (a long silence is often a hold or dead air).

With **Note how each transcribed call went** on (Settings, AI), the AI also reads the transcript and says how the caller seemed at the start and the end, and lists up to six moments: **Went well**, **Look at** (talking over the caller, a long hold, an unclear answer), **Not answered** (a question the caller asked that got no answer) and **Promised** (something someone said they would do), each with the time and the words said, so you can go straight to it. An administrator can ask for it on any transcribed call with **Look at this call**.

On [Call quality](#/quality), **Conversations, by who took them** sums the transcribed calls of the period per person: their share of the talking, their longest stretch, how often they talked over the caller, quiet time, calls that ended with the caller upset, and the moments flagged. Use it to choose calls to listen to, not to rank people.`,
  },
  {
    id: 'calllinks', title: 'Call links and website calling', route: '/call-links', keys: 'call link website call button qr code click to call browser web call no app',
    body: `A call link is a web address that rings your business from any browser: no phone number, no app. Put it in an email signature, print its **QR code** on a flyer or a shop window, or paste the **website button** into your site.

::figure calllinks

On [Call links](#/call-links), add a link, choose where its calls go (an extension, a group, a queue, a menu, the AI receptionist, a schedule or voicemail), and save: the page shows its address and QR code. The caller sees the title and words you set, gives their name if you ask for it, allows the microphone and presses **Call now**; the keypad works for menus. Whoever answers sees **Web:** and the caller's name.

Switch **Open** off to close the line (the page says so). **Calls at once** limits how many people can call through one link (5 unless set). The address holds a long random part so it cannot be guessed; repeated wrong addresses are blocked like failed sign-ins. Callers' microphones need the page to be served over HTTPS, so give out the HTTPS address.`,
  },
  {
    id: 'timeline', title: 'Customer timeline', route: '/timeline', keys: 'timeline customer history notes everything one view contact calls texts voicemail',
    body: `The customer timeline shows everything with one phone number over the last year, newest first, so whoever picks up knows the whole story.

::figure timeline

## What it shows
| Item | With |
|---|---|
| Calls | who took them, how long, and with a transcript, its summary, the recording and the transcript |
| Voicemails | their summaries |
| Texts | both ways |
| Callbacks | asked for, and whether done |
| Notes | the team's own notes |

A number matches however it is written: +1 212 555 0100 and 2125550100 are the same.

## Opening it
From a caller card (**Timeline**), from Contacts, from an incoming call in Call history, from a text conversation, or type a number at the top of [Timeline](#/timeline).

## Notes
Administrators can **Add a note for the team**: it shows on the timeline for everyone, with who wrote it and when, and can be removed.`,
  },
  {
    id: 'texts', title: 'Business texting (SMS)', route: '/texts', keys: 'sms text message texting textmagic 10dlc stop opt out reply provider',
    body: `Send and receive text messages from your business numbers on **Texts**. Texts go through a texting provider. Built in: TextMagic, Twilio, SignalWire, Telnyx, Bandwidth, Vonage, Plivo, Flowroute and VoIP.ms, plus **Your own webhook** for any other service or a gateway of your own. Any number of provider accounts can be used at once: each number says which one carries it.

::figure texts

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

::figure briefing

It works the same when the AI receptionist hands a caller to a person: the briefing comes from what the caller told the assistant.

It needs the AI service and a speech-to-text model (Settings, AI), and the call's audio so far (up to five minutes) is sent to that service to be written out. A summary takes a few seconds, so it may appear just after the phone starts ringing.`,
  },
  {
    id: 'spam', title: 'Spam protection and STIR/SHAKEN', route: '/settings', keys: 'spam robocall block number blocklist stir shaken attestation verstat spoof spoofed caller id withheld anonymous label sign signing sti certificate identity header passport x5u',
    body: `In the US and Canada, the provider a call starts with signs its caller ID (**STIR/SHAKEN**): **A** when it knows the caller may use the number, **B** when it knows the caller but not the number, **C** when it only passed the call on. Your provider either tells BitVoice its verdict (verstat) or passes the signed Identity header on, which BitVoice then checks itself: the signature, that it is under a minute old, and that it names the numbers of this call. The verdict shows as **Verified**, **Partly verified**, **Unverified** or **Not verified** on Live calls and in Call history.

::figure spam

In Settings, **Spam protection**, choose what happens when a caller ID fails verification (ring, ring labelled, or refuse), when it is not verified at all, and when the number is withheld. A labelled call rings with **Spam?** (or your own label) before the caller's name. **Blocked numbers** are refused; an entry ending in * blocks a range (+1900* is every US premium-rate number). **Always allowed** numbers are never refused or labelled. In Call history, the block button beside a caller adds them to the list; refused calls are listed as **Blocked**.

Within an hour of an emergency call from any extension, no call is refused (blocked ones ring labelled instead), in case it is the emergency service calling back.

To check certificates against the STI authorities, paste their certificates under **STI certificate authorities**; without them, BitVoice checks that the call was signed with the certificate it names but not who issued that certificate.

## Signing your own calls
Most providers sign their customers' calls themselves. If yours lets you sign (or you are a provider running BitVoice), switch on **Sign caller ID** on the trunk ([Trunks](#/trunks)) and give it your **STI certificate** (from an STI certification authority, with your service provider code token) and its **private key** (P-256). Every call out through the trunk then carries a signed Identity header (ES256, as SHAKEN requires).

- **Certificate address (x5u)** is where the called side fetches the certificate. Leave it empty and BitVoice publishes it at \`/stir/<trunk>.pem\` on its public address (Settings), which must then be reachable from the internet over HTTPS.
- **Attestation**: automatic gives **A** when an extension calls with one of this system's numbers (a number routed in, or a caller ID set on the trunk or the extension), **B** when an extension calls with any other number, and **C** for calls passed on from outside (a forwarded caller). Or fix it at A, B or C.
- Call history marks each signed call with **signed A**, **B** or **C**. A call is never stopped because it could not be signed; the reason is in the log.`,
  },
  {
    id: 'consent', title: 'Recording consent', route: '/settings', keys: 'recording consent notice announce beep two party all party law gdpr california this call is recorded',
    body: `Some places let a call be recorded when one person on it knows; others need everyone to be told, or to agree. In Settings, **Call recording**, choose how people on a recorded call are told:
- **No notice**.
- **Announce**: as the call is answered, both sides hear the notice (record it in the same place, for example "This call is recorded for training and quality"), and recording starts only after it has played. Until one is recorded, three beeps are played, and Health says so.
- **Beep**: a short tone to both sides every 15 seconds while the call is recorded.

::figure recording

**Rules** pick by the outside number (the caller on calls in, the number dialled on calls out), longest match first: for example numbers starting 1415 (California) **Announce**, 49 (Germany) **Never record**, everything else as above. A rule's **No notice** records without one; **Never record** leaves those calls out of recording altogether. Calls between extensions follow the main setting.

Recording can also be paused during a call with the pause code (card payments). None of this is legal advice: check the laws where you and your callers are.`,
  },
  {
    id: 'hotdesk', title: 'Hot desking', route: '/phones', keys: 'hot desk hot desking shared desk phone sign in sign out hoteling hotelling flexible',
    body: `A hot desk is a shared phone that becomes whoever signs in at it: for offices where people do not have fixed desks. Make one by switching on **Hot desk** in the extension the phone is set up as (the desk's own extension, such as 900 Desk 3).

## Signing in and out
1. At the phone, dial \`*33\` and wait for the tone.
2. Enter your extension and #, and wait for the tone.
3. Enter your voicemail PIN and #. A rising tone means you are signed in; a falling tone means it was refused.

From then on, calls to your extension ring this desk as well as your other phones, and calls you make from it show your number and name and count as yours on Costs. Signing in at another desk moves you there. \`*34\` signs you out. Five wrong tries lock the desk for ten minutes.

> Emergency calls from a hot desk always carry the desk's own site, floor and room, not your usual ones: responders go to where the phone is.

## Managing hot desks
On [Phones](#/phones), **Hot desks** shows who is signed in where; administrators can sign someone in (without their PIN) or out. The codes, and a time each day when everyone is signed out, are in Settings. Sign-ins are kept across restarts.`,
  },
  {
    id: 'contacts', title: 'Contacts on every phone', route: '/contacts', keys: 'contacts directory phonebook address book csv import export yealink polycom grandstream snom caller name',
    body: `**Contacts** is the company directory: the suppliers, clients and other offices people call. Every phone lists it beside the extensions, and when one of these numbers rings in, phones and the caller card show the contact's name (when the provider sent none). A number matches however it is written: +1 212 555 0100 and 2125550100 are the same line.

::figure contacts

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

::figure costs

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

::figure quality

Each place shows its calls, its average score (MOS, out of 5: above 4 is good, under 3.6 sounds poor), the share of sound packets that never arrived, and jitter (uneven delay). Below that, BitVoice says what is wrong in plain words, for example "Packet loss on Head office's network: 3.2% of the sound never arrived, mostly between 14:00 and 16:00", with what to check. The bar chart shows the sound lost by hour of the day.

If every place loses sound at once, the cause is what they share: the internet link or the provider. Places with fewer than three calls are not judged. Per-call quality is also in [Call history](#/history) (the Quality column) and, while calls are live, on [Live calls](#/calls).`,
  },
  {
    id: 'callbacks', title: 'Callbacks', route: '/callbacks', keys: 'call back ring back request receptionist',
    body: `People who asked to be rung back. An AI receptionist books one when a caller asks: it collects their name, the number to call (offering the one they are calling from), a good time and what it is about, reads them back, and books it only when the caller agrees. It never promises an exact time. Administrators can also add callbacks by hand.

::figure callbacks

A new callback shows an alert on every open screen, and is emailed to the owner of the receptionist's **Take messages for** mailbox when that extension has an email address and email is set up in Settings.

To ring back, choose your extension and press **Ring back**: your phone rings first, and when you pick up BitVoice calls the number for you, with your normal routes and caller ID. Then add a note if you like and press **Done** (or **Cancel it**). Finished callbacks are kept below the open ones and can be opened again.`,
  },
  {
    id: 'holds', title: 'Legal holds', route: '/holds', keys: 'retention litigation ediscovery keep preserve',
    body: `A legal hold keeps call records, recordings and voicemail that a matter may need, and stops anyone deleting them, until it is released. Use it when a lawyer asks for records to be preserved.

::figure holds

## Adding a hold
On [Legal holds](#/holds), press **Add**:
| Field | What it is |
|---|---|
| Name | what the matter is called |
| Reason | why, for the record |
| Numbers | the extensions and outside numbers it covers (\`101, +15551234567\`), or **Everything** |
| From, To | the dates it covers; empty for no limit |

## While a hold is on
- Retention leaves what it covers alone, whatever its age.
- Nobody can delete a held call, recording or voicemail, administrators included.
- Held calls show a lock in [Call history](#/history).

## Releasing
**Release** makes what the hold kept subject to the retention settings again: things older than the retention are deleted at the next clean-up. Released holds stay listed, with when and by whom, and adding and releasing holds is in the audit log.`,
  },
  {
    id: 'portal', title: 'The staff portal', keys: 'chat presence message colleague team room status staff portal self service my phone forwarding do not disturb follow me voicemail greeting pin ucp user control panel',
    body: `The staff portal lets everyone look after their own calls without an administrator: do not disturb, forwarding, follow-me, speed dials, voicemail and its greeting, their PIN, and a list of their own calls. It is at \`#/me\` on this server's address, and linked from the sign-in page.

::figure portal

## Signing in
People sign in with their **extension number** and their **voicemail PIN**: the same PIN that opens their voicemail from a phone and signs them in at hot desks. An extension without a PIN cannot sign in; an administrator sets one on the extension.

Repeated wrong tries from one address lock it out for ten minutes. A portal session reaches only the person's own settings: never another extension, the configuration or anyone's recordings.

## What people can do
| Tab | What is there |
|---|---|
| Where calls go | do not disturb; forwarding of every call, when busy and when not answered; follow-me numbers; own speed dials |
| Voicemail | messages to play and delete, with transcripts and summaries when AI is on; record or upload the greeting |
| Your calls | the person's own calls, newest first, without recordings |
| PIN | change the PIN, giving the current one |

Forwarding and do not disturb set here are the same as those set from the phone with feature codes ([Feature codes](#codes)): whichever was set last is in force. Follow-me numbers and speed dials are part of the extension's configuration, so changing them makes a revision on [Changes](#/changes), recorded as the person's own.

> Forwarding to an outside number works only for extensions allowed to call outside numbers. The portal says so rather than saving a forward that could never ring.

## Chat and presence
The portal's **Chat** tab lists colleagues with how they are right now: **available**, **on a call**, **ringing**, **away** (do not disturb, a meeting or time off from their calendar, with the reason) or **offline** (no phone connected), and the status they typed ("At lunch, back at 2"). Choose someone to message them; messages arrive at once, and a number beside a name counts the unread ones. **Team rooms** (Settings, Staff chat) are conversations for a group, or for everyone when a room lists no members.

Messages are only ever seen by the people in the conversation: administrators manage the rooms, never read them. Settings, **Staff chat** switches chat off, and sets how long messages are kept (0 keeps the newest thousand of each conversation).`,
  },
  {
    id: 'conferences', title: 'Conference rooms', route: '/conferences', keys: 'conference meeting bridge confbridge meetme dial-in pin leader moderator mute lock room record',
    body: `A conference room is a number people dial to all talk together. Staff dial it from their desks; outside callers reach it through an incoming route or a menu option that points at the room.

::figure conference

## Setting up a room
1. Open [Conference rooms](#/conferences) and press **Add conference room**.
2. Give it a **number** (any free number, 500 by default) and a **name**.
3. Optionally set a **PIN**. Callers hear a tone, enter the PIN and press #. Three wrong PINs and the call ends.
4. Optionally set a **leader PIN**, different from the PIN. Whoever enters it joins as a leader.
5. To let outside callers in, point an incoming route (or a menu key) at the room.

## Choices
| Choice | What it does |
|---|---|
| Wait for the leader | everyone else hears the hold tone until a leader joins; when the last leader leaves, the room closes and everyone is hung up. Needs a leader PIN. |
| Members join muted | for briefings and lectures: leaders talk, members listen. A member presses 1 to ask to speak, and a leader unmutes them. |
| Record the room | the whole room is kept as one recording in [Call history](#/history), unless recording is off for the whole system |
| Tones when people join and leave | on by default |
| Most people at once | 50 by default, up to 200 |

## Keys during a call
- \`1\` mutes and unmutes yourself (in a room where members are muted, it asks to speak).
- \`2\`, for a leader, locks the room so nobody new can join, and unlocks it.

## While a room is in use
The page shows each room in use: who is in, who is talking (the green light), who is muted, who has asked to speak, and who is still waiting for the leader. An administrator can mute or unmute anyone, remove someone, and lock or unlock the room.

Everyone in a room hears everyone else who is not muted, mixed by BitVoice; each person's phone can use its own codec. A call record is kept for each person's call, and one for the room itself with its length and recording.

> Conference sound is mixed at telephone quality (8 kHz), whatever codecs the phones use: it is what every phone can hear, and it keeps a room of fifty cheap to run.

## HD audio
Switch on **HD audio** in a room (new rooms have it on) and it is mixed at 16 kHz instead of 8: people on wideband phones (G.722, Opus, the browser phone) hear each other in HD, with the crispness of a modern call, while people on ordinary phones (G.711, mobiles through a provider) hear the same room as usual. The live view marks the room and each member who is in HD. HD needs G.722 among the codecs the phones use (it is first in the system's list, Settings, Codecs). Recordings of the room stay in telephone quality.`,
  },
  {
    id: 'codes', title: 'Feature codes, forwarding and speed dials', route: '/codes', keys: 'feature codes star codes forward call forwarding do not disturb dnd follow me find me pickup intercom speed dial',
    body: `Feature codes are what people dial on their phones to change their own settings and reach features. They work on every phone, desk phone or softphone, with nothing set up on the phone itself. A rising tone means done; a falling tone means refused.

## The codes at a glance
| Code | What it does |
|---|---|
| \`*72\` | forward every call (then the number and #, or the number straight after: \`*722125550199\`) |
| \`*73\` | stop forwarding every call |
| \`*90\` / \`*91\` | forward when on a call / stop |
| \`*52\` / \`*53\` | forward when not answered / stop |
| \`*78\` / \`*79\` | do not disturb on / off |
| \`*76\` | switch do not disturb (rising tone: now on) |
| \`*21\` | switch follow-me on and off |
| \`*8\` | answer a call ringing in your pickup group |
| \`**\` and an extension | answer the call ringing that extension |
| \`*80\` and an extension | intercom: that phone answers on its speaker |
| \`*0\` and a code | a speed dial |
| \`*62\` | wake-up call: hear it, book one (four digits, such as 0630) or cancel it (star) |
| \`*68\` and a digit | room status, for housekeeping in hotels |
| \`*43\` | the echo test |
| \`*97\` | your voicemail |
| \`70\` | park a call (transfer it here); 71 to 79 pick it up |
| \`*45\` / \`*46\` | queue agents: log in or out / pause or resume |
| \`*33\` / \`*34\` | sign in / out at a hot desk |
| \`*7\` / \`*8\` during a call | pause and resume the recording (card payments) |

## Forwarding
::figure forwarding

- \`*72\` forwards every call. Dial it, hear a tone, then dial the number and #. Or dial the number straight after the code.
- \`*90\` forwards calls that come while you are on a call. \`*52\` forwards calls you do not answer within your ring time.
- \`*73\`, \`*91\` and \`*53\` cancel them.

Forwarding to another extension uses that extension's own settings (its voicemail, its follow-me), up to four hops so two people forwarding to each other cannot loop. Forwarding to an outside number goes through the outgoing routes, as if the person dialled it, and needs the extension to be allowed outside calls.

Forwarding is live state, not configuration: setting it makes no revision, and it is kept across restarts. [Extensions](#/extensions) and the staff portal show and change it too.

## Do not disturb
\`*78\` on, \`*79\` off, \`*76\` switches. With do not disturb on, calls go to voicemail, or to the extension's no-answer destination. Do not disturb set by hand wins over the calendar ([Calendars](#calendar)). Emergency call-backs still ring.

## Follow-me
::figure followme

\`*21\` switches follow-me on and off, for extensions that have follow-me numbers set (on the extension, or in the staff portal). The desk rings first for its ring time, then the follow-me numbers, one after another or all together. On an outside number with **press 1** on, the person hears a tone and presses 1 to take the call; a voicemail or a silence never does, and after 15 seconds the call moves on.

## Answering someone else's call
::figure pickup

- \`*8\` answers the oldest call ringing anyone in your **pickup group** (set on each extension; names are compared without regard to capitals).
- \`**\` and an extension answers a call ringing that extension: \`**101\`.

## Intercom
::figure intercom

\`*80\` and an extension calls it with auto-answer: its phone answers on the speaker, and both sides talk. Yealink, Grandstream and Snom read BitVoice's Call-Info header, Poly reads Alert-Info; some phones need intercom allowed in their own settings. An extension can refuse intercom; then the call rings as usual. Door stations never take intercom.

## Speed dials
\`*0\` and a code dials a saved number: \`*05\`. The company's speed dials are on this page; each person can have their own on their extension or in the staff portal, and theirs win over the company's with the same code. Codes are 1 to 3 digits.

## Changing the codes
Every code can be changed on [Feature codes](#/codes), or switched off with the word \`off\`. Two features cannot share a code, and no code may be an extension's or a group's number: BitVoice says which one clashes. The echo, voicemail and park numbers are in [Settings](#/settings).`,
  },
  {
    id: 'tenants', title: 'Hosting other organisations', route: '/tenants', keys: 'tenants organisations organizations multi-tenant hosted hosting reseller provider domain',
    body: `One BitVoice can host several organisations. Each one is a phone system of its own: its own extensions, numbers, trunks, routes, administrators, voicemail, recordings and call history. No organisation can see another's. They share only this server's SIP port, IAX2 port and media ports.

::figure tenants

## When to use it
- A provider, or an IT company, running phone systems for many small customers on one server.
- A group of offices or clinics that want separate systems, administered separately, on shared hardware.
- Several answering points sharing one system, each keeping its own call-takers and records.

The main system is an organisation too: the one this server was set up as. Its administrators manage the others.

## Adding an organisation
1. Open [Organisations](#/tenants) and press **Add organisation**.
2. Give its **name** and a **short name** (lowercase letters, digits and dashes). The short name is part of its web address, \`/t/acme/\`, and of its phones' sign-ins, \`acme.100\`. It cannot be changed later.
3. Optionally give its **own domain**, such as \`pbx.acme.example\`, pointed at this server. Phones register to that domain, and its administrators open it in a browser.
4. Set its **first administrator**: a username and a password of at least 12 characters, and its country.
5. Optionally set **limits**.

The organisation starts at once with an empty phone system, ready for its administrator to set up as described in [Getting started](#start).

## How calls find the right organisation
| A request | goes to |
|---|---|
| from a phone registering to \`pbx.acme.example\` | the organisation with that domain |
| from a phone signing in as \`acme.100\` | Acme, extension 100 |
| from a provider, for a number | the organisation whose trunk it comes from and whose incoming route has that number |
| anything else | the main system |

So two organisations can both have extension 100, and several can share one provider account with different numbers.

## Where its data is
On SQLite, each organisation has its own folder under \`tenants/\` in the data folder, with its own database file. On PostgreSQL, each has its own schema in the same database (\`tenant_acme\`). Backups, archives and exports are per organisation.

## Limits
| Limit | What happens at it |
|---|---|
| Extensions | adding one more is refused with a message |
| Calls at once | new calls are refused (503) until one ends |
| Storage | new recordings and voicemail are not kept |
| AI requests a day | transcripts, summaries and the AI receptionist stop until midnight UTC |

0 is no limit. Emergency calls are never refused: an organisation at its call limit, or suspended, can still call 911 and be called back.

## Opening an organisation
**Open** signs you in to its web interface as the provider, without its own account; it shows in its audit log as the provider. Its administrators sign in at its own address with their own accounts. Each organisation has its own sign-in cookie, so being signed in to one never signs anyone in to another.

Organisations never see this server's own screens: updates, the database, the other servers, the certificate, the SIP trace (which shows every organisation's messages), the call generator or this list.

## Suspending and removing
**Suspended** stops sign-ins and every call except emergency calls; nothing is deleted. **Remove** stops the organisation and moves its folder aside (\`tenants/.removed-<name>-<time>\`), so a mistake can be undone by hand.`,
  },
  {
    id: 'paging', title: 'Paging and overhead speakers', route: '/paging', keys: 'intercom announce overhead multicast speaker',
    body: `Paging speaks through many phones at once: dial a paging number, and every phone in the group answers on its speaker by itself. Use it for announcements to a warehouse, a school, a shop floor.

## Setting up a paging group
1. On [Paging](#/paging) press **Add paging group** and give it a number (800 by default) and a name.
2. Choose the phones. Overhead speakers set up as extensions (kind **Overhead speaker**) can be members too.
3. Optionally, a **multicast** address and port.

## Multicast
**Multicast** also sends the page to a multicast address and port (\`239.0.1.10:5000\`), as G.711 µ-law, which overhead speakers and many desk phones can listen to without a call each. A group may have only a multicast address. It works on one network: routers do not pass multicast on unless they are set up to.

## Paging and intercom
| | Paging | Intercom |
|---|---|---|
| Who hears | everyone in the group | one extension |
| Talk back | no, one way | yes, both ways |
| How | dial the group's number | \`*80\` and the extension |

Phones that do not answer a page by themselves within a few seconds are left out, so one phone without auto-answer never holds a page up. Some phones need auto-answer (intercom) allowed in their own settings.`,
  },
  {
    id: 'emergency', title: 'Emergency calls', keys: '911 933 112 999 karis law ray baums location notify',
    body: `Emergency numbers always go out, from every extension, even ones that cannot call outside numbers. This chapter is about emergency calls made from the phones BitVoice runs: an office calling 911. Answering 911 calls is the [911 profile](#psap).

::figure office-911

## What BitVoice does on every emergency call
1. **Routes it**, always: a route that names the number first; with none, every working trunk in turn. Limits never stop it: an extension not allowed outside calls, a hosted organisation at its call limit or suspended, a trunk at its channel limit.
2. **Sends where the caller is**: with **Send the caller's address** on (Settings), the call carries the site's civic address, floor and room to the provider as PIDF-LO.
3. **Tells people on site** (Kari's Law in the US): the extensions under **Notify** get a message on their phones, every open BitVoice screen shows an alert, and the addresses under **Also email** get an email with the caller's name, extension, site, floor and room, so someone can meet the responders at the door.
4. **Lets the call back through**: for an hour after an emergency call, calls coming in are never refused, so the answering point can always ring back.

## Setting it up
1. Check the emergency numbers in [Settings](#/settings) (set by country: 911 and 933 in the US).
2. Add every building under [Sites](#/sites), with its street address and the networks its phones use.
3. Give every extension its site, floor and room.
4. Make sure an outgoing route and trunk carry emergency calls; [Health](#/health) warns when none can.
5. Fill in **Notify** and **Also email**.
6. Dial \`933\` (in the US) to hear the address your provider has.

## Remote workers
A phone registering from outside its site's networks is a remote worker: their phone page asks where they are, and that address is used instead. Alerts say when it was not confirmed.

## Emergency mode
A different thing: one switch that sends every incoming call to one place (an evacuation, a closure, an incident). Set where calls go, an optional code such as \`*55\` and the extensions allowed to dial it in [Settings](#/settings); switch it on the [Dashboard](#/), or by dialling the code (rising tones for on, a low tone for off). A red bar shows on every screen while it is on.`,
  },
  {
    id: 'security', title: 'Security', route: '/security', keys: 'ban fraud audit password two-step totp tls',
    body: `A phone system on the internet is attacked within hours: scanners try passwords on every SIP port, and a broken-into system runs up calls to premium numbers. BitVoice is built to stand up to that by default, in layers.

::figure security

## Against password guessing
- Addresses that keep failing to sign in are banned for a while (Settings, Protection): by default ten failures in a minute ban the address for an hour. Banned addresses are listed on [Security](#/security) and can be unbanned.
- **Trusted networks** are never banned: your office network.
- SIP passwords are generated, at least 12 characters, one per extension. A phone signing in as one extension can never register another.
- A scanner that is banned gets no answer at all, so it learns nothing.

## Against fraud
- Extensions can call outside numbers only when allowed.
- Premium-rate numbers are refused unless a route names them.
- [Costs](#/costs) alerts on budgets and on runaway spend: many calls to one country in an hour.
- Each extension may have at most four calls at once (Settings, Protection).

## Encryption
- **TLS** encrypts signalling (port 5061), so passwords and numbers are not readable on the network.
- **SRTP** encrypts the sound of calls; the browser phone and apps always use it.
- The web interface has HTTPS on port 8443 with its own certificate, or one you install.

## Who can do what
- Administrators change everything; viewers see everything but change nothing; staff use the portal for their own calls only.
- Turn on **two-step sign-in** for every administrator ([Administrators](#/users)): a code from an authenticator app.
- [Single sign-on](#sso) signs people in through your identity provider.
- The **audit log** records every administrative action and who did it, and cannot be edited.`,
  },
  {
    id: 'sso', title: 'Single sign-on', keys: 'oidc entra azure google okta keycloak login provider groups',
    body: `Single sign-on lets people sign in to BitVoice with their work account, through any OpenID Connect provider: Microsoft Entra ID, Google, Okta, Keycloak and others. Leaving the organisation then removes their access here too.

::figure sso

## Setting it up
In [Settings](#/settings), **Single sign-on**:
1. Register an application with the provider, with the **Redirect address** shown here.
2. **Fill in for a provider** sets the **Issuer** for the common ones; copy the **Client ID** and **Client secret** from the provider.
3. Name the provider's **Administrator groups** and **Viewer groups**. **Groups claim** is where the provider puts them (\`groups\` unless set).
4. Optionally change the **Button text** on the sign-in page.
5. Switch on **Use single sign-on**.

## How it works
- The account here is made at the first sign-in; the groups set the role at every sign-in, so moving someone between groups at the provider changes their role here.
- Someone in neither kind of group is refused, and the refusal is in the audit log.
- With **Also allow passwords** off, passwords work only on this computer, so a provider outage never locks you out.

> Two-step sign-in is then the provider's job: switch it on there.`,
  },
  {
    id: 'compat', title: 'Asterisk compatibility (AMI and ARI)', keys: 'ami ari asterisk manager rest crm wallboard click to dial originate',
    body: `Tools written for Asterisk can control BitVoice: CRMs, wallboards, click-to-dial buttons, call recorders' controllers. Switch it on in [Settings](#/settings), **Asterisk compatibility**. Tools see calls as channels named like \`PJSIP/100-...\`, as on Asterisk.

::figure compat

## AMI
The manager interface on TCP port 5038 (**AMI listens on**: this computer only by default). Supported:
| Kind | Names |
|---|---|
| Actions | Login (plain or MD5 challenge), Ping, Events, CoreShowChannels, Status, PJSIPShowEndpoints, ExtensionState, QueueStatus, Originate, Redirect, Hangup, Logoff |
| Events | Newchannel, Newstate, Hangup |

## ARI
At \`/ari/\`, with basic authentication or \`?api_key=user:secret\`:
| Request | What it does |
|---|---|
| \`GET /ari/channels\` | the calls in progress |
| \`POST /ari/channels?endpoint=PJSIP/100&extension=5551234\` | ring 100, then call the number |
| \`DELETE /ari/channels/{id}\` | hang up |
| \`POST /ari/channels/{id}/redirect?endpoint=PJSIP/102\` | send the call to 102 |
| \`/ari/endpoints\`, \`/ari/bridges\`, \`/ari/asterisk/info\` | phones, bridges, version |
| \`/ari/events\` | a WebSocket of events |

Stasis media applications are not offered: use [Scripts](#scripts) for call logic.

## Logins
Each login has a **Username** and **Secret**. Logins are **read only** unless marked **Can change calls**. Keep AMI on this computer unless the tool runs elsewhere: AMI is plain, unencrypted TCP.`,
  },
  {
    id: 'codecs', title: 'Codecs and sound', keys: 'g722 hd voice g711 pcmu pcma opus g729 g726 g726-24 g726-40 adpcm amr amr-wb g.722.2 volte mobile codec noise clean',
    body: `A codec is how sound is turned into data on a call. Phones, browsers and carriers each support some; BitVoice converts between any two of its codecs when two parties differ, so everyone can talk to everyone. When both use the same codec the sound passes through untouched.

::figure codecs

## The codecs
| Codec | Sound | Bandwidth | Used by |
|---|---|---|---|
| G.722 | HD (wideband) | 64 kbit/s | most desk phones: the best choice between them |
| G.711 µ-law | telephone | 64 kbit/s | North America and Japan; every carrier |
| G.711 A-law | telephone | 64 kbit/s | the rest of the world |
| Opus | HD, copes with loss | 24 kbit/s | browsers and the mobile apps |
| G.729 | telephone, a little rougher | 8 kbit/s | thin links, some carriers |
| G.726 (40 kbit/s) | telephone; carries modem tones best of the G.726 rates | 40 kbit/s | gateways, some DECT systems |
| G.726 (32 kbit/s) | near telephone | 32 kbit/s | DECT bases, older gateways |
| G.726 (24 kbit/s) | between 32 and 16 | 24 kbit/s | some DECT systems |
| G.726 (16 kbit/s) | rough | 16 kbit/s | very thin links |
| AMR-WB (G.722.2) | HD | 6.6 to 23.85 kbit/s | mobile networks' HD voice, VoLTE interconnects |
| AMR | telephone, mobile | 4.75 to 12.2 kbit/s | mobile networks, some carriers and gateways |
| GSM | rougher | 13 kbit/s | older softphones and gateways |

Settings lists the codecs phones may use, best first; each trunk may have its own list. Analog adapter ports always use G.711, which faxes, modems and alarm panels need.

> BitVoice's G.726 is bit-exact with the ITU-T's own reference implementation at all four rates (ffmpeg's differs at 24 and 40 kbit/s, so equipment built on ffmpeg may sound slightly different there). G.726 comes in two packings: RFC 3551's (G726-32) and the older AAL2 order. AMR and AMR-WB follow RFC 4867: BitVoice offers octet-aligned framing, answers in whichever framing the other side asked for, and sends the highest mode the other side's mode-set allows (12.2 and 23.85 kbit/s when it gives none). Most equipment uses the first; choose **G.726 (AAL2 order)** only for equipment that sounds garbled with it.

## What converting costs
Converting takes processor time, only on calls where the two sides differ. Per call, about 0.3 ms of one core every 20 ms for Opus and 0.12 ms for G.729 (some 60 and 170 converted calls per core); G.726 and GSM cost less than G.729; G.711 and G.722 almost nothing. G.729 is sent without Annex B (no silence frames).

## Sound clean-up
**Sound clean-up** (Settings) turns down steady background noise and evens out levels, for voicemail and, if you like, during calls.`,
  },
  {
    id: 'automation', title: 'Configuration as code', keys: 'api token terraform opentofu openapi git gitops automation script ci',
    body: `Everything the screens do goes through BitVoice's REST API, so anything can be scripted, kept in Git, or managed with Terraform. The API is described for tools at [/api/openapi.json](/api/openapi.json).

::figure automation

## API tokens
Made on [Administrators](#/users): one per program, read only or able to change everything. Send it as \`Authorization: Bearer bv_...\`. It is shown once; revoke it when no longer needed. A token's changes show in Changes and the audit log under its name.

\`\`\`
curl -H "Authorization: Bearer bv_..." https://pbx.example.com/api/extensions
\`\`\`

## Terraform or OpenTofu
The repository's \`deploy/terraform\` shows extensions, ring groups and routes managed with the generic REST provider: plan shows what would change, apply makes the change.

## Configuration from Git
In [Settings](#/settings), **Configuration from Git**, BitVoice reads the configuration from a file in a repository every few minutes and either shows what differs or makes the changes. Start the file with **Export for Git**, which leaves out passwords; BitVoice keeps its own, so secrets never go into the repository.

> With BitVoice making the changes, a change made in the screens is undone at the next read if the file says otherwise: choose one place to make changes.`,
  },
  {
    id: 'updates', title: 'Updates', route: '/updates', keys: 'upgrade version install rollback release signed',
    body: `Updating BitVoice is one click, and safe: it checks the new version, installs it beside the old one, and goes back on its own if the new one does not stay up.

::figure updates

## Installing an update
1. Download the new release and unpack it. Beside the program is its signature from BioDream: \`bitvoice.sig\`, or \`bitvoice.exe.sig\` on Windows.
2. On [Updates](#/updates), upload the program and its \`.sig\` file.
3. Press **Install**. By default BitVoice waits until no calls are in progress; you can also install at once.
4. BitVoice restarts on the new version. With several servers, calls in progress are handed to another server and carry on.

## Going back
The version you left is kept: **Go back** returns to it in one click. A new version that fails to start twice, or does not stay up for a minute, is replaced by the previous one by itself.

## Signatures
Every release from v0.1.3 carries BioDream's release key. Such a build installs only programs signed with that key: an unsigned file, one signed by anyone else, or one changed by a single byte is refused with **the signature does not match**. The page says whether this build checks (**Required**) or not.

| You run | Updates accepts |
|---|---|
| a release, v0.1.3 or later | only programs signed by BioDream |
| v0.1.2 or earlier, or a build of your own | any program: with no key, a .sig file cannot be checked and is ignored |

> Install v0.1.3 or later by hand once (download, replace the program, start it): from then on the Updates page checks every update.`,
  },
  {
    id: 'loadtest', title: 'Call generator', route: '/loadtest', keys: 'load test stress regression capacity',
    body: `The call generator places real calls with real audio into BitVoice from this computer: to see how many calls a server carries and how well, or to check after a change that calls still go where they should.

::figure loadtest

## Running a test
| Setting | What it is |
|---|---|
| Number to call | a number inside BitVoice: an extension, a group, a queue, a menu |
| Calls come from | the caller number the calls show |
| Answer as | an extension the generator answers for itself; or nobody, when the number answers by itself (a menu, the echo test) |
| Calls | how many in all |
| At once | the most in progress together |
| New per second | how fast they start |
| Each lasts (s) | how long each talks |

## Reading the results
| Result | What it says |
|---|---|
| Answered, Failed | and **Why calls failed**, by reason |
| Answer time, median and 95% | how long callers waited |
| Audio lost | the share of sound packets that never arrived |
| Audio score | an estimate of the sound (MOS): 4 and over is good, under 3.6 is poor |
| Calls in progress over time | a chart of the load |

> It only calls numbers inside BitVoice, never trunks or emergency numbers, so a test can never cost money or reach 911. It is for this server's administrators only.`,
  },
  {
    id: 'changes', title: 'Changes, backups and import', route: '/changes', keys: 'revision undo restore export import backup freepbx asterisk',
    body: `Every saved change is a revision: numbered, with who made it, when, and a one-line summary. Nothing in the configuration is ever lost.

::figure changes

## Comparing and restoring
- **What changed** shows the difference between a revision and the one before, field by field.
- **Restore** shows what restoring would change and asks first. Restoring is itself a new revision, so it can be undone too.

## Export and import
- **Export** downloads the whole configuration as one file (secrets left out unless you ask for them).
- **Import** previews a file's changes before applying them.
- [Import](#/import) also reads FreePBX and Asterisk configurations, 3CX user lists, and the CSV exports of Cisco Unified CM, Avaya and Mitel.
- [Archive](#/archive) copies backups, recordings and voicemail to Amazon S3 or Azure.

## Changes made at the same time
Two administrators editing the same thing at once cannot overwrite each other: the second save is refused with a message to reload, and nothing is lost.`,
  },
  {
    id: 'ai', title: 'AI receptionist, voicemail triage and asking', route: '/receptionists', keys: 'ai assistant receptionist triage urgent ask plain words model speech',
    body: `AI features use the AI service set in [Settings](#/settings): an OpenAI-compatible service (OpenRouter by default) or one you run yourself. What they need is sent there, so choose a service you trust with calls; for a 911 centre, its own government cloud counts as local.

::figure receptionist

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
    body: `::figure call-path

## Try a call
Enter who calls (an extension, or an outside number), what they dial and when, and press **Try it**. BitVoice follows the call exactly as it would for real, without ringing anything: the incoming route, schedules at that time, routing scripts (they run, in their sandbox), menus, groups and queues, and what happens if nobody answers. The path this call takes is marked; other ways on (a menu's keys, a schedule's other side) can be opened. Green means it works, amber means look at it (a phone not connected, a script falling back), red means the call fails there. Tick **Assume every phone is connected** to see the plan as designed rather than as it is right now; **Arrives on** picks the trunk for routes that only take one.

## The diagram
The diagram shows where calls from outside go, box by box: each incoming route, then the menus, groups, queues, schedules, scripts and people it leads to.

Click a box (or Tab to it and press Enter) to change where calls go from it: a route's destination, an extension's or group's **If nobody answers**, a schedule's open and closed destinations, a menu's keys (add or remove keys right there), a script's fallback or a receptionist's person. Saving checks the change like any other and makes a revision. **Open its full editor** goes to the item's own page. **Add incoming route** adds a route without leaving the diagram.`,
  },
  {
    id: 'sites', title: 'Sites and office 911', route: '/sites', keys: 'site building address 911 emergency location remote worker floor room network e911 karis ray baum',
    body: `A site is a building: its street address and the networks its phones use. Sites are how an emergency call says where the caller is, down to the floor and room. In the US, Kari's Law and RAY BAUM'S Act require exactly this from business phone systems.

::figure sites

## Adding a site
On [Sites](#/sites), press **Add**:
| Field | What it is |
|---|---|
| Name | what people call it: Head office |
| Street, City, State or province, Postal code, Country | the civic address responders are sent to |
| Networks | the address ranges its phones use, such as \`10.1.0.0/16\`; one per line |

Then on each extension give its site, **floor** and **room**. [Health](#/health) lists extensions without a location.

## How a phone's place is found
1. A phone registering from one of a site's networks is at that site, with its extension's floor and room.
2. A hot desk carries the desk's own site, floor and room, not the person signed in ([Hot desking](#hotdesk)).
3. A phone registering from outside every site's networks is a **remote worker**. On its first emergency-capable registration from a new place, its user is asked to confirm their address on their phone page; emergency calls then carry that address.
4. If a remote worker never confirmed, the call still goes out, with the extension's usual site, and alerts say the address was not confirmed.

## On-site alerts
With **Email when someone calls 911** set in Settings, the people named there get the caller's name, extension, site, floor and room as soon as a call to an emergency number starts, so someone can meet the responders at the door. **Notify** extensions get a message on their phones, and every open BitVoice screen shows an alert. See [Emergency calls](#emergency).

> Check each site's address with your provider: dial \`933\` (in the US) from a phone at the site to hear the address the provider has.`,
  },
  {
    id: 'calls', title: 'Live calls', route: '/calls', keys: 'live active current calls hang up transfer listen whisper barge supervise door pause recording',
    body: `Live calls shows every call in progress, updated as it happens: who called whom, how long it has been going, its codecs and its quality.

::figure history

## What administrators can do
| Action | What it does |
|---|---|
| Hang up | ends the call for both sides |
| Open the door | for a call from a door station |
| Pause recording | for a card number: the paused stretch is silent in the recording and marked in the history ([Call recording](#recording)) |
| Listen | hear the call silently |
| Whisper | talk to the agent only; the caller does not hear |
| Barge | join the call; everyone hears you |

To listen, whisper or barge, press **Supervise**, choose the phone to use under **From phone** and press **Ring me**: your phone rings, and when you answer you join the call. You can switch between listening, whispering and barging while you are in it.

## What else shows
- A caller card while an outside call rings (from your CRM, or past calls; see [CRM](#crm)).
- The briefing written for the person a call was transferred to ([Transfer briefings](#briefing)).
- The caller ID verdict: **Verified**, **Partly verified** or **Unverified** ([Spam protection](#spam)).
- With several servers, calls on the others too, read only: hang up or listen on the server that has the call.

When a call ends, it moves to [Call history](#history).`,
  },
  {
    id: 'history', title: 'Call history', route: '/history', keys: 'cdr call records history log export csv recording transcript search said summary action items',
    body: `Every finished call: who called whom, when, for how long and how it ended. Sort the columns, play recordings (a pause for card numbers is marked), and download the list as CSV. How long records are kept is set in Settings, Call recording; a [legal hold](#/holds) keeps them longer.

::figure history

## Transcripts and search
With AI on and **Transcribe recorded calls** switched on (Settings, AI), every recorded call is written out, with a short summary and its action items: what someone agreed or was asked to do. Each side of the call is written out separately from the recording, so the transcript shows who said what, and when. The transcript button next to a recording opens it; administrators can also transcribe a single call, or transcribe one again.

**Search what was said** finds calls by words from their transcripts, summaries and action items. Only recorded calls are transcribed. A transcript is deleted with its recording, and reading or searching transcripts is noted in the audit log, like playing a recording.`,
  },
  {
    id: 'health', title: 'Health', route: '/health', keys: 'health status problems warnings disk certificate trunk registration vault',
    body: `Health says whether the system is ready for calls, and if not, what to do. Each line says what is wrong in plain words and links to the screen where it is fixed. Check it after every change of any size.

## What it checks
| Check | Why it matters |
|---|---|
| Emergency calls can get out | a route and a working trunk carry the emergency numbers |
| Extensions without an emergency location | their 911 calls would carry no floor or room |
| The phone service | SIP is listening, and on which port |
| Phones not connected | extensions nobody has signed in as |
| Trunks | registered, or the reason they are not |
| Menus without a recorded prompt | callers would hear silence |
| The time zone | schedules follow it |
| Recordings and their keeping | disk space, and how long recordings are kept |
| Certificates | when the TLS certificate runs out |
| Key vaults | secrets a vault cannot read |
| Other servers | when several share a database, whether each is running |

Problems are coloured: red stops calls (no emergency route), amber should be looked at (a phone not connected), and grey is for information.

> The emergency checks are always first. A phone system that cannot call 911 is not ready, whatever else works.`,
  },
  {
    id: 'trace', title: 'SIP trace', route: '/trace', keys: 'sip trace debug messages ladder invite register packet capture',
    body: `The SIP trace shows the messages BitVoice sends and receives, live: the first place to look when a phone will not register or a call fails, and what a provider's support desk will ask for.

::figure trace

## Finding a message
BitVoice keeps the last 2,000 messages in memory (nothing is written to disk, and they are gone after a restart). Type part of a number, an address or a Call-ID in the filter. Press **Refresh** for the newest.

Choose a message to see it in full and, beside it, the **call flow**: every message of the same call in order, between the phone, BitVoice and the provider, as in the figure.

## Reading a trace
| You see | It usually means |
|---|---|
| REGISTER answered 401, then nothing | the phone does not have the password, or has the wrong one |
| REGISTER answered 403 | the address is banned, or the phone signs in as another extension |
| INVITE answered 404 | no route matches the number dialled ([Call routing](#routes)) |
| INVITE answered 403 by the provider | the provider refuses the caller ID or the number |
| 486 | the other side is busy |
| 488 | the two sides have no codec in common ([Codecs](#codecs)) |
| 200 OK, then BYE after about 30 seconds | the ACK never arrived: a router in between rewrites or drops messages (switch off its "SIP ALG") |
| A call connects but nobody hears anything | the sound is blocked: check the public address in Settings, and the media ports in the firewall |

> A trace shows phone numbers and sign-in names. Only people signed in to this server see it; hosted organisations never do, since it would show every organisation's messages.`,
  },
  {
    id: 'archive', title: 'Archive', route: '/archive', keys: 'archive backup s3 azure blob offsite copy recordings',
    body: `The archive keeps copies of recordings, voicemail and the configuration somewhere else: an Amazon S3 bucket (or any S3-compatible store) or an Azure Blob Storage container. If this server's disk is lost, the archive still has them.

::figure archive

## Setting it up
1. On [Archive](#/archive), choose **Where to archive**: Amazon S3 or Azure.
2. For S3: the **Bucket**, its **Region**, an optional **Folder in the bucket** and **Endpoint** (for S3-compatible stores), and an **Access key ID** and **Secret access key**. **Copy the policy** gives the only permission BitVoice needs, to attach to the user or role.
3. For Azure: a **Container SAS URL** with write permission, and an optional **Folder in the container**.
4. Choose what to copy besides the configuration: **Call recordings**, **Voicemail**, or both.

## What happens
Every ten minutes BitVoice copies new recordings and messages that are not in the archive yet, and once a day a dated copy of the configuration (\`config/2026-10-13.json\`). The page shows the last run, the last success and any error.

> Keys can be references to a key vault instead of the keys themselves (Settings, Key vaults), so they are never stored in BitVoice.

Deleting something here (by retention or by hand) does not delete the archive's copy: set the bucket's or container's own lifecycle rules for how long the archive keeps things.`,
  },
  {
    id: 'import', title: 'Import', route: '/import', keys: 'import migrate freepbx asterisk 3cx cisco cucm avaya ip office mitel move from replace hunt group hunt pilot line group ring group route pattern bat tar',
    body: `Brings extensions, ring groups, queues and trunks over from FreePBX, Asterisk or 3CX; and people, hunt and ring groups, SIP trunks, incoming numbers and dial patterns from Cisco, Avaya and Mitel. Upload the exported files, several at once (a .zip or .tar of them works too); BitVoice shows exactly what would change before anything is applied, and the import becomes a revision you can undo on [Changes](#/changes).

::figure import

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

::figure video

- BitVoice relays the video as it is, on a port of its own for each phone, so the phones never learn each other's addresses. It never converts video: the two need a codec in common (nearly every video phone has H.264). When they number the same codec differently, BitVoice renumbers it on the way.
- The picture is encrypted (SRTP) to each phone that encrypts its sound.
- A phone's request for a fresh picture (after lost packets) reaches the other phone, so the picture recovers in a moment.
- A camera switched on during a call (a softphone's video button) works too: the other phone is asked first, and the picture starts when it says yes.
- A phone without video, or a call to an outside number, gets sound only. On hold or while parked the picture pauses, and comes back when the call does. After a transfer it carries on only when the new party sends video too.
- Video uses far more of the network than sound (about 0.5 to 2 Mbit/s each way per call): allow for it on slow links and in the media port range.`,
  },
  {
    id: 'psap', title: '911 calls (the 911 profile)', route: '/psap', keys: '911 psap ng911 i3 esinet location pidf-lo held rebid ali spill eido cad nena log answer time call-taker',
    body: `> **Lab and design-partner track.** Not certified for live 911 or radio dispatch, and not yet tried with a live ESInet, P25 system or CAD. Use it in a lab or in shadow mode beside your current equipment. Switch on **Public-safety profiles (lab)** in [Settings](#/settings) to show it in the menu.

BitVoice answers 911 calls as a PSAP's call-handling system: calls from the emergency services network (the ESInet, NENA i3) arrive with the caller's location and the network's data, the call-taker sees them at once, and CAD is told when the call is answered.

::figure psap

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
    body: `> **Lab and design-partner track.** Not certified for live 911 or radio dispatch, and not yet tried with a live ESInet, P25 system or CAD. Use it in a lab or in shadow mode beside your current equipment. Switch on **Public-safety profiles (lab)** in [Settings](#/settings) to show it in the menu.

For a state or regional 911 authority that runs its own NG911 core: BitVoice answers LoST as the ECRF, and routes each 911 call as the ESRP to the PSAP that serves the caller.

::figure core-services

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
    body: `> **Lab and design-partner track.** Not certified for live 911 or radio dispatch, and not yet tried with a live ESInet, P25 system or CAD. Use it in a lab or in shadow mode beside your current equipment. Switch on **Public-safety profiles (lab)** in [Settings](#/settings) to show it in the menu.

Dispatchers talk on radio channels from the same headset as their phone calls. BitVoice reaches the radios through radio-over-IP (RoIP) gateways; the radio network itself stays with whoever runs it.

::figure radio

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

::figure fax

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
    body: `Administrators are the people who sign in to this web interface. Staff who only look after their own calls use the staff portal instead and need no account here ([The staff portal](#portal)).

## Roles
| Role | Can |
|---|---|
| Administrator | change anything, play recordings, see every secret field's existence (never its value) |
| Viewer | see everything, change nothing; secrets are hidden |

A change of role, or of password, signs the person out everywhere at once.

## Two-step sign-in
Each person turns on two-step sign-in for themselves: **Set up** shows a key for an authenticator app; entering the code it shows turns it on. From then on, sign-in asks for a code as well as the password. Turning it off needs the password.

> Turn on two-step sign-in for every administrator of a system reachable from the internet.

## Single sign-on
With single sign-on (Settings), people sign in with the organisation's account (Microsoft Entra, Google, Okta, any OpenID Connect provider), and an account is made for them the first time. See [Single sign-on](#sso).

## API tokens
Programs (Terraform, scripts) use API tokens instead of a password: made on this page, shown once, and revoked at any time. Each token is an administrator or a viewer.`,
  },
  {
    id: 'settings', title: 'Settings', route: '/settings', keys: 'settings system codecs sso ai recording retention mobile push vault gitops sound',
    body: `Settings holds everything about the system as a whole. Each section saves on its own, and every save is a revision on [Changes](#/changes).

| Section | What is there |
|---|---|
| System | name, country, time zone, language, the public address phones and providers see |
| Numbers | the echo test, voicemail and park numbers, and the emergency numbers |
| Codecs | the codecs phones may use, best first ([Codecs](#codecs)) |
| Call recording | off, every call, or selected extensions; consent rules; how long recordings are kept |
| Sound | clean-up for voicemail and during calls |
| Protection | bans after failed sign-ins, trusted networks, calls per extension, premium numbers |
| Emergency | send the caller's address, who is notified, who is emailed, emergency mode |
| AI | the AI service, models, and which AI features are on ([AI](#ai)) |
| Single sign-on | the identity provider ([Single sign-on](#sso)) |
| Asterisk compatibility | AMI and ARI for programs written for Asterisk ([Asterisk compatibility](#compat)) |
| Mobile apps | the push service that wakes the apps |
| Email | the mail server voicemail, faxes and alerts go through |
| Key vaults | secrets kept in AWS Secrets Manager or Azure Key Vault |
| Configuration from Git | keep the configuration in step with a file in a repository |
| Database | SQLite or PostgreSQL ([Database](#database)) |

Passwords and keys are never shown again once saved; leave them empty to keep them. Viewers see Settings with every secret removed.

> The public address matters when BitVoice is behind a router: phones and providers outside send sound to the address it advertises. Leave it empty to work it out per phone.`,
  },
]

export const articleFor = (path: string) => manual.find((a) => a.route === path)
