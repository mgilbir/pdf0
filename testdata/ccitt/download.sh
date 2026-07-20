#!/bin/bash
# Downloads the real-world CCITTFaxDecode sample PDFs listed in sources.tsv into
# this directory. They serve as a decode oracle for the Group 3/4 fax decoder,
# since the veraPDF corpus contains no CCITT images. Sources are permissively
# licensed (pdf.js Apache-2.0, PyPDF4 BSD); the files are gitignored, not vendored.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
while IFS=$'\t' read -r url name; do
  [ -z "$url" ] && continue
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; fi
done < "$dir/sources.tsv"
