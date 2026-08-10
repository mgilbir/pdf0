package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/fonts"
)

// The faces a document brings with it.
//
// An @font-face rule is a document saying "here is a font, call it this". It is
// the only way a document can be set in a face the caller did not supply, and
// it is what makes an HTML-to-PDF engine able to render a page the way its
// author designed it rather than the way the fourteen standard PDF faces allow.
//
// # Why this is a resource and not a stylesheet matter
//
// The rule is written in CSS and everything interesting about it happens
// outside the cascade: it does not select anything, it does not compute a
// value, and it does not inherit. What it does is *load a file*, and that puts
// it in this package rather than in style, behind resource.go's policy in full —
// no scheme, no absolute path, no escape from the resolver's directory, and
// nothing at all when there is no resolver. There is no second loading path
// here that could disagree with the one <img> and <link rel=stylesheet> use.
//
// # Why the caps are tighter than the ones on an image
//
// The bytes go to fonts.Load, which parses an sfnt: a table directory, a
// character map, a set of outlines and — for anything with shaping — a stack of
// layout tables full of offsets into each other. That is a larger parser
// reading more attacker-controlled structure than a PNG decoder, and it is
// reached from a *stylesheet*, which is one indirection further from anything
// the caller wrote than an <img src> is.
//
// So a document gets a budget, in the shape image.go already uses: a cap on one
// file, a cap on how many faces it may end up with, a cap on the total bytes
// handed to the parser, and a cap on how many rules are looked at at all. The
// last is the one that answers a page declaring a thousand @font-face rules —
// the byte budget alone would not, because a rule whose file is missing costs a
// resolver call and no bytes, and a thousand of those is a thousand system
// calls the document did not have to pay for.

// maxFontBytes is the largest single font program this engine will parse.
//
// Eight megabytes is past every web font in use — a full-coverage Noto face is
// around five, a subset Latin one is tens of kilobytes — and small enough that
// one file cannot be the whole budget. It bounds what reaches fonts.Load, which
// is the point: the cap exists to bound a parser, not a download.
//
// A variable rather than a constant so a test can lower it and watch it fire. A
// cap nobody has seen trip is one nobody knows works.
var maxFontBytes = 8 << 20

// maxDocumentFontBytes is the total this engine will hand to the font parser
// for one document.
//
// A per-file cap does not bound a document: fifty files of eight megabytes are
// four hundred megabytes, and each one passes the per-file check. This is the
// budget that makes the total finite, and it is charged for bytes *read*,
// whether or not the parse then succeeded — a font program that fails to parse
// cost the same read and the same allocation as one that did not.
var maxDocumentFontBytes = 32 << 20

// maxDocumentFaces is how many faces one document may end up with.
//
// It bounds the number of font programs parsed, which is the expensive and
// dangerous half. A document needs a handful — a family in four weights is
// four — and one that names twenty is doing something other than setting text.
var maxDocumentFaces = 20

// maxFontFaceRules is how many @font-face rules one document's stylesheets may
// have looked at.
//
// This is the cap that answers the thousand-rule page. The two above bound the
// parsing and the reading; neither bounds a rule whose every src fails, which
// costs a resolver call apiece and no bytes at all. Fifty is more @font-face
// rules than any real document has and is a bound on the resolver traffic a
// stylesheet can direct.
var maxFontFaceRules = 50

// maxFontSources is how many entries of one rule's src list are tried.
//
// The list is a fallback chain and is meant to be short: a woff2, a woff, a
// ttf, and a local() before them. Sixteen is well past that and stops one rule
// from being a loop.
var maxFontSources = 16

// fontSource is one entry of an @font-face rule's src list.
type fontSource struct {
	// local marks a local(name) entry, whose name is looked up in the font set
	// the caller supplied rather than loaded from anywhere.
	local bool
	// ref is the reference a url() entry names, or the face name a local() one
	// does.
	ref string
	// format is the format() hint, lowercased, or empty when there was none.
	format string
}

// fontFaceRule is one @font-face, as far as this engine reads one.
type fontFaceRule struct {
	family string
	srcs   []fontSource

	// weightLow and weightHigh are the font-weight descriptor. A single weight
	// is a range of one, which is what makes the matching below uniform over
	// "font-weight: 700" and "font-weight: 100 900".
	weightLow, weightHigh float64
	italic                bool
}

// pendingFontFace is an @font-face rule and the stylesheet it was written in,
// carried from the pipeline to the loader so a finding can say where it came
// from.
type pendingFontFace struct {
	rule  css.Rule
	sheet string
}

