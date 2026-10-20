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
| Flisen **Microsoft Entra ID**, siden, kortet **Status** og de siste hendelsene | `entra:manage` |
| Stegene **1 Registrer**, **2 En hemmelighet** og **3 Tillatelser og rollen** — lagre leietakeren, legge til og tilbakekalle hemmeligheter, **Test tilkoblingen** | en **eier**, innlogget med en andre faktor; for alle andre med `entra:manage` vises de skrivebeskyttet |
| Steg **4 Kontoer** — kontoreglene | `entra:manage` |
| Stegene **5 Innlogging** og **6 Provisjonering** | en **eier**: de er skjemaene fra kortet **Single sign-on** |

`entra:manage` hører til kategorien Administrasjon og kan ikke delegeres: bare en eier
gir den til en rolle. Eieren har den allerede.

## Siden

Åpne **Microsoft Entra ID** fra appvelgeren. Siden er en veiviser med seks steg, ett
kort hvert, i den rekkefølgen du gjør dem. Hvert kort sier hvor du går i Microsoft
Entra admin center, hva du skal kopiere, og får et *Ferdig*-merke når Vantigo har det
steget trenger — merket kommer fra det Vantigo har lagret, så det stemmer uansett
hvordan steget ble gjort. Ha portalen åpen i en annen fane og arbeid deg nedover siden.

### Kortet Status

Når tilkoblingen er testet, står et kort **Status** over stegene:

- om tilkoblingen virker — tilkoblet, feiler eller ikke tilkoblet ennå — og, når det
  finnes en, årsaken i en setning: en hemmelighet avvist, leietakeren svarer ikke,
  samtykke ikke gitt, hemmelighetene utløpt, en hemmelighet som utløper innen 30
  dager, eller rollen User Administrator som ser ut til å mangle;
- organisasjonens navn, slik leietakeren oppgir det;
- tillatelsene tilkoblingen har, og hver som fortsatt mangler, ved navn;
- hemmelighetene, med etiketten, når hver utløper og sist ble brukt, hvilken som er
  **I bruk**, og **Tilbakekall** (en eier);
- når tilkoblingen sist ble testet;
- hvor mange kontoer Vantigo holder i leietakeren — aktiverte, deaktiverte og slettede;
- de siste hendelsene: hva Vantigo gjorde i leietakeren eller med tilkoblingen, når,
  av hvem (en person, eller kontoarbeideren i Ansatte), med brukerhovednavnet og hva
  som ble meldt. Aldri en hemmelighet, et passord eller et pass.

### 1 Registrer

Registrer en applikasjon for Vantigo i portalen (**App registrations** › **New
registration**, *Accounts in this organizational directory only*), kopier så
**Directory (tenant) ID** og **Application (client) ID** inn i de to feltene og velg
**Lagre**. Begge er GUID-er, slik registreringens **Overview** viser dem.

*Ferdig* når begge er lagret. Lagrer du en annen leietaker eller klient senere, går
tilkoblingen tilbake til ikke tilkoblet til den er testet igjen; mens Vantigo holder
kontoer i leietakeren, ber den deg bekrefte først, siden kontoene ble laget i den
gamle. Ingenting slettes i noen av leietakerne.

### 2 En hemmelighet

Lag en klienthemmelighet i portalen (**Certificates & secrets** › **New client
secret**), kopier **Value** med en gang, og velg så **Legg til en hemmelighet**: en
etikett som skiller den fra andre, verdien, og utløpsdatoen portalen viste — fram i
tid, høyst 24 måneder fram. Velg **Lagre**. Verdien er et passordfelt og **vises aldri
igjen**, her eller noe annet sted: Vantigo forsegler den når den kommer.

Listen under viser hver hemmelighet med etikett og datoer. Den nyeste som ikke har
utløpt eller er tilbakekalt, er den som er i bruk. For å rotere, legg til den nye
hemmeligheten først, og velg så **Tilbakekall** på den gamle; Vantigo nekter å
tilbakekalle den eneste hemmeligheten som virker mens den holder kontoer, fordi ingenting
da kunne deaktivere en som slutter. Å tilbakekalle stopper bare Vantigo fra å bruke
den: slett den i portalen selv.

*Ferdig* når en hemmelighet er i bruk.

