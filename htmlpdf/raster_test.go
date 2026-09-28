package htmlpdf

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/core"
)

// Readers' pictures of a page, and a reader's model of its paths.
//
// The drawing operations forme 0.4.0 added are checked three ways: what the
// file says (the content stream read back, by the model below and by pdf0's
// own function evaluator), what forme says (Path.Contains, Gradient.ColorAt,
// which are its definitions), and what a renderer draws — Ghostscript and
// Poppler, which know nothing of either and are asked for the colour at a
// point.

// rendering is a page rendered by one renderer, with the page transform, so a
// point in layout's coordinates can be looked up.
type rendering struct {
	name string
	img  image.Image
	dpi  float64
	// height is the page height in points, since a raster's y goes down from
	// the top of the page and PDF's up from the bottom.
	height float64
	ps     pageSpace
}

// at is the colour a renderer drew at a point in layout's coordinates.
func (r rendering) at(x, y float64) color.RGBA {
	px, py := r.pixel(x, y)
	c := color.RGBAModel.Convert(r.img.At(px, py)).(color.RGBA)
	return c
}

// pixel is the device pixel a point of layout's coordinates falls in.
func (r rendering) pixel(x, y float64) (int, int) {
	p := r.ps.point(x, y)
	return int(math.Floor(p[0] * r.dpi / 72)), int(math.Floor((r.height - p[1]) * r.dpi / 72))
}

// centre is the middle of the device pixel a point of layout's coordinates
// falls in, in layout's coordinates: where a renderer evaluates a smooth
// colour for the whole pixel.
func (r rendering) centre(x, y float64) (float64, float64) {
	px, py := r.pixel(x, y)
	dx := (float64(px) + 0.5) * 72 / r.dpi
	dy := r.height - (float64(py)+0.5)*72/r.dpi
	return (dx - r.ps.tx) / r.ps.k, (r.ps.ty - dy) / r.ps.k
}

// renderings renders a document with every renderer on this machine: gs and
// pdftoppm. A test with none skips; a renderer that fails fails the test.
func renderings(t *testing.T, doc *pdf0.Document, c layout.Composed, dpi float64) []rendering {
	t.Helper()
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "page.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	height := c.Page.Height.Pt()
	var out []rendering
	res := fmt.Sprint(dpi)
	if gs, err := exec.LookPath("gs"); err == nil {
		ppm := filepath.Join(dir, "gs.ppm")
		if b, err := exec.Command(gs, "-q", "-dNOPAUSE", "-dBATCH", "-dSAFER", "-sDEVICE=ppmraw", "-dMaxBitmap=2147483647",
			"-dTextAlphaBits=1", "-dGraphicsAlphaBits=1", "-r"+res, "-sOutputFile="+ppm, path).CombinedOutput(); err != nil {
			t.Fatalf("gs: %v\n%s", err, b)
		}
		out = append(out, rendering{"gs", readPPM(t, ppm), dpi, height, pageSpaceOf(c)})
	}
	if pp, err := exec.LookPath("pdftoppm"); err == nil {
		prefix := filepath.Join(dir, "pp")
		// Vector anti-aliasing stays on: with it off, Poppler 24.02 paints a
		// shading (sh) at full opacity whatever its alpha.
		if b, err := exec.Command(pp, "-r", res, "-aa", "no", "-singlefile", path, prefix).CombinedOutput(); err != nil {
			t.Fatalf("pdftoppm: %v\n%s", err, b)
		}
		out = append(out, rendering{"pdftoppm", readPPM(t, prefix+".ppm"), dpi, height, pageSpaceOf(c)})
	}
	if len(out) == 0 {
		t.Skip("neither Ghostscript nor Poppler is on this machine; nothing is rendered")
	}
	return out
}

// readPPM reads a binary PPM (P6), which both renderers write.
func readPPM(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	// The header is four tokens separated by white space, and may carry
	// comments from a '#' to the end of a line (Ghostscript writes one).
	var tokens []string
	var tok []byte
	for len(tokens) < 4 {
		c, err := r.ReadByte()
		if err != nil {
			t.Fatalf("%s: a short PPM header: %v", path, err)
		}
		switch {
		case c == '#':
			if _, err := r.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if len(tok) > 0 {
				tokens, tok = append(tokens, string(tok)), nil
			}
		default:
			tok = append(tok, c)
		}
	}
	var w, h, max int
	if _, err := fmt.Sscan(strings.Join(tokens[1:], " "), &w, &h, &max); err != nil || tokens[0] != "P6" || max != 255 {
		t.Fatalf("%s is not an 8-bit binary PPM: %v %q", path, err, tokens)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	px := make([]byte, 3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if _, err := io.ReadFull(r, px); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			img.Set(x, y, color.RGBA{px[0], px[1], px[2], 255})
		}
	}
	return img
}

