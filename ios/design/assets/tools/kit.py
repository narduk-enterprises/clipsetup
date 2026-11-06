#!/usr/bin/env python3
"""Set up, keep, derive and gate a product asset kit drawn by an image model (grok, sol, astra or cursor).

    kit.py init   --root <kit> --name NAME [--platforms web,macos,ios,tvos]   kit.json, .gitignore, tools/
    kit.py keep   --root <kit> <render> <name> --kind KIND [--backend grok|sol|astra|cursor] [--tool T] [--model M]
                  (--prompt TEXT | --prompt-file FILE | --prompt-index <raw/index.md>) [--from NAME] [--round N]
                  [--category C --label L --keywords a,b] [--vector]  (--kind icon only; opts that one icon into tracing)
    kit.py derive --root <kit> [name ...]
    kit.py check  --root <kit>                                      (exit 0 = PASS)

The contract: a kept image-model render IS the deliverable. Nothing here draws, and nothing alters a render's pixels.

`keep` converts one kept render (a JPEG from grok, a PNG from Codex) into raw/<name>.png, or icons/raw/<name>.png for
--kind icon, and records the exact prompt, the backend and the tool in the manifest. KIND is app-icon, og, hero,
illustration or icon, plus two optional siblings of the app icon, each its own kept render: `app-icon-maskable` (the
same symbol inside the centre 60%) and `favicon` (a simpler glyph drawn for 16 px).

`derive` only resizes (Lanczos) and crops or fits to the target aspect:
  app-icon      out/app-icon-1024.png, apple-touch-icon.png (180), icon-192.png, icon-512.png, site.webmanifest,
                head-snippet.html, and favicon.ico (16, 32 and 48 px resizes, packed; no rounding) unless a favicon
                asset is kept, in which case favicon.ico comes from it. With `macos` or `ios` (iPhone and iPad) in
                kit.json `platforms` it also writes out/AppIcon.icon (icon.json and Assets/app-icon.png, the render at
                1024): a one-layer Icon Composer bundle that actool shapes for each OS, with flat fallbacks for older
                releases
  tvos-icon-back, tvos-icon-middle (optional), tvos-icon-front, top-shelf   (`tvos` in platforms) each layer of the
                tvOS parallax icon and the Top Shelf art, cropped to 5:3 or 8:3 and resized into
                out/AppIcon.brandassets (App Icon 400x240 @1x/@2x, App Store 1280x768, Top Shelf 1920x720 and 2320x720)
  app-icon-maskable   out/icon-mask.png (512) and the manifest's maskable entry; omitted when not kept
  hero, illustration  out/<name>-w{1920,960,480}.png, every width not larger than the render (never upscaled); a dark
                version is its own kept asset, never derived
  og            out/og.png, the art cropped to 1200x630; with `title` the title is then TYPESET over it (layout with a
                real font, not drawing), see below
  icon          icons/png/<name>.png, @2x (and @3x for platform ios), the render resized, on the ground it was drawn on
No palette snapping, no knocked-out ground, no margin trimming, no colour-swapped twins, no rounded corners, no SVG
logo. Transparency exists only when the render already has alpha. `palette` and `brand` in kit.json feed prompts and
briefs only. An icon is traced to SVG only when that one icon is kept with --vector (a small monoline currentColor
glyph; trace.py): `"vector": true` on its entry in icons/manifest.json, never kit-wide or pack-wide.

OG text, optional fields on the `og` asset in kit.json: `title` (no title: the card ships as art only), `subtitle`,
`font` (a font file path, or a fontconfig name `fc-match` knows; default: a bold sans that exists on the machine, and
derive fails clearly when none is found), `text_color` (#RRGGBB, default #FFFFFF), `text_position` (left | center |
bottom-left, default bottom-left). Margin 6% of the width; no boxes, no scrims. The text settings are part of the
derive digest, so changing the title makes the output stale until derive runs again.

`check` is the gate. Every required asset is kept with its exact prompt, backend and tool (and model for Codex); every
output PNG is at its exact size, derived from the current kept render and settings (digest), and a faithful resize of
it (fidelity.py: the render resized again with Pillow must match the output within a mean absolute difference of 0.5
level, which proves nothing was recoloured; a titled OG card is exempt because the title is typeset over the art, and
is checked for size, digest and opacity only); no SVG exists except icons marked vector; and no field removed on 2026-10-07 is left in
kit.json (`logo`, `snap`, `snap_tol`, `transparent` and `dark` variants, a kit- or pack-wide `vector`, kind `mark`).

keep and derive need ImageMagick 7 (`magick`); check and the gate need pillow (a pixel comparison, no numpy).
potrace and librsvg are needed only for an icon kept with --vector.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import struct
import subprocess
import sys
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import check_icons  # noqa: E402  (ships beside this script, in the skill and in <kit>/tools/)
import fidelity  # noqa: E402
import provenance  # noqa: E402

KINDS = ('app-icon', 'app-icon-maskable', 'favicon', 'og', 'hero', 'illustration', 'tvos-icon-back', 'tvos-icon-middle',
         'tvos-icon-front', 'top-shelf')
SQUARE = {'app-icon', 'app-icon-maskable', 'favicon'}
SINGLE = SQUARE | {'og', 'top-shelf', 'tvos-icon-back', 'tvos-icon-middle', 'tvos-icon-front'}  # their outputs have fixed names, so a kit has at most one of each
HERO_WIDTHS = (1920, 960, 480)
ICO_SIZES = (16, 32, 48)
PLATFORMS = ('web', 'macos', 'ios', 'tvos')  # ios covers iPhone and iPad
ICON_COMPOSER = {'macos', 'ios'}  # one AppIcon.icon serves both; tvOS has no Icon Composer route (actool 27)
APPLE_ICON = 'out/AppIcon.icon'  # apple_icon.py compiles it into an app; actool applies the shape, nothing here does
TV = 'out/AppIcon.brandassets'
TV_LAYERS = {'tvos-icon-back': 'Back', 'tvos-icon-middle': 'Middle', 'tvos-icon-front': 'Front'}
TV_STACKS = {'App Icon': {'1x': (400, 240), '2x': (800, 480)}, 'App Icon - App Store': {'1x': (1280, 768)}}
TV_SHELF = {'Top Shelf Image': ('top-shelf', (1920, 720)), 'Top Shelf Image Wide': ('top-shelf-wide', (2320, 720))}
TV_REQUIRED = ('tvos-icon-back', 'tvos-icon-front', 'top-shelf')
XC_INFO = {'author': 'xcode', 'version': 1}
OG = (1200, 630)
OG_POSITIONS = ('left', 'center', 'bottom-left')
OG_FIELDS = ('title', 'subtitle', 'font', 'text_color', 'text_position')
RECORD = 'out/.derived.json'  # {asset name: digest of everything its outputs came from}
KEBAB = re.compile(r'[a-z0-9]+(-[a-z0-9]+)*')
HEX = re.compile(r'#[0-9a-fA-F]{6}')
GITIGNORE = ('.lane/', '.venv/', '__pycache__/')
REMOVED = 'removed 2026-10-07'
DEFAULT_FONTS = ('/System/Library/Fonts/Supplemental/Arial Bold.ttf', '/Library/Fonts/Arial Bold.ttf',
                 '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf', '/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf',
                 '/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf',
                 '/usr/share/fonts/liberation/LiberationSans-Bold.ttf')


def kinds_of(man: dict) -> set:
    return {x.get('kind') for x in man.get('assets', [])}


def platforms_of(man: dict) -> set:
    return set(man.get('platforms') or ['web'])


def titled(asset: dict) -> bool:
    return asset.get('kind') == 'og' and bool(str(asset.get('title') or '').strip())


def plan(asset: dict, raw_wh: tuple[int, int], kinds=(), platforms=()) -> list[dict]:
    """The derived files one kept render must produce. Shared by derive and check. `kinds` are the kinds in the kit:
    a kept `favicon` takes favicon.ico away from the app icon. `platforms` holding macos or ios adds the AppIcon.icon.
    The tvOS back layer also owns the brand-asset and stack Contents.json files."""
    kind, name = asset['kind'], asset['name']

    def sq(p, s):
        return {'path': p, 'size': (s, s), 'opaque': True}
    if kind == 'app-icon':
        outs = [sq('out/app-icon-1024.png', 1024), sq('out/apple-touch-icon.png', 180),
                sq('out/icon-192.png', 192), sq('out/icon-512.png', 512)]
        if 'favicon' not in kinds:
            outs.append({'path': 'out/favicon.ico', 'ico': ICO_SIZES})
        if ICON_COMPOSER & set(platforms):
            outs += [sq(f'{APPLE_ICON}/Assets/app-icon.png', 1024), {'path': f'{APPLE_ICON}/icon.json', 'text': 'json'}]
        return outs + [{'path': 'out/site.webmanifest', 'text': 'json'}, {'path': 'out/head-snippet.html', 'text': 'html'}]
    if kind == 'app-icon-maskable':
        return [sq('out/icon-mask.png', 512)]
    if kind == 'favicon':
        return [{'path': 'out/favicon.ico', 'ico': ICO_SIZES}]
    if kind == 'og':
        return [{'path': 'out/og.png', 'size': OG, 'opaque': True, 'cover': True}]
    if kind in TV_LAYERS:
        layer = TV_LAYERS[kind]
        outs = []
        for stack, scales in TV_STACKS.items():
            d = f'{TV}/{stack}.imagestack/{layer}.imagestacklayer'
            for scale, size in scales.items():
                fn = layer.lower() + ('' if scale == '1x' else f'@{scale}') + '.png'
                outs.append({'path': f'{d}/Content.imageset/{fn}', 'size': size, 'cover': True,
                             'opaque': kind == 'tvos-icon-back'})  # upper layers keep the render's own alpha
            outs += [{'path': f'{d}/Contents.json', 'text': 'json'},
                     {'path': f'{d}/Content.imageset/Contents.json', 'text': 'json'}]
        if kind == 'tvos-icon-back':
            outs += [{'path': f'{TV}/Contents.json', 'text': 'json'}]
            outs += [{'path': f'{TV}/{stack}.imagestack/Contents.json', 'text': 'json'} for stack in TV_STACKS]
        return outs
    if kind == 'top-shelf':
        outs = []
        for name_, (fn, size) in TV_SHELF.items():
            outs += [{'path': f'{TV}/{name_}.imageset/{fn}.png', 'size': size, 'opaque': True, 'cover': True},
                     {'path': f'{TV}/{name_}.imageset/Contents.json', 'text': 'json'}]
        return outs
    w, h = raw_wh  # hero, illustration
    widths = [x for x in HERO_WIDTHS if x <= w] or [w]  # never upscale a hero
    return [{'path': f'out/{name}-w{x}.png', 'size': (x, round(h * x / w))} for x in widths]


# --- pure-Python readers, so the gate needs no image tooling -----------------------------------------------

png_info = check_icons.png_info  # (width, height, has_alpha) from the IHDR chunk; ValueError if not a PNG


def ico_sizes(path: pathlib.Path) -> list[int]:
    data = path.read_bytes()
    reserved, kind, count = struct.unpack('<HHH', data[:6])
    if reserved != 0 or kind != 1:
        raise ValueError('not an ICO')
    return sorted((data[6 + 16 * i] or 256) for i in range(count))


def legacy(man: dict) -> list[tuple[str, str]]:
    """(where, message) for each kit.json field removed 2026-10-07, so an old kit fails loudly instead of drifting."""
    found = []
    top = {'logo': 'no SVG logos: the kept render is the logo, resized only',
           'snap': 'palette snapping is gone: a render is never recoloured',
           'snap_tol': 'palette snapping is gone: a render is never recoloured',
           'vector': 'a kit-wide vector switch is gone: mark single icons with kit.py keep --vector'}
    for k, why in top.items():
        if k in man:
            found.append(('kit.json', f'`{k}` was {REMOVED} ({why}); delete it'))
    per_asset = {'snap': 'palette snapping is gone: a render is never recoloured',
                 'transparent': 'the ground is never knocked out: draw it on the ground it ships on, or keep a '
                                'render that has its own alpha',
                 'dark': 'dark twins are not derived: keep the dark version as its own render and its own asset'}
    for x in man.get('assets', []):
        n = x.get('name') or '?'
        for k, why in per_asset.items():
            if k in x:
                found.append(('kit.json', f'{n}: `{k}` was {REMOVED} ({why}); delete it'))
        if x.get('kind') == 'mark':
            found.append(('kit.json', f'{n}: kind `mark` was {REMOVED} (no SVG logos, no mark/wordmark outputs); '
                          'delete the asset'))
    return found


# --- keep ---------------------------------------------------------------------------------------------------

def load(p: pathlib.Path, default: dict) -> dict:
    return json.loads(p.read_text()) if p.exists() else default


def save(p: pathlib.Path, data: dict) -> None:
    p.write_text(json.dumps(data, indent=2, ensure_ascii=False) + '\n')


def record(root: pathlib.Path, name: str, kind: str, entry: dict, icon_meta: dict | None = None) -> pathlib.Path:
    """Upsert the manifest entry for a kept render; return the manifest path."""
    if kind == 'icon':
        man_p = root / 'icons' / 'manifest.json'
        man = load(man_p, {'name': f"{load(root / 'kit.json', {}).get('name', 'Product')} icons",
                           'categories': [], 'required': [], 'icons': []})
        old = next((i for i in man['icons'] if i.get('name') == name and 'vector' in i), {})
        entry = {'name': name, **icon_meta, **({'vector': old['vector']} if old else {}), **entry}  # re-keeping keeps the flag
        if entry['category'] not in man['categories']:
            man['categories'].append(entry['category'])
        items = man['icons']
    else:
        man_p = root / 'kit.json'
        man = load(man_p, {'name': root.resolve().name, 'required': [], 'assets': []})
        entry = {'name': name, 'kind': kind, **entry}
        items = man['assets']
    items[:] = [i for i in items if i.get('name') != name] + [entry]
    man_p.parent.mkdir(parents=True, exist_ok=True)
    save(man_p, man)
    return man_p


def parse_index_prompts(index_md: pathlib.Path) -> dict[str, str]:
    """filename -> the longest cell of that row in a Codex run's raw/index.md (that cell is the exact prompt)."""
    prompts: dict[str, str] = {}
    for line in index_md.read_text(errors='replace').splitlines():
        if not line.startswith('|'):
            continue
        cells = [c.strip() for c in line.strip().strip('|').split('|')]
        m = re.match(r'(?:\*\*)?`?([\w.\-]+\.png)', cells[0]) if cells else None
        if m and len(cells) >= 3:
            prompts[m.group(1)] = max(cells[1:], key=len).replace('<br>', '\n')
    return prompts


