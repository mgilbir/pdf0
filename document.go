package pdf0

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
	"io"
	"sort"
	"strconv"
)

// This file implements whole-file I/O: the Document type, Read's pipeline over
// the PDF file structure (header, body, cross-reference section, trailer — ISO
// 32000-2 7.5) and Write's regeneration of that structure from the object
// graph. Read is the package's front door for untrusted input, so it never
// panics and it recovers aggressively: a startxref that points into the table
// instead of at it, offsets that are header-relative rather than absolute, and
// a cross-reference section too broken to use at all (rebuilt by scanning for
// object headers) all still yield a document.
//
// Read then normalizes the file structure away — /XRef and /ObjStm objects,
// xref-stream-only trailer keys — because Write always regenerates it. Nothing
// that must survive a round trip may live outside the object graph.

// Document represents a parsed PDF file.
type Document struct {
	Version string                         // e.g., "2.0"
	Objects map[int]*object.IndirectObject // object number → object
	Trailer object.Dictionary
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

	// limits holds the resource limits resolved from the Option values passed to
	// Read. Read through (*Document).lim(), never directly: the zero value means
	// "defaults", so a hand-built &Document{...} behaves like one Read produced.
	limits core.Limits

	// brokenObjStms lists object-stream container numbers whose contents could
	// not be decoded during Read. The document parses without them so that
	// validation can report the defect (see checkStreamLength / objstm rules).
	brokenObjStms []int

	// decryptFailures lists the object numbers whose ciphertext did not decrypt
	// under a known-good file key — corrupt AES data, or data that was never
	// encrypted (see stdSecurityHandler.decrypt). Their strings and stream
	// bodies are empty rather than noise, so the content is unrecoverable and
	// Write refuses, exactly as it does for brokenObjStms.
	decryptFailures []int

	// readLimits records the resource guards that tripped while this file was
	// read — the same idea as brokenObjStms, generalized (see limits.go). Read
	// happens before any validation run exists, so a read-time trip has nowhere
	// else to live; every validator merges these into its report. It is written
	// only during Read and read-only afterwards, which is what keeps validation
	// (which runs on a shallow copy sharing this pointer) non-mutating.
	readLimits *core.Recorder

	// security holds the standard security handler when an encrypted file was
	// decrypted on Read. It retains the file key and parameters so the same
	// encryption can be reproduced on Write. nil for unencrypted documents (or
	// for a scheme decryption does not support).
	security *crypt.Handler

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
// Resource limits default to values safe for untrusted input; pass With* options
// to change them. The resolved limits are stored on the returned Document, so
// every validator and extractor that runs on it inherits the same configuration.
func Read(r io.ReaderAt, size int64, opts ...Option) (*Document, error) {
	return readDocument(core.Canceler{}, r, size, "", resolveLimits(opts))
}

// ReadWithPassword is Read with a user or owner password for an encrypted file.
func ReadWithPassword(r io.ReaderAt, size int64, password string, opts ...Option) (*Document, error) {
	return readDocument(core.Canceler{}, r, size, password, resolveLimits(opts))
}

// ReadContext is Read with cancellation. Parsing is not usually the expensive
// half — a 71 MB file parses in about 100 ms — but its cost is set by the file:
// a small file can carry half a gigabyte of object streams to decompress, and
// a cross-reference section too broken to use is rebuilt by scanning the whole
// file. Those are the cases a caller with a deadline needs to be able to stop.
//
// A cancelled read returns a nil Document and an error wrapping ctx.Err(), so
// errors.Is(err, context.Canceled) and errors.Is(err, context.DeadlineExceeded)
// both work. It never returns a partial Document: a document missing an
// arbitrary subset of its objects is indistinguishable from one whose file
// genuinely lacks them, and every validator would then report the absence as a
// conformance failure. See cancel.go.
func ReadContext(ctx context.Context, r io.ReaderAt, size int64, opts ...Option) (*Document, error) {
	return readDocument(core.NewCanceler(ctx), r, size, "", resolveLimits(opts))
}

// ReadWithPasswordContext is ReadWithPassword with cancellation; see ReadContext.
func ReadWithPasswordContext(ctx context.Context, r io.ReaderAt, size int64, password string, opts ...Option) (*Document, error) {
	return readDocument(core.NewCanceler(ctx), r, size, password, resolveLimits(opts))
}

func readDocument(cancel core.Canceler, r io.ReaderAt, size int64, password string, lim core.Limits) (doc *Document, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			doc = nil
			err = fmt.Errorf("recovered from panic while reading PDF: %v", rec)
		}
	}()
	if err := cancel.StopErr("reading PDF"); err != nil {
		return nil, err
	}
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
		Objects:    make(map[int]*object.IndirectObject),
		limits:     lim,
		readLimits: &core.Recorder{},
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
	rebuilt := false // the table was reconstructed by scanning (load leniently)
	var firstErr error
	for {
		// One iteration per incremental update; a file can carry thousands.
		if err := cancel.StopErr("reading PDF cross-reference chain"); err != nil {
			return nil, err
		}
		if visitedXref[sectionOffset] {
			break // cycle in the /Prev chain
		}
		visitedXref[sectionOffset] = true

		sectionTable, sectionTrailer, err := parseXRefSection(cancel, data, sectionOffset, doc)
		if err != nil {
			// Recovery: the startxref value "shall [give] the byte offset ...
			// to the beginning of the xref keyword in the last cross-reference
			// section" (ISO 32000-2, 7.5.5). Real-world files violate this by
			// pointing a few dozen bytes INTO the table's entries instead
			// (Common Crawl sweep #13: consistently 55-57 bytes past the
			// keyword), which reads as an integer and mis-dispatches to the
			// xref-stream branch. Relocate to the spec-mandated target: the
			// nearest standalone "xref" keyword at or before the offset.
			if rec := precedingXrefKeyword(data, sectionOffset); rec >= 0 && !visitedXref[rec] {
				visitedXref[rec] = true
				sectionTable, sectionTrailer, err = parseXRefSection(cancel, data, rec, doc)
			}
			if err != nil {
				if first {
					// Last resort: the cross-reference data is unusable, so
					// rebuild the table by scanning for object headers (the
					// long-standing reader practice for damaged files; see
					// rebuildXRefByScan). The trailer comes from a scan too;
					// an xref-stream file whose trailer IS the broken stream
					// dictionary gets /Root synthesized from the catalog
					// after the objects load.
					if t := rebuildXRefByScan(data); t != nil {
						xrefTable = t
						rebuilt, firstErr = true, err
						if tr := findTrailerByScan(data); tr != nil {
							doc.Trailer = *tr
						}
						break
					}
					return nil, err
				}
				break // tolerate a broken older section
			}
		}
		// Merge: newer sections take precedence over older ones.
		for num, entry := range sectionTable.Entries {
			if _, exists := xrefTable.Entries[num]; !exists {
				xrefTable.Entries[num] = entry
			}
		}
		if first {
			doc.Trailer = *sectionTrailer
			if t, _ := sectionTrailer.Get("Type").(object.Name); t == "XRef" {
				doc.usedXRefStream = true
			}
			first = false
		}
		prevOffset, ok := sectionTrailer.Get("Prev").(object.Integer)
		if !ok {
			break
		}
		sectionOffset = int64(prevOffset) + adjust
		if sectionOffset < 0 || sectionOffset >= size {
			break // /Prev points outside the file; ignore the broken chain tail
		}
	}

	// 4. Parse all uncompressed objects from xref entries. A rebuilt table
	// loads leniently — an entry that does not parse is dropped, since a
	// header-shaped byte run inside a stream body can fabricate one. A table
	// the file itself supplied is authoritative, so a load failure there
	// triggers the same scan-rebuild as an unparseable section before giving
	// up (the sweep-13 holdout: the table parses, but every offset in it is
	// shifted and lands inside the previous object).
	effAdjust := adjust
	if rebuilt {
		effAdjust = 0 // scanned offsets are absolute by construction
	}
	if err := doc.loadObjectsFromXref(cancel, data, size, xrefTable, effAdjust, rebuilt); err != nil {
		// A cancellation is not a "this table is broken" signal, so it must not
		// trigger the (whole-file) rebuild-and-retry: that would do more work in
		// response to being told to stop.
		if cerr := cancel.StopErr("reading PDF objects"); cerr != nil {
			return nil, cerr
		}
		t := rebuildXRefByScan(data)
		if t == nil {
			return nil, err
		}
		xrefTable, rebuilt = t, true
		doc.Objects = make(map[int]*object.IndirectObject)
		if err2 := doc.loadObjectsFromXref(cancel, data, size, xrefTable, 0, true); err2 != nil {
			return nil, err
		}
	}
	// A rebuilt file may have no parseable trailer at all (an xref-stream
	// file's trailer IS its broken stream dictionary). The document catalog is
	// the root of the object hierarchy (ISO 32000-2, 7.7.2), so synthesize the
	// /Root the trailer exists to provide from the first catalog object.
	if rebuilt && doc.Trailer.Get("Root") == nil {
		nums := make([]int, 0, len(doc.Objects))
		for num := range doc.Objects {
			nums = append(nums, num)
		}
		sort.Ints(nums)
		for _, num := range nums {
			if d, ok := doc.Objects[num].Value.(*object.Dictionary); ok {
				if t, _ := d.Get("Type").(object.Name); t == "Catalog" {
					doc.Trailer.Set("Root", object.IndirectRef{Number: num})
					break
				}
			}
		}
		if doc.Trailer.Get("Root") == nil {
			if firstErr != nil {
				return nil, firstErr
			}
			return nil, fmt.Errorf("rebuilt cross-reference table found no document catalog")
		}
	}

	// 4.5. Decrypt strings and streams under the standard security handler. This
	// runs before object streams are materialized: an /ObjStm container is an
	// encrypted stream, but the objects inside it are not separately encrypted.
	if doc.Trailer.Get("Encrypt") != nil {
		h, err := crypt.Open(doc.graph(), password)
		if err != nil {
			return nil, fmt.Errorf("encryption: %w", err)
		}
		if h != nil {
			doc.decryptFailures = h.DecryptDocument(doc.graph())
			doc.security = h
		}
	}

	// 5. Materialize objects stored in object streams (type-2 entries). The
	// containers themselves were loaded as ordinary objects in step 4.
	if err := doc.loadCompressedObjects(cancel, xrefTable); err != nil {
		return nil, err
	}
	// A rebuilt table has no type-2 entries — the scan sees only what is at
	// the byte level — so objects living inside /Type /ObjStm containers
	// would be missing. Materialize every container's objects directly.
	if rebuilt {
		if err := doc.materializeScannedObjStms(cancel); err != nil {
			return nil, err
		}
	}

	// 6. Drop file-structure artifacts so the document holds only content.
	doc.normalizeStructure()

	doc.Encrypted = doc.Trailer.Get("Encrypt") != nil

	return doc, nil
}

