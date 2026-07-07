package pdf0

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestInspectFPs(t *testing.T) {
	type testCase struct {
		name  string
		path  string
		level PDFALevel
	}

	cases := []testCase{
		{
			name:  "6-2-4-3-t04-pass-a (DeviceRGB + OutputIntent)",
			path:  "testdata/verapdf-corpus/PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.3 Uncalibrated -Device colour spaces/veraPDF test suite 6-2-4-3-t04-pass-a.pdf",
			level: PDFA4,
		},
		{
			name:  "6-2-4-5-t01-pass-e (Indexed/Pattern colour spaces)",
			path:  "testdata/verapdf-corpus/PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.5 Indexed and Pattern colour spaces/veraPDF test suite 6-2-4-5-t01-pass-e.pdf",
			level: PDFA4,
		},
		{
			name:  "6-2-9-t01-pass-d (Transparency PDF/A-4)",
			path:  "testdata/verapdf-corpus/PDF_A-4/6.2 Graphics/6.2.9 Transparency/veraPDF test suite 6-2-9-t01-pass-d.pdf",
			level: PDFA4,
		},
		{
			name:  "6-8-t02-pass-d (Embedded files PDF/A-3b, transparency issue)",
			path:  "testdata/verapdf-corpus/PDF_A-3b/6.8 Embedded files/veraPDF test suite 6-8-t02-pass-d.pdf",
			level: PDFA3b,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(tc.path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()
			fi, _ := f.Stat()

			doc, err := Read(f, fi.Size())
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			fmt.Printf("\n========================================\n")
			fmt.Printf("FILE: %s\n", tc.path)
			fmt.Printf("Version: %s\n", doc.Version)
			fmt.Printf("Objects: %d\n", len(doc.Objects))
			fmt.Printf("========================================\n")

			// Print trailer
			fmt.Printf("\n--- TRAILER ---\n")
			printDict(doc, &doc.Trailer, "  ", 0)

			// Get catalog
			catalog := getCatalog(doc)
			if catalog == nil {
				t.Fatal("no catalog")
			}
			fmt.Printf("\n--- CATALOG ---\n")
			printDict(doc, catalog, "  ", 0)

			// Catalog-level OutputIntents
			fmt.Printf("\n--- CATALOG OutputIntents ---\n")
			oiRef := catalog.Get("OutputIntents")
			if oiRef != nil {
				oiObj := doc.Resolve(oiRef)
				if arr, ok := oiObj.(Array); ok {
					for i, elem := range arr {
						fmt.Printf("  OutputIntent[%d]:\n", i)
						d := doc.ResolveDict(elem)
						if d != nil {
							printDict(doc, d, "    ", 0)
							// Show ICC profile info
							profileRef := d.Get("DestOutputProfile")
							if profileRef != nil {
								profileObj := doc.Resolve(profileRef)
								if stream, ok := profileObj.(*Stream); ok {
									profileData := getICCProfileData(stream)
									if len(profileData) >= 20 {
										cs := string(profileData[16:20])
										fmt.Printf("    ICC Profile ColorSpace: %q\n", cs)
									} else {
										fmt.Printf("    ICC Profile: too short (%d bytes)\n", len(profileData))
									}
								}
							}
						}
					}
				}
			} else {
				fmt.Printf("  (none)\n")
			}

			hasRGB, hasCMYK, hasGray := getOutputIntentCoverage(doc, catalog)
			fmt.Printf("  Catalog coverage: RGB=%v CMYK=%v Gray=%v\n", hasRGB, hasCMYK, hasGray)

			// Pages
			pagesRef := catalog.Get("Pages")
			pages := collectPages(doc, pagesRef)
			fmt.Printf("\n--- PAGES (%d) ---\n", len(pages))

			for i, page := range pages {
				fmt.Printf("\n  Page %d (obj %d):\n", i, page.objNum)
				printDict(doc, page.dict, "    ", 1)

				// Page-level OutputIntents
				pOI := page.dict.Get("OutputIntents")
				if pOI != nil {
					fmt.Printf("    Page-level OutputIntents:\n")
					pOIObj := doc.Resolve(pOI)
					if arr, ok := pOIObj.(Array); ok {
						for j, elem := range arr {
							fmt.Printf("      OutputIntent[%d]:\n", j)
							d := doc.ResolveDict(elem)
							if d != nil {
								printDict(doc, d, "        ", 0)
								profileRef := d.Get("DestOutputProfile")
								if profileRef != nil {
									profileObj := doc.Resolve(profileRef)
									if stream, ok := profileObj.(*Stream); ok {
										profileData := getICCProfileData(stream)
										if len(profileData) >= 20 {
											cs := string(profileData[16:20])
											fmt.Printf("        ICC Profile ColorSpace: %q\n", cs)
										}
									}
								}
							}
						}
					}
				}

				// Page /Group
				groupRef := page.dict.Get("Group")
				if groupRef != nil {
					fmt.Printf("    /Group:\n")
					gd := doc.ResolveDict(groupRef)
					if gd != nil {
						printDict(doc, gd, "      ", 0)
					}
				}

				// Resources / ColorSpace
				res := resolveResources(doc, page.dict)
				if res != nil {
					fmt.Printf("    Resources keys: %v\n", res.Keys)
					csRef := res.Get("ColorSpace")
					if csRef != nil {
						fmt.Printf("    ColorSpace dict:\n")
						csDict := doc.ResolveDict(csRef)
						if csDict == nil {
							if d, ok := csRef.(*Dictionary); ok {
								csDict = d
							}
						}
						if csDict != nil {
							for idx, key := range csDict.Keys {
								val := doc.Resolve(csDict.Values[idx])
								fmt.Printf("      /%s = %s\n", key, describeObject(doc, val))
							}
						} else {
							fmt.Printf("      (not a dict: %T)\n", doc.Resolve(csRef))
						}
					}

					// Check for DefaultRGB/DefaultCMYK/DefaultGray
					hasDefRGB, hasDefCMYK, hasDefGray := getDefaultColorSpaces(doc, page.dict)
					fmt.Printf("    Default CS: RGB=%v CMYK=%v Gray=%v\n", hasDefRGB, hasDefCMYK, hasDefGray)

					// Show XObject resources
					xobjRef := res.Get("XObject")
					if xobjRef != nil {
						fmt.Printf("    XObject dict:\n")
						xobjDict := doc.ResolveDict(xobjRef)
						if xobjDict == nil {
							if d, ok := xobjRef.(*Dictionary); ok {
								xobjDict = d
							}
						}
						if xobjDict != nil {
							for idx, key := range xobjDict.Keys {
								val := doc.Resolve(xobjDict.Values[idx])
								if stream, ok := val.(*Stream); ok {
									subtype, _ := stream.Dict.Get("Subtype").(Name)
									cs := stream.Dict.Get("ColorSpace")
									group := stream.Dict.Get("Group")
									fmt.Printf("      /%s: Stream Subtype=%s", key, subtype)
									if cs != nil {
										fmt.Printf(" CS=%s", describeObject(doc, doc.Resolve(cs)))
									}
									if group != nil {
										gd := doc.ResolveDict(group)
										if gd != nil {
											fmt.Printf(" Group={")
											for gi, gk := range gd.Keys {
												fmt.Printf("/%s=%s ", gk, describeObject(doc, gd.Values[gi]))
											}
											fmt.Printf("}")
										}
									}
									// Show SMask on images
									if smask := stream.Dict.Get("SMask"); smask != nil {
										fmt.Printf(" SMask=%s", describeObject(doc, smask))
									}
									fmt.Printf(" (dict keys: %v)\n", stream.Dict.Keys)
								} else {
									fmt.Printf("      /%s: %T\n", key, val)
								}
							}
						}
					}

					// Show ExtGState resources
					gsRef := res.Get("ExtGState")
					if gsRef != nil {
						fmt.Printf("    ExtGState dict:\n")
						gsDict := doc.ResolveDict(gsRef)
						if gsDict == nil {
							if d, ok := gsRef.(*Dictionary); ok {
								gsDict = d
							}
						}
						if gsDict != nil {
							for idx, key := range gsDict.Keys {
								val := doc.ResolveDict(gsDict.Values[idx])
								if val != nil {
									fmt.Printf("      /%s: {", key)
									for vi, vk := range val.Keys {
										fmt.Printf("/%s=%s ", vk, describeObject(doc, val.Values[vi]))
									}
									fmt.Printf("}\n")
								}
							}
						}
					}
				}

				// Check device CS scanning
				usesRGB, usesCMYK, usesGray := scanPageForDeviceCS(doc, page.dict)
				fmt.Printf("    Device CS scan: RGB=%v CMYK=%v Gray=%v\n", usesRGB, usesCMYK, usesGray)

				// Check transparency detection
				usesTransparency := pageUsesTransparency(doc, page.dict)
				fmt.Printf("    Uses transparency: %v\n", usesTransparency)

				// Annotations
				annotsRef := page.dict.Get("Annots")
				if annotsRef != nil {
					annotsObj := doc.Resolve(annotsRef)
					if annotsArr, ok := annotsObj.(Array); ok {
						fmt.Printf("    Annotations (%d):\n", len(annotsArr))
						for ai, annotRef := range annotsArr {
							annotDict := doc.ResolveDict(annotRef)
							if annotDict == nil {
								continue
							}
							fmt.Printf("      Annot[%d] keys: %v\n", ai, annotDict.Keys)
							// Show AP
							ap := annotDict.Get("AP")
							if ap != nil {
								apDict := doc.ResolveDict(ap)
								if apDict == nil {
									if d, ok := ap.(*Dictionary); ok {
										apDict = d
									}
								}
								if apDict != nil {
									fmt.Printf("        AP keys: %v\n", apDict.Keys)
									for _, apKey := range []Name{"N", "R", "D"} {
										apEntry := apDict.Get(apKey)
										if apEntry == nil {
											continue
										}
										apObj := doc.Resolve(apEntry)
										if stream, ok := apObj.(*Stream); ok {
											fmt.Printf("        AP/%s: Stream keys=%v\n", apKey, stream.Dict.Keys)
											// Check Group on appearance stream
											appGroup := stream.Dict.Get("Group")
											if appGroup != nil {
												appGD := doc.ResolveDict(appGroup)
												if appGD != nil {
													fmt.Printf("          Group: {")
													for gi, gk := range appGD.Keys {
														fmt.Printf("/%s=%s ", gk, describeObject(doc, appGD.Values[gi]))
													}
													fmt.Printf("}\n")
												}
											}
											// Check Resources in appearance
											appRes := resolveResources(doc, &stream.Dict)
											if appRes != nil {
												fmt.Printf("          Resources keys: %v\n", appRes.Keys)
												// Check ExtGState
												appGS := appRes.Get("ExtGState")
												if appGS != nil {
													appGSDict := doc.ResolveDict(appGS)
													if appGSDict == nil {
														if d, ok := appGS.(*Dictionary); ok {
															appGSDict = d
														}
													}
													if appGSDict != nil {
														for gi, gk := range appGSDict.Keys {
															gv := doc.ResolveDict(appGSDict.Values[gi])
															if gv != nil {
																fmt.Printf("          ExtGState /%s: {", gk)
																for vi, vk := range gv.Keys {
																	fmt.Printf("/%s=%s ", vk, describeObject(doc, gv.Values[vi]))
																}
																fmt.Printf("}\n")
															}
														}
													}
												}
											}
										} else if dict, ok := apObj.(*Dictionary); ok {
											fmt.Printf("        AP/%s: Dict (state dict) keys=%v\n", apKey, dict.Keys)
										}
									}
								}
							}
							// BM, CA, ca on annotation itself
							if bm := annotDict.Get("BM"); bm != nil {
								fmt.Printf("        BM=%s\n", describeObject(doc, bm))
							}
							if ca := annotDict.Get("CA"); ca != nil {
								fmt.Printf("        CA=%s\n", describeObject(doc, ca))
							}
							if ca := annotDict.Get("ca"); ca != nil {
								fmt.Printf("        ca=%s\n", describeObject(doc, ca))
							}
						}
					}
				}
			}

			// All objects summary
			fmt.Printf("\n--- ALL OBJECTS ---\n")
			for num, obj := range doc.Objects {
				fmt.Printf("  obj %d gen %d: %s\n", num, obj.Generation, describeObjectBrief(doc, obj.Value))
			}

			// Run validation
			fmt.Printf("\n--- VALIDATION ERRORS (level=%s) ---\n", tc.level)
			errs := ValidatePDFA(doc, tc.level)
			if len(errs) == 0 {
				fmt.Printf("  PASS (no errors)\n")
			} else {
				for _, e := range errs {
					fmt.Printf("  %s\n", e.Error())
				}
			}
			fmt.Printf("\n")
		})
	}
}

