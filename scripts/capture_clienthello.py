#!/usr/bin/env python3
"""Capture the raw ClientHello curl_cffi sends for each impersonation target.

A local TCP listener accepts one connection, records the bytes, and the
impersonation target is pointed at it (the TLS handshake is expected to fail).
The captured ClientHello is parsed into cipher/extension order so we can verify
or seed the Go fingerprints.

Usage: python3 scripts/capture_clienthello.py [target ...]
"""

from __future__ import annotations

import json
import os
import socket
import sys
import threading

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

GREASE = {
    0x0A0A, 0x1A1A, 0x2A2A, 0x3A3A, 0x4A4A, 0x5A5A, 0x6A6A, 0x7A7A,
    0x8A8A, 0x9A9A, 0xAAAA, 0xBABA, 0xCACA, 0xDADA, 0xEAEA, 0xFAFA,
}


def read_client_hello(sock: socket.socket) -> bytes:
    sock.settimeout(5)
    data = b""
    while len(data) < 5:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk
    if len(data) < 5:
        return data
    length = int.from_bytes(data[3:5], "big")
    while len(data) < 5 + length:
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk
    return data


def parse_client_hello(data: bytes) -> dict:
    if len(data) < 5 + 4 or data[0] != 0x16:
        raise ValueError("not a TLS handshake record")
    body = data[5:]
    if body[0] != 0x01:
        raise ValueError("not a ClientHello")
    p = 4  # skip handshake header
    legacy_version = int.from_bytes(body[p:p + 2], "big")
    p += 2
    p += 32  # random
    sid_len = body[p]
    p += 1 + sid_len
    cs_len = int.from_bytes(body[p:p + 2], "big")
    p += 2
    ciphers = [
        int.from_bytes(body[p + i:p + i + 2], "big") for i in range(0, cs_len, 2)
    ]
    p += cs_len
    comp_len = body[p]
    p += 1 + comp_len
    exts = []
    curves = []
    sigalgs = []
    if p + 2 <= len(body):
        ext_total = int.from_bytes(body[p:p + 2], "big")
        p += 2
        end = p + ext_total
        while p + 4 <= end:
            etype = int.from_bytes(body[p:p + 2], "big")
            elen = int.from_bytes(body[p + 2:p + 4], "big")
            edata = body[p + 4:p + 4 + elen]
            exts.append(etype)
            if os.environ.get("CAPTURE_HEX"):
                print(f"    ext {etype}: {edata.hex()}")
            if etype == 10 and len(edata) >= 2:
                n = int.from_bytes(edata[0:2], "big")
                curves = [
                    int.from_bytes(edata[2 + i:4 + i], "big")
                    for i in range(0, n, 2)
                ]
            if etype == 13 and len(edata) >= 2:
                n = int.from_bytes(edata[0:2], "big")
                sigalgs = [
                    int.from_bytes(edata[2 + i:4 + i], "big")
                    for i in range(0, n, 2)
                ]
            p += 4 + elen
    return {
        "legacy_version": legacy_version,
        "ciphers": ciphers,
        "extensions": exts,
        "curves": curves,
        "sigalgs": sigalgs,
        "cipher_count_no_grease": len([c for c in ciphers if c not in GREASE]),
        "ext_count_no_grease": len([e for e in exts if e not in GREASE]),
        "ciphers_clean": [c for c in ciphers if c not in GREASE],
        "extensions_clean": [e for e in exts if e not in GREASE],
    }


def capture_with_runner(runner) -> dict | None:
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", 0))
    srv.listen(1)
    port = srv.getsockname()[1]
    captured: dict = {}

    def serve():
        conn, _ = srv.accept()
        try:
            captured["raw"] = read_client_hello(conn)
        finally:
            conn.close()

    t = threading.Thread(target=serve, daemon=True)
    t.start()
    runner(port)
    t.join(timeout=6)
    srv.close()
    raw = captured.get("raw")
    if not raw:
        return None
    return parse_client_hello(raw)


def capture(impersonate: str) -> dict | None:
    if impersonate == "curl":
        return capture_with_runner(_run_curl)
    return capture_with_runner(lambda port: _run_curl_cffi(impersonate, port))


def _run_curl(port: int) -> None:
    import subprocess

    subprocess.run(
        ["curl", "-s", "--http2", "-k", "-o", os.devnull, f"https://localhost:{port}/"],
        capture_output=True,
        timeout=8,
    )


def _run_curl_cffi(impersonate: str, port: int) -> None:
    from curl_cffi import requests

    try:
        requests.get(
            f"https://localhost:{port}/",
            impersonate=impersonate,
            verify=False,
            timeout=5,
        )
    except Exception:
        pass


def main() -> int:
    targets = sys.argv[1:]
    if targets == ["--all"]:
        from curl_cffi.requests.impersonate import BrowserType

        targets = [b.value for b in BrowserType]
    elif not targets:
        print("usage: capture_clienthello.py <target>... | --all", file=sys.stderr)
        return 2
    out = {}
    for t in targets:
        try:
            info = capture(t)
        except Exception as e:  # noqa: BLE001
            print(f"{t}: error {e}", file=sys.stderr)
            continue
        if info is None:
            print(f"{t}: no ClientHello captured (unsupported?)", file=sys.stderr)
            continue
        out[t] = info
        print(t)
        print("  ciphers :", "-".join(str(c) for c in info["ciphers"]))
        print("  exts    :", "-".join(str(e) for e in info["extensions"]))
        print("  exts_raw:", "-".join(str(e) for e in info["extensions_clean"]))
        print("  curves  :", "-".join(str(c) for c in info["curves"]))
        print("  sigalgs :", "-".join(str(c) for c in info["sigalgs"]))
    if os.environ.get("CAPTURE_JSON"):
        print(json.dumps(out, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