// loadObjectsFromXref parses every uncompressed object the cross-reference
// table lists into doc.Objects, resetting doc.Offsets first. In lenient mode
// (used for tables reconstructed by rebuildXRefByScan) an entry whose offset
// is out of range or whose bytes do not parse is dropped rather than failing
// the read: a scanned entry has no authority beyond the bytes it points at.
func (doc *Document) loadObjectsFromXref(cancel core.Canceler, data []byte, size int64, xrefTable *XRefTable, adjust int64, lenient bool) error {
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
	parsedByOffset := make(map[int64]*object.IndirectObject)
	// resolveLen resolves an indirect stream /Length by seeking to the length
	// object via the cross-reference table and reading its integer value. This
	// lets a stream with a (frequently forward-referenced) indirect /Length be
	// read by its true byte count rather than by searching for endstream, which
	// can over-read pathologically when binary data ends in a non-whitespace
	// byte (see parseStream). A fresh parser with no resolver is used so a
	// length object cannot itself trigger recursive length resolution.
	resolveLen := func(ref object.IndirectRef) (int64, bool) {
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
		return NewParserFromLexer(lx).IntegerObjectValue()
	}
	for num, entry := range xrefTable.Entries {
		// Per object: the unit of work here is one object parse, which for a
		// stream is bounded by the per-stream decode cap, so cancellation takes
		// effect after at most one such parse.
		if err := cancel.StopErr("reading PDF objects"); err != nil {
			return err
		}
		if entry.Free || entry.Compressed {
			continue
		}
		if num == 0 {
			// Object number 0 is the free-list head (ISO 32000-1 7.5.4) and can
			// never be an in-use object; "0 0 R" is a null reference by
			// definition. Real-world files mark 0 in use (and carry a "0 0 obj"
			// body); ignore the definition like other malformed constructs
			// rather than loading an object Write must then refuse (sweep #13).
			continue
		}
		if _, exists := doc.Objects[num]; exists {
			continue // already loaded (e.g., xref stream)
		}

		off := entry.Offset + adjust
		if off < 0 || off >= size {
			if lenient {
				continue
			}
			// A negative or out-of-range offset (e.g. a crafted "-0000000010"
			// entry, or an 8-byte /W field whose high bit overflowed int) would
			// otherwise seek the lexer to an invalid position.
			return fmt.Errorf("object %d xref offset %d outside file (size %d)", num, off, size)
		}
		doc.Offsets[num] = off
		if prev, ok := parsedByOffset[off]; ok {
			// Same bytes already parsed under another number: reuse the value
			// rather than re-parsing (and re-allocating any stream data).
			doc.Objects[num] = &object.IndirectObject{Number: num, Generation: prev.Generation, Value: prev.Value}
			continue
		}
		lexer.SetPosition(off)
		parser := NewParserFromLexer(lexer)
		parser.ResolveLength = resolveLen
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			if lenient {
				delete(doc.Offsets, num)
				continue
			}
			return fmt.Errorf("parsing object %d at offset %d: %w", num, entry.Offset, err)
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
	return nil
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
		if stream, ok := iobj.Value.(*object.Stream); ok {
			if t, ok := stream.Dict.Get("Type").(object.Name); ok && (t == "XRef" || t == "ObjStm") {
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
	for _, key := range []object.Name{"Type", "W", "Index", "Filter", "DecodeParms", "Length", "Prev", "XRefStm", "Size"} {
		trailer.Delete(key)
	}
	d.Trailer = *trailer
}

// parseXRefSection parses one cross-reference section (a traditional table
// followed by its trailer, or an xref stream) at the given absolute offset.
// For xref streams the stream dictionary doubles as the trailer, and the
// stream object itself is recorded in doc.Objects.
func parseXRefSection(cancel core.Canceler, data []byte, offset int64, doc *Document) (*XRefTable, *object.Dictionary, error) {
	lexer := NewLexer(data)
	lexer.SetPosition(offset)
	tok, err := lexer.NextToken()
	if err != nil {
		return nil, nil, fmt.Errorf("reading xref at offset %d: %w", offset, err)
	}

	switch tok.Type {
	case syntax.TokenXref:
		table, err := ParseXRefTable(data, lexer.Position())
		if err != nil {
			return nil, nil, fmt.Errorf("parsing xref table: %w", err)
		}
		trailer, err := findTrailer(data, lexer.Position())
		if err != nil {
			return nil, nil, fmt.Errorf("parsing trailer: %w", err)
		}
		return table, trailer, nil

	case syntax.TokenInteger:
		// Xref stream: the xref is an indirect object containing a stream
		lexer.SetPosition(offset)
		parser := NewParserFromLexer(lexer)
		iobj, err := parser.ParseIndirectObject()
		if err != nil {
			return nil, nil, fmt.Errorf("parsing xref stream object: %w", err)
		}
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			return nil, nil, fmt.Errorf("xref stream object is not a stream")
		}
		// The document's own resolved limits, not defaultLimits(): a
		// cross-reference stream is a Flate stream the file controls like any
		// other, so a caller who lowered WithMaxDecodedStreamBytes for untrusted
		// uploads has to get that ceiling here too. doc.limits is populated
		// before the cross-reference chain is walked, so the value is available.
		//
		// This costs nothing under the defaults, which is the only configuration
		// the corpus exercises: doc.lim() is then defaultLimits() field for
		// field. Measured across 3,102 files (the veraPDF corpus, the Cal Poly
		// PDF/VT suite, the WTPDF set, the Factur-X invoices and the PDF 2.0
		// reference files), 930 cross-reference stream sections decode to at most
		// 430,350 bytes — 0.4% of the 100 MB default, and none above 1 MiB. A
		// caller has to go two orders of magnitude below the default before this
		// bound is what stops their read.
		table, err := parseXRefStream(cancel, stream, doc.lim())
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
	for pos < len(tail) && syntax.IsWhitespace(tail[pos]) {
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
func findTrailer(data []byte, afterPos int64) (*object.Dictionary, error) {
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

	dict, ok := obj.(*object.Dictionary)
	if !ok {
		return nil, fmt.Errorf("trailer value is not a dictionary, got %T", obj)
	}

	return dict, nil
}

// Write serializes the document to the writer in PDF format.
//
// A document decrypted on Read is re-encrypted with its retained key so it
// round-trips. A document that could not be decrypted (Document.Locked) is
// written back verbatim as a lossless passthrough under its preserved /Encrypt.
// Write regenerates the cross-reference section, emitting a cross-reference
// stream when the source used one and a traditional table otherwise.
func (d *Document) Write(w io.Writer) error { return d.write(core.Canceler{}, w) }

// WriteContext is Write with cancellation.
//
// Writing is usually fast, but its cost is the document's, not the caller's: a
// malformed cross-reference table can make one large stream reachable from
// thousands of object numbers, and the resulting output is legitimately
// enormous. cmd/corpusprobe streams exactly that case to io.Discard.
//
// A cancelled write returns an error wrapping ctx.Err(). Whatever had already
// been written stays written — an io.Writer cannot be rewound — so the output
// is a truncated file, and the returned error is the only thing that says so.
// A caller that must not leave a partial file behind should write to a
// temporary and rename on success. See cancel.go.
func (d *Document) WriteContext(ctx context.Context, w io.Writer) error {
	return d.write(core.NewCanceler(ctx), w)
}

func (d *Document) write(cancel core.Canceler, w io.Writer) error {
	// An encrypted document with a security handler (decrypted on Read) is
	// re-encrypted below with the retained key. Without a handler (an unsupported
	// scheme or a non-empty password) the content is still in its original
	// encrypted form in the model; it is written back verbatim under the
	// preserved /Encrypt and /ID — a lossless passthrough that keeps a file we
	// cannot decrypt round-trippable rather than losing it on save.
	if (d.Encrypted || d.Trailer.Get("Encrypt") != nil) && d.security == nil {
		// The passthrough is sound only when the content is known to be
		// encrypted and the whole object model survived Read:
		//   - The /Encrypt dictionary must resolve. If it does not, the
		//     encryption state is unknown — the content in the model may be
		//     plaintext — and writing it back under a dangling /Encrypt would
		//     produce a file a reader would wrongly try to decrypt.
		//   - No object stream may have failed to decode: its compressed objects
		//     are locked inside the still-encrypted container and missing from
		//     the model, so re-serialization would silently drop them.
		// (buildWriteSet leaves the objects unpacked here, so their per-object
		// encryption is preserved.)
		if d.ResolveDict(d.Trailer.Get("Encrypt")) == nil {
			return fmt.Errorf("cannot write encrypted document: its /Encrypt dictionary is unresolvable, so the encryption state is unknown")
		}
		if len(d.brokenObjStms) > 0 {
			return fmt.Errorf("cannot write encrypted document: %d object stream(s) could not be decrypted, so some objects are missing", len(d.brokenObjStms))
		}
	}
	// Object number 0 is reserved as the free-list head (ISO 32000-1 7.5.4); it
	// cannot be represented as an in-use object. Refuse rather than silently
	// dropping it from the written file (audit C16). A negative number is not
	// representable at all — ISO 32000-2 §7.3.10 makes the object number a
	// positive integer — and can only come from a caller (or a bug) writing into
	// Objects under an index that is not an object number, so it is refused here
	// too rather than emitted as a "-1 0 obj" no reader should accept.
	if _, ok := d.Objects[0]; ok {
		return fmt.Errorf("object number 0 is reserved and cannot be written")
	}
	worst := 0
	for num := range d.Objects {
		if num < worst {
			worst = num
		}
	}
	if worst < 0 {
		return fmt.Errorf("object number %d is not a valid object number (ISO 32000-2 7.3.10 requires a positive integer)", worst)
	}
	// A broken object stream left some objects unmaterialised during Read; the
	// document may reference them, so writing would emit dangling references
	// (audit C19).
	if len(d.brokenObjStms) > 0 {
		return fmt.Errorf("cannot write: %d object stream(s) failed to decode on read, so some objects are missing", len(d.brokenObjStms))
	}
	// Objects whose ciphertext did not decrypt hold nothing; writing them would
	// silently replace their content with empty values.
	if len(d.decryptFailures) > 0 {
		return fmt.Errorf("cannot write: object(s) %v could not be decrypted on read, so their content is missing", d.decryptFailures)
	}

	s := NewSerializer(w)

	// When re-encrypting, serialize encrypted copies rather than the in-memory
	// plaintext (which stays untouched for the caller). The /Encrypt dictionary
	// and /ID remain in the trailer and are written as-is.
	writeObjects, xrefType2 := d.buildWriteSet()
	if d.security != nil {
		writeObjects = d.security.EncryptCopy(writeObjects)
	}

	// A stale indirect /Length (its target integer object not updated after a
	// stream's data changed, or a wrong length the parser recovered from) would
	// otherwise be re-emitted verbatim. Compute the correct value for each
	// indirect-length target so the written length object matches the data —
	// after encryption, since AES padding changes the length (audit C8).
	lengthOverrides := make(map[int]int64)
	for _, iobj := range writeObjects {
		if stream, ok := iobj.Value.(*object.Stream); ok {
			if ref, isRef := stream.Dict.Get("Length").(object.IndirectRef); isRef {
				n := int64(len(stream.Data))
				// Two streams pointing their /Length at one integer object with
				// different data lengths cannot both be represented; overriding it
				// once per stream in map order picked a nondeterministic wrong
				// value. Reject the malformed input instead (audit C40).
				if prev, seen := lengthOverrides[ref.Number]; seen && prev != n {
					return fmt.Errorf("object %d is the /Length target of two streams with different lengths (%d and %d)", ref.Number, prev, n)
				}
				lengthOverrides[ref.Number] = n
			}
		}
	}

	// 1. Write header
	version := d.Version
	if version == "" {
		version = "2.0"
	}
	header := fmt.Sprintf("%%PDF-%s\n%%\x80\x80\x80\x80\n", version)
	if err := s.WriteString(header); err != nil {
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
		// Per object: one iteration serializes (and, when re-encrypting, encrypts)
		// a single object, so cancellation takes effect after at most one object's
		// worth of output.
		if err := cancel.StopErr("writing PDF"); err != nil {
			return err
		}
		offsets[num] = s.Offset()
		iobj := writeObjects[num]
		if newLen, ok := lengthOverrides[num]; ok {
			if _, isInt := iobj.Value.(object.Integer); isInt {
				// Emit the corrected length without mutating the caller's object.
				iobj = &object.IndirectObject{Number: iobj.Number, Generation: iobj.Generation, Value: object.Integer(newLen)}
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
		trailer.Set("Size", object.Integer(maxObj+1))
		if err := s.WriteString("trailer\n"); err != nil {
			return err
		}
		if err := s.WriteDictionary(trailer); err != nil {
			return err
		}
		if err := s.WriteString("\n"); err != nil {
			return err
		}
	}

	// 5. Write startxref
	if err := s.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset)); err != nil {
		return err
	}

	return nil
}

// writeXRefStream writes the cross-reference structure as a /Type /XRef stream
// object numbered xrefObjNum (which lands at the current serializer offset, so
// its own entry points there). Trailer keys (/Root, /Info, /ID, /Encrypt) carry
// into the stream dictionary. The binary entries are FlateDecode-compressed.
func writeXRefStream(s *syntax.Serializer, objNums []int, offsets map[int]int64, objects map[int]*object.IndirectObject, type2 map[int][2]int, trailer *object.Dictionary, xrefObjNum int) error {
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

	// field2 holds a type-1 entry's byte offset OR a type-2 entry's containing
	// object-stream number, so it must be wide enough for both. Sizing it from
	// the byte offsets alone truncated the container number when a sparse,
	// high-numbered object stream sat in a small file, producing a corrupt xref
	// that pointed at the wrong (or a nonexistent) object stream (audit C5).
	var maxField2 uint64
	for _, off := range offsets {
		if uint64(off) > maxField2 {
			maxField2 = uint64(off)
		}
	}
	for _, e := range type2 {
		if uint64(e[0]) > maxField2 {
			maxField2 = uint64(e[0])
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
	var index object.Array
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		index = append(index, object.Integer(nums[i]), object.Integer(j-i+1))
		i = j + 1
	}

	dict := trailer.Clone()
	dict.Set("Type", object.Name("XRef"))
	dict.Set("Size", object.Integer(xrefObjNum+1))
	dict.Set("W", object.Array{object.Integer(w[0]), object.Integer(w[1]), object.Integer(w[2])})
	dict.Set("Index", index)
	encoded := core.FlateEncode(body.Bytes())
	dict.Set("Filter", object.Name("FlateDecode"))
	dict.Set("Length", object.Integer(len(encoded)))

	return s.WriteIndirectObject(&object.IndirectObject{Number: xrefObjNum, Value: &object.Stream{Dict: *dict, Data: encoded}})
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
func writeXRefTable(s *syntax.Serializer, objNums []int, offsets map[int]int64, objects map[int]*object.IndirectObject) error {
	if err := s.WriteString("xref\n"); err != nil {
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
		if err := s.WriteString(fmt.Sprintf("%d %d\n", section[0], len(section))); err != nil {
			return err
		}
		for _, num := range section {
			if err := s.WriteString(entryLine(num)); err != nil {
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
func (d *Document) Resolve(obj object.Object) object.Object {
	return d.graph().Resolve(obj)
}

// ResolveDict resolves obj and type-asserts to *Dictionary.
func (d *Document) ResolveDict(obj object.Object) *object.Dictionary {
	return d.graph().ResolveDict(obj)
}

// precedingXrefKeyword returns the offset of the last standalone "xref"
// keyword at or before off, searching a bounded window, or -1. It is the
// recovery target for a startxref (or /Prev) value that violates ISO 32000-2,
// 7.5.5 — which requires the offset of "the beginning of the xref keyword" —
// by pointing into the table instead. A match preceded by a letter (e.g. the
// tail of "startxref") is not a keyword.
func precedingXrefKeyword(data []byte, off int64) int64 {
	const window = 1024
	lo := off - window
	if lo < 0 {
		lo = 0
	}
	hi := off + 4
	if hi > int64(len(data)) {
		hi = int64(len(data))
	}
	if lo >= hi {
		return -1
	}
	region := data[lo:hi]
	for {
		i := bytes.LastIndex(region, []byte("xref"))
		if i < 0 {
			return -1
		}
		abs := lo + int64(i)
		if abs == 0 || !isLetterByte(data[abs-1]) {
			return abs
		}
		region = region[:i]
	}
}

func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// xrefLooksValid reports whether a cross-reference section plausibly begins at
// the given offset: either the traditional "xref" keyword or the start of an
// "N G obj" cross-reference stream, allowing leading whitespace.
func xrefLooksValid(data []byte, off int64) bool {
	if off < 0 || off >= int64(len(data)) {
		return false
	}
	i := off
	for i < int64(len(data)) && syntax.IsWhitespace(data[i]) {
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

// graph returns the object-graph half of the view: what resolving a reference
// needs, and nothing else.
//
// Resolve is the hottest path in the package — a validation run makes hundreds
// of thousands of calls — and the full view resolves the limits on the way,
// which fills eleven fields. None of that is read while chasing a reference, so
// graph exists to keep it off that path while still leaving one implementation
// of the walk itself.
func (d *Document) graph() core.View {
	return core.View{Objects: d.Objects, Trailer: &d.Trailer}
}

// view returns the read-only view of this document that the packages below the
// root package take in place of a *Document. It is built per call rather than
// cached: a Document may be mutated between operations, and a stale view would
// resolve against the object map it was built from.
//
// The run state travels with it when there is one, so a trip a subsystem records
// through the view lands in the same recorder the validators report from.
func (d *Document) view() core.View {
	v := core.View{Version: d.Version, Encrypted: d.Encrypted, Objects: d.Objects, Offsets: d.Offsets, Trailer: &d.Trailer, BrokenObjStms: d.brokenObjStms, DecryptFailures: d.decryptFailures, UsedXRefStream: d.usedXRefStream, EmbeddedDepth: d.embeddedDepth, Limits: d.lim(), Cancel: d.canceler()}
	if d.valCache != nil {
		v.Run = d.valCache.run.shared
	}
	return v
}
