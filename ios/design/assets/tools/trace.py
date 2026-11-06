#!/usr/bin/env python3
"""Trace image-model renders into currentColor SVG icons. OPT-IN, per icon: raster is the default, so this only runs
for icons marked vector.

    trace.py --root <pack> [name ...]            traces the marked icons (all of them, or the names given)
    trace.py --root <pack> --vector name ...     marks the named icons vector in manifest.json, then traces them

An icon is marked only per icon, with `"vector": true` on its own manifest entry (`kit.py keep --vector` writes it).
There is no pack-wide or kit-wide switch (removed 2026-10-07). Use it only for a small monoline glyph the product tints
with CSS currentColor. Anything else ships as the PNG resizes `kit.py derive` makes, and naming an unmarked icon here is
refused. Compare the trace with the render at shipping size; if the trace reads worse, ship the PNG.

Reads <pack>/raw/<name>.png (a dark glyph on a light ground) and writes <pack>/svg/<name>.svg. Deterministic; nothing
is drawn in code.

Each render is normalised before tracing so the set reads as one family:
1. optical size: the glyph is scaled so the square root of its bounding-box area is the same for every icon
   (a wide boat no longer looks small next to a tall buoy), capped so its longest side fits the frame;
2. uniform stroke: the glyph is thinned to its centreline, short spurs at line ends are pruned, and the
   centreline is redrawn with one round pen (--stroke-r);
3. clearly solid areas (wider than ~1.7x the render's own stroke) are kept as solids.

Needs ImageMagick 7 (`magick`) and potrace on PATH.
"""
import argparse, json, os, pathlib, re, shutil, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import check_icons  # noqa: E402  (ships beside this script, in the skill and in <kit>/tools/)

SIZE = 480  # trace canvas and SVG viewBox, px


def sh(*a) -> str:
    return subprocess.run([str(x) for x in a], check=True, capture_output=True, text=True).stdout.strip()


def count(png) -> float:  # white pixels in a black-ground mask
    return float(sh('magick', png, '-format', '%[fx:mean*w*h]', 'info:'))


def normalise(png: pathlib.Path, t: pathlib.Path, optical: int, max_side: int, r: int) -> pathlib.Path:
    ink = t / 'ink.png'
    # PNGs from Imagine carry alpha; without flattening it, -extent below turns the whole frame white.
    sh('magick', png, '-background', 'white', '-alpha', 'remove', '-alpha', 'off', '-colorspace', 'Gray',
       '-threshold', '55%', '-trim', '+repage', ink)
    w, h = map(int, sh('magick', ink, '-format', '%w %h', 'info:').split())
    s = min(optical / (w * h) ** 0.5, max_side / max(w, h))
    bin_ = t / 'bin.png'
    sh('magick', ink, '-resize', f'{max(1, round(w * s))}x{max(1, round(h * s))}!', '+repage', '-gravity', 'center',
       '-background', 'white', '-extent', f'{SIZE}x{SIZE}', '-negate', '-threshold', '50%', bin_)
    skel0, skel = t / 'skel0.png', t / 'skel.png'
    sh('magick', bin_, '-morphology', 'Thinning:-1', 'Skeleton', skel0)
    stroke = max(1, round(count(bin_) / max(count(skel0), 1)))  # the render's own line width
    sh('magick', skel0, '-morphology', f'Thinning:{max(r, round(0.9 * stroke))}', 'LineEnds', skel)
    pen, solid, out = t / 'pen.png', t / 'solid.png', t / 'norm.pbm'
    sh('magick', skel, '-morphology', 'Dilate', f'Disk:{r}', pen)
    sh('magick', bin_, '-morphology', 'Open', f'Disk:{max(26, round(1.7 * stroke))}', solid)
    sh('magick', pen, solid, '-compose', 'Lighten', '-composite', '-negate', '-threshold', '50%', out)
    return out


def trace(png: pathlib.Path, args) -> str:
    with tempfile.TemporaryDirectory() as d:
        pbm = normalise(png, pathlib.Path(d), args.optical, args.max_side, args.stroke_r)
        svg = sh('potrace', pbm, '-b', 'svg', '-o', '-', '--flat', '-t', '6', '-a', '1.0', '-O', '0.4',
                 '-W', f'{SIZE}pt', '-H', f'{SIZE}pt')
    paths = re.findall(r'<path d="([^"]+)"', svg)
    m = re.search(r'<g transform="([^"]+)"', svg)
    if not m:
        raise RuntimeError(f'{png.name}: potrace gave no <g transform>; is the render blank?')
    g = m.group(1)
    body = ''.join(f'<path d="{" ".join(d.split())}"/>' for d in paths)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {SIZE} {SIZE}" fill="currentColor">'
            f'<g transform="{g}">{body}</g></svg>\n')


def main(argv=None):
    ap = argparse.ArgumentParser(description='Trace icons marked vector into currentColor SVGs (raster is the default).')
    ap.add_argument('--root', type=pathlib.Path, default=pathlib.Path.cwd(), help='pack dir holding raw/ (default: cwd)')
    ap.add_argument('--optical', type=int, default=330, help='sqrt(w*h) of every glyph, px of 480 (default 330)')
    ap.add_argument('--max-side', type=int, default=430, help='longest-side cap, px of 480 (default 430)')
    ap.add_argument('--stroke-r', type=int, default=14, help='pen radius, px of 480 (default 14 = ~1.4 px at 24 px)')
    ap.add_argument('--jobs', type=int, default=min(6, os.cpu_count() or 1))
    ap.add_argument('--vector', action='store_true', help='mark the named icons vector in manifest.json, then trace them')
    ap.add_argument('names', nargs='*')
    args = ap.parse_args(argv)
    for tool in ('magick', 'potrace'):
        if not shutil.which(tool):
            sys.exit(f'{tool} not found on PATH (brew install imagemagick potrace)')
    raw, out = args.root / 'raw', args.root / 'svg'
    man_p = args.root / 'manifest.json'
    man = check_icons.load_json(man_p)
    if args.vector:
        if not args.names:
            sys.exit('--vector marks the icons you name: trace.py --vector <name> ...')
        listed = {i.get('name'): i for i in man.get('icons', [])}
        unknown = [n for n in args.names if n not in listed]
        if unknown:
            sys.exit(f"{', '.join(unknown)}: not in manifest.json; keep the render first (kit.py keep --kind icon)")
        for n in args.names:
            listed[n]['vector'] = True
        man_p.write_text(json.dumps(man, indent=2, ensure_ascii=False) + '\n')

    def marked(name: str) -> bool:
        entry = next((i for i in man.get('icons', []) if i.get('name') == name), {})
        return check_icons.is_vector(entry)

    if args.names:
        not_marked = [n for n in args.names if not marked(n)]
        if not_marked:
            sys.exit(f"{', '.join(not_marked)}: not marked vector. Raster is the default; ship the PNGs (kit.py derive). "
                     'To trace this one icon anyway: trace.py --vector <name> (marks it vector in manifest.json).')
        pngs = [raw / f'{n}.png' for n in args.names]
    else:
        pngs = [p for p in sorted(raw.glob('*.png')) if marked(p.stem)]
        if not pngs:
            print('no icons are marked vector; nothing to trace (raster is the default: run kit.py derive)')
            return
    out.mkdir(exist_ok=True)

    def one(p: pathlib.Path):
        (out / f'{p.stem}.svg').write_text(trace(p, args))

    with ThreadPoolExecutor(args.jobs) as ex:
        list(ex.map(one, pngs))
    print(f'traced {len(pngs)}')


if __name__ == '__main__':
    main()
