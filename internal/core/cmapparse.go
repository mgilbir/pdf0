package core

// Reading a CMap program: an embedded CID CMap (ISO 32000-2 9.7.5) or a
// ToUnicode CMap (9.10.3). Both are PostScript, and both are read here as a
// token stream — by the content lexer, whose tokens are PostScript's — with
// the handful of operators that carry data interpreted and everything else
// ignored.
//
// The readers this replaces searched the text. "beginbfchar" found inside a
// comment opened a section; a CMap written with several entries on a line, or
// with CR alone between lines, lost all but one entry of each line or ran its
// lines together; "usecmap" anywhere, a comment included, refused the whole
// CMap without a word; and the forbidden-target scan counted every third
// hex string in a bfrange as a destination, which an array-form entry throws
// out of step (audit 2026-09-22 C75, C76, C78). A token stream has none of
// those failure modes: a comment is not a token, a line break is white space,
// an operator is an operator only where the lexer says one is, and an array is
// one operand.

// cmapCode is a character code and its width in bytes (1 to 4). The width is
// part of the code: <00> and <0000> are different codes in a CMap.
type cmapCode struct {
	v uint32
	n int
}

// cmapKey is a cmapCode as a map key.
type cmapKey struct {
	v uint32
	n uint8
}

// bfDest is the destination of a bfchar or bfrange entry: the UTF-16BE bytes
// of a hex (or literal) string, or for a bfrange an array of them, one per
// code.
type bfDest struct {
	utf16 []byte
	array [][]byte
	// isArray marks the array form; a name destination (a glyph name, which
	// a ToUnicode CMap has no use for) is neither and maps nothing.
	isArray bool
	valid   bool
}

// cmapVisitor receives what a CMap program declares, one entry at a time. A
// nil field ignores that kind of entry. An entry func returning false stops
// the scan.
type cmapVisitor struct {
	codespace func(lo, hi cmapCode) bool
	cidRange  func(lo, hi cmapCode, cid int) bool
	cidChar   func(code cmapCode, cid int) bool
	// notdefRange and notdefChar are the notdef mappings of 9.7.6.3: the
	// CID a code in the codespace stands for when no cidrange or cidchar
	// maps it.
	notdefRange func(lo, hi cmapCode, cid int) bool
	notdefChar  func(code cmapCode, cid int) bool
	bfChar      func(src cmapCode, dst bfDest) bool
	bfRange     func(lo, hi cmapCode, dst bfDest) bool
	useCMap     func(name string)
	wmode       func(mode int)
}

// maxCMapArray bounds one array operand. A bfrange array gives one destination
// per code of a range, and no range may span more than maxCMapRangeSpan codes.
const maxCMapArray = maxCMapRangeSpan

// scanCMap reads a CMap program and reports its declarations to v. Sections
// are the begin…/end… pairs of Table 121 and 9.10.3; an entry is the operands
// between them taken two or three at a time, in order, with no regard to lines.
func scanCMap(cancel Canceler, data []byte, v cmapVisitor) {
	lx := &ContentLexer{data: data, cancel: cancel, noInline: true}
	var t ContentTok

	// The section being read ("" outside one), its arity, and the operands
	// of the entry being assembled.
	section := ""
	arity := 0
	var ops [3]cmapOperand
	nops := 0
	// Outside a section: the last two operands, for "/Name usecmap" and
	// "/WMode n def".
	var prev1, prev2 cmapOperand

	emit := func() bool {
		switch section {
		case "codespacerange":
			lo, ok1 := ops[0].code()
			hi, ok2 := ops[1].code()
			if !ok1 || !ok2 || lo.n != hi.n || lo.v > hi.v || v.codespace == nil {
				return true
			}
			return v.codespace(lo, hi)
		case "cidrange", "notdefrange":
			lo, ok1 := ops[0].code()
			hi, ok2 := ops[1].code()
			cid, ok3 := ops[2].cid()
			f := v.cidRange
			if section == "notdefrange" {
				f = v.notdefRange
			}
			if !ok1 || !ok2 || !ok3 || lo.n != hi.n || lo.v > hi.v || f == nil {
				return true
			}
			return f(lo, hi, cid)
		case "cidchar", "notdefchar":
			code, ok1 := ops[0].code()
			cid, ok2 := ops[1].cid()
			f := v.cidChar
			if section == "notdefchar" {
				f = v.notdefChar
			}
			if !ok1 || !ok2 || f == nil {
				return true
			}
			return f(code, cid)
		case "bfchar":
			src, ok := ops[0].code()
			if !ok || v.bfChar == nil {
				return true
			}
			return v.bfChar(src, ops[1].dest())
		case "bfrange":
			lo, ok1 := ops[0].code()
			hi, ok2 := ops[1].code()
			if !ok1 || !ok2 || lo.n != hi.n || lo.v > hi.v || v.bfRange == nil {
				return true
			}
			return v.bfRange(lo, hi, ops[2].dest())
		}
		return true
	}

	keyword := func(word string) {
		if section != "" {
			if word == "end"+section {
				section = ""
			}
			// Any other keyword inside a section is malformed; it ends the
			// entry being assembled, not the section.
			nops = 0
			return
		}
		switch word {
		case "begincodespacerange", "begincidchar", "beginbfchar", "beginnotdefchar":
			section, arity = word[len("begin"):], 2
		case "begincidrange", "beginbfrange", "beginnotdefrange":
			section, arity = word[len("begin"):], 3
		case "usecmap":
			if prev1.tok.Kind == ContentName && v.useCMap != nil {
				v.useCMap(prev1.tok.Name())
			}
		case "def":
			if prev2.tok.Kind == ContentName && prev2.tok.Name() == "WMode" && prev1.tok.Kind == ContentNumber && v.wmode != nil {
				if m, ok := prev1.tok.Int(); ok {
					v.wmode(m)
				}
			}
		}
		nops = 0
		prev1, prev2 = cmapOperand{}, cmapOperand{}
	}

	for lx.Next(&t) {
		var op cmapOperand
		switch t.Kind {
		case ContentOperator:
			word := string(t.Raw)
			rest, split := splitCMapKeywords(word)
			if !split {
				keyword(word)
				continue
			}
			for _, kw := range rest.keywords {
				keyword(kw)
			}
			if rest.number == nil {
				continue
			}
			// "endcodespacerange32": the count that belongs to the next
			// section's begin, run into the keyword before it.
			op = cmapOperand{tok: ContentTok{Kind: ContentNumber, Raw: rest.number, Pos: t.Pos}}
		case ContentDictStart:
			lx.SkipDict(&t)
			op = cmapOperand{tok: ContentTok{Kind: ContentDictStart}}
		case ContentArrayStart:
			op = cmapOperand{isArray: true}
			for lx.Next(&t) && t.Kind != ContentArrayEnd {
				if len(op.array) < maxCMapArray {
					op.array = append(op.array, t)
				}
			}
		case ContentArrayEnd, ContentDictEnd:
			continue
		default:
			op = cmapOperand{tok: t}
		}
		if section == "" {
			prev2, prev1 = prev1, op
			continue
		}
		ops[nops] = op
		nops++
		if nops == arity {
			nops = 0
			if !emit() {
				return
			}
		}
	}
}

