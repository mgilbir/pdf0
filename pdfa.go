package pdf0

import (
	"bytes"
	"fmt"
	"math"
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

// ValidatePDFA checks whether doc conforms to the given PDF/A level.
// Returns nil if conformant, or a slice of violations.
// For checks that require raw file bytes (e.g., post-EOF data), use ValidatePDFABytes instead.
func ValidatePDFA(doc *Document, level PDFALevel) []ValidationError {
	return ValidatePDFABytes(doc, level, nil)
}

// ValidatePDFABytes checks whether doc conforms to the given PDF/A level.
// If rawData is non-nil, additional byte-level checks are performed (e.g., no data after %%EOF).
// Returns nil if conformant, or a slice of violations.
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
		checkWidgetAA,
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
	}

	for _, check := range checks {
		errs = append(errs, check(doc, level)...)
	}

	// Byte-level checks (require raw file data)
	if rawData != nil {
		errs = append(errs, checkNoDataAfterEOF(rawData, level)...)
	}

	return errs
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
		// Rule 6.1.2 is about header format, not version number.
		// Accept any valid version for PDF/A-1b.
	case PDFA2b, PDFA3b:
		valid := doc.Version == "1.4" || doc.Version == "1.5" || doc.Version == "1.6" || doc.Version == "1.7"
		if !valid {
			return []ValidationError{{
				Rule:    "6.1.2",
				Level:   level,
				Message: fmt.Sprintf("version must be 1.4-1.7, got %s", doc.Version),
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
				Message: fmt.Sprintf("Info dictionary may only contain /ModDate, found /%s", key),
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
func checkOutputIntents(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	oiRef := catalog.Get("OutputIntents")
	if oiRef == nil {
		return nil // OutputIntents only required when device-dependent color spaces are used
	}

	oiObj := doc.Resolve(oiRef)
	if oiObj == nil {
		return []ValidationError{{
			Rule:    "6.2.3",
			Level:   level,
			Message: "/OutputIntents reference target not found",
		}}
	}

	arr, ok := oiObj.(Array)
	if !ok {
		return []ValidationError{{
			Rule:    "6.2.3",
			Level:   level,
			Message: "/OutputIntents must be an array",
		}}
	}

	if len(arr) == 0 {
		return nil // Empty OutputIntents array is OK; absence is also OK
	}

	for i, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			return []ValidationError{{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] is not a dictionary", i),
			}}
		}

		s := dict.Get("S")
		if s == nil {
			return []ValidationError{{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must have /S", i),
			}}
		}

		if _, ok := s.(Name); !ok {
			return []ValidationError{{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /S must be a name", i),
			}}
		}

		if dict.Get("DestOutputProfile") == nil && dict.Get("OutputConditionIdentifier") == nil {
			return []ValidationError{{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must have /DestOutputProfile or /OutputConditionIdentifier", i),
			}}
		}
	}

	return nil
}

func checkNoCatalogAA(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 does not restrict /AA in catalog
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	if catalog.Get("AA") != nil {
		return []ValidationError{{
			Rule:    "6.6.1",
			Level:   level,
			Message: "catalog must not contain /AA (additional actions)",
		}}
	}
	return nil
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
	}
	return errs
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
					Message: fmt.Sprintf("stream must not have /%s (external stream reference)", key),
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

	for objNum, fontDict := range fonts {
		subtype := fontDict.Get("Subtype")
		subtypeName, _ := subtype.(Name)

		// Type3 and Type0 don't need direct embedding
		if subtypeName == "Type3" || subtypeName == "Type0" {
			continue
		}

		fdRef := fontDict.Get("FontDescriptor")
		if fdRef == nil {
			// Composite fonts: check DescendantFonts
			dfRef := fontDict.Get("DescendantFonts")
			if dfRef != nil {
				dfObj := doc.Resolve(dfRef)
				if dfArr, ok := dfObj.(Array); ok && len(dfArr) > 0 {
					cidFont := doc.ResolveDict(dfArr[0])
					if cidFont != nil {
						fdRef = cidFont.Get("FontDescriptor")
					}
				}
			}
		}

		if fdRef == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.10",
				Level:   level,
				Message: "font must have a /FontDescriptor",
				Object:  objNum,
			})
			continue
		}

		fd := doc.ResolveDict(fdRef)
		if fd == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.10",
				Level:   level,
				Message: "/FontDescriptor reference not found",
				Object:  objNum,
			})
			continue
		}

		hasEmbed := fd.Get("FontFile") != nil || fd.Get("FontFile2") != nil || fd.Get("FontFile3") != nil
		if !hasEmbed {
			baseFontObj := fontDict.Get("BaseFont")
			baseFontName := ""
			if bn, ok := baseFontObj.(Name); ok {
				baseFontName = string(bn)
			}
			errs = append(errs, ValidationError{
				Rule:    "6.2.10",
				Level:   level,
				Message: fmt.Sprintf("font %s must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)", baseFontName),
				Object:  objNum,
			})
		}
	}

	return errs
}

