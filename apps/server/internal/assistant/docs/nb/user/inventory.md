---
title: Lager
description: Hva som ligger hvor — Lager-appen og sidemenyen, å finne en lokasjon eller en vare ved å skrive eller skanne koden, å opprette et lagersted, å legge til lokasjoner én og én og mange om gangen, å endre, flytte, arkivere og slette dem, varemottak, justering og flytting av beholdning med linjeredigeringen, hva som ligger på en lokasjon, en vares beholdning og bevegelser, listen Bevegelser og siden for en bevegelse, Beholdning-kortet på produktsiden, kortet på dashbordet og handlingen Finn lokasjon eller vare, og hvem som ser hva.
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
feil rettes med en ny justering. Varene kommer fra [Produkter](/nb/user/products/): bare
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

Det rettighetene dine ikke tillater, vises ikke i det hele tatt: en knapp du ikke ser, er
en rettighet du ikke har. Knappene som bokfører — varemottak, justeringer og flyttinger —
krever begge visningsrettighetene i tillegg til **Motta varer** eller **Justere
beholdning**. Antall krever begge visningsrettighetene, fordi ethvert sted
beholdning ligger, er en lokasjon; **Se lagersteder** alene viser lagerstedene og
lokasjonene deres uten antall.

## Finn en lokasjon eller en vare

**Oversikt** åpner med feltet **Finn lokasjon eller vare**, klart til å skrive i. Skriv
eller skann ett av disse og trykk Enter:

- en lokasjons **fulle kode**, som `OSL-A-03-2`, med store eller små bokstaver;
- en lokasjons **etikettkode**, `LOC` og et tall;
- en vares **varenummer** eller **strekkode**.

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
- en adresse om du vil: **Adresse**, **Adresselinje 2**, **Postnummer**, **Poststed**
  og **Land** (to bokstaver, som NO).

Det nye lagerstedet åpnes på sin egen side. Et lagersted er også en lokasjon:
beholdning kan ligge på selve lagerstedet, som passer for en liten butikk uten hyller,
og flyttes ned senere når du legger til hyller.

I **Oversikt** vises hvert lagersted som et kort med typen, poststedet og antallet
lokasjoner; med **Se beholdning** også beholdningslinjene og *Siste varemottak …*.

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
lagersted med opptil 2000 lokasjoner, når du trykker Enter på et større.

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

## Se hva som ligger på en lokasjon

Siden for en lokasjon viser stien øverst, den fulle koden med typen, og faktaene:
lagersted, kode, full kode og status. **Lokasjoner under** lister lokasjonene under den.
**Innhold** (med **Se beholdning**) lister hver vare som ligger på lokasjonen **og alt
under den** — varenummer, produkt, lokasjon og beholdning — og **Bare denne
lokasjonen** begrenser det til selve lokasjonen. **Bevegelser** viser de tjue siste som
berørte lokasjonen eller noe under den, og **Alle bevegelser** åpner hele listen
filtrert på den.

## Se en vares beholdning og bevegelser

Beholdningssiden for en vare — fra oppslaget, Beholdning-kortet på produktsiden eller
en bevegelse — viser varenummeret og produktet, samlet beholdning, hvert lagersted med
lokasjonene der varen ligger, og varens bevegelser, nyeste først. **Motta** og
**Juster** tilbys etter rettighetene dine. En vare som er slettet fra katalogen, viser
fortsatt historikken sin, merket *Slettet fra katalogen*.

## Bevegelser

**Bevegelser** lister alle justeringer, flyttinger og varemottak, nyeste først. Filtrer
på **Lagersted**, **Lokasjon** (som tar med alt under den), **Vare**, **Type**
(**Justering**, **Flytting**, **Varemottak**) og **Bokført mellom**, to dager i
organisasjonens tidssone. **Last flere** henter neste side. **Varemottak** i sidemenyen
er den samme listen med bare varemottak.

Siden for en bevegelse viser typen, lagerstedet, når og av hvem den ble bokført,
årsaken eller leverandøren og leverandørens referanse, notatet og linjene:
**Varenummer**, **Produkt**, **Fra**, **Til** og **Antall**. En bevegelse redigeres
eller slettes aldri.

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
null her først.

## Dashbordet

**Hjem** har et **Lager**-kort — *Hold oversikt over hva som ligger hvor* — for alle med
en av visningsrettighetene; trykk det for å åpne Lager. Med begge har tallraden et
Lager-kort: **Lagerlinjer**, antallet par av vare og lokasjon som har beholdning, med
ukens varemottak ved siden av (*3 varemottak denne uken*), der uken begynner på mandag.
Lager legger ikke til noe i **Trenger oppfølging** ennå.

## Hvem ser hva

| Hva | Se lagersteder | Se beholdning | Administrere lagersteder | Justere beholdning | Motta varer |
| --- | --- | --- | --- | --- | --- |
| Flisen og **Oversikt** med oppslaget — én av visningsrettighetene er nok | ✓ | ✓ | – | – | – |
| Lagerstedene, lokasjonene deres og siden for en lokasjon; lokasjoner i oppslaget og **Finn lokasjon eller vare** | ✓ | – | – | – | – |
| Antall: **Innhold**, beholdningssiden for en vare, **Bevegelser** og **Varemottak**, **Beholdning**-kortet på produktsiden, tallene på dashbordet, varer i oppslaget | ✓ | ✓ | – | – | – |
| **Nytt lagersted**, **Ny lokasjon**, **Legg til mange**, **Rediger**, **Arkiver**, **Gjenåpne**, **Slett** | ✓ | – | ✓ | – | – |
| **Ny justering**, **Juster**, **Ny flytting**, **Flytt herfra** — i Oversikt, på en lokasjon, en vare, bevegelsene og produktets Beholdning-kort | ✓ | ✓ | – | ✓ | – |
| **Nytt varemottak**, **Motta her**, **Motta** — der de enn vises | ✓ | ✓ | – | – | ✓ |

Hver av de andre radene krever hver rettighet den krysser av: knappene som bokfører,
vises bare for den som har begge visningsrettighetene **og** skriverettigheten. En sjette rettighet, **Telle varer**
(`inventory:counts-manage`), er for varetelling, som kommer senere. Ingen av de seks er
sensitive. Hele tabellen står under
[rettighetene](/en/reference/inventory/#permissions) i referansen.
