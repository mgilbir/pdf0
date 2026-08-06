.PHONY: noto-fonts clean-noto-fonts wpt test-wpt clean-wpt css-colors clean-css-colors html-entities clean-html-entities test cc-sweep check-docs check-mermaid check-links corpus test-corpus clean-corpus refpdfs profiles rule-coverage wtpdf clean-wtpdf arlington test-arlington clean-arlington ccitt clean-ccitt jbig2 clean-jbig2 facturx clean-facturx clean-cc css-tests test-css clean-css-tests bidi-tests test-bidi clean-bidi-tests bidi-tables clean-bidi-tables

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

# Noto, for the scripts the fourteen standard PDF faces do not have.
#
# Those fourteen cover Latin and nothing else, so a document with a Hebrew word
# or a kana in it gets a face that cannot encode the letters — and since the
# encoder substitutes a space for anything it cannot represent, the word is
# absent from the page rather than showing as boxes anyone would notice. The
# reftest harness hands these to the engine through FallbackFontSet.
#
# Measured against the suite: the three between them cover 81% of the characters
# the standard faces are missing and clear 64% of the documents that report one,
# against 50% for the best single font tried (DejaVu Sans) and 32% for a
# monospaced one (Cascadia Mono). Coverage per character is a poor guide —
# a document stops reporting only when *every* character it uses is covered, so
# the two commonest characters decide more than the long tail does.
#
# Licensing: all three are SIL Open Font License 1.1, which is why they were
# chosen over DejaVu Sans — it scores better on characters and is under the
# Bitstream Vera licence instead. As with Ahem, pdf0 neither vendors nor
# redistributes them: they are fetched into this gitignored directory, used only
# to run the tests, and no font bytes ship in this repository or anything it
# builds. The licence text is fetched alongside them.
#
# The Japanese face is the variable TTF and not one of the static OTFs, because
# those are CID-keyed CFF and forme does not read them. forme instantiates it at
# the font's default, which its name table reports as Thin — so CJK set through
# this fallback is lighter than it should be. It is a fallback for text that
# would otherwise be invisible, and the weight being wrong is worth saying out
# loud rather than leaving to be discovered.
NOTO_DIR := testdata/fonts-noto
NOTO_BASE := https://raw.githubusercontent.com/notofonts
NOTO_HINTED := NotoSans NotoSansHebrew NotoSansArabic NotoSansDevanagari \
               NotoSansArmenian NotoSansGeorgian

noto-fonts: $(NOTO_DIR)/.ok

$(NOTO_DIR)/.ok:
	mkdir -p $(NOTO_DIR)
	for fam in $(NOTO_HINTED); do \
	  curl -sSf -o $(NOTO_DIR)/$$fam-Regular.ttf \
	    $(NOTO_BASE)/notofonts.github.io/main/fonts/$$fam/hinted/ttf/$$fam-Regular.ttf; \
	done
	curl -sSf -o $(NOTO_DIR)/NotoSerifTibetan-Regular.ttf \
	  $(NOTO_BASE)/notofonts.github.io/main/fonts/NotoSerifTibetan/hinted/ttf/NotoSerifTibetan-Regular.ttf
	curl -sSf -o $(NOTO_DIR)/NotoSansJP-VF.ttf \
	  $(NOTO_BASE)/noto-cjk/main/Sans/Variable/TTF/Subset/NotoSansJP-VF.ttf
	curl -sSf -o $(NOTO_DIR)/OFL.txt \
	  $(NOTO_BASE)/noto-cjk/main/Sans/LICENSE
	touch $@

clean-noto-fonts:
	rm -rf $(NOTO_DIR)


# Unicode's own conformance data for the bidirectional algorithm, UAX #9, which
# internal/bidi's conformance_test.go runs in full.
#
# This is an external oracle in the sense ADR 0003 means and not a restatement of
# pdf0's own reading: BidiTest.txt is every combination of four bidirectional
# classes with the levels and the visual order the Consortium says they resolve
# to, and BidiCharacterTest.txt is the same over real code points, which is what
# exercises the bracket pairing of rule N0.
#
# Fetched rather than committed — 15 MB, versioned by Unicode — so the tests skip
# when it is absent, exactly as the veraPDF corpus and the Arlington model do.
UNICODE_VERSION ?= 17.0.0
UCD_URL         := https://www.unicode.org/Public/$(UNICODE_VERSION)/ucd
BIDI_DIR        := testdata/unicode-bidi

bidi-tests: $(BIDI_DIR)/.ok

$(BIDI_DIR)/.ok:
	mkdir -p $(BIDI_DIR)
	curl -sSf -o $(BIDI_DIR)/BidiTest.txt $(UCD_URL)/BidiTest.txt
	curl -sSf -o $(BIDI_DIR)/BidiCharacterTest.txt $(UCD_URL)/BidiCharacterTest.txt
	touch $@

