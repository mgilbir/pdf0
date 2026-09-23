// Package content builds PDF content streams: the operator sequences that draw
// a page (ISO 32000-2 8.2, "Graphics objects").
//
// It is the write side of what the rest of this module reads. pdf0 could parse,
// validate and repair a document long before it could put a mark on a page —
// the only content streams in the repository were hand-typed string literals in
// examples/ — and this is what closes that gap. It is useful on its own, and it
// is what a layout engine would eventually draw through.
//
// # What it guarantees
//
// A Builder emits only operators ISO 32000 defines (Annex A, Table A.1), which
// is also the set PDF/A permits, so a stream it produces cannot fail the
// operator rule. Beyond that it enforces the structural invariants a content
// stream has, and which are easy to break by hand:
//
//   - q and Q are balanced, and nesting stays inside the 28 levels PDF/A allows.
//   - Text operators appear only between BT and ET, and text objects do not
//     nest.
//   - Marked-content sequences (BMC and BDC, each closed by EMC) are balanced,
//     and nest with text objects: a sequence begun inside BT ends before ET,
//     and one begun outside does not end inside one.
//   - A path is painted or explicitly discarded before anything else is drawn.
//     Between m or re and the painting operator only path construction and
//     clipping operators may appear, and after W or W* only the painting
//     operator (ISO 32000-2 8.2, Figure 9). A colour, graphics-state,
//     marked-content, sh or Do operator inside a path is refused.
//   - Every number written is finite. A NaN reaching a content stream is not a
//     rendering bug, it is a malformed file, and the layout of a hostile input
//     is exactly where one would come from.
//
// None of these can be recovered from mid-stream, so the Builder records the
// first violation and reports it from Bytes. That keeps the drawing calls free
// of error returns — a page is hundreds of them — without letting a broken
// stream escape.
//
// # What it does not do
//
// It does not manage resources. An operator that names one (Tf, Do, gs, cs)
// takes the name the caller chose, and the Builder only records which names
// were used, through Resources, so the caller can build the page's /Resources
// dictionary and be told if it forgets one. Choosing names, embedding fonts and
// subsetting them are separate concerns, and separate work.
package content

import (
	"fmt"
	"math"
	"strconv"

	"github.com/mgilbir/pdf0/object"
)

// MaxNestingDepth is the deepest q/Q nesting a Builder will emit. It is the
// PDF/A implementation limit (ISO 19005-2 6.1.13), which pdf0's own validator
// enforces on read; exceeding it here would produce a file this module rejects.
const MaxNestingDepth = 28

// Builder assembles a content stream. The zero value is ready to use, and every
// drawing method returns the Builder so calls can be chained.
//
// It is not safe for concurrent use: a content stream is an ordered sequence,
// and two goroutines appending to one have no meaning.
type Builder struct {
	buf []byte
	err error

	depth  int  // current q/Q nesting
	inText bool // between BT and ET
	inPath bool // a path is under construction
	// clipped records a W or W* awaiting the painting operator that applies
	// it: Figure 9 admits nothing else there, not even path construction.
	clipped bool

	// marked is the current marked-content nesting (BMC/BDC not yet closed by
	// EMC), and textFloor what it was when the open text object began: the
	// sequences opened inside BT are the ones above the floor, and they have
	// to close before ET.
	marked    int
	textFloor int

	// setsColor records that the stream chose a colour or a colour space. It is
	// not a question about the drawing but about where the drawing may be used:
	// an uncoloured tiling pattern takes its colour from the place it is
	// painted, and its cell is only defined if it sets none.
	setsColor bool

	res Resources
}

// Resources records the names a content stream referred to, grouped by the
// dictionary in the page's /Resources that has to define them (ISO 32000-2
// 7.8.3, Table 33). A name used here and absent there is a broken page, so the
// caller building /Resources can check its work against this.
type Resources struct {
	Fonts       []object.Name // /Font — named by Tf
	XObjects    []object.Name // /XObject — named by Do
	ExtGStates  []object.Name // /ExtGState — named by gs
	ColorSpaces []object.Name // /ColorSpace — named by cs and CS
	Shadings    []object.Name // /Shading — named by sh
	Patterns    []object.Name // /Pattern — named by scn and SCN
	Properties  []object.Name // /Properties — named by BDC and DP
}

// Resources returns the names this stream used. The slices are in first-use
// order and carry no duplicates.
func (b *Builder) Resources() Resources { return b.res }

// SetsColor reports whether the stream chose a colour or a colour space.
//
// It exists for one caller: an uncoloured tiling pattern (PaintType 2) takes
// its colour from wherever it is painted, and ISO 32000-2 8.7.3.1 leaves the
// result undefined if its cell sets one. Undefined means each reader picks, so
// the file looks different in different viewers — which is exactly the kind of
// fault that is never traced back to the pattern.
func (b *Builder) SetsColor() bool { return b.setsColor }

