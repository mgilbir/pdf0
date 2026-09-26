package core

import (
	"fmt"
	"strconv"

	"github.com/mgilbir/pdf0/object"
)

// The content-state interpreter: what a document's executed content actually
// paints with, and which fonts it actually shows text in, computed by running
// the content with a graphics state rather than by scanning it for words.
//
// Two questions were answered by boolean scans that ignored q/Q, operator
// order and inheritance (audit 2026-09-22 C73, C74, C153):
//
//   - device colour: "q 3 Tr Q" hid a visible font; `sh` counted as painting
//     in DeviceGray, though a shading paints in its own space; a form, a
//     tiling pattern or a Type 3 glyph was assumed to start in DeviceGray,
//     though it inherits the colour of whoever invokes it (ISO 32000-2
//     8.10.1); a stroke in the initial colour went unseen once a fill colour
//     was set; and every Type 3 font in a resource dictionary was scanned
//     whether or not any text used it, while the XObjects its glyphs drew were
//     never counted;
//   - font usage: the render mode was a running value that Q did not restore,
//     and a form started with no font and mode 0 whatever its caller had set.
//
// The interpreter keeps the part of the graphics state these questions need:
// a q/Q stack; for fill and stroke, the colour — which device families
// painting with it uses, or the pattern it is; the text render mode; and the
// current font. It follows the executed-content model the veraPDF corpus
// established: only what the content invokes counts — a form by Do, a pattern
// by painting with it, a Type 3 glyph by showing its code, a shading by sh.
//
// Invocation follows the specification's inheritance:
//
//   - a form XObject starts in the graphics state in effect at its Do (8.10.1);
//   - a tiling pattern's cell starts in the state in effect at the beginning of
//     the content stream in which the pattern was selected (8.7.3.1), and an
//     uncoloured one paints in the colour given with it, in the underlying
//     colour space (8.7.3.3);
//   - a Type 3 glyph description starts in the state in effect where the text
//     is shown (9.6.4), and after d1 its colour operators are ignored;
//   - an annotation appearance stream starts in the initial graphics state.
//
// A device colour space is masked by a Default* colour space in the resource
// dictionary in scope where the colour space is selected (8.6.5.6), so each
// colour carries its families already masked, and a form painting in its
// caller's colour is judged by the caller's Default* spaces, not its own.
//
// Results are memoised per (content stream, resources, inherited state, kind):
// that tuple determines everything the execution does, so a form shared by
// many pages in the same state runs once. A cycle — an execution reaching one
// of its own callers still in progress — contributes nothing at the back edge,
// which is the right answer for the caller the cycle closes on (its usage is a
// union, and it already includes its own), but not for the executions between:
// their results lack the caller's usage, so they are not memoised, and the one
// the cycle closes on is (audit 2026-09-22 C147 was a memo that stored the
// former).

// devSet is a set of device colour families.
type devSet uint8

const (
	devRGB devSet = 1 << iota
	devCMYK
	devGray
)

func devSetOf(rgb, cmyk, gray bool) devSet {
	var s devSet
	if rgb {
		s |= devRGB
	}
	if cmyk {
		s |= devCMYK
	}
	if gray {
		s |= devGray
	}
	return s
}

func (s devSet) split() (rgb, cmyk, gray bool) {
	return s&devRGB != 0, s&devCMYK != 0, s&devGray != 0
}

// paint is a fill or stroke colour: the device families painting with it uses
// (already masked by the Default* spaces where it was selected), or the
// pattern it is.
type paint struct {
	dev       devSet
	isPattern bool
	// For a pattern colour: the pattern (a tiling pattern stream, or a
	// shading pattern dictionary), the families of the underlying space an
	// uncoloured pattern paints in, and the state its cell starts in (an
	// index into the engine's interned states).
	tiling   *object.Stream
	shading  *object.Dictionary
	under    devSet
	patEntry int32
}

// gstate is the part of the graphics state the interpreter keeps. It is
// comparable: it is part of the memo key.
type gstate struct {
	fill, stroke paint
	mode         int8
	font         *object.Dictionary
	fontNum      int
}