// splitFontFaces separates the @font-face rules of one stylesheet from the rest.
//
// They are taken out of the rule list rather than left in it because the
// cascade's answer to an at-rule is to report it as one it does not apply, and
// after this file that report would be wrong. Removing them changes nothing
// else: an at-rule never contributed a declaration, so the ordering the cascade
// counts is untouched.
func splitFontFaces(rules []css.Rule, sheet string, faces *[]pendingFontFace) []css.Rule {
	if len(rules) == 0 {
		return rules
	}
	found := false
	for _, r := range rules {
		if isFontFace(r) {
			found = true
			break
		}
	}
	if !found {
		return rules
	}
	out := make([]css.Rule, 0, len(rules))
	for _, r := range rules {
		if isFontFace(r) {
			*faces = append(*faces, pendingFontFace{rule: r, sheet: sheet})
			continue
		}
		out = append(out, r)
	}
	return out
}

func isFontFace(r css.Rule) bool {
	return r.At && strings.EqualFold(r.Name, "font-face")
}

// documentFace is one loaded face together with what the rule said about it.
type documentFace struct {
	rule fontFaceRule
	face *fonts.Face
	// ref is the src entry that produced the face — the url for a url() entry,
	// the name for a local() one. It is kept so that a caller can say which
	// file a family came from.
	ref string
}

// documentFonts is the font set a document's own @font-face rules make, over
// the set the caller supplied.
//
// A family the document defined shadows one the caller has, which is what CSS
// says: an @font-face for "Helvetica" is the document's Helvetica for the rest
// of that document, whatever the system has under that name.
type documentFonts struct {
	base  FontSet
	faces []*documentFace
	// byFamily indexes faces by lowercased family, in declaration order.
	byFamily map[string][]*documentFace
}

// fallbackDocumentFonts is documentFonts over a base that answers the
// coverage question too.
//
// The two types exist so that wrapping a FallbackFontSet does not lose the
// interface and wrapping a plain FontSet does not invent one. A wrapper that
// implemented FaceFor unconditionally would answer "no" for every base that
// cannot, which reads to inline.go as a set that was asked and had nothing —
// the same outcome by luck rather than by construction, and the sort of thing
// that stops being the same the day the caller of FaceFor grows a second
// branch.
type fallbackDocumentFonts struct{ *documentFonts }

func (d fallbackDocumentFonts) FaceFor(text string, bold, italic bool) (*fonts.Face, bool) {
	// The document's own faces are deliberately not offered here. FaceFor is
	// the question "what can set this text at all", and answering it with a
	// face the document loaded for some *other* family would substitute a
	// webfont for a script it was never chosen for. The base set is the one
	// that was given coverage as its job.
	return d.base.(FallbackFontSet).FaceFor(text, bold, italic)
}

// Face answers for a family the document defined, and defers otherwise.
func (d *documentFonts) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	key := strings.ToLower(strings.TrimSpace(family))
	key = strings.Trim(key, `"'`)
	key = strings.TrimSpace(key)
	candidates := d.byFamily[key]
	if len(candidates) == 0 {
		return d.base.Face(family, bold, italic)
	}
	desired := 400.0
	if bold {
		desired = 700
	}
	var best *documentFace
	bestScore := 0.0
	for _, c := range candidates {
		score := faceScore(c.rule, desired, italic)
		// "<=" rather than "<", so that the last rule declared wins a tie. That
		// is the cascade's last term, and an @font-face redeclared later in a
		// document is a document replacing the earlier one.
		if best == nil || score <= bestScore {
			best, bestScore = c, score
		}
	}
	return best.face, true
}

// faceScore ranks one face against what was asked for; lower is better.
//
// Style dominates weight, which is CSS Fonts 4 §5's order: an italic request
// takes an italic face of the wrong weight over an upright one of the right
// weight, because the slant is the more visible difference.
func faceScore(r fontFaceRule, desired float64, italic bool) float64 {
	score := 0.0
	if r.italic != italic {
		score += 1e6
	}
	return score + weightRank(desired, clampWeight(desired, r.weightLow, r.weightHigh))
}

func clampWeight(desired, low, high float64) float64 {
	if desired < low {
		return low
	}
	if desired > high {
		return high
	}
	return desired
}

