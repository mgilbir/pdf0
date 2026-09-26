package pdfx

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// What each PDF/X level requires, one row per level (audit 2026-09-22 C82,
// C85). The rules that differ between the parts of ISO 15930 are read from
// here and nowhere else, so a level cannot quietly inherit another's rule —
// which is how PDF/X-1a came to be identified by the PDF/X-4 reading, to
// demand an embedded profile PDF/X-1a does not, and to take RGB.
//
// Sources. ISO 15930 is not in this repository's spec/ directory. The rows
// are from the text of the parts as the author knows it, not checked against
// the documents here; each field says which part it rests on, and where the
// knowledge is uncertain the rule is written to withhold a finding rather than
// risk a false one. The PDF/X-4 row is also calibrated against the Cal Poly
// PDF/VT-1 suite (conforming PDF/X-4 files), which it passes without a
// finding. There is no corpus for the other levels.
type levelRules struct {
	// ids are the identifications that name the level: GTS_PDFXVersion,
	// and GTS_PDFXConformance where the part uses it. An empty conformance
	// matches any.
	ids []identification
	// xmpIdentification: the identification shall be in the XMP metadata
	// (pdfxid:GTS_PDFXVersion) — PDF/X-4 (15930-7) and PDF/X-6 (15930-9).
	// The earlier parts identify in the Info dictionary, and XMP is read
	// too.
	xmpIdentification bool
	// profile is when the GTS_PDFX output intent needs an embedded ICC
	// profile (/DestOutputProfile).
	profile profileRule
	// trappedInXMP: /Trapped is read from XMP pdf:Trapped rather than the
	// Info dictionary — PDF/X-6, whose PDF 2.0 base deprecates Info.
	trappedInXMP bool
	// maxMinor and pdf2 are the PDF version the level is defined for:
	// PDF 1.3/1.4 for X-1a and X-3 (2001-2003 parts), PDF 1.6 for X-4,
	// PDF 2.0 for X-6.
	maxMinor int
	pdf2     bool
	// noTransparency: the level predates the transparency imaging model
	// (X-1a, X-3).
	noTransparency bool
	// cmykOnly: colour is CMYK, gray or spot only (X-1a; 15930-1/-4). pdf0
	// enforces the device half — DeviceRGB used is a finding whatever the
	// output intent — and does not yet reject CIE-based spaces.
	cmykOnly bool
}

// identification is one accepted (GTS_PDFXVersion, GTS_PDFXConformance) pair.
type identification struct{ version, conformance string }

type profileRule int

const (
	// profileAlways: an embedded ICC profile is required (X-4, X-6).
	profileAlways profileRule = iota
	// profileOrReference: embedded, or referenced by /DestOutputProfileRef
	// (X-4p).
	profileOrReference
	// profileIfCustom: required only for a printing condition no registry
	// characterises, which PDF/X-1a and PDF/X-3 write as the
	// OutputConditionIdentifier "Custom"; a registered condition (FOGRA39,
	// CGATS TR 001, ...) is identified by name alone.
	profileIfCustom
)

var levels = map[Level]levelRules{
	// ISO 15930-1:2001 names PDF/X-1a:2001 with GTS_PDFXVersion
	// "PDF/X-1:2001" and GTS_PDFXConformance "PDF/X-1a:2001"; ISO
	// 15930-4:2003 names PDF/X-1a:2003 with GTS_PDFXVersion "PDF/X-1a:2003".
	PDFX1a: {
		ids:            []identification{{"PDF/X-1:2001", "PDF/X-1a:2001"}, {"PDF/X-1a:2003", ""}},
		profile:        profileIfCustom,
		maxMinor:       4,
		noTransparency: true,
		cmykOnly:       true,
	},
	// ISO 15930-3:2002 and 15930-6:2003.
	PDFX3: {
		ids:            []identification{{"PDF/X-3:2002", ""}, {"PDF/X-3:2003", ""}},
		profile:        profileIfCustom,
		maxMinor:       4,
		noTransparency: true,
	},
	// ISO 15930-7.
	PDFX4: {
		ids:               []identification{{"PDF/X-4", ""}},
		xmpIdentification: true,
		profile:           profileAlways,
		maxMinor:          6,
	},
	PDFX4p: {
		ids:               []identification{{"PDF/X-4p", ""}},
		xmpIdentification: true,
		profile:           profileOrReference,
		maxMinor:          6,
	},
	// ISO 15930-9:2020 (PDF/X-6; its 6p and 6n variants are not levels here).
	PDFX6: {
		ids:               []identification{{"PDF/X-6", ""}},
		xmpIdentification: true,
		profile:           profileAlways,
		trappedInXMP:      true,
		pdf2:              true,
	},
}

