# Generates khmer.txt: the strings the Khmer comparison shapes.
#
# Khmer is set by its own syllable model — the characters are not in the order
# they are drawn — and until now nothing outside this repository had ever checked
# it. What is exercised here:
#
#   - the coeng (U+17D2) subscript grid: every consonant written under every
#     other, which is the shape of most Khmer text and the whole of the
#     reordering.
#   - the pre-base vowels, which are written after the consonant and drawn
#     before it.
#   - the split vowels, whose halves go on opposite sides of the letter.
#   - subscript Ro, which moves to the front of the syllable whatever stands
#     between.
#   - a syllable with nothing for its marks to hang off, which needs a
#     placeholder.
#   - the join controls, which force or forbid the forms the model would choose.
#
#   python3 testdata/harfbuzz/corpus_khmer.py
import os

words = []

coeng = "្"
consonants = [chr(c) for c in range(0x1780, 0x17A3)]
independents = [chr(c) for c in range(0x17A5, 0x17B4)]
# Dependent vowel signs, including the pre-base and the split ones.
vowels = [chr(c) for c in range(0x17B6, 0x17C6)]
# Signs: nikahit, reahmuk, and the register shifters and diacritics after them.
signs = [chr(c) for c in range(0x17C6, 0x17D4)]
ro = "រ"

# Every consonant on its own, and with every vowel sign.
for c in consonants:
    words.append(c)
    for v in vowels:
        words.append(c + v)

# The subscript grid: every consonant under every other.
for a in consonants:
    for b in consonants:
        words.append(a + coeng + b)

# Two subscripts, which is as deep as Khmer goes.
for a in consonants[:10]:
    for b in consonants[:6]:
        words.append(a + coeng + b + coeng + consonants[12])

# A subscript with a vowel, which is where the reordering has to put three
# things in three different places.
for a in consonants[:12]:
    for v in vowels:
        words.append(a + coeng + consonants[3] + v)

# Subscript Ro, which is drawn at the front of the syllable.
for a in consonants:
    words.append(a + coeng + ro)
    for v in vowels[:8]:
        words.append(a + coeng + ro + v)
words.append(consonants[0] + coeng + ro + coeng + consonants[5])

# Each sign on a consonant, and on a consonant that already has a vowel.
for s in signs:
    words.append(consonants[0] + s)
    words.append(consonants[0] + vowels[0] + s)
    words.append(consonants[0] + coeng + consonants[3] + s)

# The independent vowels, which take no subscript and no shifter.
for c in independents:
    words.append(c)
    words.append(c + signs[0])

# A syllable with no consonant for its marks to hang off.
words.append(vowels[0])
words.append(signs[0])
words.append(coeng + consonants[0])

# The join controls, which force or forbid what the model would choose.
for zw in ["‌", "‍"]:
    words.append(consonants[0] + coeng + zw + consonants[3])
    words.append(consonants[0] + zw + coeng + consonants[3])

# Real words and a sentence.
words += [
    "ភាសាខ្មែរ",
    "កម្ពុជា",
    "សួស្តី",
    "អរគុណ",
    "ខ្ញុំ",
    "ប្រទេស",
    "សៀមរាប",
    "ភ្នំពេញ",
    "ព្រះរាជាណាចក្រកម្ពុជា",
]

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "khmer.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
