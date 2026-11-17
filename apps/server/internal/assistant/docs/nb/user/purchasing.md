---
title: Innkjøp
description: Innkjøp-appen — listen over innkjøpsordrer og filtrene; lag en innkjøpsordre til en leverandør og et lagersted, med produktlinjer fylt ut fra leverandørvarene og tekstlinjer; send den på e-post, marker den som sendt eller send den på nytt; registrer leverandørens bekreftelse; endre en sendt ordre og les historikken; avslutt en linje eller hele ordren, gjenåpne den, kanseller den eller kopier den; PDF-en; leverandørene og hva hver selger — leverandørvarer, foretrukket leverandør og import av prisliste med CSV-kolonnene; innstillingene; dashbordkortet og produktsidens Leverandører-kort; og det som kommer senere — varemottak, forslag og fakturaavstemming.
sidebar:
  order: 68
sources:
  - apps/purchasing/frontend
  - apps/host/frontend/src/routes/purchasing
---

I Innkjøp-appen bestiller du varer og tjenester fra leverandørene dine. En
**innkjøpsordre** sier hva du kjøper av hvem, til hvilken pris og når du venter det; en
**leverandørvare** sier hva en leverandør selger av et av produktene dine, til hvilken
pris, i hvilken innkjøpsenhet og med hvilken leveringstid.

