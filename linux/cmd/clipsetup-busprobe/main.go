// Command clipsetup-busprobe exercises a running clipsetup peripheral's exported D-Bus objects the
// way bluetoothd does for a phone: it lists the GATT objects, finds each endpoint by its 0x2901
// descriptor, and runs ESPProvision's sequence (proto-ver, session, scan, config, a custom
// endpoint) through GattCharacteristic1.WriteValue/ReadValue, splitting writes and reads at a small
// MTU so the offset paths run. It proves the D-Bus plumbing on a board without a phone; it is not
// a radio test.
//
//	sudo ./clipsetup-busprobe -dest :1.42 -pop k3m9qrst
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/narduk-enterprises/clipsetup/linux/espprov"
	"github.com/narduk-enterprises/clipsetup/linux/wifi"
)

func main() {
	dest := flag.String("dest", "", "the peripheral's unique bus name (its log prints it)")
	app := flag.String("app", "/com/narduk/clipsetup/app", "GATT application path")
	pop := flag.String("pop", "", "proof of possession")
	mtu := flag.Int("mtu", 23, "ATT MTU to simulate: writes in MTU-3 chunks, reads in MTU-1 chunks")
	ssid := flag.String("ssid", "bench", "network to provision (the fake backend knows bench)")
	pass := flag.String("pass", "correct horse", "passphrase to provision")
	custom := flag.String("custom", "custom-data", "custom endpoint to call (empty to skip)")
	skipConfig := flag.Bool("skip-config", false, "do not send a network")
	flag.Parse()
	if *dest == "" {
		log.Fatal("-dest is required")
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := conn.Object(*dest, dbus.ObjectPath(*app)).Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs); err != nil {
		log.Fatalf("GetManagedObjects: %v", err)
	}
	peer := dbus.ObjectPath("/org/bluez/hci0/dev_02_00_00_00_00_99")
	chars := map[string]dbus.ObjectPath{}
	for path, ifaces := range objs {
		d, ok := ifaces["org.bluez.GattDescriptor1"]
		if !ok || d["UUID"].Value() != "00002901-0000-1000-8000-00805f9b34fb" {
			continue
		}
		var name []byte
		if err := conn.Object(*dest, path).Call("org.bluez.GattDescriptor1.ReadValue", 0, map[string]dbus.Variant{}).Store(&name); err != nil {
			log.Fatalf("reading %s: %v", path, err)
		}
		chPath := d["Characteristic"].Value().(dbus.ObjectPath)
		ch := objs[chPath]["org.bluez.GattCharacteristic1"]
		fmt.Printf("endpoint %-14s %s flags=%v\n", name, ch["UUID"].Value(), ch["Flags"].Value())
		chars[string(name)] = chPath
	}

	writes, reads := 0, 0
	transport := func(ctx context.Context, endpoint string, req []byte) ([]byte, error) {
		path, ok := chars[endpoint]
		if !ok {
			return nil, fmt.Errorf("no characteristic named %s", endpoint)
		}
		obj := conn.Object(*dest, path)
		chunk := *mtu - 3
		for off := 0; off == 0 || off < len(req); off += chunk {
			end := min(off+chunk, len(req))
			opts := map[string]dbus.Variant{"device": dbus.MakeVariant(peer), "offset": dbus.MakeVariant(uint16(off))}
			if err := obj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, req[off:end], opts).Err; err != nil {
				return nil, fmt.Errorf("write %s at %d: %w", endpoint, off, err)
			}
			writes++
		}
		var resp []byte
		for {
			opts := map[string]dbus.Variant{"device": dbus.MakeVariant(peer), "offset": dbus.MakeVariant(uint16(len(resp)))}
			var part []byte
			if err := obj.Call("org.bluez.GattCharacteristic1.ReadValue", 0, opts).Store(&part); err != nil {
				return nil, fmt.Errorf("read %s at %d: %w", endpoint, len(resp), err)
			}
			reads++
			// A real central reads MTU-1 bytes at a time and asks for more while a read is full.
			if len(part) > *mtu-1 {
				part = part[:*mtu-1]
			}
			resp = append(resp, part...)
			if len(part) < *mtu-1 {
				return resp, nil
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := &espprov.Client{T: transport}
	info, err := c.Connect(ctx, *pop, rand.Reader)
	fmt.Printf("proto-ver %v\n", info)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	fmt.Println("session established (security 1)")
	nets, err := c.ScanWiFi(ctx)
	if err != nil {
		log.Fatalf("scan: %v", err)
	}
	fmt.Printf("scan: %d networks\n", len(nets))
	if *custom != "" {
		resp, err := c.Send(ctx, *custom, []byte(`{"token":"probe"}`))
		if err != nil {
			log.Fatalf("%s: %v", *custom, err)
		}
		fmt.Printf("%s -> %s\n", *custom, resp)
	}
	if !*skipConfig {
		st, err := c.Provision(ctx, wifi.Credentials{SSID: *ssid, Passphrase: *pass}, time.Second)
		if err != nil {
			log.Fatalf("provision: %v", err)
		}
		fmt.Printf("provision: state=%d fail_reason=%d ip=%s\n", st.State, st.FailReason, st.IP4)
	}
	fmt.Printf("done: %d WriteValue and %d ReadValue calls at MTU %d\n", writes, reads, *mtu)
}
