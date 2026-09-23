package pdf0

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mgilbir/pdf0/fonts"
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
// that must survive a round trip may live outside the object graph. What the
// structure said about the file itself — its bytes, sections, revisions and the
// object numbers it uses — is kept in the source record (source.go), for the
// operations that still need it: the object-number allocator, incremental
// writers, and byte-level checks.

// Document represents a parsed PDF file.
type Document struct {
	Version string                         // e.g., "2.0"
	Objects map[int]*object.IndirectObject // object number → object
	Trailer object.Dictionary
	// Encrypted reports whether the file carried an /Encrypt dictionary.
	// Standard-security-handler files are decrypted on Read (RC4, AES-128, and
	// AES-256) when the password — empty for Read — is the user or owner
	// password; their strings and streams are then in the clear but this flag
	// stays set. Otherwise the content stays encrypted and the document is
	// Locked (see LockReason). Write re-encrypts a decrypted document
	// (reproducing the original /Encrypt) and writes a Locked one back verbatim.
	Encrypted bool

	// valCache memoizes traversals for the duration of one validation run;
	// see validationCache.
	valCache *validationCache

	// source is the record of the file this document was read from; see
	// Source. nil for a document built in memory.
	source *Source

	// nextObjNum is the object-number allocator's hint; see allocObjNum. Zero
	// until the first allocation.
	nextObjNum int

	// faces is every face Page.Faces (or a form's or pattern's) has embedded
	// in this document, so each is embedded once; see faceembed.go.
	faces map[*fonts.Face]*faceEmbedding

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
	// skippedObjStms lists the containers Read did not unpack by its own
	// choice or inability — the materialisation budget, a decode limit, an
	// unsupported filter, ciphertext — which are not defects of the file and
	// must never be reported as one (objstm.go). Either leaves objects missing.
	brokenObjStms  []int
	skippedObjStms []core.SkippedObjStm

	// objStmLeft is the object-stream materialisation meter of the Read in
	// progress (objStmMeter), valid once objStmMetered is set, and
	// objStmCiphertext says whether a container is ciphertext pdf0 could not
	// decrypt. Both are Read's working state and mean nothing after it.
	objStmLeft       int64
	objStmMetered    bool
	objStmCiphertext func(num int) bool

	// decryptFailures lists the object numbers whose ciphertext did not decrypt
	// under a known-good file key — corrupt AES data, data that was never
	// encrypted, or a stream under a crypt filter the handler cannot apply (see
	// crypt.Handler.Decrypt). Their strings and stream bodies are empty rather
	// than noise, so the content is unrecoverable and Write refuses, exactly as
	// it does for brokenObjStms. DecryptFailures exposes it.
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

	// lockReason records why Read built no security handler for a file that
	// carries /Encrypt (see LockReason); encryptWarnings the defects that did
	// not prevent decryption (see EncryptionWarnings).
	lockReason      error
	encryptWarnings []error

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
// that cannot be decrypted — a wrong password, an unsupported scheme, or a
// malformed /Encrypt dictionary — is still parsed structurally, with its
// strings and streams left encrypted: see Document.Locked and LockReason. An
// /Encrypt dictionary never makes Read fail.
// Resource limits default to values safe for untrusted input; pass With* options
// to change them. The resolved limits are stored on the returned Document, so
// every validator and extractor that runs on it inherits the same configuration.
func Read(r io.ReaderAt, size int64, opts ...Option) (*Document, error) {
	return readDocument(core.Canceler{}, r, size, "", resolveLimits(opts))
}

// ReadWithPassword is Read with a user or owner password for an encrypted file.
// The password is prepared as ISO 32000-2 prescribes for the file's revision:
// SASLprep, UTF-8 and the first 127 bytes for AES-256 (revision 6), and
// PDFDocEncoding and the first 32 bytes for revisions 2–4. Its unprepared UTF-8
// bytes are tried as well, for files written by producers that skip the
// preparation; either must still match the file's password hash.
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
	// The size is the caller's claim: ReadSource refuses a negative one and
	// commits memory only as bytes arrive, so a claim larger than the source
	// fails without first allocating it (audit C123).
	data, err := syntax.ReadSource(r, size)
	if err != nil {
		return nil, err
	}

	// The source record keeps data itself rather than a copy; see Source.
	src := &Source{data: data}
	doc = &Document{
		Objects:    make(map[int]*object.IndirectObject),
		limits:     lim,
		readLimits: &core.Recorder{},
		source:     src,
	}

	// 1. Find header to extract version and header offset
	version, headerOffset, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	doc.Version = version

	// 2–3. Find startxref and walk the cross-reference chain from it. When
	// either fails — no startxref in the file, one that points outside it, or a
	// newest section that does not parse — the table is rebuilt by scanning for
	// object headers (the long-standing reader practice for damaged files; see
	// rebuildXRefByScan). The trailer then comes from a scan too; an
	// xref-stream file whose trailer IS the broken stream dictionary gets /Root
	// synthesized from the catalog after the objects load.
	var xrefTable *XRefTable
	rebuilt := false // the table was reconstructed by scanning (load leniently)
	var firstErr error
	budget := newFileInUseBudget(size)
	chainBroken := false
	xrefOffset, adjust, err := locateXRef(data, headerOffset)
	if err == nil {
		var sections []XRefSection
		sections, chainBroken, err = walkXRefChain(cancel, data, xrefOffset, adjust, lim, budget)
		if cerr := cancel.StopErr("reading PDF cross-reference chain"); cerr != nil {
			return nil, cerr
		}
		if err == nil {
			src.adjust = adjust
			src.sections = sections
			xrefTable = mergeSections(sections)
			// The section's trailer stays in the source record, and recovery
			// below may set /Root on the document's: clone it so that edit
			// never reaches the record.
			doc.Trailer = *sections[0].trailer.Clone() // dictcopy: a fresh clone; nothing else holds it
			doc.usedXRefStream = sections[0].kind == XRefStreamSection
		}
	}
	if err != nil {
		t := rebuildXRefByScan(data)
		if t == nil {
			return nil, err
		}
		xrefTable = t
		rebuilt, firstErr = true, err
		// A cross-reference stream pdf0 declined to decode is not a broken
		// table: the scan rebuilds one, but it cannot see what only the stream
		// said (compressed objects' containers, free entries), so the reader
		// must be told why the table is a reconstruction (audit 2026-09-22
		// C47). A table that is malformed rebuilds silently, as it always has.
		switch core.ReasonOf(err) {
		case core.ReasonLimit:
			doc.noteReadLimit(core.GuardDecodedStream, fmt.Sprintf("the cross-reference stream decodes to more than the %s-byte per-stream limit, so the table was rebuilt by scanning the file; an object only that stream located may be missing", core.LimitBound(int64(lim.DecodedStreamBytes), core.DefaultMaxDecodedStreamBytes)), 0)
		case core.ReasonUnsupported:
			doc.noteReadLimit(core.GuardUnsupportedFilter, "the cross-reference stream is encoded in a way pdf0 does not implement ("+err.Error()+"), so the table was rebuilt by scanning the file; an object only that stream located may be missing", 0)
		}
		if tr := findTrailerByScan(data); tr != nil {
			doc.Trailer = *tr // dictcopy: a fresh parse of the scanned trailer; nothing else holds it
		}
	}

	// 4. Parse all uncompressed objects from xref entries. A rebuilt table
	// loads leniently — an entry that does not parse is dropped, since a
	// header-shaped byte run inside a stream body can fabricate one. A table
	// the file itself supplied is authoritative, so a load failure there
	// triggers the same scan-rebuild as an unparseable section before giving
	// up (the sweep-13 holdout: the table parses, but every offset in it is
	// shifted and lands inside the previous object).
	if err := doc.loadObjectsFromXref(cancel, data, size, xrefTable, rebuilt); err != nil {
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
		if err2 := doc.loadObjectsFromXref(cancel, data, size, xrefTable, true); err2 != nil {
			return nil, err
		}
	}
	src.rebuilt = rebuilt
	src.merged = xrefTable
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

	// 4.5. Decrypt under the standard security handler: every string, and the
	// object-stream containers. This runs before object streams are
	// materialized: an /ObjStm container is an encrypted stream, but the objects
	// inside it are not separately encrypted. The other streams are decrypted in
	// step 5.5, once the graph is complete (see crypt.DecryptDocument).
	//
	// An /Encrypt dictionary never fails the read. A handler that cannot be
	// built — a wrong password, an unsupported scheme, a malformed dictionary —
	// leaves the document Locked with the reason recorded (LockReason).
	var pending *crypt.Pending
	if doc.Trailer.Get("Encrypt") != nil {
		h, err := crypt.Open(doc.graph(), password)
		if err != nil {
			doc.lockReason = err
		} else if h != nil {
			containers := map[int]bool{}
			for _, entry := range xrefTable.Entries {
				if entry.Compressed {
					containers[entry.StreamObjNum] = true
				}
			}
			pending = h.DecryptDocument(doc.graph(), func(num int, s *object.Stream) bool {
				t, _ := s.Dict.Get("Type").(object.Name)
				return containers[num] || t == "ObjStm"
			})
			doc.security = h
			doc.encryptWarnings = h.Warnings
		}
		// A container that is still ciphertext — the whole document when no
		// handler could be built, one container when its data did not decrypt
		// — is not unpacked, and is recorded as such rather than as a malformed
		// object stream (audit 2026-09-22 C47, C63).
		if h := doc.security; h == nil {
			doc.objStmCiphertext = func(int) bool { return true }
		} else {
			doc.objStmCiphertext = h.DecryptFailed
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
	doc.objStmCiphertext = nil // Read's working state (see the field)

	// 5.5. Decrypt the remaining streams, now that every object is loaded.
	if pending != nil {
		doc.decryptFailures = pending.Finish(doc.graph())
	}

	// 6. Record what the file uses, then drop file-structure artifacts so the
	// document holds only content.
	src.finish(doc, chainBroken)
	doc.normalizeStructure()

	doc.Encrypted = doc.Trailer.Get("Encrypt") != nil

	return doc, nil
}

// locateXRef finds the offset of the newest cross-reference section: the value
// after the file's last startxref keyword, corrected for a file whose offsets
// count from the %PDF- header rather than from the start of the file, and
// required to land inside the file.
//
// ISO 32000-2 7.5.5 has a reader look for startxref at the end of the file, and
// this looks in the last 1024 bytes first. A file with more trailing bytes than
// that after its %%EOF (padding, a second file concatenated by a transfer, a
// signature container) is searched in full, backwards, and the value used only
// if a section plausibly starts there. Every failure is returned as an error,
// on which Read rebuilds the table by scanning rather than giving up (audit
// 2026-09-22 C126).
func locateXRef(data []byte, headerOffset int64) (offset, adjust int64, err error) {
	offset, err = findStartXref(data)
	if err != nil {
		if i := bytes.LastIndex(data, []byte("startxref")); i >= 0 {
			if off, err2 := startXRefValue(data, i); err2 == nil && (xrefLooksValid(data, off) || xrefLooksValid(data, off+headerOffset)) {
				offset, err = off, nil
			}
		}
		if err != nil {
			return 0, 0, err
		}
	}
	// Byte offsets are specified from the start of the file (ISO 32000-1
	// 7.5.4), so absolute offsets are correct even when data precedes the
	// header. Some producers, however, prepend bytes without updating their
	// offsets, leaving them relative to %PDF-. Choose whichever convention
	// actually lands on the cross-reference section, preferring absolute.
	if !xrefLooksValid(data, offset) && headerOffset != 0 && xrefLooksValid(data, offset+headerOffset) {
		adjust = headerOffset
	}
	if abs := offset + adjust; abs < 0 || abs >= int64(len(data)) {
		return 0, 0, fmt.Errorf("startxref offset %d outside file (size %d)", abs, len(data))
	}
	return offset, adjust, nil
}

// walkXRefChain parses the cross-reference section at the stated offset and
// every older one its /Prev chain reaches, newest first. Both traditional
// tables and xref streams can carry /Prev (incremental updates), and a
// visited-set guards against cycles: a /Prev pointing at an already-seen
// section (or at itself) would otherwise loop forever on a crafted or corrupt
// file.
//
// Only the newest section is required: its failure is the returned error, on
// which Read rebuilds the table by scanning. A broken older section ends the
// chain there and sets broken, so Read can still count the numbers that
// section's objects use toward the high-water mark.
func walkXRefChain(cancel core.Canceler, data []byte, stated, adjust int64, lim core.Limits, budget *inUseBudget) (sections []XRefSection, broken bool, err error) {
	size := int64(len(data))
	visited := make(map[int64]bool)
	ends := eofEnds(data)
	for {
		// One iteration per incremental update; a file can carry thousands.
		if err := cancel.StopErr("reading PDF cross-reference chain"); err != nil {
			return nil, false, err
		}
		offset := stated + adjust
		if offset < 0 || offset >= size {
			// /Prev points outside the file; ignore the broken chain tail.
			return sections, true, nil
		}
		if visited[offset] {
			return sections, false, nil // cycle in the /Prev chain
		}
		visited[offset] = true

		sec, err := parseXRefSection(cancel, data, offset, adjust, lim, budget)
		if err != nil {
			// Recovery: the startxref value "shall [give] the byte offset ...
			// to the beginning of the xref keyword in the last cross-reference
			// section" (ISO 32000-2, 7.5.5). Real-world files violate this by
			// pointing a few dozen bytes INTO the table's entries instead
			// (Common Crawl sweep #13: consistently 55-57 bytes past the
			// keyword), which reads as an integer and mis-dispatches to the
			// xref-stream branch. Relocate to the spec-mandated target: the
			// nearest standalone "xref" keyword at or before the offset.
			if rec := precedingXrefKeyword(data, offset); rec >= 0 && !visited[rec] {
				visited[rec] = true
				sec, err = parseXRefSection(cancel, data, rec, adjust, lim, budget)
			}
			if err != nil {
				if len(sections) == 0 {
					return nil, false, err
				}
				return sections, true, nil // tolerate a broken older section
			}
		}
		sec.stated = stated
		sec.end = -1
		sec.end = ends.sectionEnd(stated, sec.offset)
		sections = append(sections, sec)

		prev, ok := sec.trailer.Get("Prev").(object.Integer)
		if !ok {
			return sections, false, nil
		}
		stated = int64(prev)
	}
}

// mergeSections builds the effective cross-reference table from a chain of
// sections, newest first: an object number takes the entry of the newest
// section that mentions it, and a newer section's free entry hides every older
// definition (ISO 32000-2 7.5.6). Within a section, in-use wins over free,
// which is how a hybrid section's /XRefStm entries replace the free entries its
// table lists (7.5.8.4); see XRefTable.
//
// A single section is used as it is, with no copy.
func mergeSections(sections []XRefSection) *XRefTable {
	if len(sections) == 1 {
		return sections[0].table
	}
	merged := &XRefTable{Entries: make(map[int]XRefEntry)}
	var hidden []XRefRange // free runs of the sections already merged, sorted
	isHidden := func(num int) bool {
		i := sort.Search(len(hidden), func(i int) bool { return hidden[i].Start+hidden[i].Count > num })
		return i < len(hidden) && hidden[i].Start <= num
	}
	for _, sec := range sections {
		for num, e := range sec.table.Entries {
			if _, done := merged.Entries[num]; done || isHidden(num) {
				continue
			}
			merged.Entries[num] = e
		}
		if len(sec.table.Free) > 0 {
			hidden = mergeRanges(append(hidden, sec.table.Free...))
		}
	}
	return merged
}

// loadObjectsFromXref parses every uncompressed object the cross-reference
// table lists into doc.Objects, recording where each one was found in the
// source record's offsets. In lenient mode (used for tables reconstructed by
// rebuildXRefByScan) an entry whose offset is out of range or whose bytes do
// not parse is dropped rather than failing the read: a scanned entry has no
// authority beyond the bytes it points at.
//
// Offsets in the table are absolute: walkXRefChain applies the header-offset
// correction when it parses each section.
func (doc *Document) loadObjectsFromXref(cancel core.Canceler, data []byte, size int64, xrefTable *XRefTable, lenient bool) error {
	offsets := make(map[int]int64)
	doc.source.offsets = offsets
	ends := make(map[int64]int64)
	doc.source.ends = ends
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
		if !ok || ent.Compressed {
			return 0, false
		}
		lo := ent.Offset
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
		if entry.Compressed {
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
			continue
		}

		off := entry.Offset
		if off < 0 || off >= size {
			if lenient {
				continue
			}
			// A negative or out-of-range offset (e.g. a crafted "-0000000010"
			// entry, or an 8-byte /W field whose high bit overflowed int) would
			// otherwise seek the lexer to an invalid position.
			return fmt.Errorf("object %d xref offset %d outside file (size %d)", num, off, size)
		}
		offsets[num] = off
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
				delete(offsets, num)
				continue
			}
			return fmt.Errorf("parsing object %d at offset %d: %w", num, off, err)
		}
		// The cross-reference key is the authoritative object number: readers
		// resolve references through the xref, so the body's declared number
		// must not override it. Otherwise a body "3 0 obj" reached via xref slot
		// 4 would be written back numbered 3 under slot 4 — dangling for any
		// other reader (audit C7).
		iobj.Number = num
		doc.Objects[num] = iobj
		parsedByOffset[off] = iobj
		ends[off] = parser.Offset()
	}
	return nil
}

