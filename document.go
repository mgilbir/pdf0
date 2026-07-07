package pdf0

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// Document represents a parsed PDF file.
type Document struct {
	Version string                  // e.g., "2.0"
	Objects map[int]*IndirectObject // object number → object
	Trailer Dictionary
}

// Read parses a PDF document from the given data.
func Read(r io.ReaderAt, size int64) (*Document, error) {
	data := make([]byte, size)
	_, err := r.ReadAt(data, 0)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading input: %w", err)
	}

	doc := &Document{
		Objects: make(map[int]*IndirectObject),
	}

	// 1. Find header to extract version and header offset
	version, headerOffset, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	doc.Version = version

	// 2. Find startxref and xref offset
	xrefOffset, err := findStartXref(data)
	if err != nil {
		return nil, err
	}
	// All byte offsets in the PDF are relative to the %PDF- header
	xrefOffset += headerOffset

	// 3. Parse xref and trailer
	lexer := NewLexer(data)
	lexer.SetPosition(xrefOffset)

	// Check if it's a traditional xref table or xref stream
	tok, err := lexer.NextToken()
	if err != nil {
		return nil, fmt.Errorf("reading xref: %w", err)
	}

	var xrefTable *XRefTable

	if tok.Type == TokenXref {
		// Traditional xref table
		xrefTable, err = ParseXRefTable(data, lexer.Position())
		if err != nil {
			return nil, fmt.Errorf("parsing xref table: %w", err)
		}

		// Parse trailer
		lexer.SetPosition(lexer.Position())
		// Skip to after xref entries to find trailer
		trailer, err := findTrailer(data, lexer.Position())
		if err != nil {
			return nil, fmt.Errorf("parsing trailer: %w", err)
		}
		doc.Trailer = *trailer

		// Follow Prev chain. Guard against cycles: a /Prev pointing at an
		// already-visited xref section (or at itself) would otherwise loop
		// forever on a crafted or corrupt file.
		visitedXref := map[int64]bool{xrefOffset: true}
		prevObj := doc.Trailer.Get("Prev")
		for prevObj != nil {
			prevOffset, ok := prevObj.(Integer)
			if !ok {
				break
			}
			target := int64(prevOffset) + headerOffset
			if visitedXref[target] {
				break // cycle in the /Prev chain
			}
			visitedXref[target] = true
			lexer.SetPosition(target)
			tok, err := lexer.NextToken()
			if err != nil || tok.Type != TokenXref {
				break
			}
			prevTable, err := ParseXRefTable(data, lexer.Position())
			if err != nil {
				break
			}
			// Merge: earlier entries don't override later ones
			for num, entry := range prevTable.Entries {
				if _, exists := xrefTable.Entries[num]; !exists {
					xrefTable.Entries[num] = entry
				}
			}
			// Check for further Prev
			prevTrailer, err := findTrailer(data, lexer.Position())
			if err != nil {
				break
			}
			prevObj = prevTrailer.Get("Prev")
		}
	} else if tok.Type == TokenInteger {
		// Xref stream: the xref is an indirect object containing a stream
		lexer.SetPosition(xrefOffset)
		parser := NewParserFromLexer(lexer)
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			return nil, fmt.Errorf("parsing xref stream object: %w", err)
		}
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			return nil, fmt.Errorf("xref stream object is not a stream")
		}
		xrefTable, err = ParseXRefStream(stream)
		if err != nil {
			return nil, fmt.Errorf("parsing xref stream: %w", err)
		}
		// The stream dictionary IS the trailer for xref streams
		doc.Trailer = stream.Dict

		// Store the xref stream object itself
		doc.Objects[iobj.Number] = iobj
	} else {
		return nil, fmt.Errorf("expected 'xref' or object number at offset %d, got %v", xrefOffset, tok.Type)
	}

	// 4. Parse all objects from xref entries
	for num, entry := range xrefTable.Entries {
		if entry.Free {
			continue
		}
		if entry.Compressed {
			// Object streams handled separately
			continue
		}
		if _, exists := doc.Objects[num]; exists {
			continue // already loaded (e.g., xref stream)
		}

		lexer.SetPosition(entry.Offset + headerOffset)
		parser := NewParserFromLexer(lexer)
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			return nil, fmt.Errorf("parsing object %d at offset %d: %w", num, entry.Offset, err)
		}
		doc.Objects[num] = iobj
	}

	return doc, nil
}

// parseHeader extracts the PDF version from the header and returns the header offset.
// The header offset is non-zero when data precedes the %PDF- marker.
func parseHeader(data []byte) (version string, headerOffset int64, err error) {
	// Look for %PDF-x.y in the first 1024 bytes
	searchLen := 1024
	if len(data) < searchLen {
		searchLen = len(data)
	}
	header := data[:searchLen]

	idx := bytes.Index(header, []byte("%PDF-"))
	if idx < 0 {
		return "", 0, fmt.Errorf("PDF header not found")
	}

	// Extract version (e.g., "1.7", "2.0")
	verStart := idx + 5
	verEnd := verStart
	for verEnd < len(header) && header[verEnd] != '\r' && header[verEnd] != '\n' {
		verEnd++
	}
	return string(header[verStart:verEnd]), int64(idx), nil
}