### 3 Tillatelser og rollen

Kortet lister de åtte **applikasjonstillatelsene** for Microsoft Graph som skal legges
til under **API permissions**, hver klar til å kopieres, og så **Grant admin consent**.
Det sier også steget ingen andre produkter ber om: tildel rollen **User Administrator**
til applikasjonen under **Roles & admins** — uten den nekter Microsoft å aktivere eller
deaktivere en konto, selv med alle tillatelsene.

Velg **Test tilkoblingen**. Vantigo henter et token med hemmeligheten, leser
organisasjonen og de verifiserte domenene, og sammenligner tokenets tillatelser med de
åtte. Kortet sier så om tilkoblingen virker, navngir organisasjonen og hver tillatelse
som fortsatt mangler; når den feiler, sier det hvorfor. Rollen kan ikke sjekkes herfra —
sjekk den i portalen; mangler den, avvises den første kontoen Vantigo prøver å aktivere,
og kortet **Status** sier fra.

*Ferdig* når siste test var tilkoblet uten noen manglende tillatelse.

### 4 Kontoer

Reglene hver konto følger, som alle med `entra:manage` kan endre:

- **domenet** kontoenes brukerhovednavn slutter på, valgt blant de verifiserte
  domenene testen leste — en person med e-post på jobb på et av dem, beholder den som
  brukerhovednavn;
- **mønsteret** for alle andre: `given.family`, `g.family` eller `given`, med norske
  bokstaver gjort om (`Ørjan Åsmundsen` blir `orjan.asmundsen`) og et tall lagt til når
  navnet er tatt;
- **bruksstedet**, landet som skrives når Ansatte ikke har noe for personen — Microsoft
  gir ingen lisens uten;
- om **ansettelsesdatoen** skal skrives, og **stegene for en som slutter**: tilbakekall
  øktene, skriv sluttdatoen, fjern de direkte tildelte lisensene, og hvilke grupper en
  som slutter forlater — alle, Vantigos egne eller ingen;
- **det midlertidige tilgangspasset**: hvor lenge det virker (10 minutter til 30
  dager), om det bare virker for én innlogging, og hvem som gir det videre. Kortet sier,
  med stien, at leietakerens policy for autentiseringsmetoder må slå på passet for
  brukerne, ellers avviser Microsoft det.

Velg **Lagre**. En verdi utenfor regelen merkes på feltet med årsaken. Har noen andre
lagret reglene siden du åpnet siden, last inn på nytt og lagre igjen.

*Ferdig* når reglene er lagret med et domene.

### 5 Innlogging

Valgfritt: lar folk logge inn i Vantigo med Microsoft-kontoen sin. Kortet er
leverandørskjemaet fra kortet **Single sign-on**, allerede fylt ut med leietakerens
adresse og klient-ID-en fra steg 1, med **Tilbakekalls-URL** som skal limes inn i
registreringens **Authentication** › plattformen **Web**. Fyll det ut og velg
**Lagre**. Uten dette lager Ansatte fortsatt Microsoft-kontoene, og ingen logger inn i
Vantigo med dem.

### 6 Provisjonering

Valgfritt: får Entra til å opprette hver persons Vantigo-bruker så snart kontoen er
aktivert. Kortet er SCIM-delen fra kortet **Single sign-on**: **Nytt token**,
**Endepunkt-URL**, og hvor begge limes inn i portalen (**Enterprise applications** ›
appen › **Provisioning**). Uten dette dukker personens Vantigo-bruker opp ved første
innlogging i stedet.

Kortet lenker også til Ansatteinnstillinger, der **Standard kontotype** kan settes til
**Microsoft Entra ID** for personene som registreres fra nå av
([Kontoer](/nb/user/staff/#kontoer)). Personer som allerede er registrert, beholder
kontotypen sin til HR endrer den på fanen Konto.

## Etter oppsettet

Ingenting mer skjer her fra dag til dag: kontoarbeideren i Ansatte gjør jobben, og hver
persons konto vises på fanen **Konto** i Ansatte, der HR viser det midlertidige
tilgangspasset én gang. Kom tilbake til siden når Ansattes dashbord sier at en
hemmelighet snart utløper eller at tilkoblingen feiler, og se på kortet **Status** og
de siste hendelsene.
