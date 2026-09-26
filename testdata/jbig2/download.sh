#!/bin/bash
# Downloads the JBIG2 sample PDFs listed in sources.tsv into this directory. They
# are the decode oracle for the JBIG2 decoder (the veraPDF corpus has no JBIG2
# images). The files come from the pdf.js conformance test suite (Apache-2.0):
# each bitmap-*.pdf encodes a common test image with a different JBIG2 feature
# (generic templates, MMR, symbol/text, halftone, refinement). Gitignored, not
# vendored; the manifest and this script are committed.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
# Exit non-zero if any file fails: the Makefile writes the .ok stamp only when
# this succeeds, and the tests read the stamp as "the set is complete".
rc=0
while IFS=$'\t' read -r url name; do
  case "$url" in ''|\#*) continue ;; esac
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; rc=1; fi
done < "$dir/sources.tsv"
exit $rc
