package core

import (
	"math"

	"github.com/mgilbir/pdf0/object"
)

// Colour-space and transparency queries the PDF/A and PDF/X engines both make:
// which device colour a page reaches for, what a group or Default* entry covers,
// and whether a page uses transparency at all.

// PageUsesTransparency checks if a page's resources reference transparency features.
// It checks ExtGState entries for CA/ca != 1.0, BM != Normal/Compatible, and SMask != None,
// and also recurses into Form XObjects and Type3 font resources.
func PageUsesTransparency(doc View, page *object.Dictionary) bool {
	// A page with a transparency Group is itself a transparency feature
	if groupRef := page.Get("Group"); groupRef != nil {
		groupDict := doc.ResolveDict(groupRef)
		if groupDict != nil {
			s, _ := doc.ResolveName(groupDict.Get("S"))
			if s == "Transparency" {
				return true
			}
		}
	}

	seen := make(map[*object.Dictionary]bool)
	if resourcesUseTransparency(doc, page, seen, 0) {
		return true
	}
	// Check annotations on this page for transparency features
	annotsRef := page.Get("Annots")
	if annotsRef == nil {
		return false
	}
	annotsObj := doc.Resolve(annotsRef)
	annotsArr, ok := annotsObj.(object.Array)
	if !ok {
		return false
	}
	for _, annotRef := range annotsArr {
		annotDict := doc.ResolveDict(annotRef)
		if annotDict == nil {
			continue
		}
		if blendOrAlphaIsTransparent(doc, annotDict) {
			return true
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
		for _, apKey := range []object.Name{"N", "R", "D"} {
			apEntry := apDict.Get(apKey)
			if apEntry == nil {
				continue
			}
			// Could be a stream directly or a dict of states
			apObj := doc.Resolve(apEntry)
			switch v := apObj.(type) {
			case *object.Stream:
				if resourcesUseTransparency(doc, &v.Dict, seen, 0) {
					return true
				}
				// Check if the appearance stream has its own transparency group
				if v.Dict.Get("Group") != nil {
					groupDict := doc.ResolveDict(v.Dict.Get("Group"))
					if groupDict != nil {
						s, _ := doc.ResolveName(groupDict.Get("S"))
						if s == "Transparency" {
							return true
						}
					}
				}
			case *object.Dictionary:
				// Dict of appearance states (e.g., /N << /Yes 12 0 R /Off 13 0 R >>)
				for stateVal := range v.Values() {
					stateObj := doc.Resolve(stateVal)
					if stateStream, ok := stateObj.(*object.Stream); ok {
						if resourcesUseTransparency(doc, &stateStream.Dict, seen, 0) {
							return true
						}
						if stateStream.Dict.Get("Group") != nil {
							groupDict := doc.ResolveDict(stateStream.Dict.Get("Group"))
							if groupDict != nil {
								s, _ := doc.ResolveName(groupDict.Get("S"))
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

func resourcesUseTransparency(doc View, container *object.Dictionary, seen map[*object.Dictionary]bool, depth int) bool {
	if !doc.Descend(depth) {
		return false
	}
	doc.Charge(1)
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
			for val := range xobjDict.Values() {
				obj := doc.Resolve(val)
				stream, ok := obj.(*object.Stream)
				if !ok {
					continue
				}
				subtype, _ := doc.ResolveName(stream.Dict.Get("Subtype"))
				if subtype == "Form" {
					// If the Form XObject has its own transparency Group,
					// it manages its own compositing - don't propagate to page level.
					if stream.Dict.Get("Group") != nil {
						groupDict := doc.ResolveDict(stream.Dict.Get("Group"))
						if groupDict != nil {
							s, _ := doc.ResolveName(groupDict.Get("S"))
							if s == "Transparency" {
								continue // self-contained transparency group
							}
						}
					}
					// Recurse into Form XObject Resources
					if resourcesUseTransparency(doc, &stream.Dict, seen, depth+1) {
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
			for val := range fontDict.Values() {
				fd := doc.ResolveDict(val)
				if fd == nil {
					continue
				}
				subtype, _ := doc.ResolveName(fd.Get("Subtype"))
				if subtype == "Type3" {
					if resourcesUseTransparency(doc, fd, seen, depth+1) {
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
			for val := range patDict.Values() {
				obj := doc.Resolve(val)
				stream, ok := obj.(*object.Stream)
				if !ok {
					continue
				}
				// Tiling patterns (PatternType 1) have their own Resources
				if resourcesUseTransparency(doc, &stream.Dict, seen, depth+1) {
					return true
				}
			}
		}
	}

	return false
}

func extGStateUsesTransparency(doc View, res *object.Dictionary) bool {
	gsRef := res.Get("ExtGState")
	if gsRef == nil {
		return false
	}
	gsDict := doc.ResolveDict(gsRef)
	if gsDict == nil {
		return false
	}
	for val := range gsDict.Values() {
		gs := doc.ResolveDict(val)
		if gs == nil {
			continue
		}
		if blendOrAlphaIsTransparent(doc, gs) {
			return true
		}
		// A soft mask other than /None is a transparency feature.
		if smask := doc.Resolve(gs.Get("SMask")); smask != nil {
			if n, ok := smask.(object.Name); !ok || n != "None" {
				return true
			}
		}
	}
	return false
}

// blendOrAlphaIsTransparent reports whether a graphics-state or annotation
// dictionary selects a blend mode other than Normal/Compatible, or a constant
// alpha (/CA or /ca) other than 1. Each value is resolved first: any of them
// may be written as an indirect reference.
func blendOrAlphaIsTransparent(doc View, d *object.Dictionary) bool {
	if n, ok := doc.ResolveName(d.Get("BM")); ok && n != "Normal" && n != "Compatible" {
		return true
	}
	for _, key := range []object.Name{"CA", "ca"} {
		fval := 1.0
		switch tv := doc.Resolve(d.Get(key)).(type) {
		case object.Real:
			fval = float64(tv)
		case object.Integer:
			fval = float64(tv)
		}
		if math.Abs(fval-1.0) > 1e-6 {
			return true
		}
	}
	return false
}

// ICCProfileData returns the decoded ICC profile data of a stream, through the
// same filter chain as every other stream, with the Reason. The decoded size
// is bounded to prevent decompression bombs: the default is
// DefaultMaxICCProfileBytes and a caller can change it with
// WithMaxICCProfileBytes. A profile over the bound is ReasonLimit, and the trip
// is recorded here — it used to return nil and record nothing, so a caller's
// lowered bound silently removed every ICC rule (audit 2026-09-22 C109).
//
// The default was raised from 2 MiB to 8 MiB: the largest real profile measured
// across the veraPDF corpus and a 978-file Common Crawl sample is 1,829,093
// bytes — 87% of the old cap, i.e. one slightly fatter profile away from
// silently dropping the ICC rules for that file. Unlike the XMP packet bound,
// the cost here is linear (a profile is read once and scanned, not expanded),
// so headroom is cheap.
func (v View) ICCProfileData(stream *object.Stream) ([]byte, Reason) {
	limit := v.Limits.ICCProfileBytes
	if d := v.Limits.DecodedStreamBytes; d < limit {
		// The per-stream decode cap applies to a profile as to any stream; the
		// profile bound only ever lowers it.
		return v.decodeCapped(stream, d, GuardDecodedStream, DefaultMaxDecodedStreamBytes, "decoded-stream")
	}
	return v.decodeCapped(stream, limit, GuardICCProfile, DefaultMaxICCProfileBytes, "ICC profile")
}

// DefaultColorSpaces checks if a page defines DefaultRGB, DefaultCMYK, or DefaultGray
// in its Resources/ColorSpace dictionary.
func DefaultColorSpaces(doc View, page *object.Dictionary) (hasRGB, hasCMYK, hasGray bool) {
	res := doc.Resources(page)
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
	for key := range csDict.Keys() {
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

// GroupCSCoverage checks if a page's transparency group /CS provides
// implicit color space coverage for device color spaces. An ICCBased CS
// with N=3 covers DeviceRGB, N=4 covers DeviceCMYK, N=1 covers DeviceGray.
// CalRGB covers DeviceRGB, CalGray covers DeviceGray.
func GroupCSCoverage(doc View, page *object.Dictionary) (hasRGB, hasCMYK, hasGray bool) {
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
	return ClassifyCalibratedCS(doc, csObj)
}

// ClassifyCalibratedCS determines what device color spaces a calibrated
// color space provides coverage for. Returns false for all if the CS is
// a device color space (DeviceRGB/CMYK/Gray).
func ClassifyCalibratedCS(doc View, csObj object.Object) (coversRGB, coversCMYK, coversGray bool) {
	resolved := doc.Resolve(csObj)
	// Direct device CS names don't provide coverage
	if _, ok := resolved.(object.Name); ok {
		return
	}
	arr, ok := resolved.(object.Array)
	if !ok || len(arr) < 2 {
		return
	}
	csType, _ := doc.ResolveName(arr[0])
	switch csType {
	case "ICCBased":
		// /N may be indirect like any other value; reading it only when direct
		// lost the group's coverage and reported its device colour as
		// uncovered (audit 2026-09-22 C152).
		if stream, ok := doc.Resolve(arr[1]).(*object.Stream); ok {
			if n, ok := doc.ResolveInt(stream.Dict.Get("N")); ok {
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
	case "CalRGB":
		coversRGB = true
	case "CalGray":
		coversGray = true
	}
	return
}

// CheckCSForDevice checks if a color space value is or contains a device color space.
// Handles direct names, arrays (Indexed, Separation, DeviceN, Pattern with base).
func CheckCSForDevice(doc View, csObj object.Object, usesRGB, usesCMYK, usesGray *bool) {
	checkCSForDeviceSeen(doc, csObj, usesRGB, usesCMYK, usesGray, make(map[int]bool), 0)
}

func checkCSForDeviceSeen(doc View, csObj object.Object, usesRGB, usesCMYK, usesGray *bool, seen map[int]bool, depth int) {
	if csObj == nil || !doc.Descend(depth) {
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
	if n, ok := resolved.(object.Name); ok {
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
	if arr, ok := resolved.(object.Array); ok && len(arr) >= 2 {
		csType, _ := doc.ResolveName(arr[0])
		switch csType {
		case "Indexed":
			// [/Indexed base hival lookup] - check base
			if len(arr) >= 2 {
				checkCSForDeviceSeen(doc, arr[1], usesRGB, usesCMYK, usesGray, seen, depth+1)
			}
		case "Separation":
			// A device alternate needs OutputIntent coverage like direct
			// device colour: the corpus fails a Separation with a
			// DeviceCMYK alternate absent a CMYK PDF/A intent.
			if len(arr) >= 3 {
				checkCSForDeviceSeen(doc, arr[2], usesRGB, usesCMYK, usesGray, seen, depth+1)
			}
		case "DeviceN":
			if len(arr) >= 3 {
				checkCSForDeviceSeen(doc, arr[2], usesRGB, usesCMYK, usesGray, seen, depth+1)
			}
		case "Pattern":
			// [/Pattern underlyingCS] - check underlying
			if len(arr) >= 2 {
				checkCSForDeviceSeen(doc, arr[1], usesRGB, usesCMYK, usesGray, seen, depth+1)
			}
		}
	}
}
