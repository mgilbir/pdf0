package pdf0

import (
	"fmt"
	"sort"
	"strconv"
)

// maxObjStmDecompressedTotal bounds the aggregate decompressed size of all
// object streams materialized during a single Read. Object streams are the
// compression-amplification vector: a small file can carry many object streams
// that decompress to hundreds of megabytes of small objects (e.g. arrays of
// hundreds of millions of elements), and parsing them all builds a pointer-heavy
// object graph that makes garbage collection dominate — turning a 20 MB file
// into ~30 s and multiple gigabytes of memory. Content streams are already
// bounded per stream (maxContentStreamSize); this bounds object streams in
// aggregate. The limit is generous: no realistic document has half a gigabyte
// of decompressed object-stream data (that would be a multi-gigabyte file), so
// only pathological amplification is affected. Object streams beyond the budget
// are recorded as broken, exactly like an undecodable one — their objects are
// simply unavailable and validation can report the defect.
//
// It is a var, not a const, only so tests can lower it to exercise the budget
// without constructing a half-gigabyte fixture.
var maxObjStmDecompressedTotal int64 = 512 << 20 // 512 MB

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
func parseObjStmIndex(stream *Stream) (data []byte, entries []objStmEntry, first int, err error) {
	if t, ok := stream.Dict.Get("Type").(Name); ok && t != "ObjStm" {
		return nil, nil, 0, fmt.Errorf("not an object stream: /Type /%s", t)
	}
	n, ok := stream.Dict.Get("N").(Integer)
	if !ok || n < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /N missing or invalid")
	}
	firstInt, ok := stream.Dict.Get("First").(Integer)
	if !ok || firstInt < 0 {
		return nil, nil, 0, fmt.Errorf("object stream /First missing or invalid")
	}

	data, err = decodeStreamData(stream)
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
func nextIntToken(l *Lexer) (int, error) {
	tok, err := l.NextToken()
	if err != nil {
		return 0, err
	}
	if tok.Type != TokenInteger {
		return 0, fmt.Errorf("expected integer, got %v", tok.Type)
	}
	v, err := strconv.Atoi(string(tok.Value))
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q: %w", tok.Value, err)
	}
	return v, nil
}

// loadCompressedObjects materializes objects stored in object streams
// (type-2 xref entries) into doc.Objects. Container streams must already be
// loaded; each container is decoded and indexed once regardless of how many
// of its objects are referenced.
func (d *Document) loadCompressedObjects(table *XRefTable) error {
	// Group requested object numbers by container so each object stream is
	// decoded exactly once.
	byContainer := make(map[int][]int)
	for num, entry := range table.Entries {
		if entry.Free || !entry.Compressed {
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

	var decompressed int64
	for _, containerNum := range containers {
		objNums := byContainer[containerNum]
		container, ok := d.Objects[containerNum]
		if !ok {
			return fmt.Errorf("object stream %d referenced by xref but not present", containerNum)
		}
		stream, ok := container.Value.(*Stream)
		if !ok {
			return fmt.Errorf("object stream %d is not a stream", containerNum)
		}
		// Once the aggregate decompressed object-stream budget is exhausted,
		// stop materializing further streams (recorded as broken, like an
		// undecodable one) to bound the parser's work and memory.
		if decompressed >= maxObjStmDecompressedTotal {
			d.brokenObjStms = append(d.brokenObjStms, containerNum)
			continue
		}
		// A corrupt object stream (e.g. undecodable data) makes only its own
		// objects unavailable; recording it lets validation report the defect
		// while the rest of the document is still parsed rather than aborting
		// the whole read.
		data, index, first, err := parseObjStmIndex(stream)
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
			d.Objects[num] = &IndirectObject{Number: num, Value: obj}
		}
	}
	return nil
}
