---
title: The outbound dialer
tile: Preview, progressive, power and predictive dialling, with answering-machine detection and do-not-call lists.
icon: phone
area: Contact centre
description: How BitVoice dials contact lists for a queue's agents, detects answering machines, limits abandoned calls and honours do-not-call lists.
manual: dialer
---

An outbound dialer calls a list of contacts and connects the ones who answer to a queue's agents: renewal reminders, appointment confirmations, collections, surveys. The reason a dialer exists rather than agents dialling by hand is arithmetic. A person who dials a number by hand spends most of their time listening to ringing, busy tones and answering machines; the share of time actually talking to a person is small. A dialer moves that waiting off the agent and onto the system, so agents spend their time on conversations. The trade is that a system dialling ahead of its agents can occasionally reach someone with nobody free to talk, and the whole craft of dialling is about managing that trade legally and politely.

### Four ways to dial

The four modes are a spectrum from cautious to aggressive. **Preview**: the agent asks for the next contact, sees its details, and decides whether to call it; nobody is dialled until the agent is ready. **Progressive**: one call is placed for each free agent, so there is always an agent waiting for each call. **Power**: a fixed number of calls (one to three) per free agent, betting that some will not answer. **Predictive**: the system adjusts the number of calls per agent on the fly, leaning in while few calls are abandoned and backing off as the abandon rate approaches the limit.

Walk through a progressive call. An agent becomes free. The dialer takes the next due contact from the list and places a call. The contact answers; the dialer listens to the first few seconds, decides this is a live person rather than a machine, and connects them to the waiting agent, who sees the contact's name and details on screen. Had nobody answered within the ring time, the dialer would move to the next contact and try this one again later.

### Where it usually goes wrong, and the law

Outbound dialling is the most regulated part of a phone system. In the United States the TCPA and the Telemarketing Sales Rule govern it; in the United Kingdom, Ofcom's rules on silent and abandoned calls; elsewhere, the local law. The two failures that draw regulators are abandoned calls (reaching someone with no agent free, so they get silence or a recorded message) above the allowed rate, and calling numbers that have opted out. A dialer that cannot bound its abandon rate, or cannot reliably suppress a do-not-call list, is a legal liability, not a convenience.

## How BitVoice does it

Each **campaign** on the **Outbound dialer** screen has its queue, its contact list and its dialling mode. You set the caller ID the contacts see, the hours calls may be made in a chosen time zone, how long a contact's phone rings, and how many tries and how far apart. A **list** is a CSV file with a header row and a column called `number` (or `phone`); `name` and other columns are shown to the agent in preview, and duplicate or malformed numbers are skipped. You record a **message** that plays to a person reached when no agent is free, and, if you choose, after an answering machine's greeting; it says who is calling and why, and offers "press 9 if you do not want these calls".

**People and machines.** BitVoice listens to the first seconds after an answer. A long, unbroken greeting is taken for an answering machine; a short "hello?" and a pause is taken for a person. A detected machine can be hung up on and retried, left the message, or passed to an agent. When in doubt, it treats the answer as a person, which is the safe default: it is better to put a person through than to abandon them.

**Abandoned calls.** A person reached when no agent frees up within two seconds hears the message and counts as abandoned. With power and predictive dialling, if the share of abandoned calls over the last hundred answers exceeds the campaign's limit (3% unless you change it, the US rule), the campaign falls back to one call per free agent until it is back under. The campaign screen shows its abandon rate, calls ringing and free agents live.

**Do-not-call.** No campaign calls a number on the **do-not-call list**, whatever list it arrived on, and anyone who presses 9 during the message is added at once. You add numbers by hand or through the API (`POST /api/dnc`). For a national registry, such as the FTC's National Do Not Call Registry in the US, you download the area codes you call as a flat file and import it: every line with a number counts, in the FTC's `212,5550100` form or one number a line, with headings and broken lines skipped and counted. With **Replace the last import** on, the previous download's numbers come off first so a number that left the registry becomes callable again, while numbers added by hand or by pressing 9 always stay. The US rules require the registry to be refreshed at least every thirty-one days, and the page counts the list and how much of it came from the registry. The import can also be driven over the API, with `POST /api/dnc/import?replace=1` and the file as the body.

**The campaign's working day.** A campaign calls only within the hours you set, in the time zone you choose, and stops calling when it is paused or when nothing is due. Each contact is tried the number of times you set, spaced by the interval you set, so a number that rang out is reached again later rather than hammered. Duplicate and malformed numbers in a list are skipped when it is loaded, and the campaign screen shows its abandon rate, the calls ringing and the free agents live, so a supervisor can watch the pacing and intervene. Because the dialer feeds a normal queue, a connected call is an ordinary inbound call to the agent, with the usual wrap-up time afterwards and a record in call history like any other.

The manual states the division of responsibility plainly: BitVoice gives you the tools (hours, do-not-call, abandon limits, caller ID, the message), and you remain responsible for consent, the lists you call and the rules where your contacts are.

## Why BitVoice fits

The dialer is part of the same engine as the queues, so it dials into a normal queue with normal agents, strategies and wrap-up, and a connected call looks like any other inbound call to the agent. There is no separate dialer product to license or integrate. All four modes, answering-machine detection, the abandon-rate governor and the do-not-call handling are in the one Go binary and configured in the web interface.

The design leans toward safety where the law is strict. Answering-machine detection errs toward "person" so real callers are not abandoned. The abandon-rate limit is a hard governor that forces the campaign back to one-per-agent dialling when the rate climbs, rather than a soft target. The do-not-call list is checked for every campaign regardless of which list a number came in on, and pressing 9 is honoured immediately. BitVoice is Apache-2.0 with no per-seat pricing, so the dialer scales with the contact centre at no licence cost, but the legal weight stays with the operator, as the manual is careful to say.

## What has been tested, and what has not

The dialer, its four modes, answering-machine detection, the abandon-rate governor, do-not-call handling and national-registry import run under BitVoice's own tests and the Chromium browser tests on every push. It sits on the queue engine, which is exercised the same way and imported from a real FreePBX backup.

The dialer has not been proven against a named carrier dialling campaign or measured on a live list; the capacity figures in the maturity table are for calls in general rather than a dialling campaign specifically. Answering-machine detection and the abandon-rate behaviour are tested against BitVoice's own call paths rather than against a statistically large sample of real answering machines, so expect to tune the detection window and the abandon limit for your own contacts and region before a live campaign.

## Read more

- [The manual: Outbound dialer](/manual/#dialer)
- [Queues and the contact centre](/technology/queues/)
- [How calls are routed](/technology/call-routing/)
- [Recording and payment-card pause](/technology/recording/)
