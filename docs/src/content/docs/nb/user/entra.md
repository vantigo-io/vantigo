---
title: Microsoft Entra ID
description: Å koble selskapets Microsoft Entra ID-leietaker til på siden Microsoft Entra ID — kortet Status, veiviserens seks steg og hva Ferdig betyr på hvert, tillatelsene som skal kopieres, hemmeligheten som vises én gang, tilkoblingstesten, kontoreglene — og hvem som ser hva.
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
| Flisen **Microsoft Entra ID**, siden, kortet **Status**, kontoene — **Bekreft** på en Vantigo ikke opprettet — og de siste hendelsene | `entra:manage` |
| Stegene **1 Registrer applikasjonen**, **2 Legg til en klienthemmelighet** og **3 Gi tillatelsene og rollen** — **Lagre**, **Legg til hemmeligheten**, **Trekk tilbake**, **Test tilkoblingen** | en **eier**, innlogget med en andre faktor; for alle andre med `entra:manage` vises de skrivebeskyttet, med *En eier gjør dette steget. Du ser hva som er satt, men kan ikke endre det.* |
| Steg **4 Sett kontoreglene** — **Lagre kontoreglene** | `entra:manage` |
| Stegene **5 Innlogging** og **6 Klargjøring** | en **eier**: de er skjemaene fra kortet **Single sign-on**; alle andre leser steget uten dem |

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
  ble testet; og **Kontoer**, hvor mange Vantigo holder i leietakeren — aktiverte,
  deaktiverte og slettede;
- **Tillatelser**: *Alle tillatelsene modulen trenger, er gitt.*, eller hver som
  fortsatt mangler, ved navn, og de som er gitt;
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
  hva som ble meldt. Aldri en hemmelighet, et passord eller et pass.

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
til under **API permissions**, hver klar til å kopieres, og så **Grant admin consent**.
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
  autentiseringsmetoder må slå på passet for brukerne, ellers avviser Microsoft det.

Velg **Lagre kontoreglene**. En verdi utenfor regelen merkes på feltet med årsaken. Har
noen andre lagret tilkoblingen siden du åpnet siden, sier siden fra og laster den inn på
nytt: se hva som står der nå, og gjør endringen din på nytt.

Når tilkoblingen er tilkoblet, viser kortet også **Leietakerens lisenser**, hver med
setene som er tildelt og kjøpt — det en Ansatte-gruppes lisenser kan peke på.

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

## Etter oppsettet

Ingenting mer skjer her fra dag til dag: kontoarbeideren i Ansatte gjør jobben, og hver
persons konto vises på fanen **Konto** i Ansatte, der HR viser det midlertidige
tilgangspasset én gang. Kom tilbake til siden når Ansattes dashbord sier at en
hemmelighet snart utløper eller at tilkoblingen feiler, og se på kortet **Status** og
de siste hendelsene.
