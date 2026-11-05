---
title: Lager
description: Hva som ligger hvor — Lager-appen og sidemenyen, å finne en lokasjon eller en vare ved å skrive eller skanne koden, å opprette et lagersted, å legge til lokasjoner én og én og mange om gangen, å endre, flytte, arkivere og slette dem, å skrive ut lokasjonsetiketter, varemottak, justering og flytting av beholdning med linjeredigeringen, å registrere antall i pakninger, varetelling, import av inngående beholdning, hva som ligger på en lokasjon, en vares beholdning og bevegelser, listen Bevegelser og siden for en bevegelse, Beholdning-kortet på produktsiden, det ordrene har reservert og det som er disponibelt, hvilke lagersteder som leverer ordrer, kortet på dashbordet, oppfølgingspunktet og hurtighandlingene, og hvem som ser hva.
sidebar:
  order: 65
sources:
  - apps/inventory/frontend
  - apps/host/frontend/src/routes/inventory
---

Lager-appen holder oversikt over beholdningen av varene dine: på hvilket lager eller i
hvilken butikk, og på hvilken hylle, i hvilken kasse eller på hvilken utstilling, hver
vare ligger, og hvor mye. Hvert sted hører til et **lagersted** — et lager eller en
butikk — og et lagersted er delt inn i **lokasjoner**, så dypt du trenger: en sone, en
gang, en reol, en hylle. Beholdningen endres bare når noen bokfører en **bevegelse** —
et **varemottak** av leverte varer, en **justering** med en årsak, eller en **flytting**
fra én lokasjon til en annen — og en bevegelse endres aldri etter at den er bokført: en
feil rettes med en ny justering. En **varetelling** sjekker hva som faktisk ligger i
hyllene, og bokfører avvikene. Varene kommer fra [Produkter](/nb/user/products/): bare
**varer** holdes på lager, aldri tjenester. Reglene bak hvert skjermbilde — kodene,
avvisningene og rettigheten hver forespørsel sjekkes mot — står i
[referansen for Lager-modulen](/en/reference/inventory/); denne siden handler om hva du
gjør i hvert skjermbilde.

## Appen og sidemenyen

**Lager**-flisen, etter Produkter i appvelgeren, vises for alle med **Se lagersteder**
(`inventory:locations-view`) eller **Se beholdning** (`inventory:stock-view`).
Sidemenyen viser:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Oversikt** — oppslaget og lagerstedene | `inventory:locations-view` eller `inventory:stock-view` |
| **Bevegelser** | `inventory:locations-view` og `inventory:stock-view` |
| **Varemottak** — listen over bevegelser med bare varemottak | `inventory:locations-view` og `inventory:stock-view` |
| **Varetellinger** — varetellingene | `inventory:counts-manage`, med begge visningsrettighetene |
| **Importer inngående beholdning** — et lagersteds første beholdning fra et regneark | `inventory:stock-adjust`, med begge visningsrettighetene |

Det rettighetene dine ikke tillater, vises ikke i det hele tatt: en knapp du ikke ser, er
en rettighet du ikke har. Knappene som bokfører — varemottak, justeringer og flyttinger —
krever begge visningsrettighetene i tillegg til **Motta varer** eller **Justere
beholdning**; varetelling krever begge visningsrettighetene og **Telle varer**. Antall krever begge visningsrettighetene, fordi ethvert sted
beholdning ligger, er en lokasjon; **Se lagersteder** alene viser lagerstedene og
lokasjonene deres uten antall.

## Finn en lokasjon eller en vare

**Oversikt** åpner med feltet **Finn lokasjon eller vare**, klart til å skrive i. Skriv
eller skann ett av disse og trykk Enter:

- en lokasjons **fulle kode**, som `OSL-A-03-2`, med store eller små bokstaver;
- en lokasjons **etikettkode**, `LOC` og et tall;
- en vares **varenummer** eller **strekkode**, eller strekkoden til en av pakningene
  dens — en eske eller en pall åpner varen.

