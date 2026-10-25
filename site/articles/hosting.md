---
title: Hosting other organisations, and a cluster of servers
tile: Run many phone systems on one server, each walled off, and run several servers as one for failover.
icon: server
area: Operations
description: BitVoice hosts many organisations on one server, each isolated with its own data, domain and limits, and clusters over PostgreSQL for failover.
manual: tenants
---

## What hosting and clustering are

Two different operators have two different needs that look alike from a distance.

A **hosted provider** wants to run phone systems for many customers on shared hardware: one server, many organisations, each with its own extensions, numbers, administrators and web address, none able to see any other. This is multi-tenancy. The hard part is isolation, one customer must never reach another's data or calls, and fairness, one busy customer must not starve the rest, with limits on how much of the machine each may use.

A single organisation that cannot afford downtime wants the opposite shape: one phone system, but several servers behind it, so that if a server fails, calls carry on and the phones keep working. This is clustering, or high availability. The hard part is shared state, every server has to agree on who is registered and which trunk is in use, and failover, a call in progress must survive a server dying.

BitVoice does both, and keeps them conceptually separate: tenancy divides one server among many organisations; clustering joins many servers into one phone system.

A concrete example of each: a reseller runs 40 small businesses on one virtual machine, each at `/t/acme/` or its own domain, each capped at 20 extensions. A hospital runs the same phone system on two servers sharing a database, so a server reboot during the night does not drop a single call.

## Where this goes wrong

Multi-tenancy fails on leaks and on starvation. A leak, one tenant seeing another's extensions, recordings or call history, is a breach, and it usually comes from sharing a data store without a hard wall between tenants. Starvation comes from no limits: one tenant's call storm, storage or AI spend consumes the machine and degrades everyone. And the subtle one for a phone system: whatever limits you impose, an emergency call must never be the thing that gets refused.

Clustering fails on split state and on mid-call failover. If two servers disagree about which one holds a provider's single registration, the carrier sees double and calls misroute. If a shared address does not move when a server dies, callers reach a dead box. And the test that separates real failover from the appearance of it is whether a call that is up when a server is killed actually continues, or merely that new calls find a surviving server.

## How BitVoice does it

**Hosting many organisations.** **Organisations** adds a tenant: it gets its own empty phone system, its own web address (`/t/name/` or its own domain) and its own administrators, and it cannot see any other. Isolation is at the data layer: on SQLite each organisation is its own folder, and on PostgreSQL its own schema, so there is a wall, not a shared table filtered by a column. Phones register to the organisation's domain or as `name.100`; a call arriving on a shared provider account reaches the organisation whose numbers it is for. Each tenant can be limited, extensions, calls at once, storage, AI use, and suspended without being deleted. The rule that overrides all of this: **emergency calls are never refused**, whatever a tenant's limits or suspension say.

**A cluster of servers.** Several servers run as one phone system over a shared **PostgreSQL** database (one server alone keeps everything in a SQLite file; PostgreSQL is what lets several share state). The servers agree through the database on registrations and on which one holds a trunk, so a provider line is held by **one server at a time**. Registrations are kept across restarts. A shared address moves to a running server, and each server in the cluster can have its own public address for its own network. When the server carrying a call stops for an upgrade, the call continues with no gap; when it is killed outright, the call carries on after a few seconds of silence while another server takes over, the provider line moves, and the call ends as a single record rather than two.

```
Several servers, one phone system:
  servers → PostgreSQL (shared state: registrations, trunk ownership)
          → a shared address that moves to a live server
          → one provider line, held by one server at a time
          → a call survives the loss of the server carrying it
```

**The support model.** BitVoice is Apache 2.0, free, with no paid modules and no per-extension or per-tenant meter, which is precisely what makes it suitable for a hosted provider: you are not paying a licence per seat you resell. BioDream LLC earns from hosting and support subscriptions only, so support is a service you can buy, not a gate in front of a feature.

**Choosing the store, and how limits bite.** The two jobs point at two database choices. A single server, whether it hosts one organisation or forty, can keep everything in a SQLite file, which means no database server to run or back up separately. The moment you want several servers acting as one phone system, you point them at one PostgreSQL database, because that is what lets them share registrations and trunk ownership. The choice is made in the interface, and BitVoice can move an installation from SQLite to PostgreSQL through Settings. Tenant limits are enforced where the resource is consumed, extensions when one is added, concurrent calls when a call is set up, storage as recordings accumulate, AI use against an allowance, and a suspended tenant stops taking new calls while its data stays in place for later. The one carve-out, written into the engine rather than left to policy, is that an emergency call is never the thing a limit or a suspension refuses.

## Why BitVoice fits

For a hosted provider, BitVoice is one binary that runs many organisations with hard data isolation, per-tenant limits, suspend-without-delete, and no licence cost multiplying with your customer count. For a single organisation, the same binary clusters over PostgreSQL with failover that keeps calls up through the loss of a server, all configured from the web interface with no cluster manager to install alongside it. Both are the same system under the two-mission design: a standalone phone system that must also meet a 911 reliability bar, which is why "emergency calls are never refused" is a rule even in a tenant that has hit its limits.

Compared with stitching together a multi-tenant layer and a separate high-availability layer on top of a traditional PBX, BitVoice builds tenancy into the data model and clustering into the engine, so the isolation wall and the shared-state agreement are part of the product rather than an integration you maintain.

## What has been tested, and what has not

From the maturity table, **two servers sharing one PostgreSQL with failover** is **field-tested**: on Amazon EC2, with a provider line held by one server at a time, a call arriving on one server was answered by a phone on another, and the server carrying the call was both killed with `kill -9` and stopped as for an upgrade. The call carried on, about five seconds of silence after a kill and none after a planned stop, the provider line moved, and the whole thing was recorded as one call. One-command install and restart-after-reboot are field-tested on Ubuntu, Debian, Amazon Linux, Windows Server and macOS. Capacity is field-tested up to 2,000 concurrent calls on a 16-vCPU server with no loss; run BitVoice's own call generator on your own hardware before going live.

**Hosting many organisations** is exercised by BitVoice's own tests, the isolation, per-tenant limits, suspension and the emergency-never-refused rule, but a large production fleet of tenants under real load has not been separately published as a field test. The database choices themselves, SQLite for one server and PostgreSQL for several, are tested on both in continuous integration with the race detector. So the clustering and failover claims rest on real cross-internet tests, and the tenancy claims rest on the test suite.

## Read more

- [The manual: Hosting other organisations](/manual/#tenants)
- [The manual: Several servers as one phone system](/manual/#servers)
- [Running it for real: operations](/technology/operations/)
- [Several servers as one phone system (clustering)](/technology/clustering/)