// initialState is the initial graphics state of a content stream whose
// resource scope has the Default* spaces in defaults: DeviceGray black for
// fill and stroke (8.4.1), text render mode 0, no font.
func initialState(defaults devSet) gstate {
	p := paint{dev: devGray &^ defaults, patEntry: -1}
	return gstate{fill: p, stroke: p}
}

type execKind uint8

const (
	execPage execKind = iota
	execForm
	execPattern
	execCharProc
)

// resKey identifies a resource dictionary by the sub-dictionaries the
// interpreter reads. Two pages whose /Resources are different dictionaries
// referring to the same /Font, /XObject … dictionaries execute a shared
// content stream identically, and must share its memo entry.
type resKey struct {
	font, xobj, cs, pat, sh, gs *object.Dictionary
}

type execKey struct {
	stream *object.Stream
	res    resKey
	entry  gstate
	kind   execKind
	// annot marks content reached from an annotation appearance stream. Font
	// usage is not recorded there (it never was), so the same stream reached
	// from a page must not share its entry.
	annot bool
}

// Bounds on the interpreter's own work. A form nested deeper than
// maxExecDepth is not entered, and a run that has executed maxExecs content
// streams, or interpreted execBytesFactor times the run's decoded-content
// budget (memo hits are free), executes no more; each is reported
// (GuardContentState). Real documents are far inside all three: forms nest a
// few deep, and a stream is executed once per distinct inherited state. The
// byte bound is what stops a file that makes executions unmemoisable — cycles
// through a branching chain of forms — from re-reading large streams without
// end.
const (
	maxExecDepth    = 64
	maxExecs        = 1 << 20
	execBytesFactor = 4
	// maxQDepth bounds the q/Q stack. Pushes past it are counted, not
	// stored, so a stream of a million q costs a counter, and Q still pairs
	// with the right q.
	maxQDepth = 256
)

// GuardContentState is the interpreter's work bound.
const GuardContentState = "content-state-work" // maxExecDepth, maxExecs; see interp.go

// contentEngine executes content for one run. It lives in the run's memo slot
// when there is a run, so the device-colour rules of every page and the
// font-usage walk share one set of executions.
type contentEngine struct {
	doc      View
	noMemo   bool
	memo     map[execKey]devSet
	inProg   map[execKey]int               // execution depth of each in-progress key
	pages    map[*object.Dictionary]devSet // page contents, by page
	annots   map[*object.Dictionary]devSet // page annotations, by page
	states   []gstate
	stateIdx map[gstate]int32
	execs    int
	bytes    int64
	depth    int
	tripped  bool

	fontUsage    map[*object.Dictionary]*FontTextUsage
	pendingFonts []*FontTextUsage // fonts with shown strings not yet decoded into Strings
	allPagesDone bool
	type3Enc     map[*object.Dictionary]map[byte]object.Name
}

type contentEngineSlot struct{}

func engineFor(doc View) *contentEngine {
	e := Slot[contentEngine](doc.Run, contentEngineSlot{})
	if e.memo == nil {
		*e = contentEngine{
			doc:       doc,
			memo:      map[execKey]devSet{},
			inProg:    map[execKey]int{},
			pages:     map[*object.Dictionary]devSet{},
			annots:    map[*object.Dictionary]devSet{},
			stateIdx:  map[gstate]int32{},
			fontUsage: map[*object.Dictionary]*FontTextUsage{},
			type3Enc:  map[*object.Dictionary]map[byte]object.Name{},
		}
	}
	return e
}

func (e *contentEngine) intern(s gstate) int32 {
	if i, ok := e.stateIdx[s]; ok {
		return i
	}
	i := int32(len(e.states))
	e.states = append(e.states, s)
	e.stateIdx[s] = i
	return i
}

func (e *contentEngine) trip(detail string) {
	if !e.tripped {
		e.tripped = true
		e.doc.Note(GuardContentState, detail+"; the device-colour and font-usage checks that depend on the content not executed were skipped", 0)
	}
}

