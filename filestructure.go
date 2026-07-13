package pdf0

import (
	"bytes"
	"fmt"
	"sort"
	"unicode/utf8"
)

// This file implements the byte-level PDF/A file-structure rules (ISO 19005
// clause 6.1), which operate on the raw file bytes rather than the parsed
// object model. They are grounded in ISO 32000-1 clause 7.5 (File Structure)
// and Annex C / 6.1.7 implementation limits.

// checkFileStructureBytes runs every raw-byte file-structure check.
func checkFileStructureBytes(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	if raw == nil {
		return nil
	}
	var errs []ValidationError
	errs = append(errs, checkFileHeaderBytes(level, raw)...)
	errs = append(errs, checkIndirectObjectSyntax(doc, level, raw)...)
	errs = append(errs, checkNameUTF8(doc, level)...)
	errs = append(errs, checkXRefTableFormat(doc, level, raw)...)
	errs = append(errs, checkHexStringFormat(doc, level, raw)...)
	errs = append(errs, checkStreamKeywordFormat(doc, level, raw)...)
	errs = append(errs, checkInlineImageFilters(doc, level)...)
	errs = append(errs, checkInlineImageIntent(doc, level)...)
	return errs
}

// checkFileHeaderBytes validates the file header per ISO 19005 6.1.2,
// grounded in ISO 32000-1 7.5.2: the header shall begin at byte offset 0,
// be "%PDF-" followed by a single-digit major, ".", single-digit minor and a
// single EOL marker, and be immediately followed by a comment line whose
// first four bytes after "%" are all binary (>= 128).
func checkFileHeaderBytes(level PDFALevel, raw []byte) []ValidationError {
	rule := "6.1.2"
	bad := func(msg string) []ValidationError {
		return []ValidationError{{Rule: rule, Level: level, Message: msg}}
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
func checkIndirectObjectSyntax(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	if doc == nil || doc.Offsets == nil {
		return nil
	}
	rule := indirectRule(level)

	// Sort offsets ascending to bound each object's region.
	var offs []int64
	offToNum := make(map[int64]int)
	for num, off := range doc.Offsets {
		if _, ok := doc.Objects[num]; !ok {
			continue // dropped structural object
		}
		offs = append(offs, off)
		offToNum[off] = num
	}
	sortInt64(offs)

	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	for i, off := range offs {
		num := offToNum[off]
		regionEnd := int64(len(raw))
		if i+1 < len(offs) {
			regionEnd = offs[i+1]
		}
		if off < 0 || off >= int64(len(raw)) {
			continue
		}
		checkOneObjectSyntax(raw, off, regionEnd, num, add)
	}
	return errs
}

func checkOneObjectSyntax(raw []byte, off, regionEnd int64, num int, add func(string, int)) {
	// Cross-reference offsets sometimes point at the EOL/white space just
	// before the object number rather than at the first digit; advance to
	// the digit so the layout checks apply to the real object header.
	p := int(off)
	for p < len(raw) && isPDFWhite(raw[p]) && p < int(off)+8 {
		p++
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
	if q == p {
		return // not a numeric object header; skip
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

func indirectRule(level PDFALevel) string {
	if level == PDFA1b {
		return "6.1.8"
	}
	if level == PDFA4 {
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
		before := i == 0 || !isRegular(b[i-1])
		after := i+len(k) >= len(b) || !isRegular(b[i+len(k)])
		if before && after {
			return i
		}
	}
	return -1
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
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
func checkNameUTF8(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // PDF/A-1 predates the UTF-8 name requirement
	}
	rule := "6.1.8"
	if level == PDFA4 {
		rule = "6.1.7"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	for num, iobj := range doc.Objects {
		walkColorantUTF8(doc, iobj.Value, num, add, 0)
		if level == PDFA4 {
			if d, ok := iobj.Value.(*Dictionary); ok {
				checkA4NameUTF8(doc, d, num, add)
			}
		}
	}
	return errs
}

// walkColorantUTF8 descends an object's structure (bounded depth, without
// following indirect references, which are visited as their own objects)
// checking every Separation/DeviceN colour-space array's colorant names.
func walkColorantUTF8(doc *Document, obj Object, num int, add func(string, int), depth int) {
	if depth > 12 {
		return
	}
	switch v := obj.(type) {
	case Array:
		checkColorantArrayUTF8(doc, v, num, add)
		for _, e := range v {
			walkColorantUTF8(doc, e, num, add, depth+1)
		}
	case *Dictionary:
		for _, val := range v.Values {
			walkColorantUTF8(doc, val, num, add, depth+1)
		}
	case *Stream:
		for _, val := range v.Dict.Values {
			walkColorantUTF8(doc, val, num, add, depth+1)
		}
	}
}

// checkColorantArrayUTF8 checks a Separation/DeviceN colour-space array's
// colorant name(s).
func checkColorantArrayUTF8(doc *Document, arr Array, num int, add func(string, int)) {
	if len(arr) < 2 {
		return
	}
	csType, _ := arr[0].(Name)
	switch csType {
	case "Separation":
		if name, ok := arr[1].(Name); ok && !validUTF8Name(name) {
			add("the colorant name in a Separation colour space is not a valid UTF-8 string", num)
		}
	case "DeviceN":
		if names, ok := doc.Resolve(arr[1]).(Array); ok {
			for _, el := range names {
				if name, ok := el.(Name); ok && !validUTF8Name(name) {
					add("the colorant name in a DeviceN colour space is not a valid UTF-8 string", num)
				}
			}
		}
	}
}

// checkA4NameUTF8 checks the additional PDF/A-4 name categories: font names,
// structure element type names, and RoleMap names.
func checkA4NameUTF8(doc *Document, dict *Dictionary, num int, add func(string, int)) {
	if t, _ := dict.Get("Type").(Name); t == "Font" {
		if bf, ok := dict.Get("BaseFont").(Name); ok && !validUTF8Name(bf) {
			add("the font name is not a valid UTF-8 string", num)
		}
	}
	// Structure element type name.
	if t, _ := dict.Get("Type").(Name); t == "StructElem" {
		if s, ok := dict.Get("S").(Name); ok && !validUTF8Name(s) {
			add("the structure type name is not a valid UTF-8 string", num)
		}
	}
	// RoleMap: a dictionary of name -> name.
	if rm := doc.ResolveDict(dict.Get("RoleMap")); rm != nil {
		for i, key := range rm.Keys {
			if !validUTF8Name(key) {
				add("the structure type name in RoleMap is not a valid UTF-8 string", num)
			}
			if val, ok := rm.Values[i].(Name); ok && !validUTF8Name(val) {
				add("the structure type name in RoleMap is not a valid UTF-8 string", num)
			}
		}
	}
}

// validUTF8Name reports whether a name's bytes form valid UTF-8.
func validUTF8Name(n Name) bool {
	return utf8Valid([]byte(n))
}

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// --- cross-reference table format (ISO 19005 6.1.4; ISO 32000-1 7.5.4) ---

// checkXRefTableFormat validates the byte layout of every traditional
// cross-reference table: the xref keyword followed by a single EOL, each
// subsection header "start count" separated by exactly one space, and each
// entry line in the fixed 20-byte form.
func checkXRefTableFormat(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	rule := "6.1.4"
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg})
	}

	// Each delimited "xref" keyword (not "startxref", whose preceding byte
	// is the regular char 't') introduces a cross-reference table.
	for i := 0; i+4 <= len(raw); i++ {
		if !bytes.Equal(raw[i:i+4], []byte("xref")) {
			continue
		}
		if i > 0 && isRegular(raw[i-1]) {
			continue // startxref or similar
		}
		if i+4 >= len(raw) || isRegular(raw[i+4]) {
			continue // xref stream object header etc.
		}
		validateXRefSectionFormat(raw, i+4, add)
	}
	return errs
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
	start := j
	count := 0
	for j < len(h) && isDigit(h[j]) {
		count = count*10 + int(h[j]-'0')
		j++
	}
	if j == start {
		return 0, false
	}
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
// string). Object bodies are tokenised up to the stream keyword so binary
// stream data is never misread as a hex string.
func checkHexStringFormat(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	if doc == nil || doc.Offsets == nil {
		return nil
	}
	rule := "6.1.6"
	if level == PDFA4 {
		rule = "6.1.5"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	var offs []int64
	offToNum := map[int64]int{}
	for num, off := range doc.Offsets {
		if _, ok := doc.Objects[num]; ok {
			offs = append(offs, off)
			offToNum[off] = num
		}
	}
	sortInt64(offs)

	for i, off := range offs {
		regionEnd := int64(len(raw))
		if i+1 < len(offs) {
			regionEnd = offs[i+1]
		}
		body := raw[int(off):min64(regionEnd, int64(len(raw)))]
		// Restrict to the object's dictionary/value, before any stream data.
		if s := indexToken(body, "stream"); s >= 0 {
			body = body[:s]
		}
		scanHexStrings(body, func(content []byte) {
			checkOneHexString(content, offToNum[off], add)
		})
	}

	// String objects also occur as operands inside content streams; scan the
	// decoded content of pages, form XObjects, and tiling patterns with a
	// content-aware tokenizer that skips inline-image binary data.
	for num, cs := range collectContentStreamData(doc) {
		scanContentHexStrings(cs, func(content []byte) {
			checkOneHexString(content, num, add)
		})
	}
	return errs
}

// scanContentHexStrings reports the raw content of each hexadecimal string
// operand in a content stream, skipping literal strings, comments, names,
// and inline-image binary data (BI ... ID <binary> EI).
func scanContentHexStrings(data []byte, fn func(content []byte)) {
	n := len(data)
	i := 0
	for i < n {
		switch b := data[i]; {
		case isContentWS(b):
			i++
		case b == '%':
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(':
			depth := 1
			i++
			for i < n && depth > 0 {
				switch data[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case b == '<':
			if i+1 < n && data[i+1] == '<' {
				i += 2
				continue
			}
			start := i + 1
			j := start
			for j < n && data[j] != '>' {
				j++
			}
			fn(data[start:j])
			i = j + 1
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '/' || b == '[' || b == ']' || b == '{' || b == '}' || b == ')':
			i++
		default:
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
				if i-start > 256 {
					break
				}
			}
			if i == start {
				i++ // unhandled delimiter (e.g. stray ')'): guarantee progress
				continue
			}
			if i-start == 2 && data[start] == 'B' && data[start+1] == 'I' {
				skipInlineImage(data, &i)
			}
		}
	}
}

func checkOneHexString(content []byte, obj int, add func(string, int)) {
	if !hexStringEven(content) {
		add("a hexadecimal string object contains an odd number of non-white-space characters", obj)
	}
	if !hexStringDigitsOnly(content) {
		add("a hexadecimal string object contains characters outside 0-9, A-F, a-f", obj)
	}
}

// collectContentStreamData returns the decoded bytes of every content stream
// (page Contents, form XObjects, tiling patterns, Type3 CharProcs), keyed by
// object number.
func collectContentStreamData(doc *Document) map[int][]byte {
	out := make(map[int][]byte)
	catalog := getCatalog(doc)
	if catalog != nil {
		for _, page := range collectPages(doc, catalog.Get("Pages")) {
			if data := getContentStreamData(doc, page.dict.Get("Contents")); data != nil {
				out[page.objNum] = data
			}
		}
	}
	for num, iobj := range doc.Objects {
		s, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		subtype, _ := s.Dict.Get("Subtype").(Name)
		isContent := subtype == "Form" || s.Dict.Get("PatternType") != nil
		if !isContent {
			continue
		}
		if data := decodeContentStream(doc, s); data != nil {
			out[num] = data
		}
	}
	// Type3 glyph procedures are content streams too, but carry no
	// Subtype/PatternType marker, so the loop above misses them. Pull them from
	// each Type3 font's /CharProcs (audit C27).
	for _, iobj := range doc.Objects {
		fd, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if st, _ := fd.Get("Subtype").(Name); st != "Type3" {
			continue
		}
		cp := doc.ResolveDict(fd.Get("CharProcs"))
		if cp == nil {
			continue
		}
		for _, val := range cp.Values {
			num := resolveObjNum(doc, val)
			if num == 0 {
				continue
			}
			if _, done := out[num]; done {
				continue
			}
			if s, ok := doc.Resolve(val).(*Stream); ok {
				if data := decodeContentStream(doc, s); data != nil {
					out[num] = data
				}
			}
		}
	}
	return out
}

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
		before := i == 0 || !isRegular(b[i-1])
		after := i+len(k) >= len(b) || !isRegular(b[i+len(k)])
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
func checkStreamKeywordFormat(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	if doc == nil || doc.Offsets == nil {
		return nil
	}
	rule := "6.1.7.1"
	if level == PDFA1b {
		rule = "6.1.6"
	} else if level == PDFA4 {
		rule = "6.1.6"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	var offs []int64
	offToNum := map[int64]int{}
	for num, off := range doc.Offsets {
		iobj, ok := doc.Objects[num]
		if !ok {
			continue
		}
		if _, ok := iobj.Value.(*Stream); ok {
			offs = append(offs, off)
			offToNum[off] = num
		}
	}
	sortInt64(offs)

	for i, off := range offs {
		regionEnd := int64(len(raw))
		if i+1 < len(offs) {
			regionEnd = offs[i+1]
		}
		region := raw[int(off):min64(regionEnd, int64(len(raw)))]
		checkOneStreamKeyword(region, int(off), offToNum[off], raw, add)
	}
	return errs
}

func checkOneStreamKeyword(region []byte, base, num int, raw []byte, add func(string, int)) {
	s := indexToken(region, "stream")
	if s < 0 {
		return
	}
	p := s + len("stream")
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

	// endstream must be preceded by an EOL marker. Use the last substring
	// occurrence: the real terminator is the last "endstream" in the
	// object's region, and it may be glued directly to the stream data
	// (which is itself the violation).
	e := bytes.LastIndex(region, []byte("endstream"))
	if e <= 0 {
		return
	}
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

// checkInlineImageIntent verifies that an inline image /Intent entry, when
// present, names a standard rendering intent (ISO 19005-2 6.2.6, -4 6.2.9;
// ISO 32000-1 8.6.5.8).
func checkInlineImageIntent(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.2.6"
	if level == PDFA4 {
		rule = "6.2.9"
	} else if level == PDFA1b {
		rule = "6.2.4"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	for num, data := range collectContentStreamData(doc) {
		for _, intent := range inlineImageIntents(data) {
			if standardRenderingIntents[intent] {
				continue
			}
			msg := "inline image /Intent uses a non-standard rendering intent"
			if seen[msg] {
				continue
			}
			seen[msg] = true
			errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: num})
		}
	}
	return errs
}

// inlineImageIntents extracts the /Intent value of every inline image.
func inlineImageIntents(data []byte) []string {
	var out []string
	n := len(data)
	i := 0
	for i < n {
		if data[i] == 'B' && i+1 < n && data[i+1] == 'I' &&
			(i == 0 || isContentWS(data[i-1]) || isContentDelim(data[i-1])) &&
			(i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2])) {
			i += 2
			if v := inlineImageDictValue(data, &i, "Intent"); v != "" {
				out = append(out, v)
			}
			continue
		}
		i++
	}
	return out
}

// inlineImageDictValue reads the named key's name value from an inline image
// parameter dictionary, advancing past ID.
func inlineImageDictValue(data []byte, pos *int, key string) string {
	n := len(data)
	i := *pos
	var pendingKey, value string
	readName := func() string {
		i++
		start := i
		for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
			i++
		}
		return string(data[start:i])
	}
	for i < n {
		switch b := data[i]; {
		case isContentWS(b):
			i++
		case b == '/':
			name := readName()
			if pendingKey == key {
				value = name
				pendingKey = ""
			} else {
				pendingKey = name
			}
		case b == 'I' && i+1 < n && data[i+1] == 'D':
			*pos = i + 2
			skipInlineImage(data, pos)
			return value
		default:
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
			if i == start {
				i++ // delimiter or other single byte
			}
			pendingKey = ""
		}
	}
	*pos = i
	return value
}

// checkInlineImageFilters verifies that every inline image's /F (Filter)
// entry uses only permitted filters and never LZW.
func checkInlineImageFilters(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.1.10"
	if level == PDFA4 {
		rule = "6.1.9"
	} else if level == PDFA1b {
		rule = "6.1.7"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	for num, data := range collectContentStreamData(doc) {
		for _, filters := range inlineImageFilters(data) {
			for _, f := range filters {
				switch {
				case inlineLZWNames[f]:
					add("LZW compression is used in an inline image", num)
				case !inlineFilterNames[f]:
					add(fmt.Sprintf("the inline image /F filter %q is not a permitted filter name", f), num)
				}
			}
		}
	}
	return errs
}

// inlineImageFilters extracts the /F (or /Filter) filter name(s) of every
// inline image in a content stream.
func inlineImageFilters(data []byte) [][]string {
	var out [][]string
	n := len(data)
	i := 0
	for i < n {
		// Find a BI token at a boundary.
		if data[i] == 'B' && i+1 < n && data[i+1] == 'I' &&
			(i == 0 || isContentWS(data[i-1]) || isContentDelim(data[i-1])) &&
			(i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2])) {
			i += 2
			filters := parseInlineImageFilter(data, &i) // advances past ID
			if filters != nil {
				out = append(out, filters)
			}
			continue
		}
		i++
	}
	return out
}

// parseInlineImageFilter reads the inline-image parameter dictionary up to
// the ID keyword, returning the /F (or /Filter) value as a list of names.
func parseInlineImageFilter(data []byte, pos *int) []string {
	n := len(data)
	i := *pos
	var filters []string
	var pendingKey string
	readName := func() string {
		i++ // past '/'
		start := i
		for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
			i++
		}
		return string(data[start:i])
	}
	for i < n {
		switch b := data[i]; {
		case isContentWS(b):
			i++
		case b == '/':
			name := readName()
			if pendingKey == "F" || pendingKey == "Filter" {
				filters = append(filters, name)
				pendingKey = ""
			} else {
				pendingKey = name
			}
		case b == '[':
			i++
			if pendingKey == "F" || pendingKey == "Filter" {
				for i < n && data[i] != ']' {
					if data[i] == '/' {
						filters = append(filters, readName())
					} else {
						i++
					}
				}
				if i < n {
					i++ // past ']'
				}
				pendingKey = ""
			}
		case b == 'I' && i+1 < n && data[i+1] == 'D':
			*pos = i + 2
			skipInlineImage(data, pos)
			return filters
		default:
			// numbers, booleans, etc. — a value clears any pending key.
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
			if i == start {
				i++
			}
			pendingKey = ""
		}
	}
	*pos = i
	return filters
}

// checkStreamLength enforces that a stream's /Length entry equals the actual
// number of bytes between the stream and endstream keywords (ISO 19005-1
// 6.1.7, -2/-3 6.1.7, -4 6.1.6; ISO 32000-1 7.3.8.2). The parser recovers a
// stream with an incorrect Length by locating endstream, so Stream.Data holds
// the true byte count and a divergence from the declared value is a mismatch.
func checkStreamLength(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.1.7" // 6.1.7 in ISO 19005-1
	switch level {
	case PDFA4:
		rule = "6.1.6.1"
	case PDFA2b, PDFA3b:
		rule = "6.1.7.1"
	}
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		s, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		length, ok := doc.Resolve(s.Dict.Get("Length")).(Integer)
		if !ok {
			continue // absent or unresolvable Length is a separate rule
		}
		if int(length) != len(s.Data) {
			errs = append(errs, ValidationError{Rule: rule, Level: level,
				Message: "the value of the Length key does not match the actual number of bytes in the stream",
				Object:  num})
		}
	}
	return errs
}

// checkObjectStreamDecodable flags an object stream whose compressed contents
// could not be decoded (ISO 32000-1 7.5.7, 7.3.8): such a stream is malformed,
// and the objects it should provide are unavailable.
func checkObjectStreamDecodable(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.1.7"
	if level == PDFA4 {
		rule = "6.1.6"
	}
	var errs []ValidationError
	for _, num := range doc.brokenObjStms {
		errs = append(errs, ValidationError{Rule: rule, Level: level,
			Message: "an object stream could not be decoded (malformed stream data)", Object: num})
	}
	return errs
}

// checkLinearizedTrailerID enforces ISO 19005-1 6.1.3 for linearized files: the
// file identifier (/ID) in the first-page trailer and the last trailer shall be
// the same. encoding/xml is not involved here — this is a byte-level scan of
// the traditional trailers a linearized PDF/A-1 file uses. Gated on
// /Linearized: a non-linearized incremental-update file legitimately carries
// several trailers, and comparing them there produced a false positive.
func checkLinearizedTrailerID(raw []byte, level PDFALevel) []ValidationError {
	if !bytes.Contains(raw, []byte("/Linearized")) {
		return nil
	}
	ids := collectTrailerIDFirstElements(raw)
	if len(ids) >= 2 && !bytes.Equal(ids[0], ids[len(ids)-1]) {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "linearized file: the file identifier /ID in the first-page trailer and the last trailer differ",
		}}
	}
	return nil
}

