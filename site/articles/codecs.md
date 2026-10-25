---
title: "Codecs and conversion"
tile: How sound is turned into data on a call, the codecs BitVoice speaks, and how it converts between any two.
icon: layers
area: Media
description: G.711, G.722, Opus, G.729, G.726, AMR, iLBC, Speex, GSM and more, converted between any two so every phone can talk to every carrier.
manual: codecs
---

## What a codec is

A codec is the method by which a phone, browser or carrier turns sound into data and back again. The word is short for coder-decoder. When you speak, the codec samples your voice thousands of times a second, compresses those samples into a compact stream of bytes, and that stream travels in RTP packets. At the far end the same codec reverses the process. Different codecs make different trade-offs between the bandwidth they use, the quality they deliver, and how much processing they cost.

The oldest and most universal is **G.711**, standardised in 1972, which carries ordinary telephone-quality sound at 64 kbit/s. It comes in two flavours: µ-law in North America and Japan, A-law everywhere else. Every carrier speaks G.711, which is why it is the safe fallback. **G.722** uses the same 64 kbit/s but delivers wideband, "HD" sound; it is the best choice between modern desk phones. At the low end, **G.729** squeezes telephone-quality speech into 8 kbit/s for thin links, at the cost of a little roughness and a per-stream licence history. **Opus**, the modern web codec, gives HD sound at around 24 kbit/s and copes gracefully with packet loss, which is why browsers and mobile apps use it.

### Why conversion matters

A codec is only useful if both ends of a call agree on it. A browser might offer only Opus; an old gateway might speak only G.726; a carrier might insist on G.729. If the two parties share no codec, the call cannot carry sound, unless something in the middle translates. That translation is transcoding: decode the incoming stream to raw audio and re-encode it in the codec the other side wants, for every packet, in both directions. Transcoding costs processing and adds a little delay, so it is done only when the two parties genuinely have nothing in common.

There is also the question of how codecs are named in a call. Each codec is given a payload-type number in the SIP offer, and two systems may number the same codec differently, or renumber a dynamic codec such as Opus from call to call. A media engine that transcodes has to read the offer and answer carefully, match what each side will actually accept, and renumber on the way through when the two sides disagree. Getting this wrong produces the maddening case where both ends claim the same codec yet hear nothing, because one is sending payload type 96 and the other is listening for 111. Clock rate matters too: G.722 famously advertises an 8 kHz clock rate in SDP for historical reasons even though it is a wideband codec, and a system that takes that at face value gets the timing wrong.

## Where codecs go wrong

**No codec in common.** The most direct failure: a call connects but is silent because neither side will speak what the other offered, and nothing transcodes between them.

**Narrowband where wideband was expected.** A call routed through a narrowband codec sounds worse than the phones are capable of, often because a trunk's codec list forced G.711 or G.729 when both phones could have used G.722.

**Fax and modems over compressed codecs.** Fax tones and modem signals do not survive low-bit-rate speech codecs; they need G.711, which is why analogue adapter ports always use it.

**Licensing and reference code.** Some codecs are patented or have no freely redistributable reference implementation, which limits what an open-source project may ship.

## How BitVoice does it

BitVoice has its own media engine and converts between any two of its codecs when the parties differ, so a browser on Opus can talk to a carrier on G.729 or an old gateway on G.726, and when both sides share a codec the audio passes through untouched. In Settings you list the codecs phones may use, best first, and each trunk may carry its own list.

The codecs BitVoice converts between are **G.711** (µ-law and A-law), **G.722**, **Opus**, **G.729**, **G.726** at all four rates (16, 24, 32 and 40 kbit/s), **AMR** and **AMR-WB** (the mobile and VoLTE-interconnect codecs, AMR-WB being G.722.2), **iLBC**, **Speex** and **GSM**. **G.723.1**, found on very old video phones and gateways, is converted through **ffmpeg** when you switch that on in Settings, because there is no reference code BitVoice may ship for it.

