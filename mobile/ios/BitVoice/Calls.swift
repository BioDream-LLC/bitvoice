import AVFoundation
import CallKit
import os
import Foundation
import PushKit
import UIKit

/// Calls on the phone's own call screen. A VoIP push wakes the app; iOS requires the call to be reported
/// to CallKit at once, so it is, and the phone page then connects and is rung by BitVoice. Answering or
/// ending on the call screen is passed to the page, and the page's calls are mirrored back.
@MainActor
final class Calls: NSObject, ObservableObject {
    static let shared = Calls()

    @Published var pairing: Pairing? = Vault.load()
    @Published var status = ""
    /// A pairing link that arrived from outside (a link tapped in Messages or the camera), not yet used.
    @Published var pendingLink: String? = UserDefaults.standard.string(forKey: "pairLink")

    private let provider: CXProvider
    private let controller = CXCallController()
    private let registry = PKPushRegistry(queue: .main)
    /// BitVoice call IDs and the CallKit UUIDs shown for them.
    private var uuids: [String: UUID] = [:]
    /// Calls announced by a push before the page has heard of them, by BitVoice call ID.
    private var pending: Set<String> = []
    /// Calls that reached "connected": a push timeout must not then report them as unanswered.
    private var connected: Set<String> = []
    /// Set by the web view: runs JavaScript in the phone page.
    var run: ((String) -> Void)?

    override init() {
        let cfg = CXProviderConfiguration()
        cfg.supportsVideo = false
        cfg.maximumCallsPerCallGroup = 1
        cfg.supportedHandleTypes = [.phoneNumber, .generic]
        cfg.includesCallsInRecents = true
        provider = CXProvider(configuration: cfg)
        super.init()
        provider.setDelegate(self, queue: .main)
    }

    func start() {
        registry.delegate = self
        registry.desiredPushTypes = [.voIP]
    }

    func paired(_ p: Pairing) {
        if !Vault.save(p) { status = "This phone could not keep the pairing; it will be asked again next time." }
        pairing = p
        if let t = registry.pushToken(for: .voIP) { send(token: t) }
    }

    func unpair() async {
        if let p = pairing { await BitVoiceAPI.unpair(p) }
        Vault.clear()
        pairing = nil
    }

    private func send(token: Data) {
        guard let p = pairing else { return }
        let hex = token.map { String(format: "%02x", $0) }.joined()
        Task {
            do { try await BitVoiceAPI.sendToken(hex, for: p); status = "" } catch { status = error.localizedDescription }
        }
    }

    private func uuid(for id: String) -> UUID {
        if let u = uuids[id] { return u }
        let u = UUID()
        uuids[id] = u
        return u
    }

    /// The iOS Simulator has no call screen: CallKit ends every incoming call there at once ("no UI to host
    /// the call"). On the Simulator calls stay in the phone page, answered with its own buttons.
    static let hasCallScreen: Bool = {
        #if targetEnvironment(simulator)
        return false
        #else
        return true
        #endif
    }()

    private func reportIncoming(id: String, number: String, name: String) {
        guard Calls.hasCallScreen else { return }
        let u = uuid(for: id)
        let update = CXCallUpdate()
        update.remoteHandle = CXHandle(type: number.allSatisfy(\.isNumber) ? .phoneNumber : .generic, value: number)
        update.localizedCallerName = name.isEmpty ? number : name
        update.hasVideo = false
        update.supportsDTMF = true
        update.supportsHolding = false
        provider.reportNewIncomingCall(with: u, update: update) { error in
            if let error { Calls.log.error("the call screen refused call \(id, privacy: .public): \(error.localizedDescription, privacy: .public)") }
            if error != nil { Task { @MainActor in self.uuids[id] = nil } }
        }
    }

