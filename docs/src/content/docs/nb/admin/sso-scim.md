---
title: "SSO- og SCIM-drift"
description: "Kortet Single sign-on: OIDC-leverandøren for arbeidsstyrken og SCIM-tokenet, per leverandør, rotasjon, importen fra miljøet, livssyklus og gjenoppretting."
sidebar:
  order: 21
sources:
  - apps/server/internal/identity
  - apps/server/internal/db/migrations/00058_identity_sso_settings.sql
  - apps/server/internal/db/migrations/00059_identity_expected_accounts.sql
---
Dette er produksjonsveiledningen for single sign-on for arbeidsstyrken og
SCIM-provisjonering i Vantigo. Hver installasjon har **høyst én** OpenID
Connect-leverandør for arbeidsstyrken og én SCIM-legitimasjon. Begge ligger i én rad i
databasen, `identity.sso_settings`, som en Eier redigerer på kortet **Single sign-on**
under administrasjon av arbeidsområdet. Hver prosess — hver API-replika og hver
worker — leser raden gjennom en mellomlagring på 20 sekunder, så en lagring virker
overalt innen 20 sekunder, uten omstart og uten at noe i installasjonen må endres.

Vantigo er en applikasjon for én leietaker. Det finnes ikke noe kontrollplan for
leietakere, ingen SSO- eller provisjoneringsinnstilling per leietaker, og ingenting å
avgrense en leverandør til: OIDC-leverandøren for arbeidsstyrken og SCIM-legitimasjonen
gjelder hele installasjonen.

## Forutsetninger

- En **Eier**-konto, innlogget med en andre faktor så lenge `OWNERS_REQUIRE_MFA` er på
  (standard utenfor development). Kortet og hver forespørsel bak det er bare Eierens;
  den som har fått delegert rolleadministrasjon, ser det ikke.
- `APP_URL` (og `APP_BASE_PATH`, når en slik brukes) satt til den offentlige
  opprinnelsen leverandøren omdirigerer til og katalogen kaller. Tilbakekalls-URL-en og
  SCIM-endepunktet kortet viser, utledes av dem.
