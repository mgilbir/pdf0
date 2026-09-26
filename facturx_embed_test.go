package pdf0

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Factur-X and Order-X containers, from the writer's side and the
// validator's (audit 2026-09-22: C43, C44, C148).

// attach adds an embedded file named name to doc's EmbeddedFiles tree and /AF,
// the way a producer that got there first would.
func attach(t *testing.T, d *Document, name string, data []byte) object.IndirectRef {
	t.Helper()
	ef := d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("EmbeddedFile")},
		object.Entry{Key: "Subtype", Value: object.Name("text/plain")},
	), data))
	fs := d.Add(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Filespec")},
		object.Entry{Key: "F", Value: object.String{Value: []byte(name)}},
		object.Entry{Key: "UF", Value: object.String{Value: []byte(name)}},
		object.Entry{Key: "AFRelationship", Value: object.Name("Supplement")},
		object.Entry{Key: "EF", Value: object.NewDictionary(object.Entry{Key: "F", Value: ef})},
	))
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	af, _ := d.Resolve(cat.Get("AF")).(object.Array)
	cat.Set("AF", append(af, fs))
	names := d.ResolveDict(cat.Get("Names"))
	if names == nil {
		names = &object.Dictionary{}
		cat.Set("Names", names)
	}
	tree := d.ResolveDict(names.Get("EmbeddedFiles"))
	if tree == nil {
		tree = &object.Dictionary{}
		names.Set("EmbeddedFiles", tree)
	}
	if err := d.view().NameTreeInsert(tree, []byte(name), fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

// embeddedNames lists the keys of the document's EmbeddedFiles tree, sorted.
func embeddedNames(d *Document) []string {
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	names := d.ResolveDict(cat.Get("Names"))
	if names == nil {
		return nil
	}
	entries, _ := d.view().NameTreeEntries(names.Get("EmbeddedFiles"))
	var out []string
	for _, e := range entries {
		out = append(out, string(e.Key))
	}
	sort.Strings(out)
	return out
}

func writeReadBytes(t *testing.T, d *Document) (*Document, []byte) {
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

// TestEmbedFacturXKeepsAttachments is the C44 regression: embedding an invoice
// into a document that already has attachments inserts it beside them, adds
// one /AF entry, keeps the rest of the metadata, and embedding again replaces
// the invoice instead of adding a second.
func TestEmbedFacturXKeepsAttachments(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA3b)
	if err := d.SetDocumentInfo(DocumentInfo{Creator: "Some Tool"}); err != nil {
		t.Fatal(err)
	}
	attach(t, d, "a-notes.txt", []byte("notes"))
	attach(t, d, "z-terms.txt", []byte("terms"))

	cii := []byte(ciiForProfile(formalis.ProfileEN16931))
	for i := 0; i < 2; i++ {
		if err := EmbedFacturX(d, cii, formalis.ProfileEN16931, "Invoice"); err != nil {
			t.Fatalf("EmbedFacturX #%d: %v", i+1, err)
		}
	}
	if got, want := embeddedNames(d), []string{"a-notes.txt", "factur-x.xml", "z-terms.txt"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("EmbeddedFiles = %v, want %v", got, want)
	}
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	if af, _ := d.Resolve(cat.Get("AF")).(object.Array); len(af) != 3 {
		t.Errorf("/AF has %d entries, want 3", len(af))
	}
	p, err := xmp.Parse([]byte(documentXMPText(t, d)))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.Text(xmp.NSXMP, "CreatorTool"); v != "Some Tool" {
		t.Errorf("xmp:CreatorTool = %q, want it kept", v)
	}
	// The replaced invoice's objects are gone, not left to be written out as
	// an unreachable embedded file.
	invoices := 0
	for _, o := range d.Objects {
		if s, ok := o.Value.(*object.Stream); ok && bytes.Equal(s.Data, cii) {
			invoices++
		}
	}
	if invoices != 1 {
		t.Errorf("%d invoice streams in the object table, want 1", invoices)
	}

	rt, data := writeReadBytes(t, d)
	res := ValidateFacturX(rt, data)
	if c := containerFindings(res); len(c) != 0 {
		t.Errorf("container findings after embedding: %v", c)
	}
}

// TestEmbedFacturXKeepsTheConformanceLetter: a PDF/A-3a (or 3u) document stays
// one. EmbedFacturX used to write conformance B over it.
func TestEmbedFacturXKeepsTheConformanceLetter(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA3a)
	if err := EmbedFacturX(d, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, ""); err != nil {
		t.Fatal(err)
	}
	p, _ := xmp.Parse([]byte(documentXMPText(t, d)))
	if c, _ := p.Text(xmp.NSPDFAID, "conformance"); c != "A" {
		t.Errorf("pdfaid:conformance = %q after EmbedFacturX, want A", c)
	}
	// The container composes a PDF/A-3b pass, whose "must be B" finding is not
	// a container finding: it is dropped by its Check, so a 3a container is
	// clean (audit C148's composition by rule identity).
	rt, data := writeReadBytes(t, d)
	if c := containerFindings(ValidateFacturX(rt, data)); len(c) != 0 {
		t.Errorf("container findings on a PDF/A-3a Factur-X: %v", c)
	}
	d4 := mustPDFADoc(t, pdfa.PDFA4)
	if err := EmbedFacturX(d4, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, ""); err == nil {
		t.Error("EmbedFacturX into a PDF/A-4 document succeeded; a Factur-X container is PDF/A-3")
	}
}

