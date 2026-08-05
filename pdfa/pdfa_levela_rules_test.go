package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Unit coverage for the Level A rule families that read more than the catalog:
// the language identifier wherever it is written, the structure types and their
// role map, the Unicode character map requirement, replacement text for Private
// Use Area characters, and the tagging of page content.

// levelAPage is a document with one page whose content stream is the given
// bytes, plus whatever structure and resources a test hangs on it.
type levelAPage struct {
	view    core.View
	catalog *object.Dictionary
	page    *object.Dictionary
	next    int
}

func newLevelAPage(content string) *levelAPage {
	b := &levelAPage{next: 10}
	b.view = mkV(core.View{Objects: map[int]*object.IndirectObject{}, Version: "1.4"})

	b.catalog = &object.Dictionary{}
	b.catalog.Set("Type", object.Name("Catalog"))
	b.catalog.Set("Pages", object.IndirectRef{Number: 2})

	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte(content)}
	stream.Dict.Set("Length", object.Integer(len(content)))
	b.view.Objects[4] = &object.IndirectObject{Number: 4, Value: stream}

	b.page = &object.Dictionary{}
	b.page.Set("Type", object.Name("Page"))
	b.page.Set("Contents", object.IndirectRef{Number: 4})
	b.page.Set("Resources", &object.Dictionary{})
	b.view.Objects[3] = &object.IndirectObject{Number: 3, Value: b.page}

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	b.view.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}

	b.view.Objects[1] = &object.IndirectObject{Number: 1, Value: b.catalog}
	*b.view.Trailer = object.Dictionary{}
	b.view.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return b
}

// add stores obj under a fresh object number and returns a reference to it.
func (b *levelAPage) add(obj object.Object) object.IndirectRef {
	n := b.next
	b.next++
	b.view.Objects[n] = &object.IndirectObject{Number: n, Value: obj}
	return object.IndirectRef{Number: n}
}

// structTree hangs a structure tree root carrying roleMap and the given
// elements off the catalog, and returns the root so a test can adjust it.
func (b *levelAPage) structTree(roleMap *object.Dictionary, elems ...object.Object) *object.Dictionary {
	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	if roleMap != nil {
		root.Set("RoleMap", roleMap)
	}
	kids := object.Array{}
	for _, e := range elems {
		kids = append(kids, b.add(e))
	}
	root.Set("K", kids)
	b.catalog.Set("StructTreeRoot", b.add(root))
	return root
}

// structElem is a structure element of the given type.
func structElem(s object.Name, set func(*object.Dictionary)) *object.Dictionary {
	d := &object.Dictionary{}
	d.Set("Type", object.Name("StructElem"))
	d.Set("S", s)
	if set != nil {
		set(d)
	}
	return d
}

func nameDict(pairs ...object.Name) *object.Dictionary {
	d := &object.Dictionary{}
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Set(pairs[i], pairs[i+1])
	}
	return d
}

// --- 6.8.4 / 6.7.4: the language identifier, wherever it is written ---

func TestLevelALanguageReadsAllThreePlaces(t *testing.T) {
	cases := []struct {
		name  string
		build func(*levelAPage)
		want  string
	}{
		{
			name: "structure element",
			build: func(b *levelAPage) {
				b.structTree(nil, structElem("Span", func(d *object.Dictionary) {
					d.Set("Lang", object.String{Value: []byte("az/Latn")})
				}))
			},
			want: "structure element",
		},
		{
			name:  "marked-content property list",
			build: func(b *levelAPage) { b.structTree(nil) },
			want:  "marked-content property list",
		},
	}
	// The property-list case needs the tag in the content stream, which is set
	// by the stream the builder was made with; see below.
	for _, tc := range cases {
		content := "/Span <</MCID 0>> BDC EMC"
		if tc.want == "marked-content property list" {
			content = "/Span <</Lang (de/AT)>> BDC EMC"
		}
		b := newLevelAPage(content)
		tc.build(b)
		v := checkLevelALanguage(b.view, PDFA1a)
		if !hasMsg(v, tc.want) || !hasMsg(v, "not a valid language identifier") {
			t.Errorf("%s: want a finding naming %q, got %v", tc.name, tc.want, v)
		}
	}
}

// TestLevelALanguageDecodesUTF16 pins that the value is decoded before it is
// judged. The corpus writes its Cyrillic tag as UTF-16BE precisely so that a
// checker reading raw bytes sees eight ASCII-looking ones and passes it.
func TestLevelALanguageDecodesUTF16(t *testing.T) {
	b := newLevelAPage("/Span <</Lang <feff0430043d002d00430041>>> BDC EMC")
	b.structTree(nil)
	if v := checkLevelALanguage(b.view, PDFA1a); !hasMsg(v, "not a valid language identifier") {
		t.Errorf("a Cyrillic primary language tag was accepted: %v", v)
	}
}

