package facturx

import (
	"context"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
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

// OrderXViolation is one finding of the Order-X container validator: either a
// departure from a container rule pdf0 checks itself, or one adopted from the
// order rule engine the embedded order XML is run through. Its fields mean what
// FacturXViolation's mean, which documents them.
//
// It is a type of its own rather than a shared invoice-container finding, for
// the reason every other validator in this package has one: an Order-X is a
// different standard judged by a different rule set, and Error has to say so —
// a caller holding one mixed report must be able to read "ORDER-03" and know it
// is the order type code and not something an invoice rule engine said. The two
// namespaces really do overlap, and have already collided once inside formalis
// itself, which renamed its Order-X rules out of CEN's BR-O-* numbering after a
// caller aggregating by identifier merged two unrelated defects. Sharing one
// type here would put that collision back one level up, in the type a caller
// switches on.
type OrderXViolation struct {
	Rule    string
	Message string
	Object  int
	Source  formalis.Source
}

// Error renders the finding; see FacturXViolation.Error.
func (v OrderXViolation) Error() string {
	who := "Order-X"
	if v.Source != formalis.SourceNone {
		who = "Order-X " + string(v.Source)
	}
	if v.Object != 0 {
		return fmt.Sprintf("%s %s: %s (object %d)", who, v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("%s %s: %s", who, v.Rule, v.Message)
}

// orderXMLRule is facturxXMLRule for the order document: the embedded XML could
// not be read at all. See facturxXMLRule for why this is not "limit".
const orderXMLRule = "order-xml"

// OrderXResult is the outcome of validating an Order-X container.
type OrderXResult struct {
	Violations []OrderXViolation
	Profile    OrderXProfile // "" if not identifiable
	XMLName    string        // embedded order filename, "" if not found
	XML        []byte        // decoded order XML, nil if not found

	// OrderWarnings is the advisory findings of the order rule engine, kept out
	// of the verdict for the reason FacturXResult.InvoiceWarnings gives. It is
	// empty today and would stay empty if this field did not exist: no authority
	// has flagged an Order-X rule advisory, and formalis's five ORDER-* rules are
	// all fatal by its own decision. It exists so that the split at the adoption
	// seam is total — a warning that arrived tomorrow would otherwise be counted
	// as a non-conformance by every caller, silently, which is exactly how the
	// EN 16931 syntax bindings turned every conforming EXTENDED invoice into a
	// forty-finding failure the first time this bump was compiled.
	OrderWarnings []OrderXViolation

	// OrderNotEvaluated and OrderComplete report what the order rule engine did
	// and did not evaluate, exactly as FacturXResult's invoice pair does; that
	// documentation applies unchanged.
	//
	// OrderComplete is false for every order, and will stay false until the rule
	// engine implements more than the five mandatory head terms: formalis
	// publishes that gap as a fatal family under Coverage(SourceOrderX), so
	// OrderNotEvaluated is never empty for a run that reached the engine. That is
	// precisely the state this pair exists to make visible — a clean report from
	// a rule set that checks five things — and not a defect in any order.
	OrderNotEvaluated []formalis.RuleFamily
	OrderComplete     bool
}

// ValidateOrderX checks whether doc is a conforming Order-X order container.
//
// It is ValidateOrderXContext with a background context.
func ValidateOrder(doc core.View, rawData []byte) OrderXResult {
	return ValidateOrderContext(context.Background(), doc, rawData)
}

// ValidateOrderXContext is ValidateOrderX with cancellation. Both halves of the
// work honour ctx — the PDF/A-3 container validation and the order rules — and a
// cancelled run reports a "limit" finding rather than an empty result, exactly
// as ValidateFacturXContext does and for the same reasons.
func ValidateOrderContext(ctx context.Context, doc core.View, rawData []byte) (res OrderXResult) {
	cancel := core.NewCanceler(ctx)
	add := func(rule, msg string, obj int) {
		res.Violations = append(res.Violations, OrderXViolation{Rule: rule, Message: msg, Object: obj})
	}
	adopt := func(v formalis.Violation, advisory bool) {
		f := OrderXViolation{Rule: v.Rule, Message: v.Message, Source: v.Source}
		if advisory {
			res.OrderWarnings = append(res.OrderWarnings, f)
			return
		}
		res.Violations = append(res.Violations, f)
	}

	// One recover boundary at the entry point, the cancellation finding neither
	// half may have reported, and a deterministic order on the way out — as in
	// ValidateFacturXContext, whose structure this mirrors (audit C27).
	defer func() {
		if r := recover(); r != nil {
			add(finding.InternalRule, finding.InternalMessage(r), 0)
		}
		finding.ReportCancellation(cancel, res.Violations, add)
		finding.Sort(res.Violations)
	}()

	// An Order-X file shall be PDF/A-3, adopted exactly as for Factur-X.
	adoptPDFAFindings(add, "pdfa-3/", pdfaFindings(doc))

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
		if rel, ok := fs.Get("AFRelationship").(object.Name); !ok || !facturxRelationships[rel] {
			add("attachment", "the order XML /AFRelationship shall be /Data, /Alternative or /Source", num)
		}
		if ef := doc.ResolveDict(fs.Get("EF")); ef != nil {
			if st, ok := doc.Resolve(ef.Get("F")).(*object.Stream); ok {
				res.XML = doc.Content(st)
				if sub, _ := st.Dict.Get("Subtype").(object.Name); !facturxIsXMLSubtype(sub) {
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
			if v := strings.TrimSpace(core.ExtractXMPValue(xmp, "fx:"+prop)); v != "" {
				return v
			}
			return strings.TrimSpace(core.ExtractXMPValue(xmp, "zf:"+prop))
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
		rep, err := formalis.ValidateOrderXML(ctx, res.XML)
		if err != nil {
			// Unreadable XML is an error rather than a finding, and the Report that
			// comes with it is the zero Report — so dropping it would leave an
			// unvalidated order looking like a clean one. See facturxXMLRule.
			add(orderXMLRule, "the embedded order XML could not be read: "+err.Error(), num)
		}
		res.OrderNotEvaluated = rep.NotEvaluated
		res.OrderComplete = rep.Complete()
		adoptInvoiceFindings(adopt, rep)
	}
	return res
}

// findOrderXAttachment returns the file specification for the embedded order XML.
func findOrderXAttachment(doc core.View, cat *object.Dictionary) (*object.Dictionary, string, int) {
	af, ok := doc.Resolve(cat.Get("AF")).(object.Array)
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
			return fs, name, object.RefNum(e)
		}
	}
	return nil, "", 0
}
