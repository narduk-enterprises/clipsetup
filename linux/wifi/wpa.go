package wifi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// WPA is a Manager over wpa_supplicant's control interface (the protocol wpa_cli speaks), for
// hosts where wpa_supplicant owns the interface directly, such as netplan + systemd-networkd.
// Using the socket needs root, or membership of the group the socket directory allows.
type WPA struct {
	CtrlDir string // default /run/wpa_supplicant
	Iface   string // default wlan0

	// TriggerScan sends SCAN and waits for fresh results. When false, Scan only reads the results
	// wpa_supplicant already holds (SCAN_RESULTS), which changes nothing on the host.
	TriggerScan bool
	// AllowJoin must be set for Join to change the network; otherwise Join returns ErrJoinDisabled.
	AllowJoin bool
	// Persist, when set, is called after a successful join so the network survives a reboot
	// (see NetplanPersister). A Persist error is returned with the connected Status.
	Persist func(Credentials) error

	ScanTimeout time.Duration // default 15 s
	JoinTimeout time.Duration // association, default 30 s
	IPTimeout   time.Duration // DHCP after association, default 20 s
	LocalDir    string        // where the client socket is bound; default os.TempDir()
}

var localSeq atomic.Uint64

func (w *WPA) ctrlPath() string {
	dir, iface := w.CtrlDir, w.Iface
	if dir == "" {
		dir = "/run/wpa_supplicant"
	}
	if iface == "" {
		iface = "wlan0"
	}
	return filepath.Join(dir, iface)
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Scan lists networks, strongest first.
func (w *WPA) Scan(ctx context.Context) ([]Network, error) {
	c, err := w.dial()
	if err != nil {
		return nil, err
	}
	defer c.close()
	if w.TriggerScan {
		if err := c.expectOK(ctx, "ATTACH"); err != nil {
			return nil, err
		}
		reply, err := c.request(ctx, "SCAN")
		if err != nil {
			return nil, err
		}
		// FAIL-BUSY means a scan is already running; its results arrive the same way.
		if reply != "OK" && reply != "FAIL-BUSY" {
			return nil, fmt.Errorf("wifi: SCAN: %s", reply)
		}
		sctx, cancel := context.WithTimeout(ctx, orDefault(w.ScanTimeout, 15*time.Second))
		_, err = c.waitEvent(sctx, func(ev string) bool {
			return strings.HasPrefix(ev, "CTRL-EVENT-SCAN-RESULTS") || strings.HasPrefix(ev, "CTRL-EVENT-SCAN-FAILED")
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("wifi: waiting for scan results: %w", err)
		}
	}
	out, err := c.request(ctx, "SCAN_RESULTS")
	if err != nil {
		return nil, err
	}
	return ParseScanResults(out), nil
}

// Join adds c to wpa_supplicant's running configuration and selects it. On failure the network is
// removed and the others are enabled again.
func (w *WPA) Join(ctx context.Context, cr Credentials) (Status, error) {
	if !w.AllowJoin {
		return Status{}, ErrJoinDisabled
	}
	if err := ValidateCredentials(cr); err != nil {
		return Status{}, err
	}
	for i := 0; i < len(cr.Passphrase); i++ {
		if cr.Passphrase[i] < 0x20 || cr.Passphrase[i] > 0x7e {
			return Status{}, errors.New("wifi: passphrase must be printable ASCII")
		}
	}
	c, err := w.dial()
	if err != nil {
		return Status{}, err
	}
	defer c.close()
	if err := c.expectOK(ctx, "ATTACH"); err != nil {
		return Status{}, err
	}
	idStr, err := c.request(ctx, "ADD_NETWORK")
	if err != nil {
		return Status{}, err
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return Status{}, fmt.Errorf("wifi: ADD_NETWORK: %s", idStr)
	}
	undo := func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.request(bg, fmt.Sprintf("REMOVE_NETWORK %d", id))
		_, _ = c.request(bg, "ENABLE_NETWORK all")
		_, _ = c.request(bg, "REASSOCIATE")
	}
	set := func(k, v string) error { return c.expectOK(ctx, fmt.Sprintf("SET_NETWORK %d %s %s", id, k, v)) }

	// An unquoted value is hex, so any SSID bytes are safe.
	steps := [][2]string{{"ssid", hex.EncodeToString([]byte(cr.SSID))}, {"scan_ssid", "1"}}
	switch {
	case cr.Passphrase == "":
		steps = append(steps, [2]string{"key_mgmt", "NONE"})
	case len(cr.Passphrase) == 64:
		steps = append(steps, [2]string{"psk", cr.Passphrase})
	default:
		// wpa_supplicant takes everything between the first and the last quote.
		steps = append(steps, [2]string{"psk", `"` + cr.Passphrase + `"`})
	}
	if len(cr.BSSID) == 6 {
		steps = append(steps, [2]string{"bssid", net.HardwareAddr(cr.BSSID).String()})
	}
	for _, s := range steps {
		if err := set(s[0], s[1]); err != nil {
			undo()
			return Status{}, err
		}
	}
	if cr.Passphrase != "" {
		// Allow WPA3-SAE where the driver and the network have it; older builds refuse SAE, and
		// then the default WPA-PSK stays.
		if set("key_mgmt", "WPA-PSK SAE") == nil {
			_ = set("ieee80211w", "1")
		}
	}
	if err := c.expectOK(ctx, fmt.Sprintf("SELECT_NETWORK %d", id)); err != nil {
		undo()
		return Status{}, err
	}

	jctx, cancel := context.WithTimeout(ctx, orDefault(w.JoinTimeout, 30*time.Second))
	ev, err := c.waitEvent(jctx, func(ev string) bool {
		return strings.HasPrefix(ev, "CTRL-EVENT-CONNECTED") ||
			strings.HasPrefix(ev, "CTRL-EVENT-SSID-TEMP-DISABLED") ||
			strings.HasPrefix(ev, "CTRL-EVENT-NETWORK-NOT-FOUND") ||
			strings.HasPrefix(ev, "WPA: 4-Way Handshake failed")
	})
	cancel()
	switch {
	case err != nil:
		undo()
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
		return Status{State: ConnectionFailed, FailReason: NetworkNotFound}, nil
	case strings.HasPrefix(ev, "CTRL-EVENT-NETWORK-NOT-FOUND"):
		undo()
		return Status{State: ConnectionFailed, FailReason: NetworkNotFound}, nil
	case !strings.HasPrefix(ev, "CTRL-EVENT-CONNECTED"):
		undo()
		return Status{State: ConnectionFailed, FailReason: AuthError}, nil
	}

	// Associated; wait for systemd-networkd's DHCP lease to show up in STATUS.
	deadline := time.Now().Add(orDefault(w.IPTimeout, 20*time.Second))
	for {
		st, err := c.status(ctx)
		if err == nil && st.State == Connected && st.IP4 != "" {
			if w.Persist != nil {
				if perr := w.Persist(cr); perr != nil {
					return st, fmt.Errorf("wifi: joined but not saved for the next boot: %w", perr)
				}
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			// Associated without an address. Report it as connected-without-IP rather than a
			// failure: the phone's credentials were right.
			st.State = Connected
			return st, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Status reads wpa_supplicant's STATUS.
func (w *WPA) Status(ctx context.Context) (Status, error) {
	c, err := w.dial()
	if err != nil {
		return Status{}, err
	}
	defer c.close()
	return c.status(ctx)
}

// ctrlConn is one client socket. Replies and, after ATTACH, unsolicited events ("<N>TEXT")
// arrive on the same socket; request sets events aside for waitEvent.
type ctrlConn struct {
	c      *net.UnixConn
	local  string
	events []string
	buf    []byte
}

func (w *WPA) dial() (*ctrlConn, error) {
	dir := w.LocalDir
	if dir == "" {
		dir = os.TempDir()
	}
	local := filepath.Join(dir, fmt.Sprintf("clipsetup-wpa-%d-%d", os.Getpid(), localSeq.Add(1)))
	_ = os.Remove(local)
	c, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"},
		&net.UnixAddr{Name: w.ctrlPath(), Net: "unixgram"})
	if err != nil {
		_ = os.Remove(local)
		return nil, fmt.Errorf("wifi: wpa_supplicant control socket %s: %w", w.ctrlPath(), err)
	}
	return &ctrlConn{c: c, local: local, buf: make([]byte, 65536)}, nil
}

func (c *ctrlConn) close() {
	_ = c.c.SetDeadline(time.Now().Add(time.Second))
	_, _ = c.c.Write([]byte("DETACH"))
	_ = c.c.Close()
	_ = os.Remove(c.local)
}

func (c *ctrlConn) read(ctx context.Context) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.c.SetReadDeadline(deadline)
	n, err := c.c.Read(c.buf)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return string(c.buf[:n]), nil
}

func (c *ctrlConn) request(ctx context.Context, cmd string) (string, error) {
	_ = c.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.c.Write([]byte(cmd)); err != nil {
		return "", err
	}
	for {
		msg, err := c.read(ctx)
		if err != nil {
			return "", fmt.Errorf("wifi: %s: %w", strings.Fields(cmd)[0], err)
		}
		if ev, ok := eventText(msg); ok {
			c.events = append(c.events, ev)
			continue
		}
		return strings.TrimRight(msg, "\n"), nil
	}
}

func (c *ctrlConn) expectOK(ctx context.Context, cmd string) error {
	reply, err := c.request(ctx, cmd)
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("wifi: %s: %s", strings.Fields(cmd)[0], reply)
	}
	return nil
}

func (c *ctrlConn) waitEvent(ctx context.Context, match func(string) bool) (string, error) {
	for {
		for i, ev := range c.events {
			if match(ev) {
				c.events = c.events[i+1:]
				return ev, nil
			}
		}
		c.events = c.events[:0]
		msg, err := c.read(ctx)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() == nil {
				continue
			}
			return "", err
		}
		if ev, ok := eventText(msg); ok {
			c.events = append(c.events, ev)
		}
	}
}

// eventText strips the "<level>" prefix wpa_supplicant puts on unsolicited messages.
func eventText(msg string) (string, bool) {
	if len(msg) < 3 || msg[0] != '<' {
		return "", false
	}
	end := strings.IndexByte(msg, '>')
	if end < 0 {
		return "", false
	}
	return strings.TrimRight(msg[end+1:], "\n"), true
}

func (c *ctrlConn) status(ctx context.Context) (Status, error) {
	out, err := c.request(ctx, "STATUS")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(out), nil
}

// ParseStatus reads STATUS output.
func ParseStatus(out string) Status {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[k] = v
		}
	}
	st := Status{State: Disconnected}
	switch kv["wpa_state"] {
	case "COMPLETED":
		st.State = Connected
	case "AUTHENTICATING", "ASSOCIATING", "ASSOCIATED", "4WAY_HANDSHAKE", "GROUP_HANDSHAKE":
		st.State = Connecting
	}
	st.IP4 = kv["ip_address"]
	st.SSID = unescapeSSID(kv["ssid"])
	if hw, err := net.ParseMAC(kv["bssid"]); err == nil {
		st.BSSID = hw
	}
	if f, err := strconv.Atoi(kv["freq"]); err == nil {
		st.Channel = int32(FreqToChannel(f))
	}
	st.Auth = authFromKeyMgmt(kv["key_mgmt"])
	return st
}