Ett treff åpnes med en gang: en lokasjon åpner siden sin, en vare beholdningssiden sin.
Flere treff listes opp, hvert merket **Lokasjon** eller **Vare**, så du kan velge. Ingen
treff gir *Ingenting passer med «…».*, og det du skrev, blir stående så du kan rette
det.

En **håndskanner** virker i feltet som den er: skanneren skriver koden den leser og
trykker Enter, og det er nettopp det oppslaget venter på. Varer vises bare for dem som
også har **Se beholdning**.

Det samme oppslaget er hurtighandlingen **Finn lokasjon eller vare** i søket i
toppfeltet, for alle med **Se lagersteder**.

## Opprett et lagersted

Trykk **Nytt lagersted** i **Oversikt** (det krever **Administrere lagersteder**,
`inventory:locations-manage`). Gi det:

- en **Kode** — 1–16 bokstaver A–Z og sifre, som `OSL`. Den innleder den fulle koden
  til hver lokasjon på lagerstedet, og ingen andre lagersteder kan ha den;
- et **Navn**;
- en **Type**: **Lager** eller **Butikk**. Den endrer ordene i skjermbildet, ikke
  reglene;
- **Leverer ordrer** — om ordrer reserverer og leverer varer fra lagerstedet. Den er på
  for et lager og av for en butikk til du endrer den;
- en adresse om du vil: **Adresse**, **Adresselinje 2**, **Postnummer**, **Poststed**
  og **Land** (to bokstaver, som NO).

Det nye lagerstedet åpnes på sin egen side. Et lagersted er også en lokasjon:
beholdning kan ligge på selve lagerstedet, som passer for en liten butikk uten hyller,
og flyttes ned senere når du legger til hyller.

For å endre om et eksisterende lagersted leverer ordrer, åpner du lagerstedet, trykker
på menyen (**…**) og **Rediger lagersted**, og slår **Leverer ordrer** av eller på. Et
lagersted som gjør det, viser *Leverer ordrer* ved siden av typen. Slår du den av,
stanser nye reservasjoner og leveranser der; det ordrene allerede har reservert, forblir
reservert.

I **Oversikt** vises hvert lagersted som et kort med typen, poststedet og antallet
lokasjoner; med **Se beholdning** også beholdningslinjene og *Siste varemottak …*.
Kortene lastes sammen med listen over lagersteder, så også mange lagersteder vises med
en gang.

## Legg til lokasjoner

På lagerstedets side legger **Ny lokasjon** til en rett under lagerstedet; på en
lokasjon — i radens meny i kortet **Lokasjoner**, eller i **…**-menyen på lokasjonens
side — legger **Legg til lokasjon under** til en under den. Velg **Overordnet**,
**Type** (**Sone**, **Område**, **Salgsflate**, **Bakrom**, **Gang**, **Reol**,
**Hylle**, **Kasse**, **Utstilling**, **Rampe** eller **Annet**), **Kode**, og eventuelt
**Navn** og **Sorteringsrekkefølge**. Koden må være unik under samme overordnede, og
lokasjonens **fulle kode** er kodene fra lagerstedet og ned, satt sammen med
bindestreker: hylle `2` på reol `03` i gang `A` på `OSL` er `OSL-A-03-2`. Treet går
høyst åtte nivåer under lagerstedet.

### Mange om gangen

**Legg til mange**, på lagerstedets side eller i en lokasjons meny, oppretter et helt
rutenett på én gang. Legg til opptil tre nivåer; for hvert velger du **type**, et
valgfritt **prefiks** og et intervall **fra** – **til**:

- et intervall av sifre fylles ut til bredden av starten: `01` til `12` gir
  01, 02 … 12;
- et intervall av bokstaver går fra den ene til den andre: `A` til `D`;
- la **til** stå tom for én kode.

Hver kode på et nivå opprettes under hver lokasjon på nivået før. Et eksempel, under
lagerstedet **OSL**: nivå 1 en **Gang** `A` med **til** tom, nivå 2 **Reol** `01` til
`12`, nivå 3 **Hylle** `1` til `5`. Dialogen sier *73 lokasjoner opprettes.* — gangen,
de 12 reolene og **60 hyller**, `OSL-A-01-1` til `OSL-A-12-5`. Høyst 1000 om gangen, og
alt eller ingenting: er én av kodene allerede tatt, opprettes ingenting, og du får vite
hvilke fulle koder som kolliderer.

