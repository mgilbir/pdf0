#!/bin/sh
# veraPDF through its official Docker image, for PDF0_VERAPDF. The arguments
# are veraPDF's; an argument that is an existing file is a document, and every
# document must be in one directory, which is mounted as /data. Used by
# TestType3DocumentsAreValidForVeraPDF.
set -eu
dir=""
for a in "$@"; do
	if [ -f "$a" ]; then
		dir=$(dirname "$a")
	fi
done
if [ -z "$dir" ]; then
	exec docker run --rm "${VERAPDF_IMAGE:-verapdf/cli:v1.30.3}" "$@"
fi
# Rewrite each document to its path under /data, keeping the order.
n=$#
i=0
while [ "$i" -lt "$n" ]; do
	a=$1
	shift
	if [ -f "$a" ]; then
		set -- "$@" "/data/$(basename "$a")"
	else
		set -- "$@" "$a"
	fi
	i=$((i + 1))
done
exec docker run --rm -v "$dir:/data:ro" "${VERAPDF_IMAGE:-verapdf/cli:v1.30.3}" "$@"
