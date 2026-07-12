package pdf0

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test cross-checks pdf0's hand-coded XMP schema tables (xmp_schemas.go)
// against the ISO 16684 RELAX NG schemas vendored under testdata/xmp-rng/ (from
// github.com/ceztko/XMP-RNG-Schema, MIT). It is a regression guard: if the hand
// tables ever drift from the authoritative schemas — a property gains the wrong
// value type, or is added/removed — this fails. Accepted, deliberate deviations
// are listed in xmpRNGAllowlist with a rationale. The schemas are used only by
// this test; the library remains dependency-free.

// --- generic RELAX NG node tree ---

type rngNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Kids    []rngNode  `xml:",any"`
}

func (n rngNode) local() string { return n.XMLName.Local }
func (n rngNode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// rngType is a normalized property type: form ("simple","seq","bag","alt",
// "struct","custom") plus a value class ("text","int","real","rational","bool",
// "date"; empty for struct/custom).
type rngType struct{ form, val string }

func (t rngType) String() string {
	if t.val == "" {
		return t.form
	}
	return t.form + "/" + t.val
}

// condHolds evaluates a ceztko `condition` expression for a PDF/A level number.
func condHolds(cond string, level int) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}
	for _, term := range strings.Split(cond, " or ") {
		if oneCondTerm(strings.TrimSpace(term), level) {
			return true
		}
	}
	return false
}

func oneCondTerm(term string, level int) bool {
	term = strings.TrimPrefix(term, "$IsPDFA")
	if n := strings.TrimSuffix(term, "OrGreater"); n != term {
		return level >= int(n[0]-'0')
	}
	if len(term) == 1 && term[0] >= '1' && term[0] <= '9' {
		return level == int(term[0]-'0')
	}
	return false // unknown expression: treat as not-applicable
}

// xmpRNGSchema parses one vendored property file.
type xmpRNGSchema struct {
	nsURI   string
	defines map[string]rngNode // define name -> define node
	topLvl  map[string]string  // top-level property define name -> condition
}

func parseXMPRNG(t *testing.T, path string) *xmpRNGSchema {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var g rngNode
	if err := xml.Unmarshal(b, &g); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	s := &xmpRNGSchema{defines: map[string]rngNode{}, topLvl: map[string]string{}}
	base := strings.TrimSuffix(filepath.Base(path), ".rng") // XMP_Properties-<ns>
	ns := strings.TrimPrefix(base, "XMP_Properties-")
	// namespace prefix -> URI from grammar xmlns; the property prefix equals ns
	// except a couple of aliases.
	pfx := map[string]string{"exif_aux": "aux", "pdf": "pdf"}
	wantPrefix := ns
	if p, ok := pfx[ns]; ok {
		wantPrefix = p
	}
	for _, a := range g.Attrs {
		if a.Name.Space == "xmlns" && a.Name.Local == wantPrefix {
			s.nsURI = a.Value
		}
	}
	for _, d := range g.Kids {
		if d.local() == "define" {
			s.defines[d.attr("name")] = d
		}
	}
	// top-level property set: refs inside the "XMP_Properties-<ns>" interleave
	root := s.defines["XMP_Properties-"+ns]
	collectRefs(root, "", func(refName, cond string) {
		if strings.Contains(refName, ".") { // property defines look like "dc.creator"
			s.topLvl[refName] = cond
		}
	})
	return s
}

// collectRefs walks a subtree gathering rng:ref names with the effective
// condition (nearest ancestor/self condition attr).
func collectRefs(n rngNode, cond string, f func(refName, cond string)) {
	if c := n.attr("condition"); c != "" {
		cond = c
	}
	if n.local() == "ref" {
		f(n.attr("name"), cond)
		return
	}
	for _, k := range n.Kids {
		collectRefs(k, cond, f)
	}
}

// typeOf resolves a top-level property's value type at a PDF/A level.
func (s *xmpRNGSchema) typeOf(propDefine string, level int) (rngType, bool) {
	d, ok := s.defines[propDefine]
	if !ok {
		return rngType{}, false
	}
	// find the rng:element; its type is the first type-bearing child whose
	// condition holds (a ref to a QValue/custom type, or an inline data/choice).
	var elem *rngNode
	for i := range d.Kids {
		if d.Kids[i].local() == "element" {
			elem = &d.Kids[i]
			break
		}
	}
	if elem == nil {
		return rngType{}, false
	}
	for _, k := range elem.Kids {
		if condHolds(k.attr("condition"), level) {
			if tt, ok := s.resolveTypeNode(k, level, 0); ok {
				return tt, true
			}
		}
	}
	return rngType{}, false
}