func printDict(doc *Document, d *Dictionary, indent string, maxDepth int) {
	seen := make(map[*Dictionary]bool)
	printDictRecursive(doc, d, indent, maxDepth, seen)
}

func printDictRecursive(doc *Document, d *Dictionary, indent string, maxDepth int, seen map[*Dictionary]bool) {
	if seen[d] {
		fmt.Printf("%s(cycle)\n", indent)
		return
	}
	seen[d] = true
	for i, key := range d.Keys {
		val := d.Values[i]
		if key == "Contents" || key == "Data" {
			fmt.Printf("%s/%s: (stream/content data omitted)\n", indent, key)
			continue
		}
		resolved := doc.Resolve(val)
		switch v := resolved.(type) {
		case *Dictionary:
			if maxDepth > 0 {
				fmt.Printf("%s/%s: Dict {keys: %v}\n", indent, key, v.Keys)
			} else {
				fmt.Printf("%s/%s: Dict {\n", indent, key)
				printDictRecursive(doc, v, indent+"  ", 0, seen)
				fmt.Printf("%s}\n", indent)
			}
		case *Stream:
			fmt.Printf("%s/%s: Stream {keys: %v, data: %d bytes}\n", indent, key, v.Dict.Keys, len(v.Data))
		case Array:
			if len(v) <= 6 {
				fmt.Printf("%s/%s: %s\n", indent, key, describeObject(doc, v))
			} else {
				fmt.Printf("%s/%s: Array[%d]\n", indent, key, len(v))
			}
		default:
			fmt.Printf("%s/%s: %s\n", indent, key, describeObject(doc, val))
		}
	}
}

