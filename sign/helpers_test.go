package sign

import (
	"bytes"
	"errors"
	"io"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkV completes a partially built view the way Document.view does: a non-nil
// object map, a trailer to resolve against, the real limits and a shared run.
// A zero core.Limits is a budget of zero, so a view built without them decodes
// nothing while reporting no error.
func mkV(v core.View) core.View {
	if v.Objects == nil {
		v.Objects = map[int]*object.IndirectObject{}
	}
	if v.Trailer == nil {
		v.Trailer = &object.Dictionary{}
	}
	if v.Limits == (core.Limits{}) {
		v.Limits = core.DefaultLimits()
	}
	if v.Run == nil {
		v.Run = core.NewRun(&core.Recorder{})
	}
	return v
}

// bytesFile is a core.SignedFile over bytes in memory with no known revisions:
// only a signature covering the whole file can be matched to it, which is what
// the package's unit tests build. Revision analysis is exercised through the
// root package, which has a real source record.
type bytesFile []byte

func (b bytesFile) Len() int64            { return int64(len(b)) }
func (b bytesFile) ReaderAt() io.ReaderAt { return bytes.NewReader(b) }
func (b bytesFile) RevisionEnds() []int64 { return nil }
func (b bytesFile) Diff(int) (*core.RevisionDiff, error) {
	return nil, errors.New("bytesFile has no revisions")
}
