package pdfa

import (
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
func mkView(objs map[int]*object.IndirectObject, trailer object.Dictionary) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	tr := trailer
	return core.View{
		Objects: objs,
		Trailer: &tr,
		Limits:  core.DefaultLimits(),
		Run:     core.NewRun(&core.Recorder{}),
	}
}

// mkPDFAView is mkView over the minimal conforming skeleton for a level — the
// same object graph NewPDFADocument wraps into a Document.
func mkPDFAView(level PDFALevel) core.View {
	objs, trailer, version := Skeleton(level, "", "")
	v := mkView(objs, trailer)
	v.Version = version
	return v
}

// mkViewVersion is mkView with the header version set.
func mkViewVersion(objs map[int]*object.IndirectObject, trailer object.Dictionary, version string) core.View {
	v := mkView(objs, trailer)
	v.Version = version
	return v
}

// mkViewBroken is mkView with the object-stream containers Read could not
// decode, which the object-stream rules report on.
func mkViewBroken(objs map[int]*object.IndirectObject, broken []int) core.View {
	v := mkView(objs, object.Dictionary{})
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
func dictWith(k object.Name, v object.Object) object.Dictionary {
	d := object.Dictionary{}
	d.Set(k, v)
	return d
}

// ptrDict makes an addressable copy, for the view fields that hold a pointer.
func ptrDict(d object.Dictionary) *object.Dictionary { return &d }

// utf16be encodes s as a PDF text string: a UTF-16BE byte-order mark followed
// by big-endian code units, the form /Lang and Unicode file-spec entries use.
func utf16be(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

func hasRule(errs []ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func filterRule(errs []ValidationError, rule string) []ValidationError {
	var result []ValidationError
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
