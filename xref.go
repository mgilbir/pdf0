package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
)

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
		for pos < int64(len(data)) && isWhitespace(data[pos]) {
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
func ParseXRefStream(stream *Stream) (*XRefTable, error) {
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
	streamData, err := decodeStreamData(stream)
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

// decodeStreamData decompresses stream data based on the /Filter entry.
func decodeStreamData(stream *Stream) ([]byte, error) {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		// No filter, return raw data
		return stream.Data, nil
	}

	filterName, ok := filter.(Name)
	if !ok {
		// Could be an array of filters
		filterArr, ok := filter.(Array)
		if !ok {
			return nil, fmt.Errorf("unsupported filter type: %T", filter)
		}
		// Apply filters in order
		data := stream.Data
		for _, f := range filterArr {
			fname, ok := f.(Name)
			if !ok {
				return nil, fmt.Errorf("filter array element is not a Name")
			}
			var err error
			data, err = applyFilter(fname, data)
			if err != nil {
				return nil, err
			}
		}
		return data, nil
	}

	return applyFilter(filterName, stream.Data)
}

func applyFilter(name Name, data []byte) ([]byte, error) {
	switch name {
	case "FlateDecode":
		return flateDecode(data)
	case "ASCIIHexDecode":
		return asciiHexDecode(data)
	default:
		return nil, fmt.Errorf("unsupported filter: %s", name)
	}
}

// isSupportedFilter reports whether applyFilter can decode the named filter.
func isSupportedFilter(name Name) bool {
	switch name {
	case "FlateDecode", "ASCIIHexDecode":
		return true
	}
	return false
}

// streamFiltersSupported reports whether every filter on the stream is one that
// decodeStreamData can actually apply. Callers use this to tell "we could not
// inspect this stream" apart from "this stream is corrupt": a decode failure on
// an unsupported-but-legal filter must not be reported as a violation.
func streamFiltersSupported(stream *Stream) bool {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		return true
	}
	switch f := filter.(type) {
	case Name:
		return isSupportedFilter(f)
	case Array:
		for _, e := range f {
			name, ok := e.(Name)
			if !ok || !isSupportedFilter(name) {
				return false
			}
		}
		return true
	}
	return false
}

// maxDecodeSize is the maximum size of decompressed stream data (100 MB).
// This prevents decompression bombs from consuming excessive memory.
const maxDecodeSize = 100 << 20

func flateDecode(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib: %w", err)
	}
	defer r.Close()

	limited := io.LimitReader(r, maxDecodeSize+1)
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("zlib decompress: %w", err)
	}
	if len(decoded) > maxDecodeSize {
		return nil, fmt.Errorf("decompressed data exceeds maximum size (%d bytes)", maxDecodeSize)
	}
	return decoded, nil
}

func asciiHexDecode(data []byte) ([]byte, error) {
	// Filter out whitespace and stop at '>'
	var hexDigits []byte
	for _, b := range data {
		if b == '>' {
			break
		}
		if isWhitespace(b) {
			continue
		}
		hexDigits = append(hexDigits, b)
	}
	return decodeHex(hexDigits)
}

// splitFields splits a string by whitespace into non-empty fields.
func splitFields(s string) []string {
	var fields []string
	start := -1
	for i := 0; i < len(s); i++ {
		if isWhitespace(s[i]) {
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