func collectFonts(doc *Document, pageTreeRef Object) map[int]*Dictionary {
	fonts := make(map[int]*Dictionary)
	collectFontsRecursive(doc, pageTreeRef, fonts)
	return fonts
}

func collectFontsRecursive(doc *Document, ref Object, fonts map[int]*Dictionary) {
	node := doc.ResolveDict(ref)
	if node == nil {
		return
	}

	nodeType, _ := node.Get("Type").(Name)

	if nodeType == "Pages" {
		kidsObj := doc.Resolve(node.Get("Kids"))
		if kids, ok := kidsObj.(Array); ok {
			for _, kid := range kids {
				collectFontsRecursive(doc, kid, fonts)
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
		if d, ok := resRef.(*Dictionary); ok {
			res = d
		}
		if res == nil {
			return
		}
	}

	fontDictRef := res.Get("Font")
	if fontDictRef == nil {
		return
	}
	fontDict := doc.ResolveDict(fontDictRef)
	if fontDict == nil {
		if d, ok := fontDictRef.(*Dictionary); ok {
			fontDict = d
		}
		if fontDict == nil {
			return
		}
	}

	for _, fontRef := range fontDict.Values {
		objNum := 0
		if iref, ok := fontRef.(IndirectRef); ok {
			objNum = iref.Number
		}

		fd := doc.ResolveDict(fontRef)
		if fd == nil {
			if d, ok := fontRef.(*Dictionary); ok {
				fd = d
			}
		}
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
		"Redact": true,
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

func checkAnnotationSubtypes(doc *Document, level PDFALevel) []ValidationError {
	allowed, ok := allowedAnnotSubtypes[level]
	if !ok {
		return nil
	}

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if !isAnnotation(dict) {
			continue
		}

		st, ok := dict.Get("Subtype").(Name)
		if !ok {
			continue
		}
		if !allowed[st] {
			errs = append(errs, ValidationError{
				Rule:    "6.3.1",
				Level:   level,
				Message: fmt.Sprintf("annotation subtype /%s is not allowed in %s", st, level),
				Object:  num,
			})
		}
	}
	return errs
}

// Rule 6.3.2-1/2: Non-Popup annotations require F key; flags must have Print set,
// Hidden/Invisible/ToggleNoView/NoView clear.
func checkAnnotationFlags(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if !isAnnotation(dict) {
			continue
		}

		// Popup annotations are exempt from F requirement
		st, _ := dict.Get("Subtype").(Name)
		if st == "Popup" {
			continue
		}

		fObj := dict.Get("F")
		if fObj == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation must have /F (flags)",
				Object:  num,
			})
			continue
		}
		flags, ok := fObj.(Integer)
		if !ok {
			continue
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
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation /F must have Print bit set",
				Object:  num,
			})
		}
		if int(flags)&flagHidden != 0 {
			errs = append(errs, ValidationError{
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation /F must not have Hidden bit set",
				Object:  num,
			})
		}
		if int(flags)&flagInvisible != 0 {
			errs = append(errs, ValidationError{
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation /F must not have Invisible bit set",
				Object:  num,
			})
		}
		if int(flags)&flagNoView != 0 {
			errs = append(errs, ValidationError{
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation /F must not have NoView bit set",
				Object:  num,
			})
		}
		if int(flags)&flagToggleNoView != 0 {
			errs = append(errs, ValidationError{
				Rule:    "6.3.2",
				Level:   level,
				Message: "annotation /F must not have ToggleNoView bit set",
				Object:  num,
			})
		}
	}
	return errs
}