// Bytes returns the finished content stream, or the first error that made it
// invalid.
//
// It is an error to finish with unbalanced q/Q, inside a text object, inside a
// marked-content sequence, or with an unpainted path: each leaves a stream
// whose meaning depends on what a consumer does with the leftover state, and
// none of them is recoverable by the caller after the fact. An unclosed
// sequence is not harmless either: it runs on into whatever the reader
// appends, and a tagged page's structure then claims content it never marked.
func (b *Builder) Bytes() ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	switch {
	case b.depth != 0:
		return nil, fmt.Errorf("content: %d unbalanced q (Save without Restore)", b.depth)
	case b.inText:
		return nil, fmt.Errorf("content: stream ends inside a text object (BeginText without EndText)")
	case b.inPath:
		return nil, fmt.Errorf("content: stream ends with an unpainted path")
	case b.marked != 0:
		return nil, fmt.Errorf("content: %d unclosed marked-content sequence(s) (BeginMarked or BeginTagged without EndMarked)", b.marked)
	}
	return b.buf, nil
}

// Err reports the first error, if any, without the end-of-stream checks Bytes
// makes. It is for a caller that wants to abandon a drawing early.
func (b *Builder) Err() error { return b.err }

// fail records the first error. Later calls become no-ops, so a caller may keep
// drawing and check once at the end.
func (b *Builder) fail(format string, args ...any) *Builder {
	if b.err == nil {
		b.err = fmt.Errorf("content: "+format, args...)
	}
	return b
}

// op writes an operator and its operands, in PDF's operands-then-operator
// order.
func (b *Builder) op(name string, operands ...any) *Builder {
	if b.err != nil {
		return b
	}
	// The path object of Figure 9, checked here for every operator rather than
	// in each method, so that a method added later cannot forget it. The
	// painting operators clear inPath before they get here.
	if b.inPath {
		if b.clipped && !paintingOps[name] {
			return b.fail("%s after a clip (W or W*): only the painting operator that applies the clip may follow", name)
		}
		if !pathOps[name] {
			return b.fail("%s with a path under construction: between m or re and the painting operator only "+
				"path construction and clipping operators are permitted (ISO 32000-2 8.2, Figure 9)", name)
		}
	}
	for _, o := range operands {
		switch v := o.(type) {
		case float64:
			if !b.num(v) {
				return b
			}
		case int:
			b.buf = strconv.AppendInt(b.buf, int64(v), 10)
		case object.Name:
			if !b.name(v) {
				return b
			}
		case []byte: // an already-encoded operand (a string, an array)
			b.buf = append(b.buf, v...)
		default:
			return b.fail("internal: operand type %T for operator %q", o, name)
		}
		b.buf = append(b.buf, ' ')
	}
	b.buf = append(b.buf, name...)
	b.buf = append(b.buf, '\n')
	switch name {
	// Every operator that sets a colour or a colour space, recorded in one place
	// rather than in each setter, so that a setter added later cannot forget to.
	// An uncoloured tiling pattern is defined only when its cell sets none.
	case "g", "G", "rg", "RG", "k", "K", "cs", "CS", "sc", "SC", "scn", "SCN":
		b.setsColor = true
	}
	return b
}

// pathOps are the operators Figure 9 admits inside a path object: path
// construction, clipping, and the painting operators that end it.
var pathOps = map[string]bool{
	"m": true, "l": true, "c": true, "v": true, "y": true, "h": true, "re": true,
	"W": true, "W*": true,
	"f": true, "F": true, "f*": true, "S": true, "s": true, "B": true, "B*": true,
	"b": true, "b*": true, "n": true,
}

// paintingOps are the operators that end a path object.
var paintingOps = map[string]bool{
	"f": true, "F": true, "f*": true, "S": true, "s": true, "B": true, "B*": true,
	"b": true, "b*": true, "n": true,
}

// num appends a PDF number, refusing the non-finite values that would make the
// stream unparseable. It matches the serializer's formatting so a number means
// the same thing however pdf0 wrote it.
func (b *Builder) num(f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		b.fail("cannot write non-finite number %v", f)
		return false
	}
	// Reject values a PDF number cannot carry (ISO 32000-2 7.3.3 and Annex C):
	// beyond this the reader's own bound is what a file would hit.
	if f > 1e10 || f < -1e10 {
		b.fail("number %v is outside the range a PDF real can represent", f)
		return false
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	b.buf = append(b.buf, s...)
	return true
}

// name appends a name object, escaping the bytes that would end it early. A
// name is written by the caller and may carry anything; an unescaped delimiter
// would silently change which resource the operator refers to.
func (b *Builder) name(n object.Name) bool {
	if len(n) == 0 {
		b.fail("cannot write an empty name")
		return false
	}
	b.buf = append(b.buf, '/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < '!' || c > '~' || c == '#' || isDelimiter(c) {
			b.buf = append(b.buf, '#')
			const hex = "0123456789ABCDEF"
			b.buf = append(b.buf, hex[c>>4], hex[c&0xF])
			continue
		}
		b.buf = append(b.buf, c)
	}
	return true
}

func isDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// record adds a resource name to a group, keeping first-use order and no
// duplicates.
func record(group *[]object.Name, n object.Name) {
	for _, existing := range *group {
		if existing == n {
			return
		}
	}
	*group = append(*group, n)
}