func authFromKeyMgmt(k string) AuthMode {
	switch {
	case k == "" || k == "NONE":
		return AuthOpen
	case strings.Contains(k, "SAE"):
		return AuthWPA3PSK
	case strings.Contains(k, "WPA2-PSK"):
		return AuthWPA2PSK
	case strings.Contains(k, "WPA-PSK"):
		return AuthWPAPSK
	case strings.Contains(k, "EAP") || strings.Contains(k, "802.1X"):
		return AuthWPA2Enterprise
	}
	return AuthOpen
}

// ParseScanResults reads SCAN_RESULTS output ("bssid / frequency / signal level / flags / ssid"
// then one tab-separated line per BSS), strongest first.
func ParseScanResults(out string) []Network {
	var nets []Network
	for _, line := range strings.Split(out, "\n") {
		cols := strings.SplitN(line, "\t", 5)
		if len(cols) < 4 {
			continue
		}
		hw, err := net.ParseMAC(cols[0])
		if err != nil {
			continue // the header
		}
		freq, _ := strconv.Atoi(cols[1])
		sig, _ := strconv.Atoi(cols[2])
		n := Network{BSSID: hw, Channel: uint32(FreqToChannel(freq)), RSSI: int32(sig), Auth: AuthFromFlags(cols[3])}
		if len(cols) == 5 {
			n.SSID = unescapeSSID(cols[4])
		}
		nets = append(nets, n)
	}
	sort.SliceStable(nets, func(i, j int) bool { return nets[i].RSSI > nets[j].RSSI })
	return nets
}

