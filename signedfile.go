package pdf0

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file gives the signature verifier (package sign) what it needs from the
// source record: the file's bytes, its revisions, and — for the
// allowed-changes analysis — the objects that changed between the revision a
// signature covers and the file as it stands now (ISO 32000-2 12.8.2.2.2: "PDF
// processors may compare the signed and current versions of the document to
// see whether there have been modifications to any objects that are not
// permitted").
//
// The comparison is made between two states read from the file's bytes, not
// between the file and Document.Objects. The object graph is the newest state
// only, it is decrypted, and a caller may have edited it since Read; neither
// state here is any of those. Both are read raw — an encrypted file's strings
// and streams stay enciphered on both sides — so like is compared with like.

// errDiffBudget is returned when the analysis has done as much work as one
// verification is allowed. The verifier reports the changes after such a
// signature as unknown, never as permitted.
var errDiffBudget = errors.New("the revision comparison exceeded its work budget for this verification")

// signedFile implements core.SignedFile over a Document's source record. One is
// built per verification call; it memoizes what the calls of that verification
// share (the newest state's objects and decoded object streams) and charges
// every unit of work to one budget, so that a file with many revisions and many
// signatures cannot make verification quadratic without bound.
type signedFile struct {
	src    *Source
	lim    core.Limits
	cancel core.Canceler

	// work is what remains of the budget, in units of one cross-reference
	// entry compared or one object parsed.
	work int64
	// objStmLeft is what remains of the document's object-stream budget
	// (Limits.ObjectStreamBytes), metered as Read meters it: the decoded
	// bytes of every container, which are memoized here and so charged for
	// good, and the parser's estimate of every object unpacked from one
	// (syntax.Parser.Budget). Metering decoded bytes alone let the objects
	// take several times the bound (audit 2026-09-22 C9).
	objStmLeft int64

	final *revisionState
	diffs map[int]*core.RevisionDiff
}

// signedFile returns the file d was read from, as signature verification sees
// it. It is never nil: a Document with no source has a file of length 0 and
// no revisions, on which no signature verifies.
func (d *Document) signedFile(cancel core.Canceler) *signedFile {
	src := d.Source()
	f := &signedFile{src: src, lim: d.lim(), cancel: cancel, diffs: map[int]*core.RevisionDiff{}}
	f.objStmLeft = f.lim.ObjectStreamBytes
	entries := 0
	if src.merged != nil {
		entries = len(src.merged.Entries)
	}
	// Every signature of a well-formed file compares its revision's table
	// with the newest one, so the natural cost is (signatures × entries). The
	// budget allows 64 full comparisons of the newest table plus a fixed
	// allowance for small files; a file needing more is reported as
	// unanalysable rather than analysed for minutes.
	f.work = 64*int64(entries+1) + 1<<22
	if src.merged != nil && len(src.sections) > 0 {
		f.final = &revisionState{f: f, table: src.merged, data: src.data, trailer: src.sections[0].trailer}
	}
	return f
}

func (f *signedFile) Len() int64            { return f.src.Len() }
func (f *signedFile) ReaderAt() io.ReaderAt { return f.src.ReaderAt() }

// RevisionEnds implements core.SignedFile. A file Read had to rebuild has no
// revisions it can vouch for, and a revision no %%EOF closes has no end.
func (f *signedFile) RevisionEnds() []int64 {
	if f.src.rebuilt || f.final == nil {
		return nil
	}
	var ends []int64
	for _, r := range f.src.revisions {
		if r.End < 0 {
			break
		}
		ends = append(ends, r.End)
	}
	return ends
}

// charge takes n units from the budget and polls for cancellation.
func (f *signedFile) charge(n int64) error {
	if err := f.cancel.StopErr("comparing revisions"); err != nil {
		return err
	}
	f.work -= n
	if f.work < 0 {
		return errDiffBudget
	}
	return nil
}

