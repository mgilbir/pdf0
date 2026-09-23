package xmp

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func wrap(descAttrs, body string) string {
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about=""` + descAttrs + `>` + body + `
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`
}

func mustParse(t *testing.T, s string) *Packet {
	t.Helper()
	p, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func mustBytes(t *testing.T, p *Packet) []byte {
	t.Helper()
	b, err := p.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return b
}

// An unmodified packet comes back byte for byte, whatever odd-but-legal
// spelling it uses.
func TestUnmodifiedRoundTripIsByteExact(t *testing.T) {
	src := "\xEF\xBB\xBF" + wrap(` xmlns:dc = 'http://purl.org/dc/elements/1.1/'`,
		`<!-- a comment --><dc:title><rdf:Alt><rdf:li xml:lang='x-default'>A &amp; B &#233;</rdf:li></rdf:Alt></dc:title>
      <foo:bar xmlns:foo="urn:foo" foo:q="1"/><![CDATA[<x>]]>`)
	p := mustParse(t, src)
	if got := mustBytes(t, p); string(got) != src {
		t.Fatalf("round trip changed the bytes:\n got %q\nwant %q", got, src)
	}
}

// C141: the identification is read by the parser, not by substring, so the
// scraper's traps do not catch it.
func TestIdentificationReadByParser(t *testing.T) {
	cases := map[string]string{
		"comment before the value": wrap(` xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"`,
			`<!-- was <pdfaid:conformance>B</pdfaid:conformance> --><pdfaid:conformance>A</pdfaid:conformance>`),
		"whitespace around =": wrap(` xmlns:pdfaid = "http://www.aiim.org/pdfa/ns/id/" pdfaid:conformance = "A"`, ``),
		"attribute on the element": wrap(` xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"`,
			`<pdfaid:conformance rdf:datatype="x">A</pdfaid:conformance>`),
		"other prefix": wrap(` xmlns:id="http://www.aiim.org/pdfa/ns/id/"`, `<id:conformance>A</id:conformance>`),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			p := mustParse(t, src)
			got, ok := p.Text(NSPDFAID, "conformance")
			if !ok || got != "A" {
				t.Fatalf("conformance = %q, %v; want A", got, ok)
			}
		})
	}
	// And a prefix that merely looks right is not the namespace.
	p := mustParse(t, wrap(` xmlns:pdfaid="urn:not-pdfaid"`, `<pdfaid:part>1</pdfaid:part>`))
	if _, ok := p.Text(NSPDFAID, "part"); ok {
		t.Fatal("a pdfaid prefix bound to another namespace was read as pdfaid:part")
	}
}

