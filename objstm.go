package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
	"sort"
	"strconv"
)

// This file implements reading object streams (/Type /ObjStm, ISO 32000-2
// 7.5.7): decoding a container, parsing its leading index of (object number,
// offset) pairs, and materializing the objects that type-2 cross-reference
// entries point into. It also covers the recovery path, where a rebuilt
// cross-reference table carries no type-2 entries and every container must
// instead be unpacked wholesale.
//
// Object streams are the format's compression-amplification vector, so
// decompression is budgeted in aggregate across a single Read
// (see WithMaxObjectStreamBytes). A container that fails to decode, or that
// exceeds the budget, is recorded in Document.brokenObjStms instead of failing
// the read: its objects go missing, but the document still parses and the
// defect stays reportable.

// The aggregate decompressed size of all object streams materialized during a
// single Read defaults to defaultMaxObjectStreamBytes; a caller can change it
// with WithMaxObjectStreamBytes. Object streams are the compression-
// amplification vector: a small file can carry many object streams that
// decompress to hundreds of megabytes of small objects (e.g. arrays of
// references), which the parser then materializes as live objects. The heaviest
// real document measured across the veraPDF corpus and a Common Crawl sample
// needs 9 MB.

// objStmEntry is one (object number, byte offset) pair from an object
// stream's leading index. Offsets are relative to /First.
type objStmEntry struct {
	Number int
	Offset int
}

// parseObjStmIndex decodes an object stream (/Type /ObjStm, ISO 32000-2:2020
// 7.5.7) and parses its leading index of N (object number, offset) pairs.
// It returns the decoded data alongside the index so callers can parse
// individual objects without decoding twice.
func parseObjStmIndex(cancel core.Canceler, stream *object.Stream, lim core.Limits) (data []byte, entries []objStmEntry, first int, err error) {
	if t, ok := stream.Dict.Get("Type").(object.Name); ok && t != "ObjStm" {
		return nil, nil, 0, fmt.Errorf("not an object stream: /Type /%s", t)
	}
	n, ok := stream.Dict.Get("N").(object.Integer)
	if !ok || n < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /N missing or invalid")
	}
	firstInt, ok := stream.Dict.Get("First").(object.Integer)
	if !ok || firstInt < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /First missing or invalid")
	}

	data, err = core.DecodeStreamData(cancel, stream, lim)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("decoding object stream: %w", err)
	}
	if int64(firstInt) > int64(len(data)) {
		return nil, nil, 0, fmt.Errorf("object stream /First %d beyond data length %d", firstInt, len(data))
	}
	// Each index pair needs at least 4 bytes ("N O "); reject absurd /N
	// before allocating. Divide rather than multiply: int64(n)*4 overflows for
	// /N near MaxInt64, wrapping negative and defeating the guard, which then
	// panics in make([]objStmEntry, 0, int(n)).
	if int64(n) > int64(firstInt)/4 {
		return nil, nil, 0, fmt.Errorf("object stream /N %d does not fit in /First %d bytes", n, firstInt)
	}

	lexer := NewLexer(data[:firstInt])
	entries = make([]objStmEntry, 0, int(n))
	for i := 0; i < int(n); i++ {
		num, err := nextIntToken(lexer)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("object stream index pair %d: %w", i, err)
		}
		off, err := nextIntToken(lexer)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("object stream index pair %d: %w", i, err)
		}
		if num < 0 || off < 0 || int64(firstInt)+int64(off) > int64(len(data)) {
			return nil, nil, 0, fmt.Errorf("object stream index pair %d out of range: obj %d offset %d", i, num, off)
		}
		entries = append(entries, objStmEntry{Number: num, Offset: off})
	}
	return data, entries, int(firstInt), nil
}

// nextIntToken reads one integer token from the lexer.
func nextIntToken(l *syntax.Lexer) (int, error) {
	tok, err := l.NextToken()
	if err != nil {
		return 0, err
	}
	if tok.Type != syntax.TokenInteger {
		return 0, fmt.Errorf("expected integer, got %v", tok.Type)
	}
	v, err := strconv.Atoi(string(tok.Value))
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q: %w", tok.Value, err)
	}
	return v, nil
}