    /// A message from the phone page (see web/src/native.ts).
    func fromPage(_ text: String) {
        guard let data = text.data(using: .utf8),
              let m = try? JSONSerialization.jsonObject(with: data) as? [String: Any], let type = m["type"] as? String else { return }
        if type == "phone" {
            status = (m["error"] as? String) ?? ""
            return
        }
        guard type == "call", let id = m["id"] as? String, let state = m["state"] as? String else { return }
        let number = m["number"] as? String ?? "", name = m["name"] as? String ?? ""
        let direction = m["direction"] as? String ?? "in"
        switch state {
        case "incoming":
            // The push that woke the app named BitVoice's call ID; the page knows the call by its own ID.
            let pbx = m["pbxCall"] as? String ?? ""
            if !pbx.isEmpty, pending.remove(pbx) != nil, let u = uuids.removeValue(forKey: pbx) {
                uuids[id] = u
            } else if uuids[id] == nil {
                reportIncoming(id: id, number: number, name: name)
            }
            if let u = uuids[id], answeredEarly.remove(u) != nil { run?("window.bitvoice && window.bitvoice.answer(\(js(id)))") }
        case "calling":
            guard Calls.hasCallScreen else { break }
            let u = uuid(for: id)
            let action = CXStartCallAction(call: u, handle: CXHandle(type: .phoneNumber, value: number))
            controller.request(CXTransaction(action: action)) { _ in }
        case "ringing":
            if direction == "out", let u = uuids[id] { provider.reportOutgoingCall(with: u, startedConnectingAt: Date()) }
        case "connected":
            if direction == "out", let u = uuids[id] { provider.reportOutgoingCall(with: u, connectedAt: Date()) }
            // An incoming call answered in the page (not from the call screen): tell the system it is
            // connected, so the call screen leaves the ringing state instead of still showing the call.
            connected.insert(id)
            if direction != "out", let u = uuids[id] {
                let update = CXCallUpdate()
                update.hasVideo = false
                provider.reportCall(with: u, updated: update)
            }
        case "ended":
            connected.remove(id)
            if let u = uuids.removeValue(forKey: id) { provider.reportCall(with: u, endedAt: Date(), reason: .remoteEnded) }
        default:
            break
        }
    }

    /// Calls answered on the call screen before the page knew of them; answered when the page catches up.
    private var answeredEarly: Set<UUID> = []

    private func id(for u: UUID) -> String? { uuids.first(where: { $0.value == u })?.key }

    private func js(_ s: String) -> String {
        let data = try? JSONSerialization.data(withJSONObject: [s])
        return String(data: data ?? Data("[\"\"]".utf8), encoding: .utf8).map { String($0.dropFirst().dropLast()) } ?? "\"\""
    }
}

extension Calls {
    /// What the call screen did, for Console and for diagnosing a phone (no numbers or names logged).
    nonisolated static let log = Logger(subsystem: "llc.biodream.bitvoice", category: "calls")
}

extension Calls: CXProviderDelegate {
    nonisolated func providerDidReset(_ provider: CXProvider) {
        Task { @MainActor in
            self.uuids.removeAll()
            self.pending.removeAll()
            self.connected.removeAll()
        }
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXAnswerCallAction) {
        Task { @MainActor in
            if let id = self.id(for: action.callUUID), !self.pending.contains(id) {
                self.run?("window.bitvoice && window.bitvoice.answer(\(self.js(id)))")
            } else {
                self.answeredEarly.insert(action.callUUID)
            }
            action.fulfill()
        }
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
        Calls.log.info("the call screen ended call \(action.callUUID.uuidString, privacy: .public)")
        Task { @MainActor in
            if let id = self.id(for: action.callUUID) {
                self.run?("window.bitvoice && window.bitvoice.hangup(\(self.js(id)))")
                self.uuids[id] = nil
            }
            action.fulfill()
        }
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXStartCallAction) {
        action.fulfill() // the page has already placed the call
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
        let on = action.isMuted
        Task { @MainActor in self.run?("window.bitvoice && window.bitvoice.mute(\(on))") }
        action.fulfill()
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXPlayDTMFCallAction) {
        let digits = action.digits
        Task { @MainActor in
            for d in digits { self.run?("window.bitvoice && window.bitvoice.dtmf(\(self.js(String(d))))") }
        }
        action.fulfill()
    }

    nonisolated func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {}
    nonisolated func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {}
}

extension Calls: PKPushRegistryDelegate {
    nonisolated func pushRegistry(_ registry: PKPushRegistry, didUpdate credentials: PKPushCredentials, for type: PKPushType) {
        let token = credentials.token
        Task { @MainActor in self.send(token: token) }
    }

    nonisolated func pushRegistry(_ registry: PKPushRegistry, didReceiveIncomingPushWith payload: PKPushPayload,
                                  for type: PKPushType, completion: @escaping () -> Void) {
        let p = payload.dictionaryPayload
        let id = p["callId"] as? String ?? UUID().uuidString
        let from = p["from"] as? String ?? ""
        let name = p["fromName"] as? String ?? ""
        Task { @MainActor in
            // iOS ends apps that take a VoIP push without showing a call, so report it first.
            self.pending.insert(id)
            self.reportIncoming(id: id, number: from, name: name)
            NotificationCenter.default.post(name: .bitvoiceWake, object: nil)
            completion()
            // The caller may give up before the phone page connects: then end the call screen too.
            DispatchQueue.main.asyncAfter(deadline: .now() + 35) {
                if self.pending.remove(id) != nil, !self.connected.contains(id), let u = self.uuids.removeValue(forKey: id) {
                    self.provider.reportCall(with: u, endedAt: Date(), reason: .unanswered)
                }
            }
        }
    }
}

extension Notification.Name {
    static let bitvoiceWake = Notification.Name("bitvoiceWake")
}
