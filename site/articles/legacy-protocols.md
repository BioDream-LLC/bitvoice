---
title: "Legacy protocols: H.323, Skinny and MGCP"
tile: Connecting older gear that never spoke SIP, from H.323 trunks to Cisco Skinny phones and MGCP gateways.
icon: plug
area: Phone system
description: How H.323, Skinny (SCCP) and MGCP work, and how BitVoice connects legacy trunks, Cisco phones and gateways that never spoke SIP.
manual: deskphones
---

## The world before SIP

SIP won the internet-telephony argument, but it was not the first attempt and it did not arrive to an empty field. For years the standard for voice over IP was **H.323**, a suite from the ITU that predates SIP and still runs in video conferencing bridges, older gateways and some carrier interconnects. Cisco, meanwhile, built its own protocol, **Skinny** (the Skinny Client Control Protocol, SCCP), to control its desk phones from its Call Manager, and millions of Cisco phones in service speak it. And where the phone system wanted to drive simple gateway ports directly rather than let them think for themselves, the IETF defined **MGCP**, the Media Gateway Control Protocol. Any phone system that expects to connect to installed equipment, rather than only to new SIP gear, has to understand these.

### H.323 in brief

H.323 does the same job as SIP, set up a call, agree media, tear it down, but it does it with binary messages encoded in ASN.1 rather than readable text. A call uses H.225 for call signalling and H.245 for media negotiation, and a shortcut called **Fast Connect** folds the media negotiation into the call set-up so a call can start with one round trip. An optional gatekeeper handles registration and address resolution, much as a SIP registrar does, though many H.323 links are configured point to point with no gatekeeper at all. If you are interconnecting with an older video bridge or a legacy carrier gateway, H.323 is often what it speaks.

The binary encoding is both H.323's strength and its burden. ASN.1 with its packed encoding rules is compact and unambiguous, but it is far harder to read on the wire than SIP's plain text, and a small disagreement about an optional field between two implementations can block a call in ways that are tedious to diagnose. This is one reason the industry drifted to SIP: when something goes wrong, a readable message you can print and inspect is worth a great deal. H.323 remains in service because the equipment that speaks it has not been retired, not because anyone prefers debugging it.

### Skinny in brief

Skinny is not peer-to-peer like SIP; it is a client-server protocol in which a thin phone reports every button press to the call controller and the controller tells the phone exactly what to display, which keys to light, and when to play a tone. A Skinny phone registers over TCP, shows its line and number, and dials when the controller tells it the digits are complete. Because the intelligence lives in the controller, a Skinny phone does very little on its own, which is why it needs a controller that speaks its language rather than SIP.

### MGCP in brief

MGCP inverts the usual arrangement: the gateway is deliberately dumb, and the call agent drives it one endpoint at a time. The call agent tells a gateway port to play dial tone, to collect digits, to connect its audio to a given address. Endpoints are named like `aaln/1@gateway`. MGCP suits analogue adapters and line cards where the phone system wants full control and the gateway should hold no logic of its own.

## Where legacy protocols go wrong

**H.323 and NAT.** H.323 carries addresses in binary fields and uses several ports, which makes it even less NAT-friendly than SIP; many H.323 problems are the familiar no-audio story.

**Firmware on Cisco phones.** A Cisco phone must be running SCCP firmware, not SIP firmware or vice versa, and pointing it at the wrong controller or TFTP settings leaves it unregistered.

**MGCP endpoint naming.** The call agent and the gateway must agree exactly on endpoint names and the domain in them, or the agent's commands reach nothing.

**Assuming they are gone.** The biggest mistake is assuming no one runs these any more. Installed Cisco estates, video bridges and gateway cards keep them in service for years, and a migration that cannot talk to them cannot proceed.

### Why they matter for a migration

The reason a modern phone system still needs these protocols is almost never that someone wants to deploy new H.323 or Skinny gear. It is that an organisation already has it, in quantity, and cannot replace all of it on the day it changes phone systems. A hospital with four hundred Cisco SCCP phones, a council with an H.323 video bridge, a factory with MGCP-driven analogue line cards for its intercoms: each of these is a reason a migration stalls if the new system speaks only SIP. The practical answer is to let the old system and the new one coexist. The new system registers the old phones and gateways as they are, calls flow between old and new extensions, and the estate is replaced in waves as budget and time allow, one floor or one site at a time, rather than in a single risky cut-over. Supporting the legacy protocols is what makes that gradual move possible.

## How BitVoice does it

BitVoice speaks all three, in its own Go code, from the one binary.

**H.323 trunks** connect older systems and carriers. BitVoice implements H.225 and H.245 with **Fast Connect** and G.711, point to point without a gatekeeper. You add an H.323 trunk like any other, and calls go both ways.

**Skinny phones and MGCP gateways** share a screen, **Skinny phones and MGCP gateways**, under Calls. For **Skinny**, you add each Cisco phone (79xx, 69xx, 89xx running SCCP firmware) by its MAC address, choose its extension, switch on **Accept Skinny phones** and point the phone at BitVoice on TCP port 2000 in its TFTP settings. BitVoice handles registration, the line and number on the display, dial tone, dialling, ringback and ringing with caller ID, so the phone behaves as one of its extension's devices. For **MGCP** (RFC 3435), you add the gateway by the domain name it uses in its endpoint names, its address and the extension of each line, and set the gateway's call agent to BitVoice on UDP port 2727; BitVoice then drives each port one by one. Each Skinny phone or MGCP line becomes one more device of its extension, ringing with the extension's other phones, calling out as the extension and shown in the extension's devices with its state.

Everything is in the web interface, including the toggle to accept Skinny phones and the gateway and line definitions for MGCP. These are legacy bridges, present so that an installed estate can keep working while it is migrated, not the recommended way to connect new equipment, which is SIP.

## Why BitVoice fits

Supporting three obsolete protocols from one binary, with no Asterisk `chan_skinny`, `chan_mgcp` or `chan_ooh323` modules to load, keeps the appliance model intact: the legacy support is there when an old estate needs it and invisible when it does not. Against Asterisk, which reaches these through separate channel drivers of varying maintenance, BitVoice implements them in its own code and configures them from the web interface. The point of these protocols is migration: an organisation moving off a Cisco Call Manager or an H.323 bridge can keep the old gear ringing while it moves, rather than replacing everything at once. BitVoice is Apache-2.0 with no paid legacy modules.

## What has been tested, and what has not

**H.323 trunks** are **checked against a reference**: against H323Plus 1.28 (its simph323 test tool playing and recording WAV files) on EC2, with calls both ways using Fast Connect and G.711, each end hearing the other's tone at MOS 4.39; call set-up was also exercised with Asterisk's chan_ooh323, whose own audio is broken in Asterisk 22. **Skinny (SCCP) phones** and **MGCP gateways** are **software-tested** only, by BitVoice's own tests: no Cisco phone and no MGCP gateway has been tried on real hardware. So the H.323 side has heard real H.323 software at the far end, while Skinny and MGCP have been proven only against BitVoice's own test harness, and a real Cisco phone or gateway remains to be tried. The maturity table on the project's README keeps this current.

## Read more

- [The manual: Skinny phones and MGCP gateways](/manual/#deskphones)
- [SIP: how calls are set up](/technology/sip/)
- [Desk-phone provisioning](/technology/desk-phones/)
- [Asterisk compatibility (AMI, ARI and AGI)](/technology/asterisk-compat/)
