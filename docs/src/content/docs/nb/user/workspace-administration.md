---
title: Administrasjon av arbeidsområdet
description: Brukere, invitasjoner, roller og tilgang, delegert administrasjon, førstegangsoppsett og systemadministrasjon.
sidebar:
  order: 90
sources:
  - apps/host/frontend
  - apps/server/internal/identity
---

Administrasjon av arbeidsområdet er der en **Eier** styrer hvem som kan bruke
Vantigo og hva de kan gjøre. Åpne avatar-menyen øverst til høyre og velg
**Administrer arbeidsområde**; sidemenyen har **Oversikt**, **Brukere**,
**Invitasjoner** og **Roller og tilgang**.

Alt her krever eierrollen, med ett unntak: den en eier har delegert
rolleadministrasjon til, ser bare **Roller og tilgang**, både i avatar-menyen og i
sidemenyen. Den siste aktive eieren kan aldri degraderes, deaktiveres eller slettes,
så arbeidsområdet har alltid én.

## Les oversikten

**Oversikt** åpner **Administrasjonspanel**, "en skrivebeskyttet oversikt over
kontoer og identitetsintegrasjoner":

- **Personer** — **Totalt**, **Aktive**, **Deaktivert** kontoer og **Ventende
  invitasjoner**, med lenken **Vis brukere**.
- **Invitasjoner** — antall ventende og **Administrer invitasjoner**.
- **Tilgangskontroll** — **Administrer roller og tilgang**.
- **Identitetsintegrasjoner** — om **SSO** er på og hvilken leverandør (Microsoft
  Entra ID, Google Workspace, eller bare **Aktivert**), at **Klientautentisering**
  er **Administrert av distribusjonen**, og **Siste vellykkede SSO-bruk**.
- **SCIM-klargjøring** — **Status** og **Siste autentiserte forespørsel**.
- **Operativ aktivitet** — en påminnelse om at denne oversikten bare viser
  ikke-sensitiv identitetsaktivitet; legitimasjon og leverandørkonfigurasjon vises
  aldri.

SSO og SCIM settes opp av den som drifter installasjonen, ikke fra dette
skjermbildet; se [SSO- og SCIM-drift](/nb/admin/sso-scim/).

## Finn en bruker

**Brukere** lister hver konto med **Profilbilde**, navn og e-post, **Rolle** (Bruker
eller Eier), **Tilgang** og **2FA** (På eller Av). Tilgang viser **Aktiv**,
**Deaktivert av administrator** eller **Midlertidig utestengt** — det siste etter
fem feil passord på rad, og det går over av seg selv etter 15 minutter.

Kortene over listen teller **Aktive**, **Deaktivert**, **SSO** (kontoer som logger
inn gjennom identitetsleverandøren) og **Administratorer** (eiere); velg ett for å
filtrere listen etter det, og velg det igjen for å fjerne filteret. **Ventende
invitasjoner** åpner invitasjonssiden. **Søk etter brukere** treffer på navn eller
e-post.

Din egen rad har ingen handlingsmeny — "Dette er kontoen din. Administrer den i
Kontoinnstillinger."

## Legg til en bruker

Velg **Legg til bruker**. Dialogen **Legg til en bruker** ber deg "velge hvordan
denne personen skal komme i gang":

