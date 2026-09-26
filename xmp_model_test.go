package pdf0

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The XMP model against real metadata, and the metadata writers against the
// documents they are most likely to damage (audit 2026-09-22: C32, C35, C72,
// C77, C141).

// documentXMPText is the document's metadata packet decoded to UTF-8, or "".
func documentXMPText(t *testing.T, d *Document) string {
	t.Helper()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	if cat == nil {
		return ""
	}
	s, ok := d.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return ""
	}
	raw, err := d.StreamData(s)
	if err != nil {
		return ""
	}
	return core.DecodeXMPToUTF8(raw)
}

// wellFormedByToken is the judgement the readers relied on before the model:
// encoding/xml's Token over the whole packet.
func wellFormedByToken(text string) bool {
	dec := xml.NewDecoder(strings.NewReader(text))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	saw := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return saw
		}
		if err != nil {
			return false
		}
		if _, ok := tok.(xml.StartElement); ok {
			saw = true
		}
	}
}

// propsWithout lists a packet's properties, less one, as comparable strings.
func propsWithout(p *xmp.Packet, ns, name string) []string {
	var out []string
	for _, pr := range p.Properties() {
		if pr.NS == ns && pr.Name == name {
			continue
		}
		out = append(out, pr.NS+" "+pr.Name+" "+valueString(pr.Value))
	}
	return out
}

func valueString(v xmp.Value) string {
	var b strings.Builder
	b.WriteString(v.Kind.String() + "(" + v.Text + "|" + v.Lang)
	for _, it := range v.Items {
		b.WriteString(" [" + valueString(it) + "]")
	}
	for _, f := range v.Fields {
		b.WriteString(" {" + f.NS + " " + f.Name + " " + valueString(f.Value) + "}")
	}
	b.WriteString(")")
	return b.String()
}

// TestCorpusXMPRoundTrip puts every metadata packet in the veraPDF and
// Factur-X corpora through the model:
//
//   - it parses exactly when encoding/xml's Token accepts it, so the model
//     judges well-formedness as the readers it replaced did;
//   - an unmodified packet serialises to the bytes it came from;
//   - the writer, forced to write every node from the model (Rewrite), yields a
//     packet whose tree is Equivalent to the original — the writer proved on
//     real data, not only on the nodes edits reach;
//   - setting one property changes that property and nothing else any reader
//     can see.
func TestCorpusXMPRoundTrip(t *testing.T) {
	var files []string
	files = append(files, corpusTestFiles(t, "")...)
	files = append(files, testfiles.FacturX.Glob(t, "*.pdf")...)
	sort.Strings(files)

	var withXMP, parsed, malformed, noRDF, exact, rewriteExact int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		text := documentXMPText(t, d)
		if strings.TrimSpace(text) == "" {
			continue
		}
		withXMP++
		name := filepath.Base(f)
		p, perr := xmp.Parse([]byte(text))
		if (perr == nil) != wellFormedByToken(text) {
			t.Errorf("%s: model parse error %v disagrees with encoding/xml's Token (well-formed=%v)", name, perr, wellFormedByToken(text))
			continue
		}
		if perr != nil {
			malformed++
			continue
		}
		parsed++
		out, err := p.Bytes()
		if err != nil {
			t.Errorf("%s: Bytes: %v", name, err)
			continue
		}
		if string(out) == text {
			exact++
		} else {
			t.Errorf("%s: an unmodified packet did not come back byte for byte", name)
		}

		rw, err := p.Rewrite()
		if err != nil {
			t.Errorf("%s: Rewrite: %v", name, err)
			continue
		}
		if string(rw) == text {
			rewriteExact++
		}
		q, err := xmp.Parse(rw)
		if err != nil {
			t.Errorf("%s: the rewritten packet does not parse: %v", name, err)
			continue
		}
		if err := xmp.Equivalent(p, q); err != nil {
			t.Errorf("%s: the rewritten packet is not equivalent: %v", name, err)
		}

		if !p.HasRDF() {
			noRDF++
			continue
		}
		before := propsWithout(p, xmp.NSPDF, "Producer")
		if err := p.SetText(xmp.NSPDF, "pdf", "Producer", "pdf0 round trip & <test>"); err != nil {
			t.Errorf("%s: SetText: %v", name, err)
			continue
		}
		edited, err := p.Bytes()
		if err != nil {
			t.Errorf("%s: Bytes after an edit: %v", name, err)
			continue
		}
		e, err := xmp.Parse(edited)
		if err != nil {
			t.Errorf("%s: the edited packet does not parse: %v", name, err)
			continue
		}
		if got := propsWithout(e, xmp.NSPDF, "Producer"); !reflect.DeepEqual(got, before) {
			t.Errorf("%s: setting pdf:Producer changed other properties:\n before %v\n after  %v", name, before, got)
		}
		if all := e.Lookup(xmp.NSPDF, "Producer"); len(all) != 1 || all[0].Value.Text != "pdf0 round trip & <test>" {
			t.Errorf("%s: pdf:Producer after the edit: %+v", name, all)
		}
	}
	t.Logf("XMP corpus round trip: %d packets, %d parsed, %d malformed, %d without rdf:RDF; %d byte-exact unmodified, %d byte-exact even when rewritten from the model",
		withXMP, parsed, malformed, noRDF, exact, rewriteExact)
	if parsed == 0 {
		t.Fatal("no corpus packet was parsed, so this test checked nothing")
	}
}

