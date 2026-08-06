.PHONY: wpt test-wpt clean-wpt css-colors clean-css-colors html-entities clean-html-entities test cc-sweep check-docs check-mermaid check-links corpus test-corpus clean-corpus refpdfs profiles rule-coverage wtpdf clean-wtpdf arlington test-arlington clean-arlington ccitt clean-ccitt jbig2 clean-jbig2 facturx clean-facturx clean-cc css-tests test-css clean-css-tests

CORPUS_DIR := testdata/verapdf-corpus
REFPDF_DIR := testdata/pdf20examples
# Arlington PDF Model (Apache-2.0, PDF Association): a machine-readable grammar of
# the ISO 32000 object model. Cloned under testdata (gitignored) and used by the
# structural oracle test to verify pdf0's parser and serializer represent objects
# faithfully (right types, keys, structure). Not committed.
ARLINGTON_DIR := testdata/arlington-pdf-model
# Well Tagged PDF / PDF/UA-2 example documents by the LaTeX Project
# (github.com/latex3/tagging-project/discussions/72). Downloaded from Google
# Drive into testdata/wtpdf (gitignored); the id->name manifest and downloader
# are committed so the set is reproducible.
WTPDF_DIR := testdata/wtpdf
# veraPDF validation profiles (CC BY 4.0, veraPDF Consortium) — a machine-readable
# inventory of every PDF/A rule. Cloned under spec/ (gitignored) for local use as
# a coverage reference; not committed.
PROFILES_DIR := spec/verapdf-profiles

# Pinned corpus revisions.
#
# The ratchet baselines in pdfa_test.go and arlington_test.go are measurements of
# specific documents, so the corpora are fetched at a fixed commit rather than at
# whatever their default branch holds today. Two things follow: a corpus that
# gains or loses a file upstream cannot silently move a baseline, and CI's cache
# key is stable, so the fetch happens once instead of on every run. Override to
# try a newer revision, then update the baselines it moves in the same change.
VERAPDF_CORPUS_REF ?= 49de56cd987929932c9e4fbbbe67d052bf44ef83
ARLINGTON_REF      ?= 3a7cde314d083e4c6d78d6782334b7409d3889f7
REFPDF_REF         ?= c20f2c17bfcc4baab7cfe62e70fae64caf14d5fa
CSS_TESTS_REF      ?= 203ce36bffd617db7f118c551e32794561fb273d

# shallow_at fetches exactly one commit of one repository: no history, no other
# branches. $(1) directory, $(2) URL, $(3) commit.
define shallow_at
	rm -rf $(1)
	git init -q $(1)
	git -C $(1) remote add origin $(2)
	git -C $(1) fetch -q --depth 1 origin $(3)
	git -C $(1) checkout -q FETCH_HEAD
endef

test:
	go test ./...

# Both documentation checks.
check-docs: check-links check-mermaid

# Check every relative Markdown link, including "#fragment" anchors. Moving a
# section between docs leaves the file existing and the anchor dangling, and
# GitHub silently scrolls to the top rather than erroring — so the reader lands
# on the wrong page. Pure python3, no network.
check-links:
	./scripts/check-links.sh

# Render every ```mermaid block in the Markdown and fail if one does not parse.
# The docs use Mermaid for the pipelines prose carries badly, and GitHub replaces
# a broken block with an error box — so a silent syntax error costs the reader the
# explanation. Needs node/npx; the mermaid-cli version is pinned in the script.
check-mermaid:
	./scripts/check-mermaid.sh

# Reference PDF 2.0 files for the round-trip tests.
refpdfs: $(REFPDF_DIR)/.ok

