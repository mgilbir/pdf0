package pdf0

import (
	"testing"
)

func TestParseBoolean(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"false", false},
	}
	for _, tt := range tests {
		p := NewParser([]byte(tt.input))
		obj, err := p.ParseObject()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		b, ok := obj.(Boolean)
		if !ok {
			t.Errorf("input %q: expected Boolean, got %T", tt.input, obj)
			continue
		}
		if bool(b) != tt.want {
			t.Errorf("input %q: expected %v, got %v", tt.input, tt.want, b)
		}
	}
}

func TestParseInteger(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"123", 123},
		{"-98", -98},
		{"+17", 17},
		{"0", 0},
	}
	for _, tt := range tests {
		p := NewParser([]byte(tt.input))
		obj, err := p.ParseObject()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		i, ok := obj.(Integer)
		if !ok {
			t.Errorf("input %q: expected Integer, got %T", tt.input, obj)
			continue
		}
		if int64(i) != tt.want {
			t.Errorf("input %q: expected %d, got %d", tt.input, tt.want, i)
		}
	}
}

func TestParseReal(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"3.14", 3.14},
		{"-.002", -0.002},
		{"+.5", 0.5},
		{"0.0", 0.0},
		{"34.", 34.0},
	}
	for _, tt := range tests {
		p := NewParser([]byte(tt.input))
		obj, err := p.ParseObject()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		r, ok := obj.(Real)
		if !ok {
			t.Errorf("input %q: expected Real, got %T", tt.input, obj)
			continue
		}
		if float64(r) != tt.want {
			t.Errorf("input %q: expected %f, got %f", tt.input, tt.want, r)
		}
	}
}

