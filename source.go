package pdf0

import (
	"bytes"
	"io"
	"sort"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file holds the source record: what Read learned about the file itself,
// as opposed to the object graph it built from it (ISO 32000-2 7.5: the body,
// the cross-reference sections, the trailers, and the incremental updates that
// chain them). Read normalises the file structure out of Document.Objects and
// Document.Trailer, because Write regenerates it; the record is where the facts
// that normalisation removes are kept, for the operations that still need them.
//
// It also holds the document's one object-number allocator, which is the main
// such operation: a number is only free if nothing in the file uses it.

// Source is the record of the file a Document was read from. Read fills it in
// and nothing changes it afterwards, so it can be shared freely: validating a
// Document on several goroutines, or holding a Source after the Document has
// been edited, is safe. A Document built in memory has an empty Source (every
// accessor returns its zero value).
//
// It records:
//
//   - the file's bytes (ReaderAt, Len);
//   - every cross-reference section Read parsed, in /Prev-chain order, newest
//     first (Sections), and the revisions they form (Revisions);
//   - the highest object number the file uses anywhere, and the largest /Size
//     any trailer declared (MaxObjectNumber, DeclaredSize);
//   - where each object Read parsed at the top level sits in the file (Offset).
//
// # What it is for
//
// The object graph cannot say which numbers the file already uses: Read drops
// cross-reference streams and object-stream containers from Objects, because
// Write regenerates them, and a free entry is not an object at all. A writer
// that numbered new objects one past the highest key in Objects therefore
// reused numbers the file still used, and an incremental update written that
// way redefined an object stream's number, so every object inside it became
// unreadable for every reader (audit 2026-09-22 C3). Document.Add and every
// writer allocate above MaxObjectNumber and DeclaredSize for this reason.
//
// Incremental writers (WriteIncremental, WriteSignedIncremental,
// WriteArchivalTimestamp) append to the bytes recorded here and chain their new
// section to the newest one in Sections. Signature verification and byte-level
// conformance checks need the same bytes and the same revision boundaries.
//
// # Memory
//
// Read already holds the whole file in memory while it parses; the record keeps
// that one buffer rather than copying it, so a Document read from an N-byte file
// retains N bytes more than it did before the record existed, for as long as the
// Document (or a Source obtained from it) is reachable. Stream data in the object
// graph is a separate copy: decrypting or editing a stream never changes the
// recorded bytes.
type Source struct {
	data   []byte
	adjust int64 // added to every offset the file states; see Read's header-offset recovery

	sections  []XRefSection
	revisions []Revision
	rebuilt   bool

	// merged is the effective cross-reference table: every in-use entry the
	// chain defines, newest section first, with offsets absolute.
	merged *XRefTable
	// containers are the object-stream numbers the file's type-2 entries point
	// into, plus every /Type /ObjStm object Read found. Redefining or freeing
	// one in an incremental update would orphan the objects stored in it.
	containers map[int]bool

	maxNum  int
	size    int
	offsets map[int]int64
}

// noSource is the empty record a Document built in memory reports.
var noSource = &Source{}

// Source returns the record of the file d was read from. It is never nil: a
// Document built in memory, or produced by an operation that builds a new
// document (ExtractPages, NewDocument, …), has an empty record.
func (d *Document) Source() *Source {
	if d == nil || d.source == nil {
		return noSource
	}
	return d.source
}

// Len returns the size of the file in bytes, or 0 for an empty record.
func (s *Source) Len() int64 { return int64(len(s.data)) }

// ReaderAt returns a reader over the file's bytes, exactly as Read received
// them. It is a read-only view of the retained buffer, not a copy.
func (s *Source) ReaderAt() io.ReaderAt { return bytes.NewReader(s.data) }

// Rebuilt reports that Read could not use the file's own cross-reference data
// and rebuilt the object table by scanning the bytes for object headers (a
// missing or unreadable startxref, or a first section that does not parse or
// whose offsets do not land on objects). Sections then lists whatever sections
// did parse, but none of them described the objects Read loaded, and an
// incremental update cannot be chained onto the file.
func (s *Source) Rebuilt() bool { return s.rebuilt }

// Sections returns the cross-reference sections Read parsed, in the order the
// /Prev chain visits them: the section startxref names first, then each /Prev
// in turn. A hybrid-reference section's /XRefStm stream is part of the section
// that names it, not a section of its own. The slice is a copy.
func (s *Source) Sections() []XRefSection {
	return append([]XRefSection(nil), s.sections...)
}

// Revisions returns the file's revisions, oldest first. The original file is
// the first revision and each incremental update adds one (ISO 32000-2 7.5.6).
// A linearized file's first-page section is part of the revision whose main
// section it chains to, not a revision of its own. The slices are copies.
func (s *Source) Revisions() []Revision {
	out := make([]Revision, len(s.revisions))
	for i, r := range s.revisions {
		out[i] = Revision{End: r.End, Sections: append([]int(nil), r.Sections...)}
	}
	return out
}

// MaxObjectNumber returns the highest object number the file uses anywhere:
// every entry of every cross-reference section, free and compressed ones
// included, the cross-reference streams and object streams themselves, and the
// objects the object streams hold. No object the file already defines has a
// higher number. It is 0 for an empty record.
func (s *Source) MaxObjectNumber() int { return s.maxNum }

// DeclaredSize returns the largest /Size any of the file's trailers declared
// (ISO 32000-2 Table 15: one more than the highest object number in the file).
// An incremental update never writes a smaller one. It is 0 for an empty
// record.
func (s *Source) DeclaredSize() int { return s.size }

// Entry returns the cross-reference entry through which Read resolved object
// num: the newest section's in-use entry for it, with a hybrid section's
// /XRefStm entries taking the place of the free entries its table lists
// (ISO 32000-2 7.5.8.4). An uncompressed entry's Offset is absolute — it
// includes the correction Read applies to a file whose stated offsets count
// from the %PDF- header instead of the start of the file.
func (s *Source) Entry(num int) (XRefEntry, bool) {
	if s.merged == nil {
		return XRefEntry{}, false
	}
	e, ok := s.merged.Entries[num]
	return e, ok
}

// Offset returns the absolute byte offset at which Read parsed object num: an
// object stored at the top level of the file, and not a cross-reference stream
// or object stream (those are file structure, dropped from Objects). Objects
// read from object streams have no offset of their own.
func (s *Source) Offset(num int) (int64, bool) {
	off, ok := s.offsets[num]
	return off, ok
}

// Offsets returns a copy of every offset Offset reports, keyed by object
// number.
func (s *Source) Offsets() map[int]int64 {
	out := make(map[int]int64, len(s.offsets))
	for k, v := range s.offsets {
		out[k] = v
	}
	return out
}

// nextFree returns the lowest object number above everything the file uses.
func (s *Source) nextFree() int {
	n := s.maxNum + 1
	if s.size > n {
		n = s.size
	}
	return n
}

// isContainer reports whether num is one of the file's object streams.
func (s *Source) isContainer(num int) bool { return s.containers[num] }

// XRefKind is the form of a cross-reference section.
type XRefKind int

const (
	// XRefTableSection is a traditional cross-reference table and trailer
	// (ISO 32000-2 7.5.4, 7.5.5).
	XRefTableSection XRefKind = iota + 1
	// XRefStreamSection is a cross-reference stream, whose dictionary is also
	// the trailer (7.5.8).
	XRefStreamSection
	// XRefHybridSection is a traditional table whose trailer names a
	// cross-reference stream in /XRefStm, holding the objects stored in object
	// streams (7.5.8.4).
	XRefHybridSection
)

func (k XRefKind) String() string {
	switch k {
	case XRefTableSection:
		return "table"
	case XRefStreamSection:
		return "stream"
	case XRefHybridSection:
		return "hybrid"
	}
	return "unknown"
}

// XRefSection is one cross-reference section of the file: a table and its
// trailer, a cross-reference stream, or a hybrid of the two. Its entries are
// those of the section alone; Source.Entry gives the effective entry across
// the whole chain.
type XRefSection struct {
	offset        int64
	stated        int64 // the offset startxref or /Prev gave, before any correction
	kind          XRefKind
	objNum        int
	xrefStm       int64
	xrefStmObjNum int
	end           int64
	revision      int
	trailer       *object.Dictionary
	table         *XRefTable
}

// Offset returns the absolute byte offset of the section: of its xref keyword,
// or of its cross-reference stream's "N G obj" header.
func (x XRefSection) Offset() int64 { return x.offset }

// Kind returns the section's form.
func (x XRefSection) Kind() XRefKind { return x.kind }

// ObjectNumber returns the cross-reference stream's object number for a stream
// section, and 0 for a table or hybrid section.
func (x XRefSection) ObjectNumber() int { return x.objNum }

// XRefStm returns the absolute offset and object number of a hybrid section's
// /XRefStm stream; ok is false for any other section.
func (x XRefSection) XRefStm() (offset int64, objNum int, ok bool) {
	if x.kind != XRefHybridSection {
		return 0, 0, false
	}
	return x.xrefStm, x.xrefStmObjNum, true
}

// End returns the offset one past the %%EOF marker that closes this section's
// update (and the end-of-line after it): the one after the startxref that names
// this section, or, for a section no startxref names (a linearized file's main
// section), the first one after the section. It is -1 when no %%EOF follows.
func (x XRefSection) End() int64 { return x.end }

// Revision returns the index, into Source.Revisions, of the revision this
// section belongs to.
func (x XRefSection) Revision() int { return x.revision }

// Trailer returns a copy of the section's trailer dictionary: the dictionary
// after the trailer keyword, or the cross-reference stream's dictionary.
func (x XRefSection) Trailer() *object.Dictionary {
	if x.trailer == nil {
		return &object.Dictionary{}
	}
	return x.trailer.Clone()
}

// Entry returns this section's in-use entry for num. Offsets are absolute.
func (x XRefSection) Entry(num int) (XRefEntry, bool) {
	if x.table == nil {
		return XRefEntry{}, false
	}
	e, ok := x.table.Entries[num]
	return e, ok
}

// IsFree reports whether this section marks num free, deleting whatever an
// older section defined under it.
func (x XRefSection) IsFree(num int) bool { return x.table.IsFree(num) }

// Numbers returns the object numbers this section defines in use, sorted.
func (x XRefSection) Numbers() []int {
	if x.table == nil {
		return nil
	}
	out := make([]int, 0, len(x.table.Entries))
	for n := range x.table.Entries {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// Revision is one revision of the file: the bytes from its start up to End.
type Revision struct {
	// End is the offset one past the %%EOF marker (and its end-of-line) that
	// closes the revision, or -1 when none does. The revision's bytes are
	// [0, End); an incremental update that follows begins at End.
	End int64
	// Sections lists the indices, into Source.Sections, of the sections this
	// revision added, newest first. Most revisions add one; a linearized file's
	// first revision adds two.
	Sections []int
}

// assignRevisions groups the sections into revisions from their %%EOF ends.
//
// Walking the chain from the oldest section, a revision ends at the furthest
// %%EOF seen so far: a section whose own end lies before that — a linearized
// file's first-page section, which sits at the front of the file but chains to
// the main section at the back — belongs to the same revision as the older
// section, not to one of its own. A section with no end (no %%EOF follows it)
// joins the revision of the older section before it in the walk, or forms one
// with End -1 when it is the oldest.
func (s *Source) assignRevisions() {
	running := int64(-1)
	var ends []int64
	endOf := make([]int64, len(s.sections))
	for i := len(s.sections) - 1; i >= 0; i-- {
		if e := s.sections[i].end; e > running {
			running = e
		}
		endOf[i] = running
		if len(ends) == 0 || ends[len(ends)-1] != running {
			ends = append(ends, running)
		}
	}
	s.revisions = make([]Revision, len(ends))
	idx := make(map[int64]int, len(ends))
	for i, e := range ends {
		s.revisions[i].End = e
		idx[e] = i
	}
	for i := range s.sections {
		r := idx[endOf[i]]
		s.sections[i].revision = r
		s.revisions[r].Sections = append(s.revisions[r].Sections, i)
	}
}

// eofIndex locates the %%EOF markers that close a file's updates.
type eofIndex struct {
	// byValue maps each value a "startxref N %%EOF" sequence states to the
	// offset one past its %%EOF marker and end-of-line (ISO 32000-2 7.5.5).
	// When a value occurs more than once, the last occurrence wins, as it does
	// for the reader looking for the file's startxref.
	byValue map[int64]int64
	// all holds every such end, ascending.
	all []int64
}

// sectionEnd returns where the update containing the section at offset, which
// the chain reached through the stated value, ends: the %%EOF after the
// startxref that names it, or else the first %%EOF after the section itself.
// The second case is a section no startxref names — a linearized file's main
// section, reached only through the first-page section's /Prev — whose bytes
// the next %%EOF closes. It returns -1 when no %%EOF follows.
func (e eofIndex) sectionEnd(stated, offset int64) int64 {
	if end, ok := e.byValue[stated]; ok {
		return end
	}
	i := sort.Search(len(e.all), func(i int) bool { return e.all[i] > offset })
	if i < len(e.all) {
		return e.all[i]
	}
	return -1
}

// eofEnds indexes every "startxref N %%EOF" sequence in data.
func eofEnds(data []byte) eofIndex {
	ends := eofIndex{byValue: map[int64]int64{}}
	key := []byte("startxref")
	for i := 0; ; {
		j := bytes.Index(data[i:], key)
		if j < 0 {
			return ends
		}
		p := i + j + len(key)
		i = p
		for p < len(data) && syntax.IsWhitespace(data[p]) {
			p++
		}
		start := p
		var v int64
		for p < len(data) && data[p] >= '0' && data[p] <= '9' && p-start < 19 {
			v = v*10 + int64(data[p]-'0')
			p++
		}
		if p == start {
			continue
		}
		for p < len(data) && syntax.IsWhitespace(data[p]) {
			p++
		}
		if !bytes.HasPrefix(data[p:], []byte("%%EOF")) {
			continue
		}
		p += len("%%EOF")
		if p < len(data) && data[p] == '\r' {
			p++
		}
		if p < len(data) && data[p] == '\n' {
			p++
		}
		ends.byValue[v] = int64(p)
		ends.all = append(ends.all, int64(p))
	}
}

// allocObjNum returns an object number nothing uses: not an object in
// d.Objects, not any number the source file uses (Source.MaxObjectNumber,
// Source.DeclaredSize), and not a number handed out before. It is the
// document's only allocator; Add, the signing and time-stamping writers,
// SetEncryption, AppendPages, EmbedFacturX and the incremental writer's own
// cross-reference stream all draw from it.
//
// The first call starts above the highest key in d.Objects and the file's
// high-water mark; later calls continue from a hint, stepping over any number
// a caller has since stored in Objects directly. That makes n allocations cost
// O(n) in total, where scanning Objects for its maximum on every call made a
// builder adding 40,000 objects take ten seconds (audit 2026-09-22 C96).
//
// A number handed out is never handed out again, even if the caller deletes
// the object stored under it.
func (d *Document) allocObjNum() int {
	if d.Objects == nil {
		d.Objects = map[int]*object.IndirectObject{}
	}
	if d.nextObjNum == 0 {
		n := d.Source().nextFree()
		for num := range d.Objects {
			if num >= n {
				n = num + 1
			}
		}
		if n < 1 {
			n = 1
		}
		d.nextObjNum = n
	}
	for {
		if _, used := d.Objects[d.nextObjNum]; !used {
			break
		}
		d.nextObjNum++
	}
	n := d.nextObjNum
	d.nextObjNum++
	return n
}

// updateClone returns a copy of d for a writer that adds objects to it without
// touching d: a new Objects map holding d's object values, a copy of the
// trailer, and everything that decides whether and how the copy may be written
// — the source record, the allocator's position, the resource limits, and the
// objects Read could not recover. The signing and time-stamping writers build
// their update on it. Leaving the record behind let them number new objects
// over the file's object streams (audit 2026-09-22 C3); leaving the broken
// object streams behind let an incremental signature be written over a
// document missing objects, which WriteIncremental otherwise refuses.
func (d *Document) updateClone(extra int) *Document {
	clone := &Document{
		Version:         d.Version,
		Objects:         make(map[int]*object.IndirectObject, len(d.Objects)+extra),
		Trailer:         *d.Trailer.Clone(),
		Encrypted:       d.Encrypted,
		source:          d.source,
		nextObjNum:      d.nextObjNum,
		limits:          d.limits,
		brokenObjStms:   d.brokenObjStms,
		decryptFailures: d.decryptFailures,
		readLimits:      d.readLimits,
		usedXRefStream:  d.usedXRefStream,
	}
	for num, iobj := range d.Objects {
		clone.Objects[num] = iobj
	}
	return clone
}
