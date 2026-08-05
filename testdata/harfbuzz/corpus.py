# Generates corpus.txt: the strings the HarfBuzz comparison shapes.
#
# It is weighted towards the places shaping decides something — the letter
# pairs that kern, the marks that attach, the Devanagari conjunct grid — rather
# than towards realistic prose, because prose exercises one path many times and
# a grid exercises many paths once.
#
#   python3 testdata/harfbuzz/corpus.py   (writes corpus.txt beside it)
import itertools
import os

words = []

# Every ordered pair of Latin letters, plus the punctuation that kerns hardest.
latin = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
edgy = "AVWTYLFPJvwyf.,'\"-()[]"
for a, b in itertools.product(edgy, edgy):
    words.append(a + b)
for a in latin:
    for b in "AVWTYoyv.,":
        words.append(a + b)

# Accented Latin: every base with every common combining mark, and stacked.
marks = ["̀", "́", "̂", "̃", "̈", "̊", "̧", "̣", "̛"]
for base in "aeiounycsgzAEIOUNYCSGZ":
    for m in marks:
        words.append(base + m)
    words.append(base + "̣́")
    words.append(base + "̈́")

# Three marks on one letter, over the whole combining-diacritical block.
#
# The nine above are the ones a European language writes, and every one of them
# is in the font's mark-to-mark tables, so stacking always found something and
# the grid never asked what happens when it does not. The block also holds the
# combining *letters* — U+0363 and up — which nothing stacks on. A third mark
# written after one of those must stay where the base put it rather than
# climbing over it onto the first.
allmarks = [chr(c) for c in range(0x0300, 0x0370)]
for base in "aoAOЇα":
    for m in allmarks:
        words.append(base + "̊" + m + "̑")
        words.append(base + m + "̑")

# A second nukta, after a consonant and after a vowel sign.
#
# A consonant admits two; a vowel sign admits one, and the second begins a
# broken syllable of its own. Nothing here wrote two of them anywhere.
for c in [chr(x) for x in range(0x0915, 0x0930)]:
    words.append(c + "\u093C\u093C")
    words.append(c + "\u093F\u093C")
    words.append(c + "\u093F\u093C\u093C")
    words.append(c + "\u094E\u093C\u093C")
for v in [chr(x) for x in range(0x0904, 0x0915)]:
    words.append(v + "\u094E\u093C\u093C")

# Two vowel signs written before the same consonant.
#
# Both U+093F and U+094E are stored after the letter and drawn before it, and
# the second is drawn furthest out — in front of the first, not behind it. The
# grids above write at most one sign of a kind per letter, so nothing in them
# asked which way round two of them go.
for c in [chr(x) for x in range(0x0915, 0x093A)]:
    words.append(c + "\u093F\u094E")
    words.append(c + "\u094E\u093F")
    words.append(c + "\u093F\u094E\u0902")
    words.append(c + "\u094D" + c + "\u093F\u094E")

# Greek and Cyrillic, pairwise over the alphabet — every letter against every
# letter, in both cases.
#
# It used to pair each letter with the first fourteen, which are all lower case,
# and "pairwise" was already what this comment claimed. Kerning is a property of
# a pair and of nothing else, so a grid with holes in it is a test with holes in
# it: be+TE is kerned by two subtables of one lookup that disagree, and only the
# first should count. Nothing here reached that pair, and the fuzzer did.
greek = "αβγδεζηθικλμνξοπρστυφχψωΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡΣΤΥΦΧΨΩ"
cyr = "абвгдеёжзийклмнопрстуфхцчшщъыьэюяАБВГДЕЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯ"
for alpha in (greek, cyr):
    for a in alpha:
        for b in alpha:
            words.append(a + b)

# Devanagari: the full conjunct grid, vowel signs, and three-level clusters.
cons = [chr(c) for c in range(0x0915, 0x093A)]
virama = "्"
signs = [chr(c) for c in range(0x093E, 0x094D)] + ["ं", "ँ", "ः", "़"]
for a in cons:
    for b in cons:
        words.append(a + virama + b)
for a in cons:
    for v in signs:
        words.append(a + v)
        words.append(a + virama + cons[0] + v)
