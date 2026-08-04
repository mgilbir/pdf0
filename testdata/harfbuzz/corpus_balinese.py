# Generates balinese.txt: a second script for the Universal Shaping Engine.
#
# One script is not enough to test an engine that claims seventy. Javanese was
# what the engine was written against, and it reached every case — and Balinese,
# which is its close relative, was wrong in 66 of 764 at that point. A model is
# only general if something other than the thing it was built for agrees.
#
# What this adds over the Javanese grid is the split vowel signs: U+1B40 and
# U+1B41 are each written as one character and drawn as two marks on opposite
# sides of the letter, which is the case that found the defect.
#
#   python3 testdata/harfbuzz/corpus_balinese.py
import os

cons = [chr(c) for c in range(0x1B13, 0x1B34)]
signs = [chr(c) for c in range(0x1B35, 0x1B44)]
adeg = "᭄"

words = []
for c in cons:
    words.append(c)
    for s in signs:
        words.append(c + s)
for a in cons[:14]:
    for b in cons[:14]:
        words.append(a + adeg + b)
for a in cons[:8]:
    for b in cons[:5]:
        words.append(a + adeg + b + signs[0])

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "balinese.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
