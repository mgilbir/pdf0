package fonts

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
)

// The bounded forms of Shape, ShapeWith and DrawShaped produce exactly what
// the unbounded ones do, and when a limit or the context stops them, nothing:
// no stream, no glyphs recorded, no embedding changed.

var boundedTexts = []string{"office", "affluent", "क्षत्रिय", "नमस्ते", "Ελληνικά", "ẹ́"}

func notoSans(t *testing.T) *Face {
	t.Helper()
	f, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// streamOf is what draw writes for s, in a fresh text object selecting F1.
func streamOf(t *testing.T, draw func(*content.Builder)) []byte {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F1", 12)
	draw(&b)
	b.EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// boundedFaces are the face kinds the equivalence is checked in: composite, a
// simple face, whose used glyphs only shaping records, and a bitmap face.
func boundedFaces(t *testing.T) []struct {
	name  string
	load  func() *Face
	texts []string
} {
	simple := func() *Face {
		f, err := NotoSansSimple()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	return []struct {
		name  string
		load  func() *Face
		texts []string
	}{
		{"composite", func() *Face { return notoSans(t) }, boundedTexts},
		{"simple", simple, []string{"office", "Café"}},
		{"bitmap", func() *Face { return strikesFace(t) }, []string{"HELLO", "ABCXY"}},
	}
}

func TestBoundedShapingIsUnboundedShapingWhenItFits(t *testing.T) {
	for _, fc := range boundedFaces(t) {
		t.Run(fc.name, func(t *testing.T) { boundedEquivalence(t, fc.load, fc.texts) })
	}
}

func boundedEquivalence(t *testing.T, load func() *Face, texts []string) {
	ctx := context.Background()
	for _, s := range texts {
		plain, bounded := load(), load()
		want := streamOf(t, func(b *content.Builder) { plain.DrawShaped(b, s, 12) })
		got := streamOf(t, func(b *content.Builder) {
			if _, err := bounded.DrawShapedContext(ctx, b, s, 12, RunLimits{}); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
		})
		if !bytes.Equal(got, want) {
			t.Errorf("%q: DrawShapedContext wrote\n%q\nDrawShaped\n%q", s, got, want)
		}
		if !slices.Equal(bounded.Used(), plain.Used()) {
			t.Errorf("%q: the bounded face recorded %v, the unbounded %v", s, bounded.Used(), plain.Used())
		}
		a, b := &allocator{}, &allocator{}
		if _, err := plain.Embed(a); err != nil {
			t.Fatalf("%q: embedding the unbounded face: %v", s, err)
		}
		if _, err := bounded.Embed(b); err != nil {
			t.Fatalf("%q: embedding the bounded face: %v", s, err)
		}
		if len(a.objects) != len(b.objects) {
			t.Errorf("%q: the embeddings write %d and %d objects", s, len(a.objects), len(b.objects))
		}

		p1, p2 := load(), load()
		spans, missing := p1.Shape(s)
		gotSpans, gotMissing, err := p2.ShapeContext(ctx, s, RunLimits{})
		if err != nil || gotMissing != missing || !slices.EqualFunc(gotSpans, spans, spanEqual) {
			t.Errorf("%q: ShapeContext = %v, %d, %v; Shape = %v, %d", s, gotSpans, gotMissing, err, spans, missing)
		}
		if !slices.Equal(p2.Used(), p1.Used()) {
			t.Errorf("%q: ShapeContext recorded %v, Shape %v", s, p2.Used(), p1.Used())
		}
		p1, p2 = load(), load()
		spans, missing = p1.ShapeWith(s, "smcp")
		gotSpans, gotMissing, err = p2.ShapeWithContext(ctx, s, RunLimits{}, "smcp")
		if err != nil || gotMissing != missing || !slices.EqualFunc(gotSpans, spans, spanEqual) {
			t.Errorf("%q: ShapeWithContext = %v, %d, %v; ShapeWith = %v, %d", s, gotSpans, gotMissing, err, spans, missing)
		}
		if !slices.Equal(p2.Used(), p1.Used()) {
			t.Errorf("%q: ShapeWithContext recorded %v, ShapeWith %v", s, p2.Used(), p1.Used())
		}
	}
}

// spanEqual compares two spans whole, the /ActualText markers among them.
func spanEqual(a, b content.TextSpan) bool { return reflect.DeepEqual(a, b) }

func TestBoundedShapingThatStopsLeavesNothing(t *testing.T) {
	long := strings.Repeat("office ", 1000) // 7000 bytes, past the 4096 default
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		text   string
		limits RunLimits
		want   error
	}{
		{"input past the default", context.Background(), long, RunLimits{}, ErrRunLimit},
		{"input past a limit", context.Background(), "office", RunLimits{MaxInputBytes: 3}, ErrRunLimit},
		{"work past a limit", context.Background(), "affluent office", RunLimits{MaxWork: 4}, ErrRunLimit},
		{"glyphs past a limit", context.Background(), "affluent office", RunLimits{MaxGlyphs: 2}, ErrRunLimit},
		{"cancelled", cancelled, "office", RunLimits{}, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			face := notoSans(t)
			var b content.Builder
			b.BeginText().SetFont("F1", 12)
			missing, err := face.DrawShapedContext(tc.ctx, &b, tc.text, 12, tc.limits)
			if !errors.Is(err, tc.want) {
				t.Fatalf("DrawShapedContext: %d, %v; want %v", missing, err, tc.want)
			}
			b.EndText()
			out, berr := b.Bytes()
			if berr != nil || string(out) != "BT\n/F1 12 Tf\nET\n" {
				t.Errorf("the stream was written to: %q, %v", out, berr)
			}
			if used := face.Used(); len(used) != 0 {
				t.Errorf("the face recorded %v from a run it did not draw", used)
			}
			if _, _, err := face.ShapeContext(tc.ctx, tc.text, tc.limits); !errors.Is(err, tc.want) {
				t.Errorf("ShapeContext: %v; want %v", err, tc.want)
			}
			if _, _, err := face.ShapeWithContext(tc.ctx, tc.text, tc.limits, "smcp"); !errors.Is(err, tc.want) {
				t.Errorf("ShapeWithContext: %v; want %v", err, tc.want)
			}
			if used := face.Used(); len(used) != 0 {
				t.Errorf("the face recorded %v from runs it did not shape", used)
			}
		})
	}
}

// TestTheDefaultLimitsAdmitAFullRunOfDevanagari: a run as long as the input
// default allows, of the costliest text measured, fits forme's default work
// budget. Before forme 0.9.0 it did not: forme charged a lookup's size at every
// position, and a word of Devanagari ran out (forme#916).
func TestTheDefaultLimitsAdmitAFullRunOfDevanagari(t *testing.T) {
	const word = "नमस्ते "
	s := strings.Repeat(word, 4096/len(word))
	if len(s) > 4096 || len(s) < 4096-len(word) {
		t.Fatalf("the run is %d bytes", len(s))
	}
	if _, _, err := notoSans(t).ShapeContext(context.Background(), s, RunLimits{}); err != nil {
		t.Errorf("%d bytes of Devanagari under the default limits: %v", len(s), err)
	}
}
