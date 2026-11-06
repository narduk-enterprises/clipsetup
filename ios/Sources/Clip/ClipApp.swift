import SwiftUI

/// The App Clip: opened by a device's setup QR or NFC sticker (`https://setup.nard.uk/d?n=…&p=…`).
@main
struct DeviceSetupClip: App {
    @State private var link: SetupLink?

    var body: some Scene {
        WindowGroup {
            NavigationStack {
                if let link {
                    SetupView(link: link).id(link)
                } else {
                    ContentUnavailableView("Scan a device's setup code", systemImage: "qrcode.viewfinder",
                                           description: Text("Point the Camera at the QR code on the device's screen."))
                }
            }
            .onContinueUserActivity(NSUserActivityTypeBrowsingWeb) { activity in
                if let found = SetupLink(url: activity.webpageURL) { link = found }
            }
            .onOpenURL { url in
                if let found = SetupLink(url: url) { link = found }
            }
        }
    }
}
