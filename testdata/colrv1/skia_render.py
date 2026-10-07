"""Render a font's COLR glyphs with Skia, the oracle for pdf0's colour glyphs.

Usage: skia_render.py FONT OUTDIR SCALE

Every COLRv0 and COLRv1 glyph of FONT is drawn by blackrenderer through its
Skia backend at SCALE pixels per font unit, on a white background, to
OUTDIR/<gid>.png. OUTDIR/index.json maps each glyph id to its name and to the
box the image covers, in pixels at that scale with y up, so that the test can
place pdf0's drawing of the glyph on exactly the same pixels.

blackrenderer is the COLRv1 reference renderer from Google Fonts, and Skia is
the renderer COLRv1 was specified against. The versions the test was written
against are pinned in requirements.txt next to this file.
"""

import json
import math
import os
import sys

from blackrenderer.backends import getSurfaceClass
from blackrenderer.font import BlackRendererFont


def main():
    font_path, out_dir, scale = sys.argv[1], sys.argv[2], float(sys.argv[3])
    font = BlackRendererFont(font_path)
    order = font.glyphNames
    names = set(font.colrV0GlyphNames) | set(font.colrV1GlyphNames)
    surface_class = getSurfaceClass("skia")
    index = {}
    for gid, name in enumerate(order):
        if name not in names:
            continue
        x0, y0, x1, y1 = font.getGlyphBounds(name)
        box = (
            math.floor(x0 * scale) - 2,
            math.floor(y0 * scale) - 2,
            math.ceil(x1 * scale) + 2,
            math.ceil(y1 * scale) + 2,
        )
        surface = surface_class()
        try:
            with surface.canvas(box) as canvas:
                # White beneath, as the page pdf0's drawing is rendered on.
                with canvas.savedState():
                    canvas.drawRectSolid(box, (1, 1, 1, 1))
                canvas.scale(scale)
                font.drawGlyph(name, canvas)
        except RecursionError as e:
            # A glyph that paints itself: the font's own test of a cycle.
            index[gid] = {"name": name, "error": str(e)}
            continue
        surface.saveImage(os.path.join(out_dir, f"{gid}.png"))
        index[gid] = {"name": name, "box": box}
    with open(os.path.join(out_dir, "index.json"), "w") as f:
        json.dump(index, f)


if __name__ == "__main__":
    main()