// TestLevelALanguageSubtagDigitsDifferByPart pins that the two vintages of the
// grammar are genuinely applied per part. "en-12" fails under RFC 1766, which
// ISO 19005-1 cites; "ru-petr1708" passes under RFC 3066, which ISO 19005-2/-3
// cite. Applying one rule to both parts is wrong about one of them, and the
// corpus contains a conforming file of each shape.
func TestLevelALanguageSubtagDigitsDifferByPart(t *testing.T) {
	tag := func(s string) core.View {
		b := newLevelAPage("")
		b.catalog.Set("Lang", object.String{Value: []byte(s)})
		return b.view
	}
	if v := checkLevelALanguage(tag("en-12"), PDFA1a); !hasMsg(v, "not a valid language identifier") {
		t.Errorf("PDF/A-1a accepted a digit subtag: %v", v)
	}
	if v := checkLevelALanguage(tag("ru-petr1708"), PDFA2a); len(v) != 0 {
		t.Errorf("PDF/A-2a rejected a digit subtag: %v", v)
	}
	// The empty string is explicitly permitted at both parts: it says the
	// language is unknown.
	for _, level := range []Level{PDFA1a, PDFA2a} {
		if v := checkLevelALanguage(tag(""), level); len(v) != 0 {
			t.Errorf("%s rejected an empty /Lang: %v", level, v)
		}
	}
}

// --- 6.8.3.4 / 6.7.3.4: structure types and the role map ---

func TestLevelAStructTypesUnmapped(t *testing.T) {
	b := newLevelAPage("")
	b.structTree(nil, structElem("Rectangle", nil))
	if v := checkLevelAStructTypes(b.view, PDFA1a); !hasMsg(v, "not mapped to a standard type") {
		t.Errorf("an unmapped non-standard type was accepted: %v", v)
	}

	mapped := newLevelAPage("")
	mapped.structTree(nameDict("Rectangle", "Figure"), structElem("Rectangle", nil))
	if v := checkLevelAStructTypes(mapped.view, PDFA1a); len(v) != 0 {
		t.Errorf("a mapped non-standard type was flagged: %v", v)
	}
}

func TestLevelAStructTypesCircularMapping(t *testing.T) {
	b := newLevelAPage("")
	b.structTree(nameDict("Rectangle", "Rectangle"), structElem("Rectangle", nil))
	if v := checkLevelAStructTypes(b.view, PDFA1a); !hasMsg(v, "circular mapping") {
		t.Errorf("a self-mapping non-standard type was accepted: %v", v)
	}
}

// TestLevelAStructTypesIgnoreUnusedStandardSelfMaps is the false-positive guard.
// A role map that maps a standard type to itself is a cycle when the map is read
// as a whole, and files carrying one sit in the corpus as *conforming*: the rule
// is about the types a document uses, not about every key in the map.
func TestLevelAStructTypesIgnoreUnusedStandardSelfMaps(t *testing.T) {
	b := newLevelAPage("")
	b.structTree(nameDict("Document", "Document", "Span", "Span"),
		structElem("Document", nil), structElem("Span", nil))
	if v := checkLevelAStructTypes(b.view, PDFA1a); len(v) != 0 {
		t.Errorf("standard types that the role map redundantly self-maps were flagged: %v", v)
	}
}

// --- 6.8.3.2 / 6.7.3.2: page content must be tagged or an artifact ---

func TestLevelAArtifactsUntaggedPainting(t *testing.T) {
	b := newLevelAPage("0 0 0 rg 10 10 100 20 re f")
	b.structTree(nil, structElem("Document", nil))
	if v := checkLevelAArtifacts(b.view, PDFA1a); !hasMsg(v, "outside any marked-content sequence") {
		t.Errorf("untagged painting was accepted: %v", v)
	}
}

func TestLevelAArtifactsAcceptsMarkedContent(t *testing.T) {
	for _, content := range []string{
		"/Artifact BMC 0 0 0 rg 10 10 100 20 re f EMC",
		"/Figure <</MCID 0>> BDC 10 10 100 20 re f EMC",
		// State outside a sequence and painting inside is what every tagged
		// document looks like, and must not be flagged.
		"0.1 w q /Artifact BMC 10 10 100 20 re f EMC Q",
		// Clipping is not painting: "n" ends a path without marking the page.
		"q 0 0 595 842 re W* n Q",
	} {
		b := newLevelAPage(content)
		b.structTree(nil, structElem("Document", nil))
		if v := checkLevelAArtifacts(b.view, PDFA1a); len(v) != 0 {
			t.Errorf("%q was flagged: %v", content, v)
		}
	}
}

