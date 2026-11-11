import Foundation

/// What a device's setup QR (or NFC sticker) carries: its Bluetooth name and a one-time PIN.
/// `https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst`
public struct SetupLink: Equatable, Hashable, Sendable {
    public static let host = "setup.nard.uk"

    /// Bluetooth name the device advertises while in setup mode (always starts with `PROV_`).
    /// ESPProvision matches it as a prefix, so a form that only knows the kind (`PROV_MYBO_`) finds the
    /// first such device in range.
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

    /// Reads the `setup.nard.uk` form only. To accept other forms too, use a `SetupLinkResolver`.
    public init?(url: URL?) {
        guard let url, let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
              parts.scheme == "https", parts.host?.lowercased() == Self.host, parts.path == "/d" else { return nil }
        let value = { (key: String) in parts.queryItems?.first { $0.name == key }?.value ?? "" }
        self.init(name: value("n"), pin: value("p"))
    }

    /// This link in the `setup.nard.uk` form, whichever form it was read from.
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

/// One shape of setup link an app accepts.
public struct SetupLinkForm: Sendable {
    private let read: @Sendable (URL) -> SetupLink?

    /// Any shape: return the link `url` describes, or nil if it isn't one.
    public init(_ read: @escaping @Sendable (URL) -> SetupLink?) { self.read = read }

    public func link(from url: URL) -> SetupLink? { read(url) }

    /// ClipSetup's own form: `https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=k3m9qrst`.
    public static let clipSetup = SetupLinkForm { SetupLink(url: $0) }

    /// A host-supplied form whose last path segment is the device's code, for example
    /// `.code(host: "mybo.at", pathPrefix: "/box/", deviceName: "PROV_MYBO_")` reads
    /// `https://mybo.at/box/k3m9-qrst-2wxv`. The code becomes the PIN with dashes removed and
    /// lowercased (ClipSetup's PIN alphabet is lowercase, and a printed code is grouped with dashes).
    /// `deviceName` is the Bluetooth name, or a name prefix when the code doesn't say which device.
    /// Only `https`, that host (any case) and no port, user or extra path segment are accepted;
    /// a query or fragment is ignored.
    public static func code(host: String, pathPrefix: String, deviceName: String) -> SetupLinkForm {
        let host = host.lowercased()
        let prefix = pathPrefix.hasSuffix("/") ? pathPrefix : pathPrefix + "/"
        return SetupLinkForm { url in
            guard let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
                  parts.scheme?.lowercased() == "https", parts.host?.lowercased() == host,
                  parts.port == nil, parts.user == nil, parts.password == nil,
                  parts.path.hasPrefix(prefix) else { return nil }
            var segment = parts.path.dropFirst(prefix.count)
            if segment.hasSuffix("/") { segment = segment.dropLast() }
            guard !segment.isEmpty, !segment.contains("/") else { return nil }
            let code = segment.replacingOccurrences(of: "-", with: "").lowercased()
            return SetupLink(name: deviceName, pin: code)
        }
    }
}

/// Turns a scanned or opened URL into a `SetupLink`, trying each accepted form in order.
public struct SetupLinkResolver: Sendable {
    public let forms: [SetupLinkForm]

    public init(_ forms: [SetupLinkForm]) { self.forms = forms }

    /// Accepts only ClipSetup's `setup.nard.uk` form (what the Narduk Devices app and clip use).
    public static let clipSetup = SetupLinkResolver([.clipSetup])

    /// The `setup.nard.uk` form plus a host's own forms, which are tried after it.
    public static func clipSetup(plus forms: [SetupLinkForm]) -> SetupLinkResolver {
        SetupLinkResolver([.clipSetup] + forms)
    }

    public func resolve(_ url: URL?) -> SetupLink? {
        guard let url else { return nil }
        for form in forms {
            if let link = form.link(from: url) { return link }
        }
        return nil
    }
}
