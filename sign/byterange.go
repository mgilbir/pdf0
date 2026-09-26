package sign

import (
	"crypto"
	"encoding"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file is the one place a signature's /ByteRange is judged and the bytes
// it names are hashed. A signed range is attacker-controlled arithmetic over
// the file, so it is validated once, into the only layout a signature can
// legitimately have, and the bytes are then streamed through the digest from
// the file itself — never gathered into a buffer whose size the range decides.
//
// The audit of 2026-09-22 found the range summed unchecked, which overflowed
// into a panic out of the public verifier (C5), and every segment appended to
// one buffer, so 150,000 overlapping segments over a 1.5 MB file exhausted
// memory (C6). Neither can happen here: there are exactly two segments, each
// checked against what remains of the file after its start, and nothing is
// allocated in proportion to them.

// signedRange is a validated signature byte range: the file's bytes
// [0, gapStart) and [gapEnd, end), where the gap [gapStart, gapEnd) is exactly
// the signature's own /Contents hex string.
type signedRange struct {
	gapStart, gapEnd, end int64
}

// Covers reports whether the range reaches the end of a file of size bytes.
func (r signedRange) covers(size int64) bool { return r.end == size }

// maxContentsWhitespace bounds the whitespace a /Contents hex string may carry
// between its digits, as a multiple of its decoded length. Writers put the
// value on one line; allowing some whitespace accepts one that wraps it, and
// the bound keeps the gap check linear in the signature value itself, so a gap
// of megabytes of padding cannot be scanned once per signature.
const maxContentsWhitespace = 1

// readSignedRange validates a signature dictionary's /ByteRange against a
// file of size bytes and the bytes themselves, and returns it. It accepts
// exactly one layout — ISO 32000-2 12.8.1 describes the signature as covering
// the whole file except the signature value, and every other layout leaves
// bytes a verifier would report as signed unsigned:
//
//   - four non-negative integers [0 len1 start2 len2];
//   - both spans inside the file, each length compared against what remains of
//     the file after its start (never a sum against the size, which overflows);
//   - the gap between them, [len1, start2), is exactly the dictionary's
//     /Contents value written as a hexadecimal string <…>.
func readSignedRange(d core.View, sig *object.Dictionary, file core.SignedFile) (signedRange, error) {
	br, ok := core.ReadByteRange(d, sig.Get("ByteRange"))
	if !ok {
		return signedRange{}, errors.New("malformed /ByteRange: it must be an array of four integers")
	}
	size := file.Len()
	if !br.Ordered() {
		return signedRange{}, errors.New("malformed /ByteRange: it must start at offset 0 with non-negative, ascending, non-overlapping spans")
	}
	if !br.Within(size) {
		return signedRange{}, errors.New("/ByteRange extends beyond the end of the file")
	}
	end, _ := br.End() // Within implies it fits
	r := signedRange{gapStart: br.Len1, gapEnd: br.Start2, end: end}
	contents, ok := d.Resolve(sig.Get("Contents")).(object.String)
	if !ok {
		return signedRange{}, errors.New("the signature /Contents is not a string")
	}
	if err := gapIsContents(file.ReaderAt(), r.gapStart, r.gapEnd, contents.Value); err != nil {
		return signedRange{}, err
	}
	return r, nil
}

// gapIsContents checks that the file's bytes [start, end) are exactly a hex
// string <…> whose value is contents: the only bytes a signature may leave
// unsigned are its own value (audit 2026-07-26 C12). The window is bounded by
// the length of contents before anything is read.
func gapIsContents(r io.ReaderAt, start, end int64, contents []byte) error {
	n := end - start
	max := int64(2+2*len(contents)) + int64(maxContentsWhitespace)*int64(2*len(contents))
	if n < 2 || n > max {
		return fmt.Errorf("the /ByteRange gap (%d bytes) is not the signature's /Contents hex string (%d bytes)", n, 2+2*len(contents))
	}
	window := make([]byte, n)
	if _, err := r.ReadAt(window, start); err != nil {
		return fmt.Errorf("reading the /ByteRange gap: %w", err)
	}
	if window[0] != '<' || window[n-1] != '>' {
		return errors.New("the /ByteRange gap is not a hexadecimal string")
	}
	got := 0
	hi, half := byte(0), false
	for _, b := range window[1 : n-1] {
		var v byte
		switch {
		case b >= '0' && b <= '9':
			v = b - '0'
		case b >= 'a' && b <= 'f':
			v = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			v = b - 'A' + 10
		case b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0:
			continue
		default:
			return errors.New("the /ByteRange gap is not a hexadecimal string")
		}
		if !half {
			hi, half = v, true
			continue
		}
		half = false
		if got >= len(contents) || contents[got] != hi<<4|v {
			return errors.New("the /ByteRange gap does not hold the signature's /Contents value")
		}
		got++
	}
	if half || got != len(contents) {
		return errors.New("the /ByteRange gap does not hold the signature's /Contents value")
	}
	return nil
}

// rangeHasher computes the digests of signed ranges over one file.
//
// Every signature in a file covers a prefix of it minus its own gap, and the
// prefixes are nested: the signature of a later revision covers everything the
// earlier one does. Hashing each range from offset 0 costs the whole file per
// signature — quadratic in a file with many revisions. Instead the hasher keeps,
// per algorithm, one running state over the file's leading bytes: a digest over
// [0, gapStart) ∪ [gapEnd, end) continues that state up to gapStart, takes a
// copy (the hash's own binary marshalling), and finishes the copy over the tail
// [gapEnd, end). Asked in ascending order of gapStart, as the verifier asks,
// the prefix work over all signatures is one pass over the file; the tails are
// each within one revision.
type rangeHasher struct {
	r     io.ReaderAt
	state map[crypto.Hash]*prefixState
	buf   []byte
}

type prefixState struct {
	h   hash.Hash
	pos int64
}

func newRangeHasher(r io.ReaderAt) *rangeHasher {
	return &rangeHasher{r: r, state: map[crypto.Hash]*prefixState{}, buf: make([]byte, 64<<10)}
}

// digest returns h's digest of the bytes rg covers.
func (x *rangeHasher) digest(h crypto.Hash, rg signedRange) ([]byte, error) {
	if !h.Available() {
		return nil, fmt.Errorf("hash %v is not available", h)
	}
	st := x.state[h]
	if st == nil || st.pos > rg.gapStart {
		st = &prefixState{h: h.New()}
		x.state[h] = st
	}
	if err := x.feed(st.h, st.pos, rg.gapStart); err != nil {
		return nil, err
	}
	st.pos = rg.gapStart
	m, ok := st.h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, fmt.Errorf("hash %v cannot be resumed", h)
	}
	snap, err := m.MarshalBinary()
	if err != nil {
		return nil, err
	}
	tail := h.New()
	if err := tail.(encoding.BinaryUnmarshaler).UnmarshalBinary(snap); err != nil {
		return nil, err
	}
	if err := x.feed(tail, rg.gapEnd, rg.end); err != nil {
		return nil, err
	}
	return tail.Sum(nil), nil
}

// feed writes the file's bytes [from, to) into h through a fixed buffer.
func (x *rangeHasher) feed(h hash.Hash, from, to int64) error {
	for from < to {
		n := int64(len(x.buf))
		if to-from < n {
			n = to - from
		}
		got, err := x.r.ReadAt(x.buf[:n], from)
		if int64(got) != n {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("reading the signed bytes: %w", err)
		}
		h.Write(x.buf[:n])
		from += n
	}
	return nil
}
