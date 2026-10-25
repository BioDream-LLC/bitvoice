---
title: Menus, schedules and the front door
tile: Auto-attendant menus, opening hours and holidays, day and night toggles, a dial-by-name directory, dial-in and hotel wake-up calls.
icon: book
area: Phone system
description: How BitVoice answers callers with auto-attendant menus, routes by opening hours and holidays, and offers a directory, DISA and wake-up calls.
manual: menus
---

Before a caller reaches a person, most phone systems put a small amount of automation in the way: a recorded greeting that offers choices, a rule that sends after-hours calls somewhere different, a way to spell a name on the keypad. These are the front door of a business, and they decide whether a caller feels looked after or trapped. This article covers that front door: the auto-attendant menu, the schedule that follows opening hours, the manual override, the dial-by-name directory, dial-in access, and the hotel wake-up call.

### The auto attendant

The auto attendant, or interactive voice response (IVR), is the oldest piece of phone automation still in daily use. It answers the call, plays a prompt, and routes the caller by the key they press: one for sales, two for support, zero for the operator. The idea is simple, and the usual failure is equally plain: menus that are too deep, with too many options, read too slowly, and no clear way to reach a person. Callers who get lost hang up, and a hung-up caller is a lost one.

Walk through one call. A number rings into the business. An incoming route points it at a schedule. The schedule checks the clock: it is a Tuesday at 10 a.m., inside opening hours, so the call goes to the main menu. The menu plays "For sales press 1, for support press 2, or stay on the line for reception". The caller presses 2, and the call is sent to the support queue. Had it been 8 p.m., the schedule would have sent the same call straight to voicemail.

## How BitVoice does it

Each of these pieces is a screen, and they chain together on **Call flow**.

**Menus** (the auto attendant) answer, play a prompt and route by key. On the **Menus** screen you add a menu, give it options (a key, a label, a destination), and record the prompt in the browser or upload a recording in any format, which is converted to telephone audio. You set the **timeout destination** for the caller who presses nothing, after the prompt has repeated the number of times you choose. A menu can also let callers dial an extension directly during the prompt, and a key can lead to a second menu for a sub-choice. The manual's advice is direct: keep menus to four options at most, the most common first, and always a way to a person. With AI switched on, a prompt can be written as text and read by the AI voice instead of recorded.

**Schedules** send calls one way when you are open and another when you are closed. Each line of hours is a set of days and a time from and to; several lines are allowed, hours may cross midnight for night shifts, and **holidays** count as closed all day whatever the hours say, with the holiday's name shown on Call flow when you trace a call on that date. You point an incoming route at the schedule, and the schedule at the menu for open and at voicemail for closed.

**Day/night toggles** are a manual override, what FreePBX calls call flow control. A toggle has a normal destination and a switched-over one; someone dials `*28` and the toggle's code on the way out, and calls follow the other destination until it is switched back. Either side of a toggle may itself be a schedule, so a toggle can sit in front of the opening hours and override them for an early close or a snow day. The toggle keeps its state across restarts, a busy-lamp key on a desk phone lights while it is switched over, and Call flow shows both sides and which one calls take now.

**The dial-by-name directory** lets a caller reach someone without knowing their number: they spell the first letters of the name on the keypad (2 for ABC, up to 9 for WXYZ) and are put through. On the **Dial-by-name directory** screen you choose whether callers spell the last name, the first name or either, and who is listed. One match is connected at once; several are read out, eight at a time. Record each person's **spoken name** on their extension and the directory says it, otherwise it reads out the extension number.

**Dial-in (DISA, direct inward system access)** lets someone call in from outside, prove who they are, and call out again through the office with the office's caller ID and rates. You set a PIN of four to ten digits, a list of allowed calling numbers, or both (one of the two is required), and choose the extension the outbound call is made as, so its permissions and routes apply. The manual is blunt that a dial-in is a door to your phone bill: three wrong PINs end the call, every attempt is logged in Security, and a long PIN with a caller-number list is the safe setup, because caller ID can be faked.

**Hotel wake-up calls** let anyone book a morning call by dialling `*62` and entering a time as four digits, or an administrator book one on the **Hotel** screen, once or every day. At the time the phone rings, and when answered it plays "Good morning. This is your wake-up call" in the guest's language; unanswered, it is retried (three times, five minutes apart by default) and can finally ring reception. With several servers, only one makes the wake-up calls. Guest rooms, room status dialled by housekeeping, and a link to a property management system (over FIAS or a REST API) sit on the same screen.

All of the spoken words these features use, apart from menu prompts and voicemail greetings, come from **Sounds**, where BitVoice holds every prompt in English, Spanish, French, German, Italian, Portuguese, Dutch and Polish, with other languages added as needed. A prompt missing in one language falls back to the default language, then to English, then to a short tone, so every feature works before a single prompt is recorded.

## Why BitVoice fits

The whole front door is configured in the web interface, and the pieces are designed to be seen together rather than scattered across files. Because a schedule, a toggle and a menu are all destinations on the same diagram, Call flow can draw the path a call takes through them, and the call-path simulator can trace one call through the clock, the toggles and the menu keys without ringing anything. A toggle's two sides and the side in force are both drawn, so the person who closes up early can see from a desk-phone lamp which way calls are going.

BitVoice is one Go program with its own SIP and media engine, no Asterisk underneath, and the feature codes match the usual FreePBX ones (`*28` for a toggle, `*62` for a wake-up call) so they are familiar and can be changed on one page. It is Apache-2.0 licensed with no paid modules, so the auto attendant, schedules, directory, DISA and hotel features are all in the free download rather than behind a commercial add-on.

## What has been tested, and what has not

The menu, schedule, toggle, directory and dial-in features run under BitVoice's own tests and the Chromium browser tests that drive every screen on each push. The call-path simulator and the exhaustive plan check walk schedules and menus as part of checking the whole routing plan.

The **hotel (FIAS)** integration is **software-tested** only: it runs against BitVoice's own tests, not yet against a real Oracle Opera or other property management system, as the maturity table states. The REST hotel API and wake-up booking are exercised the same way. Speech-to-text used to read prompts and the AI voice used to generate them are **field-tested** against a real model through OpenRouter and against a locally run whisper server, but the spoken-name directory and prompts in the eight built-in languages rest on BitVoice's own recordings and tests.

## Read more

- [The manual: Menus (auto attendant)](/manual/#menus)
- [How calls are routed](/technology/call-routing/)
- [Queues and the contact centre](/technology/queues/)
- [Voicemail](/technology/voicemail/)