// finish completes the source record once every object is loaded: the highest
// object number the file uses, the largest /Size it declares, the object
// streams it holds, and its revisions.
//
// The high-water mark counts every number the file puts to any use: in-use and
// free entries of every section, each cross-reference stream's own number
// (listed in its own table or not), the objects Read loaded, and the numbers
// object streams index (noted as they were unpacked). When the /Prev chain
// broke partway, the sections past the break were never read, so the numbers
// found by scanning the file for object headers count too: an allocator that
// ignored them could hand out a number an older revision still defines.
func (s *Source) finish(doc *Document, chainBroken bool) {
	note := s.note
	noteSize := func(tr *object.Dictionary) {
		if tr == nil {
			return
		}
		if v, ok := tr.Get("Size").(object.Integer); ok && v > 0 {
			sz := int(min(int64(v), int64(syntax.MaxObjectNumber)+1))
			if sz > s.size {
				s.size = sz
			}
		}
	}
	if s.containers == nil {
		s.containers = map[int]bool{}
	}
	for _, sec := range s.sections {
		note(sec.objNum)
		note(sec.xrefStmObjNum)
		noteSize(sec.trailer)
		for num, e := range sec.table.Entries {
			note(num)
			if e.Compressed {
				note(e.StreamObjNum)
			}
		}
		if f := sec.table.Free; len(f) > 0 {
			note(f[len(f)-1].Start + f[len(f)-1].Count - 1)
		}
	}
	if s.merged != nil {
		for num, e := range s.merged.Entries {
			note(num)
			if e.Compressed && e.StreamObjNum > 0 {
				s.containers[e.StreamObjNum] = true
			}
		}
	}
	for num, iobj := range doc.Objects {
		note(num)
		if st, ok := iobj.Value.(*object.Stream); ok {
			if t, _ := st.Dict.Get("Type").(object.Name); t == "ObjStm" {
				s.containers[num] = true
			}
		}
	}
	if s.rebuilt || len(s.sections) == 0 {
		noteSize(&doc.Trailer)
	}
	if chainBroken {
		if t := rebuildXRefByScan(s.data); t != nil {
			for num := range t.Entries {
				note(num)
			}
		}
	}
	s.assignRevisions()
}

