#!/usr/bin/env bash
# Check every relative Markdown link in the repository: that the target file
# exists, and that a "#fragment" resolves to a heading in it.
#
# The fragment half is the part that actually rots. Moving a section between
# documents leaves the file existing and the link pointing at a heading that is
# no longer there, and GitHub silently scrolls to the top instead of erroring —
# so the reader lands on the wrong page and blames themselves. That is how
# CONTRIBUTING.md ended up pointing at architecture.md#where-the-rules-live after
# the rules table moved to validators.md.
#
# Pure python3, no dependencies, no network.
#
# Usage: scripts/check-links.sh [root]
set -euo pipefail
root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

python3 - "$root" <<'PY'
import os, re, sys

root = sys.argv[1]
skip = {".git", "node_modules", "testdata", "spec", ".claude"}


def slugify(heading):
    """Approximate GitHub's heading -> anchor rule."""
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", heading)  # links -> their text
    text = re.sub(r"[`*_]", "", text)                        # inline formatting
    text = text.strip().lower()
    text = re.sub(r"[^\w\s-]", "", text)                     # punctuation dropped
    return re.sub(r"\s", "-", text)


docs = []
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d not in skip]
    docs += [os.path.join(dirpath, f) for f in filenames if f.endswith(".md")]

anchors = {}
for path in docs:
    seen, found = {}, set()
    with open(path, encoding="utf-8") as fh:
        fenced = False
        for line in fh:
            if line.startswith("```"):
                fenced = not fenced
                continue
            if fenced:
                continue
            m = re.match(r"^#{1,6}\s+(.*?)\s*$", line)
            if not m:
                continue
            base = slugify(m.group(1))
            n = seen.get(base, 0)
            seen[base] = n + 1
            found.add(base if n == 0 else f"{base}-{n}")
    anchors[os.path.normpath(path)] = found

bad = 0
for path in sorted(docs):
    rel = os.path.relpath(path, root)
    with open(path, encoding="utf-8") as fh:
        body = fh.read()
    # Strip fenced and inline code before looking for links: a link-shaped string
    # inside `code` is prose about links, not a link.
    body = re.sub(r"```.*?```", "", body, flags=re.S)
    body = re.sub(r"`[^`\n]*`", "", body)
    for m in re.finditer(r"\]\(([^)\s]+)\)", body):
        link = m.group(1)
        if link.startswith(("http://", "https://", "mailto:")):
            continue
        target, _, frag = link.partition("#")
        resolved = (
            os.path.normpath(os.path.join(os.path.dirname(path), target))
            if target
            else os.path.normpath(path)
        )
        if not os.path.exists(resolved):
            print(f"  MISSING FILE   {rel} -> {link}")
            bad += 1
            continue
        if frag and resolved in anchors and frag not in anchors[resolved]:
            print(f"  BAD ANCHOR     {rel} -> {link}")
            bad += 1

if bad:
    print(f"\n{bad} broken link(s). A '#fragment' must match a heading in the "
          f"target file, lower-cased with spaces as hyphens and punctuation dropped.")
    sys.exit(1)
print(f"all relative links and anchors resolve ({len(docs)} markdown files)")
PY
