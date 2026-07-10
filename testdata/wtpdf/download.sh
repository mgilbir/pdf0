#!/bin/bash
# Download the Well Tagged PDF / PDF/UA-2 example documents listed in
# sources.tsv from Google Drive. Idempotent: skips files already present and
# verified as PDFs. Run via `make wtpdf` or directly from this directory.
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
TSV="$DIR/sources.tsv"

# download_gdrive <file-id> <output-path>
# Handles Google Drive's "can't scan for viruses" confirmation interstitial
# that appears for larger files.
download_gdrive() {
  local id="$1" out="$2"
  local cookie page
  cookie=$(mktemp)
  page=$(mktemp)
  curl -sL -c "$cookie" "https://drive.usercontent.google.com/download?id=${id}&export=download" -o "$page"
  if [ "$(head -c4 "$page")" = "%PDF" ]; then
    mv "$page" "$out"
    rm -f "$cookie"
    return 0
  fi
  local confirm uuid
  confirm=$(grep -oE 'name="confirm" value="[^"]+"' "$page" | sed -E 's/.*value="([^"]+)".*/\1/' | head -1)
  uuid=$(grep -oE 'name="uuid" value="[^"]+"' "$page" | sed -E 's/.*value="([^"]+)".*/\1/' | head -1)
  curl -sL -b "$cookie" -o "$out" \
    "https://drive.usercontent.google.com/download?id=${id}&export=download&confirm=${confirm}&uuid=${uuid}"
  rm -f "$cookie" "$page"
}

rc=0
while IFS=$'\t' read -r id name; do
  case "$id" in ''|\#*) continue ;; esac
  out="$DIR/$name"
  if [ -f "$out" ] && [ "$(head -c4 "$out")" = "%PDF" ]; then
    echo "  ok (cached)  $name"
    continue
  fi
  echo "  downloading  $name ..."
  download_gdrive "$id" "$out"
  if [ "$(head -c4 "$out" 2>/dev/null)" = "%PDF" ]; then
    echo "               $(stat -c %s "$out") bytes"
  else
    echo "  FAILED       $name (not a PDF; Drive may require a confirm token change)"
    rm -f "$out"
    rc=1
  fi
done < "$TSV"
exit $rc
