package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// PDFALevel represents a PDF/A conformance level.
type PDFALevel int

const (
	PDFA1b PDFALevel = iota
	PDFA2b
	PDFA3b
	PDFA4
)

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
	default:
		return fmt.Sprintf("PDFALevel(%d)", int(l))
	}
}

// ValidationError describes a single PDF/A conformance violation.
type ValidationError struct {
	Rule    string    // e.g., "6.1.3" (ISO 19005 clause)
	Level   PDFALevel // the level that requires this rule
	Message string
	Object  int // object number, 0 if N/A
}

func (e ValidationError) Error() string {
	if e.Object != 0 {
		return fmt.Sprintf("[%s %s] object %d: %s", e.Level, e.Rule, e.Object, e.Message)
	}
	return fmt.Sprintf("[%s %s] %s", e.Level, e.Rule, e.Message)
}

// ValidatePDFA checks doc against the implemented rules for the given PDF/A
// level and returns the violations found. An empty result means "none of the
// implemented checks fired", not a guarantee of full conformance: the validator
// covers a subset of ISO 19005 (see the package README). Because it takes no
// raw bytes, it also skips every byte-level file-structure rule — use
// ValidatePDFABytes when you have the file bytes and want those too.
func ValidatePDFA(doc *Document, level PDFALevel) []ValidationError {
	return ValidatePDFABytes(doc, level, nil)
}

// runCheck runs one validation check, converting a panic into a reported
// violation instead of letting it crash the caller. The validator processes
// untrusted files, so a bug (or an adversarial structure) in one check must not
// take down the whole process. Stack overflows from unbounded recursion are
// fatal and cannot be recovered here; those are prevented at their source.
func runCheck(doc *Document, level PDFALevel, check func(*Document, PDFALevel) []ValidationError) (out []ValidationError) {
	defer func() {
		if r := recover(); r != nil {
			out = []ValidationError{{Rule: "internal", Level: level, Message: fmt.Sprintf("internal validator error: %v", r)}}
		}
	}()
	return check(doc, level)
}

// runByteCheck is runCheck for the byte-level checks, which have a different
// signature.
func runByteCheck(level PDFALevel, check func() []ValidationError) (out []ValidationError) {
	defer func() {
		if r := recover(); r != nil {
			out = []ValidationError{{Rule: "internal", Level: level, Message: fmt.Sprintf("internal validator error: %v", r)}}
		}
	}()
	return check()
}

// ValidatePDFABytes checks doc against the implemented rules for the given
// PDF/A level and returns the violations found. If rawData is non-nil, the
// byte-level file-structure rules run too (e.g. no data after %%EOF). An empty
// result means no implemented check fired, not a guarantee of full conformance
// (the validator covers a subset of ISO 19005).
func ValidatePDFABytes(doc *Document, level PDFALevel, rawData []byte) []ValidationError {
	var errs []ValidationError

	checks := []func(*Document, PDFALevel) []ValidationError{
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
		// Stream /Length correctness (6.1.6/6.1.7)
		checkStreamLength,
		// Object stream decodability (6.1.6/6.1.7)
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

	// Validate against a shallow copy of the Document so the per-run cache is
	// installed on the copy, never on the caller's Document. The copy shares
	// the (read-only during validation) Objects/Trailer/Offsets, so this is
	// cheap, and it lets a caller validate one Document concurrently — across
	// goroutines and at several levels at once — without a data race.
	//
	// Memoize expensive traversals (page-tree walks, content-stream
	// decompression) for the duration of this run: several checks walk the same
	// structures, and without the cache each content stream inflated up to three
	// times per page and the page tree was collected in ~8 checks.
	runDoc := *doc
	runDoc.valCache = &validationCache{
		pages:   make(map[int][]pageInfo),
		content: make(map[*Stream][]byte),
	}
	doc = &runDoc

	for _, check := range checks {
		errs = append(errs, runCheck(doc, level, check)...)
	}

	// Byte-level checks (require raw file data)
	if rawData != nil {
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkNoDataAfterEOF(rawData, level) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkFileStructureBytes(doc, level, rawData) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkLinearizedTrailerID(rawData, level) })...)
		errs = append(errs, runByteCheck(level, func() []ValidationError { return checkStreamLengthBytes(doc, level, rawData) })...)
	}

	// Checks iterate map-ordered doc.Objects, so their concatenated output
	// order is nondeterministic; sort for stable, diffable reports.
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Rule != errs[j].Rule {
			return errs[i].Rule < errs[j].Rule
		}
		if errs[i].Object != errs[j].Object {
			return errs[i].Object < errs[j].Object
		}
		return errs[i].Message < errs[j].Message
	})

	return errs
}

// validationCache memoizes traversals for one ValidatePDFABytes run. It is
// installed at the start of a run and dropped at the end, so documents may
// be mutated freely between validations. Validating the same Document from
// multiple goroutines concurrently is not supported.
type validationCache struct {
	pages           map[int][]pageInfo // page-tree object number -> pages
	content         map[*Stream][]byte // decoded content streams
	directAnnots    []annotOccurrence
	hasDirectAnnots bool
}

// --- File structure checks (6.1) ---

