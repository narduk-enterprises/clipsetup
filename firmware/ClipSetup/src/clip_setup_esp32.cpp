// The on-device half: Espressif BLE provisioning through the Arduino core's WiFiProv.
#ifdef ARDUINO
#include <Arduino.h>
#include <WiFi.h>
#include <WiFiProv.h>
#include <esp_mac.h>
#include <esp_random.h>
#if __has_include("qrcode.h")
#include "qrcode.h"
#define CLIPSETUP_HAS_QR 1
#endif

#include "clip_setup.h"

namespace clipsetup {

static volatile State current = State::Off;
static void (*credentialsCallback)(const char*, const char*) = nullptr;
static char deviceName[24], devicePin[9], deviceLink[160];
static const char* failureReason = "";
static bool listening = false;

static void onEvent(arduino_event_t* e) {
  switch (e->event_id) {
    case ARDUINO_EVENT_PROV_CRED_RECV:
      if (credentialsCallback)
        credentialsCallback((const char*)e->event_info.prov_cred_recv.ssid,
                            (const char*)e->event_info.prov_cred_recv.password);
      current = State::GotCredentials;
      break;
    case ARDUINO_EVENT_PROV_CRED_SUCCESS:
      current = State::Joined;
      break;
    case ARDUINO_EVENT_PROV_CRED_FAIL:
      failureReason = e->event_info.prov_fail_reason == NETWORK_PROV_WIFI_STA_AUTH_ERROR ? "wrong password"
                                                                                         : "network not found";
      current = State::Failed;
      break;
    default:
      break;
  }
}

static uint32_t hardwareRandom() { return esp_random(); }

void onCredentials(void (*callback)(const char*, const char*)) { credentialsCallback = callback; }

void begin(const char* kind, const char* linkBase) {
  uint8_t mac[6];
  esp_read_mac(mac, ESP_MAC_WIFI_STA);  // from eFuse: WiFi.macAddress() is all zeros before Wi-Fi starts
  makeName(deviceName, sizeof(deviceName), kind, mac);
  makePin(devicePin, sizeof(devicePin), hardwareRandom);
  makeLink(deviceLink, sizeof(deviceLink), linkBase, deviceName, devicePin);
  failureReason = "";
  if (!listening) {
    WiFi.onEvent(onEvent);
    listening = true;
  }
  current = State::Waiting;
  // FREE_BLE releases the Bluetooth stack's memory once setup ends.
  // kServiceUUID, least significant byte first as the provisioning manager expects.
  static uint8_t serviceUUID[16] = {0x61, 0x5b, 0x0a, 0xfa, 0xe8, 0x04, 0x37, 0xb1,
                                    0xfe, 0x4e, 0x08, 0x53, 0x0e, 0x76, 0x17, 0x46};
  WiFiProv.beginProvision(NETWORK_PROV_SCHEME_BLE, NETWORK_PROV_SCHEME_HANDLER_FREE_BLE, NETWORK_PROV_SECURITY_1,
                          devicePin, deviceName, nullptr, serviceUUID, true);
}

State state() { return current; }
const char* name() { return deviceName; }
const char* pin() { return devicePin; }
const char* link() { return deviceLink; }
const char* failure() { return failureReason; }

#ifdef CLIPSETUP_HAS_QR
static void printModules(esp_qrcode_handle_t qr) {
  int size = esp_qrcode_get_size(qr);
  // Two characters per module, with a quiet zone, so phone cameras read it off a terminal.
  for (int y = -2; y < size + 2; y++) {
    for (int x = -2; x < size + 2; x++) Serial.print(esp_qrcode_get_module(qr, x, y) ? "  " : "██");
    Serial.println();
  }
}
#endif

void printQR() {
  Serial.printf("Wi-Fi setup: scan with the iPhone Camera, or open %s\n", deviceLink);
#ifdef CLIPSETUP_HAS_QR
  esp_qrcode_config_t cfg = ESP_QRCODE_CONFIG_DEFAULT();
  cfg.display_func = printModules;
  esp_qrcode_generate(&cfg, deviceLink);
#endif
}

}  // namespace clipsetup
#endif
