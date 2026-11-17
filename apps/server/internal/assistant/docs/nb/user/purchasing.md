---
title: Innkjøp
description: Innkjøp-appen — listen over innkjøpsordrer og filtrene; lag en innkjøpsordre til en leverandør og et lagersted, med produktlinjer fylt ut fra leverandørvarene og tekstlinjer; send den på e-post, marker den som sendt eller send den på nytt; registrer leverandørens bekreftelse; endre en sendt ordre og les historikken; avslutt en linje eller hele ordren, gjenåpne den, kanseller den eller kopier den; PDF-en; leverandørene og hva hver selger — leverandørvarer, foretrukket leverandør og import av prisliste med CSV-kolonnene; innkjøpsforslagene fra lav beholdning eller innkjøpsforslagene i Produksjon, og Lag ordrer med Bestill… fra de skjermbildene; innstillingene; dashbordkortet og produktsidens Leverandører-kort; varemottak — mottaksskjermen og skanning, mer eller mindre enn bestilt, ordrens varemottak, å angre et varemottak og å markere en tekst- eller tjenestelinje som levert; leverandørfakturaene — ordrens fakturaer, Registrer faktura, å avstemme en faktura fra panelet Innkjøpsordre i Regnskap, avvikene, hva bokføring, tilbakeføring og sletting av fakturaen gjør, den siste prisen, og Mottatt, ikke fakturert med CSV-filen; og det som kommer senere — innkommende EHF, betalingsfiler og returer til leverandør.
sidebar:
  order: 68
sources:
  - apps/purchasing/frontend
  - apps/host/frontend/src/routes/purchasing
  - apps/host/frontend/src/lib/purchasing-access.ts
  - apps/host/frontend/src/lib/dashboard-helpers.ts
  - apps/host/frontend/src/components/app-spotlight.tsx
  - apps/host/frontend/src/routes/products/$productId.tsx
  - apps/host/frontend/src/routes/accounting/supplier-invoices.$supplierInvoiceId.tsx
---

I Innkjøp-appen bestiller du varer og tjenester fra leverandørene dine. En
**innkjøpsordre** sier hva du kjøper av hvem, til hvilken pris og når du venter det; en
**leverandørvare** sier hva en leverandør selger av et av produktene dine, til hvilken
pris, i hvilken innkjøpsenhet og med hvilken leveringstid.

