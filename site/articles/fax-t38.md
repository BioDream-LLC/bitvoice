---
title: "Fax over IP: T.38 and G.711 fallback"
tile: Why fax struggles over the internet, how T.38 fixes it, and how BitVoice sends and receives faxes as PDFs.
icon: fax
area: Media
description: How T.38 carries fax reliably over IP, with G.711 fallback, and how BitVoice answers fax numbers itself and emails every fax as a PDF.
manual: fax
---

## What fax over IP is, and why it is hard

Fax was designed for analogue phone lines. A fax machine turns a page into a pattern of tones, sends those tones down the line as if they were sound, and the machine at the other end turns the tones back into a page. The protocol that governs this, T.30, is a careful back-and-forth: the two machines greet each other, agree a speed, send a page, confirm it arrived, and repeat. It assumes a continuous, low-loss, low-delay path, exactly what an old copper line provides.

The internet provides nothing of the sort. When a fax is carried as ordinary audio over a voice call, the speech codecs squeeze and reshape the sound, packets arrive late or not at all, and the delicate tone sequence that fax depends on is corrupted. Low-bit-rate codecs such as G.729 destroy it outright; even G.711, which leaves the sound intact, suffers when packets are lost or jitter upsets the timing. Faxes that used to go through every time now fail half the time, and nobody can see why.

**T.38** is the fix. Instead of carrying the fax as sound, T.38 (standardised in 1998) understands the fax conversation and carries the *information* across the IP network, re-creating the tones at each end against a local, clean connection. The two fax machines each talk T.30 to a nearby gateway over a real or emulated line, and the gateways exchange T.38 packets, which include redundancy so a lost packet does not ruin the page. The call usually starts as a normal voice call and switches to T.38 the moment a fax tone is heard.

Error correction mode, ECM, adds another layer of reliability on top. When both fax machines support it, a page is sent in frames and any frame that arrives damaged is re-sent, so a page either arrives correct or not at all, rather than arriving with speckled or shifted lines. Over T.38 with ECM a fax is about as reliable as it ever was on copper. The speed a fax negotiates, up to 14,400 bit/s on an ordinary fax, falls back automatically on a poor line, trading pages-per-minute for the chance of getting through; this is the familiar sound of two fax machines "handshaking" and settling on a rate. All of this is invisible to the person sending the fax, who sees only that it went or it did not, which is exactly why the failure modes are so frustrating when the underlying path cannot carry the tones.

### A fax, step by step

A fax number's calls are routed to the fax system. A call comes in; the system answers as a voice call. The calling fax machine sends its initiating tone; the system recognises it, re-negotiates the SIP session to switch the media to T.38, and from then on the fax travels as T.38 packets rather than audio. The pages arrive, each confirmed, and when the far end hangs up the fax is complete. If the provider or a gateway does not support T.38, the call can fall back to G.711 and carry the fax as carefully preserved sound, which works on good networks but is less robust.

## Where fax over IP goes wrong

**No T.38 support.** If the provider or gateway will not do T.38, the fax is carried as audio and fails on any network with loss or jitter.

**Compressed codecs in the path.** A codec list that allows G.729 or Opus on a fax call wrecks it; fax over audio needs G.711.

**Mismatched redundancy or timing.** T.38 has options for redundancy and timing; two ends that disagree can still fail pages even when both claim T.38.

**Partial pages.** A fax that breaks off part way can lose everything or keep what arrived, depending on how the system handles it.

Fax has a stubborn afterlife worth acknowledging. In healthcare, law, government and finance, a fax is still treated as a trusted, auditable delivery, and whole workflows depend on a number that answers and prints. Those organisations are often the ones moving off old PBXs and onto IP, and they will not drop fax to do it. A phone system that cannot carry fax reliably forces them to keep an old line or a standalone fax service running alongside, which defeats much of the point of consolidating. This is why T.38 support, and the ability to receive a fax straight to email, is not a quaint extra but a requirement for a serious migration.

## How BitVoice does it

Fax machines, analogue phones, door phones and old lines connect through a SIP gateway, an ATA such as a Grandstream HT for one or two devices, or a larger Patton SmartNode or AudioCodes gateway for many ports, an ISDN line or old analogue lines, or through a telephony card in the BitVoice computer. Each port registers to BitVoice as an extension, like a phone, and those ports always use G.711, which fax needs.

**Receiving faxes without a fax machine.** On **Faxes** you add a fax number, say 300, with the email addresses each fax should go to, and in **Call routing** you send a real phone number's calls to it. BitVoice answers as a fax machine, switches the call to **T.38**, and takes the fax; each fax becomes a PDF, kept on the Faxes screen (**View**, **PDF**) and emailed. A fax that breaks off part way keeps the pages that arrived and says so. This needs a provider that supports T.38, which most do; a fax sent as plain sound cannot be received this way.

**Sending a fax.** On **Faxes**, **Send a fax**, you choose the fax number it goes from, type the number to send to, optionally write a note that becomes the first page, and add documents. Text files and pictures work everywhere; PDF needs poppler on the server, and Word, Excel, OpenDocument and RTF need LibreOffice as well, and the page says which the server can do. Every page gets a header line, as fax machines print it, with the time, the fax number's name, its identity and the page count. The call goes out over your outbound routes as an extension would, at the fastest rate the line carries (14,400 bit/s, slower if the line is poor). Faxes from one fax number go one at a time, and a busy or unanswered number is tried again five minutes later, three times in all. Sent faxes are listed alongside received ones as **Sent**, **Waiting to send**, **Failed** (with **Try again**) or **Part sent** when the line broke after some pages.

Pages are US letter in North America and A4 elsewhere, from the country set in Settings. Everything about fax is in the web interface: adding a fax number, the email recipients, routing a number to it, and sending.

## Why BitVoice fits

Fax over IP is usually a bolt-on: a separate T.38 gateway, or a module layered onto the PBX. BitVoice answers fax numbers itself, switches the call to T.38 itself, turns the result into a PDF and emails it, all from the one Go binary and all from the web interface. Against FreePBX and Asterisk, which handle fax through additional modules and often an external converter, BitVoice's claim is that receive-to-email and send-from-the-browser are built in, and that it is honest about needing a T.38-capable provider and about what document formats the server can render. It is Apache-2.0 with no paid fax module.

## What has been tested, and what has not

Fax is **checked against a reference**, spandsp (through Asterisk 20), on Amazon EC2: BitVoice received and sent faxes itself over T.38 (two pages, 14,400 bit/s, with error correction) and passed faxes between two other machines over both T.38 and G.711. During that testing a real bug was found and fixed, a fax number whose identity was written with spaces could not place a call, which is the kind of thing only testing against real fax software reveals. What has **not** been tried is a commercial fax provider or a physical fax gateway on a real line; the proof so far is against spandsp as the far end. The document-rendering path depends on poppler and LibreOffice being installed on the server, and the Faxes screen states what the server can do.

## Read more

- [The manual: Fax, fax machines and analogue phones](/manual/#fax)
- [Codecs and conversion](/technology/codecs/)
- [SIP trunks and carriers](/technology/sip-trunks/)
- [Legacy protocols: H.323, Skinny and MGCP](/technology/legacy-protocols/)
