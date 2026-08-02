package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
)

// Device-colour analysis for PDF/X, built for PDF/VT scale. The PDF/A validator
// determines per page which device colour families (DeviceRGB/CMYK/Gray) are
// used and left uncovered by a Default* colour space in scope
// (scanPageForDeviceCS). That walk rescans every shared form XObject once per
// page, which is fine for ordinary documents but quadratic on PDF/VT files that
// reuse one set of forms across hundreds of thousands of pages.
//
// The escaping device usage of a form XObject or tiling pattern — the device
// colour that survives its own Default* masking and, for forms, its transparency
// group — is a property of that stream alone, independent of the caller. So it
// can be computed once and memoised across pages. devColorScanner does exactly
// that; pdfxDeviceColorEscape reproduces scanPageForDeviceCS's result for a page
// with the shared work cached. Equivalence to scanPageForDeviceCS is asserted by
// TestDevColorScannerMatchesPDFA over the corpus, so the two cannot drift.

type devUse struct{ rgb, cmyk, gray bool }

func (u *devUse) or(o devUse) {
	u.rgb = u.rgb || o.rgb
	u.cmyk = u.cmyk || o.cmyk
	u.gray = u.gray || o.gray
}

// devColorScanner memoises the escaping device-colour usage of form XObjects,
// tiling patterns, and appearance streams so a page's device usage can be
// computed without rescanning shared content.
type devColorScanner struct {
	doc        *Document
	memo       map[devMemoKey]devUse // (stream, group masking) -> escaping usage
	inProg     map[*Stream]bool      // recursion guard for cyclic form/pattern references
	inProgDict map[*Dictionary]bool  // recursion guard for cyclic Type3-font container references
}

// devMemoKey identifies a memoised streamEscape result. The escaping usage of a
// stream is a property of the stream alone *for a fixed applyGroup*: one and the
// same form XObject reached with Do has its transparency group applied and
// reached as an annotation appearance or a tiling pattern does not. Keying on
// the stream alone let whichever call came first answer for both — an isolated
// calibrated group that should have covered the form's DeviceRGB was skipped
// when the appearance-stream visit cached the unmasked value first, and the page
// was then accused of "DeviceRGB used without a matching OutputIntent,
// DefaultRGB or covering group colour space". applyGroup belongs in the key.
type devMemoKey struct {
	st         *Stream
	applyGroup bool
}

func newDevColorScanner(doc *Document) *devColorScanner {
	return &devColorScanner{
		doc:        doc,
		memo:       map[devMemoKey]devUse{},
		inProg:     map[*Stream]bool{},
		inProgDict: map[*Dictionary]bool{},
	}
}

// pageDeviceUse returns the device colour families used on a page and left
// uncovered by a Default* space in scope — the same value scanPageForDeviceCS
// returns, computed with shared form/pattern work memoised.
func (s *devColorScanner) pageDeviceUse(page *Dictionary) devUse {
	var data []byte
	var key *Stream
	if c := page.Get("Contents"); c != nil {
		data, key = s.doc.view().ContentBytesAndKey(c)
	}
	u := s.container(page, data, key)

	// Annotation appearance streams contribute their own escaping usage.
	if annots, ok := s.doc.Resolve(page.Get("Annots")).(Array); ok {
		for _, aref := range annots {
			ad := s.doc.ResolveDict(aref)
			if ad == nil {
				continue
			}
			apd := s.doc.ResolveDict(ad.Get("AP"))
			if apd == nil {
				continue
			}
			for _, apKey := range []Name{"N", "R", "D"} {
				switch v := s.doc.Resolve(apd.Get(apKey)).(type) {
				case *Stream:
					u.or(s.streamEscape(v, false))
				case *Dictionary:
					for _, sv := range v.Values {
						if st, ok := s.doc.Resolve(sv).(*Stream); ok {
							u.or(s.streamEscape(st, false))
						}
					}
				}
			}
		}
	}

	// The page's own transparency group /CS being a device space is usage.
	if g := s.doc.ResolveDict(page.Get("Group")); g != nil {
		var gu devUse
		core.CheckCSForDevice(s.doc.view(), g.Get("CS"), &gu.rgb, &gu.cmyk, &gu.gray)
		u.or(gu)
	}
	return u
}

