---
title: Ansatte
description: Slå på Ansatte-modulen, gi folk tilgang til den, koble personer til brukerne sine, rettigheten for personopplysninger og helsedokumenter, dokumentkategoriene, å svare på en innsynsbegjæring, slettesperre, å ansette en slettet person på nytt, å importere og eksportere registeret, å eksportere en måned til lønn, oppbevaring — slettingen og sletteloggen — tersklene for antall ansatte og tjenestepensjon, fraværstyper og a-meldingskodene deres, ferieordninger, reglene for egenmelding og automatisk overføring, å overføre ferie, å rette et registrert sykefravær, å trekke tilbake retten til egenmelding, å følge en sykefraværssak, omsorgsdagfeltene og omsorgsdagene, hvem som avgjør fravær og e-postene om fravær, e-posten om endret kontonummer, og innstillingene — forretningstidssonen, ansattnummerserien og oppbevaringstidene.
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
  - apps/server/internal/staff/people_import.go
  - apps/server/internal/staff/people_export.go
  - apps/server/internal/staff/csvfile.go
  - apps/server/internal/staff/payroll.go
  - apps/server/internal/staff/retention.go
  - apps/server/internal/staff/retention_worker.go
  - apps/server/internal/staff/object_deletions.go
  - apps/server/internal/staff/thresholds.go
  - apps/server/internal/staff/absence_types.go
  - apps/server/internal/staff/holiday_schemes.go
  - apps/server/internal/staff/absence_settings.go
  - apps/server/internal/staff/adjustments.go
  - apps/server/internal/staff/deciders.go
  - apps/server/internal/staff/notifications.go
  - apps/server/internal/staff/cases.go
  - apps/server/internal/staff/sickness.go
  - apps/server/internal/staff/care_days.go
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
| `staff:approve` | Den som avgjør alles fravær: godkjenner, avslår og kansellerer søknader, ser alles arbeidsopplysninger og fravær (en HR-type som permisjon), godkjenningsinnboksen og kalenderen — ikke lønn, private opplysninger eller historikk. |
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