func (e *contentEngine) resKeyOf(res *object.Dictionary) resKey {
	if res == nil {
		return resKey{}
	}
	d := e.doc
	return resKey{
		font: d.ResolveDict(res.Get("Font")),
		xobj: d.ResolveDict(res.Get("XObject")),
		cs:   d.ResolveDict(res.Get("ColorSpace")),
		pat:  d.ResolveDict(res.Get("Pattern")),
		sh:   d.ResolveDict(res.Get("Shading")),
		gs:   d.ResolveDict(res.Get("ExtGState")),
	}
}

// scopeDefaults is the device families a resource scope's Default* colour
// spaces substitute for.
func scopeDefaults(rk resKey) devSet {
	if rk.cs == nil {
		return 0
	}
	var s devSet
	for k := range rk.cs.Keys() {
		switch k {
		case "DefaultRGB":
			s |= devRGB
		case "DefaultCMYK":
			s |= devCMYK
		case "DefaultGray":
			s |= devGray
		}
	}
	return s
}

// csFamilies is the device families a colour space value uses: itself, or
// the base or alternate of an Indexed, Separation, DeviceN or Pattern space.
func (e *contentEngine) csFamilies(cs object.Object) devSet {
	var r, c, g bool
	CheckCSForDevice(e.doc, cs, &r, &c, &g)
	return devSetOf(r, c, g)
}

// run executes one content stream and returns the device families it uses,
// masked as described above, and how complete that is: clean, or the
// depth of the shallowest caller still in progress that a cycle reached
// (the result lacks that caller's usage), or partial after a bound or
// cancellation. data is the decoded content; stream is its memo key, nil for a
// page's array of streams.
func (e *contentEngine) run(data []byte, stream *object.Stream, res *object.Dictionary, entry gstate, kind execKind, annot bool) (devSet, int) {
	rk := e.resKeyOf(res)
	key := execKey{stream: stream, res: rk, entry: entry, kind: kind, annot: annot}
	if stream != nil {
		if d, ok := e.inProg[key]; ok {
			return 0, d // a cycle back to the execution at depth d
		}
		if !e.noMemo {
			if r, ok := e.memo[key]; ok {
				return r, clean
			}
		}
	}
	if e.doc.Cancel.Stopped() {
		return 0, partial
	}
	if e.depth >= maxExecDepth {
		e.trip(fmt.Sprintf("content invokes forms, patterns or Type 3 glyphs nested more than %d deep", maxExecDepth))
		return 0, partial
	}
	if e.execs >= maxExecs {
		e.trip(fmt.Sprintf("executing the document's content needed more than %d content-stream executions", maxExecs))
		return 0, partial
	}
	if limit := execBytesFactor * e.doc.Limits.DecodedContentBytes; e.bytes+int64(len(data)) > limit {
		e.trip(fmt.Sprintf("executing the document's content needed more than %d bytes of interpretation, %d times the decoded-content budget", limit, execBytesFactor))
		return 0, partial
	}
	e.execs++
	e.bytes += int64(len(data))
	e.depth++
	depth := e.depth
	if stream != nil {
		e.inProg[key] = depth
		defer delete(e.inProg, key)
	}
	used, taint := func() (devSet, int) {
		defer func() { e.depth-- }()
		return e.interpret(data, res, rk, entry, kind, annot)
	}()
	if e.doc.Cancel.Stopped() {
		taint = partial // the lexer stopped early
	}
	if taint >= depth {
		// Clean, or a cycle that closed on this execution, whose usage is
		// then complete.
		taint = clean
		if stream != nil && !e.noMemo {
			e.memo[key] = used
		}
	}
	return used, taint
}

// Completeness of a run's result: clean, or partial (a bound or cancellation
// stopped it), or — any value between — the depth of the in-progress caller a
// cycle reached.
const (
	clean   = int(^uint(0) >> 1)
	partial = -1
)

