package core

import (
	"fmt"
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
// cidrange and cidchar, and the CMap one builds on with usecmap — because that
// is what turns a string into glyph references. The program is read as a token
// stream (cmapparse.go); the rest of the grammar is PostScript and is not run.

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
	// expanded because expanding is what a hostile file would ask for. A
	// single code is keyed with its width, like a range: <00> and <0000> are
	// different codes.
	single map[cmapKey]int
	ranges []cidRange
	// notdefSingle and notdefRanges are the notdef mappings (9.7.6.3): what a
	// code the CID mappings leave unmapped stands for.
	notdefSingle map[cmapKey]int
	notdefRanges []cidRange
	// identity is the built-in CMap, where the CID is the code. It is a flag
	// rather than a filled-in map for the same reason: sixty-five thousand
	// entries nobody needs.
	identity bool
	// base is the CMap this one names with usecmap (or /UseCMap), whose
	// mappings stand wherever this one's do not (9.7.5.3).
	base *CMap
	// opaque marks a base whose data this module does not have: a predefined
	// CMap, or one that cannot be read. A code that falls through to it is
	// Unknown — neither mapped nor undefined — and unknownWhy says why, for
	// the report onUnknown makes the first time a check meets one.
	opaque     bool
	unknownWhy cmapRefusal
	onUnknown  func()
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
	// Unknown says the code falls to a CMap this one builds on whose data this
	// module does not carry. It is neither mapped nor undefined, and a check
	// must skip it; the CMap reports the skip itself (LoadCMap). When the rest
	// of a string cannot even be cut into codes without that data, one Unknown
	// code covers all of it.
	Unknown bool
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
			if c.unknownWhy.guard != "" {
				// It may be in the codespace of the CMap this one builds on,
				// which is not known, so where this code ends — and every code
				// after it — is not known either.
				out = append(out, Code{Value: uint32(s[i]), Bytes: len(s) - i, Unknown: true})
				c.noteUnknown()
				break
			}
			// Not in any codespace. One byte, so that the scan advances and a
			// malformed string costs its length rather than an infinite loop.
			out = append(out, Code{Value: uint32(s[i]), Bytes: 1})
			i++
			continue
		}
		cid, mapped, unknown := c.lookup(v, n)
		if unknown {
			c.noteUnknown()
		}
		out = append(out, Code{Value: v, Bytes: n, CID: cid, Mapped: mapped, Unknown: unknown})
		i += n
	}
	return out
}

