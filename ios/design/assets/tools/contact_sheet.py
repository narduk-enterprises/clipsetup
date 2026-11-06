#!/usr/bin/env python3
"""Contact sheet of a derived kit on white and on ink (dark-mode check). PIL only, no browser.

usage: contact_sheet.py --root KIT [--ink #0E1418]   -> KIT/.lane/contact-sheet.png (gitignored)
Reads KIT/out/, KIT/icons/png/ (1x) and KIT/icons/svg/ (icons kept with --vector only). Look at it at the size it ships:
a white box around a render on ink, a muddy 16 px favicon and wrong crops show here. These are the pixels the model drew;
a flaw means regenerate. A `-light` asset is shown on white only, a `-dark` one (its own kept render) on ink only.
"""
from __future__ import annotations

import argparse
import subprocess
from io import BytesIO
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from lib import die, hex_rgb, load_kit

PANEL_W, GAP, PAD = 900, 20, 16
SECTIONS = [("App icon and favicon", ("out/app-icon", "out/apple", "out/icon-", "out/favicon"), 80),
            ("UI icons (48px)", ("icons/svg/", "icons/png/"), 48),
            ("Illustrations and heroes", ("out/",), 0),
            ("OG", ("out/og",), 0)]


def files(root: Path) -> list[str]:
    found = [p for p in sorted((root / "out").rglob("*")) if p.suffix in (".png", ".svg") and p.is_file()]
    found += sorted((root / "icons" / "svg").glob("*.svg"))
    found += [p for p in sorted((root / "icons" / "png").glob("*.png")) if "@" not in p.stem]  # raster icons, 1x
    return [str(p.relative_to(root)) for p in found]


def load(path: Path, fg: str, h: int, w_cap: int) -> Image.Image | None:
    if path.suffix == ".svg":
        svg = path.read_text().replace("currentColor", fg)
        if "<svg" in svg and 'xmlns' in svg:
            png = subprocess.run(["rsvg-convert", "-h", str(h)], input=svg.encode(), capture_output=True, check=True).stdout
            return Image.open(BytesIO(png)).convert("RGBA")
        return None
    if path.suffix == ".png":
        im = Image.open(path).convert("RGBA")
        if h:
            # tiny native-size rasters (favicons) are magnified with NEAREST so the sheet shows the real pixels
            im = im.resize((max(1, round(im.width * h / im.height)), h), Image.NEAREST if im.height <= 64 else Image.LANCZOS)
        else:
            im = im.resize((w_cap, round(im.height * w_cap / im.width)), Image.LANCZOS)
        return im
    return None


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", type=Path, default=Path.cwd(), help="kit dir (default: cwd)")
    ap.add_argument("--ink", help="ink ground hex; default: the palette's `ink` role, else #0E1418")
    a = ap.parse_args()
    kit = load_kit(a.root)
    out = kit["_root"]
    ink = hex_rgb(a.ink) if a.ink else kit["_palette"].get("ink", hex_rgb("#0E1418"))
    all_files = files(out)
    if not all_files:
        die("nothing derived yet: run kit.py derive first (and trace.py for icons marked vector)")
    font = ImageFont.load_default(size=11)
    big = ImageFont.load_default(size=16)
    panels = []
    for bg, fg in (((255, 255, 255), "#000000"), (ink, "#FFFFFF")):
        dark_panel = bg != (255, 255, 255)
        shown = [p for p in all_files if not (("-light" in Path(p).stem and dark_panel)
                                              or ("-dark" in Path(p).stem and not dark_panel))]
        used: set[str] = set()
        secs = []
        for title, prefixes, h in SECTIONS:
            paths = [p for p in shown if p.startswith(prefixes) and p not in used]
            if title.startswith("Illustrations"):  # everything under out/ the earlier sections did not take, minus OG
                paths = [p for p in paths if not p.startswith("out/og")]
            used.update(paths)
            w_cap = PANEL_W - 2 * PAD if h == 0 else 280
            tiles = [(p, load(out / p, fg, h or 200, w_cap)) for p in paths]
            secs.append((title, [(p, t) for p, t in tiles if t]))
        rows, y = [], 34
        for title, tiles in secs:
            if not tiles:
                continue
            rows.append(("title", title, y))
            y += 26
            x, rowh = PAD, 0
            for p, t in tiles:
                if x + t.width > PANEL_W - PAD and x > PAD:
                    x, y, rowh = PAD, y + rowh + 16, 0
                rows.append(("tile", (p, t), (x, y)))
                x += t.width + 12
                rowh = max(rowh, t.height)
            y += rowh + 26
        panels.append((bg, fg, rows, y))
    H = max(p[3] for p in panels)
    sheet = Image.new("RGB", (PANEL_W * 2 + GAP * 3, H + 50), (128, 128, 128))
    d = ImageDraw.Draw(sheet)
    d.text((GAP, 12), f"{kit['name']} asset kit: {len(all_files)} files", fill=(255, 255, 255), font=big)
    for i, (bg, fg, rows, y) in enumerate(panels):
        ox, oy = GAP + i * (PANEL_W + GAP), 40
        d.rectangle([ox, oy, ox + PANEL_W, oy + H], fill=bg)
        for kind, val, pos in rows:
            txtc = (0, 0, 0) if fg == "#000000" else (255, 255, 255)
            if kind == "title":
                d.text((ox + PAD, oy + pos), val, fill=txtc, font=big)
            else:
                p, t = val
                x, yy = pos
                sheet.paste(t, (ox + x, oy + yy), t)
                d.text((ox + x, oy + yy + t.height + 1), Path(p).stem[:max(5, t.width // 6)], fill=(140, 140, 140), font=font)
    (out / ".lane").mkdir(exist_ok=True)
    dest = out / ".lane" / "contact-sheet.png"
    sheet.save(dest, optimize=True)
    print("wrote", dest, sheet.size)


if __name__ == "__main__":
    main()