## Endre, flytte, arkivere eller slette en lokasjon

Åpne lokasjonens meny — radens meny i kortet **Lokasjoner**, eller **…** på siden
dens — med **Administrere lagersteder**:

- **Rediger** endrer typen, koden, navnet og sorteringsrekkefølgen, og **flytter**
  lokasjonen: velg en annen **Overordnet** på samme lagersted. Alt under den, og
  beholdningen som ligger der, følger med; de fulle kodene under endres tilsvarende. En
  ny overordnet må være aktiv, og kan ikke være lokasjonen selv eller noe under den.
  Mens en varetelling er åpen over lokasjonen, over noe under den eller over der den
  skal flyttes, kan den ikke flyttes — en varetelling av hele lagerstedet stopper alle
  flyttinger på lagerstedet; bokfør eller avbryt varetellingen først. Å endre bare
  koden eller navnet er alltid tillatt.
- **Arkiver** arkiverer lokasjonen og alt under den. Arkiverte lokasjoner tar ikke imot
  beholdning og forsvinner fra velgerne. Det avvises så lenge det ligger beholdning på
  lokasjonen eller under den: flytt den eller juster den til null først.
- **Gjenåpne** henter tilbake bare lokasjonen selv — ikke det som ligger under — og bare
  under en aktiv overordnet.
- **Slett** fjerner lokasjonen og alt under den for godt. Det avvises så snart det noen
  gang er bokført beholdning på noen av dem: arkiver i stedet.

Lagerstedets egen lokasjon — toppen av treet — kan ikke arkiveres eller slettes alene;
da arkiveres eller slettes lagerstedet. I lagerstedets **…**-meny: **Rediger lagersted**
(endrer du lagerstedets kode, endres hver full kode på det — trykte etiketter kan
fortsatt skannes), **Arkiver** (avvises mens lagerstedet har beholdning; arkiverer hver
lokasjon på det), **Gjenåpne** (lagerstedet og dets egen lokasjon; lokasjonene under
gjenåpnes én og én) og **Slett** (bare for et lagersted det aldri er bokført en
bevegelse på). Hver av dem ber deg bekrefte først.

I kortet **Lokasjoner** åpnes treet ett nivå om gangen. **Vis arkiverte** tar med
arkiverte lokasjoner, og **Søk på kode eller navn** finner en — mens du skriver på et
lagersted med opptil 2000 lokasjoner; på et større skriver du og trykker Enter, og søket
går over hele lagerstedet. Treet viser høyst 10 000 lokasjoner; et lagersted med flere
sier *Lagerstedet har mer enn 10 000 lokasjoner; treet viser de første 10 000 etter
full kode. Søk for å finne de andre.*

## Skriv ut lokasjonsetiketter

En etikett lar en håndskanner finne en lokasjon: den har en strekkode, lokasjonens
**fulle kode** med store bokstaver og **navnet** dens. Trykk **Skriv ut etiketter** på
siden for et lagersted eller en lokasjon (det krever bare **Se lagersteder**):

- på et lagersted: **Etiketter for** **Alle lokasjoner på lagerstedet**, eller **Valgte
  lokasjoner** — velg dem i **Lokasjoner**, med søk på kode eller navn;
- på en lokasjon: den lokasjonen, og **Ta med alle lokasjoner under denne** om du vil
  ha hele grenen.

Velg **Etikettark** — **Avery L7160 – 21 etiketter per A4** eller **Avery L7163 – 14
etiketter per A4** — og trykk **Last ned PDF**. Skriv ut PDF-en i 100 % på arkene.
Etikettene kommer i rekkefølge etter full kode; høyst 1000 om gangen (*Høyst 1000
etiketter kan skrives ut om gangen.*).

Strekkoden har lokasjonens etikettkode — `LOC` og et tall — ikke den fulle koden, så en
etikett kan fortsatt skannes etter at lokasjonen har fått nytt navn, ny kode eller er
flyttet, og med ethvert tastaturoppsett. Skriv den ut på nytt bare når teksten på den
skal endres.

