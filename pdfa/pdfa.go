package pdfa

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"math"
	"strings"
)

// This file is the core of the PDF/A validator: the conformance levels
// (ISO 19005-1/-2/-3/-4, i.e. 1b/2b/3b/4, plus the entry point into Level A),
// the ValidatePDFA/ValidatePDFABytes dispatchers, and most of the clause-6
// rule set — file structure (6.1), graphics, colour and fonts (6.2),
// annotations and font dictionaries (6.3), interactive forms (6.4), actions
// (6.6) and metadata (6.7). Clause numbering differs between the parts, so a
// check that spans levels picks its reported rule ID from the level.
//
// The rules are calibrated against the veraPDF corpus, which is treated as the
// authoritative oracle wherever it disagrees with a plain reading of the
// standard: a false positive rejects a conforming file and is far worse than a
// missed violation. Each run installs a validationCache on a shallow copy of
// the Document, so the checks share page-tree walks and decoded content
// streams without touching the caller's Document or racing another run.

// PDFALevel represents a PDF/A conformance level.
type PDFALevel int

const (
	PDFA1b PDFALevel = iota
	PDFA2b
	PDFA3b
	PDFA4
	// Level A (accessible) conformance: Level B plus tagged logical structure,
	// natural-language specification and Unicode character mapping. PDF/A-4 has
	// no Level A — accessibility there is expressed via PDF/UA-2.
	PDFA1a
	PDFA2a
	PDFA3a
)

// pdfaCache is this engine's memo for one run: the annotations found directly
// on pages rather than through the page tree. It is reached through core.Slot
// rather than held on the shared run state, because nothing else reads it.
type pdfaMemoCache struct {
	directAnnots    []annotOccurrence
	hasDirectAnnots bool
}

// pdfaSlot keys pdfaMemoCache; an unexported empty struct cannot collide.
type pdfaSlot struct{}

func pdfaMemo(d core.View) *pdfaMemoCache { return core.Slot[pdfaMemoCache](d.Run, pdfaSlot{}) }

func (l PDFALevel) String() string {
	switch l {
	case PDFA1b:
		return "PDF/A-1b"
	case PDFA2b:
		return "PDF/A-2b"
	case PDFA3b:
		return "PDF/A-3b"
	case PDFA4:
		return "PDF/A-4"
	case PDFA1a:
		return "PDF/A-1a"
	case PDFA2a:
		return "PDF/A-2a"
	case PDFA3a:
		return "PDF/A-3a"
	default:
		return fmt.Sprintf("PDFALevel(%d)", int(l))
	}
}

// IsA reports whether l is a Level A (accessible) conformance level.
func (l PDFALevel) IsA() bool { return l == PDFA1a || l == PDFA2a || l == PDFA3a }

// BaseB returns the Level B conformance level whose requirements a Level A level
// includes (1a→1b, 2a→2b, 3a→3b); for a non-A level it returns the level itself.
func (l PDFALevel) BaseB() PDFALevel {
	switch l {
	case PDFA1a:
		return PDFA1b
	case PDFA2a:
		return PDFA2b
	case PDFA3a:
		return PDFA3b
	}
	return l
}

// ValidationError describes a single PDF/A conformance violation.
type ValidationError struct {
	Rule    string    // e.g., "6.1.3" (ISO 19005 clause)
	Level   PDFALevel // the level that requires this rule
	Message string
	Object  int // object number, 0 if N/A
}

// RuleID returns the ISO 19005 clause identifier.
func (e ValidationError) RuleID() string { return e.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (e ValidationError) ObjectNum() int { return e.Object }

func (e ValidationError) Error() string {
	if e.Object != 0 {
		return fmt.Sprintf("[%s %s] object %d: %s", e.Level, e.Rule, e.Object, e.Message)
	}
	return fmt.Sprintf("[%s %s] %s", e.Level, e.Rule, e.Message)
}

// runCheck runs one validation check, converting a panic into a reported
// violation instead of letting it crash the caller. The validator processes
// untrusted files, so a bug (or an adversarial structure) in one check must not
// take down the whole process. Stack overflows from unbounded recursion are
// fatal and cannot be recovered here; those are prevented at their source.
//
// The rule identifier and the message come from validator_guard.go, which the
// other validators' equivalents also use: "internal" is a reserved identifier
// naming the checker rather than the document (IsCheckerFinding), so every
// boundary in the package has to spell it the same way.
func runCheck(doc core.View, level PDFALevel, check func(core.View, PDFALevel) []ValidationError) (out []ValidationError) {
	defer func() {
		if r := recover(); r != nil {
			out = []ValidationError{{Rule: finding.InternalRule, Level: level, Message: finding.InternalMessage(r)}}
		}
	}()
	return check(doc, level)
}

// runByteCheck is runCheck for the byte-level checks, which have a different
// signature.
func runByteCheck(level PDFALevel, check func() []ValidationError) (out []ValidationError) {
	defer func() {
		if r := recover(); r != nil {
			out = []ValidationError{{Rule: finding.InternalRule, Level: level, Message: finding.InternalMessage(r)}}
		}
	}()
	return check()
}

// ValidateView runs the PDF/A pipeline over a view.
func ValidateView(doc core.View, level PDFALevel, rawData []byte) []ValidationError {
	// Level A conformance is Level B plus the accessibility requirements; it is
	// validated by running the Level B checks and adding the Level A rule
	// families (see validatePDFALevelA).
	if level.IsA() {
		return ValidateLevelAView(doc, level, rawData)
	}

	var errs []ValidationError

	checks := []func(core.View, PDFALevel) []ValidationError{
		// File structure (6.1)
		checkNoEncrypt,
		checkFileID,
		checkHeader,
		checkTrailerInfo,
		// Catalog (6.1.12)
		checkMetadataStream,
		checkOutputIntents,
		checkOutputIntentProfile,
		checkNoCatalogAA,
		checkNoOCProperties,
		// Streams (6.1.6)
		checkNoLZW,
		checkNoExternalStreams,
		// Fonts (6.2.10)
		checkFontsEmbedded,
		// Annotations (6.3)
		checkAnnotationSubtypes,
		checkAnnotationFlags,
		checkAnnotationAppearance,
		// Interactive forms (6.4)
		checkWidgetNoAction,
		checkNoXFA,
		checkNeedAppearances,
		// Actions (6.6)
		checkNoForbiddenActions,
		checkNamedActions,
		checkAnnotationAA,
		// Metadata (6.7)
		checkMetadataVersion,
		// Transparency (PDFA-1b only)
		checkNoTransparency,
		// Images (6.2.7)
		checkNoAlternateImages,
		checkInterpolate,
		checkNoOPI,
		// Catalog version (6.1.12)
		checkCatalogVersion,
		// Font subsets (6.2.10)
		checkFontSubsets,
		// ExtGState forbidden keys (6.2.5)
		checkExtGState,
		// Info/XMP consistency (6.7.3)
		checkInfoXMPConsistency,
		// Transparency blending (6.2.4)
		checkTransparencyBlending,
		// Embedded files (6.1.12)
		checkEmbeddedFiles,
		// Optional content (6.1.13)
		checkOptionalContent,
		// Implementation limits (6.1.7)
		checkImplementationLimits,
		// Device color spaces (6.2.3/6.2.4)
		checkDeviceColorSpaces,
		// ICCBased color spaces (6.2.4.2)
		checkICCBasedProfiles,
		// Separation/DeviceN color spaces (6.2.4.4)
		checkSeparationDeviceN,
		// Permissions dictionary (6.1.12)
		checkPermsDict,
		// XMP metadata properties (6.7.2 at 1b / 6.6.2.3 at 2b/3b)
		checkXMPProperties,
		// XMP packet header / well-formedness (6.6.2.1 / 6.7.2.1)
		checkXMPWellFormed,
		// ICCBased overprint and profile-identity rules (6.2.4.2)
		checkICCBasedUsageRules,
		checkICCProfileIdentity,
		// JPEG2000 image restrictions (6.2.8.3)
		checkJPXImages,
		// Font dictionary rules (6.3 / 6.2.11 / 6.2.10)
		checkFontDictionaries,
		// Content-stream operators (6.2.2)
		checkContentStreamOperators,
		// Prohibited catalog/page entries (6.11 / 6.12)
		checkProhibitedCatalogEntries,
		// Image interpolation / rendering intent (6.2.4-6.2.9)
		checkImageIntentAndInterpolate,
		// File trailer identifier (6.1.3)
		checkFileTrailerID,
		// PDF/A-4 trigger events (6.6.3)
		checkA4TriggerEvents,
		// ActualText Private Use Area values (6.2.10.8)
		checkActualTextPUA,
		// Type 5 halftone components (6.2.5)
		checkType5Halftones,
		// Embedded PDF/A files (6.9)
		checkEmbeddedPDFA,
		// Inherited page XObject (6.2.2)
		checkInheritedPageXObject,
		// object.Stream /Length correctness (6.1.6/6.1.7)
		checkStreamLength,
		// object.Object stream decodability (6.1.6/6.1.7)
		checkObjectStreamDecodable,
		// Subset CharSet/CIDSet completeness (6.3.5 / 6.2.11.4.2)
		checkFontSubsetCompleteness,
		// CMap CID implementation limit (6.1.12 / 6.1.13)
		checkCMapCIDLimit,
		// PDF/A-1 CIDSet program completeness (6.3.5)
		checkCIDSetProgramComplete,
		// CMap embedding (6.3.3.3, PDF/A-1 only)
		checkCMapEmbedded,
	}

	// The check list is the coarsest cancellation boundary: a cancelled run
	// abandons every check it has not started. It is not the only one — the
	// traversals inside a check consult the same signal per page, per content
	// stream and per megabyte scanned — because a single check over a large
	// document is itself seconds of work. See cancel.go.
	for _, check := range checks {
		if doc.Cancel.Stopped() {
			break
		}
		errs = append(errs, runCheck(doc, level, check)...)
	}

	// Byte-level checks (require raw file data)
	if rawData != nil && !doc.Cancel.Stopped() {
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkNoDataAfterEOF(rawData, level) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkFileStructureBytes(doc, level, rawData) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkLinearizedTrailerID(rawData, level) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkStreamLengthBytes(doc, level, rawData) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkSignatureByteRange(doc, level, rawData) })...)
	}

	return errs

}

// --- File structure checks (6.1) ---

// Rule 6.1.3-2: Encrypt key must not be present in trailer dictionary.
func checkNoEncrypt(doc core.View, level PDFALevel) []ValidationError {
	if doc.Trailer.Get("Encrypt") != nil {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer must not contain /Encrypt",
		}}
	}
	return nil
}

// Rule 6.1.3-1: Document trailer must contain non-empty ID entry.
func checkFileID(doc core.View, level PDFALevel) []ValidationError {
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer must contain /ID array",
		}}
	}
	arr, ok := idObj.(object.Array)
	if !ok {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "/ID must be an array",
		}}
	}
	if len(arr) != 2 {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "/ID array must have exactly 2 elements",
		}}
	}
	for i, elem := range arr {
		if _, ok := elem.(object.String); !ok {
			return []ValidationError{{
				Rule:    "6.1.3",
				Level:   level,
				Message: fmt.Sprintf("/ID element %d must be a string", i),
			}}
		}
	}
	return nil
}

// Rule 6.1.2-1: File header version must match level.
func checkHeader(doc core.View, level PDFALevel) []ValidationError {
	switch level {
	case PDFA1b:
		// The 19005-1 header rule is about format, not version: the veraPDF
		// corpus passes a %PDF-2.0 header at PDF/A-1b. No version check.
	case PDFA2b, PDFA3b:
		// PDF/A-2/3 accept any PDF 1.x header (1.0-1.7): the standard is
		// built on PDF 1.7 but earlier headers are legal; the previous
		// 1.4-1.7 floor false-positived on conforming 1.0-1.3 files.
		valid := len(doc.Version) == 3 && strings.HasPrefix(doc.Version, "1.") &&
			doc.Version[2] >= '0' && doc.Version[2] <= '7'
		if !valid {
			return []ValidationError{{
				Rule:    "6.1.2",
				Level:   level,
				Message: fmt.Sprintf("header version must be 1.0-1.7, got %s", doc.Version),
			}}
		}
	case PDFA4:
		if !strings.HasPrefix(doc.Version, "2.") {
			return []ValidationError{{
				Rule:    "6.1.2",
				Level:   level,
				Message: fmt.Sprintf("version must be 2.x, got %s", doc.Version),
			}}
		}
	}
	return nil
}

// Rules 6.1.3-4, 6.1.3-5: Info key requires PieceInfo; Info may only contain ModDate.
func checkTrailerInfo(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil // only applies to PDF/A-4
	}

	infoRef := doc.Trailer.Get("Info")
	if infoRef == nil {
		return nil
	}

	catalog := doc.Catalog()

	// Rule 6.1.3-4: Info requires PieceInfo in catalog
	if catalog == nil || catalog.Get("PieceInfo") == nil {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer /Info requires /PieceInfo in document catalog",
		}}
	}

	// Rule 6.1.3-5: Info may only contain ModDate
	infoDict := doc.ResolveDict(infoRef)
	if infoDict == nil {
		return nil
	}
	for _, key := range infoDict.Keys {
		if key != "ModDate" {
			return []ValidationError{{
				Rule:    "6.1.3",
				Level:   level,
				Message: fmt.Sprintf("Info dictionary may only contain /ModDate, found /%s", string(key)),
			}}
		}
	}

	return nil
}

// Rule 6.1.3-3: No data after the last %%EOF marker.
func checkNoDataAfterEOF(rawData []byte, level PDFALevel) []ValidationError {
	eofMarker := []byte("%%EOF")
	idx := bytes.LastIndex(rawData, eofMarker)
	if idx < 0 {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "%%EOF marker not found",
		}}
	}
	pos := idx + len(eofMarker)
	// Skip optional EOL after %%EOF
	if pos < len(rawData) && rawData[pos] == '\r' {
		pos++
	}
	if pos < len(rawData) && rawData[pos] == '\n' {
		pos++
	}
	if pos < len(rawData) {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "data found after last %%EOF marker",
		}}
	}
	return nil
}

// --- Catalog checks ---

func getCatalog(doc core.View) *object.Dictionary {
	return doc.Catalog()
}

// Rule 6.7.2.1-1: Catalog requires Metadata stream with Type/Metadata, Subtype/XML, no Filter.
func checkMetadataStream(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return []ValidationError{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "catalog not found",
		}}
	}

	metaRef := catalog.Get("Metadata")
	if metaRef == nil {
		return []ValidationError{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "catalog must have /Metadata entry",
		}}
	}

	metaObj := doc.Resolve(metaRef)
	if metaObj == nil {
		return []ValidationError{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "/Metadata reference target not found",
		}}
	}

	stream, ok := metaObj.(*object.Stream)
	if !ok {
		return []ValidationError{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "/Metadata must be a stream",
		}}
	}

	var errs []ValidationError

	if t := stream.Dict.Get("Type"); t == nil || t != object.Name("Metadata") {
		errs = append(errs, ValidationError{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Type /Metadata",
		})
	}

	if st := stream.Dict.Get("Subtype"); st == nil || st != object.Name("XML") {
		errs = append(errs, ValidationError{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Subtype /XML",
		})
	}

	// ISO 19005-1 (PDF/A-1) 6.7.2 forbids a Filter on the document metadata
	// stream; PDF/A-2 and PDF/A-3 removed that restriction (a permitted filter
	// such as FlateDecode is allowed). veraPDF carries the PDMetadata Filter rule
	// only in its PDF/A-1 profile.
	if level == PDFA1b && stream.Dict.Get("Filter") != nil {
		errs = append(errs, ValidationError{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must not have /Filter",
		})
	}

	return errs
}

// Rule 6.2.3: OutputIntents requirements.
// colourClause returns the ISO clause for a colour-rule concept at the given
// level. Colour is under 6.2.3.x in ISO 19005-1 but 6.2.4.x in parts 2/3/4, and
// output intents move from 6.2.2 to 6.2.3; clauses follow the veraPDF profiles.
func colourClause(concept string, level PDFALevel) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"outputIntent": {"6.2.2", "6.2.3", "6.2.3"},
		"iccBased":     {"6.2.3.2", "6.2.4.2", "6.2.4.2"},
		"deviceColour": {"6.2.3.3", "6.2.4.3", "6.2.4.3"},
		"spot":         {"6.2.4.4", "6.2.4.4", "6.2.4.4"},
	}
	cl, ok := m[concept]
	if !ok {
		return "6.2.4"
	}
	switch level {
	case PDFA1b:
		return cl[0]
	case PDFA4:
		return cl[2]
	default:
		return cl[1]
	}
}

// annotActionClause returns the ISO clause for an annotation/action concept at
// the given level. These rules move between clause trees per part (annotations:
// 6.5.x in part 1, 6.3.x/6.4.x in parts 2/3/4; actions: 6.6.x in parts 1/4,
// 6.5.x in parts 2/3); clauses follow the veraPDF profiles.
func annotActionClause(concept string, level PDFALevel) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"subtype":    {"6.5.2", "6.3.1", "6.3.1"},
		"widget":     {"6.6.2", "6.4.1", "6.4.1"},
		"forbidden":  {"6.6.1", "6.5.1", "6.6.1"},
		"catalogAA":  {"6.6.1", "6.5.2", "6.6.3"},
		"flags":      {"6.5.3", "6.3.2", "6.3.2"},
		"appearance": {"6.5.3", "6.3.3", "6.3.3"},
	}
	c, ok := m[concept]
	if !ok {
		return "6.6.1"
	}
	switch level {
	case PDFA1b:
		return c[0]
	case PDFA4:
		return c[2]
	default:
		return c[1]
	}
}

