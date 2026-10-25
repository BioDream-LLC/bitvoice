---
title: Call scripts in JavaScript and Lua
tile: Write routing logic and whole calls in JavaScript or Lua, sandboxed, time-limited, never able to drop a call.
icon: code
area: Developers
description: BitVoice runs routing scripts and whole-call scripts in JavaScript (goja) and Lua (gopher-lua), sandboxed and time-limited with a fixed fallback.
manual: scripts
---

## What scripting a phone system means

Most of what a phone system does, where a call goes when it arrives, can be set with menus, schedules and ring groups. Some of it cannot. "Send VIP customers straight to their account manager, but only during that manager's shift, and if a storm warning is active route everyone to the emergency line" is a rule, not a menu. For rules like that, a phone system needs a small program.

There are two kinds of program, and they are different enough that BitVoice keeps them apart.

A **routing script** answers one question: where should this call go? It receives the call's facts, the number it came from and the name, the number dialled, whether it is inbound or internal, the trunk it arrived on, and the local date and time broken into weekday, hour and minute. It returns a destination, or nothing, in which case the next step in the call flow runs. It does not touch the call's audio; it decides and returns, in well under a second.

A **call script** runs the whole call. It plays prompts, speaks text, collects key presses, records a message, rings people, transfers, and hangs up, written top to bottom like ordinary code, each step waiting for its result before the next runs. This is what you reach for when the call is a conversation with the machine: a survey, a payment capture, a callback booking, a custom IVR.

BitVoice runs both in two languages: **JavaScript**, through the `goja` engine (a JavaScript interpreter written in Go), and **Lua**, through `gopher-lua`. Both run inside the single BitVoice binary, with no Node, no external interpreter and no runtime to install.

A routing script is the body of a function that receives the call:

```javascript
// Route storm-season VIPs to their manager during the manager's shift.
if (call.from === "+15405550142" && call.hour >= 9 && call.hour < 17) {
  log("VIP to account manager");
  return { type: "extension", target: "204" };
}
// No opinion: let the call flow continue.
```

In Lua the same shape holds: the call is a global table, and the chunk returns a table `{type=..., target=...}` or nothing.

## Where scripting goes wrong

The danger of letting code decide where calls go is that code has bugs, and a phone system cannot afford the usual failure modes. A script that loops forever would hold a caller on the line with nothing happening. A script that throws an exception, in a naive system, might drop the call. A script that can open files, make network connections or load other modules is a security hole and a reliability risk: it can hang on a slow server it reached out to, or leak data. And a script that a developer edits at 5 pm should not be able to take the phones down because of a typo.

The usual fixes are partial. Rate-limiting helps but does not stop a tight loop. Sandboxing helps but is easy to get wrong by leaving one dangerous built-in in place. The one rule that matters most for a phone system, and that scripting engines rarely enforce, is that a script must never be able to lose or hold up a call.

## How BitVoice does it

BitVoice enforces that rule in the engine, not in the script.

**The sandbox is real.** A routing script's runtime has no `require`, no host objects, no file, network, process or module access: only the language's own built-ins plus a `log` function. In Lua, BitVoice opens only the base, string, table and math libraries and then removes `dofile`, `loadfile`, `load`, `loadstring`, `require`, `module`, `print`, `collectgarbage` and `newproxy`, so there is no way to reach the disk or load more code.

**Everything is time-limited.** A routing script is stopped after 200 milliseconds; if it runs past that it is interrupted and the result is treated as "no opinion". A call script's own code may run at most 250 milliseconds between two steps, one call may take at most 1,000 steps, and the whole script stops after the time its owner allows (10 minutes by default). A loop cannot run away.

**The fallback is fixed.** When a script fails, is stopped, or ends without hanging up or handing the call on, the call goes to that script's configured fallback. This is the heart of the design: a call is never dropped by a script. A routing script that errors uses the fallback destination; a call script that dies mid-call sends the caller on rather than leaving them in silence.

**Emergency calls never reach a script.** A call to the emergency number is routed by the engine directly, so a scripting mistake can never stand between a caller and 911.

The call script host gives a small, deliberate set of verbs, each of which waits for its result and returns a clear reason when the caller hangs up or the call is handed on:

```javascript
say("Thanks for calling. Press 1 to book, 2 to cancel.");
const key = collect({ max: 1, timeout: 5 });
if (key === "1") {
  record({ box: "bookings", beep: true });
  say("Booked. Goodbye.");
  hangup();
} else {
  // Ring the front desk; if nobody answers, the script continues.
  const answered = dial({ type: "extension", target: "100" }, 20);
  if (!answered) say("Sorry, we missed you. Please call back.");
}
```

**You can try it in the editor.** On the **Scripts** screen, BitVoice compiles a script without running it and reports errors as single sentences with the line number counted in your own code, so a syntax mistake is caught before the script ever meets a caller.

**Two languages, the same guarantees.** Offering both JavaScript and Lua is a convenience, not a compromise: a team that already writes JavaScript can use it, and sites that prefer Lua, common in telephony, can use that instead, with identical sandbox, time limits and fallback behaviour behind both. The engines are chosen for fitting the single-binary rule: `goja` interprets JavaScript in pure Go, and `gopher-lua` is a pure-Go Lua virtual machine, so neither drags a C runtime into BitVoice. A routing script is the body of `route(call)`; a call script is a top-to-bottom program. The same `log` function is available in both for leaving a trail that shows up on the call's record, so when a script decides something surprising you can see why without attaching a debugger to a live call.

## Why BitVoice fits

BitVoice scripting lives inside one Go binary, configured and edited in the web interface, with no separate runtime to deploy or keep patched. The two engines, `goja` for JavaScript and `gopher-lua` for Lua, are two of the small number of pinned, pure-Go libraries BitVoice depends on, so scripts run wherever the binary runs, Linux, macOS or Windows, Alpine or distroless, with no C extensions.

Compared with Asterisk's AGI, where a script is an external program over a protocol, BitVoice's routing and call scripts run in-process and sandboxed, which is faster and safer for the common cases, while the separate AGI compatibility layer is still there for the external-program style when you want it. The guarantees, time limit, fixed fallback, emergency bypass, are the same guarantees BitVoice applies to every integration, because the project holds the whole system to a 911 reliability bar: a script is one more thing that must not be allowed to lose a call.

## What has been tested, and what has not

Scripts are part of BitVoice's own test suite, exercised the way the rest of the engine is: Go tests with the race detector, and browser tests that drive the **Scripts** editor. The sandbox limits, the time limits and the fixed-fallback behaviour are tested directly. The script engines are pinned, pure-Go libraries used unmodified.

What the maturity table does not claim is a large body of production scripts run at scale; the guarantees are verified, but your own scripts are your own to test. BitVoice makes that easier by letting you check a script in the editor and by running the whole call plan through the **Check every situation** tool, which tries the routing across combinations of hours, toggles, menu keys and no-answers and reports calls that end nowhere or loops, so a script wired into the flow is checked against dead ends before it goes live.

## Read more

- [The manual: Scripts (JavaScript and Lua)](/manual/#scripts)
- [Asterisk compatibility: AGI, ARI, AMI and AudioSocket](/technology/asterisk-compat/)
- [Call routing and the call flow](/technology/call-routing/)
- [The REST API and configuration as code](/technology/api-automation/)
