#include <clip_setup.h>
#include <string.h>
#include <unity.h>

void setUp() {}
void tearDown() {}

static const uint8_t kMac[6] = {0xac, 0xa7, 0x04, 0x03, 0x4b, 0x98};

static void test_name_from_kind_and_mac() {
  char out[24];
  clipsetup::makeName(out, sizeof(out), "dial", kMac);
  TEST_ASSERT_EQUAL_STRING("PROV_DIAL_4B98", out);
  clipsetup::makeName(out, sizeof(out), "button-hub c3!", kMac);
  TEST_ASSERT_EQUAL_STRING("PROV_BUTTONHUBC3_4B98", out);
  clipsetup::makeName(out, sizeof(out), "", kMac);
  TEST_ASSERT_EQUAL_STRING("PROV_DEVICE_4B98", out);
  clipsetup::makeName(out, sizeof(out), "averyveryverylongkind", kMac);
  TEST_ASSERT_EQUAL_STRING("PROV_AVERYVERYVER_4B98", out);  // 12 characters of kind at most
}

static uint32_t counter = 0;
static uint32_t fakeRandom() { return counter++; }

static void test_pin_is_8_unambiguous_chars() {
  char pin[9];
  counter = 0;
  clipsetup::makePin(pin, sizeof(pin), fakeRandom);
  TEST_ASSERT_EQUAL(8, strlen(pin));
  TEST_ASSERT_EQUAL_STRING("abcdefgh", pin);
  for (int round = 0; round < 50; round++) {
    clipsetup::makePin(pin, sizeof(pin), fakeRandom);
    for (const char* c = pin; *c; c++) TEST_ASSERT_NULL(strchr("0o1li", *c));
  }
}

static void test_link_matches_the_app_clip() {
  char link[160];
  clipsetup::makeLink(link, sizeof(link), clipsetup::kDefaultLinkBase, "PROV_DIAL_4B98", "2wxv8xya");
  TEST_ASSERT_EQUAL_STRING("https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=2wxv8xya", link);
}

int main() {
  UNITY_BEGIN();
  RUN_TEST(test_name_from_kind_and_mac);
  RUN_TEST(test_pin_is_8_unambiguous_chars);
  RUN_TEST(test_link_matches_the_app_clip);
  return UNITY_END();
}
