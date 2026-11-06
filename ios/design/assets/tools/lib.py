#!/usr/bin/env python3
"""Shared helpers for the asset-pack scripts that read kit.json.

Nothing here draws art or touches pixels. `palette` and `brand` in kit.json are information for prompts, briefs and the
contact sheet only; no script recolours a render to them.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path


def die(msg: str):
    sys.exit(f"error: {msg}")


def hex_rgb(h: str) -> tuple[int, int, int]:
    h = h.lstrip("#")
    if len(h) != 6:
        die(f"bad hex colour {h!r}")
    return tuple(int(h[i:i + 2], 16) for i in (0, 2, 4))  # type: ignore[return-value]


def load_kit(root) -> dict:
    """Read <root>/kit.json and add `_root` and `_palette` ({role: (r, g, b)}). See SKILL.md for the schema."""
    root = Path(root).resolve()
    p = root / "kit.json"
    if not p.is_file():
        die(f"{p} not found; `kit.py keep` writes it")
    man = json.loads(p.read_text())
    man["_root"] = root
    man["_palette"] = {k: hex_rgb(v) for k, v in (man.get("palette") or {}).items()}
    return man
