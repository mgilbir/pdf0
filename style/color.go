package style

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
)

// Colour, from CSS Color Module Level 4.
//
// What is implemented is the sRGB half: the named keywords, the hexadecimal
// notations, rgb()/rgba() and hsl()/hsla(), in both the comma-separated syntax
// of Level 3 and the space-separated one of Level 4. That is what a document
// generator meets, and it is what a PDF can hold without a colour-management
// decision being made on the author's behalf.
//
// The rest of Level 4 — lab(), lch(), oklab(), oklch(), hwb() and the color()
// function's other colour spaces — is refused and reported rather than
// approximated. Converting one of those to sRGB is a *rendering intent*
// decision, and silently picking one would produce a document whose colours are
// nearly right, with nothing to say that a choice was made. When they arrive
// they should arrive with an ICC profile and an output intent, which pdf0
// already knows how to write.

// RGBA is a colour in sRGB: three components on a 0–255 scale and an alpha in
// [0, 1].
//
// Every component is a real rather than a byte, and that is the whole design
// decision in this file. A byte is what the *notations* look like — "#f00",
// "rgb(255, 0, 0)" — but it is not what the values are: "hsl(0, 33.33%, 12.5%)"
// is exactly 42.498938 red, and rounding it to 42 is a quantisation this engine
// would be inventing. PDF writes DeviceRGB components and /ca as reals, so that
// quantisation would then be carried all the way into the output for no reason
// at all.
//
// The 0–255 scale rather than 0–1 is chosen because it is the one CSS
// serialises in, so a value can be compared against a reference without a
// conversion in between. Writing one to a PDF divides by 255.
type RGBA struct {
	// R, G and B are in [0, 255]; A is in [0, 1].
	R, G, B, A float64
}

// String renders a colour the way CSS Color 4 serialises it, which is what the
// external suite compares against: "rgb(r, g, b)" when opaque and
// "rgba(r, g, b, a)" otherwise, with the alpha as a number rather than a
// percentage.
func (c RGBA) String() string {
	r, g, b := formatNumber(c.R), formatNumber(c.G), formatNumber(c.B)
	if c.A >= 1 {
		return fmt.Sprintf("rgb(%s, %s, %s)", r, g, b)
	}
	return fmt.Sprintf("rgba(%s, %s, %s, %s)", r, g, b, formatNumber(c.A))
}

