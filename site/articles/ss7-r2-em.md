---
title: SS7, MFC-R2 and E&M trunks
tile: Carrier signalling on telephony cards: SS7 ISUP link sets, MFC-R2 in nine country variants, and E&M with Feature Group D and CAMA.
icon: plug
area: Media
description: How BitVoice carries SS7 ISUP, MFC-R2 and E&M trunks through bitvoice-dahdi, with link sets, changeover, country variants and MF signalling.
manual: cards
---

Before ISDN and SIP, and still today on many carrier interconnects and in much of the world, telephone calls are set up by older signalling systems. SS7 is the signalling network the public telephone network runs on between exchanges. MFC-R2 is the register signalling used across Latin America, parts of Asia and Africa on E1 lines. E&M is the channel-associated signalling of tie lines and long-distance carrier trunks in North America. A phone system that handles carrier-grade and international interconnects needs all three. This article is about carrying them into BitVoice on a telephony card, through the same separate card program that keeps a card fault away from the phone system.

### Three older ways to signal a call

The three differ in where the signalling lives. In **SS7** the signalling is taken out of the voice circuits entirely: the voice circuits carry calls, each named by a circuit identification code (CIC), and a separate signalling link carries the call-control messages, over a protocol called ISUP, to the carrier's signal transfer point or switch. BitVoice becomes a signalling point of its own, with a point code the carrier assigns. In **MFC-R2** each circuit on an E1 carries its own line signals on the A and B bits of timeslot 16, and the number, the caller's category and the caller's number are exchanged as compelled multifrequency tones, forward and backward, between the two registers. In **E&M** each channel of a T1 is signalled on its own by robbed-bit signalling, and the number is sent as DTMF or as MF R1 tones after a start handshake.

Walk through one SS7 call in. A message arrives on the signalling link: an ISUP Initial Address Message naming a CIC, the number dialled and the caller's number. BitVoice maps the CIC to a voice channel, routes the call to an extension, and sends back an Address Complete Message when the extension rings and an Answer Message when it picks up. Because the signalling is separate from the voice, one link carries the set-up for every circuit on the span, and the voice channels carry nothing but audio.

### Where it usually goes wrong

These systems are unforgiving of mismatch. SS7 needs the right variant (ITU or ANSI), the right point codes, network indicator and CIC numbering, all assigned by the carrier, or the link never aligns. A multi-link set must share traffic correctly and change over cleanly when a link fails, or calls drop when they should carry on. MFC-R2 assigns tone meanings differently in each country, so the wrong variant reads a busy signal as something else. E&M needs the right start handshake (wink, immediate or delay) or the two ends talk past each other.

## How BitVoice does it

All three run through **bitvoice-dahdi**, the separate program that drives the telephony card and joins its ports to BitVoice over loopback, so the phone system never touches the kernel driver and a card fault cannot stop it. You set the span up in DAHDI's `/etc/dahdi/system.conf` for the signalling type, then add it under **Digital lines** on the **Telephony cards** screen.

**SS7 (ISUP).** You add the span, set Signalling to SS7, and give the **variant** (ANSI for North America, with network-cluster-member point codes; or ITU-T for most of the world, with zone-area-point point codes), **our point code** and **adjacent point code**, the **network** (national or international), the signalling **link code**, and the **first CIC**, all from the carrier. When the link comes up BitVoice aligns it at MTP level 2, proves it with the link test (SLTM and SLTA), exchanges traffic restart allowed, and resets every circuit with a group reset. Calls in reach the inbound routes with the number dialled, the caller's number, the redirecting number and, on ANSI, the caller's name; calls out carry the extension's caller ID and name. On a dual seizure the end with the higher point code keeps the even circuits, as Q.764 says.

**Link sets of several links** are the carrier-grade part. Spans with the same variant, point codes and network form one link set of up to sixteen links; each span's signalling link is one of the set's links with its own link code, and the circuits of every span belong to the set. Traffic is shared across the links in service by each message's signalling link selection, so one call's messages keep to one link. When a link fails, its traffic changes over to the others by Q.704 (COO and COA, resending the messages the far end had not acknowledged, or by timer when there is no answer), the calls on every span carry on, and only when every link is down is the link set down. When the link returns and is tested, its traffic changes back (CBD and CBA). The screen shows how many links are in service.

