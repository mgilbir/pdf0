#!/usr/bin/env bash
# Render every ```mermaid block in the repository's Markdown and fail if any of
# them does not parse.
#
# The docs lean on Mermaid to carry the pipelines that prose carries badly, and a
# diagram that does not render is worse than no diagram: GitHub replaces it with
# an error box, so the reader loses the explanation AND trusts the page less.
# This is not hypothetical — the signature-verification sequence diagram shipped
# broken because a Note contained a ";", which Mermaid reads as a statement
# separator. Nothing caught it until the blocks were rendered for the first time.
#
# Requires node and npx. Renders with a pinned mermaid-cli so a new upstream
# release cannot turn CI red on its own.
#
# Usage: scripts/check-mermaid.sh [root]   (default root: the repository)
set -euo pipefail

MERMAID_CLI_VERSION="${MERMAID_CLI_VERSION:-11.16.0}"
root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Chromium cannot use its sandbox in most CI containers.
cat >"$work/puppeteer.json" <<'JSON'
{ "args": ["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"] }
JSON

# Extract each fenced mermaid block to its own file, named so a failure points
# straight at the source: <sanitised-path>#<block-index>.mmd
python3 - "$root" "$work" <<'PY'
import os, re, sys

root, work = sys.argv[1], sys.argv[2]
skip = {".git", "node_modules", "testdata", "spec", ".claude"}
blocks = os.path.join(work, "blocks")
os.makedirs(blocks, exist_ok=True)

count = 0
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d not in skip]
    for name in sorted(filenames):
        if not name.endswith(".md"):
            continue
        path = os.path.join(dirpath, name)
        rel = os.path.relpath(path, root)
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        for i, m in enumerate(re.finditer(r"```mermaid\n(.*?)```", text, re.S), 1):
            count += 1
            slug = rel.replace(os.sep, "__")
            with open(os.path.join(blocks, f"{slug}#{i}.mmd"), "w", encoding="utf-8") as out:
                out.write(m.group(1))
print(f"found {count} mermaid block(s)")
PY

shopt -s nullglob
files=("$work"/blocks/*.mmd)
if [ ${#files[@]} -eq 0 ]; then
	echo "no mermaid blocks found — nothing to check"
	exit 0
fi

# report prints the diagnosis, not the renderer's stack trace: mermaid-cli dumps
# a puppeteer backtrace on a parse failure and the one line that says what is
# actually wrong ("Parse error on line N: …") scrolls past it.
report() {
	printf 'FAIL  %s\n' "$1"
	printf '%s\n' "$2" |
		grep -iE 'parse error|^error|expecting|no diagram type detected|syntax error' |
		grep -v 'at async' | head -4 | sed 's/^/      /'
	# The offending source line follows the "Parse error" line in mermaid's output.
	printf '%s\n' "$2" | grep -A2 -i 'parse error' | tail -2 | sed 's/^/      /'
}

fail=0
for f in "${files[@]}"; do
	label="$(basename "${f%.mmd}")"
	label="${label//__//}"
	if out=$(npx --yes "@mermaid-js/mermaid-cli@${MERMAID_CLI_VERSION}" \
		-p "$work/puppeteer.json" -i "$f" -o "$work/out.svg" 2>&1) &&
		! printf '%s' "$out" | grep -qiE 'parse error|syntax error'; then
		printf 'ok    %s\n' "$label"
	else
		report "$label" "$out"
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo
	echo "A mermaid block failed to render. Common causes:"
	echo "  - a ';' inside a node or edge label (mermaid reads it as a statement separator)"
	echo "  - an unquoted label containing '(', ')', '/', ':' or ','  — wrap it: A[\"text (here)\"]"
	echo "  - a literal newline in a label — use <br/>"
	exit 1
fi
echo "all mermaid blocks render"
