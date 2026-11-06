#!/usr/bin/env python3
"""Quick gate on raw renders: size, aspect bucket, background flatness, palette coverage, sheet slice.

usage: inspect_raw.py --root KIT RUN_OR_FILE... [--sheet ROWSxCOLS]
The palette is `palette` in <KIT>/kit.json (without one only size, bucket and corner drift print). Prints one line
per image. Use it right after a Codex run and after picking BEST; the buckets are Codex's (grok sizes differ: a 1:1
render is 1024x1024). Read the flags:
  HAZE   many pixels are neither palette colours nor anti-aliasing: the haze ships as drawn, so regenerate
  BGVAR  the four corners disagree: not a flat ground (gradient/vignette/texture), regenerate
  OFFPAL a large share is far from every palette colour (wrong brand colours, gradients)
  SLICE  (with --sheet) the icon sheet does not cut into exactly ROWSxCOLS icons: regenerate
"""
from __future__ import annotations

import argparse
from pathlib import Path

import numpy as np
from PIL import Image

from lib import load_kit
from slice_sheet import SheetError, slice_sheet

BUCKETS = {(1254, 1254): "1:1", (1448, 1086): "4:3", (1586, 992): "16:10", (1730, 909): "~1.9:1",
           (1672, 941): "16:9", (2172, 724): "3:1", (1086, 1448): "3:4", (992, 1586): "10:16"}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", type=Path, default=Path.cwd(), help="kit dir (default: cwd)")
    ap.add_argument("paths", nargs="+")
    ap.add_argument("--sheet", help="ROWSxCOLS, e.g. 3x4, to test slicing")
    a = ap.parse_args()
    pal_d = load_kit(a.root)["_palette"]
    pal = np.array(list(pal_d.values()), dtype=np.int32) if pal_d else None
    files: list[Path] = []
    for p in map(Path, a.paths):
        files += sorted(p.rglob("*.png")) if p.is_dir() else [p]
    for f in files:
        if "generated_images" not in str(f) and not f.is_file():
            continue
        im = Image.open(f).convert("RGB")
        arr = np.asarray(im).astype(np.int32)
        exact = aa = off = float("nan")
        if pal is not None:
            d = np.sqrt(((arr[::4, ::4, None, :] - pal[None, None]) ** 2).sum(-1)).min(-1)
            exact, aa, off = (d <= 26).mean(), ((d > 26) & (d <= 90)).mean(), (d > 90).mean()
        k = max(4, min(im.size) // 100)
        cs = np.array([arr[:k, :k].mean((0, 1)), arr[:k, -k:].mean((0, 1)), arr[-k:, :k].mean((0, 1)), arr[-k:, -k:].mean((0, 1))])
        bgvar = float(np.abs(cs - cs.mean(0)).max())
        flags = []
        if bgvar > 12:
            flags.append("BGVAR")
        if pal is not None and aa > 0.20:
            flags.append("HAZE")
        if pal is not None and off > 0.25:
            flags.append("OFFPAL")
        if a.sheet:
            r, c = map(int, a.sheet.lower().split("x"))
            try:
                n = len(slice_sheet(f, r, c))
                flags.append(f"slice-ok({n})")
            except SheetError as e:
                flags.append(f"SLICE({e})")
        print(f"{f.name}: {im.width}x{im.height} [{BUCKETS.get(im.size, 'other')}] "
              f"near-palette {exact:.0%} blended {aa:.0%} off {off:.0%} corner-drift {bgvar:.0f} {' '.join(flags)}")


if __name__ == "__main__":
    main()
