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

Hver modul er en **app** med sin egen sidemeny: Kunder, Prosjekter, Timer, Kalender,
Utlegg, Ansatte, Fakturaer, Kommunikasjon, Produkter og Energi — og Microsoft Entra ID,
der en administrator kobler til selskapets leietaker. App-velgeren i toppfeltet lister appene
du kan åpne; **Hjem** er dashbordet, som åpner med agendaen **I dag og denne uken** fra
Kalender når du bruker Kalender ([dashbordet](/nb/user/calendar/#dashbordet)), og der
hver app du bruker viser sitt eget kort med
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
hovedferien, fravær noen godkjente selv, og sykdom i godkjent ferie — for den som har
`staff:personal-data` også HR-fravær som den eneste med den tilgangen godkjente for seg
selv, som åpner fraværsfanen til personen; og, for `staff:manage`, kontoene: hver
person Ansatte ikke kan opprette, aktivere eller holde oppdatert en konto for, med
grunnen — ikke noe arbeidsforhold eller ingen adresse å gi den fra, en konto en eier
har deaktivert, sluttet å la Ansatte styre eller som tilhører en annen, et nummer
eller en adresse en annen konto har, invitasjoner som ikke er besvart eller er
tilbakekalt, forsøk som feiler — som åpner kontofanen til personen, og kontoene som
venter på installasjonen (ingen roller Ansatte kan gi, en rolle blant dem som gir
administrasjon, en påloggingsmodus jobben er uenig i), som åpner innstillingene i
Ansatte, og hver gruppe som ikke er synkronisert med katalogen, som åpner gruppens
side.

Inne i en app lister sidemenyen appens områder. Mange av dem vises bare til den som har
den tilhørende rettigheten — og noen få, som **Hurtigfaktura** i Fakturaer, bare til den
som har flere rettigheter samtidig — så en kollegas sidemeny kan være lengre eller
kortere enn din:

| App | Områder |
| --- | --- |
| [Kunder](/nb/user/customers/) | Kunder, Kontakter, Oppfølginger |
| [Prosjekter](/nb/user/projects/) | Prosjekter, Mine oppgaver, Prosjektøkonomi |
| [Timer](/nb/user/time/) | Min uke, Godkjenning, Personer, Timeinnstillinger |
| [Kalender](/nb/user/calendar/) | Kalender (din egen, og en kollegas med velgeren), Min kalender (kalenderen din på mobilen, og kalendere fra andre steder) |
| [Utlegg](/nb/user/expenses/) | Mine utlegg, Godkjenning av utlegg, Refusjoner, Utleggsinnstillinger |
| [Ansatte](/nb/user/staff/) | Min side (fanen **Fravær** og, for en leder, **Mitt team**), Ansattoversikt, Fravær (kalenderen og godkjenningene), Grupper, Ansatteinnstillinger |
| [Fakturaer](/nb/user/invoices/) | Fakturaer, Hurtigfaktura, Fakturajournal, Forfalt, Innbetalinger, Fakturainnstillinger |
| [Kommunikasjon](/nb/user/communications/) | Innboks, Kanaler, Blokkeringer |
| [Produkter](/nb/user/products/) | Produkter, Kategorier, Avgiftskategorier |
| [Energi](/nb/user/energy/) | Målepunkter |
| [Microsoft Entra ID](/nb/user/entra/) | Microsoft Entra ID (tilkoblingens status og de seks stegene i oppsettet), for `entra:manage` |

Noen få skjermbilder hører til et område uten å ha en egen oppføring i sidefeltet, og
åpnes i stedet fra en knapp i områdets toppfelt — **Importer kunder**, for eksempel,
fra **Importer** i kundelisten ([Eksport og
import](/nb/user/customers/#eksport-og-import)), og **Importer målepunkter** fra
**Importer** i målepunktlisten ([Importer
målepunkter](/nb/user/energy/#importer-målepunkter)).

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
