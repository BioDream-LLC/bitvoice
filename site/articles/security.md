---
title: Security
tile: TLS, SRTP, fail-to-ban, a toll-fraud guard, two-step sign-in, single sign-on and an audit log, locked down by default.
icon: shield
area: Operations
description: How BitVoice protects a phone system: TLS and SRTP, fail-to-ban, a toll-fraud guard, two-step sign-in, single sign-on, an audit log and API tokens.
manual: security
---

## Why a phone system is a target

A phone system reachable from the internet is attacked within hours of going live. The reason is money. An ordinary web server that is broken into leaks data; a phone system that is broken into places calls, and calls cost money that someone else collects. Fraud rings run scanners across the internet all day, trying extension numbers and common passwords against every SIP port they find. When one answers, they dial premium-rate and international numbers that pay them a share of the call charge, often overnight and at weekends, and the owner learns about it from the next bill.

The attacks fall into a few shapes. Password guessing (brute force) throws millions of REGISTER attempts at a server until one extension's password gives way. Toll fraud follows: the stolen extension dials expensive destinations as fast as it can. Call floods (telephony denial of service) ring a number until nobody can get through. And caller ID spoofing lets a stranger pretend to be a number they do not own, which is how most robocalls and many scams start.

Defending against this is not one feature but a set of layers, each covering a different attack, with sensible defaults so that a system is safe the moment it starts rather than after a hardening checklist. A concrete example: a new extension needs a password. Left to a person, that password is often short and guessable; the first layer is that the system generates a strong one and will not let a phone register as an extension other than its own.

## Where it usually goes wrong

Most real breaches are not clever. They come from a default left in place: an admin account with a weak password, an extension whose SIP secret is the extension number, a management port open to the whole internet, a cloud security group that allows SIP from anywhere. Encryption is often skipped because it is fiddly, so passwords and call audio travel in clear text where anyone on the path can read them.

Toll fraud is often noticed too late because nothing watches spend as it happens. Call logs are kept, but nobody reads them until the bill arrives. And when a system does block attackers, it often tells them so with a clear rejection, which only helps them map what exists; silence is a better answer.

The harder problem is that a defence has to tell an attack from ordinary use. Blocking a country outright stops fraud to that country but also stops a salesperson calling a real customer there. The useful question is not "is this call allowed" but "is this call normal for this extension," and answering it means keeping a little history and reasoning about it, carefully, so that something the system never measured is never mistaken for something safe.

## How BitVoice does it

BitVoice is built to stand up to this by default, in layers, and every control has a screen. **Security** lists what is banned, what the judges decided and why.

Against password guessing, addresses that keep failing to sign in are banned for a while (**Settings → Protection**): ten failures in a minute ban the address for an hour, by default. **Trusted networks** are never banned, so the office is never locked out. SIP passwords are generated and at least twelve characters, one per extension, and a phone signing in as one extension can never register another. A banned scanner gets no answer at all, so it learns nothing about what exists.

Against fraud, extensions call outside numbers only when allowed, premium-rate numbers are refused unless a route names them, each extension may have at most four calls at once, and **Costs** raises an alarm on runaway spend: many calls to one country in an hour. The **toll-fraud guard** (**Settings → Toll-fraud guard**) judges every call out of the country before it is placed, by +, 00 or 011, and the places that share +1 with North America but are other countries, such as Jamaica, a classic toll-fraud destination. A call to a country this extension has called in the last ninety days goes through; a first call to a country is allowed but reported; a first call outside **Business hours**, or placed from a dial-in or a forward, is asked for a PIN or refused; too many calls abroad in ten minutes is refused; and a first call abroad while someone is guessing passwords is refused. An emergency number always goes through. **Countries always allowed** and per-extension **Exceptions** relax "first call" and "out of hours" but never excuse a burst or an attack, because a stolen password looks approved too.