## Motta varer

Trykk **Nytt varemottak** i **Oversikt**, **Motta her** på en lokasjon eller **Motta**
på en vare (det krever **Motta varer**, `inventory:receipts-manage`). Velg
**Lagersted** og **Motta til**, lokasjonen varene skal til; eventuelt **Leverandør**,
**Leverandørens referanse** — for eksempel følgeseddelens nummer — og et **Notat**.
Legg så til linjene med [linjeredigeringen](#linjeredigeringen): hver linjes
**Lokasjon** er *Som varemottaket* med mindre du velger en annen. Trykk **Bokfør
varemottak**.

En tjeneste kan ikke mottas, og heller ikke en vare der produktet er utgått.

## Juster beholdning

Trykk **Ny justering** i **Oversikt**, **Juster** på en lokasjon eller **Juster** på en
vare (det krever **Justere beholdning**, `inventory:stock-adjust`). Velg **Lagersted**
og en **Årsak** — **Korrigering**, **Skadet**, **Tapt**, **Funnet**, **Inngående
beholdning** eller **Annet** — og et **Notat** om du vil. For hver linje velger du
varen, **Lokasjon** og måten:

- **Endre med** legger til eller trekker fra: `5` legger fem til, `-2` tar to bort;
- **Sett til** sier hva som ligger der nå: `12` gjør det til tolv, uansett hva det var.

En **Sett til**-linje som allerede stemmer, tas ikke med; stemmer alle linjene, bokføres
ingenting. Trykk **Bokfør justering**.

En justering er måten du retter det som ble talt feil, skriver av det som gikk i
stykker, og legger inn inngående beholdning (**Inngående beholdning**).

## Flytt beholdning

Trykk **Ny flytting** i **Oversikt**, eller **Flytt herfra** på en lokasjon (det krever
**Justere beholdning**). Velg **Lagersted**; for hver linje varen, **Fra**, **Til** og
antallet. Begge lokasjonene må være på samme lagersted, og ikke den samme. Trykk
**Bokfør flytting**.

## Linjeredigeringen

De tre skjemaene registrerer linjer på samme måte:

- **Skann en vare**: skann eller skriv et varenummer eller en strekkode og trykk Enter.
  En ny vare gir en ny linje; en vare som alt står der, telles én til på sin linje.
  **Legg til linje** legger til en tom linje, der du søker etter varen på varenummer,
  navn eller strekkode.
- **Antall** er i varens egen enhet, med opptil tre desimaler — 2,5 meter, 0,125 kg.
- Når **Lagersted** endres, tømmes lokasjonene du alt har valgt: en bevegelse holder
  seg på ett lagersted.
- **I pakninger**: se [Registrer antall i pakninger](#registrer-antall-i-pakninger).
- Når serveren avviser, **bokføres ingenting**: hver avvist linje merkes med rødt og
  årsaken — *Ikke nok beholdning på lokasjonen.*, *Lokasjonen er arkivert.* og
  lignende — og skjemaet sier hvor mange linjer som ble avvist. Rett dem og bokfør igjen:
  etter en avvisning er neste forsøk en ny bokføring.
- **Et nytt forsøk bokfører aldri to ganger.** Svikter forbindelsen — *Fikk ikke
  kontakt med serveren. Sjekk tilkoblingen og prøv igjen.* — eller svarer serveren med
  en feil, beholder skjemaet den samme bokføringen, så å bokføre igjen kan ikke bokføre
  den to ganger: Lager kjenner den igjen, og varene mottas én gang.
- **Allerede bokført.** Gikk en tidligere versjon av bokføringen faktisk gjennom, men
  svaret gikk tapt, og du endret skjemaet før du prøvde igjen, sier skjemaet **Allerede
  bokført** — *En tidligere versjon av denne bokføringen er allerede bokført; svaret på
  den kan ha gått tapt underveis. Sjekk bevegelsene for å se hva som ble bokført.* Følg
  **Sjekk lagerstedets bevegelser** for å se den. *Hvis den ikke er der, eller dette er
  ment som en ny bokføring, bokfør den som ny*: **Bokfør som ny** bokfører den med en gang
  som en egen bevegelse.

Etter bokføringen tømmes skjemaet for den neste, med lenken **Åpne bevegelse …** til det
du bokførte.

## Registrer antall i pakninger

Når en vare har **pakninger** i Produkter — en eske med 15, en europall med 40 esker
(se [Pakninger](/nb/user/products/#pakninger)) — viser hver av linjene dens i
linjeredigeringen en teller per pakning og et felt for **løst** antall i varens egen
enhet, med totalen under: 2 europaller, 3 esker og 4 løse av en vare i pcs blir
*= 1 249 pcs*. Skriv eller tell opp antallet av hver pakning, og legg resten i feltet
for løst antall.

- **Skanner du strekkoden til en pakning**, legges én av den pakningen til på varens
  linje — skann en pall tre ganger for tre paller.
- I en **justering** med **Endre med** velger du **Legg til** eller **Trekk fra**: det
  du registrerte, legges til eller trekkes fra det som ligger der. Med **Sett til** er
  det det som ligger der nå.
- Antallet regnes ut når bevegelsen bokføres, fra pakningene slik de er da, og
  bevegelsen tar vare på det du registrerte: siden dens viser det under **Registrert
  som** — *2 Euro pallet + 2 Box* — selv om pakningene endres senere.
- Ble en pakning fjernet i Produkter mens du skrev, avvises linjen: *Varen har ikke
  lenger denne forpakningen.*

**Å lese antall i pakninger.** Overalt der beholdning listes — **Innhold** på en
lokasjon, beholdningssiden for en vare, kortet **Beholdning** på produktsiden og
varetellingen — vises et antall av en vare med pakninger også fordelt, største pakning
først: 1 830 pcs med en pall på 600 og en eske på 15 blir *3 Euro pallet, 2 Box*, under
selve antallet.

## Tell varer

En **varetelling** sammenligner det som ligger i hyllene med det Lager sier, og
bokfører avvikene som én bevegelse. Det krever **Telle varer**
(`inventory:counts-manage`) og begge visningsrettighetene. Bevegelser kan fortsatt
bokføres mens en varetelling er åpen: varetellingen merker det.

### Åpne en varetelling

Åpne **Varetellinger** i sidemenyen og trykk **Åpne varetelling**, eller trykk **Tell**
på siden for et lagersted eller en lokasjon, eller velg **Start varetelling** i søket i
toppfeltet. Velg **Lagersted** og **Omfang** — **Hele lagerstedet**, eller en lokasjon,
som teller den og alt under den — og et **Notat** om du vil. Trykk **Åpne varetelling**.

Det som ligger i omfanget i det øyeblikket, fryses som **Forventet**. To varetellinger
kan ikke dekke samme sted samtidig: *En annen varetelling som pågår, dekker deler av
dette omfanget.* En varetelling av et tomt lagersted eller en tom lokasjon er helt i
orden — det er slik beholdning plasseres i hyllene første gang: alt du teller, legges
til.

**Varetellinger** lister varetellingene som **Telles** — velg **Alle statuser** eller
en annen status, eller et lagersted, for å se andre — nyeste først, med omfanget, hvem
som åpnet den og når, hvor mange linjer som er telt (*12 av 40 linjer telt*) og hvor
mange som er **Endret**.

### Tell

Siden for varetellingen, **Varetelling …**, åpner på fanen **Telling**:

1. **Skann en lokasjon** — etiketten, eller skriv den fulle koden — i **Skann en
   lokasjon eller en vare**. **Teller på** viser hvor du er, og **Bare denne
   lokasjonen** viser bare linjene der. En lokasjon utenfor varetellingen avvises:
   *… er ikke med i denne varetellingen.*
2. **Skann hver vare** på den. En skanning finner varens linje og setter markøren i
   feltet **Telt**: skriv det du teller, i pakninger når varen har det. Strekkoden til
   en eske eller en pall teller én eske eller pall på linjen, så å skanne hver eske
   teller dem. En vare ingen ventet der, får en ny linje merket **Lagt til**. Skanner du
   en vare før noen lokasjon, står det *Skann en lokasjon først, deretter varene på
   den.*
3. Uten skanner skriver du i **Telt** på hver linje.

En varetelling av et helt lagersted kan ha tusenvis av linjer. Siden tegner bare linjene
nær det du ser, og resten etter hvert som du ruller, så den holder seg rask; linjen til en
skannet vare rulles fram uansett hvor den ligger, og **Tab** går videre til neste linje
som før.

Hver linje viser **Forventet**, **Nå** — det som ligger der nå —, **Telt** og
**Differanse**. En linje der beholdningen er endret siden varetellingen ble åpnet, er
merket **Endret**. Det du skriver, **lagres av seg selv** et øyeblikk etter at du
stopper: siden sier **Lagrer …**, så **Alle endringer er lagret**; **Ulagrede
endringer** og **Ikke lagret** kommer med **Lagre nå**. Går du fra siden, lagres det
du har telt først; mislykkes lagringen, blir du spurt om du vil **Forlat uten å lagre** eller
**Fortsett å telle**, og lukker du fanen med tellinger som ikke er lagret, spør nettleseren. Flere kan telle samme varetelling
samtidig, på hver sine hyller.

### Gå gjennom og bokfør

Fanen **Gjennomgang** viser **Differanser mot beholdningen nå** — det som er telt, mot
det som ligger der akkurat nå — eller *Ingenting avviker: bokføringen endrer ingen
beholdning.*

- **Endrede linjer**: det ble mottatt, flyttet eller justert beholdning på dem mens du
  talte. Bokføringen teller dem mot beholdningen nå, ikke mot det som var forventet;
  kryss av for **Bokfør de endrede linjene mot beholdningen nå** for å godta det, eller
  tell dem på nytt først. **Bokfør varetelling** venter til du gjør det.
- **Linjer som ikke er telt**: velg **Ikke telt: behold** for å la dem være som de er,
  eller **Ikke telt: sett til null** når det å ikke finne noe betyr at det er borte.

Trykk **Bokfør varetelling** og bekreft. Det som ennå ikke er lagret, lagres først.
Avvikene bokføres som én bevegelse av typen **Varetelling**, med lenke fra
varetellingen, og varetellingen lukkes — **Bokført**. En varetelling der alt stemte,
bokføres også, uten bevegelse: den viser at beholdningen var riktig. En bokført
varetelling kan ikke angres; en feil rettes med en justering.

**Avbryt varetelling** lukker den uten å bokføre noe — **Fortsett å telle** går tilbake.
Det som er telt, blir stående som historikk, og omfanget kan telles på nytt. En bokført
eller avbrutt varetelling vises bare for lesing.

Mens en varetelling er åpen, kan lokasjonene dens, og det som ligger over og under dem,
ikke arkiveres: *En varetelling pågår på lokasjonen eller noe under den. Bokfør eller
avbryt den først.* En varetelling som står åpen i mer enn en uke, vises på dashbordet.

## Importer inngående beholdning

For å plassere et lagersteds første beholdning fra et regneark åpner du **Importer
inngående beholdning** i sidemenyen (det krever **Justere beholdning** og begge
visningsrettighetene).

1. Trykk **Last ned mal** for en CSV-fil med overskriften `location,sku,quantity`, og
   fyll inn én rad per vare og lokasjon: lokasjonens **fulle kode** (`OSL-A-03-2`),
   varens **varenummer** og **antallet** i varens enhet — med opptil tre desimaler,
   med punktum eller komma, og uten tusenskille. Kolonnene kan stå i hvilken som helst
   rekkefølge, skilt med komma, semikolon eller tabulator; høyst 5000 rader. Har
   antallene **desimalkomma**, skiller du kolonnene med semikolon — slik Excel gjør når
   det lagrer CSV på norsk — eller setter antallet i anførselstegn (`"2,5"`): i en fil
   skilt med komma blir en `2,5` uten anførselstegn to celler, og raden avvises som
   **Ikke et gyldig antall**. En fil lagret fra Excel leses som den er.
2. Velg **Lagersted** og **CSV-fil**, og trykk **Prøvekjøring**. Ingenting bokføres:
   hver rad viser sitt **Resultat** — **OK**, **Lokasjonen finnes ikke på
   lagerstedet**, **Varenummeret finnes ikke**, **En tjeneste, holdes ikke på lager**
   eller **Ikke et gyldig antall**.
3. Når alle radene er OK (*Alle 120 rader er klare til bokføring.*), trykker du
   **Importer**. Radene bokføres som **én justering** med årsaken **Inngående
   beholdning**, og siden sier **Inngående beholdning bokført** med en lenke til den.
   Feiler en rad, bokføres ingenting: rett filen og prøv igjen.

Laster du opp **den samme filen igjen til samme lagersted**, bokføres ingenting nytt —
svaret er bevegelsen som alt er bokført — så en import kan ikke dobles ved et uhell. En
endret fil er en ny bokføring.

## Se hva som ligger på en lokasjon

Siden for en lokasjon viser stien øverst, den fulle koden med typen, og faktaene:
lagersted, kode, full kode og status. **Lokasjoner under** lister lokasjonene under den.
**Innhold** (med **Se beholdning**) lister hver vare som ligger på lokasjonen **og alt
under den** — varenummer, produkt, lokasjon og beholdning, fordelt i pakninger der varen
har det — og **Bare denne lokasjonen** begrenser det til selve lokasjonen. **Bevegelser** viser de tjue siste som
berørte lokasjonen eller noe under den, og **Alle bevegelser** åpner hele listen
filtrert på den.

## Se en vares beholdning og bevegelser

Beholdningssiden for en vare — fra oppslaget, Beholdning-kortet på produktsiden eller
en bevegelse — viser varenummeret og produktet, samlet beholdning, hvert lagersted med
lokasjonene der varen ligger, det ordrene har [reservert](#reservert-og-disponibelt) der
og det som er disponibelt, og varens bevegelser, nyeste først. **Motta** og
**Juster** tilbys etter rettighetene dine. En vare som er slettet fra katalogen, viser
fortsatt historikken sin, merket *Slettet fra katalogen*.

## Bevegelser

**Bevegelser** lister alle justeringer, flyttinger, varemottak, varetellinger og uttak,
nyeste først. Et **Uttak** er varer tatt ut til en ordres leveranse; det viser ordrens
dokument som kilde og bokføres fra Ordrer, aldri her. Filtrer på **Lagersted**,
**Lokasjon** (som tar med alt under den), **Vare**,
**Type** (**Justering**, **Flytting**, **Varemottak**, **Varetelling**, **Uttak**) og **Bokført mellom**, to dager i
organisasjonens tidssone. **Last flere** henter neste side. **Varemottak** i sidemenyen
er den samme listen med bare varemottak.

Siden for en bevegelse viser typen, lagerstedet, når og av hvem den ble bokført,
årsaken eller leverandøren og leverandørens referanse, notatet, varetellingen den
bokførte, og linjene: **Varenummer**, **Produkt**, **Fra**, **Til** og **Antall**, med
**Registrert som** for en linje registrert i pakninger. En bevegelse redigeres eller
slettes aldri.

## Beholdning-kortet på produktsiden

Når Lager er i bruk og du har både **Se lagersteder** og **Se beholdning**, har et
produkts side i Produkter kortet **Beholdning**: samlet beholdning per enhet, hvert
lagersted som åpnes til lokasjonene der varen ligger — med full kode og navn — og, for
et produkt med flere varianter, det samme for hvert varenummer, med **Beholdning og
bevegelser for …** til beholdningssiden. Er ingenting på lager, står det **Ingen
beholdning**, og **Motta** eller **Juster** tilbys etter det du har lov til. En tjeneste
har ikke noe Beholdning-kort. Se [produktsiden](/nb/user/products/#produktsiden).

En variant som fortsatt har beholdning, kan ikke slettes i Produkter, og et produkt med
beholdning kan ikke gjøres om til en tjeneste: flytt beholdningen eller juster den til
null her først. Det samme gjelder en variant som ordrer har reservert: lever eller
kanseller de ordrene først.

## Reservert og disponibelt

Når en ordre bekreftes i Ordrer, blir varene på den **reservert** på ordrens lagersted,
så to ordrer ikke kan love bort den samme siste varen. Under hvert lagersteds antall på
Beholdning-kortet og på en vares beholdningsside ser du da *Reservert … · disponibelt …*:

- **Reservert** er det åpne ordrer holder av på lagerstedet;
- **Disponibelt** er beholdningen på lagerstedet, summert over alle lokasjonene, minus
  det som er reservert. Det er det en ny ordre fortsatt kan reservere.

En ordres leveranse tar varene ut fra hyllene de plukkes fra og bruker opp
reservasjonen. Kanselleres ordren, eller reduseres en linje, gis reservasjonen tilbake.

Dine egne justeringer, flyttinger og varetellinger bokføres alltid, også når de tar
beholdningen under det som er reservert: de retter opp det som faktisk ligger der.
Disponibelt blir da negativt og vises i rødt, med varselet *Mer er reservert enn det som
ligger her*. Motta mer, eller be den som håndterer ordrene om å redusere eller kansellere
dem.

Bare lagersteder som **leverer ordrer**, tar imot reservasjoner
([opprett et lagersted](#opprett-et-lagersted)).

## Dashbordet

**Hjem** har et **Lager**-kort — *Hold oversikt over hva som ligger hvor* — for alle med
en av visningsrettighetene; trykk det for å åpne Lager. Med begge har tallraden et
Lager-kort: **Lagerlinjer**, antallet par av vare og lokasjon som har beholdning, med
ukens varemottak ved siden av (*3 varemottak denne uken*), der uken begynner på mandag.

Med **Telle varer** og begge visningsrettighetene viser **Trenger oppfølging** **En
varetelling har vært åpen lenge** for hver varetelling som fortsatt telles en uke etter
at den ble åpnet; trykk på den for å åpne varetellingen, og bokfør eller avbryt den.
Søket i toppfeltet tilbyr **Start varetelling** til de samme.

## Hvem ser hva

| Hva | Se lagersteder | Se beholdning | Administrere lagersteder | Justere beholdning | Motta varer | Telle varer |
| --- | --- | --- | --- | --- | --- | --- |
| Flisen og **Oversikt** med oppslaget — én av visningsrettighetene er nok | ✓ | ✓ | – | – | – | – |
| Lagerstedene, lokasjonene deres og siden for en lokasjon; lokasjoner i oppslaget og **Finn lokasjon eller vare**; **Skriv ut etiketter** | ✓ | – | – | – | – | – |
| Antall: **Innhold**, beholdningssiden for en vare, **Bevegelser** og **Varemottak**, **Beholdning**-kortet på produktsiden, kolonnen **Beholdning** i produktlisten, tallene på dashbordet, varer i oppslaget | ✓ | ✓ | – | – | – | – |
| **Nytt lagersted**, **Ny lokasjon**, **Legg til mange**, **Rediger**, **Arkiver**, **Gjenåpne**, **Slett** | ✓ | – | ✓ | – | – | – |
| **Ny justering**, **Juster**, **Ny flytting**, **Flytt herfra** — i Oversikt, på en lokasjon, en vare, bevegelsene og produktets Beholdning-kort; **Importer inngående beholdning** | ✓ | ✓ | – | ✓ | – | – |
| **Nytt varemottak**, **Motta her**, **Motta** — der de enn vises | ✓ | ✓ | – | – | ✓ | – |
| **Varetellinger**, **Åpne varetelling**, **Tell**, **Start varetelling**, telling, bokføring og avbrytelse; **En varetelling har vært åpen lenge** på dashbordet | ✓ | ✓ | – | – | – | ✓ |

Hver av de andre radene krever hver rettighet den krysser av: knappene som bokfører og
teller, vises bare for den som har begge visningsrettighetene **og** skriverettigheten.
Å skrive ut etiketter krever bare **Se lagersteder**: en etikett viser ikke noe siden for
lokasjonen ikke viser. Ingen av de seks rettighetene er sensitive. Hele tabellen står
under [rettighetene](/en/reference/inventory/#permissions) i referansen.
