package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
	"math"
	"sort"
	"strconv"
)

// This file implements the cross-reference machinery: parsing traditional xref
// tables (ISO 32000-2 7.5.4) and cross-reference streams (7.5.8) into one
// XRefTable, plus the stream-decoding entry point (/Filter, /DecodeParms) the
// rest of the reader shares. Entries are read line by line rather than as the
// spec's fixed 20-byte records, because real files pad them inconsistently.
//
// It also holds the last-resort recovery for a file whose own cross-reference
// data is unusable: rebuildXRefByScan reconstructs the table from "N G obj"
// headers found in the raw bytes (7.3.10), and findTrailerByScan recovers the
// newest trailer carrying /Root (7.5.6). A scanned table has no authority
// beyond the bytes it points at — a header-shaped run inside a stream body
// fabricates entries — so callers must load it leniently.

// XRefEntry is one in-use cross-reference entry: an object stored at a byte
// offset (a traditional "n" line or a type-1 stream entry), or an object stored
// inside an object stream (a type-2 stream entry, Compressed set).
//
// Free entries are not XRefEntry values; see XRefTable.Free.
type XRefEntry struct {
	Offset        int64 // byte offset of "N G obj", as the file states it (type 1)
	Generation    int   // 0..65535 (type 1); always 0 for a compressed object
	Compressed    bool  // stored in an object stream (type 2)
	StreamObjNum  int   // object stream containing this object (type 2)
	IndexInStream int   // index within that object stream (type 2)
}

// XRefRange is a run of consecutive object numbers: Start, Start+1, …,
// Start+Count-1.
type XRefRange struct {
	Start, Count int
}

// XRefTable is one cross-reference section's entries.
//
// In-use entries are held one per object number in Entries. Free entries are
// held as runs of object numbers in Free, sorted and non-overlapping, and carry
// neither their generation nor their free-list link: pdf0 never reuses a free
// number, so neither is needed, and holding them per number is what let a
// 19.6 KB cross-reference stream declaring twenty million free entries exhaust
// memory (audit 2026-09-22 C7). IsFree answers whether a number is free.
//
// When one section lists a number both in use and free, the in-use entry wins
// and the number is not in Free. That is the precedence ISO 32000-2 7.5.8.4
// gives a hybrid-reference section, whose table marks an object free while its
// /XRefStm stream holds it; a table or stream that lists one number twice is
// malformed, and keeping the object is the reading that loses nothing.
type XRefTable struct {
	Entries map[int]XRefEntry
	Free    []XRefRange
}

// IsFree reports whether the table marks num free (and does not also list it
// in use).
func (t *XRefTable) IsFree(num int) bool {
	if t == nil {
		return false
	}
	if _, used := t.Entries[num]; used {
		return false
	}
	i := sort.Search(len(t.Free), func(i int) bool { return t.Free[i].Start+t.Free[i].Count > num })
	return i < len(t.Free) && t.Free[i].Start <= num
}

// addFree records num as free, extending the last run when num continues it.
// normalizeFree sorts and merges the runs once parsing is done.
func (t *XRefTable) addFree(num int) {
	if n := len(t.Free); n > 0 && t.Free[n-1].Start+t.Free[n-1].Count == num {
		t.Free[n-1].Count++
		return
	}
	t.Free = append(t.Free, XRefRange{Start: num, Count: 1})
}

// normalizeFree sorts Free, merges overlapping and adjacent runs, and removes
// the numbers Entries lists in use, so that IsFree's binary search is valid and
// in-use wins within the section.
func (t *XRefTable) normalizeFree() {
	t.Free = mergeRanges(t.Free)
	if len(t.Entries) == 0 || len(t.Free) == 0 {
		return
	}
	// Collect the in-use numbers that fall inside a free run. There are none in
	// a well-formed section, so this is one pass over Entries with a binary
	// search each, and no sort. Walking the in-use numbers rather than the free
	// ones matters too: a run can span millions of numbers, and splitting it
	// costs only the in-use numbers inside it.
	var used []int
	for n := range t.Entries {
		i := sort.Search(len(t.Free), func(i int) bool { return t.Free[i].Start+t.Free[i].Count > n })
		if i < len(t.Free) && t.Free[i].Start <= n {
			used = append(used, n)
		}
	}
	if len(used) == 0 {
		return
	}
	sort.Ints(used)
	var out []XRefRange
	for _, r := range t.Free {
		start, end := r.Start, r.Start+r.Count
		for i := sort.SearchInts(used, start); i < len(used) && used[i] < end; i++ {
			if used[i] > start {
				out = append(out, XRefRange{Start: start, Count: used[i] - start})
			}
			start = used[i] + 1
		}
		if end > start {
			out = append(out, XRefRange{Start: start, Count: end - start})
		}
	}
	t.Free = out
}

