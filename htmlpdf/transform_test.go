package htmlpdf

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/forme/style"
	"github.com/mgilbir/pdf0/object"
)

// transformGroups counts the TransformGroups in a display list, a group inside
// another among them.
func transformGroups(ops []layout.Op) int {
	n := 0
	for _, op := range ops {
		switch v := op.(type) {
		case layout.TransformGroup:
			n += 1 + transformGroups(v.Ops)
		case layout.ClipPath:
			n += transformGroups(v.Ops)
		case layout.FilterGroup:
			n += transformGroups(v.Ops)
		}
	}
	return n
}

// cssTurn is CSS's rotate(deg) as a matrix about the origin, in layout's
// coordinates, where y goes down and a positive angle turns clockwise.
func cssTurn(deg float64) [6]float64 {
	s, c := math.Sincos(deg * math.Pi / 180)
	return [6]float64{c, s, -s, c, 0, 0}
}

// halves is a box whose left half is red and right half blue, so that where a
// transform puts each half says which way it turned and whether it mirrored.
func halves(style string) string {
	return `<div style="` + style + `; display:flex">` +
		`<div style="flex:1; background:rgb(255,0,0)"></div><div style="flex:1; background:rgb(0,0,255)"></div></div>`
}

var (
	red   = color.RGBA{255, 0, 0, 255}
	blue  = color.RGBA{0, 0, 255, 255}
	white = color.RGBA{255, 255, 255, 255}
)

// TestATransformAtAnyAngleIsDrawnThroughItsMatrix: a turn, a skew, a mirror, a
// turn inside a turn and a turn cut by the box around it each put every pixel
// of a two-coloured box where CSS Transforms 1 says — the point p is drawn in
// the colour of the box's point M⁻¹(p − origin) — in Ghostscript and in
// Poppler, with the display list holding the TransformGroups that drew it.
func TestATransformAtAnyAngleIsDrawnThroughItsMatrix(t *testing.T) {
	const box = "position:absolute; left:100px; top:100px; width:200px; height:60px"
	origin := [2]float64{200, 130}
	skew := math.Tan(20 * math.Pi / 180)
	for _, tc := range []struct {
		name   string
		html   string
		m      [6]float64 // about origin
		groups int
		// clip, when set, is the box around the transformed one, which cuts
		// it: [xMin yMin xMax yMax].
		clip *[4]float64
	}{
		{name: "rotate(30deg)", html: halves(box + "; transform: rotate(30deg)"), m: cssTurn(30), groups: 1},
		{name: "rotate(-140deg)", html: halves(box + "; transform: rotate(-140deg)"), m: cssTurn(-140), groups: 1},
		{name: "skewX(20deg)", html: halves(box + "; transform: skewX(20deg)"), m: [6]float64{1, 0, skew, 1, 0, 0}, groups: 1},
		{
			name: "scaleX(-1) rotate(20deg)", html: halves(box + "; transform: scaleX(-1) rotate(20deg)"),
			m: concatMatrix(cssTurn(20), [6]float64{-1, 0, 0, 1, 0, 0}), groups: 1,
		},
		{
			name: "rotate(15deg) inside rotate(15deg)",
			html: `<div style="` + box + `; transform: rotate(15deg)">` +
				halves("width:200px; height:60px; transform: rotate(15deg)") + `</div>`,
			m: cssTurn(30), groups: 2,
		},
		{
			name: "rotate(30deg) cut by overflow:hidden",
			html: `<div style="` + box + `; overflow:hidden">` +
				halves("width:200px; height:60px; transform: rotate(30deg)") + `</div>`,
			m: cssTurn(30), groups: 1, clip: &[4]float64{100, 100, 300, 160},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{HTML: tc.html}
			c := layout.Compose(in, drawing(Options{}))
			if n := transformGroups(c.Ops); n != tc.groups {
				t.Fatalf("the display list holds %d TransformGroups; the case needs %d", n, tc.groups)
			}
			out, err := Render(in, Options{})
			if err != nil {
				t.Fatal(err)
			}
			inv := invertMatrix(tc.m)
			const margin = 2.5 // pixels kept from every edge, where a renderer's coverage rule decides
			checked := 0
			for _, r := range renderings(t, out.Document, c, 96) {
				bad := 0
				for y := origin[1] - 160; y <= origin[1]+160; y += 3 {
					for x := origin[0] - 160; x <= origin[0]+160; x += 3 {
						dx, dy := x-origin[0], y-origin[1]
						qx, qy := inv[0]*dx+inv[2]*dy, inv[1]*dx+inv[3]*dy
						dist := math.Min(math.Min(100-math.Abs(qx), 30-math.Abs(qy)), math.Abs(qx))
						if math.Abs(dist) < margin {
							continue
						}
						want := white
						if dist > 0 {
							want = blue
							if qx < 0 {
								want = red
							}
						}
						if k := tc.clip; k != nil {
							in := math.Min(math.Min(x-k[0], k[2]-x), math.Min(y-k[1], k[3]-y))
							if math.Abs(in) < margin {
								continue
							}
							if in < 0 {
								want = white
							}
						}
						checked++
						if got := r.at(x, y); !near(got, want, 3) {
							if bad++; bad <= 5 {
								t.Errorf("%s draws (%v, %v) %v; want %v", r.name, x, y, got, want)
							}
						}
					}
				}
				if bad > 5 {
					t.Errorf("%s: %d points in all are wrong", r.name, bad)
				}
			}
			if checked < 1000 {
				t.Errorf("only %d points were checked", checked)
			}
		})
	}
}

