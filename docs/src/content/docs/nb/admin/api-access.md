---
title: API-tilgang
description: La et annet program bruke Vantigos API uten en persons økt — integrasjoner, rollene deres, API-nøkler med utløp, begrensning, bryter for bare lesing og nettverk, rotering og tilbakekalling av en nøkkel, hva en nøkkel aldri får, hvordan en klient sender den, hastighetsgrensen og hodene dens, svarene den kan få, forespørselsloggen, varslene om utløp, webhook-endepunkter, signeringshemmelighetene deres og leveranseloggen med ny sending, å koble til en AI-klient med MCP med en nøkkel eller ved å logge inn, samtykkesiden, tilkoblede apper og å tilbakekalle en, og hva du gjør når en nøkkel lekker.
sidebar:
  order: 22
sources:
  - apps/server/internal/identity/integrations.go
  - apps/server/internal/identity/access_apikey.go
  - apps/server/internal/identity/apikey.go
  - apps/server/internal/identity/workers.go
  - apps/server/internal/identity/apirequests.go
  - apps/server/internal/identity/apikey_notices.go
  - apps/server/internal/identity/integration_requests.go
  - apps/server/internal/identity/idempotency.go
  - apps/host/frontend/src/routes/workspace/api-access.tsx
  - apps/host/frontend/src/routes/workspace/api-access_.$integrationId.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-integration.tsx
  - apps/host/frontend/src/routes/workspace/-api-key-dialog.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-requests.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-webhooks.tsx
  - apps/host/frontend/src/routes/workspace/-webhook-secret-dialog.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-webhook-deliveries.tsx
  - apps/server/internal/identity/webhooks.go
  - apps/server/internal/identity/webhooks_url.go
  - apps/server/internal/identity/webhooks_secret.go
  - apps/server/internal/identity/webhooks_signer.go
  - apps/server/internal/identity/webhooks_client.go
  - apps/server/internal/identity/webhooks_handler.go
  - apps/server/internal/identity/webhooks_worker.go
  - apps/server/internal/identity/webhooks_log.go
  - apps/server/internal/contracts/webhook_events.go
  - apps/server/internal/identity/integrations_mcp.go
  - apps/host/frontend/src/routes/workspace/-api-access-mcp.tsx
  - apps/server/internal/module/apimcp.go
  - apps/server/internal/module/apimcp_protocol.go
  - apps/server/internal/module/apimcp_index.go
  - apps/server/internal/module/apimcp_tools.go
  - apps/host/frontend/src/routes/workspace/-api-access-shared.ts
  - apps/server/internal/apiref
  - apps/apiref/frontend/src
  - apps/server/internal/identity/oauth_connections.go
  - apps/server/internal/identity/oauth_client.go
  - apps/server/internal/identity/oauth_authorize.go
  - apps/host/frontend/src/routes/oauth.authorize.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-connections.tsx
  - apps/host/frontend/src/api/oauth.ts
---

En **integrasjon** er kontoen et annet program — en nettbutikk, et rapportverktøy, et
skript — bruker Vantigos API som. Den har roller slik en person har, den logger aldri inn, og
den handler bare med én av sine **API-nøkler** på forespørselen. Det den lager og endrer,
føres på den, med navnet og et **API**-merke på avataren.

