---
title: Ansatte
description: Slå på Ansatte-modulen, gi folk tilgang til den, koble personer til brukerne sine, rettigheten for personopplysninger og helsedokumenter, dokumentkategoriene, å svare på en innsynsbegjæring, slettesperre, å ansette en slettet person på nytt, e-posten om endret kontonummer, og innstillingene — forretningstidssonen og ansattnummerserien.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
  - apps/server/internal/staff/document_categories.go
  - apps/server/internal/staff/personal_data.go
  - apps/server/internal/staff/legal_hold.go
  - apps/server/internal/staff/erased.go
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
- Personers dokumenter lagres i objektlageret, så å arkivere et dokument krever
  `STORAGE_PROVIDER=fs` og en lagringsrot
  ([Objektlagring](/nb/admin/object-storage/)). Uten det virker alt annet, og en
  opplasting eller nedlasting svarer 503 `storage_unavailable`.

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
| `staff:manage` | HR: personer, arbeidsforhold, dokumenter unntatt helsedokumenter, dokumentkategoriene, strukturen og innstillingene. Sensitiv. |
| `staff:personal-data` | Den som får vise fødselsnumre og kontonumre, og lese og arkivere helsedokumenter. Sensitiv. |

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

**Helsedokumenter** — en persons dokumenter i en begrenset kategori, blant
standardkategoriene `health` — er også `staff:personal-data` sine. HR med bare
`staff:manage` ser dem ikke i personens arkiv, kan ikke åpne, arkivere, flytte eller
fjerne et, og ser ikke i personens historikk at et ble lagt til; personen selv leser
sine egne. Gi `staff:personal-data` til den som håndterer sykmeldinger og korrespondanse
med bedriftshelsetjenesten, og ingen andre.

## Dokumentkategorier

Hvert dokument arkiveres under en kategori, og kategorien avgjør når dokumentet slettes:
dens **oppbevaringsklasse** — A for lønns- og avtaledokumentasjon (fem år etter året
arbeidsforholdet sluttet), D for personaladministrasjon (sammen med resten av
personens opplysninger), E for helse. Seks kategorier finnes fra start: arbeidsavtale og
attest (A), sertifikat, korrespondanse og annet (D), og helse (E). En kategori i klasse
E er begrenset: dokumentene i den er helsedokumenter.

Slik legger du til en egen, som en bruker med `staff:manage`:

1. `POST /api/v1/staff/document-categories` med en `key` (1–40 av `a–z`, `0–9` og
   bindestreker), `names` på engelsk og norsk og en `class` som er `A`, `D` eller `E`,
   for eksempel `{"key": "courses", "names": {"en": "Courses", "nb": "Kurs"}, "class": "D"}`.
   Velg klassen med omhu: den endres aldri, fordi slettingen leser den fra hvert dokument
   som er arkivert under kategorien.
2. Kontroller: `GET /api/v1/staff/document-categories` lister den etter
   standardkategoriene.

For å gi en nytt navn, eller slutte å tilby den, sender du `names` og `archived` til
`PUT /api/v1/staff/document-categories/{id}` med revisjonen i `If-Match`. En arkivert
kategori tilbys ikke for nye dokumenter, og bare en kategori ingen dokumenter er arkivert
under, kan arkiveres. De seks standardkategoriene endres aldri.

## Svar på en innsynsbegjæring

En person har rett til en kopi av personopplysningene som behandles om dem
(personvernforordningen art. 15, innsyn). Ansattes fullstendige svar er
personopplysningsfilen HR henter ut:

1. Som en bruker med `staff:personal-data`, `GET /api/v1/staff/people/{id}/personal-data`.
   Svaret er en nedlasting, `staff-<ansattnummer>-personal-data.json`: personens
   opplysninger i alle feltgrupper, fødselsnummeret og kontonummeret i sin helhet, HRs
   notater, pårørende, rollene, hvert arbeidsforhold med vilkårene (lønn og notater
   medregnet), brukerkoblingen, listen over dokumenter og hele historikken.
2. Last ned hvert dokument filen lister gjennom `GET /api/v1/staff/documents/{id}`;
   filen har metadataene deres, ikke innholdet.
