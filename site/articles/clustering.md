---
title: Several servers as one phone system
tile: Run two or more servers on a shared PostgreSQL database so calls carry on when a server stops and a shared address moves to a running one.
icon: server
area: Operations
description: BitVoice clusters several servers over one PostgreSQL database: takeover after a crash or planned stop, trunks held one server at a time, SQLite or PostgreSQL.
manual: servers
---

## What a phone-system cluster is

A phone system is something people lean on. When it stops, calls in progress drop, phones go dead, and nobody can reach the business or be reached. For anything that matters, you want more than one server, so that if one fails another keeps the calls going. The difficulty is that a phone call is not a web request. A web request is short and stateless: any server can answer the next one, and a load balancer spreads them around. A call lasts minutes, has two ends sending audio to particular ports on a particular server, and has signalling state (who is registered, who is on hold, who is being transferred) that has to survive the loss of the server holding it.

The usual way to make several servers act as one is a shared database for the state they all need to see, a way for a surviving server to pick up the calls a failed one was carrying, and a stable address that phones and carriers reach regardless of which server is actually up. A concrete example: a call comes in on server A and is answered by a phone registered to server B. Both servers must agree that the call exists, B must ring the right phone, the audio must flow, and if A dies mid-call, B (or another server) must take over the carrier's half of the call within a few seconds without the phone even noticing.

Two ideas make this manageable. First, hold shared state in one place everyone reads, rather than trying to keep copies in step. Second, decide carefully who owns what: a carrier line that allows only one registration can be held by exactly one server at a time, with the others ready to take it.

## Where it usually goes wrong

Clustering is where phone systems earn their reputation for being hard. The classic failure is split brain: two servers both believe they are in charge, both register the carrier line, and calls bounce or double-bill. Another is the takeover that drops the call it was meant to save, because the surviving server does not know the media ports or the far end's address, or because it re-invites a phone whose leg was already local and the phone, confused, hangs up.

Addresses are a persistent trap. If phones point at one server by name, that server's failure takes the phones with it; a shared (floating) address solves this but needs the network to cooperate, and a DNS name with SRV records needs the records to actually list the running servers. Carrier lines that allow one registration are over-registered by every server at once and the carrier rejects all but one, unpredictably. And in the cloud, where each server has its own public address, the audio for a taken-over call tries to follow the old server's address and goes nowhere.

The deeper issue is that takeover has to be both fast and honest. Too eager and a brief network blip triggers a needless failover that itself drops calls; too cautious and a real crash leaves callers in silence for too long. Deciding when a server is really gone, without flapping, is the heart of the problem.

## How BitVoice does it

Two or more BitVoice servers that share one PostgreSQL database work as one phone system. A call reaches a phone whichever server it is registered with: ring groups, queues, transfers to an extension and calls from provider lines all ring phones on every server. Settings changed on one server apply on all within five seconds, and users, contacts, call history, recordings' transcripts and texts are shared. Busy lamps show phones on every server, parking slots are shared so a call parked on one is picked up from another, paging reaches every server, and texts reach desk phones wherever they are registered. A queue agent on a call on one server counts as busy on the others, and a provider line's channel limit counts calls on every server. The call record is written once, by the server that has the caller.

Setting it up is four steps, all in the web interface. Move the first server's data into PostgreSQL in **Settings → Database**. Start each other server with the same database (`bitvoice -database postgres://…`); it joins at once, and **Servers** lists it. On **Servers**, check where each server is reached on the network they share and give each a name; servers each reachable from the internet at an address of their own (cloud servers with public IPs) get **Its own public address** too, so when one takes over a call the carrier's audio follows it. Then tell phones how to find the servers, and set how each provider line is shared.

