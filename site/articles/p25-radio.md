---
title: The radio dispatch console
tile: A lab-track console: P25 talkgroups over the ISSI, radio over IP, IMBE voice, emergencies, patches, simulselect, unit names and positions.
icon: radio
area: Public safety
description: BitVoice's lab-track radio dispatch console: P25 ISSI and CSSI, radio over IP, IMBE voice, emergency acknowledge, patches, simulselect and AES-256.
manual: radio
---

This is the **lab and design-partner track.** The radio console is not certified for live radio dispatch, and it has not run against a live P25 RFSS; it is tested against a recorded ISSI capture and stand-ins. It is off the menu until **Public-safety profiles (lab)** is switched on in **Settings**, and its screen carries that notice. BioDream LLC is looking for a radio shop or agency willing to run it in a lab or in shadow mode.

## What a radio dispatch console is

In public safety, dispatchers talk to field units (police cars, fire engines, ambulances) over two-way land mobile radio, from a console: a screen and headset with push-to-talk buttons for each channel. A dispatcher monitors several channels at once, keys up to talk on one, patches two together when a mutual-aid situation calls for it, and sees which unit is talking and whether a unit has hit its emergency button.

Most new public-safety radio systems in North America follow Project 25 (P25), a suite of standards published by the Telecommunications Industry Association. P25 organises radios into talkgroups (a channel is a talkgroup, not a frequency), carries voice digitally with a vocoder, and lets separate radio subsystems interconnect. The interface that lets one subsystem join another's talkgroups is the Inter-RF Subsystem Interface (ISSI, TIA-102.BACA); a dispatch console attaching over it uses the console variant (CSSI). P25 Phase 1 encodes voice with the IMBE vocoder (a full-rate codec); Phase 2 adds a half-rate vocoder, AMBE+2, to fit two calls in the space of one. Encryption on P25 (TIA-102.AAAD) uses keys loaded into the radios, with AES-256 the modern choice.

Where a system is not P25, or is a mix of radios, a console reaches it through a radio-over-IP (RoIP) gateway: a box that turns a radio's audio and push-to-talk into IP packets. A concrete example: a dispatcher presses hold-to-talk on the "County fire dispatch" talkgroup; the console asks that talkgroup's home subsystem for the channel over the ISSI, and once it is granted, the dispatcher's voice, converted to IMBE, goes out to every radio on the talkgroup, and the tile shows which unit talks back.

## Where it usually goes wrong

Radio has its own failure modes. A voice-keyed radio held on the air because the console kept sending silence blocks everyone else. An encrypted transmission played as noise, because the console lacks the key, is worse than silence: it sounds like a fault. An emergency that clears itself when the radio stops transmitting, before any dispatcher acknowledged it, is a safety failure; and a second unit's emergency that silently overwrites the first means one is lost. Patches and simulselect that key some channels but not others, because one was already busy, leave a dispatcher thinking they transmitted on all of them when they did not.

The patent wall is real and often glossed over. P25 Phase 2's half-rate vocoder, AMBE+2, is patented, and its half-rate voice block format on the ISSI is sold by TIA rather than published, so a console cannot decode Phase 2 voice on its own; pretending otherwise, or decoding it without a licence, is not an option. An honest console has to say what it cannot do rather than play silence and let a dispatcher assume the channel is quiet.

## How BitVoice does it

Dispatchers talk on radio channels from the same headset as their phone calls. Under **Console setup**, choose a console number and add each channel: a RoIP channel gives the port the gateway sends audio to and the gateway's address for audio out, with the gateway set to RTP, G.711 µ-law and voice-keying. A dispatcher dials the console number or presses **Join with the browser phone**, then hears every channel's traffic mixed, with **Hearing / Muted** per channel and a lamp that lights while a channel receives. **Hold to talk** (or the space bar on a focused channel) sends the dispatcher's voice to that channel only, with the tile showing **TX**; one dispatcher talks at a time, and nothing is sent while nobody talks, so a voice-keyed radio is never held on the air. **Patch** on two channels joins them until **Unpatch**.

