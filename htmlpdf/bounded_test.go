package htmlpdf

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
)

// RenderContext renders a document a caller does not control under a context
// and one budget for the shaping it costs (forme's layout.ComposeContext). It
// writes what Render writes, or, stopped, nothing.

func written(t *testing.T, r Result) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Document.Write(&buf); err != nil {
		t.Fatal(err)
	}
	// The file identifier is made afresh for each file written.
	return regexp.MustCompile(`/ID \[<[0-9A-F]+> <[0-9A-F]+>\]`).ReplaceAll(buf.Bytes(), nil)
}

func TestRenderContextIsRender(t *testing.T) {
	// A sheet long enough that the documents fit without being shrunk: the
	// document is one page, and Render refuses one shrunk past legibility.
	tall := Options{Page: PageSizePt(1200, 9000)}
	for _, tc := range []struct{ name, html string }{
		{"paragraphs", strings.Repeat("<p>office affluent नमस्ते क्षत्रिय <b>bold</b> <i>text</i></p>", 40)},
		// Layout shapes a paragraph as one run; a long one is admitted by
		// the default budget, which grows with the text (forme#923).
		{"a 20 KB paragraph", "<p>" + strings.Repeat("office affluent नमस्ते ", 600) + "</p>"},
		{"an 8 KB pre", "<pre>" + strings.Repeat(strings.Repeat("x", 79)+"\n", 100) + "</pre>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{HTML: tc.html, Fonts: notoSansSet(t)}
			plain, err := Render(in, tall)
			if err != nil {
				t.Fatal(err)
			}
			bounded, err := RenderContext(context.Background(), in, tall, RunLimits{})
			if err != nil {
				t.Fatalf("RenderContext: %v", err)
			}
			if !bytes.Equal(written(t, plain), written(t, bounded)) {
				t.Error("RenderContext wrote a different document from Render")
			}
			if bounded.ShapingWork <= 0 || plain.ShapingWork != 0 {
				t.Errorf("ShapingWork: RenderContext %d, Render %d", bounded.ShapingWork, plain.ShapingWork)
			}
			if len(bounded.Findings) != len(plain.Findings) || bounded.Scale != plain.Scale {
				t.Errorf("the results differ: %d and %d findings, scale %v and %v",
					len(bounded.Findings), len(plain.Findings), bounded.Scale, plain.Scale)
			}
		})
	}
}

func TestRenderContextStops(t *testing.T) {
	html := "<p>" + strings.Repeat("office affluent ", 200) + "</p>"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		limits RunLimits
		want   error
	}{
		{"cancelled", cancelled, RunLimits{}, context.Canceled},
		{"work past a limit", context.Background(), RunLimits{MaxWork: 50}, ErrRunLimit},
		{"a paragraph past a set input limit", context.Background(), RunLimits{MaxInputBytes: 1024}, ErrRunLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderContext(tc.ctx, Input{HTML: html, Fonts: notoSansSet(t)}, Options{}, tc.limits)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if out.Document != nil || out.Findings != nil || out.ShapingWork != 0 {
				t.Errorf("a stopped render returned %+v", out)
			}
		})
	}
}

// TestRenderContextRefusesAsRenderDoes: a document the engine refuses is
// refused the same way, with the same findings, whichever call renders it.
func TestRenderContextRefusesAsRenderDoes(t *testing.T) {
	in := Input{HTML: `<p>漢字</p>`, Fonts: notoSansSet(t)} // the face has no CJK
	_, plainErr := Render(in, Options{})
	out, err := RenderContext(context.Background(), in, Options{}, RunLimits{})
	var a, b *RefusedError
	if !errors.As(plainErr, &a) || !errors.As(err, &b) {
		t.Fatalf("Render: %v; RenderContext: %v", plainErr, err)
	}
	if len(a.Findings) != len(b.Findings) || out.Document != nil {
		t.Errorf("Render refused with %d findings, RenderContext with %d", len(a.Findings), len(b.Findings))
	}
}

// errCounter is a live context that counts its Err calls and reports itself
// cancelled from the done-th on (never, for done 0).
type errCounter struct {
	context.Context
	done, calls int
}

func (c *errCounter) Err() error {
	c.calls++
	if c.done > 0 && c.calls >= c.done {
		return context.Canceled
	}
	return nil
}

// TestRenderContextReachesEmbedding (#420): the context RenderContext is given
// is the one the page's fonts are embedded under, not only the one layout
// composes under. Composition asks its context a number of times the same
// input always comes to; a context that turns done on the next ask after
// those — the page's, as AddPageContext embeds its faces — stops the render
// with its error and no document, where one that composition alone saw would
// have let the page be written.
func TestRenderContextReachesEmbedding(t *testing.T) {
	in := Input{HTML: "<p>office affluent</p>", Fonts: notoSansSet(t)}
	opts := Options{Page: A4}
	composeAsks := func() int {
		c := &errCounter{Context: context.Background()}
		if _, err := layout.ComposeContext(c, opts.layoutInput(in), opts.layoutOptions(), RunLimits{}); err != nil {
			t.Fatal(err)
		}
		return c.calls
	}
	n := composeAsks()
	if again := composeAsks(); again != n {
		t.Fatalf("composition asked its context %d times, then %d: the count is not the input's", n, again)
	}
	all := &errCounter{Context: context.Background()}
	if _, err := RenderContext(all, in, opts, RunLimits{}); err != nil {
		t.Fatal(err)
	}
	if all.calls <= n {
		t.Fatalf("the render asked its context %d times, composition alone %d: nothing after composition asks it", all.calls, n)
	}
	out, err := RenderContext(&errCounter{Context: context.Background(), done: n + 1}, in, opts, RunLimits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a context done after composition: err = %v, want context.Canceled", err)
	}
	if out.Document != nil {
		t.Error("a render stopped while embedding returned a document")
	}
}
