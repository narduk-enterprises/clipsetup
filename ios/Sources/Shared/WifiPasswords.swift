import Foundation
import Security

/// Wi-Fi passwords the user chose to remember, kept in this app's Keychain so the next device is one tap.
enum WifiPasswords {
    private static let service = "com.narduk.devices.wifi"

    static func load(_ ssid: String) -> String? {
        var query = base(ssid)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func save(_ password: String, for ssid: String) {
        SecItemDelete(base(ssid) as CFDictionary)
        var item = base(ssid)
        item[kSecValueData as String] = Data(password.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(item as CFDictionary, nil)
    }

    static func forget(_ ssid: String) { SecItemDelete(base(ssid) as CFDictionary) }

    private static func base(_ ssid: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: ssid]
    }
}
