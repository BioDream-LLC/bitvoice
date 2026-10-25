---
title: "RTP, SRTP and the media engine"
tile: How the actual sound of a call travels, how it is encrypted, and how BitVoice carries and measures it.
icon: wave
area: Media
description: RTP carries call audio; SRTP encrypts it. How jitter, loss and MOS work, and how BitVoice's media engine relays, measures and protects the sound.
manual: codecs
---

## What RTP is

SIP sets a call up, but it does not carry the voice. The sound travels in a separate stream using the Real-time Transport Protocol, RTP, standardised as RFC 3550. Understanding the split between signalling and media is the single most useful thing to know about internet telephony: when a call "connects but there is no audio", the signalling worked and the media did not.

RTP is deliberately lightweight. Each packet has a small header and a payload of encoded audio, typically 20 milliseconds of speech, so a normal call sends 50 packets a second in each direction. The header carries a sequence number, so the receiver can tell if packets arrive out of order or go missing; a timestamp, so it can reassemble the sound with correct timing; a payload type, naming the codec in use; and a synchronisation source identifier, naming the stream. Alongside RTP runs RTCP, a control channel on which each side reports statistics: how many packets it received, how much it lost, how much the arrival times jittered.

### A call's audio, concretely

When extension 100 and extension 101 agree on G.722 in their SIP exchange, each starts sending RTP to the address and port the other named in SDP. Every 20 milliseconds, the speaking phone encodes a slice of sound into a packet and sends it. The listening phone collects packets into a small buffer, puts them back in sequence order, and plays them out smoothly. If a packet is late it waits a little; if it is too late it plays silence or conceals the gap. That small buffer is the jitter buffer, and it is where the trade-off between delay and smoothness lives.

Three numbers describe how a call sounds. **Loss** is the fraction of packets that never arrived. **Jitter** is how much the gap between arrivals varies from the steady 20 milliseconds. **MOS**, the Mean Opinion Score, is a single figure from 1 to 5 estimating perceived quality, computed from loss, jitter, delay and the codec. A MOS around 4.4 is what a clean G.711 or G.722 call should reach; below about 3.6 people start to complain.

### The jitter buffer, and why delay is a trade-off

The jitter buffer deserves a moment of its own, because it is where a judgement is made on every call. The internet does not deliver packets at an even pace; two packets sent 20 milliseconds apart may arrive 5 milliseconds apart, then 40 milliseconds apart. If the receiver played each packet the instant it arrived, the sound would stutter. So it holds a short backlog, a few tens of milliseconds, and plays out of that backlog at a steady rate, absorbing the unevenness. A larger buffer absorbs more jitter but adds delay, and delay above roughly 150 milliseconds each way starts to make conversation feel like a walkie-talkie, with people talking over each other. A smaller buffer keeps the call snappy but breaks up when the network is rough. Good telephony picks the buffer adaptively, growing it when jitter rises and shrinking it when the path is calm. When a packet is too late or never comes, the codec conceals the gap by stretching the previous sound, which is why a single lost packet is usually inaudible but a burst of loss is not.

## SRTP: encrypting the sound

Plain RTP is readable by anyone who can capture the packets. SRTP, the Secure Real-time Transport Protocol (RFC 3711), encrypts the payload and authenticates each packet, so the audio cannot be overheard or tampered with. The encryption keys are exchanged during call set-up: with SIP over TLS the keys ride in the SDP (SDES), and with WebRTC they are negotiated by DTLS. The usual cipher is AES with HMAC-SHA1 authentication. SRTP protects only the media; TLS protects the signalling, and a properly secured call uses both.

## Where media goes wrong

**Addresses behind NAT.** As with SIP, the addresses written into SDP are private addresses behind a home or office router, and audio sent to them goes nowhere. This is the root of most one-way or no-audio calls.

**Firewalls open to the wrong thing.** A firewall or cloud security group opened to a carrier's SIP servers but not to its media ranges blocks the audio while letting the call connect. The symptom is a call that rings, answers, and is silent.