// note raises the high-water mark to n. It is how the object-stream loaders
// count the numbers a container's index lists, including ones no
// cross-reference entry names. A nil record (a hand-built Document) ignores it.
func (s *Source) note(n int) {
	if s != nil && n > s.maxNum && n <= syntax.MaxObjectNumber {
		s.maxNum = n
	}
}

// normalizeStructure removes cross-reference plumbing from the parsed
// document. An xref stream's dictionary doubles as the trailer, so a document
// read from a modern file would otherwise carry xref-stream-only keys in
// doc.Trailer and re-emit stale /XRef and /ObjStm objects on Write — encoding
// obsolete offsets contradicting the rewritten file (audit C5). Object-stream
// contents are already materialized as ordinary objects, and Write always
// regenerates the cross-reference structure and /Size, so nothing the graph
// needs is lost; what incremental writers need — the numbers those objects
// used, and the file's /Size — is kept in the source record (see Source).
func (d *Document) normalizeStructure() {
	for num, iobj := range d.Objects {
		if stream, ok := iobj.Value.(*object.Stream); ok {
			if t, ok := stream.Dict.Get("Type").(object.Name); ok && (t == "XRef" || t == "ObjStm") {
				delete(d.Objects, num)
				// Drop the byte offset too: leaving it among the offsets makes
				// the byte-level file-structure checks treat the removed object's
				// span as part of the previous surviving object's region,
				// mis-attributing errors and skipping the last real object's
				// endobj check (audit C9).
				if d.source != nil {
					delete(d.source.offsets, num)
				}
			}
		}
	}
	trailer := d.Trailer.Clone()
	for _, key := range []object.Name{"Type", "W", "Index", "Filter", "DecodeParms", "Length", "Prev", "XRefStm", "Size"} {
		trailer.Delete(key)
	}
	d.Trailer = *trailer // dictcopy: installs the edited clone; nothing else holds it
}

