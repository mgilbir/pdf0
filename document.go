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
	// Encrypted reports whether the file carried an /Encrypt dictionary.
	// Standard-security-handler files with the empty user password are decrypted
	// on Read (RC4, AES-128, and AES-256); their strings and streams are then in
	// the clear but this flag stays set. Schemes decryption does not handle
	// (non-empty passwords) keep their contents encrypted. Write re-encrypts a
	// decrypted document (reproducing the original /Encrypt) but refuses one whose
	// content is still encrypted.
	Encrypted bool

	// valCache memoizes traversals for the duration of one validation run;
	// see validationCache.
	valCache *validationCache

	// Offsets records the absolute byte offset of each uncompressed indirect
	// object, for the byte-level file-structure checks. Objects materialised
	// from object streams are absent.
	Offsets map[int]int64

	// embeddedDepth guards the recursive validation of embedded PDF/A files
	// (see checkEmbeddedPDFA); it is 0 for a top-level document.
	embeddedDepth int

	// brokenObjStms lists object-stream container numbers whose contents could
	// not be decoded during Read. The document parses without them so that
	// validation can report the defect (see checkStreamLength / objstm rules).
	brokenObjStms []int

	// security holds the standard security handler when an encrypted file was
	// decrypted on Read. It retains the file key and parameters so the same
	// encryption can be reproduced on Write. nil for unencrypted documents (or
	// for a scheme decryption does not support).
	security *stdSecurityHandler

	// usedXRefStream records that the file's primary cross-reference section was
	// a cross-reference stream (/Type /XRef) rather than a traditional table, so
	// Write regenerates the same kind of structure.
	usedXRefStream bool
}

// Read parses a PDF document from the given data.
//
// A malformed or adversarial file always yields an error, never a panic: any
// panic escaping the parse is recovered and returned as an error.
//
// Encrypted files (standard security handler) are decrypted with the empty
// password; use ReadWithPassword to supply a user or owner password. A file
// that cannot be decrypted is still parsed structurally, with its strings and
// streams left encrypted (see Document.Encrypted).
func Read(r io.ReaderAt, size int64) (*Document, error) {
	return readDocument(r, size, "")
}

// ReadWithPassword is Read with a user or owner password for an encrypted file.
func ReadWithPassword(r io.ReaderAt, size int64, password string) (*Document, error) {
	return readDocument(r, size, password)
}

