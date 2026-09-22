#!/bin/bash
# Downloads the real-world CCITTFaxDecode sample PDFs listed in sources.tsv into
# this directory. They serve as a decode oracle for the Group 3/4 fax decoder,
# since the veraPDF corpus contains no CCITT images. Sources are permissively
# licensed (pdf.js Apache-2.0, PyPDF4 BSD); the files are gitignored, not vendored.
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
