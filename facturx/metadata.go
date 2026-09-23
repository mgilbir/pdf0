package facturx

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
)

// The container's XMP identification, read and written through pdf0's one XMP
// model.
//
// It used to be read by scraping the packet text for "fx:" and "zf:" prefixed
// elements, which read whatever namespace the prefix happened to mean — the
// invoice and the order namespaces alike, so neither validator could tell an
// invoice's metadata from an order's — and missed a packet that used another
// prefix, as the Order-X specification's own XMP example does ("fx_1_"). It is
// read by namespace URI now, and each validator knows which namespaces are its
// family's.

// The Factur-X family's XMP namespaces. The prefix is conventionally fx (zf for
// ZUGFeRD 1.0); the namespace is what identifies the property.
const (
	// NSFacturX is the Factur-X 1.0 (and ZUGFeRD 2.1+) invoice namespace.
	NSFacturX = "urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#"
	// NSZUGFeRD2 is the ZUGFeRD 2.0 invoice namespace.
	NSZUGFeRD2 = "urn:zugferd:pdfa:CrossIndustryDocument:invoice:2p0#"
	// NSZUGFeRD1 is the ZUGFeRD 1.0 invoice namespace (prefix zf).
	NSZUGFeRD1 = "urn:ferd:pdfa:CrossIndustryDocument:invoice:1p0#"
	// NSOrderX is the Order-X 1.0 namespace (Order-X specification, "XMP
	// example"): the invoice namespace without its ":invoice" segment.
	NSOrderX = "urn:factur-x:pdfa:CrossIndustryDocument:1p0#"
)

// family is a document family's identification: the namespaces its metadata
// may use, and the attachment names it may carry.
type family struct {
	name       string   // "Factur-X" or "Order-X", for messages
	namespaces []string // in order of preference; the first is what pdf0 writes
	fileNames  []string // exactly as the specifications spell them
}

var (
	invoiceFamily = family{
		name:       "Factur-X",
		namespaces: []string{NSFacturX, NSZUGFeRD2, NSZUGFeRD1},
		// factur-x.xml (Factur-X, ZUGFeRD 2.1+), zugferd-invoice.xml (ZUGFeRD
		// 2.0), ZUGFeRD-invoice.xml (ZUGFeRD 1.0, which capitalised it) and
		// xrechnung.xml (a ZUGFeRD 2.x XRechnung container).
		fileNames: []string{"factur-x.xml", "zugferd-invoice.xml", "ZUGFeRD-invoice.xml", "xrechnung.xml"},
	}
	orderFamily = family{
		name:       "Order-X",
		namespaces: []string{NSOrderX},
		fileNames:  []string{"order-x.xml", "zugferd-order.xml"},
	}
)

// matchName reports whether name is one of the family's attachment names,
// ignoring case, and whether it is spelled exactly as the specification
// spells it. The first is what makes an attachment the invoice; the second is
// a finding of its own, because a consumer is entitled to look it up by the
// exact name.
func (f family) matchName(name string) (isOurs, exact bool) {
	for _, n := range f.fileNames {
		if name == n {
			return true, true
		}
		if strings.EqualFold(name, n) {
			isOurs = true
		}
	}
	return isOurs, false
}

// containerMetadata is what the XMP says about the embedded document.
type containerMetadata struct {
	status core.XMPStatus
	// ns is the namespace the properties were read from; "" when none of the
	// family's namespaces (nor the other family's) carries any.
	ns string
	// foreign is set when the properties were found only in the other
	// family's namespace, which is itself a finding.
	foreign bool
	// strays are namespaces other than the known ones in which a DocumentType
	// property was found — metadata that means to identify the container and
	// does not.
	strays []string

	docType, fileName, version, level string
}

var containerProperties = []string{"DocumentType", "DocumentFileName", "Version", "ConformanceLevel"}

// readContainerMetadata reads the fx properties for family f; other is the
// sibling family, consulted only to say where misplaced metadata went.
func readContainerMetadata(doc core.View, f, other family) containerMetadata {
	packet, status := doc.DocumentXMPPacket()
	m := containerMetadata{status: status}
	if status != core.XMPParsed {
		return m
	}
	has := func(ns string) bool {
		for _, name := range containerProperties {
			if _, ok := packet.Get(ns, name); ok {
				return true
			}
		}
		return false
	}
	for _, ns := range f.namespaces {
		if has(ns) {
			m.ns = ns
			break
		}
	}
	if m.ns == "" {
		for _, ns := range other.namespaces {
			if has(ns) {
				m.ns, m.foreign = ns, true
				break
			}
		}
	}
	if m.ns == "" {
		known := map[string]bool{}
		for _, ns := range append(append([]string(nil), f.namespaces...), other.namespaces...) {
			known[ns] = true
		}
		seen := map[string]bool{}
		for _, p := range packet.Properties() {
			if p.Name == "DocumentType" && !known[p.NS] && !seen[p.NS] && (p.Prefix == "fx" || p.Prefix == "zf") {
				seen[p.NS] = true
				m.strays = append(m.strays, p.NS)
			}
		}
		return m
	}
	m.docType, _ = packet.Text(m.ns, "DocumentType")
	m.fileName, _ = packet.Text(m.ns, "DocumentFileName")
	m.version, _ = packet.Text(m.ns, "Version")
	m.level, _ = packet.Text(m.ns, "ConformanceLevel")
	return m
}

