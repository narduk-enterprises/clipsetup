// Package bluez publishes a provisioning service through BlueZ's D-Bus API: one GATT service whose
// characteristics are the protocol's endpoints, each named by a 0x2901 user-description
// descriptor (which is how ESPProvision finds them), and an LE advertisement carrying the service
// UUID and the PROV_ name. BlueZ 5.82 puts the UUID in the advertising data and the name in the
// scan response by itself (linux/BENCH-EVIDENCE.md).
//
// It talks to bluetoothd only; it needs the system bus and, with Debian's default D-Bus policy,
// root.
package bluez

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

const (
	bluezBus        = "org.bluez"
	ifaceAdapter    = "org.bluez.Adapter1"
	ifaceDevice     = "org.bluez.Device1"
	ifaceGattMgr    = "org.bluez.GattManager1"
	ifaceAdvMgr     = "org.bluez.LEAdvertisingManager1"
	ifaceService    = "org.bluez.GattService1"
	ifaceChar       = "org.bluez.GattCharacteristic1"
	ifaceDesc       = "org.bluez.GattDescriptor1"
	ifaceAdv        = "org.bluez.LEAdvertisement1"
	ifaceObjMgr     = "org.freedesktop.DBus.ObjectManager"
	ifaceProps      = "org.freedesktop.DBus.Properties"
	userDescription = "00002901-0000-1000-8000-00805f9b34fb"
)

// Endpoint is one characteristic: the protocol endpoint name (the 0x2901 value) and its UUID.
type Endpoint struct {
	Name string
	UUID string
}

// Handler answers one request on one endpoint. peer is the BlueZ device path of the central.
type Handler func(ctx context.Context, peer, endpoint string, req []byte) ([]byte, error)

// Config describes the peripheral.
type Config struct {
	Adapter     string // default hci0
	Name        string // advertised local name, e.g. PROV_MYBO_4B98
	ServiceUUID string
	Endpoints   []Endpoint
	Handle      Handler
	// OnDisconnect is called when a central that used the service disconnects.
	OnDisconnect func(peer string)
	// Root is the D-Bus object path prefix; default /com/narduk/clipsetup.
	Root dbus.ObjectPath
	// Logf receives events (never request bytes). Optional.
	Logf func(format string, args ...any)
}

// Peripheral is a registered GATT application and advertisement.
type Peripheral struct {
	cfg     Config
	conn    *dbus.Conn
	adapter dbus.ObjectPath
	appPath dbus.ObjectPath
	advPath dbus.ObjectPath
	objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	chars   []*gattChar
	ctx     context.Context
	cancel  context.CancelFunc
	advOn   bool
	appOn   bool
}

// AdapterAddress reads the adapter's Bluetooth address, for naming the device.
func AdapterAddress(adapter string) ([6]byte, error) {
	var out [6]byte
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if adapter == "" {
		adapter = "hci0"
	}
	v, err := conn.Object(bluezBus, dbus.ObjectPath("/org/bluez/"+adapter)).GetProperty(ifaceAdapter + ".Address")
	if err != nil {
		return out, err
	}
	s, _ := v.Value().(string)
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return out, fmt.Errorf("bluez: adapter address %q", s)
	}
	copy(out[:], hw)
	return out, nil
}

