---
title: Contributing
description: Getting the stack running and the conventions a change follows.
sidebar:
  order: 0
  label: Overview
---

Vantigo is a Go server with a React front end per module, built with Bun and a
toolchain pinned in `mise.toml`. The complete guide to getting the stack running, the
project layout, the command dispatch table, migrations, testing and the release
pipeline is
[CONTRIBUTING.md](https://github.com/vantigo-io/vantigo/blob/main/CONTRIBUTING.md) in
the repository.

The pages here cover the rules a change is held to:

- [Module boundaries](/en/contributing/module-boundaries/) — implementation ownership
  and the conventions every module follows.
- [Documentation](/en/contributing/documentation/) — how this site is organised, how to
  run it, and the rule that a change to behaviour lands together with the change to
  its documentation.
- [E-invoice validation](/en/contributing/e-invoice-validation/) — the EHF oracle: the
  official XSD and Schematron artefacts over the invoice module's goldens, how to run
  it, and how to bump the artefacts.
- [Bank file validation](/en/contributing/bank-file-validation/) — how the OCR giro and
  camt.054 parsers are held to the formats: the element paths pinned by tests, the
  fixtures and the builders, and why the ISO 20022 XSD oracle is deferred.
