package pdf0

import (
	"fmt"
	"strings"
)

// Predefined XMP schema tables. PDF/A-1 (ISO 19005-1) normatively references
// the XMP Specification of January 2004; PDF/A-2/-3 (ISO 19005-2/-3) the
// 2005-era XMP Specification, which adds schemas (Dynamic Media, EXIF aux,
// Camera Raw) and a handful of properties. A property in one of these
// namespaces must exist in the table and match its value form; a property in
// any other namespace must be declared by an embedded extension schema.

// xmpSyntax constrains the text of a simple value (or array item).
type xmpSyntax int

const (
	synText xmpSyntax = iota
	synInteger
	synReal
	synRational
	synDate
	synBoolean
)

// xmpPropType describes the expected value form of a property.
type xmpPropType struct {
	Form   xmpKind   // xmpSimple, xmpStruct, xmpBag, xmpSeq, xmpAlt
	Lang   bool      // Alt items must carry xml:lang (language alternative)
	Syntax xmpSyntax // syntax of simple values / array items
}

// Shorthand constructors for the tables.
func tText() xmpPropType     { return xmpPropType{Form: xmpSimple, Syntax: synText} }
func tInt() xmpPropType      { return xmpPropType{Form: xmpSimple, Syntax: synInteger} }
func tReal() xmpPropType     { return xmpPropType{Form: xmpSimple, Syntax: synReal} }
func tRational() xmpPropType { return xmpPropType{Form: xmpSimple, Syntax: synRational} }
func tDate() xmpPropType     { return xmpPropType{Form: xmpSimple, Syntax: synDate} }
func tBool() xmpPropType     { return xmpPropType{Form: xmpSimple, Syntax: synBoolean} }
func tStruct() xmpPropType   { return xmpPropType{Form: xmpStruct} }
func tLangAlt() xmpPropType  { return xmpPropType{Form: xmpAlt, Lang: true, Syntax: synText} }

func tBag(s xmpSyntax) xmpPropType   { return xmpPropType{Form: xmpBag, Syntax: s} }
func tSeq(s xmpSyntax) xmpPropType   { return xmpPropType{Form: xmpSeq, Syntax: s} }
func tAltOf(s xmpSyntax) xmpPropType { return xmpPropType{Form: xmpAlt, Syntax: s} }
func tBagStruct() xmpPropType        { return xmpPropType{Form: xmpBag, Syntax: -1} }
func tSeqStruct() xmpPropType        { return xmpPropType{Form: xmpSeq, Syntax: -1} }

// isStructArray reports whether the array items are structures.
func (t xmpPropType) isStructArray() bool { return t.Syntax == -1 }

// Namespace URIs of the predefined schemas.
const (
	nsDC        = "http://purl.org/dc/elements/1.1/"
	nsXMPBasic  = "http://ns.adobe.com/xap/1.0/"
	nsXMPRights = "http://ns.adobe.com/xap/1.0/rights/"
	nsXMPMM     = "http://ns.adobe.com/xap/1.0/mm/"
	nsXMPBJ     = "http://ns.adobe.com/xap/1.0/bj/"
	nsXMPTPg    = "http://ns.adobe.com/xap/1.0/t/pg/"
	nsAdobePDF  = "http://ns.adobe.com/pdf/1.3/"
	nsPhotoshop = "http://ns.adobe.com/photoshop/1.0/"
	nsTIFF      = "http://ns.adobe.com/tiff/1.0/"
	nsEXIF      = "http://ns.adobe.com/exif/1.0/"
	nsEXIFAux   = "http://ns.adobe.com/exif/1.0/aux/"
	nsXMPDM     = "http://ns.adobe.com/xmp/1.0/DynamicMedia/"
	nsCameraRaw = "http://ns.adobe.com/camera-raw-settings/1.0/"
	nsPDFAID    = "http://www.aiim.org/pdfa/ns/id/"
	nsXMPIdent  = "http://ns.adobe.com/xmp/Identifier/qual/1.0/"
	nsSTRef     = "http://ns.adobe.com/xap/1.0/sType/ResourceRef#"
	nsSTEvt     = "http://ns.adobe.com/xap/1.0/sType/ResourceEvent#"
	nsSTVer     = "http://ns.adobe.com/xap/1.0/sType/Version#"
	nsSTJob     = "http://ns.adobe.com/xap/1.0/sType/Job#"
	nsSTDim     = "http://ns.adobe.com/xap/1.0/sType/Dimensions#"
	nsSTFnt     = "http://ns.adobe.com/xap/1.0/sType/Font#"
	nsXMPGImg   = "http://ns.adobe.com/xap/1.0/g/img/"
)

var xmpDCProperties = map[string]xmpPropType{
	"contributor": tBag(synText),
	"coverage":    tText(),
	"creator":     tSeq(synText),
	"date":        tSeq(synDate),
	"description": tLangAlt(),
	"format":      tText(),
	"identifier":  tText(),
	"language":    tBag(synText),
	"publisher":   tBag(synText),
	"relation":    tBag(synText),
	"rights":      tLangAlt(),
	"source":      tText(),
	"subject":     tBag(synText),
	"title":       tLangAlt(),
	"type":        tBag(synText),
}

var xmpBasicProperties = map[string]xmpPropType{
	"Advisory":     tBag(synText),
	"BaseURL":      tText(),
	"CreateDate":   tDate(),
	"CreatorTool":  tText(),
	"Identifier":   tBag(synText),
	"MetadataDate": tDate(),
	"ModifyDate":   tDate(),
	"Nickname":     tText(),
	"Thumbnails":   {Form: xmpAlt, Syntax: -1}, // Alt of Thumbnail structures
}

