---
title: "Trunks and carriers: connecting to the phone network"
tile: How a phone system reaches the outside world, over SIP or IAX2, with registration, failover and signed caller ID.
icon: swap
area: Phone system
description: SIP and IAX2 trunks, registration, failover, channel limits, STIR/SHAKEN signing, Teams Direct Routing and forwarded-call history in BitVoice.
manual: trunks
---

## What a trunk is

A trunk is the phone system's account with a phone company. Internal calls between extensions never leave the building, but a call to or from a real outside number has to cross from your system onto the public telephone network, and the trunk is that crossing. In the old world a trunk was a physical bundle of lines or a T1 circuit; today it is almost always a SIP connection to a carrier's servers across the internet.

A trunk does two jobs. Outbound, it takes a call your system places and hands it to the carrier, which routes it onto the public network. Inbound, the carrier sends calls for your numbers to the trunk, and your system decides which extension, menu or queue they reach. The carrier identifies your traffic either by a registration (your system signs in, so the carrier knows where to send calls) or by a fixed IP address it recognises.

### A call out, step by step

Suppose someone at extension 100 dials an outside number. The system matches the digits against its outbound routes, picks a trunk, and sends an `INVITE` to the carrier's server with the dialled number and your caller ID. The carrier authenticates the request, usually with digest authentication against the trunk's username and password, then routes the call out. Ringback, answer and hang-up follow the same SIP exchange as any call. The difference from an internal call is only that the far end is the carrier, and the carrier enforces your account's limits: how many calls at once, which countries, which caller IDs you may present.

## Transports and protocols

SIP trunks run over **UDP**, **TCP** or **TLS**. UDP is the default most carriers expect. TLS encrypts the signalling and is the right choice over the public internet, especially paired with SRTP for the audio so the call content is encrypted too. A few carriers still want TCP without TLS.

Besides SIP, there is **IAX2** (the Inter-Asterisk eXchange protocol, RFC 5456). IAX2 carries both signalling and audio over a single UDP port, which makes it easier through NAT and firewalls, and it is the native way to link to Asterisk and FreePBX servers and to the IAX2 providers that still offer it. It supports call tokens to resist spoofing and can register in either direction.

## Where trunks go wrong

**One-way or no audio.** The most common failure is not the trunk failing to connect but the call connecting with no sound. A carrier's audio (RTP) often comes from different addresses than its SIP servers, and a firewall or cloud security group opened only to the SIP addresses keeps the audio out. The fix is to allow UDP to your media ports from the carrier's published media ranges.

**Registration churn.** If the registration interval is wrong, or a firewall closes the idle path between refreshes, inbound calls stop arriving because the carrier no longer knows where to send them, while outbound still works. This asymmetry is a classic clue.

**Everything down at once.** When every trunk seems to fail together, the cause is usually your own internet link, not the carriers. Treating that as a carrier failure and failing calls away wrongly makes things worse.

**Toll fraud.** A trunk is a way to spend money. A stolen extension password or an open route lets an attacker dial premium-rate and international numbers, often overnight, running up a large bill before anyone notices.

## How BitVoice does it

On **Trunks** you press **Add trunk**, enter the provider server and port, the username and password, and switch on **Register** if the provider asks for it. Within a few seconds the trunk shows **registered**, and if it does not, the reason is shown beside it. A trunk's fields include the transport, a separate authentication username when the provider uses one, a From domain, the caller ID shown on calls out, the provider's other addresses allowed to send calls in, and the **Calls at once** limit your plan allows; calls beyond the limit are handled rather than silently dropped. Calls from phones registered on this computer, and from the built-in call generator, are never mistaken for the trunk's traffic, even when the trunk allows 127.0.0.1.

**Failover is judged, not guessed.** With two or more trunks on an outbound route, the **trunk health judge** checks every 15 seconds whether each trunk is being used in its place or has been pushed behind the others. A trunk moves behind the rest when its provider stops answering keep-alive checks, or when its recent calls out failed one after another on the provider's side, and another trunk is working. It is not moved on a failing keep-alive alone while calls still get through, and when every trunk fails at once nothing is moved, because the likelier cause is your own link. A change needs two judgements in a row, about 30 seconds, so a trunk never flaps. A trunk is never removed outright: it is still tried when the others fail, so an emergency call always has every way out. The trunk's page shows the judgement and its reasons, and a call that went out another way says so in its record.

**Encryption on demand.** On a trunk, **Require encryption** (with media encryption on) refuses what the provider will not encrypt: a call out answered without SRTP is hung up, and a call in offered without SRTP is refused, with the reason recorded. BitVoice also detects a call that connects but carries no sound and marks it **No sound from** that side in Call history, with Call quality saying which media addresses to open.

**Signed caller ID (STIR/SHAKEN).** In the US and Canada the originating provider signs caller ID. BitVoice reads your provider's verdict, or checks the signed Identity header itself: the signature, that it is under a minute old, and that it names this call's numbers. If your provider lets you sign, or you are a provider yourself, switch on **Sign caller ID** on the trunk and give it your STI certificate and its P-256 private key; every call out then carries a signed Identity header (ES256, as SHAKEN requires), with the certificate published at `/stir/<trunk>.pem` unless you set another address.

**Microsoft Teams Direct Routing.** BitVoice can be the session border controller for a Teams tenant. You install a public certificate for the server's name, add a trunk starting from **Microsoft Teams**, and run the PowerShell the drawer generates (`New-CsOnlinePSTNGateway` and the voice route, policy and dial-plan rule) as a Teams administrator. Teams users then call BitVoice extensions and your lines, and BitVoice can ring Teams users.

**Forwarded calls keep their history.** When a call reaches you already forwarded, the provider says so with **History-Info** (RFC 7044) or **Diversion** (RFC 5806). BitVoice reads either, shows who the call was first dialled to and why it was forwarded, and sends the history on when it forwards the call further, so voicemail and the next system know who was really called. When the provider marks the history private, the numbers are not kept.

## Why BitVoice fits

Trunks are configured entirely in the web interface, failover is explained in plain words rather than hidden in dial-plan logic, and the whole system is one Go binary with its own SIP and IAX2 stacks. The trunk health judge follows a rulebook that is checked in every combination of its facts, so it will not, for example, strand emergency calling by removing a trunk. Against FreePBX and Asterisk, the trunk features line up on the everyday work, and BitVoice adds a judge that distinguishes a provider being down from your own link being down, and a check that never lets failover leave 911 without a path. It is Apache-2.0 with no paid trunk modules.

## What has been tested, and what has not

SIP trunking is **field-tested**: registration and calls out over UDP and TLS against a commercial carrier (SIP.US demo, from EC2) at MOS 4.39 with no loss, with inbound calls awaiting the account's verification; Asterisk 20 and 23.5 as the carrier across the internet; Kamailio 5.7 and FreeSWITCH 1.11 as the far end over TLS. **IAX2** is checked against a reference, Asterisk 23: registration, calls both ways with sound, busy. **History-Info and Diversion** are checked against Asterisk 20's PJSIP both ways, but not yet with a real carrier or Teams sending them. **Teams Direct Routing** is software-tested only; no live Teams tenant has been tried. AWS's own Voice Connector trunk could not be tested because it is closed to the test account.

## Read more

- [The manual: Trunks](/manual/#trunks)
- [SIP: how calls are set up](/technology/sip/)
- [Security: hardening against fraud and attacks](/technology/security/)
- [Codecs and conversion](/technology/codecs/)
