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

**Oversikt** åpner **Administrasjonspanel**, "En oversikt over kontoer og
identitetsintegrasjoner, og arbeidsområdets egne innstillinger.":

- **Personer** — **Totalt**, **Aktive**, **Deaktivert** kontoer og **Ventende
  invitasjoner**, med lenken **Vis brukere**.
- **Invitasjoner** — antall ventende og **Administrer invitasjoner**.
- **Tilgangskontroll** — **Administrer roller og tilgang**.
- **Identitetsintegrasjoner** — om **SSO** er på og hvilken leverandør (Microsoft
  Entra ID, Google Workspace, eller bare **Aktivert**), at **Klientautentisering**
  **Settes på Single sign-on-kortet**, og **Siste vellykkede SSO-bruk**.
- **SCIM-klargjøring** — **Status** og **Siste autentiserte forespørsel**.
- **Operativ aktivitet** — en merknad om at disse statuskortene bare viser
  ikke-sensitiv identitetsaktivitet. Legitimasjon vises aldri; kortet **Single
  sign-on** nedenfor viser leverandørens innstillinger, men aldri hemmeligheten eller
  et token.

SSO og SCIM settes lenger ned på den samme oversikten, på kortet **Single sign-on**;
se [Sett opp single sign-on](#sett-opp-single-sign-on).

## Finn en bruker

**Brukere** lister hver konto med **Profilbilde**, navn og e-post, **Rolle** (Bruker
eller Eier), **Tilgang** og **2FA** (På eller Av). Tilgang viser **Aktiv**,
**Venter** (en konto Ansatte har laget, som venter på at invitasjonen godtas),
**Deaktivert av en eier**, **Deaktivert av Ansatte** eller **Midlertidig utestengt** —
det siste etter fem feil passord på rad, og det går over av seg selv etter 15
minutter. En konto Ansatte styrer, har **Styres av Ansatte · nr.** og ansattnummeret
under adressen (se [Kontoer Ansatte styrer](#kontoer-ansatte-styrer)).

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
tilbakekall dem først. En konto Ansatte styrer, har **Slett bruker** nedtonet, med
grunnen under (nedenfor).

## Kontoer Ansatte styrer

Når HR slår på kontoflagget for en person i [Ansatte](/nb/user/staff/), oppretter
Ansatte kontoen før første arbeidsdag, aktiverer den på dagen og deaktiverer den
etter den siste — uten noen gang å slette den. En slik konto **styres av Ansatte**, med
ansattnummeret som knytter den til personen, og brukersiden viser den slik. Ansatte
gjør bare noe med kontoer den styrer: en konto noen hadde fra før, blir Ansattes først
når en eier lar Ansatte styre den.

**En ventende konto.** Ansatte oppretter kontoen som **Venter**: den finnes, med
personens navn og rollene Ansatte kan gi, men kan ikke brukes før personen godtar
invitasjonen Ansatte sender og velger et passord (se
[Kom i gang](/nb/user/getting-started/#når-kontoen-din-ble-satt-opp-fra-ansattregisteret)).
**Aktiver tilgang**, **Send e-post for tilbakestilling** og **Angi startpassord** i
menyen er nedtonet — "Har ikke godtatt invitasjonen ennå: kontoen åpnes først når
personen godtar den." — og å gjøre kontoen til eier avvises: "En ventende bruker kan
ikke gjøres til eier før vedkommende har godtatt invitasjonen." Du kan fortsatt velge
**Deaktiver tilgang**; da aktiverer ikke Ansatte den, og HR ser hvorfor på personens
kontofane.

**Hvem som deaktiverte den.** Ansatte deaktiverer en konto dagen etter at personens
siste arbeidsforhold er slutt; raden viser **Deaktivert av Ansatte**. Deaktiverer du
en konto selv, viser den **Deaktivert av en eier**, også over Ansattes egen
deaktivering, og Ansatte aktiverer aldri en konto en eier har deaktivert. **Aktiver
tilgang** fjerner begge.

**La Ansatte styre en konto.** For en person som hadde en konto før HR registrerte
vedkommende, åpner du kontoens meny og velger **La Ansatte styre**. Skriv personens
**Ansattnummer** slik Ansatte har det — "1 til 16 tegn: bokstavene a–z, sifre og
bindestreker." — og velg **La Ansatte styre**: "Ansatte styrer nå kontoen", og
kontojobben i Ansatte tar over kontoen neste gang den kjører. Menyen tilbyr det ikke
for en eier, og avviser, med ord:

- "En annen bruker har allerede dette ansattnummeret." — en annen konto har det, med
  store eller små bokstaver.
- "En eier eller en systemadministrator kan ikke styres av Ansatte; slike kontoer tar
  en eier seg av for hånd."
- "Skriv 1 til 16 tegn, hvert en bokstav a–z, et siffer eller en bindestrek." — et
  nummer utenfor Ansattes tegnsett, sagt før noe sendes.

**Stopp styringen.** **Stopp styringen** på en konto Ansatte styrer, spør **Skal
Ansatte slutte å styre denne kontoen?** — "Ansatte vil ikke lenger aktivere,
deaktivere eller oppdatere _navn_. Kontoen blir værende slik den er nå; en ventende
konto venter til personen godtar invitasjonen eller du sletter den." Etterpå lar
Ansatte kontoen være, og HR ser at den ikke styres på personens kontofane. Gjør du en
konto Ansatte styrer til eier, slutter Ansatte å styre den i samme endring; dialogen
**Rediger bruker** sier det når du velger **Eier**: "Gjør du vedkommende til eier,
slutter Ansatte å styre denne kontoen." For å gi en konto Ansatte styrer et annet
ansattnummer stopper du styringen og lar Ansatte styre den igjen med det nye.

**Slett.** En konto Ansatte styrer, kan ikke slettes: **Slett bruker** er nedtonet, med
"Ansatte styrer denne kontoen, så den kan ikke slettes. Stopp styringen først." under.
Stopp styringen, og slett den så som enhver annen.

Hva Ansatte gjør på sin side, og når, er beskrevet i
[administrasjonsveiledningen for Ansatte](/nb/admin/staff/).

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

En tilbakekalt invitasjon sier hvorfor under statusen:

- **Tilbakekalt av en eier** — du eller en annen eier tilbakekalte den.
- **Erstattet av en nyere invitasjon eller en konto** — adressen ble invitert på nytt,
  eller det ble laget en konto for den.
- **E-posten kunne ikke sendes** — se nedenfor.
- **Brukerens adresse ble endret** — kontoen invitasjonen gjaldt, fikk en annen
  adresse.
- **Ansatte deaktiverte kontoen** — Ansatte deaktiverte den ventende kontoen den
  gjaldt.

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

### Roller Ansatte kan gi

Under rollene ser en eier **Roller Ansatte kan gi**: de egendefinerte rollene hver
konto [Ansatte styrer](#kontoer-ansatte-styrer) får når Ansatte aktiverer den, i
tillegg til den innebygde rollen Bruker. Ansatte oppretter ingen konto før du har
valgt minst én; til da sier delen "Ingen rolle valgt: Ansatte oppretter ingen kontoer
før du velger en." og dashbordet i Ansatte lister personene som venter.

Hver egendefinerte rolle står med en avkrysningsboks og tillatelsene den har. Kryss av
rollene de ansatte skal ha, og velg **Lagre roller Ansatte kan gi**, som blir
tilgjengelig når du har endret settet: hele settet lagres på en gang, så en rolle du fjerner krysset for, tas ut av det — "Roller Ansatte kan gi
er lagret".

En rolle som gir administrasjon, kan ikke være blant dem, slik at HR, som avgjør hvem
som får en konto, aldri kan gi noen administrasjon: en rolle med **Administrer
identitet**, **Administrere ansatte**, **Avgjøre fravær**, **Vise og slette
personopplysninger** eller en annen tillatelse som ikke kan delegeres, er merket **Gir
administrasjon** og kan ikke krysses av. En som alt var i settet, kan fortsatt fjernes;
et sett som beholder den, avvises med rollen og tillatelsen navngitt —
"_rolle_ har _tillatelse_ (_nøkkel_), som gir administrasjon; Ansatte kan ikke gi den."
— og ingenting endres. En rolle i settet beholder den grensen når den redigeres: å gi
den en slik tillatelse i rolledialogen avvises med "Denne rollen er en Ansatte kan gi,
så den kan ikke få en tillatelse som gir administrasjon. Ta den ut av Roller Ansatte
kan gi først." Sletter du en rolle, tas den ut av settet.

Tillatelser er additive: en person har summen av rollenes tillatelser. Hva hver
tillatelse låser opp i en app, er listet på appens referanseside, for eksempel
[Customers](/en/reference/customers/), [Projects](/en/reference/projects/),
[Time](/en/reference/time/), [Expenses](/en/reference/expenses/),
[Invoices](/en/reference/invoices/) og [Staff](/en/reference/staff/). Beskrivelsene i katalogen sier det navnet ikke
sier: I Fakturaer sender **Utstede fakturaer** også et dokument på e-post eller som
EHF, avbryter eller avklarer EHF-sendingene, registrerer at en faktura er levert, lager
eller trekker tilbake en Vipps-betalingslenke og tar imot en Vipps-betaling på stedet —
og lager, sammen med **Lage fakturaer**, en hurtigfaktura; **Administrere fakturering**
omfatter også Peppol-ID-en, KID-avtalen, aksesspunktet for e-faktura,
purreinnstillingene, inkassosatsene, Vipps-nøklene og betalingsinnstillingene, og
forlater en Vipps-betaling som står fast; og **Registrere betalinger** dekker hele
kredittstyringen — bankimport og avvikskøen, purrekjøringer og brev, vent, overlevering
til inkasso, en kundes purreregel og å registrere en dags Vipps-betalinger som avstemt. I Ansatte er **Vise og slette
personopplysninger** den eneste tillatelsen som viser et fødselsnummer eller en bankkonto
— **Administrere ansatte** gjør det ikke — og **Avgjøre fravær** kan gis allerede, men
tillater ingenting før fravær kommer til Ansatte. **Administrer
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

## Innstillinger for arbeidsområdet

Kortet **Arbeidsområde** på oversikten holder det som gjelder hele installasjonen og
som hver modul leser. I dag er det én innstilling, **Virksomhetens tidssone**:
kalenderen hver regnskapsdag dømmes etter — dagen en faktura utstedes og forfaller, en
periodelås, dagen en reise startet, «i dag» i Ansatte. Den er `Europe/Oslo` til en
eier endrer den. Innstillingssidene i Ansatte og Utlegg viser den også, men bare her
kan den endres.

1. På oversikten, under **Arbeidsområde**, velg sonen fra listen. Bruk en bysone som
   `Europe/Oslo`; et navn serveren og databasen leser forskjellig, som `CET`, avvises
   med en setning som sier det.
2. Velg **Lagre innstillinger for arbeidsområdet** og bekreft **Endre virksomhetens
   tidssone?** Dialogen sier hva som endres: fra neste forespørsel leses «i dag» og
   hver dag som utledes av et tidspunkt i den nye sonen. Datoer som alt er skrevet —
   en fakturas utstedelsesdato, en reises diettdager — blir som de er.

Hver endring føres i revisjonsloggen for autorisasjon som
`workspace.business_time_zone_changed`, med sonen før og etter. Der en person ser et
tidspunkt, ser de det i denne sonen også, til en egen personlig tidssone finnes.

### Sett opp single sign-on

Kortet **Single sign-on**, i full bredde under oversiktens andre kort, kobler
arbeidsområdet til en identitetsleverandør for arbeidsstyrken — Microsoft Entra ID
eller Google Workspace — så folk logger inn med jobbkontoen sin, og gir en katalog
tokenet den klargjør kontoer med (SCIM). Det krever eierrollen og, når installasjonen
krever det, et andre trinn. Hva som skal gjøres hos leverandøren — appregistreringen,
hemmeligheten, omdirigeringen — er beskrevet trinn for trinn i
[SSO- og SCIM-drift](/nb/admin/sso-scim/).

Slik lar du folk logge inn gjennom en leverandør:

1. Kopier **Tilbakekalls-URL** og registrer den hos leverandøren.
2. Velg **Leverandør**: **Microsoft Entra ID** eller **Google Workspace**.
3. Fyll inn **Autoritet**, leverandørens utstederadresse —
   `https://login.microsoftonline.com/<tenant-guid>/v2.0` for Entra; for Google fyller
   kortet inn `https://accounts.google.com` — og **Klient-ID**.
4. Oppgi legitimasjonen: lim inn **Klienthemmelighet**, eller, for Entra på en
   plattform som leverer en, velg **Tokenfil** under **Legitimasjon** og skriv stien.
   Når en hemmelighet er lagret, er feltet tomt og sier «Satt _dato_. La feltet stå
   tomt for å beholde den.»: skriv en ny bare for å erstatte den. En ny
   **Leverandør** eller **Klient-ID** trenger sin egen hemmelighet eller tokenfil.
5. For Google, list opp **Domener** folk kan logge inn fra.
6. Gi eventuelt knappen på innloggingssiden et **Visningsnavn**; står det tomt, er det
   `Workforce SSO`.
7. Velg **Lagre**. Kortet sier «OIDC-innstillingene er lagret»; et felt leverandøren
   ikke ville godta, merkes med formen det må ha.

Innen 20 sekunder tilbyr innloggingssiden **Fortsett med** og visningsnavnet, på hver
server, uten omstart.

Endrer du **Autoritet** mens folk allerede logger inn gjennom leverandøren, spør
Vantigo først — «Endre leverandørens autoritet?» — fordi folk som er koblet under den
gamle autoriteten, ikke gjenkjennes under den nye, så deres neste innlogging gjennom
leverandøren avvises så lenge kontoen deres finnes. Velg **Endre autoritet** bare når
du flytter til en annen leietaker eller leverandør. **Slå av** — eller **Av** som
leverandør og **Lagre** — spør også først, «Slå av SSO?», og mens folk er koblet til
leverandøren, spør en ny dialog, «Personer er koblet til denne leverandøren», en gang
til. Har noen andre lagret kortet etter at du åpnet det, sier det «Innstillingene er
endret»: velg **Last inn på nytt** og gjør endringen igjen.

Slik gir du en katalog SCIM-tokenet:

1. Kopier **Endepunkt-URL** inn i katalogens klargjøringsinnstillinger.
2. Velg **Nytt token**. Finnes det allerede et token, blir du først spurt, «Utstede et
   nytt token?», i feltet «Godta det nåværende tokenet i (timer)» — 1 til 24, 24 som
   standard — så katalogen kan bytte uten opphold; velg **Utsted token**.
3. Dialogen, «Det nye SCIM-tokenet», viser det **én gang**: velg **Kopier token**, lim
   det inn i katalogen, og velg **Ferdig** først etterpå. Det vises ikke igjen; er det
   tapt, utsted et nytt.
4. Når katalogen bruker det nye tokenet, velg **Avslutt overlapp** for å slutte å godta
   det gamle, eller la overlappet løpe ut.

**Slå av** i SCIM-delen spør først, «Slå av SCIM?», og stopper SCIM: innen 20 sekunder
avvises katalogens forespørsler, og kontoene og gruppene den opprettet, blir som de er.

Når innstillingene ble importert fra installasjonens miljø, sier kortet det —
**Importert fra miljøet** — og ber om at variablene fjernes; det er for den som
drifter installasjonen ([Før kortet](/nb/admin/sso-scim/#før-kortet-miljøvariablene)).
Meldingen blir stående til neste endring som gjøres på kortet. Hver endring føres i
revisjonsloggen for autorisasjon som `sso-settings.changed`, aldri med hemmeligheten
eller et token.

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
en lokal eier med passord forblir veien inn om leverandøren er utilgjengelig — også
til kortet **Single sign-on**, der en eier [setter opp begge](#sett-opp-single-sign-on).
Driftshåndboken finnes i [SSO- og SCIM-drift](/nb/admin/sso-scim/).

En konto leverandøren oppretter for noen i Ansatte som Ansatte lot leverandøren lage
kontoen til — kontotypen **Microsoft Entra ID** — kommer til brukersiden allerede
merket **Styres av Ansatte · nr.** med personens ansattnummer, og ingen ventende invitasjon: Ansatte
kunngjorde den, og Vantigo kjenner den igjen på det leverandøren sender — ansattnummeret,
brukerhovednavnet eller kontoens objekt-ID. Du
trenger ikke ta den inn under Ansatte. En konto ingen kunngjorde, kommer som før, og en
eier tar den inn hvis den hører til Ansatte
([den kunngjorte kontoen](/nb/admin/authentication/#den-kunngjorte-kontoen)).
