package wifi

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// SCAN_RESULTS as wpa_supplicant 2.10 prints it; names are invented, escapes are printf_encode's.
const scanResults = "bssid / frequency / signal level / flags / ssid\n" +
	"02:00:00:00:00:01\t2437\t-48\t[WPA2-PSK-CCMP][ESS]\tbench\n" +
	"02:00:00:00:00:02\t5180\t-61\t[WPA2-PSK+SAE-CCMP][ESS]\tCaf\\xc3\\xa9 5G\n" +
	"02:00:00:00:00:03\t2462\t-77\t[ESS]\tmarina guest\n" +
	"02:00:00:00:00:04\t2412\t-80\t[RSN-SAE-CCMP][ESS]\tdock-ap\n" +
	"02:00:00:00:00:05\t5745\t-90\t[WPA-PSK-CCMP+TKIP][WPA2-PSK-CCMP+TKIP][ESS]\ta \\\"quoted\\\" \\\\ name\n" +
	"02:00:00:00:00:06\t2412\t-40\t[WPA2-EAP-CCMP][ESS]\t\n" +
	"02:00:00:00:00:07\t5955\t-85\t[WEP][ESS]\told\n"

func TestParseScanResults(t *testing.T) {
	nets := ParseScanResults(scanResults)
	want := []struct {
		ssid string
		ch   uint32
		rssi int32
		auth AuthMode
	}{
		{"", 1, -40, AuthWPA2Enterprise},
		{"bench", 6, -48, AuthWPA2PSK},
		{"Café 5G", 36, -61, AuthWPA2WPA3PSK},
		{"marina guest", 11, -77, AuthOpen},
		{"dock-ap", 1, -80, AuthWPA3PSK},
		{"old", 1, -85, AuthWEP},
		{`a "quoted" \ name`, 149, -90, AuthWPAWPA2PSK},
	}
	if len(nets) != len(want) {
		t.Fatalf("%d networks, want %d", len(nets), len(want))
	}
	for i, w := range want {
		n := nets[i]
		if n.SSID != w.ssid || n.Channel != w.ch || n.RSSI != w.rssi || n.Auth != w.auth || len(n.BSSID) != 6 {
			t.Errorf("network %d = %+v, want %+v", i, n, w)
		}
	}
}

func TestParseStatus(t *testing.T) {
	st := ParseStatus("bssid=02:00:00:00:00:01\nfreq=2437\nssid=bench\nid=0\nmode=station\nkey_mgmt=WPA2-PSK\nwpa_state=COMPLETED\nip_address=192.168.1.206\n")
	if st.State != Connected || st.IP4 != "192.168.1.206" || st.SSID != "bench" || st.Channel != 6 || st.Auth != AuthWPA2PSK {
		t.Fatalf("status %+v", st)
	}
	if st := ParseStatus("wpa_state=4WAY_HANDSHAKE\n"); st.State != Connecting {
		t.Fatalf("handshake state %+v", st)
	}
	if st := ParseStatus("wpa_state=DISCONNECTED\n"); st.State != Disconnected {
		t.Fatalf("disconnected state %+v", st)
	}
}

