#!/usr/bin/env python3
"""
Extract the example code of Adobe's PDF Reference, sixth edition, version 1.7.

Unlike main.py and main17.py, which read the ISO specifications and cannot be
redistributed, this writes a fixture that is committed
(testdata/pdfref17_examples.json). Section 1.4 of the Reference, "Intellectual
Property", gives everyone permission to copy, modify and distribute any example
code in it, to the extent necessary to implement PDF; THIRD_PARTY_NOTICES
quotes the grant. It covers the code and nothing else, so the fixture holds
only that: no caption, no title, no prose. Each example records where it is
(its number and page) so that a reader can find it.

The Reference sets each example under a bold "Example N.M" caption, in Myriad
Pro at 13 points, a face and size its prose (Minion Pro), headings and tables
do not use. An example is the run of text in that face after its caption, up to
the first text in any other, page headers aside. pdftohtml -xml says which text
is in which face and where it sits; pdftotext -raw has the better spacing (where
pdftohtml reads "( Gold )", -raw reads "(Gold)", as the ISO edition prints it).
So the lines come from -raw, and pdftohtml says where an example starts and
ends, which -raw lines are its own, and how far each line is indented. A line
that cannot be matched between the two stops the extraction rather than being
guessed at.

What is left out, and said on stderr: the prose set among the code in four
examples (see PROSE), and one example with no code in it at all (3.17, an
outline of a name tree). Minus signs set as U+2212 are written "-".

The PDF is pinned by its SHA-256: a different file is refused. It is not
committed; pass a local copy with --pdf, or let this download it.

Needs pdftohtml and pdftotext (poppler).

    python3 cmd/extract_spec_examples/pdfref17.py [--pdf pdfreference1.7old.pdf] \\
        > testdata/pdfref17_examples.json

Output: a JSON array of examples, each with its example number, printed page,
a "source" naming both, the code, and the classification main17.py gives it
(pdf_syntax or pdf_fragment).
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.request
import xml.etree.ElementTree as ET

sys.dont_write_bytecode = True  # no __pycache__ beside the scripts
from main17 import classify_example  # noqa: E402

URL = "https://opensource.adobe.com/dc-acrobat-sdk-docs/pdfstandards/pdfreference1.7old.pdf"
SHA256 = "4aa598e7e2cc88867565fae793db38c140a10ca2b5551dccdb45e2124282ce29"
TITLE = "PDF Reference, sixth edition, version 1.7 (Adobe, November 2006)"

# The examples' face and size, as pdftohtml names them (the subset prefix
# stripped). Italic is the same face's placeholders (alternateSpace,
# tintTransform); Symbol and Zapf Dingbats are glyphs set inside the code
# (minus signs, mostly).
CODE_FACES = {"MyriadPro", "MyriadPro-It", "Symbol", "ZapfDingbats"}
CODE_SIZE = 13
# Running headers (chapter or section, title, page number) sit above this, in
# pdftohtml's coordinates; nothing else does.
HEADER_BOTTOM = 75
# Text this close vertically is on one line.
SAME_LINE = 4
# pdftohtml's units per space of indentation: the examples step their indents
# by 18 units, which this makes four spaces.
INDENT_UNIT = 4.5

# Prose: three or more words of letters, maybe ending in a colon. Two kinds of
# it are set among the code: a note beside a line of code, not after a %
# (Examples 8.20 and 8.21), and a line of its own ending in a colon that
# introduces the code or what a viewer shows of it (4.37 and 4.38). Only those
# two placings are left out: XML text inside a string (8.15) and the rest of
# a comment (8.22) are letters too.
PROSE = re.compile(r"[A-Za-z]+(?: [A-Za-z]+){2,}:?")
CAPTION = re.compile(r"Example ([0-9]+|[A-Z])\.([0-9]+)(?:\s+(.*))?")


def fail(msg):
    print(f"pdfref17.py: {msg}", file=sys.stderr)
    sys.exit(1)


def obtain(pdf, workdir):
    """Return the path of the pinned PDF: pdf if given, else a download."""
    if pdf is None:
        pdf = os.path.join(workdir, "pdfreference1.7old.pdf")
        print(f"downloading {URL}", file=sys.stderr)
        with urllib.request.urlopen(URL) as r, open(pdf, "wb") as f:
            while chunk := r.read(1 << 20):
                f.write(chunk)
    h = hashlib.sha256()
    with open(pdf, "rb") as f:
        while chunk := f.read(1 << 20):
            h.update(chunk)
    if h.hexdigest() != SHA256:
        fail(f"{pdf} has SHA-256 {h.hexdigest()}, not the pinned {SHA256}")
    return pdf


def squeeze(s):
    """Text without white space: what pdftohtml and pdftotext agree on."""
    return re.sub(r"\s+", "", normalize(s))


def normalize(s):
    """The Reference sets minus signs as U+2212 and some spaces as U+00A0."""
    return s.replace("−", "-").replace(" ", " ")


def read_items(xml_path):
    """Every text run, in document order: (page, top, left, face, size, bold, text)."""
    root = ET.parse(xml_path).getroot()
    fonts = {}
    items = []
    for page in root.iter("page"):
        number = int(page.get("number"))
        for fs in page.iter("fontspec"):
            fonts[fs.get("id")] = (fs.get("family").split("+")[-1], int(fs.get("size")))
        for e in page.iter("text"):
            face, size = fonts[e.get("font")]
            items.append((number, int(e.get("top")), int(e.get("left")), face, size,
                          e.find("b") is not None, "".join(e.itertext())))
    return items


def printed_page(items, number):
    """The page number printed in a page's running header."""
    for p, top, _, _, _, bold, text in items:
        if p == number and top < HEADER_BOTTOM and bold and text.strip().isdigit():
            return int(text.strip())
    fail(f"page {number} has no printed page number in its header")