func checkOutputIntents(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	// PDF/A-4: validate page-level OutputIntents have /S /GTS_PDFA1
	// (must run even if no catalog-level OutputIntents)
	var errsPageLevel []ValidationError
	if level == PDFA4 {
		pages := doc.Pages(catalog.Get("Pages"))
		for _, page := range pages {
			pageOIRef := page.Dict.Get("OutputIntents")
			if pageOIRef == nil {
				continue
			}
			pageOIObj := doc.Resolve(pageOIRef)
			pageOIArr, ok := pageOIObj.(object.Array)
			if !ok || len(pageOIArr) == 0 {
				continue
			}
			for j, elem := range pageOIArr {
				oiDict := doc.ResolveDict(elem)
				if oiDict == nil {
					continue
				}
				sName, _ := resolveName(doc, oiDict.Get("S"))
				if sName != "GTS_PDFA1" {
					errsPageLevel = append(errsPageLevel, ValidationError{
						Rule:    colourClause("outputIntent", level),
						Level:   level,
						Message: fmt.Sprintf("page OutputIntents[%d] must have /S /GTS_PDFA1, got /%s", j, string(sName)),
						Object:  page.ObjNum,
					})
				}
			}
		}
	}

	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return errsPageLevel // OutputIntents only required when device-dependent color spaces are used
	}

	oiObj := doc.Resolve(oiRef)
	if oiObj == nil {
		return append(errsPageLevel, ValidationError{
			Rule:    colourClause("outputIntent", level),
			Level:   level,
			Message: "/OutputIntents reference target not found",
		})
	}

	arr, ok := oiObj.(object.Array)
	if !ok {
		return append(errsPageLevel, ValidationError{
			Rule:    colourClause("outputIntent", level),
			Level:   level,
			Message: "/OutputIntents must be an array",
		})
	}

	if len(arr) == 0 {
		return errsPageLevel // Empty OutputIntents array is OK; absence is also OK
	}

	errs := errsPageLevel

	for i, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] is not a dictionary", i),
			})
			continue
		}

		s := dict.Get("S")
		if s == nil {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must have /S", i),
			})
			continue
		}

		if _, ok := s.(object.Name); !ok {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /S must be a name", i),
			})
			continue
		}

		// /DestOutputProfileRef is not allowed in PDF/A
		if dict.Get("DestOutputProfileRef") != nil {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must not have /DestOutputProfileRef", i),
			})
		}

		profRef := dict.Get("DestOutputProfile")
		if profRef == nil {
			// /DestOutputProfile is required unless /OutputConditionIdentifier
			// identifies a standard registered condition
			oci := dict.Get("OutputConditionIdentifier")
			if oci == nil {
				errs = append(errs, ValidationError{
					Rule:    colourClause("outputIntent", level),
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] must have /DestOutputProfile or /OutputConditionIdentifier", i),
				})
			}
		}
	}

	// A PDF/A OutputIntent (GTS_PDFA1) is NOT mandatory: it is only needed
	// when device-dependent color is used, which checkDeviceColorSpaces
	// verifies. A file whose only intent is e.g. PDF/X remains conformant.

	// When the array has multiple entries, ALL entries carrying a
	// DestOutputProfile must reference the same object — the spec covers
	// every intent, not only the GTS_PDFA1 ones.
	if len(arr) > 1 {
		var profileRefs []object.Object
		for _, elem := range arr {
			dict := doc.ResolveDict(elem)
			if dict == nil {
				continue
			}
			if p := dict.Get("DestOutputProfile"); p != nil {
				profileRefs = append(profileRefs, p)
			}
		}
		for j := 1; j < len(profileRefs); j++ {
			ref0, ok0 := profileRefs[0].(object.IndirectRef)
			refJ, okJ := profileRefs[j].(object.IndirectRef)
			if ok0 && okJ {
				if ref0.Number != refJ.Number {
					errs = append(errs, ValidationError{
						Rule:    colourClause("outputIntent", level),
						Level:   level,
						Message: "all output intents with /DestOutputProfile must reference the same ICC profile",
					})
					break
				}
			}
		}
	}

	// GTS_PDFA1 output intents must have /DestOutputProfile
	for i, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			continue
		}
		sName, _ := resolveName(doc, dict.Get("S"))
		if sName == "GTS_PDFA1" && dict.Get("DestOutputProfile") == nil {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] with /S /GTS_PDFA1 must have /DestOutputProfile", i),
			})
		}
	}

	// errsPageLevel is already the seed of errs (above); the page-level errors are
	// not re-appended here or they would be reported twice (audit C23).
	return errs
}

func checkOutputIntentProfile(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return nil
	}

	oiObj := doc.Resolve(oiRef)
	arr, ok := oiObj.(object.Array)
	if !ok || len(arr) == 0 {
		return nil
	}

	var errs []ValidationError
	for i, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			continue
		}
		profRef := dict.Get("DestOutputProfile")
		if profRef == nil {
			continue
		}
		profObj := doc.Resolve(profRef)
		profStream, ok := profObj.(*object.Stream)
		if !ok {
			continue
		}
		// Validate ICC profile N matches the profile data
		nObj := profStream.Dict.Get("N")
		if nObj == nil {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /DestOutputProfile must have /N", i),
			})
			continue
		}
		nVal, ok := nObj.(object.Integer)
		if !ok {
			continue
		}
		// Decompress and check ICC profile header
		data, err := core.DecodeStreamData(doc.Cancel, profStream, doc.Limits)
		if err != nil {
			// Only treat a decode failure as a violation when we actually
			// support every filter on the stream. A legal profile encoded with
			// a filter we don't decode (e.g. ASCII85Decode, RunLengthDecode, or
			// a filter array) must not produce a false positive.
			if core.StreamFiltersSupported(profStream) {
				errs = append(errs, ValidationError{
					Rule:    colourClause("outputIntent", level),
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] /DestOutputProfile ICC data cannot be decoded: %v", i, err),
				})
			}
			continue
		}
		if len(data) < 128 {
			errs = append(errs, ValidationError{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /DestOutputProfile ICC data too short (%d bytes, minimum 128)", i, len(data)),
			})
			continue
		}
		// ICC profile header: bytes 16-19 contain color space signature
		if len(data) >= 20 {
			cs := string(data[16:20])
			var expectedN int
			switch cs {
			case "GRAY":
				expectedN = 1
			case "RGB ":
				expectedN = 3
			case "CMYK":
				expectedN = 4
			default:
				// Invalid or unsupported color space in output intent profile
				errs = append(errs, ValidationError{
					Rule:    colourClause("outputIntent", level),
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] ICC profile has unsupported color space %q", i, cs),
				})
			}
			if expectedN > 0 && int(nVal) != expectedN {
				errs = append(errs, ValidationError{
					Rule:    colourClause("outputIntent", level),
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] /N=%d does not match ICC profile color space %s (expected %d)", i, nVal, cs, expectedN),
				})
			}
		}
		// ICC profile header: bytes 12-15 contain device class
		if len(data) >= 16 {
			cls := string(data[12:16])
			// Output intent profiles must be of class "mntr" (monitor),
			// "prtr" (printer), or "spac" (color space conversion)
			switch cls {
			case "mntr", "prtr", "spac":
				// OK
			default:
				errs = append(errs, ValidationError{
					Rule:    colourClause("outputIntent", level),
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] ICC profile has invalid device class %q (must be mntr, prtr, or spac)", i, cls),
				})
			}
		}
		// Check ICC profile version (bytes 8-11)
		if len(data) >= 12 {
			major := data[8]
			minor := data[9] >> 4
			if level == PDFA1b {
				// PDF/A-1b: ICC profile version must be <= 2.x
				if major > 2 {
					errs = append(errs, ValidationError{
						Rule:    colourClause("outputIntent", level),
						Level:   level,
						Message: fmt.Sprintf("/OutputIntents[%d] ICC profile version %d.%d not allowed for PDF/A-1b (max 2.x)", i, major, minor),
					})
				}
			} else if level == PDFA2b || level == PDFA3b {
				// PDF/A-2b/3b: ICC profile version must be <= 4.x
				if major > 4 {
					errs = append(errs, ValidationError{
						Rule:    colourClause("outputIntent", level),
						Level:   level,
						Message: fmt.Sprintf("/OutputIntents[%d] ICC profile version %d.%d not allowed for PDF/A-2b/3b (max 4.x)", i, major, minor),
					})
				}
			}
		}
	}
	return errs
}

func checkNoCatalogAA(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 does not restrict /AA in catalog
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	if catalog.Get("AA") != nil {
		errs = append(errs, ValidationError{
			Rule:    annotActionClause("catalogAA", level),
			Level:   level,
			Message: "catalog must not contain /AA (additional actions)",
		})
	}
	// Page dictionaries are equally forbidden from carrying /AA at 1b/2b/3b
	// (ISO 19005-2, 6.6.2); previously only the catalog was checked.
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		if page.Dict.Get("AA") != nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("catalogAA", level),
				Level:   level,
				Message: "page dictionary must not contain /AA (additional actions)",
				Object:  page.ObjNum,
			})
		}
	}
	return errs
}

func checkNoOCProperties(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA1b {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	if catalog.Get("OCProperties") != nil {
		return []ValidationError{{
			Rule:    "6.1.13",
			Level:   level,
			Message: "catalog must not contain /OCProperties (optional content, PDF/A-1b)",
		}}
	}
	return nil
}

// Rule 6.1.12: Perms dictionary may only contain UR3 and DocMDP keys.
func checkPermsDict(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // PDF/A-1b doesn't have Perms rules
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	permsRef := catalog.Get("Perms")
	if permsRef == nil {
		return nil
	}
	permsDict := doc.ResolveDict(permsRef)
	if permsDict == nil {
		return nil
	}

	var errs []ValidationError
	for _, key := range permsDict.Keys {
		if key != "UR3" && key != "DocMDP" {
			errs = append(errs, ValidationError{
				Rule:    "6.1.12",
				Level:   level,
				Message: fmt.Sprintf("Perms dictionary contains forbidden key /%s (only /UR3 and /DocMDP allowed)", string(key)),
			})
		}
	}

	// The signature referenced by /DocMDP must not use the deprecated
	// DigestLocation/DigestMethod/DigestValue keys in its signature reference
	// dictionaries (ISO 19005-2, 6.1.12).
	if sigDict := doc.ResolveDict(permsDict.Get("DocMDP")); sigDict != nil {
		refArr, ok := doc.Resolve(sigDict.Get("Reference")).(object.Array)
		if !ok {
			if a, isArr := sigDict.Get("Reference").(object.Array); isArr {
				refArr = a
			}
		}
		for _, el := range refArr {
			refDict := doc.ResolveDict(el)
			if refDict == nil {
				if d, isDict := el.(*object.Dictionary); isDict {
					refDict = d
				} else {
					continue
				}
			}
			for _, forbidden := range []object.Name{"DigestLocation", "DigestMethod", "DigestValue"} {
				if refDict.Get(forbidden) != nil {
					errs = append(errs, ValidationError{
						Rule:    "6.1.12",
						Level:   level,
						Message: fmt.Sprintf("signature reference dictionary contains deprecated key /%s", string(forbidden)),
					})
				}
			}
		}
	}
	return errs
}

// --- object.Stream checks (6.1.6) ---

// filterClause returns the stream-filter rule's ISO clause for the level: only
// the standard filters (Table 6) are permitted, so LZWDecode and any
// non-standard name are rejected. ISO 19005-1 6.1.10; -2/-3 6.1.7.2; -4 6.1.6.2.
func filterClause(level PDFALevel) string {
	switch level {
	case PDFA1b:
		return "6.1.10"
	case PDFA4:
		return "6.1.6.2"
	default:
		return "6.1.7.2"
	}
}

// Rule: only the standard stream filters may be used; LZWDecode is prohibited.
func checkNoLZW(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if hasFilter(stream, "LZWDecode") {
			errs = append(errs, ValidationError{
				Rule:    filterClause(level),
				Level:   level,
				Message: "stream must not use /LZWDecode filter",
				Object:  num,
			})
		}
		// JPXDecode (JPEG 2000) is a PDF 1.5 filter and is not permitted in
		// PDF/A-1, which is based on PDF 1.4. It is a standard filter at 2b/3b/4,
		// so isStandardFilter accepts it there; forbid it explicitly at PDF/A-1
		// (audit C17).
		if level == PDFA1b && hasFilter(stream, "JPXDecode") {
			errs = append(errs, ValidationError{
				Rule:    filterClause(level),
				Level:   level,
				Message: "stream must not use /JPXDecode filter (not permitted in PDF/A-1)",
				Object:  num,
			})
		}
		// Check for non-standard filter names
		if badFilter := getNonStandardFilter(stream); badFilter != "" {
			errs = append(errs, ValidationError{
				Rule:    filterClause(level),
				Level:   level,
				Message: fmt.Sprintf("stream uses non-standard filter /%s", badFilter),
				Object:  num,
			})
		}
	}
	return errs
}

// checkSignatureByteRange enforces 6.4.3 (parts 2/3): a signature's digest must
// be computed over the entire file, so the /ByteRange of each signature must
// start at byte 0 and its two covered segments plus the excluded /Contents gap
// must span to the end of the file. Works from the raw bytes; only the single
// gap (the signature value) may be uncovered.
func checkSignatureByteRange(doc core.View, level PDFALevel, raw []byte) []ValidationError {
	if level != PDFA2b && level != PDFA3b {
		return nil
	}
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		// A signature dictionary carries both /ByteRange and /Contents; that
		// pairing is unique to signatures (and document timestamps).
		brObj := dict.Get("ByteRange")
		if brObj == nil || dict.Get("Contents") == nil {
			continue
		}
		if t, _ := dict.Get("Type").(object.Name); t != "" && t != "Sig" && t != "DocTimeStamp" {
			continue
		}

		bad := func(msg string) {
			errs = append(errs, ValidationError{Rule: "6.4.3", Level: level, Message: msg, Object: num})
		}
		br, ok := doc.Resolve(brObj).(object.Array)
		if !ok || len(br) != 4 {
			bad("signature /ByteRange must be an array of four integers")
			continue
		}
		var v [4]int64
		malformed := false
		for i, e := range br {
			n, ok := doc.Resolve(e).(object.Integer)
			if !ok {
				malformed = true
				break
			}
			v[i] = int64(n)
		}
		if malformed {
			bad("signature /ByteRange must be an array of four integers")
			continue
		}
		// v = [start1, len1, start2, len2]. The digest covers [start1,start1+len1)
		// and [start2,start2+len2); the hole between them is the /Contents value.
		start1, len1, start2, len2 := v[0], v[1], v[2], v[3]
		if start1 != 0 || len1 < 0 || len2 < 0 || start2 < start1+len1 {
			bad("signature /ByteRange does not cover the document from its start")
			continue
		}
		// The signed range must reach the end of the file: if it stops short,
		// the trailing bytes are unsigned and the digest does not cover the whole
		// document. A range that meets or exceeds the file length covers it — the
		// veraPDF corpus carries stub signatures whose /ByteRange overshoots the
		// truncated test file, and those are treated as covering (not a defect).
		if start2+len2 < int64(len(raw)) {
			bad("signature /ByteRange does not cover the entire document")
		}

		// The PKCS#7/CMS signature blob in /Contents must embed the signing
		// certificate and hold exactly one SignerInfo. Only applies when the blob
		// parses as CMS SignedData — an adbe.x509.rsa_sha1 signature stores a raw
		// value and its certificate in /Cert instead.
		if c, ok := doc.Resolve(dict.Get("Contents")).(object.String); ok {
			if info := core.ParseCMSSignedData(c.Value); info.Parsed {
				if !info.HasCertificate {
					bad("signature PKCS#7 data must contain the signing certificate")
				}
				if info.SignerInfoCount != 1 {
					bad(fmt.Sprintf("signature PKCS#7 data must contain exactly one SignerInfo, found %d", info.SignerInfoCount))
				}
			}
		}
	}
	return errs
}

func isStandardFilter(name object.Name) bool {
	switch name {
	case "ASCIIHexDecode", "ASCII85Decode", "LZWDecode", "FlateDecode",
		"RunLengthDecode", "CCITTFaxDecode", "JBIG2Decode", "DCTDecode",
		"JPXDecode", "Crypt":
		return true
	}
	return false
}

func getNonStandardFilter(stream *object.Stream) string {
	f := stream.Dict.Get("Filter")
	if f == nil {
		return ""
	}
	if name, ok := f.(object.Name); ok {
		if !isStandardFilter(name) {
			return string(name)
		}
	}
	if arr, ok := f.(object.Array); ok {
		for _, elem := range arr {
			if name, ok := elem.(object.Name); ok && !isStandardFilter(name) {
				return string(name)
			}
		}
	}
	return ""
}

func hasFilter(stream *object.Stream, filterName string) bool {
	f := stream.Dict.Get("Filter")
	if f == nil {
		return false
	}
	if name, ok := f.(object.Name); ok {
		return string(name) == filterName
	}
	if arr, ok := f.(object.Array); ok {
		for _, elem := range arr {
			if name, ok := elem.(object.Name); ok && string(name) == filterName {
				return true
			}
		}
	}
	return false
}

// Rule 6.1.6.1-2: object.Stream dict cannot contain F, FFilter, or FDecodeParms.
func checkNoExternalStreams(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		for _, key := range []object.Name{"F", "FFilter", "FDecodeParms"} {
			if stream.Dict.Get(key) != nil {
				errs = append(errs, ValidationError{
					Rule:    "6.1.6",
					Level:   level,
					Message: fmt.Sprintf("stream must not have /%s (external stream reference)", string(key)),
					Object:  num,
				})
			}
		}
	}
	return errs
}

// --- Font checks (6.2.10) ---

// Rule 6.2.10.4.1-1: Font programs must be embedded.
func checkFontsEmbedded(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return nil
	}

	fonts := collectFonts(doc, pagesRef)

	// A font used only for invisible text (rendering mode 3/7) is not
	// "used for rendering" and need not be embedded (the corpus passes an
	// unembedded Type1 shown in mode 3).
	usage := core.CollectFontTextUsage(doc)
	exemptInvisible := make(map[*object.Dictionary]bool)
	for d, u := range usage {
		if !rendersVisibly(u) {
			exemptInvisible[d] = true
		}
	}

	// Fonts reached only through form XObjects, tiling patterns, or Type3
	// glyph procedures never appear in the page-tree /Resources that
	// collectFonts walks, so they would escape the embedding rule. Include the
	// executed-content fonts too (audit C21), deduped by dictionary pointer.
	checked := make(map[*object.Dictionary]bool, len(fonts))
	for objNum, fontDict := range fonts {
		checked[fontDict] = true
		if exemptInvisible[fontDict] {
			continue
		}
		errs = append(errs, checkOneFontEmbedded(doc, fontDict, objNum, level)...)
	}
	for fontDict := range usage {
		if checked[fontDict] || exemptInvisible[fontDict] {
			continue
		}
		errs = append(errs, checkOneFontEmbedded(doc, fontDict, fontObjNum(doc, fontDict), level)...)
	}

	return errs
}

// objNumForDict returns the object number under which dict is stored, or 0 if
// it is a direct dictionary with no indirect identity.
// objNumForDict returns the object number whose value is dict, or 0 when dict has
// no indirect identity. It delegates to the cached (*Document).dictObjNum so that
// the many per-font / per-halftone lookups in a validation run share one reverse
// index instead of each rescanning the whole object table — which was quadratic
// on a document with hundreds of thousands of objects (audit C34). The 0-on-miss
// convention here matches the "unknown object" sentinel used in
// ValidationError.Object; dictObjNum itself reports -1 on miss.
func objNumForDict(doc core.View, dict *object.Dictionary) int {
	if n := doc.DictObjNum(dict); n >= 0 {
		return n
	}
	return 0
}

