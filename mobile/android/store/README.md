# Publishing the Android app on Google Play

Everything Play Console asks for, in the order it asks. The files beside this one are the graphics:
`icon-512.png`, `feature-1024x500.png` and `screenshots/` (Pixel 8, Android 16).

## 1. Build the bundle

```sh
cd mobile/android
BITVOICE_UPLOAD_KEY=~/keys/bitvoice-play-upload/upload.properties ./gradlew bundleRelease
# -> app/build/outputs/bundle/release/app-release.aab
```

The upload key is `~/keys/bitvoice-play-upload/upload.jks` (a copy is on the Mac mini, same path). Its
password is in `upload.properties` beside it. It is not in the repository. Choose **Play App Signing**
when Play offers it: Google then holds the key that signs what users install, and this one only proves
uploads are ours, so a lost upload key can be replaced (Play Console, App integrity) rather than ending
the app. Upload key certificate SHA-256:
`D0:24:61:06:30:0F:A0:50:1E:89:D5:A0:A9:4A:12:1F:EF:94:BB:22:41:B0:C9:B9:D4:30:D5:E9:1E:BD:D9:AD`.

Raise `versionCode` in `app/build.gradle.kts` (and `versionName`) for every upload after the first.

## 2. Store listing

- **App name** (30 at most): `BitVoice`
- **Short description** (80 at most): `Your BitVoice office extension on your phone: calls ring like phone calls.`
- **Full description**:

```
BitVoice puts your extension on the BitVoice phone system on your Android phone.

Calls to your extension ring on your phone with Answer and Decline, the way phone calls do. Answer, and the call keeps going when the screen turns off or you switch to another app. Call other extensions and outside numbers from the keypad, with your office number as the caller ID.

Getting started takes one scan: your administrator opens your extension in BitVoice and presses Add a phone, and you scan the code it shows. There is no password to type. Unpair at any time from the app.

- Calls are encrypted between your phone and your BitVoice (SRTP over DTLS).
- The app talks only to your organisation's BitVoice, and to Google to be woken for calls.
- No advertising, no analytics, no tracking.

This app needs a BitVoice phone system. BitVoice is a free, open-source phone system from BioDream LLC that organisations run themselves or have hosted: https://bitvoice.org
```

- **App category**: Business. **Tags**: VoIP, Business phone.
- **Contact details**: an email address is required and shown publicly. Use a BioDream support address
  (none is published yet; see "Still open" below). Website: `https://bitvoice.org`.
- **Privacy policy**: `https://bitvoice.org/privacy/`
- **Graphics**: app icon `icon-512.png`; feature graphic `feature-1024x500.png`; phone screenshots
  `screenshots/1-pair.png` to `5-ongoing.png`, in that order.

## 3. App content (Policy, App content)

- **App access**: "All or some functionality is restricted." The app does nothing until paired with a
  BitVoice server, so reviewers need one. See "Still open".
- **Ads**: no.
- **Content rating** (IARC questionnaire): category *Communication*; it lets users talk to other people
  (yes), shares the user's location (no), no purchases, no gambling, no violence. Expect *Everyone*.
- **Target audience**: 18 and over. Not designed for children.
- **News app**: no. **COVID-19 tracing**: no. **Government app**: no. **Financial features**: none.
- **Health apps**: no.
- **Data safety**:
  - Collects or shares data: **yes, collects**; shares: **no**.
  - Encrypted in transit: **yes**. Users can ask for deletion: **yes** (Unpair, or uninstall).
  - *Audio: voice or sound recordings*: collected, not shared, processed ephemerally (calls), required,
    for **App functionality**. (Recording, where the organisation turns it on, happens on its server.)
  - *Device or other IDs*: collected (the push token and the device secret), not shared, required, for
    **App functionality**.
  - *Personal info: name*: collected (the extension's name, from the server), for **App functionality**.
  - Nothing else: no location, contacts, photos, messages, browsing, app activity or diagnostics.
- **Permissions declarations** Play will ask for:
  - **Full-screen intent** (`USE_FULL_SCREEN_INTENT`): "Calling or video call app: shows incoming calls
    over the lock screen." Play grants it to calling apps on install.
  - **Foreground service types** `phoneCall` and `microphone`: "Keeps a call that the user answered
    connected, with its sound, while the phone is used for other things or the screen is off. Runs only
    while a call is connected; its notification shows the call with Hang up." Play asks for a short
    video: answer a call, press Home, pull down the notification shade. The screenshots `4-call.png` and
    `5-ongoing.png` show the same.
  - **Camera**: scanning the pairing code (and video, when chosen).

## 4. Release

Start on **Internal testing** (up to 100 testers by email, no review wait for later updates), then
**Production**. A new developer account must run a closed test with at least 12 testers for 14 days
before Production is allowed (Play's rule for personal accounts; organisation accounts are exempt).

## 5. Push wake-up (Firebase)

Without it the app rings only while it is on screen (tried on the Pixel 8: in the background, or with the
screen off, a call goes to voicemail). With it, BitVoice wakes the app for every call, even after days
closed. Publish to Production only with push working: the listing promises calls that ring. It needs a Firebase project in BioDream's name:

1. https://console.firebase.google.com, **Add project** `BitVoice` (Google Analytics: off).
2. **Add app**, Android, package `llc.biodream.bitvoice`. Skip the SDK steps.
3. Project settings, General, the Android app: note **App ID**, **Project ID**, **Web API key** and
   **Project number** (the sender ID). Put them in `~/keys/bitvoice-firebase.json`:
   `{"projectId": "...", "applicationId": "...", "apiKey": "...", "senderId": "..."}`.
   Build with `BITVOICE_FIREBASE=~/keys/bitvoice-firebase.json` as well; it goes in as `assets/firebase.json`.
4. Project settings, **Service accounts**, Generate new private key: save it as
   `~/keys/bitvoice-fcm-service-account.json`. This one is secret: it goes only to the push relay.
5. The push relay (`cmd/bvpush`) runs with `-fcm` pointing at that file, on a server of BioDream's with a
   real certificate; each BitVoice server is given its relay address, key ID and secret
   (Settings, Mobile apps).
