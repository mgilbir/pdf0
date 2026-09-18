package core

import (
	"strings"

	"github.com/mgilbir/pdf0/object"
)

// A CMap: how a Type 0 font's character codes become CIDs.
//
// The codes in a content stream are bytes, and only the CMap says how to cut
// them up. Identity-H makes every code two bytes and every CID equal to its
// code, which is the case this module handled and the only one it handled — but
// a CJK document commonly embeds a CMap of its own, where a code may be one
// byte or two and the CID is whatever the map says. Without reading it, a
// checker cannot name a single glyph the page uses: not to ask whether the font
// has it, not to compare its width, not to notice .notdef.
//
// ISO 32000-2 9.7.6. What is implemented is the CID half — codespace ranges,
// cidrange and cidchar — because that is what turns a string into glyph
// references. The rest of the CMap grammar is PostScript and is not run.

// maxCMapEntries bounds a CMap's mappings, and maxCMapRangeSpan bounds one
// range.
//
// A CMap arrives in a document and is therefore hostile until proven otherwise.
// "<0000> <FFFFFFFF> 1" is eleven bytes asking for four billion map inserts,
// and the file that contains it costs nothing to make. The bounds are far above
// any real CMap: Adobe's largest published one, UniJIS-UCS2-HW-V, has some
// thousands of entries.
const (
	maxCMapEntries   = 1 << 16
	maxCMapRangeSpan = 1 << 16
)

// CMap maps character codes to CIDs, and knows how wide a code is.
type CMap struct {
	// codespace is the set of byte ranges a code may fall in, by length. It is
	// what decides where one code ends and the next begins, and a CMap without
	// it can decode nothing.
	codespace []codespaceRange
	// single and ranges are the mapping. A range is kept as a range rather than
	// expanded because expanding is what a hostile file would ask for.
	single map[uint32]int
	ranges []cidRange
	// identity is the built-in CMap, where the CID is the code. It is a flag
	// rather than a filled-in map for the same reason: sixty-five thousand
	// entries nobody needs.
	identity bool
}

type codespaceRange struct {
	bytes  int // 1 to 4
	lo, hi uint32
}

type cidRange struct {
	lo, hi uint32
	bytes  int
	cid    int
}

// IdentityCMap is Identity-H and Identity-V: two bytes to a code, and the CID
// is the code.
func IdentityCMap() *CMap {
	return &CMap{
		identity:  true,
		codespace: []codespaceRange{{bytes: 2, lo: 0, hi: 0xFFFF}},
	}
}

// Code is one character code from a string, and what it means.
type Code struct {
	// Value is the code itself, and Bytes how many bytes of the string it took.
	Value uint32
	Bytes int
	// CID is what the CMap maps it to, and Mapped says whether the CMap had an
	// entry at all. An unmapped code is not CID 0 — it is a code the document
	// used and the CMap does not define, which is a different fault and one a
	// caller may want to report.
	CID    int
	Mapped bool
}

// Decode cuts a string into codes.
//
// §9.7.6.2: the length of a code is decided by the codespace ranges, matching
// the shortest range whose first byte the string's first byte falls inside. A
// byte that starts no range is still consumed — one byte, unmapped — because
// stopping would silently drop the rest of a string that a reader will happily
// show.
func (c *CMap) Decode(s []byte) []Code {
	if c == nil || len(c.codespace) == 0 {
		return nil
	}
	out := make([]Code, 0, len(s))
	for i := 0; i < len(s); {
		n, v, ok := c.codeAt(s[i:])
		if !ok {
			// Not in any codespace. One byte, so that the scan advances and a
			// malformed string costs its length rather than an infinite loop.
			out = append(out, Code{Value: uint32(s[i]), Bytes: 1})
			i++
			continue
		}
		cid, mapped := c.lookup(v, n)
		out = append(out, Code{Value: v, Bytes: n, CID: cid, Mapped: mapped})
		i += n
	}
	return out
}

