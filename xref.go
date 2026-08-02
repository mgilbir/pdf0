package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/syntax"
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

// XRefEntry represents a single cross-reference table entry.
type XRefEntry struct {
	Offset        int64
	Generation    int
	Free          bool
	Compressed    bool
	StreamObjNum  int // object stream containing this object
	IndexInStream int // index within object stream
}

// XRefTable holds all cross-reference entries indexed by object number.
type XRefTable struct {
	Entries map[int]XRefEntry
}

// ParseXRefTable parses a traditional xref table starting at the given position.
// The position should be right after the "xref" keyword.
func ParseXRefTable(data []byte, pos int64) (*XRefTable, error) {
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

		startObj, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid start object number %q: %w", parts[0], err)
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid object count %q: %w", parts[1], err)
		}
		if startObj < 0 || count < 0 {
			return nil, fmt.Errorf("invalid xref subsection header %q: negative start or count", line)
		}

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

			entryType := fields[2]
			objNum := startObj + i

			entry := XRefEntry{
				Offset:     offset,
				Generation: gen,
				Free:       entryType == "f",
			}

			table.Entries[objNum] = entry
		}
	}

	return table, nil
}

// ParseXRefStream parses a cross-reference stream.
//
// Resource limits default to values safe for untrusted input; pass With*
// options to change them. (*Document) supplies its own resolved limits when it
// calls this during Read, so a document read with options keeps them here.
func ParseXRefStream(stream *Stream, opts ...Option) (*XRefTable, error) {
	return parseXRefStream(core.Canceler{}, stream, resolveLimits(opts))
}

func parseXRefStream(cancel core.Canceler, stream *Stream, lim core.Limits) (*XRefTable, error) {
	table := &XRefTable{
		Entries: make(map[int]XRefEntry),
	}

	// Get W array (field widths)
	wObj := stream.Dict.Get("W")
	if wObj == nil {
		return nil, fmt.Errorf("xref stream missing /W entry")
	}
	wArr, ok := wObj.(Array)
	if !ok || len(wArr) != 3 {
		return nil, fmt.Errorf("xref stream /W must be array of 3 integers")
	}

	w := make([]int, 3)
	for i, obj := range wArr {
		iv, ok := obj.(Integer)
		if !ok {
			return nil, fmt.Errorf("xref stream /W[%d] is not an integer", i)
		}
		if iv < 0 {
			return nil, fmt.Errorf("xref stream /W[%d] is negative (%d)", i, iv)
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
		indexArr, ok := indexObj.(Array)
		if !ok {
			return nil, fmt.Errorf("xref stream /Index is not an array")
		}
		for _, obj := range indexArr {
			iv, ok := obj.(Integer)
			if !ok {
				return nil, fmt.Errorf("xref stream /Index element is not an integer")
			}
			indices = append(indices, int(iv))
		}
		if len(indices)%2 != 0 {
			return nil, fmt.Errorf("xref stream /Index must have an even number of elements, got %d", len(indices))
		}
		// A negative start-object or count would index the object table out of
		// range; the traditional xref table already rejects this, so match it
		// here for parity (audit C38).
		for i := 0; i+1 < len(indices); i += 2 {
			if indices[i] < 0 || indices[i+1] < 0 {
				return nil, fmt.Errorf("xref stream /Index has a negative start object or count")
			}
		}
	} else {
		sizeObj := stream.Dict.Get("Size")
		if sizeObj == nil {
			return nil, fmt.Errorf("xref stream missing /Size")
		}
		size, ok := sizeObj.(Integer)
		if !ok {
			return nil, fmt.Errorf("xref stream /Size is not an integer")
		}
		indices = []int{0, int(size)}
	}

	// Decompress stream data
	streamData, err := core.DecodeStreamData(cancel, stream, lim)
	if err != nil {
		return nil, fmt.Errorf("decoding xref stream data: %w", err)
	}

	// Parse entries
	offset := 0
	for i := 0; i < len(indices); i += 2 {
		startObj := indices[i]
		count := indices[i+1]

		for j := 0; j < count; j++ {
			if offset+entrySize > len(streamData) {
				return nil, fmt.Errorf("xref stream data truncated")
			}

			// Read fields
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
			case 0: // free entry
				table.Entries[objNum] = XRefEntry{
					Offset:     int64(field2),
					Generation: field3,
					Free:       true,
				}
			case 1: // uncompressed entry
				table.Entries[objNum] = XRefEntry{
					Offset:     int64(field2),
					Generation: field3,
				}
			case 2: // compressed entry
				table.Entries[objNum] = XRefEntry{
					Compressed:    true,
					StreamObjNum:  field2,
					IndexInStream: field3,
				}
			}
		}
	}

	return table, nil
}

// readField reads a big-endian integer of the given width from data.
func readField(data []byte, width int) int {
	if width == 0 {
		return 0
	}
	val := 0
	for i := 0; i < width && i < len(data); i++ {
		val = val<<8 | int(data[i])
	}
	return val
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
		if err1 != nil || err2 != nil || num < 1 || gen > 65535 {
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
func findTrailerByScan(data []byte) *Dictionary {
	var best *Dictionary
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
		if d, ok := dict.(*Dictionary); ok && d.Get("Root") != nil {
			best = d
		}
	}
	return best
}