// Start exports the objects and registers them with bluetoothd. Close undoes it.
func Start(cfg Config) (*Peripheral, error) {
	if cfg.Adapter == "" {
		cfg.Adapter = "hci0"
	}
	if cfg.Root == "" {
		cfg.Root = "/com/narduk/clipsetup"
	}
	if cfg.Handle == nil || cfg.ServiceUUID == "" || len(cfg.Endpoints) == 0 {
		return nil, errors.New("bluez: Config needs a service UUID, endpoints and a handler")
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("bluez: system bus: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Peripheral{
		cfg: cfg, conn: conn, ctx: ctx, cancel: cancel,
		adapter: dbus.ObjectPath("/org/bluez/" + cfg.Adapter),
		appPath: cfg.Root + "/app",
		advPath: cfg.Root + "/adv0",
	}
	if err := p.export(); err != nil {
		p.Close()
		return nil, err
	}
	p.watchDisconnects()

	opts := map[string]dbus.Variant{}
	if err := conn.Object(bluezBus, p.adapter).Call(ifaceGattMgr+".RegisterApplication", 0, p.appPath, opts).Err; err != nil {
		p.Close()
		return nil, fmt.Errorf("bluez: RegisterApplication: %w", err)
	}
	p.appOn = true
	if err := conn.Object(bluezBus, p.adapter).Call(ifaceAdvMgr+".RegisterAdvertisement", 0, p.advPath, opts).Err; err != nil {
		p.Close()
		return nil, fmt.Errorf("bluez: RegisterAdvertisement: %w", err)
	}
	p.advOn = true
	p.logf("advertising %s with service %s on %s", cfg.Name, cfg.ServiceUUID, cfg.Adapter)
	return p, nil
}

// BusName is this connection's unique D-Bus name, where the GATT objects live.
func (p *Peripheral) BusName() string {
	if names := p.conn.Names(); len(names) > 0 {
		return names[0]
	}
	return ""
}

// AppPath is the GATT application's object path.
func (p *Peripheral) AppPath() string { return string(p.appPath) }

// StopAdvertising withdraws the advertisement and keeps the GATT service for a connected central.
func (p *Peripheral) StopAdvertising() error {
	if !p.advOn {
		return nil
	}
	p.advOn = false
	return p.conn.Object(bluezBus, p.adapter).Call(ifaceAdvMgr+".UnregisterAdvertisement", 0, p.advPath).Err
}

// Close unregisters everything and closes the bus connection.
func (p *Peripheral) Close() error {
	var errs []error
	if p.advOn {
		errs = append(errs, p.StopAdvertising())
	}
	if p.appOn {
		p.appOn = false
		errs = append(errs, p.conn.Object(bluezBus, p.adapter).Call(ifaceGattMgr+".UnregisterApplication", 0, p.appPath).Err)
	}
	p.cancel()
	errs = append(errs, p.conn.Close())
	return errors.Join(errs...)
}

func (p *Peripheral) logf(format string, args ...any) {
	if p.cfg.Logf != nil {
		p.cfg.Logf(format, args...)
	}
}

func (p *Peripheral) export() error {
	svcPath := p.appPath + "/service0"
	p.objects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}

	svcProps := map[string]any{"UUID": p.cfg.ServiceUUID, "Primary": true}
	if err := p.exportObject(svcPath, ifaceService, nil, svcProps); err != nil {
		return err
	}
	for i, ep := range p.cfg.Endpoints {
		chPath := dbus.ObjectPath(fmt.Sprintf("%s/char%d", svcPath, i))
		dPath := chPath + "/desc0"
		ch := &gattChar{p: p, name: ep.Name, ex: newExchanges()}
		p.chars = append(p.chars, ch)
		// Every characteristic must be read+write: ESPProvision marks the whole peripheral
		// unusable if any characteristic in the service lacks either.
		chProps := map[string]any{"UUID": strings.ToLower(ep.UUID), "Service": svcPath, "Flags": []string{"read", "write"}}
		if err := p.exportObject(chPath, ifaceChar, ch, chProps); err != nil {
			return err
		}
		d := &gattDesc{value: []byte(ep.Name)}
		dProps := map[string]any{"UUID": userDescription, "Characteristic": chPath, "Flags": []string{"read"}}
		if err := p.exportObject(dPath, ifaceDesc, d, dProps); err != nil {
			return err
		}
	}
	if err := p.conn.Export(&objectManager{p}, p.appPath, ifaceObjMgr); err != nil {
		return err
	}

	advProps := map[string]any{
		"Type":         "peripheral",
		"ServiceUUIDs": []string{strings.ToLower(p.cfg.ServiceUUID)},
		"LocalName":    p.cfg.Name,
		"Discoverable": true,
	}
	return p.exportObject(p.advPath, ifaceAdv, &advertisement{p}, advProps)
}

