package pdfa

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// This file is the core of the PDF/A validator: the conformance levels
// (ISO 19005-1/-2/-3/-4, i.e. 1b/2b/3b/4, plus the entry point into Level A),
// the validateView dispatcher, and most of the clause-6
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

// pdfaMemoCache is this engine's memo for one run: the annotations the
// document reaches, which half a dozen rules walk. It is reached through
// core.Slot rather than held on the shared run state, because nothing else
// reads it.
type pdfaMemoCache struct {
	annots    []annotOccurrence
	hasAnnots bool
}

// pdfaSlot keys pdfaMemoCache; an unexported empty struct cannot collide.
type pdfaSlot struct{}

func pdfaMemo(d core.View) *pdfaMemoCache { return core.Slot[pdfaMemoCache](d.Run, pdfaSlot{}) }

// sortedReachableObjectNums returns the numbers of the objects the document
// reaches in ascending order, for a rule whose report depends on which object
// it meets first: ascending number is a total order, so the report is the same
// on every run.
func sortedReachableObjectNums(doc core.View) []int {
	nums := slices.Clone(doc.ReachableObjectNums())
	slices.Sort(nums)
	return nums
}

// Violation describes a single PDF/A conformance violation.
type Violation struct {
	Rule    string // e.g., "6.1.3" (ISO 19005 clause)
	Level   Level  // the level that requires this rule
	Message string
	Object  int // object number, 0 if N/A
	// Check identifies the specific requirement within Rule, for the findings
	// another validator composes on (CheckPDFAIDConformance). A clause covers
	// several requirements and a message is prose that may be reworded; a
	// validator that replaces one base finding with its own keys on this.
	// Empty on findings nothing composes on.
	Check string
}

// RuleID returns the ISO 19005 clause identifier.
func (e Violation) RuleID() string { return e.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (e Violation) ObjectNum() int { return e.Object }

func (e Violation) Error() string {
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
//
// It is also where the run's work meter unwinds to (core.Meter): a check the
// meter stopped reports nothing, because what it had found so far was found
// on a partial walk, and the trip that stopped it is reported for the run.
func runCheck(doc core.View, level Level, check func(core.View, Level) []Violation) (out []Violation) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			if !core.IsAbort(r) {
				out = []Violation{{Rule: finding.InternalRule, Level: level, Message: finding.InternalMessage(r)}}
			}
		}
	}()
	return check(doc, level)
}

// runByteCheck is runCheck for a byte-level check: one rule over the file
// record, behind its own recover boundary, so a check that fails internally
// costs its own rule and not the others' (they used to share one boundary, and
// one out-of-range slice turned all of them into a single "internal").
func runByteCheck(f *core.FileRecord, level Level, check func(*core.FileRecord, Level) []Violation) (out []Violation) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			if !core.IsAbort(r) {
				out = []Violation{{Rule: finding.InternalRule, Level: level, Message: finding.InternalMessage(r)}}
			}
		}
	}()
	return check(f, level)
}

// byteChecks are the byte-level file-structure rules (filestructure.go). Each
// reads the file record; the signature rule also asks the document which of
// the file's signature dictionaries it has (see checkSignatureCoversFile).
func byteChecks(doc core.View) []func(*core.FileRecord, Level) []Violation {
	return []func(*core.FileRecord, Level) []Violation{
		func(f *core.FileRecord, level Level) []Violation { return checkNoDataAfterEOF(f.Data, level) },  // 6.1.3
		func(f *core.FileRecord, level Level) []Violation { return checkFileHeaderBytes(level, f.Data) }, // 6.1.2
		checkIndirectObjectSyntax, // 6.1.8 / 6.1.9
		checkXRefTableFormat,      // 6.1.4
		checkNoXRefStreams,        // 6.1.4 (PDF/A-1)
		checkStreamKeywordFormat,  // 6.1.7.1 / 6.1.6
		checkLinearizedTrailerID,  // 6.1.3
		checkStreamLengthBytes,    // 6.1.7 / 6.1.6.1
		func(f *core.FileRecord, level Level) []Violation { return checkSignatureCoversFile(doc, f, level) }, // 6.4.3
	}
}

// validateView runs the PDF/A pipeline over a view, against the target
// profile level names (see resolveTarget for LevelDeclared and invalid
// levels).
//
// The byte-level file-structure rules read doc.FileRecord(), the record of the file
// the document was read from. A view with none — a document built in memory —
// has no file to judge: those rules do not run, and the run records a
// GuardNoSourceFile trip saying so, which the caller reports as a checker
// finding. They are never skipped silently.
//
// There is one pipeline for every level. Each check is handed the target
// unflattened and asks it what it needs — the part, the conformance level,
// the variant — so a Level A, Level U or PDF/A-4 variant run is the same run
// as a Level B one with the families that level adds switched on, and no
// finding is produced at one level only to be dropped at another.
func validateView(doc core.View, level Level) []Violation {
	level, refused := resolveTarget(doc, level)
	if refused != nil {
		return refused
	}

	var errs []Violation

	checks := []func(core.View, Level) []Violation{
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
		// Metadata (6.7): the declaration against the target
		checkIdentification,
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
		// Names that must be UTF-8 (6.1.8 / 6.1.7), hexadecimal strings
		// (6.1.6 / 6.1.5), inline-image filters (6.1.10 / 6.1.9) and
		// signature contents (6.4.3). They read the object graph, and the
		// hexadecimal-string rule the file record too when there is one; they
		// used to run only when the caller passed the file's bytes.
		checkNameUTF8,
		checkHexStringFormat,
		checkInlineImageFilters,
		checkSignatureContents,
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
		// The PDF/A-4e / PDF/A-4f variants' own requirements (6.9, 6.1.6.1)
		checkA4FEmbeddedFilesPresent,
		checkA4E3DStreamSubtype,
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
		// Unicode character maps (Level A and Level U: 6.3.8 / 6.2.11.7.2)
		checkUnicodeMapping,
		// Level A: logical structure, artifacts, structure types, language,
		// ActualText for Private Use Area code points
		checkLevelAStructure,
		checkLevelAArtifacts,
		checkLevelAStructTypes,
		checkLevelALanguage,
		checkLevelAActualText,
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

	// Byte-level checks, over the file the document was read from. A
	// cancelled run has already said that what it did not reach was skipped.
	switch file := doc.FileRecord(); {
	case doc.Cancel.Stopped():
	case file == nil:
		doc.Note(core.GuardNoSourceFile, "the document was not read from a file, so the byte-level file-structure rules (header, cross-reference tables, object and stream syntax, stream lengths, data after %%EOF, signature coverage) were not checked; write it and read it back to check them", 0)
	default:
		for _, check := range byteChecks(doc) {
			if doc.Cancel.Stopped() {
				break
			}
			errs = append(errs, runByteCheck(file, level, check)...)
		}
	}

	finding.Sort(errs)
	return errs
}

// --- File structure checks (6.1) ---

// Rule 6.1.3-2: Encrypt key must not be present in trailer dictionary.
func checkNoEncrypt(doc core.View, level Level) []Violation {
	if doc.Trailer.Get("Encrypt") != nil {
		return []Violation{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer must not contain /Encrypt",
		}}
	}
	return nil
}

// Rule 6.1.3-1: Document trailer must contain non-empty ID entry.
func checkFileID(doc core.View, level Level) []Violation {
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return []Violation{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "trailer must contain /ID array",
		}}
	}
	arr, ok := doc.Resolve(idObj).(object.Array)
	if !ok {
		return []Violation{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "/ID must be an array",
		}}
	}
	if len(arr) != 2 {
		return []Violation{{
			Rule:    "6.1.3",
			Level:   level,
			Message: "/ID array must have exactly 2 elements",
		}}
	}
	for i, elem := range arr {
		if _, ok := doc.Resolve(elem).(object.String); !ok { // string: a type check on the file identifier, which is never encrypted (ISO 32000-2 7.6.2)
			return []Violation{{
				Rule:    "6.1.3",
				Level:   level,
				Message: fmt.Sprintf("/ID element %d must be a string", i),
			}}
		}
	}
	return nil
}

// Rule 6.1.2-1: File header version must match level.
func checkHeader(doc core.View, level Level) []Violation {
	switch level.Part() {
	case 1:
		// The 19005-1 header rule is about format, not version: the veraPDF
		// corpus passes a %PDF-2.0 header at PDF/A-1b. No version check.
	case 2, 3:
		// PDF/A-2/3 accept any PDF 1.x header (1.0-1.7): the standard is
		// built on PDF 1.7 but earlier headers are legal; the previous
		// 1.4-1.7 floor false-positived on conforming 1.0-1.3 files.
		valid := len(doc.Version) == 3 && strings.HasPrefix(doc.Version, "1.") &&
			doc.Version[2] >= '0' && doc.Version[2] <= '7'
		if !valid {
			return []Violation{{
				Rule:    "6.1.2",
				Level:   level,
				Message: fmt.Sprintf("header version must be 1.0-1.7, got %s", doc.Version),
			}}
		}
	case 4:
		if !strings.HasPrefix(doc.Version, "2.") {
			return []Violation{{
				Rule:    "6.1.2",
				Level:   level,
				Message: fmt.Sprintf("version must be 2.x, got %s", doc.Version),
			}}
		}
	}
	return nil
}