Integrasjoner og nøklene deres lages under **Administrer arbeidsområde › API-tilgang**.
Hvordan en nøkkel vurderes ved hver forespørsel, står under
[Identitet, autentisering og utrulling](/nb/admin/authentication/#integrasjonsbrukere).

## Før du begynner

- Du er **Eier**. Alle operasjonene bak skjermbildet er eierens, og når arbeidsområdet krever
  MFA for eiere, avvises de til du har det. En administrator som har fått rolleadministrasjon
  delegert, ser ikke skjermbildet.
- Rollene integrasjonen skal ha, finnes som **egendefinerte roller** under **Roller og
  tilgang**. En integrasjon kan bare ha egendefinerte roller: aldri **Eier**, **SystemAdmin**
  eller **Bruker**. Lag en rolle for hvert program med nøyaktig det programmet trenger.
- Du vet hvor programmet skal oppbevare nøkkelen — et hemmelighetslager eller
  konfigurasjonen — fordi nøkkelen vises én gang.

## Opprett en integrasjon

1. Åpne avatar-menyen, velg **Administrer arbeidsområde** og så **API-tilgang**. Listen
   viser hver integrasjons **Status** (*Aktiv*, *Deaktivert* eller *Arkivert*), **Roller**,
   **Gyldige nøkler**, **Første utløp** blant dem og når en nøkkel ble **Sist brukt**.
2. Velg **Ny integrasjon**. Gi den et **Navn** (1 til 100 tegn, unikt blant integrasjonene som
   ikke er arkivert), en **Beskrivelse** om det hjelper, en **Kontaktperson** — personen som
   har ansvaret for den, og som må være en person med konto — og **Roller**.
3. Velg **Opprett integrasjon**. Siden dens åpnes.

For å endre den senere velger du **Handlinger › Rediger detaljer**: navnet, beskrivelsen,
kontaktpersonen og **Hastighetsgrense** (60 til 6 000 forespørsler i minuttet til sammen for
nøklene; standard er 600; se [Hastighetsgrensen](#hastighetsgrensen)). Kontaktpersonen får
nøklenes [varsler om utløp](#varsler-om-utløp). Lagring erstatter beskrivelsen og kontaktpersonen med det skjemaet
inneholder, så et felt du tømmer, blir tømt. Har noen andre endret integrasjonen etter at du
åpnet den, avvises lagringen, og siden viser det siste; gjør endringen på nytt.

## Gi den roller

På integrasjonens side er **Roller** den samme velgeren som **Roller og tilgang** bruker, med
de egendefinerte rollene. Velg **Lagre roller** for å erstatte rollene. Neste forespørsel fra
hver nøkkel ser de nye rollene.

Under viser **Hva rollene tillater** alle tillatelsene rollene gir integrasjonen:

- **Aldri tilgjengelig for API-nøkler** merker en tillatelse ingen nøkkel noen gang har, uansett
  hva rollene sier: alle tillatelser under `identity:` og `agents:`, og alle tillatelser som
  ikke kan delegeres. En forespørsel som trenger en av dem, avvises med `403`.
- **Sensitiv** merker en tillatelse som når personopplysninger eller økonomiske data. En nøkkel
  som har den, kan lese eller endre disse dataene i store mengder, så gi den bare til en
  integrasjon som trenger den.

Bare en eier kan endre en integrasjons roller, og bare på denne siden: en integrasjon vises
ikke under **Roller og tilgang**, og databasen avviser en rolle gitt den på annen måte. En
delegert administrator som forvalter en rolle en integrasjon har, kan heller ikke endre den
rollen (`403 role_edit_denied`), fordi en tillatelse lagt til den ville utvidet alle
integrasjonens nøkler.

## Lag en nøkkel

1. Under **API-nøkler** velger du **Ny API-nøkkel**.
2. Gi den et **Navn**, for eksempel *Produksjon*.
3. Velg **Utløp**: 30, 90, 180 eller 365 dager, eller **På en dato** høyst 365 dager fram.
   Alle nøkler utløper; standard er 90 dager.
4. Slå eventuelt på **Begrens nøkkelen** (nedenfor), slå på **Bare lesing** (nedenfor) og fyll
   inn **Nettverk** (nedenfor).
5. Velg **Opprett nøkkel**. Nøkkelen vises **én gang**, med **Kopier** og et `curl`-eksempel
   mot denne installasjonens adresse — og, når integrasjonens MCP er på, tilkoblingen for en
   AI-klient ([Koble til en klient](#koble-til-en-klient)). Kopier den inn i programmets
   hemmelighetslager og velg
   **Jeg har kopiert nøkkelen**. Vantigo beholder bare en hash: nøkkelen kan ikke vises igjen,
   og når dialogen lukkes, er den glemt. Går den tapt, roterer du nøkkelen.

Nøkkeltabellen viser hver nøkkels **Navn**, formen den kan kjennes igjen på
(`vtg_4f2a…9XyZ`), status (*Aktiv*, *Utløpt* eller *Tilbakekalt*), **Utløp**,
**Begrensning**, om den er **Bare lesing**, **Nettverk** og når og fra hvilken adresse den
ble **Sist brukt** (på minuttet).

### Begrens en nøkkel

En nøkkel som ikke er begrenset, kan bruke alt rollene tillater. **Begrens nøkkelen**
avgrenser den til tillatelsene du krysser av, av dem rollene gir, gruppert etter modul som i
rolleredigeringen. En tillatelse som aldri er tilgjengelig for API-nøkler, vises og kan ikke
krysses av.

**Vis tillatelsene rollene ikke gir** viser resten av katalogen. En nøkkel kan navngi en av
dem: den gjør ingenting før rollene gir den, og nøkkeltabellen merker den **Ikke gitt av
rollene**. En tillatelse en senere versjon av Vantigo fjerner, beholdes i begrensningen og
ignoreres, og tabellen stryker den over.

### Gjør en nøkkel til bare lesing

**Bare lesing** lar nøkkelen kalle bare operasjonene som leser. Hvilke det er, bestemmes av hver
operasjon i Vantigos API-kontrakt, ikke av navnet på tillatelsen den krever. En nøkkel for
bare lesing som kaller noe annet, avvises med `403 api_key_read_only`.

### Begrens en nøkkels nettverk

En tilkoblet app har ingen nettverksliste: **Nettverk** hører til en nøkkel, og en tilkobling
laget ved å logge inn begrenses ikke av dem ([Koble til ved å logge inn](#koble-til-ved-å-logge-inn)).

**Nettverk** lister adressene eller CIDR-områdene nøkkelen kan brukes fra, én per linje —
IPv4 eller IPv6, høyst 20. Tomt betyr hvor som helst. En forespørsel utenfra avvises med
`401 unauthenticated`, som en ukjent nøkkel, også når nøkkelen i tillegg er utløpt eller
tilbakekalt, så avsenderen får ikke vite noe om nøkkelen. **Sist brukt** registrerer bare en
forespørsel nøkkelen ble sluppet gjennom for, aldri en avvist. Adressen er klientens slik serveren ser den gjennom
`TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS`
([Videresendte headere og HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)):
bak en proxy som ikke er klarert, har alle forespørsler proxyens adresse.

## Roter en nøkkel

Rotering bytter ut en nøkkel uten et øyeblikk der programmet står uten.

1. Velg **Roter** i nøkkelens rad.
2. Velg **Overlapp** — hvor lenge den gamle nøkkelen fortsetter å virke: ingen, 1 time,
   24 timer (standard), 3 dager eller 7 dager — og den nye nøkkelens **Utløp**.
3. Velg **Roter**. Den nye nøkkelen vises én gang, som da den ble laget. Den har den gamle
   nøkkelens navn, begrensning, innstilling for bare lesing og nettverk.
4. Legg den nye nøkkelen inn i programmet før overlappet er over. Den gamle nøkkelen utløper
   da av seg selv; til da er den merket **Rotert**.

En rotering lar den gamle nøkkelen virke gjennom overlappet. Har nøkkelen lekket, tilbakekaller
du den i stedet ([Når en nøkkel lekker](#når-en-nøkkel-lekker)): det sier dialogen også.

En nøkkel kan roteres én gang: å rotere den igjen avvises med `409 api_key_already_rolled`,
og en tilbakekalt eller utløpt nøkkel kan ikke roteres (`409 api_key_revoked`). Å lage og
rotere nøkler deler identitys grense for brukeradministrasjon (UserManagement) på 30
forespørsler i minuttet fra én adresse med de andre operasjonene for brukeradministrasjon.

## Tilbakekall en nøkkel

Velg **Tilbakekall** i nøkkelens rad og bekreft. Nøkkelen avvises med `401 unauthenticated`
fra neste forespørsel, på alle replikaer samtidig: ingenting mellomlagrer en nøkkel. Det kan
ikke angres; lag en ny nøkkel hvis programmet trenger en.

## Deaktiver, aktiver eller arkiver en integrasjon

Under **Handlinger**:

- **Deaktiver** avviser alle integrasjonens nøkler med en gang, til du velger **Aktiver**. Bruk
  det for å stoppe et program mens du finner ut hva det gjør.
- **Arkiver** tilbakekaller alle nøklene, deaktiverer alle [webhook-endepunktene](#webhooks),
  frigjør navnet og beholder integrasjonen, uten mulighet til å endre den, for det den har
  gjort. Det kan ikke angres.

## Når en nøkkel lekker

Kan en nøkkel ha blitt sett av noen som ikke skal ha den — lagt inn i et kodelager, limt inn i
en sak, liggende på en bærbar maskin:

1. **Tilbakekall** den med en gang. Vet du ikke hvilken nøkkel som lekket, **Deaktiver**
   integrasjonen først: det stopper alle nøklene mens du leter.
2. Lag en ny nøkkel, eller **Roter** de andre programmet bruker, og legg dem inn i programmet.
   Aktiver integrasjonen igjen hvis du deaktiverte den.
3. Sjekk hva integrasjonen gjorde: det den laget og endret, er ført på den, og identitys
   revisjonsspor registrerer alle endringer av integrasjonen og nøklene (nedenfor). Fanen
   **Forespørsler** ([Forespørselsloggen](#forespørselsloggen)) viser hva hver nøkkel har kalt de
   siste 30 dagene, fra hvilken adresse, og hva som ble avvist; filtrer den på nøkkelen.
   Nøkkelens **Sist brukt** sier når, og fra hvilken adresse, den sist ble brukt.
4. Hadde nøkkelen sensitive tillatelser, regn dataene de når, som eksponert.

## Hvordan en klient sender nøkkelen

Programmet sender nøkkelen i `Authorization`-headeren i hver forespørsel, med ordningen
`Bearer`:

```sh
curl -H "Authorization: Bearer vtg_…" https://vantigo.example.com/api/v1/customers
```

- En nøkkel leses bare fra den headeren — aldri fra en spørrestreng eller en informasjonskapsel
  — og en forespørsel må ha én `Authorization`-header: flere avvises med
  `401 unauthenticated`.
- En forespørsel med en nøkkel vurderes som nøkkelens alene, også når nettleseren i tillegg
  sender en øktkapsel, og en avvist nøkkel logger aldri en person ut.
- En nøkkel begynner med `vtg_` og har 53 tegn, med en kontrollsum, så en hemmelighetsskanner
  kan kjenne den igjen, og en feilskrevet nøkkel avvises uten et databaseoppslag.
- En nøkkel når bare operasjoner bak en tillatelse eller åpne for alle; aldri en persons egen
  konto, en eiers administrasjon, SSO, SCIM eller nøklene selv. Hver operasjon som aldri er
  tilgjengelig for en nøkkel, sier det i den genererte API-referansen (`/nb/reference/api/`).

## Hastighetsgrensen

Hver forespørsel integrasjonens nøkler gjør, telles mot integrasjonens **Hastighetsgrense**, per
minutt, til sammen for alle nøklene: flere nøkler gir ikke høyere grense. En forespørsel telles
når nøkkelen er sluppet gjennom, så en avvist nøkkel bruker aldri opp integrasjonens kvote.
Grensene identitet og modulene setter per adresse på enkeltoperasjoner — innlogging, å lage
nøkler — gjelder fortsatt i tillegg.

Hvert svar på en forespørsel en nøkkel ble sluppet gjennom for, har tre hoder:

| Hode | Sier |
| --- | --- |
| `RateLimit-Limit` | Integrasjonens grense i minuttet |
| `RateLimit-Remaining` | Hvor mange forespørsler til dette minuttet tillater |
| `RateLimit-Reset` | Sekundene til minuttet er over og tellingen starter på nytt |

En forespørsel over grensen avvises med `429 rate_limited` og `Retry-After`, sekundene det skal
ventes. Et program bør senke farten når `RateLimit-Remaining` nærmer seg null, og vente ut
`Retry-After` når det får `429`.

Tellingen holdes i databasen, så alle replikaer ser den samme. Hver replika legger sine egne
forespørsler til den hvert sekund, enten det kommer flere eller ikke, og en gang til når den
stenges ned, så den ligger høyst om lag ett sekund av sine egne forespørsler etter. Et
arbeidsområde med flere replikaer kan derfor slippe gjennom opptil ett sekunds forespørsler mer
enn grensen før alle avviser.

## Prøv API-et på /api

Vantigo serverer en interaktiv referanse for API-et sitt på `/api` på applikasjonens egen
adresse, for eksempel `https://vantigo.example.com/api`. Åpne den i en nettleser:

1. **Innlogget, uten nøkkel,** viser den alle operasjoner en API-nøkkel kan kalle, med
   tillatelsen hver av dem krever. Et kall du prøver der med **Test Request**, går med din
   egen økt og vurderes som ditt kall, slik siden sier over listen.
2. **For å se hva en nøkkel når,** lim den inn i **API-nøkkel** og velg **Bruk nøkkel**.
   Listen skjæres ned til operasjonene nøkkelen kan kalle — for en nøkkel for bare lesing
   til lesingene — og **Test Request** sender nøkkelen, så kallet vurderes, avvises og
   logges som nøkkelens.
3. **Tøm** (eller å tømme feltet) setter siden tilbake til økten din.
4. **Verken innlogget eller med nøkkel** forteller siden hvordan du får en nøkkel, og lenker
   til [introduksjonen til API-et](/en/reference/api-introduction/).

Nøkkelen blir i siden: den lagres ikke i nettleseren, sendes ikke noe annet sted enn til
denne installasjonen, og er borte når du går herfra eller tømmer feltet. En nøkkel som
avvises ved **Bruk nøkkel**, navngis med koden sin (se nedenfor), og en avvist nøkkel logger
deg aldri ut. Ingenting på siden hentes utenfra installasjonen. Siden serveres ikke på den
offentlige verten. **Test Request** virker bare når siden er åpnet på installasjonens egen
adresse, `APP_URL`: dokumentet oppgir den adressen som tjener, og sidens
innholdssikkerhetspolicy avviser forespørsler til alle andre opphav.

Et program kan hente det samme dokumentet selv: `GET /api/openapi.json` med nøkkelen i
`Authorization`-headeren svarer med OpenAPI-dokumentet for det nøkkelen når, klart for en
klientgenerator; en økt som ber om `?view=api`, får alle operasjoner en nøkkel kan kalle
([API-kontraktdokumentet](/nb/admin/transport-security/#api-kontraktdokumentet)).
Å hente det med en nøkkel teller mot integrasjonens hastighetsgrense og vises i
forespørselsloggen, som ethvert annet kall.

## MCP

En AI-klient — Claude Code, Cursor, VS Code, n8n eller en egen agent — kan koble seg til
Vantigo med [Model Context Protocol](https://modelcontextprotocol.io/) og en av en integrasjons
nøkler. Den finner, beskriver og kaller da operasjonene den nøkkelen kan kalle, og ingenting
mer: hvert kall går gjennom de samme kontrollene som en REST-forespørsel med nøkkelen, teller
mot integrasjonens hastighetsgrense og vises i forespørselsloggen. Endepunktet er `/api/mcp` på
applikasjonens egen adresse; kontrakten står i
[referansen for MCP-serveren](/en/reference/mcp/) (på engelsk).

En agent handler på det den leser, og en post — et notat på en kunde, en e-post, en artikkel —
kan inneholde tekst skrevet for å villede den. Gi en agent en egen integrasjon, med bare de
rollene den trenger, og en nøkkel med bare lesing med mindre den må endre data.

**Før du begynner:** en integrasjon med rollene agenten trenger. MCP er av for hver
integrasjon til en eier slår det på.

### Velg hva agenten kan gjøre

1. Åpne integrasjonen under **Administrer arbeidsområde › API-tilgang** og bli på **Oversikt**.
2. Under **MCP** velger du **MCP-tilgang**:
   - **Av** — integrasjonens nøkler avvises ved MCP-endepunktet. Standardvalget.
   - **Bare lese** — en AI-klient kan søke i API-et og kalle operasjoner som leser.
   - **Lese og skrive** — en AI-klient kan også kalle operasjoner som endrer data: opprette,
     endre og sende, for eksempel en faktura på e-post (verktøyet `write`), og slette (verktøyet
     `delete`). En nøkkel med bare lesing leser alltid bare, uansett valg.

**Bare lese begrenser MCP, ikke nøkkelen.** Med **Bare lese** kan en AI-klient ikke endre noe
gjennom MCP, men selve nøkkelen er den samme: har den ikke bare lesing, kan den som har den,
fortsatt skrive gjennom REST-API-et så langt integrasjonens roller rekker. Bare en nøkkel med bare
lesing sikrer at den bare leser. For en agent som bare skal slå opp, velg **Bare lese** og gi den
en nøkkel med bare lesing.

**Skrivetilgang.** Med **Lese og skrive** kan en agent endre og slette alt integrasjonens roller
tillater, og en post den leser, kan prøve å overtale den til en endring. Gi integrasjonen bare
rollene endringen trenger. Klienter som Claude Code, Cursor og VS Code spør deg før de kjører et
verktøy med mindre du har tillatt det: ikke godkjenn `write` eller `delete` automatisk. Sletting
har et eget verktøy, så du kan tillate endringer og likevel bli spurt før hver sletting.

### Koble til en klient

Tilkoblingen inneholder aldri nøkkelen, så den kan ligge i prosjektets filer uten å lekke den:
Claude Code og Cursor leser nøkkelen fra miljøvariabelen `VANTIGO_API_KEY`, og VS Code spør etter
den én gang og tar vare på den i sitt eget hemmelighetslager. Legg aldri en nøkkel i en fil du
committer.

1. Lag en nøkkel til agenten under **API-nøkler** ([Lag en nøkkel](#lag-en-nøkkel)). Slå på
   **Bare lesing** med mindre den må skrive. Når integrasjonens MCP er på, viser dialogen for
   den nye nøkkelen, under `curl`-eksempelet, **Koble til en AI-klient (MCP)**: kommandoen for
   Claude Code og blokkene for Cursor og VS Code. Mens MCP er **Av**, sier den at det må slås på
   først, og viser ingen tilkobling.
2. Kopier nøkkelen inn i passordbehandleren din eller maskinens hemmelighetslager før du velger
   **Jeg har kopiert nøkkelen** — den vises ikke igjen.
3. Koble til klienten:
   - **Claude Code:** sett nøkkelen i skallet, og kjør så kommandoen i prosjektet agenten arbeider
     i. Claude Code tar vare på headeren i din egen `~/.claude.json`, ikke i prosjektet:

     ```sh
     export VANTIGO_API_KEY=vtg_…
     claude mcp add --transport http vantigo https://vantigo.example.com/api/mcp \
       --header "Authorization: Bearer $VANTIGO_API_KEY"
     ```

   - **Cursor:** legg `mcpServers`-blokken inn i `.cursor/mcp.json` i prosjektet eller
     `~/.cursor/mcp.json`. Headeren leser `${env:VANTIGO_API_KEY}`, så sett variabelen der
     Cursor startes.
   - **VS Code:** legg blokken, med `inputs`-oppføringen, inn i `.vscode/mcp.json`. Første gang
     serveren starter, spør VS Code etter nøkkelen i et passordfelt.

   Seksjonen **MCP** på **Oversikt** viser den samme kommandoen og de samme blokkene, for en
   nøkkel som er laget tidligere.
4. Start klienten på nytt eller last den inn igjen, så den leser den nye serveren.

**Slik kontrollerer du:** be klienten liste verktøyene sine. Den viser `search_operations`,
`describe_operation` og `read`, og `write` og `delete` med **Lese og skrive** for en nøkkel uten
bare lesing. Hvert kall av `read`, `write` eller `delete` vises to ganger i integrasjonens fane
**Forespørsler**: som operasjonen den kalte, som teller mot hastighetsgrensen, og som
`postApiMcp` for MCP-forespørselen rundt, som ikke gjør det; de to har samme sporings-ID. Alle
andre MCP-forespørsler er én rad `postApiMcp` og teller én gang.

Endringen revideres som `integration.mcp_changed`, og hvis noen andre har endret integrasjonen
i mellomtiden, avvises den med beskjed om å laste inn på nytt, som for detaljene.

### Slå av MCP for installasjonen

`MCP_ENABLED=0` ([Konfigurasjon](/nb/admin/configuration/)) slår av endepunktet for hele
installasjonen: `/api/mcp` svarer `404` for hver nøkkel, uansett hva integrasjonen er satt
til, og seksjonen **MCP** sier fra. Slå det på igjen med `MCP_ENABLED=1` (standard) og en
omstart.

### Når en klient ikke kommer inn

- **`401 api_key_required`:** klienten sender ingen nøkkel, eller en nøkkel Vantigo ikke
  kjenner. Kontroller headeren `Authorization` i klientens oppsett: `Bearer` og hele nøkkelen.
- **`401 unauthenticated`:** nøkkelen er tilbakekalt, utløpt, deaktivert eller arkivert, eller
  brukt utenfor nettverkene sine; fanen **Forespørsler** viser hvilket, under `postApiMcp`.
- **`403 mcp_not_enabled`:** integrasjonens MCP-tilgang er **Av**.
- **`404`:** MCP er slått av for installasjonen, eller adressen er ikke applikasjonens egen
  (`APP_URL`, med basestien).
- **Et verktøy svarer at en operasjon ikke er tilgjengelig for denne nøkkelen:**
  integrasjonens roller, nøkkelens begrensning eller bryteren for bare lesing når den ikke,
  eller den endrer data og MCP-tilgangen er **Bare lese**.
- **Klienten har ikke verktøyet `write` eller `delete`, eller det avvises som ukjent:**
  MCP-tilgangen er **Bare lese**, eller nøkkelen har bare lesing. Tilgangen kontrolleres ved
  hvert kall, så en senkning virker straks, også i en klient som listet `write` før.
- **En endring svarer `409` (eller `412`):** posten er endret siden agenten leste den; agenten
  leser den på nytt og prøver igjen med den nye versjonen, slik en REST-klient ville gjort.
- **En endring svarte ikke i tide:** den kan ha blitt gjort, eller ikke. Der kallet sendte en
  `Idempotency-Key`, prøver agenten igjen med samme nøkkel og får det første svaret igjen i
  stedet for en endring til; der det ikke sendte noen, leser agenten posten før den prøver igjen.
  Verktøyene ber agenten lage én tilfeldig nøkkel per handling, aldri en ny nøkkel for et nytt
  forsøk og aldri en nøkkel hentet fra dataene i arbeidsområdet
  ([MCP-server](/en/reference/mcp/#the-idempotency-key)).
- **Et kall svarer at nøkkelen har for mange kall i gang:** en nøkkel har høyst fire
  MCP-kall i gang samtidig; klienten prøver igjen om litt.
- **En klient som kjører i nettleseren** kommer ikke inn: Vantigo avviser forespørsler fra
  andre nettsteder i nettleseren.

## Koble til ved å logge inn

Claude Code, Claude og ChatGPT kan koble seg til uten at en nøkkel kopieres noe sted: du gir
klienten MCP-adressen, den sender nettleseren din til Vantigo, du logger inn som eier, velger
integrasjonen klienten kan handle som, og godkjenner. Klienten har da en **tilkobling** som
handler som den integrasjonen, med rollene, MCP-tilgangen og hastighetsgrensen dens, som én
nøkkel til; den fornyer selv tokenet sitt, som varer én time, og ser aldri en nøkkel. Kontrakten
er [OAuth connections](/en/reference/oauth/).

**Før du begynner:**

- En integrasjon med rollene klienten trenger og **MCP-tilgang** satt til **Bare lese** eller
  **Lese og skrive** ([Velg hva agenten kan gjøre](#velg-hva-agenten-kan-gjøre)). En med
  MCP-tilgang **Av**, eller som er deaktivert, kan ikke velges.
- Du er **eier**. Når installasjonen krever flerfaktorautentisering for administratorer, har du
  tatt den i bruk; hvis ikke sender Vantigo deg dit for å ta den i bruk og så tilbake.
- Serveren når klientens vert over **utgående HTTPS** — `claude.ai`, `chatgpt.com` eller verten
  til den klienten som kobler seg til. Vantigo henter klientens publiserte beskrivelse derfra når
  du blir bedt om å godkjenne den, aldri fra en privat eller intern adresse.
- **Claude og ChatGPT på nettet** kaller installasjonen fra sine egne servere, så den må ha en
  offentlig `https`-adresse. **Claude Code** kjører på datamaskinen din og kan koble seg til enhver
  adresse den når, også en utviklingsinstans på `http://localhost`.
- Med en basesti sendes `/.well-known/oauth-*` til Vantigo
  ([Installasjon](/nb/admin/installation/#basesti-og-omvendt-proxy)).

### Koble til Claude Code

1. Legg til serveren uten nøkkel, i prosjektet agenten jobber i:

   ```sh
   claude mcp add --transport http vantigo https://vantigo.example.com/api/mcp
   ```

2. Kjør `/mcp` i Claude Code, velg **vantigo** og autentiser. Nettleseren åpner Vantigos
   samtykkeside; logg inn først hvis du blir bedt om det.
3. Godkjenn som beskrevet under [Samtykkesiden](#samtykkesiden). Nettleseren går tilbake til
   Claude Code, som sier at den er koblet til.

### Koble til Claude eller ChatGPT

1. Legg til en egendefinert connector (Claude) eller app (ChatGPT) i klientens innstillinger,
   med MCP-adressen, `https://vantigo.example.com/api/mcp`, og OAuth som autentisering.
2. Klienten sender nettleseren til Vantigos samtykkeside; logg inn først hvis du blir bedt om det.
3. Godkjenn som beskrevet under [Samtykkesiden](#samtykkesiden). Nettleseren går tilbake til
   klienten.

**Slik kontrollerer du:** be klienten liste verktøyene sine, som for en nøkkel
([Koble til en klient](#koble-til-en-klient)), og åpne integrasjonen: klienten står under
**Tilkoblede apper** på **Oversikt**, og kallene dens står i fanen **Forespørsler**, med navnet
appen kaller seg og merket *(tilkoblet app)*.

### Samtykkesiden

Siden viser hva klienten ber om, og hva Vantigo kunne bekrefte om den:

- **Appen kaller seg** — navnet fra klientens egen beskrivelse. Hvem som helst kan publisere
  hvilket som helst navn, så siden viser ved siden av det Vantigo har sjekket: **verten
  beskrivelsen ble lest fra** og **verten nettleseren går til** når du har svart. Stol på
  vertene, ikke på navnet.
- En advarsel når navnet nevner Vantigo eller arbeidsområdets navn.
- En advarsel når svaret går til **en app på denne datamaskinen** (en loopback-adresse, slik
  Claude Code bruker): hvilket som helst program på maskinen kan stå bak forespørselen, så
  godkjenn bare en tilkobling du selv nettopp startet.
- En merknad når ingen app fra den verten har vært koblet til arbeidsområdet før.
- **Integrasjon** — ingen er valgt for deg. En integrasjon som er deaktivert eller har MCP av,
  vises, men kan ikke velges.
- **Tilgang** — **Bare lese** med mindre du velger **Lese og skrive**, som du bare kan når
  klienten ba om å skrive og integrasjonens MCP-tilgang er **Lese og skrive**.

**Godkjenn** lager tilkoblingen og sender nettleseren tilbake til klienten med en kode den bytter
inn innen ett minutt; **Avslå** sender den tilbake med et avslag. En tilkobling varer til den
tilbakekalles, og høyst 180 dager; da ber klienten deg logge inn igjen.

Hva siden ellers kan si:

- **Spør en eier** — du er logget inn, men er ikke eier. Ingenting sendes tilbake til klienten;
  en eier må lage tilkoblingen.
- **Denne appen kan ikke kobles til** — Vantigo kunne ikke hente eller godta klientens
  beskrivelse, eller adressen den vil tilbake til, er ikke en den har publisert. Ingenting sendes
  tilbake: forespørselen kommer kanskje ikke fra klienten den nevner.
- **Forespørselen fra appen er ugyldig** med **Tilbake til *vert*** — klienten sendte en
  forespørsel Vantigo avviser, for eksempel en uten PKCE. Knappen gir klienten beskjed; Vantigo
  går tilbake til en klients vert uten et klikk bare når en klient fra den verten har vært koblet
  til her før.
- **Tilkobling av apper ved innlogging er slått av** — `OAUTH_ENABLED` er `0`.

Siden kan ikke vises inne i en ramme på et annet nettsted.

### Tilkoblede apper

Integrasjonens **Oversikt** viser de tilkoblede appene under **MCP**: navnet hver klient kaller
seg og verten dens, hvem som koblet den til, **Bare lese** eller **Lese og skrive**, når den ble
koblet til, siste bruk og adresse, og når den **slutter** av seg selv. En tilkobling der personen
ikke lenger er en aktiv eier — fratatt rollen, deaktivert, slettet eller deaktivert av
katalogen — er **Inaktiv**: tokenene avvises. Tilbakekall den, og koble klienten til på nytt hvis
den fortsatt trengs.

Hver bærbar maskin som kjører Claude Code, er en egen tilkobling; å tilbakekalle én lar de andre
være.

**Slik tilbakekaller du en tilkobling:** velg **Tilbakekall** på raden og bekreft. Klienten
avvises fra neste forespørsel, på alle servere på én gang, og må kobles til på nytt ved
innlogging. Tilbakekallingen revideres som `connection.revoked`.

### Hva som avslutter en tilkobling av seg selv

- Integrasjonen **arkiveres**.
- Personen som godkjente den, slettes, deaktiveres eller er ikke lenger eier.
- Det går **180 dager** fra den ble godkjent.
- Klienten viser fram en kode eller et oppdateringstoken en gang til etter at det er brukt, som
  betyr at noen andre kan ha det.

En deaktivert integrasjons tilkoblinger avvises mens den er deaktivert, og virker igjen når den
aktiveres, slik nøklene dens gjør.

### Slå det av

`OAUTH_ENABLED=0` ([Konfigurasjon](/nb/admin/configuration/)) og en omstart slår tilkobling ved
innlogging av for hele installasjonen: hver OAuth-adresse svarer `404`, samtykkesiden sier at det
er slått av, hver tilkoblings token avvises, og **Tilkoblede apper** vises ikke. Tilkoblingene
beholdes og virker igjen når det slås på igjen med `OAUTH_ENABLED=1`, som er standard. Nøkler
berøres ikke.

### Når en tilkobling ikke virker

- **Klienten sier at innloggingen mislyktes, eller åpner aldri nettleseren:** sjekk at serveren
  når klientens vert over HTTPS, og, for Claude eller ChatGPT på nettet, at installasjonens
  adresse er offentlig `https`.
- **Denne appen kan ikke kobles til:** klientens beskrivelse kunne ikke hentes eller ble avvist;
  årsaken står under overskriften. Prøv igjen senere, eller koble klienten til med en nøkkel.
- **Klienten ble koblet til, men kallene avvises:** integrasjonens MCP-tilgang er **Av**, den er
  deaktivert, eller tilkoblingen er **Inaktiv**. Fanen **Forespørsler** viser avvisningen.
- **En endring avvises med `insufficient_scope`:** tilkoblingen ble godkjent for **Bare lese**.
  Tilbakekall den og koble til på nytt med **Lese og skrive**.

## Svarene en nøkkel kan få

| Status | Kode | Når |
| --- | --- | --- |
| `401` | `unauthenticated` | Nøkkelen er feilformet, ukjent eller tilbakekalt, eller integrasjonen er deaktivert eller arkivert; forespørselen kom utenfra nøkkelens nettverk; eller den hadde flere `Authorization`-headere |
| `401` | `api_key_expired` | Nøkkelen passet, innenfor nettverkene sine, men er utløpt |
| `403` | `forbidden` | En persons eller en eiers operasjon, eller en tillatelse nøkkelen ikke når: ikke i rollene, aldri tilgjengelig for API-nøkler eller utenfor begrensningen |
| `403` | `api_not_available` | Operasjonen er aldri tilgjengelig for API-nøkler |
| `403` | `api_key_read_only` | En nøkkel for bare lesing på en operasjon som ikke er en lesing |
| `429` | `rate_limited` | Integrasjonen har gjort så mange forespørsler som **Hastighetsgrense** tillater dette minuttet; `Retry-After` sier hvor lenge det skal ventes |

En forespørsel sendt med en `Idempotency-Key` kan også få `400 idempotency_key_invalid`,
`409 idempotency_in_progress`, `409 idempotency_outcome_unknown`, `422 idempotency_key_reused` eller
`429 idempotency_quota` ([Prøv på nytt uten å gjøre noe to ganger](#prøv-på-nytt-uten-å-gjøre-noe-to-ganger)).

En `401` har `WWW-Authenticate: Bearer realm="vantigo"`. Innholdet er den samme JSON-feilen API-et
svarer personer med: `{"error": {"code": "…", "message": "…"}}`.

## Forespørselsloggen

Integrasjonens side har fanen **Forespørsler**: hva nøklene har kalt de siste 30 dagene. Hver
forespørsel en nøkkel gjorde som Vantigo kunne knytte til nøkkelen, står der, også de som ble
avvist; en forespørsel med en nøkkel som ikke passet noen nøkkel, står ikke der, fordi det ikke
er noe å knytte den til. En AI-klients forespørsel ved MCP-endepunktet er operasjonen
`postApiMcp`, og hvert `read` eller `write` den gjør, er en rad til, operasjonen den kalte
([MCP](#mcp)).

- **Forespørsler per dag** teller dem per dag og statusklasse — *2xx vellykket*, *3xx
  videresending*, *4xx avvist* og *5xx serverfeil* — som et diagram og en tabell. Dagene er
  UTC-døgn.
- **Mest brukt** viser de ti operasjonene som er kalt mest.
- **Siste forespørsler** viser de 200 nyeste: når, i UTC, nøkkelen, metoden og operasjonen, statusen
  — med årsaken når den ble avvist — hvor lang tid den tok, klientens adresse og sporings-ID-en.
  En nøkkel med slettet rad, 90 dager etter at den utløp, vises som *Slettet nøkkel*. En
  forespørsel en tilkoblet app gjorde, viser appen slik den kalte seg selv, merket *(tilkoblet
  app)*, eller *Fjernet app* når tilkoblingens rad er borte.
- **Nøkkel**, **Status**, **Årsak** og **Dag** filtrerer alle tre.

En avvisning registreres med årsaken, og det gjør et gjentatt svar også:

| Årsak | Forespørselen |
| --- | --- |
| *Utløpt* (`expired`) | ble avvist fordi nøkkelen hadde utløpt |
| *Tilbakekalt* (`revoked`) | ble avvist fordi nøkkelen var tilbakekalt |
| *Integrasjonen er deaktivert* (`disabled`) | ble avvist fordi integrasjonen var deaktivert |
| *Integrasjonen er arkivert* (`archived`) | ble avvist fordi integrasjonen var arkivert |
| *Utenfor nettverkene sine* (`network`) | ble avvist fordi den kom fra utenfor nøkkelens **Nettverk** |
| *Nøkkelen er bare for lesing* (`read_only`) | ble avvist fordi en nøkkel for bare lesing kalte en operasjon som ikke er en lesing |
| *Ikke tilgjengelig for API-nøkler* (`never`) | ble avvist fordi operasjonen er aldri tilgjengelig for API-nøkler |
| *Ikke tillatt av rollene* (`forbidden`) | ble avvist fordi rollene, eller begrensningen, ikke gir det operasjonen trenger |
| *Over grensen for forespørsler* (`rate_limited`) | ble avvist fordi integrasjonen hadde gjort så mange forespørsler som **Hastighetsgrense** tillot det minuttet |
| *Den tilkoblede appen kan bare lese* (`insufficient_scope`) | en tilkobling godkjent for bare lesing kalte en operasjon som ikke er en lesing ([Koble til ved å logge inn](#koble-til-ved-å-logge-inn)) |
| *Den tilkoblede appen har ikke lenger tilgang* (`token_invalid`) | en tilkobling der den som godkjente, ikke lenger er en aktiv eier, eller som aldri ble fullført, viste tokenet sitt |
| *Gjentatt svar for Idempotency-Key* (`idempotent_replay`) | ble ikke kjørt: den gjentok en tidligere forespørsel med samme `Idempotency-Key` og fikk svaret på den igjen ([Prøv på nytt uten å gjøre noe to ganger](#prøv-på-nytt-uten-å-gjøre-noe-to-ganger)) |

En tilbakekalt nøkkel, en deaktivert eller arkivert integrasjon og en forespørsel fra utenfor
nettverkene får alle `401 unauthenticated`, slik en ukjent nøkkel gjør, så klienten får ikke
vite noe om nøkkelen; loggen sier hva det var. De andre får koden i
[Svarene en nøkkel kan få](#svarene-en-nøkkel-kan-få).

Loggen inneholder operasjonen, aldri stien, en spørrestreng eller en kropp, fordi de kan
inneholde personopplysninger. Sporings-ID-en finner forespørselen i serverens egne spor og
logglinjer. Radene skrives samlet annethvert sekund, så en forespørsel vises i fanen noen
sekunder etter at den ble gjort; en server som stopper brått, kan miste de to siste sekundene.
Når databasen ikke holder følge, droppes rader i stedet for at forespørsler går tregere, og de
telles i metrikken `vantigo.identity.api_requests.dropped`. Loggen er en driftslogg, ikke
revisjonssporet.

Loggen beholder 30 dager, i dag medregnet: eldre rader slettes av arbeideren `identity.api-keys`
([Konfigurasjon](/nb/admin/configuration/#arbeidere)). Er arbeideren slått av, slettes ingenting
fra loggen.

## Prøv på nytt uten å gjøre noe to ganger

Et program som sender en opprettende forespørsel — en kunde, en faktura, en e-post — og ikke får
svar, kan ikke vite om den ble utført. Sender det forespørselen med en `Idempotency-Key`-header,
en verdi det finner på én gang for den ene handlingen, og samme nøkkel ved hvert nytt forsøk,
utfører Vantigo handlingen én gang og svarer hvert nytt forsøk med det første svaret, merket
`Idempotent-Replayed: true`. Det virker på hver `POST` en nøkkel kan kalle som oppretter eller
endrer noe i JSON, uten noe å slå på per integrasjon; [API-introduksjonen](/en/reference/api-introduction/#retrying-safely)
sier nøyaktig hvor, og hva hvert svar betyr. Dokumentet en nøkkel får på `/api/openapi.json`, og
referansen på `/api`, viser den valgfrie `Idempotency-Key`-headeren på hver av disse operasjonene,
og en agent koblet til over MCP får beskjed om å bruke den.

- Et gjentatt svar er en forespørsel: det teller mot integrasjonens **Hastighetsgrense** og står i
  fanen **Forespørsler** med årsaken *Gjentatt svar for Idempotency-Key*. Operasjonen ble ikke
  kjørt igjen, så den revideres én gang.
- En nøkkel hører til integrasjonen, på tvers av alle nøklene dens, og til operasjonen. En
  integrasjon har høyst 2 000 nøkler, eller 16 MB lagrede svar (en forespørsel som fortsatt
  kjører, teller som 256 KB), om gangen; over det får en ny nøkkel `429 idempotency_quota` til
  eldre nøkler utløper, og et nytt forsøk med en nøkkel den har, får fortsatt svar.
- Et gjentatt svar er det første svaret slik det var: operasjonens kontroller kjøres ikke på nytt
  for det, så et nytt forsøk innen de 24 timene får det første svaret selv om posten eller
  integrasjonens roller har endret seg siden.
- Når Vantigo ikke kan vite hva den første forespørselen gjorde — den feilet med en `5xx`, den
  tidsavbrøt, serveren startet på nytt — får et nytt forsøk med samme nøkkel
  `409 idempotency_outcome_unknown`, og operasjonen kjøres ikke igjen. La programmet lese posten
  det ville opprette, og sende handlingen på nytt med en ny nøkkel hvis den mangler.

Nøklene beholdes i 24 timer fra den første forespørselen, i `identity.idempotency_keys`, og det
første svaret — status, headere og innhold — lagres kryptert med en nøkkel avledet av
installasjonens `APP_SECRET`; forespørselen det svarer på, lagres bare som et nøklet
fingeravtrykk. Arbeideren `identity.api-keys` sletter utløpte nøkler hver syklus, uansett hva
`IDEMPOTENCY_ENABLED` sier og også når API-nøklenes eget vedlikehold er slått av
([Konfigurasjon](/nb/admin/configuration/#arbeidere)). Å slette en person sletter nøklene personen
sendte. Å anonymisere en kunde sletter nøklene til agentkjøringer for den kunden, og hindrer at
noe svar lagret før det gis igjen, siden hvert av dem kan nevne kunden: et nytt forsøk får
`409 idempotency_outcome_unknown`, og raden forsvinner når den utløper. En sikkerhetskopi inneholder nøklene fra de siste 24 timene; gjenopprettet utløper de som
vanlig. `IDEMPOTENCY_ENABLED=0` slår det av for hele installasjonen: headeren ignoreres da, hvert
nytt forsøk kjøres igjen, og verken dokumentet, referansen på `/api` eller MCP tilbyr headeren
lenger. Svaret på en forespørsel som sendte en nøkkel til en av disse operasjonene, har
`Idempotency-Key-Ignored: true`, så et program kan se det.

## Varsler om utløp

Vantigo sender et varsel om en nøkkel på e-post 14 dager før den utløper, 3 dager før, og på
dagen (UTC-døgn). Hvert går til integrasjonens **Kontaktperson**, hvis personens konto er
aktiv, og til hver aktiv eier, på hver mottakers språk, fra adressen invitasjoner kommer fra.
En kontaktperson med deaktivert konto blir stående på integrasjonen og hoppes over; eierne får
fortsatt varselet.

- Hvert varsel sendes én gang. En nøkkel som lages med mindre enn 14 dager igjen, får bare
  varslene som ennå ligger foran den.
- En nøkkel som er rotert eller tilbakekalt, får ingen: å rotere nøkkelen er det varselet ber om.
- Varselet nevner nøkkelen og integrasjonen ved navn, aldri selve nøkkelen, og sier at nøkkelen
  skal roteres under **Administrer arbeidsområde › API-tilgang** og den nye gis til programmet.
- Et varsel som ikke nådde noen — e-posten feilet, eller ingen mottaker er aktiv med en adresse
  e-post når fram til — prøves igjen ved neste kjøring. Et varsel som nådde noen, sendes ikke på
  nytt til dem det ikke nådde. Stopper serveren i øyeblikket mellom at et varsel registreres og
  sendes, sendes ikke det varselet: hvert går høyst én gang.

Varslene sendes av arbeideren `identity.api-keys`; er den slått av, sendes ingen.

## Webhooks

Et **webhook-endepunkt** er en HTTPS-adresse til programmet som Vantigo skal sende en kort,
signert melding til når noe skjer — en salgsordre bekreftet, en faktura forfalt. Meldingen
sier hvilken ressurs det gjelder og ingenting annet; programmet henter den med API-nøkkelen
sin. Hva meldingen inneholder, hvordan den signeres og hvordan programmet kontrollerer den,
står i referansen, [Webhooks](/en/reference/webhooks/) (på engelsk).

Meldingene sendes av arbeideren `identity.webhook-sender`, i løpet av sekunder etter hendelsen,
og en som feiler, prøves på nytt i omtrent to døgn ([Konfigurasjon](/nb/admin/configuration/#arbeidere)).
Hvert endepunkts **leveranselogg** viser hver melding og hvert forsøk de siste 30 dagene, og en
melding kan sendes på nytt derfra.

### Legg til et endepunkt

1. På integrasjonens side åpner du fanen **Webhooks** og velger **Nytt endepunkt**.
2. Oppgi **URL**: en `https://`-adresse på port 443 med et vertsnavn — ikke en IP-adresse,
   `localhost` eller et navn under `.local`, `.localhost` eller `.internal` — uten brukernavn,
   passord eller `#`-del. En spørrestreng er tillatt. Legg til en **Beskrivelse** om det hjelper.
3. Kryss av for **Hendelser**. De er gruppert etter modul, og bare de integrasjonens roller lar
   den motta, tilbys: en hendelse om en salgsordre krever for eksempel en rolle med tilgang til
   Ordre. Tilbys ingen, gir du integrasjonen en rolle først.
4. Velg **Opprett endepunkt**. **Signeringshemmeligheten** (`whsec_…`) vises **én gang**, med
   **Kopier**. Legg den der programmet kontrollerer meldingene, og velg **Jeg har kopiert
   hemmeligheten**. Vantigo oppbevarer den forseglet og viser den aldri igjen; går den tapt,
   roterer du den.
5. Vantigo sender det nye endepunktet en **testhendelse** med en gang, en `webhook.ping`-melding.
   Sjekk at programmet tok imot den og godtok signaturen.

Vantigo slår opp vertsnavnet når du lagrer. En adresse som peker til en privat, lokal eller på
annen måte reservert adresse, avvises (*The address resolves to an address Vantigo never sends
to.*), og det gjør også en som ikke finnes (*The host name does not resolve.*). Meldinger går
bare til offentlige adresser, kontrollert på nytt hver gang en sendes, så et program på deres
eget nettverk trenger en offentlig adresse eller en tunnel.

En integrasjon kan ha høyst 5 endepunkter, og arbeidsområdet 50. Fanen viser hvert endepunkts
fulle URL, spørrestrengen med — det eneste stedet den vises — hendelsene og statusen.

### Send en testhendelse

I raden til et aktivert endepunkt velger du **Handlinger › Send testhendelse**. *Testhendelsen er
lagt i kø* sier at en `webhook.ping`-melding er på vei; den sendes én gang, i løpet av sekunder,
uten nye forsøk, og teller aldri med når det avgjøres om endepunktet skal deaktiveres. Bruk den
til å sjekke URL-en, hemmeligheten og programmets kontroll av signaturen. Svarer programmet
`410 Gone`, deaktiveres endepunktet som for enhver melding. Testhendelsen vises i endepunktets
leveranselogg, merket **Test**.

### Les leveranseloggen

1. I endepunktets rad velger du **Leveranser**. **Leveranselogg** viser meldingene som er lagt i
   kø til endepunktet de siste **30 dagene**, nyeste først; eldre slettes. Velg **Last flere**
   for eldre sider, og **Tilbake til endepunktene** for å gå ut.
2. Snevre den inn med **Status** og **Hendelse**; hendelsen `webhook.ping` viser testhendelsene.
3. Hver rad har når meldingen ble lagt i kø, hendelsen, statusen, forsøkene — *3 av 8 (3 i alt)*
   er det tredje forsøket i en runde på 8 — siste svars statuskode og feil, og når den ble
   ferdig.
4. Velg **Vis** for å åpne en melding: **webhook-id**-en, **Innhold** den sender, og hvert forsøk,
   nyeste først, med når, statuskoden, hvor lang tid det tok, feilen, hvilke hemmeligheter som
   signerte, og, for et svar som ikke var `2xx`, begynnelsen av det programmet svarte. Den teksten
   er programmets, vist slik den kom, som ren tekst.

| Status | Hva den betyr |
| --- | --- |
| **Venter** | Venter på neste forsøk; raden sier når. |
| **Levert** | Programmet svarte `2xx`. |
| **Mislyktes** | Gitt opp: alle forsøkene feilet, endepunktet svarte `410 Gone`, det ble deaktivert eller integrasjonen arkivert mens meldingen ventet, eller den var fortsatt ikke levert etter 3 dager. Raden sier hvilken. |
| **Hoppet over** | Ikke sendt, fordi integrasjonens roller ikke lenger lar den motta hendelsen, eller endepunktet ikke lenger lytter etter den. Gi den rollen igjen, eller legg hendelsen til igjen, for å motta de neste. |

Feilen sier med et ord hvorfor et forsøk feilet:

| Feil | Hva du sjekker |
| --- | --- |
| **Tidsavbrudd** | Programmet svarte ikke innen 15 sekunder: svar med en gang og gjør arbeidet etterpå. |
| **Tilkoblingen ble avvist** | Ingenting lytter på adressen på port 443. |
| **Fant ikke verten** | Vertsnavnet finnes ikke i DNS. |
| **TLS feilet** | Sertifikatet gjelder ikke verten, er utløpt eller er ikke signert av en kjent utsteder. |
| **Blokkert adresse** | Verten peker nå til en privat eller reservert adresse, som Vantigo aldri sender til. |
| **Videresending** | Programmet svarte `3xx`; videresendinger følges ikke, så oppgi den endelige URL-en. |
| **Feilstatus** | Ethvert annet svar som ikke er `2xx`; koden og begynnelsen av svaret sier mer. |
| **For stort svar** | Hodene i svaret var for store. |
| **Annen feil** | Alt annet. Sier hvert forsøk dette, roterer du endepunktets hemmelighet. |

### Send en leveranse på nytt

Når programmet aldri behandlet en melding — det var nede, eller feilet og mistet det det
gjorde — sender du den igjen:

1. Finn meldingen i leveranseloggen — **Levert** eller **Mislyktes**, lagt i kø de siste 27
   dagene — og velg **Send på nytt**.
2. Bekreft. *Leveransen er lagt i kø igjen*: den sendes i løpet av sekunder, til endepunktets URL
   slik den er nå, med samme **webhook-id** og innhold og en ny signatur, og prøves på nytt etter den vanlige planen, opptil
   8 forsøk, hvis den feiler.
3. Raden sier **Venter**, så **Levert**; **Vis** viser de nye forsøkene over de gamle.

Programmet bør se bort fra en melding med en webhook-id det allerede har behandlet, så det er
trygt å sende på nytt — og det får ikke programmet til å handle på en melding to ganger. **Send
på nytt** tilbys bare mens endepunktet er aktivert og integrasjonen ikke er arkivert, for en
melding som ble lagt i kø for mindre enn 27 dager siden (så de nye forsøkene er over før den
slettes etter 30 dager) og er forsøkt færre enn 80 ganger i alt; en testhendelse sendes ikke på
nytt — send en ny. Vantigo avviser å sende på nytt:

- den samme meldingen en fjerde gang innen et minutt etter den første nye sendingen — *En
  leveranse sendes på nytt høyst 3 ganger innen et minutt etter den første*; vent til minuttet
  er gått;
- når integrasjonens roller ikke lenger lar den motta hendelsen — gi den rollen igjen;
- når endepunktet ikke lenger lytter etter hendelsen — *Endepunktet lytter ikke lenger etter
  denne hendelsen*; legg den til igjen med **Rediger endepunkt** hvis den skal sendes;
- når endepunktet er deaktivert i mellomtiden — aktiver det først.

*For mange forespørsler* betyr at du har sendt flere nye sendinger og andre endringer fra
adressen din dette minuttet enn Vantigo tillater; prøv igjen om litt.

### Endre, deaktiver eller slett et endepunkt

I endepunktets rad, under **Handlinger**:

- **Rediger endepunkt** endrer URL, beskrivelse og hendelser, etter de samme reglene. En ny URL
  beholder hemmeligheten. Har noen andre endret endepunktet etter at du åpnet det, avvises
  lagringen og fanen viser det siste; gjør endringen på nytt.
- **Deaktiver** stopper det når du har bekreftet: ingenting mer sendes til det, og meldingene
  som venter på det, forkastes. **Aktiver** starter det igjen med hendelsene som skjer fra da av,
  og sender det en testhendelse.
- **Slett** sletter endepunktet og leveransene til det når du har bekreftet. Det kan ikke
  angres.

Statusen sier hvorfor et endepunkt er deaktivert: av en eier, fordi det har feilet i 5 dager,
fordi det svarte `410 Gone`, eller fordi integrasjonen ble arkivert. Arkiverer du en
integrasjon, deaktiveres endepunktene; da kan de bare slettes. Deaktiverer du selve
integrasjonen, settes endepunktene på pause: ingenting sendes mens den er deaktivert, og når du
aktiverer den igjen, forkastes en melding som har vært klar til sending i mer enn 24 timer, og
resten sendes.

### Når Vantigo deaktiverer et endepunkt

En melding som feiler — ethvert svar unntatt `2xx`, ikke noe svar innen 15 sekunder, en adresse
som ikke kan nås — prøves på nytt etter 30 sekunder, 5 minutter, 30 minutter, 2 timer, 6 timer,
12 timer og 24 timer, og gis så opp. Mens meldingene til et aktivert endepunkt feiler, sier
statusen **Feiler**, med siden når og når det sist fikk en. Vantigo deaktiverer det:

- når det har feilet i **5 dager** og minst **5 meldinger** er gitt opp etter alle nye forsøk —
  statusen *Deaktivert: det har feilet i 5 dager*;
- med en gang når det svarer **`410 Gone`**, som sier at adressen ikke lenger er i bruk —
  statusen *Deaktivert: det svarte 410 Gone*.

Meldingene som venter på det, forkastes, og integrasjonens kontaktperson og alle eiere får
**én e-post**, på sitt eget språk, som nevner endepunktet ved adressens vert og sier hvorfor.
Fanen viser et banner, *Endepunktet er deaktivert: …*, med årsaken.

Slik starter du det igjen:

1. Rett opp programmet, eller endepunktets URL med **Rediger endepunkt**.
2. Velg **Aktiver** i banneret, eller **Handlinger › Aktiver**. Endepunktet starter på nytt uten
   feil talt med og får en testhendelse med en gang.
3. Sjekk at programmet tok imot testhendelsen. Feiler det igjen, deaktiveres endepunktet igjen,
   og en ny e-post sendes.

Hendelsene som skjedde mens det var deaktivert, sendes ikke: programmet henter det det gikk glipp
av, gjennom API-et.

### Roter en hemmelighet

Rotering gir et endepunkt en ny hemmelighet uten et øyeblikk der programmet ikke kan kontrollere
en melding.

1. I endepunktets rad velger du **Handlinger › Roter hemmeligheten**.
2. Velg **Overlapp** — hvor lenge den gamle hemmeligheten fortsetter å signere ved siden av den
   nye: 1 time, 24 timer (standard), 3 dager eller 7 dager.
3. Velg **Roter hemmeligheten**. Den nye hemmeligheten vises én gang, slik som da endepunktet
   ble laget.
4. Legg den inn i programmet før overlappet er over. Fram til da har hver melding en signatur
   fra hver hemmelighet, så programmet godtar den med hvilken som helst av dem; raden sier til
   når den forrige hemmeligheten signerer.

Roterer du igjen mens et overlapp pågår, slutter den eldre hemmeligheten med en gang, og
dialogen advarer om det. Kan en hemmelighet ha lekket, roterer du den med det korteste
overlappet.

## Hva som revideres

Identitys revisjonsspor for autorisasjon registrerer, med eieren som gjorde det og om hen brukte
MFA: `integration.created`, `integration.updated`, `integration.roles_changed`,
`integration.disabled`, `integration.enabled`, `integration.archived`, `api_key.created`,
`api_key.rolled`, `api_key.revoked` og `integration.mcp_changed`, med MCP-tilgangen før og
etter. En tilkoblet app revideres som `connection.authorized` når klienten fullfører
innloggingen, med klienten, verten, omfanget og integrasjonen, og som `connection.revoked` med
årsaken når den avsluttes — `revoked`, `integration_archived`, `authorizer_removed`,
`code_replayed` eller `refresh_reused`, de to siste som `system:oauth`. Ingen token eller kode
registreres noen gang. Arbeideren `identity.api-keys` registrerer
`api_key.expired` én gang for hver nøkkel som passerer utløpet, som `system:api-keys`, og
sletter en nøkkels rad 90 dager etter at den utløp; revisjonsradene blir værende
([Konfigurasjon](/nb/admin/configuration/#arbeidere)). Navn, roller, begrensningen, bare
lesing, nettverkene og utløpet registreres; en nøkkels hemmelighet og hash gjør aldri det.

For webhook-endepunkter registrerer det `webhook.created`, `webhook.updated`, `webhook.deleted`,
`webhook.enabled`, `webhook.disabled` (også én gang for hvert endepunkt en arkivering
deaktiverer, og som `system:webhooks` når Vantigo deaktiverer et), `webhook.secret_rolled`,
`webhook.test_sent` og `webhook.resent`, med meldingens id og hendelse. Av et endepunkts URL beholdes bare skjemaet og verten,
med et kort sammendrag som viser når resten endret seg: tjenester som Zapier og Slack har
tokenet sitt i stien. Signeringshemmeligheten registreres aldri.

## Hva som kan gå galt

- **Programmet får `401 unauthenticated` med en ny nøkkel.** Sjekk at det sender
  `Authorization: Bearer` og hele nøkkelen, at integrasjonen er *Aktiv*, og at adressen er
  innenfor nøkkelens **Nettverk** slik serveren ser den bak proxyen din.
- **Det får `403 forbidden`.** Rollene gir ikke tillatelsen, begrensningen utelater den, eller
  tillatelsen er aldri tilgjengelig for API-nøkler. Integrasjonens side viser hvilken.
- **Det får `429 rate_limited`.** Integrasjonen har gjort flere forespørsler dette minuttet enn
  **Hastighetsgrense** tillater, til sammen for alle nøklene. La programmet vente ut
  `Retry-After` og følge med på `RateLimit-Remaining`, eller øk grensen under **Handlinger ›
  Rediger detaljer**. **Mest brukt** i fanen **Forespørsler** viser hva forespørslene går til.
- **Det får `409 idempotency_outcome_unknown`.** Den første forespørselen med den
  `Idempotency-Key`-en endte uten et svar Vantigo kan gi igjen, eller kjører fortsatt etter to
  minutter. Finn ut av posten om den ble utført, og send den på nytt med en ny nøkkel hvis ikke.
- **Det får `422 idempotency_key_reused`.** Programmet sendte én nøkkel med to ulike
  forespørsler. Hver handling trenger sin egen nøkkel; bare et nytt forsøk på samme handling
  bruker den igjen.
- **En forespørsel står ikke i fanen Forespørsler.** Vent noen sekunder: radene skrives samlet.
  En forespørsel med en nøkkel som ikke passet noen nøkkel, registreres aldri; sjekk nøkkelen
  programmet sender.
- **Å lage en nøkkel svarer at utløpet er feil.** Datoen må være fram i tid og høyst 365 dager
  fram.
- **En nøkkel er tapt.** Den kan ikke vises igjen: roter den.
- **Et endepunkts URL avvises.** Den må være `https://` på port 443 med et vertsnavn som bare
  peker til offentlige adresser; et program på deres eget nettverk trenger en offentlig adresse
  eller en tunnel.
- **En hendelse tilbys ikke.** Integrasjonens roller gir ikke tillatelsen hendelsens ressurs
  leses med; referansen viser tillatelsen for hver hendelse.
- **En webhook-hemmelighet er tapt.** Den kan ikke vises igjen: roter den.
- **Programmet får ingen meldinger.** Send en testhendelse. Kommer den ikke fram, sjekker du at
  URL-en kan nås fra internett og svarer `2xx` innen 15 sekunder uten videresending, og at
  integrasjonen og endepunktet er aktivert. En driftsansvarlig kan sjekke at arbeideren
  `identity.webhook-sender` kjører ([Konfigurasjon](/nb/admin/configuration/#arbeidere)).
  Velg **Leveranser** på endepunktet: hvert forsøks feil og begynnelsen av programmets svar sier
  hva som gikk galt.
- **Programmet avviser signaturen.** Det må kontrollere den rå meldingskroppen før den tolkes,
  med hemmelighetens byte etter `whsec_` dekodet fra base64; under et overlapp ved rotering får
  det to signaturer og bør godta hvilken som helst av dem.
- **Et endepunkt er deaktivert.** Se [Når Vantigo deaktiverer et endepunkt](#når-vantigo-deaktiverer-et-endepunkt).
- **En melding står ikke i leveranseloggen.** Den slettes 30 dager etter at den ble lagt i kø;
  en hendelse som skjedde mens endepunktet var deaktivert, ble aldri lagt i kø.
- **Det finnes ingen Send på nytt.** Meldingen venter fortsatt eller ble hoppet over, er en
  testhendelse, ble lagt i kø for 27 dager siden eller mer, eller er forsøkt 80 ganger; eller
  endepunktet er deaktivert eller integrasjonen arkivert.
