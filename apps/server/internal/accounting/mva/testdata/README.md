# Skatteetaten's VAT return material

Copied, unchanged, from Skatteetaten's published repository
[Skatteetaten/mva-meldingen](https://github.com/Skatteetaten/mva-meldingen) at commit
`e1bb462d32c4abcf62f607c05ecf7013dc68b327` (2026-11-01), under its Apache License 2.0,
from `docs/informasjonsmodell_filer/`:

| Here | There |
| --- | --- |
| `xsd/no.skatteetaten.fastsetting.avgift.mva.skattemeldingformerverdiavgift.v1.0.xsd` | `xsd/` — the return's schema, v1.0 |
| `kodelister/*.xml` | `kodelister/` — the code lists: `mvaKodeSAFT` (the codes, their groups and order), `mvaSpesifikasjon`, `sats`, `merknad`, `meldingskategori` |
| `examples/eksempelMedAlleTilfeller.xml`, `mvakode81.xml`, `mvakode86.xml` | `example_files/melding/` |
| `examples/tappaakrav.xml` | `example_files/melding/tappåkrav.xml` (renamed: an ASCII file name) |

`golden/` is ours: the returns `internal/accounting` writes for the test ledgers,
validated against the XSD by the Go test (when `xmllint` is on the PATH) and by
`tools/mva/validate.sh` in CI, with Skatteetaten's examples beside them.
