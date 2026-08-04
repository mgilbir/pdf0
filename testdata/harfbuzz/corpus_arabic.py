# Generates arabic.txt: the strings the Arabic comparison shapes.
#
# Arabic is where a shaper does the most work per character, and none of it was
# checked against anything outside this repository until now — the bundled face
# has no Arabic in it at all. What is exercised here:
#
#   - joining. Every letter takes one of four forms from what stands either side
#     of it, so every letter appears alone, at the start, in the middle and at
#     the end of a word. That is the grid, and it is most of the file.
#   - the letters that only ever join to the right, which break the word for the
#     letter after them. A dozen letters do this and getting one wrong is
#     invisible until someone reads it.
#   - lam-alef, which is a required ligature rather than an optional one, in all
#     four of its alef forms.
#   - the marks: the short vowels, sukun, shadda, the tanween, and the
#     combinations that stack two on one letter.
#   - hamza written over and under letters, which is where the mark reordering
#     rules bite.
#
#   python3 testdata/harfbuzz/corpus_arabic.py
import os
import unicodedata

words = []

# The alphabet, in the order it is recited.
letters = [
    "ا", "ب", "ت", "ث", "ج", "ح", "خ",
    "د", "ذ", "ر", "ز", "س", "ش", "ص",
    "ض", "ط", "ظ", "ع", "غ", "ف", "ق",
    "ك", "ل", "م", "ن", "ه", "و", "ي",
]
# Letters that join only to the right, so the next letter starts a new shape.
rightOnly = ["ا", "د", "ذ", "ر", "ز", "و",
             "آ", "أ", "إ", "ة", "ى"]
# Hamza carriers and the forms of alef.
alefs = ["ا", "آ", "أ", "إ"]
extra = ["ء", "ؤ", "ئ", "ة", "ى", "ـ"]

marks = [
    "ً", "ٌ", "ٍ",  # the tanween
    "َ", "ُ", "ِ",  # fatha, damma, kasra
    "ّ", "ْ",            # shadda, sukun
    "ٓ", "ٔ", "ٕ",  # madda, hamza above, hamza below
    "ٰ",                      # dagger alef
]

# Every letter alone, and the four positions each takes in a word.
base = "ب"  # beh joins both ways, so it makes the neighbours it needs
for a in letters + extra:
    words.append(a)
    words.append(a + base)          # a at the start
    words.append(base + a)          # a at the end
    words.append(base + a + base)   # a in the middle

# Every ordered pair, which is where a right-joining letter shows itself.
for a in letters:
    for b in letters:
        words.append(a + b)

# A right-joining letter in the middle of a longer word.
for a in rightOnly:
    for b in letters[:10]:
        words.append(base + a + b + base)

# Lam-alef, required, in each of its forms, alone and in a word.
lam = "ل"
for a in alefs:
    words.append(lam + a)
    words.append(base + lam + a)
    words.append(lam + a + base)
    words.append(base + lam + a + base)

# Marks: each on a letter alone, each on a letter inside a word, and stacked.
#
# On *every* letter, not the first twelve. The first twelve are the plain ones,
# whose skeleton carries at most dots above or dots below. A letter like teh
# with ring is split by 'ccmp' into a skeleton, a ring below and dots above, and
# a mark written after it has three marks in front of it — which is what asks
# whether mark-to-mark steps over the ones its lookup's mark glyph set leaves
# out. Nothing in the first twelve does.
for m in marks:
    words.append(base + m)
    words.append(base + m + base)
    for a in letters:
        words.append(a + m)
words.append(base + "َّ")   # shadda then fatha, the ordinary pair
words.append(base + "َّ")   # written the other way round
words.append(base + "ٌّ")   # shadda with a tanween
words.append(lam + "َّ" + "ه")

# Every other letter of the block, each with a mark written above it.
#
# The alphabet above is the twenty-eight, whose skeletons carry at most dots.
# The block also holds the letters other languages added — teh with ring among
# them — which 'ccmp' splits into a skeleton and two or three marks of different
# kinds. A mark written after one of those has several in front of it, and that
# is what asks whether mark-to-mark steps over the ones its lookup's mark glyph
# set leaves out. It stacks on the dots above and not on the ring below.
for a in (chr(c) for c in range(0x0620, 0x0700)):
    if unicodedata.category(a) != "Lo":
        continue
    for m in ("ْ", "َ", "ّ", "ٰ"):
        words.append(a + m)

# Hamza over and under a carrier, with a vowel: the reordering case.
for carrier in ["ا", "و", "ي"]:
    for m in ["َ", "ُ", "ِ"]:
        words.append(carrier + "ٔ" + m)
        words.append(carrier + m + "ٔ")
        words.append(carrier + "ٕ" + m)

# Real words and a sentence, so that something in the file reads as language.
words += [
    "السلام",              # as-salaam
    "عليكم",                    # alaykum
    "مرحبا",                    # marhaba
    "العربية",        # al-arabiyya
    "كتاب",                          # kitab
    "مدرسة",                    # madrasa
    "بسم الله",       # bism allah
    "الحمد لله",
    "لا إله إلا الله",
    "مُحَمَّد",  # fully vowelled
    "كَتَبَ",
    "الرَّحمَن",
]

# A character nothing is drawn for, between a letter and its mark, in a font
# that gives that character a width. Kept so the difference is pinned rather
# than merely known: CoreText agrees with this package and HarfBuzz does not.
# See fonts/harfbuzz_test.go.
words.append("".join(chr(c) for c in (0x063D, 0x061C, 0x0655)))

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "arabic.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