// container computes the device colour that escapes one content container (a
// page, form, pattern, or appearance stream) given its content bytes. Local
// device usage is masked by the container's own Default* spaces; usage from
// invoked forms, tiling patterns and Type 3 glyphs bypasses that masking (it was
// already masked in their own scope), matching scanContainerForDeviceCS.
func (s *devColorScanner) container(c *Dictionary, data []byte, key *Stream) devUse {
	// A Type3 font whose /Resources reference itself (directly or through another
	// Type3 font) would recurse here forever — a stack overflow, which is fatal
	// and not recoverable. The stream path is guarded by inProg; guard the dict
	// path too, mirroring the PDF/A device-colour scanner (audit C3).
	if c != nil {
		if s.inProgDict[c] {
			return devUse{}
		}
		s.inProgDict[c] = true
		defer delete(s.inProgDict, c)
	}

	var local, nested devUse
	if data != nil {
		r, cc, g := core.ScanStreamForDeviceOps(s.doc.canceler(), data)
		local.rgb, local.cmyk, local.gray = r, cc, g
	}
	used := s.doc.view().ContentUsedNamesCached(data, key)

	res := resolveResources(s.doc, c)
	if res != nil {
		if cs := s.doc.ResolveDict(res.Get("ColorSpace")); cs != nil {
			for _, v := range cs.Values {
				core.CheckCSForDevice(s.doc.view(), v, &local.rgb, &local.cmyk, &local.gray)
			}
		}
		if xo := s.doc.ResolveDict(res.Get("XObject")); xo != nil {
			for i, k := range xo.Keys {
				if !used.XObjects[string(k)] {
					continue
				}
				st, ok := s.doc.Resolve(xo.Values[i]).(*Stream)
				if !ok {
					continue
				}
				if sub, _ := st.Dict.Get("Subtype").(Name); sub == "Form" {
					nested.or(s.streamEscape(st, true))
				} else {
					core.CheckCSForDevice(s.doc.view(), st.Dict.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				}
			}
		}
		if sh := s.doc.ResolveDict(res.Get("Shading")); sh != nil {
			for i, k := range sh.Keys {
				if !used.Shadings[string(k)] {
					continue
				}
				if sd := s.doc.ResolveDict(sh.Values[i]); sd != nil {
					core.CheckCSForDevice(s.doc.view(), sd.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				} else if st, ok := s.doc.Resolve(sh.Values[i]).(*Stream); ok {
					core.CheckCSForDevice(s.doc.view(), st.Dict.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				}
			}
		}
		if pat := s.doc.ResolveDict(res.Get("Pattern")); pat != nil {
			for i, k := range pat.Keys {
				if !used.Patterns[string(k)] {
					continue
				}
				switch v := s.doc.Resolve(pat.Values[i]).(type) {
				case *Stream:
					nested.or(s.streamEscape(v, false)) // tiling pattern: no group masking
				case *Dictionary:
					if sd := s.doc.ResolveDict(v.Get("Shading")); sd != nil {
						core.CheckCSForDevice(s.doc.view(), sd.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
					}
				}
			}
		}
		if fonts := s.doc.ResolveDict(res.Get("Font")); fonts != nil {
			for _, v := range fonts.Values {
				fd := s.doc.ResolveDict(v)
				if fd == nil {
					continue
				}
				if sub, _ := fd.Get("Subtype").(Name); sub == "Type3" {
					nested.or(s.container(fd, nil, nil)) // Type3 font resources, own Default* scope
					if cp := s.doc.ResolveDict(fd.Get("CharProcs")); cp != nil {
						for _, cpv := range cp.Values {
							if st, ok := s.doc.Resolve(cpv).(*Stream); ok {
								if d := decodeContentStream(s.doc, st); d != nil {
									r, cc, g := core.ScanStreamForDeviceOps(s.doc.canceler(), d)
									nested.rgb = nested.rgb || r
									nested.cmyk = nested.cmyk || cc
									nested.gray = nested.gray || g
								}
							}
						}
					}
				}
			}
		}
	}

	dR, dC, dG := core.DefaultColorSpaces(s.doc.view(), c)
	local.rgb = local.rgb && !dR
	local.cmyk = local.cmyk && !dC
	local.gray = local.gray && !dG
	local.or(nested)
	return local
}

// streamEscape returns a content stream's escaping device usage, memoised. When
// applyGroup is set (a form XObject invoked with Do) the stream's transparency
// group /CS is applied: a device group /CS is itself usage, and an isolated
// calibrated group /CS covers matching device usage within the form.
func (s *devColorScanner) streamEscape(st *Stream, applyGroup bool) devUse {
	key := devMemoKey{st: st, applyGroup: applyGroup}
	if u, ok := s.memo[key]; ok {
		return u
	}
	if s.inProg[st] {
		return devUse{} // cyclic reference: contributes nothing, don't cache a partial
	}
	s.inProg[st] = true

	u := s.container(&st.Dict, decodeContentStream(s.doc, st), st)
	if applyGroup {
		if g := s.doc.ResolveDict(st.Dict.Get("Group")); g != nil {
			core.CheckCSForDevice(s.doc.view(), g.Get("CS"), &u.rgb, &u.cmyk, &u.gray)
			if iso, _ := s.doc.Resolve(g.Get("I")).(Boolean); bool(iso) {
				if cs := g.Get("CS"); cs != nil {
					gR, gC, gG := core.ClassifyCalibratedCS(s.doc.view(), cs)
					if gR {
						u.rgb = false
					}
					if gC {
						u.cmyk = false
					}
					if gG {
						u.gray = false
					}
				}
			}
		}
	}

	delete(s.inProg, st)
	s.memo[key] = u
	return u
}
