// Package simplefont holds what ISO 32000 says about simple fonts that does
// not belong to any one font program: the standard Latin encodings a simple
// font's /Encoding names (Annex D.2), the standard Latin and Symbol character
// sets (Annex D.2 and D.5, the repertoires ISO 19005-1 6.3.8 lets a font use
// without a ToUnicode CMap), and the rule by which a character code selects a
// glyph of a simple TrueType font (9.6.6.4).
//
// Text extraction, the PDF/A font rules and the font writer all read them, so
// they are one public package rather than three copies. The font programs they
// are applied to are read by github.com/mgilbir/forme/font, whose Program this
// package takes.
//
// # Provenance
//
// TrueTypeGlyph, IsStandardLatin and IsSymbolSet were forme's font.TrueTypeGID,
// font.StandardLatinName and font.SymbolSetNames. forme took them out of its
// API (forme f9687eb, audit C208) and then deleted them (forme f016e258c), as
// code its engine never calls: they are PDF's rules, not a font engine's. They
// are ported from forme 067d8c8 (f016e258c's parent), the last version that
// had them, with their tests; forme's changes to them after v0.3.0 were names
// and comments only. forme is MIT-licensed, by the same author as pdf0. The
// encoding tables are still forme's: this package reads them through forme's
// accessors, once, and serves them read-only.
package simplefont