// fontObjNum returns the object number of a font dictionary, or 0 if it is a
// direct dictionary with no indirect identity.
func fontObjNum(doc core.View, fontDict *object.Dictionary) int {
	return objNumForDict(doc, fontDict)
}

// checkOneFontEmbedded applies the 6.2.10 embedding rule to a single font
// dictionary.
func checkOneFontEmbedded(doc core.View, fontDict *object.Dictionary, objNum int, level PDFALevel) []ValidationError {
	subtypeName, _ := fontDict.Get("Subtype").(object.Name)

	// Type3 fonts define their glyphs with content streams, so they carry no
	// font program to embed. Type0 (composite) fonts DO require embedding —
	// via their descendant CIDFont's FontDescriptor, handled below.
	if subtypeName == "Type3" {
		return nil
	}

	fdRef := fontDict.Get("FontDescriptor")
	if fdRef == nil {
		// Composite fonts (Type0): check the descendant CIDFont's descriptor
		if dfArr, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(object.Array); ok && len(dfArr) > 0 {
			if cidFont := doc.ResolveDict(dfArr[0]); cidFont != nil {
				fdRef = cidFont.Get("FontDescriptor")
			}
		}
	}

	if fdRef == nil {
		return []ValidationError{{
			Rule:    fontClause("embed", level),
			Level:   level,
			Message: "font must have a /FontDescriptor",
			Object:  objNum,
		}}
	}

	fd := doc.ResolveDict(fdRef)
	if fd == nil {
		return []ValidationError{{
			Rule:    fontClause("embed", level),
			Level:   level,
			Message: "/FontDescriptor reference not found",
			Object:  objNum,
		}}
	}

	// The FontFile entry must resolve to an actual stream: the corpus
	// fails a descriptor whose FontFile3 references a missing object.
	for _, key := range []object.Name{"FontFile", "FontFile2", "FontFile3"} {
		if _, ok := doc.Resolve(fd.Get(key)).(*object.Stream); ok {
			return nil
		}
	}
	baseFontName := ""
	if bn, ok := fontDict.Get("BaseFont").(object.Name); ok {
		baseFontName = string(bn)
	}
	return []ValidationError{{
		Rule:    fontClause("embed", level),
		Level:   level,
		Message: fmt.Sprintf("font %s must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)", baseFontName),
		Object:  objNum,
	}}
}

func collectFonts(doc core.View, pageTreeRef object.Object) map[int]*object.Dictionary {
	fonts := make(map[int]*object.Dictionary)
	collectFontsRecursive(doc, pageTreeRef, fonts, make(map[int]bool))
	return fonts
}

func collectFontsRecursive(doc core.View, ref object.Object, fonts map[int]*object.Dictionary, seen map[int]bool) {
	if r, ok := ref.(object.IndirectRef); ok {
		if seen[r.Number] {
			return // cycle in the page tree
		}
		seen[r.Number] = true
	}
	node := doc.ResolveDict(ref)
	if node == nil {
		return
	}

	nodeType, _ := node.Get("Type").(object.Name)

	if nodeType == "Pages" {
		kidsObj := doc.Resolve(node.Get("Kids"))
		if kids, ok := kidsObj.(object.Array); ok {
			for _, kid := range kids {
				collectFontsRecursive(doc, kid, fonts, seen)
			}
		}
		collectFontsFromResources(doc, node, fonts)
	} else if nodeType == "Page" {
		collectFontsFromResources(doc, node, fonts)
	}
}

func collectFontsFromResources(doc core.View, pageOrPages *object.Dictionary, fonts map[int]*object.Dictionary) {
	resRef := pageOrPages.Get("Resources")
	if resRef == nil {
		return
	}
	res := doc.ResolveDict(resRef)
	if res == nil {
		return
	}

	fontDictRef := res.Get("Font")
	if fontDictRef == nil {
		return
	}
	fontDict := doc.ResolveDict(fontDictRef)
	if fontDict == nil {
		return
	}

	for _, fontRef := range fontDict.Values {
		objNum := 0
		if iref, ok := fontRef.(object.IndirectRef); ok {
			objNum = iref.Number
		}

		fd := doc.ResolveDict(fontRef)
		if fd == nil {
			continue
		}
		if objNum == 0 {
			objNum = -len(fonts) - 1
		}
		fonts[objNum] = fd
	}
}

// --- Annotation checks (6.3) ---

// Allowed annotation subtypes per PDF/A level.
// Rule 6.3.1-1.
var allowedAnnotSubtypes = map[PDFALevel]map[object.Name]bool{
	PDFA4: {
		"Text": true, "Link": true, "FreeText": true, "Line": true,
		"Square": true, "Circle": true, "Polygon": true, "PolyLine": true,
		"Highlight": true, "Underline": true, "Squiggly": true, "StrikeOut": true,
		"Stamp": true, "Caret": true, "Ink": true, "Popup": true,
		"Widget": true, "PrinterMark": true, "TrapNet": true,
		"Watermark": true, "Redact": true, "Projection": true,
		"FileAttachment": true,
	},
	// PDF/A-1b, 2b, 3b: same set minus Polygon, PolyLine, Projection, Redact; plus some others
	// For now, 1b/2b/3b get the same restrictive list as 4 with adjustments
}

func init() {
	// PDF/A-2b/3b allowed subtypes (per ISO 19005-2/3 clause 6.5.1)
	pdfa2bAnnots := map[object.Name]bool{
		"Text": true, "Link": true, "FreeText": true, "Line": true,
		"Square": true, "Circle": true, "Polygon": true, "PolyLine": true,
		"Highlight": true, "Underline": true, "Squiggly": true, "StrikeOut": true,
		"Stamp": true, "Caret": true, "Ink": true, "Popup": true,
		"Widget": true, "PrinterMark": true, "TrapNet": true, "Watermark": true,
		"Redact": true, "FileAttachment": true,
	}
	allowedAnnotSubtypes[PDFA2b] = pdfa2bAnnots
	allowedAnnotSubtypes[PDFA3b] = pdfa2bAnnots

	// PDF/A-1b allowed subtypes (per ISO 19005-1 clause 6.5.1)
	allowedAnnotSubtypes[PDFA1b] = map[object.Name]bool{
		"Text": true, "Link": true, "FreeText": true, "Line": true,
		"Square": true, "Circle": true, "Highlight": true, "Underline": true,
		"Squiggly": true, "StrikeOut": true, "Stamp": true, "Ink": true,
		"Popup": true, "Widget": true, "PrinterMark": true, "TrapNet": true,
	}
}

// annotOccurrence is one annotation dictionary paired with the object number
// used for error attribution: the annotation's own number, or the owning
// page's number when the annotation is a direct dictionary inside /Annots.
type annotOccurrence struct {
	dict *object.Dictionary
	num  int
}

// collectDirectAnnotations returns annotations written as direct dictionaries
// inside page /Annots arrays. These are not top-level objects, so the flat
// doc.Objects scans the annotation checks start from can never see them
// (audit A9); every annotation check runs over this list as well.
func collectDirectAnnotations(doc core.View) []annotOccurrence {
	if c := pdfaMemo(doc); true && c.hasDirectAnnots {
		return c.directAnnots
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var out []annotOccurrence
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		annots, ok := doc.Resolve(page.Dict.Get("Annots")).(object.Array)
		if !ok {
			continue
		}
		for _, el := range annots {
			if dict, ok := el.(*object.Dictionary); ok {
				out = append(out, annotOccurrence{dict: dict, num: page.ObjNum})
			}
		}
	}
	if c := pdfaMemo(doc); true {
		c.directAnnots = out
		c.hasDirectAnnots = true
	}
	return out
}

// resolveName resolves obj (following an indirect reference) and returns it as
// a object.Name. Rules must resolve before type-asserting: a value placed behind an
// indirect reference — e.g. /Subtype 12 0 R — would otherwise silently evade
// the check (audit C12).
func resolveName(doc core.View, obj object.Object) (object.Name, bool) {
	n, ok := doc.Resolve(obj).(object.Name)
	return n, ok
}

func checkAnnotationSubtypes(doc core.View, level PDFALevel) []ValidationError {
	allowed, ok := allowedAnnotSubtypes[level]
	if !ok {
		return nil
	}
	// PDF/A-4e permits 3D and RichMedia annotations (they carry the embedded
	// 3D/multimedia content that "e" stands for); plain PDF/A-4 forbids them.
	extra := map[object.Name]bool{}
	if level == PDFA4 && pdfaConformanceFlag(doc) == "E" {
		extra["3D"] = true
		extra["RichMedia"] = true
	}

	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		st, ok := resolveName(doc, dict.Get("Subtype"))
		if !ok {
			return
		}
		if !allowed[st] && !extra[st] {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("subtype", level),
				Level:   level,
				Message: fmt.Sprintf("annotation subtype /%s is not allowed in %s", string(st), level),
				Object:  num,
			})
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok && core.IsAnnotation(dict) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// annotOpacity returns an annotation /CA value as a float, if it is numeric.
func annotOpacity(v object.Object) (float64, bool) {
	switch n := v.(type) {
	case object.Integer:
		return float64(n), true
	case object.Real:
		return float64(n), true
	}
	return 0, false
}

// Rule 6.3.2-1/2: Non-Popup annotations require F key; flags must have Print set,
// Hidden/Invisible/ToggleNoView/NoView clear.
func checkAnnotationFlags(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		// 6.5.3: at PDF/A-1, an annotation's /CA (constant opacity) must be 1.0
		// — annotation transparency is not permitted. This applies to every
		// annotation subtype, so it precedes the Popup exemption below.
		if level == PDFA1b {
			if ca, ok := annotOpacity(doc.Resolve(dict.Get("CA"))); ok && math.Abs(ca-1.0) > 1e-6 {
				errs = append(errs, ValidationError{
					Rule:    "6.5.3",
					Level:   level,
					Message: "annotation /CA (opacity) must be 1.0",
					Object:  num,
				})
			}
		}

		// Popup annotations are exempt from F requirement
		st, _ := dict.Get("Subtype").(object.Name)
		if st == "Popup" {
			return
		}

		fObj := dict.Get("F")
		if fObj == nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation must have /F (flags)",
				Object:  num,
			})
			return
		}
		flags, ok := doc.Resolve(fObj).(object.Integer)
		if !ok {
			return
		}

		const (
			flagInvisible    = 1 << 0
			flagHidden       = 1 << 1
			flagPrint        = 1 << 2
			flagNoView       = 1 << 5
			flagToggleNoView = 1 << 8
		)

		if int(flags)&flagPrint == 0 {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must have Print bit set",
				Object:  num,
			})
		}
		if int(flags)&flagHidden != 0 {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have Hidden bit set",
				Object:  num,
			})
		}
		if int(flags)&flagInvisible != 0 {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have Invisible bit set",
				Object:  num,
			})
		}
		if int(flags)&flagNoView != 0 {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have NoView bit set",
				Object:  num,
			})
		}
		if int(flags)&flagToggleNoView != 0 {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have ToggleNoView bit set",
				Object:  num,
			})
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok && core.IsAnnotation(dict) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.3.3-1: Annotations need AP except Popup, Link, Projection, and zero-area rects.
func checkAnnotationAppearance(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		st, _ := dict.Get("Subtype").(object.Name)

		// Exempt subtypes
		if st == "Popup" || st == "Link" || st == "Projection" {
			return
		}

		// Exempt zero-area rectangles
		if isZeroAreaRect(dict.Get("Rect")) {
			return
		}

		ap := dict.Get("AP")
		if ap == nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("appearance", level),
				Level:   level,
				Message: "annotation must have /AP (appearance dictionary)",
				Object:  num,
			})
			return
		}

		apDict := doc.ResolveDict(ap)
		if apDict == nil {
			return
		}

		if apDict.Get("N") == nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("appearance", level),
				Level:   level,
				Message: "annotation /AP must have /N (normal appearance)",
				Object:  num,
			})
		}

		// The appearance dictionary shall contain only the N entry (ISO
		// 19005-2 6.3.3, -4 6.3.4): the down (D) and rollover (R) appearances
		// are not permitted.
		if apDict.Get("D") != nil || apDict.Get("R") != nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("appearance", level),
				Level:   level,
				Message: "annotation appearance dictionary must contain only the /N entry (not /D or /R)",
				Object:  num,
			})
		}

		// For a Widget of button field type (FT Btn), the N appearance shall
		// be a sub-dictionary of appearance states, not a single stream.
		if st == "Widget" && annotFieldType(doc, dict) == "Btn" {
			if _, ok := doc.Resolve(apDict.Get("N")).(*object.Dictionary); !ok {
				errs = append(errs, ValidationError{
					Rule:    annotActionClause("appearance", level),
					Level:   level,
					Message: "button Widget /AP /N must be an appearance sub-dictionary of states, not a stream",
					Object:  num,
				})
			}
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok && core.IsAnnotation(dict) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// annotFieldType returns the form field type (FT) governing a widget
// annotation: its own FT, or an inherited one from its /Parent field chain.
func annotFieldType(doc core.View, dict *object.Dictionary) object.Name {
	node := dict
	for hops := 0; node != nil && hops < 32; hops++ {
		if ft, ok := node.Get("FT").(object.Name); ok {
			return ft
		}
		node = doc.ResolveDict(node.Get("Parent"))
	}
	return ""
}

func isZeroAreaRect(obj object.Object) bool {
	arr, ok := obj.(object.Array)
	if !ok || len(arr) != 4 {
		return false
	}
	vals := make([]float64, 4)
	for i, elem := range arr {
		switch v := elem.(type) {
		case object.Integer:
			vals[i] = float64(v)
		case object.Real:
			vals[i] = float64(v)
		default:
			return false
		}
	}
	// Zero area if width or height is zero
	return (vals[2]-vals[0]) == 0 || (vals[3]-vals[1]) == 0
}

// --- Interactive forms (6.4) ---

// Rule 6.4.1-1: Widget annotation cannot contain A key.
func checkWidgetNoAction(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		st, _ := dict.Get("Subtype").(object.Name)
		if st != "Widget" {
			return
		}
		if dict.Get("A") != nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("widget", level),
				Level:   level,
				Message: "Widget annotation must not contain /A key",
				Object:  num,
			})
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.4.2-1: AcroForm dictionary cannot contain XFA key.
func checkNoXFA(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	afRef := catalog.Get("AcroForm")
	if afRef == nil {
		return nil
	}
	af := doc.ResolveDict(afRef)
	if af == nil {
		return nil
	}
	if af.Get("XFA") != nil {
		return []ValidationError{{
			Rule:    "6.4.2",
			Level:   level,
			Message: "AcroForm must not contain /XFA",
		}}
	}
	return nil
}

// Rule 6.4.1-2: NeedAppearances flag must be absent or false.
func checkNeedAppearances(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	afRef := catalog.Get("AcroForm")
	if afRef == nil {
		return nil
	}
	af := doc.ResolveDict(afRef)
	if af == nil {
		return nil
	}
	na := af.Get("NeedAppearances")
	if na == nil {
		return nil
	}
	if b, ok := na.(object.Boolean); ok && bool(b) {
		return []ValidationError{{
			Rule:    "6.4.1",
			Level:   level,
			Message: "NeedAppearances must be false",
		}}
	}
	return nil
}

// --- Action checks (6.6) ---

// Forbidden action types by level per ISO 19005.
// Rule 6.6.1-1.
func isForbiddenAction(s object.Name, level PDFALevel, conformance string) bool {
	// Universally forbidden across all PDF/A levels:
	universallyForbidden := map[object.Name]bool{
		"Launch":     true,
		"Sound":      true,
		"Movie":      true,
		"ResetForm":  true,
		"ImportData": true,
		"Hide":       true,
		"Rendition":  true,
		"Trans":      true,
	}
	if universallyForbidden[s] {
		return true
	}

	switch level {
	case PDFA1b, PDFA2b, PDFA3b:
		// Additionally forbidden in parts 1-3:
		forbidden123 := map[object.Name]bool{
			"JavaScript":  true,
			"SetOCGState": true,
			"GoTo3DView":  true,
			"GoToDp":      true,
			"SetState":    true,
			"NOP":         true,
		}
		return forbidden123[s]
	case PDFA4:
		// PDF/A-4e permits the 3D/multimedia navigation actions SetOCGState and
		// GoTo3DView; plain PDF/A-4 forbids them. SetState/NOP (deprecated) stay
		// forbidden at every part-4 conformance.
		if conformance == "E" {
			return s == "SetState" || s == "NOP"
		}
		forbidden4 := map[object.Name]bool{
			"SetOCGState": true,
			"GoTo3DView":  true,
			"SetState":    true,
			"NOP":         true,
		}
		return forbidden4[s]
	}
	return false
}

func checkNoForbiddenActions(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError

	// PDF/A-4e relaxes a couple of 3D/multimedia actions; the conformance flag
	// selects that behaviour. Computed once (it decodes the XMP packet).
	conformance := ""
	if level == PDFA4 {
		conformance = pdfaConformanceFlag(doc)
	}

	// Check catalog /OpenAction
	catalog := doc.Catalog()
	if catalog != nil {
		oaRef := catalog.Get("OpenAction")
		if oaRef != nil {
			errs = append(errs, checkActionObject(doc, oaRef, 0, level, conformance)...)
		}
	}

	// Check all objects for /A and action dictionaries
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}

		// Check /A (action) in any dictionary
		if aRef := dict.Get("A"); aRef != nil {
			errs = append(errs, checkActionObject(doc, aRef, num, level, conformance)...)
		}

		// Check if the object itself is an action dict (has /S and /Type=Action or no /Type)
		if s, ok := dict.Get("S").(object.Name); ok {
			typeObj := dict.Get("Type")
			isAction := typeObj == nil || typeObj == object.Name("Action")
			if isAction && isForbiddenAction(s, level, conformance) {
				errs = append(errs, ValidationError{
					Rule:    annotActionClause("forbidden", level),
					Level:   level,
					Message: fmt.Sprintf("forbidden action type /%s", string(s)),
					Object:  num,
				})
			}
		}
	}

	// Annotations written as direct dictionaries inside /Annots are invisible
	// to the object scan above. Check their direct /A actions explicitly (an
	// indirect /A resolves to a top-level object the scan already covers).
	for _, a := range collectDirectAnnotations(doc) {
		if actionDict, ok := a.dict.Get("A").(*object.Dictionary); ok {
			errs = append(errs, checkActionObject(doc, actionDict, a.num, level, conformance)...)
		}
	}

	return errs
}

