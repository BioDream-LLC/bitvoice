# The public BitVoice demo

A server anyone can open at `https://demo.bitvoice.org`, sign in to as a visitor, look around every
screen, and call from the browser, with no download and nothing at stake. It is how a FreePBX or 3CX
user sees BitVoice working in two minutes. Live since 17 October 2026 on Lightsail (nano, us-east-1,
static IP 52.7.26.145), with a Let's Encrypt certificate BitVoice got itself.

## What a visitor gets

- **Sign in:** `demo` / `try-bitvoice-demo`, a read-only login: every screen and setting is visible,
  nothing can be changed (the server refuses every change from a viewer).
- **Northwind Dental (demo):** six people, a ring group, a billing queue with agents, a main menu, office
  hours, a phone line (switched off) with its routes, a call script, and a few hundred calls made by the
  built-in call generator, so the reports, wallboard and call quality screens have data. All invented.
- **The browser phone:** extension 150 / `try-the-demo-phone`. Dial:
  - `*43` the echo test (hear yourself);
  - `810` the call script (press 1 or 2);
  - `800` the main menu; `600` the ring group; `700` the queue (nobody answers: invented agents).
- Every night at 04:00 UTC the server starts again from nothing.

## Why it is safe to leave open

- **No outside calls.** The phone line is switched off and points at a made-up carrier; extension 150
  may not call out at all. A visitor cannot reach a real number, so there is no toll fraud. Emergency
  numbers cannot connect either (there is no line); the README and the demo's own name say it is a demo.
- **No SIP from the internet.** Only 443 (the web interface and the browser phone), 80 (Let's Encrypt's
  check) and the calls' audio ports (10000-20000 UDP) are open; 5060 is closed, so nobody registers a
  phone or probes SIP.
- **No real data.** People, numbers and calls are invented; the nightly reset wipes whatever visitors did.
- **The administrator** has a random password kept only on the server (`/root/bitvoice-demo-admin`).
- Failed sign-ins are slowed and banned as on any BitVoice server.

## Running it

1. Cut a release (the demo installs a release, as any user would, checked against its signature).
2. `DOMAIN=demo.bitvoice.org VERSION=v0.1.15 sh deploy/demo/provision.sh` (EMAIL= is optional) creates the Lightsail
   server and static IP and prints the IP.
3. At Namecheap, add an A record `demo` → that IP. Run the same command again: it installs, seeds and
   turns on Let's Encrypt and the nightly reset.
4. Upgrades: run it again with the new `VERSION`.

Cost: Lightsail's smallest Linux plan (check the current price on Lightsail's pricing page). BitVoice
used about 100 MB of memory at 200 calls at once, so the smallest plan is enough for a demo.

## Then

- A **"Try it now"** button at the top of the README and on bitvoice.org, with the sign-in under it
  (as Perfuse and Vaglio have).
- A two-minute screen recording of the tour (sign in, browse, call `810` from the browser) for the site
  and posts. Before recording, check that nothing shows a personal name, the Mac's hostname or a home path.

## Tried

`deploy/demo/seed` was run against a local server: setup, the visitor login (a change refused with 403),
the people, groups, queue, menu, schedule, call script and routes, 20 calls from the generator, and the
browser phone signed in as 150 calling `810` (answered by the script, which hung up after its timeout).
Deployed with `provision.sh` on 17 October 2026 (v0.1.6); upgraded to v0.1.7 on 18 October 2026 by running it again. Checked from outside: a visitor signs in, sees the
extensions, is refused a change (403), and the browser phone's call to `810` connects over the internet.

## The 911 side

The demo also shows the public-safety lab (Settings > Public-safety profiles) working for a CAD.
`bitvoice-demo-911` (deploy/demo/caller), a service on the same server, stands in for an NG911 core:
every ten minutes it sends BitVoice an invented 911 call (urn:service:sos, SDP and a PIDF-LO location;
five made-up incidents in turn in Roanoke, Virginia, where the bitdispatcher demo is; callback numbers 540-555-01xx). Extension 190, a call-taker phone the same
program registers, answers it after eight seconds, unless someone answers ring group 690 first.

Both sides play their lines as tones, one pitch per line, and the AI's speech service on the demo is a
stand-in in the same program (http://127.0.0.1:8179/inference) that tells the pitch and answers with
the line. So the live transcript is made up; everything around it is BitVoice's own: the ring, the
routing trail, the location on the 911 screen and map, call.transcript events, the call record and the
EIDO. A real server writes real speech down with whisper; the demo's nano server has no memory to spare
for it. Never run bitvoice-demo-911 beside a real PSAP: it talks only to 127.0.0.1.

The same program is the demo's radio system: a stand-in P25 home RFSS (127.0.0.1:5192) for the
console's talkgroup "County fire dispatch (demo)" (console number 880). Every few minutes one of five
invented units (Engine 1, Engine 2, Ladder 1, Battalion 1, Ambulance 5) talks on it, as a radio's
transmission arrives over the ISSI: IMBE voice (a voice-like sound, no words) with the unit in each
PTT word; every eighth one has the emergency button pressed. After each, the units report positions
(POST /api/radio/locations with a token kept like the CAD's, /etc/bitvoice-demo/radio-token). So the
console shows who is talking, transmissions are recorded, the map shows the units, and the CAD gets
radio.unit, radio.transmission, radio.emergency and radio.location events. A dispatcher on 880 may
talk on the channel too: the stand-in grants the floor when no unit is on the air.

To send the demo's EIDOs and radio events to a CAD, put its https addresses and secrets in
`~/keys/bitdispatcher-demo-push` (or CAD_ENV=file; KEY=VALUE or key: value lines) before running provision.sh:

```sh
EIDO_URL=https://cad.example/api/ng911/eido
EIDO_TOKEN=...
RADIO_URL=https://cad.example/api/radio/events
RADIO_SECRET=...
```

The CAD calls BitVoice back with an administrator token that names the dispatcher it acts for
(X-BitVoice-On-Behalf-Of). It is made once on the server (/etc/bitvoice-demo/cad-token), put into the
data before each nightly rebuild so it keeps working, and copied by provision.sh to
`~/keys/bitvoice-demo-cad`.