// reportMetadataReadability adds the findings that say the metadata could not
// be read at all, or was read from the wrong place, and reports whether the
// property checks should run.
func reportMetadataReadability(m containerMetadata, f, other family, add func(rule, msg string, obj int)) bool {
	switch m.status {
	case core.XMPAbsent:
		add("metadata", "document has no XMP metadata", 0)
		return false
	case core.XMPMalformed:
		add("metadata", "the XMP metadata is not well-formed XML, so the "+f.name+" identification cannot be read", 0)
		return false
	case core.XMPLimit:
		// Not read: the trip is on the run and reaches the report as a
		// "limit" finding. Reporting the properties missing would be a guess.
		return false
	}
	for _, ns := range m.strays {
		add("metadata", fmt.Sprintf("XMP fx:DocumentType is in the namespace %q, which is not a %s namespace (%s)", ns, f.name, f.namespaces[0]), 0)
	}
	if m.foreign {
		add("metadata", fmt.Sprintf("the XMP fx properties are in the %s namespace %q; a %s document declares them in %q", other.name, m.ns, f.name, f.namespaces[0]), 0)
	}
	return true
}

// ExtensionSchema returns the PDF/A extension schema that declares a family's
// fx namespace, which PDF/A requires for any metadata namespace it does not
// predefine.
func extensionSchema(ns string) xmp.ExtensionSchema {
	return xmp.ExtensionSchema{
		Schema:       "Factur-X PDFA Extension Schema",
		NamespaceURI: ns,
		Prefix:       "fx",
		Properties: []xmp.ExtensionProperty{
			{Name: "DocumentFileName", ValueType: "Text", Category: "external", Description: "The name of the embedded XML document"},
			{Name: "DocumentType", ValueType: "Text", Category: "external", Description: "The type of the hybrid document in capital letters, e.g. INVOICE or ORDER"},
			{Name: "Version", ValueType: "Text", Category: "external", Description: "The actual version of the standard applying to the embedded XML document"},
			{Name: "ConformanceLevel", ValueType: "Text", Category: "external", Description: "The conformance level of the embedded XML document"},
		},
	}
}

// writeContainerMetadata sets a family's identification in a packet: the
// PDF/A-3 identification, the extension schema, the four fx properties in the
// family's namespace, and the title when given. Everything else in the packet
// is kept.
//
// The PDF/A identification is kept, not rewritten: a container is a PDF/A-3
// file, and the conformance letter it already declares (A, B or U) is the
// producer's claim to keep, not pdf0's to downgrade to B. A packet that
// declares no PDF/A part gets 3 and B, which is what the caller asserts by
// making a container of it; one that declares another part is refused, since
// no other part may carry the attachment.
func writeContainerMetadata(p *xmp.Packet, f family, docType, fileName, level, title string) error {
	part, hasPart := p.Text(xmp.NSPDFAID, "part")
	switch {
	case !hasPart:
		if err := p.SetText(xmp.NSPDFAID, "pdfaid", "part", "3"); err != nil {
			return err
		}
		if err := p.SetText(xmp.NSPDFAID, "pdfaid", "conformance", "B"); err != nil {
			return err
		}
	case part != "3":
		return fmt.Errorf("a %s container is a PDF/A-3 file, and the document declares PDF/A part %q", f.name, part)
	default:
		if c, _ := p.Text(xmp.NSPDFAID, "conformance"); c == "" {
			if err := p.SetText(xmp.NSPDFAID, "pdfaid", "conformance", "B"); err != nil {
				return err
			}
		}
	}
	ns := f.namespaces[0]
	// Metadata of the family written under an older namespace (ZUGFeRD 2.0,
	// say) is replaced, not left beside the new one to contradict it.
	for _, old := range f.namespaces[1:] {
		for _, name := range containerProperties {
			p.Remove(old, name)
		}
	}
	if err := p.SetExtensionSchema(extensionSchema(ns)); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"DocumentType", docType},
		{"DocumentFileName", fileName},
		{"Version", "1.0"},
		{"ConformanceLevel", level},
	} {
		if err := p.SetText(ns, "fx", kv[0], kv[1]); err != nil {
			return err
		}
	}
	if title != "" {
		if err := p.SetAltText(xmp.NSDC, "dc", "title", "x-default", title); err != nil {
			return fmt.Errorf("title: %w", err)
		}
	}
	return nil
}
