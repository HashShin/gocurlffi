#!/usr/bin/env python3
"""Compare gocurlffi fingerprints against the installed curl_cffi.

Requires the gocurlffi binary to be built (see Makefile). Prints one row per
target with the JA3N/JA4/Akamai hashes from both implementations.

Usage: python3 scripts/compare_fingerprints.py [target ...]
"""
from __future__ import annotations

import json
import os
import subprocess
import sys

BINARY = os.environ.get("GOCURLFFI_BIN", "gocurlffi")
URL = "https://tls.browserleaks.com/json"
KEYS = ["ja3n_hash", "ja4", "akamai_hash"]


def python_fp(target: str) -> dict | None:
    from curl_cffi import requests

    try:
        r = requests.get(URL, impersonate=target)
        return r.json()
    except Exception:
        return None


def go_fp(target: str) -> dict | None:
    try:
        out = subprocess.run(
            [BINARY, "get", URL, "-i", target, "--body"],
            capture_output=True, text=True, timeout=40, check=True,
        ).stdout
        return json.loads(out)
    except Exception:
        return None


def main() -> int:
    from curl_cffi.requests.impersonate import BrowserType

    targets = sys.argv[1:] or [b.value for b in BrowserType]
    mismatches = 0
    for t in targets:
        py = python_fp(t)
        if py is None:
            print(f"{t:18} (not supported by installed curl_cffi)")
            continue
        go = go_fp(t)
        if go is None:
            print(f"{t:18} go: FAILED")
            mismatches += 1
            continue
        diffs = [k for k in KEYS if py.get(k) != go.get(k)]
        if diffs:
            mismatches += 1
            print(f"{t:18} MISMATCH {diffs}")
            for k in KEYS:
                print(f"    {k}: py={py.get(k)} go={go.get(k)}")
        else:
            print(f"{t:18} ok")
    print(f"\n{mismatches} mismatches")
    return 1 if mismatches else 0


if __name__ == "__main__":
    raise SystemExit(main())