// TestCorpusFacturXSetDocumentInfoKeepsFindings is the C32 regression over the Factur-X
// corpus: describing a Factur-X invoice must not change what the Factur-X or
// PDF/A-3b validators say about it. The audit saw 0 → 6 and 0 → 2 findings,
// because the metadata was regenerated with only three pdfaid properties.
//
// Both sides are written and read back, so the only difference between them
// is the SetDocumentInfo call.
func TestCorpusFacturXSetDocumentInfoKeepsFindings(t *testing.T) {
	roundTrip := func(t *testing.T, d *Document) (*Document, []byte) {
		t.Helper()
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatalf("Write: %v", err)
		}
		rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		return rt, buf.Bytes()
	}
	findings := func(d *Document, data []byte) []string {
		var out []string
		for _, v := range ValidateFacturX(d, data).Violations {
			out = append(out, "FX "+v.Rule+": "+v.Message)
		}
		for _, v := range ValidatePDFABytes(d, pdfa.PDFA3b, data) {
			out = append(out, "A "+v.Rule+": "+v.Message)
		}
		sort.Strings(out)
		return out
	}
	files := testfiles.FacturX.Glob(t, "*.pdf")
	checked := 0
	for _, f := range files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "FAIL") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			want := findings(roundTrip(t, plain))

			described, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if err := described.SetDocumentInfo(DocumentInfo{Title: "Invoice"}); err != nil {
				t.Fatalf("SetDocumentInfo: %v", err)
			}
			rt, rtData := roundTrip(t, described)
			if got := findings(rt, rtData); !reflect.DeepEqual(got, want) {
				t.Errorf("SetDocumentInfo changed the findings:\n before %q\n after  %q", want, got)
			}
			if got := documentXMPText(t, rt); !strings.Contains(got, "Invoice") {
				t.Errorf("the title was not written")
			}
			checked++
		})
	}
	if checked == 0 {
		t.Fatal("no Factur-X corpus file was checked")
	}
}

// otherWritersPacket carries what other writers put in a packet: a PDF/A
// identification in attribute form, the PDF/UA, PDF/X and PDF/VT
// identifications, Factur-X properties, an extension schema, a property in an
// unknown namespace, a second-language title and a comment.
const otherWritersPacket = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="3" pdfaid:conformance="A"/>
    <rdf:Description rdf:about="" xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" xmlns:pdfxid="http://www.npes.org/pdfx/ns/id/" xmlns:pdfvtid="http://www.npes.org/pdfvt/ns/id/">
      <!-- written by another tool -->
      <pdfuaid:part>1</pdfuaid:part>
      <pdfxid:GTS_PDFXVersion>PDF/X-4</pdfxid:GTS_PDFXVersion>
      <pdfvtid:GTS_PDFVTVersion>PDF/VT-1</pdfvtid:GTS_PDFVTVersion>
    </rdf:Description>
    <rdf:Description rdf:about="" xmlns:fx="urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#" xmlns:u="urn:unknown#" xmlns:dc="http://purl.org/dc/elements/1.1/">
      <fx:DocumentType>INVOICE</fx:DocumentType>
      <u:thing u:q="1">kept</u:thing>
      <dc:title><rdf:Alt><rdf:li xml:lang="de">Rechnung</rdf:li></rdf:Alt></dc:title>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

