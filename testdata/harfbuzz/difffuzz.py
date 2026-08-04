# Differential fuzzing: generate text, shape it with both, compare.
#
# The corpora beside this file are fixed lists, chosen by hand to reach the
# places shaping decides something. They are good at what they were written for
# and blind to everything nobody thought of — which is most of the state space,
# because a shaper's behaviour is a function of a *sequence* and the sequences
# are unbounded.
#
# This generates them instead. Every defect it reports is one implementation
# disagreeing with the other on a real font, which is the same evidence the
# checked-in corpora carry; a case it finds is meant to be added to them.
#
# # What it does not do
#
# It does not mutate the fonts. Random bytes in a font produce a font neither
# side can read, and structured mutation of a layout table produces one whose
# *correct* shaping nobody knows — HarfBuzz's answer would be as arbitrary as
# this package's, and comparing them says nothing. The Go fuzzer in
# fonts/panic_test.go is what covers malformed fonts, and it asks a different
# question: not "is this right" but "does this survive".
#
# So the fonts are real and the text is generated. That is where the answers are
# knowable and where the disagreements have been.
#
# # What it avoids, and why
#
#   - Mixed direction. HarfBuzz runs no bidirectional algorithm — its caller is
#     required to hand it runs of one direction — so a string whose direction it
#     would guess differently is not a comparison of shaping. Generated text is
#     drawn from one script at a time.
#   - The differences already known and written down. They are listed in
#     fonts/harfbuzz_test.go with their reasons, and re-reporting them would bury
#     anything new.
#
#   python3 testdata/harfbuzz/difffuzz.py [seconds] [--seed N]
import os
import random
import subprocess
import sys
import time
import unicodedata

import uharfbuzz as hb
from fontTools.ttLib import TTFont

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))

# Each font, and the Unicode ranges its script owns. Text is drawn from the
# font's own cmap narrowed to those ranges: a font covers Latin as well, and a
# string mixing Latin with Javanese is two runs and two directions' worth of
# assumptions rather than one shaper's behaviour.
# The last field says whether the script is written right to left, which decides
# which bidirectional class the generated text is drawn from.
FONTS = [
    ("bundled", os.path.join(ROOT, "fonts", "notosans", "NotoSans-Variable.ttf"),
     [(0x0041, 0x024F), (0x0300, 0x036F), (0x0370, 0x03FF), (0x0400, 0x04FF),
      (0x0900, 0x097F)], False),
    ("arabic", os.path.join(HERE, "fonts", "NotoSansArabic.ttf"),
     [(0x0600, 0x06FF), (0x0750, 0x077F), (0xFB50, 0xFDFF), (0xFE70, 0xFEFF)], True),
    ("khmer", os.path.join(HERE, "fonts", "NotoSansKhmer.ttf"),
     [(0x1780, 0x17FF), (0x19E0, 0x19FF)], False),
    ("javanese", os.path.join(HERE, "fonts", "NotoSansJavanese.ttf"),
     [(0xA980, 0xA9DF)], False),
    ("balinese", os.path.join(HERE, "fonts", "NotoSansBalinese.ttf"),
     [(0x1B00, 0x1B7F)], False),
    ("tibetan", os.path.join(HERE, "fonts", "NotoSerifTibetan.ttf"),
     [(0x0F00, 0x0FFF)], False),
]

