package pdfa

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/checked"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
	"math"
	"unicode/utf8"
)

// This file implements the byte-level PDF/A file-structure rules (ISO 19005
// clause 6.1), which operate on the file's bytes rather than the parsed object
// model. They are grounded in ISO 32000-1 clause 7.5 (File Structure) and
// Annex C / 6.1.7 implementation limits.
//
// Every one of them reads a core.FileRecord — the bytes of the file the
// document was read from and what Read learned about them — and nothing else:
// not a caller's bytes, which need not be that file's (audit 2026-09-22 C69),
// and not the object graph, which a caller may have edited since. Each is
// dispatched on its own from validateView, so one that fails internally costs
// only its own rule.

// checkFileHeaderBytes validates the file header per ISO 19005 6.1.2,
// grounded in ISO 32000-1 7.5.2: the header shall begin at byte offset 0,
// be "%PDF-" followed by a single-digit major, ".", single-digit minor and a
// single EOL marker, and be immediately followed by a comment line whose
// first four bytes after "%" are all binary (>= 128).
func checkFileHeaderBytes(level Level, raw []byte) []Violation {
	rule := "6.1.2"
	bad := func(msg string) []Violation {
		return []Violation{{Rule: rule, Level: level, Message: msg}}
	}

	if !bytes.HasPrefix(raw, []byte("%PDF-")) {
		// The header may not begin at offset 0 (leading bytes), or be absent.
		if idx := bytes.Index(raw, []byte("%PDF-")); idx > 0 {
			return bad("file header does not begin at byte offset 0")
		}
		return bad("file header %PDF- not found")
	}

	// %PDF-  D  .  D  <EOL>
	if len(raw) < 9 {
		return bad("file header is truncated")
	}
	major, dot, minor := raw[5], raw[6], raw[7]
	if !isDigit(major) || dot != '.' || !isDigit(minor) {
		return bad(fmt.Sprintf("file header version is malformed: %q", string(raw[5:min2(len(raw), 12)])))
	}

	// A single EOL marker must immediately follow the version.
	i := 8
	switch raw[i] {
	case '\n':
		i++
	case '\r':
		i++
		if i < len(raw) && raw[i] == '\n' {
			i++
		}
	default:
		return bad("file header is not followed by a single EOL marker")
	}

	// The header line shall be immediately followed by a binary comment line.
	if i >= len(raw) || raw[i] != '%' {
		return bad("file header is not followed by a comment line")
	}
	i++ // past '%'
	// Collect the comment bytes up to the next EOL.
	end := i
	for end < len(raw) && raw[end] != '\r' && raw[end] != '\n' {
		end++
	}
	comment := raw[i:end]
	if len(comment) < 4 {
		return bad("the comment following the file header has fewer than four bytes")
	}
	for k := 0; k < 4; k++ {
		if comment[k] < 128 {
			return bad("the comment following the file header must contain four bytes each >= 128 (binary)")
		}
	}
	if end >= len(raw) {
		return bad("the comment following the file header is not terminated by an EOL marker")
	}
	return nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isEOLByte reports whether b is a PDF end-of-line marker byte.
func isEOLByte(b byte) bool { return b == '\n' || b == '\r' }

// isPDFWhite reports whether b is PDF white space.
func isPDFWhite(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

// checkIndirectObjectSyntax validates the byte layout of every uncompressed
// indirect object (ISO 19005-2 6.1.8, -4 6.1.8; grounded in ISO 32000-1
// 7.3.10): "objnum gen obj" with exactly one white-space between the parts,
// the object number preceded by an EOL marker, the obj keyword followed by
// an EOL marker, and the endobj keyword preceded and followed by an EOL
// marker (with no extra spaces).
//
// Each object is examined over its own region, [Offset, End) as Read parsed
// it. The region used to run to the next object's offset, so bytes between
// objects — and, once Read dropped them from the offsets, whole
// cross-reference streams — were read as part of the object before.
func checkIndirectObjectSyntax(f *core.FileRecord, level Level) []Violation {
	rule := indirectRule(level)
	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}
	for _, o := range f.Objects {
		if o.Offset < 0 || o.Offset >= int64(len(f.Data)) || o.End <= o.Offset {
			continue
		}
		checkOneObjectSyntax(f.Data, o.Offset, o.End, o.Num, add)
	}
	return errs
}

func checkOneObjectSyntax(raw []byte, off, regionEnd int64, num int, add func(string, int)) {
	// Cross-reference offsets sometimes point at the EOL/white space just
	// before the object number rather than at the first digit; advance to
	// the digit so the layout checks apply to the real object header.
	//
	// The scan is bounded by the object's own region, not by a byte count. The
	// rule (ISO 19005-1 6.1.8, -2 6.1.9, -4 6.1.8: "the object number and
	// endobj keyword shall each be preceded by an EOL marker") is a statement
	// about the byte immediately before the header, so the header has to be
	// found wherever the offset left it. The eight-byte cap it replaces stopped
	// mid-white-space on any object preceded by a longer run: the object then
	// went unchecked, and when the byte eight in happened to be a space or tab
	// the check reported "indirect object number is not preceded by an EOL
	// marker" against a header that was in fact EOL-preceded.
	limit := int(min64(regionEnd, int64(len(raw))))
	p := int(off)
	for p < limit && isPDFWhite(raw[p]) {
		p++
	}
	if p >= limit || !isDigit(raw[p]) {
		return // not a numeric object header; skip
	}
	// Object number preceded by an EOL marker.
	if p > 0 && !isEOLByte(raw[p-1]) {
		add("indirect object number is not preceded by an EOL marker", num)
	}
	// objnum
	q := p
	for q < len(raw) && isDigit(raw[q]) {
		q++
	}
	// exactly one white-space
	if q >= len(raw) || !isPDFWhite(raw[q]) {
		add("object number and generation number are not separated by a single white-space character", num)
		return
	}
	if q+1 < len(raw) && isPDFWhite(raw[q+1]) {
		add("object number and generation number are not separated by a single white-space character", num)
	}
	q++
	// gen
	g := q
	for q < len(raw) && isDigit(raw[q]) {
		q++
	}
	if q == g {
		return
	}
	if q >= len(raw) || !isPDFWhite(raw[q]) {
		add("generation number and obj keyword are not separated by a single white-space character", num)
		return
	}
	if q+1 < len(raw) && isPDFWhite(raw[q+1]) {
		add("generation number and obj keyword are not separated by a single white-space character", num)
	}
	q++
	// obj keyword
	if q+3 > len(raw) || string(raw[q:q+3]) != "obj" {
		return
	}
	q += 3
	// obj keyword followed by an EOL marker.
	if q >= len(raw) || !isEOLByte(raw[q]) {
		add("obj keyword is not followed by an EOL marker", num)
	}

	// endobj: the last occurrence within the object's region.
	region := raw[int(off):min64(regionEnd, int64(len(raw)))]
	idx := lastIndexToken(region, "endobj")
	if idx < 0 {
		return
	}
	ep := int(off) + idx
	// preceded by an EOL marker, with no extra white space.
	if ep > 0 {
		if raw[ep-1] == ' ' || raw[ep-1] == '\t' {
			add("endobj keyword is not preceded by an EOL marker (extra white space)", num)
		} else if !isEOLByte(raw[ep-1]) {
			add("endobj keyword is not preceded by an EOL marker", num)
		}
	}
	// followed by an EOL marker.
	after := ep + 6
	if after < len(raw) && !isEOLByte(raw[after]) {
		add("endobj keyword is not followed by an EOL marker", num)
	}
}

func indirectRule(level Level) string {
	if level.Part() == 1 {
		return "6.1.8"
	}
	if level.Part() == 4 {
		return "6.1.8"
	}
	return "6.1.9"
}

// lastIndexToken returns the offset of the last delimited occurrence of a
// keyword (preceded and followed by a non-regular byte or a boundary).
func lastIndexToken(b []byte, kw string) int {
	k := []byte(kw)
	for i := len(b) - len(k); i >= 0; i-- {
		if !bytes.Equal(b[i:i+len(k)], k) {
			continue
		}
		before := i == 0 || !syntax.IsRegular(b[i-1])
		after := i+len(k) >= len(b) || !syntax.IsRegular(b[i+len(k)])
		if before && after {
			return i
		}
	}
	return -1
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// --- name UTF-8 validity (ISO 19005-2 6.1.8, -4 6.1.7) ---

// checkNameUTF8 verifies that human-readable name objects are valid UTF-8:
// Separation/DeviceN colorant names at every level, plus font names,
// structure type names and RoleMap names at PDF/A-4 (PDF 2.0, where names
// are defined as UTF-8, ISO 32000-2 7.3.5).
func checkNameUTF8(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil // PDF/A-1 predates the UTF-8 name requirement
	}
	rule := "6.1.8"
	if level.Part() == 4 {
		rule = "6.1.7"
	}
	// One example per distinct message, attributed to the lowest object number
	// that produced it — the objects are reached in doc.Objects map order.
	var found exampleFindings
	add := func(msg string, obj int) {
		found.add(Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	// The objects the document reaches (an orphan names nothing the document
	// shows); walkColorantUTF8 descends each one's direct values itself.
	for _, num := range doc.ReachableObjectNums() {
		iobj := doc.Objects[num]
		walkColorantUTF8(doc, iobj.Value, num, add, 0)
		if level.Part() == 4 {
			if d, ok := iobj.Value.(*object.Dictionary); ok {
				checkA4NameUTF8(doc, d, num, add)
			}
		}
	}
	return found.errs
}

// walkColorantUTF8 descends an object's structure (bounded depth, without
// following indirect references, which are visited as their own objects)
// checking every Separation/DeviceN colour-space array's colorant names.
func walkColorantUTF8(doc core.View, obj object.Object, num int, add func(string, int), depth int) {
	if depth > 12 {
		return
	}
	switch v := obj.(type) {
	case object.IndirectRef:
		// Visited as its own object.
	case object.Array:
		checkColorantArrayUTF8(doc, v, num, add)
		for _, e := range v {
			walkColorantUTF8(doc, e, num, add, depth+1)
		}
	case *object.Dictionary:
		for val := range v.Values() {
			walkColorantUTF8(doc, val, num, add, depth+1)
		}
	case *object.Stream:
		for val := range v.Dict.Values() {
			walkColorantUTF8(doc, val, num, add, depth+1)
		}
	}
}

// checkColorantArrayUTF8 checks a Separation/DeviceN colour-space array's
// colorant name(s).
func checkColorantArrayUTF8(doc core.View, arr object.Array, num int, add func(string, int)) {
	if len(arr) < 2 {
		return
	}
	csType, _ := doc.ResolveName(arr[0])
	switch csType {
	case "Separation":
		if name, ok := doc.ResolveName(arr[1]); ok && !validUTF8Name(name) {
			add("the colorant name in a Separation colour space is not a valid UTF-8 string", num)
		}
	case "DeviceN":
		if names, ok := doc.Resolve(arr[1]).(object.Array); ok {
			for _, el := range names {
				if name, ok := doc.ResolveName(el); ok && !validUTF8Name(name) {
					add("the colorant name in a DeviceN colour space is not a valid UTF-8 string", num)
				}
			}
		}
	}
}

// checkA4NameUTF8 checks the additional PDF/A-4 name categories: font names,
// structure element type names, and RoleMap names.
func checkA4NameUTF8(doc core.View, dict *object.Dictionary, num int, add func(string, int)) {
	if t, _ := doc.ResolveName(dict.Get("Type")); t == "Font" {
		if bf, ok := doc.ResolveName(dict.Get("BaseFont")); ok && !validUTF8Name(bf) {
			add("the font name is not a valid UTF-8 string", num)
		}
	}
	// Structure element type name.
	if t, _ := doc.ResolveName(dict.Get("Type")); t == "StructElem" {
		if s, ok := doc.ResolveName(dict.Get("S")); ok && !validUTF8Name(s) {
			add("the structure type name is not a valid UTF-8 string", num)
		}
	}
	// RoleMap: a dictionary of name -> name.
	if rm := doc.ResolveDict(dict.Get("RoleMap")); rm != nil {
		for key, rval := range rm.All() {
			if !validUTF8Name(key) {
				add("the structure type name in RoleMap is not a valid UTF-8 string", num)
			}
			if val, ok := doc.ResolveName(rval); ok && !validUTF8Name(val) {
				add("the structure type name in RoleMap is not a valid UTF-8 string", num)
			}
		}
	}
}

// validUTF8Name reports whether a name's bytes form valid UTF-8.
func validUTF8Name(n object.Name) bool {
	return utf8Valid([]byte(n))
}

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// --- cross-reference table format (ISO 19005 6.1.4; ISO 32000-1 7.5.4) ---

// checkXRefTableFormat validates the byte layout of every traditional
// cross-reference table: the xref keyword followed by a single EOL, each
// subsection header "start count" separated by exactly one space, and each
// entry line in the fixed 20-byte form.
//
// The tables are the ones Read located through startxref and the /Prev chain
// (FileRecord.XRefTables). It used to take every delimited "xref" in the file
// for a table, so a string or a comment saying "see the xref table" was
// reported under 6.1.4 (audit 2026-09-22 C71).
func checkXRefTableFormat(f *core.FileRecord, level Level) []Violation {
	rule := "6.1.4"
	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: rule, Level: level, Message: msg})
	}
	for _, kw := range f.XRefTables {
		if kw < 0 || kw+4 > int64(len(f.Data)) || string(f.Data[kw:kw+4]) != "xref" {
			continue
		}
		validateXRefSectionFormat(f.Data, int(kw)+4, add)
	}
	return errs
}