// codeAt reads the next code, and how many bytes it took.
//
// §9.7.6.2 decides the *length* from the first byte alone, and not from whether
// the whole code falls inside a range. The difference is the whole of mixed-
// width CJK: a Shift-JIS CMap has a one-byte space <00>-<80> and a two-byte one
// <8140>-<9FFC>, and the string 81 20 is a two-byte code — an invalid one,
// outside the range, but two bytes. Deciding by containment would call it a
// one-byte code and read the 20 as the start of the next, and every code after
// it in the string would be wrong.
//
// So the first byte picks the length, the shortest when several ranges could
// take it, and containment is a separate question the mapping answers.
func (c *CMap) codeAt(s []byte) (n int, v uint32, ok bool) {
	best := 0
	for _, r := range c.codespace {
		if r.bytes > len(s) {
			continue
		}
		lo := byte(r.lo >> uint(8*(r.bytes-1)))
		hi := byte(r.hi >> uint(8*(r.bytes-1)))
		if s[0] < lo || s[0] > hi {
			continue
		}
		if best == 0 || r.bytes < best {
			best = r.bytes
		}
	}
	if best == 0 {
		return 0, 0, false
	}
	var acc uint32
	for k := 0; k < best; k++ {
		acc = acc<<8 | uint32(s[k])
	}
	return best, acc, true
}

// lookup is the code's CID.
func (c *CMap) lookup(v uint32, n int) (int, bool) {
	if c.identity {
		return int(v), true
	}
	if cid, ok := c.single[v]; ok {
		return cid, true
	}
	for _, r := range c.ranges {
		if r.bytes == n && v >= r.lo && v <= r.hi {
			return r.cid + int(v-r.lo), true
		}
	}
	return 0, false
}

// Identity reports whether this is the built-in CMap, for a caller that has a
// shortcut for it.
func (c *CMap) Identity() bool { return c != nil && c.identity }

// LoadCMap is the CMap a Type 0 font's /Encoding names or carries, and whether
// there is one this module can read.
//
// Three shapes. Identity-H and Identity-V are built in. A stream is a CMap
// program embedded in the document, which is parsed. Any other name is one of
// Adobe's predefined CMaps — data this module does not carry, so the answer is
// no and the caller skips rather than guesses.
func LoadCMap(doc View, fontDict *object.Dictionary) (*CMap, bool) {
	switch e := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		if e == "Identity-H" || e == "Identity-V" {
			return IdentityCMap(), true
		}
		return nil, false
	case *object.Stream:
		// Through the same budgeted decode every other stream goes through: a
		// CMap is compressed like anything else, and a compression bomb in one
		// is a bomb.
		data, err := DecodeStreamData(doc.Cancel, e, doc.Limits)
		if err != nil || len(data) > doc.Limits.ContentStreamBytes {
			return nil, false
		}
		return ParseCMap(string(data))
	}
	return nil, false
}

// PredefinedCMapName is the predefined CMap a Type 0 font's /Encoding names,
// when that is a name whose data this module does not carry.
//
// It exists so that the skip can be reported rather than taken silently. Three
// checks need a code-to-CID mapping — whether every code shown has a glyph,
// whether .notdef is drawn, and whether /W agrees with the program's own
// advances — and for these fonts none of them can run.
//
// Two cases are deliberately not skips. Identity-H and Identity-V are answered
// by LoadCMap, and an embedded stream is read. A name that is not predefined at
// all is not reported here either: that is a violation of Table 118 and the
// CMap-legality rule says so, which is a stronger statement than "not checked".
func PredefinedCMapName(v View, fontDict *object.Dictionary) (string, bool) {
	name, ok := v.ResolveName(fontDict.Get("Encoding"))
	if !ok || name == "Identity-H" || name == "Identity-V" {
		return "", false
	}
	if _, known := PredefinedCMaps[string(name)]; !known {
		return "", false
	}
	return string(name), true
}

// ParseCMap reads the CID half of a CMap program.
//
// The grammar is PostScript and this is not an interpreter: it finds the
// begin/end blocks and reads the hex tokens inside them, which is what the
// ToUnicode reader beside it does and for the same reason — the operators that
// matter are declarative and everything around them is boilerplate a font tool
// wrote.
//
// A CMap that names another with usecmap is not resolved. Nearly every embedded
// one that does so names a predefined CMap, which is data this module does not
// have, so the result would be a map with holes in it that reported nothing.
// Saying no is the honest answer and the caller skips the font.
func ParseCMap(src string) (*CMap, bool) {
	if strings.Contains(src, "usecmap") {
		return nil, false
	}
	c := &CMap{single: map[uint32]int{}}
	parseCodespaces(c, src)
	if len(c.codespace) == 0 {
		// Without a codespace nothing can be cut into codes. A CMap is required
		// to have one; a file that omits it has not said how to read itself.
		return nil, false
	}
	if !parseCIDMappings(c, src) {
		return nil, false
	}
	return c, true
}

