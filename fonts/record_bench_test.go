package fonts

import "testing"

// BenchmarkRecordSteadyState is a run recorded again on a face that has
// already seen every glyph of it: a caller recording many short labels (#434).
func BenchmarkRecordSteadyState(b *testing.B) {
	s := notoShapingFace(b)
	for _, text := range []string{"0", "label", "Measurement 0123456789"} {
		f := Adopt(s.Clone())
		glyphs := labelGlyphs(b, f, text)
		recordLabel(b, f, text)
		b.Run(text, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := f.Record(text, glyphs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