def find_blocks(items):
    """Each example: its number, its caption (squeezed), the caption's page, and
    its code's text runs."""
    blocks = []
    i = 0
    while i < len(items):
        page, _, _, face, size, bold, text = items[i]
        m = CAPTION.fullmatch(text.strip())
        if not (m and bold and face == "MyriadPro" and size == CODE_SIZE):
            i += 1
            continue
        number = f"{m.group(1)}.{m.group(2)}"
        caption = squeeze(text)
        body = []
        last = items[i]  # the caption, then the last line of code
        j = i + 1
        while j < len(items):
            x = items[j]
            if x[1] < HEADER_BOTTOM or (x[3] == "Symbol" and not x[6].strip()):
                j += 1  # a running header, or a space set in Symbol
                continue
            if x[0] == last[0] and x[1] < last[1] - SAME_LINE:
                # Above the code on its own page: a figure the page draws
                # after the example (Example G.11 continues past one).
                j += 1
                continue
            if x[3] in CODE_FACES and x[4] == CODE_SIZE and not x[5]:
                body.append(x)
                last = x
                j += 1
                continue
            break
        if not body:
            fail(f"Example {number} on page {page} has no code after its caption")
        blocks.append((number, caption, page, body))
        i = j
    return blocks


def visual_lines(body):
    """The code's text runs grouped into lines: (page, top, left, squeezed,
    note), where note is the squeezed text of a note set beside the code, or "".

    A glyph set in Symbol sits a few units higher than the text around it, so
    runs are grouped by height first and read left to right after."""
    groups = []
    for run in sorted(body, key=lambda x: (x[0], x[1])):
        if groups and groups[-1][0][0] == run[0] and abs(groups[-1][0][1] - run[1]) <= SAME_LINE:
            groups[-1].append(run)
        else:
            groups.append([run])
    lines = []
    for g in groups:
        g = sorted((x for x in g if x[6].strip()), key=lambda x: x[2])
        if not g:
            continue
        note = ""
        # Not when it follows a %: then it is the rest of a comment (8.22).
        if (len(g) > 1 and PROSE.fullmatch(normalize(g[-1][6]).strip())
                and "%" not in "".join(x[6] for x in g[:-1])):
            note = squeeze(g[-1][6])
        lines.append((g[0][0], g[0][1], g[0][2], "".join(squeeze(x[6]) for x in g), note))
    return lines


def raw_pages(pdf, workdir):
    """pdftotext -raw's lines, page by page (index 0 is page 1)."""
    out = os.path.join(workdir, "raw.txt")
    subprocess.run(["pdftotext", "-raw", pdf, out], check=True)
    with open(out, encoding="utf-8") as f:
        return [p.split("\n") for p in f.read().split("\f")]


