// Command clipsetup-example runs a ClipSetup peripheral on a Linux board: it advertises
// PROV_<KIND>_<last four of the Bluetooth address>, prints the App Clip link with a fresh PIN, and
// serves Espressif provisioning (security 1) over BlueZ until a network is joined or the setup
// window ends.
//
// By default it uses a fake Wi-Fi backend and changes nothing on the host. -wifi wpa uses the real
// wpa_supplicant for scans (cached results unless -wpa-scan) and joins only with -allow-join.
//
//	sudo ./clipsetup-example -kind MYBO
//	sudo ./clipsetup-example -kind MYBO -wifi wpa -wpa-scan -allow-join -persist-netplan
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	clipsetup "github.com/narduk-enterprises/clipsetup/linux"
	"github.com/narduk-enterprises/clipsetup/linux/bluez"
	"github.com/narduk-enterprises/clipsetup/linux/espprov"
	"github.com/narduk-enterprises/clipsetup/linux/wifi"
)

func main() {
	adapter := flag.String("adapter", "hci0", "Bluetooth adapter")
	kind := flag.String("kind", "DEVICE", "device kind in the name, A-Z 0-9, at most 12")
	name := flag.String("name", "", "advertised name (default PROV_<KIND>_<last four of the adapter address>)")
	pop := flag.String("pop", "", "proof of possession (default: a new 8-character PIN)")
	linkBase := flag.String("link-base", clipsetup.DefaultLinkBase, "link base for the printed link")
	backend := flag.String("wifi", "fake", "Wi-Fi backend: fake or wpa")
	ctrlDir := flag.String("wpa-ctrl", "/run/wpa_supplicant", "wpa_supplicant control socket directory")
	iface := flag.String("iface", "wlan0", "Wi-Fi interface")
	wpaScan := flag.Bool("wpa-scan", false, "with -wifi wpa: trigger fresh scans (default: read cached results only)")
	allowJoin := flag.Bool("allow-join", false, "with -wifi wpa: let the phone change this host's Wi-Fi")
	persist := flag.Bool("persist-netplan", false, "with -allow-join: save the joined network under /etc/netplan")
	custom := flag.String("custom", "custom-data", "name of an example custom endpoint (empty for none)")
	window := flag.Duration("window", 15*time.Minute, "stop advertising after this long")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var mgr wifi.Manager
	switch *backend {
	case "fake":
		mgr = wifi.NewFake()
	case "wpa":
		w := &wifi.WPA{CtrlDir: *ctrlDir, Iface: *iface, TriggerScan: *wpaScan, AllowJoin: *allowJoin}
		if *persist {
			if !*allowJoin {
				log.Fatal("-persist-netplan needs -allow-join")
			}
			w.Persist = wifi.NetplanPersister{Iface: *iface}.Save
		}
		mgr = w
	default:
		log.Fatalf("unknown -wifi %q", *backend)
	}

	if *name == "" {
		addr, err := bluez.AdapterAddress(*adapter)
		if err != nil {
			log.Fatalf("reading the adapter address: %v", err)
		}
		*name = clipsetup.Name(*kind, addr)
	}
	if len(*name) > clipsetup.MaxNameLen {
		log.Fatalf("name %q is longer than %d characters", *name, clipsetup.MaxNameLen)
	}
	if *pop == "" {
		p, err := clipsetup.NewPIN(nil)
		if err != nil {
			log.Fatal(err)
		}
		*pop = p
	}

	joined := make(chan wifi.Status, 1)
	srv, err := espprov.NewServer(espprov.Config{
		PoP:  *pop,
		WiFi: mgr,
		Logf: log.Printf,
		OnCredentials: func(peer string, c wifi.Credentials) {
			log.Printf("phone sent a network (%d-byte name); joining", len(c.SSID))
		},
		OnJoin: func(peer string, st wifi.Status, err error) {
			if st.State == wifi.Connected {
				select {
				case joined <- st:
				default:
				}
			}
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	if *custom != "" {
		if _, err := srv.Register(*custom, func(ctx context.Context, peer string, req []byte) ([]byte, error) {
			log.Printf("%s: %d bytes from %s", *custom, len(req), peer)
			return json.Marshal(map[string]any{"received": len(req)})
		}); err != nil {
			log.Fatal(err)
		}
	}

	var eps []bluez.Endpoint
	for _, e := range srv.Endpoints() {
		u, err := clipsetup.EndpointUUID(clipsetup.ServiceUUID, e.ID)
		if err != nil {
			log.Fatal(err)
		}
		eps = append(eps, bluez.Endpoint{Name: e.Name, UUID: u})
	}
	per, err := bluez.Start(bluez.Config{
		Adapter: *adapter, Name: *name, ServiceUUID: clipsetup.ServiceUUID, Endpoints: eps,
		Handle: srv.Handle, OnDisconnect: srv.Disconnect, Logf: log.Printf,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer per.Close()

	fmt.Printf("Wi-Fi setup: open %s\n", clipsetup.Link(*linkBase, *name, *pop))
	log.Printf("bus name %s; endpoints:", per.BusName())
	for _, e := range eps {
		log.Printf("  %-14s %s", e.Name, e.UUID)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case st := <-joined:
		log.Printf("joined, address %s; Bluetooth stays up 10 s so the phone can read the result", st.IP4)
		time.Sleep(10 * time.Second)
	case <-time.After(*window):
		log.Printf("setup window of %s ended", *window)
	case s := <-sig:
		log.Printf("%s: stopping", s)
	}
}
