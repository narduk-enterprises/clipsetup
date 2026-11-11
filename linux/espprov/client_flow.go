package espprov

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/narduk-enterprises/clipsetup/linux/wifi"
	"google.golang.org/protobuf/encoding/protowire"
)

// Transport sends one request to one endpoint and returns the response, as a BLE write followed by
// a read of the same characteristic.
type Transport func(ctx context.Context, endpoint string, req []byte) ([]byte, error)

// Client drives a device through the same steps, in the same order and with the same messages, as
// ESPProvision does for the App Clip (ESPDevice.connect, scanWifiList, provision). It is for tests
// and bench probes, not for phones.
type Client struct {
	T   Transport
	sec *Security1Client
}

// Connect reads proto-ver and runs the security 1 handshake with pop.
func (c *Client) Connect(ctx context.Context, pop string, rand io.Reader) (map[string]any, error) {
	raw, err := c.T(ctx, EndpointProtoVer, []byte("ESP"))
	if err != nil {
		return nil, fmt.Errorf("proto-ver: %w", err)
	}
	var info map[string]any
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("proto-ver: %w", err)
	}
	sec, err := NewSecurity1Client([]byte(pop), rand)
	if err != nil {
		return nil, err
	}
	r0, err := c.T(ctx, EndpointSession, sec.Cmd0())
	if err != nil {
		return info, fmt.Errorf("session 0: %w", err)
	}
	cmd1, err := sec.Cmd1(r0)
	if err != nil {
		return info, err
	}
	r1, err := c.T(ctx, EndpointSession, cmd1)
	if err != nil {
		return info, fmt.Errorf("session 1: %w", err)
	}
	if err := sec.Finish(r1); err != nil {
		return info, err
	}
	c.sec = sec
	return info, nil
}

// Send encrypts req for endpoint and decrypts the response (ESPDevice.sendData).
func (c *Client) Send(ctx context.Context, endpoint string, req []byte) ([]byte, error) {
	if c.sec == nil {
		return nil, ErrSessionNotEstablished
	}
	resp, err := c.T(ctx, endpoint, c.sec.Crypt(req))
	if err != nil {
		return nil, err
	}
	return c.sec.Crypt(resp), nil
}

// ScanWiFi asks for a blocking scan, its status, then the results four at a time.
func (c *Client) ScanWiFi(ctx context.Context) ([]wifi.Network, error) {
	start := enc(nil).bool(1, true).varint(4, 120)
	if _, err := c.Send(ctx, EndpointScan, enc(nil).varint(fScanMsg, scanCmdStart).msg(fScanCmdStart, start)); err != nil {
		return nil, err
	}
	raw, err := c.Send(ctx, EndpointScan, enc(nil).varint(fScanMsg, scanCmdStatus).msg(fScanCmdStatus, nil))
	if err != nil {
		return nil, err
	}
	m, err := parse(raw)
	if err != nil {
		return nil, err
	}
	st, err := m.msg(fScanRespStatus)
	if err != nil {
		return nil, err
	}
	count := uint32(st.uint(2))
	var nets []wifi.Network
	for i := uint32(0); i < count; i += 4 {
		n := min(4, count-i)
		req := enc(nil).varint(fScanMsg, scanCmdResult).msg(fScanCmdResult, enc(nil).varint(1, uint64(i)).varint(2, uint64(n)))
		raw, err := c.Send(ctx, EndpointScan, req)
		if err != nil {
			return nil, err
		}
		m, err := parse(raw)
		if err != nil {
			return nil, err
		}
		res, err := m.msg(fScanRespResult)
		if err != nil {
			return nil, err
		}
		for _, f := range res[1] {
			e, err := parse(f.b)
			if err != nil {
				return nil, err
			}
			nets = append(nets, wifi.Network{SSID: string(e.bytes(1)), Channel: uint32(e.uint(2)),
				RSSI: int32(e.uint(3)), BSSID: e.bytes(4), Auth: wifi.AuthMode(e.uint(5))})
		}
	}
	return nets, nil
}

// Provision sends SetConfig and ApplyConfig, then polls GetStatus every poll interval while the
// device reports Connecting (ESPProvision polls every 5 s).
func (c *Client) Provision(ctx context.Context, cr wifi.Credentials, poll time.Duration) (wifi.Status, error) {
	set := enc(nil).bytes(1, []byte(cr.SSID)).bytes(2, []byte(cr.Passphrase))
	raw, err := c.Send(ctx, EndpointConfig, enc(nil).varint(fCfgMsg, cfgCmdSet).msg(fCfgCmdSet, set))
	if err != nil {
		return wifi.Status{}, err
	}
	if s, err := respStatus(raw, fCfgRespSet); err != nil || s != StatusSuccess {
		return wifi.Status{}, fmt.Errorf("set config: status %d %v", s, err)
	}
	raw, err = c.Send(ctx, EndpointConfig, enc(nil).varint(fCfgMsg, cfgCmdApply).msg(fCfgCmdApply, nil))
	if err != nil {
		return wifi.Status{}, err
	}
	if s, err := respStatus(raw, fCfgRespApply); err != nil || s != StatusSuccess {
		return wifi.Status{}, fmt.Errorf("apply config: status %d %v", s, err)
	}
	for {
		st, err := c.WiFiStatus(ctx)
		if err != nil || st.State != wifi.Connecting {
			return st, err
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// WiFiStatus sends GetStatus.
func (c *Client) WiFiStatus(ctx context.Context) (wifi.Status, error) {
	raw, err := c.Send(ctx, EndpointConfig, enc(nil).varint(fCfgMsg, cfgCmdGetStatus).msg(fCfgCmdStatus, nil))
	if err != nil {
		return wifi.Status{}, err
	}
	return ParseGetStatus(raw)
}

// ParseGetStatus decodes a plaintext RespGetStatus payload.
func ParseGetStatus(raw []byte) (wifi.Status, error) {
	m, err := parse(raw)
	if err != nil {
		return wifi.Status{}, err
	}
	r, err := m.msg(fCfgRespStatus)
	if err != nil {
		return wifi.Status{}, err
	}
	st := wifi.Status{State: wifi.State(r.uint(2)), FailReason: wifi.FailReason(r.uint(10))}
	if r.has(11) {
		conn, err := r.msg(11)
		if err != nil {
			return st, err
		}
		st.IP4, st.Auth, st.SSID = string(conn.bytes(1)), wifi.AuthMode(conn.uint(2)), string(conn.bytes(3))
		st.BSSID, st.Channel = conn.bytes(4), int32(conn.uint(5))
	}
	return st, nil
}

func respStatus(raw []byte, f protowire.Number) (Status, error) {
	m, err := parse(raw)
	if err != nil {
		return 0, err
	}
	r, err := m.msg(f)
	if err != nil {
		return 0, err
	}
	return Status(r.uint(1)), nil
}
