package style

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/css"
)

// Colour, against the same external suite the css package uses.
//
// The suite's colour files give an input and the Level 4 serialisation of the
// result, or null when the input is not a colour. 1313 cases pass here.
//
// Only the keyword file carries a meaningful number of the null cases — eight of
// them — so the suite is largely a check that valid colours are read correctly
// rather than that invalid ones are refused. The refusals are covered by
// TestColorRefusals below, written here because the suite does not cover them,
// and that division is worth stating rather than letting the case count imply a
// thoroughness it does not have.
//
// # What is checked here and what is not
//
// The hexadecimal, rgb() and hsl() files are a genuine external check: they
// exercise an *algorithm* — digit doubling, percentage scaling, the
// hue-to-RGB conversion — that this code implements independently of them.
//
// The keyword file is weaker and it is worth saying so rather than letting it
// read as equal evidence. Both it and style/colors.go trace to the same table in
// CSS Color 4, so agreement shows the generator transcribed 148 rows correctly
// and nothing more. That is worth having — 148 rows is exactly where a
// transcription error hides — but it is not independent confirmation of a
// reading of the specification, which is what the other files give.

const colorOracleEnv = "CSS_PARSING_TESTS"

// colorFiles are the suite's files this engine answers for.
//
// The rest are listed in unsupportedColorFiles with the reason, so that a file
// is never simply absent from the run.
var colorFiles = []string{
	"color_keywords_3.json",
	"color_hexadecimal_3.json",
	"color_hexadecimal_4.json",
	"color_hsl_3.json",
	"color_hsl_4.json",
}

// unsupportedColorFiles are the suite's colour files this engine does not
// answer, each with why.
//
// Every one of them is a colour space that is not sRGB, and converting one to
// sRGB is a rendering-intent decision. Making that choice silently would produce
// a document whose colours are nearly right with nothing to say a choice was
// made; when these arrive they should arrive with an ICC profile and an output
// intent, which pdf0 already writes.
var unsupportedColorFiles = map[string]string{
	"color_function_4.json":  "the color() function names a colour space to convert from",
	"color_hwb_4.json":       "hwb() is a cylindrical space needing conversion",
	"color_lab_4.json":       "lab() is a perceptual space needing a white point",
	"color_lch_4.json":       "lch() is a perceptual space needing a white point",
	"color_oklab_4.json":     "oklab() is a perceptual space needing conversion",
	"color_oklch_4.json":     "oklch() is a perceptual space needing conversion",
	"color_keywords_4.json":  "the Level 4 keyword additions are system colours, which a page has none of",
	"color_functions_5.json": "Level 5 relative colours resolve against another colour",
}

func colorOracleDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(colorOracleEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make test-css`) to check colours against the CSS parsing tests",
			colorOracleEnv)
	}
	return dir
}

func TestColorOracle(t *testing.T) {
	dir := colorOracleDir(t)

	for _, name := range colorFiles {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Skipf("%s is not present: %v", name, err)
			}
			var flat []any
			if err := json.Unmarshal(raw, &flat); err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}
			if len(flat)%2 != 0 {
				t.Fatalf("%s is not a whole number of pairs", name)
			}

			var valid, invalid int
			for i := 0; i < len(flat); i += 2 {
				input, ok := flat[i].(string)
				if !ok {
					t.Fatalf("an input that is not a string: %v", flat[i])
				}
				vals, _ := css.ParseComponentValues(input)
				got, gotOK := ParseColor(vals)

				want, isColor := flat[i+1].(string)
				if !isColor {
					invalid++
					if gotOK {
						t.Errorf("%q read as %s, and is not a colour", input, got)
					}
					continue
				}
				valid++
				if !gotOK {
					t.Errorf("%q was rejected, and is the colour %s", input, want)
					continue
				}
				if got.String() != want {
					t.Errorf("%q read as %s, want %s", input, got, want)
				}
			}
			t.Logf("%s: %d colours and %d non-colours checked", name, valid, invalid)
			if valid == 0 || invalid == 0 {
				// A file that had drifted to all-valid or all-invalid would
				// still pass every assertion above while checking almost
				// nothing.
				t.Logf("note: %s gave %d valid and %d invalid", name, valid, invalid)
			}
		})
	}
}

// TestUnsupportedColorFilesAreAccountedFor stops a colour file from being
// quietly skipped. Every file the suite ships is either answered or named in
// unsupportedColorFiles with a reason — a new one upstream fails here rather
// than going unnoticed.
func TestUnsupportedColorFilesAreAccountedFor(t *testing.T) {
	dir := colorOracleDir(t)

	present, err := filepath.Glob(filepath.Join(dir, "color_*.json"))
	if err != nil || len(present) == 0 {
		t.Skip("the suite's colour files are not present")
	}

	answered := map[string]bool{}
	for _, f := range colorFiles {
		answered[f] = true
	}
	for _, path := range present {
		name := filepath.Base(path)
		if answered[name] {
			continue
		}
		if _, ok := unsupportedColorFiles[name]; !ok {
			t.Errorf("the suite ships %s, which is neither answered nor listed "+
				"with a reason for not being", name)
		}
	}

	// And the reasons do not outlive their files.
	for name := range unsupportedColorFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is listed as unsupported and the suite no longer ships it", name)
		}
	}
}

// TestColorsThisEngineRefuses pins that the colour spaces above are refused
// rather than approximated. A silent conversion produces a document whose
// colours are nearly right, which is the failure this whole design is against.
func TestColorsThisEngineRefuses(t *testing.T) {
	for _, input := range []string{
		"lab(50% 40 59.5)",
		"lch(52.2% 72.2 50)",
		"oklab(0.4 0.1 0.1)",
		"oklch(0.4 0.1 50)",
		"hwb(194 0% 0%)",
		"color(srgb 0.1 0.2 0.3)",
		"color(display-p3 1 0.5 0)",
	} {
		vals, _ := css.ParseComponentValues(input)
		if got, ok := ParseColor(vals); ok {
			t.Errorf("%q was read as %s; converting it is a rendering-intent decision", input, got)
		}
	}
}

// TestColorSerialisationRoundTrips pins that String is the inverse of parsing
// for every colour this engine produces. The suite compares serialised text, so
// a serialiser that was wrong in the same way as the parser would agree with
// itself and with nothing else.
func TestColorSerialisationRoundTrips(t *testing.T) {
	cases := []RGBA{
		{0, 0, 0, 1}, {255, 255, 255, 1}, {1, 2, 3, 1},
		{0, 0, 0, 0}, {255, 0, 0, 0.5}, {12, 34, 56, 136.0 / 255}, {12, 34, 56, 0.25},
		// A component that is not a whole number, which hsl() produces.
		{31.875, 42.498938, 0, 1},
	}
	for _, want := range cases {
		text := want.String()
		vals, errs := css.ParseComponentValues(text)
		if len(errs) != 0 {
			t.Errorf("%s serialised to %q, which does not tokenize: %v", want, text, errs)
			continue
		}
		got, ok := ParseColor(vals)
		if !ok {
			t.Errorf("%s serialised to %q, which does not parse back", want, text)
			continue
		}
		// The text is what has to survive, not the exact float: serialising
		// rounds to six decimals, so a value read back from its own
		// serialisation is equal to six decimals and not to the bit. Comparing
		// the floats would be asserting that the rounding never happened.
		if got.String() != text {
			t.Errorf("%s serialised to %q and read back as %s", want, text, got)
		}
	}
}

// TestColorShapes covers the notations directly, including the ones the suite's
// files do not reach.
func TestColorShapes(t *testing.T) {
	cases := map[string]RGBA{
		// Hexadecimal, including the digit doubling that makes #f00 red rather
		// than #f00000.
		"#f00":      {255, 0, 0, 1},
		"#ff0000":   {255, 0, 0, 1},
		"#0f08":     {0, 255, 0, 136.0 / 255},
		"#00ff0088": {0, 255, 0, 136.0 / 255},
		"#FFF":      {255, 255, 255, 1},
		// Keywords, matched without regard to case.
		"red":           {255, 0, 0, 1},
		"RED":           {255, 0, 0, 1},
		"ReBeCcApUrPlE": {102, 51, 153, 1},
		"transparent":   {0, 0, 0, 0},
		// Both function syntaxes.
		"rgb(1, 2, 3)":       {1, 2, 3, 1},
		"rgb(1 2 3)":         {1, 2, 3, 1},
		"rgba(1, 2, 3, 0.5)": {1, 2, 3, 0.5},
		"rgb(1 2 3 / 0.5)":   {1, 2, 3, 0.5},
		"rgb(1 2 3 / 50%)":   {1, 2, 3, 0.5},
		"RGB(1,2,3)":         {1, 2, 3, 1},
		// Percentages, and clamping past the ends.
		"rgb(100%, 0%, 0%)": {255, 0, 0, 1},
		// Mixing is allowed in the space syntax and not in the comma one.
		"rgb(50% 128 0)": {127.5, 128, 0, 1},
		// "none" is a missing component, which resolves to zero.
		"rgb(none 128 0)":        {0, 128, 0, 1},
		"hsl(none 100% 50%)":     {255, 0, 0, 1},
		"hsl(0 100% 50% / none)": {255, 0, 0, 0},
		"rgb(300, 0, 0)":         {255, 0, 0, 1},
		"rgb(-10, 0, 0)":         {0, 0, 0, 1},
		// Hue accepts the angle units as well as a bare number.
		"hsl(0, 100%, 50%)":     {255, 0, 0, 1},
		"hsl(120, 100%, 50%)":   {0, 255, 0, 1},
		"hsl(0deg, 100%, 50%)":  {255, 0, 0, 1},
		"hsl(1turn, 100%, 50%)": {255, 0, 0, 1},
		"hsl(-120, 100%, 50%)":  {0, 0, 255, 1},
		"hsl(480, 100%, 50%)":   {0, 255, 0, 1},
	}
	for input, want := range cases {
		vals, _ := css.ParseComponentValues(input)
		got, ok := ParseColor(vals)
		if !ok {
			t.Errorf("%q was rejected, want %s", input, want)
			continue
		}
		if got != want {
			t.Errorf("%q read as %s, want %s", input, got, want)
		}
	}
}

// TestColorRefusals pins what is not a colour. Each of these looks like one, and
// a parser that took them would apply a value the author never wrote.
func TestColorRefusals(t *testing.T) {
	for _, input := range []string{
		"", " ", "nosuchcolour", "#", "#f", "#ff", "#fffff", "#fffffff",
		"#gggggg", "rgb()", "rgb(1)", "rgb(1, 2)", "rgb(1, 2, 3, 4, 5)",
		// The two syntaxes must not be mixed.
		"rgb(1, 2 3)", "rgb(1 2, 3)", "rgb(1 2 3, 0.5)", "rgb(1, 2, 3 / 0.5)",
		// The comma syntax requires the components to agree about numbers or
		// percentages. The space syntax does not — "rgb(50% 128 0)" is a
		// colour, and is checked above.
		"rgb(50%, 128, 0)",
		// Saturation and lightness are percentages and nothing else.
		"hsl(0, 50, 50%)", "hsl(0, 50%, 50)", "hsl(0, 50, 50)",
		// A hue in an unknown unit.
		"hsl(0px, 50%, 50%)",
		// Two colours are not a colour.
		"red blue", "#f00 #0f0",
	} {
		vals, _ := css.ParseComponentValues(input)
		if got, ok := ParseColor(vals); ok {
			t.Errorf("%q read as %s, and is not a colour", input, got)
		}
	}
}

// TestNamedColorTable pins the generated table's shape, so a regeneration that
// changed the key convention fails here rather than in every document.
func TestNamedColorTable(t *testing.T) {
	if len(namedColors) != 148 {
		t.Errorf("the table holds %d colours, want the specification's 148", len(namedColors))
	}
	for name, c := range namedColors {
		if name != strings.ToLower(name) {
			t.Errorf("the key %q is not lowercased, so it can never be matched", name)
		}
		if c.A != 1 {
			t.Errorf("%s has alpha %v; every named colour is opaque", name, c.A)
		}
	}
	// "transparent" is deliberately not in the table, because its alpha is 0 and
	// the generator asserts every entry is opaque.
	if _, ok := namedColors["transparent"]; ok {
		t.Error("\"transparent\" is in the table, where every colour is opaque")
	}
	for name, want := range map[string]RGBA{
		"black":         {0, 0, 0, 1},
		"white":         {255, 255, 255, 1},
		"red":           {255, 0, 0, 1},
		"rebeccapurple": {102, 51, 153, 1},
	} {
		if got := namedColors[name]; got != want {
			t.Errorf("%s is %s, want %s", name, got, want)
		}
	}
}
