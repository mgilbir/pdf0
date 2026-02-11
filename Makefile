.PHONY: test test-corpus clean-corpus

CORPUS_DIR := testdata/verapdf-corpus

test:
	go test ./...

corpus: $(CORPUS_DIR)/.ok

$(CORPUS_DIR)/.ok:
	git clone --depth 1 https://github.com/veraPDF/veraPDF-corpus $(CORPUS_DIR)
	touch $@

test-corpus: corpus
	VERAPDF_CORPUS=$(CORPUS_DIR) go test -v -run TestCorpus -count=1 ./...

clean-corpus:
	rm -rf $(CORPUS_DIR)
