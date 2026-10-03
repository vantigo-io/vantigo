---
title: Energi
description: Målepunkter, målere, leveranseperioder og forbruk per kunde.
sidebar:
  order: 70
sources:
  - apps/energy/frontend
---

Energi-appen holder orden på målepunktene organisasjonen din leverer strøm til: hvor
hvert av dem er, hvilken måler som sitter på det, hvilken kunde det leveres til og hvor
lenge, og forbruket som er lest av fra det. Appen har ett område, **Målepunkter**, og
den legger til en **Energi**-fane på hver kunde i Kunder-appen.

Appen vises når energimodulen er slått på og du har `energy:metering-points-view` og
`energy:meters-view`. Hver del av et målepunkts side trenger i tillegg sin egen
rettighet; en del du mangler rettighet til vises rett og slett tom. Rettighetene er
listet nederst på denne siden.

## Finn et målepunkt

Åpne **Energi** → **Målepunkter**. Listen viser alle målepunkter med GSRN,
målernummer, adresse, prisområde og tilkoblingsstatus (**Ny**, **Tilkoblet** eller
**Frakoblet**), 25 per side, med totalen i overskriften.

Skriv i søkefeltet (**Søk etter GSRN, målernummer eller adresse…**) for å avgrense
listen. Søket treffer GSRN, nummeret på den nåværende måleren, gateadressen og byen.
Søkefeltet i toppfeltet finner også målepunkter, under overskriften **Målepunkter**,
så lenge du har `energy:metering-points-view`.

Klikk på en rad for å åpne målepunktet. Blyanten ytterst på raden åpner
redigeringsdialogen direkte.

## Opprett et målepunkt

Klikk **Nytt målepunkt** i listen. Fyll inn:

- **GSRN**: nøyaktig 18 sifre. To målepunkter kan ikke ha samme GSRN.
- **Målernummer**: måleren som sitter på punktet i dag. Den registreres som punktets
  første måler, installert i det øyeblikket du lagrer.
- **Gateadresse**, **Postnummer**, **By**, **Landskode**: alle påkrevd; landskoden er
  to bokstaver og er `NO` som standard.
- **Prisområde**: NO1 til NO5.
- **Nettområde**: valgfritt.
- **Tilkoblingsstatus**: **Ny**, **Tilkoblet** eller **Frakoblet**; nye punkter
  starter som **Ny**.
- **Forventet årsforbruk (kWh)**, **Breddegrad**, **Lengdegrad**: valgfrie.
  Breddegraden må ligge mellom -90 og 90, lengdegraden mellom -180 og 180.

**Opprett målepunkt** lagrer det og bekrefter med *Målepunktet er opprettet*. Dialogen
markerer feltene som mangler eller har feil form; en GSRN som allerede finnes avvises
med *Kunne ikke lagre målepunktet*. Å opprette krever `energy:metering-points-manage`
og `energy:meters-manage`.

## Rediger et målepunkt

Åpne målepunktet og klikk **Rediger målepunkt**, eller klikk på blyanten på raden i
listen. Dialogen har de samme feltene som når du oppretter, bortsett fra
målernummeret: en måler endres ved å bytte den, se nedenfor. **Lagre endringer**
bekrefter med *Målepunktet er oppdatert*. Å endre GSRN til en et annet målepunkt
allerede har, avvises. Å redigere krever `energy:metering-points-manage`.

## Les et målepunkts side

Siden har GSRN og adressen som overskrift, og fire kort:

- **Detaljer om målepunkt**: målernummer, prisområde, nettområde, tilkoblingsstatus og
  forventet årsforbruk.
- **Målerhistorikk**: hver måler som har sittet på punktet, med tidspunkt for
  **Installert** og **Fjernet**. Den nåværende måleren har ikke noe fjernet-tidspunkt.
- **Leveranseperioder**: hvilken kunde punktet har levert til, fra når til når, med
  statusen **Aktiv**, **Avsluttet** eller **Kansellert**. En slutt som ikke er satt,
  vises som **Åpen slutt**.
- **Forbruk**: avlesningene for et datointervall, som diagram og tabell.

Tidspunkter på denne siden vises i norsk markedstid (Europe/Oslo).

## Bytt måler

Klikk **Bytt måler** under **Målerhistorikk**. Fyll inn **Nytt målernummer** og
**Installert**, som starter på nåværende tidspunkt. Måleren som sitter der, merkes som
fjernet på det tidspunktet, og den nye blir nåværende måler; målernummeret i
detaljkortet følger med. Installasjonstidspunktet må være senere enn den nåværende
målerens eget installasjonstidspunkt, ellers avviser dialogen det på feltet. Krever
`energy:meters-manage`.

## Start en leveranseperiode for en kunde

Under **Leveranseperioder** heter knappen **Tildel kunde** når punktet ikke har noen
aktiv periode, og **Bytt kunde** når det har en. Begge åpner den samme dialogen: velg
**Kunde** (skriv for å søke) og **Byttedato**.

