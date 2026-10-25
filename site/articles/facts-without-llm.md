---
title: Facts and summaries without a language model
tile: Rules read a call for what happened, where and who is hurt, every fact shown with the sentence it came from.
icon: check
area: AI
description: BitVoice extracts facts and writes call summaries with rules, not a language model, so a denial is never inverted; coverage is published honestly.
manual: ai
---

## What this is, and why not a language model

A language model can summarise a call fluently. The problem is that fluency and faithfulness are not the same thing. A generative model builds its answer from patterns, and under the wrong conditions it will rephrase "nobody is hurt" into "someone is hurt", invert a negation, or add a detail nobody said because the detail is plausible. For a marketing call that is an annoyance. For a 911 call, or any call where a denial or a correction carries weight, it is a defect that cannot be tolerated.

BitVoice's model-free approach is a different tool for that job. Instead of asking a model what the call was about, it reads the transcript with rules and word lists and extracts specific facts, the incident, the location, how many are hurt, the hazards, the vehicles, who the caller is to the person in need, and a callback number, and it attaches to every fact the sentence it came from and the side that said it. Nothing is added that no one said. It is faithful rather than complete: the rules find what they know and miss the rest, and they never guess.

This mirrors a deliberate decision made elsewhere in BioDream's work: a generative summariser was removed after a small model inverted a self-harm denial, reproducibly, and was replaced with rules that reuse only what was actually said, each fact carrying its source sentence.

A concrete example. Given this exchange:

```
callee: 911, where are you?
caller: It's 418 Maple Avenue, there's a fire in my kitchen.
callee: Is anyone hurt?
caller: No, nobody's hurt.
caller: Call me on 540-555-0142.
```

the rules produce: **"Fire at 418 Maple Avenue. Nobody hurt. Hazards: fire. Callback 540-555-0142."** The denial is kept as a fact of its own, not dropped and not inverted.

## Where this is hard

Reading meaning from text with rules is harder than it looks, because human speech on the phone is messy. A denial has to stop at the right clause: "he doesn't have a gun, but there are knives" is one denial and one hazard, so the rules split a sentence at commas, "but" and "and then" before deciding. A report of someone else's words ("my neighbour says her husband is not breathing") must be marked as reported, not stated as fact. A correction ("no, 42 Oak, not 24") has to replace what it corrects. A bare "no" or "yes" means something only in the context of the question asked right before it: "no" to "is he breathing?" is "not breathing", but "no" to "any trouble breathing?" is "breathing". Each of these is a rule, and the risk is writing rules that fit your examples so closely they fail on anything new.

## How BitVoice does it

The extractor (`internal/facts`) works on the transcript as a list of lines, each tagged with the side that said it. It splits lines into sentences, tracks the caller's sentences against the last question the other side asked, and applies ordered rules per kind of fact:

- **Incident**, most specific first: vehicle fire before fire, gas leak, hazmat, crash, domestic, burglary, assault, overdose, fall, suspicious person, medical. The first the caller's words match is the call's.
- **Location** keeps the first place given, numbered addresses, cross streets, highways with mile markers or exits, unless a later sentence corrects it.
- **Injured** combines named conditions, counts ("two people hurt") and denials ("nobody's hurt") into a number or "none".
- **Condition** reads states like not breathing, unconscious, bleeding, chest pain, trapped, and answers to yes/no questions about them.
- **Hazard** picks up fire, smoke, gas, knife, gun, chemical and fuel, dropping any that are negated, and adds a "yes" to "does he have a weapon?" when the weapon is named in the question.
- **Vehicle**, **relation** (neighbour, family, friend, coworker, or the caller themselves) and **callback** (with a "no wait, 0171" mending the last four digits) round it out.

The negation rule runs over clauses, not whole sentences, so a hazard in one clause survives a denial in another. The reported-speech rule flags passed-on facts. The summary writer then states only what the facts say, in short sentences, in a fixed order, and marks reported facts as reported.

This feeds after-call summaries, transfer briefings and voicemail summaries and triage. When no language model is configured, BitVoice uses facts for the summary rather than nothing; a model stays an option for those who choose it, and the receptionist and Ask features still need one.

### Two harder examples

Reported speech and a correction are the cases that trip generative summaries, so they are worth seeing. Given "27 Oak Street. My neighbour says her husband is not breathing", the rules record the location, mark the relation as neighbour, and keep the condition "not breathing" with its reported flag, so the summary reads "not breathing (reported)" rather than stating flatly that someone is not breathing. The distinction matters to a dispatcher: a caller who saw the patient and a caller passing on a third party's words are different kinds of evidence, and the rule preserves which one this is.

A correction is handled by order and by cue words. "No wait, 42 not 24" replaces the earlier address because the correction comes after it and begins with a correcting phrase ("no wait", "I mean", "sorry", "actually"). The same logic mends a callback number: "call me on 540-555-0142, no sorry, 0147" keeps the number but fixes its last four digits. Within a single sentence, the last value said wins, since a correction follows the thing it corrects. None of this requires understanding the sentence; it requires rules that know where a correction lives in speech.

## Why BitVoice fits

This runs in pure Go inside the one BitVoice binary, on the server, with no model to download, no GPU, and no account. For a business that will not or cannot send call contents to a cloud model, it is a usable summary that costs nothing and leaks nothing. For an emergency centre, it is a summary you can trust not to invent an injury or drop a denial, with every fact traceable to the sentence that produced it, which is exactly what a dispatcher needs beside a live transcript. The rules run in well under the time a cloud round-trip would take, so a live transcript can be turned into facts as the call unfolds.

The honesty is the point. BitVoice does not claim the rules read everything; it claims they do not lie, and it publishes how much they catch.

## What has been tested, and what has not

This is the one place the maturity table is bluntest, and deliberately so. Facts and summaries without a language model are **software-tested on invented 911 calls only**. The calls are split into three sets, each scored cold (with no rule written from it):

- **Training**, which the rules were written from: 41 of 41.
- **Hold-out**: 27 of 40 cold, then 40 of 40 after rules were written from its misses.
- **Sealed**, from which no rule is ever written: **23 of 43, about 53%**, cold, and 3 facts found that nobody expected.

The firm rule is that no rule is ever written from the sealed set, so the sealed number is an honest measure of what the approach catches on calls it has never seen. And the larger caveat, stated plainly: **this has never been measured on real calls.** Every corpus is hand-written, invented text, chosen to include denials, third-party reports, corrections and questions answered by "no". The approach is faithful, not complete, and the published 53% is what "faithful, not complete" costs on unseen invented calls. Before it is relied on anywhere real, it needs measuring on real audio, which is exactly what a design partner would provide.

## Read more

- [The manual: AI receptionist, voicemail triage and asking](/manual/#ai)
- [AI receptionists, briefings and asking in plain words](/technology/ai-receptionist/)
- [Speech to text on the server itself](/technology/speech-to-text/)
- [Voicemail](/technology/voicemail/)