func readDocument(r io.ReaderAt, size int64, password string) (doc *Document, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			doc = nil
			err = fmt.Errorf("recovered from panic while reading PDF: %v", rec)
		}
	}()
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

	doc = &Document{
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
	// Byte offsets are specified from the start of the file (ISO 32000-1
	// 7.5.4), so absolute offsets are correct even when data precedes the
	// header. Some producers, however, prepend bytes without updating their
	// offsets, leaving them relative to %PDF-. Choose whichever convention
	// actually lands on the cross-reference section, preferring absolute.
	adjust := int64(0)
	if !xrefLooksValid(data, xrefOffset) && headerOffset != 0 && xrefLooksValid(data, xrefOffset+headerOffset) {
		adjust = headerOffset
	}
	xrefOffset += adjust
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
			if t, _ := sectionTrailer.Get("Type").(Name); t == "XRef" {
				doc.usedXRefStream = true
			}
			first = false
		}
		prevOffset, ok := sectionTrailer.Get("Prev").(Integer)
		if !ok {
			break
		}
		sectionOffset = int64(prevOffset) + adjust
		if sectionOffset < 0 || sectionOffset >= size {
			break // /Prev points outside the file; ignore the broken chain tail
		}
	}

	// 4. Parse all uncompressed objects from xref entries
	doc.Offsets = make(map[int]int64)
	lexer := NewLexer(data)
	// parsedByOffset caches the object parsed at each byte offset. A malformed
	// cross-reference table can point many distinct object numbers at the same
	// offset; parsing it once per number would re-materialize the object, and if
	// it is a large stream, re-allocate its data every time (a 55 MB file with
	// 819 entries all pointing at one 7.7 MB stream expanded to 6.3 GB of stream
	// data on read — a small-input memory-DoS). Parsing each distinct offset only
	// once bounds the work to the file's real content. Parsing identical bytes
	// always yields an identical object, so the shared value is correct; the
	// per-number wrapper still carries the authoritative object number.
	parsedByOffset := make(map[int64]*IndirectObject)
	// resolveLen resolves an indirect stream /Length by seeking to the length
	// object via the cross-reference table and reading its integer value. This
	// lets a stream with a (frequently forward-referenced) indirect /Length be
	// read by its true byte count rather than by searching for endstream, which
	// can over-read pathologically when binary data ends in a non-whitespace
	// byte (see parseStream). A fresh parser with no resolver is used so a
	// length object cannot itself trigger recursive length resolution.
	resolveLen := func(ref IndirectRef) (int64, bool) {
		ent, ok := xrefTable.Entries[ref.Number]
		if !ok || ent.Free || ent.Compressed {
			return 0, false
		}
		lo := ent.Offset + adjust
		if lo < 0 || lo >= size {
			return 0, false
		}
		lx := NewLexer(data)
		lx.SetPosition(lo)
		return NewParserFromLexer(lx).integerObjectValue()
	}
	for num, entry := range xrefTable.Entries {
		if entry.Free || entry.Compressed {
			continue
		}
		if _, exists := doc.Objects[num]; exists {
			continue // already loaded (e.g., xref stream)
		}

		off := entry.Offset + adjust
		if off < 0 || off >= size {
			// A negative or out-of-range offset (e.g. a crafted "-0000000010"
			// entry, or an 8-byte /W field whose high bit overflowed int) would
			// otherwise seek the lexer to an invalid position.
			return nil, fmt.Errorf("object %d xref offset %d outside file (size %d)", num, off, size)
		}
		doc.Offsets[num] = off
		if prev, ok := parsedByOffset[off]; ok {
			// Same bytes already parsed under another number: reuse the value
			// rather than re-parsing (and re-allocating any stream data).
			doc.Objects[num] = &IndirectObject{Number: num, Generation: prev.Generation, Value: prev.Value}
			continue
		}
		lexer.SetPosition(off)
		parser := NewParserFromLexer(lexer)
		parser.resolveLength = resolveLen
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			return nil, fmt.Errorf("parsing object %d at offset %d: %w", num, entry.Offset, err)
		}
		// The cross-reference key is the authoritative object number: readers
		// resolve references through the xref, so the body's declared number
		// must not override it. Otherwise a body "3 0 obj" reached via xref slot
		// 4 would be written back numbered 3 under slot 4 — dangling for any
		// other reader (audit C7).
		iobj.Number = num
		doc.Objects[num] = iobj
		parsedByOffset[off] = iobj
	}

	// 4.5. Decrypt strings and streams under the standard security handler. This
	// runs before object streams are materialized: an /ObjStm container is an
	// encrypted stream, but the objects inside it are not separately encrypted.
	if doc.Trailer.Get("Encrypt") != nil {
		h, err := buildStdSecurityHandler(doc, password)
		if err != nil {
			return nil, fmt.Errorf("encryption: %w", err)
		}
		if h != nil {
			h.decryptDocument(doc)
			doc.security = h
		}
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
				// Drop the byte offset too: leaving it in d.Offsets makes the
				// byte-level file-structure checks treat the removed object's
				// span as part of the previous surviving object's region,
				// mis-attributing errors and skipping the last real object's
				// endobj check (audit C9).
				delete(d.Offsets, num)
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
	// An encrypted document can be written only when we hold the security
	// handler (from decrypting it on Read): Write re-encrypts the content with
	// the retained key and re-emits the preserved /Encrypt and /ID. Without a
	// handler (an unsupported scheme, or a wrong password) the content is still
	// encrypted, so refuse rather than write a file no reader could decrypt.
	if (d.Encrypted || d.Trailer.Get("Encrypt") != nil) && d.security == nil {
		return fmt.Errorf("writing encrypted documents is not supported")
	}
	// Object number 0 is reserved as the free-list head (ISO 32000-1 7.5.4); it
	// cannot be represented as an in-use object. Refuse rather than silently
	// dropping it from the written file (audit C16).
	if _, ok := d.Objects[0]; ok {
		return fmt.Errorf("object number 0 is reserved and cannot be written")
	}
	// A broken object stream left some objects unmaterialised during Read; the
	// document may reference them, so writing would emit dangling references
	// (audit C19).
	if len(d.brokenObjStms) > 0 {
		return fmt.Errorf("cannot write: %d object stream(s) failed to decode on read, so some objects are missing", len(d.brokenObjStms))
	}

	s := NewSerializer(w)

	// When re-encrypting, serialize encrypted copies rather than the in-memory
	// plaintext (which stays untouched for the caller). The /Encrypt dictionary
	// and /ID remain in the trailer and are written as-is.
	writeObjects, xrefType2 := d.buildWriteSet()
	if d.security != nil {
		writeObjects = d.security.encryptCopy(writeObjects)
	}

	// A stale indirect /Length (its target integer object not updated after a
	// stream's data changed, or a wrong length the parser recovered from) would
	// otherwise be re-emitted verbatim. Compute the correct value for each
	// indirect-length target so the written length object matches the data —
	// after encryption, since AES padding changes the length (audit C8).
	lengthOverrides := make(map[int]int64)
	for _, iobj := range writeObjects {
		if stream, ok := iobj.Value.(*Stream); ok {
			if ref, isRef := stream.Dict.Get("Length").(IndirectRef); isRef {
				lengthOverrides[ref.Number] = int64(len(stream.Data))
			}
		}
	}

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
	for num := range writeObjects {
		objNums = append(objNums, num)
	}
	sort.Ints(objNums)

	// 3. Write objects and record offsets
	offsets := make(map[int]int64)
	for _, num := range objNums {
		offsets[num] = s.Offset()
		iobj := writeObjects[num]
		if newLen, ok := lengthOverrides[num]; ok {
			if _, isInt := iobj.Value.(Integer); isInt {
				// Emit the corrected length without mutating the caller's object.
				iobj = &IndirectObject{Number: iobj.Number, Generation: iobj.Generation, Value: Integer(newLen)}
			}
		}
		if err := s.WriteIndirectObject(iobj); err != nil {
			return fmt.Errorf("writing object %d: %w", num, err)
		}
	}

	maxObj := 0
	for _, num := range objNums {
		if num > maxObj {
			maxObj = num
		}
	}

	// 4. Write the cross-reference structure. A file read from a cross-reference
	// stream is regenerated as one (its dictionary doubles as the trailer);
	// otherwise a traditional table followed by a trailer.
	xrefOffset := s.Offset()
	if d.usedXRefStream {
		if err := writeXRefStream(s, objNums, offsets, writeObjects, xrefType2, &d.Trailer, maxObj+1); err != nil {
			return err
		}
	} else {
		if err := writeXRefTable(s, objNums, offsets, writeObjects); err != nil {
			return err
		}
		// Clone so setting Size doesn't mutate the caller's Document.Trailer
		// (Dictionary shares its backing slices on a plain struct copy).
		trailer := d.Trailer.Clone()
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
	}

	// 5. Write startxref
	if err := s.writeString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset)); err != nil {
		return err
	}

	return nil
}

