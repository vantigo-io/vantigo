---
title: Produksjon
description: Produksjon-appen — sidemenyen og Oversikt; å sette opp en arbeidsstasjon — koden, lagerstedet, kapasiteten, effektiviteten, rigging og rydding, kostnadssatsene, ukemønsteret og stengningene — og å arkivere, gjenaktivere og slette en; å lage en stykkliste for et produkt — det produserte antallet, linjene med svinn, operasjonene på arbeidsstasjoner med instruksjonene sine, og hvilken operasjon som forbruker en linje — standardstykklisten og de alternative stykklistene, å arkivere og gjenaktivere dem; å lese oversikten over en stykkliste — mengdene med svinn, det som lages og det som kjøpes, det som er tilgjengelig på et lagersted, de forventede minuttene og, for dem som ser kostnader, den samlede kostnaden; å lage en produksjonsordre — opprette, endre og bekrefte den, reservere komponentene og linjene som mangler, legge til og endre en komponent, registrere en produksjon med forhåndsutfyllingen og avviksvarselet, partiet den lager, serienumre og partiene den forbruker, fanen Partier på ordren, lukke den med eller uten restordre, kansellere den og lese historikken — og oversiktens ordretall; verkstedet — å velge arbeidsstasjonen din, køen, å starte, sette på pause og fullføre en arbeidsordre, å starte en utenfor rekkefølgen, å bytte til en annen, å registrere produksjon på den siste operasjonen, å melde svinn, å blokkere og oppheve blokkeringen av en arbeidsstasjon som har stoppet, Mitt arbeid og varselet etter 12 timer; og for arbeidsledere ordrens arbeidsordrer og tidsregistreringer, å korrigere en registrering og å legge til en som mangler; produksjonsplanen — å planlegge en ordre og å planlegge alle, ønsket start, å lese tavlens belastning, farger og stengte dager, å flytte en arbeidsordre ved å dra den eller med Flytt til, og å fjerne en plan; kostnaden for en ordre — estimatet, det faktiske, avviket og kostnaden per enhet, og arbeidsordrenes tidsavvik; rapportene — produksjonskostnad, utnyttelse av arbeidsstasjoner og tidsavvik, og å laste dem ned; kvalitet — kontrollene på en operasjon, å registrere dem ved stasjonen, hva de stopper, og avvik — å melde et, å sette produksjonen i karantene, å lukke det og å oppheve sperrene; påfyllingsforslag — hva som er fremskrevet, hva som foreslås produsert eller kjøpt og innen når, å opprette utkast eller bekreftede ordrer fra dem, å avvise et, å kjøre nå, å eksportere innkjøpsforslagene og automatisk oppretting på en stykkliste; hvorfor Produkter nekter å slette en variant en stykkliste eller en åpen produksjonsordre navngir; og hvem som ser hva.
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
**produksjon** — det som ble laget og det som gikk med. Hver operasjon i en bekreftet ordre
er en **arbeidsordre** på arbeidsstasjonen sin, og **verkstedet** er der operatørene
starter, setter på pause og fullfører dem, og registrerer tiden de arbeider.
**Produksjonsplanen** legger arbeidsordrene inn i arbeidsstasjonenes kalendere, og
**rapportene** sammenligner det som var planlagt, med det som skjedde.

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
| **Oversikt** | **Rapporter** i toppen; **Ordrer som skal startes i dag**, **Ordrer som pågår** og **Forsinkede ordrer**, og ordrene som er forsinket eller forfaller denne uken; **Aktive arbeidsstasjoner**, **Blokkerte arbeidsstasjoner**, **Aktive stykklister** og **Forslag å produsere**: hvor mange det er |
| **Produksjonsordrer** | Alle åpne produksjonsordrer, med nummer, produkt, antall, lagersted, forfallsdato og reservasjonsstatus |
| **Forslag** | Hva som bør produseres eller kjøpes inn så beholdningen holder seg over hvert produkts bestillingspunkt, lagersted for lagersted, med begrunnelsen og datoen det trengs innen |
| **Verksted** | Arbeidsstasjonenes køer, der operatørene starter, setter på pause og fullfører arbeidsordrer, og **Mitt arbeid** |
| **Produksjonsplan** | Tavlen: arbeidsordrene som er planlagt på hver arbeidsstasjon, dag for dag, med belastningen, og ordrene som gjenstår å planlegge |
| **Avvik** | Det som gikk galt: alle avvik, de åpne først, med ordre, produkt og partier, og om de holdes i karantene |
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
innen slutten av denne uken — søndag, i virksomhetens egen tidssone — med forfallsdato og
status; åpne en for å se om komponentene er klare. Om komponentene til en ordre er
tilgjengelige, vises bare på ordresiden, ikke på **Oversikt** eller i listen. Tallene og
listen følger ordrene etter hvert som de endres.

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
  Å arkivere nektes så lenge en arbeidsordre som verken er ferdig eller kansellert, er på
  arbeidsstasjonen, eller så lenge den er blokkert: fullfør arbeidet, eller lukk eller
  kanseller ordrene, og opphev blokkeringen, først.
