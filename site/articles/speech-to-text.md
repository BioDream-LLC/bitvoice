---
title: Speech to text on the server itself
tile: BitVoice runs whisper on your own server, so transcripts, live 911 ones included, never leave the building.
icon: wave
area: AI
description: BitVoice ships whisper.cpp's whisper-server, fetches models pinned by SHA-256, and transcribes on your own server with no cloud fallback.
manual: ai
---

## What speech to text is, and why on-server matters

Speech to text, or automatic speech recognition, turns recorded or live audio into written words. For a phone system it is the foundation of call transcripts, voicemail transcription, searching across what was said, and, for an emergency centre, a live transcript of a 911 call as it happens. The modern approach uses a neural network model trained on large amounts of speech; OpenAI's Whisper is the best-known open model, and `whisper.cpp` is a widely used C++ implementation of it that runs the model efficiently on ordinary CPUs and on GPUs. It ships a small HTTP server, `whisper-server`, that accepts an audio file and returns the text.

The usual way to get transcription is to send the audio to a cloud service. That works, but for phone calls it means the contents of every call leave your building and land on a third party's servers. For a business that is a privacy and compliance decision; for a 911 centre it can be a non-starter. The alternative is to run the recognition yourself, on your own hardware, so the audio never leaves. The cost is that you now have a model to download and a process to keep running, which is exactly the part most self-hosted setups get wrong.

A concrete example: a voicemail arrives. BitVoice has the recording as a WAV. To write it down, it needs a transcriber. If that transcriber is a cloud API, the voicemail's audio is uploaded. If it is `whisper-server` on the same machine, the audio is posted to `127.0.0.1` and nothing leaves the computer.

## Where on-server speech to text goes wrong

Running a model locally sounds simple and is full of sharp edges. The model file is hundreds of megabytes and must be the right one; a truncated or wrong-version download produces quiet nonsense rather than an obvious error. The recognition process is a separate program that can crash, hang while loading a large model, or be killed by the operating system under memory pressure, and if the phone system is waiting on it synchronously, a fault in the model can take calls down. People often reach for a C library bound directly into the application, which drags in a C toolchain and ties the whole build to one platform. And the subtle failure is the "fallback": a system that quietly sends audio to the cloud when the local model is not ready defeats the entire reason for running it locally, and does so silently.

## How BitVoice does it

BitVoice ships `whisper-server` (whisper.cpp, MIT licence) **in its downloads**, builds of it for every platform alongside the main binary, and manages it as a sidecar. BitVoice itself stays pure Go: the recogniser runs as its own process bound to `127.0.0.1`, so a fault in it cannot take calls down, and no C library is linked into the phone engine.

**Finding the program.** BitVoice looks for `whisper-server` beside itself first, then in the installed location (`/usr/local/bin`, `/opt/bitvoice`, or `Program Files\BitVoice` on Windows), then on the PATH, and honours a `BITVOICE_WHISPER_SERVER` override. If it is not present, the status says so plainly rather than failing a transcript with a cryptic error.

**Models, fetched and pinned.** On the **Who writes speech down** screen you choose where transcription happens, a cloud model, a service on your network, or this server, and which model to run on this server. BitVoice offers five whisper models, smallest to largest: tiny, base (the default), small, medium, and large-v3-turbo. Each understands about a hundred languages; the larger ones are more accurate and slower. The model file is downloaded on first use into `DATA/whisper` and checked against a **SHA-256 hash pinned in the code** for the exact file Hugging Face publishes. A file that does not match, too large, wrong hash, is refused and not used, so a corrupt or substituted model never runs.

**Kept running.** A manager starts `whisper-server` on a random `127.0.0.1` port, waits for its `/health` endpoint (allowing up to three minutes, since a large model on a slow disk takes a while to load), and restarts it with exponential backoff if it exits, up to 30 seconds between tries, logging each restart. On Linux the child is tied to BitVoice's lifetime so it cannot be orphaned. The status (off, no-program, downloading, starting, running, failed) is shown in the interface and on the **Health** page.

**No silent fallback.** This is the rule that makes on-server transcription worth having. When you choose speech to text on this server, transcripts go to `whisper-server` here **and nowhere else**. If it is still downloading its model or starting, transcribing fails and says why, it never reaches out to an outside service to cover the gap. The code strips any cloud transcription key and model when local speech is selected, so there is no path to the cloud to fall back onto.

**Live 911 transcripts.** For the public-safety lab track, the same on-server transcriber produces live transcripts of a 911 call as it is spoken, shown on the call-taker's card, with the audio never leaving the computer.

```
POST http://127.0.0.1:<port>/inference   (multipart, field "file")
→ {"text": "..."}
```

The same client speaks to any OpenAI-compatible `/audio/transcriptions` endpoint too, if you would rather point at a service on your own network, but the default on-server path keeps everything local.

**Choosing a model is choosing a trade.** The five models are not better and worse versions of one thing; they are points on a speed-against-accuracy line, and the right one depends on your hardware and what the transcript is for. Tiny and base run comfortably on a modest CPU and are enough for clear speech and for searching call history by keyword; small and medium are more accurate on noisy lines and accents but want a faster machine; large-v3-turbo is the most accurate and expects a GPU or a sizeable server. Because the choice is on a screen rather than in a build, you can start with base, listen to a few transcripts, and move up if the line quality demands it, with BitVoice fetching the new model, verifying it, and switching over without a reinstall. The status line, off, no-program, downloading with a megabyte count, starting, running or failed, tells you where it stands at any moment, and the Health page surfaces a problem rather than letting it hide behind empty transcripts.

## Why BitVoice fits

BitVoice is one Go binary, and the transcriber ships beside it in the same download, so there is nothing extra to install and no account to open to transcribe calls. Because `whisper-server` runs as a separate process on loopback rather than as a linked library, the phone engine stays pure Go and portable, and a crash in the recogniser restarts without touching calls. The model is pinned by hash, so what runs is exactly what was published. And the no-fallback rule means that choosing local transcription is a real guarantee, not a best-effort default, which is the difference that matters to a clinic, a law firm or a 911 centre.

Compared with phone systems that treat transcription as "point at a cloud API", BitVoice makes the private option the shipped, managed, default-capable one: no language model account needed, no audio leaving the building, and an honest status when the local recogniser is not ready.

## What has been tested, and what has not

From the maturity table, speech to text on this server is **field-tested on macOS**: `whisper-server` 1.9.5 run by BitVoice, with the downloads for every platform built and tried in continuous integration, which runs the real-whisper test on each build. A dedicated workflow builds `whisper-server` for all six platforms and runs the transcription test against each.

What this means in practice: the mechanism, finding the program, fetching and verifying the model, keeping the server up, posting audio and getting text back, is proven end to end, and the per-platform builds are tested on each release. What has not been published is a transcription-accuracy benchmark on real call audio, which depends heavily on the model you pick, the codec and the line quality; the larger models trade speed for accuracy, and you choose the balance on the screen. For call transcripts and summaries through a cloud model, the AI path is separately **field-tested** against a real model through OpenRouter.

## Read more

- [The manual: AI receptionist, voicemail triage and asking](/manual/#ai)
- [AI receptionists, briefings and asking in plain words](/technology/ai-receptionist/)
- [Facts and summaries without a language model](/technology/facts-without-llm/)
- [Voicemail](/technology/voicemail/)
