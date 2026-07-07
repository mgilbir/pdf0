package pdf0

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// Document represents a parsed PDF file.
type Document struct {
	Version string                  // e.g., "2.0"
	Objects map[int]*IndirectObject // object number → object
	Trailer Dictionary
	// Encrypted reports whether the file carries an /Encrypt dictionary.
	// Decryption is not implemented: the document structure is readable, but
	// string and stream contents remain in their encrypted form. Write
	// refuses such documents.
	Encrypted bool
}

// Read parses a PDF document from the given data.
//
// Encrypted files are parsed structurally but not decrypted; see
// Document.Encrypted.
func Read(r io.ReaderAt, size int64) (*Document, error) {
	data := make([]byte, size)
	n, err := r.ReadAt(data, 0)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	if int64(n) < size {
		// Zero padding from a short read counts as PDF whitespace and would
		// silently mask truncated input.
		return nil, fmt.Errorf("short read: got %d of %d bytes", n, size)
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
	if xrefOffset < 0 || xrefOffset >= size {
		return nil, fmt.Errorf("startxref offset %d outside file (size %d)", xrefOffset, size)
	}

	// 3. Parse xref sections, following the /Prev chain. Both traditional
	// tables and xref streams can carry /Prev (incremental updates), and a
	// visited-set guards against cycles: a /Prev pointing at an already-seen
	// section (or at itself) would otherwise loop forever on a crafted or
	// corrupt file.
	xrefTable := &XRefTable{Entries: make(map[int]XRefEntry)}
	visitedXref := make(map[int64]bool)
	sectionOffset := xrefOffset
	first := true
	for {
		if visitedXref[sectionOffset] {
			break // cycle in the /Prev chain
		}
		visitedXref[sectionOffset] = true

		sectionTable, sectionTrailer, err := parseXRefSection(data, sectionOffset, doc)
		if err != nil {
			if first {
				return nil, err
			}
			break // tolerate a broken older section
		}
		// Merge: newer sections take precedence over older ones.
		for num, entry := range sectionTable.Entries {
			if _, exists := xrefTable.Entries[num]; !exists {
				xrefTable.Entries[num] = entry
			}
		}
		if first {
			doc.Trailer = *sectionTrailer
			first = false
		}
		prevOffset, ok := sectionTrailer.Get("Prev").(Integer)
		if !ok {
			break
		}
		sectionOffset = int64(prevOffset) + headerOffset
	}

	// 4. Parse all uncompressed objects from xref entries
	lexer := NewLexer(data)
	for num, entry := range xrefTable.Entries {
		if entry.Free || entry.Compressed {
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

	// 5. Materialize objects stored in object streams (type-2 entries). The
	// containers themselves were loaded as ordinary objects in step 4.
	if err := doc.loadCompressedObjects(xrefTable); err != nil {
		return nil, err
	}

	// 6. Drop file-structure artifacts so the document holds only content.
	doc.normalizeStructure()

	doc.Encrypted = doc.Trailer.Get("Encrypt") != nil

	return doc, nil
}

// normalizeStructure removes cross-reference plumbing from the parsed
// document. An xref stream's dictionary doubles as the trailer, so a document
// read from a modern file would otherwise carry xref-stream-only keys in
// doc.Trailer and re-emit stale /XRef and /ObjStm objects on Write — encoding
// obsolete offsets contradicting the rewritten file (audit C5). Object-stream
// contents are already materialized as ordinary objects, and Write always
// regenerates the cross-reference structure and /Size, so nothing is lost.
func (d *Document) normalizeStructure() {
	for num, iobj := range d.Objects {
		if stream, ok := iobj.Value.(*Stream); ok {
			if t, ok := stream.Dict.Get("Type").(Name); ok && (t == "XRef" || t == "ObjStm") {
				delete(d.Objects, num)
			}
		}
	}
	trailer := d.Trailer.Clone()
	for _, key := range []Name{"Type", "W", "Index", "Filter", "DecodeParms", "Length", "Prev", "XRefStm", "Size"} {
		trailer.Delete(key)
	}
	d.Trailer = *trailer
}

// parseXRefSection parses one cross-reference section (a traditional table
// followed by its trailer, or an xref stream) at the given absolute offset.
// For xref streams the stream dictionary doubles as the trailer, and the
// stream object itself is recorded in doc.Objects.
func parseXRefSection(data []byte, offset int64, doc *Document) (*XRefTable, *Dictionary, error) {
	lexer := NewLexer(data)
	lexer.SetPosition(offset)
	tok, err := lexer.NextToken()
	if err != nil {
		return nil, nil, fmt.Errorf("reading xref at offset %d: %w", offset, err)
	}

	switch tok.Type {
	case TokenXref:
		table, err := ParseXRefTable(data, lexer.Position())
		if err != nil {
			return nil, nil, fmt.Errorf("parsing xref table: %w", err)
		}
		trailer, err := findTrailer(data, lexer.Position())
		if err != nil {
			return nil, nil, fmt.Errorf("parsing trailer: %w", err)
		}
		return table, trailer, nil

	case TokenInteger:
		// Xref stream: the xref is an indirect object containing a stream
		lexer.SetPosition(offset)
		parser := NewParserFromLexer(lexer)
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			return nil, nil, fmt.Errorf("parsing xref stream object: %w", err)
		}
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			return nil, nil, fmt.Errorf("xref stream object is not a stream")
		}
		table, err := ParseXRefStream(stream)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing xref stream: %w", err)
		}
		if _, exists := doc.Objects[iobj.Number]; !exists {
			doc.Objects[iobj.Number] = iobj
		}
		return table, &stream.Dict, nil

	default:
		return nil, nil, fmt.Errorf("expected 'xref' or object number at offset %d, got %v", offset, tok.Type)
	}
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
//
// Encrypted documents are refused: their string and stream contents are
// still in encrypted form (decryption is not implemented), so writing them
// without the original cross-reference layout would produce a file no
// reader could decrypt.
func (d *Document) Write(w io.Writer) error {
	if d.Encrypted || d.Trailer.Get("Encrypt") != nil {
		return fmt.Errorf("writing encrypted documents is not supported")
	}

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
	sort.Ints(objNums)

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

// writeXRefTable writes a traditional xref table. Contiguous object-number
// runs are emitted as separate subsections, so sparse numbering does not
// balloon the table with fabricated free entries whose free-list linkage
// would then have to be maintained. The only free entry is the list head
// (object 0, generation 65535, next-free 0: the canonical empty list).
func writeXRefTable(s *Serializer, objNums []int, offsets map[int]int64, objects map[int]*IndirectObject) error {
	if err := s.writeString("xref\n"); err != nil {
		return err
	}

	entryLine := func(num int) string {
		if num == 0 {
			return "0000000000 65535 f \r\n"
		}
		gen := 0
		if obj, ok := objects[num]; ok {
			gen = obj.Generation
		}
		return fmt.Sprintf("%010d %05d n \r\n", offsets[num], gen)
	}

	// Object 0 (the free-list head) always begins the first subsection;
	// objects numbered from 1 up continue it.
	section := []int{0}
	flush := func() error {
		if err := s.writeString(fmt.Sprintf("%d %d\n", section[0], len(section))); err != nil {
			return err
		}
		for _, num := range section {
			if err := s.writeString(entryLine(num)); err != nil {
				return err
			}
		}
		return nil
	}

	for _, num := range objNums {
		if num <= 0 {
			continue // object 0 is synthesized; negative numbers are invalid
		}
		if num == section[0]+len(section) {
			section = append(section, num)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		section = []int{num}
	}
	return flush()
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