// writeXRefStream writes the cross-reference structure as a /Type /XRef stream
// object numbered xrefObjNum (which lands at the current serializer offset, so
// its own entry points there). Trailer keys (/Root, /Info, /ID, /Encrypt) carry
// into the stream dictionary. The binary entries are FlateDecode-compressed.
func writeXRefStream(s *Serializer, objNums []int, offsets map[int]int64, objects map[int]*IndirectObject, type2 map[int][2]int, trailer *Dictionary, xrefObjNum int) error {
	offsets[xrefObjNum] = s.Offset()

	// Entry set: the free-list head (object 0), every written object (including
	// this xref stream itself), and every object packed into an object stream.
	numSet := map[int]bool{0: true, xrefObjNum: true}
	for _, num := range objNums {
		numSet[num] = true
	}
	for num := range type2 {
		numSet[num] = true
	}
	nums := make([]int, 0, len(numSet))
	for num := range numSet {
		nums = append(nums, num)
	}
	sort.Ints(nums)

	var maxField2 uint64
	for _, off := range offsets {
		if uint64(off) > maxField2 {
			maxField2 = uint64(off)
		}
	}
	// field3 holds the free-list generation (65535 for the head), an object
	// generation, or — for type-2 entries — the index within an object stream,
	// which can exceed 65535 when a stream packs more than 65536 objects. Size
	// the field to the largest value actually written, rather than assuming two
	// bytes, or a large index silently wraps and corrupts the xref.
	maxField3 := uint64(65535) // free-list head generation
	for _, e := range type2 {
		if uint64(e[1]) > maxField3 {
			maxField3 = uint64(e[1])
		}
	}
	for _, o := range objects {
		if uint64(o.Generation) > maxField3 {
			maxField3 = uint64(o.Generation)
		}
	}
	w := [3]int{1, byteWidth(maxField2), byteWidth(maxField3)} // type, field2, field3

	var body bytes.Buffer
	put := func(v uint64, width int) {
		for i := width - 1; i >= 0; i-- {
			body.WriteByte(byte(v >> (8 * uint(i))))
		}
	}
	for _, num := range nums {
		switch {
		case num == 0:
			put(0, w[0]) // type 0: free-list head
			put(0, w[1])
			put(65535, w[2])
		case type2[num] != [2]int{}:
			e := type2[num] // {objStmNum, index}
			put(2, w[0])    // type 2: object stored in an object stream
			put(uint64(e[0]), w[1])
			put(uint64(e[1]), w[2])
		default:
			gen := 0
			if o, ok := objects[num]; ok {
				gen = o.Generation
			}
			put(1, w[0]) // type 1: uncompressed object
			put(uint64(offsets[num]), w[1])
			put(uint64(gen), w[2])
		}
	}

	// /Index: [start count ...] over contiguous runs of object numbers.
	var index Array
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		index = append(index, Integer(nums[i]), Integer(j-i+1))
		i = j + 1
	}

	dict := trailer.Clone()
	dict.Set("Type", Name("XRef"))
	dict.Set("Size", Integer(xrefObjNum+1))
	dict.Set("W", Array{Integer(w[0]), Integer(w[1]), Integer(w[2])})
	dict.Set("Index", index)
	encoded := flateEncode(body.Bytes())
	dict.Set("Filter", Name("FlateDecode"))
	dict.Set("Length", Integer(len(encoded)))

	return s.WriteIndirectObject(&IndirectObject{Number: xrefObjNum, Value: &Stream{Dict: *dict, Data: encoded}})
}

