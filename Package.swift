// swift-tools-version: 6.0
import PackageDescription

// ClipSetupKit: the iPhone side of ClipSetup as a package, so other apps (My Boat) can set up a
// ClipSetup device. The link, the step order and the remembered passwords build everywhere; the
// Bluetooth part (`DeviceSetup`, `nearbySetupDevices`) is iOS only, because ESPProvision is.
let package = Package(
    name: "ClipSetupKit",
    platforms: [
        .iOS("26.0"),   // the Narduk Devices app's target; AccessorySetupKit needs 18
        .macOS(.v14),   // only so `swift test` runs the platform-free parts on a Mac
    ],
    products: [
        .library(name: "ClipSetupKit", targets: ["ClipSetupKit"]),
    ],
    dependencies: [
        // 3.2.0 is 78e58f010f8539f800bce063a26d3e4c4ac596b9, the revision the app pinned before.
        .package(url: "https://github.com/espressif/esp-idf-provisioning-ios", exact: "3.2.0"),
    ],
    targets: [
        .target(
            name: "ClipSetupKit",
            dependencies: [
                .product(name: "ESPProvision", package: "esp-idf-provisioning-ios", condition: .when(platforms: [.iOS])),
            ]
        ),
        .testTarget(name: "ClipSetupKitTests", dependencies: ["ClipSetupKit"]),
    ]
)
