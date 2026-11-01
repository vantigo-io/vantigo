---
title: Produkter
description: Katalogen over varer og tjenester, kategorier, mva-kategorier og priser.
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

## Produktsiden

Når du klikker på et produkt, åpnes siden dets, med statusmerket ved siden av navnet.
Kortet **Produktdetaljer** viser **SKU**, **Type**, **Kategori** (som hele stien, for
eksempel *Møbler / Stoler*), **Strekkode**, **Enhet**, **Standardkostnad**,
**Mva-kategori** med sats, **Gjeldende pris** i hver valuta som har en, og
beskrivelsen. For varer viser en **Logistikk**-del **Vekt** og **Dimensjoner (L×B×H)**,
eller sier at ingen er registrert. Under følger kortet **Priser** og, når produktet har
mer enn én variant, kortet **Varianter**.

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

Strekkoden, vekten og dimensjonene på produktsiden er også variantfelter, men appen
redigerer dem ikke; de settes gjennom API-et.

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
- **Fakturaer** — fakturalinjer skrives på selve fakturaen (beskrivelse, antall, enhet,
  enhetspris og mva-kode) og leser ikke katalogen. Se
  [referansen for Fakturaer](/en/reference/invoices/).

## Rettigheter

| Hva | Rettighet |
| --- | --- |
| Se området Produkter, listen og en produktside | `products:products-view`, `products:variants-view`, `products:pricing-view`, `products:categories-view` og `products:tax-categories-view` sammen |
| Opprette, redigere og arkivere produkter | `products:products-manage` (opprettelse krever også `products:variants-manage`) |
| Legge til, redigere og fjerne varianter | `products:variants-manage` (redigering og fjerning krever også `products:pricing-manage`) |
| Legge til, redigere og slette priser | `products:pricing-manage` |
| Se området Kategorier | `products:categories-view` |
| Opprette, redigere og slette kategorier | `products:categories-manage` |
| Se området Avgiftskategorier | `products:tax-categories-view` |
| Opprette, redigere og slette mva-kategorier | `products:tax-categories-manage` |

Et skjermbilde du mangler rettighet til vises ikke i sidemenyen; en handling du mangler
rettighet til avvises når du prøver den. Se [referansen](/en/reference/products/#permissions).
