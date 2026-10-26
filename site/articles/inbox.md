---
title: One inbox for web chat, WhatsApp, Messenger and texts
tile: One inbox for your website's chat box, WhatsApp, Facebook Messenger and business texting through ten SMS providers.
icon: chat
area: Contact centre
description: BitVoice brings web chat, WhatsApp Cloud API, Facebook Messenger and SMS from ten providers into one inbox beside calls.
manual: inbox
---

## What an omnichannel inbox is

Customers reach a business on whatever channel is in front of them: a chat box on the website, a WhatsApp message, a Facebook Messenger thread, a text to the company's number. From the business's side these are four different APIs with four different rules, and the natural result is four different tools, four logins, and conversations that fall through the gaps between them. An omnichannel inbox is the answer: one place where messages from every channel arrive, are answered, and are kept, beside the phone calls, so the person serving a customer sees the whole conversation regardless of how it came in.

The channels work differently underneath. **Web chat** is a widget you put on your own site; the browser and the server exchange messages directly. **WhatsApp** and **Messenger** both run on Meta's Graph API: Meta sends your server a webhook when a message arrives, and you call the API to reply, but only within rules Meta sets about when you may send free-form text. **SMS** is carried by a texting provider (a carrier or an aggregator), each with its own API for sending and its own callback format for receiving.

A concrete example: a customer messages a shop on WhatsApp about an order, then calls an hour later. In an omnichannel inbox the agent who answers the call already sees the WhatsApp thread, so the customer does not start over.

## Where messaging integrations go wrong

The sharp edges are mostly about trust and timing. Meta's webhooks must be verified: a webhook body that is not checked against the app secret can be forged, so anything acted on before verification is a security hole. Meta also enforces a 24-hour window: you may send free-form text only within 24 hours of the customer's last message, and after that you must use a pre-approved template, or the API refuses outright. Messenger echoes your own replies back to you as events, which naive code treats as new incoming messages and loops on. SMS providers each have their own idea of how a received message looks and which fields identify the number, so a single hard-coded parser works for one provider and breaks on the next. And a business usually wants more than one SMS number, sometimes from more than one provider, which an integration that assumes a single provider cannot do.

## How BitVoice does it

BitVoice brings all four into one **Inbox** screen, beside calls and the customer timeline.

**Web chat** is a widget served from BitVoice for your own site (`/webchat.js`), so a visitor can message you with no app and no number.

**WhatsApp and Messenger** run on Meta's Cloud API, and BitVoice verifies every webhook before believing anything in it: the body is checked against the app secret with an HMAC-SHA256 signature (`X-Hub-Signature-256`), and Meta's verification handshake is answered only when the verify token matches. Incoming WhatsApp messages are parsed for text, and button, interactive and media messages are shown for what they are (a picture, a voice note, a location named as such). **Templates** are supported for WhatsApp's out-of-window sending, so you can reach a customer after the 24-hour window with a pre-approved message; Messenger's human-agent tag allows replies for up to seven days. Messenger echoes of your own replies are filtered out so they are never treated as new messages, and BitVoice can ask Meta for a Messenger user's name to show on the thread.

**Business texting** goes through a provider interface so that any number of providers can run at once, and each of your numbers names the provider it belongs to. BitVoice speaks to **ten**: TextMagic, Twilio, SignalWire, Telnyx, Bandwidth, Vonage, Plivo, Flowroute, VoIP.ms, and your own webhook. Each provider's settings screen asks for exactly the fields that provider needs, its account identifier, key, and where required the account, application or space address, and tells you the callback address to set in the provider's portal so received texts come back to BitVoice. US senders get a guided 10DLC registration, and standard opt-out handling (reply STOP) is built in.

```
Your own webhook provider:
  BitVoice posts {"from","to","text"} as JSON, expects {"id"} back;
  post received texts to BitVoice the same way.
```

Because each number names its own provider, a business can run, say, Twilio for one country and Telnyx for another, or move numbers between providers, without the inbox caring.

**How a message reaches a person.** An incoming message, whatever the channel, lands in the Inbox as a conversation. Each conversation carries the sender's number or identity, the profile name where the channel supplies it (for Messenger, BitVoice can ask Meta for the user's first and last name), and the channel it came from, so an agent sees at a glance whether this is a WhatsApp thread, a website chat or a text. Non-text content is not dropped on the floor: a WhatsApp voice note, picture or location, or a Messenger attachment, is shown labelled for what it is, so the agent knows there is media to open even though the inbox itself handles text. Replies go back out through the same channel and the same number the customer used, so the thread stays coherent.

**The 24-hour window, handled rather than hit.** Meta allows free-form replies to WhatsApp and Messenger only for a time after the customer's last message, 24 hours. BitVoice respects that: within the window, text replies go through; outside it, WhatsApp needs a pre-approved template and Messenger needs the human-agent tag, which stretches the reply window to seven days. BitVoice supports both, so an agent coming back to a thread the next morning is not silently blocked, and the common surprise, "why won't my reply send?", is answered by the interface rather than by a raw API error.

## Why BitVoice fits

The inbox is part of the one BitVoice binary, configured entirely in the web interface, with no separate messaging server and no cloud SDK: the Meta and provider integrations are plain HTTP from BitVoice. Because the SMS layer is built around a provider interface rather than one hard-wired vendor, you are not tied to a single carrier and can run several at once, which matters for cost and for coverage across countries. The webhook verification and echo filtering are built in, so the common security and looping mistakes are handled rather than left to you.

Compared with bolting a separate live-chat product and a separate SMS tool onto a phone system, BitVoice keeps the conversation in one place, beside the call history and the customer timeline, so the person answering sees everything the customer has ever sent, by any channel, under one number.

## What has been tested, and what has not

From the maturity table, **texting, WhatsApp, Messenger and web chat** are **software-tested**: tested against stand-ins for each provider's and each channel's API, not yet against the live services with real accounts. The parsing, the signature verification, the echo filtering, the 24-hour and template behaviour and the ten provider integrations are exercised by BitVoice's own tests against those stand-ins.

What that leaves open is a run against the real Meta Cloud API and against each live SMS provider with a real account and a real number, which depends on opening those accounts. CRM caller cards, which pull a customer's record onto the agent's screen beside the conversation, are likewise software-tested against stand-ins. The capacity and reliability work in the README is about calls; messaging throughput has not been separately published.

## Read more

- [The manual: Inbox: web chat, WhatsApp and Messenger](/manual/#inbox)
- [The manual: Business texting (SMS)](/manual/#texts)
- [AI receptionists, briefings and asking in plain words](/technology/ai-receptionist/)
- [Running it for real: operations](/technology/operations/)
