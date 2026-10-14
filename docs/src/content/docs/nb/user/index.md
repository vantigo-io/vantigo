---
title: Finn fram i Vantigo
description: Apper, sidemenyen, dashbordet, søk og kontoen din.
sidebar:
  order: 0
  label: Oversikt
sources:
  - apps/host/frontend
---

Vantigo samler modulene en bedrift trenger i én applikasjon. Hvilke moduler du ser
avhenger av to ting: hvilke organisasjonen din har slått på, og hvilke rettigheter
rollene dine gir deg. En modul du ikke har rettighet til vises rett og slett ikke.

## Apper og app-velgeren

Hver modul er en **app** med sin egen sidemeny: Kunder, Prosjekter, Timer, Utlegg,
Ansatte, Fakturaer, Kommunikasjon, Produkter og Energi. App-velgeren i toppfeltet lister appene
du kan åpne; **Hjem** er dashbordet, der hver app du bruker viser sitt eget kort med
tallene som trenger oppmerksomheten din — Ansattes, hvor mange som er ansatt i dag med
årsverkene, hvem som begynner og slutter snart og hvem som er borte i dag, for
`staff:view` eller `staff:manage`
([dashbordet](/nb/user/staff/#dashbordet)). Listen **Trenger oppfølging** samler det i
appene som venter på noen — også fra Fakturaer: forfalte fakturaer, penger som skal
tilbake på en faktura, og, med `invoices:payments`, banklinjer i avvikskøen, purringer
som feilet eller venter på en sats eller en gjennomgang, utskriftsbunker som ikke er
bekreftet postlagt, hver med de utskrevne brevene talt, og Vipps-betalinger som trenger en
person — der det er ukjent om pengene ble trukket, som ikke kunne trekkes, som står fast,
eller som ble refundert i Vipps-portalen — hver med lenke til fakturaen sin
([kortet på dashbordet](/nb/user/invoices/#kortet-på-dashbordet)); og fra Ansatte:
arbeidsavtaler som ikke er signert, prøvetider som går ut, midlertidige arbeidsforhold
over ett eller tre år, personer uten leder, nye vurderinger som skal gjøres, dokumenter
lageret ikke vil slette, terskler for antall ansatte og tjenestepensjon som ikke er
bekreftet, og fraværspunktene: søknader som venter på din avgjørelse,
sykefraværsoppfølging som forfaller, ferie igjen fra i fjor eller for lite ferie i
hovedferien, fravær noen godkjente selv, og sykdom i godkjent ferie.

Inne i en app lister sidemenyen appens områder. Mange av dem vises bare til den som har
den tilhørende rettigheten — og noen få, som **Hurtigfaktura** i Fakturaer, bare til den
som har flere rettigheter samtidig — så en kollegas sidemeny kan være lengre eller
kortere enn din:

| App | Områder |
| --- | --- |
| [Kunder](/nb/user/customers/) | Kunder, Kontakter, Oppfølginger |
| [Prosjekter](/nb/user/projects/) | Prosjekter, Mine oppgaver, Prosjektøkonomi |
| [Timer](/nb/user/time/) | Min uke, Godkjenning, Personer, Timeinnstillinger |
| [Utlegg](/nb/user/expenses/) | Mine utlegg, Godkjenning av utlegg, Refusjoner, Utleggsinnstillinger |
| [Ansatte](/nb/user/staff/) | Min side (fanen **Fravær** og, for en leder, **Mitt team**), Ansattoversikt, Fravær (kalenderen og godkjenningene), Ansatteinnstillinger |
| [Fakturaer](/nb/user/invoices/) | Fakturaer, Hurtigfaktura, Fakturajournal, Forfalt, Innbetalinger, Fakturainnstillinger |
| [Kommunikasjon](/nb/user/communications/) | Innboks, Kanaler, Blokkeringer |
| [Produkter](/nb/user/products/) | Produkter, Kategorier, Avgiftskategorier |
| [Energi](/nb/user/energy/) | Målepunkter |

Noen få skjermbilder hører til et område uten å ha en egen oppføring i sidefeltet, og
åpnes i stedet fra en knapp i områdets toppfelt — **Importer kunder**, for eksempel,
fra **Importer** i kundelisten ([Eksport og
import](/nb/user/customers/#eksport-og-import)).

## Søk

Søkefeltet i toppfeltet (eller hurtigtasten som vises i det) åpner et søk som hopper
rett til et hvilket som helst skjermbilde du har tilgang til, så du trenger sjelden
app-velgeren for et skjermbilde du vet navnet på.

## Kontoen din

Avatar-menyen øverst til høyre fører til **Innstillinger**, med profilen din (navn,
språk) og sikkerheten din (passord, tofaktorautentisering, passnøkler).
**Hjelp** i den samme menyen åpner denne dokumentasjonen, på ditt språk, som kopien
bygget inn i den Vantigo-en du bruker, så den beskriver alltid versjonen du har.

Er du eier av arbeidsområdet, fører den samme menyen til **Administrasjon av
arbeidsområdet**: brukere, invitasjoner, og roller og tilgang. En systemadministrator
ser i tillegg **Systemadministrasjon**.

Innlogging, invitasjoner, passord, tofaktorautentisering og passnøkler er dekket i [Kom i gang](/nb/user/getting-started/); brukere, invitasjoner og roller i [Administrasjon av arbeidsområdet](/nb/user/workspace-administration/).

## Språk

Vantigo snakker engelsk og norsk. Språket følger nettleseren din med mindre du velger
ett under Innstillinger → Profil; datoer, tall og valuta følger språket.
