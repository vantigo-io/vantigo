---
title: Energi
description: Målepunkter, målere, leveranseperioder, forbruk per kunde, spotpriser og tariffer.
sidebar:
  order: 70
sources:
  - apps/energy/frontend
  - apps/host/frontend/src/routes/energy
---

Energi-appen holder orden på målepunktene organisasjonen din leverer strøm til: hvor
hvert av dem er, hvilken måler som sitter på det, hvilken kunde det leveres til og hvor
lenge, og forbruket som er lest av fra det, spotprisene forbruket prises etter, og
kraft- og nettariffene det skal faktureres etter. Appen har tre områder,
**Målepunkter**, **Priser** og **Tariffer**, og den legger til en **Energi**-fane på
hver kunde i Kunder-appen.

Appen vises når energimodulen er slått på og du har `energy:metering-points-view` og
`energy:meters-view` for **Målepunkter**, `energy:prices-view` for **Priser**, eller
`energy:tariffs-view` for **Tariffer**; hvert område tilbys bare den som har
rettighetene til det. Hver del av et målepunkts side trenger i tillegg sin egen
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

Slå på **Uten kunde** for å liste bare målepunktene som ikke har noen kunde nå: ingen
aktiv leveranseperiode som har startet og ikke er avsluttet. Et målepunkt der den eneste
leveranseperioden starter senere, listes også. Dette er listen du går gjennom etter en
import som lot målepunkter stå uten kunde, se
[Importer målepunkter](#importer-målepunkter).

## Opprett et målepunkt

Klikk **Nytt målepunkt** i listen. Fyll inn:

- **GSRN**: nøyaktig 18 sifre. To målepunkter kan ikke ha samme GSRN.
- **Målernummer**: måleren som sitter på punktet i dag. Den registreres som punktets
  første måler, installert i det øyeblikket du lagrer.
- **Gateadresse**, **Postnummer**, **By**, **Landskode**: alle påkrevd; landskoden er
  to bokstaver og er `NO` som standard.
- **Prisområde**: NO1 til NO5.
- **Nettområde**: valgfritt, høyst 64 tegn.
- **Tilkoblingsstatus**: **Ny**, **Tilkoblet** eller **Frakoblet**; nye punkter
  starter som **Ny**.
- **Forventet årsforbruk (kWh)**, **Breddegrad**, **Lengdegrad**: valgfrie.
  Forventet årsforbruk må være under 100 000 000 000 kWh; breddegraden må ligge mellom
  -90 og 90, lengdegraden mellom -180 og 180.

**Opprett målepunkt** lagrer det og bekrefter med *Målepunktet er opprettet*. Dialogen
markerer feltene som mangler eller har feil form; en GSRN som allerede finnes avvises
med *Kunne ikke lagre målepunktet*. Å opprette krever `energy:metering-points-manage`
og `energy:meters-manage`.

## Importer målepunkter

**Importer** i listen henter inn målepunktene fra eksporten fra det forrige systemet
ditt, én rad per målepunkt. Knappen vises når du har `energy:import` og
`energy:metering-points-manage`; det importen skriver, krever de samme rettighetene som
å skrive det for hånd (se tabellen nederst). Trykk **Importer** for å åpne
**Importer målepunkter**.

Filen må ha en **GSRN** på hver rad; hver rad finnes igjen etter den. Et nytt målepunkt
trenger i tillegg **Målernummer**, **Gateadresse**, **Postnummer**, **Poststed** og
**Prisområde**. For å knytte et målepunkt til kunden sin trenger filen kundens
**organisasjonsnummer**, og gjerne datoen leveringen starter.

1. **Last opp filen.** Slipp en CSV-fil på siden eller velg en. Den kan være separert
   med semikolon, komma eller tabulator, i UTF-8 eller Windows (ANSI), med
   kolonnenavnene i første rad; høyst 50 MB og 100 000 rader. **Siste importer**
   nedenfor lister tidligere importer med status: åpne en for å fortsette der den sto,
   eller slett en som ikke kjører. Å slette en import fjerner ikke målepunktene den
   skrev.
2. **Velg kolonner.** Under **Hva som skal gjøres med radene** velger du **Opprett nye
   målepunkter** (en GSRN som finnes fra før, avvises), **Oppdater eksisterende
   målepunkter** (en GSRN ingen har, avvises) eller **Opprett og oppdater**. Velg så,
   for hver kolonne i filen, under **Importeres som** hvilket felt den blir, fra
   gruppene **Målepunkt**, **Adresse** og **Kundekobling**, eller la den stå som
   **Importeres ikke**. Vantigo foreslår felt for kolonnenavnene leverandøreksporter
   bruker, som *MÅLEPUNKTID*, *MÅLERNR*, *ANLEGGSADRESSE*, *PRISOMRÅDE*, *ORG. NR.* og
   *STARTDATO KUNDE*; når filen har både kundens postadresse og anleggets (*POSTNR* og
   *POSTNR ANLEGG*), foreslås anleggets. **Samme verdi for alle rader** setter et felt
   filen ikke har noen kolonne for, for eksempel ett prisområde eller én startdato for
   levering for alle radene. **Forhåndsvisning** viser de første radene slik de vil bli
   lest, med hver feil markert i cellen sin. Valgene lagres mens du gjør dem. Ved
   oppdatering endres bare feltene som er valgt; en tom celle i et valgt valgfritt felt
   tømmer det. En import endrer aldri et målepunkts **Fakturering**: et nytt
   målepunkt har ingen nettariff, er ikke merket for husholdning og har den ordinære
   elavgiftsklassen, og et oppdatert målepunkt beholder sine (se [Rediger et
   målepunkt](#rediger-et-målepunkt)).
3. **Kontroller filen.** Hver rad prøves akkurat som importen ville gjort, uten at noe
   lagres. Resultatet teller radene som **Ville blitt opprettet**, **Ville blitt
   oppdatert**, **Ville blitt hoppet over** og **Har feil**, og, når det finnes noen,
   de som **Ville byttet kunde** og **Ville ventet på en startdato**. **Endre valg av
   kolonner** tar deg tilbake til steg 2.
4. **Importer** lagrer radene, etter en bekreftelse som gjentar hvor mange målepunkter
   som opprettes og oppdateres. Importen kjører i bakgrunnen: du kan forlate siden og
   komme tilbake. **Stopp** stopper den etter radene som er i gang; radene som allerede
   er importert, blir stående.

Valgene du ikke har rettighet til, er grået ut, og gruppen **Kundekobling** tilbys bare
når du har `energy:supply-periods-manage`.

### Slik knyttes en rad til kunden sin

Et målepunkt knyttes til en kunde med en leveranseperiode, og en leveranseperiode
trenger en startdato. Derfor leser importen kunden og datoen hver for seg:

- **Kundens organisasjonsnummer** angir kunden. Det må være et gyldig norsk
  organisasjonsnummer som nøyaktig én kunde har. Et nummer ingen har, et som flere
  kunder har, eller en kunde som er arkivert eller slått sammen med en annen, gjør at
  raden feiler, og målepunktet skrives ikke.
- **Startdato for levering**, fra en kolonne eller som én verdi for alle rader, er
  dagen leveranseperioden starter (ved midnatt UTC). Datoer kan skrives `2026-01-01`,
  `01.01.2026` eller `01/01/2026`, alltid med dagen først. Med en dato skrives
  leveranseperioden når målepunktet importeres.
- **Uten startdato** blir målepunktet importert og koblet til kunden sin, men venter
  på å bli knyttet til: se [Knytt til de ventende
  målepunktene](#knytt-til-de-ventende-målepunktene).
- **Uten organisasjonsnummer** (kolonnen ikke valgt, eller cellen tom) blir
  målepunktet importert uten kunde, og en eksisterende leveranseperiode blir stående
  som den er. Finn disse målepunktene med **Uten kunde** i listen, og gi hvert av dem
  en kunde med **Tildel kunde**.
- Et målepunkt som allerede leveres til den kunden, blir stående som det er, uansett
  hva datoen sier.

En leveranseperiode importen skriver, når raden importeres, bytter kunde eller knyttes
til senere, har ingen krafttariff og faktureringsintervallet **Månedlig**: filen har
ingen avtale. Gi hver periode tariffen sin etterpå med **Endre vilkår**, se [Endre
vilkårene for en leveranseperiode](#endre-vilkårene-for-en-leveranseperiode).

### Når et målepunkt bytter kunde

Når et målepunkt leveres til en annen kunde i dag og raden angir en ny kunde med en
startdato, **bytter** importen kunde: den nåværende leveranseperioden avsluttes på
radens startdato, og kunden i filen overtar. Startdatoen må være senere enn starten på
den nåværende perioden.

Fordi filen kan være eldre enn et kundebytte som er gjort for hånd, setter kontrollen
en rød advarsel øverst på siden: *12 målepunkter vil bytte kunde. Hvert målepunkts
nåværende leveranseperiode avsluttes på radens startdato, og kunden i filen overtar.*,
med en tabell over **GSRN**, **Nåværende kunde**, **Ny kunde** og **Dato**. Når du
trykker **Importer**, gjentar bekreftelsen antallet, og **Importer**-knappen i den er
deaktivert til du krysser av for *Jeg forstår at 12 målepunkter vil bytte kunde.*
Har antallet endret seg siden kontrollen, avvises importen med årsaken, og siden leser
importen på nytt; kontroller filen igjen før du importerer.

### Knytt til de ventende målepunktene

Når rader hadde en kunde, men ingen startdato, viser den ferdige importen **Knytt til
kunder**: hvor mange målepunkter som venter, og en tabell over dem med kunden deres.
Velg **Startdato for levering** og trykk **Knytt til**; hvert ventende målepunkt får en
leveranseperiode for kunden sin fra den datoen. Når noen av dem leveres til en annen
kunde nå, vises den samme røde advarselen og avkrysningsboksen som over, og de bytter
kunde på den datoen. Hvert målepunkt blir knyttet til, eller fortsetter å vente med
årsaken vist i tabellen, så du kan knytte til på nytt med en annen dato til ingen er
igjen. Et målepunkt noen har knyttet til for hånd i mellomtiden, blir stående som det
er. Å knytte til krever `energy:supply-periods-manage`.

### Rader som feilet eller ble hoppet over

- Rader som **feilet**, listes med rad, kolonne og feil, for eksempel en GSRN som ikke
  er 18 sifre, en rad til med samme GSRN, eller et målernummer ved oppdatering som er
  forskjellig fra den nåværende måleren (bruk **Bytt måler** til det). **Last ned
  radene som feilet** gir deg de radene akkurat som de var, med en error-kolonne
  først; rett dem og last opp den filen som en ny import.
- Rader som ble **hoppet over**, er summeringslinjer: en rad uten GSRN og med høyst én
  annen utfylt celle, som det summerte årsforbruket mange eksporter slutter med. De
  telles og listes, men feiler aldri.

Den ferdige importen lister målepunktene som ble opprettet og oppdatert, og de som
byttet kunde, med begge kundene, hver som en lenke. Importer fjernes automatisk etter
30 dager; målepunktene og leveranseperiodene blir stående. Alle reglene står i
[Importing metering points](/en/reference/energy/#importing-metering-points).

## Rediger et målepunkt

Åpne målepunktet og klikk **Rediger målepunkt**, eller klikk på blyanten på raden i
listen. Dialogen har de samme feltene som når du oppretter, bortsett fra
målernummeret: en måler endres ved å bytte den, se nedenfor. **Lagre endringer**
bekrefter med *Målepunktet er oppdatert*. Å endre GSRN til en et annet målepunkt
allerede har, avvises. Å redigere krever `energy:metering-points-manage`.

Dialogen har også målepunktets faktureringsopplysninger, som et oppgjør vil trenge:

- **Nettariff**: nettariffen organisasjonen din fakturerer punktets nettjenester
  etter, når den er nettselskap her; la den stå tom når nettselskapet fakturerer dem
  selv, det vanlige for en kraftleverandør. Bare aktive nettariffer tilbys; en tariff
  som er arkivert etter at den ble valgt, blir stående på punktet til du endrer den.
  Listen vises når du har `energy:tariffs-view`; ellers vises tariffens navn, og den
  kan ikke endres her.
- **Husholdning**: kryss av når punktet leveres til husholdningsbruk. Vantigo kan
  ikke vite dette; du setter det.
- **Elavgiftsklasse**: **Ordinær** for de fleste punkter, **Redusert** eller
  **Fritatt** for et punkt som har krav på det, som avgjør hvilken elavgiftssats
  nettariffen bruker.

Dialogen beholder de tre som de er når du bare redigerer adressen eller et annet
felt.

## Les et målepunkts side

Siden har GSRN og adressen som overskrift, og fem kort:

- **Detaljer om målepunkt**: målernummer, prisområde, nettområde, tilkoblingsstatus og
  forventet årsforbruk.
- **Fakturering**: **Nettariff** (eller ingen), **Husholdning**, **Elavgiftsklasse**
  og **Mva-behandling**. Mva-behandlingen settes ikke for hånd: den er *fritatt
  (husholdning, Nord-Norge)* når punktet er til husholdningsbruk, landet er `NO` og
  postnummeret er fra 8000 til 9999 – elektrisk kraft til husholdningsbruk i
  Nordland, Troms og Finnmark er fritatt for merverdiavgift – og *standard* ellers.
  Den vises slik at et feil postnummer eller en glemt **Husholdning** blir sett her,
  før en faktura lages.
- **Målerhistorikk**: hver måler som har sittet på punktet, med tidspunkt for
  **Installert** og **Fjernet**. Den nåværende måleren har ikke noe fjernet-tidspunkt.
- **Leveranseperioder**: hvilken kunde punktet har levert til, fra når til når, etter
  hvilken **Krafttariff** og hvilket **Faktureringsintervall**, med statusen
  **Aktiv**, **Avsluttet** eller **Kansellert**. En slutt som ikke er satt, vises som
  **Åpen slutt**.
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
**Kunde** (skriv for å søke) og **Byttedato**, og vilkårene kunden leveres på:

- **Krafttariff**: kundens avtale, en av de aktive krafttariffene (se
  [Tariffer](#tariffer)). Den kan stå tom, men en periode uten krafttariff kan ikke
  faktureres. Listen vises når du har `energy:tariffs-view`.
- **Faktureringsintervall**: hvor ofte perioden faktureres, **Månedlig** (standard),
  **Kvartalsvis** eller **Årlig**. En forbruker kan faktureres høyst kvartalsvis;
  Vantigo godtar **Årlig** her for alle kunder, og faktureringen vil avvise det for en
  privatperson.

- Uten aktiv periode starter en ny **Aktiv** periode på den datoen.
- Med en aktiv periode avsluttes den på byttedatoen, og en ny starter for den nye
  kunden på samme dato. Byttedatoen må være senere enn den aktive periodens start, og
  kunden må være en annen; dialogen viser grunnen på feltet. Den nye kundens periode
  starter på vilkårene i dialogen; den overtar ikke den avsluttede periodens
  krafttariff eller faktureringsintervall.

Dialogen avviser når punktet allerede har en ikke-kansellert periode som dekker den
datoen, og ber deg avslutte den eksisterende perioden først. Perioden starter ved
midnatt UTC den valgte dagen. Krever `energy:supply-periods-manage`.

En leveranseperiode kan også startes fra kundens side, se
[Energi-fanen](#energi-fanen-på-kundesiden) nedenfor.

## Endre vilkårene for en leveranseperiode

Klikk **Endre vilkår** på raden under **Leveranseperioder**. Dialogen har periodens
**Krafttariff** og **Faktureringsintervall**, som da den ble startet; endre dem og
lagre. Slik får en periode som ble startet uten tariff, av en import eller før
tariffene fantes, en tariff. En aktiv eller en avsluttet periode kan endres; en
kansellert kan ikke. En tariff som er arkivert etter at perioden fikk den, blir stående
som den er, så du kan endre bare faktureringsintervallet, men en annen arkivert tariff
kan ikke velges. Krever `energy:supply-periods-manage`.

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
  eller **Manuell**) og **Kilde** (**Elhub**, **Manuell** eller **Import**).
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

## Importer avlesninger

For å hente inn et målepunkts historikk fra et annet system klikker du **Importer
avlesninger** under **Forbruk**. Knappen vises når du har `energy:import`,
`energy:consumption-manage` og `energy:consumption-view`. Én fil inneholder
avlesningene for dette ene målepunktet.

1. **Velg en CSV-fil** på høyst 10 MB og 50 000 rader (mer enn fem år med timeverdier):
   den første raden navngir kolonnene, skilletegnet kan være semikolon, komma eller
   tabulator, og filen kan være UTF-8 eller en norsk Excels egen tegnkoding.
2. Si hvilken kolonne som inneholder hva. Kolonnene foreslås ut fra navnene (*Fra*,
   *Fra og med*, *Start* eller *From* for starten; *Til*, *Slutt*, *End* eller *To* for
   slutten; *kWh*, *Forbruk*, *Volum* eller *Mengde* for mengden; *Kvalitet* for
   kvaliteten; *Målepunkt* eller *GSRN* for målepunktet), og du kan endre hver av dem:
   - **Start** og **Mengde (kWh)** må være med.
   - **Slutt**, eller, når filen har ett tidspunkt per linje, en **Oppløsning**: **Én
     time (PT1H)** eller **15 minutter (PT15M)**, og hver avlesning slutter så lenge
     etter at den starter.
   - **Kvalitet** er valgfri; uten den er hver avlesning **Målt**. Filen kan skrive
     Measured, Estimated, Corrected eller Manual, eller *målt*, *estimert*,
     *stipulert*, *korrigert* eller *manuell*, med store eller små bokstaver; en tom
     celle er målt.
   - **Målepunkt (GSRN)** er valgfri. Når du knytter den til en kolonne, må hver rad
     nevne dette målepunktet, slik at en fil ment for et annet punkt avvises før noe
     skrives.
3. Klikk **Kontroller**. Ingenting lagres; du ser hvor mange avlesninger som er
   **nye**, hvor mange som **erstatter** en som allerede ligger på målepunktet, og hvor
   mange som er **uendret**, perioden filen dekker, og, når rader har feil, de første
   200 av dem med rad (den første raden under overskriften er rad 1) og kolonne.
4. Når kontrollen ikke fant noen feil, klikker du **Importer**. Den sier hva den vil
   gjøre – *Legger til 12 avlesninger og erstatter 3.* – og **Importer** der skriver
   dem. Forbruket på siden oppdateres med en gang.

Slik leses filen:

- **Tidspunkt.** En dato og et klokkeslett kan skrives som `2025-01-31 13:00` eller
  `2025-01-31T13:00` (sekunder valgfritt), `31.01.2025 13:00` eller
  `31/01/2025 13:00`. Et tidspunkt med tidssoneforskyvning, som
  `2025-01-31T13:00+01:00` eller `…Z`, er akkurat det øyeblikket. Et tidspunkt uten er
  målepunktets lokale tid: norsk tid for et norsk prisområde.
- **Sommertid.** Natten om våren da klokkene hopper fra 02:00 til 03:00, finnes ikke et
  tidspunkt imellom, som 02:30, og raden avvises. Natten om høsten da klokkene går
  tilbake fra 03:00 til 02:00, skjer timen fra 02:00 to ganger: den første raden som
  starter på et gjentatt tidspunkt, er den første forekomsten (sommertid), den neste
  raden som starter på samme tidspunkt, den andre (normaltid). Hold radene i den
  rekkefølgen de skjedde. En slutt på et gjentatt tidspunkt er det første øyeblikket
  med det navnet etter avlesningens start, så *02:00–03:00* to ganger leses som to
  timer etter hverandre.
- **Mengder** godtar desimalkomma eller -punktum (`1,5` og `1.5`; mellomrom som
  tusenskille ignoreres), må være null eller større, og lagres med tre desimaler.
- **Hver rad** følger reglene for manuelle avlesninger: slutten må være senere enn
  starten. To rader i filen med samme start og slutt er en feil på den andre.

Dette skriver importen:

- En rad med samme start og slutt som en avlesning som allerede ligger på
  målepunktet, og med samme mengde og kvalitet, er **uendret**: ingenting skrives for
  den, så å importere den samme filen to ganger endrer ingenting.
- En rad med samme start og slutt, men en annen mengde eller kvalitet, **erstatter**
  den avlesningen, slik en manuell avlesning gjør; den gamle beholdes som historikk.
- Alle andre rader er **nye**. En avlesning som bare overlapper en rad uten å ha samme
  start og slutt, blir liggende som den er.
- Importerte avlesninger har kilden **Import**.
- **Alt eller ingenting.** Når en rad har en feil, importeres ingenting; rett filen og
  kontroller den på nytt.

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
for kunden på det punktet, uten krafttariff og med faktureringsintervallet
**Månedlig**; sett dem på målepunktets side med **Endre vilkår**. Det avvises med *Dette målepunktet har allerede en
overlappende leveranseperiode* når punktet allerede leveres til på den datoen, og med
*Kunne ikke knytte til målepunktet* ellers. Knappen tilbys ikke på en kunde som er
slått sammen med en annen eller anonymisert. Når to kunder slås sammen, flyttes
leveranseperiodene til den oppslukte kunden over til den som består.

## Priser

**Priser** holder spotprisene fra døgnmarkedet for hvert prisområde: prisen per kWh for
hver time, eller hvert kvarter, som forbruk på en spotavtale prises etter. Prisene
kommer fra én kilde administratoren har valgt
([Bakgrunnsjobber og skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)):
**spotpristjenesten** våre driftsmiljøer kjører, som en bakgrunnsjobb kopierer fra,
årene med historikk medregnet; eller en **offentlig priskilde**, som jobben spør om hver
dags priser; eller ingen av dem. Priser kan dessuten alltid importeres fra en fil, som
også er måten å fylle inn dager kilden mangler.

Åpne **Energi** → **Priser**. Siden krever `energy:prices-view`. Priser er markedstall,
ikke en kundes, så siden viser ingen kundedata.

### Les dekningen

Det første kortet, **Dekning**, sier i et merke ved tittelen hvor prisene kommer fra:
**Spotpristjenesten**, **Offentlig feed**, eller **Bare filer** når ingen kilde er satt
og prisene bare kommer ved import. Det har én rad per prisområde: hvert område et
målepunkt med en ikke-kansellert leveranseperiode ligger i, hvert område som allerede
har priser og, med spotpristjenesten, hvert område tjenesten følger — så kortet fylles
før det første målepunktet finnes. **Ingen prisområder ennå** betyr at det ikke finnes
noe slikt område. Hver rad viser:

- **Første** og **Siste**: starten på den første prisen som finnes, og slutten på den
  siste;
- **Oppløsning**: lengden på den siste prisen som finnes, **15 min** eller **1 time**.
  Døgnmarkedet gikk fra timer til kvarter 1. oktober 2025, så et område med historikk
  fra før da har begge;
- **Intervaller**: hvor mange priser som finnes;
- **Dager uten priser siste 30**: dagene blant de siste 30, i dag medregnet, der ingen
  pris starter — **Komplett** når det ikke er noen;
- **Historikk**, bare med spotpristjenesten: hvor langt tilbake jobben har kopiert
  områdets priser. **Kopierer… tilbake til** *en dato*: kopieringen pågår, bakover fra
  for to uker siden, og har kommet til den dagen; mens den kopierer, arbeider jobben
  hvert minutt, og fem områder tilbake til 2015 tar omtrent en halvtime. **Komplett
  fra** *en dato*: alt tjenesten har for området fra den dagen av er kopiert — den
  eldste dagen den har, eller en senere dag administratoren valgte. **Venter på
  tjenestens dekning**: kopieringen har ikke begynt, fordi tjenesten ennå ikke har sagt
  hvor langt tilbake den har området. Under teksten teller *N dager hoppet over* dager
  tjenesten ikke hadde hele, som jobben lar være igjen til du importerer dem fra en fil,
  og den siste feilen, når det er en, vises i en setning; jobben prøver igjen senere av
  seg selv. **Følges ikke av spotpristjenesten** merker et område i listen som
  tjenesten ikke samler inn: jobben spør ikke etter det, og prisene må importeres. Et
  område tjenesten slutter å følge, beholder prisene og historikken som allerede er
  kopiert;
- **Siste henting**: dagen bakgrunnsjobben sist spurte om, og hvordan det gikk.
  **Hentet**: dagens priser er lagret. **Ikke publisert ennå**: dagens eller
  morgendagens priser er ikke ute ennå — døgnmarkedets auksjon publiserer dem rundt
  klokken 13 norsk tid — eller, fra spotpristjenesten, dagen er lagret, men noen av
  prisene er fortsatt foreløpige ([nedenfor](#foreløpige-priser)); jobben spør igjen
  hver time. **Mangler**: kilden har ingen priser for den tidligere dagen; jobben spør
  igjen én gang i døgnet, høyst sju ganger på rad, og deretter importerer du dagen fra
  en fil. **Feilet**: årsaken vises under, og jobben prøver igjen senere og venter
  lenger hver gang, høyst seks timer. **Ikke hentet** betyr at jobben aldri har prøvd
  området, som når den er slått av.

Fra den offentlige priskilden henter jobben bare de siste sju dagene og morgendagen; en
dag som mangler lenger tilbake, må importeres. Fra spotpristjenesten leser den de siste
fjorten dagene og morgendagen om og om igjen, så tjenestens rettelser kommer med, og
kopierer historikken én gang.

### Se prisene for en dag eller en uke

Kortet **Spotpriser** viser prisene som en linje i kr/kWh, lest i norsk tid. Det åpner
på dagens dato i det første området. Velg perioden i knapperaden — **Dag**, **Uke**,
**Måned**, **År** eller **Tidsrom** — og deretter tiden den dekker.

- **Dag**, med dagen i **Dag**: hver pris den dagen som en trappelinje, hver tegnet fra
  intervallets start og holdt til den neste, med et punkt på hver. En dag har 24
  timepriser (23 eller 25 de nettene klokkene stilles), eller 96 kvarter med den
  oppløsningen. Høstnatten kommer timen fra 02:00 to ganger, og diagrammet merker hver
  av dem med UTC-forskyvningen, *02:00 +02* og *02:00 +01*.
- **Uke**, med en hvilken som helst dag i uken i **Dag**: hver pris i uken, mandag til
  søndag, som en trappelinje uten punkter, merket med ukedag og klokkeslett.

For å se hver pris i en måned, se på den en uke om gangen med **Uke**: **Måned** viser
gjennomsnitt per dag ([nedenfor](#se-en-måned-et-år-eller-et-tidsrom)).

### Se en måned, et år eller et tidsrom

En måned eller mer er for mange priser til å tegne én og én, så disse periodene viser
**gjennomsnitt**: en linje gjennom hver dags, ukes eller måneds gjennomsnittspris, på et
skyggelagt bånd fra den dagens laveste til dens høyeste pris (**Lavest–høyest**). Når du
peker på diagrammet, vises gjennomsnittet og båndets to ytterpunkter.

- **Måned**, valgt i **Måned**: ett gjennomsnitt per dag i måneden. Dette er endret:
  **Måned** tegnet tidligere hver pris i måneden, og tegner nå gjennomsnitt per dag;
  bruk **Uke** for hver pris.
- **År**, valgt i **År**: ett gjennomsnitt per dag i året.
- **Tidsrom**, fra **Fra måned** til **Til måned**, begge medregnet: ett gjennomsnitt
  per uke for et tidsrom på opptil to år — hele uker, mandag til søndag, så den første
  og den siste uken kan strekke seg inn i månedene på hver side — og ett per måned for
  et lengre, opptil tretti år (et lengre tidsrom beholder de siste tretti). Det åpner
  på de siste tolv månedene.

Et gjennomsnitt vekter hver pris etter hvor lenge den gjaldt, så en dag med timepriser
og en dag med kvarterpriser teller likt. En dag, en uke eller en måned uten noen pris
utelates fra linjen i stedet for å tegnes som null, og en med bare noen av prisene får
gjennomsnittet av dem. En periode uten priser sier *Ingen priser for denne perioden.*

### Sammenlign områder

I **Sammenlign områder** velger du opptil fem prisområder. Hvert får sin egen linje i
sin egen farge, med en forklaring, og det første området du valgte får den første
fargen. Båndet lavest–høyest tegnes bare når ett område vises, siden flere bånd ville
skjule hverandre. Minst ett område er alltid valgt.

### Les tallene

Over diagrammet oppsummerer tre tall perioden for det **første** området i
**Sammenlign områder** — når du sammenligner, navngir hver etikett det, som
*Gjennomsnitt · NO1*:

- **Gjennomsnitt**: periodens gjennomsnittspris, hver pris vektet etter hvor lenge den
  gjaldt;
- **Lavest** og **Høyest**: periodens laveste og høyeste pris, med når den gjaldt:
  intervallet (og, for en uke, dagen) for **Dag** og **Uke**, eller dagen, uken eller
  måneden den falt i for de lengre periodene.

### Foreløpige priser

Fra spotpristjenesten publiseres morgendagens priser så snart auksjonen er ferdig, mens
kronebeløpet fortsatt er **foreløpig**: det bruker en midlertidig valutakurs til dagens
offisielle kurs er ute. Jobben lagrer dem som de er og erstatter dem når de blir
endelige, så en foreløpig pris kan fortsatt endre seg litt. Når en pris i visningen er
foreløpig, vises merket **Foreløpig** ved **Spotpriser**, og den foreløpige delen av
hver linje tegnes **stiplet**, med en egen oppføring i forklaringen (*NO1
(foreløpig)*). For de lengre periodene tegnes en dag, uke eller måned med en foreløpig
pris stiplet. Priser fra den offentlige priskilden eller fra en fil er alltid endelige.

### Del en lenke til en visning

Siden holder det du ser på — områdene, perioden, dagen og månedene i et tidsrom — i
adressen sin, som i
`/energy/prices?areas=NO1,NO2&period=range&from=2024-01&to=2025-12`. Kopier adressen
for å sende noen den samme visningen, lagre den som bokmerke, eller last inn siden på
nytt uten å miste den. En verdi i adressen siden ikke kjenner igjen, ignoreres, og
siden viser standardvisningen i stedet; det samme gjelder et område siden ikke viser.

### Importer priser fra en fil

**Importer priser**, øverst på siden, vises når du i tillegg har `energy:prices-manage`.
Den åpner **Importer priser**:

1. **Velg en CSV-fil** på høyst 10 MB og 50 000 rader: den første raden navngir
   kolonnene, skilletegnet kan være semikolon, komma eller tabulator, og filen kan være
   UTF-8 eller en norsk Excels egen tegnkoding. Kolonnene finnes ut fra navnene, uansett
   store og små bokstaver, mellomrom og tegnsetting:
   - **prisområdet**, må være med: *Område*, *Prisområde*, *Elspotområde*, *Sone*,
     *Area* eller *Price area*, med området, som `NO1`;
   - **starten**, må være med: *Start*, *Fra*, *Starttid*, *From* eller *Time start*;
   - **slutten**, valgfri: *Til*, *Sluttid*, *End*, *To* eller *Time end*;
   - **prisen**, må være med: i kroner per kWh, *Pris*, *kr/kWh*, *NOK/kWh*, *Price*
     eller *NOK per kWh*; eller i øre per kWh, *øre/kWh* eller *ore_per_kwh*, som deles
     på 100;
   - **prisen i EUR** (*EUR per kWh*, *EUR/kWh*) og **valutakursen** (*Kurs*, *EXR*,
     *Exchange rate*), valgfrie og tatt vare på for ordens skyld.

   En fil kan inneholde flere prisområder. En fil med to kolonner for det samme — *Pris*
   og *øre/kWh*, for eksempel — avvises, og det samme gjør en som mangler en kolonne som
   må være med.
2. Filen kontrolleres så snart du velger den, og ingenting lagres. Resultatet,
   **Tørrkjøring** med antall rader, viser for hvert område hvor mange priser som er
   **Nye**, hvor mange som er **Erstattet** (en pris som allerede finnes, med andre
   tall) og hvor mange som er **Uendret**, og perioden filen **Dekker**; eller, når
   rader har feil, de første 200 av dem med rad (den første raden under overskriften er
   rad 1) og kolonne, og ingenting kan importeres før filen er rettet.
3. Når kontrollen ikke fant noen feil, klikker du **Importer** for å lagre prisene.
   *Priser importert* bekrefter det, og dekningen og diagrammet oppdateres. Når alle
   prisene i filen allerede finnes, sier kontrollen *Alle prisene i filen er allerede
   lagret.*, og det er ingenting å importere. Har de lagrede prisene endret seg etter
   kontrollen slik at en rad nå har en feil, importeres ingenting: *Filen har feil nå.
   De vises nedenfor.*, og kontrollen kjøres på nytt.

Slik leses filen:

- **Tidspunkt** skrives som for [Importer avlesninger](#importer-avlesninger):
  `2025-01-31 13:00`, `2025-01-31T13:00`, `31.01.2025 13:00` eller `31/01/2025 13:00`.
  Et tidspunkt med tidssoneforskyvning (`+01:00`, `Z`) er akkurat det øyeblikket; et
  tidspunkt uten er norsk tid, uansett område. Natten om våren avvises et tidspunkt
  klokkene hopper over; natten om høsten er et områdes første rad på et gjentatt
  tidspunkt den første forekomsten og dets neste den andre.
- **Prisområdet** kan skrives med store eller små bokstaver: `no1` er `NO1`.
- **Priser** godtar desimalkomma eller -punktum (`0,8123` eller `0.8123`, ikke begge);
  mellomrom som tusenskille ignoreres, og en pris kan være negativ. De lagres med seks
  desimaler. En valutakurs må være over null; prisen i EUR og kursen kan stå tomme.
- **Hver pris** varer et kvarter eller en time, starter på kvarteret (minutt 00, 15,
  30 eller 45), og en pris på en time starter på hel time — slik markedets gjør. En rad
  som ikke gjør det, avvises med årsaken.
- **Med en sluttkolonne** varer hver pris fra starten til slutten.
  Radene trenger ikke å følge rett etter hverandre, så én fil kan fylle flere adskilte
  dager; men radene i hvert område må stå i tidsrekkefølge og kan ikke overlappe.
- **Uten en sluttkolonne** varer hver pris til neste rad i samme område starter, og et
  områdes siste rad like lenge som raden før den (én time når området bare har én rad).
  Hold radene i hvert område i tidsrekkefølge uten hull: en rad avvises når neste rad i
  området starter senere enn ett intervall etter den.

Dette skriver importen:

- En pris med samme område, start og slutt som en som allerede finnes, og med de samme
  tallene, er **Uendret**: å importere den samme filen to ganger endrer ingenting.
- En med andre tall **erstatter** prisen som finnes; det gamle tallet beholdes ikke.
- En pris som overlapper en lagret pris av en annen lengde — et kvarter inne i en
  lagret time, for eksempel — avvises på raden sin: ett område kan ikke ha priser med
  to oppløsninger for samme tid.
- **Alt eller ingenting.** Når en rad har en feil, importeres ingenting; rett filen og
  velg den på nytt.

De fullstendige reglene står i [Spot prices](/en/reference/energy/#spot-prices).

## Tariffer

**Tariffer** holder prislistene forbruket skal faktureres etter, i tillegg til
spotprisen. Det finnes to slag:

- En **krafttariff** er avtalen din med en kunde: en spotpris eller en fastpris, et
  påslag og et månedsbeløp, for eksempel. Den velges på kundens leveranseperiode.
- En **nettariff** er din egen nettariff, for målepunktene organisasjonen din er
  nettselskap for: energileddet, kapasitetsleddet, fastleddet, elavgiften og
  Enova-avgiften. Den velges på målepunktet. Når nettselskapet fakturerer
  nettjenestene selv, trenger du ingen.

En tariff har **versjoner** etter dato, og hver versjon en liste med **komponenter**,
én linje på fakturaen hver. Når prisene endres, legger du til en versjon fra dagen de
endres; den gamle versjonen blir stående for periodene den priset.

Åpne **Energi** → **Tariffer**. Listen viser hver tariffs navn, slag, status,
versjonen som gjelder i dag og hvor mange leveranseperioder og målepunkter som bruker
den, aktive tariffer først; avgrens den etter slag eller status. Siden krever
`energy:tariffs-view`; å opprette, redigere, arkivere og slette krever i tillegg
`energy:tariffs-manage`.

Tariffene brukes ikke til noe ennå: oppgjør av forbruk etter dem kommer i en senere
versjon av Vantigo. Fram til da kan du sette dem opp og velge dem på
leveranseperiodene og målepunktene.

### Opprett en tariff

Klikk **Ny tariff**. Velg slaget, **Kraft** eller **Nett** (det kan ikke endres
etterpå), og gi tariffen et **Navn**, som skrives på fakturaen som avtalens navn, og
eventuelt en beskrivelse. Legg så til minst én versjon med minst én komponent, og
lagre. Klikk på en rad i listen for å åpne en tariff og redigere den på samme måte.

Dialogen markerer hver feil på feltet den gjelder, som en sats som mangler eller to
versjoner med datoer som overlapper.

### Legg til versjoner

Hver versjon har **Gyldig fra** og **Gyldig til**. **Gyldig fra** er den første dagen
versjonen gjelder; **Gyldig til** er den første dagen den ikke lenger gjelder, så en
versjon fra 1. januar gyldig til 1. juli dekker januar til juni. La **Gyldig til** stå
tom for en versjon som gjelder inntil videre. Dagene er norske kalenderdager.

To versjoner av én tariff kan ikke overlappe. De kan la det være et opphold, men en
dag ingen versjon dekker, prises ikke. For å endre prisene fra en dag setter du
**Gyldig til** på den gjeldende versjonen til den dagen og legger til en ny versjon
fra den.

### Legg til komponenter

Legg til komponenter i en versjon etter slag; hver har et **Navn**, teksten på linjen
sin på fakturaen. Flytt dem opp eller ned for å bestemme rekkefølgen linjene skrives
i. Satsene er i NOK. En krafttariff kan ha:

- **Spotpris**: energien til timens eller kvarterets spotpris. Ingen sats.
- **Fastpris**: energien til en fast pris per kWh.
- **Påslag**: en pris per kWh i tillegg til energiprisen. En versjon kan ha flere.
- **Månedsbeløp**: et fast beløp per måned, forholdsmessig når leveringen dekker en
  del av en måned.

Hver versjon av en krafttariff har nøyaktig én **Spotpris** eller **Fastpris**.

En nettariff kan ha:

- **Energiledd**: en pris per kWh på dagtid, og eventuelt en nattsats med timene
  dagsatsen gjelder (fra og til, hele timer) og om nattsatsen gjelder hele helgen.
- **Kapasitetsledd**: et beløp per måned etter trinnene nedenfor.
- **Fastledd**: et fast beløp per måned.
- **Elavgift**: den ordinære og den reduserte satsen per kWh; målepunktets
  **Elavgiftsklasse** avgjør hvilken som gjelder, eller ingen.
- **Enova-avgift**: husholdningssatsen per kWh, og beløpet per år et punkt som ikke er
  til husholdningsbruk, betaler.
- **Månedsbeløp**: som for en krafttariff.

En versjon har høyst ett hver av energiledd, kapasitetsledd, elavgift og
Enova-avgift.

### Sett kapasitetstrinnene

Kapasitetsleddet er en tabell med trinn: **Fra kW** og prisen **Per måned**. Hver
måned finnes punktets trinn ut fra snittet av de tre høyeste timeforbrukene, på tre
forskjellige dager. Det første trinnet starter på 0 kW, og hvert neste må starte
høyere; en tabell har høyst 50 trinn.

### Arkiver eller slett en tariff

**Arkiver** en tariff som ikke lenger skal velges. En arkivert tariff blir stående på
leveranseperiodene og målepunktene som allerede har den, og gjelder fortsatt for dem,
og du kan fortsatt redigere den, for å rette et tall; den tilbys bare ikke for nye
valg.

**Slett** fjerner en tariff for godt, og bare en ingen bruker: ingen
leveranseperiode, heller ikke en avsluttet eller en kansellert, og intet målepunkt har
den. For en tariff som er i bruk, er knappen deaktivert med årsaken; arkiver den i
stedet.

De fullstendige reglene står i [Tariffs](/en/reference/energy/#tariffs).

## Rettigheter

| Rettighet | Lar deg |
| --- | --- |
| `energy:metering-points-view` | Se listen og målepunktets side, søke etter målepunkter, se Energi-fanen |
| `energy:metering-points-manage` | Opprette og redigere målepunkter, faktureringsopplysningene medregnet |
| `energy:meters-view` | Se målerhistorikken (kreves sammen med den over for å se appen i det hele tatt) |
| `energy:meters-manage` | Bytte måler og registrere den første måleren ved opprettelse |
| `energy:supply-periods-view` | Se leveranseperioder og en kundes målepunkter |
| `energy:supply-periods-manage` | Tildele, bytte, avslutte og knytte til leveranseperioder, og endre vilkårene deres |
| `energy:consumption-view` | Se forbruk og Energi-fanens forbrukstall |
| `energy:consumption-manage` | Legge til manuelle avlesninger |
| `energy:prices-view` | Se **Priser**: dekningen og hvert områdes priser |
| `energy:prices-manage` | Importere priser fra en fil, sammen med `energy:prices-view` |
| `energy:tariffs-view` | Se **Tariffer**, og velge en tariff på en leveranseperiode eller et målepunkt |
| `energy:tariffs-manage` | Opprette, redigere, arkivere og slette tariffer, sammen med `energy:tariffs-view` |
| `energy:import` | Importere målepunkter fra en fil, sammen med `energy:metering-points-manage`; importere avlesninger, sammen med `energy:consumption-manage` og `energy:consumption-view`; det en import skriver, krever rettighetene over |
