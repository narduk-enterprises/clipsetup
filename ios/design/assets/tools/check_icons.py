#!/usr/bin/env python3
"""Gate for an image-model-drawn icon pack: check_icons.py --root <pack>  (exit 0 = PASS).

A kept render IS the icon: `kit.py derive` only resizes it, so png/<name>.png (@1x) plus @2x (and @3x for iOS) are the
render at exact sizes, on the ground the model drew. Transparency exists only when the render already had alpha. The
gate checks each PNG exists at its exact size, was derived from the current raw/<name>.png and settings, and is a
faithful resize of it (fidelity.py: a recoloured, knocked-out or trimmed icon fails). An icon is vector only when its
own manifest entry says `"vector": true` (`kit.py keep --vector`, a small monoline currentColor glyph); only a vector
icon needs svg/<name>.svg, which must be trace.py output (currentColor). An svg for any other icon is an error.

Every icon needs raw/<name>.png, and manifest.json must list exactly the kept icons with the exact prompt, the backend
and the drawing tool per icon (provenance.py: grok, sol, astra or cursor; an icon cut from a sheet also names its
sheet, kept under sheets/). Every name in the manifest's optional `required` list must exist, preview.html must show
every icon and README.md must exist.

Raster settings, optional, in manifest.json: "raster": {"platform": "web" (@1x,@2x) | "ios" (@1x,@2x,@3x),
"size": 24 (points), "scales": [1, 2]}. Fields removed 2026-10-07 fail the gate: a pack-wide `vector`, `snap`,
`transparent` (per icon or in `raster`).
"""
from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import struct
import sys
import xml.etree.ElementTree as ET

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import fidelity  # noqa: E402  (ships beside this script, in the skill and in <kit>/tools/)
import provenance  # noqa: E402

NS = '{http://www.w3.org/2000/svg}'
VIEWBOX = '0 0 480 480'
MAX_BYTES = 40000
ALLOWED = {'svg', 'g', 'path', 'title'}
RECORD = '.derived.json'  # {icon name: digest of the render and settings its PNGs came from}
PLATFORM_SCALES = {'web': (1, 2), 'ios': (1, 2, 3)}
DEFAULT_SIZE = 24
KEBAB = re.compile(r'[a-z0-9]+(-[a-z0-9]+)*')
REMOVED = 'removed 2026-10-07'


def png_info(path: pathlib.Path) -> tuple[int, int, bool]:
    """(width, height, has_alpha) from the IHDR chunk; raises ValueError if it is not a PNG."""
    data = path.read_bytes()
    if data[:8] != b'\x89PNG\r\n\x1a\n' or data[12:16] != b'IHDR':
        raise ValueError('not a PNG')
    w, h, _depth, colour = struct.unpack('>IIBB', data[16:26])
    return w, h, colour in (4, 6) or b'tRNS' in data[:4096]


def load_json(p: pathlib.Path) -> dict:
    try:
        data = json.loads(p.read_text())
    except (OSError, ValueError):
        return {}
    return data if isinstance(data, dict) else {}


def is_vector(icon: dict) -> bool:
    """Does this icon ship as a traced currentColor SVG? Only when its own entry says so; raster otherwise."""
    return icon.get('vector') is True


def legacy(man: dict) -> list[str]:
    """Pack fields that were removed 2026-10-07, each as a gate message naming the field."""
    found = []
    if 'vector' in man:
        found.append("manifest.json: `vector` (pack-wide) was removed 2026-10-07: mark single icons with "
                     "`\"vector\": true` on the icon (kit.py keep --vector), never the whole pack")
    if 'transparent' in (man.get('raster') or {}):
        found.append("manifest.json: `raster.transparent` was removed 2026-10-07: icons ship on the ground the model "
                     "drew; transparency exists only if the render has alpha")
    for i in man.get('icons', []):
        for k, why in (('snap', 'palette snapping is gone, a render is never recoloured'),
                       ('transparent', 'the ground is never knocked out; draw the icon on the ground it ships on')):
            if k in i:
                found.append(f"manifest.json: {i.get('name')}: `{k}` was removed 2026-10-07 ({why})")
    return found