// --- 6.3.8 / 6.2.11.7.2: the Unicode character map ---

// levelAFontPage builds a page that shows one byte of text in the given font.
func levelAFontPage(font *object.Dictionary) *levelAPage {
	b := newLevelAPage("BT /F1 12 Tf <41> Tj ET")
	fonts := &object.Dictionary{}
	fonts.Set("F1", b.add(font))
	res := &object.Dictionary{}
	res.Set("Font", fonts)
	b.page.Set("Resources", res)
	return b
}

func simpleFont(subtype object.Name, set func(*object.Dictionary)) *object.Dictionary {
	d := &object.Dictionary{}
	d.Set("Type", object.Name("Font"))
	d.Set("Subtype", subtype)
	d.Set("BaseFont", object.Name("AAAAAA+Test"))
	if set != nil {
		set(d)
	}
	return d
}

func TestLevelAToUnicodeExemptions(t *testing.T) {
	descriptor := func(charSet string, flags int) *object.Dictionary {
		d := &object.Dictionary{}
		d.Set("Type", object.Name("FontDescriptor"))
		d.Set("Flags", object.Integer(flags))
		if charSet != "" {
			d.Set("CharSet", object.String{Value: []byte(charSet)})
		}
		return d
	}
	cases := []struct {
		name   string
		font   *object.Dictionary
		exempt bool
	}{
		{"predefined WinAnsiEncoding", simpleFont("TrueType", func(d *object.Dictionary) {
			d.Set("Encoding", object.Name("WinAnsiEncoding"))
		}), true},
		{"predefined MacExpertEncoding", simpleFont("Type1", func(d *object.Dictionary) {
			d.Set("Encoding", object.Name("MacExpertEncoding"))
		}), true},
		{"standard Latin glyph names", simpleFont("Type1", func(d *object.Dictionary) {
			d.Set("FontDescriptor", descriptor("/period/one/H/a/e/space", 4))
		}), true},
		{"Symbol glyph names", simpleFont("Type1", func(d *object.Dictionary) {
			d.Set("FontDescriptor", descriptor("/angle/infinity/minus/space", 4))
		}), true},
		{"glyph names in neither set", simpleFont("Type1", func(d *object.Dictionary) {
			d.Set("FontDescriptor", descriptor("/integraldisplay/space", 4))
		}), false},
		{"symbolic TrueType with no encoding", simpleFont("TrueType", func(d *object.Dictionary) {
			d.Set("FontDescriptor", descriptor("", 6))
		}), false},
	}
	for _, tc := range cases {
		b := levelAFontPage(tc.font)
		v := checkLevelAToUnicode(b.view, PDFA1a)
		if tc.exempt && len(v) != 0 {
			t.Errorf("%s: exempt font flagged: %v", tc.name, v)
		}
		if !tc.exempt && !hasMsg(v, "no ToUnicode CMap") {
			t.Errorf("%s: want a ToUnicode finding, got %v", tc.name, v)
		}
	}
}

// TestLevelAToUnicodeCompositeCollections pins the third exemption, and pins
// that Identity is not one of them: a CID with the Adobe-Identity ordering
// carries no Unicode meaning, which is the case the rule exists for.
func TestLevelAToUnicodeCompositeCollections(t *testing.T) {
	composite := func(ordering string) *levelAPage {
		b := newLevelAPage("BT /F1 12 Tf <0041> Tj ET")
		csi := &object.Dictionary{}
		csi.Set("Registry", object.String{Value: []byte("Adobe")})
		csi.Set("Ordering", object.String{Value: []byte(ordering)})
		csi.Set("Supplement", object.Integer(0))
		desc := &object.Dictionary{}
		desc.Set("Type", object.Name("Font"))
		desc.Set("Subtype", object.Name("CIDFontType2"))
		desc.Set("CIDSystemInfo", csi)
		font := simpleFont("Type0", func(d *object.Dictionary) {
			d.Set("Encoding", object.Name("Identity-H"))
			d.Set("DescendantFonts", object.Array{b.add(desc)})
		})
		fonts := &object.Dictionary{}
		fonts.Set("F1", b.add(font))
		res := &object.Dictionary{}
		res.Set("Font", fonts)
		b.page.Set("Resources", res)
		return b
	}
	for _, ordering := range []string{"Japan1", "Korea1", "GB1", "CNS1"} {
		if v := checkLevelAToUnicode(composite(ordering).view, PDFA1a); len(v) != 0 {
			t.Errorf("Adobe-%s was flagged: %v", ordering, v)
		}
	}
	if v := checkLevelAToUnicode(composite("Identity").view, PDFA1a); !hasMsg(v, "no ToUnicode CMap") {
		t.Errorf("an Identity-ordered composite font without ToUnicode was accepted: %v", v)
	}
}

