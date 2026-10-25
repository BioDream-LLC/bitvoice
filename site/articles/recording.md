---
title: Call recording
tile: Recording calls with a payment-card pause, consent announcements by region, retention, legal holds and offsite archive.
icon: wave
area: Media
description: How BitVoice records calls, pauses for card numbers, announces consent by region, enforces retention and legal holds, and archives offsite.
manual: recording
---

Recording a call is technically simple: capture the audio of both sides and write it to a file. What makes recording hard is everything around the file. Card numbers must never be kept, because keeping them breaks payment-industry rules. People on the call often must be told, and the rule for telling them differs by country and even by US state. Recordings must be kept long enough to be useful and no longer, except when a lawyer asks for some to be preserved, in which case they must not be deleted at all. And a recording on a single server's disk is one disk failure from being lost. This article is about those four problems: the payment-card pause, consent, retention and legal holds, and offsite archive.

### How recording works, and the rules around it

A call recorder taps the audio streams of a call and stores them, usually as a stereo file with each side on its own channel so a listener can tell who said what. The four rules that shape a recorder are external.

The payment-card rule (PCI DSS) forbids storing card numbers in a recording. The practical answer is a pause: the person on the call pauses recording while the caller reads out a card number, and resumes afterwards, so the stretch with the number is absent from the file.

The consent rule varies. Some jurisdictions allow a call to be recorded when one party knows (one-party consent); others require every party to be told or to agree (all-party, or "two-party", consent). A business that calls across borders must apply the most protective rule that touches a given call.

The retention rule is about keeping recordings only as long as they are needed, which both saves disk and limits what can later be demanded or leaked, with a legal hold as the exception that overrides it.

Walk through one recorded call with a payment. A customer calls to pay an invoice. The agent answers, the call records. When the customer is ready to read their card number, the agent presses `*7`; the recording goes silent. The number is read, the agent presses `*8`, and recording resumes. In the call history, the paused stretch is marked, so an auditor can see exactly where and for how long recording stopped, and confirm no card number could have been captured.

## How BitVoice does it

Recording is set in **Settings**, **Call recording**: off, every call, or only the calls of extensions marked **Record calls**. Recordings play from **Call history**, and with AI on they are transcribed and summarised.

**The payment-card pause.** Before a caller reads a card number, the person on the extension presses `*7`, and `*8` resumes; **Pause recording** on **Live calls** does the same. The paused stretch is silent in the recording and listed in the call history, so an auditor can see where and for how long. Recording is captured in stereo, each side on its own channel.

**Consent.** In Settings you choose how people on a recorded call are told: **no notice**, **announce** (as the call is answered both sides hear a notice you record, and recording starts only after it has played), or **beep** (a short tone to both sides every fifteen seconds). **Rules** pick by the outside number, longest match first, so numbers in a California area code can announce while numbers in Germany are never recorded. Your own number counts too: the rules are matched against both the outside number and yours, and the most protective wins, in the order never record, announce, beep, no notice. So an office in a place that requires every party's consent announces whoever it calls, wherever they are, and each call's record says which rule applied and why.

**Retention.** Settings sets how many days recordings, call history and voicemail are kept; older ones are deleted within the hour.

**Legal holds.** On the **Legal holds** screen you add a hold with a name, a reason, the numbers it covers (extensions and outside numbers, or everything) and a date range. While a hold is on, retention leaves what it covers alone whatever its age, nobody can delete a held call, recording or voicemail (administrators included), and held calls show a lock in Call history. Releasing a hold makes what it kept subject to retention again; released holds stay listed with when and by whom, and adding and releasing holds is in the audit log.

**Offsite archive.** On the **Archive** screen you copy recordings, voicemail and the configuration to an Amazon S3 bucket (or any S3-compatible store) or an Azure Blob Storage container. Every ten minutes BitVoice copies new recordings and messages not yet in the archive, and once a day a dated copy of the configuration. BitVoice uses its own SigV4 and Entra code, with no cloud SDKs, and the access keys can be references to a key vault rather than the keys themselves. Deleting something in BitVoice does not delete the archive's copy, so you set the bucket's own lifecycle rules for how long the archive keeps things.

Who can do what is deliberate: administrators and viewers can play recordings and read transcripts, only administrators can transcribe or re-transcribe a call, and playing a recording or reading its transcript is noted in the audit log.

A recording is also more than one file's worth of material once AI is on. Each recorded call is captured in stereo, each side on its own channel, so when a transcript is made the two sides are written out separately and the transcript shows who said what and when. **Search what was said** then finds calls by words from their transcripts, summaries and action items, and a transcript is deleted together with its recording when retention or a hand removes it, so the search index never outlives the audio it came from. Coaching notes that a supervisor attaches to a recording travel with it as well, which means the same retention period and the same legal hold cover the notes, the transcript and the recording as one unit rather than as separate things that could fall out of step.

## Why BitVoice fits

Recording, the payment-card pause, consent, retention, legal holds and archive are all in the one Go binary and the one web interface. They are not add-ons: the pause is a feature code and a Live-calls button, consent is a set of rules in Settings, a legal hold is a screen, and archive is a screen with a copy-the-policy button that gives the exact S3 permission BitVoice needs. Because they share one engine, the retention and legal-hold rules that cover recordings cover voicemail and coaching notes too, and the consent rule that applied is written onto the call's own record.

The consent engine matching both the outside number and your own and taking the most protective rule is a real design choice for a business that calls across borders, rather than a single global on-or-off switch. The payment-card pause marks the gap in the history so an auditor can verify it, which is what PCI DSS actually asks for. The archive's own SigV4 and Entra implementations mean no Amazon or Microsoft SDK is pulled in, keeping to the single-binary, standard-library design. BitVoice is Apache-2.0 with no paid modules, so recording with its compliance features is in the free download. The manual is careful to add that none of this is legal advice: recording laws differ by country and state.

## What has been tested, and what has not

Recording, the payment-card pause, the consent announce/beep/rules engine, retention and legal holds run under BitVoice's own tests and the Chromium browser tests on every push, and the CI web job installs ffmpeg so recording playback is exercised end to end. A browser call has been recorded and transcribed through a stand-in AI in the suite.

**Archive to S3** and reading secrets from **AWS Secrets Manager** are **field-tested** against real AWS with BitVoice's own SigV4, as the maturity table states. **Azure Blob Storage** and **Azure Key Vault** are **software-tested** against stand-ins, not yet against real Azure. The AI transcripts and summaries that attach to recordings are field-tested against a real model through OpenRouter.

## Read more

- [The manual: Call recording and card payments](/manual/#recording)
- [Queues and the contact centre](/technology/queues/)
- [Hosting and operations](/technology/operations/)
- [Voicemail](/technology/voicemail/)
