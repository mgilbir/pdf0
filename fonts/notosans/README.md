# Noto Sans

`NotoSans-Regular.ttf` is bundled so that this module can set text without a
caller having to find a typeface first. A conforming PDF/A must embed every font
it shows, so "use the font the reader already has" is not an option for anything
that must conform — and until this was here, the only way to produce a
conforming document with text on it was to supply your own file.

## What this is

| | |
|---|---|
| Family | Noto Sans |
| Style | Regular |
| Version | 2.015 |
| Upstream | <https://github.com/notofonts/latin-greek-cyrillic> |
| Also distributed as | <https://fonts.google.com/noto/specimen/Noto+Sans> (`google/fonts`, `ofl/notosans`) |
| Licence | SIL Open Font License, Version 1.1 — see [OFL.txt](OFL.txt) |
| Copyright | Copyright 2022 The Noto Project Authors |
| Coverage | Latin, Greek, Cyrillic — 3884 glyphs |
| `sha256` (ttf) | `478c558ea716033cd60c03438f628dfa75694dcf6b5f6d505a2f05fd2b4f3823` |
| `sha256` (OFL) | `cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a` |

The file is byte-for-byte the upstream release; nothing about it has been
modified. Google Fonts ships Noto Sans as a variable font
(`NotoSans[wdth,wght].ttf`); this is the static Regular instance from the same
upstream, because a variable font embedded in a PDF without being instanced
first is not what any reader expects, and this module does not instance them.
The two carry the same licence text, verified identical.

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
six-letter tag ISO 32000-2 9.6.4 requires (`ABCDEF+NotoSans-Regular`).

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
move to a newer release, replace both files from the upstream above, update the
version and checksums in this table, and run the tests — `TestBundledFontIsTheFileWeDocumented`
checks the checksum, so a swap that forgets this table fails rather than drifts.