func checkActionObject(doc core.View, ref object.Object, objNum int, level PDFALevel, conformance string) []ValidationError {
	var errs []ValidationError
	checkActionChain(doc, ref, objNum, level, conformance, &errs, make(map[*object.Dictionary]bool))
	return errs
}

// checkActionChain validates one action dictionary and follows its /Next
// entry (a single action or an array of actions), which previous versions
// ignored entirely — a legal action whose /Next launches JavaScript passed.
func checkActionChain(doc core.View, ref object.Object, objNum int, level PDFALevel, conformance string, errs *[]ValidationError, seen map[*object.Dictionary]bool) {
	// ref might be an action dict or an array (for OpenAction destination)
	actionDict := doc.ResolveDict(ref)
	if actionDict == nil || seen[actionDict] {
		return // destination array, unresolvable, or a /Next cycle
	}
	seen[actionDict] = true

	if s, ok := actionDict.Get("S").(object.Name); ok && isForbiddenAction(s, level, conformance) {
		*errs = append(*errs, ValidationError{
			Rule:    annotActionClause("forbidden", level),
			Level:   level,
			Message: fmt.Sprintf("forbidden action type /%s", string(s)),
			Object:  objNum,
		})
	}

	switch next := doc.Resolve(actionDict.Get("Next")).(type) {
	case *object.Dictionary:
		checkActionChain(doc, next, objNum, level, conformance, errs, seen)
	case object.Array:
		for _, el := range next {
			checkActionChain(doc, el, objNum, level, conformance, errs, seen)
		}
	}
}

// Rule 6.6.1-2: Named actions limited to NextPage, PrevPage, FirstPage, LastPage.
func checkNamedActions(doc core.View, level PDFALevel) []ValidationError {
	allowedNames := map[string]bool{
		"NextPage":  true,
		"PrevPage":  true,
		"FirstPage": true,
		"LastPage":  true,
	}

	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		s, _ := resolveName(doc, dict.Get("S"))
		if s != "Named" {
			return
		}
		nName, ok := resolveName(doc, dict.Get("N"))
		if !ok {
			return
		}
		if !allowedNames[string(nName)] {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("forbidden", level),
				Level:   level,
				Message: fmt.Sprintf("named action /%s not allowed (only NextPage, PrevPage, FirstPage, LastPage)", string(nName)),
				Object:  num,
			})
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok {
			check(dict, num)
		}
	}
	// Direct annotations may carry direct action dictionaries that never
	// appear as top-level objects (an indirect /A is already covered above).
	for _, a := range collectDirectAnnotations(doc) {
		if actionDict, ok := a.dict.Get("A").(*object.Dictionary); ok {
			check(actionDict, a.num)
		}
	}
	return errs
}

// Rule 6.6.3-1: Widget/FormField AA is level-gated.
// For PDF/A-1b/2b/3b: no /AA on widgets or form fields.
// For PDF/A-4: AA allowed on widgets/form fields (trigger events).
// Non-widget AA (doc/page/annot) keys restricted to: E, X, D, U, Fo, Bl.
func checkAnnotationAA(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 gates trigger events per-event; see checkA4TriggerEvents
	}

	// ISO 19005-1 6.5.3 / 19005-2 6.3.3: an annotation dictionary shall not
	// contain the AA key — for ANY annotation, not only widgets/form fields.
	var errs []ValidationError
	check := func(dict *object.Dictionary, num int) {
		if dict.Get("AA") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.6.3",
				Level:   level,
				Message: "annotation must not have /AA (additional-actions)",
				Object:  num,
			})
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok && (core.IsAnnotation(dict) || isWidgetOrField(dict)) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// isWidgetOrField reports whether dict is a widget annotation or an interactive
// form field, which the /AA prohibition also covers and which need not carry
// the /Rect that core.IsAnnotation looks for.
func isWidgetOrField(dict *object.Dictionary) bool {
	if st, ok := dict.Get("Subtype").(object.Name); ok && st == "Widget" {
		return true
	}
	return dict.Get("FT") != nil
}

// --- Metadata checks (6.7) ---

// Rule 6.7.3: Version identification via XMP pdfaid:part, pdfaid:rev, pdfaid:conformance.
// metadataClause returns the ISO clause for a metadata-rule concept at the
// given level. Metadata requirements are numbered differently per part (ISO
// 19005-1 6.7.x; -2/-3 6.6.x; -4 6.7.x); clauses follow the veraPDF profiles.
func metadataClause(concept string, level PDFALevel) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"version":       {"6.7.11", "6.6.4", "6.7.3"},
		"xmpProperties": {"6.7.2", "6.6.2.3.1", "6.7.2"},
		"extSchema":     {"6.7.8", "6.6.2.3.3", "6.7.8"},
	}
	c, ok := m[concept]
	if !ok {
		return "6.7"
	}
	switch level {
	case PDFA1b:
		return c[0]
	case PDFA4:
		return c[2]
	default:
		return c[1]
	}
}

func checkMetadataVersion(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	metaRef := catalog.Get("Metadata")
	if metaRef == nil {
		return nil // already reported by checkMetadataStream
	}

	metaObj := doc.Resolve(metaRef)
	if metaObj == nil {
		return nil
	}

	stream, ok := metaObj.(*object.Stream)
	if !ok {
		return nil
	}

	xmp := doc.XMPText(stream)
	var errs []ValidationError

	// Check pdfaid namespace URI. XML allows either quote style around the value,
	// so accept both — matching only double quotes falsely flagged a legal
	// single-quoted declaration (audit C33).
	if strings.Contains(xmp, "pdfaid:") {
		const ns = "http://www.aiim.org/pdfa/ns/id/"
		if !strings.Contains(xmp, `xmlns:pdfaid="`+ns+`"`) && !strings.Contains(xmp, `xmlns:pdfaid='`+ns+`'`) {
			errs = append(errs, ValidationError{
				Rule:    metadataClause("version", level),
				Level:   level,
				Message: "pdfaid namespace must be http://www.aiim.org/pdfa/ns/id/",
			})
			return errs
		}
	}

	// Check pdfaid:part
	expectedPart := ""
	switch level {
	case PDFA1b:
		expectedPart = "1"
	case PDFA2b:
		expectedPart = "2"
	case PDFA3b:
		expectedPart = "3"
	case PDFA4:
		expectedPart = "4"
	}

	part := core.ExtractXMPValue(xmp, "pdfaid:part")
	if part == "" {
		errs = append(errs, ValidationError{
			Rule:    metadataClause("version", level),
			Level:   level,
			Message: "metadata must contain pdfaid:part",
		})
	} else if part != expectedPart {
		errs = append(errs, ValidationError{
			Rule:    metadataClause("version", level),
			Level:   level,
			Message: fmt.Sprintf("pdfaid:part must be %s, got %s", expectedPart, part),
		})
	}

	// Check pdfaid:conformance
	switch level {
	case PDFA1b, PDFA2b, PDFA3b:
		conf := core.ExtractXMPValue(xmp, "pdfaid:conformance")
		if conf != "B" {
			errs = append(errs, ValidationError{
				Rule:    metadataClause("version", level),
				Level:   level,
				Message: fmt.Sprintf("pdfaid:conformance must be B, got %q", conf),
			})
		}
	case PDFA4:
		// PDF/A-4: conformance is absent for plain A-4, but "F" (A-4f) and "E"
		// (A-4e) are valid — a compliant 4f/4e file (e.g. an embedded one) must
		// not be rejected for carrying it (audit C23).
		if xmpHasKey(xmp, "pdfaid:conformance") {
			conf := core.ExtractXMPValue(xmp, "pdfaid:conformance")
			if conf != "F" && conf != "E" {
				errs = append(errs, ValidationError{
					Rule:    metadataClause("version", level),
					Level:   level,
					Message: fmt.Sprintf("PDF/A-4 pdfaid:conformance must be absent, F, or E, got %q", conf),
				})
			}
		}

		// Check pdfaid:rev must be "2020" for PDF/A-4
		rev := core.ExtractXMPValue(xmp, "pdfaid:rev")
		if rev == "" {
			errs = append(errs, ValidationError{
				Rule:    metadataClause("version", level),
				Level:   level,
				Message: "PDF/A-4 metadata must contain pdfaid:rev",
			})
		} else if rev != "2020" {
			errs = append(errs, ValidationError{
				Rule:    metadataClause("version", level),
				Level:   level,
				Message: fmt.Sprintf("pdfaid:rev must be 2020, got %q", rev),
			})
		}
	}

	return errs
}

// xmpHasKey returns true if the key is present in the XMP data at all,
// even if its value is empty. This distinguishes "not present" from "present but empty".
func xmpHasKey(xmp, key string) bool {
	// Check element form: <key>...</key> or <key/>
	if strings.Contains(xmp, "<"+key+">") || strings.Contains(xmp, "<"+key+"/>") {
		return true
	}
	// Check attribute form: key="..." or key='...' (both legal XML).
	if strings.Contains(xmp, key+"=\"") || strings.Contains(xmp, key+"='") {
		return true
	}
	return false
}

// --- Transparency checks (PDFA-1b only) ---

func checkNoTransparency(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA1b {
		return nil
	}

	var errs []ValidationError

	// Check for page-level transparency Groups (forbidden in PDF/A-1b)
	catalog := doc.Catalog()
	if catalog != nil {
		pages := doc.Pages(catalog.Get("Pages"))
		for _, page := range pages {
			groupRef := page.Dict.Get("Group")
			if groupRef == nil {
				continue
			}
			groupDict := doc.ResolveDict(groupRef)
			if groupDict == nil {
				continue
			}
			s, _ := groupDict.Get("S").(object.Name)
			if s == "Transparency" {
				errs = append(errs, ValidationError{
					Rule:    "6.4",
					Level:   level,
					Message: "page must not have /Group with /S /Transparency (PDF/A-1b forbids transparency)",
					Object:  page.ObjNum,
				})
			}
		}
	}

	// Image soft masks and form transparency groups are equally forbidden
	// (ISO 19005-1 6.4). The ExtGState scan below only sees the /SMask
	// graphics-state parameter, so walk page resources for the XObject-level
	// signals too.
	if catalog != nil {
		seen := map[*object.Dictionary]bool{}
		for _, page := range doc.Pages(catalog.Get("Pages")) {
			find1bTransparencyXObjects(doc, page.Dict, level, seen, &errs)
		}
	}

	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		gs := entry.dict
		objNum := entry.objNum

		smask := gs.Get("SMask")
		if smask != nil {
			if n, ok := smask.(object.Name); ok && n == "None" {
				// acceptable
			} else {
				errs = append(errs, ValidationError{
					Rule:    "6.4",
					Level:   level,
					Message: "/SMask must not be used (PDF/A-1b)",
					Object:  objNum,
				})
			}
		}

		bm := gs.Get("BM")
		if bm != nil {
			if n, ok := bm.(object.Name); ok {
				if n != "Normal" && n != "Compatible" {
					errs = append(errs, ValidationError{
						Rule:    "6.4",
						Level:   level,
						Message: fmt.Sprintf("/BM must be /Normal or /Compatible, got /%s", string(n)),
						Object:  objNum,
					})
				}
			}
		}

		for _, key := range []object.Name{"CA", "ca"} {
			v := gs.Get(key)
			if v != nil {
				val := 1.0
				switch tv := v.(type) {
				case object.Real:
					val = float64(tv)
				case object.Integer:
					val = float64(tv)
				}
				if math.Abs(val-1.0) > 1e-6 {
					errs = append(errs, ValidationError{
						Rule:    "6.4",
						Level:   level,
						Message: fmt.Sprintf("/%s must be 1.0 (PDF/A-1b)", string(key)),
						Object:  objNum,
					})
				}
			}
		}
	}
	return errs
}

// extGStateEntry holds a resolved ExtGState dictionary and its source object number.
type extGStateEntry struct {
	dict   *object.Dictionary
	objNum int
}

// collectAllExtGState finds all ExtGState dictionaries by scanning Resources/ExtGState
// in all pages, Form XObjects, and Type3 fonts. This avoids relying on the optional
// /Type key which many ExtGState objects don't have.
func collectAllExtGState(doc core.View) []extGStateEntry {
	seen := make(map[*object.Dictionary]bool)
	var entries []extGStateEntry

	addFromResources := func(res *object.Dictionary, fallbackObjNum int) {
		gsRef := res.Get("ExtGState")
		if gsRef == nil {
			return
		}
		gsDict := doc.ResolveDict(gsRef)
		if gsDict == nil {
			return
		}
		for _, val := range gsDict.Values {
			objNum := fallbackObjNum
			if iref, ok := val.(object.IndirectRef); ok {
				objNum = iref.Number
			}
			gs := doc.ResolveDict(val)
			if gs == nil {
				continue
			}
			if seen[gs] {
				continue
			}
			seen[gs] = true
			entries = append(entries, extGStateEntry{dict: gs, objNum: objNum})
		}
	}

	// Scan all objects for Resources dicts (pages, Form XObjects, Type3 fonts).
	//
	// In ascending object-number order, not doc.Objects map order. A graphics
	// state written as a DIRECT dictionary takes its object number from the
	// container that reached it (fallbackObjNum), and one /Resources object is
	// routinely shared by many pages — so the same *object.Dictionary is offered by
	// several containers and seen keeps only the first. Which container that was
	// came from Go's randomised map iteration, so a /CA or /SMask violation on a
	// shared graphics state reported a different object number on every run over
	// the same file. Lowest container object number is a total order, so it is
	// reproducible; that is load-bearing, since reports are diffed run to run.
	for _, num := range doc.SortedObjectNums() {
		switch v := doc.Objects[num].Value.(type) {
		case *object.Dictionary:
			resRef := v.Get("Resources")
			if resRef != nil {
				res := doc.ResolveDict(resRef)
				if res != nil {
					addFromResources(res, num)
				}
			}
		case *object.Stream:
			resRef := v.Dict.Get("Resources")
			if resRef != nil {
				res := doc.ResolveDict(resRef)
				if res != nil {
					addFromResources(res, num)
				}
			}
		}
	}

	return entries
}

// --- Image checks (6.2.7) ---

// Rule 6.2.7.1-1: No /Alternates in image XObjects.
// imageClause returns the ISO clause for an image/XObject-rule concept at the
// given level. Images are 6.2.4 in ISO 19005-1, 6.2.8.x in parts 2/3, and
// 6.2.7.x in part 4; clauses follow the veraPDF profiles.
func imageClause(concept string, level PDFALevel) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"image": {"6.2.4", "6.2.8", "6.2.7.1"},
		"jpx":   {"6.2.4", "6.2.8.3", "6.2.7.3"},
	}
	c, ok := m[concept]
	if !ok {
		return "6.2.8"
	}
	switch level {
	case PDFA1b:
		return c[0]
	case PDFA4:
		return c[2]
	default:
		return c[1]
	}
}

func checkNoAlternateImages(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if st, ok := stream.Dict.Get("Subtype").(object.Name); ok && st == "Image" {
			if stream.Dict.Get("Alternates") != nil {
				errs = append(errs, ValidationError{
					Rule:    imageClause("image", level),
					Level:   level,
					Message: "image XObject must not have /Alternates",
					Object:  num,
				})
			}
		}
	}
	return errs
}

// Rule 6.2.7.1-3: Interpolate must be false.
func checkInterpolate(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if st, ok := stream.Dict.Get("Subtype").(object.Name); ok && st == "Image" {
			interpObj := stream.Dict.Get("Interpolate")
			if interpObj != nil {
				if b, ok := interpObj.(object.Boolean); ok && bool(b) {
					errs = append(errs, ValidationError{
						Rule:    imageClause("image", level),
						Level:   level,
						Message: "/Interpolate must be false in image XObjects",
						Object:  num,
					})
				}
			}
		}
	}
	return errs
}

// Rules 6.2.7.1-2, 6.2.8.1-1: No /OPI in XObjects.
// xobjectClause returns the ISO clause for a form-XObject rule at the given
// level. A form XObject must not carry OPI/Subtype2/PS, and reference XObjects
// (a /Ref key) are forbidden outright. ISO 19005-1 6.2.4/6.2.6; -2/-3 6.2.9;
// -4 6.2.8.1/6.2.8.2. Clauses follow the veraPDF profiles.
func xobjectClause(concept string, level PDFALevel) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"formMisc": {"6.2.4", "6.2.9", "6.2.8.1"}, // OPI / Subtype2 / PS on a form
		"refXObj":  {"6.2.6", "6.2.9", "6.2.8.2"}, // reference XObjects
	}
	c, ok := m[concept]
	if !ok {
		return "6.2.9"
	}
	switch level {
	case PDFA1b:
		return c[0]
	case PDFA4:
		return c[2]
	default:
		return c[1]
	}
}

func checkNoOPI(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		st, ok := stream.Dict.Get("Subtype").(object.Name)
		if !ok {
			continue
		}
		add := func(rule, msg string) {
			errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: num})
		}
		switch st {
		case "Image":
			if stream.Dict.Get("OPI") != nil {
				add(imageClause("image", level), "image XObject must not have /OPI")
			}
		case "Form":
			// 6.2.9 (parts 2/3) / 6.2.8.1 (part 4): a form XObject shall not
			// contain the OPI key. (Subtype2/PS PostScript XObjects are handled
			// under the executed-content model in content_operators.go.)
			if stream.Dict.Get("OPI") != nil {
				add(xobjectClause("formMisc", level), "form XObject must not have /OPI")
			}
			// Reference XObjects (a /Ref key) are forbidden outright.
			if stream.Dict.Get("Ref") != nil {
				add(xobjectClause("refXObj", level), "form XObject must not be a reference XObject (/Ref)")
			}
		}
	}
	return errs
}

// --- Catalog version check (MR-3) ---

// Rule 6.1.12: PDF/A-4 catalog /Version must match pattern 2.N.
func checkCatalogVersion(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	versionObj := catalog.Get("Version")
	if versionObj == nil {
		return nil
	}

	vName, ok := versionObj.(object.Name)
	if !ok {
		return []ValidationError{{
			Rule:    "6.1.12",
			Level:   level,
			Message: "catalog /Version must be a name",
		}}
	}

	v := string(vName)
	if len(v) != 3 || v[0] != '2' || v[1] != '.' || v[2] < '0' || v[2] > '9' {
		return []ValidationError{{
			Rule:    "6.1.12",
			Level:   level,
			Message: fmt.Sprintf("catalog /Version must match 2.N, got %s", v),
		}}
	}

	return nil
}

// --- Font subset checks (MR-8) ---

