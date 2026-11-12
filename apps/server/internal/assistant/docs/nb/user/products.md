---
title: Produkter
description: Katalogen over varer og tjenester, kategorier, mva-kategorier, priser, pakninger og bilder, og kolonnen Beholdning i produktlisten.
sidebar:
  order: 60
sources:
  - apps/products/frontend
---

Appen **Produkter** er katalogen over alt organisasjonen din selger: fysiske varer og
utførte tjenester, hver med sine priser og sin mva-sats. Sidemenyen har tre områder:
**Produkter**, **Kategorier** og **Avgiftskategorier**. Hvert område vises bare til den
som har rettigheten til det, så sidemenyen din kan være kortere enn en kollegas — se
[Rettigheter](#rettigheter) nederst på siden. Reglene bak hvert skjermbilde står i
[referansen for Produkter](/en/reference/products/).

## Finn et produkt

Åpne **Produkter** i sidemenyen. Listen viser hvert produkt med navn, **SKU /
varianter**, **Type**, **Kategori**, **Status**, **Enhet**, **Mva-kategori** (med sats)
og **Gjeldende NOK-pris**; merket ved siden av overskriften er totalt antall. Listen er
delt i sider med 25 produkter om gangen.

Når organisasjonen bruker Lager og du har både `inventory:locations-view` og
`inventory:stock-view`, har listen én kolonne til, **Beholdning**: produktets
beholdning på alle lagersteder, alle variantene til sammen, i enheten dens. Den er tom
for en tjeneste. Se [Lager](/nb/user/inventory/).

- **Søk etter navn eller SKU...** snevrer inn listen mens du skriver.
- **Status** beholder bare produkter som er **Utkast**, **Aktiv** eller **Utgått**.
- **Kategori** beholder produktene i én kategori og i underkategoriene dens; **Uten
  kategori** beholder produktene som ikke har noen kategori i det hele tatt.

Filtrene er en del av sideadressen, så en filtrert liste kan bokmerkes eller deles.
Klikk på en rad for å åpne produktet; blyanten ytterst i raden åpner **Rediger
produkt** uten at du forlater listen. Når ingenting treffer, sier listen **Ingen
produkter funnet.**

Søkefeltet i toppfeltet (**Søk etter produkter...**) finner produkter etter navn eller
SKU fra hvor som helst i appen etter to tegn, og åpner det du velger.

## Opprett et produkt

Klikk **Nytt produkt** på produktlisten. Dialogen spør først hva du legger til, fordi
valget styrer hvilke detaljer produktet spør etter:

- **Varer** — en fysisk vare du har på lager, sender eller leverer ut.
- **Tjeneste** — arbeid eller tilgang du leverer, uten noe å sende.

Velg ett (du kan gå tilbake med **Endre**) og fyll inn:

- **Navn** — obligatorisk.
- **Beskrivelse** — fritekst, valgfri.
- **Kategori** — valgfri; listen viser hele hierarkiet, innrykket.
- **Mva-kategori** — obligatorisk; hver oppføring viser satsen sin. Se
  [Mva-kategorier](#mva-kategorier).
- **Status** — **Utkast** til du endrer den. Det er du som bestemmer når et produkt
  blir **Aktiv**.

Klikk **Opprett produkt**. Vantigo oppretter produktet med én standardvariant der SKU er
produktnavnet med store bokstaver og `-001` på slutten (`KONTORSTOL-001`), og bekrefter
med **Produkt opprettet**. SKU, enhet, kostnad og logistikkdetaljer ligger på denne
varianten og redigeres på produktsiden — se [Varianter og enheter](#varianter-og-enheter).

Et produkt kan ikke opprettes når den genererte SKU-en allerede finnes, siden SKU-er er
unike i hele bedriften; gi produktet et annet navn eller rediger SKU-en på det
eksisterende. Alle reglene står i [referansen](/en/reference/products/#domain-model).

## Rediger et produkt

Åpne produktet og klikk **Rediger produkt**, eller bruk blyanten i produktlisten.
Dialogen er den samme som ved opprettelse, med to tillegg: **Type** kan byttes mellom
**Varer** og **Tjeneste**, og **Status** mellom **Utkast**, **Aktiv** og **Utgått**.
Klikk **Lagre endringer**; bekreftelsen er **Produkt oppdatert**.

Redigering av et produkt endrer bare de felles detaljene; SKU, enhet, kostnad, logistikk
og priser hører til varianten og endres på produktsiden i stedet.

Når Lager er i bruk, kan et produkt av typen **Varer** som fortsatt har varianter på lager,
ikke bli en **Tjeneste**: lagringen sier **Produktet har beholdning** — *En variant av
produktet har fortsatt beholdning. Flytt eller juster beholdningen til null før du gjør
produktet til en tjeneste.* Flytt beholdningen eller juster den til null i
[Lager](/nb/user/inventory/) først. Det samme avslaget gjelder mens en variant er **under
transport mellom lagersteder** i en [overføring](/nb/user/inventory/#overfør-varer-mellom-lagersteder),
selv om ingenting er igjen i noen hylle: varene har forlatt ett lagersted og ennå ikke
kommet til det andre, og overføringen må fortsatt sette dem et sted. [Motta
overføringen](/nb/user/inventory/#motta-en-overføring) eller [avbryt
den](/nb/user/inventory/#avbryt-en-overføring) først. Å gjøre en tjeneste om til varer er
alltid tillatt.

## Produktsiden

Når du klikker på et produkt, åpnes siden dets, med statusmerket ved siden av navnet.
Kortet **Produktdetaljer** viser **SKU**, **Type**, **Kategori** (som hele stien, for
eksempel *Møbler / Stoler*), **Strekkode**, **Enhet**, **Standardkostnad**,
**Mva-kategori** med sats, **Gjeldende pris** i hver valuta som har en, og
beskrivelsen. For varer viser en **Logistikk**-del **Vekt** og **Dimensjoner (L×B×H)**,
eller sier at ingen er registrert. Under følger kortet [**Bilder**](#bilder), kortet
**Priser** og, når produktet har mer enn én variant, kortet **Varianter**.

Når organisasjonen bruker Lager og du har `inventory:locations-view` og
`inventory:stock-view`, ligger kortet **Beholdning** over **Priser**: totalen på lager, og
beholdningen per lagersted, som hver åpnes til lokasjonene der varen ligger — per variant
når produktet har flere. For en vare som holdes i partier, lister hver lokasjon partiene
sine med utløpsdato og status, hvert med lenke til siden for partiet, og et lagersted med
sperret eller utløpt beholdning sier *herav … sperret eller utløpt*. En tjeneste har ikke noe beholdningskort. Se
[Lager](/nb/user/inventory/).

## Varianter og enheter

En variant er det som faktisk selges: den bærer **SKU**, **Enhet**, **Enhetskostnad**
og sine egne priser. Et produkt har alltid minst én, og én per kombinasjon når det
selges i flere farger eller størrelser.

Når et produkt har mer enn én variant, lister kortet **Varianter** på produktsiden dem
opp med **Tilleggsverdier**, **Enhet**, **Kostnad** og **Priser**, og tilbyr **Legg til
variant**. Blyanten åpner **Rediger variant** og søppelbøtten fjerner en variant. Begge
dialogene spør etter:

- **SKU** — obligatorisk og unik i hele katalogen.
- **Tilleggsverdier** — det som skiller denne varianten fra de andre, skrevet som
  `Farge=Blå, Størrelse=M`.
- **Enhet** — hvordan produktet telles, for eksempel `pcs` eller `time`; dette er
  enheten som vises på produktet og brukes av andre moduler.
- **Enhetskostnad** — hva varen koster deg, valgfri.

Klikk **Lagre variant**. Vantigo nekter å endre en SKU når produktet ikke lenger er et
utkast, nekter en SKU eller strekkode som en annen variant allerede bruker, og nekter å
fjerne den siste varianten (**Et produkt må beholde minst én variant.**).

Søppelbøtten spør først: **Slett variant** — *Slette varianten {SKU}? Prisene slettes
sammen med den.* — og **Slett** fjerner varianten med prisene. En variant som fortsatt
ligger på lager, kan ikke slettes: appen sier **Varianten har fortsatt beholdning** —
*Flytt eller juster beholdningen til null før du sletter varianten.* Når du kan se
beholdning, lenker meldingen **Se hvor den ligger** til variantens beholdning i Lager. En
variant med lagerbevegelser i historikken, men ingenting på lager, kan slettes.

Det samme avslaget gjelder mens varianten er **under transport mellom lagersteder** i en
[overføring](/nb/user/inventory/#overfør-varer-mellom-lagersteder) — sendt fra ett
lagersted, ennå ikke mottatt på det andre — selv når den ikke ligger i noen hylle: å
slette den ville etterlate overføringen med varer den ikke kunne sette noe sted.
Beholdningssiden i Lager viser den som *+… innkommende* under lagerstedet den skal til.
[Motta overføringen](/nb/user/inventory/#motta-en-overføring) eller [avbryt
den](/nb/user/inventory/#avbryt-en-overføring) først. En variant som bare står på et
**utkast** til overføring, holdes ikke: et utkast holder ingenting, og utkastet avvises
når det sendes.

Strekkoden, vekten og dimensjonene på produktsiden er også variantfelter, men appen
redigerer dem ikke; de settes gjennom API-et.

## Pakninger

En **pakning** er en eske, en kartong eller en pall varen kjøpes, lagres og telles i —
en eske med 15, en europall med 40 esker. Lager bruker dem: beholdning kan mottas,
flyttes og telles i esker og paller, strekkoden til en pakning kan skannes, og
beholdningen vises fordelt i dem. Se
[Registrer antall i pakninger](/nb/user/inventory/#registrer-antall-i-pakninger).

Pakninger finnes bare for **varer**. På et produkt med én variant ligger tabellen
**Pakninger** i kortet **Produktdetaljer**, under **Logistikk**; på et produkt med
flere er det én tabell per variant, **Pakninger for {SKU}**. Tabellen viser hver
paknings **Navn**, **Type**, hva den **Inneholder**, **Strekkode (GTIN)** og
**Dimensjoner (L×B×H)**.

Trykk **Legg til pakning** og fyll inn:

- **Navn** — opptil 40 tegn, som *Eske* eller *Europall*.
- **Type** — **Innerpakning**, **Kartong**, **Pall** eller **Annet**.
- Hva den inneholder — ett av to:
  - **Et antall i {enhet}** — for en eske med 15 pcs, **Antall** 15;
  - **Andre pakninger** — en **Pakning** av samme vare og **Antall pakninger**, minst
    2: en europall med 40 esker. Antallet regnes ut for deg, og **Inneholder** viser
    det: *40 × Eske (600 pcs)*.
- **Strekkode (GTIN)** — valgfri; ingen annen vare eller pakning kan ha den samme.
- **Lengde (cm)**, **Bredde (cm)**, **Høyde (cm)** og **Vekt (kg)** — valgfrie.

Trykk **Lagre**. Blyanten åpner **Rediger pakning**; søppelbøtten spør **Slett
pakning** — *Slette pakningen {navn}? Lagerbeholdningen påvirkes ikke.* En vare har
høyst **ti** pakninger: **Legg til pakning** er grået ut ved ti.

**Endrer du en eske, endres hver pall som er bygd av den**: la esken inneholde 12 i
stedet for 15, og pallen med 40 esker inneholder 480. En endring av en pakning **endrer
aldri beholdningen**, som alltid holdes i varens egen enhet; bevegelser som alt er
bokført, beholder pakningene slik de var da de ble bokført.

Lagringen avvises, med appens ord:

- *En annen variant eller pakning har allerede denne strekkoden.*
- *En pakning kan ikke inneholde en pakning som er bygd av den.* — en pall med esker
  kan ikke bli en del av en eske.
- *En annen pakning er bygd av denne. Endre eller slett den pakningen først.* — når du
  sletter en eske en pall er bygd av.

Å legge til og endre pakninger krever `products:variants-manage`.

## Bilder

Kortet **Bilder** på produktsiden holder bildene av produktet: de som kan vises til en
kunde. Det er det samme kortet for varer og tjenester, og det ligger under
**Produktdetaljer**.

For å legge til bilder slipper du JPEG- eller PNG-filer på kortet, eller trykker på
filvelgeren (**Legg til bilder**) og velger dem; flere samtidig går fint. En fil må være
en **JPEG eller PNG** på høyst **10 MiB** og minst 200 × 200 piksler. Vantigo beholder
ikke filen slik den ble sendt: bildet snus rett etter orienteringen kameraet registrerte,
krympes til høyst 2048 piksler på den lengste siden, og lagres på nytt **uten noen
kameradata** — verken sted, enhet eller tidspunkt når en kunde. Fargeprofilen (ICC)
fjernes sammen med resten, så et bilde med bredt fargerom kan se litt mindre mettet ut. En liten versjon lages til
miniatyrbildene på kortet.

Hvert bilde vises som et miniatyrbilde, i den rekkefølgen en kunde vil se dem, med:

- **Flytt til venstre** og **Flytt til høyre**, som endrer rekkefølgen (det første kan
  ikke flyttes til venstre, det siste ikke til høyre);
- en stjerne: den fylte merker **Hovedbilde**; trykk på stjernen på et annet bilde
  (**Gjør dette til hovedbildet**) for å flytte den. Det første bildet du legger til er
  hovedbilde, og når du sletter hovedbildet overtar det neste;
- **Alternativ tekst** — hva bildet viser, for en kunde som ikke kan se det, opptil 250
  tegn; den lagres når du forlater feltet;
- **Vis i Kommunikasjon** — på som standard: bildet kan vises til en kunde i en melding;
- **Vis i markedsføring** — for markedsføring, for eksempel annonser — ingenting leser
  dette ennå;
- søppelkassen, **Slett bilde**, som spør først og fjerner bildet fra produktet for godt.

En fil avvises med appens ord: *{name} er ikke et JPEG- eller PNG-bilde.*, *{name} er større
enn 10 MiB.*, og fra Vantigo selv *Bildet må være minst 200 × 200 piksler.*, *Bildet er
større enn 10 MiB, eller har mer enn 40 megapiksler.*, *Bare JPEG- og PNG-bilder kan
lastes opp.* (WebP og HEIC godtas ikke), *Filen kunne ikke leses som et bilde.* og
*Bildelageret kunne ikke nås. Ingenting ble endret. Prøv igjen.* Å legge til, endre og
slette bilder krever `products:products-manage`; å se dem krever `products:products-view`.

## Priser

Kortet **Priser** lister prisradene til produktets første variant: **Valuta**,
**Beløp**, **Gyldig fra** og **Gyldig til**. Prisene er ekskludert merverdiavgift;
mva-en følger produktets mva-kategori. En rad uten datoer er **Uten sluttdato** —
grunnprisen — og en rad med et gyldighetsintervall er en kampanjepris, som vinner over
grunnprisen så lenge den løper. Raden som gjelder akkurat nå bærer merket
**Gjeldende**. Et produkt uten rader sier **Ingen priser konfigurert.** og viser ingen
pris i listen.

Klikk **Legg til pris** og fyll inn **Valuta** (en ISO 4217-kode som `NOK`), **Beløp
(ekskl. mva)** og, for en kampanje, **Gyldig fra** og **Gyldig til** i din lokale tid
(**Gyldig til** er eksklusiv). La begge datoene stå tomme for grunnprisen. Klikk **Legg
til pris**.

Blyanten åpner **Rediger pris**, som advarer om at **Redigering omskriver denne
prisraden**: registrerte transaksjoner beholder prisen de lagret, men alt som leser
raden på nytt ser de nye verdiene, så for en planlagt endring bør du heller legge til
en ny rad med et gyldighetsintervall. Søppelbøtten ber deg bekrefte **Slett pris**, som
også fjerner raden fra historikken.

Vantigo nekter en rad som overlapper en annen av samme slag i samme valuta: to
NOK-grunnpriser, eller to NOK-kampanjer med overlappende intervaller. Én grunnpris og
én kampanje kan leve side om side.

## Arkiver et produkt

Et produkt slettes aldri, fordi andre moduler kan vise til det. Åpne i stedet produktet
og klikk **Arkiver**; bekreftelsen **Arkiver produkt** forklarer at produktet merkes som
utgått og ikke lenger kan selges, men beholdes for historisk referanse. Statusen blir
**Utgått** og knappen **Arkiver** forsvinner.

Utgåtte produkter blir stående i listen, der **Status**-filteret finner dem; for å hente
ett tilbake setter du **Status** til **Aktiv** i **Rediger produkt**.

## Kategorier

Åpne **Kategorier** i sidemenyen. Kategorier danner et hierarki; hvert produkt tilhører
høyst én kategori, og filtrering av produktlisten på en kategori inkluderer også
produktene i underkategoriene. Kortene øverst teller **Totalt antall kategorier**,
**Rotkategorier**, **Maksimal dybde** i hierarkiet, **Tomme kategorier** (ingen
produkter i undertreet) og **Produkter uten kategori**. Tabellen lister treet,
innrykket, med antall produkter i hver kategori og, når underkategoriene legger til
flere, antallet i undertreet. En kategori uten noe i undertreet er merket **Tom**.

For å opprette en klikker du **Ny kategori** og fyller inn **Navn** og, valgfritt,
**Overordnet kategori** — la den stå på **Ingen (rotkategori)** for en kategori på
øverste nivå. Klikk **Opprett kategori**. Blyanten åpner **Rediger kategori**, der en
kategori kan få nytt navn eller flyttes under en annen overordnet; søppelbøtten ber deg
bekrefte **Slett kategori**.

Vantigo nekter to kategorier med samme navn under samme overordnede, nekter å flytte en
kategori under en av sine egne etterkommere, og nekter å slette en kategori som fortsatt
har underkategorier eller produkter. Flytt eller omplasser dem først.

## Mva-kategorier

En mva-kategori er en sentralt konfigurert mva-sats. Hvert produkt peker på én i stedet
for å bære sin egen sats, så satsen som vises ved siden av et produkt er alltid
mva-kategoriens gjeldende sats, og endrer du den, endres den for alle produktene på én
gang. Priser legges inn uten mva; et salg legger til satsen til produktets mva-kategori
på salgstidspunktet.

Åpne **Avgiftskategorier** i sidemenyen (siden selv har overskriften
**Mva-kategorier**). Tabellen viser hver kategoris **Navn**, **Type** og **Sats**. Klikk
**Ny mva-kategori** og fyll inn:

- **Navn** — obligatorisk og unikt.
- **Type** — **Standard**, **Redusert**, **Null** eller **Fritatt**.
- **Sats (%)** — skriv `25` for en sats på 25 %; 0 til 100.

Klikk **Lagre**; bekreftelsen **Mva-kategori lagret** betyr at endringen er aktiv.
Blyanten redigerer en kategori, søppelbøtten sletter en. En mva-kategori som produkter
fortsatt bruker kan ikke slettes (**Denne mva-kategorien er i bruk og kan ikke
slettes.**): flytt de produktene til en annen mva-kategori først.

## Hvor produkter brukes

- **Prosjekter** — et prosjekts faktureringslinjer er knyttet til en produktvariant.
  Velgeren **Produktvariant** i en faktureringslinje søker bare i
  **Tjeneste**-produkter, etter navn eller SKU, og krever at du har tilgang til
  produkter; linjen viser deretter produktets navn, SKU og enhet til alle på
  prosjektet, også uten en produktrettighet. Se
  [faktureringslinjer](/en/reference/projects/#billing-lines-and-the-optional-products-dependency).
- **Lager** — holder beholdningen av **Varer**-variantene på lokasjonene på hvert
  lagersted, i enheten deres eller i [pakningene](#pakninger) deres, legger kortet
  **Beholdning** til produktsiden og kolonnen **Beholdning** til listen. Se
  [Lager](/nb/user/inventory/).
- **Fakturaer** — fakturalinjer skrives på selve fakturaen (beskrivelse, antall, enhet,
  enhetspris og mva-kode) og leser ikke katalogen. Se
  [referansen for Fakturaer](/en/reference/invoices/).

## Rettigheter

| Hva | Rettighet |
| --- | --- |
| Se området Produkter, listen og en produktside | `products:products-view`, `products:variants-view`, `products:pricing-view`, `products:categories-view` og `products:tax-categories-view` sammen |
| Opprette, redigere og arkivere produkter | `products:products-manage` (opprettelse krever også `products:variants-manage`) |
| Legge til, redigere og fjerne varianter | `products:variants-manage` (redigering og fjerning krever også `products:pricing-manage`) |
| Legge til, redigere og slette pakninger | `products:variants-manage` |
| Se et produkts bilder | `products:products-view` |
| Legge til, endre og slette bilder | `products:products-manage` |
| Kolonnen **Beholdning** | `inventory:locations-view` og `inventory:stock-view` |
| Legge til, redigere og slette priser | `products:pricing-manage` |
| Se området Kategorier | `products:categories-view` |
| Opprette, redigere og slette kategorier | `products:categories-manage` |
| Se området Avgiftskategorier | `products:tax-categories-view` |
| Opprette, redigere og slette mva-kategorier | `products:tax-categories-manage` |

Et skjermbilde du mangler rettighet til vises ikke i sidemenyen; en handling du mangler
rettighet til avvises når du prøver den. Se [referansen](/en/reference/products/#permissions).
