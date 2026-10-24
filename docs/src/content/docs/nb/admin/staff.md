---
title: Ansatte
description: Slå på Ansatte-modulen, gi folk tilgang til den, koble personer til brukerne sine, slå på konto for en person og kontoarbeideren med gruppene den holder, rettigheten for personopplysninger og helsedokumenter, dokumentkategoriene, å svare på en innsynsbegjæring, slettesperre, å ansette en slettet person på nytt, å importere og eksportere registeret, å eksportere en måned til lønn, oppbevaring — slettingen og sletteloggen — tersklene for antall ansatte og tjenestepensjon, fraværstyper og a-meldingskodene deres, ferieordninger, reglene for egenmelding og automatisk overføring, å overføre ferie, å rette et registrert sykefravær, å trekke tilbake retten til egenmelding, å følge en sykefraværssak, omsorgsdagfeltene og omsorgsdagene, hvem som avgjør fravær og e-postene om fravær, e-posten om endret kontonummer, kompetansearbeideren og e-postene dens, og innstillingene — forretningstidssonen, ansattnummerserien, oppbevaringstidene og kontoinnstillingene.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
  - apps/server/internal/staff/document_categories.go
  - apps/server/internal/staff/personal_data.go
  - apps/server/internal/staff/legal_hold.go
  - apps/server/internal/staff/erased.go
  - apps/server/internal/staff/people_import.go
  - apps/server/internal/staff/people_export.go
  - apps/server/internal/staff/csvfile.go
  - apps/server/internal/staff/payroll.go
  - apps/server/internal/staff/retention.go
  - apps/server/internal/staff/retention_worker.go
  - apps/server/internal/staff/object_deletions.go
  - apps/server/internal/staff/thresholds.go
  - apps/server/internal/staff/absence_types.go
  - apps/server/internal/staff/holiday_schemes.go
  - apps/server/internal/staff/absence_settings.go
  - apps/server/internal/staff/adjustments.go
  - apps/server/internal/staff/deciders.go
  - apps/server/internal/staff/notifications.go
  - apps/server/internal/staff/competence_worker.go
  - apps/server/internal/staff/competence_mails.go
  - apps/server/internal/staff/cases.go
  - apps/server/internal/staff/sickness.go
  - apps/server/internal/staff/care_days.go
  - apps/server/internal/staff/account_flag.go
  - apps/server/internal/staff/account_settings.go
  - apps/server/internal/staff/accounts_decide.go
  - apps/server/internal/staff/accounts_worker.go
  - apps/server/internal/staff/accounts_run.go
  - apps/server/internal/staff/accounts_attention.go
  - apps/server/internal/staff/accounts_provider.go
  - apps/server/internal/staff/accounts_decide_provider.go
  - apps/server/internal/staff/accounts_spec.go
  - apps/server/internal/staff/account_seed.go
  - apps/server/internal/staff/account_pass.go
  - apps/server/internal/staff/directory_profile.go
  - apps/server/internal/staff/holders.go
  - apps/server/internal/staff/groups_sync.go
---

Ansatte er bedriftens register over menneskene som jobber for den: personopplysningene
deres, arbeidsforholdene og bedriftens struktur. Denne siden er driftssiden: å slå på
modulen, slippe folk inn, og innstillingene. Hva modulen gjør og reglene den sjekker,
står i [referansen](/en/reference/staff/).

## Før du begynner

- Ansatte trenger ingen annen forretningsmodul. Usatt `MODULES` slår den på sammen med
  alle andre moduler; en installasjon som lister `MODULES` legger `staff` til i
  listen, gjerne alene (`MODULES=staff`).
- Migreringen oppretter skjemaet `staff` enten modulen er slått på eller ikke, så å
  slå den på senere trenger ingen migrering. Hvis databasen kjører en
  applikasjonsrolle med minste privilegium, trenger den skjemaet `staff` tildelt som
  alle de andre ([Installasjon](/nb/admin/installation/)).
- Personers dokumenter lagres i objektlageret, så å arkivere et dokument krever
  `STORAGE_PROVIDER=fs` og en lagringsrot
  ([Objektlagring](/nb/admin/object-storage/)). Uten det virker alt annet, og en
  opplasting eller nedlasting svarer 503 `storage_unavailable`.

## Slå på modulen

1. Legg `staff` til i `MODULES` i `vantigo.env`, eller la `MODULES` stå usatt.
2. Start Vantigo på nytt (`docker compose up -d`).
3. Kontroller: `GET /api/v1/staff/meta` svarer 401 uten en sesjon, ikke 404 som en
   avslått moduls sti svarer.

En avslått modul bidrar ikke med noen rute og ingen rettighet; dataene dens blir
liggende urørt i databasen.

## Gi folk tilgang

Hver operasjon i Ansatte krever `staff:access`, og fire rettigheter ligger over den:

| Rettighet | For |
| --- | --- |
| `staff:access` | Alle som skal se sine egne opplysninger. |
| `staff:view` | Kolleger som skal se katalogen og bedriftens struktur. |
| `staff:approve` | Den som avgjør alles fravær: godkjenner, avslår og trekker tilbake søknader, ser alles arbeidsopplysninger og fravær (en HR-type som permisjon), godkjenningsinnboksen og kalenderen, og åpne sykefraværssaker (milepælene, ikke notatene) — ikke lønn, private opplysninger eller historikk. De bekrefter også alles kompetanse unntatt sin egen. |
| `staff:manage` | HR: personer, arbeidsforhold, dokumenter unntatt helsedokumenter, dokumentkategoriene, kompetansekatalogen og alles kompetanse, strukturen og innstillingene. Sensitiv. |
| `staff:personal-data` | Den som får vise fødselsnumre og kontonumre, og lese og arkivere helsedokumenter. Sensitiv. |