# Reph and the explicit half form.
for b in cons:
    words.append("र" + virama + b)
    words.append(b + virama + "र")
    words.append(b + virama + "‍" + cons[3])
    words.append(b + virama + "‌" + cons[3])

# Ligatures formed across a mark the lookup steps over. The mark is not part of
# the rule and must survive it; "f + dot below + fi" makes the ffi ligature and
# keeps the dot, which is the whole case in four characters.
for lig in ["ffi", "ffl", "ff", "fi", "fl"]:
    for at in range(1, len(lig)):
        for mark in ["\u0323", "\u0301", "\u0308"]:
            words.append(lig[:at] + mark + lig[at:])
# The same in Devanagari, where a nukta stands between a consonant and its
# virama and the conjunct forms across it.
for a in cons[:12]:
    for b in cons[:8]:
        words.append(a + "\u093C" + virama + b)
        words.append(a + virama + "\u093C" + b)

# The characters nothing is drawn for. Every category of
# Default_Ignorable_Code_Point, between letters, inside a word, around a word,
# and alone — plus the Hangul fillers, which are default-ignorable and are
# nevertheless drawn.
#
# The controls that force a right-to-left direction — ALM, RLM, RLE, RLO, RLI —
# are deliberately absent. HarfBuzz performs no bidirectional algorithm at all:
# its caller is required to run UAX #9 and hand it runs of one direction, and
# hb_buffer_guess_segment_properties only picks a direction from the first
# character with a script. So for "RLO a b c" HarfBuzz answers "abc" and this
# package answers "cba", and this package is right — an override means what it
# says. That is a difference in what the two are *for*, not in shaping, and the
# right oracle for it is Unicode's own BidiTest and BidiCharacterTest, which
# bidi_conformance_test.go runs in full.
ignorables = [
    "\u00AD",   # soft hyphen: what HTML writes as &shy;
    "\u034F",   # combining grapheme joiner: blocks composition
    "\u200B",   # zero width space
    "\u200E",   # left-to-right mark
    "\u202A", "\u202C", "\u202D",  # an embedding, its terminator, an override
    "\u2060",   # word joiner
    "\u2064",   # invisible plus
    "\u2066", "\u2069",  # a left-to-right isolate and its terminator
    "\uFE00", "\uFE0F",  # variation selectors
    "\uFEFF",   # zero width no-break space
    "\U0001D173",  # musical begin beam
    "\U000E0041",  # tag letter A
]
for c in ignorables:
    words += [c, "x" + c + "y", "un" + c + "breakable", c + "abc", "abc" + c,
              "\u0915" + c + "\u094D\u0937", "a\u0301" + c + "b"]
words.append("".join(ignorables))
words.append("un" + "\u00AD".join(["break", "able", "ness"]))
fillers = ["\u115F", "\u1160", "\u3164", "\uFFA0"]
for c in fillers:
    words += [c, "x" + c + "y", "\u1100" + c + "\u11A8"]

# Real sentences.
words += [
    "The quick brown fox jumps over the lazy dog.",
    "Waltz, bad nymph, for quick jigs vex.",
    "Sphinx of black quartz, judge my vow!",
    "Portez ce vieux whisky au juge blond qui fume.",
    "Voix ambiguë d'un cœur qui au zéphyr préfère les jattes de kiwis.",
    "Falsches Üben von Xylophonmusik quält jeden größeren Zwerg.",
    "Съешь же ещё этих мягких французских булок, да выпей чаю.",
    "Ξεσκεπάζω την ψυχοφθόρα βδελυγμία.",
    "नमस्ते दुनिया, यह एक परीक्षण वाक्य है।",
    "हिन्दी भारत की राजभाषा है और देवनागरी लिपि में लिखी जाती है।",
    "संस्कृत भाषा अत्यन्त प्राचीन है।",
    "Mixed देवनागरी and Latin and Ελληνικά and Кириллица in one line.",
]

seen, out = set(), []
for w in words:
    if w and "\n" not in w and w not in seen:
        seen.add(w)
        out.append(w)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "corpus.txt"), "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(out), "strings")
