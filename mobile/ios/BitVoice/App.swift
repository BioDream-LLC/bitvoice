import SwiftUI

@main
struct BitVoiceApp: App {
    @StateObject private var calls = Calls.shared

    init() { Calls.shared.start() }

    var body: some Scene {
        WindowGroup {
            Group {
                if let p = calls.pairing {
                    Home(pairing: p)
                } else {
                    PairView()
                }
            }
            .environmentObject(calls)
            .preferredColorScheme(.dark)
            .onOpenURL { url in
                // A pairing link opened from the camera app or a message.
                if calls.pairing == nil { calls.pendingLink = url.absoluteString }
            }
        }
    }
}

struct Home: View {
    let pairing: Pairing
    @EnvironmentObject var calls: Calls
    @State private var confirm = false

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Extension \(pairing.extensionNumber)").font(.headline).foregroundStyle(.white)
                    Text(pairing.name).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Menu {
                    Button("Unpair this phone", role: .destructive) { confirm = true }
                } label: { Image(systemName: "ellipsis.circle").font(.title3) }
                .accessibilityLabel("Settings")
            }
            .padding(.horizontal).padding(.vertical, 10)
            if !calls.status.isEmpty {
                Text(calls.status).font(.footnote).foregroundStyle(.orange).padding(.horizontal).frame(maxWidth: .infinity, alignment: .leading)
            }
            // The phone page keeps clear of the status bar and the home indicator (as the Android app
            // adds the system bars to its padding), so its Hang up bar is never under the indicator.
            PhoneView(pairing: pairing)
        }
        .background(Color.black)
        .confirmationDialog("Unpair this phone? It stops ringing for extension \(pairing.extensionNumber).", isPresented: $confirm, titleVisibility: .visible) {
            Button("Unpair", role: .destructive) { Task { await calls.unpair() } }
        }
    }
}

struct PairView: View {
    @EnvironmentObject var calls: Calls
    @State private var scanning = false
    @State private var link = ""
    @State private var error = ""
    @State private var busy = false

    var body: some View {
        VStack(spacing: 20) {
            Spacer()
            Image(systemName: "phone.bubble.fill").font(.system(size: 56)).foregroundStyle(.teal)
            Text("BitVoice").font(.largeTitle.bold())
            Text("Ask your administrator to open your extension in BitVoice and press **Add a phone**, then scan the code.")
                .multilineTextAlignment(.center).foregroundStyle(.secondary).padding(.horizontal)
            Button { scanning = true } label: { Label("Scan the code", systemImage: "qrcode.viewfinder").frame(maxWidth: .infinity) }
                .buttonStyle(.borderedProminent).controlSize(.large).padding(.horizontal).disabled(busy)
            HStack {
                TextField("or paste the pairing link", text: $link).textInputAutocapitalization(.never).autocorrectionDisabled()
                    .textFieldStyle(.roundedBorder)
                Button("Pair") { Task { await pair(link) } }.disabled(link.isEmpty || busy)
            }.padding(.horizontal)
            if busy { ProgressView() }
            if !error.isEmpty { Text(error).foregroundStyle(.red).font(.footnote).padding(.horizontal).multilineTextAlignment(.center) }
            Spacer()
        }
        .sheet(isPresented: $scanning) {
            Scanner { code in scanning = false; Task { await pair(code) } }.ignoresSafeArea()
        }
        .onAppear(perform: usePending)
        .onChange(of: calls.pendingLink) { _ in usePending() }
    }

    private func usePending() {
        guard let l = calls.pendingLink else { return }
        calls.pendingLink = nil
        Task { await pair(l) }
    }

    private func pair(_ text: String) async {
        guard let (server, code) = BitVoiceAPI.parse(link: text) else {
            error = "That is not a BitVoice pairing code."
            return
        }
        busy = true
        defer { busy = false }
        do {
            let p = try await BitVoiceAPI.pair(server: server, code: code, name: UIDevice.current.name)
            error = ""
            calls.paired(p)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