En innkjøpsordre flytter verken beholdning eller regnskap. I denne versjonen bestiller og
følger du opp; å ta varene inn på lager, forslag fra lav beholdning og å avstemme
leverandørens faktura mot ordren kommer senere ([Det som kommer senere](#det-som-kommer-senere)).
Innkjøp kjøper fra leverandørene i registeret i [Regnskap](/nb/user/accounting/#leverandører)
og bestiller produktene i [Produkter](/nb/user/products/) til et lagersted i
[Lager](/nb/user/inventory/), så en installasjon med Innkjøp har alle tre.

## Appen og sidemenyen

Sidemenyen har **Innkjøpsordrer** og **Leverandører** for alle med **Bruke Innkjøp**, og
**Innstillinger** for dem som administrerer innstillingene for Innkjøp. Hva du kan gjøre,
avhenger av rettighetene dine: å skrive ordrer, å sende dem og å vedlikeholde
leverandørvarene er egne rettigheter, så en kollega kan se knapper du ikke ser
([Rettigheter](#rettigheter)). Alle med **Bruke Innkjøp** ser prisene.

## Ordrelisten

**Innkjøpsordrer** lister ordrene med de nyeste først, med nummer, leverandør, status,
lagersted, forventet dato og sum uten mva. Et utkast har ikke nummer ennå. En ordre kan
være:

- **Utkast** — under arbeid; den har ikke nummer og kan slettes.
- **Sendt** — sendt til leverandøren, på e-post eller markert som sendt; den har fått
  nummeret sitt.
- **Delvis mottatt** og **Mottatt** — nås når varer tas imot mot ordren, noe som kommer
  med varemottaket.
- **Avsluttet** — avsluttet med noe igjen som ikke kommer.
- **Kansellert** — avlyst før noe ble mottatt.

To merker vises ved statusen:

- **Forsinket** — ordren er sendt, og en åpen linjes forventede dato (linjens egen, ellers
  ordrens) er før i dag.
- **Ubekreftet** — ordren ble sendt for mer enn tre virkedager siden (mandag til fredag,
  helligdager unntatt), og ingen bekreftelse fra leverandøren er registrert: verken
  leverandørens referanse eller bekreftet dato.

Avgrens listen med søket (ordrenummeret, leverandørens navn eller leverandørens
referanse), statusbrikkene — **Utkast**, **Sendt**, **Delvis mottatt**, **Mottatt**,
**Avsluttet**, **Kansellert**, gjerne flere sammen — og bryterne **Forsinket**,
**Ubekreftet** og **Mine** (ordrene der du er **Vår referanse**).

## Lag en innkjøpsordre

1. I **Innkjøpsordrer** velger du **Ny innkjøpsordre**.
2. Velg **Leverandør**. Bare en aktiv leverandør kan få en ny ordre.
3. Kontroller **Lagersted** — lagerstedet varene leveres til. Det er innstillingenes
   standard lagersted, ellers det eneste aktive lagerstedet; med flere og ingen standard
   velger du ett.
4. Velg **Opprett utkastet**. Ordren åpnes som et utkast.

Fra leverandøren tar ordren **Valuta** og **Språk på ordren** — norsk når leverandørens
land er Norge eller ikke oppgitt, ellers engelsk. Du er **Vår referanse** (innkjøperen)
med mindre du velger en kollega som kan administrere innkjøpsordrer.

### Hodet

På et utkast velger du **Rediger** for å endre leverandøren, lagerstedet, valutaen — til en linje har fått en
pris —, språket, **Ordredato**, **Forventet dato**, **Vår referanse**, **Leverandørens
kontakt**, en **Merknad på ordren**, som skrives ut på PDF-en, og en **Intern merknad**,
som aldri skrives ut.

**Leveringsadresse** er adressen innstillingene gir lagerstedet, eller, når de ikke gir
noen, selskapets adresse med lagerstedets navn. Et utkast viser den slik den er nå; når
ordren sendes, festes den på ordren, sammen med leverandørens navn og adresse.

### Linjer

En ordre har opptil 500 linjer, av to slag:

- **Produktlinje** (**Legg til produkt**) — søk i katalogen etter SKU eller navn, eller bare i det leverandøren
  har leverandørvarer for. Linjen tar fra leverandørvaren det du lar stå tomt:
  **leverandørens varenummer**, prisen — når leverandørvarens pris er i ordrens valuta —,
  **innkjøpsenheten** så lenge produktet fortsatt har den, og **Forventet dato**,
  ordredatoen pluss leverandørvarens leveringstid. Du fører antallet i innkjøpsenheten, og
  linjen viser det i produktets enhet også: 3 esker à 15 er 45 stykk. **Rabatt %** er en
  prosent av prisen. Linjens beløp er antall × pris, minus rabatten, avrundet til to
  desimaler.
- **Tekstlinje** (**Legg til tekstlinje**) — noe utenfor katalogen, som frakt eller en tjeneste: en beskrivelse, et
  antall, en enhet og en pris.

Et antall under leverandørvarens **Minste antall**, eller som ikke er et helt antall av
dens **Antall per bestilling (multippel)**, vises som en advarsel; ordren kan likevel
lagres og sendes. Et produkt som er **Utgått**, kan ikke settes på en ny linje; et
tjenesteprodukt kan bestilles.

Alle beløp er uten mva., i ordrens valuta, og PDF-en sier det.

## Send ordren

**Send** krever **Sende innkjøpsordrer**, minst én linje og at e-post er satt opp: en
e-postkanal i [innstillingene](#innstillinger) for Innkjøp og Kommunikasjon i bruk.

1. På utkastet velger du **Send**.
2. Kontroller mottakeren — leverandørens e-post i registeret, eller en annen adresse for
   denne ordren — og legg gjerne til kopimottakere, et emne og en melding til
   leverandøren.
3. Send. Ordren får neste nummer i serien, PDF-en lagres, og PDF-en sendes til
   leverandøren med en kort følge-e-post på ordrens språk som ber dem oppgi ordrenummeret
   på fakturaen.

Tar ikke e-postleverandøren imot e-posten, er ordren likevel sendt og har fått nummeret
sitt: appen sier at e-posten ikke kunne sendes, og e-posten prøves på nytt av seg selv en
stund. Ordresiden sier at den siste e-posten feilet og sendes på nytt; **Send på nytt**
sender en ny kopi.

**Marker som sendt** gjør det samme uten e-post — for en ordre lagt inn på telefon eller i
leverandørens nettbutikk: ordren får nummeret sitt, og PDF-en lagres. Når e-post ikke er
satt opp, er det **Marker som sendt** ordresiden tilbyr.

Å velge **Send** to ganger, eller et brudd i forbindelsen fulgt av et nytt forsøk, sender
ikke ordren to ganger.

## Etter at ordren er sendt

En sendt ordre kan fortsatt endres, og hver endring skrives til **Historikk** med hvem som
gjorde den og når:

- nye linjer;
- en linjes antall, pris, rabatt og forventede dato — men ikke produktet, slaget eller
  innkjøpsenheten;
- ordredatoen, forventet dato, vår referanse, leverandørens kontakt, merknadene og språket;
- lagerstedet, så lenge ingenting er mottatt.

Leverandøren og valutaen blir som de ble sendt. En avsluttet linje endres ikke, og en
avsluttet, mottatt eller kansellert ordre redigeres ikke.

På en sendt ordre åpner **Rediger** ordren for disse endringene; **Lagre** skriver dem.

**Send på nytt** lagrer en ny PDF av ordren slik den er nå, merket **Revidert**, med neste
versjonsnummer, og sender den til leverandøren; fjern haken for e-post for bare å lagre
den. Ordrens **Historikk** har tre deler: **Endringer**, **PDF-er** — hver lagret PDF med
versjonen sin — og **E-poster**, hver med mottakeren og om den er **Sendt**, **I kø** for
et nytt forsøk eller **Feilet**.

## Registrer leverandørens bekreftelse

Når leverandøren bekrefter ordren, velger du **Registrer bekreftelse** på en sendt ordre
og fører inn **leverandørens referanse** (deres ordrenummer) og **Bekreftet dato** — for
hele ordren og, om leverandøren bekrefter linjer ulikt, per linje. Bekreftelsen er en
opplysning på ordren; den endrer ikke statusen. Den fjerner ordrens merke **Ubekreftet**.
Tøm feltene for å fjerne den.

## Avslutt en linje eller en ordre

- **Avslutt linje** avslutter én linje: det som er igjen på den, kommer ikke. Når ingen
  linje er åpen lenger, avsluttes ordren.
- **Avslutt** avslutter en sendt ordre med det som er igjen på den: hver åpen linje
  avsluttes, og ordren blir **Avsluttet**. Det som var igjen, teller ikke lenger som *i
  bestilling* i Lager.
- **Gjenåpne** åpner en avsluttet ordre igjen, med linjene avslutningen avsluttet; en linje
  du avsluttet for seg før, blir avsluttet.

Å avslutte krever **Administrere innkjøpsordrer**.

## Kanseller en ordre

**Kanseller ordre** avlyser en sendt ordre før noe er mottatt; et utkast slettes i stedet.
Når ordren ble sendt på e-post, får leverandøren kanselleringen — en PDF merket
**Kansellert** — med mindre du fjerner haken for **Send leverandøren kanselleringen**. En ordre som ble
markert som sendt, kanselleres uten e-post. Å kansellere krever **Sende innkjøpsordrer**.

## Slett et utkast

**Slett utkast** fjerner et utkast; det har ikke fått nummer, så ingenting blir igjen av
det. En sendt ordre kanselleres eller avsluttes i stedet. Å slette krever
**Administrere innkjøpsordrer**.

## Kopier en ordre

**Kopier** lager et nytt utkast fra en hvilken som helst ordre, til samme leverandør og
lagersted, i leverandørens valuta nå og med dagens priser fra leverandørvarene. Et produkt
som nå er **Utgått**, utelates; tekstlinjer kopieres som de er. Et kjøp du gjør igjen og
igjen, er en kopi av det forrige.

## PDF-en

PDF-en har selskapets navn og adresse, leverandørens, leveringsadressen, ordrenummeret og
datoen, vår referanse, linjene med antall, priser og beløp, summen, *Beløpene er uten
mva.*, en linje som ber leverandøren oppgi ordrenummeret på fakturaen, merknaden på ordren
og innstillingenes tekst nederst på ordren. Den er på norsk eller engelsk etter ordrens
språk.

På et utkast skriver **Forhåndsvis PDF** ut PDF-en slik den er nå, merket **Utkast**, og
lagrer ingenting. På en sendt ordre gir **Last ned PDF** PDF-en som ble lagret da den ble
sendt, eller sist sendt på nytt; en revidert er merket **Revidert**, en
kansellering **Kansellert**.

## Leverandører og leverandørvarene deres

**Leverandører** lister leverandørene i registeret i Regnskap, med hvor mange
leverandørvarer og åpne ordrer hver har. Siden for en leverandør viser oppføringen —
nummer, navn, organisasjonsnummer, adresse, e-post, valuta og betalingsbetingelser — uten
at den kan endres der, og leverandørvarene. **Åpne i
Regnskap** fører til siden for leverandøren i Regnskap, for dem med **Bruke Regnskap**.

Kan du vedlikeholde leverandørene — **Vedlikeholde leverandører**, eller **Bokføre bilag**
med **Bruke Regnskap** — legger **Ny leverandør** i listen til en leverandør i registeret
fra Innkjøp. **Rediger leverandør** på siden for en leverandør endrer den, og krever
**Bruke Regnskap** i tillegg. Uten **Bokføre bilag** har skjemaet ikke kontonummer, IBAN,
BIC, standardkonto eller mva-kode; se [Leverandører](/nb/user/accounting/#leverandører)
for hva hver rettighet lar deg endre.

### Leverandørvarer

En leverandørvare er det én leverandør selger av én produktvariant; en leverandør har én
leverandørvare per variant. Den har:

- **leverandørens varenummer** og leverandørens navn på varen, begge valgfrie;
- **Pris** per innkjøpsenhet, i leverandørens valuta, med opptil fire desimaler, med
  datoen den ble satt og om en person eller importen satte den;
- **Innkjøpsenhet** — produktets enhet eller en av pakningene;
- **Leveringstid** i dager, **Minste antall** og **Antall per bestilling (multippel)**, i
  innkjøpsenheter, hver valgfri.

Den første leverandørvaren for en variant er variantens **foretrukne** leverandør. Merker du
en annen som foretrukket, flyttes merket dit; en variant har alltid én foretrukket
leverandørvare så lenge den har noen, og når den foretrukne slettes, tar den eldste av de
andre over.

Når leverandørens valuta i registeret endres, beholder leverandørvaren prisen og sier at
valutaen avviker til noen setter prisen på nytt. Når pakningen en leverandørvare nevner,
slettes fra produktet, faller leverandørvaren tilbake til enheten og sier det.

**Legg til leverandørvare** på siden for en leverandør legger til en; hver kan redigeres
og slettes. Å legge til, endre og slette leverandørvarer krever **Administrere leverandørvarer**. Å
slette en endrer ikke ordrene som tok fra den.

### Importer en prisliste

**Importer prisliste** på siden for en leverandør leser leverandørens priser fra en
CSV-fil — UTF-8 eller Windows-1252, skilt med semikolon, komma eller tabulator — med
høyst 5000 rader. Overskriften navngir kolonnene, i hvilken som helst rekkefølge og med
store eller små bokstaver:

| Kolonne | Hva den har |
| --- | --- |
| `sku` eller `barcode` | Påkrevd, én av dem: produktvariantens SKU, eller strekkoden til en variant eller en av pakningene — en paknings strekkode setter også innkjøpsenheten |
| `supplier_item_number` | Leverandørens varenummer |
| `supplier_item_name` | Leverandørens navn på varen |
| `price` | Prisen per innkjøpsenhet, i leverandørens valuta |
| `packaging` | Navnet på en av produktets pakninger |
| `lead_time_days` | Leveringstiden i dager |
| `minimum_quantity` | Minste antall, i innkjøpsenheter |
| `multiple` | Antall per bestilling (multippel), i innkjøpsenheter |

1. Velg **Filen**, og deretter **Kontroller filen**. Ingenting skrives ennå: hver rad sier om den ville opprettet
   en leverandørvare eller oppdatert en, eller hvorfor den feiler — en ukjent eller
   tvetydig SKU eller strekkode, en ukjent pakning, en pris eller et antall som ikke er et
   tall, en rad som gjentar en annen.
2. Når hver rad går gjennom, velger du **Importer**. Hver rad skrives, eller — feiler noen —
   ingen.

En tom celle beholder det leverandørvaren har. Priser fra importen merkes som satt av
importen.

## Innstillinger

**Innstillinger** krever **Administrere innstillinger for Innkjøp**:

- **Første ordrenummer** — der ordreserien begynner. Når numre er brukt, kan det bare
  heves over det siste.
- **Standard lagersted** — lagerstedet en ny ordre får.
- **E-postkanal** — kanalen i Kommunikasjon ordrene sendes fra. Uten en markeres ordrene
  som sendt i stedet.
- **Tekst nederst på ordren** — skrives ut nederst på hver innkjøpsordre, opptil 4000 tegn.
- **Leveringsadresser** — per lagersted krysser du av for **Egen leveringsadresse** for å
  gi adressen leverandøren leverer til; uten en brukes selskapets adresse med lagerstedets
  navn.
- **Pristoleranse (%)** — en prosent, 2 % om den ikke endres, for fakturaavstemmingen som
  kommer senere.

## Dashbordet og produktsiden

Kortet for Innkjøp på dashbordet, for dem med **Bruke Innkjøp**, teller ordrene som er
forsinket, og de ubekreftede.

På siden for et produkt i Produkter lister kortet **Leverandører** leverandørvarene for
produktets varianter — hvilken leverandør som selger den, til hvilken pris og hvilken som
er foretrukket — når Innkjøp er i bruk og du har **Bruke
Innkjøp**.

Når en leverandørvare eller en linje på en innkjøpsordre nevner en produktvariant, sletter
ikke Produkter den, og et produkt med en åpen varelinje på et utkast eller en sendt ordre
blir ikke en tjeneste.

## Når en leverandør slettes eller anonymiseres

En leverandør som en innkjøpsordre eller en leverandørvare nevner, kan ikke slettes i
Regnskap; den kan gjøres inaktiv, og de åpne ordrene blir stående. En leverandør som er en
person, anonymiseres ikke mens et utkast, en sendt eller en delvis mottatt ordre nevner
personen. Når en personleverandør anonymiseres, fjernes navnet, adressen, e-posten og
kontakten fra ordrene, og ordrenes lagrede PDF-er slettes.

## Rettigheter

| Rettighet | Lar deg |
| --- | --- |
| **Bruke Innkjøp** (`purchasing:access`) | Åpne appen og lese hver ordre, leverandør og leverandørvare, prisene medregnet, og laste ned PDF-ene. |
| **Administrere innkjøpsordrer** (`purchasing:orders-manage`) | Skrive og slette utkast, endre sendte ordrer, registrere bekreftelsen, avslutte en linje eller en ordre, gjenåpne og kopiere. |
| **Sende innkjøpsordrer** (`purchasing:orders-send`) | Sende, markere som sendt, sende på nytt og kansellere. |
| **Administrere leverandørvarer** (`purchasing:supplier-items-manage`) | Legge til, endre, slette og importere leverandørvarer. |
| **Administrere innstillinger for Innkjøp** (`purchasing:settings`) | Innstillingene. |

**Ta imot varer** (`purchasing:receive`) og **Avstemme leverandørfakturaer**
(`purchasing:invoices-match`) kan allerede gis i en rolle; de tas i bruk når varemottaket
og fakturaavstemmingen kommer.

## Det som kommer senere

- **Varemottak** — å ta varene på en sendt ordre inn på lager, delvis eller helt, med
  partier og serienumre.
- **Forslag** — utkast til ordrer laget fra lav beholdning og fra innkjøpsforslagene i
  Produksjon, per foretrukket leverandør.
- **Fakturaavstemming** — leverandørens faktura i Regnskap avstemt mot ordren, og det som
  avviker.

Reglene bak hvert skjermbilde står i [referansen for Innkjøp](/en/reference/purchasing/).
