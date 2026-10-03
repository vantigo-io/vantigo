---
title: "Transportsikkerhet og herding av nettleseren"
description: "Operatørens valg av https, PostgreSQL-TLS eller Unix-socket og SMTP-TLS, HSTS, vertsfiltrering og innholdssikkerhetspolicyen."
sidebar:
  order: 30
sources:
  - apps/server/internal/server
  - apps/server/internal/security
  - apps/server/internal/netguard
---
Transport — http eller https i ytterkanten, TLS eller klartekst til PostgreSQL og til
SMTP-reléet — er operatørens valg, gjort per installasjon i miljøet. Prosessen
overprøver det ikke: ingenting ved transporten feiler konfigurasjonsvalideringen, i noe
miljø. Det den gjør, er å sende et sett med sikkerhetshoder for nettleseren på hvert
svar og utlede de få tingene som følger av valget (cookie-attributtet `Secure`, HSTS).
Dette dokumentet forklarer hva hver innstilling betyr, hva klartekstalternativene
koster, og hvordan du konfigurerer de vanlige oppsettene.

## Statusoversikt

| Kontroll | Development | Utenfor development |
| --- | --- | --- |
| `APP_URL`-skjema | http eller https | http eller https |
| PostgreSQL-tilkobling | hvilken som helst `sslmode`, TCP eller Unix-socket | hvilken som helst `sslmode`, TCP eller Unix-socket |
| Applikasjonens SMTP (`SMTP_*`) | `starttls`, `implicit` eller `none` | `starttls`, `implicit` eller `none` |
| Cookie-attributtet `Secure` | satt når `APP_URL` er https | satt når `APP_URL` er https |
| HSTS | sendes ikke | sendes på https-forespørsler, loopback unntatt |
| Sikkerhetshoder, inkludert CSP | sendes | sendes |
| Filtrering av Host-hode | `APP_URL`s vert + loopback | `APP_URL`s vert + loopback |
| HTTPS-omdirigering | ingen | ingen (bare HSTS-hode) |
| Sammenslått OpenAPI-dokument | `GET /api/openapi.json`, enhver autentisert økt | `GET /api/openapi.json`, enhver autentisert økt |

## Hva hvert valg koster

- **`APP_URL` på `http://`.** Øktcookies og bærerlenkene som sendes på e-post av
  invitasjons- og passordgjenopprettingsflytene utledes fra denne opprinnelsen, så en
  http-opprinnelse gir dem til hvem som helst på nettverksveien. Cookies settes uten
  `Secure`-attributtet (en `Secure`-cookie når aldri en http-opprinnelse, så
  attributtet følger skjemaet), og HSTS sendes aldri. Forsvarlig bare når veien mellom
  nettleser og prosess er en du kontrollerer fra ende til ende. Dette er også grunnen
  til at `Secure`-attributtet beregnes i stedet for å være hardkodet, og hvorfor
  kodeskanningens `go/cookie-secure-not-set` på `internal/identity/cookies.go` er en
  falsk positiv som skal avvises, ikke et funn som skal fikses: spørringen flagger en
  cookie-skriving dens taint-sporing ikke klarer å koble til noen `Secure`-verdi, så å
  hardkode `true` der fjerner den ikke — det er målt — mens det ville stenge hver
  rene http-installasjon ute fra innlogging. Attributtet er låst per skjema av
  `TestSessionCookieAttributes`.
- **En PostgreSQL-tilkobling uten sertifikatverifisering.** libpqs standard `sslmode`
  er `prefer`, som faller tilbake til klartekst og aldri sjekker et sertifikat selv
  over TLS; `require` krypterer, men autentiserer ikke tjeneren. Bare `verify-full` —
  eller `verify-ca` når tjenersertifikatet ikke navngir verten du kobler til —
  autentiserer den. Over et nettverk du ikke kontrollerer, går databaselegitimasjonen
  og hver rad for hver leietaker over denne tilkoblingen.
- **`SMTP_TLS=none`.** E-postlegitimasjon og meldingsinnhold, inkludert bærerlenkene
  ovenfor, går i klartekst til reléet. `starttls` (standard) er obligatorisk, ikke
  opportunistisk, så en tjener som ikke annonserer STARTTLS gir en feil i stedet for
  en klartekstlevering; `implicit` er TLS fra første byte.

Både `DATABASE_URL` og `MIGRATIONS_DATABASE_URL` tolkes fortsatt slik pgx vil tolke
dem, så en misformet tilkoblingsstreng feiler konfigurasjonen i stedet for den første
tilkoblingen. Feilen siterer aldri strengen: den kan inneholde et passord.

