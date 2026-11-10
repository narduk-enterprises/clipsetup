import Foundation

/// Where one device's setup has got to.
public enum SetupState: Equatable, Sendable {
    case finding, connecting
    /// Sending a host's custom step to the device (the endpoint name only; never its data).
    case custom(path: String)
    case scanning, pickNetwork
    case sending(ssid: String)
    case done(ssid: String)
    case failed(String)
}

/// A Wi-Fi network the device can see.
public struct SetupNetwork: Identifiable, Hashable, Sendable {
    public let ssid: String
    public let rssi: Int32
    public let secured: Bool
    public var id: String { ssid }
    /// 0 to 3 bars, like the status bar.
    public var bars: Int { rssi >= -55 ? 3 : rssi >= -67 ? 2 : rssi >= -78 ? 1 : 0 }

    public init(ssid: String, rssi: Int32, secured: Bool) {
        self.ssid = ssid
        self.rssi = rssi
        self.secured = secured
    }
}

/// Bytes for one of the device's own endpoints, sent inside the same encrypted provisioning session
/// before the Wi-Fi (the device turns Bluetooth off once it has joined, so anything after would be lost).
public struct CustomStep: Sendable {
    /// The device's endpoint name (its GATT characteristic's user description), for example `mybo-claim`.
    public let path: String
    public let data: Data
    /// Reads the device's reply: return a sentence to stop setup with, or nil to go on.
    public let check: @Sendable (Data) -> String?

    public init(path: String, data: Data, check: @escaping @Sendable (Data) -> String? = { _ in nil }) {
        self.path = path
        self.data = data
        self.check = check
    }

    public static func custom(path: String, data: Data, check: @escaping @Sendable (Data) -> String? = { _ in nil }) -> CustomStep {
        CustomStep(path: path, data: data, check: check)
    }

    /// Espressif's own endpoints, which a custom step may not use.
    public static let reservedPaths: Set<String> = ["proto-ver", "prov-session", "prov-scan", "prov-config", "prov-ctrl"]
}

/// The order of one device's setup, without the radio: find, connect, custom steps in order, scan, join.
/// The caller feeds it what happened (`Event`) and carries out what it asks for (`Effect`), so the
/// ordering is the same whichever transport drives it, and testable without one.
public struct SetupFlow: Sendable {
    public enum Event: Sendable {
        /// Begin again from nothing (also after a failure).
        case start
        /// Looking for the device over Bluetooth.
        case searching
        case notFound(String?)
        /// The person closed the accessory sheet before picking the device.
        case cancelled
        case found
        case connected
        case disconnected
        case connectFailed(String)
        /// The device answered the current custom step.
        case replied(Data)
        case customFailed(String)
        /// The person asked for another Wi-Fi scan.
        case rescan
        case scanned([SetupNetwork], String?)
        case join(ssid: String, password: String)
        case configApplied(ssid: String)
        case joined(ssid: String)
        case joinFailed(String)
    }

    public enum Effect: Equatable, Sendable {
        case connect
        case send(path: String, data: Data)
        case scan
        case provision(ssid: String, password: String)
    }

    public let displayName: String
    public let steps: [CustomStep]
    public private(set) var state: SetupState = .finding
    public private(set) var networks: [SetupNetwork] = []
    /// The device's reply to each custom step this session, by endpoint name.
    public private(set) var replies: [String: Data] = [:]
    private var hasDevice = false
    private var nextStep = 0

    public init(displayName: String, steps: [CustomStep] = []) {
        self.displayName = displayName
        self.steps = steps
    }

    /// True once every custom step has been answered in this session.
    public var stepsDone: Bool { nextStep >= steps.count }

    public mutating func handle(_ event: Event) -> Effect? {
        switch event {
        case .start:
            state = .finding
            networks = []
            replies = [:]
            hasDevice = false
            nextStep = 0
            return nil
        case .searching:
            state = .finding
            return nil
        case .notFound(let why):
            state = .failed("Couldn't find \(displayName). Make sure it's showing its setup code and your phone is close. (\(why ?? "not found"))")
            return nil
        case .cancelled:
            state = .failed("Setup was cancelled. Tap Try again when you're ready.")
            return nil
        case .found:
            hasDevice = true
            nextStep = 0
            replies = [:]
            state = .connecting
            return .connect
        case .connected:
            return nextAfterConnect()
        case .disconnected:
            // The device turns Bluetooth off once it has joined; only a drop mid-setup is a failure.
            if case .done = state { return nil }
            if case .sending = state { return nil }
            state = .failed("\(displayName) disconnected. Try again.")
            return nil
        case .connectFailed(let detail):
            state = .failed(detail.localizedCaseInsensitiveContains("pop") || detail.localizedCaseInsensitiveContains("session")
                ? "\(displayName) didn't accept the code. Show a fresh setup code on it and scan again."
                : "Couldn't connect to \(displayName): \(detail)")
            return nil
        case .replied(let data):
            guard case .custom(let path) = state, nextStep < steps.count, steps[nextStep].path == path else { return nil }
            replies[path] = data
            if let why = steps[nextStep].check(data) {
                state = .failed(why)
                return nil
            }
            nextStep += 1
            return nextAfterConnect()
        case .customFailed(let why):
            guard case .custom(let path) = state else { return nil }
            state = .failed("\(displayName) didn't take \(path): \(why)")
            return nil
        case .rescan:
            guard hasDevice, stepsDone else { return nil }
            state = .scanning
            return .scan
        case .scanned(let found, let why):
            // Strongest first, one row per name (mesh networks repeat).
            var seen = Set<String>()
            networks = found.filter { !$0.ssid.isEmpty }.sorted { $0.rssi > $1.rssi }.filter { seen.insert($0.ssid).inserted }
            state = networks.isEmpty ? .failed("\(displayName) didn't see any Wi-Fi. \(why ?? "")") : .pickNetwork
            return nil
        case .join(let ssid, let password):
            guard hasDevice, stepsDone else { return nil }
            state = .sending(ssid: ssid)
            return .provision(ssid: ssid, password: password)
        case .configApplied(let ssid):
            state = .sending(ssid: ssid)
            return nil
        case .joined(let ssid):
            state = .done(ssid: ssid)
            return nil
        case .joinFailed(let why):
            state = .failed(why)
            return nil
        }
    }

    /// Custom steps first, in order, then the Wi-Fi scan.
    private mutating func nextAfterConnect() -> Effect? {
        guard nextStep < steps.count else {
            state = .scanning
            return .scan
        }
        let step = steps[nextStep]
        guard !step.path.isEmpty, !CustomStep.reservedPaths.contains(step.path) else {
            state = .failed("Setup step \"\(step.path)\" can't be sent: its endpoint name is empty or one of Espressif's own.")
            return nil
        }
        state = .custom(path: step.path)
        return .send(path: step.path, data: step.data)
    }
}
