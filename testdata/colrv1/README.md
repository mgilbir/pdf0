# COLRv1 conformance glyphs

`test_glyphs-glyf_colr_1.ttf` is Google Fonts' COLRv1 test font, from
[googlefonts/color-fonts](https://github.com/googlefonts/color-fonts) at
commit `0046ea4c3b69e9fbbe464c2594816894e3aa5e4b` (`fonts/`), under the
Apache License 2.0 in `LICENSE`. Each glyph tests one feature of COLRv1:
every gradient and its extend modes, sweeps past a full turn, every composite
mode, clip boxes, transforms, nested glyphs.

`skia_render.py` draws its glyphs with blackrenderer's Skia backend, the
oracle `TestColourGlyphsAreWhatSkiaPaints` holds pdf0's Type 3 colour glyphs
to. It needs the packages in `requirements.txt`; the test finds the Python
that has them through `PDF0_SKIA_PYTHON` and skips without it:

    python3 -m venv .venv-skia
    .venv-skia/bin/pip install -r testdata/colrv1/requirements.txt
    PDF0_SKIA_PYTHON=.venv-skia/bin/python go test -run TestColourGlyphs .
