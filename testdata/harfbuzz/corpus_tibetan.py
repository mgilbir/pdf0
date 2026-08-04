# Generates tibetan.txt: a third script for the Universal Shaping Engine, and
# the largest font in this directory.
#
# It earns its size. Adding it found three defects that had nothing to do with
# Tibetan and everything to do with what a large font is allowed to contain:
# a lookup list truncated at 512 when this font declares 1190, one lookup's
# subtables truncated at 256 when this font states 738 of them, and the mark
# glyph sets a lookup names to say which marks it looks at, which were not read
# at all. The first two were silent — a lookup is named by index, so cutting the
# list breaks every reference past the cut rather than losing the tail.
#
#   python3 testdata/harfbuzz/corpus_tibetan.py
import os

cons = [chr(c) for c in range(0x0F40, 0x0F6D)]
sub = [chr(c) for c in range(0x0F90, 0x0FBD)]
vow = [chr(c) for c in range(0x0F71, 0x0F85)]

words = []
for c in cons:
    words.append(c)
    for v in vow:
        words.append(c + v)
# Every consonant of the first sixteen under every subjoined form, not the
# first sixteen of those: a subjoined letter is given an advance by a
# contextual rule that names the pair, and the pair the fuzzer found — tta with
# subjoined tsa — is the twenty-sixth.
for a in cons[:16]:
    for b in sub:
        words.append(a + b)
for a in cons[:8]:
    for b in sub[:5]:
        words.append(a + b + vow[0])

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "tibetan.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
