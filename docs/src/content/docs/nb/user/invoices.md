---
title: Fakturaer
description: Fakturering av arbeid, utkast, utstedelse og sending av fakturaer, kreditnotaer, betalinger, journalen og eksporten.
sidebar:
  order: 50
sources:
  - apps/invoices/frontend
  - apps/host/frontend/src/routes/customers/-customer-invoices-tab.tsx
  - apps/host/frontend/src/lib/invoice-access.ts
  - apps/host/frontend/src/routes/invoices
---

Fakturaer-appen utsteder salgsdokumentene i bokføringen din: et utkast blir en
nummerert faktura eller kreditnota i det øyeblikket det utstedes, får en PDF, og endres
aldri etter det. Appen har fire områder i sidemenyen: **Fakturaer**, **Fakturajournal**,
**Innbetalinger** — bankens filer og avvikskøen, vist bare med `invoices:payments` — og
**Fakturainnstillinger**. Alle beløp er i NOK i denne fasen. En faktura leveres som
PDF — ved nedlasting eller på e-post — eller som EHF-faktura i Peppol-nettverket, når
installasjonen er satt opp for det
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

Under **Selgeren** har siden to kort til, **E-faktura** og **KID**, som beskrives
nedenfor. Knappen **Lagre** nederst på siden lagrer selgeren, Peppol-ID-en og
KID-avtalen samtidig; aksesspunktet har sin egen lagreknapp.

Lagret en kollega innstillingene mens du redigerte, sier skjemaet **Innstillingene er
endret** og tilbyr **Last inn på nytt**; de ulagrede endringene dine forkastes, de
flettes aldri.

### Sett opp e-faktura

EHF er den norske e-fakturaen: dokumentet som data kundens system leser, med PDF-en
inni, levert gjennom Peppol-nettverket. Vantigo overleverer den til et **aksesspunkt**,
en leverandør som bringer den ut på nettverket; Storecove er den Vantigo støtter. Å
skaffe en Storecove-konto og nøkkelen til den er administratorens jobb
([administrasjon av e-faktura](/nb/admin/e-invoicing/)); å legge dem inn her krever
`invoices:manage`, som resten av siden.

Kortet **E-faktura** — *Send dokumenter som EHF i Peppol-nettverket, gjennom et
aksesspunkt* — starter med **Hva e-faktura trenger**:

- **Peppol-ID 0192:974760673** med en hake når selgerens Peppol-ID er satt, eller
  *Selgerens Peppol-ID mangler: fyll den inn, eller organisasjonsnummeret den utledes
  av.* Linjen gjelder bare e-faktura: utstedelse venter aldri på den.
- En informativ linje: *Sending som EHF er tilgjengelig*, eller *Sending som EHF er ikke
  tilgjengelig. Det krever at driftsansvarlig har slått på e-faktura og Peppol-oppslag,
  og at aksesspunktets påloggingsdata og selgerens Peppol-ID er lagt inn.* De to første
  er driftsinnstillinger, ikke noe denne siden styrer.

**Peppol-ID** er adressen din i Peppol-nettverket, den EHF-fakturaene sendes fra. Står
den tom, er den `0192:` og organisasjonsnummeret — feltet viser det som plassholder, og
lagringen fyller det inn. En norsk virksomhet trenger ikke noe mer her; en `0192`-ID må
være ditt eget organisasjonsnummer. Den lagres med sidens **Lagre**, og en endring
gjelder alt som sendes som EHF fra da av, også dokumenter utstedt tidligere.

Under **Aksesspunkt** viser kortet det som er lagret:

- **Leverandør** er **Storecove**, den eneste så langt.
- **ID for juridisk enhet** er den juridiske enheten i Storecove som dokumentene sendes
  som, et heltall administratoren får fra Storecove; når den er lagret, viser feltet den.
- **API-nøkkel** er nøkkelen fra Storecove. Den lagres kryptert og vises aldri igjen: når
  en er lagret, viser kortet **Nøkkel lagret**, feltet står tomt, og en ny nøkkel du
  skriver inn, erstatter den. La feltet stå tomt for å beholde den lagrede.

Klikk **Lagre aksesspunkt**; meldingen sier *Aksesspunktet er lagret*. **Kontroller** og
**Fjern påloggingsdataene** er grået ut til en nøkkel er lagret. Klikk **Kontroller**:
Vantigo spør Storecove med den lagrede nøkkelen og svarer på én av tre måter —

- *Aksesspunktet godtok nøkkelen.* Da er du ferdig.
- *Aksesspunktet avviste nøkkelen. Kontroller den og lagre den på nytt.*
- *Aksesspunktet kunne ikke nås, eller nøkkelen gir ikke tilgang til denne juridiske
  enheten. Kontroller ID-en for den juridiske enheten, eller prøv igjen senere.*

Kan ikke Vantigo lese den lagrede nøkkelen i det hele tatt — installasjonens hemmelighet
er byttet — sier Kontroller i stedet *Ingen nøkkel er lagret, eller den lagrede nøkkelen
kan ikke lenger leses her. Skriv inn nøkkelen på nytt.*: legg inn nøkkelen på nytt og
lagre den.

**Fjern påloggingsdataene** spør først — **Fjerne aksesspunktets påloggingsdata?** — fordi
den lagrede nøkkelen slettes og ikke kan vises igjen, og ingenting kan sendes som EHF før
en nøkkel er lagret på nytt; bekreft med **Fjern påloggingsdataene**. Fjerningen avvises
mens et dokument fortsatt er underveis — *Et dokument er fortsatt underveis gjennom
dette aksesspunktet. Vent til det er levert eller feilet før du fjerner eller bytter
påloggingsdataene.* Å bytte til en ny nøkkel avvises aldri.