// parseCodespaces reads begincodespacerange blocks.
func parseCodespaces(c *CMap, src string) {
	eachBlock(src, "begincodespacerange", "endcodespacerange", func(body string) bool {
		for _, line := range strings.Split(body, "\n") {
			f := AngleTokens(line)
			for i := 0; i+1 < len(f); i += 2 {
				lo, nlo := hexCode(f[i])
				hi, nhi := hexCode(f[i+1])
				if nlo == 0 || nlo != nhi || lo > hi {
					continue
				}
				c.codespace = append(c.codespace, codespaceRange{bytes: nlo, lo: lo, hi: hi})
			}
		}
		return len(c.codespace) < maxCMapEntries
	})
}

// parseCIDMappings reads begincidrange and begincidchar, and reports whether it
// stayed inside its bounds.
//
// A CMap that runs past them is refused rather than truncated: a partial map
// answers "this code has no CID" for codes it simply did not reach, and a
// caller cannot tell that from a code the document really left undefined.
func parseCIDMappings(c *CMap, src string) bool {
	within := true
	eachBlock(src, "begincidrange", "endcidrange", func(body string) bool {
		for _, line := range strings.Split(body, "\n") {
			f := AngleTokens(line)
			// "<lo> <hi> cid" — the CID is a plain integer, so it is not an
			// angle token and is read from the tail of the line.
			if len(f) < 2 {
				continue
			}
			lo, nlo := hexCode(f[0])
			hi, nhi := hexCode(f[1])
			cid, ok := trailingInt(line)
			if nlo == 0 || nlo != nhi || lo > hi || !ok {
				continue
			}
			if uint64(hi-lo) >= maxCMapRangeSpan {
				within = false
				return false
			}
			c.ranges = append(c.ranges, cidRange{lo: lo, hi: hi, bytes: nlo, cid: cid})
			if len(c.ranges)+len(c.single) >= maxCMapEntries {
				within = false
				return false
			}
		}
		return true
	})
	if !within {
		return false
	}
	eachBlock(src, "begincidchar", "endcidchar", func(body string) bool {
		for _, line := range strings.Split(body, "\n") {
			f := AngleTokens(line)
			if len(f) < 1 {
				continue
			}
			code, n := hexCode(f[0])
			cid, ok := trailingInt(line)
			if n == 0 || !ok {
				continue
			}
			c.single[code] = cid
			if len(c.ranges)+len(c.single) >= maxCMapEntries {
				within = false
				return false
			}
		}
		return true
	})
	return within
}

// eachBlock calls f with the body of every begin/end pair, stopping when f
// returns false.
func eachBlock(src, begin, end string, f func(body string) bool) {
	rest := src
	for {
		b := strings.Index(rest, begin)
		if b < 0 {
			return
		}
		after := rest[b+len(begin):]
		e := strings.Index(after, end)
		if e < 0 {
			return
		}
		if !f(after[:e]) {
			return
		}
		rest = after[e+len(end):]
	}
}

// hexCode reads a <hh...> token as a code and its width in bytes.
//
// The width is the point: <00> and <0000> are different codes in a CMap, one
// byte and two, and a reader that treated both as zero would cut every string
// in the wrong places.
func hexCode(tok string) (uint32, int) {
	s := strings.TrimPrefix(strings.TrimSuffix(tok, ">"), "<")
	if len(s) == 0 || len(s)%2 != 0 || len(s) > 8 {
		return 0, 0
	}
	var v uint32
	for i := 0; i < len(s); i++ {
		d := hexDigit(s[i])
		if d < 0 {
			return 0, 0
		}
		v = v<<4 | uint32(d)
	}
	return v, len(s) / 2
}

func hexDigit(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// trailingInt reads the decimal integer after the last '>' on a line, which is
// where cidrange and cidchar put the CID.
func trailingInt(line string) (int, bool) {
	i := strings.LastIndexByte(line, '>')
	if i < 0 {
		return 0, false
	}
	s := strings.TrimSpace(line[i+1:])
	if s == "" {
		return 0, false
	}
	n := 0
	for k := 0; k < len(s); k++ {
		if s[k] < '0' || s[k] > '9' {
			if k == 0 {
				return 0, false
			}
			break
		}
		n = n*10 + int(s[k]-'0')
		if n > 1<<21 {
			// Far past any CID in any published collection, and past what a
			// glyph index can be. A number this large is a malformed file.
			return 0, false
		}
	}
	return n, true
}