// resolveTypeNode maps a type-bearing node (a ref or inline definition) to an
// rngType, resolving custom refs a bounded number of hops.
func (s *xmpRNGSchema) resolveTypeNode(n rngNode, level, depth int) (rngType, bool) {
	if depth > 8 {
		return rngType{"custom", ""}, true
	}
	switch n.local() {
	case "ref":
		name := n.attr("name")
		if q := strings.TrimPrefix(name, "ISO16684-1.Types.QValue."); q != name {
			return qvalueType(q), true
		}
		def, ok := s.defines[name]
		if !ok {
			return rngType{"custom", ""}, true
		}
		// resolve into the referenced define, skipping the rdf:Description /
		// rdf:value qualified-value wrapper (same base type).
		return s.resolveInside(def, level, depth+1)
	case "data", "value":
		return rngType{"simple", xsdClass(n.attr("type"))}, true
	case "element", "choice":
		return s.resolveInside(n, level, depth+1)
	}
	return rngType{"custom", ""}, true
}

func (s *xmpRNGSchema) resolveInside(n rngNode, level, depth int) (rngType, bool) {
	for _, k := range n.Kids {
		switch k.local() {
		case "ref":
			name := k.attr("name")
			// skip the rdf:value wrapper's own element; follow real type refs
			if strings.HasPrefix(name, "rdf:") {
				continue
			}
			if tt, ok := s.resolveTypeNode(k, level, depth); ok && tt.form != "custom" {
				return tt, true
			}
		case "data", "value":
			return rngType{"simple", xsdClass(k.attr("type"))}, true
		case "choice", "element":
			// descend (handles the rdf:Description/rdf:value nesting)
			if k.local() == "element" && k.attr("name") != "rdf:Description" && k.attr("name") != "rdf:value" {
				// a non-wrapper element inside means a struct
				return rngType{"struct", ""}, true
			}
			if tt, ok := s.resolveInside(k, level, depth+1); ok && tt.form != "custom" {
				return tt, true
			}
		}
	}
	return rngType{"custom", ""}, true
}

func qvalueType(q string) rngType {
	switch {
	case strings.HasPrefix(q, "OrderedArray."):
		return rngType{"seq", valBucket(strings.TrimPrefix(q, "OrderedArray."))}
	case strings.HasPrefix(q, "UnorderedArray."):
		return rngType{"bag", valBucket(strings.TrimPrefix(q, "UnorderedArray."))}
	case q == "LanguageAlternative":
		return rngType{"alt", "text+lang"}
	case q == "ResourceRef":
		return rngType{"struct", ""}
	default:
		return rngType{"simple", valBucket(q)}
	}
}

func valBucket(t string) string {
	switch t {
	case "Date":
		return "date"
	case "Integer":
		return "int"
	case "Real":
		return "real"
	case "Rational":
		return "rational"
	case "Boolean":
		return "bool"
	default:
		return "text"
	}
}

func xsdClass(t string) string {
	switch t {
	case "double", "float", "decimal":
		return "real"
	case "int", "integer", "nonNegativeInteger", "positiveInteger", "long", "short":
		return "int"
	case "boolean":
		return "bool"
	case "date", "dateTime":
		return "date"
	default:
		return "text"
	}
}

// pdfTypeString renders a pdf0 xmpPropType in the same normalized vocabulary.
func pdfTypeString(p xmpPropType) rngType {
	syn := func(s xmpSyntax) string {
		switch s {
		case synInteger:
			return "int"
		case synReal:
			return "real"
		case synRational:
			return "rational"
		case synDate:
			return "date"
		case synBoolean:
			return "bool"
		default:
			return "text"
		}
	}
	switch p.Form {
	case xmpSimple:
		return rngType{"simple", syn(p.Syntax)}
	case xmpStruct:
		return rngType{"struct", ""}
	case xmpBag:
		if p.Syntax < 0 {
			return rngType{"bag", "struct"}
		}
		return rngType{"bag", syn(p.Syntax)}
	case xmpSeq:
		if p.Syntax < 0 {
			return rngType{"seq", "struct"}
		}
		return rngType{"seq", syn(p.Syntax)}
	case xmpAlt:
		if p.Lang {
			return rngType{"alt", "text+lang"}
		}
		return rngType{"alt", syn(p.Syntax)}
	}
	return rngType{"custom", ""}
}

// completeNamespaces are the XMP schemas where pdf0's table and the RNG are
// expected to have the same set of (scalar) properties, so the guard also
// enforces presence parity there. The large media/raw namespaces (xmpDM, exif,
// crs) are deliberately partial in pdf0 and are checked for type drift only.
var completeNamespaces = map[string]bool{
	nsDC: true, nsXMPBasic: true, nsXMPRights: true, nsAdobePDF: true,
	nsTIFF: true, nsPhotoshop: true, nsPDFAID: true, nsXMPTPg: true, nsXMPBJ: true,
}

