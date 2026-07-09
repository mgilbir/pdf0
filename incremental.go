package pdf0

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
)

// WriteIncremental writes an incremental update: the original file bytes verbatim
// followed by only the objects listed in changed, a new cross-reference section
// whose /Prev chains back to the original, and a new trailer. The original bytes
// are preserved exactly, so any signature over them stays valid and the update
// can be undone by truncation.
//
// changed lists the object numbers whose current value in d.Objects should be
// (re)written; numbers absent from d.Objects are recorded as free (deleted).
// Encrypted documents are not supported.
func (d *Document) WriteIncremental(w io.Writer, original []byte, changed []int) error {
	if d.security != nil || d.Encrypted {
		return errors.New("incremental update of an encrypted document is not supported")
	}
	if len(changed) == 0 {
		return errors.New("incremental update with no changed objects")
	}
	prevXref, err := findStartXref(original)
	if err != nil {
		return fmt.Errorf("incremental: locating the original startxref: %w", err)
	}

	if _, err := w.Write(original); err != nil {
		return err
	}
	base := int64(len(original))

	var buf bytes.Buffer
	if base > 0 && original[base-1] != '\n' {
		buf.WriteByte('\n')
	}
	s := NewSerializer(&buf)

	nums := append([]int(nil), changed...)
	sort.Ints(nums)
	offsets := make(map[int]int64, len(nums))
	free := map[int]bool{}
	for _, num := range nums {
		iobj := d.Objects[num]
		if iobj == nil {
			free[num] = true
			continue
		}
		offsets[num] = base + s.Offset()
		if err := s.WriteIndirectObject(iobj); err != nil {
			return fmt.Errorf("incremental: writing object %d: %w", num, err)
		}
	}

	xrefOffset := base + s.Offset()
	if err := writeIncrementalXRef(s, nums, offsets, free, d.Objects); err != nil {
		return err
	}

	maxObj := 0
	for num := range d.Objects {
		if num > maxObj {
			maxObj = num
		}
	}
	trailer := d.Trailer.Clone()
	trailer.Set("Size", Integer(maxObj+1))
	trailer.Set("Prev", Integer(prevXref))
	trailer.Delete("XRefStm") // this update is a traditional section
	if err := s.writeString("trailer\n"); err != nil {
		return err
	}
	if err := s.writeDictionary(trailer); err != nil {
		return err
	}
	if err := s.writeString(fmt.Sprintf("\nstartxref\n%d\n%%%%EOF\n", xrefOffset)); err != nil {
		return err
	}

	_, err = w.Write(buf.Bytes())
	return err
}

// writeIncrementalXRef writes a traditional xref section covering only the given
// object numbers, in contiguous subsections.
func writeIncrementalXRef(s *Serializer, nums []int, offsets map[int]int64, free map[int]bool, objects map[int]*IndirectObject) error {
	if err := s.writeString("xref\n"); err != nil {
		return err
	}
	entry := func(num int) string {
		if free[num] {
			return "0000000000 00001 f\r\n"
		}
		gen := 0
		if o, ok := objects[num]; ok {
			gen = o.Generation
		}
		return fmt.Sprintf("%010d %05d n\r\n", offsets[num], gen)
	}
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if err := s.writeString(fmt.Sprintf("%d %d\n", nums[i], j-i+1)); err != nil {
			return err
		}
		for k := i; k <= j; k++ {
			if err := s.writeString(entry(nums[k])); err != nil {
				return err
			}
		}
		i = j + 1
	}
	return nil
}