// mergeRanges returns rs sorted by Start with overlapping and adjacent runs
// merged. Ranges never extend past MaxObjectNumber, so Start+Count cannot
// overflow.
func mergeRanges(rs []XRefRange) []XRefRange {
	if len(rs) < 2 {
		return rs
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.Start+last.Count {
			if end := r.Start + r.Count; end > last.Start+last.Count {
				last.Count = end - last.Start
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// checkSubsection validates a subsection's first object number and entry
// count: both non-negative, and every number in it at most MaxObjectNumber.
// Written as a subtraction, the upper-bound check cannot overflow the way
// start+count does for a start near MaxInt64 (audit 2026-09-22 C128).
func checkSubsection(start, count int64) error {
	if start < 0 || count < 0 {
		return fmt.Errorf("negative start object %d or count %d", start, count)
	}
	if start > syntax.MaxObjectNumber || count > syntax.MaxObjectNumber-start+1 {
		return fmt.Errorf("subsection %d+%d runs past the largest object number, %d", start, count, syntax.MaxObjectNumber)
	}
	return nil
}

// inUseBudget bounds how many in-use entries one Read materialises across its
// whole cross-reference chain, relative to the size of the file.
//
// Every in-use entry names an object the file must hold, either at a byte
// offset or inside an object stream, and every such object costs bytes of the
// file: the smallest possible top-level object ("1 0 obj 1 endobj") is 16 bytes,
// and a compressed one costs its index pair in the object stream plus its entry
// in a cross-reference stream. One entry per four bytes of file is therefore
// far above what a file can plausibly hold — the densest file in the veraPDF,
// Factur-X, WTPDF, PDF/VT and PDF 2.0 sets holds one per 135 bytes — while
// still tying the memory the table costs to the size of the input. The floor
// keeps a small file's table from being refused over a handful of entries.
//
// What the budget stops is a cross-reference stream that decodes to far more
// entries than its file could hold: 19.6 KB of Flate data decodes to twenty
// million entries (audit 2026-09-22 C7).
type inUseBudget struct {
	left int64 // entries still allowed; negative means unbounded
}

const inUseBudgetFloor = 4096

func newFileInUseBudget(fileSize int64) *inUseBudget {
	return &inUseBudget{left: inUseBudgetFloor + fileSize/4}
}

// take charges one entry, failing once the budget is spent.
func (b *inUseBudget) take() error {
	if b == nil || b.left < 0 {
		return nil
	}
	if b.left == 0 {
		return fmt.Errorf("the cross-reference data lists more in-use objects than the file could hold")
	}
	b.left--
	return nil
}

// ParseXRefTable parses a traditional xref table starting at the given position.
// The position should be right after the "xref" keyword.
//
// A table's in-use entries each occupy a line of data, so the table cannot list
// more of them than its input holds, and no further bound is applied.
func ParseXRefTable(data []byte, pos int64) (*XRefTable, error) {
	return parseXRefTable(data, pos, nil)
}

func parseXRefTable(data []byte, pos int64, budget *inUseBudget) (*XRefTable, error) {
	table := &XRefTable{
		Entries: make(map[int]XRefEntry),
	}

	for {
		// Skip whitespace
		for pos < int64(len(data)) && syntax.IsWhitespace(data[pos]) {
			pos++
		}
		if pos >= int64(len(data)) {
			break
		}

		// Check if we've reached "trailer"
		if pos+7 <= int64(len(data)) && string(data[pos:pos+7]) == "trailer" {
			break
		}

		// Parse subsection header: start count
		lineEnd := pos
		for lineEnd < int64(len(data)) && data[lineEnd] != '\r' && data[lineEnd] != '\n' {
			lineEnd++
		}
		line := string(data[pos:lineEnd])
		parts := splitFields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid xref subsection header %q at offset %d", line, pos)
		}

		start64, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid start object number %q: %w", parts[0], err)
		}
		count64, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid object count %q: %w", parts[1], err)
		}
		if err := checkSubsection(start64, count64); err != nil {
			return nil, fmt.Errorf("invalid xref subsection header %q: %w", line, err)
		}
		startObj, count := int(start64), int(count64)

		// Skip past the header line
		pos = lineEnd
		if pos < int64(len(data)) && data[pos] == '\r' {
			pos++
		}
		if pos < int64(len(data)) && data[pos] == '\n' {
			pos++
		}

		// Parse entries line by line (handles both 20-byte and other variations)
		for i := 0; i < count; i++ {
			// Read to end of line
			entryEnd := pos
			for entryEnd < int64(len(data)) && data[entryEnd] != '\r' && data[entryEnd] != '\n' {
				entryEnd++
			}
			entryLine := string(data[pos:entryEnd])

			// Skip EOL
			pos = entryEnd
			if pos < int64(len(data)) && data[pos] == '\r' {
				pos++
			}
			if pos < int64(len(data)) && data[pos] == '\n' {
				pos++
			}

			// Parse: "0000000000 00000 n" or "0000000000 00000 f"
			fields := splitFields(entryLine)
			if len(fields) != 3 {
				return nil, fmt.Errorf("invalid xref entry %q at offset %d (expected 3 fields, got %d)", entryLine, pos, len(fields))
			}

			offset, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid offset in xref entry: %q", fields[0])
			}

			gen, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("invalid generation in xref entry: %q", fields[1])
			}

			objNum := startObj + i
			if fields[2] == "f" {
				table.addFree(objNum)
				continue
			}
			// An in-use generation above 65535 cannot be written back and
			// the object header it names is refused by the parser; see
			// syntax.MaxGeneration (audit 2026-09-22 C125).
			if gen < 0 || gen > syntax.MaxGeneration {
				return nil, fmt.Errorf("xref entry for object %d has generation %d, outside 0..%d", objNum, gen, syntax.MaxGeneration)
			}
			if err := budget.take(); err != nil {
				return nil, err
			}
			table.Entries[objNum] = XRefEntry{Offset: offset, Generation: gen}
		}
	}

	table.normalizeFree()
	return table, nil
}

