#!/usr/bin/env python3
"""Build an icon pack's contact sheet: preview.py --root <pack>

Shows every icon as-is (no redrawing) at 16, 24 and 40 px, grouped by the manifest's categories, on each theme ground.
A raster icon is its derived png/<name>.png (the largest scale, scaled down, so transparency shows on each ground); a
vector icon inlines its svg/<name>.svg and takes its colour from `color` through currentColor. Themes come from the
manifest's optional "preview": {"themes": [{"key", "label", "bg", "ink"}, ...]}; the default is a light and a dark
ground.
"""
from __future__ import annotations

import argparse
import html
import json
import pathlib

import check_icons

SIZES = (16, 24, 40)
DEFAULT_THEMES = [{'key': 'day', 'label': 'Light', 'bg': '#F4F5F6', 'ink': '#15181B'},
                  {'key': 'night', 'label': 'Dark', 'bg': '#111417', 'ink': '#E9ECEF'}]

CSS = """  :root { color-scheme: light; }
  body { margin: 0; font: 14px/1.4 ui-sans-serif, system-ui, sans-serif; background: #d9dcdf; color: #15181B; }
  header { padding: 28px 32px 8px; }
  h1 { font-size: 22px; font-weight: 600; margin: 0 0 4px; }
  p.lead { margin: 0; max-width: 40rem; }
  .theme { margin: 20px; padding: 24px; border-radius: 12px; }
  .theme h2 { font-size: 13px; letter-spacing: 0.08em; text-transform: uppercase; margin: 28px 0 12px; font-weight: 600; }
  .theme .cat:first-of-type h2 { margin-top: 0; }
  .banner { font-size: 12px; letter-spacing: 0.12em; text-transform: uppercase; margin: 0 0 8px; opacity: 0.7; }
  .grid { display: flex; flex-wrap: wrap; gap: 18px 14px; }
  figure.icon { margin: 0; width: 112px; }
  .sizes { display: flex; align-items: flex-end; gap: 10px; min-height: 40px; }
  .slot { display: inline-flex; color: inherit; }
  .slot svg, .slot img { width: 100%; height: 100%; display: block; }
  figcaption { margin-top: 6px; font-size: 12px; font-variant-numeric: tabular-nums; }"""


def main(argv=None):
    ap = argparse.ArgumentParser(description='Build an icon pack contact sheet.')
    ap.add_argument('--root', type=pathlib.Path, default=pathlib.Path.cwd(), help='pack dir (default: cwd)')
    root = ap.parse_args(argv).root
    man = json.loads((root / 'manifest.json').read_text())
    THEMES = [(t['key'], t['label'], t['bg'], t['ink']) for t in man.get('preview', {}).get('themes', DEFAULT_THEMES)]
    svgs = {p.stem: p.read_text().strip() for p in sorted((root / 'svg').glob('*.svg'))}
    _, scales = check_icons.raster_spec(man)
    pngs = {p.stem for p in (root / 'png').glob('*.png') if '@' not in p.stem} if (root / 'png').is_dir() else set()
    shown = set(svgs) | pngs
    big = check_icons.raster_path('{n}', max(scales))
    title = html.escape(man.get('name', 'Icons'))
    css = CSS + ''.join(f'\n  .theme.{k} {{ background: {bg}; color: {ink}; }}' for k, _, bg, ink in THEMES)
    out = ['<!DOCTYPE html>', '<html lang="en">', '<head>', '<meta charset="utf-8">', f'<title>{title}</title>',
           f'<style>\n{css}\n</style>', '</head>', '<body>', '<header>', f'  <h1>{title}</h1>',
           f'  <p class="lead">{len(shown)} icons, each shown at {", ".join(map(str, SIZES[:-1]))} and {SIZES[-1]} px. '
           'Vector icons take their colour from <code>color</code> through <code>currentColor</code>; raster icons keep their own.</p>', '</header>']
    for key, label, bg, ink in THEMES:
        out += [f'<div class="theme {key}">', f'  <p class="banner">{label} · {bg} / {ink}</p>']
        for cat in man['categories']:
            icons = [i for i in man['icons'] if i['category'] == cat and i['name'] in shown]
            if not icons:
                continue
            out.append(f'  <section class="cat"><h2>{html.escape(cat)}</h2><div class="grid">')
            for i in icons:
                def glyph(n):
                    if n in svgs:
                        return svgs[n]
                    return f'<img src="{big.format(n=n)}" alt="" width="100%" height="100%">'
                slots = ''.join(f'<span class="slot s{s}" style="width:{s}px;height:{s}px">{glyph(i["name"])}</span>'
                                for s in SIZES)
                out.append(f'<figure class="icon" title="{html.escape(i.get("label", ""))}"><div class="sizes">{slots}'
                           f'</div><figcaption>{i["name"]}</figcaption></figure>')
            out.append('</div></section>')
        out.append('</div>')
    out += ['</body>', '</html>', '']
    (root / 'preview.html').write_text('\n'.join(out))
    print(f'preview.html: {len(shown)} icons')


if __name__ == '__main__':
    main()