// interpret is the execution loop of run.
func (e *contentEngine) interpret(data []byte, res *object.Dictionary, rk resKey, entry gstate, kind execKind, annot bool) (used devSet, taint int) {
	taint = clean
	d := e.doc
	defaults := scopeDefaults(rk)
	st := entry
	var stack []gstate
	overflow := 0
	d1 := false // after d1, a glyph description's colour operators are ignored

	// The operands the operators below read, kept as the lexer's raw bytes:
	// copying whole tokens, or decoding every string whether or not anything
	// reads it, was most of the interpreter's cost.
	var lastName, lastNum []byte
	var strs []rawString

	use := func(p paint) {
		if !p.isPattern {
			used |= p.dev
			return
		}
		r, c := e.paintPattern(p, defaults, annot)
		used |= r
		taint = min(taint, c)
	}
	// Selecting a device colour space is a use of it, painted with or not:
	// the corpus fails DeviceRGB set for stroking while the only text is
	// filled (6-2-4-3-t01-fail-s). Painting then uses the colour selected —
	// which is how the initial DeviceGray, and a colour inherited from a
	// caller, come to be used.
	device := func(fam devSet) paint {
		p := paint{dev: fam &^ defaults, patEntry: -1}
		used |= p.dev
		return p
	}
	selectCS := func(name string) paint {
		switch name {
		case "DeviceGray", "G":
			return device(devGray)
		case "DeviceRGB", "RGB":
			return device(devRGB)
		case "DeviceCMYK", "CMYK":
			return device(devCMYK)
		case "Pattern":
			return paint{isPattern: true, patEntry: -1}
		}
		if rk.cs == nil {
			return paint{patEntry: -1}
		}
		cs := rk.cs.Get(object.Name(name))
		if arr, ok := d.Resolve(cs).(object.Array); ok && len(arr) >= 1 {
			if fam, _ := d.ResolveName(arr[0]); fam == "Pattern" {
				p := paint{isPattern: true, patEntry: -1}
				if len(arr) >= 2 {
					p.under = e.csFamilies(arr[1]) &^ defaults
					used |= p.under
				}
				return p
			}
		}
		p := paint{dev: e.csFamilies(cs) &^ defaults, patEntry: -1}
		used |= p.dev
		return p
	}
	selectPattern := func(p *paint) {
		if !p.isPattern || lastName == nil || rk.pat == nil {
			return
		}
		p.tiling, p.shading = nil, nil
		switch v := d.Resolve(rk.pat.Get(object.Name(contentName(lastName)))).(type) {
		case *object.Stream:
			p.tiling = v
		case *object.Dictionary:
			p.shading = v
		}
		p.patEntry = e.intern(entry)
	}
	show := func() {
		if st.font != nil && !annot {
			u := e.fontUsage[st.font]
			if u == nil {
				u = &FontTextUsage{FontDict: st.font, ObjNum: st.fontNum, Modes: map[int]bool{}}
				e.fontUsage[st.font] = u
			}
			// Kept raw, and decoded when font usage is asked for: a run that
			// only wants device colour (PDF/X) never pays for it.
			if len(u.pending) == 0 {
				e.pendingFonts = append(e.pendingFonts, u)
			}
			u.pending = append(u.pending, strs...)
			u.Modes[int(st.mode)] = true
		}
		if st.font != nil && e.isType3(st.font) {
			// A Type 3 glyph is painted by its description, which runs in
			// this state; what it paints with is what it uses. Invisible text
			// paints nothing.
			if st.mode != 3 && st.mode != 7 {
				r, c := e.showType3(st, strs, res, annot)
				used |= r
				taint = min(taint, c)
			}
			return
		}
		switch st.mode {
		case 0, 4:
			use(st.fill)
		case 1, 5:
			use(st.stroke)
		case 2, 6:
			use(st.fill)
			use(st.stroke)
		}
	}

	lx := NewContentLexer(d.Cancel, data)
	var t ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case ContentName:
			lastName = t.Raw
			continue
		case ContentNumber:
			lastNum = t.Raw
			continue
		case ContentString, ContentHexString:
			strs = append(strs, rawString{t.Kind, t.Raw})
			continue
		case ContentArrayStart, ContentArrayEnd, ContentDictEnd:
			continue
		case ContentDictStart:
			lx.SkipDict(&t)
			continue
		case ContentInlineImage:
			used |= e.inlineImage(t.Params, rk, defaults, st, use)
			strs = strs[:0]
			continue
		}
		switch string(t.Raw) {
		case "q":
			if len(stack) < maxQDepth {
				stack = append(stack, st)
			} else {
				overflow++
			}
		case "Q":
			if overflow > 0 {
				overflow--
			} else if n := len(stack); n > 0 {
				st = stack[n-1]
				stack = stack[:n-1]
			}
		case "g", "rg", "k", "G", "RG", "K":
			if d1 {
				break
			}
			var fam devSet
			switch t.Raw[0] {
			case 'g', 'G':
				fam = devGray
			case 'r', 'R':
				fam = devRGB
			default:
				fam = devCMYK
			}
			if t.Raw[0] >= 'a' {
				st.fill = device(fam)
			} else {
				st.stroke = device(fam)
			}
		case "cs":
			if !d1 {
				st.fill = selectCS(contentName(lastName))
			}
		case "CS":
			if !d1 {
				st.stroke = selectCS(contentName(lastName))
			}
		case "sc", "scn":
			if !d1 {
				selectPattern(&st.fill)
			}
		case "SC", "SCN":
			if !d1 {
				selectPattern(&st.stroke)
			}
		case "d1":
			if kind == execCharProc {
				d1 = true
			}
		case "f", "F", "f*":
			use(st.fill)
		case "S", "s":
			use(st.stroke)
		case "B", "B*", "b", "b*":
			use(st.fill)
			use(st.stroke)
		case "sh":
			if rk.sh != nil {
				sh := d.Resolve(rk.sh.Get(object.Name(contentName(lastName))))
				used |= e.shadingFamilies(sh) &^ defaults
			}
		case "Tf":
			st.font, st.fontNum = nil, 0
			if rk.font != nil {
				ref := rk.font.Get(object.Name(contentName(lastName)))
				st.font = d.ResolveDict(ref)
				if st.font != nil {
					st.fontNum = object.RefNum(ref)
				}
			}
		case "Tr":
			if m, err := strconv.Atoi(string(lastNum)); err == nil && m >= 0 && m <= 7 {
				st.mode = int8(m)
			}
		case "Tj", "TJ", "'", "\"":
			show()
		case "gs":
			if rk.gs != nil {
				if gs := d.ResolveDict(rk.gs.Get(object.Name(contentName(lastName)))); gs != nil {
					if fa, ok := d.Resolve(gs.Get("Font")).(object.Array); ok && len(fa) >= 1 {
						st.font = d.ResolveDict(fa[0])
						st.fontNum = object.RefNum(fa[0])
					}
				}
			}
		case "Do":
			r, c := e.doXObject(contentName(lastName), rk, defaults, st, annot, use)
			used |= r
			taint = min(taint, c)
		}
		strs = strs[:0]
	}
	return used, taint
}