En innkjøpsordre flytter verken beholdning eller regnskap. Du bestiller og følger opp,
lager utkast til ordrer fra [innkjøpsforslag](#innkjøpsforslag), og når varene kommer,
[tar du dem imot](#ta-imot-varene), noe som legger dem på lager i Lager; når leverandørens
faktura kommer, [avstemmer du den mot ordren](#leverandørfakturaer).
Innkjøp kjøper fra leverandørene i registeret i [Regnskap](/nb/user/accounting/#leverandører)
og bestiller produktene i [Produkter](/nb/user/products/) til et lagersted i
[Lager](/nb/user/inventory/), så en installasjon med Innkjøp har alle tre.

## Appen og sidemenyen

Sidemenyen har **Innkjøpsordrer**, **Innkjøpsforslag** og **Leverandører** for alle med **Bruke Innkjøp**,
**Motta** for alle med **Ta imot varer**, **Mottatt, ikke fakturert** for dem som
avstemmer leverandørfakturaer, og **Innkjøpsinnstillinger** for dem som forvalter
innstillingene for Innkjøp. Hva du kan gjøre,
avhenger av rettighetene dine: å skrive ordrer, å sende dem og å vedlikeholde
leverandørvarene er egne rettigheter, så en kollega kan se knapper du ikke ser
([Rettigheter](#rettigheter)). Alle med **Bruke Innkjøp** ser prisene; den som bare har
**Ta imot varer**, ser **Motta** og ingen priser.

## Ordrelisten

**Innkjøpsordrer** lister ordrene med de nyeste først, med **Nummer**, **Leverandør** —
leverandørens referanse under navnet når den er registrert —, **Status**, **Ordredato**,
**Forventet dato**, **Mottatt**, **Fakturert** og **Sum eks. mva.** Et utkast har ikke
nummer ennå. **Mottatt** teller ordrens varelinjer som er mottatt i sin helhet eller
avsluttet, av alle varelinjene, og **Fakturert** linjene som er fakturert i sin helhet, av
alle linjene, etter [leverandørfakturaene som er avstemt mot den](#leverandørfakturaer).
En ordre kan være:

- **Utkast** — under arbeid; den har ikke nummer og kan slettes.
- **Sendt** — sendt til leverandøren, på e-post eller markert som sendt; den har fått
  nummeret sitt.
- **Delvis mottatt** — noe av varene er [tatt imot](#ta-imot-varene), og resten skal
  fortsatt komme.
- **Mottatt** — hver varelinje er mottatt i sin helhet eller avsluttet, og hver tekst- og
  tjenestelinje er markert som levert.
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
med mindre du velger en kollega som kan forvalte innkjøpsordrer.

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
  ordredatoen pluss leverandørvarens leveringstid. Leverandørvarens pris gjelder dens egen
  innkjøpsenhet: en linje i en annen innkjøpsenhet får den omregnet — prisen for en eske
  à 10 er ti ganger enhetens —, og blir stående på 0 når størrelsen på enheten ikke er
  kjent. Du fører antallet i innkjøpsenheten; valget av innkjøpsenhet sier hvor mange av
  produktets enhet den rommer — **Eske (15 stk)** —, så 3 av dem er 45 stykk i bestilling.
  **Rabatt %** er en prosent av prisen. Linjens beløp er antall × pris, minus rabatten,
  avrundet til to desimaler.
- **Tekstlinje** (**Legg til tekstlinje**) — noe utenfor katalogen, som frakt eller en tjeneste: en beskrivelse, et
  antall, en enhet og en pris.

Et antall under leverandørvarens **Minste antall**, eller som ikke er et helt antall av
dens **Antall per bestilling (multippel)**, vises som en advarsel; ordren kan likevel
lagres og sendes. Et produkt som er **Utgått**, kan ikke settes på en ny linje; et
tjenesteprodukt kan bestilles.

Alle beløp er uten mva., i ordrens valuta, og PDF-en sier det. En linjes beløp og ordrens
sum er høyst 999 999 999 999,99; et større avvises.

Å skrive ordrer krever **Forvalte innkjøpsordrer**: uten den sier **Ny innkjøpsordre** og
**Rediger** det i stedet for å vise et skjema. Endrer noen ordren mens du redigerer den,
sier **Lagre** det og tilbyr **Last inn på nytt**, som åpner ordren slik den er nå.

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

E-posten er en del av sendingen: den settes i kø sammen med ordren, så en sendt ordre har
alltid e-posten sin. Går den ikke med en gang — e-postleverandøren tar ikke imot den,
eller PDF-en kunne ikke lagres —, er ordren likevel sendt og har fått nummeret sitt:
dialogen lukkes, appen varsler at ordren er sendt og e-posten står i kø, og e-posten
sendes av seg selv så snart den kan, med nye forsøk en stund. Ordresiden sier at den
siste e-posten feilet og sendes på nytt; **Send på nytt** sender en ny kopi.

**Marker som sendt** gjør det samme uten e-post — for en ordre lagt inn på telefon eller i
leverandørens nettbutikk: ordren får nummeret sitt, og PDF-en lagres. Når e-post ikke er
satt opp, er det **Marker som sendt** ordresiden tilbyr.

Å velge **Send** to ganger, eller et brudd i forbindelsen fulgt av et nytt forsøk, sender
ikke ordren to ganger: dialogen sender den samme forespørselen igjen, og Innkjøp svarer
med den første.

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
et nytt forsøk eller **Feilet**. En e-post som ikke gikk, sier hvorfor med dine ord, med
e-postleverandørens egen melding ved siden av. En e-post i kø for en tidligere versjon
sendes ikke når ordren er sendt på nytt: den blir **Feilet**, fordi en nyere versjon ble
sendt først.

## Registrer leverandørens bekreftelse

Når leverandøren bekrefter ordren, velger du **Registrer bekreftelse** på en sendt ordre
og fører inn **leverandørens referanse** (deres ordrenummer) og **Bekreftet dato** — for
hele ordren og, om leverandøren bekrefter linjer ulikt, per linje. Bekreftelsen er en
opplysning på ordren; den endrer ikke statusen. Den fjerner ordrens merke **Ubekreftet**.
Tøm feltene for å fjerne den.

## Ta imot varene

Når varene kommer, velger du **Motta** på en sendt eller delvis mottatt ordre — eller, i
varemottaket, **Motta** i sidemenyen: den lister hver sendt ordre med varer som ennå skal
komme, den som er forventet først, øverst, med leverandøren, lagerstedet, forventet dato,
**Forsinket** når den er over tiden, og hvor mange av varelinjene som er igjen — *2 av 3*
—; velg ordrenummeret eller **Motta** på raden. Å ta imot krever bare **Ta imot varer**,
så den som står i varemottaket kan ta imot uten å forvalte ordrer eller se hva varene
koster. Skjermbildet, **Motta ordre 4501**, viser ingen priser. Det har en rad for hver
åpen varelinje — *Åpent 60 av 180 m, 120 mottatt* — med:

- **Antall**, fylt ut med det som fortsatt er åpent på linjen, i produktets enhet, med
  desimalkomma eller -punktum slik du skriver det; **Motta … i** viser det i linjens
  innkjøpsenhet når linjen har en — det du skrev, blir stående slik du skrev det, så du
  mister ingenting ved å bytte tilbake, og et antall skrevet i innkjøpsenheten tas imot
  som så mange pakninger;
- **Lokasjon** på lagerstedet som varene legges på: varens
  [hovedlokasjon](/nb/user/inventory/#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare)
  på lagerstedet når varen ligger på lager der, ellers lokasjonen der det meste av den
  ligger, ellers selve lagerstedet;
- for en vare som holdes i partier, **Partinummer** — står det tomt mens leverandørens
  parti eller en dato er fylt ut, nummererer Lager partiet; er ingenting fylt ut, ber
  varemottaket om partiet —, **Leverandørens parti**, **Produksjonsdato**, **Best før** og
  **Utløpsdato**; en dato som står tom, følger av produktets dager, som på
  [varemottaket i Lager](/nb/user/inventory/#partier-på-varemottak-flytting-og-justering);
- for en vare som holdes i serienumre, **Serienumre**, ett per linje, ett for hver
  enhet.

**Del opp** legger til en rad for samme linje, for å legge en del av den på en annen
lokasjon eller i et annet parti; **Fjern raden** tar en bort. En rad på 0 utelates, så en
linje som ikke har kommet, lar du bare stå på 0. **Følgeseddel** — nummeret på
leverandørens følgeseddel — og en merknad gjelder hele varemottaket.

Velg **Motta**. Varemottaket får neste nummer i en egen serie — *Varemottak 12 er
registrert.* — og varene legges på lokasjonene sine i én lagerbevegelse av typen
**Varemottak** i Lager, som lenker tilbake til ordren. Et varemottak bokfører ingenting i
regnskapet. Har du **Ta imot varer** uten **Bruke Innkjøp**, kommer du tilbake til
**Motta** etter varemottaket; ellers til ordren. En vare som skal [kontrolleres ved mottak](/nb/user/inventory/#kontroller-ved-mottak),
får partiene sine satt i karantene, som på varemottaket i Lager, og et produkt som er
utgått siden det ble bestilt, tas likevel imot. Det som er mottatt, teller ikke lenger
som *i bestilling* i Lager, med en gang.

Å velge **Motta** to ganger, eller å prøve igjen etter et brudd i forbindelsen, tar ikke
imot varene to ganger: skjermbildet sender det samme varemottaket igjen, og Innkjøp
svarer med det første — også når det første varemottaket siden har fullført ordren eller
er angret. Tar to personer imot samme ordre i samme øyeblikk, venter den ene på den
andre, og den andre regner med det den første tok imot.

Når leverandøren er en person, nevner lagerbevegelsen verken personen eller
følgeseddelen: Lager beholder bevegelsene sine når en leverandørs personopplysninger
slettes.

### Skann varene

**Skann en vare** tar det en strekkodeleser skriver og Enter-trykket den sender: varens
strekkode, strekkoden til en av pakningene, eller varenummeret. Den første skanningen
setter skjermbildet i telling: hvert utfylt antall tømmes, og hver skanning legger
deretter til på sin linje — én pakning for en paknings strekkode, én av enheten for
varens egen. En GS1-128-kode, som på en palleetikett, fyller også ut partinummeret og
produksjons-, best før- og utløpsdatoen; et annet parti av samme linje åpner en egen
oppdelt rad, og et serienummer legges til radens serienumre. Felt på etiketten som
skjermbildet ikke bruker — en nettovekt, pallens SSCC, et antall — hoppes over. Når
partiet ikke kan leses sikkert — leseren utelot skilletegnet etter det, så det som følger,
kan være en del av det — telles varen på en rad uten parti, og skjermbildet ber deg skrive
partiet. En kode uten serienummer på en vare som holdes i serienumre, sier **Skann
serienummeret** og teller ingenting. En kode som ikke nevner noen åpen linje på ordren,
sier **Ikke på denne ordren**.

### Mer eller mindre enn bestilt

Mer enn det som er åpent på en linje, spørres det om først: **Mer enn bestilt — motta
likevel?** nevner linjene, og **Motta likevel** tar det imot, så linjen har mer mottatt
enn bestilt.

Mindre går fint. Ordren er **Delvis mottatt**, resten står åpent på linjene til et senere
varemottak, og ingen restordre lages; når resten ikke kommer, avslutter **Avslutt
linje** eller **Avslutt** den. Når ingenting er åpent lenger, avsluttes ordren, uansett
hvilket steg som fullførte den: den er **Mottatt** når hver varelinje kom i sin helhet og
hver tekst- og tjenestelinje er markert som levert, og **Avsluttet** når en linje ble
avsluttet med noe som fortsatt manglet — også når det siste varemottaket eller
levert-merket kommer etter at linjen ble avsluttet. En avsluttet ordre tas ikke imot:
**Gjenåpne** den først.

### Ordrens varemottak

**Varemottak** på ordresiden lister ordrens varemottak, nyeste først: **Varemottak 12**,
dagen, hvem som tok imot, følgeseddelen, **Registrert** eller **Angret** — *Angret … av …*
—, og hver linje med antallet, lokasjonen og partiet eller serienumrene med datoene.
**Lagerbevegelse** og **Angrebevegelse** åpner bevegelsene i Lager for dem som kan se dem
(**Se beholdning**). Et varemottak viser ingen priser. Uten noen sier den *Ingenting er
mottatt på denne ordren ennå.*

### Angre et varemottak

**Angre** på et registrert varemottak tar det tilbake — en feiltelling, eller varer som ikke
besto kontrollen. Skriv gjerne en merknad. Varene tas fra lokasjonene og partiene de ble
lagt på, uansett hvilke sperringer partiene har, karantenen varemottaket satte på dem
frigis, og linjene regner dem som ikke mottatt igjen. Lager viser det som en bevegelse av
typen **Reversert varemottak**; varemottaket blir stående på ordren, merket **Angret** med
hvem og når.

Angringen avvises når varene ikke lenger ligger der de ble tatt imot — flyttet eller
levert siden —, når en lokasjon de ble lagt på er arkivert, og når varemottaket allerede
er angret. En mottatt ordre blir delvis mottatt igjen, eller sendt når ingenting annet på
den er mottatt eller levert; en avsluttet ordre forblir avsluttet.

### Marker en tekst- eller tjenestelinje som levert

En tekstlinje eller en tjeneste tas aldri inn på lager. Når den er levert — frakten
utført, tjenesten gjort — velger du **Marker som levert** på linjen på en sendt eller
delvis mottatt ordre; linjen sier da *Levert …*. **Ikke levert** tar merket av igjen, også
på en mottatt ordre, som da blir delvis mottatt — eller sendt, når ingenting annet på den
har kommet. En varelinje tas imot i stedet, og en avsluttet linje markeres ikke.
**Ikke levert** avvises mens en leverandørfaktura gjelder linjen: opphev avstemmingen
først.

Å ta imot, angre og markere en linje som levert krever **Ta imot varer**.

## Leverandørfakturaer

Når leverandørens faktura kommer, er den en leverandørfaktura i
[Regnskap](/nb/user/accounting/#leverandørfakturaer), og Innkjøp **avstemmer** den mot
ordren: hvilke ordrelinjer den gjelder, og hvor mye av hver. Avstemmingen er det
**Fakturert** teller, og den viser hvor fakturaen og ordren avviker. En faktura kan gjelde
flere ordrer fra samme leverandør og en ordre flere fakturaer, men hver linje på en faktura
gjelder én ordrelinje. Alt her krever **Avstemme leverandørfakturaer**; å lese
avstemmingene krever bare **Bruke Innkjøp**.

### Ordrens leverandørfakturaer

Ordresidens **Leverandørfakturaer** lister fakturaene som er avstemt mot ordren — **Faktura
55120** eller **Kreditnota …**, datoen, **Foreløpig** mens fakturaen er et utkast i Regnskap
og **Bokført** når den er bokført — med, per fakturalinje, ordrelinjen, antallet
**Avstemt**, **Ordrens pris per enhet** og avvikene. De som kan bruke Regnskap (**Bruke
Regnskap**), ser også **Fakturalinje** og **Fakturaens pris per enhet**; andre ser bare
antallene. Er det ingen, står det *Ingen leverandørfaktura er avstemt mot denne ordren.*
Hver ordrelinje viser hva som er bestilt, mottatt og fakturert.

### Registrer faktura

**Registrer faktura** på en sendt, mottatt eller avsluttet ordre lager leverandørfakturaen i
Regnskap for deg, allerede avstemt mot ordren:

1. Skriv **Leverandørens fakturanummer** og **Fakturadato**, og om du vil **Forfallsdato**
   (tom: leverandørens betalingsbetingelser etter fakturadatoen) og **Fakturabeløp** slik
   det står på fakturaen, med mva.
2. For en ordre i en annen valuta enn NOK oppgir du **Valutakurs** eller **Total i NOK**.
3. **Registrer faktura**. Regnskap får et utkast med én linje per ordrelinje som er mottatt
   — en tekst- eller tjenestelinje: markert som levert — og ikke fakturert ennå, til
   ordrens priser, ført netto på leverandørens standardkonto og mva-kode, og ordrenummeret
   som referanse.

Åpne så utkastet i Regnskap, kontroller det mot fakturaen, legg ved fakturaens fil og
bokfør det. Uten fakturabeløp er utkastets total linjenes nettobeløp: rett den til
fakturaens før du bokfører, for Regnskap bokfører en faktura bare når linjene gir totalen.
Et utkast har plass til 200 linjer; en ordre med flere faktureres i deler — dialogen sier
hvor mange linjer som gjenstår, og neste **Registrer faktura** tar dem. Det ett utkast tok,
regnes som fakturert, så en ny registrering tar bare resten, og en ordre uten noe igjen å
fakturere sier *Ingenting på ordren er mottatt og ikke fakturert.*

**Registrer faktura** krever **Bokføre bilag** i Regnskap i tillegg til **Avstemme
leverandørfakturaer**. Den avvises mens regnskapet ikke er satt opp, når leverandøren ikke
har noen standardkonto — *Åpne leverandøren i Regnskap* tar deg dit for å sette den —, og
når leverandørens fakturanummer allerede er registrert.

### Avstem en faktura fra Regnskap

En faktura en regnskapsfører har skrevet inn i Regnskap, avstemmes fra siden dens der:
panelet **Innkjøpsordre** viser ordrene fra fakturaens leverandør som referansen nevner
nummeret til (**Ordrer referansen nevner**, hver med hvor mange linjer som gjenstår å
fakturere), og fakturaens avstemminger med avvikene.

- **Avstem alt mottatt** parer fakturaens linjer som ikke er avstemt, i rekkefølge, med
  ordrens linjer som har noe mottatt og ikke fakturert, i rekkefølge, hver for det som
  gjenstår.
- **Fordel …** avstemmer linje for linje: velg ordren — en referansen nevner, eller **En
  annen ordre fra leverandøren** etter nummeret —, deretter for hver fakturalinje
  **Ordrelinje** og antallet, og **Avstem**. En fakturalinje avstemt mot samme ordrelinje
  på nytt får det nye antallet.
- **Opphev avstemming** tar tilbake avstemmingen av én linje, eller av en hel ordre.

Antall og beløp tar desimalkomma eller punktum. Mer enn det som er mottatt og ikke fakturert på en linje spør først — *Avstem likevel*
avstemmer den, og linjen viser da *Fakturert mer enn mottatt*. En kreditnota senker det som
er fakturert og avstemmes aldri for mer enn det som er fakturert på en linje. En faktura fra
en annen leverandør enn ordrens, i en annen valuta enn ordrens, eller tilbakeført, avstemmes
ikke; en fakturalinje avstemt mot en annen ordrelinje må oppheves først. En tilbakeført
faktura avstemmes ikke, men en avstemming en kreditnota holdt på den, blir stående, og
**Opphev avstemming** tar den tilbake. De uten **Bruke Regnskap** ser panelet bare for en
faktura som er avstemt mot en ordre, med bare de avstemte antallene — ingen fakturalinjer,
beløp eller foreslåtte ordrer.

### Avvikene

En avstemming viser avvikene som advarsler; de stopper aldri en bokføring:

- **Fakturert mer enn mottatt** — ordrelinjen er fakturert ut over det som er mottatt.
- **Fakturert mer enn bestilt** — ut over det som er bestilt.
- **Prisen avviker** — fakturalinjens nettobeløp avviker fra det ordren gir for samme antall
  med mer enn **Pristoleranse** i [innstillingene](#innstillinger).
- **Valutaen avviker** — fakturaen er i en annen valuta enn ordren.

Panelet og merket i Regnskap teller dem, en linjes egne — fakturert mer enn mottatt eller
bestilt — én gang uansett hvor mange fakturalinjer som gjelder den.

### Når fakturaen bokføres eller tilbakeføres

Avstemmingen av et utkast er **Foreløpig**; den blir **Bokført** når fakturaen bokføres i
Regnskap, og prisene vurderes på nytt fra de bokførte linjene. Hver avstemte produktlinjes
pris per enhet på den bokførte fakturaen blir da leverandørvarens pris, merket som satt av
fakturaen på datoen dens — med mindre fakturaen er i en annen valuta enn leverandørvaren,
eller prisen ble satt etter fakturaens dato, eller satt av en person eller en import på den
datoen etter at fakturaen ble bokført. En bokført faktura der leverandøren ble endret på utkastet, gjelder ikke lenger
ordren, og avstemmingen fjernes. En faktura som ble bokført før den ble avstemt, avstemmes
som bokført.

**Tilbakefør og kopier** i Regnskap tar avstemmingen av den tilbakeførte fakturaen og gir
den til kopien, foreløpig igjen, så det som er fakturert, står som før. Et varemottak kan
ikke angres mens det det tok imot, er fakturert: opphev avstemmingen først.

### Når utkastet slettes

Sletter du et utkast til leverandørfaktura i Regnskap — et du skrev inn, et **Registrer
faktura** laget, eller kopien **Tilbakefør og kopier** laget —, fjernes avstemmingene fra
ordren, så det de talte, skal faktureres igjen, og **Registrer faktura** tar det på nytt. En
avstemming en kreditnota holder, blir stående til kreditnotaen er opphevet; da tar
**Opphev avstemming** på ordrens faktura den tilbake, selv om Regnskap ikke lenger har
fakturaen.

### Mottatt, ikke fakturert

**Mottatt, ikke fakturert** i sidemenyen, for dem som avstemmer leverandørfakturaer, lister
per leverandør og ordrelinje det som var mottatt **Per** en dag — og ikke tatt tilbake innen
da — minus det bokførte fakturaer datert til og med den dagen gjelder, til ordrens
nettopris per enhet, med en sum per valuta. En tekst- eller tjenestelinje teller når den er
markert som levert innen da. Den er grunnlaget for en periodisering ved en periodes slutt;
ingenting bokføres fra den. **Last ned CSV** gir samme liste som en fil med semikolon og
desimalkomma.

## Avslutt en linje eller en ordre

- **Avslutt linje** avslutter én linje: det som er igjen på den, kommer ikke. Når ingen
  linje er åpen lenger, avsluttes ordren — eller blir **Mottatt**, når alt på den ble
  mottatt eller levert.
- **Avslutt** avslutter en sendt ordre med det som er igjen på den: hver åpen linje
  avsluttes, og ordren blir **Avsluttet**. Det som var igjen, teller ikke lenger som *i
  bestilling* i Lager.
- **Gjenåpne** åpner en avsluttet ordre igjen, med linjene avslutningen avsluttet; en linje
  du avsluttet for seg før, blir avsluttet. Ordren blir da delvis mottatt når noe på den
  er mottatt eller markert som levert, ellers sendt.

Å avslutte krever **Forvalte innkjøpsordrer**.

## Kanseller en ordre

**Kanseller ordre** avlyser en sendt ordre før noe er mottatt; et utkast slettes i stedet.
Når ordren ble sendt på e-post, får leverandøren kanselleringen — en PDF merket
**Kansellert** — med mindre du fjerner haken for **Send leverandøren kanselleringen**. En ordre som ble
markert som sendt, kanselleres uten e-post. Å kansellere krever **Sende innkjøpsordrer**.

## Slett et utkast

**Slett utkast** fjerner et utkast; det har ikke fått nummer, så ingenting blir igjen av
det. En sendt ordre kanselleres eller avsluttes i stedet. Å slette krever
**Forvalte innkjøpsordrer**.

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

## Innkjøpsforslag

**Innkjøpsforslag** i sidemenyen viser det som bør kjøpes inn nå, én rad per produkt og
lagersted. Der [Produksjon](/nb/user/manufacturing/#påfyllingsforslag) er slått på, er
radene innkjøpsforslagene fra siste påfyllingskjøring, som regner med det som produseres
og det salgsordrene trenger; der den ikke er det, er de
[listen over lav beholdning](/nb/user/inventory/#listen-over-lav-beholdning) i Lager. Et
produkt Produksjon lager, foreslås aldri kjøpt inn. Linjen under listen sier hvilken av de
to det er: *Fra innkjøpsforslagene i Produksjon.* eller *Fra lav beholdning i Lager.*

Hver rad viser:

- **Produkt** og **Lagersted**.
- **Behov** — det som fortsatt skal kjøpes, i produktets enhet: behovet minus det som
  allerede står på utkast til innkjøpsordrer for produktet og lagerstedet, og minus det som
  er bestilt til lagerstedet siden forslagene ble beregnet. Når noe er trukket fra, sier
  raden det: *33 trengs, 10 på utkast, 0 bestilt siden*. Et produkt som er dekket slik, er
  ingen rad.
- **Leverandør** — hver aktive leverandør med en leverandørvare for produktet, den
  foretrukne først, med leverandørens varenummer. Velger du en annen, endres antall og pris.
- **Antall** — i leverandørens innkjøpsenhet, rundet opp én gang: til hele innkjøpsenheter,
  så til leverandørvarens minste antall, så til antall per bestilling (multippelen). Under står det samme i produktets
  enhet. Du kan skrive et annet antall, mer enn 0 med høyst tre desimaler, med punktum
  eller komma (*2,5*).
- **Pris** — leverandørvarens pris per innkjøpsenhet, eller *Ingen pris*. En linje får
  prisen bare når den havner på et utkast i prisens valuta. En pris som ikke er i
  leverandørens valuta, vises i oransje: et nytt utkast er i leverandørens valuta, så der
  får linjen ingen pris.
- **Bestill innen** — dagen det trengs, eller i dag, minus leverandørens leveringstid,
  merket **Forsinket** når den er passert.

Et produkt uten leverandørvare står for seg under **Uten leverandør**: det kan ikke
bestilles her. Legg til en leverandørvare på leverandørens side
([Leverandørvarer](#leverandørvarer)), så får det en leverandør i listen. Et utgått produkt
står der også, merket **Utgått**, uansett hvilke leverandørvarer det har: det bestilles
ikke lenger.

### Lag ordrer fra forslag

1. Kryss av radene som skal bestilles, og endre leverandør eller antall der du trenger det.
2. Velg **Lag ordrer**. Knappen sier hvor mange rader som er krysset av: *Lag ordrer (3
   rader)*.

Det lages ett utkast til innkjøpsordre per leverandør og lagersted — eller, når du allerede
har et utkast til den leverandøren og det lagerstedet, legges linjene til det eldste, der
en linje med samme produkt i samme innkjøpsenhet får antallet lagt til. Hver linje får
leverandørens varenummer, innkjøpsenhet, pris og forventet dag, slik en ny linje i
redigeringen gjør ([Linjer](#linjer)). En linje som legges til et utkast du allerede hadde,
får prisen bare når utkastet er i prisens valuta; når det ikke er det, sier siden ved
utkastet hvor mange linjer som ikke fikk pris, så du kan sette den der. Siden lenker så til
hvert utkast, *Utkast til Norsk Stål AS*: åpne det, sjekk det og send det ([Send
ordren](#send-ordren)). Ingenting sendes fra forslagene, og ingenting bestilles av seg
selv. Utkastets historikk viser **Linjer fra forslag**.

Det som står på et utkast, trekkes fra forslagene med en gang. Hver avkrysset rad sendes
med behovet siden viste, og en rad bestilles bare når nøyaktig det behovet fortsatt
gjenstår når ordrene lages. Når det ikke gjør det — raden har kommet på et utkast, er
bestilt eller behovet er endret siden listen ble lastet, enten ved ditt eget tidligere klikk
eller en kollegas — lages ingenting i det hele tatt, siden sier det på raden og over
listen, og laster listen på nytt; sjekk radene og velg på nytt. En rad der den valgte
leverandøren ikke lenger tilbys etter at listen er lastet på nytt, mister avkrysningen, og
antallet går tilbake til forslaget.

Å velge **Lag ordrer** igjen med de samme radene etter et svar som aldri kom, bestiller
ikke to ganger: det andre klikket får det første svaret, selv om et av utkastene er slettet
siden.

**Bestill…** på en rad i listen over lav beholdning i Lager og på et innkjøpsforslag i
Produksjon åpner forslagene for det produktet og lagerstedet; **Vis alle** går tilbake til
alle radene. Den vises når Innkjøp er slått på og du kan forvalte innkjøpsordrer.

Å se forslagene krever **Bruke Innkjøp**; å lage ordrer fra dem krever i tillegg
**Forvalte innkjøpsordrer**.

## Leverandører og leverandørvarene deres

**Leverandører** lister leverandørene i registeret i Regnskap, med hvor mange
leverandørvarer og åpne ordrer hver har. Siden for en leverandør viser oppføringen —
nummer, navn, organisasjonsnummer, adresse, e-post, valuta og betalingsbetingelser — uten
at den kan endres der, og leverandørvarene. **Åpne i
Regnskap** fører til siden for leverandøren i Regnskap, for dem med **Bruke Regnskap**.
Listen viser de første 50 leverandørene som passer; søk for å finne de andre.

Kan du vedlikeholde leverandørene — **Vedlikeholde leverandører**, eller **Bokføre bilag**
med **Bruke Regnskap** — legger **Ny leverandør** i listen til en leverandør i registeret
fra Innkjøp, og **Rediger leverandør** på siden for en leverandør endrer den.
**Vedlikeholde leverandører** alene holder til begge: skjemaet åpnes med oppføringen i
registeret slik Innkjøp leser den. Uten **Bokføre bilag** og **Bruke Regnskap** har
skjemaet ikke kontonummer, IBAN, BIC, standardkonto eller mva-kode; se
[Leverandører](/nb/user/accounting/#leverandører) for hva hver rettighet lar deg endre.
Kan ikke leverandøren leses, sier **Rediger leverandør** det.

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
og slettes. Siden viser opptil 200 leverandørvarer og sier hvor mange det er når
leverandøren har flere; søk for å finne de andre. Å legge til, endre og slette
leverandørvarer krever **Forvalte leverandørvarer**. Å slette en endrer ikke ordrene som
tok fra den.

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

En tom celle beholder det leverandørvaren har. En pris kan skrives `1234.50`, eller på
norsk vis, `1234,50`, `1 234,50` eller `1.234,50`. Priser fra importen merkes som satt
av importen.

## Innstillinger

**Innkjøpsinnstillinger** krever **Forvalte innstillinger for Innkjøp**:

- **Første ordrenummer** — der ordreserien begynner. Når numre er brukt, kan det bare
  heves over det siste.
- **Standard lagersted** — lagerstedet en ny ordre får.
- **E-postkanal** — kanalen i Kommunikasjon ordrene sendes fra. Uten en markeres ordrene
  som sendt i stedet.
- **Tekst nederst på ordren** — skrives ut nederst på hver innkjøpsordre, opptil 4000 tegn.
- **Leveringsadresser** — per lagersted krysser du av for **Egen leveringsadresse** for å
  gi adressen leverandøren leverer til; uten en brukes selskapets adresse med lagerstedets
  navn. Landet er en kode på to bokstaver, som `NO`.
- **Pristoleranse (%)** — hvor mange prosent en fakturas pris kan avvike fra ordrens før
  avstemmingen viser *Prisen avviker* ([Avvikene](#avvikene)); 2 % om den ikke endres.

Har noen lagret innstillingene mens du endret dem, sier **Lagre** det og tilbyr **Last inn
på nytt**.

## Dashbordet og produktsiden

Kortet for Innkjøp på dashbordet, for dem med **Bruke Innkjøp**, teller ordrene som er
forsinket, og de ubekreftede. Når ordrer der du er **Vår referanse** er ubekreftet, sier
dashbordets liste over det som trenger oppmerksomhet det — *2 av innkjøpsordrene dine er
ikke bekreftet av leverandøren* — og åpner ordrelisten. Sidene i Innkjøp finnes også fra
søket i toppfeltet.

På siden for et produkt i Produkter lister kortet **Leverandører** leverandørvarene for
produktets varianter — hvilken leverandør som selger den, til hvilken pris og hvilken som
er foretrukket — når Innkjøp er i bruk og du har **Bruke
Innkjøp**. Det viser opptil 200 og sier hvor mange det er når det er flere.

Når en leverandørvare eller en linje på en innkjøpsordre nevner en produktvariant, sletter
ikke Produkter den, og et produkt med en åpen varelinje på et utkast eller en sendt ordre
blir ikke en tjeneste.

## Når en leverandør slettes eller anonymiseres

En leverandør som en innkjøpsordre eller en leverandørvare nevner, kan ikke slettes i
Regnskap; den kan gjøres inaktiv, og de åpne ordrene blir stående. En leverandør som er en
person, anonymiseres ikke mens et utkast, en sendt eller en delvis mottatt ordre nevner
personen. Når en personleverandør anonymiseres, fjernes navnet, adressen, e-posten og
kontakten fra ordrene, adressene, meldingene og avvisningene fra ordrenes e-poster, og
kontakten og merknadene fra ordrenes historikk — som beholder at de ble endret —,
følgesedlene og merknadene fra ordrenes varemottak, og ordrenes lagrede PDF-er slettes.

## Rettigheter

| Rettighet | Lar deg |
| --- | --- |
| **Bruke Innkjøp** (`purchasing:access`) | Åpne appen og lese hver ordre, leverandør og leverandørvare, prisene medregnet, og forslagene, og laste ned PDF-ene. |
| **Forvalte innkjøpsordrer** (`purchasing:orders-manage`) | Skrive og slette utkast, lage ordrer fra forslag, endre sendte ordrer, registrere bekreftelsen, avslutte en linje eller en ordre, gjenåpne og kopiere. |
| **Sende innkjøpsordrer** (`purchasing:orders-send`) | Sende, markere som sendt, sende på nytt og kansellere. |
| **Ta imot varer** (`purchasing:receive`) | Se **Motta** — ordrene som skal tas imot —, ta imot varer på innkjøpsordrer, angre et varemottak, og markere en tekst- eller tjenestelinje som levert. Alene, uten **Bruke Innkjøp**, viser den ingen priser. |
| **Forvalte leverandørvarer** (`purchasing:supplier-items-manage`) | Legge til, endre, slette og importere leverandørvarer. |
| **Avstemme leverandørfakturaer** (`purchasing:invoices-match`) | Avstemme og oppheve avstemming av leverandørfakturaer mot ordrer, registrere en faktura fra en ordre — med **Bokføre bilag** i Regnskap i tillegg —, og lese **Mottatt, ikke fakturert**. |
| **Forvalte innstillinger for Innkjøp** (`purchasing:settings`) | Innstillingene. |

## Det som kommer senere

- **Innkommende EHF og betalingsfiler** — en leverandørfaktura som kommer som EHF med
  ordrereferansen, og å betale den fra en betalingsfil; begge venter på Regnskaps neste fase.
- **Returer til leverandør** — varer som sendes tilbake til leverandøren.

Reglene bak hvert skjermbilde står i [referansen for Innkjøp](/en/reference/purchasing/).