## Vanlige oppsett

En installasjon på tvers av et nettverk ser slik ut:

```text
APP_URL=https://vantigo.example.com
DATABASE_URL=postgresql://vantigo:<from-secret-store>@db.example.com:5432/vantigo?sslmode=verify-full
MAIL_DRIVER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_TLS=starttls
```

Når tjenersertifikatet er utstedt av en privat sertifikatutsteder, pek tilkoblingen
på den med `sslrootcert=/etc/ssl/certs/internal-ca.pem` og behold
`sslmode=verify-full`. Bruk `verify-ca` bare når sertifikatet ikke bærer vertsnavnet
du kobler til.

En installasjon der PostgreSQL kjører på samme vert har ingen sertifikatutsteder å
verifisere mot og ikke noe nettverk å beskytte. Enten si det på en TCP-tilkobling:

```text
DATABASE_URL=postgresql://vantigo:<password>@127.0.0.1:5432/vantigo?sslmode=disable
```

eller koble til over PostgreSQLs Unix-domenesocket, som aldri involverer TLS i det hele
tatt (pgx, som libpq, ignorerer hver `ssl*`-innstilling på en socket-vert). Verten er
katalogen som inneholder socketen, ikke socket-filen:

```text
DATABASE_URL=postgresql://vantigo:<password>@/vantigo?host=/var/run/postgresql
```

eller, i nøkkelordform, `host=/var/run/postgresql user=vantigo password=… dbname=vantigo`.
De samme formene virker for `MIGRATIONS_DATABASE_URL`. To ting å vite om sockets:

- **Fra containeren, monter socket-katalogen** — for eksempel
  `-v /var/run/postgresql:/var/run/postgresql` — og hold den monterte stien under
  108 byte, kjernens grense for en Unix-socket-sti. Imaget kjører som en
  uprivilegert bruker, så PostgreSQLs `peer`-autentisering (som tilordner OS-brukeren
  til en rolle) vil ikke treffe; gi rollen et passord og la `pg_hba.conf` bruke
  `scram-sha-256` for `local`-tilkoblinger i stedet.
- **Den medfølgende Compose-stacken.** PostgreSQL-containerens socket deles ikke med
  applikasjonscontainerne; de kobler til over det private compose-nettverket, der
  `sslmode=disable` er den ærlige innstillingen.

SMTP har en beskyttelse som ikke handler om TLS: destinasjonen slås opp og kontrolleres
før socketen åpnes, og private adresser, loopback, link-local, carrier-grade NAT og
sky-metadataadresser (inkludert `169.254.169.254`) avvises. Tilkoblingen gjøres så til
adressen som ble kontrollert, ikke til et nytt oppslag, og det er det som slår DNS
rebinding. Dette gjelder uansett hva `SMTP_TLS` sier.

## Oppgradering fra de lukkede reglene

Tidligere utgivelser nektet å starte utenfor development med mindre `APP_URL` var
https, databasetilkoblingen verifiserte tjenersertifikatet og `SMTP_TLS` ikke var
`none`, med `ALLOW_INSECURE_TRANSPORT=1` som én enkelt nødutgang som lempet på alle tre
samtidig. De reglene og den variabelen er borte; prosessen leser ikke lenger
`ALLOW_INSECURE_TRANSPORT` og ignorerer den dersom den fortsatt er satt. En
installasjon som hadde flagget, fortsetter å virke uendret. En installasjon som
oppfylte de gamle reglene, fortsetter også å virke uendret — `sslmode=verify-full` og
`SMTP_TLS=starttls` betyr nøyaktig det de betydde.

## HSTS

Utenfor development sendes `Strict-Transport-Security: max-age=2592000` (30 dager,
ingen `includeSubDomains`, ingen preload) på https-forespørsler til verter som ikke er
loopback. Hodet settes etter behandlingen av videresendte hoder, så skjemaet fra en
TLS-terminerende proxy er det avgjørelsen ser. Terminerende ingress bør likevel avvise
klartekst på egen hånd — hodet hjelper bare en nettleser som allerede har vært der én
gang.

## Filtrering av Host-hode

Tillatelseslisten utledes fra vertsnavnet i `APP_URL` pluss `localhost`, `127.0.0.1`
og `::1`; en forespørsel som bærer en annen `Host` besvares med et 400-problem før den
når applikasjonen. Porten ignoreres i sammenligningen.