drawing_entry = provenance.drawing_entry  # retain the copied tool's public helper


def keep(a) -> None:
    if not KEBAB.fullmatch(a.name):
        sys.exit(f'{a.name}: name must be kebab-case')
    if a.kind == 'icon' and not (a.category and a.label):
        sys.exit('--kind icon needs --category and --label')
    if a.prompt_index:
        found = parse_index_prompts(a.prompt_index).get(a.render.name)
        if not found:
            sys.exit(f'{a.render.name}: no row in {a.prompt_index} (its first column names the file)')
        prompt = found.strip()
    else:
        prompt = (a.prompt_file.read_text() if a.prompt_file else a.prompt or '').strip()
    if not prompt:
        sys.exit('the exact prompt of the kept render is required (--prompt, --prompt-file or --prompt-index)')
    if a.vector and a.kind != 'icon':
        sys.exit('--vector is for --kind icon (one small monoline currentColor glyph, traced); logos, app icons and '
                 'illustrations ship as the PNG they were drawn as')
    entry = drawing_entry(a.backend, a.tool, a.model, prompt, a.round, a.from_)
    if a.vector:
        entry['vector'] = True
    if entry['tool'] == 'image_edit' and not a.from_:
        sys.exit('--tool image_edit needs --from: the render it edited')
    bad = provenance.problems(entry)
    if bad:
        sys.exit(f"--backend {a.backend} --tool {entry['tool']}: {'; '.join(bad)}")
    if not a.render.is_file():
        sys.exit(f'{a.render}: render not found')
    need_magick()
    dest = a.root / ('icons/raw' if a.kind == 'icon' else 'raw') / f'{a.name}.png'
    dest.parent.mkdir(parents=True, exist_ok=True)
    try:
        sh('magick', a.render, '-auto-orient', '-strip', f'PNG:{dest}')
    except subprocess.CalledProcessError as e:
        sys.exit(f'{a.render}: magick could not convert it: {e.stderr.strip()}')
    meta = {'category': a.category, 'label': a.label,
            'keywords': [k.strip() for k in (a.keywords or a.name).split(',') if k.strip()]}
    man = record(a.root, a.name, a.kind, entry, meta if a.kind == 'icon' else None)
    print(f'kept {dest.relative_to(a.root)}; prompt and backend recorded in {man.relative_to(a.root)}')


