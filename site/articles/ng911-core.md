---
title: NG911 Core Services
tile: A lab-track core: ESRP routing, ECRF and LoST, LVF address validation, a BCF with STIR/SHAKEN and media checks, LIS, i3 logging and a forest guide.
icon: layers
area: Public safety
description: BitVoice's lab-track NG911 Core Services: ESRP, ECRF and LoST, LVF, a BCF with STIR/SHAKEN and media checks, LIS, i3 logging and NENA state events.
manual: core
---

This is the **lab and design-partner track.** NG911 Core Services are not certified for live 911 use, and they have not run against a live ESInet or another vendor's elements; they are tested against NENA's published definitions and sample data. They stay off the menu until **Public-safety profiles (lab)** is switched on in **Settings**. BioDream LLC is looking for a 911 authority willing to run them in a lab or in shadow mode.

## What NG911 Core Services are

A PSAP answers 911 calls, but something has to get each call to the right PSAP, decide the route by where the caller is, keep the attackers out, validate addresses, and keep the record. In Next Generation 911 that work is done by a set of network functions NENA calls the Core Services, built on internet standards and running on the ESInet, the emergency services IP network. A state or regional 911 authority runs them.

The pieces fit together like this. The ESRP (Emergency Services Routing Proxy) is the router: it takes each 911 call and sends it onward by policy. To decide where, it asks the ECRF (Emergency Call Routing Function), which answers a protocol called LoST (Location-to-Service Translation, RFC 5222): given the caller's location, which PSAP serves it. The LVF (Location Validation Function) answers the same kind of question ahead of time, validating a civic address against the authority's GIS data so carriers can fix a bad address before a 911 call ever uses it. The BCF (Border Control Function) sits at the edge, a session border controller specialised for 911: it keeps floods out, checks signed caller ID, and marks suspicious calls. The LIS (Location Information Server) answers HELD requests with a caller's location. A forest guide is the national directory that tells one authority's ECRF where another's is. And an i3 logging service keeps the signed log events every element produces, so the whole handling of a call can be reconstructed.

A concrete example: a wireless 911 call reaches the ESRP; the BCF has already checked it is not part of a flood and its caller ID signature is sound; the ESRP asks the ECRF for the PSAP serving the caller's coordinates, applies the authority's policy (fire calls here, overflow there), and routes the call, logging each decision.

## Where it usually goes wrong

The core is where a whole region's 911 either works or does not, so the failure modes are large. An ESRP that leaves a call with nowhere to go when every PSAP is busy, rather than sending it on anyway, is a disaster; so is an ECRF that loops forever when a query bounces between authorities. A BCF that refuses a 911 call because its STIR/SHAKEN signature failed throws away a real emergency, since most calls today are unsigned and a failed signature is far more often a quirk than an attack. A LIS left open to anyone becomes a tool for locating people who are not calling 911. And the standards are precise: each web service has a published definition, and an implementation that answers in nearly the right shape fails to interoperate with another vendor's elements.

The signing and logging raise their own problem. NENA's i3 asks for a particular signature algorithm (EdDSA with Ed448), and a core that cannot do exactly that has to be honest about what it does instead rather than claim conformance it does not have.

## How BitVoice does it

For a 911 authority that runs its own core, BitVoice answers LoST as the ECRF and routes each call as the ESRP. **Service areas** are your GIS boundaries exported as GeoJSON polygons (PSAP boundaries for `urn:service:sos`, and police, fire or EMS areas for the specific services), with a default PSAP for those without one, and **Try a place** shows which area answers a position or a city as a LoST query would. The **ECRF** answers `findService` at `/lost` (RFC 5222) from the addresses you list, a specific service answered from its own areas first then from sos, and a place no area covers passed to the **Forest guide** (RFC 5582), with a query that has already passed through this ECRF refused as a loop.

