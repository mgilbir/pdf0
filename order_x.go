package pdf0

import (
	"fmt"
	"strings"

	"github.com/mgilbir/formalis"
)

// This file validates the container of an Order-X (a.k.a. ZUGFeRD Order) hybrid
// electronic order. Order-X is the order-document sibling of Factur-X: a PDF/A-3
// file carrying a human-readable order and an embedded UN/CEFACT Cross Industry
// Order XML (SCRDMCCBDACIOMessage) as an associated file, identified by fx: XMP
// metadata with DocumentType ORDER. It shares Factur-X's container machinery.
//
// Order-X business rules differ from EN 16931 (which is invoice-specific), so
// this validates the container and the order document's mandatory head terms,
// not a full order rule set. No public conformance corpus is bundled.

// OrderXProfile is an Order-X conformance profile, in increasing richness.
type OrderXProfile string

const (
	OrderXBasic    OrderXProfile = "BASIC"
	OrderXComfort  OrderXProfile = "COMFORT"
	OrderXExtended OrderXProfile = "EXTENDED"
)

// orderXProfileFor maps an XMP ConformanceLevel to an Order-X profile, matched
// case- and space-insensitively.
func orderXProfileFor(level string) (OrderXProfile, bool) {
	switch strings.ToUpper(strings.ReplaceAll(level, " ", "")) {
	case "BASIC":
		return OrderXBasic, true
	case "COMFORT":
		return OrderXComfort, true
	case "EXTENDED":
		return OrderXExtended, true
	}
	return "", false
}

// The embedded order XML is named order-x.xml; zugferd-order.xml is also seen.
var orderXMLNames = map[string]bool{
	"order-x.xml":       true,
	"zugferd-order.xml": true,
}

// The XMP fx:DocumentType values Order-X uses for its three message kinds.
var orderXDocumentTypes = map[string]bool{"ORDER": true, "ORDER_CHANGE": true, "ORDER_RESPONSE": true}

// OrderXResult is the outcome of validating an Order-X container.
type OrderXResult struct {
	Violations []formalis.Violation
	Profile    OrderXProfile // "" if not identifiable
	XMLName    string        // embedded order filename, "" if not found
	XML        []byte        // decoded order XML, nil if not found
}

// ValidateOrderX checks whether doc is a conforming Order-X order container.
func (doc *Document) ValidateOrderX(rawData []byte) OrderXResult {
	var res OrderXResult
	add := func(rule, msg string, obj int) {
		res.Violations = append(res.Violations, formalis.Violation{Rule: rule, Message: msg, Object: obj})
	}

	// An Order-X file shall be PDF/A-3 (validated at level B; the A-vs-B
	// conformance-letter difference is suppressed, as for Factur-X).
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

	// Locate the embedded order XML as an associated file (/AF).
	fs, name, num := findOrderXAttachment(doc, cat)
	if fs == nil {
		add("attachment", "no embedded order XML (order-x.xml) is present as an associated file", 0)
	} else {
		res.XMLName = name
		if rel, ok := fs.Get("AFRelationship").(Name); !ok || !facturxRelationships[rel] {
			add("attachment", "the order XML /AFRelationship shall be /Data, /Alternative or /Source", num)
		}
		if ef := doc.ResolveDict(fs.Get("EF")); ef != nil {
			if st, ok := doc.Resolve(ef.Get("F")).(*Stream); ok {
				res.XML = decodeContentStream(doc, st)
				if sub, _ := st.Dict.Get("Subtype").(Name); !facturxIsXMLSubtype(sub) {
					add("attachment", fmt.Sprintf("the order embedded-file /Subtype should be text/xml, got /%s", sub), num)
				}
			} else {
				add("attachment", "the order file specification has no embedded file stream (/EF /F)", num)
			}
		} else {
			add("attachment", "the order file specification has no /EF entry", num)
		}
	}

	// XMP metadata (fx: namespace; zf: is the ZUGFeRD equivalent).
	xmp := facturxXMP(doc, cat)
	if xmp == "" {
		add("metadata", "document has no XMP metadata", 0)
	} else {
		get := func(prop string) string {
			if v := strings.TrimSpace(extractXMPValue(xmp, "fx:"+prop)); v != "" {
				return v
			}
			return strings.TrimSpace(extractXMPValue(xmp, "zf:"+prop))
		}
		if dt := get("DocumentType"); dt == "" {
			add("metadata", "missing XMP fx:DocumentType", 0)
		} else if !orderXDocumentTypes[dt] {
			add("metadata", fmt.Sprintf("XMP fx:DocumentType %q is not an Order-X document type (ORDER/ORDER_CHANGE/ORDER_RESPONSE)", dt), 0)
		}
		if fn := get("DocumentFileName"); fn == "" {
			add("metadata", "missing XMP fx:DocumentFileName", 0)
		} else if name != "" && fn != name {
			add("metadata", fmt.Sprintf("XMP fx:DocumentFileName %q does not match the embedded file name %q", fn, name), 0)
		}
		if level := get("ConformanceLevel"); level == "" {
			add("metadata", "missing XMP fx:ConformanceLevel", 0)
		} else if p, ok := orderXProfileFor(level); !ok {
			add("metadata", fmt.Sprintf("XMP fx:ConformanceLevel %q is not an Order-X profile", level), 0)
		} else {
			res.Profile = p
		}
	}

	// Order document head: a well-formed Cross Industry Order with the mandatory
	// head terms (order number, issue date, type code, buyer and seller).
	if len(res.XML) > 0 {
		res.Violations = append(res.Violations, formalis.ValidateOrderXML(res.XML)...)
	}
	return res
}

// findOrderXAttachment returns the file specification for the embedded order XML.
func findOrderXAttachment(doc *Document, cat *Dictionary) (*Dictionary, string, int) {
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
		if orderXMLNames[strings.ToLower(name)] {
			return fs, name, refNum(e)
		}
	}
	return nil, "", 0
}
