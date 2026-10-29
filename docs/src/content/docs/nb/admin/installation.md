---
title: Installasjon
description: Kjøre Vantigo med Docker Compose, fra første oppstart til produksjon og oppgradering.
sidebar:
  order: 10
sources:
  - deploy
  - Dockerfile
  - apps/server/cmd/vantigo
---

Vantigo leveres som ett container-image og kjører mot én PostgreSQL-database. Imaget
inneholder én Go-binær med React-grensesnittet innebygd; hvilke forretningsmoduler en
installasjon tilbyr velges med innstillingen `MODULES`, og identitet (kontoer,
innlogging, MFA, RBAC, OIDC og SCIM) er alltid en del av applikasjonen. Denne siden
tar deg fra en tom katalog til en kjørende installasjon, og videre gjennom det en
produksjonsutrulling trenger og hvordan du oppgraderer.

Den ferdige stacken ligger i
[deploy/compose](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose) i
repositoriet og kjører det ferdigbygde imaget fra
[GHCR](https://github.com/orgs/vantigo-io/packages).

## Det du trenger

- Docker med Compose-tillegget, eller en Compose-kompatibel kjøretid. Ikke noe annet:
  ingen verktøykjede og ingen byggeverktøy, siden imaget er ferdigbygd.
- En vert med en ledig port for applikasjonen, `8080` som standard.
- En `APP_SECRET` på minst 32 byte, som du genererer selv
  (`openssl rand -base64 32`). Applikasjonen genererer eller logger aldri en for deg.
- Et SMTP-relé før noen andre enn deg bruker installasjonen. Invitasjoner og
  passordgjenoppretting leveres som e-post, og stacken starter med plassholderverdier
  som ikke leverer noe.

## Hurtigstart

1. Last ned de tre filene stacken består av:

   ```bash
   mkdir vantigo && cd vantigo
   base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
   curl -fsSLO "$base/compose.yaml"
   curl -fsSLO "$base/.env.example"
   curl -fsSLO "$base/vantigo.env.example"
   ```

2. Opprett de to lokale konfigurasjonsfilene fra eksemplene:

   ```bash
   cp .env.example .env
   cp vantigo.env.example vantigo.env
   ```

3. Rediger dem. Applikasjonen kjører utenfor utvikling, og konfigurasjonen er
   fail-closed: den rapporterer alle manglende verdier samtidig og nekter å starte.
   Fordi jobben `vantigo-migrate` laster den samme konfigurasjonen, stopper en
   manglende verdi hele stacken og ikke bare API-et. Før første oppstart:

   - I `.env` setter du databasepassordene. `POSTGRES_PASSWORD` er eierrollen som
     kjører migreringene. `POSTGRES_APP_PASSWORD` og `VANTIGO_DB_PASSWORD` er
     kjøretidsrollen med minst mulig rettigheter som API-et kobler til som; begge
     har standardverdien `change-me-too` og må være like, fordi den første oppretter
     rollen og den andre autentiserer som den.
   - I `vantigo.env` setter du `APP_SECRET` til minst 32 byte generert med
     `openssl rand -base64 32`. Hver nøkkel prosessen bruker (CSRF-tokener,
     cookie-signering, kryptering av TOTP-hemmeligheter) utledes fra den.
   - `vantigo.env` leveres med `SMTP_HOST` og `SMTP_FROM` satt til plassholdere. La
     dem stå for å starte stacken — utenfor utvikling er standard e-postdriver
     `smtp`, som gjør begge obligatoriske for oppstart, ikke bare for sending — men
     bytt dem ut med et ekte relé før noen andre bruker installasjonen. Inntil du gjør
     det, kjører stacken normalt og leverer ingen e-post, så ingen bruker kan
     inviteres og ingen passord kan gjenopprettes.

4. Start stacken:

   ```bash
   docker compose up -d
   ```

Compose starter PostgreSQL, kjører applikasjonens databasemigreringer som en
engangsjobb, og starter deretter Vantigo:

- **Vantigo** — <http://localhost:8080>
- **API-kontrakten** — `GET http://localhost:8080/api/openapi.json` (krever en sesjon)

Modulene Kunder, Produkter, Energi, Kommunikasjon, Prosjekter, Tid, Utgifter,
Fakturaer, Ansatte, Bemanning, Kalender og Microsoft Entra ID er aktivert som standard
(`MODULES` i `vantigo.env`). De deler én PostgreSQL-database ved navn `vantigo`, med
uavhengige skjemaer `identity`, `customers`, `products`, `energy`, `communications`,
`projects`, `time`, `expenses`, `invoices`, `staff`, `planning`, `calendar` og `entra`, hver med sin egen
migreringshistorikk.

## Hva stacken inneholder

`compose.yaml` definerer tre tjenester:

| Tjeneste | Image | Hva den gjør |
| --- | --- | --- |
| `postgres` | `postgres:18` | Databasen. Ved første oppstart, når volumet `postgres-data` opprettes, oppretter et init-skript databasen `vantigo` og kjøretidsrollen med minst mulig rettigheter. |
| `vantigo-migrate` | `ghcr.io/vantigo-io/vantigo` | En engangsjobb som kjører kommandoen `migrate` som eierrollen og avslutter. Den venter på at `postgres` er frisk og starter aldri på nytt. |
| `vantigo` | `ghcr.io/vantigo-io/vantigo` | Applikasjonen: kommandoen `api`, publisert på `VANTIGO_PORT` (8080). Den starter først når `vantigo-migrate` har fullført vellykket. |

Imaget er én binær med en kommandotabell; kommandoen er det første argumentet:

| Kommando | Hva den gjør | Når du bruker den |
| --- | --- | --- |
| `api` | Kjører migreringer under en advisory lock, og serverer deretter SPA-en, API-et og helse — pluss bakgrunnsjobbene til hver aktivert modul når `WORKERS_IN_PROCESS=1` (standard). | Utrullingen med én container som hurtigstart-stacken kjører. |
| `server` | Serverer bare: migrerer aldri og kjører aldri bakgrunnsjobber, uansett `WORKERS_IN_PROCESS`. | En flåte av tilstandsløse replikaer bak en lastbalanserer, der migreringer og bakgrunnsjobber kjøres et annet sted. |
| `worker` | Bakgrunnsjobbene til hver aktivert modul, pluss `/health/live` og `/health/ready` for prober. | Én dedikert worker-container når API-et skaleres horisontalt. |
| `migrate` | Kjører migreringer og avslutter med 0 eller 1. | Engangsjobben før applikasjonen starter, ved første oppstart og ved hver oppgradering. |
| `healthcheck` | Prober denne containerens eget `/health/ready` på `127.0.0.1:$PORT` og avslutter med 0 eller 1. Den konstruerer ingenting, så en probe feiler aldri på en feil `DATABASE_URL` — å rapportere det er `/health/ready` sin jobb. | Containerens `HEALTHCHECK` og enhver liveness- eller readiness-probe. |

`seed` finnes også for lokal utvikling og demomiljøer (`APP_ENV=development`; den
avslutter med 2 utenfor det): den kjører migreringer og fyller inn et idempotent norsk
demodatasett på tvers av alle moduler (20 brukere fordelt på 7 tilgangsroller, 200
bedrifter med 400 kontakter, 50 produkter, 75 prosjekter, timeregistreringer, utlegg,
fakturaer, målepunkter og kundesamtaler). Ingen kommando, eller en ukjent en, skriver
bruksanvisning til stderr og avslutter med 2, så en skrivefeil i en jobbdefinisjon
feiler høylytt i stedet for stille å bli en webserver.

## Første innlogging

Gå til <http://localhost:8080/setup> for å opprette den første eierkontoen (Owner).
Den er installasjonens fulle administrator — Owner og SystemAdmin — og ingen
hemmelighet beskytter siden: den som fullfører oppsettet først, eier installasjonen,
så gjør det før adressen kan nås av noen andre. Oppsettet lukkes når eieren finnes, og
API-et logger en advarsel ved hver oppstart inntil da. Eiere kan invitere flere brukere
fra `/settings`.

Hvis stacken vil være tilgjengelig før en operatør kan fullføre `/setup`, setter du
`BOOTSTRAP_OWNER_EMAIL` i `vantigo.env` i stedet. `/setup` er da lukket fra starten
av, og den adressen får en eierinvitasjon på e-post én gang; utløper den ubrukt,
utsteder neste oppstart en ny. Kontoen invitasjonen oppretter er Owner og
SystemAdmin, akkurat som `/setup` sin. Denne veien trenger et fungerende SMTP-relé,
siden invitasjonen leveres som e-post. Se
[management-lytteren](/nb/admin/management-listener/) for bootstrap-tilstanden et
kontrollplan kan spørre etter, og invitasjonens fulle oppførsel.

## Konfigurasjon

Innstillingene ligger i to filer ved siden av `compose.yaml`:

| Fil | Formål |
| --- | --- |
| `.env` | Image-tag, PostgreSQL-legitimasjon og vertsport |
| `vantigo.env` | Innstillinger for applikasjon, identitet, e-post, moduler og proxy |

Hver nøkkel `vantigo.env` godtar er dokumentert i feltkommentarene i
[apps/server/internal/config/config.go](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
den autoritative referansen, og oppsummert i
[Identitet, autentisering og utrulling](/nb/admin/authentication/). OIDC for
arbeidsstyrken og SCIM-provisjonering er ikke blant dem: en Owner setter begge på
kortet **Single sign-on**, og [SSO- og SCIM-drift](/nb/admin/sso-scim/) har
prosedyrene og gjenopprettingen.

### Innstillingene du må ta stilling til

- **`APP_SECRET`** (`vantigo.env`) — påkrevd, minst 32 byte. Hver krypteringsnøkkel
  prosessen bruker (CSRF-tokener, cookie-signering, AES-256-GCM-nøkkelen for
  TOTP-hemmeligheter) utledes fra den via HKDF-SHA256, én utledet nøkkel per formål.
  Generer den én gang med `openssl rand -base64 32`, lagre den i en
  hemmelighetsforvalter, og roter den aldri uten en migreringsplan: en rotasjon
  ugyldiggjør hver åpen sesjon og hver TOTP-hemmelighet kryptert under den gamle
  verdien. Med Ansatte gjør en rotasjon også hvert forseglede fødselsnummer og
  kontonummer umulig å åpne, og etterlater indeksen over fødselsnumrene ute av stand
  til å finne et nummer eller avvise et duplikat av et som ble skrevet før den — se
  [Rotating APP_SECRET](/en/contributing/platform-packages/#rotating-app_secret).
- **`APP_URL`** (`vantigo.env`) — bare det offentlige opphavet: skjema og vert, med
  port om nødvendig, ingen sti og ingen avsluttende skråstrek. E-postlenker, de
  godtatte `Host`-verdiene, cookie-attributtet `Secure`, den statiske
  OIDC-tilbakekallingen og omdirigerings-URI-en til Microsoft Entra ID-modulens
  Outlook-tilkobling utledes alle fra den. Forespørsler med en annen `Host` enn
  denne eller loopback (`localhost`, `127.0.0.1`, `[::1]`) avvises med 400. Porten må
  stemme med `VANTIGO_PORT` i `.env`; ingenting avstemmer de to, og et avvik gir ingen
  feil — stacken serverer, men hver invitasjons- og passordtilbakestillingslenke den
  sender peker på en port ingen lytter på.
- **`DATABASE_URL`** — bygges av `compose.yaml` fra verdiene i `.env`, med ulik rolle
  per tjeneste, så ikke sett den i `vantigo.env` i tillegg. Se
  [Databaseroller](#databaseroller).
- **`SMTP_HOST`, `SMTP_FROM`** (`vantigo.env`) — påkrevd for at serveren skal starte
  i det hele tatt. `SMTP_FROM` tolkes som en ren adresse: `no-reply@example.com`
  godtas, `Vantigo <no-reply@example.com>` avvises ved oppstart. `SMTP_PORT` har
  standardverdi 587, `SMTP_USERNAME` og `SMTP_PASSWORD` er valgfrie, og `SMTP_TLS` er
  `starttls` som standard (`implicit` forhandler også TLS; `none` er klartekst). La
  `MAIL_DRIVER` stå usatt: den har allerede standardverdien `smtp` her, og `log`
  avvises utenfor utvikling fordi de e-postene bærer bearer-lenker.
- **`MODULES`** (`vantigo.env`) — den kommaseparerte listen over forretningsmoduler
  denne installasjonen aktiverer. `identity` er alltid montert og listes aldri. Usatt
  aktiverer hver modul binæren kan montere: `customers`, `products`, `energy`,
  `communications`, `projects`, `time`, `expenses`, `invoices`, `staff`, `planning`,
  `calendar` og `entra`. `energy`, `communications`, `projects` og `invoices` krever hver
  `customers`, `time` krever `projects`, `planning` krever både `projects` og `staff`,
  og `entra` krever `staff`; `projects` bruker
  også `products` når den er aktivert og svarer 409 på endepunktene for
  faktureringslinjer når den ikke er det; `expenses` krever ingenting og leser
  `projects` bare når den tilfeldigvis er aktivert; `staff` og `calendar` krever
  ingenting.
  For en mindre installasjon lister du bare det du vil ha, for eksempel
  `MODULES=customers,products`.
- **`OWNERS_REQUIRE_MFA=0` og `OWNERS_ALLOW_INSECURE_NO_MFA=1`** (`vantigo.env`) —
  leveres i eksempelet fordi en fersk installasjon ikke har noen til å registrere en
  autentikator rett etter `/setup`. Fjern dem før produksjon; se
  [Produksjonsmerknader](#produksjonsmerknader).

### Låse en utgivelse

Lås en bestemt utgivelse med `VANTIGO_TAG=0.16.1` i `.env`. Publiserte image-tagger
dropper `v`-en som git-utgivelsestagger beholder (`v0.16.1` er git-taggen, `0.16.1`
image-taggen; `latest` påvirkes ikke). For reproduserbare produksjonsutrullinger låser
du til digest i stedet for tag — tagger kan endres, digester kan ikke:

```dotenv
VANTIGO_TAG=0.16.1@sha256:<digest fra utgivelsen>
```

Utgivelses-imagene er signert med cosign; verifiser en digest før du ruller den ut:

```bash
cosign verify ghcr.io/vantigo-io/vantigo@sha256:<digest> \
    --certificate-identity-regexp 'https://github.com/vantigo-io/vantigo/.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Databaseroller

Stacken oppretter to PostgreSQL-roller ved første initialisering:

| Variabel | Standard | Rolle |
| --- | --- | --- |
| `POSTGRES_USER` | `vantigo` | Superbruker. Eier hvert skjema og hver tabell og kjører jobben `vantigo-migrate`. |
| `POSTGRES_APP_USER` | `vantigo_app` | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`, bare tabell-DML, eier ingenting. |

`VANTIGO_DB_USER` og `VANTIGO_DB_PASSWORD` velger rollen API-et kobler til som
(`compose.yaml` bygger `DATABASE_URL` fra dem). De har kjøretidsrollen som standard —
vanlig forsvar i dybden, slik at en kompromittert API-prosess ikke kan endre skjemaet,
opprette objekter eller på annen måte opptre som databaseeieren. Dette er en
applikasjon for én leietaker: det finnes ingen `tenant_id`-kolonne og ingen
radnivå-sikkerhetspolicy noe sted i skjemaet, så dette er ikke en grense for
leietakerisolasjon, bare en for rettighetsseparasjon. Pek `VANTIGO_DB_USER` mot
eierrollen bare for feilsøking.

`api`-modus sjekker også migreringene selv før den begynner å servere, men den sjekken
kjører som den samme `DATABASE_URL` med minst mulig rettigheter — tjenesten `vantigo`
får aldri eierens legitimasjon. Den trenger bare lesetilgang for å konkludere med at
det ikke er noe å gjøre: migreringsbiblioteket sjekker om versjonstabellen finnes med
en ren `SELECT`, returnerer umiddelbart når den allerede finnes, og utsteder aldri
`CREATE TABLE`. Det ene tilfellet som ville trengt DDL — å starte med migreringer som
faktisk venter — når aldri `api` i det hele tatt: `vantigo-migrate` kjører først, under
eierrollen, og `vantigo` sin `depends_on: condition: service_completed_successfully`
hindrer API-containeren i å starte før den lykkes, oppgraderinger inkludert.

Rollesetningene kjøres fra PostgreSQL sitt init-skript, som bare kjører når volumet
`postgres-data` opprettes. En eksisterende installasjon kan legge til rollen for hånd
(juster skjemalistene så de stemmer med `MODULES` som er aktivert i din installasjon):

```sql
\connect vantigo

CREATE ROLE "vantigo_app"
    LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT
    PASSWORD '<passord>';

GRANT CONNECT ON DATABASE vantigo TO "vantigo_app";

ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT USAGE ON SCHEMAS TO "vantigo_app";
ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "vantigo_app";
ALTER DEFAULT PRIVILEGES FOR ROLE "vantigo" GRANT USAGE, SELECT ON SEQUENCES TO "vantigo_app";

-- Standardrettigheter dekker bare objekter som opprettes etterpå; gi rettigheter
-- på skjemaene de aktiverte modulene allerede har opprettet (legg til/fjern
-- skjemaer så de stemmer med MODULES aktivert i din installasjon).
GRANT USAGE ON SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, calendar, entra TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, calendar, entra TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, calendar, entra TO "vantigo_app";
```

## Bakgrunnsjobber og skalering

Standardoppsettet med én container kjører bakgrunnsjobbene til hver aktivert modul
(utboks-levering, oppbevaring, opprydding av vedlegg, kundemodulens registerjobber,
kundemodulens importjobb `customers-import`, energimodulens importjobb `energy-import`, energimodulens spotprisjobb
`energy-spot-prices` når `ENERGY_SPOT_PRICE_SERVICE_URL` er satt eller `ENERGY_SPOT_PRICE_FEED_ENABLED=1`, fakturamodulens EHF-jobber `invoices-ehf` og `invoices-ehf-events` når
`INVOICES_EHF_ENABLED` er på, fakturamodulens purrejobb `invoices-reminders`, alltid,
fakturamodulens faktureringsjobb `invoices-billing`, alltid,
fakturamodulens betalingsjobb `invoices-payments` når `INVOICES_VIPPS_ENABLED` er på, og
kalendermodulens avlysningsjobb `calendar-cancellations` med mindre
`CALENDAR_CANCELLATIONS_ENABLED=0`, kalendermodulens abonnementsjobb
`calendar-subscriptions` med mindre `CALENDAR_SUBSCRIPTIONS_ENABLED=0`, og kalendermodulens
postbokssynkronisering `calendar-mailbox-sync` med mindre `CALENDAR_MAILBOX_SYNC_ENABLED=0`,
og Microsoft Entra ID-modulens `entra-outlook-sweep`, alltid, som rydder i folks
Outlook-tilkoblinger hver time) inne i API-prosessen: `WORKERS_IN_PROCESS=1`, standardverdien. Utrullinger som
skalerer API-et horisontalt bør flytte dem til én dedikert worker-container, slik at
hver ekstra HTTP-replika ikke mangedobler pollerne:

1. Sett `WORKERS_IN_PROCESS=0` i `vantigo.env`. Det gjelder API-replikaene.
2. Kjør én ekstra container fra det samme imaget med kommandoen `worker`. Den kjører
   alltid bakgrunnsjobbene uansett `WORKERS_IN_PROCESS`, og serverer bare
   `/health/live` og `/health/ready` for prober.
3. Workeren trenger ingen egne OIDC- eller SCIM-innstillinger. Hvem som har en
   tillatelse (en deaktivering i katalogen teller bare så lenge SCIM er på) og
   kontomodusen (lokal eller føderert) følger begge innstillingene for single sign-on i
   databasen, som workeren leser slik API-et gjør, innen 20 sekunder etter en Owners
   endring (se [autoritetsmodusen](/nb/admin/authentication/#autoritetsmodusen)).
   Den trenger heller ingenting for Microsoft Entra ID: med modulen på når
   kontojobben i Ansatte leietakeren gjennom den, med tilkoblingen, hemmelighetene og
   kontoreglene den leser fra databasen — så worker-containeren trenger, som API-et,
   utgående https til `login.microsoftonline.com` og `graph.microsoft.com`, uten proxy
   ([Microsoft Entra ID](/nb/admin/entra/#før-du-begynner)).
4. Hold klokkene på API- og worker-vertene synkronisert (NTP). Kontojobben i Ansatte
   tar koblinger med leieavtaler som hver prosess sammenligner med sin egen klokke, så
   en klokke som går for fort kan ta over en kobling en annen prosess fortsatt arbeider
   med.

Replikaer som verken skal migrere eller kjøre bakgrunnsjobber kan kjøre kommandoen
`server` i stedet for `api`; den serverer bare, uansett hva `WORKERS_IN_PROCESS` sier.
Uansett topologi tar oppbevaringsoppryddingen, registerjobbene, EHF-hendelsesjobben,
kalenderens avlysningsjobb og `entra-outlook-sweep` en rådgivende lås (advisory lock) i PostgreSQL, så hver av dem kjører på nøyaktig én
instans per syklus; utboksen, `invoices-ehf`-jobben, `invoices-reminders`-jobben,
`invoices-billing`-jobben, `invoices-payments`-jobben og kalenderens abonnements- og
postboksjobb tar i stedet én rad om gangen under en tidsbegrenset lås, så
et hvilket som helst antall instanser deler arbeidet uten å sende noe to ganger. Purrejobben sender ett brev om
gangen, høyst ett i sekundet per instans, og hver sending må bli ferdig godt innenfor
låsen på 60 sekunder
([Purrejobben og e-post](/nb/admin/payments/#purrejobben-og-e-post)).
Betalingsjobben ser etter arbeid hvert sekund og behandler alle Vipps-betalinger som er
klare, i hver syklus
([Betalingsjobben](/nb/admin/payments/#betalingsjobben)).
Faktureringsjobben har ingen innstillinger: den utfører fakturakjøringene folk lager
under **Fakturaer → Fakturakjøringer**, ser etter arbeid hvert 5. sekund og lager — og,
når kjøringen ber om det, utsteder — én kundes fakturaer om gangen, høyst fem kunder
per syklus, hver under en lås på 10 minutter; den sender ingenting
([Billing runs](/en/reference/invoices/#billing-runs)).

Kundemodulens importjobb kontrollerer og kjører kundefilene folk laster opp. Den ser
etter arbeid hvert `CUSTOMERS_IMPORT_POLL` (5 sekunder som standard), og i
standardoppsettet med én container vekker API-et den med en gang en kontroll eller en
import blir bedt om. Den tar en rådgivende lås, så én instans arbeider med én import
om gangen. `CUSTOMERS_IMPORT_ENABLED=0` slår den av; importer venter da, og
importsiden sier at den venter på bakgrunnsjobben. Ferdige og forlatte importer, med
radene de lastet opp, fjernes etter `CUSTOMERS_IMPORT_RETENTION` (`720h`, 30 dager);
kundene de opprettet, blir stående. Se
[Export, and import from a file](/en/reference/customers/#export-and-import-from-a-file).

Energimodulens importjobb `energy-import` kontrollerer, kjører og knytter til kunder
målepunktfilene folk laster opp, på samme måte: den ser etter arbeid hvert
`ENERGY_IMPORT_POLL` (5 sekunder som standard), API-et vekker den med en gang en
kontroll, en import eller en tilknytning blir bedt om, og den tar en egen rådgivende
lås. `ENERGY_IMPORT_ENABLED=0` slår den av; importer venter da. Ferdige og forlatte
importer, med radene sine, fjernes etter `ENERGY_IMPORT_RETENTION` (`720h`, 30 dager);
målepunktene og leveranseperiodene de skrev, blir stående. I worker-modus leser den
kunder gjennom kundemodulen, så kjør den med `customers` blant `MODULES` når importene
navngir kunder. Se
[Importing metering points](/en/reference/energy/#importing-metering-points).

Energimodulens spotprisjobb `energy-spot-prices` fyller døgnmarkedets priser for hvert
prisområde fra **én** kilde: den delte spotpristjenesten, eller en offentlig priskilde.
Det er et utgående kall installasjonen selv velger, så den er **av** med mindre én av
dem er satt; uten noen av dem importeres prisene fra en fil på Priser-siden. Å sette
både `ENERGY_SPOT_PRICE_SERVICE_URL` og `ENERGY_SPOT_PRICE_FEED_ENABLED=1` stopper
oppstarten («set one spot price source»). `ENERGY_SPOT_PRICE_FEED_POLL` (15 minutter
som standard, minst `1m`) styrer takten uansett kilde, og begge kjører under den samme
rådgivende låsen, så én instans henter, med høyst tjue forespørsler per runde.

**Med spotpristjenesten** (`ENERGY_SPOT_PRICE_SERVICE_URL` og
`ENERGY_SPOT_PRICE_SERVICE_KEY`, installasjonens bærernøkkel — på våre verter
`http://vantigo-spotprices:8080` og nøkkelen vertens verktøy skriver) kopierer jobben
prisene for hvert område tjenesten følger (NO1–NO5 på våre verter) inn i
installasjonens egen database, så Priser-siden fylles før det første målepunktet
finnes; et område i bruk som tjenesten ikke følger, spørres det ikke etter (se
nedenfor). Hver runde spør først tjenesten hvilke områder den følger, leser deretter
hvert områdes løpende vindu — fjorten dager tilbake til i morgen — med én
forespørsel, når en av dagene er forfalt eller vinduet sist ble lest for mer enn et
døgn siden (en ny lesing uten forfalte dager som feiler, registrerer ingenting og
prøves igjen neste runde). Har jobben vært borte i mer enn to uker, leser den først
dagene den gikk glipp av mellom det forrige løpende vinduet og dagens, eldste først,
som historikk, så et avbrudd ikke etterlater hull. Deretter går den bakover gjennom
hvert områdes historikk, 35 dager per
forespørsel (34 over høstens klokkeskifte), ned til den eldste dagen tjenesten har,
eller til `ENERGY_SPOT_PRICE_HISTORY_FROM` når den er senere. De tjue forespørslene
omfatter den om områdene. **Mens historikken kopieres, kjører jobben en runde hvert
minutt** i stedet for ved hvert poll; fem områder fra 2015 tar omtrent en halvtime, og
deretter styrer pollen takten igjen. Priser tjenesten merker som foreløpige
(morgendagens NOK-pris til valutakursen er endelig) lagres som foreløpige, spørres
etter igjen hver time og erstattes når de blir endelige. Tjenesten overskriver aldri
en pris importert fra en fil. Tjenesten nås på en privat adresse gjennom det ene
unntaket i vakten for utgående adresser
([Transportsikkerhet](/nb/admin/transport-security/#unntaket-for-private-nettverk)):
en `http`-URL bare til en privat adresse, `https` til enhver rutbar adresse, og aldri en
videresending, en proxy eller en annen vert.

For å kontrollere, åpne Priser-siden: tittelen på **Dekning**-kortet sier
**Spotpristjenesten**, hvert område viser oppløsningen sin, og **Historikk**-kolonnen
sier hvor langt tilbake den har kopiert («Kopierer… tilbake til …», deretter «Komplett
fra …»). Eller spør `GET /api/v1/energy/spot-prices/coverage` med en økt som har
`energy:prices-view`: den svarer `"source": "service"`, og per område
`followedByService` og `history` når den første runden har kjørt.

Hva som kan gå galt — feilen i det løpende vinduet vises i **Dekning**-kortets kolonne
**Siste henting**, historikkens under **Historikk**, og begge logges som advarsel:

- **«the spot-price service refused the key»** — en `401`: nøkkelen er feil, trukket
  tilbake eller rotert uten at Vantigo er startet på nytt. Sett den gjeldende nøkkelen
  og start på nytt.
- **«the spot-price service's address is public; use https»** — URL-en er `http`, og
  verten slås opp til en offentlig adresse. Bruk den interne adressen, eller `https`.
- **«the spot-price service is rate limiting this installation»** — en `429`: runden
  gjør ingen flere forespørsler, og neste forsøk venter den vanlige ventetiden etter en
  feil eller til tjenestens `Retry-After`, det som er senest, men aldri mer enn et
  døgn.
- **«the spot-price service answered 400 Bad Request»**, **«… did not answer a list of
  prices»**, **«… larger than 4 MiB»** eller et intervalltidspunkt den ikke kan lese —
  tjenesten avviste et historikkvindu eller svarte noe Vantigo ikke kan lese. Et slikt
  vindu spørres tre ganger, med ventetid mellom; etter den tredje **hoppes det over**,
  dagene telles som hoppet over, og gjennomgangen fortsetter, mens setningen blir stående
  under **Historikk** til neste vindu er kopiert. Fyll de dagene fra en fil. Et
  tidsavbrudd, en `5xx`, en `401` eller en `429` hopper aldri over et vindu: jobben venter
  og spør igjen.
- **Tjenestens områder kan ikke leses** — logges som advarsel med tomt område;
  **Historikk**-kolonnen blir stående som den var, ingenting av historikken kopieres den
  runden, og det løpende vinduet kjører fortsatt for områdene i bruk.
- **Et område i bruk som tjenesten ikke følger** — et målepunkt i for eksempel `SE3` på
  en vert der tjenesten bare følger NO1–NO5. **Historikk**-kolonnen sier «Følges ikke av
  spotpristjenesten», og jobben spør ikke etter noe for det: be tjenestens operatør
  følge området, eller importer områdets priser fra en fil. Et område tjenesten
  **slutter** å følge, sier det samme fra neste runde og beholder det som er kopiert;
  når det følges igjen, leses dagene det gikk glipp av i mellomtiden som over.
- **Dager hoppet over** — en dag tjenesten har ufullstendig eller uten NOK-pris, eller
  en som ligger over en dag importert fra en fil med en annen oppløsning, telles i
  **Historikk**-kolonnen og overlates til en fil; jobben overskriver aldri en importert
  pris.

**Med den offentlige priskilden** (`ENERGY_SPOT_PRICE_FEED_ENABLED=1`) henter jobben
prisene for hvert prisområde i bruk — det til et målepunkt med en ikke-kansellert
leveranseperiode, eller et som allerede har priser — fra sju dager tilbake til i
morgen, fra `ENERGY_SPOT_PRICE_FEED_URL` — som standard den offentlige priskilden på
`www.hvakosterstrommen.no`. En dag eldre enn en uke fylles inn fra en fil på
Priser-siden.
Standardkilden har bare de norske områdene NO1–NO5: et målepunkt i et annet område (et
svensk `SE3`, for eksempel) får jobben til å spørre etter dager kilden aldri har, så gi
den en kilde som har dem, eller importer områdets priser fra en fil. Priskilden hentes gjennom vakten for utgående adresser, slik
kalenderabonnementene gjør: bare `https`, ingen proxy, ingen videresending, høyst
1 MiB, og aldri en vert som slås opp til en privat adresse, loopback, link-local,
carrier-grade NAT, en skyplattforms metadata-adresse eller en annen ikke-rutbar
adresse — så API- eller worker-containeren trenger utgående https til priskildens vert,
uten proxy. En ugyldig `ENERGY_SPOT_PRICE_FEED_URL` eller en `ENERGY_SPOT_PRICE_FEED_POLL` under `1m` stopper
oppstarten selv når jobben er slått av. En mislykket henting logges som advarsel med
området, dagen og priskildens vert, aldri adressens sti eller spørring. Se
[Spot prices](/en/reference/energy/#spot-prices).

Ved avslutning tømmer prosessen pågående arbeid i opptil `SHUTDOWN_TIMEOUT` (30
sekunder som standard), nok til at den lengste enkeltoperasjonen i en bakgrunnsjobb
rekker å fullføre og committe. `compose.yaml` setter `stop_grace_period: 35s` på
tjenesten `vantigo` slik at Compose sin SIGKILL aldri kommer før tømmingen kan
fullføre. Uansett hva du setter `SHUTDOWN_TIMEOUT` til, hold `stop_grace_period`
(Compose) eller `terminationGracePeriodSeconds` (Kubernetes) med god margin over den,
ellers kan en pågående operasjon bli drept midt i og kjørt på nytt etter omstart. En
SIGTERM som kommer under en migrering venter til den er ferdig; et andre signal
avslutter umiddelbart.

## Helseprober

Imaget har ingen shell, så containerens `HEALTHCHECK` kjører binæren selv
(`/app/vantigo healthcheck`) i stedet for en `CMD-SHELL`-enlinjer med curl eller wget.
Den prober `/health/ready`: oppstart fullført, PostgreSQL tilgjengelig, og
objektlagring tilgjengelig hvis konfigurert. Hold enhver probe du konfigurerer —
Compose sin `healthcheck.test`, en liveness- eller readiness-probe i Kubernetes, hva
som helst annet — i den samme exec-formen. En HTTP-probe som kobler til direkte, i
stedet for å kjøre inne i containeren, sender sin egen adresse som `Host`-header, og
vertsfilteret godtar bare verten i `APP_URL` pluss loopback (`localhost`, `127.0.0.1`,
`::1`); enhver annen `Host` avvises med 400, så en slik probe feiler alltid.

Stackens probe kjører hvert 10. sekund med 5 sekunders tidsavbrudd, 6 forsøk og en
startperiode på 30 sekunder.

## Databasens tilkoblingsbudsjett

PostgreSQL-poolen til hver API-replika har som standard `max(4, NumCPU)` tilkoblinger,
som knytter poolstørrelsen til verten replikaen tilfeldigvis havner på. Lås den
eksplisitt med `pool_max_conns` på `DATABASE_URL` i `compose.yaml` i stedet for å
stole på den standardverdien, særlig når du kjører mer enn én replika. Dimensjoner
budsjettet slik at

```
replikaer × poolstørrelse  ≤  max_connections − margin (reserver ~10 til
                               migreringer, overvåking og manuelle sesjoner)
```

Migreringer kjører uten pool-taket, siden skjemaendringer legitimt kan ta lengre tid.

## Basesti og omvendt proxy

Hele applikasjonen kan serveres under ett konfigurerbart stiprefiks. Sett
`APP_BASE_PATH` i `vantigo.env` og sett `APP_URL` til det offentlige skjemaet og
verten — ingen sti, siden stiprefikset er `APP_BASE_PATH`, ikke en del av `APP_URL`.
Omdirigerings-URI-er registrert andre steder følger begge: Microsoft Entra ID-modulens
Outlook-tilkobling svarer Microsoft på `APP_URL` + `APP_BASE_PATH` +
`/api/v1/entra/outlook/callback` — aldri på `PUBLIC_BASE_URL` — så endrer du en av dem,
må den nye URI-en legges til appregistreringen
([Microsoft Entra ID](/nb/admin/entra/#outlook-tilkobling)).
API-prefiksene forblir `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`,
`/api/v1/energy`, `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`,
`/api/v1/expenses`, `/api/v1/invoices`, `/api/v1/staff`, `/api/v1/planning`,
`/api/v1/calendar` og `/api/v1/entra`.

For eksempel, med nginx:

```nginx
server {
    listen 443 ssl;
    server_name vantigo.example.com;

    location /vantigo/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Med `APP_BASE_PATH=/vantigo` inneholder genererte invitasjons-,
passordtilbakestillings- og OIDC-tilbakekallings-URL-er det prefikset. Basestien er
en kjøretidsinnstilling; det ene imaget serverer den tilhørende SPA-en uten ny bygging.
Stol på nøyaktig proxyen foran deg med `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS`,
ellers ignoreres de videresendte headerne for skjema, vert og klientadresse.

## Den innebygde dokumentasjonen

Hvert image har med seg denne dokumentasjonen, bygget for nøyaktig den versjonen det
leveres med, og serverer den på `/docs/` ved siden av applikasjonen (`/docs/en/` og
`/docs/nb/`, under basestien når en er satt). Den trenger verken nettverk eller
innlogging, og **Hjelp** i avatar-menyen åpner den på leserens språk. Den offentlige
siden på <https://docs.vantigo.io> beskriver i stedet den nyeste utviklingsversjonen;
en linje under toppfeltet på hver side sier hvilken Vantigo den beskriver.

Sidene er statiske filer inne i binæren: ingen innstilling slår dem av eller på, og de
legger noen få megabyte til imaget. Den genererte API-referansen er utelatt fra den
innebygde kopien og finnes bare på den offentlige siden. Fordi sidens egne skript
kjører inline og søket kjører WebAssembly, bærer svar under `/docs/` sidens egen
innholdssikkerhetspolicy i stedet for applikasjonens; se
[Transportsikkerhet og herding av nettleseren](/nb/admin/transport-security/#sikkerhetshoder-for-nettleseren).

## E-post og observerbarhet

Identitetsinvitasjoner og passordgjenoppretting bruker innstillingene `MAIL_DRIVER` og
`SMTP_*`. Leveringslegitimasjon per postkasse for Kommunikasjon-modulen konfigureres
gjennom Kommunikasjon-API-et, der vert, port og et beskyttet passord lagres som
postkasselegitimasjon — det finnes ingen miljøvariabel for dem. De kanalene er bare
SMTP: API-et nekter å opprette eller oppdatere en kanal som navngir en annen
leverandør, og avviser en Mailgun-legitimasjon blankt. Se
[Kommunikasjon](/en/reference/communications/).

Modulene Prosjekter og Tid trenger ingen egen miljøvariabel; se
[Prosjekter](/en/reference/projects/) og [Tid](/en/reference/time/). Utgifter trenger
objektlageret konfigurert (`STORAGE_PROVIDER` i `vantigo.env`, den samme innstillingen
Kommunikasjon-vedlegg bruker) i det øyeblikket noen prøver å legge ved en kvittering,
uansett om kvitteringsregelen krever en: ukonfigurert svarer en opplasting eller
nedlasting av kvittering med 503 i stedet for at prosessen nekter å starte. Se
[Utgifter](/en/reference/expenses/) og [Objektlagring](/nb/admin/object-storage/).

Telemetri er av som standard. De vanlige OTLP-variablene kan settes i `vantigo.env`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
# OTEL_EXPORTER_OTLP_HEADERS=x-api-key=secret
```

## Produksjonsmerknader

- Sett applikasjonen bak en omvendt proxy som terminerer TLS, og konfigurer
  `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` til å stole på nøyaktig den proxyen.
  Hurtigstart-stacken serverer `http://localhost:8080`; på et nettverk du ikke
  kontrollerer, sender et http-opphav sesjons-cookies og bearer-lenkene for
  invitasjon og passordtilbakestilling i klartekst. Se
  [Transportsikkerhet](/nb/admin/transport-security/).
- Sett `APP_URL` til det offentlige `https://`-opphavet. E-postlenker, de godtatte
  `Host`-verdiene, cookie-attributtet `Secure` og den statiske OIDC-tilbakekallingen
  utledes alle fra den. Tilbakekallingen er fast på `/api/v1/identity/oidc/callback`.
- Den medfølgende PostgreSQL-en nås over det private Compose-nettverket uten noen
  sertifikatutsteder, så `DATABASE_URL` har ingen `sslmode`, og pgx kobler til slik
  libpq sin `prefer` gjør. Hvis du i stedet peker stacken mot en PostgreSQL over et
  nettverk, legg til `sslmode=verify-full` (eller `verify-ca` når serversertifikatet
  ikke navngir verten) på både tjenesten `vantigo-migrate` og `vantigo` — rediger
  `compose.yaml`, ikke `vantigo.env`, siden det er `compose.yaml` som bygger
  tilkoblings-URL-en.
- `vantigo.env.example` dokumenterer også innstillingene for e-faktura,
  `INVOICES_EHF_ENABLED` og `INVOICES_STORECOVE_BASE_URL`, ved siden av
  Peppol-oppslagets; å sette opp sending som EHF — Storecove-kontoen, påloggingsdataene
  og KID-avtalen — er en egen prosedyre ([E-faktura](/nb/admin/e-invoicing/)).
- `vantigo.env.example` dokumenterer også, kommentert ut, innstillingene for betaling av
  fakturaer med Vipps MobilePay: `PUBLIC_BASE_URL`, der betalingssiden kan nås fra
  internett, og `INVOICES_VIPPS_ENABLED` (standard på) og `INVOICES_VIPPS_BASE_URL`
  (standard `https://api.vipps.no`). Å publisere betalingssiden — verten, proxyen og å
  holde tokenet i betalingslenken ute av proxyens logger — er en egen prosedyre
  ([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten)); proxyen foran
  den offentlige verten må sende `Host` som det offentlige vertsnavnet, en konstant,
  aldri klientens. Den samme offentlige verten bærer nå også Microsofts endringsvarsler
  for Outlook-synkroniseringen i Kalender, `POST /api/v1/entra/mailbox-notifications`, som
  proxyen må slippe gjennom, og `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` (standard på; `0`
  for en offentlig vert Microsoft ikke kan nå) slår dem av
  ([Microsoft Entra ID](/nb/admin/entra/#endringsvarsler),
  [Kalender](/nb/admin/calendar/#postbokssynkroniseringen)).
- `vantigo.env.example` dokumenterer også `STAFF_IMPORT_MAX_ROWS`, hvor mange personer
  én import i Ansatte tar og én eksport skriver
  ([Ansatte](/nb/admin/staff/#importer-og-eksporter-registeret)), og
  oppbevaringsarbeiderens `STAFF_RETENTION_ENABLED` (standard på) og
  `STAFF_WORKERS_POLL` (standard `1h`, minst `1m`), som sletter det oppbevaringsreglene
  ikke lenger lar registeret beholde, og fullfører slettingen av dokumentenes filer
  ([Ansatte](/nb/admin/staff/#oppbevaring)), og kontoarbeiderens
  `STAFF_ACCOUNTS_ENABLED` (standard på), som klargjør, aktiverer og deaktiverer brukerne
  kontoflagget ber om, på den samme kjøringsfrekvensen
  ([Ansatte](/nb/admin/staff/#kontoarbeideren)). Den leser kontomodusen fra
  innstillingene for single sign-on i databasen, slik API-et gjør. Kompetansearbeiderens
  `STAFF_COMPETENCE_ENABLED` (standard på) sender en person og lederne deres e-post før
  et sertifikat utløper, og når det har utløpt, på den samme kjøringsfrekvensen
  ([Ansatte](/nb/admin/staff/#kompetansearbeideren)).
- Det finnes **ingen `ENTRA_*`-variabel**. Leietakeren og klienten til Microsoft Entra
  ID-modulen, klienthemmelighetene (forseglet under `APP_SECRET`) og kontoreglene
  ligger i databasen, satt av en eier og `entra:manage` på modulens side; modulen når
  to faste verter som ingen innstilling endrer
  ([Microsoft Entra ID](/nb/admin/entra/)).
- OIDC-leverandøren for arbeidsstyrken og SCIM-tokenet settes på kortet **Single
  sign-on**, ikke i `vantigo.env`: `OIDC_*` og `SCIM_*` der importeres i databasen én
  gang, ved første oppstart av utgivelsen som kom med kortet, og varsles deretter bare
  om — men valideres fortsatt ved oppstart — til du fjerner dem
  ([Før kortet](/nb/admin/sso-scim/#før-kortet-miljøvariablene)). Én OIDC-variabel
  forblir i bruk: `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR`, katalogen en workload
  identity-tokenfil lagret på kortet må ligge i, som standard webhookens
  `/var/run/secrets/azure/tokens`
  ([Workload identity](/nb/admin/sso-scim/#microsoft-entra-id-workload-identity)).
  Ikke legg hemmeligheter i filer under versjonskontroll — injiser
  `COMMUNICATIONS_AI_API_KEY` fra en hemmelighetsforvalter i stedet.
- SCIM bruker `/api/v1/identity/scim/v2`, bearer-tokener og `application/scim+json`.
  En Owner utsteder tokenet med **Nytt token** på kortet, og roterer det der med en
  overlapping på høyst 24 timer. Se [SSO- og SCIM-drift](/nb/admin/sso-scim/#scim).
- `MANAGEMENT_PORT` og `MANAGEMENT_TOKEN` aktiverer et privat statusendepunkt for et
  kontrollplan; se [management-lytteren](/nb/admin/management-listener/).
  Compose-stacken publiserer bevisst ikke den porten: nå den fra en annen container
  på Compose-nettverket, aldri fra vertens offentlige grensesnitt.
- **Fjern `OWNERS_REQUIRE_MFA=0` og `OWNERS_ALLOW_INSECURE_NO_MFA=1` fra
  `vantigo.env`.** Hurtigstart-stacken leveres med begge fordi en fersk installasjon
  ikke har noen registrert autentikator, og med kravet på holdes en administrator
  igjen på `/settings/security` til en er registrert. Det er `=0` som slår kravet av;
  bekreftelsen alene gjør det ikke, den lar bare API-et starte med kravet av. Blir de
  stående, er et kompromittert Owner- eller SystemAdmin-passord alene nok til full
  kontroll over identitet og leietakerens kontrollplan. Registrer en autentikator for
  hver privilegert konto, sett deretter `OWNERS_REQUIRE_MFA=1` og fjern bekreftelsen —
  se [Identitet, autentisering og utrulling](/nb/admin/authentication/).
- Kjør aldri `seed` i produksjon; den er bare for utvikling (`APP_ENV=development`).
- `APP_SECRET` utleder hver krypteringsnøkkel denne prosessen bruker (CSRF-tokener,
  cookie-signering, kryptering av TOTP-hemmeligheter) via HKDF-SHA256 — det finnes
  ikke noe eksternt nøkkelhvelv å sette opp og ingenting å pakke inn. Generer den én
  gang med `openssl rand -base64 32`, lagre den i en hemmelighetsforvalter, og behandle
  tap av den som tap av en signeringsnøkkel: hver åpen sesjon og hver lagret
  TOTP-hemmelighet blir umulig å gjenopprette.

`api`-modus kjører ventende migreringer før den begynner å servere, i tillegg til
engangsjobben `vantigo-migrate` som denne stacken kjører først. For en kontrollert
utgivelse kjører du likevel nøyaktig én migreringsjobb, venter på vellykket
fullføring, og starter deretter API-et. Poenget er å se en migreringsfeil før noen
API-replika starter, ikke å hoppe over `api` sin egen oppstartssjekk. Ta og verifiser
en PostgreSQL-sikkerhetskopi før du ruller ut en utgivelse som inneholder en
destruktiv migrering. Migreringer går bare framover; ikke planlegg en tilbakerulling
av applikasjonen over en.

## Oppgradering

```bash
docker compose pull
docker compose up -d
```

Compose henter det nye imaget, kjører `vantigo-migrate` på nytt som en engangsjobb, og
starter `vantigo` på nytt først når migreringene har fullført vellykket. Hvis du har
låst `VANTIGO_TAG` i `.env`, endrer du den til den nye utgivelsen først.

**Utgivelsen med Fakturaer.** En installasjon som lar `MODULES` stå usatt aktiverer
hver modul denne binæren kjenner, så den får Fakturaer-appen ved oppgradering. Ingen
kan bruke den før en rolle gir `invoices:access` (Owner har allerede hver rettighet).
Utstedelse trenger et objektlager (`STORAGE_PROVIDER`): uten et åpner appen, og hver
utstedelse svarer 503. En installasjon som lister `MODULES` eksplisitt får Fakturaer
først når `invoices` legges til, ved siden av `customers`. Se
[Fakturaer](/en/reference/invoices/).

**Utgivelsen med fakturabetalinger og sending.** Én ny rettighet,
`invoices:payments` — registrere og fjerne betalinger — som ingen innebygd rolle har;
Owner har den gjennom jokertegnet, og alle andre trenger en rolle som gir den. Å sende
et dokument på e-post ligger under `invoices:issue`, så hver rolle som har
`invoices:issue` kan sende fra denne utgivelsen. Det går gjennom den samme
`SMTP_*`-konfigurasjonen som identitetens e-post
([E-post og observerbarhet](#e-post-og-observerbarhet)): en installasjon uten en
fungerende SMTP-server kan ikke sende fakturaer, og en på `MAIL_DRIVER=log` (bare
utvikling) svarer hver sending med 503. En retur går til konvoluttavsenderen,
`SMTP_FROM`, ikke til selgerens Reply-To, og Vantigo registrerer ingen: pek `SMTP_FROM`
mot en postkasse noen leser hvis returer betyr noe.

Dokumenter utstedt før denne utgivelsen til en norsk virksomhet har ikke
organisasjonsnummer i kjøperøyeblikksbildet — `buyer_foreign_id` viser `no…` i stedet
— fordi utstedelsen sammenlignet katalogens land med skille mellom store og små
bokstaver. De øyeblikksbildene er uforanderlige: der det betyr noe, krediterer du et
slikt dokument og utsteder det på nytt. Dokumenter utstedt fra denne utgivelsen av er
riktige.

**Utgivelsen med Ansatte.** En installasjon som lar `MODULES` stå usatt aktiverer hver
modul denne binæren kjenner, så den får Ansatte-appen ved oppgradering, og migreringen
oppretter skjemaet `staff`. Bare Owner kan bruke den — Owner har allerede hver
rettighet — før en rolle gir `staff:access`; de fire andre rettighetene ligger over
den. Hvis databasen kjører en applikasjonsrolle med minste privilegium, gir du den
skjemaet `staff` som over. En installasjon som lister `MODULES` eksplisitt får Ansatte
først når `staff` legges til; den trenger ingen annen modul. Se
[Ansatte](/nb/admin/staff/).

**Utgivelsen med Ansattes kontoer.** Den har to migreringer, én per skjema: identitys
`00050` (brukere styrt av Ansatte, ansattrollene, den registrerte autoritetsmodusen) og
Ansattes `00051`. Ansattes har med seg, i sin helhet: en kobling som ennå ikke navngir
en bruker (kontoarbeiderens kobling mens den klargjøres), kontoflaggets egen revisjon
(`people.account_revision`, 1 for alle), koblingens synkroniserte ansattnummer,
deaktiveringen *Deaktiver nå* ba om (`identity_links.disable_requested_at`),
ansattnummeret unikt under sorteringen `C` (nedenfor), den forhåndsdefinerte typen
`ammefri` lønnet, de reserverte skjulte typene `absent_hours`, `absent_hr_only` og
`absent_hr_only_hours`, og de to sletteinnstillingene (`erase_pregnancy_health_types`,
`erase_sick_case_dates`, begge av). Den starter også kontoarbeideren,
`staff-accounts`, på som standard. Den gjør ingenting før HR slår på en persons
kontoflagg; fra da oppretter den personens ventende, deaktiverte bruker før startdatoen
også før en Owner har navngitt ansattrollene, og aktiverer ingen før de er navngitt.
`STAFF_ACCOUNTS_ENABLED=0` holder den av.
Ansatte-migreringen (`00051`) gjør en persons
ansattnummer unikt etter store bokstaver under sorteringen `C`, så store og små
bokstaver vurderes likt uansett hvilken locale databasen ble opprettet med. Under en
tyrkisk eller aserbajdsjansk locale kan den gamle indeksen ha godtatt to numre som bare
skiller seg i store og små bokstaver (`i-1` ved siden av `I-1`), og da feiler
migreringen på den nye indeksen. Før du oppgraderer en slik installasjon, ser du etter
dem:

```sql
SELECT upper(employee_number COLLATE "C"), count(*) FROM staff.people GROUP BY 1 HAVING count(*) > 1;
```

Ingen rad betyr at oppgraderingen er trygg; for hver rad gir du først én av personene et
annet nummer i Ansatte. Migreringen avviser også, med nøkkelen, en egen fraværstype som
alt har en av de tre reserverte nøklene; se etter dem også:

```sql
SELECT key FROM staff.absence_types WHERE key IN ('absent_hours','absent_hr_only','absent_hr_only_hours') AND NOT seeded;
```

Ingen rad betyr at oppgraderingen er trygg; ellers gir du først hver type en ny nøkkel.

Å rulle `00051` tilbake er en operatørs siste utvei. Den sletter alle koblinger som
fortsatt klargjøres, med eller uten bruker: de ventende brukerne de koblingene navnga,
blir værende i identity, styrt av Ansatte, for en Owner å slutte å styre og slette. Den
avvises, med typene, så lenge et fravær navngir en av de reserverte typene eller en
hendelses `typeIds` gjør det — slett eller gi radene en annen type, og skriv om eller
slett hendelsene, for hånd først. Å rulle identitys `00050` tilbake er også en siste
utvei: den fjerner hvem som styrer en bruker, ansattnummeret og om brukeren venter, så
en ventende bruker Ansatte laget, ser etterpå ut som en vanlig deaktivert bruker, og å
rulle fram igjen etterlater dem foreldreløse — styrt av ingen, aldri invitert. Før du
ruller den tilbake, lar du en Owner slutte å styre og slette de ventende brukerne på
siden **Brukere**.

**Utgivelsen med kortet Single sign-on.** Den har identitys
`00058_identity_sso_settings.sql`, raden OIDC-leverandøren for arbeidsstyrken og
SCIM-legitimasjonen ligger i fra nå av. La `OIDC_*` og `SCIM_*` stå til den første
oppstarten: API-et importerer dem i raden, revisjonslogger `sso-settings.imported` med
navnene deres og logger en advarsel; fjern dem deretter fra hver container. En worker
trenger dem ikke lenger. Å rulle migreringen tilbake dropper raden med den forseglede
klienthemmeligheten og tokensammendragene, så sett variablene igjen først, med et nytt
`SCIM_TOKEN` når kortet utstedte det nåværende. Prosedyren står i
[Før kortet](/nb/admin/sso-scim/#før-kortet-miljøvariablene).

**Utgivelsen med Microsoft Entra ID.** Den har `00061_entra_baseline.sql`, skjemaet
`entra`. En installasjon som lar `MODULES` stå usatt, får modulen, og flisen dens for
eiere, ved oppgradering; en som lister `MODULES`, får den når `entra` legges til ved
siden av `staff`, som den krever — `entra` uten `staff` nekter å starte. Ingenting når
en leietaker før en eier registrerer en på modulens side, og Ansatte lager kontoer der
bare for personer med kontotypen **Microsoft Entra ID**. En rolle med minste privilegium
trenger tilgang til skjemaet `entra` ([Databaseroller](#databaseroller)). Å rulle
migreringen tilbake dropper skjemaet med de forseglede hemmelighetene og et pass som
ikke er vist ennå, og rører ingenting i leietakeren. Se
[Microsoft Entra ID](/nb/admin/entra/).

**Utgivelsen med Bemanning.** Den har `00072_planning_baseline.sql`, skjemaet
`planning` med innstillingene på standardverdiene og ingen allokeringer. En installasjon
som lar `MODULES` stå usatt, får modulen, og **Bemanning**-flisen for alle som får
`planning:access`, ved oppgradering; en som lister `MODULES`, får den når `planning`
legges til ved siden av `projects` og `staff`, som den krever begge — `planning` uten en
av dem nekter å starte. De tre rettighetene er nye, og ingen egendefinert rolle har dem
før en eier legger dem til. En rolle med minste privilegium trenger tilgang til skjemaet
`planning` ([Databaseroller](#databaseroller)). Den starter ingen bakgrunnsjobb og
trenger ingen innstilling; det er ikke noe mer å gjøre. Å rulle migreringen tilbake
dropper skjemaet med alle allokeringene. Se [Bemanning](/nb/user/planning/).

**Utgivelsen med kompetanse i Ansatte.** Den har `00066_staff_competence.sql`:
kompetansekatalogen — fem kategorier og femten norske sertifikater forhåndsdefinert — og
personenes kompetanser, tomme. Ingen rettighet er ny: `staff:approve` bekrefter nå også
kompetanser, og `staff:manage` holder katalogen. Den starter også kompetansearbeideren,
`staff-competence`, på som standard; den sender ingen e-post før noen har et sertifikat
med sluttdato, og `STAFF_COMPETENCE_ENABLED=0` holder den av. Å rulle migreringen
tilbake dropper de tre tabellene med alle personers kompetanserader; dokumentasjonen
blir liggende blant personenes dokumenter. Se
[Ansatte](/nb/admin/staff/#kompetansearbeideren).

**Utgivelsen med kompetansesamtalen.** Den har `00067_staff_one_to_one.sql`: fire tomme
tabeller for det hver person ønsker — interessene, preferansene og
tilgjengelighetserklæringen deres — og for kompetansesamtaler; Sett-stempelet på
personenes kompetanserader; og intervallet for kompetansesamtaler i innstillingene, 12
måneder som standard, så dashbordet begynner å vise en forfalt samtale for alle i
arbeid som ikke har hatt en innen intervallet etter den siste samtalen eller starten på
det arbeidsforholdet de er i nå — sett det til 0 under
[innstillingene](/nb/admin/staff/#innstillingene) først for å holde punktet av. Ingen
rettighet er ny: `staff:approve` registrerer nå også kompetansesamtaler. Å rulle
migreringen tilbake dropper de fire tabellene med alle personers ønsker og samtaler,
Sett-stempelene på kompetanseradene og intervallinnstillingen.

**Utgivelsen med kompetanseprofiler.** Den har
`00068_staff_competence_profiles.sql`: tre tomme tabeller for kompetanseprofilene —
profilene, punktene deres og profilene som er tildelt personer — og en kolonne på
avdelingene for en avdelings standardprofil, som kan stå tom og er tom. Ingen innstilling og
ingen rettighet er ny: `staff:manage` holder profilene sammen med kompetansekatalogen.
Dashbordet begynner å vise at noen ikke oppfyller kompetanseprofilen sin først når HR
har opprettet en profil og tildelt den til noen eller gjort den til en avdelings
standardprofil. Å rulle migreringen tilbake dropper profilene, punktene deres,
tildelingene og avdelingenes standardprofiler.

**Utgivelsen med bilder av personene.** Den har `00069_staff_person_photo.sql`: tre
kolonner på Ansattes personer, som kan stå tomme og er tomme — bildets nøkkel i
objektlageret, typen og når det ble lagt inn. Ingen innstilling og ingen rettighet er
ny: personen selv, `staff:manage` og `staff:personal-data` legger inn bildet til en
person, og alle som ser personen, ser det. Selve bildene ligger i objektlageret under
`staff`-området, slik personenes dokumenter gjør, så en installasjon uten objektlager
(`STORAGE_PROVIDER`) svarer at lagringen ikke er tilgjengelig når noen laster opp et
bilde; et byttet eller fjernet bilde, og bildet til en person som er slettet, fjernes
fra lageret av sletteloggen i Ansatte, som oppbevaringsarbeideren kjører. Den samme
utgivelsen viser en persons timer denne uken på personsiden når Timer-modulen er på,
lest fra Timer uten noen ny rettighet; med `time` av sier kortet at timeføring er slått
av. Å rulle migreringen tilbake dropper de tre kolonnene, men ikke objektene: bildene
personene har akkurat da, blir liggende i lageret uten at noe peker på dem, og objekter
som alt står i kø, fjernes fortsatt av sletteloggen. Vil du ikke la noe bilde bli igjen,
får du fjernet alle bildene — eller sletter nøklene `people/*/photo/` under
`staff`-området for hånd — og lar sletteloggen kjøre før du ruller tilbake.

**Utgivelsen der bildet følger kontoen.** Den har `00070_identity_managed_avatars.sql`,
en tom tabell i identitetsmodulen for de **styrte profilbildene** — HRs bilde av en
person, som står som den koblede brukerens bilde foran det brukeren valgte selv — og
`00071_staff_photo_push.sql`, én tom kolonne på Ansattes leverandørkoblinger, stempelet
til bildet som sist ble skrevet til Microsoft Entra ID. Ingen innstilling og ingen
rettighet er ny, og Entra-applikasjonen trenger ikke noe nytt samtykke:
`User.ReadWrite.All` dekker bildet. Etter oppgraderingen legger bare HR inn bildet til en
person i Ansatte; en person bytter sitt eget bilde gjennom profilbildet sitt i Vantigo,
på Min side eller under **Innstillinger › Profil**, som er stengt så lenge HRs bilde
står. Kontoarbeideren i Ansatte gjør så hvert eksisterende HR-bilde til den koblede
brukerens bilde ved personens neste kjøring, og skriver bildet til hver aktiverte
Entra-konto én gang; for en person uten bilde i Vantigo skriver den ingenting, så et
bilde som alt ligger i leietakeren, blir stående til Vantigo skriver et. Å rulle
tilbake 00071 dropper stemplene, så hvert bilde skrives til leietakeren én gang til
etter neste oppgradering; å rulle tilbake 00070 dropper de styrte profilbildene, så
hver brukers eget profilbilde er bildet deres igjen til Ansatte legger inn HRs bilder
på nytt. Bildene i Ansattes objektlager berøres ikke i noe tilfelle.

For en komplett driftsbok for sikkerhetskopi, én migrator, token-rotasjon og
Owner-nødtilgang, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).