// Rule 6.2.10: PDF/A-1b subset fonts must have CharSet or CIDSet.
func checkFontSubsets(doc core.View, level PDFALevel) []ValidationError {
	// CharSet/CIDSet PRESENCE is only required by 19005-1: the veraPDF
	// corpus passes a PDF/A-2 subset CIDFont without /CIDSet (Part 2 only
	// constrains the sets when present).
	if level != PDFA1b {
		return nil
	}

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return nil
	}

	fonts := collectFonts(doc, pagesRef)
	var errs []ValidationError

	for objNum, fontDict := range fonts {
		subtype, _ := fontDict.Get("Subtype").(object.Name)
		baseFont, _ := fontDict.Get("BaseFont").(object.Name)

		// Check if it's a subset font (XXXXXX+ prefix)
		baseFontStr := string(baseFont)
		if len(baseFontStr) < 7 || baseFontStr[6] != '+' {
			continue
		}
		isSubset := true
		for i := 0; i < 6; i++ {
			if baseFontStr[i] < 'A' || baseFontStr[i] > 'Z' {
				isSubset = false
				break
			}
		}
		if !isSubset {
			continue
		}

		switch subtype {
		case "Type1", "MMType1":
			fd := getFontDescriptor(doc, fontDict)
			if fd != nil && fd.Get("CharSet") == nil {
				errs = append(errs, ValidationError{
					Rule:    fontClause("charSet", level),
					Level:   level,
					Message: fmt.Sprintf("subset font %s (Type1) must have /CharSet in FontDescriptor", baseFontStr),
					Object:  objNum,
				})
			}
		case "Type0":
			dfRef := fontDict.Get("DescendantFonts")
			if dfRef == nil {
				continue
			}
			dfObj := doc.Resolve(dfRef)
			dfArr, ok := dfObj.(object.Array)
			if !ok || len(dfArr) == 0 {
				continue
			}
			cidFont := doc.ResolveDict(dfArr[0])
			if cidFont == nil {
				continue
			}
			fdRef := cidFont.Get("FontDescriptor")
			if fdRef == nil {
				continue
			}
			fd := doc.ResolveDict(fdRef)
			if fd != nil && fd.Get("CIDSet") == nil {
				errs = append(errs, ValidationError{
					Rule:    fontClause("charSet", level),
					Level:   level,
					Message: fmt.Sprintf("subset CIDFont %s must have /CIDSet in FontDescriptor", baseFontStr),
					Object:  objNum,
				})
			}
		}
	}

	return errs
}

func getFontDescriptor(doc core.View, fontDict *object.Dictionary) *object.Dictionary {
	fdRef := fontDict.Get("FontDescriptor")
	if fdRef == nil {
		return nil
	}
	return doc.ResolveDict(fdRef)
}

// --- ExtGState checks (MR-1) ---

// Rule 6.2.5: ExtGState forbidden keys for PDF/A-2b/3b/4.
func checkExtGState(doc core.View, level PDFALevel) []ValidationError {
	// ISO 19005-1 clause 6.2.8 carries the same TR/TR2 prohibitions as
	// 19005-2 clause 6.2.5; previously the whole check was skipped at 1b
	// with a comment claiming checkNoTransparency covered it, which never
	// looked at /TR, /TR2, or halftones.
	rule := "6.2.5"
	if level == PDFA1b {
		rule = "6.2.8"
	}

	var errs []ValidationError
	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		dict := entry.dict
		num := entry.objNum

		// /TR must not be present
		if dict.Get("TR") != nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /TR",
				Object:  num,
			})
		}

		// /TR2 must be /Default if present
		if tr2 := dict.Get("TR2"); tr2 != nil {
			if n, ok := tr2.(object.Name); !ok || n != "Default" {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: "/TR2 must be /Default",
					Object:  num,
				})
			}
		}

		// /HTO and /HTP must not be present (PDF 2.0 halftone keys;
		// restricted at 2b+).
		if level != PDFA1b && dict.Get("HTO") != nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /HTO",
				Object:  num,
			})
		}
		if level != PDFA1b && dict.Get("HTP") != nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /HTP",
				Object:  num,
			})
		}
		// /RI, when present, must be a standard rendering intent (all levels).
		if ri, ok := doc.Resolve(dict.Get("RI")).(object.Name); ok && !standardRenderingIntents[string(ri)] {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: fmt.Sprintf("ExtGState /RI uses a non-standard rendering intent /%s", string(ri)),
				Object:  num,
			})
		}

		// Check halftone
		if htRef := dict.Get("HT"); htRef != nil {
			checkHalftoneErrors(doc, htRef, num, level, rule, &errs)
		}

		// Check BM is a valid blend mode. At 1b any transparency use is
		// forbidden wholesale by checkNoTransparency.
		if level != PDFA1b {
			if bm := dict.Get("BM"); bm != nil {
				if n, ok := bm.(object.Name); ok {
					if !isValidBlendMode(n) {
						errs = append(errs, ValidationError{
							Rule:    rule,
							Level:   level,
							Message: fmt.Sprintf("invalid blend mode /%s", string(n)),
							Object:  num,
						})
					}
				}
			}
		}
	}
	return errs
}

// isValidBlendMode returns true if the name is one of the standard PDF blend modes.
func isValidBlendMode(bm object.Name) bool {
	switch bm {
	case "Normal", "Compatible", "Multiply", "Screen", "Overlay",
		"Darken", "Lighten", "ColorDodge", "ColorBurn",
		"HardLight", "SoftLight", "Difference", "Exclusion",
		"Hue", "Saturation", "Color", "Luminosity":
		return true
	}
	return false
}

func checkHalftoneErrors(doc core.View, htRef object.Object, objNum int, level PDFALevel, rule string, errs *[]ValidationError) {
	htDict := doc.ResolveDict(htRef)
	if htDict == nil {
		return
	}

	if htType := htDict.Get("HalftoneType"); htType != nil {
		if intVal, ok := htType.(object.Integer); ok {
			if intVal != 1 && intVal != 5 {
				*errs = append(*errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("halftone type must be 1 or 5, got %d", intVal),
					Object:  objNum,
				})
			}
		}
	}

	if htDict.Get("HalftoneName") != nil {
		*errs = append(*errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: "halftone must not contain /HalftoneName",
			Object:  objNum,
		})
	}

	if htDict.Get("TransferFunction") != nil {
		*errs = append(*errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: "halftone must not contain /TransferFunction",
			Object:  objNum,
		})
	}
}

// --- Info/XMP consistency check (MR-6) ---

// Rule 6.7.3: PDF/A-1b requires Info dict and XMP metadata to be consistent.
func checkInfoXMPConsistency(doc core.View, level PDFALevel) []ValidationError {
	// Info<->XMP consistency is a 19005-1 (6.7.3) requirement only: the
	// veraPDF corpus passes PDF/A-2 files whose Info entries deliberately
	// differ from their XMP counterparts (Part 2 deprecates Info instead).
	if level != PDFA1b {
		return nil
	}

	infoRef := doc.Trailer.Get("Info")
	if infoRef == nil {
		return nil
	}
	infoDict := doc.ResolveDict(infoRef)
	if infoDict == nil {
		return nil
	}

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	metaRef := catalog.Get("Metadata")
	if metaRef == nil {
		return nil
	}
	metaObj := doc.Resolve(metaRef)
	if metaObj == nil {
		return nil
	}
	stream, ok := metaObj.(*object.Stream)
	if !ok {
		return nil
	}
	xmp := doc.XMPText(stream)

	var errs []ValidationError

	pairs := []struct {
		infoKey string
		xmpKey  string
		isList  bool
	}{
		{"Title", "dc:title", true},
		{"Author", "dc:creator", true},
		{"Subject", "dc:description", true},
		{"Keywords", "pdf:Keywords", false},
		{"Creator", "xmp:CreatorTool", false},
		{"Producer", "pdf:Producer", false},
		{"CreationDate", "xmp:CreateDate", false},
		{"ModDate", "xmp:ModifyDate", false},
	}

	for _, p := range pairs {
		raw := infoDict.Get(object.Name(p.infoKey))
		if raw == nil {
			continue
		}
		// An Info entry that is an indirect object must resolve to a string
		// (ISO 19005-1 6.7.3): a non-string value is itself a violation.
		resolved := doc.Resolve(raw)
		if _, isNull := resolved.(object.Null); isNull || resolved == nil {
			continue // an indirect null value is equivalent to absence
		}
		strVal, isStr := resolved.(object.String)
		if !isStr {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("Info /%s is not a string value", p.infoKey),
			})
			continue
		}
		infoVal := core.DecodePDFTextString(strVal.Value)
		if infoVal == "" {
			continue
		}

		// When Info /Author is present, XMP dc:creator shall contain
		// exactly one entry (ISO 19005-1 6.7.3).
		if p.infoKey == "Author" && countXMPListEntries(xmp, "dc:creator") > 1 {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: "XMP dc:creator contains more than one entry while Info /Author is present",
			})
			continue
		}

		var xmpVal string
		if p.isList {
			xmpVal = extractXMPListValue(xmp, p.xmpKey)
		} else {
			xmpVal = core.ExtractXMPValue(xmp, p.xmpKey)
		}

		if xmpVal == "" {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("Info /%s present but XMP %s missing", p.infoKey, p.xmpKey),
			})
			continue
		}

		// For dates, normalize before comparing
		if p.infoKey == "CreationDate" || p.infoKey == "ModDate" {
			infoNorm := normalizePDFDate(infoVal)
			xmpNorm := normalizeXMPDate(xmpVal)
			if infoNorm != "" && xmpNorm != "" && infoNorm != xmpNorm {
				errs = append(errs, ValidationError{
					Rule:    "6.7.3",
					Level:   level,
					Message: fmt.Sprintf("Info /%s (%s) does not match XMP %s (%s)", p.infoKey, infoVal, p.xmpKey, xmpVal),
				})
			}
		} else {
			if infoVal != xmpVal {
				errs = append(errs, ValidationError{
					Rule:    "6.7.3",
					Level:   level,
					Message: fmt.Sprintf("Info /%s (%q) does not match XMP %s (%q)", p.infoKey, infoVal, p.xmpKey, xmpVal),
				})
			}
		}
	}

	return errs
}

func getInfoString(info *object.Dictionary, key string) string {
	obj := info.Get(object.Name(key))
	if obj == nil {
		return ""
	}
	if s, ok := obj.(object.String); ok {
		return core.DecodePDFTextString(s.Value)
	}
	return ""
}

// countXMPListEntries counts the rdf:li entries inside an XMP list-valued
// property (rdf:Seq/rdf:Bag/rdf:Alt).
func countXMPListEntries(xmp, key string) int {
	openTag := "<" + key + ">"
	closeTag := "</" + key + ">"
	i := strings.Index(xmp, openTag)
	if i < 0 {
		return 0
	}
	i += len(openTag)
	end := strings.Index(xmp[i:], closeTag)
	if end < 0 {
		return 0
	}
	section := xmp[i : i+end]
	return strings.Count(section, "<rdf:li")
}

func extractXMPListValue(xmp, key string) string {
	// Extract first rdf:li from an rdf:Seq/rdf:Bag/rdf:Alt container
	openTag := "<" + key + ">"
	closeTag := "</" + key + ">"
	idx := strings.Index(xmp, openTag)
	if idx < 0 {
		return core.ExtractXMPValue(xmp, key)
	}
	start := idx + len(openTag)
	endIdx := strings.Index(xmp[start:], closeTag)
	if endIdx < 0 {
		return ""
	}
	inner := xmp[start : start+endIdx]

	liOpen := strings.Index(inner, "<rdf:li")
	if liOpen < 0 {
		return ""
	}
	gtIdx := strings.Index(inner[liOpen:], ">")
	if gtIdx < 0 {
		return ""
	}
	valStart := liOpen + gtIdx + 1
	liClose := strings.Index(inner[valStart:], "</rdf:li>")
	if liClose < 0 {
		return ""
	}
	return strings.TrimSpace(inner[valStart : valStart+liClose])
}

func normalizePDFDate(s string) string {
	// Convert D:YYYYMMDDHHmmSSOHH'mm' to YYYY-MM-DDTHH:mm:SS+HH:mm
	s = strings.TrimPrefix(s, "D:")
	if len(s) < 4 {
		return s
	}
	year := s[0:4]
	month := "01"
	day := "01"
	hour := "00"
	min := "00"
	sec := "00"
	tz := "Z"

	if len(s) >= 6 {
		month = s[4:6]
	}
	if len(s) >= 8 {
		day = s[6:8]
	}
	if len(s) >= 10 {
		hour = s[8:10]
	}
	if len(s) >= 12 {
		min = s[10:12]
	}
	if len(s) >= 14 {
		sec = s[12:14]
	}
	if len(s) >= 15 {
		tzChar := s[14]
		if tzChar == 'Z' {
			tz = "Z"
		} else if tzChar == '+' || tzChar == '-' {
			tzOff := string(tzChar)
			if len(s) >= 17 {
				tzOff += s[15:17]
				rest := s[17:]
				rest = strings.TrimPrefix(rest, "'")
				if len(rest) >= 2 {
					tzOff += ":" + rest[0:2]
				} else {
					tzOff += ":00"
				}
			} else {
				// Offset hour is missing/truncated (e.g. "…SS+"); default to
				// whole-hour zero rather than slicing past the end of the string.
				tzOff += "00:00"
			}
			tz = tzOff
		}
	}

	result := year + "-" + month + "-" + day + "T" + hour + ":" + min + ":" + sec + tz
	// Normalize UTC offsets: +00:00 and -00:00 are equivalent to Z
	if strings.HasSuffix(result, "+00:00") {
		result = result[:len(result)-6] + "Z"
	} else if strings.HasSuffix(result, "-00:00") {
		result = result[:len(result)-6] + "Z"
	}
	return result
}

func normalizeXMPDate(s string) string {
	s = strings.TrimSpace(s)
	// normalizePDFDate folds a zero UTC offset to Z and always emits seconds;
	// apply the same canonicalization to the XMP-side ISO 8601 date so equal
	// instants written in different-but-equivalent forms compare equal (audit
	// C22). Info D:202401011200Z and XMP 2024-01-01T12:00+00:00 are the same
	// time and must not be reported as an Info/XMP mismatch.
	if strings.HasSuffix(s, "+00:00") || strings.HasSuffix(s, "-00:00") {
		s = s[:len(s)-6] + "Z"
	}
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		timePart := s[i+1:]
		tzIdx := len(timePart)
		for j := 0; j < len(timePart); j++ {
			if c := timePart[j]; c == 'Z' || c == '+' || c == '-' {
				tzIdx = j
				break
			}
		}
		hms := timePart[:tzIdx]
		if strings.Count(hms, ":") == 1 { // hh:mm -> hh:mm:00
			s = s[:i+1] + hms + ":00" + timePart[tzIdx:]
		}
	}
	return s
}

// --- Transparency blending check (MR-2) ---

// Rule 6.2.4: Pages using transparency must have proper blending color space.
func checkTransparencyBlending(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // PDF/A-1b prohibits transparency entirely
	}

	var errs []ValidationError

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return nil
	}

	pages := doc.Pages(pagesRef)
	for _, page := range pages {
		if !core.PageUsesTransparency(doc, page.Dict) {
			continue
		}

		groupRef := page.Dict.Get("Group")
		if groupRef == nil {
			// Check if the requirement can be relaxed
			if transparencyGroupNotRequired(doc, catalog, page.Dict, level) {
				continue
			}
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "page using transparency must have /Group with /S /Transparency",
				Object:  page.ObjNum,
			})
			continue
		}
		groupDict := doc.ResolveDict(groupRef)
		if groupDict == nil {
			continue
		}

		s, _ := groupDict.Get("S").(object.Name)
		if s != "Transparency" {
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "page /Group must have /S /Transparency",
				Object:  page.ObjNum,
			})
			continue
		}
		if groupDict.Get("CS") == nil {
			// For PDF/A-4, OutputIntents can provide the blending CS implicitly
			if !transparencyGroupNotRequired(doc, catalog, page.Dict, level) {
				errs = append(errs, ValidationError{
					Rule:    "6.2.4",
					Level:   level,
					Message: "page transparency group must have /CS (color space)",
					Object:  page.ObjNum,
				})
			}
		}
	}

	return errs
}

// transparencyGroupNotRequired checks if the transparency /Group requirement
// can be relaxed for a page. For PDF/A-4, OutputIntents provide implicit
// blending CS. For PDF/A-2b/3b, DefaultCS coverage can substitute.
func transparencyGroupNotRequired(doc core.View, catalog *object.Dictionary, page *object.Dictionary, level PDFALevel) bool {
	if level == PDFA4 {
		// PDF/A-4: page-level or catalog-level OutputIntents provide blending CS
		catalogRGB, catalogCMYK, catalogGray := getOutputIntentCoverage(doc, catalog)
		pageRGB, pageCMYK, pageGray := getOutputIntentCoverage(doc, page)
		if catalogRGB || catalogCMYK || catalogGray || pageRGB || pageCMYK || pageGray {
			return true
		}
	}

	// For PDF/A-2b/3b: OutputIntents or DefaultCS coverage can provide blending CS
	if level == PDFA2b || level == PDFA3b {
		// Catalog-level OutputIntents provide blending CS for all pages
		catalogRGB, catalogCMYK, catalogGray := getOutputIntentCoverage(doc, catalog)
		if catalogRGB || catalogCMYK || catalogGray {
			return true
		}

		// DefaultCS entries cover device CS usage
		hasDefRGB, hasDefCMYK, hasDefGray := core.DefaultColorSpaces(doc, page)
		usesRGB, usesCMYK, usesGray := core.PageDeviceColourUse(doc, page)
		allCovered := true
		if usesRGB && !hasDefRGB {
			allCovered = false
		}
		if usesCMYK && !hasDefCMYK {
			allCovered = false
		}
		if usesGray && !hasDefGray {
			allCovered = false
		}
		if allCovered && (hasDefRGB || hasDefCMYK || hasDefGray) {
			return true
		}
	}

	return false
}

