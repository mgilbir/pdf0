# Generates javanese.txt: the strings the Universal Shaping Engine comparison
# shapes.
#
# Javanese has no shaper of its own anywhere — it is one of the several dozen
# scripts the Universal Shaping Engine covers, which is the model for scripts
# that behave like the Indic ones without each having a hand-written shaper.
# What is exercised here is what such a script needs:
#
#   - every aksara on its own, and with every sandhangan: the vowel signs and
#     medials, including the three that are written after the letter and drawn
#     before it;
#   - the pangkon grid, every consonant stacked under every other, which is how
#     Javanese writes a consonant cluster;
#   - a stacked pair carrying a vowel sign as well, so that the reordering has
#     three things to put in three places.
#
#   python3 testdata/harfbuzz/corpus_javanese.py
import os

cons = [chr(c) for c in range(0xA98A, 0xA9B3)]   # aksara
signs = [chr(c) for c in range(0xA9B4, 0xA9C1)]  # sandhangan
pangkon = "꧀"

words = []
for c in cons:
    words.append(c)
    for s in signs:
        words.append(c + s)
for a in cons[:16]:
    for b in cons[:16]:
        words.append(a + pangkon + b)
for a in cons[:10]:
    for b in cons[:6]:
        words.append(a + pangkon + b + signs[0])
words += ["ꦲꦏ꧀ꦱꦫ", "ꦗꦮ", "ꦱꦸꦒꦼꦁ", "ꦥꦿꦧꦸ"]

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "javanese.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
