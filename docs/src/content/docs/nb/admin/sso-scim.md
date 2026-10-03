---
title: "SSO- og SCIM-drift"
description: "Statisk OIDC- og SCIM-konfigurasjon i produksjon, rotasjon, migrering, livssyklus og gjenoppretting."
sidebar:
  order: 21
sources:
  - apps/server/internal/identity
---
Dette er produksjonsveiledningen for Vantigos statiske identitetsintegrasjoner. Hver
installasjon har **høyst én** OpenID Connect-leverandør for arbeidsstyrken og én
SCIM-legitimasjon bundet til installasjonen. Begge leses fra miljøet når prosessen
starter; å endre en variabel, en montert fil eller en oppføring i hemmelighetslageret
har ingen effekt før applikasjonen startes på nytt.

Det finnes ikke noe dynamisk API for SSO- eller SCIM-konfigurasjon, og ikke noe
administrasjonsgrensesnitt for Eier for å legge til leverandører, redigere
leverandørmetadata, utstede SCIM-tokener eller endre SCIM-omfang. Bruk
installasjonskonfigurasjonen og utgivelsesprosedyren nedenfor i stedet.

Vantigo er en applikasjon for én leietaker. Det finnes ikke noe kontrollplan for
leietakere, ingen SSO- eller provisjoneringsinnstilling per leietaker, og ingenting å
avgrense en leverandør til: OIDC-leverandøren for arbeidsstyrken og SCIM-legitimasjonen
gjelder hele installasjonen.

## Konfigurasjon

Konfigurasjonen kommer **utelukkende fra miljøvariabler**. Det finnes ingen
`appsettings.json`, ingen søkesti for konfigurasjonsfiler og ingen variabel for
miljønavn; `APP_ENV` (`production` som standard, eller `development`) er den eneste
miljøbryteren, og bare development lemper på noe som helst.

