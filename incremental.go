package pdf0

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file implements incremental update (ISO 32000-2 7.5.6): appending a new
// body, cross-reference section and trailer to a file rather than rewriting it.
// The original bytes are copied verbatim and never re-serialized, which is the
// entire point — it is what keeps an existing digital signature over them valid
// and lets the update be undone by truncation.
//
// The original bytes, the section the update chains to, the numbers the file
// already uses and its /Size all come from the document's source record (see
// Source); an update is always an update of the file the document was read
// from. Encrypted documents are refused.

// WriteIncremental writes an incremental update of the file d was read from:
// that file's bytes verbatim, followed by the objects listed in changed, a new
// cross-reference section whose /Prev chains back to the file's newest section,
// and a new trailer. The original bytes are preserved exactly, so any signature
// over them stays valid and the update can be undone by truncation.
//
// changed lists the object numbers whose current value in d.Objects should be
// (re)written; numbers absent from d.Objects are recorded as deleted, with a
// free entry whose generation is one more than the object's (ISO 32000-2
// 7.5.4). New objects must be numbered by Add (or another writer that uses the
// document's allocator), which never reuses a number the file uses.
//
// The update's cross-reference section has the form of the section it chains
// to: a cross-reference stream after a cross-reference stream, a table after a
// table or a hybrid section. ISO 32000-2 7.5.8.1 says a file that uses
// cross-reference streams throughout uses neither the xref nor the trailer
// keyword, and a table whose /Prev names a stream is not the hybrid form of
// 7.5.8.4 (which names its stream in /XRefStm and whose /Prev chain is made of
// tables), so a table appended to a stream-only file would make a file of
// neither form.
//
// The trailer keeps every entry of the document's trailer, with /Prev set,
// /Size never below the file's own (Source.DeclaredSize), and the second /ID
// string replaced by one computed from the updated file's contents: ISO 32000-2
// 14.4 makes the first string permanent and the second a changing identifier
// "based on the file's contents at the time it was last updated". (Write, which
// serialises the document model rather than updating a file, writes /ID as the
// model holds it, so that reading and writing a document changes nothing the
// caller did not change; a caller who wants a new identifier sets it.)
//
// Nothing is written to w unless the whole update serialises: an object that
// cannot be written leaves w untouched. An error from w itself can still leave
// part of the output behind.
//
// d must have been read from a file (it has a non-empty Source), not rebuilt by
// scan (Source.Rebuilt), and not encrypted. An update is appended to the file d
// was read from, not to the output of an earlier WriteIncremental; read that
// output back to update it again.
func (d *Document) WriteIncremental(w io.Writer, changed []int) error {
	update, err := d.incrementalUpdate(changed)
	if err != nil {
		return err
	}
	if _, err := w.Write(d.source.data); err != nil {
		return err
	}
	_, err = w.Write(update)
	return err
}

// incrementalBytes returns the source file followed by the update, as one
// buffer the signing writers can patch in place. The source record's own bytes
// are never patched: they are copied here.
func (d *Document) incrementalBytes(changed []int) ([]byte, error) {
	update, err := d.incrementalUpdate(changed)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(d.source.data)+len(update))
	out = append(out, d.source.data...)
	return append(out, update...), nil
}

// xrefUpdateEntry is one entry of an update's cross-reference section.
type xrefUpdateEntry struct {
	num  int
	free bool
	off  int64 // in use: the stated offset; free: the next free object number
	gen  int
}

