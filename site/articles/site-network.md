---
title: A private phone network between your sites
tile: Join a BitVoice server in each office or state into one private network: staff dial each other's extensions, encrypted, with no phone company in between.
icon: globe
area: Operations
description: BitVoice servers at each site joined by encrypted trunks, directly or through a hub, so offices dial each other's extensions with no phone company.
manual: sitenetwork
---

## What a private network between sites is

A business with offices in several places usually pays a phone company to connect them, or lives with each office being its own island: to reach a colleague in another state you dial a full outside number, the call goes out over the public phone network and comes back in, and it is billed both ways. The old answer was a leased "tie line" between two phone systems, so staff could dial a four-digit extension in the other office as if it were down the hall.

With every site already on the internet, the tie line can be a connection between two phone servers. Each site keeps its own phone system, its own phones and its own voicemail, and the servers pass calls to each other directly, encrypted, with no carrier in the path and nothing to pay per call. Texas dials 3101 and an Ohio phone rings, showing "2101 Jane Smith".

There are two shapes. **Separate servers joined by trunks**: each site runs its own BitVoice and keeps working on its own when the links are down. **One cluster across sites**: a BitVoice server in each place, all sharing one database, so they act as one phone system, with each site's server using its own outside line.

## Where it usually goes wrong

Joining phone systems is easy to start and easy to get subtly wrong. Each server has to know which numbers belong to which site, and every number in the other office needs an incoming route of its own, which nobody keeps up to date. Caller ID is lost on the way, so the far end sees the trunk's number instead of the person. Calls from the other site are treated as strangers and caught by spam screening.

With more than two sites, a trunk from every site to every other one becomes a tangle, so a hub in the middle passes calls on, and then a mistake in two routes sends a call round in a circle, tying up lines until something gives way. 911 is the dangerous one: a site joined to others by private links still needs a real way to the emergency services, and a hub must never refuse an emergency call because a counter ran out.

Clusters spread over a country have their own trap: if every server registers the same carrier line, or sends its calls out of one office's line, a call from Oregon leaves through Virginia, with the wrong caller location and an outage in one place taking everyone down.

## How BitVoice does it

**Separate servers joined by trunks.** On each site's BitVoice, add a trunk to each other site with **Connects to** set to **Another of your phone systems**. A call in on such a trunk may dial this server's own extensions, ring groups, queues, conferences and menus with no incoming route for each, and it is not screened as spam. Give each site its own range of numbers (Texas 2xxx, Ohio 3xxx) and an outbound route for each other site's range. The caller's extension number and name go across. Use TLS with **Require encryption**, and calls between sites are refused unless both signalling and sound are encrypted.

**A hub.** With many sites, each site has one trunk to a hub, whose trunks are set to **Another of your phone systems, through this hub**: calls coming in may go out again, to another site or to the phone company. Every call between sites carries a hop count, and a routing loop ends after 10 hops with "Too Many Hops" instead of tying up lines. An emergency call is always passed on, even with no hops left.

**One cluster across sites.** Servers sharing one PostgreSQL database work as one phone system; a provider line can be marked **Used by** some servers only, so each site's server registers and sends calls out on its own line. A server with no line of its own sends a 911 call out through another server's line rather than failing it. If a server stops, another carries its calls on within seconds.

The call-path simulator shows each step, including "Dialled on Ohio", and every call record keeps the reason a call went where it did.

## Why BitVoice fits

It is the same free program at every site, set up entirely in the web interface: no separate SIP proxy, no paid multi-site edition, no per-call charge between your own offices. Each office keeps working when another one's link is down. Calls between sites stay on servers you run, encrypted on the way from one server to the other, and staff in every site see who is calling by name.

## What has been tested, and what has not

Both shapes are rated **Field-tested**. Three BitVoice servers in three Amazon regions (Virginia, Ohio and Oregon) were joined over the internet by TLS trunks with required SRTP and Let's Encrypt certificates. Extension calls ran between every pair of sites, 50 at once, with 0.004% packet loss and a MOS of 4.41, carrying the caller's number and name. A hub passed calls on through a third site; a deliberate routing loop was ended by the hop count; a site switched off made calls to it fail in under a second while the others carried on.

One cluster was then spread across the same three regions, with the database in Virginia over TLS. Each site's server sent its calls out on its own line; an emergency call from a server with no line of its own went out through another's. The server carrying a call was killed and another region carried the call on after 4 seconds, with 3.6 seconds without sound; a server cut off from the database for 30 seconds had its call carried on elsewhere with no gap.

The field test found and fixed four real bugs, among them a far end that ignored a moved call's new sound address. Not yet tried: real carriers at each site, and following a moved call's later requests over TCP or TLS (UDP follows; TCP and TLS keep the first address). A server whose database link goes quiet notices within about seven seconds and says so on Servers and Health (it used to take about a minute).

## Read more

- [The manual: A private network between sites](/manual/#sitenetwork)
- [Several servers as one phone system](/technology/clustering/)
- [SIP trunks](/technology/sip-trunks/)