def init(a) -> None:
    """kit.json (when absent), .gitignore, and tools/: a copy of these scripts so the kit rebuilds without the repo."""
    a.root.mkdir(parents=True, exist_ok=True)
    kit_p = a.root / 'kit.json'
    if not kit_p.exists():
        man = {'name': a.name, 'required': [], 'assets': []}
        plats = [x.strip() for x in a.platforms.split(',') if x.strip()]
        if plats != ['web']:  # an Apple app ships its icon from the kit too (out/AppIcon.icon, out/AppIcon.brandassets)
            man['platforms'] = plats
            req = ['app-icon'] if ICON_COMPOSER & set(plats) else []
            man['required'] = req + (list(TV_REQUIRED) if 'tvos' in plats else [])
        save(kit_p, man)
    gi = a.root / '.gitignore'
    have = gi.read_text().splitlines() if gi.exists() else []
    gi.write_text('\n'.join(have + [g for g in GITIGNORE if g not in have]) + '\n')
    tools = a.root / 'tools'
    shutil.copytree(HERE, tools, dirs_exist_ok=True, ignore=shutil.ignore_patterns('__pycache__'))
    print(f'kit ready at {a.root}; edit kit.json (name, required, palette, brand), then draw and keep')


# --- derive -------------------------------------------------------------------------------------------------