func TestXMPTablesMatchRNG(t *testing.T) {
	dir := "testdata/xmp-rng"
	files, _ := filepath.Glob(filepath.Join(dir, "XMP_Properties-*.rng"))
	if len(files) == 0 {
		t.Skip("vendored XMP RNG schemas not present")
	}
	schemas := map[string]*xmpRNGSchema{} // nsURI -> schema
	for _, f := range files {
		s := parseXMPRNG(t, f)
		if s.nsURI != "" {
			schemas[s.nsURI] = s
		}
	}

	levels := []struct {
		lvl PDFALevel
		n   int
	}{{PDFA1b, 1}, {PDFA2b, 2}}

	for _, L := range levels {
		pdf := predefinedXMPSchemas(L.lvl)

		for nsURI, sc := range schemas {
			pt := pdf[nsURI]
			if pt == nil {
				continue // pdf0 does not model this namespace at this level
			}
			// RNG property types at this level, by bare name.
			rngAt := map[string]rngType{}
			for def, cond := range sc.topLvl {
				if !condHolds(cond, L.n) {
					continue
				}
				if rt, ok := sc.typeOf(def, L.n); ok {
					rngAt[bareName(def)] = rt
				}
			}

			// (a) type-drift: every pdf0 property the RNG also types as a clean
			// scalar/array/alt must agree.
			for prop, pv := range pt {
				if allowed(nsURI, prop) {
					continue
				}
				rt, ok := rngAt[prop]
				if !ok || isStructLike(rt) {
					continue // RNG lacks it or types it as a struct/custom
				}
				got := pdfTypeString(pv)
				if isStructLike(got) {
					continue
				}
				if !typesEquivalent(got, rt) {
					t.Errorf("L%d type drift: %s:%s pdf0=%s rng=%s (allowlist if intended)", L.n, nsShort(nsURI), prop, got, rt)
				}
			}

			// (b) presence parity, only for the complete namespaces and only for
			// properties the RNG types as clean scalars/arrays.
			if completeNamespaces[nsURI] {
				for prop, rt := range rngAt {
					if isStructLike(rt) || allowed(nsURI, prop) {
						continue
					}
					if _, has := pt[prop]; !has {
						t.Errorf("L%d RNG defines %s:%s (%s) but pdf0 table omits it", L.n, nsShort(nsURI), prop, rt)
					}
				}
			}
		}
	}
}

func bareName(def string) string {
	if i := strings.Index(def, "."); i >= 0 {
		return def[i+1:]
	}
	return def
}

func isStructLike(t rngType) bool {
	return t.form == "struct" || t.form == "custom" || t.val == "struct"
}

func allowed(nsURI, prop string) bool { return xmpRNGAllowlist[nsURI+"/"+prop] != "" }

func nsShort(uri string) string {
	switch uri {
	case nsDC:
		return "dc"
	case nsXMPBasic:
		return "xmp"
	case nsXMPRights:
		return "xmpRights"
	case nsXMPMM:
		return "xmpMM"
	case nsXMPBJ:
		return "xmpBJ"
	case nsXMPTPg:
		return "xmpTPg"
	case nsAdobePDF:
		return "pdf"
	case nsPhotoshop:
		return "photoshop"
	case nsTIFF:
		return "tiff"
	case nsEXIF:
		return "exif"
	case nsEXIFAux:
		return "aux"
	case nsXMPDM:
		return "xmpDM"
	case nsCameraRaw:
		return "crs"
	case nsPDFAID:
		return "pdfaid"
	}
	return uri
}

// typesEquivalent treats the specialized text subtypes (already bucketed to
// "text") as equal; everything else must match exactly.
func typesEquivalent(a, b rngType) bool { return a == b }

// xmpRNGAllowlist records deliberate, understood deviations, each verified in
// the ISO 16684 / EXIF / PDF-A cross-check. Key is "<nsURI>/<prop>".
var xmpRNGAllowlist = map[string]string{
	// xmp:Rating is a closed choice of Real in ISO 16684-1 (−1, 0..5); pdf0 uses
	// Real. The ceztko RNG types it Integer at A-2/3 (and even flags its own
	// uncertainty with a CHECK-ME comment), Real at A-4. pdf0 is spec-correct.
	nsXMPBasic + "/Rating": "ISO 16684-1: Rating is Real; RNG A-2/3 Integer is a questioned simplification",
	// exif:GPSDestDistance is RATIONAL per the EXIF spec; pdf0 uses Rational. The
	// RNG relaxes it to Text (accept-anything). pdf0 is the stricter, correct one.
	nsEXIF + "/GPSDestDistance": "EXIF spec: RATIONAL; RNG relaxes to Text",
	// pdfaid:part / :rev are positive integers per PDF/A; pdf0 validates them as
	// Integer. The RNG models them as a closed choice of the string values
	// "1".."4"; pdf0's Integer typing is spec-aligned.
	nsPDFAID + "/part": "PDF/A: part is an integer; RNG uses a closed string choice",
	nsPDFAID + "/rev":  "PDF/A: rev is an integer; RNG uses a string",
}
