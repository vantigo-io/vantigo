---
title: Lager
description: Hva som ligger hvor — Lager-appen og sidemenyen, å finne en lokasjon eller en vare ved å skrive eller skanne koden, å opprette et lagersted, å legge til lokasjoner én og én og mange om gangen, å endre, flytte, arkivere og slette dem, å skrive ut lokasjonsetiketter, varemottak, justering og flytting av beholdning med linjeredigeringen, å registrere antall i pakninger, partier og serienumre — på varemottak, flyttinger og justeringer, listen Partier, siden for et parti, å sperre et parti og frigi det, kontroll ved mottak og partiene som venter på den, å melde avvik på et parti, filene til et parti, partiene som utløper snart, partietiketter, å spore et parti begge veier og eksportere sporingen, å blokkere et parti med partiene laget av det ved en tilbakekalling, GS1-128-etiketter — varetelling, partier medregnet, import av inngående beholdning med eller uten partier, overføring av varer mellom lagersteder — utkast, sending, mottak i deler, avslutning med avvik og avbrytelse — listen over overføringer og siden for en overføring, å angi en vares hovedlokasjon, bestillingspunkt og maksbeholdning på et lagersted, listen over lav beholdning — forslagene rundet opp til hele pakninger, å opprette en overføring fra den og å eksportere den — import av påfylling, hva som ligger på en lokasjon, en vares beholdning og bevegelser, listen Bevegelser og siden for en bevegelse, Beholdning-kortet på produktsiden, det ordrene har reservert og det som er disponibelt, hvilke lagersteder som leverer ordrer, kortet på dashbordet, oppfølgingspunktene og hurtighandlingene, og hvem som ser hva.
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
hyllene, og bokfører avvikene, og en **overføring** sender varer fra ett lagersted til et
annet, der de er **under transport** til de er mottatt. En **påfylling** sier, per vare og
lagersted, hvor varen hører hjemme — **hovedlokasjonen** — og når den blir lav —
**bestillingspunktet** — og **Lav beholdning** lister det som har blitt det. Varene kommer fra [Produkter](/nb/user/products/): bare
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
| **Partier** — partiene til varene som holdes i partier eller serienumre | `inventory:locations-view` og `inventory:stock-view` |
| **Utløper snart** — partiene på lager som snart utløper eller har utløpt | `inventory:locations-view` og `inventory:stock-view` |
| **Overføringer** — varer sendt mellom lagersteder | `inventory:locations-view` og `inventory:stock-view` |
| **Bevegelser** | `inventory:locations-view` og `inventory:stock-view` |
| **Varemottak** — listen over bevegelser med bare varemottak | `inventory:locations-view` og `inventory:stock-view` |
| **Varetellinger** — varetellingene | `inventory:counts-manage`, med begge visningsrettighetene |
| **Lav beholdning** — varene på eller under bestillingspunktet | `inventory:locations-view` og `inventory:stock-view` |
| **Importer inngående beholdning** — et lagersteds første beholdning, eller påfyllingen, fra et regneark | `inventory:stock-adjust`, med begge visningsrettighetene |

Det rettighetene dine ikke tillater, vises ikke i det hele tatt: en knapp du ikke ser, er
en rettighet du ikke har. Knappene som bokfører — varemottak, justeringer og flyttinger —
krever begge visningsrettighetene i tillegg til **Motta varer** eller **Justere
beholdning**; varetelling krever begge visningsrettighetene og **Telle varer**; å opprette, sende,
motta og avbryte overføringer krever begge visningsrettighetene og **Overføre varer**; å endre et parti, sperringene og filene krever begge visningsrettighetene og **Administrere partier**. Antall krever begge visningsrettighetene, fordi ethvert sted
beholdning ligger, er en lokasjon; **Se lagersteder** alene viser lagerstedene og
lokasjonene deres uten antall.

## Finn en lokasjon eller en vare

**Oversikt** åpner med feltet **Finn lokasjon eller vare**, klart til å skrive i. Skriv
eller skann ett av disse og trykk Enter:

- en lokasjons **fulle kode**, som `OSL-A-03-2`, med store eller små bokstaver;
- en lokasjons **etikettkode**, `LOC` og et tall;
- en vares **varenummer** eller **strekkode**, eller strekkoden til en av pakningene
  dens — en eske eller en pall åpner varen;