// parseXRefSection parses one cross-reference section at the given absolute
// offset: a traditional table followed by its trailer, or an xref stream, whose
// dictionary doubles as the trailer. A table whose trailer carries /XRefStm is
// a hybrid-reference section, and the stream it names is parsed as part of it
// (ISO 32000-2 7.5.8.4); a section whose /XRefStm stream cannot be read fails
// as a whole, so that Read falls back to rebuilding the table by scan, which
// recovers the compressed objects from their object streams, rather than
// silently losing every object only that stream lists.
//
// Offsets in the returned entries are absolute (adjust applied). Nothing is
// added to the document: the xref stream is loaded like any other object if
// the effective table lists it, and dropped by normalizeStructure. Loading it
// here instead let an older section's stream shadow a newer section's
// redefinition of its number (audit 2026-09-22 C3).
func parseXRefSection(cancel core.Canceler, data []byte, offset, adjust int64, lim core.Limits, budget *inUseBudget) (XRefSection, error) {
	lexer := NewLexer(data)
	lexer.SetPosition(offset)
	tok, err := lexer.NextToken()
	if err != nil {
		return XRefSection{}, fmt.Errorf("reading xref at offset %d: %w", offset, err)
	}

	sec := XRefSection{offset: offset}
	switch tok.Type {
	case syntax.TokenXref:
		table, err := parseXRefTable(data, lexer.Position(), budget)
		if err != nil {
			return XRefSection{}, fmt.Errorf("parsing xref table: %w", err)
		}
		trailer, err := findTrailer(data, lexer.Position())
		if err != nil {
			return XRefSection{}, fmt.Errorf("parsing trailer: %w", err)
		}
		sec.kind, sec.table, sec.trailer = XRefTableSection, table, trailer
		if v, ok := trailer.Get("XRefStm").(object.Integer); ok {
			stm, num, _, err := parseXRefStreamAt(cancel, data, int64(v)+adjust, lim, budget)
			if err != nil {
				return XRefSection{}, fmt.Errorf("parsing /XRefStm stream: %w", err)
			}
			for n, e := range stm.Entries {
				if _, inTable := table.Entries[n]; !inTable {
					table.Entries[n] = e
				}
			}
			table.Free = append(table.Free, stm.Free...)
			table.normalizeFree()
			sec.kind, sec.xrefStm, sec.xrefStmObjNum = XRefHybridSection, int64(v)+adjust, num
		}

	case syntax.TokenInteger:
		table, num, trailer, err := parseXRefStreamAt(cancel, data, offset, lim, budget)
		if err != nil {
			return XRefSection{}, err
		}
		sec.kind, sec.table, sec.trailer, sec.objNum = XRefStreamSection, table, trailer, num

	default:
		return XRefSection{}, fmt.Errorf("expected 'xref' or object number at offset %d, got %v", offset, tok.Type)
	}
	if adjust != 0 {
		for n, e := range sec.table.Entries {
			if !e.Compressed {
				e.Offset += adjust
				sec.table.Entries[n] = e
			}
		}
	}
	return sec, nil
}