- For workload identity i Entra: at plattformen projiserer tokenfilen inn i hver
  replika som betjener API-et, på samme sti, i katalogen
  `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` navngir — som standard webhookens,
  `/var/run/secrets/azure/tokens` (se
  [workload identity](#microsoft-entra-id-workload-identity)).

## Kortet Single sign-on

Åpne avatar-menyen, velg **Administrer arbeidsområde**, og bli på **Oversikt**: kortet
**Single sign-on** går i full bredde under oversiktens andre kort, kortet
**Arbeidsområde** blant dem. Det inneholder:

- **Leverandør** — **Av**, **Microsoft Entra ID** eller **Google Workspace**.
- **Autoritet** — fylt inn som `https://accounts.google.com` når Google velges —
  **Klient-ID** og legitimasjonen: for Entra et valg under **Legitimasjon** mellom
  **Klienthemmelighet** og **Tokenfil**; for Google alltid **Klienthemmelighet**.
  Hemmelighetsfeltet er tomt ved hvert besøk. Når en hemmelighet er lagret, står det
  «Satt _dato_. La feltet stå tomt for å beholde den.» så lenge leverandøren og
  **Klient-ID** er de den ble lagret for; ellers «Forsegles når den lagres, og vises
  aldri igjen.», og en hemmelighet eller en tokenfil må oppgis.
- **Domener** (bare Google) og **Visningsnavn** for innloggingsknappen, `Workforce SSO`
  når det står tomt.
- **Tilbakekalls-URL** med en kopieringsknapp, vist før noe er satt.
- **Lagre**, **Slå av** for leverandøren, og **Siste innlogging**.
- SCIM-delen: **Endepunkt-URL** å kopiere inn i katalogen, **Token utstedt**,
  **Nytt token**, **Avslutt overlapp** mens en rotasjon overlapper, **Slå av**, og
  **Siste forespørsel**.
- En melding, **Importert fra miljøet**, når innstillingene ble importert fra miljøet,
  om å fjerne variablene (se [Før kortet](#før-kortet-miljøvariablene)). Den blir
  stående til neste endring som gjøres på kortet — en lagring, et utstedt token, en del
  som slås av — og å slå av noe som allerede er av endrer ingenting.

**Med Microsoft Entra ID-modulen** er kortets to deler også de to siste stegene i
modulens veiviser: steg 5, **Innlogging**, er leverandørskjemaet, fylt ut med
leietakerens autoritet og klient-ID-en veiviseren registrerte (bare på et tomt
skjema), og steg 6, **Klargjøring**, er SCIM-delen. Begge er bare en eiers. De er de samme skjemaene over
den samme raden og de samme operasjonene; lagre på hvilket som helst av stedene
([Microsoft Entra ID](/nb/admin/entra/#5-innlogging)).

Bak kortet ligger seks operasjoner under `/api/v1/identity/owner/sso`, i kontrakten
[`openapi/identity.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/identity.yaml):

| På kortet | Forespørsel |
| --- | --- |
| Når det åpnes | `GET /owner/sso` — raden uten klienthemmeligheten eller noe token, tilbakekalls-URL-en, SCIM-endepunktet, siste OIDC-innlogging og siste SCIM-forespørsel |
| **Lagre** | `PUT /owner/sso/oidc`, med `If-Match` som bærer versjonen kortet leste |
| **Slå av** for leverandøren, eller **Av** og **Lagre** | `DELETE /owner/sso/oidc` |
| **Nytt token** | `POST /owner/sso/scim/token` |
| **Avslutt overlapp** | `DELETE /owner/sso/scim/previous` |
| **Slå av** i SCIM-delen | `DELETE /owner/sso/scim` |

Hver skriving holder raden i én transaksjon, flytter `version` videre, svarer hele
raden med den nye versjonen, og revideres som `sso-settings.changed` med innstillingene
før og etter — aldri klienthemmeligheten og aldri et token eller sammendraget av det.
Et **Slå av** eller **Avslutt overlapp** uten noe å fjerne skriver ingenting: det
svarer raden som den er, med samme versjon og uten revisjonslogg. Bare **Lagre** bærer
versjonen: en lagring fra et kort som ble åpnet før noen andres skriving, avvises med
`412 version_mismatch`, og kortet sier «Innstillingene er endret»: velg **Last inn på
nytt** og lagre igjen. Svaret som bærer et nytt SCIM-token, sendes med
`Cache-Control: no-store`. Operasjonene er begrenset til 30 forespørsler i minuttet,
som resten av brukeradministrasjonen.

## OIDC for arbeidsstyrken

OIDC bruker autorisasjonskodeflyten med PKCE og nonce, en forseglet state-cookie og en
fast lokal fullføringssti. Leverandørens tilgangs- og ID-tokener lagres aldri.
Leverandøridentiteten er bare en korrelasjon ved innlogging: den gir ikke lokale roller,
beviser ikke lokal MFA og omgår ikke den lokale Eier/MFA-policyen. Nye identiteter
provisjoneres fortløpende som vanlige lokale `User`-kontoer nøklet på den validerte
utstederen og `sub` (skiller mellom store og små bokstaver); en e-postkollisjon avvises
i stedet for å kobles automatisk.

Det offentlige tilbakekallet, som kortet viser som **Tilbakekalls-URL**, er:

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

Det finnes ingen innstilling for klientautentisering. Vantigo avgjør ut fra **hvilken
legitimasjon kortet har**:

| Legitimasjon lagret | Modus |
| --- | --- |
| **Klienthemmelighet** | Klienthemmelighet |
| **Tokenfil** | Workload identity (bare Entra) |
| Begge i én lagring | **Avvises** — de utelukker hverandre |
| Ingen av dem, med en hemmelighet lagret for samme leverandør og klient-ID | Klienthemmelighet — den lagrede beholdes |
| Ingen av dem, ellers | **Avvises** — én er påkrevd |

Å lagre den ene fjerner den andre: en tokenfil fjerner den lagrede hemmeligheten, en
hemmelighet fjerner tokenfilen. Bare en hemmelighet beholdes ved å utelates; en
tokenfil er med i hver lagring, og kortet sender stien det viser hver gang. En lagring som endrer leverandøren eller klient-ID-en,
må ha med en legitimasjon, siden den gamle applikasjonens hemmelighet aldri overføres
til en ny. Hemmeligheten er forseglet under `APP_SECRET` og vises aldri igjen.

### Microsoft Entra ID: klienthemmelighet

Med Microsoft Entra ID-modulen, følg veiviseren dens i stedet: registreringen,
leietakeren og klient-ID-en er steg 1 til 3, og dette skjemaet er steg 5
([Microsoft Entra ID](/nb/admin/entra/)). For hånd:

1. Opprett eller velg én Entra-appregistrering for denne installasjonen. Bruk en
   konfidensiell webapplikasjon og opprett en klienthemmelighet. Kopier verdien: du
   trenger den én gang, til kortet.
2. Noter den leietakerspesifikke autoriteten, nøyaktig i denne formen, inkludert
   `/v2.0`:

   ```text
   https://login.microsoftonline.com/<tenant-guid>/v2.0
   ```

   `<tenant-guid>` må være leietakerens GUID. Autoriteter som bruker `common`,
   `organizations`, `consumers`, en annen vert, en spørrestreng eller en annen sti,
   avvises på **Autoritet** med formen den må ha.
3. Registrer kortets **Tilbakekalls-URL** som registreringens omdirigerings-URI for
   web.
4. Gi bare de delegerte omfangene innloggingsflyten trenger (`openid`, `profile`,
   `email`). Behandle ikke leverandørens gruppe- eller rollekrav som
   autorisasjonsgrunnlag i Vantigo.
5. På kortet **Single sign-on**, velg **Microsoft Entra ID** som leverandør, og fyll
   inn **Autoritet** og applikasjons-ID-en (klient-ID-en) som **Klient-ID** (den må
   være en GUID). Velg **Klienthemmelighet** under **Legitimasjon** og lim den inn. Gi
   knappen et **Visningsnavn** dersom ikke `Workforce SSO` er det folk skal se (høyst
   100 tegn, trimmet). Velg **Lagre**: kortet sier «OIDC-innstillingene er lagret»,
   eller merker hvert felt lagringen avviste, med setningen som sier hvorfor.

Verifiser: innen 20 sekunder viser innloggingssiden **Fortsett med** og visningsnavnet.
Logg inn med en vanlig testkonto, og sjekk at kortets **Siste innlogging** flytter seg.

Etter de innebygde tokenkontrollene krever Vantigo en `tid`-GUID som samsvarer med
leietakeren i den konfigurerte autoriteten, og en `oid`-GUID. Dersom det validerte
tokenet bærer flere `aud`-krav, må `azp` være lik den konfigurerte klient-ID-en.
Leverandøridentiteter forblir lokale `User`-kontoer og blir aldri Eiere gjennom krav.

For å rotere hemmeligheten, opprett en ny på registreringen, lim den inn i
**Klienthemmelighet** og velg **Lagre**; når kortet sier at den ble satt i dag, slett
den gamle i Entra. Ingen omstart trengs.

### Microsoft Entra ID: workload identity

Workload identity erstatter **klienthemmeligheten som brukes når autorisasjonskoden
innløses**. Den erstatter ikke sluttbrukerens innlogging, gjør ikke OIDC-krav til
betrodd lokal autorisasjon, og erstatter ikke det uavhengige SCIM-bærertokenet.

Plattformen må levere alt det følgende før kortet lagres:

- Et Kubernetes-cluster med en OIDC-utsteder og Azure Workload Identity aktivert, eller
  den tilsvarende Azure-hostede integrasjonen.
- Konfigurasjonen for den muterende webhooken/sidecaren som projiserer et
  tjenestekontotoken inn i hver pod som betjener API-et, på én absolutt sti (webhooken
  setter `AZURE_FEDERATED_TOKEN_FILE` til den, normalt
  `/var/run/secrets/azure/tokens/azure-identity-token`).
- En Entra-appregistrering hvis klient-ID er kortets **Klient-ID**.
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

Deretter, på kortet: **Microsoft Entra ID**, **Autoritet** og **Klient-ID**; velg
**Tokenfil** under **Legitimasjon** og skriv stien til den projiserte filen. Velg
**Lagre**. Prosessen som lagrer, kontrollerer før den rører raden at stien er absolutt
og ligger i katalogen `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` navngir — katalogen til
`AZURE_FEDERATED_TOKEN_FILE` når webhooken satte den, ellers
`/var/run/secrets/azure/tokens`, med mindre distribusjonen setter variabelen selv —
og først da at den peker på en vanlig fil på høyst 16 KiB som den kan lese. Den
avviser stien på **Tokenfil** ellers og navngir katalogen; kortets ord når ingen
undersøkelse utenfor den, så kortet røper ingenting om resten av serverens filsystem.
Stien lagres normalisert. Å lagre den fjerner en eventuell lagret klienthemmelighet.

**Hver replika må se den samme filen på den samme stien.** Lagringen kontrolleres av
bare én prosess; en replika som ikke kan lese filen, feiler innloggingene den fullfører
med `/sign-in?error=oidc_remote_failure`. Påstanden leses **på nytt fra filen ved hver
innløsning av en autorisasjonskode** — bare fra en vanlig fil, og ikke mer enn 16 KiB
av den — og sendes med
`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
Roter det projiserte tokenet gjennom plattformen; Vantigo mellomlagrer det ikke.

### Google Workspace

1. Opprett en Google OAuth-webklient og kopier hemmeligheten. Google støtter bare
   klienthemmelighet — workload identity godtas ikke.
2. Registrer kortets **Tilbakekalls-URL** som en autorisert omdirigerings-URI.
3. På kortet, velg **Google Workspace**: **Autoritet** fylles inn med nøyaktig
   utstederen `https://accounts.google.com`. Skriv en klient-ID som slutter på
   `.apps.googleusercontent.com` som **Klient-ID**, og **Klienthemmelighet**.
4. List opp hvert tillatte Workspace-domene i **Domener** som et rent DNS-navn, for
   eksempel `example.com`. **Minst ett er påkrevd for Google.** Hvert trimmes, gjøres
   om til små bokstaver og fratas etterfølgende punktum, og duplikater fjernes; en
   URL, en adresse, en port eller en enkelt etikett avvises.
5. Velg **Lagre**, og verifiser som for Entra.

Vantigo krever `email_verified`, en syntaktisk gyldig e-postadresse og et ikke-tomt
`hd`-krav som samsvarer med e-postdomenet og finnes i **Domener**. Personlige
Gmail-kontoer og identiteter som er uverifiserte eller har et domene som ikke
samsvarer, avvises.

### Endre autoriteten, eller slå leverandøren av

Folk som har logget inn gjennom leverandøren, er koblet til den med utstederen —
autoriteten — og sitt subject. En lagring som endrer **Autoritet** mens noen er koblet
under den nåværende, ber først om bekreftelse — «Endre leverandørens autoritet?» — og
sendes på nytt med `confirmIssuerChange` bare når du velger **Endre autoritet**
(forespørselen avvises med `409 oidc_links_exist` uten den). Bekreft bare når du mener å flytte til en
annen leietaker eller leverandør: ingen som er koblet under den gamle autoriteten,
gjenkjennes under den nye. Deres neste SSO-innlogging finner ingen kobling, og fordi en
adresse aldri kobler en ny identitet til en eksisterende konto, avvises den
(`oidc_email_conflict`) så lenge brukeren deres finnes.

**Slå av** i leverandørens del av kortet — eller **Av** som leverandør og **Lagre**,
som gjør det samme — spør først, «Slå av SSO?», og med **Slå av** slår det OIDC av og
tømmer innstillingene, den lagrede hemmeligheten medregnet. Mens folk er koblet til
leverandøren, sier en dialog til «Personer er koblet til denne leverandøren», og bare
dens **Slå av** sender `?confirm=true` (uten den avvises forespørselen med
`409 oidc_links_exist`). Innen 20 sekunder viser
innloggingssiden ingen leverandørknapp, og hver prosess slutter å godta leverandørens
tilbakekall.

## SCIM

SCIM er på så lenge et token finnes. Det faste protokollendepunktet, som kortet viser
som **Endepunkt-URL**, er:

```text
https://<public-host><base-path>/api/v1/identity/scim/v2
```

Forespørsler autentiseres med `Authorization: Bearer <token>` og bruker
`application/scim+json`. De støttede ressursene er `Users` og `Groups`, pluss
standardendepunktene for oppdagelse `ServiceProviderConfig`, `ResourceTypes` og
`Schemas`.

**Tokenet lages av serveren og vises én gang.** Vantigo lager det (32 tilfeldige byte,
base64url uten utfylling), viser det i en dialog med **Kopier token** og «Det vises
ikke igjen.», og beholder bare SHA-256-sammendraget, så et token vises aldri igjen: et tapt token erstattes med
et nytt, det gjenopprettes aldri.

Slik slår du SCIM på:

1. På kortet **Single sign-on**, kopier **Endepunkt-URL**.
2. Velg **Nytt token**. Dialogen viser tokenet; kopier det rett inn i katalogens
   provisjoneringsinnstillinger (i Entra, bedriftsapplikasjonens side
   **Provisioning**: **Tenant URL** er endepunkt-URL-en, **Secret Token** er tokenet)
   og lukk dialogen med **Ferdig**. Den lukkes ikke av et klikk ved siden av.
3. Test tilkoblingen fra katalogen, og sjekk at SCIM-delens **Siste forespørsel**
   flytter seg.

SCIM-tilstanden er persistent: brukertilordninger, SCIM-grupper og -medlemskap,
livssyklustilstand, ETag-er og revisjonsposter ligger i PostgreSQL. Brukere opprettet
av SCIM er uprivilegerte, og **Eier-kontoer er beskyttet mot SCIM-endringer** — en
oppdatering, patch eller sletting rettet mot en Eier avvises, noe som holder
nødtilgangskontoen utenfor provisjoneringssystemets rekkevidde.

### Virksomhetsutvidelsen og ansattnummeret

Ressursen User godtar SCIMs **virksomhetsutvidelse** (enterprise extension),
`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User` — `employeeNumber`,
`costCenter`, `organization`, `division`, `department` og `manager` — som beholdes i
brukerens SCIM-profil, svares tilbake på `GET`, annonseres av `Schemas` og
`ResourceTypes`, og kan patches på sine egne stier (`urn:…:User:department`) eller med
en PatchOp uten sti der verdien har blokken — formen Entra sender, med lederen som en ren
streng godtatt også. Hver verdi er på høyst 256 tegn. Den endrer ingenting i brukerens egen post: særlig blir katalogens
`employeeNumber` aldri brukerens ansattnummer i Vantigo — bare Ansattes klargjøring eller
en eiers inntak gjør det kravet ([detaljer](/nb/admin/authentication/#scim)).

Entras standardtilordning av attributter for en egendefinert applikasjon sender den
allerede: **`employeeId → employeeNumber`** i utvidelsen, og
`userPrincipalName → userName`. Merk at samme standard sender
**`mailNickname → externalId`** — e-postkallenavnet, ikke objekt-ID-en. La begge stå som
de er. Entra matcher en bruker den klargjør, mot målet på ett attributt om gangen, og
målet må kunne filtrere på det: Vantigo svarer på
`GET /Users?filter=urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:employeeNumber eq "1042"`
så vel som `userName eq` og `externalId eq`.

**Kontoer Ansatte kunngjør.** For en person med kontotypen `entra` i Ansatte lar Ansatte
leverandøren opprette kontoen og kunngjør den for identitetsmodulen
([den kunngjorte kontoen](/nb/admin/authentication/#den-kunngjorte-kontoen)).
SCIM-opprettelsen som bringer brukeren, matches i rekkefølge på utvidelsens
`employeeNumber`, så på `userName` mot det kunngjorte brukerhovednavnet, så på
`externalId` mot den kunngjorte objekt-ID-en — som bare treffer hvis du tilordner
`objectId` til `externalId`, siden standarden sender e-postkallenavnet. Et treff gjør
brukeren styrt av Ansatte og nummerert idet den kommer, uten inntak; revisjonsloggen
registrerer `account.adopted-from-provider`. En opprettelse uten treff lager en vanlig
bruker, som før.

For å kontrollere, etter at leverandøren har opprettet en persons konto og
klargjøringsrunden har kjørt: brukeren vises på siden **Brukere** styrt av Ansatte med
personens ansattnummer og ikke ventende, og personens fane Konto i Ansatte viser
Vantigo-kontoen aktiv.

### Tokenrotasjon med overlapp

1. Velg **Nytt token**. Siden et token finnes, spør kortet først, «Utstede et nytt
   token?», i feltet «Godta det nåværende tokenet i (timer)»: 1 til 24, 24 som
   standard (forespørselens `previousExpiresAt`; 24 sender ingen, så serverens egen
   standard gjelder). Velg **Utsted token**. Det nye tokenet vises så én gang, med
   «Det forrige tokenet godtas til _dato_.»
2. Legg det nye tokenet inn i katalogen og test tilkoblingen der.
3. Når alle klienter har byttet, velg **Avslutt overlapp**: det gamle tokenet slutter å
   godtas nå i stedet for ved utløpet. Eller la overlappet løpe ut.

Bare tokenet som nettopp ble erstattet, overlapper. **Nytt token** under et overlapp
avslutter det pågående med en gang: tokenet før det forrige slutter å godtas
umiddelbart. Ingenting krever omstart, og ingenting i installasjonen endres. Et utløp i
fortiden eller mer enn 24 timer frem avvises på `previousExpiresAt`.

### Slå av SCIM

Velg **Slå av** i SCIM-delen, og bekreft «Slå av SCIM?» med **Slå av**. Tokenene
glemmes, og innen 20 sekunder avviser endepunktet hver forespørsel. Brukerne, tilordningene og gruppene SCIM provisjonerte, blir som de er, og
en katalogs deaktivering av en bruker slutter å telle for hvem som har en rettighet.
For å slå det på igjen, utsted et **Nytt token** og gi det til katalogen.

### Status og driftsbevis

Kortet viser siste OIDC-innlogging (**Siste innlogging**) og siste autentiserte
SCIM-forespørsel (**Siste forespørsel**). En
autentisert Eier kan også lese:

```text
GET /api/v1/identity/owner/system-status
```

Svaret inneholder, lest fra den samme raden:

- `total`, `active`, `disabled` — brukertellinger
- `pendingInvitations`
- `staticOidcEnabled`, `staticOidcProvider`
- `staticScimEnabled`
- `lastStaticOidcSignInAtUtc`, `lastAuthenticatedScimRequestAtUtc`

De to tidsstemplene er driftsprojeksjoner, ikke autentiseringskritisk tilstand: en feil
ved skriving av telemetri eller til databasen må ikke gjøre en vellykket OIDC- eller
SCIM-operasjon til en mislykket en. Ingen av endepunktene returnerer noen gang
bærertokener, tokensammendrag eller klienthemmeligheter. Behandle null-tidsstempler
som «ingen vellykket bruk er registrert ennå».

## Før kortet: miljøvariablene

Frem til utgivelsen som kom med kortet, var leverandøren og SCIM-tokenet
miljøvariablene `OIDC_*` og `SCIM_*`, lest ved hver oppstart. Den utgivelsen flytter
dem inn i raden én gang, av seg selv:

- **Importen.** Den første prosessen som betjener API-et (`api` eller `server`) og
  finner raden aldri skrevet, mens miljøet setter opp en leverandør (`OIDC_PROVIDER`)
  eller SCIM (`SCIM_TOKEN`) — de to bryterne lasteren leser de andre variablene etter —
  importerer dem i én transaksjon som holder raden: klienthemmeligheten forseglet,
  `SCIM_TOKEN` og `SCIM_PREVIOUS_TOKEN` beholdt som sammendragene sine med overlappets
  utløp, uten noen bruker bak. Den samme transaksjonen registrerer
  [autoritetsmodusen](/nb/admin/authentication/#autoritetsmodusen) raden gir, ved denne
  oppstarten og hver senere. Katalogen og
  leverandøren fortsetter å virke med legitimasjonen de alt har. Replikaer som starter
  samtidig, importerer én gang: den andre finner raden skrevet. En worker importerer
  aldri.
- **Sporet.** Importen revideres som `sso-settings.imported`, uten aktør og med
  **navnene** på variablene den fant, aldri verdiene — av `OIDC_PROVIDER`,
  `OIDC_AUTHORITY`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`,
  `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` (også for en tokenfil hentet fra
  `AZURE_FEDERATED_TOKEN_FILE`), `OIDC_ALLOWED_DOMAINS`, `OIDC_DISPLAY_NAME` (med mindre
  den er `Workforce SSO`), `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN` og
  `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`. Loggen sier, som en advarsel, med de samme navnene
  under `variables`:
  `identity: the OIDC and SCIM settings were imported from the environment into the database; the variables are now ignored and should be removed`.
  Kortet viser meldingen om å fjerne dem.
- **En verdi for lang for raden.** Importen feiler aldri på en slik. Et visningsnavn
  over 100 tegn importeres avkortet til 100, med en advarsel som navngir
  `OIDC_DISPLAY_NAME`. En `OIDC_AUTHORITY` eller `OIDC_CLIENT_ID` over 200 tegn, eller
  en `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` over 500, kan ikke avkortes: leverandøren
  importeres **av**, hemmeligheten med den, og en advarsel navngir variabelen —
  `identity: the OIDC provider was imported off, since <variable> is longer than the single sign-on settings hold; set the provider on the Single sign-on card`.
  SCIM importeres som vanlig. Sett leverandøren på kortet.
- **Raden vinner etterpå.** En prosess som finner raden skrevet, importerer ingenting,
  uansett hva variablene sier — en senere endring av en av dem har ingen virkning — og
  navngir hver som fortsatt er satt, i en advarsel ved hver oppstart:
  `identity: these OIDC and SCIM variables are ignored, since the single sign-on settings are kept in the database; remove them`.
  En Eier som slår leverandøren eller SCIM av på kortet, overstyres ikke av dem.
- **De kontrolleres fortsatt ved oppstart i denne utgivelsen.** Lasteren validerer dem
  fortsatt, så en feilformet verdi, en halvveis fjernet leverandør (`OIDC_PROVIDER`
  ikke satt mens en annen `OIDC_*` er igjen) eller en `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`
  som har passert, gjør fortsatt at oppstarten feiler. **Neste utgivelse avviser dem**
  ved oppstart, og navngir hver av dem.

Prosedyren, én gang, ved oppgraderingen til den utgivelsen:

1. Oppgrader slik [utgivelsesprosedyren](#utgivelses--og-oppgraderingsprosedyre) sier,
   med variablene fortsatt på plass til den første oppstarten.
2. Se etter importadvarselen i loggen, eller etter `sso-settings.imported` i
   revisjonsloggen, og åpne kortet **Single sign-on**: leverandøren, autoriteten,
   klient-ID-en, «Satt» for hemmeligheten og **Token utstedt** for SCIM står der, med
   meldingen. Den blir stående til neste endring som gjøres på kortet.
3. Fjern hver `OIDC_*`- og `SCIM_*`-variabel fra hver API- og worker-container —
   `vantigo.env`, hemmelighetslageret, plattformens miljø — alle samtidig, og start på
   nytt når det passer. `AZURE_FEDERATED_TOKEN_FILE`, som webhooken for workload
   identity setter, kan bli stående. En tokenfil importert fra
   `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` utenfor webhookens katalog trenger
   `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` satt til sin katalog før kortet lagres igjen,
   ellers avvises lagringen på **Tokenfil**.
4. Verifiser: neste oppstart logger ingen advarsel om variablene, og lokal
   Eier-innlogging, en SSO-innlogging og en SCIM-testtilkobling virker fortsatt.

En installasjon uten noen av variablene importerer ingenting; raden forblir uskrevet
til en Eier lagrer kortet.

**Å rulle tilbake over utgivelsen** dropper raden, og med den den forseglede
hemmeligheten og tokensammendragene: binærfilen før den leser variablene igjen, så sett
dem før du starter den. Et SCIM-token utstedt på kortet kan ikke gjenopprettes; sett et
nytt `SCIM_TOKEN` og gi det til katalogen.

## Ansatte i en føderert installasjon

Ansatte bestemmer **per person**, etter personens kontotype; ved oppgraderingen som
innførte typene, ble hver person og standarden satt fra
[autoritetsmodusen](/nb/admin/authentication/#autoritetsmodusen) én gang — `external` i
en føderert installasjon, der SSO eller SCIM er på. Denne delen gjelder typen `external`;
HR kan gi en person `local` eller, når en leverandørmodul er installert, `entra`
([Ansattes kontotyper](/nb/user/staff/#konto)). For en person av typen `external` eier
identitetsleverandøren kontoen, og Ansattes kontoklargjøring kobler seg bare til den. Den oppretter aldri en bruker og sender aldri en invitasjon. For hver
person Ansatte styrer kontoen til, finner den brukeren Ansatte styrer som har personens
ansattnummer — en bruker identitetsleverandøren opprettet og en eier
[tok inn](/nb/admin/authentication/#ta-en-eksisterende-bruker-inn-under-ansatte) med det
nummeret — aktiverer den igjen når Ansatte deaktiverte den (en ny ansettelse),
deaktiverer den etter at ansettelsen er slutt, og holder språket og ansattnummeret i
takt. Den endrer verken navnet eller adressen, og gir ingen rolle: de er leverandørens
og eierens. En bruker en eier deaktiverte, forblir deaktivert. En som slutter,
deaktiveres i identitetsmodulen også når katalogen allerede har deaktivert dem, så en
senere aktivering i katalogen, eller SCIM slått av, slipper dem ikke inn igjen; en ny
ansettelse aktiverer dem igjen i identitetsmodulen, og så lenge katalogen fortsatt
melder dem inaktive, holder den dem ute. Til leverandøren har opprettet
kontoen og en eier har tatt den inn, melder Ansatte personens konto som ikke funnet.
For typen `entra` trengs ikke noe inntak: kontoen
[kunngjøres](#virksomhetsutvidelsen-og-ansattnummeret) og tas inn idet den kommer.

En ventende bruker — en Ansatte opprettet for typen `local`, før SSO eller SCIM ble slått
på, og som ikke har godtatt invitasjonen — blir aldri identitetsleverandørens konto bare
ved adressen: OIDCs første innlogging med den adressen svarer `account_locked` og
kobler ingenting, og SCIMs opprettelse av en User med den adressen avvises
`DuplicateEmail`, uten noen ny bruker. La den ventende brukeren godta invitasjonen,
eller la en eier slutte å la Ansatte styre den og slette den, før leverandørens konto
tar adressen.

## Lokal Eier for nødtilgang og MFA

Behold minst én lokal Eier-konto som nødtilgangsvei selv når SSO er på. Opprett den
første Eieren via `/setup` på den ferske installasjonen, før noen andre kan nå den;
den kontoen er også SystemAdmin. Lokal passordinnlogging, passnøkler og lokal MFA er
uavhengige av den eksterne leverandøren, og kortet nås med dem: en lokal Eier kan slå
av en leverandør som ikke virker, eller utstede et nytt SCIM-token, mens leverandøren
er nede.

Dersom installasjonen er tilgjengelig før en operatør rekker å fullføre `/setup`, sett
`BOOTSTRAP_OWNER_EMAIL` i stedet: den samme nødtilgangs-Eieren settes da inn via en
e-postinvitasjon utstedt ved oppstart, i stedet for av den som når `/setup` først. Se
[management-lytteren](/nb/admin/management-listener/#sette-inn-den-første-eieren-uten-setup).

Med `OWNERS_REQUIRE_MFA=1` (standard utenfor development) krever Eier- og
SystemAdmin-operasjoner — kortet **Single sign-on** blant dem — en andre faktor: en
TOTP-kode, en gjenopprettingskode eller en passnøkkel. Oppbevar gjenopprettingskoder
frakoblet i organisasjonens nødtilgangsprosess. OIDC-krav oppfyller aldri dette kravet
og gir aldri lokale roller.

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
  plassholdere. Kopier et nytt SCIM-token fra dialogen rett inn i katalogen, ingen
  andre steder.
- Foretrekk plattformens hemmelighetstjeneste eller en injisert miljøhemmelighet for det
  som fortsatt ligger i miljøet. Begrens fileierskap og -rettigheter, og gi
  migreringsjobben bare de hemmelighetene den trenger.
- Roter OIDC-klienthemmeligheten ved å lagre en ny på kortet, og SCIM-tokenet med
  **Nytt token**; ingen av dem krever omstart. Det tidsbegrensede overlappet med
  forrige token finnes bare for SCIM.
- `APP_SECRET` kan ikke roteres på stedet: den utleder nøklene for økter, cookies, TOTP
  og kanallegitimasjon i Kommunikasjon og nøkkelen OIDC-klienthemmeligheten er
  forseglet under, så å endre den ugyldiggjør alle sammen. Etter en slik endring, lagre
  klienthemmeligheten på kortet igjen.
- Dersom en klienthemmelighet eller et SCIM-token er eksponert, erstatt det på kortet
  med en gang — for SCIM, **Nytt token** og deretter **Avslutt overlapp** — og hver
  replika slutter å godta det gamle innen 20 sekunder; tilbakekall hemmeligheten hos
  leverandøren også. Dersom `APP_SECRET` er eksponert, erstatt den og start alle
  replikaer på nytt. Undersøk deretter logger, revisjonsloggen og statusendepunktet for
  uventet bruk.