// rawString is a string operand as the lexer read it, decoded only when
// something reads it.
type rawString struct {
	kind ContentKind
	raw  []byte
}

// content is a stream's decoded content for execution. The reason is not
// needed here: content pdf0 declined to decode is executed as empty, which can
// only leave a use unfound — a device-colour or font rule then asserts nothing
// about it — and the producer has recorded the trip that says so.
func (e *contentEngine) content(s *object.Stream) []byte {
	data, _ := e.doc.Content(s) // reason: presence-only execution; the producer recorded any declined trip
	return data
}

// shadingFamilies is the device families a shading (dictionary or stream)
// paints in.
func (e *contentEngine) shadingFamilies(sh object.Object) devSet {
	switch v := e.doc.Resolve(sh).(type) {
	case *object.Dictionary:
		return e.csFamilies(v.Get("ColorSpace"))
	case *object.Stream:
		return e.csFamilies(v.Dict.Get("ColorSpace"))
	}
	return 0
}

// paintPattern paints with a pattern colour: a tiling pattern's cell is
// executed, a shading pattern paints in its shading's space.
func (e *contentEngine) paintPattern(p paint, defaults devSet, annot bool) (devSet, int) {
	d := e.doc
	if p.shading != nil {
		return e.shadingFamilies(d.Resolve(p.shading.Get("Shading"))) &^ defaults, clean
	}
	if p.tiling == nil {
		return 0, clean
	}
	entry := initialState(defaults)
	if p.patEntry >= 0 {
		entry = e.states[p.patEntry]
	}
	if pt, _ := d.ResolveInt(p.tiling.Dict.Get("PaintType")); pt == 2 {
		// An uncoloured pattern paints in the colour given with it, in the
		// underlying space; its own colour operators are not allowed.
		c := paint{dev: p.under, patEntry: -1}
		entry.fill, entry.stroke = c, c
	}
	return e.run(e.content(p.tiling), p.tiling, d.Resources(&p.tiling.Dict), entry, execPattern, annot)
}