// parseXRefStreamAt parses the cross-reference stream object at an absolute
// offset and returns its entries, its object number and its dictionary.
func parseXRefStreamAt(cancel core.Canceler, data []byte, offset int64, lim core.Limits, budget *inUseBudget) (*XRefTable, int, *object.Dictionary, error) {
	if offset < 0 || offset >= int64(len(data)) {
		return nil, 0, nil, fmt.Errorf("xref stream offset %d outside file (size %d)", offset, len(data))
	}
	lexer := NewLexer(data)
	lexer.SetPosition(offset)
	iobj, err := NewParserFromLexer(lexer).ParseIndirectObject()
	if err != nil {
		return nil, 0, nil, fmt.Errorf("parsing xref stream object: %w", err)
	}
	stream, ok := iobj.Value.(*object.Stream)
	if !ok {
		return nil, 0, nil, fmt.Errorf("xref stream object is not a stream")
	}
	// The document's own resolved limits, not defaultLimits(): a
	// cross-reference stream is a Flate stream the file controls like any
	// other, so a caller who lowered WithMaxDecodedStreamBytes for untrusted
	// uploads has to get that ceiling here too.
	//
	// This costs nothing under the defaults, which is the only configuration
	// the corpus exercises. Measured across 3,102 files (the veraPDF corpus,
	// the Cal Poly PDF/VT suite, the WTPDF set, the Factur-X invoices and the
	// PDF 2.0 reference files), 930 cross-reference stream sections decode to
	// at most 430,350 bytes — 0.4% of the 100 MB default, and none above
	// 1 MiB. A caller has to go two orders of magnitude below the default
	// before this bound is what stops their read.
	table, err := parseXRefStream(cancel, stream, lim, budget)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("parsing xref stream: %w", err)
	}
	return table, iobj.Number, &stream.Dict, nil
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

