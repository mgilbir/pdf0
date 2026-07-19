package pdf0

import (
	"bytes"
	"github.com/mgilbir/formalis"
	"strings"
	"testing"
)

// validCII is a minimal Cross Industry Invoice payload used to exercise the
// container writer (the writer embeds these bytes verbatim; their EN 16931
// validity is tested in the einvoice package).
const validCII = `<CrossIndustryInvoice><ExchangedDocument><ID>INV-1</ID></ExchangedDocument></CrossIndustryInvoice>`

// TestEmbedFacturXRoundTrip is the writer's core guarantee: a Factur-X document
// built by embedding invoice XML into a PDF/A-3 base validates as a conforming
// Factur-X container after a Write/Read round trip, with the invoice XML
// recovered intact, for every profile.
func TestEmbedFacturXRoundTrip(t *testing.T) {
	for _, profile := range []formalis.Profile{
		formalis.ProfileMinimum, formalis.ProfileBasicWL, formalis.ProfileBasic, formalis.ProfileEN16931, formalis.ProfileExtended,
	} {
		t.Run(string(profile), func(t *testing.T) {
			doc := NewPDFADocument(PDFA3b)
			if err := EmbedFacturX(doc, []byte(validCII), profile, "Invoice INV-1"); err != nil {
				t.Fatalf("EmbedFacturX: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("Write: %v", err)
			}
			rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("re-Read: %v", err)
			}
			res := ValidateFacturX(rt, buf.Bytes())
			if len(res.Violations) != 0 {
				t.Fatalf("produced file has %d Factur-X violation(s): %s: %s",
					len(res.Violations), res.Violations[0].Rule, res.Violations[0].Message)
			}
			if res.Profile != profile {
				t.Errorf("detected profile %q, want %q", res.Profile, profile)
			}
			if res.XMLName != "factur-x.xml" {
				t.Errorf("embedded file name %q, want factur-x.xml", res.XMLName)
			}
			if !bytes.Equal(res.XML, []byte(validCII)) {
				t.Errorf("recovered invoice XML does not match the input (%d vs %d bytes)", len(res.XML), len(validCII))
			}
		})
	}
}

func TestEmbedFacturXUnknownProfile(t *testing.T) {
	doc := NewPDFADocument(PDFA3b)
	if err := EmbedFacturX(doc, []byte(validCII), formalis.Profile("BOGUS"), ""); err == nil {
		t.Error("expected an error for an unknown profile")
	}
}

// TestFacturXXMPPacket checks the generated metadata declares the fx extension
// schema and the Factur-X properties for the profile.
func TestFacturXXMPPacket(t *testing.T) {
	xmp := string(facturxXMPPacket(formalis.ProfileBasic, "INVOICE", "Some & Title"))
	for _, want := range []string{
		"<pdfaid:part>3</pdfaid:part>",
		"urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#",
		"<pdfaSchema:prefix>fx</pdfaSchema:prefix>",
		"<fx:DocumentType>INVOICE</fx:DocumentType>",
		"<fx:DocumentFileName>factur-x.xml</fx:DocumentFileName>",
		"<fx:ConformanceLevel>BASIC</fx:ConformanceLevel>",
		"Some &amp; Title", // title is XML-escaped
	} {
		if !strings.Contains(xmp, want) {
			t.Errorf("XMP packet missing %q", want)
		}
	}
}

func TestEncodeUTF16BE(t *testing.T) {
	got := encodeUTF16BE("factur-x.xml")
	if got[0] != 0xFE || got[1] != 0xFF {
		t.Fatal("missing UTF-16BE byte-order mark")
	}
	if decodePDFTextString(got) != "factur-x.xml" {
		t.Errorf("round trip failed: %q", decodePDFTextString(got))
	}
}
