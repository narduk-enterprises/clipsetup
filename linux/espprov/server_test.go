package espprov

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/narduk-enterprises/clipsetup/linux/wifi"
)

func TestProtoVer(t *testing.T) {
	s, _ := newTestServer(t, "pin")
	got, err := s.Handle(context.Background(), "p", EndpointProtoVer, []byte("ESP"))
	if err != nil || string(got) != `{"prov":{"cap":["wifi_scan"],"sec_ver":1,"ver":"v1.1"}}` {
		t.Fatalf("proto-ver = %s, %v", got, err)
	}
	open, _ := NewServer(Config{WiFi: wifi.NewFake(), AppInfo: map[string]any{"mybo": map[string]any{"ver": "1"}}})
	got, _ = open.Handle(context.Background(), "p", EndpointProtoVer, nil)
	if string(got) != `{"mybo":{"ver":"1"},"prov":{"cap":["wifi_scan","no_pop"],"sec_ver":1,"ver":"v1.1"}}` {
		t.Fatalf("proto-ver without PoP = %s", got)
	}
}

func TestEndpoints(t *testing.T) {
	s, _ := newTestServer(t, "pin")
	if _, err := s.Register("mybo-claim", func(context.Context, string, []byte) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{{"prov-scan", 0xFF50}, {"prov-session", 0xFF51}, {"prov-config", 0xFF52}, {"proto-ver", 0xFF53}, {"custom-data", 0xFF54}, {"mybo-claim", 0xFF55}}
	got := s.Endpoints()
	if len(got) != len(want) {
		t.Fatalf("endpoints %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("endpoint %d = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := s.Register("prov-config", func(context.Context, string, []byte) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("a standard endpoint name was registered twice")
	}
}

func TestEncryptedEndpointsNeedASession(t *testing.T) {
	s, _ := newTestServer(t, "pin")
	for _, ep := range []string{EndpointScan, EndpointConfig, "custom-data"} {
		if _, err := s.Handle(context.Background(), "p", ep, []byte{1}); !errors.Is(err, ErrSessionNotEstablished) {
			t.Errorf("%s without a session: %v", ep, err)
		}
	}
}

// RSSI is a proto int32: -48 is the 10-byte sign-extended varint d0 ff ff ff ff ff ff ff ff 01.
func TestNegativeRSSIEncoding(t *testing.T) {
	e := enc(nil).int32(3, -48)
	want := []byte{0x18, 0xd0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if !bytes.Equal(e, want) {
		t.Fatalf("int32(-48) = %x, want %x", []byte(e), want)
	}
}

func TestScanPagesAndCap(t *testing.T) {
	fake := &wifi.Fake{}
	for i := 0; i < 40; i++ {
		fake.Networks = append(fake.Networks, wifi.Network{SSID: string(rune('A' + i%26)), RSSI: int32(-30 - i)})
	}
	s, _ := NewServer(Config{PoP: "pin", WiFi: fake, MaxScanResults: 9})
	c := &Client{T: direct(s, "p")}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	nets, err := c.ScanWiFi(ctx)
	if err != nil || len(nets) != 9 || nets[8].RSSI != -38 {
		t.Fatalf("scan: %d networks, %v", len(nets), err)
	}
	// A page past the end is refused rather than answered empty (ESPProvision traps on empty).
	page := enc(nil).varint(fScanMsg, scanCmdResult).msg(fScanCmdResult, enc(nil).varint(1, 9).varint(2, 4))
	if _, err := c.Send(ctx, EndpointScan, page); err == nil {
		t.Fatal("page past the end answered")
	}
}

func TestProvisionOutcomes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, ssid, pass string
		want             wifi.Status
	}{
		{"joins", "bench", "correct horse", wifi.Status{State: wifi.Connected, IP4: "192.0.2.10"}},
		{"wrong password", "bench", "wrong horse", wifi.Status{State: wifi.ConnectionFailed, FailReason: wifi.AuthError}},
		{"not found", "elsewhere", "whatever!", wifi.Status{State: wifi.ConnectionFailed, FailReason: wifi.NetworkNotFound}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &wifi.Fake{Networks: goldenNetworks(), Passwords: map[string]string{"bench": "correct horse"}, Delay: 50 * time.Millisecond}
			s, _ := NewServer(Config{PoP: "pin", WiFi: fake}) // no ApplyWait: the client polls
			c := &Client{T: direct(s, "p")}
			if _, err := c.Connect(ctx, "pin", rand.Reader); err != nil {
				t.Fatal(err)
			}
			st, err := c.Provision(ctx, wifi.Credentials{SSID: tc.ssid, Passphrase: tc.pass}, 10*time.Millisecond)
			if err != nil || st.State != tc.want.State || st.FailReason != tc.want.FailReason || st.IP4 != tc.want.IP4 {
				t.Fatalf("status %+v, %v; want %+v", st, err, tc.want)
			}
		})
	}
}

// AuthError is 0, the proto3 default, but as a set oneof member it must still be on the wire.
func TestFailReasonZeroIsSent(t *testing.T) {
	fake := &wifi.Fake{Passwords: map[string]string{"bench": "correct horse"}}
	s, _ := NewServer(Config{PoP: "pin", WiFi: fake, ApplyWait: time.Second})
	c := &Client{T: direct(s, "p")}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Provision(ctx, wifi.Credentials{SSID: "bench", Passphrase: "not the one"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	raw, _ := c.Send(ctx, EndpointConfig, enc(nil).varint(fCfgMsg, cfgCmdGetStatus).msg(fCfgCmdStatus, nil))
	// msg=1, field 11 { status omitted, sta_state=3, fail_reason(10)=0 }
	if want := []byte{0x08, 0x01, 0x5a, 0x04, 0x10, 0x03, 0x50, 0x00}; !bytes.Equal(raw, want) {
		t.Fatalf("RespGetStatus = %x, want %x", raw, want)
	}
}

func TestConfigRejectsBadInput(t *testing.T) {
	s, fake := newTestServer(t, "pin")
	c := &Client{T: direct(s, "p")}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	apply := enc(nil).varint(fCfgMsg, cfgCmdApply).msg(fCfgCmdApply, nil)
	raw, _ := c.Send(ctx, EndpointConfig, apply)
	if st, _ := respStatus(raw, fCfgRespApply); st != StatusInvalidArgument {
		t.Fatalf("apply with no network: status %d", st)
	}
	set := enc(nil).varint(fCfgMsg, cfgCmdSet).msg(fCfgCmdSet, enc(nil).bytes(1, []byte("bench")).bytes(2, []byte("short")))
	raw, _ = c.Send(ctx, EndpointConfig, set)
	if st, _ := respStatus(raw, fCfgRespSet); st != StatusInvalidArgument {
		t.Fatalf("7-character passphrase: status %d", st)
	}
	if len(fake.Joins()) != 0 {
		t.Fatal("a join was attempted")
	}
}

func TestPeersHaveSeparateSessions(t *testing.T) {
	s, _ := newTestServer(t, "pin")
	ctx := context.Background()
	a, b := &Client{T: direct(s, "a")}, &Client{T: direct(s, "b")}
	if _, err := a.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ScanWiFi(ctx); err != nil {
		t.Fatalf("a: %v", err)
	}
	s.Disconnect("b")
	if _, err := b.ScanWiFi(ctx); !errors.Is(err, ErrSessionNotEstablished) {
		t.Fatalf("b after disconnect: %v", err)
	}
}