func describeObject(doc *Document, obj Object) string {
	if obj == nil {
		return "<nil>"
	}
	switch v := obj.(type) {
	case Boolean:
		return fmt.Sprintf("%v", bool(v))
	case Integer:
		return fmt.Sprintf("%d", int64(v))
	case Real:
		return fmt.Sprintf("%.4f", float64(v))
	case String:
		if len(v.Value) > 40 {
			return fmt.Sprintf("String(%d bytes)", len(v.Value))
		}
		return fmt.Sprintf("%q", string(v.Value))
	case Name:
		return fmt.Sprintf("/%s", string(v))
	case Array:
		parts := make([]string, 0, len(v))
		for _, elem := range v {
			parts = append(parts, describeObject(doc, elem))
		}
		return "[" + strings.Join(parts, " ") + "]"
	case *Dictionary:
		return fmt.Sprintf("Dict{keys:%v}", v.Keys)
	case *Stream:
		return fmt.Sprintf("Stream{keys:%v, %d bytes}", v.Dict.Keys, len(v.Data))
	case Null:
		return "null"
	case IndirectRef:
		return fmt.Sprintf("%d %d R", v.Number, v.Generation)
	case *IndirectObject:
		return fmt.Sprintf("IndObj(%d %d) -> %s", v.Number, v.Generation, describeObject(doc, v.Value))
	default:
		return fmt.Sprintf("%T", v)
	}
}

func describeObjectBrief(doc *Document, obj Object) string {
	if obj == nil {
		return "<nil>"
	}
	switch v := obj.(type) {
	case *Dictionary:
		typeVal := v.Get("Type")
		subtypeVal := v.Get("Subtype")
		s := fmt.Sprintf("Dict{keys:%v", v.Keys)
		if typeVal != nil {
			s += fmt.Sprintf(" Type=%s", describeObject(doc, typeVal))
		}
		if subtypeVal != nil {
			s += fmt.Sprintf(" Subtype=%s", describeObject(doc, subtypeVal))
		}
		return s + "}"
	case *Stream:
		typeVal := v.Dict.Get("Type")
		subtypeVal := v.Dict.Get("Subtype")
		s := fmt.Sprintf("Stream{keys:%v %d bytes", v.Dict.Keys, len(v.Data))
		if typeVal != nil {
			s += fmt.Sprintf(" Type=%s", describeObject(doc, typeVal))
		}
		if subtypeVal != nil {
			s += fmt.Sprintf(" Subtype=%s", describeObject(doc, subtypeVal))
		}
		return s + "}"
	case Array:
		return fmt.Sprintf("Array[%d]", len(v))
	default:
		return describeObject(doc, obj)
	}
}