// weightRank is CSS Fonts 4 §5.2's weight matching, as a distance.
//
// The three cases are the specification's, and the reason it is not simply
// "closest number" is the middle one: at a desired weight of 400 or 500 a
// *heavier* face up to 500 is preferred to a lighter one, however much closer
// the lighter one is, because 400 and 500 are both "normal" and the step down
// to 300 is a visible change of face where the step up to 500 is not.
func weightRank(desired, w float64) float64 {
	if w == desired {
		return 0
	}
	switch {
	case desired >= 400 && desired <= 500:
		if w > desired && w <= 500 {
			return w - desired
		}
		if w < desired {
			return 1000 + (desired - w)
		}
		return 10000 + (w - 500)
	case desired < 400:
		if w < desired {
			return desired - w
		}
		return 1000 + (w - desired)
	default:
		if w > desired {
			return w - desired
		}
		return 1000 + (desired - w)
	}
}

// loadFontFaces turns a document's @font-face rules into the font set it is set
// in.
//
// base is what the caller supplied and is never nil by the time this is called.
// When nothing loaded, base is returned unchanged rather than wrapped — a
// wrapper with no faces in it would answer every question by delegating, and
// would drop the FallbackFontSet interface on the way for no gain.
func loadFontFaces(pending []pendingFontFace, res ResourceResolver, base FontSet, rec *Recorder) FontSet {
	if len(pending) == 0 {
		return base
	}
	l := &fontFaceLoader{
		res: res, rec: rec, base: base,
		loaded: map[string]*fonts.Face{},
		failed: map[string]bool{},
		budget: maxDocumentFontBytes,
	}
	set := &documentFonts{base: base, byFamily: map[string][]*documentFace{}}
	for _, p := range pending {
		if l.rules >= maxFontFaceRules {
			l.overRuleCap(p, len(pending))
			break
		}
		l.rules++
		rule, ok := l.parse(p)
		if !ok {
			continue
		}
		face, ref, ok := l.face(p, rule)
		if !ok {
			continue
		}
		df := &documentFace{rule: rule, face: face, ref: ref}
		set.faces = append(set.faces, df)
		key := strings.ToLower(rule.family)
		set.byFamily[key] = append(set.byFamily[key], df)
	}
	if len(set.faces) == 0 {
		return base
	}
	if _, ok := base.(FallbackFontSet); ok {
		return fallbackDocumentFonts{set}
	}
	return set
}

// fontFaceLoader loads the faces, under the caps.
type fontFaceLoader struct {
	res  ResourceResolver
	rec  *Recorder
	base FontSet

	// loaded memoizes by reference, so a document naming one file in four
	// @font-face rules reads and parses it once. Sharing a face between two
	// families within one document is right: a face records the glyphs it was
	// asked to show, and that record is per document.
	loaded map[string]*fonts.Face
	// failed records the references already reported, so a stylesheet with
	// twenty rules pointing at one missing file makes one attempt.
	failed map[string]bool

	// budget is how many bytes of font program the document may still read.
	budget int
	// faces counts the faces loaded, and rules the @font-face rules looked at.
	faces int
	rules int

	cappedFaces, cappedBytes, cappedRules bool
}

// at is the place a finding about one rule points to.
func (p pendingFontFace) at() Source {
	return Source{HTMLOffset: -1, CSSOffset: p.rule.Offset, Sheet: p.sheet}
}

