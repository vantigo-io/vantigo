# The VAT return oracle

Skatteetaten's `skattemeldingformerverdiavgift` v1.0 XSD over the VAT returns the
accounting module writes — the goldens its tests compare with — and Skatteetaten's
own example returns, all under `apps/server/internal/accounting/mva/testdata`. It
runs in CI (the `🧾 Validate VAT returns` job) and on demand; it never runs in the
server.

```bash
mise install java          # once: Temurin 21, pinned in mise.toml
mise run mva:validate
```

| File | What it is |
| --- | --- |
| `validate.sh` | Runs `Validate.java` over every golden and every example. Nothing is fetched: the XSD is in the repository, copied from Skatteetaten (see the testdata's `README.md`). |
| `Validate.java` | One source file, run as `java Validate.java --xsd <xsd> <xml>…`: the JDK's `javax.xml.validation`, external entities and schemas refused. |

The Go tests (`internal/accounting/mva` and the accounting module's
`TestVatReturn_Goldens`) validate the same files with `xmllint` when it is on the
PATH and say so in the log when it is not; this oracle is what CI holds them to.
After a change to the return, rewrite the goldens with
`go test ./internal/accounting -run TestVatReturn_Goldens -update` and run the oracle.
