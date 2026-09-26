package core

import (
	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/object"
)

// CIDGlyphMap is how a CIDFont's CIDs select glyphs in its program, read once
// per font.
//
// A CIDFontType2 selects them through its CIDToGIDMap (ISO 32000-1 9.7.4.2,
// Table 117): /Identity, or absent, makes the glyph index the CID; a stream
// holds the glyph index of CID c in bytes 2c and 2c+1. A CIDFontType0 has no
// map: its CFF program is keyed by the CID itself (a CID-keyed charset, or
// GID == CID for a font that is not CID-keyed), so its "glyph" is the CID.
//
// Every question about a shown CID — does its glyph exist, is its outline
// empty, is its width what the dictionary says, is it listed in /CIDSet — is
// about the same glyph, so every checker resolves it here. PDF/A's width
// check used to map and its existence and emptiness checks did not, and
// PDF/UA's CIDSet check declined every stream map (audit 2026-09-22 C68).
type CIDGlyphMap struct {
	type2 bool
	// For a CIDFontType2 with a stream map: that it has one, whether pdf0
	// read it, and the map. A map pdf0 did not read names no glyph.
	stream   bool
	readable bool
	data     []byte
}

// LoadCIDGlyphMap reads a descendant font's glyph selection. A CIDToGIDMap
// name other than /Identity, or a value of the wrong type, is its own finding
// (each validator's CIDToGIDMap rule) and is read as Identity here, the only
// selection the font could mean.
func LoadCIDGlyphMap(doc View, desc *object.Dictionary, cidSub object.Name) CIDGlyphMap {
	m := CIDGlyphMap{type2: cidSub == "CIDFontType2"}
	if !m.type2 {
		return m
	}
	if s, ok := doc.Resolve(desc.Get("CIDToGIDMap")).(*object.Stream); ok {
		data, r := doc.Content(s)
		// A map pdf0 declined to decode names no glyph (Glyph reports !ok),
		// and the producer recorded why.
		m.stream, m.readable, m.data = true, r == ReasonOK, data
	}
	return m
}

// Stream reports whether the map is a CIDToGIDMap stream, and Readable
// whether pdf0 read it. A font without a stream map selects glyph c for CID c.
func (m CIDGlyphMap) Stream() bool   { return m.stream }
func (m CIDGlyphMap) Readable() bool { return !m.stream || m.readable }

// StreamCIDs is the number of CIDs a readable stream map has an entry for:
// CIDs 0 to StreamCIDs()-1. Every CID past it selects glyph 0.
func (m CIDGlyphMap) StreamCIDs() int {
	if !m.stream || !m.readable {
		return 0
	}
	return len(m.data) / 2
}

// Glyph returns the key a CID's glyph is stored under in the program: its
// glyph index for a CIDFontType2, the CID for a CIDFontType0. ok is false when
// the map could not be read and no glyph can be named: guessing Identity would
// judge some other glyph. A CID past the end of a stream map has no entry, and
// selects glyph 0 — no glyph.
func (m CIDGlyphMap) Glyph(cid int) (key int, ok bool) {
	if !m.stream {
		return cid, true
	}
	if !m.readable {
		return 0, false
	}
	if cid >= 0 && 2*cid+1 < len(m.data) {
		return int(m.data[2*cid])<<8 | int(m.data[2*cid+1]), true
	}
	return 0, true
}

// Width returns the advance of the glyph a CID selected (key, from Glyph) in
// the embedded CIDFont program.
func (m CIDGlyphMap) Width(fp *font.Program, key int) (float64, bool) {
	if m.type2 {
		if key < 0 || key >= len(fp.WidthByGID) {
			return 0, false
		}
		return fp.WidthByGID[key], true
	}
	// CIDFontType0 (CFF): CID-keyed by CID, or GID==CID for non-CID CFF.
	if fp.WidthByCID != nil {
		w, ok := fp.WidthByCID[key]
		return w, ok
	}
	if key >= 0 && key < len(fp.WidthByGID) {
		return fp.WidthByGID[key], true
	}
	return 0, false
}

// Exists reports whether the glyph a CID selected is in the program. Glyph 0
// is .notdef, which is no glyph of the font's own.
func (m CIDGlyphMap) Exists(fp *font.Program, key int) bool {
	if m.type2 {
		if key <= 0 || key >= fp.NumGlyphs {
			return false
		}
		if fp.GlyphPresent != nil {
			return fp.GlyphPresent[key]
		}
		return true
	}
	if fp.CIDGIDs != nil {
		return fp.CIDGIDs[key]
	}
	return key > 0 && key < fp.NumGlyphs
}

// Empty reports whether the glyph a CID selected is a TrueType glyph with no
// outline. Only a CIDFontType2's glyf table can say so.
func (m CIDGlyphMap) Empty(fp *font.Program, key int) bool {
	return m.type2 && fp.GlyphNonEmpty != nil && key >= 0 && key < len(fp.GlyphNonEmpty) && !fp.GlyphNonEmpty[key]
}