// Rule 6.3.3-1: Annotations need AP except Popup, Link, Projection, and zero-area rects.
func checkAnnotationAppearance(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if !isAnnotation(dict) {
			continue
		}

		st, _ := dict.Get("Subtype").(Name)

		// Exempt subtypes
		if st == "Popup" || st == "Link" || st == "Projection" {
			continue
		}

		// Exempt zero-area rectangles
		if isZeroAreaRect(dict.Get("Rect")) {
			continue
		}

		ap := dict.Get("AP")
		if ap == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.3.3",
				Level:   level,
				Message: "annotation must have /AP (appearance dictionary)",
				Object:  num,
			})
			continue
		}

		apDict := doc.ResolveDict(ap)
		if apDict == nil {
			if d, ok := ap.(*Dictionary); ok {
				apDict = d
			}
		}
		if apDict == nil {
			continue
		}

		if apDict.Get("N") == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.3.3",
				Level:   level,
				Message: "annotation /AP must have /N (normal appearance)",
				Object:  num,
			})
		}
	}
	return errs
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
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		st, _ := dict.Get("Subtype").(Name)
		if st != "Widget" {
			continue
		}
		if dict.Get("A") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.4.1",
				Level:   level,
				Message: "Widget annotation must not contain /A key",
				Object:  num,
			})
		}
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
		if d, ok := afRef.(*Dictionary); ok {
			af = d
		}
	}
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
		if d, ok := afRef.(*Dictionary); ok {
			af = d
		}
	}
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
func isForbiddenAction(s Name, level PDFALevel) bool {
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
			"JavaScript":   true,
			"SetOCGState":  true,
			"GoTo3DView":   true,
			"GoToDp":       true,
			"set-state":    true,
			"no-op":        true,
		}
		return forbidden123[s]
	case PDFA4:
		// PDF/A-4: SetOCGState and GoTo3DView only allowed in 4e (not base 4)
		forbidden4 := map[Name]bool{
			"SetOCGState": true,
			"GoTo3DView":  true,
			"set-state":   true,
			"no-op":       true,
		}
		return forbidden4[s]
	}
	return false
}

func checkNoForbiddenActions(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError

	// Check catalog /OpenAction
	catalog := getCatalog(doc)
	if catalog != nil {
		oaRef := catalog.Get("OpenAction")
		if oaRef != nil {
			errs = append(errs, checkActionObject(doc, oaRef, 0, level)...)
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
			errs = append(errs, checkActionObject(doc, aRef, num, level)...)
		}

		// Check if the object itself is an action dict (has /S and /Type=Action or no /Type)
		if s, ok := dict.Get("S").(Name); ok {
			typeObj := dict.Get("Type")
			isAction := typeObj == nil || typeObj == Name("Action")
			if isAction && isForbiddenAction(s, level) {
				errs = append(errs, ValidationError{
					Rule:    "6.6.1",
					Level:   level,
					Message: fmt.Sprintf("forbidden action type /%s", s),
					Object:  num,
				})
			}
		}
	}

	return errs
}