Phones that BitVoice provisions (Yealink, Poly, Grandstream) are given another running server as a backup automatically. For the rest, **Servers** offers two ways. **A DNS name for every server** uses a name with SRV records (`_sip._udp`, `_sip._tcp`, `_sips._tcp`) listing each server; the screen checks the records against the running servers and gives you the records to create, ready to copy. **A shared address** is an unused address on the servers' network: one running server holds it at a time and announces it with gratuitous ARP, and if it stops, the next takes the address within seconds and answers its phones at the same address without them registering again. The shared address needs Linux with the NET_ADMIN and NET_RAW permissions, and the servers on one network.

Provider lines are set per trunk under **Shared between servers**. **Each server uses it** has every server register and place its own calls, for providers that allow several registrations. **One server at a time** has one server register and carry the line while the others show it as **Standby, held by** that server and send their calls out through it; if it stops, the next registers within seconds. That one answer to the over-registration problem is why a line is never double-registered.

When a server stops, calls carry on. Each server keeps a note of its calls in the database; when one stops, a running server picks up each call within seconds, moves the audio to itself and tells both ends with a re-INVITE, with a gap of a few seconds in the sound. A planned stop, for an upgrade, hands calls over at once with no gap. A call whose phone is registered with the taking-over server is carried on there without disturbing that phone; only the other party is moved, which avoids the "confused phone hangs up" trap. A server that merely restarts still knows where its own phones were registered, because it keeps them in `registrations.json` in its data folder, so calls reach them before they register again. When a stopped server comes back it takes nothing back from the one that took over.

Between servers, the traffic is authenticated and can be encrypted: servers reach each other at their SIP port over TCP, or TLS when they have a certificate (each accepts only the certificate the other published, and the audio between them is then SRTP), calls between servers are signed with a secret shared in the database, and they are accepted only from a running member at its own address. There is a lab to try it before trusting it: `deploy/lab/run.sh` runs two servers, their database and test phones in Docker on one computer, each on its own address, and `run.sh failover` kills the server carrying a call through the shared address and checks the call carries on.

## SQLite or PostgreSQL

One server keeps everything in a single SQLite file, `bitvoice.db`, with nothing to install; back up the data folder and you have everything. PostgreSQL is for several servers sharing one set of data, or for reports in tools such as Power BI, Grafana or Metabase. Moving between them is done in **Settings → Database**: make an empty PostgreSQL database and an owner, enter its address, **Test**, then copy the data across in one go (all of it or none) and restart on it; the old file stays where it was, and moving back works the same way. A move is refused while calls are in progress. The database can also be named at startup with `-database` or `BITVOICE_DATABASE`. Recordings and voicemail stay as sound files in the data folder either way.

## Why BitVoice fits

It is the same one Go program, pointed at a shared database, with no Asterisk, no separate proxy and no clustering add-on to license. The whole cluster is set up and watched in the web interface: **Servers** shows each node, lets you name it, checks the DNS records for you and tells you which to create. There is no paid high-availability edition; clustering is in the free, Apache-2.0 binary. The "one server at a time" trunk mode is a direct answer to the over-registration problem rather than a workaround, and the shared-address takeover moves only the far end of a call, so phones are not disturbed. And there is a Docker lab that reproduces a crash and checks the call survives, so an operator can see the behaviour before relying on it.

## What has been tested, and what has not

Clustering is rated **Field-tested** in the maturity table. Two BitVoice servers sharing PostgreSQL have been run on Amazon EC2 across the internet: a provider line held by one server at a time, a call in on one server answered by a phone on the other, and the server carrying a call both killed with `kill -9` and stopped as for an upgrade. The call carried on in both cases, with about five seconds of silence after a kill and none after a planned stop; the provider line moved to the surviving server, and one call record was written. The shared-address takeover is exercised by the Docker lab's failover check. Capacity numbers are measured per single server (see the operations article); the cluster shares load by where phones and lines land rather than by spreading one call across servers. PostgreSQL is run under the race detector in CI on every push, alongside SQLite.

## Read more

- [The manual: Several servers as one phone system](/manual/#servers)
- [Running BitVoice: one program, installers and updates](/technology/operations/)
- [Security](/technology/security/)