// parse reads the descriptors of one @font-face rule.
//
// A rule missing either of the two descriptors that make it a font — the family
// it is called and the file it comes from — defines nothing, and that is
// reported as malformed CSS rather than as an unsupported feature: the author
// wrote a rule that cannot mean anything, which is a different thing from this
// engine not doing something.
func (l *fontFaceLoader) parse(p pendingFontFace) (fontFaceRule, bool) {
	out := fontFaceRule{weightLow: 400, weightHigh: 400}
	if !p.rule.HasBlock {
		l.rec.ReportDetail(Finding{
			Rule:     RuleInvalidCSS,
			Source:   p.at(),
			Message:  "@font-face has no block, so it declares no font",
			Property: "@font-face",
		})
		return out, false
	}
	decls, _, errs := css.ParseDeclarationValues(p.rule.Block)
	for _, e := range errs {
		l.rec.ReportDetail(Finding{
			Rule:    RuleInvalidCSS,
			Source:  Source{HTMLOffset: -1, CSSOffset: e.Offset, Sheet: p.sheet},
			Message: e.Message,
		})
	}

	for _, d := range decls {
		switch strings.ToLower(d.Name) {
		case "font-family":
			out.family = descriptorFamily(d.Value)
		case "src":
			out.srcs = l.parseSrc(p, d)
		case "font-weight":
			if low, high, ok := parseWeightDescriptor(d.Value); ok {
				out.weightLow, out.weightHigh = low, high
			} else {
				l.badDescriptor(p, d, "font-weight")
			}
		case "font-style":
			if italic, ok := parseStyleDescriptor(d.Value); ok {
				out.italic = italic
			} else {
				l.badDescriptor(p, d, "font-style")
			}
		case "unicode-range":
			l.unicodeRange(p, d)
		case "font-display":
			// A hint about what to show while a font is downloading. There is
			// no download here and no moment at which a page is half-drawn, so
			// there is nothing for it to change and nothing to report.
		default:
			// Every other descriptor changes how the face is used —
			// size-adjust and the override descriptors change its metrics
			// outright, font-feature-settings changes which glyphs are chosen.
			// Ignoring one silently would move the text on the page with
			// nothing saying so.
			l.rec.ReportDetail(Finding{
				Rule:     RuleUnsupportedProperty,
				Source:   Source{HTMLOffset: -1, CSSOffset: d.Offset, Sheet: p.sheet},
				Message:  "the @font-face descriptor " + quoteValue(d.Name) + " is not applied",
				Property: strings.ToLower(d.Name),
			})
		}
	}

	if out.family == "" {
		l.rec.ReportDetail(Finding{
			Rule:     RuleInvalidCSS,
			Source:   p.at(),
			Message:  "@font-face names no font-family, so nothing can ask for it",
			Property: "@font-face",
		})
		return out, false
	}
	if len(out.srcs) == 0 {
		l.rec.ReportDetail(Finding{
			Rule:   RuleInvalidCSS,
			Source: p.at(),
			Message: "@font-face for " + quoteValue(out.family) +
				" names no usable src, so there is no font to load",
			Property: "@font-face",
		})
		return out, false
	}
	return out, true
}

func (l *fontFaceLoader) badDescriptor(p pendingFontFace, d css.Declaration, name string) {
	l.rec.ReportDetail(Finding{
		Rule:     RuleInvalidCSS,
		Source:   Source{HTMLOffset: -1, CSSOffset: d.Offset, Sheet: p.sheet},
		Message:  "the @font-face descriptor " + quoteValue(name) + " is not a value this engine can read; the default was used",
		Property: name,
	})
}

// unicodeRange reads the descriptor and says what this engine does with it.
//
// What it does is not honour it, and the reason is structural rather than
// temporary: a unicode-range asks for a face to be used *for some characters
// and not others*, and this engine picks one face per box — see the note on
// FallbackFontSet in font.go, which is against the same wall for the same
// reason. Implementing it would mean cutting a run into per-face pieces through
// measurement, line breaking and the content stream.
//
// So the descriptor is parsed, which is what makes the report worth having: a
// range covering the whole of Unicode restricts nothing and is silently
// correct, and only a range that would really have excluded something is
// reported.
func (l *fontFaceLoader) unicodeRange(p pendingFontFace, d css.Declaration) {
	ranges, ok := parseUnicodeRange(d.Value)
	if !ok {
		l.badDescriptor(p, d, "unicode-range")
		return
	}
	if coversAllOfUnicode(ranges) {
		return
	}
	l.rec.ReportDetail(Finding{
		Rule:   RuleUnsupportedValue,
		Source: Source{HTMLOffset: -1, CSSOffset: d.Offset, Sheet: p.sheet},
		Message: "this @font-face restricts its face to " + quoteValue(unicodeRangeText(ranges)) +
			"; this engine chooses one face for a whole run, so the face was used for all of it",
		Property: "unicode-range",
	})
}

