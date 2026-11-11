package clipsetup

import (
	"strings"
	"testing"
)

// The cases are firmware/ClipSetup/test/test_links/test_main.cpp's, so the port and the ESP32
// library name and link a device identically.
var kMac = [6]byte{0xac, 0xa7, 0x04, 0x03, 0x4b, 0x98}

func TestNameMatchesFirmware(t *testing.T) {
	for kind, want := range map[string]string{
		"dial":                  "PROV_DIAL_4B98",
		"button-hub c3!":        "PROV_BUTTONHUBC3_4B98",
		"":                      "PROV_DEVICE_4B98",
		"averyveryverylongkind": "PROV_AVERYVERYVER_4B98",
		"MYBO":                  "PROV_MYBO_4B98",
	} {
		if got := Name(kind, kMac); got != want {
			t.Errorf("Name(%q) = %q, want %q", kind, got, want)
		}
		if n := len(Name(kind, kMac)); n > MaxNameLen {
			t.Errorf("Name(%q) is %d characters, over the clip's %d", kind, n, MaxNameLen)
		}
	}
}

func TestPINMatchesFirmware(t *testing.T) {
	var counter uint32
	next := func() uint32 { v := counter; counter++; return v }
	if got := PINFrom(next); got != "abcdefgh" {
		t.Fatalf("PINFrom(counter) = %q, want abcdefgh", got)
	}
	for i := 0; i < 50; i++ {
		if p := PINFrom(next); strings.ContainsAny(p, "0o1li") || len(p) != 8 {
			t.Fatalf("PIN %q has a look-alike or the wrong length", p)
		}
	}
	p, err := NewPIN(nil)
	if err != nil || len(p) != 8 || strings.ContainsAny(p, "0o1li") {
		t.Fatalf("NewPIN = %q, %v", p, err)
	}
}

func TestLinkMatchesFirmware(t *testing.T) {
	got := Link(DefaultLinkBase, "PROV_DIAL_4B98", "2wxv8xya")
	if want := "https://setup.nard.uk/d?n=PROV_DIAL_4B98&p=2wxv8xya"; got != want {
		t.Fatalf("Link = %q, want %q", got, want)
	}
}

// ESP-IDF's protocomm BLE transport puts the endpoint's 16-bit id in bytes 12-13 of the
// little-endian service UUID; for ClipSetup's service that is the "760e" group.
func TestEndpointUUID(t *testing.T) {
	for id, want := range map[uint16]string{
		0xFF50: "4617ff50-5308-4efe-b137-04e8fa0a5b61",
		0xFF53: "4617ff53-5308-4efe-b137-04e8fa0a5b61",
		0xFF54: "4617ff54-5308-4efe-b137-04e8fa0a5b61",
	} {
		got, err := EndpointUUID(strings.ToUpper(ServiceUUID), id)
		if err != nil || got != want {
			t.Errorf("EndpointUUID(%#x) = %q, %v; want %q", id, got, err, want)
		}
	}
	// The same derivation in ESP-IDF's byte order: little-endian UUID, uint16 at [12].
	le := []byte{0x61, 0x5b, 0x0a, 0xfa, 0xe8, 0x04, 0x37, 0xb1, 0xfe, 0x4e, 0x08, 0x53, 0x0e, 0x76, 0x17, 0x46}
	le[12], le[13] = 0x53, 0xff
	var s strings.Builder
	for i := 15; i >= 0; i-- {
		s.WriteString(strings.ToLower(string("0123456789abcdef"[le[i]>>4]) + string("0123456789abcdef"[le[i]&15])))
		if i == 12 || i == 10 || i == 8 || i == 6 {
			s.WriteByte('-')
		}
	}
	if got, _ := EndpointUUID(ServiceUUID, 0xFF53); got != s.String() {
		t.Fatalf("EndpointUUID = %q, ESP-IDF byte order gives %q", got, s.String())
	}
	if _, err := EndpointUUID("not-a-uuid", 1); err == nil {
		t.Fatal("EndpointUUID accepted a bad UUID")
	}
}