// C35: values come back unescaped, and dc:title's x-default item is the one
// read, not the first.
func TestValuesAreUnescapedAndXDefaultWins(t *testing.T) {
	p := mustParse(t, wrap(` xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		`<dc:title><rdf:Alt><rdf:li xml:lang="de">Titel</rdf:li><rdf:li xml:lang="x-default">Smith &amp; Sons &lt;&#62; &#x43;af&#233;</rdf:li></rdf:Alt></dc:title>`))
	pr, ok := p.Get(NSDC, "title")
	if !ok {
		t.Fatal("no dc:title")
	}
	got, _ := pr.Value.AltText("x-default")
	if want := "Smith & Sons <> Café"; got != want {
		t.Fatalf("x-default title = %q, want %q", got, want)
	}
}

func TestSetTextEditsInPlaceAndKeepsEverythingElse(t *testing.T) {
	src := wrap(` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:fx="urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#" xmlns:pdf="http://ns.adobe.com/pdf/1.3/"`, `
      <fx:DocumentType>INVOICE</fx:DocumentType>
      <pdf:Producer>old</pdf:Producer>
      <unknown:thing xmlns:unknown='urn:u'><unknown:deep a = '1'><![CDATA[keep <me>]]></unknown:deep><unknown:e /></unknown:thing>`)
	p := mustParse(t, src)
	if err := p.SetText(NSPDF, "pdf", "Producer", "new & improved"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetText(NSPDFAID, "pdfaid", "part", "3"); err != nil {
		t.Fatal(err)
	}
	out := mustBytes(t, p)
	q := mustParse(t, string(out))
	for _, c := range []struct{ ns, name, want string }{
		{NSPDF, "Producer", "new & improved"},
		{NSPDFAID, "part", "3"},
		{"urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#", "DocumentType", "INVOICE"},
	} {
		if got, _ := q.Text(c.ns, c.name); got != c.want {
			t.Errorf("%s = %q, want %q\n%s", c.name, got, c.want, out)
		}
	}
	// Untouched content comes back byte for byte, odd-but-legal spelling and
	// all: an edit elsewhere does not re-serialise it.
	if !strings.Contains(string(out), `<unknown:thing xmlns:unknown='urn:u'><unknown:deep a = '1'><![CDATA[keep <me>]]></unknown:deep><unknown:e /></unknown:thing>`) {
		t.Errorf("unknown content not kept verbatim:\n%s", out)
	}
	if n := len(q.Lookup(NSPDF, "Producer")); n != 1 {
		t.Errorf("%d pdf:Producer after set, want 1", n)
	}
	if !strings.Contains(string(out), `xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"`) {
		t.Errorf("pdfaid not declared with its canonical prefix:\n%s", out)
	}
}

func TestSetReplacesAttributeFormAndDuplicates(t *testing.T) {
	src := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="3" pdfaid:conformance="A"/>` +
		`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"><pdfaid:part>2</pdfaid:part></rdf:Description>` +
		`</rdf:RDF></x:xmpmeta>`
	p := mustParse(t, src)
	if err := p.SetText(NSPDFAID, "pdfaid", "part", "4"); err != nil {
		t.Fatal(err)
	}
	q := mustParse(t, string(mustBytes(t, p)))
	if all := q.Lookup(NSPDFAID, "part"); len(all) != 1 || all[0].Value.Text != "4" {
		t.Fatalf("part occurrences after set: %+v", all)
	}
	if c, _ := q.Text(NSPDFAID, "conformance"); c != "A" {
		t.Fatalf("conformance lost: %q", c)
	}
}

func TestSetAltTextKeepsOtherLanguages(t *testing.T) {
	p := mustParse(t, wrap(` xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		`<dc:title><rdf:Alt><rdf:li xml:lang="de">Titel</rdf:li></rdf:Alt></dc:title>`))
	if err := p.SetAltText(NSDC, "dc", "title", "x-default", "Title"); err != nil {
		t.Fatal(err)
	}
	q := mustParse(t, string(mustBytes(t, p)))
	pr, _ := q.Get(NSDC, "title")
	if len(pr.Value.Items) != 2 || pr.Value.Items[0].Lang != "x-default" || pr.Value.Items[0].Text != "Title" || pr.Value.Items[1].Text != "Titel" {
		t.Fatalf("title items = %+v", pr.Value.Items)
	}
	if err := p.SetAltText(NSDC, "dc", "title", "x-default", "Other"); err != nil {
		t.Fatal(err)
	}
	q = mustParse(t, string(mustBytes(t, p)))
	pr, _ = q.Get(NSDC, "title")
	if len(pr.Value.Items) != 2 || pr.Value.Items[0].Text != "Other" {
		t.Fatalf("title items after second set = %+v", pr.Value.Items)
	}
}

// C72: a value that cannot be XML is refused, never written.
func TestInvalidTextIsRefused(t *testing.T) {
	for _, bad := range []string{"bad\xffutf8", "a\uFFFEb", "bell\x07", "nul\x00"} {
		p := New()
		err := p.SetAltText(NSDC, "dc", "title", "x-default", bad)
		if !errors.Is(err, ErrInvalidText) {
			t.Errorf("SetAltText(%q) = %v, want ErrInvalidText", bad, err)
		}
		if err := p.SetText(NSPDF, "pdf", "Producer", bad); !errors.Is(err, ErrInvalidText) {
			t.Errorf("SetText(%q) = %v, want ErrInvalidText", bad, err)
		}
	}
}

// Text that is legal but needs care — markup characters, CR, tab, astral
// characters — comes back exactly as it went in.
func TestHostileButLegalTextRoundTrips(t *testing.T) {
	vals := []string{"a<b>&c\"d'e", "]]>", "cr\rlf\n\ttab", "😀 é \U0010FFFD"}
	for _, v := range vals {
		p := New()
		if err := p.SetText(NSPDF, "pdf", "Keywords", v); err != nil {
			t.Fatal(err)
		}
		if err := p.SetSeq(NSDC, "dc", "creator", []string{v, v}); err != nil {
			t.Fatal(err)
		}
		q := mustParse(t, string(mustBytes(t, p)))
		pr, _ := q.Get(NSPDF, "Keywords")
		if pr.Value.Text != strings.TrimSpace(v) {
			t.Errorf("keywords %q came back %q", v, pr.Value.Text)
		}
		cr, _ := q.Get(NSDC, "creator")
		if cr.Value.Kind != Seq || len(cr.Value.Items) != 2 {
			t.Errorf("creator = %+v", cr.Value)
		}
	}
}

func TestExtensionSchemaReplacedNotDuplicated(t *testing.T) {
	src := wrap(` xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/" xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"`, `
      <pdfaExtension:schemas><rdf:Bag>
        <rdf:li rdf:parseType="Resource"><pdfaSchema:schema>Other</pdfaSchema:schema><pdfaSchema:namespaceURI>urn:other</pdfaSchema:namespaceURI><pdfaSchema:prefix>o</pdfaSchema:prefix></rdf:li>
      </rdf:Bag></pdfaExtension:schemas>`)
	p := mustParse(t, src)
	s := ExtensionSchema{Schema: "Mine", NamespaceURI: "urn:mine", Prefix: "m",
		Properties: []ExtensionProperty{{Name: "P", ValueType: "Text", Category: "external", Description: "d"}}}
	for i := 0; i < 3; i++ {
		if err := p.SetExtensionSchema(s); err != nil {
			t.Fatal(err)
		}
	}
	q := mustParse(t, string(mustBytes(t, p)))
	pr, _ := q.Get(NSPDFAExtension, "schemas")
	var uris []string
	for _, it := range pr.Value.Items {
		for _, f := range it.Fields {
			if f.Name == "namespaceURI" {
				uris = append(uris, f.Value.Text)
			}
		}
	}
	if !reflect.DeepEqual(uris, []string{"urn:other", "urn:mine"}) {
		t.Fatalf("extension schema namespaces = %v", uris)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat("<a>", MaxDepth+1) + strings.Repeat("</a>", MaxDepth+1)
	if _, err := Parse([]byte(deep)); !errors.Is(err, ErrLimit) {
		t.Fatalf("Parse of a %d-deep packet = %v, want ErrLimit", MaxDepth+1, err)
	}
	ok := strings.Repeat("<a>", MaxDepth) + strings.Repeat("</a>", MaxDepth)
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("Parse at the depth limit: %v", err)
	}
}

func TestMalformed(t *testing.T) {
	for _, s := range []string{`<a><b></a>`, ``, `text`, `<a>`, `<a>&bogus;</a>`, `</a>`} {
		if _, err := Parse([]byte(s)); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q) = %v, want ErrMalformed", s, err)
		}
	}
}

func TestNewPacketCarriesProperties(t *testing.T) {
	p := New()
	if err := p.SetText(NSPDFAID, "pdfaid", "part", "2"); err != nil {
		t.Fatal(err)
	}
	out := mustBytes(t, p)
	if !strings.HasPrefix(string(out), "<?xpacket begin=\"\xEF\xBB\xBF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>") {
		t.Errorf("no packet header: %q", out)
	}
	if !strings.HasSuffix(string(out), `<?xpacket end="w"?>`) {
		t.Errorf("no packet trailer: %q", out)
	}
	q := mustParse(t, string(out))
	if v, _ := q.Text(NSPDFAID, "part"); v != "2" {
		t.Fatalf("part = %q\n%s", v, out)
	}
}

func TestWritingIntoPacketWithoutRDF(t *testing.T) {
	p := mustParse(t, `<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`)
	if err := p.SetText(NSPDF, "pdf", "Producer", "x"); !errors.Is(err, ErrNoRDF) {
		t.Fatalf("SetText without rdf:RDF = %v, want ErrNoRDF", err)
	}
}

// A prefix already bound to another URI in scope is not reused for a new
// namespace.
func TestPrefixCollisionPicksAnother(t *testing.T) {
	p := mustParse(t, wrap(` xmlns:pdfaid="urn:impostor"`, `<pdfaid:part>9</pdfaid:part>`))
	if err := p.SetText(NSPDFAID, "pdfaid", "part", "3"); err != nil {
		t.Fatal(err)
	}
	q := mustParse(t, string(mustBytes(t, p)))
	if v, _ := q.Text(NSPDFAID, "part"); v != "3" {
		t.Fatalf("pdfaid part = %q", v)
	}
	if v, _ := q.Text("urn:impostor", "part"); v != "9" {
		t.Fatalf("impostor part = %q", v)
	}
}
