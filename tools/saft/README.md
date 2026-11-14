# The SAF-T oracle

Skatteetaten's SAF-T Financial 1.40 XSD over the files the accounting module's
export writes — the goldens its tests compare with — and Skatteetaten's own example
files against the XSD of their version, all under
`apps/server/internal/accounting/saft/testdata`. It runs in CI (the
`🧾 Validate SAF-T files` job) and on demand; it never runs in the server.

```bash
mise install java          # once: Temurin 21, pinned in mise.toml
mise run saft:validate
```

| File | What it is |
| --- | --- |
| `validate.sh` | Runs the VAT return oracle's `tools/mva/Validate.java` — the JDK's `javax.xml.validation`, external entities and schemas refused — with the 1.40 XSD over every golden and the 1.40 example, and the 1.30 XSD over the 1.30 example. Nothing is fetched. |

The Go tests (`internal/accounting/saft` and the accounting module's
`TestSaftExport_*`) validate every file the export writes with `xmllint` when it is
on the PATH and say so in the log when it is not; this oracle is what CI holds the
goldens to. After a change to the export, rewrite the golden with
`go test ./internal/accounting -run TestSaftExport_TheLedgerGolden -update` and run
the oracle.
