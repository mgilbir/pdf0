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
for m in marks:
    words.append(base + m)
    words.append(base + m + base)
    for a in letters[:12]:
        words.append(a + m)
words.append(base + "َّ")   # shadda then fatha, the ordinary pair
words.append(base + "َّ")   # written the other way round
words.append(base + "ٌّ")   # shadda with a tanween
words.append(lam + "َّ" + "ه")

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

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
here = os.path.dirname(os.path.abspath(__file__))
open(os.path.join(here, "arabic.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