Tabellen under **Ansatteinnstillinger** → **Dokumentkategorier** gjør alt dette på
skjermen ([brukerveiledningen](/nb/user/staff/#dokumentkategorier)). Gjennom API-et legger
du til en egen slik, som en bruker med `staff:manage`:

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

## Fraværstyper

Fravær registreres i et vokabular av typer: ferie, egenmelding, sykmelding, de
lovfestede permisjonene, avspasering, lege- eller tannlegetime og resten — 22
standardtyper som hver installasjon starter med, hver knyttet til permisjonsbeskrivelsen
den rapporteres som i a-meldingen ([referansen](/en/reference/staff/#absence-types)).
Selve fraværet kommer med et senere steg i fraværsleveransen; typene, ferieordningene og
reglene under kan settes opp allerede nå. Gjennom API-et, som en bruker med
`staff:manage`:

1. Les listen: `GET /api/v1/staff/absence-types` svarer med alle typene, de skjulte også
   for deg.
2. For å legge til en egen, `POST /api/v1/staff/absence-types` med en `key` (1–40 av
   `a–z`, `0–9` og understreker), `names` på engelsk og norsk, en `kind` — `holiday`,
   `leave`, `time_off` eller `other` — en `unit` (`days` eller `hours`), flaggene `paid`,
   `drawsHoliday`, `allowsPercent` og `needsDecision`, en `visibility` (`absent`, eller
   `hr_only` for en type som avslører særlige kategorier av personopplysninger) og, for
   en permisjon, `ameldingLeave` den rapporteres som. Typen og enheten endres aldri.
3. For å gi en type nytt navn, flytte den eller slutte å tilby den, sender du den tilbake
   til `PUT /api/v1/staff/absence-types/{id}` med `hidden` og `sort` slik du vil ha dem og
   revisjonen i `If-Match`. En standardtype endrer bare navnene, plassen og om den er
   skjult; alt annet avvises med `seed_immutable`. En egen type kan når som helst gjøres
   til bare for HR, men når noen har tatt den som bare for HR, kan den ikke åpnes for
   kolleger og ledere (`absence_type_in_use`): legg heller til en ny type. Å gjøre en
   type som er i bruk til bare for HR virker bakover: fra da av viser radene som alt er
   lagt inn, bare *permisjon* for ledere, og søknadene, avslagene og historikken deres
   skjules også for dem.
4. Kontroller: `GET /api/v1/staff/meta` lister typene folk kan velge — uten de skjulte.

**Velg a-meldingskoden etter permisjonens rettsgrunnlag.** A-meldingen tar imot seks
permisjonsbeskrivelser og permittering. Velferdspermisjon er avtalt, ikke lovfestet, så
standardtypen `velferdspermisjon` rapporteres som «andre ikke lovfestede permisjoner»
(`andreIkkeLovfestedePermisjoner`); hviler velferdspermisjonen deres på en lov, bruker
dere typen `annen_lovfestet` for den i stedet. Kodene `velferdspermisjon` og
`utdanningspermisjon` gikk ut ved utgangen av 2022 og avvises. Bare permisjon på mer enn
fjorten dager rapporteres — og flere kortere permisjoner innenfor tolv måneder som til
sammen passerer fjorten dager, telles sammen — og omsorgsdager for sykt barn rapporteres
aldri.

## Ferieordninger

En ferieordning er bedriftens ferieregel, i virkedager (mandag til lørdag, slik
ferieloven teller). Den lovfestede ordningen — 25 dager, 6 til fra 60, 18 i
hovedferieperioden, 6 ved start etter 30. september, 12 dagers avtalt overføring —
finnes fra start og er standardordningen. Hver ordning svares med tallene omregnet til
arbeidsdager i en femdagersuke: 25 virkedager er 21 dager, 30 er 25.

For å gi noen eller alle ansatte mer, som en bruker med `staff:manage`:

1. `POST /api/v1/staff/holiday-schemes` med et `name` og tallene, for eksempel
   `{"name": "Fem uker", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6}`
   (hovedferieperioden, dagene etter september og overføringen er 18, 6 og 12 når de
   utelates). Ferielovens grenser gjelder: minst 18 dager i hovedferieperioden, minst 6
   ved start etter 30. september, og avtalt overføring på høyst 12 dager pluss dagene
   over 25 — 17 for en ordning på 30 dager. Legg til `"isDefault": true` for å gjøre den til ordningen for hvert
   arbeidsforhold som ikke nevner noen.
2. Sett et arbeidsforhold på den: `holidaySchemeId` på
   `POST /api/v1/staff/people/{id}/employments` eller `PUT /api/v1/staff/employments/{id}`.
   Utelatt følger arbeidsforholdet standardordningen.
3. Kontroller: `GET /api/v1/staff/holiday-schemes` lister den, med `converted`.

For å slutte å tilby en ordning sender du den til `PUT /api/v1/staff/holiday-schemes/{id}`
med `"archived": true`. Standardordningen kan ikke arkiveres, og heller ikke en ordning et
arbeidsforhold følger (`holiday_scheme_in_use`): flytt arbeidsforholdene først. Det finnes
alltid nøyaktig én standardordning; gjør en annen til standard for å flytte den.

## Reglene for egenmelding

Hvor mye egenmelding en ansatt kan bruke, er installasjonens regel, på
`PUT /api/v1/staff/settings` (`staff:manage`):

- `selfCertPolicy: "minimum"` — lovens regel: 3 kalenderdager om gangen, 4 ganger på
  tolv måneder. Standard.
- `selfCertPolicy: "extended"` — 8 kalenderdager om gangen, 24 dager på tolv måneder,
  slik en IA-avtale gir.
- `selfCertPolicy: "custom"` med `selfCertDaysPerOccurrence` (3–16),
  `selfCertOccurrencesPerYear` (4–52) og `selfCertDaysPerYear` (12–365). Bunnen er
  lovens minimum (ftrl. § 8-23), som ingen regel går under.

Med en forhåndsinnstilling gjelder dens tall, uansett hva du sender.
`selfCertAfterMonths` (0–2, standard 2) er månedene i arbeid før egenmelding kan brukes,
under alle reglene. Reglene brukes på hver egenmelding når den registreres: en som går
forbi det de tillater, avvises, og den ansatte får beskjed om å skaffe sykmelding. En
egenmeldingsperiode er dagene på rad, og en helg eller helligdag mellom to egenmeldinger
teller som dager i den — med minimumsregelen er egenmelding fredag og igjen mandag fire
dager. Dager en sykmelding dekker mellom to egenmeldinger, binder dem også sammen, som
én sykdom, uten å telle som egenmeldte dager.

**Automatisk overføring.** `autoCarryOver: true` overfører ferie som står igjen ved
årsskiftet til neste år uten at HR gjør noe, som tvungen overføring. Dagen du slår den på
lagres (`autoCarryOverSince`), og ingen år før den overføres automatisk. Den er av som
standard.

## Overfør ferie

Ferie som står igjen ved årsskiftet, forsvinner ikke. HR overfører den til neste år med
`POST /api/v1/staff/people/{id}/carry-over` (`staff:manage`):

1. Les personens konto for året den kommer fra:
   `GET /api/v1/staff/people/{id}/holiday-account?year=2025` — `remaining` er det som står
   igjen.
2. Registrer overføringen med `fromYear` og `kind`:
   - `agreed` — det du og den ansatte avtalte, begrenset av ferieordningens maksimum
     (12 virkedager, 10 arbeidsdager, etter lovens ordning). Utelat `days` for å overføre
     dagene som står igjen, opp til grensen.
   - `forced` — dager den ansatte ikke kunne ta (sykdom i ferien, for eksempel), uten
     grense, med en `note` som sier hvorfor.
3. Se på kontoen for neste år: dagene står i `carriedIn`.

En person har **høyst én overføring fra et år**: en andre gir `carry_over_recorded`. For
å endre den, slett den (`DELETE /api/v1/staff/holiday-adjustments/{id}`; listen er
`GET /api/v1/staff/people/{id}/holiday-adjustments`) og registrer den på nytt. En
rettelse av en konto — en dag registrert feil, for eksempel — er en justering med
`reason: "correction"` og en merknad; en justering som er en overføring, oppgir året
rett før året den endrer.

**Automatisk overføring.** Med `autoCarryOver` slått på i innstillingene overfører en
konto selv dagene som stod igjen året før (`carriedInDerived`), som tvungen overføring —
men bare til et år der 1. januar er samme dag som eller etter dagen den ble slått på:
slått på 12. september 2026 overfører den først 2026s dager til 2027. Den rekker aldri
inn i et år som er i gang, der HR har gjort opp overføringen for hånd. En overføring registrert for hånd for et år erstatter den
automatiske.

## Rett et registrert sykefravær

En egenmelding eller en sykmelding er registrert, ikke avgjort, og den ansatte kan ikke
endre den når den først er lagt inn: slutter en sykmelding tidligere, eller var en dag
feil, endrer HR den (`PUT /api/v1/staff/absences/{id}`, `staff:manage`, med revisjonen i
`If-Match`) eller fjerner den (`DELETE /api/v1/staff/absences/{id}`). Begge føres i
personens historikk. En endring holder raden en registrering: å gjøre en sykmelding om
til ferie, for eksempel, avvises (`absence_status_change`) — fjern den og legg inn
ferien. Fraværsendringer er `staff:manage` sine; `staff:personal-data` alene skriver dem
ikke. Et fravær noen har avgjort, fjernes aldri; det trekkes tilbake
eller rettes (`POST /api/v1/staff/absences/{id}/correct`). En endring i et sykefravær
utleder personens sykefraværssak på nytt — dagene, arbeidsgiverperioden og datoene for
milepælene — og beholder det som er markert på milepælene.

## Trekk tilbake retten til egenmelding

Når en ansatt har brukt egenmelding slik at det gir grunn til det (ftrl. § 8-27), kan HR
trekke tilbake retten for en periode:

1. `PUT /api/v1/staff/people/{id}/self-certification/withdrawal` (`staff:manage`) med
   `until` — en dag etter i dag, høyst ett år fram — og `reason`.
2. Sjekk `GET /api/v1/staff/people/{id}/self-certification`: `withdrawnUntil` og
   `withdrawnReason` står der.
3. Fra i dag og fram til den dagen avvises egenmelding for personen
   (`self_certification_withdrawn`); vedkommende registrerer sykmelding i stedet.
   Egenmelding for dager før du registrerte tilbaketrekkingen, står seg.

For å oppheve den før tiden, send `{"until": null}`. Tilbaketrekkingen og opphevingen
står i personens historikk for personen selv, HR og `staff:personal-data` — aldri for
lederen, som ser personens egenmeldingsstatus uten den.

## Følg en sykefraværssak

Hver egenmelding og sykmelding hører til en sykefraværssak, som Ansatte holder selv: et
sykefravær som begynner høyst seksten dager etter det forrige, hører til samme sak, og
ett som begynner senere, åpner en ny. `GET /api/v1/staff/cases` viser de åpne sakene du
følger — personens leder ser sine medarbeideres, HR og den som har `staff:approve`, alle.

1. **Arbeidsgiverperioden.** Milepælen `employer_period_end` er sakens sekstende
   sykedag, der helger og helligdager mellom to sykefravær telles med; før det har den
   ingen dato. Arbeidsgiveren betaler de dagene når den ansatte har arbeidet der i fire
   uker.
2. **Oppfølgingen.** Oppfølgingsplanen forfaller fire uker etter første dag, første
   dialogmøte etter sju, NAVs aktivitetskrav etter åtte og andre dialogmøte etter
   tjueseks. En milepæl vises som aktuell bare mens saken er åpen og har vart til en uke
   før den, så et kort sykefravær gir ingenting å følge opp. Merk en som gjort
   (`POST /api/v1/staff/cases/{id}/milestones/{kind}/done`, i dag hvis du ikke sender
   `doneAt`, aldri før sakens første dag) eller frafall den som åpenbart unødvendig med
   en begrunnelse (`.../waive`) — aldri en diagnose; bare den ansatte og
   `staff:personal-data` leser begrunnelsen. Lederen, HR og `staff:approve` kan det.
   Slutten på arbeidsgiverperioden følger av sykedagene og markeres ikke.
3. **Notater.** Tilretteleggingsnotatet (`PUT /api/v1/staff/cases/{id}`) gjelder
   tilrettelegging — hva den ansatte kan og ikke kan — og aldri en diagnose; lederen og
   HR skriver det. En milepæls eget notat er `staff:personal-data` sitt, og bare den
   ansatte og `staff:personal-data` leser det. Et helsedokument arkiveres under saken
   med `caseId` på `PUT /api/v1/staff/documents/{id}`.
4. **52-ukersgrensen** anslås som første dag pluss 364 dager; HR setter NAVs dato med
   `maxBenefitEstimate` og sender den som null for å gå tilbake til anslaget. Et felt du
   utelater i `PUT /api/v1/staff/cases/{id}`, beholdes. NAV teller de 248
   sykepengedagene på tvers av sykefravær med mindre den ansatte har vært arbeidsfør i
   26 uker imellom (ftrl. § 8-12), så en tidligere sak kan flytte grensen fram.
5. **Avslutningen.** En sak avsluttes av seg selv seksten dager etter siste dag. HR
   avslutter den tidligere med `POST /api/v1/staff/cases/{id}/close`; et sykefravær som
   hører til den senere, åpner den igjen.

Når et sykefravær fyller gapet mellom to saker, blir de to én: den eldste beholder id-en
sin og tar over den andres markerte milepæler og notater, lagt etter sine egne.

En sykmelding over en egenmelding korter egenmeldingen inn, eller fjerner den når den
dekker den helt.

## Utsett ferie ved sykdom

En sykmelding over innvilget ferie endrer ikke ferien av seg selv: om ferien utsettes,
er den ansattes krav, med legeerklæring (ferieloven § 9). Imens viser dashbordet ditt
ferien under *ferie berørt av sykdom*.

1. Sjekk at sykmeldingen er 100 %: en gradert sykmelding gir ikke rett til utsettelse.
2. Når den ansatte krever det, registrer kravet på sykmeldingen:
   `POST /api/v1/staff/absences/{id}/postpone-holiday` (`staff:manage`) med
   `demandedOn`, dagen kravet ble satt fram (i dag hvis den utelates).
3. Ansatte deler ferien rundt sykmeldingen, og dagene går tilbake til feriekontoen;
   svaret viser ferien slik den nå står, og punktet forsvinner fra dashbordet.

Sykdom som begynte **før** ferien, må være krevd senest siste arbeidsdag før den; et
senere krav avvises (`holiday_postponement_too_late`). Sykdom **i** ferien gir
erstatningsdager når enn kravet kommer. En sykmelding over ferie som bare er søkt om,
lar søknaden stå; godkjenningen avvises da.

## Omsorgsdagfeltene

En persons omsorgsdager utledes av tre felt på personen, som HR fyller inn ut fra det
den ansatte opplyser — ingen barn navngis:

- **Barn under 12** (`childrenUnder12`) — barna til og med året de fyller tolv, som
  **ikke** har vedtak fra NAV om utvidet rett.
- **Kronisk syke eller funksjonshemmede barn** (`chronicallyIllChildren`) — barna **med**
  vedtak fra NAV om utvidet rett, til og med året de fyller atten.
- **Alene om omsorgen** (`soleCarer`) — om den ansatte har omsorgen alene.

Tell hvert barn én gang: et barn med vedtak fra NAV føres bare i det andre feltet.
Feltene skrives sammen med resten av personen (`PUT /api/v1/staff/people/{id}`); den
ansatte ser dem på sin egen side, men kan ikke endre dem.

Telleren er `GET /api/v1/staff/people/{id}/care-days?year` (den ansatte, HR og
`staff:personal-data`): 10 dager for ett eller to barn, 15 for tre eller flere, 20 og
30 for den som er alene om omsorgen, og 10 til per kronisk sykt eller funksjonshemmet
barn (20 for den som er alene). Årets arbeidsdager med sykt barn telles mot den; de
første ti betaler arbeidsgiveren selv, resten refunderer NAV — fra første dag når de
eneste barna som gir rett, er over tolv med utvidet rett, noe lønn bruker for hånd. Mer
enn retten vises, men avvises aldri. Retten krever fire ukers ansettelse — regnet fra
starten av en sammenhengende rekke ansettelser, der en som begynner dagen etter at en
annen sluttet, fortsetter den: før det sier svaret `eligible: false` og fra hvilken
dag, og en dag med sykt barn før det telles for seg (`ineligibleDays`), ikke som
omsorgsdag.

## Svar på en innsynsbegjæring

En person har rett til en kopi av personopplysningene som behandles om dem
(personvernforordningen art. 15, innsyn). Ansattes fullstendige svar er
personopplysningsfilen HR henter ut:

1. Som en bruker med `staff:personal-data` åpner du personen under **Personer**, trykker
   **Last ned personopplysninger** øverst på siden og bekrefter
   ([brukerveiledningen](/nb/user/staff/#last-ned-en-persons-opplysninger);
   `GET /api/v1/staff/people/{id}/personal-data`). Svaret er en nedlasting,
   `staff-<ansattnummer>-personal-data.json`: personens opplysninger i alle
   feltgrupper, fødselsnummeret og kontonummeret i sin helhet, HRs notater, pårørende,
   rollene, hvert arbeidsforhold med vilkårene (lønn og notater medregnet),
   brukerkoblingen, listen over dokumenter og hele historikken.
2. Last ned hvert dokument filen lister, fra personens fane **Dokumenter** (eller
   `GET /api/v1/staff/documents/{id}`); filen har metadataene deres, ikke innholdet.
3. Kontroller: personens **Historikk** begynner nå med **Personopplysninger
   eksportert**, med deg som den som gjorde det. Hver slik eksport registreres, fordi
   filen åpner de forseglede verdiene.

Overlever filen slik bedriften overleverer personopplysninger — ikke med vanlig
e-post. Personen kan også laste ned sin egen kopi fra Min side
(`GET /api/v1/staff/me/export`); den utelater de forseglede verdiene, HRs notater og
notatene på vilkårene, så den er ikke det fullstendige svaret, og den registrerer
ingenting.

## Sett en slettesperre på en person

En slettesperre (legal hold) holder en persons opplysninger hele — ingen sletting etter
oppbevaringsreglene rører dem, og personen kan ikke slettes — så lenge en tvist, et krav
eller en undersøkelse trenger det.

1. Som en bruker med `staff:personal-data` åpner du personen og trykker **Sett
   slettesperre** øverst på siden. Skriv **Grunn** — påkrevd, inntil 500 tegn: hva
   sperren gjelder og gjerne hvor lenge — og trykk **Sett slettesperren**
   ([brukerveiledningen](/nb/user/staff/#en-slettesperre);
   `PUT /api/v1/staff/people/{id}/legal-hold` med `{"held": true, "reason": "…"}`).
2. Kontroller: siden viser **Under slettesperre** med dagen og grunnen (personens
   `legalHold` er `{"held": true, …}`), og **Historikk** har **Slettesperre satt**.
3. Når saken er avsluttet, trykker du **Opphev slettesperren** og bekrefter
   (`{"held": false}`, uten grunn). Grunnen og hvem som satte den, fjernes; historikken
   beholder begge.

En slettet person kan fortsatt få og miste en slettesperre; ingenting annet ved dem
endres.

## Ansett en slettet person på nytt

Når oppbevaringskjøringen har slettet en tidligere ansatt, er opplysningene skrivebeskyttet:
hver endring svarer `person_erased`, bortsett fra en slettesperre, en ny vurdering, å
fjerne koblingen til brukeren og en nyansettelse. Personsiden sier det i en melding og
tilbyr ikke noe annet ([brukerveiledningen](/nb/user/staff/#en-slettet-person)).

1. Før du legger til noen som kan ha jobbet her før, fyller du inn **Fornavn**,
   **Etternavn** og **Fødselsdato** i **Ny person** (`staff:manage`). Ved **Lagre og
   fortsett** lister skjemaet slettede personer med samme navn og fødselsdato, med
   ansattnummer og når de ble slettet (`POST /api/v1/staff/people/similar-erased` med
   `{"givenName", "familyName", "birthDate"}`). Fødselsnummeret kan ikke avgjøre det —
   slettingen fjernet det.
2. Er det samme person, trykker du **Åpne** med ansattnummeret, går til
   **Arbeidsforhold** og trykker **Ansett på nytt**
   (`POST /api/v1/staff/people/{id}/employments`). Opplysningene åpnes igjen under samme
   ansattnummer; historikken får **Ansatt på nytt** før det nye arbeidsforholdet.
3. Skriv inn igjen det slettingen fjernet — kontaktopplysninger, adresse, pårørende,
   fødselsnummeret og kontonummeret (de to siste med `staff:personal-data`).
4. Er det en annen, trykker du **Opprett likevel**; de slettede opplysningene blir
   stående.

For å koble personens bruker til nye opplysninger i stedet, trykker du først **Koble
fra** under Selvbetjening på de slettede (`PUT /api/v1/staff/people/{id}/user` med
`{"userId": null}`) — den ene endringen av koblingen slettede opplysninger tillater.

## Importer og eksporter registeret

Registeret flyttes som én semikolonseparert CSV-fil — formen et norsk Excel åpner og
lagrer som CSV uten en importdialog. Alle tre stegene krever `staff:manage` og ligger på
kortet **Importer og eksporter personer** i **Ansatteinnstillinger**
([brukerveiledningen](/nb/user/staff/#importer-og-eksporter-personer)); hvert av dem er
også et endepunkt, nevnt nedenfor. Kolonnene, reglene og kodene står i
[referansen](/en/reference/staff/#the-registers-import-and-export).

### Importer et register

1. Last ned malen: **Last ned mal**
   (`GET /api/v1/staff/people/import-template.csv`). Den er overskriftsraden alene — personens kolonner, det første arbeidsforholdets, og de forseglede bare hvis du
   har `staff:personal-data`. Eller start fra en eksport.
2. Fyll inn én rad per person. `employeeNumber` er påkrevd på hver rad: et nummer ingen
   har, legger til personen, et nummer en person har, endrer dem. For en ny person fyller
   du inn `startDate` for å legge til det første arbeidsforholdet også — `businessUnit`
   med navn eller organisasjonsnummer (tomt er standardenheten), `department` med koden,
   `location` med navnet, `managerEmployeeNumber` med lederens ansattnummer. En leder
   filen legger til, må komme på en tidligere rad. En person som allerede er i registeret,
   får bare sine egne kolonner endret — aldri et arbeidsforhold: dem endrer du i appen.
3. Lagre den som CSV UTF-8 med semikolon, og sjekk den: **Importer personer**, velg
   filen, **Kontroller** (`POST /api/v1/staff/people/import` med filen i
   multipart-delen `file`). Kontrollen er en **prøvekjøring**: hver rad kjøres og rulles
   tilbake, og siden viser hva radene ville gjort — opprettet, oppdatert, med feil — og
   hvert problem med rad og kolonne. Uten `dryRun=false` gjør endepunktet det samme.
4. Rett det som feilet, og kontroller igjen. Når kontrollen er ren, trykker du
   **Importer**, som sender den samme filen med `?dryRun=false`.
5. Kontroller: tallene er de du venter, personene er i listen, og historikken til hver
   person viser importens endringer under ditt navn.

En rad som feiler, skriver ingenting og stopper ikke de andre; rader som allerede er
importert, blir stående, så send en rettet fil med bare de feilede radene — **Last ned
feilede rader** gir deg dem tilbake slik de var skrevet, bak en `error`-kolonne importen
ser bort fra — eller hele filen på nytt: rader som ikke endrer noe, skriver ingenting. En import som avbrytes
underveis — forbindelsen brytes midt i filen — beholder radene den kjørte, og
installasjonens historikk registrerer den likevel med tallene så langt: `rows` er hele
filen, og de opprettede, endrede og feilede radene blir til sammen færre. Filen er høyst 5 MiB og
`STAFF_IMPORT_MAX_ROWS` rader (5000 med mindre du har hevet det,
[konfigurasjonsreferansen](/nb/admin/authentication/#konfigurasjonsreferanse)).

### De forseglede kolonnene

`nationalIdKind`, `nationalIdCountry`, `nationalId`, `bankAccountKind` og `bankAccount`
krever `staff:personal-data` i tillegg. En fil med noen av dem fra noen uten den, avvises
i sin helhet (`import_sealed_columns`), og importen sier fra med ord — fjern kolonnene
eller be om rettigheten. En
utfylt celle setter verdien med de samme kontrollene som på personens opplysninger; en
tom celle lar verdien som er lagret, stå. Et kontonummer importen endrer, gir personen en
e-post, slik en endring for hånd gjør ([E-posten om endret kontonummer](#e-posten-om-endret-kontonummer)),
når raden deres er lagret — aldri i en prøvekjøring. Ingen svar, logglinje eller
historikkoppføring har et siffer av noen av verdiene.

E-posten sendes rad for rad, etter hvert som hver rad lagres, og importen venter på hver
sending: en import av hundre nyansatte med kontonummer sender hundre e-poster — et
første kontonummer gir også e-post, slik det gjør for hånd — og tar tilsvarende lengre
tid.

En rad kan ikke endre en persons `birthDate` og `nationalId` samtidig: importen skriver
personens kolonner først, og kontrollerer den nye fødselsdatoen mot nummeret som er
lagret, som fortsatt koder den gamle — raden avvises med
`national_id_birth_date_mismatch`. Gjør endringen i to filer, slik du ville gjort for
hånd: først det nye `nationalId` med tom `birthDate`-celle, deretter den nye
`birthDate`.

### Eksporter registeret

**Eksporter personer** (`GET /api/v1/staff/people/export.csv`) laster ned
`staff-people-<i dag>.csv`: hver person —
slettede også — med jobb- og privatgruppen og hvor hver står i dag. Fødselsnummeret og
kontonummeret er aldri med: `nationalIdOnFile` sier `yes` eller `no`. Hver eksport
registreres i installasjonens historikk med ditt navn og antall rader. Filen inneholder
personopplysninger: oppbevar den der du oppbevarer registeret, og slett den når du er
ferdig.

En eksport importeres tilbake uendret: kolonnene etter privatgruppen ignoreres, så å
endre personens kolonner i en eksport og importere den er en måte å endre mange
personer på én gang. Raden til en slettet person kommer også tilbake og skriver
ingenting; endrer du den, avvises raden med `person_erased`.

## Eksporter en måned til lønn

Lønnsfilen gir lønnssystemet en måneds arbeidsforhold. Du trenger `staff:manage`.

1. I **Ansatteinnstillinger**, under **Lønnseksport**, velger du **Måned** — måneden før
   i dag er valgt fra start — og trykker **Eksporter til lønn**
   ([brukerveiledningen](/nb/user/staff/#eksporter-en-måned-til-lønn)). Siden ber om
   `GET /api/v1/staff/payroll/export?month=2026-09&format=csv`; kalt direkte kan `format`
   utelates — `csv` er det eneste formatet som finnes.
2. Lagre nedlastingen, `staff-payroll-2026-09.csv`, og importer den i lønnssystemet.

Hva filen inneholder: én rad per arbeidsforhold som var i måneden — startet senest den
siste dagen og ikke avsluttet før den første — også slettede personers, med vilkårene
som gjaldt den siste dagen i måneden (eller sluttdagen, for et arbeidsforhold som
sluttet i løpet av måneden). Hver rad har ansatt- og arbeidsforholdsnummeret, navnet,
virksomhetens organisasjonsnummer, startdatoen, yrkeskoden, timene i full stilling,
stillingsprosenten og når den sist ble endret, arbeidstidsordningen og
ansettelsesformen som Skatteetatens a-meldingskoder, avlønningstypen, når lønnen sist
ble endret, avdelingens kode, kostnadsstedet (vilkårenes eget, ellers avdelingens), og
lønnsansienniteten og lønnstrinnet når de er satt. Sluttdatoen og sluttårsaken fylles
bare ut i måneden arbeidsforholdet slutter. Et arbeidsforhold uten vilkår den dagen
beholder raden sin med tomme vilkårskolonner. Formen er Utlegg-modulens lønnsfil —
semikolon, byte order mark og desimalkomma — så et norsk Excel åpner den som den er.
Hele kolonnelisten og kodene står i [referansen](/en/reference/staff/#the-payroll-export).
Kodene for de tre ansettelsesformene som kom i 2025 (utleid, og tilkallingsvikar) er
ikke verifisert mot Skatteetatens kodeliste: sjekk dem med lønnssystemet før du stoler
på dem.

Fødselsnummeret og kontonummeret er **ikke** med i filen: lønnssystemet har dem
allerede, så eksporten leser dem aldri.

Hver eksport registreres i installasjonens historikk med ditt navn, måneden, formatet,
antall rader og filens SHA-256-sjekksum, så filen lønnssystemet fikk kan kjennes igjen
senere — regn ut sjekksummen til en fil du har (`shasum -a 256 staff-payroll-2026-09.csv`)
og sammenlign. En måned som ikke er `YYYY-MM` avvises med `payroll_month_invalid`, et
annet format enn `csv` med `payroll_format_unsupported`; ingen av dem registreres.
Filen inneholder alles lønnsvilkår: oppbevar den der du oppbevarer lønnssystemets egne
opplysninger, og slett din kopi når lønnssystemet har den.

## Oppbevaring

Ansatte beholder en tidligere ansatts opplysninger bare så lenge loven tillater det, og
sletter resten selv: arbeideren `staff-retention` ser etter hver time og sletter det som
er blitt forfalt. Hva som forfaller, og hva hver sletting fjerner, står i
[referansen](/en/reference/staff/#retention); kort fortalt:

- **Personaladministrasjon** — kontaktopplysninger og adresse, fødselsnummeret og
  kontonummeret, feltene for omsorgsdager, HRs notater og notatene på vilkårene,
  pårørende, og dokumentene i kategorier av klasse D (sertifikater, korrespondanse,
  annet) — slettes **`retentionGraceDays`** (standard 90) dager etter at personens siste
  arbeidsforhold sluttet. Personen merkes da som slettet, og opplysningene blir
  skrivebeskyttet.
- **Helsedokumenter** — de i en begrenset kategori — slettes **`healthRetentionDays`**
  (standard 365) dager etter at det siste arbeidsforholdet sluttet.
- **Lønn** — et arbeidsforholds lønnsvilkår, ansiennitetsdato og lønnstrinn,
  arbeidsavtalene og attestene, og lønnen i historikken — slettes **fem år etter året
  arbeidsforholdet sluttet**, arbeidsforhold for arbeidsforhold: en person som har kommet
  tilbake, beholder lønnen i det nåværende arbeidsforholdet mens det gamles forsvinner.

**Hva en sletting lar stå**: navnene, fødselsdatoen, kjønnet, ansattnummeret,
arbeidsforholdene og vilkårene deres utenom lønn, rollene, koblingen til en bruker, og
historikkens hendelsestyper og datoer. Opplysningene svarer altså fortsatt på hvem som
jobbet her, når og i hvilken stilling; historikken viser en verdi som er slettet som
*endret*, og en hendelse **Slettet etter oppbevaringsreglene** sier hva som forsvant,
etter type og antall. En person uten arbeidsforhold, eller som fortsatt er ansatt,
slettes aldri.

### Sett tidene

1. Som en bruker med `staff:manage`, åpne **Ansatteinnstillinger**; kortet
   **Oppbevaring** viser tidene ([brukerveiledningen](/nb/user/staff/#oppbevaring)).
2. Endre **Frist (dager)** (`retentionGraceDays`, 0 til 3650) og **Helsedokumenter
   (dager)** (`healthRetentionDays`, 30 til 3650), og trykk **Lagre fristene**. Gjennom
   API-et er det `PUT /api/v1/staff/settings` med revisjonen du leste i `If-Match`; en
   forespørsel som utelater en av tidene, beholder den.
3. Kontroller: kortet viser de nye tidene. Neste kjøring bedømmer etter dem; en tid som
   gjøres kortere kan gjøre mange personer forfalt på én gang, og arbeideren tar femti
   per kjøring.

### Slå av arbeideren, eller endre hvor ofte den ser etter

Arbeideren kjører med mindre `STAFF_RETENTION_ENABLED=0`, hver `STAFF_WORKERS_POLL`
(standard `1h`, minst `1m`) — i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1` ([Installasjon](/nb/admin/installation/)). En sletting finner
ingenting forfalt andre gang, så å se etter hver time sletter hver dags forfalte personer
én gang. Flere replikaer bytter på gjennom en lås; én kjører slettingen om gangen.

Slått av slettes ingenting etter tidsplanen, og filene som venter på sletting, venter;
kjøringen for hånd nedenfor virker fortsatt.

### Kjør den for hånd

**Kjør sletting nå** på kortet **Oppbevaring** i **Ansatteinnstillinger**
(`staff:manage`; `POST /api/v1/staff/retention/run`) kjører arbeiderens sletting og en
runde i sletteloggen nå, med deg som forfatter av slettehendelsene, etter at du har
bekreftet. Kortet sier så hva som ble slettet per type, og hvor mange filer som ble
slettet fra objektlageret. Runden i sletteloggen stopper etter tretti sekunder, så et
tregt objektlager ikke holder igjen forespørselen; arbeideren sletter resten neste gang
den ser etter. Mens arbeideren kjører, sier kortet fra (`retention_busy`, 409); prøv
igjen om et minutt.

### Slettesperren

En person med [slettesperre](#sett-en-slettesperre-på-en-person) hoppes over av hver
sletting, av hver type, så lenge sperren står. Opphev sperren når saken er avsluttet, så
sletter neste kjøring det som har forfalt i mellomtiden.

### Sletteloggen

En sletting fjerner et dokuments oppføring med en gang og setter filen i kø for sletting
fra objektlageret; det gjør også en sletting for hånd, når lageret ikke bekrefter med en
gang. Arbeideren spør lageret på nytt hver gang den ser etter, venter lenger etter hver
avvisning (høyst en time), og gir aldri opp — en lovpålagt sletting er ikke et forsøk.

Kortet **Oppbevaring** i **Ansatteinnstillinger** viser sletteloggen — **Ventende
slettinger**, **Feilede slettinger**, **Eldste ventende siden** og **Sist slettet** —
slik `GET /api/v1/staff/settings` rapporterer den i blokken `retention`:
`pendingDeletions` (filer som venter), `failingDeletions` (filer lageret har avvist minst
én gang), `oldestPendingSince` og `lastErasedAt` (den siste slettingen). En
`failingDeletions` som blir stående over null, betyr at objektlageret avviser: sjekk at
lagring er konfigurert og kan nås ([Objektlagring](/nb/admin/object-storage/)), og
serverloggen, som navngir lagerets feil. Når lageret svarer, forsvinner filene i køen
neste gang arbeideren ser etter.

### Etter en sletting

En slettet person som kommer tilbake, [ansettes på nytt](#ansett-en-slettet-person-på-nytt)
på de samme opplysningene: det slettingen fjernet, blir stående tomt til du skriver det
inn igjen, og lønnen i det nye arbeidsforholdet røres ikke av slettingen av det gamle.

## Tersklene

Fire terskler gir arbeidsgiveren en plikt, og Ansatte følger med på hver av dem. Så lenge
en er nådd og ikke bekreftet, ser de med `staff:manage` et punkt for den i **Trenger
oppfølging** på dashbordet ([dashbordet](/nb/user/staff/#dashbordet)):

| Terskel | Nådd når | Hva arbeidsgiveren må gjøre |
| --- | --- | --- |
| `5` | 5 eller flere er ansatt i dag | Ha skriftlige varslingsrutiner (aml. § 2 A-6) og verneombud (§ 6-1). |
| `30` | 30 eller flere | Ha arbeidsmiljøutvalg, AMU (§ 7-1). |
| `50` | Flere enn 50 — 51 eller flere | Aktivitets- og redegjørelsesplikten, ARP (likestillings- og diskrimineringsloven § 26), som gjelder arbeidsgivere med flere enn femti ansatte. Med nøyaktig 50 kommer det ikke noe punkt ennå. |
| `otp` | Noen har 75 % eller mer, eller de med 20 % eller mer har til sammen to fulle stillinger — stillingsprosentene i dag, summert per person | Ha obligatorisk tjenestepensjon, OTP (OTP-loven § 2) — med mindre den eneste det gjelder er eieren, noe Ansatte ikke kan vite, siden den ikke registrerer noen eier. |

Når plikten er ivaretatt — eller ikke gjelder — bekrefter du terskelen. Den bekreftes
**én gang, for godt**: punktet kommer aldri tilbake, uansett hva antallet ansatte gjør
senere, og en terskel kan bekreftes før den er nådd.

1. Som en med `staff:manage`, åpne **Ansatteinnstillinger**, trykk **Bekreft** på
   terskelens rad under **Terskler**, og bekreft
   ([brukerveiledningen](/nb/user/staff/#terskler)). Gjennom API-et er det
   `POST /api/v1/staff/thresholds/{key}/acknowledge` med nøkkelen — `5`, `30`, `50` eller
   `otp`.
2. Kontroller: raden sier **Bekreftet** med datoen, slik `thresholdsAcknowledged` i
   `GET /api/v1/staff/settings` gjør; punktet er borte fra dashbordet.

Hver bekreftelse registreres i innstillingene med tidspunktet, og som en
`threshold_acknowledged`-hendelse for installasjonen som navngir hvem som bekreftet.
Å bekrefte en terskel på nytt endrer ingenting og registrerer ingenting. En annen nøkkel
enn de fire avvises med `threshold_unknown`.

## Hvem som avgjør fravær

En fraværssøknad — ferie, en permisjon, avspasering — avgjøres av personens leder i dag
(lederen arbeidsvilkårene nevner, ellers avdelingens, og den som har sluttet hoppes
over), av alle med `staff:approve` og av HR (`staff:manage`). Gi noen en rolle med
`staff:approve` for å la dem avgjøre alles fravær uten HRs øvrige rettigheter. Ingen
avgjør sin egen søknad: en leders går til lederens egen leder, og HRs til en annen med
`staff:approve` eller `staff:manage`. En leder avgjør først når personen deres er koblet
til en aktiv bruker ([Koble personer til brukerne sine](#koble-personer-til-brukerne-sine)).

**HR-permisjon avgjøres av `staff:personal-data`** (foreløpig, inntil eieren har
bestemt seg): en søknad om en HR-type — svangerskapspermisjon, en religiøs høytid, en
egen type merket som HR-type — går ikke til noen leder, ingen med `staff:approve` og ingen
HR uten `staff:personal-data`, og ingen av dem får e-post om den. Alle med
`staff:personal-data` unntatt personen selv avgjør den i godkjenningsinnboksen sin, og bare
HR med `staff:personal-data` endrer, kansellerer eller retter en slik rad. Gi den
rettigheten til den som skal avgjøre slik permisjon; finnes ingen andre med den, venter
søknaden. For alle andre vises permisjonen først når den er godkjent, som *permisjon*.

**Det ene unntaket, foreløpig** (inntil eieren har bestemt seg): den som har
`staff:approve` eller `staff:manage`, står øverst i strukturen — leder en avdeling uten
noen leder over seg — og ikke har noen leder med aktiv bruker, kan godkjenne sitt eget.
Hver slik godkjenning er et oppfølgingspunkt for `staff:manage` (`selfApproved`, med
personens navn) til fraværet er over, så noen ser det. For å unngå
det gir du personen en leder i arbeidsvilkårene, koblet til en aktiv bruker.

HR retter et godkjent eller registrert fravær — dagene, typen, prosenten eller
graderingen — fra personens fravær; rettingen lagres med hva det var før, og personen
får e-post. Et avgjort fravær som har begynt, kanselleres bare av HR.

## E-postene om fravær

Ansatte sender e-post om fravær bare til adresser den selv har — en persons
jobb-e-post, ellers den private — på personens språk:

- en **søknad** eller en **registrering** går til personens ledere som avgjør den
  (lederen i dag, koblet til en aktiv bruker);
- en **godkjenning**, et **avslag**, en leders **kansellering** og HRs **retting** går
  til personen.

De med `staff:approve` og `staff:manage` får **ikke** e-post — Ansatte kjenner ikke
adressene deres — og ser det som venter på dem i godkjenningsinnboksen i stedet.
E-posten nevner typen bare når den verken er sykdom eller en HR-type: en HR-type er
«permisjon», sykefravær og sykt barn «fravær»; den har aldri gradering, timer, en
merknad eller begrunnelsen for et avslag. Som e-posten om endret kontonummer nedenfor
trenger den `MAIL_DRIVER=smtp`; med en annen driver logger serveren
`staff: absence mail skipped: the mail driver is not smtp`, og en leder uten e-post
hoppes over med `staff: absence mail skipped: the recipient has no email`. En sending
som feiler, logges som en advarsel med personens id; fraværet blir stående.

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
- **Oppbevaringstider** (`retentionGraceDays`, 90, fra 0 til 3650, og
  `healthRetentionDays`, 365, fra 30 til 3650): hvor mange dager etter at personens siste
  arbeidsforhold sluttet, personaladministrasjonen og helsedokumentene slettes
  ([Oppbevaring](#oppbevaring)). Utelatt fra en erstatning beholder hver sin verdi.
- **Regler for egenmelding** (`selfCertPolicy`, `minimum`, med
  `selfCertDaysPerOccurrence`, `selfCertOccurrencesPerYear`, `selfCertDaysPerYear` og
  `selfCertAfterMonths`) og **automatisk overføring** (`autoCarryOver`, av, med den
  skrivebeskyttede `autoCarryOverSince`): [Reglene for egenmelding](#reglene-for-egenmelding).
  Utelatt fra en erstatning beholder hver sin verdi.
- **Oppbevaringsstatus** (`retention`, skrivebeskyttet): sletteloggens ventende og
  avviste filer, den eldste ventende, og den siste slettingen
  ([Sletteloggen](#sletteloggen)). Filer lageret har avvist minst én gang, og som ble
  lagt i køen for mer enn 24 timer siden, gir også et punkt på dashbordet for
  `staff:manage`.
- **Bekreftede terskler** (`thresholdsAcknowledged`, skrivebeskyttet): når hver terskel
  ble bekreftet ([Tersklene](#tersklene)).

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
- **`threshold_unknown`.** Nøkkelen er ingen av `5`, `30`, `50` og `otp`.
- **`document_count_exceeded`.** Personen har 200 dokumenter, det meste én person kan
  ha — talt uansett hva du får se, så det kan omfatte helsedokumenter du ikke ser. Fjern
  dokumenter som ikke lenger trengs.
- **`person_erased`.** Oppbevaringskjøringen har slettet personen, og opplysningene er
  skrivebeskyttet. Ansett dem på nytt med et nytt arbeidsforhold, eller la dem være.
- **`legal_hold`.** Personen du sletter, har en slettesperre. Opphev den først, hvis
  saken er avsluttet.
- **`reassessment_not_due`.** Personen er ikke slettet, er allerede revurdert, eller det
  siste arbeidsforholdet sluttet for mindre enn tolv måneder siden.
- **`import_not_semicolon`.** Filen er kommaseparert. Lagre den på nytt som CSV med
  semikolon — i et norsk Excel gjør *CSV UTF-8* det.
- **`import_too_large`** eller **`import_too_many_rows`.** Filen er over 5 MiB eller har
  flere rader enn `STAFF_IMPORT_MAX_ROWS`. Importer den i deler.
- **`import_sealed_columns`.** Filen har en kolonne for fødselsnummer eller kontonummer,
  og du har ikke `staff:personal-data`. Fjern kolonnene, eller be om rettigheten.
- **En ukjent kolonne.** Importen navngir den: en feilstavet overskrift avvises i stedet
  for å ignoreres. Rett den til et av navnene i malen.
- **`export_too_many`.** Registeret har flere personer enn `STAFF_IMPORT_MAX_ROWS`. Hev
  det, innenfor grensene, og start på nytt.
- **`retention_busy`.** Oppbevaringsarbeideren kjører, på denne serveren eller en annen.
  Vent et minutt og kjør den igjen.
- **`failingDeletions` over null.** Objektlageret nekter å slette filene i køen. Rett opp
  lagringen; arbeideren prøver igjen uten ende.
- **En oppbevaringstid avvist.** `retentionGraceDays` er 0 til 3650 dager,
  `healthRetentionDays` 30 til 3650.
- **Ingen blir slettet.** Arbeideren er slått av (`STAFF_RETENTION_ENABLED=0`), personen
  har slettesperre, har fortsatt et arbeidsforhold som ikke har sluttet, eller har ingen;
  eller tiden har ikke gått ennå.