# The differences that are already understood, so that a new one is visible.
# They are keyed the way fonts/harfbuzz_test.go keys them, and this file is the
# only other place they are named — if one is fixed there it will simply stop
# appearing here.
# Two of these are decisions, written down with their reasons in
# fonts/harfbuzz_test.go. The third is a gap: it is here so that the fuzzer keeps
# showing things nobody has seen yet rather than burying them under something
# already known, and it should be removed the moment it is fixed.
KNOWN = {
    # A decision. A character nothing is drawn for, inside a cluster: this
    # package removes it before shaping, HarfBuzz keeps it until after.
    "ignorable-in-cluster",
    # A decision. A deprecated Tibetan vowel sign after an unassigned code point.
    "deprecated-after-unassigned",
    # NOT a decision. The universal engine does not insert a dotted circle for a
    # cluster it cannot parse, and the Indic and Khmer shapers here do. Marking
    # a syllable as malformed is how a reader is told the text is; leaving it out
    # sets the characters as though they were fine.
    #
    # Closing it needs the engine's cluster *grammar*, not just its segmentation:
    # something has to decide that a Balinese musical symbol after an independent
    # vowel is not a syllable. That is why it is recorded rather than fixed here.
    "no-dotted-circle",
    # NOT a decision. Two marks of class 220 or 230 on one letter, where UTR #53
    # has something to say about at least one of them. The single-mark cases —
    # which is what real Arabic writes — agree; these do not, and no rule tried
    # here fits every one of them.
    #
    # It is recorded rather than guessed at again. Three hypotheses were tested
    # against HarfBuzz and each fitted some combinations and contradicted
    # others, which is the shape of fitting rules to observations rather than
    # implementing a specification. Closing it needs UTR #53's own text.
    "two-modifier-marks",
}

IGNORABLE = [
    (0x00AD, 0x00AD), (0x034F, 0x034F), (0x061C, 0x061C), (0x180B, 0x180F),
    (0x200B, 0x200F), (0x202A, 0x202E), (0x2060, 0x206F), (0xFE00, 0xFE0F),
    (0xFEFF, 0xFEFF), (0x1D173, 0x1D17A),
]


def is_ignorable(ch):
    return any(lo <= ord(ch) <= hi for lo, hi in IGNORABLE)


def classify(text, ours, theirs):
    """Name a difference that is already understood, or None if it is new."""
    if any(is_ignorable(c) for c in text[1:-1]):
        return "ignorable-in-cluster"
    if any(0x0F48 == ord(c) or 0x0F98 == ord(c) for c in text):
        return "deprecated-after-unassigned"
    # A dotted circle on one side and not the other. It is recognised by the
    # glyph *counts* rather than by naming a glyph index, because the index is
    # the font's and every font numbers it differently.
    if len(theirs.split()) > len(ours.split()):
        return "no-dotted-circle"
    # Two of the marks UTR #53 orders, on one letter.
    ordered = [c for c in text if unicodedata.combining(c) in (220, 230)]
    if len(ordered) >= 2 and any(ord(c) in UTR53_MARKS for c in text):
        return "two-modifier-marks"
    return None


# The fourteen characters UTR #53 is about, mirroring arabicModifierMarks in
# fonts/normalize.go.
UTR53_MARKS = {
    0x0654, 0x0655, 0x0658, 0x06DC, 0x06E3, 0x06E7, 0x06E8,
    0x08CA, 0x08CB, 0x08CD, 0x08CE, 0x08CF, 0x08D3, 0x08F3,
}


def alphabet(path, ranges, rtl):
    """The characters of the script the font has, and the letters among them.

    Narrowed to one bidirectional class plus the marks, which is what makes the
    comparison mean anything. HarfBuzz runs no bidirectional algorithm — it
    takes the whole buffer as one direction — while this package runs UAX #9, so
    anything that resolves to a *second* direction comes back as a reversal
    rather than as a difference in shaping.
    
    The Arabic-Indic digits are the case that taught this: they are class AN, so
    a number inside right-to-left text reads left to right, and this package
    orders it that way while HarfBuzz reverses it with everything else. Forty
    thousand strings produced sixteen hundred of those and not one defect.
    """
    strong = {"AL", "NSM"} if rtl else {"L", "NSM"}
    cmap = set(TTFont(path, lazy=True).getBestCmap())
    alpha = sorted(
        c for c in cmap
        if any(lo <= c <= hi for lo, hi in ranges)
        and unicodedata.bidirectional(chr(c)) in strong
    )
    letters = [c for c in alpha if unicodedata.category(chr(c)) in ("Lo", "Lu", "Ll")]
    return alpha, letters


def shape_harfbuzz(face, lines):
    out = []
    for s in lines:
        font = hb.Font(face)
        buf = hb.Buffer()
        buf.add_str(s)
        buf.guess_segment_properties()
        buf.flags = hb.BufferFlags.REMOVE_DEFAULT_IGNORABLES
        hb.shape(font, buf, None)
        fields = []
        for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
            if pos.x_offset or pos.y_offset:
                fields.append(f"{info.codepoint},{pos.x_advance},{pos.x_offset},{pos.y_offset}")
            else:
                fields.append(f"{info.codepoint},{pos.x_advance}")
        out.append(" ".join(fields))
    return out