// parseSrc reads the src descriptor's list of alternatives.
//
// An entry this engine cannot read is dropped rather than invalidating the
// list, which is what CSS Fonts asks for: the list is a chain of alternatives
// written for readers with different capabilities, and one written for a reader
// this is not must not take the others down with it.
func (l *fontFaceLoader) parseSrc(p pendingFontFace, d css.Declaration) []fontSource {
	var out []fontSource
	for _, item := range splitOnComma(d.Value) {
		if len(out) >= maxFontSources {
			l.rec.ReportDetail(Finding{
				Rule:   RuleLimit,
				Source: Source{HTMLOffset: -1, CSSOffset: d.Offset, Sheet: p.sheet},
				Message: fmt.Sprintf("this @font-face offers more than the %d src entries "+
					"this engine will try; the rest were not tried", maxFontSources),
				Property: "src",
			})
			break
		}
		if s, ok := parseSrcEntry(item); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseSrcEntry reads one alternative of a src list.
func parseSrcEntry(vals []css.ComponentValue) (fontSource, bool) {
	var out fontSource
	have := false
	for _, v := range vals {
		switch {
		case v.IsToken() && v.Token.Kind == css.Whitespace:
			continue
		case v.IsToken() && v.Token.Kind == css.URL:
			if have {
				return fontSource{}, false
			}
			out = fontSource{ref: v.Token.Value}
			have = true
		case v.IsFunction() && strings.EqualFold(v.Token.Value, "url"):
			if have {
				return fontSource{}, false
			}
			ref, ok := singleString(v.Values)
			if !ok {
				return fontSource{}, false
			}
			out = fontSource{ref: ref}
			have = true
		case v.IsFunction() && strings.EqualFold(v.Token.Value, "local"):
			if have {
				return fontSource{}, false
			}
			name, ok := singleString(v.Values)
			if !ok {
				// local() with a bare unquoted name is legal and common:
				// local(Ahem). The name is the concatenation of the idents.
				name = strings.TrimSpace(descriptorFamily(v.Values))
				if name == "" {
					return fontSource{}, false
				}
			}
			out = fontSource{local: true, ref: name}
			have = true
		case v.IsFunction() && strings.EqualFold(v.Token.Value, "format"):
			if !have {
				return fontSource{}, false
			}
			f, ok := singleString(v.Values)
			if !ok {
				f = strings.TrimSpace(descriptorFamily(v.Values))
			}
			out.format = strings.ToLower(strings.TrimSpace(f))
		case v.IsFunction() && strings.EqualFold(v.Token.Value, "tech"):
			// A capability list — colour tables, variations, palettes. It
			// narrows when an entry may be used and never widens it, so an
			// engine that ignores it can only try a font it might have skipped,
			// and the try either parses or moves to the next entry.
			continue
		default:
			// Anything else in an entry makes it one this engine does not
			// understand, and the specification's own rule for a src entry it
			// cannot parse is to skip to the next.
			return fontSource{}, false
		}
	}
	return out, have
}

// singleString returns the one string a function's arguments consist of.
func singleString(vals []css.ComponentValue) (string, bool) {
	var got string
	found := false
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			continue
		}
		if !v.IsToken() || v.Token.Kind != css.String || found {
			return "", false
		}
		got, found = v.Token.Value, true
	}
	return got, found
}

// face loads the first src entry that yields one.
//
// The list is a fallback chain and is walked as one: an entry that cannot be
// loaded is not a failure of the rule, it is the next entry's turn. Only when
// none of them worked has the rule contributed nothing, and that is the single
// finding raised — one per rule rather than one per entry, because an author
// who wrote four alternatives expects three of them to be unused.
func (l *fontFaceLoader) face(p pendingFontFace, r fontFaceRule) (*fonts.Face, string, bool) {
	var why []string
	for _, s := range r.srcs {
		if s.local {
			// local() names a *face*, not a family in a weight, so the
			// descriptors on the rule are what say how the face is to be
			// treated and are not asked for again here. The set is asked for
			// the upright regular of the name, which is the only thing a set
			// keyed by family can answer to a full face name.
			if face, ok := l.base.Face(s.ref, false, false); ok {
				return face, "local(" + s.ref + ")", true
			}
			// Not having a local face is the ordinary case and not a fault:
			// the whole point of writing local() first is that most readers
			// will not have it. It is the next entry's turn, silently.
			continue
		}
		face, fail := l.load(p, r, s)
		if face != nil {
			return face, s.ref, true
		}
		if fail != nil {
			why = append(why, fail.message)
		}
	}
	l.rec.ReportDetail(Finding{
		Rule:   RuleResourceBlocked,
		Source: p.at(),
		Message: "the @font-face for " + quoteValue(r.family) +
			" loaded no font, so the family is not available: " + joinReasons(why),
		Property: "@font-face",
	})
	return nil, "", false
}

// joinReasons renders why every alternative failed, or says that none was
// usable at all.
func joinReasons(why []string) string {
	if len(why) == 0 {
		return "none of its sources was one this engine could use"
	}
	return strings.Join(why, "; ")
}

