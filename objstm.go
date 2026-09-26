package pdf0

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file implements reading object streams (/Type /ObjStm, ISO 32000-2
// 7.5.7): decoding a container, parsing its leading index of (object number,
// offset) pairs, and materializing the objects that type-2 cross-reference
// entries point into. It also covers the recovery path, where a rebuilt
// cross-reference table carries no type-2 entries and every container must
// instead be unpacked wholesale.
//
// Object streams are the format's compression-amplification vector, so what
// they materialise is budgeted in aggregate across a single Read (see
// WithMaxObjectStreamBytes and objStmMeter). A container that fails to decode
// is recorded in Document.brokenObjStms, and one pdf0 does not unpack by its
// own choice — the budget, a limit, a filter, ciphertext — in
// Document.skippedObjStms, instead of failing the read: its objects go missing,
// but the document still parses and the reason stays reportable.
//
// The budget is on the memory the unpacked objects take, estimated by the
// parser as it builds them (syntax.Parser.Budget), plus the decoded bytes of
// the container being unpacked. A small file can carry object streams that
// decompress to hundreds of megabytes of small objects (arrays of references),
// and the objects are several times larger in memory than in the file, so
// metering the decoded bytes left the real cost five times the bound (audit
// 2026-09-22 C9).

// objStmEntry is one (object number, byte offset) pair from an object
// stream's leading index. Offsets are relative to /First.
type objStmEntry struct {
	Number int
	Offset int
}

// parseObjStmIndex decodes an object stream (/Type /ObjStm, ISO 32000-2:2020
// 7.5.7) and parses its leading index of N (object number, offset) pairs.
// It returns the decoded data alongside the index so callers can parse
// individual objects without decoding twice. resolve follows indirect filter
// entries (core.Resolver); nil when there is no object graph to follow.
func parseObjStmIndex(cancel core.Canceler, stream *object.Stream, lim core.Limits, resolve core.Resolver) (data []byte, entries []objStmEntry, first int, err error) {
	if t, ok := stream.Dict.Get("Type").(object.Name); ok && t != "ObjStm" {
		return nil, nil, 0, fmt.Errorf("not an object stream: /Type %s", t)
	}
	n, ok := stream.Dict.Get("N").(object.Integer)
	if !ok || n < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /N missing or invalid")
	}
	firstInt, ok := stream.Dict.Get("First").(object.Integer)
	if !ok || firstInt < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /First missing or invalid")
	}

	data, err = core.DecodeStreamData(cancel, stream, lim, resolve)
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

// What became of a container that was not unpacked. A container is broken when
// the file is at fault — it is missing, not a stream, its data does not decode,
// its index lies — and a rule may say so (ISO 19005 6.1.7: "malformed"). It is
// skipped when pdf0 did not unpack it by its own choice or inability: the
// materialisation budget, the per-stream decode limit, a filter it does not
// implement, ciphertext it could not decrypt. A skipped container is not
// malformed and no rule may say it is; the trip is recorded here, once, so
// every validator reports it under "limit" (audit 2026-09-22 C47). Both leave
// the container's objects missing, so Write refuses either.

func (d *Document) objStmBroken(num int) {
	d.brokenObjStms = append(d.brokenObjStms, num)
}

func (d *Document) objStmSkipped(num int, r core.Reason, guard, why string) {
	d.skippedObjStms = append(d.skippedObjStms, core.SkippedObjStm{Num: num, Reason: r})
	d.noteReadLimit(guard, fmt.Sprintf("object stream %d was not unpacked: %s; its objects are missing from the document, so any finding of the form \"X is absent\" may be a consequence of that", num, why), num)
}

// missingObjectsErr is the error an operation that needs the whole object graph
// returns when Read could not recover all of it, or nil. what names the
// operation.
func (d *Document) missingObjectsErr(what string) error {
	switch b, s := len(d.brokenObjStms), len(d.skippedObjStms); {
	case b > 0 && s > 0:
		return fmt.Errorf("%s: %d object stream(s) failed to decode on read and %d were not unpacked (a resource limit, an unsupported filter or undecrypted data), so some objects are missing", what, b, s)
	case b > 0:
		return fmt.Errorf("%s: %d object stream(s) failed to decode on read, so some objects are missing", what, b)
	case s > 0:
		return fmt.Errorf("%s: %d object stream(s) were not unpacked on read (a resource limit, an unsupported filter or undecrypted data), so some objects are missing", what, s)
	}
	return nil
}

