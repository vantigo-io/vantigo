---
title: SAF-T validation
description: The SAF-T oracle — Skatteetaten's SAF-T Financial 1.40 XSD over the accounting module's export goldens, and the 1.30 and 1.40 XSDs over Skatteetaten's examples, in the Go tests, in CI and on demand; the checks the tests make beyond the XSD; and how to change the export.
sidebar:
  order: 4
sources:
  - tools/saft
  - apps/server/internal/accounting/saft/testdata
---

The Accounting module writes SAF-T Financial files in Skatteetaten's schema, version 1.40
([SAF-T Financial](/en/reference/accounting/#saf-t-financial)), and reads 1.30 and 1.40
files to set up the books. The Go tests prove what the file says — every voucher, the
balances, the parties, the references — and compare a fixed ledger's file with a golden.
That the files are valid against Skatteetaten's schema is the **SAF-T oracle**'s to prove.
It lives in [`tools/saft`](https://github.com/vantigo-io/vantigo/tree/main/tools/saft),
runs in CI on every pull request, and never runs inside the server. It follows the
[VAT return oracle](/en/contributing/vat-return-validation/) exactly and shares its
validator.

## What it checks

`tools/saft/validate.sh` runs `tools/mva/Validate.java` — one source file, the JDK's own
`javax.xml.validation`, external entities and schemas refused — with:

- the 1.40 XSD over every golden under
  `apps/server/internal/accounting/saft/testdata/golden` (a year of the fixed ledger with
  every voucher kind the books post) and Skatteetaten's 1.40 example;
- the 1.30 XSD over Skatteetaten's 1.30 example, the version the import also reads.

Nothing is fetched. The XSDs and the examples are copied, unchanged, from Skatteetaten's
published repository [Skatteetaten/saf-t](https://github.com/Skatteetaten/saf-t);
`testdata/README.md` names the commit they were taken from.

The schema checks names, order, types and the key references, not the books. The
accounting module's tests hold every file the export writes — the golden, an empty year,
periods within a year, a fiscal year from July, each part of a split — to the books
beyond the XSD: the totals to the lines and debit to credit, every account's closing
balance to the trial balance, the parties' balances to their reports, and every account,
customer, supplier, tax code and project a line names to the master files. The round trip
exports a ledger and imports the file into new books.

## Running it

```bash
mise install java          # once: Temurin 21, pinned in mise.toml
mise run saft:validate
```

The Go tests (`internal/accounting/saft` and the accounting module's `TestSaftExport_*`)
validate every file they write with `xmllint`. Locally a missing `xmllint` is said in the
log and the file left to the oracle; in CI — where `CI` is set and the Go test job installs
`libxml2-utils` — a missing `xmllint` fails the test, so every file written, not only the
golden, is held to the XSD there. CI's `🧾 Validate SAF-T files` job runs the oracle over
the goldens and examples as well, and `✅ CI passed` needs it.

## Changing the export

1. Change the writer (`internal/accounting/saft`) or what the module gives it
   (`internal/accounting/saft_export.go`).
2. Rewrite the golden: `go test ./internal/accounting -run TestSaftExport_TheLedgerGolden -update`
   in `apps/server`, and read the diff.
3. Run `mise run saft:validate`.

When Skatteetaten publishes a new version of the schema, copy it into `testdata/xsd`
unchanged with an example, update the commit in its `README.md`, and run the tests and the
oracle.
