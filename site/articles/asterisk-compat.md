---
title: Asterisk compatibility: AGI, ARI, AMI and AudioSocket
tile: Run the AGI scripts, ARI apps, AMI tools and AudioSocket bots you wrote for Asterisk, unchanged.
icon: code
area: Developers
description: BitVoice speaks Asterisk's AGI, FastAGI and EAGI, the full ARI REST surface and Stasis apps, AMI actions and events, and AudioSocket.
manual: compat
---

## What these interfaces are

Asterisk has been the default open-source phone engine for over twenty years, and most of the integration code written for open telephony was written against one of its control interfaces. There are four that matter, and each solves a different problem.

**AGI, the Asterisk Gateway Interface,** is the oldest. It lets a program outside the dialplan drive a single call. Asterisk writes the call's details to the program as `agi_` variables, then the program sends one command per line (`ANSWER`, `STREAM FILE`, `GET DATA`, `RECORD FILE`) and reads one answer per command (`200 result=...`). A plain AGI program runs on the same machine, reading commands on standard input and writing answers on standard output. **FastAGI** moves the program onto another machine: the engine opens a TCP connection to `agi://host:port/script` and speaks the same line protocol over it, so one script server can handle many PBXes. **EAGI** ("enhanced") adds a copy of the caller's audio on file descriptor 3, for a program that wants to listen as well as control.

**ARI, the Asterisk REST Interface,** is newer and the opposite shape. Instead of a script per call, your application connects once, over a WebSocket for events and plain HTTP for commands, and takes control of calls that enter a named **Stasis** application. ARI thinks in resources: channels, bridges, playbacks, recordings, endpoints, device states, mailboxes and sounds. You answer a channel with `POST /ari/channels/{id}/answer`, mix two channels by putting them in a bridge, and hear everything that happens as JSON events. ARI is how modern call-centre and voicebot platforms are built.

**AMI, the Asterisk Manager Interface,** is the management and monitoring channel. A client opens a TCP socket, logs in, and then sends actions (`Originate` to place a call, `QueuePause` to pause an agent) and receives a stream of events (a call started, a device changed state, a queue member joined). Wallboards, click-to-dial buttons and CRM screen-pops are usually AMI clients.

**AudioSocket** is the simplest and the youngest: a tiny TCP framing that carries one call's raw audio to a program and back, so a bot can listen and speak without any of the above. It is how people wire a call into a speech service.

A concrete example: a clinic's appointment reminder is an AGI script. Asterisk answers, runs the script, the script plays a prompt, collects a digit, and books or cancels. Move the clinic to a new engine and that script, and every other one like it, is the migration risk.

## Where it goes wrong

The trouble with these interfaces in real deployments is rarely the happy path. It is the edges. An AGI script that keeps sending commands after the caller has hung up needs to be told the call is gone; Asterisk sends `HANGUP` on a FastAGI connection and `SIGHUP` to a local program, and code that depends on that signal breaks silently if the new engine does not send it. FastAGI servers are often behind more than one address for resilience, so the engine has to honour `hagi://` SRV records and try servers in priority order. ARI clients read `/ari/api-docs` at startup to learn the surface; if the fields in an event do not match the model the client expects, it throws before the first call connects. AMI clients parse the exact event names and key casing. And any of these, given a buggy script, can hang a call forever if the engine waits on the script without a limit.

## How BitVoice does it

BitVoice has no dialplan and no Asterisk underneath; its engine is its own Go code. The compatibility layer is a translation, built so that tools written for Asterisk keep working.

**AGI, FastAGI and EAGI.** On **Routing** (and in call scripts) you point a destination at an AGI target: a program's file name in the AGI programs folder, `agi://host:port/script` for FastAGI, or `hagi://domain/script`, whose servers come from the domain's `_agi._tcp` SRV records, tried in priority order. EAGI programs get the caller's audio (8 kHz, signed 16-bit, little-endian) on file descriptor 3 on Linux and macOS. The script reads the usual `agi_` variables and then the full command set. Because there is no dialplan to return to, where an Asterisk script would leave at a context and extension (`SET EXTENSION`, a `Goto`), the BitVoice call goes on to that number when the script ends. A script that takes longer than its time limit (an hour by default) ends and the call carries on; a program that runs past the call gets `SIGHUP`, and BitVoice stops waiting for it after a grace period (30 seconds by default). A command line longer than 8 KB is cut, as Asterisk cuts it.