The **ESRP** has you list the PSAPs (each one's URI, the trunk that reaches it and its overflow) and choose a default, then send the ESInet's incoming calls to **ESRP** in **Call routing**. Each call goes to the PSAP for its location (from this server's areas, or an outside ECRF by LoST), to the overflow when that PSAP will not take it, and to the default without a location; each decision is in the i3 log. **Policy rules** are checked in order after the ECRF picks a PSAP, the first whose conditions hold deciding to send the call elsewhere or refuse it as busy, looking at the service, the chosen PSAP, a schedule, missing location, BCF suspicion and the incoming trunk.

The **BCF** checks calls from the ESInet first: addresses under **Always refuse** are turned away, and so is a source starting more calls a minute or holding more at once than you allow (a telephony denial-of-service flood), refused with 503 and Retry-After. **Let the call through marked suspicious** sends such calls on for a policy rule to decide, so a real caller in a flood is not lost. **Check STIR/SHAKEN signatures** checks the signed caller ID of every call that carries one against the STI roots, a failed signature sending the call through **marked suspicious** rather than refused, and **a 911 call is never refused for it**, with unsigned calls left alone. **Inspect media** checks every RTP packet of an ESInet call before it is used (wrong source address, a payload type the offer did not name, impossible header lengths, oversize, or past a packets-per-second ceiling), dropping and counting bad packets while never ending the call for its media, so the caller keeps the good packets.

The **LVF** takes your 911 GIS address data as GeoJSON in NENA's data model (NENA-STA-006): road centrelines and address points. A LoST query with `validateLocation="true"` then gets the address's valid, invalid and unchecked parts (RFC 5222), and **Check an address** does the same on screen, an exact point the best match and then a street segment whose range on the right side of the road holds the number, with "Did you mean" suggestions (a street a letter or two away, the same street in the next town, the nearest number the street has). The **LIS** answers HELD (RFC 5985) from a spreadsheet of location records keyed by telephone number, network address or MAC address, locating a device by the narrowest network that holds it, and it is answered only for the addresses you allow, so it is never open to anyone.

The **i3 logging service** takes signed log events (NENA-STA-010) from the core's other elements, each a JWS with the element's certificate in its header, by POST to `/i3log/LogEvents`, checks and keeps each one, and serves them back by ID, by time, by call and by incident. **PSAP Credentialing Agency roots** pasted into the settings make a signed event trusted only when its signer's certificate chains to one of them, shown with **PCA**, otherwise checked by signature alone and marked **not traced to the PCA**. The full set of **NENA i3 web services** runs at `/i3/<service>/v1`: a Policy Store whose signed routing policies the ESRP runs, discrepancy reports, bad actors (shared with the BCF), geocode and MSAG conversion, an Additional Data Repository, the service and agency locator, dequeue registration, and a test-call generator, each checked against NENA's published definitions. BitVoice also handles the NENA SIP state events: an ESRP subscribing to each PSAP's queue and service state (passing over a PSAP that cannot take calls, and never leaving a call with nowhere to go), and a notifier for this server's own element, service and queue state, whose bodies are checked against NENA's notify-body schemas.

On signing, NENA asks for EdDSA with Ed448. Go's standard library, which BitVoice uses for every cryptographic step, has Ed25519 but not Ed448, so EdDSA signatures are checked with Ed25519 keys, and ECDSA and RSA signatures are taken too; an Ed448 certificate is refused. That is stated plainly rather than papered over, because BitVoice writes no cryptography of its own.

## Why BitVoice fits

The whole core is the same one Go program, so an authority runs the ESRP, ECRF, LVF, BCF, LIS, forest guide and logging service as one binary rather than a rack of separate products, and all of it is configured in the web interface: the service-area GeoJSON, the PSAP list, the policy rules, the GIS data, the location records, the STI roots, the PCA roots. It is Apache-2.0 and free, where NG911 core products are normally large commercial systems. The safety rules are built in: the ESRP never leaves a call with nowhere to go, the ECRF refuses a loop, the BCF never refuses a 911 call for a failed signature, and media inspection drops bad packets without ending the call. And the conformance is checked against NENA's own published definitions and schemas rather than asserted.

## What has been tested, and what has not

The maturity table rates NG911 Core Services **Lab**: never on a live ESInet or against another vendor's elements, and not certified. Each web service answers as NENA's published OpenAPI definitions say, and the test suite checks the answers against those definitions and against NENA's notify-body schemas; the ECRF, LVF and geocoding are exercised against sample GIS data. The i3 logging service's answers are validated against NENA's OpenAPI definition but have not been tried with another vendor's elements, which the chapter states. The Ed448 limit is the honest consequence of the no-own-cryptography rule, with Ed25519 used instead. STIR/SHAKEN checking leans on the STI roots you provide. Nothing here is a claim of certification or of operation by any 911 authority; the invitation is to an authority willing to help prove it against a real core.

## Read more

- [The manual: NG911 Core Services](/manual/#core)
- [NG911 call handling (the 911 profile)](/technology/ng911/)
- [Text, TTY, real-time text and caller video to 911](/technology/text-tty-rtt/)
- [Security](/technology/security/)
