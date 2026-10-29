#!/bin/sh
# veraPDF through its official Docker image, for PDF0_VERAPDF. The arguments
# are veraPDF's; an argument that is an existing file is a document, and every
# document must be in one directory, which is mounted as /data. An argument
# that is a directory is mounted as /data itself, for --recurse; it is then the
# only path. Used by the tests that hold pdf0's output to veraPDF, and by
# `make verapdf-verdicts`.
set -eu
dir=""
for a in "$@"; do
	if [ -d "$a" ]; then
		dir=$(cd "$a" && pwd)
	elif [ -f "$a" ]; then
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
	if [ -d "$a" ]; then
		set -- "$@" "/data"
	elif [ -f "$a" ]; then
		set -- "$@" "/data/$(basename "$a")"
	else
		set -- "$@" "$a"
	fi
	i=$((i + 1))
done
exec docker run --rm -v "$dir:/data:ro" "${VERAPDF_IMAGE:-verapdf/cli:v1.30.3}" "$@"