// TestATransformGroupTurnsPatternsAndFormsWithIt: a box with a tiled gradient
// and an opacity group inside it, turned a quarter and moved, is drawn the
// same through a TransformGroup — where the picture in it, which forme does
// not turn itself, makes it one — as forme draws it without one, by turning
// the coordinates. A tiling pattern's /Matrix is stated against the page's
// default coordinates and not the group's, and a filter's form has its
// bounding box in the group's: without the group's matrix in each, the tiles
// would run the other way and the opacity group, which sits off the page
// until the move brings it on, would be cut away.
func TestATransformGroupTurnsPatternsAndFormsWithIt(t *testing.T) {
	const (
		left, top   = 800.0, 100.0
		width, high = 200.0, 60.0
	)
	box := `<div style="position:absolute; left:800px; top:100px; width:200px; height:60px; ` +
		`background: linear-gradient(to right, rgb(255,0,0), rgb(0,0,255)) 0 0 / 20px 20px; ` +
		`transform: translate(-700px, 0) rotate(90deg)">%s` +
		`<div style="filter: opacity(0.5); margin: 20px 0 0 120px; width:40px; height:30px; background: rgb(0,160,0)"></div></div>`
	picture := `<img src="half.png" style="position:absolute; left:0; top:0; width:10px; height:10px">`
	grouped := filterInput(t, strings.Replace(box, "%s", picture, 1))
	turned := filterInput(t, strings.Replace(box, "%s", "", 1))

	cg := layout.Compose(grouped, drawing(Options{}))
	ct := layout.Compose(turned, drawing(Options{}))
	if n := transformGroups(cg.Ops); n != 1 {
		t.Fatalf("with the picture the display list holds %d TransformGroups; the case needs one", n)
	}
	if n := transformGroups(ct.Ops); n != 0 || len(ct.Findings) != 0 {
		t.Fatalf("without it the display list holds %d TransformGroups and the findings %v; "+
			"the case needs forme to turn the box itself", n, ct.Findings)
	}
	if cg.Scale != ct.Scale || cg.Page != ct.Page {
		t.Fatalf("the two pages differ: scale %v and %v", cg.Scale, ct.Scale)
	}
	og, err := Render(grouped, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ot, err := Render(turned, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rg, rt := renderings(t, og.Document, cg, 96), renderings(t, ot.Document, ct, 96)

	// The box's point (u, v), from its top left, is drawn at the page's
	// point origin + move + turn(u − w/2, v − h/2).
	cx, cy := left+width/2, top+high/2
	near2 := func(v, step float64) bool { // within 2px of a multiple of step
		m := math.Mod(v, step)
		return m < 2 || m > step-2
	}
	checked, inked := 0, 0
	for v := 1.0; v < high; v += 1.5 {
		for u := 1.0; u < width; u += 1.5 {
			switch {
			case near2(u, 20) || near2(v, 20) || u > width-2 || v > high-2:
				continue // a tile's seam, or the box's edge
			case u < 13 && v < 13:
				continue // the picture, in one page only
			case math.Abs(u-120) < 2 || math.Abs(u-160) < 2 || math.Abs(v-20) < 2 || math.Abs(v-50) < 2:
				continue // the opacity group's edge
			}
			du, dv := u-width/2, v-high/2
			x, y := cx-700-dv, cy+du
			for i := range rg {
				g, w := rg[i].at(x, y), rt[i].at(x, y)
				checked++
				if g != white {
					inked++
				}
				if !near(g, w, 6) {
					t.Errorf("%s draws the box's (%v, %v), at (%v, %v), %v through a group and %v without",
						rg[i].name, u, v, x, y, g, w)
					if t.Failed() && checked > 50 {
						return
					}
				}
			}
		}
	}
	if inked < checked*9/10 || checked < 1000 {
		t.Errorf("%d points checked, %d of them inked; the comparison saw too little of the box", checked, inked)
	}
}

// TestADropShadowIsDrawnInsideATransformGroup: a picture whose left half is
// transparent, with a sharp drop shadow, in a box turned 30° and moved, is
// drawn where the matrix puts it — the picture's right half blue, its shadow
// black at the offset beneath it, the rest white. The box sits off the sheet
// until the move brings it on, so the shadow's form, its mask and the fill
// through the mask, each sized to the sheet, are drawn only when the sheet is
// taken into the group's coordinates, the form's as well as the page's.
func TestADropShadowIsDrawnInsideATransformGroup(t *testing.T) {
	in := filterInput(t, `<div style="position:absolute; left:800px; top:100px; width:120px; height:60px; `+
		`transform: translate(-700px, 0) rotate(30deg)">`+
		`<img src="half.png" style="display:block; width:80px; height:40px; filter: drop-shadow(10px 10px 0 rgb(0,0,0))"></div>`)
	c := layout.Compose(in, drawing(Options{}))
	shadows := 0
	for _, op := range c.Ops {
		if g, ok := op.(layout.TransformGroup); ok {
			for _, op := range g.Ops {
				if f, ok := op.(layout.FilterGroup); ok && len(f.Filters) == 1 && f.Filters[0].Kind == layout.FilterDropShadow {
					shadows++
				}
			}
		}
	}
	if shadows != 1 {
		t.Fatalf("the display list holds %d drop-shadow groups inside a TransformGroup; the case needs one", shadows)
	}
	out, err := Render(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The box's point (u, v), from its top left, is drawn at the page's
	// origin + move + turn(u − 60, v − 30), the origin (860, 130).
	m := cssTurn(30)
	inv := invertMatrix(m)
	const margin = 2.5
	inside := func(u, v, x0, y0, x1, y1 float64) float64 {
		return math.Min(math.Min(u-x0, x1-u), math.Min(v-y0, y1-v))
	}
	checked, black := 0, 0
	for _, r := range renderings(t, out.Document, c, 96) {
		bad := 0
		for y := 30.0; y <= 230; y += 2 {
			for x := 60.0; x <= 260; x += 2 {
				dx, dy := x-(860-700), y-130
				u, v := inv[0]*dx+inv[2]*dy+60, inv[1]*dx+inv[3]*dy+30
				pic, shadow := inside(u, v, 40, 0, 80, 40), inside(u, v, 50, 10, 90, 50)
				if math.Abs(pic) < margin || math.Abs(shadow) < margin {
					continue
				}
				want := white
				switch {
				case pic > 0:
					want = blue
				case shadow > 0:
					want = color.RGBA{0, 0, 0, 255}
					black++
				}
				checked++
				if got := r.at(x, y); !near(got, want, 3) {
					if bad++; bad <= 5 {
						t.Errorf("%s draws (%v, %v), the box's (%.1f, %.1f), %v; want %v", r.name, x, y, u, v, got, want)
					}
				}
			}
		}
		if bad > 5 {
			t.Errorf("%s: %d points in all are wrong", r.name, bad)
		}
	}
	if checked < 1000 || black < 100 {
		t.Errorf("%d points checked, %d of them in the shadow; the case saw too little", checked, black)
	}
}

// TestATurnedRunStillExtracts: text turned at an angle is still the page's
// text.
func TestATurnedRunStillExtracts(t *testing.T) {
	in := Input{HTML: `<p style="position:absolute; left:100px; top:200px; margin:0; transform: rotate(30deg)">Turned text</p>`, Fonts: notoSansSet(t)}
	if n := transformGroups(layout.Compose(in, drawing(Options{})).Ops); n != 1 {
		t.Fatalf("the display list holds %d TransformGroups; the case needs one", n)
	}
	out, err := Render(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := out.Document.ExtractText()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Turned text") {
		t.Errorf("the page extracts as %q", text)
	}
}

// TestATransformPastWhatAReaderCarriesIsRefused: a scale of less than a
// millionth is refused under RuleUndrawable, its group and all in it; a
// matrix that is not invertible draws nothing, as CSS Transforms 1 §6 says;
// and a caller's TransformGroups of false does not stop a turn being drawn,
// since it says what this backend draws.
func TestATransformPastWhatAReaderCarriesIsRefused(t *testing.T) {
	in := Input{HTML: `<div style="position:absolute; left:100px; top:100px; width:200px; height:60px; ` +
		`background:red; transform: rotate(30deg) scale(0.0000001)"></div>`}
	_, err := Render(in, Options{})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("rendered (%v); want a refusal", err)
	}
	found := false
	for _, f := range refused.Findings {
		if f.Rule == RuleUndrawable && strings.Contains(f.Message, "millionfold") {
			found = true
		}
	}
	if !found {
		t.Errorf("refused with %+v; want %s about the scale", refused.Findings, RuleUndrawable)
	}

	for _, m := range [][6]float64{
		{1e-7, 0, 0, 1e-7, 0, 0}, {1e7, 0, 0, 1, 0, 0}, {1, 0, 0, 1, 2e9, 0},
		{math.NaN(), 0, 0, 1, 0, 0}, {1, 0, math.Inf(1), 1, 0, 0},
	} {
		if transformUndrawable(m) == "" {
			t.Errorf("transformUndrawable(%v) accepts it", m)
		}
	}
	for _, m := range [][6]float64{identityMatrix, cssTurn(30), {0, 0, 0, 0, 0, 0}, {1e-5, 0, 0, 1e5, 0, 0}} {
		if why := transformUndrawable(m); why != "" {
			t.Errorf("transformUndrawable(%v): %s", m, why)
		}
	}

	// A group that is not invertible, written as a literal: nothing of it
	// reaches the page.
	u := func(px float64) style.Unit { v, _ := style.FromPx(px); return v }
	fill := layout.FillRect{Rect: layout.Rect{X: u(10), Y: u(10), W: u(50), H: u(50)}, Color: style.RGBA{R: 255, A: 1}}
	page := PageSizePt(200, 200).WithMarginPt(0)
	for _, tc := range []struct {
		m    [6]float64
		want bool
	}{{identityMatrix, true}, {[6]float64{1, 0, 2, 0, 0, 0}, false}} {
		doc, err := writePage([]layout.Op{layout.TransformGroup{Matrix: tc.m, Ops: []layout.Op{fill}}}, page, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(contentOf(t, doc)), " re"); got != tc.want {
			t.Errorf("a group with the matrix %v draws its rectangle: %v; want %v", tc.m, got, tc.want)
		}
	}

	// A link inside a group, which forme does not make, is the rectangle
	// around where the matrix draws its area: a quarter turn and a move of
	// 50px take the area 20 × 10 at the origin to x −10…0 and y 0…20, moved to
	// x 40…50, which is [30 185 37.5 200] in points on this page.
	turn := [6]float64{0, 1, -1, 0, 50 / style.Unit(1).Px(), 0}
	link := layout.Link{Rects: []layout.Rect{{W: u(20), H: u(10)}}, Href: "https://example.com/"}
	group := layout.TransformGroup{Matrix: turn, Ops: []layout.Op{link}}
	if findings, refused := checkDrawable(layout.Composed{Ops: []layout.Op{group}}, nil); refused {
		t.Errorf("a link in a group is refused: %+v", findings)
	}
	doc, err := writePage([]layout.Op{group}, page, 1)
	if err != nil {
		t.Fatal(err)
	}
	annots, _ := doc.Resolve(doc.PageList()[0].Get("Annots")).(object.Array)
	if len(annots) != 1 {
		t.Fatalf("the page has %d link annotations; want one", len(annots))
	}
	rect, _ := doc.Resolve(doc.ResolveDict(annots[0]).Get("Rect")).(object.Array)
	want := []float64{30, 185, 37.5, 200}
	if len(rect) != 4 {
		t.Fatalf("the link's /Rect is %v", rect)
	}
	for i, v := range rect {
		var f float64
		switch n := doc.Resolve(v).(type) {
		case object.Integer:
			f = float64(n)
		case object.Real:
			f = float64(n)
		}
		if math.Abs(f-want[i]) > 0.01 { // the page height is in layout units, 1/64px
			t.Errorf("the link's /Rect is %v; want %v", rect, want)
			break
		}
	}
}

// TestMatrixArithmetic holds the matrix helpers to what they mean, point by
// point: concatMatrix(a, b) draws a point where a and then b draw it,
// invertMatrix undoes, and boundsOf holds every corner drawn through.
func TestMatrixArithmetic(t *testing.T) {
	apply := func(m [6]float64, x, y float64) (float64, float64) {
		return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
	}
	ms := [][6]float64{
		identityMatrix, cssTurn(30), cssTurn(-140),
		{1.5, 0.25, -0.75, 2, 10, -20}, {-1, 0, 0.3, 1, 5, 7}, {0.75, 0, 0, -0.75, 36, 800},
	}
	pts := [][2]float64{{0, 0}, {1, 0}, {0, 1}, {13, -7}, {-250, 410}}
	close := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*(1+math.Abs(a)+math.Abs(b)) }
	for _, a := range ms {
		inv := invertMatrix(a)
		for _, b := range ms {
			ab := concatMatrix(a, b)
			for _, p := range pts {
				x1, y1 := apply(a, p[0], p[1])
				x2, y2 := apply(b, x1, y1)
				if x, y := apply(ab, p[0], p[1]); !close(x, x2) || !close(y, y2) {
					t.Errorf("concatMatrix(%v, %v) draws %v at (%v, %v); a then b draw it at (%v, %v)", a, b, p, x, y, x2, y2)
				}
			}
		}
		for _, p := range pts {
			x, y := apply(a, p[0], p[1])
			if bx, by := apply(inv, x, y); !close(bx, p[0]) || !close(by, p[1]) {
				t.Errorf("invertMatrix(%v) takes %v back to (%v, %v)", a, p, bx, by)
			}
		}
		// stretches is the longest and shortest a unit vector is drawn.
		long, short := 0.0, math.Inf(1)
		for i := 0; i < 3600; i++ {
			s, c := math.Sincos(float64(i) * math.Pi / 1800)
			l := math.Hypot(a[0]*c+a[2]*s, a[1]*c+a[3]*s)
			long, short = math.Max(long, l), math.Min(short, l)
		}
		if most, least := stretches(a); math.Abs(most-long) > 1e-4*long || math.Abs(least-short) > 1e-4*long {
			t.Errorf("stretches(%v) = %v, %v; a unit vector is drawn %v to %v long", a, most, least, short, long)
		}
		box := [4]float64{-3, 5, 40, 17}
		got := boundsOf(a, box)
		for _, c := range [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[0], box[3]}, {box[2], box[3]}} {
			x, y := apply(a, c[0], c[1])
			if x < got[0]-1e-9 || x > got[2]+1e-9 || y < got[1]-1e-9 || y > got[3]+1e-9 {
				t.Errorf("boundsOf(%v, %v) = %v misses the corner at (%v, %v)", a, box, got, x, y)
			}
		}
	}
}