// load fetches and parses one url() entry.
func (l *fontFaceLoader) load(p pendingFontFace, r fontFaceRule, s fontSource) (*fonts.Face, *loadFailure) {
	if face, ok := l.loaded[s.ref]; ok {
		return face, nil
	}
	if l.failed[s.ref] {
		return nil, nil
	}
	if !usableFontFormat(s.format) {
		// A format this engine has no parser for. Saying so before the read is
		// not an optimisation, it is the difference between skipping an entry
		// the author expected most readers to skip and reporting a failure.
		return nil, &loadFailure{
			rule: RuleFontUndecodable,
			message: "the font at " + quoteValue(s.ref) + " is declared as " +
				quoteValue(s.format) + ", which this engine does not read",
		}
	}
	if l.faces >= maxDocumentFaces {
		if !l.cappedFaces {
			l.cappedFaces = true
			l.rec.Report(RuleLimit, NoSource, fmt.Sprintf(
				"this document loads more than the %d font faces this engine will parse; "+
					"the rest were not loaded", maxDocumentFaces))
		}
		return nil, &loadFailure{
			rule: RuleResourceBlocked,
			message: fmt.Sprintf("the font at %s was not loaded: this document already "+
				"used the %d faces this engine will parse", quoteValue(s.ref), maxDocumentFaces),
		}
	}

	data, fail := l.fetch(s.ref)
	if fail != nil {
		l.failed[s.ref] = true
		return nil, fail
	}
	if len(data) > maxFontBytes {
		l.failed[s.ref] = true
		return nil, &loadFailure{
			rule: RuleResourceBlocked,
			message: fmt.Sprintf("the font at %s is %d bytes, more than the %d this engine will parse",
				quoteValue(s.ref), len(data), maxFontBytes),
		}
	}
	if len(data) > l.budget {
		l.failed[s.ref] = true
		if !l.cappedBytes {
			l.cappedBytes = true
			l.rec.Report(RuleLimit, NoSource, fmt.Sprintf(
				"this document's fonts together need more than the %d bytes this engine "+
					"will parse for one document; the rest were not loaded", maxDocumentFontBytes))
		}
		return nil, &loadFailure{
			rule: RuleResourceBlocked,
			message: "the font at " + quoteValue(s.ref) +
				" was not loaded: the document's font budget was already spent",
		}
	}
	// Charged before the parse, because the read is what allocated and the
	// parse is what the budget exists to bound. A program that fails to parse
	// cost exactly as much as one that did not.
	l.budget -= len(data)

	face, err := fonts.Load(data)
	if err != nil {
		l.failed[s.ref] = true
		return nil, &loadFailure{
			rule: RuleFontUndecodable,
			message: "the font at " + quoteValue(s.ref) +
				" is not one this engine can read: " + err.Error(),
		}
	}
	l.faces++
	l.loaded[s.ref] = face
	return face, nil
}

// fetch obtains the bytes of one font, applying resource.go's policy — the same
// three answers, in the same order and for the same reasons, that an image and a
// linked stylesheet get.
func (l *fontFaceLoader) fetch(ref string) ([]byte, *loadFailure) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, &loadFailure{
			rule:    RuleResourceBlocked,
			message: "an @font-face src names an empty reference",
		}
	}
	if scheme, ok := schemeOf(ref); ok {
		if scheme == "data" {
			return decodeDataURI(ref, "font", RuleFontUndecodable)
		}
		return nil, &loadFailure{
			rule: RuleResourceBlocked,
			message: "the font at " + quoteValue(ref) + " names the " + quoteValue(scheme) +
				" scheme; this engine resolves no URLs and fetches nothing",
		}
	}
	if l.res == nil {
		return nil, &loadFailure{
			rule:    RuleResourceBlocked,
			message: "the font at " + quoteValue(ref) + " was not loaded: " + ErrNoResolver.Error(),
		}
	}
	data, err := l.res.Resolve(ref)
	if err != nil {
		return nil, &loadFailure{
			rule:    RuleResourceBlocked,
			message: "the font at " + quoteValue(ref) + " was not loaded: " + err.Error(),
		}
	}
	if len(data) == 0 {
		return nil, &loadFailure{
			rule:    RuleFontUndecodable,
			message: "the font at " + quoteValue(ref) + " is empty",
		}
	}
	return data, nil
}

// overRuleCap reports the document-wide rule count tripping.
//
// Two findings, on the model of stylesheet.go's: the guard tripped, which every
// other part of pdf0 reports as "limit", and the document is missing fonts it
// asked for, which is what makes the page wrong.
func (l *fontFaceLoader) overRuleCap(p pendingFontFace, total int) {
	if !l.cappedRules {
		l.cappedRules = true
		l.rec.Report(RuleLimit, NoSource, fmt.Sprintf(
			"this document declares %d @font-face rules, more than the %d this engine "+
				"will look at; the rest were not read", total, maxFontFaceRules))
	}
	l.rec.ReportDetail(Finding{
		Rule:   RuleResourceBlocked,
		Source: p.at(),
		Message: fmt.Sprintf("this @font-face was not read: the document already declared "+
			"the %d this engine will look at", maxFontFaceRules),
		Property: "@font-face",
	})
}

