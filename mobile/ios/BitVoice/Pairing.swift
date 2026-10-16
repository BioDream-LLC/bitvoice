import Foundation
import Security

/// What pairing gave this phone: the server, the device's ID and secret (kept in the Keychain), and the
/// extension it rings for.
struct Pairing: Codable, Equatable {
    var server: URL
    var device: String
    var secret: String
    var extensionNumber: String
    var name: String
}

/// The Keychain, through Apple's Security framework: the device secret never touches plain files.
enum Vault {
    private static let service = "llc.biodream.bitvoice.pairing"

    static func load() -> Pairing? {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecReturnData as String: true, kSecMatchLimit as String: kSecMatchLimitOne]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return try? JSONDecoder().decode(Pairing.self, from: data)
    }

    @discardableResult
    static func save(_ p: Pairing) -> Bool {
        clear()
        guard let data = try? JSONEncoder().encode(p) else { return false }
        let add: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                  kSecValueData as String: data,
                                  kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    static func clear() {
        SecItemDelete([kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service] as CFDictionary)
    }
}

/// The few calls the app makes to BitVoice itself; everything else happens in the phone page.
enum BitVoiceAPI {
    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    private static func post(_ url: URL, _ body: [String: String]) async throws -> [String: String] {
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("1", forHTTPHeaderField: "X-BitVoice") // BitVoice's guard against cross-site requests
        req.httpBody = try JSONEncoder().encode(body)
        req.timeoutInterval = 15
        let (data, resp) = try await URLSession.shared.data(for: req)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        guard code == 200 else {
            throw Failure(message: json["message"] as? String ?? "BitVoice answered \(code)")
        }
        return json.compactMapValues { $0 as? String }
    }

    /// Reads a pairing link (bitvoice://pair?server=...&code=...).
    static func parse(link: String) -> (server: URL, code: String)? {
        guard let c = URLComponents(string: link.trimmingCharacters(in: .whitespacesAndNewlines)), c.scheme == "bitvoice",
              c.host == "pair",
              let s = c.queryItems?.first(where: { $0.name == "server" })?.value, let server = URL(string: s),
              let code = c.queryItems?.first(where: { $0.name == "code" })?.value, !code.isEmpty,
              server.scheme == "https" else { return nil } // the device secret never travels unencrypted
        return (server, code)
    }

    static func pair(server: URL, code: String, name: String) async throws -> Pairing {
        let r = try await post(server.appending(path: "api/mobile/pair"), ["code": code, "name": name, "platform": "ios"])
        guard let device = r["device"], let secret = r["secret"], let ext = r["extension"] else {
            throw Failure(message: "BitVoice's answer was incomplete")
        }
        let home = r["server"].flatMap(URL.init(string:)) ?? server
        return Pairing(server: home, device: device, secret: secret, extensionNumber: ext, name: r["name"] ?? "")
    }

    static func sendToken(_ token: String, for p: Pairing) async throws {
        _ = try await post(p.server.appending(path: "api/mobile/token"), ["device": p.device, "secret": p.secret, "token": token])
    }

    static func unpair(_ p: Pairing) async {
        _ = try? await post(p.server.appending(path: "api/mobile/unpair"), ["device": p.device, "secret": p.secret])
    }
}
