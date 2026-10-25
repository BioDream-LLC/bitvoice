---
title: Moving from FreePBX, Asterisk, 3CX and the rest
tile: Import a whole FreePBX backup, or 3CX, Cisco, Avaya and Mitel exports; see every change first and undo it.
icon: download
area: Operations
description: BitVoice imports FreePBX Backup & Restore backups, Asterisk configs, 3CX, Cisco, Avaya and Mitel, shows what changes, and lets you run beside the old system.
manual: import
---

## What migrating a phone system involves

A phone system holds years of accumulated configuration: every extension and its voicemail PIN, the trunks to the phone company, the outbound routes that decide which trunk a call leaves on and how the number is rewritten, the inbound routes that send a DID to the right place, the ring groups and the queues. Moving to a new engine means carrying that across without retyping it, and retyping is where mistakes and downtime come from. A good migration reads the old system's own files, turns them into the new system's objects, and, as importantly, tells you plainly what it could not bring over.

The source systems store their configuration in different ways. **FreePBX** keeps its state in a MySQL database and exports it as a Backup & Restore archive, a `.tar.gz` containing a manifest and SQL dumps. **Asterisk** underneath it uses text files, `pjsip.conf` and the older `sip.conf`. **3CX** exports users as a CSV. **Cisco Unified Communications Manager**, **Avaya IP Office and Communication Manager**, and **Mitel MiVoice Business** each export CSVs in their own shapes. A migration tool has to recognise each format and map its idea of an extension or a trunk onto the new system's.

A concrete example: a company runs FreePBX 17 with 60 extensions, three SIP trunks and a dozen outbound routes. Migrating by hand means re-entering 60 passwords. Migrating by import means dropping the backup file in and reviewing a list.

## Where migrations go wrong

The classic migration failures are losing data silently and guessing. A tool that imports extensions but quietly drops the ones it did not understand leaves you to discover the gaps when a phone does not ring. A tool that invents a value for something it could not read, a trunk's codec, a route's prefix, is worse, because the error is hidden inside working-looking configuration. Backups are also bigger than demos: a real FreePBX backup is tens of megabytes, and a tool whose upload or unpacking limits were set for a toy file falls over on the real thing. And the riskiest migration of all is the "big bang", cut everything over on a Friday and hope, with no way back and no way to run the old and new systems side by side while you gain confidence.

## How BitVoice does it

BitVoice's importer (`internal/importer`) reads the other systems' own formats and reports what it could not bring over instead of guessing.

**FreePBX, a whole Backup & Restore backup.** Drop the `.tar.gz` (FreePBX 13 through 17) on the **Import** screen and BitVoice reads the backup directly: extensions with their SIP passwords and voicemail PINs, PJSIP and chan_sip trunks, outbound routes (with the digits stripped and prepended per pattern), inbound routes and DIDs, ring groups, and queues with member penalties, all in one step. The formats come from FreePBX's own source for extensions, ring groups, queues, voicemail and the backup module. Current and legacy manifests are both handled, as is a plain `mysqldump`, and the Bulk Handler extension CSV. What cannot come over automatically, IVRs and time conditions, is listed in the preview rather than dropped without a word.

**Asterisk, 3CX, Cisco, Avaya and Mitel.** BitVoice reads `pjsip.conf` and `sip.conf` for Asterisk, the 3CX users CSV, and the CSV exports of Cisco Unified CM, Avaya IP Office, Avaya Communication Manager and Mitel MiVoice Business, turning each into BitVoice extensions, trunks and, where the export has them, groups, routes and queues.

**See every change first, and undo it.** This is the safeguard against silent loss and against the big-bang risk. Every import shows exactly what would change before anything is applied, a preview, and when you apply it, it becomes a revision you can restore from. If the import is wrong, you roll it back in one step. Feature codes land on the usual FreePBX values (`*72`, `*78`, `*8`) so muscle memory and documentation still work, and they can be changed.

```
Import → drop freepbx-backup-2026-….tar.gz
       → preview: 60 extensions, 3 trunks, 12 outbound routes, 4 ring groups, 2 queues
                  (IVRs and time conditions listed as not imported)
       → apply   → becomes a revision; "Go back" restores the state before it
```

**Run beside the old system.** You do not have to cut over all at once. Because BitVoice is a standard SIP system, you can point a few test phones or one DID at it while the old PBX keeps running, and move traffic across as you gain confidence. Tools written against Asterisk's AMI, ARI or AGI can drive BitVoice too, so integrations move with the calls.

**Mapping, not guessing.** The importer's design principle is that it turns what it understands into BitVoice objects and reports the rest, rather than inventing. A FreePBX outbound route, for instance, carries dial patterns with digits stripped from the front and prefixes prepended; BitVoice reads those rules per pattern rather than assuming a single rewrite, because getting the dialled digits wrong sends calls to the wrong place or nowhere. Extensions come across with their SIP secrets and voicemail PINs so phones re-register and mailboxes still open with the same codes. Queues bring their members and penalties. Where the source has something BitVoice does not map automatically, an IVR tree, a time condition, it is named in the preview as not imported, so you rebuild exactly those pieces in BitVoice's call flow and nothing is silently missing. The preview is the contract: what it lists is what you will get, and what it flags is what you still have to do by hand.

## Why BitVoice fits

BitVoice is one Go binary with no Asterisk or FreeSWITCH underneath, so the destination of a FreePBX or Asterisk migration is a simpler system: one file, one web interface, no configuration files to hand-edit and no paid modules. The importer reads the sources' own formats rather than asking you to produce an intermediate export, and the preview-and-revision model means a migration is reversible, which is what makes running beside the old system safe to attempt. The licence is Apache 2.0, so there is no per-extension cost waiting on the far side of the move, which is often the reason for leaving a licensed system in the first place.

Compared with a migration that is a spreadsheet and a week of retyping, BitVoice's import turns the move into a review: read the backup, check the preview, apply, keep the old system up until you are sure.

## What has been tested, and what has not

From the maturity table, **FreePBX migration (Import)** is **field-tested**: a real FreePBX 17.0.33 backup, 70 megabytes, was imported whole, and that test is what found the original upload and unpacking limits were too small for a real backup, now fixed by reading backups as a stream rather than into memory. Imports from FreePBX's other backup formats, and from Asterisk, 3CX, Cisco, Avaya and Mitel, are covered by BitVoice's own tests against the vendors' documented formats and fixture files.

So the FreePBX path, the most common migration, is proven on a genuine backup from a real installation, and the streaming fix means a production-sized backup works. The 3CX, Cisco, Avaya and Mitel importers are tested against their export formats but not yet against exports from a wide range of live installations; the honest position is that the formats are implemented and tested, and more real-world exports are welcome. Asterisk AMI, ARI and AGI compatibility, so your Asterisk-era tools keep working after the move, is checked against reference implementations, as the Asterisk compatibility article describes.

## Read more

- [The manual: Import](/manual/#import)
- [Asterisk compatibility: AGI, ARI, AMI and AudioSocket](/technology/asterisk-compat/)
- [Running it for real: operations](/technology/operations/)
- [Hosting other organisations and running a cluster](/technology/hosting/)
