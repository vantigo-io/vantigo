---
title: "Identitet, autentisering og utrulling"
description: "Lokale kontoer, OIDC-leverandøren som konfigureres i miljøet, e-post, proxy-tillit, nøkkelmaterialet som utledes fra APP_SECRET og migreringer."
sidebar:
  order: 20
sources:
  - apps/server/internal/identity
  - apps/server/internal/config
  - apps/server/cmd/vantigo
---
Vantigo autentiserer nettleser-SPA-en og API-et på samme opprinnelse med en
applikasjonsinformasjonskapsel utstedt av identitetsmodulen. API-et svarer med
JSON-problemer `401`/`403`, ikke med omdirigeringer til innlogging.

**Det finnes ikke noe antiforgery-tokenendepunkt og ingen `X-XSRF-TOKEN`-header.**
Forfalskede forespørsler på tvers av nettsteder (CSRF) avvises etter opprinnelse, ikke
med et token: serveren pakker hver `/api`-forespørsel inn i Gos
`http.CrossOriginProtection`, som avviser usikre nettleserforespørsler fra andre
nettsteder ved hjelp av `Sec-Fetch-Site` (med `Origin` sammenlignet mot `Host` som
reserve). `APP_URL` registreres som en klarert opprinnelse, slik at en proxy som skriver
om `Host` ikke gjør forespørsler fra samme opprinnelse til avvisninger; en avvisning
svarer `403` som et RFC 7807-problem, og en forespørsel uten noen av de to headerne — en
klient som ikke er en nettleser — slipper gjennom. Øktinformasjonskapsler er
`SameSite=Strict` som det andre laget.

Kjør SPA-en og API-et under én offentlig opprinnelse. En omvendt proxy kan betjene
begge stiene, men den må bevare `Host`-headeren: `X-Forwarded-Host` respekteres aldri,
og vertsfiltreringen utledes fra `APP_URL`. SPA og API på forskjellige opprinnelser er
ikke en støttet utrullingsform.

For statisk OIDC for arbeidsstyrken, SCIM 2.0, oppsett av leverandør og
gjenopprettingsprosedyren for operatører, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).

