package pdf0

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
	doc    *Document
	memo   map[*Stream]devUse // stream -> escaping usage (forms include group masking)
	inProg map[*Stream]bool   // recursion guard for cyclic form/pattern references
}

func newDevColorScanner(doc *Document) *devColorScanner {
	return &devColorScanner{doc: doc, memo: map[*Stream]devUse{}, inProg: map[*Stream]bool{}}
}

// pageDeviceUse returns the device colour families used on a page and left
// uncovered by a Default* space in scope — the same value scanPageForDeviceCS
// returns, computed with shared form/pattern work memoised.
func (s *devColorScanner) pageDeviceUse(page *Dictionary) devUse {
	var data []byte
	var key *Stream
	if c := page.Get("Contents"); c != nil {
		data, key = s.doc.contentBytesAndKey(c)
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
		checkCSForDevice(s.doc, g.Get("CS"), &gu.rgb, &gu.cmyk, &gu.gray)
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
	var local, nested devUse
	if data != nil {
		r, cc, g := scanStreamForDeviceOps(data)
		local.rgb, local.cmyk, local.gray = r, cc, g
	}
	used := s.doc.contentUsedNamesCached(data, key)

	res := resolveResources(s.doc, c)
	if res != nil {
		if cs := s.doc.ResolveDict(res.Get("ColorSpace")); cs != nil {
			for _, v := range cs.Values {
				checkCSForDevice(s.doc, v, &local.rgb, &local.cmyk, &local.gray)
			}
		}
		if xo := s.doc.ResolveDict(res.Get("XObject")); xo != nil {
			for i, k := range xo.Keys {
				if !used.xobjects[string(k)] {
					continue
				}
				st, ok := s.doc.Resolve(xo.Values[i]).(*Stream)
				if !ok {
					continue
				}
				if sub, _ := st.Dict.Get("Subtype").(Name); sub == "Form" {
					nested.or(s.streamEscape(st, true))
				} else {
					checkCSForDevice(s.doc, st.Dict.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				}
			}
		}
		if sh := s.doc.ResolveDict(res.Get("Shading")); sh != nil {
			for i, k := range sh.Keys {
				if !used.shadings[string(k)] {
					continue
				}
				if sd := s.doc.ResolveDict(sh.Values[i]); sd != nil {
					checkCSForDevice(s.doc, sd.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				} else if st, ok := s.doc.Resolve(sh.Values[i]).(*Stream); ok {
					checkCSForDevice(s.doc, st.Dict.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
				}
			}
		}
		if pat := s.doc.ResolveDict(res.Get("Pattern")); pat != nil {
			for i, k := range pat.Keys {
				if !used.patterns[string(k)] {
					continue
				}
				switch v := s.doc.Resolve(pat.Values[i]).(type) {
				case *Stream:
					nested.or(s.streamEscape(v, false)) // tiling pattern: no group masking
				case *Dictionary:
					if sd := s.doc.ResolveDict(v.Get("Shading")); sd != nil {
						checkCSForDevice(s.doc, sd.Get("ColorSpace"), &local.rgb, &local.cmyk, &local.gray)
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
									r, cc, g := scanStreamForDeviceOps(d)
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

	dR, dC, dG := getDefaultColorSpaces(s.doc, c)
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
	if u, ok := s.memo[st]; ok {
		return u
	}
	if s.inProg[st] {
		return devUse{} // cyclic reference: contributes nothing, don't cache a partial
	}
	s.inProg[st] = true

	u := s.container(&st.Dict, decodeContentStream(s.doc, st), st)
	if applyGroup {
		if g := s.doc.ResolveDict(st.Dict.Get("Group")); g != nil {
			checkCSForDevice(s.doc, g.Get("CS"), &u.rgb, &u.cmyk, &u.gray)
			if iso, _ := s.doc.Resolve(g.Get("I")).(Boolean); bool(iso) {
				if cs := g.Get("CS"); cs != nil {
					gR, gC, gG := classifyCalibratedCS(s.doc, cs)
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
	s.memo[st] = u
	return u
}
