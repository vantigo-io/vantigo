---
title: Produksjon
description: Produksjon-appen — sidemenyen og Oversikt; å sette opp en arbeidsstasjon — koden, lagerstedet, kapasiteten, effektiviteten, rigging og rydding, kostnadssatsene, ukemønsteret og stengningene — og å arkivere, gjenaktivere og slette en; å lage en stykkliste for et produkt — det produserte antallet, linjene med svinn, operasjonene på arbeidsstasjoner med instruksjonene sine, og hvilken operasjon som forbruker en linje — standardstykklisten og de alternative stykklistene, å arkivere og gjenaktivere dem; å lese oversikten over en stykkliste — mengdene med svinn, det som lages og det som kjøpes, det som er tilgjengelig på et lagersted, de forventede minuttene og, for dem som ser kostnader, den samlede kostnaden; å lage en produksjonsordre — opprette, endre og bekrefte den, reservere komponentene og linjene som mangler, legge til og endre en komponent, registrere en produksjon med forhåndsutfyllingen og avviksvarselet, lukke den med eller uten restordre, kansellere den og lese historikken — og oversiktens ordretall; hvorfor Produkter nekter å slette en variant en stykkliste eller en åpen produksjonsordre navngir; og hvem som ser hva.
sidebar:
  order: 66
sources:
  - apps/manufacturing/frontend
  - apps/host/frontend/src/routes/manufacturing
---

Produksjon-appen beskriver hvordan produktene dine lages. På engelsk heter appen
**Manufacturing**. En **arbeidsstasjon** er en maskin, en celle eller et lag der et steg av
arbeidet gjøres, med timene den er åpen. En **stykkliste** forteller hvordan ett produkt
lages: hvilke komponenter som går inn i det, i hvilke mengder, og **operasjonene** —
stegene, hvert på en arbeidsstasjon — som gjør dem om til produktet. **Oversikten** over en
stykkliste bryter den ned i alt en gitt mengde trenger, nivå for nivå, med det som er på
lager og hvor lang tid arbeidet er ventet å ta. En **produksjonsordre** lager en mengde av
et produkt etter en av stykklistene: den reserverer komponentene og registrerer hver
**produksjon** — det som ble laget og det som gikk med.

Produktene og komponentene kommer fra [Produkter](/nb/user/products/), og bare **varer**
kan produseres eller brukes: aldri tjenester. Lagerstedene og beholdningen kommer fra
[Lager](/nb/user/inventory/). Reglene bak hvert skjermbilde — grensene, avvisningene og
rettigheten hver forespørsel sjekkes mot — står i
[referansen for Produksjon-modulen](/en/reference/manufacturing/).

## Appen og sidemenyen

**Produksjon**-flisen vises for alle med **Bruke Produksjon** (`manufacturing:access`).
Sidemenyen viser:

| Område i sidemenyen | Hva det inneholder |
| --- | --- |
| **Oversikt** | **Ordrer som skal startes i dag**, **Ordrer som pågår** og **Forsinkede ordrer**, og ordrene som er forsinket eller forfaller denne uken; **Aktive arbeidsstasjoner**, **Blokkerte arbeidsstasjoner** og **Aktive stykklister**: hvor mange det er |
| **Produksjonsordrer** | Alle åpne produksjonsordrer, med nummer, produkt, antall, lagersted, forfallsdato og reservasjonsstatus |
| **Stykklister** | Alle stykklistene, med produktet, koden, produsert antall, om den er standard, og antall linjer og operasjoner |
| **Arbeidsstasjoner** | Alle arbeidsstasjonene, etter kode, med lagerstedet sitt |

