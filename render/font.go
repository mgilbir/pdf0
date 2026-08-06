package render

import (
	"strings"
	"sync"

	"github.com/mgilbir/pdf0/fonts"
)

// Choosing a face for a box, and saying so when the choice was not the one
// asked for.
//
// §10 of the rendering proposal makes the font set the caller's to supply,
// through an interface, and the reason is packaging: a font committed to this
// repository is paid for by every pdf0 user including the ones who only parse.
// What is here is the interface and a default made of the fourteen faces every
// PDF reader already has, which need no embedding at all.

// FontSet supplies the faces a document is set in.
//
// A set is asked for a family by name and answers whether it has one. It is not
// asked to do the fallback itself: which families to try, in what order, and
// what to do when none of them is there are decisions the engine makes and
// reports on, and a set that made them silently would be a set that could hide
// a substitution.
type FontSet interface {
	// Face returns the face for a family in a weight and style, and whether the
	// set has it. The family is matched case-insensitively, as CSS matches one.
	Face(family string, bold, italic bool) (*fonts.Face, bool)
}

// StandardFonts is a FontSet of the fourteen faces every PDF reader has built
// in.
//
// They cost nothing to use — a standard font is named in the file rather than
// embedded — and they cover Latin, which makes them the right default for a
// document that has not said what it wants. They cover nothing else, and a
// document that needs more will be told, which is the point of reporting a
// missing glyph rather than drawing a box.
func StandardFonts() FontSet { return &standardFonts{} }

type standardFonts struct {
	mu     sync.Mutex
	loaded map[string]*fonts.Face
}

// standardFamilies maps a CSS family to the standard face for it.
//
// The three generic families are the ones CSS guarantees, and mapping them here
// is what makes "font-family: sans-serif" work without a caller supplying
// anything. The concrete names are included because real documents ask for them
// by name constantly, and answering "Arial" with Helvetica is what every PDF
// reader does anyway — they are metrically compatible, which is the only reason
// the substitution is honest rather than approximate.
var standardFamilies = map[string]string{
	"serif":           "Times",
	"times":           "Times",
	"times new roman": "Times",
	"georgia":         "Times",

	"sans-serif": "Helvetica",
	"helvetica":  "Helvetica",
	"arial":      "Helvetica",
	"verdana":    "Helvetica",
	"system-ui":  "Helvetica",

	"monospace":        "Courier",
	"courier":          "Courier",
	"courier new":      "Courier",
	"ui-monospace":     "Courier",
	"consolas":         "Courier",
	"dejavu sans mono": "Courier",
}

func (s *standardFonts) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	base, ok := standardFamilies[strings.ToLower(strings.TrimSpace(family))]
	if !ok {
		return nil, false
	}
	name := standardName(base, bold, italic)

	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.loaded[name]; ok {
		return f, true
	}
	f, err := fonts.Standard(name)
	if err != nil {
		return nil, false
	}
	if s.loaded == nil {
		s.loaded = map[string]*fonts.Face{}
	}
	s.loaded[name] = f
	return f, true
}

// standardName spells the standard-14 name for a base family in a weight and
// style. The three families spell their variants differently — Times has
// "Times-Roman" where the others have a bare name — which is the sort of detail
// that produces a silent fallback if it is guessed at.
func standardName(base string, bold, italic bool) string {
	switch base {
	case "Times":
		switch {
		case bold && italic:
			return "Times-BoldItalic"
		case bold:
			return "Times-Bold"
		case italic:
			return "Times-Italic"
		}
		return "Times-Roman"
	case "Courier":
		switch {
		case bold && italic:
			return "Courier-BoldOblique"
		case bold:
			return "Courier-Bold"
		case italic:
			return "Courier-Oblique"
		}
		return "Courier"
	default:
		switch {
		case bold && italic:
			return "Helvetica-BoldOblique"
		case bold:
			return "Helvetica-Bold"
		case italic:
			return "Helvetica-Oblique"
		}
		return "Helvetica"
	}
}

// fontFor picks the face a box's text is set in, following its font-family list
// and reporting a substitution.
//
// The list is tried in order, which is what a font stack is for. When none of
// the named families is available the last resort is the set's sans-serif, and
// *that* is reported: a document set in a face its author did not choose has
// different metrics and different line breaks, and nothing about the resulting
// page says so.
func (l *layouter) fontFor(b *Box) (*fonts.Face, bool) {
	key := fontKey{
		families: b.Style["font-family"],
		bold:     isBold(b.Style["font-weight"]),
		italic:   isItalic(b.Style["font-style"]),
	}
	if got, ok := l.fonts[key]; ok {
		return got.face, got.face != nil
	}

	families := parseFamilyList(key.families)
	for _, family := range families {
		if face, ok := l.fontSet.Face(family, key.bold, key.italic); ok {
			l.fonts[key] = resolvedFont{face: face}
			return face, true
		}
	}

	// Nothing the author asked for. The generic sans-serif is the last resort,
	// and taking it silently is what this whole design is against.
	face, ok := l.fontSet.Face("sans-serif", key.bold, key.italic)
	l.fonts[key] = resolvedFont{face: face}
	if !ok {
		return nil, false
	}
	if len(families) > 0 {
		l.rec.ReportDetail(Finding{
			Rule:     RuleFontFallback,
			Message:  "no face was available for " + quoteValue(key.families) + ", so a default was used; the metrics and the line breaks will differ",
			Property: "font-family",
		})
	}
	return face, true
}

type fontKey struct {
	families string
	bold     bool
	italic   bool
}

type resolvedFont struct{ face *fonts.Face }

// parseFamilyList splits a font-family value into the families it names.
//
// It is a comma-separated list whose entries may be quoted, and the quotes are
// how a family whose name contains a comma or a keyword is written. The css
// package has already resolved the quoting, so this only has to split.
func parseFamilyList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		name := strings.TrimSpace(part)
		name = strings.Trim(name, `"'`)
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// isBold reads font-weight. The numeric scale runs 100 to 900 and 400 is
// normal; the boundary is at 600, which is where every renderer puts it.
func isBold(value string) bool {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "bold", "bolder":
		return true
	case "", "normal", "lighter":
		return false
	default:
		n := 0
		for i := 0; i < len(v); i++ {
			if v[i] < '0' || v[i] > '9' {
				return false
			}
			n = n*10 + int(v[i]-'0')
		}
		return n >= 600
	}
}

func isItalic(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "italic", "oblique":
		return true
	}
	return false
}
