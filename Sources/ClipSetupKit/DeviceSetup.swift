#if canImport(ESPProvision)   // iOS only: the package links ESPProvision for iOS alone
import AccessorySetupKit
import CoreBluetooth
import Foundation
import UIKit
@preconcurrency import ESPProvision

/// ESPProvision calls back on its Bluetooth queue, so every callback below is `@Sendable` (nonisolated)
/// and hops to the main actor itself; an inherited main-actor closure traps in Swift 6 when called off-main.
/// Drives one device's Wi-Fi setup over Bluetooth with Espressif's provisioning protocol
/// (security 1: the PIN from the QR is the proof of possession). The order of the steps is `SetupFlow`'s.
@available(iOS 18.0, *)
@MainActor
public final class DeviceSetup: ObservableObject {
    public typealias Step = SetupState
    public typealias Network = SetupNetwork

    public let link: SetupLink
    @Published private var flow: SetupFlow
    public var step: Step { flow.state }
    public var networks: [Network] { flow.networks }
    /// The device's reply to each custom step this session, by endpoint name.
    public var replies: [String: Data] { flow.replies }
    /// Why the accessory setup sheet wasn't used, when it wasn't (shown small, for testing).
    @Published public private(set) var sheetNote: String?
    private var accessories: ASAccessorySession?
    public static let serviceUUID = CBUUID(string: "4617760e-5308-4efe-b137-04e8fa0a5b61")
    private var device: Unchecked<ESPDevice>?
    private var pop: PinProvider?
    /// Bumped on every start, so a late callback from an earlier attempt is dropped, not applied.
    private var attempt = 0
    /// Counts custom-step sends, so a step's timeout can't fire on a later step.
    private var sends = 0
    /// How long a custom step may go unanswered. ESPProvision doesn't fail a request when the link drops.
    public var customStepTimeout: Duration = .seconds(20)

    /// `steps` run in order inside the encrypted session, after it opens and before the Wi-Fi scan.
    public init(link: SetupLink, steps: [CustomStep] = []) {
        self.link = link
        self.flow = SetupFlow(displayName: link.displayName, steps: steps)
    }

    private func handle(_ event: SetupFlow.Event) {
        guard let effect = flow.handle(event) else { return }
        perform(effect)
    }

    /// Applies a radio callback only if it belongs to the current attempt.
    private func handle(_ event: SetupFlow.Event, from attempt: Int) {
        guard attempt == self.attempt else { return }
        handle(event)
    }

    /// Starts with iOS's accessory setup sheet (one tap, no Bluetooth permission prompt). If the sheet
    /// isn't available or fails, it falls back to plain Bluetooth, which asks for permission once.
    public func start() {
        attempt += 1
        handle(.start)
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
                handle(.cancelled)
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
        handle(.searching)
        let attempt = attempt
        ESPProvisionManager.shared.createESPDevice(deviceName: link.name, transport: .ble, security: .secure,
                                                   proofOfPossession: link.pin) { @Sendable [weak self] found, error in
            let box = found.map(Unchecked.init)
            let why = error?.description
            Task { @MainActor in self?.found(box, why, attempt) }
        }
    }

    private func found(_ box: Unchecked<ESPDevice>?, _ why: String?, _ attempt: Int) {
        guard attempt == self.attempt else { return }
        guard let box else {
            handle(.notFound(why))
            return
        }
        device = box
        pop = PinProvider(pin: link.pin)
        handle(.found)
    }

    private func perform(_ effect: SetupFlow.Effect) {
        guard let device else { return }
        let attempt = attempt
        switch effect {
        case .connect:
            device.value.connect(delegate: pop) { @Sendable [weak self] status in
                let event: SetupFlow.Event
                switch status {
                case .connected: event = .connected
                case .failedToConnect(let error): event = .connectFailed(error.description)
                case .disconnected: event = .disconnected
                }
                Task { @MainActor in self?.handle(event, from: attempt) }
            }
        case .send(let path, let data):
            sends += 1
            let send = sends
            device.value.sendData(path: path, data: data) { @Sendable [weak self] reply, error in
                let event: SetupFlow.Event = if let reply, error == nil { .replied(reply) } else { .customFailed(error?.description ?? "no reply") }
                Task { @MainActor in self?.handle(event, from: attempt) }
            }
            Task { @MainActor [weak self, timeout = customStepTimeout] in
                try? await Task.sleep(for: timeout)
                guard let self, self.sends == send, self.step == .custom(path: path) else { return }
                self.handle(.customFailed("no reply in \(timeout.components.seconds) seconds"), from: attempt)
            }
        case .scan:
            device.value.scanWifiList { @Sendable [weak self] list, error in
                let found = (list ?? []).map { Network(ssid: $0.ssid, rssi: $0.rssi, secured: $0.auth != .open) }
                let why = error?.description
                Task { @MainActor in self?.handle(.scanned(found, why), from: attempt) }
            }
        case .provision(let ssid, let password):
            device.value.provision(ssid: ssid, passPhrase: password) { @Sendable [weak self] status in
                let event: SetupFlow.Event
                switch status {
                case .success: event = .joined(ssid: ssid)
                case .configApplied: event = .configApplied(ssid: ssid)
                case .failure(let error):
                    switch error {
                    case .wifiStatusAuthenticationError: event = .joinFailed("That password didn't work for \(ssid).")
                    case .wifiStatusNetworkNotFound: event = .joinFailed("\(ssid) is out of range of the device.")
                    default: event = .joinFailed("Couldn't join \(ssid): \(error.description)")
                    }
                }
                Task { @MainActor in self?.handle(event, from: attempt) }
            }
        }
    }

    /// Scans for Wi-Fi again ("Scan again").
    public func scan() { handle(.rescan) }

    public func join(ssid: String, password: String) { handle(.join(ssid: ssid, password: password)) }

    public func cancel() {
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

/// Devices nearby that are waiting for setup, by Bluetooth name, sorted and without repeats.
@MainActor
public func nearbySetupDevices(prefix: String = "PROV_") async -> [String] {
    let found: [String] = await withCheckedContinuation { done in
        ESPProvisionManager.shared.searchESPDevices(devicePrefix: prefix, transport: .ble, security: .secure) { @Sendable devices, _ in
            done.resume(returning: (devices ?? []).map(\.name))
        }
    }
    return Array(Set(found)).sorted()
}
#endif
