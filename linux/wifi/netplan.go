package wifi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// NetplanPersister saves a joined network as its own netplan file, so the network survives a
// reboot on hosts where netplan regenerates wpa_supplicant's configuration at every boot. netplan
// merges the file's access point into the interface's other access points. It writes the file
// only; it does not run `netplan apply`, because the network is already joined at runtime.
type NetplanPersister struct {
	Dir   string // default /etc/netplan
	Iface string // default wlan0
	// File is the file name; default 90-clipsetup-<iface>.yaml. Each save replaces it, so only the
	// most recent network set up over Bluetooth is kept here.
	File string
}

// Path is the file Save writes.
func (p NetplanPersister) Path() string {
	dir, iface, file := p.Dir, p.Iface, p.File
	if dir == "" {
		dir = "/etc/netplan"
	}
	if iface == "" {
		iface = "wlan0"
	}
	if file == "" {
		file = "90-clipsetup-" + iface + ".yaml"
	}
	return filepath.Join(dir, file)
}

// Save writes c with mode 0600 (netplan warns about readable files holding passwords), replacing
// the previous file atomically.
func (p NetplanPersister) Save(c Credentials) error {
	body, err := p.Render(c)
	if err != nil {
		return err
	}
	path := p.Path()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".clipsetup-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Render returns the YAML Save writes.
func (p NetplanPersister) Render(c Credentials) (string, error) {
	if err := ValidateCredentials(c); err != nil {
		return "", err
	}
	if !utf8.ValidString(c.SSID) || !utf8.ValidString(c.Passphrase) {
		return "", errors.New("wifi: netplan can only store UTF-8 network names and passwords")
	}
	iface := p.Iface
	if iface == "" {
		iface = "wlan0"
	}
	var b strings.Builder
	b.WriteString("# Written by clipsetup: the network last set up over Bluetooth.\n")
	b.WriteString("network:\n  version: 2\n  wifis:\n")
	fmt.Fprintf(&b, "    %s:\n      dhcp4: true\n      access-points:\n", yamlQuote(iface))
	fmt.Fprintf(&b, "        %s:", yamlQuote(c.SSID))
	if c.Passphrase == "" {
		b.WriteString(" {}\n")
	} else {
		fmt.Fprintf(&b, "\n          password: %s\n", yamlQuote(c.Passphrase))
	}
	return b.String(), nil
}

// yamlQuote writes a YAML double-quoted scalar.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
