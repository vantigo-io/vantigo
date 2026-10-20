---
title: "Identitet, autentisering og utrulling"
description: "Lokale kontoer, brukere Ansatte styrer, kontoene Ansatte kunngjør og rollene Ansatte kan gi, OIDC-leverandøren for arbeidsstyrken og SCIM, e-post, proxy-tillit, nøkkelmaterialet som utledes fra APP_SECRET og migreringer."
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

OIDC-leverandøren for arbeidsstyrken og SCIM 2.0 settes av en eier på kortet **Single
sign-on**, ikke i miljøet; for oppsett av leverandør og gjenopprettingsprosedyren for
operatører, se [SSO- og SCIM-drift](/nb/admin/sso-scim/).

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

## Brukere som Ansatte styrer

En bruker er **styrt av Ansatte** når identitetsmodulen merker brukeren som styrt av
Ansatte, med ansattnummeret som knytter brukeren til en person i
[Ansatte](/nb/admin/staff/)-registeret. Kontoklargjøringen i Ansatte kan da aktivere,
deaktivere og oppdatere den brukeren; den rører aldri en bruker som ikke er styrt av
Ansatte. Merkingen, ansattnummeret og hvem som deaktiverte en bruker er
identitetsmodulens egne kolonner (`identity.users`): Ansatte når dem bare gjennom
identitetsmodulen, aldri gjennom sine egne tabeller.

`GET /api/v1/identity/owner/users` viser tilstanden på hver bruker: `pending` (sendes
alltid: en bruker Ansatte har opprettet som ennå ikke har valgt en innloggingsmetode,
vist som `disabled` og ikke `active` til det skjer), og, bare når de er satt,
`managedBy` (`staff`), `employeeNumber` og `disabledBy` (`owner` eller `staff`).

Dette kan en eier fortsatt gjøre med en bruker som Ansatte styrer:

- **Redigere, tilbakestille passordet eller MFA, og aktivere** brukeren som enhver annen
  — bortsett fra at en **ventende** bruker verken kan aktiveres eller få et passord:
  begge svarer 409 `account_pending`, siden kontoen bare blir brukbar ved at
  invitasjonen aksepteres. Skulle en ventende bruker likevel ha en innloggingsmetode,
  avviser innlogging med passord, passnøkkel og OIDC brukeren slik de avviser en
  deaktivert konto, og en økt brukeren allerede har, avsluttes ved neste forespørsel.
  Ingen skriver noen gang et passord til en ventende bruker, verken en eier, en
  tilbakestilling eller brukeren selv: selve skrivingen sjekker, så en bruker som blir
  ventende mens et passord settes, får likevel ikke noe (409 `account_pending`;
  brukerens egen endring svarer 401 og avslutter ingen økt). Gjenoppretting av passord
  sender ingen tilbakestillingslenke til en ventende bruker, og svarer som for enhver
  annen adresse.
- **Deaktivere** brukeren. En eiers deaktivering registreres som eierens
  (`disabledBy: owner`), også over en bruker Ansatte hadde deaktivert, og Ansatte
  aktiverer aldri igjen en bruker en eier har deaktivert. Aktivering fjerner den. Å
  overstyre deaktiveringen til Ansatte — å deaktivere over den, eller å aktivere en
  bruker Ansatte deaktiverte — logges i tillegg som `user.disabled-by-changed`, med hvem
  som hadde deaktivert brukeren før og etter.
- **Forfremme** brukeren til eier. Ansatte styrer aldri en eier, så forfremmelsen
  slutter å la Ansatte styre brukeren i samme endring, logget som
  `user.staff-unmanaged` med årsaken `promoted`. En ventende bruker kan ikke
  forfremmes (409 `account_pending`), og en ventende bruker regnes aldri som en aktiv
  eier, så å deaktivere eller nedgradere den siste eieren som kan logge inn, avvises
  med `last_active_owner` uansett hva ventende brukere holder.
- **Ikke slette** brukeren: slettingen svarer 409 `account_managed`. Slutt å la Ansatte
  styre brukeren først (nedenfor), og slett deretter.

Brukere som var deaktivert før denne versjonen, er registrert som deaktivert av en eier,
siden bare en eier kunne deaktivere en bruker.

En ventende brukers **invitasjonsrunde** begynner når Ansatte oppretter brukeren
(`identity.users.invitations_since`): invitasjonene Ansattes klargjøring leverer til
brukeren fra da av, teller mot de tre, og en ny invitasjon (Ansattes *Kjør nå*) eller en
ny adresse før den første invitasjonen er levert, starter en ny runde.

En ventende brukers **adresse følger personens oppføring i Ansatte bare til den første
invitasjonen som navngir brukeren, er levert** (en som ikke kunne leveres, teller ikke).
Fra da av er adressen den personen ble invitert til: Ansattes klargjøring skriver den
ikke lenger, sender ingenting til en ny adresse, og melder forskjellen
(`accountEmailDiffers` i Ansatte) til en eier, som endrer adressen på siden
**Brukere** — som for en aktiv bruker.

### En ventende bruker godtar invitasjonen sin

Invitasjonen kontoklargjøringen i Ansatte sender en ventende bruker, navngir den
brukeren (`identity.invitations.user_id`). Når den godtas, fullføres den eksisterende
kontoen i stedet for at en ny opprettes: under eierlåsen, så invitasjonens radlås, så
brukerens, må brukeren fortsatt ha invitasjonens adresse, være styrt av Ansatte, være
ventende, ikke være deaktivert (av en eier eller av Ansatte), verken være eier eller
SystemAdmin og ikke ha passord, passnøkkel eller OIDC-kobling. Passordet må oppfylle
reglene; brukeren får det, en bekreftet e-post, ingen ventemerking og ingen utestenging,
beholder visningsnavnet sitt (det som skrives på
siden, brukes ikke) og rollene brukeren har — en invitasjon som navngir en bruker, gir
ingen rolle og gjør aldri noen til første eier. Revisjonsloggen registrerer
`invitation.accepted-completing-user`, og brukeren blir logget inn. Alt annet svarer
samme 400 `invitation_invalid` som en ukjent eller utløpt lenke, og ingenting skrives.