func TestValidateCredentials(t *testing.T) {
	ok := []Credentials{{SSID: "x"}, {SSID: "x", Passphrase: "12345678"}, {SSID: strings.Repeat("s", 32), Passphrase: strings.Repeat("ab", 32)}}
	bad := []Credentials{{}, {SSID: strings.Repeat("s", 33)}, {SSID: "x", Passphrase: "1234567"}, {SSID: "x", Passphrase: strings.Repeat("z", 64)}, {SSID: "x", BSSID: []byte{1}}}
	for _, c := range ok {
		if err := ValidateCredentials(c); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, c := range bad {
		if ValidateCredentials(c) == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

// fakeSupplicant answers wpa_supplicant control commands on a unixgram socket.
type fakeSupplicant struct {
	conn     *net.UnixConn
	mu       sync.Mutex
	got      []string
	joinWith string // the event sent after SELECT_NETWORK
	ip       string
}

func startSupplicant(t *testing.T, dir string, joinWith string) *fakeSupplicant {
	t.Helper()
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(dir, "wlan0"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSupplicant{conn: c, joinWith: joinWith}
	t.Cleanup(func() { c.Close() })
	go f.serve()
	return f
}

func (f *fakeSupplicant) serve() {
	buf := make([]byte, 4096)
	for {
		n, from, err := f.conn.ReadFromUnix(buf)
		if err != nil {
			return
		}
		cmd := string(buf[:n])
		f.mu.Lock()
		f.got = append(f.got, cmd)
		f.mu.Unlock()
		reply := func(s string) { _, _ = f.conn.WriteToUnix([]byte(s), from) }
		switch {
		case cmd == "SCAN":
			reply("OK\n")
			reply("<3>CTRL-EVENT-SCAN-STARTED ")
			reply("<3>CTRL-EVENT-SCAN-RESULTS ")
		case cmd == "SCAN_RESULTS":
			reply(scanResults)
		case cmd == "ADD_NETWORK":
			reply("3\n")
		case cmd == "STATUS":
			if f.ip != "" {
				reply("wpa_state=COMPLETED\nssid=bench\nfreq=2437\nkey_mgmt=WPA2-PSK\nip_address=" + f.ip + "\n")
			} else {
				reply("wpa_state=DISCONNECTED\n")
			}
		case strings.HasPrefix(cmd, "SELECT_NETWORK"):
			reply("OK\n")
			reply("<3>Trying to associate with 02:00:00:00:00:01")
			reply("<3>" + f.joinWith)
			if strings.HasPrefix(f.joinWith, "CTRL-EVENT-CONNECTED") {
				f.ip = "192.168.1.50"
			}
		case cmd == "DETACH":
		default:
			reply("OK\n")
		}
	}
}

func (f *fakeSupplicant) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func newTestWPA(t *testing.T, joinWith string) (*WPA, *fakeSupplicant) {
	dir, err := os.MkdirTemp("", "cs") // short: unix socket paths are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := startSupplicant(t, dir, joinWith)
	return &WPA{CtrlDir: dir, Iface: "wlan0", LocalDir: dir, JoinTimeout: 2 * time.Second, IPTimeout: 2 * time.Second}, f
}

func TestWPAScanCachedAndFresh(t *testing.T) {
	w, f := newTestWPA(t, "")
	nets, err := w.Scan(context.Background())
	if err != nil || len(nets) != 7 {
		t.Fatalf("cached scan: %d, %v", len(nets), err)
	}
	if strings.Join(f.sent(), ",") != "SCAN_RESULTS,DETACH" {
		t.Fatalf("cached scan sent %v", f.sent())
	}
	w.TriggerScan = true
	if nets, err = w.Scan(context.Background()); err != nil || len(nets) != 7 {
		t.Fatalf("fresh scan: %d, %v", len(nets), err)
	}
	entries, _ := os.ReadDir(w.LocalDir)
	if len(entries) != 1 { // only the fake's own socket; client sockets are removed
		t.Fatalf("left %d entries in the socket dir", len(entries))
	}
}

func TestWPAJoin(t *testing.T) {
	ctx := context.Background()
	w, _ := newTestWPA(t, "")
	if _, err := w.Join(ctx, Credentials{SSID: "bench", Passphrase: "correct horse"}); !errors.Is(err, ErrJoinDisabled) {
		t.Fatalf("join without AllowJoin: %v", err)
	}

	w, f := newTestWPA(t, "CTRL-EVENT-CONNECTED - Connection to 02:00:00:00:00:01 completed [id=3 id_str=]")
	w.AllowJoin = true
	var saved Credentials
	w.Persist = func(c Credentials) error { saved = c; return nil }
	st, err := w.Join(ctx, Credentials{SSID: "bench", Passphrase: `pa"ss word`})
	if err != nil || st.State != Connected || st.IP4 != "192.168.1.50" || saved.SSID != "bench" {
		t.Fatalf("join: %+v, %v, saved %+v", st, err, saved)
	}
	sent := strings.Join(f.sent(), "\n")
	for _, want := range []string{"SET_NETWORK 3 ssid 62656e6368", `SET_NETWORK 3 psk "pa"ss word"`, "SELECT_NETWORK 3"} {
		if !strings.Contains(sent, want) {
			t.Errorf("did not send %q; sent:\n%s", want, sent)
		}
	}

	for event, reason := range map[string]FailReason{
		"CTRL-EVENT-SSID-TEMP-DISABLED id=3 ssid=\"bench\" auth_failures=1 duration=10 reason=WRONG_KEY": AuthError,
		"CTRL-EVENT-NETWORK-NOT-FOUND": NetworkNotFound,
	} {
		w, f := newTestWPA(t, event)
		w.AllowJoin = true
		st, err := w.Join(ctx, Credentials{SSID: "bench", Passphrase: "wrong horse"})
		if err != nil || st.State != ConnectionFailed || st.FailReason != reason {
			t.Fatalf("%s: %+v, %v", event, st, err)
		}
		if !strings.Contains(strings.Join(f.sent(), "\n"), "REMOVE_NETWORK 3") {
			t.Fatalf("%s: the failed network was not removed", event)
		}
	}
}

func TestNetplanRenderAndSave(t *testing.T) {
	dir := t.TempDir()
	p := NetplanPersister{Dir: dir}
	body, err := p.Render(Credentials{SSID: `Café "5G"`, Passphrase: `back\slash pass`})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Written by clipsetup: the network last set up over Bluetooth.\n" +
		"network:\n  version: 2\n  wifis:\n    \"wlan0\":\n      dhcp4: true\n      access-points:\n" +
		"        \"Café \\\"5G\\\"\":\n          password: \"back\\\\slash pass\"\n"
	if body != want {
		t.Fatalf("render:\n%s\nwant:\n%s", body, want)
	}
	if open, _ := p.Render(Credentials{SSID: "open cafe"}); !strings.Contains(open, `"open cafe": {}`) {
		t.Fatalf("open network:\n%s", open)
	}
	if _, err := p.Render(Credentials{SSID: "\xff\xfe"}); err == nil {
		t.Fatal("non-UTF-8 name rendered")
	}
	if err := p.Save(Credentials{SSID: "bench", Passphrase: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "90-clipsetup-wlan0.yaml"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("saved file: %v, %v", fi, err)
	}
}