// near reports whether two colours agree to within tol in every channel.
func near(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}

// writtenPath is a path as a content stream builds and paints it: its
// subpaths flattened to polygons, and the operator that painted it.
type writtenPath struct {
	polys [][][2]float64
	op    string // f, f*, W n, W* n, and the like
	// curves are the Béziers, each as its four points, for a test that
	// measures them.
	curves [][4][2]float64
}

// writtenPaths is every path a content stream paints or clips with, in
// order, in the coordinates it is written in.
func writtenPaths(t *testing.T, stream []byte) []writtenPath {
	t.Helper()
	var (
		out      []writtenPath
		cur      writtenPath
		poly     [][2]float64
		pt       [2]float64
		operands []core.ContentToken
		clip     string
	)
	flush := func() {
		if len(poly) > 0 {
			cur.polys = append(cur.polys, poly)
			poly = nil
		}
	}
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		n := len(operands)
		num := func(i int) float64 { return operands[i].Number() }
		switch tk.Op {
		case "m":
			flush()
			pt = [2]float64{num(n - 2), num(n - 1)}
			poly = [][2]float64{pt}
		case "l":
			pt = [2]float64{num(n - 2), num(n - 1)}
			poly = append(poly, pt)
		case "c":
			p0 := pt
			p1 := [2]float64{num(n - 6), num(n - 5)}
			p2 := [2]float64{num(n - 4), num(n - 3)}
			p3 := [2]float64{num(n - 2), num(n - 1)}
			cur.curves = append(cur.curves, [4][2]float64{p0, p1, p2, p3})
			for i := 1; i <= 32; i++ {
				poly = append(poly, bezier(p0, p1, p2, p3, float64(i)/32))
			}
			pt = p3
		case "h":
			if len(poly) > 0 {
				pt = poly[0]
			}
		case "re":
			flush()
			x, y, w, h := num(n-4), num(n-3), num(n-2), num(n-1)
			cur.polys = append(cur.polys, [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}})
		case "W", "W*":
			clip = tk.Op
		case "f", "F", "f*", "n", "S", "B", "B*", "b", "b*", "s":
			flush()
			cur.op = tk.Op
			if clip != "" {
				cur.op = clip + " " + tk.Op
			}
			if len(cur.polys) > 0 {
				out = append(out, cur)
			}
			cur, clip = writtenPath{}, ""
		}
		operands = operands[:0]
	}
	return out
}

func bezier(p0, p1, p2, p3 [2]float64, t float64) [2]float64 {
	u := 1 - t
	var out [2]float64
	for i := range out {
		out[i] = u*u*u*p0[i] + 3*u*u*t*p1[i] + 3*u*t*t*p2[i] + t*t*t*p3[i]
	}
	return out
}

// evenOdd reports whether a point is inside polygons by the even-odd rule.
func evenOdd(polys [][][2]float64, x, y float64) bool {
	in := false
	for _, p := range polys {
		for i := range p {
			a, b := p[i], p[(i+1)%len(p)]
			if (a[1] > y) != (b[1] > y) && x < a[0]+(y-a[1])/(b[1]-a[1])*(b[0]-a[0]) {
				in = !in
			}
		}
	}
	return in
}

// nonzero reports whether a point is inside polygons by the nonzero winding
// rule.
func nonzero(polys [][][2]float64, x, y float64) bool {
	w := 0
	for _, p := range polys {
		for i := range p {
			a, b := p[i], p[(i+1)%len(p)]
			if (a[1] > y) != (b[1] > y) && x < a[0]+(y-a[1])/(b[1]-a[1])*(b[0]-a[0]) {
				if b[1] > a[1] {
					w++
				} else {
					w--
				}
			}
		}
	}
	return w != 0
}

// streamOps is the operators of a content stream, in order, for a test that
// looks for one.
func streamOps(t *testing.T, stream []byte) []string {
	t.Helper()
	var out []string
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		if tk.Kind == core.KindOp {
			out = append(out, tk.Op)
		}
	}
	return out
}

func joinOps(ops []string) string { return " " + strings.Join(ops, " ") + " " }