def sh(*args) -> str:
    return subprocess.run([str(x) for x in args], check=True, capture_output=True, text=True).stdout.strip()


def need_magick() -> None:
    if not shutil.which('magick'):
        sys.exit('magick not found on PATH (brew install imagemagick)')


def fc_match(name: str) -> str | None:
    """The font file fontconfig has for `name`, only if it really is that font (fc-match always answers something)."""
    if not shutil.which('fc-match'):
        return None
    norm = lambda t: re.sub(r'[^a-z0-9]', '', t.lower())  # noqa: E731
    want = norm(name.split(':')[0])
    for pattern in (name, name.replace('-', ':style=', 1)):
        try:
            out = sh('fc-match', '-f', '%{file}|%{family}|%{fullname}|%{postscriptname}', pattern)
        except subprocess.CalledProcessError:
            continue
        file, _, names = out.partition('|')
        if file and want and want in norm(names):
            return file
    return None


def resolve_font(spec: str | None) -> str:
    """The font file to typeset the OG title with: `font` as a path, else as a fontconfig name, else a default bold
    sans (macOS Arial Bold, then DejaVu or Liberation). Fails clearly when none exists."""
    if spec:
        p = pathlib.Path(spec).expanduser()
        if p.is_file():
            return str(p)
        found = fc_match(spec)
        if found:
            return found
        sys.exit(f'og font {spec!r}: not a font file and not a font name fontconfig (fc-match) knows; '
                 'give a path to a .ttf, .otf or .ttc')
    for candidate in DEFAULT_FONTS:
        if pathlib.Path(candidate).is_file():
            return candidate
    sys.exit('no default sans font found for the OG title (looked for Arial Bold, DejaVu Sans Bold, Liberation Sans '
             'Bold); set `font` on the og asset to a font file')


def font_identity(spec: str | None) -> dict:
    """What identifies the font in the digest: the setting, and the file's bytes when it is a path."""
    if not spec:
        return {'font': 'default'}
    p = pathlib.Path(spec).expanduser()
    return {'font': spec, 'font_sha256': hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else None}


def inputs(asset: dict, man: dict) -> dict:
    """The settings, besides the render itself, that change what derive writes for this asset."""
    cfg: dict = {}
    if asset['kind'] == 'app-icon':
        kinds = kinds_of(man)
        cfg.update(theme_color=man.get('theme_color'), name=man.get('name'),
                   maskable='app-icon-maskable' in kinds, favicon='favicon' in kinds)
        if ICON_COMPOSER & platforms_of(man):
            cfg['icon_composer'] = True
    if asset['kind'] == 'tvos-icon-back':  # it writes the stack and brand JSON, which list what else is kept
        cfg['tv_kinds'] = sorted(kinds_of(man) & (set(TV_LAYERS) | {'top-shelf'}))
    if titled(asset):
        cfg.update({k: asset.get(k) for k in ('title', 'subtitle', 'text_color', 'text_position')})
        cfg.update(font_identity(asset.get('font')))
    return cfg


def digest(root: pathlib.Path, asset: dict, man: dict) -> str:
    """sha256 of the kept render plus the settings its outputs depend on, so a re-kept render or an edited theme
    colour or OG title is flagged until derive runs again."""
    h = hashlib.sha256((root / 'raw' / f"{asset['name']}.png").read_bytes())
    cfg = inputs(asset, man)
    if cfg:
        h.update(json.dumps(cfg, sort_keys=True).encode())
    return h.hexdigest()


def resize_png(src: pathlib.Path, dest: pathlib.Path, w: int, h: int, *, cover: bool, opaque: bool,
               alpha: bool) -> None:
    """The only pixel operation: a Lanczos resize to exactly w x h, cropping the centre to the aspect when `cover`.
    A render already at the target size is copied through unresized, so its pixels stay identical. `opaque` flattens a
    render that has alpha onto white (a no-op for the usual opaque render); otherwise alpha is kept exactly when the
    render has it (the crop pads with transparent, never white, so alpha corners survive)."""
    cmd = ['magick', src]
    if opaque:
        cmd += ['-background', 'white', '-alpha', 'remove', '-alpha', 'off']
    else:
        cmd += ['-background', 'none']
    if png_info(src)[:2] != (w, h):
        cmd += ['-filter', 'Lanczos']
        cmd += ['-resize', f'{w}x{h}^', '-gravity', 'center', '-extent', f'{w}x{h}'] if cover else ['-resize', f'{w}x{h}!']
    sh(*cmd, '-depth', '8', f"{'PNG32' if alpha and not opaque else 'PNG24'}:{dest}")


