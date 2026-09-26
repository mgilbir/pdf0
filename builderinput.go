package pdf0

import (
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/mgilbir/pdf0/object"
)

// Validating what a builder is handed, and adding its objects only once it
// cannot fail.
//
// Every builder in this package follows one order: check every input, then
// change the document. A NaN or an infinity is not a number a PDF can carry,
// and the serializer refuses one — but at Write, long after the call that
// accepted it returned nil and left the value in the document (audit
// 2026-09-22 C131). A check that ran after the first object was added left a
// half-built document behind it instead. The helpers here are the one place
// the numeric checks live; stagedAdds is how a builder whose one fallible
// mutation (embedding a font) comes before others takes it back.

// checkFinite refuses NaN and the infinities, which no PDF number can be.
func checkFinite(what string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("pdf0: %s is %v, which is not a number a PDF can carry", what, v)
	}
	return nil
}

// checkPositive refuses a value that is not a finite number above zero: a page
// size or a width, where zero and less describe nothing.
func checkPositive(what string, v float64) error {
	if err := checkFinite(what, v); err != nil {
		return err
	}
	if v <= 0 {
		return fmt.Errorf("pdf0: %s is %g; it has to be greater than zero", what, v)
	}
	return nil
}

// checkUnit refuses a value outside [0,1], which is what an alpha or a colour
// component is. NaN is outside it.
func checkUnit(what string, v float64) error {
	if !(v >= 0 && v <= 1) {
		return fmt.Errorf("pdf0: %s is %g, outside [0,1]", what, v)
	}
	return nil
}

// checkBox refuses a rectangle with a non-finite corner or no area. why says
// what having no area would mean for this particular box.
func checkBox(what string, box [4]float64, why string) error {
	for i, v := range box {
		if err := checkFinite(fmt.Sprintf("%s's coordinate %d", what, i), v); err != nil {
			return err
		}
	}
	if box[2] <= box[0] || box[3] <= box[1] {
		return fmt.Errorf("pdf0: %s %v has no area; %s", what, box, why)
	}
	return nil
}

// checkMatrix refuses a matrix with a non-finite entry. A nil matrix is the
// identity, and fine.
func checkMatrix(what string, m *[6]float64) error {
	if m == nil {
		return nil
	}
	for i, v := range m {
		if err := checkFinite(fmt.Sprintf("%s's entry %d", what, i), v); err != nil {
			return err
		}
	}
	return nil
}

// sortedNames is a map's keys in order. Anything written from a map goes
// through it: Go's map order is random, and output must not be (C95).
func sortedNames[V any](m map[object.Name]V) []object.Name {
	return slices.Sorted(maps.Keys(m))
}

// numberFor writes a value as an integer when it is one, which keeps the file
// tidy and matches what a reader expects to see for a page size.
//
// Only a value an integer holds exactly is converted: past 2^53 a float64 has
// no fractional part to lose, and converting a value past the int range is not
// defined at all, so those stay reals.
func numberFor(v float64) object.Object {
	const exact = 1 << 53
	if v == math.Trunc(v) && v > -exact && v < exact {
		return object.Integer(int64(v))
	}
	return object.Real(v)
}

// stagedAdds is an Allocator that numbers the objects a builder adds but keeps
// them out of the document until commit: a builder whose fallible step adds
// objects — embedding a face does — calls abort on failure and the document is
// as it was, numbering included. Numbers in reuse are handed out first, for a
// builder that replaces objects it wrote before (see faceEmbedding).
type stagedAdds struct {
	d     *Document
	hint  int
	reuse []int
	objs  []*object.IndirectObject
}

func (d *Document) stageAdds() *stagedAdds {
	return &stagedAdds{d: d, hint: d.nextObjNum}
}

// Add numbers value and holds it for commit.
func (s *stagedAdds) Add(value object.Object) object.IndirectRef {
	var n int
	if len(s.reuse) > 0 {
		n, s.reuse = s.reuse[0], s.reuse[1:]
	} else {
		n = s.d.allocObjNum()
	}
	s.objs = append(s.objs, &object.IndirectObject{Number: n, Value: value})
	return object.IndirectRef{Number: n}
}

// commit puts every staged object into the document.
func (s *stagedAdds) commit() {
	for _, o := range s.objs {
		s.d.Objects[o.Number] = o
	}
	s.objs = nil
}

// abort drops the staged objects and returns the numbers they took, so the
// next object added is numbered as if the builder had never run.
func (s *stagedAdds) abort() {
	s.objs = nil
	s.d.nextObjNum = s.hint
}
