// Package espprov is the device side of Espressif's unified provisioning protocol, as the App Clip's
// ESPProvision client speaks it over Bluetooth LE: proto-ver, prov-session (security 1), prov-scan,
// prov-config and registered custom endpoints. It knows nothing about Bluetooth; package bluez
// carries each endpoint as one GATT characteristic and calls Server.Handle.
//
// References: ESP-IDF v5.3.1 components/protocomm and components/wifi_provisioning (device), and
// espressif/esp-idf-provisioning-ios at 78e58f01 (the client ClipSetup pins).
package espprov

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/narduk-enterprises/clipsetup/linux/wifi"
	"google.golang.org/protobuf/encoding/protowire"
)

// The standard endpoints, with ESP-IDF's 16-bit ids (wifi_provisioning manager.c). prov-ctrl
// (0xFF4F, Wi-Fi reset) is not offered: ClipSetup never calls it.
const (
	EndpointScan     = "prov-scan"
	EndpointSession  = "prov-session"
	EndpointConfig   = "prov-config"
	EndpointProtoVer = "proto-ver"

	firstCustomID = 0xFF54
)

// Endpoint is a name and the 16-bit id its characteristic UUID is derived from.
type Endpoint struct {
	Name string
	ID   uint16
}

// Handler serves a custom endpoint. It gets the decrypted request and returns the plaintext
// response; the server encrypts it. peer identifies the connection (a BlueZ device path).
type Handler func(ctx context.Context, peer string, req []byte) ([]byte, error)

// Config configures a Server.
type Config struct {
	// PoP is the proof of possession: the PIN in the link, or the sticker code. Empty means no PoP,
	// which the server announces with the no_pop capability.
	PoP string
	// WiFi scans and joins. Required.
	WiFi wifi.Manager
	// Rand supplies session keys; crypto/rand when nil.
	Rand io.Reader
	// Version is reported as prov.ver; default "v1.1", ESP-IDF's.
	Version string
	// AppInfo adds top-level objects to the proto-ver JSON beside "prov", as ESP-IDF's
	// wifi_prov_mgr_set_app_info does (for example {"mybo": {"ver": "1", "cap": ["claim"]}}).
	AppInfo map[string]any
	// MaxScanResults caps the networks offered, strongest first; default 30.
	MaxScanResults int
	// JoinTimeout bounds one join; default 60 s.
	JoinTimeout time.Duration
	// ApplyWait is how long ApplyConfig waits for the join before answering; default 0 (answer at
	// once and let the client poll GetStatus, as ESP-IDF does). Tests set it for a fixed transcript.
	ApplyWait time.Duration
	// OnCredentials is called with the network the phone chose, before the join. Never log the
	// passphrase.
	OnCredentials func(peer string, c wifi.Credentials)
	// OnJoin is called when a join finishes.
	OnJoin func(peer string, st wifi.Status, err error)
	// Logf receives protocol events (never secrets). Optional.
	Logf func(format string, args ...any)
}

// Server is the protocol state for one device: one Wi-Fi radio and one session per peer.
type Server struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	custom   []Endpoint
	handlers map[string]Handler
	peers    map[string]*peerState

	jmu      sync.Mutex
	creds    *wifi.Credentials
	joining  bool
	attempts int
	last     wifi.Status
	joinDone chan struct{}
}

type peerState struct {
	mu   sync.Mutex
	sec  *Security1
	scan []wifi.Network
}

