#!/usr/bin/env python3
"""Compare HTTP status codes between curl_cffi and gocurlffi across sites."""
from __future__ import annotations

import os
import subprocess
import sys

BINARY = os.environ.get("GOCURLFFI_BIN", "bin/gocurlffi")
TARGET = os.environ.get("PARITY_TARGET", "chrome131")
SITES = sys.argv[1:] or [
    "https://tls.browserleaks.com/json",
    "https://tls.peet.ws/api/all",
    "https://httpbin.org/get",
    "https://www.google.com",
    "https://www.cloudflare.com",
    "https://nowsecure.nl",
    "https://www.wikipedia.org",
    "https://www.nike.com",
    "https://www.adidas.co.uk",
    "https://www.zillow.com",
    "https://www.g2.com",
]


def py_status(url: str) -> str:
    from curl_cffi import requests

    try:
        r = requests.get(url, impersonate=TARGET, timeout=30)
        return str(r.status_code)
    except Exception as e:  # noqa: BLE001
        return f"ERR:{type(e).__name__}"


def go_status(url: str) -> str:
    try:
        out = subprocess.run(
            [BINARY, "get", url, "-i", TARGET, "--headers", "-t", "30"],
            capture_output=True, text=True, timeout=40,
        ).stdout.splitlines()
        for line in out:
            if line.startswith("HTTP/"):
                return line.split()[1]
        return "ERR:" + (out[0][:30] if out else "no output")
    except Exception as e:  # noqa: BLE001
        return f"ERR:{type(e).__name__}"


def main() -> int:
    bad = 0
    for url in SITES:
        py = py_status(url)
        go = go_status(url)
        mark = "ok " if py == go else "DIFF"
        if py != go:
            bad += 1
        print(f"{mark} py={py:>10} go={go:>10}  {url}")
    print(f"\n{bad} differences")
    return 1 if bad else 0


if __name__ == "__main__":
    raise SystemExit(main())