- **Send invitasjon** — personen får en e-post og velger sitt eget passord (se
  [Kom i gang](/nb/user/getting-started/#godta-en-invitasjon-og-opprett-kontoen-din)).
- **Angi startpassord** — du velger **Startpassord** ("12+ tegn med store og små
  bokstaver samt et siffer") og gir det til personen selv. Kontoen finnes med en
  gang, med e-postadressen allerede bekreftet.

I begge tilfeller fyller du inn **Visningsnavn**, **E-post** og **Rolle** —
**Bruker** eller **Eier** — og velger **Lagre**. **Invitasjon sendt** eller **Bruker
opprettet** bekrefter det. "Det finnes allerede en konto for denne e-postadressen."
betyr at adressen er opptatt; inviterer du den på nytt, erstattes en invitasjon som
fortsatt ventet på den.

**Bruker** er "standard brukerrolle uten tillatelser som standard": en ny bruker ser
bare kontrollpanelet før en rolle gir en app, som beskrevet under
[Roller og tilgang](#administrer-roller-og-tilgang). **Eier** gir full tilgang til
alt.

## Rediger en bruker, tilbakestill et passord

Handlingsmenyen på en rad (de tre prikkene) tilbyr:

- **Rediger detaljer** — endre **Visningsnavn**, **E-post** eller **Rolle** i
  dialogen **Rediger bruker**. Enhver endring av disse avslutter alle øktene
  personen har, så de må logge inn på nytt; en ny e-postadresse avlyser også
  ventende invitasjoner til den gamle og den nye adressen.
- **Send e-post for tilbakestilling** — sender personen en lenke for
  tilbakestilling av passord: "Brukeren mottar snart instruksjoner."
- **Angi startpassord** — velg et nytt passord for personen på deres vegne.
  "Administratorens valgte passord er aktivt nå." Bruk dette for noen som er
  utestengt uten tilgang til e-post, og be dem bytte det etterpå.

Å tilbakestille en annen administrators tofaktorautentisering tilbys ikke i dette
skjermbildet; det finnes i API-et for en eier hvis egen økt er MFA-bekreftet.

## Deaktiver, aktiver eller slett en bruker

For å hindre noen i å logge inn uten å miste noe velger du **Deaktiver tilgang** og
bekrefter **Deaktivere tilgang?** — "_navn_ vil ikke lenger kunne logge inn."
Øktene deres avsluttes med en gang, og kontoen beholder data, roller og
innloggingsmetoder. Raden viser så **Deaktivert av administrator**, og den samme
menyen tilbyr **Aktiver tilgang** — "_navn_ vil kunne logge inn igjen." — som
slipper dem inn igjen med passordet de hadde.

**Slett bruker** spør **Slett denne brukeren?** — "Dette fjerner kontoen permanent
og kan ikke angres." Kontoen forsvinner fra listen sammen med øktene,
rolletildelingene, passnøklene og autentiseringsappen sin. Det personen har
opprettet i appene — kunder, timer, utlegg, prosjekter, fakturaer — blir der det
er; bare kontoen forsvinner.

Sletting avvises i tre tilfeller, hvert forklart på skjermen: den siste aktive
eieren; en konto som ble klargjort av SCIM eller har logget inn gjennom
identitetsleverandøren ("Denne brukeren har SCIM- eller føderert identitetshistorikk
og kan ikke slettes." — deaktiver den i stedet, eller avklargjør den hos
leverandøren); og en eier som har gitt delegeringer som fortsatt finnes —
tilbakekall dem først.

## Følg opp invitasjoner

**Invitasjoner** lister alle invitasjoner som noen gang er sendt, med **Mottaker**,
**Rolle**, **Status** og **Opprettet / utløper**. **Inviter noen** tar deg til
brukersiden. Filtrer på **Alle**, **Ventende**, **Utløpt**, **Tilbakekalt** eller
**Godtatt**, eller søk på navn eller e-post; listen oppdaterer seg selv hvert halve
minutt.

En invitasjon er **Ventende** fra den sendes, og blir **Godtatt** når personen
oppretter kontoen sin, **Utløpt** når datoen passerer — sju dager etter sending med
mindre installasjonen setter en annen levetid — eller **Tilbakekalt** når du trekker
den tilbake. Handlingsmenyen på en ventende eller utløpt rad tilbyr:

- **Send på nytt** — utsteder en ny lenke med nytt utløp og sender den på e-post;
  enhver tidligere ventende invitasjon for adressen slutter å virke. Dette er måten
  å fornye en utløpt på.
- **Tilbakekall** — etter **Tilbakekalle invitasjonen?** ("_e-post_ vil ikke lenger
  kunne bruke denne invitasjonen.") er lenken død. Ingenting kan gjøres med en
  godtatt eller tilbakekalt invitasjon.

Kan e-posten ikke sendes, tilbakekalles invitasjonen umiddelbart og feilen vises,
så en lenke ingen mottok aldri forblir aktiv. E-postmalene og lenkene er beskrevet
under invitasjons- og gjenopprettingslenker i
[Identitet, autentisering og distribusjon](/nb/admin/authentication/).

## Administrer roller og tilgang

**Roller og tilgang** bygger "additive tillatelsessett" og tildeler dem. Siden
åpner for en eier, og for alle som har en delegering (nedenfor); alle andre ser
"Kontoen din kan ikke administrere roller og tilgang." Siden har tre faner.

### Roller

**Rolletillatelser** lister hver rolle. To er innebygd og merket **Beskyttet**:
**Bruker**, uten tillatelser, og **Eier**, med full tilgang til installasjonen.
Merket teller de **egendefinerte under administrasjon**, som du oppretter.

**Opprett egendefinert rolle** åpner en dialog med:

- **Internt navn** — små bokstaver, tall og bindestreker; det kan ikke endres etter
  lagring.
- **Visningsnavn** og **Beskrivelse** — det folk ser.
- Tillatelseskatalogen, gruppert etter modul og kategori (Identitet ·
  Administrasjon, Kunder · Kontakter, Timer · Timer, Fakturaer · Fakturaer, og så
  videre), hver med en kort beskrivelse av hva den tillater. En tillatelse som
  avslører mer enn den ser ut til — en persons kostnadssats, for eksempel — bærer
  merket **Sensitiv**.

Velg minst én tillatelse og velg **Lagre rolle**. **Rediger** på en egendefinert
rolle åpner den samme dialogen; sletteknappen spør **Slette egendefinert rolle?** —
"Tildelinger som bruker denne rollen kan endres." — fordi alle som hadde den, mister
det den ga.

Tillatelser er additive: en person har summen av rollenes tillatelser. Hva hver
tillatelse låser opp i en app, er listet på appens referanseside, for eksempel
[Customers](/en/reference/customers/), [Projects](/en/reference/projects/),
[Time](/en/reference/time/), [Expenses](/en/reference/expenses/) og
[Invoices](/en/reference/invoices/). Beskrivelsene i katalogen sier det navnet ikke
sier: I Fakturaer sender **Utstede fakturaer** også et dokument på e-post eller som
EHF, avbryter eller avklarer EHF-sendingene og registrerer at en faktura er levert;
**Administrere fakturering** omfatter også Peppol-ID-en, KID-avtalen, aksesspunktet for
e-faktura, purreinnstillingene og inkassosatsene; og **Registrere betalinger** dekker
hele kredittstyringen — bankimport og avvikskøen, purrekjøringer og brev, vent,
overlevering til inkasso og en kundes purreregel. **Administrer
identitet** (`identity:manage`) er identitetsmodulens egen, eneste tillatelse.

### Tildelinger

**Brukertildelinger** legger til eller fjerner egendefinerte roller for en person.
Velg en **bruker** (du kan ikke velge deg selv) for å se **Gjeldende roller**,
**Effektive tillatelser** (en eier viser **Alle tillatelser**) og velgeren
**Tildelbare egendefinerte roller**. Å krysse av eller fjerne en rolle lagres med en
gang: **Roller tildelt**. De innebygde rollene Bruker og Eier tildeles ikke her, men
under [Brukere](#rediger-en-bruker-tilbakestill-et-passord), gjennom kontoens
rolle.

### Delegeringer

**Delegert administrasjon** lar en eier overlate deler av rolleadministrasjonen til
noen "uten å gi tilgang til virksomhetsdata". Bare eiere ser knappen **Deleger
administrasjon**; en delegat ser "Delegeringer administreres av kontoeieren."

**Deleger rolleadministrasjon** ber om **Administrator** (ikke deg selv),
**Delegerbare tillatelser** personen kan legge i roller, **Forvaltede egendefinerte
roller** de kan redigere og tildele, et valgfritt **Utløp (valgfritt)** på formen
`2027-01-31T00:00:00Z`, og om de **Kan opprette egendefinerte roller**. **Gi
delegering** bekrefter: "Dette gir bare administrasjon, ikke datatilgang."

Delegaten får så **Roller og tilgang** i avatar-menyen sin. Der ser de de
beskyttede rollene og rollene som forvaltes for dem — andre egendefinerte roller
viser **Utenfor gjeldende delegeringsgrense** — og må velge et
**Delegeringsområde** eller **Tildelingsområde** før de oppretter en rolle eller
tildeler en. Roller de ikke kan røre, lar stå som de er: "Roller utenfor ditt
delegerte område beholdes."

Aktive delegeringer er listet under knappen, hver med **Tilbakekall** — "Dette
fjerner grensen for delegert administrasjon." — som avslutter delegatens tilgang til
denne siden med en gang, med mindre en annen delegering gjenstår.

### Grupper

Grupper — sett av brukere med roller knyttet til seg — finnes i Vantigo for
SCIM-klargjøring og API-et, ikke som et skjermbilde her. Når identitetsleverandøren
styrer grupper, får medlemmene de tilknyttede rollene automatisk; se
[SSO- og SCIM-drift](/nb/admin/sso-scim/).

## Sett opp en ny installasjon

Aller første gang noen åpner en fersk installasjon, sender innloggingssiden dem til
**Sett opp Vantigo**: "Opprett den første eierkontoen for denne installasjonen. Den
blir full administrator, med alle rettigheter og tilgang til systemadministrasjon."
Fyll inn **Visningsnavn**, **E-post** og **Passord** og velg **Opprett eierkonto**;
du logges inn og, når installasjonen krever tofaktorautentisering for
administratorer, sendes rett til å sette det opp. Den som sender inn dette skjemaet
først, eier installasjonen, så gjør det før adressen kan nås av noen andre. Når en
eier finnes, sier siden bare **Oppsett er ikke tilgjengelig** — "Denne
Vantigo-installasjonen er allerede satt opp." En installasjon kan i stedet settes
opp til å sende den første eieren en invitasjon på e-post; se første eier og lokale
kontoer i [Identitet, autentisering og distribusjon](/nb/admin/authentication/).

## Systemadministrasjon

Kontoen som ble opprettet ved oppsettet, er også installasjonens
**systemadministrator**, en status ingen skjermbilder kan gi til noen andre. Den
legger **Systemadministrasjon** til avatar-menyen, og åpner en systemadministrator
Vantigos rotadresse, havner de der i stedet for på kontrollpanelet.

Siden, **Systemstatus**, styrer vedlikeholdsmodus for hele systemet. Under
**Vedlikeholdsmodus** ("Blokker midlertidig tilgang for andre enn administratorer
under vedlikehold.") slår du på **Aktiver vedlikeholdsmodus**, skriver eventuelt en
**Melding som vises til brukere** (opptil 500 tegn) og velger **Lagre
vedlikeholdsinnstillinger**. Innen et halvt minutt ser alle andre brukere
**Midlertidig utilgjengelig** med meldingen din, eller "Vantigo gjennomgår
vedlikehold. Prøv igjen senere." når det ikke er noen, og kan ikke bruke
applikasjonen før du slår det av. Systemadministratorer fortsetter å arbeide, med
et gult banner — **Vedlikeholdsmodus er aktiv** — på hver side som påminnelse.

Å logge en bruker ut overalt er også en systemadministratoroperasjon, tilgjengelig
gjennom API-et snarere enn et skjermbilde; se øktlevetid og tilbakekalling i
[Identitet, autentisering og distribusjon](/nb/admin/authentication/).

## Når SSO eller SCIM-klargjøring er i bruk

Med single sign-on for arbeidsstyrken satt opp logger folk inn med **Fortsett med
_leverandør_**, og kontoene deres telles under **SSO** på brukersiden. Passordet og
det andre trinnet deres ligger hos leverandøren, og deres egen sikkerhetsside sier
**Lokalt passord er ikke tilgjengelig**. Med SCIM-klargjøring oppretter, oppdaterer
og deaktiverer leverandøren kontoer og grupper selv, og en slik konto kan ikke
slettes fra brukersiden — deaktiver den, eller la leverandøren avklargjøre den.
Eierkontoer er beskyttet mot leverandøren: SCIM kan ikke endre eller fjerne dem, og
en lokal eier med passord forblir veien inn om leverandøren er utilgjengelig.
Konfigurasjonen og driftshåndboken finnes i [SSO- og SCIM-drift](/nb/admin/sso-scim/).
