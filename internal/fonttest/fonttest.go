// Package fonttest builds font-program fixtures for tests in packages that
// cannot share a _test.go file: the font rules live in pdfa, the determinism
// harness lives in the root package, and both need the same input. Keeping one
// builder here stops the two copies from drifting apart.
package fonttest

import (
	"encoding/binary"
	"strings"
)

// Type1Program builds a minimal Type 1 font program defining exactly the named
// glyphs, in the eexec-encrypted form parseType1 expects. Only the glyph names
// matter to the /CharSet rule, so the charstrings are filler.
func Type1Program(names []string) []byte {
	var priv strings.Builder
	// lenIV 0 keeps the filler charstrings from needing a decryption prefix.
	priv.WriteString("dup /Private 8 dict dup begin\n/lenIV 0 def\n")
	// No dict count after /CharStrings: parseType1 treats a name followed by a
	// number as a charstring entry, so a count would register "CharStrings"
	// itself as a glyph.
	priv.WriteString("2 index /CharStrings dict dup begin\n")
	for _, n := range names {
		// "/name len RD <len bytes> ND"
		priv.WriteString("/" + n + " 1 RD \x8b ND\n")
	}
	priv.WriteString("end\nend\nmark currentfile closefile\n")

	// eexec encryption is the inverse of eexecDecrypt(data, 55665, 4): four
	// leading pad bytes are consumed by the decryptor's discard.
	plain := append([]byte("pad!"), priv.String()...)
	var r uint16 = 55665
	const c1, c2 = 52845, 22719
	enc := make([]byte, 0, len(plain))
	for _, p := range plain {
		c := p ^ byte(r>>8)
		r = (uint16(c)+r)*c1 + c2
		enc = append(enc, c)
	}
	return append([]byte("%!PS-AdobeFont-1.0\n/FontMatrix [0.001 0 0 0.001 0 0] readonly def\ncurrentfile eexec\n"), enc...)
}

func CmapFormat4(segs [][3]int) []byte {
	segX2 := len(segs) * 2
	b := make([]byte, 16+4*segX2)
	put16 := func(off, v int) { b[off] = byte(v >> 8); b[off+1] = byte(v) }
	put16(0, 4)      // format
	put16(2, len(b)) // length
	put16(6, segX2)  // segCountX2
	endBase := 14
	startBase := endBase + segX2 + 2
	deltaBase := startBase + segX2
	rangeBase := deltaBase + segX2
	for i, seg := range segs {
		put16(startBase+2*i, seg[0])
		put16(endBase+2*i, seg[1])
		put16(deltaBase+2*i, seg[2]&0xFFFF)
		put16(rangeBase+2*i, 0)
	}
	return b
}

func CmapFormat12(groups [][3]uint32) []byte {
	b := make([]byte, 16+12*len(groups))
	b[1] = 12                                               // format
	binary.BigEndian.PutUint32(b[4:], uint32(len(b)))       // length
	binary.BigEndian.PutUint32(b[12:], uint32(len(groups))) // nGroups
	for i, g := range groups {
		p := 16 + 12*i
		binary.BigEndian.PutUint32(b[p:], g[0])
		binary.BigEndian.PutUint32(b[p+4:], g[1])
		binary.BigEndian.PutUint32(b[p+8:], g[2])
	}
	return b
}

// CmapSub is one cmap subtable of a synthetic font: its platform and encoding
// IDs and its bytes.
type CmapSub struct {
	Plat, Enc int
	Data      []byte
}

func SFNTWithCmapSubtables(subs []CmapSub) []byte {
	cmap := make([]byte, 4+8*len(subs))
	binary.BigEndian.PutUint16(cmap[2:], uint16(len(subs)))
	for i, s := range subs {
		binary.BigEndian.PutUint16(cmap[4+8*i:], uint16(s.Plat))
		binary.BigEndian.PutUint16(cmap[4+8*i+2:], uint16(s.Enc))
		binary.BigEndian.PutUint32(cmap[4+8*i+4:], uint32(len(cmap)))
		cmap = append(cmap, s.Data...)
	}
	font := make([]byte, 12+16)
	binary.BigEndian.PutUint32(font, 0x00010000) // sfnt version 1.0
	binary.BigEndian.PutUint16(font[4:], 1)      // numTables
	copy(font[12:], "cmap")                      // tag
	binary.BigEndian.PutUint32(font[12+8:], 28)  // offset
	binary.BigEndian.PutUint32(font[12+12:], uint32(len(cmap)))
	return append(font, cmap...)
}
