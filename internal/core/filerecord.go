package core

// FileRecord is what the byte-level rules read of the file a document was
// read from: the bytes, and the facts Read established about them while it
// parsed. The root package builds it from the document's source record
// (Document.Source); it is nil in a View of a document built in memory, which
// has no file.
//
// Everything here describes the file as read, never the object graph as it
// stands: a caller may have edited Document.Objects since, and a byte-level
// rule that took a value from the graph and a position from the file would
// judge a file that does not exist (audit 2026-09-22 C69). It is immutable
// and shared.
type FileRecord struct {
	// Data is the file's bytes, exactly as Read received them.
	Data []byte
	// Objects lists every object Read parsed at the top level of the file —
	// not the cross-reference streams and object streams, which are file
	// structure — in file order, one entry per distinct offset (the lowest
	// object number when several cross-reference entries name the same
	// bytes).
	Objects []FileObject
	// XRefTables are the offsets of the xref keyword of every traditional
	// cross-reference table Read parsed through the /Prev chain, hybrid
	// sections included, in file order. Cross-reference streams are objects.
	XRefTables []int64
	// XRefStreams reports that Read parsed a cross-reference stream: a
	// stream section, or a hybrid section's /XRefStm.
	XRefStreams bool
	// Trailers are the trailers of the cross-reference sections Read parsed
	// (a table's trailer dictionary, or a cross-reference stream's), in file
	// order.
	Trailers []FileTrailer
	// Linearized reports that the first object in the file is a
	// linearization parameter dictionary (ISO 32000-2 Annex F.2).
	Linearized bool
	// Signatures are the byte ranges of the file's signature dictionaries —
	// every dictionary with /ByteRange and /Contents whose /Type, if any, is
	// /Sig or /DocTimeStamp — by object number, ascending. OK is false when
	// /ByteRange is not an array of four integers.
	Signatures []FileSignature
}

// FileObject is one object as Read found it in the file.
type FileObject struct {
	Num int
	// Offset is where the cross-reference data says the object begins;
	// End is just past the endobj keyword Read parsed. [Offset, End) is the
	// object's region.
	Offset, End int64
	// Stream reports that the object is a stream. StreamKeyword is then the
	// offset of the stream keyword the parser took, and Length the /Length
	// the file declares, resolved as Read resolved it; LengthOK is false when
	// that is not an integer.
	Stream        bool
	StreamKeyword int64
	Length        int64
	LengthOK      bool
}

// FileTrailer is one cross-reference section's trailer.
type FileTrailer struct {
	// Offset is the section's: its xref keyword, or its stream's object.
	Offset int64
	// ID0 is the first element of the trailer's /ID, when HasID.
	ID0   []byte
	HasID bool
}

// FileSignature is one signature dictionary's /ByteRange.
type FileSignature struct {
	Num       int
	ByteRange ByteRange
	OK        bool
}

// FileRecord returns the record of the file the document was read from, or
// nil for a document built in memory (see View.File).
func (v View) FileRecord() *FileRecord {
	if v.File == nil {
		return nil
	}
	return v.File()
}

// Region returns the object's bytes, [Offset, End), clamped to the file.
func (f *FileRecord) Region(o FileObject) []byte {
	lo, hi := clampRegion(o.Offset, o.End, int64(len(f.Data)))
	return f.Data[lo:hi]
}

// clampRegion clamps [lo, hi) to [0, n), with lo <= hi.
func clampRegion(lo, hi, n int64) (int64, int64) {
	lo = max(0, min(lo, n))
	hi = max(lo, min(hi, n))
	return lo, hi
}
