---
title: ISDN PRI and BRI on telephony cards
tile: Digital ISDN lines (T1, E1 and BRI) through the separate bitvoice-dahdi program, with names, redirecting and network transfers.
icon: plug
area: Media
description: How BitVoice carries ISDN PRI and BRI lines through bitvoice-dahdi, speaking Q.921 and Q.931 with names, redirecting, transfers and AOC.
manual: cards
---

ISDN, the Integrated Services Digital Network, was the phone industry's first fully digital line, and for decades it was how a business connected its phone system to the phone company. It is still in service in much of the world, and a phone system that cannot speak it cannot connect to those lines. This article is about carrying ISDN into BitVoice: primary rate (T1 and E1) and basic rate (BRI) lines on a telephony card in the server, driven by a separate program so a card fault can never take the phone system down.

### How ISDN works

An ISDN line carries several voice circuits and one signalling channel. A T1 carries 23 voice circuits (the B-channels) and one signalling channel (the D-channel); an E1 carries 30 B-channels and a D-channel. Everything about a call, the number dialled, the caller's ID and name, whether the far end answered or is busy, travels on the D-channel as messages, while the B-channels carry only audio. Two protocol layers run on the D-channel: Q.921 (LAPD) keeps a reliable link between the two ends, and Q.931 carries the call-control messages on top of it. Basic rate ISDN (BRI) is the small version: two B-channels and a D-channel, common in Europe for small offices and for ISDN desk phones.

Walk through one incoming PRI call. The phone company sends a SETUP message on the D-channel naming a free B-channel, the number that was dialled and the caller's number and name. BitVoice answers with a CALL PROCEEDING, routes the call to an extension, and when the extension rings sends an ALERTING; the phone company plays ringback to the caller. When the extension answers, BitVoice sends CONNECT, the phone company bridges the audio onto the named B-channel, and the two talk. Because the answer is a signalling message, BitVoice knows the exact moment the far end picked up, which an analogue line cannot tell it.

### Where it usually goes wrong

ISDN deployments stumble on three things. The switch type: the phone company runs National ISDN-2, 5ESS, DMS-100 or EuroISDN, and the wrong one means the D-channel never comes up. The user/network side: exactly one end is the "network" and the other the "user", and getting it backwards is a common wiring-closet mistake. And supplementary services: caller name, redirecting numbers and call transfer handed to the network are extras that the line must actually subscribe to, and that a system must encode exactly as the standard says or the phone company rejects them.

## How BitVoice does it

A telephony card lives in the BitVoice computer (Digium, Sangoma, OpenVox and other cards with Linux DAHDI drivers). A separate program, **bitvoice-dahdi**, drives the card and joins its ports to BitVoice over the computer's own loopback connection, so **BitVoice itself never touches the kernel driver and a card problem cannot stop the phone system**. bitvoice-dahdi ships in the Linux download beside the main binary, talks only to 127.0.0.1, and BitVoice answers its token only from there.

You set the span up in DAHDI's `/etc/dahdi/system.conf` as usual (for a T1, `span=1,1,0,esf,b8zs`, `bchan=1-23`, `hardhdlc=24`), run `dahdi_cfg`, then on the **Telephony cards** screen under **Digital lines** add the span: T1 or E1, its first DAHDI channel, whether BitVoice is the PBX (user side, the usual) or the network, and the switch type the phone company gave you. BitVoice speaks the PRI itself, Q.921 on the D-channel and Q.931 call control, so every call comes in with the number dialled and the caller ID, goes out with the extension's caller ID, and the network reports answer, ringing and busy. Calls out take a PRI first and analogue lines after; the page shows whether each span's D-channel is up and how many channels are in calls.

**Names, forwarding and transfers** are the supplementary services, and BitVoice handles them. A caller's name arrives with the call (from the Display element, or on National ISDN-2 from a name facility) and phones show it; calls out send the extension's name the same way. The number a call was forwarded from arrives as its redirecting number, which BitVoice passes on as SIP's Diversion with the reason, and when an extension's forwarding sends a call back out on the PRI, the extension's number goes with it as the redirecting number. When an outside call is answered, BitVoice tells the network the connected number.

