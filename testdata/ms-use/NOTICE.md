# Vendored Universal Shaping Engine override data

These two files are vendored, unmodified, from HarfBuzz:

  https://github.com/harfbuzz/harfbuzz/tree/main/src/ms-use

They carry the Universal Shaping Engine's corrections to two Unicode
properties — `Indic_Syllabic_Category` and `Indic_Positional_Category` — and
their own headers say what they are: *"Override values … Not derivable"*,
maintained since Unicode 7.0 by Andrew Glass, who edits the engine's
specification at Microsoft.

## Why they are here rather than derived

`cmd/genuse` derives the engine's categories from five properties Unicode
publishes, which is most of the answer and all of the answer that Unicode is in
a position to give. The remainder is not derivable by anybody: it is the
engine's own judgement about characters whose Unicode property is right for
Unicode's purposes and wrong for laying out a syllable. Two of the 198 entries
are about Javanese alone — U+A9BE PENGKAL is `Bottom_And_Right` in Unicode and
`Right` to the engine, and U+A9BF CAKRA is `Right` in Unicode and `Bottom` to
the engine — and a shaper that used Unicode's values would draw both on the
wrong side of the letter.

They are read by `cmd/genuse` when the table is regenerated, and by nothing at
runtime.

## Licence

HarfBuzz is distributed under the "Old MIT" licence, which permits
redistribution with its notice. The files are unmodified and carry their own
headers; HarfBuzz's licence follows.

    Copyright © 2010,2011,2012  Google, Inc.
    Copyright © 2012,2013  Mozilla Foundation
    ... and the other copyright holders named in HarfBuzz's COPYING.

    Permission is hereby granted, without written agreement and without
    license or royalty fees, to use, copy, modify, and distribute this
    software and its documentation for any purpose, provided that the
    above copyright notice and the following two paragraphs appear in
    all copies of this software.

    IN NO EVENT SHALL THE COPYRIGHT HOLDER BE LIABLE TO ANY PARTY FOR
    DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES
    ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS DOCUMENTATION, EVEN
    IF THE COPYRIGHT HOLDER HAS BEEN ADVISED OF THE POSSIBILITY OF SUCH
    DAMAGE.

    THE COPYRIGHT HOLDER SPECIFICALLY DISCLAIMS ANY WARRANTIES, INCLUDING,
    BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND
    FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS
    ON AN "AS IS" BASIS, AND THE COPYRIGHT HOLDER HAS NO OBLIGATION TO
    PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.
