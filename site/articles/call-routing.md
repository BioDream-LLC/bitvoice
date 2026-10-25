---
title: How calls are routed
tile: Where inbound calls go and which trunk outbound calls take, with a diagram, a simulator and a check that tries every situation.
icon: swap
area: Phone system
description: How BitVoice routes inbound and outbound calls by dial pattern, keeps 911 always available, and checks the plan for dead ends and loops.
manual: routes
---

Call routing is the set of rules that decides two separate questions. When a call arrives from a phone company, where does it go: which extension, menu, queue or voicemail box answers it? And when someone in the office dials an outside number, which phone line carries the call out? Every phone system answers both, and most of the trouble people have with a phone system comes from getting one of them subtly wrong.

Routing grew out of the old private branch exchange, where an operator plugged a caller's line into the right internal line by hand. On a modern IP system the operator is replaced by a table of rules. Inbound, the rule is matched on the number the caller dialled (the DID, the direct inward dialling number your provider sends you). Outbound, the rule is matched on the digits the person pressed, using a pattern language that goes back to the early digital exchanges.

### A pattern language for dialled digits

Outbound patterns describe what a dialled number looks like, one character at a time. A capital `X` stands for any digit, `Z` for 1 to 9, `N` for 2 to 9, and a set in brackets like `[1-5]` for one of a range. A dot at the end means "and one or more further digits". So `NXXNXXXXXX` matches any ten-digit North American number, `1NXXNXXXXXX` matches the same with a 1 in front, and `011.` matches an international call dialled from the United States. This notation is the same one Asterisk and most other systems use, so it is familiar to anyone who has configured a dial plan before.

Walk through one outbound call. Someone dials `9` for an outside line, then a ten-digit number. The first outbound route whose pattern matches is `9NXXNXXXXXX`. The route is told to remove one digit from the front (the `9`), leaving the real number, and to send it to a trunk. If that trunk is down or full, the next trunk in the route's list is tried. The caller never knows which line carried the call.

### Where it usually goes wrong

Three mistakes are common in real deployments. The first is pattern order: patterns are tried top to bottom, and a broad pattern placed above a specific one swallows calls the specific one was meant to catch. The second is digit manipulation: forgetting to strip the outside-line prefix, or forgetting to add a country code, so calls fail with a number the carrier cannot route. The third, and the most dangerous, is a routing plan that quietly has no path for an emergency call, because the one trunk that carries 911 is also the trunk that fills up first during a busy afternoon.

## How BitVoice does it

In BitVoice both halves live on one screen, **Call routing**, and the whole plan is drawn on **Call flow**.

**Incoming routes** each match a called number (or any number) and send the call to a destination: an extension, a ring group, a queue, a menu, a schedule, a script, a conference room, an AI receptionist or voicemail. Numbers match in any written form, so `+1 212 555 0100`, `2125550100` and `12125550100` are treated as the same number. A route for one specific number wins over a route for any number, and a route can be limited to a single trunk for the case where two providers both deliver the same number.

**Outgoing routes** match dialled digits by pattern and send them to trunks in order. **Remove** takes digits off the front, **Then add in front** puts digits on, and dragging routes changes the order in which patterns are tried. Premium-rate ranges such as 900 numbers are refused unless a route names them.

**Emergency numbers always work.** A route that names the emergency number exactly is used first; with none, every working trunk is tried in turn. A call to an emergency number is never refused for an extension's permissions, a hosted organisation's limits, or a missing route, and **Health** warns whenever no trunk can carry them. The manual suggests testing emergency routing with the provider's test number (933 in the US, which reads back the address the call carries) rather than dialling 911.

Several features sit on top of the basic table. **PIN sets** let an outbound route ask for a PIN before it calls out, to keep long-distance or international calling to people who have the code, or to bill calls to a client by their account code; emergency numbers never ask for a PIN, and forwarded calls and follow-me numbers are not asked either, only people dialling from a phone. **Day/night toggles** can sit in front of a schedule so staff override the opening hours by dialling a feature code.

### The things that make routing safe

Three tools set BitVoice's routing apart from a plain table of rules.

