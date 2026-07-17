#!/bin/bash
# Downloads the official CEN/TC 434 EN 16931 code lists (the "Registry of
# supporting artefacts to implement EN16931" on the EU eInvoicing site) into this
# directory and unpacks the genericode bundle. These are the authoritative,
# versioned machine-readable code lists; they are gitignored, not vendored. The
# committed Go tables (en16931_codelists.go, facturx_units.go) are generated from
# them by gen.py, and en16931_codelists_test.go verifies the tables still match.
set -u
dir="$(cd "$(dirname "$0")" && pwd)"
base="https://ec.europa.eu/digital-building-blocks/sites/download/attachments/467108974"
curl -sfL -o "$dir/digital-genericodes.zip" "$base/digital-genericodes-2026-05-15.zip?version=2&modificationDate=1776349554309&api=v2" && echo "  genericode zip"
curl -sfL -o "$dir/EAS.xlsx" "$base/Electronic%20Address%20Scheme%20Code%20list%20-%20version%2016%20-%20published%20Mar2026.xlsx?version=1&modificationDate=1774529409108&api=v2" && echo "  EAS xlsx"
curl -sfL -o "$dir/VATEX.xlsx" "$base/VAT%20Exemption%20Reason%20Code%20list%20VATEX%20-%20version%208.xlsx?version=1&modificationDate=1761226199796&api=v2" && echo "  VATEX xlsx"
rm -rf "$dir/genericode" && unzip -o -q "$dir/digital-genericodes.zip" -d "$dir/genericode" && echo "  unpacked genericode/"
