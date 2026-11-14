---
title: VAT return validation
description: The VAT return oracle — Skatteetaten's skattemeldingformerverdiavgift v1.0 XSD over the accounting module's return goldens and Skatteetaten's examples, in the Go tests, in CI and on demand.
sidebar:
  order: 4
sources:
  - tools/mva
  - apps/server/internal/accounting/mva/testdata
---

The Accounting module writes each closed VAT term's return in Skatteetaten's
`skattemelding for merverdiavgift` schema, v1.0
([the return](/en/reference/accounting/#the-return)). The Go tests prove the writer lays
the figures out as the return asks — the groups and order of Skatteetaten's code list, the
lines each code makes, whole kroner — and compare what a close stores with golden files.
That the files are valid against Skatteetaten's schema is the **VAT return oracle**'s to
prove. It lives in
[`tools/mva`](https://github.com/vantigo-io/vantigo/tree/main/tools/mva), runs in CI on
every pull request, and never runs inside the server.

## What it checks

`tools/mva/validate.sh` runs `Validate.java` — one source file, the JDK's own
`javax.xml.validation`, external entities and schemas refused, and taking any XSD, so the
[SAF-T oracle](/en/contributing/saft-validation/) runs it too — with the XSD over:

- every golden under `apps/server/internal/accounting/mva/testdata/golden`: a term of
  sales and purchases at mixed rates; one with reverse charge, VAT not deductible, a loss
  on a receivable and a reversal of an earlier term's purchase; an empty term;
- Skatteetaten's own example returns under `testdata/examples`, so the schema and the
  validator are known to accept what Skatteetaten publishes.

Nothing is fetched. The XSD, the code lists and the examples are copied, unchanged, from
Skatteetaten's published repository
[Skatteetaten/mva-meldingen](https://github.com/Skatteetaten/mva-meldingen) under its
Apache License 2.0; `testdata/README.md` names the commit they were taken from.

The schema checks names, order and types, not Skatteetaten's business rules — the sums
(R018), a VAT against its base and rate (R019), the codes a specification may stand on
(R038). Those are the module's tests' to hold: the shape against the published examples,
the rounding, and the goldens' figures.

## Running it

```bash
mise install java          # once: Temurin 21, pinned in mise.toml
mise run mva:validate
```

The Go tests (`internal/accounting/mva` and the accounting module's
`TestVatReturn_Goldens`) validate the same files with `xmllint` when it is on the PATH,
and say in the log when it is not — but in CI, where the Go test job installs it, a
missing `xmllint` fails them; CI's `🧾 Validate VAT returns` job runs the oracle
whatever the tests found, and `✅ CI passed` needs it.

## Changing the return

1. Change the writer (`internal/accounting/mva`) or the figures it is given.
2. Rewrite the goldens: `go test ./internal/accounting -run TestVatReturn_Goldens -update`
   in `apps/server`, and read the diff — each line is a figure a person types in at
   Skatteetaten.
3. Run `mise run mva:validate`.

When Skatteetaten publishes a new version of the schema or a code list, copy the files
into `testdata` unchanged, update the commit in its `README.md`, and run the tests and the
oracle.
