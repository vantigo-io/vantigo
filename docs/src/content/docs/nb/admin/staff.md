---
title: Ansatte
description: Slå på Ansatte-modulen, gi folk tilgang til den, koble personer til brukerne sine, rettigheten for personopplysninger, e-posten om endret kontonummer, og innstillingene — forretningstidssonen og ansattnummerserien.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
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

## Koble personer til brukerne sine

En person ser sine egne opplysninger — Min side i Ansatte-appen,
`GET /api/v1/staff/me` — først når HR har koblet personen til brukeren de logger inn
som. Koblingen gjør den brukeren til personen selv for Ansatte og ingenting annet: den
gir ingen rettighet, og den setter aldri brukeren under Ansattes forvaltning. Enhver
aktiv bruker kan kobles, også Owner.

1. Som en bruker med `staff:manage` finner du brukeren:
   `GET /api/v1/staff/assignable-users?q=` med en del av navnet lister opptil tjue
   aktive brukere.
2. Koble dem: `PUT /api/v1/staff/people/{id}/user` med
   `{"userId": "<brukerens id>"}`. For å fjerne koblingen sender du
   `{"userId": null}`.
3. Kontroller: personen logger inn, og `GET /api/v1/staff/me` svarer med
   opplysningene deres i stedet for `no_person`. Personens historikk viser
   `user_linked`.

En bruker kan bare kobles til én person, og en person til én bruker; å koble en person
til en annen bruker erstatter koblingen. En deaktivert bruker kan ikke kobles.

## Rettigheten for personopplysninger

Fødselsnummeret og kontonummeret er forseglet: lagret kryptert, vist til ingen — ikke
til HR med `staff:manage`, ikke til personen — bortsett fra nummerets type og
kontoens fire siste sifre. Bare `staff:personal-data` skriver og viser dem, og hver
visning registreres i personens historikk med hvem som så. Gi `staff:personal-data` til
så få som lønnskjøringen trenger; den er en egen rolle ved siden av HR sin med vilje,
slik at det trengs to par hender for å endre hvor noens lønn går.

Verdiene er forseglet under nøkler utledet fra `APP_SECRET`. Å rotere den gjør dem
umulige å åpne og duplikatsjekken for fødselsnumre blind — se
[Installasjon](/nb/admin/installation/#innstillingene-du-må-ta-stilling-til).

## E-posten om endret kontonummer

Når et kontonummer settes eller endres, får personen en e-post — til den private
e-postadressen når de har en, ellers til jobbadressen, på det foretrukne språket — som
nevner kontoens fire siste sifre og ikke mer, så en endring noen andre har gjort ikke
går upåaktet hen. E-posten går gjennom installasjonens egen SMTP-server etter at
endringen er lagret. Å sende kontonummeret som allerede er lagret, på nytt endrer
ingenting og sender ingen e-post.

- Den krever `MAIL_DRIVER=smtp` og `SMTP_*`-innstillingene
  ([Installasjon](/nb/admin/installation/#e-post-og-observerbarhet)). Med en hvilken
  som helst annen driver hoppes e-posten over, og serveren logger
  `staff: bank-account change mail skipped: the mail driver is not smtp`.
- En person uten noen e-postadresse får ingen e-post, og serveren logger
  `staff: bank-account change mail skipped: the person has no email`.
- En sending som feiler, logges som en advarsel med personens id; selve endringen blir
  stående lagret.

## Innstillingene

Innstillingene er `staff:manage` sine, og leses og erstattes gjennom `GET` og
`PUT /api/v1/staff/settings`, og endres på Innstillinger-siden i Ansatte-appen
([Endre innstillingene](/nb/user/staff/#endre-innstillingene)). En erstatning bærer revisjonen den leste i `If-Match`, så to personer som
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
- **`user_inactive`.** Brukeren du kobler, er deaktivert. Aktiver den først, eller koble
  en annen.
- **`user_linked_elsewhere`.** Brukeren er koblet til en annen person. Fjern koblingen
  der først.
- **`no_person`.** Personen som ber om Min side, er ikke koblet til noen. Koble dem.
- **`national_id_duplicate`.** En annen person har allerede dette fødselsnummeret.
  Avvisningen sier ikke hvem; noen med `staff:personal-data` kan sammenligne ved å vise
  numrene.
- **Ingen e-post om endret kontonummer kom fram.** Se etter linjen over i loggen, og at
  personen har en e-postadresse.