def make_ico(src: pathlib.Path, dest: pathlib.Path, sizes, alpha: bool, tmp: pathlib.Path) -> None:
    """favicon.ico: one Lanczos resize of the render per size, packed side by side. Corners stay square."""
    parts = []
    for s in sizes:
        part = tmp / f'ico-{s}.png'
        resize_png(src, part, s, s, cover=True, opaque=False, alpha=alpha)
        parts.append(part)
    sh('magick', *parts, dest)


def typeset(art: pathlib.Path, dest: pathlib.Path, asset: dict, tmp: pathlib.Path) -> None:
    """Typeset the OG title (and subtitle) over the art with a real font. Layout, not drawing: no shapes, boxes or
    scrims; the art outside the glyphs is untouched."""
    width, height = OG
    margin = round(width * 0.06)
    pos = asset.get('text_position') or 'bottom-left'
    colour = asset.get('text_color') or '#FFFFFF'
    font = resolve_font(asset.get('font'))
    box = width - 2 * margin if pos == 'center' else round(width * 0.62)
    gravity = 'center' if pos == 'center' else 'west'
    layers = []
    for key, size in (('title', round(width * 0.065)), ('subtitle', round(width * 0.032))):
        text = str(asset.get(key) or '').strip()
        if not text:
            continue
        f = tmp / f'{key}.txt'
        f.write_text(text)
        layers.append((size, f))
    cmd: list = ['-background', 'none', '-fill', colour, '-font', font, '-gravity', gravity]
    for i, (size, f) in enumerate(layers):
        if i:
            cmd += ['-size', f'{box}x{round(width * 0.014)}', 'xc:none']
        cmd += ['-size', f'{box}x', '-pointsize', str(size), f'caption:@{f}']
    cmd += ['-background', 'none', '-append']
    layer = tmp / 'text-layer.png'
    sh('magick', *cmd, f'PNG32:{layer}')
    lh = int(sh('magick', 'identify', '-format', '%h', layer))
    if lh > height - 2 * margin:
        sys.exit(f"og {asset['name']}: the title does not fit at this size ({lh}px tall, {height - 2 * margin}px "
                 'available); shorten the title or subtitle')
    place = {'left': ['West', f'+{margin}+0'], 'center': ['Center', '+0+0'],
             'bottom-left': ['SouthWest', f'+{margin}+{margin}']}[pos]
    sh('magick', art, layer, '-gravity', place[0], '-geometry', place[1], '-compose', 'over', '-composite',
       f'PNG24:{dest}')


def theme_colour(src: pathlib.Path, man: dict) -> str:
    return man.get('theme_color') or '#' + sh('magick', src, '-depth', '8', '-format', '%[hex:u.p{2,2}]', 'info:')[:6].upper()


def write_web_wiring(root: pathlib.Path, man: dict, src: pathlib.Path) -> None:
    colour = theme_colour(src, man)
    name = man.get('name', 'App')
    icons = [{'src': '/icon-192.png', 'sizes': '192x192', 'type': 'image/png', 'purpose': 'any'},
             {'src': '/icon-512.png', 'sizes': '512x512', 'type': 'image/png', 'purpose': 'any'}]
    if 'app-icon-maskable' in kinds_of(man):
        icons.append({'src': '/icon-mask.png', 'sizes': '512x512', 'type': 'image/png', 'purpose': 'maskable'})
    (root / 'out/site.webmanifest').write_text(json.dumps(
        {'name': name, 'short_name': name, 'theme_color': colour, 'background_color': colour,
         'display': 'standalone', 'icons': icons}, indent=2) + '\n')
    (root / 'out/head-snippet.html').write_text(
        '<link rel="icon" href="/favicon.ico" sizes="48x48">\n'
        '<link rel="apple-touch-icon" href="/apple-touch-icon.png">\n'
        '<link rel="manifest" href="/site.webmanifest">\n'
        f'<meta name="theme-color" content="{colour}">\n')


def write_icon_composer(root: pathlib.Path, man: dict, src: pathlib.Path) -> None:
    """icon.json for a one-layer Icon Composer bundle: the full-bleed render is the only layer, and actool (Xcode 26
    or newer) draws each OS's shape (the macOS squircle with margin and shadow, the iOS mask), including the flat
    fallbacks for older releases (.icns for macOS 15, AppIcon60x60/76x76 PNGs for iOS 17). The fill
    is the theme colour, hidden under the opaque layer."""
    hex_ = theme_colour(src, man)
    rgb = [int(hex_[i:i + 2], 16) / 255 for i in (1, 3, 5)]
    (root / APPLE_ICON / 'icon.json').write_text(json.dumps({
        'fill': {'solid': 'srgb:' + ','.join(f'{c:.5f}' for c in rgb) + ',1.00000'},
        'groups': [{'layers': [{'image-name': 'app-icon.png', 'name': 'app-icon'}]}],
        'supported-platforms': {'squares': 'shared'}}, indent=2) + '\n')