$(REFPDF_DIR)/.ok:
	$(call shallow_at,$(REFPDF_DIR),https://github.com/pdf-association/pdf20examples,$(REFPDF_REF))
	touch $@

corpus: $(CORPUS_DIR)/.ok

$(CORPUS_DIR)/.ok:
	$(call shallow_at,$(CORPUS_DIR),https://github.com/veraPDF/veraPDF-corpus,$(VERAPDF_CORPUS_REF))
	touch $@

test-corpus: corpus
	VERAPDF_CORPUS=$(CORPUS_DIR) go test -v -run TestCorpus -count=1 ./...

profiles: $(PROFILES_DIR)/.ok

$(PROFILES_DIR)/.ok:
	git clone --depth 1 https://github.com/veraPDF/veraPDF-validation-profiles $(PROFILES_DIR)
	touch $@

# Report which veraPDF PDF/A rules this validator covers (needs `make profiles`).
rule-coverage: profiles
	VERAPDF_PROFILES=$(PROFILES_DIR) go run ./cmd/rulecoverage

# Download the LaTeX Project's Well Tagged PDF / PDF/UA-2 example documents.
wtpdf: $(WTPDF_DIR)/.ok

$(WTPDF_DIR)/.ok: $(WTPDF_DIR)/sources.tsv $(WTPDF_DIR)/download.sh
	bash $(WTPDF_DIR)/download.sh
	touch $@

arlington: $(ARLINGTON_DIR)/.ok

$(ARLINGTON_DIR)/.ok:
	$(call shallow_at,$(ARLINGTON_DIR),https://github.com/pdf-association/arlington-pdf-model,$(ARLINGTON_REF))
	touch $@

# Check pdf0's parser/serializer represent objects faithfully against the
# Arlington grammar. With the corpus present it also runs the broad parse-check
# over the veraPDF conformant (-pass-) files.
test-arlington: arlington refpdfs
	ARLINGTON_MODEL=$(ARLINGTON_DIR)/tsv/2.0 go test -v -run TestArlington -count=1 ./...

clean-arlington:
	rm -rf $(ARLINGTON_DIR)

clean-corpus:
	rm -rf $(CORPUS_DIR)

clean-wtpdf:
	rm -f $(WTPDF_DIR)/*.pdf $(WTPDF_DIR)/.ok

# Factur-X / ZUGFeRD example invoices (Apache-2.0) used as the Factur-X
# validator's oracle. Downloaded into testdata/facturx (gitignored); the source
# manifest and downloader are committed so the set is reproducible.
FACTURX_DIR := testdata/facturx

facturx: $(FACTURX_DIR)/.ok

$(FACTURX_DIR)/.ok: $(FACTURX_DIR)/sources.tsv $(FACTURX_DIR)/download.sh
	bash $(FACTURX_DIR)/download.sh
	touch $@

clean-facturx:
	rm -f $(FACTURX_DIR)/*.pdf $(FACTURX_DIR)/.ok

# Sweep real-world PDFs from the Common Crawl untruncated extraction for parser
# panics and hangs — the one oracle here made of input nobody designed. Blocks
# are streamed and deleted, so this needs ~1.4 GB of disk, not the corpus.
# Override the range: make cc-sweep FIRST=100 LAST=199
FIRST ?= 4200
LAST  ?= 4203

cc-sweep:
	mkdir -p testdata/cc/run
	go build -o testdata/cc/run/corpusprobe ./cmd/corpusprobe
	testdata/cc/sweep.sh $(FIRST) $(LAST)

clean-cc:
	rm -rf testdata/cc/run

# CSS parsing tests (CC0, Simon Sapin): implementation-independent expected
# outputs for the algorithms of CSS Syntax Level 3, one JSON file per algorithm.
#
# This is the css package's external oracle, and the framing matters — see
# docs/adr/0003-arlington-as-parser-oracle.md for the two attempts this
# repository scrapped for guarding nothing. These expectations were written by
# someone else, from the specification, and three independent parsers
# (tinycss2, rust-cssparser, Crass) are checked against them. So a disagreement
# is evidence about pdf0 rather than a restatement of pdf0's own reading.
#
# Cloned under testdata (gitignored); tests skip if absent, mirroring `make
# corpus` and `make arlington`.
CSS_TESTS_DIR := testdata/css-parsing-tests

css-tests: $(CSS_TESTS_DIR)/.ok

$(CSS_TESTS_DIR)/.ok:
	$(call shallow_at,$(CSS_TESTS_DIR),https://github.com/SimonSapin/css-parsing-tests,$(CSS_TESTS_REF))
	touch $@

# The path is absolute because `go test ./css/` runs with the package directory
# as its working directory, not the repository root.
test-css: css-tests
	CSS_PARSING_TESTS=$(CURDIR)/$(CSS_TESTS_DIR) go test -v -run TestCSSOracle -count=1 ./css/

clean-css-tests:
	rm -rf $(CSS_TESTS_DIR)

# The HTML standard's own list of named character references, which
# cmd/genhtmlentities turns into html/entities.go. The *generated table* is
# committed and the input is not, on the arrangement the font tables used before
# they moved to forme: the table is part of the source, and re-deriving it needs
# the network, so a checkout builds without one.
#
# Regenerate after the standard adds a name — which it has not done in years, so
# this is a rare errand rather than part of a build.
HTML_ENTITIES := testdata/html/entities.json

html-entities:
	mkdir -p $(dir $(HTML_ENTITIES))
	curl -sSf -o $(HTML_ENTITIES) https://html.spec.whatwg.org/entities.json
	go run ./cmd/genhtmlentities -in $(HTML_ENTITIES) -out html/entities.go
	gofmt -w html/entities.go

clean-html-entities:
	rm -f $(HTML_ENTITIES)

# The CSS Color 4 specification's named-colour table, which cmd/gencolors turns
# into style/colors.go.
#
# The source is the specification's own Bikeshed document and *not* the CSS
# parsing tests, which hold the same 148 mappings: generating the table from the
# suite that checks it would make that check circular, proving only that a file
# round-trips through a generator. As with the HTML entities, the generated table
# is committed and the input is not.
CSS_COLOR_SPEC := testdata/css-color-4.bs

css-colors:
	mkdir -p $(dir $(CSS_COLOR_SPEC))
	curl -sSf -o $(CSS_COLOR_SPEC) https://raw.githubusercontent.com/w3c/csswg-drafts/main/css-color-4/Overview.bs
	go run ./cmd/gencolors -in $(CSS_COLOR_SPEC) -out style/colors.go
	gofmt -w style/colors.go

clean-css-colors:
	rm -f $(CSS_COLOR_SPEC)

# W3C Web Platform Tests: the external oracle for the layout engine.
#
# A CSS reftest is a pair of documents with the assertion *these two render
# identically*, and the pair and the claim come from the CSS Working Group. That
# is what makes it an oracle rather than a restatement of pdf0's own reading —
# ADR 0003 records what this repository already learned about the difference.
# Reftests are also built so that the two documents reach the same rendering by
# *different* mechanisms, so an engine bug usually moves one and not the other.
#
# No browser is needed: pdf0 renders both and compares its own display lists.
#
# WPT is enormous, so this is a blobless sparse clone of the directories whose
# tests exercise what the engine currently does. Widen WPT_DIRS as more lands.
WPT_DIR  := testdata/wpt
WPT_REF  ?= master
WPT_DIRS := css/CSS2/normal-flow css/CSS2/box-display css/CSS2/margin-padding-clear \
            css/css-text/white-space css/reference css/CSS2/reference

wpt: $(WPT_DIR)/.ok

$(WPT_DIR)/.ok:
	rm -rf $(WPT_DIR)
	git clone --filter=blob:none --sparse --depth 1 \
		https://github.com/web-platform-tests/wpt.git $(WPT_DIR)
	git -C $(WPT_DIR) sparse-checkout set $(WPT_DIRS)
	touch $@

test-wpt: wpt
	WPT_TESTS=$(CURDIR)/$(WPT_DIR) go test -v -run TestWPT -count=1 ./render/

clean-wpt:
	rm -rf $(WPT_DIR)

# Real-world CCITTFaxDecode sample PDFs (pdf.js Apache-2.0, PyPDF4 BSD) used as
# the decode oracle for the Group 3/4 fax decoder. Downloaded into
# testdata/ccitt (gitignored); the source manifest and downloader are committed.
CCITT_DIR := testdata/ccitt

ccitt: $(CCITT_DIR)/.ok

$(CCITT_DIR)/.ok: $(CCITT_DIR)/sources.tsv $(CCITT_DIR)/download.sh
	bash $(CCITT_DIR)/download.sh
	touch $@

clean-ccitt:
	rm -f $(CCITT_DIR)/*.pdf $(CCITT_DIR)/.ok

# JBIG2 sample PDFs (pdf.js conformance suite, Apache-2.0) used as the decode
# oracle for the JBIG2 decoder. Downloaded into testdata/jbig2 (gitignored); the
# source manifest and downloader are committed.
JBIG2_DIR := testdata/jbig2

jbig2: $(JBIG2_DIR)/.ok

$(JBIG2_DIR)/.ok: $(JBIG2_DIR)/sources.tsv $(JBIG2_DIR)/download.sh
	bash $(JBIG2_DIR)/download.sh
	touch $@

clean-jbig2:
	rm -f $(JBIG2_DIR)/*.pdf $(JBIG2_DIR)/.ok

# Shaping — the OpenType layout engine, the bidirectional algorithm, the
# script-specific models and the font-program reader — lives in
# github.com/mgilbir/forme. Its oracles go with it: HarfBuzz's answers over a
# checked-in corpus, Unicode's own UAX #9 conformance suite, CoreText for a
# third opinion, and the generators that build the Unicode-derived tables. They
# are run by that module's own Makefile.
#
# What stays here is what PDF does with a shaped run: writing it into a content
# stream, and writing the font into the document. testdata/shaping/corpus.txt
# is forme's corpus, kept because those two have to agree with each other over
# text that is more than a line of Latin.

# The EN 16931 / CIUS validation lives in github.com/mgilbir/formalis; its oracle
# data (EN 16931 artefacts, code lists, UBL examples, XRechnung/Peppol/NLCIUS
# suites) is fetched by that module's own Makefile.
