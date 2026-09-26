package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

func reachView(objs map[int]*object.IndirectObject, trailer *object.Dictionary) View {
	return View{Objects: objs, Trailer: trailer, Limits: DefaultLimits(), Run: NewRun(&Recorder{})}
}

// TestReachableDictsIsTheDocument pins what a rule sees: every dictionary the
// trailer reaches, direct ones included and carrying the number of the object
// they are written in, stream dictionaries with their stream, each once
// however often it is referenced — and no orphan.
func TestReachableDictsIsTheDocument(t *testing.T) {
	inlineAction := object.NewDictionary(object.Entry{Key: "S", Value: object.Name("JavaScript")})
	annot := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Link")},
		object.Entry{Key: "A", Value: inlineAction})
	page := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Page")},
		object.Entry{Key: "Parent", Value: object.IndirectRef{Number: 2}}, // a back-edge
		object.Entry{Key: "Annots", Value: object.Array{annot}},
		object.Entry{Key: "Contents", Value: object.IndirectRef{Number: 4}})
	objs := map[int]*object.IndirectObject{
		1: {Number: 1, Value: object.NewDictionary(object.Entry{Key: "Pages", Value: object.IndirectRef{Number: 2}})},
		2: {Number: 2, Value: object.NewDictionary(object.Entry{Key: "Kids", Value: object.Array{object.IndirectRef{Number: 3}, object.IndirectRef{Number: 3}}})},
		3: {Number: 3, Value: page},
		4: {Number: 4, Value: object.NewStream(object.NewDictionary(), []byte("q Q"))},
		// An orphan: nothing refers to it.
		9: {Number: 9, Value: object.NewDictionary(object.Entry{Key: "S", Value: object.Name("JavaScript")})},
	}
	info := object.NewDictionary(object.Entry{Key: "Title", Value: object.String{Value: []byte("t")}})
	trailer := object.NewDictionary(
		object.Entry{Key: "Root", Value: object.IndirectRef{Number: 1}},
		object.Entry{Key: "Info", Value: info}) // a direct dictionary in the trailer
	v := reachView(objs, trailer)

	got := map[*object.Dictionary]ReachableDict{}
	for _, r := range v.ReachableDicts() {
		if _, dup := got[r.Dict]; dup {
			t.Errorf("dictionary %v visited twice", r.Dict)
		}
		got[r.Dict] = r
	}
	want := []struct {
		name   string
		d      *object.Dictionary
		objNum int
		top    bool
		stream bool
	}{
		{"catalog", objs[1].Value.(*object.Dictionary), 1, true, false},
		{"pages", objs[2].Value.(*object.Dictionary), 2, true, false},
		{"page", page, 3, true, false},
		{"direct annotation", annot, 3, false, false},
		{"inline action", inlineAction, 3, false, false},
		{"content stream", &objs[4].Value.(*object.Stream).Dict, 4, true, true},
		{"direct Info", info, 0, false, false},
	}
	for _, w := range want {
		r, ok := got[w.d]
		if !ok {
			t.Errorf("%s: not reached", w.name)
			continue
		}
		if r.ObjNum != w.objNum || r.Top != w.top || (r.Stream != nil) != w.stream {
			t.Errorf("%s: ObjNum=%d Top=%v stream=%v, want %d %v %v", w.name, r.ObjNum, r.Top, r.Stream != nil, w.objNum, w.top, w.stream)
		}
	}
	if _, ok := got[objs[9].Value.(*object.Dictionary)]; ok {
		t.Error("the orphan object 9 was reached")
	}
	if len(got) != len(want) {
		t.Errorf("reached %d dictionaries, want %d", len(got), len(want))
	}
	nums := v.ReachableObjectNums()
	if fmt.Sprint(nums) != "[1 2 3 4]" {
		t.Errorf("ReachableObjectNums = %v, want [1 2 3 4]", nums)
	}
}

// TestReachableDictsBoundsDirectNesting: a graph built in memory can hold an
// array inside itself, which no parsed file can. The walk stops at its nesting
// bound, says so under GuardGraphNesting, and still returns what it reached.
//
// Without the bound the walk never ends, so it runs in a capped child.
func TestReachableDictsBoundsDirectNesting(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, testReachableDictsBoundsDirectNesting)
}

func testReachableDictsBoundsDirectNesting(t *testing.T) {
	loop := make(object.Array, 2)
	loop[0] = object.NewDictionary(object.Entry{Key: "K", Value: object.Name("v")})
	loop[1] = loop
	rec := &Recorder{}
	v := View{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: loop}},
		Trailer: object.NewDictionary(object.Entry{Key: "Root", Value: object.IndirectRef{Number: 1}}),
		Limits:  DefaultLimits(),
		Run:     NewRun(rec),
	}
	got := v.ReachableDicts()
	if len(got) != 1 {
		t.Errorf("reached %d dictionaries, want the one in the loop", len(got))
	}
	trips := rec.Snapshot()
	if len(trips) != 1 || trips[0].guard != GuardGraphNesting {
		t.Errorf("trips = %v, want one %s", trips, GuardGraphNesting)
	}
}