def write_tv_json(root: pathlib.Path, asset: dict, man: dict) -> None:
    """The Contents.json files Xcode reads for the tvOS brand assets (the layout sound-tv#3 shipped)."""
    def put(rel, obj):
        p = root / TV / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(json.dumps(obj, indent=2) + '\n')
    kind = asset['kind']
    if kind == 'top-shelf':
        for name_, (fn, _) in TV_SHELF.items():
            put(f'{name_}.imageset/Contents.json',
                {'images': [{'idiom': 'tv', 'filename': f'{fn}.png', 'scale': '1x'}], 'info': XC_INFO})
        return
    layer = TV_LAYERS[kind]
    for stack, scales in TV_STACKS.items():
        d = f'{stack}.imagestack/{layer}.imagestacklayer'
        put(f'{d}/Contents.json', {'info': XC_INFO})
        put(f'{d}/Content.imageset/Contents.json', {'images': [
            {'idiom': 'tv', 'filename': layer.lower() + ('' if sc == '1x' else f'@{sc}') + '.png', 'scale': sc}
            for sc in scales], 'info': XC_INFO})
    if kind != 'tvos-icon-back':
        return
    kinds = kinds_of(man)
    layers = [v for k, v in TV_LAYERS.items() if k in kinds][::-1]  # front first, as Xcode lists them
    for stack in TV_STACKS:
        put(f'{stack}.imagestack/Contents.json',
            {'layers': [{'filename': f'{n}.imagestacklayer'} for n in layers], 'info': XC_INFO})
    assets = [{'filename': 'App Icon - App Store.imagestack', 'idiom': 'tv', 'role': 'primary-app-icon',
               'size': '1280x768'},
              {'filename': 'App Icon.imagestack', 'idiom': 'tv', 'role': 'primary-app-icon', 'size': '400x240'}]
    if 'top-shelf' in kinds:
        assets += [{'filename': 'Top Shelf Image Wide.imageset', 'idiom': 'tv', 'role': 'top-shelf-image-wide',
                    'size': '2320x720'},
                   {'filename': 'Top Shelf Image.imageset', 'idiom': 'tv', 'role': 'top-shelf-image',
                    'size': '1920x720'}]
    put('Contents.json', {'assets': assets, 'info': XC_INFO})


def derive_one(root: pathlib.Path, asset: dict, man: dict) -> list[str]:
    raw = root / 'raw' / f"{asset['name']}.png"
    w, h, alpha = png_info(raw)
    notes: list[str] = []
    with tempfile.TemporaryDirectory() as t:
        tmp = pathlib.Path(t)
        for out in plan(asset, (w, h), kinds_of(man), platforms_of(man)):
            dest = root / out['path']
            dest.parent.mkdir(parents=True, exist_ok=True)
            if 'text' in out:
                continue  # written once below, from the app icon's corner colour
            if 'ico' in out:
                make_ico(raw, dest, out['ico'], alpha, tmp)
                continue
            tw, th = out['size']
            if tw > w or th > h:
                notes.append(f"{out['path']}: upscaled from {w}x{h}; render larger or accept the softness")
            target = tmp / 'art.png' if titled(asset) else dest
            resize_png(raw, target, tw, th, cover=out.get('cover', False) or asset['kind'] in SQUARE,
                       opaque=bool(out.get('opaque')), alpha=alpha)
            if titled(asset):
                typeset(target, dest, asset, tmp)
        if asset['kind'] == 'app-icon':
            write_web_wiring(root, man, raw)
            if ICON_COMPOSER & platforms_of(man):
                write_icon_composer(root, man, raw)
        if asset['kind'] in TV_LAYERS or asset['kind'] == 'top-shelf':
            write_tv_json(root, asset, man)
    return notes


def derive_icons(root: pathlib.Path, man: dict, names: list[str]) -> int:
    """Raster icons: each kept icons/raw/<name>.png becomes PNGs at the pack's size and scales (icons/png/<name>.png,
    @2x, @3x): a Lanczos resize of the render on the ground it was drawn on, alpha only if the render has it. Icons
    marked vector are left to trace.py."""
    pack_p = root / 'icons' / 'manifest.json'
    if not pack_p.exists():
        return 0
    pack = json.loads(pack_p.read_text())
    size, scales = check_icons.raster_spec(pack)
    icons_dir = root / 'icons'
    rec_p = icons_dir / check_icons.RECORD
    rec = load(rec_p, {})
    done = 0
    for icon in pack.get('icons', []):
        name = icon['name']
        if (names and name not in names) or check_icons.is_vector(icon):
            continue
        raw = icons_dir / 'raw' / f'{name}.png'
        if not raw.is_file():
            sys.exit(f'icons/raw/{name}.png: missing; keep the render with kit.py keep --kind icon')
        _, _, alpha = png_info(raw)
        for scale in scales:
            dest = icons_dir / check_icons.raster_path(name, scale)
            dest.parent.mkdir(parents=True, exist_ok=True)
            resize_png(raw, dest, size * scale, size * scale, cover=True, opaque=False, alpha=alpha)
        rec[name] = check_icons.digest(icons_dir, {**icon, 'name': name}, pack)
        done += 1
    rec = {k: v for k, v in rec.items() if k in {i['name'] for i in pack.get('icons', [])}}
    if done or rec_p.exists():
        save(rec_p, dict(sorted(rec.items())))
    return done


def derive(a) -> None:
    need_magick()
    man = load(a.root / 'kit.json', {})  # an icons-only kit has no kit.json until it has assets
    refused = legacy(man)
    if refused:
        sys.exit('\n'.join(f'{where}: {msg}' for where, msg in refused))
    names = set(a.names)
    if names & {'app-icon-maskable', 'favicon'} or any(
            x['name'] in names and x['kind'] in ('app-icon-maskable', 'favicon') for x in man.get('assets', [])):
        names |= {x['name'] for x in man.get('assets', []) if x['kind'] == 'app-icon'}  # its manifest and ico follow
    assets = [x for x in man.get('assets', []) if not names or x['name'] in names]
    rec_p = a.root / RECORD
    known = {x['name'] for x in man.get('assets', [])}
    rec = {k: v for k, v in load(rec_p, {}).items() if k in known}
    for x in assets:
        if x['kind'] not in KINDS:
            sys.exit(f"{x['name']}: kind {x['kind']!r} is not one of {', '.join(KINDS)}")
        for n in derive_one(a.root, x, man):
            print('note:', n)
        rec[x['name']] = digest(a.root, x, man)
    rec_p.parent.mkdir(parents=True, exist_ok=True)
    save(rec_p, dict(sorted(rec.items())))
    n_icons = derive_icons(a.root, man, a.names)
    print(f'derived {len(assets)} asset(s)' + (f' and {n_icons} raster icon(s)' if n_icons else ''))


