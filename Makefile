.PHONY: test corpus test-corpus clean-corpus refpdfs

CORPUS_DIR := testdata/verapdf-corpus
REFPDF_DIR := testdata/pdf20examples

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

clean-corpus:
	rm -rf $(CORPUS_DIR)
