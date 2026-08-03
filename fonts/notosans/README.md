# Noto Sans

`NotoSans-Variable.ttf` is bundled so that this module can set text without a
caller having to find a typeface first. A conforming PDF/A must embed every font
it shows, so "use the font the reader already has" is not an option for anything
that must conform — and until this was here, the only way to produce a
conforming document with text on it was to supply your own file.

## What this is

| | |
|---|---|
| Family | Noto Sans |
| Build | Google Fonts, variable (`wdth`, `wght`); default instance is Regular |
| Upstream filename | `NotoSans[wdth,wght].ttf` — renamed here only because `[` and `]` are glob metacharacters in a `//go:embed` pattern |
| Source | `google/fonts`, [`ofl/notosans`](https://github.com/google/fonts/tree/main/ofl/notosans) — the repository behind <https://fonts.google.com/noto/specimen/Noto+Sans> |
| Licence | SIL Open Font License, Version 1.1 — see [OFL.txt](OFL.txt) |
| Copyright | Copyright 2022 The Noto Project Authors |
| Coverage | Latin, Greek, Cyrillic, **Devanagari** — 4515 glyphs |
| Shaping | GSUB/GPOS scripts `DFLT cyrl deva dev2 grek latn` |
| `sha256` (ttf) | `bfb7bb691513f12e734dc346c03a03f784912432d7e3fa8e56efcf906fe86b3d` |
| `sha256` (OFL) | `cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a` |

The file is byte-for-byte the upstream release; only its name differs.

## Why the variable font

This originally bundled `NotoSans-Regular.ttf` from
[`notofonts/latin-greek-cyrillic`](https://github.com/notofonts/latin-greek-cyrillic),
on the reasoning that a variable font embedded in a PDF is not what a reader
expects. The conclusion was wrong twice over.

That upstream is a *narrower project* than what Google Fonts ships as "Noto
Sans". Noto is a family of per-script fonts, and the Google Fonts build merges
several of them: its `METADATA.pb` lists `devanagari` among the subsets, and the
file carries all 128 Devanagari code points and both the `deva` and `dev2`
shaping tags. The per-script upstream carries none of them — a check for
Devanagari coverage returned 0/128.

And nothing is embedded whole. Subsetting keeps the static tables and drops
`fvar`, `gvar`, `avar`, `HVAR`, `MVAR` and `STAT`; in a variable font the `glyf`
outlines *are* the default instance. So what reaches a document is an ordinary
static font at the default weight — verified metrically identical to the
separately-published static Regular, glyph for glyph, and 108 kB after
subsetting a page of mixed Latin and Devanagari.

The cost is the repository: 2 MB committed rather than 621 kB. What a document
carries is unchanged.

## The licence, and what it means here

The SIL Open Font License 1.1 permits use, study, modification and
redistribution, bundled or sold with other software. Its conditions are that the
copyright notice and licence travel with the font, that it is not sold on its
own, and that a modified version does not use a Reserved Font Name.

**Noto Sans declares no Reserved Font Name.** The copyright line carries no
"with Reserved Font Name" clause — the only mention of the term in `OFL.txt` is
the licence's own definition of it. That matters here because subsetting is a
modification: with an RFN, an embedded subset would have to be renamed. It has
none, so the subsets this module writes keep the family name, under the
six-letter tag ISO 32000-2 9.6.4 requires (`ABCDEF+NotoSans`).

The Google Fonts build carries the same copyright line and the same licence text
as the per-script upstream — both verified identical to the committed `OFL.txt`.

**A PDF made with this font is not covered by the licence.** OFL 1.1 says so in
as many words: "The requirement for fonts to remain under this license does not
apply to any document created using the Font Software." A document that embeds a
subset is such a document. Nothing about using this module puts the OFL onto
what you produce with it.

"Noto" is a trademark of Google LLC. Naming the family in a `/BaseFont` entry,
which is what embedding does, is how a PDF identifies the face it carries.

## Keeping it up to date

There is no `make` target: the file is committed, not fetched, because a build
that reaches the network to find a font is a build that fails on a train. To
move to a newer release, replace both files from the source above, update the
version, coverage and checksums in this table, and run the tests —
`TestBundledFontIsTheFileWeDocumented` checks the checksum and
`TestBundledFontCoversTheScriptsWeClaim` checks the coverage, so a swap that
forgets this table fails rather than drifts.
