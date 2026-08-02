package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/pdfx"
)

// TestDevColorScannerMatchesPDFA is the correctness guard for the memoised
// device-colour scanner: for every page it must return exactly what the trusted
// PDF/A scanPageForDeviceCS returns. The two implementations share only the leaf
// primitives, so this pins them together — a divergence in the fast path would
// fail here. It runs over the veraPDF corpus when present (the widest variety of
// real colour usage) and always over any Cal Poly PDF/VT files, and skips when
// neither is available.
func TestDevColorScannerMatchesPDFA(t *testing.T) {
	var files []string
	if root := os.Getenv("VERAPDF_CORPUS"); root != "" {
		filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
			if e == nil && !i.IsDir() && filepath.Ext(p) == ".pdf" {
				files = append(files, p)
			}
			return nil
		})
	}
	cp, _ := filepath.Glob("testdata/pdfvt/*.pdf")
	for _, f := range cp {
		b := filepath.Base(f)
		if strings.HasSuffix(b, "- 10.pdf") || strings.HasSuffix(b, "- 100.pdf") || strings.HasPrefix(b, "Documentation") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Skip("no corpus or Cal Poly files available")
	}

	var pages, mismatches int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return
			}
			cat := doc.view().Catalog()
			if cat == nil {
				return
			}
			sc := pdfx.NewDevColorScanner(doc.view())
			for _, pg := range doc.view().Pages(cat.Get("Pages")) {
				pages++
				wantR, wantC, wantG := scanPageForDeviceCS(doc, pg.Dict)
				got := sc.PageDeviceUse(pg.Dict)
				if got.RGB != wantR || got.CMYK != wantC || got.Gray != wantG {
					mismatches++
					if mismatches <= 10 {
						t.Errorf("%s obj %d: scanPageForDeviceCS=(R%v C%v G%v) memoised=(R%v C%v G%v)",
							filepath.Base(f), pg.ObjNum, wantR, wantC, wantG, got.RGB, got.CMYK, got.Gray)
					}
				}
			}
		}()
	}
	t.Logf("compared %d pages, %d mismatches", pages, mismatches)
}

// TestDevColorScannerGroupMemoKey pins that the memo distinguishes the two ways
// one stream can be reached. A form XObject invoked with Do has its transparency
// group applied; the same stream reached as an annotation appearance (or a
// tiling pattern) does not. Keying the memo on the stream alone let whichever
// visit came first answer for both, so a form whose isolated CalRGB group covers
// its DeviceRGB was reported unmasked once an appearance-stream visit had cached
// the raw value — "DeviceRGB used without a matching OutputIntent, DefaultRGB or
// covering group colour space" against a page whose colour was in fact covered.
func TestDevColorScannerGroupMemoKey(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	put := func(num int, v Object) { doc.Objects[num] = &IndirectObject{Number: num, Value: v} }

	// The shared form: DeviceRGB inside, wrapped in an isolated CalRGB group.
	form := &Stream{Dict: Dictionary{}, Data: []byte("1 0 0 rg 0 0 10 10 re f\n")}
	form.Dict.Set("Type", Name("XObject"))
	form.Dict.Set("Subtype", Name("Form"))
	form.Dict.Set("Length", Integer(len(form.Data)))
	group := &Dictionary{}
	group.Set("S", Name("Transparency"))
	group.Set("CS", Array{Name("CalRGB"), &Dictionary{}})
	group.Set("I", Boolean(true))
	form.Dict.Set("Group", group)
	put(10, form)

	// Page 1 reaches the form as an annotation appearance: no group masking.
	annot := &Dictionary{}
	annot.Set("Type", Name("Annot"))
	ap := &Dictionary{}
	ap.Set("N", IndirectRef{Number: 10})
	annot.Set("AP", ap)
	put(11, annot)
	page1 := &Dictionary{}
	page1.Set("Type", Name("Page"))
	page1.Set("Annots", Array{IndirectRef{Number: 11}})
	put(12, page1)

	// Page 2 invokes the very same stream with Do: the group applies.
	content := &Stream{Dict: Dictionary{}, Data: []byte("q /X1 Do Q\n")}
	content.Dict.Set("Length", Integer(len(content.Data)))
	put(13, content)
	xo := &Dictionary{}
	xo.Set("X1", IndirectRef{Number: 10})
	res := &Dictionary{}
	res.Set("XObject", xo)
	page2 := &Dictionary{}
	page2.Set("Type", Name("Page"))
	page2.Set("Resources", res)
	page2.Set("Contents", IndirectRef{Number: 13})
	put(14, page2)

	// The trusted PDF/A scanner is the oracle for both pages.
	wantR1, _, _ := scanPageForDeviceCS(doc, page1)
	wantR2, _, _ := scanPageForDeviceCS(doc, page2)
	if !wantR1 || wantR2 {
		t.Fatalf("fixture does not exercise the bug: appearance page RGB=%v (want true), Do page RGB=%v (want false)", wantR1, wantR2)
	}

	// One scanner, appearance stream first: that visit must not answer for the
	// group-masked one.
	sc := pdfx.NewDevColorScanner(doc.view())
	if got := sc.PageDeviceUse(page1); got.RGB != wantR1 {
		t.Errorf("appearance-stream page: rgb=%v, want %v", got.RGB, wantR1)
	}
	if got := sc.PageDeviceUse(page2); got.RGB != wantR2 {
		t.Errorf("page invoking the same form with Do: rgb=%v, want %v (the memo answered for the wrong applyGroup)", got.RGB, wantR2)
	}

	// And the reverse order, so neither visit is privileged.
	sc = pdfx.NewDevColorScanner(doc.view())
	if got := sc.PageDeviceUse(page2); got.RGB != wantR2 {
		t.Errorf("Do page first: rgb=%v, want %v", got.RGB, wantR2)
	}
	if got := sc.PageDeviceUse(page1); got.RGB != wantR1 {
		t.Errorf("appearance-stream page second: rgb=%v, want %v", got.RGB, wantR1)
	}
}