// ParseXRefStream parses a cross-reference stream.
//
// Resource limits default to values safe for untrusted input; pass With*
// options to change them. (*Document) supplies its own resolved limits when it
// calls this during Read, so a document read with options keeps them here.
//
// With no file to measure it against, the number of in-use entries is bounded
// by the stream's own encoded length: at most 64 per byte (the densest stream
// in the test corpora holds 11), plus a floor of 4096. Read bounds the entries
// of every section by the size of the whole file instead.
func ParseXRefStream(stream *object.Stream, opts ...Option) (*XRefTable, error) {
	if stream == nil {
		return nil, fmt.Errorf("xref stream is nil")
	}
	lim, err := resolveLimits(opts)
	if err != nil {
		return nil, err
	}
	budget := &inUseBudget{left: inUseBudgetFloor + 64*int64(len(stream.Data))}
	return parseXRefStream(core.Canceler{}, stream, lim, budget)
}

// maxXRefFieldWidth is the widest /W field pdf0 reads: eight bytes, the width
// of the int64 an offset is held in. ISO 32000-2 Table 17 leaves the widths to
// the writer; a wider field can only carry leading zeros or a value no file
// offset reaches, and summing unbounded widths is what overflowed and panicked
// (audit 2026-09-22 C101).
const maxXRefFieldWidth = 8