// doXObject executes a Do: an image paints in its colour space (an image
// mask in the fill colour), a form runs in the current state.
func (e *contentEngine) doXObject(name string, rk resKey, defaults devSet, st gstate, annot bool, use func(paint)) (devSet, int) {
	d := e.doc
	if rk.xobj == nil {
		return 0, clean
	}
	s, ok := d.Resolve(rk.xobj.Get(object.Name(name))).(*object.Stream)
	if !ok {
		return 0, clean
	}
	switch sub, _ := d.ResolveName(s.Dict.Get("Subtype")); sub {
	case "Form":
		r, taint := e.run(e.content(s), s, d.Resources(&s.Dict), st, execForm, annot)
		return e.applyGroup(s, r), taint
	case "Image":
		if im, _ := d.ResolveBool(s.Dict.Get("ImageMask")); im {
			use(st.fill)
			return 0, clean
		}
		return e.csFamilies(s.Dict.Get("ColorSpace")) &^ defaults, clean
	}
	return 0, clean
}

// applyGroup applies a form's transparency group to what it uses: a device
// group colour space is itself a use, and an isolated group with a calibrated
// colour space covers the matching device families inside it. A non-isolated
// group composites against the backdrop, and the corpus fails DeviceRGB in a
// non-isolated CalRGB group.
func (e *contentEngine) applyGroup(s *object.Stream, used devSet) devSet {
	d := e.doc
	g := d.ResolveDict(s.Dict.Get("Group"))
	if g == nil {
		return used
	}
	used |= e.csFamilies(g.Get("CS"))
	if iso, _ := d.ResolveBool(g.Get("I")); bool(iso) {
		if cs := g.Get("CS"); cs != nil {
			used &^= devSetOf(ClassifyCalibratedCS(d, cs))
		}
	}
	return used
}

// inlineImage paints an inline image: in its colour space, or as a mask in the
// fill colour.
func (e *contentEngine) inlineImage(params []byte, rk resKey, defaults devSet, st gstate, use func(paint)) devSet {
	var used devSet
	mask := false
	for _, p := range ParseInlineImageParams(params) {
		switch p.Key {
		case "IM", "ImageMask":
			if len(p.Value) == 1 && p.Value[0].Is("true") {
				mask = true
			}
		case "CS", "ColorSpace":
			used |= e.inlineCSFamilies(p, rk) &^ defaults
		}
	}
	if mask {
		use(st.fill)
		return 0
	}
	return used
}

// inlineCSFamilies reads an inline image's colour space: a device name or its
// abbreviation, a named ColorSpace resource, or an Indexed array over either.
func (e *contentEngine) inlineCSFamilies(p InlineImageParam, rk resKey) devSet {
	name := func(t ContentTok) devSet {
		switch n := t.Name(); n {
		case "G", "DeviceGray":
			return devGray
		case "RGB", "DeviceRGB":
			return devRGB
		case "CMYK", "DeviceCMYK":
			return devCMYK
		default:
			if rk.cs != nil {
				return e.csFamilies(rk.cs.Get(object.Name(n)))
			}
		}
		return 0
	}
	if len(p.Value) == 0 {
		return 0
	}
	if !p.Array {
		if p.Value[0].Kind == ContentName {
			return name(p.Value[0])
		}
		return 0
	}
	// [/I base hival lookup] or [/Indexed …]: the base.
	if len(p.Value) >= 2 && p.Value[0].Kind == ContentName && p.Value[1].Kind == ContentName {
		if n := p.Value[0].Name(); n == "I" || n == "Indexed" {
			return name(p.Value[1])
		}
	}
	return 0
}

