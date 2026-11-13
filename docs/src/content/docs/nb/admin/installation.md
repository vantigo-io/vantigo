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
Fakturaer, Ansatte, Bemanning, Lager, Ordre, Produksjon, Kunnskap, Kalender, Microsoft Entra ID, Google Workspace og Assistent er aktivert som standard
(`MODULES` i `vantigo.env`). De deler én PostgreSQL-database ved navn `vantigo`, med
uavhengige skjemaer `identity`, `customers`, `products`, `energy`, `communications`,
`projects`, `time`, `expenses`, `invoices`, `staff`, `planning`, `inventory`, `orders`, `manufacturing`, `knowledge`, `calendar`, `entra`, `google` og `assistant`, hver med sin egen
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
demodatasett på tvers av alle moduler (20 brukere fordelt på 9 tilgangsroller, 200
bedrifter med 400 kontakter, 62 produkter, 75 prosjekter, timeregistreringer, utlegg,
fakturaer, målepunkter, kundesamtaler og et lite verksted med lager, stykklister,
produksjonsordrer og salgsordrer). Ingen kommando, eller en ukjent en, skriver
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
- **`SMTP_HOST`, `SMTP_FROM`** (`vantigo.env`) — systempostboksen, som skriver til dine egne
  ansatte; påkrevd for at serveren skal starte i det hele tatt. `SMTP_FROM` tolkes som en ren adresse: `no-reply@example.com`
  godtas, `Vantigo <no-reply@example.com>` avvises ved oppstart. `SMTP_PORT` har
  standardverdi 587, `SMTP_USERNAME` og `SMTP_PASSWORD` er valgfrie, og `SMTP_TLS` er
  `starttls` som standard (`implicit` forhandler også TLS; `none` er klartekst). La
  `MAIL_DRIVER` stå usatt: den har allerede standardverdien `smtp` her, og `log`
  avvises utenfor utvikling fordi de e-postene bærer bearer-lenker.
  For å sende gjennom Cloudflare Email Service i stedet for en SMTP-tjener, setter du
  `MAIL_DRIVER=cloudflare` og `CLOUDFLARE_EMAIL_*`-innstillingene; `SMTP_*` leses da
  ikke ([Cloudflare Email Service](/nb/admin/authentication/#cloudflare-email-service)).
- **`MODULES`** (`vantigo.env`) — den kommaseparerte listen over forretningsmoduler
  denne installasjonen aktiverer. `identity` er alltid montert og listes aldri. Usatt
  aktiverer hver modul binæren kan montere: `customers`, `products`, `energy`,
  `communications`, `projects`, `time`, `expenses`, `invoices`, `staff`, `planning`,
  `inventory`, `orders`, `manufacturing`, `knowledge`, `calendar`, `entra`, `google` og `assistant`. `energy`, `communications`, `projects` og `invoices` krever hver
  `customers`, `time` krever `projects`, `planning` krever både `projects` og `staff`,
  `inventory` krever `products`, `orders` krever `products`, `customers` og `invoices`,
  `manufacturing` krever `products` og `inventory`, og `entra` og `google` krever hver `staff`; `projects` bruker
  også `products` når den er aktivert og svarer 409 på endepunktene for
  faktureringslinjer når den ikke er det; `expenses` krever ingenting og leser
  `projects` bare når den tilfeldigvis er aktivert; `staff`, `calendar` og `assistant` krever
  ingenting (assistenten har bare nytte av seg mens `AI_PROVIDER` er satt).
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
GRANT USAGE ON SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses, invoices, staff, planning, inventory, orders, manufacturing, knowledge, calendar, entra, google, assistant TO "vantigo_app";
```

## Bakgrunnsjobber og skalering

Standardoppsettet med én container kjører bakgrunnsjobbene til hver aktivert modul
(utboks-levering, oppbevaring, opprydding av vedlegg, Kommunikasjons
postbokssynkronisering `communications-mailbox-sync`, alltid, som leser innboksen til hver
Microsoft 365-kanal hvert 30. sekund — den trenger objektlageret og utgående https til
`graph.microsoft.com` —, Kommunikasjons meldingssynkronisering `communications-messenger-sync`,
alltid, som hvert 2. sekund spør meldingsleverandørene hvilke sider som har nye hendelser og
leser hendelsesinnboksen til hver Messenger-kanal — den trenger objektlageret, og når ingenting
på nettverket selv: meldingsleverandørens egen modul holder forbindelsen —, kundemodulens registerjobber,
kundemodulens importjobb `customers-import`, energimodulens importjobb `energy-import`, energimodulens spotprisjobb
`energy-spot-prices` når `ENERGY_SPOT_PRICE_SERVICE_URL` er satt eller `ENERGY_SPOT_PRICE_FEED_ENABLED=1`, fakturamodulens EHF-jobber `invoices-ehf` og `invoices-ehf-events` når
`INVOICES_EHF_ENABLED` er på, fakturamodulens purrejobb `invoices-reminders`, alltid,
fakturamodulens faktureringsjobb `invoices-billing`, alltid, fakturamodulens avstemming av
ordreallokeringer `invoices.allocation_reconcile`, alltid,
fakturamodulens betalingsjobb `invoices-payments` når `INVOICES_VIPPS_ENABLED` er på, og
kalendermodulens avlysningsjobb `calendar-cancellations` med mindre
`CALENDAR_CANCELLATIONS_ENABLED=0`, kalendermodulens abonnementsjobb
`calendar-subscriptions` med mindre `CALENDAR_SUBSCRIPTIONS_ENABLED=0`, og kalendermodulens
postbokssynkronisering `calendar-mailbox-sync` med mindre `CALENDAR_MAILBOX_SYNC_ENABLED=0`,
og Microsoft Entra ID-modulens `entra-outlook-sweep`, alltid, som rydder i folks
Outlook-tilkoblinger hver time, Google Workspace-modulens `google-sweep`, alltid, som gjør
det samme for folks Google-tilkoblinger, Meta-modulens `meta-split`, alltid, som deler
Metas lagrede webhook-hendelser opp i innboksen til Messenger-kanalene og henter filene
folk sendte fra Metas tjenere, og `meta-retention`, alltid, som hver time sletter det modulen
oppbevarer forbi periodene sine, og Assistentens `assistant-runs`, alltid, som feiler en
kjøring en omstart etterlot som pågående, og `assistant-retention`, alltid, som sletter samtaler
som er eldre enn oppbevaringstiden én gang i døgnet — og plattformens hendelsesutsender
`events.dispatch`, alltid, uansett hva `MODULES` sier) inne i API-prosessen: `WORKERS_IN_PROCESS=1`, standardverdien. Utrullinger som
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
kalenderens avlysningsjobb, `entra-outlook-sweep` og `assistant-retention` en rådgivende lås (advisory lock) i PostgreSQL, så hver av dem kjører på nøyaktig én
instans per syklus; utboksen, `invoices-ehf`-jobben, `invoices-reminders`-jobben,
`invoices-billing`-jobben, `invoices-payments`-jobben, `communications-mailbox-sync`,
`communications-messenger-sync`, kalenderens abonnements- og postboksjobb og `events.dispatch` tar i stedet én rad om gangen under en tidsbegrenset lås, så
et hvilket som helst antall instanser deler arbeidet uten å sende noe to ganger.
`meta-split` tar de lagrede hendelsene under en lease og `meta-retention` sletter i grupper,
så begge er trygge på et hvilket som helst antall instanser.
`google-sweep` og `invoices.allocation_reconcile` tar ingen av delene: hvert av stegene
deres tar radene sine i én setning, så to instanser behandler aldri samme rad. Purrejobben sender ett brev om
gangen, høyst ett i sekundet per instans, og hver sending må bli ferdig godt innenfor
låsen på 60 sekunder
([Purrejobben og e-post](/nb/admin/payments/#purrejobben-og-e-post)).
Betalingsjobben ser etter arbeid hvert sekund og behandler alle Vipps-betalinger som er
klare, i hver syklus
([Betalingsjobben](/nb/admin/payments/#betalingsjobben)).
Faktureringsjobben har ingen innstillinger: den utfører fakturakjøringene folk lager
under **Fakturaer → Fakturakjøringer** — av forbruk, eller av ordre når Ordre er slått
på —, ser etter arbeid hvert 5. sekund og lager — og,
når kjøringen ber om det, utsteder — én kundes fakturaer om gangen, høyst fem kunder
per syklus, hver under en lås på 10 minutter; den sender ingenting
([Billing runs](/en/reference/invoices/#billing-runs)).
Hendelsesutsenderen har heller ingen innstillinger og trenger ingen oppføring i
`MODULES`: hvert 5. sekund leverer den hendelsene modulene publiserer til modulene som
tar imot dem, høyst 100 om gangen, hver under en lås på 2 minutter, prøver en feil på
nytt med økende ventetid, og lar en levering som har feilet åtte ganger, bli liggende
død til operatøren prøver den på nytt eller hopper over den på
[management-lytteren](/nb/admin/management-listener/#hendelsesutboksen), der
logglinjene også står.

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

**Legg til to tilkoblinger per HTTP-replika for sanntidsoppdateringer.** Hver replika som
serverer HTTP (`api`- og `server`-modus) holder to PostgreSQL-tilkoblinger til som ikke
er en del av poolen og ikke begrenses av `pool_max_conns`: lytteren som hører endringer,
og utgiveren som deler hvem som ser på hva. En ren `worker`-prosess åpner ingen av dem.
Ta dem med i formelen:

```
replikaer × (poolstørrelse + 2)  ≤  max_connections − margin
```

De kobler til `LIVE_DATABASE_URL`, eller til `DATABASE_URL` når den ikke er satt
([Sanntidsoppdateringer og proxyen din](#sanntidsoppdateringer-og-proxyen-din)).

## Basesti og omvendt proxy

Hele applikasjonen kan serveres under ett konfigurerbart stiprefiks. Sett
`APP_BASE_PATH` i `vantigo.env` og sett `APP_URL` til det offentlige skjemaet og
verten — ingen sti, siden stiprefikset er `APP_BASE_PATH`, ikke en del av `APP_URL`.
Omdirigerings-URI-er registrert andre steder følger begge: Microsoft Entra ID-modulens
Outlook-tilkobling svarer Microsoft på `APP_URL` + `APP_BASE_PATH` +
`/api/v1/entra/outlook/callback` — aldri på `PUBLIC_BASE_URL` — så endrer du en av dem,
må den nye URI-en legges til appregistreringen
([Microsoft Entra ID](/nb/admin/entra/#outlook-tilkobling)). Tilsvarende svarer
Google Workspace-modulens tilkobling for personer Google på `APP_URL` + `APP_BASE_PATH` +
`/api/v1/google/connect/callback`, aldri på `PUBLIC_BASE_URL`, og den må registreres på
den interne nettklienten ([Google Workspace](/nb/admin/google-workspace/#tilkobling-for-personer)).
API-prefiksene forblir `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`,
`/api/v1/energy`, `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`,
`/api/v1/expenses`, `/api/v1/invoices`, `/api/v1/staff`, `/api/v1/planning`,
`/api/v1/inventory`, `/api/v1/orders`, `/api/v1/manufacturing`, `/api/v1/knowledge`, `/api/v1/calendar`, `/api/v1/entra`, `/api/v1/google` og `/api/v1/assistant`.

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

## Sanntidsoppdateringer og proxyen din

Vantigo holder skjermbildene oppdatert uten at du laster siden på nytt: Innboks i
Kommunikasjon og en åpen samtale oppdateres av seg selv, og viser hvem andre som er i en
samtale. Nettleseren åpner én WebSocket til `GET /api/v1/live` (under `APP_BASE_PATH` når
du har satt en), og serveren forteller hva som er endret. Kan ikke forbindelsen opprettes,
går ingenting i stykker: hvert skjermbilde faller tilbake til å oppdatere seg omtrent én
gang i minuttet. Gå gjennom dette avsnittet slik at forbindelsen kan opprettes.

### Forutsetninger

- Den omvendte proxyen må slippe gjennom WebSocket-oppgraderinger for `/api/v1/live`.
- Proxyens lesetidsavbrudd for den stien må være minst **75 sekunder**. Serveren sender en
  ping hvert 25. sekund og kutter en forbindelse som har vært stille i 60, så en proxy som
  kutter ledige forbindelser etter 60 sekunder går også bra, men en som kutter etter 30
  gjør det ikke.
- Proxyen må slippe gjennom **binære** WebSocket-meldinger på samme måte som tekstmeldinger, og
  tillate meldinger på minst **4 MiB**: samredigering i Kunnskap sender hele dokumenter i én
  melding. En proxy som begrenser meldingsstørrelsen (nginx har ingen slik grense som
  standard) må settes opp for denne stien.
- Proxyen må videresende `Origin` uendret, og `APP_URL` må være adressen folk åpner i
  nettleseren: serveren godtar håndtrykket bare fra den opprinnelsen.
- `LISTEN` må fungere på databasetilkoblingen ([Pooler](#pooler-og-live_database_url)).

### Sett opp proxyen

nginx, i `server`-blokken fra [Basesti og omvendt proxy](#basesti-og-omvendt-proxy):

```nginx
location /vantigo/api/v1/live {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 75s;
}
```

Uten `APP_BASE_PATH` er stien `/api/v1/live`. Den må stå før, eller være mer spesifikk
enn, `location`-blokken som serverer resten av applikasjonen.

Caddy slipper gjennom oppgraderinger av seg selv, så den vanlige blokken er nok:

```
vantigo.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy setter ikke noe lesetidsavbrudd. Setter du et på transporten, hold det på 75 sekunder
eller mer. For andre proxyer og lastbalanserere: se etter «WebSocket», «upgrade» eller
«idle timeout» i innstillingene, og sett tidsavbruddet for ledig forbindelse til minst
75 sekunder.

### Pooler og LIVE_DATABASE_URL

Sanntidsoppdateringer bruker PostgreSQLs `LISTEN`, som ikke fungerer gjennom en pooler i
**transaksjonsmodus** (PgBouncer i transaksjonsmodus, eller en administrert leverandørs
innebygde pooler). Går `DATABASE_URL` gjennom en slik, setter du `LIVE_DATABASE_URL` til
databasens direkteport, med samme rolle. Den brukes bare til de to sanntidstilkoblingene;
poolen bruker fortsatt `DATABASE_URL`. Når den ikke er satt, bruker sanntidstilkoblingene
`DATABASE_URL`. Verdien sladdes når konfigurasjonen skrives ut.

### Slå det av

`LIVE_ENABLED=0` får `/api/v1/live` til å svare `404`. Hvert skjermbilde oppdaterer seg da
omtrent én gang i minuttet, som når forbindelsen ikke kan opprettes. Det er på som standard.

### Kontroller

1. Logg inn, åpne **Innboks**, og åpne nettleserens utviklerverktøy på Nettverk-fanen.
2. Filtrer på WS. En forespørsel til `live?v=1` skal ha status `101 Switching Protocols`
   og forbli åpen.
3. Be om status fra en replika ([Management-lytteren](/nb/admin/management-listener/#endepunktet)):
   `live`-blokken skal vise `"listener": "up"`, og `connections` skal telle de åpne fanene.

### Hva kan gå galt

| Du ser | Sannsynlig årsak |
| --- | --- |
| En `4xx` (typisk `426`) eller ingen `101`, forespørselen avsluttes med en gang | Proxyen sender ikke `Upgrade` og `Connection` videre. |
| `101`, men lukket etter nøyaktig 30 eller 60 sekunder og åpnet igjen og igjen | Proxyens tidsavbrudd for ledig forbindelse er kortere enn serverens ping; sett det til 75 sekunder eller mer. |
| `403` | `Origin` samsvarer ikke med `APP_URL`: en proxy som skriver den om, eller folk som åpner Vantigo på en annen adresse. |
| `404` | `LIVE_ENABLED=0`, eller replikaen kjører bare arbeidere. |
| `401` | Økten er avsluttet; logg inn på nytt. |
| `429` | Mer enn 30 tilkoblingsforsøk i minuttet for én person, vanligvis en løkke foran proxyen. |
| `"listener": "down"` og skjermbildene oppdaterer seg hvert minutt | Databasetilkoblingen for `LISTEN` feiler: en pooler i transaksjonsmodus (sett `LIVE_DATABASE_URL`), en rolle som ikke har lov til å koble til, eller `max_connections` er nådd fordi de to ekstra tilkoblingene ikke ble talt med. Replikaen kobler til igjen av seg selv; den tester også en stille lytter hvert 30. sekund. |
| `queueUsage` nær `1` | PostgreSQLs varslingskø fylles fordi en lytter står fast; start replikaen som melder det på nytt. |

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

**To slags e-post, to veier.** Installasjonens egen e-post — `MAIL_DRIVER` og `SMTP_*` — er
**systempostboksen**: den skriver til tenantens **egne ansatte**, aldri til kundene.
Identitetens invitasjoner og passordgjenoppretting, Ansattes meldinger og meldingene
Kalender sender en kollega, går den veien, og ingenting annet. **Ingen kanal, ingen
dokumenter og ingen invitasjoner bruker den noen gang.**

E-post til **kunder** går gjennom en kanal en eier har valgt. Fakturaers dokumenter og
purrebrev, og Kalenders invitasjoner til folk utenfor bedriften, går hver ut fra en
Kommunikasjon-kanal som er navngitt i modulens egne innstillinger — **Utsending** i
fakturainnstillingene, **Invitasjoner til personer utenfor selskapet** i
kalenderinnstillingene —, eller ikke i det hele tatt mens innstillingen er **Avslått** eller
**Av**, som en installasjon starter med
([Customer mail from other modules](/en/reference/communications/#customer-mail-from-other-modules)).
Legitimasjonen til en kanal konfigureres gjennom Kommunikasjon-API-et, der vert, port og et
beskyttet passord lagres som postkasselegitimasjon — det finnes ingen miljøvariabel for
dem —, og en kanal uten den er **Utilgjengelig** og sender ingenting: den faller ikke
tilbake på `SMTP_*`. En kanal kan i stedet sende fra en delt postboks i Microsoft 365,
uten egen legitimasjon, når **E-postkanaler** i Microsoft Entra ID-modulen er satt opp
([E-postkanaler](/nb/admin/entra/#mail-channels)); det finnes ingen annen leverandør. Se
[Kommunikasjon](/en/reference/communications/#providers).

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
  [Kalender](/nb/admin/calendar/#postbokssynkroniseringen)). Den bærer også Google
  Kalenders push-varsler for synkroniseringen med Google Kalender,
  `POST /api/v1/google/calendar-notifications`, som proxyen også må slippe gjennom:
  Google poster dem over HTTPS til `PUBLIC_BASE_URL`, med et sertifikat Google stoler på,
  og den samme bryteren slår dem av
  ([Google Workspace](/nb/admin/google-workspace/#kalendersynkronisering),
  [Kalender](/nb/admin/calendar/#google-kalender)). Den bærer også Meta-modulens webhook
  for Facebook Messenger, `GET` og `POST /api/v1/meta/webhook`, som proxyen må slippe
  gjennom som to ruter: Metas håndtrykk og hendelsene dens, der signaturen sjekkes over
  kroppen nøyaktig slik Meta sendte den, så proxyen må ikke skrive om kroppen
  ([Meta](/nb/admin/meta/#rutene-på-den-offentlige-verten)). Den bærer også bildene til
  kortene Kommunikasjon sender på Messenger, `GET` og `HEAD`
  `/api/v1/communications/public-media/{token}`, som Meta henter fra internett med et token
  som gjelder i 7 dager; proxyen må slippe den gjennom, som nøyaktig ett stisegment
  ([Meta](/nb/admin/meta/#rutene-på-den-offentlige-verten)). Uten `PUBLIC_BASE_URL` avvises et
  svar med kort. Tokenet står i stien, så proxyens tilgangslogg bør føre den stien uten
  tokenet, eller ikke føre den i det hele tatt.
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
- Det finnes heller **ingen `GOOGLE_*`-variabel**. Google Workspace-modulens nøkler for
  tjenestekontoen og hemmeligheten til nettklienten (forseglet under `APP_SECRET`),
  administratoren som etterlignes, og nettklientens ID ligger i databasen, satt av en
  eier på modulens side; modulen når faste Google-verter som ingen innstilling endrer
  ([Google Workspace](/nb/admin/google-workspace/)).
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
  `AI_API_KEY` fra en hemmelighetsforvalter i stedet. KI-abonnementet (`AI_PROVIDER`,
  `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS`) er også konfigurasjon på installasjonsnivå, og en
  eier velger modellen til hver KI-oppgave under **KI-modeller**: se
  [KI](/nb/admin/configuration/#ki).
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
`invoices:issue` kan sende fra denne utgivelsen. Det gikk først ut gjennom den samme
`SMTP_*`-konfigurasjonen som identitetens e-post; det gjør det ikke lenger — et dokument
går ut fra en kanal en eier har valgt (utgivelsen der kundepost tar en kanal, under). En
retur går til kanalens adresse, ikke til selgerens Reply-To, og Vantigo registrerer ingen:
pek kanalen mot en postkasse noen leser hvis returer betyr noe.

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

**Utgivelsen med Assistenten.** Den har `00103_assistant_baseline.sql`, skjemaet
`assistant`: innstillingene (alle har assistenten, samtaler beholdes i 12 måneder, sensitive
data er ikke tilgjengelige for den), hver persons bryter, samtalene med meldinger, kjøringer og
handlingslogg, og eierens tillatelsesnivåer, alt tomt. En installasjon som lar `MODULES` stå
usatt, får modulen, og **Assistent**-flisen for alle, ved oppgradering; en som lister
`MODULES`, får den når `assistant` legges til. Den trenger ingen rettighet og ingen annen
modul, men har bare nytte av seg mens `AI_PROVIDER` er satt, med en modell som støtter
verktøykall ([Konfigurasjon](/nb/admin/configuration/#ki)); uten en tilbys assistenten ikke til
noen. En rolle med minste privilegium trenger tilgang til skjemaet `assistant`
([Databaseroller](#databaseroller)). Den starter to bakgrunnsjobber: `assistant-runs`, som
feiler en kjøring en omstart etterlot som pågående, og `assistant-retention`, som sletter
samtaler som er eldre enn oppbevaringstiden og samtalene til personer identity har slettet.
Å rulle migreringen tilbake dropper skjemaet med alle samtalene. Se
[Assistent](/nb/user/assistant/) og [referansen for Assistent](/en/reference/assistant/).

**Utgivelsen med Lager.** Den har `00108_inventory_baseline.sql`, skjemaet `inventory`
uten lagersteder, lokasjoner eller beholdning. En installasjon som lar `MODULES` stå usatt,
får modulen, og **Lager**-flisen for alle som får `inventory:locations-view` eller
`inventory:stock-view`, ved oppgradering; en som lister `MODULES`, får den når
`inventory` legges til ved siden av `products`, som den krever — `inventory` uten
`products` nekter å starte. De seks rettighetene er nye, og ingen egendefinert rolle har
dem før en eier legger dem til. En rolle med minste privilegium trenger tilgang til
skjemaet `inventory` ([Databaseroller](#databaseroller)). Den starter ingen
bakgrunnsjobb og trenger ingen innstilling; det er ikke noe mer å gjøre. Å rulle
migreringen tilbake dropper skjemaet med alle lagersteder, lokasjoner og bevegelser. Se
[Lager](/nb/user/inventory/).

**Utgivelsen med varetelling i Lager og pakninger i Produkter.** Den har to
migreringer. `00109_products_packagings.sql` oppretter `products.variant_packagings`,
tom: eskene, kartongene og pallene en variant lagres og telles i, redigert på
produktsiden. `00110_inventory_counts.sql` oppretter `inventory.counts` og
`inventory.count_lines`, tomme, og legger to kolonner som kan være null til
lagerreskontroen — `movement_lines.entered` og `movements.count_id` — som eksisterende
bevegelser lar stå tomme. Ingen beholdning og ingen bevegelse endres. Ingen rettighet er
ny — `inventory:counts-manage` fantes og tas nå i bruk — og det er ingen innstilling og
ingen bakgrunnsjobb; standardrettighetene i [Databaseroller](#databaseroller) dekker de
nye tabellene. Å rulle tilbake dropper de nye tabellene og kolonnene med alle pakninger,
varetellinger, tellingsbevegelsenes kobling til varetellingen og det som ble registrert i
pakninger; bevegelsene og beholdningen blir stående. Se
[Tell varer](/nb/user/inventory/#tell-varer) og [Pakninger](/nb/user/products/#pakninger).

**Utgivelsen med Google Workspace.** Den har `00114_google_baseline.sql`, skjemaet
`google` med sin ene tilkoblingsrad, ikke satt opp. En installasjon som lar `MODULES`
stå usatt, får modulen, og siden under **Integrasjoner** for eiere, ved oppgradering; en
som lister `MODULES`, får den når `google` legges til ved siden av `staff`, som den
krever — `google` uten `staff` nekter å starte. Rettigheten `google:manage` er ny, og
ingen egendefinert rolle har den før en eier legger den til. Ingenting når Google før en
eier laster opp en nøkkel og tester tilkoblingen. En rolle med minste privilegium
trenger tilgang til skjemaet `google` ([Databaseroller](#databaseroller)). Den starter
ingen bakgrunnsjobb. Å rulle migreringen tilbake dropper skjemaet med de forseglede
nøklene og hemmeligheten, og rører ingenting hos Google. Se
[Google Workspace](/nb/admin/google-workspace/).

**Utgivelsen med den personlige Google-tilkoblingen.** Den har
`00116_google_connections.sql`: folks egne Google-tilkoblinger, ingen ennå, og
**Tilkobling for personer**, av. Den starter `google-sweep`, som ikke har noe å gjøre før
en eier slår bryteren på og folk kobler til. Før du slår den på, registrer `APP_URL` +
`APP_BASE_PATH` + `/api/v1/google/connect/callback` på den interne nettklienten. Å rulle
migreringen tilbake dropper hver persons tilkobling med det forseglede tokenet, uten å
tilbakekalle det: Google lar et ubrukt token utløpe, og en person kan fjerne Vantigo i
Google-kontoen sin. Se [Google Workspace](/nb/admin/google-workspace/#tilkobling-for-personer).

**Utgivelsen med Ordre.** Den har `00119_orders_baseline.sql`, skjemaet `orders` uten
tilbud, uten ordrer og med standardinnstillinger. En installasjon som lar `MODULES` være
usatt, får modulen, **Ordre**-flisen for alle som får `orders:access` og fanen **Ordre** på
kundesiden, ved oppgradering; en som lister `MODULES`, får den når `orders` legges til ved
siden av `products`, `customers` og `invoices`, som den krever — `orders` uten én av dem
nekter å starte og navngir den. De ni rettighetene er nye og holdes ikke av noen egen
rolle før en eier legger dem til. En rolle med minste rettigheter må ha tilgang til
skjemaet `orders` ([Databaseroller](#databaseroller)). Den starter tre arbeidere —
`orders.mail`, `orders.retention` og `orders.quote_expiry` — og én hendelsesbehandler,
`orders.queue_confirmation`, og lagrer PDF-ene sine i objektlageret under `orders/`. Før
noen lager tilbud, koble avgiftskategoriene i Produkter til mva-koder i Fakturaer og velg
en avsenderkanal ([Ordre](/nb/admin/orders/)). Å rulle migreringen tilbake fjerner
skjemaet med hvert tilbud, hver ordre og hver mal; de lagrede PDF-ene blir liggende i
objektlageret.

**Utgivelsen med fakturering fra ordre.** Den har `00121_orders_allocations.sql`, tabellen
over hva fakturautkast og utstedte fakturaer holder av hver ordrelinje, tom, og
`00122_invoices_order_sources.sql`, som lar en fakturalinje vise til en ordrelinjes
allokering og gir en fakturakjøring en kilde — alle kjøringer laget før den, fakturerer
forbruk. Ingen rettighet og ingen innstilling er ny: knappen **Fakturer** på en ordre og
veiviseren **Fra ordre** i Fakturaer krever `invoices:access` og `invoices:create` i
Fakturaer. `00123_invoices_order_allocation_ledger.sql` legger til Fakturaers oversikt
over ordreantallene den har tatt, tom, og Fakturaer starter én arbeider til, alltid,
`invoices.allocation_reconcile`: hvert tiende minutt gir den tilbake til Ordre det
Fakturaer tok for over en time siden og som ingen fakturautkast holder —
sikkerhetsnettet for et utkast som ble slettet mens Ordre ikke kunne få beskjed, en
kundes utkast slettet ved en anonymisering, eller en veiviser eller fakturakjøring som
ble avbrutt underveis. Et fakturautkast som fortsatt finnes, beholder det det holder,
uansett hvor gammelt det er. Arbeideren tar radene sine i én setning, så et hvilket som
helst antall instanser kan kjøre den, trenger ingenting eget og sender ingenting; med
Ordre slått av gjør den ingenting. Å rulle `00123` tilbake fjerner oversikten, og det den
ville gitt tilbake, blir holdt til noen frigjør det på ordren; å rulle `00122` tilbake
avvises så lenge en fakturalinje viser til en ordrelinje eller en fakturakjøring
fakturerer ordre; å rulle `00121` tilbake fjerner det fakturaene holdt.

**Utgivelsen med frakt.** Den har `00127_orders_shipping.sql`: fraktkontoene,
standardkontoen per lagersted, sendingene med kolli, lagrede etiketter og fraktbrev og
sporingshendelser, alle tomme; tre innstillinger i Ordre — ikke noe fraktprodukt, ikke noe
påslag, og sendingsvarselet på — og e-posttypen for sendingsvarselet. Ordre starter én
arbeider til, alltid, `orders.shipment_tracking`: hvert 30. minutt spør den transportørene om
sendingene bestilt de siste 60 dagene, og tar hver ved å flytte tidspunktet den sist ble
sporet (`SKIP LOCKED`), så et hvilket som helst antall instanser deler arbeidet uten å spørre
to ganger; uten fraktkonto gjør den ingenting. API-et og arbeidercontaineren trenger utgående
https til `api.bring.com` (ingen sertifikatfesting: Bring bruker Let's Encrypt).
`SHIPPING_CARRIERS` (standard `bring`) holder tilbake en transportør. Nøklene forsegles med
`APP_SECRET`: endres den, kan de ikke leses, og hver kontos nøkler må legges inn på nytt. Å
rulle migreringen tilbake fjerner sendingene og kontoene med nøklene; etikettene blir liggende
i objektlageret under `orders/shipment/`. Se [Frakt](/nb/admin/shipping/).

**Utgivelsen med Kunnskap.** Den har `00135_knowledge_baseline.sql`, skjemaet `knowledge`
uten artikler. En installasjon som lar `MODULES` stå usatt, får modulen, med **Kunnskap**-flisen
for alle som får `knowledge:articles`, ved oppgradering; en som lister `MODULES`, får den når
`knowledge` legges til. Modulen krever ingen annen modul. De to rettighetene,
`knowledge:articles` og `knowledge:manage`, er nye og ligger hos ingen rolle før en Owner
legger dem til — ingen rolle har Kunnskap som standard. En rolle med færrest mulige
rettigheter trenger skjemaet `knowledge` ([Databaseroller](#databaseroller)). Den starter én
worker, `knowledge.retention`, som én gang i døgnet sletter artikler 30 dager etter at de ble
kastet, rydder bort gamle versjoner og feier bort slettede brukere, og den legger to
live-emner (`knowledge.article` og `knowledge.tree`) til
[sanntidsoppdateringene](#sanntidsoppdateringer-og-proxyen-din) en proxy må slippe gjennom.
Assistenten får Kunnskap som kilde. Å rulle migreringen tilbake fjerner skjemaet med alle
artikler og historikken deres. Se [Kunnskap](/nb/user/knowledge/).

**Utgivelsen med Produksjon.** Den har `00156_manufacturing_baseline.sql`, skjemaet
`manufacturing` uten arbeidsstasjoner og uten stykklister. En installasjon som lar
`MODULES` stå usatt, får modulen, med **Produksjon**-flisen for alle som får
`manufacturing:access`, ved oppgradering; en som lister `MODULES`, får den når
`manufacturing` legges til ved siden av `products` og `inventory`, som den krever —
`manufacturing` uten en av dem nekter å starte og navngir den som mangler. De fem
rettighetene er nye og ligger hos ingen egendefinert rolle før en Owner legger dem til. En
rolle med færrest mulige rettigheter trenger skjemaet `manufacturing`
([Databaseroller](#databaseroller)). Den starter ingen worker og trenger ingen innstilling.
Fra denne utgivelsen nekter Produkter også å slette en variant, eller å gjøre produktet
til en tjeneste, mens en aktiv stykkliste navngir den. Å rulle migreringen tilbake fjerner
skjemaet med alle arbeidsstasjoner og stykklister. Se
[Produksjon](/nb/admin/manufacturing/).

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

**Utgivelsen der kundepost tar en kanal.** Den har `00096_invoices_send_channel.sql`, én
tom kolonne i Fakturaers innstillinger, og `00097_calendar_settings.sql`, Kalenders første
innstilling for arbeidsområdet, én rad uten kanal, og én ny rettighet, `calendar:manage`
(«Administrere kalenderinnstillinger», sensitiv og delegerbar, som Owner har gjennom
jokertegnet). **Etter oppgraderingen sender Fakturaer og Kalender ingenting til kunder før
en eier velger en kanal**: dokumenter og purrebrev ble sendt fra `SMTP_FROM` før, og
invitasjoner også; nå starter **Utsending** i fakturainnstillingene og **Invitasjoner til
personer utenfor selskapet** i kalenderinnstillingene på **Avslått** og **Av**. Til da
finnes ikke knappen **Send**, en kunde som vil ha purringer på e-post får brev på papir, og
en deltaker utenfra lagres som **Ikke sendt**. En eier setter opp en kanal i Kommunikasjon
og velger den begge steder
([Fakturaer](/nb/user/invoices/#velge-hvor-dokumentene-sendes-fra),
[Kalender](/nb/user/calendar/#hvor-invitasjonene-sendes-fra)); et purrebrev som ikke kunne
gå i mellomtiden, prøves på nytt innen de 48 timene. En Kommunikasjon-**SMTP-kanal uten
lagret legitimasjon** sender ikke lenger på `SMTP_*` som reserveløsning: den er
**Utilgjengelig**, og en melding i kø på den feiler med en gang. Ingenting slettes av
oppgraderingen, `SMTP_*` er fortsatt påkrevd for systempostboksen, og å rulle migreringene
tilbake fjerner de to innstillingene (valgene, ikke noen e-post).

For en komplett driftsbok for sikkerhetskopi, én migrator, token-rotasjon og
Owner-nødtilgang, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).
