package pdf0

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// countingContext is a live context that counts its Err calls and reports
// itself cancelled from the done-th on (never, for done 0).
type countingContext struct {
	context.Context
	done  int64
	calls atomic.Int64
}

func (c *countingContext) Err() error {
	if n := c.calls.Add(1); c.done > 0 && n >= c.done {
		return context.Canceled
	}
	return nil
}

// addWithFaces adds one of each thing that names faces — a page, a form, a
// tiling pattern — drawn in faces, through the Context form, and the plain
// form when ctx is nil.
var addWithFaces = map[string]func(d *Document, ctx context.Context, faces map[object.Name]*fonts.Face, limits fonts.SubsetLimits) error{
	"page": func(d *Document, ctx context.Context, faces map[object.Name]*fonts.Face, limits fonts.SubsetLimits) error {
		p := Page{Width: 200, Height: 100, Content: drawnIn(faces), Faces: faces}
		var err error
		if ctx == nil {
			_, err = d.AddPage(p)
		} else {
			_, err = d.AddPageContext(ctx, p, limits)
		}
		return err
	},
	"form": func(d *Document, ctx context.Context, faces map[object.Name]*fonts.Face, limits fonts.SubsetLimits) error {
		f := Form{BBox: [4]float64{0, 0, 200, 100}, Content: drawnIn(faces), Faces: faces}
		var err error
		if ctx == nil {
			_, err = d.AddForm(f)
		} else {
			_, err = d.AddFormContext(ctx, f, limits)
		}
		return err
	},
	"pattern": func(d *Document, ctx context.Context, faces map[object.Name]*fonts.Face, limits fonts.SubsetLimits) error {
		p := TilingPattern{BBox: [4]float64{0, 0, 200, 100}, XStep: 200, YStep: 100, Content: drawnIn(faces), Faces: faces}
		var err error
		if ctx == nil {
			_, err = d.AddTilingPattern(p)
		} else {
			_, err = d.AddTilingPatternContext(ctx, p, limits)
		}
		return err
	},
}

// drawnIn is a drawing of a word in each face, by the name it is given.
func drawnIn(faces map[object.Name]*fonts.Face) *content.Builder {
	var b content.Builder
	for _, name := range sortedNames(faces) {
		b.BeginText().SetFont(name, 12)
		faces[name].DrawShaped(&b, "Hello", 12)
		b.EndText()
	}
	return &b
}

func writtenBytes(t *testing.T, d *Document) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestTheContextFormsEmbedUnderTheContext (#420) adds a page, a form and a
// tiling pattern through their Context forms. A done context, a subset limit
// too small for the font, and a context that turns done between the first
// face and the second each fail the call and leave the document byte for
// byte as it was, the first face's embedding included; a live context and
// zero limits write what the plain call writes.
func TestTheContextFormsEmbedUnderTheContext(t *testing.T) {
	for name, add := range addWithFaces {
		t.Run(name, func(t *testing.T) {
			one := func() map[object.Name]*fonts.Face { return map[object.Name]*fonts.Face{"F1": testFace(t)} }
			two := func() map[object.Name]*fonts.Face {
				return map[object.Name]*fonts.Face{"F1": testFace(t), "F2": simpleFace(t)}
			}
			unchanged := func(t *testing.T, faces map[object.Name]*fonts.Face, ctx context.Context, limits fonts.SubsetLimits, want error) {
				t.Helper()
				doc := NewDocument()
				before := writtenBytes(t, doc)
				if err := add(doc, ctx, faces, limits); !errors.Is(err, want) {
					t.Fatalf("got %v; want %v", err, want)
				}
				if !bytes.Equal(writtenBytes(t, doc), before) {
					t.Error("the refused call changed the document")
				}
			}
			t.Run("done", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				unchanged(t, one(), ctx, fonts.SubsetLimits{}, context.Canceled)
			})
			t.Run("limit", func(t *testing.T) {
				unchanged(t, one(), context.Background(), fonts.SubsetLimits{MaxBytes: 64}, fonts.ErrSubsetLimit)
			})
			t.Run("between faces", func(t *testing.T) {
				// Count the Err calls of the whole call under a context that
				// never stops, then stop at the last one: the second face's
				// embedding, after the first face's has been staged.
				count := &countingContext{Context: context.Background()}
				if err := add(NewDocument(), count, two(), fonts.SubsetLimits{}); err != nil {
					t.Fatal(err)
				}
				n := count.calls.Load()
				if n < 3 {
					t.Fatalf("the call asked its context %d times; one per face and one at the start is the least", n)
				}
				unchanged(t, two(), &countingContext{Context: context.Background(), done: n}, fonts.SubsetLimits{}, context.Canceled)
			})
			t.Run("same", func(t *testing.T) {
				plain, bounded := NewDocument(), NewDocument()
				if err := add(plain, nil, one(), fonts.SubsetLimits{}); err != nil {
					t.Fatal(err)
				}
				if err := add(bounded, context.Background(), one(), fonts.SubsetLimits{}); err != nil {
					t.Fatal(err)
				}
				// Object for object: the trailer's /ID is random per document.
				if len(plain.Objects) != len(bounded.Objects) {
					t.Fatalf("%d objects against %d", len(plain.Objects), len(bounded.Objects))
				}
				for num, o := range plain.Objects {
					if b, ok := bounded.Objects[num]; !ok || !object.Equal(o.Value, b.Value) {
						t.Errorf("object %d differs under a live context", num)
					}
				}
			})
		})
	}
}