3. Kontroller: personens historikk begynner nå med **Personopplysninger eksportert**,
   med deg som den som gjorde det. Hver slik eksport registreres, fordi filen åpner de
   forseglede verdiene.

Overlever filen slik bedriften overleverer personopplysninger — ikke med vanlig
e-post. Personen kan også laste ned sin egen kopi fra Min side
(`GET /api/v1/staff/me/export`); den utelater de forseglede verdiene, HRs notater og
notatene på vilkårene, så den er ikke det fullstendige svaret, og den registrerer
ingenting.

## Sett en slettesperre på en person

En slettesperre (legal hold) holder en persons opplysninger hele — ingen sletting etter
oppbevaringsreglene rører dem, og personen kan ikke slettes — så lenge en tvist, et krav
eller en undersøkelse trenger det.

1. Som en bruker med `staff:personal-data`, `PUT /api/v1/staff/people/{id}/legal-hold`
   med `{"held": true, "reason": "…"}`. Grunnen er påkrevd, inntil 500 tegn; skriv hva
   sperren gjelder og gjerne hvor lenge.
2. Kontroller: personens `legalHold` er `{"held": true, …}` med grunnen og tidspunktet,
   og historikken har **Slettesperre satt**.
3. Når saken er avsluttet, opphever du den med `{"held": false}` (uten grunn). Grunnen og
   hvem som satte den, fjernes; historikken beholder begge.

En slettet person kan fortsatt få og miste en slettesperre; ingenting annet ved dem
endres.

## Ansett en slettet person på nytt

Når oppbevaringskjøringen har slettet en tidligere ansatt, er opplysningene skrivebeskyttet:
hver endring svarer `person_erased`, bortsett fra en slettesperre, en revurdering, å
fjerne koblingen til brukeren og en nyansettelse.

1. Før du legger til noen som kan ha jobbet her før, spør
   `POST /api/v1/staff/people/similar-erased` (`staff:manage`) med
   `{"givenName", "familyName", "birthDate"}`. Slettede personer med samme navn og
   fødselsdato listes med ansattnummer og når de ble slettet. Fødselsnummeret kan ikke
   avgjøre det — slettingen fjernet det.
2. Er det samme person, legger du det nye arbeidsforholdet til de slettede
   opplysningene: `POST /api/v1/staff/people/{id}/employments`. Opplysningene åpnes igjen
   under samme ansattnummer; historikken får **Ansatt på nytt** før det nye
   arbeidsforholdet.
3. Skriv inn igjen det slettingen fjernet — kontaktopplysninger, adresse, pårørende,
   fødselsnummeret og kontonummeret (de to siste med `staff:personal-data`).
4. Er det en annen, legger du dem til som en ny person; de slettede opplysningene blir
   stående.

For å koble personens bruker til nye opplysninger i stedet, fjerner du først koblingen
fra de slettede (`PUT /api/v1/staff/people/{id}/user` med `{"userId": null}`) — den ene
endringen av koblingen slettede opplysninger tillater.

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
- **`storage_unavailable`.** Objektlageret kunne ikke skrives til eller leses fra:
  lagring er ikke konfigurert, eller roten er ikke skrivbar. Ingenting ble registrert;
  rett opp lagringen og prøv igjen ([Objektlagring](/nb/admin/object-storage/)).
- **`seed_immutable`.** En av de seks standardkategoriene kan ikke endres. Legg til en
  egen kategori i stedet.
- **`category_in_use`.** Det er fortsatt dokumenter arkivert under kategorien du
  arkiverer. Flytt dem til en annen kategori først.
- **`document_count_exceeded`.** Personen har 200 dokumenter, det meste én person kan
  ha — talt uansett hva du får se, så det kan omfatte helsedokumenter du ikke ser. Fjern
  dokumenter som ikke lenger trengs.
- **`person_erased`.** Oppbevaringskjøringen har slettet personen, og opplysningene er
  skrivebeskyttet. Ansett dem på nytt med et nytt arbeidsforhold, eller la dem være.
- **`legal_hold`.** Personen du sletter, har en slettesperre. Opphev den først, hvis
  saken er avsluttet.
- **`reassessment_not_due`.** Personen er ikke slettet, er allerede revurdert, eller det
  siste arbeidsforholdet sluttet for mindre enn tolv måneder siden.
