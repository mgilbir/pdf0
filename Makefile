.PHONY: test corpus test-corpus clean-corpus refpdfs profiles rule-coverage wtpdf clean-wtpdf arlington test-arlington clean-arlington

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

test:
	go test ./...

# Reference PDF 2.0 files for the round-trip tests.
refpdfs: $(REFPDF_DIR)/.ok

$(REFPDF_DIR)/.ok:
	git clone --depth 1 https://github.com/pdf-association/pdf20examples $(REFPDF_DIR)
	touch $@

corpus: $(CORPUS_DIR)/.ok

$(CORPUS_DIR)/.ok:
	git clone --depth 1 https://github.com/veraPDF/veraPDF-corpus $(CORPUS_DIR)
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
	git clone --depth 1 https://github.com/pdf-association/arlington-pdf-model $(ARLINGTON_DIR)
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
