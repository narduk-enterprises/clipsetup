#include "clip_setup.h"

#include <stdio.h>
#include <string.h>

namespace clipsetup {

void makeName(char* out, size_t size, const char* kind, const uint8_t mac[6]) {
  char clean[13];
  size_t n = 0;
  for (const char* c = kind ? kind : ""; *c && n < sizeof(clean) - 1; c++) {
    char ch = *c;
    if (ch >= 'a' && ch <= 'z') ch = ch - 'a' + 'A';
    if ((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) clean[n++] = ch;
  }
  clean[n] = 0;
  if (n == 0) strcpy(clean, "DEVICE");
  snprintf(out, size, "PROV_%s_%02X%02X", clean, mac[4], mac[5]);
}

void makePin(char* out, size_t size, uint32_t (*random32)()) {
  static const char alphabet[] = "abcdefghjkmnpqrstuvwxyz23456789";
  size_t len = size > 8 ? 8 : (size ? size - 1 : 0);
  for (size_t i = 0; i < len; i++) out[i] = alphabet[random32() % (sizeof(alphabet) - 1)];
  if (size) out[len] = 0;
}

void makeLink(char* out, size_t size, const char* base, const char* name, const char* pin) {
  snprintf(out, size, "%s?n=%s&p=%s", base, name, pin);
}

}  // namespace clipsetup