# --- check --------------------------------------------------------------------------------------------------

SKIP_DIRS = {'tools', '.venv', '.lane', 'node_modules', '__pycache__', '.git'}


def stray_svgs(root: pathlib.Path) -> list[str]:
    """Every .svg in the kit except icons/svg/ (check_icons.py gates those: allowed only for icons marked vector)."""
    found = []
    for base, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        rel = pathlib.Path(base).relative_to(root)
        if rel.parts[:2] == ('icons', 'svg'):
            continue
        found += [str(rel / f) for f in files if f.lower().endswith('.svg')]
    return sorted(found)


def og_problems(x: dict) -> list[str]:
    found = []
    for f in OG_FIELDS:
        if f in x and x.get('kind') != 'og':
            found.append(f'`{f}` is an og field; only the og asset sets it')
    if x.get('kind') != 'og':
        return found
    if 'title' in x and not str(x['title'] or '').strip():
        found.append('`title` is empty: set it, or remove the field to ship the art only')
    if x.get('subtitle') and not titled(x):
        found.append('`subtitle` needs a `title`')
    if x.get('text_position') not in (None,) + OG_POSITIONS:
        found.append(f"`text_position` must be one of {', '.join(OG_POSITIONS)}")
    if x.get('text_color') is not None and not HEX.fullmatch(str(x['text_color'])):
        found.append('`text_color` must be a #RRGGBB hex colour')
    return found


def check(root: pathlib.Path) -> tuple[int, list[str]]:
    errs: list[str] = []

    def err(f, m):
        errs.append(f'{f}: {m}')
    man_p = root / 'kit.json'
    if not man_p.exists():
        return 0, ['kit.json: missing; `kit.py keep` writes it']
    man = json.loads(man_p.read_text())
    assets = man.get('assets', [])
    names = [x.get('name') for x in assets]
    kinds = kinds_of(man)
    platforms = platforms_of(man)
    expected: set[str] = set()
    try:
        record_ = json.loads((root / RECORD).read_text())
    except (OSError, ValueError):
        record_ = {}
    if not isinstance(record_, dict):
        record_ = {}
    for where, msg in legacy(man):
        err(where, msg)
    for dup in sorted({n for n in names if names.count(n) > 1}, key=str):
        err('kit.json', f'{dup}: listed twice')
    for kind in sorted(SINGLE):
        if sum(x.get('kind') == kind for x in assets) > 1:
            err('kit.json', f'more than one {kind}; its outputs have fixed names')
    if platforms - set(PLATFORMS):
        err('kit.json', f"platforms: {', '.join(sorted(platforms - set(PLATFORMS)))} unknown; use {', '.join(PLATFORMS)}")
    for plat in sorted(ICON_COMPOSER & platforms):
        if 'app-icon' not in kinds:
            err('kit.json', f'platforms has {plat}: keep an app-icon asset; out/AppIcon.icon is derived from it')
    if 'tvos' in platforms and set(TV_REQUIRED) - kinds:
        err('kit.json', 'platforms has tvos: keep ' + ', '.join(sorted(set(TV_REQUIRED) - kinds)) +
            ' (the parallax icon needs a back and a front layer, and tvOS needs Top Shelf art)')
    if (kinds & (set(TV_LAYERS) | {'top-shelf'})) and 'tvos-icon-back' not in kinds:
        err('kit.json', 'a tvOS layer or top-shelf needs tvos-icon-back: it writes the brand assets Contents.json')
    if kinds & {'app-icon-maskable', 'favicon'} and 'app-icon' not in kinds:
        err('kit.json', 'an app-icon-maskable or favicon is a sibling of the app icon: keep an app-icon asset')
    no_pillow = False
    for x in assets:
        n = x.get('name') or '?'
        if not KEBAB.fullmatch(n):
            err('kit.json', f'{n}: name must be kebab-case')
        if x.get('kind') not in KINDS:
            if x.get('kind') != 'mark':  # mark already has its removal message
                err('kit.json', f"{n}: kind must be one of {', '.join(KINDS)}")
            continue
        for problem in provenance.problems(x):
            err('kit.json', f'{n}: {problem}')
        if 'vector' in x:
            err('kit.json', f'{n}: `vector` is for icons (icons/manifest.json, one icon at a time); an asset ships as '
                'the PNG it was drawn as')
        for problem in og_problems(x):
            err('kit.json', f'{n}: {problem}')
        raw = root / 'raw' / f'{n}.png'
        try:
            w, h, raw_alpha = png_info(raw)
        except (OSError, ValueError):
            err(f'raw/{n}.png', 'missing or not a PNG: every asset is a kept image-model render')
            continue
        if x['kind'] in SQUARE and abs(w - h) > max(w, h) * 0.01:
            err(f'raw/{n}.png', f'{w}x{h}; a {x["kind"]} render must be square')
            continue
        outs = plan(x, (w, h), kinds, platforms)
        if outs and record_.get(n) != digest(root, x, man):
            err(RECORD, f'{n}: out/ was not derived from the current raw/{n}.png and settings; run kit.py derive')
        ref = None
        if not titled(x) and not no_pillow:  # a titled OG card has its title typeset over the art
            try:
                ref = fidelity.Reference(raw)
            except fidelity.NeedsPillow as e:
                no_pillow = True
                err('pillow', str(e))
            except Exception as e:
                err(f'raw/{n}.png', f'not a readable image: {e}')
        for out in outs:
            expected.add(out['path'])
            p = root / out['path']
            if not p.exists():
                err(out['path'], 'missing; run kit.py derive')
                continue
            if out.get('text'):
                try:
                    if out['text'] == 'json':
                        json.loads(p.read_text())
                    elif not p.read_text().strip():
                        raise ValueError('empty')
                except ValueError as e:
                    err(out['path'], f'not valid {out["text"]}: {e}')
                continue
            try:
                if 'ico' in out:
                    if ico_sizes(p) != list(out['ico']):
                        err(out['path'], f"sizes {ico_sizes(p)}, want {list(out['ico'])}")
                    elif ref:
                        try:
                            for msg in fidelity.ico_problems(raw, p, out['ico']):
                                err(out['path'], msg)
                        except Exception as e:
                            err(out['path'], f'not a readable ICO: {e}')
                    continue
                pw, ph, alpha = png_info(p)
            except ValueError as e:
                err(out['path'], str(e))
                continue
            tw, th = out['size']
            if pw != tw or ph != th:
                err(out['path'], f'{pw}x{ph}, want {tw}x{th}')
                continue
            if out.get('opaque') and alpha:
                err(out['path'], 'has an alpha channel; app icons, touch icons and OG cards must be opaque')
            elif alpha and not raw_alpha:
                err(out['path'], 'has an alpha channel but the render has none: transparency is never manufactured')
            if ref:
                try:
                    from PIL import Image
                    with Image.open(p) as im:
                        msg = ref.problem(im)
                except Exception as e:
                    msg = f'not a readable image: {e}'
                if msg:
                    err(out['path'], msg)
    for path in stray_svgs(root):
        err(path, 'SVG is not allowed here: every asset ships as the PNG it was drawn as (an SVG exists only for an '
            'icon kept with --vector, in icons/svg/)')
    missing = set(man.get('required', [])) - set(names)
    if missing:
        err('kit', 'missing required assets: ' + ', '.join(sorted(missing)))
    out_dir = root / 'out'
    if out_dir.exists():
        stray = sorted(str(p.relative_to(root)) for p in out_dir.rglob('*')
                       if p.is_file() and not p.name.startswith('.'))  # .derived.json, .DS_Store
        stray = [p for p in stray if p not in expected and not p.lower().endswith('.svg')]
        if stray:
            err('out/', 'not produced by derive (remove, or keep the render and derive): ' + ', '.join(stray))
    n_icons = 0
    if (root / 'icons').exists():
        n_icons, icon_errs = check_icons.check(root / 'icons')
        errs += [f'icons/{e}' for e in icon_errs]
    if not assets and not n_icons:
        err('kit', 'nothing kept yet; keep renders with kit.py keep')
    return len(assets) + n_icons, list(dict.fromkeys(errs))