var xmpRightsProperties = map[string]xmpPropType{
	"Certificate":  tText(),
	"Marked":       tBool(),
	"Owner":        tBag(synText),
	"UsageTerms":   tLangAlt(),
	"WebStatement": tText(),
}

var xmpMMProperties = map[string]xmpPropType{
	"DerivedFrom":     tStruct(),
	"DocumentID":      tText(),
	"History":         tSeqStruct(),
	"InstanceID":      tText(),
	"LastURL":         tText(),
	"ManagedFrom":     tStruct(),
	"Manager":         tText(),
	"ManageTo":        tText(),
	"ManageUI":        tText(),
	"ManagerVariant":  tText(),
	"RenditionClass":  tText(),
	"RenditionOf":     tStruct(),
	"RenditionParams": tText(),
	"SaveID":          tInt(),
	"VersionID":       tText(),
	"Versions":        tSeqStruct(),
}

var xmpBJProperties = map[string]xmpPropType{
	"JobRef": tBagStruct(),
}

var xmpTPgProperties = map[string]xmpPropType{
	"MaxPageSize": tStruct(),
	"NPages":      tInt(),
	// XMP 2005 additions (PDF/A-2/-3 only; gated below).
	"Fonts":      tBagStruct(),
	"Colorants":  tSeqStruct(),
	"PlateNames": tSeq(synText),
}

var xmpAdobePDFProperties = map[string]xmpPropType{
	"Keywords":   tText(),
	"PDFVersion": tText(),
	"Producer":   tText(),
	// XMP 2005 addition.
	"Trapped": tBool(),
}

var xmpPhotoshopProperties = map[string]xmpPropType{
	"AuthorsPosition":        tText(),
	"CaptionWriter":          tText(),
	"Category":               tText(),
	"City":                   tText(),
	"Country":                tText(),
	"Credit":                 tText(),
	"DateCreated":            tDate(),
	"Headline":               tText(),
	"History":                tText(),
	"Instructions":           tText(),
	"Source":                 tText(),
	"State":                  tText(),
	"SupplementalCategories": tBag(synText), // XMP 2005; the 2004 form is Text (1b override below)
	"TransmissionReference":  tText(),
	"Urgency":                tInt(),
	// XMP 2005 additions.
	"ColorMode":  tInt(),
	"ICCProfile": tText(),
	"TextLayers": tSeqStruct(),
}

var xmpTIFFProperties = map[string]xmpPropType{
	"ImageWidth":                tInt(),
	"ImageLength":               tInt(),
	"BitsPerSample":             tSeq(synInteger),
	"Compression":               tInt(),
	"PhotometricInterpretation": tInt(),
	"Orientation":               tInt(),
	"SamplesPerPixel":           tInt(),
	"PlanarConfiguration":       tInt(),
	"YCbCrSubSampling":          tSeq(synInteger),
	"YCbCrPositioning":          tInt(),
	"XResolution":               tRational(),
	"YResolution":               tRational(),
	"ResolutionUnit":            tInt(),
	"TransferFunction":          tSeq(synInteger),
	"WhitePoint":                tSeq(synRational),
	"PrimaryChromaticities":     tSeq(synRational),
	"YCbCrCoefficients":         tSeq(synRational),
	"ReferenceBlackWhite":       tSeq(synRational),
	"DateTime":                  tDate(),
	"ImageDescription":          tLangAlt(),
	"Make":                      tText(),
	"Model":                     tText(),
	"Software":                  tText(),
	"Artist":                    tText(),
	"Copyright":                 tLangAlt(),
}

var xmpEXIFProperties = map[string]xmpPropType{
	"ExifVersion":              tText(),
	"FlashpixVersion":          tText(),
	"ColorSpace":               tInt(),
	"ComponentsConfiguration":  tSeq(synInteger),
	"CompressedBitsPerPixel":   tRational(),
	"PixelXDimension":          tInt(),
	"PixelYDimension":          tInt(),
	"UserComment":              tLangAlt(),
	"RelatedSoundFile":         tText(),
	"DateTimeOriginal":         tDate(),
	"DateTimeDigitized":        tDate(),
	"ExposureTime":             tRational(),
	"FNumber":                  tRational(),
	"ExposureProgram":          tInt(),
	"SpectralSensitivity":      tText(),
	"ISOSpeedRatings":          tSeq(synInteger),
	"OECF":                     tStruct(),
	"ShutterSpeedValue":        tRational(),
	"ApertureValue":            tRational(),
	"BrightnessValue":          tRational(),
	"ExposureBiasValue":        tRational(),
	"MaxApertureValue":         tRational(),
	"SubjectDistance":          tRational(),
	"MeteringMode":             tInt(),
	"LightSource":              tInt(),
	"Flash":                    tStruct(),
	"FocalLength":              tRational(),
	"SubjectArea":              tSeq(synInteger),
	"FlashEnergy":              tRational(),
	"SpatialFrequencyResponse": tStruct(),
	"FocalPlaneXResolution":    tRational(),
	"FocalPlaneYResolution":    tRational(),
	"FocalPlaneResolutionUnit": tInt(),
	"SubjectLocation":          tSeq(synInteger),
	"ExposureIndex":            tRational(),
	"SensingMethod":            tInt(),
	"FileSource":               tInt(),
	"SceneType":                tInt(),
	"CFAPattern":               tStruct(),
	"CustomRendered":           tInt(),
	"ExposureMode":             tInt(),
	"WhiteBalance":             tInt(),
	"DigitalZoomRatio":         tRational(),
	"FocalLengthIn35mmFilm":    tInt(),
	"SceneCaptureType":         tInt(),
	"GainControl":              tInt(),
	"Contrast":                 tInt(),
	"Saturation":               tInt(),
	"Sharpness":                tInt(),
	"DeviceSettingDescription": tStruct(),
	"SubjectDistanceRange":     tInt(),
	"ImageUniqueID":            tText(),
	"MakerNote":                tText(),
	"GPSVersionID":             tText(),
	"GPSLatitude":              tText(),
	"GPSLongitude":             tText(),
	"GPSAltitudeRef":           tInt(),
	"GPSAltitude":              tRational(),
	"GPSTimeStamp":             tDate(),
	"GPSSatellites":            tText(),
	"GPSStatus":                tText(),
	"GPSMeasureMode":           tInt(), // corpus: "2" passes, "2.0" fails
	"GPSDOP":                   tRational(),
	"GPSSpeedRef":              tText(),
	"GPSSpeed":                 tRational(),
	"GPSTrackRef":              tText(),
	"GPSTrack":                 tRational(),
	"GPSImgDirectionRef":       tText(),
	"GPSImgDirection":          tRational(),
	"GPSMapDatum":              tText(),
	"GPSDestLatitude":          tText(),
	"GPSDestLongitude":         tText(),
	"GPSDestBearingRef":        tText(),
	"GPSDestBearing":           tRational(),
	"GPSDestDistanceRef":       tText(),
	"GPSDestDistance":          tRational(),
	"GPSProcessingMethod":      tText(),
	"GPSAreaInformation":       tText(),
	"GPSDifferential":          tInt(),
}