def code_of(number, caption, caption_page, lines, items, pages):
    """The example's lines as -raw reads them: (left, text), notes cut off.

    Each line pdftohtml sees must be the next line -raw has, blank lines and
    running headers aside, letter for letter once white space is dropped."""
    headers = {}
    for p, top, _, _, _, _, text in items:
        if top < HEADER_BOTTOM:
            headers.setdefault(p, set()).add(squeeze(text))

    # Start on the line after the caption.
    page = caption_page
    raw = pages[page - 1]
    at = next((k + 1 for k, r in enumerate(raw) if squeeze(r) == caption), None)
    if at is None:
        fail(f"pdftotext -raw has no caption for Example {number} on page {page}")

    out = []
    for lpage, _, left, want, note in lines:
        while page < lpage:
            page += 1
            raw, at = pages[page - 1], 0
        while True:
            if at >= len(raw):
                fail(f"Example {number}: page {page} ran out before the line {want!r}")
            r = normalize(raw[at]).strip()
            at += 1
            s = squeeze(r)
            if s == want:
                break
            if s and s not in headers.get(page, ()):  # not a blank, nor a running header
                fail(f"Example {number}, page {page}: pdftotext -raw reads {r!r} "
                     f"where pdftohtml has the line {want!r}")
        if note:
            # Cut the note off the end: as many letters as it has.
            k, n = len(r), len(note)
            while n:
                k -= 1
                if not r[k].isspace():
                    n -= 1
            if squeeze(r[k:]) != note:
                fail(f"Example {number}: the note {note!r} is not at the end of {r!r}")
            print(f"Example {number}: left out the note {r[k:]!r}", file=sys.stderr)
            r = r[:k].rstrip()
        out.append((left, r))
    return out


def code_only(number, lines):
    """The lines without the sentences that introduce the code or what a viewer
    shows of it, each indented as it is set.

    Two examples (4.37 and 4.38) open with "Given the following PDF objects:"
    and close with "A PDF viewer should display ... as follows:" and the
    display. A line that is a sentence ending in a colon is left out; after the
    first line, so is everything that follows it."""
    kept = []
    for k, (left, text) in enumerate(lines):
        if PROSE.fullmatch(text) and text.endswith(":"):
            rest = lines[k:] if k > 0 and kept else lines[k:k + 1]
            for _, t in rest:
                print(f"Example {number}: left out {t!r}", file=sys.stderr)
            if k > 0 and kept:
                break
            continue
        kept.append((left, text))
    if not kept:
        fail(f"Example {number} has no code once its prose is left out")
    base = min(left for left, _ in kept)
    return "\n".join(" " * round((left - base) / INDENT_UNIT) + text for left, text in kept)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--pdf", help="a local copy of the Reference (else it is downloaded)")
    args = ap.parse_args()

    with tempfile.TemporaryDirectory() as workdir:
        pdf = obtain(args.pdf, workdir)
        subprocess.run(["pdftohtml", "-xml", "-i", "-q", pdf, os.path.join(workdir, "ref")], check=True)
        items = read_items(os.path.join(workdir, "ref.xml"))
        pages = raw_pages(pdf, workdir)

        examples = []
        for number, caption, page, body in find_blocks(items):
            content = code_only(number, code_of(number, caption, page, visual_lines(body), items, pages))
            printed = printed_page(items, page)
            ex = {
                "example": number,
                "page": printed,
                "source": f"{TITLE}, Example {number}, page {printed}",
                "content": content,
            }
            ex["type"] = classify_example(ex)
            if ex["type"] == "text":
                # Nothing of PDF in it (3.17, an outline of a name tree): an
                # illustration, not code, so not what the grant covers.
                print(f"Example {number}: left out, it is not code", file=sys.stderr)
                continue
            examples.append(ex)

    counts = {}
    for ex in examples:
        counts[ex["type"]] = counts.get(ex["type"], 0) + 1
    print(f"{len(examples)} examples: " + ", ".join(f"{n} {t}" for t, n in sorted(counts.items())),
          file=sys.stderr)
    json.dump(examples, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
