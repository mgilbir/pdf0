package object

import "testing"

// TestNewStream: NewStream takes the dictionary over (it shares the entries,
// like any Dictionary copy), and a nil dictionary gives an empty, usable Dict.
func TestNewStream(t *testing.T) {
	d := NewDictionary(Entry{Key: "Filter", Value: Name("FlateDecode")})
	st := NewStream(d, []byte("x"))
	if st.Dict.Get("Filter") != Name("FlateDecode") || string(st.Data) != "x" {
		t.Fatalf("NewStream lost its inputs: %v %q", st.Dict.Get("Filter"), st.Data)
	}
	st.Dict.Set("Length", Integer(1))
	if d.Get("Length") != Integer(1) {
		t.Fatal("the stream's Dict does not share the dictionary it took over")
	}
	e := NewStream(nil, nil)
	if e.Dict.Len() != 0 {
		t.Fatal("NewStream(nil) has entries")
	}
	e.Dict.Set("A", Integer(1))
	if e.Dict.Get("A") != Integer(1) {
		t.Fatal("Set on NewStream(nil).Dict did not store")
	}
}
