package simplefont

import "sync"

// IsStandardLatin reports whether a glyph name belongs to the standard Latin
// character set — the repertoire ISO 32000-2 Annex D.2 tabulates and that
// ISO 19005-1 6.3.8 names as one of the two vocabularies a font may draw on
// without carrying a ToUnicode CMap.
//
// The set is derived from the encoding tables rather than written out again.
// D.2 is one table with four encoding columns, and StandardEncoding, MacRoman
// and WinAnsi between them name every glyph in it; PDFDocEncoding, the fourth
// column, adds no glyph the other three lack. Deriving it keeps the repertoire
// and the encodings from drifting apart, which two hand-maintained lists of the
// same 200-odd names would eventually do.
func IsStandardLatin(name string) bool {
	return standardLatinNames()[name]
}

var standardLatinNames = sync.OnceValue(func() map[string]bool {
	m := make(map[string]bool, 256)
	for _, e := range []Encoding{StandardEncoding, MacRomanEncoding, WinAnsiEncoding} {
		for _, n := range e.Codes() {
			if n != ".notdef" {
				m[n] = true
			}
		}
	}
	return m
})

// IsSymbolSet reports whether a glyph name belongs to the repertoire of the
// Symbol font — the "set of named characters in the Symbol font" that ISO
// 32000-2 Annex D.5 defines and that ISO 19005-1 6.3.8 names as the other
// glyph-name vocabulary a font may draw on without also carrying a ToUnicode
// CMap.
//
// It is the font's repertoire, not an encoding: several of these glyphs (the
// bracket and integral pieces, apple) sit outside Symbol's own encoding vector
// and are reachable only through a Differences array, and the rule is about the
// names a font uses, not the codes it uses them at.
func IsSymbolSet(name string) bool {
	return symbolSetNames[name]
}

var symbolSetNames = map[string]bool{
	"space": true, "exclam": true, "universal": true, "numbersign": true, "existential": true,
	"percent": true, "ampersand": true, "suchthat": true, "parenleft": true, "parenright": true,
	"asteriskmath": true, "plus": true, "comma": true, "minus": true, "period": true, "slash": true, "zero": true, "one": true, "two": true, "three": true, "four": true, "five": true,
	"six": true, "seven": true, "eight": true, "nine": true, "colon": true, "semicolon": true,
	"less": true, "equal": true, "greater": true, "question": true,
	"congruent": true, "Alpha": true, "Beta": true, "Chi": true, "Delta": true, "Epsilon": true,
	"Phi": true, "Gamma": true, "Eta": true, "Iota": true, "theta1": true, "Kappa": true,
	"Lambda": true, "Mu": true, "Nu": true, "Omicron": true, "Pi": true, "Theta": true, "Rho": true, "Sigma": true, "Tau": true, "Upsilon": true, "sigma1": true, "Omega": true, "Xi": true, "Psi": true, "Zeta": true, "bracketleft": true, "therefore": true, "bracketright": true, "perpendicular": true, "underscore": true, "radicalex": true,
	"alpha": true, "beta": true, "chi": true, "delta": true, "epsilon": true, "phi": true,
	"gamma": true, "eta": true, "iota": true, "phi1": true, "kappa": true, "lambda": true, "mu": true, "nu": true, "omicron": true, "pi": true, "theta": true, "rho": true, "sigma": true,
	"tau": true, "upsilon": true, "omega1": true, "omega": true, "xi": true, "psi": true,
	"zeta": true, "braceleft": true, "bar": true, "braceright": true, "similar": true,
	"Euro": true, "Upsilon1": true, "minute": true, "lessequal": true, "fraction": true,
	"infinity": true, "florin": true, "club": true, "diamond": true, "heart": true, "spade": true, "arrowboth": true, "arrowleft": true, "arrowup": true, "arrowright": true,
	"arrowdown": true, "degree": true, "plusminus": true, "second": true, "greaterequal": true,
	"multiply": true, "proportional": true, "partialdiff": true, "bullet": true, "divide": true,
	"notequal": true, "equivalence": true, "approxequal": true, "ellipsis": true, "arrowvertex": true, "arrowhorizex": true, "carriagereturn": true,
	"aleph": true, "Ifraktur": true, "Rfraktur": true, "weierstrass": true, "circlemultiply": true, "circleplus": true, "emptyset": true, "intersection": true, "union": true,
	"propersuperset": true, "reflexsuperset": true, "notsubset": true, "propersubset": true,
	"reflexsubset": true, "element": true, "notelement": true, "angle": true, "gradient": true,
	"registerserif": true, "copyrightserif": true, "trademarkserif": true, "product": true,
	"radical": true, "dotmath": true, "logicalnot": true, "logicaland": true, "logicalor": true,
	"arrowdblboth": true, "arrowdblleft": true, "arrowdblup": true, "arrowdblright": true,
	"arrowdbldown": true,
	"lozenge":      true, "angleleft": true, "registersans": true, "copyrightsans": true,
	"trademarksans": true, "summation": true, "parenlefttp": true, "parenleftex": true,
	"parenleftbt": true, "bracketlefttp": true, "bracketleftex": true, "bracketleftbt": true,
	"bracelefttp": true, "braceleftmid": true, "braceleftbt": true, "braceex": true,
	"angleright": true, "integral": true, "integraltp": true, "integralex": true, "integralbt": true, "parenrighttp": true, "parenrightex": true, "parenrightbt": true, "bracketrighttp": true, "bracketrightex": true, "bracketrightbt": true, "bracerighttp": true, "bracerightmid": true, "bracerightbt": true, "apple": true,
}
