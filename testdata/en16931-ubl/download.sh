#!/bin/bash
# Downloads the EN 16931 UBL example-invoice oracle listed in sources.tsv
# (CEN TC 434 eInvoicing-EN16931; OpenPEPPOL peppol-bis-invoice-3) into this directory.
# The files are a local validation oracle; they are gitignored, not vendored.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
while IFS=$'\t' read -r url name; do
  [ -z "$url" ] && continue
  if curl -sfL "$url" -o "$dir/$name"; then echo "  $name"; else echo "  FAIL $name"; fi
done < "$dir/sources.tsv"
