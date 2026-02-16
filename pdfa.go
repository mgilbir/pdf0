package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
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
		// Device color spaces (6.2.3/6.2.4)
		checkDeviceColorSpaces,
		// ICCBased color spaces (6.2.4.2)
		checkICCBasedProfiles,
		// Separation/DeviceN color spaces (6.2.4.4)
		checkSeparationDeviceN,
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

	var errs []ValidationError
	var pdfaIntentCount int
	var firstPdfaProfile Object
	_ = firstPdfaProfile

	for i, elem := range arr {
		dict := doc.ResolveDict(elem)
		if dict == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] is not a dictionary", i),
			})
			continue
		}

		s := dict.Get("S")
		if s == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] must have /S", i),
			})
			continue
		}

		sName, ok := s.(Name)
		if !ok {
			errs = append(errs, ValidationError{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] /S must be a name", i),
			})
			continue
		}

		// For PDF/A, at least one OutputIntent must have /S = /GTS_PDFA1
		if sName == "GTS_PDFA1" {
			pdfaIntentCount++
			profRef := dict.Get("DestOutputProfile")
			if firstPdfaProfile == nil {
				firstPdfaProfile = profRef
			}
		}

		// /DestOutputProfileRef is not allowed in PDF/A
		if dict.Get("DestOutputProfileRef") != nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.3",
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
					Rule:    "6.2.3",
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] must have /DestOutputProfile or /OutputConditionIdentifier", i),
				})
			}
		}
	}

	// PDF/A requires at least one OutputIntent with /S = /GTS_PDFA1
	if pdfaIntentCount == 0 {
		errs = append(errs, ValidationError{
			Rule:    "6.2.3",
			Level:   level,
			Message: "at least one OutputIntent must have /S /GTS_PDFA1",
		})
	}

	// If there are multiple GTS_PDFA1 output intents, they must all reference
	// the same ICC profile (same indirect reference)
	if pdfaIntentCount > 1 {
		var profileRefs []Object
		for _, elem := range arr {
			dict := doc.ResolveDict(elem)
			if dict == nil {
				continue
			}
			sName, _ := dict.Get("S").(Name)
			if sName == "GTS_PDFA1" {
				profileRefs = append(profileRefs, dict.Get("DestOutputProfile"))
			}
		}
		// Check all profiles are the same indirect reference
		for j := 1; j < len(profileRefs); j++ {
			ref0, ok0 := profileRefs[0].(IndirectRef)
			refJ, okJ := profileRefs[j].(IndirectRef)
			if ok0 && okJ {
				if ref0.Number != refJ.Number {
					errs = append(errs, ValidationError{
						Rule:    "6.2.3",
						Level:   level,
						Message: "multiple GTS_PDFA1 output intents must reference the same ICC profile",
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
		sName, _ := dict.Get("S").(Name)
		if sName == "GTS_PDFA1" && dict.Get("DestOutputProfile") == nil {
			errs = append(errs, ValidationError{
				Rule:    "6.2.3",
				Level:   level,
				Message: fmt.Sprintf("/OutputIntents[%d] with /S /GTS_PDFA1 must have /DestOutputProfile", i),
			})
		}
	}

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
				Rule:    "6.2.3",
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
		if err != nil || len(data) < 20 {
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
					Rule:    "6.2.3",
					Level:   level,
					Message: fmt.Sprintf("/OutputIntents[%d] ICC profile has unsupported color space %q", i, cs),
				})
			}
			if expectedN > 0 && int(nVal) != expectedN {
				errs = append(errs, ValidationError{
					Rule:    "6.2.3",
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
					Rule:    "6.2.3",
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
						Rule:    "6.2.3",
						Level:   level,
						Message: fmt.Sprintf("/OutputIntents[%d] ICC profile version %d.%d not allowed for PDF/A-1b (max 2.x)", i, major, minor),
					})
				}
			} else if level == PDFA2b || level == PDFA3b {
				// PDF/A-2b/3b: ICC profile version must be <= 4.x
				if major > 4 {
					errs = append(errs, ValidationError{
						Rule:    "6.2.3",
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
				if d, ok := groupRef.(*Dictionary); ok {
					groupDict = d
				}
			}
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
						Message: fmt.Sprintf("/BM must be /Normal or /Compatible, got /%s", n),
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
						Message: fmt.Sprintf("/%s must be 1.0 (PDF/A-1b)", key),
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
			if d, ok := gsRef.(*Dictionary); ok {
				gsDict = d
			}
		}
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
				if res == nil {
					if d, ok := resRef.(*Dictionary); ok {
						res = d
					}
				}
				if res != nil {
					addFromResources(res, num)
				}
			}
		case *Stream:
			resRef := v.Dict.Get("Resources")
			if resRef != nil {
				res := doc.ResolveDict(resRef)
				if res == nil {
					if d, ok := resRef.(*Dictionary); ok {
						res = d
					}
				}
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
	gsEntries := collectAllExtGState(doc)
	for _, entry := range gsEntries {
		dict := entry.dict
		num := entry.objNum

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

		// Check BM is a valid blend mode
		if bm := dict.Get("BM"); bm != nil {
			if n, ok := bm.(Name); ok {
				if !isValidBlendMode(n) {
					errs = append(errs, ValidationError{
						Rule:    "6.2.5",
						Level:   level,
						Message: fmt.Sprintf("invalid blend mode /%s", n),
						Object:  num,
					})
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
		if groupDict == nil {
			if d, ok := groupRef.(*Dictionary); ok {
				groupDict = d
			}
		}
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
			if d, ok := ap.(*Dictionary); ok {
				apDict = d
			}
		}
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
		if d, ok := resRef.(*Dictionary); ok {
			res = d
		}
	}
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
		if xobjDict == nil {
			if d, ok := xobjRef.(*Dictionary); ok {
				xobjDict = d
			}
		}
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
		if fontDict == nil {
			if d, ok := fontRef.(*Dictionary); ok {
				fontDict = d
			}
		}
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
		if patDict == nil {
			if d, ok := patRef.(*Dictionary); ok {
				patDict = d
			}
		}
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
		if d, ok := gsRef.(*Dictionary); ok {
			gsDict = d
		}
	}
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
	maxArrayLen := 8191
	maxNestingDepth := 28

	for num, iobj := range doc.Objects {
		checkObjectLimits(iobj.Value, num, level, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, 0, &errs)
	}

	// q/Q nesting depth check in content streams
	checkQNestingDepth(doc, level, &errs)

	// Page size limits for 2b+ only
	if level != PDFA1b {
		checkPageSizeLimits(doc, level, &errs)
	}

	return errs
}

func checkObjectLimits(obj Object, objNum int, level PDFALevel, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, depth int, errs *[]ValidationError) {
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
		if depth > maxNestingDepth {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("dictionary nesting depth %d exceeds maximum %d", depth, maxNestingDepth),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if v.Len() > maxDictEntries {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("dictionary has %d entries, exceeds maximum %d", v.Len(), maxDictEntries),
				Object:  objNum,
			})
		}
		for i, key := range v.Keys {
			checkObjectLimits(key, objNum, level, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, depth+1, errs)
			checkObjectLimits(v.Values[i], objNum, level, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, depth+1, errs)
		}
	case Array:
		if depth > maxNestingDepth {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("array nesting depth %d exceeds maximum %d", depth, maxNestingDepth),
				Object:  objNum,
			})
			return // Don't recurse further
		}
		if len(v) > maxArrayLen {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("array has %d elements, exceeds maximum %d", len(v), maxArrayLen),
				Object:  objNum,
			})
		}
		for _, elem := range v {
			checkObjectLimits(elem, objNum, level, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, depth+1, errs)
		}
	case *Stream:
		checkObjectLimits(&v.Dict, objNum, level, maxNameLen, maxStringLen, maxDictEntries, maxArrayLen, maxNestingDepth, depth, errs)
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

