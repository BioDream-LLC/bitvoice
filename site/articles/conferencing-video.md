---
title: Conference rooms and video
tile: Audio conference rooms with PINs and HD mixing, and video rooms that switch, grid or forward every camera.
icon: video
area: Media
description: How BitVoice runs audio conference rooms with leader PINs and HD mixing, and three kinds of video room: switched, ffmpeg grid and SFU.
manual: conferences
---

A conference room is a number people dial to all talk together. The audio problem it solves is mixing: each participant's microphone must be combined and sent to everyone else, minus their own voice, in real time, for as many people as the room holds. The video problem is harder, because video streams are large and decoding and re-encoding them is expensive, so a video conference has to choose carefully how much work the server does. This article covers both: the audio room with its PINs and HD mixing, and the three ways BitVoice handles video, which trade server cost against what each participant sees.

### How audio mixing works

In an audio conference the server receives every participant's audio, decodes each to raw samples, sums the samples (leaving out each listener's own voice so they do not hear an echo), and encodes the result for each listener's phone. Because the mix is produced centrally, every phone can use its own codec: a mobile on G.711 and a browser on Opus hear the same room. The classic failure is sample rate: if the room mixes at telephone quality (8 kHz), a participant on a wideband phone loses the crispness their phone is capable of, and if it mixes at a high rate for everyone, a room of fifty gets expensive.

Walk through one room. Four people dial extension 500. Each hears a tone, enters the PIN, and joins. BitVoice mixes the four, and each of the four hears the other three. A fifth joins as a leader with the leader PIN; because this room waits for a leader, the first four had been hearing hold music until that moment, and now the room opens.

### Where video goes wrong

Video conferencing fails on processor cost and on what the viewer sees. Decoding and re-encoding every stream (a true mixer) gives a tidy grid but costs a processor core or more per room and adds delay. Switching to show only the current speaker costs almost nothing but shows only one person at a time. Forwarding every stream to every viewer (a selective forwarding unit, or SFU) lets each viewer see everyone, but the limit becomes the network: each participant's upload and each viewer's download.

## How BitVoice does it

A **conference room** on the **Conference rooms** screen has a number, a name, an optional **PIN** and an optional **leader PIN**. The choices are a short list: **wait for the leader** (everyone else hears hold music until a leader joins, and the room closes when the last leader leaves), **members join muted** (for a briefing: members press 1 to ask to speak, a leader unmutes them), **record the room** (the whole room kept as one recording), join and leave tones, and the maximum people (50 by default, up to 200). During a call, `1` mutes and unmutes yourself and `2` lets a leader lock the room; the screen shows each room in use, who is talking, who is muted, who has asked to speak, and lets an administrator mute, remove or lock.

Rooms mix at telephone quality (8 kHz) by default, which is what every phone can hear and keeps a fifty-person room cheap. Switch on **HD audio** and the room mixes at 16 kHz instead: people on wideband phones (G.722, Opus, the browser phone) hear each other in HD while people on ordinary phones hear the same room as usual, with the live view marking who is in HD. HD needs G.722 among the codecs the phones use.

Video comes in three kinds, chosen to match the cost trade above.

**Switched video** is the cheapest. Set **Video** on a room and video phones that join see one picture, switched as people talk, in one of three layouts: **follow the speaker**, **show the leader**, or a **grid** of up to nine. BitVoice switches the pictures without decoding them, so it costs next to nothing, and asks each new picture's phone for a full frame (RTCP PLI and FIR, and the SIP INFO picture-fast-update most desk phones answer) so the switch shows at once. Each viewer gets one steady stream whoever is on screen, in the room's video codec (H.264 unless you choose VP8); a phone without that codec joins with sound only.

**The ffmpeg grid** is the true mixer. A grid room hands every picture to ffmpeg on the server, which decodes them, places them in a 1280 by 720 picture at fifteen frames a second, and encodes that once for everyone. It needs ffmpeg installed (Settings says whether it is found; without it a grid room falls back to follow-the-speaker). Each picture costs processor time, so on a small server you keep grids to a few rooms at once. Only members whose camera is sending are shown, so the grid never freezes on a still camera, and the tenth member and later are heard but not shown.

**Everyone's video (the SFU)** is for browsers. Choose **Everyone's video** and the room gets its own page. Each person presses **Join with my camera**, and the browser sends its camera once in three sizes (simulcast); BitVoice forwards the pictures as they came, never decoding them, so it costs little processor time and takes up to sixteen cameras. The big picture gets the full size of that camera and the small ones the small size; **Make big** swaps them, at the next full picture of the new size, which BitVoice asks for with an RTCP PLI. A browser short of upload stops sending its largest size, and viewers then get the largest size still coming. To make Chrome send its larger layers at all, BitVoice sends a REMB of 2.5 Mbit/s. The sound is the room's conference as always, so joining calls the room on the browser phone, and anyone dialling the room from a phone is heard and hears everyone.

## Why BitVoice fits

All three kinds of video, and the audio mixing, are in the one Go binary and configured in the web interface, alongside the rest of the phone system; a conference is a destination you can point a route or a menu key at. The three kinds give an honest choice rather than one compromise: switched video for desk phones at almost no cost, the ffmpeg grid when you want everyone on screen and can spend the processor, and the SFU for browsers where each viewer picks what they see. The switching, the keyframe requests, the simulcast layer selection and the REMB are BitVoice's own WebRTC code, built on its own ICE, DTLS and SRTP, with no Asterisk or media server underneath.

The audio design is explicit about the cost of quality: telephone quality by default to keep large rooms cheap, HD on demand for the phones that can use it, with mixed phones in one room each hearing what they can. BitVoice is Apache-2.0 with no per-participant pricing, so a two-hundred-person room and a four-person room cost the same licence.

## What has been tested, and what has not

The audio rooms, PINs, leader PINs, HD mixing, muting, locking, recording and switched video run under BitVoice's own tests and the Chromium browser tests on every push. Video calls between two phones that both send video (relayed and encrypted) are part of the same suite.

**Everyone's-video rooms (the SFU with simulcast)** are **checked against a reference**: three Chromium browsers in one room, each sending three sizes (VP8 simulcast) and receiving the other two, with the sizes switched at keyframes as tiles grow and shrink. The maturity table is clear about the limits: not yet tried with Firefox or Safari, over the internet, or with more than three cameras, and H.264 simulcast picks layers by the browser's order only. The ffmpeg grid rests on ffmpeg on the server and BitVoice's own tests; video conversion between H.264 and VP8 through ffmpeg is off by default.

## Read more

- [The manual: Conference rooms](/manual/#conferences)
- [Codecs and the media engine](/technology/codecs/)
- [The browser phone and WebRTC](/technology/webrtc/)
- [Recording and payment-card pause](/technology/recording/)
