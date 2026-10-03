---
title: Referanse
description: Domenemodellen, reglene, rettighetene og endepunktene i hver modul.
sidebar:
  order: 0
  label: Oversikt
---

Referansen beskriver hver modul slik koden implementerer den: domenemodellen, reglene en
forespørsel sjekkes mot, rettighetene, bakgrunnsjobbene og endepunktene. Den er skrevet
for en integrator, en driftsansvarlig som feilsøker et avslag, eller en bidragsyter som
skal endre modulen, og den holdes i takt med koden: en endring i en moduls oppførsel
lander sammen med endringen i siden her.

Referansen vedlikeholdes på engelsk, og sidene under viser den engelske teksten. Den
genererte **API**-referansen bygges fra OpenAPI-kontraktene i
[`openapi/`](https://github.com/vantigo-io/vantigo/tree/main/openapi), ett dokument
per modul, så den kan ikke drive fra det serveren faktisk tilbyr. Den er en del av den
offentlige siden på <https://docs.vantigo.io> og utelatt fra kopien en
Vantigo-installasjon serverer under `/docs/`.

## Moduler

| Modul | Hva den inneholder |
| --- | --- |
| [Kunder](/en/reference/customers/) | Kunder, juridisk identitet, kontakter og roller, tidslinjen, adresser, faktureringsprofilen, registeroppslag, GDPR. |
| [Kommunikasjon](/en/reference/communications/) | Utgående e-post over delte postkasser, utboksen og oppbevaring. |
| [Produkter](/en/reference/products/) | Katalogen over varer og tjenester bedriften selger, med priser. |
| [Prosjekter](/en/reference/projects/) | Prosjekter per kunde, koder, roller, økonomisk oppsett og arbeidstyper. |
| [Timer](/en/reference/time/) | Timer på prosjekter, satser og øyeblikksbildene av dem, ukentlig innsending og godkjenning. |
| [Utlegg](/en/reference/expenses/) | Utlegg og kjøregodtgjørelse, kvitteringer, godkjenning, refusjon og fakturering. |
| [Fakturaer](/en/reference/invoices/) | Salgsdokumentet: nummerering, mva, utstedelse, kreditnotaer, PDF-en, betalinger, sending og eksport. |

Identitet er ikke en modul: kontoer, økter, MFA, RBAC, OIDC og SCIM er alltid en del av
applikasjonen og er dokumentert under [Administrasjon](/nb/admin/authentication/).
