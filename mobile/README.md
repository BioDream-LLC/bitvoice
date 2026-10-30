# BitVoice mobile apps

`ios/` (Swift, SwiftUI, CallKit, PushKit) and `android/` (Kotlin, Telecom, Firebase Cloud Messaging). Both show
BitVoice's own phone page (`#/phone`) in a web view, signed in with the phone's pairing, and put its calls on the
phone's call screen. The manual's "Mobile apps" chapter covers pairing and push.

## Building

- iOS: `xcodegen generate` in `ios/` after editing `project.yml`, then open `BitVoice.xcodeproj` (Xcode 16 or later). Publishing on the App Store: `ios/AppStore/README.md`.
- Android: `./gradlew assembleDebug` in `android/` (JDK 17, Android SDK 36). Releases for Google Play: `android/store/README.md`.

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

On Android, the debug app also trusts certificates installed on the device (`res/xml/network_security_config.xml`,
debug builds only), so an emulator can use a test server's own certificate; release builds trust the system's
authorities only. With the emulator running (`emulator -avd NAME`), as root on a Google APIs image:

```sh
H=$(openssl x509 -in DATA/tls/cert.pem -noout -subject_hash_old)
adb root && adb push DATA/tls/cert.pem /data/local/tmp/bv.pem
adb shell "mkdir -p /data/misc/user/0/cacerts-added && cp /data/local/tmp/bv.pem /data/misc/user/0/cacerts-added/$H.0"
adb install -r -g android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -a android.intent.action.VIEW -d "'bitvoice://pair?...'" llc.biodream.bitvoice
```

Tried on the Android 15 emulator: it pairs, a call to the extension rings in the app, Answer connects it
(Opus from the app, no loss), and it reopens paired. Push wake-up and the system call screen need a real phone.

Both apps accept only `https://` servers in a pairing link, so the device secret never travels unencrypted.
