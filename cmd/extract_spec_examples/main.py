#!/usr/bin/env python3
"""
Extract ALL examples from ISO 32000-2:2020 (PDF 2.0 spec).

Uses pdftotext -layout output to find every EXAMPLE block,
tracking current section and page number.

Output: JSON array of examples with section, page, content.
"""

import json
import re
import sys


def detect_page_number(line):
    """Try to detect a page number from header/footer lines."""
    stripped = line.strip()
    m = re.match(r'^(\d+)\s{10,}©\s*ISO\s*2020', stripped)
    if m:
        return int(m.group(1))
    m = re.match(r'^©\s*ISO\s*2020.*\s(\d+)$', stripped)
    if m:
        return int(m.group(1))
    return None


def is_page_header_footer(line):
    """Check if a line is a page header, footer, or separator."""
    stripped = line.strip()
    if not stripped:
        return False
    if re.match(r'^ISO 32000-2:2020\(E\)$', stripped):
        return True
    if re.match(r'^©\s*ISO\s*2020', stripped):
        return True
    if re.match(r'^\d+\s{10,}©\s*ISO\s*2020', stripped):
        return True
    if re.match(r'^Sold by the PDF Association', stripped):
        return True
    if re.match(r'^Single user only', stripped):
        return True
    return False


def is_section_heading(line):
    """Check if line is a section heading like '7.3.2 Boolean objects'."""
    stripped = line.strip()
    # Must have at least one dot in the number
    m = re.match(r'^(\d+(?:\.\d+)+)\s+([A-Z][A-Za-z].*)', stripped)
    if m:
        section_num = m.group(1)
        parts = section_num.split('.')
        if 1 <= int(parts[0]) <= 14:
            leading_spaces = len(line) - len(line.lstrip())
            if leading_spaces < 8:
                # Clean up: remove "Goto errata" and extra whitespace
                title = re.sub(r'\s+Goto errata.*$', '', m.group(0))
                title = re.sub(r'\s+', ' ', title).strip()
                return title
    # Also check for top-level sections (just a single number)
    m = re.match(r'^(\d+)\s+([A-Z][a-z][\w\s,]+)$', stripped)
    if m:
        num = int(m.group(1))
        if 1 <= num <= 14:
            leading_spaces = len(line) - len(line.lstrip())
            if leading_spaces < 4:
                return m.group(0).strip()
    return None


def extract_examples(text_file):
    """Extract all examples from pdftotext -layout output."""
    with open(text_file, 'r', encoding='utf-8', errors='replace') as f:
        lines = f.readlines()

    examples = []
    current_section = "Unknown"
    current_page = 0

    example_re = re.compile(r'^EXAMPLE(?:\s+(\d+))?\s*(.*?)$')

    i = 0
    while i < len(lines):
        line = lines[i].rstrip('\n')
        stripped = line.strip()

        pn = detect_page_number(line)
        if pn is not None:
            current_page = pn

        if is_page_header_footer(line):
            i += 1
            continue

        heading = is_section_heading(line)
        if heading:
            current_section = heading

        m = example_re.match(stripped)
        if m:
            example_num = m.group(1) if m.group(1) else ""
            description = m.group(2).strip() if m.group(2) else ""
            example_page = current_page
            example_section = current_section

            content_lines = []
            i += 1
            had_blank = False
            collecting_started = False

            while i < len(lines):
                cline = lines[i].rstrip('\n')
                cstripped = cline.strip()

                cpn = detect_page_number(cline)
                if cpn is not None:
                    current_page = cpn

                if is_page_header_footer(cline):
                    i += 1
                    continue

                if not collecting_started and not cstripped:
                    i += 1
                    continue

                if cstripped:
                    collecting_started = True

                if example_re.match(cstripped):
                    break
                if is_section_heading(cline):
                    break
                if re.match(r'^NOTE\b', cstripped):
                    break
                if re.match(r'^Table\s+\d+\s+[—–-]', cstripped):
                    break

                if not cstripped:
                    if had_blank:
                        break
                    had_blank = True
                    content_lines.append("")
                    i += 1
                    continue

                if had_blank:
                    had_blank = False
                    leading = len(cline) - len(cline.lstrip())
                    is_pdf_syntax = bool(re.match(r'^[/\[<({\d%]', cstripped))

                    if leading < 4 and not is_pdf_syntax:
                        while content_lines and not content_lines[-1].strip():
                            content_lines.pop()
                        break

                content_lines.append(cline.rstrip())
                had_blank = False
                i += 1

            while content_lines and not content_lines[-1].strip():
                content_lines.pop()

            # Strip common leading whitespace
            if content_lines:
                non_empty = [l for l in content_lines if l.strip()]
                if non_empty:
                    min_indent = min(len(l) - len(l.lstrip()) for l in non_empty)
                    content_lines = [l[min_indent:] if len(l) > min_indent else l.strip() for l in content_lines]

            content = '\n'.join(content_lines)
            content = fix_extraction_artifacts(content)

            if content.strip():
                examples.append({
                    "page": example_page,
                    "section": example_section,
                    "example_num": example_num,
                    "description": description,
                    "content": content,
                })
        else:
            i += 1

    return examples


def fix_extraction_artifacts(content):
    """Fix common text extraction artifacts in PDF examples."""
    content = fix_wrapped_comments(content)
    content = fix_unbalanced_dicts(content)
    return content


