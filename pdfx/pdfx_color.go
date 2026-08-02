package pdfx

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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

type DevUse struct{ RGB, CMYK, Gray bool }

func (u *DevUse) or(o DevUse) {
	u.RGB = u.RGB || o.RGB
	u.CMYK = u.CMYK || o.CMYK
	u.Gray = u.Gray || o.Gray
}

// DevColorScanner memoises the escaping device-colour usage of form XObjects,
// tiling patterns, and appearance streams so a page's device usage can be
// computed without rescanning shared content.
type DevColorScanner struct {
	doc        core.View
	memo       map[devMemoKey]DevUse       // (stream, group masking) -> escaping usage
	inProg     map[*object.Stream]bool     // recursion guard for cyclic form/pattern references
	inProgDict map[*object.Dictionary]bool // recursion guard for cyclic Type3-font container references
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
	st         *object.Stream
	applyGroup bool
}

func NewDevColorScanner(doc core.View) *DevColorScanner {
	return &DevColorScanner{
		doc:        doc,
		memo:       map[devMemoKey]DevUse{},
		inProg:     map[*object.Stream]bool{},
		inProgDict: map[*object.Dictionary]bool{},
	}
}

// PageDeviceUse returns the device colour families used on a page and left
// uncovered by a Default* space in scope — the same value scanPageForDeviceCS
// returns, computed with shared form/pattern work memoised.
func (s *DevColorScanner) PageDeviceUse(page *object.Dictionary) DevUse {
	var data []byte
	var key *object.Stream
	if c := page.Get("Contents"); c != nil {
		data, key = s.doc.ContentBytesAndKey(c)
	}
	u := s.container(page, data, key)

	// Annotation appearance streams contribute their own escaping usage.
	if annots, ok := s.doc.Resolve(page.Get("Annots")).(object.Array); ok {
		for _, aref := range annots {
			ad := s.doc.ResolveDict(aref)
			if ad == nil {
				continue
			}
			apd := s.doc.ResolveDict(ad.Get("AP"))
			if apd == nil {
				continue
			}
			for _, apKey := range []object.Name{"N", "R", "D"} {
				switch v := s.doc.Resolve(apd.Get(apKey)).(type) {
				case *object.Stream:
					u.or(s.streamEscape(v, false))
				case *object.Dictionary:
					for _, sv := range v.Values {
						if st, ok := s.doc.Resolve(sv).(*object.Stream); ok {
							u.or(s.streamEscape(st, false))
						}
					}
				}
			}
		}
	}

	// The page's own transparency group /CS being a device space is usage.
	if g := s.doc.ResolveDict(page.Get("Group")); g != nil {
		var gu DevUse
		core.CheckCSForDevice(s.doc, g.Get("CS"), &gu.RGB, &gu.CMYK, &gu.Gray)
		u.or(gu)
	}
	return u
}

