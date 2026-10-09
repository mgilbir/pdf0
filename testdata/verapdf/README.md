# veraPDF

`TestType3DocumentsAreValidForVeraPDF` holds the documents pdf0's Type 3
fonts produce to [veraPDF](https://verapdf.org), the PDF/A reference validator,
beside pdf0's own. It runs the command in `PDF0_VERAPDF` with `--format text`
and the documents, and skips when that is unset.

`verapdf-docker.sh` runs the official image, `verapdf/cli:v1.30.3` unless
`VERAPDF_IMAGE` says otherwise:

    docker pull verapdf/cli:v1.30.3
    PDF0_VERAPDF=$PWD/testdata/verapdf/verapdf-docker.sh go test -run TestType3DocumentsAreValidForVeraPDF .

A veraPDF installed locally works as well: `PDF0_VERAPDF=verapdf`.

veraPDF 1.30.3 logs `Invalid glyph code: -N` for a Type 3 code of 128 or more,
reading the byte as signed. It is noise: a width made wrong at codes 150 and
200 is reported under 6.2.11.5 as at code 5.