**MFC-R2 on an E1.** You set the span up as CAS, add an E1 under Digital lines with Signalling set to MFC-R2, and choose the **country variant**: ITU-T, Argentina, Brazil, Colombia, Czech Republic, Ecuador, Mexico, Philippines or Venezuela. Each variant holds that country's differences, such as what means busy, how the end of the number is told, and Mexico's extra Group C. Every circuit carries calls both ways, a busy extension or wrong number is signalled back as the variant says, and a withheld caller's number comes in as anonymous. China's variant is not offered.

**E&M trunks on a T1** give 24 individually signalled trunks for tie lines, channel banks and carriers' Feature Group D trunks. You set the **start signal**: wink start (the usual), immediate start, delay dial, or **Feature Group D** (wink start with MF numbers, the caller's number sent first as KP 00 ANI ST then KP number ST, the ANI becoming the caller ID). **Numbers sent as** is DTMF or MF (MF R1, KP number ST) as most carrier trunks want, and you can set a number a call reaches when the far end seizes the trunk without dialling.

**CAMA trunks** are the 911 piece, on the lab track. A PSAP still fed by a 911 selective router over CAMA trunks sets the start signal to CAMA: the router seizes, BitVoice winks, and the router sends the caller's number (ANI) in MF, enhanced CAMA as KP, two information digits and ten digits ST, or traditional CAMA as KP, one numbering-plan digit and seven digits ST, with the area code each numbering-plan digit stands for listed on the screen. Each call goes to the 911 inbound route as a 911 call with the ANI as its callback number, and BitVoice looks the location up by that number.

## Why BitVoice fits

The SS7 stack (MTP2, MTP3 with multi-link changeover, and ISUP for ITU and ANSI), the MFC-R2 register signalling in nine variants, and the E&M handshakes and MF tones are BitVoice's own Go code, driven through the DAHDI kernel driver by the separate card program. There is no Asterisk running the signalling; keeping the card program apart means the phone system stays pure Go and a card problem cannot take calls down. Everything is configured on the Telephony cards screen, with the carrier's values entered as a form rather than edited into a signalling config file.

BitVoice is honest about scope: being a signal transfer point for others, and SCCP/TCAP for number portability and toll-free lookups, are not offered, because a PBX is an end point and those are the carrier's job. That is a design boundary, not a gap in testing. BitVoice is Apache-2.0 with no paid modules, so SS7, MFC-R2 and E&M are in the free download rather than behind the commercial signalling stacks these interconnects usually require.

## What has been tested, and what has not

SS7, MFC-R2 and E&M are **checked against a reference** in the maturity table, and none has been run on a real carrier link or card.

SS7 (ISUP, MTP, and link sets with changeover) is tested against **libss7**, the SS7 library Asterisk's chan_dahdi uses, in ITU and ANSI: alignment, link test, traffic restart, circuit reset, calls both ways with numbers and names, release, busy and blocking; and BitVoice against itself with frames lost on the link (resent by MTP2) and a dual seizure. The documentation records a real interoperability detail: libss7 takes network-management messages only on the link whose SLS names them, so a changeover message about a cut link (sent on another link as Q.704 requires) is dropped there, and both ends change over by timer instead, which works. MFC-R2 is tested against **OpenR2** (the R2 library chan_dahdi uses) in every variant offered and both directions, including a call refused as busy; the E&M MF tones are tested by BitVoice's own tests against simulated channels.

What is **not offered**, with reasons, is stated plainly: SS7 signal transfer (STP) and SCCP/TCAP (the carrier's job); and the MFC-R2 China variant, because OpenR2's own China variant disagrees with itself, so there is nothing sound to test against.

## Read more

- [The manual: Telephony cards](/manual/#cards)
- [ISDN PRI and BRI on telephony cards](/technology/isdn-pri/)
- [SIP trunks](/technology/sip-trunks/)
- [NG911 call handling](/technology/ng911/)
