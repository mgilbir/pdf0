package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/pdfua"
)

// The PDF/UA corpus ratchet (docs audit 2026-10-11 D12). The veraPDF corpus's
// PDF_UA-1 and PDF_UA-2 suites are the second oracle the PDF/UA validator is
// held to, beside the PDF Association reference files
// (TestUAReferenceFilesNoFalsePositives): a -pass- file is one veraPDF
// accepts, a -fail- file one it rejects. Nothing ratcheted them before, and the
// numbers moved unnoticed in both directions — 155/155 fail files caught in
// July 2026, 150 by October, with 30 pass files reported.
//
// Findings that are about the checker rather than the document (IsCheckerFinding:
// a budget, a cancellation, an internal error) count neither way. A pass file
// with any other finding is a false positive; a fail file with one is caught.
// docs/pdfua.md names every file on the wrong side of the line and why.
//
// Update the baselines with the values the test logs after an intended change.
var uaCorpusBaselines = map[string]struct {
	maxFalsePositives int // pass files reported
	minCaught         int // fail files reported
}{
	"PDF_UA-1": {maxFalsePositives: 0, minCaught: 153},
	"PDF_UA-2": {maxFalsePositives: 0, minCaught: 68},
}

func TestCorpusPDFUA(t *testing.T) {
	for _, part := range []string{"PDF_UA-1", "PDF_UA-2"} {
		t.Run(part, func(t *testing.T) {
			validate := ValidatePDFUA
			if part == "PDF_UA-2" {
				validate = ValidatePDFUA2
			}
			var pass, fail, caught int
			var fpFiles, missedFiles []string
			root := corpusRoot(t)
			for i, path := range corpusTestFiles(t, part) {
				rel, _ := filepath.Rel(root, path)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", rel, err)
				}
				doc, err := Read(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Errorf("%s does not parse: %v", rel, err)
					continue
				}
				var found []pdfua.Violation
				for _, v := range validate(doc) {
					if !IsCheckerFinding(v) {
						found = append(found, v)
					}
				}
				if strings.Contains(filepath.Base(path), "-pass-") {
					pass++
					if len(found) > 0 {
						fpFiles = append(fpFiles, rel+": "+found[0].Error())
					}
				} else {
					fail++
					if len(found) > 0 {
						caught++
					} else {
						missedFiles = append(missedFiles, rel)
					}
				}
				if (i+1)%100 == 0 {
					runtime.GC()
				}
			}
			t.Logf("%s: pass=%d reported=%d | fail=%d caught=%d missed=%d", part, pass, len(fpFiles), fail, caught, len(missedFiles))
			base := uaCorpusBaselines[part]
			if len(fpFiles) > base.maxFalsePositives {
				t.Errorf("%d pass files reported, baseline %d (regression):\n  %s", len(fpFiles), base.maxFalsePositives, strings.Join(fpFiles, "\n  "))
			}
			if caught < base.minCaught {
				t.Errorf("%d fail files caught, baseline %d (detection regressed). Missed:\n  %s", caught, base.minCaught, strings.Join(missedFiles, "\n  "))
			}
		})
	}
}
