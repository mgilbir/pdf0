package pdf0

import (
	"context"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"

	"github.com/mgilbir/formalis"
)

// This file validates the PDF container of a Factur-X (a.k.a. ZUGFeRD 2.x)
// hybrid electronic invoice. A Factur-X document is a PDF/A-3 file that carries
// a human-readable invoice and an embedded XML representation of the same
// invoice (UN/CEFACT Cross Industry Invoice, EN 16931). This validator checks
// the container — PDF/A-3 conformance, the embedded invoice XML attached as an
// associated file, and the Factur-X XMP metadata that identifies it — and then
// runs the invoice XML through the formalis EN 16931 rule engine at the declared
// profile, mirroring ValidateOrderX. CIUS layers (XRechnung, Peppol, NLCIUS)
// remain the caller's choice, via the returned XML.
//
// The checks are calibrated against a corpus of conforming Factur-X / ZUGFeRD
// invoices across all profiles (MINIMUM, BASIC WL, BASIC, EN 16931, EXTENDED).

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

// FacturXViolation is one finding of the Factur-X container validator: either a
// departure from a container rule pdf0 checks itself, or one adopted from the
// EN 16931 rule engine the embedded invoice XML is run through.
//
// It exists because this validator composes two rule engines and the caller gets
// one report. pdf0's findings anchor to a PDF object; the invoice engine's
// anchor to a business term in an XML document and name the authority that wrote
// the rule. Until formalis v0.2.0 this validator carried formalis.Violation
// values directly, borrowing that type's Object field for PDF object numbers,
// which made Factur-X and Order-X the only findings in this package that could
// not satisfy Violation — the exception the package documentation had to keep
// explaining. Both halves now arrive in a type pdf0 owns, so they combine with
// every other validator's findings and IsCheckerFinding applies to them.
type FacturXViolation struct {
	// Rule is the identifier of the rule that was broken. pdf0's own container
	// rules are "structure", "attachment", "metadata" and "invoice-xml"; the
	// PDF/A-3 base's are ISO 19005 clauses under a "pdfa-3/" prefix; the invoice
	// engine's are adopted verbatim (EN 16931's BR-*, and formalis's reserved
	// "limit", "profile" and "root"). The reserved checker identifiers "limit"
	// and "internal" keep their bare names whichever half reports them, so
	// IsCheckerFinding recognises them — see adoptPDFAFindings.
	Rule string
	// Message describes what is wrong.
	Message string
	// Object is the PDF object number the finding anchors to, and 0 when it does
	// not anchor to one: a document-wide container finding, or any finding about
	// the invoice XML, which is a document inside the PDF and not an object of
	// it. Every finding adopted from the rule engine is of the second kind.
	Object int
	// Source names the authority that defines Rule, for a finding adopted from
	// the invoice rule engine: formalis.SourceEN16931 for a business rule,
	// formalis.SourceChecker for that engine's statements about its own run. It
	// is the zero Source on pdf0's own container findings, including the PDF/A-3
	// ones, because those rules are not formalis's to attribute — and
	// formalis.SourceNone is documented as the absent authority rather than as a
	// value any formalis finding carries, so the zero value cannot be mistaken
	// for a real attribution.
	Source formalis.Source
}

