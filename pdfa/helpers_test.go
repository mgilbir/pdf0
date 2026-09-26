package pdfa

import (
	"testing"

	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkView builds the view these tests check against.
//
// The limits and the run both matter. A zero core.Limits is a budget of zero,
// so a view built without them decodes nothing while reporting no error; and a
// nil run makes every memo a fresh one, which turns the memoization tests into
// tautologies. The root package's Document.view supplies both; this is the
// equivalent for a package that has no Document.
func mkView(objs map[int]*object.IndirectObject, trailer *object.Dictionary) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	if trailer == nil {
		trailer = &object.Dictionary{}
	}
	return core.View{
		Objects: objs,
		Trailer: trailer,
		Limits:  core.DefaultLimits(),
		Run:     core.NewRun(&core.Recorder{}),
	}
}

// mkPDFAView is mkView over the minimal conforming skeleton for a level — the
// same object graph NewPDFADocument wraps into a Document.
func mkPDFAView(level Level) core.View {
	// Skeleton's error is always nil today and this helper has no testing.TB to
	// report one through. Panicking here would be the very thing the error
	// return replaced — but this is a test helper, not the library, and a
	// fixture that cannot be built has nothing useful to return. mkPDFAViewT
	// below is the version that fails properly, for callers that have a T.
	objs, trailer, version, err := Skeleton(level, "", "")
	if err != nil {
		panic("pdfa: test fixture: building the " + level.String() + " skeleton: " + err.Error())
	}
	v := mkView(objs, &trailer)
	v.Version = version
	return v
}

// mkPDFAViewT is mkPDFAView for a caller that has a testing.TB, which is all of
// them but for the table-level fixtures built before a subtest starts.
func mkPDFAViewT(tb testing.TB, level Level) core.View {
	tb.Helper()
	objs, trailer, version, err := Skeleton(level, "", "")
	if err != nil {
		tb.Fatalf("building the %s skeleton: %v", level, err)
	}
	v := mkView(objs, &trailer)
	v.Version = version
	return v
}

// mkViewVersion is mkView with the header version set.
func mkViewVersion(objs map[int]*object.IndirectObject, trailer *object.Dictionary, version string) core.View {
	v := mkView(objs, trailer)
	v.Version = version
	return v
}

// mkViewBroken is mkView with the object-stream containers Read could not
// decode, which the object-stream rules report on.
func mkViewBroken(objs map[int]*object.IndirectObject, broken []int) core.View {
	v := mkView(objs, nil)
	v.BrokenObjStms = broken
	return v
}

// mkV completes a partially built view the way Document.view does: a non-nil
// object map, a trailer to resolve against, the real limits and a shared run.
// Tests that care about a specific field set it in the literal they pass.
func mkV(v core.View) core.View {
	if v.Objects == nil {
		v.Objects = map[int]*object.IndirectObject{}
	}
	if v.Trailer == nil {
		v.Trailer = &object.Dictionary{}
	}
	if v.Limits == (core.Limits{}) {
		v.Limits = core.DefaultLimits()
	}
	if v.Run == nil {
		v.Run = core.NewRun(&core.Recorder{})
	}
	return v
}

// addTestPage inserts a page (object 20) into a skeleton's empty page tree and
// returns its dictionary for further mutation.
func addTestPage(v core.View) *object.Dictionary {
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	v.Objects[20] = &object.IndirectObject{Number: 20, Value: page}
	pages := v.Objects[2].Value.(*object.Dictionary)
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 20}})
	pages.Set("Count", object.Integer(1))
	return page
}

// dictWith is the one-entry dictionary these fixtures build over and over.
func dictWith(k object.Name, v object.Object) *object.Dictionary {
	return object.NewDictionary(object.Entry{Key: k, Value: v})
}

// utf16be encodes s as a PDF text string: a UTF-16BE byte-order mark followed
// by big-endian code units, the form /Lang and Unicode file-spec entries use.
func utf16be(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

func hasRule(errs []Violation, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func filterRule(errs []Violation, rule string) []Violation {
	var result []Violation
	for _, e := range errs {
		if e.Rule == rule {
			result = append(result, e)
		}
	}
	return result
}

// addExtGStateToDoc adds an ExtGState dict to the view's page Resources.
// It creates a page (obj 20) with Resources/ExtGState referencing gsObj (obj 10).
func addExtGStateToDoc(v core.View, gs *object.Dictionary) {
	v.Objects[10] = &object.IndirectObject{Number: 10, Value: gs}

	gsDict := &object.Dictionary{}
	gsDict.Set("GS0", object.IndirectRef{Number: 10})

	resDict := &object.Dictionary{}
	resDict.Set("ExtGState", gsDict)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Resources", resDict)

	v.Objects[20] = &object.IndirectObject{Number: 20, Value: page}

	// Update page tree to include this page
	pagesDict := v.ResolveDict(object.IndirectRef{Number: 2})
	pagesDict.Set("Kids", object.Array{object.IndirectRef{Number: 20}})
	pagesDict.Set("Count", object.Integer(1))
}

// hasMessage reports whether any violation's message contains want.
func hasMessage(errs []Violation, want string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return true
		}
	}
	return false
}

// referenced makes objects part of the document and returns the view. A rule
// judges what the document reaches (core.View.ReachableDicts), so an object
// dropped into the table with nothing pointing at it is an orphan, which is
// deliberately not judged (audit 2026-09-22 C83). The references hang from a
// trailer entry no rule reads, which keeps a fixture about the rule it tests;
// the tests of reachability itself build real structure. Call it before the
// first check: the reachable set is computed once per run.
func referenced(v core.View, nums ...int) core.View {
	arr, _ := v.Trailer.Get("PDF0TestFixture").(object.Array)
	for _, n := range nums {
		arr = append(arr, object.IndirectRef{Number: n})
	}
	v.Trailer.Set("PDF0TestFixture", arr)
	return v
}
