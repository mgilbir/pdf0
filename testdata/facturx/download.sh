#!/bin/bash
# Downloads the Factur-X / ZUGFeRD example-invoice oracle listed in sources.tsv
# (Apache-2.0: ZUGFeRD/corpus, ZUGFeRD/mustangproject) into this directory.
# The files are a local validation oracle; they are gitignored, not vendored.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
while IFS=$'\t' read -r url name; do
  [ -z "$url" ] && continue
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; fi
done < "$dir/sources.tsv"
