---
title: "NAT traversal: STUN, TURN and ICE"
tile: Why calls lose their audio behind routers, and how STUN, TURN and ICE get the sound through.
icon: globe
area: Media
description: How NAT breaks VoIP audio, and how STUN, BitVoice's own TURN server and full ICE get the sound through firewalls and routers.
manual: phones
---

## Why NAT is a problem for calls

Almost every phone, browser and office computer sits behind Network Address Translation, NAT. A home or office router gives devices private addresses (192.168.x.x) and rewrites the outer packets so they appear to come from the router's single public address. For the web this is invisible: a browser starts a connection outward, and replies come back along the path the router opened. Telephony is harder, because a call's audio addresses are written inside the SIP and SDP messages, and those addresses are the private ones, meaningless on the public internet.

The result is the most common complaint in VoIP: the call rings, answers, and then nobody can hear anything. The signalling succeeded because it travelled along an outbound-opened path, but the media was sent to a private address behind a router and fell on the floor. Sometimes one side hears and the other does not, which is NAT biting in one direction only.

Three mechanisms were built to solve this, and WebRTC ties them together.

**STUN** (Session Traversal Utilities for NAT) is a way for a device to learn its own public address. The device asks a STUN server "what address do you see me coming from?", and the server replies with the public address and port the router mapped. Armed with that, the device can advertise an address the far end can actually reach. STUN is lightweight and works for most ordinary NATs.

**TURN** (Traversal Using Relays around NAT) is the fallback for the hard cases: networks that block UDP entirely, or symmetric NATs that assign a different mapping for every destination so STUN's answer is useless. A TURN server is a relay with a public address: both sides send their media to it, and it forwards each to the other. It always works, at the cost of carrying the traffic, so it is used only when a direct path cannot be found.

**ICE** (Interactive Connectivity Establishment) is the procedure that tries every path and picks one that works. Each side gathers candidate addresses, its own local ones, its public one via STUN, and a relayed one via TURN, exchanges them, and then systematically tests pairs until it finds a pair that carries packets both ways. ICE is why a WebRTC call usually connects even across awkward networks: it quietly tries the options in order and settles on the best path that works.

### A connection, concretely

A browser phone on a corporate guest network wants to reach the phone system. It gathers its local address (useless outside the office), asks a STUN server and learns its public address (which may or may not be reachable), and requests a relay address from a TURN server (which is always reachable). It offers all three. The server replies with its own address. ICE tests the pairs: the direct public path fails because the guest firewall blocks inbound UDP, so ICE falls back to the TURN relay, and the audio flows through it, still encrypted end to end.

## Where NAT traversal goes wrong

**No TURN available.** On a strict network with only STUN, the direct paths fail and there is nowhere to fall back to, so the call has no audio.

**TURN on a blocked port.** A TURN server on UDP 3478 is no help on a network that allows only outbound HTTPS; it needs to be reachable on a port the firewall permits, such as 443 over TLS.

**The server itself behind NAT.** If the phone system has no public address of its own (an office without port forwarding, or a cloud machine with no public IP), it cannot answer at one fixed address; it has to take part in ICE like a client.

**mDNS candidates.** Browsers often hide their local addresses as `.local` names, which the far end must resolve on the local network or the connectivity checks never complete.

## How BitVoice does it

By default BitVoice uses **ICE-lite**: it answers at its own address rather than gathering candidates, which is correct and fast for a server that has a stable, reachable address, and lets browsers and softphones connect without any setting.

For networks that block UDP or hide devices behind difficult NATs, you switch on the **TURN server** in Settings. BitVoice then runs its **own** TURN server (RFC 8656) on port 3478 over UDP and TCP, and, with the server's certificate, on port 5349 over TLS. Browser phones, the staff phone and call links are handed short-lived credentials and use the relay only when they cannot reach the media ports directly. The audio stays encrypted end to end, and crucially BitVoice relays only to its own addresses, so the TURN server cannot be abused to reach anything else on the network. You open the TURN port, the TLS port and the relay ports (49152 to 49407 unless you change them) in the firewall; TURN over TLS is offered to browsers only once there is a certificate they trust for the name they use, so they do not try it, fail and wait, and you can set its port to 443 on a server with a spare address to get through firewalls that allow only HTTPS.

When BitVoice **itself** has no public address, you switch on **This server is behind NAT** in Settings. BitVoice then takes full part in ICE (RFC 8445) instead of answering at one address: it offers its own addresses, its public address as **STUN servers** see it, and relays on **TURN servers** it signs in to, and it runs the connectivity checks against the browser as a browser would. **Relay every call through TURN** forces all browser media through the relays for networks that require it, and **Find my addresses** asks the servers as a call would and lists exactly what BitVoice will offer, flagging a STUN server that does not answer. Browsers that hide their addresses behind mDNS names are resolved on the local network. This mode adds at most about a second and a half to call set-up while the servers answer, which is why it is off by default; caller video uses ICE-lite either way.

## Why BitVoice fits

NAT traversal normally means running and securing a separate TURN server such as coturn alongside the PBX. BitVoice builds the STUN handling, its own TURN server and the full ICE agent into the same Go binary, all switched on from Settings, and locks the TURN relay to its own addresses so it cannot become an open relay. Against FreePBX and Asterisk, which reach the same capability by configuring an external STUN/TURN service, BitVoice's claim is that the relay and the behind-NAT mode are part of the one appliance and are turned on with a toggle, not assembled from separate pieces. It is Apache-2.0 with no paid component.

## What has been tested, and what has not

Full ICE behind NAT (the STUN and TURN client) is **checked against a reference**: against Chromium's own ICE agent (host candidates and relay only), with JsSIP softphones and BitVoice offering, using both coturn 4.18 and BitVoice's own TURN server as relays, and with Chromium's mDNS host names resolved. It has **not yet been tried from a server behind a real NAT router**; the testing used cloud networking rather than a home or office router. The TURN server is exercised in these tests and in the browser-phone tests. The browser phone and the WebRTC path it depends on are field-tested from outside to EC2 and in Chromium on every push, so the common case, a browser behind ordinary NAT reaching a BitVoice with a public address, is well covered; the hardest case, BitVoice itself behind a consumer NAT router, is proven against a reference agent but not yet in that exact setting.

## Read more

- [The manual: Connecting phones](/manual/#phones)
- [The browser phone (WebRTC)](/technology/webrtc/)
- [RTP, SRTP and the media engine](/technology/rtp-media/)
- [SIP: how calls are set up](/technology/sip/)
