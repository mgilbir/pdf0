package pdf0

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestSaveVerdict is audit 2026-09-22 C49: Save turned every finding into a
// ConformanceError ("the document claims X and does not meet it"), including a
// resource limit that stopped a check and a context that ended the run.
func TestSaveVerdict(t *testing.T) {
	real := pdfa.Violation{Rule: "6.2.4.3", Level: pdfa.PDFA2b, Message: "DeviceRGB used"}
	limit := pdfa.Violation{Rule: "limit", Level: pdfa.PDFA2b, Message: "resource limit reached"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, vs := range [][]pdfa.Violation{nil, {limit}, {real, limit}} {
		if err := saveVerdict(core.NewCanceler(ctx), pdfa.PDFA2b, vs); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled run with %d findings: %v, want an error wrapping context.Canceled", len(vs), err)
		}
	}
	live := core.Canceler{}
	if err := saveVerdict(live, pdfa.PDFA2b, nil); err != nil {
		t.Errorf("clean: %v", err)
	}
	var ice *IncompleteCheckError
	var ce *ConformanceError
	if err := saveVerdict(live, pdfa.PDFA2b, []pdfa.Violation{limit}); !errors.As(err, &ice) || errors.As(err, &ce) {
		t.Errorf("checker finding only: %T %v, want *IncompleteCheckError and not *ConformanceError", err, err)
	}
	err := saveVerdict(live, pdfa.PDFA2b, []pdfa.Violation{real, limit})
	if !errors.As(err, &ce) || len(ce.Violations) != 1 || ce.Violations[0] != real || len(ce.Unchecked) != 1 {
		t.Errorf("mixed: %T %+v, want a ConformanceError with the one real finding and the checker finding apart", err, err)
	}
}

// TestSaveReadsBackUnderTheDocumentsLimits: the read-back used plain Read, so
// the document's own limits did not apply to the check of it. A document read
// with a scanning limit its content exceeds cannot be fully checked, and Save
// says so instead of writing it.
func TestSaveReadsBackUnderTheDocumentsLimits(t *testing.T) {
	var buf bytes.Buffer
	if err := mustPDFADoc(t, pdfa.PDFA2b).Write(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	var out bytes.Buffer
	if err := readRaw(t, raw).Save(&out); err != nil {
		t.Fatalf("control: Save of the skeleton: %v", err)
	}
	// The ICC bound: the output intent's profile is not read, so the colour
	// rules cannot finish; the metadata still can, which is what lets Save
	// reach the check at all.
	doc := readRaw(t, raw, WithMaxICCProfileBytes(1))
	out.Reset()
	err := doc.Save(&out)
	var ice *IncompleteCheckError
	if !errors.As(err, &ice) {
		t.Fatalf("Save under limits the check cannot finish within: %T %v, want *IncompleteCheckError", err, err)
	}
	if out.Len() != 0 {
		t.Errorf("Save wrote %d bytes of a document it could not check", out.Len())
	}
}
