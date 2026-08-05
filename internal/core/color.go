package core

import (
	"bytes"
	"compress/zlib"
	"io"
	"math"
	"strings"

	"github.com/mgilbir/pdf0/object"
)

// Colour-space and transparency queries the PDF/A and PDF/X engines both make:
// which device colour a page reaches for, what a group or Default* entry covers,
// and whether a page uses transparency at all.

// ExtractXMPValue extracts a simple value from XMP for a given key.
// Handles both <key>value</key> and key="value" attribute forms.
func ExtractXMPValue(xmp, key string) string {
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

// PageUsesTransparency checks if a page's resources reference transparency features.
// It checks ExtGState entries for CA/ca != 1.0, BM != Normal/Compatible, and SMask != None,
// and also recurses into Form XObjects and Type3 font resources.
func PageUsesTransparency(doc View, page *object.Dictionary) bool {
	// A page with a transparency Group is itself a transparency feature
	if groupRef := page.Get("Group"); groupRef != nil {
		groupDict := doc.ResolveDict(groupRef)
		if groupDict != nil {
			s, _ := groupDict.Get("S").(object.Name)
			if s == "Transparency" {
				return true
			}
		}
	}

	seen := make(map[*object.Dictionary]bool)
	if resourcesUseTransparency(doc, page, seen) {
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
		// Check /BM on annotation itself
		if bm := annotDict.Get("BM"); bm != nil {
			if n, ok := bm.(object.Name); ok && n != "Normal" && n != "Compatible" {
				return true
			}
		}
		// Check /CA or /ca on annotation
		for _, key := range []object.Name{"CA", "ca"} {
			if v := annotDict.Get(key); v != nil {
				fval := 1.0
				switch tv := v.(type) {
				case object.Real:
					fval = float64(tv)
				case object.Integer:
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
		for _, apKey := range []object.Name{"N", "R", "D"} {
			apEntry := apDict.Get(apKey)
			if apEntry == nil {
				continue
			}
			// Could be a stream directly or a dict of states
			apObj := doc.Resolve(apEntry)
			switch v := apObj.(type) {
			case *object.Stream:
				if resourcesUseTransparency(doc, &v.Dict, seen) {
					return true
				}
				// Check if the appearance stream has its own transparency group
				if v.Dict.Get("Group") != nil {
					groupDict := doc.ResolveDict(v.Dict.Get("Group"))
					if groupDict != nil {
						s, _ := groupDict.Get("S").(object.Name)
						if s == "Transparency" {
							return true
						}
					}
				}
			case *object.Dictionary:
				// Dict of appearance states (e.g., /N << /Yes 12 0 R /Off 13 0 R >>)
				for _, stateVal := range v.Values {
					stateObj := doc.Resolve(stateVal)
					if stateStream, ok := stateObj.(*object.Stream); ok {
						if resourcesUseTransparency(doc, &stateStream.Dict, seen) {
							return true
						}
						if stateStream.Dict.Get("Group") != nil {
							groupDict := doc.ResolveDict(stateStream.Dict.Get("Group"))
							if groupDict != nil {
								s, _ := groupDict.Get("S").(object.Name)
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

func resourcesUseTransparency(doc View, container *object.Dictionary, seen map[*object.Dictionary]bool) bool {
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
				stream, ok := obj.(*object.Stream)
				if !ok {
					continue
				}
				subtype, _ := stream.Dict.Get("Subtype").(object.Name)
				if subtype == "Form" {
					// If the Form XObject has its own transparency Group,
					// it manages its own compositing - don't propagate to page level.
					if stream.Dict.Get("Group") != nil {
						groupDict := doc.ResolveDict(stream.Dict.Get("Group"))
						if groupDict != nil {
							s, _ := groupDict.Get("S").(object.Name)
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
				subtype, _ := fd.Get("Subtype").(object.Name)
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
				stream, ok := obj.(*object.Stream)
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

func extGStateUsesTransparency(doc View, res *object.Dictionary) bool {
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
		for _, key := range []object.Name{"CA", "ca"} {
			v := gs.Get(key)
			if v != nil {
				fval := 1.0
				switch tv := v.(type) {
				case object.Real:
					fval = float64(tv)
				case object.Integer:
					fval = float64(tv)
				}
				if math.Abs(fval-1.0) > 1e-6 {
					return true
				}
			}
		}
		// Non-Normal blend modes are transparency features
		if bm := gs.Get("BM"); bm != nil {
			if n, ok := bm.(object.Name); ok && n != "Normal" && n != "Compatible" {
				return true
			}
		}
		// Check SMask for non-None values
		if smask := gs.Get("SMask"); smask != nil {
			if n, ok := smask.(object.Name); !ok || n != "None" {
				return true
			}
		}
	}
	return false
}

// ICCProfileData returns the decompressed ICC profile data from a stream.
// Returns the raw stream data if no filter or decoding fails. The decoded size
// is bounded to prevent decompression bombs; the default is
// defaultMaxICCProfileBytes and a caller can change it with
// WithMaxICCProfileBytes.
//
// The default was raised from 2 MiB to 8 MiB: the largest real profile measured
// across the veraPDF corpus and a 978-file Common Crawl sample is 1,829,093
// bytes — 87% of the old cap, i.e. one slightly fatter profile away from
// silently dropping the ICC rules for that file. Unlike the XMP packet bound,
// the cost here is linear (a profile is read once and scanned, not expanded),
// so headroom is cheap.
func ICCProfileData(stream *object.Stream, lim Limits) []byte {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		if len(stream.Data) > lim.ICCProfileBytes {
			return nil
		}
		return stream.Data
	}

	filterName, ok := filter.(object.Name)
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

	limited := io.LimitReader(r, int64(lim.ICCProfileBytes)+1)
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil
	}
	if len(decoded) > lim.ICCProfileBytes {
		return nil
	}
	return decoded
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
	csType, _ := arr[0].(object.Name)
	switch csType {
	case "ICCBased":
		profileObj := doc.Resolve(arr[1])
		if stream, ok := profileObj.(*object.Stream); ok {
			if nObj := stream.Dict.Get("N"); nObj != nil {
				if n, ok := nObj.(object.Integer); ok {
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

// CheckCSForDevice checks if a color space value is or contains a device color space.
// Handles direct names, arrays (Indexed, Separation, DeviceN, Pattern with base).
func CheckCSForDevice(doc View, csObj object.Object, usesRGB, usesCMYK, usesGray *bool) {
	checkCSForDeviceSeen(doc, csObj, usesRGB, usesCMYK, usesGray, make(map[int]bool))
}

func checkCSForDeviceSeen(doc View, csObj object.Object, usesRGB, usesCMYK, usesGray *bool, seen map[int]bool) {
	if csObj == nil {
		return
	}
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
		csType, _ := arr[0].(object.Name)
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

// ScanStreamForDeviceOps scans decoded content stream bytes for device color operators.
// Uses a simple tokenizer that handles inline images (BI/ID/EI) to avoid
// scanning binary image data.
//
// The scan stops when cancel fires, checked every cancelScanBytes of input
// like the other content scanners; see cancel.go.
func ScanStreamForDeviceOps(cancel Canceler, data []byte) (usesRGB, usesCMYK, usesGray bool) {
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
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + CancelScanBytes
		}
		// Skip whitespace
		for i < n && IsContentWS(data[i]) {
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

		// Skip array/proc delimiters, and a stray ')' (a delimiter that would
		// otherwise stall the token scan below on untrusted content).
		if b == '[' || b == ']' || b == '{' || b == '}' || b == ')' {
			i++
			continue
		}

		// PDF names (/object.Name): remember the last one seen, so a following
		// cs/CS operator can be checked for direct device selection.
		if b == '/' {
			i++
			nameStart := i
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
			lastName = string(data[nameStart:i])
			continue
		}

		// Read a token
		start := i
		for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
			i++
		}
		// A run longer than this is binary data, not a token: no PDF operator
		// or operand keyword is anywhere near this long. Discarding it whole is
		// what matters — cutting it at the cap and letting the scan re-enter
		// mid-run turns the tail into further "tokens", and a fragment of
		// binary read as an operator is a violation the file does not commit
		// (a stray 'k'/'g' fragment reads as DeviceCMYK/DeviceGray use).
		if i-start > MaxContentTokenLen {
			continue
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
				for i < n && IsContentWS(data[i]) {
					i++
				}
				if i >= n {
					break
				}
				// Check for ID token (end of inline image dict)
				if data[i] == 'I' && i+1 < n && data[i+1] == 'D' &&
					(i+2 >= n || IsContentWS(data[i+2])) {
					i += 2
					// Skip one whitespace byte after ID
					if i < n && IsContentWS(data[i]) {
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
					for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
						i++
					}
					key := string(data[keyStart:i])
					// If key is CS or ColorSpace, check the next value
					if key == "CS" || key == "ColorSpace" {
						// Skip whitespace
						for i < n && IsContentWS(data[i]) {
							i++
						}
						// Read value - could be /object.Name or /abbreviation
						if i < n && data[i] == '/' {
							valStart := i + 1
							i++
							for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
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
						for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
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
						atBoundary := (i == 0 || IsContentWS(data[i-1]))
						endBoundary := (i+2 >= n || IsContentWS(data[i+2]) || IsContentDelim(data[i+2]))
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
			if i < n && IsContentWS(data[i]) {
				i++
			}
			// Scan for EI at word boundary
			for i < n {
				if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
					atBoundary := (i == 0 || IsContentWS(data[i-1]))
					endBoundary := (i+2 >= n || IsContentWS(data[i+2]) || IsContentDelim(data[i+2]))
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