// find1bTransparencyXObjects recursively scans a resource-bearing dictionary's
// XObjects (and nested form/pattern/Type3 resources) for the transparency
// signals PDF/A-1b forbids but the page-/Group and ExtGState scans miss: image
// soft masks and form transparency groups. Unlike resourcesUseTransparency
// (tuned for the 2b+ blending-group question, which treats a self-contained
// form group as not propagating), presence alone is a violation here.
func find1bTransparencyXObjects(doc core.View, container *object.Dictionary, level PDFALevel, seen map[*object.Dictionary]bool, errs *[]ValidationError) {
	if seen[container] {
		return
	}
	seen[container] = true

	res := doc.ResolveDict(container.Get("Resources"))
	if res == nil {
		return
	}

	if xobjDict := doc.ResolveDict(res.Get("XObject")); xobjDict != nil {
		for i, val := range xobjDict.Values {
			stream, ok := doc.Resolve(val).(*object.Stream)
			if !ok {
				continue
			}
			num := resolveObjNum(doc, val)
			switch subtype, _ := stream.Dict.Get("Subtype").(object.Name); subtype {
			case "Image":
				if sm := stream.Dict.Get("SMask"); sm != nil {
					if n, ok := sm.(object.Name); !ok || n != "None" {
						*errs = append(*errs, ValidationError{
							Rule:    "6.4",
							Level:   level,
							Message: "image XObject must not have /SMask (PDF/A-1b forbids transparency)",
							Object:  num,
						})
					}
				}
			case "Form":
				if g := doc.ResolveDict(stream.Dict.Get("Group")); g != nil {
					if s, _ := g.Get("S").(object.Name); s == "Transparency" {
						*errs = append(*errs, ValidationError{
							Rule:    "6.4",
							Level:   level,
							Message: "form XObject must not have a /Group with /S /Transparency (PDF/A-1b forbids transparency)",
							Object:  num,
						})
					}
				}
				find1bTransparencyXObjects(doc, &stream.Dict, level, seen, errs)
			}
			_ = i
		}
	}

	if patDict := doc.ResolveDict(res.Get("Pattern")); patDict != nil {
		for _, val := range patDict.Values {
			if stream, ok := doc.Resolve(val).(*object.Stream); ok {
				find1bTransparencyXObjects(doc, &stream.Dict, level, seen, errs)
			}
		}
	}

	if fontDict := doc.ResolveDict(res.Get("Font")); fontDict != nil {
		for _, val := range fontDict.Values {
			if fd := doc.ResolveDict(val); fd != nil {
				if st, _ := fd.Get("Subtype").(object.Name); st == "Type3" {
					find1bTransparencyXObjects(doc, fd, level, seen, errs)
				}
			}
		}
	}
}

func collectPages(doc core.View, pageTreeRef object.Object) []core.PageInfo {
	return doc.Pages(pageTreeRef)
}

// --- Embedded files check (MR-4) ---

// Rule 6.1.12: Embedded file restrictions.
func checkEmbeddedFiles(doc core.View, level PDFALevel) []ValidationError {
	// PDF/A-1 (ISO 19005-1, 6.1.11) forbids embedded files outright: no
	// file specification may carry /EF, wherever it lives — not only in the
	// catalog's Names tree.
	if level == PDFA1b {
		var errs []ValidationError
		for num, iobj := range doc.Objects {
			if dict, ok := iobj.Value.(*object.Dictionary); ok && dict.Get("EF") != nil {
				errs = append(errs, ValidationError{
					Rule:    "6.1.11",
					Level:   level,
					Message: "file specification must not contain /EF (embedded files are forbidden in PDF/A-1)",
					Object:  num,
				})
			}
		}
		catalog := doc.Catalog()
		if catalog != nil {
			if namesDict := doc.ResolveDict(catalog.Get("Names")); namesDict != nil {
				if namesDict.Get("EmbeddedFiles") != nil {
					errs = append(errs, ValidationError{
						Rule:    "6.1.11",
						Level:   level,
						Message: "Names/EmbeddedFiles must not be present",
					})
				}
			}
		}
		return errs
	}

	// PDF/A-2 permits embedded files (they must themselves be PDF/A, which
	// is not machine-checkable here); PDF/A-3/4 permit arbitrary embedded
	// files. All three levels constrain the file specifications.
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	return checkEmbeddedFileSpecs(doc, level, catalog)
}

func checkEmbeddedFileSpecs(doc core.View, level PDFALevel, catalog *object.Dictionary) []ValidationError {
	var errs []ValidationError

	// Embedded-file rules live in clause 6.8 for 19005-2/-3 and 6.9 for
	// 19005-4.
	rule := "6.8"
	if level == PDFA4 {
		rule = "6.9"
	}

	// PDF/A-3 and A-4 require embedded files to be associated with the
	// document or one of its parts via /AF (the corpus fails A-3 files
	// whose embedded file is associated with nothing). PDF/A-2 has no
	// association mechanism. PDF/A-4f and -4e exist to carry embedded files
	// (arbitrary files; 3D/RichMedia content) and associate them per-filespec
	// via /AFRelationship rather than a document-level /AF array, so the
	// document-/AF requirement is relaxed for both.
	conformance := ""
	if level == PDFA4 {
		conformance = pdfaConformanceFlag(doc)
	}
	relaxAF := conformance == "F" || conformance == "E"
	if level != PDFA2b && !relaxAF && documentHasEmbeddedFiles(doc, catalog) && !documentHasAF(doc) {
		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: "document must have /AF array when embedded files are present",
		})
	}

	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		// A file specification is not required to carry /Type /Filespec;
		// anything holding an /EF is acting as one.
		t, hasType := dict.Get("Type").(object.Name)
		isFilespec := (hasType && t == "Filespec") || dict.Get("EF") != nil
		if !isFilespec {
			continue
		}

		if dict.Get("F") == nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /F",
				Object:  num,
			})
		}
		if dict.Get("UF") == nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /UF",
				Object:  num,
			})
		}
		// /AFRelationship is the PDF/A-3+ mechanism relating an embedded
		// file to the document; PDF/A-2 has no such key.
		if level != PDFA2b && dict.Get("AFRelationship") == nil {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /AFRelationship",
				Object:  num,
			})
		}

		// Embedded file streams must declare their MIME type in PDF/A-3/4.
		if level == PDFA3b || level == PDFA4 {
			if efDict := doc.ResolveDict(dict.Get("EF")); efDict != nil {
				for _, val := range efDict.Values {
					stream, ok := doc.Resolve(val).(*object.Stream)
					if !ok {
						continue
					}
					st := stream.Dict.Get("Subtype")
					if st == nil {
						errs = append(errs, ValidationError{
							Rule:    rule,
							Level:   level,
							Message: "embedded file stream must have /Subtype (MIME type)",
							Object:  num,
						})
					} else if name, ok := st.(object.Name); ok {
						if !strings.Contains(string(name), "/") {
							errs = append(errs, ValidationError{
								Rule:    rule,
								Level:   level,
								Message: fmt.Sprintf("embedded file stream /Subtype must be a MIME type, got /%s", string(name)),
								Object:  num,
							})
						}
					}
				}
			}
		}
	}

	return errs
}

// documentHasEmbeddedFiles reports whether the catalog's Names tree declares
// EmbeddedFiles or any object carries an /EF file specification.
func documentHasEmbeddedFiles(doc core.View, catalog *object.Dictionary) bool {
	if namesDict := doc.ResolveDict(catalog.Get("Names")); namesDict != nil {
		if namesDict.Get("EmbeddedFiles") != nil {
			return true
		}
	}
	for _, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok && dict.Get("EF") != nil {
			return true
		}
	}
	return false
}

func documentHasAF(doc core.View) bool {
	catalog := doc.Catalog()
	if catalog != nil && catalog.Get("AF") != nil {
		return true
	}
	for _, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*object.Dictionary); ok {
			if dict.Get("AF") != nil {
				return true
			}
		}
		if stream, ok := iobj.Value.(*object.Stream); ok {
			if stream.Dict.Get("AF") != nil {
				return true
			}
		}
	}
	return false
}

// --- Optional content check (MR-5) ---

// Rule 6.1.13: Optional content requirements for PDF/A-4.
func checkOptionalContent(doc core.View, level PDFALevel) []ValidationError {
	// Optional-content configuration rules are 19005-2/-3 clause 6.9 and
	// 19005-4 clause 6.10. PDF/A-1 forbids optional content wholesale
	// (checkNoOCProperties).
	if level == PDFA1b {
		return nil
	}
	ocRule := "6.9"
	if level == PDFA4 {
		ocRule = "6.10"
	}

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	ocpRef := catalog.Get("OCProperties")
	if ocpRef == nil {
		return nil
	}

	ocpDict := doc.ResolveDict(ocpRef)
	if ocpDict == nil {
		return nil
	}

	var errs []ValidationError

	dRef := ocpDict.Get("D")
	if dRef == nil {
		return errs
	}
	dDict := doc.ResolveDict(dRef)
	if dDict == nil {
		return errs
	}

	if dDict.Get("Name") == nil {
		errs = append(errs, ValidationError{
			Rule:    ocRule,
			Level:   level,
			Message: "OCProperties default config /D must have /Name",
		})
	}

	// Check all config names unique
	ocgsRef := ocpDict.Get("OCGs")
	if ocgsRef == nil {
		return errs
	}
	ocgsArr, ok := doc.Resolve(ocgsRef).(object.Array)
	if !ok {
		return errs
	}

	names := make(map[string]bool)
	configs := []object.Object{dRef}
	if configsRef := ocpDict.Get("Configs"); configsRef != nil {
		if arr, ok := doc.Resolve(configsRef).(object.Array); ok {
			configs = append(configs, arr...)
			// Every configuration dictionary in /Configs must carry a /Name
			// (ISO 19005-2/-3 6.9, -4 6.10).
			for _, cfgRef := range arr {
				if cfg := doc.ResolveDict(cfgRef); cfg != nil && cfg.Get("Name") == nil {
					errs = append(errs, ValidationError{
						Rule:    ocRule,
						Level:   level,
						Message: "an optional-content configuration in /Configs must contain a /Name",
					})
				}
			}
		}
	}
	for _, cfgRef := range configs {
		cfgDict := doc.ResolveDict(cfgRef)
		if cfgDict == nil {
			continue
		}
		if nameObj := cfgDict.Get("Name"); nameObj != nil {
			if s, ok := nameObj.(object.String); ok {
				n := string(s.Value)
				if names[n] {
					errs = append(errs, ValidationError{
						Rule:    ocRule,
						Level:   level,
						Message: fmt.Sprintf("OCProperties config name %q is not unique", n),
					})
				}
				names[n] = true
			}
		}
	}

	// Check /Order references all OCGs
	orderRef := dDict.Get("Order")
	if orderRef != nil {
		orderArr, ok := doc.Resolve(orderRef).(object.Array)
		if ok {
			referencedOCGs := make(map[int]bool)
			collectOCGRefs(orderArr, referencedOCGs)
			for _, ocgRef := range ocgsArr {
				if iref, ok := ocgRef.(object.IndirectRef); ok {
					if !referencedOCGs[iref.Number] {
						errs = append(errs, ValidationError{
							Rule:    ocRule,
							Level:   level,
							Message: fmt.Sprintf("OCG %d not referenced in /Order array", iref.Number),
						})
					}
				}
			}
		}
	}

	return errs
}

func collectOCGRefs(arr object.Array, refs map[int]bool) {
	for _, item := range arr {
		if iref, ok := item.(object.IndirectRef); ok {
			refs[iref.Number] = true
		}
		if subArr, ok := item.(object.Array); ok {
			collectOCGRefs(subArr, refs)
		}
	}
}

// --- Implementation limits check (MR-7) ---

// Rule 6.1.7: Implementation limits for PDF/A.
// implLimits carries the Annex C implementation limits and the rule ID they
// are reported under: ISO 19005-1 clause 6.1.12 for PDF/A-1, ISO 19005-2/-3
// clause 6.1.13 for PDF/A-2/-3. PDF/A-4 (PDF 2.0) has no such clause.
type implLimits struct {
	rule      string
	nameLen   int
	stringLen int
	dictEnt   int
	arrayLen  int
	nesting   int
	realLimit float64
}

func checkImplementationLimits(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		// PDF 2.0 (ISO 32000-2) abolished the Annex C limits; ISO 19005-4
		// has no implementation-limits clause.
		return nil
	}

	lim := implLimits{
		rule:      "6.1.12", // ISO 19005-1
		nameLen:   127,
		stringLen: 65535,
		dictEnt:   4095,
		arrayLen:  8191,
		nesting:   28,
		realLimit: 32767, // PDF 1.4 Annex C
	}
	if level == PDFA2b || level == PDFA3b {
		lim.rule = "6.1.13" // ISO 19005-2/-3
		lim.stringLen = 32767
		lim.realLimit = 3.403e38 // PDF 1.7 Annex C (float32 range)
	}

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		checkObjectLimits(iobj.Value, num, level, lim, 0, &errs)
	}

	// q/Q nesting depth check in content streams
	checkQNestingDepth(doc, level, lim.rule, &errs)

	// Content-stream operand limits (Annex C: reals, integers, and the
	// content-stream string-length limit apply per-operand, not to the
	// parsed object model).
	checkContentStreamLimits(doc, level, lim, &errs)

	// Page size limits for 2b+ only
	if level != PDFA1b {
		checkPageSizeLimits(doc, level, &errs)
	}

	return errs
}

func checkObjectLimits(obj object.Object, objNum int, level PDFALevel, lim implLimits, depth int, errs *[]ValidationError) {
	if obj == nil {
		return
	}

	switch v := obj.(type) {
	case object.Name:
		if len(string(v)) > lim.nameLen {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("name length %d exceeds maximum %d", len(string(v)), lim.nameLen),
				Object:  objNum,
			})
		}
	case object.String:
		if len(v.Value) > lim.stringLen {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("string length %d exceeds maximum %d", len(v.Value), lim.stringLen),
				Object:  objNum,
			})
		}
	case object.Integer:
		i := int64(v)
		if i < -2147483648 || i > 2147483647 {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("integer %d out of range [-2^31, 2^31-1]", i),
				Object:  objNum,
			})
		}
	case object.Real:
		if math.Abs(float64(v)) > lim.realLimit {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("real %g exceeds magnitude limit %g", float64(v), lim.realLimit),
				Object:  objNum,
			})
		}
	case *object.Dictionary:
		if depth > lim.nesting {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("dictionary nesting depth %d exceeds maximum %d", depth, lim.nesting),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if v.Len() > lim.dictEnt {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("dictionary has %d entries, exceeds maximum %d", v.Len(), lim.dictEnt),
				Object:  objNum,
			})
		}
		for i, key := range v.Keys {
			checkObjectLimits(key, objNum, level, lim, depth+1, errs)
			checkObjectLimits(v.Values[i], objNum, level, lim, depth+1, errs)
		}
	case object.Array:
		if depth > lim.nesting {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("array nesting depth %d exceeds maximum %d", depth, lim.nesting),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if len(v) > lim.arrayLen {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("array has %d elements, exceeds maximum %d", len(v), lim.arrayLen),
				Object:  objNum,
			})
		}
		for _, elem := range v {
			checkObjectLimits(elem, objNum, level, lim, depth+1, errs)
		}
	case *object.Stream:
		checkObjectLimits(&v.Dict, objNum, level, lim, depth, errs)
	}
}

func checkPageSizeLimits(doc core.View, level PDFALevel, errs *[]ValidationError) {
	catalog := doc.Catalog()
	if catalog == nil {
		return
	}
	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return
	}

	pages := doc.Pages(pagesRef)
	for _, page := range pages {
		for _, boxKey := range []object.Name{"MediaBox", "CropBox", "BleedBox", "TrimBox", "ArtBox"} {
			var boxObj object.Object
			switch boxKey {
			case "MediaBox", "CropBox":
				// Inheritable attributes: a page without its own entry
				// takes its Pages ancestor's.
				boxObj = doc.Resolve(doc.InheritedPageAttr(page.Dict, boxKey))
			default:
				boxObj = doc.Resolve(page.Dict.Get(boxKey))
			}
			if boxObj == nil {
				continue
			}
			arr, ok := boxObj.(object.Array)
			if !ok || len(arr) != 4 {
				continue
			}
			vals := make([]float64, 4)
			valid := true
			for i, elem := range arr {
				switch ev := elem.(type) {
				case object.Integer:
					vals[i] = float64(ev)
				case object.Real:
					vals[i] = float64(ev)
				default:
					valid = false
				}
			}
			if !valid {
				continue
			}
			width := math.Abs(vals[2] - vals[0])
			height := math.Abs(vals[3] - vals[1])
			if width < 3 || width > 14400 || height < 3 || height > 14400 {
				*errs = append(*errs, ValidationError{
					Rule:    "6.1.13",
					Level:   level,
					Message: fmt.Sprintf("page %s dimensions %.0fx%.0f out of range [3, 14400]", boxKey, width, height),
					Object:  page.ObjNum,
				})
			}
		}
	}
}

// checkQNestingDepth checks that q/Q nesting depth in content streams
// does not exceed 28 levels (PDF/A implementation limit).
func checkQNestingDepth(doc core.View, level PDFALevel, rule string, errs *[]ValidationError) {
	const maxQDepth = 28

	report := func(data []byte, objNum int) {
		if d := qNestingMaxDepth(doc.Cancel, data); d > maxQDepth {
			*errs = append(*errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: fmt.Sprintf("q/Q nesting depth %d exceeds maximum %d", d, maxQDepth),
				Object:  objNum,
			})
		}
	}

	// Only page /Contents is measured: the limit is about runtime
	// graphics-state nesting, and a form XObject's q/Q only nest when the
	// form is actually invoked (veraPDF passes a depth-30 form that no
	// content stream executes).
	catalog := doc.Catalog()
	if catalog == nil {
		return
	}
	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return
	}
	for _, page := range doc.Pages(pagesRef) {
		contentsRef := page.Dict.Get("Contents")
		if contentsRef == nil {
			continue
		}
		if data := core.ContentStreamData(doc, contentsRef); data != nil {
			report(data, page.ObjNum)
		}
	}
}

// qNestingMaxDepth computes the maximum q/Q nesting depth of a decoded
// content stream using a real operator tokenizer, so 'q' bytes inside string
// literals, comments, names, or inline-image binary data do not count.
func qNestingMaxDepth(cancel core.Canceler, data []byte) int {
	depth, maxDepth := 0, 0
	forEachContentOperator(cancel, data, func(op []byte) {
		if len(op) != 1 {
			return
		}
		switch op[0] {
		case 'q':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case 'Q':
			if depth > 0 {
				depth--
			}
		}
	})
	return maxDepth
}

// --- Device color space checks (6.2.3/6.2.4) ---

