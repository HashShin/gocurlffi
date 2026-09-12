#!/usr/bin/env python3
"""Parse curl-impersonate's lib/impersonate.c into impersonate/presets.json.

The C file is a single array of `struct impersonate_opts` initializers. We do a
small brace-aware scan so that string literals, comments and nested header
arrays are handled correctly, then emit one JSON object per preset.

Usage: python3 scripts/gen_presets.py
"""

from __future__ import annotations

import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
SRC = os.path.join(ROOT, "impersonate", "upstream", "impersonate.c")
OUT = os.path.join(ROOT, "impersonate", "presets.json")

# Fields whose value is an integer.
INT_FIELDS = {
    "http2_window_update",
    "http2_stream_weight",
    "http2_stream_exclusive",
    "tls_record_size_limit",
    "tls_key_shares_limit",
}
# Fields whose value is a boolean.
BOOL_FIELDS = {
    "npn",
    "alpn",
    "alps",
    "tls_session_ticket",
    "ws_disable_session_ticket",
    "tls_permute_extensions",
    "tls_use_new_alps_codepoint",
    "tls_signed_cert_timestamps",
    "tls_grease",
    "http2_no_priority",
    "proxy_credential_no_reuse",
    "split_cookies",
}
# Fields whose value is a C array of strings.
ARRAY_FIELDS = {"http_headers", "http3_headers", "ws_headers"}


def strip_comments(text: str) -> str:
    """Remove /* */ and // comments, preserving string literals."""
    out = []
    i = 0
    n = len(text)
    in_str = False
    while i < n:
        c = text[i]
        if in_str:
            out.append(c)
            if c == "\\":
                if i + 1 < n:
                    out.append(text[i + 1])
                    i += 2
                    continue
            elif c == '"':
                in_str = False
            i += 1
            continue
        if c == '"':
            in_str = True
            out.append(c)
            i += 1
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "*":
            j = text.find("*/", i + 2)
            i = n if j < 0 else j + 2
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "/":
            j = text.find("\n", i)
            i = n if j < 0 else j
            continue
        out.append(c)
        i += 1
    return "".join(out)


def unescape_c(s: str) -> str:
    return s.replace("\\\"", '"').replace("\\\\", "\\")


def parse_array(body: str) -> list[str]:
    """Parse `{ "a", "b", }` (body excludes the outer braces)."""
    body = body.strip()
    if body == "":
        return []
    return [unescape_c(m.group(1)) for m in re.finditer(r'"((?:[^"\\]|\\.)*)"', body)]


def parse_scalar(body: str):
    body = body.strip()
    if body == "NULL":
        return None
    if body == "true":
        return True
    if body == "false":
        return False
    strings = re.findall(r'"((?:[^"\\]|\\.)*)"', body)
    if strings:
        return "".join(unescape_c(s) for s in strings)
    integers = re.findall(r"0x[0-9a-fA-F]+|-?\d+", body)
    if integers:
        return int(integers[0], 0)
    return body


def split_top_level(s: str, sep: str = ",") -> list[str]:
    parts: list[str] = []
    depth = 0
    cur: list[str] = []
    i = 0
    n = len(s)
    in_str = False
    while i < n:
        c = s[i]
        if in_str:
            cur.append(c)
            if c == "\\" and i + 1 < n:
                cur.append(s[i + 1])
                i += 2
                continue
            if c == '"':
                in_str = False
            i += 1
            continue
        if c == '"':
            in_str = True
            cur.append(c)
        elif c in "{[(":
            depth += 1
            cur.append(c)
        elif c in "}])":
            depth -= 1
            cur.append(c)
        elif c == sep and depth == 0:
            parts.append("".join(cur))
            cur = []
        else:
            cur.append(c)
        i += 1
    if cur:
        parts.append("".join(cur))
    return parts


def split_assignments(block: str) -> list[tuple[str, str]]:
    """Return (field, raw_value) pairs for a struct initializer body."""
    result: list[tuple[str, str]] = []
    for item in split_top_level(block):
        item = item.strip()
        if not item:
            continue
        m = re.match(r"^\.([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$", item, re.S)
        if not m:
            continue
        result.append((m.group(1), m.group(2).strip()))
    return result


def parse_preset(block: str) -> dict:
    preset: dict = {}
    for field, raw in split_assignments(block):
        if raw.startswith("{") and raw.endswith("}"):
            preset[field] = parse_array(raw[1:-1])
        else:
            preset[field] = parse_scalar(raw)
    # Coerce declared types.
    for f in list(preset):
        if f in INT_FIELDS and not isinstance(preset[f], int):
            preset[f] = int(preset[f] or 0)
        if f in BOOL_FIELDS and not isinstance(preset[f], bool):
            preset[f] = bool(preset[f])
        if f in ARRAY_FIELDS and not isinstance(preset[f], list):
            preset[f] = []
    return preset


def main() -> int:
    raw = open(SRC, encoding="utf-8").read()
    text = strip_comments(raw)
    start = text.find("impersonations[]")
    if start < 0:
        print("could not find impersonations[]", file=sys.stderr)
        return 1
    body_start = text.find("{", start)
    presets = []
    i = body_start + 1
    n = len(text)
    depth = 0
    in_str = False
    block: list[str] = []
    capturing = False
    while i < n:
        c = text[i]
        if in_str:
            block.append(c)
            if c == "\\" and i + 1 < n:
                block.append(text[i + 1])
                i += 2
                continue
            if c == '"':
                in_str = False
            i += 1
            continue
        if c == '"':
            in_str = True
            block.append(c)
            i += 1
            continue
        if c == "{":
            depth += 1
            capturing = True
            block.append(c)
            i += 1
            continue
        if c == "}":
            depth -= 1
            block.append(c)
            if depth == 0:
                if capturing:
                    presets.append(parse_preset("".join(block)[1:-1]))
                block = []
                capturing = False
            i += 1
            continue
        if capturing:
            block.append(c)
        i += 1

    presets = [p for p in presets if p.get("target")]
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(presets, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"wrote {len(presets)} presets to {OUT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