// materializeScannedObjStms loads the contents of every /Type /ObjStm
// container present in doc.Objects. It backs cross-reference rebuild (see
// rebuildXRefByScan): a scanned table carries no type-2 entries, so the
// objects inside object streams are recovered from the containers themselves.
// Numbers already defined win — a top-level definition found by the scan is
// newer or equal in authority to a compressed one — and a container that does
// not decode is recorded in brokenObjStms exactly like the normal path. The
// same aggregate decompression budget applies.
//
// The only error it returns is a cancellation: a container that fails is
// recorded, not reported, which is the point of the recovery path.
func (d *Document) materializeScannedObjStms(cancel core.Canceler) error {
	var containers []int
	for num, iobj := range d.Objects {
		if st, ok := iobj.Value.(*object.Stream); ok {
			if t, _ := st.Dict.Get("Type").(object.Name); t == "ObjStm" {
				containers = append(containers, num)
			}
		}
	}
	sort.Ints(containers) // deterministic order, like loadCompressedObjects
	objStmBudget := d.lim().ObjectStreamBytes
	var decompressed int64
	for _, cnum := range containers {
		// Per container, as in loadCompressedObjects: one iteration decompresses
		// one object stream (cancel.go).
		if err := cancel.StopErr("reading PDF object streams"); err != nil {
			return err
		}
		st := d.Objects[cnum].Value.(*object.Stream)
		if decompressed >= objStmBudget {
			d.brokenObjStms = append(d.brokenObjStms, cnum)
			d.noteReadLimit(limitObjStmTotal, fmt.Sprintf("object stream %d was not unpacked: this read has already decompressed %d bytes of object streams, reaching the %s-byte budget for one read; its objects are missing from the document, so any finding of the form \"X is absent\" may be a consequence of that", cnum, decompressed, core.LimitBound(objStmBudget, core.DefaultMaxObjectStreamBytes)), cnum)
			continue
		}
		data, index, first, err := parseObjStmIndex(cancel, st, d.lim())
		if err != nil {
			d.brokenObjStms = append(d.brokenObjStms, cnum)
			continue
		}
		decompressed += int64(len(data))
		for _, ie := range index {
			if ie.Number <= 0 {
				continue
			}
			if _, exists := d.Objects[ie.Number]; exists {
				continue
			}
			parser := NewParser(data)
			parser.Lexer().SetPosition(int64(first + ie.Offset))
			obj, err := parser.ParseObject()
			if err != nil {
				continue // drop just this object; the container index may lie
			}
			d.Objects[ie.Number] = &object.IndirectObject{Number: ie.Number, Value: obj}
		}
	}
	return nil
}

// loadCompressedObjects materializes objects stored in object streams
// (type-2 xref entries) into doc.Objects. Container streams must already be
// loaded; each container is decoded and indexed once regardless of how many
// of its objects are referenced.
func (d *Document) loadCompressedObjects(cancel core.Canceler, table *XRefTable) error {
	// Group requested object numbers by container so each object stream is
	// decoded exactly once.
	byContainer := make(map[int][]int)
	for num, entry := range table.Entries {
		if entry.Free || !entry.Compressed {
			continue
		}
		if num == 0 {
			// Object number 0 is the free-list head and can never be an in-use
			// object; see the same skip in the uncompressed load loop.
			continue
		}
		if _, exists := d.Objects[num]; exists {
			continue
		}
		byContainer[entry.StreamObjNum] = append(byContainer[entry.StreamObjNum], num)
	}

	// Process containers in object-number order so that, if the aggregate
	// decompression budget is reached, the set of object streams left
	// unmaterialized is deterministic rather than dependent on map iteration.
	containers := make([]int, 0, len(byContainer))
	for containerNum := range byContainer {
		containers = append(containers, containerNum)
	}
	sort.Ints(containers)

	objStmBudget := d.lim().ObjectStreamBytes
	var decompressed int64
	for _, containerNum := range containers {
		// Per container: one iteration decompresses at most one object stream,
		// which the per-stream cap already bounds. This is the other unbounded
		// loop in a read (the uncompressed object load is the first), and the one
		// that can decompress half a gigabyte from a small file.
		if err := cancel.StopErr("reading PDF object streams"); err != nil {
			return err
		}
		objNums := byContainer[containerNum]
		container, ok := d.Objects[containerNum]
		if !ok {
			return fmt.Errorf("object stream %d referenced by xref but not present", containerNum)
		}
		stream, ok := container.Value.(*object.Stream)
		if !ok {
			return fmt.Errorf("object stream %d is not a stream", containerNum)
		}
		// Once the aggregate decompressed object-stream budget is exhausted,
		// stop materializing further streams (recorded as broken, like an
		// undecodable one) to bound the parser's work and memory.
		if decompressed >= objStmBudget {
			d.brokenObjStms = append(d.brokenObjStms, containerNum)
			d.noteReadLimit(limitObjStmTotal, fmt.Sprintf("object stream %d was not unpacked: this read has already decompressed %d bytes of object streams, reaching the %s-byte budget for one read; its objects are missing from the document, so any finding of the form \"X is absent\" may be a consequence of that", containerNum, decompressed, core.LimitBound(objStmBudget, core.DefaultMaxObjectStreamBytes)), containerNum)
			continue
		}
		// A corrupt object stream (e.g. undecodable data) makes only its own
		// objects unavailable; recording it lets validation report the defect
		// while the rest of the document is still parsed rather than aborting
		// the whole read.
		data, index, first, err := parseObjStmIndex(cancel, stream, d.lim())
		if err != nil {
			d.brokenObjStms = append(d.brokenObjStms, containerNum)
			continue
		}
		decompressed += int64(len(data))
		for _, num := range objNums {
			entry := table.Entries[num]
			idx := entry.IndexInStream
			if idx < 0 || idx >= len(index) {
				return fmt.Errorf("object %d: index %d out of range in object stream %d (N=%d)", num, idx, containerNum, len(index))
			}
			ie := index[idx]
			if ie.Number != num {
				return fmt.Errorf("object %d: object stream %d index %d holds object %d", num, containerNum, idx, ie.Number)
			}
			parser := NewParser(data)
			parser.Lexer().SetPosition(int64(first + ie.Offset))
			obj, err := parser.ParseObject()
			if err != nil {
				return fmt.Errorf("parsing object %d in object stream %d: %w", num, containerNum, err)
			}
			// Objects in an object stream always have generation 0.
			d.Objects[num] = &object.IndirectObject{Number: num, Value: obj}
		}
	}
	return nil
}