// EXIF aux schema (XMP 2005; PDF/A-2/-3 only).
var xmpEXIFAuxProperties = map[string]xmpPropType{
	"Lens":                               tText(),
	"SerialNumber":                       tText(),
	"Firmware":                           tText(),
	"FlashCompensation":                  tRational(),
	"OwnerName":                          tText(),
	"ImageNumber":                        tInt(),
	"VignetteCorrectionAlreadyApplied":   tBool(),
	"LensCorrectionSettings":             tText(),
	"LensInfo":                           tText(),
	"LensID":                             tText(),
	"ApproximateFocusDistance":           tRational(),
	"DistortionCorrectionAlreadyApplied": tBool(),
	"LateralChromaticAberrationCorrectionAlreadyApplied": tBool(),
}

// Dynamic Media schema (XMP 2005; PDF/A-2/-3 only).
var xmpDMProperties = map[string]xmpPropType{
	"projectRef":                   tStruct(),
	"videoFrameRate":               tText(),
	"videoFrameSize":               tStruct(),
	"videoPixelAspectRatio":        tRational(),
	"videoPixelDepth":              tText(),
	"videoColorSpace":              tText(),
	"videoAlphaMode":               tText(),
	"videoAlphaPremultipleColor":   tStruct(),
	"videoAlphaUnityIsTransparent": tBool(),
	"videoCompressor":              tText(),
	"videoFieldOrder":              tText(),
	"pullDown":                     tText(),
	"audioSampleRate":              tInt(),
	"audioSampleType":              tText(),
	"audioChannelType":             tText(),
	"audioCompressor":              tText(),
	"speakerPlacement":             tText(),
	"fileDataRate":                 tRational(),
	"tapeName":                     tText(),
	"altTapeName":                  tText(),
	"startTimecode":                tStruct(),
	"altTimecode":                  tStruct(),
	"duration":                     tStruct(),
	"scene":                        tText(),
	"shotName":                     tText(),
	"shotDate":                     tDate(),
	"shotLocation":                 tText(),
	"logComment":                   tText(),
	"markers":                      tSeqStruct(),
	"contributedMedia":             tBagStruct(),
	"absPeakAudioFilePath":         tText(),
	"relativePeakAudioFilePath":    tText(),
	"videoModDate":                 tDate(),
	"audioModDate":                 tDate(),
	"metadataModDate":              tDate(),
	"artist":                       tText(),
	"album":                        tText(),
	"trackNumber":                  tInt(),
	"genre":                        tText(),
	"copyright":                    tText(),
	"releaseDate":                  tDate(),
	"composer":                     tText(),
	"engineer":                     tText(),
	"tempo":                        tReal(),
	"instrument":                   tText(),
	"introTime":                    tStruct(),
	"outCue":                       tStruct(),
	"relativeTimestamp":            tStruct(),
	"loop":                         tBool(),
	"numberOfBeats":                tReal(),
	"key":                          tText(),
	"stretchMode":                  tText(),
	"timeScaleParams":              tStruct(),
	"resampleParams":               tStruct(),
	"beatSpliceParams":             tStruct(),
	"timeSignature":                tText(),
	"scaleType":                    tText(),
}

