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

# Two sandhangan on one letter, which the grid above never writes.
#
# The differential fuzzer found what this is here for. Noto Sans Javanese
# carries a placeholder mark through its rules and takes it off again with a
# substitution of *no glyphs at all* — a deletion, which the format's own text
# forbids and fonts state anyway. A shaper that ignores it leaves the
# placeholder on the page, and nothing in the corpus showed it, because the
# rule that puts the placeholder there needs a second sign to fire.
#
# Every ordered pair on one letter, and then one pair on every letter: the
# first says the rule is reached from any combination, the second that it is
# not about the letter.
vowels = [chr(c) for c in range(0xA984, 0xA98A)]  # the independent vowels
for s in signs:
    for t in signs:
        words.append(vowels[0] + s + t)
for c in vowels + cons:
    words.append(c + "ꦴ" + "ꦁ")  # tarung, then cecak

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "javanese.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