// NewServer makes a server. Register custom endpoints before the transport starts.
func NewServer(cfg Config) (*Server, error) {
	if cfg.WiFi == nil {
		return nil, errors.New("espprov: Config.WiFi is required")
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Version == "" {
		cfg.Version = "v1.1"
	}
	if cfg.MaxScanResults <= 0 {
		cfg.MaxScanResults = 30
	}
	if cfg.JoinTimeout <= 0 {
		cfg.JoinTimeout = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{cfg: cfg, ctx: ctx, cancel: cancel, handlers: map[string]Handler{}, peers: map[string]*peerState{}}, nil
}

// Close cancels a join in progress.
func (s *Server) Close() { s.cancel() }

// Register adds a custom endpoint, carried encrypted in the session like prov-config. The box
// registers "mybo-claim". Ids are given in registration order from 0xFF54, as ESP-IDF's
// wifi_prov_mgr_endpoint_create does.
func (s *Server) Register(name string, h Handler) (Endpoint, error) {
	if name == "" || len(name) > 64 || h == nil {
		return Endpoint{}, errors.New("espprov: an endpoint needs a name of 1-64 bytes and a handler")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.endpoints() {
		if e.Name == name {
			return Endpoint{}, fmt.Errorf("espprov: endpoint %q already exists", name)
		}
	}
	e := Endpoint{Name: name, ID: uint16(firstCustomID + len(s.custom))}
	s.custom = append(s.custom, e)
	s.handlers[name] = h
	return e, nil
}

// Endpoints lists every endpoint in ESP-IDF's order.
func (s *Server) Endpoints() []Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpoints()
}

func (s *Server) endpoints() []Endpoint {
	out := []Endpoint{{EndpointScan, 0xFF50}, {EndpointSession, 0xFF51}, {EndpointConfig, 0xFF52}, {EndpointProtoVer, 0xFF53}}
	return append(out, s.custom...)
}

// Disconnect forgets a peer's session. The transport calls it when the link drops.
func (s *Server) Disconnect(peer string) {
	s.mu.Lock()
	delete(s.peers, peer)
	s.mu.Unlock()
}

func (s *Server) peer(id string) *peerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok {
		p = &peerState{sec: NewSecurity1([]byte(s.cfg.PoP), s.cfg.Rand)}
		s.peers[id] = p
	}
	return p
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// Handle answers one request on one endpoint. An error means the request cannot be answered; the
// transport reports it as an ATT error, as ESP-IDF does by dropping the connection.
func (s *Server) Handle(ctx context.Context, peer, endpoint string, req []byte) ([]byte, error) {
	if endpoint == EndpointProtoVer {
		return s.protoVer()
	}
	p := s.peer(peer)
	p.mu.Lock()
	defer p.mu.Unlock()

	if endpoint == EndpointSession {
		resp, err := p.sec.Handle(req)
		if err != nil {
			s.logf("session %s: %v", peer, err)
		} else if p.sec.Established() {
			s.logf("session %s: established", peer)
		}
		return resp, err
	}

	var h func(context.Context, *peerState, string, []byte) ([]byte, error)
	switch endpoint {
	case EndpointScan:
		h = s.handleScan
	case EndpointConfig:
		h = s.handleConfig
	default:
		s.mu.Lock()
		ch := s.handlers[endpoint]
		s.mu.Unlock()
		if ch == nil {
			return nil, fmt.Errorf("espprov: no endpoint %q", endpoint)
		}
		h = func(ctx context.Context, _ *peerState, peer string, b []byte) ([]byte, error) {
			return ch(ctx, peer, b)
		}
	}
	plain, err := p.sec.Crypt(req)
	if err != nil {
		return nil, err
	}
	resp, err := h(ctx, p, peer, plain)
	if err != nil {
		// The client's keystream has moved past this request but not past a response; the session
		// cannot continue, so make the client start a new one.
		p.sec = NewSecurity1([]byte(s.cfg.PoP), s.cfg.Rand)
		s.logf("%s %s: %v (session reset)", endpoint, peer, err)
		return nil, err
	}
	return p.sec.Crypt(resp)
}

func (s *Server) protoVer() ([]byte, error) {
	caps := []string{"wifi_scan"}
	if s.cfg.PoP == "" {
		caps = append(caps, "no_pop")
	}
	info := map[string]any{}
	for k, v := range s.cfg.AppInfo {
		info[k] = v
	}
	info["prov"] = map[string]any{"ver": s.cfg.Version, "sec_ver": 1, "cap": caps}
	return json.Marshal(info)
}

// wifi_scan.proto
const (
	fScanMsg        protowire.Number = 1
	fScanStatus     protowire.Number = 2
	fScanCmdStart   protowire.Number = 10
	fScanRespStart  protowire.Number = 11
	fScanCmdStatus  protowire.Number = 12
	fScanRespStatus protowire.Number = 13
	fScanCmdResult  protowire.Number = 14
	fScanRespResult protowire.Number = 15

	scanCmdStart   = 0
	scanRespStart  = 1
	scanCmdStatus  = 2
	scanRespStatus = 3
	scanCmdResult  = 4
	scanRespResult = 5
)

func (s *Server) handleScan(ctx context.Context, p *peerState, peer string, req []byte) ([]byte, error) {
	m, err := parse(req)
	if err != nil {
		return nil, err
	}
	switch m.uint(fScanMsg) {
	case scanCmdStart:
		// Blocking or not, the scan runs here: the result is ready by the time the client asks
		// for ScanStatus, which then reports finished.
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		nets, err := s.cfg.WiFi.Scan(sctx)
		cancel()
		status := StatusSuccess
		if err != nil {
			s.logf("scan: %v", err)
			status, nets = StatusInternalError, nil
		}
		if len(nets) > s.cfg.MaxScanResults {
			nets = nets[:s.cfg.MaxScanResults]
		}
		p.scan = nets
		s.logf("scan %s: %d networks", peer, len(nets))
		return enc(nil).varint(fScanMsg, scanRespStart).varint(fScanStatus, uint64(status)).msg(fScanRespStart, nil), nil
	case scanCmdStatus:
		st := enc(nil).bool(1, true).varint(2, uint64(len(p.scan)))
		return enc(nil).varint(fScanMsg, scanRespStatus).msg(fScanRespStatus, st), nil
	case scanCmdResult:
		cmd, err := m.msg(fScanCmdResult)
		if err != nil {
			return nil, err
		}
		start, count := cmd.uint(1), cmd.uint(2)
		// ESPWiFiManager iterates 0...entries.count-1, which traps on an empty page, so a page
		// outside the results is an error, never an empty success.
		if count == 0 || start >= uint64(len(p.scan)) {
			return nil, fmt.Errorf("espprov: scan page %d+%d outside %d results", start, count, len(p.scan))
		}
		end := min(start+count, uint64(len(p.scan)))
		var res enc
		for _, n := range p.scan[start:end] {
			e := enc(nil).bytes(1, []byte(n.SSID)).varint(2, uint64(n.Channel)).int32(3, n.RSSI).
				bytes(4, n.BSSID).varint(5, uint64(n.Auth))
			res = res.msg(1, e)
		}
		return enc(nil).varint(fScanMsg, scanRespResult).msg(fScanRespResult, res), nil
	default:
		return nil, fmt.Errorf("espprov: unknown prov-scan message %d", m.uint(fScanMsg))
	}
}

// wifi_config.proto
const (
	fCfgMsg        protowire.Number = 1
	fCfgCmdStatus  protowire.Number = 10
	fCfgRespStatus protowire.Number = 11
	fCfgCmdSet     protowire.Number = 12
	fCfgRespSet    protowire.Number = 13
	fCfgCmdApply   protowire.Number = 14
	fCfgRespApply  protowire.Number = 15

	cfgCmdGetStatus  = 0
	cfgRespGetStatus = 1
	cfgCmdSet        = 2
	cfgRespSet       = 3
	cfgCmdApply      = 4
	cfgRespApply     = 5
)

func (s *Server) handleConfig(ctx context.Context, p *peerState, peer string, req []byte) ([]byte, error) {
	m, err := parse(req)
	if err != nil {
		return nil, err
	}
	switch m.uint(fCfgMsg) {
	case cfgCmdGetStatus:
		return s.getStatus(ctx), nil
	case cfgCmdSet:
		cmd, err := m.msg(fCfgCmdSet)
		if err != nil {
			return nil, err
		}
		c := wifi.Credentials{
			SSID: string(cmd.bytes(1)), Passphrase: string(cmd.bytes(2)),
			BSSID: append([]byte(nil), cmd.bytes(3)...), Channel: int32(cmd.uint(4)),
		}
		status := StatusSuccess
		if err := wifi.ValidateCredentials(c); err != nil {
			s.logf("config %s: %v", peer, err)
			status = StatusInvalidArgument
		} else {
			s.jmu.Lock()
			s.creds = &c
			s.jmu.Unlock()
			s.logf("config %s: network set (%d-byte name)", peer, len(c.SSID))
		}
		return enc(nil).varint(fCfgMsg, cfgRespSet).msg(fCfgRespSet, enc(nil).varint(1, uint64(status))), nil
	case cfgCmdApply:
		status, done := s.apply(peer)
		if done != nil && s.cfg.ApplyWait > 0 {
			select {
			case <-done:
			case <-time.After(s.cfg.ApplyWait):
			case <-ctx.Done():
			}
		}
		return enc(nil).varint(fCfgMsg, cfgRespApply).msg(fCfgRespApply, enc(nil).varint(1, uint64(status))), nil
	default:
		return nil, fmt.Errorf("espprov: unknown prov-config message %d", m.uint(fCfgMsg))
	}
}

func (s *Server) apply(peer string) (Status, chan struct{}) {
	s.jmu.Lock()
	if s.joining {
		done := s.joinDone
		s.jmu.Unlock()
		return StatusSuccess, done
	}
	if s.creds == nil {
		s.jmu.Unlock()
		return StatusInvalidArgument, nil
	}
	c := *s.creds
	s.joining, s.attempts = true, s.attempts+1
	done := make(chan struct{})
	s.joinDone = done
	s.jmu.Unlock()
	if s.cfg.OnCredentials != nil {
		s.cfg.OnCredentials(peer, c)
	}
	go func() {
		defer close(done)
		jctx, cancel := context.WithTimeout(s.ctx, s.cfg.JoinTimeout)
		st, err := s.cfg.WiFi.Join(jctx, c)
		cancel()
		if err != nil {
			// The protocol has no reason for "the device could not try"; Disconnected makes
			// ESPProvision report a failed join rather than a wrong password.
			s.logf("join: %v", err)
			st = wifi.Status{State: wifi.Disconnected}
		}
		s.jmu.Lock()
		s.joining, s.last = false, st
		s.jmu.Unlock()
		s.logf("join: state %d", st.State)
		if s.cfg.OnJoin != nil {
			s.cfg.OnJoin(peer, st, err)
		}
	}()
	return StatusSuccess, done
}

func (s *Server) getStatus(ctx context.Context) []byte {
	s.jmu.Lock()
	joining, attempts, st := s.joining, s.attempts, s.last
	s.jmu.Unlock()
	switch {
	case joining:
		st = wifi.Status{State: wifi.Connecting}
	case attempts == 0:
		cur, err := s.cfg.WiFi.Status(ctx)
		if err != nil {
			cur = wifi.Status{State: wifi.Disconnected}
		}
		st = cur
	}
	r := enc(nil).varint(1, uint64(StatusSuccess)).varint(2, uint64(st.State))
	switch st.State {
	case wifi.ConnectionFailed:
		r = r.varintAlways(10, uint64(st.FailReason)) // a set oneof member is sent even when 0
	case wifi.Connected:
		conn := enc(nil).bytes(1, []byte(st.IP4)).varint(2, uint64(st.Auth)).bytes(3, []byte(st.SSID)).
			bytes(4, st.BSSID).int32(5, st.Channel)
		r = r.msg(11, conn)
	}
	return enc(nil).varint(fCfgMsg, cfgRespGetStatus).msg(fCfgRespStatus, r)
}