// Camera Raw schema (XMP 2005; PDF/A-2/-3 only).
var xmpCameraRawProperties = map[string]xmpPropType{
	"AutoBrightness":       tBool(),
	"AutoContrast":         tBool(),
	"AutoExposure":         tBool(),
	"AutoShadows":          tBool(),
	"BlueHue":              tInt(),
	"BlueSaturation":       tInt(),
	"Brightness":           tInt(),
	"CameraProfile":        tText(),
	"ChromaticAberrationB": tInt(),
	"ChromaticAberrationR": tInt(),
	"ColorNoiseReduction":  tInt(),
	"Contrast":             tInt(),
	"CropTop":              tReal(),
	"CropLeft":             tReal(),
	"CropBottom":           tReal(),
	"CropRight":            tReal(),
	"CropAngle":            tReal(),
	"CropWidth":            tReal(),
	"CropHeight":           tReal(),
	"CropUnits":            tInt(),
	"Exposure":             tReal(),
	"GreenHue":             tInt(),
	"GreenSaturation":      tInt(),
	"HasCrop":              tBool(),
	"HasSettings":          tBool(),
	"LuminanceSmoothing":   tInt(),
	"RawFileName":          tText(),
	"RedHue":               tInt(),
	"RedSaturation":        tInt(),
	"Saturation":           tInt(),
	"Shadows":              tInt(),
	"ShadowTint":           tInt(),
	"Sharpness":            tInt(),
	"Temperature":          tInt(),
	"Tint":                 tInt(),
	"ToneCurve":            tSeq(synText),
	"ToneCurveName":        tText(),
	"Version":              tText(),
	"VignetteAmount":       tInt(),
	"VignetteMidpoint":     tInt(),
	"WhiteBalance":         tText(),
}

// PDF/A identification schema.
var xmpPDFAIDProperties = map[string]xmpPropType{
	"part":        tInt(),
	"conformance": tText(),
	"amd":         tText(),
	"corr":        tText(),
	"rev":         tInt(),
}

// XMP 2005 additions to the Basic and identifier-qualifier schemas.
var xmpBasic2005Additions = map[string]xmpPropType{
	"Label":  tText(),
	"Rating": tReal(), // corpus: "1.0" passes at 2b
}

var xmpIdentQualProperties = map[string]xmpPropType{
	"Scheme": tText(),
}

// predefinedXMPSchemas returns the predefined schema tables for a level.
func predefinedXMPSchemas(level PDFALevel) map[string]map[string]xmpPropType {
	schemas := map[string]map[string]xmpPropType{
		nsDC:        xmpDCProperties,
		nsXMPBasic:  xmpBasicProperties,
		nsXMPRights: xmpRightsProperties,
		nsXMPMM:     xmpMMProperties,
		nsXMPBJ:     xmpBJProperties,
		nsXMPTPg:    xmpTPgProperties,
		nsAdobePDF:  xmpAdobePDFProperties,
		nsPhotoshop: xmpPhotoshopProperties,
		nsTIFF:      xmpTIFFProperties,
		nsEXIF:      xmpEXIFProperties,
		nsPDFAID:    xmpPDFAIDProperties,
	}
	if level != PDFA1b {
		schemas[nsEXIFAux] = xmpEXIFAuxProperties
		schemas[nsXMPDM] = xmpDMProperties
		schemas[nsCameraRaw] = xmpCameraRawProperties
		schemas[nsXMPIdent] = xmpIdentQualProperties
		basic := make(map[string]xmpPropType, len(xmpBasicProperties)+len(xmpBasic2005Additions))
		for k, v := range xmpBasicProperties {
			basic[k] = v
		}
		for k, v := range xmpBasic2005Additions {
			basic[k] = v
		}
		schemas[nsXMPBasic] = basic
	} else {
		// PDF/A-1 (2005) predates the corr (corrigendum) identifier.
		aid := make(map[string]xmpPropType, len(xmpPDFAIDProperties))
		for k, v := range xmpPDFAIDProperties {
			aid[k] = v
		}
		delete(aid, "corr")
		schemas[nsPDFAID] = aid
		// XMP 2004 versions of the tables: drop the 2005-only properties.
		tpg := map[string]xmpPropType{"MaxPageSize": tStruct(), "NPages": tInt()}
		schemas[nsXMPTPg] = tpg
		pdfNS := map[string]xmpPropType{"Keywords": tText(), "PDFVersion": tText(), "Producer": tText()}
		schemas[nsAdobePDF] = pdfNS
		ps := make(map[string]xmpPropType, len(xmpPhotoshopProperties))
		for k, v := range xmpPhotoshopProperties {
			ps[k] = v
		}
		delete(ps, "ColorMode")
		delete(ps, "ICCProfile")
		delete(ps, "TextLayers")
		// XMP 2004 form: simple Text (the corpus fails a Bag at 1b and
		// passes one at 2b).
		ps["SupplementalCategories"] = tText()
		schemas[nsPhotoshop] = ps
	}
	return schemas
}

// --- simple-value syntax validation ---

func validXMPSyntax(s string, syn xmpSyntax) bool {
	switch syn {
	case synInteger:
		return isXMPInteger(s)
	case synReal:
		return isXMPReal(s)
	case synRational:
		return isXMPRational(s)
	case synDate:
		return isXMPDate(s)
	case synBoolean:
		return s == "True" || s == "False"
	}
	return true
}

func (s xmpSyntax) String() string {
	switch s {
	case synInteger:
		return "Integer"
	case synReal:
		return "Real"
	case synRational:
		return "Rational"
	case synDate:
		return "Date"
	case synBoolean:
		return "Boolean"
	}
	return "Text"
}

func isXMPInteger(s string) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isXMPReal(s string) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	digits, dot := 0, 0
	for ; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.' && dot == 0:
			dot++
		default:
			return false
		}
	}
	return digits > 0
}

func isXMPRational(s string) bool {
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return false
	}
	return isXMPInteger(s[:slash]) && isXMPInteger(s[slash+1:])
}