// checkNoXRefStreams enforces ISO 19005-1 6.1.4 (veraPDF 6.1.4-t03): a
// PDF/A-1 file shall not use cross-reference streams, which PDF 1.4 does not
// have. The later parts are built on PDF 1.7 and 2.0 and allow them.
//
// The veraPDF fail file for it (6-1-4-t03-fail-a) used to be caught only by
// accident: its outline carries the title "Cross reference table contains the
// xref stream", and the old xref-table check took that " xref " for a table
// whose keyword was not followed by an EOL (audit 2026-09-22 C71). Once only
// the tables Read located were checked, the file had no finding at all, which
// is what showed this rule was missing.
func checkNoXRefStreams(f *core.FileRecord, level Level) []Violation {
	if level.Part() != 1 || !f.XRefStreams {
		return nil
	}
	return []Violation{{Rule: "6.1.4", Level: level,
		Message: "the file uses a cross-reference stream, which PDF/A-1 does not allow"}}
}

func validateXRefSectionFormat(raw []byte, p int, add func(string)) {
	// Exactly one EOL after the xref keyword.
	if p >= len(raw) || (raw[p] != '\r' && raw[p] != '\n') {
		add("the xref keyword is not followed by a single EOL marker")
		return
	}
	q := consumeSingleEOL(raw, p)
	// The next byte must begin a subsection header (a digit); reject an
	// extra blank line.
	if q >= len(raw) || !isDigit(raw[q]) {
		add("the xref keyword and the subsection header are not separated by a single EOL marker")
		return
	}

	// Subsections: repeat while a header line of two integers is present.
	for q < len(raw) && isDigit(raw[q]) {
		// header: start SP count EOL
		lineEnd := q
		for lineEnd < len(raw) && raw[lineEnd] != '\r' && raw[lineEnd] != '\n' {
			lineEnd++
		}
		header := raw[q:lineEnd]
		count, ok := validateXRefSubsectionHeader(header, add)
		if !ok {
			return
		}
		q = consumeSingleEOL(raw, lineEnd)
		// count entries of 20 bytes each.
		for k := 0; k < count; k++ {
			if q+18 > len(raw) {
				return
			}
			if !validXRefEntryLine(raw[q:]) {
				add("a cross-reference entry is not in the fixed 20-byte format")
				return
			}
			q += 20
		}
	}
}

