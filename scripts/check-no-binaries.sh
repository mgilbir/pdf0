#!/bin/bash
# Fails if a compiled binary, or anything implausibly large, is in the index.
#
# Three executables — text, simple_pdf and genuse — were committed to the root
# of this repository and shipped in the v0.2.0 and v0.3.1 modules: 19.5 MB of
# ELF in a 24 MB download, 81% of what every consumer fetched. A bare
# `go build ./examples/text` drops an executable named after the package in the
# working directory, and `git add -A` took it.
#
# Nothing noticed for six weeks, because nothing was looking. This looks.
#
# Everything is read from the index — the staged blob of each entry — so the
# check sees exactly what the next commit will contain: in CI, on a fresh
# checkout, that is HEAD; before a commit it is what was staged. An earlier
# version read file types from the working tree and sizes from HEAD, so an
# oversize file that was staged but not yet committed passed, and so did a
# staged binary whose working-tree copy had since been changed or deleted
# (audit 2026-09-22 C156).
set -uo pipefail

top=$(git rev-parse --show-toplevel) || {
	echo "check-no-binaries: not inside a git work tree" >&2
	exit 2
}
cd "$top" || exit 2

# The size ceiling catches the shapes the magic-number test does not: a
# committed PDF, a zip, a generated corpus. The largest legitimate file here is
# testdata/spec_examples.json at ~217 KB.
limit=$((256 * 1024))

fail=0
count=0
# `git ls-files -s -z` prints "<mode> <blob> <stage>\t<path>\0" for every index
# entry, including each stage of an unresolved conflict. NUL-separated, so a
# path with spaces, tabs or newlines arrives whole.
while IFS= read -r -d '' entry; do
	count=$((count + 1))
	path=${entry#*$'\t'}
	read -r mode blob _ <<<"${entry%%$'\t'*}"
	# Only regular files have content to inspect: a symlink's blob is its
	# target path, and a submodule entry is a commit, not a blob.
	case "$mode" in
	100644 | 100755) ;;
	*) continue ;;
	esac

	if ! size=$(git cat-file -s "$blob"); then
		printf 'cannot read the staged blob of %s\n' "$path" >&2
		fail=1
		continue
	fi
	if [ "$size" -gt "$limit" ]; then
		printf 'oversized file in the index: %s (%s bytes, limit %s)\n' "$path" "$size" "$limit" >&2
		fail=1
	fi

	# An executable is never source. Checked by content rather than by the
	# mode bit, because a file can be committed 644 and still be an ELF. od
	# rather than a string compare: the bytes are read as hex, so a null in
	# the input is not something the shell has to carry.
	case "$(git cat-file blob "$blob" | head -c 4 | od -An -tx1 | tr -d ' \n')" in
	7f454c46 | cafebabe | feedface | feedfacf | cffaedfe | cefaedfe | 4d5a*)
		printf 'binary in the index: %s (%s bytes)\n' "$path" "$size" >&2
		fail=1
		;;
	esac
done < <(git ls-files -s -z)

# A listing that produced nothing checked nothing; that is not a pass.
if [ "$count" -eq 0 ]; then
	echo "check-no-binaries: the index lists no files; nothing was checked" >&2
	exit 2
fi

if [ "$fail" -ne 0 ]; then
	echo >&2
	echo "Nothing compiled or generated belongs in the tree. If a file genuinely" >&2
	echo "does, raise the limit here deliberately rather than around this check." >&2
	exit 1
fi
echo "no binaries in the index; nothing over $((limit / 1024)) KB ($count entries)"