// isXMPDate accepts the XMP date forms: YYYY, YYYY-MM, YYYY-MM-DD, and the
// full forms with time YYYY-MM-DDThh:mm(:ss(.f+)?)?(Z|±hh:mm).
func isXMPDate(s string) bool {
	digits := func(sub string) bool {
		if sub == "" {
			return false
		}
		for i := 0; i < len(sub); i++ {
			if sub[i] < '0' || sub[i] > '9' {
				return false
			}
		}
		return true
	}
	if len(s) == 4 {
		return digits(s)
	}
	if len(s) == 7 {
		return digits(s[:4]) && s[4] == '-' && digits(s[5:7])
	}
	if len(s) == 10 {
		return digits(s[:4]) && s[4] == '-' && digits(s[5:7]) && s[7] == '-' && digits(s[8:10])
	}
	// Full date-time.
	if len(s) < 17 || !digits(s[:4]) || s[4] != '-' || !digits(s[5:7]) || s[7] != '-' || !digits(s[8:10]) || s[10] != 'T' {
		return false
	}
	if !digits(s[11:13]) || s[13] != ':' || !digits(s[14:16]) {
		return false
	}
	rest := s[16:]
	// Optional seconds and fraction.
	if strings.HasPrefix(rest, ":") {
		if len(rest) < 3 || !digits(rest[1:3]) {
			return false
		}
		rest = rest[3:]
		if strings.HasPrefix(rest, ".") {
			j := 1
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			if j == 1 {
				return false
			}
			rest = rest[j:]
		}
	}
	// Time zone: Z or ±hh:mm.
	if rest == "Z" {
		return true
	}
	if len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && digits(rest[1:3]) && rest[3] == ':' && digits(rest[4:6]) {
		return true
	}
	return false
}

// --- extension schema declarations ---

// extensionDeclared extracts the properties declared by embedded PDF/A
// extension schemas: namespaceURI → property name → declared valueType text.
func extensionDeclared(props []xmpProperty) map[string]map[string]string {
	declared := make(map[string]map[string]string)
	for _, p := range props {
		if p.NS != nsPDFAExtension || p.Name != "schemas" {
			continue
		}
		for _, schema := range p.Value.Items {
			var nsURI string
			var schemaProps []xmpValue
			for _, f := range schema.Fields {
				if f.NS != nsPDFASchema {
					continue
				}
				switch f.Name {
				case "namespaceURI":
					nsURI = f.Value.Text
				case "property":
					schemaProps = f.Value.Items
				}
			}
			if nsURI == "" {
				continue
			}
			if declared[nsURI] == nil {
				declared[nsURI] = make(map[string]string)
			}
			for _, sp := range schemaProps {
				var name, valueType string
				for _, f := range sp.Fields {
					if f.NS != nsPDFAProperty {
						continue
					}
					switch f.Name {
					case "name":
						name = f.Value.Text
					case "valueType":
						valueType = f.Value.Text
					}
				}
				if name != "" {
					declared[nsURI][name] = valueType
				}
			}
		}
	}
	return declared
}

// declaredTypeToPropType maps an extension-schema valueType string to a
// checkable form. Unknown type names return ok=false (no check applied).
func declaredTypeToPropType(vt string) (xmpPropType, bool) {
	vt = strings.TrimSpace(vt)
	lower := strings.ToLower(vt)
	// Array prefixes: "Bag Text", "Seq Integer", "Alt Text", "Lang Alt".
	if lower == "lang alt" {
		return tLangAlt(), true
	}
	for _, pre := range []struct {
		word string
		form xmpKind
	}{{"bag ", xmpBag}, {"seq ", xmpSeq}, {"alt ", xmpAlt}} {
		if strings.HasPrefix(lower, pre.word) {
			if base, ok := simpleDeclaredSyntax(lower[len(pre.word):]); ok {
				return xmpPropType{Form: pre.form, Syntax: base}, true
			}
			return xmpPropType{Form: pre.form, Syntax: -1}, true
		}
	}
	if base, ok := simpleDeclaredSyntax(lower); ok {
		return xmpPropType{Form: xmpSimple, Syntax: base}, true
	}
	return xmpPropType{}, false
}

func simpleDeclaredSyntax(lower string) (xmpSyntax, bool) {
	switch lower {
	case "text", "agentname", "propername", "uri", "url", "mimetype", "locale",
		"guid", "renditionclass", "xpath", "choice", "open choice", "closed choice":
		return synText, true
	case "integer":
		return synInteger, true
	case "real":
		return synReal, true
	case "rational":
		return synRational, true
	case "date":
		return synDate, true
	case "boolean":
		return synBoolean, true
	}
	return 0, false
}

// --- the check ---