// usableFontFormat says whether a format() hint names something fonts.Load can
// parse.
//
// An absent hint is usable: the file is tried and either parses or does not.
// The named ones are the two sfnt spellings; woff and woff2 are compressed
// containers this engine does not unwrap, and svg and embedded-opentype are not
// sfnt at all.
func usableFontFormat(f string) bool {
	switch f {
	case "", "truetype", "opentype", "truetype-variations", "opentype-variations":
		return true
	}
	return false
}

// descriptorFamily renders the font-family descriptor's value as a name.
//
// It is a string or a sequence of identifiers, and the two are the same name:
// `font-family: Times New Roman` and `font-family: "Times New Roman"` name one
// family. Anything else — a number, a function — is not a family name and
// yields nothing.
func descriptorFamily(vals []css.ComponentValue) string {
	var parts []string
	for _, v := range vals {
		if !v.IsToken() {
			return ""
		}
		switch v.Token.Kind {
		case css.Whitespace:
			continue
		case css.String:
			if len(parts) > 0 {
				return ""
			}
			parts = append(parts, v.Token.Value)
		case css.Ident:
			parts = append(parts, v.Token.Value)
		default:
			return ""
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// parseWeightDescriptor reads the font-weight descriptor as a range.
func parseWeightDescriptor(vals []css.ComponentValue) (low, high float64, ok bool) {
	var nums []float64
	for _, v := range vals {
		if !v.IsToken() {
			return 0, 0, false
		}
		switch v.Token.Kind {
		case css.Whitespace:
			continue
		case css.Ident:
			switch strings.ToLower(v.Token.Value) {
			case "normal":
				nums = append(nums, 400)
			case "bold":
				nums = append(nums, 700)
			default:
				return 0, 0, false
			}
		case css.Number:
			// The descriptor takes a number in [1,1000]; "bolder" and
			// "lighter" are relative to an inherited value and mean nothing
			// here, which is why they are not in the switch above.
			n := v.Token.Number
			if n < 1 || n > 1000 {
				return 0, 0, false
			}
			nums = append(nums, n)
		default:
			return 0, 0, false
		}
	}
	switch len(nums) {
	case 1:
		return nums[0], nums[0], true
	case 2:
		if nums[0] > nums[1] {
			return 0, 0, false
		}
		return nums[0], nums[1], true
	}
	return 0, 0, false
}

// parseStyleDescriptor reads the font-style descriptor.
//
// An oblique with an angle is italic here: this engine has no synthetic slant
// and no variable slnt axis to set, so what the angle would choose between is
// two faces it cannot tell apart. An angle of zero is upright, which is the one
// case where the number decides something.
func parseStyleDescriptor(vals []css.ComponentValue) (italic bool, ok bool) {
	var kw string
	var angles []float64
	for _, v := range vals {
		if !v.IsToken() {
			return false, false
		}
		switch v.Token.Kind {
		case css.Whitespace:
			continue
		case css.Ident:
			if kw != "" {
				return false, false
			}
			kw = strings.ToLower(v.Token.Value)
		case css.Dimension:
			if !strings.EqualFold(v.Token.Unit, "deg") {
				return false, false
			}
			angles = append(angles, v.Token.Number)
		case css.Number:
			if v.Token.Number != 0 {
				return false, false
			}
			angles = append(angles, 0)
		default:
			return false, false
		}
	}
	switch kw {
	case "normal":
		return false, len(angles) == 0
	case "italic":
		return true, len(angles) == 0
	case "oblique":
		if len(angles) == 0 {
			return true, true
		}
		if len(angles) > 2 {
			return false, false
		}
		for _, a := range angles {
			if a != 0 {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

// unicodeSpan is one span of the unicode-range descriptor.
type unicodeSpan struct{ lo, hi rune }

// parseUnicodeRange reads the unicode-range descriptor.
//
// The descriptor's syntax predates CSS Syntax Level 3's token set, which has no
// unicode-range token: "U+0025-00FF" tokenizes as an identifier, a number and a
// dimension whose unit happens to be hexadecimal digits. So the text is
// reconstructed from the tokens — every one of which preserves what the author
// typed — and read as the grammar it is. Reading it off the token kinds instead
// would be reading it off an accident of where the tokenizer split it.
func parseUnicodeRange(vals []css.ComponentValue) ([]unicodeSpan, bool) {
	var out []unicodeSpan
	for _, item := range splitOnComma(vals) {
		text := strings.TrimSpace(rawText(item))
		if text == "" {
			return nil, false
		}
		span, ok := parseUnicodeSpan(text)
		if !ok {
			return nil, false
		}
		out = append(out, span)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// parseUnicodeSpan reads one of the three forms: a single code point, a
// wildcard, or a range.
func parseUnicodeSpan(text string) (unicodeSpan, bool) {
	if len(text) < 3 || (text[0] != 'u' && text[0] != 'U') || text[1] != '+' {
		return unicodeSpan{}, false
	}
	body := text[2:]
	if i := strings.IndexByte(body, '-'); i >= 0 {
		lo, ok1 := parseHex(body[:i])
		hi, ok2 := parseHex(body[i+1:])
		if !ok1 || !ok2 || lo > hi {
			return unicodeSpan{}, false
		}
		return unicodeSpan{lo, hi}, true
	}
	if q := strings.IndexByte(body, '?'); q >= 0 {
		// A wildcard: every "?" stands for one hexadecimal digit, and they must
		// all be at the end. "4??" is U+400 to U+4FF.
		if len(body) > 6 {
			return unicodeSpan{}, false
		}
		prefix := body[:q]
		for i := q; i < len(body); i++ {
			if body[i] != '?' {
				return unicodeSpan{}, false
			}
		}
		digits := len(body) - q
		lo, ok := parseHex(prefix + strings.Repeat("0", digits))
		hi, ok2 := parseHex(prefix + strings.Repeat("F", digits))
		if !ok || !ok2 {
			return unicodeSpan{}, false
		}
		return unicodeSpan{lo, hi}, true
	}
	v, ok := parseHex(body)
	if !ok {
		return unicodeSpan{}, false
	}
	return unicodeSpan{v, v}, true
}

// parseHex reads one to six hexadecimal digits as a code point.
func parseHex(s string) (rune, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	if n > 0x10FFFF {
		return 0, false
	}
	return rune(n), true
}

// coversAllOfUnicode reports whether the spans between them leave nothing out.
func coversAllOfUnicode(spans []unicodeSpan) bool {
	sorted := append([]unicodeSpan(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo < sorted[j].lo })
	next := rune(0)
	for _, s := range sorted {
		if s.lo > next {
			return false
		}
		if s.hi >= next {
			next = s.hi + 1
		}
	}
	return next > 0x10FFFF
}

// unicodeRangeText renders the parsed spans for a finding, so the message says
// what was actually read rather than what was typed.
func unicodeRangeText(spans []unicodeSpan) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		if s.lo == s.hi {
			parts = append(parts, fmt.Sprintf("U+%04X", s.lo))
			continue
		}
		parts = append(parts, fmt.Sprintf("U+%04X-%04X", s.lo, s.hi))
	}
	return strings.Join(parts, ", ")
}

// rawText renders component values back to the text the author wrote, as far as
// the tokens preserve it.
//
// It exists for unicode-range and for nothing else: every other descriptor here
// is read off the token kinds, which is the right way round. A number's Repr
// and a dimension's Unit are what the author typed, so "U+0025-00FF" comes back
// as itself.
func rawText(vals []css.ComponentValue) string {
	var b strings.Builder
	for _, v := range vals {
		if !v.IsToken() {
			// A function or a block in a unicode-range is not the grammar, and
			// rendering something for it would produce text that then failed to
			// parse for the wrong reason.
			return ""
		}
		t := v.Token
		switch t.Kind {
		case css.Ident, css.Delim:
			b.WriteString(t.Value)
		case css.Number:
			b.WriteString(t.Repr)
		case css.Dimension:
			b.WriteString(t.Repr + t.Unit)
		case css.Whitespace:
			b.WriteByte(' ')
		default:
			return ""
		}
	}
	return b.String()
}

// splitOnComma cuts a value list at its top-level commas.
func splitOnComma(vals []css.ComponentValue) [][]css.ComponentValue {
	var out [][]css.ComponentValue
	start := 0
	for i, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Comma {
			out = append(out, vals[start:i])
			start = i + 1
		}
	}
	out = append(out, vals[start:])
	return out
}
