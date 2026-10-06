---
title: Bank file validation
description: How the bank-file parsers are held to the formats — the camt.054 element paths pinned by tests, the fixtures and the builders — and why the ISO 20022 XSD oracle is not in the repository yet.
sidebar:
  order: 4
sources:
  - apps/server/internal/invoices/bankfile/testdata
---

The Invoices module reads the bank files a seller imports: OCR giro from Mastercard
Payment Services and camt.054 debit/credit notifications in versions `.001.02` and
`.001.08`. Both parsers are hand-written over the standard library — OCR is a handful
of fixed-width records, camt.054 about thirty element paths through `encoding/xml` —
and live in
[`apps/server/internal/invoices/bankfile`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/bankfile),
a package that imports nothing of the module but `invoices/kid`. What a file must
satisfy to be imported is documented with the import; this page is about how the
parsers are kept true to the formats.

## No schema validation at runtime

The server never validates a camt.054 file against its XSD. Go's `encoding/xml` does
not read XML Schema, and validating at runtime would need libxml2 through cgo, which
the server's static image — distroless, without a C library — does not carry. The
parser checks instead what the import depends on, all or nothing: the file is strict, well-formed XML without a `DOCTYPE`,
at most 64 levels deep and at most 10 000 elements plus 100 per transaction; every
entry's transactions sum to its amount and match its batch count; the summary agrees
with the entries; every currency is NOK; every amount has at most two decimals; every
reference fits the column it is stored in. A refusal names the element, such as
`Ntry[2]/NtryDtls/TxDtls[1]/AmtDtls/TxAmt/Amt`.

## The XSD oracle, deferred

The plan was to vendor the two ISO 20022 schemas, `camt.054.001.02.xsd` and
`camt.054.001.08.xsd`, under `bankfile/testdata/xsd/` with a `NOTICE`, and to run
`xmllint --schema` over every fixture and every builder output in a tagged test, a
`mise run bankfiles:validate` task and a CI job, as the
[EHF oracle](/en/contributing/e-invoice-validation/) does for invoices. It is not in
the repository: iso20022.org answers a scripted download with an off-line page, so the
files could not be fetched where the work was done, and their own headers — which
decide whether they may be redistributed — have not been read. Without both, nothing
is vendored, and there is no oracle script, task or CI job.

Until it lands, the tests stand in for it:

- **`TestBankFileCamt_ElementPaths`** pins every path the parser reads, from R4 §3.2's
  table of the research, in the version that has it: each case changes one element of
  the research's example and checks that the matching field changes, and the cases
  that differ between versions — `Ntry/Sts` against `Ntry/Sts/Cd`, `Dbtr/Nm` against
  `Dbtr/Pty/Nm`, `.08`'s own `TxDtls/Amt` — check that the other version's path is
  **not** read. A path that moves fails a case.
- **`TestBankFileCamt_Parse`** reads every fixture to the field, refuses one broken file
  per rule at its element, and holds each cap at its bound.
- **`TestBankfiletest_CamtIsAccepted`** parses the builder's output in both versions and
  every entry shape.
- **`FuzzParse`**, seeded with every fixture, checks that no input panics, that a
  refusal is always the parser's own error, and that whatever is accepted fits the
  database's columns.

To add the oracle later: fetch both files in a browser from iso20022.org
(`/message/12746/download` and `/message/12776/download`), read each file's header
before committing it, write a `NOTICE` that names https://www.iso20022.org/, says it is
not the official site and quotes the ISO 20022 IPR policy's royalty-free licence, and
add the tagged test, the script under `tools/bankfiles/` and the CI job.

## The fixtures

The committed files live under
[`bankfile/testdata`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/bankfile/testdata),
`ocr/` and `camt054/`. **They are our own**: written for this repository with invented
values — account `12345678903`, KIDs such as `0010017`, names such as Kunde AS — and
never a bank's file, whose licence to redistribute is not stated. The research's two
examples, `ocr/r4-example.ocr` and `camt054/v02-r4-example.xml`, are copied verbatim
from it; the rest each show one shape a bank sends:

| Fixture | Shape |
| --- | --- |
| `camt054/v08-r4-example.xml` | the same payment in `.001.08`'s paths |
| `camt054/v02-danske-shaped.xml` | two entries sharing one entry reference with a leading space, no transaction reference, the KID on `SCOR` under `PMNT/NTAV/NTAV` |
| `camt054/v02-batch.xml` | one entry of three transactions with `Btch/NbOfTxs` and `TxsSummry`: a KID, a text naming an invoice, a Vipps payout |
| `camt054/v02-reversals.xml` | a debit with `RvslInd`, a `PMNT/ICDT/RRTN` return, a plain debit, a credit |
| `camt054/v02-statuses.xml` | a pending entry, an entry without transactions, a 0.00 transaction |
| `camt054/v08-iban.xml` | the account as a spaced IBAN, the amount only on `TxDtls/Amt` |
| `camt054/v08-copy-batch.xml` | a `CpyDplctInd` `COPY`, a batch with a 0.00 line, a reversal, a pending entry |

A broken file — a sum that does not add up, a foreign currency, three decimals, a
`DOCTYPE`, a file nested too deep, an element bomb, a third namespace — is never
committed: each test builds it from a fixture or a builder's output by changing one
place. To add a fixture, write it by hand in the same way, add its case to
`TestBankFileCamt_Parse` (or, for OCR, to the builder's table in
`bankfiletest/ocr_test.go`, which holds every OCR fixture to the builder's output);
`FuzzParse` seeds itself with every file under `camt054/`.

## The builders

[`bankfile/bankfiletest`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/bankfile/bankfiletest)
builds well-formed files for tests anywhere in the module: `OCR(transmission,
payments...)` an OCR giro transmission with its counts, sums and dates, and
`Camt054(version, msgID, created, account, entries...)` a camt.054 notification in
either version's paths with each entry's amount, its batch count and the
notification's summary computed. The tests of the import, of matching and of the
integration story build their files with them, so a change to a builder runs through
the parser's own tests first.

## Running the tests

```bash
cd apps/server
mise exec -- go test ./internal/invoices/bankfile/...
mise exec -- go test -run '^$' -fuzz FuzzParse -fuzztime 60s ./internal/invoices/bankfile/
```
