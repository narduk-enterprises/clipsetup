// ClipSetup starter for a bare ESP32-C3 SuperMini: the smallest gadget that gets on Wi-Fi from an
// iPhone. Copy this folder to start a new one-job device.
//
// First boot (or BOOT held 3 s): the blue LED blinks fast and the serial console prints a QR.
// Scan it with the iPhone Camera, pick the network in the Device Setup App Clip, done. The LED
// goes solid once the board is on Wi-Fi, and it reconnects by itself on every boot after that.

#include <Arduino.h>
#include <Preferences.h>
#include <WiFi.h>
#include <clip_setup.h>  // ClipSetup

static const int kLed = 8;     // blue LED, active low
static const int kButton = 9;  // BOOT button, active low

static Preferences prefs;

static void saveCredentials(const char* ssid, const char* password) {
  prefs.putString("ssid", ssid);
  prefs.putString("pass", password);
  Serial.printf("Got Wi-Fi \"%s\" from the phone, joining\n", ssid);
}

static void startSetup() {
  clipsetup::onCredentials(saveCredentials);
  clipsetup::begin("C3");
  clipsetup::printQR();
}

void setup() {
  Serial.begin(115200);
  pinMode(kLed, OUTPUT);
  pinMode(kButton, INPUT_PULLUP);
  prefs.begin("wifi", false);
  WiFi.mode(WIFI_STA);
  WiFi.setTxPower(WIFI_POWER_8_5dBm);  // cheap SuperMini clones drop off Wi-Fi at full power
  String ssid = prefs.isKey("ssid") ? prefs.getString("ssid") : String();
  if (ssid.isEmpty()) {
    startSetup();
  } else {
    WiFi.begin(ssid.c_str(), prefs.getString("pass").c_str());
    Serial.printf("Joining \"%s\" (hold BOOT 3 s to set up again)\n", ssid.c_str());
  }
}

void loop() {
  uint32_t now = millis();

  static uint32_t heldSince = 0;
  if (digitalRead(kButton) == LOW) {
    if (!heldSince) heldSince = now;
    if (now - heldSince > 3000) {
      prefs.clear();
      Serial.println("Forgot Wi-Fi, restarting into setup");
      delay(200);
      ESP.restart();
    }
  } else {
    heldSince = 0;
  }

  static clipsetup::State last = clipsetup::State::Off;
  clipsetup::State setup = clipsetup::state();
  if (setup != last) {
    last = setup;
    if (setup == clipsetup::State::Failed) {
      Serial.printf("Couldn't join (%s). Restarting setup with a new code.\n", clipsetup::failure());
      prefs.clear();
      delay(1000);
      startSetup();
    }
  }

  bool online = WiFi.status() == WL_CONNECTED;
  static bool wasOnline = false;
  if (online && !wasOnline) Serial.printf("Online: %s, %s\n", WiFi.SSID().c_str(), WiFi.localIP().toString().c_str());
  wasOnline = online;

  // Fast blink: waiting for the phone. Slow blink: joining. Solid: online.
  bool on = online || (setup == clipsetup::State::Waiting ? (now / 150) % 2 : (now / 600) % 2);
  digitalWrite(kLed, on ? LOW : HIGH);
  delay(10);
}
