"""Writes oracle.txt: what HarfBuzz and fontTools say a variable font's
instance draws, for fonts_instance_test.go to hold pdf0's embedding to.

    python3 oracle.py FORME_MODULE_DIR > oracle.txt

FORME_MODULE_DIR is `go list -m -f {{.Dir}} github.com/mgilbir/forme`. The
fonts are forme's: its bundled Noto Sans (a variable font with glyf outlines)
and its CFF2Blend.otf fixture (CFF2 outlines). Needs fontTools and uharfbuzz;
oracle.txt records the versions it was made with.

One line per glyph: the font, the location, the character, its glyph index,
HarfBuzz's horizontal advance at the location, the advance in the hmtx of
fontTools' instance, HarfBuzz's ink extents at the location (x bearing, y
bearing, width, height) and the bounds of the instance's outline as fontTools
draws it (xMin, yMin, xMax, yMax). The two oracles are independent programs;
the script fails where they disagree, so every line is one both state.
"""
import os
import sys

import fontTools
import uharfbuzz as hb
from fontTools.pens.boundsPen import BoundsPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

CASES = [
    ("notosans", "fonts/notosans/NotoSans-Variable.ttf",
     [{"wght": 700}, {"wght": 300, "wdth": 75}], "Hamburgefonstiv AVATAR"),
    # CFF2Blend is built so that HarfBuzz and fontTools round its blends
    # differently at the inner locations (see its cff2_fixture.py); at the
    # ends of its axes every scalar is one, and the two agree exactly.
    ("cff2blend", "testdata/harfbuzz/fonts/CFF2Blend.otf",
     [{"wght": 16384}, {"wght": 16384, "XOPQ": 16384}], "abcdefghijkl"),
]


def loc_str(loc):
    return ",".join(f"{k}={v:g}" for k, v in sorted(loc.items()))


def main():
    root = sys.argv[1]
    print(f"# fontTools {fontTools.version}, uharfbuzz {hb.__version__}, HarfBuzz {hb.version_string()}")
    print("# font location char gid hbAdvance ftAdvance hbXBearing hbYBearing hbWidth hbHeight ftXMin ftYMin ftXMax ftYMax")
    for name, rel, locs, text in CASES:
        path = os.path.join(root, rel)
        blob = hb.Blob.from_file_path(path)
        for loc in locs:
            face = hb.Face(blob)
            font = hb.Font(face)
            font.set_variations(loc)
            inst = instancer.instantiateVariableFont(TTFont(path), loc)
            gs = inst.getGlyphSet()
            order = inst.getGlyphOrder()
            cmap = inst.getBestCmap()
            for ch in dict.fromkeys(text):
                gname = cmap[ord(ch)]
                gid = order.index(gname)
                hb_adv = font.get_glyph_h_advance(gid)
                ft_adv = inst["hmtx"][gname][0]
                if hb_adv != ft_adv:
                    sys.exit(f"{name} {loc}: {ch!r} advances {hb_adv} by HarfBuzz and {ft_adv} by fontTools")
                ext = font.get_glyph_extents(gid)
                pen = BoundsPen(gs)
                gs[gname].draw(pen)
                b = pen.bounds or (0, 0, 0, 0)
                if b[0] == b[2] and b[1] == b[3]:
                    # A contour that only moves (CFF2Blend's "moveonly")
                    # has a point and no ink: fontTools' bounds are the
                    # point, and HarfBuzz states no extents.
                    b = (0, 0, 0, 0)
                if ext is None:
                    ext = hb.GlyphExtents(0, 0, 0, 0)
                hbb = (ext.x_bearing, ext.y_bearing + ext.height, ext.x_bearing + ext.width, ext.y_bearing)
                if b != (0, 0, 0, 0) and any(abs(x - y) > 1 for x, y in zip(hbb, b)):
                    sys.exit(f"{name} {loc}: {ch!r} is inked over {hbb} by HarfBuzz and {b} by fontTools")
                cp = "U+%04X" % ord(ch)
                print(name, loc_str(loc), cp, gid, hb_adv, ft_adv,
                      ext.x_bearing, ext.y_bearing, ext.width, ext.height,
                      *(round(v) for v in b))


main()
