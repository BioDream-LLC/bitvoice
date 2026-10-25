---
title: AI receptionists, transfer briefings and asking in plain words
tile: An AI receptionist that says it is automated, answers from facts you approved, and always reaches a person.
icon: spark
area: AI
description: BitVoice connects an OpenAI-compatible model for receptionists, transfer briefings, voicemail triage and plain-words configuration, with safety rules.
manual: ai
---

## What an AI receptionist is

A large language model is a program trained to produce fluent text, and in the last few years models have become good enough to hold a short spoken conversation: greet a caller, understand what they want, answer a factual question, take a message, and route the call. Wired into a phone system, that is an "AI receptionist", an automated first point of contact. The same models can also read a finished call's transcript and write a two-sentence summary, decide whether a voicemail sounds urgent, or turn a plain-English request ("forward sales to my mobile after five") into a configuration change.

The mechanism is an API. BitVoice talks to any service that speaks the OpenAI-compatible chat completions interface: OpenRouter by default, a commercial provider, or a model you run yourself on your own network. You switch it on, choose the service and model, and pick which features may use it. Until you do, no call data goes anywhere.

A concrete example: a small clinic has nobody to answer the phone at lunch. An AI receptionist greets the caller, says it is an automated assistant, answers "are you open on Saturday?" from facts the clinic approved, offers to take a message or book a callback, and, when the caller asks for a person, rings the front desk. It never pretends to be human and never traps the caller in a loop.

## Where AI on the phone goes wrong

The failure modes are well known and serious. A model can "hallucinate", state something confidently that is not true, which for a business answering customer questions is a liability and for anything safety-related is dangerous. A model can invert meaning, turning "no injuries" into "injuries", which is why generative summaries are risky where a denial matters. A receptionist that cannot reliably reach a human traps callers, the single worst experience a phone system can give. An assistant that does not disclose it is a machine erodes trust and, in some places, breaks the law. And a configuration assistant that applies changes directly can quietly break routing.

A phone system also has a hard constraint the chatbot world does not: it must never lose or hold up a call. A slow or failing model cannot be allowed to leave a caller in silence.

## How BitVoice does it

BitVoice uses a model only where you turn it on, and wraps each use in rules that come from holding the system to a 911 reliability bar.

**Receptionists that disclose, answer from approved facts, and always reach a person.** On the receptionist screen you give the assistant its facts; it answers from those, says it is automated, takes messages and books callbacks. The project rule is explicit: a set-up that would loop a caller back to an assistant is refused. A receptionist self-loop is rejected at the API level, not merely discouraged, so a caller asking for a human gets one.

**Transfer briefings.** When a call is transferred, BitVoice can hand the next person a two-sentence summary of who is calling and why, so the caller does not repeat themselves. The **Transfer briefings** chapter covers it. The briefing is built from the conversation; it informs the person taking the call, it does not decide anything.

**Voicemail triage and transcripts.** The model transcribes and summarises recorded calls and voicemails, lists action items (things someone agreed or was asked to do, each named to a person where the call says so), and can flag a voicemail that sounds urgent. For supervisors there is **call insight**: how the caller's mood moved and moments worth hearing, each tied to a quote. The prompts tell the model to use only what the transcript says and never invent names, numbers, dates or promises, and the insight is presented as evidence, never an automatic score.

**Ask in plain words.** You can type "forward sales to my mobile after 5 pm" and BitVoice shows the result as a proposed change you review and approve, not a change applied behind your back. The proposal runs through the same **Check every situation** validation as any edit, so a plain-words request that would create a dead end or a loop is caught before you confirm.

**Any OpenAI-compatible service, including one you run.** OpenRouter is the default, but any compatible endpoint works, and for a 911 centre its own government cloud counts as local. Requests carry a temperature of zero for steadiness and can be gated by a daily allowance, refused when the allowance is used up rather than running without limit.

```
Settings → AI → service: https://openrouter.ai/api/v1
                model:   your choice of OpenAI-compatible model
                used by: receptionist, summaries, insight, Ask (each opt-in)
```

**A failing model cannot hold a call.** The chat requests BitVoice makes carry a time limit, and the features built on the model are shaped so that a slow or broken service degrades to something safe rather than to silence. A receptionist that cannot get a reply falls through to a real destination; a summary or insight that fails does not appear at all, leaving the call record intact. This is the same discipline BitVoice applies to scripts and every other integration: the model is allowed to help, never to be the single point on which a call depends. Transcription for summaries can run on your own server through whisper, so the only thing that need leave the building is the text sent to the language model, and only for the features you switched on.

## Why BitVoice fits

BitVoice is one Go binary, and the AI connector is a thin HTTP client to a service you choose, so there is no bundled model, no GPU requirement for the phone system itself, and no lock-in to one vendor: point it at OpenRouter, at a commercial API, or at a model on your own network. The safety rules, disclosure, answer-from-facts, always-reach-a-person, review-before-apply, are enforced in BitVoice rather than left to the prompt, because the project treats a caller trapped by a bot as a failure of the same kind as a dropped call.

For the jobs where a generative model is the wrong tool, BitVoice offers a model-free alternative (see the related article), so you are not forced to send call contents to an LLM to get a usable summary. And speech to text can run entirely on your own server, so a receptionist or transcript pipeline can be built with nothing leaving the building except, if you choose it, the text sent to the language model.

## What has been tested, and what has not

From the maturity table, **AI transcripts, summaries and the receptionist** are **field-tested** against a real model through OpenRouter. The connector, the receptionist flow, voicemail triage, transfer briefings, call insight and Ask are exercised against a live model, and the self-loop refusal is tested at the API level.

What the project is careful about is the model's content, which it does not and cannot guarantee: a language model can still be wrong, and BitVoice's answer to that is to keep the model out of decisions that must not be wrong (it never routes emergency calls, never applies a change without review, and presents insight as evidence not a verdict), and to offer a model-free path where faithfulness matters more than fluency. Speech to text on your own server is field-tested on macOS with per-platform builds tested in continuous integration.

## Read more

- [The manual: AI receptionist, voicemail triage and asking](/manual/#ai)
- [Facts and summaries without a language model](/technology/facts-without-llm/)
- [Speech to text on the server itself](/technology/speech-to-text/)
- [Voicemail](/technology/voicemail/)
