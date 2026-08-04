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
# The bundled face appears four times, once per script it covers, and that is
# the point. Listing its ranges together drew strings mixing Latin with
# Devanagari, and HarfBuzz performs no script itemization either — its caller is
# required to hand it a run of one script, exactly as it is required to hand it
# one direction. So a mixed-script string is not a comparison of shaping, and
# 853 of the 854 differences it reported were that and nothing else.
FONTS = [
    ("latin", os.path.join(ROOT, "fonts", "notosans", "NotoSans-Variable.ttf"),
     [(0x0041, 0x024F), (0x0300, 0x036F)], False),
    ("greek", os.path.join(ROOT, "fonts", "notosans", "NotoSans-Variable.ttf"),
     [(0x0370, 0x03FF), (0x0300, 0x036F)], False),
    ("cyrillic", os.path.join(ROOT, "fonts", "notosans", "NotoSans-Variable.ttf"),
     [(0x0400, 0x04FF), (0x0300, 0x036F)], False),
    ("devanagari", os.path.join(ROOT, "fonts", "notosans", "NotoSans-Variable.ttf"),
     [(0x0900, 0x097F)], False),
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
#
# It is empty. There were four. Two were gaps and are fixed: the missing dotted
# circle for a broken cluster, and the ordering of two modifier marks on one
# letter. The third was recorded as a decision and was not one — a reserved
# character being given a category that broke the cluster after it. The fourth
# was a real decision, held for a long time and now reversed: a character nothing
# is drawn for, written inside a syllable, is kept until the syllable model has
# seen it, because Unicode does not ask for it to be removed and neither HarfBuzz
# nor CoreText removes it. See fonts/harfbuzz_test.go.
#
# An empty set is the point of the tool: every difference it reports is a defect.
KNOWN = set()

def classify(text, ours, theirs):
    """Name a difference that is already understood, or None if it is new.

    Nothing is, so this names nothing. It is kept because the shape of the tool
    is "report what has not been explained", and the day something has to be
    explained again this is where the explanation goes — with the table it needs
    beside it, derived rather than typed. The last one it held was a hand-written
    copy of Unicode's Default_Ignorable_Code_Point that had gone stale by two
    characters.
    """
    return None


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
                sa = shape_pdf0(path, [small])[0]
                sb = shape_harfbuzz(face, [small])[0]
                # Classified again, on the string that is actually reported.
                #
                # Minimising changes what the case *is*: dropping a character
                # can turn a difference nobody has seen into one that is written
                # down, and the first version of this only asked before
                # minimising. So a run of a few minutes reported hundreds of
                # "new" differences that were the recorded gap all along, which
                # is the failure this tool exists to avoid — a report nobody can
                # read is a report nobody reads.
                if classify(small, sa, sb) in KNOWN:
                    continue
                found[key] = (sa, sb)

    print(f"{tried} strings over {len(loaded)} fonts, {len(found)} differences")
    # Grouped by font, with a count per font first. The interesting number is
    # how many *kinds* there are, and a flat list of several hundred hides it.
    by_font = {}
    for name, text in found:
        by_font.setdefault(name, []).append(text)
    if found:
        print("\n" + "  ".join(f"{n} {len(v)}" for n, v in sorted(by_font.items())))
    for (name, text), (a, b) in sorted(found.items()):
        cps = " ".join(f"U+{ord(c):04X}" for c in text)
        print(f"\n{name}: {cps}")
        print(f"   pdf0     {a}")
        print(f"   harfbuzz {b}")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