// checkQNestingDepth checks that q/Q nesting depth in content streams
// does not exceed 28 levels (PDF/A implementation limit).
func checkQNestingDepth(doc *Document, level PDFALevel, errs *[]ValidationError) {
	const maxQDepth = 28

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
		contentsRef := page.dict.Get("Contents")
		if contentsRef == nil {
			continue
		}
		data := getContentStreamData(doc, contentsRef)
		if data == nil {
			continue
		}
		depth := 0
		maxDepth := 0
		for i := 0; i < len(data); i++ {
			// Skip whitespace
			if data[i] <= ' ' {
				continue
			}
			// Check for 'q' or 'Q' operators (single character followed by whitespace/EOL or EOF)
			if data[i] == 'q' && (i+1 >= len(data) || data[i+1] <= ' ' || data[i+1] == '%') {
				// Check it's not part of a longer keyword
				if i == 0 || data[i-1] <= ' ' {
					depth++
					if depth > maxDepth {
						maxDepth = depth
					}
				} else {
					// Skip to end of token
					for i < len(data) && data[i] > ' ' {
						i++
					}
				}
			} else if data[i] == 'Q' && (i+1 >= len(data) || data[i+1] <= ' ' || data[i+1] == '%') {
				if i == 0 || data[i-1] <= ' ' {
					if depth > 0 {
						depth--
					}
				} else {
					for i < len(data) && data[i] > ' ' {
						i++
					}
				}
			} else {
				// Skip to end of token
				for i < len(data) && data[i] > ' ' {
					i++
				}
			}
		}
		if maxDepth > maxQDepth {
			*errs = append(*errs, ValidationError{
				Rule:    "6.1.7",
				Level:   level,
				Message: fmt.Sprintf("q/Q nesting depth %d exceeds maximum %d", maxDepth, maxQDepth),
				Object:  page.objNum,
			})
		}
	}
}

