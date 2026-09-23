package pdf0

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// sameResolved compares a value of one document with a value of another,
// resolving references on both sides: the oracle for "the copy says what the
// source said".
func sameResolved(da *Document, a object.Object, db *Document, b object.Object, depth int) bool {
	if depth > 16 {
		return true
	}
	a, b = da.Resolve(a), db.Resolve(b)
	switch av := a.(type) {
	case *object.Dictionary:
		bv, ok := b.(*object.Dictionary)
		if !ok || av.Len() != bv.Len() {
			return false
		}
		for k, v := range av.All() {
			if !sameResolved(da, v, db, bv.Get(k), depth+1) {
				return false
			}
		}
		return true
	case object.Array:
		bv, ok := b.(object.Array)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sameResolved(da, av[i], db, bv[i], depth+1) {
				return false
			}
		}
		return true
	case *object.Stream:
		bv, ok := b.(*object.Stream)
		return ok && string(av.Data) == string(bv.Data)
	}
	return object.Equal(a, b)
}

// annotsOf returns a page's annotation dictionaries.
func annotsOf(d *Document, page *object.Dictionary) []*object.Dictionary {
	arr, _ := d.Resolve(page.Get("Annots")).(object.Array)
	var out []*object.Dictionary
	for _, a := range arr {
		if ad := d.ResolveDict(a); ad != nil {
			out = append(out, ad)
		}
	}
	return out
}

func omitted(r ImportReport) []string {
	var out []string
	for _, o := range r.Omitted {
		out = append(out, o.Entry)
	}
	return out
}

// TestExtractMaterialisesInheritedAttributes is the C30 regression: every
// extracted page carries, as its own entries, exactly the inheritable
// attributes that applied to it in the source — the source is the oracle.
func TestExtractMaterialisesInheritedAttributes(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	srcPages := src.PageList()
	for i, sp := range srcPages {
		out, _, err := src.ExtractPages([]int{i})
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		re := writeRead(t, out)
		pg := re.PageList()[0]
		for _, key := range []object.Name{"Resources", "MediaBox", "CropBox", "Rotate"} {
			want := src.view().InheritedPageAttr(sp, key)
			got := pg.Get(key)
			if want == nil {
				if got != nil && key != "Rotate" {
					t.Errorf("page %d: %s is %v, and the source had none", i, key, got)
				}
				continue
			}
			if got == nil {
				t.Errorf("page %d: %s is missing from the extracted page (source: %v)", i, key, want)
				continue
			}
			if !sameResolved(src, want, re, got, 0) {
				t.Errorf("page %d: %s is %v, the source's is %v", i, key, re.Resolve(got), src.Resolve(want))
			}
		}
		if got, want := pageTexts(t, re)[0], "p"+string(rune('0'+i)); got != want {
			t.Errorf("page %d: extracted text %q, want %q", i, got, want)
		}
	}
}

// TestAppendDoesNotLendTheTargetsAttributes is the other half of C30: an
// appended page must not take the target's /Rotate or /CropBox either.
func TestAppendDoesNotLendTheTargetsAttributes(t *testing.T) {
	objs := flatTreeObjects(1)
	objs[2] = `<< /Type /Pages /Kids [100 0 R] /Count 1 /Rotate 270 /CropBox [0 0 50 50] >>`
	dst := readAssembled(t, objs)
	src := readAssembled(t, nestedTreeObjects())
	if _, err := dst.AppendPages(src); err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, dst)
	pages := re.PageList()
	srcPages := src.PageList()
	if len(pages) != 5 {
		t.Fatalf("%d pages, want 5", len(pages))
	}
	for i, sp := range srcPages {
		pg := pages[i+1]
		wantRot := object.Int(src.Resolve(src.view().InheritedPageAttr(sp, "Rotate")))
		if got := object.Int(re.Resolve(re.view().InheritedPageAttr(pg, "Rotate"))); got != wantRot {
			t.Errorf("appended page %d shows /Rotate %d, the source page %d", i, got, wantRot)
		}
		wantCrop := src.view().InheritedPageAttr(sp, "CropBox")
		if wantCrop == nil {
			wantCrop = src.view().InheritedPageAttr(sp, "MediaBox")
		}
		if got := re.view().InheritedPageAttr(pg, "CropBox"); !sameResolved(src, wantCrop, re, got, 0) {
			t.Errorf("appended page %d shows /CropBox %v, the source page %v", i, re.Resolve(got), src.Resolve(wantCrop))
		}
	}
	checkCounts(t, re)
}

