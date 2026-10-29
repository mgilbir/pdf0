package testfiles

// The data sets the suite reads. docs/testing.md's data set table is generated
// from this list (what each proves and where it comes from are its Proves and
// Source) and from the tests that read each. The stamps are the ones the
// Makefile's fetch targets touch after a fetch completes.
var (
	VeraPDFCorpus = Dataset{
		Name:   "veraPDF corpus",
		Dir:    "testdata/verapdf-corpus",
		Env:    "VERAPDF_CORPUS",
		Stamp:  ".ok",
		Fetch:  "run `make corpus`",
		Proves: "The PDF/A conformance ratchet: no false positives, no missed violations, no parse errors; the rule-coverage ratchet; most whole-file robustness tests",
		Source: "[veraPDF/veraPDF-corpus](https://github.com/veraPDF/veraPDF-corpus)",
	}
	VeraPDFVerdicts = Dataset{
		Name:   "veraPDF's verdicts on the corpus",
		Dir:    "testdata/verapdf-verdicts",
		Env:    "VERAPDF_VERDICTS",
		Stamp:  ".ok",
		Fetch:  "run `make verapdf-verdicts` (needs the corpus, and Docker or a veraPDF in `PDF0_VERAPDF`)",
		Proves: "The rule-coverage ratchet judged by what veraPDF reports for each corpus file rather than what its name says, and false positives against veraPDF's own verdicts",
		Source: "Generated here by veraPDF from the veraPDF corpus (`testdata/verapdf-verdicts/generate.sh`); never committed",
	}
	PDF20Examples = Dataset{
		Name:   "PDF 2.0 reference PDFs",
		Dir:    "testdata/pdf20examples",
		Stamp:  ".ok",
		Fetch:  "run `make refpdfs`",
		Proves: "Read→Write→Read round trips over real PDF 2.0 files; also seeds the fuzzers",
		Source: "[pdf-association/pdf20examples](https://github.com/pdf-association/pdf20examples)",
	}
	Arlington = Dataset{
		Name:   "Arlington PDF model",
		Dir:    "testdata/arlington-pdf-model",
		Env:    "ARLINGTON_MODEL",
		Stamp:  ".ok",
		Sub:    "tsv/2.0",
		Fetch:  "run `make arlington`",
		Proves: "External grammar oracle: the parser and serializer represent objects faithfully (right types, keys, structure)",
		Source: "[pdf-association/arlington-pdf-model](https://github.com/pdf-association/arlington-pdf-model), Apache-2.0",
	}
	VeraPDFProfiles = Dataset{
		Name:   "veraPDF validation profiles",
		Dir:    "spec/verapdf-profiles",
		Env:    "VERAPDF_PROFILES",
		Stamp:  ".ok",
		Fetch:  "run `make profiles`",
		Proves: "Per-rule detection coverage against the reference validator's own rule inventory (with the corpus)",
		Source: "[veraPDF/veraPDF-validation-profiles](https://github.com/veraPDF/veraPDF-validation-profiles), CC BY 4.0",
	}
	WTPDF = Dataset{
		Name:   "WTPDF / PDF/UA-2 examples",
		Dir:    "testdata/wtpdf",
		Stamp:  ".ok",
		Fetch:  "run `make wtpdf`",
		Proves: "Round trips and robustness over complex real tagged PDF 2.0 (structure trees, associated files, MathML, role maps)",
		Source: "LaTeX Project, [tagging-project discussion 72](https://github.com/latex3/tagging-project/discussions/72), fetched from Google Drive; licences vary per file (see `testdata/wtpdf/sources.tsv`)",
	}
	FacturX = Dataset{
		Name:   "Factur-X corpus",
		Dir:    "testdata/facturx",
		Stamp:  ".ok",
		Fetch:  "run `make facturx`",
		Proves: "FP=0 oracle for the Factur-X **container** checks; the invoice rule engine's findings are ratcheted, not forbidden, because which business rules fire is formalis' scope decision",
		Source: "ZUGFeRD/corpus and ZUGFeRD/mustangproject, Apache-2.0",
	}
	CCITT = Dataset{
		Name:   "CCITT samples",
		Dir:    "testdata/ccitt",
		Stamp:  ".ok",
		Fetch:  "run `make ccitt`",
		Proves: "Decode oracle for the Group 3/4 fax decoder (the veraPDF corpus has no CCITT images)",
		Source: "pdf.js (Apache-2.0), PyPDF4 (BSD)",
	}
	JBIG2 = Dataset{
		Name:   "JBIG2 samples",
		Dir:    "testdata/jbig2",
		Stamp:  ".ok",
		Fetch:  "run `make jbig2`",
		Proves: "Decode oracle for the JBIG2 decoder: generic templates, MMR, symbol/text, halftone, refinement",
		Source: "pdf.js conformance suite, Apache-2.0",
	}
	NotoCJK = Dataset{
		Name:   "Noto Sans CJK face",
		Dir:    "testdata/notocjk",
		Stamp:  ".ok",
		Fetch:  "run `make notocjk`",
		Proves: "The only CID-keyed CFF face the embedding tests have: a charset that is not the identity is what tells a CID from a glyph index",
		Source: "[notofonts/noto-cjk](https://github.com/notofonts/noto-cjk), OFL",
	}
	NotoEmoji = Dataset{
		Name:   "Noto Color Emoji (CBDT and COLRv1)",
		Dir:    "testdata/notoemoji",
		Stamp:  ".ok",
		Fetch:  "run `make notoemoji`",
		Proves: "Colour glyphs written as Type 3 fonts: CBDT colour bitmaps and COLRv1 paint graphs, checked against HarfBuzz, Skia and veraPDF where those are installed",
		Source: "[googlefonts/noto-emoji](https://github.com/googlefonts/noto-emoji), OFL",
	}

	// Placed by hand: no make target, so no stamp. The directory's presence is
	// the signal, and a present directory with nothing in it fails.
	CalPolyPDFVT = Dataset{
		Name:   "Cal Poly PDF/VT-1 suite",
		Dir:    "testdata/pdfvt",
		Fetch:  "it is copyrighted and placed by hand (see docs/testing.md)",
		Proves: "FP=0 oracle for PDF/VT, PDF/X and DPart: conforming files must report zero violations",
		Source: "Cal Poly Graphic Communications PDF/VT-1 Test File Suite; copyrighted test content, not redistributable",
	}
	PDFUAReference = Dataset{
		Name:   "PDFUA-Reference-Files",
		Dir:    "spec/pdfua/reference-files",
		Fetch:  "it is placed by hand (see docs/testing.md)",
		Proves: "FP=0 oracle for PDF/UA: conformant reference documents must report zero violations",
		Source: "PDFUA-Reference-Files suite from pdfa.org",
	}
	OrderXExamples = Dataset{
		Name:   "Order-X examples",
		Dir:    "spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES",
		Fetch:  "they come with the Order-X specification bundle and are placed by hand",
		Proves: "Order-X container checks against the conforming examples",
		Source: "Order-X specification bundle",
	}
	SpecPDF20 = Dataset{
		Name:   "ISO 32000-2 PDF",
		Dir:    "spec/pdf2.0",
		Fetch:  "it is copyrighted and placed by hand",
		Proves: "The ISO 32000-2 spec-example tests, over every EXAMPLE of `spec/pdf2.0/ISO_32000-2_sponsored-ec2.pdf` as `cmd/extract_spec_examples` extracts it on the spot into `testdata/spec-examples/` (needs `pdftotext` and `python3`): they lex and parse each, logging what does not, and round-trip each single complete object. The examples are ISO's and are not committed",
		Source: "ISO; copyrighted, never committed",
	}
	SpecPDF17 = Dataset{
		Name:   "ISO 32000-1 PDF",
		Dir:    "spec/pdf1.7",
		Fetch:  "it is copyrighted and placed by hand",
		Proves: "As for ISO 32000-2, from `spec/pdf1.7/PDF32000_2008.pdf`",
		Source: "ISO / Adobe; copyrighted, never committed",
	}
	// Freely downloadable, but 32 MB that only a guard needs, so it is
	// placed by hand like the ISO PDFs rather than fetched in CI.
	PDFReference17 = Dataset{
		Name:   "Adobe PDF Reference 1.7",
		Dir:    "spec/pdfref1.7",
		Env:    "PDF_REFERENCE_17",
		Fetch:  "download it from Adobe and place it by hand (see docs/testing.md)",
		Proves: "Guards the committed `testdata/pdfref17_examples.json`: it must be exactly what `cmd/extract_spec_examples/pdfref17.py` makes of `pdfreference1.7old.pdf` (also needs `pdftohtml`, `pdftotext` and `python3`)",
		Source: "[Adobe](https://opensource.adobe.com/dc-acrobat-sdk-docs/pdfstandards/pdfreference1.7old.pdf); copyrighted, never committed; its example code is licensed by its section 1.4 (THIRD_PARTY_NOTICES)",
	}
)
