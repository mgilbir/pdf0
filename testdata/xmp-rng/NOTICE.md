# Vendored XMP RelaxNG schemas

These `XMP_Properties-*.rng` files are vendored, unmodified, from the
**XMP-RNG-Schema** project by Francesco Pretto:

  https://github.com/ceztko/XMP-RNG-Schema

They are RELAX NG schemas for the predefined XMP property schemas of
ISO 16684 (XMP), used here **only by the test suite** (`xmp_rng_test.go`) as an
authoritative cross-check of pdf0's hand-coded XMP schema tables
(`xmp_schemas.go`). They are not compiled into or used by the pdf0 library at
runtime, which remains dependency-free.

Each file preserves its original SPDX headers. Per those headers the schemas are
offered under the MIT License (© 2025 Francesco Pretto), which permits
redistribution; the ISO copyright notice for the underlying schema text is
retained as required.

    SPDX-License-Identifier: MIT OR LicenseRef-ISO16684_2-2014-schema
