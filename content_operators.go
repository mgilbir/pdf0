package pdf0

import "fmt"

// contentOperators is the set of operators permitted in PDF content streams
// (ISO 32000-1 Annex A, Table A.1). PDF/A forbids any operator outside this
// set, even inside a BX/EX compatibility section. PDF 2.0 (ISO 32000-2)
// defines the same content-stream operator set.
var contentOperators = map[string]bool{
	// Graphics state
	"q": true, "Q": true, "cm": true, "w": true, "J": true, "j": true,
	"M": true, "d": true, "ri": true, "i": true, "gs": true,
	// Path construction
	"m": true, "l": true, "c": true, "v": true, "y": true, "h": true, "re": true,
	// Path painting
	"S": true, "s": true, "f": true, "F": true, "f*": true, "B": true,
	"B*": true, "b": true, "b*": true, "n": true,
	// Clipping
	"W": true, "W*": true,
	// Text objects
	"BT": true, "ET": true,
	// Text state
	"Tc": true, "Tw": true, "Tz": true, "TL": true, "Tf": true, "Tr": true, "Ts": true,
	// Text positioning
	"Td": true, "TD": true, "Tm": true, "T*": true,
	// Text showing
	"Tj": true, "TJ": true, "'": true, "\"": true,
	// Type 3 fonts
	"d0": true, "d1": true,
	// Color
	"CS": true, "cs": true, "SC": true, "SCN": true, "sc": true, "scn": true,
	"G": true, "g": true, "RG": true, "rg": true, "K": true, "k": true,
	// Shading patterns
	"sh": true,
	// Inline images
	"BI": true, "ID": true, "EI": true,
	// XObjects
	"Do": true,
	// Marked content
	"MP": true, "DP": true, "BMC": true, "BDC": true, "EMC": true,
	// Compatibility
	"BX": true, "EX": true,
}

// standardRenderingIntents are the four rendering intent names permitted as
// the operand of the ri operator and the /Intent key (ISO 32000-1 8.6.5.8,
// Table 70).
var standardRenderingIntents = map[string]bool{
	"AbsoluteColorimetric": true,
	"RelativeColorimetric": true,
	"Saturation":           true,
	"Perceptual":           true,
}

// checkContentStreamOperators verifies that every content stream uses only
// operators defined in ISO 32000 (6.2.2), that the ri operator's operand is
// a standard rendering intent, and that named XObject/resource references
// resolve within the associated resource dictionary.
func checkContentStreamOperators(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.2.2"
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	// Only EXECUTED content is validated: an operator inside a form XObject
	// that no content stream invokes does not appear on the page (the
	// corpus passes an UnknownOperator in an uninvoked form).
	seenContainer := map[*Dictionary]bool{}
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		data := getContentStreamData(doc, page.dict.Get("Contents"))
		walkExecutedContent(doc, page.dict, data, page.objNum, seenContainer, add)
	}
	return errs
}