// findStartXref finds the byte offset stored after the last startxref keyword
// in the final 1024 bytes of the file.
func findStartXref(data []byte) (int64, error) {
	searchLen := 1024
	if len(data) < searchLen {
		searchLen = len(data)
	}
	base := len(data) - searchLen
	idx := bytes.LastIndex(data[base:], []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("startxref not found")
	}
	return startXRefValue(data, base+idx)
}

// startXRefValue reads the offset after the startxref keyword at idx.
func startXRefValue(data []byte, idx int) (int64, error) {
	pos := idx + len("startxref")
	for pos < len(data) && syntax.IsWhitespace(data[pos]) {
		pos++
	}
	numStart := pos
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if numStart == pos {
		return 0, fmt.Errorf("no offset after startxref")
	}
	offset, err := strconv.ParseInt(string(data[numStart:pos]), 10, 64)
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
	if d.Locked() {
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
		if err := d.missingObjectsErr("cannot write encrypted document"); err != nil {
			return err
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
	for num, iobj := range d.Objects {
		if num < worst {
			worst = num
		}
		// A nil entry is a caller mistake — a map written into directly, or an
		// allocation whose error went unchecked — and it is one the serializer
		// would meet as a nil dereference several layers down. Naming it here
		// is the difference between an error and the process ending.
		if iobj == nil {
			return fmt.Errorf("object %d is nil; every entry in Objects must hold an object", num)
		}
	}
	if worst < 0 {
		return fmt.Errorf("object number %d is not a valid object number (ISO 32000-2 7.3.10 requires a positive integer)", worst)
	}
	// A broken or skipped object stream left some objects unmaterialised during
	// Read; the document may reference them, so writing would emit dangling
	// references (audit C19).
	if err := d.missingObjectsErr("cannot write"); err != nil {
		return err
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
		// Which crypt filter a stream uses depends on the document (embedded
		// files follow /EFF, the catalog's metadata may be exempt), so the
		// context comes from the model, not the packed write set.
		enc, err := d.security.EncryptCopy(writeObjects, d.security.StreamContext(d.view()))
		if err != nil {
			return fmt.Errorf("cannot write encrypted document: %w", err)
		}
		writeObjects = enc
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

	return s.WriteIndirectObject(&object.IndirectObject{Number: xrefObjNum, Value: object.NewStream(dict, encoded)})
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
	entryLine := func(num int) (string, error) {
		if num == 0 {
			return "0000000000 65535 f\r\n", nil
		}
		gen := 0
		if obj, ok := objects[num]; ok {
			gen = obj.Generation
		}
		line, err := xrefLine(offsets[num], gen, false)
		if err != nil {
			return "", fmt.Errorf("object %d: %w", num, err)
		}
		return line, nil
	}

	// Object 0 (the free-list head) always begins the first subsection;
	// objects numbered from 1 up continue it.
	section := []int{0}
	flush := func() error {
		if err := s.WriteString(fmt.Sprintf("%d %d\n", section[0], len(section))); err != nil {
			return err
		}
		for _, num := range section {
			line, err := entryLine(num)
			if err != nil {
				return err
			}
			if err := s.WriteString(line); err != nil {
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
	v := core.View{Version: d.Version, Encrypted: d.Encrypted, Locked: d.Locked(), Objects: d.Objects, Offsets: d.Source().offsets, Trailer: &d.Trailer, BrokenObjStms: d.brokenObjStms, SkippedObjStms: d.skippedObjStms, DecryptFailures: d.decryptFailures, UsedXRefStream: d.usedXRefStream, EmbeddedDepth: d.embeddedDepth, Limits: d.lim(), Cancel: d.canceler(), Alloc: d.allocObjNum}
	if d.valCache != nil {
		v.Run = d.valCache.run.shared
	}
	return v
}

// Add stores an object under the next free object number and returns a
// reference to it. It is how a writer grows the object graph without having to
// track numbering: font embedding, image embedding and anything else that adds
// several linked objects at once need one allocator between them.
//
// The number is above every number the source file uses — including the
// cross-reference streams and object streams Read removes from Objects, and
// free entries (see Source.MaxObjectNumber) — and above every key in Objects,
// so it collides neither with the file nor with the graph. A number Add has
// handed out is never handed out again. Adds cost O(1) amortised.
func (d *Document) Add(value object.Object) object.IndirectRef {
	n := d.allocObjNum()
	d.Objects[n] = &object.IndirectObject{Number: n, Value: value}
	return object.IndirectRef{Number: n}
}