// AuthFromFlags maps SCAN_RESULTS flags ("[WPA2-PSK-CCMP][ESS]") to WifiAuthMode.
func AuthFromFlags(flags string) AuthMode {
	has := func(s string) bool { return strings.Contains(flags, s) }
	sae := has("SAE")
	wpa2psk := has("[WPA2-PSK") || has("[RSN-PSK") || (has("[WPA2-") && has("PSK") && !has("EAP"))
	wpapsk := has("[WPA-PSK")
	switch {
	case sae && (wpa2psk || wpapsk):
		return AuthWPA2WPA3PSK
	case sae:
		return AuthWPA3PSK
	case wpa2psk && wpapsk:
		return AuthWPAWPA2PSK
	case wpa2psk:
		return AuthWPA2PSK
	case wpapsk:
		return AuthWPAPSK
	case has("EAP"):
		return AuthWPA2Enterprise
	case has("WEP"):
		return AuthWEP
	}
	return AuthOpen
}

// FreqToChannel converts MHz to an 802.11 channel number (0 when unknown).
func FreqToChannel(f int) int {
	switch {
	case f == 2484:
		return 14
	case f >= 2412 && f <= 2472:
		return (f - 2407) / 5
	case f >= 5160 && f <= 5885:
		return (f - 5000) / 5
	case f == 5935:
		return 2
	case f >= 5955 && f <= 7115:
		return (f - 5950) / 5
	}
	return 0
}

// unescapeSSID reverses wpa_supplicant's printf_encode: \\ \" \e \n \r \t and \xNN.
func unescapeSSID(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case '\\', '"':
			b.WriteByte(s[i])
		case 'e':
			b.WriteByte(0x1b)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