- Uten aktiv periode starter en ny **Aktiv** periode på den datoen.
- Med en aktiv periode avsluttes den på byttedatoen, og en ny starter for den nye
  kunden på samme dato. Byttedatoen må være senere enn den aktive periodens start, og
  kunden må være en annen; dialogen viser grunnen på feltet.

Dialogen avviser når punktet allerede har en ikke-kansellert periode som dekker den
datoen, og ber deg avslutte den eksisterende perioden først. Perioden starter ved
midnatt UTC den valgte dagen. Krever `energy:supply-periods-manage`.

En leveranseperiode kan også startes fra kundens side, se
[Energi-fanen](#energi-fanen-på-kundesiden) nedenfor.

## Avslutt en leveranseperiode

Klikk **Avslutt periode** på den aktive raden under **Leveranseperioder**. Perioden
avsluttes på nåværende tidspunkt, uten noe videre spørsmål, og statusen blir
**Avsluttet**. Bare en aktiv periode har knappen; en kansellert kan ikke avsluttes.
Kunne ikke slutten lagres, får du beskjed med *Kunne ikke avslutte perioden*. Krever
`energy:supply-periods-manage`.

## Se på forbruk

Velg **Fra** og **Til** under **Forbruk** (den siste måneden er forhåndsvalgt) og en
oppløsning: **Time**, **Dag** eller **Måned**. Diagrammet summerer kWh per bøtte.

- **Time** lister hver avlesning i intervallet, gruppert per dag, med **Start**,
  **Slutt**, **Mengde (kWh)**, **Kvalitet** (**Målt**, **Estimert**, **Korrigert**
  eller **Manuell**) og **Kilde** (**Elhub** eller **Manuell**).
- **Dag** og **Måned** lister én rad per bøtte med summert **Mengde (kWh)**, antall
  **Intervaller** bak den, og **Målt** eller **Inneholder estimert** som kvalitet.

Et intervall uten avlesninger sier *Fant ingen avlesninger i det valgte
datointervallet*. Å se forbruk krever `energy:consumption-view`.

## Legg til en manuell avlesning

Klikk **Legg til manuell avlesning** under **Forbruk**. Fyll inn **Start**, **Slutt**
og **Mengde (kWh)**: slutten må være senere enn starten, og mengden null eller større.
Avlesningen lagres med kvaliteten **Manuell** og kilden **Manuell**. En avlesning med
nøyaktig samme start og slutt som en eksisterende erstatter den i listene og summene;
den gamle beholdes som historikk. En avvisning vises som *Kunne ikke legge til
avlesningen*. Krever `energy:consumption-manage`.

## Energi-fanen på kundesiden

Åpne en kunde under **Kunder** og velg fanen **Energi**. Den vises når energimodulen
er på og du har `energy:metering-points-view`; se [Kunder](/nb/user/customers/) for
selve kundesiden.

Øverst står fire tall: **Målepunkter** som leveres til denne kunden, deres summerte
**Forventet årsforbruk**, **Forbruk siste 12 måneder** på tvers av dem, og **Aktive
leveranseperioder**. Under står en tabell med én rad per målepunkt kunden har en
ikke-kansellert leveranseperiode på: **Målepunkt-ID**, **Installasjonsadresse**,
**Prisområde**, **Forventet årsforbruk**, **Leveranseperioder** (den siste perioden og
statusen dens; hold pekeren over for å se alle) og **Status**. Klikk på en rad for å
åpne målepunktet. En kunde uten noen får beskjeden *Ingen målepunkter* – *Knytt til et
målepunkt for å begynne å følge forbruket.*

**Knytt til målepunkt** åpner en dialog der du søker i **Målepunkt** etter GSRN eller
målernummer og velger en **Start**-dato; dette starter en **Aktiv** leveranseperiode
for kunden på det punktet. Det avvises med *Dette målepunktet har allerede en
overlappende leveranseperiode* når punktet allerede leveres til på den datoen, og med
*Kunne ikke knytte til målepunktet* ellers. Knappen tilbys ikke på en kunde som er
slått sammen med en annen eller anonymisert. Når to kunder slås sammen, flyttes
leveranseperiodene til den oppslukte kunden over til den som består.

## Rettigheter

| Rettighet | Lar deg |
| --- | --- |
| `energy:metering-points-view` | Se listen og målepunktets side, søke etter målepunkter, se Energi-fanen |
| `energy:metering-points-manage` | Opprette og redigere målepunkter |
| `energy:meters-view` | Se målerhistorikken (kreves sammen med den over for å se appen i det hele tatt) |
| `energy:meters-manage` | Bytte måler og registrere den første måleren ved opprettelse |
| `energy:supply-periods-view` | Se leveranseperioder og en kundes målepunkter |
| `energy:supply-periods-manage` | Tildele, bytte, avslutte og knytte til leveranseperioder |
| `energy:consumption-view` | Se forbruk og Energi-fanens forbrukstall |
| `energy:consumption-manage` | Legge til manuelle avlesninger |