// incrementalUpdate serialises the bytes an incremental update appends to the
// source file.
func (d *Document) incrementalUpdate(changed []int) ([]byte, error) {
	src := d.source
	if src == nil || len(src.data) == 0 {
		return nil, errors.New("incremental update needs the file the document was read from; this document was built in memory, so use Write")
	}
	if src.rebuilt || len(src.sections) == 0 {
		return nil, errors.New("incremental update needs the file's cross-reference chain, and this file's was unusable (the table was rebuilt by scanning), so use Write")
	}
	if d.security != nil || d.Encrypted {
		return nil, errors.New("incremental update of an encrypted document is not supported")
	}
	if len(changed) == 0 {
		return nil, errors.New("incremental update with no changed objects")
	}
	// Some objects never made it into d.Objects: an object stream failed to
	// decode, or the aggregate decompression budget stopped it being unpacked.
	// Write refuses such a document (audit C19) because the object graph it
	// writes is incomplete; an update over it would reference objects that are
	// missing, so it refuses for the same reason.
	if len(d.brokenObjStms) > 0 {
		return nil, fmt.Errorf("incremental: %d object stream(s) failed to decode on read, so some objects are missing", len(d.brokenObjStms))
	}

	nums := append([]int(nil), changed...)
	sort.Ints(nums)
	nums = compactInts(nums)
	for _, num := range nums {
		// Object numbers are positive integers (ISO 32000-2 §7.3.10); 0 is
		// reserved as the free-list head.
		if num <= 0 || num > syntax.MaxObjectNumber {
			return nil, fmt.Errorf("incremental: %d is not a valid object number (ISO 32000-2 7.3.10 requires a positive integer, and pdf0 writes at most %d)", num, syntax.MaxObjectNumber)
		}
		// Redefining or deleting an object stream would orphan every object
		// stored in it: their type-2 entries name the container by number, and
		// the update would give that number to something else (audit
		// 2026-09-22 C3, the writer side).
		if src.isContainer(num) {
			return nil, fmt.Errorf("incremental: object %d is an object stream of the source file; redefining or deleting it would orphan the objects stored in it", num)
		}
	}

	base := int64(len(src.data))
	// stated turns a position in buf into the offset the file states for it,
	// in the same convention as the file's own offsets (see Source.adjust).
	stated := func(pos int64) int64 { return base + pos - src.adjust }

	var buf bytes.Buffer
	if last := src.data[base-1]; last != '\n' && last != '\r' {
		buf.WriteByte('\n')
	}
	s := NewSerializer(&buf)

	var entries []xrefUpdateEntry
	var freed []int
	for _, num := range nums {
		iobj := d.Objects[num]
		if iobj == nil {
			freed = append(freed, num)
			continue
		}
		off := stated(s.Offset())
		// The map key is the object number (as Read makes it); write the header
		// under it, so the header and the cross-reference entry agree.
		if iobj.Number != num {
			iobj = &object.IndirectObject{Number: num, Generation: iobj.Generation, Value: iobj.Value}
		}
		if err := s.WriteIndirectObject(iobj); err != nil {
			return nil, fmt.Errorf("incremental: writing object %d: %w", num, err)
		}
		entries = append(entries, xrefUpdateEntry{num: num, off: off, gen: iobj.Generation})
	}
	if len(freed) > 0 {
		// Deleted objects: each free entry's generation is one more than the
		// object's, so the number could be reused only under the new generation,
		// and the free entries are linked into a list headed by object 0 (ISO
		// 32000-2 7.5.4). A generation already at 65535 stays there: that
		// number is never reused.
		entries = append(entries, xrefUpdateEntry{num: 0, free: true, off: int64(freed[0]), gen: syntax.MaxGeneration})
		for i, num := range freed {
			gen := 0
			if e, ok := src.merged.Entries[num]; ok && !e.Compressed {
				gen = e.Generation
			}
			if gen < syntax.MaxGeneration {
				gen++
			}
			next := 0
			if i+1 < len(freed) {
				next = freed[i+1]
			}
			entries = append(entries, xrefUpdateEntry{num: num, free: true, off: int64(next), gen: gen})
		}
	}

	size := src.nextFree()
	if n := nums[len(nums)-1] + 1; n > size {
		size = n
	}

	newest := src.sections[0]
	trailer := d.Trailer.Clone()
	for _, key := range []object.Name{"Type", "W", "Index", "Filter", "DecodeParms", "Length", "XRefStm"} {
		trailer.Delete(key)
	}
	trailer.Set("Prev", object.Integer(newest.offset-src.adjust))
	refreshSecondID(trailer, src.data, buf.Bytes())

	xrefPos := s.Offset()
	if newest.kind == XRefStreamSection {
		xnum := d.allocObjNum()
		if xnum > syntax.MaxObjectNumber {
			return nil, fmt.Errorf("incremental: no object number is left for the cross-reference stream")
		}
		if xnum+1 > size {
			size = xnum + 1
		}
		entries = append(entries, xrefUpdateEntry{num: xnum, off: stated(xrefPos)})
		sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })
		trailer.Set("Size", object.Integer(size))
		if err := writeUpdateXRefStream(s, entries, trailer, xnum); err != nil {
			return nil, err
		}
	} else {
		sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })
		if err := writeUpdateXRefTable(s, entries); err != nil {
			return nil, err
		}
		trailer.Set("Size", object.Integer(size))
		if err := s.WriteString("trailer\n"); err != nil {
			return nil, err
		}
		if err := s.WriteDictionary(trailer); err != nil {
			return nil, err
		}
		if err := s.WriteString("\n"); err != nil {
			return nil, err
		}
	}
	if err := s.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", stated(xrefPos))); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compactInts removes adjacent duplicates from a sorted slice.
