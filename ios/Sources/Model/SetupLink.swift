import Foundation

/// What a device's setup QR (or NFC sticker) carries: its Bluetooth name and a one-time PIN.
/// `https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst`
public struct SetupLink: Equatable, Hashable, Sendable {
    public static let host = "setup.nard.uk"

    /// Bluetooth name the device advertises while in setup mode (always starts with `PROV_`).
    public let name: String
    /// Proof of possession: the device makes a new one each time it enters setup.
    public let pin: String

    public init?(name: String, pin: String) {
        let name = name.trimmingCharacters(in: .whitespaces)
        let pin = pin.trimmingCharacters(in: .whitespaces)
        guard name.hasPrefix("PROV_"), name.count > 5, name.count <= 29, !pin.isEmpty, pin.count <= 64 else { return nil }
        self.name = name
        self.pin = pin
    }

    public init?(url: URL?) {
        guard let url, let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
              parts.scheme == "https", parts.host?.lowercased() == Self.host, parts.path == "/d" else { return nil }
        let value = { (key: String) in parts.queryItems?.first { $0.name == key }?.value ?? "" }
        self.init(name: value("n"), pin: value("p"))
    }

    public var url: URL {
        var parts = URLComponents()
        parts.scheme = "https"
        parts.host = Self.host
        parts.path = "/d"
        parts.queryItems = [URLQueryItem(name: "n", value: name), URLQueryItem(name: "p", value: pin)]
        return parts.url!
    }

    /// `PROV_DIAL_4B98` reads as "Dial 4B98".
    public var displayName: String { Self.displayName(for: name) }

    public static func displayName(for name: String) -> String {
        let words = name.dropFirst("PROV_".count).split(separator: "_").map { word -> String in
            word.allSatisfy(\.isHexDigit) && word.count <= 4 ? String(word) : word.prefix(1) + word.dropFirst().lowercased()
        }
        return words.isEmpty ? name : words.joined(separator: " ")
    }
}