// checkXMPProperties validates every XMP property against the predefined
// schema tables (or the packet's extension schema declarations).
func checkXMPProperties(doc *Document, level PDFALevel) []ValidationError {
	// PDF/A-4 (ISO 19005-4) does NOT apply the strict per-property value-form
	// validation that 1b/2b/3b do. This is deliberate, not a TODO: the veraPDF
	// corpus proves A-4 tolerates non-conforming XMP property values — e.g.
	// PDF_A-4/6.1.5/…6-1-5-t02-pass-a.pdf carries xmp:CreateDate =
	// "D:20221116191452+00'00" (a PDF date string, not an XMP/ISO 8601 date) and
	// still passes at A-4. Enabling these checks at A-4 therefore produces false
	// positives on conformant files. A-4 XMP is instead governed by the
	// well-formedness and UTF-8 requirements, which are checked separately (see
	// checkXMPWellFormed). Do not "implement" property-value validation here for
	// A-4 without corpus evidence that veraPDF requires it.
	if level == PDFA4 {
		return nil
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*Stream)
	if !ok {
		return nil
	}
	xmp := decodeXMPToUTF8(decodeContentStream(doc, stream))
	if xmp == "" {
		return nil
	}
	props, err := parseXMPProperties([]byte(xmp))
	if err != nil {
		return nil // malformed XML is checked elsewhere
	}

	rule := metadataClause("xmpProperties", level)

	schemas := predefinedXMPSchemas(level)
	declared := extensionDeclared(props)

	var errs []ValidationError
	// The extension schema container itself is constrained at every level:
	// canonical prefixes and required description fields (ISO 19005-1 6.7.8,
	// -2/-3 6.6.2.3.3).
	containerRule := metadataClause("extSchema", level)
	errs = append(errs, checkXMPExtensionContainer(xmp, props, containerRule, level)...)
	typeFields := extensionTypeFields(props)
	for _, p := range props {
		// The extension schema machinery itself is validated separately.
		switch p.NS {
		case nsPDFAExtension, nsPDFASchema, nsPDFAProperty, nsPDFAType, nsPDFAField:
			continue
		}

		if table, isPredefined := schemas[p.NS]; isPredefined {
			pt, known := table[p.Name]
			if !known {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("XMP property %s is not defined in predefined schema %s", p.Name, p.NS),
				})
				continue
			}
			errs = append(errs, checkXMPValueForm(p, pt, rule, level)...)
			continue
		}

		if decl, ok := declared[p.NS]; ok {
			vt, propDeclared := decl[p.Name]
			if !propDeclared {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: fmt.Sprintf("XMP property %s in schema %s is not declared by its extension schema", p.Name, p.NS),
				})
				continue
			}
			if pt, checkable := declaredTypeToPropType(vt); checkable {
				errs = append(errs, checkXMPValueForm(p, pt, rule, level)...)
			} else if p.Value.Kind == xmpStruct {
				// Custom structured type: every field used by the value
				// must be declared by the type's pdfaType:field list.
				declaredFields := typeFields[vt]
				for _, f := range p.Value.Fields {
					if !declaredFields[f.Name] {
						errs = append(errs, ValidationError{
							Rule:    rule,
							Level:   level,
							Message: fmt.Sprintf("XMP property %s: structure field %s is not declared by custom value type %s", p.Name, f.Name, vt),
						})
					}
				}
			}
			continue
		}

		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf("XMP property %s uses schema %s, which is neither predefined nor declared by an extension schema", p.Name, p.NS),
		})
	}
	return errs
}

// checkXMPValueForm verifies a property value has the expected structural
// form and simple-value syntax.
func checkXMPValueForm(p xmpProperty, pt xmpPropType, rule string, level PDFALevel) []ValidationError {
	var errs []ValidationError
	bad := func(format string, args ...interface{}) {
		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf("XMP property %s: ", p.Name) + fmt.Sprintf(format, args...),
		})
	}
	v := p.Value
	if v.Kind != pt.Form {
		bad("value must be a %s, got %s", pt.Form, v.Kind)
		return errs
	}
	switch pt.Form {
	case xmpSimple:
		if !validXMPSyntax(v.Text, pt.Syntax) {
			bad("value %q is not a valid %s", v.Text, pt.Syntax)
		}
	case xmpBag, xmpSeq, xmpAlt:
		for _, item := range v.Items {
			if pt.isStructArray() {
				if item.Kind != xmpStruct {
					bad("array items must be structures, got %s", item.Kind)
					return errs
				}
				continue
			}
			if item.Kind != xmpSimple {
				bad("array items must be simple values, got %s", item.Kind)
				return errs
			}
			if pt.Lang && !item.HasLang {
				bad("language-alternative items must carry xml:lang")
				return errs
			}
			if !validXMPSyntax(item.Text, pt.Syntax) {
				bad("array item %q is not a valid %s", item.Text, pt.Syntax)
				return errs
			}
		}
	}
	return errs
}

// --- extension schema container validation (ISO 19005-2/-3, 6.6.2.3.3) ---

// canonicalXMPPrefixes are the namespace prefixes the standard REQUIRES for
// the extension schema container namespaces: binding the right URI to a
// different prefix is itself a violation.
var canonicalXMPPrefixes = map[string]string{
	nsPDFAExtension: "pdfaExtension",
	nsPDFASchema:    "pdfaSchema",
	nsPDFAProperty:  "pdfaProperty",
	nsPDFAType:      "pdfaType",
	nsPDFAField:     "pdfaField",
}

// checkXMPExtensionContainer validates the structure of embedded PDF/A
// extension schemas: canonical prefixes, required fields at every level
// (schema, property, value type, field), and correct container forms.
// standardXMPValueTypes are the value-type names an extension-schema field may
// reference without declaring them (the XMP basic and structured types, ISO
// 16684-2 / the PDF/A extension-schema value-type namespaces). Any other name
// must be declared by the schema's own pdfaType list.
var standardXMPValueTypes = map[string]bool{
	// Basic value types.
	"Text": true, "ProperName": true, "XPath": true, "Boolean": true,
	"Integer": true, "Real": true, "MIMEType": true, "AgentName": true,
	"RenditionClass": true, "URL": true, "URI": true, "Date": true,
	"GUID": true, "Locale": true, "Lang": true, "Choice": true, "Rational": true,
	// Structured value types (XMP 2005).
	"Colorant": true, "Dimensions": true, "Font": true, "Thumbnail": true,
	"ResourceRef": true, "ResourceEvent": true, "Version": true, "Job": true,
	"Struct": true, "Time": true, "Marker": true, "Media": true, "Track": true,
	"ProjectLink": true, "BeatSpliceStretch": true, "ResampleStretch": true,
	"TimeCode": true, "TimeScaleStretch": true, "PartMapping": true,
	"LayerGroup": true, "Frame": true, "CuePointParam": true,
}

