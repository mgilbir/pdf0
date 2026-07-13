package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			cat := getCatalog(doc)
			if cat == nil {
				return
			}
			sc := newDevColorScanner(doc)
			for _, pg := range collectPages(doc, cat.Get("Pages")) {
				pages++
				wantR, wantC, wantG := scanPageForDeviceCS(doc, pg.dict)
				got := sc.pageDeviceUse(pg.dict)
				if got.rgb != wantR || got.cmyk != wantC || got.gray != wantG {
					mismatches++
					if mismatches <= 10 {
						t.Errorf("%s obj %d: scanPageForDeviceCS=(R%v C%v G%v) memoised=(R%v C%v G%v)",
							filepath.Base(f), pg.objNum, wantR, wantC, wantG, got.rgb, got.cmyk, got.gray)
					}
				}
			}
		}()
	}
	t.Logf("compared %d pages, %d mismatches", pages, mismatches)
}
