# Vendored XMP RelaxNG schemas

These `XMP_Properties-*.rng` files are vendored, unmodified, from the
**XMP-RNG-Schema** project by Francesco Pretto, at commit
`0642aafbde2e68f35920b5b5f8510accdc5c0e4d` (`Schemas/`):

  https://github.com/ceztko/XMP-RNG-Schema

They are RELAX NG schemas for the predefined XMP property schemas of
ISO 16684 (XMP), used here **only by the test suite**
(`TestXMPTablesMatchRNG` in `pdfa/xmp_rng_test.go`) as an authoritative
cross-check of pdf0's hand-coded XMP schema tables (`pdfa/xmp_schemas.go`).
No package of the library reads them.

Each file preserves its original SPDX headers. Per those headers the schemas are
offered under the MIT License (© 2025 Francesco Pretto) or the ISO 16684-2:2014
schema terms, at the recipient's choice; both permit redistribution, and the
ISO copyright notice for the underlying schema text is retained. Both licence
texts are in [THIRD_PARTY_NOTICES](../../THIRD_PARTY_NOTICES).

    SPDX-License-Identifier: MIT OR LicenseRef-ISO16684_2-2014-schema
