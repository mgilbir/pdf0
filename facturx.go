package pdf0

import (
	"fmt"
	"strings"
)

// This file validates the PDF container of a Factur-X (a.k.a. ZUGFeRD 2.x)
// hybrid electronic invoice. A Factur-X document is a PDF/A-3 file that carries
// a human-readable invoice and an embedded XML representation of the same
// invoice (UN/CEFACT Cross Industry Invoice, EN 16931). This validator checks
// the container: PDF/A-3 conformance, the embedded invoice XML attached as an
// associated file, and the Factur-X XMP metadata that identifies it. Validating
// the invoice XML itself against the EN 16931 business rules is a separate layer.
//
// The checks are calibrated against a corpus of conforming Factur-X / ZUGFeRD
// invoices across all profiles (MINIMUM, BASIC WL, BASIC, EN 16931, EXTENDED).

// FacturXProfile is a Factur-X conformance profile, in increasing data richness.
type FacturXProfile string

const (
	FacturXMinimum  FacturXProfile = "MINIMUM"
	FacturXBasicWL  FacturXProfile = "BASIC WL"
	FacturXBasic    FacturXProfile = "BASIC"
	FacturXEN16931  FacturXProfile = "EN 16931"
	FacturXExtended FacturXProfile = "EXTENDED"
)

var facturxProfiles = map[string]FacturXProfile{
	"MINIMUM":  FacturXMinimum,
	"BASIC WL": FacturXBasicWL,
	"BASIC":    FacturXBasic,
	"EN 16931": FacturXEN16931,
	"EXTENDED": FacturXExtended,
}

// The embedded invoice XML is named factur-x.xml (Factur-X and ZUGFeRD 2.1+);
// zugferd-invoice.xml (ZUGFeRD 2.0) and xrechnung.xml are also seen in practice.
var facturxXMLNames = map[string]bool{
	"factur-x.xml":        true,
	"zugferd-invoice.xml": true,
	"xrechnung.xml":       true,
}

// The invoice file is associated with the document; the relationship is /Data or
// /Alternative in the Factur-X spec, and /Source is used by some producers.
var facturxRelationships = map[Name]bool{"Data": true, "Alternative": true, "Source": true}

// FacturXViolation reports a way in which a document departs from the Factur-X
// container requirements. Base-profile issues are prefixed "pdfa-3/".
type FacturXViolation struct {
	Rule    string
	Message string
	Object  int
}