func parseXRefStream(cancel core.Canceler, stream *object.Stream, lim core.Limits, budget *inUseBudget) (*XRefTable, error) {
	table := &XRefTable{
		Entries: make(map[int]XRefEntry),
	}

	// Get W array (field widths)
	wObj := stream.Dict.Get("W")
	if wObj == nil {
		return nil, fmt.Errorf("xref stream missing /W entry")
	}
	wArr, ok := wObj.(object.Array)
	if !ok || len(wArr) != 3 {
		return nil, fmt.Errorf("xref stream /W must be array of 3 integers")
	}

	var w [3]int
	for i, obj := range wArr {
		iv, ok := obj.(object.Integer)
		if !ok {
			return nil, fmt.Errorf("xref stream /W[%d] is not an integer", i)
		}
		if iv < 0 || iv > maxXRefFieldWidth {
			return nil, fmt.Errorf("xref stream /W[%d] is %d, outside 0..%d", i, iv, maxXRefFieldWidth)
		}
		w[i] = int(iv)
	}
	entrySize := w[0] + w[1] + w[2]
	if entrySize == 0 {
		return nil, fmt.Errorf("xref stream /W field widths sum to zero")
	}

	// Get Index array (default: [0 Size])
	var indices []int
	indexObj := stream.Dict.Get("Index")
	if indexObj != nil {
		indexArr, ok := indexObj.(object.Array)
		if !ok {
			return nil, fmt.Errorf("xref stream /Index is not an array")
		}
		if len(indexArr)%2 != 0 {
			return nil, fmt.Errorf("xref stream /Index must have an even number of elements, got %d", len(indexArr))
		}
		for i := 0; i+1 < len(indexArr); i += 2 {
			start, ok1 := indexArr[i].(object.Integer)
			count, ok2 := indexArr[i+1].(object.Integer)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("xref stream /Index element is not an integer")
			}
			// Negative values, or a run past the largest object number, would
			// index the object table out of range or wrap start+count negative
			// (audit C38, and 2026-09-22 C128).
			if err := checkSubsection(int64(start), int64(count)); err != nil {
				return nil, fmt.Errorf("xref stream /Index: %w", err)
			}
			indices = append(indices, int(start), int(count))
		}
	} else {
		sizeObj := stream.Dict.Get("Size")
		if sizeObj == nil {
			return nil, fmt.Errorf("xref stream missing /Size")
		}
		size, ok := sizeObj.(object.Integer)
		if !ok {
			return nil, fmt.Errorf("xref stream /Size is not an integer")
		}
		if err := checkSubsection(0, int64(size)); err != nil {
			return nil, fmt.Errorf("xref stream /Size: %w", err)
		}
		indices = []int{0, int(size)}
	}

	// Decompress stream data
	// No resolver: this runs before there is an object table, and ISO 32000-2
	// 7.5.8.2 requires the dictionary's entries to be direct.
	streamData, err := core.DecodeStreamData(cancel, stream, lim, nil)
	if err != nil {
		return nil, fmt.Errorf("decoding xref stream data: %w", err)
	}

	// Every declared entry must be present. Checking the total up front, in
	// int64, means a count the data cannot back is refused before the loop.
	var declared int64
	for i := 1; i < len(indices); i += 2 {
		declared += int64(indices[i])
	}
	if declared > int64(len(streamData))/int64(entrySize) {
		return nil, fmt.Errorf("xref stream data truncated: %d entries of %d bytes declared, %d bytes present", declared, entrySize, len(streamData))
	}

	// Parse entries
	offset := 0
	for i := 0; i < len(indices); i += 2 {
		startObj := indices[i]
		count := indices[i+1]
		if err := cancel.StopErr("reading PDF cross-reference stream"); err != nil {
			return nil, err
		}

		for j := 0; j < count; j++ {
			field1 := readField(streamData[offset:], w[0])
			field2 := readField(streamData[offset+w[0]:], w[1])
			field3 := readField(streamData[offset+w[0]+w[1]:], w[2])
			offset += entrySize

			objNum := startObj + j

			// Default type is 1 if w[0] == 0
			entryType := field1
			if w[0] == 0 {
				entryType = 1
			}

			switch entryType {
			case 1: // uncompressed entry
				if field3 > syntax.MaxGeneration {
					return nil, fmt.Errorf("xref stream entry for object %d has generation %d, outside 0..%d", objNum, field3, syntax.MaxGeneration)
				}
				if err := budget.take(); err != nil {
					return nil, err
				}
				table.Entries[objNum] = XRefEntry{
					Offset:     fieldInt64(field2),
					Generation: int(field3),
				}
			case 2: // compressed entry
				if err := budget.take(); err != nil {
					return nil, err
				}
				table.Entries[objNum] = XRefEntry{
					Compressed:    true,
					StreamObjNum:  fieldObjNum(field2),
					IndexInStream: fieldObjNum(field3),
				}
			default:
				// Type 0 is a free entry. Any other type "shall be interpreted
				// as a reference to the null object" (ISO 32000-2 Table 18),
				// which is what a free entry means to a reader too, and it
				// likewise hides an older section's definition.
				table.addFree(objNum)
			}
		}
	}

	table.normalizeFree()
	return table, nil
}

// readField reads a big-endian unsigned integer of the given width (at most
// maxXRefFieldWidth) from data.
func readField(data []byte, width int) uint64 {
	var val uint64
	for i := 0; i < width && i < len(data); i++ {
		val = val<<8 | uint64(data[i])
	}
	return val
}

// fieldInt64 converts a field holding a byte offset, mapping a value no int64
// holds to -1, which every consumer rejects as outside the file.
func fieldInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return -1
	}
	return int64(v)
}

// fieldObjNum converts a field holding an object number or an index, mapping a
// value above MaxObjectNumber to -1: no object stream has that number and no
// index reaches it, so the entry is later reported as a broken object stream
// rather than wrapping into a plausible-looking small number.
func fieldObjNum(v uint64) int {
	if v > syntax.MaxObjectNumber {
		return -1
	}
	return int(v)
}