**Check every situation**, at the top of Call flow, tries the whole plan in every situation it can be in, not a sample. It walks every combination of open and closed that the schedules actually reach over the coming year (each holiday included, found in fifteen-minute steps), every combination of day/night toggles, every incoming route, every key a caller can press in every menu, every "if nobody answers" and queue timeout along the way, and every extension dialling every emergency number. It then lists what it finds, worst first: a call that goes nowhere, a loop, a queue with no agents, a menu or schedule no call reaches, a branch no situation ever takes, and emergency calls that rest on a single trunk. Each finding names the exact situation that shows it, such as `a call to +12125550100 → Press 2 (Billing) → No answer`. The same check runs on a proposed change before it is applied, so restoring an old revision or accepting a plain-language proposal shows what it would break that works today.

**Try a call** (the call-path simulator) traces one call without ringing anything. Enter who calls, what they dial and when, and BitVoice follows the call exactly as it would for real: the incoming route, the schedules at that time, routing scripts (which run, in their sandbox), menus, groups and queues. Green means it works, amber means look at it, red means the call fails there.

**Every call says how it was routed.** On each record in Call history, **How it was routed** lists in plain words and in order what decided where the call went: the incoming route, whether a schedule was open or closed and the time, a toggle's side, what a routing script chose, the keys pressed in a menu, who was away or had no phone connected, the outbound route and trunks used, and the toll-fraud guard's judgement.

Two judges watch the plan while it runs. The **toll-fraud guard** examines every call out of the country before it is placed, letting through a call to a country this extension already called in the last ninety days, reporting a first call, asking for a PIN on a first call out of hours or placed from outside, and refusing bursts or calls made while someone is guessing passwords. It never blocks an emergency number. The **trunk health judge** decides every fifteen seconds whether each trunk is being used in its place or should be tried after the others, moving a trunk behind the rest when its provider stops answering or its recent calls failed one after another, but never removing a trunk altogether, so an emergency call always has every way out. Each judge follows a rulebook that is checked in every combination of its facts, and something it has not measured is never taken for safe.

## Why BitVoice fits

BitVoice is one Go program. The routing table, the pattern matcher, the SIP stack that carries the calls and the media engine that connects the audio are all its own code, with no Asterisk or FreeSWITCH underneath. Everything on this page is configured in the web interface: there is no dial-plan file to edit, and a rule is a row on a screen, not a line of config syntax.

The three safety tools follow from that single design. Because BitVoice holds the whole plan in one place, it can walk every path and tell you, before you go live, where a call would end nowhere or loop. Asterisk's dial plan is powerful and well understood, but its correctness is checked by testing calls one at a time; BitVoice checks the plan exhaustively and keeps a plain-words trail on every finished call. The routing behaviour has been proven across the public internet on Amazon EC2, with a commercial carrier and with Asterisk playing both the carrier and a desk phone, so the matching, digit manipulation and failover are not only unit-tested.

BitVoice is Apache-2.0 licensed, with no paid modules and no per-extension pricing, so the full routing engine, the simulator and the exhaustive check are in the free download.

## What has been tested, and what has not

From the feature maturity table: SIP trunks and the routing that feeds them are **field-tested**. Registration, calls in and out, caller ID, busy and keypad digits (RFC 4733) were run against a commercial carrier (SIP.US, from an EC2 server) over UDP and over TLS with the carrier's certificate checked, reaching MOS 4.39 with no loss; calls in wait for the demo account's verification. The same path was run against Asterisk as the carrier across the internet, and against Kamailio and FreeSWITCH as the far end. Failover between two servers sharing one PostgreSQL database was proven with a `kill -9` and a planned stop on EC2: the call carries on and the provider line moves.

The routing engine itself (incoming and outgoing routes, pattern matching, the toll-fraud guard, the trunk health judge, the call-path simulator and the plan check) runs under BitVoice's own tests and browser tests on every push. The guards' rulebooks are checked in every combination of their facts at start-up and in the tests.

## Read more

- [The manual: Call routing](/manual/#routes)
- [SIP trunks](/technology/sip-trunks/)
- [Menus and schedules](/technology/menus-schedules/)
- [Queues and the contact centre](/technology/queues/)
