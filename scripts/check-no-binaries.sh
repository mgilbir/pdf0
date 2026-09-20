#!/bin/bash
# Fails if a compiled binary, or anything implausibly large, is tracked.
#
# Three executables — text, simple_pdf and genuse — were committed to the root
# of this repository and shipped in the v0.2.0 and v0.3.1 modules: 19.5 MB of
# ELF in a 24 MB download, 81% of what every consumer fetched. A bare
# `go build ./examples/text` drops an executable named after the package in the
# working directory, and `git add -A` took it.
#
# Nothing noticed for six weeks, because nothing was looking. This looks.
set -uo pipefail

fail=0

# An executable is never source. Checked by content rather than by the mode
# bit, because a file can be committed 644 and still be an ELF.
while IFS= read -r f; do
	[ -f "$f" ] || continue
	# od rather than a string compare: the bytes are read as hex, so a null in
	# the input is not something the shell has to carry.
	case "$(head -c 4 "$f" | od -An -tx1 | tr -d ' \n')" in
	7f454c46 | cafebabe | feedface | feedfacf | cffaedfe | cefaedfe | 4d5a*)
		printf 'binary tracked: %s (%s bytes)\n' "$f" "$(wc -c <"$f")" >&2
		fail=1
		;;
	esac
done < <(git ls-files)

# And a size ceiling, which catches the shapes the magic-number test does not:
# a committed PDF, a zip, a generated corpus. The largest legitimate file here
# is testdata/spec_examples.json at ~217 KB.
limit=$((256 * 1024))
while IFS=$'\t' read -r size path; do
	[ "$size" -gt "$limit" ] || continue
	printf 'oversized tracked file: %s (%s bytes, limit %s)\n' "$path" "$size" "$limit" >&2
	fail=1
done < <(git ls-tree -r -l HEAD | awk '{print $4"\t"$5}')

if [ "$fail" -ne 0 ]; then
	echo >&2
	echo "Nothing compiled or generated belongs in the tree. If a file genuinely" >&2
	echo "does, raise the limit here deliberately rather than around this check." >&2
	exit 1
fi
echo "no binaries tracked; nothing over $((limit / 1024)) KB ($(git ls-files | wc -l) files)"