// getContentStreamData extracts and concatenates content stream data.
// Handles both single stream references and arrays of stream references.
func getContentStreamData(doc *Document, contentsRef Object) []byte {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *Stream:
		return decodeContentStream(v)
	case Array:
		var result []byte
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if stream, ok := streamObj.(*Stream); ok {
				data := decodeContentStream(stream)
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
		// Check what default color spaces the page defines
		hasDefaultRGB, hasDefaultCMYK, hasDefaultGray := getDefaultColorSpaces(doc, page.dict)

		// For PDF/A-4, also check page-level OutputIntents
		pageRGB, pageCMYK, pageGray := hasRGBIntent, hasCMYKIntent, hasGrayIntent
		if level == PDFA4 {
			prgb, pcmyk, pgray := getOutputIntentCoverage(doc, page.dict)
			pageRGB = pageRGB || prgb
			pageCMYK = pageCMYK || pcmyk
			pageGray = pageGray || pgray
		}

		// Scan for device color space usage on this page
		usesRGB, usesCMYK, usesGray := scanPageForDeviceCS(doc, page.dict)

		// Check if page's transparency /Group /CS provides implicit coverage
		groupRGB, groupCMYK, groupGray := getGroupCSCoverage(doc, page.dict)

		// DeviceRGB: needs DefaultRGB, RGB OutputIntent, or Group CS coverage
		if usesRGB && !hasDefaultRGB && !pageRGB && !groupRGB {
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "DeviceRGB used without matching OutputIntent or DefaultRGB",
				Object:  page.objNum,
			})
		}

		// DeviceCMYK: needs DefaultCMYK, CMYK OutputIntent, or Group CS coverage
		if usesCMYK && !hasDefaultCMYK && !pageCMYK && !groupCMYK {
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
				Level:   level,
				Message: "DeviceCMYK used without matching OutputIntent or DefaultCMYK",
				Object:  page.objNum,
			})
		}

		// DeviceGray: needs DefaultGray, any OutputIntent, or any Group CS coverage
		if usesGray && !hasDefaultGray && !pageRGB && !pageCMYK && !pageGray && !groupRGB && !groupCMYK && !groupGray {
			errs = append(errs, ValidationError{
				Rule:    "6.2.4",
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
		if d, ok := csRef.(*Dictionary); ok {
			csDict = d
		}
	}
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
		if d, ok := groupRef.(*Dictionary); ok {
			groupDict = d
		}
	}
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
	resRef := page.Get("Resources")
	if resRef == nil {
		return nil
	}
	res := doc.ResolveDict(resRef)
	if res == nil {
		if d, ok := resRef.(*Dictionary); ok {
			return d
		}
	}
	return res
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
					if d, ok := ap.(*Dictionary); ok {
						apDict = d
					}
				}
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
						scanResourcesForDeviceCS(doc, &v.Dict, seen, &usesRGB, &usesCMYK, &usesGray)
					case *Dictionary:
						for _, stateVal := range v.Values {
							if s, ok := doc.Resolve(stateVal).(*Stream); ok {
								scanResourcesForDeviceCS(doc, &s.Dict, seen, &usesRGB, &usesCMYK, &usesGray)
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
		if groupDict == nil {
			if d, ok := groupRef.(*Dictionary); ok {
				groupDict = d
			}
		}
		if groupDict != nil {
			checkCSForDevice(doc, groupDict.Get("CS"), &usesRGB, &usesCMYK, &usesGray)
		}
	}

	return
}