// showType3 runs the glyph descriptions a Type 3 font draws for the strings
// shown: each code's CharProc, in the state in effect where the text is shown.
func (e *contentEngine) showType3(st gstate, strs []rawString, res *object.Dictionary, annot bool) (devSet, int) {
	d := e.doc
	procs := d.ResolveDict(st.font.Get("CharProcs"))
	if procs == nil {
		return 0, clean
	}
	enc := e.type3Encoding(st.font)
	// A glyph description's resources are the font's; a font without its own
	// takes those of the content showing it (9.6.4).
	gres := d.ResolveDict(st.font.Get("Resources"))
	if gres == nil {
		gres = res
	}
	var used devSet
	taint := clean
	seen := map[byte]bool{}
	for _, rs := range strs {
		for _, code := range contentString(rs.kind, rs.raw) {
			if seen[code] {
				continue
			}
			seen[code] = true
			name, ok := enc[code]
			if !ok {
				continue
			}
			cp, ok := d.Resolve(procs.Get(name)).(*object.Stream)
			if !ok {
				continue
			}
			r, c := e.run(e.content(cp), cp, gres, st, execCharProc, annot)
			used |= r
			taint = min(taint, c)
		}
	}
	return used, taint
}

func (e *contentEngine) isType3(font *object.Dictionary) bool {
	sub, _ := e.doc.ResolveName(font.Get("Subtype"))
	return sub == "Type3"
}

// type3Encoding is a Type 3 font's code-to-glyph-name map, from its encoding
// dictionary's /Differences (9.6.5, which requires the complete encoding
// there).
func (e *contentEngine) type3Encoding(font *object.Dictionary) map[byte]object.Name {
	if m, ok := e.type3Enc[font]; ok {
		return m
	}
	d := e.doc
	m := map[byte]object.Name{}
	if enc := d.ResolveDict(font.Get("Encoding")); enc != nil {
		if diffs, ok := d.Resolve(enc.Get("Differences")).(object.Array); ok {
			code := -1
			for _, v := range diffs {
				switch x := d.Resolve(v).(type) {
				case object.Integer:
					code = int(x)
				case object.Name:
					if code >= 0 && code <= 255 {
						m[byte(code)] = x
					}
					if code >= 0 {
						code++
					}
				}
			}
		}
	}
	e.type3Enc[font] = m
	return m
}

// pageContent executes a page's content stream(s) from the initial state, once
// per page per run, recording the fonts it shows text in.
func (e *contentEngine) pageContent(page *object.Dictionary) devSet {
	if r, ok := e.pages[page]; ok {
		return r
	}
	d := e.doc
	res := d.Resources(page)
	var data []byte
	var key *object.Stream
	if c := page.Get("Contents"); c != nil {
		data, key, _ = d.ContentBytesAndKey(c) // reason: presence-only execution; declined content paints nothing here, and the producer recorded the trip
	}
	r, _ := e.run(data, key, res, initialState(scopeDefaults(e.resKeyOf(res))), execPage, false)
	e.pages[page] = r
	return r
}