// validateXRefSubsectionHeader checks "start count" with a single space and
// returns the object count.
func validateXRefSubsectionHeader(h []byte, add func(string)) (int, bool) {
	i := 0
	for i < len(h) && isDigit(h[i]) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	if i >= len(h) || h[i] != ' ' {
		add("a cross-reference subsection header is not formatted as 'start count' with a single space")
		return 0, false
	}
	if i+1 < len(h) && h[i+1] == ' ' {
		add("a cross-reference subsection header has extra spaces between the start and count")
		return 0, false
	}
	j := i + 1
	// A count too large for an int saturates: the entries that follow run out
	// long before it would, which is where the loop over them stops.
	c, digits, _ := checked.Decimal(h[j:])
	if digits == 0 {
		return 0, false
	}
	count := int(min(c, math.MaxInt))
	j += digits
	// Trailing content on the header line (other than the count) is invalid.
	if j != len(h) {
		add("a cross-reference subsection header has trailing characters")
		return 0, false
	}
	return count, true
}

// validXRefEntryLine reports whether the bytes begin a fixed-format 20-byte
// xref entry: 10-digit offset, space, 5-digit generation, space, type, EOL.
func validXRefEntryLine(b []byte) bool {
	if len(b) < 20 {
		return false
	}
	for i := 0; i < 10; i++ {
		if !isDigit(b[i]) {
			return false
		}
	}
	if b[10] != ' ' {
		return false
	}
	for i := 11; i < 16; i++ {
		if !isDigit(b[i]) {
			return false
		}
	}
	if b[16] != ' ' {
		return false
	}
	if b[17] != 'n' && b[17] != 'f' {
		return false
	}
	// bytes 18-19: EOL (CRLF, SP+CR, or SP+LF).
	e1, e2 := b[18], b[19]
	okEOL := (e1 == '\r' && e2 == '\n') ||
		(e1 == ' ' && (e2 == '\r' || e2 == '\n'))
	return okEOL
}

