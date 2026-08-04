.PHONY: test cc-sweep check-docs check-mermaid check-links corpus test-corpus clean-corpus refpdfs profiles rule-coverage wtpdf clean-wtpdf arlington test-arlington clean-arlington ccitt clean-ccitt jbig2 clean-jbig2 facturx clean-facturx clean-cc bidi-tests test-bidi clean-bidi-tests hbshaping test-hbshaping

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

# Unicode's own conformance suite for the bidirectional algorithm (UAX #9), used
# as the oracle for fonts/bidi.go. BidiTest.txt is every combination of
# bidirectional character classes up to length four; BidiCharacterTest.txt is
# real character sequences, which is what brings the paired-bracket rule into
# scope. Downloaded into testdata/unicode-bidi (gitignored); not committed.
#
# The version is pinned to the one the generated tables in fonts/bidiclass.go
# were built from, because the two have to agree: a character whose class
# changed between releases would be a test failure that is really a stale table.
BIDI_DIR := testdata/unicode-bidi
UNICODE_VERSION ?= 17.0.0

bidi-tests: $(BIDI_DIR)/.ok

$(BIDI_DIR)/.ok:
	mkdir -p $(BIDI_DIR)
	curl -fsSL -o $(BIDI_DIR)/BidiTest.txt \
		https://www.unicode.org/Public/$(UNICODE_VERSION)/ucd/BidiTest.txt
	curl -fsSL -o $(BIDI_DIR)/BidiCharacterTest.txt \
		https://www.unicode.org/Public/$(UNICODE_VERSION)/ucd/BidiCharacterTest.txt
	touch $@

# The path is made absolute because the test's working directory is fonts/, not
# the repository root.
test-bidi: bidi-tests
	UNICODE_BIDI_TESTS=$(abspath $(BIDI_DIR)) go test -v -run TestBidiConformance -count=1 ./fonts/

clean-bidi-tests:
	rm -rf $(BIDI_DIR)

# Shaping checked against HarfBuzz.
#
# Unlike every other oracle here, this one is checked in: testdata/harfbuzz/
# holds the corpus and what HarfBuzz answered for it, so the comparison runs on
# any machine with a Go toolchain and nothing else. That matters because it has
# to run on every change to the shaper, and an oracle that needs the right
# Python on the machine is one that quietly stops running.
#
# This target is what regenerates it, and is needed only when the corpus grows,
# the bundled font changes, or a HarfBuzz release moves an answer. It needs
# Python with uharfbuzz:
#
#	python3 -m venv .hbenv && .hbenv/bin/pip install uharfbuzz
#	PYTHON=.hbenv/bin/python make hbshaping
#
# Review the diff to expected.txt before committing it. A change there is
# HarfBuzz changing its mind, and is worth understanding rather than accepting.
HARFBUZZ_DIR := testdata/harfbuzz
PYTHON ?= python3

hbshaping:
	$(PYTHON) $(HARFBUZZ_DIR)/corpus.py
	$(PYTHON) $(HARFBUZZ_DIR)/shape.py \
		fonts/notosans/NotoSans-Variable.ttf \
		$(HARFBUZZ_DIR)/corpus.txt \
		$(HARFBUZZ_DIR)/expected.txt

test-hbshaping:
	go test -v -run 'TestShapingAgreesWithHarfBuzz|TestTheHarfBuzzOracleHasTeeth' -count=1 ./fonts/

# The EN 16931 / CIUS validation lives in github.com/mgilbir/formalis; its oracle
# data (EN 16931 artefacts, code lists, UBL examples, XRechnung/Peppol/NLCIUS
# suites) is fetched by that module's own Makefile.