// Error renders the finding, naming the authority when the finding was adopted
// from the invoice rule engine. The authority is in the string and not only in
// the field for the reason formalis gives for putting it in its own: a rule
// identifier is unique within its authority and not outside it, so a logged
// finding that omits the authority is not identified.
func (v FacturXViolation) Error() string {
	who := "Factur-X"
	if v.Source != formalis.SourceNone {
		who = "Factur-X " + string(v.Source)
	}
	if v.Object != 0 {
		return fmt.Sprintf("%s %s: %s (object %d)", who, v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("%s %s: %s", who, v.Rule, v.Message)
}

// facturxXMLRule is the rule identifier for an embedded invoice XML the rule
// engine could not read at all — XML that is not well-formed, or a character
// encoding formalis does not implement, which it reports as an error wrapping
// formalis.ErrMalformedXML rather than as a finding.
//
// It is deliberately not "limit", and the difference is the one the reserved
// identifier exists to carry. "limit" says *pdf0 stopped*, so whether the file
// conforms is unknown; a caller reads it as "ask again with more room, or
// escalate", and IsCheckerFinding hides it from a conformance count. This is a
// statement about the file, and a definite one: the attachment is not XML. It
// belongs beside "attachment" and "metadata" as a container defect, which is
// what the identifier says.
const facturxXMLRule = "invoice-xml"

// FacturXResult is the outcome of validating a Factur-X invoice: the container
// and EN 16931 violations found and, when identifiable, the declared conformance
// profile and the embedded invoice XML (returned for CIUS-layer validation).
type FacturXResult struct {
	// Violations is every non-conformance: pdf0's container findings, the PDF/A-3
	// base's, and the invoice rule engine's fatal ones — the findings whose
	// authority rejects a document for breaking them, plus that engine's
	// statements about its own run. An empty slice is the clean answer, as it is
	// for every other validator in this package.
	Violations []FacturXViolation

	// InvoiceWarnings is the advisory findings of the invoice rule engine: rules
	// their authority reports without rejecting the document, above all the CEN
	// syntax-binding rules (CII-SR-*, CII-DT-*) that hold an invoice to the
	// EN 16931 core subset of CII. A conforming Factur-X EXTENDED invoice trips
	// those by design — carrying more than the core is what EXTENDED is for — so
	// they are reported beside the verdict rather than inside it. See
	// adoptInvoiceFindings for why they are neither dropped nor merged.
	InvoiceWarnings []FacturXViolation

	Profile formalis.Profile // "" if not identifiable
	// CIUS is the Core Invoice Usage Specification the XMP names, when the
	// fx:ConformanceLevel names one instead of a data-richness profile —
	// "XRECHNUNG" is the level a ZUGFeRD 2.x producer really writes. The two
	// questions are separate and the metadata answers exactly one of them, so
	// pdf0 asks both (formalis.ProfileFor and formalis.CIUSFor) and reports what
	// it was told; a level that answers neither is a metadata finding.
	//
	// When the level named a CIUS, the embedded XML is validated by the rule set
	// the *invoice* declares in BT-24 rather than by the EN 16931 core at a
	// guessed profile, which formalis documents as the more reliable of the two
	// claims.
	CIUS    formalis.CIUS
	XMLName string // embedded invoice filename, "" if not found
	XML     []byte // decoded invoice XML bytes, nil if not found

	// InvoiceNotEvaluated names the EN 16931 rule families the invoice rule
	// engine publishes and does not evaluate, as that engine reported them for
	// this run (formalis.Report.NotEvaluated). It is what lets a caller tell "no
	// findings" from "no findings, and here is what nobody looked at".
	//
	// It is not turned into findings, deliberately. Every rule set has gaps, so a
	// finding per unevaluated family would fire on every conforming invoice ever
	// validated and would say nothing about this one. It is also not a limit
	// trip: nothing stopped, and running the same invoice again with a larger
	// budget would not close a gap that is a static property of the rule set.
	//
	// It is nil when the rule engine was never reached — no embedded XML — which
	// is the case InvoiceComplete's false zero value already covers.
	InvoiceNotEvaluated []formalis.RuleFamily

	// InvoiceComplete reports that the invoice rule engine evaluated every rule
	// that can be evaluated (formalis.Report.Complete): the run was not cut
	// short, and no evaluable family went unchecked. It is false when no invoice
	// XML was found and false when the XML could not be read, so it is never a
	// claim about a document nobody validated.
	//
	// It describes the invoice half only. pdf0's own container half — the PDF/A-3
	// base above all — implements a subset of ISO 19005 and publishes no
	// equivalent coverage table, so there is no honest whole-result version of
	// this question to answer, and a field called Complete would have claimed
	// one.
	InvoiceComplete bool
}

// ValidateFacturX checks whether doc is a conforming Factur-X invoice container.
// rawData is the original file bytes, needed for the PDF/A-3 byte-level checks.
//
// It is ValidateFacturXContext with a background context.
func ValidateFacturX(doc *Document, rawData []byte) FacturXResult {
	return ValidateFacturXContext(context.Background(), doc, rawData)
}

// ValidateFacturXContext is ValidateFacturX with cancellation.
//
// Both halves of the work honour ctx: the PDF/A-3 container validation, which is
// the larger of the two on every file in this repository's Factur-X corpus, and
// the EN 16931 rule engine, which threads the context through its own parse and
// rule loops.
//
// A cancelled run reports itself the way every other pdf0 validator does — a
// finding under the reserved rule "limit", which IsCheckerFinding recognises —
// and keeps the findings gathered before it stopped. The rule engine uses that
// same identifier for the same event, so a caller draining Violations has one
// name to look for across container and invoice findings alike. What cannot
// happen is an empty result: a cancelled validation never looks clean.
func ValidateFacturXContext(ctx context.Context, doc *Document, rawData []byte) (res FacturXResult) {
	cancel := core.NewCanceler(ctx)
	add := func(rule, msg string, obj int) {
		res.Violations = append(res.Violations, FacturXViolation{Rule: rule, Message: msg, Object: obj})
	}
	adopt := func(v formalis.Violation, advisory bool) {
		f := FacturXViolation{Rule: v.Rule, Message: v.Message, Source: v.Source}
		if advisory {
			res.InvoiceWarnings = append(res.InvoiceWarnings, f)
			return
		}
		res.Violations = append(res.Violations, f)
	}

	// The container checks are one straight-line sequence rather than a list of
	// independent checks, so they get a single recover boundary at the entry
	// point: a panic on hostile input (here or in the embedded XML rule engine)
	// becomes an "internal" finding instead of crashing the caller, and the
	// findings reported before it are kept in the named result (audit C27). The
	// deferred tail also runs on the normal path: it owes the caller the
	// cancellation finding neither half may have reported (reportCancellation),
	// and a deterministic order, since the PDF/A-3 findings this composes are
	// sorted but the container ones are appended after and the rule engine has
	// an order of its own.
	defer func() {
		if r := recover(); r != nil {
			add(internalRule, internalMessage(r), 0)
		}
		reportCancellation(cancel, res.Violations, add)
		sortViolations(res.Violations)
	}()

	// A Factur-X file shall be PDF/A-3, so the PDF/A-3 findings are adopted under
	// a "pdfa-3/" namespace — except the reserved checker identifiers, which keep
	// their names (adoptPDFAFindings).
	adoptPDFAFindings(add, "pdfa-3/", ValidatePDFABytesContext(ctx, doc, PDFA3b, rawData))

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
	} else {
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

		// Factur-X is the invoice member of the family; an ORDER document
		// belongs to ValidateOrderX (audit C44).
		if docType == "" {
			add("metadata", "missing XMP fx:DocumentType", 0)
		} else if docType != "INVOICE" {
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
		// The level answers one of two questions and never both: how rich the data
		// claims to be (a Factur-X profile) or which national rule set it claims to
		// follow (a CIUS). "XRECHNUNG" is the second, and it is a level ZUGFeRD 2.x
		// producers really write — four of this repository's conforming corpus files
		// carry it — so asking only formalis.ProfileFor and rejecting what it does
		// not know accuses a conforming container. formalis documents the pair, and
		// exactly one of the two reports true for any level either recognises.
		if level == "" {
			add("metadata", "missing XMP fx:ConformanceLevel", 0)
		} else if p, ok := formalis.ProfileFor(level); ok {
			res.Profile = p
		} else if c, ok := formalis.CIUSFor(level); ok {
			res.CIUS = c
		} else {
			add("metadata", fmt.Sprintf("XMP fx:ConformanceLevel %q is neither a Factur-X profile nor a CIUS", level), 0)
		}
	}

	// Invoice content: the embedded XML must satisfy the EN 16931 business
	// rules at the declared profile, exactly as ValidateOrderX runs the order
	// rules inline (audit C44). See validateInvoiceXML for which rule set runs.
	if len(res.XML) > 0 {
		rep, err := res.validateInvoiceXML(ctx)
		if err != nil {
			// The error means the attachment could not be read as XML at all, and
			// the Report returned with it is the zero Report. Dropping it would
			// leave a container whose invoice was never validated looking exactly
			// like one whose invoice passed, which is the single outcome this whole
			// design refuses; it is reported as a container defect for the reason
			// facturxXMLRule gives, anchored to the file specification it came from.
			add(facturxXMLRule, "the embedded invoice XML could not be read: "+err.Error(), num)
		}
		res.InvoiceNotEvaluated = rep.NotEvaluated
		res.InvoiceComplete = rep.Complete()
		adoptInvoiceFindings(adopt, rep)
	}
	return res
}

// validateInvoiceXML runs the extracted invoice XML through the rule set the
// container declared. It is a method on the result rather than a step inside
// ValidateFacturXContext so that a caller — or a test — reading res.XML can
// reproduce exactly the run whose findings res.Violations holds, instead of
// guessing the entry point from res.Profile and getting a different rule set.
//
// Which rule set runs follows what the container actually declared, and the
// three cases are three different claims:
//
//   - a data-richness profile: the EN 16931 core at that profile, which is the
//     one thing a Profile does — excuse the rules a leaner tier need not meet.
//     This is what formalis.Validate exists for, and pdf0 is the caller it names:
//     the metadata lives in the container, not in the invoice.
//   - a CIUS and no profile: the rule set the *invoice* declares in BT-24, which
//     formalis routes on and documents as the more reliable of the two claims. A
//     CIUS is not a profile and must not be passed as one — that conflation was
//     the bug formalis removed when it stopped accepting "XRECHNUNG" as a
//     Profile, since the call that looked most like "validate this as XRechnung"
//     was the one that applied no German rule at all.
//   - neither: res.Profile is passed through empty rather than replaced by a
//     guess. The engine refuses a profile it does not implement and says so under
//     its reserved "profile" rule, so the caller is told the invoice was not
//     checked; substituting EN 16931 would run a rule set the document never
//     claimed and report the result as if it had. pdf0 has already reported the
//     container defect that got here, as a "metadata" finding.
func (res FacturXResult) validateInvoiceXML(ctx context.Context) (formalis.Report, error) {
	if res.Profile == "" && res.CIUS != formalis.CIUSNone {
		return formalis.ValidateCIUS(ctx, res.XML)
	}
	return formalis.Validate(ctx, res.XML, res.Profile)
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
			return fs, name, object.RefNum(e)
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
	return xmpText(doc, ms)
}
