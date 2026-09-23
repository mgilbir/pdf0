package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// TestCorpusContentStateMemoSound is the correctness guard for the content
// interpreter's memo (internal/core/interp.go). The memo shares one execution
// of a stream among every caller that reaches it with the same resources and
// the same inherited state; that is sound only if those really determine the
// result. Over the veraPDF corpus and the small Cal Poly PDF/VT files, every
// page's device colour and the document's font usage are computed twice —
// through one memoised engine for the whole document, pages in order, and with
// no memo at all — and must agree exactly.
//
// It replaces the cross-check between PDF/A's scanner and PDF/X's memoised
// copy of it, which no longer exist: PDF/A and PDF/X now read the same
// interpreter, so what needs guarding is the memo, not agreement between two
// implementations.
func TestCorpusContentStateMemoSound(t *testing.T) {
	var files []string
	var absent []string
	if _, ok, why := testfiles.VeraPDFCorpus.Lookup(t); ok {
		files = append(files, testfiles.VeraPDFCorpus.PDFs(t, "")...)
	} else {
		absent = append(absent, why)
	}
	if _, ok, why := testfiles.CalPolyPDFVT.Lookup(t); ok {
		for _, f := range testfiles.CalPolyPDFVT.Glob(t, "*.pdf") {
			b := filepath.Base(f)
			if strings.HasSuffix(b, "- 10.pdf") || strings.HasSuffix(b, "- 100.pdf") || strings.HasPrefix(b, "Documentation") {
				files = append(files, f)
			}
		}
	} else {
		absent = append(absent, why)
	}
	if len(absent) == 2 {
		t.Skipf("neither data set is present:\n%s", strings.Join(absent, "\n"))
	}
	if len(files) == 0 {
		t.Fatal("the data sets are present but no file was selected; the Cal Poly name filter no longer matches")
	}

	var pages, mismatches int
	report := func(format string, args ...any) {
		mismatches++
		if mismatches <= 10 {
			t.Errorf(format, args...)
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		v := doc.view()
		v.Run = core.NewRun(&core.Recorder{})
		cat := v.Catalog()
		if cat == nil {
			continue
		}
		for _, pg := range v.Pages(cat.Get("Pages")) {
			pages++
			r, c, g := core.PageDeviceColourUse(v, pg.Dict)
			wr, wc, wg := core.PageDeviceColourUseNoMemo(v, pg.Dict)
			if r != wr || c != wc || g != wg {
				report("%s page %d: memoised (R%v C%v G%v), unmemoised (R%v C%v G%v)", filepath.Base(f), pg.ObjNum, r, c, g, wr, wc, wg)
			}
			if op, wop := core.PageOverprintsICCCMYK(v, pg.Dict), core.PageOverprintsICCCMYKNoMemo(v, pg.Dict); op != wop {
				report("%s page %d: ICCBased CMYK overprint memoised %v, unmemoised %v", filepath.Base(f), pg.ObjNum, op, wop)
			}
		}
		if got, want := fontUsageSummary(core.CollectFontTextUsage(v)), fontUsageSummary(core.CollectFontTextUsageNoMemo(v)); got != want {
			report("%s: font usage differs\n memoised:   %s\n unmemoised: %s", filepath.Base(f), got, want)
		}
	}
	t.Logf("compared %d pages in %d files, %d mismatches", pages, len(files), mismatches)
}

// fontUsageSummary renders font usage as a set: which fonts, which distinct
// strings, which modes. The memo records a stream's text once where the
// unmemoised run records it per invocation, so repetition is not compared.
func fontUsageSummary(u map[*object.Dictionary]*core.FontTextUsage) string {
	var fonts []string
	for _, fu := range u {
		strs := map[string]bool{}
		for _, s := range fu.Strings {
			strs[string(s)] = true
		}
		var ss []string
		for s := range strs {
			ss = append(ss, fmt.Sprintf("%q", s))
		}
		sort.Strings(ss)
		var modes []int
		for m := range fu.Modes {
			modes = append(modes, m)
		}
		sort.Ints(modes)
		fonts = append(fonts, fmt.Sprintf("%d%v%v", fu.ObjNum, modes, ss))
	}
	sort.Strings(fonts)
	return strings.Join(fonts, " ")
}

// TestDeviceColourMemoKeepsGroupApart pins that one stream reached two ways is
// two memo entries. A form XObject invoked with Do has its transparency group
// applied; the same stream reached as an annotation appearance does not.
// Keying a memo on the stream alone once let whichever visit came first answer
// for both, so a form whose isolated CalRGB group covers its DeviceRGB was
// reported unmasked once an appearance-stream visit had cached the raw value.
func TestDeviceColourMemoKeepsGroupApart(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{}, Trailer: object.Dictionary{}}
	put := func(num int, v object.Object) { doc.Objects[num] = &object.IndirectObject{Number: num, Value: v} }

	// The shared form: DeviceRGB inside, wrapped in an isolated CalRGB group.
	form := &object.Stream{Dict: object.Dictionary{}, Data: []byte("1 0 0 rg 0 0 10 10 re f\n")}
	form.Dict.Set("Type", object.Name("XObject"))
	form.Dict.Set("Subtype", object.Name("Form"))
	form.Dict.Set("Length", object.Integer(len(form.Data)))
	group := &object.Dictionary{}
	group.Set("S", object.Name("Transparency"))
	group.Set("CS", object.Array{object.Name("CalRGB"), &object.Dictionary{}})
	group.Set("I", object.Boolean(true))
	form.Dict.Set("Group", group)
	put(10, form)

	// Page 1 reaches the form as an annotation appearance: no group masking.
	annot := &object.Dictionary{}
	annot.Set("Type", object.Name("Annot"))
	ap := &object.Dictionary{}
	ap.Set("N", object.IndirectRef{Number: 10})
	annot.Set("AP", ap)
	put(11, annot)
	page1 := &object.Dictionary{}
	page1.Set("Type", object.Name("Page"))
	page1.Set("Annots", object.Array{object.IndirectRef{Number: 11}})
	put(12, page1)

	// Page 2 invokes the very same stream with Do: the group applies.
	content := &object.Stream{Dict: object.Dictionary{}, Data: []byte("q /X1 Do Q\n")}
	content.Dict.Set("Length", object.Integer(len(content.Data)))
	put(13, content)
	xo := &object.Dictionary{}
	xo.Set("X1", object.IndirectRef{Number: 10})
	res := &object.Dictionary{}
	res.Set("XObject", xo)
	page2 := &object.Dictionary{}
	page2.Set("Type", object.Name("Page"))
	page2.Set("Resources", res)
	page2.Set("Contents", object.IndirectRef{Number: 13})
	put(14, page2)

	for _, order := range [][]*object.Dictionary{{page1, page2}, {page2, page1}} {
		v := doc.view()
		v.Run = core.NewRun(nil) // one memo for both pages
		for _, pg := range order {
			wantRGB := pg == page1
			if r, _, _ := core.PageDeviceColourUse(v, pg); r != wantRGB {
				t.Errorf("page %s (visited %s): DeviceRGB = %v, want %v", pageName(pg, page1), orderName(order, page1), r, wantRGB)
			}
		}
	}
}

func pageName(pg, page1 *object.Dictionary) string {
	if pg == page1 {
		return "with the appearance stream"
	}
	return "invoking the form with Do"
}

func orderName(order []*object.Dictionary, page1 *object.Dictionary) string {
	if order[0] == page1 {
		return "second"
	}
	return "first"
}
