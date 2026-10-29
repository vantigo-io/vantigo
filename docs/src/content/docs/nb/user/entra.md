---
title: Microsoft Entra ID
description: Å koble selskapets Microsoft Entra ID-leietaker til på siden Microsoft Entra ID — kortet Status, veiviserens seks steg og hva Ferdig betyr på hvert, tillatelsene som skal kopieres, hemmeligheten som vises én gang, tilkoblingstesten, kontoreglene og bryteren Outlook-tilkobling med status, antall og omdirigerings-URI, bryteren E-postkanaler med status og kommandoene for Exchange Online som skal kopieres, varselet om tillatelser Vantigo ikke bruker, tabellen Grupper å bekrefte en gruppe Vantigo ikke opprettet, og å koble til eksisterende brukere med katalogvelgeren og gjennomgangen av nøyaktige treff — og hvem som ser hva.
sidebar:
  order: 47
sources:
  - apps/entra/frontend
  - apps/host/frontend/src/routes/entra
  - apps/host/frontend/src/routes/entra.tsx
---

Appen **Microsoft Entra ID** kobler Vantigo til selskapets Microsoft Entra
ID-leietaker, slik at kontoene Ansatte gir folk, lages der: hver persons
Microsoft-konto opprettes før første arbeidsdag, aktiveres den dagen med et midlertidig
tilgangspass, holdes oppdatert og deaktiveres når personen slutter. Ansatte bestemmer
hvem som får en konto og når ([fanen Konto i Ansatte](/nb/user/staff/#konto)); denne
appen er der tilkoblingen settes opp og følges med på. Driftsprosedyren, med hva du gjør
i Microsoft-portalen, står i [administrasjonsveiledningen](/nb/admin/entra/); reglene
står i [referansen](/en/reference/entra/).

## Hvem ser hva

| Hva | Hvem |
| --- | --- |
| Flisen **Microsoft Entra ID**, siden, kortet **Status**, kontoene og tabellen **Grupper** — **Bekreft** på en konto eller en gruppe Vantigo ikke opprettet — og de siste hendelsene | `entra:manage` |
| Stegene **1 Registrer applikasjonen**, **2 Legg til en klienthemmelighet** og **3 Gi tillatelsene og rollen** — **Lagre**, **Legg til hemmeligheten**, **Trekk tilbake**, **Test tilkoblingen** | en **eier**, innlogget med en andre faktor; for alle andre med `entra:manage` vises de skrivebeskyttet, med *En eier gjør dette steget. Du ser hva som er satt, men kan ikke endre det.* |
| Steg **4 Sett kontoreglene** — **Lagre kontoreglene** | `entra:manage` |
| Stegene **5 Innlogging** og **6 Klargjøring** | en **eier**: de er skjemaene fra kortet **Single sign-on**; alle andre leser steget uten dem |
| **Søk i katalogen** i tilkoblingsdialogen og **Finn treff i katalogen…** under **Brukere** ([koble til eksisterende brukere](#koble-til-eksisterende-brukere)) | en **eier**, innlogget med en andre faktor når eiere må bruke en |

`entra:manage` hører til kategorien Administrasjon og kan ikke delegeres: bare en eier
gir den til en rolle. Eieren har den allerede. En eier som logget inn uten en andre
faktor mens eiere må bruke en, ser skjemaene, men lagringen avvises.

## Siden

Åpne **Microsoft Entra ID** fra appvelgeren. Siden er en veiviser med seks steg, ett
kort hvert, i den rekkefølgen du gjør dem. Hvert kort sier hvor du går i Microsoft
Entra admin center, hva du skal kopiere, og får et merke: *Ferdig* når Vantigo har det
steget trenger, *Ikke gjort* til da, og *Valgfritt* på de to siste stegene — merket
kommer fra det Vantigo har lagret, så det stemmer uansett hvordan steget ble gjort. Ha
portalen åpen i en annen fane og arbeid deg nedover siden.

### Kortet Status

Når tilkoblingen er testet, står et kort **Status** over stegene; før det, og igjen
etter at steg 1 endres, vises det ikke:

- **Tilkoblet** eller **Feiler**, og, når det finnes en, årsaken i en setning: en
  hemmelighet avvist, leietakeren svarer ikke, samtykke ikke gitt, hemmelighetene
  utløpt, en hemmelighet som utløper innen 30 dager, eller rollen User Administrator
  som ser ut til å mangle;
- **Organisasjon**, slik leietakeren oppgir den; **Sist testet**, når tilkoblingen sist
  ble testet; **Kontoer**, hvor mange Vantigo holder i leietakeren — aktiverte,
  deaktiverte og slettede; og **Grupper**, *… holdt*, gruppene som finnes der;
- **Tillatelser**: *Alle tillatelsene modulen trenger, er gitt.*, eller hver som
  fortsatt mangler, ved navn, og de som er gitt. Har applikasjonen tillatelser Vantigo
  ikke bruker, sier et oransje varsel *Applikasjonen har tillatelser Vantigo ikke
  bruker:* med listen, og at de kan være igjen fra et tidligere oppsett eller bredere
  enn nødvendig — fjern dem under **API permissions** med mindre et annet produkt bruker
  registreringen. En applikasjonstillatelse `Calendars.ReadWrite` gitt for
  kalendersynkronisering i en tidligere versjon står også her: kalendersynkroniseringen
  kjører nå på hver persons egen tilkobling. E-posttillatelsene (`Mail.Read`,
  `Mail.Send` og lignende) står her mens **E-postkanaler** er av; mens den er på, vises de
  under den bryteren i stedet ([nedenfor](#e-postkanaler)). Det er bare et varsel:
  tilkoblingen forblir **Tilkoblet**;
- **Klienthemmeligheter**: hver med **Beskrivelse**, **Lagt til**, **Utløper**, **Sist
  brukt** (når den sist hentet et token) og **Tilstand** — **I bruk** for den som
  prøves først, *Trukket tilbake* og datoen, og *Avvist av leietakeren* og datoen for
  en leietakeren har avvist — og **Trekk tilbake** (en eier);
- **Modulens kontoer**, når det finnes noen: hvert **Brukerhovednavn**, **Tilstand** og
  hva som ble **Rapportert** for det. En konto Vantigo fant i leietakeren i stedet for å
  opprette den, står som *Overtatt – bekreft* til noen bekrefter den
  ([under](#bekreft-en-konto-vantigo-ikke-opprettet));
- **Siste hendelser**: hva Vantigo gjorde i leietakeren eller med tilkoblingen, når,
  brukerhovednavnet, *av Ansattes kontokjøring* når Ansattes kontojobb gjorde det, og
  hva som ble meldt. Aldri en hemmelighet, et passord eller et pass. En gruppes
  hendelse vises også med ord — *En gruppe ble opprettet*, *En gruppe ble funnet og
  overtatt*, *En gruppes medlemmer ble avstemt*, *En gruppe ble tømt og beholdt*,
  *Overtakelsen av en gruppe ble bekreftet* og lignende — med det som ble meldt i ord,
  for eksempel *3 medlemmer lagt til* eller *teamet venter til gruppen er femten
  minutter gammel og Teams har sett den*.

### Tabellen Grupper

Under kortet **Status**, og vist sammen med det, viser **Grupper** gruppene Vantigo
holder i leietakeren for Ansattes grupper ([Grupper i Ansatte](/nb/user/staff/#grupper)),
slettede og beholdte medregnet og nedtonet: hver gruppes **Navn** slik Vantigo sist
skrev det, med e-postaliaset under; **Type**, *Sikkerhet* eller *Microsoft 365*;
**Team**, *Ja* når den er blitt et team i Microsoft Teams, ellers *Nei*; **Tilstand** —
*Finnes*, *Slettet*, eller *Tømt og beholdt* for en sikkerhetsgruppe Ansatte ga slipp
på, som Vantigo aldri sletter — med *Funnet i leietakeren og overtatt* under for en
gruppe Vantigo fant med aliaset i stedet for å lage den, eller *Overtatt – bekreft* og
**Bekreft** så lenge ingen har bekreftet den
([under](#bekreft-en-gruppe-vantigo-ikke-opprettet)); og **Siste
medlemssynkronisering**, når Vantigo sist brakte medlemmene i samsvar, eller *Aldri*.
Før det finnes noen, står det *Ingen grupper ennå. En gruppe i Ansatte som er slått på
for Microsoft Entra ID, vises her når kontoarbeideren har laget den.* Bortsett fra
**Bekreft** er den skrivebeskyttet: HR oppretter, endrer og arkiverer grupper i
Ansatte, og Ansattes kontoarbeider bringer dem hit.

### 1 Registrer applikasjonen

Registrer en applikasjon for Vantigo i portalen (**App registrations** › **New
registration**, *Accounts in this organizational directory only*), kopier så
**Directory (tenant) ID** og **Application (client) ID** inn i de to feltene og velg
**Lagre**. Begge er GUID-er, slik registreringens **Overview** viser dem.

*Ferdig* når begge er lagret. Lagrer du en annen leietaker eller klient senere, går
tilkoblingen tilbake til ikke tilkoblet til den er testet igjen. Mens Vantigo holder
kontoer i leietakeren, spør den først — **Bytte leietaker eller applikasjon?** — og
**Bytt** lagrer, siden kontoene ble laget i den gamle: Vantigo forvalter dem ikke
lenger, og deaktiverer eller sletter dem ikke lenger når personene slutter. Et bytte av
leietaker forkaster også alle midlertidige tilgangspass som ikke er vist ennå.
Ingenting slettes i noen av leietakerne.

### 2 Legg til en klienthemmelighet

Lag en klienthemmelighet i portalen (**Certificates & secrets** › **New client
secret**), og kopier **Value** med en gang. Fyll så ut **Legg til en hemmelighet**:
**Beskrivelse** som skiller den fra andre (valgfri), **Utløper**, dagen portalen viste —
fram i tid, høyst 24 måneder fram; Vantigo tar starten av den dagen der du er — og
**Verdi**, og velg **Legg til hemmeligheten**. Verdien er et passordfelt og **vises
aldri igjen**, her eller noe annet sted: Vantigo forsegler den når den kommer.

Listen over skjemaet viser hver hemmelighet med beskrivelse og datoer. Den nyeste som
ikke har utløpt eller er trukket tilbake, prøves først og er merket **I bruk**; en som
leietakeren har avvist, prøves etter de andre. For å rotere, legg til den nye
hemmeligheten først, og velg så **Trekk tilbake** på den gamle og bekreft; Vantigo
nekter å trekke tilbake den eneste hemmeligheten som virker mens den holder kontoer,
fordi ingenting da kunne deaktivere en som slutter. Å trekke den tilbake stopper bare
Vantigo fra å bruke den: slett den i portalen selv.

*Ferdig* når en hemmelighet er i bruk.

### 3 Gi tillatelsene og rollen

Kortet lister de åtte **applikasjonstillatelsene** for Microsoft Graph som skal legges
til under **API permissions** — hver klar til å kopieres, og så **Grant admin consent**.
Det sier også steget ingen andre produkter ber om: tildel rollen **User Administrator**
til applikasjonen under **Roles & admins** — uten den nekter Microsoft å aktivere,
deaktivere eller slette en konto og å utstede et midlertidig tilgangspass, selv med
alle tillatelsene.

Velg **Test tilkoblingen**. Vantigo henter et token med hemmeligheten, leser
organisasjonen og de verifiserte domenene, og sammenligner tokenets tillatelser med de
åtte. Kortet sier så *Tilkoblet* organisasjonen og viser de verifiserte domenene, eller
*Tilkoblingen feilet* og hvorfor; det navngir hver tillatelse som fortsatt mangler, og
merker hver i listen gitt eller mangler. Rollen kan ikke sjekkes herfra — sjekk den i
portalen; mangler den, avvises den første kontoen Vantigo prøver å aktivere, og den
melder det, og etter tre slike avvisninger på rad sier kortet **Status** at rollen mest
sannsynlig mangler.

*Ferdig* når siste test var tilkoblet uten noen manglende tillatelse.

### 4 Sett kontoreglene

Reglene hver konto følger, som alle med `entra:manage` kan endre:

- **Domene for brukerhovednavn**, valgt blant de verifiserte domenene testen leste
  (skrevet inn før første test) — en person med e-post på jobb på et av dem, beholder
  den som brukerhovednavn;
- **Navnemønster** for alle andre: `given.family`, `g.family` eller `given`, med norske
  bokstaver gjort om (`Ørjan Åsmundsen` blir `orjan.asmundsen`) og et tall lagt til når
  navnet er tatt;
- **Standard bruksland**, landet som skrives når Ansatte ikke har noe for personen —
  Microsoft gir ingen lisens uten;
- **Når noen slutter** — kontoen deaktiveres alltid først — **Logg dem ut overalt**,
  **Skriv sluttdatoen**, **Fjern direkte tildelte lisenser**, og **Fjern fra grupper**:
  alle grupper de kan fjernes fra, bare gruppene Vantigo forvalter, eller ingen grupper;
- **Når noen begynner**: **Skriv ansettelsesdatoen**, **Levetid for midlertidig
  tilgangspass (minutter)** (10 til 43 200), om **Passet kan bare brukes én gang**, og
  **Hvem som overleverer passet**. Kortet sier, med stien, at leietakerens policy for
  autentiseringsmetoder må slå på passet for brukerne, ellers avviser Microsoft det;
- **Outlook-tilkobling**: **Outlook-tilkobling: la folk koble Outlook til Vantigo**
  ([nedenfor](#outlook-tilkobling));
- **E-postkanaler**: **E-postkanaler: la Kommunikasjon sende og motta gjennom delte
  postbokser i Exchange Online** ([nedenfor](#e-postkanaler)).

Velg **Lagre kontoreglene**. En verdi utenfor regelen merkes på feltet med årsaken. Har
noen andre lagret tilkoblingen siden du åpnet siden, sier siden fra og laster den inn på
nytt: se hva som står der nå, og gjør endringen din på nytt.

Når tilkoblingen er tilkoblet, viser kortet også **Leietakerens lisenser**, hver med
setene som er tildelt og kjøpt — det en Ansatte-gruppes lisenser kan peke på. Når
listen leses her, oppdateres også valgene en Ansatte-gruppes **Lisenser** gir, slik
hver tilkoblingstest gjør.

*Ferdig* når et domene er lagret.

### 5 Innlogging

Valgfritt: lar folk logge inn i Vantigo med Microsoft-kontoen sin. Kortet er
leverandørskjemaet fra kortet **Single sign-on** — for en eier, når steg 1 er lagret
(*Registrer applikasjonen i steg 1 først.* til da) — fylt ut med leietakerens adresse
og klient-ID-en fra steg 1 når det ennå er tomt, med **Tilbakekalls-URL** som skal
limes inn i registreringens **Authentication** › plattformen **Web**. Fyll det ut og
velg **Lagre**. Uten dette lager Ansatte fortsatt Microsoft-kontoene, men ingen logger
inn i Vantigo med dem.

### 6 Klargjøring

Valgfritt: får Entra til å opprette hver persons Vantigo-bruker så snart kontoen er
aktivert. Kortet er, for en eier, SCIM-delen fra kortet **Single sign-on**: **Nytt token**,
**Endepunkt-URL**, og hvor begge limes inn i portalen (**Enterprise applications** ›
appen › **Provisioning**). Uten dette dukker personens Vantigo-bruker opp ved første
innlogging i stedet.

Kortet tilbyr også **Åpne innstillingene til Ansatte**, der **Standard kontotype** kan settes til
**Microsoft Entra ID** for personene som registreres fra nå av
([Kontoer](/nb/user/staff/#kontoer)). Personer som allerede er registrert, beholder
kontotypen sin til HR endrer den på fanen Konto.

## Outlook-tilkobling

**Outlook-tilkobling** lar hver person koble sin egen Outlook til Vantigo under
**Innstillinger → Tilkoblinger** ved å logge på Microsoft én gang
([Tilkoblinger](/nb/user/connections/)). Den er av til noen med `entra:manage` slår den
på. Den samme bryteren lar folk [synkronisere kalenderen sin til
Outlook](/nb/user/calendar/#synkroniser-med-outlook), hver på sin egen tilkobling:
applikasjonen får ikke noe mer for det, og bare de som kobler til, blir synkronisert.

1. En eier gjør appregistreringen klar først — **Omdirigerings-URI** som vises under
   bryteren, lagt til **Web**-plattformen, og de delegerte tillatelsene `openid`,
   `profile`, `offline_access` og `Calendars.ReadWrite` med administratorsamtykke.
   [Administrasjonsveiledningen](/nb/admin/entra/#outlook-tilkobling) har stegene.
2. I steg **4 Sett kontoreglene**, under **Outlook-tilkobling**, slår du på
   **Outlook-tilkobling: la folk koble Outlook til Vantigo**, og velger **Lagre
   kontoreglene**.
3. Koble til din egen Outlook under **Innstillinger → Tilkoblinger** for å sjekke at det
   virker.

Under bryteren viser kortet **Omdirigerings-URI** som skal legges til registreringen,
med en kopieringsknapp, og en linje sier hvor tilkoblingen står:

- **Klar** — noen har koblet til siden bryteren ble slått på;
- **Ingen har koblet til ennå: koble til din egen Outlook under Innstillinger ›
  Tilkoblinger for å sjekke at det virker.**;
- **Den siste tilkoblingen manglet en delegert tillatelse: gi administratorsamtykke for
  den i appregistreringen og koble til på nytt.** — en persons tilkobling kom tilbake
  uten tillatelsen den ba om;
- **Venter på tilkoblingen** — tilkoblingen er ikke tilkoblet, med årsaken under.

Under den teller *Personer tilkoblet: … Venter på ny tilkobling: …* dem som har en
tilkobling som virker, og dem Microsoft har avvist tilkoblingen til, som ser **Koble til
Outlook på nytt** på sin egen side.

Under det, mens bryteren er på, sier **Endringsvarsler** om Microsoft gir Vantigo beskjed
om endringer gjort i Outlook, slik at de tas på omtrent ett minutt i stedet for ved
neste synkronisering, hvert 15. minutt:

- **På, overvåker 3 postbokser. Siste varsel: …** — det virker; tidspunktet er siste
  varsel som kom, eller *ingen ennå*;
- **Slått av av driftsansvarlig …** eller **Krever PUBLIC_BASE_URL …** — driftsansvarlig
  har slått det av, eller installasjonen har ingen offentlig adresse; endringer i Outlook
  kommer ved neste synkronisering;
- **Microsoft har ikke nådd adressen siden …** — Vantigo prøver igjen innen en time;
  endringene kommer ved neste synkronisering i mellomtiden. [Driftsveiledningen](/nb/admin/entra/#endringsvarsler)
  sier hva som bør sjekkes.

Her er det ingenting å velge: linjene melder bare.

**Slår du den av**, lukkes hver persons tilkobling når du lagrer — er noen tilkoblet
eller venter på ny tilkobling, spør siden først *Slå av Outlook-tilkoblingen?*: *Alle må
koble til Outlook på nytt.*, og **Slå av og lagre** lagrer. Det stopper også
kalendersynkroniseringen for alle og fjerner kopiene Vantigo la inn i Outlook deres, hver
med sin egen tilkobling som lukkes. Slår du den på igjen, fortsetter den ikke for noen av
seg selv: hver person som hadde synkroniseringen på, kobler til Outlook på nytt, og
synkroniseringen deres fortsetter når de har gjort det.

## E-postkanaler

**E-postkanaler** lar Kommunikasjon sende fra selskapets **delte postbokser** i Exchange
Online — hver av dem en kanal der ([Kommunikasjon](/nb/user/communications/#sett-opp-en-kanal)).
Vantigo når dem med applikasjonens egen tilgang, som en Exchange-administrator gir i
Exchange Online, avgrenset til kanalenes postbokser. Det er av til noen med
`entra:manage` slår det på. I denne versjonen **sender en kanal bare**: ordene *sende og
motta* på bryteren ser fram mot mottak, som ikke er bygget ennå.

1. I steg **4 Sett kontoreglene**, under **E-postkanaler**, slå på **E-postkanaler: la
   Kommunikasjon sende og motta gjennom delte postbokser i Exchange Online**, og velg
   **Lagre kontoreglene**.
2. Under bryteren viser **Kommandoer for Exchange Online PowerShell** de seks kommandoene
   som skal kjøres, med applikasjonens applikasjons-ID og objekt-ID fylt inn og en
   kopieringsknapp. Før tilkoblingen er testet, sier *Test tilkoblingen først* fra, og
   kommandoene viser `<applikasjons-ID>` og `<objekt-ID>` i stedet. En
   Exchange-administrator kjører dem og bytter ut `post@firma.no` med adressen til hver
   delte postboks — [administrasjonsveiledningen](/nb/admin/entra/#mail-channels) har
   framgangsmåten.
3. Legg til postboksen som kanal i Kommunikasjon, under **Kanaler**.

Mens bryteren er på, sier en linje under den hvor e-postkanalene står:

- **Klar: applikasjonen har ingen e-posttillatelse for hele leietakeren.** — når du
  legger til en kanal i Kommunikasjon, sjekkes det at Vantigo når postboksen;
- **Applikasjonen har tilgang til alle postboksene i leietakeren. Begrens den med RBAC
  for Applications, og fjern samtykket som gjelder hele leietakeren.** — i oransje:
  appregistreringen har en `Mail`-tillatelse, som når hver postboks uansett hva Exchange
  begrenser. Kanalene virker fortsatt; fjern tillatelsen under **API permissions** og test
  tilkoblingen igjen;
- **Venter på tilkoblingen** — tilkoblingen er ikke tilkoblet, med årsaken under.

**Å slå den av** sletter ingenting: Kommunikasjon tilbyr ikke lenger Microsoft 365 for en
kanal, og e-post som venter på en Microsoft 365-kanal, prøves igjen i noen minutter og
gis så opp.

## Bekreft en konto Vantigo ikke opprettet

Når leietakeren allerede har en bruker med ansattnummeret til en person Ansatte
registrerer, kobler Vantigo den, men skriver ingenting til den — ingen opplysninger,
ingen aktivering, ikke noe pass, ingen deaktivering, ingen sletting — før noen med
`entra:manage` bekrefter at den er personens. Fanen Konto i Ansatte og dashbordet sier
fra, og dashbordet lenker hit.

1. Finn kontoen som står som *Overtatt – bekreft* blant kontoene på kortet **Status**.
2. Sjekk i portalen at den er personens.
3. Velg **Bekreft** ved siden av den, og så **Bekreft** i *Bekrefte overtakelsen av
   …?*.

Siden sier *Overtakelsen er bekreftet*, og Vantigo forvalter kontoen fra Ansattes neste
kontokjøring. Bekreft bare en konto du har sjekket: å bekrefte gir den til den som har
ansattnummeret i Ansatte.

## Bekreft en gruppe Vantigo ikke opprettet

Når leietakeren allerede har en gruppe med e-postaliaset til en Ansatte-gruppe som er
slått på for Microsoft Entra ID, kobler Vantigo den, men skriver ingenting til den —
ikke noe navn, ingen eier, ingen medlemmer, ingen sletting — før noen med
`entra:manage` bekrefter at det er den gruppen. Ansatte-gruppens side sier *Funnet i
Microsoft Entra ID og venter på bekreftelse der* så lenge.

1. Finn gruppen som står som *Overtatt – bekreft* i tabellen **Grupper**.
2. Sjekk i portalen at det er gruppen som er ment — navnet, eierne, medlemmene.
3. Velg **Bekreft** ved siden av den, og så **Bekreft** i *Bekrefte overtakelsen av
   …?*, som sier hva som følger: *Bekrefter du, forvalter Vantigo den for Ansatte. Den
   første skrivingen gjør gruppens eiere til nøyaktig eierne fra Ansatte og
   applikasjonen – eierne den har nå, blir fjernet – og skriver navnet, beskrivelsen og
   synligheten fra Ansatte til den; så gjør Vantigo medlemmene til gruppens i Ansatte og
   fjerner alle andre. Vantigo sletter aldri en gruppe den ikke har opprettet.*

Siden sier *Overtakelsen er bekreftet* — *Vantigo forvalter gruppen fra Ansattes neste
kontokjøring.* Bekreft bare en gruppe du har sjekket.

## Koble til eksisterende brukere

Når enkel pålogging slås på i et arbeidsområde som allerede har brukere, må hver av dem
kobles til katalogkontoen sin før de kan logge inn gjennom Microsoft
([Koble en konto til enkel pålogging](/nb/user/workspace-administration/#koble-en-konto-til-enkel-pålogging)).
Mens denne appen er slått på, trenger du ikke skrive inn kontoene: Vantigo slår dem opp i
leietakeren. Begge måtene nedenfor er for en **eier**, under **Administrer arbeidsområde** ›
**Brukere**, innlogget med en andre faktor når eiere må bruke en; de krever
tilkoblingen til leietakeren fra steg 1 til 3, og kolonnen **Enkel pålogging**, som
finnes mens enkel pålogging er på.

### Gå gjennom de nøyaktige treffene

1. Velg **Finn treff i katalogen…** ved siden av søket under **Brukere**.
2. **Brukere som passer med en konto i katalogen** åpnes. En linje øverst teller
   *Brukere som ikke er koblet til*, *Nøyaktige treff* og dem som er *Utelatt fordi mer
   enn én konto passet*. Under den er hvert forslag en rad: **Bruker**, med adressen;
   **Konto i katalogen**, med visningsnavn, brukerprinsipalnavn, ansatt-ID når den har
   en, og *Deaktivert* når kontoen er det; og **Treff på**: **E-post**,
   **Brukerprinsipalnavn**, **Ansatt-ID**, eller flere.
3. Se over radene. En rad er krysset av fra start bare når kontoen er aktiv,
   brukerprinsipalnavnet traff, og brukeren ikke er eier. Alle andre rader står uten
   kryss og er merket **Sjekk treffet**, med grunnen: *Treff bare på e-post eller
   ansatt-ID, ikke på brukerprinsipalnavnet personen logger på med. Forsikre deg om at
   det er samme person.*, eller *Denne brukeren er eier. Den som har denne kontoen,
   logger på med eierens tilgang.* Kryss av dem du har sjekket, og fjern krysset på dem
   du ikke vil ha.
4. Velg **Koble til valgte**, som viser hvor mange som er krysset av. Vantigo kobler
   parene til ett om gangen, og **Resultat** på hver rad sier *Koblet til*, eller med
   ord hvorfor det ble avvist — for eksempel at en annen bruker allerede er koblet til
   kontoen. **Brukere** oppdateres når det er ferdig.

En bruker foreslås bare når treffet er **nøyaktig**: adressen deres er kontoens e-post
eller brukerprinsipalnavn, uten hensyn til store og små bokstaver, eller
ansattnummeret deres er kontoens ansatt-ID — og alt som traff, peker på den samme
kontoen. Det gjettes ikke på navn eller lignende adresser. Utelatt blir:

- en bruker som passer med mer enn én konto, eller der kontoen også passer med en annen
  bruker — telt som *Utelatt fordi mer enn én konto passet*; koble dem til én om
  gangen;
- gjestekontoer, invitert fra en annen organisasjon: de foreslås aldri og telles ikke;
- en konto noen andre allerede har: en kunngjort for en annen bruker, en leverandørens
  klargjøring har gitt en annen bruker, eller en Ansatte har kunngjort for en ny person.
  En konto leverandørens klargjøring allerede har gitt den samme brukeren, foreslås
  likevel;
- brukere som allerede er koblet til eller venter på første pålogging, brukere som er
  **Venter**, og deg selv — du kobler deg selv til ved å logge inn gjennom Microsoft.

Når ingen passer, sier vinduet det, og at du kobler dem til én om gangen fra kolonnen
**Enkel pålogging**. Bare de 1 000 første brukerne som ikke er koblet til, sammenlignes;
er det flere, sier det fra — koble til disse, og se så etter igjen. Å finne treffene kan
ta litt tid i en stor leietaker; åpner du vinduet igjen i mellomtiden, venter det på det
samme svaret, og etter to minutter gir det opp og sier at leietakeren ikke svarte.

En rad som er koblet til, viser **Venter på første pålogging** under **Brukere** til
personen neste gang logger inn gjennom Microsoft, eller leverandøren sender kontoen;
da fullføres koblingen, og raden viser **Koblet til**. Navn, adresse og roller blir som
de var.

### Velg kontoen når du kobler til én bruker

**Koble til…** i en brukers rad åpner **Koble _navn_ til enkel pålogging**, som, mens
denne appen er slått på, begynner med **Søk i katalogen**. Skriv to tegn eller mer av
personens navn, adresse eller brukerprinsipalnavn; etter en pause viser listen
kontoene som passer, etter visningsnavn, hver med brukerprinsipalnavn, ansatt-ID,
*Gjest* for en gjest i leietakeren, og *Deaktivert* når kontoen er det. En konto noen allerede har, viser *Venter på* og
brukerens navn (eller *Venter på en annen bruker*), *Klargjort for en annen bruker*
eller *Kunngjort av Ansatte*, og kan ikke velges. Høyst 25 kontoer vises; når det er så
mange, ber den deg snevre inn søket.

Å velge en konto fyller ut **Brukerprinsipalnavn** og, under **Mer**, **Objekt-ID**.
De to hører sammen: endrer du brukerprinsipalnavnet, tømmes objekt-ID-en; søker du på
nytt etter et valg, tømmes begge. Kontroller dem og velg **Koble til**, som før. Feltene du skriver i, blir stående: du
kan fortsatt skrive navnet selv, og de er alt som finnes mens katalogen ikke kan leses.

Et søk eller en gjennomgang kan avvises når leietakeren ikke kan spørres:
hemmelighetene avvises, leietakeren svarer ikke, eller den ber Vantigo vente — prøv igjen
om et minutt. Gjennomgangen avvises når enkel pålogging ikke er satt opp.

## Etter oppsettet

Ingenting mer skjer her fra dag til dag: kontoarbeideren i Ansatte gjør jobben, og hver
persons konto vises på fanen **Konto** i Ansatte, der HR viser det midlertidige
tilgangspasset én gang. Kom tilbake til siden når Ansattes dashbord sier at en
hemmelighet snart utløper eller at tilkoblingen feiler, og se på kortet **Status** og
de siste hendelsene.
