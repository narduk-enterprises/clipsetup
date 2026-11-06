import AccessorySetupKit
import CoreBluetooth
import Foundation
import UIKit
@preconcurrency import ESPProvision

/// ESPProvision calls back on its Bluetooth queue, so every callback below is `@Sendable` (nonisolated)
/// and hops to the main actor itself; an inherited main-actor closure traps in Swift 6 when called off-main.
/// Drives one device's Wi-Fi setup over Bluetooth with Espressif's provisioning protocol
/// (security 1: the PIN from the QR is the proof of possession).
@MainActor
final class DeviceSetup: ObservableObject {
    enum Step: Equatable {
        case finding, connecting, scanning, pickNetwork
        case sending(ssid: String)
        case done(ssid: String)
        case failed(String)
    }

    struct Network: Identifiable, Hashable {
        let ssid: String
        let rssi: Int32
        let secured: Bool
        var id: String { ssid }
        /// 0 to 3 bars, like the status bar.
        var bars: Int { rssi >= -55 ? 3 : rssi >= -67 ? 2 : rssi >= -78 ? 1 : 0 }
    }

    let link: SetupLink
    @Published private(set) var step: Step = .finding
    @Published private(set) var networks: [Network] = []
    /// Why the accessory setup sheet wasn't used, when it wasn't (shown small, for testing).
    @Published private(set) var sheetNote: String?
    private var accessories: ASAccessorySession?
    static let serviceUUID = CBUUID(string: "4617760e-5308-4efe-b137-04e8fa0a5b61")
    private var device: Unchecked<ESPDevice>?
    private var pop: PinProvider?

    init(link: SetupLink) { self.link = link }

    /// Starts with iOS's accessory setup sheet (one tap, no Bluetooth permission prompt). If the sheet
    /// isn't available or fails, it falls back to plain Bluetooth, which asks for permission once.
    func start() {
        step = .finding
        networks = []
        device?.value.disconnect()
        device = nil
        if let accessories {
            showSheet(accessories)
            return
        }
        let session = ASAccessorySession()
        accessories = session
        session.activate(on: .main) { [weak self] event in
            MainActor.assumeIsolated { self?.accessoryEvent(event) }
        }
    }

    private var sheetShown = false
    private var accessoryReady = false

    private func accessoryEvent(_ event: ASAccessoryEvent) {
        switch event.eventType {
        case .activated:
            guard let accessories else { return }
            // Already added once on this iPhone: no sheet needed.
            if accessories.accessories.contains(where: { $0.displayName == link.displayName }) {
                accessoryReady = true
                findDevice()
            } else {
                showSheet(accessories)
            }
        case .accessoryAdded:
            // Start right away; the sheet's "Done" only dismisses it.
            accessoryReady = true
            sheetNote = nil
            findDevice()
        case .pickerDidDismiss:
            sheetShown = false
            if !accessoryReady, step == .finding {
                step = .failed("Setup was cancelled. Tap Try again when you're ready.")
            }
        default:
            break
        }
    }

    private var sheetTries = 0

