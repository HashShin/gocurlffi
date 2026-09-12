#!/usr/bin/env python3
"""Compare gocurlffi fingerprints against the installed curl_cffi.

For every impersonation target both clients request tls.browserleaks.com/json
and the reported TLS/HTTP2 fingerprints are compared.

Two sources of noise are removed:

* curl_cffi's libcurl keeps a TLS session cache, so each reference is fetched in
  a fresh interpreter.
* Presets with ECH randomise the GREASE-ECH payload length, which makes
  BoringSSL emit the padding extension only when the ClientHello is short. The
  comparison therefore drops padding (extension 21) from the JA3N extension
  list and compares the resulting cipher/extension/curve sets.

Usage: python3 scripts/compare_fingerprints.py [target ...]
"""

from __future__ import annotations

import json
import os
import subprocess
import sys

BINARY = os.environ.get("GOCURLFFI_BIN", "bin/gocurlffi")
URL = "https://tls.browserleaks.com/json"


def normalize_ja3n(text: str) -> str | None:
    """Drop GREASE/padding noise from a ja3n string."""
    parts = text.split(",")
    if len(parts) < 5:
        return None
    version, ciphers, exts, curves, formats = parts[0], parts[1], parts[2], parts[3], parts[4]
    ext_list = [e for e in exts.split("-") if e not in ("", "21")]
    return "|".join([version, ciphers, "-".join(sorted(ext_list)), curves, formats])


def fetch_python(target: str) -> dict | None:
    code = (
        "import json\n"
        "from curl_cffi import requests\n"
        f"r=requests.get({URL!r}, impersonate={target!r})\n"
        "d=r.json()\n"
        "print(json.dumps({'ja3n': d.get('ja3n_text'), 'ja4': d.get('ja4'),"
        " 'akamai': d.get('akamai_hash')}))\n"
    )
    try:
        out = subprocess.run(
            [sys.executable, "-c", code],
            capture_output=True, text=True, timeout=60, check=True,
        ).stdout
        return json.loads(out)
    except Exception:
        return None


def fetch_go(target: str) -> dict | None:
    try:
        out = subprocess.run(
            [BINARY, "get", URL, "-i", target, "--body"],
            capture_output=True, text=True, timeout=60, check=True,
        ).stdout
        d = json.loads(out)
        return {
            "ja3n": d.get("ja3n_text"),
            "ja4": d.get("ja4"),
            "akamai": d.get("akamai_hash"),
        }
    except Exception:
        return None


def main() -> int:
    from curl_cffi.requests.impersonate import BrowserType

    targets = sys.argv[1:] or [b.value for b in BrowserType]
    bad = 0
    for t in targets:
        py = fetch_python(t)
        if py is None:
            print(f"{t:18} (not supported by installed curl_cffi)")
            continue
        go = fetch_go(t)
        if go is None:
            print(f"{t:18} go: FAILED")
            bad += 1
            continue
        diffs = []
        if normalize_ja3n(py["ja3n"] or "") != normalize_ja3n(go["ja3n"] or ""):
            diffs.append("ja3n")
        if py["akamai"] != go["akamai"]:
            diffs.append("akamai")
        if diffs:
            bad += 1
            print(f"{t:18} MISMATCH {diffs}")
            print(f"    py ja3n: {normalize_ja3n(py['ja3n'] or '')}")
            print(f"    go ja3n: {normalize_ja3n(go['ja3n'] or '')}")
            print(f"    py akamai={py['akamai']} go akamai={go['akamai']}")
        else:
            print(f"{t:18} ok")
    print(f"\n{bad} mismatches")
    return 1 if bad else 0


if __name__ == "__main__":
    raise SystemExit(main())
