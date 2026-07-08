package pdf0

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSpecExamplesRegenerate guards the spec-example pipeline against drift: the
// committed testdata/spec_examples*.json must still be exactly what
// cmd/extract_spec_examples/ produces from the spec PDF. Without this, an edit
// to the extraction scripts (or a spec update) could leave the committed
// fixtures stale and nothing would notice.
//
// It runs only when the (gitignored, copyrighted) spec PDFs, pdftotext, and
// python3 are all present, so it self-skips on a fresh clone and in CI.
func TestSpecExamplesRegenerate(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	cases := []struct {
		name      string
		specPDF   string
		script    string
		committed string
	}{
		{
			name:      "PDF 2.0",
			specPDF:   "spec/pdf2.0/ISO_32000-2_sponsored-ec2.pdf",
			script:    "cmd/extract_spec_examples/main.py",
			committed: "testdata/spec_examples.json",
		},
		{
			name:      "PDF 1.7",
			specPDF:   "spec/pdf1.7/PDF32000_2008.pdf",
			script:    "cmd/extract_spec_examples/main17.py",
			committed: "testdata/spec_examples_17.json",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := os.Stat(c.specPDF); err != nil {
				t.Skipf("spec PDF %s not present", c.specPDF)
			}

			dir := t.TempDir()
			txt := filepath.Join(dir, "spec.txt")
			if out, err := exec.Command("pdftotext", "-layout", c.specPDF, txt).CombinedOutput(); err != nil {
				t.Fatalf("pdftotext: %v\n%s", err, out)
			}

			regen, err := exec.Command("python3", c.script, txt).Output()
			if err != nil {
				t.Fatalf("%s: %v", c.script, err)
			}

			committed, err := os.ReadFile(c.committed)
			if err != nil {
				t.Fatalf("reading %s: %v", c.committed, err)
			}

			var got, want interface{}
			if err := json.Unmarshal(regen, &got); err != nil {
				t.Fatalf("regenerated JSON invalid: %v", err)
			}
			if err := json.Unmarshal(committed, &want); err != nil {
				t.Fatalf("committed JSON invalid: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s is stale: re-run the extractor and commit the result\n"+
					"  pdftotext -layout %q /tmp/spec.txt && python3 %s /tmp/spec.txt > %s",
					c.committed, c.specPDF, c.script, c.committed)
			}
		})
	}
}