// TestEmbedFacturXRejectsNonXML is the writer half of C43: nothing that cannot
// be validated as an invoice is embedded as one, and a refusal changes nothing.
func TestEmbedFacturXRejectsNonXML(t *testing.T) {
	for _, bad := range [][]byte{nil, []byte("   "), []byte("not xml"), []byte("<a><b></a>"), []byte("<a/><b/>")} {
		d := mustPDFADoc(t, pdfa.PDFA3b)
		before := len(d.Objects)
		err := EmbedFacturX(d, bad, formalis.ProfileBasic, "")
		if !errors.Is(err, facturx.ErrNotXML) {
			t.Errorf("EmbedFacturX(%q) = %v, want ErrNotXML", bad, err)
		}
		if len(d.Objects) != before {
			t.Errorf("EmbedFacturX(%q) failed but added objects", bad)
		}
	}
}

// TestEmbedFacturXIntoKidsTree: the name tree insert handles a tree of
// intermediate nodes, keeps /Limits true, and the invoice is found by a
// reader looking the key up.
func TestEmbedFacturXIntoKidsTree(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA3b)
	leaf := func(keys ...string) object.IndirectRef {
		var names object.Array
		for _, k := range keys {
			names = append(names, object.String{Value: []byte(k)}, d.Add(object.NewDictionary(
				object.Entry{Key: "Type", Value: object.Name("Filespec")},
				object.Entry{Key: "F", Value: object.String{Value: []byte(k)}},
			)))
		}
		return d.Add(object.NewDictionary(
			object.Entry{Key: "Names", Value: names},
			object.Entry{Key: "Limits", Value: object.Array{object.String{Value: []byte(keys[0])}, object.String{Value: []byte(keys[len(keys)-1])}}},
		))
	}
	root := object.NewDictionary(object.Entry{Key: "Kids", Value: object.Array{leaf("a.txt", "c.txt"), leaf("m.txt", "z.txt")}})
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Names", object.NewDictionary(object.Entry{Key: "EmbeddedFiles", Value: d.Add(root)}))

	if err := EmbedFacturX(d, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(embeddedNames(d), ","); got != "a.txt,c.txt,factur-x.xml,m.txt,z.txt" {
		t.Errorf("EmbeddedFiles = %s", got)
	}
	// "factur-x.xml" sorts after "c.txt" and before "m.txt": it belongs in the
	// second leaf, whose lower limit widens to it.
	kids, _ := root.Get("Kids").(object.Array)
	second := d.ResolveDict(kids[1])
	lim, _ := second.Get("Limits").(object.Array)
	if lo, _ := lim[0].(object.String); string(lo.Value) != "factur-x.xml" {
		t.Errorf("second leaf's lower limit = %q, want factur-x.xml", lo.Value)
	}
}

// facturxWithInvoice is a written-and-read Factur-X container whose invoice
// stream the caller can then damage.
func facturxWithInvoice(t *testing.T) (*Document, *object.Stream) {
	t.Helper()
	d := mustPDFADoc(t, pdfa.PDFA3b)
	if err := EmbedFacturX(d, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, ""); err != nil {
		t.Fatal(err)
	}
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	fs, _, _ := facturx.FindAttachment(d.view(), cat)
	st, _ := d.Resolve(d.ResolveDict(fs.Get("EF")).Get("F")).(*object.Stream)
	return d, st
}

