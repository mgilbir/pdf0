package core

import "github.com/mgilbir/pdf0/object"

// View is what a subsystem needs in order to read a document: the object graph,
// enough of the file's identity to judge it, the budget it must stay inside, and
// somewhere to say so when it cannot.
//
// It exists so that a subsystem does not have to name Document. Document carries
// the public API — twenty-eight exported methods that callers depend on — and a
// method must be declared in the package that declares its type, so Document
// cannot move below the packages that would need it without taking the whole
// public surface along. A View is passed *down* instead: Document builds one and
// hands it over, which leaves the facade at the top of the graph where it
// belongs and nothing underneath depending on it.
//
// A View is a value, and a cheap one: five words plus two pointers. Build it per
// call rather than caching it, so that a Document mutated between operations is
// never read through a stale copy. What must be shared across the calls of one
// operation lives behind Run.
type View struct {
	// Objects is the object graph, keyed by object number. It is the same map
	// the Document holds, not a copy: resolving through a View sees whatever the
	// Document currently holds.
	Objects map[int]*object.IndirectObject
	// Trailer is the file trailer.
	Trailer *object.Dictionary
	// Version is the header version, "1.7" or "2.0".
	Version string
	// Limits is the resolved resource budget for this document.
	Limits Limits
	// Cancel is this operation's cancellation signal, or the zero value when the
	// operation cannot be cancelled.
	Cancel Canceler
	// Run holds what the calls of one operation share. It is nil outside a run —
	// a bare Read, say — and every method here tolerates that.
	Run *Run
}

// Run is the state whose lifetime is exactly one operation: the trips recorded
// while it ran, and the memos that keep a traversal from being repeated.
//
// It is a pointer inside View precisely so that a View copied by value still
// shares it. Copying a View must never fork the memo table, or the second copy
// silently redoes work the first already did — which is invisible in the output
// and shows up only as time.
type Run struct {
	// Trips collects the guard trips of this operation. May be nil.
	Trips *Recorder
}

// Resolve follows an indirect reference to the object it names, chasing
// reference-to-reference chains. It returns obj unchanged when obj is not a
// reference, and nil when the reference names an object the file does not
// contain.
//
// The hop count doubles as the cycle guard. A bounded loop costs nothing on
// what is the hottest path in the package, where a visited set would allocate
// on every call; real files chain a handful of hops at most, so exceeding the
// bound means a cycle or garbage either way.
func (v View) Resolve(obj object.Object) object.Object {
	for hops := 0; hops < 64; hops++ {
		ref, ok := obj.(object.IndirectRef)
		if !ok {
			return obj
		}
		iobj, exists := v.Objects[ref.Number]
		if !exists {
			return nil
		}
		obj = iobj.Value
	}
	return nil
}

// ResolveDict resolves obj and type-asserts to *object.Dictionary, returning nil
// when it resolves to anything else. The nil covers both "absent" and "present
// but the wrong type", because a caller that wanted a dictionary can do nothing
// useful with either.
func (v View) ResolveDict(obj object.Object) *object.Dictionary {
	d, _ := v.Resolve(obj).(*object.Dictionary)
	return d
}

// Catalog returns the document catalog, or nil when the trailer names no /Root
// or names one that is not a dictionary.
func (v View) Catalog() *object.Dictionary {
	if v.Trailer == nil {
		return nil
	}
	root := v.Trailer.Get("Root")
	if root == nil {
		return nil
	}
	return v.ResolveDict(root)
}

// Note records that a guard stopped this operation short. It is a no-op outside
// a run, which is what lets a guard report unconditionally without first asking
// whether anyone is listening.
//
// What becomes of the trip afterwards — which rule identifier it carries, which
// finding type it turns into — is not decided here. That belongs with the
// findings, in the package that defines them.
func (v View) Note(guard, detail string, obj int) {
	if v.Run == nil {
		return
	}
	v.Run.Trips.Note(guard, detail, obj)
}
