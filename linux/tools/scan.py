"""Scan with CoreBluetooth (via bleak) and report any advertiser carrying the ClipSetup UUID or a PROV_ name.

Optionally connect and list the GATT service, characteristics and 0x2901 descriptors.
Usage: python scan.py [seconds] [--gatt]
"""
import asyncio
import sys

from bleak import BleakClient, BleakScanner

UUID = "4617760e-5308-4efe-b137-04e8fa0a5b61"


async def main() -> None:
    secs = float(sys.argv[1]) if len(sys.argv) > 1 and not sys.argv[1].startswith("-") else 15.0
    want_gatt = "--gatt" in sys.argv
    found = await BleakScanner.discover(timeout=secs, return_adv=True)
    print(f"scanned {secs}s, {len(found)} advertisers")
    hit = None
    for addr, (dev, adv) in found.items():
        uuids = [u.lower() for u in (adv.service_uuids or [])]
        name = adv.local_name or dev.name or ""
        if UUID in uuids or name.startswith("PROV_"):
            print(f"MATCH id={addr} local_name={adv.local_name!r} name={dev.name!r} rssi={adv.rssi} uuids={uuids}")
            hit = dev
    if hit is None:
        print("no ClipSetup advertiser seen")
        return
    if not want_gatt:
        return
    async with BleakClient(hit, timeout=20.0) as client:
        for svc in client.services:
            if svc.uuid.lower() != UUID:
                continue
            print(f"service {svc.uuid}")
            for ch in svc.characteristics:
                names = []
                for d in ch.descriptors:
                    if d.uuid.lower().startswith("00002901"):
                        names.append((await client.read_gatt_descriptor(d.handle)).decode(errors="replace"))
                print(f"  char {ch.uuid} props={ch.properties} 2901={names}")
            pv = [c for c in svc.characteristics if any(
                d.uuid.lower().startswith("00002901") for d in c.descriptors)]
            for ch in pv:
                for d in ch.descriptors:
                    if d.uuid.lower().startswith("00002901"):
                        if (await client.read_gatt_descriptor(d.handle)).decode() == "proto-ver":
                            await client.write_gatt_char(ch, b"ESP", response=True)
                            print("  proto-ver ->", (await client.read_gatt_char(ch)).decode(errors="replace"))


asyncio.run(main())
