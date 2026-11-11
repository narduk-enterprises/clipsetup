package espprov

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/narduk-enterprises/clipsetup/linux/wifi"
)

// The golden transcript is a full session between this server and ESP-IDF v5.3.1's own client,
// tools/esp_prov (security1.py, prov/wifi_scan.py, prov/wifi_prov.py, prov/custom_prov.py), run by
// testdata/esp_prov_client.py with a pinned client key. Every request in it was produced by
// Espressif's code, so replaying it checks that this server decrypts and parses what Espressif's
// client sends, and answers byte for byte as it did when that client accepted the answers.
//
// Regenerate (needs an ESP-IDF checkout and a Python with protobuf and cryptography):
//
//	CLIPSETUP_ESP_IDF=/path/to/esp-idf CLIPSETUP_PYTHON=/path/to/python \
//	  go test ./espprov -run TestESPProvInterop -update
var update = flag.Bool("update", false, "rewrite testdata/esp_prov_sec1.json from a live esp_prov run")

const goldenPath = "testdata/esp_prov_sec1.json"

type exchangeRec struct {
	Endpoint string `json:"endpoint"`
	Request  string `json:"request"`
	Response string `json:"response"`
}

type transcript struct {
	Source      string          `json:"source"`
	PoP         string          `json:"pop"`
	SSID        string          `json:"ssid"`
	Passphrase  string          `json:"passphrase"`
	Exchanges   []exchangeRec   `json:"exchanges"`
	ClientSaw   json.RawMessage `json:"client_saw"`
	DeviceNotes string          `json:"device_notes"`
}

const (
	goldenPoP  = "k3m9qrst"
	goldenSSID = "bench"
	goldenPass = "correct horse"
)

func TestESPProvInterop(t *testing.T) {
	idf, py := os.Getenv("CLIPSETUP_ESP_IDF"), os.Getenv("CLIPSETUP_PYTHON")
	if idf == "" || py == "" {
		t.Skip("set CLIPSETUP_ESP_IDF and CLIPSETUP_PYTHON to run ESP-IDF's esp_prov client against this server")
	}
	s, fake := newTestServer(t, goldenPoP)
	script, _ := filepath.Abs("testdata/esp_prov_client.py")
	cmd := exec.Command(py, "-I", script, goldenPoP, goldenSSID, goldenPass)
	cmd.Env = append(os.Environ(), "IDF_PATH="+idf)
	cmd.Stderr = os.Stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := transcript{
		Source: "ESP-IDF v5.3.1 tools/esp_prov client (testdata/esp_prov_client.py) against espprov.Server",
		PoP:    goldenPoP, SSID: goldenSSID, Passphrase: goldenPass,
		DeviceNotes: "device randomness: newDRBG(\"clipsetup golden device\"); client key bytes 0x40..0x5f",
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		kind, rest, _ := strings.Cut(sc.Text(), " ")
		if kind == "DONE" {
			tr.ClientSaw = json.RawMessage(rest)
			break
		}
		ep, reqHex, _ := strings.Cut(rest, " ")
		req, err := hex.DecodeString(reqHex)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := s.Handle(context.Background(), "golden", ep, req)
		if err != nil {
			stdin.Write([]byte("ERR " + err.Error() + "\n"))
			t.Fatalf("%s: %v", ep, err)
		}
		stdin.Write([]byte("RESP " + hex.EncodeToString(resp) + "\n"))
		tr.Exchanges = append(tr.Exchanges, exchangeRec{ep, reqHex, hex.EncodeToString(resp)})
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("esp_prov client: %v", err)
	}
	checkClientSaw(t, tr.ClientSaw)
	checkJoin(t, fake.Joins())
	if *update {
		b, _ := json.MarshalIndent(tr, "", "  ")
		if err := os.WriteFile(goldenPath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestESPProvTranscript(t *testing.T) {
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var tr transcript
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.Exchanges) < 10 {
		t.Fatalf("transcript has %d exchanges", len(tr.Exchanges))
	}
	s, fake := newTestServer(t, tr.PoP)
	for i, x := range tr.Exchanges {
		req, _ := hex.DecodeString(x.Request)
		resp, err := s.Handle(context.Background(), "golden", x.Endpoint, req)
		if err != nil {
			t.Fatalf("exchange %d (%s): %v", i, x.Endpoint, err)
		}
		if got := hex.EncodeToString(resp); got != x.Response {
			t.Fatalf("exchange %d (%s):\n got %s\nwant %s", i, x.Endpoint, got, x.Response)
		}
	}
	checkClientSaw(t, tr.ClientSaw)
	checkJoin(t, fake.Joins())
}

// checkClientSaw checks what Espressif's client decoded from this server's answers.
func checkClientSaw(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var saw struct {
		ProtoVer map[string]map[string]any `json:"proto_ver"`
		Session  string                    `json:"session"`
		Scan     []struct {
			SSID    string `json:"ssid"`
			BSSID   string `json:"bssid"`
			Channel int    `json:"channel"`
			RSSI    int    `json:"rssi"`
			Auth    string `json:"auth"`
		} `json:"scan"`
		Custom      string `json:"custom"`
		SetConfig   int    `json:"set_config"`
		ApplyConfig int    `json:"apply_config"`
		GetStatus   string `json:"get_status"`
	}
	if err := json.Unmarshal(raw, &saw); err != nil {
		t.Fatal(err)
	}
	if saw.ProtoVer["prov"]["sec_ver"] != float64(1) || saw.Session != "ok" {
		t.Fatalf("client saw proto-ver %v, session %q", saw.ProtoVer, saw.Session)
	}
	want := goldenNetworks()
	if len(saw.Scan) != len(want) {
		t.Fatalf("client saw %d networks, want %d", len(saw.Scan), len(want))
	}
	auth := []string{"Open", "WEP", "WPA_PSK", "WPA2_PSK", "WPA_WPA2_PSK", "WPA2_ENTERPRISE", "WPA3_PSK", "WPA2_WPA3_PSK"}
	for i, n := range saw.Scan {
		w := want[i]
		// esp_prov decodes the name as latin-1; compare bytes.
		if latin1(n.SSID) != w.SSID || n.RSSI != int(w.RSSI) || n.Channel != int(w.Channel) ||
			n.BSSID != hex.EncodeToString(w.BSSID) || n.Auth != auth[w.Auth] {
			t.Errorf("network %d: client saw %+v, want %+v", i, n, w)
		}
	}
	if saw.Custom != `{"received":18}` || saw.SetConfig != 0 || saw.ApplyConfig != 0 || saw.GetStatus != "connected" {
		t.Fatalf("client saw custom %q set %d apply %d status %q", saw.Custom, saw.SetConfig, saw.ApplyConfig, saw.GetStatus)
	}
}

func checkJoin(t *testing.T, joins []wifi.Credentials) {
	t.Helper()
	if len(joins) != 1 || joins[0].SSID != goldenSSID || joins[0].Passphrase != goldenPass {
		t.Fatalf("device decrypted joins %+v, want one join to %q with the golden passphrase", joins, goldenSSID)
	}
}

func latin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}