These judges share a design. The toll-fraud guard, the trunk health judge, the call-quality diagnosis and the 911 caller-location check each follow a **rulebook**: facts that are measured, the conclusions they support, the exceptions that overrule a conclusion, and the choices. **How BitVoice decides**, on **Security**, shows each one. The rules they keep are worth stating because they matter for safety. Every answer comes with its reasons: what held, what was argued against it, and what overruled that. Something nobody measured is unknown, never "no"; when an unknown could change the answer, the judge takes its cautious choice and says what it could not measure. "Cannot determine" is a real answer. And each rulebook is checked in every combination of its facts before it is used, when BitVoice starts and in its tests, so that it never reaches two choices at once and every rule is reachable; the screen shows how many situations each was checked in. In practice this means that if the toll-fraud guard cannot read the call history, it does not assume a call is a first call and wave it through: it asks for the PIN.

Encryption uses TLS for SIP signalling on port 5061, so passwords and numbers are not readable on the network, and SRTP for the audio of calls; the browser phone and the mobile apps always use SRTP. The web interface is HTTPS only on port 8443, with BitVoice's own certificate or one you install. All of this cryptography comes from Go's standard library; BitVoice writes no cryptographic primitives of its own, which is the single most important rule for code that handles keys.

Access is divided into roles: administrators change everything, viewers see everything but change nothing with secrets hidden, and staff use the portal for their own calls only. **Two-step sign-in** with an authenticator app can be turned on for every administrator (**Administrators**). **Single sign-on** through any OpenID Connect provider (Microsoft Entra ID, Google, Okta, Keycloak) signs people in with their work account, so leaving the organisation removes access here too; group membership sets the role at every sign-in, and **Also allow passwords** can be turned off so a provider outage never locks you out, except on this computer. The **audit log** records every administrative action and who did it, including who played a recording or read a transcript, and it cannot be edited.

Programs use **API tokens** rather than a password: one per program, read-only or able to change everything, sent as `Authorization: Bearer bv_...`, shown once, and revocable at any time. A token's changes show in **Changes** and the audit log under its name. Secrets such as trunk passwords and LDAP reading accounts can be kept in **AWS Secrets Manager or Azure Key Vault** and referenced rather than stored in BitVoice, reached with BitVoice's own SigV4 and Entra token code and no cloud SDK. Spam and spoofed caller ID are handled on their own screen, from STIR/SHAKEN results, block lists and caller reputation.

## Why BitVoice fits

BitVoice is one Go program with no Asterisk or FreeSWITCH underneath, so there are fewer moving parts to misconfigure and one place to reason about. Everything described here is set in the web interface, including the bans list, the fraud guard's reasons and the audit log, so security is not a matter of editing files that only an expert can read. It is Apache-2.0 and free, with no paid security tier: two-step sign-in, single sign-on, SRTP, TLS and the fraud guard are all in the one binary. The cryptography is Go's standard library, not a reimplementation, which removes a whole class of mistakes. And the judges explain themselves: a refused international call, or a trunk moved behind the others, carries its reasons on the call's record, so an administrator can see why rather than guess.

## What has been tested, and what has not

The feature maturity table is honest about where each part stands. Full systems have been run on Amazon EC2 across the internet with SIP over TLS and SRTP, against Asterisk, Kamailio 5.7 and FreeSWITCH 1.11 as far ends, and a commercial carrier (SIP.US) over TLS with the carrier's certificate checked. The SIP, SDP, STUN and SRTP parsers are fuzzed on every push. Secrets from AWS Secrets Manager have been read from real AWS with BitVoice's own SigV4. Single sign-on through OpenID Connect and the directory sign-in are described below in the related articles; Azure Key Vault is so far tested against stand-ins rather than a real vault, as the maturity table states. The judges' rulebooks are checked in every combination of their facts by the tests. None of this makes a system secure on its own: the defaults help, but the public address, the firewall rules and the choice to turn on two-step sign-in are still the operator's.

## Read more

- [The manual: Security](/manual/#security)
- [Single sign-on and the company directory](/technology/directory-ldap/)
- [Running BitVoice: one program, installers and updates](/technology/operations/)