- en overførings kode, `TRF` og nummeret, slik den står under strekkoden på
  [følgeseddelen](#skriv-ut-følgeseddelen) — den åpner overføringen;
- et [partis](#partier-og-serienumre) nummer eller serienummer, med store eller små
  bokstaver, eller etikettkoden, `LOT` og et tall — det åpner siden for partiet. Et
  serienummer som også er en vares varenummer, viser begge.

Ett treff åpnes med en gang: en lokasjon åpner siden sin, en vare beholdningssiden sin.
Flere treff listes opp, hvert merket **Lokasjon**, **Vare**, **Overføring** eller **Parti**, så du kan velge. Ingen
treff gir *Ingenting passer med «…».*, og det du skrev, blir stående så du kan rette
det. Overføringer og partier, som varer, vises bare for dem som har begge visningsrettighetene.

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
  lokasjonen eller under den: flytt den eller juster den til null først. Det avvises
  også mens en [overføring](#overfør-varer-mellom-lagersteder) som fortsatt er under
  transport, ble sendt fra lokasjonen eller fra noe under den — *En linje under
  transport ble sendt fra denne lokasjonen; avbrytes den, kommer den tilbake hit. Motta
  eller avbryt den først.*
- **Gjenåpne** henter tilbake bare lokasjonen selv — ikke det som ligger under — og bare
  under en aktiv overordnet.
- **Slett** fjerner lokasjonen og alt under den for godt. Det avvises så snart det noen
  gang er bokført beholdning på noen av dem: arkiver i stedet.

Lagerstedets egen lokasjon — toppen av treet — kan ikke arkiveres eller slettes alene;
da arkiveres eller slettes lagerstedet. I lagerstedets **…**-meny: **Rediger lagersted**
(endrer du lagerstedets kode, endres hver full kode på det — trykte etiketter kan
fortsatt skannes), **Arkiver** (avvises mens lagerstedet har beholdning, og mens en
overføring til eller fra det fortsatt er under transport — *En overføring til eller fra
lagerstedet er fortsatt under transport. Motta eller avbryt den først.*; arkiverer hver
lokasjon på det), **Gjenåpne** (lagerstedet og dets egen lokasjon; lokasjonene under
gjenåpnes én og én) og **Slett** (bare for et lagersted det aldri er bokført en
bevegelse på, og som ingen overføring nevner — *En overføring nevner lagerstedet, så det
kan bare arkiveres.*). Et utkast stopper ikke arkiveringen; det kan da ikke lenger
sendes. Hver av dem ber deg bekrefte først.

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
**Lokasjon** er *Som varemottaket* med mindre du velger en annen — eller, for en vare
med en [hovedlokasjon](#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare) på
lagerstedet, den lokasjonen: linjen til en skannet eller valgt vare går dit til du velger
en annen. Trykk **Bokfør varemottak**.

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

## Partier og serienumre

En vare kan holdes i **partier** — en sats med eget nummer, som et leverandørparti med
stål eller en dags produksjon av melk — eller i **serienumre**, der hvert eksemplar er et
parti for seg og gjelder nøyaktig én. All beholdning av en slik vare ligger under partiet
sitt, så du ser hvilket parti som ligger i hvilken hylle, når det går ut, og hvor det ble
av. Partisporing slås på per vare i [Produkter](/nb/user/products/#spor-partier-og-serienumre),
mens varen ikke har beholdning; leveranser i Ordrer og produksjoner i
[Produksjon](/nb/user/manufacturing/#partier-og-serienumre) oppgir da også partiene sine.

Et parti har sitt **nummer** (1–20 bokstaver, sifre og `. _ / -`, ett per vare),
leverandørens **leverandørparti** — for eksempel smeltenummeret på et 3.1-sertifikat — en
**produksjonsdato**, en **best før**-dato og en **utløpsdato**. En vare kan gi et nytt
parti datoene selv: så mange dager etter produksjonsdatoen, eller etter dagen det mottas.
Et parti er **utløpt** fra dagen etter utløpsdatoen, i organisasjonens tidssone; best før
alene stopper aldri noe, den vises.

Et parti er **Tilgjengelig**, i **Karantene** eller **Blokkert**, ut fra **sperringene**
sine: et parti med en blokkering er blokkert, med en karantene er det i karantene.
Beholdning i et sperret eller utløpt parti blir liggende, men kan ikke leveres eller
brukes, og holdes utenfor [disponibelt](#reservert-og-disponibelt). Den kan fortsatt
flyttes — til en sperrehylle, for eksempel — telles, skrives av med en justering og
sendes tilbake.

### Partier på varemottak, flytting og justering

For en vare som holdes i partier, viser [linjeredigeringen](#linjeredigeringen) kolonnen
**Parti**:

- På et **varemottak** skriver du partiets nummer eller velger et varen allerede har på
  lagerstedet; står det tomt, lages et nummer (`L1001`, `L1002` …). Et nummer varen
  allerede har, legges til i det partiet. Kalenderknappen ved siden av åpner partiets
  **Produksjonsdato**, **Best før** og **Utløpsdato**: står de tomme, følger de av varens
  dager; for et parti som finnes, må en dato du oppgir stemme med partiets egen — *Partiet
  finnes med en annen dato. Utelat datoen for å legge til i det.* Datoene du oppgir, må stå
  i rekkefølge: en utløpsdato før produksjonsdatoen, eller en best før-dato etter
  utløpsdatoen, avvises — *Datoene er i feil rekkefølge*.
- For en vare som holdes i **serienumre**, tar en linje på varemottaket **Første
  serienummer** og **Antall**: sifrene på slutten telles opp med samme bredde — `SN0099`,
  `SN0100`, `SN0101` — og linjens antall er antallet serienumre. Et serienummer som
  allerede er på lager, avvises.
- På en **flytting** velger du først lokasjonen **Fra**: partikolonnen tilbyr da partiene
  som ligger der, hvert med utløpsdato og hva som ligger av det, og foreslår det første som
  kan brukes — det som går ut først, og av to som går ut samme dag det som ble mottatt
  først. Velger du en annen **Fra**-lokasjon, foreslås partiet der.
- På en **justering** velger du et av partiene på linjens lokasjon — foreslått på samme
  måte — eller skriver et nytt nummer for å legge beholdning under et nytt parti.

En linje for en sporet vare uten parti holdes tilbake — *Velg partiet på linjen* — og
serveren avviser et parti av en annen vare, en serienummerlinje på mer enn én og et
varemottak til et blokkert parti.

### Listen Partier

**Partier** i sidemenyen lister partiene etter vare og nummer: **Parti**, **Varenummer**,
**Produkt**, **Leverandørparti**, **Utløpsdato**, **Best før**, **Status** — *Utløpt* i rødt
etter utløpsdatoen — og **Beholdning**. **Søk** finner et partinummer, et leverandørparti
eller et varenummer; **Lagersted** teller bare det som ligger der; **Status** og **Årsak**
snevrer inn til de sperrede partiene, og hvorfor. **Bare med beholdning** er på med mindre
du vil se partier som ikke lenger har noe. **Last flere** henter neste side. **Spor**
ytterst på en rad åpner [sporingen](#spor-et-parti) av partiet. Søket i toppfeltet tilbyr
**Finn et parti**, som åpner denne listen, og **Spor et parti**, som åpner sporingen og
spør etter partiet.

### Siden for et parti

Trykk et partinummer — i listen, under en lokasjon på en vares beholdning, på en bevegelse
eller i oppslaget — for å åpne siden: varen, hva som ligger av det i alt,
leverandørpartiet, når det først ble mottatt, de tre datoene, bevegelsen som opprettet
det, og merknaden. **Sperringer** lister alle sperringer, de aktive først; **Hvor det
ligger** lister lagerstedene og lokasjonene som har det; **Bevegelser** bevegelseslinjene,
nyeste først, hver med lenke — de siste 200; er det flere, sier siden fra, og de eldre
finnes under **Bevegelser** i sidemenyen; **Filer** filene. **Spor** øverst åpner
[sporingen](#spor-et-parti) av partiet. Med **Administrere partier** endrer
**Rediger** leverandørpartiet, de tre datoene og merknaden; nummeret og varen endres
aldri, og en ny utløpsdato gjelder fra neste bokføring. Datoene må stå i rekkefølge —
ingen utløpsdato før produksjonsdatoen, ingen best før-dato etter utløpsdatoen.

Siden og listen oppdateres av seg selv når noen andre bokfører beholdning av partiet,
sperrer det eller legger til en fil.

### Sperr et parti og frigi det

Med **Administrere partier** setter **Karantene** på siden for et parti en sperring fra
partisiden: velg **Årsak** — **Kontroll**, **Avvik**, **Reklamasjon**, **Tilbakekalling**
eller **Annet** — skriv en **Merknad** og trykk **Sperr**. **Blokker** blokkerer partiet, og
partiene laget av det sammen med det: se
[Blokker et parti og partiene laget av det](#blokker-et-parti-og-partiene-laget-av-det).
**Frigi** på en av sidens egne sperringer spør *Hvorfor den frigis* og avslutter den;
partiet er tilgjengelig igjen når ingen sperring er igjen. Siden setter høyst én karantene og én
blokkering: mens dens egen karantene er aktiv, er **Karantene** nedtonet og siden sier
*Denne siden har allerede satt partiet i karantene*, og tilsvarende for **Blokker** — frigi
den sperringen for å sette en ny. En sperring som et dokument i en annen modul har satt —
et avvik i Produksjon, for eksempel — viser *Satt av Produksjon, frigis der*: den frigis på
det dokumentet, ikke her, og den hindrer ikke siden i å sette sin egen.

### Kontroller ved mottak

På siden til en vare som spores med partier eller serienumre, setter **Kontroller ved
mottak** det som kommer inn i karantene til noen har kontrollert det. Å slå det av og på
krever **Justere beholdning** (`inventory:stock-adjust`); en vare som ikke spores, tilbyr
det ikke.

Fra da av setter hvert varemottak av varen hvert parti det lager, eller legger til, under en
karantenesperre med årsaken **Kontroll**, navngitt med mottakets bevegelse. Mottaket sier
fra når det bokføres: *2 partier holdt for kontroll*. Mottatt i et parti som allerede har
beholdning, er hele partiet i karantene — også det som lå der fra før, med mengden som
ble godkjent tidligere — fordi et parti er én mengde: frigi det når de nye varene er
kontrollert. Mens partiet er sperret, blir det som er reservert av varen, stående
reservert, men en leveranse verken foreslår eller tar det sperrede partiet: den velger et
annet, eller venter.

Partiene som venter på kontroll, er kontrollkøen: forsiden lenker til dem som *Partier
venter på kontroll*, og de er listen **Partier** filtrert på årsaken **Kontroll**, med
eller uten beholdning — alle partier under en kontrollsperring, også et som en
tilbakekalling har blokkert i tillegg. På siden til et parti avslutter **Frigi** på mottakets sperring — med
**Administrere partier** — den som alle sidens egne sperringer.

### Meld avvik på et parti

Når Produksjon er i bruk, åpner **Meld avvik** på siden til et parti et nytt avvik på
partiet i Produksjon, som kan sette partiet i karantene. Sperringen vises på siden med
avviksnummeret, lenket til avviket, og oppheves ved å lukke avviket: se
[Avvik](/nb/user/manufacturing/#avvik).

### Filene til et parti

Med **Administrere partier** legger **Legg til fil** på siden for et parti til et
sertifikat, en analyse eller et bilde: en PDF, JPEG eller PNG på høyst 20 MB, og høyst 20
filer per parti. Trykk filnavnet eller nedlastingsknappen for å lagre filen — nettleseren
laster den ned i stedet for å åpne den — og **Fjern** for å ta den av partiet. Filer krever installasjonens
[objektlagring](/nb/admin/object-storage/); uten den står det *Fillagring er ikke satt opp i
denne installasjonen.*

### Utløper snart

**Utløper snart** i sidemenyen viser partiene på lager som utløper innen de neste 30
dagene, eller allerede har utløpt, med det som utløper først øverst: **Utløpsdato**,
**Dager igjen** — *8 dager*, eller *for 2 dager siden* i rødt når den er passert —,
**Parti**, **Varenummer**, **Produkt**, **Status** og **Beholdning**. Endre **Utløper innen
(dager)** — 1 til 365 — for å se lenger eller kortere fram; **Lagersted** teller bare det
som ligger på ett lagersted, og **Status** viser bare partiene som er tilgjengelige, i
karantene eller blokkert. Et parti som ikke har noe igjen, vises ikke. **Eksporter CSV**
laster ned de samme radene, `expiring-2026-09-12.csv`, med dager igjen som tall, negativt
når datoen er passert; **Skriv ut etiketter** skriver ut partienes etiketter. Listen viser
høyst 1000 partier: *Bare de første 1000 partiene vises. Velg færre dager eller et
lagersted.* Den krever begge visningsrettighetene.

### Skriv ut partietiketter

**Skriv ut etiketter** på siden for et parti, på listen **Partier** — for partiene den
viser, som dialogen sier fra om når listen har flere å hente (*Bare de 100 partiene som
vises, skrives ut. Bla ned i listen for å hente flere først.*) — og på **Utløper snart** laster ned en PDF med partietiketter, på de samme
Avery-arkene som [lokasjonsetikettene](#skriv-ut-lokasjonsetiketter): velg **Avery L7160**
(21 etiketter per A4) eller **Avery L7163** (14 per A4) og hvor mange **Etiketter per
parti**, og trykk **Last ned PDF**. Hver etikett har partiets `LOT`-kode som strekkode
øverst — `LOT1001`, nummeret i Lager, som aldri endres —, partinummeret med store tegn,
varenummeret og produktets navn, forkortet til én linje så det aldri går inn i datoen, og
utløpsdatoen (*EXP 2026-12-31*), eller best før-datoen (*BB …*) når partiet ikke har noen
utløpsdato. Et serienummer er et parti for
seg, så serienumre får en etikett hver. Å skanne en etikett i **Finn lokasjon eller
vare**, eller på en varetelling, finner partiet. Høyst 1000 etiketter om gangen.

**Etikett** velger hva etikettene bærer. **Intern** er etiketten over, for egne hyller.
**GS1-128** skriver ut en etikett som kundenes skannere leser: strekkoden har varens GTIN —
varens strekkode i [Produkter](/nb/user/products/), når den er en gyldig GTIN-8, -12, -13
eller -14 —, produksjonsdatoen, utløpsdatoen (eller best før-datoen når det ikke er noen)
og partinummeret, eller serienummeret, og det samme står i parentes under den:
*(01)04006381333931(17)261231(10)B1*. En vare uten gyldig GTIN kan ikke få en GS1-etikett:
partiene får den interne etiketten, merket *No GTIN — not a GS1 label*. Strekkoden går over
hele etiketten, så strekene blir så brede som etiketten tillater; likevel blir de smalere jo
lengre partinummeret er. Velg **Avery L7163** for etiketter kundene skal skanne: på **Avery
L7160** er strekene for de fleste partier smalere enn GS1s vanlige minimum, og en kundes
skanner leser dem kanskje ikke. Prøv en etikett på skanneren som skal lese den før du
skriver ut en hel bunke.

### Spor et parti

**Spor** på siden for et parti, på listen **Partier**, ved et parti funnet i oppslaget, på
fanen **Partier** på en produksjonsordre og på en leveranse i en ordre åpner sporingen av
partiet: produksjonene det gikk gjennom og hvor de fører, som et tre.

- **Hvor det gikk** følger partiet framover: partiene laget av det, og partiene laget av
  dem, hver med hva den brukte av partiet og hva den laget; og hvor hver vei ender — en
  **leveranse**, med leveranse- og ordrenummer og kunden, en justering eller telling ned,
  en overføring til et annet lagersted — *Sendt til* og lagerstedet —, eller en produksjon
  som ikke laget noe parti. Et parti sendt til et annet lagersted og levert derfra viser
  både overføringen og leveransen.
- **Hvor det kom fra** følger det bakover: partiene det ble laget av, ned til
  **varemottakene**, med leverandøren og leverandørens referanse. **Vis komponentene uten
  parti** viser også varene en produksjon brukte som ikke holdes i partier.

Hvert parti viser status og hva det har nå, og lenker til siden sin; hver produksjon
lenker til produksjonsordren. Et parti som nås to ganger — det samme melet i deigen og i
brødet — vises helt én gang; senere står det *Vist ovenfor*, som hopper dit og åpner en
skjult gren på veien. Pilen ved et
parti skjuler det som ligger under. Å ta en ferdigvare fra hverandre (en demontering)
avslutter veien: ferdigpartiet viser *Demontert av demontering …*, delene det ga tilbake
*Tilbakeført av demontering …*, og sporingen går ikke fra det ene inn i det andre.

Sporingen følger ti produksjoner dypt; et parti på det tiende nivået sier *Ikke sporet
videre: dybdegrensen* når mer ble laget av det. Den viser høyst 2 000 partier og 20 000
leveranser, mottak og andre rader, så et parti levert tusenvis av ganger kan likevel
spores; en større avvises — *Sporingen har mer enn 2 000 partier …* — så spor et parti
lenger ned i treet.

Hvem som fikk partiet, kommer fra Ordre: du ser leveransen, ordren og kundens nummer bare
om du kan bruke Ordre, og kundens navn bare om du også kan se kunder. Ellers sier
sporingen *Du kan ikke se ordrene, så en leveranse vises bare som bevegelsen sin*.

**Eksporter CSV** laster ned sporingen som et regneark, én rad per linje i treet — partiet,
varen, antallet, datoen, dokumentet og leverandøren eller kunden — og **Last ned PDF** som
et dokument som sier når det ble laget, av hvem og i hvilken retning: det Mattilsynet ber
om når de vil ha svaret innen timer.

### Blokker et parti og partiene laget av det

Når et parti ikke må gå ut — en tilbakekalling fra leverandøren, en reklamasjon fra en
kunde — viser **Blokker** på siden for et parti, med **Administrere partier**, først hva
partiet har nådd: **partiene laget av det, fortsatt på lager**, og hvem det er **levert
til**. Velg **Årsak** — **Tilbakekalling**, **Reklamasjon** eller **Annet** —, skriv en
**Merknad**, og la **Blokker også partiene laget av det** være krysset av for å blokkere dem
også, og trykk **Blokker**.

Blokkeringen settes på partiet og på hvert parti laget av det som fortsatt har
beholdning, i det øyeblikket den settes: et parti produsert etter at dialogen ble åpnet,
fanges også, og svaret lister hvert parti som ble blokkert — *Blokkerte 4 partier* — og
merker et slikt parti *Ny*. Partier som ikke har noe igjen, blokkeres ikke; sporingen og
leveransene sier hvor de gikk. En blokkering kan settes igjen for å fange partier laget
siden. Blokkeringen følger hver produksjon, uansett hvor dypt — ikke bare de ti nivåene
sporingen viser. Mer enn 2 000 partier blokkerer ingenting. Blir det stadig laget partier
av partiet mens det blokkeres, blokkeres ingenting, og dialogen sier *Det ble laget partier
av dette partiet mens det ble blokkert*: trykk **Blokker** igjen.

**Frigi** på partiets blokkering tilbyr **Frigi også blokkeringene den satte på partiene
laget av det**, krysset av: den løfter dem alle på en gang. Blokkeringen av et parti kan
også frigis på partiets egen side. En sperring noe annet har satt — et avviks karantene,
for eksempel — blir stående.

Å ringe kundene og ta varene tilbake gjøres fortsatt for hånd, ut fra leveranselisten
sporingen gir.

## Overfør varer mellom lagersteder

En **overføring** sender varer fra ett lagersted — **Fra lagersted** — til et annet —
**Til lagersted**: fra sentrallageret til en butikk, for eksempel. Den går gjennom disse
statusene:

- **Utkast** — under forberedelse. Et utkast holder ingenting: ingen beholdning settes
  av og ingenting flyttes, så det kan endres og slettes fritt.
- **Sendt** — varene har forlatt lokasjonene sine på fra-lagerstedet og er **under
  transport**: de ligger ikke i noen hylle, på ingen av lagerstedene, før de er mottatt.
- **Delvis mottatt** — noe har kommet og er satt på plass; resten er fortsatt under
  transport.
- **Mottatt** — alt har kommet, eller det som ikke kom, er
  [avsluttet med avvik](#avslutt-med-avvik).
- **Avbrutt** — overføringen ble kalt tilbake, og alt gikk tilbake dit det kom fra.

Overføringer krever **Overføre varer** (`inventory:transfers-manage`) i tillegg til begge
visningsrettighetene. Uten den kan du fortsatt åpne **Overføringer**, siden for en
overføring og følgeseddelen, men ikke opprette, sende, motta eller avbryte en.

### Lag et utkast

I **Overføringer** trykker du **Ny overføring** — *Send varer fra ett lagersted til et
annet. Et utkast holder ingenting før det er sendt.* Velg **Fra lagersted** og **Til
lagersted**, som må være forskjellige (*Fra- og til-lagerstedet må være forskjellige.*),
og om du vil, **Forventet ankomst**, en **Referanse** og et **Notat**. Legg så til
linjene med [linjeredigeringen](#linjeredigeringen): skann eller søk etter hver vare og
skriv antallet, i varens enhet eller [i pakninger](#registrer-antall-i-pakninger). En
vare kan stå på flere linjer — slik reiser varer fra to lokasjoner, eller to partier, i
én overføring.

For en vare som holdes i [partier](#partier-og-serienumre), tilbyr kolonnen **Parti**
varens partier på fra-lagerstedet, hvert med utløpsdato og hva som ligger av det. En linje
har ett parti: velg det nå, eller la det stå — *Velges ved sending* — så foreslår
sendingen ett. To partier av en vare står på to linjer.

Ved siden av hver linje viser to kolonner beholdningen på begge lagerstedene mens du
skriver:

- **På OSL: disponibelt** — det som er disponibelt av varen på fra-lagerstedet. En linje
  som ber om mer, merkes **For lite**: *Overføringen ber om mer enn det som er
  disponibelt på fra-lagerstedet. Å sende mer enn en lokasjon har, avvises.* Utkastet
  kan likevel lagres.
- **På BGN: disponibelt · innkommende** — det som er disponibelt på til-lagerstedet og,
  etter *+… innkommende*, det som allerede er på vei dit i andre overføringer; deretter,
  når varen har et [bestillingspunkt](#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare)
  der, *bestill ved ≤ …*, merket **Lav** mens den er lav.

En linje [laget fra listen over lav beholdning](#opprett-en-overføring-fra-listen) sier
også hvordan antallet ble funnet — *21 trengs, rundet opp til 2 kartonger à 15 = 30* —
til du endrer linjen.

Kan ikke beholdningen leses, sier skjemaet det, og utkastet kan likevel lagres.

Trykk **Lagre utkast**: *Utkast lagret*, og siden for overføringen åpnes med nummeret —
*Overføring 1042*. På siden for et utkast endrer **Rediger** det, og **Slett utkast**
spør *Slette utkastet til overføring 1042?* — *Utkastet og linjene slettes. Ingenting
ble sendt, så ingen beholdning endres.*

### Send en overføring

På siden for overføringen, eller rett fra skjemaet, trykker du **Send …** (skjemaet
lagrer først det du har endret). Dialogen **Send overføring 1042 til BGN** ber deg velge
lokasjonen på fra-lagerstedet hver linje plukkes fra: i kolonnen **Fra en lokasjon på
OSL** tilbyr hver linje lokasjonene på fra-lagerstedet der varen ligger, med hva som
ligger der, og starter på varens hovedlokasjon der, merket *(hovedlokasjon)*, når den har
noe av varen — ellers på den som har mest. En vare som ikke ligger noe sted på
lagerstedet, sier *Ingenting av den ligger på lagerstedet*.

For en vare som holdes i partier, tilbyr kolonnen **Parti** partiene som ligger på
linjens lokasjon, med utløpsdatoene, og starter på utkastets parti når det ligger der —
ellers på det første som kan brukes, det som utløper først, som ved en flytting. Velger du
en annen lokasjon, foreslås den lokasjonens parti. Et sperret eller utløpt parti kan
fortsatt sendes; det er merket. En linje for en sporet vare uten parti holdes tilbake —
*Varen spores i partier: oppgi partiet.* Partiet som sendes, følger linjen: i transitt er
per parti, mottaket legger det partiet i hyllen på til-lagerstedet, og en avbrytelse
legger det tilbake der det kom fra.

Dialogen varsler deg før du sender:

- *Lokasjonen har bare …; sendingen vil bli avvist.* — velg en annen lokasjon, eller del
  linjen i utkastet;
- *Partiet har bare … her: del linjen i utkastet for å sende resten fra et annet parti.*
  — en linje sender ett parti, så resten trenger en egen linje;
- *Bare … av … ligger på OSL; sendingen vil bli avvist.* — lagerstedet har mindre enn
  overføringen tar;
- *… av … er reservert for ordrer på OSL; sendingen gjør at de mangler.* — sendingen
  tar varer som [ordrer har reservert](#reservert-og-disponibelt). Dette er bare et
  varsel: sendingen går gjennom, og disponibelt på fra-lagerstedet blir negativt.

**Skriv ut følgeseddelen** er krysset av. Trykk **Send 3 linjer**: *Overføring sendt*.
Varene forlater de valgte lokasjonene med en gang, bokført som én bevegelse **Overføring
ut** på fra-lagerstedet, statusen blir **Sendt**, og følgeseddelen lastes ned. En sending
som avvises, sender ingenting; de avviste linjene merkes med årsaken. Å trykke på nytt
etter et brudd i forbindelsen sender aldri to ganger.

En vare som er slettet fra katalogen, eller der produktet er blitt en tjeneste, etter at
utkastet ble skrevet, avvises ved sending.

### Skriv ut følgeseddelen

Når en overføring er sendt, laster **Skriv ut følgeseddel** på siden ned følgeseddelen
som PDF, `folgeseddel-TRF-1042.pdf`, som følger varene: nummeret, de to lagerstedene med adressene
sine, når den ble sendt og av hvem, forventet ankomst, referansen, hver linje med
varenummer, navn, antall (og pakningene det ble registrert i), partiet og utløpsdatoen
når den har et, og lokasjonen den ble plukket fra, en linje for mottakerens signatur, og en strekkode for overføringens kode,
`TRF-1042`. Å skanne den strekkoden — i **Finn lokasjon eller vare** eller på
mottakssiden — åpner overføringen.

### Send og motta på en gang

Når de to lagerstedene ligger vegg i vegg — en butikk og bakrommet i en annen bygning,
for eksempel — og varene kommer fram idet du sender dem, trykker du **Send og motta …** i
stedet. Dialogen **Send og motta overføring 1042 på BGN** har, i tillegg til hver linjes
fra-lokasjon, en kolonne **Til en lokasjon på BGN** der hver linje settes på plass, med
varens hovedlokasjon der som utgangspunkt når den har en;
**Motta alle linjene på en lokasjon på BGN** setter samme lokasjon på hver linje. Hver
linje må ha en (*Velg hvor hver linje skal mottas.*). Trykk **Send og motta 3 linjer**:
*Overføring sendt og mottatt*. Varene forlater fra-lagerstedet og kommer til
til-lagerstedet i ett steg — en bevegelse **Overføring ut** og en **Overføring inn** — og
overføringen er **Mottatt** med en gang. **Skriv ut følgeseddelen** er ikke krysset av
her; kryss av om du vil ha følgeseddelen.

## Motta en overføring

Når varene kommer, åpner du overføringen — fra **Overføringer**, fra til-lagerstedets
**Innkommende**, eller ved å skanne følgeseddelens strekkode i **Finn lokasjon eller
vare** — og trykker **Motta …**. Den tilbys mens overføringen er **Sendt** eller **Delvis
mottatt**, til dem som har **Overføre varer**.

Siden **Motta overføring 1042 fra OSL** sier når overføringen ble sendt og av hvem, og
lister hver linje som fortsatt er under transport: **Vare**, **Sendt**, **Mottatt før**,
**Nå** og **Til en lokasjon på BGN**. Tell det som har kommet:

- **Skann** i feltet **Skann en vare eller følgeseddelen**: hver skanning av et
  varenummer eller en strekkode teller én til på linjen, og en eskes eller palls
  strekkode én eske eller pall. Å skanne strekkoden på en annen overførings følgeseddel
  åpner mottakssiden for den overføringen. En vare som ikke er på overføringen, sier
  *«…» er ikke på denne overføringen, eller ingenting av den er fortsatt under
  transport.*
- **Skriv** antallet i **Nå**, i varens enhet eller i pakningene.
- **Godta alt** fyller hver linje med alt som fortsatt er under transport — for en
  leveranse du har sjekket er komplett.

Hver linje settes på plass på varens hovedlokasjon på til-lagerstedet — eller, uten en,
på til-lagerstedets egen lokasjon — med mindre du velger en
annen i **Til en lokasjon på BGN**. En linje kan ikke ta mer enn det som fortsatt er
under transport på den (*Mer enn det som fortsatt er under transport på linjen.*). Under
linjene forteller *2 linjer vil fortsatt være under transport.* hva du ikke har talt
ennå.

Trykk **Motta**. Varene bokføres som én bevegelse **Overføring inn** på til-lagerstedet,
på lokasjonene du valgte, og siden for overføringen åpnes: *Overføring mottatt* når
ingenting er igjen under transport, *Overføring delvis mottatt* når noe er det.

### Motta i deler

En overføring kan mottas i så mange deler som varene kommer i: motta det som kom i dag,
og trykk **Motta …** igjen for neste del. Hver del telles bare mot det som fortsatt er
under transport.

To personer kan motta samme overføring samtidig — én skanner pallene, en annen de løse
eskene. Begge mottakene går gjennom, og hvert av dem vurderes mot det den andre
allerede har mottatt. Kom den andre først, og tellingene dine nå blir mer enn det som
fortsatt er under transport, merkes linjene som gikk over, ingenting av ditt bokføres,
og siden viser det som fortsatt er under transport med tellingene dine bevart: rett dem
og trykk **Motta** igjen. Brøt forbindelsen etter at et mottak var bokført, sier siden
*Et tidligere mottak fra denne siden er allerede bokført; svaret kan ha gått tapt på
veien.* og viser linjene slik de er nå — sjekk dem før du mottar igjen. Å trykke
**Motta** på nytt etter et brudd i forbindelsen mottar aldri to ganger.

## Avslutt med avvik

Når leveransen mangler noe og resten ikke kommer — tapt på veien, knust, aldri pakket —
mottar du det som kom og avslutter resten med avvik. På mottakssiden trykker du **Motta
og avslutt med avvik …**; den tilbys så lenge noe fortsatt ville være under transport
etter det du har talt.

Dialogen **Avslutt med avvik** lister det som ikke blir mottatt, linje for linje —
*3 ikke mottatt* — og spør **Hva skjedde?**: **Tapt under transport**, **Skadet**,
**Aldri pakket** eller **Annet**, med en **Merknad (valgfritt)**. *Det som gjenstår,
føres som ikke mottatt på overføring 1042 og legges ikke til noen beholdning.
Overføringen er da mottatt.* Trykk **Avslutt overføring 1042**: det du talte, mottas,
resten føres som **Ikke mottatt**, og overføringen er **Mottatt**.

Varene som mangler, forlot fra-lagerstedet allerede da overføringen ble sendt, så å
avslutte med avvik endrer ingen beholdning noe sted — det er ingenting å justere på noen
av lagerstedene. Avviket står på overføringen, under **Ikke mottatt** og **Avsluttet med
avvik** med årsaken og merknaden, og på varens beholdningsside under
[Avvik på overføringer](#se-en-vares-beholdning-og-bevegelser). Dukker varene likevel
opp senere, bokfører du dem med en [justering](#juster-beholdning) med årsaken
**Funnet**.

## Avbryt en overføring

En overføring som ble sendt ved en feil, kan kalles tilbake så lenge **ingenting av den
er mottatt eller ført som ikke mottatt**: trykk **Avbryt overføring** på siden. *Avbryte
overføring 1042? Alt som ble sendt, går tilbake til lokasjonene det kom fra på OSL.
Dette kan ikke angres.* Trykk **Avbryt overføring** for å bekrefte, eller **Behold
overføringen**. Hver linje går tilbake til lokasjonen den ble sendt fra, bokført som en
bevegelse **Overføring inn** på fra-lagerstedet, og overføringen er **Avbrutt**:
*Overføring avbrutt*.

Så snart noe av den er mottatt, kan overføringen ikke lenger avbrytes (*Bare en sendt
overføring der ingenting er mottatt eller mangler, kan avbrytes; slett et utkast i
stedet.*): motta resten, eller avslutt med avvik, og send tilbake det som ikke skulle
vært sendt, med en ny overføring. Et utkast avbrytes ikke, men slettes.

## Siden for en overføring

Siden for en overføring viser nummeret med statusen og, mens den er på vei, *Under
transport …*; fra-lagerstedet og til-lagerstedet, som hver lenker til lagerstedet sitt;
**Referanse**, **Forventet ankomst**, **Opprettet av** og **Sendt av** med når, og for en
overføring avsluttet med avvik, **Avsluttet med avvik** med årsaken og merknaden; og
notatet.

Linjene viser **Varenummer**, **Produkt**, **Fra** — lokasjonen hver linje ble plukket
fra —, **Sendt** (**Antall** på et utkast), **Mottatt**, **Ikke mottatt** og **Under
transport**, med **Registrert som** for en linje registrert i pakninger. Et varenummer
åpner varens beholdningsside.

**Historikk** lister, eldste først, hver sending, hvert mottak, hvert avvik og hver
avbrytelse: når, av hvem og på hvilket lagersted — *Sendt av Kari Nordmann fra OSL*,
*Mottatt av Ola Hansen på BGN*, *Avsluttet med avvik av Ola Hansen*, *Avbrutt av Kari
Nordmann, tilbake til OSL* —, hver linje den berørte med lokasjonen den gikk til eller
kom fra, og en lenke til bevegelsen den bokførte. Siden for bevegelsen lenker tilbake
til overføringen.

Knappene følger statusen: **Rediger**, **Send …**, **Send og motta …** og **Slett
utkast** på et utkast; **Motta …** mens den er sendt eller delvis mottatt; **Avbryt
overføring** så lenge ingenting av den er mottatt; **Skriv ut følgeseddel** når den er
sendt.

## Listen over overføringer

**Overføringer** i sidemenyen — *Varer sendt fra ett lagersted til et annet, under
transport til de er mottatt.* — lister overføringene, nyeste først: **Overføring** (med
referansen), **Fra lagersted**, **Til lagersted**, **Status**, **Linjer** og **Forventet
ankomst**. En overføring som har passert forventet ankomst, eller ble sendt for mer enn
7 dager siden uten en, og ennå ikke er mottatt, merkes **Forsinket**.

Filtrer på **Status** — **Åpne**, standardvalget, er utkast, sendte og delvis mottatte
overføringer sammen; eller én status; eller **Alle statuser** —, på **Fra lagersted** og
**Til lagersted**, og med **Søk**, et overføringsnummer eller et varenummer på en av
linjene (trykk Enter). **Last flere** henter neste side. **Ny overføring** åpner skjemaet
med de to lagerstedene du filtrerte på, allerede valgt.

Siden for et lagersted viser, med **Se beholdning**, **Innkommende** og **Utgående**:
antallet overføringer på vei til og fra lagerstedet — *2 åpne overføringer* — som hver
åpner listen filtrert på lagerstedet.

## Angi hovedlokasjon og bestillingspunkt for en vare

En **påfylling** er fire innstillinger for én vare på ett lagersted, hver av dem valgfri:

- **Hovedlokasjon** — *Der varen mottas og plukkes som standard. Varer kan fortsatt ligge
  hvor som helst.* Den er et forslag, aldri en regel: en varemottakslinje, sendingen og
  mottaket av en overføring og en ordres leveranse starter på den, og du kan alltid velge
  en annen.
- **Bestillingspunkt** — *Lav når tilgjengelig + innkommende er på eller under det.* Null
  eller mer, i varens enhet.
- **Maksbeholdning** — *Forslaget fyller opp til den.* Mer enn 0, og aldri lavere enn
  bestillingspunktet.
- **Multiplum** — *Et forslag rundes opp til et helt antall av den, for eksempel en
  partistørrelse.* Mer enn 0. Produksjons
  [påfyllingsforslag](/nb/user/manufacturing/#påfyllingsforslag) runder til det, og det
  gjør også listen over lav beholdning nedenfor. Alene holder det ikke på en påfylling:
  sett også en hovedlokasjon, et bestillingspunkt eller en maksbeholdning.

Åpne varens beholdningsside (fra oppslaget eller **Beholdning**-kortet på produktsiden).
Under hvert lagersted viser den lagerstedets **Bestillingspunkt**, **Maksbeholdning** og
**Multiplum** når de er satt, og **Lav** mens varen er lav der. Trykk **Endre påfylling** under lagerstedet
(det krever **Justere beholdning** og begge visningsrettighetene); et lagersted som ikke
har noe av varen, er også med, så du kan angi påfyllingen før det første varemottaket.
Dialogen **Påfylling på Sentrallageret** har de fire feltene; trykk **Lagre** — *Påfylling
lagret*. **Fjern** sletter påfyllingen — *Påfylling fjernet* — og varen har ikke lenger
noen hovedlokasjon, noe bestillingspunkt eller noen maksbeholdning på lagerstedet.

Dialogen avviser det den ikke kan ta vare på:

- en maksbeholdning på 0 — *Maksbeholdningen må være mer enn 0.*;
- et multiplum på 0 — *Multiplumet må være mer enn 0.*;
- en maksbeholdning lavere enn bestillingspunktet — *Maksbeholdningen kan ikke være lavere
  enn bestillingspunktet.*;
- ingenting satt — *Velg en hovedlokasjon, et bestillingspunkt eller en maksbeholdning —
  eller fjern påfyllingen.*;
- en hovedlokasjon som er arkivert i mellomtiden — *Hovedlokasjonen må være en aktiv
  lokasjon på dette lagerstedet.*

Har noen andre endret påfyllingen siden du åpnet den, lastes siden på nytt og sier
*Endret av noen andre*: gjør endringen din på nytt.

Å arkivere eller slette en lokasjon fjerner den som hovedlokasjon for alle varer; en
påfylling som ikke hadde noe annet satt, fjernes. Å arkivere et lagersted gjør det samme
for alle lokasjonene og beholder bestillingspunktene og maksbeholdningene, men et arkivert
lagersted kommer aldri på listen over lav beholdning.

## Listen over lav beholdning

**Lav beholdning** i sidemenyen — *Varer på eller under bestillingspunktet på hvert
lagersted, og hva som fyller dem opp.* — lister hver vare som har blitt lav på et aktivt
lagersted. En vare er **lav** på et lagersted når det som er **disponibelt** der —
beholdningen som kan brukes over alle lagerstedets lokasjoner, minus det ordrer har
[reservert](#reservert-og-disponibelt) — pluss det som er **innkommende** på overføringer
til lagerstedet, er på eller under bestillingspunktet. Varer som allerede er på vei,
teller med: så snart en overføring som bringer nok er sendt, forsvinner varen fra listen.

Hver rad viser **Vare**, **Lagersted** (når listen ikke er filtrert på ett),
**Disponibelt** (rødt når det er negativt), **Innkommende**, **Bestillingspunkt**,
**Maksbeholdning**, **Foreslått** antall og **Andre steder** — det hvert annet
aktivt lagersted der varen ligger eller er reservert har disponibelt, *Bergen butikk 12 ·
Trondheim butikk 0*. Et utkast til overføring til lagerstedet som allerede har varen,
vises under den som *Utkast 1043 åpent*. Filtrer på **Lagersted** (eller **Alle
lagersteder**) og søk etter et varenummer eller navn (trykk Enter); radene står etter
lagerstedskode og så varenummer, og **Last flere** henter neste side. Er ingenting lavt,
står det *Ingenting er på eller under bestillingspunktet.*

**Forslaget.** Listen foreslår det som fyller varen opp: til **maksbeholdningen**, eller,
uten en, til **én over bestillingspunktet**, slik at et mottak av forslaget tar raden ut
av listen — å fylle bare *til* bestillingspunktet ville latt den være lav. Det er alltid
minst 1. Når påfyllingen har et **multiplum**, rundes forslaget først opp til et helt
antall av det. Når varen har [pakninger](/nb/user/products/#pakninger), rundes forslaget **opp**
til et helt antall av den **minste** pakningen, og raden sier det under antallet:

- *21 trengs* — ingenting å runde av;
- *21 trengs, rundet opp til 2 kartonger à 15 = 30* — pakningen nevnt ved typen
  (innerpakning, kartong, pall);
- *25 trengs, rundet opp til 2 kartonger à 15 = 30, 5 over maksbeholdningen på 40* — når
  avrundingen går forbi maksbeholdningen, med hvor mye.

### Opprett en overføring fra listen

Med **Overføre varer** har hver rad en avkrysningsboks. Kryss av radene for **ett**
lagersted — radene for andre lagersteder kan ikke krysses av når én er det — og linjen
under sier *3 valgt*. I **Opprett overføring til Bergen butikk fra** velger du lagerstedet
det skal sendes fra; det starter på lagerstedet som har mest disponibelt av de valgte
varene. Trykk **Opprett overføring**: et utkast lages med det foreslåtte antallet av hver
vare — *Utkast lagret* — og åpnes, og hver linje sier hvordan antallet ble funnet til du
endrer den. Ingenting begrenses: utkastet viser fra-lagerstedets beholdning ved siden av
hver linje, og [sendingen](#send-en-overføring) avviser det en lokasjon ikke har.

Er et utkast til lagerstedet allerede åpent for en av de valgte varene, spør **Opprett
overføring** først — *Et utkast er allerede åpent*: *Et utkast til overføring til Bergen
butikk er allerede åpent for noen av disse varene. Opprette et til?* — og **Opprett et
nytt utkast** lager et til.

### Eksporter listen

**Eksporter CSV** laster ned listen slik den er filtrert — lagerstedet og søket — med
alle radene, som `low-stock-2026-11-09.csv`, med kolonnene `site` (koden), `sku`, `name`,
`barcode`, `available`, `incoming`, `reorder_point`, `maximum`, `needed`, `suggested`,
`packaging` (pakningens navn) og `packaging_count`. Kolonnene er skilt med semikolon og
antallene har desimalkomma, så Excel åpner den som den er. Det er ingen
leverandørkolonne: en leverandør er fritekst på et varemottak.

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

En vare som holdes i [partier](#partier-og-serienumre), har en linje per parti på hver
lokasjon, under hverandre, hver med partiets nummer — lenket til siden — og utløpsdato i
kolonnen **Parti**: tell hvert parti på sin egen linje. Å skanne et partis
[etikett](#skriv-ut-partietiketter) eller skrive nummeret går til det partiets linje, eller
legger til en; et partinummer som brukes av mer enn én vare, går til varen som allerede
er på lokasjonen, ellers spør siden **Hvilken vare?**. Å skanne varens egen strekkode
eller en forpaknings kan ikke si hvilket parti du holder, så når varen har linjer på
lokasjonen, spør siden **Hvilket parti av MILK-1?** — velg en linje, eller **Nytt parti
…**. En vare uten linjer der, eller **Nytt parti …**, får en ny linje med feltet
**Partinummer** og en kalenderknapp for partiets **Produksjonsdato**, **Best før** og
**Utløpsdato**. Skriv nummeret på pakken og trykk **Enter** eller gå videre: nummeret tas
først da, så et halvskrevet nummer blir aldri et parti, og et nummer som ikke kan leses,
sier *1–20 tegn: A–Z, a–z, 0–9 og . _ / -* med en gang. Et parti varen har, telles; et
nytt nummer lager et nytt parti når linjen lagres, med **bare datoene du ga** — et parti
funnet i en telling får ingen fra varens dager. Flere nye partier av én vare kan legges
til side om side. En linje serveren avviser — *Partinummeret kan ikke leses* eller datoer
i feil rekkefølge — markeres rødt med grunnen. Et serienummer telles nøyaktig 0 eller 1
(*Et serienummer gjelder nøyaktig én, og er på lager én gang.*). Bokføringen setter hvert
parti på hver lokasjon til det som ble telt.

Ble en vare holdt i partier etter at varetellingen ble åpnet, har linjene dens ikke noe
parti. De bokføres som de er når ingenting var forventet og ingenting telt; ellers avvises
bokføringen — *Varen spores i partier: oppgi partiet.* — og varen må telles på nytt i en
ny varetelling.

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
   Malen har også kolonnene `lot`, `supplier_lot`, `production_date`, `best_before` og
   `expiry`, for varer som holdes i [partier](#partier-og-serienumre): hver rad for en
   slik vare oppgir partiets nummer, og kan oppgi leverandørparti og datoer (`2026-12-31`
   eller `31.12.2026`); står datoene tomme, følger de av varens dager. Et nummer varen
   allerede har, legges til i det partiet; et nytt lager partiet, med importen som
   opphav. Et serienummer står på én rad, med antall 1: en rad til med det samme avvises
   allerede i prøvekjøringen. Partikolonnene kan utelates i en fil
   uten partier, og stå tomme på radene for andre varer.
2. Velg **Lagersted** og **CSV-fil**, og trykk **Prøvekjøring**. Ingenting bokføres:
   hver rad viser sitt **Resultat** — **OK**, **Lokasjonen finnes ikke på
   lagerstedet**, **Varenummeret finnes ikke**, **En tjeneste, holdes ikke på lager**,
   **Ikke et gyldig antall**, **Varen spores i partier: oppgi partiet**, **Varen spores
   ikke i partier**, **Partiet eller datoene kan ikke leses** eller **Et serienummer står
   på én rad, med antall 1** — og, når filen har partier, **Parti** for hver rad.
3. Når alle radene er OK (*Alle 120 rader er klare til bokføring.*), trykker du
   **Importer**. Radene bokføres som **én justering** med årsaken **Inngående
   beholdning**, og siden sier **Inngående beholdning bokført** med en lenke til den.
   Feiler en rad, bokføres ingenting: rett filen og prøv igjen.

Slik legges også beholdning som allerede står i hyllene, under partier: når en vare ikke
har noe på lager, lar du den holdes i partier i Produkter, og importerer så beholdningen
med partikolonnene.

Laster du opp **den samme filen igjen til samme lagersted**, bokføres ingenting nytt —
svaret er bevegelsen som alt er bokført — så en import kan ikke dobles ved et uhell. En
endret fil er en ny bokføring.

## Importer påfylling

For å angi mange [påfyllinger](#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare) på en
gang, på tvers av alle lagersteder, åpner du **Importer inngående beholdning** i
sidemenyen (det krever **Justere beholdning** og begge visningsrettighetene) og velger
**Påfylling** under **Hva som skal importeres**.

1. Trykk **Last ned mal** for `placements.csv`, med overskriften
   `sku,site,primary_location,reorder_point,maximum` — nøyaktig de fem kolonnene, i
   hvilken som helst rekkefølge, og en sjette, `multiple`, hvis du vil angi multiplumet. Fyll inn én rad per vare og lagersted: varens
   **varenummer**, lagerstedets **kode** (`OSL`), hovedlokasjonens **fulle kode**
   (`OSL-A-03-2`), **bestillingspunktet** og **maksbeholdningen** (og **multiplumet**), i
   varens enhet.
   Skilletegn, desimalkomma og grensen på 5000 rader er som for
   [inngående beholdning](#importer-inngående-beholdning).
2. **Hver rad setter alle feltene i filen.** En tom celle tømmer feltet — en fil uten
   kolonnen `multiple` lar hvert multiplum være som det er, og en påfylling den oppretter,
   får ikke noe — og en rad der hovedlokasjon, bestillingspunkt og
   maksbeholdning alle er tomme, **fjerner** påfyllingen. Nevner en fil samme vare og lagersted to ganger,
   gjelder den siste raden.
3. Velg **CSV-fil** og trykk **Prøvekjøring**. Ingenting skrives: hver rad viser
   **Varenummer**, **Lagersted**, **Hovedlokasjon**, **Bestillingspunkt**,
   **Maksbeholdning**, **Multiplum** og **Resultat** — **OK**, **Lagerstedet finnes ikke**,
   **Lagerstedet er arkivert**, **Ikke en aktiv lokasjon på lagerstedet**,
   **Varenummeret finnes ikke**, **En tjeneste, holdes ikke på lager**, **Ikke et gyldig
   antall** eller **Maksbeholdningen er lavere enn bestillingspunktet**.
4. Når alle radene er OK (*Alle 120 rader er klare til import.*), trykker du
   **Importer**: **Påfylling importert** — *118 satt, 2 fjernet.* Feiler en rad (*3 rader
   kan ikke importeres. Rett filen og prøv igjen.*), skrives ingenting.

Laster du opp **den samme filen igjen**, skrives ingenting nytt, så en import kan ikke
dobles ved et uhell. Blir et lagersted eller en lokasjon i filen arkivert mellom
prøvekjøringen og importen, avvises importen og skriver ingenting: prøvekjør på nytt.

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
og det som er disponibelt, og varens bevegelser, nyeste først. Under et lagersted som
en [overføring](#overfør-varer-mellom-lagersteder) er på vei til med mer, sier
*+… innkommende* hvor mye som er på vei dit; et lagersted med varer på vei og ingenting
på lager ennå har også en rad. Varens
[hovedlokasjon](#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare) på et lagersted står
først blant lokasjonene, merket *hovedlokasjon*, og hvert lagersted viser
**Bestillingspunkt** og **Maksbeholdning**, **Lav** mens varen er lav der og, med
**Justere beholdning**, **Endre påfylling**. For en vare som holdes i partier, lister
hver lokasjon partiene sine under seg — nummer, utløpsdato og *Karantene*, *Blokkert*
eller *Utløpt* der det gjelder — og et lagersted med sperret eller utløpt beholdning sier
*herav … sperret eller utløpt* under antallet. **Motta** og **Juster** tilbys etter
rettighetene dine. En
vare som er slettet fra katalogen, viser fortsatt historikken sin, merket *Slettet fra
katalogen*.

**Avvik på overføringer** lister overføringene av varen som ble
[avsluttet med noe som ikke ble mottatt](#avslutt-med-avvik) — *Overføringer av denne
varen som ble avsluttet med noe som ikke ble mottatt.* — med hvor mye av varen som ikke
ble mottatt på hver; uten noen står det *Ingen overføring av denne varen har avvik.*

## Bevegelser

**Bevegelser** lister alle justeringer, flyttinger, varemottak, varetellinger, uttak,
overførings- og produksjonsbevegelser, nyeste først. Et **Uttak** er varer tatt ut til
en ordres leveranse; det viser ordrens dokument som kilde og bokføres fra Ordrer, aldri
her. En **Produksjon** er det en produksjonsordre forbrukte — komponenter tatt fra
lokasjonene sine — og produserte — varen lagt på sin lokasjon — i én bevegelse på ett
lagersted; den bokføres fra Produksjon, aldri her. En
**Overføring ut** er sendingen av en overføring, tatt fra fra-lagerstedets lokasjoner; en
**Overføring inn** er mottaket av en overføring på til-lagerstedet, eller avbrytelsen
som legger varene tilbake på fra-lagerstedet. Begge lenker til overføringen og bokføres
fra den, aldri her. Filtrer på **Lagersted**, **Lokasjon** (som tar med alt under den),
**Vare**, **Type** (**Justering**, **Flytting**, **Varemottak**, **Varetelling**,
**Uttak**, **Overføring ut**, **Overføring inn**, **Produksjon**) og **Bokført mellom**, to dager i
organisasjonens tidssone. **Last flere** henter neste side. **Varemottak** i sidemenyen
er den samme listen med bare varemottak.

Siden for en bevegelse viser typen, lagerstedet, når og av hvem den ble bokført,
årsaken eller leverandøren og leverandørens referanse, notatet, varetellingen den
bokførte eller overføringen den hører til (**Overføring**), kilden (**Kilde**) en annen
modul bokførte den for — en produksjons kilde lenker til produksjonsordren når
Produksjon er i bruk — og linjene: **Varenummer**, **Produkt**, **Fra**, **Til**, **Parti** for en vare som holdes i
partier — med lenke til partiet, og *Utløpt* der et utløpt parti ble tatt med vilje — og
**Antall**, med **Registrert som** for en linje registrert i pakninger. En bevegelse redigeres eller
slettes aldri.

## Beholdning-kortet på produktsiden

Når Lager er i bruk og du har både **Se lagersteder** og **Se beholdning**, har et
produkts side i Produkter kortet **Beholdning**: samlet beholdning per enhet, hvert
lagersted som åpnes til lokasjonene der varen ligger — med full kode og navn — og, for
et produkt med flere varianter, det samme for hvert varenummer, med **Beholdning og
bevegelser for …** til beholdningssiden. Varer på vei mellom lagersteder i en overføring
vises som *Under transport …* ved siden av totalen, og *+… innkommende* under
lagerstedet de skal til; de er ikke med i samlet beholdning, som er det som ligger i
hyllene. Under et lagersted der varen har et
[bestillingspunkt](#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare), sier *bestill
ved ≤ 10 stk* hva det er, i rødt mens varen er lav der. For en vare som holdes i partier,
lister hver lokasjon partiene sine — nummer, utløpsdato og status — med lenke til
partiet, og *herav … sperret eller utløpt* under lagerstedet sier hvor mye som ikke kan
brukes. Er ingenting på lager og ingenting på vei, står det **Ingen beholdning**, og
**Motta** eller **Juster** tilbys etter det du har lov til. En tjeneste
har ikke noe Beholdning-kort. Se [produktsiden](/nb/user/products/#produktsiden).

En variant som fortsatt har beholdning, kan ikke slettes i Produkter, og et produkt med
beholdning kan ikke gjøres om til en tjeneste: flytt beholdningen eller juster den til
null her først. Det samme gjelder en variant som ordrer har reservert: lever eller
kanseller de ordrene først — og en variant som er på vei mellom lagersteder i en
overføring: motta eller avbryt overføringen først.

## Reservert og disponibelt

Når en ordre bekreftes i Ordrer, blir varene på den **reservert** på ordrens lagersted,
så to ordrer ikke kan love bort den samme siste varen. Under hvert lagersteds antall på
Beholdning-kortet og på en vares beholdningsside ser du da *Reservert … · disponibelt …*:

- **Reservert** er det åpne ordrer holder av på lagerstedet;
- **Disponibelt** er beholdningen på lagerstedet, summert over alle lokasjonene, minus
  det som er reservert. Det er det en ny ordre fortsatt kan reservere.

For en vare som holdes i **partier**, teller disponibelt bare beholdning som kan brukes:
et parti som er sperret — i karantene eller blokkert — eller har passert utløpsdatoen,
blir stående i beholdningen, men holdes utenfor disponibelt — på Beholdning-kortet, på en
vares beholdningsside og i kolonnene i overføringsskjemaet likt — og dermed utenfor det en
ny ordre kan reservere og utenfor lista over lav beholdning. En vare som er merket lav på
beholdningssiden, står på lista over lav beholdning. En sperring på beholdning som
allerede er reservert, kan gjøre disponibelt negativt; reservasjonene blir stående.
Partisporing slås på per vare når leveranser og produksjoner kan registrere partier.

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
Lager-kort: **Lagerlinjer**, antallet par av vare og lokasjon som har beholdning — en
vare som ligger i flere partier på én lokasjon, telles én gang — med
ukens varemottak ved siden av (*3 varemottak denne uken*), der uken begynner på mandag.

Med **Telle varer** og begge visningsrettighetene viser **Trenger oppfølging** **En
varetelling har vært åpen lenge** for hver varetelling som fortsatt telles en uke etter
at den ble åpnet; trykk på den for å åpne varetellingen, og bokfør eller avbryt den.
Søket i toppfeltet tilbyr **Start varetelling** til de samme.

Med **Overføre varer** og begge visningsrettighetene viser **Trenger oppfølging** **En
overføring har passert forventet ankomst** for hver overføring som ennå ikke er mottatt,
der forventet ankomst er passert, eller som ble sendt for mer enn 7 dager siden uten en;
trykk på den for å åpne overføringen, og [motta den](#motta-en-overføring),
[avslutt med avvik](#avslutt-med-avvik) eller [avbryt den](#avbryt-en-overføring).

Med begge visningsrettighetene viser **Trenger oppfølging**, for hvert aktivt lagersted
med noe lavt, *4 varer på eller under bestillingspunktet på et lagersted*; trykk på den
for å åpne [listen over lav beholdning](#listen-over-lav-beholdning) filtrert på det
lagerstedet.

Med begge visningsrettighetene viser **Trenger oppfølging** også *3 partier på lager
utløper innen 14 dager* og *2 partier på lager har utløpt* når det finnes noen; trykk på
en av dem for å åpne [Utløper snart](#utløper-snart).

## Hvem ser hva

| Hva | Se lagersteder | Se beholdning | Administrere lagersteder | Justere beholdning | Motta varer | Telle varer | Overføre varer | Administrere partier |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Flisen og **Oversikt** med oppslaget — én av visningsrettighetene er nok | ✓ | ✓ | – | – | – | – | – | – |
| Lagerstedene, lokasjonene deres og siden for en lokasjon; lokasjoner i oppslaget og **Finn lokasjon eller vare**; **Skriv ut etiketter** | ✓ | – | – | – | – | – | – | – |
| Antall: **Innhold**, beholdningssiden for en vare, **Bevegelser** og **Varemottak**, **Beholdning**-kortet på produktsiden, kolonnen **Beholdning** i produktlisten, tallene på dashbordet, varer i oppslaget | ✓ | ✓ | – | – | – | – | – | – |
| **Nytt lagersted**, **Ny lokasjon**, **Legg til mange**, **Rediger**, **Arkiver**, **Gjenåpne**, **Slett** | ✓ | – | ✓ | – | – | – | – | – |
| **Lav beholdning**, **Eksporter CSV**; punktet om lav beholdning på dashbordet | ✓ | ✓ | – | – | – | – | – | – |
| **Ny justering**, **Juster**, **Ny flytting**, **Flytt herfra** — i Oversikt, på en lokasjon, en vare, bevegelsene og produktets Beholdning-kort; **Endre påfylling**; **Importer inngående beholdning**, begge slagene | ✓ | ✓ | – | ✓ | – | – | – | – |
| **Nytt varemottak**, **Motta her**, **Motta** — der de enn vises | ✓ | ✓ | – | – | ✓ | – | – | – |
| **Varetellinger**, **Åpne varetelling**, **Tell**, **Start varetelling**, telling, bokføring og avbrytelse; **En varetelling har vært åpen lenge** på dashbordet | ✓ | ✓ | – | – | – | ✓ | – | – |
| **Overføringer**, siden for en overføring og historikken, **Skriv ut følgeseddel**, overføringer i oppslaget, **Innkommende** og **Utgående** på et lagersted, **Avvik på overføringer** på en vare | ✓ | ✓ | – | – | – | – | – | – |
| **Ny overføring**, **Rediger**, **Lagre utkast**, **Send …**, **Send og motta …**, **Slett utkast**, **Motta …**, å avslutte med avvik, **Avbryt overføring**; **En overføring har passert forventet ankomst** på dashbordet; **Opprett overføring** på listen over lav beholdning | ✓ | ✓ | – | – | – | – | ✓ | – |
| **Partier**, siden for et parti med sperringene, hvor det ligger, bevegelsene og filene; partier i oppslaget og **Finn et parti** | ✓ | ✓ | – | – | – | – | – | – |
| **Spor**, **Spor et parti**, **Eksporter CSV** og **Last ned PDF** der — ordren og kunden på en leveranse krever også Ordre, kundens navn Kunder | ✓ | ✓ | – | – | – | – | – | – |
| **Utløper snart**, **Eksporter CSV** der, **Skriv ut etiketter** for partier; partiene som utløper og har utløpt på dashbordet | ✓ | ✓ | – | – | – | – | – | – |
| **Rediger** på et parti, **Karantene**, **Blokker** med partiene laget av det, **Frigi**, **Legg til fil**, **Fjern** | ✓ | ✓ | – | – | – | – | – | ✓ |

Hver av de andre radene krever hver rettighet den krysser av: knappene som bokfører og
teller, vises bare for den som har begge visningsrettighetene **og** skriverettigheten.
Å skrive ut etiketter krever bare **Se lagersteder**: en etikett viser ikke noe siden for
lokasjonen ikke viser. Å motta en overføring krever **Overføre varer**, ikke **Motta
varer**: den flytter dine egne varer mellom dine lagersteder og er ikke et mottak fra en
leverandør. Å angi en påfylling krever **Justere beholdning**: den er en innstilling for
beholdningen, aldri beholdning i seg selv. Å endre et parti krever **Administrere partier**: en sperring hindrer at partiet leveres eller brukes. Ingen av de ni rettighetene er sensitive. Hele tabellen står
under [rettighetene](/en/reference/inventory/#permissions) i referansen.