// formatNumber renders a component the way CSS serialises one: to six decimal
// places, with no trailing zeros and no trailing point.
//
// Six is where the specification's rounding lands and where the reference
// implementations agree. It matters because these values are usually a third or
// a sixth of something — 136/255 is 0.533333…, and hsl() lands on 42.498938 —
// and a shorter form is a different number that merely happens to quantise back
// to the same byte, which is only the same colour if the reader quantises too.
func formatNumber(v float64) string {
	r := math.Round(v*1e6) / 1e6
	// -0 and 0 are the same colour and only one of them reads well.
	if r == 0 {
		return "0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Transparent is the one keyword whose alpha is not 1.
var Transparent = RGBA{R: 0, G: 0, B: 0, A: 0}

// ParseColor reads a colour from component values.
//
// It reports false for anything that is not a colour this engine reads, which
// covers both malformed input and the colour spaces named above. The caller
// decides which of those it was — the styling stage reports the second as
// unsupported and the first as an error.
func ParseColor(vals []css.ComponentValue) (RGBA, bool) {
	parts := splitOnWhitespace(vals)
	if len(parts) != 1 || len(parts[0]) != 1 {
		return RGBA{}, false
	}
	v := parts[0][0]

	if v.IsFunction() {
		return parseColorFunction(v)
	}
	if !v.IsToken() {
		return RGBA{}, false
	}

	switch v.Token.Kind {
	case css.Hash:
		return parseHex(v.Token.Value)
	case css.Ident:
		name := strings.ToLower(v.Token.Value)
		if name == "transparent" {
			return Transparent, true
		}
		c, ok := namedColors[name]
		return c, ok
	}
	return RGBA{}, false
}

// parseHex reads #rgb, #rgba, #rrggbb and #rrggbbaa. The "#" is already gone —
// the tokenizer keeps it out of the hash token's value.
func parseHex(s string) (RGBA, bool) {
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return RGBA{}, false
		}
	}
	// The short forms double each digit, so #f00 is #ff0000 rather than #f00000
	// — which is why "f" becomes "ff" and not "f0".
	expand := func(i int) float64 {
		d := hexVal(s[i])
		return float64(d<<4 | d)
	}
	pair := func(i int) float64 {
		return float64(hexVal(s[i])<<4 | hexVal(s[i+1]))
	}
	// The alpha digit is a position on 0–255, so it becomes a number in [0, 1]
	// by dividing.
	switch len(s) {
	case 3:
		return RGBA{expand(0), expand(1), expand(2), 1}, true
	case 4:
		return RGBA{expand(0), expand(1), expand(2), expand(3) / 255}, true
	case 6:
		return RGBA{pair(0), pair(2), pair(4), 1}, true
	case 8:
		return RGBA{pair(0), pair(2), pair(4), pair(6) / 255}, true
	}
	return RGBA{}, false
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func parseColorFunction(fn css.ComponentValue) (RGBA, bool) {
	switch strings.ToLower(fn.Token.Value) {
	case "rgb", "rgba":
		return parseRGBFunction(fn.Values)
	case "hsl", "hsla":
		return parseHSLFunction(fn.Values)
	}
	return RGBA{}, false
}

// colorArgs splits a colour function's arguments into the three components and
// the optional alpha, accepting both syntaxes.
//
// Level 3 writes "rgb(1, 2, 3)" and "rgba(1, 2, 3, 0.5)"; Level 4 writes
// "rgb(1 2 3)" and "rgb(1 2 3 / 0.5)". The two must not be mixed — "rgb(1, 2 3)"
// is not a colour — so which one is in use is decided by the first separator and
// then held to.
// legacy reports the comma-separated syntax, which is the one with the stricter
// rules: its components must agree about being numbers or percentages, and its
// hsl() saturation and lightness must be percentages. The space-separated syntax
// of Level 4 relaxes both, and admits "none" for a missing component.
func colorArgs(vals []css.ComponentValue) (args [][]css.ComponentValue, alpha []css.ComponentValue, legacy, ok bool) {
	hasComma := false
	hasSlash := false
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Comma {
			hasComma = true
		}
		if v.IsToken() && v.Token.IsDelim('/') {
			hasSlash = true
		}
	}
	// The two syntaxes must not be mixed. In practice nothing reaches this:
	// "rgb(1, 2, 3 / 0.5)" splits on its commas into three groups of which the
	// last is "3 / 0.5", and that is refused as a component further down —
	// which was checked by removing this and watching the tests still pass.
	//
	// It stays because it says what the rule is, at the point the rule applies,
	// rather than leaving a reader to discover that mixing happens to be caught
	// by an unrelated check three functions away.
	if hasComma && hasSlash {
		return nil, nil, false, false
	}

	if hasComma {
		var groups [][]css.ComponentValue
		cur := []css.ComponentValue{}
		for _, v := range vals {
			if v.IsToken() && v.Token.Kind == css.Comma {
				groups = append(groups, cur)
				cur = nil
				continue
			}
			cur = append(cur, v)
		}
		groups = append(groups, cur)
		for i := range groups {
			groups[i] = trimSpace(groups[i])
		}
		switch len(groups) {
		case 3:
			return groups, nil, true, true
		case 4:
			return groups[:3], groups[3], true, true
		}
		return nil, nil, false, false
	}

	// The space-separated form, with an optional "/ alpha".
	before, after := vals, []css.ComponentValue(nil)
	for i, v := range vals {
		if v.IsToken() && v.Token.IsDelim('/') {
			before, after = vals[:i], vals[i+1:]
			break
		}
	}
	groups := splitOnWhitespace(before)
	if len(groups) != 3 {
		return nil, nil, false, false
	}
	if !hasSlash {
		return groups, nil, false, true
	}
	a := splitOnWhitespace(after)
	if len(a) != 1 {
		return nil, nil, false, false
	}
	return groups, a[0], false, true
}

func trimSpace(vals []css.ComponentValue) []css.ComponentValue {
	for len(vals) > 0 && vals[0].IsToken() && vals[0].Token.Kind == css.Whitespace {
		vals = vals[1:]
	}
	for len(vals) > 0 && vals[len(vals)-1].IsToken() && vals[len(vals)-1].Token.Kind == css.Whitespace {
		vals = vals[:len(vals)-1]
	}
	return vals
}

func parseRGBFunction(vals []css.ComponentValue) (RGBA, bool) {
	args, alphaArg, legacy, ok := colorArgs(vals)
	if !ok {
		return RGBA{}, false
	}

	var comps [3]float64
	var sawNumber, sawPercent bool
	for i, arg := range args {
		v, kind, ok := numericArg(arg, !legacy)
		if !ok {
			return RGBA{}, false
		}
		switch kind {
		case argPercent:
			sawPercent = true
			comps[i] = clamp(v, 0, 100) / 100 * 255
		case argNumber:
			sawNumber = true
			comps[i] = clamp(v, 0, 255)
		case argNone:
			// A missing component. Nothing here carries missingness forward —
			// it matters only for interpolation, which this engine does not do
			// — so it resolves to zero, which is what serialising one gives.
			comps[i] = 0
		}
	}
	// The comma syntax does not allow "rgb(50%, 128, 0)"; the space syntax does
	// allow "rgb(50% 128 0)". That difference is the whole of why legacy is
	// tracked.
	if legacy && sawNumber && sawPercent {
		return RGBA{}, false
	}

	a, ok := parseAlpha(alphaArg, !legacy)
	if !ok {
		return RGBA{}, false
	}
	return RGBA{comps[0], comps[1], comps[2], a}, true
}

