---
title: Ordre
description: Slå på Ordre — modulene den krever og de ni rettighetene — og, etter hvert som de kommer, innstillingene, nummerseriene, avsenderkanalen og tilbudsmalene.
sidebar:
  order: 48
sources:
  - apps/server/internal/orders/module.go
  - apps/server/internal/orders/settings.go
---

Ordre krever modulene Produkter, Kunder og Fakturaer: når `MODULES` er satt, list
`orders` ved siden av `products`, `customers` og `invoices`, ellers nekter serveren å
starte og navngir den som mangler. Ingen andre enn eieren kan bruke appen før en rolle
gir rettighetene; `orders:access` åpner den.

Innstillingene — nummerseriene, hvor lenge et tilbud gjelder, betalingsfristen,
avsenderkanalen og e-posttekstene — er under arbeid, og denne siden beskriver dem etter
hvert som de kommer.
