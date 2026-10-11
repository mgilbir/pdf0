package pdfua

// This file documents PDF/UA-2 (ISO 14289-2:2024), the PDF 2.0 accessibility
// standard succeeding PDF/UA-1. It holds no code: PDF/UA-2 shares most of
// PDF/UA-1's requirements — a tagged logical structure, a shown document
// title, Unicode-mapped text, correctly used artifacts and headings — so
// validateView runs the same checks for both parts, with the part as a
// parameter. The root package's ValidatePDFUA2 selects part 2, which makes the
// identification rule require pdfuaid:part 2, drops the UA-1 PDF 1.x header
// rule, and adds the PDF 2.0 version rule. The part also selects where the two
// standards differ, as the veraPDF profiles for them do: part 2 requires the
// catalog /Lang (8.4.4) where part 1 asks only that each text's language be
// determinable (pdfua_lang.go); part 1's annotation-description rules (7.18.1,
// 7.18.5) and its Formula rule (7.7) are not part 2's; part 2 finds a table
// header by position (positionalHeaders); and part 2 keeps Link annotations
// with different targets in different structure elements (8.2.5.20).
//
// The rules only part 2 has beyond those are in three files, each following
// what the veraPDF PDF/UA-2 profile tests: pdfua2_ident.go (pdfuaid:rev,
// clause 5), pdfua2_struct.go (one Document root in the PDF 2.0 namespace, no
// H, ListNumbering on a labelled list, one widget per Form: 8.2.5.2, 8.2.5.12,
// 8.2.5.25, 8.10.1) and pdfua2_interactive.go (structure destinations,
// invisible annotations as artifacts, AFRelationship, a widget's label or
// /Contents, an embedded file's /Desc: 8.8, 8.9.2.2, 8.9.2.4.10, 8.10.2.3,
// 8.14.1).
//
// Structure types are resolved in the element's namespace (ISO 32000-2
// 14.8.6, core.ResolveStructType): the PDF 1.7 standard namespace, which an
// element with no /NS is in and whose types the root /RoleMap maps; the PDF
// 2.0 standard namespace, with its own vocabulary (Title, FENote, Aside, Em,
// Strong, Sub, DocumentFragment, Artifact, Hn for every n; none of Art,
// BlockQuote, TOC, TOCI, Index, Private, Quote, Note, Reference, BibEntry,
// Code); MathML, which needs no mapping; and any other namespace through its
// /RoleMapNS. The checks compare the resolved type and do not yet apply
// PDF/UA-2's own rules for the 2.0-only types (FENote's note rules, Title), nor
// the profile's rules no corpus file fails (docs/pdfua.md lists them), so this
// does not assert full ISO 14289-2 conformance.
