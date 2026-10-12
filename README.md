# BitVoice

BitVoice is an open-source real-time communications engine, written in Go and shipped as one binary.
Voice, video, real-time text, text and fax arrive as sessions, wait in queues, and are answered by
agents at browser-based positions. Every session is recorded and logged.

Double-click the binary and a browser opens on a setup wizard; a few minutes later a phone rings.

- **PBX profile:** a complete, free phone system for offices and businesses (extensions, trunks, ring
  groups, voicemail to email, auto-attendant, parking, paging, desk-phone provisioning), with an import
  from Asterisk and FreePBX and compatibility with their APIs.
- **911 profile:** NG911 call-handling equipment for public safety answering points: NENA i3 ingress,
  caller location, ALI spill and EIDO output to any CAD.
- **Radio console** and **NG911 Core Services** profiles for dispatch centres and 911 authorities.

The core also serves contact centres, nurse and on-call lines, utility outage lines, and campus and
security dispatch.

The core is built to the 911 reliability bar, whichever profile is running.

Status: the PBX profile is working. The 911, radio console and core-services profiles are built and tested
against stand-ins and real captures, not yet against real PSAP, radio or ESInet equipment, and are not
certified for live 911 or public-safety use.

## What works today

- **Calls:** extensions, ring groups, queues (longest-idle, round-robin, ring-all) with a live wallboard,
  auto-attendant menus, opening-hours schedules, voicemail with a message-waiting lamp, call recording,
  busy lamps, blind and attended transfer, hold, SIP trunks over UDP, TCP or TLS, and 911 with Kari's Law
  notification. Video calls between phones that both send video.
- **Fax:** T.38 between fax gateways and providers, fax numbers BitVoice answers itself (each fax kept as
  a PDF and emailed), and faxes sent from the web interface or by email.
- **Codecs:** G.711, G.722, Opus and G.729, converted between any two, so a browser on Opus can talk to a
  carrier on G.729.
- **911 profile:** NENA i3 calls with PIDF-LO location and additional data, call-taker positions, ALI spill
  and EIDO to CAD, i3 logging, answer-time reports, rebids and transfers between PSAPs.
- **Radio dispatch console:** radio-over-IP gateway channels and P25 talkgroups over the ISSI/CSSI
  (encrypted with AES-256, DES or triple DES), private calls to and from radios, alert tones, patches,
  emergencies, every transmission recorded in a radio log, and radio events to CAD.
- **NG911 Core Services:** an ESRP with policy rules, an ECRF answering LoST from your boundaries, a
  location validation function from your address points and road centrelines, and a border control
  function limiting and marking calls at the ESInet edge.
- **Texts:** business texting through TextMagic, Twilio, SignalWire, Telnyx, Bandwidth, Vonage, Plivo,
  Flowroute, VoIP.ms or your own webhook, several at once.
- **Several servers:** servers sharing a PostgreSQL database work as one phone system: calls carry on
  when a server stops, a shared address moves to a running server, and provider lines can be held by one
  server at a time. A Docker lab in [deploy/lab](deploy/lab) shows it on one computer.
- **Phones:** any SIP phone; automatic set-up for Yealink, Poly, Grandstream and Snom; and a browser phone
  (WebRTC) for anyone without a desk phone.
- **Security:** SRTP and SIP over TLS, fail-to-ban, toll-fraud limits, an audit log, and every change saved
  as a revision you can restore.
- **Moving in:** import extensions and trunks from FreePBX (Bulk Handler CSV) or Asterisk (pjsip.conf,
  sip.conf), and people and extensions from 3CX, Cisco Unified CM, Avaya IP Office, Avaya Communication
  Manager and Mitel MiVoice Business exports.
- **Optional extras:** routing scripts in JavaScript or Lua; voicemail transcripts from an AI model you
  choose; off-site copies to Amazon S3 or Azure Blob Storage.

Everything is set up in the built-in web interface. Run the binary and open the address it prints.

## Building

```sh
make check build     # Go tests and the binary in bin/
make web             # rebuild the web interface (needs Node and pnpm; the built copy is committed)
make e2e             # browser tests, including WebRTC calls in Chromium
```

## Principles

- One Go binary with its own web admin and setup wizard. No Asterisk, FreeSWITCH or FreePBX underneath.
- Everything in the GUI, with JavaScript and Lua scripting for anything custom.
- Go standard library only, with six pinned, pure-Go exceptions: the JavaScript and Lua engines, the
  SQLite and PostgreSQL drivers, and the Opus and G.729 codecs. The SIP stack, RTP, SRTP and media engine are our own code.
  Cryptographic primitives always come from the standard library and are never written here.
- Data in one SQLite file by default, or in PostgreSQL for several servers and reporting tools;
  recordings and voicemail stay as sound files.
- Interfaces to other systems are standards only, so BitVoice works with any CAD, and any CPE can work
  with our CAD.
- Survivable: active-active across sites, keeps answering when cut off, reconciles when the link returns.

## Hosting and support

BitVoice is free and complete, with no paid modules. BioDream LLC offers hosting and support
subscriptions for organisations that want them.

## Security

Please report security problems privately through this repository's Security tab ("Report a
vulnerability"), not in a public issue.

## Licence

Apache License 2.0. Copyright 2026 BioDream LLC.