Hver innstilling dette dokumentet nevner leses av
[`apps/server/internal/config/config.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
der feltkommentarene er den autoritative referansen. Konfigurasjonen parses én gang
ved oppstart, og **alle** problemer rapporteres samtidig, slik at en feilkonfigurert
container feiler ved første oppstart med den komplette listen i stedet for én omstart
per feil.

## Informasjonskapsler

| Informasjonskapsel | Formål | Attributter |
| --- | --- | --- |
| `vantigo.session` | Tokenet for den autentiserte økten | `HttpOnly`, `SameSite=Strict`, avgrenset til basisstien |
| `vantigo.2fa` | Billetten for en ventende tofaktorinnlogging, 5 minutter | `HttpOnly`, `SameSite=Strict` |
| `vantigo.oidc` | OIDC-state, nonce og PKCE-verifikator, forseglet | `HttpOnly`, `SameSite=Lax` |
| `vantigo.identity.external` | Den validerte eksterne identiteten mellom tilbakekall og fullføring, forseglet | `HttpOnly`, `SameSite=Lax` |

Alle fire er `Secure` nøyaktig når `APP_URL` er en https-opprinnelse (en
`Secure`-informasjonskapsel når aldri fram til en http-opprinnelse). De to
OIDC-informasjonskapslene er `SameSite=Lax` med vilje: leverandørens omdirigering
tilbake er en toppnivånavigasjon fra et annet nettsted, som `Lax` slipper gjennom og
`Strict` ikke ville gjort. Begge er forseglet med AES-256-GCM under hvert sitt formål,
slik at en state-informasjonskapsel ikke kan spilles av på nytt som en ekstern
identitet.

En varig øktinformasjonskapsel (`rememberMe` i 2FA-steget) bærer den standard absolutte
levetiden som sin `Max-Age`; serveren håndhever likevel de strammere privilegerte
grensene per forespørsel, uansett hva informasjonskapselen sier.

## Første eier og lokale kontoer

Det finnes én bevisst engangsvei for oppstart, og den trenger ingen hemmelighet: på en
fersk installasjon går du til `/setup` og oppgir e-post, visningsnavn og passord for den
første eieren. Den kontoen blir installasjonens fulle administrator og holder både
`Owner` (alle rettigheter i alle moduler) og `SystemAdmin` (`/admin` og
vedlikeholdsmodus). Ingen konfigurasjon utpeker den, og ingen annen vei gir
`SystemAdmin`.

**Fullfør oppsettet før installasjonen kan nås av noen andre.** Den som sender inn
`/setup` først, eier installasjonen. Så lenge det ikke finnes noen eier, logger
prosessen en `WARN` ved oppstart som sier det; når eieren finnes, avviser oppsett og
bootstrap alle videre forespørsler.

`GET /api/v1/identity/bootstrap-status` er anonymt og returnerer
`{"available": true|false}`. `POST /api/v1/identity/bootstrap` er fortsatt tilgjengelig
for en orkestrert flyt:

```bash
curl -X POST https://vantigo.example.com/api/v1/identity/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","displayName":"Owner","password":"a strong password"}'
```

Hver endring i hvem som holder Eier — bootstrap, opprettelse av eier, aksept av
eierinvitasjon og nedgradering av eier — serialiseres på en rådgivende lås
(advisory lock) i transaksjonen, slik at samtidige bootstrap-forespørsler ikke begge
kan vinne. Oppsett og bootstrap blir utilgjengelige når den første eieren finnes.
Bootstrap-kontoen er en vanlig lokal passordkonto, og en lokal eier forblir
nødveien (break-glass) selv når OIDC for arbeidsstyrken er slått på.

**`BOOTSTRAP_OWNER_EMAIL` erstatter `/setup` med en invitasjon på e-post**, for en
utrulling som kan nås før en operatør rekker å logge inn. Så lenge den er satt og ingen
eier finnes, svarer `/setup` og `POST /api/v1/identity/bootstrap` med den samme 409
`bootstrap_unavailable` som en forbrukt bootstrap får, og hver oppstart sender en
eierinvitasjon til den adressen — én gang: en ventende invitasjon sendes ikke på nytt
før den utløper, og en utløpt invitasjon utstedes på nytt ved neste oppstart.
Invitasjonen følger den konfigurerte adressen, ikke en fast: endrer du
`BOOTSTRAP_OWNER_EMAIL` mens ingen eier finnes, trekkes invitasjonen som allerede er
utstedt til den forrige adressen tilbake, og den nye adressen får sin ved neste
oppstart, slik at en feilskrevet adresse ikke beholder installasjonens eneste gyldige
eier-token. En adresse som en konto allerede holder, inviteres aldri — i stedet logges
en advarsel. En mislykket sending trekker invitasjonen tilbake og logger feilen uten å
stoppe oppstarten; neste oppstart prøver på nytt. Den kontoen som aksepterer
invitasjonen, blir installasjonens Eier og SystemAdmin, nøyaktig slik `/setup` ville
opprettet den.

Installasjonens første eier holder `Owner` og `SystemAdmin`, og bootstrap-markøren
skrives, **uansett hvordan den kontoen opprettes** — det anonyme skjemaet på `/setup`
og en akseptert eierinvitasjon gir samme resultat.

Eiere inviterer `User`- eller `Owner`-kontoer fra `/settings`. Invitasjonstokener er
ugjennomsiktige og lagres bare som hasher. `INVITATION_LIFETIME` er som standard `168h`
(sju dager) og godtas fra `24h` til `720h`; en verdi utenfor det området gjør at
oppstarten feiler.

Passordgjenoppretting svarer generisk, slik at den aldri avslører om en adresse finnes,
og detaljer om passordpolicyen rapporteres først etter at tokenet er validert.

## MFA

MFA er en sekssifret TOTP-autentisator pluss engangs gjenopprettingskoder, og
passnøkler (WebAuthn) er en fullverdig andre faktor: en innlogging som verifiserte en
TOTP-kode, en gjenopprettingskode **eller** en passnøkkel registreres som MFA-verifisert
for den økten.

`OWNERS_REQUIRE_MFA` krever MFA for Owner- og SystemAdmin-policyene. Den er som standard
**på utenfor utvikling og av i utvikling**. Utenfor utvikling nekter prosessen å starte
med mindre den er på eller `OWNERS_ALLOW_INSECURE_NO_MFA=1` bevisst godtar å kjøre uten
— konfigurasjonen rapporterer:

```text
OWNERS_REQUIRE_MFA: must not be 0 outside development unless OWNERS_ALLOW_INSECURE_NO_MFA=1
(accepts that a compromised Owner or SystemAdmin password alone reaches every identity
and tenant control-plane endpoint)
```

Unntaket finnes for demoutrullinger; sett det bevisst, ikke som standard. Det krever
begge nøklene: `OWNERS_REQUIRE_MFA=0` slår kravet av, og
`OWNERS_ALLOW_INSECURE_NO_MFA=1` lar prosessen starte med det av. Bekreftelsen alene
endrer ingenting — kravet forblir på, og en administrator uten registrert autentisator
avvises av hver modul og hvert kontrollplan-endepunkt. `MFA_ISSUER` (standard
`Vantigo`) er etiketten autentisator-apper viser.

MFA-registrering og -status forblir tilgjengelige for en økt som ennå ikke har
registrert seg, slik at det å slå på kravet aldri låser ute kontoen som må aktivere det.
En slik økt bærer `mfaEnrollmentRequired: true` (GET `/session`, svarene på innlogging
og invitasjon, GET `/account/mfa`) når kontoen holder Owner eller SystemAdmin, kravet er
på og ingen autentisator er registrert. Frontend-en holder den økten på
`/settings/security`: alle andre innloggede sider omdirigerer dit, siden forklarer hva
som må gjøres, og aktivering av autentisatoren markerer økten som MFA-verifisert, slik
at tilgangen kommer tilbake uten ny innlogging.
Gjenopprettingskoder vises én gang. En MFA-autentisert eier kan tilbakestille en annen
eiers MFA, noe som avslutter den kontoens økter og krever ny registrering.

## Øktens levetid og tilbakekalling

Fire grenser håndheves på hver forespørsel. En privilegert grense kan ikke overstige sin
standardmotpart, ellers feiler oppstarten.

| Nøkkel | Standard | Betydning |
| --- | --- | --- |
| `SESSION_IDLE_TIMEOUT` | `8h` | Inaktivitetsvindu for en standardøkt |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | `2h` | Inaktivitetsvindu for en Owner- eller SystemAdmin-økt |
| `SESSION_ABSOLUTE_LIFETIME` | `24h` | Hard grense for en standardøkt, fra innlogging |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | `8h` | Hard grense for en Owner- eller SystemAdmin-økt |

Inaktivitetsvinduet glir, men skrivingen er begrenset: `last_seen_at` skrives om bare
når den er eldre enn `min(inaktivitet/4, 5 minutter)`, ikke på hver forespørsel. Bare
det å presentere legitimasjon på nytt starter en ny absolutt levetid.

**Tilbakekalling trer i kraft på aller neste forespørsel.** Hver forespørsel slår opp
øktinformasjonskapselen til en rad i `identity.sessions` og evaluerer operasjonens
tilgangsregel mot den raden og brukerens nåværende roller — det finnes ingen
mellomlagret tilbakekallingstilstand og ikke noe konvergensvindu for sikkerhetsstempel,
så en tilbakekalling, en deaktivering eller en rolleendring gjelder umiddelbart på tvers
av alle replikaer.

- `POST /api/v1/identity/account/sessions/revoke` logger den kallende kontoen ut
  overalt, inkludert nettleseren som sendte forespørselen.
- `POST /api/v1/identity/system/users/{userId}/sessions/revoke` gjør det samme for
  en hvilken som helst konto og krever SystemAdmin.

Hver innlogging rydder også bort den brukerens døde økter (tilbakekalte, eller forbi
den standard absolutte levetiden), slik at tabellen holdes avgrenset til økter som
fortsatt kan være gyldige.

## Hastighetsbegrensninger

Autentiseringsendepunktene er hastighetsbegrenset av en begrenser med faste vinduer,
der tellerne ligger i PostgreSQL (`platform.rate_limit`), slik at en grense holder
**på tvers av replikaer og omstarter** og ikke per prosess. Grensen anvendes før
tilgangskontrollen.

| Policy | Grense | Gjelder for |
| --- | --- | --- |
| `Login` | 100 / minutt | `POST /login` |
| `login-attempts` | 10 / minutt | Mislykkede passord for én e-postadresse fra én adresse, nøkkel `EMAIL\|ip`; svarer uten `Retry-After` |
| `Bootstrap` | 20 / minutt | `POST /bootstrap` |
| `Mfa` | 20 / 5 minutter | 2FA-steget, MFA-administrasjon og registrering av passnøkkel |
| `PasskeyLogin` | 30 / 5 minutter | Innlogging med passnøkkel |
| `PasswordRecovery` | 10 / 15 minutter | Forespørsel om gjenoppretting, tilbakestilling og endring av kontopassord |
| `Invitations` | 30 / minutt | Eiers administrasjon av invitasjoner |
| `InvitationAcceptance` | 20 / minutt | Validering og aksept av invitasjon |
| `UserManagement` | 30 / minutt | Eiers brukeradministrasjon |
| `OwnerAvatarRead` | 300 / minutt | Lesing av eieres avatarer |

Alle policyer unntatt `login-attempts` bruker klientadressen som nøkkel, og det er
derfor `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` betyr noe bak en proxy: setter du
dem feil, deler alle forespørsler én bøtte, eller en klient velger sin egen.

## URL-er for invitasjon og gjenoppretting

Frontend-rutene er `/invitations/accept?token=…`, `/forgot-password` og
`/password-reset?email=…&token=…`.

Lenker som sendes på e-post utledes som standard fra `APP_URL` pluss `APP_BASE_PATH`,
så det er vanligvis nok å sette den offentlige opprinnelsen:

```text
APP_URL=https://vantigo.example.com
# gir https://vantigo.example.com/invitations/accept?token=...
# og  https://vantigo.example.com/password-reset?email=...&token=...
```

Når de utsendte lenkene må være annerledes, setter du malene eksplisitt. Hver av dem
må inneholde plassholderne sine ordrett, ellers feiler oppstarten:

```text
INVITATION_ACCEPT_URL=https://vantigo.example.com/invitations/accept?token={token}
PASSWORD_RESET_URL=https://vantigo.example.com/password-reset?email={email}&token={token}
```

`APP_URL` må bare være en opprinnelse — skjema, vert og valgfri port, ingen sti,
spørring eller legitimasjon; et stiprefiks hører hjemme i `APP_BASE_PATH`. `{email}` og
`{token}` URL-kodes av serveren. Ikke legg tokener i kildekontroll.

## E-postlevering

`MAIL_DRIVER=log` skriver e-post til applikasjonsloggen i stedet for å sende den. Den er
som standard `log` i utvikling og `smtp` alle andre steder, og **avvises utenfor
utvikling**: invitasjons- og passordtilbakestillingsmeldinger bærer bærerlenker, så å
logge dem noe annet sted er en lekkasje, ikke en funksjon.

For produksjon:

```text
MAIL_DRIVER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_FROM=no-reply@vantigo.example.com
SMTP_USERNAME=<from-secret-store>
SMTP_PASSWORD=<from-secret-store>
SMTP_TLS=starttls
```

`SMTP_HOST` og en gyldig `SMTP_FROM`-adresse kreves for SMTP-driveren.
`SMTP_TLS` er `starttls` (standard), `implicit` eller `none` — operatørens valg; se
[transportsikkerhet](/nb/admin/transport-security/) for hva `none` koster.
STARTTLS er obligatorisk, ikke opportunistisk — en server som ikke tilbyr TLS gir en
feil, ikke en levering i klartekst. `SMTP_USERNAME` er valgfri for servere som ikke
trenger autentisering.

SMTP-mål slås opp og kontrolleres før socketen åpnes: private, loopback-, link-local-,
carrier-grade-NAT- og sky-metadata-adresser avvises, og tilkoblingen gjøres til
adressen som ble kontrollert, ikke et nytt oppslag. Se
[transportsikkerhet](/nb/admin/transport-security/) for regelen og nødutgangen dens. Et
mål som bare nås gjennom DNS64 på det lokale prefikset `64:ff9b:1::/48` avvises uten
videre (det finnes ingen fast forskyvning å dekode en innebygd adresse fra); en
resolver på det velkjente prefikset `64:ff9b::/96` fungerer som normalt.

Fakturaer sender gjennom den samme konfigurasjonen: et dokument som sendes på e-post
til en kunde går ut fra `SMTP_FROM`, under selgerens navn og med selgerens e-post som
Reply-To, gjennom denne SMTP-serveren og vakten dens
([Fakturaer](/en/reference/invoices/#sending-a-document)). Under `MAIL_DRIVER=log` kan
ingenting sendes den veien: sendingen svarer 503 `mail_unavailable`, og
`GET /invoices/meta` svarer `mailAvailable: false`.

Dette er identitetsmodulens applikasjonspost, og Fakturaers. Kommunikasjons
postkasselegitimasjon per kanal er noe helt annet — den konfigureres gjennom
Kommunikasjons-API-et og forsegles i hvile, aldri via miljøvariabler. Se
[Kommunikasjon](/en/reference/communications/).

## Nøkkelmateriale

`APP_SECRET` er nøkkelmaterialet for hele prosessen: minst 32 byte, som hver nøkkel
prosessen bruker utledes fra med HKDF-SHA256 — én utledet nøkkel per formål — og brukes
som AES-256-GCM (CSRF- og informasjonskapselforsegling, OIDC-state, kryptering av
TOTP-hemmeligheter, kanalpassord i Kommunikasjon). Formålsstrengen bindes inn som
ekstra autentiserte data, slik at en verdi forseglet for ett formål ikke kan åpnes under
et annet, og en nøkkel-id-byte gir rom for et framtidig rotasjonsopplegg.

**Det finnes ingen lagret nøkkelring, ingen nøkkelinnpakkingstjeneste og ikke noe
eksternt nøkkelhvelv.** Ingenting trenger klargjøring utover selve variabelen. To
konsekvenser betyr noe i drift:

- **Alle replikaer må dele samme `APP_SECRET`** (og samme database), ellers kan ikke
  økter utstedt av én replika leses av en annen.
- **Å miste eller rotere den tilsvarer å miste en signeringsnøkkel**: hver åpen økt,
  hver pågående OIDC-flyt, hver lagret TOTP-hemmelighet og hver forseglet
  kanallegitimasjon blir umulig å gjenopprette. Roter bare med en migreringsplan.

## Konfigurasjonsreferanse

Feltkommentarene i `config.go` er fortsatt autoritative; denne tabellen er
operatørsammendraget. Boolske verdier er strenge `0`/`1`-brytere — alt annet gjør at
oppstarten feiler.

### Applikasjon

| Variabel | Formål | Standard |
| --- | --- | --- |
| `APP_ENV` | `production` eller `development`; bare development lemper på noe | `production` |
| `APP_URL` | Offentlig opprinnelse (skjema + vert [+ port]); ingen sti | **påkrevd** |
| `APP_BASE_PATH` | Stiprefiks på et delt domene, f.eks. `/vantigo` | tom (domenets rot) |
| `PORT` | Lytteport | `8080` |
| `DATABASE_URL` | Kjøretidstilkoblingen, rollen med minst rettigheter | **påkrevd** |
| `MIGRATIONS_DATABASE_URL` | Tilkoblingen `migrate` bruker (eierrollen) | `DATABASE_URL` |
| `SHUTDOWN_TIMEOUT` | Budsjett for å tømme pågående forespørsler og arbeidere | `30s` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `MODULES` | Kommaseparert liste over forretningsmoduler som skal betjenes | `customers,products,energy,communications,projects,time,expenses` |
| `WORKERS_IN_PROCESS` | Om `api` også kjører bakgrunnsarbeidere i samme prosess | `1` |

### Hemmeligheter og identitet

| Variabel | Formål | Standard |
| --- | --- | --- |
| `APP_SECRET` | Nøkkelmateriale for hele prosessen, ≥ 32 byte | **påkrevd** |
| `OWNERS_REQUIRE_MFA` | Krev MFA for Owner og SystemAdmin | på utenfor utvikling |
| `OWNERS_ALLOW_INSECURE_NO_MFA` | Lar prosessen starte med den over satt til `0`; slår den ikke av alene | `0` |
| `MFA_ISSUER` | Etikett i autentisator-appen | `Vantigo` |
| `SESSION_IDLE_TIMEOUT` | Standard inaktivitetsvindu | `8h` |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | Privilegert inaktivitetsvindu | `2h` |
| `SESSION_ABSOLUTE_LIFETIME` | Standard absolutt grense | `24h` |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | Privilegert absolutt grense | `8h` |
| `INVITATION_LIFETIME` | Invitasjonens gyldighet, `24h`–`720h` | `168h` |
| `INVITATION_ACCEPT_URL` | Mal som inneholder `{token}` | utledet fra `APP_URL` + `APP_BASE_PATH` |
| `PASSWORD_RESET_URL` | Mal som inneholder `{email}` og `{token}` | utledet fra `APP_URL` + `APP_BASE_PATH` |
| `BOOTSTRAP_OWNER_EMAIL` | Stenger `/setup`; setter inn den første eieren ved invitasjon på e-post i stedet | ikke satt (`/setup` åpen) |

### E-post

| Variabel | Formål | Standard |
| --- | --- | --- |
| `MAIL_DRIVER` | `smtp`, eller `log` (bare utvikling) | `log` i utvikling, ellers `smtp` |
| `SMTP_HOST` | SMTP-server | påkrevd for `smtp` |
| `SMTP_PORT` | SMTP-port | `587` |
| `SMTP_FROM` | Avsenderpostkasse | påkrevd for `smtp` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Valgfri legitimasjon | ikke satt |
| `SMTP_TLS` | `starttls`, `implicit` eller `none` | `starttls` |

### OIDC og SCIM for arbeidsstyrken

| Variabel | Formål | Standard |
| --- | --- | --- |
| `OIDC_PROVIDER` | `entra` eller `google`; ikke satt slår av OIDC | ikke satt |
| `OIDC_AUTHORITY` | Nøyaktig utsteder for leverandøren | ikke satt |
| `OIDC_CLIENT_ID` | Entra-GUID, eller en `.apps.googleusercontent.com`-klient-ID | ikke satt |
| `OIDC_CLIENT_SECRET` | Klienthemmelighet | ikke satt |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` | Absolutt, lesbar assertion-fil (bare Entra) | ikke satt |
| `AZURE_FEDERATED_TOKEN_FILE` | Plattformlevert reserve for den over | ikke satt |
| `OIDC_ALLOWED_DOMAINS` | Kommaseparert liste over rene DNS-navn (bare Google, påkrevd der) | ikke satt |
| `OIDC_DISPLAY_NAME` | Etikett på innloggingsknappen | `Workforce SSO` |
| `SCIM_TOKEN` | Statisk SCIM-bærertoken; ikke satt slår av SCIM | ikke satt |
| `SCIM_PREVIOUS_TOKEN` | Forrige token under en rotasjonsoverlapping | ikke satt |
| `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` | RFC 3339, i framtiden, ≤ 24h fram | ikke satt |

### Transport, lagring og moduler

| Variabel | Formål | Standard |
| --- | --- | --- |
| `CSP_REPORT_ONLY` | Send CSP-en som report-only | `0` |
| `TRUSTED_PROXY_HOPS` | Antall proxyer foran (0–10) | `0` |
| `TRUSTED_PROXY_CIDRS` | Proxyenes egne adresser, som CIDR-prefikser | ikke satt |
| `STORAGE_PROVIDER` | `fs`, eller ikke satt for lagring som feiler lukket | ikke satt |
| `STORAGE_FS_ROOT` | Absolutt rot for `fs`-driveren | ikke satt |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Lemp på rettighetskontrollen av roten (bare utvikling) | `0` |
| `BRREG_BASE_URL` | Opprinnelse for Brønnøysundregistrene | `https://data.brreg.no` |
| `BRREG_TIMEOUT` | Budsjett for ett oppslag, inkludert nye forsøk | `15s` |
| `PEPPOL_LOOKUP_ENABLED` | Av/på-bryter for oppslaget av Peppol EHF-kapasitet | `1` |
| `PEPPOL_SML_ZONE` | SML-sonen en deltakeridentifikator hashes inn i; må være et rent vertsnavn — ingen skjema, sti eller blanktegn | `participant.sml.prod.tech.peppol.org` |
| `PEPPOL_DNS_SERVER` | `host:port` for en resolver som skal brukes i stedet for `/etc/resolv.conf`; porten er påkrevd og må være numerisk | ikke satt |
| `PEPPOL_TIMEOUT` | Budsjett for ett oppslag fra ende til ende (DNS og SMP samlet) | `10s` |
| `INVOICES_EHF_ENABLED` | Av/på-bryter for å sende fakturaer som EHF i Peppol-nettverket; krever også `PEPPOL_LOOKUP_ENABLED=1`, påloggingsdata for aksesspunktet og selgerens Peppol-ID ([E-faktura](/nb/admin/e-invoicing/)) | `1` |
| `INVOICES_STORECOVE_BASE_URL` | Basis-URL for API-et til Storecove-aksesspunktet — en absolutt http- eller https-URL, en avsluttende skråstrek fjernes; pek den på en testvert for en test (Storecoves sandkasse er den samme verten med en sandkassenøkkel) | `https://api.storecove.com/api/v2/` |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | Profilering av SPA-en | ikke satt |

Peppol-oppslaget behandler NXDOMAIN og NOERROR-uten-NAPTR-poster som det samme
endelige «ikke registrert»-svaret ([Peppol-oppslag](/en/reference/customers/#peppol-lookup)),
siden SML bare publiserer et navn for en registrert deltaker. Det gjør at resolveren
`PEPPOL_DNS_SERVER` peker på betyr noe: en som svarer NODATA i stedet for å
videresende den autoritative NXDOMAIN — eller som stille dropper en ukjent posttype
som NAPTR i stedet for å sende den videre — ville gjort en faktisk registrert deltaker
til en stille falsk negativ, uten noe i svaret som skiller de to. Pek
`PEPPOL_DNS_SERVER` på en vanlig rekursiv resolver, ikke en som syntetiserer eller
filtrerer svar.

### Management-lytter

| Variabel | Formål | Standard |
| --- | --- | --- |
| `MANAGEMENT_PORT` | Port for den private kontrollplan-lytteren (må være en annen enn `PORT`) | ikke satt (slått av) |
| `MANAGEMENT_TOKEN` | Bærertoken for den, ≥ 32 tegn, ingen blanktegn | ikke satt (slått av) |

De to er én bryter: sett begge eller ingen. Se
[management-lytteren](/nb/admin/management-listener/).

Kommunikasjons egne innstillinger (`COMMUNICATIONS_*`) er dokumentert i
[Kommunikasjon](/en/reference/communications/). OpenTelemetry konfigureres med de
standard `OTEL_EXPORTER_OTLP_*`-variablene, som leses av OTel-SDK-et og ikke av
`config.go`.

## OIDC for arbeidsstyrken

Høyst én OpenID Connect-leverandør for arbeidsstyrken konfigureres, ved oppstart, fra
miljøet; ingenting om den lagres eller kan redigeres gjennom et API. `OIDC_PROVIDER`
er av/på-bryteren — når den ikke er satt, gjør enhver annen `OIDC_*`-variabel som
fortsatt er satt at oppstarten feiler med navn på restene, slik at en halvveis fjernet
leverandør ikke kan se slått av ut mens den bærer gyldig legitimasjon.

**Klientautentiseringsmodusen utledes, den konfigureres ikke.** Det finnes ingen
`ClientAuthentication`-innstilling: sett `OIDC_CLIENT_SECRET` for autentisering med
klienthemmelighet, eller `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` (eller la plattformen
levere `AZURE_FEDERATED_TOKEN_FILE`) for workload identity. Å sette begge er en
konfigurasjonsfeil, og det er også å sette ingen av dem. Workload identity er bare for
Entra; Google er bare klienthemmelighet.

Flyten er autorisasjonskode pluss PKCE med en nonce. Leverandørmetadata, utsteder,
mottaker, signatur, state, nonce og korrelasjon valideres, den validerte identiteten
holdes i en forseglet informasjonskapsel, og leverandørtokener lagres aldri.
Nettleseren starter på `/api/v1/identity/oidc/challenge`, leverandøren vender tilbake
til det faste `/api/v1/identity/oidc/callback`, og lokal fullføring er
`/api/v1/identity/oidc/complete`. Hver feil er en omdirigering til
`/sign-in?error=<code>`; intet omdirigeringsmål kommer noen gang fra forespørselen.

Deretter gjelder leverandørspesifikke kontroller: Entra krever en `tid` som samsvarer
med tenant-GUID-en i den konfigurerte autoriteten og en `oid`-GUID, og krever at
`azp` er lik klient-ID-en når tokenet bærer flere mottakere; Google krever
`email_verified`, en gyldig e-post og en ikke-tom `hd` som samsvarer med
e-postdomenet og finnes i `OIDC_ALLOWED_DOMAINS`.

Nye identiteter klargjøres akkurat i tide som lokale `User`-kontoer, med den validerte
utstederen og `sub` (skiller mellom store og små bokstaver) som nøkkel. E-post fra
leverandøren er til informasjon: en e-postkollisjon **feiler i stedet for å koble
automatisk**. OIDC-claims gir aldri lokale roller og oppfyller aldri det lokale
MFA-kravet.

`GET /api/v1/identity/providers` er anonymt og rapporterer de konfigurerte
innloggingsleverandørene til SPA-en.

### Påkrevd tilbakekalls-URI

Registrer det nøyaktige offentlige HTTPS-tilbakekallet hos leverandøren, inkludert
basisstien når en slik er konfigurert:

```text
https://vantigo.example.com/api/v1/identity/oidc/callback
```

Tilbakekallsstien er fast og er ikke en utrullingsinnstilling. Den offentlige
opprinnelsen, det videresendte skjemaet og leverandørregistreringen må stemme overens.

## SCIM

SCIM 2.0 betjenes på `/api/v1/identity/scim/v2` og autentiseres med det statiske
bærertokenet i `SCIM_TOKEN`; ikke satt slår det av. Tokenet kan ikke inneholde
blanktegn. **Det finnes ingen filbasert tokenvariant** — injiser verdien fra en
hemmelighetsforvalter. Rotasjon med en avgrenset overlapping er beskrevet i
[SSO- og SCIM-drift](/nb/admin/sso-scim/).

## Videresendte headere og HTTPS

`TRUSTED_PROXY_HOPS` **teller proxyene foran serveren** i stedet for å liste dem opp:
med `N > 0` har hver av de N klarerte proxyene lagt til én oppføring i
`X-Forwarded-For`, så klienten er den N-te oppføringen fra høyre, og alt til venstre
for den er skrevet av klienten og ikke klarert. Siste oppføring i `X-Forwarded-Proto`
blir skjemaet. `X-Forwarded-Host` respekteres aldri — proxyer må bevare `Host`.

Utenfor utvikling gjør hopp uten `TRUSTED_PROXY_CIDRS` at oppstarten feiler:

```text
TRUSTED_PROXY_HOPS: requires TRUSTED_PROXY_CIDRS outside development:
forwarded headers are honoured only from a peer inside that list
```

Med listen satt respekteres videresendte headere bare når den direkte motparten
faller innenfor ett av prefiksene; enhver annen motpart behandles som selve klienten.
Uten den kunne hver motpart velge klientadressen hastighetsbegrensningene bruker som
nøkkel.

```text
TRUSTED_PROXY_HOPS=1
TRUSTED_PROXY_CIDRS=10.0.0.0/24
```

Terminer TLS på proxyen, videresend det opprinnelige skjemaet, og eksponer SPA-en,
API-et og OIDC-tilbakekallet på den HTTPS-opprinnelsen. Utenfor utvikling er
informasjonskapslene `Secure`, og `APP_URL` må være `https`.

## Databasemigreringer

Én PostgreSQL-database holder ett skjema per modul — `identity`, `customers`,
`products`, `energy`, `communications`, `projects` og `time` — migrert av rene SQL-filer
innebygd i binærfilen. Alle skjemaer migreres uansett hvilke moduler `MODULES` slår på,
så det å slå på en modul senere krever ingen migrering. Se
[migreringsveiledningen for bidragsytere](https://github.com/vantigo-io/vantigo/blob/main/CONTRIBUTING.md#database-migrations)
for hvordan migreringer skrives og anvendes.

**Både `migrate`-kommandoen og `api`-kommandoen anvender migreringer.** `api` anvender
ventende migreringer under en rådgivende lås i PostgreSQL og begynner først å betjene
når de har lyktes; `server`-modus migrerer aldri. Migratorer serialiseres på den
låsen, så en gammel og en ny binærfil som starter samtidig venter på hverandre i stedet
for å kappes.

Å kjøre en avsluttende `migrate`-jobb først og vente på den er fortsatt den riktige
utrullingsformen: poenget er å se en migreringsfeil **før** noen betjenende replika
starter, ikke at `api` ellers ville latt skjemaet ligge igjen. Bruk
`MIGRATIONS_DATABASE_URL` for eierrollen der en utrulling skiller den fra
kjøretidsrollen.

Ikke kjør `seed` i produksjon; den er bare for utvikling og avslutter med 2 utenfor det.
