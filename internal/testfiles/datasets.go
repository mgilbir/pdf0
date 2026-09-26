package testfiles

// The data sets the suite reads. docs/testing.md describes each one: what it
// proves, which tests read it, and where it comes from. The stamps are the ones
// the Makefile's fetch targets touch after a fetch completes.
var (
	VeraPDFCorpus = Dataset{
		Name:  "veraPDF corpus",
		Dir:   "testdata/verapdf-corpus",
		Env:   "VERAPDF_CORPUS",
		Stamp: ".ok",
		Fetch: "run `make corpus`",
	}
	PDF20Examples = Dataset{
		Name:  "PDF 2.0 reference PDFs",
		Dir:   "testdata/pdf20examples",
		Stamp: ".ok",
		Fetch: "run `make refpdfs`",
	}
	Arlington = Dataset{
		Name:  "Arlington PDF model",
		Dir:   "testdata/arlington-pdf-model",
		Env:   "ARLINGTON_MODEL",
		Stamp: ".ok",
		Sub:   "tsv/2.0",
		Fetch: "run `make arlington`",
	}
	VeraPDFProfiles = Dataset{
		Name:  "veraPDF validation profiles",
		Dir:   "spec/verapdf-profiles",
		Env:   "VERAPDF_PROFILES",
		Stamp: ".ok",
		Fetch: "run `make profiles`",
	}
	WTPDF = Dataset{
		Name:  "WTPDF / PDF/UA-2 examples",
		Dir:   "testdata/wtpdf",
		Stamp: ".ok",
		Fetch: "run `make wtpdf`",
	}
	FacturX = Dataset{
		Name:  "Factur-X corpus",
		Dir:   "testdata/facturx",
		Stamp: ".ok",
		Fetch: "run `make facturx`",
	}
	CCITT = Dataset{
		Name:  "CCITT samples",
		Dir:   "testdata/ccitt",
		Stamp: ".ok",
		Fetch: "run `make ccitt`",
	}
	JBIG2 = Dataset{
		Name:  "JBIG2 samples",
		Dir:   "testdata/jbig2",
		Stamp: ".ok",
		Fetch: "run `make jbig2`",
	}
	NotoCJK = Dataset{
		Name:  "Noto Sans CJK face",
		Dir:   "testdata/notocjk",
		Stamp: ".ok",
		Fetch: "run `make notocjk`",
	}

	// Placed by hand: no make target, so no stamp. The directory's presence is
	// the signal, and a present directory with nothing in it fails.
	CalPolyPDFVT = Dataset{
		Name:  "Cal Poly PDF/VT-1 suite",
		Dir:   "testdata/pdfvt",
		Fetch: "it is copyrighted and placed by hand (see docs/testing.md)",
	}
	PDFUAReference = Dataset{
		Name:  "PDFUA-Reference-Files",
		Dir:   "spec/pdfua/reference-files",
		Fetch: "it is placed by hand (see docs/testing.md)",
	}
	OrderXExamples = Dataset{
		Name:  "Order-X examples",
		Dir:   "spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES",
		Fetch: "they come with the Order-X specification bundle and are placed by hand",
	}
	SpecPDF20 = Dataset{
		Name:  "ISO 32000-2 PDF",
		Dir:   "spec/pdf2.0",
		Fetch: "it is copyrighted and placed by hand",
	}
	SpecPDF17 = Dataset{
		Name:  "ISO 32000-1 PDF",
		Dir:   "spec/pdf1.7",
		Fetch: "it is copyrighted and placed by hand",
	}
)