// Diff implements core.SignedFile.
//
// The state at the end of revision rev is what a reader of the file truncated
// there sees: the cross-reference sections that lie before that end, merged
// newest first, with every object read from the truncated bytes. Sections that
// lie after it are later changes whatever revision the section grouping put
// them in — an update appended without a %%EOF of its own joins the revision
// before it in Source.Revisions, and must not be mistaken for part of the
// signed state.
//
// Every object number either state defines is compared. Two entries that are
// identical, for an object whose bytes (and, for a compressed object, whose
// container's bytes) end before the signed state does, name the same bytes in
// both states and cannot differ; everything else is read in both states and
// compared by value. An object the newest state reads differently only because
// a later revision changed an object it depends on — a stream's indirect
// /Length, an object stream holding it — is caught through that object, which
// is itself a change.
func (f *signedFile) Diff(rev int) (*core.RevisionDiff, error) {
	ends := f.RevisionEnds()
	if rev < 0 || rev >= len(ends) {
		return nil, fmt.Errorf("revision %d is not a revision of this file", rev)
	}
	if d, ok := f.diffs[rev]; ok {
		return d, nil
	}
	end := ends[rev]

	var secs []XRefSection
	closed := false
	for _, s := range f.src.sections {
		if s.revision > rev || s.offset >= end {
			continue
		}
		secs = append(secs, s)
		if s.end == end {
			closed = true
		}
	}
	if len(secs) == 0 || !closed {
		return nil, fmt.Errorf("no cross-reference section of the file ends at revision %d's %%%%EOF", rev)
	}
	oldTable := mergeSections(secs)
	newTable := f.src.merged
	old := &revisionState{f: f, table: oldTable, data: f.src.data[:end], trailer: secs[0].trailer}
	cur := f.final

	nums := make([]int, 0, len(newTable.Entries)+len(oldTable.Entries))
	for n := range newTable.Entries {
		nums = append(nums, n)
	}
	for n := range oldTable.Entries {
		if _, ok := newTable.Entries[n]; !ok {
			nums = append(nums, n)
		}
	}
	if err := f.charge(int64(len(nums))); err != nil {
		return nil, err
	}
	sort.Ints(nums)

	diff := &core.RevisionDiff{Old: old, New: cur}
	for _, num := range nums {
		if num == 0 {
			continue // the free-list head; never an object (see loadObjectsFromXref)
		}
		eo, inOld := oldTable.Entries[num]
		en, inNew := newTable.Entries[num]
		if inOld && inNew && eo == en && f.sameBytesBefore(eo, oldTable, newTable, end) {
			continue
		}
		ov, err := old.Object(num)
		if err != nil {
			return nil, fmt.Errorf("object %d as signed: %w", num, err)
		}
		nv, err := cur.Object(num)
		if err != nil {
			return nil, fmt.Errorf("object %d as it stands: %w", num, err)
		}
		if ov == nil && nv == nil {
			continue
		}
		if ov != nil && nv != nil && object.Equal(ov, nv) {
			continue
		}
		diff.Changes = append(diff.Changes, core.ObjectChange{Number: num, Old: ov, New: nv})
	}
	f.diffs[rev] = diff
	return diff, nil
}

// sameBytesBefore reports that an entry both tables share names bytes that
// end at or before end, so the object reads identically in both states.
func (f *signedFile) sameBytesBefore(e XRefEntry, oldTable, newTable *XRefTable, end int64) bool {
	if e.Compressed {
		co, ok1 := oldTable.Entries[e.StreamObjNum]
		cn, ok2 := newTable.Entries[e.StreamObjNum]
		if !ok1 || !ok2 || co != cn || co.Compressed {
			return false
		}
		e = co
	}
	objEnd, ok := f.src.ends[e.Offset]
	return ok && objEnd <= end
}

// revisionState reads the objects of one state of the file: a merged
// cross-reference table over a prefix of the file's bytes.
type revisionState struct {
	f       *signedFile
	table   *XRefTable
	data    []byte
	trailer *object.Dictionary

	objs map[int]stateObject
	// objStms memoizes decoded object streams by container number.
	objStms map[int]*decodedObjStm
}

type stateObject struct {
	v   object.Object
	err error
}

type decodedObjStm struct {
	data  []byte
	index []objStmEntry
	first int
	err   error
}

// Trailer implements core.ObjectReader.
func (s *revisionState) Trailer() *object.Dictionary {
	if s.trailer == nil {
		return &object.Dictionary{}
	}
	return s.trailer.Clone()
}

// Object implements core.ObjectReader. An entry pointing outside this state's
// bytes defines nothing in this state: the truncated file a reader of the
// signed revision had did not contain it.
func (s *revisionState) Object(num int) (object.Object, error) {
	if num <= 0 {
		return nil, nil
	}
	if o, ok := s.objs[num]; ok {
		return o.v, o.err
	}
	v, err := s.load(num)
	if s.objs == nil {
		s.objs = map[int]stateObject{}
	}
	s.objs[num] = stateObject{v: v, err: err}
	return v, err
}

