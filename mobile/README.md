# BitVoice mobile apps

`ios/` (Swift, SwiftUI, CallKit, PushKit) and `android/` (Kotlin, Telecom, Firebase Cloud Messaging). Both show
BitVoice's own phone page (`#/phone`) in a web view, signed in with the phone's pairing, and put its calls on the
phone's call screen. The manual's "Mobile apps" chapter covers pairing and push.

## Building

- iOS: `xcodegen generate` in `ios/` after editing `project.yml`, then open `BitVoice.xcodeproj` (Xcode 16 or later).
- Android: `./gradlew assembleDebug` in `android/` (JDK 17, Android SDK 35).

## Testing against a real server

`ios/BitVoiceUITests` drives the app as a person would. It needs a running BitVoice whose certificate the
Simulator trusts, and a pairing link from **Add a phone** (or `POST /api/extensions/{n}/pair`):

```sh
xcrun simctl keychain booted add-root-cert DATA/tls/cert.pem
TEST_RUNNER_BV_LINK='bitvoice://pair?...' TEST_RUNNER_BV_EXT=100 TEST_RUNNER_BV_RING_SECONDS=60 \
  xcodebuild test -project ios/BitVoice.xcodeproj -scheme BitVoice -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
```

With `BV_RING_SECONDS` set, place a call to the extension while the test waits (Health, **Call generator**):
it checks the call rings in the app, answers it, and hangs up. Without `BV_LINK` the test is skipped.

The Simulator has no call screen: CallKit ends incoming calls there at once, so on the Simulator the app
leaves calls in the phone page. The call screen, push wake-up and audio routes need a real phone.
