# Publishing the iOS app on the App Store

Everything App Store Connect asks for, in the order it asks. The files beside this one are the graphics:
`icon-1024.png` (the store icon) and `screenshots/` (iPhone 6.9", portrait).

It all needs an **Apple Developer Program** membership ($99/year) in BioDream's name. Without it the app
builds and runs in the Simulator but cannot be signed, pushed to, or submitted.

## 1. Signing and the project

1. Apple Developer, Certificates, Identifiers & Profiles:
   - **Identifier** (App ID) `llc.biodream.bitvoice`, with **Push Notifications** on.
   - A **Key** with **Apple Push Notifications service (APNs)** on; download the `.p8` once (it is shown
     only once). Note its **Key ID** and your **Team ID**. These are what BitVoice signs VoIP pushes with
     (Settings, Mobile apps, or the push relay); see section 5.
2. Put the Team ID into the build: in `mobile/ios/project.yml` set `DEVELOPMENT_TEAM` to it (or pass
   `DEVELOPMENT_TEAM=...` to `xcodebuild`), then `xcodegen generate`.
3. Set the release push environment: `BitVoice/BitVoice.entitlements` has `aps-environment` =
   `development` for building here. For a store or TestFlight build it must be `production`
   (the Makefile's release step sets it; see section 4). A production build can only be woken by APNs
   production, and a development build only by the sandbox, so BitVoice must point at the matching one
   (Settings, Mobile apps, "Apple sandbox").

## 2. Store listing

- **App name** (30 at most): `BitVoice`
- **Subtitle** (30 at most): `Your office extension`
- **Promotional text** (170 at most): `Calls to your BitVoice extension ring on your iPhone like phone
  calls, on the lock screen, even when the app is closed. One scan to pair, no password.`
- **Description**:

```
BitVoice puts your extension on the BitVoice phone system on your iPhone.

Calls to your extension ring on your iPhone's own call screen, the way phone calls do, even when the app is closed or the screen is off. Answer, and the call keeps going when you switch to another app. Call other extensions and outside numbers from the keypad, with your office number as the caller ID.

Getting started takes one scan: your administrator opens your extension in BitVoice and presses Add a phone, and you scan the code it shows. There is no password to type. Unpair at any time from the app.

- Calls are encrypted between your iPhone and your BitVoice (SRTP over DTLS).
- The app talks only to your organisation's BitVoice, and to Apple to be woken for calls.
- No advertising, no analytics, no tracking.

This app needs a BitVoice phone system. BitVoice is a free, open-source phone system from BioDream LLC that organisations run themselves or have hosted: https://bitvoice.org
```

- **Keywords** (100 at most, comma-separated): `VoIP,SIP,PBX,office phone,extension,business phone,softphone,CallKit`
- **Category**: Primary *Business*, Secondary *Utilities*.
- **Support URL**: `https://bitvoice.org`. **Marketing URL**: `https://bitvoice.org`.
- **Privacy policy URL**: `https://bitvoice.org/privacy/`
- **Graphics**: store icon `icon-1024.png` (no alpha, no rounding: Apple rounds it); screenshots
  `screenshots/1-pair.png` to `5-ongoing.png` for 6.9" (and 6.5" if asked; App Store Connect scales one
  set to the others). The icon in the app itself is `AppIcon` in the asset catalogue.

## 3. App privacy (App Store Connect, App privacy)

Mirrors the Android Data safety answers.

- **Data used to track you**: none.
- **Data linked to you**: none that leaves for Apple's definition of "linked"; see below. We **collect**,
  do **not** share, and do **not** use for tracking:
  - **Audio data** (voice, during calls): App Functionality; processed on the call path, not stored by the
    app. (Recording, where the organisation turns it on, happens on its server.)
  - **Device ID** (the APNs token and the device secret): App Functionality.
  - **Name** (the extension's name, from the server): App Functionality.
- Nothing else: no location, contacts, photos, browsing or usage data, no diagnostics.
- **Account deletion**: Unpair in the app, or delete the app. The app has no account of its own; a pairing
  is removed from the server by Unpair or by the administrator.

## 4. Build for TestFlight and the App Store

```sh
cd mobile/ios
# aps-environment must be production for a store or TestFlight build:
/usr/libexec/PlistBuddy -c "Set :aps-environment production" BitVoice/BitVoice.entitlements
DEVELOPMENT_TEAM=XXXXXXXXXX xcodegen generate
xcodebuild -project BitVoice.xcodeproj -scheme BitVoice -configuration Release \
  -archivePath build/BitVoice.xcarchive archive DEVELOPMENT_TEAM=XXXXXXXXXX
xcodebuild -exportArchive -archivePath build/BitVoice.xcarchive \
  -exportOptionsPlist ExportOptions.plist -exportPath build
```

`ExportOptions.plist` (beside this file) asks for App Store signing. The `.ipa` lands in `build/`; upload
it with Xcode's Organizer, Transporter, or `xcrun notarytool`/`altool` as Apple currently requires. Put the
entitlement back to `development` for Simulator work afterwards. Raise `MARKETING_VERSION` and
`CURRENT_PROJECT_VERSION` in `project.yml` for every upload after the first.

Start on **TestFlight** (internal testers need no review; external testers get a quick review), then
submit for App Store review. The reviewer needs a BitVoice to pair with: give a demo pairing link and
note in **App Review Information** that the app does nothing until paired (see "Still open").

## 5. Push wake-up (APNs)

Without it the app rings only while it is on screen. With it, BitVoice wakes the app for every call, even
after days closed, through a **VoIP push** (PushKit): iOS launches the app in the background and it reports
the call to the system at once. Submit to the App Store only with push working: the listing promises calls
that ring.

BitVoice signs the push itself (Settings, Mobile apps, "Own apps") or sends it to the push relay
(`cmd/bvpush`), with:

- **Team ID** and **Key ID** from section 1,
- the **Key (.p8)** file's contents,
- the **Topic** (the bundle ID `llc.biodream.bitvoice`; BitVoice adds `.voip`),
- **Apple sandbox** on for a development/TestFlight-from-Xcode build, off for an App Store build.

Keep the `.p8` secret: with the relay it lives only on BioDream's push server, never on a customer's
BitVoice. The same key serves every BitVoice; a customer running its own push gives its own key.

## Still open (needs the owner)

- Apple Developer Program membership ($99/year) in BioDream's name: the App ID, the APNs key, signing.
- A public support email for the listing (none published yet; the Android guide notes the same).
- A reviewer pairing link to a reachable BitVoice (the demo server can mint one from **Add a phone**).
- Screenshots are taken on a real device or a working Simulator (`xcodebuild -runFirstLaunch` once on this
  Mac to enable the Simulator); until then `screenshots/` holds the frames captured earlier.