// TestExtractStopsAtOtherPages is the C89 regression: extracting one page
// copies that page and what it uses, not the rest of the document through the
// back-references pages hold to each other.
func TestExtractStopsAtOtherPages(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	out, report, err := src.ExtractPages([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	pageish := 0
	for _, o := range out.Objects {
		d, ok := o.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		switch typ, _ := d.Get("Type").(object.Name); typ {
		case "Page", "Pages":
			pageish++
		case "StructElem", "StructTreeRoot", "Bead", "Thread", "Outlines":
			t.Errorf("the extract holds a %s it has no use for", typ)
		}
	}
	if pageish != 2 {
		t.Errorf("a one-page extract holds %d Page/Pages dictionaries, want 2 (the page and the root)", pageish)
	}
	// Three links led to pages 1, 2 and 3; none of them came along.
	if report.DestinationsDropped != 3 {
		t.Errorf("DestinationsDropped = %d, want 3", report.DestinationsDropped)
	}
	re := writeRead(t, out)
	page := re.PageList()[0]
	var subtypes []string
	for _, a := range annotsOf(re, page) {
		st, _ := a.Get("Subtype").(object.Name)
		subtypes = append(subtypes, string(st))
		if p, ok := a.Get("P").(object.IndirectRef); ok && re.ResolveDict(p) != page {
			t.Errorf("a %s annotation's /P is not its page", st)
		}
		// An index into the source's parent tree, which did not come along.
		if sp := a.Get("StructParent"); sp != nil {
			t.Errorf("a %s annotation kept /StructParent %v", st, sp)
		}
	}
	if want := []string{"Text", "Popup", "Widget", "Widget"}; !slices.Equal(subtypes, want) {
		t.Errorf("annotations %v, want %v", subtypes, want)
	}
	if page.Get("B") != nil || page.Get("StructParents") != nil {
		t.Errorf("the page kept /B or /StructParents: %v %v", page.Get("B"), page.Get("StructParents"))
	}
	for _, want := range []string{"StructTreeRoot", "Outlines", "B", "Threads", "Names"} {
		if !slices.Contains(omitted(report), want) {
			t.Errorf("the report does not name %s as omitted: %v", want, omitted(report))
		}
	}
}

// TestExtractRemapsDestinations: links between pages that were all extracted
// lead to the copies, whether the destination was explicit, named or an
// action.
func TestExtractRemapsDestinations(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	out, report, err := src.ExtractPages([]int{0, 1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if report.DestinationsDropped != 0 || report.DestinationsRemapped != 3 {
		t.Errorf("remapped %d, dropped %d; want 3 and 0", report.DestinationsRemapped, report.DestinationsDropped)
	}
	re := writeRead(t, out)
	pages := re.PageList()
	links := 0
	for _, a := range annotsOf(re, pages[0]) {
		if st, _ := a.Get("Subtype").(object.Name); st != "Link" {
			continue
		}
		dest, _ := re.Resolve(a.Get("Dest")).(object.Array)
		if act := re.ResolveDict(a.Get("A")); act != nil {
			dest, _ = re.Resolve(act.Get("D")).(object.Array)
		}
		if len(dest) == 0 {
			t.Fatalf("link %d has no explicit destination: %v", links, a)
		}
		want := []int{3, 1, 2}[links]
		if re.ResolveDict(dest[0]) != pages[want] {
			t.Errorf("link %d leads to %v, want page %d", links, dest[0], want)
		}
		links++
	}
	if links != 3 {
		t.Errorf("%d links, want 3", links)
	}
}

// TestExtractCarriesFormFields: a widget keeps its field, the field keeps only
// the widgets that were imported, and the fields join the extract's form.
func TestExtractCarriesFormFields(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	cases := []struct {
		pages      []int
		fields     []string
		choiceKids int
	}{
		{[]int{0}, []string{"choice", "name"}, 1},
		{[]int{2}, []string{"choice"}, 1},
		{[]int{0, 2}, []string{"choice", "name"}, 2},
		{[]int{1}, nil, 0},
	}
	for _, tc := range cases {
		out, _, err := src.ExtractPages(tc.pages)
		if err != nil {
			t.Fatal(err)
		}
		re := writeRead(t, out)
		cat := re.ResolveDict(re.Trailer.Get("Root"))
		form := re.ResolveDict(cat.Get("AcroForm"))
		if tc.fields == nil {
			if form != nil {
				t.Errorf("pages %v: a form with no fields on them was carried", tc.pages)
			}
			continue
		}
		if form == nil {
			t.Fatalf("pages %v: no /AcroForm", tc.pages)
		}
		if !sameResolved(src, src.ResolveDict(src.ResolveDict(src.Trailer.Get("Root")).Get("AcroForm")).Get("DR"), re, form.Get("DR"), 0) {
			t.Errorf("pages %v: /DR not carried", tc.pages)
		}
		fields, _ := re.Resolve(form.Get("Fields")).(object.Array)
		var names []string
		for _, f := range fields {
			fd := re.ResolveDict(f)
			name, _ := re.Resolve(fd.Get("T")).(object.String)
			names = append(names, string(name.Value))
			if string(name.Value) != "choice" {
				continue
			}
			kids, _ := re.Resolve(fd.Get("Kids")).(object.Array)
			if len(kids) != tc.choiceKids {
				t.Errorf("pages %v: choice has %d kids, want %d", tc.pages, len(kids), tc.choiceKids)
			}
			for _, k := range kids {
				w := re.ResolveDict(k)
				if w.Get("Parent") != f {
					t.Errorf("pages %v: a kid's /Parent is %v, want %v", tc.pages, w.Get("Parent"), f)
				}
				if !slices.Contains(re.PageList(), re.ResolveDict(w.Get("P"))) {
					t.Errorf("pages %v: a widget's /P is not a page of the extract", tc.pages)
				}
			}
		}
		if !slices.Equal(names, tc.fields) {
			t.Errorf("pages %v: fields %v, want %v", tc.pages, names, tc.fields)
		}
	}
}

// TestExtractDuplicateIndices is the C90 regression: a repeated index is a
// second page, with its own dictionary and annotations, sharing the content.
func TestExtractDuplicateIndices(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	out, report, err := src.ExtractPages([]int{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, out)
	pages := re.PageList()
	if len(pages) != 2 || rootCount(t, re) != 2 {
		t.Fatalf("%d pages, /Count %d; want 2 and 2", len(pages), rootCount(t, re))
	}
	checkCounts(t, re)
	if pages[0] == pages[1] {
		t.Fatal("both pages are one dictionary")
	}
	if pages[0].Get("Contents") != pages[1].Get("Contents") {
		t.Errorf("the repeat has its own content stream (%v, %v); it should share it", pages[0].Get("Contents"), pages[1].Get("Contents"))
	}
	a0, a1 := pages[0].Get("Annots").(object.Array), pages[1].Get("Annots").(object.Array)
	for i := range a0 {
		if a0[i] == a1[i] {
			t.Errorf("annotation %d is shared by both pages", i)
		}
	}
	for _, a := range annotsOf(re, pages[1]) {
		if p := a.Get("P"); p != nil && re.ResolveDict(p) != pages[1] {
			t.Errorf("an annotation on the repeat points at the other page")
		}
	}
	want := []FieldRename{{"choice", "choice_2"}, {"name", "name_2"}}
	if !slices.Equal(report.FieldsRenamed, want) {
		t.Errorf("FieldsRenamed = %v, want %v", report.FieldsRenamed, want)
	}
	if got := pageTexts(t, re); !slices.Equal(got, []string{"p0", "p0"}) {
		t.Errorf("texts %v", got)
	}
}

// TestExtractInlinePage is the C129 regression: a page held as a direct
// dictionary in /Kids is extracted, not dropped.
func TestExtractInlinePage(t *testing.T) {
	objs := flatTreeObjects(2)
	objs[2] = `<< /Type /Pages /Kids [100 0 R << /Type /Page /MediaBox [0 0 100 100] /Contents 201 0 R /Resources << /Font << /F1 20 0 R >> >> >>] /Count 2 >>`
	src := readAssembled(t, objs)
	out, _, err := src.ExtractPages([]int{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, out)
	if got := pageTexts(t, re); !slices.Equal(got, []string{"flat1", "flat0"}) {
		t.Errorf("texts %v, want [flat1 flat0]", got)
	}
	if box := re.Resolve(re.PageList()[0].Get("MediaBox")); !object.Equal(box, object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)}) {
		t.Errorf("the inline page's /MediaBox is %v", box)
	}
	dst := readAssembled(t, flatTreeObjects(1))
	if _, err := dst.AppendPages(src); err != nil {
		t.Fatal(err)
	}
	if got := pageTexts(t, writeRead(t, dst)); !slices.Equal(got, []string{"flat0", "flat0", "flat1"}) {
		t.Errorf("after appending: texts %v", got)
	}
}

// lockedDocument returns a document encrypted with a user password and read
// without it.
func lockedDocument(t *testing.T) *Document {
	t.Helper()
	d := NewDocument()
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	if _, err := d.AddPage(Page{Width: 10, Height: 10, Content: &b}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEncryption("user", "owner"); err != nil {
		t.Fatal(err)
	}
	locked := writeRead(t, d)
	if !locked.Locked() {
		t.Fatal("precondition: the document is not Locked")
	}
	return locked
}

// TestPageOperationsRefuseLocked is the C31 regression, with its siblings: a
// Locked document's pages are ciphertext and may not be copied out, and
// plaintext may not be added to one, since Write passes it through verbatim.
func TestPageOperationsRefuseLocked(t *testing.T) {
	locked := lockedDocument(t)
	if _, _, err := locked.ExtractPages([]int{0}); err == nil || !errors.Is(err, ErrWrongPassword) {
		t.Errorf("ExtractPages of a Locked source: err = %v, want one wrapping ErrWrongPassword", err)
	}
	dst := readAssembled(t, flatTreeObjects(1))
	before := len(dst.Objects)
	if _, err := dst.AppendPages(locked); err == nil {
		t.Error("AppendPages of a Locked source was accepted")
	}
	if len(dst.Objects) != before || dst.PageCount() != 1 {
		t.Errorf("a refused AppendPages changed the target: %d objects (was %d), %d pages", len(dst.Objects), before, dst.PageCount())
	}

	target := lockedDocument(t)
	objs := len(target.Objects)
	if _, err := target.AppendPages(readAssembled(t, flatTreeObjects(1))); err == nil {
		t.Error("AppendPages into a Locked document was accepted")
	}
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	if _, err := target.AddPage(Page{Width: 10, Height: 10, Content: &b}); err == nil {
		t.Error("AddPage on a Locked document was accepted")
	}
	page := object.IndirectRef{Number: target.view().Pages(target.view().CatalogPages())[0].ObjNum}
	if err := target.SetOutline([]OutlineItem{{Title: "x", Page: page}}); err == nil {
		t.Error("SetOutline on a Locked document was accepted")
	}
	if err := target.SetStructureTree([]StructElem{{Tag: "P", Page: &page}}, nil); err == nil {
		t.Error("SetStructureTree on a Locked document was accepted")
	}
	var fb content.Builder
	fb.Rect(0, 0, 1, 1).Fill()
	if _, err := target.AddForm(Form{BBox: [4]float64{0, 0, 1, 1}, Content: &fb}); err == nil {
		t.Error("AddForm on a Locked document was accepted")
	}
	if _, err := target.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 1, 1}, Content: &fb}); err == nil {
		t.Error("AddTilingPattern on a Locked document was accepted")
	}
	if len(target.Objects) != objs {
		t.Errorf("refused operations changed the Locked document: %d objects, was %d", len(target.Objects), objs)
	}
}

// TestExtractFromDecryptedSource: a source read with its password is plaintext
// in memory and may be extracted; the extract is not encrypted, and the report
// says so rather than let the protection vanish unmentioned.
func TestExtractFromDecryptedSource(t *testing.T) {
	d := readAssembled(t, flatTreeObjects(1))
	if err := d.SetEncryption("user", "owner"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	src, err := ReadWithPassword(bytes.NewReader(buf.Bytes()), int64(buf.Len()), "user")
	if err != nil || src.Locked() {
		t.Fatalf("precondition: read with the password: err=%v locked=%v", err, src.Locked())
	}
	out, report, err := src.ExtractPages([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(omitted(report), "Encrypt") {
		t.Errorf("the report does not say the encryption was not carried: %v", omitted(report))
	}
	re := writeRead(t, out)
	if re.Encrypted {
		t.Error("the extract is encrypted")
	}
	if got := pageTexts(t, re); !slices.Equal(got, []string{"flat0"}) {
		t.Errorf("texts %v, want [flat0]", got)
	}
}

// TestImportRefusesDamagedSource: objects that Read could not decode or decrypt
// are missing (or empty) in the model, and Write refuses such a document. An
// import would launder the loss into a target that writes cleanly, so it
// refuses too.
func TestImportRefusesDamagedSource(t *testing.T) {
	for name, damage := range map[string]func(*Document){
		"a broken object stream":  func(d *Document) { d.brokenObjStms = []int{99} },
		"an undecryptable object": func(d *Document) { d.decryptFailures = []int{200} },
	} {
		src := readAssembled(t, flatTreeObjects(1))
		damage(src)
		if _, _, err := src.ExtractPages([]int{0}); err == nil {
			t.Errorf("%s: ExtractPages was accepted", name)
		}
		if _, err := NewDocument().AppendPages(src); err == nil {
			t.Errorf("%s: AppendPages was accepted", name)
		}
	}
}

// TestPageOperationsRefuseNil: a nil document is an error, not a panic.
func TestPageOperationsRefuseNil(t *testing.T) {
	var nilDoc *Document
	if _, _, err := nilDoc.ExtractPages([]int{0}); err == nil {
		t.Error("ExtractPages on nil")
	}
	if _, err := nilDoc.AppendPages(NewDocument()); err == nil {
		t.Error("AppendPages on nil")
	}
	if _, err := NewDocument().AppendPages(nil); err == nil {
		t.Error("AppendPages of nil")
	}
	var b content.Builder
	if _, err := nilDoc.AddPage(Page{Width: 1, Height: 1, Content: &b}); err == nil {
		t.Error("AddPage on nil")
	}
	if err := nilDoc.SetOutline(nil); err == nil {
		t.Error("SetOutline on nil")
	}
	if err := nilDoc.SetStructureTree(nil, nil); err == nil {
		t.Error("SetStructureTree on nil")
	}
}

// TestPageTreeCountsOnNestedTrees is the C29 regression: /Count stays the
// number of leaves on a nested tree, and an added page does not inherit the
// tree's rotation.
func TestPageTreeCountsOnNestedTrees(t *testing.T) {
	d := readAssembled(t, nestedTreeObjects())
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	ref, err := d.AddPage(Page{Width: 10, Height: 10, Content: &b})
	if err != nil {
		t.Fatal(err)
	}
	checkCounts(t, d)
	if rootCount(t, d) != 5 {
		t.Errorf("after AddPage the root /Count is %d, want 5", rootCount(t, d))
	}
	if rot := d.view().InheritedPageAttr(d.ResolveDict(ref), "Rotate"); object.Int(d.Resolve(rot)) != 0 {
		t.Errorf("the new page shows /Rotate %v, want 0", rot)
	}

	d2 := readAssembled(t, nestedTreeObjects())
	if _, err := d2.AppendPages(readAssembled(t, nestedTreeObjects())); err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, d2)
	checkCounts(t, re)
	if rootCount(t, re) != 8 {
		t.Errorf("after AppendPages the root /Count is %d, want 8", rootCount(t, re))
	}
	if got := pageTexts(t, re); !slices.Equal(got, []string{"p0", "p1", "p2", "p3", "p0", "p1", "p2", "p3"}) {
		t.Errorf("texts %v", got)
	}
}

// TestDirectPagesRoot is the C20 regression: a catalog whose /Pages is a
// direct dictionary, as the target and as the source.
func TestDirectPagesRoot(t *testing.T) {
	direct := func() *Document {
		objs := flatTreeObjects(1)
		objs[1] = `<< /Type /Catalog /Pages << /Type /Pages /Kids [100 0 R] /Count 1 /Rotate 90 >> >>`
		delete(objs, 2)
		return readAssembled(t, objs)
	}
	d := direct()
	if _, err := d.AppendPages(readAssembled(t, flatTreeObjects(2))); err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	if _, err := d.AddPage(Page{Width: 10, Height: 10, Content: &b}); err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, d)
	checkCounts(t, re)
	if re.PageCount() != 4 {
		t.Errorf("%d pages, want 4", re.PageCount())
	}
	// The first page still shows the root's rotation; the added ones do not.
	for i, want := range []int{90, 0, 0, 0} {
		pg := re.PageList()[i]
		if got := object.Int(re.Resolve(re.view().InheritedPageAttr(pg, "Rotate"))); got != want {
			t.Errorf("page %d shows /Rotate %d, want %d", i, got, want)
		}
	}

	// As a source: the rotation inherited from the direct root is carried,
	// although no /Parent could point at it.
	out, _, err := direct().ExtractPages([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	if rot := out.PageList()[0].Get("Rotate"); object.Int(rot) != 90 {
		t.Errorf("extracted from a direct root: /Rotate %v, want 90", rot)
	}
}

// TestSelfAppend: a document appended to itself doubles, and the second half's
// links and fields belong to the second half.
func TestSelfAppend(t *testing.T) {
	d := readAssembled(t, nestedTreeObjects())
	report, err := d.AppendPages(d)
	if err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, d)
	checkCounts(t, re)
	pages := re.PageList()
	if got := pageTexts(t, re); !slices.Equal(got, []string{"p0", "p1", "p2", "p3", "p0", "p1", "p2", "p3"}) {
		t.Fatalf("texts %v", got)
	}
	// The copy of page 0's first link leads to the copy of page 3.
	for _, a := range annotsOf(re, pages[4]) {
		if dest, ok := re.Resolve(a.Get("Dest")).(object.Array); ok {
			if re.ResolveDict(dest[0]) != pages[7] {
				t.Errorf("the appended link leads to %v, want the appended page 3", dest[0])
			}
			break
		}
	}
	if want := []FieldRename{{"choice", "choice_2"}, {"name", "name_2"}}; !slices.Equal(report.FieldsRenamed, want) {
		t.Errorf("FieldsRenamed = %v, want %v", report.FieldsRenamed, want)
	}
	cat := re.ResolveDict(re.Trailer.Get("Root"))
	fields, _ := re.Resolve(re.ResolveDict(cat.Get("AcroForm")).Get("Fields")).(object.Array)
	if len(fields) != 4 {
		t.Errorf("the form has %d fields, want 4", len(fields))
	}
}

// TestImportOptionalContent: a layer that is off by default in the source is
// off on the imported pages too.
func TestImportOptionalContent(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	offLayer := func(d *Document) int {
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		oc := d.ResolveDict(cat.Get("OCProperties"))
		if oc == nil {
			return 0
		}
		def := d.ResolveDict(oc.Get("D"))
		offs, _ := d.Resolve(def.Get("OFF")).(object.Array)
		n := 0
		for _, o := range offs {
			if name, _ := d.Resolve(d.ResolveDict(o).Get("Name")).(object.String); string(name.Value) == "Layer" {
				n++
			}
		}
		return n
	}
	out, _, err := src.ExtractPages([]int{3})
	if err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, out)
	if offLayer(re) != 1 {
		t.Error("the extract lost the layer's default OFF state")
	}
	// The layer the page's content uses is the one the configuration names.
	props := re.ResolveDict(re.ResolveDict(re.PageList()[0].Get("Resources")).Get("Properties"))
	cat := re.ResolveDict(re.Trailer.Get("Root"))
	ocgs, _ := re.Resolve(re.ResolveDict(cat.Get("OCProperties")).Get("OCGs")).(object.Array)
	if len(ocgs) != 1 || props.Get("OC1") != ocgs[0] {
		t.Errorf("the page uses %v, the configuration lists %v", props.Get("OC1"), ocgs)
	}

	// Into a document with its own optional content.
	objs := flatTreeObjects(1)
	objs[1] = `<< /Type /Catalog /Pages 2 0 R /OCProperties << /OCGs [50 0 R] /D << /Order [50 0 R] >> >> >>`
	objs[50] = `<< /Type /OCG /Name (Mine) >>`
	dst := readAssembled(t, objs)
	if _, err := dst.AppendPages(src); err != nil {
		t.Fatal(err)
	}
	re = writeRead(t, dst)
	if offLayer(re) != 1 {
		t.Error("appending lost the layer's default OFF state")
	}
}

// TestExtractCarriesPageDescription: /OutputIntents and /Lang describe the
// pages and go with an extract; the rest of the catalog is reported.
func TestExtractCarriesPageDescription(t *testing.T) {
	src := readAssembled(t, nestedTreeObjects())
	out, report, err := src.ExtractPages([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	re := writeRead(t, out)
	cat := re.ResolveDict(re.Trailer.Get("Root"))
	srcCat := src.ResolveDict(src.Trailer.Get("Root"))
	for _, key := range []object.Name{"OutputIntents", "Lang"} {
		if !sameResolved(src, srcCat.Get(key), re, cat.Get(key), 0) {
			t.Errorf("%s not carried: %v", key, cat.Get(key))
		}
		if slices.Contains(omitted(report), string(key)) {
			t.Errorf("%s reported as omitted, and it was carried", key)
		}
	}
	dst := readAssembled(t, flatTreeObjects(1))
	report, err = dst.AppendPages(src)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(omitted(report), "OutputIntents") {
		t.Errorf("AppendPages did not report the source's output intents: %v", omitted(report))
	}
}

// TestDestinationsMustNameAPage is the C136 regression: outline entries, links
// and structure elements must name a page of the document.
func TestDestinationsMustNameAPage(t *testing.T) {
	d := readAssembled(t, nestedTreeObjects())
	realPage := object.IndirectRef{Number: 13} // in the nested tree
	notPages := map[string]object.IndirectRef{
		"the catalog":      {Number: 1},
		"a font":           {Number: 20},
		"a /Pages node":    {Number: 3},
		"a missing object": {Number: 9999},
	}
	var b content.Builder
	b.Rect(0, 0, 1, 1).Fill()
	for what, ref := range notPages {
		ref := ref
		if err := d.SetOutline([]OutlineItem{{Title: "x", Page: ref}}); err == nil {
			t.Errorf("an outline entry naming %s was accepted", what)
		}
		if _, err := d.AddPage(Page{Width: 1, Height: 1, Content: &b, Links: []Link{{Rect: [4]float64{0, 0, 1, 1}, Page: &ref}}}); err == nil {
			t.Errorf("a link naming %s was accepted", what)
		}
		if err := d.SetStructureTree([]StructElem{{Tag: "P", Page: &ref, Content: []int{0}}}, nil); err == nil {
			t.Errorf("a structure element naming %s was accepted", what)
		}
	}
	if err := d.SetOutline([]OutlineItem{{Title: "x", Page: realPage}}); err != nil {
		t.Errorf("an outline entry naming a page was refused: %v", err)
	}

	// A page that is in the tree although its /Parent is wrong is still a page
	// (the full walk decides); an orphan /Page dictionary is not.
	objs := nestedTreeObjects()
	objs[13] = `<< /Type /Page /Parent 2 0 R /Contents 33 0 R >>`
	objs[14] = `<< /Type /Page /Parent 4 0 R /Contents 33 0 R >>`
	d = readAssembled(t, objs)
	if err := d.SetOutline([]OutlineItem{{Title: "x", Page: object.IndirectRef{Number: 13}}}); err != nil {
		t.Errorf("a listed page with a wrong /Parent was refused: %v", err)
	}
	if err := d.SetOutline([]OutlineItem{{Title: "x", Page: object.IndirectRef{Number: 14}}}); err == nil {
		t.Error("an orphan /Page no tree lists was accepted")
	}
}

// TestAppendMergesIntoExistingForm: appended fields join the target's form; a
// name the target already uses is renamed, the target's default resources win
// a clash and the clash is reported, and a default appearance the fields took
// from the source's form moves onto them.
func TestAppendMergesIntoExistingForm(t *testing.T) {
	objs := flatTreeObjects(1)
	objs[1] = `<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [60 0 R] /DR << /Font << /Helv 61 0 R >> >> /DA (/Helv 12 Tf 1 0 0 rg) >> >>`
	objs[100] = `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 200 0 R /Resources << /Font << /F1 20 0 R >> >> /Annots [60 0 R] >>`
	objs[60] = `<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /Rect [0 0 10 10] /P 100 0 R >>`
	objs[61] = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>`
	dst := readAssembled(t, objs)
	src := readAssembled(t, nestedTreeObjects())
	report, err := dst.AppendPages(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []FieldRename{{"name", "name_2"}}; !slices.Equal(report.FieldsRenamed, want) {
		t.Errorf("FieldsRenamed = %v, want %v", report.FieldsRenamed, want)
	}
	if !slices.Contains(omitted(report), "AcroForm/DR/Font/Helv") {
		t.Errorf("the /Helv clash is not reported: %v", omitted(report))
	}
	re := writeRead(t, dst)
	cat := re.ResolveDict(re.Trailer.Get("Root"))
	form := re.ResolveDict(cat.Get("AcroForm"))
	fields, _ := re.Resolve(form.Get("Fields")).(object.Array)
	var names []string
	for _, f := range fields {
		fd := re.ResolveDict(f)
		name, _ := re.Resolve(fd.Get("T")).(object.String)
		names = append(names, string(name.Value))
		if string(name.Value) == "name" {
			continue // the target's own
		}
		if da, _ := re.Resolve(fd.Get("DA")).(object.String); string(da.Value) != "/Helv 0 Tf 0 g" {
			t.Errorf("appended field %s has /DA %q, want the source form's", name.Value, da.Value)
		}
	}
	if want := []string{"name", "choice", "name_2"}; !slices.Equal(names, want) {
		t.Errorf("fields %v, want %v", names, want)
	}
	helv := re.ResolveDict(re.ResolveDict(re.ResolveDict(form.Get("DR")).Get("Font")).Get("Helv"))
	if base, _ := helv.Get("BaseFont").(object.Name); base != "Helvetica-Bold" {
		t.Errorf("the target's /Helv was replaced by %v", base)
	}
}

// TestExtractPagesOutOfRange keeps the index check and its message.
func TestExtractPagesOutOfRange(t *testing.T) {
	src := readAssembled(t, flatTreeObjects(2))
	for _, idx := range []int{-1, 2} {
		_, _, err := src.ExtractPages([]int{0, idx})
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("index %d: err = %v", idx, err)
		}
	}
}