func scanResourcesForDeviceCS(doc *Document, container *Dictionary, seen map[*Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[container] {
		return
	}
	seen[container] = true

	res := resolveResources(doc, container)
	if res == nil {
		// Container itself might have Contents (like a page)
		contentsRef := container.Get("Contents")
		if contentsRef != nil {
			r, c, g := scanContentsForDeviceOps(doc, contentsRef)
			*usesRGB = *usesRGB || r
			*usesCMYK = *usesCMYK || c
			*usesGray = *usesGray || g
		}
		return
	}

	// Check ColorSpace dict for device CS references (including Indexed bases)
	csRef := res.Get("ColorSpace")
	if csRef != nil {
		csDict := doc.ResolveDict(csRef)
		if csDict == nil {
			if d, ok := csRef.(*Dictionary); ok {
				csDict = d
			}
		}
		if csDict != nil {
			for _, val := range csDict.Values {
				checkCSForDevice(doc, val, usesRGB, usesCMYK, usesGray)
			}
		}
	}

	// Check XObject resources
	xobjRef := res.Get("XObject")
	if xobjRef != nil {
		xobjDict := doc.ResolveDict(xobjRef)
		if xobjDict == nil {
			if d, ok := xobjRef.(*Dictionary); ok {
				xobjDict = d
			}
		}
		if xobjDict != nil {
			for _, val := range xobjDict.Values {
				resolved := doc.Resolve(val)
				stream, ok := resolved.(*Stream)
				if !ok {
					continue
				}
				subtype, _ := stream.Dict.Get("Subtype").(Name)
				if subtype == "Form" {
					// Scan Form XObject resources for device CS separately,
					// so we can apply the Form's Group /CS coverage before
					// propagating to the parent.
					var formRGB, formCMYK, formGray bool
					scanResourcesForDeviceCS(doc, &stream.Dict, seen, &formRGB, &formCMYK, &formGray)
					// Check transparency group CS
					if groupRef := stream.Dict.Get("Group"); groupRef != nil {
						groupDict := doc.ResolveDict(groupRef)
						if groupDict == nil {
							if d, ok := groupRef.(*Dictionary); ok {
								groupDict = d
							}
						}
						if groupDict != nil {
							// Group /CS being a device CS is itself device usage
							checkCSForDevice(doc, groupDict.Get("CS"), &formRGB, &formCMYK, &formGray)
							// But a calibrated Group /CS covers device CS within the Form
							if csObj := groupDict.Get("CS"); csObj != nil {
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
					checkCSForDevice(doc, stream.Dict.Get("ColorSpace"), usesRGB, usesCMYK, usesGray)
				}
			}
		}
	}

	// Check Shading resources
	shadingRef := res.Get("Shading")
	if shadingRef != nil {
		shadingDict := doc.ResolveDict(shadingRef)
		if shadingDict == nil {
			if d, ok := shadingRef.(*Dictionary); ok {
				shadingDict = d
			}
		}
		if shadingDict != nil {
			for _, val := range shadingDict.Values {
				sd := doc.ResolveDict(val)
				if sd == nil {
					// Could be a stream (type 4-7 shadings)
					if s, ok := doc.Resolve(val).(*Stream); ok {
						checkCSForDevice(doc, s.Dict.Get("ColorSpace"), usesRGB, usesCMYK, usesGray)
					}
					continue
				}
				checkCSForDevice(doc, sd.Get("ColorSpace"), usesRGB, usesCMYK, usesGray)
			}
		}
	}

	// Check Pattern resources (tiling patterns have content streams)
	patRef := res.Get("Pattern")
	if patRef != nil {
		patDict := doc.ResolveDict(patRef)
		if patDict == nil {
			if d, ok := patRef.(*Dictionary); ok {
				patDict = d
			}
		}
		if patDict != nil {
			for _, val := range patDict.Values {
				obj := doc.Resolve(val)
				if stream, ok := obj.(*Stream); ok {
					// Tiling pattern - recurse into its resources
					scanResourcesForDeviceCS(doc, &stream.Dict, seen, usesRGB, usesCMYK, usesGray)
				}
			}
		}
	}

	// Check Type3 font CharProcs
	fontRef := res.Get("Font")
	if fontRef != nil {
		fontDict := doc.ResolveDict(fontRef)
		if fontDict == nil {
			if d, ok := fontRef.(*Dictionary); ok {
				fontDict = d
			}
		}
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
					if cpDict == nil {
						if d, ok := cpRef.(*Dictionary); ok {
							cpDict = d
						}
					}
					if cpDict != nil {
						for _, cpVal := range cpDict.Values {
							cpObj := doc.Resolve(cpVal)
							if cpStream, ok := cpObj.(*Stream); ok {
								data := decodeContentStream(cpStream)
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

	// Scan content stream(s)
	contentsRef := container.Get("Contents")
	if contentsRef != nil {
		r, c, g := scanContentsForDeviceOps(doc, contentsRef)
		*usesRGB = *usesRGB || r
		*usesCMYK = *usesCMYK || c
		*usesGray = *usesGray || g
	}
}

// checkCSForDevice checks if a color space value is or contains a device color space.
// Handles direct names, arrays (Indexed, Separation, DeviceN, Pattern with base).
func checkCSForDevice(doc *Document, csObj Object, usesRGB, usesCMYK, usesGray *bool) {
	if csObj == nil {
		return
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
				checkCSForDevice(doc, arr[1], usesRGB, usesCMYK, usesGray)
			}
		case "Separation", "DeviceN":
			// Separation/DeviceN alternates are fallback color spaces, not
			// direct device CS usage - don't flag them here.
		case "Pattern":
			// [/Pattern underlyingCS] - check underlying
			if len(arr) >= 2 {
				checkCSForDevice(doc, arr[1], usesRGB, usesCMYK, usesGray)
			}
		}
	}
}

// maxContentStreamSize is the maximum decoded content stream size we'll scan.
// Larger streams are skipped to avoid decompression bombs.
const maxContentStreamSize = 1 << 20 // 1 MB

// decodeContentStream attempts to decode a stream for content scanning.
// Returns nil if the stream is too large, compressed with an unsupported filter,
// or otherwise undecodable. Uses a size-limited reader to prevent decompression bombs.
func decodeContentStream(stream *Stream) []byte {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		if len(stream.Data) > maxContentStreamSize {
			return nil
		}
		return stream.Data
	}

	// Only handle FlateDecode for content streams
	filterName, ok := filter.(Name)
	if !ok {
		return nil // arrays of filters not worth scanning for content ops
	}
	if filterName != "FlateDecode" {
		return nil // only decode flate-compressed content
	}

	if len(stream.Data) == 0 {
		return nil
	}

	r, err := zlib.NewReader(bytes.NewReader(stream.Data))
	if err != nil {
		return nil
	}
	defer r.Close()

	// Read with a size limit to prevent decompression bombs
	limited := io.LimitReader(r, maxContentStreamSize+1)
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil
	}
	if len(decoded) > maxContentStreamSize {
		return nil // too large
	}
	return decoded
}

// scanContentsForDeviceOps scans a page's Contents (stream or array of streams)
// for device color operators (rg/RG, k/K, g/G).
func scanContentsForDeviceOps(doc *Document, contentsRef Object) (usesRGB, usesCMYK, usesGray bool) {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *Stream:
		data := decodeContentStream(v)
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
				data := decodeContentStream(s)
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

		// Skip PDF names (/Name)
		if b == '/' {
			i++
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
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
				Rule:    "6.2.4",
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
					Rule:    "6.2.4",
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
	tintTransforms := make(map[Name]int) // colorant name → first seen tint transform obj num

	// Scan all objects for color space arrays used in Resources
	for num, iobj := range doc.Objects {
		dict, isDict := iobj.Value.(*Dictionary)
		stream, isStream := iobj.Value.(*Stream)

		// Check dictionary Resources/ColorSpace
		if isDict {
			checkDictForSepDeviceN(doc, dict, num, level, &errs)
			collectTintTransforms(doc, dict, tintTransforms, num, level, &errs)
		}
		// Check stream dict (e.g., Form XObjects, Image XObjects)
		if isStream {
			csObj := stream.Dict.Get("ColorSpace")
			if csObj != nil {
				checkColorSpaceValue(doc, csObj, num, level, &errs)
			}
			// Also check Resources in Form XObjects
			resRef := stream.Dict.Get("Resources")
			if resRef != nil {
				resDict := doc.ResolveDict(resRef)
				if resDict == nil {
					if d, ok := resRef.(*Dictionary); ok {
						resDict = d
					}
				}
				if resDict != nil {
					checkDictForSepDeviceN(doc, resDict, num, level, &errs)
					collectTintTransforms(doc, resDict, tintTransforms, num, level, &errs)
				}
			}
		}
		_ = dict
	}

	return errs
}

// collectTintTransforms tracks Separation color spaces by colorant name
// and flags inconsistent tint transforms for the same colorant name.
func collectTintTransforms(doc *Document, dict *Dictionary, tintTransforms map[Name]int, objNum int, level PDFALevel, errs *[]ValidationError) {
	csRef := dict.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		if d, ok := csRef.(*Dictionary); ok {
			csDict = d
		}
	}
	if csDict == nil {
		return
	}
	for _, val := range csDict.Values {
		resolved := doc.Resolve(val)
		arr, ok := resolved.(Array)
		if !ok || len(arr) < 4 {
			continue
		}
		csType, _ := arr[0].(Name)
		if csType != "Separation" {
			continue
		}
		colorantName, ok := arr[1].(Name)
		if !ok {
			continue
		}
		// Get the tint transform reference (object number)
		tintRef, isRef := arr[3].(IndirectRef)
		if !isRef {
			continue
		}
		if prevNum, exists := tintTransforms[colorantName]; exists {
			if prevNum != tintRef.Number {
				*errs = append(*errs, ValidationError{
					Rule:    "6.2.4",
					Level:   level,
					Message: fmt.Sprintf("Separation colorant /%s has inconsistent tint transforms (objects %d and %d)", colorantName, prevNum, tintRef.Number),
					Object:  objNum,
				})
			}
		} else {
			tintTransforms[colorantName] = tintRef.Number
		}
	}
}

func checkDictForSepDeviceN(doc *Document, dict *Dictionary, objNum int, level PDFALevel, errs *[]ValidationError) {
	csRef := dict.Get("ColorSpace")
	if csRef == nil {
		return
	}
	csDict := doc.ResolveDict(csRef)
	if csDict == nil {
		if d, ok := csRef.(*Dictionary); ok {
			csDict = d
		}
	}
	if csDict == nil {
		return
	}
	for _, val := range csDict.Values {
		checkColorSpaceValue(doc, val, objNum, level, errs)
	}
}

func checkColorSpaceValue(doc *Document, csObj Object, objNum int, level PDFALevel, errs *[]ValidationError) {
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
					Rule:    "6.2.4",
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

		// Get colorant names from the DeviceN array
		namesArr, namesOk := doc.Resolve(arr[1]).(Array)

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
											Message: fmt.Sprintf("DeviceN colorant /%s not found in Colorants dictionary", name),
											Object:  objNum,
										})
									}
								}
							}
						}
						// Recursively check Colorant entries
						for _, cval := range colorantsDict.Values {
							checkColorSpaceValue(doc, cval, objNum, level, errs)
						}
					}
				}
			}
		}
	}
}

// checkAlternateCS validates that an alternate color space in Separation/DeviceN
// is not a restricted space. For PDF/A-1b, device CS alternates are always forbidden
// (must be CIE-based). For 2b/3b/4, device alternates are handled by checkDeviceColorSpaces
// which verifies OutputIntent coverage.
func checkAlternateCS(doc *Document, altCS Object, objNum int, level PDFALevel, errs *[]ValidationError) {
	resolved := doc.Resolve(altCS)

	if n, ok := resolved.(Name); ok {
		switch n {
		case "DeviceRGB", "DeviceCMYK", "DeviceGray":
			// For PDF/A-1b: device alternates are always forbidden
			if level == PDFA1b {
				*errs = append(*errs, ValidationError{
					Rule:    "6.2.3",
					Level:   level,
					Message: fmt.Sprintf("Separation/DeviceN alternate color space must not be %s (must be CIE-based)", n),
					Object:  objNum,
				})
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
					checkAlternateCS(doc, arr[2], objNum, level, errs)
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
