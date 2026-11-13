---
title: Administrasjon av arbeidsområdet
description: Brukere, invitasjoner, roller og tilgang, delegert administrasjon, førstegangsoppsett og systemadministrasjon.
sidebar:
  order: 90
sources:
  - apps/host/frontend
  - apps/server/internal/identity
  - apps/server/internal/db/migrations/00098_identity_ai_task_models.sql
---

Administrasjon av arbeidsområdet er der en **Eier** styrer hvem som kan bruke
Vantigo og hva de kan gjøre. Åpne avatar-menyen øverst til høyre og velg
**Administrer arbeidsområde**; sidemenyen har **Oversikt**, **Brukere**,
**Invitasjoner**, **Roller og tilgang** og, når Produksjon er på, **Stasjoner**, og, under **Integrasjoner**, **Microsoft Entra ID**,
**Google Workspace**, **Meta**, **Frakt** og, når Vantigo har satt opp KI for installasjonen, **KI-modeller** og **Assistent**.

Alt her krever eierrollen, med ett unntak: den en eier har delegert
rolleadministrasjon til, ser bare **Roller og tilgang**, både i avatar-menyen og i
sidemenyen; den som har `entra:manage`, `google:manage` eller `orders:carriers-manage` uten å være eier, ser bare
**Integrasjoner**
([nedenfor](#integrasjoner)); og den som har `identity:manage`, ser **Stasjoner**
([nedenfor](#stasjoner-i-verkstedet)). Den siste aktive eieren kan aldri degraderes, deaktiveres
eller slettes, så arbeidsområdet har alltid én.

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
under adressen (se [Kontoer Ansatte styrer](#kontoer-ansatte-styrer)). Mens SSO er på,
viser kolonnen **Enkel pålogging** hvem som er koblet til leverandøren (se
[Koble en konto til enkel pålogging](#koble-en-konto-til-enkel-pålogging)).

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

Mens modulen Assistent er på, har dialogen **Rediger bruker** også bryteren **Har assistenten**,
som gjelder når assistenten er satt til *Per person* ([Assistent](#assistent)).

Når Produksjon er på, viser dialogen også personens **Stasjon i verkstedet**: om de **Har
PIN-kode** — **Fjern PIN-kode** fjerner den; bare personen selv setter en — og om de **Har
kort**. For å registrere et kort setter du markøren i **Kort**, skanner kortet med leseren og
velger **Registrer kort**; **Fjern kort** tar det bort. Et kort som allerede er registrert på
en annen, avvises.

Å tilbakestille en annen administrators tofaktorautentisering tilbys ikke i dette
skjermbildet; det finnes i API-et for en eier hvis egen økt er MFA-bekreftet.

## Stasjoner i verkstedet

**Stasjoner** viser de delte nettbrettene i verkstedet der operatører logger på med PIN-kode
eller kort ([arbeid på en stasjon](/nb/user/manufacturing/#arbeid-på-en-delt-stasjon)). Den
krever `identity:manage` og finnes når Produksjon er på. Hver rad viser stasjonens
**Tilstand** (*Venter på tilkobling*, *Tilkoblet*, *Tilkoblingen er utløpt*, *Ikke tilkoblet*
eller *Tilbakekalt*), når den var **Sist aktiv** med adressen, hvem som er **Pålogget**, og
**Mislykkede pålogginger** når gjentatte feil PIN-koder eller kort der sendte e-post til
administratorene.

1. Velg **Registrer en stasjon** og gi den et **Navn**. Sett **Tidsavbrudd ved
   inaktivitet**, om den skal **Krev PIN-kode sammen med kort** (på, med mindre bare kort
   brukes i et kontrollert område), **Tillatte nettverk** den kan brukes fra (tomt for alle)
   og **Arbeidsstasjoner** den betjener — det siste krever Administrere stykklister og
   arbeidsstasjoner.
2. Velg **Lagre**. **Koblingskoden** vises én gang: åpne adressen som vises på nettbrettet, og
   skriv inn koden innen 15 minutter.
3. For å endre en stasjon velger du **Rediger** i radens meny. **Ny koblingskode** kobler den
   til på nytt — etter 90 dager, eller på et nytt nettbrett — og **Tilbakekall** stopper den
   med en gang og logger av alle som er pålogget der.

Administratorens detaljer — tokenet, ventetiden og varslene — står under
[Stasjoner i verkstedet](/nb/admin/authentication/#stasjoner-i-verkstedet).

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

## Agenter blant brukerne

En **virtuell ansatt** er en KI-agent, og kontoen er en bruker av en egen type. Den
står oppført i en egen del etter personene, aldri blant dem, merket med **KI**-merket på avataren, og tellingene skiller de to: arbeidsområdet
har så mange personer og så mange agenter, og ingenting begrenser noen av dem. En agent
logger aldri inn, har ikke passord, ikke autentiseringsapp og ingen innloggingslenke, så ingen
av handlingene over — **Rediger bruker**, et passord, **Deaktiver tilgang**, **Slett
bruker**, en kobling til single sign-on, en invitasjon — gjelder den. Den opprettes,
omdøpes, settes på pause og fjernes under [Virtuelle ansatte](/nb/user/agents/), og den får aldri e-post fra
Vantigo.

Gi en agent rollene dens under **Roller og tilgang**, som for alle andre. To ting nektes for
en agent: rollene **Eier** og **SystemAdmin**, og en delegering. En agent tilbys ikke der en
person velges for en kunde, et prosjekt, et timeprissett eller en ansattpost; bare ansvarlig
for en samtale i Kommunikasjon tilbyr agenter.

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

## Koble en konto til enkel pålogging

Når enkel pålogging slås på i et arbeidsområde som allerede har brukere, logger de
brukerne fortsatt inn med passord, og en adresse alene kobler aldri en innlogging
gjennom leverandøren til en eksisterende konto: det første forsøket gjennom
leverandøren avvises. Du, som eier, sier at en konto og en katalogkonto er samme
person, og koblingen fullføres neste gang personen logger inn gjennom leverandøren
(eller leverandøren sender kontoen). **Brukere** får en kolonne, **Enkel pålogging**,
til dette mens SSO er på; den finnes ikke mens SSO er av, og din egen rad har ingen
knapp.

Kolonnen viser én av tre ting:

- **Koblet til** med en grønn hake — personen har logget inn gjennom leverandøren, og
  kontoen er koblet.
- **Venter på første pålogging** — du har kunngjort koblingen, og den er ikke
  fullført ennå. Det kunngjorte brukerprinsipalnavnet (eller objekt-ID-en, når du bare
  oppga den) står under.
- **Koble til…** — en knapp: ingenting er koblet eller kunngjort.

**Koble en konto.**

1. Velg **Koble til…** i kontoens rad under **Brukere**, eller **Koble til enkel
   pålogging…** i menyen.
2. Kontroller **Brukerprinsipalnavn** i **Koble _navn_ til enkel pålogging**. Feltet er
   fylt ut med kontoens adresse; endre det når personens katalogkonto har et annet
   navn. Under **Mer** er **Objekt-ID** katalogkontoens id, for når du har den i stedet
   for eller i tillegg. Når leverandørens katalog kan søkes i, begynner dialogen med
   **Søk i katalogen**: skriv to tegn eller mer av personens navn eller adresse og velg
   kontoen deres, som fyller ut begge feltene (se
   [Velg kontoen i katalogen](#velg-kontoen-i-katalogen)).
3. Velg **Koble til**. Raden viser nå **Venter på første pålogging**.

**Koble til mange på en gang.** Når leverandørens katalog kan søkes i, foreslår **Finn
treff i katalogen…**, ved siden av søket under **Brukere**, for hver bruker som ikke er
koblet til, den ene katalogkontoen som passer nøyaktig, og **Koble til valgte** kobler
til dem du lar være krysset av; se
[Gå gjennom de nøyaktige treffene](#gå-gjennom-de-nøyaktige-treffene).

Ved personens neste innlogging gjennom leverandøren — eller når leverandøren sender
kontoen — kobler Vantigo katalogkontoen til kontoen du pekte ut, uansett hvilken
e-postadresse leverandøren oppgir, og logger personen inn som den kontoen. Navn,
adresse og roller blir som de var. Kunngjøringen forsvinner da, og raden viser
**Koblet til**.

**Endre eller avbryt.** Mens en rad viser **Venter på første pålogging**, tilbyr
menyen **Endre tilkoblingen…**, som åpner samme dialog og erstatter det du oppga, og
**Avbryt tilkoblingen**, som trekker kunngjøringen tilbake.

**Koble fra.** For en konto som er **Koblet til**, tilbyr menyen **Koble fra enkel
pålogging…**. Bekreftelsen sier at personen beholder kontoen sin og logger inn med en
lokal påloggingsopplysning, hvis vedkommende har en. En konto uten passord kan da ikke
logge inn før du sender en tilbakestilling av passord eller kobler den til igjen.
Leverandørens egen oppføring av kontoen, hvis den har sendt en, fjernes ikke; den tar
slutt når leverandøren sletter den.

Koblingen avvises, med ord, når:

- enkel pålogging ikke er slått på;
- kontoen er **Venter**: personens vei inn er invitasjonen (eller kontoen Ansatte har
  kunngjort for vedkommende);
- kontoen allerede er koblet til: koble den fra først;
- «En annen bruker er allerede koblet til denne kontoen.» — katalogkontoen er kunngjort
  for, eller allerede pekt ut av, en annen bruker, eller leverandørens klargjøring har
  allerede gitt den til en annen bruker;
- prinsipalnavnet ikke er en adresse, eller ingen av feltene er fylt ut.

En deaktivert konto kan kobles: personen kommer først inn når du aktiverer den. Når
leverandørens **Autoritet** er endret, gjelder en kunngjort kobling den gamle
leverandøren og oppfyller ingenting: raden vises som ikke koblet til, og å koble kontoen på nytt
erstatter den gamle kunngjøringen.

### Slå opp kontoene i katalogen

Du trenger ikke skrive inn kontoene når katalogen til den valgte leverandøren kan søkes
i: enkel pålogging er på, og modulen som betjener leverandørens katalog, er slått på —
**Microsoft Entra ID**, koblet til leietakeren i steg 1 til 3
([Microsoft Entra ID](/nb/user/entra/#koble-til-eksisterende-brukere)), eller **Google
Workspace**, koblet til organisasjonen og testet
([Google Workspace](/nb/user/google-workspace/#koble-til-eksisterende-brukere)). Vantigo
slår da opp kontoene i katalogen til leverandøren enkel pålogging er satt til. Begge måtene nedenfor er for en **eier**, innlogget med en
andre faktor når eiere må bruke en; mens det ikke finnes noen katalog å søke i, tilbys
de ikke, og feltene du skriver i, er alt som finnes.

#### Velg kontoen i katalogen

**Koble til…** i en brukers rad åpner **Koble _navn_ til enkel pålogging**, som da
begynner med **Søk i katalogen**. Skriv to tegn eller mer av personens navn, adresse
eller brukerprinsipalnavn; etter en pause viser listen kontoene som passer, etter
visningsnavn, hver med brukerprinsipalnavn, ansattnummer, *Gjest* for en gjest invitert
fra en annen organisasjon, og *Deaktivert* når kontoen er det. En konto noen allerede
har, viser *Venter på* og brukerens navn (eller *Venter på en annen bruker*),
*Klargjort for en annen bruker* eller *Kunngjort av Ansatte*, og kan ikke velges. Høyst
25 kontoer vises (20 fra Google Workspace); når det er så mange, ber den deg snevre inn
søket. Fra Google Workspace viser listen kontoene der fullt navn eller adresse —
primæradressen eller et alias — begynner med det du skrev; brukerprinsipalnavnet er
primæradressen, og det finnes ingen gjester.

Å velge en konto fyller ut **Brukerprinsipalnavn** og, under **Mer**, **Objekt-ID**.
De to hører sammen: endrer du brukerprinsipalnavnet, tømmes objekt-ID-en; søker du på
nytt etter et valg, tømmes begge. Kontroller dem og velg **Koble til**, som før.
Feltene du skriver i, blir stående: du kan fortsatt skrive navnet selv, og de er alt som
finnes mens katalogen ikke kan leses.

#### Gå gjennom de nøyaktige treffene

1. Velg **Finn treff i katalogen…** ved siden av søket under **Brukere**.
2. **Brukere som passer med en konto i katalogen** åpnes. En linje øverst teller
   *Brukere som ikke er koblet til*, *Nøyaktige treff* og dem som er *Utelatt fordi mer
   enn én konto passet*. Under den er hvert forslag en rad: **Bruker**, med adressen;
   **Konto i katalogen**, med visningsnavn, brukerprinsipalnavn, ansattnummer når den
   har et, og *Deaktivert* når kontoen er det; og **Treff på**: **E-post**,
   **Brukerprinsipalnavn**, **Ansattnummer**, eller flere.
3. Se over radene. En rad er krysset av fra start bare når kontoen er aktiv,
   brukerprinsipalnavnet traff, og brukeren ikke er eier. Alle andre rader står uten
   kryss og er merket **Sjekk treffet**, med grunnen: *Treff bare på e-post eller
   ansattnummer, ikke på brukerprinsipalnavnet personen logger på med. Forsikre deg om
   at det er samme person.*, eller *Denne brukeren er eier. Den som har denne kontoen,
   logger på med eierens tilgang.* Kryss av dem du har sjekket, og fjern krysset på dem
   du ikke vil ha.
4. Velg **Koble til valgte**, som viser hvor mange som er krysset av. Vantigo kobler
   parene til ett om gangen, og **Resultat** på hver rad sier *Koblet til*, eller med
   ord hvorfor det ble avvist — for eksempel at en annen bruker allerede er koblet til
   kontoen. **Brukere** oppdateres når det er ferdig.

En bruker foreslås bare når treffet er **nøyaktig**: adressen deres er kontoens e-post
eller brukerprinsipalnavn, uten hensyn til store og små bokstaver, eller
ansattnummeret deres er kontoens — og alt som traff, peker på den samme kontoen. I
Google Workspace er primæradressen både e-posten og brukerprinsipalnavnet, et alias
teller som **E-post**, og ansattnummeret er det Google har som kontoens
organisasjons-ID. Det
gjettes ikke på navn eller lignende adresser. Utelatt blir:

- en bruker som passer med mer enn én konto, eller der kontoen også passer med en annen
  bruker — telt som *Utelatt fordi mer enn én konto passet*; koble dem til én om
  gangen;
- gjestekontoer, invitert fra en annen organisasjon: de foreslås aldri og telles ikke;
- en konto noen andre allerede har: en kunngjort for en annen bruker, en leverandørens
  klargjøring har gitt en annen bruker, eller en Ansatte har kunngjort for en ny person.
  En konto leverandørens klargjøring allerede har gitt den samme brukeren, foreslås
  likevel;
- brukere som allerede er koblet til eller venter på første pålogging, brukere som er
  **Venter**, og deg selv — du kobler deg selv til ved å logge inn gjennom leverandøren.

Når ingen passer, sier vinduet det, og at du kobler dem til én om gangen fra kolonnen
**Enkel pålogging**. Når ikke alle brukerne ble sammenlignet (begge
leverandørene sammenligner de 1 000 første; Google Workspace leser også høyst 10 000 av
kontoene sine, og matcher utover det bare på adresse), sier det fra — koble til disse, og se så etter igjen. Å
finne treffene kan ta litt tid i en stor katalog; åpner du vinduet igjen i mellomtiden,
venter det på det samme svaret.

En rad som er koblet til, viser **Venter på første pålogging** under **Brukere** til
personen neste gang logger inn gjennom leverandøren, eller leverandøren sender kontoen;
da fullføres koblingen, og raden viser **Koblet til**. Navn, adresse og roller blir som
de var.

Et søk eller en gjennomgang avvises, med ord, når det ikke finnes noen katalog å søke
i — enkel pålogging ble slått av, eller leverandørens modul er ikke slått på — og når
katalogen ikke kan spørres: tilkoblingen avvises, den svarer ikke, eller den ber Vantigo
vente. Prøv igjen om et minutt, og sjekk tilkoblingen til leverandøren.

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
[Invoices](/en/reference/invoices/), [Staff](/en/reference/staff/),
[Planning](/en/reference/planning/) og [Orders](/en/reference/orders/). Beskrivelsene i katalogen sier det navnet ikke
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
— **Administrere ansatte** gjør det ikke, men holder kompetansekatalogen — og **Avgjøre
fravær** avgjør alles fravær og bekrefter også folks kompetanse og registrerer
kompetansesamtalene deres, aldri innehaverens egne. I Bemanning gir **Bruke Bemanning**
appen, innehaverens egne bekreftede allokeringer og ønsker, og de allokeringene i
Kalender; **Se planen**, merket sensitiv, viser planen, hver persons tidslinje og hvert
prosjekts kode, navn, kunde, status og datoer, tilbud medregnet, med behovene og
inntaket; og **Planlegge**, også sensitiv, skriver allokeringene og vilkårene deres —
oppsigelsestiden, overlappingen og karantenen i en kundekontrakt — og behovene, finner
kandidatene til et behov og fyller det, leser planleggernes notater og personenes
ønsker, og holder Bemanningsinnstillinger. En planlegger trenger også **Se
ansattoversikten** fra Ansatte, siden planen bare viser personene Ansatte viser dem. I
Kalender gir **Bruk Kalender** appen, **Se kollegers kalendere** når en kollega er opptatt
og ledig, og **Administrere kalenderinnstillinger**, merket sensitiv og i kategorien
Administrasjon, kanalen invitasjoner til folk utenfor bedriften sendes fra, siden den
avgjør hvem kundenes e-post kommer fra
([hvor invitasjonene sendes fra](/nb/user/calendar/#hvor-invitasjonene-sendes-fra)).
**Administrer
identitet** (`identity:manage`) er identitetsmodulens egen, eneste tillatelse. I Ordre
krever hver rettighet **Bruke Ordre** i tillegg; **Bekrefte ordrer** aksepterer også et
tilbud rett til en bekreftet ordre; **Håndtere innstillinger for Ordre** holder også
tilbudsmalene og kan ikke delegeres; **Håndtere abonnementer** lager, endrer, pauser og
avslutter abonnement; **Håndtere leveranser** lager, plukker, bokfører og kansellerer
leveranser; og **Håndtere fraktselskaper** er listet for frakten som kommer, og gjør ingenting
ennå ([Ordre](/nb/admin/orders/#gi-folk-tilgang)). I Lager lar **Bruke utgåtte partier** en person
ta ut et parti etter utløpsdatoen på en leveranse eller en produksjon, når de selv velger
partiet; den gjør ingenting før partisporing er slått på
([Lager](/en/reference/inventory/#expiry-and-today)). **Administrere partier** lar en person endre
et partis datoer, leverandørparti og merknad, legge til og fjerne filene, og sette og frigi
sperringer på siden for partiet ([Lager](/nb/user/inventory/#partier-og-serienumre)).

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

## Integrasjoner

**Integrasjoner** er den andre seksjonen i sidemenyen til **Administrer arbeidsområde**:
stedet selskapets tilkoblinger til systemer andre steder styres, og eierens innstillinger for
assistenten. Den har fem punkter.
**Microsoft Entra ID** kobler Vantigo til selskapets Microsoft Entra ID-leietaker; siden har
fire faner — **Status**, **Tilkobling**, **Kontoer** og **Outlook og e-post** — beskrevet i
[Microsoft Entra ID](/nb/user/entra/#hvor-den-er-og-de-fire-fanene). Det er der bare mens
modulen Microsoft Entra ID er på i denne installasjonen, og bare for den som har
`entra:manage`; en eier har alle rettigheter. **Google Workspace** kobler Vantigo til
selskapets Google Workspace-organisasjon; siden har fire faner — **Status**,
**Tilkobling**, **Kontoer** og **Kalender og Gmail** — beskrevet i [Google
Workspace](/nb/user/google-workspace/#hvor-den-er-og-de-fire-fanene). Det er der bare mens
modulen Google Workspace er på, og bare for den som har `google:manage`.
**Meta** kobler til Facebook Messenger: Meta-appen, webhooken, innstillingen for Human Agent
og Facebook-sidene, beskrevet i [Meta](/nb/admin/meta/). Den er bare eierens, og er der bare
mens modulen Meta er på.
**Frakt** holder fraktkontoene Ordre bestiller sendinger med — Mybring-brukeren og
API-nøkkelen hos Bring, kundenumrene, testmodus — og standardkontoen per lagersted, beskrevet i
[Frakt](/nb/admin/shipping/). Det er der bare mens modulen Ordre er på, og bare for den som har
`orders:carriers-manage`.
**KI-modeller** er bare eierens, og er der bare
mens Vantigo har satt opp KI for denne installasjonen ([nedenfor](#ki-modeller)). **Assistent** er
også bare eierens, og er der mens modulen Assistent er på og KI er satt opp
([nedenfor](#assistent)).

- En eier ser hele Administrer arbeidsområde, med **Integrasjoner** etter resten.
- Den som har `entra:manage`, `google:manage` eller `orders:carriers-manage` uten å være
  eier, ser bare **Integrasjoner** i sidemenyen — slik en person med delegert
  rolleadministrasjon bare ser **Roller og tilgang** — med sidene rettighetene åpner, og
  finner **Microsoft Entra ID**, **Google Workspace** eller **Frakt** i avatar-menyen. Stegene som er en eiers, leser
  vedkommende uten skjemaene. Vedkommende ser ikke **KI-modeller** eller **Assistent**.
- Uten `entra:manage`, `google:manage` eller `orders:carriers-manage` (og uten å være eier)
  finnes ingen seksjon **Integrasjoner**. For en eier finnes den heller ikke mens modulene
  Microsoft Entra ID, Google Workspace og Ordre er av og KI ikke er satt opp.

Dine egne kontoer andre steder, som Outlook, kobler du til under **Innstillinger** ›
**Tilkoblinger** ([Tilkoblinger](/nb/user/connections/)); postboksene Kommunikasjon
sender fra, er dens **Kanaler**, ikke her. [Konfigurasjon](/nb/admin/configuration/) sier
hvilke innstillinger som er arbeidsområdets og hvilke som er installasjonens.

### KI-modeller

Noen funksjoner ber en KI-modell om hjelp — i dag Kommunikasjons *Draft a reply* og
*Suggest the customer*. **KI-modeller** (en eiers side under **Integrasjoner**) er der du
velger hvilken modell hver av disse jobbene bruker. Siden finnes bare mens Vantigo har satt
opp KI for installasjonen: ellers er det ikke noe punkt i sidemenyen, og adressen svarer
«siden finnes ikke».

**Det du ser.** Kortet **Abonnementet** forteller hva Vantigo har satt opp, som fakta uten
felt å redigere: **Leverandør**, **Modeller abonnementet tillater**, og «En API-nøkkel er
satt opp av Vantigo.» — eller, for en modell som kjører på ditt eget nettverk og ikke trenger
noen, «Ingen API-nøkkel er satt opp.» Selve nøkkelen vises aldri, for noen, noe sted.
Leverandøren står som **OpenAI-compatible** for en slik modell, **OpenAI** for OpenAI og **Anthropic** når modellene
er Claude-modeller, der spørsmål og data sendes til Anthropic (innstillingene til Assistenten sier det). Under kommer ett kort
for hver modul som bruker KI, med modulens eget navn, og en rad for hver **oppgave** den
utfører: hva modellen blir bedt om å gjøre og med hvilke data, og en liste over modellene
abonnementet tillater. Oppgavene har engelske navn. En modul som er slått av, er ikke med.

Et merke sier hvor modellen kommer fra så lenge ingen har valgt: **Standard** er modellen
oppgavens modul anbefaler, **Abonnementets standard** abonnementets første modell. En oppgave
noen har valgt for, har ikke noe merke.

**Det du endrer.** Åpne listen til en oppgave og velg en modell. Den lagres med en gang —
«Modellen er lagret» — og oppgaven bruker den neste gang den kjøres; alle servere følger
innen omtrent 20 sekunder. Valget gjelder den ene oppgaven; de andre er uendret. Hvis noen
andre endret den samme oppgaven etter at du åpnet siden, får du beskjed om «KI-modellene er
endret», siden lastes på nytt, og du velger på nytt. Når abonnementet bare tillater én modell,
er alle lister låst med den modellen vist, og det er ingenting å velge.

**Det Vantigo bestemmer.** Leverandøren, nøkkelen, modellene abonnementet tillater og hva
hver oppgave er — en oppgave kommer sammen med funksjonen som bruker den. En modell du valgte
som abonnementet senere slutter å tillate, blir ignorert: oppgaven bruker modulens anbefaling,
eller abonnementets første modell, til du velger på nytt. Hvert valg føres i revisjonsloggen
som `ai-task-model.changed`, med modellen før og etter, og ingenting føres om hva modellen ble
spurt om eller svarte utover det funksjonen selv beholder (Kommunikasjon beholder sammendrag og
tall, aldri tekst:
[AI draft and customer suggestion](/en/reference/communications/#ai-draft-and-customer-suggestion)).
Innstillingene som hører til installasjonen, står i [Konfigurasjon](/nb/admin/configuration/#ki).

### Assistent

[Assistenten](/nb/user/assistant/) svarer på folks spørsmål fra dokumentasjonen og leser
dataene de har lov til å lese. **Assistent** (en eiers side under **Integrasjoner**) er der du
bestemmer hvem som har den, hvor lenge samtaler beholdes, hva den kan bruke, hvor mange tokens en
person kan bruke per dag og hvem som har brukt den. Siden finnes mens
modulen Assistent er på i denne installasjonen og installasjonen har en AI-leverandør; uten en
tilbys assistenten ikke til noen. Den er bare eierens.
[Konfigurasjon](/nb/admin/configuration/#ki) sier hva installasjonen setter opp.

**Hvem har assistenten.** Velg én av tre, og valget lagres med en gang:

| Modus | Hvem har assistenten |
| --- | --- |
| **Slått på for alle** (standard) | Alle aktive brukere. De personlige bryterne ignoreres. |
| **Per person** | Bare de som har sin egen bryter på. |
| **Slått av for alle** | Ingen, uansett hva bryterne sier. |

Den personlige bryteren er **Har assistenten**, i dialogen **Rediger bruker** for en person under
**Brukere** (handlingsmenyen på raden › **Rediger detaljer**). Den lagres av seg selv og teller
bare i modusen **Per person**; dialogen sier det ved siden av. Den er av til du slår den på. Den
som har en deaktivert eller utestengt konto, har ikke assistenten i noen modus.

**Hvor lenge samtaler beholdes.** **Behold samtaler i** tar 1 til 60 måneder, 12 som standard;
velg **Lagre** etter at du har endret den. En samtale slettes, hel, når siste aktivitet er eldre
enn det, og slettingen kjører én gang i døgnet, så en kortere tid gjelder fra neste daglige
kjøring. Ingen, heller ikke en eier, kan lese en persons samtaler; bare hvor lenge de lever, er
din å bestemme.

**Hvor dataene går.** En merknad ved siden av innstillingene sier det rett ut. Når modellen
kjører på installasjonens eget nettverk: *Modellen kjører på denne installasjonen. Spørsmål og
data sendes ikke andre steder.* Ellers: *Spørsmål og dataene assistenten leser, sendes til*
leverandøren — spørsmålene, dokumentasjonen den finner og det den leser for den som spør. Å slå
på **Kan lese** nedenfor for en leverandør som ikke er lokal, er en beslutning om å la de dataene
forlate installasjonen; merknaden gjentas ved siden av bryteren.

#### Hva assistenten kan bruke

Assistenten leser gjennom det samme API-et som skjermbildene bruker, som den som spør, så den
kan aldri bruke mer enn den personen kan. Dette kortet bestemmer hvor mye av det den kan bruke
*i det hele tatt*. Nivåene kan bare **snevre inn** det personen kan gjøre, aldri utvide det, og de
endrer aldri hva personen ser på et skjermbilde.

- **Sensitive data** har to verdier, **Ikke tilgjengelig** (standard) og **Kan lese**. Den
  setter standardnivået for hver tillatelse som er merket **Sensitiv** i rolleeditoren — i
  Kunder for eksempel kontaktene, tidslinjen, den juridiske identiteten og faktureringsprofilene.
  Med **Ikke tilgjengelig** kan assistenten ikke lese bak en sensitiv tillatelse, heller ikke for
  en person som har den.
- **Tillatelsestabellen** lister tillatelsene til modulene som er slått på, gruppert etter modul
  og kategori, med merket **Sensitiv** der det finnes. Hver har et **nivå**, valgt på raden og
  lagret med en gang:

  | Nivå | Assistenten kan |
  | --- | --- |
  | **Av** | Bruke ingen operasjon der tilgangsregelen nevner tillatelsen. |
  | **Lese** | Lese bak tillatelsen. Operasjoner som endrer data er ikke tilgjengelige for den. |
  | **Lese og endre** | Bruke tillatelsen så langt personen kan. Assistenten kan **foreslå** endringer bak den: hver vises for personen på et bekreftelseskort og utføres, som personen, først når de trykker **Bekreft**. |

- **Standarder og overstyringer.** En tillatelse som ikke er sensitiv, starter på **Lese og
  endre**; en sensitiv tar verdien til bryteren **Sensitive data**, **Av** eller **Lese**. Bare
  nivåene du selv setter, lagres, merket **Endret**, så en tillatelse en senere utgivelse legger
  til, får standarden sin. **Tilbakestill til standard** på en rad fjerner valget ditt, og
  filteret **Vis** snevrer tabellen inn til **Alle**, **Sensitive** eller de **Endret**.
- **En endring gjelder fra assistentens neste steg**, innen omtrent ett sekund på hvilken som
  helst server, også i et svar som allerede er i gang.
- **Operasjoner der regelen ikke nevner noen tillatelse** — de som er åpne for alle påloggede
  eller for en forretningskonto — berøres ikke av nivåene. Operasjoner som hører til eiere og
  administratorer, og opplasting og nedlasting av filer, er aldri tilgjengelige for assistenten,
  uansett hva du setter.
- **Hver endring føres** med nivået før og etter, hvem som gjorde den og når, i assistentens
  egen logg. [Referansen](/en/reference/assistant/#the-owners-audit) sier hvor.

**Masseendringer følger nivåene for hvert element.** Når assistenten endrer mange poster på én gang
([Masseendringer](/nb/user/assistant/#masseendringer)), endres hvert element som personen, gjennom den
samme operasjonen, så nivået for tillatelsen den nevner, gjelder for hvert eneste. **Å snevre inn et nivå
stopper en masseendring som kjører**: jobben sjekker nivåene mellom bolkene på 100 elementer, ender som
*Stoppet* med årsaken *Eieren innskrenket hva assistenten kan endre*, og elementene den ikke har kommet
til, forblir uendret. En masseendring som venter på at personen skal trykke **Start** eller **Fortsett**,
kan ikke lenger startes eller fortsettes. Det som allerede er endret, forblir endret.

Et kort som venter når du snevrer inn et nivå, kan ikke lenger bekreftes: **Bekreft** svarer *Assistenten
har ikke lenger lov til å gjøre denne endringen*, og personen kan fortsatt avbryte det.

Hvis noen andre endret innstillingene mens du hadde siden åpen, får du beskjed om *Noen andre har
lagret disse innstillingene*, siden lastes på nytt, og du gjør endringen din på nytt.

#### Daglig tokenbudsjett

**Daglig tokenbudsjett per person** begrenser hvor mange modelltokens én person kan bruke i løpet av
en forretningsdag, både det de sender og det som kommer tilbake. Det er tomt som standard, som betyr ingen
grense. Skriv et tall fra 1 000 til 1 000 000 000 og velg **Lagre**; tøm feltet og lagre for å fjerne grensen
igjen. Dagen er forretningstidssonens.

Når en person har brukt budsjettet, avvises neste melding med *Du har brukt opp assistentens tokenbudsjett
for i dag*, og et svar som når det underveis, stopper og sier fra; en endring personen allerede hadde
bekreftet, angres ikke. Assistenten er tilgjengelig for dem igjen neste forretningsdag. Å slette en
samtale gir ikke tokens tilbake: bruken telles mens den skjer. Budsjettet lagres under den samme versjonen
som de andre innstillingene og føres i assistentens logg med verdien før og etter.

#### Bruk av assistenten

**Bruk av assistenten** lister, per person og forretningsdag, hvor mange samtaler de begynte, hvor mange
ganger de spurte (kjøringer), modelltokens inn og ut, og hvor mange endringer de bekreftet og hvor mange av
dem som mislyktes. Velg **Siste 7 dager**, **Siste 30 dager** eller **Siste 90 dager**. Den er for kostnad og
misbruk. Den viser **ikke noe innhold**: ingen titler, ingen spørsmål og ikke hva en endring var; ingen,
heller ikke en eier, kan lese en samtale. Tallene beholdes like lenge som samtalene.

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
   plattform som leverer en, velg **Tokenfil** under **Legitimasjon** og skriv stien, i
   katalogen distribusjonen tillater for den.
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

Det samme kortet finnes på en leverandørs egen side, ferdig utfylt: fanen **Kontoer** for
**Microsoft Entra ID** fyller inn leietakerens autoritet og applikasjonens klient-ID, og
fanen **Kontoer** for **Google Workspace** fyller i steget **Innlogging** inn Google,
klient-ID-en til den interne nettklienten og hvert verifiserte domene i organisasjonen som
**Domener** ([Google Workspace](/nb/user/google-workspace/#fanen-kontoer)). Det fyller bare
ut et skjema som ikke har noe i seg ennå, aldri over det som er lagret.

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
