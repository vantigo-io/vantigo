# Skatteetaten's SAF-T Financial material

Copied, unchanged, from Skatteetaten's published repository
[Skatteetaten/saf-t](https://github.com/Skatteetaten/saf-t) at commit
`10c8fd82a6ff47e53385bc33c7638c5fde239a4f` (2026-10-16):

| Here | There |
| --- | --- |
| `xsd/Norwegian_SAF-T_Financial_Schema_v_1.40.xsd` | `SAF-T_Financial_1.4/` — the schema the export writes, mandatory for periods from 1 January 2027 |
| `xsd/Norwegian_SAF-T_Financial_Schema_v_1.30.xsd` | `SAF-T_Financial_1.3/` — the schema an import also reads |
| `examples/example-1.40.xml` | `SAF-T_Financial_1.4/Example Files/generated_auditfile_formatted_v_1.40.xml` (renamed) |
| `examples/example-1.30.xml` | `SAF-T_Financial_1.3/Example Files/generated_auditfile_formatted_v_1.30.xml` (renamed) |

The rules the schema does not express — which line carries TaxInformation, the
empty values (`NA`, `0.00`), every account and every tax code in MasterFiles, the
period of a deviating fiscal year — are from Skatteetaten's *Norwegian SAF-T
Financial data — Technical description* v1.7 (31 August 2026), published on
skatteetaten.no, and are cited where the code follows them.

`golden/` is ours: the files `internal/accounting` writes for its test ledgers,
validated against the 1.40 XSD by the Go tests (when `xmllint` is on the PATH) and
by `tools/saft/validate.sh` in CI, with Skatteetaten's examples beside them.
