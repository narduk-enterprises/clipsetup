import ClipSetupKit
import SwiftUI

@main
struct NardukDevicesApp: App {
    @State private var path: [SetupLink] = []

    var body: some Scene {
        WindowGroup {
            NavigationStack(path: $path) {
                NearbyView { path = [$0] }
                    .navigationDestination(for: SetupLink.self) { SetupView(link: $0).id($0) }
            }
            .onContinueUserActivity(NSUserActivityTypeBrowsingWeb) { activity in
                if let link = SetupLinkResolver.clipSetup.resolve(activity.webpageURL) { path = [link] }
            }
            .onOpenURL { url in
                if let link = SetupLinkResolver.clipSetup.resolve(url) { path = [link] }
            }
        }
    }
}

/// Devices nearby that are waiting for setup. Scanning a device's QR skips this; picking one here asks
/// for the PIN shown under its QR.
private struct NearbyView: View {
    let open: (SetupLink) -> Void
    @State private var names: [String] = []
    @State private var searching = false
    @State private var asking: String?
    @State private var pin = ""

    var body: some View {
        List {
            Section {
                if names.isEmpty {
                    Text(searching ? "Looking…" : "No devices in setup mode nearby.").foregroundStyle(.secondary)
                }
                ForEach(names, id: \.self) { name in
                    Button(SetupLink.displayName(for: name)) { pin = ""; asking = name }
                }
            } header: {
                Text("Waiting for setup")
            } footer: {
                Text("Quickest: scan the QR on the device's screen with the Camera.")
            }
        }
        .navigationTitle("Narduk Devices")
        .refreshable { await search() }
        .task { await search() }
        .alert("Setup PIN", isPresented: Binding(get: { asking != nil }, set: { if !$0 { asking = nil } })) {
            TextField("PIN under the QR code", text: $pin)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button("Cancel", role: .cancel) {}
            Button("Connect") {
                if let name = asking, let link = SetupLink(name: name, pin: pin) { open(link) }
            }
        }
    }

    private func search() async {
        searching = true
        names = await nearbySetupDevices(prefix: "PROV_")
        searching = false
    }
}
