// Package wifi is the Wi-Fi side of setup: list networks, join one, report the result. The
// provisioning server only sees the Manager interface.
//
// Implementations:
//   - WPA talks to wpa_supplicant's control socket. On the Orange Pi Zero 2W's Armbian image
//     NetworkManager is not installed; netplan renders systemd-networkd and a wpa_supplicant for
//     wlan0 (linux/BENCH-EVIDENCE.md), so the control socket is the runtime interface.
//   - NetplanPersister writes the joined network as a netplan file so it survives a reboot.
//   - Fake is an in-memory Manager for tests and for running the peripheral without touching Wi-Fi.
package wifi

import (
	"context"
	"errors"
)

// AuthMode is wifi_constants.proto WifiAuthMode.
type AuthMode int32

const (
	AuthOpen AuthMode = iota
	AuthWEP
	AuthWPAPSK
	AuthWPA2PSK
	AuthWPAWPA2PSK
	AuthWPA2Enterprise
	AuthWPA3PSK
	AuthWPA2WPA3PSK
)

// State is wifi_constants.proto WifiStationState.
type State int32

const (
	Connected State = iota
	Connecting
	Disconnected
	ConnectionFailed
)

// FailReason is wifi_constants.proto WifiConnectFailedReason.
type FailReason int32

const (
	AuthError FailReason = iota
	NetworkNotFound
)

// Network is one scan result.
type Network struct {
	SSID    string // raw bytes; SSIDs are not always UTF-8
	BSSID   []byte // 6 bytes
	Channel uint32
	RSSI    int32 // dBm
	Auth    AuthMode
}

// Credentials is what the phone sends in CmdSetConfig.
type Credentials struct {
	SSID       string
	Passphrase string // empty for an open network
	BSSID      []byte // optional
	Channel    int32  // optional
}

// Status is the station state after a join, as reported in RespGetStatus.
type Status struct {
	State      State
	FailReason FailReason // when State is ConnectionFailed
	IP4        string     // when State is Connected
	SSID       string
	BSSID      []byte
	Channel    int32
	Auth       AuthMode
}

// Manager is a Wi-Fi backend.
type Manager interface {
	// Scan lists the networks in range.
	Scan(ctx context.Context) ([]Network, error)
	// Join connects to c and blocks until the station is connected with an address, the join has
	// clearly failed, or ctx ends. A failure the phone should show (wrong password, not found) is a
	// Status with State ConnectionFailed and a nil error.
	Join(ctx context.Context, c Credentials) (Status, error)
	// Status reports the current station state without changing anything.
	Status(ctx context.Context) (Status, error)
}

// ErrJoinDisabled is returned by a backend configured not to change the network.
var ErrJoinDisabled = errors.New("wifi: joining is disabled for this backend")

// ValidateCredentials applies 802.11 and WPA limits: an SSID of 1-32 bytes and a passphrase that is
// empty (open), 8-63 characters, or 64 hex digits (a raw PSK).
func ValidateCredentials(c Credentials) error {
	if n := len(c.SSID); n < 1 || n > 32 {
		return errors.New("wifi: SSID must be 1 to 32 bytes")
	}
	switch n := len(c.Passphrase); {
	case n == 0:
	case n >= 8 && n <= 63:
	case n == 64 && isHex(c.Passphrase):
	default:
		return errors.New("wifi: passphrase must be 8 to 63 characters or 64 hex digits")
	}
	if len(c.BSSID) != 0 && len(c.BSSID) != 6 {
		return errors.New("wifi: BSSID must be 6 bytes")
	}
	return nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