De samme vilkårene avgjør resten av en navngitt invitasjons liv. Å validere lenken
(`GET /api/v1/identity/invitations/validate`) svarer `valid: false` så snart brukeren
ikke lenger oppfyller dem, og navngir ingen rolle mens brukeren gjør det, siden kontoen
beholder sine egne. En ny sending beholder brukeren invitasjonen navngir, og avvises med
409 `invitation_not_active`, uten at noe sendes, så snart den brukeren ikke lenger
oppfyller dem eller er borte.

En eier kan ikke invitere adressen til en ventende bruker for hånd: kontoen finnes, så
invitasjonen svarer 409 `account_exists`. Det er Ansatte som sender invitasjonen til en
ventende bruker; en eier kan sende en som allerede er sendt, på nytt.

### Revisjonsloggens systemaktør

En endring kontoklargjøringen i Ansatte gjør i identitetsmodulen, logges som en eiers, i
`identity.authorization_audit_events`, men aktøren er et system, ikke en bruker:
`actor_user_id` er tom, `details` er
`{"action":"…","actor":"system:staff-accounts"}`, og det er ingen korrelasjons-id, siden
ingen forespørsel gjorde endringen. Alle andre raders `details` er `{"action":"…"}` som
før.

**Etter at Ansatte har slettet en person.** Ansattes sletting når bare Ansattes rader:
identity beholder brukeren Ansatte styrte for dem — navnet, adressen (kanskje personens
private) og ansattnummeret — brukerens invitasjoner og revisjonsradene som navngir dem,
der øyeblikksbildene har navn og ansattnumre. Dette er godtatt i denne utgivelsen
(Ansattes design S15); en automatisk fjerning kommer i en senere utgivelse. En eier
fjerner brukeren for hånd: [slutt å la Ansatte styre
dem](#slutte-å-la-ansatte-styre-en-bruker), og slett dem så. Revisjonsradene blir
stående, som for enhver slettet bruker.

### Autoritetsmodusen

Ansattes klargjøring arbeider i én av to moduser. **Lokal**: identitetsmodulen har
innloggingsmetodene, så den oppretter brukerne Ansatte ber om, aktiverer dem og
inviterer dem til å velge et passord. **Føderert**: en identitetsleverandør eier
kontoene — OIDC for arbeidsstyrken eller SCIM er på i
[innstillingene for single sign-on](/nb/admin/sso-scim/#kortet-single-sign-on) — så den
bare finner en bruker etter ansattnummer, aktiverer igjen en bruker
Ansatte deaktiverte og deaktiverer; den oppretter og inviterer aldri (se
[SSO- og SCIM-drift](/nb/admin/sso-scim/#ansatte-i-en-føderert-installasjon)).

**Ansatte bestemmer nå per person.** Hver person i Ansatte har en **kontotype** —
`local`, `external` eller `entra` — og hvert kall Ansattes klargjøring gjør, har den med
seg, så typen, ikke installasjonens modus alene, bestemmer hva identitetsmodulen
oppretter, gir og inviterer ([under](#hva-ansattes-klargjøring-gjør-i-identitetsmodulen)):
en `local`-person i en installasjon der single sign-on er på, opprettes og inviteres
fortsatt. Modusen står igjen for to ting: et kall som ikke navngir noen type — fra en
Ansatte-worker fra utgivelsen før — dømmes etter den (`local` fra lokal, `external` fra
føderert), og Ansatte setter hver persons type og standardtypen fra den én gang, første
gang den starter på denne utgivelsen.

Modusen utledes av `identity.sso_settings` i hver prosess — API-et og workerne likt —
så ingen kan være uenige, og en worker trenger ingen OIDC- eller SCIM-innstillinger av
sine egne. Å slå SSO eller SCIM av eller på på kortet endrer modusen overalt innen 20
sekunder, uten omstart. Modusen registreres også, som sporet av endringene sine, i
`identity.system_settings.authority_mode` med `authority_mode_at`: av en eiers lagring
på kortet som endrer den, i lagringens egen transaksjon, og ved hver oppstart av
API-et, i transaksjonen som holder raden for importen — den første etter
oppgraderingen registrerer modusen de
[importerte innstillingene](/nb/admin/sso-scim/#før-kortet-miljøvariablene) gir. En
endring revisjonslogges `authority-mode.changed` med modusene før og etter, uten aktør
og uten korrelasjons-ID. En lagring eller en oppstart med samme modus endrer
ingenting.

Samme rad holder **arbeidsområdets forretningstidssone**
(`identity.system_settings.business_time_zone`, `Europe/Oslo` som standard): den ene
kalenderen hver modul dømmer en regnskapsdag etter, lest gjennom plattformens
`BusinessCalendar`-kontrakt og satt av en eier med
`PUT /api/v1/identity/owner/workspace` eller kortet **Arbeidsområde** under
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet).
Et navn serveren ikke kan laste, og et navn databasen leser annerledes enn serveren
(`CET`), avvises. En endring revisjonslogges `workspace.business_time_zone_changed` med
sonen før og etter, av eieren som gjorde den. Før denne innstillingen hadde Ansatte og
Utlegg hver sin sone; en installasjon oppgradert derfra kjører på `Europe/Oslo` til en
eier setter arbeidsområdets, og migreringsloggen advarer når en av modulenes verdi var
en annen.

Ved siden av har hver bruker en **personlig tidssone** (`identity.users.time_zone`):
sonen tidspunkter vises i for den brukeren, og som et klokkeslett brukeren skriver inn,
leses i. Den er tom — «følg arbeidsområdet» — til brukeren velger en under **Personlige
innstillinger** ([Kom i gang](/nb/user/getting-started/#velg-tidssonen-din)), eller en klient
sender `timeZone` til `PUT` eller `PATCH /api/v1/identity/account/profile`. Navnet
holdes til de samme to kontrollene som arbeidsområdets, og avvises ellers på `timeZone`
med `400 invalid_request`; `null` eller et blankt navn tømmer den igjen, og en
forespørsel som utelater `timeZone`, beholder den. `GET /api/v1/identity/account`
svarer med det lagrede valget (`null` når brukeren følger arbeidsområdet), og
`GET /api/v1/identity/session` svarer med sonen som gjelder som `timeZone`, hvor den kom
fra som `timeZoneSource` (`personal` eller `business`), og arbeidsområdets som
`businessTimeZone`. En personlig sone flytter aldri en regnskapsdag; en tom sone følger
en senere endring av arbeidsområdets sone med en gang, siden den slås opp når økten
leses og ikke kopieres når brukeren opprettes. Den er en preferanse, så en endring
revisjonslogges ikke, og ingen eier eller katalog setter den for brukeren: verken SCIM
eller Ansattes klargjøring skriver den.

### Hva Ansattes klargjøring gjør i identitetsmodulen

Ansatte når identitetsmodulens brukere bare gjennom identitetsmodulens klargjøring, som
kjører hver endring i en egen transaksjon — under eierlåsen, deretter brukerens
invitasjoner, deretter brukeren — revisjonslogger den med aktøren (personen som trykket
på en knapp i Ansatte, eller `system:staff-accounts`), og sender e-post først når
endringen er lagret. Den avviser alt uten en gyldig aktør, og arbeider etter
**kontotypen kallet har med seg** — personens i Ansatte — eller, for et kall som ikke
navngir noen, etter [autoritetsmodusen](#autoritetsmodusen) innstillingene for single
sign-on gir, lest i endringens egen transaksjon (`local` fra lokal modus, `external` fra
føderert). Typen `external` er føderert modus' regler for én person; `entra` er den
samme bare-koblende veien for en person som Ansatte lar en ekstern leverandør opprette
kontoen til og [kunngjør](#den-kunngjorte-kontoen), med ansattrollene gitt én gang.

| Steg | `local` | `external` | `entra` | Revisjonslogg |
| --- | --- | --- | --- | --- |
| Klargjør | Finner brukeren Ansatte styrer med ansattnummeret, ellers oppretter den en: styrt av Ansatte, ventende, deaktivert av Ansatte, med den innebygde rollen `User`, uten innloggingsmetode og med ubekreftet adresse. En eiers åpne invitasjon til adressen trekkes tilbake `replaced` og navngis i revisjonsloggen — men en åpen invitasjon til å bli eier holder adressen for den inviterte. Svaret sier om kallet opprettet brukeren (`Created`), så Ansatte aldri omgjør en bruker den bare fant. | Finner brukeren Ansatte styrer med nummeret; oppretter ingenting (`Ikke funnet` til en eier tar en inn) | Finner brukeren Ansatte styrer med nummeret — den den kunngjorte kontoens ankomst laget; oppretter ingenting | `user.staff-prepared` |
| Aktiver | Aktiverer en bruker Ansatte har deaktivert — også en klargjort bruker — og gir dem [ansattrollene](#ansattrollene) bare ved det kallet, så en rolle en eier fjerner senere, forblir fjernet, og registrerer når (`provisioning_roles_at`); inviterer en ventende bruker uten innloggingsmetode ved navn | Aktiverer igjen en bruker Ansatte deaktiverte; gir ingen rolle og sender ingen invitasjon | Aktiverer igjen en bruker Ansatte deaktiverte og, så lenge `provisioning_roles_at` er tom, gir ansattrollene og registrerer det — én gang, aktivert eller ikke, aldri igjen; sender ingen invitasjon | `user.staff-activated` |
| Deaktiver | Deaktiverer brukeren som Ansattes (`disabledBy: staff`) — også en katalogen allerede har deaktivert — avslutter øktene og tilbakestillingslenkene, og trekker tilbake de åpne invitasjonene `deactivated` | Det samme | Det samme | `user.staff-deactivated` |
| Oppdater | Skriver visningsnavnet, språket og ansattnummeret; adressen bare mens brukeren venter og ingen invitasjon som navngir dem, er levert ennå, og da trekkes invitasjonene tilbake `email_changed` og en eiers åpne invitasjoner til den nye adressen `replaced` (navngitt i revisjonsloggen) | Skriver bare språket og ansattnummeret | Det samme som `external` | `user.staff-updated` |
| Forvent (`Expect`) | — | — | Registrerer eller erstatter [kunngjøringen](#den-kunngjorte-kontoen) for ansattnummeret | `account.expected` |
| Trekk tilbake (`Unexpect`) | — | — | Trekker tilbake en kunngjøring ingen har oppfylt | `account.unexpected` |

En aktivert ventende bruker er aktivert, men kan fortsatt ikke logge inn: innlogging med
passord, passnøkkel og OIDC avviser dem — OIDCs første innlogging med adressen deres
svarer `account_locked` og kobler ingenting — til de godtar invitasjonen. En bruker som
har godtatt, beholder adressen identitetsmodulen har; når Ansattes register har en
annen, melder Ansatte det for en eier å endre her.

Invitasjonene følger den ventende brukerens [invitasjonsrunde](#brukere-som-ansatte-styrer):
en invitasjon som har gått ut, erstattes ved neste aktivering til tre er levert; deretter
melder Ansatte den som ubesvart. En eiers tilbaketrekking av invitasjonen står — Ansatte
sender ingen ny — til noen kjører personens konto for hånd i Ansatte (*Kjør nå*), som
starter en ny runde. *Kjør nå* sender aldri på nytt en invitasjon som fortsatt kan
godtas. Ingen invitasjon sendes innen ti minutter etter den forrige som navnga brukeren
— verken ved *Kjør nå*, etter en adresseendring eller ved neste planlagte kjøring — så
å trykke *Kjør nå* igjen og igjen, eller å endre en adresse fram og tilbake, sender
høyst én invitasjon hvert tiende minutt. En e-post som ikke kan sendes, trekker invitasjonen tilbake
`delivery_failed` og teller ikke; neste aktivering prøver igjen.

Ansattes klargjøring rører aldri en eier eller en SystemAdmin, aktiverer aldri igjen en
bruker en eier deaktiverte, og deaktiverer aldri den siste aktive eieren. En som slutter,
mister tilgangen uansett hva katalogen sier: Ansatte deaktiverer dem i
identitetsmodulen også når katalogen allerede melder dem inaktive (SCIM deaktiverer
aldri en bruker i identitetsmodulen, og deaktiveringen der teller bare mens SCIM er
konfigurert), så en katalog som aktiverer dem igjen senere, eller SCIM slått av, slipper
dem ikke inn. En deaktivering fra Ansatte fjerner ingen
rolle, så en ny ansettelse beholder rollene en eier har gitt brukeren tidligere i
tillegg til ansattrollene; en eier fjerner roller for hånd når det ikke er ønsket. En ny
ansettelse aktiverer igjen det Ansatte deaktiverte; så lenge
katalogen fortsatt melder personen inaktiv, holder den dem ute, og Ansatte melder
kontoen som deaktivert av katalogen. Dette avviser den:

| Avvisning | Betydning | Hva du gjør |
| --- | --- | --- |
| Ikke styrt | Brukeren styres ikke av Ansatte | Ta dem inn, eller overlat dem til en eier |
| Privilegert | Brukeren er eier eller SystemAdmin (avvist for hvert steg, også en oppdatering). Identitet melder dette når Ansatte leser kontoen, så Ansatte melder det uten å forsøke, også for en styrt bruker som siden er gjort til eier | Håndter kontoen for hånd |
| Sperret av en eier | En eier deaktiverte brukeren | Aktiver dem her hvis Ansatte skal styre dem igjen |
| Ansattrolle mangler | Ingen ansattrolle er valgt | Velg [ansattrollene](#ansattrollene) |
| Usikker ansattrolle | En rolle i settet har en tillatelse som gir administrasjon — bare mulig ved en endring utenom identitetsmodulens kontroller | Ta tillatelsen av rollen, eller rollen ut av settet |
| Adressen er tatt | En annen bruker har personens adresse, eller en eier har invitert noen til å bli eier på den og invitasjonen er fortsatt åpen | Gi personen en annen adresse i Ansatte, eller rydd i den andre kontoen eller invitasjonen |
| Ansattnummeret er tatt | En bruker Ansatte ikke styrer, har nummeret | Ta inn den brukeren, eller rett nummeret i Ansatte |
| Ikke funnet | `external` eller `entra`: ingen bruker Ansatte styrer, har nummeret ennå | `external`: la identitetsleverandøren opprette kontoen, og ta den inn; `entra`: kontoen kommer med SCIM eller personens første innlogging og tas inn av kunngjøringen sin |
| Ulik modus | Meldes bare av en worker som fortsatt kjører utgivelsen før kortet Single sign-on, under en rullerende oppgradering (avvist for hvert steg unntatt deaktivering) | Fullfør oppgraderingen |

### Den kunngjorte kontoen

For en person av typen `entra` lar Ansatte den eksterne leverandøren opprette kontoen og
**kunngjør** den så for identitetsmodulen (`Expect`), slik at brukeren som kommer — med
leverandørens SCIM-overføring, eller ved personens første OIDC-innlogging — tas inn under
Ansatte idet den kommer, styrt, nummerert og ikke ventende, uten en eier imellom. En
kunngjøring holdes i `identity.expected_accounts`, én per ansattnummer (en ny erstatter
den): ansattnummeret, visningsnavnet, språket, utstederen (identitetsmodulens egen fra
innstillingene for single sign-on — Ansatte lar den stå tom), leverandørens objekt-ID
for kontoen, brukerhovednavnet (UPN, lagret normalisert slik adresser er) og høyst én
bruker den erstatter.

**Treffet, tre nøkler i rekkefølge.** En SCIM-opprettelse av en User matches først på
[virksomhetsutvidelsens](#scim) `employeeNumber`, etter nummerets nøkkel; deretter på
`userName` mot det kunngjorte brukerhovednavnet; deretter på `externalId` mot den
kunngjorte objekt-ID-en. En første OIDC-innlogging matches på kravet `oid` mot
objekt-ID-en, under den kunngjorte utstederen. Nummeret kommer først fordi leverandøren
skriver det som kontoens `employeeId`, som Entras standardtilordning i SCIM sender som
`employeeNumber`; brukerhovednavnet som nummer to, siden Entra sender `userName` fra det
som standard; og objekt-ID-en sist for SCIM, fordi Entras standardtilordning sender
**e-postkallenavnet** (`mailNickname`), ikke objekt-ID-en, som `externalId` — den treffer
bare når eieren tilordner `objectId` dit — mens OIDC alltid har med `oid`, som
identitetsmodulen allerede krever.

**Hva en ankomst gjør.** Under eierlåsen, i ankomstens egen transaksjon:

- **Et treff** oppretter brukeren slik SCIM eller OIDC ville gjort, og merker dem så som
  styrt av Ansatte med det kunngjorte ansattnummeret, ikke ventende (innloggingsmetoden er
  leverandørens), med det kunngjorte visningsnavnet bare når leverandøren ikke sendte
  noe. Kunngjøringen brukes opp og revisjonslogges `account.adopted-from-provider` med
  `matchedBy` (`employeeNumber`, `userName`, `externalId` eller `oid`) og
  ansattnummeret — aldri en adresse — ved siden av SCIMs egen `scim.user.created`.
- **Et treff som navngir en bruker å erstatte** (`ReplaceUserID`: personen var `local`
  med en ventende bruker uten innloggingsmetode da HR endret typen til `entra`) fullfører
  den brukeren i stedet for å legge inn en til: brukeren låses, de åpne invitasjonene
  trekkes tilbake `replaced`, `pending_since` og invitasjonsrunden tømmes, adressen
  overskrives av den ankommende `userName` — har en annen bruker den, er det fortsatt
  `DuplicateEmail` — og SCIM-tilordningen eller OIDC-koblingen festes til dem, med
  ID-en beholdt; `account.adopted-from-provider` navngir `replacedUserId`. For OIDC er
  e-postkravet som kolliderer med nettopp den brukeren, fullføringen, ikke
  `oidc_email_conflict`; med noen annen avvises det som før.
- **Uten treff** er det som før: en vanlig SCIM- eller OIDC-bruker, som en eier tar inn
  for hånd hvis de hører til Ansatte. Virksomhetsutvidelsens `employeeNumber` beholdes i
  SCIM-profilen og blir **aldri** brukerens ansattnummer: bare Ansattes klargjøring og en
  eiers inntak gjør det kravet.

**Hva `Expect` avviser.** En bruker som ikke styres, og som har nummeret, er
`ErrEmployeeNumberTaken`; en styrt eier eller SystemAdmin `ErrAccountPrivileged`. En
styrt bruker som har nummeret og ikke venter, har allerede kommet: ingenting registreres,
og kallet lykkes. En ventende styrt bruker registreres bare når kunngjøringen navngir dem
som den som skal erstattes; en bruker å erstatte som ikke styres, ikke venter eller har
et annet nummer, avvises. `Unexpect` sletter en kunngjøring og revisjonslogges
`account.unexpected` når en ble slettet; uten noen lykkes den og skriver ingenting.

**Rollene, én gang.** En innlemmet bruker kommer aktivert, så ingen `Activate` aktiverer
dem noen gang; `identity.users.provisioning_roles_at` registrerer når Ansattes
`Activate` ga en bruker ansattrollene. For typen `entra` gir den første `Activate` som
finner den tom, rollene og setter den — én gang: en eiers senere fjerning av en rolle
omgjøres ikke. Den lokale veien setter den også når den gir dem.

En kunngjøring ingen oppfyller, står til Ansatte trekker den tilbake; etter nitti dager
melder Ansatte personens konto som ikke kommet (`accountNotArrived`). Eierens manuelle
[inntak](#ta-en-eksisterende-bruker-inn-under-ansatte) står igjen for typen `external` og
for enhver bruker leverandøren laget uten kunngjøring. Migreringen
`00059_identity_expected_accounts.sql` legger til tabellen og `provisioning_roles_at`;
Down fjerner begge, og en kunngjøring som ikke er oppfylt, gjøres på nytt etter
leverandørens neste `Prepare`.

### Ta en eksisterende bruker inn under Ansatte

Å ta inn en bruker legger en bruker som allerede finnes — en kollega som hadde konto før
kontoene i Ansatte ble slått på — under styringen til Ansatte.

Forutsetninger: du er eier, innlogget med MFA når `OWNERS_REQUIRE_MFA` er på; personen
finnes i Ansatte-registeret med et ansattnummer; brukeren er verken eier eller
SystemAdmin.

1. Finn brukerens id i `GET /api/v1/identity/owner/users`.
2. Ta inn brukeren med personens ansattnummer:

   ```bash
   curl -X POST https://vantigo.example.com/api/v1/identity/owner/users/<id>/manage \
     -H 'Content-Type: application/json' \
     -d '{"module":"staff","employeeNumber":"1042"}'
   ```

3. Kontroller: svaret, og brukeren i listen, viser `managedBy: staff` og
   ansattnummeret. Revisjonsloggen registrerer `user.staff-managed`.

Et ansattnummer bruker Ansattes eget alfabet: etter trimming 1 til 16 tegn, hvert en
bokstav `a`–`z` (store eller små), et siffer eller en bindestrek — det a-meldingen, en
lønnsfil og Entras `employeeId` bærer uten escaping, og det Ansattes eget register
selv har. Numrene sammenlignes med en nøkkel identitetsmodulen beregner og lagrer ved
siden av nummeret (`identity.users.employee_number_key`), nummeret med store bokstaver,
så `a-17` og `A-17` er samme nummer, mens `E-1` og `E-01` er to. Databasens
sorteringsregler spiller ingen rolle.
Å ta inn en styrt bruker på nytt med et annet
nummer endrer nummeret. En bruker som
SCIM klargjør, kan tas inn: i en føderert installasjon er den styrte brukeren
identitetsleverandørens.

Dette kan gå galt:

| Svar | Betydning |
| --- | --- |
| 409 `account_privileged` | Brukeren er eier eller SystemAdmin; Ansatte styrer dem aldri, og en eier håndterer eiere for hånd |
| 409 `employee_number_taken` | En annen bruker har allerede nummeret, uansett store eller små bokstaver |
| 400 `invalid_request` | Modulen er ikke `staff`, nummeret er tomt, lengre enn 16 tegn eller inneholder noe annet enn `a`–`z`, `A`–`Z`, sifre og bindestreker — et mellomrom inni, en understrek, en bokstav som `Å` eller `ı`, et siffer i full bredde, et usynlig tegn eller et kontrolltegn — eller brukeren er deg selv |

### Slutte å la Ansatte styre en bruker

`POST /api/v1/identity/owner/users/<id>/unmanage` fjerner merkingen og ansattnummeret,
logget som `user.staff-unmanaged`. En bruker som Ansatte ikke styrer, blir som den er.
En ventende bruker forblir ventende: å fullføre eller slette den brukeren er da eierens
sak. Personen Ansatte koblet brukeren til, **mister Min side** (Ansattes egen visning)
til HR kobler brukeren til dem igjen for hånd på personsiden i Ansatte; da blir det en
vanlig selvbetjent kobling som Ansatte aldri handler på.

### Ansattrollene

Den innebygde rollen `User` har ingen rettigheter, så brukerne Ansatte klargjør, får
**rollene Ansatte kan gi**: egendefinerte roller en eier utpeker, typisk én
«Ansatt»-rolle med `staff:access`, `time:access` og `expenses:access`.
Identitetsmodulen holder settet, og bare en eier (med MFA når det kreves) endrer det,
slik at `staff:manage` aldri kan gi en konto en rolle den selv velger.

1. Opprett rollen blant arbeidsområdets roller, hvis den ikke finnes.
2. Angi hele settet:

   ```bash
   curl -X PUT https://vantigo.example.com/api/v1/identity/owner/provisioning-roles \
     -H 'Content-Type: application/json' \
     -d '{"roleIds":["<rolle-id>"]}'
   ```

3. Kontroller: `GET /api/v1/identity/owner/provisioning-roles` lister rollene etter
   navn, hver med sine rettighetsnøkler. Revisjonsloggen registrerer
   `provisioning-roles.replaced` med settet før og etter.

Forespørselen erstatter settet; en tom `roleIds` tømmer det. Å slette en rolle fjerner
den fra settet. 400 `role_not_assignable` avviser, og ingenting endres, for:

- en innebygd rolle, en systemrolle eller en ukjent rolle;
- en rolle med en rettighet som gir administrasjon — `identity:manage` og enhver annen
  rettighet som ikke kan delegeres, `staff:manage`, `staff:personal-data` eller
  `staff:approve`. Meldingen navngir rollen og nøkkelen.

**Forsiktig:** en rolle i settet gis til hver bruker Ansatte klargjør, og å endre
rettighetene til den rollen senere endrer det alle de brukerne holder, på én gang. Gi
rollen bare det hver ansatt trenger, og gå gjennom settet før du utvider en av rollene
i det. En endring som ville gi en rolle i settet en av rettighetene ovenfor — av en eier
eller av en delegert som forvalter rollen, gjennom
`PUT /api/v1/identity/access/roles/{id}` eller dens `/permissions` — avvises med 403
`role_provisioning_protected`, og ingenting endres; ta rollen ut av settet først. Den
som ikke har myndighet over rollen, avvises for det først (`role_edit_denied`). En
endring av en rolle venter mens settet navngis med den rollen, og å navngi settet mens en
av rollene i det endres, venter på endringen og vurderer så rollen slik endringen
etterlot den — så en rolle som i mellomtiden fikk en av disse rettighetene, avvises med
`role_not_assignable`.

### Hvorfor en invitasjon ble trukket tilbake

Hver tilbaketrukket invitasjon registrerer hvorfor (`identity.invitations.revoked_reason`):

| Årsak | Trukket tilbake av |
| --- | --- |
| `owner` | En eiers tilbaketrekking; også hver invitasjon som ble trukket tilbake før denne versjonen |
| `replaced` | En ny invitasjon til samme adresse (en invitasjon eller en ny sending), en konto opprettet for adressen, eller en rettet `BOOTSTRAP_OWNER_EMAIL` |
| `delivery_failed` | E-posten med invitasjonen kunne ikke sendes |
| `email_changed` | En eier endret en brukers e-post til, eller fra, invitasjonens adresse; eller Ansattes klargjøring ga den ventende brukeren den navnga en annen adresse |
| `deactivated` | Ansattes klargjøring deaktiverte brukeren den navnga |

Eierens liste over invitasjoner (`GET /api/v1/identity/owner/invitations`, og svarene på
tilbaketrekking og ny sending) viser årsaken som `revokedReason` på hver tilbaketrukket
invitasjon, og utelater den på en som ikke er trukket tilbake.

### Hvem som har en tillatelse

Identitet svarer andre moduler på hvem som har en tillatelse (for eksempel Ansatte, for
å vite hvem som kan avgjøre et fravær). En bruker har den gjennom en rolle som er tildelt
dem direkte, gjennom en rolle som er koblet til en aktiv tilgangsgruppe de er medlem av
(tvunget inn, eller til stede i katalogen og ikke overstyrt ut — den samme regelen hver
tillatelsessjekk bruker), eller ved å være Eier, som har alle tillatelser. En delegering
av autorisasjon lar mottakeren administrere roller; den gir ingen tillatelse i seg selv.
Bare aktive brukere teller: en deaktivert bruker, en ventende bruker som ennå ikke har
godtatt invitasjonen sin, og — så lenge SCIM er konfigurert — en bruker katalogen har
deaktivert, med mindre det er en Eier, har ingenting. En utestengelse er midlertidig og
endrer ikke hvem som har en tillatelse. For å gjøre noen til innehaver, gi dem en rolle
med tillatelsen, eller koble en slik rolle til en gruppe de er med i.

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

Det samme gjelder en lesing på vegne av en bruker uten noen økt bak — i dag en persons
kalenderadresse, som en mobil leser med et token i stedet for en informasjonskapsel.
Identitetsmodulen svarer på den gjennom `UserHolds`, der kontrollene av personen er én
spørring, `UserMayAct`, som bruker øktspørringens egne: ikke deaktivert, ikke ventende,
og mens SCIM er konfigurert ikke deaktivert av katalogen med mindre de er Owner — og
ikke utestengt, som hver rettighetskontroll krever. De to spørringene holdes i takt, så
en deaktivering, en utestengning, en deaktivering i katalogen eller en rolleendring
stopper en slik lesing på aller neste forespørsel, slik den stopper en økt
([Kalender → strømmen](/nb/admin/calendar/#kalenderstrømmen-på-den-offentlige-verten)).

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
| `UserManagement` | 30 / minutt | Eiers brukeradministrasjon, å ta inn brukere under Ansatte og ansattrollene |
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

Kalender sender også gjennom den. En invitasjon til noen utenfor bedriften går ut fra
`SMTP_FROM` under arrangørens navn, med `SMTP_FROM` som invitasjonens arrangør, og en
kollega på en avtale får en melding fra identitetsmodulen på adressen identitetsmodulen
har; med en annen driver enn `smtp` sendes ingen invitasjon, og `GET /calendar/meta`
svarer `invitationsAvailable: false`. En invitasjon Gmail skal legge i mottakerens
kalender, trenger at domenet til `SMTP_FROM` består SPF, DKIM og DMARC
([Kalender](/nb/admin/calendar/#invitasjoner-til-folk-utenfor-bedriften)).

Dette er identitetsmodulens applikasjonspost, og Fakturaers og Kalenders. Kommunikasjons
postkasselegitimasjon per kanal er noe helt annet — den konfigureres gjennom
Kommunikasjons-API-et og forsegles i hvile, aldri via miljøvariabler. Se
[Kommunikasjon](/en/reference/communications/).

## Nøkkelmateriale

`APP_SECRET` er nøkkelmaterialet for hele prosessen: minst 32 byte, som hver nøkkel
prosessen bruker utledes fra med HKDF-SHA256 — én utledet nøkkel per formål — og brukes
som AES-256-GCM (CSRF- og informasjonskapselforsegling, OIDC-state, kryptering av
TOTP-hemmeligheter, OIDC-klienthemmeligheten, kanalpassord i Kommunikasjon).
Formålsstrengen bindes inn som ekstra autentiserte data, slik at en verdi forseglet for
ett formål ikke kan åpnes under et annet, og en nøkkel-id-byte gir rom for et
framtidig rotasjonsopplegg.

**Det finnes ingen lagret nøkkelring, ingen nøkkelinnpakkingstjeneste og ikke noe
eksternt nøkkelhvelv.** Ingenting trenger klargjøring utover selve variabelen. To
konsekvenser betyr noe i drift:

- **Alle replikaer må dele samme `APP_SECRET`** (og samme database), ellers kan ikke
  økter utstedt av én replika leses av en annen.
- **Å miste eller rotere den tilsvarer å miste en signeringsnøkkel**: hver åpen økt,
  hver pågående OIDC-flyt, hver lagret TOTP-hemmelighet, den forseglede
  OIDC-klienthemmeligheten og hver forseglet kanallegitimasjon blir umulig å
  gjenopprette. Roter bare med en migreringsplan, og lagre klienthemmeligheten på
  kortet Single sign-on igjen etterpå.

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
| `PUBLIC_BASE_URL` | `https`-URL-en betalingssiden i Faktura og svarsiden i Kalender kan nås på fra internett: en opprinnelse og `APP_BASE_PATH`, ingenting annet, med et ASCII-vertsnavn (punycode for et internasjonalisert navn) uten avsluttende punktum, og aldri vertsnavnet til `APP_URL` på en annen port; på en egen vert serveres bare den offentlige vertens tillatelsesliste der. Uten den finnes det ingen betalingslenker, og svarlenken i en invitasjon og en persons kalenderadresse (strømmen) står på `APP_URL` ([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten), [Kalender](/nb/admin/calendar/#forutsetninger), [strømmen](/nb/admin/calendar/#kalenderstrømmen-på-den-offentlige-verten)) | ikke satt |
| `PORT` | Lytteport | `8080` |
| `DATABASE_URL` | Kjøretidstilkoblingen, rollen med minst rettigheter | **påkrevd** |
| `MIGRATIONS_DATABASE_URL` | Tilkoblingen `migrate` bruker (eierrollen) | `DATABASE_URL` |
| `SHUTDOWN_TIMEOUT` | Budsjett for å tømme pågående forespørsler og arbeidere | `30s` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `MODULES` | Kommaseparert liste over forretningsmoduler som skal betjenes | `customers,products,energy,communications,projects,time,expenses,invoices,staff,calendar` |
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

**Disse variablene leses for én import, og konfigurerer ikke lenger noe.**
Leverandøren og SCIM-legitimasjonen ligger i `identity.sso_settings` og settes på
[kortet Single sign-on](/nb/admin/sso-scim/#kortet-single-sign-on). Den første
prosessen som betjener API-et og finner den raden aldri skrevet, mens `OIDC_PROVIDER`
eller `SCIM_TOKEN` er satt, importerer disse inn i den, revisjonslogget `sso-settings.imported` med navnene deres; når
raden er skrevet, ignoreres de, og hver som fortsatt er satt, navngis i en advarsel ved
hver oppstart. Denne utgivelsen validerer dem fortsatt ved oppstart, så fjern dem etter
importen; neste utgivelse avviser dem ved oppstart. Se
[Før kortet](/nb/admin/sso-scim/#før-kortet-miljøvariablene).

| Variabel | Importeres som | Standard |
| --- | --- | --- |
| `OIDC_PROVIDER` | `entra` eller `google`; ikke satt forblir OIDC av | ikke satt |
| `OIDC_AUTHORITY` | Nøyaktig utsteder for leverandøren; over 200 tegn importeres leverandøren av | ikke satt |
| `OIDC_CLIENT_ID` | Entra-GUID, eller en `.apps.googleusercontent.com`-klient-ID; over 200 tegn importeres leverandøren av | ikke satt |
| `OIDC_CLIENT_SECRET` | Klienthemmelighet, forseglet | ikke satt |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` | Absolutt, lesbar assertion-fil (bare Entra); over 500 tegn importeres leverandøren av | ikke satt |
| `AZURE_FEDERATED_TOKEN_FILE` | Plattformlevert reserve for den over | ikke satt |
| `OIDC_ALLOWED_DOMAINS` | Kommaseparert liste over rene DNS-navn (bare Google, påkrevd der) | ikke satt |
| `OIDC_DISPLAY_NAME` | Etikett på innloggingsknappen, avkortet til 100 tegn | `Workforce SSO` |
| `SCIM_TOKEN` | SCIM-bærertokenet, beholdt som sammendrag; ikke satt forblir SCIM av | ikke satt |
| `SCIM_PREVIOUS_TOKEN` | Forrige token under en rotasjonsoverlapping, beholdt som sammendrag | ikke satt |
| `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` | Slutten på overlappingen; RFC 3339, i framtiden, ≤ 24h fram | ikke satt |

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
| `INVOICES_VIPPS_ENABLED` | Av/på-bryter for betaling av fakturaer med Vipps MobilePay; krever også påloggingsdataene til salgsenheten, og for betalingslenker `PUBLIC_BASE_URL` ([Innbetalinger](/nb/admin/payments/)) | `1` |
| `INVOICES_VIPPS_BASE_URL` | Basis-URL for API-et til Vipps MobilePay — en absolutt http- eller https-URL, en avsluttende skråstrek fjernes; `https://apitest.vipps.no` er testmiljøet til Vipps | `https://api.vipps.no` |
| `STAFF_IMPORT_MAX_ROWS` | Personer én import i Ansatte tar og én eksport skriver, fra `5000` til `20000` — ett tall for begge, så en eksport alltid kan importeres tilbake (over 20000 personer ville en eksport bli større enn importens fil på 5 MiB) ([Ansatte](/nb/admin/staff/#importer-og-eksporter-registeret)) | `5000` |
| `STAFF_RETENTION_ENABLED` | Av/på-bryter for oppbevaringsarbeideren i Ansatte, `staff-retention`, som sletter det oppbevaringsreglene ikke lenger lar registeret beholde og fullfører slettingen av dokumentenes filer; slått av slettes ingenting etter tidsplanen, og `POST /retention/run` kjører den fortsatt for hånd ([Ansatte](/nb/admin/staff/#oppbevaring)) | `1` |
| `STAFF_ACCOUNTS_ENABLED` | Av/på-bryter for kontoarbeideren i Ansatte, `staff-accounts`, som klargjør, aktiverer, oppdaterer og deaktiverer brukerne kontoflagget ber om — med flagget på oppretter den personens ventende, deaktiverte bruker også før en Owner har navngitt ansattrollene, og aktiverer ingen før de er navngitt; slått av gjøres ingenting etter tidsplanen — de som slutter, beholder tilgangen til den slås på igjen eller en Owner deaktiverer dem ([Ansatte](/nb/admin/staff/#kontoarbeideren)) | `1` |
| `STAFF_WORKERS_POLL` | Hvor ofte arbeiderne i Ansatte ser etter: en sletterunde på høyst femti personer og en runde i sletteloggen, og høyst femti kontolenker som står for tur, der en som feilet prøves igjen ved neste kjøring; en varighet på minst `1m` | `1h` |
| `CALENDAR_CANCELLATIONS_ENABLED` | Av/på-bryter for avlysningsarbeideren i Kalender, `calendar-cancellations`, som sletter en avlyst avtale — en som ble slettet etter at noen utenfor bedriften hadde fått en invitasjon — når den siste dagen er passert; slått av beholdes avlyste avtaler til den slås på igjen ([Kalender](/nb/admin/calendar/#avlysningsarbeideren)) | `1` |
| `CALENDAR_WORKERS_POLL` | Hvor ofte arbeiderne i Kalender ser etter: en sletterunde på høyst 200 avlyste avtaler; en varighet på minst `1m` | `1h` |
| `CALENDAR_SUBSCRIPTIONS_ENABLED` | Av/på-bryter for abonnementsarbeideren i Kalender, `calendar-subscriptions`, som henter kalenderne folk abonnerer på fra andre steder gjennom vakten for utgående adresser — ingen privat adresse, ingen videresending, høyst 1 MiB; slått av viser hvert abonnement det det sist leste, og en lagring eller **Oppdater nå** henter det likevel én gang ([Kalender](/nb/admin/calendar/#abonnementsarbeideren)) | `1` |
| `CALENDAR_SUBSCRIPTIONS_POLL` | Hvor ofte hvert abonnement hentes — sjeldnere når kalenderen ber om det, høyst daglig — av arbeideren, som våkner hvert femte minutt; en varighet på minst `5m` | `30m` |
| `CUSTOMERS_IMPORT_ENABLED` | Kjør kundemodulens importjobb, som kontrollerer og importerer opplastede filer; `0` lar importer vente | `1` |
| `CUSTOMERS_IMPORT_POLL` | Hvor ofte kundemodulens importjobb ser etter en import i kø | `5s` |
| `CUSTOMERS_IMPORT_RETENTION` | Hvor lenge en ferdig eller forlatt kundeimport og radene dens beholdes | `720h` |
| `ENERGY_IMPORT_ENABLED` | Kjør energimodulens importjobb, som kontrollerer, importerer og knytter til kunder opplastede målepunktfiler; `0` lar importer vente | `1` |
| `ENERGY_IMPORT_POLL` | Hvor ofte energimodulens importjobb ser etter en import i kø | `5s` |
| `ENERGY_IMPORT_RETENTION` | Hvor lenge en ferdig eller forlatt målepunktimport og radene dens beholdes | `720h` |
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

Høyst én OpenID Connect-leverandør for arbeidsstyrken konfigureres. Den er en del av
raden `identity.sso_settings` — leverandøren, autoriteten, klient-ID-en,
klienthemmeligheten forseglet under `APP_SECRET` eller en tokenfil, de tillatte
domenene og visningsnavnet — som en eier setter på kortet **Single sign-on** (`PUT
/api/v1/identity/owner/sso/oidc`, `policy:Owner+OwnerManagement`: en eier, med MFA så
lenge `OWNERS_REQUIRE_MFA` er på) og hver prosess leser gjennom en mellomlagring på 20
sekunder, så en lagring virker innen 20 sekunder uten omstart. Reglene er de samme som
`OIDC_*`-variablene var underlagt, kontrollert ved lagringen; en avvisning er en `400`
med feltfeil. Relying party bygges på nytt når radens versjon flytter seg.

**Klientautentiseringsmodusen utledes, den konfigureres ikke.** Det finnes ingen
`ClientAuthentication`-innstilling: kortets **Klienthemmelighet** betyr autentisering
med klienthemmelighet, dets **Tokenfil** (en absolutt sti til en vanlig fil på høyst
16 KiB som prosessen som lagrer, kan lese) workload identity. Å lagre begge samtidig
avvises, og det gjør også å lagre ingen av dem, med mindre en klienthemmelighet er
lagret for samme leverandør og klient-ID, som da beholdes; å lagre den ene fjerner den
andre. Workload identity er
bare for Entra; Google er bare klienthemmelighet.

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
e-postdomenet og finnes blant de tillatte domenene.

Nye identiteter klargjøres akkurat i tide som lokale `User`-kontoer, med den validerte
utstederen og `sub` (skiller mellom store og små bokstaver) som nøkkel. E-post fra
leverandøren er til informasjon: en e-postkollisjon **feiler i stedet for å koble
automatisk**. OIDC-claims gir aldri lokale roller og oppfyller aldri det lokale
MFA-kravet.

`GET /api/v1/identity/providers` er anonymt og rapporterer de konfigurerte
innloggingsleverandørene til SPA-en, lest fra raden, så knappen dukker opp innen 20
sekunder etter en lagring.

### Påkrevd tilbakekalls-URI

Registrer det nøyaktige offentlige HTTPS-tilbakekallet hos leverandøren, inkludert
basisstien når en slik er konfigurert:

```text
https://vantigo.example.com/api/v1/identity/oidc/callback
```

Tilbakekallsstien er fast og er ikke en utrullingsinnstilling; kortet **Single
sign-on** viser hele URL-en som **Tilbakekalls-URL**. Den offentlige opprinnelsen, det
videresendte skjemaet og leverandørregistreringen må stemme overens.

## SCIM

SCIM 2.0 betjenes på `/api/v1/identity/scim/v2` og autentiseres med et bærertoken en
eier utsteder med **Nytt token** på kortet **Single sign-on** (`POST
/api/v1/identity/owner/sso/scim/token`): laget av serveren, svart én gang, og beholdt i
`identity.sso_settings` bare som SHA-256-sammendraget. SCIM er på så lenge et token
finnes, og **Slå av** fjerner det. En rotasjon beholder tokenet den erstatter i en
overlapping på høyst 24 timer, som **Avslutt overlapp** avslutter tidlig; begge
sammendragene sammenlignes, det forrige bare til utløpet. Hver prosess leser raden
gjennom en mellomlagring på 20 sekunder. Prosedyrene står i
[SSO- og SCIM-drift](/nb/admin/sso-scim/#scim).

Ressursen User godtar **virksomhetsutvidelsen**,
`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User` (RFC 7643 §4.3):
`employeeNumber`, `costCenter`, `organization`, `division`, `department` og `manager`
(`value`, `displayName`, og `$ref` svart som denne serverens sti), hver på høyst 256
tegn. En opprettelse eller en erstatning med blokken, og en PATCH på hvilken som helst av
stiene dens (`urn:…:User:department`, `urn:…:User:manager.value`, …), godtas og beholdes i
brukerens SCIM-profil under `enterprise`; en erstatning uten den tømmer den. `GET` svarer
med den, `Schemas` og `ResourceTypes` annonserer den (`schemaExtensions`, ikke påkrevd),
og `GET /Users` filtrerer på `urn:…:User:employeeNumber eq "…"`. En sti utenfor de to
skjemaene er fortsatt `invalidPath`. Ingenting i `identity.users` endres av den:
`employeeNumber` er det en [kunngjort konto](#den-kunngjorte-kontoen) matches på, aldri
brukerens ansattnummer.

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
`products`, `energy`, `communications`, `projects`, `time`, `expenses`, `invoices` og
`staff` — migrert av rene SQL-filer innebygd i binærfilen. Alle skjemaer migreres uansett hvilke moduler `MODULES` slår på,
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