func checkActionObject(doc *Document, ref Object, objNum int, level PDFALevel) []ValidationError {
	// ref might be an action dict or an array (for OpenAction destination)
	actionDict := doc.ResolveDict(ref)
	if actionDict == nil {
		if d, ok := ref.(*Dictionary); ok {
			actionDict = d
		}
	}
	if actionDict == nil {
		return nil // might be a destination array, not an action
	}

	s, ok := actionDict.Get("S").(Name)
	if !ok {
		return nil
	}

	if isForbiddenAction(s, level) {
		return []ValidationError{{
			Rule:    "6.6.1",
			Level:   level,
			Message: fmt.Sprintf("forbidden action type /%s", s),
			Object:  objNum,
		}}
	}
	return nil
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
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		s, _ := dict.Get("S").(Name)
		if s != "Named" {
			continue
		}
		n := dict.Get("N")
		if n == nil {
			continue
		}
		nName, ok := n.(Name)
		if !ok {
			continue
		}
		if !allowedNames[string(nName)] {
			errs = append(errs, ValidationError{
				Rule:    "6.6.1",
				Level:   level,
				Message: fmt.Sprintf("named action /%s not allowed (only NextPage, PrevPage, FirstPage, LastPage)", nName),
				Object:  num,
			})
		}
	}
	return errs
}

// Rule 6.6.3-1: Widget/FormField AA is level-gated.
// For PDF/A-1b/2b/3b: no /AA on widgets or form fields.
// For PDF/A-4: AA allowed on widgets/form fields (trigger events).
// Non-widget AA (doc/page/annot) keys restricted to: E, X, D, U, Fo, Bl.
func checkWidgetAA(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 allows AA on widgets/form fields
	}

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}

		isWidgetOrField := false
		if st, ok := dict.Get("Subtype").(Name); ok && st == "Widget" {
			isWidgetOrField = true
		}
		if dict.Get("FT") != nil {
			isWidgetOrField = true
		}

		if isWidgetOrField && dict.Get("AA") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.6.3",
				Level:   level,
				Message: "widget annotation/form field must not have /AA",
				Object:  num,
			})
		}
	}
	return errs
}

// --- Metadata checks (6.7) ---

// Rule 6.7.3: Version identification via XMP pdfaid:part, pdfaid:rev, pdfaid:conformance.
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
				Rule:    "6.7.3",
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
			Rule:    "6.7.3",
			Level:   level,
			Message: "metadata must contain pdfaid:part",
		})
	} else if part != expectedPart {
		errs = append(errs, ValidationError{
			Rule:    "6.7.3",
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
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("pdfaid:conformance must be B, got %q", conf),
			})
		}
	case PDFA4:
		// PDF/A-4: conformance must NOT be present at all (even empty value counts)
		if xmpHasKey(xmp, "pdfaid:conformance") {
			conf := extractXMPValue(xmp, "pdfaid:conformance")
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: fmt.Sprintf("PDF/A-4 must not have pdfaid:conformance, got %q", conf),
			})
		}

		// Check pdfaid:rev must be "2020" for PDF/A-4
		rev := extractXMPValue(xmp, "pdfaid:rev")
		if rev == "" {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
				Level:   level,
				Message: "PDF/A-4 metadata must contain pdfaid:rev",
			})
		} else if rev != "2020" {
			errs = append(errs, ValidationError{
				Rule:    "6.7.3",
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
	// Check attribute form: key="..."
	if strings.Contains(xmp, key+"=\"") {
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

	// Try attribute form: key="value"
	attrPrefix := key + "=\""
	if idx := strings.Index(xmp, attrPrefix); idx >= 0 {
		start := idx + len(attrPrefix)
		if end := bytes.IndexByte([]byte(xmp[start:]), '"'); end >= 0 {
			return xmp[start : start+end]
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
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}

		typeObj := dict.Get("Type")
		if t, ok := typeObj.(Name); ok && t == "ExtGState" {
			smask := dict.Get("SMask")
			if smask != nil {
				if n, ok := smask.(Name); ok && n == "None" {
					// acceptable
				} else {
					errs = append(errs, ValidationError{
						Rule:    "6.4",
						Level:   level,
						Message: "/SMask must not be used (PDF/A-1b)",
						Object:  num,
					})
				}
			}

			bm := dict.Get("BM")
			if bm != nil {
				if n, ok := bm.(Name); ok {
					if n != "Normal" && n != "Compatible" {
						errs = append(errs, ValidationError{
							Rule:    "6.4",
							Level:   level,
							Message: fmt.Sprintf("/BM must be /Normal or /Compatible, got /%s", n),
							Object:  num,
						})
					}
				}
			}

			for _, key := range []Name{"CA", "ca"} {
				v := dict.Get(key)
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
							Message: fmt.Sprintf("/%s must be 1.0 (PDF/A-1b)", key),
							Object:  num,
						})
					}
				}
			}
		}
	}
	return errs
}

