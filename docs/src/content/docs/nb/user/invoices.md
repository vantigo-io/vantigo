---
title: Fakturaer
description: Utkast, utstedelse og sending av fakturaer, kreditnotaer, betalinger, journalen og eksporten.
sidebar:
  order: 50
sources:
  - apps/invoices/frontend
---

Fakturaer-appen utsteder salgsdokumentene i bokføringen din: et utkast blir en
nummerert faktura eller kreditnota i det øyeblikket det utstedes, får en PDF, og endres
aldri etter det. Appen har tre områder i sidemenyen: **Fakturaer**, **Fakturajournal**
og **Fakturainnstillinger**. Alle beløp er i NOK i denne fasen, og en faktura leveres
som PDF — ved nedlasting eller på e-post — ikke som EHF-faktura
([det loven krever](/en/reference/invoices/#the-law-in-one-page)).

Å åpne appen i det hele tatt krever `invoices:access`; de andre rettighetene nevnes der
de gjelder og er samlet under [Rettigheter](#rettigheter).

## Før den første fakturaen

Ingenting kan utstedes før selskapet selv er beskrevet. Inntil da viser listen
**Selgeropplysningene er ikke fullstendige** til alle som kan utstede.

### Selgeropplysningene og nummerserien

Åpne **Fakturainnstillinger** i sidemenyen (vises bare med `invoices:manage`; uten den
sier siden *Å endre fakturainnstillingene krever invoices:manage*). Under **Selgeren**
krysser listen **Det som trengs for å utstede** av det som er fylt ut: **Juridisk
navn**, **Organisasjonsnummer**, **Adresse**, **Postnummer**, **Poststed** og
**Kontonummer**. En siste, informativ linje sier om e-post er satt opp — *E-post er satt
opp (SMTP)* eller *E-post er ikke satt opp (SMTP), så dokumenter kan ikke sendes på
e-post. Utstedelse trenger det ikke.*

Fyll ut feltene og klikk **Lagre**:

- **Juridisk navn**, **Organisasjonsnummer** (ni sifre med gyldig kontrollsiffer),
  **Adresse**, **Adresselinje 2**, **Postnummer**, **Poststed**, **Land** (en kode på to
  bokstaver, som NO) og **E-post** — adressen svar på sendte fakturaer går til.
- **Kontonummer** (elleve sifre med gyldig kontrollsiffer), og **IBAN** og **BIC** for
  kjøpere i utlandet.
- **Standard betalingsfrist (dager)**, 0 til 365: fristen et nytt utkast starter med
  når kundens faktureringsprofil ikke oppgir noen.
- **Registrert i Merverdiavgiftsregisteret (skriver MVA etter nummeret)** og
  **Registrert i Foretaksregisteret** — begge skrives på dokumentet slik forskriften
  krever.
- **Bunntekst**, som skrives nederst på hvert dokument.
- **Nummerserien starter på** — nummeret det første dokumentet får. Det låses i det noe
  utstedes, og feltet sier da *Låst: det er utstedt dokumenter fra denne serien*.

Lagring beholder to ting siden ennå ikke har felter for: selgerens **Peppol-ID** —
adressen EHF-fakturaer sendes fra, fylt ut som `0192:` og organisasjonsnummeret når du
har et — og **KID-avtalen** med banken, lengden og kontrollsifferet (MOD10 eller MOD11).
Til de får egne felter, settes de gjennom API-et
([Peppol-ID og KID-avtalen](/en/reference/invoices/#the-peppol-id-and-the-kid-agreement)).
Med en KID-avtale får hver faktura som utstedes fra da av en KID, betalingsreferansen
banken kobler betalingen til; en kreditnota får aldri det.

Lagret en kollega innstillingene mens du redigerte, sier skjemaet **Innstillingene er
endret** og tilbyr **Last inn på nytt**; de ulagrede endringene dine forkastes, de
flettes aldri.

### Mva-koder

Lenger ned på samme side lister **Mva-koder** hver kode med **Kode**, **Navn**,
**Kategori**, **SAF-T-kode** og **Sats i dag**. Vantigo legger inn de norske kodene fra
start (3 med 25 %, 31 med 15 %, 32 med 11,11 %, 33 med 12 %, og 0 %-kodene 5, 51, 52, 6
og 7), så de fleste bedrifter trenger ikke endre noe her.

- **Legg til en mva-kode** spør etter kode, navn, SAF-T-kode, kategori og en
  fritaksgrunn, og etter den første **Sats %** og dagen den **Gjelder fra**.
- Blyanten (**Rediger mva-kode …**) redigerer koden, navnet, fritaksgrunnen og — til
  linjer bruker den — kategorien og SAF-T-koden; boksen **Tilbys på nye linjer** tar en
  kode ut av redigeringens liste uten å røre linjene som alt bruker den. Bruker linjer
  en kode, *ligger kategorien og SAF-T-koden fast*: deaktiver den og opprett en ny.
- **Satsperioder** viser en kodes satser som daterte perioder. Oppgi **Ny sats %** og
  dagen den **Gjelder fra**, og klikk **Endre satsen fra denne datoen**; den forrige
  perioden avsluttes dagen før. Bare den siste perioden kan fjernes, og bare mens den
  ligger i framtiden. En sats kan ikke endres fra en dag på eller før et utstedt
  dokuments dato ([reglene](/en/reference/invoices/#endpoints)).

## Finne et dokument

**Fakturaer** i sidemenyen lister hvert utkast og hvert utstedte dokument: utkast først,
så etter nummer, nyeste først. Hver rad viser **Nummer** (eller *Utkast*), **Type**,
**Tilstand**, **Kunde**, **Fakturadato**, **Forfallsdato**, **Sum** og — for en utstedt
faktura — det som er **Utestående**. Klikk nummeret i en rad for å åpne dokumentet.

Snevre inn listen med knappene og feltene over den:

- **Status**: *Alle statuser*, *Utkast* eller *Utstedt*.
- **Type**: *Alle typer*, *Faktura* eller *Kreditnota*.
- **Tilstand**: *Alle tilstander*, *Åpen*, *Delvis betalt*, *Forfalt*, *Betalt* eller
  *Kreditert*. Bare en utstedt faktura har en av disse tilstandene; utkast og
  kreditnotaer treffer aldri filteret. På en telefon blir knappene en nedtrekksliste.
- **Kunde** — et søk blant kundene, arkiverte inkludert, siden dokumentene deres
  oppbevares. Feltet vises når du også har `customers:view`.
- **Søk** etter *Nummer eller kjøper*, og **Utstedt fra** / **Utstedt til** for et
  intervall av fakturadatoer.

Tilstandene bedømmes den dagen du ser, de lagres ikke: *Åpen* fram til forfallsdatoen,
*Forfalt* fra dagen etter, *Delvis betalt* når noe er registrert, *Betalt* når ingenting
er utestående, *Kreditert* når kreditnotaer dekker hele fakturaen
([hvordan tilstanden utledes](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

## Lage et utkast

Klikk **Ny faktura** øverst i listen. Knappen krever `invoices:create` og
`customers:view`, fordi kjøperen velges fra Kunder-appen. Velg **Kunde** — bare aktive
kunder tilbys — og klikk **Lag utkastet**. Utkastet åpnes i redigeringen med en gang.

Utkastet starter med det kundens faktureringsprofil i Kunder sier
([brukerveiledningen for Kunder](/nb/user/customers/)): **Deres referanse** er profilens
kjøperreferanse, og betalingsfristen er profilens, eller innstillingenes standard når
profilen ikke har noen. **Vår referanse** er ditt navn, og leveringen er i dag til du endrer den.

Utkastet kan avvises når kunden er slått sammen med en annen (*fakturer den i stedet*),
er arkivert, er sperret for fakturering eller ikke finnes lenger — de samme kontrollene
kjøres igjen ved hver lagring og ved utstedelse
([kundekontrollene](/en/reference/invoices/#drafts)).

## Redigere og slette et utkast

Redigeringen viser utkastet under overskriften **Faktura — Utkast** med kjøperen,
leveringen, referansene, linjene og merknadene. Endringer holdes i nettleseren til du
klikker **Lagre**; mens det finnes ulagrede endringer, sier en linje *Lagre endringene
før du forhåndsviser eller utsteder*.

- **Kunde** kan byttes til en annen aktiv kunde, aldri tømmes.
- **Levering** er **En dag** med en **Leveringsdato**, eller en **Leveringsperiode** med
  **Levert fra** og **Levert til** — begge ender, ellers venter Lagre. Uten levering kan
  utkastet lagres, men ikke utstedes (*Leveringsdato eller -periode må fylles ut før
  dokumentet kan utstedes*). Kryss av **Levert et annet sted enn kjøperens adresse** for
  å oppgi et leveringssted, som bare da skrives på dokumentet.
- **Deres referanse** (kjøperens; mens det er tomt, sier feltet at en EHF-faktura vil
  trenge det), **Vår referanse**, **Ordrereferanse** og **Betalingsfrist (dager)**, 0 til
  365. Forfallsdatoen er fakturadatoen pluss fristen, fastsatt ved utstedelse.
- **Linjer**: **Legg til en linje**, deretter **Beskrivelse**, **Antall** (inntil tre
  desimaler), **Enhet**, **Enhetspris** (inntil fire desimaler), **Rabatt %** og
  **Mva-kode** fra kodene som tilbys på nye linjer; **Beløp** er linjens nettobeløp.
  Pilene flytter en linje, søppelbøtta fjerner den. En linje med en kode som siden er
  tatt ut av listen, viser den fortsatt, merket *(tilbys ikke lenger)*.
- Under linjene står summene per mva-sats, **Sum eks. mva**, **Mva** og **Sum**. Mens du
  redigerer, er de *et anslag*; det lagrede utkastets summer er serverens, beregnet med
  satsene som gjelder i dag.
- **Merknad** skrives ut på dokumentet; **Intern merknad** skrives aldri ut.

En boks med navnet **Verdt å se på** lister utkastets advarsler: kunden faktureres i en
annen valuta, leveringen ble avsluttet for mer enn en måned siden, en linjes mva-kode
har ingen sats i dag. En advarsel stopper aldri en lagring, men den siste ville stoppet
utstedelsen ([advarslene](/en/reference/invoices/#drafts)).

**Forhåndsvis** åpner utkastet som PDF i en ny fane, med vannmerket *UTKAST — ikke et
salgsdokument* og uten nummer; knappen holdes tilbake mens det finnes ulagrede
endringer. **Slett** spør *Slette utkastet?* og minner om at ingenting er utstedt, så
ingen nummer går tapt. Begge krever `invoices:create`; uten den er redigeringen
skrivebeskyttet.

Lagret noen andre utkastet i mellomtiden, sier redigeringen **Utkastet er endret** og
tilbyr **Last inn på nytt**, som forkaster dine ulagrede endringer for den nyeste versjonen.

## Utstede

Klikk **Utsted** i utkastets topptekst — tilbys med `invoices:issue`, etter en lagring,
og bare mens installasjonen har et dokumentlager (*Denne installasjonen har ikke noe
dokumentlager, så ingenting kan utstedes*). Dialogen **Utsted fakturaen** advarer om at
*dette gir dokumentet neste nummer og kan ikke angres*, og viser **Fakturadato**: i dag,
eller et valg mellom i dag og siste dag i forrige måned når forskriften tillater
tilbakedatering ([fakturadatoen](/en/reference/invoices/#the-law-in-one-page)). Klikk
**Utsted**.

Utkastet blir **Faktura 1001** — neste nummer i den ene serien fakturaer og kreditnotaer
deler — og en melding sier *Utstedt som nummer 1001*. Alt på det ligger nå fast: linjene,
mva per sats med satsene på fakturadatoen, forfallsdatoen, og en kopi av kjøperen slik
kunden var i det øyeblikket og av selgeren slik innstillingene var. Senere endringer av
kunden eller innstillingene når aldri et utstedt dokument. Ble leveringen avsluttet for
over en måned siden, lykkes utstedelsen likevel og ber deg *huske fristen neste gang*.

En utstedelse avvises, og nummeret gis tilbake, når selgeropplysningene er
ufullstendige, utkastet mangler linjer eller levering, kjøperen verken har fullstendig
adresse eller organisasjonsnummer, en linjes mva-kode ikke lenger tilbys eller mangler
sats på fakturadatoen, linjene ikke stemmer med selgerens mva-registrering, en linje med
omvendt avgiftsplikt mangler kjøperens organisasjonsnummer, eller den valgte datoen ikke
er tillatt den dagen. Hver avvisning sies i dialogen
([alle avvisningene](/en/reference/invoices/#issuing)).

## Laste ned PDF-en

Et utstedt dokuments side tilbyr **Last ned PDF**. Filen er navngitt etter dokumentet og
kjøperens språk — `faktura-1001.pdf` eller `invoice-1001.pdf`, `kreditnota-1002.pdf`
eller `credit-note-1002.pdf` — og er den ene PDF-en som ble laget og lagret ved
utstedelse, levert nøyaktig som lagret hver gang ([PDF-en](/en/reference/invoices/#the-pdf)).
Alle med `invoices:access` kan laste den ned.

En faktura med KID viser den i betalingsfeltet, som **KID**, og ber kjøperen betale med
den i stedet for fakturanummeret.

Kunne lageret ikke nås ved utstedelse, sier siden *PDF-en kunne ikke lagres da dokumentet
ble utstedt. Den lagres første gang den lastes ned* — en nedlasting eller en sending lagrer den.

## Kreditere en faktura

For å reversere en utstedt faktura, helt eller delvis, åpner du den og klikker
**Krediter** — tilbys med `invoices:issue` mens kortet **Kreditnotaer** fortsatt viser
noe *Igjen å kreditere*. Vantigo lager en **Kreditnota — Utkast** som kopierer fakturaen:
kjøperen slik fakturaen navnga den, leveringen og referansene, og hver linje.

I et kreditnotautkast kan du bare ta bort: fjerne linjer, senke et **Antall** eller en
**Enhetspris**, og redigere beskrivelser og merknader. Kunden, leveringen, referansene og
mva-kodene blir som fakturaen hadde dem, og en kreditnota har ingen betalingsfrist.
Behold hver linje for en full reversering; fjern eller senk linjer for en delvis. Så
**Utsted kreditnotaen** slik du utsteder en faktura.

Kreditnotaen tar neste nummer i samme serie og listes under **Kreditnotaer** på
fakturaen, som den lenker tilbake til med *Kreditnota til faktura 1001*. Fakturaens
**Utestående** beløp faller med kreditnotaens sum; når kreditnotaer dekker hele
fakturaen, blir tilstanden **Kreditert**. En kreditnota etter en betaling er tillatt:
fakturaen viser da **Til tilbakebetaling**, det som skyldes tilbake. Utkastet advarer, og
utstedelsen avviser, en kreditnota som er større enn det fakturaen eller en linje har
igjen; en kreditnota kan ikke selv krediteres
([kreditnotaer](/en/reference/invoices/#credit-notes)).

## Registrere betalinger

Vantigo leser ikke bankfiler: mottatte penger registreres for hånd, på den utstedte
fakturaens side under **Betalinger**. Hver registrering krever `invoices:payments`.

Klikk **Registrer betaling** — tilbys mens noe er igjen å betale — og fyll ut:

- **Betalingsdato**: dagen pengene kom inn, forhåndsutfylt med i dag; på eller etter
  fakturadatoen, og ikke etter i dag.
- **Beløp**: forhåndsutfylt med utestående beløp; mer enn 0 og høyst utestående beløp.
  En overbetaling avvises, med utestående beløp nevnt.
- **Referanse** (bankens eller betalerens) og **Merknad**, begge valgfrie.

Klikk **Registrer**. Tabellen lister hver betalings **Betalingsdato**, **Beløp**,
**Referanse**, **Merknad** og når den ble **Registrert**, og summene får **Betalt** og
**Utestående** — summen minus det som er kreditert og betalt. En betaling på hele det
utestående gjør fakturaen **Betalt**; en mindre gjør den **Delvis betalt**, eller lar den
stå som **Forfalt** etter forfall. En kreditnota tar ingen betaling
([betalinger og tilstanden](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

En feil registrering redigeres aldri: klikk **Fjern** ved siden av den, oppgi en
**Begrunnelse** i dialogen **Fjern betalingen**, og bekreft. *Betalingen blir stående på
fakturaen, gjennomstreket med begrunnelsen. En fjerning kan ikke angres; registrer
betalingen på nytt om den likevel var riktig.*

## Sende et dokument på e-post

Åpne en utstedt faktura eller kreditnota og klikk **Send**. Knappen krever
`invoices:issue` og en installasjon med e-post satt opp; uten SMTP finnes den ikke.

Dialogen **Send på e-post** åpnes med **Mottaker** forhåndsutfylt fra kundens
faktura-e-post i faktureringsprofilen — adressen slik den er i dag, ikke slik den var
ved utstedelse. Endre den for å sende dokumentet til en annen adresse; bare *selve
e-postadressen, for eksempel faktura@example.no* godtas. Har kunden ingen
faktura-e-post, eller kunne adressen ikke leses, starter feltet tomt, og du skriver inn
en.

Over feltet viser dialogen det du bør vite før du sender. To er røde og kan ikke
overses: **Kunden forventer EHF** og, fra 1. januar 2027 for en norsk virksomhet,
**Elektronisk faktura er påkrevd** — en PDF på e-post oppfyller ikke plikten. Resten er
merknader: kjøperen er en norsk virksomhet (før 2027), eller kunden foretrekker eFaktura
eller papir. Ingen av dem stopper sendingen. På en faktura som er delvis betalt eller
kreditert, sier en merknad at e-posten bare ber om det utestående beløpet; på en som er
gjort opp, at den sier det ikke er noe å betale.

Klikk **Send**. E-posten går med en gang, med den lagrede PDF-en vedlagt og en kort,
ren tekst — *Faktura 1001 fra <selger>* eller *Invoice 1001 from <selger>* på kjøperens
språk — som ber om utestående beløp til selgerens konto, merket med fakturaens KID når den har
en og med nummeret ellers; svar går til e-posten i
innstillingene ([tekstene](/en/reference/invoices/#sending-a-document)). En melding
bekrefter *Sendt til …*, og kortet **Sendt på e-post** på dokumentet får en rad med
**Sendt**, **Til** og **Emne**. Kolonnen **Til** vises bare for den som har
`invoices:issue`; andre ser når og under hvilket emne hver sending gikk.

En sending avvises når kunden er anonymisert (*Kunden er anonymisert, og det sendes ikke
mer til den*), når det ikke finnes noen adresse, når dokumentlageret er utilgjengelig,
eller etter mer enn 60 sendinger på ti minutter fra ett sted. Bekrefter e-postserveren
ikke sendingen, sier dialogen *E-postserveren bekreftet ikke sendingen. Ingenting ble
registrert; den kan likevel ha kommet fram. Sjekk med kunden før du sender på nytt.* En
rad under **Sendt på e-post** betyr at e-postserveren tok imot e-posten, ikke at den kom
fram: en retur går til installasjonens avsenderadresse og registreres ikke her. Samme
dokument kan sendes på nytt, og loggføres da på nytt.

## Kontrollere journalen

**Fakturajournal** i sidemenyen er beviset bokføringsforskriften krever: hvert utstedte
dokument i nummerrekkefølge, og kontrollen av at serien ikke har hull. Den krever
`invoices:access`. Intervallet **Utstedt fra** / **Utstedt til** starter som inneværende
måned; endre den ene eller begge datoene.

Øverst står kontrollen i ord: *Ingen dokumenter er utstedt i perioden, så det er
ingenting å kontrollere*, eller *Ingen hull mellom 1001 og 1042*, eller det røde
**Nummerserien har brudd** med *Manglende nummer*. Et annet rødt varsel, **Telleren og
dokumentene stemmer ikke overens**, betyr at et nummer er tatt uten dokument. Ingen av
delene skjer ved vanlig bruk — en avvist utstedelse gir nummeret tilbake, og et utstedt
dokument slettes aldri — så begge betyr at dataene er endret utenfor Vantigo
([journalen](/en/reference/invoices/#the-journal)).

Under står **Summer per mva-kode** over hele intervallet (**SAF-T-kode**, **Kategori**,
**Sats %**, **Grunnlag**, **Mva**) med *Netto · Mva · Sum*, og dokumentene én side om
gangen: **Nummer**, **Type** (en kreditnota står som *Kreditnota til 1001*),
**Fakturadato**, **Kunde**, **Sum eks. mva**, **Mva** og **Sum**. En kreditnota vises
negativt, så summene er periodens nettosalg.

## Eksportere perioden til regnskapsføreren

På journalen laster **Eksporter CSV** ned intervallet som vises, som
`invoices-<fra>-<til>.csv`. Filen har én rad per dokument og mva-sats — en kreditnotas
beløp negative — med faste engelske kolonner: Number, Kind, Issue date, Delivery, Due,
Customer number, Buyer, Buyer org no, Currency, SAF-T code, Rate, Base, VAT, Base NOK,
VAT NOK, Credits number og KID — importer KID-kolonnen som tekst, ellers fjerner
regnearket de innledende nullene. Den åpnes i et regneark slik norske systemer venter: `;`
mellom cellene, desimalkomma, UTF-8 ([CSV-eksporten](/en/reference/invoices/#the-csv-export)).

Et intervall på mer enn 5000 rader avvises — *Eksporten ville hatt mer enn 5000 rader;
snevre inn perioden* — før noe skrives, aldri kuttet.

## Kortet på dashbordet

**Hjem** viser et **Fakturaer**-kort til alle med `invoices:access`. Tallet er
**Utestående**: utestående beløp, akkurat nå, for hver faktura som er åpen, delvis
betalt eller forfalt. Under det vises *N forfalte (beløp)* bare mens noe er forfalt, og
prosenten *fakturert mot forrige periode* sammenligner det som er fakturert i
dashbordets valgte periode (7, 30 eller 90 dager, 12 måneder eller tilpasset) med
perioden av samme lengde før ([statistikken](/en/reference/invoices/#stats)). Kortet
har ingen graf; et klikk på det åpner listen.

## En kundes fakturaer

En kundes side i Kunder-appen har en **Fakturaer**-fane (vises når Fakturaer-modulen er
på og du har `invoices:access`): den samme listen, filtrert til den kunden, med hvert
dokuments tilstand og utestående beløp. Knappen **Ny faktura** der lager utkastet for
den kunden — *levert i dag til du endrer det* — og åpner redigeringen; den tilbys med
`invoices:create` og `customers:view`, og bare på en aktiv kunde, aldri en arkivert,
sperret, sammenslått eller anonymisert.

## Oppbevaring og anonymiserte kunder

Ingenting utstedt slettes noensinne: et utstedt dokument, betalingene og sendingsloggen
er bokføringsmateriale som oppbevares fem år etter regnskapsårets slutt, og PDF-ene
ligger i installasjonens dokumentlager, der sikkerhetskopiene er en del av oppbevaringen
([oppbevaring](/en/reference/invoices/#retention-and-personal-data)). Slås to kunder
sammen, følger dokumentene den gjenværende kunden, men beholder kjøperen som står på
dem. Anonymiseres en person i Kunder, slettes utkastene deres, mottakeren på hver sending
blankes — kolonnen **Til** viser da *(anonymisert)* — og merknadene på betalingene deres
tømmes; de utstedte dokumentene, med kjøperen de navngir, blir stående. Ingen dokumenter
sendes til en anonymisert kunde igjen, men en kreditnota kan fortsatt utstedes, med
kjøperen originalen navnga.

## Rettigheter

Ingen innebygd rolle har disse; en eier har alt
([rettigheter](/en/reference/invoices/#permissions)).

| Du vil | Du trenger |
| --- | --- |
| Åpne appen, lese hvert dokument, laste ned PDF-er, se betalinger og sendinger, lese journalen, eksportere CSV-filen, se kortet på dashbordet | `invoices:access` |
| Lage, redigere, forhåndsvise og slette utkast | `invoices:create`, og `customers:view` for å velge kjøperen |
| Utstede et utkast, lage en kreditnota, sende et dokument, se hvor hver sending gikk | `invoices:issue` |
| Registrere en betaling eller fjerne en med begrunnelse | `invoices:payments` |
| Redigere selgeropplysningene, nummerserien og mva-kodene | `invoices:manage` |