Avviser Storecove den lagrede nøkkelen mens dokumenter sendes, viser
**Aksesspunkt**-delen av kortet et rødt **Aksesspunktet avviste nøkkelen**, med datoen
det skjedde når den er kjent: *Leverandøren avviste den lagrede API-nøkkelen.
Dokumentene venter i køen til en gyldig nøkkel er lagret.* Lagre riktig nøkkel snart —
varselet forsvinner straks du gjør det, og hvert dokument som venter, går ut neste gang
det står for tur, innen en time — for et dokument som fortsatt står i kø 48 timer etter
at det ble sendt, tas ut av køen som **Feilet** (eller **Ubekreftet**, når Storecove kan
ha det) og må følges opp av en person
([tilstandene](#følg-sendingen-på-kortet-e-faktura-ehf)).

### Avtal KID med banken

En KID er betalingsreferansen banken kobler en innbetaling til. For å bruke den ber du
banken om en KID-avtale (OCR giro) på kontoen i innstillingene: banken registrerer en
**lengde** og en metode for **kontrollsifferet**. Legg inn nøyaktig det banken
registrerte på kortet **KID** — *Kundeidentifikasjonen banken har avtalt for
innbetalinger* — og klikk sidens **Lagre**:

- **KID-lengde**: 4 til 25 tegn, kontrollsifferet medregnet.
- **Kontrollsiffer**: **MOD10 (anbefalt)** eller **MOD11**. Med MOD11 får et nummer der
  kontrollen ville blitt 10, en `-` som kontrollsiffer, noe som forvirrer enkelte
  betalere; derfor er MOD10 det vanlige valget.

Oppgi begge eller ingen (*Velg både lengde og kontrollsiffer, eller ingen av dem.*). Uten
avtale sier kortet *Ingen KID-avtale: fakturaene har nummeret sitt som
betalingsreferanse.*

Med en avtale viser kortet den neste, ut fra neste fakturanummer serveren oppgir, for
eksempel *Neste KID: 0010017 (faktura 1001)*: fakturanummeret fylt ut med nuller foran
til lengden minus én, så kontrollsifferet. To
advarsler kan dukke opp:

- I rødt: *Faktura 1042, den neste som utstedes, får ikke plass i 4 tegn med
  kontrollsifferet. Velg en lengre KID.* — lagringen avvises til den får plass.
- I gult, så lenge den lagrede avtalen gir for lite rom: *Neste fakturanummer gir mindre
  enn to sifre å gå på i KID-lengden. Be banken om en lengre KID før numrene vokser
  forbi den.*

Hva som endres: hver faktura som utstedes fra da av, får en KID. Den skrives som **KID**
i PDF-ens betalingsfelt, e-posten ber kjøperen merke betalingen med den i stedet for
fakturanummeret, og en EHF har den som betalingsreferanse. En kreditnota får aldri en, og
en faktura utstedt før avtalen får ingen i ettertid — en EHF uten KID har ingen
betalingsreferanse i det hele tatt, så kjøperens system aldri tar fakturanummeret for en
KID.

Avtalen kan endres eller fjernes senere. Når det er utstedt dokumenter, minner kortet
deg på: *Utstedte fakturaer beholder KID-ene som ble beregnet under avtalen de ble
utstedt med. Be banken holde den gamle lengden gyldig til de er betalt.* En lengde neste
nummer ikke får plass i, lagres aldri (den røde advarselen over). Utstedelsen stopper
bare når fakturanumrene vokser forbi en lengde som passet da den ble lagret — *Neste
fakturanummer passer ikke lenger i KID-avtalens lengde. Endre KID-avtalen i
innstillingene.* Den gule advarselen om reserve er der for at det aldri skal komme
overraskende.

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

### Arbeid til fakturering

Kortet **Arbeid til fakturering** på samme side — *Mva-koden hver type arbeid faktureres
med, og om nye fakturaer får timeliste* — holder det et utkast laget av arbeid starter
med ([Fakturere arbeid](#fakturere-arbeid)). Det lagres med sidens **Lagre**.

- **Mva-kode for timer**, **Mva-kode for utlegg** og **Mva-kode for milepæler**: koden
  linjene for hver type arbeid får. Alle tre er kode 3 (25 %) til du endrer dem. Listene
  tilbyr kodene som tilbys for nye linjer, og den lagrede når den siden er tatt ut av
  listen, merket *(tilbys ikke lenger)*; velg en annen før du fakturerer arbeid av den
  typen. Så lenge selgeren ikke er registrert i Merverdiavgiftsregisteret, faktureres all
  slags arbeid med kode 7 i stedet, uansett hva kortet sier.
- **Legg ved timeliste på nye fakturaer**: om et nytt fakturautkast får en timeliste i
  PDF-en med mindre du velger noe annet. Av til du slår det på.
- **Hvordan timelisten navngir hver person**: **Initialer (KN)** — standard; en ny «KN»
  blir «KN2» — **Person 1, Person 2**, nummerert i den rekkefølgen de står på timelisten,
  eller **Fullt navn**. *En timeliste viser kunden de ansattes arbeid. Initialer sier
  minst; som arbeidsgiver må du informere de ansatte.* Merket brukes når timelistens
  rader skrives, og en utstedt fakturas timeliste endres aldri
  ([timelisten](/en/reference/invoices/#the-timesheet)).

## Finne et dokument

**Fakturaer** i sidemenyen lister hvert utkast og hvert utstedte dokument: utkast først,
så etter nummer, nyeste først. Hver rad viser **Nummer** (eller *Utkast*), **Type**,
**Tilstand**, **Kunde**, **Fakturadato**, **Forfallsdato**, **Sum**, det som er
**Utestående** på en utstedt faktura, under **EHF** hvor et utstedt dokument står som
e-faktura: *Ikke sendt*, *I kø*, *Overlevert*, *Levert*, *Feilet*, *Ubekreftet* eller
*Avbrutt* ([hva hver betyr](#følg-sendingen-på-kortet-e-faktura-ehf)) — for et utkast er
den tom — og under **Prosjekt** koden til prosjektet dokumentets arbeid hører til, når
alt hører til ett. Klikk nummeret i en rad for å åpne dokumentet, eller et prosjekts kode
for å vise bare det prosjektets dokumenter: listen sier da *Prosjekt P-41*, og krysset ved
siden av (*Fjern prosjektfilteret*) viser alle igjen.

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

Skal du fakturere timer, utlegg eller milepæler, start heller fra arbeidet: utkastet lages
med linjene sine ([Fakturere arbeid](#fakturere-arbeid)).

## Fakturere arbeid

Timene som er godkjent i Timer, utleggene som er klare til fakturering i Utlegg og
faktureringsmilepælene som er klare til fakturering i Prosjekter, blir fakturalinjer her
uten at noe tastes inn på nytt: du velger arbeidet, Vantigo skriver linjene, og
utstedelsen merker arbeidet som fakturert i appen det kom fra
([slik virker det](/en/reference/invoices/#invoicing-work)).

### Hvor det ufakturerte arbeidet står

- På en kundes side starter fanen **Fakturaer** med kortet **Ufakturert arbeid** —
  *Godkjente timer, fakturerbare utlegg og milepæler klare til fakturering som ennå ikke
  står på en faktura. Velg hva som skal faktureres.* — som viser arbeidet på alle
  prosjektene som faktureres den kunden, over kundens dokumenter.
- På et prosjekts side viser fanen **Fakturagrunnlag** — den siste, etter **Timer** og
  **Utlegg** — det samme kortet for det ene prosjektet. Et prosjekt som ikke fakturerer
  noen kunde, sier *Dette prosjektet fakturerer ingen kunde, så arbeidet kan ikke
  faktureres herfra.* På fanen **Økonomi** lenker faktureringsplanen dit: **Fakturer
  arbeidet**.

Begge krever `invoices:access` og `invoices:create`, med Fakturaer-modulen slått på. Er
ingen modul som registrerer arbeid — Timer, Utlegg eller Prosjekter — slått på, vises
ikke kortet på kunden i det hele tatt, og prosjektets fane sier *Ingen modul som
registrerer fakturerbart arbeid — Timer, Utlegg eller Prosjekter — er slått på.* Venter
ingenting, sier kortet *Ingenting å fakturere*.

### Lese kortet

Arbeidet grupperes per prosjekt — en overskrift som *P-41 · Apollo*, med merket
**Fastpris** eller **Ikke fakturerbart** når prosjektet er det — og innenfor et prosjekt i
opptil tre tabeller:

- **Timer**: **Dato**, **Person**, **Arbeidstype**, **Timer** med satsen de faktureres
  med (*4 t à NOK 1 200,00*) og **Beløp**. En arbeidstype med påslag faktureres med sin
  egen sats.
- **Utlegg**: **Dato**, **Type** — **Utlegg**, **Kjøregodtgjørelse** eller
  **Leverandørfaktura** — **Beskrivelse** (en leverandørfakturas starter med leverandøren
  og fakturanummeret), **Avstand** for kjøregodtgjørelse, og **Beløp**, prisen for kunden
  med eventuelt påslag.
- **Milepæler**: **Dato**, dagen den ble klar til fakturering, **Milepæl**,
  **Beskrivelse** og **Beløp**.

En rad som ikke kan velges, er nedtonet, og **Hvorfor ikke** sier hvorfor:

| Hvorfor ikke | Hva det betyr |
| --- | --- |
| *Fastprisprosjekt: timene vises, men faktureres ikke* | Et fastprisprosjekt fakturerer milepælene sine; timene står der for å sammenlignes med planen. |
| *Prosjektet er ikke fakturerbart* | Prosjektet fakturerer ingen — det kan ha endret seg etter at arbeidet ble godkjent. |
| *Ikke i NOK* | Vantigo fakturerer bare i NOK. |
| *Prosjektet fakturerer ingen kunde* | På et prosjekts fane: prosjektet har ingen kunde å fakturere. |
| **På utkast 12** eller **På faktura 1042** | Et utkast holder det allerede, eller en faktura har fakturert det. Lenken åpner dokumentet. |

Under tabellene er *Klart til fakturering: NOK 48 500,00* det som kan velges, per valuta.
Kortet advarer også, uten å stoppe noe:

- på et prosjekt: *Noe av arbeidet på dette prosjektet ble levert for mer enn en måned
  siden; loven krever faktura senest en måned etter levering*;
- på en leverandørfaktura: *En leverandørfaktura med denne leverandøren og dette nummeret
  er allerede fakturert, eller står her to ganger. Sjekk at den ikke faktureres dobbelt*;
- øverst: *Ikke alt arbeidet vises: det er mer enn én lesing kan vise. Fakturer noe av
  det, eller vis til en tidligere dato, for å se resten.*

### Lag et utkast av det

Kryss av radene som skal faktureres, eller boksen øverst i en tabell for å velge alle
radene i den. Linjen ved siden av knappen teller det du har valgt — *3 valgt: NOK
14 500,00*. Klikk **Fakturer det valgte arbeidet**. På en kundes fane tilbys knappen
bare for en aktiv kunde, slik **Ny faktura** gjør: aldri for en som er arkivert, sperret
for fakturering, slått sammen eller anonymisert.

Dialogen **Fakturer arbeidet** gjentar det som er valgt, og spør om:

- **Linjer** — hvordan arbeidet grupperes i linjer, hvert valg med antallet linjer det
  ville gitt (*— 3 linjer*): **Én linje per prosjekt** (standard), **Per arbeidstype**,
  **Per person**, **Per dag** eller **Spesifisert**, én linje per føring. *Timer med ulike
  satser blir alltid egne linjer, og hver milepæl blir en egen linje*; utlegg blir én
  linje per type utlegg, unntatt spesifisert. Linjene skrives på kundens språk —
  «Konsulenttimer, Apollo, september 2026» eller «Consulting hours, Apollo, September
  2026» — og kan redigeres etterpå som alle andre linjer
  ([linjetekstene](/en/reference/invoices/#from-work-to-a-draft)).
- **Legg ved timeliste i PDF-en** — vises når timer er valgt: *Én rad per timeføring:
  dato, person, arbeidstype og timer — aldri notatet på føringen.* Den starter slik
  innstillingene sier for et nytt utkast, og slik utkastet har den når du legger til i et
  ([timelisten](#timelisten)).
- **Mva-kode for timer**, **Mva-kode for utlegg**, **Mva-kode for milepæler** — én for
  hver type som er valgt, med kodene på kortet **Arbeid til fakturering** i
  innstillingene som utgangspunkt, eller kode 7 for alle typer så lenge selgeren ikke er
  mva-registrert. Et viderefakturert utlegg får koden som velges her, aldri mvaen på
  kvitteringen. Vantigo har ikke «utlegg» i merverdiavgiftslovens forstand: en kostnad
  som sendes videre til kunden, er et salg som alle andre.
- **Levert fra** og **Levert til** — *Står den tom, går leveransen fra første til siste
  dag med arbeid.*
- **Merknad** — skrives på fakturaen. Står den tom, og noe av arbeidet er gitt tilbake av
  en kreditnota før, får utkastet en merknad om hva det erstatter — *Erstatter faktura
  1001, kreditert med kreditnota 1002* eller *Replaces invoice 1001, credited by credit
  note 1002*, på kundens språk — som du kan endre på utkastet.
- **Legg arbeidet på** — **Et nytt utkast**, eller et av kundens fakturautkast, vist som
  *Utkast 12 — NOK 9 000,00*. Har kunden mer enn 100 utkast, sier feltet *Bare 100 av
  kundens utkast vises; de andre tilbys ikke her.*

Klikk **Opprett utkast**, eller **Legg til i utkast 12**. Utkastet åpnes. Et nytt utkast
får kundens referanse og betalingsfrist slik **Ny faktura** gir dem. Legges arbeidet til
et utkast, kommer det etter linjene utkastet alt har; utkastet beholder sitt eget
toppfelt og sin merknad, leveringsperioden utvides til å dekke det nye arbeidet med
mindre du oppga en, og timelisten blir slik boksen sa.

### Når veiviseren avviser

Ingenting lages, og dialogen sier *Kunne ikke fakturere arbeidet* og hvorfor:

- *Noe av dette arbeidet ligger allerede på et annet utkast eller en utstedt faktura*,
  med en lenke til dokumentet — noen tok det i mellomtiden.
- *Noe av det valgte arbeidet er endret siden det ble vist. Les arbeidet på nytt og
  velg.*
- *Noe av det valgte arbeidet kan ikke lenger faktureres: godkjenningen er trukket, det er
  fakturert et annet sted, eller modulen er slått av. Les arbeidet på nytt.*
- *Noe av det valgte arbeidet ligger på et fastpris- eller ikke-fakturerbart prosjekt, så
  det kan ikke faktureres.*
- *Noe av det valgte arbeidet hører til et prosjekt som ikke fakturerer denne kunden.*
- *Det valgte arbeidet er i mer enn én valuta, og én faktura er i én valuta*, eller *Det
  valgte arbeidet er ikke i NOK, den eneste valutaen denne modulen fakturerer i.*
- *Arbeid faktureres per prosjekt, og Prosjekter-modulen er slått av.*
- *Ett dokument kan holde høyst 5 000 arbeidsposter. Velg færre, eller lag et nytt
  utkast.*
- *Gruppert slik blir arbeidet mer enn 500 linjer. Velg en grovere gruppering* — og
  dialogen bytter til den fineste grupperingen som får plass, og sier for eksempel
  *«Per dag» får plass innenfor 500 linjer, så linjene grupperes nå slik.*
- Kunden er slått sammen, arkivert, sperret for fakturering eller borte, som for **Ny
  faktura**; eller utkastet du la til i, ble lagret av noen andre i mellomtiden.

Gjelder avvisningen arbeidet, nevner varselet det — *Arbeidet som ble avvist: …* — og
listen leses på nytt, så arbeid som ikke lenger kan faktureres, faller ut av det du
valgte. Sjekk valget og klikk knappen igjen.

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
  365. Forfallsdatoen er fakturadatoen pluss fristen, fastsatt ved utstedelse. Faktureres
  kunden med EHF — faktureringsprofilen foretrekker det, eller kunden har en Peppol-ID —
  og både **Deres referanse** og **Ordrereferanse** er tomme, sier en gul advarsel ved
  referansene *Denne kunden faktureres med EHF, som krever kundens referanse eller en
  ordrereferanse. Legg inn en før du utsteder: ingen av dem kan endres etterpå.* Ta den
  på alvor: et utstedt dokument uten noen av dem kan aldri sendes som EHF, bare
  krediteres og utstedes på nytt.
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

### Arbeidet på et utkast

Et utkast laget av arbeid holder arbeidet på linjene sine: under hver linje viser
redigeringen hva den fakturerer — *Timeføring 4211 · 3. sep. 2026 · 7,5 · NOK 9 000,00*,
*Utlegg …* eller *Milepæl …* — og på en utstedt faktura om hvert er *Fakturert* eller
*Frigitt*. Så lenge utkastet holder arbeidet, kan ikke noe annet utkast ta det. Når alt
arbeidet hører til ett prosjekt, står det *Prosjekt: P-41* under kunden: koden skrives på
PDF-en som *Prosjekt* / *Project*, er med i EHF-en, beholdes av den utstedte fakturaen og
kreditnotaene til den, vises i listens kolonne **Prosjekt**, og er den siste kolonnen i
eksporten til regnskapsføreren ([prosjektet](/en/reference/invoices/#the-project)).

- **Rediger en linjes tekst, antall eller pris** som på ethvert utkast: arbeidet blir
  værende på linjen. En linje der beløpet ikke lenger stemmer med arbeidet, sier fra i
  oransje under linjen og under **Verdt å se på** — *Beløpet på en linje avviker fra
  arbeidet den fakturerer* — og det er lov: en nedskrivning, en avrunding.
- **Fjern en linje** for å gi arbeidet tilbake: etter **Lagre** er arbeidet ufakturert
  igjen, og **Verdt å se på** sier *Lagringen frigjorde arbeid fra dette utkastet* og
  *Frigitt:* med arbeidet nevnt. Sletter du utkastet, gis alt arbeidet tilbake.
- **Bytt kunde** bare vel vitende om at det gir alt arbeidet tilbake: redigeringen sier
  *Bytter du kunde, frigis arbeidet dette utkastet holder: det blir ufakturert igjen når
  du lagrer.*
- **Oppdater arbeidet**, over linjene, leser arbeidet på nytt fra Timer, Utlegg og
  Prosjekter: nye tall tas inn, og arbeid som ikke lenger kan faktureres, fjernes og
  nevnes som frigitt. Knappen er nedtonet mens du har ulagrede endringer (*Lagre
  endringene før du oppdaterer arbeidet*); når den er ferdig, *Arbeidet er oppdatert*.
  Lagret noen andre utkastet i mellomtiden, avvises oppdateringen (*Fakturaen er endret;
  prøv igjen*): prøv igjen.
- Er arbeidet endret siden det ble lagt til, eller kan det ikke lenger faktureres — en
  føring som ikke lenger er godkjent, en milepæl som er flyttet tilbake — sier linjen og
  **Verdt å se på** fra: *Arbeid på dette utkastet er endret siden det ble lagt til;
  oppdater arbeidet, ellers avviser utstedelsen det*, eller *Arbeid på dette utkastet kan
  ikke lenger faktureres; oppdater arbeidet for å fjerne det, ellers avviser utstedelsen
  det.*

Arbeid kommer bare inn på et utkast fra kortet **Ufakturert arbeid**, aldri ved å
redigere en linje.

### Timelisten

Hvert fakturautkast har kortet **Timeliste**, med boksen **Legg ved timeliste i PDF-en**.
Er den krysset av, får fakturaens PDF, etter selve fakturaen og på egne sider, hver
timeføring utkastet fakturerer — **Dato**, **Person**, **Arbeidstype**, **Beskrivelse**
(oppgaven, ellers prosjektets navn, ellers arbeidstypen) og **Timer** — med en sum per
person og en sum for alt. Den viser aldri notatet en person skrev på en føring. Radene
skrives når utkastet lagres, og kortet viser dem da; før du lagrer, sier det
*Timelistens rader skrives når utkastet lagres*, og på et utkast uten timer *Utkastet
holder ingen timer til timelisten*. Hver lagring holder timelisten til timene utkastet
fortsatt fakturerer, **Oppdater arbeidet** skriver den på nytt, og fjerner du krysset,
fjernes den. **Forhåndsvis** viser den; når fakturaen er utstedt, er timelisten en del av
den og endres aldri, uansett hva som senere skjer med en bruker. En kreditnota har ingen
timeliste.

En timeliste forteller kunden hvem som har jobbet med hva. Å fortelle de ansatte at
timene deres vises for kunder, er arbeidsgiverens oppgave — informasjonen GDPR art. 13
krever — og derfor er initialer standard, og fullt navn er et valg som tas på kortet
**Arbeid til fakturering** ([timelisten](/en/reference/invoices/#the-timesheet)).

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

**Å utstede et utkast laget av arbeid** merker også arbeidet som fakturert — timene,
utleggene og milepælene linjene fakturerer — i samme steg, så de forlater det
ufakturerte arbeidet for godt. Utstedelsen avvises, med linjen nevnt, når et arbeid på
den ikke lenger kan faktureres (*Arbeid på linje 2 kan ikke lenger faktureres*), er endret
siden det ble lagt til, allerede er merket som fakturert, hører til et prosjekt som ikke
lenger fakturerer denne kunden eller som nå er fastpris eller ikke fakturerbart — og også
når Prosjekter-modulen er slått av, siden arbeidet da ikke kan kontrolleres, eller når
utkastet ble lagret av noen andre mens du utstedte. Ingenting utstedes og ingen nummer
brukes: oppdater arbeidet eller endre linjen, og utsted på nytt
([tilbakeskrivingen](/en/reference/invoices/#the-write-back)).

## Laste ned PDF-en

Et utstedt dokuments side tilbyr **Last ned PDF**. Filen er navngitt etter dokumentet og
kjøperens språk — `faktura-1001.pdf` eller `invoice-1001.pdf`, `kreditnota-1002.pdf`
eller `credit-note-1002.pdf` — og er den ene PDF-en som ble laget og lagret ved
utstedelse, levert nøyaktig som lagret hver gang ([PDF-en](/en/reference/invoices/#the-pdf)).
Alle med `invoices:access` kan laste den ned.

En faktura med KID viser den i betalingsfeltet, som **KID**, og ber kjøperen betale med
den i stedet for fakturanummeret ([KID-avtalen](#avtal-kid-med-banken)).

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

**Å kreditere en faktura laget av arbeid** gir arbeidet tilbake — timene, utleggene og
milepælene en linje fakturerte, blir ufakturert igjen og klare for en ny faktura — bare
når kreditnotaen returnerer den linjen **i sin helhet**: hele antallet, til fakturaens
egen enhetspris og rabatt, medregnet kreditnotaene som er utstedt før den. En linje som
krediteres delvis, eller til lavere pris, beholder arbeidet sitt fakturert til resten av
linjen er returnert; en milepæl kommer tilbake hel eller ikke i det hele tatt.
Kreditnotautkastets kort **Arbeid denne kreditnotaen gir tilbake** viser, slik utkastet
står, arbeidet utstedelsen ville gi tilbake — *Når den utstedes, blir dette arbeidet
ufakturert igjen og kan faktureres på nytt* — eller sier *Slik den står, gir utstedelsen
ikke noe arbeid tilbake: bare en linje kreditert i sin helhet, til sin egen pris, frigir
arbeidet sitt.* Når den er utstedt, viser fakturaens linjer arbeidet som *Frigitt*, og det er
tilbake på kortet **Ufakturert arbeid**. En ny faktura kan fakturere det frigjorte
arbeidet på nytt; den trenger ikke å nevne kreditnotaen, og veiviseren foreslår en
merknad om hva den erstatter
([frigjøring ved kreditering](/en/reference/invoices/#release-on-credit)).

## Sluttoppgjør

Et stort oppdrag faktureres ofte **a konto** underveis — en vanlig faktura for hvert
avdrag, gjerne en milepæl — og avsluttes med et **sluttoppgjør**: en faktura for helheten
som trekker fra det a konto-fakturaene allerede har fakturert. Et sluttoppgjør er et
vanlig utkast med én **fradragslinje** per tidligere faktura og mva-kode:

- linjen viser til den tidligere fakturaen den trekker fra, en utstedt faktura til samme
  kunde i samme valuta — aldri et utkast, en kreditnota eller en annen kundes faktura;
- **Antall** er **-1** og **Enhetspris** beløpet som trekkes fra, over 0, uten rabatt;
  beløpet skrives med minus, «-125 000,00», og senker totalen;
- **Mva-koden** er en den tidligere fakturaen har en linje på, og linjen avgiftsberegnes
  med satsen den fakturaen ble utstedt med — så en satsendring siden, eller en kode som
  ikke lenger tilbys, endrer den ikke. Teksten som foreslås, er *Tidligere fakturert a
  konto, faktura 985* / *Previously invoiced on account, invoice 985*. Et sluttoppgjør kan
  selv trekkes fra senere, men ikke på en mva-kode der det trekker fra tidligere fakturaer.

For å legge dem til klikker du **Trekk fra tidligere fakturaer** over linjene på et
fakturautkast. Dialogen viser, per mva-kode, hva hver av kundens utstedte fakturaer har
igjen å trekke fra — **Faktura**, **Fakturadato**, **Mva-kode**, **Mva %** og **Igjen å
trekke fra** — *Et fradrag avgiftsberegnes med den tidligere fakturaens sats.* Kryss av
radene som skal trekkes fra, og gi hver et beløp under **Trekk fra**: mer enn 0 og høyst
det som er igjen, som også er det beløpet starter på. Et par utkastet allerede trekker
fra, sier *Står allerede på utkastet*. Klikk **Legg til fradragslinjene**: hver blir en
linje *Tidligere fakturert a konto, faktura 985* eller *Previously invoiced on account,
invoice 985* — på kundens språk fra faktureringsprofilen, ellers ditt — antall -1 til beløpet, med lenken **Trekker fra en
tidligere faktura** under. Antallet, rabatten og mva-koden på en fradragslinje kan ikke
endres; prisen og teksten kan. **Lagre** utkastet for å beholde dem. Har ingen tidligere
faktura noe igjen, sier dialogen *Ingen tidligere faktura til denne kunden har noe igjen
å trekke fra.*

Et fradrag kan ikke ta mer enn den tidligere fakturaen har **igjen** på sin mva-kode: det
den fakturerte der, minus det kreditnotaer har gitt tilbake av det og det tidligere
sluttoppgjør har trukket fra. Et utkast som går over, sier fra under **Verdt å se på**
(*Et fradrag tar mer enn den tidligere fakturaen har igjen på sin mva-kode*), og
utstedelsen avvises med linjen. Et sluttoppgjør må bli **mer enn null**: ett som trekker
fra like mye som det fakturerer, eller mer, får en advarsel og kan ikke utstedes — en
fastpris fakturert i sin helhet a konto avsluttes med den siste a konto-fakturaen. To
fradragslinjer for samme faktura og mva-kode avvises når du lagrer; behold én.

PDF-en viser fakturaene som er trukket fra under referansene (*Fratrukket a konto: Faktura
985 av 01.08.2026*), og EHF-en nevner hver av dem som en tidligere faktura. **Å kreditere
et sluttoppgjør** reverserer også fradragene — kreditnotaen kopierer dem som minuslinjer —
og gir de tidligere fakturaene tilbake det som ble trukket fra; en kreditnota av bare
fradragene, under null, avvises. **Å kreditere en a konto-faktura et sluttoppgjør har
trukket fra** avvises utover det sluttoppgjøret lot være igjen av den: krediter
sluttoppgjøret først
([a konto og sluttoppgjør](/en/reference/invoices/#a-konto-and-the-final-settlement)).

## Registrere betalinger

Mottatte penger registreres for hånd, på den utstedte fakturaens side under
**Betalinger**. Hver registrering krever `invoices:payments`. Vantigo kan også ta inn
bankens egne filer med innbetalinger — OCR-giro og camt.054, importert av noen med
`invoices:payments` ([bankfiler](/en/reference/invoices/#bank-files-and-the-exception-queue);
bankavtalen og nedlastingen står i [Innbetalinger fra banken](/nb/admin/payments/)). En
importert fils innbetalinger avstemmes mot fakturaene dine på KID: en innbetaling som
bærer KID-en til en utstedt faktura, betalt til kontoen den fakturaen viste, registreres
mot den — utestående beløp først, eventuelle purregebyrer og renter med resten — med dagen
banken bokførte den som betalingsdato, KID-en som referanse og den som importerte filen
som den som registrerte den. Hver betaling viser hvor den kom fra: registrert for hånd,
eller hentet fra en linje i en OCR-giro- eller camt.054-fil. En innbetaling avstemmingen
ikke kan plassere — uten KID, med en KID ingen faktura har, mer enn det som gjenstår å
betale, eller en som kan gjenta en betaling som alt er registrert — holdes tilbake for en
person og registreres ikke. Noen med `invoices:payments` behandler den i avvikskøen på
siden **Innbetalinger** ([importere innbetalinger fra banken](#importere-innbetalinger-fra-banken),
[avvikskøen](#avvikskøen)): før den mot én eller flere fakturaer og purrekravene deres, med forslag — en faktura hvis nummer står i
innbetalingens tekst, en hvis utestående beløp den er lik, en for kunden som har betalt fra
samme konto før; avvis den som ikke en kundebetaling, med et notat; for en tilbakeføring,
fjern betalingen banken tok tilbake; bekreft et duplikat eller behold den som en egen
betaling; eller gjenåpne den. Det en innbetaling ikke blir ført mot, står synlig på linjen
— Vantigo fører ingen kreditsaldo og gjør ingen tilbakebetaling. En innbetaling som avvises fordi fakturaen
er kreditert eller allerede betalt, eller fordi den var mer enn det som skyldtes, står også
som ikke ført: pengene skal tilbake, og tilbakebetalingen gjøres utenfor Vantigo. Når en
tilbakeføring har tatt en betaling tilbake, føres eller gjenåpnes linjen den kom fra aldri
igjen.
Det gjelder også den samme innbetalingen i en annen fil fra banken, eller en kopi av
linjen: de holdes tilbake for en person og registreres aldri av seg selv. En
tilbakeføring som pekte på feil betaling, kan ikke angres; registrer den betalingen på nytt
for hånd.

Klikk **Registrer betaling** — tilbys mens noe er igjen å betale — og fyll ut:

- **Betalingsdato**: dagen pengene kom inn, forhåndsutfylt med i dag; på eller etter
  fakturadatoen, og ikke etter i dag.
- **Beløp**: forhåndsutfylt med utestående beløp; mer enn 0 og høyst utestående beløp.
  En overbetaling avvises, med utestående beløp nevnt.
- **Referanse** (bankens eller betalerens) og **Merknad**, begge valgfrie.

Klikk **Registrer**. Tabellen lister hver betalings **Betalingsdato**, **Beløp**, dens
**Kilde** — **Manuelt**, **OCR giro** eller **camt.054**, der en betaling fra banken
viser sin **Banklinje**, lenket til **Innbetalinger** for den som har `invoices:payments` —
dens **Referanse**, **Merknad** og når den ble **Registrert**, og summene får **Betalt** og
**Utestående** — summen minus det som er kreditert og betalt. En betaling på hele det
utestående gjør fakturaen **Betalt**; en mindre gjør den **Delvis betalt**, eller lar den
stå som **Forfalt** etter forfall. En kreditnota tar ingen betaling
([betalinger og tilstanden](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

En feil registrering redigeres aldri: klikk **Fjern** ved siden av den, oppgi en
**Begrunnelse** i dialogen **Fjern betalingen**, og bekreft. *Betalingen blir stående på
fakturaen, gjennomstreket med begrunnelsen. En fjerning kan ikke angres; registrer
betalingen på nytt om den likevel var riktig.*

## Importere innbetalinger fra banken

Åpne **Innbetalinger** i sidemenyen. Den vises bare for den som har `invoices:payments`
(en leser med bare `invoices:access` ser den ikke), og den har fire deler: **Importer en
bankfil**, **Bankkontoer**, **Importerte filer** og **Avvikskøen**.

**Hvor filene kommer fra.** Banken gir deg OCR giro-filer under en OCR/KID-avtale, eller
camt.054-meldinger om innbetalinger; last dem ned fra nettbanken
([Innbetalinger fra banken](/nb/admin/payments/) forteller hvilken avtale og hvor). Rediger
aldri en fil for hånd.

**Importer en fil.** Under **Importer en bankfil** velger du filen i **Bankfil** og klikker
**Importer**. Hele filen kontrolleres først, og ingenting av den importeres hvis noe av den
er feil. Siden sier da hvorfor, under *Bankfilen ble ikke importert*: filen er ikke en OCR
giro- eller camt.054-fil, er for stor eller bryter sine egne regler (med hvor i filen); en
konto den nevner, er verken din **Bankkonto** eller en som en utstedt faktura har skrevet
ut; kontoens filer importeres i det andre formatet; eller det finnes ikke noe
dokumentlager å oppbevare filen i. En fil som er importert før, avvises også, med den
tidligere importen nevnt — filnummeret, når den ble lastet opp, og om det var av deg eller en annen bruker — og en lenke,
**Åpne fil**, til den.

**Hva resultatet sier.** Etter en import teller **Resultatet av importen** filens
**Betalinger i filen**: de som er **Avstemt mot fakturaer**, med beløpet som er registrert
som betalinger; de som er **I avvikskøen**, med beløpet som venter der; de som er
**Importert før, beholdt som duplikater** — betalinger en tidligere eller overlappende fil
hadde brakt inn; de som er **Ikke avstemt ennå**; og de som er **Oversett**, etter
slag — kortinformasjon i en OCR-fil, belastninger som ikke er tilbakeføringer, poster som
ikke er bokført, beløp på null. Hver avstemte betaling registreres mot fakturaen sin —
utestående beløp først, eventuelle purrekrav med resten — med dagen banken bokførte den,
KID-en som referanse og deg som den som registrerte den. **Åpne filen** viser filens egen
side, med hver betaling den brakte inn.

**Avstem resten.** Avstemmingen kjører rett etter importen, én betaling om gangen.
Stopper den tidlig, står filen likevel importert, og noen av betalingene er **Ikke avstemt
ennå**: klikk **Avstem resten** — i resultatet, på filens rad under **Importerte filer**,
eller på filens side — og du er den som registrerer dem. Resultatet viser da hva den
avstemmingen registrerte og la i køen.

**Importerte filer** lister hver fil, nyeste først: formatet, når den ble lastet opp og om
det var av deg eller en annen bruker, bokføringsdagene den dekker, og betalingene talt opp — avstemt, i køen, duplikater og
ikke avstemt ennå.

**Formatet for hver konto.** **Bankkontoer** lister hver konto det er importert en fil
for: formatet — **OCR giro** eller **camt.054**, satt av kontoens første fil — siste fil og
siste bokføringsdag, og etter en endring det tidligere formatet med skjæringsdagen. En fil
i det andre formatet for en konto avvises. For å bytte en konto — for eksempel fra OCR giro
til camt.054 — klikker noen med `invoices:manage` på **Endre formatet** ved siden av den,
velger **Nytt format** og klikker **Endre formatet**. Dialogen forklarer
**skjæringsdagen** først: Vantigo registrerer den siste bokføringsdagen for kontoens
betalinger i det gamle formatet, og holder tilbake en betaling i det nye formatet bokført
den dagen eller tidligere som mulig duplikat, fordi det gamle formatet kan ha brakt den inn
allerede. Gjør endringen når den siste filen i det gamle formatet er importert. Uten
`invoices:manage` vises formatene, og siden sier at det trengs for å endre et.

**En ekte andre betaling som ser ut som en gjentakelse.** En betaling holdes tilbake som
**Et mulig duplikat**, og registreres ikke, når en betaling fra **en annen fil** til samme
konto, bokført samme dag, med samme beløp og samme KID, allerede er registrert — fordi det
er slik én betaling ser ut i to av bankens filer: en melding i løpet av dagen og en ved
dagens slutt, eller en OCR giro- og en camt.054-fil fra samme dag. En kunde som virkelig
betaler samme beløp to ganger samme dag med samme KID, og der de to betalingene kommer i
hver sin fil, får derfor den andre lagt i køen. Sjekk kontoutskriften; er begge
betalingene ekte, finner du linjen i avvikskøen og klikker **Fordel** — mot fakturaen,
purrekravene eller en annen faktura — som for en hvilken som helst annen betaling. To
slike betalinger i samme fil registreres begge.

## Avvikskøen

**Avvikskøen**, nederst på **Innbetalinger**, har betalingene avstemmingen ikke kunne
plassere, og duplikatene — det som venter på en person. Den åpner på **I køen**;
duplikatene står under statusen **Duplikat**. Filtrer den på **Status**, **Årsak** og
**Fil** (skriv for å finne en fil; den tilbyr de siste 500 filene og sier fra når det er
flere), eller kryss av for **Bare betalinger med en rest
som ikke er ført** for å se betalinger som er ført delvis, avstemt og senere fjernet, eller
avvist som penger som skal tilbake. Hver linje viser dagen banken bokførte den, KID-en
eller ellers teksten, betaleren og kontoen deres, beløpet, det som er ført — hver betaling
lenket til fakturaen sin, en fjernet en gjennomstreket — det som ikke er ført, og tilstand
og årsak. **Detaljer** viser hva som har skjedd med linjen, hvert steg med når, og om det var av deg
eller en annen bruker,
fakturaene den kan betale, og for et mulig duplikat linjen den kan gjenta: den linjens fil,
bokføringsdag og betalinger, og om banken tilbakeførte en betaling fra den.

Hver årsak med vanlige ord, og hva du gjør:

| Årsak | Hva den betyr | Hva du gjør |
| --- | --- | --- |
| **KID-en er ikke gyldig** | KID-ens kontrollsiffer er feil | **Fordel** den for hånd, eller avvis den |
| **Ingen faktura har denne KID-en** | et annet systems eller en annen avtales KID | **Fordel** den, eller avvis den |
| **Fakturaen er kreditert** | KID-ens faktura er kreditert i sin helhet | **Ikke en kundebetaling**, med en merknad: pengene skal tilbake, og betales tilbake utenfor Vantigo |
| **Ingenting er igjen å betale** | fakturaen er betalt, og betalingen er mer enn purrekravene | det samme |
| **Mer enn det som skyldes** | mer enn utestående beløp og purrekravene | **Fordel** det som skyldes mot fakturaen og purrekravene; resten blir stående som ikke ført |
| **Ingen KID** | betalingen har ingen KID | **Fordel** den mot én eller flere fakturaer fra forslagene |
| **Et negativt beløp** | en OCR-linje med minustegn | **Ikke en kundebetaling**, med en merknad |
| **En tilbakeføring** | banken tok en betaling tilbake | **Behandle tilbakeføring** |
| **En Vipps-utbetaling** | en utbetaling fra Vipps, ikke en kundes betaling | **Ikke en kundebetaling** |
| **Betalt før fakturaen ble utstedt** | bokført før KID-ens faktura ble utstedt | **Fordel** den etter å ha sjekket, eller avvis den |
| **Betalt til en annen konto** | ikke til kontoen fakturaen viste | **Fordel** den etter å ha sjekket, eller avvis den |
| **Et mulig duplikat** | kontoens skjæringsdag, eller samme betaling allerede registrert fra en annen fil | **Bekreft duplikat**, eller **Fordel** den som en egen betaling |
| **Betalingen ble fjernet** | en avstemt betaling der alle registreringene ble fjernet, gjenåpnet | **Fordel** den på nytt, eller avvis den |
| status **Duplikat** | en tidligere eller overlappende fil brakte samme linje | **Bekreft duplikat**, eller **Behold som egen betaling** |

**Fordel.** Dialogen **Fordel linje …** åpner med fakturaene betalingen kan betale — den KID-en
pekte på, og forslagene: en faktura hvis nummer står i betalingens tekst, en hvis
utestående beløp er betalingens beløp, en åpen faktura for en kunde som har betalt fra samme
konto før — fylt ut i rekkefølge, hver med inntil utestående beløp, til betalingen er
brukt opp. Legg til en annen med **Legg til en faktura etter nummer**, eller fjern en. For
hver faktura fyller du inn **Hovedstol** og **Purrekrav** den betaler; en betaling kan
betale bare purrekravene på en faktura som allerede er betalt. Summen under sier hvor mye
som føres og hvor mye som blir stående som ikke ført, og fordelinger som til sammen er mer
enn det som er igjen av betalingen, avvises i dialogen før noe sendes. Klikk **Fordel**. En
avvisning sies med ord, og ingenting føres: et beløp over en fakturas utestående beløp
nevner fakturaen og det beløpet, en betaling av purrekrav over det som står ute nevner det
som står ute, og en faktura utstedt etter at banken bokførte betalingen, kan ikke betales
av den. Det som ikke føres, blir stående synlig på linjen: Vantigo fører ingen kundesaldo
og betaler ikke tilbake.

**Ikke en kundebetaling.** For en Vipps-utbetaling, en betaling tilbakebetalt utenfor
Vantigo eller et annet systems KID: si hva den er, eller hva som ble gjort med den, i
**Merknad**, og klikk **Avvis**.

**Behandle tilbakeføring.** Dialogen tilbyr betalingene tilbakeføringen kan ta tilbake —
de som er registrert fra banklinjer med samme konto og beløp, bokført samme dag eller
tidligere; den leser de siste 500 slike linjene av hvert slag og sier fra når det er
flere — en eldre betaling tilbys da ikke — og sier fra når de ikke kunne leses, i stedet
for at det ikke finnes noen. Kryss av for den den tilbakefører, eller velg **Ingen betaling fjernes;
merknaden sier hvorfor** og skriv merknaden, og klikk **Behandle tilbakeføringen**. Hver
valgt betaling fjernes med begrunnelsen «Reversed by the bank», og linjen den kom fra,
føres aldri igjen. Uten en betaling eller en merknad avvises tilbakeføringen, med ord. En
tilbakeføring som peker på feil betaling, kan ikke angres: registrer den betalingen på
nytt for hånd.

**Duplikater.** **Bekreft duplikat**, med en valgfri merknad, beholder linjen og
registrerer ingenting. **Behold som egen betaling** gjør en duplikatrad til en egen
betaling, tilbake i køen som et mulig duplikat, klar til å **Fordele**; det avvises når banken
tilbakeførte en betaling fra linjen den gjentar, fordi de pengene gikk tilbake.

**Gjenåpne.** En behandlet linje — eller en avstemt der alle betalingene ble fjernet — går
tilbake til køen med **Gjenåpne**, med sin årsak. Det avvises så lenge en betaling
registrert fra linjen fortsatt står (fjern den på fakturaen først); en linje banken
tilbakeførte en betaling fra, får aldri tilbudet.

Hver handling kontrolleres på nytt idet den gjøres: en linje noen andre har behandlet i
mellomtiden, avvises, med ord, og ingenting endres.

## Purringer

Vantigo sender purringer på forfalte fakturaer: brev med gebyret, kompensasjonen og
forsinkelsesrenten loven tillater, på e-post eller på papir
([purringer](/en/reference/invoices/#reminders)). Det de krever, holdes atskilt fra det
fakturaen selv gjelder, og hvert tall i et brev fastsettes den dagen det sendes.

**En fakturas brev.** Siden til en utstedt faktura har kortet **Purringer**. Øverst står
hva som kommer neste gang, slik Vantigo vurderer det i dag: *Neste: en purring*, *Neste:
et inkassovarsel* eller *Neste: overlever fakturaen til et inkassoselskap* — et forslag;
Vantigo gjør det aldri selv — fra en dag, med brevet slik det ville gått i dag: totalen,
fristen, gebyret, kompensasjonen og rentene. Eller det står hvorfor ingen brev går —
fakturaen står på vent, er overlevert, kunden skal ikke purres, purringer er slått av, et
brev er på vei, fristen i forrige brev er ikke passert, ingen levering innen forfall er
registrert, en sats mangler for et halvår, eller regelverket er ikke gjennomgått — og
hvorfor et brev krever mindre enn det kunne, for eksempel *Ikke gebyr: det har gått under
14 dager siden forrige brev.* Under står hvert brev med nummeret sitt: en **Purring**, en
purring som varsler overlevering til inkasso, eller et **Inkassovarsel**; hvordan det
sendes (**E-post**, med adressen, eller **Papir**); statusen — **I kø**, **Venter på
utskrift**, **Skrevet ut, ikke bekreftet postlagt** (med utskriftsbunken), **Sendt**,
**Feilet** eller **Trukket tilbake** — med hvorfor et brev i kø venter, hvorfor et brev
feilet, og hvorfor og av hvem et brev ble trukket tilbake; datoen, fristen og totalen.
**PDF**-en til et utskrevet eller sendt brev lastes ned ved siden av det av alle som kan
lese fakturaen. Den som har `invoices:payments`, kan trekke tilbake et brev som ikke er
sendt (**Trekk tilbake**), med en begrunnelse — et utskrevet brev kan allerede ligge i
posten, så ta det ut først; et som sendes på e-post i samme øyeblikk, avvises — og sende et
som feilet på nytt (**Send på nytt**).

**Forfallslisten og purrekjøringer.** Vantigo vurderer hver forfalte faktura: hva som
kommer neste gang, med brevet slik det ville gått i dag, og om bankdataene er ferske nok
til å stole på. Den som har `invoices:payments`, forhåndsviser en kjøring og gjør den så:
brevene lages, på e-post til kundens purreadresse eller på papir, og tallene i dem
fastsettes når de sendes. Skjermbildene for forfalte fakturaer kommer med listen og
papirbrevene; til da finnes listen og kjøringene i API-et
([forfallslisten](/en/reference/invoices/#the-overdue-list),
[kjøringer](/en/reference/invoices/#runs)).

**Betalt på fristen.** En betaling som er gitt i oppdrag på fristen i et brev, er i tide,
og banken kan bokføre den dager senere. En OCR-girofil sier når en betaling ble gitt i
oppdrag; en camt.054-fil gjør det ikke, så en betaling derfra vurderes etter dagen den ble
bokført. Standardinnstillingen på 3 dagers karenstid etter en frist, før neste brev,
dekker en betaling gitt i oppdrag på fristen og bokført etter en vanlig langhelg; påsken
kan ta lenger tid, og da er det bekreftelsen en kjøring ber om når den siste bankfilen er
gammel, som er vernet. Med 1 dags karenstid kan en slik betaling utløse et nytt gebyr som
aldri ettergis: hold karenstiden på 3 dager eller mer når bankfilene dine er camt.054.

**Forhåndsvise en del av listen.** En forhåndsvisning kan avgrenses til én kunde eller til
fakturaer med forfall før en dag, slik forfallslisten kan — og må avgrenses når over 5 000
fakturaer har forfalt.

**Slik sendes et brev på e-post.** Vantigo sender brevene som går på e-post selv, i
bakgrunnen, ett om gangen. Hvert brev vurderes på nytt den dagen det går: datoen, fristen
— minst 14 dager fram — gebyret og renten er den dagens, ikke kjøringens, og et brev som
ville gått etter midnatt, vurderes på nytt den nye dagen; en faktura som er
betalt, satt på vent eller overlevert i mellomtiden, får ikke brevet, og brevet trekkes
tilbake med årsaken. Brevet er en PDF, vedlagt en kort e-post på kundens språk, og svar går
til e-postadressen i fakturainnstillingene. Et brev som mangler satser eller en gjennomgått
regelordning, venter, og går når de er på plass. Et brev e-postserveren fortsetter å
avvise, feiler etter 48 timer; det kan da sendes på nytt eller trekkes tilbake fra kortet
**Purringer** ([brev](/en/reference/invoices/#letters),
[jobben](/en/reference/invoices/#the-worker)).

**Brev på papir.** Et brev på papir går når det postlegges. Den som har
`invoices:payments`, skriver ut brevene som venter på utskrift, for dagen de skal
postlegges — i dag eller inntil en uke fram — og hvert brev vurderes for den dagen: datoen,
fristen og gebyret er postleggingsdagens. Et brev som ikke kan gå den dagen, holdes utenfor
og navngis: trukket tilbake i mellomtiden, venter på en sats eller en gjennomgått
regelordning, eller ikke lenger aktuelt — fakturaen er betalt, satt på vent eller
overlevert. Brevene i bunken kommer som én PDF, som kan lastes ned på nytt. Når bunken er
postlagt, bekrefter du at den ble postlagt den dagen, og brevene er sendt; et gebyr dagen
ikke lenger gir grunnlag for — fakturaen er betalt, satt på vent eller overlevert etter
utskriften — ettergis. Ble bunken postlagt en annen dag, må den skrives ut på nytt for
dagen den går. Blir en bunke bekreftet postlagt, eller skrevet ut på nytt, mens den
fortsatt skrives ut, beholder den brevene som allerede er skrevet ut i den; resten
navngis og venter på en annen bunke. Skjermbildene kommer med forfallsområdet; til da finnes dette i API-et
([papir og postlegging](/en/reference/invoices/#paper-and-posting)).

### Purrekrav

Kortet **Purrekrav** på fakturasiden viser det brevene krever, atskilt fra fakturaens eget
utestående beløp: **Krevd** — gebyrene og kompensasjonen de sendte brevene krevde, og
rentene i det siste brevet — **Ettergitt**, **Betalt** og **Utestående**, og **Purrekrav å
betale tilbake** når et krav ble betalt og så ettergitt (tilbakebetalingen gjøres utenfor
Vantigo). Når forsinkelsesrente er slått på, står også rentene påløpt til i dag — bare et
tall; et brev krever dem. Under står hver betaling av purrekrav, en fjernet en
gjennomstreket med begrunnelsen, og hver ettergivelse
([purrekrav](/en/reference/invoices/#charges)).

Den som har `invoices:payments`, klikker **Registrer en betaling av purrekrav** så lenge
noe står ute: **Betalingsdato** (i dag, samme dag som fakturadatoen eller senere og ikke
etter i dag) og **Beløp** — forhåndsutfylt med de utestående purrekravene, og aldri mer: en
overbetaling avvises med det utestående beløpet nevnt — med en **Referanse** og en
**Merknad**. En betaling av purrekrav dekker først gebyrene og kompensasjonen, eldste brev
først, deretter rentene. En feil betaling fjernes med en begrunnelse, slik en betaling
gjør. En betaling av selve fakturaen registreres på kortet **Betalinger**; en
bankbetaling som dekker begge, fordeles i avvikskøen.

### Ettergi et purrekrav

Klikk **Ettergi** på kortet **Purrekrav** (`invoices:payments`) og kryss av det som skal
frafalles: gebyret eller kompensasjonen i et sendt brev — hele kravet i brevet — eller
rentene det siste brevet krevde, som ettergis som et beløp: det brevet krevde og som
fortsatt er ubetalt, ingenting påløpt siden. Velg **Begrunnelse** — *Innsigelsen ble tatt
til følge*, *Krevd ved en feil* eller *Godvilje* — og skriv en merknad. En ettergivelse
fjernes aldri. Ettergivelsene viser hvert krav med brevet, beløpet, begrunnelsen og hvem
som ettergav det og når; en ettergivelse av renter nevner dagen brevet krevde renter til
og med, og en som bankens treff gjorde, sier *Betalt innen fristen likevel*. Et krav som
ikke er krevd, eller som allerede er ettergitt, avvises med ord. Brevene selv er historikk
og beholder det de sa.

### Registrere en levering

En purring kan bare kreve gebyr, kompensasjon eller renter av en faktura som er levert
innen forfall: på e-post, som EHF eller registrert manuelt. Når du overleverte fakturaen
eller sendte den i posten, klikker du **Registrer en levering** på kortet **Leveringer**
(`invoices:issue`), velger **Overlevert** eller **Postlagt**, dagen — fra fakturadatoen
til i dag — og en merknad, og klikker **Registrer**. Kortet viser registreringene under
**Registrert manuelt**, med hvem som registrerte hver. En feil registrering fjernes med en
begrunnelse og blir stående, gjennomstreket; Vantigo avviser fjerningen så lenge et
purrekrav hviler på den leveringen alene — ettergi først de kravene som *Krevd ved en
feil* ([leveringsfaktumet](/en/reference/invoices/#the-delivery-fact)). Uten en levering
innen forfall går bare purringer uten gebyr, og verken inkassovarsel eller overlevering.

### En omtvistet faktura

Når kunden bestrider en faktura, klikker den som har `invoices:payments`, **Sett på vent**
på kortet **Innsigelse** og skriver hva som bestrides. Mens den står på vent, går ingen
purring, forsinkelsesrenten løper videre, og betalinger registreres fortsatt. Brev som ikke
er gått, trekkes tilbake; et utskrevet brev — det kan ligge i posten — og et som sendes på
e-post i samme øyeblikk, gjør det ikke, og siden nevner dem under **Brev som ikke ble
trukket tilbake**, hvert utskrevne med **Trekk tilbake** når du har tatt det ut.

**Fjern fra vent** spør *Var innsigelsen åpenbart grunnløs?* Svaret står på **Nei — den
hadde rimelig grunn**: alle gebyrer og all kompensasjon som er krevd på fakturaen,
ettergis da (innsigelsen tatt til følge), og ingen kreves på den igjen — kortet sier det
etterpå. Bare **Ja — den var åpenbart grunnløs** beholder kravene. Forsinkelsesrente er
ingen kostnad og løper videre uansett
([vent](/en/reference/invoices/#holds-and-the-hand-off-to-collection)).

### Overlevere en faktura til inkasso

Vantigo sender ikke krav til et inkassoselskap; Vantigo registrerer at du gjorde det. På
kortet **Inkasso** (`invoices:payments`) laster **Eksporter til inkassoselskapet** ned
inkassofilen for denne fakturaen — det skyldige beløpet holdt atskilt fra gebyrer og
renter, og det som er ettergitt, i en egen kolonne. Klikk så **Overlever til inkasso** og
oppgi dagen den ble overlevert (ikke før fakturadatoen, ikke etter i dag),
**Inkassoselskap** og **Inkassoselskapets saksnummer**. En faktura uten registrert
levering innen forfall avvises med ord: ble den levert, klikker du **Den ble levert:
registrer leveringen**; ellers krysser du av **Overlever likevel, uten levering innen
forfall** og overleverer igjen. Som når en faktura settes på vent, trekkes brevene som ikke er gått,
tilbake, og de utskrevne nevnes.

Mens den er overlevert, går ingen brev, og betalinger registreres fortsatt: kortet
**Betalinger** sier *Meld fra til inkassoselskapet om hver betaling du får direkte.* Når
kravet kommer tilbake, klikker du **Trekk tilbake overleveringen** med dagen og
begrunnelsen; brev kan følge igjen.

### Purreinnstillinger, regelverket og satser

Under **Fakturainnstillinger** (`invoices:manage`) setter kortet **Purringer** når
purringer går og hva de krever; en endring gjelder brevene som lages etterpå:

- **Tilby purringer** — av: ingen brev lages.
- **Første purring, dager etter forfall** (1 til 60), **Fristen i hvert brev, i dager**
  (14 til 60 — loven krever minst 14), **Karenstid etter en frist, i dager** (1 til 10) og
  **Purringer før inkassovarselet** (0 til 2).
- **Send inkassovarsel** — ditt eget inkassovarsel, bare etter inkassoloven av 1988.
- **Krav overfor en privatperson** (purregebyr eller ingenting) og **Krav overfor en
  næringsdrivende** (purregebyr, kompensasjon for næringsdrivende eller ingenting — aldri
  begge), og **Krev forsinkelsesrente**.
- **Bankdata er gamle etter, i dager** (1 til 30): en kjøring som krever noe på eldre
  bankdata, ber deg bekrefte den.
- **Inkassoregelverket**: den nye inkassoloven trer i kraft en dag som ennå ikke er
  fastsatt, og fra den dagen har ingen brev gebyr. **Den nye loven gjelder fra** står tom
  til dagen er kjent. **Regelverket gjennomgått til og med** er den siste dagen et gebyr
  eller et inkassovarsel lages etter loven av 1988 uten at noen har sett på det igjen —
  høyst ett år fram; kortet sier hvem som gjennomgikk det sist. Etter den dagen venter brev
  med gebyr og inkassovarsler.

Har en kollega lagret innstillingene mens du redigerte, sier kortet **Purreinnstillingene
er endret** og tilbyr **Last inn på nytt**.

Kortet **Inkassosatser** viser **Forsinkelsesrente**, **Kompensasjon for
næringsdrivende** og **Inkassosats** som daterte rader Vantigos utgivelser fyller inn, hver
gjeldende fra sin dag til neste rad av samme slag; raden som gjelder i dag, er merket
**Gjelder**. **Legg til en sats** legger inn en rad før en utgivelse: slaget, dagen den
gjelder fra — etter i dag og etter siste utskrevne eller sendte brev, og 1. januar eller
1. juli for renten og kompensasjonen — verdien innenfor grensene for slaget (rente 0,01 til
30 prosent; kompensasjon 100 til 2 000 og inkassosats 100 til 5 000 kroner) og forskriften.
En rad av samme slag og dag avvises med ord. Dine egne rader som ikke gjelder ennå og som
ingen brev har brukt, kan slettes; en utgivelses rader aldri. Når en utgivelse har en
annen verdi for en rad en administrator la inn, varsler kortet og merker raden med
utgivelsens verdi ([satser](/en/reference/invoices/#collection-rates)).

### En kundes purreregel

Fanen **Fakturaer** hos en kunde slutter med kortet **Purreregel**: **Normal** (brev slik
purreinnstillingene lager dem), **Uten purrekrav** (brev uten gebyr, kompensasjon eller
renter) eller **Ingen purringer** (ingen brev i det hele tatt; kundens forfalte fakturaer
vises fortsatt), med en merknad og hvem som satte den. Alle med `invoices:access` leser
den; den som har `invoices:payments`, endrer den og klikker **Lagre**. En kunde uten
faktura eller kladd her, eller en anonymisert kunde, kan ikke få noen purreregel, og
kortet sier det. Når to kunder slås sammen, vinner den strengeste regelen.

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
**Elektronisk faktura er påkrevd** — en PDF på e-post oppfyller ikke plikten; kan
dokumentet sendes som EHF i stedet, sier den første det
([e-post eller EHF](#når-kunden-forventer-ehf-men-du-sender-e-post)). Resten er
merknader: kjøperen er en norsk virksomhet (før 2027), eller kunden foretrekker eFaktura
eller papir. Ingen av dem stopper sendingen. På en faktura som er delvis betalt eller
kreditert, sier en merknad at e-posten bare ber om det utestående beløpet; på en som er
gjort opp, at den sier det ikke er noe å betale.

Klikk **Send**. E-posten går med en gang, med den lagrede PDF-en vedlagt og en kort,
ren tekst — *Faktura 1001 fra <selger>* eller *Invoice 1001 from <selger>* på kjøperens
språk — som ber om utestående beløp til selgerens konto, merket med fakturaens KID når den har
en og med nummeret ellers; svar går til e-posten i
innstillingene ([tekstene](/en/reference/invoices/#sending-a-document)). En melding
bekrefter *Sendt til …*, og kortet **Leveringer** på dokumentet får en rad med
**Sendt**, **Til** og **Emne**. Kolonnen **Til** vises bare for den som har
`invoices:issue`; andre ser når og under hvilket emne hver sending gikk.

En sending avvises når kunden er anonymisert (*Kunden er anonymisert, og det sendes ikke
mer til den*), når det ikke finnes noen adresse, når dokumentlageret er utilgjengelig,
eller etter mer enn 60 sendinger på ti minutter fra ett sted. Bekrefter e-postserveren
ikke sendingen, sier dialogen *E-postserveren bekreftet ikke sendingen. Ingenting ble
registrert; den kan likevel ha kommet fram. Sjekk med kunden før du sender på nytt.* En
rad under **Leveringer** betyr at e-postserveren tok imot e-posten, ikke at den kom
fram: en retur går til installasjonens avsenderadresse og registreres ikke her. Samme
dokument kan sendes på nytt, og loggføres da på nytt.

## Sende som EHF

Å sende som EHF overleverer en utstedt faktura eller kreditnota til kundens eget system
gjennom Peppol-nettverket, med PDF-en inni. Det krever `invoices:issue` og en
installasjon som er satt opp for det — kortet **E-faktura** i innstillingene sier
*Sending som EHF er tilgjengelig* ([Sett opp e-faktura](#sett-opp-e-faktura)) — og et
dokument utstedt til en kunde som hadde en Peppol-ID da.

### Send en faktura som EHF

Åpne det utstedte dokumentet. Hvilken knapp som kommer først, avhenger av kunden:

- Foretrekker kundens faktureringsprofil EHF — eller har kunden en Peppol-ID og ingen
  preferanse — og dokumentet kan sendes, er **Send som EHF** sidens hovedknapp, ved
  siden av **Last ned PDF**, og **Send** (på e-post) står ved siden av som andrevalg.
- Ellers ligger **Send som EHF** på kortet **E-faktura (EHF)**, og tilbys så lenge
  dokumentet kan sendes.

Kan dokumentet ikke sendes, sier kortet hvorfor, med de samme ordene en avvist sending
ville brukt (nedenfor) — eller, når installasjonen ikke er satt opp, *Denne
installasjonen kan ikke sende EHF ennå: se E-faktura i fakturainnstillingene.*

Klikk **Send som EHF**. Dialogen viser hvor dokumentet skal — *Til Peppol-ID 0192:…*,
ID-en dokumentet ble utstedt med — og dokumentet og beløpet, og forklarer hva som skjer:
*Før dokumentet legges i kø, spørres Peppol-nettverket om mottakeren tar imot denne
typen dokument. Et aksesspunkt leverer det deretter, og E-faktura-kortet følger det.*
Er dokumentet allerede sendt på e-post, sier en merknad *Dette dokumentet er allerede
sendt på e-post*; å sende det som EHF i tillegg er lov. Klikk **Send som EHF** i
dialogen. Meldingen *Lagt i kø for sending som EHF* bekrefter det, og kortet viser
**I kø**.

En sending kan avvises; dialogen sier da *Kunne ikke sende som EHF* og hvorfor:

- *Dokumentet ble utstedt til en kjøper uten Peppol-ID, så det kan ikke sendes som EHF.
  Send det på e-post, eller krediter det og utsted det på nytt når kunden har en.*
  Dokumentet beholder kunden slik den var ved utstedelse, så å legge Peppol-ID-en inn på
  kunden etterpå hjelper ikke for dette dokumentet.
- *EHF krever kjøperens referanse eller en ordrereferanse, og dokumentet har ingen av
  dem. Krediter det og utsted det på nytt med en.* Det er fellen utkastet advarer mot.
- *Dokumentet er allerede underveis som EHF, eller levert. Avbryt eller avklar den
  sendingen først.*
- *EHF-en til dokumentet bryter en Peppol-regel, så det kan ikke sendes som EHF*, fulgt
  av *Reglene den bryter:* — en linje med mva-kategori K sies med ord (*En linje har
  mva-kategori K (levering innen EU), som ikke sendes som EHF*), enhver annen regel med
  den offisielle ID-en. Send et slikt dokument på e-post.
- *Mottakeren tar ikke imot dette dokumentet som EHF i Peppol-nettverket. Send det på
  e-post i stedet*, med det nettverket svarte: *Mottakeren er ikke registrert i
  Peppol-nettverket*, eller *Mottakeren er registrert i Peppol-nettverket, men tar ikke
  imot denne typen dokument* — noen mottakere tar imot fakturaer, men ikke kreditnotaer.
- *Peppol-nettverket kunne ikke svare på om mottakeren tar imot EHF. Prøv igjen.*
- At e-faktura ikke er tilgjengelig på denne installasjonen: driftsansvarliges bryter
  eller Peppol-oppslaget er slått av, eller påloggingsdataene til aksesspunktet eller
  selgerens Peppol-ID mangler eller kan ikke lenger leses. Den som har `invoices:manage`,
  ser på **E-faktura** i innstillingene.
- *Dokumentlageret er utilgjengelig*, *Kunden er anonymisert, og det sendes ikke mer til
  den*, eller mer enn 60 sendinger som EHF på ti minutter fra ett sted.

### Følg sendingen på kortet E-faktura (EHF)

Hvert utstedte dokument har kortet **E-faktura (EHF)** ved siden av **Leveringer**.
Det viser siste tilstand med ord, med når dokumentet ble lagt i kø, overlevert, levert
eller feilet, og — for den som har `invoices:issue` — leverandørens referanse og årsaken
til at en sending feilet. Under lister **Sendinger** hvert forsøk, nyeste først, med når
det ble **Lagt i kø**, **Status** og **Mottaker**.

| Kortet sier | Hva det betyr for deg |
| --- | --- |
| **Ikke sendt** | *Ikke sendt som EHF ennå.* |
| **I kø** | Vantigo holder dokumentet og overleverer det til aksesspunktet i løpet av sekunder. Til Vantigo første gang prøver å overlevere det, kan det avbrytes. |
| **Overlevert aksesspunktet** | Aksesspunktet har dokumentet og leverer det. Du trenger ikke gjøre noe: Vantigo får beskjed fra leverandøren og sjekker selv etter fem minutter, igjen etter femten, og så hver time. |
| **Levert til mottakerens aksesspunkt** | Mottakerens aksesspunkt har bekreftet at det fikk dokumentet. Det er det sterkeste beviset Peppol gir — ikke at noen har lest eller godkjent det. Dokumentet kan ikke sendes som EHF igjen. |
| **Feilet** | Det ble ikke levert: aksesspunktet avviste det, mottakeren har forlatt nettverket, eller det sto i kø i to døgn uten å nå leverandøren. Les årsaken, rett det den peker på, og send på nytt — **Send som EHF** tilbys igjen — eller send på e-post. |
| **Ubekreftet – må sjekkes med leverandøren** | Vantigo kan ikke vite om det kom fram: leverandøren tok imot det, men har ikke bekreftet leveringen på sju dager, eller overleveringen ble avbrutt, og leverandøren kan ha det eller ikke. En ny sending venter til en person har avklart den. |
| **Avbrutt** | Noen avbrøt sendingen før den ble overlevert. Dokumentet kan sendes på nytt. |

**Avbryt sendingen** tilbys på en rad i kø for den som har `invoices:issue`, helt til
Vantigo har prøvd å overlevere dokumentet til aksesspunktet; da forsvinner knappen, og
utfallet avgjør. Starter overleveringen idet du klikker, er svaret *Sendingen kan
allerede ha nådd aksesspunktet, så den kan ikke lenger avbrytes* — vent da på
utfallet.

**Avklar** tilbys på en ubekreftet rad. Sjekk først med leverandøren: slå opp sendingen
hos Storecove med leverandørens referanse på kortet, eller med når den ble lagt i kø —
eller spør den i bedriften som har Storecove-kontoen — og se om den ble levert til
mottakerens aksesspunkt eller feilet. Kan ikke leverandøren svare, spør kunden om de
fikk fakturaen. Velg så **Utfall**, **Levert** eller **Feilet**, i **Avklar den
ubekreftede sendingen**, skriv **Hva leverandøren sa** (1 til 500 tegn) og klikk
**Avklar**: *Sendingen er avklart*, og raden viser merknaden som *Avklart: …*.
**Levert** lukker den. **Feilet** lar deg sende dokumentet på nytt, og den sendingen har
nøyaktig den samme EHF-en, slik at kunden, om den første likevel kom fram, har to kopier
av ett dokument, aldri to forskjellige. Vantigo spør også selv leverandøren om en
ubekreftet sending én gang i døgnet i tretti dager, og avklarer den på egen hånd — som
levert, eller som feilet om Storecoves hendelse sier det — om leverandøren til slutt
svarer; merknaden sier da at leverandøren avklarte den, og en sending etter en slik feil
har en ny EHF.

**Last ned EHF (XML)** på hver rad laster ned EHF-en nøyaktig slik Vantigo lagret den da
den ble lagt i kø, navngitt som PDF-en med sendingens nummer lagt til — `faktura-1001-1001.xml`
eller `invoice-1001-1001.xml` — for alle med `invoices:access`. Aksesspunktet bygger EHF-en
det leverer, på nytt ut fra denne filen; kopien det faktisk leverte, oppbevares sammen
med kvitteringen i installasjonens dokumentlager.

### Når kunden forventer EHF, men du sender e-post

E-post er alltid mulig, og ingen av kanalene stopper den andre. Foretrekker kundens
faktureringsprofil EHF, og kan dokumentet sendes som EHF, viser dialogen **Send på
e-post** det røde **Kunden forventer EHF**: *Denne kunden forventer EHF, og dokumentet
kan sendes som EHF. En PDF på e-post oppfyller ikke plikten til e-faktura.* Lukk den og
bruk **Send som EHF** i stedet, med mindre du har avtalt noe annet med kunden. Er
dokumentet allerede underveis som EHF, eller levert, sier e-postdialogen *Dette
dokumentet er allerede underveis som EHF, eller levert*; en e-post sender da kunden en
kopi til.

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
VAT NOK, Credits number, KID og Project — importer KID-kolonnen som tekst, ellers fjerner
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
sperret, sammenslått eller anonymisert. Over listen viser kortet **Ufakturert arbeid**
kundens arbeid som ikke er fakturert ennå, til den som også har `invoices:create`
([Fakturere arbeid](#fakturere-arbeid)). Under listen sier kortet **Purreregel** om
kunden purres og avkreves gebyrer, endret med `invoices:payments`
([En kundes purreregel](#en-kundes-purreregel)).

## Oppbevaring og anonymiserte kunder

Ingenting utstedt slettes noensinne: et utstedt dokument, betalingene, sendingsloggen og
EHF-sendingene er bokføringsmateriale som oppbevares fem år etter regnskapsårets slutt,
og PDF-ene og EHF-filene ligger i installasjonens dokumentlager, der sikkerhetskopiene
er en del av oppbevaringen
([oppbevaring](/en/reference/invoices/#retention-and-personal-data)). Slås to kunder
sammen, følger dokumentene den gjenværende kunden, men beholder kjøperen som står på
dem. Anonymiseres en person i Kunder, slettes utkastene deres, mottakeren på hver
sending blankes — kolonnen **Til** viser da *(anonymisert)* — merknadene på betalingene
deres tømmes, og en EHF som fortsatt venter i køen, og som Vantigo aldri har prøvd å
overlevere til aksesspunktet, avbrytes; de utstedte dokumentene, med kjøperen de
navngir og timelistene sine, blir stående. Ingen dokumenter sendes til en anonymisert kunde igjen, men en
kreditnota kan fortsatt utstedes, med kjøperen originalen navnga.

## Rettigheter

Ingen innebygd rolle har disse; en eier har alt
([rettigheter](/en/reference/invoices/#permissions)).

| Du vil | Du trenger |
| --- | --- |
| Åpne appen, lese hvert dokument, laste ned PDF-er og EHF-filer, se betalinger, sendinger og EHF-tilstander, lese journalen, eksportere CSV-filen, se kortet på dashbordet, lese forfallslisten, se en fakturas purringer, purrekrav, leveringer, vent og overlevering og laste ned PDF-en til et brev, lese en kundes purreregel | `invoices:access` |
| Lage, redigere, forhåndsvise og slette utkast | `invoices:create`, og `customers:view` for å velge kjøperen |
| Se det ufakturerte arbeidet — timene, personene og satsene — på en kundes fane Fakturaer eller et prosjekts fane Fakturagrunnlag, lage et utkast av det eller legge det til i et, oppdatere arbeidet på et utkast, slå timelisten av eller på, trekke fra tidligere fakturaer | `invoices:create` |
| Utstede et utkast — som merker arbeidet på det som fakturert i Timer, Utlegg og Prosjekter, uten å spørre etter rettighetene der — lage en kreditnota, sende et dokument på e-post eller som EHF, se hvor hver sending gikk, avbryte eller avklare en EHF-sending, registrere en levering manuelt eller fjerne en | `invoices:issue` |
| Registrere en betaling eller fjerne en med begrunnelse; importere bankfiler, og bruke **Innbetalinger** og avvikskøen der; sette en kundes purreregel; forhåndsvise og gjøre purrekjøringer; trekke tilbake et brev eller sende et som feilet på nytt; registrere en betaling av purrekrav, fjerne en, ettergi et purrekrav; sette en faktura på vent og fjerne den fra vent, registrere en overlevering til inkasso og trekke den tilbake, eksportere inkassofilen | `invoices:payments` |
| Redigere selgeropplysningene, nummerserien, Peppol-ID-en, aksesspunktet, KID-avtalen, mva-kodene, kortet **Arbeid til fakturering**, inkassosatsene og purreinnstillingene; endre en bankkontos filformat under **Innbetalinger** | `invoices:manage` |
