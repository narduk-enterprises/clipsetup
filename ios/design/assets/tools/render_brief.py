#!/usr/bin/env python3
"""Render a Codex run brief (sol or astra): shared brand header (from kit.json `brand`) + one template, placeholders filled.

usage: render_brief.py --root KIT TEMPLATE [--var KEY=VALUE | KEY=@file]... [--assets N] > RUN/brief.md
       render_brief.py --root KIT --list        # templates, their placeholders and defaults
TEMPLATE is a name in templates/ without .md (icon-sheet, avatars, illustrations, marketing,
logo-exploration, logo-master, illustration-dark). Placeholders are {UPPER_CASE}. Brand ones come
from kit.json `brand` and `palette`; template defaults live in its first-line <!-- defaults: {...} --> comment.
Exits non-zero, naming them, if any placeholder is left unresolved. --assets N sets the image
count used for the cap in the header (default: inferred from N_AVATARS/N_ILLOS/N_MKT/N_IMAGES, else 1).
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

from lib import die, load_kit

TEMPLATES = Path(__file__).resolve().parents[1] / "templates"
PH = re.compile(r"\{([A-Z][A-Z0-9_]*)\}")
BRAND_KEYS = ("blurb", "avoid", "illustration_style", "icon_style", "text_rule")


def template_parts(name: str):
    text = (TEMPLATES / f"{name}.md").read_text()
    m = re.match(r"<!--\s*defaults:\s*(\{.*?\})\s*-->\n", text, re.S)
    defaults = json.loads(m.group(1)) if m else {}
    return (text[m.end():] if m else text), defaults


def brand_vars(cfg: dict) -> dict:
    b = cfg.get("brand") or die("kit.json has no 'brand' block (see kit.example.json)")
    for k in BRAND_KEYS:
        if not b.get(k):
            die(f"kit.json brand.{k} is required")
    if not cfg.get("palette"):
        die("kit.json has no 'palette' block (see kit.example.json)")
    pal = "; ".join(f"{k} {v}" for k, v in cfg["palette"].items())
    if b.get("palette_note"):
        pal += ". " + b["palette_note"]
    retries = int(b.get("max_retries", 2))
    return {"BRAND_NAME": cfg["name"], "BRAND_BLURB": b["blurb"], "BRAND_AVOID": b["avoid"], "PALETTE_LINE": pal,
            "ILLUSTRATION_STYLE": b["illustration_style"], "ICON_STYLE": b["icon_style"], "TEXT_RULE": b["text_rule"],
            "BRAND_EXTRA": "\n".join("- " + x for x in b.get("extra", [])), "MAX_RETRIES": str(retries)}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", type=Path, default=Path.cwd(), help="kit dir (default: cwd)")
    ap.add_argument("template", nargs="?")
    ap.add_argument("--var", action="append", default=[])
    ap.add_argument("--assets", type=int)
    ap.add_argument("--list", action="store_true")
    a = ap.parse_args()
    if a.list:
        for f in sorted(TEMPLATES.glob("[!_]*.md")):
            body, defaults = template_parts(f.stem)
            need = sorted(set(PH.findall(body)) - set(defaults) - set(brand_keys_static()))
            print(f"{f.stem}: needs {', '.join(need) or '-'}; defaults {json.dumps(defaults)}")
        return
    if not a.template:
        die("give a TEMPLATE name (or --list)")
    cfg = load_kit(a.root)
    body, defaults = template_parts(a.template)
    v = dict(brand_vars(cfg))
    v.update(defaults)
    for kv in a.var:
        k, _, val = kv.partition("=")
        if not k or not _:
            die(f"--var needs KEY=VALUE, got {kv!r}")
        v[k] = Path(val[1:]).read_text().rstrip("\n") if val.startswith("@") else val
    n = a.assets or next((int(v[k]) for k in ("N_AVATARS", "N_ILLOS", "N_MKT", "N_IMAGES") if k in v and v[k].isdigit()), 1)
    v["IMAGE_CAP"] = str(n * (1 + int(v["MAX_RETRIES"])))
    text = (TEMPLATES / "_header.md").read_text().split("\n", 1)[1] + body
    missing = sorted({k for k in PH.findall(text) if k not in v})
    if missing:
        sys.exit("unresolved placeholders (pass --var KEY=VALUE): " + ", ".join(missing))
    sys.stdout.write(PH.sub(lambda m: v[m.group(1)], text))


def brand_keys_static():
    return ("BRAND_NAME", "BRAND_BLURB", "BRAND_AVOID", "PALETTE_LINE", "ILLUSTRATION_STYLE", "ICON_STYLE",
            "TEXT_RULE", "BRAND_EXTRA", "MAX_RETRIES", "IMAGE_CAP")


if __name__ == "__main__":
    main()