```
agi://scripts.example.com:4573/reminder?patient=4821
hagi://agi.example.com/ivr        # servers from _agi._tcp SRV records
reminder.py                       # a program in the AGI programs folder
```

**ARI and Stasis.** Turn on **Asterisk apps** in Settings and point a destination at a Stasis application. BitVoice serves `/ari/api-docs` and the whole REST surface: channels, bridges, playbacks, recordings, endpoints, device states, mailboxes and sounds, with the matching events over the WebSocket. A call entering the app raises `StasisStart`; your code plays, records, reads keys, puts channels into mixing bridges, originates new channels into the app, and continues the call back into BitVoice. If your application disconnects, BitVoice holds the call for a short reconnect grace rather than dropping it.

**AMI.** The Manager interface carries actions and events a monitoring or dialler tool expects: `Originate`, queue control (`QueuePause`, `QueueAdd`, `QueueRemove`, `Agents`), `ConfBridge` actions, `ParkedCalls`, mailbox counts and status, monitor pause and resume, `Getvar`, `Filter` and `UserEvent`, with events such as `DialBegin` and `DialEnd`, `Hold`, `PeerStatus` and `MessageWaiting`. AstDB (`DATABASE GET/PUT`) and Asterisk sound names (`digit-1`, `goodbye`) resolve against BitVoice's own prompts.

**AudioSocket** is available for voice bots and ARI external media, so a call's audio streams to a bot process and back. The **Voice bots** chapter of the manual walks through connecting one.

## Why BitVoice fits

BitVoice is one Go program with a web interface and no outside runtime. There is no Asterisk, FreeSWITCH or dialplan file underneath the compatibility layer; the SIP stack, RTP, SRTP and media engine are BitVoice's own. That matters for a migration because the thing you are keeping, your scripts and tools, sits on a smaller, simpler base that is configured entirely from the browser. The licence is Apache 2.0, with no paid modules and no per-channel meter.

The honest comparison with Asterisk is that BitVoice gives you the control interfaces, not the dialplan. If your value lives in AGI scripts, ARI apps and AMI tools, they move. If it lives in a large handwritten `extensions.conf`, you rebuild that routing in BitVoice's visual call flow, which is usually clearer than the file it replaces. BitVoice does not claim to be Asterisk; it claims that the programs you pointed at Asterisk can point at it instead.

## What has been tested, and what has not

From BitVoice's feature maturity table, these interfaces are **checked against a reference**, meaning tested against an independent implementation or the standard's own data, not yet against a production deployment.

- **AGI and FastAGI:** every command's answer was compared with Asterisk 20.6's over FastAGI, a FastAGI server written with the `pyst2` library ran a call against BitVoice, and EAGI audio was delivered to a program. It has not been tried with a production IVR.
- **ARI:** every operation of Asterisk's published ARI was called and its answer checked against the model's required fields, and `ari-client` 2.2.0 (Asterisk's own JavaScript library) read `/ari/api-docs` and ran an application.
- **AMI:** covered by BitVoice's own tests.
- **AudioSocket and ARI external media:** software-tested with BitVoice's own tests.

So the protocol behaviour is verified against the reference tools people actually use, and the gap that remains is a run against a real, busy production integration. If you have one, that is exactly the kind of test BitVoice wants.

## Read more

- [The manual: Asterisk compatibility (AMI, ARI and AGI)](/manual/#compat)
- [Routing scripts and whole-call scripts](/technology/call-scripts/)
- [The REST API and configuration as code](/technology/api-automation/)
- [Moving from FreePBX, Asterisk and the rest](/technology/migration/)
