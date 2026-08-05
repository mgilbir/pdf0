# Reads what CoreText answered and says which of the other two it agrees with.
#
# The comparison is absolute position, not the numbers each engine prints: they
# express the same layout differently — CoreText says where a glyph sits, the
# others say how far it is displaced from a pen that the advances move. And
# CoreText's line origin is not theirs for right-to-left text, so each line is
# normalised on its last glyph before being compared. What survives both is the
# shape a reader sees, which is the only thing worth comparing.
#
#   ./shape <font> < batch-tibetan.txt > out.txt
#   python3 compare.py batch-tibetan out.txt
#
# Stock Python; no uharfbuzz needed, because what the other two answer is
# recorded in the .expected file beside the input.
import sys


def coretext(line):
    """CoreText's output as absolute positions: the second field is the step."""
    out, pos = [], 0
    for field in line.split():
        if field in ("DOTTED", "SUBSTITUTED-FONT"):
            continue
        p = field.split(",")
        dy = int(p[3]) if len(p) > 3 else 0
        out.append((int(p[0]), pos, dy))
        pos += int(p[1])
    return out


def recorded(field):
    """A .expected column: gid@x,y already absolute."""
    out = []
    for g in field.split():
        gid, rest = g.split("@")
        x, y = rest.split(",")
        out.append((int(gid), int(x), int(y)))
    return out


def normalise(glyphs):
    """Same layout, same origin — CoreText's is not the others' for RTL."""
    if not glyphs:
        return []
    base = glyphs[-1][1]
    return [(g, x - base, y) for g, x, y in glyphs]


def main():
    stem, got = sys.argv[1], sys.argv[2]
    lines = [l.rstrip("\n") for l in open(f"{stem}.txt", encoding="utf-8") if l.strip()]
    ref = [l.rstrip("\n") for l in open(f"{stem}.expected", encoding="utf-8")]
    controls = int(ref[0].split()[-1])
    ref = [l.split("\t") for l in ref[1:]]
    ct = [l.rstrip("\n") for l in open(got, encoding="utf-8")]
    if len(ct) != len(lines):
        raise SystemExit(f"{len(ct)} lines of output for {len(lines)} of input")

    bad_control = False
    agree_pdf0 = agree_hb = neither = 0
    for i, (text, (ours, theirs), line) in enumerate(zip(lines, ref, ct)):
        C, A, B = normalise(coretext(line)), normalise(recorded(ours)), normalise(recorded(theirs))
        cps = " ".join(f"U+{ord(c):04X}" for c in text)
        if i < controls:
            if not (C == A == B):
                bad_control = True
                print(f"CONTROL FAILED  {cps}\n   coretext {C}\n   pdf0     {A}\n   harfbuzz {B}")
            continue
        if C == A and C == B:
            continue
        if C == A:
            agree_pdf0 += 1
        elif C == B:
            agree_hb += 1
            print(f"harfbuzz  {cps}")
        else:
            neither += 1
            print(f"NEITHER   {cps}\n   coretext {C}\n   pdf0     {A}\n   harfbuzz {B}")
    if bad_control:
        raise SystemExit("\na control disagreed, so the harness is measuring something "
                         "else and nothing above means anything")
    print(f"\n{agree_pdf0} agree with pdf0, {agree_hb} with harfbuzz, {neither} with neither")


if __name__ == "__main__":
    main()