Owner har dem alle allerede. Alle andre trenger en rolle som gir dem: under
**Administrer arbeidsområde → Roller og tilgang** oppretter eller redigerer du en rolle og
gir den rettighetene i kategorien «Staff»
([Administrer roller og tilgang](/nb/user/workspace-administration/#administrer-roller-og-tilgang)).
Ingen innebygd rolle har noen av dem.

## Koble personer til brukerne sine

En person ser sine egne opplysninger — Min side i Ansatte-appen,
`GET /api/v1/staff/me` — først når HR har koblet personen til brukeren de logger inn
som. Koblingen gjør den brukeren til personen selv for Ansatte og ingenting annet: den
gir ingen rettighet, og den setter aldri brukeren under Ansattes forvaltning. Enhver
aktiv bruker kan kobles, også Owner.

1. Som en bruker med `staff:manage` åpner du personens side. Under **Selvbetjening** på
   fanen Profil skriver du en del av brukerens navn i **Bruker** — opptil tjue aktive
   brukere foreslås — velger dem og trykker **Koble**
   ([brukerveiledningen](/nb/user/staff/#koble-personen-til-brukeren-sin-hr)). **Koble
   fra** fjerner koblingen. Gjennom API-et: `GET /api/v1/staff/assignable-users?q=`, så
   `PUT /api/v1/staff/people/{id}/user` med `{"userId": "<brukerens id>"}`, eller
   `{"userId": null}` for å fjerne koblingen.
2. Kontroller: gruppen viser *… — koblet, ikke styrt av Ansatte*; personen logger inn,
   og Min side viser opplysningene deres (`GET /api/v1/staff/me` svarer med dem i
   stedet for `no_person`). Personens historikk viser `user_linked`.

En bruker kan bare kobles til én person, og en person til én bruker; å koble en person
til en annen bruker erstatter koblingen. En deaktivert bruker kan ikke kobles.

En bruker som Ansattes kontoer forvalter, kobles ikke til eller fra her: Selvbetjening
viser en slik kobling som *… — styrt av Ansatte; se fanen Konto* og tilbyr verken
**Koble** eller **Koble fra**, og forespørselen svarer `link_managed` (409), som siden
sier med ord — slik den gjør for en person hvis kobling er kontoarbeiderens (personens
Konto-fane viser tilstanden). Kan ikke identity spørres om brukeren forvaltes, svarer
forespørselen `accounts_unavailable` (503), siden sier at identitet ikke kunne nås, og
ingenting endres; prøv igjen. En installasjon der ingen modul tilbyr kontoer, svarer slik på hver kobling
og frakobling som navngir en bruker.

## Slå på konto for en person

Ansatte kan gi en person en bruker i identity, klargjort før de begynner og aktivert på
første arbeidsdag (leveranse C). Kontoflagget settes her; kontoarbeideren
([nedenfor](#kontoarbeideren)) handler på det.

1. Før den første kontoen navngir en Owner rollene Ansatte kan gi i identity
   (ansattrollene, under **Roller Ansatte kan gi** på siden Roller og tilgang). Kortet
   **Kontoer** i Ansatteinnstillinger advarer *Ingen eier har angitt rollene Ansatte kan
   gi* til en har gjort det, med **Åpne Roller og tilgang**. Det har også **Standard
   kontotype** en ny person får, og viser identitetsleverandørene en installert modul
   tilbyr, hver med status. Gjennom API-et svarer `GET /api/v1/staff/meta`
   `provisioningRolesSet`, `defaultAccountKind` og `providers` til `staff:manage`.
2. Som en bruker med `staff:manage` åpner du personens fane **Konto**
   (`/staff/people/<id>?tab=account`). Under **Kontoflagg** slår du på **Ansatte gir en
   konto**.
3. La **Konto fra** stå tom for å aktivere kontoen på første dag i personens tidligste
   arbeidsforhold som ikke har sluttet — feltet sier hvilken dag det er — eller velg en
   annen dag. Mellom to arbeidsforhold hører en dag fra før det første sluttet til det,
   og kontoen følger den senere startdatoen; en dag etter det følges. Velg **Privat
   e-post** under **Adresse** for å gi identitet personens private e-post i stedet for
   jobb-e-posten, og **Kontotype** — *Lokal*, *Ekstern identitetsleverandør* eller
   *Microsoft Entra ID* ([kontotypen](#kontotypen)). Trykk **Lagre kontoflagget**.
   Gjennom API-et er det
   `PUT /api/v1/staff/people/{id}/account` med `If-Match` satt til flaggets egen
   `revision` fra `GET` — aldri personens: å endre flagget flytter aldri personens
   revisjon — og alle tre feltene, `{"provisionAccount": true, "provisionFrom": null,
   "accountEmailKind": "work"}`, med `accountKind` når den endres (utelatt beholdes
   typen); en uten `provisionAccount`, eller med den `null`,
   avvises (400) og endrer ingenting.
4. Kontroller: **Konto fra** navngir dagen kontoen aktiveres (`provisionFromEffective`),
   og fanen viser ingen advarsel om at personen *har ingen e-post på jobb registrert*
   (`address.present` er `true`). Et flagg som slås på uten den valgte adressen godtas,
   men ingen konto lages før personen har den. Personens historikk viser
   `account_flag_updated`.

Hvor mange dager før den dagen kontoen klargjøres, deaktivert, er **Forbered kontoen
(dager før)** på kortet Kontoer (`createBeforeDays`, [Innstillingene](#innstillingene)).
En slettet persons flagg kan ikke endres (`person_erased`): fanen tilbyr HR bare
**Deaktiver nå** på en slettet person. Har noen andre lagret flagget først, svarer
forespørselen `revision_conflict`: fanen lastes inn på nytt og sier det.

### Kontotypen

Hver persons konto holdes av én av tre typer, som Ansatte gir identitetsmodulen ved hvert
kall:

| Type | Hvem som har kontoen | Hva Ansatte gjør |
| --- | --- | --- |
| `local` (*Lokal*) | Vantigos identitetsmodul | Oppretter brukeren, inviterer dem, gir ansattrollene |
| `external` (*Ekstern identitetsleverandør*) | Selskapets identitetsleverandør, med SSO eller SCIM | Finner brukeren leverandøren laget når en Owner har tatt den inn; aktiverer igjen det Ansatte deaktiverte; deaktiverer |
| `entra` (*Microsoft Entra ID*) | Microsoft Entra ID, gjennom en leverandørmodul | Lar Entra opprette, aktivere, oppdatere og deaktivere kontoen; kunngjør den, så brukeren som kommer, tas inn uten en Owner; gir ansattrollene én gang |

**Ved oppgraderingen** som innførte typene, satte Ansatte hver persons type og
standarden ut fra installasjonens autoritetsmodus, én gang, første gang den startet —
`local` fra lokal, `external` fra føderert (SSO eller SCIM på) — og logger `staff: the
account kinds were seeded from the authority mode` med typen. Kunne identity ikke
spørres i det øyeblikket — eller var en workers innstillinger uenige med den registrerte
modusen — logger den en advarsel og prøver igjen ved neste oppstart eller kontorunde.
Innstillingenes revisjon flytter seg bare når standarden ble `external`, så en
innstillingsside som var åpen under en seeding som lot `local` stå, lagrer som vanlig.
Ingenting endrer noens type etterpå, bortsett fra HR.

**Til seedingen har kjørt**, leser hver person `local`, som kanskje ikke er regelen
deres, så Ansatte vurderer ingenting ut fra en type: arbeideren kjører bare
deaktiveringer, lar alle andre koblinger være som de var og logger `staff: the account
kinds are not yet seeded; only deactivations run` én gang per runde; **Kjør nå** for en
person, og en endring av en persons **Kontotype** eller av **Standard kontotype**,
svarer 503 `accounts_unavailable`, som sier at typene ikke er seedet ennå, og endrer
ingenting. Hver av dem prøver seedingen igjen først, så de virker så snart identity
svarer; kontroller med logglinjen over.

**For å endre standarden**: Ansatteinnstillinger → **Kontoer** → **Standard kontotype**,
så **Lagre kontoinnstillingene** (`defaultAccountKind` på `PUT /settings`). Bare nye
personer får den. **For å endre én persons**: Konto-fanen deres → **Kontotype**. Typen
`entra` avvises `provider_missing` med mindre en aktivert modul tilbyr leverandøren; å
forlate `entra` avvises `account_kind_locked` (409) mens personens Entra-kobling navngir
en konto Entra ikke har slettet — aktivert, deaktivert, eller stoppet på den, med mindre
Entra meldte den borte — så deaktiver dem med **Deaktiver nå**, eller la arbeidsforholdet
ta slutt, og endre typen når kontoen er slettet. En endring som går gjennom før Entra
laget en konto, trekker tilbake kunngjøringen av kontoen til Vantigo når ingen
Vantigo-bruker har kommet ennå; en kobling personen beholder under Entra, deaktiveres
fortsatt for en som slutter eller med **Deaktiver nå**, og ingenting annet gjøres med
den. Å flytte en person fra `local` til `entra` mens
Vantigo-brukeren deres fortsatt venter på invitasjonen, er tillatt: brukeren Entra
bringer, tar over den.

**Typen `entra` krever** at leverandørmodulen er installert og at tilkoblingen virker:
kortet **Kontoer** viser *Microsoft Entra ID: tilkoblet.* Til da — *ikke tilkoblet
ennå* eller *feiler* — lager og endrer arbeideren ingenting i katalogen for disse
personene utenom deaktiveringen av en som slutter (som Entra fortsatt kan avvise mens
legitimasjonen ikke virker), gjør ingenting for dem i Vantigo utenom å deaktivere dem som
slutter, og dashbordet viser ett punkt for installasjonen. En person med en type som
navngir en leverandør ingen aktivert modul tilbyr, holdes igjen på samme måte. Sett opp **SCIM** fra Entra til Vantigo slik
[SSO- og SCIM-drift](/nb/admin/sso-scim/#virksomhetsutvidelsen-og-ansattnummeret)
sier, eller la personene logge inn med Entra: den kunngjorte kontoen tas inn idet den
kommer, uansett vei.

### Kontoarbeideren

Arbeideren `staff-accounts` handler på flagget hver `STAFF_WORKERS_POLL` (standard `1h`)
— i worker-modus, og i api-modus med `WORKERS_IN_PROCESS=1`
([Installasjon](/nb/admin/installation/)) — med mindre `STAFF_ACCOUNTS_ENABLED=0`. For en
person med flagget på gjør den dette:

- `createBeforeDays` før den effektive dagen oppretter den brukeren i identity, ventende
  og deaktivert, og kobler den (lenkens `state` er `pending`);
- på dagen aktiverer den brukeren med ansattrollene og sender en invitasjon til å velge
  passord; lenken er `active` når invitasjonen er godtatt;
- dagen etter at siste arbeidsforhold slutter, deaktiverer den brukeren og avslutter
  øktene deres (`disabled`); en gjenansettelse aktiverer dem igjen på den nye startdatoen;
- holder brukerens navn, språk og ansattnummer i takt med personen — og adressen bare
  mens brukeren fortsatt venter og ikke har fått tilsendt noen invitasjon.

**Å slutte deaktiverer uansett.** Når flagget slås av, slutter Ansatte å opprette,
aktivere og oppdatere personens konto, men ikke deaktiveringen ved slutten: når alle
arbeidsforhold er avsluttet (ingen senere), deaktiveres en bruker Ansatte styrer gjennom
sin egen lenke dagen etter siste sluttdag, uansett hva flagget sier. En bruker HR koblet
for hånd (selvbetjeningslenken) er HRs og blir latt være; en Owner eller en SystemAdmin
deaktiveres aldri av Ansatte — en annen Owner fratar dem rollen og deaktiverer dem. For å
la en som slutter, beholde tilgangen, lar en Owner Ansatte slutte å styre brukeren i
identity; personen mister da Min side til HR kobler brukeren til dem igjen for hånd.

Hver person behandles én gang om dagen, ved første kjøring etter midnatt i
forretningstidssonen. Flere arbeidere, eller en arbeider og en kjøring for hånd, deler
arbeidet person for person; ingen venter på en annen.

1. Forutsetning: en Owner har navngitt ansattrollene (`provisioningRolesSet` er `true`).
   Uten dem oppretter arbeideren likevel de ventende, deaktiverte brukerne til personene
   med flagget på, men aktiverer aldri én (`employee_role_missing`).
2. Kontroller dagen etter at arbeideren har kjørt: personens Konto-fane viser under
   **Brukerkonto** **Tilstand** med ord, hvorfor lenken holdes tilbake når den gjør det,
   *Mislykkede forsøk på rad: …* etter en feil, **Invitasjon** og **Neste forsøk**
   (`GET /api/v1/staff/people/{id}/account` svarer det samme som en `link` med `state`,
   `reason`, `attempts` og `nextAttemptAt`). Personens historikk (som HR) viser `account_prepared`,
   `account_activated`, `account_deactivated` og `account_updated`; en leder, en
   godkjenner og en kollega ser ingen av dem.
3. For å stoppe den: `STAFF_ACCOUNTS_ENABLED=0` og start på nytt. **Da gjøres ingenting
   etter tidsplanen, heller ikke for dem som slutter**: en bruker hvis arbeidsforhold er
   avsluttet, beholder tilgangen til arbeideren kjører igjen eller en Owner deaktiverer
   dem i identity. Å slå av `staff`-modulen har samme virkning, og etterlater i tillegg
   ventende brukere — klargjort, aldri invitert — som en Owner da fullfører eller sletter
   i identity.

**Typen `external`** (en identitetsleverandør eier kontoen): arbeideren oppretter eller
inviterer aldri noen. Den finner brukeren leverandøren opprettet etter ansattnummeret,
kobler den, aktiverer igjen en bruker den selv deaktiverte, og deaktiverer dem som
slutter; til leverandøren har opprettet brukeren, holdes lenken tilbake med `not_found`
og prøves igjen daglig. En ventende bruker laget for typen `local` før endringen holdes
tilbake med `mode_changed` for en Owner.

**Typen `entra`**: arbeideren arbeider med personens konto i Entra først, så med
Vantigo-brukeren deres, i samme omgang. Før virkningsdagen lar den Entra opprette
kontoen, deaktivert, med feltene i [katalogprofilen](#innstillingene), og kunngjør den
for identity (`account_external_prepared`, `account_announced`); på dagen aktiverer den
kontoen i Entra, som utsteder et midlertidig tilgangspass, og finner Vantigo-brukeren
kontoens ankomst laget — ingen Owner tar den inn — og gir dem ansattrollene én gang; den
skriver endringene for en som bytter stilling, til Entra (`account_external_updated`);
dagen etter siste slutt deaktiverer den kontoen i Entra, så brukeren i Vantigo; og etter
**Slett en ekstern konto (dager etter slutt)**, uten slettesperre, sletter den
Entra-kontoen (`account_external_deleted`), som Entra holder gjenopprettbar i tretti
dager — også etter at personen er slettet i Ansatte. En adresse Entra laget for en person uten e-post på jobb, skrives til personen
(`work_email_set_by_provider`). HR viser passet én gang på personens Konto-fane (**Vis
passet**, `POST /api/v1/staff/people/{id}/account/pass`, `pass_revealed` i historikken);
et nytt forsøk, eller et etter at det gikk ut, er `pass_gone` (409): **Kjør nå (nytt
pass)** utsteder et nytt.

**Grupper.** Hver runde henter også de avledede [gruppene](/nb/user/staff/#grupper) —
medlemmene en gruppes regel gir i dag, hver som kommer inn eller går ut, registrert i
historikken deres (`group_joined`, `group_left`) — før den arbeider med kontoene, men
ikke før seedingen av kontotypene har kjørt, og etter dem synkroniserer den opptil 100
gruppeseksjoner — en gruppe under én leverandør HR har slått på for den — per runde, så
en person som aktiveres i en runde, er med i gruppene sine i samme runde: gruppen lages
i katalogen med eierne, navnet, eierne og verdiene skrives når de endres, medlemmene
settes til de av personene som har en aktivert konto der — når de endres, og én gang i
døgnet uansett, noe som fjerner et medlem lagt til for hånd i katalogen — og en arkivert
gruppe — eller en en leverandør er slått av for — slettes etter **Slett en ekstern konto
(dager etter slutt)** eller, når den står tom, får `… (archived)` etter navnet og
beholdes. Entra sletter bare en Microsoft 365-gruppe den laget: en sikkerhetsgruppe
tømmes og beholdes, og en gruppe den ikke laget, blir stående ([Ansattes grupper i
leietakeren](/nb/admin/entra/#ansattes-grupper-i-leietakeren)). En leverandør som ikke
er tilkoblet, hoppes over og prøves igjen ved neste kjøring. En gruppe leverandøren fant
i katalogen sin i stedet for å lage den, venter, uten noe varsel, til en med
leverandørens rettighet bekrefter den der. En gruppe leverandøren avviste, eller der
kallene mislyktes tre ganger på rad, gir `groupSyncFailed`; en avvist verdi sendes ikke
igjen før HR endrer gruppen, men medlemmene i en gruppe leverandøren har, holdes i
samsvar i mellomtiden. **Kjør kontoer nå** kjører gruppene også. Med `STAFF_ACCOUNTS_ENABLED=0` skjer ingen av
delene.

**Ulik modus.** Arbeideren leser modusen fra innstillingene for single sign-on i
databasen, slik API-et gjør
([autoritetsmodusen](/nb/admin/authentication/#autoritetsmodusen)), så de to er ikke
lenger uenige. Bare en arbeider som fortsatt kjører utgivelsen før kortet **Single
sign-on**, under en rullerende oppgradering, melder det fortsatt: den deaktiverer dem
som slutter, men holder alt annet tilbake, og lenkene viser `reason: mode_mismatch`.
Fullfør oppgraderingen; lenkene som ble holdt tilbake, prøves igjen ved neste
kjøring.

**En lenke som holdes tilbake** (`reason` satt; Konto-fanen sier hver årsak i en
setning) prøves igjen hver dag, så den løser seg
selv når årsaken er borte. Årsakene en operatør møter: `employee_role_missing` og
`employee_role_unsafe` (en Owner navngir, eller retter, ansattrollene),
`account_email_taken` og `employee_number_taken` (en annen bruker i identity har
adressen eller nummeret), `blocked_by_owner` og `disabled_by_owner` (en Owner deaktiverte
brukeren; bare en Owner opphever det), `account_privileged` (brukeren er Owner eller
SystemAdmin og håndteres for hånd), `account_not_managed` (en Owner lot Ansatte slutte å
styre brukeren), `mismatch` (brukeren har et annet ansattnummer: en annens konto),
`user_gone` (brukeren ble slettet i identity), `erasure_incomplete` (personen er slettet
i Ansatte, men identity har ennå ikke slettet brukeren — brukeren er fortsatt aktivert,
er Owner eller SystemAdmin, styres ikke lenger av Ansatte, eller identity svarte ikke;
oppbevaringsarbeideren prøver igjen ved hver kjøring), `invitation_revoked` og
`invitation_unanswered` (en Owner trakk invitasjonen tilbake, eller tre invitasjoner ble
ikke besvart). På en Entra-konto: `provider_unavailable` (Entra ikke tilkoblet, eller
feiler), `external_ambiguous` (to kontoer i Entra har ansattnummeret),
`external_address_taken` (e-posten på jobb er en annen kontos i Entra),
`external_synced_on_premises`, `external_privileged` (kontoen har en katalogrolle),
`external_enabled` (en sletting av en konto som fortsatt er aktivert) og `user_gone`
(kontoen er borte fra Entra; Ansatte lager aldri en til). En lenke der `attempts` når tre, kom ikke fram til identity: den prøves
daglig fra da av, og loggen navngir feilen — men deaktiveringen av en som har sluttet,
prøves ved hver kjøring så lenge den feiler, så tilgangen forsvinner så snart identity
svarer.

### Kjør kontoene nå

Arbeideren kan kjøres for hånd, for alle eller for én person, med deg som aktøren
identity registrerer og historikken navngir.

- **Alle**: **Kjør kontoer nå** på kortet **Kontoer** i Ansatteinnstillinger
  (`POST /api/v1/staff/accounts/run`, `staff:manage`) kjører én runde nå; kortet sier så
  hvor mange kontoer den behandlet, hvor mange som ble ferdige, hvor mange som mislyktes
  og hvor mange som er igjen (`{attempted, done, failed, remaining}`): koblingene den arbeidet med, de identity
  svarte på (et steg utført, eller en årsak registrert), de den ikke nådde identity for
  (prøves igjen av arbeideren), og de forfalte koblingene den lot ligge. Den begynner
  ingen kobling — og klargjør ingen — etter seksti sekunder og tar høyst femti; `remaining` er for arbeiderens
  neste runde, eller kjør den igjen. Den kjører side om side med arbeiderens egne runder
  uten å vente på dem.
- **Én person**: **Kjør nå** på personens Konto-fane
  (`POST /api/v1/staff/people/{id}/account/run`) arbeider med personen nå, uansett neste
  forsøk, og ber identity om en **ny invitasjon**: en ventende bruker der en Owner trakk
  invitasjonen tilbake, eller som lot tre være ubesvart (`invitation_revoked`,
  `invitation_unanswered`), inviteres på nytt. Fanen sier *Ferdig: en ny invitasjon ble
  sendt* eller *Ferdig: ingen invitasjon ble sendt* (`{invitationSent, account}`).
  Identity sender aldri på nytt en invitasjon som fortsatt kan godtas, og sender ingen
  innen ti minutter etter den forrige; kjøringen er like fullt gjort — prøv igjen etter
  ti minutter om det trengs. En person med flagget av får ingenting kjørt, og fanen
  tilbyr ikke Kjør nå. For en `entra`-person er det **Kjør nå (nytt pass)**: et nytt
  pass bes om fra Entra bare når kjøringen aktiverer en deaktivert konto — en nyansatt
  på første dag, en som ansettes på nytt — og vises med **Vis passet**; en konto som
  allerede er aktivert, får ikke noe nytt pass og bringes bare i samsvar. En avvisning
  fra Entra er `provider_refused` (409) med koden sin. Så lenge kontotypene ikke er
  seedet, svarer Kjør nå 503 og kjører ingenting.
- **Grenser**: fra én klientadresse høyst 30 kjøringer av alle kontoer i timen, og
  høyst 120 av en persons Kjør nå, Deaktiver nå og Vis passet til sammen i timen; over grensen
  avvises forespørselen (429) — vent, eller la arbeiderens egen runde gjøre det. Antallet
  telles per klientadresse og deles av alle på den adressen: bak en omvendt proxy setter
  du `TRUSTED_PROXY_HOPS` eller `TRUSTED_PROXY_CIDRS` ([Autentisering](/nb/admin/authentication/)),
  ellers teller hver forespørsel som proxyens.

Hva kan gå galt, hvert sagt med ord på fanen eller kortet: `accounts_busy` (409) —
arbeideren arbeider med personen akkurat nå; prøv igjen om noen minutter. `person_erased` (409) — en slettet persons konto kjøres
ikke. `accounts_unavailable` (503) — ingen modul tilbyr kontoer, eller identity svarte
ikke; koblingen registrerer feilen og arbeideren prøver igjen.

En kjøring fortsetter når nettleseren eller en mellomtjener gir opp å vente. Det
realistiske verste tilfellet er flere minutter — hvert kall til identity kan vente opptil
to minutter, og identity sender invitasjonene på e-post — så når identity eller
e-posttjeneren er treg, kan forespørselen gå ut på tid i nettleseren mens arbeidet
fortsatt blir gjort: les Konto-fanen, eller dashbordet, igjen etterpå i stedet for å
kjøre den to ganger.

### Deaktiver en konto nå

**Deaktiver nå** på personens Konto-fane
(`POST /api/v1/staff/people/{id}/account/disable`, `staff:manage`) **slår av personens
kontoflagg** og deaktiverer brukeren straks, så arbeideren lar den være deaktivert til
HR slår flagget på igjen.

1. Trykk **Deaktiver nå** og bekreft — dialogen sier *Dette slår også av
   kontoflagget*. Flagget slås av først (historikken viser `account_flag_updated`)
   sammen med en merknad på koblingen om at en deaktivering er bedt om, så deaktiverer
   identity brukeren (`account_deactivated`) og avslutter øktene. For en `entra`-person
   deaktiveres Entra-kontoen først (`account_external_deactivated`), så
   Vantigo-brukeren; en avvisning fra Entra er `provider_refused` (409) med koden sin, og
   en deaktivering Entra ikke bekreftet — Entra ikke tilgjengelig, kontoen ikke lesbar,
   eller ingen aktivert modul som tilbyr den — `accounts_unavailable` (503), som sier at
   katalogen ikke kunne nås. Flagget er allerede av uansett, identitys egen avvisning
   svares først, og deaktiveringen står som bedt om: arbeideren gjør den ferdig ved neste
   kjøring. Personens egen revisjon
   flytter seg ikke. Gir nettleseren opp å vente, fortsetter deaktiveringen likevel; det
   som ikke når fram til identity, gjør arbeideren ferdig ved de neste kjøringene.
2. Kontroller: fanen sier *Kontoen er deaktivert og kontoflagget er av* — for en
   `entra`-person *Kontoen og kontoen i katalogen er deaktivert, og kontoflagget er av*,
   eller *Kontoflagget er av og kontoen i katalogen er deaktivert.* når det ikke fantes
   noen Vantigo-bruker ennå — **Ansatte gir
   en konto** er av, og **Tilstand** viser *Deaktivert av Ansatte* (svaret har
   `provisionAccount: false` og koblingen `state: disabled`). En person uten kobling, eller der brukeren ikke er opprettet ennå,
   får bare flagget slått av.
3. Hva kan gå galt — fanen sier hvert med ord, og de som kommer etter at flagget er
   skrevet, sier at flagget allerede er av:
   - `accounts_busy` (409): arbeideren holder personens kobling. Som regel ble ingenting
     endret — prøv igjen om noen minutter. Tok arbeideren koblingen i øyeblikket etter at
     flagget ble skrevet, **er flagget allerede av**: da gjør arbeideren deaktiveringen
     ferdig selv.
   - `account_not_managed` (409) på HRs egen selvbetjente kobling: Deaktiver nå virker
     bare på kontoer Ansatte styrer. Flagget er av, ingenting annet gjøres og ingenting
     prøves igjen — fjern koblingen, eller en Owner deaktiverer brukeren i identity.
   - `account_not_managed` (409) på arbeiderens kobling, der en Owner lot Ansatte slutte å
     styre brukeren; `account_privileged` (409): brukeren er Owner eller SystemAdmin.
     Flagget er av, men brukeren forblir aktiv: en Owner deaktiverer dem i identity, eller tar
     brukeren inn for Ansatte igjen (eller fjerner Owner-rollen). Ansatte prøver
     deaktiveringen igjen daglig — så når en Owner har tatt brukeren inn igjen eller
     fjernet rollen, deaktiverer neste forsøk dem — og dashbordet viser
     *accountNotManaged* eller *accountPrivileged* til det er gjort eller brukeren er
     deaktivert i identity.
   - `account_mismatch` (409): brukeren koblingen navngir har et annet ansattnummer — en
     annens konto — og ingenting gjøres med dem. Flagget er av; koblingen viser
     `mismatch` og dashbordet viser *accountMismatch*. Rett personens ansattnummer: neste
     daglige forsøk deaktiverer da brukeren.
   - `accounts_unavailable` (503): identity svarte ikke. **Flagget er allerede av**, men
     brukeren er fortsatt aktiv: koblingen registrerer feilen, arbeideren prøver
     deaktiveringen igjen ved hver kjøring, og dashbordet viser *accountFailing* til
     identity svarer. Send forespørselen på nytt om det vedvarer.

En slettet persons konto kan også deaktiveres: på en slettet person tilbyr Konto-fanen
HR **Deaktiver nå** og ikke noe annet.

### Kontopunktene på dashbordet

`staff:manage` ser ett punkt per person på dashbordet når arbeideren står fast for dem,
lest fra Ansattes egne data (identity spørres ikke når dashbordet lastes). Hvert punkt
navngir personen og ikke noe mer; åpne Konto-fanen deres for detaljene. Bare personer
med flagget på som ikke er slettet listes, unntatt *accountFailing* for en deaktivering
som er bedt om og ikke har nådd identity — også for en slettet person, med tittelen «An
erased person» — *erasureIncomplete* for en slettet person hvis bruker i identity ennå
ikke har blitt slettet (med tittelen «An erased person»), og *accountFailing* for en som
har sluttet og hvis konto arbeideren ikke fikk deaktivert.

| Punkt | Hva det betyr | Hva du gjør |
| --- | --- | --- |
| `accountNoEmployment` | Flagget er på, men personen har ikke noe arbeidsforhold | Legg til arbeidsforholdet, eller slå flagget av |
| `accountEmailMissing` | Personen har ingen adresse av typen flagget navngir | Legg til jobb- (eller privat) e-post, eller bytt adressevalget |
| `accountNotFound` | Typen `external`: identitetsleverandøren har ikke opprettet brukeren ennå | Opprett brukeren hos leverandøren med personens ansattnummer |
| `accountNotManaged` | En Owner lot Ansatte slutte å styre brukeren — eller, med flagget av, en Deaktiver nå som ble avvist for en bruker Ansatte ikke styrer, prøvd igjen daglig | En Owner tar brukeren inn igjen (neste forsøk deaktiverer dem) eller deaktiverer dem i identity; HR fjerner en selvbetjent kobling. Til da har personen ingen Min side: HR fjerner koblingen og kobler brukeren for hånd igjen (**Bruker** på personsiden) for å gi den tilbake |
| `accountBlockedByOwner` | En Owner deaktiverte den ventende brukeren | En Owner aktiverer dem i identity, om de skal begynne |
| `accountDisabledByOwner` | En Owner deaktiverte brukeren mens personen er ansatt | En Owner avgjør; Ansatte opphever det ikke |
| `accountUserGone` | Brukeren ble slettet i identity | Fjern koblingen, eller en Owner gjenoppretter kontoen |
| `accountPrivileged` | Brukeren er Owner eller SystemAdmin — også etter en Deaktiver nå som ble avvist for dem, prøvd igjen daglig; en som slutter og er det, forblir aktiv, siden Ansatte aldri deaktiverer en Owner eller en SystemAdmin | En annen Owner fratar dem rollen og deaktiverer dem i identity (uten rollen deaktiverer neste forsøk dem) |
| `accountMismatch` | Brukeren har et annet ansattnummer: en annens konto — også etter en Deaktiver nå som ikke gjorde noe med den | Sjekk personens ansattnummer; når det er riktig, kjøres deaktiveringen |
| `accountModeChanged` | En ventende bruker laget for typen `local`, mens personens type nå er `external`, som aldri inviterer | En Owner fullfører eller sletter brukeren |
| `accountEmailDiffers` | Brukerens adresse i identity er ulik personens | En Owner endrer den i identity (Ansatte endrer den bare mens brukeren venter og ingen invitasjon er levert til dem) |
| `accountEmailTaken` | En annen bruker i identity har adressen | Rydd opp i duplikatet i identity, eller endre personens adresse |
| `accountNumberTaken` | En bruker Ansatte ikke styrer har ansattnummeret | En Owner tar den brukeren inn for Ansatte, eller rydder i nummeret |
| `accountFailing` (flagget på) | Identity svarte ikke tre ganger på rad | Sjekk identity og loggen, og kjør så Kjør nå |
| `accountFailing` (flagget av, en som har sluttet) | Personen har sluttet (alle arbeidsforhold avsluttet, ingen senere), og arbeideren fikk ikke deaktivert brukeren Ansatte styrer for dem, tre kjøringer på rad — de kan fortsatt logge inn | Sjekk identity og loggen; arbeideren prøver igjen ved hver kjøring, og punktet forsvinner når brukeren er deaktivert |
| `accountFailing` (flagget av, eller en slettet person) | En Deaktiver nå har ikke nådd identity — feilet, eller ventet mer enn ti minutter | Arbeideren prøver igjen ved hver kjøring og stopper når identity svarer (en bruker som alt er deaktivert, teller); send Deaktiver nå på nytt om det vedvarer |
| `erasureIncomplete` | Personen er slettet, og identity kunne ennå ikke slette brukeren Ansatte styrte for dem (fortsatt aktivert, Owner eller SystemAdmin, ikke lenger styrt av Ansatte, eller identity ikke tilgjengelig; med tittelen «An erased person») | Deaktiver brukeren, fjern rollen eller ta brukeren inn for Ansatte igjen i identity, eller sjekk identity — oppbevaringsarbeideren prøver igjen ved hver kjøring |
| `invitationUnanswered` | Tre invitasjoner ble ikke besvart | Sjekk adressen; Kjør nå sender en ny |
| `invitationRevoked` | En Owner trakk invitasjonen tilbake | Kjør nå sender en ny, om personen skal ha den |
| `employeeRoleMissing` | Ingen ansattrolle er navngitt (ett punkt, med antall) | En Owner navngir rollene Ansatte kan gi |
| `employeeRoleUnsafe` | En rolle navngitt for Ansatte gir administrasjon (ett punkt) | En Owner tar den nøkkelen ut av rollen, eller rollen ut av settet |
| `accountModeMismatch` | En arbeiders OIDC- eller SCIM-innstillinger er ulike API-ets (ett punkt) | Gjør innstillingene like og start arbeideren på nytt |
| `accountNotArrived` | Ansatte kunngjorde personens Entra-konto for nitti dager siden, og ingen Vantigo-bruker har kommet for den | Sjekk at brukeren er innenfor omfanget av Entras klargjøring til Vantigo, eller la personen logge inn |
| `accountAmbiguousInEntra` | To kontoer i Entra har personens ansattnummer | Fjern nummeret fra feil konto i Entra |
| `accountAddressTakenInEntra` | Personens e-post på jobb er en annen kontos i Entra | Endre personens e-post på jobb, eller rydd i den andre kontoen |
| `accountSyncedOnPremises` | Kontoen synkroniseres fra en lokal katalog | Styr den i den katalogen, eller gi personen en annen type |
| `accountPrivilegedInEntra` | Kontoen har en administrativ rolle i Entra | En administrator håndterer den for hånd |
| `accountUnconfirmedInEntra` | Entra har en konto under personens ansattnummer som Vantigo ikke opprettet, og ingen har bekreftet overtakelsen; Ansatte skriver ingenting til den | Sjekk at den er personens, og bekreft den på siden for Microsoft Entra ID ([Kontoer Vantigo ikke opprettet](/nb/admin/entra/#kontoer-vantigo-ikke-opprettet)) |
| `hireDateNotWritten` | Entra avviste ansettelsesdatoen | Ingenting trengs for innlogging; slå av **Ansettelsesdato** i katalogprofilen om det fortsetter å feile |
| `licenceUnavailable` | En lisens kunne ikke tildeles, fordi det ikke er flere igjen | Kjøp eller frigjør en lisens i Microsoft 365 |
| `passUnrevealed` | Et pass ble utstedt for en dag eller mer siden, og ingen har vist det | Vis det til personen, eller Kjør nå (nytt pass) når de er klare |
| `managerNotInEntra` | Personens leder har ingen konto i Entra, eller Entra fant den ikke, så kontoen har ingen leder | Gi lederen typen Microsoft Entra ID, eller godta det |
| `entraNotConnected` | Entra-leverandøren har ingen tilkobling ennå, eller ingen aktivert modul tilbyr den (ett punkt, som teller `entra`-personene med flagget på) | Koble den til i Entra-modulen, eller aktiver modulen |
| `entraFailing` | Entra-leverandørens siste kontroll feilet — en legitimasjon avvist, et samtykke trukket tilbake — eller Ansatte kunne ikke spørre den (ett punkt) | Rett tilkoblingen i Entra-modulen |
| `secretExpiring` | Den nyeste gyldige klienthemmeligheten utløper innen tretti dager (ett punkt) | Legg til en ny hemmelighet i Entra-modulen |
| `secretExpired` | Klienthemmeligheten har utløpt (ett punkt) | Legg til en ny hemmelighet i Entra-modulen |
| `groupSyncFailed` | En gruppe kunne ikke holdes i en leverandørs katalog — en verdi leverandøren avviste, en gruppe med nøkkelen som Vantigo ikke får skrive til, eller tre mislykkede forsøk på rad (ett per gruppe og leverandør, med gruppens navn) | Åpne gruppen under **Grupper** i Ansatte: leverandørkortet sier hvorfor, ved innstillingen det gjelder; endre verdien, eller løs konflikten i katalogen |

## Rettigheten for personopplysninger

Fødselsnummeret og kontonummeret er forseglet: lagret kryptert, vist til ingen — ikke
til HR med `staff:manage`, ikke til personen — bortsett fra nummerets type og
kontoens fire siste sifre. Bare `staff:personal-data` skriver og viser dem, og hver
visning registreres i personens historikk med hvem som så. Gi `staff:personal-data` til
så få som lønnskjøringen trenger; den er en egen rolle ved siden av HR sin med vilje,
slik at det trengs to par hender for å endre hvor noens lønn går.

Verdiene er forseglet under nøkler utledet fra `APP_SECRET`. Å rotere den gjør dem
umulige å åpne og duplikatsjekken for fødselsnumre blind — se
[Installasjon](/nb/admin/installation/#innstillingene-du-må-ta-stilling-til).

**Helsedokumenter** — en persons dokumenter i en begrenset kategori, blant
standardkategoriene `health` — er også `staff:personal-data` sine. HR med bare
`staff:manage` ser dem ikke i personens arkiv, kan ikke åpne, arkivere, flytte eller
fjerne et, og ser ikke i personens historikk at et ble lagt til; personen selv leser
sine egne. Gi `staff:personal-data` til den som håndterer sykmeldinger og korrespondanse
med bedriftshelsetjenesten, og ingen andre.

## Dokumentkategorier

Hvert dokument arkiveres under en kategori, og kategorien avgjør når dokumentet slettes:
dens **oppbevaringsklasse** — A for lønns- og avtaledokumentasjon (fem år etter året
arbeidsforholdet sluttet), D for personaladministrasjon (sammen med resten av
personens opplysninger), E for helse. Seks kategorier finnes fra start: arbeidsavtale og
attest (A), sertifikat, korrespondanse og annet (D), og helse (E). En kategori i klasse
E er begrenset: dokumentene i den er helsedokumenter.

Tabellen under **Ansatteinnstillinger** → **Dokumentkategorier** gjør alt dette på
skjermen ([brukerveiledningen](/nb/user/staff/#dokumentkategorier)). Gjennom API-et legger
du til en egen slik, som en bruker med `staff:manage`:

1. `POST /api/v1/staff/document-categories` med en `key` (1–40 av `a–z`, `0–9` og
   bindestreker), `names` på engelsk og norsk og en `class` som er `A`, `D` eller `E`,
   for eksempel `{"key": "courses", "names": {"en": "Courses", "nb": "Kurs"}, "class": "D"}`.
   Velg klassen med omhu: den endres aldri, fordi slettingen leser den fra hvert dokument
   som er arkivert under kategorien.
2. Kontroller: `GET /api/v1/staff/document-categories` lister den etter
   standardkategoriene.

For å gi en nytt navn, eller slutte å tilby den, sender du `names` og `archived` til
`PUT /api/v1/staff/document-categories/{id}` med revisjonen i `If-Match`. En arkivert
kategori tilbys ikke for nye dokumenter, og bare en kategori ingen dokumenter er arkivert
under, kan arkiveres. De seks standardkategoriene endres aldri.

## Fraværstyper

Fravær registreres i et vokabular av typer: ferie, egenmelding, sykmelding, de
lovfestede permisjonene, avspasering, lege- eller tannlegetime og resten — 22
standardtyper som hver installasjon starter med, hver knyttet til permisjonsbeskrivelsen
den rapporteres som i a-meldingen ([referansen](/en/reference/staff/#absence-types)).

På skjermen, som en bruker med `staff:manage`: åpne **Ansatteinnstillinger** og finn
**Fraværstyper**.

1. Tabellen viser alle typene: navnet, **Art**, **Hvem ser typen** (*Leder og HR*, eller
   *Bare HR*), hva den **Rapporteres i a-meldingen som**, reglene og om den er
   **Forhåndsdefinert** eller **Skjult**. De fire reserverte typene slettingene skriver
   (*Fravær*, *Fravær (timer)* og de to tilsvarende bare for HR), vises ikke.
2. **Ny fraværstype** legger til en egen: en **Nøkkel**, **Navn på engelsk** og **Navn på
   norsk**, **Art** og hva den **Telles i** (begge låst fra da av), hvem som ser den,
   permisjonsbeskrivelsen og bryterne **Må avgjøres**, **Lønnet**, **Trekkes fra
   feriekontoen** og **Kan være en prosent av dagen** (de to siste for en type som telles
   i dager). Trykk **Legg til**.
3. Blyanten (**Endre** *type*) åpner en type. En forhåndsdefinert type endrer bare
   navnene, **Rekkefølge**, om den er **Skjult** og om den er **Lønnet** — de andre
   feltene vises nedtonet; en egen type endrer alt unntatt nøkkel, art og enhet.
   **Ammefri** forblir lønnet: skjemaet sier hvorfor og nekter å lagre den ulønnet.
4. **Skjul** slutter å tilby en type for nytt fravær (fravær som har den, beholder den);
   **Tilby** tar den tilbake. Alle felt du ikke endret, sendes tilbake som de var.
5. En avvisning står i skjemaet: en endring av en forhåndsdefinert types regler, ammefri
   satt til ulønnet, eller å åpne en type som bare er for HR, for kolleger mens fravær
   har den.

Gjennom API-et, som en bruker med `staff:manage`:

1. Les listen: `GET /api/v1/staff/absence-types` svarer med alle typene, de skjulte også
   for deg.
2. For å legge til en egen, `POST /api/v1/staff/absence-types` med en `key` (1–40 av
   `a–z`, `0–9` og understreker), `names` på engelsk og norsk, en `kind` — `holiday`,
   `leave`, `time_off` eller `other` — en `unit` (`days` eller `hours`), flaggene `paid`,
   `drawsHoliday`, `allowsPercent` og `needsDecision`, en `visibility` (`absent`, eller
   `hr_only` for en type som avslører særlige kategorier av personopplysninger) og, for
   en permisjon, `ameldingLeave` den rapporteres som. Typen og enheten endres aldri.
3. For å gi en type nytt navn, flytte den eller slutte å tilby den, sender du den tilbake
   til `PUT /api/v1/staff/absence-types/{id}` med `hidden` og `sort` slik du vil ha dem og
   revisjonen i `If-Match`. En standardtype endrer bare navnene, plassen, om den er
   skjult og `paid`; alt annet — og `ammefri` med `paid: false` — avvises med
   `seed_immutable`. En egen type kan når som helst gjøres
   til bare for HR, men når noen har tatt den som bare for HR, kan den ikke åpnes for
   kolleger og ledere (`absence_type_in_use`): legg heller til en ny type. En type gjøres
   heller ikke til bare for HR mens et fravær av den som personen selv godkjente, ikke er
   over (også `absence_type_in_use`): vent til det er over. Å gjøre en
   type som er i bruk til bare for HR virker bakover: fra da av viser radene som alt er
   lagt inn, bare *permisjon* for ledere, og søknadene, avslagene og historikken deres
   skjules også for dem. En egen HR-type tillater verken prosent eller trekk fra
   feriekontoen: skjemaet avviser begge, siden en andel eller et feriefradrag ville
   fortelle ledere hva slags permisjon det er.
4. Kontroller: `GET /api/v1/staff/meta` lister typene folk kan velge — uten de skjulte.

**Merk en standardtype lønnet når dere betaler utover loven.** Standardtypene sier hva
loven gir lønn for: en virksomhet der avtalen gir lønn under for eksempel
foreldrepermisjon eller omsorgspermisjon, slår **Lønnet** på for den typen, og av igjen
om avtalen opphører. Flagget er til opplysning — ingen tall leser det, heller ikke
lønnseksporten, som rapporterer dager og timer etter art — så det forteller HR og lønn
hva virksomheten har bestemt, ikke mer. **Ammefri** forblir
lønnet: den er lønnet innenfor grensene i aml. § 12-8 (2) — inntil én time per dag i barnets første leveår ved arbeidsdag på sju timer eller mer; ammefri utover det er ulønnet etter loven med mindre annet er avtalt — registrer den med en egen type.
**Svangerskapskontroll** forblir også lønnet: aml. § 12-1 gir en gravid arbeidstaker
fri med lønn til svangerskapskontroller som ikke rimelig kan finne sted utenom
arbeidstiden.

**Velg a-meldingskoden etter permisjonens rettsgrunnlag.** A-meldingen tar imot seks
permisjonsbeskrivelser og permittering. Velferdspermisjon er avtalt, ikke lovfestet, så
standardtypen `velferdspermisjon` rapporteres som «andre ikke lovfestede permisjoner»
(`andreIkkeLovfestedePermisjoner`); hviler velferdspermisjonen deres på en lov, bruker
dere typen `annen_lovfestet` for den i stedet. Kodene `velferdspermisjon` og
`utdanningspermisjon` gikk ut ved utgangen av 2022 og avvises. Bare permisjon på mer enn
fjorten dager rapporteres — og flere kortere permisjoner innenfor tolv måneder som til
sammen passerer fjorten dager, telles sammen — og omsorgsdager for sykt barn rapporteres
aldri.

## Ferieordninger

En ferieordning er bedriftens ferieregel, i virkedager (mandag til lørdag, slik
ferieloven teller). Den lovfestede ordningen — 25 dager, 6 til fra 60, 18 i
hovedferieperioden, 6 ved start etter 30. september, 12 dagers avtalt overføring —
finnes fra start og er standardordningen. Hver ordning svares med tallene omregnet til
arbeidsdager i en femdagersuke: 25 virkedager er 21 dager, 30 er 25.

For å gi noen eller alle ansatte mer, som en bruker med `staff:manage`, på skjermen:

1. Åpne **Ansatteinnstillinger**, finn **Ferieordninger** og trykk **Ny ferieordning**.
   Tabellen viser hver ordnings **Ferierett** og **Mest overført** som «30 virkedager =
   25 dager i en femdagersuke», og hvilken som er **Standard**.
2. Fyll inn **Navn**, **Ferierett (virkedager)**, **Ekstra fra 60 år (virkedager)**,
   **Hovedferie (virkedager)**, **Etter september (virkedager)** og **Mest overført
   (virkedager)**; hvert felt viser hva det blir i en femdagersuke. Skjemaet holder
   ferielovens gulv før noe sendes. Slå på **Standard** for å gjøre den til ordningen for
   alle arbeidsforhold uten ordning. Trykk **Legg til**.
3. Sett et arbeidsforhold på den: under personens **Arbeidsforhold**, **Endre
   arbeidsforholdet** og velg **Ferieordning**.
4. **Arkiver** slutter å tilby en ordning; **Gjenopprett** tar den tilbake. Å arkivere
   standarden, eller en ordning et arbeidsforhold følger, avvises med ord.

Gjennom API-et:

1. `POST /api/v1/staff/holiday-schemes` med et `name` og tallene, for eksempel
   `{"name": "Fem uker", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6}`
   (hovedferieperioden, dagene etter september og overføringen er 18, 6 og 12 når de
   utelates). Ferielovens grenser gjelder: minst 18 dager i hovedferieperioden, minst 6
   ved start etter 30. september, og avtalt overføring på høyst 12 dager pluss dagene
   over 25 — 17 for en ordning på 30 dager. Legg til `"isDefault": true` for å gjøre den til ordningen for hvert
   arbeidsforhold som ikke nevner noen.
2. Sett et arbeidsforhold på den: `holidaySchemeId` på
   `POST /api/v1/staff/people/{id}/employments` eller `PUT /api/v1/staff/employments/{id}`.
   Utelatt følger arbeidsforholdet standardordningen.
3. Kontroller: `GET /api/v1/staff/holiday-schemes` lister den, med `converted`.

For å slutte å tilby en ordning sender du den til `PUT /api/v1/staff/holiday-schemes/{id}`
med `"archived": true`. Standardordningen kan ikke arkiveres, og heller ikke en ordning et
arbeidsforhold følger (`holiday_scheme_in_use`): flytt arbeidsforholdene først. Det finnes
alltid nøyaktig én standardordning; gjør en annen til standard for å flytte den.

## Reglene for egenmelding

Hvor mye egenmelding en ansatt kan bruke, er installasjonens regel. På skjermen: åpne
**Ansatteinnstillinger**, finn **Egenmelding og overføring**, velg **Regler** — *Lovens
minimum (ftrl. § 8-23)*, *Utvidet (IA)* eller *Egne tall* — og for egne tall **Dager per
tilfelle**, **Tilfeller per år** og **Dager per år**; skjemaet avviser tall under lovens
gulv før noe sendes. Sett **Måneder i arbeid først** og **Overfør ferie som ikke er tatt,
automatisk** i samme kort, og trykk **Lagre**. Kortet sender de generelle innstillingene
tilbake som de er, så ingenting annet endres.

Gjennom API-et, på `PUT /api/v1/staff/settings` (`staff:manage`):

- `selfCertPolicy: "minimum"` — lovens regel: 3 kalenderdager om gangen, 4 ganger på
  tolv måneder. Standard.
- `selfCertPolicy: "extended"` — 8 kalenderdager om gangen, 24 dager på tolv måneder,
  slik en IA-avtale gir.
- `selfCertPolicy: "custom"` med `selfCertDaysPerOccurrence` (3–16),
  `selfCertOccurrencesPerYear` (4–52) og `selfCertDaysPerYear` (12–365). Bunnen er
  lovens minimum (ftrl. § 8-23), som ingen regel går under.

Med en forhåndsinnstilling gjelder dens tall, uansett hva du sender.
`selfCertAfterMonths` (0–2, standard 2) er månedene i arbeid før egenmelding kan brukes,
under alle reglene. Reglene brukes på hver egenmelding når den registreres: en som går
forbi det de tillater, avvises, og den ansatte får beskjed om å skaffe sykmelding. En
egenmeldingsperiode er dagene på rad, og en helg eller helligdag mellom to egenmeldinger
teller som dager i den — med minimumsregelen er egenmelding fredag og igjen mandag fire
dager. Dager en sykmelding dekker mellom to egenmeldinger, binder dem også sammen, som
én sykdom, uten å telle som egenmeldte dager.

**Automatisk overføring.** `autoCarryOver: true` overfører ferie som står igjen ved
årsskiftet til neste år uten at HR gjør noe, som tvungen overføring. Dagen du slår den på
lagres (`autoCarryOverSince`), og ingen år før den overføres automatisk. Den er av som
standard.

## Overfør ferie

Ferie tatt på forskudd før året den opptjenes i (*forskuddsferie*, inntil 12 virkedager
etter skriftlig avtale, ferieloven § 7 nr. 3), modelleres ikke: kontoen fører ingen forskudd. Når forskudd avtales, registrerer du det
som en **Retting** med en merknad, i året den tas og tilbake i året den opptjenes.

Ferie som står igjen ved årsskiftet, forsvinner ikke. HR overfører den til neste år. På
skjermen: åpne personens **Fravær**-fane, bla til året med **Forrige år**, og trykk
**Overfør fra** *år*. Velg **Type** — *Avtalt* eller *Tvungen* (med en **Merknad**) — la
**Dager** stå tomt for det som er igjen, og trykk **Overfør**. En ny overføring fra samme
år avvises med ord. **Feriejusteringer** på samme fane viser overføringene og rettingene;
**Legg til justering** registrerer en retting (med merknad), og søppelbøtta sletter en.

Gjennom API-et, med `POST /api/v1/staff/people/{id}/carry-over` (`staff:manage`):

1. Les personens konto for året den kommer fra:
   `GET /api/v1/staff/people/{id}/holiday-account?year=2025` — `remaining` er det som står
   igjen.
2. Registrer overføringen med `fromYear` og `kind`:
   - `agreed` — det du og den ansatte avtalte, begrenset av ferieordningens maksimum
     (12 virkedager, 10 arbeidsdager, etter lovens ordning). Utelat `days` for å overføre
     dagene som står igjen, opp til grensen.
   - `forced` — dager den ansatte ikke kunne ta (sykdom i ferien, for eksempel), uten
     grense, med en `note` som sier hvorfor.
3. Se på kontoen for neste år: dagene står i `carriedIn`.

En person har **høyst én overføring fra et år**: en andre gir `carry_over_recorded`. For
å endre den, slett den (`DELETE /api/v1/staff/holiday-adjustments/{id}`; listen er
`GET /api/v1/staff/people/{id}/holiday-adjustments`) og registrer den på nytt. En
rettelse av en konto — en dag registrert feil, for eksempel — er en justering med
`reason: "correction"` og en merknad; en justering som er en overføring, oppgir året
rett før året den endrer.

**Automatisk overføring.** Med `autoCarryOver` slått på i innstillingene overfører en
konto selv dagene som stod igjen året før (`carriedInDerived`), som tvungen overføring —
men bare til et år der 1. januar er samme dag som eller etter dagen den ble slått på:
slått på 12. september 2026 overfører den først 2026s dager til 2027. Den rekker aldri
inn i et år som er i gang, der HR har gjort opp overføringen for hånd. En overføring registrert for hånd for et år erstatter den
automatiske.

## Rett et registrert sykefravær

En egenmelding eller en sykmelding er registrert, ikke avgjort, og den ansatte kan ikke
endre den når den først er lagt inn. På personens **Fravær**-fane har en registrert rad
eller en søknad **Endre**, en godkjent rad **Rett**, og **Registrer fravær** legger inn en
sykmelding for personen; arket sender med det raden har for å vokte den, og har noen endret
raden imens, lukkes det og lastes på nytt i stedet for å overskrive. Gjennom API-et:
slutter en sykmelding tidligere, eller var en dag
feil, endrer HR den (`PUT /api/v1/staff/absences/{id}`, `staff:manage`, med revisjonen i
`If-Match`) eller fjerner den (`DELETE /api/v1/staff/absences/{id}`). Begge føres i
personens historikk. En endring holder raden en registrering: å gjøre en sykmelding om
til ferie, for eksempel, avvises (`absence_status_change`) — fjern den og legg inn
ferien. Fraværsendringer er `staff:manage` sine; `staff:personal-data` alene skriver dem
ikke. Et fravær noen har avgjort, fjernes aldri; det trekkes tilbake
eller rettes (`POST /api/v1/staff/absences/{id}/correct`). En endring i et sykefravær
utleder personens sykefraværssak på nytt — dagene, arbeidsgiverperioden og datoene for
milepælene — og beholder det som er markert på milepælene.

## Trekk tilbake retten til egenmelding

Når en ansatt har brukt egenmelding slik at det gir grunn til det (ftrl. § 8-27), kan HR
trekke tilbake retten for en periode. På skjermen: personens **Fravær**-fane, kortet
**Egenmelding**, **Trekk tilbake egenmelding** — **Til og med** og **Grunn** — og så
**Trekk tilbake**; **Opphev tilbaketrekkingen** avslutter den tidligere. Gjennom API-et:

1. `PUT /api/v1/staff/people/{id}/self-certification/withdrawal` (`staff:manage`) med
   `until` — en dag etter i dag, høyst ett år fram — og `reason`.
2. Sjekk `GET /api/v1/staff/people/{id}/self-certification`: `withdrawnUntil` og
   `withdrawnReason` står der.
3. Fra i dag og fram til den dagen avvises egenmelding for personen
   (`self_certification_withdrawn`); vedkommende registrerer sykmelding i stedet.
   Egenmelding for dager før du registrerte tilbaketrekkingen, står seg som en egen rad:
   en som går inn i tilbaketrekkingen, avvises i sin helhet. Å forlenge en
   tilbaketrekking som gjelder, beholder dagen du først registrerte den.

For å oppheve den før tiden, send `{"until": null}`. Tilbaketrekkingen og opphevingen
står i personens historikk for personen selv, HR og `staff:personal-data` — aldri for
lederen, som ser personens egenmeldingsstatus uten den.

## Følg en sykefraværssak

Hver egenmelding og sykmelding hører til en sykefraværssak, som Ansatte holder selv: et
sykefravær som begynner høyst seksten dager etter det forrige, hører til samme sak, og
ett som begynner senere, åpner en ny. `GET /api/v1/staff/cases` viser de åpne sakene du
følger — personens leder ser sine medarbeideres, HR og den som har `staff:approve`, alle.
På skjermen er den åpne saken et kort på personens **Fravær**-fane (og, for en leder, på
**Mitt team**): **Marker som gjort** og **Frafall** på hver milepæl, **Endre
tilretteleggingsnotatet**, og for HR **Endre anslaget** (med **Tilbakestill til første
dag + 364**) og **Lukk saken**; den som har `staff:personal-data`, har også **Endre
notatet** på hver milepæl. Et helsedokument arkiveres under saken fra personens
**Dokumenter**-fane: endre dokumentet og velg **Sykefraværssak**.

1. **Arbeidsgiverperioden.** Milepælen `employer_period_end` er sakens sekstende
   sykedag, der helger og helligdager mellom to sykefravær telles med, og tellingen
   begynner på en arbeidsdag (et sykefravær fra en lørdag telles fra mandagen); før
   det har den ingen dato. Arbeidsgiveren betaler de dagene når den ansatte har arbeidet der i fire
   uker.
2. **Oppfølgingen.** Oppfølgingsplanen forfaller fire uker etter første dag, første
   dialogmøte etter sju, NAVs aktivitetskrav etter åtte og andre dialogmøte etter
   tjueseks. En milepæl vises som aktuell bare mens saken er åpen og har vart til en uke
   før den, så et kort sykefravær gir ingenting å følge opp. Merk en som gjort
   (`POST /api/v1/staff/cases/{id}/milestones/{kind}/done`, i dag hvis du ikke sender
   `doneAt`, aldri før sakens første dag) eller frafall den som åpenbart unødvendig med
   en begrunnelse (`.../waive`) — aldri en diagnose; bare den ansatte og
   `staff:personal-data` leser begrunnelsen. Lederen, HR og `staff:approve` kan det.
   Slutten på arbeidsgiverperioden følger av sykedagene og markeres ikke.
3. **Notater.** Tilretteleggingsnotatet (`PUT /api/v1/staff/cases/{id}`) gjelder
   tilrettelegging — hva den ansatte kan og ikke kan — og aldri en diagnose; lederen og
   HR skriver det. En milepæls eget notat er `staff:personal-data` sitt, og bare den
   ansatte og `staff:personal-data` leser det. Et helsedokument arkiveres under saken
   med `caseId` på `PUT /api/v1/staff/documents/{id}`.
4. **52-ukersgrensen** anslås som første dag pluss 364 dager; HR setter NAVs dato med
   `maxBenefitEstimate` og sender den som null for å gå tilbake til anslaget. Et felt du
   utelater i `PUT /api/v1/staff/cases/{id}`, beholdes. NAV teller de 248
   sykepengedagene på tvers av sykefravær med mindre den ansatte har vært arbeidsfør i
   26 uker imellom (ftrl. § 8-12), så en tidligere sak kan flytte grensen fram.
5. **Avslutningen.** En sak avsluttes av seg selv seksten dager etter siste dag. HR
   avslutter den tidligere med `POST /api/v1/staff/cases/{id}/close`; et sykefravær som
   hører til den senere, åpner den igjen.

Når et sykefravær fyller gapet mellom to saker, blir de to én: den eldste beholder id-en
sin og tar over den andres markerte milepæler og notater, lagt etter sine egne. Et
notat som ikke lenger får plass, kuttes ikke: den gjenværende sakens notat slutter med
en henvisning, og den andre sakens notater står hele i sakens historikk.

En sykmelding over en egenmelding korter egenmeldingen inn, eller fjerner den når den
dekker den helt.

## Utsett ferie ved sykdom

En sykmelding over innvilget ferie endrer ikke ferien av seg selv: om ferien utsettes,
er den ansattes krav, med legeerklæring (ferieloven § 9). Imens viser dashbordet ditt
det: *… var sykmeldt i godkjent ferie*. På skjermen har sykmeldingens rad på personens
**Fravær**-fane **Utsett ferie**: sett **Krevd den** og trykk **Registrer kravet**; en
avvisning står i dialogen.

1. Sjekk at sykmeldingen er 100 %: en gradert sykmelding gir ikke rett til utsettelse.
2. Når den ansatte krever det, registrer kravet på sykmeldingen:
   `POST /api/v1/staff/absences/{id}/postpone-holiday` (`staff:manage`) med
   `demandedOn`, dagen kravet ble satt fram (i dag hvis den utelates).
3. Ansatte deler ferien rundt sykmeldingen, og dagene går tilbake til feriekontoen;
   svaret viser ferien slik den nå står, og punktet forsvinner fra dashbordet.

Sykdom som begynte **før** ferien, må være krevd senest siste arbeidsdag før den; et
senere krav avvises (`holiday_postponement_too_late`), og dashbordet slutter å vise en
slik ferie når den har begynt. Sykdom **i** ferien gir erstatningsdager; den ansatte
skal kreve dem uten ugrunnet opphold etter at arbeidet er gjenopptatt. Et krav dateres
aldri før sykmeldingens første dag, og loven krever ikke et minste antall sykedager. En sykmelding over ferie som bare er søkt om,
lar søknaden stå; godkjenningen avvises da.

## Omsorgsdagfeltene

En persons omsorgsdager utledes av tre felt på personen, som HR fyller inn ut fra det
den ansatte opplyser — ingen barn navngis:

- **Barn under 12** (`childrenUnder12`) — barna til og med året de fyller tolv, som
  **ikke** har vedtak fra NAV om utvidet rett.
- **Kronisk syke eller funksjonshemmede barn** (`chronicallyIllChildren`) — barna **med**
  vedtak fra NAV om utvidet rett, til og med året de fyller atten.
- **Alene om omsorgen** (`soleCarer`) — om den ansatte har omsorgen alene.

Tell hvert barn én gang: et barn med vedtak fra NAV føres bare i det andre feltet. På
skjermen er de **Barn under 12**, **Aleneomsorg** og **Barn med ekstra omsorgsdager** i
**Endre opplysninger** på personens **Profil**, og profilen viser dem som **Barn og
omsorgsdager**; et felt som står tomt, er ikke registrert. Feltene skrives sammen med
resten av personen (`PUT /api/v1/staff/people/{id}`); den ansatte ser dem på sin egen
side, men kan ikke endre dem.

Telleren er `GET /api/v1/staff/people/{id}/care-days?year` (den ansatte, HR og
`staff:personal-data`): 10 dager for ett eller to barn, 15 for tre eller flere, 20 og
30 for den som er alene om omsorgen, og 10 til per kronisk sykt eller funksjonshemmet
barn (20 for den som er alene). Årets arbeidsdager med sykt barn telles mot den; de
første ti betaler arbeidsgiveren selv, resten refunderer NAV — fra første dag når de
eneste barna som gir rett, er over tolv med utvidet rett, noe lønn bruker for hånd. Mer
enn retten vises, men avvises aldri; dagene som regnes som refundert, stopper ved retten
når feltene er registrert, i telleren og i lønnsfilen. Retten krever fire ukers ansettelse — regnet fra
starten av en sammenhengende rekke ansettelser, der en som begynner dagen etter at en
annen sluttet, fortsetter den: før det sier svaret `eligible: false` og fra hvilken
dag, og en dag med sykt barn før det telles for seg (`ineligibleDays`), ikke som
omsorgsdag.

## Svar på en innsynsbegjæring

En person har rett til en kopi av personopplysningene som behandles om dem
(personvernforordningen art. 15, innsyn). Ansattes fullstendige svar er
personopplysningsfilen HR henter ut:

1. Som en bruker med `staff:personal-data` åpner du personen under **Personer**, trykker
   **Last ned personopplysninger** øverst på siden og bekrefter
   ([brukerveiledningen](/nb/user/staff/#last-ned-en-persons-opplysninger);
   `GET /api/v1/staff/people/{id}/personal-data`). Svaret er en nedlasting,
   `staff-<ansattnummer>-personal-data.json`: personens opplysninger i alle
   feltgrupper og feltene for omsorgsdager, fødselsnummeret og kontonummeret i sin
   helhet, HRs notater, pårørende, rollene, hvert arbeidsforhold med vilkårene (lønn og
   notater medregnet), brukerkoblingen, listen over dokumenter, alt fravær,
   ferieendringene, hver sykefraværssak med oppfølgingspunktene og notatene deres, årets
   omsorgsdager, egenmeldingsstatusen med en eventuell tilbaketrekking, og hele
   historikken.
2. Last ned hvert dokument filen lister, fra personens fane **Dokumenter** (eller
   `GET /api/v1/staff/documents/{id}`); filen har metadataene deres, ikke innholdet.
3. Kontroller: personens **Historikk** begynner nå med **Personopplysninger
   eksportert**, med deg som den som gjorde det. Hver slik eksport registreres, fordi
   filen åpner de forseglede verdiene.

Overlever filen slik bedriften overleverer personopplysninger — ikke med vanlig
e-post. Personen kan også laste ned sin egen kopi fra Min side
(`GET /api/v1/staff/me/export`); den utelater de forseglede verdiene, HRs notater og
notatene på vilkårene, så den er ikke det fullstendige svaret, og den registrerer
ingenting.

## Sett en slettesperre på en person

En slettesperre (legal hold) holder en persons opplysninger hele — ingen sletting etter
oppbevaringsreglene rører dem, og personen kan ikke slettes — så lenge en tvist, et krav
eller en undersøkelse trenger det.

1. Som en bruker med `staff:personal-data` åpner du personen og trykker **Sett
   slettesperre** øverst på siden. Skriv **Grunn** — påkrevd, inntil 500 tegn: hva
   sperren gjelder og gjerne hvor lenge — og trykk **Sett slettesperren**
   ([brukerveiledningen](/nb/user/staff/#en-slettesperre);
   `PUT /api/v1/staff/people/{id}/legal-hold` med `{"held": true, "reason": "…"}`).
2. Kontroller: siden viser **Under slettesperre** med dagen og grunnen (personens
   `legalHold` er `{"held": true, …}`), og **Historikk** har **Slettesperre satt**.
3. Når saken er avsluttet, trykker du **Opphev slettesperren** og bekrefter
   (`{"held": false}`, uten grunn). Grunnen og hvem som satte den, fjernes; historikken
   beholder begge.

En slettet person kan fortsatt få og miste en slettesperre; ingenting annet ved dem
endres.

## Ansett en slettet person på nytt

Når oppbevaringskjøringen har slettet en tidligere ansatt, er opplysningene skrivebeskyttet:
hver endring svarer `person_erased`, bortsett fra en slettesperre, en ny vurdering, å
fjerne koblingen til brukeren og en nyansettelse. Personsiden sier det i en melding og
tilbyr ikke noe annet ([brukerveiledningen](/nb/user/staff/#en-slettet-person)).

1. Før du legger til noen som kan ha jobbet her før, fyller du inn **Fornavn**,
   **Etternavn** og **Fødselsdato** i **Ny person** (`staff:manage`). Ved **Lagre og
   fortsett** lister skjemaet slettede personer med samme navn og fødselsdato, med
   ansattnummer og når de ble slettet (`POST /api/v1/staff/people/similar-erased` med
   `{"givenName", "familyName", "birthDate"}`). Fødselsnummeret kan ikke avgjøre det —
   slettingen fjernet det.
2. Er det samme person, trykker du **Åpne** med ansattnummeret, går til
   **Arbeidsforhold** og trykker **Ansett på nytt**
   (`POST /api/v1/staff/people/{id}/employments`). Opplysningene åpnes igjen under samme
   ansattnummer; historikken får **Ansatt på nytt** før det nye arbeidsforholdet.
3. Skriv inn igjen det slettingen fjernet — kontaktopplysninger, adresse, pårørende,
   fødselsnummeret og kontonummeret (de to siste med `staff:personal-data`).
4. Er det en annen, trykker du **Opprett likevel**; de slettede opplysningene blir
   stående.

For å koble personens bruker til nye opplysninger i stedet, trykker du først **Koble
fra** under Selvbetjening på de slettede (`PUT /api/v1/staff/people/{id}/user` med
`{"userId": null}`) — den ene endringen av koblingen slettede opplysninger tillater.

## Importer og eksporter registeret

Registeret flyttes som én semikolonseparert CSV-fil — formen et norsk Excel åpner og
lagrer som CSV uten en importdialog. Alle tre stegene krever `staff:manage` og ligger på
kortet **Importer og eksporter personer** i **Ansatteinnstillinger**
([brukerveiledningen](/nb/user/staff/#importer-og-eksporter-personer)); hvert av dem er
også et endepunkt, nevnt nedenfor. Kolonnene, reglene og kodene står i
[referansen](/en/reference/staff/#the-registers-import-and-export).

### Importer et register

1. Last ned malen: **Last ned mal**
   (`GET /api/v1/staff/people/import-template.csv`). Den er overskriftsraden alene — personens kolonner, det første arbeidsforholdets, og de forseglede bare hvis du
   har `staff:personal-data`. Eller start fra en eksport.
2. Fyll inn én rad per person. `employeeNumber` er påkrevd på hver rad: et nummer ingen
   har, legger til personen, et nummer en person har, endrer dem. For en ny person fyller
   du inn `startDate` for å legge til det første arbeidsforholdet også — `businessUnit`
   med navn eller organisasjonsnummer (tomt er standardenheten), `department` med koden,
   `location` med navnet, `managerEmployeeNumber` med lederens ansattnummer. En leder
   filen legger til, må komme på en tidligere rad. En person som allerede er i registeret,
   får bare sine egne kolonner endret — aldri et arbeidsforhold: dem endrer du i appen.
3. Lagre den som CSV UTF-8 med semikolon, og sjekk den: **Importer personer**, velg
   filen, **Kontroller** (`POST /api/v1/staff/people/import` med filen i
   multipart-delen `file`). Kontrollen er en **prøvekjøring**: hver rad kjøres og rulles
   tilbake, og siden viser hva radene ville gjort — opprettet, oppdatert, med feil — og
   hvert problem med rad og kolonne. Uten `dryRun=false` gjør endepunktet det samme.
4. Rett det som feilet, og kontroller igjen. Når kontrollen er ren, trykker du
   **Importer**, som sender den samme filen med `?dryRun=false`.
5. Kontroller: tallene er de du venter, personene er i listen, og historikken til hver
   person viser importens endringer under ditt navn.

En rad som feiler, skriver ingenting og stopper ikke de andre; rader som allerede er
importert, blir stående, så send en rettet fil med bare de feilede radene — **Last ned
feilede rader** gir deg dem tilbake slik de var skrevet, bak en `error`-kolonne importen
ser bort fra — eller hele filen på nytt: rader som ikke endrer noe, skriver ingenting. En import som avbrytes
underveis — forbindelsen brytes midt i filen — beholder radene den kjørte, og
installasjonens historikk registrerer den likevel med tallene så langt: `rows` er hele
filen, og de opprettede, endrede og feilede radene blir til sammen færre. Filen er høyst 5 MiB og
`STAFF_IMPORT_MAX_ROWS` rader (5000 med mindre du har hevet det,
[konfigurasjonsreferansen](/nb/admin/authentication/#konfigurasjonsreferanse)).

### De forseglede kolonnene

`nationalIdKind`, `nationalIdCountry`, `nationalId`, `bankAccountKind` og `bankAccount`
krever `staff:personal-data` i tillegg. En fil med noen av dem fra noen uten den, avvises
i sin helhet (`import_sealed_columns`), og importen sier fra med ord — fjern kolonnene
eller be om rettigheten. En
utfylt celle setter verdien med de samme kontrollene som på personens opplysninger; en
tom celle lar verdien som er lagret, stå. Et kontonummer importen endrer, gir personen en
e-post, slik en endring for hånd gjør ([E-posten om endret kontonummer](#e-posten-om-endret-kontonummer)),
når raden deres er lagret — aldri i en prøvekjøring. Ingen svar, logglinje eller
historikkoppføring har et siffer av noen av verdiene.

E-posten sendes rad for rad, etter hvert som hver rad lagres, og importen venter på hver
sending: en import av hundre nyansatte med kontonummer sender hundre e-poster — et
første kontonummer gir også e-post, slik det gjør for hånd — og tar tilsvarende lengre
tid.

En rad kan ikke endre en persons `birthDate` og `nationalId` samtidig: importen skriver
personens kolonner først, og kontrollerer den nye fødselsdatoen mot nummeret som er
lagret, som fortsatt koder den gamle — raden avvises med
`national_id_birth_date_mismatch`. Gjør endringen i to filer, slik du ville gjort for
hånd: først det nye `nationalId` med tom `birthDate`-celle, deretter den nye
`birthDate`.

### Eksporter registeret

**Eksporter personer** (`GET /api/v1/staff/people/export.csv`) laster ned
`staff-people-<i dag>.csv`: hver person —
slettede også — med jobb- og privatgruppen og hvor hver står i dag. Fødselsnummeret og
kontonummeret er aldri med: `nationalIdOnFile` sier `yes` eller `no`. Hver eksport
registreres i installasjonens historikk med ditt navn og antall rader. Filen inneholder
personopplysninger: oppbevar den der du oppbevarer registeret, og slett den når du er
ferdig.

En eksport importeres tilbake uendret: kolonnene etter privatgruppen ignoreres, så å
endre personens kolonner i en eksport og importere den er en måte å endre mange
personer på én gang. Raden til en slettet person kommer også tilbake og skriver
ingenting; endrer du den, avvises raden med `person_erased`.

## Eksporter en måned til lønn

Lønnsfilen gir lønnssystemet en måneds arbeidsforhold. Du trenger `staff:manage`
**og** `staff:personal-data`: filens permisjonsrader navngir HR-permisjon, som
svangerskapspermisjon, med a-meldingens beskrivelse. Lønn kjøres av noen som har begge
(en midlertidig regel; eieren kan i stedet velge å utelate HR-permisjon fra filen for HR
uten `staff:personal-data`).

1. I **Ansatteinnstillinger**, under **Lønnseksport**, velger du **Måned** — måneden før
   i dag er valgt fra start — og trykker **Eksporter til lønn**
   ([brukerveiledningen](/nb/user/staff/#eksporter-en-måned-til-lønn)). Siden ber om
   `GET /api/v1/staff/payroll/export?month=2026-09&format=csv`; kalt direkte kan `format`
   utelates — `csv` er det eneste formatet som finnes.
2. Lagre nedlastingen, `staff-payroll-2026-09.csv`, og importer den i lønnssystemet.

Hva filen inneholder: én rad per arbeidsforhold som var i måneden — startet senest den
siste dagen og ikke avsluttet før den første — også slettede personers, med vilkårene
som gjaldt den siste dagen i måneden (eller sluttdagen, for et arbeidsforhold som
sluttet i løpet av måneden). Hver rad har ansatt- og arbeidsforholdsnummeret, navnet,
virksomhetens organisasjonsnummer, startdatoen, yrkeskoden, timene i full stilling,
stillingsprosenten og når den sist ble endret, arbeidstidsordningen og
ansettelsesformen som Skatteetatens a-meldingskoder, avlønningstypen, når lønnen sist
ble endret, avdelingens kode, kostnadsstedet (vilkårenes eget, ellers avdelingens), og
lønnsansienniteten og lønnstrinnet når de er satt. Sluttdatoen og sluttårsaken fylles
bare ut i måneden arbeidsforholdet slutter. Et arbeidsforhold uten vilkår den dagen
beholder raden sin med tomme vilkårskolonner. Formen er Utlegg-modulens lønnsfil —
semikolon, byte order mark og desimalkomma — så et norsk Excel åpner den som den er.
Hele kolonnelisten og kodene står i [referansen](/en/reference/staff/#the-payroll-export).
Kodene for de tre ansettelsesformene som kom i 2025 (utleid, og tilkallingsvikar) er
ikke verifisert mot Skatteetatens kodeliste: sjekk dem med lønnssystemet før du stoler
på dem.

**Fraværskolonnene.** Til høyre for lønnstrinnet kommer tretten til, så en
lønnssystemkobling av de nitten første fortsatt virker. På hvert arbeidsforholds rad
står månedens summer i arbeidsdager, hver etter sin andel: **Holiday days** (ferie),
**Self-certified sick days** (egenmelding), **Certified sick days** (sykmelding, etter
graden), **Child sick days** (sykt barn) og av dem **Child sick days beyond employer** —
utover de ti arbeidsgiveren dekker i året, det NAV refunderer — **Leave days**
(permisjon), **Time off hours** (avspasering i timer) og **Other days** (annet). Bare
godkjent og registrert fravær teller. To regler å kjenne: en sykedag ved siden av en
prosentpermisjon teller grad × (100 − permisjonens prosent) / 100, og godkjent ferie
teller **som ferie** under all sykdom — en sykmelding av hvilken som helst grad eller en
egenmelding — siden ferien står til den ansatte krever den utsatt, og det ikke betales
sykepenger på en feriedag; når du registrerer kravet
([Utsett ferie ved sykdom](#utsett-ferie-ved-sykdom)), deles ferien, og sykedagene teller
som syk. Sykt barn-dager teller etter prosenten.

**En person med to arbeidsforhold samtidig**: dagsummene står på hvert arbeidsforholds
rad, sykt barn-dagene utover arbeidsgiverens ti bare på hovedarbeidsforholdet (høyest
stillingsprosent, ved likhet det laveste arbeidsforholdsnummeret). Et fravær sier ikke
hvilket arbeidsforhold det gjelder, så **en permisjonsrad gjentas på hvert
arbeidsforhold som gjelder — sjekk slike personers permisjonsrader for hånd** før
lønnssystemet rapporterer dem.

Etter et arbeidsforholds rad kommer **permisjonsradene**: én per permisjon a-meldingen
skal ha (Skatteetatens *Permisjon*), med **Leave ID**, **Leave start**, **Leave end**,
**Leave description** og **Leave percent**. En permisjon rapporteres når den varer mer
enn fjorten dager, eller når personens permisjoner de siste tolv månedene til sammen
passerer fjorten dager; permittering alltid; sykt barn og permisjon i timer (*ammefri*)
aldri. En permisjon som forlenges med en ny rad — dagen etter, eller etter en helg eller
helligdag — er fortsatt én permisjon under én **Leave ID**, den id-en a-meldingen kjenner
den under: en kode som `L-C098AE873961`, den samme i hver måneds fil og aldri en id fra
registeret. Før du kobler filen til et lønnssystem, sjekk de sju beskrivelsesverdiene
mot kodelisten det systemet bruker: de ble sjekket mot NAVs kilder 14.10.2026, men
Skatteetatens egen kodeliste var ikke tilgjengelig
([referansen](/en/reference/staff/#the-payroll-export)).

Fødselsnummeret og kontonummeret er **ikke** med i filen: lønnssystemet har dem
allerede, så eksporten leser dem aldri.

Hver eksport registreres i installasjonens historikk med ditt navn, måneden, formatet,
antall rader og filens SHA-256-sjekksum, så filen lønnssystemet fikk kan kjennes igjen
senere — regn ut sjekksummen til en fil du har (`shasum -a 256 staff-payroll-2026-09.csv`)
og sammenlign. En måned som ikke er `YYYY-MM` avvises med `payroll_month_invalid`, et
annet format enn `csv` med `payroll_format_unsupported`; ingen av dem registreres.
Filen inneholder alles lønnsvilkår: oppbevar den der du oppbevarer lønnssystemets egne
opplysninger, og slett din kopi når lønnssystemet har den.

## Oppbevaring

Ansatte beholder en tidligere ansatts opplysninger bare så lenge loven tillater det, og
sletter resten selv: arbeideren `staff-retention` ser etter hver time og sletter det som
er blitt forfalt. Hva som forfaller, og hva hver sletting fjerner, står i
[referansen](/en/reference/staff/#retention); kort fortalt:

- **Personaladministrasjon** — kontaktopplysninger og adresse, fødselsnummeret og
  kontonummeret, feltene for omsorgsdager, HRs notater og notatene på vilkårene,
  pårørende, og dokumentene i kategorier av klasse D (sertifikater, korrespondanse,
  annet) — slettes **`retentionGraceDays`** (standard 90) dager etter at personens siste
  arbeidsforhold sluttet. Personen merkes da som slettet, og opplysningene blir
  skrivebeskyttet.
- **Sykefraværsopplysninger** slettes etter sin egen tidsplan, **uansett personens
  arbeidsforhold** — også for en som fortsatt er ansatt: en sykefraværssaks notater,
  anslag og fakta om oppfølgingspunktene, radenes sykefraværstype (som blir *Fravær*) og
  grad, dokumentene som er lagt på saken, og det historikken sier om sykdommen, slettes
  **`healthRetentionDays`** (standard 365) dager etter at saken ble avsluttet — av deg,
  eller seksten dager etter siste dag. En sykt barn-rads type slettes like lenge etter
  slutten av året, og en tilbaketrekking av egenmelding like lenge etter at den løp ut —
  eller alt sammen med personens øvrige helseopplysninger, når siste arbeidsforhold
  sluttet for mer enn `healthRetentionDays` dager siden og det kommer først. Dagene står:
  hvem som var borte når, beholdes, og en slettet sak beholder datoene sine og
  milepælenes navn og datoer — med mindre du [sletter sakenes datoer og
  milepæler](#slett-svangerskapsrader-og-sykefraværssakers-datoer) også.
- **Svangerskap og helsetimer** — svangerskapskontroll, svangerskapspermisjon,
  fødselspermisjon og lege- eller tannlegetimer — beholdes med arbeidsforholdets
  opplysninger som standard. Med innstillingen på slettes de som dager med sykt barn:
  **`healthRetentionDays`** dager etter slutten av året raden sluttet, blir raden
  *Fravær*, med dagene og timene beholdt, og historikken sier ikke lenger hvilken type
  den var.
- **Andre helsedokumenter** — de i en begrenset kategori som ikke er lagt på en sak —
  slettes **`healthRetentionDays`** dager etter at det siste arbeidsforholdet sluttet.
- **Lønn** — et arbeidsforholds lønnsvilkår, ansiennitetsdato og lønnstrinn,
  arbeidsavtalene og attestene, og lønnen i historikken — slettes **fem år etter året
  arbeidsforholdet sluttet**, arbeidsforhold for arbeidsforhold: en person som har kommet
  tilbake, beholder lønnen i det nåværende arbeidsforholdet mens det gamles forsvinner.

**Hva en sletting lar stå**: navnene, fødselsdatoen, kjønnet, ansattnummeret,
arbeidsforholdene og vilkårene deres utenom lønn, rollene, koblingen til en bruker, og
historikkens hendelsestyper og datoer. Opplysningene svarer altså fortsatt på hvem som
jobbet her, når og i hvilken stilling; historikken viser en verdi som er slettet som
*endret*, og en hendelse **Slettet etter oppbevaringsreglene** sier hva som forsvant,
etter type og antall. En person uten arbeidsforhold, eller som fortsatt er ansatt,
slettes aldri.

### Sett tidene

1. Som en bruker med `staff:manage`, åpne **Ansatteinnstillinger**; kortet
   **Oppbevaring** viser tidene ([brukerveiledningen](/nb/user/staff/#oppbevaring)).
2. Endre **Frist (dager)** (`retentionGraceDays`, 0 til 3650) og **Helsedokumenter
   (dager)** (`healthRetentionDays`, 30 til 3650), og trykk **Lagre fristene**. Gjennom
   API-et er det `PUT /api/v1/staff/settings` med revisjonen du leste i `If-Match`; en
   forespørsel som utelater en av tidene, beholder den.
3. Kontroller: kortet viser de nye tidene. Neste kjøring bedømmer etter dem; en tid som
   gjøres kortere kan gjøre mange personer forfalt på én gang, og arbeideren tar femti
   per kjøring.

### Slett svangerskapsrader og sykefraværssakers datoer

To innstillinger, av som standard, sletter mer enn loven krever at dere beholder (eierens
avgjørelse av Bs åpne spørsmål 11 og 12). Avgjør dem med personvernombudet: hver sletter
for godt.

1. Som en bruker med `staff:manage`, åpne **Ansatteinnstillinger**; kortet
   **Oppbevaring** har to brytere, hver forklart under seg.
2. **Slett svangerskap og helsetimer** (`erasePregnancyHealthTypes`): rader med
   svangerskapskontroll, svangerskapspermisjon, fødselspermisjon og *Lege eller
   tannlege* får en reservert *Fravær*-type — en timesrad beholder timene —
   `healthRetentionDays` dager etter slutten av året de sluttet, aldri før lønnsmåneden
   som rapporterte dem er passert — og **svangerskapspermisjon og fødselspermisjon
   aldri før et helt år etter slutten av året sitt**, uansett fristen, så feriepengene
   for året etter dem, og en ny lønnseksport av det året, fortsatt leser permisjonen.
   Bare typen slettes: fraværets notat og avgjørelsens notat står, og slettes med
   personaladministrasjonen. Typene som bare er for HR, får en reservert type som
   også bare er for HR, så ledere, godkjennere og HR uten `staff:personal-data` fortsatt
   ser raden som *permisjon* og ingen hendelse om slettingen; personen og
   `staff:personal-data` ser *Fravær (bare HR)*. Historikken beholder datoene og viser
   typen som *endret*; en hendelse om en rad som er slettet eller endret til en annen
   type, slettes etter året den ble registrert. En slettet rad som bare er for HR,
   beholder revisjonen sin, så merket HR uten `staff:personal-data` har for den, endres
   heller ikke. En lønnsmåned som eksporteres på nytt
   etter at svangerskapsradene er slettet, rapporterer de dagene som annet fravær, ikke
   som permisjon.
3. **Slett sykefraværssakens datoer og milepæler** (`eraseSickCaseDates`): når
   helseinnholdet i en avsluttet sak slettes, går også selve saken — datoene,
   milepælene og historikken, som bare beholder hendelsestypene og datoene. Fraværet
   står igjen som *Fravær*-dager uten sak; dokumentene slettes som før.
4. Trykk **Lagre fristene**. Gjennom API-et er det `PUT /api/v1/staff/settings` med ett
   av feltene og revisjonen i `If-Match`; en forespørsel som utelater et felt, beholder
   det, og `null` avvises.
5. Kontroller: neste kjøring — av seg selv innen en time, eller **Kjør sletting nå**
   — sletter **alt som allerede er forfalt**, også saker som er slettet før, og svaret
   nevner *svangerskap og helsetimer* eller *sykefraværssaker* med et antall.

**Å slå en innstilling av gjenoppretter ingenting**: det som er slettet, forblir
slettet; den hindrer bare at neste kjøringer sletter mer. En person med slettesperre
hoppes over av begge, og `STAFF_RETENTION_ENABLED=0` stopper begge sammen med resten.
**Hvem som slo en innstilling på eller av, og når, registreres** — installasjonshendelsen
`settings_updated`, med innstillingen før og etter og navnet ditt.

**Lønnsdokumentasjonen er lønnssystemets.** Ettårsgulvet for svangerskapspermisjon
beholder radene som neste års feriepenger og en ny eksport leser; det er ikke
lønnsdokumentasjonen. Etter bokføringsloven er det lønnssystemets regnskap og
lønnsfilene som er eksportert dit, oppbevart der i sine egne perioder — ikke Vantigos
rader, som slettingen kan skrive om.

### Slå av arbeideren, eller endre hvor ofte den ser etter

Arbeideren kjører med mindre `STAFF_RETENTION_ENABLED=0`, hver `STAFF_WORKERS_POLL`
(standard `1h`, minst `1m`) — i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1` ([Installasjon](/nb/admin/installation/)). En sletting finner
ingenting forfalt andre gang, så å se etter hver time sletter hver dags forfalte personer
én gang. Flere replikaer bytter på gjennom en lås; én kjører slettingen om gangen.

Slått av slettes ingenting etter tidsplanen — **heller ikke sykefraværssakene**: de
slettes i samme kjøring, uten en egen arbeider — og filene som venter på sletting,
venter; kjøringen for hånd nedenfor virker fortsatt.

### Kjør den for hånd

**Kjør sletting nå** på kortet **Oppbevaring** i **Ansatteinnstillinger**
(`staff:manage`; `POST /api/v1/staff/retention/run`) kjører arbeiderens sletting og en
runde i sletteloggen nå, med deg som forfatter av slettehendelsene, etter at du har
bekreftet. Kortet sier så hva som ble slettet per type — helsetypene
(sykefraværssaker, dager med sykt barn, tilbaketrekkinger, svangerskap og helsetimer,
helsedokumenter) bare til en bruker som også har `staff:personal-data` — og hvor mange
filer som ble slettet fra objektlageret. **Sist slettet** utelater på samme måte
helseslettingene for en bruker uten den. Runden i sletteloggen stopper etter tretti sekunder, så et
tregt objektlager ikke holder igjen forespørselen; arbeideren sletter resten neste gang
den ser etter. Mens arbeideren kjører, sier kortet fra (`retention_busy`, 409); prøv
igjen om et minutt.

### Slettesperren

En person med [slettesperre](#sett-en-slettesperre-på-en-person) hoppes over av hver
sletting, av hver type, så lenge sperren står. Opphev sperren når saken er avsluttet, så
sletter neste kjøring det som har forfalt i mellomtiden.

### Sletteloggen

En sletting fjerner et dokuments oppføring med en gang og setter filen i kø for sletting
fra objektlageret; det gjør også en sletting for hånd, når lageret ikke bekrefter med en
gang. Arbeideren spør lageret på nytt hver gang den ser etter, venter lenger etter hver
avvisning (høyst en time), og gir aldri opp — en lovpålagt sletting er ikke et forsøk.

Kortet **Oppbevaring** i **Ansatteinnstillinger** viser sletteloggen — **Ventende
slettinger**, **Feilede slettinger**, **Eldste ventende siden** og **Sist slettet** —
slik `GET /api/v1/staff/settings` rapporterer den i blokken `retention`:
`pendingDeletions` (filer som venter), `failingDeletions` (filer lageret har avvist minst
én gang), `oldestPendingSince` og `lastErasedAt` (den siste slettingen). En
`failingDeletions` som blir stående over null, betyr at objektlageret avviser: sjekk at
lagring er konfigurert og kan nås ([Objektlagring](/nb/admin/object-storage/)), og
serverloggen, som navngir lagerets feil. Når lageret svarer, forsvinner filene i køen
neste gang arbeideren ser etter.

### Etter en sletting

En slettet person som kommer tilbake, [ansettes på nytt](#ansett-en-slettet-person-på-nytt)
på de samme opplysningene: det slettingen fjernet, blir stående tomt til du skriver det
inn igjen, og lønnen i det nye arbeidsforholdet røres ikke av slettingen av det gamle.

**Hva som skjer i identity og katalogen.** Når en person er slettet (og ikke har
slettesperre), ber oppbevaringsarbeideren også identity om å slette den deaktiverte
brukeren Ansatte styrte for dem (`identity_erased` i historikken, koblingen fjernet):
brukeren slettes med invitasjonene, innloggingsmetodene, øktene, SCIM-koblingen og en
eventuell ubrukt kunngjøring for ansattnummeret, og øyeblikksbildene i identitys
revisjonsrader som navngir dem, pseudonymiseres på stedet (`Erased user`). En bruker HR
koblet for hånd under Selvbetjening, er ikke Ansattes og røres ikke. Hvis identity
avviser — brukeren er fortsatt aktivert, er Owner eller SystemAdmin, eller styres ikke
lenger av Ansatte — eller ikke svarer, registrerer koblingen `erasure_incomplete`,
dashbordet viser *erasureIncomplete* («An erased person»), og oppbevaringsarbeideren
prøver igjen ved hver kjøring til brukeren er slettet eller allerede borte. For en
`entra`-person der katalogkontoen ennå ikke er slettet, lar Ansatte en tom kobling stå
slik at kontoarbeideren fortsatt sletter Entra-kontoen når **Slett en ekstern konto
(dager etter slutt)** er passert etter at siste arbeidsforhold sluttet.

## Tersklene

Fire terskler gir arbeidsgiveren en plikt, og Ansatte følger med på hver av dem. Så lenge
en er nådd og ikke bekreftet, ser de med `staff:manage` et punkt for den i **Trenger
oppfølging** på dashbordet ([dashbordet](/nb/user/staff/#dashbordet)):

| Terskel | Nådd når | Hva arbeidsgiveren må gjøre |
| --- | --- | --- |
| `5` | 5 eller flere er ansatt i dag | Ha skriftlige varslingsrutiner (aml. § 2 A-6) og verneombud (§ 6-1). |
| `30` | 30 eller flere | Ha arbeidsmiljøutvalg, AMU (§ 7-1). |
| `50` | Flere enn 50 — 51 eller flere | Aktivitets- og redegjørelsesplikten, ARP (likestillings- og diskrimineringsloven § 26), som gjelder arbeidsgivere med flere enn femti ansatte. Med nøyaktig 50 kommer det ikke noe punkt ennå. |
| `otp` | Noen har 75 % eller mer, eller de med 20 % eller mer har til sammen to fulle stillinger — stillingsprosentene i dag, summert per person | Ha obligatorisk tjenestepensjon, OTP (OTP-loven § 2) — med mindre den eneste det gjelder er eieren, noe Ansatte ikke kan vite, siden den ikke registrerer noen eier. |

Når plikten er ivaretatt — eller ikke gjelder — bekrefter du terskelen. Den bekreftes
**én gang, for godt**: punktet kommer aldri tilbake, uansett hva antallet ansatte gjør
senere, og en terskel kan bekreftes før den er nådd.

1. Som en med `staff:manage`, åpne **Ansatteinnstillinger**, trykk **Bekreft** på
   terskelens rad under **Terskler**, og bekreft
   ([brukerveiledningen](/nb/user/staff/#terskler)). Gjennom API-et er det
   `POST /api/v1/staff/thresholds/{key}/acknowledge` med nøkkelen — `5`, `30`, `50` eller
   `otp`.
2. Kontroller: raden sier **Bekreftet** med datoen, slik `thresholdsAcknowledged` i
   `GET /api/v1/staff/settings` gjør; punktet er borte fra dashbordet.

Hver bekreftelse registreres i innstillingene med tidspunktet, og som en
`threshold_acknowledged`-hendelse for installasjonen som navngir hvem som bekreftet.
Å bekrefte en terskel på nytt endrer ingenting og registrerer ingenting. En annen nøkkel
enn de fire avvises med `threshold_unknown`.

## Hvem som avgjør fravær

En fraværssøknad — ferie, en permisjon, avspasering — avgjøres av personens leder i dag
(lederen arbeidsvilkårene nevner, ellers avdelingens, og den som har sluttet hoppes
over), av alle med `staff:approve` og av HR (`staff:manage`). Gi noen en rolle med
`staff:approve` for å la dem avgjøre alles fravær uten HRs øvrige rettigheter. Ingen
avgjør sin egen søknad: en leders går til lederens egen leder, og HRs til en annen med
`staff:approve` eller `staff:manage`. En leder avgjør først når personen deres er koblet
til en aktiv bruker ([Koble personer til brukerne sine](#koble-personer-til-brukerne-sine)).

**HR-permisjon avgjøres av `staff:personal-data`**: en søknad om en HR-type —
svangerskapspermisjon, en religiøs høytid, en egen type merket som HR-type — går ikke til
noen leder, ingen med `staff:approve` og ingen HR uten `staff:personal-data`, og ingen av
dem får e-post om den. Alle med `staff:personal-data` unntatt personen selv avgjør den i
godkjenningsinnboksen sin, og bare HR med `staff:personal-data` registrerer, endrer,
trekker tilbake eller retter en slik rad — HR uten den får samme svar som for en type som
ikke finnes. Gi den rettigheten til den som skal avgjøre slik permisjon. For alle andre
vises permisjonen først når den er godkjent, som *permisjon*.

**Den eneste med `staff:personal-data` avgjør sin egen søknad om HR-permisjon**
(eierens regel). Når nøyaktig én aktiv bruker har `staff:personal-data` — og det er den
som søker — godkjenner de sin egen søknad i godkjenningsinnboksen sin. Hvem som har
rettigheten, er det identity som sier: en rolle brukeren har selv, en rolle koblet til
en tilgangsgruppe de er med i, eller **Owner**-rollen, som har alle rettigheter — så med
en Owner og én til med rettigheten er ingen av dem den eneste, og søknaden går til den
andre. En delegering gir ingen rettighet. En deaktivert bruker, en bruker som ennå ikke
har tatt imot invitasjonen sin, og med SCIM en bruker katalogen har deaktivert, teller
ikke. Får Ansatte ikke lest fra identity hvem som har rettigheten, er ingen den eneste,
og søknaden venter. Hver slik godkjenning er oppfølgingspunktet `hrOnlySelfApproved` for
alle med `staff:personal-data` (personens navn, aldri typen) til fraværet er over: gir du
en til rettigheten, ser de det — det andre blikket regelen mangler. For å unngå regelen
gir du `staff:personal-data` til to personer.

**Det ene unntaket, foreløpig** (inntil eieren har bestemt seg): den som har
`staff:approve` eller `staff:manage`, står øverst i strukturen — leder en avdeling uten
noen leder over seg — og ikke har noen leder med aktiv bruker, kan godkjenne sitt eget.
Hver slik godkjenning er et oppfølgingspunkt for `staff:manage` (`selfApproved`, med
personens navn) til fraværet er over, så noen ser det. For å unngå
det gir du personen en leder i arbeidsvilkårene, koblet til en aktiv bruker.

HR retter et godkjent eller registrert fravær — dagene, typen, prosenten eller
graderingen — med **Rett** (et godkjent) eller **Endre** (et registrert) på personens
**Fravær**-fane; rettingen lagres med hva det var før, og personen
får e-post om en retting. Et avgjort fravær som har begynt, trekkes bare tilbake av HR.
Verken en endring eller en retting gjør en HR-type om til en annen type, eller omvendt
(`absence_visibility_change`): de avgjøres og ses av forskjellige personer. For å rette et
fravær lagt inn som feil slags fravær, trekker du det tilbake (**Trekk tilbake fraværet**) —
eller, et registrert fravær, som ikke kan trekkes tilbake, fjerner du det
(`DELETE /absences/{id}`) — og legger inn det andre på nytt.

## E-postene om fravær

Hvem som får e-post om fravær, og når:

- en **søknad** eller en **registrering** går til personens ledere som avgjør den
  (lederen i dag, koblet til en aktiv bruker), til jobb-e-posten Ansatte har for dem,
  ellers den private, på den personens språk;
- en **søknad** fra en person **uten en slik leder** går i stedet til alle med
  `staff:approve` eller `staff:manage` — **Owner-brukere også**, som har alle
  rettigheter — én gang hver, aldri til personen selv, til adressen brukeren har i
  identity, på språket til personen de er koblet til, ellers sitt eget, ellers norsk. En
  registrering for en slik person sendes ikke til noen. Gi personen en leder koblet til
  en aktiv bruker for å holde disse e-postene hos lederen;
- en **søknad om en HR-type** går ikke til noen leder og ingen med `staff:approve` eller
  `staff:manage`, men til alle andre med `staff:personal-data` (Owner-brukere også), som
  en nøytral e-post — «En fraværssøknad venter på avgjørelsen din» — som ikke nevner
  person, type eller dager. Den e-posten sendes bare for en søknad om en HR-type, så at
  den kommer, sier i seg selv at en finnes: gi `staff:personal-data` bare til brukere
  med en egen adresse i identity, aldri en felles postkasse eller en liste;
- en **godkjenning**, et **avslag**, en **tilbaketrekking** av et godkjent fravær gjort av den som avgjør det, og HRs **retting** går
  til personen. En egengodkjenning sender ingen e-post.

Alle som avgjør, ser også det som venter på dem i godkjenningsinnboksen. E-posten nevner
typen bare når den verken er sykdom eller en HR-type: en HR-type er «permisjon»,
sykefravær og sykt barn «fravær»; den har aldri gradering, timer, en merknad eller
begrunnelsen for et avslag. Som e-posten om endret kontonummer nedenfor trenger den
`MAIL_DRIVER=smtp`; med en annen driver logger serveren
`staff: absence mail skipped: the mail driver is not smtp`, og en leder uten e-post
hoppes over med `staff: absence mail skipped: the recipient has no email`. En bruker med
rettigheten der identity bare har en plassholderadresse fra en identitetsleverandør eller
en katalog, hoppes over med `staff: absence mail skipped: the holder has no address a mail
reaches` og brukerens id; får Ansatte ikke lest fra identity hvem som har rettighetene,
logger den `staff: absence mail skipped: the holders could not be read` og sender ingen av
dem. E-postene for én hendelse sendes etter hverandre innen 30 sekunder til sammen; det
som gjenstår når tiden er ute, logges med antallet. En sending som feiler, logges som en
advarsel med personens eller brukerens id, aldri en adresse; fraværet blir stående.

## E-posten om endret kontonummer

Når et kontonummer settes eller endres, får personen en e-post — til den private
e-postadressen når de har en, ellers til jobbadressen, på det foretrukne språket — som
nevner kontoens fire siste sifre og ikke mer, så en endring noen andre har gjort ikke
går upåaktet hen. E-posten går gjennom installasjonens egen SMTP-server etter at
endringen er lagret. Å sende kontonummeret som allerede er lagret, på nytt endrer
ingenting og sender ingen e-post.

- Den krever `MAIL_DRIVER=smtp` og `SMTP_*`-innstillingene
  ([Installasjon](/nb/admin/installation/#e-post-og-observerbarhet)). Med en hvilken
  som helst annen driver hoppes e-posten over, og serveren logger
  `staff: bank-account change mail skipped: the mail driver is not smtp`.
- En person uten noen e-postadresse får ingen e-post, og serveren logger
  `staff: bank-account change mail skipped: the person has no email`.
- En sending som feiler, logges som en advarsel med personens id; selve endringen blir
  stående lagret.

## Kompetansearbeideren

Arbeideren `staff-competence` sender e-post før et sertifikat utløper. Ved hver
`STAFF_WORKERS_POLL` (standard `1h`) — i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1` ([Installasjon](/nb/admin/installation/)) — med mindre
`STAFF_COMPETENCE_ENABLED=0`, ser den på hver gjeldende kompetanserad med en sluttdato
for en person som er i arbeid i dag:

- når raden kommer innenfor påminnelsen før siste gyldige dag — **Påminnelse (dager)**
  HR setter på kompetansen, som standard 30 dager for en gyldighet på inntil ett år, 60
  inntil to år og 180 utover det — får personen og hver av lederne deres én e-post
  («utløper snart»);
- når siste gyldige dag er passert, får de én til («er utløpt»).

Hver rad får hver e-post én gang, og raden stemples før e-postene går, så to
replikaer aldri sender den to ganger. En fornyelse begynner på nytt med den nye raden,
og en rettet sluttdato sendes på nytt når den forfaller. Fordi arbeideren ser etter ved
hver kjøring og ikke på et fast klokkeslett, går e-posten ved første kjøring etter at
raden forfaller — ofte like etter midnatt i virksomhetens tidssone. Dashbordet viser
sertifikater som snart utløper og som er utløpt, uansett hva arbeideren har gjort
([referansen](/en/reference/staff/#the-competence-worker)).

1. Forutsetning: `MAIL_DRIVER=smtp` og `SMTP_*`-innstillingene
   ([Installasjon](/nb/admin/installation/#e-post-og-observerbarhet)). E-postene går til
   jobbadressen Ansatte har for hver person, ellers den private, på deres språk; en
   leder får dem bare når brukerkoblingen deres viser til en aktiv bruker.
2. Kontroller: hver runde logger `staff: the competence cycle ran` med hvor mange rader
   den stemplet og hvor mange e-poster den sendte og ikke fikk sendt.
3. For å stoppe den: `STAFF_COMPETENCE_ENABLED=0` og start på nytt. Da får ingen
   e-post; punktene på dashbordet blir stående.

Hva som kan gå galt:

- **`staff: competence mails skipped: the mail driver is not smtp`.** Radene stemples
  likevel: de e-postene sendes ikke senere når SMTP er satt opp.
- **`staff: competence mail skipped: the recipient has no email`.** Personen eller
  lederen har verken jobbadresse eller privat e-post i Ansatte; linjen nevner
  person-id-en.
- **`staff: a competence mail could not be sent`.** Logges som en advarsel med
  mottakerens person-id og adressen skjult; den prøves ikke igjen — punktet på
  dashbordet er signalet.
- **`staff: competence mails not sent: the time for the cycle ran out`.** En rundes
  e-poster har 30 sekunder til sammen; det som er igjen, logges med antall og sendes
  ikke.

## Innstillingene

Innstillingene er `staff:manage` sine, og leses og erstattes gjennom `GET` og
`PUT /api/v1/staff/settings`, og endres på Innstillinger-siden i Ansatte-appen
([Endre innstillingene](/nb/user/staff/#endre-innstillingene)). En erstatning bærer revisjonen den leste i `If-Match`, så to personer som
endrer innstillingene samtidig ikke kan overskrive hverandre uten å merke det.

- **Forretningstidssone** (`timeZone`, svares, men tas aldri imot av `PUT`). Hver dato
  i Ansatte — en start, en slutt, «i dag» — er en kalenderdag i denne sonen. Den er
  arbeidsområdets, satt av en eier under
  [Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet),
  og vises her så siden sier hvilken kalender datoene dens er i.
- **Prefiks og neste ansattnummer** (`employeeNumberPrefix`, tomt, og
  `nextEmployeeNumber`, 1). En person som opprettes uten ansattnummer får prefikset og
  det neste nummeret, uten utfylling: `EMP-` og 7 blir `EMP-7`. Prefikset er opptil
  åtte bokstaver, sifre eller bindestreker, og de to sammen kan bli høyst 16 tegn.
  To ansattnumre som bare skiller seg i store og små bokstaver er samme nummer
  (`emp-7` avvises ved siden av `EMP-7`), og det avgjøres likt uansett hvilken locale
  databasen ble opprettet med — også en tyrkisk — og likt med hvordan identity
  vurderer ansattnummeret den holder på en bruker.
- **Pensjonsleverandør** og **yrkesskadeforsikrer** (`pensionProvider`,
  `injuryInsurer`). Leverandøren av obligatorisk tjenestepensjon (OTP) og
  yrkesskadeforsikringen, som kontrollen av arbeidsavtalen leser.
- **Oppbevaringstider** (`retentionGraceDays`, 90, fra 0 til 3650, og
  `healthRetentionDays`, 365, fra 30 til 3650): hvor mange dager etter at personens siste
  arbeidsforhold sluttet, personaladministrasjonen og helsedokumentene slettes
  ([Oppbevaring](#oppbevaring)). Utelatt fra en erstatning beholder hver sin verdi.
- **Regler for egenmelding** (`selfCertPolicy`, `minimum`, med
  `selfCertDaysPerOccurrence`, `selfCertOccurrencesPerYear`, `selfCertDaysPerYear` og
  `selfCertAfterMonths`) og **automatisk overføring** (`autoCarryOver`, av, med den
  skrivebeskyttede `autoCarryOverSince`): [Reglene for egenmelding](#reglene-for-egenmelding).
  Utelatt fra en erstatning beholder hver sin verdi.
- **Kontoer** — kortet **Kontoer**, med **Forbered kontoen (dager før)** og **Slett en
  ekstern konto (dager etter slutt)**, tom for aldri (`createBeforeDays`, 14, fra 0 til
  90, og `deleteExternalAfterDays`, ingen, fra 1 til 3650 eller `null` for aldri): hvor
  mange dager før kontoens
  virkningsdato den klargjøres, deaktivert ([Slå på konto for en
  person](#slå-på-konto-for-en-person)), og når en persons konto i Entra slettes etter
  at det siste arbeidsforholdet sluttet, når den er deaktivert og ingen slettesperre er
  på personen — og like mange dager etter at en gruppe ble arkivert, eller en
  leverandør slått av for den, slettes gruppen i katalogen (Entra tømmer og beholder en
  sikkerhetsgruppe i stedet, og lar en den ikke laget, stå); aldri, får den
  `… (archived)` etter navnet og beholdes. Utelatt fra en erstatning beholder hver sin
  verdi; `null` setter `deleteExternalAfterDays` til aldri.
- **Standard kontotype** (`defaultAccountKind`, på kortet **Kontoer**): typen en ny
  person får — `local`, `external` eller `entra` — satt én gang fra autoritetsmodusen ved
  oppgraderingen ([kontotypen](#kontotypen)). `entra` avvises `provider_missing` på
  feltet med mindre en aktivert modul tilbyr leverandøren. Utelatt fra en erstatning
  beholder den verdien sin; personer som allerede er registrert, beholder sin.
- **Katalogprofil** (`directoryFields`, kortet **Katalogprofil**): hvilke felt Ansatte
  skriver på en `entra`-persons konto i Entra, som `{"<felt>": true|false}` over de
  som kan slås av og på — `employeeHireDate`, `employeeLeaveDateTime`, `employeeType`,
  `jobTitle`, `department`, `companyName`, `employeeOrgData`, `officeLocation`,
  `preferredLanguage` og `manager` på som standard, `workAddress` og `phones` av. Et
  felt som utelates, beholder verdien sin; et ukjent avvises på
  `directoryFields.<felt>`. Et felt som slås av, sendes ikke lenger: Ansatte slutter å
  skrive det, og Entra beholder det det sist hadde. Navnene, ansattnummeret, brukerhovednavnet og arbeidslandet skrives
  alltid ([tilordningen](/en/reference/staff/#the-directory-profile)).
- **Oppbevaringsstatus** (`retention`, skrivebeskyttet): sletteloggens ventende og
  avviste filer, den eldste ventende, og den siste slettingen
  ([Sletteloggen](#sletteloggen)). Filer lageret har avvist minst én gang, og som ble
  lagt i køen for mer enn 24 timer siden, gir også et punkt på dashbordet for
  `staff:manage`.
- **Bekreftede terskler** (`thresholdsAcknowledged`, skrivebeskyttet): når hver terskel
  ble bekreftet ([Tersklene](#tersklene)).

## Hva som kan gå galt

- **`timeZone` avvist.** Navnet er ukjent, eller serveren og databasen er uenige om det
  om vinteren eller om sommeren. Velg bysonen forkortelsen står for.
- **`employee_number_too_long`.** Prefikset og det neste nummeret blir til sammen mer
  enn 16 tegn. Kort ned prefikset, eller senk det neste nummeret.
- **`revision_conflict`.** Noen har endret innstillingene siden du leste dem. Les dem på
  nytt og gjenta endringen.
- **`user_inactive`.** Brukeren du kobler, er deaktivert. Aktiver den først, eller koble
  en annen.
- **`user_linked_elsewhere`.** Brukeren er koblet til en annen person. Fjern koblingen
  der først.
- **`no_person`.** Personen som ber om Min side, er ikke koblet til noen. Koble dem.
- **`national_id_duplicate`.** En annen person har allerede dette fødselsnummeret.
  Avvisningen sier ikke hvem; noen med `staff:personal-data` kan sammenligne ved å vise
  numrene.
- **Ingen e-post om endret kontonummer kom fram.** Se etter linjen over i loggen, og at
  personen har en e-postadresse.
- **`storage_unavailable`.** Objektlageret kunne ikke skrives til eller leses fra:
  lagring er ikke konfigurert, eller roten er ikke skrivbar. Ingenting ble registrert;
  rett opp lagringen og prøv igjen ([Objektlagring](/nb/admin/object-storage/)).
- **`seed_immutable`.** En av de seks standardkategoriene kan ikke endres. Legg til en
  egen kategori i stedet.
- **`category_in_use`.** Det er fortsatt dokumenter arkivert under kategorien du
  arkiverer. Flytt dem til en annen kategori først.
- **`threshold_unknown`.** Nøkkelen er ingen av `5`, `30`, `50` og `otp`.
- **`document_count_exceeded`.** Personen har 200 dokumenter, det meste én person kan
  ha — talt uansett hva du får se, så det kan omfatte helsedokumenter du ikke ser. Fjern
  dokumenter som ikke lenger trengs.
- **`person_erased`.** Oppbevaringskjøringen har slettet personen, og opplysningene er
  skrivebeskyttet. Ansett dem på nytt med et nytt arbeidsforhold, eller la dem være.
- **`legal_hold`.** Personen du sletter, har en slettesperre. Opphev den først, hvis
  saken er avsluttet.
- **`reassessment_not_due`.** Personen er ikke slettet, er allerede revurdert, eller det
  siste arbeidsforholdet sluttet for mindre enn tolv måneder siden.
- **`import_not_semicolon`.** Filen er kommaseparert. Lagre den på nytt som CSV med
  semikolon — i et norsk Excel gjør *CSV UTF-8* det.
- **`import_too_large`** eller **`import_too_many_rows`.** Filen er over 5 MiB eller har
  flere rader enn `STAFF_IMPORT_MAX_ROWS`. Importer den i deler.
- **`import_sealed_columns`.** Filen har en kolonne for fødselsnummer eller kontonummer,
  og du har ikke `staff:personal-data`. Fjern kolonnene, eller be om rettigheten.
- **En ukjent kolonne.** Importen navngir den: en feilstavet overskrift avvises i stedet
  for å ignoreres. Rett den til et av navnene i malen.
- **`export_too_many`.** Registeret har flere personer enn `STAFF_IMPORT_MAX_ROWS`. Hev
  det, innenfor grensene, og start på nytt.
- **`retention_busy`.** Oppbevaringsarbeideren kjører, på denne serveren eller en annen.
  Vent et minutt og kjør den igjen.
- **`failingDeletions` over null.** Objektlageret nekter å slette filene i køen. Rett opp
  lagringen; arbeideren prøver igjen uten ende.
- **En oppbevaringstid avvist.** `retentionGraceDays` er 0 til 3650 dager,
  `healthRetentionDays` 30 til 3650.
- **Ingen blir slettet.** Arbeideren er slått av (`STAFF_RETENTION_ENABLED=0`), personen
  har slettesperre, har fortsatt et arbeidsforhold som ikke har sluttet, eller har ingen;
  eller tiden har ikke gått ennå.
