import Foundation
import Security

/// Wi-Fi passwords the user chose to remember, kept in this app's Keychain so the next device is one tap.
public struct WifiPasswords: Sendable {
    /// The Keychain service the passwords are stored under. Each app uses its own.
    public let service: String

    public init(service: String) { self.service = service }

    /// The Narduk Devices app and its clip; the service they have always used, so remembered passwords carry over.
    public static let nardukDevices = WifiPasswords(service: "com.narduk.devices.wifi")

    public func load(_ ssid: String) -> String? {
        var query = base(ssid)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func save(_ password: String, for ssid: String) {
        SecItemDelete(base(ssid) as CFDictionary)
        var item = base(ssid)
        item[kSecValueData as String] = Data(password.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(item as CFDictionary, nil)
    }

    public func forget(_ ssid: String) { SecItemDelete(base(ssid) as CFDictionary) }

    private func base(_ ssid: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: ssid]
    }
}