Loopback står på den listen med vilje. Containerens helsesjekk kjører
`vantigo healthcheck`, som prober `http://127.0.0.1:$PORT/health/ready` i prosessen
med den bokstavelige adressen i `Host`-hodet, og vertsfiltreringen kjører før
rutingen, så den kan ikke unnta den stien ved navn. Et 400-svar der ville merke
containeren som permanent usunn.

Dette er også grunnen til at container- og orkestratorprober må bruke exec-formen
(`["/app/vantigo", "healthcheck"]`): en HTTP-probe som kobler til utenfra sender sin
egen adresse som `Host`, som filteret avviser.

Ingenting i pipelinen omdirigerer ren HTTP. Det finnes ingen mellomvare for
HTTPS-omdirigering, og HSTS legger bare til et svarhode — og bare når forespørselen
allerede er https — så en probe som behandler alt annet enn 2xx som en feil, får aldri
en 307. Terminer TLS og avvis klartekst i ingressen i stedet.

## Sikkerhetshoder for nettleseren

Hvert svar — SPA-dokument så vel som API — bærer:

| Hode | Verdi |
| --- | --- |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `no-referrer` |
| `Permissions-Policy` | kamera, mikrofon, geolokasjon og andre ubrukte funksjoner nektes |
| `Content-Security-Policy` | se nedenfor |

Policyen er:

```text
default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none';
form-action 'self'; script-src 'self' 'sha256-…'; style-src 'self' 'unsafe-inline';
img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self';
frame-src 'self'; worker-src 'self'; manifest-src 'self'
```

To direktiver fortjener en forklaring.

- **`script-src` bærer en hash, ikke `'unsafe-inline'`.** SPA-ens inngangsdokument
  males ved oppstart med ett inline-skript, kjøretidskonfigurasjonen
  `window.__VANTIGO_APP__`. Dokumentet og hashen produseres fra samme streng, så
  policyen kan ikke drive bort fra skriptet den tillater. Alt annet i den publiserte
  Vite-pakken er en ekstern modul.
- **`style-src` beholder `'unsafe-inline'`.** Mantine rendrer tema-CSS-variablene og
  komponentstilene sine som inline `<style>`-elementer, og React-stilprops blir
  style-attributter.

`img-src` tillater `https:` slik at en konfigurert `APP_LOGO_URL` og eksterne bilder
inne i den sandkasseavgrensede HTML-e-postforhåndsvisningen fortsatt lastes.

Dersom en tilpasset frontend trenger en videre policy, sett `CSP_REPORT_ONLY=1` for å
sende `Content-Security-Policy-Report-Only` mens forskjellen avklares. Det slår av
beskyttelsen, så det er en diagnostisk innstilling, ikke et sted å bli værende.

### Dokumentasjonen under `/docs/`

Den innebygde dokumentasjonssiden (se [Installasjon](/nb/admin/installation/#den-innebygde-dokumentasjonen))
er et statisk bygg der sidene bærer Starlights inline tema- og navigasjonsskript og
søket kjører Pagefinds WebAssembly, og ingen av delene tillates av den hash-baserte
`script-src` ovenfor. Svar under `/docs/` erstatter derfor applikasjonens policy med
sidens egen: `script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'`,
`style-src 'self' 'unsafe-inline'`, `img-src 'self' data:`, og de samme
`default-src 'self'`, `frame-ancestors 'none'`, `object-src 'none'` og
`form-action 'self'`. Sidene er et byggeartefakt uten brukerlevert innhold, så
inline-skriptene er sidens egne. Navnet på hodet følger `CSP_REPORT_ONLY` som
applikasjonens. Alle andre hoder i denne delen er uendret på de svarene.

## API-kontraktdokumentet

Den kjørende tjeneren serverer den sammenslåtte kontrakten for sine **aktiverte**
moduler på:

```text
GET /api/openapi.json
```

Det krever en økt i alle miljøer, development inkludert — det er ikke miljøstyrt, og
det finnes ingen Swagger, Scalar eller annet dokumentasjonsgrensesnitt i imaget.
Regelen er `session`, som **enhver autentisert bruker** oppfyller: ingen rettighet,
policy eller rolle kontrolleres. Dokumentet er derfor beskyttet mot anonyme besøkende,
ikke begrenset til administratorer. Det finnes ingen `/openapi/v1.json`-rute.
Kildekontraktene per modul ligger i `openapi/*.yaml` i repositoriet og er riktig
inndata for klientgenerering; `bun run gen:client` regenererer den typede
frontend-klienten fra dem.
