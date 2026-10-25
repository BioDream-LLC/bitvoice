---
title: Text, TTY, real-time text and caller video to 911
tile: A lab-track reach for 911: TTY, real-time text, text to 911 over MSRP, and caller video and photos by link with evidence hashing.
icon: video
area: Public safety
description: BitVoice's lab-track text and video for 911: TTY, real-time text, text to 911 over MSRP and SIP MESSAGE, and caller video and photos by WHIP and WHEP.
manual: psap
---

This is the **lab and design-partner track.** None of what follows is certified for live 911 use, and it has not run against a live ESInet, a text control centre or a PSAP CAD; it is tested against reference software and stand-ins. It is off the menu until **Public-safety profiles (lab)** is switched on in **Settings**, and the screens carry that notice. BioDream LLC is looking for design partners to prove it against real systems.

## What text and video to 911 are

Not everyone can speak on a 911 call, and sometimes a picture tells the responder more than words. Several technologies, of different ages, let a caller reach 911 in text or show what is happening.

The oldest is the TTY (text telephone, also called a TDD), used by deaf and hard-of-hearing people for decades. A TTY sends each typed character as a pair of tones (Baudot code at 45.45 bit/s) down an ordinary phone line, taking turns like a walkie-talkie, with conventions such as "GA" for "go ahead" and "SK" for signing off. It is slow and old, but it is still required on every 911 line. Newer is real-time text (RTT, RFC 4103), which modern mobile phones and NG911 calls send alongside the voice: each character appears as it is typed, with redundancy so a lost packet does not lose text. Different again is text to 911: a person texts 911 from an ordinary phone, the carrier's text control centre (TCC) relays it to the PSAP as a session carrying text and no voice, over MSRP (the Message Session Relay Protocol, RFC 4975) or as SIP MESSAGE requests.

Caller video is the newest. Rather than build an app, a PSAP sends the caller a one-time link; the caller opens it in a phone browser and can stream the back camera live or send photos and clips already taken (a dashcam file, say). The browser standards for this are WHIP for publishing a live stream and WHEP for viewing one. Because what the caller sends can become evidence, each file needs a hash (a SHA-256 fingerprint) taken as it arrives, so a copy can later be shown to be the same file, and the pictures, which may be distressing, should open blurred until a call-taker chooses to reveal them.

A concrete example: a silent 911 call might be a deaf caller waiting on a TTY; the call-taker can begin typing before any character has come, and what they type goes out as TTY tones while their voice is muted, because TTYs take turns.

## Where it usually goes wrong

Detecting a TTY on a voice call is delicate: speech, hold music and touch tones can look like Baudot tones to a sloppy detector, so a call-taker gets garbage, or a real TTY is missed. Text to 911 is often an afterthought bolted on beside voice, so the two are handled by different systems and a conversation is split. And the privacy and evidence rules around caller media are easy to get wrong: media sent over an unencrypted path, photos shown unblurred by default, files kept without a hash so their integrity cannot be proven, and no record of who viewed what, which a wellness programme needs because exposure to distressing images harms call-takers.

The hardest requirement is still the one from the voice side: none of this may lose or drop a 911 call. A text session that nobody is watching must wait, not disappear; a video link that cannot be texted must say so rather than fail silently.

## How BitVoice does it

All of this is part of the 911 profile, shown on **911 calls**. TTY is detected on every 911 call's voice from the first moment: what the caller types appears under **Text** as they type it, a silent call may be a TTY waiting, and **Reply by TTY** sends text, even before any has come, as Baudot tones in capitals, with the call-taker's voice muted while it plays because TTYs take turns. The usual etiquette applies (end a turn with GA, sign off with SK), characters a TTY cannot show are left out, and a character counts only when every bit of it is a clean TTY tone, so speech, music and touch tones are never mistaken for text. The conversation is kept with the call and in the i3 log, one event per turn, and listening can be turned off in the 911 settings.

