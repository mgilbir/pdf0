package core

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// The document graph as a rule sees it: every dictionary reachable from the
// trailer, direct ones included.
//
// A rule about what a document *does* — its annotations, actions, images, file
// specifications, fonts — is a rule about the objects the document uses, and
// those are the ones reachable from the trailer. Ranging over the object table
// instead gets that wrong twice (audit 2026-09-22 C83):
//
//   - a dictionary written directly inside another object is not in the table,
//     so it is never judged — a Link whose /A is an inline JavaScript action,
//     the shape most producers write, passed the PDF/X prohibition on
//     JavaScript while the same action written as its own object failed it;
//   - an object nothing refers to is in the table, so it is judged — an
//     orphan left behind by an incremental update accuses the document of
//     something it no longer contains.
//
// ReachableDicts is the walk that gets both right, done once per run and shared
// by every rule that asks. Rules about the file's syntax — what every indirect
// object in the file must look like, used or not — still range over the table,
// and say so (see internal/lint's TestValidatorsWalkTheReachableGraph).

// ReachableDict is one dictionary the document reaches.
type ReachableDict struct {
	// Dict is the dictionary: a stream's dictionary when Stream is set.
	Dict *object.Dictionary
	// Stream is the stream Dict belongs to, or nil for a plain dictionary.
	Stream *object.Stream
	// ObjNum is the indirect object that holds Dict: Dict's own object when
	// Top, otherwise the object Dict is written inside. It is 0 for a
	// dictionary written directly in the trailer.
	ObjNum int
	// Top reports that Dict (or Stream) is the value of indirect object ObjNum
	// itself, rather than a direct dictionary nested inside it.
	Top bool
}

// maxDirectNesting bounds how deep the walk descends through direct values
// within one indirect object. The parser caps nesting at 1000, so no parsed
// document comes near it; it is here for a graph built in memory, where a
// slice can hold itself and an unbounded walk would never end.
const maxDirectNesting = 4096

// GuardGraphNesting is reported when the walk meets direct values nested more
// deeply than maxDirectNesting: what lies below was not visited, so a rule
// that finds nothing there has not looked.
const GuardGraphNesting = "graph-nesting" // no knob: maxDirectNesting

type reachableSlot struct{}

type reachableMemo struct {
	dicts []ReachableDict
	nums  []int
	valid bool
}

// ReachableDicts returns every dictionary reachable from the trailer, each
// exactly once, in a fixed depth-first order (dictionary entries in the order
// the dictionary holds them). Direct dictionaries are included, stream
// dictionaries are included with their stream, and an object nothing reaches
// is not. The list is computed once per run.
//
// The walk is iterative, so no file can exhaust the stack, and it visits each
// indirect object and each dictionary once, so its cost is the size of the
// graph whatever the file's references look like. Every value visited is
// charged to the run's work meter; a run stopped during the walk — its budget
// spent or its context ended — is unwound (core.Meter) rather than handed a
// truncated list, which the memo would otherwise keep for every later rule.
func (v View) ReachableDicts() []ReachableDict {
	m := Slot[reachableMemo](v.Run, reachableSlot{})
	if m.valid {
		return m.dicts
	}
	m.dicts, m.nums = v.walkReachable()
	m.valid = true
	return m.dicts
}

// ReachableObjectNums returns the number of every indirect object reachable
// from the trailer — whatever its value, an array or a number as much as a
// dictionary — in the order the walk first reaches them. It is for a rule that
// looks at each object as a whole and descends into its direct values itself.
func (v View) ReachableObjectNums() []int {
	m := Slot[reachableMemo](v.Run, reachableSlot{})
	if !m.valid {
		m.dicts, m.nums = v.walkReachable()
		m.valid = true
	}
	return m.nums
}

func (v View) walkReachable() ([]ReachableDict, []int) {
	if v.Trailer == nil {
		return nil, nil
	}
	type item struct {
		o      object.Object
		objNum int
		top    bool // o is the value of indirect object objNum
		depth  int  // direct nesting below objNum
	}
	var (
		out      []ReachableDict
		nums     []int
		stack    []item
		seenRef  = map[int]bool{}
		seenDict = map[*object.Dictionary]bool{}
		tripped  bool
	)
	// push adds values in reverse so they pop in the order they are written.
	push := func(vals []object.Object, objNum, depth int) {
		for i := len(vals) - 1; i >= 0; i-- {
			stack = append(stack, item{o: vals[i], objNum: objNum, depth: depth})
		}
	}
	dictValues := func(d *object.Dictionary) []object.Object {
		vals := make([]object.Object, 0, d.Len())
		for val := range d.Values() {
			vals = append(vals, val)
		}
		return vals
	}
	push(dictValues(v.Trailer), 0, 1)
	for len(stack) > 0 {
		v.Charge(1)
		if v.Cancel.Stopped() {
			// Outside a run nothing is metered, and the walk stops; inside
			// one this does not return.
			v.CheckStopped()
			break
		}
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.depth > maxDirectNesting {
			if !tripped {
				tripped = true
				v.Note(GuardGraphNesting, fmt.Sprintf("direct objects nested more than %d deep; what lies below was not visited", maxDirectNesting), it.objNum)
			}
			continue
		}
		switch x := it.o.(type) {
		case object.IndirectRef:
			if seenRef[x.Number] {
				continue
			}
			seenRef[x.Number] = true
			if io := v.Objects[x.Number]; io != nil && io.Value != nil {
				nums = append(nums, x.Number)
				stack = append(stack, item{o: io.Value, objNum: x.Number, top: true})
			}
		case *object.Dictionary:
			if x == nil || seenDict[x] {
				continue
			}
			seenDict[x] = true
			out = append(out, ReachableDict{Dict: x, ObjNum: it.objNum, Top: it.top})
			push(dictValues(x), it.objNum, it.depth+1)
		case *object.Stream:
			if x == nil || seenDict[&x.Dict] {
				continue
			}
			seenDict[&x.Dict] = true
			out = append(out, ReachableDict{Dict: &x.Dict, Stream: x, ObjNum: it.objNum, Top: it.top})
			push(dictValues(&x.Dict), it.objNum, it.depth+1)
		case object.Array:
			push([]object.Object(x), it.objNum, it.depth+1)
		}
	}
	return out, nums
}