Four more codecs are **carried untouched** between two parties that both use them, with their bit rate or mode kept in the offer so the far end knows how to decode: **G.722.1** (Siren 7), **G.722.1C** (Siren 14), **G.719** and **Codec2**. These appear on Polycom phones, conference phones, telepresence room systems and radio links. BitVoice forwards them as they are but does not transcode them, because there is no free reference code it may ship for G.722.1 and G.719.

For contact centres and conferencing, BitVoice mixes conferences in wideband (16 kHz and above) where the phones support it, so an HD conference does not drop to telephone quality because one leg is narrowband. Analogue adapter ports are always held to G.711, which faxes, modems and alarm panels need.

Choosing a codec in practice is mostly about the link and the endpoints. Between desk phones on a healthy office network, G.722 gives the best sound at no real cost. To a carrier, follow what the carrier offers, usually G.711, sometimes G.729 on thinner interconnects. To browsers and mobiles, Opus is the natural fit because it copes with the variable quality of home and cellular networks. On a constrained branch link, G.729 trades a little clarity for a great deal of bandwidth. The figure that matters when planning a network is that the codec's own bit rate is not the whole story: packet headers, and the overhead of sending 50 small packets a second, add roughly half as much again, so a 64 kbit/s G.711 call uses closer to 85 kbit/s on the wire each way once the IP, UDP and RTP headers are counted. BitVoice converts between any pair, so there is no need to force every endpoint onto one lowest common codec; each side can use what suits it.

| Codec | Sound | Bandwidth | Typical use |
|---|---|---|---|
| G.722 | HD (wideband) | 64 kbit/s | desk phones, the best choice between them |
| G.711 µ-law / A-law | telephone | 64 kbit/s | every carrier; North America/Japan and the rest of the world |
| Opus | HD, copes with loss | ~24 kbit/s | browsers and mobile apps |
| G.729 | telephone, a little rougher | 8 kbit/s | thin links, some carriers |
| G.726 (16-40 kbit/s) | telephone to rough | 16-40 kbit/s | gateways, DECT systems |
| AMR-WB / AMR | HD / mobile | 6.6-23.85 / 4.75-12.2 kbit/s | VoLTE interconnects, mobile networks |
| iLBC / Speex / GSM | telephone to rough | 13-15 kbit/s | older softphones and gateways |
| G.723.1 | rough (ffmpeg only) | 5.3 or 6.3 kbit/s | very old video phones |
| G.722.1 / G.722.1C / G.719 / Codec2 | carried untouched | varies | Polycom, room systems, radio links |

## Why BitVoice fits

Converting between codecs is normally a reason to run a heavy media server. BitVoice does it inside one Go binary, with Opus and G.729 the only two codecs that use pinned external Go libraries and the rest implemented in its own code. Everything is set in the web interface, including which codecs phones and trunks may use and whether G.723.1 and video conversion are switched on, and Settings says whether ffmpeg was found. Against Asterisk, which also transcodes widely, BitVoice's claim is that the same breadth of codecs is reachable from a single appliance with no modules to load, and that it is honest about which codecs it transcodes and which it only passes through.

## What has been tested, and what has not

**Checked against a reference:** GSM decoded sample for sample as ffmpeg does, G.726 streams exchanged with ffmpeg, iLBC against the RFC 3951 reference program, and MP3 hold music against minimp3. **Software-tested:** G.711, G.722, Opus, G.729, AMR and Speex through BitVoice's own round-trip tests, with Opus and G.729 going through their pinned Go libraries and Opus proven on real Android-app calls. The four pass-through codecs, G.722.1, G.722.1C, G.719 and Codec2, are software-tested: offered with their bit rate or mode, answered, and packets checked byte for byte both ways; they are not converted, and they have not yet been tried with a real Polycom or room system. G.723.1 conversion depends on ffmpeg being installed.

## Read more

- [The manual: Codecs and sound](/manual/#codecs)
- [RTP, SRTP and the media engine](/technology/rtp-media/)
- [Conferencing and video](/technology/conferencing-video/)
- [Fax and T.38](/technology/fax-t38/)
