---
title: "The browser phone: WebRTC without plug-ins"
tile: Turn any browser into a phone, with encrypted audio built on BitVoice's own ICE, DTLS and SRTP.
icon: browser
area: Phone system
description: How WebRTC makes a browser a phone, and how BitVoice's own ICE-lite, DTLS and SRTP gateway rings it, with mobile apps too.
manual: phones
---

## What WebRTC is

WebRTC is the technology that lets a web browser make a real-time voice or video call without any plug-in or app. It was built for the web from the start and is now standard in every major browser. A page that uses it can capture the microphone, encrypt the audio, punch through firewalls, and send the sound to another party, all from JavaScript, with no installation.

WebRTC is really a bundle of older ideas assembled for the browser. The media is carried by RTP, as in any internet call. The keys that encrypt it are agreed by **DTLS**, a version of TLS that runs over the unreliable UDP transport media uses. The encryption itself is **SRTP**. And because a browser is usually behind NAT, WebRTC includes **ICE**, a procedure for discovering which network paths can actually carry the audio and choosing one that works. The crucial difference from classic SIP telephony is that WebRTC insists on encryption: there is no unencrypted WebRTC media.

That insistence is deliberate. WebRTC was designed in the open, by browser makers and the standards bodies, with the assumption that a call might start from any web page, so it could never be allowed to send media in the clear or to be set up without the user's consent to use the microphone. Every WebRTC call therefore authenticates its media with DTLS fingerprints exchanged in the signalling, and encrypts every packet. For a phone system this is a gift: a browser phone is encrypted by default, with no certificate to install on the endpoint and no SRTP keying to configure, because the browser and the server negotiate it themselves. The cost is that the server has to speak DTLS and SRTP and run the ICE checks, which is more work than answering a plain SIP call, and it is why a WebRTC gateway has historically been a separate, heavier component.

### What happens when you click the phone

In BitVoice, the phone icon at the bottom of every screen turns the browser into a phone for an extension. When you press it, the browser asks to use your microphone, generates its DTLS keys, gathers its candidate network addresses, and offers them to BitVoice. BitVoice answers with its own address and keys. The two run the ICE checks to find a working path, DTLS negotiates the SRTP keys over that path, and the encrypted audio flows. From then on the browser is a full phone of that extension: it rings with the extension's other phones, appears in ring groups and queues, can hold and be transferred, and shows busy lamps and message-waiting, exactly as a desk phone would.

## Where WebRTC goes wrong

**ICE failures on strict networks.** On a hotel or guest network that blocks UDP, or hides the browser behind a NAT WebRTC cannot traverse, the ICE checks find no working path and the call fails or has no audio. A relay (TURN) is the answer, covered in the NAT article.

**Certificate trust.** A softphone page served from another website, or a browser reaching a server on the local network, runs into the browser's security rules: the server's certificate must be trusted, and recent browsers ask the user before a public page may reach a local-network device.

**mDNS address hiding.** For privacy, browsers often present their local addresses as `something.local` names rather than real IP addresses, which a server must resolve on the local network to complete the ICE checks.

**Codec and DTLS version mismatches.** A gateway that only speaks an old DTLS version, or offers a codec the browser will not use, fails to connect.

## How BitVoice does it

The browser phone is built on BitVoice's **own** WebRTC code: its own ICE, its own DTLS 1.2, and its own SRTP. There is no external gateway process and no Asterisk underneath. By default BitVoice answers at a single address rather than gathering candidates of its own; this is **ICE-lite**, the standard arrangement for a server with a stable, reachable address, and it keeps call set-up fast. When the browser is on a network that cannot reach the media ports directly, BitVoice's own **TURN server** relays the audio, and when BitVoice itself is behind NAT it can take full part in ICE instead; both are covered in the NAT, STUN and TURN article.

BitVoice also accepts **SIP over WebSocket** (RFC 7118), so softphones and web apps written with libraries such as JsSIP and SIP.js connect too. You switch on **SIP over WebSocket** in Settings and point the softphone at `wss://host/ws` with the extension number and its SIP password; BitVoice terminates the WebRTC media itself (ICE, DTLS-SRTP, Opus or G.711), so these softphones reach every phone, trunk and feature as desk phones do. The WebSocket needs no sign-in of its own: the softphone authenticates with SIP, and failed sign-ins are counted against its address like any phone's.

For people without a desk phone there is a **staff phone** page at `#/phone`, a full phone reached with the extension and its SIP password and no administrator account. And there are **iPhone and Android apps** that make a mobile one of an extension's phones, ringing on the phone's own call screen even when the app is closed, woken by Apple's and Google's push services and paired from the extension's page with a QR code that works once for ten minutes.

Because the browser phone is built on WebRTC, the same stack powers a feature that has no equivalent on a desk phone: **call links**. A web address, QR code or website button rings you from any visitor's browser, with no app, no account and no phone number for the visitor; their browser becomes a WebRTC endpoint for the duration of the call. The same media path, ICE, DTLS and SRTP, carries it, and the TURN server and behind-NAT mode apply to it exactly as they do to the staff browser phone. This is why a single, solid WebRTC implementation is worth more than it first appears: the browser phone, softphones over WebSocket, the staff phone and website calling all ride on it.

Everything is in the web interface: the browser phone is on every screen, SIP-over-WebSocket, the TURN server and the behind-NAT mode are all Settings toggles, and the apps are paired and managed from the extension's page.

## Why BitVoice fits

WebRTC gateways are usually a separate, heavy component bolted onto a PBX. BitVoice builds ICE, DTLS and SRTP into the same Go binary as the rest of the phone system, so a browser phone works out of the box with nothing to install and no second service to run or secure. Against FreePBX and Asterisk, which reach WebRTC through additional configuration and modules, BitVoice's claim is that the browser phone is present on every screen by default, built on its own stack, and that softphones built on JsSIP and SIP.js are a supported path rather than a fragile add-on. It is Apache-2.0 with no paid WebRTC component.

## What has been tested, and what has not

The browser phone is **field-tested**: real WebRTC calls between browser phones and the call engine, with sound checked in both directions, run in Chromium on every push, and from outside to EC2 across the internet. **SIP over WebSocket** is checked against a reference, two independent libraries: JsSIP 3.13 and SIP.js 0.21, with two softphones of each registering, calling each other and hearing each other through BitVoice. Other softphones such as Linphone's and Zoiper's web clients speak the same protocol but have not been tried. The **Android app** is field-tested on a Google Pixel 11 in AWS Device Farm against BitVoice on EC2 with a Let's Encrypt certificate: paired by link, rung, answered from the notification and calling out, over Opus at MOS 4.1 with about 5% loss between the phone lab and the server. The **iPhone app** has been tested only in the iOS Simulator; its own call screen and push wake-up need a real phone and BioDream's own Apple and Firebase accounts, and have not yet been tried on one. The SIP, SDP, STUN and SRTP parsers are fuzzed on every push.

## Read more

- [The manual: Connecting phones](/manual/#phones)
- [NAT, STUN and TURN](/technology/nat-ice-turn/)
- [RTP, SRTP and the media engine](/technology/rtp-media/)
- [SIP: how calls are set up](/technology/sip/)
