---
title: Voicemail
tile: Mailboxes with greetings, message-waiting lamps, email delivery, and transcripts and triage when AI is switched on.
icon: chat
area: Phone system
description: How BitVoice records voicemail, lights the message lamp, emails the recording, and transcribes and triages messages when AI is on.
manual: voicemail
---

Voicemail is the oldest piece of automation in the office: a caller who reaches nobody leaves a recording, and the person hears it later. The idea is simple, but the details decide whether voicemail helps or becomes a place messages go to die. How long can a message be? How does the person know one is waiting? Can they get it without being at their desk? And in a busy office, can they tell an urgent message from a routine one without listening to all of them first?

### How voicemail works

A mailbox belongs to an extension. When a call is not answered, the caller hears the mailbox's greeting and leaves a message, which is stored as an audio file. Two signals tell the owner a message is waiting: the message-waiting indicator (MWI), the lamp or icon on the desk phone, which the phone system turns on and off over SIP; and, increasingly, an email with the recording attached. The owner retrieves messages by dialling a feature code from their own phone, or from any phone by entering the mailbox number and a PIN.

Walk through one message. A caller rings an extension whose owner is in a meeting. The extension's "if nobody answers" is set to its mailbox, so after the ring time the caller hears the greeting, leaves a message and hangs up. BitVoice saves the recording, lights the phone's message lamp, and emails the message to the owner's address. When the owner gets back, they see the email, or dial `*97` and hear the message, and the lamp goes out once no new messages remain.

The message-waiting indicator deserves a note of its own, because it is the detail people notice when it is wrong. The lamp is not a property of the phone; it is a state the phone system pushes to the phone over SIP, with a NOTIFY message that says how many new and old messages are waiting. If the count is never pushed, the lamp never lights; if it is never cleared, the lamp stays on after the messages are heard, and people learn to ignore it. A system that gets this right turns the lamp on the instant a message lands and off the instant the last new one is heard, whichever phone or screen the owner used to hear it.

### Where it usually goes wrong

Voicemail fails quietly. A mailbox with no greeting leaves callers with a bare beep and no idea whose phone they reached. A message lamp that never clears, or never lights, trains people to ignore it. And the deeper problem in a busy office is volume: a sales line or a support line can fill with messages, and without a way to see at a glance which ones matter, the urgent message waits behind the routine ones.

## How BitVoice does it

On an extension you switch on **Voicemail**, which sets **If nobody answers** to the mailbox, and give it a PIN of four to ten digits. The same PIN opens the mailbox from another phone and signs the person in to the staff portal and at hot desks.

The settings are a short list. **Longest message** is 10 to 900 seconds (180 by default). **Email new messages** sends them to the extension's email address with the message attached. The **greeting** is recorded in the browser, uploaded, or recorded from the phone; without one, callers hear a beep.

People listen three ways. From their own phone they dial `*97` and messages play newest first, with keys to replay, delete or keep each one. From any other phone they dial `*97`, then the mailbox number and PIN. On the **Voicemail** screen (administrators) or in the staff portal (the person themselves) they play, mark heard and delete. The phone's message light is on while there are new messages. Messages under a legal hold cannot be deleted by anyone until the hold is released.

The staff portal is worth calling out, because it is where most people will actually read their messages. A person signs in to the portal with their extension number and the same voicemail PIN they use from a phone, and the Voicemail tab lists their messages with the transcript and summary shown when AI is on, a player, and buttons to delete. They can record or upload their greeting there too, without an administrator and without touching a phone's keypad. A portal session reaches only that person's own settings and messages, never anyone else's recordings or the system configuration, and repeated wrong sign-ins from one address are locked out for ten minutes.

**Sound clean-up**, set in Settings, removes steady background noise and evens out levels as messages are saved, so a message left from a car or a busy street is easier to hear.

The part that addresses the volume problem is AI triage. With **Transcribe and summarise voicemail** on (Settings, AI), each message is written out, summarised in a line, and marked urgent, normal or low priority with its kind: sales, support, billing. Urgent messages raise an alert on every administrator's screen. The transcript and summary also show on the staff portal and go into the customer timeline for that number, so the message sits with the rest of the history for that caller. When no AI model is set, BitVoice can still extract facts from a message by rule, without a language model, though that path was built and measured for 911-style calls rather than general voicemail.

The message-waiting lamp works across a cluster: with several servers sharing a database, the lamp and the messages follow the person wherever their phone is registered.

## Why BitVoice fits

Voicemail is part of the one binary and the one web interface. The greeting is recorded in the browser, the mailbox is a toggle on the extension, and the retention and legal-hold rules that cover recordings cover voicemail too, so a message a matter needs cannot be deleted until the hold is released. Because the transcript, summary and triage draw on the same AI and speech-to-text settings as the rest of the system, the same choice of a cloud model or a locally run whisper server applies: a message can be transcribed on the server itself without the audio leaving the building.

The triage is the practical difference from a plain mailbox. Marking each message urgent, normal or low, by kind, and raising an alert on urgent ones turns a growing list of recordings into something a busy team can act on. The feature code `*97`, the message lamp over SIP and the behaviour of email delivery match what people expect from Asterisk and FreePBX, and a FreePBX backup brings voicemail across with its PIN, email address and whether messages are sent attached. BitVoice is Apache-2.0 with no paid modules, so voicemail, email delivery and the AI triage are all in the free download.

## What has been tested, and what has not

The mailbox, greetings, the `*97` retrieval, the message-waiting lamp, email delivery, retention and legal holds run under BitVoice's own tests and the Chromium browser tests on every push, and voicemail with its PIN, email address and attachment setting imports from a real FreePBX 17.0.33 backup, which was tested whole.

The AI transcript, summary and triage are **field-tested** against a real model through OpenRouter, and speech-to-text on the server is **field-tested on macOS** with a whisper server BitVoice runs itself, with per-platform builds tried in CI. The rule-based fact extraction used when no model is set is **software-tested** on invented 911 calls only (23 of 43 facts, 53%, on the sealed set), never measured on real voicemail, as the maturity table states plainly; it is a 911 feature reused, not a voicemail summariser proven on office messages.

## Read more

- [The manual: Voicemail](/manual/#voicemail)
- [Speech to text on the server](/technology/speech-to-text/)
- [How calls are routed](/technology/call-routing/)
- [Menus, schedules and the front door](/technology/menus-schedules/)
