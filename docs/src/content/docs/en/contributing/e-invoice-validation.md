---
title: E-invoice validation
description: The EHF oracle — the official XSD and Schematron artefacts run over the invoice module's goldens, in CI and on demand.
sidebar:
  order: 3
sources:
  - tools/ehf
  - apps/server/internal/invoices/ehf/testdata
---

The Invoices module renders an issued invoice or credit note as an EHF Billing 3.0
document — Peppol BIS Billing 3.0 UBL 2.1 — with a hand writer
([the mapping](/en/reference/invoices/#the-ehf-document)). The Go tests prove that the
writer is deterministic and maps each field; they cannot prove that a receiver will
accept the result. **The oracle** can: it runs the official validation artefacts, the
same ones an access point and the receiver run, over documents the writer produced. It
lives in
[`tools/ehf`](https://github.com/vantigo-io/vantigo/tree/main/tools/ehf), runs in CI on
every pull request, and never runs inside the server.

## What it checks

Each document goes through three layers.

- **The UBL 2.1 XSD** from OASIS, validated with the JDK's own `javax.xml.validation`.
  Schematron says nothing about element order or names, and those are exactly what a
  hand writer can get wrong. The Invoice or the CreditNote schema is picked by the
  root element.
- **The EN 16931 rules** from CEN (`BR-*`, `BR-CO-*`, `UBL-CR-*`), release
  `validation-1.3.16`, which ships them compiled to XSLT.
- **The Peppol BIS Billing 3.0 rules** (`PEPPOL-EN16931-*`, and the Norwegian
  `NO-R-001` and `NO-R-002`), tag `v3.0.20`. The Peppol repository ships Schematron
  sources only, so the oracle compiles `PEPPOL-EN16931-UBL.sch` to XSLT with SchXslt,
  once per pin.

Both rule sets run through Saxon-HE 12.7. The oracle reads the SVRL report itself: an
assertion flagged `fatal` fails the document, and a `warning` is printed and never
fails it.

**The goldens** must be green: no XSD error and no fatal rule. A golden that trips a
fatal rule the design has yet to settle can be listed in
`tools/ehf/known-failures.txt`, one golden and one rule id per line, with the reason
above it. The rule is then tolerated on that golden only, and the run fails as soon as
it stops firing, so the list never goes stale. **The invalid fixtures** must each trip
exactly the fatal rule ids their manifest entry names, no more and no fewer.

## Running it

```bash
mise install java     # once: Temurin 21, pinned in mise.toml
mise run ehf:validate
```

The JDK is in `mise.toml` for the oracle alone; nothing else in the repository needs
Java. The first run downloads about 70 MB of artefacts into `tools/ehf/.cache`, which
git ignores, and compiles the Peppol Schematron; later runs take about ten seconds.
The output has one line per document, each rule's text beneath it, and a summary line
at the end.

## The fixtures

Both sets live beside the writer, under
[`apps/server/internal/invoices/ehf/testdata`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/invoices/ehf/testdata).

- **`golden/`** holds the documents `TestEHF_Goldens` renders from fixed fixtures: an
  invoice with every VAT category, a discount, a foreign buyer, a person as buyer, a KID
  under each algorithm, a delivery period and place, a seller that is not VAT
  registered, and three credit notes — full, partial and final with the squaring row.
  `go test ./internal/invoices/ehf/ -update` rewrites them. After changing the writer,
  regenerate them, run the oracle, and commit them together.
- **`invalid/`** holds hand-tampered documents: each is a golden with one change, and
  `-update` never rewrites them.

### The manifest

`invalid/manifest.json` lists each invalid fixture with five fields:

| Field | What it says |
| --- | --- |
| `file` | the fixture's file name under `invalid/` |
| `from` | the golden it was made from |
| `change` | the one change, in words |
| `rules` | the fatal rule ids the oracle must report, as an exact set |
| `module` | the module's own ids for a check no official rule names, such as `vat_category_k_unsupported`; the oracle ignores them |

The manifest holds two checks to the same ids. The oracle requires that `rules` be
exactly what the official artefacts report. `TestPrecheck_AgreesWithTheManifest`
requires that the Go pre-check and invariants report every id in `rules` and `module`
that they know, and nothing the manifest does not list. A new fixture is a copy of a
golden with one change and an entry whose `rules` come from the oracle's output, never
from reading the rules by hand. A fixture in `invalid/` without an entry fails the run,
and so does an entry without a fixture.

## Bumping the artefacts

CEN and OpenPEPPOL release new artefacts each spring and autumn, and each release
becomes mandatory about three months later. Adopt one by bumping its pin:

1. In `tools/ehf/artefacts.lock`, change the artefact's file name and URL, then write
   the new file's SHA-256. Check it against the publisher's hash where one exists:
   GitHub's on a release asset, Maven Central's `.sha1`. The file name must keep its
   prefix (`Saxon-HE-`, `xmlresolver-`, `schxslt-`, `en16931-ubl-`,
   `peppol-bis-invoice-3-`, `UBL-`): the script finds each artefact by it. The Peppol
   rules are pinned as the raw `PEPPOL-EN16931-UBL.sch` at the new tag's commit.
2. Run `mise run ehf:validate`. The lock's new hash names a new work directory, so the
   artefacts are unpacked and the Schematron compiled again.
3. Act on every change in the output. If a golden now fails, change the writer, or add
   a known failure with its reason when the design has to decide. If an invalid
   fixture's fatal set changes, update its `rules` in the manifest and run the Go tests
   again.
4. Commit the lock together with everything the run made you change. CI caches
   `tools/ehf/.cache` keyed on the lock's hash, so the next run fills the cache again.

[`tools/ehf/README.md`](https://github.com/vantigo-io/vantigo/blob/main/tools/ehf/README.md)
lists every pinned artefact and why it is there.
