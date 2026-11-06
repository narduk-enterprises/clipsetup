// ClipSetup: put an ESP32 on Wi-Fi from an iPhone without typing on the device.
//
// The device advertises over Bluetooth as PROV_<KIND>_<last 4 of MAC> and shows (or prints) a link,
//   https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=<one-time PIN>
// as a QR code. The iPhone Camera opens the Device Setup App Clip (narduk-enterprises/narduk-devices),
// which connects with Espressif's provisioning protocol (security 1, the PIN as proof of possession),
// lists the networks the device can see and sends the chosen one. A new PIN is made every time setup
// starts, so an old photo of the QR is useless.
//
//   clipsetup::onCredentials([](const char* ssid, const char* pass) { save them });
//   clipsetup::begin("DIAL");
//   ... show clipsetup::link() as a QR, watch clipsetup::state() ...
#pragma once
#include <stddef.h>
#include <stdint.h>

namespace clipsetup {

static const char* const kDefaultLinkBase = "https://setup.nard.uk/d";
// Every ClipSetup device advertises this Bluetooth service, so iOS's accessory setup sheet
// (AccessorySetupKit) can find it without a Bluetooth permission prompt.
static const char* const kServiceUUID = "4617760e-5308-4efe-b137-04e8fa0a5b61";

// Pure helpers, also used by the host tests.
// `PROV_` + KIND (A-Z, 0-9, at most 12) + `_` + the last two MAC bytes in hex.
void makeName(char* out, size_t size, const char* kind, const uint8_t mac[6]);
// 8 characters from an alphabet without look-alikes (no 0/o/1/l/i).
void makePin(char* out, size_t size, uint32_t (*random32)());
// `<base>?n=<name>&p=<pin>`. Name and PIN only ever hold URL-safe characters.
void makeLink(char* out, size_t size, const char* base, const char* name, const char* pin);

#ifdef ARDUINO
enum class State : uint8_t {
  Off,             // begin() not called
  Waiting,         // advertising; show the QR
  GotCredentials,  // the phone sent a network; joining
  Joined,          // on Wi-Fi; Bluetooth is released
  Failed,          // the join failed (see failure()); call begin() again to retry
};

// Called on the Wi-Fi event task as soon as the phone sends a network, before the join is tried.
// Store them yourself (Preferences); the password is never printed by this library.
void onCredentials(void (*callback)(const char* ssid, const char* password));

// Starts Bluetooth setup. `kind` is how the App Clip names the device ("DIAL" reads "Dial 4B98").
void begin(const char* kind, const char* linkBase = kDefaultLinkBase);

State state();
const char* name();
const char* pin();
const char* link();
const char* failure();  // "wrong password" or "network not found" after Failed

// Prints the link and a QR of it to a serial console (for boards without a screen).
void printQR();
#endif

}  // namespace clipsetup