// rules returns the level's row, and whether the level names one.
func (l Level) rules() (levelRules, bool) {
	r, ok := levels[l]
	return r, ok
}

// valid reports whether l names a PDF/X level. Anything else — an arbitrary
// integer converted to Level — used to be validated as PDF/X-4 by every
// default branch (audit 2026-09-22 C139, C85).
func (l Level) valid() bool {
	_, ok := levels[l]
	return ok
}

func (r levelRules) accepts(version, conformance string) bool {
	for _, id := range r.ids {
		if version == id.version && (id.conformance == "" || conformance == id.conformance) {
			return true
		}
	}
	return false
}

func (r levelRules) describeIDs() string {
	var parts []string
	for _, id := range r.ids {
		s := fmt.Sprintf("GTS_PDFXVersion %q", id.version)
		if id.conformance != "" {
			s += fmt.Sprintf(" with GTS_PDFXConformance %q", id.conformance)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", or ")
}

// pdfxCheckIdentification verifies the file identifies as the requested level
// in the place that level puts it (C82, C85).
//
// The XMP identification is read through the XMP model, by namespace URI, so a
// comment, an attribute-form property or another prefix cannot hide or forge it
// (audit C141). A property named GTS_PDFXVersion in no namespace at all is also
// accepted, as the substring reader that model replaced accepted it.
func pdfxCheckIdentification(doc core.View, level Level, add func(rule, msg string, obj int)) {
	r, _ := level.rules()

	var xmpVersion, xmpConformance string
	unread := false
	switch packet, status := doc.DocumentXMPPacket(); status {
	case core.XMPParsed:
		get := func(name string) string {
			if v, ok := packet.Text(xmp.NSPDFXID, name); ok {
				return v
			}
			v, _ := packet.Text("", name)
			return v
		}
		xmpVersion, xmpConformance = get("GTS_PDFXVersion"), get("GTS_PDFXConformance")
	case core.XMPLimit:
		unread = true
	}
	var infoVersion, infoConformance string
	if info := doc.ResolveDict(doc.Trailer.Get("Info")); info != nil {
		// Text strings: decoded, so a UTF-16 identifier reads as itself. A
		// string still encrypted cannot be read, so the claim is unknown.
		text := func(key object.Name) string {
			switch s, r := doc.StringValue(info.Get(key)); r {
			case core.ReasonOK:
				return strings.TrimSpace(core.DecodePDFTextString(s.Value))
			case core.ReasonLocked:
				unread = true
			}
			return ""
		}
		infoVersion, infoConformance = text("GTS_PDFXVersion"), text("GTS_PDFXConformance")
	}

	version, conformance := xmpVersion, xmpConformance
	if !r.xmpIdentification && infoVersion != "" {
		// The Info dictionary is where PDF/X-1a and PDF/X-3 identify.
		version, conformance = infoVersion, infoConformance
	}
	if version == "" {
		if unread {
			// The XMP packet was not read (a limit, already on the run) or
			// the Info string is ciphertext, so whether the file is
			// identified is unknown.
			return
		}
		switch {
		case r.xmpIdentification && infoVersion != "":
			add("identification", fmt.Sprintf("%s is identified in the XMP metadata (pdfxid:GTS_PDFXVersion); the Info dictionary's GTS_PDFXVersion %q alone does not identify it", level, infoVersion), 0)
		case r.xmpIdentification:
			add("identification", "file is not identified as PDF/X (no XMP pdfxid:GTS_PDFXVersion)", 0)
		default:
			add("identification", "file is not identified as PDF/X (no Info /GTS_PDFXVersion or pdfxid:GTS_PDFXVersion)", 0)
		}
		return
	}
	if !r.accepts(version, conformance) {
		claimed := fmt.Sprintf("GTS_PDFXVersion %q", version)
		if conformance != "" {
			claimed += fmt.Sprintf(" with GTS_PDFXConformance %q", conformance)
		}
		add("identification", fmt.Sprintf("%s does not identify %s, which is %s", claimed, level, r.describeIDs()), 0)
	}
}

// pdfxCheckTrapped verifies the /Trapped flag is present and definite (True
// or False, not Unknown or absent): in the Info dictionary for the PDF 1.x
// levels (15930-1/-3/-4/-6/-7), in XMP pdf:Trapped for PDF/X-6, whose PDF 2.0
// base deprecates the Info dictionary.
func pdfxCheckTrapped(doc core.View, level Level, add func(rule, msg string, obj int)) {
	if r, _ := level.rules(); r.trappedInXMP {
		packet, status := doc.DocumentXMPPacket()
		switch status {
		case core.XMPLimit:
			return // not read; the trip is on the run
		case core.XMPParsed:
			switch t, _ := packet.Text(xmp.NSPDF, "Trapped"); t {
			case "True", "False":
				return
			}
		}
		add("trapped", fmt.Sprintf("%s requires XMP pdf:Trapped to be True or False, not Unknown or absent", level), 0)
		return
	}
	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	if info == nil {
		add("trapped", "Info dictionary with a definite /Trapped value is required", 0)
		return
	}
	switch t, _ := doc.ResolveName(info.Get("Trapped")); t {
	case "True", "False":
		// definite, as required
	default:
		add("trapped", "Info /Trapped shall be True or False, not Unknown or absent", 0)
	}
}

// pdfxCheckOutputIntent verifies the PDF/X output intent: a GTS_PDFX intent
// with a non-empty /OutputConditionIdentifier, whose ICC destination profile
// is embedded when the level requires it (see profileRule), and all GTS_PDFX
// intents referencing one profile.
func pdfxCheckOutputIntent(doc core.View, level Level, add func(rule, msg string, obj int)) {
	r, _ := level.rules()
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	arr, ok := doc.Resolve(cat.Get("OutputIntents")).(object.Array)
	if !ok || len(arr) == 0 {
		add("output-intent", "a PDF/X file requires a catalog /OutputIntents array with a GTS_PDFX intent", 0)
		return
	}
	var profiles []object.Object
	found := false
	for _, e := range arr {
		oi := doc.ResolveDict(e)
		if oi == nil {
			continue
		}
		if s, _ := doc.ResolveName(oi.Get("S")); s != "GTS_PDFX" {
			continue
		}
		found = true
		if !doc.NonEmptyStringOrLocked(oi.Get("OutputConditionIdentifier")) {
			add("output-intent", "GTS_PDFX output intent lacks a non-empty /OutputConditionIdentifier", object.RefNum(e))
		}
		prof := oi.Get("DestOutputProfile")
		if _, ok := doc.Resolve(prof).(*object.Stream); ok {
			profiles = append(profiles, prof)
			continue
		}
		switch r.profile {
		case profileAlways:
			add("output-intent", fmt.Sprintf("%s requires an embedded ICC /DestOutputProfile in the GTS_PDFX output intent", level), object.RefNum(e))
		case profileOrReference:
			if oi.Get("DestOutputProfileRef") == nil {
				add("output-intent", fmt.Sprintf("%s output intent has neither an embedded /DestOutputProfile nor a /DestOutputProfileRef", level), object.RefNum(e))
			}
		case profileIfCustom:
			// A Custom condition names no registry entry. An identifier that
			// cannot be read (ciphertext) is not known to be Custom.
			if oci, r := doc.StringValue(oi.Get("OutputConditionIdentifier")); r == core.ReasonOK &&
				strings.TrimSpace(core.DecodePDFTextString(oci.Value)) == "Custom" {
				add("output-intent", fmt.Sprintf("%s requires an embedded ICC /DestOutputProfile for a Custom (unregistered) printing condition", level), object.RefNum(e))
			}
		}
	}
	if !found {
		add("output-intent", "no output intent with /S /GTS_PDFX is present", 0)
	}
	// All GTS_PDFX intents shall reference the same profile.
	for i := 1; i < len(profiles); i++ {
		if object.RefNum(profiles[i]) != object.RefNum(profiles[0]) {
			add("output-intent", "multiple GTS_PDFX output intents reference different destination profiles", 0)
			break
		}
	}
}
