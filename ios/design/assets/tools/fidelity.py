"""Pixel-fidelity proof for the asset-pack gate: a derived raster is the kept render, resized and cropped, nothing else.

`kit.py check` and `check_icons.py` call this for every derived PNG (and each favicon.ico entry). The kept render is
centre-cropped to the output's aspect and Lanczos-resized to the output's exact size (the same operation derive ran,
done again with Pillow), then compared pixel by pixel with the output. Two correct Lanczos resizers agree to a small
fraction of a level; recolouring (palette snapping, a knocked-out ground, a colour swap, a rounded corner, a trimmed
margin) does not. Alpha is resized in premultiplied RGBA and clamped before compositing over mid grey, matching derive;
a changed alpha still shows as a changed colour. Flattening before resizing invents dark Lanczos fringes at clear edges.

The bar is a mean absolute difference of MEAN_MAX levels (0..255, averaged over pixels and channels). Measured on a
noisy, finely textured 1024 px test image, correct resizes scored 0.0 to 0.17; palette snapping scored 0.7 to 1.1 and a
single-level colour shift 0.8. Smooth art scores lower still.

Pure Pillow, no numpy; deterministic. A titled OG card is exempt (its title is typeset over the art), see kit.py.
"""
from __future__ import annotations

import pathlib

MEAN_MAX = 0.5


class NeedsPillow(RuntimeError):
    pass


def _pil():
    try:
        from PIL import Image, ImageChops, ImageStat
    except ImportError as e:  # pragma: no cover - environment dependent
        raise NeedsPillow('the pixel-fidelity check needs pillow (pip3 install pillow)') from e
    return Image, ImageChops, ImageStat


def _over_grey(im):
    """RGB of `im`, composited over mid grey when it has any transparency."""
    Image, _, _ = _pil()
    rgba = im.convert('RGBA')
    if rgba.getchannel('A').getextrema()[0] == 255:
        return rgba.convert('RGB')
    base = Image.new('RGBA', rgba.size, (128, 128, 128, 255))
    return Image.alpha_composite(base, rgba).convert('RGB')


def _cover_box(src_wh: tuple[int, int], target_wh: tuple[int, int]) -> tuple[float, float, float, float]:
    """The centred crop of src that has the target's aspect (the whole image when the aspects agree)."""
    sw, sh = src_wh
    tw, th = target_wh
    if sw * th >= sh * tw:  # src is wider than the target: trim the sides
        w = sh * tw / th
        return ((sw - w) / 2, 0.0, (sw + w) / 2, float(sh))
    h = sw * th / tw
    return (0.0, (sh - h) / 2, float(sw), (sh + h) / 2)


class Reference:
    """One kept render, loaded once, compared against each of its derived outputs."""

    def __init__(self, path: pathlib.Path):
        Image, _, _ = _pil()
        with Image.open(path) as im:
            self.rgba = im.convert('RGBA')

    def difference(self, out_im) -> float:
        """Mean absolute difference between `out_im` and the render resized and cropped to its size."""
        Image, ImageChops, ImageStat = _pil()
        out = _over_grey(out_im)
        want = _over_grey(self.rgba.resize(out.size, Image.LANCZOS,
                                          box=_cover_box(self.rgba.size, out.size)))
        return sum(ImageStat.Stat(ImageChops.difference(want, out)).mean) / 3

    def problem(self, out_im) -> str | None:
        mean = self.difference(out_im)
        if mean > MEAN_MAX:
            return (f'not a faithful resize of the kept render (mean difference {mean:.2f} levels, allowed '
                    f'{MEAN_MAX}): scripts only resize and crop, never recolour; regenerate the render instead')
        return None


def ico_problems(raw: pathlib.Path, ico: pathlib.Path, sizes) -> list[str]:
    """Fidelity of every entry of an ICO against the render it was resized from."""
    Image, _, _ = _pil()
    ref = Reference(raw)
    found = []
    with Image.open(ico) as f:
        for s in sizes:
            msg = ref.problem(f.ico.getimage((s, s)))
            if msg:
                found.append(f'{s}x{s} entry: {msg}')
    return found