func (v FacturXViolation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("Factur-X %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("Factur-X %s: %s", v.Rule, v.Message)
}

// FacturXResult is the outcome of validating a Factur-X container: the
// violations found and, when identifiable, the declared conformance profile and
// the embedded invoice XML (for downstream EN 16931 validation).
type FacturXResult struct {
	Violations []FacturXViolation
	Profile    FacturXProfile // "" if not identifiable
	XMLName    string         // embedded invoice filename, "" if not found
	XML        []byte         // decoded invoice XML bytes, nil if not found
}

// ValidateFacturX checks whether doc is a conforming Factur-X invoice container.
// rawData is the original file bytes, needed for the PDF/A-3 byte-level checks.
func ValidateFacturX(doc *Document, rawData []byte) FacturXResult {
	var res FacturXResult
	add := func(rule, msg string, obj int) {
		res.Violations = append(res.Violations, FacturXViolation{Rule: rule, Message: msg, Object: obj})
	}

	// A Factur-X file shall be PDF/A-3. pdf0 validates at level B; PDF/A-3 also
	// permits level A (which only adds tagging), so the sole A-vs-B difference
	// pdf0 reports — the pdfaid:conformance letter — is suppressed here.
	for _, e := range ValidatePDFABytes(doc, PDFA3b, rawData) {
		if e.Rule == "6.6.4" && strings.Contains(e.Message, "pdfaid:conformance") {
			continue
		}
		add("pdfa-3/"+e.Rule, e.Message, e.Object)
	}

	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		add("structure", "document has no catalog", 0)
		return res
	}

	// Locate the embedded invoice XML as an associated file (/AF).
	fs, name, num := findFacturXAttachment(doc, cat)
	if fs == nil {
		add("attachment", "no embedded invoice XML (factur-x.xml or zugferd-invoice.xml) is present as an associated file", 0)
	} else {
		res.XMLName = name
		if rel, ok := fs.Get("AFRelationship").(Name); !ok || !facturxRelationships[rel] {
			add("attachment", "the invoice XML /AFRelationship shall be /Data, /Alternative or /Source", num)
		}
		if ef := doc.ResolveDict(fs.Get("EF")); ef != nil {
			if st, ok := doc.Resolve(ef.Get("F")).(*Stream); ok {
				res.XML = decodeContentStream(doc, st)
				if sub, _ := st.Dict.Get("Subtype").(Name); !facturxIsXMLSubtype(sub) {
					add("attachment", fmt.Sprintf("the invoice embedded-file /Subtype should be text/xml, got /%s", sub), num)
				}
			} else {
				add("attachment", "the invoice file specification has no embedded file stream (/EF /F)", num)
			}
		} else {
			add("attachment", "the invoice file specification has no /EF entry", num)
		}
	}

	// Factur-X XMP metadata (the fx: namespace; zf: is the ZUGFeRD equivalent).
	xmp := facturxXMP(doc, cat)
	if xmp == "" {
		add("metadata", "document has no XMP metadata", 0)
		return res
	}
	get := func(prop string) string {
		if v := strings.TrimSpace(extractXMPValue(xmp, "fx:"+prop)); v != "" {
			return v
		}
		return strings.TrimSpace(extractXMPValue(xmp, "zf:"+prop))
	}
	docType := get("DocumentType")
	fileName := get("DocumentFileName")
	version := get("Version")
	level := get("ConformanceLevel")

	if docType == "" {
		add("metadata", "missing XMP fx:DocumentType", 0)
	} else if docType != "INVOICE" && docType != "ORDER" {
		add("metadata", fmt.Sprintf("XMP fx:DocumentType %q is not INVOICE", docType), 0)
	}
	if version == "" {
		add("metadata", "missing XMP fx:Version", 0)
	}
	if fileName == "" {
		add("metadata", "missing XMP fx:DocumentFileName", 0)
	} else if name != "" && fileName != name {
		add("metadata", fmt.Sprintf("XMP fx:DocumentFileName %q does not match the embedded file name %q", fileName, name), 0)
	}
	if level == "" {
		add("metadata", "missing XMP fx:ConformanceLevel", 0)
	} else if p, ok := facturxProfiles[level]; !ok {
		add("metadata", fmt.Sprintf("XMP fx:ConformanceLevel %q is not a Factur-X profile", level), 0)
	} else {
		res.Profile = p
	}

	return res
}

// findFacturXAttachment returns the file specification for the embedded invoice
// XML (located via the catalog /AF associated-files array), its decoded file
// name, and its object number.
func findFacturXAttachment(doc *Document, cat *Dictionary) (*Dictionary, string, int) {
	af, ok := doc.Resolve(cat.Get("AF")).(Array)
	if !ok {
		return nil, "", 0
	}
	for _, e := range af {
		fs := doc.ResolveDict(e)
		if fs == nil {
			continue
		}
		name := facturxFileSpecName(doc, fs)
		if facturxXMLNames[strings.ToLower(name)] {
			return fs, name, refNum(e)
		}
	}
	return nil, "", 0
}

// facturxFileSpecName returns a file specification's name, preferring the
// Unicode /UF entry (decoded from its UTF-16 or PDFDoc encoding) over /F.
func facturxFileSpecName(doc *Document, fs *Dictionary) string {
	for _, key := range []Name{"UF", "F"} {
		if s, ok := doc.Resolve(fs.Get(key)).(String); ok {
			if name := decodePDFTextString(s.Value); name != "" {
				return name
			}
		}
	}
	return ""
}

func facturxIsXMLSubtype(sub Name) bool {
	s := strings.ToLower(string(sub))
	return s == "text/xml" || s == "application/xml"
}

// facturxXMP returns the document's decoded XMP metadata packet, or "".
func facturxXMP(doc *Document, cat *Dictionary) string {
	ms, ok := doc.Resolve(cat.Get("Metadata")).(*Stream)
	if !ok {
		return ""
	}
	return decodeXMPToUTF8(decodeContentStream(doc, ms))
}
