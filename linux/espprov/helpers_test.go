package espprov

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/narduk-enterprises/clipsetup/linux/wifi"
)

// drbg is a deterministic byte stream (SHA-256 of seed||counter) so device keys are fixed.
type drbg struct {
	seed []byte
	ctr  uint64
	buf  []byte
}

func newDRBG(seed string) *drbg { return &drbg{seed: []byte(seed)} }

func (d *drbg) Read(p []byte) (int, error) {
	for len(d.buf) < len(p) {
		var c [8]byte
		binary.BigEndian.PutUint64(c[:], d.ctr)
		d.ctr++
		h := sha256.Sum256(append(append([]byte(nil), d.seed...), c[:]...))
		d.buf = append(d.buf, h[:]...)
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

// goldenNetworks cover a negative RSSI, an open network, WPA3 and a non-ASCII name.
func goldenNetworks() []wifi.Network {
	return []wifi.Network{
		{SSID: "bench", BSSID: []byte{0x02, 0, 0, 0, 0, 1}, Channel: 6, RSSI: -48, Auth: wifi.AuthWPA2PSK},
		{SSID: "Café 5G", BSSID: []byte{0x02, 0, 0, 0, 0, 2}, Channel: 36, RSSI: -61, Auth: wifi.AuthWPA2WPA3PSK},
		{SSID: "marina guest", BSSID: []byte{0x02, 0, 0, 0, 0, 3}, Channel: 11, RSSI: -77, Auth: wifi.AuthOpen},
		{SSID: "dock-ap", BSSID: []byte{0x02, 0, 0, 0, 0, 4}, Channel: 1, RSSI: -80, Auth: wifi.AuthWPA3PSK},
		{SSID: "neighbour", BSSID: []byte{0x02, 0, 0, 0, 0, 5}, Channel: 149, RSSI: -90, Auth: wifi.AuthWPAWPA2PSK},
	}
}

// newTestServer is the device every protocol test talks to: a fake radio, pinned randomness and
// one custom endpoint that reports the size of what it got.
func newTestServer(t *testing.T, pop string) (*Server, *wifi.Fake) {
	t.Helper()
	fake := &wifi.Fake{Networks: goldenNetworks(), Passwords: map[string]string{"bench": "correct horse"}}
	s, err := NewServer(Config{PoP: pop, WiFi: fake, Rand: newDRBG("clipsetup golden device"), ApplyWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Register("custom-data", func(ctx context.Context, peer string, req []byte) ([]byte, error) {
		return json.Marshal(map[string]int{"received": len(req)})
	}); err != nil {
		t.Fatal(err)
	}
	return s, fake
}

func direct(s *Server, peer string) Transport {
	return func(ctx context.Context, endpoint string, req []byte) ([]byte, error) {
		return s.Handle(ctx, peer, endpoint, req)
	}
}