// exportObject exports methods (when impl is non-nil) and read-only properties on path.
func (p *Peripheral) exportObject(path dbus.ObjectPath, iface string, impl any, props map[string]any) error {
	if impl != nil {
		if err := p.conn.Export(impl, path, iface); err != nil {
			return err
		}
	}
	pm := map[string]*prop.Prop{}
	vm := map[string]dbus.Variant{}
	for k, v := range props {
		pm[k] = &prop.Prop{Value: v, Emit: prop.EmitFalse}
		vm[k] = dbus.MakeVariant(v)
	}
	if _, err := prop.Export(p.conn, path, prop.Map{iface: pm}); err != nil {
		return err
	}
	if path != p.advPath {
		p.objects[path] = map[string]map[string]dbus.Variant{iface: vm}
	}
	return nil
}

func (p *Peripheral) watchDisconnects() {
	if err := p.conn.AddMatchSignal(
		dbus.WithMatchInterface(ifaceProps),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, ifaceDevice),
	); err != nil {
		p.logf("watching disconnects: %v", err)
		return
	}
	ch := make(chan *dbus.Signal, 16)
	p.conn.Signal(ch)
	go func() {
		for {
			select {
			case <-p.ctx.Done():
				return
			case sig, ok := <-ch:
				if !ok {
					return
				}
				if len(sig.Body) < 2 {
					continue
				}
				changed, _ := sig.Body[1].(map[string]dbus.Variant)
				v, ok := changed["Connected"]
				if !ok {
					continue
				}
				if up, _ := v.Value().(bool); up {
					continue
				}
				peer := string(sig.Path)
				used := false
				for _, c := range p.chars {
					if c.ex.forget(peer) {
						used = true
					}
				}
				if used {
					p.logf("central %s disconnected", peer)
					if p.cfg.OnDisconnect != nil {
						p.cfg.OnDisconnect(peer)
					}
				}
			}
		}
	}()
}

type objectManager struct{ p *Peripheral }

func (o *objectManager) GetManagedObjects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, *dbus.Error) {
	return o.p.objects, nil
}

type advertisement struct{ p *Peripheral }

// Release is called by bluetoothd when it drops the advertisement.
func (a *advertisement) Release() *dbus.Error {
	a.p.advOn = false
	a.p.logf("advertisement released by bluetoothd")
	return nil
}

type gattDesc struct{ value []byte }

func (d *gattDesc) ReadValue(opts map[string]dbus.Variant) ([]byte, *dbus.Error) {
	off := optOffset(opts)
	if off > len(d.value) {
		return nil, errInvalidOffset
	}
	return d.value[off:], nil
}

// gattChar is one endpoint. The client writes a request, then reads the same characteristic for
// the response (ESPProvision's ESPBleTransport). Requests longer than the MTU arrive as several
// WriteValue calls with offsets, and long responses are read with offsets, so the request is
// processed on the first read (offset 0) and the response is kept for the reads that follow.
type gattChar struct {
	p    *Peripheral
	name string
	ex   *exchanges
}

func (c *gattChar) WriteValue(value []byte, opts map[string]dbus.Variant) *dbus.Error {
	if err := c.ex.write(optPeer(opts), optOffset(opts), value); err != nil {
		return toDBusError(err)
	}
	return nil
}

func (c *gattChar) ReadValue(opts map[string]dbus.Variant) ([]byte, *dbus.Error) {
	peer := optPeer(opts)
	out, err := c.ex.read(peer, optOffset(opts), func(req []byte) ([]byte, error) {
		return c.p.cfg.Handle(c.p.ctx, peer, c.name, req)
	})
	if err != nil {
		c.p.logf("%s: %v", c.name, err)
		return nil, toDBusError(err)
	}
	return out, nil
}

func optOffset(opts map[string]dbus.Variant) int {
	if v, ok := opts["offset"]; ok {
		switch n := v.Value().(type) {
		case uint16:
			return int(n)
		case uint32:
			return int(n)
		}
	}
	return 0
}

func optPeer(opts map[string]dbus.Variant) string {
	if v, ok := opts["device"]; ok {
		if p, ok := v.Value().(dbus.ObjectPath); ok {
			return string(p)
		}
	}
	return ""
}

var (
	errInvalidOffset = dbus.NewError("org.bluez.Error.InvalidOffset", nil)
	errFailed        = "org.bluez.Error.Failed"
)

func toDBusError(err error) *dbus.Error {
	if errors.Is(err, ErrInvalidOffset) {
		return errInvalidOffset
	}
	return dbus.NewError(errFailed, []any{err.Error()})
}