// Rules 6.1.3-4, 6.1.3-5: Info key requires PieceInfo; Info may only contain ModDate.
func checkTrailerInfo(doc core.View, level Level) []Violation {
	if level.Part() != 4 {
		return nil // only applies to PDF/A-4
	}

	infoRef := doc.Trailer.Get("Info")
	if infoRef == nil {
		return nil
	}

	catalog := doc.Catalog()

	// Rule 6.1.3-4: Info requires PieceInfo in catalog
	if catalog == nil || catalog.Get("PieceInfo") == nil {
		return []Violation{{
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
	for key := range infoDict.Keys() {
		if key != "ModDate" {
			return []Violation{{
				Rule:    "6.1.3",
				Level:   level,
				Message: fmt.Sprintf("Info dictionary may only contain /ModDate, found /%s", string(key)),
			}}
		}
	}

	return nil
}

// Rule 6.1.3-3: No data after the last %%EOF marker.
func checkNoDataAfterEOF(rawData []byte, level Level) []Violation {
	eofMarker := []byte("%%EOF")
	idx := bytes.LastIndex(rawData, eofMarker)
	if idx < 0 {
		return []Violation{{
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
		return []Violation{{
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
func checkMetadataStream(doc core.View, level Level) []Violation {
	catalog := doc.Catalog()
	if catalog == nil {
		return []Violation{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "catalog not found",
		}}
	}

	metaRef := catalog.Get("Metadata")
	if metaRef == nil {
		return []Violation{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "catalog must have /Metadata entry",
		}}
	}

	metaObj := doc.Resolve(metaRef)
	if metaObj == nil {
		return []Violation{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "/Metadata reference target not found",
		}}
	}

	stream, ok := metaObj.(*object.Stream)
	if !ok {
		return []Violation{{
			Rule:    "6.7.2",
			Level:   level,
			Message: "/Metadata must be a stream",
		}}
	}

	var errs []Violation

	if t, _ := doc.ResolveName(stream.Dict.Get("Type")); t != "Metadata" {
		errs = append(errs, Violation{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Type /Metadata",
		})
	}

	if st, _ := doc.ResolveName(stream.Dict.Get("Subtype")); st != "XML" {
		errs = append(errs, Violation{
			Rule:    "6.7.2",
			Level:   level,
			Message: "metadata stream must have /Subtype /XML",
		})
	}

	// ISO 19005-1 (PDF/A-1) 6.7.2 forbids a Filter on the document metadata
	// stream; PDF/A-2 and PDF/A-3 removed that restriction (a permitted filter
	// such as FlateDecode is allowed). veraPDF carries the PDMetadata Filter rule
	// only in its PDF/A-1 profile.
	if level.Part() == 1 && stream.Dict.Get("Filter") != nil {
		errs = append(errs, Violation{
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
func colourClause(concept string, level Level) string {
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
	switch level.Part() {
	case 1:
		return cl[0]
	case 4:
		return cl[2]
	default:
		return cl[1]
	}
}

// annotActionClause returns the ISO clause for an annotation/action concept at
// the given level. These rules move between clause trees per part (annotations:
// 6.5.x in part 1, 6.3.x/6.4.x in parts 2/3/4; actions: 6.6.x in parts 1/4,
// 6.5.x in parts 2/3); clauses follow the veraPDF profiles.
func annotActionClause(concept string, level Level) string {
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
	switch level.Part() {
	case 1:
		return c[0]
	case 4:
		return c[2]
	default:
		return c[1]
	}
}

func checkOutputIntents(doc core.View, level Level) []Violation {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	// PDF/A-4: validate page-level OutputIntents have /S /GTS_PDFA1
	// (must run even if no catalog-level OutputIntents)
	var errsPageLevel []Violation
	if level.Part() == 4 {
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
					errsPageLevel = append(errsPageLevel, Violation{
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
		return append(errsPageLevel, Violation{
			Rule:    colourClause("outputIntent", level),
			Level:   level,
			Message: "/OutputIntents reference target not found",
		})
	}

	arr, ok := oiObj.(object.Array)
	if !ok {
		return append(errsPageLevel, Violation{
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
			errs = append(errs, Violation{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] is not a dictionary", i),
			})
			continue
		}

		s := dict.Get("S")
		if s == nil {
			errs = append(errs, Violation{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must have /S", i),
			})
			continue
		}

		if _, ok := doc.ResolveName(s); !ok {
			errs = append(errs, Violation{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /S must be a name", i),
			})
			continue
		}

		// /DestOutputProfileRef is not allowed in PDF/A
		if dict.Get("DestOutputProfileRef") != nil {
			errs = append(errs, Violation{
				Rule:    colourClause("outputIntent", level),
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must not have /DestOutputProfileRef", i),
			})
		}

		profRef := dict.Get("DestOutputProfile")
		if sName, _ := doc.ResolveName(s); profRef == nil && sName != "GTS_PDFA1" {
			// /DestOutputProfile is required unless /OutputConditionIdentifier
			// identifies a standard registered condition. A GTS_PDFA1 intent
			// needs the profile whatever its identifier says, which the rule
			// below reports; reporting it here as well gave one missing profile
			// two findings (audit 2026-09-22 C140).
			oci := dict.Get("OutputConditionIdentifier")
			if oci == nil {
				errs = append(errs, Violation{
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
					errs = append(errs, Violation{
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
			errs = append(errs, Violation{
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

// outputIntentRef is one output intent a profile rule judges: its dictionary,
// how a message names it, and the object a finding anchors to.
type outputIntentRef struct {
	dict  *object.Dictionary
	label string
	obj   int
}

// documentOutputIntents lists the catalog's output intents and, at PDF/A-4,
// every page's — ISO 19005-4 6.2.3 lets a page carry its own, and its
// destination profile is held to the same requirements as the document's.
func documentOutputIntents(doc core.View, level Level) []outputIntentRef {
	var out []outputIntentRef
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	if arr, ok := doc.Resolve(catalog.Get("OutputIntents")).(object.Array); ok {
		for i, elem := range arr {
			if d := doc.ResolveDict(elem); d != nil {
				out = append(out, outputIntentRef{d, fmt.Sprintf("/OutputIntents[%d]", i), 0})
			}
		}
	}
	if level.Part() == 4 {
		for _, page := range doc.Pages(catalog.Get("Pages")) {
			arr, ok := doc.Resolve(page.Dict.Get("OutputIntents")).(object.Array)
			if !ok {
				continue
			}
			for j, elem := range arr {
				if d := doc.ResolveDict(elem); d != nil {
					out = append(out, outputIntentRef{d, fmt.Sprintf("page OutputIntents[%d]", j), page.ObjNum})
				}
			}
		}
	}
	return out
}

// checkOutputIntentProfile judges the destination profile of every output
// intent — the catalog's and, at PDF/A-4, the pages' — once per profile: an
// intent sharing another's profile adds nothing to check, and every finding
// names the first intent that carries it. Each finding is under the
// output-intent clause (6.2.2 at part 1, 6.2.3 later).
//
// It does not look at ICCBased colour spaces: checkICCBasedProfiles judges a
// profile used as one, under its own clause, and a profile that is only an
// output intent's is not one (audit 2026-09-22 C140).
func checkOutputIntentProfile(doc core.View, level Level) []Violation {
	rule := colourClause("outputIntent", level)
	var errs []Violation
	report := func(oi outputIntentRef, format string, args ...any) {
		errs = append(errs, Violation{Rule: rule, Level: level, Object: oi.obj,
			Message: oi.label + " " + fmt.Sprintf(format, args...)})
	}
	judged := map[*object.Stream]bool{}
	for _, oi := range documentOutputIntents(doc, level) {
		profStream, ok := doc.Resolve(oi.dict.Get("DestOutputProfile")).(*object.Stream)
		if !ok || judged[profStream] {
			continue
		}
		judged[profStream] = true
		// Validate ICC profile N matches the profile data
		nObj := profStream.Dict.Get("N")
		if nObj == nil {
			report(oi, "/DestOutputProfile must have /N")
			continue
		}
		nVal, ok := doc.ResolveInt(nObj)
		if !ok {
			continue
		}
		// Decompress and check ICC profile header
		data, r := doc.ICCProfileData(profStream)
		if r != core.ReasonOK {
			// Only malformed data is a violation. A profile pdf0 declined to
			// decode — over the ICC or decode limit, a filter it does not
			// implement, ciphertext — must not produce a false positive; the
			// producer recorded the trip.
			if r == core.ReasonMalformed {
				report(oi, "/DestOutputProfile ICC data cannot be decoded (malformed stream data)")
			}
			continue
		}
		if len(data) < 128 {
			report(oi, "/DestOutputProfile ICC data too short (%d bytes, minimum 128)", len(data))
			continue
		}
		// ICC profile header: bytes 16-19 contain color space signature
		cs := string(data[16:20])
		expectedN := 0
		switch cs {
		case "GRAY":
			expectedN = 1
		case "RGB ":
			expectedN = 3
		case "CMYK":
			expectedN = 4
		default:
			report(oi, "ICC profile has unsupported color space %q", cs)
		}
		if expectedN > 0 && int(nVal) != expectedN {
			report(oi, "/N=%d does not match ICC profile color space %s (expected %d)", nVal, cs, expectedN)
		}
		// ICC profile header: bytes 12-15 contain device class. Output intent
		// profiles must be of class "mntr" (monitor), "prtr" (printer), or
		// "spac" (color space conversion).
		switch cls := string(data[12:16]); cls {
		case "mntr", "prtr", "spac":
		default:
			report(oi, "ICC profile has invalid device class %q (must be mntr, prtr, or spac)", cls)
		}
		// ICC profile version (bytes 8-11): at most 2.x at part 1, which is
		// based on PDF 1.4, and 4.x at parts 2, 3 and 4 (veraPDF 6.2.2 t01,
		// 6.2.3 t01: version < 3.0, version < 5.0). Part 4 was not checked
		// here; a v5 output-intent profile at 4 was reported only because the
		// ICCBased rule took it for a colour space.
		major, minor := data[8], data[9]>>4
		switch {
		case level.Part() == 1 && major > 2:
			report(oi, "ICC profile version %d.%d not allowed for %s (max 2.x)", major, minor, level)
		case level.Part() > 1 && major > 4:
			report(oi, "ICC profile version %d.%d not allowed for %s (max 4.x)", major, minor, level)
		}
	}
	return errs
}

func checkNoCatalogAA(doc core.View, level Level) []Violation {
	if level.Part() == 4 {
		return nil // PDF/A-4 does not restrict /AA in catalog
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	if catalog.Get("AA") != nil {
		errs = append(errs, Violation{
			Rule:    annotActionClause("catalogAA", level),
			Level:   level,
			Message: "catalog must not contain /AA (additional actions)",
		})
	}
	// Page dictionaries are equally forbidden from carrying /AA at 1b/2b/3b
	// (ISO 19005-2, 6.6.2); previously only the catalog was checked.
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		if page.Dict.Get("AA") != nil {
			errs = append(errs, Violation{
				Rule:    annotActionClause("catalogAA", level),
				Level:   level,
				Message: "page dictionary must not contain /AA (additional actions)",
				Object:  page.ObjNum,
			})
		}
	}
	return errs
}

func checkNoOCProperties(doc core.View, level Level) []Violation {
	if level.Part() != 1 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	if catalog.Get("OCProperties") != nil {
		return []Violation{{
			Rule:    "6.1.13",
			Level:   level,
			Message: "catalog must not contain /OCProperties (optional content, PDF/A-1b)",
		}}
	}
	return nil
}

// Rule 6.1.12: Perms dictionary may only contain UR3 and DocMDP keys.
func checkPermsDict(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
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

	var errs []Violation
	for key := range permsDict.Keys() {
		if key != "UR3" && key != "DocMDP" {
			errs = append(errs, Violation{
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
		// Resolve returns a non-reference as it stands, so these two reach a
		// direct array and direct dictionaries without a fallback. The
		// fallbacks that used to be here could only fire when the assertion
		// they retried had already failed for the same reason, and the second
		// one reinstated a nil dictionary that the check below would then
		// dereference.
		refArr, _ := doc.Resolve(sigDict.Get("Reference")).(object.Array)
		for _, el := range refArr {
			refDict := doc.ResolveDict(el)
			if refDict == nil {
				continue
			}
			for _, forbidden := range []object.Name{"DigestLocation", "DigestMethod", "DigestValue"} {
				if refDict.Get(forbidden) != nil {
					errs = append(errs, Violation{
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

// filterClause returns the stream-filter rule's ISO clause for the level: only
// the standard filters (Table 6) are permitted, so LZWDecode and any
// non-standard name are rejected. ISO 19005-1 6.1.10; -2/-3 6.1.7.2; -4 6.1.6.2.
func filterClause(level Level) string {
	switch level.Part() {
	case 1:
		return "6.1.10"
	case 4:
		return "6.1.6.2"
	default:
		return "6.1.7.2"
	}
}

// Rule: only the standard stream filters may be used; LZWDecode is prohibited.
func checkNoLZW(doc core.View, level Level) []Violation {
	var errs []Violation
	// allobjects: a stream's filter is file syntax, required of every stream
	// the file holds whether or not the document uses it.
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if hasFilter(doc, stream, "LZWDecode") {
			errs = append(errs, Violation{
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
		if level.Part() == 1 && hasFilter(doc, stream, "JPXDecode") {
			errs = append(errs, Violation{
				Rule:    filterClause(level),
				Level:   level,
				Message: "stream must not use /JPXDecode filter (not permitted in PDF/A-1)",
				Object:  num,
			})
		}
		// Check for non-standard filter names
		if badFilter := getNonStandardFilter(doc, stream); badFilter != "" {
			errs = append(errs, Violation{
				Rule:    filterClause(level),
				Level:   level,
				Message: fmt.Sprintf("stream uses non-standard filter /%s", badFilter),
				Object:  num,
			})
		}
	}
	return errs
}

// checkSignatureContents enforces the parts of 6.4.3 (PDF/A-2/-3) that are
// about a signature dictionary's values: /ByteRange is four integers that
// start at byte 0 with the two covered segments in order, and a PKCS#7/CMS
// /Contents embeds the signing certificate and holds exactly one SignerInfo.
// That the range reaches the end of the file is a fact about the file's
// bytes, and checkSignatureCoversFile's.
func checkSignatureContents(doc core.View, level Level) []Violation {
	if level.Part() != 2 && level.Part() != 3 {
		return nil
	}
	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		dict, num := r.Dict, r.ObjNum
		if r.Stream != nil {
			continue
		}
		// A signature dictionary carries both /ByteRange and /Contents; that
		// pairing is unique to signatures (and document timestamps).
		brObj := dict.Get("ByteRange")
		if brObj == nil || dict.Get("Contents") == nil {
			continue
		}
		if t, _ := doc.ResolveName(dict.Get("Type")); t != "" && t != "Sig" && t != "DocTimeStamp" {
			continue
		}

		bad := func(msg string) {
			errs = append(errs, Violation{Rule: "6.4.3", Level: level, Message: msg, Object: num})
		}
		// The arithmetic on these file-controlled integers is core.ByteRange's,
		// shared with signature verification: start+len computed unchecked
		// overflowed there into a panic (audit 2026-09-22 C5), and here into a
		// wrapped sum.
		br, ok := core.ReadByteRange(doc, brObj)
		if !ok {
			bad("signature /ByteRange must be an array of four integers")
			continue
		}
		// [start1, len1, start2, len2]: the digest covers [start1,start1+len1)
		// and [start2,start2+len2); the hole between them is the /Contents value.
		if !br.Ordered() {
			bad("signature /ByteRange does not cover the document from its start")
			continue
		}

		// The PKCS#7/CMS signature blob in /Contents must embed the signing
		// certificate and hold exactly one SignerInfo. Only applies when the blob
		// parses as CMS SignedData — an adbe.x509.rsa_sha1 signature stores a raw
		// value and its certificate in /Cert instead.
		if c, ok := doc.Resolve(dict.Get("Contents")).(object.String); ok { // string: a signature's /Contents is never encrypted (ISO 32000-2 7.6.2)
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

// checkSignatureCoversFile enforces the byte half of 6.4.3 (PDF/A-2/-3): a
// signature's digest must be computed over the entire file, so its signed
// range must reach the end of the file, or the trailing bytes are unsigned.
// The ranges are the file's own, as Read found them (FileRecord.Signatures);
// a malformed or out-of-order range is checkSignatureContents' finding. Which
// of them are the document's signatures is a question about the graph — a
// signature dictionary nothing reaches signs nothing, the reading
// checkSignatureContents takes too (audit 2026-09-22 C83) — so the record's
// signatures are those whose objects the document reaches.
//
// A range that meets or exceeds the file length covers it: the veraPDF corpus
// carries stub signatures whose /ByteRange overshoots the truncated test file,
// and those are treated as covering (not a defect). An end too large for an
// int64 overshoots every file.
func checkSignatureCoversFile(doc core.View, f *core.FileRecord, level Level) []Violation {
	if level.Part() != 2 && level.Part() != 3 || len(f.Signatures) == 0 {
		return nil
	}
	reached := make(map[int]bool)
	for _, num := range doc.ReachableObjectNums() {
		reached[num] = true
	}
	var errs []Violation
	for _, sig := range f.Signatures {
		if !reached[sig.Num] || !sig.OK || !sig.ByteRange.Ordered() {
			continue
		}
		if end, fits := sig.ByteRange.End(); fits && end < int64(len(f.Data)) {
			errs = append(errs, Violation{Rule: "6.4.3", Level: level, Message: "signature /ByteRange does not cover the entire document", Object: sig.Num})
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

// getNonStandardFilter returns the first filter on the stream that is not a
// standard one, or "". The chain is read through StreamFilters, which resolves
// the /Filter value and each name in it: `/Filter 7 0 R` naming /LZWDecode is
// as much an LZW stream as the direct spelling (audit C36).
func getNonStandardFilter(doc core.View, stream *object.Stream) string {
	for _, name := range doc.StreamFilters(stream) {
		if !isStandardFilter(name) {
			return string(name)
		}
	}
	return ""
}

// hasFilter reports whether filterName is anywhere in the stream's filter
// chain, resolved as getNonStandardFilter's is.
func hasFilter(doc core.View, stream *object.Stream, filterName string) bool {
	for _, name := range doc.StreamFilters(stream) {
		if string(name) == filterName {
			return true
		}
	}
	return false
}

// Rule 6.1.6.1-2: Stream dict cannot contain F, FFilter, or FDecodeParms.
func checkNoExternalStreams(doc core.View, level Level) []Violation {
	var errs []Violation
	// allobjects: the stream dictionary's keys are file syntax, required of
	// every stream the file holds whether or not the document uses it.
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		for _, key := range []object.Name{"F", "FFilter", "FDecodeParms"} {
			if stream.Dict.Get(key) != nil {
				errs = append(errs, Violation{
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
func checkFontsEmbedded(doc core.View, level Level) []Violation {
	var errs []Violation

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
	for fontDict, objNum := range fonts {
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
// Violation.Object; dictObjNum itself reports -1 on miss.
func objNumForDict(doc core.View, dict *object.Dictionary) int {
	return doc.ObjNumOf(dict)
}

// fontObjNum returns the object number of a font dictionary, or 0 if it is a
// direct dictionary with no indirect identity.
func fontObjNum(doc core.View, fontDict *object.Dictionary) int {
	return objNumForDict(doc, fontDict)
}

// checkOneFontEmbedded applies the 6.2.10 embedding rule to a single font
// dictionary.
func checkOneFontEmbedded(doc core.View, fontDict *object.Dictionary, objNum int, level Level) []Violation {
	subtypeName, _ := doc.ResolveName(fontDict.Get("Subtype"))

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
		return []Violation{{
			Rule:    fontClause("embed", level),
			Level:   level,
			Message: "font must have a /FontDescriptor",
			Object:  objNum,
		}}
	}

	fd := doc.ResolveDict(fdRef)
	if fd == nil {
		return []Violation{{
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
	if bn, ok := doc.ResolveName(fontDict.Get("BaseFont")); ok {
		baseFontName = string(bn)
	}
	return []Violation{{
		Rule:    fontClause("embed", level),
		Level:   level,
		Message: fmt.Sprintf("font %s must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)", baseFontName),
		Object:  objNum,
	}}
}

// collectFonts returns every font dictionary named in the page tree's
// /Resources, each once, with the object number that holds it — 0 for a font
// written directly inside a /Font dictionary.
//
// A font is identified by its dictionary, not by a number. Keyed by number, a
// direct font had none, so each sighting got a fresh negative one: the same
// font was reported once per page that reached it, as "object -1", "object
// -2"... (audit 2026-09-22 C138).
func collectFonts(doc core.View, pageTreeRef object.Object) map[*object.Dictionary]int {
	fonts := make(map[*object.Dictionary]int)
	collectFontsRecursive(doc, pageTreeRef, fonts, make(map[int]bool), 0)
	return fonts
}

func collectFontsRecursive(doc core.View, ref object.Object, fonts map[*object.Dictionary]int, seen map[int]bool, depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
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

	nodeType, _ := doc.ResolveName(node.Get("Type"))

	if nodeType == "Pages" {
		kidsObj := doc.Resolve(node.Get("Kids"))
		if kids, ok := kidsObj.(object.Array); ok {
			for _, kid := range kids {
				collectFontsRecursive(doc, kid, fonts, seen, depth+1)
			}
		}
		collectFontsFromResources(doc, node, fonts)
	} else if nodeType == "Page" {
		collectFontsFromResources(doc, node, fonts)
	}
}

func collectFontsFromResources(doc core.View, pageOrPages *object.Dictionary, fonts map[*object.Dictionary]int) {
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

	for fontRef := range fontDict.Values() {
		fd := doc.ResolveDict(fontRef)
		if fd == nil {
			continue
		}
		if _, seen := fonts[fd]; !seen {
			fonts[fd] = resolveObjNum(doc, fontRef)
		}
	}
}

// --- Annotation checks (6.3) ---

// Allowed annotation subtypes per part of ISO 19005 (the conformance level
// does not change them). Rule 6.3.1-1.
var allowedAnnotSubtypes = map[int]map[object.Name]bool{
	4: {
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
	allowedAnnotSubtypes[2] = pdfa2bAnnots
	allowedAnnotSubtypes[3] = pdfa2bAnnots

	// PDF/A-1b allowed subtypes (per ISO 19005-1 clause 6.5.1)
	allowedAnnotSubtypes[1] = map[object.Name]bool{
		"Text": true, "Link": true, "FreeText": true, "Line": true,
		"Square": true, "Circle": true, "Highlight": true, "Underline": true,
		"Squiggly": true, "StrikeOut": true, "Stamp": true, "Ink": true,
		"Popup": true, "Widget": true, "PrinterMark": true, "TrapNet": true,
	}
}

// annotOccurrence is one annotation dictionary paired with the object number
// used for error attribution: the annotation's own number, or, for an
// annotation written directly inside another object (a page's /Annots, say),
// that object's number.
type annotOccurrence struct {
	dict *object.Dictionary
	num  int
}

// reachableAnnotations returns every annotation the document reaches, direct
// ones included, each once. It replaces a scan of the object table plus a
// second pass for the direct annotations of page /Annots arrays (audit A9):
// the table scan also judged annotations nothing refers to, and the second
// pass missed a direct annotation anywhere but a page's own /Annots
// (audit 2026-09-22 C83).
func reachableAnnotations(doc core.View) []annotOccurrence {
	if c := pdfaMemo(doc); true && c.hasAnnots {
		return c.annots
	}
	var out []annotOccurrence
	for _, r := range doc.ReachableDicts() {
		if r.Stream == nil && doc.IsAnnotation(r.Dict) {
			out = append(out, annotOccurrence{dict: r.Dict, num: r.ObjNum})
		}
	}
	if c := pdfaMemo(doc); true {
		c.annots = out
		c.hasAnnots = true
	}
	return out
}

// resolveName resolves obj (following an indirect reference) and returns it as
// a Name. Rules must resolve before type-asserting: a value placed behind an
// indirect reference — e.g. /Subtype 12 0 R — would otherwise silently evade
// the check (audit C12).
func resolveName(doc core.View, obj object.Object) (object.Name, bool) {
	n, ok := doc.Resolve(obj).(object.Name)
	return n, ok
}

func checkAnnotationSubtypes(doc core.View, level Level) []Violation {
	allowed, ok := allowedAnnotSubtypes[level.Part()]
	if !ok {
		return nil
	}
	// PDF/A-4e permits 3D and RichMedia annotations (they carry the embedded
	// 3D/multimedia content that "e" stands for); plain PDF/A-4 forbids them.
	extra := map[object.Name]bool{}
	if level.variant() == "E" {
		extra["3D"] = true
		extra["RichMedia"] = true
	}

	var errs []Violation
	check := func(dict *object.Dictionary, num int) {
		st, ok := resolveName(doc, dict.Get("Subtype"))
		if !ok {
			return
		}
		if !allowed[st] && !extra[st] {
			errs = append(errs, Violation{
				Rule:    annotActionClause("subtype", level),
				Level:   level,
				Message: fmt.Sprintf("annotation subtype /%s is not allowed in %s", string(st), level),
				Object:  num,
			})
		}
	}
	for _, a := range reachableAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.3.2-1/2: Non-Popup annotations require F key; flags must have Print set,
// Hidden/Invisible/ToggleNoView/NoView clear.
func checkAnnotationFlags(doc core.View, level Level) []Violation {
	var errs []Violation
	check := func(dict *object.Dictionary, num int) {
		// 6.5.3: at PDF/A-1, an annotation's /CA (constant opacity) must be 1.0
		// — annotation transparency is not permitted. This applies to every
		// annotation subtype, so it precedes the Popup exemption below.
		if level.Part() == 1 {
			if ca, ok := doc.ResolveNumber(dict.Get("CA")); ok && math.Abs(ca-1.0) > 1e-6 {
				errs = append(errs, Violation{
					Rule:    "6.5.3",
					Level:   level,
					Message: "annotation /CA (opacity) must be 1.0",
					Object:  num,
				})
			}
		}

		// Popup annotations are exempt from F requirement
		st, _ := doc.ResolveName(dict.Get("Subtype"))
		if st == "Popup" {
			return
		}

		fObj := dict.Get("F")
		if fObj == nil {
			errs = append(errs, Violation{
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
			errs = append(errs, Violation{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must have Print bit set",
				Object:  num,
			})
		}
		if int(flags)&flagHidden != 0 {
			errs = append(errs, Violation{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have Hidden bit set",
				Object:  num,
			})
		}
		if int(flags)&flagInvisible != 0 {
			errs = append(errs, Violation{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have Invisible bit set",
				Object:  num,
			})
		}
		if int(flags)&flagNoView != 0 {
			errs = append(errs, Violation{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have NoView bit set",
				Object:  num,
			})
		}
		if int(flags)&flagToggleNoView != 0 {
			errs = append(errs, Violation{
				Rule:    annotActionClause("flags", level),
				Level:   level,
				Message: "annotation /F must not have ToggleNoView bit set",
				Object:  num,
			})
		}
	}
	for _, a := range reachableAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// Rule 6.3.3-1: Annotations need AP except Popup, Link, Projection, and zero-area rects.
func checkAnnotationAppearance(doc core.View, level Level) []Violation {
	var errs []Violation
	// The colour space of the catalog's PDF/A output intent profile, read once:
	// "" with known=true when there is no such intent; known=false when there
	// is one whose profile pdf0 could not read.
	var oiSpace string
	var oiKnown, oiRead bool
	pdfa1OutputIntentSpace := func() (string, bool) {
		if !oiRead {
			oiRead = true
			oiKnown = true
			if cat := doc.Catalog(); cat != nil {
				if p := pdfaOutputIntentProfile(doc, cat); p != nil {
					data, _ := doc.ICCProfileData(p) // reason: an unread profile is "not known" below and the check declines; the producer recorded any declined trip
					if len(data) < 20 {
						oiKnown = false
					} else {
						oiSpace = string(data[16:20])
					}
				}
			}
		}
		return oiSpace, oiKnown
	}
	check := func(dict *object.Dictionary, num int) {
		st, _ := doc.ResolveName(dict.Get("Subtype"))

		// Exempt subtypes
		if st == "Popup" || st == "Link" || st == "Projection" {
			return
		}

		// Exempt an annotation whose rectangle has no size at all
		if isZeroSizeRect(doc, dict.Get("Rect")) {
			return
		}

		ap := dict.Get("AP")
		if ap == nil {
			errs = append(errs, Violation{
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
			errs = append(errs, Violation{
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
			errs = append(errs, Violation{
				Rule:    annotActionClause("appearance", level),
				Level:   level,
				Message: "annotation appearance dictionary must contain only the /N entry (not /D or /R)",
				Object:  num,
			})
		}

		// For a Widget of button field type (FT Btn), the N appearance shall
		// be a sub-dictionary of appearance states, not a single stream; for
		// every other annotation it shall be an appearance stream (ISO 19005-1
		// 6.5.3 as corrected, -2/-3 6.3.3; Isartor 6-5-3-t04-fail-d).
		if st == "Widget" && annotFieldType(doc, dict) == "Btn" {
			if _, ok := doc.Resolve(apDict.Get("N")).(*object.Dictionary); !ok {
				errs = append(errs, Violation{
					Rule:    annotActionClause("appearance", level),
					Level:   level,
					Message: "button Widget /AP /N must be an appearance sub-dictionary of states, not a stream",
					Object:  num,
				})
			}
		} else if n := doc.Resolve(apDict.Get("N")); n != nil {
			if _, ok := n.(*object.Stream); !ok {
				errs = append(errs, Violation{
					Rule:    annotActionClause("appearance", level),
					Level:   level,
					Message: "annotation /AP /N must be an appearance stream (only a button Widget's is a dictionary of states)",
					Object:  num,
				})
			}
		}

		// PDF/A-1: an annotation may carry a colour (/C) or interior colour
		// (/IC) — which are given in DeviceRGB — only when the PDF/A output
		// intent's destination profile is RGB (ISO 19005-1 6.5.3). An output
		// intent whose profile could not be read is not judged.
		if level.Part() == 1 && (dict.Get("C") != nil || dict.Get("IC") != nil) {
			space, known := pdfa1OutputIntentSpace()
			if known && space != "RGB " {
				why := "there is no PDF/A output intent"
				if space != "" {
					why = fmt.Sprintf("the PDF/A output intent's profile is %q, not RGB", strings.TrimSpace(space))
				}
				errs = append(errs, Violation{
					Rule:    annotActionClause("appearance", level),
					Level:   level,
					Message: "annotation /C or /IC colour is present but " + why,
					Object:  num,
				})
			}
		}
	}
	for _, a := range reachableAnnotations(doc) {
		check(a.dict, a.num)
	}
	return errs
}

// annotFieldType returns the form field type (FT) governing a widget
// annotation: its own FT, or an inherited one from its /Parent field chain.
func annotFieldType(doc core.View, dict *object.Dictionary) object.Name {
	node := dict
	for hops := 0; node != nil && hops < 32; hops++ {
		if ft, ok := doc.ResolveName(node.Get("FT")); ok {
			return ft
		}
		node = doc.ResolveDict(node.Get("Parent"))
	}
	return ""
}

// isZeroSizeRect reports whether a /Rect has neither width nor height — the
// annotations 6.3.3 (PDF/A-1 6.5.3) exempts from needing an appearance. Both,
// not either: the veraPDF rule is (width == 0 && height == 0), and a
// FileAttachment annotation 50 points tall and 0 wide without /AP is a failing
// file in the corpus (PDF_A-2b 6-3-3-t01-fail-p), which "either" exempted.
// That went unnoticed while an over-broad /UF rule happened to reject the file
// for a reason veraPDF does not share. The rectangle and each coordinate are
// resolved: an indirect /Rect used to read as "not a rectangle", which
// reported a zero-area annotation as missing its /AP.
func isZeroSizeRect(doc core.View, obj object.Object) bool {
	arr, ok := doc.Resolve(obj).(object.Array)
	if !ok || len(arr) != 4 {
		return false
	}
	vals := make([]float64, 4)
	for i, elem := range arr {
		v, ok := doc.ResolveNumber(elem)
		if !ok {
			return false
		}
		vals[i] = v
	}
	return (vals[2]-vals[0]) == 0 && (vals[3]-vals[1]) == 0
}

// --- Interactive forms (6.4) ---

// Rule 6.4.1-1: Widget annotation cannot contain A key.
func checkWidgetNoAction(doc core.View, level Level) []Violation {
	var errs []Violation
	check := func(dict *object.Dictionary, num int) {
		st, _ := doc.ResolveName(dict.Get("Subtype"))
		if st != "Widget" {
			return
		}
		if dict.Get("A") != nil {
			errs = append(errs, Violation{
				Rule:    annotActionClause("widget", level),
				Level:   level,
				Message: "Widget annotation must not contain /A key",
				Object:  num,
			})
		}
	}
	for _, r := range doc.ReachableDicts() {
		if r.Stream == nil {
			check(r.Dict, r.ObjNum)
		}
	}
	return errs
}

// Clause 6.4.2 forbids XFA, and says so about two different keys.
//
//   - testNumber 1: the interactive form dictionary shall not contain /XFA.
//   - testNumber 2: the document catalog's /NeedsRendering shall not be true.
//     That is the flag saying the form is a dynamic XFA one whose real content
//     is the XML rather than the page, so a viewer that honours it shows
//     something the PDF does not contain.
//
// The second was missing. It went unnoticed because the only corpus file that
// isolates it — PDF_A-4 6-4-2-t01-fail-b, which sets the flag and carries no
// /XFA — was being failed by an unrelated false positive about Type 1 glyph
// widths, and fixing that upstream left the file passing.
//
// The value is what counts, not the key: veraPDF's test is
// `NeedsRendering == false`, so an explicit false is as good as an absence.
// The profiles carry this at PDF/A-2, -3 and -4 and not at PDF/A-1, which is
// based on PDF 1.4, before the key existed.
func checkNoXFA(doc core.View, level Level) []Violation {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}

	var errs []Violation
	if level.Part() != 1 {
		if nr, ok := doc.ResolveBool(catalog.Get("NeedsRendering")); ok && bool(nr) {
			errs = append(errs, Violation{
				Rule:    "6.4.2",
				Level:   level,
				Message: "document catalog /NeedsRendering must not be true (dynamic XFA form)",
			})
		}
	}

	// /NeedsRendering lives in the catalog and does not need a form to be
	// there, so the XFA check is what the missing /AcroForm returns from.
	if af := doc.ResolveDict(catalog.Get("AcroForm")); af != nil && af.Get("XFA") != nil {
		errs = append(errs, Violation{
			Rule:    "6.4.2",
			Level:   level,
			Message: "AcroForm must not contain /XFA",
		})
	}
	return errs
}

// Rule 6.4.1-2: NeedAppearances flag must be absent or false.
func checkNeedAppearances(doc core.View, level Level) []Violation {
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
	if doc.IsTrue(af.Get("NeedAppearances")) {
		return []Violation{{
			Rule:    "6.4.1",
			Level:   level,
			Message: "NeedAppearances must be false",
		}}
	}
	return nil
}

// --- Action checks (6.6) ---

// Forbidden action types by level per ISO 19005, rule 6.6.1-1. The tables are
// package variables: building them on every call was about a quarter of the
// CPU of a document with many actions (audit 2026-09-22 C37).
var (
	// Universally forbidden across all PDF/A levels.
	universallyForbiddenActions = map[object.Name]bool{
		"Launch":     true,
		"Sound":      true,
		"Movie":      true,
		"ResetForm":  true,
		"ImportData": true,
		"Hide":       true,
		"Rendition":  true,
		"Trans":      true,
	}
	// Additionally forbidden in parts 1-3.
	forbiddenActions123 = map[object.Name]bool{
		"JavaScript":  true,
		"SetOCGState": true,
		"GoTo3DView":  true,
		"GoToDp":      true,
		"SetState":    true,
		"NOP":         true,
	}
	// Additionally forbidden in part 4.
	forbiddenActions4 = map[object.Name]bool{
		"SetOCGState": true,
		"GoTo3DView":  true,
		"SetState":    true,
		"NOP":         true,
	}
)

func isForbiddenAction(s object.Name, level Level) bool {
	if universallyForbiddenActions[s] {
		return true
	}

	switch level.Part() {
	case 1, 2, 3:
		return forbiddenActions123[s]
	case 4:
		// PDF/A-4e permits the 3D/multimedia navigation actions SetOCGState and
		// GoTo3DView; plain PDF/A-4 forbids them. SetState/NOP (deprecated) stay
		// forbidden at every part-4 conformance.
		if level.variant() == "E" {
			return s == "SetState" || s == "NOP"
		}
		return forbiddenActions4[s]
	}
	return false
}

func checkNoForbiddenActions(doc core.View, level Level) []Violation {
	var errs []Violation

	// Check catalog /OpenAction
	catalog := doc.Catalog()
	if catalog != nil {
		oaRef := catalog.Get("OpenAction")
		if oaRef != nil {
			errs = append(errs, checkActionObject(doc, oaRef, 0, level)...)
		}
	}

	// Check every dictionary the document reaches for /A and for being an
	// action itself. Direct dictionaries are included — an inline /A, or an
	// action written directly in an /AA, is the shape most producers write —
	// and an orphan action nothing reaches is not part of the document
	// (audit 2026-09-22 C83).
	//
	// Each action is judged once. One reached through a holder's /A (or the
	// /Next chain behind it) is judged there, under the holder's number, and
	// not again as a dictionary in its own right — whether it is written
	// inline or as an object of its own, which is the same document and must
	// get the same findings (TestFindingsAreInvariantUnderIndirection). So
	// every /A is followed first, and the standalone pass skips what it
	// reached.
	dicts := doc.ReachableDicts()
	viaA := map[*object.Dictionary]bool{}
	for _, r := range dicts {
		doc.Charge(1)
		if r.Stream != nil {
			continue
		}
		if aRef := r.Dict.Get("A"); aRef != nil {
			checkActionChain(doc, aRef, r.ObjNum, level, &errs, viaA)
		}
	}
	for _, r := range dicts {
		doc.Charge(1)
		dict, num := r.Dict, r.ObjNum
		if r.Stream != nil || viaA[dict] {
			continue
		}

		// Check if the object itself is an action dict (has /S and /Type=Action or no /Type)
		if s, ok := doc.ResolveName(dict.Get("S")); ok {
			typeObj := doc.Resolve(dict.Get("Type"))
			isAction := typeObj == nil || typeObj == object.Name("Action")
			if isAction && isForbiddenAction(s, level) {
				errs = append(errs, Violation{
					Rule:    annotActionClause("forbidden", level),
					Level:   level,
					Message: fmt.Sprintf("forbidden action type /%s", string(s)),
					Object:  num,
				})
			}
		}
	}
	return errs
}

func checkActionObject(doc core.View, ref object.Object, objNum int, level Level) []Violation {
	var errs []Violation
	checkActionChain(doc, ref, objNum, level, &errs, make(map[*object.Dictionary]bool))
	return errs
}

// checkActionChain validates one action dictionary and follows its /Next
// entry (a single action or an array of actions), which previous versions
// ignored entirely — a legal action whose /Next launches JavaScript passed.
//
// The walk is iterative, in the order the recursive one took (a /Next array's
// actions pushed in reverse): a /Next chain is as long as the file makes it,
// and a 20,000-long one overflowed a 16 MB stack (audit 2026-09-22 C37). Each
// action visited is charged to the run's work meter; seen, shared by every
// holder in checkNoForbiddenActions, is what visits each action once.
func checkActionChain(doc core.View, ref object.Object, objNum int, level Level, errs *[]Violation, seen map[*object.Dictionary]bool) {
	stack := []object.Object{ref}
	for len(stack) > 0 {
		ref := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		doc.Charge(1)
		// ref might be an action dict or an array (for OpenAction destination)
		actionDict := doc.ResolveDict(ref)
		if actionDict == nil || seen[actionDict] {
			continue // destination array, unresolvable, or a /Next cycle
		}
		seen[actionDict] = true

		if s, ok := doc.ResolveName(actionDict.Get("S")); ok && isForbiddenAction(s, level) {
			*errs = append(*errs, Violation{
				Rule:    annotActionClause("forbidden", level),
				Level:   level,
				Message: fmt.Sprintf("forbidden action type /%s", string(s)),
				Object:  objNum,
			})
		}

		switch next := doc.Resolve(actionDict.Get("Next")).(type) {
		case *object.Dictionary:
			stack = append(stack, next)
		case object.Array:
			for i := len(next) - 1; i >= 0; i-- {
				stack = append(stack, next[i])
			}
		}
	}
}

// Rule 6.6.1-2: Named actions limited to NextPage, PrevPage, FirstPage, LastPage.
func checkNamedActions(doc core.View, level Level) []Violation {
	allowedNames := map[string]bool{
		"NextPage":  true,
		"PrevPage":  true,
		"FirstPage": true,
		"LastPage":  true,
	}

	var errs []Violation
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
			errs = append(errs, Violation{
				Rule:    annotActionClause("forbidden", level),
				Level:   level,
				Message: fmt.Sprintf("named action /%s not allowed (only NextPage, PrevPage, FirstPage, LastPage)", string(nName)),
				Object:  num,
			})
		}
	}
	// Every action the document reaches, direct ones included.
	for _, r := range doc.ReachableDicts() {
		if r.Stream == nil {
			check(r.Dict, r.ObjNum)
		}
	}
	return errs
}

// Rule 6.6.3-1: Widget/FormField AA is level-gated.
// For PDF/A-1b/2b/3b: no /AA on widgets or form fields.
// For PDF/A-4: AA allowed on widgets/form fields (trigger events).
// Non-widget AA (doc/page/annot) keys restricted to: E, X, D, U, Fo, Bl.
func checkAnnotationAA(doc core.View, level Level) []Violation {
	if level.Part() == 4 {
		return nil // PDF/A-4 gates trigger events per-event; see checkA4TriggerEvents
	}

	// The clause is the widget/form-field additional-actions rule: 6.6.2 at
	// PDF/A-1 and 6.4.1 at -2 and -3, which annotActionClause has under
	// "widget". It was hardcoded to 6.6.3, which is PDF/A-4's — a level this
	// function returns nil for — and the comment here cited 6.5.3 and 6.3.3,
	// which are the annotation rules about CA, F, C/IC and AP and say nothing
	// about AA. Three different wrong numbers for one report.
	//
	// The scope stays wider than veraPDF's, which writes the rule against
	// PDWidgetAnnot and PDFormField: this reports /AA on any annotation. The
	// corpus is the oracle on that and it holds at FP=0 either way, so the
	// broader reading keeps whatever it catches.
	clause := annotActionClause("widget", level)
	var errs []Violation
	check := func(dict *object.Dictionary, num int) {
		if dict.Get("AA") != nil {
			errs = append(errs, Violation{
				Rule:    clause,
				Level:   level,
				Message: "annotation must not have /AA (additional-actions)",
				Object:  num,
			})
		}
	}
	for _, r := range doc.ReachableDicts() {
		if r.Stream == nil && (doc.IsAnnotation(r.Dict) || isWidgetOrField(doc, r.Dict)) {
			check(r.Dict, r.ObjNum)
		}
	}
	return errs
}

// isWidgetOrField reports whether dict is a widget annotation or an interactive
// form field, which the /AA prohibition also covers and which need not carry
// the /Rect that core.IsAnnotation looks for.
func isWidgetOrField(doc core.View, dict *object.Dictionary) bool {
	if st, ok := doc.ResolveName(dict.Get("Subtype")); ok && st == "Widget" {
		return true
	}
	return dict.Get("FT") != nil
}

// --- Metadata checks (6.7) ---

// metadataClause returns the ISO clause for a metadata-rule concept at the
// given level. Metadata requirements are numbered differently per part (ISO
// 19005-1 6.7.x; -2/-3 6.6.x; -4 6.7.x); clauses follow the veraPDF profiles.
func metadataClause(concept string, level Level) string {
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
	switch level.Part() {
	case 1:
		return c[0]
	case 4:
		return c[2]
	default:
		return c[1]
	}
}

// checkIdentification is the one rule that compares what a document declares
// about itself — its pdfaid identification, read through the XMP model — with
// the target it is validated against (ISO 19005-1 6.7.11, -2/-3 6.6.4, -4
// 6.7.3).
//
// It is the only place the declaration is judged. The part must be the
// target's; the conformance letter must be one the target accepts, and which
// ones that is — the hierarchy that lets a 2b target accept a 2u or 2a file —
// is written down once, in Level.acceptsConformance; part 4 must also carry
// its revision. Nothing else in the package reads the declaration to decide
// what to check: the target alone does that.
func checkIdentification(doc core.View, level Level) []Violation {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	if _, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream); !ok {
		return nil // already reported by checkMetadataStream
	}

	id := readPDFAIdentification(doc)
	switch id.status {
	case core.XMPLimit:
		// Not modelled; the trip is on the run and becomes a "limit" finding.
		// Reporting the identification missing would be a guess.
		return nil
	case core.XMPMalformed:
		// The packet is not XML, which checkXMPWellFormed reports. It
		// identifies nothing, and saying so again per property adds nothing.
		return nil
	}
	rule := metadataClause("version", level)
	var errs []Violation
	report := func(check, msg string) {
		errs = append(errs, Violation{Rule: rule, Level: level, Message: msg, Check: check})
	}

	// A property written as pdfaid: whose prefix means some other namespace is
	// not an identification at all, however much it looks like one.
	if id.impostor {
		report("", "pdfaid namespace must be http://www.aiim.org/pdfa/ns/id/")
		return errs
	}
	// The identification schema's prefix is normative, not a convention
	// (ISO 19005-1 Table 3; -2/-3 6.6.4; -4 6.7.3).
	for _, p := range id.otherPrefixes {
		report("", fmt.Sprintf("the PDF/A identification schema must use the namespace prefix pdfaid, found %q", p))
	}

	switch want := strconv.Itoa(level.Part()); {
	case !id.hasPart || id.part == "":
		report("", "metadata must contain pdfaid:part")
	case id.part != want:
		report("", fmt.Sprintf("pdfaid:part must be %s, got %s", want, id.part))
	}

	if !level.acceptsConformance(id.conformance, id.hasConformance) {
		var msg string
		switch {
		case level.Part() == 4 && level.Conformance() == "":
			// Base rule 6.7.3-3: a file conforming to neither variant shall
			// not provide a conformance entry.
			msg = fmt.Sprintf("pdfaid:conformance is %q; plain %s declares none", id.conformance, level)
			if id.conformance == "E" || id.conformance == "F" {
				msg += fmt.Sprintf(" (the document identifies itself as PDF/A-4%s, a level of its own)", strings.ToLower(id.conformance))
			}
		case !id.hasConformance && level.Part() == 4:
			// The case the variants are levels for: a conforming plain
			// PDF/A-4 file is not the variant file the caller asked for.
			msg = fmt.Sprintf("the document declares no pdfaid:conformance, so it identifies itself as plain PDF/A-4 rather than %s, which must declare %s", level, level.acceptedConformance())
		case !id.hasConformance:
			msg = fmt.Sprintf("metadata must declare pdfaid:conformance; %s accepts %s", level, level.acceptedConformance())
		default:
			// Including the right letter in the wrong case, which is the
			// whole of the difference for a case-sensitive property.
			msg = fmt.Sprintf("pdfaid:conformance is %q; %s accepts %s", id.conformance, level, level.acceptedConformance())
		}
		report(CheckPDFAIDConformance, msg)
	}

	if rev := level.revision(); rev != "" {
		switch {
		case id.rev == "":
			report("", fmt.Sprintf("%s metadata must contain pdfaid:rev", level))
		case id.rev != rev:
			report("", fmt.Sprintf("pdfaid:rev must be %s, got %q", rev, id.rev))
		}
	}
	return errs
}

// --- Transparency checks (PDFA-1b only) ---

func checkNoTransparency(doc core.View, level Level) []Violation {
	if level.Part() != 1 {
		return nil
	}

	var errs []Violation

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
			s, _ := doc.ResolveName(groupDict.Get("S"))
			if s == "Transparency" {
				errs = append(errs, Violation{
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
			find1bTransparencyXObjects(doc, page.Dict, level, seen, &errs, 0)
		}
	}

	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		gs := entry.dict
		objNum := entry.objNum

		smask := gs.Get("SMask")
		if smask != nil {
			if n, ok := doc.ResolveName(smask); ok && n == "None" {
				// acceptable
			} else {
				errs = append(errs, Violation{
					Rule:    "6.4",
					Level:   level,
					Message: "/SMask must not be used (PDF/A-1b)",
					Object:  objNum,
				})
			}
		}

		bm := gs.Get("BM")
		if bm != nil {
			if n, ok := doc.ResolveName(bm); ok {
				if n != "Normal" && n != "Compatible" {
					errs = append(errs, Violation{
						Rule:    "6.4",
						Level:   level,
						Message: fmt.Sprintf("/BM must be /Normal or /Compatible, got /%s", string(n)),
						Object:  objNum,
					})
				}
			}
		}

		for _, key := range []object.Name{"CA", "ca"} {
			if val, ok := doc.ResolveNumber(gs.Get(key)); ok {
				if math.Abs(val-1.0) > 1e-6 {
					errs = append(errs, Violation{
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
		for val := range gsDict.Values() {
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

	// Scan the objects the document reaches for Resources dicts (pages, Form
	// XObjects, Type3 fonts).
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
	for _, num := range sortedReachableObjectNums(doc) {
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
func imageClause(concept string, level Level) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"image": {"6.2.4", "6.2.8", "6.2.7.1"},
	}
	c, ok := m[concept]
	if !ok {
		return "6.2.8"
	}
	switch level.Part() {
	case 1:
		return c[0]
	case 4:
		return c[2]
	default:
		return c[1]
	}
}

// jpxClause returns the ISO clause for the JPEG 2000 image rules.
//
// It is separate from imageClause because there is no PDF/A-1 answer to give
// and the table above should not invent one: JPXDecode is not a permitted
// filter at that level, so a JPEG 2000 image there is reported by the filter
// check under 6.1.10 and the rules below never run.
func jpxClause(level Level) string {
	if level.Part() == 4 {
		return "6.2.7.3"
	}
	return "6.2.8.3"
}

func checkNoAlternateImages(doc core.View, level Level) []Violation {
	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil {
			continue
		}
		if st, ok := doc.ResolveName(stream.Dict.Get("Subtype")); ok && st == "Image" {
			if stream.Dict.Get("Alternates") != nil {
				errs = append(errs, Violation{
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
func checkInterpolate(doc core.View, level Level) []Violation {
	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil {
			continue
		}
		if st, ok := doc.ResolveName(stream.Dict.Get("Subtype")); ok && st == "Image" {
			interpObj := stream.Dict.Get("Interpolate")
			if interpObj != nil {
				if b, ok := doc.ResolveBool(interpObj); ok && bool(b) {
					errs = append(errs, Violation{
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
func xobjectClause(concept string, level Level) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"formMisc": {"6.2.4", "6.2.9", "6.2.8.1"}, // OPI / Subtype2 / PS on a form
		"refXObj":  {"6.2.6", "6.2.9", "6.2.8.2"}, // reference XObjects
	}
	c, ok := m[concept]
	if !ok {
		return "6.2.9"
	}
	switch level.Part() {
	case 1:
		return c[0]
	case 4:
		return c[2]
	default:
		return c[1]
	}
}

func checkNoOPI(doc core.View, level Level) []Violation {
	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil {
			continue
		}
		st, ok := doc.ResolveName(stream.Dict.Get("Subtype"))
		if !ok {
			continue
		}
		add := func(rule, msg string) {
			errs = append(errs, Violation{Rule: rule, Level: level, Message: msg, Object: num})
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
func checkCatalogVersion(doc core.View, level Level) []Violation {
	if level.Part() != 4 {
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

	vName, ok := doc.ResolveName(versionObj)
	if !ok {
		return []Violation{{
			Rule:    "6.1.12",
			Level:   level,
			Message: "catalog /Version must be a name",
		}}
	}

	v := string(vName)
	if len(v) != 3 || v[0] != '2' || v[1] != '.' || v[2] < '0' || v[2] > '9' {
		return []Violation{{
			Rule:    "6.1.12",
			Level:   level,
			Message: fmt.Sprintf("catalog /Version must match 2.N, got %s", v),
		}}
	}

	return nil
}

// --- Font subset checks (MR-8) ---

// Rule 6.2.10: PDF/A-1b subset fonts must have CharSet or CIDSet.
func checkFontSubsets(doc core.View, level Level) []Violation {
	// CharSet/CIDSet PRESENCE is only required by 19005-1: the veraPDF
	// corpus passes a PDF/A-2 subset CIDFont without /CIDSet (Part 2 only
	// constrains the sets when present).
	if level.Part() != 1 {
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
	var errs []Violation

	for fontDict, objNum := range fonts {
		subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))
		baseFont, _ := doc.ResolveName(fontDict.Get("BaseFont"))

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
				errs = append(errs, Violation{
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
				errs = append(errs, Violation{
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
func checkExtGState(doc core.View, level Level) []Violation {
	// ISO 19005-1 clause 6.2.8 carries the same TR/TR2 prohibitions as
	// 19005-2 clause 6.2.5; previously the whole check was skipped at 1b
	// with a comment claiming checkNoTransparency covered it, which never
	// looked at /TR, /TR2, or halftones.
	rule := "6.2.5"
	if level.Part() == 1 {
		rule = "6.2.8"
	}

	var errs []Violation
	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		dict := entry.dict
		num := entry.objNum

		// /TR must not be present
		if dict.Get("TR") != nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /TR",
				Object:  num,
			})
		}

		// /TR2 must be /Default if present
		if tr2 := dict.Get("TR2"); tr2 != nil {
			if n, ok := doc.ResolveName(tr2); !ok || n != "Default" {
				errs = append(errs, Violation{
					Rule:    rule,
					Level:   level,
					Message: "/TR2 must be /Default",
					Object:  num,
				})
			}
		}

		// /HTO and /HTP must not be present (PDF 2.0 halftone keys;
		// restricted at 2b+).
		if level.Part() != 1 && dict.Get("HTO") != nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /HTO",
				Object:  num,
			})
		}
		if level.Part() != 1 && dict.Get("HTP") != nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "ExtGState must not contain /HTP",
				Object:  num,
			})
		}
		// /RI, when present, must be a standard rendering intent (all levels).
		if ri, ok := doc.Resolve(dict.Get("RI")).(object.Name); ok && !standardRenderingIntents[string(ri)] {
			errs = append(errs, Violation{
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
		if level.Part() != 1 {
			if bm := dict.Get("BM"); bm != nil {
				if n, ok := doc.ResolveName(bm); ok {
					if !isValidBlendMode(n) {
						errs = append(errs, Violation{
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

func checkHalftoneErrors(doc core.View, htRef object.Object, objNum int, level Level, rule string, errs *[]Violation) {
	htDict := doc.ResolveDict(htRef)
	if htDict == nil {
		return
	}

	if htType := htDict.Get("HalftoneType"); htType != nil {
		if intVal, ok := doc.ResolveInt(htType); ok {
			if intVal != 1 && intVal != 5 {
				*errs = append(*errs, Violation{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("halftone type must be 1 or 5, got %d", intVal),
					Object:  objNum,
				})
			}
		}
	}

	if htDict.Get("HalftoneName") != nil {
		*errs = append(*errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: "halftone must not contain /HalftoneName",
			Object:  objNum,
		})
	}

	if htDict.Get("TransferFunction") != nil {
		*errs = append(*errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: "halftone must not contain /TransferFunction",
			Object:  objNum,
		})
	}
}

// --- Info/XMP consistency check (MR-6) ---

// Rule 6.7.3: PDF/A-1b requires Info dict and XMP metadata to be consistent.
func checkInfoXMPConsistency(doc core.View, level Level) []Violation {
	// Info<->XMP consistency is a 19005-1 (6.7.3) requirement only: the
	// veraPDF corpus passes PDF/A-2 files whose Info entries deliberately
	// differ from their XMP counterparts (Part 2 deprecates Info instead).
	if level.Part() != 1 {
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
	if _, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream); !ok {
		return nil
	}
	// The XMP side is read through the model, so a value is compared as the
	// packet means it — "Smith &amp; Sons" is "Smith & Sons" — and a language
	// alternative contributes its x-default item rather than whichever came
	// first (audit C35). A packet pdf0 did not model (a limit, already on the
	// run) or could not (malformed, reported by the well-formedness rule) has no
	// values to compare, which is different from values that disagree.
	packet, status := doc.DocumentXMPPacket()
	if status == core.XMPLimit || status == core.XMPMalformed {
		return nil
	}

	var errs []Violation

	pairs := []struct {
		infoKey string
		ns      string
		xmpKey  string // for messages
		name    string
	}{
		{"Title", xmp.NSDC, "dc:title", "title"},
		{"Author", xmp.NSDC, "dc:creator", "creator"},
		{"Subject", xmp.NSDC, "dc:description", "description"},
		{"Keywords", xmp.NSPDF, "pdf:Keywords", "Keywords"},
		{"Creator", xmp.NSXMP, "xmp:CreatorTool", "CreatorTool"},
		{"Producer", xmp.NSPDF, "pdf:Producer", "Producer"},
		{"CreationDate", xmp.NSXMP, "xmp:CreateDate", "CreateDate"},
		{"ModDate", xmp.NSXMP, "xmp:ModifyDate", "ModifyDate"},
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
		strVal, r := doc.StringValue(resolved)
		if r == core.ReasonAbsent {
			errs = append(errs, Violation{
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("Info /%s is not a string value", p.infoKey),
			})
			continue
		}
		if r != core.ReasonOK {
			continue // ciphertext: nothing to compare
		}
		infoVal := core.DecodePDFTextString(strVal.Value)
		if infoVal == "" {
			continue
		}

		var prop xmp.Property
		var present bool
		if packet != nil {
			prop, present = packet.Get(p.ns, p.name)
		}

		// When Info /Author is present, XMP dc:creator shall contain
		// exactly one entry (ISO 19005-1 6.7.3).
		if p.infoKey == "Author" && present && len(prop.Value.Items) > 1 {
			errs = append(errs, Violation{
				Rule:    "6.7.3",
				Level:   level,
				Message: "XMP dc:creator contains more than one entry while Info /Author is present",
			})
			continue
		}

		xmpVal := ""
		if present {
			xmpVal = xmpComparableText(prop.Value)
		}
		if xmpVal == "" {
			errs = append(errs, Violation{
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
				errs = append(errs, Violation{
					Rule:    "6.7.3",
					Level:   level,
					Message: fmt.Sprintf("Info /%s (%s) does not match XMP %s (%s)", p.infoKey, infoVal, p.xmpKey, xmpVal),
				})
			}
		} else {
			if infoVal != xmpVal {
				errs = append(errs, Violation{
					Rule:    "6.7.3",
					Level:   level,
					Message: fmt.Sprintf("Info /%s (%q) does not match XMP %s (%q)", p.infoKey, infoVal, p.xmpKey, xmpVal),
				})
			}
		}
	}

	return errs
}

// xmpComparableText is the text an Info entry is compared with: a simple
// value's own text; a language alternative's x-default item (xmp.Value.AltText);
// the first item of a Seq or Bag, which for dc:creator is the one author the
// rule allows.
func xmpComparableText(v xmp.Value) string {
	// No trimming beyond what the model does (element text is trimmed, an
	// attribute value is not): a trailing space in an attribute-form value is
	// part of the value, and veraPDF's 6-1-5 pass files depend on it matching
	// the Info entry's.
	switch v.Kind {
	case xmp.Simple:
		return v.Text
	case xmp.Alt:
		t, _ := v.AltText("x-default")
		return t
	case xmp.Seq, xmp.Bag:
		if len(v.Items) > 0 {
			return v.Items[0].Text
		}
	}
	return ""
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
func checkTransparencyBlending(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil // PDF/A-1b prohibits transparency entirely
	}

	var errs []Violation

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
			errs = append(errs, Violation{
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

		s, _ := doc.ResolveName(groupDict.Get("S"))
		if s != "Transparency" {
			errs = append(errs, Violation{
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
				errs = append(errs, Violation{
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
func transparencyGroupNotRequired(doc core.View, catalog *object.Dictionary, page *object.Dictionary, level Level) bool {
	if level.Part() == 4 {
		// PDF/A-4: page-level or catalog-level OutputIntents provide blending CS
		catalogRGB, catalogCMYK, catalogGray := getOutputIntentCoverage(doc, catalog)
		pageRGB, pageCMYK, pageGray := getOutputIntentCoverage(doc, page)
		if catalogRGB || catalogCMYK || catalogGray || pageRGB || pageCMYK || pageGray {
			return true
		}
	}

	// For PDF/A-2b/3b: OutputIntents or DefaultCS coverage can provide blending CS
	if level.Part() == 2 || level.Part() == 3 {
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
func find1bTransparencyXObjects(doc core.View, container *object.Dictionary, level Level, seen map[*object.Dictionary]bool, errs *[]Violation, depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
	if seen[container] {
		return
	}
	seen[container] = true

	res := doc.ResolveDict(container.Get("Resources"))
	if res == nil {
		return
	}

	if xobjDict := doc.ResolveDict(res.Get("XObject")); xobjDict != nil {
		for val := range xobjDict.Values() {
			stream, ok := doc.Resolve(val).(*object.Stream)
			if !ok {
				continue
			}
			num := resolveObjNum(doc, val)
			switch subtype, _ := doc.ResolveName(stream.Dict.Get("Subtype")); subtype {
			case "Image":
				if sm := stream.Dict.Get("SMask"); sm != nil {
					if n, ok := doc.ResolveName(sm); !ok || n != "None" {
						*errs = append(*errs, Violation{
							Rule:    "6.4",
							Level:   level,
							Message: "image XObject must not have /SMask (PDF/A-1b forbids transparency)",
							Object:  num,
						})
					}
				}
			case "Form":
				if g := doc.ResolveDict(stream.Dict.Get("Group")); g != nil {
					if s, _ := doc.ResolveName(g.Get("S")); s == "Transparency" {
						*errs = append(*errs, Violation{
							Rule:    "6.4",
							Level:   level,
							Message: "form XObject must not have a /Group with /S /Transparency (PDF/A-1b forbids transparency)",
							Object:  num,
						})
					}
				}
				find1bTransparencyXObjects(doc, &stream.Dict, level, seen, errs, depth+1)
			}
		}
	}

	if patDict := doc.ResolveDict(res.Get("Pattern")); patDict != nil {
		for val := range patDict.Values() {
			if stream, ok := doc.Resolve(val).(*object.Stream); ok {
				find1bTransparencyXObjects(doc, &stream.Dict, level, seen, errs, depth+1)
			}
		}
	}

	if fontDict := doc.ResolveDict(res.Get("Font")); fontDict != nil {
		for val := range fontDict.Values() {
			if fd := doc.ResolveDict(val); fd != nil {
				if st, _ := doc.ResolveName(fd.Get("Subtype")); st == "Type3" {
					find1bTransparencyXObjects(doc, fd, level, seen, errs, depth+1)
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
func checkEmbeddedFiles(doc core.View, level Level) []Violation {
	// PDF/A-1 (ISO 19005-1, 6.1.11) forbids embedded files outright: no
	// file specification may carry /EF, wherever it lives — not only in the
	// catalog's Names tree.
	if level.Part() == 1 {
		var errs []Violation
		for _, r := range doc.ReachableDicts() {
			if dict, num := r.Dict, r.ObjNum; r.Stream == nil && dict.Get("EF") != nil {
				errs = append(errs, Violation{
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
					errs = append(errs, Violation{
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

func checkEmbeddedFileSpecs(doc core.View, level Level, catalog *object.Dictionary) []Violation {
	var errs []Violation

	// Embedded-file rules live in clause 6.8 for 19005-2/-3 and 6.9 for
	// 19005-4.
	rule := "6.8"
	if level.Part() == 4 {
		rule = "6.9"
	}

	// PDF/A-3 and A-4 require embedded files to be associated with the
	// document or one of its parts via /AF (the corpus fails A-3 files
	// whose embedded file is associated with nothing). PDF/A-2 has no
	// association mechanism. PDF/A-4f and -4e exist to carry embedded files
	// (arbitrary files; 3D/RichMedia content) and associate them per-filespec
	// via /AFRelationship rather than a document-level /AF array, so the
	// document-/AF requirement is relaxed for both — when the target is one;
	// the document's own declaration is the identification rule's to judge.
	relaxAF := level.variant() != ""
	if level.Part() != 2 && !relaxAF && documentHasEmbeddedFiles(doc, catalog) && !documentHasAF(doc) {
		errs = append(errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: "document must have /AF array when embedded files are present",
		})
	}

	for _, r := range doc.ReachableDicts() {
		dict, num := r.Dict, r.ObjNum
		if r.Stream != nil {
			continue
		}
		// A file specification is not required to carry /Type /Filespec;
		// anything holding an /EF is acting as one.
		t, hasType := doc.ResolveName(dict.Get("Type"))
		isFilespec := (hasType && t == "Filespec") || dict.Get("EF") != nil
		if !isFilespec {
			continue
		}

		// /F and /UF are required of the specification of an *embedded* file
		// (ISO 19005-2/-3 6.8, -4 6.9; veraPDF: containsEF == false ||
		// (F != null && UF != null)). A specification that only names an
		// external file — a Launch action's /F, say, written inline — is
		// not held to it. That distinction was invisible while only the
		// specifications written as objects of their own were examined,
		// since those are the embedded ones in practice.
		embedded := dict.Get("EF") != nil
		if embedded && dict.Get("F") == nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /F",
				Object:  num,
			})
		}
		if embedded && dict.Get("UF") == nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /UF",
				Object:  num,
			})
		}
		// /AFRelationship is the PDF/A-3+ mechanism relating an embedded
		// file to the document; PDF/A-2 has no such key. PDF/A-3 asks it of
		// every file specification (6.8 t03: AFRelationship != null), PDF/A-4
		// of an embedded file's only (6.9 t04: containsEF == false ||
		// AFRelationship != null).
		if level.Part() != 2 && (level.Part() != 4 || embedded) && dict.Get("AFRelationship") == nil {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "filespec must have /AFRelationship",
				Object:  num,
			})
		}

		// Embedded file streams must declare their MIME type in PDF/A-3/4.
		if level.Part() == 3 || level.Part() == 4 {
			if efDict := doc.ResolveDict(dict.Get("EF")); efDict != nil {
				for val := range efDict.Values() {
					stream, ok := doc.Resolve(val).(*object.Stream)
					if !ok {
						continue
					}
					st := stream.Dict.Get("Subtype")
					if st == nil {
						errs = append(errs, Violation{
							Rule:    rule,
							Level:   level,
							Message: "embedded file stream must have /Subtype (MIME type)",
							Object:  num,
						})
					} else if name, ok := doc.ResolveName(st); ok {
						if !strings.Contains(string(name), "/") {
							errs = append(errs, Violation{
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
	for _, r := range doc.ReachableDicts() {
		if r.Stream == nil && r.Dict.Get("EF") != nil {
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
	for _, r := range doc.ReachableDicts() {
		if r.Dict.Get("AF") != nil {
			return true
		}
	}
	return false
}

// --- Optional content check (MR-5) ---

// Rule 6.1.13: Optional content requirements for PDF/A-4.
func checkOptionalContent(doc core.View, level Level) []Violation {
	// Optional-content configuration rules are 19005-2/-3 clause 6.9 and
	// 19005-4 clause 6.10. PDF/A-1 forbids optional content wholesale
	// (checkNoOCProperties).
	if level.Part() == 1 {
		return nil
	}
	ocRule := "6.9"
	if level.Part() == 4 {
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

	var errs []Violation

	dRef := ocpDict.Get("D")
	if dRef == nil {
		return errs
	}
	dDict := doc.ResolveDict(dRef)
	if dDict == nil {
		return errs
	}

	if dDict.Get("Name") == nil {
		errs = append(errs, Violation{
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
					errs = append(errs, Violation{
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
			if s, r := doc.StringValue(nameObj); r == core.ReasonOK {
				// A text string: the same name in UTF-16 and in PDFDocEncoding
				// is the same name.
				n := core.DecodePDFTextString(s.Value)
				if names[n] {
					errs = append(errs, Violation{
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
			collectOCGRefs(doc, orderArr, referencedOCGs, map[int]bool{})
			for _, ocgRef := range ocgsArr {
				if iref, ok := ocgRef.(object.IndirectRef); ok {
					if !referencedOCGs[iref.Number] {
						errs = append(errs, Violation{
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

// collectOCGRefs records every OCG an /Order array names, descending into its
// nested arrays. An optional content group is always a reference (it is a
// dictionary identified by its object), but a nested array may be one too,
// so a reference is recorded and, when it names an array, descended into;
// seen stops an array that contains itself.
func collectOCGRefs(doc core.View, arr object.Array, refs map[int]bool, seen map[int]bool) {
	for _, item := range arr {
		switch v := item.(type) {
		case object.IndirectRef:
			refs[v.Number] = true
			if sub, ok := doc.Resolve(v).(object.Array); ok && !seen[v.Number] {
				seen[v.Number] = true
				collectOCGRefs(doc, sub, refs, seen)
			}
		case object.Array:
			collectOCGRefs(doc, v, refs, seen)
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

func checkImplementationLimits(doc core.View, level Level) []Violation {
	if level.Part() == 4 {
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
	if level.Part() == 2 || level.Part() == 3 {
		lim.rule = "6.1.13" // ISO 19005-2/-3
		lim.stringLen = 32767
		lim.realLimit = 3.403e38 // PDF 1.7 Annex C (float32 range)
	}

	var errs []Violation
	// allobjects: the implementation limits bound the file's syntax, which
	// every indirect object in the file must respect, used or not.
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
	if level.Part() != 1 {
		checkPageSizeLimits(doc, level, &errs)
	}

	return errs
}

func checkObjectLimits(obj object.Object, objNum int, level Level, lim implLimits, depth int, errs *[]Violation) {
	if obj == nil {
		return
	}

	switch v := obj.(type) {
	case object.IndirectRef:
		// The limits are on each object as written: a referenced object is
		// measured as its own object, at its own nesting depth.
	case object.Name:
		if len(string(v)) > lim.nameLen {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("name length %d exceeds maximum %d", len(string(v)), lim.nameLen),
				Object:  objNum,
			})
		}
	case object.String:
		if len(v.Value) > lim.stringLen {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("string length %d exceeds maximum %d", len(v.Value), lim.stringLen),
				Object:  objNum,
			})
		}
	case object.Integer:
		i := int64(v)
		if i < -2147483648 || i > 2147483647 {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("integer %d out of range [-2^31, 2^31-1]", i),
				Object:  objNum,
			})
		}
	case object.Real:
		if math.Abs(float64(v)) > lim.realLimit {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("real %g exceeds magnitude limit %g", float64(v), lim.realLimit),
				Object:  objNum,
			})
		}
	case *object.Dictionary:
		if depth > lim.nesting {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("dictionary nesting depth %d exceeds maximum %d", depth, lim.nesting),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if v.Len() > lim.dictEnt {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("dictionary has %d entries, exceeds maximum %d", v.Len(), lim.dictEnt),
				Object:  objNum,
			})
		}
		for key, ev := range v.All() {
			checkObjectLimits(key, objNum, level, lim, depth+1, errs)
			checkObjectLimits(ev, objNum, level, lim, depth+1, errs)
		}
	case object.Array:
		if depth > lim.nesting {
			*errs = append(*errs, Violation{
				Rule:    lim.rule,
				Level:   level,
				Message: fmt.Sprintf("array nesting depth %d exceeds maximum %d", depth, lim.nesting),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if len(v) > lim.arrayLen {
			*errs = append(*errs, Violation{
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

func checkPageSizeLimits(doc core.View, level Level, errs *[]Violation) {
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
				v, ok := doc.ResolveNumber(elem)
				if !ok {
					valid = false
				}
				vals[i] = v
			}
			if !valid {
				continue
			}
			width := math.Abs(vals[2] - vals[0])
			height := math.Abs(vals[3] - vals[1])
			if width < 3 || width > 14400 || height < 3 || height > 14400 {
				*errs = append(*errs, Violation{
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
func checkQNestingDepth(doc core.View, level Level, rule string, errs *[]Violation) {
	const maxQDepth = 28

	report := func(data []byte, objNum int) {
		if d := qNestingMaxDepth(doc.Cancel, data); d > maxQDepth {
			*errs = append(*errs, Violation{
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
		if data, _ := core.ContentStreamData(doc, contentsRef); data != nil { // reason: presence-only; the producer recorded any declined trip
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
func checkDeviceColorSpaces(doc core.View, level Level) []Violation {
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

	var errs []Violation
	pages := doc.Pages(pagesRef)
	for _, page := range pages {
		// For PDF/A-4, also check page-level OutputIntents
		pageRGB, pageCMYK, pageGray := hasRGBIntent, hasCMYKIntent, hasGrayIntent
		if level.Part() == 4 {
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
			errs = append(errs, Violation{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceRGB used without matching OutputIntent or DefaultRGB",
				Object:  page.ObjNum,
			})
		}

		if usesCMYK && !pageCMYK && !groupCMYK {
			errs = append(errs, Violation{
				Rule:    colourClause("deviceColour", level),
				Level:   level,
				Message: "DeviceCMYK used without matching OutputIntent or DefaultCMYK",
				Object:  page.ObjNum,
			})
		}

		// DeviceGray: any OutputIntent covers it
		if usesGray && !pageRGB && !pageCMYK && !pageGray {
			errs = append(errs, Violation{
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
		if s, _ := doc.ResolveName(dict.Get("S")); s != "GTS_PDFA1" {
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
		profileData, _ := doc.ICCProfileData(stream) // reason: an unread profile is taken to cover both spaces below; the producer recorded any declined trip
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

// forEachContentOperator calls fn for each operator of a decoded content
// stream, as the content lexer reads them: bytes inside strings, comments,
// dictionary operands and inline-image data are never operators.
func forEachContentOperator(cancel core.Canceler, data []byte, fn func(op []byte)) {
	lx := core.NewContentLexer(cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentOperator:
			fn(t.Raw)
		case core.ContentDictStart:
			lx.SkipDict(&t)
		}
	}
}

// --- ICCBased color space checks (6.2.4.2) ---

// Rule 6.2.4.2: ICCBased color spaces must reference valid ICC profiles.
// checkICCBasedProfiles judges every ICC profile used as an ICCBased colour
// space (ISO 19005-1 6.2.3.2, -2/-3/-4 6.2.4.2): /N is 1, 3 or 4 and matches
// the profile's colour space, and the profile version is at most 2.x at part 1
// (PDF 1.4) and 4.x later. Every finding is under the ICCBased clause.
//
// A profile is found through an [/ICCBased profile] array, wherever one is
// written — resources, image dictionaries, Default colour spaces, group /CS,
// nested inside Indexed, Separation and DeviceN — and judged once. It used to be
// "every stream with an integer /N", which took an output intent's destination
// profile for a colour space: a PDF/A-2b skeleton at 1b got its v4 profile
// reported twice, once as the output intent it is (6.2.2) and once, at a
// different clause, as an ICCBased colour space the file does not have (audit
// 2026-09-22 C140).
func checkICCBasedProfiles(doc core.View, level Level) []Violation {
	rule := colourClause("iccBased", level)
	var errs []Violation
	for _, p := range iccBasedProfiles(doc) {
		n, ok := doc.ResolveInt(p.stream.Dict.Get("N"))
		if !ok {
			continue
		}
		nVal := int(n)
		if nVal != 1 && nVal != 3 && nVal != 4 {
			errs = append(errs, Violation{Rule: rule, Level: level, Object: p.num,
				Message: fmt.Sprintf("ICCBased profile /N must be 1, 3, or 4, got %d", nVal)})
			continue
		}
		profileData, _ := doc.ICCProfileData(p.stream) // reason: the header checks below run only on data that is present; the producer recorded any declined trip
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
				errs = append(errs, Violation{Rule: rule, Level: level, Object: p.num,
					Message: fmt.Sprintf("ICCBased profile /N=%d does not match ICC color space %q", nVal, cs)})
			}
		}
		if len(profileData) >= 9 {
			majorVersion := profileData[8]
			maxVersion := byte(4)
			if level.Part() == 1 {
				maxVersion = 2
			}
			if majorVersion > maxVersion {
				errs = append(errs, Violation{Rule: rule, Level: level, Object: p.num,
					Message: fmt.Sprintf("ICCBased profile version %d.x not allowed (max %d.x)", majorVersion, maxVersion)})
			}
		}
	}
	return errs
}

// iccBasedProfile is a profile stream an ICCBased colour space names, and the
// object that holds it.
type iccBasedProfile struct {
	stream *object.Stream
	num    int
}

// iccBasedProfiles finds every profile stream named by an [/ICCBased profile]
// array anywhere in the document it reaches, each once, in object-number
// order. Each
// object's value is walked to a bounded depth without following references —
// a referenced object is walked as its own object — so every array written
// anywhere is seen exactly as written.
func iccBasedProfiles(doc core.View) []iccBasedProfile {
	seen := map[*object.Stream]bool{}
	var out []iccBasedProfile
	var walk func(o object.Object, depth int)
	walk = func(o object.Object, depth int) {
		if depth > 32 {
			return
		}
		switch v := o.(type) {
		case object.IndirectRef:
			// Walked as its own object.
		case object.Array:
			if len(v) >= 2 {
				if name, _ := doc.ResolveName(v[0]); name == "ICCBased" {
					if s, ok := doc.Resolve(v[1]).(*object.Stream); ok && !seen[s] {
						seen[s] = true
						out = append(out, iccBasedProfile{s, resolveObjNum(doc, v[1])})
					}
				}
			}
			for _, e := range v {
				walk(e, depth+1)
			}
		case *object.Dictionary:
			for val := range v.Values() {
				walk(val, depth+1)
			}
		case *object.Stream:
			for val := range v.Dict.Values() {
				walk(val, depth+1)
			}
		}
	}
	// The objects the document reaches, in ascending number; an orphan
	// colour space colours nothing (audit 2026-09-22 C83).
	for _, num := range sortedReachableObjectNums(doc) {
		if doc.Cancel.Stopped() {
			break
		}
		walk(doc.Objects[num].Value, 0)
	}
	return out
}

// --- Separation/DeviceN checks (6.2.4.4) ---

// Rule 6.2.4.4 / 6.2.3.4: Separation and DeviceN color space restrictions.
func checkSeparationDeviceN(doc core.View, level Level) []Violation {

	var errs []Violation

	// Scan the objects the document reaches for color space arrays used in
	// Resources; an orphan colour space colours nothing (audit 2026-09-22 C83).
	for _, num := range doc.ReachableObjectNums() {
		iobj := doc.Objects[num]
		dict, isDict := iobj.Value.(*object.Dictionary)
		stream, isStream := iobj.Value.(*object.Stream)

		// Check dictionary Resources/ColorSpace
		if isDict {
			checkDictForSepDeviceN(doc, dict, num, level, &errs)
			// A direct /Resources sub-dictionary (the common case on pages)
			// is not a top-level object, so this scan would never visit its
			// /ColorSpace entries; descend explicitly. Indirect Resources
			// are separate objects and are visited by the loop itself.
			switch resDict := dict.Get("Resources").(type) {
			case object.IndirectRef:
				// Visited by the loop as its own object.
			case *object.Dictionary:
				checkDictForSepDeviceN(doc, resDict, num, level, &errs)
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
			switch resDict := stream.Dict.Get("Resources").(type) {
			case object.IndirectRef:
				// Visited by the loop as its own object.
			case *object.Dictionary:
				checkDictForSepDeviceN(doc, resDict, num, level, &errs)
			}
		}
	}

	return append(errs, checkSeparationConsistency(doc, level)...)
}

// sepDefinition is one Separation colour space the executed content uses.
type sepDefinition struct {
	alt, tint object.Object // as written
	tintNum   int           // the tint transform's object number; 0 when written inline
}

// checkSeparationConsistency enforces that all Separation colour spaces with
// the same colorant name have the same tint transform and the same alternate
// space (ISO 19005-2/-3/-4 6.2.4.4, 19005-1 6.2.3.4).
//
// Which definitions: those of the colour spaces executed content uses —
// selected by cs/CS, an image's, a shading's, a transparency group's (see
// core.UsedColourSpaces) — and the Separations inside them: a DeviceN space's
// /Colorants, an Indexed space's base, a pattern space's underlying space. A
// colour space in a resource dictionary nothing draws defines nothing that is
// rendered, which is the executed-content model the other colour rules follow.
//
// "The same" is a question about content, not objects: two tint transforms
// written as separate but identical objects are the same function, which the
// corpus confirms (6-2-4-4-t03-pass-a). They are compared with
// core.ResolvedEqual, which follows references all the way down; object.Equal
// compared nested references by number.
//
// The report does not depend on the order anything was found in. It used to:
// the "first definition" every other was compared against came from ranging
// over doc.Objects, a Go map, so one parsed document gave three different
// reports across fifty runs (audit 2026-09-22 C66). The definitions of each
// colorant are now partitioned into classes of equal tint transforms (and of
// equal alternates), in object-number order, and more than one class is one
// finding per colorant, naming the tint transforms that begin the first two
// classes (0 for one written inline). The finding is about the document — two
// definitions disagreeing — and anchors to no object.
func checkSeparationConsistency(doc core.View, level Level) []Violation {
	defs := map[object.Name][]sepDefinition{}
	visited := map[int]bool{}
	var walk func(cs object.Object, depth int)
	walk = func(cs object.Object, depth int) {
		if depth > 16 {
			return
		}
		if r, ok := cs.(object.IndirectRef); ok {
			if visited[r.Number] {
				return
			}
			visited[r.Number] = true
		}
		arr, ok := doc.Resolve(cs).(object.Array)
		if !ok || len(arr) < 2 {
			return
		}
		switch family, _ := doc.ResolveName(arr[0]); family {
		case "Separation":
			if len(arr) < 4 {
				return
			}
			if colorant, ok := doc.ResolveName(arr[1]); ok {
				defs[colorant] = append(defs[colorant], sepDefinition{alt: arr[2], tint: arr[3], tintNum: object.RefNum(arr[3])})
			}
		case "DeviceN", "NChannel":
			if len(arr) < 5 {
				return
			}
			if attrs := doc.ResolveDict(arr[4]); attrs != nil {
				if colorants := doc.ResolveDict(attrs.Get("Colorants")); colorants != nil {
					for _, k := range slices.Sorted(colorants.Keys()) {
						walk(colorants.Get(k), depth+1)
					}
				}
			}
		case "Indexed", "Pattern":
			walk(arr[1], depth+1)
		}
	}
	for _, cs := range core.UsedColourSpaces(doc) {
		if doc.Cancel.Stopped() {
			return nil
		}
		walk(cs, 0)
	}

	// classes partitions a colorant's definitions by one of their parts, and
	// returns the first definition of each class.
	classes := func(ds []sepDefinition, part func(sepDefinition) object.Object) []sepDefinition {
		var firsts []sepDefinition
		for _, d := range ds {
			found := false
			for _, f := range firsts {
				if core.ResolvedEqual(doc, part(f), part(d)) {
					found = true
					break
				}
			}
			if !found {
				firsts = append(firsts, d)
			}
		}
		return firsts
	}
	var errs []Violation
	for _, colorant := range slices.Sorted(maps.Keys(defs)) {
		ds := defs[colorant]
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].tintNum < ds[j].tintNum })
		if tc := classes(ds, func(d sepDefinition) object.Object { return d.tint }); len(tc) > 1 {
			errs = append(errs, Violation{
				Rule:    colourClause("spot", level),
				Level:   level,
				Message: fmt.Sprintf("Separation colorant /%s has inconsistent tint transforms (objects %d and %d)", string(colorant), tc[0].tintNum, tc[1].tintNum),
			})
		}
		if ac := classes(ds, func(d sepDefinition) object.Object { return d.alt }); len(ac) > 1 {
			errs = append(errs, Violation{
				Rule:    colourClause("spot", level),
				Level:   level,
				Message: fmt.Sprintf("Separation colorant /%s has inconsistent alternate color spaces", string(colorant)),
			})
		}
	}
	return errs
}

func checkDictForSepDeviceN(doc core.View, dict *object.Dictionary, objNum int, level Level, errs *[]Violation) {
	csRef := dict.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		return
	}
	for val := range csDict.Values() {
		checkColorSpaceValue(doc, val, objNum, level, errs)
	}
}

func checkColorSpaceValue(doc core.View, csObj object.Object, objNum int, level Level, errs *[]Violation) {
	checkColorSpaceValueSeen(doc, csObj, objNum, level, errs, make(map[int]bool), 0)
}

func checkColorSpaceValueSeen(doc core.View, csObj object.Object, objNum int, level Level, errs *[]Violation, seen map[int]bool, depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
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

	csType, ok := doc.ResolveName(arr[0])
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
		checkColorSpaceValueSeen(doc, arr[1], objNum, level, errs, seen, depth+1)
	case "Separation":
		// [/Separation name alternateSpace tintTransform]
		if len(arr) < 4 {
			rule := "6.2.4"
			if level.Part() == 1 {
				rule = "6.2.3"
			}
			*errs = append(*errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "Separation color space array must have 4 elements",
				Object:  objNum,
			})
			return
		}
		// Check colorant name is not None for PDF/A-2b+ (it's reserved)
		if name, ok := doc.ResolveName(arr[1]); ok && name == "None" {
			// "None" is a special name in PDF 2.0 only
			if level.Part() != 4 {
				*errs = append(*errs, Violation{
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
			if level.Part() == 1 {
				rule = "6.2.3"
			}
			*errs = append(*errs, Violation{
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
		switch level.Part() {
		case 1:
			maxColorants = 8
			rule = "6.2.3"
		case 2, 3:
			maxColorants = 32
		}
		if maxColorants > 0 {
			if namesArr, ok := doc.Resolve(arr[1]).(object.Array); ok && len(namesArr) > maxColorants {
				*errs = append(*errs, Violation{
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
		if level.Part() != 1 && namesOk {
			hasSpot := false
			for _, nameObj := range namesArr {
				if name, ok := doc.ResolveName(nameObj); ok && !isProcessColorant(name) {
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
					*errs = append(*errs, Violation{
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
								if name, ok := doc.ResolveName(nameObj); ok {
									if colorantsDict.Get(name) == nil {
										rule := "6.2.4"
										if level.Part() == 1 {
											rule = "6.2.3"
										}
										*errs = append(*errs, Violation{
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
						for cval := range colorantsDict.Values() {
							checkColorSpaceValueSeen(doc, cval, objNum, level, errs, seen, depth+1)
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
func checkCIEDictParams(doc core.View, family string, dict *object.Dictionary, objNum int, level Level, errs *[]Violation) {
	rule := "6.2.4"
	if level.Part() == 1 {
		rule = "6.2.3"
	}
	bad := func(format string, args ...interface{}) {
		*errs = append(*errs, Violation{
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
func checkAlternateCS(doc core.View, altCS object.Object, objNum int, level Level, errs *[]Violation) {
	checkAlternateCSSeen(doc, altCS, objNum, level, errs, make(map[int]bool), 0)
}

func checkAlternateCSSeen(doc core.View, altCS object.Object, objNum int, level Level, errs *[]Violation, seen map[int]bool, depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
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
			if level.Part() == 1 {
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
					*errs = append(*errs, Violation{
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
			if level.Part() == 1 {
				rule = "6.2.3"
			}
			*errs = append(*errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "Separation/DeviceN alternate color space must not be /Pattern",
				Object:  objNum,
			})
		}
	}

	// If it's an array, recurse to check for nested Separation/DeviceN
	if arr, ok := resolved.(object.Array); ok && len(arr) >= 2 {
		if csType, ok := doc.ResolveName(arr[0]); ok {
			if csType == "Separation" || csType == "DeviceN" {
				// Nested Separation/DeviceN - check their alternates too
				if len(arr) >= 3 {
					checkAlternateCSSeen(doc, arr[2], objNum, level, errs, seen, depth+1)
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
}

func scanContentColorUsage(cancel core.Canceler, data []byte) contentColorUsage {
	u := contentColorUsage{
		fillCS:   make(map[string]bool),
		strokeCS: make(map[string]bool),
		gsNames:  make(map[string]bool),
	}
	var lastName string
	lx := core.NewContentLexer(cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentName:
			lastName = t.Name()
			continue
		case core.ContentDictStart:
			lx.SkipDict(&t)
			continue
		case core.ContentOperator:
		default:
			continue
		}
		switch string(t.Raw) {
		case "cs":
			u.fillCS[lastName] = true
		case "CS":
			u.strokeCS[lastName] = true
		case "gs":
			u.gsNames[lastName] = true
		}
	}
	return u
}

// iccCMYKProfile returns the profile stream when csVal is an ICCBased colour
// space with N=4, nil otherwise.
func iccCMYKProfile(doc core.View, csVal object.Object) *object.Stream {
	arr, ok := doc.Resolve(csVal).(object.Array)
	if !ok || len(arr) < 2 {
		return nil
	}
	if n, _ := doc.ResolveName(arr[0]); n != "ICCBased" {
		return nil
	}
	stream, ok := doc.Resolve(arr[1]).(*object.Stream)
	if !ok {
		return nil
	}
	if n, ok := doc.ResolveInt(stream.Dict.Get("N")); !ok || n != 4 {
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
	if n, _ := doc.ResolveName(arr[0]); n != "ICCBased" {
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
	// An unread profile compares unequal, and "the same profile" is the only
	// thing a caller asserts: the producer recorded any declined trip.
	da, _ := doc.ICCProfileData(a) // reason: see above
	db, _ := doc.ICCProfileData(b) // reason: see above
	if len(da) == 0 || len(da) != len(db) {
		return false
	}
	if len(da) >= 100 {
		// The ICC Profile ID is an MD5 of the profile, in header bytes 84-99.
		//
		// Two *different* non-zero IDs mean different profiles, and that is
		// the corpus's ruling rather than a reading: PDF_A-4 6-2-4-2-t03-pass-d
		// embeds two 557,188-byte profiles that are identical but for one byte
		// of their IDs, and it is a pass file — so veraPDF holds them distinct,
		// and comparing content with the ID zeroed reports it as a violation.
		//
		// Two *equal* non-zero IDs are not taken as proof, which is the half
		// that was missing. The ID is sixteen bytes in a stream the document
		// supplies, so it is a claim the file makes about itself, and the rule
		// this feeds — an ICCBased space shall not embed the same profile as
		// the output intent — reports a violation when the answer is "same". A
		// forged ID would therefore turn a conforming file into a reported one,
		// so an equal ID still has to be borne out by the content.
		ida, idb := da[84:100], db[84:100]
		if !allZero(ida) && !allZero(idb) && !bytes.Equal(ida, idb) {
			return false
		}
		// Either the IDs agree — in which case the content still has to — or
		// one is absent, which is common and says nothing about the colours.
		na := append([]byte(nil), da...)
		nb := append([]byte(nil), db...)
		for i := 84; i < 100; i++ {
			na[i], nb[i] = 0, 0
		}
		return bytes.Equal(na, nb)
	}
	return bytes.Equal(da, db)
}

// checkICCBasedUsageRules implements the ICCBased overprint rule (ISO
// 19005-2/-3/-4 6.2.4.2): overprint mode shall not be 1 when an ICCBased CMYK
// colour space is used for a stroke with stroking overprint on, or for any
// other painting operation with non-stroking overprint on.
//
// Every part of that is a fact about the graphics state at the painting
// operator, so the rule is answered by executing the content (see
// core.PageOverprintsICCCMYK): the overprint parameters are set by gs,
// restored by Q and inherited by the forms, patterns and Type 3 glyphs the
// content invokes, and only an operation that paints — with the colour space
// current at that moment — counts. The rule used to OR every gs a page named,
// in whatever order and inside or outside q/Q, against every colour space it
// selected, so "q /GSopm gs Q /GSop gs … f" was reported although the fill ran
// at overprint mode 0; and forms were never examined (audit 2026-09-22 C65).
//
// The rule that an ICCBased space shall not embed the output intent's or the
// blending space's profile is checkICCProfileIdentity's.
func checkICCBasedUsageRules(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		if doc.Cancel.Stopped() {
			break
		}
		if core.PageOverprintsICCCMYK(doc, page.Dict) {
			errs = append(errs, Violation{
				Rule:    colourClause("iccBased", level),
				Level:   level,
				Message: "overprint mode must not be 1 when an ICCBased CMYK colour space is used with overprinting",
				Object:  page.ObjNum,
			})
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
func checkJPXImages(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil // JPXDecode is forbidden outright at PDF/A-1 (6.1.10)
	}
	rule := jpxClause(level)

	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil || !hasFilter(doc, stream, "JPXDecode") {
			continue
		}
		info := parseJP2Header(stream.Data)
		if !info.valid {
			continue
		}
		bad := func(format string, args ...interface{}) {
			errs = append(errs, Violation{
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

// exampleFindings collects at most one Violation per distinct rule and
// message. Several rules report a single representative example rather than
// every occurrence, and their candidates arrive from a range over doc.Objects,
// the object table or collectContentStreamData — Go maps, whose iteration order is
// randomised on every run. Keeping whichever candidate the range happened to
// yield first therefore named a different object each time the same file was
// validated. Keeping the numerically smallest object number instead is a total
// order over the candidates, so the report is reproducible. The choice is
// load-bearing, not incidental: reports are diffed run against run.
//
// Emission order is deliberately not part of the contract — validateView
// sorts the concatenated findings before returning them.
type exampleFindings struct {
	idx  map[string]int // rule+message -> index into errs
	errs []Violation
}

// add records e, or — when a finding with the same rule and message is already
// held — lowers that finding's object number to e's when e's is smaller.
func (f *exampleFindings) add(e Violation) {
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