    private func showSheet(_ session: ASAccessorySession) {
        guard !sheetShown else { return }
        // The sheet refuses ("Application is not in foreground") until the scene is active, which can lag
        // a launch from a link or the Camera by a moment.
        guard UIApplication.shared.applicationState == .active else {
            sheetTries += 1
            if sheetTries > 20 { sheetNote = "Setup sheet unavailable: app never became active"; findDevice(); return }
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(150))
                self.showSheet(session)
            }
            return
        }
        let descriptor = ASDiscoveryDescriptor()
        descriptor.bluetoothServiceUUID = Self.serviceUUID
        descriptor.bluetoothNameSubstring = "PROV_"
        descriptor.bluetoothRange = .immediate
        let image = UIImage(systemName: "dot.radiowaves.left.and.right") ?? UIImage()
        let item = ASPickerDisplayItem(name: link.displayName, productImage: image, descriptor: descriptor)
        sheetShown = true
        session.showPicker(for: [item]) { [weak self] error in
            guard let error else { return }
            let why = error.localizedDescription
            Task { @MainActor in
                guard let self else { return }
                self.sheetShown = false
                if why.localizedCaseInsensitiveContains("foreground"), self.sheetTries < 20 {
                    self.sheetTries += 1
                    try? await Task.sleep(for: .milliseconds(300))
                    self.showSheet(session)
                    return
                }
                self.sheetNote = "Setup sheet unavailable: \(why)"
                self.findDevice()
            }
        }
    }

    private func findDevice() {
        step = .finding
        ESPProvisionManager.shared.createESPDevice(deviceName: link.name, transport: .ble, security: .secure,
                                                   proofOfPossession: link.pin) { @Sendable [weak self] found, error in
            let box = found.map(Unchecked.init)
            let why = error?.description
            Task { @MainActor in self?.found(box, why) }
        }
    }

    private func found(_ box: Unchecked<ESPDevice>?, _ why: String?) {
        guard let box else {
            step = .failed("Couldn't find \(link.displayName). Make sure it's showing its setup code and your phone is close. (\(why ?? "not found"))")
            return
        }
        device = box
        step = .connecting
        let pop = PinProvider(pin: link.pin)
        self.pop = pop
        box.value.connect(delegate: pop) { @Sendable [weak self] status in
            let state: String
            switch status {
            case .connected: state = "connected"
            case .failedToConnect(let error): state = "failed:" + error.description
            case .disconnected: state = "disconnected"
            }
            Task { @MainActor in self?.session(state) }
        }
    }

    private func session(_ state: String) {
        switch state {
        case "connected":
            scan()
        case "disconnected":
            // The device turns Bluetooth off once it has joined; only a drop mid-setup is a failure.
            if case .done = step { return }
            if case .sending = step { return }
            step = .failed("\(link.displayName) disconnected. Try again.")
        default:
            let detail = state.dropFirst("failed:".count)
            step = .failed(detail.localizedCaseInsensitiveContains("pop") || detail.localizedCaseInsensitiveContains("session")
                ? "\(link.displayName) didn't accept the code. Show a fresh setup code on it and scan again."
                : "Couldn't connect to \(link.displayName): \(detail)")
        }
    }

    func scan() {
        guard let device else { return }
        step = .scanning
        device.value.scanWifiList { @Sendable [weak self] list, error in
            let found = (list ?? []).map { Network(ssid: $0.ssid, rssi: $0.rssi, secured: $0.auth != .open) }
            let why = error?.description
            Task { @MainActor in self?.scanned(found, why) }
        }
    }

    private func scanned(_ found: [Network], _ why: String?) {
        // Strongest first, one row per name (mesh networks repeat).
        var seen = Set<String>()
        networks = found.filter { !$0.ssid.isEmpty }.sorted { $0.rssi > $1.rssi }.filter { seen.insert($0.ssid).inserted }
        step = networks.isEmpty ? .failed("\(link.displayName) didn't see any Wi-Fi. \(why ?? "")") : .pickNetwork
    }

    func join(ssid: String, password: String) {
        guard let device else { return }
        step = .sending(ssid: ssid)
        device.value.provision(ssid: ssid, passPhrase: password) { @Sendable [weak self] status in
            let outcome: String
            switch status {
            case .success: outcome = "success"
            case .configApplied: outcome = "applied"
            case .failure(let error):
                switch error {
                case .wifiStatusAuthenticationError: outcome = "That password didn't work for \(ssid)."
                case .wifiStatusNetworkNotFound: outcome = "\(ssid) is out of range of the device."
                default: outcome = "Couldn't join \(ssid): \(error.description)"
                }
            }
            Task { @MainActor in self?.provisioned(ssid, outcome) }
        }
    }

    private func provisioned(_ ssid: String, _ outcome: String) {
        switch outcome {
        case "applied": step = .sending(ssid: ssid)
        case "success": step = .done(ssid: ssid)
        default: step = .failed(outcome)
        }
    }

    func cancel() {
        device?.value.disconnect()
    }
}

/// ESPProvision's types predate Sendable; they are only touched from the main actor or their own callbacks.
struct Unchecked<T>: @unchecked Sendable {
    let value: T
    init(_ value: T) { self.value = value }
}

private final class PinProvider: ESPDeviceConnectionDelegate {
    let pin: String
    init(pin: String) { self.pin = pin }
    func getProofOfPossesion(forDevice: ESPDevice, completionHandler: @escaping (String) -> Void) { completionHandler(pin) }
    func getUsername(forDevice: ESPDevice, completionHandler: @escaping (String?) -> Void) { completionHandler(nil) }
}
