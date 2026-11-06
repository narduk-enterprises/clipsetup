# ClipSetup

**Put an ESP32 on Wi-Fi from an iPhone, without installing an app or typing on the device.**

The device shows a QR code. You point the iPhone Camera at it, an App Clip slides up, iOS's own
accessory sheet pairs with the device, you pick your network, and the device joins. That's the whole
setup.

```
device in setup mode ──QR──▶ iPhone Camera ──▶ App Clip card ──▶ iOS accessory sheet (one tap)
        ▲                                                              │
        └──────── Wi-Fi name + password, encrypted over Bluetooth ◀────┘
```

- **Firmware (`firmware/ClipSetup`):** an Arduino-ESP32 library. `clipsetup::begin("LAMP")` and the device
  advertises over Bluetooth, makes a new one-time PIN, and hands you the link to show as a QR (on a
  screen, a sticker, or printed to the serial console).
- **iPhone (`ios/`):** the Device Setup App Clip and the Narduk Devices app around it. Both use
  Espressif's standard provisioning protocol through [ESPProvision](https://github.com/espressif/esp-idf-provisioning-ios)
  and iOS's AccessorySetupKit sheet, so there's no Bluetooth permission prompt.

## Status

Working end to end on an M5Dial (ESP32-S3) and an iPhone 17 Pro on iOS 27, in development builds. The
App Clip isn't on the App Store yet, so for now you build and install it yourself (below). Once it's
published, devices using the default link work on any iPhone with nothing to install.

## Add it to a device

PlatformIO:

```ini
lib_deps = https://github.com/narduk-enterprises/clipsetup.git
```

Needs the Arduino-ESP32 core 3.x (for example the [pioarduino](https://github.com/pioarduino/platform-espressif32)
platform) and a board with Bluetooth LE: ESP32, S3, C3 or C6.

```cpp
#include <Preferences.h>
#include <WiFi.h>
#include <clip_setup.h>

Preferences prefs;

void setup() {
  Serial.begin(115200);
  prefs.begin("wifi");
  if (prefs.isKey("ssid")) {
    WiFi.begin(prefs.getString("ssid").c_str(), prefs.getString("pass").c_str());
    return;
  }
  clipsetup::onCredentials([](const char* ssid, const char* pass) {
    prefs.putString("ssid", ssid);
    prefs.putString("pass", pass);
  });
  clipsetup::begin("LAMP");   // advertises as PROV_LAMP_<last 4 of MAC>
  clipsetup::printQR();       // or draw clipsetup::link() as a QR on your screen
}

void loop() {
  if (clipsetup::state() == clipsetup::State::Joined) { /* online */ }
}
```

A full starter for a $3 ESP32-C3 SuperMini, with the LED showing setup state and the BOOT button held
for 3 seconds to set up again, is in [`firmware/ClipSetup/examples/C3Starter`](firmware/ClipSetup/examples/C3Starter/C3Starter.ino).

## The link

```
https://setup.nard.uk/d?n=PROV_LAMP_4B98&p=k3m9qrst
```

- `n`: the device's Bluetooth name. It always starts with `PROV_`.
- `p`: a one-time PIN, 8 characters, new every time setup starts.

Every ClipSetup device advertises Bluetooth service `4617760E-5308-4EFE-B137-04E8FA0A5B61`, which is
how the accessory sheet finds it.

## Security

- The Wi-Fi password travels only over an encrypted Bluetooth session between the phone and the device
  (Espressif security 1: X25519 key exchange, AES-CTR). It never leaves the phone otherwise and is never
  printed by the firmware.
- The session only opens with the PIN from the QR, so someone nearby can't push their own network to
  your device without seeing its screen. The PIN changes every time setup starts, so an old photo of
  the QR is useless.
- Bluetooth is switched off once the device has joined.
- The App Clip can remember a password on the phone (in its Keychain) so the next device is one tap.

## Build the iPhone app yourself

Needs Xcode 26 or newer and [XcodeGen](https://github.com/yonaskolb/XcodeGen).

```bash
cd ios
xcodegen generate
open NardukDevices.xcodeproj
```

Set your own team and bundle IDs in `project.yml`. To try the App Clip card before publishing, run the
`NardukDevicesClip` scheme once, then on the iPhone go to Settings → Developer → App Clips Testing →
Local Experiences and register the URL prefix `https://setup.nard.uk/d` with the clip's bundle ID. If
the full app is installed, iOS opens it instead of the clip; it handles the same link.

## Roadmap

- Publish the App Clip, so devices work with nothing to build.
- Security 2 (SRP) as an option.
- Send extra settings along with the Wi-Fi (a server address, a device name).
- NFC stickers as an alternative to the QR.

## License

MIT