// --- Image checks (6.2.7) ---

// Rule 6.2.7.1-1: No /Alternates in image XObjects.
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
					Rule:    "6.2.7",
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
						Rule:    "6.2.7",
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
					Rule:    "6.2.7",
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
					Rule:    "6.2.10",
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
					Rule:    "6.2.10",
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
	if level == PDFA1b {
		return nil // Handled by checkNoTransparency
	}

	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		typeObj := dict.Get("Type")
		if t, ok := typeObj.(Name); !ok || t != "ExtGState" {
			continue
		}

		// /TR must not be present
		if dict.Get("TR") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.5",
				Level:   level,
				Message: "ExtGState must not contain /TR",
				Object:  num,
			})
		}

		// /TR2 must be /Default if present
		if tr2 := dict.Get("TR2"); tr2 != nil {
			if n, ok := tr2.(Name); !ok || n != "Default" {
				errs = append(errs, ValidationError{
					Rule:    "6.2.5",
					Level:   level,
					Message: "/TR2 must be /Default",
					Object:  num,
				})
			}
		}

		// /HTO must not be present
		if dict.Get("HTO") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.5",
				Level:   level,
				Message: "ExtGState must not contain /HTO",
				Object:  num,
			})
		}

		// Check halftone
		if htRef := dict.Get("HT"); htRef != nil {
			checkHalftoneErrors(doc, htRef, num, level, &errs)
		}
	}
	return errs
}

func checkHalftoneErrors(doc *Document, htRef Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	htDict := doc.ResolveDict(htRef)
	if htDict == nil {
		if d, ok := htRef.(*Dictionary); ok {
			htDict = d
		}
	}
	if htDict == nil {
		return
	}

	if htType := htDict.Get("HalftoneType"); htType != nil {
		if intVal, ok := htType.(Integer); ok {
			if intVal != 1 && intVal != 5 {
				*errs = append(*errs, ValidationError{
					Rule:    "6.2.5",
					Level:   level,
					Message: fmt.Sprintf("halftone type must be 1 or 5, got %d", intVal),
					Object:  objNum,
				})
			}
		}
	}

	if htDict.Get("HalftoneName") != nil {
		*errs = append(*errs, ValidationError{
			Rule:    "6.2.5",
			Level:   level,
			Message: "halftone must not contain /HalftoneName",
			Object:  objNum,
		})
	}

	if htDict.Get("TransferFunction") != nil {
		*errs = append(*errs, ValidationError{
			Rule:    "6.2.5",
			Level:   level,
			Message: "halftone must not contain /TransferFunction",
			Object:  objNum,
		})
	}
}

// --- Info/XMP consistency check (MR-6) ---

// Rule 6.7.3: PDF/A-1b requires Info dict and XMP metadata to be consistent.
func checkInfoXMPConsistency(doc *Document, level PDFALevel) []ValidationError {
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
		infoVal := getInfoString(infoDict, p.infoKey)
		if infoVal == "" {
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
		return string(s.Value)
	}
	return ""
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
			}
			rest := s[17:]
			rest = strings.TrimPrefix(rest, "'")
			if len(rest) >= 2 {
				tzOff += ":" + rest[0:2]
			} else {
				tzOff += ":00"
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
	return strings.TrimSpace(s)
}

// --- Transparency blending check (MR-2) ---

// Rule 6.2.4: Pages using transparency must have proper blending color space.
func checkTransparencyBlending(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b || level == PDFA4 {
		return nil // PDF/A-1b prohibits transparency; PDF/A-4 has different rules (page-level OutputIntents)
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
			if d, ok := groupRef.(*Dictionary); ok {
				groupDict = d
			}
		}
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
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "page transparency group must have /CS (color space)",
				Object:  page.objNum,
			})
		}
	}

	return errs
}

