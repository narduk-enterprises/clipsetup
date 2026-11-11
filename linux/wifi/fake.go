package wifi

import (
	"context"
	"sync"
	"time"
)

// Fake is an in-memory Manager. A join succeeds when Passwords[SSID] equals the passphrase, fails
// with AuthError when the SSID is known with another passphrase, and fails with NetworkNotFound
// otherwise. It never touches the host's network.
type Fake struct {
	Networks  []Network
	Passwords map[string]string
	IP4       string        // reported after a successful join; default 192.0.2.10
	Delay     time.Duration // how long a join takes

	mu     sync.Mutex
	status Status
	joins  []Credentials
}

// NewFake returns a Fake with one example network, "bench", passphrase "correct horse".
func NewFake() *Fake {
	return &Fake{
		Networks: []Network{
			{SSID: "bench", BSSID: []byte{0x02, 0, 0, 0, 0, 1}, Channel: 6, RSSI: -48, Auth: AuthWPA2PSK},
			{SSID: "open cafe", BSSID: []byte{0x02, 0, 0, 0, 0, 2}, Channel: 11, RSSI: -71, Auth: AuthOpen},
		},
		Passwords: map[string]string{"bench": "correct horse", "open cafe": ""},
	}
}

func (f *Fake) Scan(ctx context.Context) ([]Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Network(nil), f.Networks...), nil
}

func (f *Fake) Join(ctx context.Context, c Credentials) (Status, error) {
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-ctx.Done():
			return Status{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joins = append(f.joins, c)
	want, known := f.Passwords[c.SSID]
	switch {
	case !known:
		f.status = Status{State: ConnectionFailed, FailReason: NetworkNotFound}
	case want != c.Passphrase:
		f.status = Status{State: ConnectionFailed, FailReason: AuthError}
	default:
		ip := f.IP4
		if ip == "" {
			ip = "192.0.2.10"
		}
		f.status = Status{State: Connected, IP4: ip, SSID: c.SSID}
		for _, n := range f.Networks {
			if n.SSID == c.SSID {
				f.status.BSSID, f.status.Channel, f.status.Auth = n.BSSID, int32(n.Channel), n.Auth
			}
		}
	}
	return f.status, nil
}

func (f *Fake) Status(ctx context.Context) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.joins == nil {
		return Status{State: Disconnected}, nil
	}
	return f.status, nil
}

// Joins returns every join attempt, for tests.
func (f *Fake) Joins() []Credentials {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Credentials(nil), f.joins...)
}