// pageAnnotations is the device colour a page's annotations paint with: their
// appearance streams, each from the initial graphics state, and the artwork of
// a 3D annotation, which is not in its appearance stream (the 3D stream
// carries its own /ColorSpace; PDF/A-4e permits the annotation, not the
// unmanaged colour).
func (e *contentEngine) pageAnnotations(page *object.Dictionary) devSet {
	if r, ok := e.annots[page]; ok {
		return r
	}
	d := e.doc
	var used devSet
	appearance := func(s *object.Stream) {
		res := d.Resources(&s.Dict)
		r, _ := e.run(e.content(s), s, res, initialState(scopeDefaults(e.resKeyOf(res))), execForm, true)
		used |= r
	}
	annots, _ := d.Resolve(page.Get("Annots")).(object.Array)
	for _, aref := range annots {
		a := d.ResolveDict(aref)
		if a == nil {
			continue
		}
		if st, _ := d.ResolveName(a.Get("Subtype")); st == "3D" {
			if td, ok := d.Resolve(a.Get("3DD")).(*object.Stream); ok {
				used |= e.csFamilies(td.Dict.Get("ColorSpace"))
			} else if ref := d.ResolveDict(a.Get("3DD")); ref != nil {
				if s, ok := d.Resolve(ref.Get("3DD")).(*object.Stream); ok {
					used |= e.csFamilies(s.Dict.Get("ColorSpace"))
				}
			}
		}
		ap := d.ResolveDict(a.Get("AP"))
		if ap == nil {
			continue
		}
		for _, k := range []object.Name{"N", "R", "D"} {
			switch v := d.Resolve(ap.Get(k)).(type) {
			case *object.Stream:
				appearance(v)
			case *object.Dictionary:
				for sv := range v.Values() {
					if s, ok := d.Resolve(sv).(*object.Stream); ok {
						appearance(s)
					}
				}
			}
		}
	}
	e.annots[page] = used
	return used
}

// PageDeviceColourUse reports which device colour families a page paints
// with, by executing its content and its annotations' appearance streams
// (see the top of this file), after the Default* colour spaces in scope where
// each was selected. The page's transparency group /CS being a device space
// is a use too. Coverage by an output intent or the page group's calibrated
// /CS is the caller's question.
func PageDeviceColourUse(doc View, page *object.Dictionary) (usesRGB, usesCMYK, usesGray bool) {
	e := engineFor(doc)
	used := e.pageContent(page) | e.pageAnnotations(page)
	if g := doc.ResolveDict(page.Get("Group")); g != nil {
		used |= e.csFamilies(g.Get("CS"))
	}
	return used.split()
}

// PageDeviceColourUseNoMemo is PageDeviceColourUse with every execution run
// afresh, on an engine of its own. It is the oracle for the test that the
// memo is sound: the two must agree on every page of every file.
func PageDeviceColourUseNoMemo(doc View, page *object.Dictionary) (usesRGB, usesCMYK, usesGray bool) {
	run := doc
	run.Run = nil
	e := engineFor(run)
	e.noMemo = true
	used := e.pageContent(page) | e.pageAnnotations(page)
	if g := doc.ResolveDict(page.Get("Group")); g != nil {
		used |= e.csFamilies(g.Get("CS"))
	}
	return used.split()
}

// CollectFontTextUsage runs every page's content (and the forms, patterns and
// Type 3 glyphs it invokes) and reports which fonts show which text, in which
// render modes. Annotation appearance streams are not included.
func CollectFontTextUsage(doc View) map[*object.Dictionary]*FontTextUsage {
	return collectFontTextUsage(engineFor(doc))
}

// CollectFontTextUsageNoMemo is CollectFontTextUsage with no memo, the oracle
// for the memo-soundness test.
func CollectFontTextUsageNoMemo(doc View) map[*object.Dictionary]*FontTextUsage {
	run := doc
	run.Run = nil
	e := engineFor(run)
	e.noMemo = true
	return collectFontTextUsage(e)
}

func collectFontTextUsage(e *contentEngine) map[*object.Dictionary]*FontTextUsage {
	if !e.allPagesDone {
		if catalog := e.doc.Catalog(); catalog != nil {
			for _, page := range e.doc.Pages(catalog.Get("Pages")) {
				e.pageContent(page.Dict)
			}
		}
		e.allPagesDone = true
	}
	for _, u := range e.pendingFonts {
		for _, s := range u.pending {
			u.Strings = append(u.Strings, contentString(s.kind, s.raw))
		}
		u.pending = nil
	}
	e.pendingFonts = nil
	return e.fontUsage
}

// ContentExecutions reports how many content streams the run's interpreter
// has executed — memo hits are not executions. It exists for the tests that
// pin the sharing: a stream referenced by thousands of pages in the same state
// must run once.
func (r *Run) ContentExecutions() int {
	if r == nil {
		return 0
	}
	if e, ok := r.slots[contentEngineSlot{}].(*contentEngine); ok {
		return e.execs
	}
	return 0
}
