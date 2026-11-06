import SwiftUI

/// The whole setup flow for one device: find it, pick a network, send the password, done.
struct SetupView: View {
    @StateObject private var setup: DeviceSetup
    @State private var chosen: DeviceSetup.Network?

    init(link: SetupLink) { _setup = StateObject(wrappedValue: DeviceSetup(link: link)) }

    var body: some View {
        content
            .navigationTitle(setup.link.displayName)
            .navigationBarTitleDisplayMode(.inline)
            .task { setup.start() }
            .onDisappear { setup.cancel() }
            .sheet(item: $chosen) { network in
                PasswordSheet(network: network, device: setup.link.displayName) { password, remember in
                    if remember { WifiPasswords.save(password, for: network.ssid) } else { WifiPasswords.forget(network.ssid) }
                    chosen = nil
                    setup.join(ssid: network.ssid, password: password)
                }
            }
    }

    @ViewBuilder private var content: some View {
        switch setup.step {
        case .finding:
            Working(symbol: "dot.radiowaves.left.and.right", title: "Looking for \(setup.link.displayName)",
                    detail: "Keep your phone near it.", note: setup.sheetNote)
        case .connecting:
            Working(symbol: "lock.shield", title: "Connecting securely", detail: "Using the code you scanned.", note: setup.sheetNote)
        case .scanning:
            Working(symbol: "wifi", title: "Asking what Wi-Fi it can see", detail: nil, note: setup.sheetNote)
        case .pickNetwork:
            List {
                Section {
                    ForEach(setup.networks) { network in
                        Button { chosen = network } label: { NetworkRow(network: network) }
                            .foregroundStyle(.primary)
                    }
                } header: {
                    Text("Networks \(setup.link.displayName) can see")
                } footer: {
                    VStack(alignment: .leading, spacing: 6) {
                        Button("Scan again") { setup.scan() }
                        if let note = setup.sheetNote { Text(note).font(.caption2).foregroundStyle(.tertiary) }
                    }
                }
            }
        case .sending(let ssid):
            Working(symbol: "arrow.up.circle", title: "Joining \(ssid)",
                    detail: "\(setup.link.displayName) is trying your password.")
        case .done(let ssid):
            Finished(symbol: "checkmark.circle.fill", color: .green, title: "\(setup.link.displayName) is on \(ssid)",
                     detail: "You can close this.", action: nil)
        case .failed(let why):
            Finished(symbol: "exclamationmark.triangle.fill", color: .orange, title: "That didn't work",
                     detail: why, action: ("Try again", { setup.start() }))
        }
    }
}

private struct Working: View {
    let symbol: String, title: String, detail: String?
    var note: String? = nil
    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: symbol).font(.system(size: 44)).foregroundStyle(.tint).symbolEffect(.pulse)
            Text(title).font(.title3.weight(.semibold)).multilineTextAlignment(.center)
            if let detail { Text(detail).foregroundStyle(.secondary).multilineTextAlignment(.center) }
            ProgressView().padding(.top, 4)
            if let note { Text(note).font(.caption2).foregroundStyle(.tertiary).multilineTextAlignment(.center) }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct Finished: View {
    let symbol: String, color: Color, title: String, detail: String
    let action: (String, () -> Void)?
    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: symbol).font(.system(size: 52)).foregroundStyle(color)
            Text(title).font(.title3.weight(.semibold)).multilineTextAlignment(.center)
            Text(detail).foregroundStyle(.secondary).multilineTextAlignment(.center)
            if let action { Button(action.0, action: action.1).buttonStyle(.borderedProminent).padding(.top, 8) }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct NetworkRow: View {
    let network: DeviceSetup.Network
    var body: some View {
        HStack {
            Text(network.ssid)
            Spacer()
            if network.secured { Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary) }
            Image(systemName: "wifi", variableValue: Double(network.bars) / 3).foregroundStyle(.secondary)
        }
    }
}

private struct PasswordSheet: View {
    let network: DeviceSetup.Network
    let device: String
    let send: (String, Bool) -> Void
    @State private var password = ""
    @State private var remember = true
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    if network.secured {
                        SecureField("Password", text: $password)
                            .textContentType(.password)
                            .submitLabel(.join)
                            .onSubmit(join)
                    } else {
                        Text("Open network: no password needed.").foregroundStyle(.secondary)
                    }
                } footer: {
                    Text("Sent over an encrypted Bluetooth link straight to \(device). It isn't uploaded anywhere.")
                }
                if network.secured {
                    Toggle("Remember on this iPhone", isOn: $remember)
                }
            }
            .navigationTitle(network.ssid)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Join", action: join).disabled(network.secured && password.isEmpty)
                }
            }
            .onAppear { password = WifiPasswords.load(network.ssid) ?? "" }
        }
        .presentationDetents([.medium])
    }

    private func join() {
        guard !network.secured || !password.isEmpty else { return }
        send(network.secured ? password : "", remember && network.secured)
    }
}
