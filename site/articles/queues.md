---
title: Queues and the contact centre
tile: Call queues with distribution strategies, agent penalties, callbacks, supervisors, a live wallboard and call insight.
icon: queue
area: Contact centre
description: How BitVoice holds callers in queues, picks the next agent, handles callbacks and supervisors, and shows a live wallboard and call insight.
manual: queues
---

A queue holds callers in order, plays them music, and rings the next free agent when one becomes available. It is the heart of a contact centre, and it is useful in any office where calls arrive faster than people can answer them. The machinery behind it is older than the term "contact centre": automatic call distribution (ACD) was built into telephone exchanges to spread incoming calls across a pool of operators fairly, and the questions it answers have not changed. Who gets the next call? What does the caller hear while they wait? What happens when everyone is busy for too long?

### How a call is offered to an agent

The core of a queue is its distribution strategy, the rule that chooses which free agent rings next. Different strategies suit different teams. "Longest idle" spreads work evenly by ringing the agent who has waited longest since their last call. "Round robin" goes through the agents in turn. "Ring all" rings every free agent at once and the first to answer wins. "Fewest calls" rings whoever has taken the fewest today. "Linear" always starts from the top of the list, so the first agents are busiest, which suits a tiered team. "Random" and "weighted random" spread work by chance.

Walk through one caller. A call reaches the support queue. No agent is free, so the caller hears the queue's hold music and, every so often, their place in line and the expected wait. An agent finishes a call and enters wrap-up for a few seconds; when that ends they become available, the queue offers them the waiting caller, their phone rings, they answer, and the caller is connected. The wallboard shows the whole thing happening.

### Where it usually goes wrong

Queues go wrong in ways that are easy to miss until a busy day. A strategy that always rings the same few people burns them out while others sit idle. No wrap-up time means an agent is rung again the instant they hang up, with no chance to finish notes. No timeout means callers wait forever with no way out. And a queue with no agents logged in silently swallows every caller, which is why a good system flags that condition rather than leaving callers in silence.

## How BitVoice does it

The **Queues** screen is both the configuration and a live wallboard: who is waiting, for how long, and each agent's state.

**How calls are offered** is chosen per queue from the seven strategies above. **Penalties** rank agents from 0 to 100: calls go to the free agents with the lowest penalty, and an agent with a higher penalty is rung only when nobody lower is free, which makes a first line and an overflow line inside one queue. **Weight** ranks queues that share agents, so a higher-weighted queue's callers are taken first. **Wrap-up** gives an agent time after a call before the next. **Longest wait** sends callers who wait too long to the queue's timeout destination, with 0 meaning wait forever. **Hold music** is the queue's own, the system default, or the plain queue tone. **Tell waiting callers** announces their place in line and the expected wait (the average wait of answered callers today) every so many seconds, in the queue's language, using the queue prompts from **Sounds**.

**Agents** log in and out of all their queues by dialling `*45`, and pause and resume with `*46`; the wallboard has the same buttons and shows why an agent paused. The board shows each agent as available, ringing, busy, in wrap-up, paused (with the reason), away (from their calendar) or logged out.

The announcements to waiting callers are worth setting up with care, because they are most of what a caller experiences. The place-in-line and expected-wait message is spoken from the queue prompts in **Sounds**, in the queue's language, and until those prompts are recorded nothing is said, so a queue can go live silent by mistake. The expected wait is computed from real data, the average wait of answered callers so far today, rather than a fixed guess, so it tracks how busy the queue actually is. Hold music is chosen per queue, from the queue's own class, the system default, or the plain queue tone, and the manual's reminder applies: play only music you hold the rights to play to callers, because most recorded music needs a licence for on-hold use.

**Sticky routing** offers a returning caller (one answered in the last thirty days, or as many as you set) first to the agent who answered them last time, if that agent is free, before the queue goes on as usual. **VIP callers**, listed at the foot of the screen, go to the front of every queue they call.

**Callbacks** can be booked by callers or by staff, and raise an alert on every open screen so nobody is forgotten. **Supervisors** work from the **Live calls** screen: pressing **Supervise** rings the supervisor's phone, and when they answer they can **listen** (heard by nobody), **whisper** (heard by the agent only) or **barge** (heard by both), switching between the three during the call.

Around the queue sit the tools a contact centre runs on. A **customer timeline** gathers every call, recording, transcript, voicemail, text and note for one phone number. **CRM caller cards** show who is calling, how often they have called, any waiting callback and the summary of their last transcribed call, from any system with a web address, while the call rings; call logging posts each call to the CRM as it ends. **Call insight** gives supervisors evidence about how a call went, measured from the recording's sound: how much each side talked, their longest stretches, how often they talked over each other, and the quiet. With AI on, it adds a reading of how the caller seemed and flags up to six moments (went well, look at, a question not answered, something promised), each with the time and the words, and **coaching notes** let a supervisor write their own notes against a moment. Metrics for a wallboard or an external dashboard flow out over Prometheus, OpenTelemetry and SNMP, and each queue can be watched there: callers waiting, agents in each queue, calls answered and abandoned.

The **outbound dialer** turns a queue around to make calls to a contact list, which is covered in its own article.

## Why BitVoice fits

The whole contact centre is in the one binary and the one web interface. The queue, the wallboard, callbacks, supervisor controls, the customer timeline, CRM cards and call insight are not separate products bolted together; they are screens over the same engine, so a caller card appears on the same Live calls screen where a supervisor presses Barge, and the customer timeline draws on the same transcripts the call insight reads.

Call insight is deliberately evidence rather than a score: the talk ratios and silences are measured from the audio, and the AI reading flags moments to listen to rather than ranking people. That is a design choice about what a supervisor should be given. The strategies, penalties and positions cover what Asterisk's queues offer and are imported directly from a FreePBX backup, members and penalties included. BitVoice is Apache-2.0 with no per-agent or per-position pricing, so a fifty-seat contact centre costs the same licence as a two-phone office: nothing.

## What has been tested, and what has not

The queue engine, strategies, penalties, weights, sticky routing, VIP routing, agent states, supervisor listen/whisper/barge and the wallboard run under BitVoice's own tests and the Chromium browser tests on every push, including a browser call recorded and transcribed through a stand-in AI. Queues and their penalties import from a real FreePBX 17.0.33 backup, which was tested whole.

**AI transcripts, summaries and the receptionist** are **field-tested** against a real model through OpenRouter, so the transcript-based parts of call insight rest on a real model; the talk-ratio and silence measurements are computed from the audio by BitVoice's own code. **CRM caller cards** are **software-tested** against stand-ins for the external system, not yet against a named commercial CRM. Metrics have been checked against real tools: Prometheus scraping and SNMP against net-snmp's walk and trap daemon.

## Read more

- [The manual: Queues](/manual/#queues)
- [Outbound dialer](/technology/outbound-dialer/)
- [How calls are routed](/technology/call-routing/)
- [Recording and payment-card pause](/technology/recording/)
