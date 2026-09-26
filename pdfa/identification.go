package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
)

// The PDF/A identification, read once, through the XMP model.
//
// Every reader of pdfaid — the identification rule, the Level A and PDF/A-4
// variant rules, the relaxations they gate, DeclaredLevel and the embedded-file
// check — used to scrape the packet text for "<pdfaid:conformance>" or
// `pdfaid:conformance="`, each slightly differently. A comment holding an old
// value, whitespace around "=", an attribute on the element or a prefix other
// than "pdfaid" each made one of them read something the packet did not say
// (audit C141). They all read here now, by namespace URI, with XML unescaping
// done by the parser.

// CheckPDFAIDConformance is the Check of the finding that pdfaid:conformance is
// not the value the requested level requires. The Level A and PDF/A-4 variant
// validators, and the Factur-X container, compose on the base validator and
// replace exactly that finding with their own; they find it by this
// identifier, not by the words of its message.
const CheckPDFAIDConformance = "pdfaid-conformance"

// pdfaIDPrefix is the prefix ISO 19005 requires for the identification schema
// (19005-1 Table 3, 19005-2/-3 6.6.4, 19005-4 6.7.3).
const pdfaIDPrefix = "pdfaid"

// pdfaIdentification is what a document's metadata says about its PDF/A
// conformance.
type pdfaIdentification struct {
	// status is how the packet read. Anything but XMPParsed leaves every other
	// field zero: an absent packet declares nothing, a malformed one is the
	// well-formedness rule's to report, and one pdf0 declined to model (a
	// limit, already noted on the run) must not be read as declaring nothing.
	status core.XMPStatus

	part, conformance, rev          string
	hasPart, hasConformance, hasRev bool

	// impostor is set when a property is written with the pdfaid prefix but
	// that prefix is bound to some other namespace, so the packet looks like
	// it identifies itself and does not.
	impostor bool
	// otherPrefixes lists prefixes other than pdfaid that identification
	// properties are written with.
	otherPrefixes []string
}

func readPDFAIdentification(doc core.View) pdfaIdentification {
	packet, status := doc.DocumentXMPPacket()
	id := pdfaIdentification{status: status}
	if status != core.XMPParsed {
		return id
	}
	id.part, id.hasPart = packet.Text(xmp.NSPDFAID, "part")
	id.conformance, id.hasConformance = packet.Text(xmp.NSPDFAID, "conformance")
	id.rev, id.hasRev = packet.Text(xmp.NSPDFAID, "rev")
	seen := map[string]bool{}
	for _, p := range packet.Properties() {
		if p.Prefix == pdfaIDPrefix && p.NS != xmp.NSPDFAID {
			id.impostor = true
		}
		if p.NS == xmp.NSPDFAID && p.Prefix != pdfaIDPrefix && !seen[p.Prefix] {
			seen[p.Prefix] = true
			id.otherPrefixes = append(id.otherPrefixes, p.Prefix)
		}
	}
	return id
}
