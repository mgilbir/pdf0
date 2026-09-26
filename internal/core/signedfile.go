package core

import (
	"io"

	"github.com/mgilbir/pdf0/object"
)

// SignedFile is the file a signature is verified against, as the signature
// verifier (package sign) needs it: the bytes, the revisions the file's
// incremental updates make (ISO 32000-2 7.5.6), and what changed between one
// revision and the newest.
//
// A signature covers bytes, not objects, so verification reads the file the
// document was read from — its source record — never a byte slice a caller
// passes alongside a Document, which may be a different file. The root
// package implements it over Document.Source.
type SignedFile interface {
	// Len is the size of the file in bytes.
	Len() int64
	// ReaderAt reads the file's bytes.
	ReaderAt() io.ReaderAt
	// RevisionEnds returns, oldest first, the offset one past the %%EOF marker
	// (and its end-of-line) that closes each revision. It is empty when the
	// file's revisions are unknown: a Document with no source, or one whose
	// cross-reference data Read had to rebuild by scanning.
	RevisionEnds() []int64
	// Diff returns the objects that differ between the file as it stood at the
	// end of revision rev — the state a reader of the first RevisionEnds()[rev]
	// bytes sees — and the file as it stands now. An error means the
	// difference could not be established, which a caller must treat as "the
	// changes are unknown", never as "nothing changed".
	Diff(rev int) (*RevisionDiff, error)
}

// RevisionDiff is what changed between two states of a file.
type RevisionDiff struct {
	// Changes lists every object number whose value differs between the two
	// states, in ascending order. An object present in only one state has a
	// nil value in the other.
	Changes []ObjectChange
	// Old and New read objects in the earlier and the newest state.
	Old, New ObjectReader
}

// ObjectChange is one object that differs between two states of a file. The
// values are as the file stores them: strings and streams of an encrypted
// file are not decrypted.
type ObjectChange struct {
	Number   int
	Old, New object.Object
}

// ObjectReader reads the objects of one state of a file.
type ObjectReader interface {
	// Object returns the value of object num in this state, or nil when the
	// state does not define it. An error means the object is defined but
	// could not be read.
	Object(num int) (object.Object, error)
	// Trailer returns the trailer dictionary of this state's newest
	// cross-reference section.
	Trailer() *object.Dictionary
}
