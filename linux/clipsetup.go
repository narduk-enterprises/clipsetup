// Package clipsetup holds the pieces of ClipSetup that do not depend on a radio: the Bluetooth name,
// the PIN and the link, ported from firmware/ClipSetup so a Linux device and an ESP32 present
// themselves to the App Clip the same way.
//
// The protocol itself is in package espprov, the BlueZ peripheral in package bluez and the Wi-Fi
// backends in package wifi. cmd/clipsetup-example wires them together.
package clipsetup

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ServiceUUID is advertised by every ClipSetup device. iOS's AccessorySetupKit picker matches on it
// (firmware/ClipSetup/src/clip_setup.h).
const ServiceUUID = "4617760e-5308-4efe-b137-04e8fa0a5b61"

// DefaultLinkBase is the App Clip's link (firmware kDefaultLinkBase).
const DefaultLinkBase = "https://setup.nard.uk/d"

// MaxNameLen is the longest name the App Clip accepts in a link (ios SetupLink: at most 29).
const MaxNameLen = 29

const pinAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// Name returns PROV_<KIND>_<last two address bytes in hex>, as firmware makeName does: KIND is
// upper-cased, keeps only A-Z and 0-9, is cut to 12 characters and defaults to DEVICE.
// addr is a Bluetooth or MAC address in transmission order (the last two bytes are addr[4:6]).
func Name(kind string, addr [6]byte) string {
	var clean strings.Builder
	for _, r := range kind {
		if clean.Len() == 12 {
			break
		}
		if r >= 'a' && r <= 'z' {
			r = r - 'a' + 'A'
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			clean.WriteRune(r)
		}
	}
	k := clean.String()
	if k == "" {
		k = "DEVICE"
	}
	return fmt.Sprintf("PROV_%s_%02X%02X", k, addr[4], addr[5])
}

// PINFrom makes an 8-character PIN from an alphabet without look-alikes, drawing one 32-bit value
// per character from next, exactly as firmware makePin does. It exists so the firmware's own test
// vectors can check this port.
func PINFrom(next func() uint32) string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = pinAlphabet[next()%uint32(len(pinAlphabet))]
	}
	return string(b)
}

// NewPIN makes a PIN from r (crypto/rand when nil). The modulo bias over 31 symbols from a 32-bit
// value is below 2^-27 per character, as in the firmware.
func NewPIN(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var buf [32]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return "", err
	}
	i := 0
	return PINFrom(func() uint32 {
		v := binary.LittleEndian.Uint32(buf[i*4:])
		i++
		return v
	}), nil
}

// Link returns base?n=<name>&p=<pin>, the link the App Clip opens (firmware makeLink). Name and PIN
// only ever hold URL-safe characters, but they are escaped anyway.
func Link(base, name, pin string) string {
	return base + "?n=" + url.QueryEscape(name) + "&p=" + url.QueryEscape(pin)
}

// EndpointUUID derives a characteristic UUID from a 128-bit service UUID the way ESP-IDF's
// protocomm BLE transport does: the 16-bit endpoint id replaces bytes 12-13 of the little-endian
// UUID, which are the third and fourth bytes of the string form ("4617760e" -> "4617ff53").
func EndpointUUID(service string, id uint16) (string, error) {
	s := strings.ToLower(service)
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return "", fmt.Errorf("clipsetup: %q is not a 128-bit UUID", service)
	}
	return fmt.Sprintf("%s%04x%s", s[:4], id, s[8:]), nil
}