// --- 6.2.11.7.3: replacement text for Private Use Area characters ---

// puaPage builds a page showing one character that a ToUnicode CMap maps to
// dest, inside the given marked-content wrapper.
func puaPage(dest, open, close string) *levelAPage {
	cmap := "begincmap\n1 beginbfchar\n<01> <" + dest + "> endbfchar\nendcmap\n"
	content := open + " BT /F1 12 Tf <01> Tj ET " + close
	b := newLevelAPage(content)
	tu := &object.Stream{Dict: object.Dictionary{}, Data: []byte(cmap)}
	tu.Dict.Set("Length", object.Integer(len(cmap)))
	font := simpleFont("TrueType", func(d *object.Dictionary) {
		d.Set("ToUnicode", b.add(tu))
	})
	fonts := &object.Dictionary{}
	fonts.Set("F1", b.add(font))
	res := &object.Dictionary{}
	res.Set("Font", fonts)
	b.page.Set("Resources", res)
	return b
}

func TestLevelAActualTextForPrivateUseArea(t *testing.T) {
	cases := []struct {
		name    string
		dest    string
		open    string
		close   string
		flagged bool
	}{
		{"BMP PUA, no ActualText", "E000", "/Span <</MCID 0>> BDC", "EMC", true},
		{"supplementary PUA-B, no ActualText", "DBC0DD6D", "/Span <</MCID 0>> BDC", "EMC", true},
		{"ActualText on the enclosing sequence", "E000", "/Span <</ActualText <feff0041>>> BDC", "EMC", false},
		{"ActualText on the same sequence", "E000", "/Span <</MCID 0 /ActualText <feff0041>>> BDC", "EMC", false},
		{"not a Private Use Area code point", "0041", "/Span <</MCID 0>> BDC", "EMC", false},
		// A sequence that was closed before the text is not an enclosing one.
		{"ActualText in a closed sequence", "E000",
			"/Span <</ActualText <feff0041>>> BDC EMC /Span <</MCID 0>> BDC", "EMC", true},
	}
	for _, tc := range cases {
		b := puaPage(tc.dest, tc.open, tc.close)
		b.structTree(nil)
		v := checkLevelAActualText(b.view, PDFA2a)
		if tc.flagged && !hasMsg(v, "Private Use Area") {
			t.Errorf("%s: want a finding, got %v", tc.name, v)
		}
		if !tc.flagged && len(v) != 0 {
			t.Errorf("%s: unexpected finding %v", tc.name, v)
		}
	}
}

// TestLevelAActualTextFromTheStructureElement pins the other place replacement
// text may live: on the structure element the marked-content sequence belongs
// to, reached through its /MCID.
func TestLevelAActualTextFromTheStructureElement(t *testing.T) {
	b := puaPage("E000", "/Span <</MCID 7>> BDC", "EMC")
	b.structTree(nil, structElem("Span", func(d *object.Dictionary) {
		d.Set("Pg", object.IndirectRef{Number: 3})
		d.Set("ActualText", object.String{Value: []byte("A")})
		d.Set("K", object.Integer(7))
	}))
	if v := checkLevelAActualText(b.view, PDFA2a); len(v) != 0 {
		t.Errorf("a character covered by a structure element's ActualText was flagged: %v", v)
	}

	// The same tree, but the replacement text covers a different sequence.
	other := puaPage("E000", "/Span <</MCID 7>> BDC", "EMC")
	other.structTree(nil, structElem("Span", func(d *object.Dictionary) {
		d.Set("Pg", object.IndirectRef{Number: 3})
		d.Set("ActualText", object.String{Value: []byte("A")})
		d.Set("K", object.Integer(8))
	}))
	if v := checkLevelAActualText(other.view, PDFA2a); !hasMsg(v, "Private Use Area") {
		t.Errorf("replacement text on another sequence was taken as cover: %v", v)
	}
}

// TestLevelAActualTextIsPartsTwoAndThreeOnly pins that ISO 19005-1 states no
// such requirement, so a part-1 file is not judged against it.
func TestLevelAActualTextIsPartsTwoAndThreeOnly(t *testing.T) {
	b := puaPage("E000", "/Span <</MCID 0>> BDC", "EMC")
	b.structTree(nil)
	if v := checkLevelAActualText(b.view, PDFA1a); len(v) != 0 {
		t.Errorf("PDF/A-1a applied the Private Use Area rule: %v", v)
	}
}
