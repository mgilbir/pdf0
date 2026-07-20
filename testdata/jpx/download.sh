#!/bin/bash
# Downloads the JPEG 2000 sample codestreams listed in sources.tsv into this
# directory. They are the decode oracle for the JPXDecode decoder, taken from the
# OpenJPEG conformance test set (uclouvain/openjpeg-data, BSD-2-Clause). Gitignored,
# not vendored; the manifest and this script are committed.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
while IFS=$'\t' read -r url name; do
  [ -z "$url" ] && continue
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; fi
done < "$dir/sources.tsv"
