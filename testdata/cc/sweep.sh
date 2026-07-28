#!/bin/bash
# Sweeps a real-world PDF corpus for robustness bugs: parses every file with
# cmd/corpusprobe and quarantines anything that panics or hangs. The parser must
# return an error on malformed input, never crash and never loop, and only
# genuinely hostile input proves that — the veraPDF corpus is conformance
# fixtures, not the open web.
#
# The corpus is digitalcorpora's CC-MAIN-2021-31-PDF-UNTRUNCATED: ~8 million PDFs
# extracted from Common Crawl, served as 1000-file zip blocks of roughly 1.4 GB.
# "Untruncated" is the load-bearing word. Common Crawl's own WARC payloads are
# cut off at a few MiB, so a WARC-sourced sample is several percent truncated
# files that fail with "startxref not found" and look exactly like parser bugs
# (measured: 40 of 978, all at 5 MiB). This extraction has the complete bytes.
#
# Blocks are streamed one at a time — fetch, unzip, probe, delete — so disk stays
# bounded at about one block no matter how many are swept. Nothing is cached:
# at 1.4 GB per 1000 files, refetching is cheaper than storing.
#
# Usage: sweep.sh FIRST LAST [pipelines] [workers]
#
#   sweep.sh 4200 4211        # 12 blocks, 12,000 PDFs
#   sweep.sh 100 999          # a long soak
#
# Block numbers are arbitrary and the point is to sweep bytes nobody has swept
# before, so pick a fresh range rather than re-running one from the log.
#
# Results land in run/: per-pipeline logs, aggregate.txt, and a quarantine
# directory holding every file that panicked or timed out. Investigate a
# quarantined file with cmd/corpustime, which times each stage separately and so
# distinguishes a genuine hang from a merely slow huge file.
#
# Requires curl, unzip, and a built cmd/corpusprobe — `make cc-sweep` builds it.
set -u

[ $# -ge 2 ] || {
	grep '^#' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

FIRST=$1
LAST=$2
NPIPE=${3:-2}
WORKERS=${4:-3}

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$DIR/run"
QUAR="$ROOT/quarantine"
PROBE="${CORPUSPROBE:-$ROOT/corpusprobe}"
BASE="https://digitalcorpora.s3.amazonaws.com/corpora/files/CC-MAIN-2021-31-PDF-UNTRUNCATED/zipfiles"

mkdir -p "$QUAR"
if [ ! -x "$PROBE" ]; then
	echo "corpusprobe not found at $PROBE — run 'make cc-sweep', or set CORPUSPROBE" >&2
	exit 1
fi

# Blocks are served in directories of a thousand: 4200.zip sits under 4000-4999/.
zipurl() {
	local z=$1 n lo
	n=$((10#$z))
	lo=$(((n / 1000) * 1000))
	printf '%s/%04d-%04d/%s.zip' "$BASE" "$lo" "$((lo + 999))" "$z"
}

ALL=()
for z in $(seq "$FIRST" "$LAST"); do ALL+=("$(printf '%04d' "$z")"); done

pipeline() {
	local idx=$1
	local work="$ROOT/p$idx"
	local log="$ROOT/p$idx.log"
	mkdir -p "$work"
	: >"$log"
	local i=$idx
	while [ "$i" -lt ${#ALL[@]} ]; do
		local z=${ALL[$i]}
		i=$((i + NPIPE))
		if ! curl -sfL --retry 2 "$(zipurl "$z")" -o "$work/$z.zip" 2>/dev/null; then
			echo "[$z] FETCH_FAIL" >>"$log"
			continue
		fi
		rm -rf "$work/pdfs"
		mkdir -p "$work/pdfs"
		unzip -q -o "$work/$z.zip" -d "$work/pdfs" 2>/dev/null
		rm -f "$work/$z.zip"
		local n
		n=$(find "$work/pdfs" -name '*.pdf' | wc -l)

		# GOMEMLIMIT stops one pathological file taking the machine with it; the
		# probe's own per-file timeout stops a slow one stalling the run.
		local out
		out=$(TMPDIR="$work" GOMEMLIMIT=3GiB "$PROBE" "$work/pdfs" "$WORKERS" 2>&1)

		local ok err pan tmo
		ok=$(echo "$out" | awk '/^  ok /{print $2}')
		err=$(echo "$out" | awk '/^  error /{print $2}')
		pan=$(echo "$out" | awk '/^  panic /{print $2}')
		tmo=$(echo "$out" | awk '/^  timeout /{print $2}')
		echo "[$z] files=$n ok=${ok:-0} err=${err:-0} panic=${pan:-0} timeout=${tmo:-0}" >>"$log"
		echo "$out" | awk '/^=== TOP ERROR GROUPS/,/^=== PANICS/' | grep -E '^ +[0-9]+ ' >>"$ROOT/errgroups.txt"

		# Keep the evidence: a panic or a hang is a bug, and the file is the repro.
		if [ -f "$work/corpusprobe-failures.tsv" ]; then
			awk -F'\t' '$2=="panic"||$2=="timeout"{print}' "$work/corpusprobe-failures.tsv" |
				while IFS=$'\t' read -r path kind detail; do
					[ -f "$path" ] || continue
					cp "$path" "$QUAR/${z}-${kind}-$(basename "$path")"
					echo "[$z] QUARANTINED $kind $(basename "$path") :: ${detail:0:140}" >>"$log"
				done
		fi
		rm -rf "$work/pdfs"
	done
	echo "PIPE $idx DONE" >>"$log"
}

echo "sweeping blocks $FIRST-$LAST ($NPIPE pipelines, $WORKERS workers per probe)"
: >"$ROOT/errgroups.txt"
for p in $(seq 0 $((NPIPE - 1))); do pipeline "$p" & done
wait

{
	echo "=== blocks $FIRST-$LAST ==="
	awk -F'[= ]' '/files=/{f+=$3; o+=$5; e+=$7; p+=$9; t+=$11}
	     END{printf "files=%d ok=%d error=%d panic=%d timeout=%d\n", f, o, e, p, t}' "$ROOT"/p*.log
	echo
	echo "=== error groups ==="
	awk '{n=$1; $1=""; sub(/^ +/,""); c[$0]+=n} END{for (k in c) print c[k], k}' "$ROOT/errgroups.txt" | sort -rn | head -15
	echo
	echo "=== quarantined — panics and hangs are bugs ==="
	ls -1 "$QUAR" 2>/dev/null | sed 's/^/  /'
} | tee "$ROOT/aggregate.txt"