**Transfers through the phone company** are the most demanding piece. Switch it on for a span and when someone transfers a caller on that line to another number on the same line, BitVoice asks the network to join the two calls itself: Two B-Channel Transfer on National ISDN-2 and 5ESS lines, Release Link Trunk transfer on a Nortel DMS-100, and explicit call transfer on EuroISDN. Both B-channels are freed as soon as the network has joined the two parties, which is the point of the feature: the transferred call no longer ties up the PBX's channels. A DMS-100 gives a transfer identifier only to calls BitVoice places, so there one of the two must be an outbound call. If the network refuses, the transfer still works through BitVoice using both channels as before.

**Basic rate ISDN (BRI)** carries two calls at once and connects ISDN desk phones as well as lines. BRI cards (the Digium B410P, HFC-based cards from OpenVox and others) work through DAHDI the same way, added under Digital lines with BRI as the line type, in three roles: BitVoice as the PBX on a point-to-point line, as the user on a multipoint line (asking the network for its own TEI and matching the MSN the network sends), or as the network, where ISDN phones plug into the card's S-bus and dial digit by digit until the phone says it is complete. Names, forwarding, EuroISDN explicit call transfer and advice of charge work on a BRI as on a PRI.

**Advice of charge** asks the phone company, on a EuroISDN line, to say what each outbound call costs, during the call (AOC-D) and at its end (AOC-E), in money or in charging units; the last figure goes on the call's record, and when it is in your own currency it becomes the call's cost. SIP trunks may send the same advice as 3GPP AOC XML, which BitVoice keeps the same way.

## Why BitVoice fits

The ISDN stack is BitVoice's own Go code: Q.921, Q.931, the switch variants, the name and redirecting elements, and the three kinds of network transfer, byte for byte. There is no Asterisk or chan_dahdi running the signalling; the only thing from the Asterisk world is the DAHDI kernel driver that talks to the card itself, which is the standard way to reach these cards on Linux. Splitting the driver into bitvoice-dahdi keeps the design rule intact: the phone system is pure Go, the card program is a separate process, and a crash in the card program cannot take calls down. Everything is configured on the Telephony cards screen; the DAHDI `system.conf` lines are the card's own setup, which the manual shows for each line type.

A FreePBX DAHDI trunk imports as the BitVoice telephony-cards trunk, so a move from an Asterisk-based system keeps its digital lines. BitVoice is Apache-2.0 with no paid modules, so PRI, BRI, names, redirecting, network transfers and advice of charge are in the free download, not a commercial channel-bank add-on.

## What has been tested, and what has not

ISDN PRI and BRI are **checked against a reference** in the maturity table. The PRI code is tested against libpri, the ISDN library Asterisk uses, both as the user side and as the network side, for National ISDN-2 and EuroISDN calls, names, redirecting and connected numbers, explicit call transfer, and the Two B-Channel Transfer and DMS-100 Release Link Trunk requests byte for byte (with libpri's PBX joined by BitVoice acting as the DMS-100), and advice of charge in both directions. BRI is tested point to point and multipoint, as the terminal and as the network, including TEI assignment, calls broadcast to the bus, and digit-by-digit dialling.

On Amazon EC2 the whole path was run through the **real DAHDI 3.4 kernel driver**, with two spans wired together in the kernel and Asterisk's chan_dahdi as the phone company: calls both ways with numbers and names, all 23 channels at once, MOS 4.39. That run found and fixed a real bug, where restarting the card bridge left the PRI with no B-channels.

What has not been done is stated plainly: there is **no physical card or real line** yet, and bitvoice-dahdi is tested against the simulated and kernel-dynamic spans above, not against card hardware. Caller ID shown on or read from analogue phones, hook flash, and echo cancellation beyond what the card itself does are not yet built.

## Read more

- [The manual: Telephony cards](/manual/#cards)
- [SS7, MFC-R2 and E&M trunks](/technology/ss7-r2-em/)
- [SIP trunks](/technology/sip-trunks/)
- [Codecs and the media engine](/technology/codecs/)