// Rule 6.2.3.3/6.2.4.3: Device color spaces (DeviceRGB, DeviceCMYK, DeviceGray)
// require either a default color space mapping or a matching OutputIntent.
func checkDeviceColorSpaces(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	// Determine which color spaces are covered by catalog-level OutputIntents
	hasRGBIntent, hasCMYKIntent, hasGrayIntent := getOutputIntentCoverage(doc, catalog)

	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return nil
	}

	var errs []ValidationError
	pages := doc.Pages(pagesRef)
	for _, page := range pages {
		// For PDF/A-4, also check page-level OutputIntents
		pageRGB, pageCMYK, pageGray := hasRGBIntent, hasCMYKIntent, hasGrayIntent
		if level == PDFA4 {
			prgb, pcmyk, pgray := getOutputIntentCoverage(doc, page.Dict)
			pageRGB = pageRGB || prgb
			pageCMYK = pageCMYK || pcmyk
			pageGray = pageGray || pgray
		}

		// Scan for device color space usage on this page. Default* colour
		// spaces are applied inside the scan, per resource scope: a page-
		// level DefaultCMYK does not cover DeviceCMYK inside a pattern with
		// its own resources, and the corpus fails exactly that.
		usesRGB, usesCMYK, usesGray := core.PageDeviceColourUse(doc, page.Dict)

		// The page's transparency /Group /CS covers type-matched DeviceRGB
		// and DeviceCMYK, but NOT DeviceGray: the corpus passes DeviceRGB
		// under an ICCBased RGB page group yet fails DeviceGray under an
		// ICCBased Gray one.
		groupRGB, groupCMYK, _ := core.GroupCSCoverage(doc, page.Dict)

		if usesRGB && !pageRGB && !groupRGB {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceRGB used without matching OutputIntent or DefaultRGB",
				Object:  page.ObjNum,
			})
		}

		if usesCMYK && !pageCMYK && !groupCMYK {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceCMYK used without matching OutputIntent or DefaultCMYK",
				Object:  page.ObjNum,
			})
		}

		// DeviceGray: any OutputIntent covers it
		if usesGray && !pageRGB && !pageCMYK && !pageGray {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceGray used without matching OutputIntent or DefaultGray",
				Object:  page.ObjNum,
			})
		}
	}

	return errs
}

// getOutputIntentCoverage checks OutputIntents for DestOutputProfile and
// returns which color space types are covered (RGB, CMYK).
func getOutputIntentCoverage(doc core.View, catalog *object.Dictionary) (hasRGB, hasCMYK, hasGray bool) {
	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return
	}
	oiObj := doc.Resolve(oiRef)
	arr, ok := oiObj.(object.Array)
	if !ok || len(arr) == 0 {
		return
	}

	for _, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			continue
		}
		// Only the PDF/A output intent counts: device colour backed solely
		// by e.g. a PDF/X intent is a violation (the corpus fails a
		// DeviceRGB file whose only intent is GTS_PDFX).
		if s, _ := dict.Get("S").(object.Name); s != "GTS_PDFA1" {
			continue
		}
		profileRef := dict.Get("DestOutputProfile")
		if profileRef == nil {
			// If there's an OutputIntent without a profile, it still signals
			// intent. For OutputConditionIdentifier-based intents, treat as
			// covering both RGB and CMYK (conservative).
			oci := dict.Get("OutputConditionIdentifier")
			if oci != nil {
				hasRGB = true
				hasCMYK = true
			}
			continue
		}
		profileObj := doc.Resolve(profileRef)
		stream, ok := profileObj.(*object.Stream)
		if !ok {
			continue
		}

		// Decompress the profile data to read the ICC header
		profileData := core.ICCProfileData(stream, doc.Limits)
		if len(profileData) < 20 {
			// Can't read profile header; assume it covers both spaces
			// to avoid false positives.
			hasRGB = true
			hasCMYK = true
			continue
		}

		// ICC profile color space is at bytes 16-19
		cs := string(profileData[16:20])
		switch cs {
		case "RGB ":
			hasRGB = true
		case "CMYK":
			hasCMYK = true
		case "GRAY":
			hasGray = true
		default:
			// Unknown profile type - assume it covers both to avoid false positives
			hasRGB = true
			hasCMYK = true
		}
	}
	return
}

// resolveResources resolves a page's Resources dictionary.
func resolveResources(doc core.View, page *object.Dictionary) *object.Dictionary {
	return doc.Resources(page)
}

// inheritedPageAttr looks up an inheritable page attribute (Resources,
// MediaBox, CropBox, Rotate), walking up the /Parent chain when the page
// itself does not define it — pages routinely inherit these from their
// Pages node, which the direct Get missed entirely.
func inheritedPageAttr(doc core.View, page *object.Dictionary, key object.Name) object.Object {
	return doc.InheritedPageAttr(page, key)
}

// from the aggregate does not unbound the run; the bytes are still charged, so
// they still count against genuinely unbounded content.
func decodeMetadataStream(doc core.View, stream *object.Stream) []byte {
	return doc.MetadataContent(stream)
}

// scanContentsForDeviceOps scans a page's Contents (stream or array of streams)
// for device color operators (rg/RG, k/K, g/G).
func scanContentsForDeviceOps(doc core.View, contentsRef object.Object) (usesRGB, usesCMYK, usesGray bool) {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *object.Stream:
		data := doc.Content(v)
		if data == nil {
			return
		}
		r, c, g := core.ScanStreamForDeviceOps(doc.Cancel, data)
		usesRGB = usesRGB || r
		usesCMYK = usesCMYK || c
		usesGray = usesGray || g
	case object.Array:
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if s, ok := streamObj.(*object.Stream); ok {
				data := doc.Content(s)
				if data == nil {
					continue
				}
				r, c, g := core.ScanStreamForDeviceOps(doc.Cancel, data)
				usesRGB = usesRGB || r
				usesCMYK = usesCMYK || c
				usesGray = usesGray || g
			}
		}
	}
	return
}

// forEachContentOperator tokenizes a decoded content stream and calls fn for
// each operator-position token (anything that is not a string, hex string,
// dictionary marker, array/procedure delimiter, comment, or name). object.String
// literals, comments, and inline-image binary data (BI ... ID <binary> EI)
// are skipped, so operator bytes occurring inside them are never reported.
func forEachContentOperator(cancel core.Canceler, data []byte, fn func(op []byte)) {
	core.ForEachContentToken(cancel, data, func(tok []byte, isName bool) {
		if !isName {
			fn(tok)
		}
	})
}

// --- ICCBased color space checks (6.2.4.2) ---

// Rule 6.2.4.2: ICCBased color spaces must reference valid ICC profiles.
func checkICCBasedProfiles(doc core.View, level PDFALevel) []ValidationError {
	var errs []ValidationError

	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}

		// Check if this stream is used as an ICC profile (has /N key typical of ICC)
		nObj := stream.Dict.Get("N")
		if nObj == nil {
			continue
		}

		// Structural stream types also carry an integer /N with a different
		// meaning (an object stream's /N is its object count); they are never
		// ICC profiles.
		if t, ok := stream.Dict.Get("Type").(object.Name); ok && (t == "ObjStm" || t == "XRef") {
			continue
		}

		// Verify it's actually an ICC profile by checking for Alternate or being
		// referenced from a ColorSpace array. We check for the /N key which is
		// specific to ICC profile streams.
		nVal := 0
		switch v := nObj.(type) {
		case object.Integer:
			nVal = int(v)
		default:
			continue
		}

		// N must be 1, 3, or 4
		if nVal != 1 && nVal != 3 && nVal != 4 {
			errs = append(errs, ValidationError{
				Rule:    colourClause("iccBased", level),
				Level:   level,
				Message: fmt.Sprintf("ICCBased profile /N must be 1, 3, or 4, got %d", nVal),
				Object:  num,
			})
			continue
		}

		// Decompress profile data to check ICC header
		profileData := core.ICCProfileData(stream, doc.Limits)

		// Check ICC profile header if data is available
		if len(profileData) >= 20 {
			cs := string(profileData[16:20])
			expectedN := 0
			switch cs {
			case "RGB ":
				expectedN = 3
			case "CMYK":
				expectedN = 4
			case "GRAY":
				expectedN = 1
			}
			if expectedN > 0 && expectedN != nVal {
				errs = append(errs, ValidationError{
					Rule:    colourClause("iccBased", level),
					Level:   level,
					Message: fmt.Sprintf("ICCBased profile /N=%d does not match ICC color space %q", nVal, cs),
					Object:  num,
				})
			}
		}

		// Check ICC profile version
		if len(profileData) >= 9 {
			majorVersion := profileData[8]
			maxVersion := byte(4) // Default max for 2b/3b/4
			rule := "6.2.4"
			if level == PDFA1b {
				maxVersion = 2
				rule = "6.2.3"
			}
			if majorVersion > maxVersion {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("ICCBased profile version %d.x not allowed (max %d.x)", majorVersion, maxVersion),
					Object:  num,
				})
			}
		}
	}

	return errs
}

// --- Separation/DeviceN checks (6.2.4.4) ---

// Rule 6.2.4.4 / 6.2.3.4: Separation and DeviceN color space restrictions.
func checkSeparationDeviceN(doc core.View, level PDFALevel) []ValidationError {

	var errs []ValidationError

	// Track tint transform references by colorant name for consistency check
	tintTransforms := make(map[object.Name]sepColorantSeen) // colorant name → first seen definition

	// Scan all objects for color space arrays used in Resources
	for num, iobj := range doc.Objects {
		dict, isDict := iobj.Value.(*object.Dictionary)
		stream, isStream := iobj.Value.(*object.Stream)

		// Check dictionary Resources/ColorSpace
		if isDict {
			checkDictForSepDeviceN(doc, dict, num, level, &errs)
			collectTintTransforms(doc, dict, tintTransforms, num, level, &errs)
			// A direct /Resources sub-dictionary (the common case on pages)
			// is not a top-level object, so this scan would never visit its
			// /ColorSpace entries; descend explicitly. Indirect Resources
			// are separate objects and are visited by the loop itself.
			if resDict, ok := dict.Get("Resources").(*object.Dictionary); ok {
				checkDictForSepDeviceN(doc, resDict, num, level, &errs)
				collectTintTransforms(doc, resDict, tintTransforms, num, level, &errs)
			}
		}
		// Check stream dict (e.g., Form XObjects, Image XObjects)
		if isStream {
			csObj := stream.Dict.Get("ColorSpace")
			if csObj != nil {
				checkColorSpaceValue(doc, csObj, num, level, &errs)
			}
			// Also check direct Resources in Form XObjects (indirect ones
			// are visited as top-level objects).
			if resDict, ok := stream.Dict.Get("Resources").(*object.Dictionary); ok {
				checkDictForSepDeviceN(doc, resDict, num, level, &errs)
				collectTintTransforms(doc, resDict, tintTransforms, num, level, &errs)
			}
		}
	}

	return errs
}

// collectTintTransforms tracks Separation color spaces by colorant name
// and flags inconsistent tint transforms for the same colorant name.
// sepColorantSeen records the first Separation definition seen for a
// colorant name, for the same-tint-transform/same-alternate consistency rule.
type sepColorantSeen struct {
	objNum int
	tint   object.Object
	alt    object.Object
}

func collectTintTransforms(doc core.View, dict *object.Dictionary, tintTransforms map[object.Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError) {
	csRef := dict.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		return
	}
	for _, val := range csDict.Values {
		collectSeparationConsistency(doc, val, tintTransforms, objNum, level, errs)
	}
}

// collectSeparationConsistency records a Separation definition (top-level or
// inside a DeviceN/NChannel Colorants dictionary) and flags same-name
// definitions whose tint transform or alternate space differ.
func collectSeparationConsistency(doc core.View, val object.Object, tintTransforms map[object.Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError) {
	collectSeparationConsistencySeen(doc, val, tintTransforms, objNum, level, errs, make(map[int]bool))
}

func collectSeparationConsistencySeen(doc core.View, val object.Object, tintTransforms map[object.Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	// Guard against a DeviceN whose /Colorants entry cycles back to itself: a
	// self-referential colorant would otherwise recurse until the goroutine
	// stack overflows (an unrecoverable fatal error), like the other
	// colour-space walkers this thread a visited-set keyed on object number.
	if ref, ok := val.(object.IndirectRef); ok {
		if seen[ref.Number] {
			return
		}
		seen[ref.Number] = true
	}
	resolved := doc.Resolve(val)
	arr, ok := resolved.(object.Array)
	if !ok || len(arr) == 0 {
		return
	}
	csType, _ := arr[0].(object.Name)

	// Separations inside a DeviceN attributes' Colorants dictionary join
	// the same consistency pool (the corpus flags NChannel colorants with
	// same-name/different-transform Separations).
	if csType == "DeviceN" && len(arr) >= 5 {
		if attrDict := doc.ResolveDict(arr[4]); attrDict != nil {
			if colorantsDict := doc.ResolveDict(attrDict.Get("Colorants")); colorantsDict != nil {
				for _, cval := range colorantsDict.Values {
					collectSeparationConsistencySeen(doc, cval, tintTransforms, objNum, level, errs, seen)
				}
			}
		}
		return
	}

	if csType != "Separation" || len(arr) < 4 {
		return
	}
	colorantName, ok := arr[1].(object.Name)
	if !ok {
		return
	}
	tintRef, isRef := arr[3].(object.IndirectRef)
	if !isRef {
		return
	}
	if prev, exists := tintTransforms[colorantName]; exists {
		// Different objects may still hold identical content, which is
		// conformant: the rule requires the SAME tint transform and
		// alternate space, and veraPDF accepts equal-by-content duplicates.
		sameTint := prev.objNum == tintRef.Number || object.Equal(doc.Resolve(prev.tint), doc.Resolve(tintRef))
		if !sameTint {
			*errs = append(*errs, ValidationError{
				Rule:    colourClause("spot", level),
				Level:   level,
				Message: fmt.Sprintf("Separation colorant /%s has inconsistent tint transforms (objects %d and %d)", string(colorantName), prev.objNum, tintRef.Number),
				Object:  objNum,
			})
		}
		if !object.Equal(doc.Resolve(prev.alt), doc.Resolve(arr[2])) {
			*errs = append(*errs, ValidationError{
				Rule:    colourClause("spot", level),
				Level:   level,
				Message: fmt.Sprintf("Separation colorant /%s has inconsistent alternate color spaces", string(colorantName)),
				Object:  objNum,
			})
		}
	} else {
		tintTransforms[colorantName] = sepColorantSeen{objNum: tintRef.Number, tint: tintRef, alt: arr[2]}
	}
}

func checkDictForSepDeviceN(doc core.View, dict *object.Dictionary, objNum int, level PDFALevel, errs *[]ValidationError) {
	csRef := dict.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		return
	}
	for _, val := range csDict.Values {
		checkColorSpaceValue(doc, val, objNum, level, errs)
	}
}

func checkColorSpaceValue(doc core.View, csObj object.Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	checkColorSpaceValueSeen(doc, csObj, objNum, level, errs, make(map[int]bool))
}

func checkColorSpaceValueSeen(doc core.View, csObj object.Object, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	if r, ok := csObj.(object.IndirectRef); ok {
		if seen[r.Number] {
			return // cycle through an indirect color-space reference
		}
		seen[r.Number] = true
	}
	resolved := doc.Resolve(csObj)
	arr, ok := resolved.(object.Array)
	if !ok || len(arr) < 2 {
		return
	}

	csType, ok := arr[0].(object.Name)
	if !ok {
		return
	}

	switch csType {
	case "CalGray", "CalRGB", "Lab":
		if dict := doc.ResolveDict(arr[1]); dict != nil {
			checkCIEDictParams(doc, string(csType), dict, objNum, level, errs)
		}
	case "Indexed":
		// [/Indexed base hival lookup] — validate the base space too.
		checkColorSpaceValueSeen(doc, arr[1], objNum, level, errs, seen)
	case "Separation":
		// [/Separation name alternateSpace tintTransform]
		if len(arr) < 4 {
			rule := "6.2.4"
			if level == PDFA1b {
				rule = "6.2.3"
			}
			*errs = append(*errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "Separation color space array must have 4 elements",
				Object:  objNum,
			})
			return
		}
		// Check colorant name is not None for PDF/A-2b+ (it's reserved)
		if name, ok := arr[1].(object.Name); ok && name == "None" {
			// "None" is a special name in PDF 2.0 only
			if level != PDFA4 {
				*errs = append(*errs, ValidationError{
					Rule:    colourClause("spot", level),
					Level:   level,
					Message: "Separation colorant name /None is reserved",
					Object:  objNum,
				})
			}
		}
		// Check alternate color space is not a device space (for 2b/3b)
		checkAlternateCS(doc, arr[2], objNum, level, errs)

	case "DeviceN":
		// [/DeviceN names alternateSpace tintTransform ...]
		if len(arr) < 4 {
			rule := "6.2.4"
			if level == PDFA1b {
				rule = "6.2.3"
			}
			*errs = append(*errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "DeviceN color space array must have at least 4 elements",
				Object:  objNum,
			})
			return
		}
		// Check alternate color space
		checkAlternateCS(doc, arr[2], objNum, level, errs)

		// DeviceN colorant limit is an implementation limit that varies by part:
		// PDF/A-1 (PDF 1.4) caps DeviceN at 8 colorants; PDF/A-2 and PDF/A-3
		// (PDF 1.7, NChannel) raise it to 32; PDF/A-4 (PDF 2.0) has no such limit.
		maxColorants := 0
		rule := "6.2.4"
		switch level {
		case PDFA1b:
			maxColorants = 8
			rule = "6.2.3"
		case PDFA2b, PDFA3b:
			maxColorants = 32
		}
		if maxColorants > 0 {
			if namesArr, ok := doc.Resolve(arr[1]).(object.Array); ok && len(namesArr) > maxColorants {
				*errs = append(*errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("DeviceN color space has %d colorants, maximum is %d", len(namesArr), maxColorants),
					Object:  objNum,
				})
			}
		}

		// Get colorant names from the DeviceN array
		namesArr, namesOk := doc.Resolve(arr[1]).(object.Array)

		// Spot colorants require a Colorants dictionary with their
		// definitions (ISO 19005-2/-3/-4, 6.2.4.4); process colour names
		// need none.
		if level != PDFA1b && namesOk {
			hasSpot := false
			for _, nameObj := range namesArr {
				if name, ok := nameObj.(object.Name); ok && !isProcessColorant(name) {
					hasSpot = true
					break
				}
			}
			if hasSpot {
				hasColorants := false
				if len(arr) >= 5 {
					if attrDict := doc.ResolveDict(arr[4]); attrDict != nil {
						hasColorants = doc.ResolveDict(attrDict.Get("Colorants")) != nil
					}
				}
				if !hasColorants {
					*errs = append(*errs, ValidationError{
						Rule:    colourClause("spot", level),
						Level:   level,
						Message: "DeviceN color space with spot colorants must have a Colorants dictionary",
						Object:  objNum,
					})
				}
			}
		}

		// If there's a 5th element (attributes dict), check Colorants
		if len(arr) >= 5 {
			attrDict := doc.ResolveDict(arr[4])
			if attrDict != nil {
				colorantsRef := attrDict.Get("Colorants")
				if colorantsRef != nil {
					colorantsDict := doc.ResolveDict(colorantsRef)
					if colorantsDict != nil {
						// Check that each DeviceN colorant name has an entry in Colorants dict
						if namesOk {
							for _, nameObj := range namesArr {
								if name, ok := nameObj.(object.Name); ok {
									if colorantsDict.Get(name) == nil {
										rule := "6.2.4"
										if level == PDFA1b {
											rule = "6.2.3"
										}
										*errs = append(*errs, ValidationError{
											Rule:    rule,
											Level:   level,
											Message: fmt.Sprintf("DeviceN colorant /%s not found in Colorants dictionary", string(name)),
											Object:  objNum,
										})
									}
								}
							}
						}
						// Recursively check Colorant entries
						for _, cval := range colorantsDict.Values {
							checkColorSpaceValueSeen(doc, cval, objNum, level, errs, seen)
						}
					}
				}
			}
		}
	}
}