// objStmMeter is the materialisation meter of one Read: what is left of
// Limits.ObjectStreamBytes once the estimated memory of every object unpacked
// from an object stream so far, and of the container being unpacked, is
// charged. It meters what the parse builds rather than the bytes decoded,
// because the objects are what stays: fifteen million "1 0 R" references are
// 90 MB of decoded text and several times that in memory (audit 2026-09-22 C9).
func (d *Document) objStmMeter() *int64 {
	if !d.objStmMetered {
		d.objStmLeft = d.lim().ObjectStreamBytes
		d.objStmMetered = true
	}
	return &d.objStmLeft
}

// openObjStm decodes container num and parses its index, within what the
// meter has left, recording why when it cannot (broken or skipped). The
// decoded bytes are charged to the meter while the container's objects are
// parsed; closeObjStm returns them. The only error is a cancellation.
//
// The check includes this container: the decode itself is capped at what the
// meter has left, so a container that does not fit is refused as it decodes
// rather than after it has been materialised. Checking only whether the
// budget was already spent let one container overshoot it by the per-stream
// decode limit.
func (d *Document) openObjStm(cancel core.Canceler, num int) (data []byte, index []objStmEntry, first int, ok bool, err error) {
	iobj, found := d.Objects[num]
	if !found {
		d.objStmBroken(num)
		return nil, nil, 0, false, nil
	}
	st, isStream := iobj.Value.(*object.Stream)
	if !isStream {
		d.objStmBroken(num)
		return nil, nil, 0, false, nil
	}
	if d.objStmCiphertext != nil && d.objStmCiphertext(num) {
		d.objStmSkipped(num, core.ReasonLocked, core.GuardLocked, "it is ciphertext pdf0 could not decrypt")
		return nil, nil, 0, false, nil
	}
	lim := d.lim()
	left := d.objStmMeter()
	budgetBound := func() string {
		return fmt.Sprintf("unpacking it would take the objects this read materialises from object streams past the %s-byte budget for one read", core.LimitBound(lim.ObjectStreamBytes, core.DefaultMaxObjectStreamBytes))
	}
	if *left <= 0 {
		d.objStmSkipped(num, core.ReasonLimit, limitObjStmTotal, budgetBound())
		return nil, nil, 0, false, nil
	}
	byBudget := false
	if *left < int64(lim.DecodedStreamBytes) {
		lim.DecodedStreamBytes = int(*left)
		byBudget = true
	}
	data, index, first, err = parseObjStmIndex(cancel, st, lim, d.Resolve)
	if err != nil {
		switch r := core.ReasonOf(err); r {
		case core.ReasonCanceled:
			return nil, nil, 0, false, err
		case core.ReasonLimit:
			if byBudget {
				d.objStmSkipped(num, r, limitObjStmTotal, budgetBound())
			} else {
				d.objStmSkipped(num, r, core.GuardDecodedStream, fmt.Sprintf("it decodes to more than the %s-byte per-stream limit", core.LimitBound(int64(lim.DecodedStreamBytes), core.DefaultMaxDecodedStreamBytes)))
			}
		case core.ReasonUnsupported:
			d.objStmSkipped(num, r, core.GuardUnsupportedFilter, "it is encoded in a way pdf0 does not implement ("+err.Error()+")")
		default:
			d.objStmBroken(num)
		}
		return nil, nil, 0, false, nil
	}
	*left -= int64(len(data))
	return data, index, first, true, nil
}

// closeObjStm returns a container's decoded bytes to the meter once its
// objects are parsed: they are not retained, since the parser copies every
// string it builds, and what the objects themselves cost has been charged.
func (d *Document) closeObjStm(data []byte) {
	*d.objStmMeter() += int64(len(data))
}

// parseObjStmObject parses the object at offset off of a container's data,
// charging the meter for what it builds. overBudget reports that the meter
// ran out: the container must not be read further.
func (d *Document) parseObjStmObject(data []byte, off int64) (obj object.Object, overBudget bool, err error) {
	parser := NewParser(data)
	parser.Budget = d.objStmMeter()
	parser.SetOffset(off)
	obj, err = parser.ParseObject()
	if errors.Is(err, syntax.ErrBudget) {
		return nil, true, err
	}
	return obj, false, err
}

