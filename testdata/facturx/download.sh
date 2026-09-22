#!/bin/bash
# Downloads the Factur-X / ZUGFeRD example-invoice oracle listed in sources.tsv
# (Apache-2.0: ZUGFeRD/corpus, ZUGFeRD/mustangproject) into this directory.
# The files are a local validation oracle; they are gitignored, not vendored.
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