// container computes the device colour that escapes one content container (a
// page, form, pattern, or appearance stream) given its content bytes. Local
// device usage is masked by the container's own Default* spaces; usage from
// invoked forms, tiling patterns and Type 3 glyphs bypasses that masking (it was
// already masked in their own scope), matching scanContainerForDeviceCS.
func (s *DevColorScanner) container(c *object.Dictionary, data []byte, key *object.Stream) DevUse {
	// A Type3 font whose /Resources reference itself (directly or through another
	// Type3 font) would recurse here forever — a stack overflow, which is fatal
	// and not recoverable. The stream path is guarded by inProg; guard the dict
	// path too, mirroring the PDF/A device-colour scanner (audit C3).
	if c != nil {
		if s.inProgDict[c] {
			return DevUse{}
		}
		s.inProgDict[c] = true
		defer delete(s.inProgDict, c)
	}

	var local, nested DevUse
	if data != nil {
		r, cc, g := core.ScanStreamForDeviceOps(s.doc.Cancel, data)
		local.RGB, local.CMYK, local.Gray = r, cc, g
	}
	used := s.doc.ContentUsedNamesCached(data, key)

	res := s.doc.Resources(c)
	if res != nil {
		if cs := s.doc.ResolveDict(res.Get("ColorSpace")); cs != nil {
			for _, v := range cs.Values {
				core.CheckCSForDevice(s.doc, v, &local.RGB, &local.CMYK, &local.Gray)
			}
		}
		if xo := s.doc.ResolveDict(res.Get("XObject")); xo != nil {
			for i, k := range xo.Keys {
				if !used.XObjects[string(k)] {
					continue
				}
				st, ok := s.doc.Resolve(xo.Values[i]).(*object.Stream)
				if !ok {
					continue
				}
				if sub, _ := st.Dict.Get("Subtype").(object.Name); sub == "Form" {
					nested.or(s.streamEscape(st, true))
				} else {
					core.CheckCSForDevice(s.doc, st.Dict.Get("ColorSpace"), &local.RGB, &local.CMYK, &local.Gray)
				}
			}
		}
		if sh := s.doc.ResolveDict(res.Get("Shading")); sh != nil {
			for i, k := range sh.Keys {
				if !used.Shadings[string(k)] {
					continue
				}
				if sd := s.doc.ResolveDict(sh.Values[i]); sd != nil {
					core.CheckCSForDevice(s.doc, sd.Get("ColorSpace"), &local.RGB, &local.CMYK, &local.Gray)
				} else if st, ok := s.doc.Resolve(sh.Values[i]).(*object.Stream); ok {
					core.CheckCSForDevice(s.doc, st.Dict.Get("ColorSpace"), &local.RGB, &local.CMYK, &local.Gray)
				}
			}
		}
		if pat := s.doc.ResolveDict(res.Get("Pattern")); pat != nil {
			for i, k := range pat.Keys {
				if !used.Patterns[string(k)] {
					continue
				}
				switch v := s.doc.Resolve(pat.Values[i]).(type) {
				case *object.Stream:
					nested.or(s.streamEscape(v, false)) // tiling pattern: no group masking
				case *object.Dictionary:
					if sd := s.doc.ResolveDict(v.Get("Shading")); sd != nil {
						core.CheckCSForDevice(s.doc, sd.Get("ColorSpace"), &local.RGB, &local.CMYK, &local.Gray)
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
				if sub, _ := fd.Get("Subtype").(object.Name); sub == "Type3" {
					nested.or(s.container(fd, nil, nil)) // Type3 font resources, own Default* scope
					if cp := s.doc.ResolveDict(fd.Get("CharProcs")); cp != nil {
						for _, cpv := range cp.Values {
							if st, ok := s.doc.Resolve(cpv).(*object.Stream); ok {
								if d := s.doc.Content(st); d != nil {
									r, cc, g := core.ScanStreamForDeviceOps(s.doc.Cancel, d)
									nested.RGB = nested.RGB || r
									nested.CMYK = nested.CMYK || cc
									nested.Gray = nested.Gray || g
								}
							}
						}
					}
				}
			}
		}
	}

	dR, dC, dG := core.DefaultColorSpaces(s.doc, c)
	local.RGB = local.RGB && !dR
	local.CMYK = local.CMYK && !dC
	local.Gray = local.Gray && !dG
	local.or(nested)
	return local
}

// streamEscape returns a content stream's escaping device usage, memoised. When
// applyGroup is set (a form XObject invoked with Do) the stream's transparency
// group /CS is applied: a device group /CS is itself usage, and an isolated
// calibrated group /CS covers matching device usage within the form.
func (s *DevColorScanner) streamEscape(st *object.Stream, applyGroup bool) DevUse {
	key := devMemoKey{st: st, applyGroup: applyGroup}
	if u, ok := s.memo[key]; ok {
		return u
	}
	if s.inProg[st] {
		return DevUse{} // cyclic reference: contributes nothing, don't cache a partial
	}
	s.inProg[st] = true

	u := s.container(&st.Dict, s.doc.Content(st), st)
	if applyGroup {
		if g := s.doc.ResolveDict(st.Dict.Get("Group")); g != nil {
			core.CheckCSForDevice(s.doc, g.Get("CS"), &u.RGB, &u.CMYK, &u.Gray)
			if iso, _ := s.doc.Resolve(g.Get("I")).(object.Boolean); bool(iso) {
				if cs := g.Get("CS"); cs != nil {
					gR, gC, gG := core.ClassifyCalibratedCS(s.doc, cs)
					if gR {
						u.RGB = false
					}
					if gC {
						u.CMYK = false
					}
					if gG {
						u.Gray = false
					}
				}
			}
		}
	}

	delete(s.inProg, st)
	s.memo[key] = u
	return u
}