// collectTrailerIDFirstElements returns the first element of the /ID array of
// every traditional "trailer" dictionary in raw, in file order.
func collectTrailerIDFirstElements(raw []byte) [][]byte {
	var ids [][]byte
	for i := 0; ; {
		idx := bytes.Index(raw[i:], []byte("trailer"))
		if idx < 0 {
			break
		}
		at := i + idx
		i = at + 7
		p := NewParser(raw)
		p.Lexer().SetPosition(int64(at + 7))
		obj, err := p.ParseObject()
		if err != nil {
			continue
		}
		d, ok := obj.(*Dictionary)
		if !ok {
			continue
		}
		if arr, ok := d.Get("ID").(Array); ok && len(arr) >= 1 {
			if s, ok := arr[0].(String); ok {
				ids = append(ids, append([]byte(nil), s.Value...))
			}
		}
	}
	return ids
}

// checkStreamLengthBytes verifies each uncompressed stream's /Length against the
// byte extent measured from the raw file (ISO 32000-1 7.3.8.2; PDF/A 6.1.7 /
// 6.1.6). The parser recovers from a wrong /Length by searching for endstream,
// so the in-memory Stream.Data can mask the defect; this measures the file
// directly.
//
// Two spec facts make the measurement unambiguous and false-positive-free:
//   - Only white space may appear between endstream and endobj (7.3.8.1), so the
//     real endstream is the one just before endobj even when the stream data
//     itself contains the bytes "endstream".
//   - The optional end-of-line marker before endstream is not counted in the
//     length, and a lone CARRIAGE RETURN may be data (NOTE 2). The declared
//     length is therefore valid within [raw-eol, raw-1] when an EOL is present
//     (raw exactly when none is), and only a length outside that range — such as
//     one that wrongly includes the whole EOL — is a violation.
func checkStreamLengthBytes(doc *Document, level PDFALevel, raw []byte) []ValidationError {
	rule := "6.1.7" // 6.1.7 in ISO 19005-1
	switch level {
	case PDFA4:
		rule = "6.1.6.1"
	case PDFA2b, PDFA3b:
		rule = "6.1.7.1"
	}
	// Locate every delimited "stream" and "endobj" keyword once, up front, so
	// each object's extent is a binary search rather than a fresh forward scan.
	// Searching per object made this O(objects × filesize): a 27 MB file with
	// ~40k streams spent >80 s here because each scan restarted from the object's
	// offset. The single-pass precompute is O(filesize) total.
	streamKW := allDelimitedKeywords(raw, "stream", true)
	endobjKW := allDelimitedKeywords(raw, "endobj", true)

	var errs []ValidationError
	for num, off := range doc.Offsets {
		iobj := doc.Objects[num]
		if iobj == nil {
			continue
		}
		s, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		declared, ok := doc.Resolve(s.Dict.Get("Length")).(Integer)
		if !ok {
			continue
		}
		rawLen, eol, ok := streamByteExtent(raw, off, streamKW, endobjKW)
		if !ok {
			continue
		}
		hi := rawLen
		if eol > 0 {
			hi = rawLen - 1
		}
		lo := rawLen - eol
		if int64(declared) < lo || int64(declared) > hi {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "the value of the Length key does not match the actual number of bytes in the stream",
				Object:  num,
			})
		}
	}
	return errs
}

