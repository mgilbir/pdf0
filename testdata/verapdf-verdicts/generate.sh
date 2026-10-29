#!/bin/sh
# Writes what veraPDF says of every file in the veraPDF corpus's PDF/A levels:
# one <level directory>.txt each, veraPDF's own text report (--format text -v),
# which names every file PASS or FAIL and, under each FAIL, the rules it
# failed. TestRuleCoverageByVeraPDF judges pdf0 against these, where the
# corpus file names alone would say the wrong rule for some files.
#
# The reports are veraPDF's output over files of another project, and are
# generated here, never committed. Run through `make verapdf-verdicts`, with
# the corpus fetched (`make corpus`) and veraPDF in PDF0_VERAPDF — by default
# the Docker wrapper beside the corpus tests, testdata/verapdf/verapdf-docker.sh.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
corpus=${VERAPDF_CORPUS:-$here/../verapdf-corpus}
vera=${PDF0_VERAPDF:-$here/../verapdf/verapdf-docker.sh}
for pair in PDF_A-1b:1b PDF_A-2b:2b PDF_A-3b:3b PDF_A-4:4; do
	level=${pair%%:*}
	flavour=${pair##*:}
	if [ ! -d "$corpus/$level" ]; then
		echo "generate.sh: $corpus/$level is not there; fetch the corpus with \`make corpus\`" >&2
		exit 1
	fi
	# veraPDF exits 1 when a file fails, which most of these do; anything
	# else is veraPDF failing to run.
	status=0
	"$vera" --flavour "$flavour" --format text -v --recurse "$corpus/$level" >"$here/$level.txt.tmp" 2>"$here/$level.log" || status=$?
	if [ "$status" -gt 1 ]; then
		echo "generate.sh: veraPDF exited $status for $level; see $here/$level.log" >&2
		exit 1
	fi
	# A report that names no file is a veraPDF that did not run.
	if ! grep -q '^\(PASS\|FAIL\) ' "$here/$level.txt.tmp"; then
		echo "generate.sh: veraPDF reported nothing for $level; see $here/$level.log" >&2
		exit 1
	fi
	mv "$here/$level.txt.tmp" "$here/$level.txt"
done