// The maximum size of decompressed stream data defaults to
// defaultMaxDecodedStreamBytes; a caller can change it with
// WithMaxDecodedStreamBytes. This prevents decompression bombs from consuming
// excessive memory.

// splitFields splits a string by whitespace into non-empty fields.
func splitFields(s string) []string {
	var fields []string
	start := -1
	for i := 0; i < len(s); i++ {
		if syntax.IsWhitespace(s[i]) {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		} else {
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

// rebuildXRefByScan reconstructs a cross-reference table by scanning the raw
// bytes for indirect-object headers. It is the last-resort recovery for a file
// whose cross-reference data is unusable (a table that does not parse, or one
// whose offsets do not land on the objects they promise).
//
// The scan recognizes the header form ISO 32000-2, 7.3.10 defines: an object
// number (a positive integer), a generation number (a non-negative integer not
// exceeding 65535) and the keyword "obj", each delimited. When the same object
// number is defined more than once, the definition later in the file wins —
// the same precedence 7.5.6 gives objects re-defined by incremental updates.
// A header-shaped byte run inside a stream body can produce a bogus entry;
// the caller loads rebuilt tables leniently, dropping entries that do not
// parse as objects.
func rebuildXRefByScan(data []byte) *XRefTable {
	table := &XRefTable{Entries: make(map[int]XRefEntry)}
	for i := 0; i+3 <= len(data); {
		j := bytes.Index(data[i:], []byte("obj"))
		if j < 0 {
			break
		}
		pos := i + j
		i = pos + 3
		// The keyword must be delimited on both sides ("endobj" has 'd'
		// before; "objx" has a regular character after).
		if pos+3 < len(data) && !syntax.IsWhitespace(data[pos+3]) && !syntax.IsDelimiter(data[pos+3]) {
			continue
		}
		if pos == 0 || !syntax.IsWhitespace(data[pos-1]) {
			continue
		}
		// Backtrack over: whitespace, generation digits, whitespace, object
		// number digits.
		k := pos - 1
		for k >= 0 && syntax.IsWhitespace(data[k]) {
			k--
		}
		genEnd := k + 1
		for k >= 0 && data[k] >= '0' && data[k] <= '9' {
			k--
		}
		genStart := k + 1
		if genStart == genEnd || genEnd-genStart > 5 {
			continue
		}
		if k < 0 || !syntax.IsWhitespace(data[k]) {
			continue
		}
		for k >= 0 && syntax.IsWhitespace(data[k]) {
			k--
		}
		numEnd := k + 1
		for k >= 0 && data[k] >= '0' && data[k] <= '9' {
			k--
		}
		numStart := k + 1
		if numStart == numEnd || numEnd-numStart > 9 {
			continue
		}
		// The object number must itself be delimited (start of file,
		// whitespace or a delimiter before it).
		if numStart > 0 && !syntax.IsWhitespace(data[numStart-1]) && !syntax.IsDelimiter(data[numStart-1]) {
			continue
		}
		num, err1 := strconv.Atoi(string(data[numStart:numEnd]))
		gen, err2 := strconv.Atoi(string(data[genStart:genEnd]))
		// Object number 0 is the reserved free-list head and can never be an
		// in-use object (7.5.4); generations cap at 65535.
		if err1 != nil || err2 != nil || num < 1 || gen > syntax.MaxGeneration {
			continue
		}
		table.Entries[num] = XRefEntry{Offset: int64(numStart), Generation: gen}
	}
	if len(table.Entries) == 0 {
		return nil
	}
	return table
}

// findTrailerByScan locates the file's trailer dictionary when the
// cross-reference chain could not provide one: it scans for every delimited
// "trailer" keyword and returns the last dictionary that parses and carries
// /Root — the trailer of the newest update (7.5.6). It returns nil if none
// qualifies.
func findTrailerByScan(data []byte) *object.Dictionary {
	var best *object.Dictionary
	for i := 0; ; {
		j := bytes.Index(data[i:], []byte("trailer"))
		if j < 0 {
			break
		}
		pos := i + j
		i = pos + 7
		if pos > 0 && !syntax.IsWhitespace(data[pos-1]) && !syntax.IsDelimiter(data[pos-1]) {
			continue
		}
		if pos+7 < len(data) && !syntax.IsWhitespace(data[pos+7]) && !syntax.IsDelimiter(data[pos+7]) {
			continue
		}
		lx := NewLexer(data)
		lx.SetPosition(int64(pos + 7))
		dict, err := NewParserFromLexer(lx).ParseObject()
		if err != nil {
			continue
		}
		if d, ok := dict.(*object.Dictionary); ok && d.Get("Root") != nil {
			best = d
		}
	}
	return best
}
