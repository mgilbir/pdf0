package core

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The aggregate content budget is enforced by View.Content, so its test lives
// here with it.

func makeFlateContentStream(decodedLen int) *object.Stream {
	raw := bytes.Repeat([]byte("0 0 0 rg\n"), decodedLen/9+1)[:decodedLen]
	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(raw)
	zw.Close()
	d := &object.Dictionary{}
	d.Set("Length", object.Integer(zb.Len()))
	d.Set("Filter", object.Name("FlateDecode"))
	return &object.Stream{Dict: *d, Data: zb.Bytes()}
}

// TestDecodeContentStreamBudget verifies the aggregate decoded-content budget:
// content streams decode normally until the per-run budget is reached, after
// which further streams are treated as undecodable (nil) so a flate-bomb file
// cannot force unbounded decode+tokenize work. Under the budget behaviour is
// unchanged.
func TestDecodeContentStreamBudget(t *testing.T) {
	v := View{Limits: DefaultLimits(), Run: NewRun(&Recorder{})}

	// A ~1 MB content stream decodes fine while under budget.
	s1 := makeFlateContentStream(1 << 20)
	if got := v.Content(s1); len(got) != 1<<20 {
		t.Fatalf("under budget: decoded %d bytes, want %d", len(got), 1<<20)
	}
	if v.Run.contentBytes != 1<<20 {
		t.Fatalf("contentBytes = %d, want %d", v.Run.contentBytes, 1<<20)
	}

	// Simulate the run having reached the budget.
	v.Run.contentBytes = v.Limits.DecodedContentBytes
	s2 := makeFlateContentStream(1 << 20)
	if got := v.Content(s2); got != nil {
		t.Fatalf("over budget: decoded %d bytes, want nil (budget must skip decoding)", len(got))
	}
	// The decision is negatively cached and stable on re-request.
	if got := v.Content(s2); got != nil {
		t.Fatalf("over budget (cached): got %d bytes, want nil", len(got))
	}
}

// TestContentBombBoundedValidation is an end-to-end guard: a small PDF whose
// many page-content streams each inflate well past the budget (a flate bomb)
// must validate in bounded time rather than decoding and tokenizing everything.
// The budget is lowered so the test stays fast; the ratio (total content far
// exceeds the budget) is what matters. Without the budget this walks all
// nPages*perPage of content; with it, work stops once the budget is exhausted.
