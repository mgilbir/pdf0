package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/einvoice"
)

// TestValidateFacturXInvoiceCorpus is the FP=0 oracle bridging the container and
// the EN 16931 rule engine: the invoice XML of every conforming Factur-X / ZUGFeRD
// sample must pass the foundational EN 16931 rules. Skips when the corpus is
// absent.
func TestValidateFacturXInvoiceCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/*.pdf")
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present")
	}
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "FAIL") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		res := ValidateFacturX(doc, data)
		if len(res.XML) == 0 {
			continue
		}
		if v := einvoice.Validate(res.XML, res.Profile); len(v) != 0 {
			t.Errorf("%s [%s]: expected 0 EN 16931 violations, got %d (first: %s: %s)",
				name, res.Profile, len(v), v[0].Rule, v[0].Message)
		}
	}
}
