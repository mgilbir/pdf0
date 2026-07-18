.PHONY: test corpus test-corpus clean-corpus refpdfs profiles rule-coverage wtpdf clean-wtpdf arlington test-arlington clean-arlington en16931-artefacts clean-en16931-artefacts en16931-codelists clean-en16931-codelists cius-oracles clean-cius-oracles

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

# EN 16931 UBL example invoices (CEN TC 434 eInvoicing-EN16931; OpenPEPPOL
# peppol-bis-invoice-3) used as the UBL oracle. Downloaded into
# testdata/en16931-ubl (gitignored); manifest and downloader are committed.
UBL_DIR := testdata/en16931-ubl

en16931-ubl: $(UBL_DIR)/.ok

$(UBL_DIR)/.ok: $(UBL_DIR)/sources.tsv $(UBL_DIR)/download.sh
	bash $(UBL_DIR)/download.sh
	touch $@

clean-en16931-ubl:
	rm -f $(UBL_DIR)/*.xml $(UBL_DIR)/.ok

# Official CEN/TC 434 EN 16931 supporting artefacts (ConnectingEurope/
# eInvoicing-EN16931, EUPL-1.2): the validation Schematron, code lists, and the
# per-rule unit-test suite. Cloned under testdata (gitignored) and used as a
# differential oracle for the EN 16931 rule engine and to verify the committed
# code-list tables. Not committed.
EN16931_DIR := testdata/en16931-artefacts

en16931-artefacts: $(EN16931_DIR)/.ok

$(EN16931_DIR)/.ok:
	git clone --depth 1 https://github.com/ConnectingEurope/eInvoicing-EN16931 $(EN16931_DIR)
	touch $@

clean-en16931-artefacts:
	rm -rf $(EN16931_DIR)

# Official CEN/TC 434 EN 16931 code lists (genericode + EAS/VATEX). Downloaded
# into testdata/en16931-codelists (gitignored); download.sh + gen.py are
# committed. gen.py regenerates en16931_codelists.go; the fidelity test verifies
# the committed tables against the genericode.
CODELISTS_DIR := testdata/en16931-codelists

en16931-codelists:
	bash $(CODELISTS_DIR)/download.sh
	python3 $(CODELISTS_DIR)/gen.py

clean-en16931-codelists:
	rm -rf $(CODELISTS_DIR)/genericode $(CODELISTS_DIR)/*.zip $(CODELISTS_DIR)/*.xlsx

# National CIUS oracles: KoSIT XRechnung (Schematron + instance test suite) and
# OpenPEPPOL BIS 3 (Schematron + examples). Cloned under testdata (gitignored);
# used as FP=0 oracles for the XRechnung and Peppol rule layers. Not committed.
cius-oracles:
	git clone --depth 1 https://github.com/itplr-kosit/xrechnung-schematron testdata/xrechnung/schematron
	git clone --depth 1 https://github.com/itplr-kosit/xrechnung-testsuite testdata/xrechnung/testsuite
	git clone --depth 1 https://github.com/OpenPEPPOL/peppol-bis-invoice-3 testdata/peppol/repo

clean-cius-oracles:
	rm -rf testdata/xrechnung testdata/peppol