// Rule 6.1.3-2: Encrypt key must not be present in trailer dictionary.
func checkNoEncrypt(doc *Document, level PDFALevel) []ValidationError {
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
func checkFileID(doc *Document, level PDFALevel) []ValidationError {
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return []ValidationError{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer must contain /ID array",
		}}
	}
	arr, ok := idObj.(Array)
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
		if _, ok := elem.(String); !ok {
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
func checkHeader(doc *Document, level PDFALevel) []ValidationError {
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
func checkTrailerInfo(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil // only applies to PDF/A-4
	}

	infoRef := doc.Trailer.Get("Info")
	if infoRef == nil {
		return nil
	}

	catalog := getCatalog(doc)

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

func getCatalog(doc *Document) *Dictionary {
	rootRef := doc.Trailer.Get("Root")
	if rootRef == nil {
		return nil
	}
	return doc.ResolveDict(rootRef)
}

// Rule 6.7.2.1-1: Catalog requires Metadata stream with Type/Metadata, Subtype/XML, no Filter.
func checkMetadataStream(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
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

	stream, ok := metaObj.(*Stream)
	if !ok {
		return []ValidationError{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "/Metadata must be a stream",
		}}
	}

	var errs []ValidationError

	if t := stream.Dict.Get("Type"); t == nil || t != Name("Metadata") {
		errs = append(errs, ValidationError{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Type /Metadata",
		})
	}

	if st := stream.Dict.Get("Subtype"); st == nil || st != Name("XML") {
		errs = append(errs, ValidationError{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Subtype /XML",
		})
	}

	if stream.Dict.Get("Filter") != nil {
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

func checkOutputIntents(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	// PDF/A-4: validate page-level OutputIntents have /S /GTS_PDFA1
	// (must run even if no catalog-level OutputIntents)
	var errsPageLevel []ValidationError
	if level == PDFA4 {
		pages := collectPages(doc, catalog.Get("Pages"))
		for _, page := range pages {
			pageOIRef := page.dict.Get("OutputIntents")
			if pageOIRef == nil {
				continue
			}
			pageOIObj := doc.Resolve(pageOIRef)
			pageOIArr, ok := pageOIObj.(Array)
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
						Object:  page.objNum,
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

	arr, ok := oiObj.(Array)
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

		if _, ok := s.(Name); !ok {
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
		var profileRefs []Object
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
			ref0, ok0 := profileRefs[0].(IndirectRef)
			refJ, okJ := profileRefs[j].(IndirectRef)
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

	errs = append(errs, errsPageLevel...)
	return errs
}

func checkOutputIntentProfile(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return nil
	}

	oiObj := doc.Resolve(oiRef)
	arr, ok := oiObj.(Array)
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
		profStream, ok := profObj.(*Stream)
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
		nVal, ok := nObj.(Integer)
		if !ok {
			continue
		}
		// Decompress and check ICC profile header
		data, err := decodeStreamData(profStream)
		if err != nil {
			// Only treat a decode failure as a violation when we actually
			// support every filter on the stream. A legal profile encoded with
			// a filter we don't decode (e.g. ASCII85Decode, RunLengthDecode, or
			// a filter array) must not produce a false positive.
			if streamFiltersSupported(profStream) {
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

func checkNoCatalogAA(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 does not restrict /AA in catalog
	}
	catalog := getCatalog(doc)
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
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		if page.dict.Get("AA") != nil {
			errs = append(errs, ValidationError{
				Rule:    annotActionClause("catalogAA", level),
				Level:   level,
				Message: "page dictionary must not contain /AA (additional actions)",
				Object:  page.objNum,
			})
		}
	}
	return errs
}

func checkNoOCProperties(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA1b {
		return nil
	}
	catalog := getCatalog(doc)
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
func checkPermsDict(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // PDF/A-1b doesn't have Perms rules
	}
	catalog := getCatalog(doc)
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
		refArr, ok := doc.Resolve(sigDict.Get("Reference")).(Array)
		if !ok {
			if a, isArr := sigDict.Get("Reference").(Array); isArr {
				refArr = a
			}
		}
		for _, el := range refArr {
			refDict := doc.ResolveDict(el)
			if refDict == nil {
				if d, isDict := el.(*Dictionary); isDict {
					refDict = d
				} else {
					continue
				}
			}
			for _, forbidden := range []Name{"DigestLocation", "DigestMethod", "DigestValue"} {
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

// --- Stream checks (6.1.6) ---

// Rule 6.1.6.2-1: LZWDecode prohibited in all PDF/A levels.
func checkNoLZW(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if hasFilter(stream, "LZWDecode") {
			errs = append(errs, ValidationError{
				Rule:    "6.1.6",
				Level:   level,
				Message: "stream must not use /LZWDecode filter",
				Object:  num,
			})
		}
		// Check for non-standard filter names
		if badFilter := getNonStandardFilter(stream); badFilter != "" {
			errs = append(errs, ValidationError{
				Rule:    "6.1.6",
				Level:   level,
				Message: fmt.Sprintf("stream uses non-standard filter /%s", badFilter),
				Object:  num,
			})
		}
	}
	return errs
}

func isStandardFilter(name Name) bool {
	switch name {
	case "ASCIIHexDecode", "ASCII85Decode", "LZWDecode", "FlateDecode",
		"RunLengthDecode", "CCITTFaxDecode", "JBIG2Decode", "DCTDecode",
		"JPXDecode", "Crypt":
		return true
	}
	return false
}

func getNonStandardFilter(stream *Stream) string {
	f := stream.Dict.Get("Filter")
	if f == nil {
		return ""
	}
	if name, ok := f.(Name); ok {
		if !isStandardFilter(name) {
			return string(name)
		}
	}
	if arr, ok := f.(Array); ok {
		for _, elem := range arr {
			if name, ok := elem.(Name); ok && !isStandardFilter(name) {
				return string(name)
			}
		}
	}
	return ""
}

func hasFilter(stream *Stream, filterName string) bool {
	f := stream.Dict.Get("Filter")
	if f == nil {
		return false
	}
	if name, ok := f.(Name); ok {
		return string(name) == filterName
	}
	if arr, ok := f.(Array); ok {
		for _, elem := range arr {
			if name, ok := elem.(Name); ok && string(name) == filterName {
				return true
			}
		}
	}
	return false
}

// Rule 6.1.6.1-2: Stream dict cannot contain F, FFilter, or FDecodeParms.
func checkNoExternalStreams(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		for _, key := range []Name{"F", "FFilter", "FDecodeParms"} {
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
func checkFontsEmbedded(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError

	catalog := getCatalog(doc)
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
	usage := collectFontTextUsage(doc)
	exemptInvisible := make(map[*Dictionary]bool)
	for d, u := range usage {
		if !rendersVisibly(u) {
			exemptInvisible[d] = true
		}
	}

	// Fonts reached only through form XObjects, tiling patterns, or Type3
	// glyph procedures never appear in the page-tree /Resources that
	// collectFonts walks, so they would escape the embedding rule. Include the
	// executed-content fonts too (audit C21), deduped by dictionary pointer.
	checked := make(map[*Dictionary]bool, len(fonts))
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
func objNumForDict(doc *Document, dict *Dictionary) int {
	for num, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*Dictionary); ok && d == dict {
			return num
		}
	}
	return 0
}

// fontObjNum returns the object number of a font dictionary, or 0 if it is a
// direct dictionary with no indirect identity.
func fontObjNum(doc *Document, fontDict *Dictionary) int {
	return objNumForDict(doc, fontDict)
}

// checkOneFontEmbedded applies the 6.2.10 embedding rule to a single font
// dictionary.
func checkOneFontEmbedded(doc *Document, fontDict *Dictionary, objNum int, level PDFALevel) []ValidationError {
	subtypeName, _ := fontDict.Get("Subtype").(Name)

	// Type3 fonts define their glyphs with content streams, so they carry no
	// font program to embed. Type0 (composite) fonts DO require embedding —
	// via their descendant CIDFont's FontDescriptor, handled below.
	if subtypeName == "Type3" {
		return nil
	}

	fdRef := fontDict.Get("FontDescriptor")
	if fdRef == nil {
		// Composite fonts (Type0): check the descendant CIDFont's descriptor
		if dfArr, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(Array); ok && len(dfArr) > 0 {
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
	for _, key := range []Name{"FontFile", "FontFile2", "FontFile3"} {
		if _, ok := doc.Resolve(fd.Get(key)).(*Stream); ok {
			return nil
		}
	}
	baseFontName := ""
	if bn, ok := fontDict.Get("BaseFont").(Name); ok {
		baseFontName = string(bn)
	}
	return []ValidationError{{
		Rule:    fontClause("embed", level),
		Level:   level,
		Message: fmt.Sprintf("font %s must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)", baseFontName),
		Object:  objNum,
	}}
}

func collectFonts(doc *Document, pageTreeRef Object) map[int]*Dictionary {
	fonts := make(map[int]*Dictionary)
	collectFontsRecursive(doc, pageTreeRef, fonts, make(map[int]bool))
	return fonts
}

func collectFontsRecursive(doc *Document, ref Object, fonts map[int]*Dictionary, seen map[int]bool) {
	if r, ok := ref.(IndirectRef); ok {
		if seen[r.Number] {
			return // cycle in the page tree
		}
		seen[r.Number] = true
	}
	node := doc.ResolveDict(ref)
	if node == nil {
		return
	}

	nodeType, _ := node.Get("Type").(Name)

	if nodeType == "Pages" {
		kidsObj := doc.Resolve(node.Get("Kids"))
		if kids, ok := kidsObj.(Array); ok {
			for _, kid := range kids {
				collectFontsRecursive(doc, kid, fonts, seen)
			}
		}
		collectFontsFromResources(doc, node, fonts)
	} else if nodeType == "Page" {
		collectFontsFromResources(doc, node, fonts)
	}
}

func collectFontsFromResources(doc *Document, pageOrPages *Dictionary, fonts map[int]*Dictionary) {
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
		if iref, ok := fontRef.(IndirectRef); ok {
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
var allowedAnnotSubtypes = map[PDFALevel]map[Name]bool{
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
	pdfa2bAnnots := map[Name]bool{
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
	allowedAnnotSubtypes[PDFA1b] = map[Name]bool{
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
	dict *Dictionary
	num  int
}

// collectDirectAnnotations returns annotations written as direct dictionaries
// inside page /Annots arrays. These are not top-level objects, so the flat
// doc.Objects scans the annotation checks start from can never see them
// (audit A9); every annotation check runs over this list as well.
func collectDirectAnnotations(doc *Document) []annotOccurrence {
	if c := doc.valCache; c != nil && c.hasDirectAnnots {
		return c.directAnnots
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	var out []annotOccurrence
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		annots, ok := doc.Resolve(page.dict.Get("Annots")).(Array)
		if !ok {
			continue
		}
		for _, el := range annots {
			if dict, ok := el.(*Dictionary); ok {
				out = append(out, annotOccurrence{dict: dict, num: page.objNum})
			}
		}
	}
	if c := doc.valCache; c != nil {
		c.directAnnots = out
		c.hasDirectAnnots = true
	}
	return out
}

// resolveName resolves obj (following an indirect reference) and returns it as
// a Name. Rules must resolve before type-asserting: a value placed behind an
// indirect reference — e.g. /Subtype 12 0 R — would otherwise silently evade
// the check (audit C12).
func resolveName(doc *Document, obj Object) (Name, bool) {
	n, ok := doc.Resolve(obj).(Name)
	return n, ok
}

func checkAnnotationSubtypes(doc *Document, level PDFALevel) []ValidationError {
	allowed, ok := allowedAnnotSubtypes[level]
	if !ok {
		return nil
	}
	// PDF/A-4e permits 3D and RichMedia annotations (they carry the embedded
	// 3D/multimedia content that "e" stands for); plain PDF/A-4 forbids them.
	extra := map[Name]bool{}
	if level == PDFA4 && pdfaConformanceFlag(doc) == "E" {
		extra["3D"] = true
		extra["RichMedia"] = true
	}

	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
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
		if dict, ok := iobj.Value.(*Dictionary); ok && isAnnotation(dict) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// annotOpacity returns an annotation /CA value as a float, if it is numeric.
func annotOpacity(v Object) (float64, bool) {
	switch n := v.(type) {
	case Integer:
		return float64(n), true
	case Real:
		return float64(n), true
	}
	return 0, false
}

// Rule 6.3.2-1/2: Non-Popup annotations require F key; flags must have Print set,
// Hidden/Invisible/ToggleNoView/NoView clear.
func checkAnnotationFlags(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
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
		st, _ := dict.Get("Subtype").(Name)
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
		flags, ok := doc.Resolve(fObj).(Integer)
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
		if dict, ok := iobj.Value.(*Dictionary); ok && isAnnotation(dict) {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.3.3-1: Annotations need AP except Popup, Link, Projection, and zero-area rects.
func checkAnnotationAppearance(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
		st, _ := dict.Get("Subtype").(Name)

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
			if _, ok := doc.Resolve(apDict.Get("N")).(*Dictionary); !ok {
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
		if dict, ok := iobj.Value.(*Dictionary); ok && isAnnotation(dict) {
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
func annotFieldType(doc *Document, dict *Dictionary) Name {
	node := dict
	for hops := 0; node != nil && hops < 32; hops++ {
		if ft, ok := node.Get("FT").(Name); ok {
			return ft
		}
		node = doc.ResolveDict(node.Get("Parent"))
	}
	return ""
}

func isZeroAreaRect(obj Object) bool {
	arr, ok := obj.(Array)
	if !ok || len(arr) != 4 {
		return false
	}
	vals := make([]float64, 4)
	for i, elem := range arr {
		switch v := elem.(type) {
		case Integer:
			vals[i] = float64(v)
		case Real:
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
func checkWidgetNoAction(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
		st, _ := dict.Get("Subtype").(Name)
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
		if dict, ok := iobj.Value.(*Dictionary); ok {
			check(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.4.2-1: AcroForm dictionary cannot contain XFA key.
func checkNoXFA(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
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
func checkNeedAppearances(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
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
	if b, ok := na.(Boolean); ok && bool(b) {
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
func isForbiddenAction(s Name, level PDFALevel, conformance string) bool {
	// Universally forbidden across all PDF/A levels:
	universallyForbidden := map[Name]bool{
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
		forbidden123 := map[Name]bool{
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
		forbidden4 := map[Name]bool{
			"SetOCGState": true,
			"GoTo3DView":  true,
			"SetState":    true,
			"NOP":         true,
		}
		return forbidden4[s]
	}
	return false
}

func checkNoForbiddenActions(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError

	// PDF/A-4e relaxes a couple of 3D/multimedia actions; the conformance flag
	// selects that behaviour. Computed once (it decodes the XMP packet).
	conformance := ""
	if level == PDFA4 {
		conformance = pdfaConformanceFlag(doc)
	}

	// Check catalog /OpenAction
	catalog := getCatalog(doc)
	if catalog != nil {
		oaRef := catalog.Get("OpenAction")
		if oaRef != nil {
			errs = append(errs, checkActionObject(doc, oaRef, 0, level, conformance)...)
		}
	}

	// Check all objects for /A and action dictionaries
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}

		// Check /A (action) in any dictionary
		if aRef := dict.Get("A"); aRef != nil {
			errs = append(errs, checkActionObject(doc, aRef, num, level, conformance)...)
		}

		// Check if the object itself is an action dict (has /S and /Type=Action or no /Type)
		if s, ok := dict.Get("S").(Name); ok {
			typeObj := dict.Get("Type")
			isAction := typeObj == nil || typeObj == Name("Action")
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
		if actionDict, ok := a.dict.Get("A").(*Dictionary); ok {
			errs = append(errs, checkActionObject(doc, actionDict, a.num, level, conformance)...)
		}
	}

	return errs
}

func checkActionObject(doc *Document, ref Object, objNum int, level PDFALevel, conformance string) []ValidationError {
	var errs []ValidationError
	checkActionChain(doc, ref, objNum, level, conformance, &errs, make(map[*Dictionary]bool))
	return errs
}

// checkActionChain validates one action dictionary and follows its /Next
// entry (a single action or an array of actions), which previous versions
// ignored entirely — a legal action whose /Next launches JavaScript passed.
func checkActionChain(doc *Document, ref Object, objNum int, level PDFALevel, conformance string, errs *[]ValidationError, seen map[*Dictionary]bool) {
	// ref might be an action dict or an array (for OpenAction destination)
	actionDict := doc.ResolveDict(ref)
	if actionDict == nil || seen[actionDict] {
		return // destination array, unresolvable, or a /Next cycle
	}
	seen[actionDict] = true

	if s, ok := actionDict.Get("S").(Name); ok && isForbiddenAction(s, level, conformance) {
		*errs = append(*errs, ValidationError{
			Rule:    annotActionClause("forbidden", level),
			Level:   level,
			Message: fmt.Sprintf("forbidden action type /%s", string(s)),
			Object:  objNum,
		})
	}

	switch next := doc.Resolve(actionDict.Get("Next")).(type) {
	case *Dictionary:
		checkActionChain(doc, next, objNum, level, conformance, errs, seen)
	case Array:
		for _, el := range next {
			checkActionChain(doc, el, objNum, level, conformance, errs, seen)
		}
	}
}

// Rule 6.6.1-2: Named actions limited to NextPage, PrevPage, FirstPage, LastPage.
func checkNamedActions(doc *Document, level PDFALevel) []ValidationError {
	allowedNames := map[string]bool{
		"NextPage":  true,
		"PrevPage":  true,
		"FirstPage": true,
		"LastPage":  true,
	}

	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
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
		if dict, ok := iobj.Value.(*Dictionary); ok {
			check(dict, num)
		}
	}
	// Direct annotations may carry direct action dictionaries that never
	// appear as top-level objects (an indirect /A is already covered above).
	for _, a := range collectDirectAnnotations(doc) {
		if actionDict, ok := a.dict.Get("A").(*Dictionary); ok {
			check(actionDict, a.num)
		}
	}
	return errs
}

// Rule 6.6.3-1: Widget/FormField AA is level-gated.
// For PDF/A-1b/2b/3b: no /AA on widgets or form fields.
// For PDF/A-4: AA allowed on widgets/form fields (trigger events).
// Non-widget AA (doc/page/annot) keys restricted to: E, X, D, U, Fo, Bl.
func checkAnnotationAA(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 gates trigger events per-event; see checkA4TriggerEvents
	}

	// ISO 19005-1 6.5.3 / 19005-2 6.3.3: an annotation dictionary shall not
	// contain the AA key — for ANY annotation, not only widgets/form fields.
	var errs []ValidationError
	check := func(dict *Dictionary, num int) {
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
		if dict, ok := iobj.Value.(*Dictionary); ok && (isAnnotation(dict) || isWidgetOrField(dict)) {
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
// the /Rect that isAnnotation looks for.
func isWidgetOrField(dict *Dictionary) bool {
	if st, ok := dict.Get("Subtype").(Name); ok && st == "Widget" {
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

func checkMetadataVersion(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
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

	stream, ok := metaObj.(*Stream)
	if !ok {
		return nil
	}

	xmp := decodeXMPToUTF8(stream.Data)
	var errs []ValidationError

	// Check pdfaid namespace URI
	if strings.Contains(xmp, "pdfaid:") {
		if !strings.Contains(xmp, `xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"`) {
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

	part := extractXMPValue(xmp, "pdfaid:part")
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
		conf := extractXMPValue(xmp, "pdfaid:conformance")
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
			conf := extractXMPValue(xmp, "pdfaid:conformance")
			if conf != "F" && conf != "E" {
				errs = append(errs, ValidationError{
					Rule:    metadataClause("version", level),
					Level:   level,
					Message: fmt.Sprintf("PDF/A-4 pdfaid:conformance must be absent, F, or E, got %q", conf),
				})
			}
		}

		// Check pdfaid:rev must be "2020" for PDF/A-4
		rev := extractXMPValue(xmp, "pdfaid:rev")
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

// extractXMPValue extracts a simple value from XMP for a given key.
// Handles both <key>value</key> and key="value" attribute forms.
func extractXMPValue(xmp, key string) string {
	// Try element form: <key>value</key>
	openTag := "<" + key + ">"
	closeTag := "</" + key + ">"
	if idx := strings.Index(xmp, openTag); idx >= 0 {
		start := idx + len(openTag)
		if end := strings.Index(xmp[start:], closeTag); end >= 0 {
			return strings.TrimSpace(xmp[start : start+end])
		}
	}

	// Try attribute form: key="value" or key='value' (both legal XML).
	for _, q := range []byte{'"', '\''} {
		attrPrefix := key + "=" + string(q)
		if idx := strings.Index(xmp, attrPrefix); idx >= 0 {
			start := idx + len(attrPrefix)
			if end := bytes.IndexByte([]byte(xmp[start:]), q); end >= 0 {
				return xmp[start : start+end]
			}
		}
	}

	return ""
}

// --- Transparency checks (PDFA-1b only) ---

func checkNoTransparency(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA1b {
		return nil
	}

	var errs []ValidationError

	// Check for page-level transparency Groups (forbidden in PDF/A-1b)
	catalog := getCatalog(doc)
	if catalog != nil {
		pages := collectPages(doc, catalog.Get("Pages"))
		for _, page := range pages {
			groupRef := page.dict.Get("Group")
			if groupRef == nil {
				continue
			}
			groupDict := doc.ResolveDict(groupRef)
			if groupDict == nil {
				continue
			}
			s, _ := groupDict.Get("S").(Name)
			if s == "Transparency" {
				errs = append(errs, ValidationError{
					Rule:    "6.4",
					Level:   level,
					Message: "page must not have /Group with /S /Transparency (PDF/A-1b forbids transparency)",
					Object:  page.objNum,
				})
			}
		}
	}

	// Image soft masks and form transparency groups are equally forbidden
	// (ISO 19005-1 6.4). The ExtGState scan below only sees the /SMask
	// graphics-state parameter, so walk page resources for the XObject-level
	// signals too.
	if catalog != nil {
		seen := map[*Dictionary]bool{}
		for _, page := range collectPages(doc, catalog.Get("Pages")) {
			find1bTransparencyXObjects(doc, page.dict, level, seen, &errs)
		}
	}

	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		gs := entry.dict
		objNum := entry.objNum

		smask := gs.Get("SMask")
		if smask != nil {
			if n, ok := smask.(Name); ok && n == "None" {
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
			if n, ok := bm.(Name); ok {
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

		for _, key := range []Name{"CA", "ca"} {
			v := gs.Get(key)
			if v != nil {
				val := 1.0
				switch tv := v.(type) {
				case Real:
					val = float64(tv)
				case Integer:
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
	dict   *Dictionary
	objNum int
}

// collectAllExtGState finds all ExtGState dictionaries by scanning Resources/ExtGState
// in all pages, Form XObjects, and Type3 fonts. This avoids relying on the optional
// /Type key which many ExtGState objects don't have.
func collectAllExtGState(doc *Document) []extGStateEntry {
	seen := make(map[*Dictionary]bool)
	var entries []extGStateEntry

	addFromResources := func(res *Dictionary, fallbackObjNum int) {
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
			if iref, ok := val.(IndirectRef); ok {
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

	// Scan all objects for Resources dicts (pages, Form XObjects, Type3 fonts)
	for num, iobj := range doc.Objects {
		switch v := iobj.Value.(type) {
		case *Dictionary:
			resRef := v.Get("Resources")
			if resRef != nil {
				res := doc.ResolveDict(resRef)
				if res != nil {
					addFromResources(res, num)
				}
			}
		case *Stream:
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

func checkNoAlternateImages(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, ok := stream.Dict.Get("Subtype").(Name); ok && st == "Image" {
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
func checkInterpolate(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, ok := stream.Dict.Get("Subtype").(Name); ok && st == "Image" {
			interpObj := stream.Dict.Get("Interpolate")
			if interpObj != nil {
				if b, ok := interpObj.(Boolean); ok && bool(b) {
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
func checkNoOPI(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, ok := stream.Dict.Get("Subtype").(Name); ok && (st == "Image" || st == "Form") {
			if stream.Dict.Get("OPI") != nil {
				errs = append(errs, ValidationError{
					Rule:    imageClause("image", level),
					Level:   level,
					Message: "XObject must not have /OPI",
					Object:  num,
				})
			}
		}
	}
	return errs
}

// --- Catalog version check (MR-3) ---

// Rule 6.1.12: PDF/A-4 catalog /Version must match pattern 2.N.
func checkCatalogVersion(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}

	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	versionObj := catalog.Get("Version")
	if versionObj == nil {
		return nil
	}

	vName, ok := versionObj.(Name)
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
func checkFontSubsets(doc *Document, level PDFALevel) []ValidationError {
	// CharSet/CIDSet PRESENCE is only required by 19005-1: the veraPDF
	// corpus passes a PDF/A-2 subset CIDFont without /CIDSet (Part 2 only
	// constrains the sets when present).
	if level != PDFA1b {
		return nil
	}

	catalog := getCatalog(doc)
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
		subtype, _ := fontDict.Get("Subtype").(Name)
		baseFont, _ := fontDict.Get("BaseFont").(Name)

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
			dfArr, ok := dfObj.(Array)
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

func getFontDescriptor(doc *Document, fontDict *Dictionary) *Dictionary {
	fdRef := fontDict.Get("FontDescriptor")
	if fdRef == nil {
		return nil
	}
	return doc.ResolveDict(fdRef)
}

// --- ExtGState checks (MR-1) ---

// Rule 6.2.5: ExtGState forbidden keys for PDF/A-2b/3b/4.
func checkExtGState(doc *Document, level PDFALevel) []ValidationError {
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
			if n, ok := tr2.(Name); !ok || n != "Default" {
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
		if ri, ok := doc.Resolve(dict.Get("RI")).(Name); ok && !standardRenderingIntents[string(ri)] {
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
				if n, ok := bm.(Name); ok {
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
func isValidBlendMode(bm Name) bool {
	switch bm {
	case "Normal", "Compatible", "Multiply", "Screen", "Overlay",
		"Darken", "Lighten", "ColorDodge", "ColorBurn",
		"HardLight", "SoftLight", "Difference", "Exclusion",
		"Hue", "Saturation", "Color", "Luminosity":
		return true
	}
	return false
}

func checkHalftoneErrors(doc *Document, htRef Object, objNum int, level PDFALevel, rule string, errs *[]ValidationError) {
	htDict := doc.ResolveDict(htRef)
	if htDict == nil {
		return
	}

	if htType := htDict.Get("HalftoneType"); htType != nil {
		if intVal, ok := htType.(Integer); ok {
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
func checkInfoXMPConsistency(doc *Document, level PDFALevel) []ValidationError {
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

	catalog := getCatalog(doc)
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
	stream, ok := metaObj.(*Stream)
	if !ok {
		return nil
	}
	xmp := decodeXMPToUTF8(stream.Data)

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
		raw := infoDict.Get(Name(p.infoKey))
		if raw == nil {
			continue
		}
		// An Info entry that is an indirect object must resolve to a string
		// (ISO 19005-1 6.7.3): a non-string value is itself a violation.
		resolved := doc.Resolve(raw)
		if _, isNull := resolved.(Null); isNull || resolved == nil {
			continue // an indirect null value is equivalent to absence
		}
		strVal, isStr := resolved.(String)
		if !isStr {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("Info /%s is not a string value", p.infoKey),
			})
			continue
		}
		infoVal := decodePDFTextString(strVal.Value)
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
			xmpVal = extractXMPValue(xmp, p.xmpKey)
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

func getInfoString(info *Dictionary, key string) string {
	obj := info.Get(Name(key))
	if obj == nil {
		return ""
	}
	if s, ok := obj.(String); ok {
		return decodePDFTextString(s.Value)
	}
	return ""
}

// decodePDFTextString converts a PDF text string to UTF-8. Text strings are
// either UTF-16BE with a BOM (PDF 2.0 adds UTF-8 with a BOM) or
// PDFDocEncoded; comparing raw bytes against UTF-8 XMP values made every
// UTF-16 Info entry "inconsistent" with its metadata counterpart.
func decodePDFTextString(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		// UTF-16BE
		u := b[2:]
		var sb strings.Builder
		for i := 0; i+1 < len(u); i += 2 {
			r := rune(u[i])<<8 | rune(u[i+1])
			if r >= 0xD800 && r <= 0xDBFF && i+3 < len(u) {
				lo := rune(u[i+2])<<8 | rune(u[i+3])
				if lo >= 0xDC00 && lo <= 0xDFFF {
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					i += 2
				}
			}
			sb.WriteRune(r)
		}
		return sb.String()
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return string(b[3:]) // UTF-8 with BOM (PDF 2.0)
	}
	// PDFDocEncoding matches ASCII in the printable range; pass through.
	return string(b)
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
		return extractXMPValue(xmp, key)
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
func checkTransparencyBlending(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // PDF/A-1b prohibits transparency entirely
	}

	var errs []ValidationError

	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return nil
	}

	pages := collectPages(doc, pagesRef)
	for _, page := range pages {
		if !pageUsesTransparency(doc, page.dict) {
			continue
		}

		groupRef := page.dict.Get("Group")
		if groupRef == nil {
			// Check if the requirement can be relaxed
			if transparencyGroupNotRequired(doc, catalog, page.dict, level) {
				continue
			}
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "page using transparency must have /Group with /S /Transparency",
				Object:  page.objNum,
			})
			continue
		}
		groupDict := doc.ResolveDict(groupRef)
		if groupDict == nil {
			continue
		}

		s, _ := groupDict.Get("S").(Name)
		if s != "Transparency" {
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "page /Group must have /S /Transparency",
				Object:  page.objNum,
			})
			continue
		}
		if groupDict.Get("CS") == nil {
			// For PDF/A-4, OutputIntents can provide the blending CS implicitly
			if !transparencyGroupNotRequired(doc, catalog, page.dict, level) {
				errs = append(errs, ValidationError{
					Rule:    "6.2.4",
					Level:   level,
					Message: "page transparency group must have /CS (color space)",
					Object:  page.objNum,
				})
			}
		}
	}

	return errs
}

// transparencyGroupNotRequired checks if the transparency /Group requirement
// can be relaxed for a page. For PDF/A-4, OutputIntents provide implicit
// blending CS. For PDF/A-2b/3b, DefaultCS coverage can substitute.
func transparencyGroupNotRequired(doc *Document, catalog *Dictionary, page *Dictionary, level PDFALevel) bool {
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
		hasDefRGB, hasDefCMYK, hasDefGray := getDefaultColorSpaces(doc, page)
		usesRGB, usesCMYK, usesGray := scanPageForDeviceCS(doc, page)
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

// pageUsesTransparency checks if a page's resources reference transparency features.
// It checks ExtGState entries for CA/ca != 1.0, BM != Normal/Compatible, and SMask != None,
// and also recurses into Form XObjects and Type3 font resources.
func pageUsesTransparency(doc *Document, page *Dictionary) bool {
	// A page with a transparency Group is itself a transparency feature
	if groupRef := page.Get("Group"); groupRef != nil {
		groupDict := doc.ResolveDict(groupRef)
		if groupDict != nil {
			s, _ := groupDict.Get("S").(Name)
			if s == "Transparency" {
				return true
			}
		}
	}

	seen := make(map[*Dictionary]bool)
	if resourcesUseTransparency(doc, page, seen) {
		return true
	}
	// Check annotations on this page for transparency features
	annotsRef := page.Get("Annots")
	if annotsRef == nil {
		return false
	}
	annotsObj := doc.Resolve(annotsRef)
	annotsArr, ok := annotsObj.(Array)
	if !ok {
		return false
	}
	for _, annotRef := range annotsArr {
		annotDict := doc.ResolveDict(annotRef)
		if annotDict == nil {
			continue
		}
		// Check /BM on annotation itself
		if bm := annotDict.Get("BM"); bm != nil {
			if n, ok := bm.(Name); ok && n != "Normal" && n != "Compatible" {
				return true
			}
		}
		// Check /CA or /ca on annotation
		for _, key := range []Name{"CA", "ca"} {
			if v := annotDict.Get(key); v != nil {
				fval := 1.0
				switch tv := v.(type) {
				case Real:
					fval = float64(tv)
				case Integer:
					fval = float64(tv)
				}
				if math.Abs(fval-1.0) > 1e-6 {
					return true
				}
			}
		}
		// Check appearance streams for transparency
		ap := annotDict.Get("AP")
		if ap == nil {
			continue
		}
		apDict := doc.ResolveDict(ap)
		if apDict == nil {
			continue
		}
		// Check N, R, D appearance entries
		for _, apKey := range []Name{"N", "R", "D"} {
			apEntry := apDict.Get(apKey)
			if apEntry == nil {
				continue
			}
			// Could be a stream directly or a dict of states
			apObj := doc.Resolve(apEntry)
			switch v := apObj.(type) {
			case *Stream:
				if resourcesUseTransparency(doc, &v.Dict, seen) {
					return true
				}
				// Check if the appearance stream has its own transparency group
				if v.Dict.Get("Group") != nil {
					groupDict := doc.ResolveDict(v.Dict.Get("Group"))
					if groupDict != nil {
						s, _ := groupDict.Get("S").(Name)
						if s == "Transparency" {
							return true
						}
					}
				}
			case *Dictionary:
				// Dict of appearance states (e.g., /N << /Yes 12 0 R /Off 13 0 R >>)
				for _, stateVal := range v.Values {
					stateObj := doc.Resolve(stateVal)
					if stateStream, ok := stateObj.(*Stream); ok {
						if resourcesUseTransparency(doc, &stateStream.Dict, seen) {
							return true
						}
						if stateStream.Dict.Get("Group") != nil {
							groupDict := doc.ResolveDict(stateStream.Dict.Get("Group"))
							if groupDict != nil {
								s, _ := groupDict.Get("S").(Name)
								if s == "Transparency" {
									return true
								}
							}
						}
					}
				}
			}
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
func find1bTransparencyXObjects(doc *Document, container *Dictionary, level PDFALevel, seen map[*Dictionary]bool, errs *[]ValidationError) {
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
			stream, ok := doc.Resolve(val).(*Stream)
			if !ok {
				continue
			}
			num := resolveObjNum(doc, val)
			switch subtype, _ := stream.Dict.Get("Subtype").(Name); subtype {
			case "Image":
				if sm := stream.Dict.Get("SMask"); sm != nil {
					if n, ok := sm.(Name); !ok || n != "None" {
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
					if s, _ := g.Get("S").(Name); s == "Transparency" {
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
			if stream, ok := doc.Resolve(val).(*Stream); ok {
				find1bTransparencyXObjects(doc, &stream.Dict, level, seen, errs)
			}
		}
	}

	if fontDict := doc.ResolveDict(res.Get("Font")); fontDict != nil {
		for _, val := range fontDict.Values {
			if fd := doc.ResolveDict(val); fd != nil {
				if st, _ := fd.Get("Subtype").(Name); st == "Type3" {
					find1bTransparencyXObjects(doc, fd, level, seen, errs)
				}
			}
		}
	}
}

func resourcesUseTransparency(doc *Document, container *Dictionary, seen map[*Dictionary]bool) bool {
	if seen[container] {
		return false
	}
	seen[container] = true

	resRef := container.Get("Resources")
	if resRef == nil {
		return false
	}
	res := doc.ResolveDict(resRef)
	if res == nil {
		return false
	}

	// Check ExtGState resources for transparency indicators
	if extGStateUsesTransparency(doc, res) {
		return true
	}

	// Recurse into Form XObjects
	xobjRef := res.Get("XObject")
	if xobjRef != nil {
		xobjDict := doc.ResolveDict(xobjRef)
		if xobjDict != nil {
			for _, val := range xobjDict.Values {
				obj := doc.Resolve(val)
				stream, ok := obj.(*Stream)
				if !ok {
					continue
				}
				subtype, _ := stream.Dict.Get("Subtype").(Name)
				if subtype == "Form" {
					// If the Form XObject has its own transparency Group,
					// it manages its own compositing - don't propagate to page level.
					if stream.Dict.Get("Group") != nil {
						groupDict := doc.ResolveDict(stream.Dict.Get("Group"))
						if groupDict != nil {
							s, _ := groupDict.Get("S").(Name)
							if s == "Transparency" {
								continue // self-contained transparency group
							}
						}
					}
					// Recurse into Form XObject Resources
					if resourcesUseTransparency(doc, &stream.Dict, seen) {
						return true
					}
				} else if subtype == "Image" {
					// Image XObjects with /SMask use transparency
					if stream.Dict.Get("SMask") != nil {
						return true
					}
				}
			}
		}
	}

	// Recurse into Type3 font resources
	fontRef := res.Get("Font")
	if fontRef != nil {
		fontDict := doc.ResolveDict(fontRef)
		if fontDict != nil {
			for _, val := range fontDict.Values {
				fd := doc.ResolveDict(val)
				if fd == nil {
					continue
				}
				subtype, _ := fd.Get("Subtype").(Name)
				if subtype == "Type3" {
					if resourcesUseTransparency(doc, fd, seen) {
						return true
					}
				}
			}
		}
	}

	// Recurse into tiling patterns
	patRef := res.Get("Pattern")
	if patRef != nil {
		patDict := doc.ResolveDict(patRef)
		if patDict != nil {
			for _, val := range patDict.Values {
				obj := doc.Resolve(val)
				stream, ok := obj.(*Stream)
				if !ok {
					continue
				}
				// Tiling patterns (PatternType 1) have their own Resources
				if resourcesUseTransparency(doc, &stream.Dict, seen) {
					return true
				}
			}
		}
	}

	return false
}

func extGStateUsesTransparency(doc *Document, res *Dictionary) bool {
	gsRef := res.Get("ExtGState")
	if gsRef == nil {
		return false
	}
	gsDict := doc.ResolveDict(gsRef)
	if gsDict == nil {
		return false
	}
	for _, val := range gsDict.Values {
		gs := doc.ResolveDict(val)
		if gs == nil {
			continue
		}
		// Check CA/ca for non-opaque values
		for _, key := range []Name{"CA", "ca"} {
			v := gs.Get(key)
			if v != nil {
				fval := 1.0
				switch tv := v.(type) {
				case Real:
					fval = float64(tv)
				case Integer:
					fval = float64(tv)
				}
				if math.Abs(fval-1.0) > 1e-6 {
					return true
				}
			}
		}
		// Non-Normal blend modes are transparency features
		if bm := gs.Get("BM"); bm != nil {
			if n, ok := bm.(Name); ok && n != "Normal" && n != "Compatible" {
				return true
			}
		}
		// Check SMask for non-None values
		if smask := gs.Get("SMask"); smask != nil {
			if n, ok := smask.(Name); !ok || n != "None" {
				return true
			}
		}
	}
	return false
}

type pageInfo struct {
	dict   *Dictionary
	objNum int
}

func collectPages(doc *Document, pageTreeRef Object) []pageInfo {
	c := doc.valCache
	ref, isRef := pageTreeRef.(IndirectRef)
	if c != nil && isRef {
		if pages, ok := c.pages[ref.Number]; ok {
			return pages
		}
	}
	var pages []pageInfo
	collectPagesRecursive(doc, pageTreeRef, &pages, make(map[int]bool))
	if c != nil && isRef {
		c.pages[ref.Number] = pages
	}
	return pages
}

func collectPagesRecursive(doc *Document, ref Object, pages *[]pageInfo, seen map[int]bool) {
	objNum := 0
	if iref, ok := ref.(IndirectRef); ok {
		objNum = iref.Number
		if seen[objNum] {
			return // cycle in the page tree
		}
		seen[objNum] = true
	}
	node := doc.ResolveDict(ref)
	if node == nil {
		return
	}
	nodeType, _ := node.Get("Type").(Name)
	if nodeType == "Pages" {
		kidsObj := doc.Resolve(node.Get("Kids"))
		if kids, ok := kidsObj.(Array); ok {
			for _, kid := range kids {
				collectPagesRecursive(doc, kid, pages, seen)
			}
		}
	} else if nodeType == "Page" {
		*pages = append(*pages, pageInfo{dict: node, objNum: objNum})
	}
}

// --- Embedded files check (MR-4) ---

// Rule 6.1.12: Embedded file restrictions.
func checkEmbeddedFiles(doc *Document, level PDFALevel) []ValidationError {
	// PDF/A-1 (ISO 19005-1, 6.1.11) forbids embedded files outright: no
	// file specification may carry /EF, wherever it lives — not only in the
	// catalog's Names tree.
	if level == PDFA1b {
		var errs []ValidationError
		for num, iobj := range doc.Objects {
			if dict, ok := iobj.Value.(*Dictionary); ok && dict.Get("EF") != nil {
				errs = append(errs, ValidationError{
					Rule:    "6.1.11",
					Level:   level,
					Message: "file specification must not contain /EF (embedded files are forbidden in PDF/A-1)",
					Object:  num,
				})
			}
		}
		catalog := getCatalog(doc)
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
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	return checkEmbeddedFileSpecs(doc, level, catalog)
}

func checkEmbeddedFileSpecs(doc *Document, level PDFALevel, catalog *Dictionary) []ValidationError {
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
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		// A file specification is not required to carry /Type /Filespec;
		// anything holding an /EF is acting as one.
		t, hasType := dict.Get("Type").(Name)
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
					stream, ok := doc.Resolve(val).(*Stream)
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
					} else if name, ok := st.(Name); ok {
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
func documentHasEmbeddedFiles(doc *Document, catalog *Dictionary) bool {
	if namesDict := doc.ResolveDict(catalog.Get("Names")); namesDict != nil {
		if namesDict.Get("EmbeddedFiles") != nil {
			return true
		}
	}
	for _, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*Dictionary); ok && dict.Get("EF") != nil {
			return true
		}
	}
	return false
}

func documentHasAF(doc *Document) bool {
	catalog := getCatalog(doc)
	if catalog != nil && catalog.Get("AF") != nil {
		return true
	}
	for _, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*Dictionary); ok {
			if dict.Get("AF") != nil {
				return true
			}
		}
		if stream, ok := iobj.Value.(*Stream); ok {
			if stream.Dict.Get("AF") != nil {
				return true
			}
		}
	}
	return false
}

// --- Optional content check (MR-5) ---

// Rule 6.1.13: Optional content requirements for PDF/A-4.
func checkOptionalContent(doc *Document, level PDFALevel) []ValidationError {
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

	catalog := getCatalog(doc)
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
	ocgsArr, ok := doc.Resolve(ocgsRef).(Array)
	if !ok {
		return errs
	}

	names := make(map[string]bool)
	configs := []Object{dRef}
	if configsRef := ocpDict.Get("Configs"); configsRef != nil {
		if arr, ok := doc.Resolve(configsRef).(Array); ok {
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
			if s, ok := nameObj.(String); ok {
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
		orderArr, ok := doc.Resolve(orderRef).(Array)
		if ok {
			referencedOCGs := make(map[int]bool)
			collectOCGRefs(orderArr, referencedOCGs)
			for _, ocgRef := range ocgsArr {
				if iref, ok := ocgRef.(IndirectRef); ok {
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

func collectOCGRefs(arr Array, refs map[int]bool) {
	for _, item := range arr {
		if iref, ok := item.(IndirectRef); ok {
			refs[iref.Number] = true
		}
		if subArr, ok := item.(Array); ok {
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

func checkImplementationLimits(doc *Document, level PDFALevel) []ValidationError {
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

func checkObjectLimits(obj Object, objNum int, level PDFALevel, lim implLimits, depth int, errs *[]ValidationError) {
	if obj == nil {
		return
	}

	switch v := obj.(type) {
	case Name:
		if len(string(v)) > lim.nameLen {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("name length %d exceeds maximum %d", len(string(v)), lim.nameLen),
				Object:  objNum,
			})
		}
	case String:
		if len(v.Value) > lim.stringLen {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("string length %d exceeds maximum %d", len(v.Value), lim.stringLen),
				Object:  objNum,
			})
		}
	case Integer:
		i := int64(v)
		if i < -2147483648 || i > 2147483647 {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("integer %d out of range [-2^31, 2^31-1]", i),
				Object:  objNum,
			})
		}
	case Real:
		if math.Abs(float64(v)) > lim.realLimit {
			*errs = append(*errs, ValidationError{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("real %g exceeds magnitude limit %g", float64(v), lim.realLimit),
				Object:  objNum,
			})
		}
	case *Dictionary:
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
	case Array:
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
	case *Stream:
		checkObjectLimits(&v.Dict, objNum, level, lim, depth, errs)
	}
}

func checkPageSizeLimits(doc *Document, level PDFALevel, errs *[]ValidationError) {
	catalog := getCatalog(doc)
	if catalog == nil {
		return
	}
	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return
	}

	pages := collectPages(doc, pagesRef)
	for _, page := range pages {
		for _, boxKey := range []Name{"MediaBox", "CropBox", "BleedBox", "TrimBox", "ArtBox"} {
			var boxObj Object
			switch boxKey {
			case "MediaBox", "CropBox":
				// Inheritable attributes: a page without its own entry
				// takes its Pages ancestor's.
				boxObj = doc.Resolve(inheritedPageAttr(doc, page.dict, boxKey))
			default:
				boxObj = doc.Resolve(page.dict.Get(boxKey))
			}
			if boxObj == nil {
				continue
			}
			arr, ok := boxObj.(Array)
			if !ok || len(arr) != 4 {
				continue
			}
			vals := make([]float64, 4)
			valid := true
			for i, elem := range arr {
				switch ev := elem.(type) {
				case Integer:
					vals[i] = float64(ev)
				case Real:
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
					Object:  page.objNum,
				})
			}
		}
	}
}

// checkQNestingDepth checks that q/Q nesting depth in content streams
// does not exceed 28 levels (PDF/A implementation limit).
func checkQNestingDepth(doc *Document, level PDFALevel, rule string, errs *[]ValidationError) {
	const maxQDepth = 28

	report := func(data []byte, objNum int) {
		if d := qNestingMaxDepth(data); d > maxQDepth {
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
	catalog := getCatalog(doc)
	if catalog == nil {
		return
	}
	pagesRef := catalog.Get("Pages")
	if pagesRef == nil {
		return
	}
	for _, page := range collectPages(doc, pagesRef) {
		contentsRef := page.dict.Get("Contents")
		if contentsRef == nil {
			continue
		}
		if data := getContentStreamData(doc, contentsRef); data != nil {
			report(data, page.objNum)
		}
	}
}

// qNestingMaxDepth computes the maximum q/Q nesting depth of a decoded
// content stream using a real operator tokenizer, so 'q' bytes inside string
// literals, comments, names, or inline-image binary data do not count.
func qNestingMaxDepth(data []byte) int {
	depth, maxDepth := 0, 0
	forEachContentOperator(data, func(op []byte) {
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

// getContentStreamData extracts and concatenates content stream data.
// Handles both single stream references and arrays of stream references.
func getContentStreamData(doc *Document, contentsRef Object) []byte {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *Stream:
		return decodeContentStream(doc, v)
	case Array:
		var result []byte
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if stream, ok := streamObj.(*Stream); ok {
				data := decodeContentStream(doc, stream)
				if data != nil {
					result = append(result, ' ')
					result = append(result, data...)
				}
			}
		}
		return result
	}
	return nil
}

// --- Device color space checks (6.2.3/6.2.4) ---

// Rule 6.2.3.3/6.2.4.3: Device color spaces (DeviceRGB, DeviceCMYK, DeviceGray)
// require either a default color space mapping or a matching OutputIntent.
func checkDeviceColorSpaces(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
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
	pages := collectPages(doc, pagesRef)
	for _, page := range pages {
		// For PDF/A-4, also check page-level OutputIntents
		pageRGB, pageCMYK, pageGray := hasRGBIntent, hasCMYKIntent, hasGrayIntent
		if level == PDFA4 {
			prgb, pcmyk, pgray := getOutputIntentCoverage(doc, page.dict)
			pageRGB = pageRGB || prgb
			pageCMYK = pageCMYK || pcmyk
			pageGray = pageGray || pgray
		}

		// Scan for device color space usage on this page. Default* colour
		// spaces are applied inside the scan, per resource scope: a page-
		// level DefaultCMYK does not cover DeviceCMYK inside a pattern with
		// its own resources, and the corpus fails exactly that.
		usesRGB, usesCMYK, usesGray := scanPageForDeviceCS(doc, page.dict)

		// The page's transparency /Group /CS covers type-matched DeviceRGB
		// and DeviceCMYK, but NOT DeviceGray: the corpus passes DeviceRGB
		// under an ICCBased RGB page group yet fails DeviceGray under an
		// ICCBased Gray one.
		groupRGB, groupCMYK, _ := getGroupCSCoverage(doc, page.dict)

		if usesRGB && !pageRGB && !groupRGB {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceRGB used without matching OutputIntent or DefaultRGB",
				Object:  page.objNum,
			})
		}

		if usesCMYK && !pageCMYK && !groupCMYK {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceCMYK used without matching OutputIntent or DefaultCMYK",
				Object:  page.objNum,
			})
		}

		// DeviceGray: any OutputIntent covers it
		if usesGray && !pageRGB && !pageCMYK && !pageGray {
			errs = append(errs, ValidationError{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceGray used without matching OutputIntent or DefaultGray",
				Object:  page.objNum,
			})
		}
	}

	return errs
}

// getOutputIntentCoverage checks OutputIntents for DestOutputProfile and
// returns which color space types are covered (RGB, CMYK).
func getOutputIntentCoverage(doc *Document, catalog *Dictionary) (hasRGB, hasCMYK, hasGray bool) {
	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return
	}
	oiObj := doc.Resolve(oiRef)
	arr, ok := oiObj.(Array)
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
		if s, _ := dict.Get("S").(Name); s != "GTS_PDFA1" {
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
		stream, ok := profileObj.(*Stream)
		if !ok {
			continue
		}

		// Decompress the profile data to read the ICC header
		profileData := getICCProfileData(stream)
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

// maxICCProfileSize is the maximum size for a decoded ICC profile (2 MB).
const maxICCProfileSize = 2 << 20

// getICCProfileData returns the decompressed ICC profile data from a stream.
// Returns the raw stream data if no filter or decoding fails.
// Limits decoded size to maxICCProfileSize to prevent decompression bombs.
func getICCProfileData(stream *Stream) []byte {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		if len(stream.Data) > maxICCProfileSize {
			return nil
		}
		return stream.Data
	}

	filterName, ok := filter.(Name)
	if !ok {
		return nil
	}
	if filterName != "FlateDecode" {
		return nil
	}

	if len(stream.Data) == 0 {
		return nil
	}

	r, err := zlib.NewReader(bytes.NewReader(stream.Data))
	if err != nil {
		return nil
	}
	defer r.Close()

	limited := io.LimitReader(r, maxICCProfileSize+1)
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil
	}
	if len(decoded) > maxICCProfileSize {
		return nil
	}
	return decoded
}

// getDefaultColorSpaces checks if a page defines DefaultRGB, DefaultCMYK, or DefaultGray
// in its Resources/ColorSpace dictionary.
func getDefaultColorSpaces(doc *Document, page *Dictionary) (hasRGB, hasCMYK, hasGray bool) {
	res := resolveResources(doc, page)
	if res == nil {
		return
	}
	csRef := res.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		return
	}
	for _, key := range csDict.Keys {
		switch key {
		case "DefaultRGB":
			hasRGB = true
		case "DefaultCMYK":
			hasCMYK = true
		case "DefaultGray":
			hasGray = true
		}
	}
	return
}

// getGroupCSCoverage checks if a page's transparency group /CS provides
// implicit color space coverage for device color spaces. An ICCBased CS
// with N=3 covers DeviceRGB, N=4 covers DeviceCMYK, N=1 covers DeviceGray.
// CalRGB covers DeviceRGB, CalGray covers DeviceGray.
func getGroupCSCoverage(doc *Document, page *Dictionary) (hasRGB, hasCMYK, hasGray bool) {
	groupRef := page.Get("Group")
	if groupRef == nil {
		return
	}
	groupDict := doc.ResolveDict(groupRef)
	if groupDict == nil {
		return
	}
	csObj := groupDict.Get("CS")
	if csObj == nil {
		return
	}
	return classifyCalibratedCS(doc, csObj)
}

// classifyCalibratedCS determines what device color spaces a calibrated
// color space provides coverage for. Returns false for all if the CS is
// a device color space (DeviceRGB/CMYK/Gray).
func classifyCalibratedCS(doc *Document, csObj Object) (coversRGB, coversCMYK, coversGray bool) {
	resolved := doc.Resolve(csObj)
	// Direct device CS names don't provide coverage
	if _, ok := resolved.(Name); ok {
		return
	}
	arr, ok := resolved.(Array)
	if !ok || len(arr) < 2 {
		return
	}
	csType, _ := arr[0].(Name)
	switch csType {
	case "ICCBased":
		profileObj := doc.Resolve(arr[1])
		if stream, ok := profileObj.(*Stream); ok {
			if nObj := stream.Dict.Get("N"); nObj != nil {
				if n, ok := nObj.(Integer); ok {
					switch int(n) {
					case 1:
						coversGray = true
					case 3:
						coversRGB = true
					case 4:
						coversCMYK = true
					}
				}
			}
		}
	case "CalRGB":
		coversRGB = true
	case "CalGray":
		coversGray = true
	}
	return
}

// resolveResources resolves a page's Resources dictionary.
func resolveResources(doc *Document, page *Dictionary) *Dictionary {
	return doc.ResolveDict(inheritedPageAttr(doc, page, "Resources"))
}

// inheritedPageAttr looks up an inheritable page attribute (Resources,
// MediaBox, CropBox, Rotate), walking up the /Parent chain when the page
// itself does not define it — pages routinely inherit these from their
// Pages node, which the direct Get missed entirely.
func inheritedPageAttr(doc *Document, page *Dictionary, key Name) Object {
	node := page
	for hops := 0; node != nil && hops < 64; hops++ {
		if v := node.Get(key); v != nil {
			return v
		}
		node = doc.ResolveDict(node.Get("Parent"))
	}
	return nil
}

// scanPageForDeviceCS checks if a page uses device color spaces.
// It scans Image XObjects, Form XObjects, and content streams.
func scanPageForDeviceCS(doc *Document, page *Dictionary) (usesRGB, usesCMYK, usesGray bool) {
	seen := make(map[*Dictionary]bool)
	scanResourcesForDeviceCS(doc, page, seen, &usesRGB, &usesCMYK, &usesGray)

	// Also scan annotation appearance streams
	annotsRef := page.Get("Annots")
	if annotsRef != nil {
		annotsObj := doc.Resolve(annotsRef)
		if annotsArr, ok := annotsObj.(Array); ok {
			for _, annotRef := range annotsArr {
				annotDict := doc.ResolveDict(annotRef)
				if annotDict == nil {
					continue
				}
				ap := annotDict.Get("AP")
				if ap == nil {
					continue
				}
				apDict := doc.ResolveDict(ap)
				if apDict == nil {
					continue
				}
				for _, apKey := range []Name{"N", "R", "D"} {
					apEntry := apDict.Get(apKey)
					if apEntry == nil {
						continue
					}
					apObj := doc.Resolve(apEntry)
					switch v := apObj.(type) {
					case *Stream:
						scanContentStreamForDeviceCS(doc, v, seen, &usesRGB, &usesCMYK, &usesGray)
					case *Dictionary:
						for _, stateVal := range v.Values {
							if s, ok := doc.Resolve(stateVal).(*Stream); ok {
								scanContentStreamForDeviceCS(doc, s, seen, &usesRGB, &usesCMYK, &usesGray)
							}
						}
					}
				}
			}
		}
	}

	// Check transparency group CS on page itself
	if groupRef := page.Get("Group"); groupRef != nil {
		groupDict := doc.ResolveDict(groupRef)
		if groupDict != nil {
			checkCSForDevice(doc, groupDict.Get("CS"), &usesRGB, &usesCMYK, &usesGray)
		}
	}

	return
}

// scanContentStreamForDeviceCS scans a content-bearing stream — a form
// XObject, tiling pattern, or appearance stream. Unlike pages, whose content
// lives behind /Contents, these carry their operators in the stream body
// itself (ISO 32000-1, 7.8.2), which the resources walk alone never read: a
// form with '1 0 0 rg' and no resources scanned as clean.
func scanContentStreamForDeviceCS(doc *Document, stream *Stream, seen map[*Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[&stream.Dict] {
		return
	}
	scanContainerForDeviceCS(doc, &stream.Dict, decodeContentStream(doc, stream), seen, usesRGB, usesCMYK, usesGray)
}

func scanResourcesForDeviceCS(doc *Document, container *Dictionary, seen map[*Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[container] {
		return
	}
	var data []byte
	if contentsRef := container.Get("Contents"); contentsRef != nil {
		data = getContentStreamData(doc, contentsRef)
	}
	scanContainerForDeviceCS(doc, container, data, seen, usesRGB, usesCMYK, usesGray)
}

// scanContainerForDeviceCS scans one content container — a page (content
// behind /Contents), a form XObject, tiling pattern, or appearance stream
// (content in the stream body, passed as data) — for device colour usage.
//
// Only EXECUTED resources count: a form XObject or pattern that is listed in
// the resource dictionary but never invoked by a Do/scn/sh operator does not
// contribute (the corpus passes a DeviceCMYK form that no content stream
// draws), so the resource walks below are gated on the names the content
// actually uses.
func scanContainerForDeviceCS(doc *Document, container *Dictionary, data []byte, seen map[*Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[container] {
		return
	}
	seen[container] = true

	// Device usage selected in THIS container's scope; masked by the
	// container's own Default* colour spaces before propagating (ISO
	// 32000-1, 8.6.5.6: DefaultRGB/DefaultCMYK/DefaultGray in the resource
	// dictionary substitute for device spaces selected in that scope).
	var localRGB, localCMYK, localGray bool
	defer func() {
		dR, dC, dG := getDefaultColorSpaces(doc, container)
		*usesRGB = *usesRGB || (localRGB && !dR)
		*usesCMYK = *usesCMYK || (localCMYK && !dC)
		*usesGray = *usesGray || (localGray && !dG)
	}()

	if data != nil {
		r, c, g := scanStreamForDeviceOps(data)
		localRGB = localRGB || r
		localCMYK = localCMYK || c
		localGray = localGray || g
	}
	used := contentUsedNames(data)

	res := resolveResources(doc, container)
	if res == nil {
		return
	}

	// Check ColorSpace dict for device CS references (including Indexed bases)
	csRef := res.Get("ColorSpace")
	if csRef != nil {
		csDict := doc.ResolveDict(csRef)
		if csDict != nil {
			for _, val := range csDict.Values {
				checkCSForDevice(doc, val, &localRGB, &localCMYK, &localGray)
			}
		}
	}

	// Check XObject resources actually invoked with Do
	xobjRef := res.Get("XObject")
	if xobjRef != nil {
		xobjDict := doc.ResolveDict(xobjRef)
		if xobjDict != nil {
			for i, key := range xobjDict.Keys {
				if !used.xobjects[string(key)] {
					continue
				}
				resolved := doc.Resolve(xobjDict.Values[i])
				stream, ok := resolved.(*Stream)
				if !ok {
					continue
				}
				subtype, _ := stream.Dict.Get("Subtype").(Name)
				if subtype == "Form" {
					// Scan the Form XObject (its own content stream plus its
					// resources) separately, so the Form's Group /CS coverage
					// applies before propagating to the parent.
					var formRGB, formCMYK, formGray bool
					scanContentStreamForDeviceCS(doc, stream, seen, &formRGB, &formCMYK, &formGray)
					// Check transparency group CS
					if groupRef := stream.Dict.Get("Group"); groupRef != nil {
						groupDict := doc.ResolveDict(groupRef)
						if groupDict != nil {
							// Group /CS being a device CS is itself device usage
							checkCSForDevice(doc, groupDict.Get("CS"), &formRGB, &formCMYK, &formGray)
							// A calibrated Group /CS covers device CS within
							// the Form only when the group is ISOLATED: a
							// non-isolated group composites against the
							// backdrop, and the corpus fails DeviceRGB in a
							// non-isolated CalRGB group.
							isolated, _ := doc.Resolve(groupDict.Get("I")).(Boolean)
							if csObj := groupDict.Get("CS"); csObj != nil && bool(isolated) {
								gRGB, gCMYK, gGray := classifyCalibratedCS(doc, csObj)
								if gRGB {
									formRGB = false
								}
								if gCMYK {
									formCMYK = false
								}
								if gGray {
									formGray = false
								}
							}
						}
					}
					// Propagate only uncovered device CS to parent
					*usesRGB = *usesRGB || formRGB
					*usesCMYK = *usesCMYK || formCMYK
					*usesGray = *usesGray || formGray
				} else {
					// Image XObject - check ColorSpace
					checkCSForDevice(doc, stream.Dict.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
				}
			}
		}
	}

	// Check Shading resources painted with sh
	shadingRef := res.Get("Shading")
	if shadingRef != nil {
		shadingDict := doc.ResolveDict(shadingRef)
		if shadingDict != nil {
			for i, key := range shadingDict.Keys {
				if !used.shadings[string(key)] {
					continue
				}
				val := shadingDict.Values[i]
				sd := doc.ResolveDict(val)
				if sd == nil {
					// Could be a stream (type 4-7 shadings)
					if s, ok := doc.Resolve(val).(*Stream); ok {
						checkCSForDevice(doc, s.Dict.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
					}
					continue
				}
				checkCSForDevice(doc, sd.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
			}
		}
	}

	// Check Pattern resources set with scn/SCN (tiling patterns have
	// content streams; shading patterns carry a /Shading)
	patRef := res.Get("Pattern")
	if patRef != nil {
		patDict := doc.ResolveDict(patRef)
		if patDict != nil {
			for i, key := range patDict.Keys {
				if !used.patterns[string(key)] {
					continue
				}
				obj := doc.Resolve(patDict.Values[i])
				switch v := obj.(type) {
				case *Stream:
					// Tiling pattern: its body is a content stream.
					scanContentStreamForDeviceCS(doc, v, seen, usesRGB, usesCMYK, usesGray)
				case *Dictionary:
					// Shading pattern.
					if sd := doc.ResolveDict(v.Get("Shading")); sd != nil {
						checkCSForDevice(doc, sd.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
					}
				}
			}
		}
	}

	// Check Type3 font CharProcs
	fontRef := res.Get("Font")
	if fontRef != nil {
		fontDict := doc.ResolveDict(fontRef)
		if fontDict != nil {
			for _, val := range fontDict.Values {
				fd := doc.ResolveDict(val)
				if fd == nil {
					continue
				}
				subtype, _ := fd.Get("Subtype").(Name)
				if subtype == "Type3" {
					// Recurse into Type3 font resources
					scanResourcesForDeviceCS(doc, fd, seen, usesRGB, usesCMYK, usesGray)
					// Also scan CharProc streams
					cpRef := fd.Get("CharProcs")
					cpDict := doc.ResolveDict(cpRef)
					if cpDict != nil {
						for _, cpVal := range cpDict.Values {
							cpObj := doc.Resolve(cpVal)
							if cpStream, ok := cpObj.(*Stream); ok {
								data := decodeContentStream(doc, cpStream)
								if data != nil {
									r, c, g := scanStreamForDeviceOps(data)
									*usesRGB = *usesRGB || r
									*usesCMYK = *usesCMYK || c
									*usesGray = *usesGray || g
								}
							}
						}
					}
				}
			}
		}
	}
}

// checkCSForDevice checks if a color space value is or contains a device color space.
// Handles direct names, arrays (Indexed, Separation, DeviceN, Pattern with base).
func checkCSForDevice(doc *Document, csObj Object, usesRGB, usesCMYK, usesGray *bool) {
	checkCSForDeviceSeen(doc, csObj, usesRGB, usesCMYK, usesGray, make(map[int]bool))
}

func checkCSForDeviceSeen(doc *Document, csObj Object, usesRGB, usesCMYK, usesGray *bool, seen map[int]bool) {
	if csObj == nil {
		return
	}
	if r, ok := csObj.(IndirectRef); ok {
		if seen[r.Number] {
			return // cycle through an indirect color-space reference
		}
		seen[r.Number] = true
	}
	resolved := doc.Resolve(csObj)
	if n, ok := resolved.(Name); ok {
		switch n {
		case "DeviceRGB":
			*usesRGB = true
		case "DeviceCMYK":
			*usesCMYK = true
		case "DeviceGray":
			*usesGray = true
		}
		return
	}
	if arr, ok := resolved.(Array); ok && len(arr) >= 2 {
		csType, _ := arr[0].(Name)
		switch csType {
		case "Indexed":
			// [/Indexed base hival lookup] - check base
			if len(arr) >= 2 {
				checkCSForDeviceSeen(doc, arr[1], usesRGB, usesCMYK, usesGray, seen)
			}
		case "Separation":
			// A device alternate needs OutputIntent coverage like direct
			// device colour: the corpus fails a Separation with a
			// DeviceCMYK alternate absent a CMYK PDF/A intent.
			if len(arr) >= 3 {
				checkCSForDeviceSeen(doc, arr[2], usesRGB, usesCMYK, usesGray, seen)
			}
		case "DeviceN":
			if len(arr) >= 3 {
				checkCSForDeviceSeen(doc, arr[2], usesRGB, usesCMYK, usesGray, seen)
			}
		case "Pattern":
			// [/Pattern underlyingCS] - check underlying
			if len(arr) >= 2 {
				checkCSForDeviceSeen(doc, arr[1], usesRGB, usesCMYK, usesGray, seen)
			}
		}
	}
}

// maxContentStreamSize is the maximum decoded content stream size we'll scan.
// Larger streams are skipped to bound memory on hostile input. The previous
// 1 MB cap (and Flate-only, no-filter-array decoding) silently hid ordinary
// content from every scanner — an oversize or [/FlateDecode]-wrapped stream
// full of DeviceRGB validated clean.
const maxContentStreamSize = 64 << 20 // 64 MB

// decodeContentStream decodes a stream for content scanning through the full
// filter pipeline (filter arrays, ASCIIHex, predictors). Results are
// memoized per validation run: several checks re-decode the same page
// contents. Returns nil if the stream cannot be decoded.
func decodeContentStream(doc *Document, stream *Stream) []byte {
	if c := doc.valCache; c != nil {
		if data, ok := c.content[stream]; ok {
			return data
		}
	}
	var data []byte
	if decoded, err := decodeStreamData(stream); err == nil && len(decoded) <= maxContentStreamSize {
		data = decoded
	}
	if c := doc.valCache; c != nil {
		c.content[stream] = data
	}
	return data
}

// scanContentsForDeviceOps scans a page's Contents (stream or array of streams)
// for device color operators (rg/RG, k/K, g/G).
func scanContentsForDeviceOps(doc *Document, contentsRef Object) (usesRGB, usesCMYK, usesGray bool) {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *Stream:
		data := decodeContentStream(doc, v)
		if data == nil {
			return
		}
		r, c, g := scanStreamForDeviceOps(data)
		usesRGB = usesRGB || r
		usesCMYK = usesCMYK || c
		usesGray = usesGray || g
	case Array:
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if s, ok := streamObj.(*Stream); ok {
				data := decodeContentStream(doc, s)
				if data == nil {
					continue
				}
				r, c, g := scanStreamForDeviceOps(data)
				usesRGB = usesRGB || r
				usesCMYK = usesCMYK || c
				usesGray = usesGray || g
			}
		}
	}
	return
}

// scanStreamForDeviceOps scans decoded content stream bytes for device color operators.
// Uses a simple tokenizer that handles inline images (BI/ID/EI) to avoid
// scanning binary image data.
func scanStreamForDeviceOps(data []byte) (usesRGB, usesCMYK, usesGray bool) {
	n := len(data)
	var lastName string
	sawColorOp := false
	paints := false
	defer func() {
		// Painting without ever selecting a colour uses the initial colour:
		// DeviceGray black (ISO 32000-1, 8.4.1).
		if paints && !sawColorOp {
			usesGray = true
		}
	}()
	// Scan for operators at word boundaries.
	// An operator token is an alphabetic sequence preceded by whitespace (or BOF)
	// and followed by whitespace, delimiter, or EOF.
	i := 0
	for i < n {
		// Skip whitespace
		for i < n && isContentWS(data[i]) {
			i++
		}
		if i >= n {
			break
		}

		b := data[i]

		// Skip comments
		if b == '%' {
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}

		// Skip string literals (...)
		if b == '(' {
			depth := 1
			i++
			for i < n && depth > 0 {
				if data[i] == '\\' {
					i++ // skip escape char
					if i >= n {
						break
					}
				} else if data[i] == '(' {
					depth++
				} else if data[i] == ')' {
					depth--
				}
				i++
			}
			continue
		}

		// Skip hex strings and dict markers
		if b == '<' {
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else {
				for i < n && data[i] != '>' {
					i++
				}
				if i < n {
					i++
				}
			}
			continue
		}
		if b == '>' {
			i++
			if i < n && data[i] == '>' {
				i++
			}
			continue
		}

		// Skip array/proc delimiters
		if b == '[' || b == ']' || b == '{' || b == '}' {
			i++
			continue
		}

		// PDF names (/Name): remember the last one seen, so a following
		// cs/CS operator can be checked for direct device selection.
		if b == '/' {
			i++
			nameStart := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
			lastName = string(data[nameStart:i])
			continue
		}

		// Read a token
		start := i
		for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
			i++
			// Safety: cap token length to avoid scanning huge binary data
			if i-start > 256 {
				break
			}
		}

		tokLen := i - start

		// Skip names (start with /)
		if tokLen > 0 && data[start] == '/' {
			continue
		}

		switch string(data[start:i]) {
		case "rg", "RG", "g", "G", "k", "K", "cs", "CS", "sc", "scn", "SC", "SCN":
			sawColorOp = true
		case "f", "F", "f*", "S", "s", "B", "B*", "b", "b*", "Tj", "TJ", "'", "\"", "sh":
			paints = true
		}

		// Handle inline images: BI <dict> ID <binary> EI
		// Check for BI (begin inline image), parse dict for CS, then skip binary
		if tokLen == 2 && data[start] == 'B' && data[start+1] == 'I' {
			// Parse inline image dict until ID token
			// Look for /CS or /ColorSpace keys with device CS values
			foundID := false
			for i < n && !foundID {
				// Skip whitespace
				for i < n && isContentWS(data[i]) {
					i++
				}
				if i >= n {
					break
				}
				// Check for ID token (end of inline image dict)
				if data[i] == 'I' && i+1 < n && data[i+1] == 'D' &&
					(i+2 >= n || isContentWS(data[i+2])) {
					i += 2
					// Skip one whitespace byte after ID
					if i < n && isContentWS(data[i]) {
						i++
					}
					foundID = true
					break
				}
				// Read key or value token
				if data[i] == '/' {
					// Read name
					keyStart := i + 1
					i++
					for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
						i++
					}
					key := string(data[keyStart:i])
					// If key is CS or ColorSpace, check the next value
					if key == "CS" || key == "ColorSpace" {
						// Skip whitespace
						for i < n && isContentWS(data[i]) {
							i++
						}
						// Read value - could be /Name or /abbreviation
						if i < n && data[i] == '/' {
							valStart := i + 1
							i++
							for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
								i++
							}
							csVal := string(data[valStart:i])
							switch csVal {
							case "RGB", "DeviceRGB":
								usesRGB = true
							case "CMYK", "DeviceCMYK":
								usesCMYK = true
							case "G", "DeviceGray":
								usesGray = true
							}
						}
					}
				} else {
					// Skip non-name token (numbers, arrays, etc.)
					prev := i
					if data[i] == '[' || data[i] == ']' || data[i] == '(' || data[i] == ')' ||
						data[i] == '<' || data[i] == '>' {
						i++ // skip single delimiter
					} else {
						for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
							i++
						}
					}
					// Safety: if no progress, advance by 1
					if i == prev {
						i++
					}
				}
			}
			// Now skip binary data until EI at word boundary
			if foundID {
				for i < n {
					if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
						atBoundary := (i == 0 || isContentWS(data[i-1]))
						endBoundary := (i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2]))
						if atBoundary && endBoundary {
							i += 2
							break
						}
					}
					i++
				}
			}
			continue
		}

		// Handle ID token outside of BI context (shouldn't happen, but be safe)
		if tokLen == 2 && data[start] == 'I' && data[start+1] == 'D' {
			// Skip one whitespace byte after ID
			if i < n && isContentWS(data[i]) {
				i++
			}
			// Scan for EI at word boundary
			for i < n {
				if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
					atBoundary := (i == 0 || isContentWS(data[i-1]))
					endBoundary := (i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2]))
					if atBoundary && endBoundary {
						i += 2
						break
					}
				}
				i++
			}
			continue
		}

		// Check for device color operators (only short alphabetic tokens)
		if tokLen == 2 {
			if data[start] == 'r' && data[start+1] == 'g' {
				usesRGB = true
			} else if data[start] == 'R' && data[start+1] == 'G' {
				usesRGB = true
			} else if (data[start] == 'c' && data[start+1] == 's') ||
				(data[start] == 'C' && data[start+1] == 'S') {
				// Direct device selection: /DeviceRGB cs (etc.). Named
				// resource selections (/CS0 cs) are covered by the
				// resource-dictionary walk.
				switch lastName {
				case "DeviceRGB":
					usesRGB = true
				case "DeviceCMYK":
					usesCMYK = true
				case "DeviceGray":
					usesGray = true
				}
			}
		} else if tokLen == 1 {
			switch data[start] {
			case 'g':
				usesGray = true
			case 'G':
				usesGray = true
			case 'k':
				usesCMYK = true
			case 'K':
				usesCMYK = true
			}
		}
	}
	return
}

// forEachContentOperator tokenizes a decoded content stream and calls fn for
// each operator-position token (anything that is not a string, hex string,
// dictionary marker, array/procedure delimiter, comment, or name). String
// literals, comments, and inline-image binary data (BI ... ID <binary> EI)
// are skipped, so operator bytes occurring inside them are never reported.
func forEachContentOperator(data []byte, fn func(op []byte)) {
	forEachContentToken(data, func(tok []byte, isName bool) {
		if !isName {
			fn(tok)
		}
	})
}

// forEachContentToken is forEachContentOperator's core walker; it also
// reports name tokens (without the leading slash) so callers can associate
// operand names with the operators that consume them.
func forEachContentToken(data []byte, fn func(tok []byte, isName bool)) {
	n := len(data)
	i := 0
	for i < n {
		for i < n && isContentWS(data[i]) {
			i++
		}
		if i >= n {
			return
		}
		switch b := data[i]; {
		case b == '%': // comment to end of line
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(': // string literal with escapes and balanced parens
			depth := 1
			i++
			for i < n && depth > 0 {
				switch data[i] {
				case '\\':
					i++ // skip escaped char
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case b == '<':
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else { // hex string
				for i < n && data[i] != '>' {
					i++
				}
				if i < n {
					i++
				}
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}':
			i++
		case b == '/':
			i++
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
			fn(data[start:i], true)
		default:
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
				if i-start > 256 { // cap runaway binary tokens
					break
				}
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				skipInlineImage(data, &i)
				continue
			}
			fn(tok, false)
		}
	}
}

// usedResourceNames records which named resources a content stream actually
// executes. Device colour (and other content-level properties) only matter
// on executed content: a form XObject that is referenced in /XObject but
// never invoked with Do does not contribute (the corpus passes a DeviceCMYK
// form that no content stream draws).
type usedResourceNames struct {
	xobjects map[string]bool
	patterns map[string]bool
	shadings map[string]bool
}

func contentUsedNames(data []byte) usedResourceNames {
	u := usedResourceNames{
		xobjects: make(map[string]bool),
		patterns: make(map[string]bool),
		shadings: make(map[string]bool),
	}
	var lastName string
	forEachContentToken(data, func(tok []byte, isName bool) {
		if isName {
			lastName = string(tok)
			return
		}
		switch string(tok) {
		case "Do":
			u.xobjects[lastName] = true
		case "sh":
			u.shadings[lastName] = true
		case "scn", "SCN":
			// A pattern is set by name; non-pattern scn uses numeric
			// operands, in which case lastName is stale — over-recording is
			// harmless (it only widens the scan).
			u.patterns[lastName] = true
		}
	})
	return u
}

// skipInlineImage advances *pos past an inline image: the parameter
// dictionary tokens up to ID, then binary data until a whitespace-delimited
// EI token.
func skipInlineImage(data []byte, pos *int) {
	n := len(data)
	i := *pos
	paramStart := i
	// Scan tokens until the ID keyword that starts the binary section.
	for i < n {
		for i < n && isContentWS(data[i]) {
			i++
		}
		if i >= n {
			break
		}
		if data[i] == 'I' && i+1 < n && data[i+1] == 'D' && (i+2 >= n || isContentWS(data[i+2])) {
			i += 2
			if i < n && isContentWS(data[i]) {
				i++ // single whitespace after ID
			}
			break
		}
		prev := i
		if isContentDelim(data[i]) {
			i++
			if data[prev] == '(' { // string value inside the param dict
				depth := 1
				for i < n && depth > 0 {
					switch data[i] {
					case '\\':
						i++
					case '(':
						depth++
					case ')':
						depth--
					}
					i++
				}
			}
		} else {
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
		}
		if i == prev {
			i++
		}
	}
	// Inline-image sample data is arbitrary binary and can contain the bytes
	// "EI" by chance, which the boundary search below would mistake for the end
	// (spewing the rest of the image as bogus operators/hex strings). When the
	// dictionary declares /L (or /Length), skip exactly that many bytes and
	// confirm EI follows; only fall back to the search if it is absent or
	// inconsistent, so behaviour never regresses (audit C25).
	binaryStart := i
	if declLen, ok := inlineImageDeclaredLength(data[paramStart:binaryStart]); ok {
		end := binaryStart + declLen
		if end <= n {
			j := end
			for j < n && isContentWS(data[j]) {
				j++
			}
			if j+1 < n && data[j] == 'E' && data[j+1] == 'I' &&
				(j+2 >= n || isContentWS(data[j+2]) || isContentDelim(data[j+2])) {
				*pos = j + 2
				return
			}
		}
	}

	// Skip binary data until EI at a token boundary.
	for i < n {
		if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
			atBoundary := i == 0 || isContentWS(data[i-1])
			endBoundary := i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2])
			if atBoundary && endBoundary {
				i += 2
				break
			}
		}
		i++
	}
	*pos = i
}

// inlineImageDeclaredLength extracts the /L (or /Length) value from an inline
// image's parameter region, if present. It reports the declared byte count of
// the binary sample data.
func inlineImageDeclaredLength(params []byte) (int, bool) {
	for i := 0; i < len(params); i++ {
		if params[i] != '/' {
			continue
		}
		// Read the key name.
		j := i + 1
		for j < len(params) && !isContentWS(params[j]) && !isContentDelim(params[j]) {
			j++
		}
		key := string(params[i+1 : j])
		if key != "L" && key != "Length" {
			continue
		}
		// Skip whitespace to the value.
		for j < len(params) && isContentWS(params[j]) {
			j++
		}
		start := j
		v := 0
		for j < len(params) && params[j] >= '0' && params[j] <= '9' {
			v = v*10 + int(params[j]-'0')
			j++
		}
		if j == start {
			continue
		}
		return v, true
	}
	return 0, false
}

func isContentWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\x00' || b == '\x0c'
}

func isContentDelim(b byte) bool {
	return b == '(' || b == ')' || b == '<' || b == '>' || b == '[' || b == ']' || b == '{' || b == '}' || b == '/' || b == '%'
}

// --- ICCBased color space checks (6.2.4.2) ---

// Rule 6.2.4.2: ICCBased color spaces must reference valid ICC profiles.
func checkICCBasedProfiles(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError

	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
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
		if t, ok := stream.Dict.Get("Type").(Name); ok && (t == "ObjStm" || t == "XRef") {
			continue
		}

		// Verify it's actually an ICC profile by checking for Alternate or being
		// referenced from a ColorSpace array. We check for the /N key which is
		// specific to ICC profile streams.
		nVal := 0
		switch v := nObj.(type) {
		case Integer:
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
		profileData := getICCProfileData(stream)

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
func checkSeparationDeviceN(doc *Document, level PDFALevel) []ValidationError {

	var errs []ValidationError

	// Track tint transform references by colorant name for consistency check
	tintTransforms := make(map[Name]sepColorantSeen) // colorant name → first seen definition

	// Scan all objects for color space arrays used in Resources
	for num, iobj := range doc.Objects {
		dict, isDict := iobj.Value.(*Dictionary)
		stream, isStream := iobj.Value.(*Stream)

		// Check dictionary Resources/ColorSpace
		if isDict {
			checkDictForSepDeviceN(doc, dict, num, level, &errs)
			collectTintTransforms(doc, dict, tintTransforms, num, level, &errs)
			// A direct /Resources sub-dictionary (the common case on pages)
			// is not a top-level object, so this scan would never visit its
			// /ColorSpace entries; descend explicitly. Indirect Resources
			// are separate objects and are visited by the loop itself.
			if resDict, ok := dict.Get("Resources").(*Dictionary); ok {
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
			if resDict, ok := stream.Dict.Get("Resources").(*Dictionary); ok {
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
	tint   Object
	alt    Object
}

func collectTintTransforms(doc *Document, dict *Dictionary, tintTransforms map[Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError) {
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
func collectSeparationConsistency(doc *Document, val Object, tintTransforms map[Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError) {
	collectSeparationConsistencySeen(doc, val, tintTransforms, objNum, level, errs, make(map[int]bool))
}

func collectSeparationConsistencySeen(doc *Document, val Object, tintTransforms map[Name]sepColorantSeen, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	// Guard against a DeviceN whose /Colorants entry cycles back to itself: a
	// self-referential colorant would otherwise recurse until the goroutine
	// stack overflows (an unrecoverable fatal error), like the other
	// colour-space walkers this thread a visited-set keyed on object number.
	if ref, ok := val.(IndirectRef); ok {
		if seen[ref.Number] {
			return
		}
		seen[ref.Number] = true
	}
	resolved := doc.Resolve(val)
	arr, ok := resolved.(Array)
	if !ok || len(arr) == 0 {
		return
	}
	csType, _ := arr[0].(Name)

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
	colorantName, ok := arr[1].(Name)
	if !ok {
		return
	}
	tintRef, isRef := arr[3].(IndirectRef)
	if !isRef {
		return
	}
	if prev, exists := tintTransforms[colorantName]; exists {
		// Different objects may still hold identical content, which is
		// conformant: the rule requires the SAME tint transform and
		// alternate space, and veraPDF accepts equal-by-content duplicates.
		sameTint := prev.objNum == tintRef.Number || Equal(doc.Resolve(prev.tint), doc.Resolve(tintRef))
		if !sameTint {
			*errs = append(*errs, ValidationError{
				Rule:    colourClause("spot", level),
				Level:   level,
				Message: fmt.Sprintf("Separation colorant /%s has inconsistent tint transforms (objects %d and %d)", string(colorantName), prev.objNum, tintRef.Number),
				Object:  objNum,
			})
		}
		if !Equal(doc.Resolve(prev.alt), doc.Resolve(arr[2])) {
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

func checkDictForSepDeviceN(doc *Document, dict *Dictionary, objNum int, level PDFALevel, errs *[]ValidationError) {
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

func checkColorSpaceValue(doc *Document, csObj Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	checkColorSpaceValueSeen(doc, csObj, objNum, level, errs, make(map[int]bool))
}

func checkColorSpaceValueSeen(doc *Document, csObj Object, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	if r, ok := csObj.(IndirectRef); ok {
		if seen[r.Number] {
			return // cycle through an indirect color-space reference
		}
		seen[r.Number] = true
	}
	resolved := doc.Resolve(csObj)
	arr, ok := resolved.(Array)
	if !ok || len(arr) < 2 {
		return
	}

	csType, ok := arr[0].(Name)
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
		if name, ok := arr[1].(Name); ok && name == "None" {
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
			if namesArr, ok := doc.Resolve(arr[1]).(Array); ok && len(namesArr) > maxColorants {
				*errs = append(*errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("DeviceN color space has %d colorants, maximum is %d", len(namesArr), maxColorants),
					Object:  objNum,
				})
			}
		}

		// Get colorant names from the DeviceN array
		namesArr, namesOk := doc.Resolve(arr[1]).(Array)

		// Spot colorants require a Colorants dictionary with their
		// definitions (ISO 19005-2/-3/-4, 6.2.4.4); process colour names
		// need none.
		if level != PDFA1b && namesOk {
			hasSpot := false
			for _, nameObj := range namesArr {
				if name, ok := nameObj.(Name); ok && !isProcessColorant(name) {
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
								if name, ok := nameObj.(Name); ok {
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
func checkCIEDictParams(doc *Document, family string, dict *Dictionary, objNum int, level PDFALevel, errs *[]ValidationError) {
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
	nums := func(v Object) ([]float64, bool) {
		arr, ok := doc.Resolve(v).(Array)
		if !ok {
			return nil, false
		}
		out := make([]float64, 0, len(arr))
		for _, el := range arr {
			switch n := doc.Resolve(el).(type) {
			case Integer:
				out = append(out, float64(n))
			case Real:
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
			case Integer:
				gv, isNum = float64(n), true
			case Real:
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
func isProcessColorant(name Name) bool {
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
func checkAlternateCS(doc *Document, altCS Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	checkAlternateCSSeen(doc, altCS, objNum, level, errs, make(map[int]bool))
}

func checkAlternateCSSeen(doc *Document, altCS Object, objNum int, level PDFALevel, errs *[]ValidationError, seen map[int]bool) {
	if r, ok := altCS.(IndirectRef); ok {
		if seen[r.Number] {
			return // cycle through an indirect alternate color-space reference
		}
		seen[r.Number] = true
	}
	resolved := doc.Resolve(altCS)

	if n, ok := resolved.(Name); ok {
		switch n {
		case "DeviceRGB", "DeviceCMYK", "DeviceGray":
			// For PDF/A-1b: a device alternate follows the same rule as
			// direct device color-space use — legal when a matching
			// OutputIntent covers it (ISO 19005-1, 6.2.3.2), forbidden
			// otherwise.
			if level == PDFA1b {
				covered := false
				if catalog := getCatalog(doc); catalog != nil {
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
			// which is checked by checkDeviceColorSpaces via checkCSForDevice.
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
	if arr, ok := resolved.(Array); ok && len(arr) >= 2 {
		if csType, ok := arr[0].(Name); ok {
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

func decodeXMPToUTF8(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// Check for BOM
	if len(data) >= 4 {
		// UTF-32 BE BOM: 00 00 FE FF
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
			return decodeUTF32(data[4:], true)
		}
		// UTF-32 LE BOM: FF FE 00 00
		if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
			return decodeUTF32(data[4:], false)
		}
	}
	if len(data) >= 2 {
		// UTF-16 BE BOM: FE FF
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeUTF16(data[2:], true)
		}
		// UTF-16 LE BOM: FF FE
		if data[0] == 0xFF && data[1] == 0xFE {
			return decodeUTF16(data[2:], false)
		}
	}

	// UTF-8 BOM: EF BB BF - just skip it
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:])
	}

	// Heuristic: detect encoding without BOM (check UTF-32 before UTF-16)
	if len(data) >= 4 {
		// UTF-32 BE: 00 00 00 xx
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] != 0x00 {
			return decodeUTF32(data, true)
		}
		// UTF-32 LE: xx 00 00 00
		if data[0] != 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x00 {
			return decodeUTF32(data, false)
		}
		// UTF-16 BE: 00 xx
		if data[0] == 0x00 && data[1] != 0x00 {
			return decodeUTF16(data, true)
		}
		// UTF-16 LE: xx 00
		if data[0] != 0x00 && data[1] == 0x00 {
			return decodeUTF16(data, false)
		}
	}

	return string(data)
}

func decodeUTF16(data []byte, bigEndian bool) string {
	if len(data) < 2 {
		return ""
	}
	var buf []byte
	for i := 0; i+1 < len(data); i += 2 {
		var codeUnit uint16
		if bigEndian {
			codeUnit = uint16(data[i])<<8 | uint16(data[i+1])
		} else {
			codeUnit = uint16(data[i+1])<<8 | uint16(data[i])
		}

		// Handle surrogate pairs
		if codeUnit >= 0xD800 && codeUnit <= 0xDBFF {
			if i+3 < len(data) {
				var low uint16
				if bigEndian {
					low = uint16(data[i+2])<<8 | uint16(data[i+3])
				} else {
					low = uint16(data[i+3])<<8 | uint16(data[i+2])
				}
				if low >= 0xDC00 && low <= 0xDFFF {
					r := rune(0x10000 + (rune(codeUnit-0xD800)<<10 | rune(low-0xDC00)))
					var tmp [4]byte
					n := utf8.EncodeRune(tmp[:], r)
					buf = append(buf, tmp[:n]...)
					i += 2
					continue
				}
			}
			buf = append(buf, 0xEF, 0xBF, 0xBD) // replacement char
			continue
		}

		var tmp [4]byte
		n := utf8.EncodeRune(tmp[:], rune(codeUnit))
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

func decodeUTF32(data []byte, bigEndian bool) string {
	if len(data) < 4 {
		return ""
	}
	var buf []byte
	for i := 0; i+3 < len(data); i += 4 {
		var codePoint uint32
		if bigEndian {
			codePoint = uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3])
		} else {
			codePoint = uint32(data[i+3])<<24 | uint32(data[i+2])<<16 | uint32(data[i+1])<<8 | uint32(data[i])
		}

		r := rune(codePoint)
		if !utf8.ValidRune(r) {
			r = 0xFFFD
		}
		var tmp [4]byte
		n := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

// --- helpers ---

func isAnnotation(dict *Dictionary) bool {
	if t, ok := dict.Get("Type").(Name); ok && t == "Annot" {
		return true
	}
	// Also detect annotations by Subtype + Rect (some PDFs omit /Type)
	if _, ok := dict.Get("Subtype").(Name); ok && dict.Get("Rect") != nil {
		return true
	}
	return false
}

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

func scanContentColorUsage(data []byte) contentColorUsage {
	u := contentColorUsage{
		fillCS:   make(map[string]bool),
		strokeCS: make(map[string]bool),
		gsNames:  make(map[string]bool),
	}
	var lastName string
	forEachContentToken(data, func(tok []byte, isName bool) {
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
func iccCMYKProfile(doc *Document, csVal Object) *Stream {
	arr, ok := doc.Resolve(csVal).(Array)
	if !ok || len(arr) < 2 {
		return nil
	}
	if n, _ := arr[0].(Name); n != "ICCBased" {
		return nil
	}
	stream, ok := doc.Resolve(arr[1]).(*Stream)
	if !ok {
		return nil
	}
	if n, ok := stream.Dict.Get("N").(Integer); !ok || n != 4 {
		return nil
	}
	return stream
}

// iccProfileStream returns the profile stream of any ICCBased colour space.
func iccProfileStream(doc *Document, csVal Object) *Stream {
	arr, ok := doc.Resolve(csVal).(Array)
	if !ok || len(arr) < 2 {
		return nil
	}
	if n, _ := arr[0].(Name); n != "ICCBased" {
		return nil
	}
	stream, _ := doc.Resolve(arr[1]).(*Stream)
	return stream
}

// sameICCProfile reports whether two profile streams hold the same profile:
// the same object, or byte-identical data after zeroing the Profile ID field
// (ICC header bytes 84-99), which is what distinguishes an original from a
// copy whose MD5 was filled in.
func sameICCProfile(doc *Document, a, b *Stream) bool {
	if a == nil || b == nil {
		return false
	}
	if a == b {
		return true
	}
	da := getICCProfileData(a)
	db := getICCProfileData(b)
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
func checkICCBasedUsageRules(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	var errs []ValidationError
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		res := resolveResources(doc, page.dict)
		if res == nil {
			continue
		}
		data := getContentStreamData(doc, page.dict.Get("Contents"))
		if data == nil {
			continue
		}
		usage := scanContentColorUsage(data)

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
				if v, ok := gs.Get("OPM").(Integer); ok && v == 1 {
					opm1 = true
				}
				strokeSet, strokeIsSet := gs.Get("OP").(Boolean)
				fillSet, fillIsSet := gs.Get("op").(Boolean)
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
			csVal := csDict.Get(Name(name))
			if csVal == nil {
				return
			}
			if cmyk := iccCMYKProfile(doc, csVal); cmyk != nil && opm1 {
				if (stroke && opStroke && usage.paintsStroke) || (!stroke && opFill && usage.paintsFill) {
					errs = append(errs, ValidationError{
						Rule:    colourClause("iccBased", level),
						Level:   level,
						Message: "overprint mode must not be 1 when an ICCBased CMYK colour space is used with overprinting",
						Object:  page.objNum,
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
func checkJPXImages(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // JPXDecode is forbidden outright at PDF/A-1 (6.1.10)
	}
	rule := imageClause("jpx", level)

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
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