// byteWidth returns the number of bytes needed to hold v (at least 1).
func byteWidth(v uint64) int {
	n := 1
	for v >>= 8; v != 0; v >>= 8 {
		n++
	}
	return n
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

	// Each entry must be exactly 20 bytes (ISO 32000-1 7.5.4): a 10-digit
	// offset, space, 5-digit generation, space, the type byte, then a 2-byte
	// EOL. Emitting "n \r\n" (a space AND CRLF after the type) produced a
	// 21-byte line that no fixed-format reader — including this package's own
	// 6.1.4 validator — accepts. Use a bare CRLF EOL.
	entryLine := func(num int) string {
		if num == 0 {
			return "0000000000 65535 f\r\n"
		}
		gen := 0
		if obj, ok := objects[num]; ok {
			gen = obj.Generation
		}
		return fmt.Sprintf("%010d %05d n\r\n", offsets[num], gen)
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

// Resolve follows an IndirectRef to its value, iterating through chains of
// references (a legal indirect object whose value is itself a reference).
// Returns the object unchanged if it is not an IndirectRef, and nil if any
// target in the chain does not exist or the chain cycles.
func (d *Document) Resolve(obj Object) Object {
	// A bounded hop count doubles as the cycle guard without allocating a
	// visited set on this hot path; real files chain a handful of hops at
	// most, so exceeding the bound means a reference cycle or garbage.
	for hops := 0; hops < 64; hops++ {
		ref, ok := obj.(IndirectRef)
		if !ok {
			return obj
		}
		iobj, exists := d.Objects[ref.Number]
		if !exists {
			return nil
		}
		obj = iobj.Value
	}
	return nil
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

// xrefLooksValid reports whether a cross-reference section plausibly begins at
// the given offset: either the traditional "xref" keyword or the start of an
// "N G obj" cross-reference stream, allowing leading whitespace.
func xrefLooksValid(data []byte, off int64) bool {
	if off < 0 || off >= int64(len(data)) {
		return false
	}
	i := off
	for i < int64(len(data)) && isWhitespace(data[i]) {
		i++
	}
	rest := data[i:]
	if bytes.HasPrefix(rest, []byte("xref")) {
		return true
	}
	// Cross-reference stream: "<num> <num> obj ... /Type /XRef".
	if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
		window := rest
		if len(window) > 64 {
			window = window[:64]
		}
		return bytes.Contains(window, []byte("obj"))
	}
	return false
}
