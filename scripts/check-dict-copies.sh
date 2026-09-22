#!/bin/bash
# Fails if the module copies an object.Dictionary by value where nobody said
# the copy was intended.
#
# A Dictionary refers to its entries the way a map does, so a copy of one — a
# Stream copied by value, `x := *d`, a Dictionary passed or returned by value —
# shares them with the original. Code written expecting a snapshot mutates the
# original instead, with nothing to see at the call site. (Before the storage
# became private a copy shared some of its state and not the rest, which was
# worse; ADR 0008 has the history.)
#
# Built with the dictcopycheck tag, Dictionary carries a marker that go vet's
# copylocks analyzer reports on every copy, including copies of the structs
# that hold one (Stream, Document). A copy that is meant — moving a freshly
# built dictionary into place, the validators' shallow per-run Document — says
# so with a "dictcopy:" comment on the reported line, giving the reason. Any
# other report fails this check.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

out=$(go vet -tags dictcopycheck -copylocks ./... 2>&1)
status=$?

fail=0
reported=0
while IFS= read -r line; do
	case "$line" in
	'#'* | '') continue ;;
	esac
	# path:line:col: message
	if [[ "$line" =~ ^([^:]+\.go):([0-9]+):[0-9]+:\ (.*)$ ]]; then
		file=${BASH_REMATCH[1]}
		n=${BASH_REMATCH[2]}
		msg=${BASH_REMATCH[3]}
		reported=$((reported + 1))
		if sed -n "${n}p" -- "$file" | grep -q 'dictcopy:'; then
			continue
		fi
		printf '%s:%s: %s\n' "$file" "$n" "${msg%%: github.com/*}" >&2
		fail=1
	else
		# Anything else is vet failing to run (a type error), which must not
		# pass as "no copies found".
		printf 'go vet: %s\n' "$line" >&2
		fail=1
	fi
done <<<"$out"

if [ "$status" -ne 0 ] && [ "$reported" -eq 0 ] && [ "$fail" -eq 0 ]; then
	echo "go vet failed without reporting a copy:" >&2
	echo "$out" >&2
	fail=1
fi

if [ "$fail" -ne 0 ]; then
	echo "" >&2
	echo "Unannotated Dictionary copies. Use Clone() for an independent copy, object.NewStream" >&2
	echo "to put a dictionary into a stream, or a pointer; if the copy is intended, say why" >&2
	echo "with a \"dictcopy:\" comment on that line." >&2
	exit 1
fi
echo "no unannotated Dictionary copies ($reported annotated)"