test-bidi: bidi-tests
	UNICODE_BIDI_TESTS=$(CURDIR)/$(BIDI_DIR) go test -v -run 'TestBidi|TestRepresentatives' -count=1 ./internal/bidi/

clean-bidi-tests:
	rm -rf $(BIDI_DIR)

# The Unicode Character Database, which cmd/genbidi turns into the Bidi_Class,
# bracket and mirroring tables in internal/bidi/tables.go.
#
# Three files rather than one because the property needs all three:
# UnicodeData.txt is normative for assigned characters, DerivedBidiClass.txt adds
# the block defaults for unassigned ones (a code point nobody has assigned inside
# the Hebrew block still runs right to left), and BidiBrackets.txt is a property
# of its own that rule N0 needs. Rule L4's mirroring is the shaper's and its table
# is deliberately not generated — see cmd/genbidi.
#
# As with the HTML entities and the CSS colours, the generated table is committed
# and the input is not, so a checkout builds without a network. Regenerating is a
# rare errand — a new Unicode version — and the version to move to is set above.
UCD_DIR := testdata/unicode

bidi-tables:
	mkdir -p $(UCD_DIR)/extracted
	curl -sSf -o $(UCD_DIR)/UnicodeData.txt $(UCD_URL)/UnicodeData.txt
	curl -sSf -o $(UCD_DIR)/BidiBrackets.txt $(UCD_URL)/BidiBrackets.txt
	curl -sSf -o $(UCD_DIR)/extracted/DerivedBidiClass.txt $(UCD_URL)/extracted/DerivedBidiClass.txt
	go run ./cmd/genbidi -ucd $(UCD_DIR) -out internal/bidi/tables.go
	gofmt -w internal/bidi/tables.go

clean-bidi-tables:
	rm -rf $(UCD_DIR)

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
# WPT is enormous, so this is a blobless sparse clone rather than the whole of
# it. The directories are everything a page laid out *once* can be held to.
#
# What is left out is left out for a reason and not for convenience: pagination
# and page-box describe flowing content across several pages, which §2.2 decides
# against; ui and run-in are interaction and a feature CSS removed. Floats,
# positioning and z-index are emphatically *in* — they are only dynamic in a
# viewport that resizes, and this one does not.
WPT_DIR  := testdata/wpt
WPT_REF  ?= master
WPT_DIRS := css/CSS2/normal-flow css/CSS2/box-display css/CSS2/margin-padding-clear \
            css/CSS2/abspos css/CSS2/positioning css/CSS2/visuren css/CSS2/visudet \
            css/CSS2/visufx css/CSS2/floats css/CSS2/floats-clear css/CSS2/tables \
            css/CSS2/zindex css/CSS2/zorder css/CSS2/stacking-context \
            css/CSS2/linebox css/CSS2/text css/CSS2/bidi-text css/CSS2/lists \
            css/CSS2/generated-content css/CSS2/borders css/CSS2/backgrounds \
            css/CSS2/box css/CSS2/colors css/CSS2/values \
            css/CSS2/support css/CSS2/reference css/css-text/white-space css/reference \
            fonts

# "fonts" is there for Ahem.ttf, which a quarter of the suite is written
# against and which the harness hands to the engine — see render/ahem_test.go
# for why a test font is the only way those assertions can be expressed.
#
# Licensing, since it is a font and fonts often are not as free as the code
# around them: Ahem.ttf is tracked in the web-platform-tests repository, which
# is under the 3-Clause BSD licence above, and carries no separate licence of
# its own. pdf0 neither vendors nor redistributes it — it is fetched into this
# gitignored directory exactly as the rest of the corpus is, is used only to run
# the tests, and no font bytes are shipped in this repository or in anything it
# builds. The exposure is therefore the same as depending on the suite at all,
# which the ratchet already does.

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

# Shaping — the OpenType layout engine, the script-specific models and the
# font-program reader — lives in github.com/mgilbir/forme. Its oracles go with
# it: HarfBuzz's answers over a checked-in corpus, CoreText for a third opinion,
# and the generators that build the Unicode-derived tables it needs. They are run
# by that module's own Makefile.
#
# The bidirectional algorithm is the one thing on that list with a copy on each
# side, and ADR 0006 records why: forme applies it to a string to decide which way
# a run of glyphs is drawn, and internal/bidi applies it to a paragraph laid out
# across boxes, with a base direction and the CSS unicode-bidi controls. The
# second cannot be expressed in terms of the first.
#
# What stays here is what PDF does with a shaped run: writing it into a content
# stream, and writing the font into the document. testdata/shaping/corpus.txt
# is forme's corpus, kept because those two have to agree with each other over
# text that is more than a line of Latin.

# The EN 16931 / CIUS validation lives in github.com/mgilbir/formalis; its oracle
# data (EN 16931 artefacts, code lists, UBL examples, XRechnung/Peppol/NLCIUS
# suites) is fetched by that module's own Makefile.