func ruleMessages(res facturx.Result) []string {
	var out []string
	for _, v := range res.Violations {
		out = append(out, v.Rule+": "+v.Message)
	}
	return out
}

// TestFacturXUnreadableInvoiceIsAFinding is the validator half of C43: an
// invoice attachment that is present but empty, corrupt or not XML is reported,
// never validated as clean.
func TestFacturXUnreadableInvoiceIsAFinding(t *testing.T) {
	cases := []struct {
		name   string
		damage func(st *object.Stream)
		want   string
	}{
		{"empty", func(st *object.Stream) { st.Data = nil }, "is empty"},
		{"whitespace", func(st *object.Stream) { st.Data = []byte(" \n ") }, "is empty"},
		{"corrupt Flate", func(st *object.Stream) {
			st.Data = []byte("this is not zlib")
			st.Dict.Set("Filter", object.Name("FlateDecode"))
		}, "could not be decoded"},
		{"not XML", func(st *object.Stream) { st.Data = []byte("%PDF-1.7 garbage") }, "could not be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, st := facturxWithInvoice(t)
			c.damage(st)
			res := ValidateFacturX(d, nil)
			found := false
			for _, v := range res.Violations {
				if v.Rule == "invoice-xml" && strings.Contains(v.Message, c.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no invoice-xml finding containing %q; got %q", c.want, ruleMessages(res))
			}
		})
	}
	// A filter pdf0 does not implement is pdf0's limit, not the file's defect.
	d, st := facturxWithInvoice(t)
	st.Dict.Set("Filter", object.Name("JBIG2Decode"))
	res := ValidateFacturX(d, nil)
	limit := false
	for _, v := range res.Violations {
		if v.Rule == "invoice-xml" {
			t.Errorf("an unsupported filter was reported as a defect of the file: %s", v.Message)
		}
		if v.Rule == finding.LimitRule && strings.Contains(v.Message, core.GuardUnsupportedFilter) {
			limit = true
		}
	}
	if !limit {
		t.Errorf("no limit finding for an invoice pdf0 could not decode; got %q", ruleMessages(res))
	}
}

// TestFacturXContainerTripsAreReported: a trip in the container run — here an
// XMP packet over a lowered limit — reaches the result as a "limit" finding,
// rather than the metadata simply reading as absent.
func TestFacturXContainerTripsAreReported(t *testing.T) {
	d, _ := facturxWithInvoice(t)
	rt, data := writeReadBytes(t, d)
	rt.limits.XMPPacketBytes = 64
	// The container half alone: the PDF/A-3 half is a separate run that would
	// report its own trip over the same packet and hide a dropped one here.
	res := facturx.Validate(beginRun(rt).view(), data)
	limit := false
	for _, v := range res.Violations {
		if v.Rule == finding.LimitRule && strings.Contains(v.Message, core.GuardXMPPacket) {
			limit = true
		}
		if v.Rule == "metadata" {
			t.Errorf("unread metadata reported as a defect: %s", v.Message)
		}
	}
	if !limit {
		t.Errorf("the container's XMP trip was not reported; got %q", ruleMessages(res))
	}
}

// TestFacturXDuplicateInvoices: a second invoice attachment is flagged, and the
// one /AF designates as the data is the one validated.
func TestFacturXDuplicateInvoices(t *testing.T) {
	d, _ := facturxWithInvoice(t)
	// A stale second invoice, listed first in /AF but with a relationship the
	// specification does not allow for the invoice.
	stale := attach(t, d, "zugferd-invoice.xml", []byte("<stale/>"))
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	af, _ := cat.Get("AF").(object.Array)
	var reordered object.Array
	reordered = append(reordered, stale)
	for _, e := range af {
		if object.RefNum(e) != stale.Number {
			reordered = append(reordered, e)
		}
	}
	cat.Set("AF", reordered)
	rt, data := writeReadBytes(t, d)
	res := ValidateFacturX(rt, data)
	if res.XMLName != "factur-x.xml" {
		t.Errorf("validated %q, want the /Data invoice factur-x.xml", res.XMLName)
	}
	dup := false
	for _, v := range res.Violations {
		if v.Rule == "attachment" && strings.Contains(v.Message, "2 invoice XML attachments") {
			dup = true
		}
	}
	if !dup {
		t.Errorf("duplicate invoices not flagged; got %q", ruleMessages(res))
	}
}