func parseHSLFunction(vals []css.ComponentValue) (RGBA, bool) {
	args, alphaArg, legacy, ok := colorArgs(vals)
	if !ok {
		return RGBA{}, false
	}

	hue, ok := parseHue(args[0], !legacy)
	if !ok {
		return RGBA{}, false
	}
	// In the comma syntax saturation and lightness must be percentages: "hsl(0,
	// 50, 50%)" is not a colour. The space syntax reads a bare number as the
	// same percentage, so "hsl(0 50 50)" is.
	sat, ok := percentArg(args[1], legacy)
	if !ok {
		return RGBA{}, false
	}
	light, ok := percentArg(args[2], legacy)
	if !ok {
		return RGBA{}, false
	}

	a, ok := parseAlpha(alphaArg, !legacy)
	if !ok {
		return RGBA{}, false
	}
	r, g, b := hslToRGB(hue, clamp(sat, 0, 100)/100, clamp(light, 0, 100)/100)
	return RGBA{r, g, b, a}, true
}

// percentArg reads a saturation or lightness, on a 0-100 scale.
func percentArg(arg []css.ComponentValue, legacy bool) (float64, bool) {
	v, kind, ok := numericArg(arg, !legacy)
	if !ok {
		return 0, false
	}
	switch kind {
	case argPercent:
		return v, true
	case argNone:
		return 0, true
	case argNumber:
		if legacy {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// parseHue reads a hue, which may be a bare number or an angle, and normalises
// it into [0, 360).
func parseHue(arg []css.ComponentValue, allowNone bool) (float64, bool) {
	if len(arg) != 1 || !arg[0].IsToken() {
		return 0, false
	}
	t := arg[0].Token
	if allowNone && t.Kind == css.Ident && strings.EqualFold(t.Value, "none") {
		return 0, true
	}
	var deg float64
	switch t.Kind {
	case css.Number:
		deg = t.Number
	case css.Dimension:
		switch strings.ToLower(t.Unit) {
		case "deg":
			deg = t.Number
		case "grad":
			deg = t.Number * 360 / 400
		case "rad":
			deg = t.Number * 180 / math.Pi
		case "turn":
			deg = t.Number * 360
		default:
			return 0, false
		}
	default:
		return 0, false
	}
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	return deg, true
}

// argKind is what one component of a colour function turned out to be.
type argKind uint8

const (
	argNumber argKind = iota
	argPercent
	argNone
)

// numericArg reads one component. "none" is only a component in the Level 4
// syntax, so allowNone follows which syntax is in use.
func numericArg(arg []css.ComponentValue, allowNone bool) (value float64, kind argKind, ok bool) {
	if len(arg) != 1 || !arg[0].IsToken() {
		return 0, 0, false
	}
	switch t := arg[0].Token; t.Kind {
	case css.Number:
		return t.Number, argNumber, true
	case css.Percentage:
		return t.Number, argPercent, true
	case css.Ident:
		if allowNone && strings.EqualFold(t.Value, "none") {
			return 0, argNone, true
		}
	}
	return 0, 0, false
}

// parseAlpha reads the optional alpha, which is 1 when absent.
func parseAlpha(arg []css.ComponentValue, allowNone bool) (float64, bool) {
	if len(arg) == 0 {
		return 1, true
	}
	v, kind, ok := numericArg(arg, allowNone)
	if !ok {
		return 0, false
	}
	switch kind {
	case argPercent:
		v /= 100
	case argNone:
		v = 0
	}
	return clamp(v, 0, 1), true
}

// hslToRGB converts a hue in degrees and a saturation and lightness in [0,1],
// by the algorithm in CSS Color 4 §7. The result is on the 0–255 scale and is
// deliberately not rounded — see the note on RGBA.
func hslToRGB(h, s, l float64) (float64, float64, float64) {
	f := func(n float64) float64 {
		k := math.Mod(n+h/30, 12)
		a := s * math.Min(l, 1-l)
		v := l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
		return clamp(v, 0, 1) * 255
	}
	return f(0), f(8), f(4)
}