def raster_spec(man: dict) -> tuple[int, tuple[int, ...]]:
    """(size in points, scales) the pack's PNGs ship at: web 1x and 2x, or ios 1x, 2x and 3x."""
    r = man.get('raster') or {}
    scales = r.get('scales') or PLATFORM_SCALES.get(r.get('platform', 'web'), PLATFORM_SCALES['web'])
    return int(r.get('size', DEFAULT_SIZE)), tuple(int(s) for s in scales)


def raster_path(name: str, scale: int) -> str:
    return f'png/{name}.png' if scale == 1 else f'png/{name}@{scale}x.png'


def digest(root: pathlib.Path, icon: dict, man: dict) -> str:
    """sha256 of the kept render plus the settings its PNGs depend on, so a re-kept render or an edited size or
    platform is flagged until `kit.py derive` runs again."""
    size, scales = raster_spec(man)
    h = hashlib.sha256((root / 'raw' / f"{icon['name']}.png").read_bytes())
    h.update(json.dumps({'size': size, 'scales': scales}, sort_keys=True).encode())
    return h.hexdigest()


def check_svg(f: pathlib.Path, err) -> None:
    """A vector icon's svg must be trace.py output: currentColor, no style, no hard-coded colour, one viewBox."""
    txt = f.read_text()
    if re.search(r'style=|<style|<script|foreignObject|<image|href=|#[0-9a-fA-F]{3,6}\b|rgb\(', txt):
        err(f.name, 'no style, script, images, links or hard-coded colours')
    try:
        svg = ET.fromstring(txt)
    except ET.ParseError as e:
        err(f.name, f'not well-formed: {e}')
        return
    if svg.tag != NS + 'svg' or svg.attrib.get('viewBox') != VIEWBOX or svg.attrib.get('fill') != 'currentColor':
        err(f.name, f'must be trace.py output: viewBox "{VIEWBOX}", fill="currentColor"')
    if not any(el.tag == NS + 'path' for el in svg.iter()):
        err(f.name, 'empty icon')
    for el in svg.iter():
        if el.tag.replace(NS, '') not in ALLOWED:
            err(f.name, f"element <{el.tag.replace(NS, '')}> not allowed")
    if len(txt) > MAX_BYTES:
        err(f.name, f'{len(txt)} bytes; too detailed for an icon, simplify the render')


def check_raster(root: pathlib.Path, icon: dict, man: dict, record: dict, expected: set, err) -> None:
    """A raster icon ships png/<name>.png (@1x) and its @2x (@3x) at exact sizes, from the current render, as a
    faithful resize of it. Alpha only if the render has alpha."""
    name = icon['name']
    size, scales = raster_spec(man)
    raw = root / 'raw' / f'{name}.png'
    raw_alpha = False
    ref = None
    if raw.exists():
        if record.get(name) != digest(root, icon, man):
            err(f'png/{name}.png', 'not derived from the current raw render and settings; run kit.py derive')
        try:
            rw, rh, raw_alpha = png_info(raw)
            if abs(rw - rh) > max(rw, rh) * 0.01:
                err(f'raw/{name}.png', f'{rw}x{rh}; an icon render must be square (1:1)')
        except ValueError:
            err(f'raw/{name}.png', 'not a PNG')
        try:
            ref = fidelity.Reference(raw)
        except fidelity.NeedsPillow as e:
            err('pillow', str(e))
        except Exception as e:  # unreadable render: the size and alpha checks above already said what they could
            err(f'raw/{name}.png', f'not a readable image: {e}')
    for scale in scales:
        path = raster_path(name, scale)
        expected.add(path)
        p = root / path
        if not p.exists():
            err(path, 'missing; run kit.py derive')
            continue
        try:
            w, h, alpha = png_info(p)
        except ValueError as e:
            err(path, str(e))
            continue
        if (w, h) != (size * scale, size * scale):
            err(path, f'{w}x{h}, want {size * scale}x{size * scale}')
            continue
        if alpha and not raw_alpha:
            err(path, 'has an alpha channel but the render has none: transparency is never manufactured')
        if ref:
            try:
                from PIL import Image
                with Image.open(p) as im:
                    msg = ref.problem(im)
            except Exception as e:
                msg = f'not a readable image: {e}'
            if msg:
                err(path, msg)


