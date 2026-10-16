import XCTest

/// Drives the app as a person would, against a real BitVoice server. The server, a fresh pairing link and
/// what to expect come in through the environment (TEST_RUNNER_BV_LINK on the xcodebuild command line);
/// without them the test is skipped. See mobile/README.md.
final class PairingUITests: XCTestCase {
    func testPairAndRing() throws {
        let env = ProcessInfo.processInfo.environment
        guard let link = env["BV_LINK"], !link.isEmpty else { throw XCTSkip("no BV_LINK: no server to pair with") }
        let app = XCUIApplication()
        app.launch()
        if app.buttons["Extension"].exists == false, app.textFields["or paste the pairing link"].waitForExistence(timeout: 10) {
            let field = app.textFields["or paste the pairing link"]
            field.tap()
            field.typeText(link)
            app.buttons["Pair"].tap()
        }
        // Paired: the phone page of the extension loads in the app.
        XCTAssertTrue(app.staticTexts["Extension \(env["BV_EXT"] ?? "100")"].waitForExistence(timeout: 20), "not paired")
        XCTAssertFalse(app.staticTexts["This phone could not keep the pairing; it will be asked again next time."].exists, "the pairing was not kept")
        // The phone page has loaded inside the app: its own status line shows.
        XCTAssertTrue(app.webViews.staticTexts["BitVoice phone"].waitForExistence(timeout: 20), "the phone page did not load: \(app.debugDescription)")
        let shot = XCTAttachment(screenshot: app.screenshot()); shot.lifetime = .keepAlways; add(shot)
        // A call to the extension: BV_RING_SECONDS long, placed by the harness once the app is up.
        if let s = env["BV_RING_SECONDS"], let wait = Double(s) {
            let answer = app.webViews.buttons["Answer"]
            XCTAssertTrue(answer.waitForExistence(timeout: wait), "the call did not ring in the app")
            let ring = XCTAttachment(screenshot: app.screenshot()); ring.lifetime = .keepAlways; add(ring)
            answer.tap()
            XCTAssertTrue(app.webViews.buttons["Hang up"].waitForExistence(timeout: 10), "the call was not answered")
            let talk = XCTAttachment(screenshot: app.screenshot()); talk.lifetime = .keepAlways; add(talk)
            sleep(3)
            app.webViews.buttons["Hang up"].tap()
        }
    }
}