// consumeSingleEOL advances past exactly one EOL marker (CR, LF, or CRLF).
func consumeSingleEOL(raw []byte, p int) int {
	if p < len(raw) && raw[p] == '\r' {
		p++
		if p < len(raw) && raw[p] == '\n' {
			p++
		}
		return p
	}
	if p < len(raw) && raw[p] == '\n' {
		p++
	}
	return p
}

// --- hexadecimal string format (ISO 19005 6.1.6; ISO 32000-1 7.3.4.3) ---

// checkHexStringFormat verifies that every hexadecimal string object contains
// only hexadecimal digits and white space, and an even number of them
// (PDF/A forbids the implicit trailing-zero padding of an odd-length hex
// string).
//
// Hexadecimal strings are found in two places. In the file's object bodies,
// read from the file record (a document built in memory has none): each
// object's region up to its stream keyword, so stream data is never misread
// as syntax. And in the decoded content streams the document draws with,
// read from the graph whatever the document's origin — this half is not a
// byte-level rule, and used to be skipped whenever the caller passed no bytes.
//
// One finding per distinct message, attributed to the lowest object number
// that produced it: the content streams come from a map, and taking the first
// one found made the report vary between runs.
func checkHexStringFormat(doc core.View, level Level) []Violation {
	rule := "6.1.6"
	if level.Part() == 4 {
		rule = "6.1.5"
	}
	var found exampleFindings
	add := func(msg string, obj int) {
		found.add(Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}
	if f := doc.FileRecord(); f != nil {
		for _, o := range f.Objects {
			body := f.Region(o)
			if o.Stream && o.StreamKeyword >= o.Offset && o.StreamKeyword-o.Offset <= int64(len(body)) {
				body = body[:o.StreamKeyword-o.Offset]
			}
			scanHexStrings(body, func(content []byte) {
				checkOneHexString(content, o.Num, add)
			})
		}
	}
	// String objects also occur as operands inside content streams; scan the
	// decoded content of pages, form XObjects, and tiling patterns with a
	// content-aware tokenizer that skips inline-image binary data.
	// The content lexer reads every hexadecimal string operand, inside
	// dictionary operands too, so strings, comments and inline-image data are
	// never mistaken for one (contentBytesFacts).
	for num, f := range contentBytesFactsOf(doc) {
		if f.hexOdd {
			add(hexOddMsg, num)
		}
		if f.hexNonDigit {
			add(hexNonDigitMsg, num)
		}
	}
	return found.errs
}

const (
	hexOddMsg      = "a hexadecimal string object contains an odd number of non-white-space characters"
	hexNonDigitMsg = "a hexadecimal string object contains characters outside 0-9, A-F, a-f"
)

func checkOneHexString(content []byte, obj int, add func(string, int)) {
	if !hexStringEven(content) {
		add(hexOddMsg, obj)
	}
	if !hexStringDigitsOnly(content) {
		add(hexNonDigitMsg, obj)
	}
}

// collectContentStreamData returns the decoded bytes of every content stream
// (page Contents, form XObjects, tiling patterns, Type3 CharProcs), keyed by
// object number, each distinct content once.
//
// Its consumers are byte-level rules whose findings depend on nothing but the
// bytes, and which report one example per message, attributed to the lowest
// object number that produced it (exampleFindings). Content that several
// holders share — one stream every page names, or one /Contents array per
// page naming the same streams — is therefore listed once, under the lowest
// object number among its holders, which is where each of its findings was
// reported when it was listed once per holder. Listing it per holder made
// every consumer scan it once per page: a template drawn on 20 pages was
// tokenised 20 times by each of five rules. The result is memoised per run,
// since those five rules all ask for it.
func collectContentStreamData(doc core.View) map[int][]byte {
	memo := core.Slot[map[int][]byte](doc.Run, contentStreamDataSlot{})
	if *memo != nil {
		return *memo
	}
	out := make(map[int][]byte)
	// holder is the object number each distinct content is listed under.
	holder := map[*object.Stream]int{}
	put := func(num int, key *object.Stream, data []byte) {
		if key == nil {
			out[num] = data
			return
		}
		if prev, ok := holder[key]; ok {
			if num >= prev {
				return
			}
			delete(out, prev)
		}
		holder[key] = num
		out[num] = data
	}
	catalog := doc.Catalog()
	if catalog != nil {
		for _, page := range doc.Pages(catalog.Get("Pages")) {
			// Per page and, below, per stream: one iteration inflates one
			// content stream, bounded by the per-stream decode cap (cancel.go).
			if doc.Cancel.Stopped() {
				return out
			}
			if data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")); data != nil { // reason: every consumer scans for what is present; the producer recorded any declined trip
				put(page.ObjNum, key, data)
			}
		}
	}
	for _, r := range doc.ReachableDicts() {
		if doc.Cancel.Stopped() {
			return out
		}
		num := r.ObjNum
		s, ok := r.Stream, r.Stream != nil
		if !ok {
			continue
		}
		subtype, _ := doc.ResolveName(s.Dict.Get("Subtype"))
		isContent := subtype == "Form" || s.Dict.Get("PatternType") != nil
		if !isContent {
			continue
		}
		if data, _ := doc.Content(s); data != nil { // reason: as above
			put(num, s, data)
		}
	}
	// Type3 glyph procedures are content streams too, but carry no
	// Subtype/PatternType marker, so the loop above misses them. Pull them from
	// each Type3 font's /CharProcs (audit C27).
	for _, r := range doc.ReachableDicts() {
		fd := r.Dict
		if r.Stream != nil {
			continue
		}
		if st, _ := doc.ResolveName(fd.Get("Subtype")); st != "Type3" {
			continue
		}
		cp := doc.ResolveDict(fd.Get("CharProcs"))
		if cp == nil {
			continue
		}
		for val := range cp.Values() {
			num := resolveObjNum(doc, val)
			if num == 0 {
				continue
			}
			if _, done := out[num]; done {
				continue
			}
			if s, ok := doc.Resolve(val).(*object.Stream); ok {
				if data, _ := doc.Content(s); data != nil { // reason: as above
					put(num, s, data)
				}
			}
		}
	}
	if !doc.Cancel.Stopped() {
		*memo = out
	}
	return out
}

