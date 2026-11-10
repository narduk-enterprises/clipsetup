# Bench evidence: Bluetooth LE setup on the Orange Pi Zero 2W (lane B0)

Board `mybo-zero` (`ssh narduk@mybo-zero.local`), a bench box, not a boat. Read on 2026-11-10
between 17:20 and 17:30 CT (23:20–23:30 UTC). Every command below was read-only, or a throwaway
under `/tmp/b0` that was time-boxed, stopped by its recorded PID and removed. Nothing was installed,
no service was enabled and no network setting was changed. `bluetoothctl show` after the run matched
the run before it (Powered yes, Discoverable no, Pairable no, ActiveInstances 0).

## Answer

- **Bluetooth works for ClipSetup's advertising shape.** `hci0` is powered, BlueZ 5.82 registers a
  GATT application and an LE advertisement, and it puts the 128-bit service UUID in the advertising
  data and the `PROV_` name in the scan response by itself, using legacy `ADV_IND` PDUs.
- **Nobody has read it over the air yet.** The Mac scan was refused by macOS privacy (below), so the
  advertisement is proven from the board's own HCI trace, not from a receiver.

## needs_facts (ADR-0019)

| Command                                       | Result                                                                                                                                                                                                                                                                                       |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cat /etc/armbian-release`                    | `BOARD=orangepizero2w`, Armbian community nightly `26.11.0-trunk.120`, `BRANCH=current`; kernel `6.18.60-current-sunxi64`                                                                                                                                                                    |
| `bluetoothctl show`                           | Controller `D8:78:61:1C:21:18` (public), Powered yes, Discoverable no, Pairable no, Roles central + peripheral. Advertising: 16 instances, `MaxAdvLen` 251, `MaxScnRspLen` 251, secondary channels 1M/2M/Coded, `HardwareOffload`                                                            |
| `btmgmt info`                                 | `hci0` primary controller, version 9, manufacturer 492 (Spreadtrum). Supported settings include `le advertising secure-conn privacy`; current `powered ssp br/edr le secure-conn`                                                                                                            |
| `dmesg \| grep -i -E 'blue\|hci\|uwe\|sprd'`  | UWE5622 combo chip: `WCN: marlin_request_firmware from /lib/firmware/uwe5622/wcnmodem.bin`, then `Bluetooth: Core ver 2.22`, `HCI UART protocol H4 registered`, `MGMT ver 1.23`. No Bluetooth errors. One kernel `WARNING` at 123 s is in `gs_usb` (the CAN adapter), not Bluetooth          |
| `lsmod \| grep -i -E 'bt\|uwe\|sprd'`         | `sprdbt_tty`, `uwe5622_bsp_sdio`, `sprdwl_ng` (Wi-Fi), `hci_uart`, `bluetooth`                                                                                                                                                                                                              |
| `systemctl is-active bluetooth NetworkManager` | `active`, `inactive`                                                                                                                                                                                                                                                                        |
| `nmcli -t general status`                     | `nmcli: command not found`. `dpkg-query -W network-manager` reports `not-installed`: NetworkManager is **absent**, not just stopped                                                                                                                                                          |
| `bluetoothd --version`                        | `5.82` (package `bluez 5.82-1.1`); `/etc/bluetooth/main.conf` has only empty section headers, so every default applies                                                                                                                                                                      |

## What manages Wi-Fi

- **netplan** (`netplan.io 1.1.2`) renders `/etc/netplan/10-dhcp-all-interfaces.yaml` and
  `30-wifis-dhcp.yaml` for **systemd-networkd** (active; `networkctl` shows `wlan0` routable,
  configured) and for `netplan-wpa-wlan0.service`.
- `netplan-wpa-wlan0.service` runs `wpa_supplicant -c /run/netplan/wpa-wlan0.conf -iwlan0
  -Dnl80211,wext`. That instance owns `wlan0`. Its control socket is `/run/wpa_supplicant/wlan0`
  (`srwxrwx--- root root` in a `root:netdev 0750` directory), so only root can use it.
- A second, D-Bus wpa_supplicant (`wpa_supplicant.service`, `-u`, bus name
  `fi.w1.wpa_supplicant1`) is also running, but its `Interfaces` property is empty (`ao 0`). It does
  not manage `wlan0`.
- The `/run/netplan/*.conf` file is regenerated from `/etc/netplan` at every boot. Neither file was
  read here; both hold the Wi-Fi passphrase.

**What that means for the Wi-Fi interface (K2).** There is no NetworkManager to call. A runtime scan
and join go through wpa_supplicant's control socket (`SCAN`, `SCAN_RESULTS`, `ADD_NETWORK`,
`SET_NETWORK`, `SELECT_NETWORK`), which needs root. A joined network survives a reboot only if it is
also written as a netplan YAML under `/etc/netplan` (mode 0600), because anything saved into the
`/run` config is lost at boot. ADR-0019 §3 item 5 and §5 step 3 assume NetworkManager ("talk to
NetworkManager"); on this image that is wrong, which is for the master to fold into the ADR.

## Proof: BlueZ advertises the ClipSetup shape and serves a `0x2901` descriptor

Throwaway: stock `bluetoothctl` fed a script (no code, nothing installed), under `sudo timeout 70`,
with `sudo timeout 75 btmon` capturing HCI. It registered:

- a primary service `4617760e-5308-4efe-b137-04e8fa0a5b61`;
- one characteristic `4617ff53-5308-4efe-b137-04e8fa0a5b61`, flags `read,write`. This is ESP-IDF's
  own derivation for the `proto-ver` endpoint: the service UUID with bytes 12–13 (little-endian)
  replaced by `0xFF53`;
- one descriptor `2901`, flags `read`, value `proto-ver`;
- an advertisement with `uuids 4617760e-…`, `name PROV_MYBO_2118` (the firmware's
  `PROV_<KIND>_<last two address bytes>` rule on this controller's address) and
  `discoverable on`.

Result, from `bluetoothctl` and `btmon` (17:26 CT):

- `Application registered`; characteristic at handle `0x0015`, `0x2901` descriptor at `0x0016`.
- `Advertising object registered`, `advertising_added: instance 1`, ActiveInstances 1.
- `LE Set Extended Advertising Parameters`: properties `0x0013` = connectable, scannable,
  **legacy `ADV_IND` PDUs**; interval 1280 ms; TX power selected 7 dBm.
- `LE Set Extended Advertising Data`, 21 bytes: `128-bit Service UUIDs (complete): 1 entry`
  + `Flags: 0x06` (LE General Discoverable, BR/EDR not supported).
- `LE Set Extended Scan Response Data`, 16 bytes: `Name (complete): PROV_MYBO_2118`.
- On exit BlueZ removed the set (`LE Remove Advertising Set`, `Remove UUID`) and set Bondable back to
  disabled. `bluetoothctl` had enabled Bondable while its default agent was registered; a service
  that registers no agent does not touch it.

This settles ADR §3 item 1: BlueZ moves the name into the scan response without being asked, and
the 21 + 16 bytes fit the 31-byte legacy limits.

## Scan from this Mac: refused by macOS

`bleak` (CoreBluetooth) in a venv, run from this Claude Code session at 17:25 CT, exited 134
(SIGABRT) before scanning. The TCC log says why:

```
Refusing authorization request for service kTCCServiceBluetoothAlways and subject
Sub:{com.anthropic.claude-code} ... without NSBluetoothAlwaysUsageDescription key
```

The responsible process is the Claude Code binary, which has no Bluetooth usage description, so
macOS kills any child that touches Bluetooth. This was not worked around. A person can run the same
check from Terminal.app (which can be granted Bluetooth in System Settings):

```
python3 -m venv /tmp/v && /tmp/v/bin/pip install bleak==1.1.1
/tmp/v/bin/python linux/tools/scan.py 15 --gatt   # while the example runs on the board
```

## K2: the Go peripheral on the board (17:43 and 17:48 CT)

`clipsetup-example` and `clipsetup-busprobe` cross-compiled (`CGO_ENABLED=0 GOOS=linux
GOARCH=arm64`), copied to `/tmp/k2`, run under `sudo timeout` with their PIDs recorded, then removed.

- **On the air (btmon):** the same shape as the B0 proof: properties `0x0013` (legacy `ADV_IND`),
  advertising data = the 128-bit UUID + flags `0x06`, scan response = `Name (complete):
  PROV_MYBO_2118`.
- **Fake Wi-Fi backend, through the exported D-Bus objects** (the probe calls
  `GattCharacteristic1.WriteValue`/`ReadValue` as bluetoothd would, at MTU 23 so every request is
  split across offset writes and every long response is read in pieces): five characteristics
  `4617ff50`…`4617ff54`, all `[read write]`, each found by its `0x2901` name; `proto-ver` →
  `{"prov":{"cap":["wifi_scan"],"sec_ver":1,"ver":"v1.1"}}`; security 1 established with the PIN;
  scan 2 networks; `custom-data` → `{"received":17}`; provision → Connected, `192.0.2.10`;
  15 WriteValue and 19 ReadValue calls. The example then stopped advertising and exited 0, as
  ClipSetup does after a join.
- **Wrong PoP:** refused at session command 1 (`proof of possession did not match`).
- **Real wpa_supplicant backend, read-only** (cached `SCAN_RESULTS`, no `SCAN`, no join): session,
  scan (1 cached network; names not recorded) and `custom-data` at MTU 185; SIGINT stopped it
  cleanly and no client socket was left in `/tmp`.

This proves the D-Bus plumbing and the protocol on the board. It is not a radio test: no central
connected over the air, so long reads and writes through a real ATT link are still unproven.

## Risks noted

- **Advertising interval 1280 ms** (BlueZ's default for an application advertisement). iPhones in
  the foreground still find it, but the AccessorySetupKit sheet may take a second or two longer.
  Shortening it needs `MinInterval`/`MaxInterval` on the advertisement (BlueZ experimental) or
  `main.conf` (`[LE] MinAdvertisementInterval`); both are E1's to decide, not changed here.
- The Wi-Fi driver (`sprdwl_ng`, out of tree) and the Bluetooth half of the same UWE5622 chip share
  one radio. Whether a join over Wi-Fi drops the BLE link at that moment is unknown until a phone
  runs the whole flow.

## Still needs a phone, and Logan's hands

1. **nRF Connect** (or the scan above from Terminal.app): see `PROV_MYBO_xxxx` with the service UUID,
   connect, and read the four `0x2901` names.
2. **ESPProvision end to end**: the Narduk Devices app or clip against the board running
   `clipsetup-example`, with the printed link opened on the phone. This is the first real test of
   the session (security 1), of long reads and writes over a real MTU, and of the AccessorySetupKit
   picker matching a name that sits in the scan response.
3. **A real join**: only with `-wifi wpa -allow-join` and a network Logan chooses, because it changes
   the board's Wi-Fi at runtime.