func (s *revisionState) load(num int) (object.Object, error) {
	e, ok := s.table.Entries[num]
	if !ok {
		return nil, nil
	}
	if err := s.f.charge(1); err != nil {
		return nil, err
	}
	if !e.Compressed {
		iobj, err := s.parseAt(e.Offset)
		if err != nil || iobj == nil {
			return nil, err
		}
		return iobj.Value, nil
	}
	c, err := s.objStm(e.StreamObjNum)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	if e.IndexInStream < 0 || e.IndexInStream >= len(c.index) {
		return nil, fmt.Errorf("object stream %d has no index %d", e.StreamObjNum, e.IndexInStream)
	}
	ie := c.index[e.IndexInStream]
	if ie.Number != num {
		return nil, fmt.Errorf("object stream %d index %d holds object %d", e.StreamObjNum, e.IndexInStream, ie.Number)
	}
	p := syntax.NewParser(c.data)
	p.Budget = &s.f.objStmLeft
	p.SetOffset(int64(c.first + ie.Offset))
	return p.ParseObject()
}

// resolveUncompressed is the Resolver a container's /Filter and /DecodeParms
// are read through: it follows a reference to an uncompressed object of this
// state, and no further. A reference into an object stream is left
// unresolved, and the decode fails rather than guess — following it could
// need the very container being decoded.
func (s *revisionState) resolveUncompressed(o object.Object) object.Object {
	for hops := 0; hops < 64; hops++ {
		ref, ok := o.(object.IndirectRef)
		if !ok {
			return o
		}
		e, ok := s.table.Entries[ref.Number]
		if !ok || e.Compressed {
			return o
		}
		iobj, err := s.parseAt(e.Offset)
		if err != nil || iobj == nil {
			return nil
		}
		o = iobj.Value
	}
	return nil
}

// parseAt parses the indirect object at off in this state's bytes, resolving
// an indirect stream /Length through this state's table, as Read does. It
// returns nil, nil for an offset outside the bytes.
func (s *revisionState) parseAt(off int64) (*object.IndirectObject, error) {
	size := int64(len(s.data))
	if off < 0 || off >= size {
		return nil, nil
	}
	lx := syntax.NewLexer(s.data)
	lx.SetPosition(off)
	p := syntax.NewParserFromLexer(lx)
	p.ResolveLength = func(ref object.IndirectRef) (int64, bool) {
		le, ok := s.table.Entries[ref.Number]
		if !ok || le.Compressed || le.Offset < 0 || le.Offset >= size {
			return 0, false
		}
		llx := syntax.NewLexer(s.data)
		llx.SetPosition(le.Offset)
		return syntax.NewParserFromLexer(llx).IntegerObjectValue()
	}
	return p.ParseIndirectObject()
}

// objStm returns object stream num decoded, or nil when this state does not
// define it. The decoded bytes are charged to the document's aggregate
// object-stream budget, as a Read charges them.
func (s *revisionState) objStm(num int) (*decodedObjStm, error) {
	if c, ok := s.objStms[num]; ok {
		return c, c.err
	}
	if s.objStms == nil {
		s.objStms = map[int]*decodedObjStm{}
	}
	c := &decodedObjStm{}
	e, ok := s.table.Entries[num]
	switch {
	case !ok:
		s.objStms[num] = nil
		return nil, nil
	case e.Compressed:
		c.err = fmt.Errorf("object stream %d is itself stored in an object stream", num)
	default:
		iobj, err := s.parseAt(e.Offset)
		switch {
		case err != nil:
			c.err = err
		case iobj == nil:
			s.objStms[num] = nil
			return nil, nil
		default:
			st, isStream := iobj.Value.(*object.Stream)
			if !isStream {
				c.err = fmt.Errorf("object %d is not an object stream", num)
				break
			}
			if s.f.objStmLeft <= 0 {
				c.err = fmt.Errorf("object stream %d not decoded: the %d-byte object-stream budget is spent", num, s.f.lim.ObjectStreamBytes)
				break
			}
			// The decode is held to what the budget has left, so the check
			// includes this container.
			lim := s.f.lim
			if s.f.objStmLeft < int64(lim.DecodedStreamBytes) {
				lim.DecodedStreamBytes = int(s.f.objStmLeft)
			}
			c.data, c.index, c.first, c.err = parseObjStmIndex(s.f.cancel, st, lim, s.resolveUncompressed)
			s.f.objStmLeft -= int64(len(c.data))
		}
	}
	s.objStms[num] = c
	return c, c.err
}