type contentStreamDataSlot struct{}

// scanHexStrings tokenises PDF object syntax and reports the content of each
// hexadecimal string (<...>), correctly skipping << >> dictionary markers,
// literal strings, comments, and names.
func scanHexStrings(b []byte, fn func(content []byte)) {
	i, n := 0, len(b)
	for i < n {
		switch c := b[i]; {
		case c == '%':
			for i < n && b[i] != '\r' && b[i] != '\n' {
				i++
			}
		case c == '(':
			depth := 1
			i++
			for i < n && depth > 0 {
				switch b[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case c == '<':
			if i+1 < n && b[i+1] == '<' {
				i += 2
				continue
			}
			start := i + 1
			j := start
			for j < n && b[j] != '>' {
				j++
			}
			fn(b[start:j])
			i = j + 1
		case c == '>':
			i++
			if i < n && b[i] == '>' {
				i++
			}
		default:
			i++
		}
	}
}

func hexStringEven(content []byte) bool {
	count := 0
	for _, c := range content {
		if !isPDFWhite(c) {
			count++
		}
	}
	return count%2 == 0
}

func hexStringDigitsOnly(content []byte) bool {
	for _, c := range content {
		if isPDFWhite(c) {
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// indexToken returns the offset of the first delimited occurrence of kw.
func indexToken(b []byte, kw string) int {
	k := []byte(kw)
	for i := 0; i+len(k) <= len(b); i++ {
		if !bytes.Equal(b[i:i+len(k)], k) {
			continue
		}
		before := i == 0 || !syntax.IsRegular(b[i-1])
		after := i+len(k) >= len(b) || !syntax.IsRegular(b[i+len(k)])
		if before && after {
			return i
		}
	}
	return -1
}

// --- stream keyword layout (ISO 19005-2 6.1.7.1; ISO 32000-1 7.3.8.1) ---

// checkStreamKeywordFormat verifies that the stream keyword is followed by
// CRLF or a single LF (not a bare CR, and with no extra white space before
// the EOL), and that endstream is preceded by an EOL marker.
//
// The keyword is the one Read's parser took (FileObject.StreamKeyword), not
// the first word "stream" a search finds, and endstream is looked for inside
// the object's own region.
func checkStreamKeywordFormat(f *core.FileRecord, level Level) []Violation {
	rule := "6.1.7.1"
	if level.Part() == 1 {
		rule = "6.1.6"
	} else if level.Part() == 4 {
		rule = "6.1.6"
	}
	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}
	for _, o := range f.Objects {
		if !o.Stream {
			continue
		}
		checkOneStreamKeyword(f.Region(o), o.StreamKeyword-o.Offset, o.Num, add)
	}
	return errs
}

// checkOneStreamKeyword checks one stream object's keyword layout. region is
// the object's bytes and kw the stream keyword's offset in it.
func checkOneStreamKeyword(region []byte, kw int64, num int, add func(string, int)) {
	if kw < 0 || kw+int64(len("stream")) > int64(len(region)) {
		return
	}
	p := int(kw) + len("stream")
	// After the stream keyword: CRLF or a single LF.
	switch {
	case p < len(region) && region[p] == '\r':
		if p+1 >= len(region) || region[p+1] != '\n' {
			add("the stream keyword is followed by a carriage return not followed by a line feed", num)
		}
	case p < len(region) && region[p] == '\n':
		// ok
	case p < len(region) && (region[p] == ' ' || region[p] == '\t'):
		add("the stream keyword has an extra white-space character before the EOL marker", num)
	default:
		add("the stream keyword is not followed by an EOL marker", num)
	}

	// endstream must be preceded by an EOL marker. Use the last occurrence in
	// the object's region: the real terminator is the one just before endobj,
	// and it may be glued directly to the stream data (which is itself the
	// violation).
	e := bytes.LastIndex(region[p:], []byte("endstream"))
	if e < 0 {
		return
	}
	e += p
	if region[e-1] != '\r' && region[e-1] != '\n' {
		add("the endstream keyword is not preceded by an EOL marker", num)
	}
}

// --- inline image filters (ISO 19005-4 6.1.9 / -2 6.1.10; ISO 32000 8.9.7) ---

// inlineFilterNames are the filter names permitted on an inline image /F
// entry (ISO 32000-1 Table 92 abbreviations and their full forms). LZW is a
// valid PDF filter but prohibited by PDF/A, so it is excluded here.
var inlineFilterNames = map[string]bool{
	"AHx": true, "ASCIIHexDecode": true,
	"A85": true, "ASCII85Decode": true,
	"Fl": true, "FlateDecode": true,
	"RL": true, "RunLengthDecode": true,
	"CCF": true, "CCITTFaxDecode": true,
	"DCT": true, "DCTDecode": true,
}

var inlineLZWNames = map[string]bool{"LZW": true, "LZWDecode": true}

// forEachInlineImage calls fn with the parameter entries of every inline image
// in a content stream.
func forEachInlineImage(data []byte, fn func([]core.InlineImageParam)) {
	lx := core.NewContentLexer(core.Canceler{}, data)
	var t core.ContentTok
	for lx.Next(&t) {
		if t.Kind == core.ContentInlineImage {
			fn(core.ParseInlineImageParams(t.Params))
		}
	}
}

// checkInlineImageFilters verifies that every inline image's /F (Filter)
// entry uses only permitted filters and never LZW.
func checkInlineImageFilters(doc core.View, level Level) []Violation {
	rule := "6.1.10"
	if level.Part() == 4 {
		rule = "6.1.9"
	} else if level.Part() == 1 {
		rule = "6.1.7"
	}
	// One example per distinct message, attributed to the lowest object number
	// that produced it — contentBytesFactsOf returns a map.
	var found exampleFindings
	add := func(msg string, obj int) {
		found.add(Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	for num, f := range contentBytesFactsOf(doc) {
		for _, filters := range f.inlineFilters {
			for _, name := range filters {
				switch {
				case inlineLZWNames[name]:
					add("LZW compression is used in an inline image", num)
				case !inlineFilterNames[name]:
					add(fmt.Sprintf("the inline image /F filter %q is not a permitted filter name", name), num)
				}
			}
		}
	}
	return found.errs
}

// inlineImageFilters is the /F names of each inline image in data that
// declares any, as contentBytesFacts reads them.
func inlineImageFilters(cancel core.Canceler, data []byte) [][]string {
	return scanContentBytes(cancel, data).inlineFilters
}

// checkStreamLength enforces that a stream's /Length entry equals the actual
// number of bytes between the stream and endstream keywords (ISO 19005-1
// 6.1.7, -2/-3 6.1.7, -4 6.1.6; ISO 32000-1 7.3.8.2). The parser recovers a
// stream with an incorrect Length by locating endstream, so object.Stream.Data holds
// the true byte count and a divergence from the declared value is a mismatch.
func checkStreamLength(doc core.View, level Level) []Violation {
	rule := "6.1.7" // 6.1.7 in ISO 19005-1
	switch level.Part() {
	case 4:
		rule = "6.1.6.1"
	case 2, 3:
		rule = "6.1.7.1"
	}
	var errs []Violation
	// allobjects: /Length is stream syntax, required of every stream the file
	// holds whether or not the document uses it.
	for num, iobj := range doc.Objects {
		s, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		length, ok := doc.Resolve(s.Dict.Get("Length")).(object.Integer)
		if !ok {
			continue // absent or unresolvable Length is a separate rule
		}
		if int(length) != len(s.Data) {
			errs = append(errs, Violation{Rule: rule, Level: level,
				Message: "the value of the Length key does not match the actual number of bytes in the stream",
				Object:  num})
		}
	}
	return errs
}

// checkObjectStreamDecodable flags an object stream whose compressed contents
// could not be decoded (ISO 32000-1 7.5.7, 7.3.8): such a stream is malformed,
// and the objects it should provide are unavailable.
func checkObjectStreamDecodable(doc core.View, level Level) []Violation {
	rule := "6.1.7"
	if level.Part() == 4 {
		rule = "6.1.6"
	}
	var errs []Violation
	for _, num := range doc.BrokenObjStms {
		errs = append(errs, Violation{Rule: rule, Level: level,
			Message: "an object stream could not be decoded (malformed stream data)", Object: num})
	}
	return errs
}

// checkLinearizedTrailerID enforces ISO 19005-1 6.1.3 for linearized files: the
// file identifier (/ID) in the first-page trailer and the last trailer shall be
// the same. A non-linearized incremental-update file legitimately carries
// several trailers, and comparing them there produced a false positive.
//
// Both facts come from the file record: the file is linearized when its first
// object is a linearization dictionary, and the trailers are those of the
// cross-reference sections Read parsed, in file order. The check used to call
// a file linearized when "/Linearized" occurred anywhere in its bytes, and to
// parse a trailer after every "trailer" in them, stream data included — the
// sibling of the "xref" mistake (audit 2026-09-22 C71).
func checkLinearizedTrailerID(f *core.FileRecord, level Level) []Violation {
	if !f.Linearized {
		return nil
	}
	var ids [][]byte
	for _, t := range f.Trailers {
		if t.HasID {
			ids = append(ids, t.ID0)
		}
	}
	if len(ids) >= 2 && !bytes.Equal(ids[0], ids[len(ids)-1]) {
		return []Violation{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "linearized file: the file identifier /ID in the first-page trailer and the last trailer differ",
		}}
	}
	return nil
}

// checkStreamLengthBytes verifies each uncompressed stream's /Length against the
// byte extent measured in the file (ISO 32000-1 7.3.8.2; PDF/A 6.1.7 /
// 6.1.6). The parser recovers from a wrong /Length by searching for endstream,
// so the in-memory object.Stream.Data can mask the defect; this measures the
// file directly.
//
// The extent runs from the end of the line after the stream keyword the parser
// took to the endstream just before the object's endobj. Only white space may
// appear between endstream and endobj (7.3.8.1), so that endstream is the real
// one even when the data itself contains the bytes "endstream". The keyword
// used to be found by searching the file for a white-space-preceded "stream"
// from the object's offset, which stepped over the legal ">>stream" and
// measured the next object's stream instead (audit 2026-09-22 C70). The
// declared length is the file's own, taken when Read finished.
//
// The optional end-of-line marker before endstream is not counted in the
// length, and a lone CARRIAGE RETURN may be data (NOTE 2). The declared length
// is therefore valid within [raw-eol, raw-1] when an EOL is present (raw
// exactly when none is), and only a length outside that range — such as one
// that wrongly includes the whole EOL — is a violation.
func checkStreamLengthBytes(f *core.FileRecord, level Level) []Violation {
	rule := "6.1.7" // 6.1.7 in ISO 19005-1
	switch level.Part() {
	case 4:
		rule = "6.1.6.1"
	case 2, 3:
		rule = "6.1.7.1"
	}
	var errs []Violation
	for _, o := range f.Objects {
		if !o.Stream || !o.LengthOK {
			continue
		}
		rawLen, eol, ok := streamByteExtent(f.Region(o), o.StreamKeyword-o.Offset)
		if !ok {
			continue
		}
		hi := rawLen
		if eol > 0 {
			hi = rawLen - 1
		}
		lo := rawLen - eol
		if o.Length < lo || o.Length > hi {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "the value of the Length key does not match the actual number of bytes in the stream",
				Object:  o.Num,
			})
		}
	}
	return errs
}

// streamByteExtent returns, for a stream object's region whose stream keyword
// is at kw, the number of bytes between the EOL after the keyword and the
// endstream that precedes the region's closing endobj, and the length of the
// EOL before that endstream (0, 1 or 2). ok is false when the region does not
// end "endstream <white space> endobj".
func streamByteExtent(region []byte, kw int64) (rawLen, eol int64, ok bool) {
	n := int64(len(region))
	if kw < 0 || kw+6 > n {
		return 0, 0, false
	}
	ds := kw + 6
	if ds < n && region[ds] == '\r' {
		ds++
	}
	if ds < n && region[ds] == '\n' {
		ds++
	}
	endobj := n - int64(len("endobj"))
	if endobj < ds || string(region[endobj:]) != "endobj" {
		return 0, 0, false
	}
	j := endobj - 1
	for j >= ds && syntax.IsWhitespace(region[j]) {
		j--
	}
	if j-8 < ds || string(region[j-8:j+1]) != "endstream" {
		return 0, 0, false
	}
	esStart := j - 8
	rawLen = esStart - ds
	if esStart-2 >= ds && region[esStart-1] == '\n' && region[esStart-2] == '\r' {
		eol = 2
	} else if esStart-1 >= ds && (region[esStart-1] == '\n' || region[esStart-1] == '\r') {
		eol = 1
	}
	return rawLen, eol, true
}