// TestABitmapStrikeIsTheOneTheTransformShowsTheTextAt: text in a bitmap face
// inside a TransformGroup is drawn from the strike for the size it is shown
// at, as text whose font size is that is. forme's EBDT fixture has a 1-bit
// strike at 12px and a 2-bit one at 16px; 9pt is 12px, and a third more is
// 16px — by a scale with the turn, by two groups each scaling a little, or by
// a stretch along one axis, which is the direction the strike is chosen for.
// A turn alone keeps the 12px strike, and one page with both draws both.
func TestABitmapStrikeIsTheOneTheTransformShowsTheTextAt(t *testing.T) {
	set := strikesSet(t)
	gid, _ := set.face.GlyphID('A')
	images := map[int]string{}
	for _, ppem := range []int{12, 16} {
		var c oneImageCapture
		if err := set.face.PaintGlyph(gid, shape.PaintOptions{PPEM: ppem}, &c); err != nil || !c.got {
			t.Fatalf("A has no image at %d ppem: %v", ppem, err)
		}
		images[ppem] = fmt.Sprintf("%d×%d", c.img.Width, c.img.Height)
	}
	if images[12] == images[16] {
		t.Fatalf("the two strikes draw A the same size, %s; the case cannot tell them apart", images[12])
	}
	a := func(transform string) string {
		return `<p style="margin:40px; color:#c00; font-size:9pt; transform:` + transform + `">A</p>`
	}
	for _, tc := range []struct {
		name   string
		html   string
		groups int
		want   []int // the strikes A is drawn from, by ppem
	}{
		{"a turn", a("rotate(10deg)"), 1, []int{12}},
		{"a turn and a scale", a("rotate(10deg) scale(1.3333)"), 1, []int{16}},
		{"two groups", `<div style="transform: rotate(5deg) scale(1.1547)">` + a("rotate(5deg) scale(1.1547)") + `</div>`, 2, []int{16}},
		{"a stretch along one axis", a("rotate(10deg) scaleX(1.3333)"), 1, []int{16}},
		{"one page, both", a("none") + a("rotate(10deg) scale(1.3333)"), 1, []int{12, 16}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{HTML: tc.html, Fonts: set}
			if n := transformGroups(layout.Compose(in, drawing(Options{})).Ops); n != tc.groups {
				t.Fatalf("the display list holds %d TransformGroups; the case needs %d", n, tc.groups)
			}
			doc, _ := roundTrip(t, in, Options{})
			var got []string
			res := doc.ResolveDict(doc.PageList()[0].Get("Resources"))
			for _, ref := range doc.ResolveDict(res.Get("Font")).All() {
				fd := doc.ResolveDict(ref)
				if doc.ResolveDict(fd.Get("CharProcs")).Get(object.Name("g"+strconv.Itoa(gid))) == nil {
					continue
				}
				for _, im := range doc.ResolveDict(doc.ResolveDict(fd.Get("Resources")).Get("XObject")).All() {
					st := doc.Resolve(im).(*object.Stream)
					if sm := st.Dict.Get("SMask"); sm != nil {
						st = doc.Resolve(sm).(*object.Stream)
					}
					got = append(got, fmt.Sprintf("%v×%v", st.Dict.Get("Width"), st.Dict.Get("Height")))
				}
			}
			var want []string
			for _, p := range tc.want {
				want = append(want, images[p])
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("A is drawn from images %v; the strikes %v ppem draw it %v", got, tc.want, want)
			}
		})
	}
}
