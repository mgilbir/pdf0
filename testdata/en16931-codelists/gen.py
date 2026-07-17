#!/usr/bin/env python3
"""Generate en16931_codelists.go from the official CEN/TC 434 genericode bundle.

Run `bash download.sh` first (it fetches and unpacks the gitignored genericode/).
Then `python3 gen.py` rewrites ../../en16931_codelists.go with the code-list
tables. en16931_codelists_test.go re-derives the same sets from genericode/ and
asserts the committed tables still match, so they stay faithful to the source.
"""
import os
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
GC = os.path.join(HERE, "genericode")
OUT = os.path.abspath(os.path.join(HERE, "..", "..", "en16931_codelists.go"))


def codes(fn):
    seen, out = set(), []
    for row in ET.parse(os.path.join(GC, fn)).getroot().iter("Row"):
        for v in row.findall("Value"):
            if v.get("ColumnRef") == "Code":
                c = v.find("SimpleValue").text
                if c is not None and c not in seen:
                    seen.add(c)
                    out.append(c)
    return out


# (Go map name, genericode file, per-line count, comment)
TABLES = [
    ("en16931Currencies", "Currency.gc", 8,
     "en16931Currencies is the ISO 4217 currency set (BR-CL-03/04/05)."),
    ("en16931Countries", "Country.gc", 10,
     "en16931Countries is the ISO 3166 country code set (BR-CL-14/15)."),
    ("en16931TypeCodes", "1001.gc", 8,
     "en16931TypeCodes is the UNTDID 1001 document type code set (BR-CL-01)."),
    ("en16931VATCategories", "5305.gc", 10,
     "en16931VATCategories is the UNCL 5305 VAT category code set (BR-CL-17/18)."),
    ("en16931PaymentMeans", "Payment.gc", 10,
     "en16931PaymentMeans is the UNCL 4461 payment means code set (BR-CL-16)."),
    ("en16931AllowanceReasons", "Allowance.gc", 10,
     "en16931AllowanceReasons is the UNCL 5189 allowance reason code set (BR-CL-19)."),
    ("en16931ChargeReasons", "Charge.gc", 10,
     "en16931ChargeReasons is the UNCL 7161 charge reason code set (BR-CL-20)."),
    ("en16931VATEX", "VATEX.gc", 6,
     "en16931VATEX is the CEF VAT exemption reason code set (BR-CL-22)."),
    ("en16931EAS", "EAS.gc", 10,
     "en16931EAS is the CEF Electronic Address Scheme code set (BR-CL-25)."),
    ("en16931ICD", "ICD.gc", 10,
     "en16931ICD is the ISO 6523 ICD identifier scheme code set (BR-CL-10/11/13/21/26)."),
    ("en16931MIME", "MIME.gc", 6,
     "en16931MIME is the permitted attachment MIME code set (BR-CL-24)."),
    ("en16931RefTypeCodes", "1153.gc", 10,
     "en16931RefTypeCodes is the UNTDID 1153 reference type code set (BR-CL-07)."),
    ("en16931Units", "Unit.gc", 8,
     "en16931Units is the UNECE Rec 20/21 unit of measure code set (BR-CL-23)."),
]


def emit(name, comment, vals, per):
    lines = [f"// {comment}", f"var {name} = map[string]bool{{"]
    for i in range(0, len(vals), per):
        lines.append("\t" + " ".join(f'"{v}": true,' for v in vals[i:i + per]))
    lines.append("}")
    return "\n".join(lines)


def main():
    ver = ""
    root = ET.parse(os.path.join(GC, "Currency.gc")).getroot()
    for e in root.iter("Version"):
        ver = e.text
        break
    hdr = f'''package pdf0

// Code generated from the official CEN/TC 434 EN 16931 genericode bundle
// (digital-genericodes, version {ver}); DO NOT EDIT. Regenerate with
// `make en16931-codelists` (see testdata/en16931-codelists/gen.py).
// en16931_codelists_test.go re-derives these sets from the genericode and
// asserts they match, so the tables stay faithful to the source of truth.
'''
    blocks = [emit(n, c, codes(fn), p) for n, fn, p, c in TABLES]
    with open(OUT, "w") as f:
        f.write(hdr + "\n" + "\n\n".join(blocks) + "\n")
    print(f"wrote {OUT} ({len(TABLES)} tables, genericode {ver})")


if __name__ == "__main__":
    main()