// checkCIEDictParams validates the parameter dictionary of a CalGray,
// CalRGB, or Lab colour space against ISO 32000-1 Tables 63-65: WhitePoint
// is required with Xw, Zw positive and Yw exactly 1.0; BlackPoint components
// must be non-negative; a Lab Range must be four numbers with min <= max.
func checkCIEDictParams(doc core.View, family string, dict *object.Dictionary, objNum int, level PDFALevel, errs *[]ValidationError) {
	rule := "6.2.4"
	if level == PDFA1b {
		rule = "6.2.3"
	}
	bad := func(format string, args ...interface{}) {
		*errs = append(*errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf("%s colour space: ", family) + fmt.Sprintf(format, args...),
			Object:  objNum,
		})
	}
	nums := func(v object.Object) ([]float64, bool) {
		arr, ok := doc.Resolve(v).(object.Array)
		if !ok {
			return nil, false
		}
		out := make([]float64, 0, len(arr))
		for _, el := range arr {
			switch n := doc.Resolve(el).(type) {
			case object.Integer:
				out = append(out, float64(n))
			case object.Real:
				out = append(out, float64(n))
			default:
				return nil, false
			}
		}
		return out, true
	}

	wp := dict.Get("WhitePoint")
	if wp == nil {
		bad("required /WhitePoint is missing")
	} else if vals, ok := nums(wp); !ok || len(vals) != 3 {
		bad("/WhitePoint must be an array of three numbers")
	} else if vals[0] <= 0 || vals[2] <= 0 || vals[1] != 1.0 {
		bad("/WhitePoint [%g %g %g] must have positive Xw and Zw and Yw equal to 1.0", vals[0], vals[1], vals[2])
	}

	if bp := dict.Get("BlackPoint"); bp != nil {
		if vals, ok := nums(bp); !ok || len(vals) != 3 {
			bad("/BlackPoint must be an array of three numbers")
		} else if vals[0] < 0 || vals[1] < 0 || vals[2] < 0 {
			bad("/BlackPoint components must be non-negative")
		}
	}

	if family == "Lab" {
		if r := dict.Get("Range"); r != nil {
			if vals, ok := nums(r); !ok || len(vals) != 4 {
				bad("/Range must be an array of four numbers")
			} else if vals[0] > vals[1] || vals[2] > vals[3] {
				bad("/Range minima must not exceed maxima")
			}
		}
	}

	if family == "CalGray" {
		if g := dict.Get("Gamma"); g != nil {
			gv, isNum := 0.0, false
			switch n := doc.Resolve(g).(type) {
			case object.Integer:
				gv, isNum = float64(n), true
			case object.Real:
				gv, isNum = float64(n), true
			}
			if !isNum || gv <= 0 {
				bad("/Gamma must be a positive number")
			}
		}
	}
}

// isProcessColorant reports whether a DeviceN colorant name refers to a
// process colour (or the reserved names), which needs no Colorants entry.
func isProcessColorant(name object.Name) bool {
	switch name {
	case "Cyan", "Magenta", "Yellow", "Black", "None", "All":
		return true
	}
	return false
}

// checkAlternateCS validates that an alternate color space in Separation/DeviceN
// is not a restricted space. For PDF/A-1b, device CS alternates are always forbidden
// (must be CIE-based). For 2b/3b/4, device alternates are handled by checkDeviceColorSpaces
// which verifies OutputIntent coverage.
func checkAlternateCS(doc core.View, altCS object.Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	checkAlternateCSSeen(doc, altCS, objNum, level, errs, make(map[int]bool))
}

func checkAlternateCSSeen(doc core.View, altCS object.Object, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	if r, ok := altCS.(object.IndirectRef); ok {
		if seen[r.Number] {
			return // cycle through an indirect alternate color-space reference
		}
		seen[r.Number] = true
	}
	resolved := doc.Resolve(altCS)

	if n, ok := resolved.(object.Name); ok {
		switch n {
		case "DeviceRGB", "DeviceCMYK", "DeviceGray":
			// For PDF/A-1b: a device alternate follows the same rule as
			// direct device color-space use — legal when a matching
			// OutputIntent covers it (ISO 19005-1, 6.2.3.2), forbidden
			// otherwise.
			if level == PDFA1b {
				covered := false
				if catalog := doc.Catalog(); catalog != nil {
					hasRGB, hasCMYK, hasGray := getOutputIntentCoverage(doc, catalog)
					switch n {
					case "DeviceRGB":
						covered = hasRGB
					case "DeviceCMYK":
						covered = hasCMYK
					case "DeviceGray":
						covered = hasGray || hasRGB || hasCMYK
					}
				}
				if !covered {
					*errs = append(*errs, ValidationError{
						Rule:    "6.2.3",
						Level:   level,
						Message: fmt.Sprintf("Separation/DeviceN alternate color space %s requires a matching OutputIntent", n),
						Object:  objNum,
					})
				}
			}
			// For 2b/3b/4: device alternates require OutputIntent coverage,
			// which is checked by checkDeviceColorSpaces via core.CheckCSForDevice.
		case "Pattern":
			rule := "6.2.4"
			if level == PDFA1b {
				rule = "6.2.3"
			}
			*errs = append(*errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "Separation/DeviceN alternate color space must not be /Pattern",
				Object:  objNum,
			})
		}
	}

	// If it's an array, recurse to check for nested Separation/DeviceN
	if arr, ok := resolved.(object.Array); ok && len(arr) >= 2 {
		if csType, ok := arr[0].(object.Name); ok {
			if csType == "Separation" || csType == "DeviceN" {
				// Nested Separation/DeviceN - check their alternates too
				if len(arr) >= 3 {
					checkAlternateCSSeen(doc, arr[2], objNum, level, errs, seen)
				}
			}
		}
	}
}

// --- XMP encoding helpers (FP-2) ---

// --- helpers ---

// --- ICCBased overprint and profile-identity rules (6.2.4.2 at 2b+/A-4) ---

// contentColorUsage summarizes the colour-relevant selections a content
// stream makes: fill/stroke colour space resource names (cs/CS) and
// ExtGState applications (gs).
type contentColorUsage struct {
	fillCS   map[string]bool
	strokeCS map[string]bool
	gsNames  map[string]bool
	// Whether any painting operation of each flavour occurs: setting a
	// stroke colour space that never strokes is not a use.
	paintsFill   bool
	paintsStroke bool
}

func scanContentColorUsage(cancel core.Canceler, data []byte) contentColorUsage {
	u := contentColorUsage{
		fillCS:   make(map[string]bool),
		strokeCS: make(map[string]bool),
		gsNames:  make(map[string]bool),
	}
	var lastName string
	core.ForEachContentToken(cancel, data, func(tok []byte, isName bool) {
		if isName {
			lastName = string(tok)
			return
		}
		switch string(tok) {
		case "cs":
			u.fillCS[lastName] = true
		case "CS":
			u.strokeCS[lastName] = true
		case "gs":
			u.gsNames[lastName] = true
		case "f", "F", "f*":
			u.paintsFill = true
		case "S", "s":
			u.paintsStroke = true
		case "B", "B*", "b", "b*":
			u.paintsFill = true
			u.paintsStroke = true
		case "Tj", "TJ", "'", "\"":
			// Text defaults to fill rendering mode.
			u.paintsFill = true
		}
	})
	return u
}

// iccCMYKProfile returns the profile stream when csVal is an ICCBased colour
// space with N=4, nil otherwise.
func iccCMYKProfile(doc core.View, csVal object.Object) *object.Stream {
	arr, ok := doc.Resolve(csVal).(object.Array)
	if !ok || len(arr) < 2 {
		return nil
	}
	if n, _ := arr[0].(object.Name); n != "ICCBased" {
		return nil
	}
	stream, ok := doc.Resolve(arr[1]).(*object.Stream)
	if !ok {
		return nil
	}
	if n, ok := stream.Dict.Get("N").(object.Integer); !ok || n != 4 {
		return nil
	}
	return stream
}

// iccProfileStream returns the profile stream of any ICCBased colour space.
func iccProfileStream(doc core.View, csVal object.Object) *object.Stream {
	arr, ok := doc.Resolve(csVal).(object.Array)
	if !ok || len(arr) < 2 {
		return nil
	}
	if n, _ := arr[0].(object.Name); n != "ICCBased" {
		return nil
	}
	stream, _ := doc.Resolve(arr[1]).(*object.Stream)
	return stream
}

// sameICCProfile reports whether two profile streams hold the same profile:
// the same object, or byte-identical data after zeroing the Profile ID field
// (ICC header bytes 84-99), which is what distinguishes an original from a
// copy whose MD5 was filled in.
func sameICCProfile(doc core.View, a, b *object.Stream) bool {
	if a == nil || b == nil {
		return false
	}
	if a == b {
		return true
	}
	da := core.ICCProfileData(a, doc.Limits)
	db := core.ICCProfileData(b, doc.Limits)
	if len(da) == 0 || len(da) != len(db) {
		return false
	}
	if len(da) >= 100 {
		// The ICC Profile ID (header bytes 84-99) is an MD5 of the profile.
		// When both profiles carry a non-zero Profile ID, they are the same
		// iff the IDs match — two profiles with different non-zero IDs are
		// distinct even if otherwise byte-identical. When either ID is zero
		// (not computed), fall back to comparing the content with the ID
		// field zeroed.
		ida, idb := da[84:100], db[84:100]
		if !allZero(ida) && !allZero(idb) {
			return bytes.Equal(ida, idb)
		}
		na := append([]byte(nil), da...)
		nb := append([]byte(nil), db...)
		for i := 84; i < 100; i++ {
			na[i], nb[i] = 0, 0
		}
		return bytes.Equal(na, nb)
	}
	return bytes.Equal(da, db)
}

// checkICCBasedUsageRules implements two content-level ICCBased rules:
//
//   - Overprint (ISO 19005-2/-4): when an ICCBased CMYK colour space is used
//     for a fill or stroke that overprints, overprint mode shall not be 1.
//   - Profile identity (ISO 19005-4, 6.2.4.2): an ICCBased colour space used
//     for rendering shall not embed the same profile as the current PDF/A
//     output intent or the current transparency blending colour space — the
//     device colour operators exist for exactly that case.
func checkICCBasedUsageRules(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	var errs []ValidationError
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		res := doc.Resources(page.Dict)
		if res == nil {
			continue
		}
		data := core.ContentStreamData(doc, page.Dict.Get("Contents"))
		if data == nil {
			continue
		}
		usage := scanContentColorUsage(doc.Cancel, data)

		// Accumulated overprint state from applied ExtGStates.
		opm1, opFill, opStroke := false, false, false
		if gsDict := doc.ResolveDict(res.Get("ExtGState")); gsDict != nil {
			for i, name := range gsDict.Keys {
				if !usage.gsNames[string(name)] {
					continue
				}
				gs := doc.ResolveDict(gsDict.Values[i])
				if gs == nil {
					continue
				}
				if v, ok := gs.Get("OPM").(object.Integer); ok && v == 1 {
					opm1 = true
				}
				strokeSet, strokeIsSet := gs.Get("OP").(object.Boolean)
				fillSet, fillIsSet := gs.Get("op").(object.Boolean)
				if strokeIsSet && bool(strokeSet) {
					opStroke = true
				}
				// op defaults to OP when absent (ISO 32000-1, Table 58).
				if fillIsSet && bool(fillSet) || !fillIsSet && strokeIsSet && bool(strokeSet) {
					opFill = true
				}
			}
		}

		csDict := doc.ResolveDict(res.Get("ColorSpace"))
		if csDict == nil {
			continue
		}
		checkOne := func(name string, stroke bool) {
			csVal := csDict.Get(object.Name(name))
			if csVal == nil {
				return
			}
			if cmyk := iccCMYKProfile(doc, csVal); cmyk != nil && opm1 {
				if (stroke && opStroke && usage.paintsStroke) || (!stroke && opFill && usage.paintsFill) {
					errs = append(errs, ValidationError{
						Rule:    colourClause("iccBased", level),
						Level:   level,
						Message: "overprint mode must not be 1 when an ICCBased CMYK colour space is used with overprinting",
						Object:  page.ObjNum,
					})
				}
			}
		}
		for name := range usage.fillCS {
			checkOne(name, false)
		}
		for name := range usage.strokeCS {
			checkOne(name, true)
		}
	}
	return errs
}

// --- JPEG2000 image rules (ISO 19005-2/-3, 6.2.8.3; -4, 6.2.8) ---

// jp2Info summarizes the JP2 header boxes of a JPXDecode stream.
type jp2Info struct {
	valid      bool
	nc         int  // ihdr number of components
	bpcRaw     byte // ihdr bits-per-component field (0xFF = per-component bpcc box)
	hasBPCC    bool
	colrMETH   []byte // METH of each colour specification box
	colrAPPROX []byte
	colrEnumCS []uint32 // EnumCS when METH==1, else 0
}

// parseJP2Header walks the box structure of a JP2 file far enough to read
// the image header (ihdr) and colour specification (colr) boxes.
func parseJP2Header(data []byte) jp2Info {
	var info jp2Info
	// A raw JPEG2000 codestream (SOC marker) carries no boxes.
	if len(data) < 8 || (data[0] == 0xFF && data[1] == 0x4F) {
		return info
	}
	var walk func(b []byte, depth int)
	walk = func(b []byte, depth int) {
		if depth > 4 {
			return
		}
		for len(b) >= 8 {
			lbox := uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
			tbox := string(b[4:8])
			header := uint64(8)
			if lbox == 1 {
				if len(b) < 16 {
					return
				}
				lbox = 0
				for _, by := range b[8:16] {
					lbox = lbox<<8 | uint64(by)
				}
				header = 16
			} else if lbox == 0 {
				lbox = uint64(len(b)) // box extends to end
			}
			if lbox < header || lbox > uint64(len(b)) {
				return
			}
			payload := b[header:lbox]
			switch tbox {
			case "jp2h":
				walk(payload, depth+1)
			case "ihdr":
				if len(payload) >= 10 {
					info.valid = true
					info.nc = int(payload[8])<<8 | int(payload[9])
					if len(payload) >= 11 {
						info.bpcRaw = payload[10]
					}
				}
			case "bpcc":
				info.hasBPCC = true
			case "colr":
				if len(payload) >= 3 {
					info.colrMETH = append(info.colrMETH, payload[0])
					info.colrAPPROX = append(info.colrAPPROX, payload[2])
					var enum uint32
					if payload[0] == 1 && len(payload) >= 7 {
						enum = uint32(payload[3])<<24 | uint32(payload[4])<<16 | uint32(payload[5])<<8 | uint32(payload[6])
					}
					info.colrEnumCS = append(info.colrEnumCS, enum)
				}
			}
			b = b[lbox:]
		}
	}
	walk(data, 0)
	return info
}

// checkJPXImages validates JPEG2000 image data against the PDF/A-2/-3/-4
// restrictions: 1/3/4 colour channels, bit depth 1-38, colour-specification
// method 1-3, permitted enumerated colour spaces, and a single authoritative
// colour specification when several are present.
func checkJPXImages(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // JPXDecode is forbidden outright at PDF/A-1 (6.1.10)
	}
	rule := imageClause("jpx", level)

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok || !hasFilter(stream, "JPXDecode") {
			continue
		}
		info := parseJP2Header(stream.Data)
		if !info.valid {
			continue
		}
		bad := func(format string, args ...interface{}) {
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: fmt.Sprintf(format, args...),
				Object:  num,
			})
		}

		if info.nc != 1 && info.nc != 3 && info.nc != 4 {
			bad("JPEG2000 image has %d colour channels; only 1, 3 or 4 are permitted", info.nc)
		}
		if info.bpcRaw != 0xFF {
			depth := int(info.bpcRaw&0x7F) + 1
			if depth < 1 || depth > 38 {
				bad("JPEG2000 image bit depth %d outside the permitted 1-38 range", depth)
			}
		}
		for i, meth := range info.colrMETH {
			if meth != 1 && meth != 2 && meth != 3 {
				bad("JPEG2000 colour specification METH %d is not 1, 2 or 3", meth)
			}
			if meth == 1 {
				switch info.colrEnumCS[i] {
				case 12, 16, 17, 18: // CMYK, sRGB, greyscale, sYCC
				default:
					bad("JPEG2000 enumerated colour space %d is not permitted", info.colrEnumCS[i])
				}
			}
		}
		// When several colour specifications exist, exactly one shall be
		// the authoritative one (APPROX 0x01).
		if len(info.colrMETH) > 1 && stream.Dict.Get("ColorSpace") == nil {
			approxOnes := 0
			for _, a := range info.colrAPPROX {
				if a == 1 {
					approxOnes++
				}
			}
			if approxOnes != 1 {
				bad("JPEG2000 image with %d colour specifications must mark exactly one with APPROX 1", len(info.colrMETH))
			}
		}
	}
	return errs
}

// allZero reports whether every byte is zero.
func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// exampleFindings collects at most one ValidationError per distinct rule and
// message. Several rules report a single representative example rather than
// every occurrence, and their candidates arrive from a range over doc.Objects,
// doc.Offsets or collectContentStreamData — Go maps, whose iteration order is
// randomised on every run. Keeping whichever candidate the range happened to
// yield first therefore named a different object each time the same file was
// validated. Keeping the numerically smallest object number instead is a total
// order over the candidates, so the report is reproducible. The choice is
// load-bearing, not incidental: reports are diffed run against run.
//
// Emission order is deliberately not part of the contract — ValidatePDFABytes
// sorts the concatenated findings before returning them.
type exampleFindings struct {
	idx  map[string]int // rule+message -> index into errs
	errs []ValidationError
}

// add records e, or — when a finding with the same rule and message is already
// held — lowers that finding's object number to e's when e's is smaller.
func (f *exampleFindings) add(e ValidationError) {
	key := e.Rule + "\x00" + e.Message
	if i, ok := f.idx[key]; ok {
		if e.Object < f.errs[i].Object {
			f.errs[i].Object = e.Object
		}
		return
	}
	if f.idx == nil {
		f.idx = make(map[string]int)
	}
	f.idx[key] = len(f.errs)
	f.errs = append(f.errs, e)
}
