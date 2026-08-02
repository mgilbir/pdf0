package core

import (
	"github.com/mgilbir/pdf0/object"
)

// Device colour-space usage: which of DeviceRGB, DeviceCMYK and DeviceGray a
// page actually draws with, following the executed-content model — a resource
// counts only when the content invokes it. PDF/A needs this to decide whether
// an OutputIntent is required at all, and PDF/X cross-checks its own memoised
// scanner against it, so it lives here rather than in either validator.

// PageDeviceColourUse checks if a page uses device color spaces.
// It scans Image XObjects, Form XObjects, and content streams.
func PageDeviceColourUse(doc View, page *object.Dictionary) (usesRGB, usesCMYK, usesGray bool) {
	seen := make(map[*object.Dictionary]bool)
	scanResourcesForDeviceCS(doc, page, seen, &usesRGB, &usesCMYK, &usesGray)

	// Also scan annotation appearance streams
	annotsRef := page.Get("Annots")
	if annotsRef != nil {
		annotsObj := doc.Resolve(annotsRef)
		if annotsArr, ok := annotsObj.(object.Array); ok {
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
				for _, apKey := range []object.Name{"N", "R", "D"} {
					apEntry := apDict.Get(apKey)
					if apEntry == nil {
						continue
					}
					apObj := doc.Resolve(apEntry)
					switch v := apObj.(type) {
					case *object.Stream:
						scanContentStreamForDeviceCS(doc, v, seen, &usesRGB, &usesCMYK, &usesGray)
					case *object.Dictionary:
						for _, stateVal := range v.Values {
							if s, ok := doc.Resolve(stateVal).(*object.Stream); ok {
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
			CheckCSForDevice(doc, groupDict.Get("CS"), &usesRGB, &usesCMYK, &usesGray)
		}
	}

	return
}

// scanContentStreamForDeviceCS scans a content-bearing stream — a form
// XObject, tiling pattern, or appearance stream. Unlike pages, whose content
// lives behind /Contents, these carry their operators in the stream body
// itself (ISO 32000-1, 7.8.2), which the resources walk alone never read: a
// form with '1 0 0 rg' and no resources scanned as clean.
func scanContentStreamForDeviceCS(doc View, stream *object.Stream, seen map[*object.Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[&stream.Dict] {
		return
	}
	scanContainerForDeviceCS(doc, &stream.Dict, doc.Content(stream), stream, seen, usesRGB, usesCMYK, usesGray)
}

func scanResourcesForDeviceCS(doc View, container *object.Dictionary, seen map[*object.Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
	if seen[container] {
		return
	}
	var data []byte
	var key *object.Stream
	if contentsRef := container.Get("Contents"); contentsRef != nil {
		data, key = doc.ContentBytesAndKey(contentsRef)
	}
	scanContainerForDeviceCS(doc, container, data, key, seen, usesRGB, usesCMYK, usesGray)
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

// scanContainerForDeviceCS scans one content container — a page (content
// behind /Contents), a form XObject, tiling pattern, or appearance stream
// (content in the stream body, passed as data) — for device colour usage.
//
// Only EXECUTED resources count: a form XObject or pattern that is listed in
// the resource dictionary but never invoked by a Do/scn/sh operator does not
// contribute (the corpus passes a DeviceCMYK form that no content stream
// draws), so the resource walks below are gated on the names the content
// actually uses.
func scanContainerForDeviceCS(doc View, container *object.Dictionary, data []byte, key *object.Stream, seen map[*object.Dictionary]bool, usesRGB, usesCMYK, usesGray *bool) {
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
		dR, dC, dG := DefaultColorSpaces(doc, container)
		*usesRGB = *usesRGB || (localRGB && !dR)
		*usesCMYK = *usesCMYK || (localCMYK && !dC)
		*usesGray = *usesGray || (localGray && !dG)
	}()

	if data != nil {
		r, c, g := ScanStreamForDeviceOps(doc.Cancel, data)
		localRGB = localRGB || r
		localCMYK = localCMYK || c
		localGray = localGray || g
	}
	used := doc.ContentUsedNamesCached(data, key)

	res := doc.Resources(container)
	if res == nil {
		return
	}

	// Check ColorSpace dict for device CS references (including Indexed bases)
	csRef := res.Get("ColorSpace")
	if csRef != nil {
		csDict := doc.ResolveDict(csRef)
		if csDict != nil {
			for _, val := range csDict.Values {
				CheckCSForDevice(doc, val, &localRGB, &localCMYK, &localGray)
			}
		}
	}

	// Check XObject resources actually invoked with Do
	xobjRef := res.Get("XObject")
	if xobjRef != nil {
		xobjDict := doc.ResolveDict(xobjRef)
		if xobjDict != nil {
			for i, key := range xobjDict.Keys {
				if !used.XObjects[string(key)] {
					continue
				}
				resolved := doc.Resolve(xobjDict.Values[i])
				stream, ok := resolved.(*object.Stream)
				if !ok {
					continue
				}
				subtype, _ := stream.Dict.Get("Subtype").(object.Name)
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
							CheckCSForDevice(doc, groupDict.Get("CS"), &formRGB, &formCMYK, &formGray)
							// A calibrated Group /CS covers device CS within
							// the Form only when the group is ISOLATED: a
							// non-isolated group composites against the
							// backdrop, and the corpus fails DeviceRGB in a
							// non-isolated CalRGB group.
							isolated, _ := doc.Resolve(groupDict.Get("I")).(object.Boolean)
							if csObj := groupDict.Get("CS"); csObj != nil && bool(isolated) {
								gRGB, gCMYK, gGray := ClassifyCalibratedCS(doc, csObj)
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
					CheckCSForDevice(doc, stream.Dict.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
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
				if !used.Shadings[string(key)] {
					continue
				}
				val := shadingDict.Values[i]
				sd := doc.ResolveDict(val)
				if sd == nil {
					// Could be a stream (type 4-7 shadings)
					if s, ok := doc.Resolve(val).(*object.Stream); ok {
						CheckCSForDevice(doc, s.Dict.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
					}
					continue
				}
				CheckCSForDevice(doc, sd.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
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
				if !used.Patterns[string(key)] {
					continue
				}
				obj := doc.Resolve(patDict.Values[i])
				switch v := obj.(type) {
				case *object.Stream:
					// Tiling pattern: its body is a content stream.
					scanContentStreamForDeviceCS(doc, v, seen, usesRGB, usesCMYK, usesGray)
				case *object.Dictionary:
					// Shading pattern.
					if sd := doc.ResolveDict(v.Get("Shading")); sd != nil {
						CheckCSForDevice(doc, sd.Get("ColorSpace"), &localRGB, &localCMYK, &localGray)
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
				subtype, _ := fd.Get("Subtype").(object.Name)
				if subtype == "Type3" {
					// Recurse into Type3 font resources
					scanResourcesForDeviceCS(doc, fd, seen, usesRGB, usesCMYK, usesGray)
					// Also scan CharProc streams
					cpRef := fd.Get("CharProcs")
					cpDict := doc.ResolveDict(cpRef)
					if cpDict != nil {
						for _, cpVal := range cpDict.Values {
							cpObj := doc.Resolve(cpVal)
							if cpStream, ok := cpObj.(*object.Stream); ok {
								data := doc.Content(cpStream)
								if data != nil {
									r, c, g := ScanStreamForDeviceOps(doc.Cancel, data)
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

// The maximum decoded content stream size we'll scan defaults to
// defaultMaxContentStreamBytes; a caller can change it with
// WithMaxContentStreamBytes. Larger streams are skipped to bound memory on
// hostile input. The previous 1 MB cap (and Flate-only, no-filter-array
// decoding) silently hid ordinary content from every scanner — an oversize or
// [/FlateDecode]-wrapped stream full of DeviceRGB validated clean. A stream
// skipped for this reason is reported (limitContentStream): every
// content-driven rule then sees nothing from it, which is the failure the old
// cap caused silently.
//
// The aggregate size of decoded content that one validation run will
// materialize defaults to defaultMaxDecodedContentBytes
// (WithMaxDecodedContentBytes). The per-stream cap stops a single stream from
// exploding, but a small file can carry many content streams that each
// decompress near that cap (a flate bomb): 100 pages whose contents each inflate
// to ~60 MB is a ~12 MB file that would otherwise decode and tokenize ~6 GB of
// content, driving validation past 9 GB of memory. Once this budget is reached,
// further content streams are treated as undecodable (nil), so they are neither
// decoded nor tokenized and the work stays bounded. object.Real documents decode far
// less than this, so the budget never affects their validation; it only
// truncates pathologically amplified input — the heaviest document measured
// across the veraPDF corpus and a Common Crawl sample needs 218 MB. Exhausting
// it is reported too (limitContentTotal), because from there on "the content
// does not do X" is no longer something this run can say.

// decodeContentStream decodes a stream for content scanning through the full
// filter pipeline (filter arrays, ASCIIHex, predictors). Results are
// memoized per validation run: several checks re-decode the same page
// contents. Returns nil if the stream cannot be decoded, or if the run's
// aggregate decoded-content budget is exhausted.