// findStartXref finds the byte offset stored after the startxref keyword.
func findStartXref(data []byte) (int64, error) {
	// Search backwards from end of file for startxref
	// Look in the last 1024 bytes
	searchLen := 1024
	if len(data) < searchLen {
		searchLen = len(data)
	}
	tail := data[len(data)-searchLen:]

	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("startxref not found")
	}

	// Skip "startxref" and whitespace to get the offset value
	pos := idx + len("startxref")
	for pos < len(tail) && isWhitespace(tail[pos]) {
		pos++
	}

	// Read digits
	numStart := pos
	for pos < len(tail) && tail[pos] >= '0' && tail[pos] <= '9' {
		pos++
	}
	if numStart == pos {
		return 0, fmt.Errorf("no offset after startxref")
	}

	offset, err := strconv.ParseInt(string(tail[numStart:pos]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid startxref offset: %w", err)
	}

	return offset, nil
}

// findTrailer finds and parses the trailer dictionary after xref entries.
func findTrailer(data []byte, afterPos int64) (*Dictionary, error) {
	// Search for "trailer" keyword after the given position
	searchData := data[afterPos:]
	idx := bytes.Index(searchData, []byte("trailer"))
	if idx < 0 {
		return nil, fmt.Errorf("trailer keyword not found after offset %d", afterPos)
	}

	// Parse the dictionary after "trailer"
	dictStart := afterPos + int64(idx) + int64(len("trailer"))
	parser := NewParser(data)
	parser.Lexer().SetPosition(dictStart)
	obj, err := parser.ParseObject()
	if err != nil {
		return nil, fmt.Errorf("parsing trailer dictionary: %w", err)
	}

	dict, ok := obj.(*Dictionary)
	if !ok {
		return nil, fmt.Errorf("trailer value is not a dictionary, got %T", obj)
	}

	return dict, nil
}

// Write serializes the document to the writer in PDF format.
func (d *Document) Write(w io.Writer) error {
	s := NewSerializer(w)

	// 1. Write header
	version := d.Version
	if version == "" {
		version = "2.0"
	}
	header := fmt.Sprintf("%%PDF-%s\n%%\x80\x80\x80\x80\n", version)
	if err := s.writeString(header); err != nil {
		return err
	}

	// 2. Collect and sort object numbers
	var objNums []int
	for num := range d.Objects {
		objNums = append(objNums, num)
	}
	sortInts(objNums)

	// 3. Write objects and record offsets
	offsets := make(map[int]int64)
	for _, num := range objNums {
		offsets[num] = s.Offset()
		if err := s.WriteIndirectObject(d.Objects[num]); err != nil {
			return fmt.Errorf("writing object %d: %w", num, err)
		}
	}

	// 4. Write xref table
	xrefOffset := s.Offset()
	if err := writeXRefTable(s, objNums, offsets, d.Objects); err != nil {
		return err
	}

	// 5. Write trailer
	// Clone so setting Size doesn't mutate the caller's Document.Trailer
	// (Dictionary shares its backing slices on a plain struct copy).
	trailer := d.Trailer.Clone()
	// Update/set Size
	maxObj := 0
	for _, num := range objNums {
		if num > maxObj {
			maxObj = num
		}
	}
	trailer.Set("Size", Integer(maxObj+1))

	if err := s.writeString("trailer\n"); err != nil {
		return err
	}
	if err := s.writeDictionary(trailer); err != nil {
		return err
	}
	if err := s.writeString("\n"); err != nil {
		return err
	}

	// 6. Write startxref
	if err := s.writeString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset)); err != nil {
		return err
	}

	return nil
}

// writeXRefTable writes a traditional xref table.
func writeXRefTable(s *Serializer, objNums []int, offsets map[int]int64, objects map[int]*IndirectObject) error {
	if err := s.writeString("xref\n"); err != nil {
		return err
	}

	// Find contiguous subsections
	maxObj := 0
	for _, num := range objNums {
		if num > maxObj {
			maxObj = num
		}
	}

	// Write single section from 0 to maxObj
	if err := s.writeString(fmt.Sprintf("0 %d\n", maxObj+1)); err != nil {
		return err
	}

	// Entry 0: free entry head
	if err := s.writeString("0000000000 65535 f \r\n"); err != nil {
		return err
	}

	for i := 1; i <= maxObj; i++ {
		if offset, ok := offsets[i]; ok {
			gen := 0
			if obj, ok := objects[i]; ok {
				gen = obj.Generation
			}
			entry := fmt.Sprintf("%010d %05d n \r\n", offset, gen)
			if err := s.writeString(entry); err != nil {
				return err
			}
		} else {
			// Free entry
			if err := s.writeString("0000000000 00000 f \r\n"); err != nil {
				return err
			}
		}
	}

	return nil
}

// Resolve follows an IndirectRef to its value. Returns the object
// unchanged if it is not an IndirectRef. Returns nil if the reference
// target does not exist.
func (d *Document) Resolve(obj Object) Object {
	ref, ok := obj.(IndirectRef)
	if !ok {
		return obj
	}
	iobj, exists := d.Objects[ref.Number]
	if !exists {
		return nil
	}
	return iobj.Value
}

// ResolveDict resolves obj and type-asserts to *Dictionary.
func (d *Document) ResolveDict(obj Object) *Dictionary {
	v := d.Resolve(obj)
	if v == nil {
		return nil
	}
	if dict, ok := v.(*Dictionary); ok {
		return dict
	}
	return nil
}

// sortInts sorts a slice of ints in ascending order (simple insertion sort to avoid import).
func sortInts(a []int) {
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