- **Slett** fjerner den for godt, og er bare tillatt så lenge ingen operasjon i noen
  stykkliste, ingen arbeidsordre og ingen tidsregistrering navngir den. Ellers får du
  beskjed om at den er i bruk; arkiver den i stedet.

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
   - **Produser mot ordre** — produktet lages for hver salgsordre i stedet for til lager
     ([Produser mot ordre](#produser-mot-ordre)).
   - **Mønster for partinummer** — for et produkt som spores med partier: hvordan partiet
     produksjonene lager, nummereres, `{MO}` med mindre det endres. Se
     [Partier og serienumre](#partier-og-serienumre).
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
     stasjonen;
   - **Kvalitetskontroller**, valgfritt: det operatøren registrerer ved stasjonen — se
     [Kvalitetskontroller på en operasjon](#kvalitetskontroller-på-en-operasjon).
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

### Kvalitetskontroller på en operasjon

Under hver operasjon legger **Legg til kontroll** til en kvalitetskontroll, opptil 20 per
operasjon. Hver har:

- en **Kontroll**: tittelen, for eksempel *Fyllvekt* eller *Tett forsegling*;
- en **Type**:
  - **Instruks** — lest og bekreftet med **Utført**;
  - **Godkjent eller avvist** — operatøren svarer **Godkjent** eller **Avvist**;
  - **Måling** — operatøren skriver inn den målte verdien, og Vantigo vurderer den:
    innenfor grensene er godkjent, utenfor er avvist. Oppgi **Min**, **Maks** eller
    begge, og eventuelt den **Nominelle** — mellom dem — og **Enheten**. Opptil fire
    desimaler; en måling kan være negativ;
  - **Bilde** — operatøren tar et bilde med nettbrettets kamera;
- **Når**:
  - **Hver arbeidsordre** — registreres én gang før arbeidsordren fullføres;
  - **Første stykk** — registreres før noe som er laget på arbeidsordren, sendes videre,
    og vises først når operatøren starter;
- **Instruks til operatøren**, valgfritt;
- **Karantene ved avvik**: når kontrollen avvises, setter avviket den oppretter det ordren
  lager på vent — se [Avvik](#avvik).

Kontrollene lagres med stykklisten slik redigeringen viser dem: å fjerne en kontroll,
eller alle kontrollene på en operasjon, og lagre, sletter den fra stykklisten. En
produksjonsordre får kontrollene til operasjonene sine når den bekreftes: å endre
stykklisten etterpå endrer ikke kontrollene til en ordre som allerede er bekreftet.

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
| **Pågår** | Minst én produksjon er registrert, eller en arbeidsordre er startet |
| **Ferdig** | Lukket |
| **Kansellert** | Kansellert før noe ble produsert |

### Finn en ordre

På **Produksjonsordrer** filtrerer du på **Status**, **Lagersted**, **Arbeidsstasjon**
(ordrene med en arbeidsordre der; for et utkast, de der stykklisten har en operasjon
der), produkt, et intervall for forfallsdato,
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
   - **Forfallsdato** — dagen varene må være klare. De planlagte datoene settes ikke
     her: bare [planleggingen](#planlegg-en-ordre) setter dem, og når du endrer en ordre,
     viser skjemaet dem som **Planlagt**, uten at de kan endres;
   - **Prioritet** — **Normal** eller **Haster**;
   - **Ansvarlig** — personen som følger opp ordren. En deaktivert bruker kan ikke
     velges;
   - **Merknad** — opptil 2 000 tegn.
4. Velg **Opprett produksjonsordre**. Ordren er et **Utkast**.

Lokasjonene som tilbys, er lagerstedets aktive.

### Endre en ordre

På et utkast kan du endre alt. På en bekreftet eller påbegynt ordre kan du bare endre
datoene, prioriteten, den ansvarlige, merknaden og lokasjonene; for å lage et annet
produkt eller antall, kanseller den og opprett en ny. Lokasjonene må være aktive
lokasjoner på ordrens eget lagersted; ordren beholder lagerstedet og stykklisten sin også
når arbeidsstasjonene i stykklisten er flyttet til et annet lagersted, eller lagerstedet
er arkivert, etter at den ble bekreftet. En ansvarlig som er deaktivert siden, blir
stående på ordren, men du kan ikke velge en deaktivert. En ferdig eller kansellert ordre kan
ikke endres. Når noen andre har lagret ordren etter at du åpnet den, nektes lagringen din,
og du blir bedt om å laste inn på nytt.

Skjemaet har også **Ønsket start**: dagen [planleggingen](#planlegg-en-ordre) kan starte
ordren fra. La den stå tom for å planlegge fra standarddatoen; på en ordre som finnes,
nevner feltet den datoen.

### Bekreft en ordre

På et utkast velger du **Bekreft**. Å bekrefte:

- kopierer stykklistens linjer til ordren som **komponentene** dens, så senere endringer i
  stykklisten ikke lenger når den. Hver komponents **Trengs** er linjens mengde skalert til
  ordrens antall, med svinnet lagt til, **rundet opp** til tre desimaler;
- **reserverer** komponentene på lagerstedet, så ingen andre tar dem;
- oppretter en **arbeidsordre** for hver operasjon i stykklisten, på arbeidsstasjonen
  sin, med instruksjonene og de forventede minuttene. Den første operasjonen er klar til
  å startes i [verkstedet](#verkstedet); de andre venter på tur. En ordre der stykklisten
  ikke har operasjoner, har ingen arbeidsordrer.

Når lagerstedet ikke har nok av en komponent, blir ordren bekreftet likevel: linjene som
ikke kunne reserveres, blir stående ureservert, og siden navngir hver av dem med mengden
som mangler. Når produktet eller en av komponentene er **utgått** i Produkter, blir ordren
likevel opprettet og bekreftet, og ordresiden varsler **Et utgått produkt**: den bruker
opp det som er igjen. En linje reserveres helt eller ikke i det hele tatt. Et utkast der
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

### Partier og serienumre

Når produktet eller en komponent spores med **partier** eller **serienumre** (variantens
**Sporing** i [Produkter](/nb/user/products/#spor-partier-og-serienumre)), spør
**Registrer produksjon** også etter dem.

**Partiet produksjonen lager.** For et produkt som spores med partier viser skjemaet
**Partinummer**, laget av stykklistens **Mønster for partinummer** slik ordren beholdt det
da den ble bekreftet:

- `{MO}` — ordrens nummer, standarden: alle produksjoner på ordren legges i ett parti,
  *MO-12*, uansett dag.
- `{YYMMDD}`, eller `{YYYY}`, `{YY}`, `{MM}`, `{DD}` — produksjonsdatoen: `L{YYMMDD}-{N}`
  lager et nytt parti for hver produksjon, *L261112-1*, *L261112-2*.
- `{SKU}` — produktets SKU; `{N}` — et løpenummer, talt for hvert prefiks. Et nummer det
  teller, lager alltid et nytt parti: har noen skrevet det nummeret allerede, tas det neste.
- Alt annet skrives som det står. Et partinummer har 1–20 bokstaver, sifre og `. _ / -`;
  stykklisteredigeringen viser et eksempel og nekter et mønster som ikke gir et, vurdert
  med et langt ordrenummer og løpenummer, så et mønster som bare passer dagens korte også
  fanges opp (**Kan gi … på en senere ordre**).

Behold nummeret eller skriv et annet; et nummer produktet allerede har, legger til i det
partiet. **Produksjonsdato** er i dag med mindre du endrer den. **Utløpsdato** og **Best
før** står tomme for å ta produktets eget antall dager fra produksjonsdatoen; en dato du
skriver på et parti som allerede finnes, må være partiets egen (**Partiet finnes med andre
datoer**).

For et produkt som spores med **serienumre** må godkjent antall være helt, og hver enhet
får sitt eget nummer: **Første serienummer** telles opp, ett per enhet — som standard
ordrens nummer og fire sifre, *MO-12-0001*, videre etter det høyeste slike nummeret ordren
laget før. Hver enhet får et nytt serienummer: et som er brukt fra før, selv av en enhet
som ble levert for lenge siden, nektes (**Det serie- eller partinummeret er tatt fra
før**). Kasserte ferdigvarer nummereres aldri.

**Partiene den forbruker.** Under hver sporet komponent viser skjemaet partiene den ville
ta, det som går ut først først — *Tas etter først utløpt, først ut når du bokfører: B-1 (1.
des. 2026) 30 · B-2 10* — eller at ingen er på lager, eller hvor mye partiene på lager
mangler. Lar du det stå, tar Lager partiene når du bokfører, først utløpt først ut, og
hopper over sperrede og utløpte partier. For å ta andre velger du **Bytt parti**: hver rad
er et parti på en lokasjon, valgt blant lagerstedets partier med utløpsdato og det som
ligger der (sperrede og utløpte partier er merket), og antallet som tas fra det. **Legg
til parti** legger til en rad, krysset fjerner en, og **Ta etter først utløpt, først ut**
går tilbake til forslaget. Radene må summere til det linjen forbruker og kasserer;
skjemaet sier fra når de ikke gjør det, og merker en rad på 0 (**Mer enn 0**) og et parti
valgt i to rader (**Valgt i en annen rad**). Når du velger hvor en komponent tas fra (**Ta
… fra**), foreslås partiene som vises på nytt fra den lokasjonen, slik Lager vil ta dem
der. Et utløpt parti kan bare brukes av den som har lov
til å bruke utløpte partier, og som krysser av for **utløpt** på raden; et sperret eller
blokkert parti nektes (**Et parti er sperret eller blokkert og kan ikke brukes**).

Når partier du ikke endret, er tatt av noen andre i mellomtiden, foreslår produksjonen dem
på nytt én gang ut fra det som er på lager, og bokfører. Dekker det som er igjen ikke
lenger linjen, bokføres ingenting, og skjemaet sier hvorfor.

**Ordrens partier.** Fanen **Partier** på ordresiden viser **Partier laget**, med det
produksjonene la i hvert og hvor mange produksjoner, og **Partier forbrukt** per komponent
med utløpsdato, forbrukt og svinn. Hvert parti lenker til siden sin i
[Lager](/nb/user/inventory/), og **Spor** ved siden av åpner
[sporingen](/nb/user/inventory/#spor-et-parti) av det der: partiene laget av det og hvem
som fikk dem. Lenkene er der når du kan se lokasjonene og beholdningen i Lager; ellers
vises partinumrene uten dem. Siden for en arbeidsordre i verkstedet viser det samme under
**Partier**.

### Lukk en ordre

Velg **Lukk** på en bekreftet eller påbegynt ordre når arbeidet er ferdig. Det som
fortsatt er reservert for den, frigjøres, og ordren er **Ferdig**. Tid som fortsatt løper
på arbeidsordrene, avsluttes, og arbeidsordrene som ikke er ferdige, kanselleres.

Når noe godt er produsert, lukkes ordren bare når hver
[kvalitetskontroll](#registrer-kontrollene) på arbeidsordrene har et resultat:
*Kontroller mangler* navngir dem som mangler, så de kan registreres først. En avvist
kontroll er et resultat.

Når mindre enn antallet ble produsert, blir du spurt om du vil **opprette en restordre**:

- **Ja** oppretter en ny ordre for resten — antallet minus det som ble produsert — med
  samme stykkliste, lagersted, lokasjoner, forfallsdato, prioritet og ansvarlig. Den
  nummereres etter den opprinnelige: den første restordren av MO-12 er **MO-12-2**, den
  neste **MO-12-3**, også når den er en restordre av MO-12-2. Den bekreftes med en gang og
  reserverer komponentene sine; linjene den ikke kunne reservere, navngis. Det gjelder også
  når stykklisten er arkivert siden: restordren beholder den, mens en ny ordre eller et
  utkast ikke kan bruke den.
- **Nei** lukker ordren med det som ble produsert.

### Kanseller en ordre

Velg **Kanseller ordre** på et utkast, en bekreftet eller en påbegynt ordre så lenge
ingenting er produsert, forbrukt eller kassert på den. Reservasjonene frigjøres, og den
beholder nummeret sitt. Tid som fortsatt løper på arbeidsordrene, avsluttes, og
arbeidsordrene kanselleres. Når noe er registrert, kan den ikke kanselleres; lukk den i
stedet. En ferdig eller kansellert ordre er endelig.

### Les historikken til en ordre

Ordresiden viser **Historikk**: hver hendelse — opprettet, endret, bekreftet, reservert,
en produksjon registrert med mengdene, en komponent lagt til eller endret, lukket,
kansellert, en restordre opprettet, en tidsregistrering korrigert eller lagt til — med
hvem som gjorde det og når. Ingenting i den kan
endres eller fjernes.

### Arbeidsordrer og tidsregistreringer på ordresiden

Fanen **Arbeidsordrer** på ordresiden lister ordrens arbeidsordrer etter rekkefølge — én
for hver operasjon i stykklisten — med arbeidsstasjonen, statusen, **Registrert /
forventet** minutter, **Faktisk tid** — tiden det ble arbeidet på arbeidsstasjonen, talt
én gang uansett hvor mange operatører som var på den — og **Avvik** fra det forventede, i
minutter og prosent, og hvem som **Kjører nå**. Et utkast har ingen ennå: de lages når det
bekreftes.

De **forventede minuttene** regnes ut én gang, ved bekreftelsen, fra operasjonen og
arbeidsstasjonen slik de var da: riggingen, ryddingen og syklusene ordrens antall trenger
med arbeidsstasjonens kapasitet og effektivitet, rundet til hele minutter — det samme
tallet som [oversikten](#les-oversikten-over-en-stykkliste) gir. Senere endringer i
stykklisten eller arbeidsstasjonen endrer dem ikke.

**Tidsregistreringer** lister hver registrering på ordrens arbeidsordrer: **Person**,
**Arbeidsordre**, **Type** — **Produktiv**, eller **Stopptid** med årsaken — **Startet**,
**Avsluttet** (eller **Kjører**), **Varighet**, **Antall laget** og **Notat**. En
registrering merkes **Utenfor rekkefølge** når arbeidsordren ble startet med Start
likevel, **Korrigert** med hvem som korrigerte den og når, og **Over 12 t** når den har
løpt så lenge.

### Korriger en tidsregistrering

Når en operatør glemte å sette på pause, eller startet på feil arbeidsordre, retter en
arbeidsleder det opp. Det krever **Administrere produksjonsordrer**; uten den er fanen
bare til å lese.

1. I fanen **Arbeidsordrer** på ordren, under **Tidsregistreringer**, velger du
   **Korriger** på registreringen.
2. I **Korriger tidsregistrering** endrer du **Startet**, **Avsluttet** eller **Antall
   laget**, og skriver **Hvorfor** — det er påkrevd, og lagres på registreringen som dine
   ord; ordrens historikk viser korrigeringen, hvem som gjorde den og når.
3. Velg **Korriger**. En registrering som fortsatt løper, avsluttes ved slutten du oppgir.

For å føre tid ingen registrerte, velger du **Legg til manglende registrering**: velg
**Arbeidsordre** (når ordren har flere), **Person**, typen — **Produktiv** eller
**Stopptid** med **Årsak** — **Startet** og **Avsluttet**, eventuelt **Antall laget**, og
**Hvorfor**, og velg **Legg til**. En registrering som legges til, endrer ikke statusen
til noen arbeidsordre: det gjør bare Start, Pause og Ferdig i verkstedet.

En korrigering eller et tillegg nektes når:

- registreringen, eller den nye starten, er mer enn **60 dager** gammel;
- ordren er ferdig eller kansellert;
- tiden overlapper en annen registrering for samme person;
- slutten ikke er etter starten, eller ligger fram i tid.

Hver korrigering og hvert tillegg tas vare på i ordrens **Historikk**, med hvem som gjorde
det og når.

## Verkstedet

**Verksted** (`/manufacturing/shop-floor`) er der operatørene arbeider. Det er laget for et
nettbrett ved arbeidsstasjonen: store knapper, ingen hover, lesbart på armlengdes
avstand. Du arbeider som den som er logget inn, så hver operatør logger inn med sin egen
konto; å bytte operatør betyr å bytte konto.

Å arbeide i verkstedet krever **Arbeide i verkstedet** (`manufacturing:shop-floor`), eller
**Administrere produksjonsordrer**, ved siden av Bruke Produksjon. Med bare Bruke
Produksjon kan du se køene og arbeidsordrene, og en arbeidsordre sier *Du kan se, men ikke
starte eller fullføre arbeid*: det finnes ingen Start, Pause, Ferdig, Registrer produksjon,
Meld svinn, Blokker eller Opphev blokkering.

En arbeidsordre går gjennom disse statusene:

| Status | Hva den betyr |
| --- | --- |
| **Venter** | En tidligere operasjon i ordren er ikke ferdig ennå |
| **Klar** | Den kan startes |
| **Pågår** | Noen arbeider på den |
| **Pauset** | Den er startet, og ingen arbeider på den nå |
| **Ferdig** | Fullført |
| **Kansellert** | Ordren ble lukket eller kansellert før den var ferdig |

### Velg arbeidsstasjonen din

1. Åpne **Verksted**.
2. Under **Velg arbeidsstasjon** velger du den du arbeider ved. En blokkert
   arbeidsstasjon er merket **Blokkert**.

Nettbrettet husker arbeidsstasjonen det sist ble brukt ved, og tilbyr den først neste
gang, som **Fortsett ved** med koden og navnet, over de andre. Valget lagres bare i
nettleseren: på en annen enhet, eller etter at nettleserens data er slettet, velger du
bare på nytt.

Siden for arbeidsstasjonen viser **Kø**: arbeidsordrene som er klare, pågår eller er
pauset, i ordrer som fortsatt er åpne — først etter planlagt start, så de hastende
ordrene, så etter forfallsdato. Hver viser ordrens nummer, produktet, antallet som
gjenstår, forfallsdatoen, om komponentene er reservert, og hvem som kjører den. Køen
oppdaterer seg selv når andre starter og fullfører arbeid. **Mitt arbeid** står øverst på
hver side i verkstedet.

### Start, sett på pause og fullfør en arbeidsordre

1. Velg arbeidsordren i **Kø**. Arbeidsordren viser ordren, antallet som gjenstår,
   fristen, **Registrert / forventet** minutter, operasjonens **Instruksjoner** og
   **Komponenter i denne operasjonen**. Under **Personer** viser **Kjører nå** hvem som
   arbeider på den, og **Andre her** hvem andre som har den åpen akkurat nå.
2. Velg **Start**. **Din tid** løper fra det øyeblikket. Den første starten på en ordre
   setter den til **Pågår**.
3. Velg **Pause** for en pause, eller når du forlater jobben for nå. Tiden din stopper;
   når ingen andre arbeider på den, er arbeidsordren **Pauset**. Velg **Start** for å
   fortsette.
4. Velg **Ferdig** når operasjonen er gjort for hele ordren, og bekreft **Fullføre denne
   arbeidsordren?**. Å fullføre avslutter tiden til alle som fortsatt arbeider på den,
   setter den til **Ferdig**, og gjør neste operasjon **Klar** på arbeidsstasjonen sin.
   Ferdig tilbys ikke på en arbeidsordre som venter. Å fullføre en som noen andre nettopp
   har fullført, er ikke en feil.

Flere operatører kan arbeide på én arbeidsordre samtidig, hver med sin egen tid.

### Start en arbeidsordre utenfor rekkefølgen

Operasjonene kjøres i rekkefølge: en arbeidsordre **Venter** til den før er ferdig. Når du
velger **Start** på den, får du beskjed om at *En tidligere operasjon er ikke ferdig*. Når
arbeidet overlapper i praksis, velger du **Start likevel**: arbeidet starter som vanlig,
og tidsregistreringen merkes **Utenfor rekkefølge**, så arbeidslederen ser det.
**Avbryt** lar den bli stående og vente.

### Bytt til en annen arbeidsordre

Du kjører én arbeidsordre om gangen, på tvers av alle arbeidsstasjoner. Når du åpner en
annen arbeidsordre mens tiden din løper, varsler den *Du kjører en annen arbeidsordre*,
med **Åpne den andre arbeidsordren**. Velger du **Start** likevel, avsluttes tiden din på
den andre — du trenger ikke sette den på pause først — og du får beskjed om at *Den andre
registreringen din ble stoppet*. Arbeidsordren du forlot, beholder de andre som arbeider
på den; var du den siste, er den **Pauset**.

### Registrer produksjon på den siste operasjonen

På ordrens siste operasjon står **Registrer produksjon** ved siden av **Ferdig**. Den
åpner skjemaet fra ordresiden — **Antall** laget, eventuelle **Kasserte ferdigvarer**, og
hver komponents forhåndsutfylte **Forbrukt** og **Svinn** (se
[Registrer en produksjon](#registrer-en-produksjon)), med samme advarsel når et tall
avviker mer enn 10 %, og partiet, serienumrene og komponentenes partier fra
[Partier og serienumre](#partier-og-serienumre). Å velge **Bokfør** registrerer
produksjonen og lar arbeidsordren løpe videre, så du kan registrere mer underveis.

Når dette er det siste, krysser du også av for **Fullfør også denne operasjonen**:
produksjonen registreres **og** arbeidsordren fullføres, sammen — noe som stopper tiden for
alle som jobber med den. Når produksjonen nektes, for eksempel fordi lagerstedet ikke har
nok av en komponent, registreres ingenting, arbeidsordren fullføres ikke, og tiden
fortsetter å løpe. Sendes den to ganger, bokføres den bare én gang.

Å lukke ordren gjøres fortsatt på ordresiden, av en med Administrere produksjonsordrer.

### Registrer kontrollene

En arbeidsordre med kvalitetskontroller viser panelet **Kontroller**: kontrollene for
første stykk som ikke er registrert øverst, så resten i rekkefølge. Hver viser typen, når
den skal gjøres, instruksen og, for en måling, grensene. Registrer hver med de store
knappene:

- en instruks: **Utført**;
- godkjent eller avvist: **Godkjent** eller **Avvist**;
- en måling: skriv verdien i **Målt verdi** — nettbrettet viser talltastaturet, og
  knappen **±** ved feltet gjør verdien negativ, siden tastaturet ikke har minus — og
  velg **Registrer**. Resultatet, **Godkjent** eller **Avvist**, er Vantigos, vurdert mot
  grensene;
- et bilde: **Ta bilde** åpner kameraet; bildet lagres med resultatet. Et stort bilde
  gjøres mindre på nettbrettet før det sendes — høyst 2560 piksler på den lengste siden,
  som JPEG. En JPEG eller PNG på høyst 10 MB; det kameraet skriver inn i et bilde om hvor
  og når det ble tatt, fjernes før det lagres.

En **Merknad** er valgfri på hver kontroll. Å registrere en kontroll på nytt erstatter
resultatet; det forrige beholdes, med hvem som registrerte det og når. Ingenting kan
registreres på en arbeidsordre som er ferdig eller kansellert.

Det kontrollene stopper:

- **Ferdig** nektes mens en kontroll på arbeidsordren, av begge slag, mangler resultat:
  *Kontroller mangler*, med kontrollene navngitt.
- **Registrer produksjon** — på ordresiden eller på den siste operasjonen — nektes mens
  en kontroll på en tidligere operasjon mangler resultat, eller en kontroll for **første
  stykk** på den siste operasjonen mangler et. Avvisningen navngir kontrollene. Å bare
  melde svinn stoppes ikke.
- **Lukk** på ordresiden nektes, når noe godt er produsert, mens en kontroll på ordrens
  arbeidsordrer mangler resultat.
- Å starte stoppes aldri.

**Avvist** stopper ingenting: det oppretter straks et avvik, navngitt på kontrollen —
**DEV-12**, for eksempel — som en arbeidsleder tar stilling til. Når kontrollen har
**Karantene ved avvik**, setter avviket det ordren lager i karantene. Avvises den samme
kontrollen igjen mens avviket er åpent, opprettes ikke et nytt: den nye målingen legges
til det åpne avviket, og kontrollen viser til det.

Ordresidens fane **Arbeidsordrer** viser hver kontroll under **Kontrollresultater**, med
resultatet, hvem som registrerte det og når, og avviket en avvisning opprettet.

### Meld avvik

Velg **Meld avvik** på en arbeidsordre, på ordresiden, i listen **Avvik** eller på siden
til et parti i Lager. Oppgi **Tittel**, en **Beskrivelse** om du vil, og kryss av for
**Sett produksjonen i karantene** for å holde det ordren har laget — se [Avvik](#avvik).
Å melde avvik krever samme rettighet som verkstedet; på et parti også Lagers rettigheter
til å se lagersteder og beholdning. Et avvik på en ordre som navngir et parti, må navngi
et parti ordren har laget eller brukt.

### Meld svinn

Velg **Meld svinn** på arbeidsordren når komponenter går tapt eller ferdige enheter
kastes, i hvilken som helst operasjon. Oppgi svinnet for hver komponent og **Kasserte
ferdigvarer**. Det registreres som en produksjon uten gode enheter: komponentene tas ut av
lageret i Lager, og ordrens tall teller dem — en komponent som spores med partier fra
partiene som går ut først. Sendes det to ganger, bokføres det bare én gang.

### Blokker en arbeidsstasjon som har stoppet

Når maskinen bryter sammen, eller arbeidet stopper av en annen grunn:

1. Velg **Blokker** på siden for arbeidsstasjonen.
2. Velg **Årsak** — **Maskinstans**, **Mangler materiale**, **Omstilling**, **Venter på
   operatør**, **Kvalitet** eller **Annet** — og skriv eventuelt et notat. Notatet er
   ditt: det lagres med navnet ditt, og forsvinner når brukeren din slettes.
3. Velg **Blokker**.

Fra det øyeblikket registreres tiden til alle som arbeider ved arbeidsstasjonen, som
stopptid med den årsaken, arbeidsstasjonens egen stopptid telles, og ingen kan starte en
arbeidsordre der: arbeidsstasjonen og arbeidsordrene viser **Blokkert** med årsaken,
siden når og hvem som blokkerte den, og Start tilbys ikke. **Oversikt** teller den under **Blokkerte
arbeidsstasjoner**. Å blokkere en arbeidsstasjon som allerede er blokkert, endrer
ingenting.

Mens den er blokkert, avslutter **Pause** eller **Ferdig** stopptiden din. Når den går
igjen, velger du **Opphev blokkering**: stopptiden avsluttes, og arbeidsordrene blir
stående **Pauset** — velg **Start** på din for å fortsette.

### Mitt arbeid

**Mitt arbeid** (`/manufacturing/shop-floor/mine`) viser under **Kjører nå** arbeidsordren
du kjører, med tidtakeren din og **Åpne arbeidsordren**, og under **I dag**
arbeidsordrene du har registrert tid på i dag.

### Tid som blir stående og løpe

Tiden avsluttes ikke av seg selv ved slutten av dagen. Når tiden din har løpt i mer enn
**12 timer**, viser tidtakeren *Har kjørt i mer enn 12 timer*: sett den på pause hvis du
har sluttet, eller be en arbeidsleder [korrigere tiden](#korriger-en-tidsregistrering).
En arbeidsordre nevner også de andre som har hatt tiden løpende så lenge, og ordrens
tidsregistreringer merker en slik registrering **Over 12 t**.

## Produksjonsplanen

**Produksjonsplan** (`/manufacturing/schedule`) viser når hver arbeidsordre er planlagt å
gå, i arbeidsstasjonens kalender. Alle med Bruke Produksjon leser den; å planlegge, flytte
og fjerne en plan krever **Administrere produksjonsordrer**.

### Planlegg en ordre

På en bekreftet eller påbegynt ordre velger du **Planlegg**. Hver arbeidsordre som ikke er
ferdig, legges inn i arbeidsstasjonens kalender, den ene etter den andre i rekkefølgen
sin, i den første ledige tiden som er lang nok: arbeidsstasjonens åpningstid, minus tiden
andre arbeidsordrer allerede har der. En arbeidsordre som ikke får plass i ett strekk,
deles i biter rundt pauser, netter, stengte dager og andre bookinger. Ordrens **Planlagt**
går da fra starten på den første biten til slutten på den siste.

- Planleggingen starter fra dagen i **Ønsket start**, eller fra nå hvis den er passert.
  Når ordren ikke har ønsket start, regnes standarden ut fra forfallsdatoen:
  forfallsdatoen minus arbeidsdagene den forventede tiden trenger, med 7,5 timer per dag,
  minus én dag til — og aldri før i dag. Uten forfallsdato er den i dag.
- En arbeidsordre som allerede er i gang, eller på pause, planlegges for tiden den
  fortsatt trenger — de forventede minuttene minus tiden som allerede er registrert på
  den, telt én gang når flere operatører jobbet på den samtidig — fra nå. En ferdig
  arbeidsordre flyttes aldri.
- Å planlegge en ordre som allerede har en plan (**Planlegg på nytt**), erstatter den
  gamle planen.
- En ordre uten arbeidsordrer får datoene fra forfallsdatoen minus stykklistens
  ledetid til forfallsdatoen.

Når planlagt ferdig faller etter forfallsdatoen, viser ordresiden **Forsinket n dager**,
og stolpene på tavlen er røde. Når en arbeidsordre ikke finner plass i
arbeidsstasjonens kalender innen 365 dager, planlegges ingenting, og du blir bedt om å
frigjøre kapasitet eller endre kalenderen.

**Fjern plan** fjerner ordrens plan: de planlagte datoene til ordren og til de uferdige
arbeidsordrene tømmes, og tiden deres i kalenderne blir ledig igjen.

### Les tavlen

1. Åpne **Produksjonsplan** og velg **Lagersted**. Tavlen husker lagerstedet i denne
   nettleseren.
2. Velg **Uke** eller **To uker** under **Dager som vises**, og bla med **Tidligere**,
   **I dag** og **Senere**.

Tavlen har en rad for hver aktive arbeidsstasjon på lagerstedet og en kolonne for hver
dag. Hver planlagte bit av en arbeidsordre er en stolpe på sitt tidspunkt, merket med
ordrenummeret og produktet; en ordre som haster, er merket med en flamme. En stolpe er rød
når ordren blir ferdig etter forfallsdatoen. En dag arbeidsstasjonen er stengt — en
helligdag (**Helligdag**) eller en av stengningene (**Stengt**) — er grå, og det samme er
raden til en arbeidsstasjon som er **Blokkert**.

En arbeidsordre som ble ferdig før den planlagte tiden, beholder stolpene sine bare fram
til den ble ferdig: tiden etter er ledig igjen og teller ikke lenger som planlagt.

Tavlens dager er forretningskalenderens dager. Under hver dag er **belastningen** tiden som
er planlagt på arbeidsstasjonen den dagen, som en andel av timene den er åpen, for eksempel
**Belastning 75 %**. Over 100 % er den merket **Overbelastet**. Tavlen oppdaterer seg selv
når noen planlegger eller flytter arbeid på lagerstedet, blokkerer en arbeidsstasjon eller
opphever blokkeringen, legger til eller fjerner en stengning, eller endrer ukemønsteret til
en arbeidsstasjon.

### Planlegg ordrene som ikke er planlagt

**Ikke planlagt**, under tavlen, lister lagerstedets bekreftede og påbegynte ordrer som
ikke har noen plan — ingen av de uferdige arbeidsordrene deres har tid på tavlen — med
forfallsdato, **Ønsket start** og **Forventet tid**. Velg
**Planlegg** på én, eller **Planlegg alle** for å planlegge alle: de som haster først,
deretter etter forfallsdato — de uten sist. En ordre som ikke finner kapasitet innen 365
dager, blir stående uplanlagt og navngitt, og beholder det den hadde; de andre planlegges
rundt den.

### Flytt en arbeidsordre

Dra en stolpe langs raden til en annen dag eller tid og slipp. En arbeidsordre som er delt
i biter, flyttes i sin helhet: drar du en av stolpene, flyttes starten på arbeidsordren like
langt som du dro. Eller trykk på stolpen, og i **Flytt … til …** velger du **Dag** og
**Tid** og **Flytt**; dialogen starter på arbeidsordrens planlagte start, dagen og tiden
er i din egen tidssone, og den tilbyr også å åpne ordren.

Arbeidsordren får den første ledige tiden fra tidspunktet du valgte — fra nå, hvis det er
passert. De senere arbeidsordrene i samme ordre følger etter bare når de ellers ville
startet før den er ferdig; arbeidsordrer i andre ordrer flyttes aldri. Å flytte til en
annen arbeidsstasjon er ikke mulig ennå.

## Kostnaden for en ordre

Bare de med **Se produksjonskostnader** ser hva en ordre koster. For dem viser ordresiden
et **Kostnad**-panel med, for hver del — **Komponenter**, **Maskin**, **Arbeid** og
**Totalt** — **Estimat**, **Faktisk**, **Avvik** og **Avvik %**.

- **Estimatet** lages når ordren bekreftes: komponentenes nødvendige mengder til
  standardkost, og arbeidsordrenes forventede timer til arbeidsstasjonens maskinsats og
  arbeidssats, for én operatør. Et utkast har ikke noe ennå.
- **Det faktiske** løper mens ordren er åpen (**Løpende**): det som ble forbrukt og
  kassert, til samme standardkost; maskinen for tiden det ble arbeidet på hver
  arbeidsordre, talt én gang uansett hvor mange operatører som var på den; og arbeid for
  hver operatørs produktive tid. Det låses når ordren lukkes (**Låst ved avslutning**),
  sammen med **Kostnad per enhet**: den faktiske totalen delt på de gode enhetene som ble
  laget.
- **Nedetid per årsak** lister tiden arbeidsstasjonene sto stille. Den belastes aldri.

Når en komponent manglet standardkost, eller en arbeidsstasjon manglet sats, regnes den
som 0, og panelet sier at estimatet eller det faktiske er ufullstendig. Beløpene rundes
til to desimaler, så delene kan avvike fra totalen med 0,01.

## Rapporter

På **Oversikt** velger du **Rapporter**. Velg perioden med **Fra** og **Til** — de siste
30 dagene hvis du ikke endrer dem, høyst 366 dager — og eventuelt et **Lagersted**. Hver
rapport har **Last ned CSV**, en fil som åpnes i et regneark med norsk tallformat.

- **Produksjonskostnad** — ordrene som ble lukket i perioden, etter produkt: hver ordres
  estimat, faktiske kostnad, avvik, avvik % og kostnad per enhet, med summer per
  produkt. Et produkts produserte sum og kostnad per enhet teller bare ordrene som har en
  faktisk kostnad. Den vises bare for dem med **Se produksjonskostnader**.
- **Utnyttelse av arbeidsstasjoner** — per arbeidsstasjon og uke: **Kapasitet**, tiden
  **Planlagt**, tiden **Produktiv**, **Nedetid** per årsak, og **Utnyttelse**, den
  produktive tiden som andel av kapasiteten. En arkivert arbeidsstasjon vises bare for
  ukene noe ble planlagt eller registrert på den.
- **Tidsavvik** — per operasjon i en stykkliste: **Gjennomsnittlig forventet** og
  **Gjennomsnittlig faktisk** minutter over de siste 10 arbeidsordrene som ble ferdige i
  perioden, og avviket i prosent. Bruk den til å se hvilke standardtider som bør
  justeres.

## Produser mot ordre

Et produkt som produseres mot ordre, lages for hver salgsordre i stedet for til lager.
Salg og produksjon følger da hverandre: når en salgsordre i [Ordre](/nb/user/orders/)
bekreftes, lager Produksjon produksjonsordren for den.

### Produser et produkt mot ordre

Åpne produktets **standard** stykkliste, slå på **Produser mot ordre** og velg **Lagre**.
Bare standardstykklisten teller: en alternativ stykkliste merket Produser mot ordre brukes
ikke for salgsordrer.

### Ordrer laget for en salgsordre

Når en salgsordre bekreftes, lager Produksjon innen et minutt én **bekreftet**
produksjonsordre for hver av varelinjene der produktet produseres mot ordre:

- for linjens åpne antall, med forfall på salgsordrens leveringsdato;
- på lagerstedet til stykklistens arbeidsstasjoner. En stykkliste uten operasjoner lages
  på salgsordrens lagersted; navngir ikke salgsordren noe heller, lages ingen ordre for
  den linjen;
- med opphav **Salgsordre** og salgsordrens nummer, for eksempel *SO-1042*.

Komponentene reserveres som når du bekrefter en ordre selv; en komponent som mangler,
stopper den aldri, og ordresiden navngir det som mangler. En linje som allerede har en
produksjonsordre, får ingen til. Et antall som økes på salgsordren senere, følges ikke:
salgsordrens linje viser hva som lages for den, og du lager resten selv.

### Lag en ordre for en salgsordrelinje selv

På salgsordrens side i Ordre tilbyr en varelinje der produktet har en stykkliste
**Opprett produksjonsordre**. Den åpner **Ny produksjonsordre** her, fylt ut fra linjen —
produktet, det åpne antallet og leveringsdatoen som forfallsdato — og sier *Lages for
SO-1042*. Endre det du trenger, og velg **Opprett produksjonsordre**; bekreft den deretter
som enhver annen. En linje har høyst én produksjonsordre om gangen, restordrene unntatt:
når én er laget, viser linjen den i stedet. Å kansellere ordren frigjør linjen.

### Når salgsordren kanselleres

En kansellert salgsordre kansellerer **ikke** produksjonsordrene som ble laget for den:
varene kan allerede være halvferdige. Hver merkes **Salgsordre kansellert**, i listen og
på siden sin, og historikken sier det. Avgjør om den skal fullføres, lukkes eller
kanselleres. En restordre av en slik ordre merkes også; underordrene dens merkes ikke — se
på den overordnede. En salgsordre som kanselleres før produksjonsordrene er laget, får
ingen.

### Når ordren er ferdig

Når du lukker en ordre laget for en salgsordrelinje, sier salgsordrens linje at varene er
produsert og klare til reservering, og **Reserver nå** der reserverer dem — når de ble
laget på salgsordrens lagersted. Laget på et annet lagersted må varene flyttes dit først;
linjen sier det.

### Opprett underordrer

En komponent som selv produseres — den har en standard stykkliste — kan lages for en
ordre. På en bekreftet eller pågående ordre der produserte komponenter mangler noe, velger
du **Opprett underordrer**. Underordrene som allerede er åpne for ordren, listes under
**Lages allerede**, med det hver har igjen å lage. Hver komponent som mangler mer enn de
lager, listes med resten den mangler, hver avkrysset; fjern krysset for dem du ikke skal
lage nå, og velg **Opprett underordrer**. Hver blir en bekreftet ordre:

- for det komponenten mangler utover de åpne underordrene, etter sin standard stykkliste;
- med opphav **Overordnet ordre**, lenket til denne ordren;
- med forfall når denne ordren er planlagt å starte, eller på den ønskede starten når den
  ikke er planlagt.

Ordresiden lister **Underordrer**, og hver av dem lenker til sin **Overordnet ordre**.
Ingenting lages automatisk. Det en underordre lager, reserveres ikke for den overordnede
av seg selv: velg **Reserver nå** på den overordnede når det er på lager.

## Avvik

Et **avvik** registrerer noe som gikk galt: en avvist kontroll, en feil funnet ved
stasjonen, et parti som ikke er som det skal. Hvert har et nummer, **DEV-1** og oppover,
tittel og beskrivelse, og det det ble meldt fra: en ordre, en av arbeidsordrene, et
kontrollresultat eller et parti.

### Karantene

Et avvik som **setter produksjonen i karantene** — meldt med avkrysningen, eller opprettet
av en avvist kontroll med **Karantene ved avvik** — setter en karantenesperre på:

- hvert parti ordren har laget så langt;
- hvert parti en produksjon av ordren lager, eller legger til, mens avviket er åpent — så
  et første stykk som avvises før noe er laget, holder likevel partiet ordren lager
  etterpå;
- partiet det ble meldt fra, for et som er meldt på siden til et parti.

Et parti i karantene tas ikke av leveranser eller produksjoner før sperrene er opphevet
(se [Partier](/nb/user/inventory/) i Lager). Sperren er avvikets: partisiden viser den med
avviksnummeret og en lenke, og kan ikke oppheve den der. Bare produkter som spores med
partier eller serienumre har partier å holde.

### Finn et avvik

**Avvik** viser dem med det nyeste først, de åpne med mindre du endrer **Status**; filtrer
på perioden de ble opprettet i. En lenke fra en ordre eller et parti viser avvikene til
den ordren eller det partiet. **Last ned CSV** gir alle avvik filtrene treffer, som en
fil. Siden til et avvik viser hvor det kom fra, beskrivelsen og **Partier i karantene** —
hver lenket til siden sin i Lager, for dem som kan se beholdning.

### Lukk et avvik

Å lukke krever **Administrere kvalitet** (`manufacturing:quality-manage`).

1. Åpne avviket.
2. Under **Lukk avviket** velger du **Disponering**:
   - **Bruk som den er** eller **Omarbeid** — sperrene oppheves når det lukkes;
   - **Kasser** eller **Returner** — sperrene blir stående: det som er igjen av partiet,
     skal ikke brukes.
3. Skriv **Avslutningsnotat**, og velg **Lukk avviket**.

Å lukke opphever bare avvikets egne sperrer. Et parti som et annet avvik holder, eller som
en tilbakekalling har sperret, blir holdt til også det er opphevet.

Etter **Kasser** eller **Returner** skriver du av det som er igjen med en
[justering](/nb/user/inventory/) i Lager, eller returnerer det, og velger så **Opphev
sperrene** på avviket.

## Påfyllingsforslag

**Forslag** (under **Produksjonsordrer** i sidemenyen) forteller hva du bør produsere,
eller kjøpe inn, før et produkt går tomt. Det bygger på **bestillingspunktet**,
**maksbeholdningen** og **multiplumet** som er satt på produktets påfylling på et
lagersted i Lager — se
[Angi hovedlokasjon og bestillingspunkt for en vare](/nb/user/inventory/#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare)
— og bare produkter med et bestillingspunkt der vurderes.

For hvert slikt produkt og lagersted regner Produksjon ut den **fremskrevne**
beholdningen: det som er brukbart på lager (partier på vent og utgåtte partier holdes
utenfor), minus det som er reservert, pluss det som er på vei dit med en overføring, pluss
det åpne produksjonsordrer — og utkastene laget fra tidligere forslag — fortsatt skal
produsere av det, minus det åpne ordrer og disse utkastene fortsatt trenger av det som
komponent og ikke har reservert, minus det åpne salgsordrer fortsatt trenger av det og
ikke har reservert. En ordre produsert mot ordre for en salgsordre holdes utenfor, og
salgsordrelinjen den lages for, teller bare for det ordren ikke fortsatt skal produsere:
det ordren allerede har produsert, er på lager, lovet bort til linjen.

Når den fremskrevne beholdningen er på eller under bestillingspunktet, foreslås produktet:

- **Produsere** når det har en aktiv standardstykkliste, **Kjøpe** når det ikke har det.
- **Antallet** fyller opp til maksbeholdningen — eller til én over bestillingspunktet når
  det ikke er noen maksbeholdning — rundet opp til et helt antall av multiplumet når det
  er satt. Det er alltid minst 1, som på lavbeholdningslisten i Lager, også for et
  produkt som telles i desimaler.
- **Trengs innen** er i dag når det som er på lager, minus det som er reservert, allerede
  er på eller under bestillingspunktet; ellers i dag pluss stykklistens ledetid, i
  virkedager. Et innkjøpsforslag trengs i dag.
- **Begrunnelsen** sier det i tall: *Fremskrevet 6 er på eller under bestillingspunktet
  20: brukbar beholdning 30, reservert 25, innkommende 2, i produksjon 3, trengs i
  produksjon 0, trengs til salgsordrer 4.* Det salgsordrene trenger, vises bare som en
  sum: ingen kunde eller ordre navngis.

Forslagene lages hver natt kl. 02.00, i virksomhetens egen tidssone, og hver natts
forslag erstatter de forrige. **Kjør nå** lager dem med en gang (høyst én gang i
minuttet for hvert lagersted); siden viser når de sist ble laget, leser dem på nytt til
kjøringen er ferdig, og hver gang et lagersteds forslag endres. Når Ordre ikke er i bruk, sier
**Salgsordrer er ikke regnet med** at etterspørselen fra salg holdes utenfor.

### Lag ordrer fra forslag

1. Åpne **Forslag**. Hvert lagersted har sin egen tabell, med produsere-radene først.
2. Kryss av radene som skal produseres. Endre et **antall** hvis du vil ha et annet —
   mer enn 0, med ditt desimaltegn.
3. Velg **Opprett utkast** for å få utkast å se over, eller **Opprett og bekreft** for å
   bekrefte dem med en gang og reservere komponentene slik **Bekreft** gjør — mangel
   stopper det ikke.

Hver rad blir én produksjonsordre, med opprinnelse **Forslag**, på forslagets lagersted,
med forfall på datoen det trengs innen, fra produktets standardstykkliste. En rad det ikke
kunne lages ordre fra, sier hvorfor — blant annet et produkt som allerede har en ordre fra
et tidligere forslag som fortsatt er et utkast eller åpen på lagerstedet; forslaget vises
da med *En produksjonsordre fra et tidligere forslag er åpen*. Å opprette ordrer krever **Administrere
produksjonsordrer**.

**Avvis** skjuler et forslag til neste kjøring, som foreslår det igjen hvis det fortsatt
trengs. Kjøpe-rader kan ikke gjøres om til ordrer — Vantigo har ikke innkjøp ennå — men
**Eksporter innkjøpsforslag** laster dem ned som en CSV-fil å bestille fra.

### Automatisk oppretting

På et produkts standardstykkliste viser **Automatisk påfylling** hvert aktive lagersted
med to brytere, begge av som standard:

- **Opprett automatisk** — nattkjøringen oppretter selv den foreslåtte
  produksjonsordren, som utkast.
- **Bekreft også** — og bekrefter den.

Nattkjøringen oppretter aldri en ordre til mens en fra et tidligere forslag for produktet
fortsatt er et utkast eller åpen på lagerstedet; forslaget vises da med *En
produksjonsordre fra et tidligere forslag er åpen*. Ordrer laget slik opprettes uten at
noen har sett på dem, så siden advarer før du slår det på. Å endre det krever
**Administrere produksjonsordrer**.

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
| En ordres **Kostnad**-panel, og rapporten **Produksjonskostnad** | ✓ | – | ✓ |

Hver rad krever alle rettighetene den krysser av. **Se produksjonskostnader** er sensitiv:
en administrator gir den bare til dem som skal se hva ting koster å lage, og den kan ikke
delegeres.

| Hva | Bruke Produksjon | Administrere produksjonsordrer | Arbeide i verkstedet |
| --- | --- | --- | --- |
| **Produksjonsordrer**, en ordre, tilgjengeligheten, produksjonene og **Historikk** | ✓ | – | – |
| **Ny produksjonsordre**, **Lagre**, **Bekreft**, **Reserver nå**, **Legg til komponent**, å endre en komponent, **Lukk**, **Kanseller ordre** | ✓ | ✓ | – |
| **Registrer produksjon** | ✓ | en av dem | en av dem |
| **Verksted**, en arbeidsstasjons **Kø**, en arbeidsordre, **Mitt arbeid**, en ordres **Arbeidsordrer** og **Tidsregistreringer** | ✓ | – | – |
| **Start**, **Start likevel**, **Pause**, **Ferdig**, **Meld svinn**, **Blokker**, **Opphev blokkering** | ✓ | en av dem | en av dem |
| **Korriger**, **Legg til manglende registrering** | ✓ | ✓ | – |
| **Produksjonsplan**, **Rapporter** (uten produksjonskostnad) | ✓ | – | – |
| **Planlegg**, **Planlegg på nytt**, **Fjern plan**, **Planlegg alle**, å flytte en arbeidsordre, **Ønsket start** | ✓ | ✓ | – |
| **Opprett underordrer**, og **Opprett produksjonsordre** på en salgsordres linje | ✓ | ✓ | – |
| **Avvik**, et avvik, ordrens **Kontrollresultater** | ✓ | – | – |
| Å registrere en kontroll, **Ta bilde**, **Meld avvik** | ✓ | en av dem | en av dem |
| **Forslag**, en stykklistes **Automatisk påfylling** | ✓ | – | – |
| **Opprett utkast**, **Opprett og bekreft**, **Avvis**, **Kjør nå**, bryterne for automatisk påfylling | ✓ | ✓ | – |

Å registrere en produksjon, og hver handling i verkstedet, krever Bruke Produksjon og en
av de to andre. Å lukke et avvik og **Opphev sperrene** krever **Administrere kvalitet**
(`manufacturing:quality-manage`) i tillegg til Bruke Produksjon; å endre kontrollene på en
operasjon er en del av å endre stykklisten. Assistenten utfører ingen handlinger i verkstedet: en operatør stempler
inn selv.
Beholdningstallene i oversikten og på en ordre er Produksjons egne og krever ingen
rettighet i Lager. En ordre laget for en salgsordre navngir salgsordrens kunde bare for dem som også har Bruke Ordre. Hele tabellen står under [Permissions](/en/reference/manufacturing/#permissions) i
referansen.