func compactInts(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// refreshSecondID sets the trailer's /ID to [first second], where first is the
// file's permanent identifier (kept when the trailer has a well-formed one) and
// second an MD5 digest of the updated file's contents so far (ISO 32000-2 14.4
// suggests MD5 over the file's contents for both). When the file has no usable
// /ID, the digest serves as both.
func refreshSecondID(trailer *object.Dictionary, parts ...[]byte) {
	h := md5.New()
	for _, p := range parts {
		h.Write(p)
	}
	second := object.String{Value: h.Sum(nil), IsHex: true}
	first := object.Object(object.String{Value: append([]byte(nil), second.Value...), IsHex: true})
	if arr, ok := trailer.Get("ID").(object.Array); ok && len(arr) == 2 {
		if s0, ok := arr[0].(object.String); ok && len(s0.Value) > 0 {
			first = s0
		}
	}
	trailer.Set("ID", object.Array{first, second})
}

// writeUpdateXRefTable writes an update's traditional cross-reference section:
// one subsection per run of consecutive object numbers.
func writeUpdateXRefTable(s *syntax.Serializer, entries []xrefUpdateEntry) error {
	if err := s.WriteString("xref\n"); err != nil {
		return err
	}
	for i := 0; i < len(entries); {
		j := i
		for j+1 < len(entries) && entries[j+1].num == entries[j].num+1 {
			j++
		}
		if err := s.WriteString(fmt.Sprintf("%d %d\n", entries[i].num, j-i+1)); err != nil {
			return err
		}
		for k := i; k <= j; k++ {
			line, err := xrefLine(entries[k].off, entries[k].gen, entries[k].free)
			if err != nil {
				return fmt.Errorf("object %d: %w", entries[k].num, err)
			}
			if err := s.WriteString(line); err != nil {
				return err
			}
		}
		i = j + 1
	}
	return nil
}

// xrefLine formats one 20-byte cross-reference table entry (ISO 32000-2
// 7.5.4): a 10-digit offset (or next free object number), a 5-digit
// generation, the type byte and a two-byte end-of-line. A value that does not
// fit its field would lengthen the line and misalign every reader that relies
// on the fixed format, so it is an error, never a longer line (audit
// 2026-09-22 C125).
func xrefLine(off int64, gen int, free bool) (string, error) {
	if off < 0 || off > 9999999999 {
		return "", fmt.Errorf("offset %d does not fit the 10-digit cross-reference field", off)
	}
	if gen < 0 || gen > syntax.MaxGeneration {
		return "", fmt.Errorf("generation %d is outside 0..%d", gen, syntax.MaxGeneration)
	}
	kind := "n"
	if free {
		kind = "f"
	}
	return fmt.Sprintf("%010d %05d %s\r\n", off, gen, kind), nil
}

// writeUpdateXRefStream writes an update's cross-reference section as a
// cross-reference stream object numbered xnum, whose dictionary carries the
// trailer entries. entries must be sorted and include xnum's own entry.
func writeUpdateXRefStream(s *syntax.Serializer, entries []xrefUpdateEntry, trailer *object.Dictionary, xnum int) error {
	var max2, max3 uint64
	for _, e := range entries {
		if uint64(e.off) > max2 {
			max2 = uint64(e.off)
		}
		if uint64(e.gen) > max3 {
			max3 = uint64(e.gen)
		}
	}
	w := [3]int{1, byteWidth(max2), byteWidth(max3)}
	var body bytes.Buffer
	put := func(v uint64, width int) {
		for i := width - 1; i >= 0; i-- {
			body.WriteByte(byte(v >> (8 * uint(i))))
		}
	}
	var index object.Array
	for i := 0; i < len(entries); {
		j := i
		for j+1 < len(entries) && entries[j+1].num == entries[j].num+1 {
			j++
		}
		index = append(index, object.Integer(entries[i].num), object.Integer(j-i+1))
		for k := i; k <= j; k++ {
			e := entries[k]
			typ := uint64(1)
			if e.free {
				typ = 0
			}
			put(typ, w[0])
			put(uint64(e.off), w[1])
			put(uint64(e.gen), w[2])
		}
		i = j + 1
	}
	dict := trailer.Clone()
	dict.Set("Type", object.Name("XRef"))
	dict.Set("W", object.Array{object.Integer(w[0]), object.Integer(w[1]), object.Integer(w[2])})
	dict.Set("Index", index)
	encoded := core.FlateEncode(body.Bytes())
	dict.Set("Filter", object.Name("FlateDecode"))
	dict.Set("Length", object.Integer(len(encoded)))
	return s.WriteIndirectObject(&object.IndirectObject{Number: xnum, Value: object.NewStream(dict, encoded)})
}