Hver innstilling valideres i én omgang ved oppstart, og **alle** problemer rapporteres
samtidig, så en feilkonfigurert container feiler ved første oppstart med den komplette
listen. Feltkommentarene i
[`apps/server/internal/config/config.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go)
er den autoritative referansen.

```bash
docker run --read-only \
  --env-file /secure/vantigo/vantigo.env \
  ghcr.io/vantigo-io/vantigo:<pinned-release> api
```

Foretrekk en hemmelighetstjeneste på plattformen eller en injisert miljøhemmelighet for
hver legitimasjon. Hemmeligheter hører ikke hjemme i imaget, i Compose-YAML, i Git eller
i en miljødump som havner i loggen.

### Miljøeksempel

```dotenv
APP_URL=https://vantigo.example.com

OIDC_PROVIDER=entra
OIDC_AUTHORITY=https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0
OIDC_CLIENT_ID=11111111-1111-1111-1111-111111111111
OIDC_CLIENT_SECRET=<inject-from-secret-store>
OIDC_DISPLAY_NAME=Workforce SSO

SCIM_TOKEN=<inject-from-secret-store>
```

`OIDC_PROVIDER` og `SCIM_TOKEN` er av/på-bryterne: er de ikke satt, er integrasjonen
slått av. **Når `OIDC_PROVIDER` ikke er satt, feiler oppstarten dersom noen annen
`OIDC_*`-variabel fortsatt er satt**, og restene navngis — en halvveis fjernet
leverandør kan ikke se avslått ut mens den fortsatt bærer gyldig legitimasjon.
Tilbakekallsstien er fast og er ikke en innstilling.

## OIDC for arbeidsstyrken

OIDC bruker autorisasjonskodeflyten med PKCE og nonce, en forseglet state-cookie og en
fast lokal fullføringssti. Leverandørens tilgangs- og ID-tokener lagres aldri.
Leverandøridentiteten er bare en korrelasjon ved innlogging: den gir ikke lokale roller,
beviser ikke lokal MFA og omgår ikke den lokale Eier/MFA-policyen. Nye identiteter
provisjoneres fortløpende som vanlige lokale `User`-kontoer nøklet på den validerte
utstederen og `sub` (skiller mellom store og små bokstaver); en e-postkollisjon avvises
i stedet for å kobles automatisk.

Det offentlige tilbakekallet er:

```text
https://<public-host><base-path>/api/v1/identity/oidc/callback
```

Registrer nøyaktig denne HTTPS-URL-en hos leverandøren. Med standardverdien tom
`APP_BASE_PATH` er den `/api/v1/identity/oidc/callback`. Nettleseren starter på
`/api/v1/identity/oidc/challenge`, og lokal fullføring er
`/api/v1/identity/oidc/complete`; ingen av dem er en tilbakekalls-URI for
leverandøren. Hver feil omdirigerer til `/sign-in?error=<code>`, og ingen
omdirigeringsmål kommer noen gang fra forespørselen.

### Klientautentiseringsmodusen utledes

Det finnes ingen `ClientAuthentication`-innstilling. Vantigo avgjør ut fra **hvilken
legitimasjon som er til stede**:

| Legitimasjon satt | Modus |
| --- | --- |
| `OIDC_CLIENT_SECRET` | Klienthemmelighet |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`, eller `AZURE_FEDERATED_TOKEN_FILE` | Workload identity (bare Entra) |
| Begge | **Oppstarten feiler** — de utelukker hverandre |
| Ingen av dem | **Oppstarten feiler** — én er påkrevd |

### Microsoft Entra ID: klienthemmelighet

1. Opprett eller velg én Entra-appregistrering for denne installasjonen. Bruk en
   konfidensiell webapplikasjon, opprett en klienthemmelighet og lagre den i
   installasjonens hemmelighetstjeneste.
2. Bruk den leietakerspesifikke autoriteten nøyaktig i denne formen, inkludert `/v2.0`:

   ```text
   https://login.microsoftonline.com/<tenant-guid>/v2.0
   ```

   `<tenant-guid>` må være leietakerens GUID. Autoriteter som bruker `common`,
   `organizations`, `consumers`, en annen vert, en spørrestreng eller en annen sti
   passerer ikke oppstartsvalideringen.
3. Sett `OIDC_PROVIDER=entra`, applikasjons-ID-en (klient-ID-en) som `OIDC_CLIENT_ID`
   (den må være en GUID), og `OIDC_CLIENT_SECRET`.
4. Registrer det faste tilbakekallet, inkludert den offentlige basisstien dersom en slik
   brukes.
5. Gi bare de delegerte omfangene innloggingsflyten trenger (`openid`, `profile`,
   `email`). Behandle ikke leverandørens gruppe- eller rollekrav som
   autorisasjonsgrunnlag i Vantigo. Sett ikke `OIDC_ALLOWED_DOMAINS` for Entra — den
   gjelder bare Google og avvises her.

Etter de innebygde tokenkontrollene krever Vantigo en `tid`-GUID som samsvarer med
leietakeren i den konfigurerte autoriteten, og en `oid`-GUID. Dersom det validerte
tokenet bærer flere `aud`-krav, må `azp` være lik den konfigurerte klient-ID-en.
Leverandøridentiteter forblir lokale `User`-kontoer og blir aldri Eiere gjennom krav.

### Microsoft Entra ID: workload identity

Workload identity erstatter **klienthemmeligheten som brukes når autorisasjonskoden
innløses**. Den erstatter ikke sluttbrukerens innlogging, gjør ikke OIDC-krav til
betrodd lokal autorisasjon, og erstatter ikke det uavhengige SCIM-bærertokenet.

Plattformen må levere alt det følgende før Vantigo starter:

- Et Kubernetes-cluster med en OIDC-utsteder og Azure Workload Identity aktivert, eller
  den tilsvarende Azure-hostede integrasjonen.
- Konfigurasjonen for den muterende webhooken/sidecaren som projiserer et
  tjenestekontotoken inn i podden og setter `AZURE_FEDERATED_TOKEN_FILE` (eller en
  eksplisitt konfigurert absolutt `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`). Stien må være
  lesbar for Vantigo-prosessen — **oppstarten kontrollerer at det er en absolutt sti
  og en lesbar fil**, og feiler ellers.
- En Entra-appregistrering hvis klient-ID brukes som `OIDC_CLIENT_ID`.
- En føderert identitetslegitimasjon på den registreringen med:
  - **Issuer**: nøyaktig clusterets OIDC-utsteder-URL;
  - **Subject**: nøyaktig arbeidslastens subject, normalt
    `system:serviceaccount:<namespace>:<service-account>`; og
  - **Audience**: `api://AzureADTokenExchange`.

Azure sammenligner issuer, subject og audience nøyaktig. Kopier ikke et annet navnerom,
tjenestekontonavn, en etterfølgende sti eller et audience fra en annen arbeidslast.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vantigo
  namespace: production
  annotations:
    azure.workload.identity/client-id: <entra-application-client-guid>
    # Valgfritt når clusterets leietaker ikke er appens leietaker:
    # azure.workload.identity/tenant-id: <tenant-guid>
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vantigo
  namespace: production
spec:
  template:
    metadata:
      labels:
        azure.workload.identity/use: "true"
    spec:
      serviceAccountName: vantigo
      containers:
        - name: vantigo
          image: ghcr.io/vantigo-io/vantigo:<pinned-release>
```

Konfigurer:

```dotenv
OIDC_PROVIDER=entra
OIDC_AUTHORITY=https://login.microsoftonline.com/<tenant-guid>/v2.0
OIDC_CLIENT_ID=<entra-application-client-guid>
# Valgfritt når plattformen ikke setter AZURE_FEDERATED_TOKEN_FILE:
# OIDC_WORKLOAD_IDENTITY_TOKEN_FILE=/var/run/secrets/azure/tokens/azure-identity-token
```

Sett ikke `OIDC_CLIENT_SECRET` i denne modusen; kombinasjonen gjør at oppstarten
feiler. Påstanden leses **på nytt fra filen ved hver innløsning av en
autorisasjonskode** og sendes med
`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
Roter det projiserte tokenet gjennom plattformen; Vantigo mellomlagrer det ikke.

### Google Workspace

1. Opprett en Google OAuth-webklient og oppbevar hemmeligheten i installasjonens
   hemmelighetstjeneste. Google støtter bare klienthemmelighet — workload identity
   godtas ikke.
2. Bruk nøyaktig utstederen `https://accounts.google.com`.
3. Bruk en klient-ID som slutter på `.apps.googleusercontent.com`, og registrer det
   faste tilbakekallet.
4. List opp hvert tillatte Workspace-domene i `OIDC_ALLOWED_DOMAINS` som en
   kommaseparert liste av rene DNS-navn. **Minst ett er påkrevd for Google.** Verdiene
   gjøres om til små bokstaver og dedupliseres; en URL, en adresse, en port, blanktegn
   eller en enkelt etikett avvises.

```dotenv
OIDC_PROVIDER=google
OIDC_AUTHORITY=https://accounts.google.com
OIDC_CLIENT_ID=<client-id>.apps.googleusercontent.com
OIDC_CLIENT_SECRET=<inject-from-secret-store>
OIDC_ALLOWED_DOMAINS=example.com,example.org
```

Vantigo krever `email_verified`, en syntaktisk gyldig e-postadresse og et ikke-tomt
`hd`-krav som samsvarer med e-postdomenet og finnes i `OIDC_ALLOWED_DOMAINS`.
Personlige Gmail-kontoer og identiteter som er uverifiserte eller har et domene som
ikke samsvarer, avvises.

## SCIM

Aktiver SCIM ved å sette `SCIM_TOKEN`. Det faste protokollendepunktet er:

```text
/api/v1/identity/scim/v2
```

Forespørsler autentiseres med `Authorization: Bearer <token>` og bruker
`application/scim+json`. De støttede ressursene er `Users` og `Groups`, pluss
standardendepunktene for oppdagelse `ServiceProviderConfig`, `ResourceTypes` og
`Schemas`. Tokenet kan ikke inneholde blanktegn, og det sammenlignes via et sammendrag
i stedet for direkte.

**Det finnes ingen filbasert tokenvariant.** `SCIM_TOKEN` og `SCIM_PREVIOUS_TOKEN` er
de eneste inndataene — det finnes ingen `BearerTokenFile` eller
`PreviousBearerTokenFile`. Injiser verdien fra en hemmelighetstjeneste.

SCIM-tilstanden er persistent selv om legitimasjonen er statisk: brukertilordninger,
SCIM-grupper og -medlemskap, livssyklustilstand, ETag-er og revisjonsposter ligger i
PostgreSQL. Brukere opprettet av SCIM er uprivilegerte, og **Eier-kontoer er beskyttet
mot SCIM-endringer** — en oppdatering, patch eller sletting rettet mot en Eier avvises,
noe som holder nødtilgangskontoen utenfor provisjoneringssystemets rekkevidde.

### Tokenrotasjon med overlapp

Overlappet med forrige token er valgfritt og begrenset til 24 timer fra oppstart:

1. Generer et nytt token med høy entropi og lagre det i hemmelighetstjenesten.
2. Sett den nye verdien som `SCIM_TOKEN`, behold den gamle som `SCIM_PREVIOUS_TOKEN`,
   sett `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` til et RFC 3339-tidsstempel i fremtiden og
   høyst 24 timer frem, og start på nytt. Begge tokener godtas frem til fristen.
3. Bytt provisjoneringsklientens legitimasjon til det nye tokenet. Verifiser med en
   oppdagelsesforespørsel eller en harmløs leseforespørsel, og sjekk Eier-statusendepunktet.
4. Når alle klienter har byttet, fjern **både** `SCIM_PREVIOUS_TOKEN` og utløpet,
   og start på nytt igjen.

Reglene håndheves ved oppstart, og hver av dem er en oppstartsfeil i stedet for en
stilltiende godtatt rotasjon:

- `SCIM_PREVIOUS_TOKEN` må være forskjellig fra `SCIM_TOKEN`.
- `SCIM_PREVIOUS_TOKEN` krever `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`, og utløpet krever
  tokenet — ingen av dem er gyldig alene.
- Utløpet må ligge i fremtiden **ved hver oppstart** og høyst 24 timer frem. En omstart
  etter at vinduet har passert, feiler derfor konfigurasjonen: fjern begge variablene
  når overlappet er over.

### Status og driftsbevis

En autentisert Eier kan lese:

```text
GET /api/v1/identity/owner/system-status
```

Svaret inneholder:

- `total`, `active`, `disabled` — brukertellinger
- `pendingInvitations`
- `staticOidcEnabled`, `staticOidcProvider`
- `staticScimEnabled`
- `lastStaticOidcSignInAtUtc`, `lastAuthenticatedScimRequestAtUtc`

De to tidsstemplene er driftsprojeksjoner, ikke autentiseringskritisk tilstand: en feil
ved skriving av telemetri eller til databasen må ikke gjøre en vellykket OIDC- eller
SCIM-operasjon til en mislykket en. Endepunktet returnerer aldri bærertokener,
klienthemmeligheter eller navn på hemmelighetsreferanser. Behandle null-tidsstempler
som «ingen vellykket bruk er registrert ennå».

## Lokal Eier for nødtilgang og MFA

Behold minst én lokal Eier-konto som nødtilgangsvei selv når OIDC er aktivert. Opprett
den første Eieren via `/setup` på den ferske installasjonen, før noen andre kan nå den;
den kontoen er også SystemAdmin. Lokal passordinnlogging, passnøkler og lokal MFA er
uavhengige av den eksterne leverandøren.

Dersom installasjonen er tilgjengelig før en operatør rekker å fullføre `/setup`, sett
`BOOTSTRAP_OWNER_EMAIL` i stedet: den samme nødtilgangs-Eieren settes da inn via en
e-postinvitasjon utstedt ved oppstart, i stedet for av den som når `/setup` først. Se
[management-lytteren](/nb/admin/management-listener/#sette-inn-den-første-eieren-uten-setup).

Med `OWNERS_REQUIRE_MFA=1` (standard utenfor development) krever Eier- og
SystemAdmin-operasjoner en andre faktor — en TOTP-kode, en gjenopprettingskode eller en
passnøkkel. Oppbevar gjenopprettingskoder frakoblet i organisasjonens
nødtilgangsprosess. OIDC-krav oppfyller aldri dette kravet og gir aldri lokale roller.

## Utgivelses- og oppgraderingsprosedyre

Migreringer er rene SQL-filer innebygd i binærfilen og kjøres i rekkefølge under en
PostgreSQL advisory lock. De går **bare fremover**: planlegg ikke å rulle
applikasjonsbinærfilen tilbake over en skjemaendring.

1. Lås imaget til en eksakt utgivelse, les utgivelsesnotatene og ta en testet
   PostgreSQL-sikkerhetskopi — for eksempel `pg_dump --format=custom`, verifisert med
   `pg_restore --list`. Test gjenoppretting et trygt sted, oppbevar kopien utenfor
   databasevolumet, og hold passordet ute av skallhistorikken.
2. Kjør en avsluttende `migrate`-jobb med det nye imaget og samme databasekonfigurasjon,
   med `MIGRATIONS_DATABASE_URL` (eierrollen) der installasjonen skiller den fra
   kjøretidsrollen.
3. Vent til den jobben avslutter vellykket før du starter applikasjonen. Migratorer
   serialiseres på advisory-låsen, så en utilsiktet samtidig migrator venter i stedet
   for å ødelegge skjemaet — men foretrekk likevel nøyaktig én jobb.
4. Start `api` (eller `server`-replikaer). **`api` kjører ventende migreringer selv før
   den begynner å betjene forespørsler**, så migreringsjobben handler om å se en feil
   *før* noen betjenende replika starter, ikke om at `api` lar skjemaet ligge etter.
   `server`-modus migrerer aldri.
5. Verifiser lokal Eier-innlogging, det faste OIDC-tilbakekallet (hvis aktivert),
   SCIM-endepunktet (hvis aktivert) og Eierens systemstatussvar. Oppbevar
   sikkerhetskopien og migreringsloggene i henhold til utgivelsens oppbevaringspolicy.

Kjør ikke `seed`-kommandoen, som bare er for development, i produksjon; den avslutter
med kode 2 utenfor development.

For Compose er `vantigo-migrate` engangstjenesten for migrering, og `vantigo` er den
langtkjørende `api`-tjenesten. `docker compose up -d` respekterer den avhengigheten; for
en kontrollert utgivelse, kjør `docker compose up vantigo-migrate`, bekreft at den
avslutter vellykket, og deretter `docker compose up -d vantigo` som beskrevet i
[Compose-veiledningen](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/README.md).

## Sjekkliste for hemmelighetshygiene

- Sjekk aldri inn klienthemmeligheter, SCIM-tokener, `APP_SECRET`, tokenfiler eller
  reelle leietakeridentifikatorer.
- Legg ikke hemmeligheter i `vantigo.env.example`, Compose-YAML, container-images,
  logger, saker eller skallhistorikk. De innsjekkede eksemplene bruker bare
  plassholdere.
- Foretrekk plattformens hemmelighetstjeneste eller en injisert miljøhemmelighet.
  Begrens fileierskap og -rettigheter, og gi migreringsjobben bare de hemmelighetene
  den trenger.
- Roter OIDC-klienthemmeligheter og SCIM-tokener gjennom leverandøren eller
  hemmelighetstjenesten, og start deretter på nytt. Det tidsbegrensede overlappet med
  forrige token finnes bare for SCIM.
- `APP_SECRET` kan ikke roteres på stedet: den utleder nøklene for økter, cookies, TOTP
  og kanallegitimasjon i Kommunikasjon, så å endre den ugyldiggjør alle sammen.
- Dersom en hemmelighet er eksponert, tilbakekall den umiddelbart, erstatt den, start
  alle replikaer på nytt, og undersøk logger og statusendepunktet for uventet bruk.
