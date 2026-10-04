# The EHF oracle

The official validation artefacts for Peppol BIS Billing 3.0 (EHF), run over the
invoice module's goldens and invalid fixtures under
`apps/server/internal/invoices/ehf/testdata`. It runs in CI (the `🧾 Validate EHF
goldens` job) and on demand; it never runs in the server. The contributing page
<https://docs.vantigo.io/en/contributing/e-invoice-validation/> explains what it
checks and why.

```bash
mise install java          # once: Temurin 21, pinned in mise.toml
mise run ehf:validate
```

| File | What it is |
| --- | --- |
| `validate.sh` | Fetches the artefacts into `.cache` (git-ignored) and verifies each SHA-256 against `artefacts.lock`; once per lock, unpacks them and compiles the Peppol Schematron with SchXslt; then runs `Validate.java` over every golden and every invalid fixture. |
| `Validate.java` | One source file, run as `java -cp <Saxon jars> Validate.java …`. Picks the Invoice or CreditNote XSD by the root element and validates with `javax.xml.validation`; runs the CEN and the Peppol XSLT through Saxon; reads the SVRL itself: any `flag="fatal"` fails a golden, warnings are printed. An invalid fixture's fatal rule ids must equal its `manifest.json` entry's `rules`, exactly. |
| `artefacts.lock` | Every download: file name, SHA-256, URL. |
| `known-failures.txt` | Fatal rules a golden is known to trip, each with its reason; tolerated on that golden only, and a failure once it stops firing. |

## The artefacts

| Artefact | Version | Why |
| --- | --- | --- |
| Saxon-HE (+ `org.xmlresolver:xmlresolver` and its `data` jar, as its POM requires) | 12.7 (xmlresolver 5.3.3) | The artefacts are XSLT 2.0. |
| SchXslt (`name.dmaus.schxslt:schxslt`) | 1.10.1 | The Peppol repository ships Schematron sources only — at `v3.0.20`, `rules/sch` holds no XSLT and the GitHub release has no assets — so the oracle compiles `PEPPOL-EN16931-UBL.sch` itself. SchXslt handles its `xsl:function`s (`u:mod11`, `u:gln`, …). |
| CEN EN 16931 UBL (`en16931-ubl-*.zip`) | `validation-1.3.16` | The BR-* and UBL-CR-* rules, shipped as compiled XSLT. |
| Peppol BIS Billing 3.0 (`OpenPEPPOL/peppol-bis-invoice-3`, tag zipball) | `v3.0.20` | The PEPPOL-EN16931-* and NO-R-* rules. |
| UBL 2.1 (OASIS Standard) | `os-UBL-2.1` | The XSD: Schematron checks nothing about element order or names. |

## Bumping a pin

The CEN and Peppol artefacts are released each spring and autumn, each mandatory
about three months later; adopt a release when it is tagged.

1. Change the URL and file name in `artefacts.lock`, and put a placeholder in the
   hash column.
2. Download the file and take its hash: `curl -fsSL <url> | shasum -a 256`. Where the
   publisher states a hash (GitHub release assets, Maven Central's `.sha1`), check it
   against yours. Write the hash into the lock.
3. `mise run ehf:validate`. The new lock hash names a new work directory, so the
   artefacts are unpacked and the Schematron compiled again.
4. Read every change in the output. A golden that now fails is a writer change to
   make or, when the design must decide, an entry in `known-failures.txt` with its
   reason. An invalid fixture whose fatal set changed means the rule ids in
   `manifest.json` follow the new release; `TestPrecheck_AgreesWithTheManifest`
   (`go test ./internal/invoices/ehf/`) then holds the Go pre-check to them.
5. Commit the lock with whatever the run made you change. CI's cache is keyed on the
   lock's hash and refills itself.

A Saxon bump checks its POM for the xmlresolver version it requires. A JDK bump is the
`java` line in `mise.toml`.
