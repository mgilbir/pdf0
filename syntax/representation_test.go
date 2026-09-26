package syntax

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestSerializerRefusesScalarPointers (C99): a pointer to a scalar PDF value
// satisfies Object by Go's method-set rules but is not the representation of
// anything. The serializer says so instead of reporting an unknown type or,
// worse, guessing.
func TestSerializerRefusesScalarPointers(t *testing.T) {
	n := object.Name("X")
	i := object.Integer(1)
	r := object.Real(1.5)
	b := object.Boolean(true)
	str := object.String{Value: []byte("s")}
	arr := object.Array{object.Integer(1)}
	null := object.Null{}
	ref := object.IndirectRef{Number: 1}
	for _, o := range []object.Object{&n, &i, &r, &b, &str, &arr, &null, &ref} {
		var buf bytes.Buffer
		err := NewSerializer(&buf).WriteObject(object.Array{o})
		if err == nil || !strings.Contains(err.Error(), "pointer to a PDF value") {
			t.Errorf("%T: err = %v, want the pointer-form error", o, err)
		}
	}
}
