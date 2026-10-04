---
title: Bidra
description: Få stacken til å kjøre, og konvensjonene en endring følger.
sidebar:
  order: 0
  label: Oversikt
---

Vantigo er en Go-server med en React-frontend per modul, bygget med Bun og en
verktøykjede låst i `mise.toml`. Den komplette veiledningen for å få stacken til å
kjøre, prosjektstrukturen, kommandotabellen, migreringer, testing og
utgivelsesløpet er
[CONTRIBUTING.md](https://github.com/vantigo-io/vantigo/blob/main/CONTRIBUTING.md) i
repoet.

Sidene her dekker reglene en endring holdes til, og vedlikeholdes på engelsk:

- [Modulgrenser](/en/contributing/module-boundaries/) — eierskap til implementasjon
  og konvensjonene hver modul følger.
- [Dokumentasjon](/en/contributing/documentation/) — hvordan denne siden er organisert,
  hvordan du kjører den, og regelen om at en endring i oppførsel lander sammen med
  endringen i dokumentasjonen.
- [E-fakturavalidering](/en/contributing/e-invoice-validation/) — EHF-oraklet: de
  offisielle XSD- og Schematron-artefaktene over fakturamodulens fasitdokumenter,
  hvordan du kjører det, og hvordan du oppgraderer artefaktene.