Alle med Bruke Produksjon leser alt dette. Å opprette og endre arbeidsstasjoner og
stykklister krever i tillegg **Administrere stykklister og arbeidsstasjoner**
(`manufacturing:boms-manage`); uten den vises ikke knappene som endrer noe. Å opprette og arbeide med produksjonsordrer
krever **Administrere produksjonsordrer** (`manufacturing:orders-manage`). Alle
kostnadstall krever **Se produksjonskostnader** (`manufacturing:costs-view`) — se
[Hvem ser hva](#hvem-ser-hva).

Når det ikke finnes noen stykkliste ennå, sier **Oversikt** fra og tilbyr **Gå til
stykklister**. Sett opp arbeidsstasjonene først: en operasjon i en stykkliste navngir en.

Ordretallene på **Oversikt** er: **Ordrer som skal startes i dag**, bekreftede ordrer som ikke er
startet, med planlagt start i dag eller tidligere; **Ordrer som pågår**; og **Forsinkede ordrer**, åpne
ordrer som forfalt før i dag. Under dem listes ordrene som er forsinket eller forfaller
denne uken, hver med om komponentene er klare; åpne en for å se den.

## Sett opp en arbeidsstasjon

1. Åpne **Arbeidsstasjoner** og velg **Ny arbeidsstasjon**.
2. Fyll ut arbeidsstasjonen:
   - **Kode** — 1 til 16 bokstaver A–Z og sifre, for eksempel `CNC1` eller `MONT`. Den
     lagres med store bokstaver, og to arbeidsstasjoner kan ikke dele en kode.
   - **Navn** — det folk kaller den, for eksempel *CNC-fres 1* eller *Monteringslinje*.
   - **Lagersted** — Lager-stedet den står på. Bare aktive lagersteder tilbys. Så lenge en
     aktiv stykkliste har en operasjon ved arbeidsstasjonen, kan lagerstedet ikke endres:
     arkiver de stykklistene, eller flytt operasjonene deres, først.
   - **Kapasitet** — hvor mange enheter den behandler samtidig i én syklus; 1 med mindre
     den endres. En ovn som steker 12 brød om gangen, har kapasitet 12.
   - **Effektivitet** — en prosent fra 1 til 500; 100 med mindre den endres. Ved 50
     tar arbeidet dobbelt så lang tid som operasjonens tid sier; ved 200 halvparten.
   - **Rigg-minutter** og **Rydde-minutter** — tiden det tar å gjøre den klar før en jobb og
     å rydde den etter, regnet én gang per jobb. En operasjon kan angi sin egen rigging.
   - **Maskinkostnad per time** og **Arbeidskostnad per time** — i NOK, begge valgfrie. De
     vises bare for dem med **Se produksjonskostnader**, og bare de kan sette eller
     tømme dem; når noen uten rettigheten lagrer arbeidsstasjonen, blir de lagrede
     satsene stående som de var.
3. Sett **Ukemønster** — timene den er åpen, dag for dag (se nedenfor).
4. Velg **Opprett arbeidsstasjon**. På en arbeidsstasjon som finnes, heter knappen **Lagre**.

### Ukemønsteret

**Ukemønster** er arbeidsstasjonens kalender: opptil **to intervaller** per dag, lagt til
med **Legg til intervall**, for eksempel
07:00–11:00 og 11:30–15:00 rundt en lunsjpause. En ny arbeidsstasjon er åpen mandag til
fredag, 07:00–15:00, og stengt i helgen.

- Tidene følger et **rutenett på 15 minutter**: 07:00, 07:15, 07:30 og så videre.
- Hvert intervall starter før det slutter, og en dags to intervaller kan ikke overlappe.
- Et intervall kan slutte **24:00**, for et skift som går til midnatt.
- En dag uten intervall er **Stengt**.

Tidene er lokal tid i arbeidsområdets tidssone. Norske helligdager er stengte dager uansett
hva mønsteret sier.

### Stengninger

En **stengning** stenger arbeidsstasjonen i en rekke dager — fellesferie, et
maskinhavari, en planlagt overhaling. På arbeidsstasjonen, under **Stengninger**, oppgir
du **Fra**-datoen, **Til**-datoen (samme dag eller senere) og en **Merknad**, og velger
**Legg til stengning**. En arbeidsstasjon har høyst 100 stengninger; slett de gamle du ikke
lenger trenger. Å slette en stengning åpner dagene igjen.

## Arkiver, gjenaktiver eller slett en arbeidsstasjon

- **Arkiver** tar en arbeidsstasjon ut av bruk: den tilbys ikke lenger for en ny
  operasjon, og **Arbeidsstasjoner** viser den bare når du setter **Status** til
  **Arkiverte** eller **Alle**.
  Stykklistene der operasjoner navngir den, beholder den.
- **Gjenaktiver** henter den tilbake. Det nektes når lagerstedet har blitt arkivert i Lager
  siden: gjenåpne lagerstedet i Lager først, eller lag en ny arbeidsstasjon på et annet lagersted.
  En arbeidsstasjon på et slikt lagersted kan fortsatt redigeres og beholder lagerstedet;
  flyttes den, er det bare til et aktivt lagersted.
- **Slett** fjerner den for godt, og er bare tillatt så lenge ingen operasjon i noen
  stykkliste navngir den. Ellers får du beskjed om at den er i bruk; arkiver den i stedet.

Når noen andre har lagret arbeidsstasjonen etter at du åpnet den, nektes lagringen din og
du blir bedt om å laste inn på nytt, slik at ingen overskriver en annens endring uten å se
den.

## Lag en stykkliste

1. Åpne **Stykklister** og velg **Ny stykkliste**.
2. Velg **Produktvariant** — varianten som skal lages — ved å skrive varenummeret eller navnet. Bare
   varer fra aktive produkter tilbys, og de som allerede har en stykkliste, er merket.
   Produktet kan ikke endres etter at stykklisten er lagret; lag en ny i stedet.
3. Fyll ut hodet:
   - **Produsert antall** — mengden stykklisten lager, mer enn 0 med høyst tre desimaler.
     Hver linjes mengde og hver operasjons tid er angitt per denne mengden: en stykkliste
     for 1 sykkel, eller en stykkliste for et parti på 50 liter.
   - **Stykklistekode** — valgfri, opptil 32 tegn, unik blant stykklistene, for eksempel
     *Rev B* eller *Sommeroppskrift*. Den skiller alternativene for ett produkt fra
     hverandre. Står den tom, har stykklisten ingen kode.
   - **Ledetid (dager)** — 0 eller mer, 0 med mindre den endres.
   - **Gjør til standard** — bare på en ny stykkliste: gjør den til produktets standard med
     en gang (se
     [Standardstykklisten og de alternative stykklistene](#standardstykklisten-og-de-alternative-stykklistene)).
   - **Produser mot ordre** — produktet lages for hver salgsordre i stedet for til lager.
   - **Merknad** — opptil 2 000 tegn.
4. Under **Operasjoner** velger du **Legg til operasjon** for hvert steg i arbeidet, i
   rekkefølgen de gjøres. Hver har:
   - **Rekkef.**, rekkefølgen den kjøres i;
   - **Operasjon**, navnet, for eksempel *Kapp*, *Sveis* eller *Lakk*;
   - **Arbeidsstasjon**, blant de aktive. Alle operasjonene i én stykkliste må være på
     arbeidsstasjoner på **samme lagersted**;
   - **Rigg-minutter**, valgfritt: la feltet stå tomt for å bruke arbeidsstasjonens;
   - **Minutter per syklus**, mer enn 0: tiden én syklus på arbeidsstasjonen tar;
   - **Instruksjoner**, i Markdown, opptil 20 000 tegn: det operatøren trenger å vite ved
     stasjonen.
5. Under **Komponenter** velger du **Legg til komponent** for hver linje. Hver har:
   - **Komponent**, en vare valgt slik produktet ble valgt;
   - **Antall** per produsert antall, mer enn 0 med høyst tre desimaler, i komponentens
     egen enhet;
   - **Svinn %**, fra 0 til 99,99, 0 med mindre den endres: andelen du venter å miste,
     som legges til det stykklisten trenger;
   - **Brukes av**, operasjonen som forbruker den, valgfri: velg en av operasjonene over,
     eller **Ingen operasjon**;
   - **Rekkefølge**, hvor linjen står i listen;
   - **Merknad**.

   Samme komponent kan stå på to linjer.
6. Velg **Opprett stykkliste**. På en stykkliste som finnes, heter knappen **Lagre**.

En stykkliste har høyst 200 linjer og 50 operasjoner. En linje kan navngi en operasjon du
la til i samme redigering, før noen av dem er lagret.

**Å redigere** en stykkliste lagrer linjene og operasjonene slik du ser dem, i én
omgang. Når du fjerner en operasjon en linje navnga, navngir linjen rett og slett ingen
lenger. Redigeringen nektes når noen andre har lagret stykklisten etter at du åpnet den;
last inn på nytt og gjør endringen din igjen.

### Det en stykkliste ikke kan

- **Inneholde seg selv.** En komponent kan ikke være stykklistens eget produkt, og heller
  ikke lages — gjennom sin egen standardstykkliste, på noe nivå — av noe som er det.
  Avvisningen navngir veien, for eksempel *Sykkel → Ramme → Sykkel*, så du ser hvilken
  stykkliste du må endre.
- **Være mer enn 10 nivåer dyp**, der hver komponent som lages av en egen stykkliste,
  teller som et nivå. En stykkliste som er, eller blir, produktets standard, teller også
  nivåene over seg: standardstykklistene som bruker produktet som komponent, og de som
  bruker deres. Et produkt nederst i en kjede ti nivåer dyp kan derfor ikke få en
  standardstykkliste.
- **Bruke en tjeneste.** Produktet og hver komponent må være varer. Et produkt som siden
  er gjort til en tjeneste, eller slettet, kan ikke lagres på en stykkliste; avvisningen
  navngir linjen.
- **Spre operasjonene over lagersteder.** Hver operasjons arbeidsstasjon må stå på samme
  lagersted.

## Standardstykklisten og de alternative stykklistene

Et produkt kan ha flere aktive stykklister — alternative oppskrifter, en ny revisjon ved
siden av den gamle. Nøyaktig én av dem er **standard**: den oversikten følger når
produktet er komponent i noe annet.

- Den første aktive stykklisten for et produkt blir standard av seg selv, enten **Gjør til
  standard** var krysset av eller ikke.
- **Gjør til standard**, på en annen av produktets stykklister, gjør den til standard og
  fjerner merket fra den gamle samtidig.
- **Arkiver** tar en stykkliste ut av bruk; den beholdes, og **Stykklister** viser den når
  du setter **Status** til **Arkiverte** eller **Alle**. En arkivert stykkliste kan ikke
  redigeres eller gjøres til standard. Når du arkiverer standarden, blir produktets eldste
  andre aktive stykkliste standard, eller produktet har ingen. Når den eldste nå ville
  inneholde seg selv eller gå mer enn 10 nivåer ned, blir heller ikke den standard:
  produktet står uten standard til du velger **Gjør til standard** på en stykkliste som
  kan være det.
- **Gjenaktiver** henter en arkivert stykkliste tilbake. Den blir standard når produktet
  ikke har noen aktiv standard. Den sjekkes på nytt som om den ble lagret, så den nektes
  hvis den nå ville inneholde seg selv eller gå for dypt, hvis en komponent ikke lenger er
  en vare, eller hvis arbeidsstasjonen til en operasjon siden er arkivert, eller flyttet
  slik at operasjonene står på mer enn ett lagersted.

## Les oversikten over en stykkliste

På en stykkliste velger du **Oversikt**. Den viser stykklisten som et tre: produktet
øverst, komponentene under det, og — for en komponent som har en egen standardstykkliste —
den stykklistens komponenter under der igjen, så dypt det går.

- **Antall som skal lages** — hvor mye av produktet du vil se på; stykklistens produserte
  antall når feltet er tomt.
- **Lagersted** — velg et lagersted for å se hva som er tilgjengelig der.

Hver rad viser:

- **Trengs**: linjens mengde skalert til mengden over den, med svinnet lagt
  til, **rundet opp** til tre desimaler, så du aldri planlegger for lite;
- **Lages** eller **Kjøpes**: lages når komponenten har en egen aktiv
  standardstykkliste, som treet da følger; kjøpes når den ikke har det;
- **Tilgjengelig**, på det valgte lagerstedet: det som er på lager der og ikke reservert;
- **operasjonene** på en rad som lages, hver med sine **forventede minutter**: riggingen,
  ryddingen og syklusene mengden trenger på arbeidsstasjonen med dens kapasitet og
  effektivitet, rundet til nærmeste minutt.

For eksempel trenger en operasjon på 6 minutter per syklus på en arbeidsstasjon med
kapasitet 2, rigging 15 og rydding 5, for 10 av et produkt der stykklisten lager 1, 5
sykluser: 15 + 5 + 5 × 6 = 50 minutter.

Med **Se produksjonskostnader** viser oversikten også:

- hver rads **Standardkost**, fra produktet i Produkter, eller **Ingen standardkost**, og
  dens **Verdi** — mengden som trengs, til den kostnaden;
- hver operasjons **kostnad**: de forventede timene til arbeidsstasjonens maskinsats pluss
  arbeidssatsen;
- den samlede kostnaden: **Komponenter til standardkost**, **Operasjoner** og, summen av
  dem, **Samlet kostnad**. En komponent som lages og har egen standardkost teller med den kostnaden, som står for alt
  under den; en uten regnes ut fra sine egne komponenter og operasjoner.

Når en innkjøpt komponent ikke har standardkost, teller den som 0, og oversikten sier
**Kostnaden er ufullstendig**. Sett standardkosten i Produkter for å fullføre den.

En stykkliste der treet ville hatt mer enn 2 000 rader, er for stor til å vises;
oversikten sier fra om det.

## Produksjonsordrer

En **produksjonsordre** sier: lag denne mengden av dette produktet, etter denne
stykklisten, på dette lagerstedet, innen denne datoen. Den nummereres **MO-1**, **MO-2**
og så videre etter hvert som den opprettes. **Produksjonsordrer** lister de åpne —
utkast, bekreftede og påbegynte — med produktet, antallet, lagerstedet, forfallsdatoen,
prioriteten og, for en bekreftet eller påbegynt ordre, om komponentene er **Reservert**,
**Delvis reservert** eller **Ikke reservert**.

Å arbeide med ordrene krever **Administrere produksjonsordrer**
(`manufacturing:orders-manage`) ved siden av Bruke Produksjon; å registrere en produksjon
kan i stedet gjøres med **Arbeide i verkstedet** (`manufacturing:shop-floor`). Uten dem
leser du ordrene, men knappene som endrer noe, vises ikke.

En ordre går gjennom disse statusene:

| Status | Hva den betyr |
| --- | --- |
| **Utkast** | Under forberedelse; alt kan endres, og den følger stykklisten sin når den endres |
| **Bekreftet** | Komponentene er kopiert fra stykklisten og reservert |
| **Pågår** | Minst én produksjon er registrert |
| **Ferdig** | Lukket |
| **Kansellert** | Kansellert før noe ble produsert |

### Finn en ordre

På **Produksjonsordrer** filtrerer du på **Status**, **Lagersted**, **Arbeidsstasjon**
(ordrene der stykklisten har en operasjon der), produkt, et intervall for forfallsdato,
**Forsinket** (forfalt før i dag og verken ferdig eller kansellert), opphav, **Ansvarlig**
eller reservasjonsstatusen, og søker på nummeret, for eksempel `MO-12`, eller produktets
varenummer eller navn. Sorter på forfallsdato, nummer eller prioritet. Listen oppdaterer
seg selv når noen endrer en ordre.

### Opprett en ordre

1. Åpne **Produksjonsordrer** og velg **Ny produksjonsordre**.
2. Velg **Produktvariant**. Bare produkter med en aktiv stykkliste kan produseres; for et
   produkt uten, lag en stykkliste først.
3. Fyll ut ordren:
   - **Stykkliste** — produktets standard, med mindre du velger et av alternativene;
   - **Antall** — mer enn 0, med høyst tre desimaler;
   - **Lagersted** — der det lages: lagerstedet til stykklistens arbeidsstasjoner, ellers
     det første aktive lagerstedet. En ordre kan ikke lages på et annet lagersted enn
     arbeidsstasjonene operasjonene navngir;
   - **Lokasjon for ferdigvarer** — der de ferdige varene legges; lagerstedets øverste
     lokasjon med mindre du velger en;
   - **Lokasjon for komponenter** — valgfri: der komponentene tas fra. La den stå tom for
     å ta hver komponent fra hovedlokasjonen sin, deretter fra de andre lokasjonene som
     har den;
   - **Forfallsdato**, **Planlagt start** og **Planlagt slutt** — slutten ikke før
     starten;
   - **Prioritet** — **Normal** eller **Haster**;
   - **Ansvarlig** — personen som følger opp ordren;
   - **Merknad** — opptil 2 000 tegn.
4. Velg **Opprett produksjonsordre**. Ordren er et **Utkast**.

Lokasjonene som tilbys, er lagerstedets aktive.

### Endre en ordre

På et utkast kan du endre alt. På en bekreftet eller påbegynt ordre kan du bare endre
datoene, prioriteten, den ansvarlige, merknaden og lokasjonene; for å lage et annet
produkt eller antall, kanseller den og opprett en ny. En ferdig eller kansellert ordre kan
ikke endres. Når noen andre har lagret ordren etter at du åpnet den, nektes lagringen din,
og du blir bedt om å laste inn på nytt.

### Bekreft en ordre

På et utkast velger du **Bekreft**. Å bekrefte:

- kopierer stykklistens linjer til ordren som **komponentene** dens, så senere endringer i
  stykklisten ikke lenger når den. Hver komponents **Trengs** er linjens mengde skalert til
  ordrens antall, med svinnet lagt til, **rundet opp** til tre desimaler;
- **reserverer** komponentene på lagerstedet, så ingen andre tar dem.

Når lagerstedet ikke har nok av en komponent, blir ordren bekreftet likevel: linjene som
ikke kunne reserveres, blir stående ureservert, og siden navngir hver av dem med mengden
som mangler. En linje reserveres helt eller ikke i det hele tatt. Et utkast der
stykklisten siden er arkivert, kan ikke bekreftes; opprett ordren på nytt fra en aktiv.

### Reserver linjene som mangler

Når varene har kommet inn, velger du **Reserver nå** på en bekreftet eller påbegynt ordre.
Den prøver igjen å reservere hver linje som ennå ikke er dekket, på samme måte, og navngir
linjene som fortsatt mangler.

Ordresiden viser for hver komponent **Trengs**, **Reservert**, **Tilgjengelig** på
lagerstedet, **Forbrukt**, **Kassert** og **Mangler**, og ordrens komponentstatus:

- **Klar** — hver linjes gjenstående behov er reservert, eller tilgjengelig på
  lagerstedet;
- **Delvis tilgjengelig** — noen linjer er dekket, noen ikke;
- **Venter** — ingen linje er dekket.

Det som er tilgjengelig på lagerstedet, deles mellom linjene for én komponent, i den
rekkefølgen linjene står. Siden regner dette ut mens du ser på den; listen viser bare det
som er reservert.

### Legg til eller endre en komponent

På en bekreftet eller påbegynt ordre:

- **Legg til komponent** legger til en linje stykklisten ikke hadde — bare varer — med
  mengden som **Trengs**. Den reserveres med en gang hvis lagerstedet har den.
- Å endre en linjes **Trengs** reserverer differansen når du øker den, og gir overskuddet
  tilbake når du senker den. Den kan ikke gå under det linjen allerede har forbrukt og
  kassert.

Selve stykklisten endres ikke.

### Registrer en produksjon

Når varer er laget, velger du **Registrer produksjon** på en bekreftet eller påbegynt
ordre.

1. Fyll ut **Antall** — de gode enhetene som er laget — og eventuelt **Kasserte
   ferdigvarer** — ferdige enheter som ble laget, men kastet. Kasserte ferdigvarer går aldri
   inn på lager og teller ikke som produsert.
2. Hver komponentlinje er **forhåndsutfylt** med det den produksjonen skal bruke: mengden
   som trengs × (antall + kassert ferdigvare) / ordrens antall, rundet til tre desimaler.
   Endre **Forbrukt** der det gikk med mer eller mindre, og legg til **Svinn** for
   komponenter som gikk tapt. Du kan også velge lokasjonen en linje tas **Fra**, og hvor
   varene legges (**Til**, ordrens lokasjon for ferdigvarer med mindre du velger en
   annen). En linje du fjerner, forbruker ingenting.
3. Velg **Registrer**.

Hvis en forbrukt mengde avviker mer enn 10 % fra det forhåndsutfylte tallet, får du en
advarsel og ser hver slik linje med det som var ventet; velg å registrere likevel, eller
rett det. Antall 0 er bare tillatt når noe forbrukes eller kasseres.

Å registrere tar komponentene ut av lageret — fra linjens lokasjon, ellers ordrens lokasjon
for komponenter, ellers komponentens hovedlokasjon først og deretter de andre lokasjonene
som har den — og legger de gode enhetene på lokasjonen for ferdigvarer, i én
**Produksjon**-bevegelse i Lager, som lenker tilbake til ordren. Det nektes når
lagerstedet ikke har nok av en komponent, når lagerstedet er arkivert, eller når Lager
nekter av en annen grunn; da registreres ingenting. Ordren går over til **Pågår**. Å
registrere mer enn ordrens antall er tillatt: siden viser overskuddet. Sendes samme
registrering to ganger — et dobbeltklikk, en tapt forbindelse — bokføres den bare én gang.

En registrert produksjon kan ikke angres i Produksjon. Rett en feil med en justering i
[Lager](/nb/user/inventory/).

### Lukk en ordre

Velg **Lukk** på en bekreftet eller påbegynt ordre når arbeidet er ferdig. Det som
fortsatt er reservert for den, frigjøres, og ordren er **Ferdig**.

Når mindre enn antallet ble produsert, blir du spurt om du vil **opprette en restordre**:

- **Ja** oppretter en ny ordre for resten — antallet minus det som ble produsert — med
  samme stykkliste, lagersted, lokasjoner, forfallsdato, prioritet og ansvarlig. Den
  nummereres etter den opprinnelige: den første restordren av MO-12 er **MO-12-2**, den
  neste **MO-12-3**, også når den er en restordre av MO-12-2. Den bekreftes med en gang og
  reserverer komponentene sine; linjene den ikke kunne reservere, navngis.
- **Nei** lukker ordren med det som ble produsert.

### Kanseller en ordre

Velg **Kanseller ordre** på et utkast, en bekreftet eller en påbegynt ordre så lenge
ingenting er produsert, forbrukt eller kassert på den. Reservasjonene frigjøres, og den
beholder nummeret sitt. Når noe er registrert, kan den ikke kanselleres; lukk den i
stedet. En ferdig eller kansellert ordre er endelig.

### Les historikken til en ordre

Ordresiden viser **Historikk**: hver hendelse — opprettet, endret, bekreftet, reservert,
en produksjon registrert med mengdene, en komponent lagt til eller endret, lukket,
kansellert, en restordre opprettet — med hvem som gjorde det og når. Ingenting i den kan
endres eller fjernes.

## Å slette et produkt en stykkliste bruker

Produkter nekter å slette en variant, eller å gjøre produktet til en tjeneste, så lenge en
**aktiv** stykkliste navngir den som produkt eller som komponent. Avvisningen sier hvor
mange stykklister som navngir den. Arkiver de stykklistene, eller endre linjene deres,
først. En arkivert stykkliste holder ingenting tilbake.

Det samme gjelder en **produksjonsordre** som verken er ferdig eller kansellert og navngir
varianten som produkt eller som komponent: avvisningen teller den sammen med
stykklistene. Lukk eller kanseller ordren først.

## Hvem ser hva

| Hva | Bruke Produksjon | Administrere stykklister og arbeidsstasjoner | Se produksjonskostnader |
| --- | --- | --- | --- |
| Flisen, **Oversikt**, **Stykklister**, **Arbeidsstasjoner**, en stykkliste, en arbeidsstasjon og dens stengninger, en stykklistes **Oversikt** uten kostnader | ✓ | – | – |
| **Ny arbeidsstasjon**, **Lagre**, **Arkiver**, **Gjenaktiver**, **Slett** på en arbeidsstasjon; å legge til og slette stengninger | ✓ | ✓ | – |
| **Ny stykkliste**, **Lagre**, **Arkiver**, **Gjenaktiver**, **Gjør til standard** på en stykkliste | ✓ | ✓ | – |
| Maskin- og arbeidssatsene på en arbeidsstasjon, og å sette dem | ✓ | ✓ for å sette dem | ✓ |
| Standardkostene, verdiene, operasjonskostnadene og den samlede kostnaden i oversikten | ✓ | – | ✓ |

Hver rad krever alle rettighetene den krysser av. **Se produksjonskostnader** er sensitiv:
en administrator gir den bare til dem som skal se hva ting koster å lage, og den kan ikke
delegeres.

| Hva | Bruke Produksjon | Administrere produksjonsordrer | Arbeide i verkstedet |
| --- | --- | --- | --- |
| **Produksjonsordrer**, en ordre, tilgjengeligheten, produksjonene og **Historikk** | ✓ | – | – |
| **Ny produksjonsordre**, **Lagre**, **Bekreft**, **Reserver nå**, **Legg til komponent**, å endre en komponent, **Lukk**, **Kanseller ordre** | ✓ | ✓ | – |
| **Registrer produksjon** | ✓ | en av dem | en av dem |

Å registrere en produksjon krever Bruke Produksjon og en av de to andre.
Beholdningstallene i oversikten og på en ordre er Produksjons egne og krever ingen
rettighet i Lager. Hele tabellen står under [Permissions](/en/reference/manufacturing/#permissions) i
referansen.
