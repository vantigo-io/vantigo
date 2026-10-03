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

Modulene Kunder, Produkter, Energi, Kommunikasjon, Prosjekter, Tid, Utgifter og
Fakturaer er aktivert som standard (`MODULES` i `vantigo.env`). De deler én
PostgreSQL-database ved navn `vantigo`, med uavhengige skjemaer `identity`,
`customers`, `products`, `energy`, `communications`, `projects`, `time`, `expenses` og
`invoices`, hver med sin egen migreringshistorikk.

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

`seed` finnes også, men er bare for utvikling (`APP_ENV=development`; den avslutter
med 2 utenfor det). Ingen kommando, eller en ukjent en, skriver bruksanvisning til
stderr og avslutter med 2, så en skrivefeil i en jobbdefinisjon feiler høylytt i
stedet for stille å bli en webserver.

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
[Identitet, autentisering og utrulling](/nb/admin/authentication/). For OIDC for
arbeidsstyrken konfigurert ved oppstart, statisk SCIM-provisjonering og
gjenopprettingsprosedyrer, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).

### Innstillingene du må ta stilling til

- **`APP_SECRET`** (`vantigo.env`) — påkrevd, minst 32 byte. Hver krypteringsnøkkel
  prosessen bruker (CSRF-tokener, cookie-signering, AES-256-GCM-nøkkelen for
  TOTP-hemmeligheter) utledes fra den via HKDF-SHA256, én utledet nøkkel per formål.
  Generer den én gang med `openssl rand -base64 32`, lagre den i en
  hemmelighetsforvalter, og roter den aldri uten en migreringsplan: en rotasjon
  ugyldiggjør hver åpen sesjon og hver TOTP-hemmelighet kryptert under den gamle
  verdien.
- **`APP_URL`** (`vantigo.env`) — bare det offentlige opphavet: skjema og vert, med
  port om nødvendig, ingen sti og ingen avsluttende skråstrek. E-postlenker, de
  godtatte `Host`-verdiene, cookie-attributtet `Secure` og den statiske
  OIDC-tilbakekallingen utledes alle fra den. Forespørsler med en annen `Host` enn
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
  `communications`, `projects`, `time`, `expenses` og `invoices`. `energy`,
  `communications`, `projects` og `invoices` krever hver `customers`, og `time` krever
  `projects`; `projects` bruker også `products` når den er aktivert og svarer 409 på
  endepunktene for faktureringslinjer når den ikke er det; `expenses` krever ingenting
  og leser `projects` bare når den tilfeldigvis er aktivert. For en mindre
  installasjon lister du bare det du vil ha, for eksempel
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
(juster skjemalistene så de stemmer med `MODULES` som er aktivert i din installasjon,
og legg til `invoices` når den er aktivert):

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
GRANT USAGE ON SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, customers, products, energy, communications, projects, time, expenses TO "vantigo_app";
```

## Bakgrunnsjobber og skalering

Standardoppsettet med én container kjører bakgrunnsjobbene til hver aktivert modul
(utboks-levering, oppbevaring, opprydding av vedlegg, kundemodulens registerjobber)
inne i API-prosessen: `WORKERS_IN_PROCESS=1`, standardverdien. Utrullinger som
skalerer API-et horisontalt bør flytte dem til én dedikert worker-container, slik at
hver ekstra HTTP-replika ikke mangedobler pollerne:

1. Sett `WORKERS_IN_PROCESS=0` i `vantigo.env`. Det gjelder API-replikaene.
2. Kjør én ekstra container fra det samme imaget med kommandoen `worker`. Den kjører
   alltid bakgrunnsjobbene uansett `WORKERS_IN_PROCESS`, og serverer bare
   `/health/live` og `/health/ready` for prober.

Replikaer som verken skal migrere eller kjøre bakgrunnsjobber kan kjøre kommandoen
`server` i stedet for `api`; den serverer bare, uansett hva `WORKERS_IN_PROCESS` sier.
Uansett topologi tar oppbevaringsoppryddingen og registerjobbene en PostgreSQL
advisory lock, så hver av dem kjører på nøyaktig én instans per syklus.

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
API-prefiksene forblir `/api/v1/identity`, `/api/v1/customers`, `/api/v1/products`,
`/api/v1/energy`, `/api/v1/communications`, `/api/v1/projects`, `/api/v1/time`,
`/api/v1/expenses` og `/api/v1/invoices`.

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
- `vantigo.env.example` dokumenterer de statiske OIDC- og SCIM-innstillingene.
  Konfigurasjonen er bundet til utrullingen, og endringer krever omstart. Ikke legg
  leverandør- eller SCIM-hemmeligheter i filer under versjonskontroll — injiser
  `OIDC_CLIENT_SECRET`, `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN` og
  `COMMUNICATIONS_AI_API_KEY` fra en hemmelighetsforvalter i stedet.
- SCIM bruker `/api/v1/identity/scim/v2`, bearer-tokener og `application/scim+json`.
  Sett `SCIM_TOKEN`; under rotasjon kan du valgfritt sette `SCIM_PREVIOUS_TOKEN`
  sammen med `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` (fram i tid, og ikke mer enn 24 timer
  etter oppstart). Se [SSO- og SCIM-drift](/nb/admin/sso-scim/).
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

For en komplett driftsbok for sikkerhetskopi, én migrator, token-rotasjon og
Owner-nødtilgang, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).