// TestFacturXNameCase: the attachment name is matched ignoring case, so it is
// found, and reported for not being spelled as the specification spells it.
func TestFacturXNameCase(t *testing.T) {
	d, _ := facturxWithInvoice(t)
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	fs, _, _ := facturx.FindAttachment(d.view(), cat)
	fs.Set("F", object.String{Value: []byte("Factur-X.xml")})
	fs.Set("UF", object.String{Value: []byte("Factur-X.xml")})
	res := ValidateFacturX(d, nil)
	if res.XMLName != "Factur-X.xml" {
		t.Fatalf("attachment not found: %q", res.XMLName)
	}
	spelled := false
	for _, v := range res.Violations {
		if v.Rule == "attachment" && strings.Contains(v.Message, "spell the name exactly") {
			spelled = true
		}
	}
	if !spelled {
		t.Errorf("a misspelt attachment name was not reported; got %q", ruleMessages(res))
	}
}

// TestOrderXMetadataSymmetry is C148: XMPPacket(ORDER) writes the Order-X
// namespace, Order-X checks fx:Version as Factur-X does, and each validator
// says when the metadata is in the other family's namespace.
func TestOrderXMetadataSymmetry(t *testing.T) {
	order, err := facturx.XMPPacket(formalis.Profile("COMFORT"), "ORDER", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := xmp.Parse(order)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.Text(facturx.NSOrderX, "DocumentType"); v != "ORDER" {
		t.Errorf("XMPPacket(ORDER) did not write the Order-X namespace:\n%s", order)
	}
	if _, ok := p.Get(facturx.NSFacturX, "DocumentType"); ok {
		t.Errorf("XMPPacket(ORDER) wrote the invoice namespace")
	}
	if v, _ := p.Text(facturx.NSOrderX, "DocumentFileName"); v != "order-x.xml" {
		t.Errorf("order file name = %q", v)
	}

	// An Order-X container built by EmbedOrderX validates; without fx:Version
	// it does not.
	d := mustPDFADoc(t, pdfa.PDFA3b)
	orderXML := []byte(`<rsm:SCRDMCCBDACIOMessageStructure xmlns:rsm="urn:un:unece:uncefact:data:SCRDMCCBDACIOMessageStructure:100"/>`)
	if err := EmbedOrderX(d, orderXML, facturx.OrderXComfort, "ORDER", ""); err != nil {
		t.Fatal(err)
	}
	rt, data := writeReadBytes(t, d)
	res := ValidateOrderX(rt, data)
	for _, v := range res.Violations {
		if v.Rule == "metadata" || v.Rule == "attachment" {
			t.Errorf("container finding on an EmbedOrderX container: %s: %s", v.Rule, v.Message)
		}
	}
	cat := rt.ResolveDict(rt.Trailer.Get("Root"))
	packet, err := xmp.Parse([]byte(documentXMPText(t, rt)))
	if err != nil {
		t.Fatal(err)
	}
	packet.Remove(facturx.NSOrderX, "Version")
	noVersion, err := packet.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	cat.Set("Metadata", rt.Add(core.MetadataStream(noVersion)))
	res = ValidateOrderX(rt, nil)
	missing := false
	for _, v := range res.Violations {
		if v.Rule == "metadata" && strings.Contains(v.Message, "fx:Version") {
			missing = true
		}
	}
	if !missing {
		t.Errorf("Order-X without fx:Version was not flagged; got %v", res.Violations)
	}

	// An invoice validated as an order says whose namespace its metadata is in.
	inv := mustPDFADoc(t, pdfa.PDFA3b)
	if err := EmbedFacturX(inv, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, ""); err != nil {
		t.Fatal(err)
	}
	ores := ValidateOrderX(inv, nil)
	foreign := false
	for _, v := range ores.Violations {
		if v.Rule == "metadata" && strings.Contains(v.Message, "Factur-X namespace") {
			foreign = true
		}
	}
	if !foreign {
		t.Errorf("invoice metadata validated as Order-X was not reported as misplaced; got %v", ores.Violations)
	}
}
