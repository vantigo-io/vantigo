---
title: Utlegg
description: Utlegg, kjøregodtgjørelse og reiseregninger, kvitteringer, godkjenning og utbetaling.
sidebar:
  order: 40
sources:
  - apps/expenses/frontend
---

I Utlegg-appen fører du opp det du har betalt for og kilometrene du har kjørt, samler
kostnadene på en reise i en reiseregning, og sender alt til godkjenning. Når et utlegg
er godkjent, kan det utbetales til deg gjennom lønn, og — hvis det er ført på et
prosjekt — viderefaktureres til kunden. Reglene bak hvert steg står i
[referansen for Utlegg-modulen](/en/reference/expenses/); denne siden handler om hva du
gjør på hvert skjermbilde.

## Hvem ser hva

Sidemenyen viser områdene rettighetene dine gir deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Mine utlegg** | alle med `expenses:access` |
| **Godkjenning av utlegg** | `expenses:approve` — en prosjektleder godkjenner sitt eget prosjekts utlegg uten den, og når den samme køen fra oppmerksomhetslisten på dashbordet |
| **Refusjoner** | `expenses:manage` |
| **Utleggsinnstillinger** | `expenses:manage` |

`expenses:manage` lar deg også føre utlegg for en kollega og arbeide på dager
periodelåsen har stengt; `expenses:view-all` lar deg se alles utlegg. Hele tabellen
står under [Permissions](/en/reference/expenses/#permissions).

## Finn fram i Mine utlegg

**Mine utlegg** åpner med tre tall: **I utkast**, **Venter på godkjenning** og
**Godkjent, ikke utbetalt** (eller **Ingenting til gode**). Under dem avgrenser
filtrene — **Status**, **Type**, **Utbetalt**, **Fra** og **Til** — begge listene på
siden: **Reiseregninger** først, så **Utlegg**. De to listes hver for seg, med hver sin
blaing, fordi en reise er én enhet uansett hvor mange utlegg den har. Så lenge et
**Type**-filter er satt, vises ikke reiseregningene — en reise er ikke av én type.

Hver utleggsrad viser dato, beskrivelse, type, prosjekt, beløp, status og
kvitteringer, og handlingene du kan gjøre på den akkurat nå: redigere, sende til
godkjenning og slette. Et avvist utlegg bærer begrunnelsen i rødt, og et utbetalt sier
**Utbetalt** med datoen.

## Før et utlegg

1. Trykk **Nytt utlegg**. **Type**-velgeren tilbyr **Utlegg**, **Kjøregodtgjørelse**
   og — når prosjektmodulen er på — **Leverandørfaktura**. Typen låses når utlegget er
   lagret.
2. Fyll inn **Dato** og **Beskrivelse**. Dager før periodelåsen er grået ut, og
   skjemaet sier det; se [the period lock](/en/reference/expenses/#the-period-lock).
3. Velg en **Kategori**, oppgi **Leverandør** om du vil, og oppgi **Beløp inkludert
   mva.** i installasjonens valuta, som vises under feltet. Under **Mva.** fyller
   hjelperen **Regn ut mva.** inn mva. med 25 %, 15 % eller 12 % av beløpet; et tall du
   skriver selv vinner. **Netto** vises ved siden av.
4. Under **Hvem betalte** velger du **Jeg betalte** eller **Firmaet betalte**. Et
   utlegg firmaet betalte gir deg ingenting til gode og når aldri lønn; se
   [what "owed to the employee" is](/en/reference/expenses/#money-rules).
5. Hvis prosjektmodulen er på, velger du et **Prosjekt** og, hvis prosjektet har dem,
   en **Linje**, og slår på **Fakturerbar** hvis kunden skal belastes. Du kan bare føre
   på et prosjekt du kan føre timer på; ellers sier skjemaet det, og utlegget føres
   uten. Hva kunden faktureres settes fra prosjektets side, ikke her.
6. Trykk **Lagre utkast** for å holde det åpent og legge ved kvitteringer, eller
   **Lagre og send inn** for å sende det til godkjenning med en gang.

Et nytt utlegg står åpent etter **Lagre utkast**, så kvitteringene kan legges ved.

## Legg ved kvitteringer

Kvitteringer kan bare legges ved et utlegg som finnes, så lagre det som utkast først.
Slipp så filene på kvitteringsfeltet, velg dem med filvelgeren, eller bruk **Ta et
bilde** på en telefon. En kvittering er JPEG, PNG, HEIC eller PDF, høyst 10 MB, og et
utlegg kan ha høyst ti; en fil som ikke passer avvises med navn. Klikk på et bilde for
å vise det (en PDF åpnes i en ny fane), og fjern en kvittering med krysset ved siden av
så lenge utlegget fortsatt er ditt å redigere.

Hvis installasjonens kvitteringsregel gjelder et utlegg du selv har betalt, sier
skjemaet **Et utlegg du selv har betalt på mer enn … må ha kvittering før det kan
sendes inn**, og innsendingen avvises til en er lagt ved. Kjøregodtgjørelse har aldri
kvittering. Se [the receipt rule](/en/reference/expenses/#the-receipt-rule) og
[receipts](/en/reference/expenses/#receipts).

## Før kjøregodtgjørelse

Velg **Kjøregodtgjørelse** som type, oppgi så **Fra** og **Til** om du vil, **Antall
kilometer** (én desimal, høyst 9999,9) og antall **Passasjerer** (0 til 8). Skjemaet
viser et anslag — kilometer × sats = beløp — fra satsen som gjelder på utleggets dato,
med passasjertillegget lagt til per passasjer. Det er bare et anslag: beløpet regnes ut
på nytt ved lagring og låses når utlegget sendes inn. Gjelder ingen kilometersats den
datoen, sier skjemaet det, og utlegget kan ikke sendes til godkjenning før en
administrator legger inn en. Det er ikke noe beløp å skrive og ingen kvittering å legge
ved. Se [the dated rates](/en/reference/expenses/#the-rates-and-what-they-deliberately-do-not-model).

## Før en leverandørfaktura

En leverandørs faktura for varer eller arbeid på et prosjekt føres som en
**Leverandørfaktura**: oppgi **Leverandør** og leverandørens **Fakturanummer**, oppgi
**Fakturadato** og eventuelt en **Forfallsdato** på eller etter den, velg en
**Kategori** (**Underleverandør** er forhåndsvalgt når den finnes) og skriv inn beløp
og mva. Firmaet betaler den alltid, så det er ingen betaler-velger, og ingen får noe
tilbakebetalt for den. **Prosjekt** er påkrevd og **Fakturerbar** starter på; velgeren
lister prosjektene der du kan se økonomien, og er den tom, peker skjemaet deg til
prosjektets egen side i stedet.

Etter **Lagre utkast** står skjemaet åpent så du kan legge ved **Leverandørens
faktura**; **Lagre og send inn** er utilgjengelig til den er lagt ved. Når den er sendt
inn, følger den den vanlige godkjenningsflyten. Se [the supplier
invoice](/en/reference/expenses/#the-supplier-invoice).

## Før en reiseregning

En reiseregning er en reise og alt den kostet, sendt inn og utbetalt under ett.

1. Trykk **Ny reiseregning** på Mine utlegg (eller bruk handlingen **Ny reiseregning**
   i søkefeltet).
2. Skriv **Formål med reisen** og, om du vil, **Reisemål**. Velg **Innenlands** eller
   **Utenlands**; en reise utenlands spør etter **Døgnsats utenlands** og **Valuta
   utenlands**, som erstatter den daterte diettabellen for den reisen.
3. Oppgi **Avreisedag**, **Avreisetidspunkt**, **Hjemkomstdag** og
   **Hjemkomsttidspunkt**. Hjemkomsten må være etter avreisen, og tidene er i
   tidssonen for virksomheten som skjemaet navngir, ikke nettleserens.
4. Velg et **Prosjekt** hvis reisen føres på ett. Hvert utlegg på reisen tar reisens
   prosjekt.
5. Trykk **Lagre**. Du lander på reisens egen side.

Reisens side viser status, reisemål, datoer og **Reisens totaler** per valuta (og **Til
kunden** når noe er fakturerbart). Herfra endrer **Rediger reisen** overskriften,
**Slett** fjerner reisen med alt som er ført på den, og **Send inn regningen** sender
hele reisen til godkjenning. En reise kan ha høyst 200 utlegg, og siden sier fra når
den er full.

## Legg til diettdager på en reise

Under **Diett** på reisens side trykker du **Foreslå dager**. Svar på **Overnattet
du?** — tidene alene kan ikke si det, og det avgjør om reisen telles i døgn fra
avreisen eller som én dag — og trykk så **Foreslå**. Dagene reisens tider gir listes
opp, priset med satsen som gjaldt hver dato; en dag som alt ligger på reisen sier
**Allerede lagt til**, og en dag ingen sats gjelder for kan ikke hukes av. Trykk **Legg
til … dager** for å føre de avhukede.

Hver dag i tabellen har en **Type dag** (**Dagsreise, 6 til 12 timer**, **Dagsreise,
over 12 timer**, **Overnatting, hotell** eller **Overnatting, annet enn hotell**), en
**Diettsats** og **Dekkede måltider** — huk av **Frokost**, **Lunsj** eller **Middag**
når noen andre betalte for det, så trekkes prosenten ved siden av fra. Hver endring
lagres i det du gjør den, og **Beløp** er serverens svar. En type dag satstabellen ikke
priser, eller et dekket måltid uten prissatt trekk, avvises på det feltet til en
administrator legger inn satsen. Se [the per diem day](/en/reference/expenses/#the-per-diem-day).

## Legg til kjøring og utlegg på en reise

**Legg til kjøring** under **Kjøring** og **Legg til et utlegg** under **Utlegg** åpner
det vanlige utleggsskjemaet med typen fastsatt og datoen satt til avreisedagen. Linjen
tar reisens prosjekt; er reisen ført på ett, ligger linjens egen
**Fakturerbar**-bryter i skjemaet. Det finnes ingen **Lagre og send inn** på en linje,
fordi en reise sendes inn under ett. Linjer redigeres og slettes fra radene sine med
blyanten og søppelbøtten.

## Rediger, slett og send inn

Så lenge et utlegg er et **Utkast** eller er **Avvist**, og datoen ikke ligger bak
periodelåsen, tilbyr raden blyanten for å redigere det, søppelbøtten for å slette det
(du blir spurt: **Slette utlegget?** — det kan ikke angres) og papirflyet for å sende
det til godkjenning. Huk av flere utlegg og reiseregninger og trykk **Send inn …
valgte** for å sende dem under ett; sendingen er alt eller ingenting, og en avvisning
skrives under raden den gjelder.

Innsendingen låser en kjøregodtgjørelses sats og en diettdags sats og trekk slik de
står den dagen. En innsending kan avvises for en manglende kvittering, en sats
tabellen ikke lenger har, en reise uten utlegg, eller en dato låsen har stengt —
setningen sier hvilket. Når det er sendt inn, er utlegget **Sendt inn** og ikke lenger
ditt å endre: åpner du det, står det **Dette utlegget kan ikke lenger endres.** Se [the
flow, and what freezes on submit](/en/reference/expenses/#the-flow-and-what-freezes-on-submit).

## Når et utlegg sendes i retur

Et avvist utlegg viser **Avvist: …** med begrunnelsen, hvem som avviste det og når, på
Mine utlegg, på Hjem-dashbordet ditt og i skjemaet. Rett det og send det inn igjen:
lagrer du et avvist utlegg, blir det et utkast, mens en avvist reise forblir **Avvist**
til du trykker **Send inn regningen** igjen.

## Godkjenn eller avvis utlegg

**Godkjenning** (sidemenyen: **Godkjenning av utlegg**) lister utleggene og
reiseregningene som er sendt inn og som du kan behandle, ett kort per person, den som
har ventet lengst først. Hvert kort bærer personens totaler per valuta og to flagg verdt
å se på: **… uten kvittering** og **… med endret sats**.

- Huk av det du vil behandle — reiser og utlegg om hverandre — og trykk **Godkjenn …
  valgte** eller **Avvis … valgte**. Å avvise åpner **Sende utleggene i retur?**, der en
  **Begrunnelse** er påkrevd (høyst 1000 tegn); personen ser den sammen med hvert
  utlegg du sender i retur. Sendingen er alt eller ingenting.
- Klikk på et utleggs beskrivelse, eller **Åpne** på en reise, for detaljene:
  beløpene, kvitteringene, eieren, prosjektet og faktureringsblokken hvis du kan se den.
  Skuffen tilbyr **Godkjenn** og **Avvis**, og på en innsendt kjøregodtgjørelse eller
  diettdag **Endre satsen**, der dialogen sier hva tabellen ga og hva linjen blir; på
  kjøring med passasjerer kan **Passasjertillegg per kilometer** også endres. Endringen
  noteres med navnet ditt.
- **Godkjent**-halvdelen av bryteren lister det som er godkjent. Huk av og trykk
  **Trekk tilbake … godkjenninger** (eller **Trekk tilbake godkjenningen** i skuffen)
  for å sette en enhet tilbake til utkast, så den går runden på nytt. Det avvises når
  enheten er utbetalt eller en av linjene er fakturert.

Du kan godkjenne dine egne utlegg. Godkjenner du ingens, sier siden **Du godkjenner
ingens utlegg**. Se [approval](/en/reference/expenses/#approval).

## Betal folk tilbake

**Utbetaling** (sidemenyen: **Refusjoner**) krever `expenses:manage`; uten den sier
siden **Du kan ikke se hva som skal utbetales**. **Venter på utbetaling** lister, per
person, de godkjente utleggene og reiseregningene som gir personen noe til gode, med
**Til gode** per rad og per valuta; **Fra** og **Til** avgrenser listen etter dato. En
reise er én rad, for summen av det linjene gir til gode.

1. Huk av radene, eller **Velg alt til …** på en persons kort, og trykk **Merk … som
   utbetalt**.
2. I **Før opp en lønnskjøring** oppgir du **Dagen pengene gikk** (i dag som standard,
   aldri frem i tid) og en valgfri **Lønnsreferanse** (høyst 100 tegn), og trykker så
   **Merk som utbetalt**. Alt eller ingenting, og en reise utbetales under ett.

**Allerede utbetalt** viser kjøringene som er ført, med dato og referanse per rad; huk
av og trykk **Angre … utbetalinger** for å fjerne et stempel og legge enhetene tilbake
i ventelisten. Verken merking eller angring holdes tilbake av periodelåsen. Se [the two
tracks after approval](/en/reference/expenses/#the-two-tracks-after-approval).

## Eksporter lønnsfilen

På begge halvdelene av Utbetaling laster **Eksporter alt som venter** / **Eksporter alt
som er utbetalt** ned det filtrene viser, og **Eksporter … valgte** nøyaktig de avhukede
enhetene. Filen er semikolonseparert med desimalkomma og ISO-datoer, én rad per linje
med reisens formål ved siden av hver av linjene sine, og heter
`expenses-reimbursements-<dato>.csv`. En eksport på over 5 000 rader avvises med
**Too many rows to export** — avgrens datoene i stedet. Kolonnene står under [the
payroll CSV](/en/reference/expenses/#the-payroll-csv).

## Utlegg på et prosjekt

Fanen **Utlegg** på prosjektsiden viser **Hva utleggene kommer på** — ett kort per
valuta, ingenting regnet om — med gruppene **Godkjent**, **Sendt inn** og **Utkast**,
**Totalt**, **Til kunden**, **Klart til fakturering**, **Fakturert** og **Herav
leverandørfakturaer**. Summene krever økonomirettigheter på prosjektet; listen under,
**Utlegg på dette prosjektet**, viser utleggene du kan åpne, og sier fra når det er
færre enn summene dekker.

- **Før en kostnad** åpner utleggsskjemaet med prosjektet oppgitt i stedet for
  tilbudt, **Firmaet betalte** som utgangspunkt, og **Linje** og **Fakturerbar** innen
  rekkevidde. **Før en leverandørfaktura** står ved siden av for prosjektets
  økonomiside.
- Brikken **Klart til fakturering** avgrenser listen til de godkjente, fakturerbare,
  prissatte linjene som ennå ikke er fakturert.
- Klikk på en beskrivelse for å åpne linjen. **Pris for kunden** setter
  **Fakturerbar**, **Linje** og **Påslag** (et utlegg eller en leverandørfaktura) eller
  **Kundepris per kilometer** (kjøring); et felt som står tomt beholder det linjen har.
  Den er åpen i alle statuser til linjen er fakturert, og aldri på en diettdag.
- **Merk som fakturert** noterer at linjen gikk ut på en faktura, med en valgfri
  **Fakturareferanse**. *Fakturert* er et stempel, ikke en status: linjen forblir
  godkjent, forsvinner fra listen over det som er klart til fakturering, og telles
  under **Fakturert** på kortet. **Angre faktureringen** tar stempelet tilbake etter en
  bekreftelse.
- En linje som gikk ut på en faktura utstedt i Fakturaer-appen, er fakturert av den
  fakturaen. **Angre faktureringen** tilbys ikke på den: bare en kreditnota som
  krediterer hele linjen tar stempelet tilbake og legger linjen i listen over det som er
  klart til fakturering igjen. Å merke en slik linje som fakturert, eller angre det, for
  hånd blir avvist.

Prising og fakturering tilhører den som kan se prosjektets økonomi, ikke
`expenses:manage`, og periodelåsen når dem ikke. Se [pricing by the project
side](/en/reference/expenses/#pricing-by-the-project-side) og [on the project
page](/en/reference/expenses/#on-the-project-page).

## Endre innstillingene

**Innstillinger for utlegg** (sidemenyen: **Utleggsinnstillinger**) krever
`expenses:manage` og har tre deler.

**Generelt**: **Standardvaluta**, som hvert nytt utlegg føres i; **Standard påslag**,
som et fakturerbart utlegg får over netto med mindre prosjektet sier noe annet;
**Kvitteringsregel** — **Av**, **Alltid** eller **Over et beløp** med **Kvittering
kreves over**; **Låst før**, den første åpne dagen i periodelåsen (la den stå tom for
ingen lås; å endre den spør **Endre låsen?**); og **Tidssone for virksomheten**, som
avgjør hvilken dag en reises avreise og hjemkomst faller på. Å endre tidssonen flytter
dagene for reiser som alt er ført, og bekreftes høyt — sett den én gang, når
installasjonen settes opp.

**Satser**: hver satstype listes enten den har rader eller ikke —
**Kilometergodtgjørelse**, **Passasjertillegg**, **Kundepris per kilometer**, de fire
diettypene og de tre måltidstrekkene. En rad gjelder fra dagen i **Gjelder fra** til
neste rad overtar. **Legg til sats** (eller **Legg til en sats i …** under en type) spør
etter **Type**, **Gjelder fra**, **Verdi** (eller **Prosent**, 0 til 100, for et trekk),
**Valuta** og en valgfri **Kilde**-merkelapp; en rad uten vises som **Egen sats**, en
merket som **Statens sats**. Rediger og slett en rad med blyanten og søppelbøtten —
utlegg som alt er sendt inn beholder satsen de ble låst med. **Gjenopprett statens
satser for …** setter typens medfølgende rader tilbake slik de kom, og lar dine egne
rader være. To typer leveres tomme med vilje, **Kundepris per kilometer** og **Diett,
annen overnatting**; se [the
rates](/en/reference/expenses/#the-rates-and-what-they-deliberately-do-not-model).

**Kategorier**: hva et utlegg føres under. **Legg til kategori** spør etter et
**Navn**; blyanten gir nytt navn og slår **Kan velges** av eller på; pilene flytter den
opp eller ned i listen, og **Deaktiver …** / **Aktiver … igjen** gjør det samme som
bryteren. En kategori slettes aldri: en deaktivert blir stående på utleggene som alt er
ført på den, men kan ikke velges for nye.