func TestParseLiteralString(t *testing.T) {
	p := NewParser([]byte("(Hello World)"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := obj.(String)
	if !ok {
		t.Fatalf("expected String, got %T", obj)
	}
	if string(s.Value) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", s.Value)
	}
	if s.IsHex {
		t.Error("expected IsHex=false")
	}
}

func TestParseHexString(t *testing.T) {
	p := NewParser([]byte("<48656C6C6F>"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := obj.(String)
	if !ok {
		t.Fatalf("expected String, got %T", obj)
	}
	if string(s.Value) != "Hello" {
		t.Errorf("expected 'Hello', got %q", s.Value)
	}
	if !s.IsHex {
		t.Error("expected IsHex=true")
	}
}

func TestParseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Type", "Type"},
		{"/Adobe#20Green", "Adobe Green"},
	}
	for _, tt := range tests {
		p := NewParser([]byte(tt.input))
		obj, err := p.ParseObject()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		n, ok := obj.(Name)
		if !ok {
			t.Errorf("input %q: expected Name, got %T", tt.input, obj)
			continue
		}
		if string(n) != tt.want {
			t.Errorf("input %q: expected %q, got %q", tt.input, tt.want, n)
		}
	}
}

func TestParseNull(t *testing.T) {
	p := NewParser([]byte("null"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := obj.(Null); !ok {
		t.Errorf("expected Null, got %T", obj)
	}
}

func TestParseArray(t *testing.T) {
	p := NewParser([]byte("[1 2.0 (hello) /Name true null]"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := obj.(Array)
	if !ok {
		t.Fatalf("expected Array, got %T", obj)
	}
	if len(arr) != 6 {
		t.Fatalf("expected 6 elements, got %d", len(arr))
	}

	// Check types
	if _, ok := arr[0].(Integer); !ok {
		t.Errorf("arr[0]: expected Integer, got %T", arr[0])
	}
	if _, ok := arr[1].(Real); !ok {
		t.Errorf("arr[1]: expected Real, got %T", arr[1])
	}
	if _, ok := arr[2].(String); !ok {
		t.Errorf("arr[2]: expected String, got %T", arr[2])
	}
	if _, ok := arr[3].(Name); !ok {
		t.Errorf("arr[3]: expected Name, got %T", arr[3])
	}
	if _, ok := arr[4].(Boolean); !ok {
		t.Errorf("arr[4]: expected Boolean, got %T", arr[4])
	}
	if _, ok := arr[5].(Null); !ok {
		t.Errorf("arr[5]: expected Null, got %T", arr[5])
	}
}

func TestParseNestedArray(t *testing.T) {
	p := NewParser([]byte("[[1 2] [3 4]]"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := obj.(Array)
	if !ok {
		t.Fatalf("expected Array, got %T", obj)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	inner1, ok := arr[0].(Array)
	if !ok {
		t.Fatalf("arr[0]: expected Array, got %T", arr[0])
	}
	if len(inner1) != 2 {
		t.Errorf("inner1: expected 2 elements, got %d", len(inner1))
	}
}

func TestParseDictionary(t *testing.T) {
	input := "<< /Type /Catalog /Pages 3 0 R /Count 5 >>"
	p := NewParser([]byte(input))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := obj.(*Dictionary)
	if !ok {
		t.Fatalf("expected *Dictionary, got %T", obj)
	}
	if dict.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", dict.Len())
	}

	// Check /Type
	typeVal := dict.Get("Type")
	if typeVal == nil {
		t.Fatal("missing /Type")
	}
	if n, ok := typeVal.(Name); !ok || string(n) != "Catalog" {
		t.Errorf("/Type: expected Name 'Catalog', got %v", typeVal)
	}

	// Check /Pages is an indirect ref
	pagesVal := dict.Get("Pages")
	if pagesVal == nil {
		t.Fatal("missing /Pages")
	}
	ref, ok := pagesVal.(IndirectRef)
	if !ok {
		t.Fatalf("/Pages: expected IndirectRef, got %T", pagesVal)
	}
	if ref.Number != 3 || ref.Generation != 0 {
		t.Errorf("/Pages: expected 3 0 R, got %d %d R", ref.Number, ref.Generation)
	}

	// Check /Count
	countVal := dict.Get("Count")
	if countVal == nil {
		t.Fatal("missing /Count")
	}
	if i, ok := countVal.(Integer); !ok || int64(i) != 5 {
		t.Errorf("/Count: expected Integer 5, got %v", countVal)
	}

	// Key order preserved
	if dict.Keys[0] != "Type" || dict.Keys[1] != "Pages" || dict.Keys[2] != "Count" {
		t.Errorf("key order not preserved: %v", dict.Keys)
	}
}

func TestParseNestedDictionary(t *testing.T) {
	input := "<< /Key1 << /Nested true >> /Key2 42 >>"
	p := NewParser([]byte(input))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := obj.(*Dictionary)
	if !ok {
		t.Fatalf("expected *Dictionary, got %T", obj)
	}
	inner := dict.Get("Key1")
	if inner == nil {
		t.Fatal("missing /Key1")
	}
	innerDict, ok := inner.(*Dictionary)
	if !ok {
		t.Fatalf("/Key1: expected *Dictionary, got %T", inner)
	}
	nestedVal := innerDict.Get("Nested")
	if b, ok := nestedVal.(Boolean); !ok || !bool(b) {
		t.Errorf("/Key1/Nested: expected true, got %v", nestedVal)
	}
}

func TestParseIndirectRef(t *testing.T) {
	p := NewParser([]byte("10 0 R"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := obj.(IndirectRef)
	if !ok {
		t.Fatalf("expected IndirectRef, got %T", obj)
	}
	if ref.Number != 10 || ref.Generation != 0 {
		t.Errorf("expected 10 0 R, got %d %d R", ref.Number, ref.Generation)
	}
}

func TestParseIndirectObject(t *testing.T) {
	input := "1 0 obj\n<< /Type /Catalog >>\nendobj"
	p := NewParser([]byte(input))
	obj, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	if obj.Number != 1 || obj.Generation != 0 {
		t.Errorf("expected 1 0 obj, got %d %d obj", obj.Number, obj.Generation)
	}
	dict, ok := obj.Value.(*Dictionary)
	if !ok {
		t.Fatalf("expected *Dictionary, got %T", obj.Value)
	}
	if n := dict.Get("Type"); n == nil {
		t.Error("missing /Type in indirect object value")
	}
}

func TestParseIndirectObjectViaParseObject(t *testing.T) {
	input := "1 0 obj\n42\nendobj"
	p := NewParser([]byte(input))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	iobj, ok := obj.(*IndirectObject)
	if !ok {
		t.Fatalf("expected *IndirectObject, got %T", obj)
	}
	if iobj.Number != 1 || iobj.Generation != 0 {
		t.Errorf("expected 1 0 obj, got %d %d obj", iobj.Number, iobj.Generation)
	}
	if i, ok := iobj.Value.(Integer); !ok || int64(i) != 42 {
		t.Errorf("expected Integer 42, got %v", iobj.Value)
	}
}

func TestParseStream(t *testing.T) {
	input := "1 0 obj\n<< /Length 11 >>\nstream\r\nHello World\nendstream\nendobj"
	p := NewParser([]byte(input))
	obj, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := obj.Value.(*Stream)
	if !ok {
		t.Fatalf("expected *Stream, got %T", obj.Value)
	}
	if string(stream.Data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", stream.Data)
	}
}

func TestParseStreamLFOnly(t *testing.T) {
	input := "1 0 obj\n<< /Length 11 >>\nstream\nHello World\nendstream\nendobj"
	p := NewParser([]byte(input))
	obj, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := obj.Value.(*Stream)
	if !ok {
		t.Fatalf("expected *Stream, got %T", obj.Value)
	}
	if string(stream.Data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", stream.Data)
	}
}

func TestParseIntegerAmbiguity(t *testing.T) {
	// "42" alone should be Integer, not part of a ref
	p := NewParser([]byte("42"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := obj.(Integer); !ok {
		t.Errorf("expected Integer, got %T", obj)
	}
}

func TestParseIntegerFollowedByNonRef(t *testing.T) {
	// "42 /Name" - the 42 should be parsed as Integer
	p := NewParser([]byte("42 /Name"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	if i, ok := obj.(Integer); !ok || int64(i) != 42 {
		t.Errorf("expected Integer 42, got %T %v", obj, obj)
	}
}

func TestParseArrayWithRefs(t *testing.T) {
	input := "[1 0 R 2 0 R 42]"
	p := NewParser([]byte(input))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := obj.(Array)
	if !ok {
		t.Fatalf("expected Array, got %T", obj)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr))
	}

	ref1, ok := arr[0].(IndirectRef)
	if !ok {
		t.Errorf("arr[0]: expected IndirectRef, got %T", arr[0])
	} else if ref1.Number != 1 {
		t.Errorf("arr[0]: expected ref 1, got %d", ref1.Number)
	}

	ref2, ok := arr[1].(IndirectRef)
	if !ok {
		t.Errorf("arr[1]: expected IndirectRef, got %T", arr[1])
	} else if ref2.Number != 2 {
		t.Errorf("arr[1]: expected ref 2, got %d", ref2.Number)
	}

	if i, ok := arr[2].(Integer); !ok || int64(i) != 42 {
		t.Errorf("arr[2]: expected Integer 42, got %T %v", arr[2], arr[2])
	}
}

func TestParseMultipleIndirectObjects(t *testing.T) {
	input := `1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Count 0 /Kids [] >>
endobj`

	p := NewParser([]byte(input))

	obj1, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	if obj1.Number != 1 {
		t.Errorf("expected object 1, got %d", obj1.Number)
	}

	obj2, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatal(err)
	}
	if obj2.Number != 2 {
		t.Errorf("expected object 2, got %d", obj2.Number)
	}
}

func TestParseEmptyDict(t *testing.T) {
	p := NewParser([]byte("<< >>"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := obj.(*Dictionary)
	if !ok {
		t.Fatalf("expected *Dictionary, got %T", obj)
	}
	if dict.Len() != 0 {
		t.Errorf("expected empty dictionary, got %d entries", dict.Len())
	}
}

func TestParseEmptyArray(t *testing.T) {
	p := NewParser([]byte("[]"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := obj.(Array)
	if !ok {
		t.Fatalf("expected Array, got %T", obj)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %d elements", len(arr))
	}
}