// materializeScannedObjStms loads the contents of every /Type /ObjStm
// container present in doc.Objects. It backs cross-reference rebuild (see
// rebuildXRefByScan): a scanned table carries no type-2 entries, so the
// objects inside object streams are recovered from the containers themselves.
// Numbers already defined win — a top-level definition found by the scan is
// newer or equal in authority to a compressed one — and a container that is
// not unpacked is recorded exactly as on the normal path (openObjStm), under
// the same materialisation meter.
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
	for _, cnum := range containers {
		// Per container, as in loadCompressedObjects: one iteration decompresses
		// one object stream (cancel.go).
		if err := cancel.StopErr("reading PDF object streams"); err != nil {
			return err
		}
		data, index, first, ok, err := d.openObjStm(cancel, cnum)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		for _, ie := range index {
			d.source.note(ie.Number)
			if ie.Number <= 0 {
				continue
			}
			if _, exists := d.Objects[ie.Number]; exists {
				continue
			}
			obj, overBudget, err := d.parseObjStmObject(data, int64(first+ie.Offset))
			if overBudget {
				d.objStmSkipped(cnum, core.ReasonLimit, limitObjStmTotal, fmt.Sprintf("its objects would take what this read materialises from object streams past the %s-byte budget for one read", core.LimitBound(d.lim().ObjectStreamBytes, core.DefaultMaxObjectStreamBytes)))
				break
			}
			if err != nil {
				continue // drop just this object; the container index may lie
			}
			d.Objects[ie.Number] = &object.IndirectObject{Number: ie.Number, Value: obj}
		}
		d.closeObjStm(data)
	}
	return nil
}

// loadCompressedObjects materializes objects stored in object streams
// (type-2 xref entries) into doc.Objects. Container streams must already be
// loaded; each container is decoded and indexed once regardless of how many
// of its objects are referenced.
//
// A container that cannot supply the objects the table places in it is
// recorded as broken and the read continues, exactly as for one whose data
// does not decode: the container is missing, is not a stream, lists fewer
// objects than an entry's index, or holds an object that does not parse. Those
// objects are then absent, validation can report why, and Write refuses rather
// than emit a document missing them. Failing the whole read instead (as this
// did) made one bad entry cost every other object in the file, with no scan
// rebuild to fall back on (audit 2026-09-22 C102). A container pdf0 does not
// unpack by its own choice is recorded as skipped instead (openObjStm).
//
// One case stays fatal: an entry whose index names a different object number
// in the container's own index. That is not missing data but two parts of the
// file disagreeing about which object is object N, and any choice between them
// is a guess a crafted file can steer, showing one object to pdf0 and another
// to a reader that trusts the other part. Read refuses the file rather than
// guess (TestReadObjStmXrefIndexMismatch).
func (d *Document) loadCompressedObjects(cancel core.Canceler, table *XRefTable) error {
	// Group requested object numbers by container so each object stream is
	// decoded exactly once.
	byContainer := make(map[int][]int)
	for num, entry := range table.Entries {
		if !entry.Compressed {
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

	// Process containers in object-number order so that, if the
	// materialisation budget is reached, the set of object streams left
	// unmaterialized is deterministic rather than dependent on map iteration.
	containers := make([]int, 0, len(byContainer))
	for containerNum := range byContainer {
		containers = append(containers, containerNum)
	}
	sort.Ints(containers)

	for _, containerNum := range containers {
		// Per container: one iteration decompresses at most one object stream,
		// which the per-stream cap already bounds. This is the other unbounded
		// loop in a read (the uncompressed object load is the first), and the one
		// that can decompress half a gigabyte from a small file.
		if err := cancel.StopErr("reading PDF object streams"); err != nil {
			return err
		}
		objNums := byContainer[containerNum]
		data, index, first, ok, err := d.openObjStm(cancel, containerNum)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		for _, ie := range index {
			d.source.note(ie.Number)
		}
		sort.Ints(objNums) // deterministic: the fatal mismatch, if any, is the same every run
		broken := false
		for _, num := range objNums {
			entry := table.Entries[num]
			idx := entry.IndexInStream
			if idx < 0 || idx >= len(index) {
				broken = true
				continue
			}
			ie := index[idx]
			if ie.Number != num {
				return fmt.Errorf("object %d: object stream %d index %d holds object %d", num, containerNum, idx, ie.Number)
			}
			obj, overBudget, err := d.parseObjStmObject(data, int64(first+ie.Offset))
			if overBudget {
				d.objStmSkipped(containerNum, core.ReasonLimit, limitObjStmTotal, fmt.Sprintf("its objects would take what this read materialises from object streams past the %s-byte budget for one read", core.LimitBound(d.lim().ObjectStreamBytes, core.DefaultMaxObjectStreamBytes)))
				break
			}
			if err != nil {
				broken = true
				continue
			}
			// Objects in an object stream always have generation 0.
			d.Objects[num] = &object.IndirectObject{Number: num, Value: obj}
		}
		d.closeObjStm(data)
		if broken {
			d.objStmBroken(containerNum)
		}
	}
	return nil
}
