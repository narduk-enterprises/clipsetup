"""Drive the Go device with ESP-IDF's own provisioning client (tools/esp_prov), for interop vectors.

Not run in CI. TestESPProvInterop starts it when CLIPSETUP_ESP_IDF names an ESP-IDF checkout
(v5.3.1 was used; only tools/esp_prov and the components/*/python *_pb2.py files are needed) and
CLIPSETUP_PYTHON names a Python with `protobuf` and `cryptography`. Espressif's files are loaded
from that checkout and are not copied into this repository.

Protocol on stdin/stdout, one line each:
    -> REQ <endpoint> <hex request>
    <- RESP <hex response> | ERR <message>
    -> DONE <json results>
esp_prov's own prints go to stderr. The client key is pinned so a run is reproducible.
"""
import json
import os
import sys

proto_out = os.fdopen(os.dup(1), "w", buffering=1)
sys.stdout = sys.stderr

idf = os.environ["IDF_PATH"]
sys.path.insert(0, os.path.join(idf, "tools", "esp_prov"))

from cryptography.hazmat.primitives.asymmetric import x25519  # noqa: E402

CLIENT_PRIVATE = bytes(range(0x40, 0x60))  # pinned client key for a fixed transcript
x25519.X25519PrivateKey.generate = staticmethod(lambda: x25519.X25519PrivateKey.from_private_bytes(CLIENT_PRIVATE))

import prov  # noqa: E402  (esp_prov/prov)
import security  # noqa: E402  (esp_prov/security)


def transact(endpoint, data):
    if isinstance(data, str):
        data = data.encode("latin-1")
    proto_out.write(f"REQ {endpoint} {data.hex()}\n")
    line = sys.stdin.readline().strip()
    kind, _, rest = line.partition(" ")
    if kind != "RESP":
        raise RuntimeError(f"{endpoint}: {rest}")
    return bytes.fromhex(rest).decode("latin-1")


def main():
    pop, ssid, passphrase = sys.argv[1], sys.argv[2], sys.argv[3]
    out = {}
    out["proto_ver"] = json.loads(transact("proto-ver", "ESP"))

    sec = security.Security1(pop, False)
    resp = None
    while True:
        req = sec.security_session(resp)
        if req is None:
            break
        resp = transact("prov-session", req)
    out["session"] = "ok"

    prov.scan_start_response(sec, transact("prov-scan", prov.scan_start_request(sec, blocking=True)))
    status = prov.scan_status_response(sec, transact("prov-scan", prov.scan_status_request(sec)))
    nets = []
    for index in range(0, status["count"], 4):
        count = min(4, status["count"] - index)
        nets += prov.scan_result_response(sec, transact("prov-scan", prov.scan_result_request(sec, index, count)))
    out["scan"] = nets

    custom = transact("custom-data", prov.custom_data_request(sec, '{"token":"golden"}'))
    out["custom"] = sec.decrypt_data(custom.encode("latin-1")).decode("latin-1")

    out["set_config"] = prov.config_set_config_response(sec, transact("prov-config", prov.config_set_config_request(sec, ssid, passphrase)))
    out["apply_config"] = prov.config_apply_config_response(sec, transact("prov-config", prov.config_apply_config_request(sec)))
    out["get_status"] = prov.config_get_status_response(sec, transact("prov-config", prov.config_get_status_request(sec)))
    proto_out.write("DONE " + json.dumps(out, sort_keys=True) + "\n")


main()