A channel can be a P25 talkgroup joined over the ISSI (TIA-102.BACA), with the CSSI console push-to-talk word added when set. You give the talkgroup ID, the home RFSS's SIP address and ID, and a voice port, and under **This console on the P25 system** the WACN, system ID, the RFSS ID your administrator gave this console, the unit ID dispatchers transmit as, and the ISSI's SIP port; the P25 system must know this server as an RFSS. The tile shows **P25** while the group call is up and the talking unit's ID, in red with EMERGENCY when it is an emergency. Hold-to-talk asks the home RFSS for the channel and sends once it is granted, saying so if it is refused. Voice is converted to and from IMBE, the P25 vocoder, in BitVoice. Under **Encryption keys**, each traffic key is added by name, algorithm (AES-256, DES-OFB or triple DES), key ID and hex key (TIA-102.AAAD); a key is never shown again once saved, the console hears an encrypted transmission only when it has the matching key and shows a lock and the algorithm, and without the key the transmission is **muted, never played as noise**.

**Private calls** let a dispatcher call one radio by unit ID and let radios call the console, each as its own tile. **Alert tones** send a steady tone, a warble, pulses or a beep, holding the channel while they play. **Emergencies** turn the tile red with the unit ID, note it in the log and tell CAD at once; the emergency stays raised on every console after the radio stops, until a dispatcher presses **Acknowledge**, and acknowledging names the unit shown, so if another unit's emergency replaced it a moment before, BitVoice refuses and shows the new one, and a second emergency is never silenced by mistake. **Simulselect** talks on several channels at once but is all or nothing: if another dispatcher is talking on one of them, none is keyed and you are told which. **Radio names** show a friendly name (Engine 5) beside the unit, and **Radio positions** list each radio's last fix on the map from reports a data or AVL gateway POSTs to the API, since positions travel over P25 packet data, not the ISSI voice, which BitVoice does not itself decode. **Record every transmission** keeps each one as a recording with a **Radio log** line (when, channel, unit or dispatcher, length, emergency, alert, encryption, private call), and **Send radio events to CAD at** POSTs each event as JSON over an encrypted or loopback path.

## Why BitVoice fits

The radio console is part of the same one Go program as the phones, so a dispatcher works radio and telephone from one headset and one screen, with the radio log beside the call history. The P25 ISSI and CSSI and the IMBE encoder are BitVoice's own Go code, with the IMBE decoder from GopherTrunk, and there is no Asterisk underneath. Everything is in the web interface: the channels, the P25 settings, the encryption keys, the radio names, the CAD address. It is Apache-2.0 and free. And it is honest where honesty matters for safety: a voice-keyed radio is never held on the air, an encrypted transmission without a key is muted rather than played as noise, an emergency stays raised until acknowledged and cannot be overwritten silently, and a Phase 2 talkgroup whose voice it cannot decode shows **Silent here** on the tile rather than letting a dispatcher take the silence for a quiet channel.

## P25 Phase 2 and the patents

Phase 2's native half-rate voice is not offered, and the reasons are stated plainly rather than hidden. Decoding it needs two things BitVoice does not have: the AMBE+2 vocoder, which is patented until 2028 and would need a licence from its owner or a licensed hardware chip, and the ISSI's half-rate voice block format (TIA-102.BACA-B), which TIA sells rather than publishes. A Phase 2 system still works with the console when its ISSI talkgroups carry full-rate (Phase 1) voice, or when a transcoder converts them, which is common; the thing to ask a P25 administrator is whether the ISSI delivers full-rate voice.

## What has been tested, and what has not

The maturity table rates the radio console **Lab**: never on a live RFSS, and not certified. The P25 path has been checked byte for byte against a real ISSI capture from NIST's public-domain ISSI tester, and the encryption against the standard's own worked examples; the rest is tested against stand-ins, including a stand-in P25 home RFSS used by the public demo. The browser tests join the radio console with the browser phone. P25 Phase 2 half-rate voice is listed as **Not offered**, patented until 2028. Position reporting relies on a gateway POSTing fixes to BitVoice, which does not itself decode LRRP or the P25 location service. None of this is a claim of certification or of use on any live radio system; the invitation is to a radio shop or agency to help prove it against a real RFSS.

## Read more

- [The manual: Radio dispatch console](/manual/#radio)
- [NG911 call handling (the 911 profile)](/technology/ng911/)
- [NG911 Core Services](/technology/ng911-core/)
- [Codecs and the media engine](/technology/codecs/)