def check(root: pathlib.Path) -> tuple[int, list[str]]:
    errs: list[str] = []
    def err(f, m):
        errs.append(f'{f}: {m}')
    man_p = root / 'manifest.json'
    man = load_json(man_p)
    icons = man.get('icons', [])
    entries = {i.get('name'): i for i in icons}
    svgs = {f.stem: f for f in sorted((root / 'svg').glob('*.svg'))}
    raws = {f.stem for f in sorted((root / 'raw').glob('*.png'))}
    names = raws | set(svgs)
    if not names and not entries:
        err('raw/', 'no icons; keep renders with kit.py keep --kind icon (raster is the default)')
    plat = (man.get('raster') or {}).get('platform', 'web')
    if plat not in PLATFORM_SCALES:
        err('manifest.json', f"raster.platform must be one of {', '.join(PLATFORM_SCALES)}")
    errs += legacy(man)
    record = load_json(root / RECORD)
    expected: set[str] = set()
    for name in sorted(names | set(map(str, entries))):
        icon = {**entries.get(name, {}), 'name': name}
        if not KEBAB.fullmatch(name):
            err(name, 'name must be kebab-case')
        if name not in raws:
            err(name, f'no raw/{name}.png: every icon is a kept image-model render')
        if is_vector(icon):
            if name not in svgs:
                err(f'svg/{name}.svg', 'missing; this icon is marked vector: run trace.py')
            else:
                check_svg(svgs[name], err)
        else:
            if name in svgs:
                err(svgs[name].name, 'traced, but this icon is not marked vector: ship the PNGs, or mark this one icon '
                    '`vector: true` (kit.py keep --vector)')
            check_raster(root, icon, man, record, expected, err)
    png_dir = root / 'png'
    if png_dir.exists():
        stray = sorted(f'png/{p.name}' for p in png_dir.iterdir() if p.is_file() and not p.name.startswith('.'))
        stray = [p for p in stray if p not in expected]
        if stray:
            err('png/', 'not produced by derive (remove, or keep the render and derive): ' + ', '.join(stray))

    if not man_p.exists():
        err('manifest.json', 'missing')
    else:
        listed = {i.get('name') for i in icons}
        for i in icons:
            for k in ('name', 'category', 'label', 'keywords'):
                if not i.get(k):
                    err('manifest.json', f"{i.get('name')}: missing {k}")
            for problem in provenance.problems(i):
                err('manifest.json', f"{i.get('name')}: {problem}")
            if i.get('sheet') and not (root / 'sheets' / f"{i['sheet']}.png").exists():
                err('manifest.json', f"{i.get('name')}: sheet {i['sheet']} is not kept in sheets/")
        if listed != names:
            err('manifest.json', f'icons listed {sorted(map(str, listed ^ names))} do not match the kept icons in raw/ and svg/')
        if not set(man.get('categories', [])) >= {i.get('category') for i in icons}:
            err('manifest.json', 'every icon category must be in categories[]')
        missing = set(man.get('required', [])) - names
        if missing:
            err('pack', 'missing required icons: ' + ', '.join(sorted(missing)))
    preview = root / 'preview.html'
    if not preview.exists():
        err('preview.html', 'missing; run preview.py')
    else:
        page = preview.read_text()
        absent = [n for n in sorted(names) if f'>{n}<' not in page]
        if absent:
            err('preview.html', 'stale; missing ' + ', '.join(absent) + ' (run preview.py)')
    if not (root / 'README.md').exists():
        err('README.md', 'missing')
    return len(names), list(dict.fromkeys(errs))


def main(argv=None):
    ap = argparse.ArgumentParser(description='Gate for an image-model-drawn icon pack.')
    ap.add_argument('--root', type=pathlib.Path, default=pathlib.Path.cwd(), help='pack dir (default: cwd)')
    n, errs = check(ap.parse_args(argv).root)
    print(f'{n} icons checked')
    if errs:
        print('\n'.join(errs))
        sys.exit(1)
    print('PASS')


if __name__ == '__main__':
    main()