// TestSetDocumentInfoEditsTheMetadata is the C32 regression at unit scale:
// SetDocumentInfo sets what it was given and keeps everything else, in the
// XMP packet and in the information dictionary.
func TestSetDocumentInfoEditsTheMetadata(t *testing.T) {
	d := NewDocument()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Metadata", d.Add(core.MetadataStream([]byte(otherWritersPacket))))
	info := object.NewDictionary(
		object.Entry{Key: "GTS_PDFXVersion", Value: object.String{Value: []byte("PDF/X-1a:2001")}},
		object.Entry{Key: "Trapped", Value: object.Name("False")},
	)
	d.Trailer.Set("Info", d.Add(info))

	if err := d.SetDocumentInfo(DocumentInfo{Title: "Invoice & Co", Author: "A"}); err != nil {
		t.Fatal(err)
	}
	p, err := xmp.Parse([]byte(documentXMPText(t, d)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ ns, name, want string }{
		{xmp.NSPDFAID, "part", "3"},
		{xmp.NSPDFAID, "conformance", "A"},
		{xmp.NSPDFUAID, "part", "1"},
		{xmp.NSPDFXID, "GTS_PDFXVersion", "PDF/X-4"},
		{xmp.NSPDFVTID, "GTS_PDFVTVersion", "PDF/VT-1"},
		{"urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#", "DocumentType", "INVOICE"},
		{"urn:unknown#", "thing", "kept"},
		{xmp.NSPDF, "Producer", "pdf0"},
	} {
		if got, _ := p.Text(c.ns, c.name); got != c.want {
			t.Errorf("{%s}%s = %q, want %q", c.ns, c.name, got, c.want)
		}
	}
	title, _ := p.Get(xmp.NSDC, "title")
	if x, _ := title.Value.AltText("x-default"); x != "Invoice & Co" {
		t.Errorf("x-default title = %q", x)
	}
	if de, _ := title.Value.AltText("de"); de != "Rechnung" {
		t.Errorf("the German title was lost: %q", de)
	}
	if !strings.Contains(documentXMPText(t, d), "<!-- written by another tool -->") {
		t.Error("a comment in the packet was lost")
	}
	gotInfo := d.ResolveDict(d.Trailer.Get("Info"))
	if s, _ := gotInfo.Get("GTS_PDFXVersion").(object.String); string(s.Value) != "PDF/X-1a:2001" {
		t.Error("the Info /GTS_PDFXVersion identification was lost")
	}
	if n, _ := gotInfo.Get("Trapped").(object.Name); n != "False" {
		t.Error("the Info /Trapped entry was lost")
	}

	// Twice is once: the edit is idempotent, so no property is duplicated.
	if err := d.SetDocumentInfo(DocumentInfo{Title: "Invoice & Co", Author: "A"}); err != nil {
		t.Fatal(err)
	}
	p, _ = xmp.Parse([]byte(documentXMPText(t, d)))
	for _, c := range [][2]string{{xmp.NSDC, "title"}, {xmp.NSDC, "creator"}, {xmp.NSPDF, "Producer"}} {
		if n := len(p.Lookup(c[0], c[1])); n != 1 {
			t.Errorf("%s occurs %d times after a second SetDocumentInfo", c[1], n)
		}
	}
}

// TestSetDocumentInfoRefusesWhatItCannotEdit: a packet that is not XML is not
// regenerated — that would destroy whatever it says — and the document is left
// as it was.
func TestSetDocumentInfoRefusesWhatItCannotEdit(t *testing.T) {
	d := NewDocument()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Metadata", d.Add(core.MetadataStream([]byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><unclosed>`))))
	if err := d.SetDocumentInfo(DocumentInfo{Title: "T"}); !errors.Is(err, xmp.ErrMalformed) {
		t.Fatalf("SetDocumentInfo on a malformed packet = %v, want ErrMalformed", err)
	}
	if d.Trailer.Get("Info") != nil {
		t.Error("a refused SetDocumentInfo still wrote an Info dictionary")
	}
}

// TestEscapedTitlesValidateAt1b is the C35 regression: a title with XML
// metacharacters, or a character outside ASCII, is written escaped into XMP
// and must compare equal to the Info entry at PDF/A-1b — through the
// constructor, through SetDocumentInfo, and against an Info string written by
// hand in PDFDocEncoding (C77).
func TestEscapedTitlesValidateAt1b(t *testing.T) {
	validate := func(t *testing.T, d *Document) {
		t.Helper()
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range ValidatePDFABytes(rt, pdfa.PDFA1b, buf.Bytes()) {
			t.Errorf("%v", v)
		}
	}
	for _, title := range []string{"Smith & Sons", "a < b > c", "&amp; is literal", "&#233; is literal", "Café", "Ünïcødé ∑"} {
		t.Run(title, func(t *testing.T) {
			// The constructor writes XMP; the Info entry is added by hand, as
			// the audit's scenario does, in the encoding a producer would use.
			d := mustPDFADocWithInfo(t, pdfa.PDFA1b, title, "")
			info := &object.Dictionary{}
			if b, ok := pdfdocEncode(title); ok {
				info.Set("Title", object.String{Value: b})
			} else {
				info.Set("Title", object.String{Value: encodePDFText(title)})
			}
			d.Trailer.Set("Info", d.Add(info))
			validate(t, d)

			// And through SetDocumentInfo, which writes both sides itself.
			d2 := mustPDFADoc(t, pdfa.PDFA1b)
			if err := d2.SetDocumentInfo(DocumentInfo{Title: title}); err != nil {
				t.Fatal(err)
			}
			validate(t, d2)
		})
	}
}

// pdfdocEncode writes s in PDFDocEncoding when every character is Latin-1
// (where PDFDocEncoding agrees with it for these test strings).
func pdfdocEncode(s string) ([]byte, bool) {
	var out []byte
	for _, r := range s {
		if r > 0xFF || (r >= 0x80 && r < 0xA1) {
			return nil, false
		}
		out = append(out, byte(r))
	}
	return out, true
}

// replaceMetadata swaps the document's metadata stream for packet.
func replaceMetadata(d *Document, packet []byte) {
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Metadata", d.Add(core.MetadataStream(packet)))
}

// TestSaveRefusesUnreadableMetadata: a document whose XMP pdf0 cannot read
// claims something pdf0 cannot see, so Save — which exists to check the claim —
// refuses rather than quietly writing it unchecked.
func TestSaveRefusesUnreadableMetadata(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA2b)
	replaceMetadata(d, []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><pdfaid:part xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">2</pdfaid:part>`))
	var buf bytes.Buffer
	err := d.Save(&buf)
	if err == nil || !strings.Contains(err.Error(), "XMP metadata cannot be read") {
		t.Fatalf("Save = %v, want a refusal naming the unreadable metadata", err)
	}
	if buf.Len() != 0 {
		t.Error("a refused Save wrote output")
	}
}

// TestEmbeddedXMPOverLimitIsUnknown: an embedded PDF whose metadata is over
// the XMP packet limit declares a level pdf0 did not read — the verdict is
// withheld, not "not PDF/A".
func TestEmbeddedXMPOverLimitIsUnknown(t *testing.T) {
	inner := mustPDFADoc(t, pdfa.PDFA2b)
	text := documentXMPText(t, inner)
	end := strings.LastIndex(text, "<?xpacket end")
	padded := text[:end] + strings.Repeat(" ", core.DefaultMaxXMPPacketBytes) + text[end:]
	replaceMetadata(inner, []byte(padded))
	var buf bytes.Buffer
	if err := inner.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if _, complete := embeddedPDFACompliant(core.Canceler{}, buf.Bytes(), core.DefaultLimits()); complete {
		t.Error("an embedded file whose metadata was not read got a verdict")
	}
}

// TestUnreadMetadataDoesNotChooseTheRules: when the metadata is over the XMP
// packet limit, what the document declares is unknown — and it no longer
// matters to any rule but the identification one, because every other rule is
// gated on the target. Plain PDF/A-4 requires the document-level /AF whatever
// the document says; PDF/A-4f relaxes it whatever the document says. Before,
// the relaxation was read out of the declaration and an unread declaration
// granted it at plain PDF/A-4 too. The run is still reported incomplete, and a
// LevelDeclared run, which needs the declaration, is not run at all.
func TestUnreadMetadataDoesNotChooseTheRules(t *testing.T) {
	build := func(level pdfa.Level) *Document {
		d := mustPDFADoc(t, level)
		// An embedded file with no document-level /AF: plain PDF/A-4 requires
		// the association, 4e and 4f relax it.
		attach(t, d, "notes.txt", []byte("notes"))
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		cat.Delete("AF")
		d.limits.XMPPacketBytes = 64
		return d
	}
	afFinding := func(vs []pdfa.Violation) bool {
		for _, v := range vs {
			if strings.Contains(v.Message, "must have /AF array") {
				return true
			}
		}
		return false
	}
	limitFinding := func(vs []pdfa.Violation) bool {
		for _, v := range vs {
			if v.Rule == "limit" && strings.Contains(v.Message, core.GuardXMPPacket) {
				return true
			}
		}
		return false
	}
	plain := ValidatePDFA(build(pdfa.PDFA4), pdfa.PDFA4)
	if !afFinding(plain) {
		t.Errorf("plain PDF/A-4 with unread metadata did not require /AF: %v", plain)
	}
	if !limitFinding(plain) {
		t.Errorf("no limit finding for the unread metadata: %v", plain)
	}
	if f := ValidatePDFA(build(pdfa.PDFA4F), pdfa.PDFA4F); afFinding(f) {
		t.Errorf("PDF/A-4f with unread metadata withheld the /AF relaxation: %v", f)
	}
	declared := ValidatePDFA(build(pdfa.PDFA4F), pdfa.LevelDeclared)
	if len(declared) != 1 || !IsCheckerFinding(declared[0]) || !strings.Contains(declared[0].Message, "XMP packet limit") {
		t.Errorf("LevelDeclared with unread metadata: want one checker finding, got %v", declared)
	}
}