func (c *CMap) noteUnknown() {
	if c.onUnknown != nil {
		c.onUnknown()
	}
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
//
// Except where the bytes do make a valid code: a code that lies wholly inside
// a range — every byte within that range's bounds for its position, which is
// how a codespace range is defined — is that range's length, shortest first.
// The first-byte rule is for the bytes that make no valid code. The two agree
// on every CMap whose ranges of different lengths start with different bytes,
// which is nearly all of them; they part on GB 18030 (GBK2K), whose two- and
// four-byte ranges share their first bytes and differ in the second: 81 30 81
// 30 is one four-byte code, and the first-byte rule alone would read it as two
// invalid two-byte ones.
func (c *CMap) codeAt(s []byte) (n int, v uint32, ok bool) {
	for length := 1; length <= 4 && length <= len(s); length++ {
		for _, r := range c.codespace {
			if r.bytes == length && r.contains(s[:length]) {
				return length, codeValue(s[:length]), true
			}
		}
	}
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
	return best, codeValue(s[:best]), true
}

// contains reports whether code, of r's length, lies inside r byte by byte.
func (r codespaceRange) contains(code []byte) bool {
	for k, b := range code {
		shift := uint(8 * (r.bytes - 1 - k))
		if b < byte(r.lo>>shift) || b > byte(r.hi>>shift) {
			return false
		}
	}
	return true
}

// codeValue is the big-endian value of a code of at most four bytes.
func codeValue(code []byte) uint32 {
	var acc uint32
	for _, b := range code {
		acc = acc<<8 | uint32(b)
	}
	return acc
}

// lookup is the code's CID, whether the CMap maps it, and whether it falls to
// an opaque base instead.
func (c *CMap) lookup(v uint32, n int) (cid int, mapped, unknown bool) {
	if c.opaque {
		return 0, false, true
	}
	if c.identity {
		return int(v), true, false
	}
	if cid, ok := c.single[cmapKey{v, uint8(n)}]; ok {
		return cid, true, false
	}
	for _, r := range c.ranges {
		if r.bytes == n && v >= r.lo && v <= r.hi {
			return r.cid + int(v-r.lo), true, false
		}
	}
	if c.base != nil {
		if cid, mapped, unknown := c.base.lookup(v, n); mapped || unknown {
			return cid, mapped, unknown
		}
	}
	if cid, ok := c.notdefSingle[cmapKey{v, uint8(n)}]; ok {
		return cid, true, false
	}
	for _, r := range c.notdefRanges {
		if r.bytes == n && v >= r.lo && v <= r.hi {
			// A notdef range maps every code in it to the one CID.
			return r.cid, true, false
		}
	}
	return 0, false, false
}

// Identity reports whether this is the built-in CMap, for a caller that has a
// shortcut for it.
func (c *CMap) Identity() bool { return c != nil && c.identity }

// LoadCMap is the CMap a Type 0 font's /Encoding names or carries, and why
// there is none when there is none.
//
// Three shapes. Identity-H and Identity-V are built in. A stream is a CMap
// program embedded in the document, which is parsed, along with the CMap it
// builds on (the stream's /UseCMap, or its usecmap operator). Any other name is
// one of Adobe's predefined CMaps — data this module does not carry, so the
// answer is ReasonUnsupported and the caller skips rather than guesses — or a
// name that is no CMap at all, ReasonMalformed (the CMap-legality rule reports
// it).
//
// Every declined outcome is recorded here, by the producer, once per font or
// stream (audit 2026-09-22 T2, C75). A CMap that builds on one this module
// cannot read is ReasonOK: its own entries are checked, and a code it leaves
// to that base decodes as Code.Unknown. The skip is recorded when a check
// first meets such a code, and not before — a CMap that defines every code
// the document shows was checked in full.
func LoadCMap(doc View, fontDict *object.Dictionary) (*CMap, Reason) {
	c, r, why := resolveCMap(doc, doc.Resolve(fontDict.Get("Encoding")), 0, map[*object.Stream]bool{})
	switch {
	case c != nil:
		if w := c.unknownWhy; w.guard != "" {
			c.onUnknown = func() {
				doc.noteDeclinedFor(fontDict, doc.DictObjNum(fontDict), w.reason, w.guard, w.detail+
					"; the checks that need a code-to-CID mapping (glyph coverage, .notdef, widths, "+
					"CIDSet) were skipped for the codes it leaves to that CMap, rather than run against a guess")
			}
		}
	case why.guard != "":
		if why.source == nil {
			why.source, why.obj = fontDict, doc.DictObjNum(fontDict)
		}
		doc.noteDeclinedFor(why.source, why.obj, r, why.guard, why.detail+
			"; the checks that need a code-to-CID mapping (glyph coverage, .notdef, widths, CIDSet) "+
			"were skipped for that font rather than run against a guess")
	}
	return c, r
}

// cmapRefusal says what could not be read and why: the reason and the guard to
// report it under, what was missing in the file's terms, and the object the
// report attaches to. A zero guard means there is nothing to report — the
// outcome is ReasonOK, or it is the file's fault (malformed) and a rule reports
// it, or the producer below already did.
type cmapRefusal struct {
	reason        Reason
	guard, detail string
	source        any
	obj           int
}

// maxCMapChain bounds how many CMaps one can build on in turn through
// /UseCMap streams. Real ones build on one predefined CMap, directly.
const maxCMapChain = 8

// resolveCMap reads the CMap enc names or carries. depth and seen guard a chain
// of embedded CMaps that use each other.
func resolveCMap(doc View, enc object.Object, depth int, seen map[*object.Stream]bool) (*CMap, Reason, cmapRefusal) {
	switch e := enc.(type) {
	case object.Name:
		if e == "Identity-H" || e == "Identity-V" {
			return IdentityCMap(), ReasonOK, cmapRefusal{}
		}
		if _, known := PredefinedCMaps[string(e)]; known {
			return nil, ReasonUnsupported, cmapRefusal{reason: ReasonUnsupported, guard: GuardPredefinedCMap,
				detail: fmt.Sprintf("the font's CMap %s is predefined and its code-to-CID data is not carried", e)}
		}
		return nil, ReasonMalformed, cmapRefusal{}
	case *object.Stream:
		if seen[e] || depth >= maxCMapChain {
			// A CMap that builds on itself, or a chain no real file has.
			return nil, ReasonMalformed, cmapRefusal{}
		}
		seen[e] = true
		// Through the same budgeted decode every other stream goes through: a
		// CMap is compressed like anything else, and a compression bomb in one
		// is a bomb. Content records its own declined outcomes.
		data, r := doc.Content(e)
		if r != ReasonOK {
			return nil, r, cmapRefusal{}
		}
		c, uses, r := parseCMap(doc.Cancel, data)
		if r == ReasonLimit {
			return nil, r, cmapRefusal{reason: r, guard: GuardCMapSize, source: e, obj: doc.StreamObjNum(e),
				detail: "an embedded CMap declares more code ranges, or wider ones, than pdf0 expands, so it was not read rather than read in part"}
		}
		if r != ReasonOK {
			return nil, r, cmapRefusal{}
		}
		// The CMap it builds on. The stream dictionary's /UseCMap names it or
		// carries it (Table 118), and the program's usecmap operator names it
		// too; when both are present the dictionary is read first, because a
		// stream there is the base itself and the operator can only name it.
		if len(uses) > 1 {
			return nil, ReasonMalformed, cmapRefusal{}
		}
		var baseRef object.Object
		if u := doc.Resolve(e.Dict.Get("UseCMap")); u != nil {
			baseRef = u
		} else if len(uses) == 1 {
			baseRef = object.Name(uses[0])
		}
		if baseRef != nil {
			base, br, why := resolveCMap(doc, baseRef, depth+1, seen)
			if base == nil {
				// The CMap it builds on cannot be read. Its own entries still
				// stand, and a code they define is checked; a code they leave
				// to the base is Unknown, and is reported when met.
				if br.Declined() && why.guard == "" {
					// Already recorded where it happened (a declined decode).
					return nil, br, cmapRefusal{}
				}
				opaque := &CMap{opaque: true}
				switch {
				case why.guard != "":
					why.detail = "the font's embedded CMap builds on another CMap, and " + strings.TrimPrefix(why.detail, "the font's ")
					opaque.unknownWhy = why
					if n, ok := baseRef.(object.Name); ok {
						// Its codespace is carried even where its mapping is
						// not, so codes are still cut in the right places.
						opaque.codespace = predefinedCodespaces[string(n)]
					}
				default:
					// Neither built in, predefined nor embedded: the CMap
					// legality rule reports the name; what the codes left to it
					// map to is unknown.
					opaque.unknownWhy = cmapRefusal{reason: ReasonUnsupported, guard: GuardEmbeddedCMap,
						detail: fmt.Sprintf("the font's embedded CMap builds on %v, which is neither predefined nor embedded", baseRef)}
				}
				base = opaque
			}
			c.base = base
			c.codespace = append(c.codespace, base.codespace...)
			if base.unknownWhy.guard != "" {
				c.unknownWhy = base.unknownWhy
			}
		}
		if len(c.codespace) == 0 {
			// Without a codespace nothing can be cut into codes. A CMap is
			// required to have one; a file that omits it has not said how to
			// read itself — or, building on a CMap whose data is not carried,
			// has left that to data this module does not have.
			if c.unknownWhy.guard != "" {
				w := c.unknownWhy
				return nil, w.reason, w
			}
			return nil, ReasonMalformed, cmapRefusal{}
		}
		return c, ReasonOK, cmapRefusal{}
	}
	return nil, ReasonMalformed, cmapRefusal{}
}

// ParseCMap reads the CID half of a CMap program on its own: codespace ranges,
// cidrange and cidchar, notdefrange and notdefchar. A CMap that builds on
// another with usecmap has that base read when it is Identity-H or Identity-V;
// any other base is opaque (its codespace taken from the predefined table when
// it has one), and the codes left to it decode as Unknown. LoadCMap, which has
// the document, also follows a /UseCMap stream and reports what it cannot
// read.
//
// ReasonMalformed is a CMap with no codespace, or more than one usecmap;
// ReasonLimit one past the fixed expansion bounds.
func ParseCMap(src string) (*CMap, Reason) {
	c, uses, r := parseCMap(Canceler{}, []byte(src))
	if r != ReasonOK {
		return nil, r
	}
	if len(uses) > 1 {
		return nil, ReasonMalformed
	}
	if len(uses) == 1 {
		if uses[0] == "Identity-H" || uses[0] == "Identity-V" {
			c.base = IdentityCMap()
		} else {
			c.base = &CMap{opaque: true, codespace: predefinedCodespaces[uses[0]],
				unknownWhy: cmapRefusal{reason: ReasonUnsupported, guard: GuardPredefinedCMap, detail: "the CMap builds on /" + uses[0]}}
			c.unknownWhy = c.base.unknownWhy
		}
		c.codespace = append(c.codespace, c.base.codespace...)
	}
	if len(c.codespace) == 0 {
		return nil, ReasonMalformed
	}
	return c, ReasonOK
}

// parseCMap reads the CID half of one CMap program, returning it with the
// names it builds on. A CMap that runs past maxCMapEntries mappings, or has a
// range wider than maxCMapRangeSpan codes, is refused (ReasonLimit) rather than
// truncated: a partial map answers "this code has no CID" for codes it simply
// did not reach, and a caller cannot tell that from a code the document really
// left undefined.
func parseCMap(cancel Canceler, data []byte) (*CMap, []string, Reason) {
	c := &CMap{single: map[cmapKey]int{}, notdefSingle: map[cmapKey]int{}}
	var uses []string
	over := false
	entries := func() int {
		return len(c.ranges) + len(c.single) + len(c.codespace) + len(c.notdefRanges) + len(c.notdefSingle)
	}
	within := func() bool {
		if entries() > maxCMapEntries {
			over = true
		}
		return !over
	}
	scanCMap(cancel, data, cmapVisitor{
		codespace: func(lo, hi cmapCode) bool {
			c.codespace = append(c.codespace, codespaceRange{bytes: lo.n, lo: lo.v, hi: hi.v})
			return within()
		},
		cidRange: func(lo, hi cmapCode, cid int) bool {
			if cid > maxCID {
				return true
			}
			if uint64(hi.v-lo.v) >= maxCMapRangeSpan {
				over = true
				return false
			}
			c.ranges = append(c.ranges, cidRange{lo: lo.v, hi: hi.v, bytes: lo.n, cid: cid})
			return within()
		},
		cidChar: func(code cmapCode, cid int) bool {
			if cid > maxCID {
				return true
			}
			c.single[cmapKey{code.v, uint8(code.n)}] = cid
			return within()
		},
		notdefRange: func(lo, hi cmapCode, cid int) bool {
			if cid > maxCID {
				return true
			}
			c.notdefRanges = append(c.notdefRanges, cidRange{lo: lo.v, hi: hi.v, bytes: lo.n, cid: cid})
			return within()
		},
		notdefChar: func(code cmapCode, cid int) bool {
			if cid > maxCID {
				return true
			}
			c.notdefSingle[cmapKey{code.v, uint8(code.n)}] = cid
			return within()
		},
		useCMap: func(name string) { uses = append(uses, name) },
	})
	if over {
		return nil, nil, ReasonLimit
	}
	if cancel.Stopped() {
		return nil, nil, ReasonCanceled
	}
	return c, uses, ReasonOK
}

// CMapMaxCID is the largest CID an embedded CMap program's cidrange and
// cidchar entries map, or 0.
func CMapMaxCID(cancel Canceler, data []byte) int64 {
	var max int64
	scanCMap(cancel, data, cmapVisitor{
		cidRange: func(lo, hi cmapCode, cid int) bool {
			if top := int64(cid) + int64(hi.v-lo.v); top > max {
				max = top
			}
			return true
		},
		cidChar: func(_ cmapCode, cid int) bool {
			if int64(cid) > max {
				max = int64(cid)
			}
			return true
		},
	})
	return max
}

// CMapWMode is the writing mode an embedded CMap program sets with
// "/WMode n def", and whether it sets one.
func CMapWMode(cancel Canceler, data []byte) (int, bool) {
	mode, found := 0, false
	scanCMap(cancel, data, cmapVisitor{wmode: func(m int) {
		if !found {
			mode, found = m, true
		}
	}})
	return mode, found
}

// CMapUseCMap is the name an embedded CMap program builds on with usecmap, and
// whether it names one. The operator counts only as an operator: the word in a
// comment, or in a string, is not a reference to anything.
func CMapUseCMap(cancel Canceler, data []byte) (string, bool) {
	name, found := "", false
	scanCMap(cancel, data, cmapVisitor{useCMap: func(n string) {
		if !found {
			name, found = n, true
		}
	}})
	return name, found
}
