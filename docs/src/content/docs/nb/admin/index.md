---
title: Administrasjon
description: Installere, konfigurere og drifte Vantigo selv.
sidebar:
  order: 0
  label: Oversikt
---

Denne delen er for den som drifter Vantigo: installerer det, konfigurerer det, holder
det oppe og oppgraderer det. Vantigo er ett container-image og én PostgreSQL-database;
hvilke forretningsmoduler en installasjon tilbyr velges med innstillingen `MODULES`,
og identitet (kontoer, innlogging, MFA, RBAC, OIDC og SCIM) er alltid en del av
applikasjonen.

## Hvor du begynner

- **Installer.** Raskeste vei er den ferdige Docker Compose-stacken: ferdigbygde images,
  en engangsjobb for migrering og applikasjonen på port 8080.
  [Installasjonsveiledningen](/nb/admin/installation/) tar deg fra første oppstart til
  produksjon: omvendt proxy, helsesjekker, databasens tilkoblingsbudsjett,
  bakgrunnsjobber og oppgradering.
- **Identitet og innlogging.** [Identitet, autentisering og utrulling](/nb/admin/authentication/)
  forklarer lokale kontoer, OIDC-leverandøren som konfigureres i miljøet, e-post,
  proxy-tillit og nøkkelmaterialet som utledes fra `APP_SECRET`.
  [SSO- og SCIM-drift](/nb/admin/sso-scim/) er produksjonsveiledningen for statisk OIDC og
  SCIM: konfigurasjon, rotasjon, migrering, livssyklus og gjenoppretting.
- **Sikkerhet.** [Transportsikkerhet og herding av nettleseren](/nb/admin/transport-security/)
  går gjennom https, PostgreSQL-TLS, SMTP-TLS, HSTS, vertsfiltrering og
  innholdssikkerhetspolicyen, og hva hvert valg koster.
- **Drift.** [Management-lytteren](/nb/admin/management-listener/) er det private
  statusendepunktet et kontrollplan spør, og måten den første eieren settes inn ved
  invitasjon. [Objektlagring](/nb/admin/object-storage/) beskriver hvor kvitteringer,
  PDF-er og vedlegg ligger.
- **E-faktura.** [E-faktura](/nb/admin/e-invoicing/) setter opp sending av fakturaer som
  EHF i Peppol-nettverket: kontoen hos Storecove og påloggingsdataene, bryterne, de to
  bakgrunnsjobbene som bærer hvert dokument, hva du gjør når en sending er ubekreftet
  eller feilet, og KID-avtalen du ber banken om.
- **Innbetalinger.** [Innbetalinger fra banken](/nb/admin/payments/) er bankavtalen som
  gir deg OCR-giro- eller camt.054-filer med innbetalinger, hvor hver bank lar deg laste
  dem ned, og formatet hver kontos filer importeres i.

## Den komplette konfigurasjonsreferansen

Hver innstilling applikasjonen leser er deklarert ett sted,
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
som validerer hele miljøet ved oppstart og rapporterer alle problemer samtidig.
Eksempelfilen i
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lister dem med standardverdiene sine.
