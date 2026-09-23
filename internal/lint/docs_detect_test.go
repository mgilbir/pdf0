package lint

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestDocChecksDetect plants each kind of drift the documentation checks exist
// for, and requires each check to see it — and to pass the corrected form, so
// that a check which fires on everything does not pass for one that works. A
// check that has never been seen to fail proves nothing.
func TestDocChecksDetect(t *testing.T) {
	root := testfiles.Root(t)

	t.Run("snippet", func(t *testing.T) {
		env := loadSnippetEnv(t)
		collectSnippets(t) // loads the module's package names
		bad := snippet{where: "planted", file: "planted.md", line: 1,
			body: "errs := pdf0.ValidatePDFA(doc, pdf0.PDFA4)\n_ = errs"}
		if got := env.check(bad); len(got) == 0 {
			t.Error("a snippet naming pdf0.PDFA4, which moved to pdfa, type-checked clean")
		}
		good := bad
		good.body = "errs := pdf0.ValidatePDFA(doc, pdfa.PDFA4)\n_ = errs"
		if got := env.check(good); len(got) != 0 {
			t.Errorf("the corrected snippet failed: %v", got)
		}
		decls := snippet{where: "planted", file: "planted.md", line: 1,
			body: "func check(doc *Document) {}"}
		if got := env.check(decls); len(got) == 0 {
			t.Error("a declaration naming an undefined type type-checked clean")
		}
	})

	t.Run("verbatim", func(t *testing.T) {
		quote := snippet{where: "planted", verbatim: "pdfa/pdfa.go",
			body: "func checkNoEncrypt(doc *Document, level pdfa.Level) []pdfa.Violation {"}
		if checkVerbatim(t, quote) == "" {
			t.Error("a quotation of a signature the file no longer has passed")
		}
		quote.body = "func checkNoEncrypt(doc core.View, level Level) []Violation {"
		if msg := checkVerbatim(t, quote); msg != "" {
			t.Errorf("the file's own line failed: %s", msg)
		}
	})

	t.Run("spans", func(t *testing.T) {
		idx := loadDeclIndex(t)
		if err := loadModulePackageNames(); err != nil {
			t.Fatal(err)
		}
		for _, stale := range []string{
			"sortViolations",          // renamed to finding.Sort
			"pdfa.go",                 // moved to pdfa/pdfa.go
			"crypt_encrypt.go",        // exists nowhere
			"pdf0.PDFA4",              // moved to pdfa
			"pdfa.NoSuchLevel",        // never existed
			"Document.NoSuchMethod",   // never existed
			"TestNoSuchTest",          // never existed
			"WithMaxNoSuchOption",     // a removed option looks like this
			"internal/core/nofile.go", // a path that does not resolve
			"pdfa/pdfa.go:999999",     // a line past the end
		} {
			if ok, applies, _ := idx.resolveSpan("docs/planted.md", stale); ok || !applies {
				t.Errorf("`%s` resolved (applies=%v)", stale, applies)
			}
		}
		for _, live := range []string{"finding.Sort", "pdfa/pdfa.go", "pdfa.PDFA4", "Document.Locked", "TestCorpus", "WithMaxCmapWork", "MediaBox"} {
			if ok, _, why := idx.resolveSpan("docs/planted.md", live); !ok {
				t.Errorf("`%s` did not resolve: %s", live, why)
			}
		}
	})

	t.Run("commands", func(t *testing.T) {
		targets := makeTargets(t, root)
		elsewhere := resolveOtherRepoTargets(t)
		for _, cmd := range []string{
			"make fuzzz",
			"make en16931-codelistz",
			"go run ./internal/cmd/rulecoverage",
			"go test -run=NONE -fuzz=FuzzCmapSubtable .",
			"go build ./cmd/nosuchdir",
		} {
			if msg, applies := checkCommand(root, targets, elsewhere, cmd); msg == "" || !applies {
				t.Errorf("%q passed", cmd)
			}
		}
		for _, cmd := range []string{
			"make fuzz FUZZTIME=5s",
			"go run -tags devtools ./internal/cmd/rulecoverage -v",
			"go test -run=NONE -fuzz=FuzzRead -fuzztime=5m",
			"go build ./cmd/pdf0",
		} {
			if msg, _ := checkCommand(root, targets, elsewhere, cmd); msg != "" {
				t.Errorf("%q failed: %s", cmd, msg)
			}
		}
	})

	t.Run("messages", func(t *testing.T) {
		lits, docs := sourceTexts(t, root)
		if containsIn(lits, "could not read FILE: it is encrypted (supply -password)") {
			t.Error("the CLI's old password message is still found in the source")
		}
		if !containsIn(lits, "object number 0 is reserved and cannot be written") {
			t.Error("a message the code does emit was not found")
		}
		if strings.Contains(docs["Intact"], "a Document Security Store or document time-stamp added for long-term validation, say") {
			t.Error("an out-of-date godoc quotation is still in the godoc")
		}
	})

	t.Run("counts", func(t *testing.T) {
		for _, line := range []string{
			"what the 59 dispatched checks actually cover",
			"pdf0 validates a document against ten conformance standards",
			"Around forty test functions self-skip",
			"Four fuzz targets live in fuzz_test.go",
		} {
			if len(driftingCounts(line)) == 0 {
				t.Errorf("%q states a count and passed", line)
			}
		}
		for _, line := range []string{
			"running the PDF/A and PDF/UA-1 validators",
			"measured over the 2907-file corpus",
		} {
			if got := driftingCounts(line); len(got) != 0 {
				t.Errorf("%q is not a count of the code, and was flagged: %v", line, got)
			}
		}
	})

	t.Run("comments", func(t *testing.T) {
		idx := loadDeclIndex(t)
		for _, gone := range []string{"runUACheck", "validationCacheX", "forEachContentItem"} {
			if idx.all[gone] || idx.literal[gone] {
				t.Errorf("%s resolves", gone)
			}
		}
	})
}