func checkXMPExtensionContainer(xmp string, props []xmpProperty, rule string, level PDFALevel) []ValidationError {
	var errs []ValidationError
	report := func(format string, args ...interface{}) {
		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf(format, args...),
		})
	}

	// Canonical prefix rule, checked on the raw packet text because XML
	// parsing resolves prefixes away.
	for uri, want := range canonicalXMPPrefixes {
		for rest := xmp; ; {
			i := strings.Index(rest, `="`+uri+`"`)
			if i < 0 {
				break
			}
			// Walk back over the prefix to the "xmlns:" marker.
			j := i
			for j > 0 && rest[j-1] != ':' && rest[j-1] != ' ' && rest[j-1] != '\t' && rest[j-1] != '\n' {
				j--
			}
			prefix := rest[j:i]
			if j > 6 && rest[j-1] == ':' && strings.HasSuffix(rest[:j-1], "xmlns") && prefix != want {
				report("extension schema namespace %s must use prefix %q, found %q", uri, want, prefix)
			}
			rest = rest[i+1:]
		}
	}

	// undefinedFieldsRule is the clause for an extension-schema object carrying a
	// field not defined by the spec (ISO 19005-1 6.7.8; -2/-3 6.6.2.3.2).
	undefinedFieldsRule := "6.7.8"
	if level == PDFA2b || level == PDFA3b {
		undefinedFieldsRule = "6.6.2.3.2"
	}
	requireFields := func(what string, v xmpValue, ns string, required, optional []string) map[string]xmpValue {
		got := make(map[string]xmpValue)
		allowed := make(map[string]bool)
		for _, name := range required {
			allowed[name] = true
		}
		for _, name := range optional {
			allowed[name] = true
		}
		for _, f := range v.Fields {
			if f.NS == ns {
				got[f.Name] = f.Value
				if !allowed[f.Name] {
					errs = append(errs, ValidationError{Rule: undefinedFieldsRule, Level: level,
						Message: fmt.Sprintf("extension schema %s contains undefined field %s", what, f.Name)})
				}
			}
		}
		for _, name := range required {
			if _, ok := got[name]; !ok {
				report("extension schema %s is missing required field %s", what, name)
			}
		}
		return got
	}

	for _, p := range props {
		if p.NS != nsPDFAExtension || p.Name != "schemas" {
			continue
		}
		if p.Value.Kind != xmpBag {
			report("pdfaExtension:schemas must be a Bag, got %s", p.Value.Kind)
			continue
		}
		for _, schema := range p.Value.Items {
			if schema.Kind != xmpStruct {
				report("pdfaExtension:schemas entries must be structures, got %s", schema.Kind)
				continue
			}
			fields := requireFields("schema description", schema, nsPDFASchema,
				[]string{"schema", "namespaceURI", "prefix"}, []string{"property", "valueType"})

			if propSeq, ok := fields["property"]; ok {
				if propSeq.Kind != xmpSeq {
					report("pdfaSchema:property must be a Seq, got %s", propSeq.Kind)
				} else {
					for _, prop := range propSeq.Items {
						if prop.Kind != xmpStruct {
							report("pdfaSchema:property entries must be structures, got %s", prop.Kind)
							continue
						}
						got := requireFields("property definition", prop, nsPDFAProperty,
							[]string{"name", "valueType", "category", "description"}, nil)
						if cat, ok := got["category"]; ok && cat.Text != "internal" && cat.Text != "external" {
							report("pdfaProperty:category must be internal or external, got %q", cat.Text)
						}
					}
				}
			}

			if vtSeq, ok := fields["valueType"]; ok {
				if vtSeq.Kind != xmpSeq {
					report("pdfaSchema:valueType must be a Seq, got %s", vtSeq.Kind)
					continue
				}
				// First pass: collect the custom value types this schema
				// declares, so a field may reference one declared later.
				declaredTypes := map[string]bool{}
				for _, vt := range vtSeq.Items {
					if vt.Kind != xmpStruct {
						continue
					}
					for _, f := range vt.Fields {
						if f.NS == nsPDFAType && f.Name == "type" {
							declaredTypes[f.Value.Text] = true
						}
					}
				}
				for _, vt := range vtSeq.Items {
					if vt.Kind != xmpStruct {
						report("pdfaSchema:valueType entries must be structures, got %s", vt.Kind)
						continue
					}
					vtFields := requireFields("value type definition", vt, nsPDFAType,
						[]string{"type", "namespaceURI", "prefix", "description"}, []string{"field"})
					if fieldSeq, ok := vtFields["field"]; ok {
						if fieldSeq.Kind != xmpSeq {
							report("pdfaType:field must be a Seq, got %s", fieldSeq.Kind)
							continue
						}
						for _, fld := range fieldSeq.Items {
							if fld.Kind != xmpStruct {
								report("pdfaType:field entries must be structures, got %s", fld.Kind)
								continue
							}
							fFields := requireFields("field definition", fld, nsPDFAField, []string{"name", "valueType", "description"}, nil)
							// A field's value type must be a standard XMP type
							// or a custom type this schema declares; otherwise
							// the referenced type has no definition (ISO 19005-1
							// 6.7.8; Isartor 6.7.8-t02-fail-j/k).
							if fv, ok := fFields["valueType"]; ok {
								vtName := fv.Text
								if vtName != "" && !standardXMPValueTypes[vtName] && !declaredTypes[vtName] {
									report("field value type %q is neither a standard type nor declared by the extension schema", vtName)
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

// extensionTypeFields collects, per extension-declared custom value type
// name, the set of structure field names its definition declares.
func extensionTypeFields(props []xmpProperty) map[string]map[string]bool {
	types := make(map[string]map[string]bool)
	for _, p := range props {
		if p.NS != nsPDFAExtension || p.Name != "schemas" {
			continue
		}
		for _, schema := range p.Value.Items {
			for _, f := range schema.Fields {
				if f.NS != nsPDFASchema || f.Name != "valueType" {
					continue
				}
				for _, vt := range f.Value.Items {
					var typeName string
					fieldNames := make(map[string]bool)
					for _, tf := range vt.Fields {
						switch {
						case tf.NS == nsPDFAType && tf.Name == "type":
							typeName = tf.Value.Text
						case tf.NS == nsPDFAType && tf.Name == "field":
							for _, fld := range tf.Value.Items {
								for _, ff := range fld.Fields {
									if ff.NS == nsPDFAField && ff.Name == "name" {
										fieldNames[ff.Value.Text] = true
									}
								}
							}
						}
					}
					if typeName != "" {
						types[typeName] = fieldNames
					}
				}
			}
		}
	}
	return types
}

// checkXMPWellFormed validates the XMP packet wrapper (ISO 19005-2 6.6.2.1,
// -4 6.7.2.1): the xpacket processing instruction must not carry a bytes or
// encoding attribute, the packet must be well-formed XML, and (PDF/A-4) it
// must be encoded as UTF-8.
func checkXMPWellFormed(doc *Document, level PDFALevel) []ValidationError {
	// The rule numbers differ by part, but the requirements — no bytes/encoding
	// attribute on the xpacket header, and a well-formed XMP packet — apply from
	// PDF/A-1 onward (ISO 19005-1 6.7.5 / 6.7.9). PDF/A-1 was previously skipped
	// entirely, missing both.
	attrRule, wfRule := "6.6.2.1", "6.6.2.1"
	switch level {
	case PDFA1b:
		attrRule, wfRule = "6.7.5", "6.7.9"
	case PDFA4:
		attrRule, wfRule = "6.7.2.1", "6.7.2.1"
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*Stream)
	if !ok {
		return nil
	}
	raw, err := decodeStreamData(stream)
	if err != nil {
		raw = stream.Data
	}

	var errs []ValidationError

	// The xpacket header processing instruction.
	if hdr := extractXPacketHeader(raw); hdr != "" {
		if xpacketHasAttr(hdr, "bytes") {
			errs = append(errs, ValidationError{Rule: attrRule, Level: level, Message: "the XMP packet header must not contain a bytes attribute"})
		}
		if xpacketHasAttr(hdr, "encoding") {
			errs = append(errs, ValidationError{Rule: attrRule, Level: level, Message: "the XMP packet header must not contain an encoding attribute"})
		}
	}

	if level == PDFA4 && !xmpIsUTF8(raw) {
		errs = append(errs, ValidationError{Rule: attrRule, Level: level, Message: "the XMP packet is not encoded as UTF-8"})
	}

	xmp := decodeXMPToUTF8(raw)
	if xmp != "" {
		// Stream the packet rather than building a node tree: well-formedness and
		// the presence of a properly namespaced rdf:RDF element are all that is
		// needed here, and streaming stays linear on an adversarially large
		// packet that would make tree-building blow up (see xmpPropertyMaxBytes).
		wellFormed, hasRDF := xmpWellFormed([]byte(xmp))
		if !wellFormed {
			errs = append(errs, ValidationError{Rule: wfRule, Level: level, Message: "the XMP packet is not well-formed XML"})
		} else if !hasRDF {
			// The packet is well-formed XML but carries no properly namespaced
			// rdf:RDF element — e.g. the RDF namespace prefix is undeclared.
			// encoding/xml resolves a declared prefix to its URI, so an
			// undeclared <RDF:RDF> yields the raw prefix "RDF" as the namespace
			// rather than the RDF URI (Isartor 6.7.2-t02-fail-a).
			errs = append(errs, ValidationError{Rule: wfRule, Level: level, Message: "the XMP packet has no properly namespaced rdf:RDF element"})
		}
	}
	return errs
}

// extractXPacketHeader returns the text of the leading "<?xpacket ... ?>"
// processing instruction, or "".
func extractXPacketHeader(raw []byte) string {
	s := decodeXMPToUTF8(raw)
	i := strings.Index(s, "<?xpacket")
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], "?>")
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

// xpacketHasAttr reports whether the xpacket header carries the named
// attribute.
func xpacketHasAttr(hdr, attr string) bool {
	return strings.Contains(hdr, " "+attr+"=") || strings.Contains(hdr, " "+attr+" =")
}

// xmpIsUTF8 reports whether the raw metadata bytes are UTF-8 (no UTF-16/32
// BOM and valid UTF-8).
func xmpIsUTF8(raw []byte) bool {
	if len(raw) >= 2 && (raw[0] == 0xFE && raw[1] == 0xFF || raw[0] == 0xFF && raw[1] == 0xFE) {
		return false // UTF-16 (BOM)
	}
	if len(raw) >= 4 && raw[0] == 0 && raw[1] == 0 {
		return false // UTF-32
	}
	// A UTF-8 XMP packet begins with printable ASCII ("<?xpacket" or
	// "<x:xmpmeta"), so a NUL as the second byte can only mean a BOM-less
	// UTF-16 encoding — which is not UTF-8 and must be flagged (audit C24).
	if len(raw) >= 2 && (raw[0] != 0 && raw[1] == 0 || raw[0] == 0 && raw[1] != 0) {
		return false // BOM-less UTF-16 (LE or BE)
	}
	// Strip a UTF-8 BOM if present, then validate.
	b := raw
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return utf8Valid(b)
}
