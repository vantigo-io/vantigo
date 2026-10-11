---
title: Ansatte
description: Slå på Ansatte-modulen, gi folk tilgang til den, og innstillingene — forretningstidssonen og ansattnummerserien.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
---

Ansatte er bedriftens register over menneskene som jobber for den: personopplysningene
deres, arbeidsforholdene og bedriftens struktur. Denne siden er driftssiden: å slå på
modulen, slippe folk inn, og innstillingene. Hva modulen gjør og reglene den sjekker,
står i [referansen](/en/reference/staff/).

## Før du begynner

- Ansatte trenger ingen annen forretningsmodul. Usatt `MODULES` slår den på sammen med
  alle andre moduler; en installasjon som lister `MODULES` legger `staff` til i
  listen, gjerne alene (`MODULES=staff`).
- Migreringen oppretter skjemaet `staff` enten modulen er slått på eller ikke, så å
  slå den på senere trenger ingen migrering. Hvis databasen kjører en
  applikasjonsrolle med minste privilegium, trenger den skjemaet `staff` tildelt som
  alle de andre ([Installasjon](/nb/admin/installation/)).

## Slå på modulen

1. Legg `staff` til i `MODULES` i `vantigo.env`, eller la `MODULES` stå usatt.
2. Start Vantigo på nytt (`docker compose up -d`).
3. Kontroller: `GET /api/v1/staff/meta` svarer 401 uten en sesjon, ikke 404 som en
   avslått moduls sti svarer.

En avslått modul bidrar ikke med noen rute og ingen rettighet; dataene dens blir
liggende urørt i databasen.

## Gi folk tilgang

Hver operasjon i Ansatte krever `staff:access`, og fire rettigheter ligger over den:

| Rettighet | For |
| --- | --- |
| `staff:access` | Alle som skal se sine egne opplysninger. |
| `staff:view` | Kolleger som skal se katalogen og bedriftens struktur. |
| `staff:approve` | Den som avgjør fravær (den gir ingenting før fravær kommer). |
| `staff:manage` | HR: personer, arbeidsforhold, strukturen og innstillingene. Sensitiv. |
| `staff:personal-data` | Den som får vise fødselsnumre og kontonumre. Sensitiv. |

Owner har dem alle allerede. Alle andre trenger en rolle som gir dem: under
**Administrer arbeidsområde → Roller og tilgang** oppretter eller redigerer du en rolle og
gir den rettighetene i kategorien «Staff»
([Administrer roller og tilgang](/nb/user/workspace-administration/#administrer-roller-og-tilgang)).
Ingen innebygd rolle har noen av dem.

## Innstillingene

Innstillingene er `staff:manage` sine, og leses og erstattes gjennom `GET` og
`PUT /api/v1/staff/settings`; Innstillinger-siden i Ansatte-appen kommer med resten av
modulen. En erstatning bærer revisjonen den leste i `If-Match`, så to personer som
endrer innstillingene samtidig ikke kan overskrive hverandre uten å merke det.

- **Forretningstidssone** (`timeZone`, standard `Europe/Oslo`). Hver dato i Ansatte —
  en start, en slutt, «i dag» — er en kalenderdag i denne sonen. Bruk en bysone som
  `Europe/Oslo`: et navn serveren og databasen leser forskjellig, som `CET`, avvises.
- **Prefiks og neste ansattnummer** (`employeeNumberPrefix`, tomt, og
  `nextEmployeeNumber`, 1). En person som opprettes uten ansattnummer får prefikset og
  det neste nummeret, uten utfylling: `EMP-` og 7 blir `EMP-7`. Prefikset er opptil
  åtte bokstaver, sifre eller bindestreker, og de to sammen kan bli høyst 16 tegn.
- **Pensjonsleverandør** og **yrkesskadeforsikrer** (`pensionProvider`,
  `injuryInsurer`). Leverandøren av obligatorisk tjenestepensjon (OTP) og
  yrkesskadeforsikringen, som kontrollen av arbeidsavtalen leser.

## Hva som kan gå galt

- **`timeZone` avvist.** Navnet er ukjent, eller serveren og databasen er uenige om det
  om vinteren eller om sommeren. Velg bysonen forkortelsen står for.
- **`employee_number_too_long`.** Prefikset og det neste nummeret blir til sammen mer
  enn 16 tegn. Kort ned prefikset, eller senk det neste nummeret.
- **`revision_conflict`.** Noen har endret innstillingene siden du leste dem. Les dem på
  nytt og gjenta endringen.
