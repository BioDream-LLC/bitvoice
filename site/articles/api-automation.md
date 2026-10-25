---
title: The REST API, events and configuration as code
tile: Every screen is a REST call; live events, webhooks, tokens, Terraform and Git configure BitVoice from code.
icon: plug
area: Developers
description: BitVoice's REST API sits behind every screen, with live events, webhooks, on-behalf-of tokens, revisions, Git and a Terraform example.
manual: automation
---

## What "everything is an API" means

A phone system administered from a web interface is, underneath, a web application: the browser is a client that makes HTTP requests to a server. The question is whether those requests are a private, undocumented back channel or a first-class interface you can use yourself. BitVoice takes the second position as a project rule. Every screen in the web interface is drawn from the same REST API that is open to you, described at `/api/openapi.json` on any running server. There is no setting that can be changed in the browser but not through the API, and a test on every build walks the whole configuration and fails if any field is missing from the interface, which keeps the API and the screens in step.

That gives three ways to work, and they coexist. A telecom administrator uses the screens. A developer automates the same screens through REST. An operations team keeps the whole configuration in Git and applies it with a tool. None of them is a second-class path; they are views of one system.

Alongside the request-and-response API there are **live events**: a stream of what is happening, calls starting and ending, phones registering, trunks going up and down, so a dashboard or an integration can react in real time instead of polling. And there are **webhooks**, the inverse: BitVoice posts to an address you give when something happens, so your own systems hear about calls without holding a connection open.

## Where API-driven telephony goes wrong

The failures here are mostly about trust and drift. Tokens that never expire and can do anything become the weak point of the whole system; when a token leaks, you cannot tell what it did. Automation that writes configuration with no record of who changed what, and no way back, turns a small mistake into an outage with no undo. Configuration as code promises reproducibility but delivers "configuration drift" when someone makes a quick change in the GUI that the code does not know about, so the next apply either reverts it or conflicts. And an API that is merely the GUI's private channel breaks every time the front end changes, because it was never meant to be depended on.

## How BitVoice does it

**Tokens, shown once, scoped, and on-behalf-of.** API tokens are created on the API settings screen and shown once, then stored only as a hash. A token acts as an administrator, but it can also carry an **on-behalf-of** identity: a request made with such a token records the person it was made for, so that when a dispatcher's tool acts through BitVoice, the audit log and the call records name the dispatcher, not the token. Every administrative action, through the screens or the API, is written to an audit log.

**Live events and webhooks.** BitVoice emits events for the things a dashboard or integration needs, and a CRM or screen-pop tool can subscribe to them; the **CRM** screen wires caller cards and automatic call logging to any system with a web address. Webhooks post to your endpoint when calls happen.

**Every change is a revision.** This is the answer to drift and to the missing undo. Whether a change comes from the GUI, the API or an import, it becomes a revision you can see, compare and restore. The revision view shows exactly what changed; restoring asks you to confirm against a preview first. Imports are revisions too, so bringing in a FreePBX backup can be rolled back in one step. You can export the configuration and import it elsewhere.

**Configuration from Git.** The whole configuration can be kept in Git and applied, so your phone system's state lives in version control with the rest of your infrastructure, reviewed and rolled back the same way.

**Terraform and OpenTofu.** `deploy/terraform` configures BitVoice through its API using the generic REST provider, so you can declare extensions, trunks and routes as Terraform resources. Because BitVoice serves HTTPS only, the example offers a flag for its self-signed certificate during setup, or you can use a Let's Encrypt or your own certificate and point the provider at the trust store.

```hcl
# Declare a trunk through BitVoice's REST API (generic restapi provider).
resource "restapi_object" "carrier" {
  path = "/api/trunks"
  data = jsonencode({
    name        = "primary-carrier"
    host        = "sip.example.net"
    transport   = "tls"
    maxChannels = 30
  })
}
```

The OpenAPI description at `/api/openapi.json` is the contract an SDK or a client generator reads; it is the same description the browser's own calls follow.

**The three ways of working, on one surface.** Because the screens, the REST API and the engine are the same process and the same configuration, an administrator clicking in the browser, a script calling REST, and a pipeline applying Git or Terraform are all writing to one store with one history. A change made in the GUI appears as a revision that Git or Terraform can see; a change applied from code appears on the screens at once. There is no second source of truth to reconcile, which is the usual cause of the "someone changed it by hand" outage. For a team, that means the phone system can be reviewed in a pull request, rolled out through the same pipeline as the rest of the infrastructure, and still be fixed in the browser in an emergency, with the emergency fix recorded rather than lost.

## Why BitVoice fits

BitVoice is one Go binary with the web interface, the API and the engine in the same process, so there is no separate API server to deploy, authenticate against differently, or keep in sync with the UI. Because the project rule is that the GUI and the API are the same surface, the API does not lag the features: when a setting exists on a screen, it exists in the API, enforced by a test that fails the build otherwise. Cryptography for tokens and TLS comes only from Go's standard library; BitVoice writes none of its own. Cloud storage for recordings, voicemail and backups to S3 or Azure Blob is done with BitVoice's own SigV4 and Entra token code, not a cloud SDK, so the binary stays dependency-light and the same everywhere.

Compared with systems where automation means editing configuration files on a box and reloading a daemon, BitVoice's model is that the API is the system of record, every change is a reviewable revision, and Git or Terraform drive that same API rather than fighting a file the daemon also owns. That removes the drift problem: there is one place state lives, and it keeps its own history.

## What has been tested, and what has not

From the maturity table:

- **Machine images (AWS) and configuration as code** are **field-tested**: an EC2 image built from Ubuntu 24.04 starts as a new server each time, and `deploy/terraform` was run with OpenTofu 1.13 through create, change, no-drift and destroy. Azure images have not been tried.
- **Archiving to S3 and reading secrets from AWS Secrets Manager** are **field-tested** against real AWS with BitVoice's own SigV4. **Azure Blob and Azure Key Vault** are **software-tested** against stand-ins only.
- **Let's Encrypt** is **field-tested**: BitVoice's own ACME client issues and renews the public demo's certificate.
- **SNMP, Prometheus and OpenTelemetry** monitoring: SNMP is field-tested against net-snmp's `snmpwalk` and `snmptrapd`.

The REST API, events, tokens, revisions and the Git workflow are exercised by BitVoice's own Go and browser tests on every build, including the configuration-parity test that keeps the API and the screens aligned. What has not been published is a benchmark of the API under heavy concurrent automation; the call engine's capacity is measured separately in the README.

## Read more

- [The manual: Configuration as code](/manual/#automation)
- [Asterisk compatibility: AGI, ARI, AMI and AudioSocket](/technology/asterisk-compat/)
- [Routing scripts and whole-call scripts](/technology/call-scripts/)
- [Running it for real: operations](/technology/operations/)
