#!/usr/bin/env python3
"""Put a kit's Apple app icon into a built app, and prove a built app (macOS, iOS, iPadOS or tvOS) carries one.

    apple_icon.py compile --platform macos|ios|tvos --icon <kit>/out/AppIcon.icon|AppIcon.brandassets --app <X.app>
                          [--min-os 15.0]
    apple_icon.py check <X.app>                                      (exit 0 = the app has its icon)

`compile` is for apps a script bundles (SwiftPM, no Xcode project): it runs `xcrun actool` (Xcode 26 or newer) on the
one-layer Icon Composer bundle (macOS, iOS) or the tvOS brand assets that kit.py derives, writes Assets.car and the
fallbacks into the bundle, and merges the icon keys actool reports into its Info.plist. actool draws each OS's shape;
nothing here touches pixels. Run it before signing: it changes the bundle. An Xcode or XcodeGen target needs no
compile: add AppIcon.icon (or, for tvOS, put AppIcon.brandassets inside the target's .xcassets) and set
ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon.

`check` is the build's icon gate: Info.plist names an icon (CFBundleIconFile, CFBundleIconName or CFBundleIcons) and
the bundle holds it (the .icns or PNG fallback files, an entry of that name in Assets.car). An empty AppIcon set
compiles without a warning and ships the generic icon, so the built app is the only proof.
"""
from __future__ import annotations

import argparse
import json
import pathlib
import plistlib
import shutil
import subprocess
import sys
import tempfile

ACTOOL = {'macos': ('macosx', [], '15.0'), 'ios': ('iphoneos', ['iphone', 'ipad'], '17.0'),
          'tvos': ('appletvos', ['tv'], '17.0')}


def layout(app: pathlib.Path) -> tuple[pathlib.Path, pathlib.Path]:
    """(Info.plist, resources dir): a Mac bundle nests both under Contents/, an iOS or tvOS bundle is flat."""
    if (app / 'Contents').is_dir():
        return app / 'Contents' / 'Info.plist', app / 'Contents' / 'Resources'
    return app / 'Info.plist', app


def compile_icon(platform: str, icon: pathlib.Path, app: pathlib.Path, min_os: str | None) -> None:
    want = 'icon.json' if platform != 'tvos' else 'Contents.json'
    if not (icon / want).is_file():
        sys.exit(f'{icon}: no {want}; run kit.py derive with "{platform}" in kit.json platforms')
    info_p, res = layout(app)
    if not info_p.is_file():
        sys.exit(f'{info_p}: missing; compile into a built app bundle')
    res.mkdir(parents=True, exist_ok=True)
    plat, devices, default_min = ACTOOL[platform]
    name = icon.name.rsplit('.', 1)[0]
    with tempfile.TemporaryDirectory() as t:
        src = icon.resolve()
        if platform == 'tvos':  # brand assets compile only from inside an asset catalog
            cat = pathlib.Path(t) / 'Icon.xcassets'
            cat.mkdir()
            (cat / 'Contents.json').write_text(json.dumps({'info': {'author': 'xcode', 'version': 1}}))
            shutil.copytree(src, cat / icon.name)
            src = cat
        partial = pathlib.Path(t) / 'partial.plist'
        cmd = ['xcrun', 'actool', str(src), '--compile', str(res.resolve()), '--platform', plat,
               '--minimum-deployment-target', min_os or default_min, '--app-icon', name,
               '--output-partial-info-plist', str(partial), '--output-format', 'human-readable-text']
        for d in devices:
            cmd += ['--target-device', d]
        run = subprocess.run(cmd, capture_output=True, text=True)
        if run.returncode or not partial.is_file():
            sys.exit(f'actool failed ({run.returncode}):\n{run.stdout}{run.stderr}')
        keys = plistlib.loads(partial.read_bytes())
    info = plistlib.loads(info_p.read_bytes())
    info.update(keys)
    info_p.write_bytes(plistlib.dumps(info))
    print(f'{app.name}: set {", ".join(sorted(keys))}; sign the bundle after this')


def car_names(car: pathlib.Path) -> set:
    out = subprocess.run(['assetutil', '--info', str(car)], capture_output=True, text=True)
    try:
        return {e.get('Name') for e in json.loads(out.stdout) if isinstance(e, dict)} if not out.returncode else set()
    except ValueError:
        return set()


def declared(info: dict) -> tuple[set, set, set]:
    """(icns files, PNG fallback prefixes, asset catalog names) the Info.plist points at."""
    icns, pngs, names = set(), set(), set()
    if info.get('CFBundleIconFile'):
        icns.add(info['CFBundleIconFile'])
    if info.get('CFBundleIconName'):
        names.add(info['CFBundleIconName'])
    for key in ('CFBundleIcons', 'CFBundleIcons~ipad'):
        primary = (info.get(key) or {}).get('CFBundlePrimaryIcon')
        if isinstance(primary, str):  # tvOS names the brand-asset stack
            names.add(primary)
        elif isinstance(primary, dict):
            if primary.get('CFBundleIconName'):
                names.add(primary['CFBundleIconName'])
            pngs.update(primary.get('CFBundleIconFiles') or [])
    return icns, pngs, names


def problems(app: pathlib.Path) -> list[str]:
    info_p, res = layout(app)
    try:
        info = plistlib.loads(info_p.read_bytes())
    except (OSError, plistlib.InvalidFileException) as e:
        return [f'{info_p}: unreadable ({e})']
    icns, pngs, names = declared(info)
    if not (icns or pngs or names):
        return ['Info.plist names no icon (no CFBundleIconFile, CFBundleIconName or CFBundleIcons): the app ships '
                "the generic icon; add the kit's out/AppIcon.icon or AppIcon.brandassets (apple_icon.py compile, or "
                'the Xcode target)']
    errs = []
    for f in sorted(icns):
        p = res / (f if f.endswith('.icns') else f'{f}.icns')
        if not p.is_file() or p.stat().st_size == 0:
            errs.append(f'CFBundleIconFile={f} but {p.relative_to(app)} is missing or empty')
    for prefix in sorted(pngs):
        if not list(res.glob(f'{prefix}*.png')):
            errs.append(f'CFBundleIconFiles lists {prefix} but no {prefix}*.png is in the bundle')
    if names:
        car = res / 'Assets.car'
        if not car.is_file():
            errs.append(f'icon {", ".join(sorted(names))} named but Assets.car is missing')
        else:
            have = car_names(car)
            errs += [f'icon {n} named but Assets.car has no entry {n} (an empty AppIcon set?)'
                     for n in sorted(names - have)]
    return errs


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description='Compile a kit Apple icon into an app, or check an app has its icon.')
    sub = ap.add_subparsers(dest='cmd', required=True)
    c = sub.add_parser('compile', help='actool the kit icon into the app and merge its Info.plist keys')
    c.add_argument('--platform', choices=sorted(ACTOOL), required=True)
    c.add_argument('--icon', type=pathlib.Path, required=True)
    c.add_argument('--app', type=pathlib.Path, required=True)
    c.add_argument('--min-os', help="the app's minimum OS version (default macOS 15.0, iOS and tvOS 17.0)")
    k = sub.add_parser('check', help='exit 0 when the built app carries its icon')
    k.add_argument('app', type=pathlib.Path)
    a = ap.parse_args(argv)
    if a.cmd == 'compile':
        compile_icon(a.platform, a.icon, a.app, a.min_os)
        return 0
    errs = problems(a.app)
    for e in errs:
        print(f'{a.app.name}: {e}')
    print('FAIL' if errs else 'PASS')
    return 1 if errs else 0


if __name__ == '__main__':
    sys.exit(main())