Real-time text (RFC 4103) is answered by BitVoice itself, since desk phones show none: each character appears as the caller types it, the reply goes back as text, and lost packets are made good by the redundancy RTT carries, with anything lost beyond that shown as a replacement mark. Text to 911 arrives from a TCC as an MSRP session with no voice over the ESInet trunk; BitVoice answers it and it waits on **911 calls** marked **Text to 911**, with no phone ringing, until **Take this text** makes a call-taker the owner (the answer time is counted then, and CAD gets the ALI spill and EIDO then). A TCC that offers the session over TLS gets it over TLS, with this server's certificate, the TCC's own certificate checked against the fingerprint its offer names or against the PSAP Credentialing Agency roots. A SIP MESSAGE inside a 911 call's dialog is taken as the caller's text the same way. The whole conversation, however it came, is kept with the call and in the i3 log.

Caller video and photos are offered by a one-time link. **Send the caller a video link** makes a link that only this call opens, texted to the caller from the number under **Text the link from** or read out; when it cannot be texted (no texting number, or no number from the caller), the screen and the API say why. The caller opens it in a phone browser with no app: **Share live video** sends the back camera live over WebRTC (WHIP, RFC 9725), and **Send photos or videos** sends files already taken. On the call, live video shows **LIVE** and plays **blurred** until **Reveal (may be distressing)** shows it; each file shows its size, when and where it was taken (from the camera's clock and GPS), and its **SHA-256**, taken as it arrived, so a copy can later be shown to be the same; **Hold as evidence** keeps a call's files past the retention period; and a WHEP viewing link lets CAD, a unit's screen or another position watch the same live video without signing in, so it is treated as a key.

Video never takes resources from the calls: each live video has its own ports, the server takes at most **Live videos at once** of them (four by default), each asked to stay under a bit-rate ceiling, and video that stops for ten seconds is ended. With **Record callers' live video** on, the live video is kept with the photos as one file per stretch, the same pictures the phone sent, in an IVF file with each picture's own time, hashed as written, with a damaged picture left out rather than kept broken; **Reveal** plays it from a WebM or MP4 copy that ffmpeg makes, and **Download the original** gives the file the hash is of. Into CAD, the EIDO names each photo, clip and recording, and the live video while it lasts, as additional data by reference with a purpose (`caller-photo`, `caller-video`, `caller-video-recording`, `caller-video-live`), and BitVoice sends the EIDO again when a file arrives or live video starts or stops. Every fetch through a link is in the **Exposure** log (who, how often, over how many calls), for supervisors and the centre's wellness programme, and links that do not exist are guessed at no faster than sign-ins.

## Why BitVoice fits

TTY, RTT, text to 911 and caller video are one conversation on one call in one program, not four bolted-on systems, so a call-taker sees text and video beside the call rather than in separate windows. The reliability rules hold throughout: a text session waits rather than disappears, a video link that cannot be texted says so, and video has its own ports and caps so it never starves the voice calls. The evidence handling is built in, not added later: a SHA-256 taken as each file arrives, originals kept untouched beside playable copies, pictures blurred until revealed, and an exposure log for the wellness programme. The cryptography (TLS for MSRP, SRTP, the hashing) is Go's standard library, and it is all Apache-2.0 and free.

## What has been tested, and what has not

The maturity table rates caller video and the 911 profile **Lab**: never on a live ESInet, TCC or PSAP CAD, and not certified. Within the lab, TTY has been checked against spandsp's V.18 TTY implementation; text to 911 and real-time text against Wireshark captures; evidence hashing and the EXIF and MP4 time-and-GPS reading against ExifTool's own output; the recordings against ffmpeg; and the EIDOs against NENA's published EIDO schema. The caller-video path uses BitVoice's own WebRTC code. What is not here yet, and the chapter says so, is passing a text session on through the NG911 core (a text session is answered where it arrives) and the EIDO conveyance standard's own WebSocket subscriptions; BitVoice posts EIDOs to one address. As with the rest of the public-safety track, none of this is a claim of certification or of use at any PSAP.

## Read more

- [The manual: 911 calls (the 911 profile)](/manual/#psap)
- [NG911 call handling (the 911 profile)](/technology/ng911/)
- [NG911 Core Services](/technology/ng911-core/)
- [The radio dispatch console](/technology/p25-radio/)