// pageUsesTransparency checks if a page's resources reference transparency features.
func pageUsesTransparency(doc *Document, page *Dictionary) bool {
	resRef := page.Get("Resources")
	if resRef == nil {
		return false
	}
	res := doc.ResolveDict(resRef)
	if res == nil {
		if d, ok := resRef.(*Dictionary); ok {
			res = d
		}
	}
	if res == nil {
		return false
	}

	// Check ExtGState resources
	gsRef := res.Get("ExtGState")
	if gsRef != nil {
		gsDict := doc.ResolveDict(gsRef)
		if gsDict == nil {
			if d, ok := gsRef.(*Dictionary); ok {
				gsDict = d
			}
		}
		if gsDict != nil {
			for _, val := range gsDict.Values {
				gs := doc.ResolveDict(val)
				if gs == nil {
					continue
				}
				// Only check CA/ca for non-opaque values as the primary
				// transparency indicator requiring a page-level group.
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
	var pages []pageInfo
	collectPagesRecursive(doc, pageTreeRef, &pages)
	return pages
}

func collectPagesRecursive(doc *Document, ref Object, pages *[]pageInfo) {
	objNum := 0
	if iref, ok := ref.(IndirectRef); ok {
		objNum = iref.Number
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
				collectPagesRecursive(doc, kid, pages)
			}
		}
	} else if nodeType == "Page" {
		*pages = append(*pages, pageInfo{dict: node, objNum: objNum})
	}
}

// --- Embedded files check (MR-4) ---

// Rule 6.1.12: Embedded file restrictions.
func checkEmbeddedFiles(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}

	namesRef := catalog.Get("Names")
	if namesRef == nil {
		return nil
	}
	namesDict := doc.ResolveDict(namesRef)
	if namesDict == nil {
		if d, ok := namesRef.(*Dictionary); ok {
			namesDict = d
		}
	}
	if namesDict == nil {
		return nil
	}

	efRef := namesDict.Get("EmbeddedFiles")

	switch level {
	case PDFA1b, PDFA2b:
		if efRef != nil {
			return []ValidationError{{
				Rule:    "6.1.12",
				Level:   level,
				Message: "Names/EmbeddedFiles must not be present",
			}}
		}
		return nil
	case PDFA3b, PDFA4:
		if efRef == nil {
			return nil
		}
		return checkEmbeddedFileSpecs(doc, level, catalog)
	}
	return nil
}