// cmapRunKeywords are the keywords that splitCMapKeywords will find run
// together with what follows them, longest first where one is a prefix of
// another.
var cmapRunKeywords = []string{
	"begincodespacerange", "endcodespacerange",
	"beginnotdefrange", "endnotdefrange", "beginnotdefchar", "endnotdefchar",
	"begincidrange", "endcidrange", "begincidchar", "endcidchar",
	"beginbfrange", "endbfrange", "beginbfchar", "endbfchar",
	"begincmap", "endcmap", "usecmap", "def",
}

// cmapRun is a run of keywords written without the white space between them,
// and the number that may end it.
type cmapRun struct {
	keywords []string
	number   []byte
}

// splitCMapKeywords reads a regular-character run that begins with a CMap
// keyword but is not one — "endcodespacerange32", "endcidrangeendcmap" — as
// the keywords it is made of, and a trailing number. Some producers write a
// CMap with no white space between a keyword and what follows it; PostScript
// would read the run as one unknown name, and a reader that did the same lost
// every section after the first one so written. Only runs that begin with a
// keyword of the CMap vocabulary are split, and the split stops at the first
// part that is neither a keyword nor a number.
func splitCMapKeywords(word string) (cmapRun, bool) {
	var r cmapRun
	for word != "" {
		kw := ""
		for _, k := range cmapRunKeywords {
			if len(word) >= len(k) && word[:len(k)] == k {
				kw = k
				break
			}
		}
		if kw == "" {
			if len(r.keywords) > 0 && isCMapNumber(word) {
				r.number = []byte(word)
			}
			break
		}
		if kw == word && len(r.keywords) == 0 {
			return cmapRun{}, false // the keyword itself, nothing run into it
		}
		r.keywords = append(r.keywords, kw)
		word = word[len(kw):]
	}
	return r, len(r.keywords) > 0
}

func isCMapNumber(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// cmapOperand is one operand of a CMap entry: a token, or an array of them.
type cmapOperand struct {
	tok     ContentTok
	array   []ContentTok
	isArray bool
}

// code reads the operand as a character code: a string of one to four bytes.
func (o cmapOperand) code() (cmapCode, bool) {
	if o.isArray || (o.tok.Kind != ContentHexString && o.tok.Kind != ContentString) {
		return cmapCode{}, false
	}
	return codeOf(o.tok.Bytes())
}

func codeOf(b []byte) (cmapCode, bool) {
	if len(b) == 0 || len(b) > 4 {
		return cmapCode{}, false
	}
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return cmapCode{v: v, n: len(b)}, true
}

// maxCID is far past any CID in any published character collection, and past
// what a glyph index can be. A CID mapping that names a larger one is a
// malformed file, and parseCMap does not map it; CMapMaxCID still sees it,
// because that is the value the implementation-limit rule is about.
const maxCID = 1 << 21

// cid reads the operand as a CID: a non-negative integer.
func (o cmapOperand) cid() (int, bool) {
	if o.isArray || o.tok.Kind != ContentNumber {
		return 0, false
	}
	n, ok := o.tok.Int()
	if !ok || n < 0 {
		return 0, false
	}
	return n, true
}

// dest reads the operand as a bfchar/bfrange destination.
func (o cmapOperand) dest() bfDest {
	if o.isArray {
		d := bfDest{isArray: true, valid: true}
		for i := range o.array {
			t := &o.array[i]
			if t.Kind == ContentHexString || t.Kind == ContentString {
				d.array = append(d.array, t.Bytes())
			} else {
				d.array = append(d.array, nil)
			}
		}
		return d
	}
	if o.tok.Kind == ContentHexString || o.tok.Kind == ContentString {
		return bfDest{utf16: o.tok.Bytes(), valid: true}
	}
	return bfDest{}
}

// utf16Units reads UTF-16BE bytes as code units; an odd final byte is
// dropped.
func utf16Units(b []byte) []uint16 {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return units
}