def main(argv=None):
    ap = argparse.ArgumentParser(description='Set up, keep, derive and gate an image-model-drawn product asset kit.')
    sub = ap.add_subparsers(dest='cmd', required=True)
    i = sub.add_parser('init', help='kit.json, .gitignore and a tools/ copy of these scripts')
    i.add_argument('--name', required=True)
    i.add_argument('--platforms', default='web', help=f"comma list of {', '.join(PLATFORMS)} (ios = iPhone and iPad); "
                   'macos/ios derive out/AppIcon.icon, tvos out/AppIcon.brandassets')
    k = sub.add_parser('keep', help='convert a kept render into raw/ and record its prompt, backend and tool')
    k.add_argument('render', type=pathlib.Path)
    k.add_argument('name')
    k.add_argument('--kind', required=True, choices=KINDS + ('icon',))
    k.add_argument('--backend', default='grok', choices=tuple(provenance.BACKENDS),
                   help='who drew it: grok (default), sol, astra or cursor')
    k.add_argument('--tool', choices=provenance.TOOLS, help='default: the backend\'s tool (grok: image_gen)')
    k.add_argument('--model', help='default: the Codex model id for sol and astra')
    k.add_argument('--prompt')
    k.add_argument('--prompt-file', type=pathlib.Path)
    k.add_argument('--prompt-index', type=pathlib.Path, help='a Codex run\'s raw/index.md: take the row named like the render')
    k.add_argument('--from', dest='from_', help='the render this one edited (image_edit) or was drawn from (a reference)')
    k.add_argument('--round', type=int, default=1)
    k.add_argument('--category')
    k.add_argument('--label')
    k.add_argument('--keywords', help='comma-separated; default: the name')
    k.add_argument('--vector', action='store_true',
                   help='--kind icon only: this one small monoline icon ships as a traced currentColor SVG '
                        '(default: PNG resizes of the render)')
    d = sub.add_parser('derive', help='resize and crop kept renders into the shipped sizes; typeset the OG title')
    d.add_argument('names', nargs='*')
    sub.add_parser('check', help='the gate; exit 0 = PASS')
    for p in sub.choices.values():
        p.add_argument('--root', type=pathlib.Path, default=pathlib.Path.cwd(), help='kit dir (default: cwd)')
    a = ap.parse_args(argv)
    if a.cmd == 'init':
        init(a)
    elif a.cmd == 'keep':
        keep(a)
    elif a.cmd == 'derive':
        derive(a)
    else:
        n, errs = check(a.root)
        print(f'{n} assets checked')
        if errs:
            print('\n'.join(errs))
            sys.exit(1)
        print('PASS')


if __name__ == '__main__':
    main()
