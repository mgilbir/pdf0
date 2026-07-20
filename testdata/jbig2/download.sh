#!/bin/bash
# Downloads the JBIG2 sample PDFs listed in sources.tsv into this directory. They
# are the decode oracle for the JBIG2 decoder (the veraPDF corpus has no JBIG2
# images). The files come from the pdf.js conformance test suite (Apache-2.0):
# each bitmap-*.pdf encodes a common test image with a different JBIG2 feature
# (generic templates, MMR, symbol/text, halftone, refinement). Gitignored, not
# vendored; the manifest and this script are committed.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
while IFS=$'\t' read -r url name; do
  [ -z "$url" ] && continue
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; fi
done < "$dir/sources.tsv"
