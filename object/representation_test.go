package object

import (
	"reflect"
	"testing"
)

// TestOneRepresentation is the C99 guard (audit 2026-09-22): each type has one
// form that is an Object. The composite types are Objects only as pointers, so
// a Dictionary, Stream or IndirectObject value can no longer reach a consumer
// that type-switches on the pointer and miss it; the compiler refuses it. The
// scalar types are Objects as values; their pointers satisfy the interface by
// Go's method-set rules, which nothing can prevent, and Equal treats them as
// equal to nothing (the serializer refuses them; see the syntax package).
func TestOneRepresentation(t *testing.T) {
	objType := reflect.TypeFor[Object]()
	for _, c := range []struct {
		name      string
		value     reflect.Type
		wantValue bool // does the value (non-pointer) form implement Object?
	}{
		{"Dictionary", reflect.TypeFor[Dictionary](), false},
		{"Stream", reflect.TypeFor[Stream](), false},
		{"IndirectObject", reflect.TypeFor[IndirectObject](), false},
		{"Boolean", reflect.TypeFor[Boolean](), true},
		{"Integer", reflect.TypeFor[Integer](), true},
		{"Real", reflect.TypeFor[Real](), true},
		{"String", reflect.TypeFor[String](), true},
		{"Name", reflect.TypeFor[Name](), true},
		{"Array", reflect.TypeFor[Array](), true},
		{"Null", reflect.TypeFor[Null](), true},
		{"IndirectRef", reflect.TypeFor[IndirectRef](), true},
	} {
		if got := c.value.Implements(objType); got != c.wantValue {
			t.Errorf("%s value implements Object = %v, want %v", c.name, got, c.wantValue)
		}
		if !reflect.PointerTo(c.value).Implements(objType) {
			t.Errorf("*%s does not implement Object", c.name)
		}
	}

	// Pointers to scalar values are not PDF objects: equal to nothing, not even
	// to the same pointer or to the value they point at.
	n := Name("X")
	i := Integer(1)
	arr := Array{Integer(1)}
	for _, c := range []struct {
		name string
		p, v Object
	}{
		{"*Name", &n, n},
		{"*Integer", &i, i},
		{"*Array", &arr, arr},
	} {
		if Equal(c.p, c.p) {
			t.Errorf("Equal(%s, same %s) = true, want false", c.name, c.name)
		}
		if Equal(c.p, c.v) || Equal(c.v, c.p) {
			t.Errorf("Equal between %s and its value = true, want false", c.name)
		}
	}
}
