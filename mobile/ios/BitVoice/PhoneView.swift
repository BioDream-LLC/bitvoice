import SwiftUI
import WebKit

/// The phone page (BitVoice's #/phone) in a web view, signed in with this device's credentials, with
/// the bridge of web/src/native.ts: the page posts its calls here, and Calls runs commands in it.
struct PhoneView: UIViewRepresentable {
    let pairing: Pairing

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> WKWebView {
        let cfg = WKWebViewConfiguration()
        cfg.allowsInlineMediaPlayback = true
        cfg.mediaTypesRequiringUserActionForPlayback = []
        let ctl = cfg.userContentController
        ctl.add(context.coordinator, name: "bitvoice")
        // The credentials go in through a script, never in the address, so they stay out of history and logs.
        let creds = try? JSONSerialization.data(withJSONObject: ["device": pairing.device, "secret": pairing.secret])
        let json = String(data: creds ?? Data("{}".utf8), encoding: .utf8) ?? "{}"
        let boot = """
        window.bitvoiceNative = Object.assign(\(json), { platform: 'ios', post: function (m) { window.webkit.messageHandlers.bitvoice.postMessage(m) } });
        """
        ctl.addUserScript(WKUserScript(source: boot, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        let view = WKWebView(frame: .zero, configuration: cfg)
        view.uiDelegate = context.coordinator
        view.navigationDelegate = context.coordinator
        view.isOpaque = false
        view.backgroundColor = .black
        context.coordinator.home = pairing.server
        Calls.shared.run = { [weak view] js in view?.evaluateJavaScript(js) }
        view.load(URLRequest(url: phoneURL))
        context.coordinator.reload = { [weak view] in
            // A push woke the app: make sure the page is connected (it may have been suspended).
            view?.evaluateJavaScript("document.visibilityState") { _, err in if err != nil { view?.reload() } }
        }
        return view
    }

    private var phoneURL: URL {
        var c = URLComponents(url: pairing.server, resolvingAgainstBaseURL: false)!
        c.path = "/"
        c.fragment = "/phone"
        return c.url!
    }

    func updateUIView(_ view: WKWebView, context: Context) {}

    final class Coordinator: NSObject, WKScriptMessageHandler, WKUIDelegate, WKNavigationDelegate {
        var home: URL?
        var reload: (() -> Void)?
        private var wake: NSObjectProtocol?

        override init() {
            super.init()
            wake = NotificationCenter.default.addObserver(forName: .bitvoiceWake, object: nil, queue: .main) { [weak self] _ in self?.reload?() }
        }

        func userContentController(_ c: WKUserContentController, didReceive message: WKScriptMessage) {
            guard let text = message.body as? String else { return }
            Task { @MainActor in Calls.shared.fromPage(text) }
        }

        // The page asks for the microphone; this app exists to make calls, and iOS has asked the person.
        func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin, initiatedByFrame frame: WKFrameInfo,
                     type: WKMediaCaptureType, decisionHandler: @escaping (WKPermissionDecision) -> Void) {
            decisionHandler(origin.host == home?.host && type == .microphone ? .grant : .deny)
        }

        // Only BitVoice's own pages load here; links elsewhere open in Safari.
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = action.request.url else { return decisionHandler(.cancel) }
            if url.host == home?.host || url.scheme == "about" { return decisionHandler(.allow) }
            UIApplication.shared.open(url)
            decisionHandler(.cancel)
        }
    }
}