def fix_wrapped_comments(content):
    """Fix comments that wrap across lines due to text extraction.

    When pdftotext wraps a long comment, the continuation on the next line
    is NOT part of the comment in PDF syntax. We merge such continuations
    back into the comment line.

    Example artifact:
        /T 2786  % Offset of first entry in main cross-reference table (part
        11)

    Should become:
        /T 2786  % Offset of first entry in main cross-reference table (part 11)
    """
    lines = content.split('\n')
    result = []
    i = 0
    while i < len(lines):
        line = lines[i]
        # Check if this line has a comment that looks like it wraps
        if '%' in line:
            comment_start = line.index('%')
            comment = line[comment_start:]
            # Count unbalanced parentheses in the comment
            open_parens = comment.count('(') - comment.count(')')
            if open_parens > 0 and i + 1 < len(lines):
                # Comment has unclosed parens - check if next line closes them
                next_line = lines[i + 1].strip()
                # If next line is short and could be a continuation
                if len(next_line) < 40 and ')' in next_line:
                    # Merge the next line into this comment
                    line = line.rstrip() + ' ' + next_line
                    i += 1  # Skip the merged line
        result.append(line)
        i += 1
    return '\n'.join(result)


def fix_unbalanced_dicts(content):
    """Fix missing >> delimiters in indirect object definitions.

    Text extraction sometimes drops the closing >> for dictionaries.
    For each obj/endobj block, check if << and >> are balanced,
    and add missing >> before endobj if needed.
    """
    lines = content.split('\n')
    result = []
    obj_start = None

    for i, line in enumerate(lines):
        stripped = line.strip()
        if re.match(r'^\d+\s+\d+\s+obj\b', stripped):
            obj_start = len(result)
        elif stripped == 'endobj' and obj_start is not None:
            # Count << and >> in the object body
            body = '\n'.join(result[obj_start:])
            # Don't count << >> inside strings or comments
            opens = count_dict_delimiters(body, '<<')
            closes = count_dict_delimiters(body, '>>')
            missing = opens - closes
            if missing > 0:
                # Add missing >> before endobj, with same indentation as endobj
                indent = len(line) - len(line.lstrip())
                for _ in range(missing):
                    result.append(' ' * indent + '      >>')
            obj_start = None

        result.append(line)

    return '\n'.join(result)


def count_dict_delimiters(text, delim):
    """Count << or >> occurrences outside of strings and comments."""
    count = 0
    i = 0
    in_string = False
    paren_depth = 0
    while i < len(text) - 1:
        c = text[i]
        if c == '%' and not in_string:
            # Skip to end of line
            while i < len(text) and text[i] != '\n':
                i += 1
            continue
        if c == '(' and not in_string:
            in_string = True
            paren_depth = 1
            i += 1
            continue
        if in_string:
            if c == '\\':
                i += 2  # Skip escaped char
                continue
            if c == '(':
                paren_depth += 1
            elif c == ')':
                paren_depth -= 1
                if paren_depth == 0:
                    in_string = False
            i += 1
            continue
        if text[i:i+2] == delim:
            count += 1
            i += 2
            continue
        i += 1
    return count


def classify_example(ex):
    """Classify whether an example contains PDF syntax we can test."""
    content = ex["content"]
    pdf_indicators = [
        re.compile(r'<<'),
        re.compile(r'>>'),
        re.compile(r'\d+\s+\d+\s+obj\b'),
        re.compile(r'\d+\s+\d+\s+R\b'),
        re.compile(r'^xref\b', re.MULTILINE),
        re.compile(r'^trailer\b', re.MULTILINE),
        re.compile(r'%PDF-'),
        re.compile(r'/[A-Z]\w+'),
        re.compile(r'^\[', re.MULTILINE),
        re.compile(r'<[0-9A-Fa-f]'),
        re.compile(r'\(.*\)'),
        re.compile(r'\bstream\b'),
        re.compile(r'\bendobj\b'),
        re.compile(r'\bstartxref\b'),
        re.compile(r'%%EOF'),
    ]
    score = sum(1 for p in pdf_indicators if p.search(content))

    # Check for content stream operators
    if re.search(r'\b(BT|ET|Tf|Td|Tj|TJ|Tm|cm)\b', content):
        score += 1

    # Has ellipsis indicating incomplete content
    has_ellipsis = '…' in content or '...' in content

    if score >= 2:
        return "pdf_syntax"
    elif score >= 1:
        return "pdf_fragment"
    else:
        return "text"


def main():
    text_file = sys.argv[1] if len(sys.argv) > 1 else "/tmp/pdf_spec.txt"

    examples = extract_examples(text_file)

    for ex in examples:
        ex["type"] = classify_example(ex)

    pdf_count = sum(1 for e in examples if e["type"] == "pdf_syntax")
    frag_count = sum(1 for e in examples if e["type"] == "pdf_fragment")
    text_count = sum(1 for e in examples if e["type"] == "text")
    print(f"Found {len(examples)} examples total ({pdf_count} PDF syntax, {frag_count} fragments, {text_count} text)", file=sys.stderr)

    sections = {}
    for ex in examples:
        sec = ex["section"]
        if sec not in sections:
            sections[sec] = []
        sections[sec].append(ex)

    print(f"\nAcross {len(sections)} sections:", file=sys.stderr)
    for sec in sorted(sections.keys(), key=lambda s: [int(x) for x in re.findall(r'\d+', s.split()[0])] if s[0].isdigit() else [999]):
        exs = sections[sec]
        types = ", ".join(f"{e['example_num'] or '-'}: {e['type']}" for e in exs)
        print(f"  {sec}: {len(exs)} [{types}]", file=sys.stderr)

    json.dump(examples, sys.stdout, indent=2, ensure_ascii=False)


if __name__ == "__main__":
    main()
