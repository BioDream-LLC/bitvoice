---
title: "SIP: how internet phone calls are set up"
tile: The signalling language that rings phones, sets up calls and tears them down across the internet.
icon: phone
area: Phone system
description: SIP is the protocol that sets up voice calls over IP networks. How it works, where it fails, and how BitVoice speaks it.
manual: trunks
---

## What SIP is

The Session Initiation Protocol, SIP, is the language phones and phone systems use to arrange a call. It does not carry the sound itself. Its job is everything around the sound: find the person being called, make the other phone ring, agree on how the audio will be sent, keep the call alive while it lasts, and end it cleanly when someone hangs up. The sound then flows separately as RTP, described in its own article.

SIP was written in the late 1990s and standardised as RFC 3261 in 2002. It was deliberately modelled on the web and on email. A SIP message looks much like an HTTP request: a first line naming a method, a block of headers, a blank line, and sometimes a body. Addresses look like email addresses with a `sip:` in front, such as `sip:100@pbx.example.com`. If you have read a raw HTTP request, a SIP message will feel familiar within a minute.

The methods are a short list. `REGISTER` tells the server where a phone can currently be reached. `INVITE` starts a call. `ACK` confirms that a call has been answered. `BYE` ends a call. `CANCEL` gives up on a call that has not been answered yet. `OPTIONS` asks whether the far end is alive. A handful of others (`REFER` for transfers, `SUBSCRIBE` and `NOTIFY` for busy lamps and message-waiting indicators, `INFO`, `MESSAGE`) cover the rest.

### One call, walked through

Imagine extension 100 calls extension 101. The phone at 100 sends an `INVITE` to the server. Inside that `INVITE` is a body written in SDP, the Session Description Protocol: "I can speak G.722 or G.711, send the audio to this address and port." The server works out where 101 is (it learned that from 101's earlier `REGISTER`) and sends an `INVITE` on to it.

The server does not wait silently. It sends back a `100 Trying` almost at once, so the caller knows the message arrived. When 101's phone starts ringing it sends `180 Ringing`, and the caller hears ringback. When someone lifts the handset, 101 answers `200 OK` with its own SDP: "I will speak G.722, send my audio here." The caller replies `ACK`, the two phones now know each other's media addresses and codecs, and the audio begins. When either side hangs up it sends `BYE`, the other answers `200 OK`, and the call is over.

Every response carries a three-digit code with the same families as HTTP: 1xx means in progress, 2xx means success, 3xx means redirection, 4xx means the request failed (486 is Busy Here, 404 is Not Found), 5xx is a server error, 6xx is a global failure such as 603 Decline. The codes are one of SIP's quiet strengths: a call that fails tells you, in a number, exactly why.

## Where SIP goes wrong

SIP is text, so it is easy to read and easy to break. The problems that bite real deployments are nearly always the same few.

**NAT.** SIP messages carry IP addresses inside their text, in the headers and in the SDP body. A phone behind a home or office router writes its private address (192.168.x.x) into those fields, but the router rewrites the outer packet to a public address. The server then tries to send audio to an address that means nothing on the public internet, and the call connects with no sound. This single mismatch is the cause of most "it rings but I can't hear anything" reports. The NAT, STUN and TURN article covers the ways round it.

**Transport confusion.** SIP runs over UDP, TCP or TLS. UDP is the oldest and most common, but a single UDP packet larger than the network's limit is fragmented and often dropped, and large `INVITE` messages with video or many codecs can exceed it. TCP and TLS avoid that but need the far end to agree.

**Timers and retransmission.** Because UDP can lose packets, SIP retransmits. If timers are mismatched, or a firewall closes an idle path, calls drop at seemingly random moments, often around the 30-second mark where a half-open path times out.

**Security.** A SIP port open to the internet is found by scanners within hours. They try to register as extensions with guessed passwords, and a system broken into runs up calls to premium-rate numbers overnight. Any SIP system facing the internet needs rate limiting, strong per-extension passwords and fail-to-ban as a baseline, not an afterthought.

## How BitVoice does it

BitVoice has its own SIP stack, written in Go. There is no Asterisk or FreeSWITCH underneath it; the parsing, the transaction state machines and the dialog handling are BitVoice's own code, and the SIP, SDP, STUN and SRTP parsers are exercised by fuzzing on every push.

A phone connects by pointing at the server's address on **port 5060**, with the extension number as the user and the extension's SIP password. On the **Phones** screen you add the phone; desk phones from the major makers are set up automatically so nobody types a password into a keypad. The transport can be **UDP**, **TCP** or **TLS**; TLS encrypts the signalling and pairs naturally with SRTP for the audio.

Everything SIP does has a place in the web interface. **Live calls** shows every call in progress with the codec, loss and jitter for each leg. The **SIP trace** screen shows the raw messages as they arrive and leave, so a registration that is failing, or an `INVITE` being rejected with a 403, can be read directly rather than guessed at. Every finished call on **Call history** carries a plain-words account of how it was routed: the route, the hours, the scripts, the keys pressed and the trunk that carried it.

Because SIP is only signalling, BitVoice always acts as a back-to-back user agent: it terminates each call and starts a fresh one towards the destination, rather than blindly passing messages through. That means it can convert between codecs, hide the phones' addresses from each other, and strip headers it does not understand before sending a call on. It also means a routing script or an integration can never leave a call hanging: each script is sandboxed, time-limited, and has a fixed outcome if it fails, so a call is never lost or held up.

On the security side, addresses that keep failing to register are banned (by default ten failures in a minute earns an hour's ban), trusted office networks are never banned, and a phone signed in as one extension can never register another. A banned scanner gets no answer at all, so it learns nothing.

## Why BitVoice fits

BitVoice is one Go binary with its own SIP stack and its own media engine. There is no second project layered underneath, which is why the whole system is a single 37 MB file that holds four live calls and eight phones in about 47 MB of memory. Everything SIP touches is configured in the web interface: there are no `sip.conf` or `pjsip.conf` files to hand-edit, and a test on every push fails the build if any setting is reachable only through a file or the API.

Against Asterisk and FreePBX the honest comparison is this: those are mature, widely deployed systems built from many C modules and configuration files, and FreePBX layers a web interface over Asterisk. BitVoice sets out to do the everyday SIP work of a PBX from a single program with one interface, and it can import a whole FreePBX backup so a move does not start from nothing. It is Apache-2.0 licensed with no paid modules and no per-extension meter. Where it has been proven against real SIP software, that is stated plainly below rather than claimed in general.

## What has been tested, and what has not

BitVoice's SIP has been **field-tested** against real systems over a real network. Against a commercial carrier (a SIP.US demo account, from Amazon EC2) it registered and placed calls out to the public phone network over both UDP and TLS, with the carrier's certificate checked, at MOS 4.39 with no loss. It has run against Asterisk 23.5 and Asterisk 20 playing both a carrier and a desk phone, with registration by digest authentication, calls in and out, caller ID and name, busy and failed numbers passed back correctly, and DTMF both ways. It has interoperated with Kamailio 5.7 and FreeSWITCH 1.11 as the far end over TLS, and with JsSIP and SIP.js softphones over SIP-over-WebSocket.

What has not been tried: real desk-phone handsets from the big makers on a live network (Asterisk has stood in as the desk phone so far), and a wider set of commercial carriers. The maturity table on the project's README keeps this current.

## Read more

- [The manual: Trunks](/manual/#trunks)
- [SIP trunks and carriers](/technology/sip-trunks/)
- [RTP, SRTP and the media engine](/technology/rtp-media/)
- [Desk-phone provisioning](/technology/desk-phones/)