**Narrow media port ranges.** Each relayed call needs a pair of ports. If the range is too small, the system runs out under load and new calls connect without sound.

**Jitter and loss on the path.** A congested or lossy network link raises jitter and loss, and the jitter buffer can only absorb so much before the sound breaks up. This is a network problem, not a phone-system one, but the phone system is where it is noticed.

## How BitVoice does it

BitVoice has its own media engine, written in Go; there is no Asterisk or FreeSWITCH carrying the audio. By default it **relays** the media: each call gets a pair of ports on BitVoice, the two phones send their RTP to BitVoice, and BitVoice forwards it on. Relaying has a cost in bandwidth and ports, but it means the phones never learn each other's addresses, NAT is handled in one place, and codec conversion and recording are possible because the audio passes through. When two parties share a codec and no conversion is needed, the payload is forwarded untouched.

**Measurement is built in.** **Live calls** shows the codec, loss, jitter and an audio score for each side of every call in progress. Finished calls keep their quality on **Call history**, and **Call quality** reports per site and per provider with a diagnosis in plain words, so "the sound is bad" becomes "this provider's calls lose 3% of packets, open UDP to its media range."

**No-sound detection.** A call that connects but never receives any audio from one side is marked **No sound from** that side, with calls shorter than five seconds, faxes, held calls and paging left out so they are not false alarms. When one side stops sending sound for a minute while the other keeps going (a phone that lost power sends no hang-up), the call is ended with the reason *the caller's audio stopped*, recorded and reported to CAD in the public-safety profiles; a side on hold, faxing, or that declared itself silent, such as a paging speaker, is never ended this way, and because silence still sends packets, a caller who cannot speak keeps the call up.

**Encryption to every phone that encrypts.** BitVoice offers SRTP to each phone and carrier that supports it, over SIP-over-TLS and over WebRTC with DTLS. On a trunk, **Require encryption** refuses what a provider will not encrypt. Call history labels each leg **Encrypted** or **Not encrypted**, so you can see at a glance which calls were protected.

**Capacity.** The default media port range (10000-20000) holds about 1,800 relayed calls; widening it in Settings raises the ceiling. On a 16-vCPU server, 2,000 relayed calls ran with no loss at MOS 4.41 using about ten vCPUs and 380 MB of memory.

## Why BitVoice fits

The media engine is BitVoice's own code, so the whole system stays one binary with a small memory footprint, and the quality numbers you see are measured by the same engine that carries the call rather than inferred. Everything is in the web interface: the port range, encryption, relaying and the quality diagnosis all have screens. Against Asterisk and FreeSWITCH, which are capable and widely used media engines in their own right, BitVoice's claim is narrower and plainer: one Go program that relays, measures, encrypts and converts the media a PBX needs, with honest per-call numbers and a no-sound detector that distinguishes real silence from a vanished caller. It is Apache-2.0 with no paid media modules.

## What has been tested, and what has not

Media relaying and measurement are **field-tested**. Capacity is field-tested: 2,000 relayed calls at once on a c7i.4xlarge with no loss and MOS 4.41, and 200 at once on a range of smaller EC2 instances, an Apple M4 and a Lightsail nano, all at MOS 4.41. Browser-to-engine WebRTC calls with sound checked both ways run in Chromium on every push, and across the internet to EC2. SRTP is exercised in those tests and against carriers that offer it; the SRTP parser is fuzzed. What is not independently certified is MOS against a formal listening panel; the figures are the engine's own estimate from loss, jitter, delay and codec, and should be treated as a guide. Run the built-in call generator on your own hardware before going live, because real numbers depend on the codec, conversion, recording and the network.

## Read more

- [The manual: Codecs and sound](/manual/#codecs)
- [SIP: how calls are set up](/technology/sip/)
- [Codecs and conversion](/technology/codecs/)
- [NAT, STUN and TURN](/technology/nat-ice-turn/)