// walkExecutedContent validates a content stream and recurses into the form
// XObjects and tiling patterns it actually invokes.
func walkExecutedContent(doc *Document, container *Dictionary, data []byte, objNum int, seen map[*Dictionary]bool, add func(string, int)) {
	if container == nil || seen[container] {
		return
	}
	seen[container] = true
	res := resolveResources(doc, container)
	if data != nil {
		checkContentTokens(data, res, doc, objNum, add)
	}
	if res == nil {
		return
	}
	used := contentUsedNames(data)
	if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
		for i, key := range xobj.Keys {
			if !used.xobjects[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(xobj.Values[i]).(*Stream); ok {
				st, _ := s.Dict.Get("Subtype").(Name)
				xnum := resolveObjNum(doc, xobj.Values[i])
				// A PostScript XObject that is actually drawn is prohibited
				// (ISO 19005-1 6.2.5, -2/-3/-4 6.2.9).
				if st == "PS" {
					add("a drawn PostScript XObject is not permitted", xnum)
				} else if st == "Form" {
					if s2, _ := s.Dict.Get("Subtype2").(Name); s2 == "PS" {
						add("a drawn form XObject has /Subtype2 /PS (PostScript)", xnum)
					}
					if s.Dict.Get("PS") != nil {
						add("a drawn form XObject dictionary contains a /PS entry", xnum)
					}
					walkExecutedContent(doc, &s.Dict, decodeContentStream(doc, s), xnum, seen, add)
				}
			}
		}
	}
	if pat := doc.ResolveDict(res.Get("Pattern")); pat != nil {
		for i, key := range pat.Keys {
			if !used.patterns[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(pat.Values[i]).(*Stream); ok {
				walkExecutedContent(doc, &s.Dict, decodeContentStream(doc, s), i, seen, add)
			}
		}
	}
}

// checkContentTokens scans one content stream for undefined operators, custom
// rendering intents, and unresolved named resource references.
func checkContentTokens(data []byte, res *Dictionary, doc *Document, objNum int, add func(string, int)) {
	var lastName string
	forEachContentToken(data, func(tok []byte, isName bool) {
		if isName {
			lastName = string(tok)
			return
		}
		s := string(tok)
		if isContentOperand(s) {
			return
		}
		if !contentOperators[s] {
			add(fmt.Sprintf("content stream contains an operator %q not defined in ISO 32000", s), objNum)
			return
		}
		switch s {
		case "ri":
			if lastName != "" && !standardRenderingIntents[lastName] {
				add(fmt.Sprintf("rendering intent operator (ri) uses a non-standard value /%s", lastName), objNum)
			}
		case "Do":
			if !namedResourcePresent(doc, res, "XObject", lastName) {
				add("content stream references an XObject that is absent from the resource dictionary", objNum)
			}
		case "sh":
			if !namedResourcePresent(doc, res, "Shading", lastName) {
				add("content stream references a shading that is absent from the resource dictionary", objNum)
			}
		case "gs":
			if !namedResourcePresent(doc, res, "ExtGState", lastName) {
				add("content stream references an ExtGState that is absent from the resource dictionary", objNum)
			}
		case "cs", "CS":
			// The colour-space operand is either a built-in device space or
			// a name defined in the Resources /ColorSpace dictionary.
			if !builtinColorSpaceName[lastName] && !namedResourcePresent(doc, res, "ColorSpace", lastName) {
				add("content stream references a colour space that is absent from the resource dictionary", objNum)
			}
		case "Tf":
			if !namedResourcePresent(doc, res, "Font", lastName) {
				add("content stream references a font that is absent from the resource dictionary", objNum)
			}
		}
	})
}

// isContentOperand reports whether a content token is an operand (number,
// boolean, or null) rather than an operator.
func isContentOperand(s string) bool {
	if s == "true" || s == "false" || s == "null" {
		return true
	}
	if s == "" {
		return true
	}
	c := s[0]
	return c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// namedResourcePresent reports whether a named resource of the given category
// exists in the resource dictionary.
func namedResourcePresent(doc *Document, res *Dictionary, category, name string) bool {
	if name == "" {
		return true // no name captured; do not flag
	}
	if res == nil {
		return false
	}
	sub := doc.ResolveDict(res.Get(Name(category)))
	if sub == nil {
		return false
	}
	return sub.Get(Name(name)) != nil
}

// builtinColorSpaceName lists the colour-space names selectable with cs/CS
// without a Resources entry (ISO 32000-1 8.6.3).
var builtinColorSpaceName = map[string]bool{
	"DeviceGray": true, "DeviceRGB": true, "DeviceCMYK": true, "Pattern": true,
}

// resolveObjNum returns the object number of an indirect reference, or 0.
func resolveObjNum(doc *Document, o Object) int {
	if ref, ok := o.(IndirectRef); ok {
		return ref.Number
	}
	return 0
}