// allDelimitedKeywords returns, in ascending order, every offset where keyword
// occurs as a delimited token in data: preceded by whitespace (when
// requireLeadingWS) and followed by a non-regular byte or end of input — the
// same match findDelimitedKeyword makes, collected in a single O(filesize) pass
// so callers can binary-search instead of re-scanning per object.
func allDelimitedKeywords(data []byte, keyword string, requireLeadingWS bool) []int64 {
	marker := []byte(keyword)
	var out []int64
	for from := 0; from < len(data); {
		idx := bytes.Index(data[from:], marker)
		if idx < 0 {
			break
		}
		at := from + idx
		end := at + len(marker)
		beforeOK := !requireLeadingWS || at == 0 || isWhitespace(data[at-1])
		afterOK := end >= len(data) || !isRegular(data[end])
		if beforeOK && afterOK {
			out = append(out, int64(at))
		}
		from = at + 1
	}
	return out
}

// firstKeywordAtOrAfter returns the first offset in the ascending slice that is
// >= pos, or -1 if none — the binary-search equivalent of a forward keyword scan
// from pos.
func firstKeywordAtOrAfter(sorted []int64, pos int64) int64 {
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= pos })
	if i < len(sorted) {
		return sorted[i]
	}
	return -1
}

// streamByteExtent returns, for the stream object beginning at objStart, the
// number of raw bytes between the EOL after the stream keyword and the real
// endstream (anchored on endobj), and the length of the trailing EOL before
// that endstream (0, 1, or 2). ok is false if the structure cannot be located.
// streamKW and endobjKW are the precomputed delimited-keyword offsets (see
// allDelimitedKeywords).
func streamByteExtent(data []byte, objStart int64, streamKW, endobjKW []int64) (rawLen, eol int64, ok bool) {
	// Locate the stream keyword as a delimited token, not a substring: an
	// embedded-file dictionary can contain the letters "stream" in a value, and
	// matching that would place the data start too early.
	sk := firstKeywordAtOrAfter(streamKW, objStart)
	if sk < 0 {
		return 0, 0, false
	}
	ds := sk + 6
	if ds < int64(len(data)) && data[ds] == '\r' {
		ds++
	}
	if ds < int64(len(data)) && data[ds] == '\n' {
		ds++
	}
	endobj := firstKeywordAtOrAfter(endobjKW, ds)
	if endobj < 0 {
		return 0, 0, false
	}
	j := endobj - 1
	for j >= ds && isWhitespace(data[j]) {
		j--
	}
	if j-8 < ds || string(data[j-8:j+1]) != "endstream" {
		return 0, 0, false
	}
	esStart := j - 8
	rawLen = esStart - ds
	if esStart-2 >= ds && data[esStart-1] == '\n' && data[esStart-2] == '\r' {
		eol = 2
	} else if esStart-1 >= ds && (data[esStart-1] == '\n' || data[esStart-1] == '\r') {
		eol = 1
	}
	return rawLen, eol, true
}
