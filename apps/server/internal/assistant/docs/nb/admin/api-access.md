---
title: API-tilgang
description: La et annet program bruke Vantigos API uten en persons økt — integrasjoner, rollene deres, API-nøkler med utløp, begrensning, bryter for bare lesing og nettverk, rotering og tilbakekalling av en nøkkel, hva en nøkkel aldri får, hvordan en klient sender den, svarene den kan få, og hva du gjør når en nøkkel lekker.
sidebar:
  order: 22
sources:
  - apps/server/internal/identity/integrations.go
  - apps/server/internal/identity/access_apikey.go
  - apps/server/internal/identity/apikey.go
  - apps/server/internal/identity/workers.go
  - apps/host/frontend/src/routes/workspace/api-access.tsx
  - apps/host/frontend/src/routes/workspace/api-access_.$integrationId.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-integration.tsx
  - apps/host/frontend/src/routes/workspace/-api-key-dialog.tsx
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
  tilgang**. En integrasjon kan bare ha egendefinerte roller: aldri **Owner**, **SystemAdmin**
  eller **User**. Lag en rolle for hvert program med nøyaktig det programmet trenger.
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
nøklene; standard er 600). Hastighetsgrensen lagres med integrasjonen, men håndheves ikke ennå.
Lagring erstatter beskrivelsen og kontaktpersonen med det skjemaet
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
4. Velg om du vil **Begrens nøkkelen** (nedenfor), slå på **Bare lesing** (nedenfor) og fyll
   inn **Nettverk** (nedenfor).
5. Velg **Opprett nøkkel**. Nøkkelen vises **én gang**, med **Kopier** og et `curl`-eksempel
   mot denne installasjonens adresse. Kopier den inn i programmets hemmelighetslager og velg
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

**Nettverk** lister adressene eller CIDR-områdene nøkkelen kan brukes fra, én per linje —
IPv4 eller IPv6, høyst 20. Tomt betyr hvor som helst. En forespørsel utenfra avvises med
`401 unauthenticated`, som en ukjent nøkkel, så avsenderen får ikke vite noe om nøkkelen, og
**Sist brukt** oppdateres ikke. Adressen er klientens slik serveren ser den gjennom
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

En nøkkel kan roteres én gang: å rotere den igjen avvises med `409 api_key_already_rolled`,
og en tilbakekalt eller utløpt nøkkel kan ikke roteres (`409 api_key_revoked`). Å lage og
rotere nøkler er begrenset til 30 i minuttet fra én adresse.

## Tilbakekall en nøkkel

Velg **Tilbakekall** i nøkkelens rad og bekreft. Nøkkelen avvises med `401 unauthenticated`
fra neste forespørsel, på alle replikaer samtidig: ingenting mellomlagrer en nøkkel. Det kan
ikke angres; lag en ny nøkkel hvis programmet trenger en.

## Deaktiver, aktiver eller arkiver en integrasjon

Under **Handlinger**:

- **Deaktiver** avviser alle integrasjonens nøkler med en gang, til du velger **Aktiver**. Bruk
  det for å stoppe et program mens du finner ut hva det gjør.
- **Arkiver** tilbakekaller alle nøklene, frigjør navnet og beholder integrasjonen, uten
  mulighet til å endre den, for det den har gjort. Det kan ikke angres.

## Når en nøkkel lekker

Kan en nøkkel ha blitt sett av noen som ikke skal ha den — lagt inn i et kodelager, limt inn i
en sak, liggende på en bærbar maskin:

1. **Tilbakekall** den med en gang. Vet du ikke hvilken nøkkel som lekket, **Deaktiver**
   integrasjonen først: det stopper alle nøklene mens du leter.
2. Lag en ny nøkkel, eller **Roter** de andre programmet bruker, og legg dem inn i programmet.
   Aktiver integrasjonen igjen hvis du deaktiverte den.
3. Sjekk hva integrasjonen gjorde: det den laget og endret, er ført på den, og identitys
   revisjonsspor registrerer alle endringer av integrasjonen og nøklene (nedenfor). Nøkkelens
   **Sist brukt** sier når, og fra hvilken adresse, den sist ble brukt.
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
  tilgjengelig for en nøkkel, sier det i den genererte API-referansen (`/en/reference/api/`).

## Svarene en nøkkel kan få

| Status | Kode | Når |
| --- | --- | --- |
| `401` | `unauthenticated` | Nøkkelen er feilformet, ukjent eller tilbakekalt, eller integrasjonen er deaktivert eller arkivert; forespørselen kom utenfra nøkkelens nettverk; eller den hadde flere `Authorization`-headere |
| `401` | `api_key_expired` | Nøkkelen passet, men er utløpt |
| `403` | `forbidden` | En persons eller en eiers operasjon, eller en tillatelse nøkkelen ikke når: ikke i rollene, aldri tilgjengelig for API-nøkler eller utenfor begrensningen |
| `403` | `api_not_available` | Operasjonen er aldri tilgjengelig for API-nøkler |
| `403` | `api_key_read_only` | En nøkkel for bare lesing på en operasjon som ikke er en lesing |

En `401` har `WWW-Authenticate: Bearer realm="vantigo"`. Innholdet er den samme JSON-feilen API-et
svarer personer med: `{"error": {"code": "…", "message": "…"}}`.

## Hva som revideres

Identitys revisjonsspor for autorisasjon registrerer, med eieren som gjorde det og om hen brukte
MFA: `integration.created`, `integration.updated`, `integration.roles_changed`,
`integration.disabled`, `integration.enabled`, `integration.archived`, `api_key.created`,
`api_key.rolled` og `api_key.revoked`. Arbeideren `identity.api-keys` registrerer
`api_key.expired` én gang for hver nøkkel som passerer utløpet, som `system:api-keys`, og
sletter en nøkkels rad 90 dager etter at den utløp; revisjonsradene blir værende
([Konfigurasjon](/nb/admin/configuration/#arbeidere)). Navn, roller, begrensningen, bare
lesing, nettverkene og utløpet registreres; en nøkkels hemmelighet og hash gjør aldri det.

## Hva som kan gå galt

- **Programmet får `401 unauthenticated` med en ny nøkkel.** Sjekk at det sender
  `Authorization: Bearer` og hele nøkkelen, at integrasjonen er *Aktiv*, og at adressen er
  innenfor nøkkelens **Nettverk** slik serveren ser den bak proxyen din.
- **Det får `403 forbidden`.** Rollene gir ikke tillatelsen, begrensningen utelater den, eller
  tillatelsen er aldri tilgjengelig for API-nøkler. Integrasjonens side viser hvilken.
- **Å lage en nøkkel svarer at utløpet er feil.** Datoen må være fram i tid og høyst 365 dager
  fram.
- **En nøkkel er tapt.** Den kan ikke vises igjen: roter den.
