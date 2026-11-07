package fonts

import (
	"context"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// discard is an allocator that keeps nothing: what embedding costs, without
// a document to hold it.
type discard struct{ n int }

func (a *discard) Add(object.Object) object.IndirectRef {
	a.n++
	return object.IndirectRef{Number: a.n}
}

// BenchmarkEmbedPerDocument is one document's worth of a label in a font
// loaded once: a fresh wrapper of a fresh clone, one Record, one embedding
// (#439).
func BenchmarkEmbedPerDocument(b *testing.B) {
	s := notoShapingFace(b)
	const text = "Measurement 0123456789"
	glyphs := labelGlyphs(b, Adopt(s.Clone()), text)
	embed := func() {
		f := Adopt(s.Clone())
		if _, err := f.Record(text, glyphs); err != nil {
			b.Fatal(err)
		}
		if _, err := f.EmbedFormsContext(context.Background(), &discard{}, Forms{Horizontal: true}, SubsetLimits{}); err != nil {
			b.Fatal(err)
		}
	}
	embed()
	b.ReportAllocs()
	for b.Loop() {
		embed()
	}
}