func checkEmbeddedFileSpecs(doc *Document, level PDFALevel, catalog *Dictionary) []ValidationError {
	var errs []ValidationError

	// Check that /AF exists somewhere in the document
	if !documentHasAF(doc) {
		errs = append(errs, ValidationError{
			Rule:    "6.1.12",
			Level:   level,
			Message: "document must have /AF array when embedded files are present",
		})
	}

	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		typeObj := dict.Get("Type")
		if t, ok := typeObj.(Name); !ok || t != "Filespec" {
			continue
		}

		if dict.Get("F") == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.1.12",
				Level:   level,
				Message: "filespec must have /F",
				Object:  num,
			})
		}
		if dict.Get("UF") == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.1.12",
				Level:   level,
				Message: "filespec must have /UF",
				Object:  num,
			})
		}
		if dict.Get("AFRelationship") == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.1.12",
				Level:   level,
				Message: "filespec must have /AFRelationship",
				Object:  num,
			})
		}

		if level == PDFA4 {
			efDictRef := dict.Get("EF")
			if efDictRef != nil {
				efDict := doc.ResolveDict(efDictRef)
				if efDict == nil {
					if d, ok := efDictRef.(*Dictionary); ok {
						efDict = d
					}
				}
				if efDict != nil {
					for _, val := range efDict.Values {
						efStream := doc.Resolve(val)
						if stream, ok := efStream.(*Stream); ok {
							st := stream.Dict.Get("Subtype")
							if st == nil {
								errs = append(errs, ValidationError{
									Rule:    "6.1.12",
									Level:   level,
									Message: "embedded file stream must have /Subtype (MIME type)",
									Object:  num,
								})
							} else if name, ok := st.(Name); ok {
								if !strings.Contains(string(name), "/") {
									errs = append(errs, ValidationError{
										Rule:    "6.1.12",
										Level:   level,
										Message: fmt.Sprintf("embedded file stream /Subtype must be a MIME type, got /%s", name),
										Object:  num,
									})
								}
							}
						}
					}
				}
			}
		}
	}

	return errs
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
	if level != PDFA4 {
		return nil
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
		if d, ok := ocpRef.(*Dictionary); ok {
			ocpDict = d
		}
	}
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
		if d, ok := dRef.(*Dictionary); ok {
			dDict = d
		}
	}
	if dDict == nil {
		return errs
	}

	if dDict.Get("Name") == nil {
		errs = append(errs, ValidationError{
			Rule:    "6.1.13",
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
		}
	}
	for _, cfgRef := range configs {
		cfgDict := doc.ResolveDict(cfgRef)
		if cfgDict == nil {
			if d, ok := cfgRef.(*Dictionary); ok {
				cfgDict = d
			}
		}
		if cfgDict == nil {
			continue
		}
		if nameObj := cfgDict.Get("Name"); nameObj != nil {
			if s, ok := nameObj.(String); ok {
				n := string(s.Value)
				if names[n] {
					errs = append(errs, ValidationError{
						Rule:    "6.1.13",
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
							Rule:    "6.1.13",
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
func checkImplementationLimits(doc *Document, level PDFALevel) []ValidationError {
	var errs []ValidationError

	maxNameLen := 127
	maxStringLen := 65535
	if level != PDFA1b {
		maxStringLen = 32767
	}
	maxDictEntries := 4095

	for num, iobj := range doc.Objects {
		checkObjectLimits(iobj.Value, num, level, maxNameLen, maxStringLen, maxDictEntries, &errs)
	}

	// Page size limits for 2b+ only
	if level != PDFA1b {
		checkPageSizeLimits(doc, level, &errs)
	}

	return errs
}

func checkObjectLimits(obj Object, objNum int, level PDFALevel, maxNameLen, maxStringLen, maxDictEntries int, errs *[]ValidationError) {
	if obj == nil {
		return
	}

	switch v := obj.(type) {
	case Name:
		if len(string(v)) > maxNameLen {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("name length %d exceeds maximum %d", len(string(v)), maxNameLen),
				Object:  objNum,
			})
		}
	case String:
		if len(v.Value) > maxStringLen {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("string length %d exceeds maximum %d", len(v.Value), maxStringLen),
				Object:  objNum,
			})
		}
	case Integer:
		i := int64(v)
		if i < -2147483648 || i > 2147483647 {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("integer %d out of range [-2^31, 2^31-1]", i),
				Object:  objNum,
			})
		}
	case *Dictionary:
		if v.Len() > maxDictEntries {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("dictionary has %d entries, exceeds maximum %d", v.Len(), maxDictEntries),
				Object:  objNum,
			})
		}
		for i, key := range v.Keys {
			checkObjectLimits(key, objNum, level, maxNameLen, maxStringLen, maxDictEntries, errs)
			checkObjectLimits(v.Values[i], objNum, level, maxNameLen, maxStringLen, maxDictEntries, errs)
		}
	case Array:
		for _, elem := range v {
			checkObjectLimits(elem, objNum, level, maxNameLen, maxStringLen, maxDictEntries, errs)
		}
	case *Stream:
		checkObjectLimits(&v.Dict, objNum, level, maxNameLen, maxStringLen, maxDictEntries, errs)
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
		for _, boxKey := range []Name{"MediaBox", "CropBox"} {
			boxObj := page.dict.Get(boxKey)
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
					Rule:    "6.1.7",
					Level:   level,
					Message: fmt.Sprintf("page %s dimensions %.0fx%.0f out of range [3, 14400]", boxKey, width, height),
					Object:  page.objNum,
				})
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