def shape_pdf0(path, lines):
    p = subprocess.run(
        [os.environ.get("SHAPETEXT", "") or "go", "run", "./cmd/shapetext", path] if not os.environ.get("SHAPETEXT") else [os.environ["SHAPETEXT"], path],
        cwd=ROOT, input="\n".join(lines) + "\n",
        capture_output=True, text=True,
    )
    if p.returncode != 0:
        raise SystemExit(f"shapetext failed: {p.stderr.strip()}")
    return p.stdout.splitlines()


def generate(rng, alpha, letters, count):
    """Random strings over one script's characters, each beginning with a letter.

    Short ones dominate, because a cluster is short and a disagreement in a long
    string is usually a disagreement in one of its clusters — reported at a
    length nobody can read.

    The first character is always a letter, and that is not a stylistic choice.
    A string of nothing but marks and signs has no strong direction in it, so
    the two implementations resolve one differently — this package by running
    UAX #9, HarfBuzz by guessing from the first character that has a script —
    and every such case comes back as a reversal rather than as a difference in
    shaping. Twelve thousand strings produced two hundred and forty-one of them
    and not one real defect. A letter at the front settles the direction for
    both, which is also what real text does.
    """
    out = []
    for _ in range(count):
        n = rng.choices([1, 2, 3, 4, 5, 6, 8], weights=[3, 8, 8, 6, 4, 2, 1])[0]
        s = chr(rng.choice(letters))
        s += "".join(chr(rng.choice(alpha)) for _ in range(n - 1))
        out.append(s)
    return out


def minimise(font_path, face, text):
    """Shrink a failing string while it still fails.

    The first character stays. It is the letter that gives the string a
    direction, and dropping it produces a case the two implementations resolve
    differently for a reason that has nothing to do with the defect being
    minimised — so the minimiser would happily "shrink" a real difference into a
    reversal and report that instead.
    """
    best = text
    changed = True
    while changed and len(best) > 2:
        changed = False
        for i in range(1, len(best)):
            shorter = best[:i] + best[i + 1:]
            if shape_pdf0(font_path, [shorter]) != shape_harfbuzz(face, [shorter]):
                best, changed = shorter, True
                break
    return best


def main():
    budget = float(sys.argv[1]) if len(sys.argv) > 1 else 60.0
    seed = 0
    if "--seed" in sys.argv:
        seed = int(sys.argv[sys.argv.index("--seed") + 1])
    rng = random.Random(seed)

    loaded = []
    for name, path, ranges, rtl in FONTS:
        if not os.path.exists(path):
            print(f"skipping {name}: {path} is not there", file=sys.stderr)
            continue
        alpha, letters = alphabet(path, ranges, rtl)
        if not letters:
            print(f"skipping {name}: no letters in range", file=sys.stderr)
            continue
        loaded.append((name, path, hb.Face(hb.Blob.from_file_path(path)), alpha, letters))

    deadline = time.time() + budget
    tried = 0
    found = {}
    while time.time() < deadline:
        for name, path, face, alpha, letters in loaded:
            if time.time() >= deadline:
                continue
            lines = generate(rng, alpha, letters, 400)
            tried += len(lines)
            ours, theirs = shape_pdf0(path, lines), shape_harfbuzz(face, lines)
            for text, a, b in zip(lines, ours, theirs):
                if a == b:
                    continue
                if classify(text, a, b) in KNOWN:
                    continue
                small = minimise(path, face, text)
                key = (name, small)
                if key in found:
                    continue
                found[key] = (
                    shape_pdf0(path, [small])[0],
                    shape_harfbuzz(face, [small])[0],
                )

    print(f"{tried} strings over {len(loaded)} fonts, {len(found)} differences")
    for (name, text), (a, b) in sorted(found.items()):
        cps = " ".join(f"U+{ord(c):04X}" for c in text)
        print(f"\n{name}: {cps}")
        print(f"   pdf0     {a}")
        print(f"   harfbuzz {b}")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
