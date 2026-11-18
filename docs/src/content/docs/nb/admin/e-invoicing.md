---
title: E-faktura
description: Sende fakturaer som EHF i Peppol-nettverket gjennom et aksesspunkt hos Storecove, de to bakgrunnsjobbene som bærer dem, KID-avtalen med banken, og eFaktura og AvtaleGiro gjennom Mastercard Payment Services.
sidebar:
  order: 42
sources:
  - apps/server/internal/invoices/accesspoint
  - apps/server/internal/invoices/ehf
  - apps/server/internal/consumerinvoice
---

EHF er den norske e-fakturaen: EHF Billing 3.0, som er Peppol BIS Billing 3.0 med to
norske regler i tillegg — en faktura eller kreditnota som UBL 2.1-XML som kjøperens
regnskapssystem leser uten å taste inn noe på nytt, med Vantigos PDF av dokumentet
innebygd. Den går gjennom **Peppol**, et nettverk av aksesspunkter: avsenderen
overleverer dokumentet til sitt aksesspunkt, som slår opp mottakeren i nettverkets
register og leverer det til mottakerens aksesspunkt. Vantigo er ikke selv et
aksesspunkt; det overleverer hvert dokument til ett, **Storecove**, gjennom API-et deres.
En faktura der alt arbeidet hører til ett prosjekt, navngir prosjektet i EHF-en med
prosjektets kode — som prosjektreferanse (BT-11, `cac:ProjectReference`) på en faktura,
og på en kreditnota, der formatet ikke har det feltet, som en tilleggsdokumentreferanse
med typekode 50 — slik at kjøperens system kan rute den
([tilordningen](/en/reference/invoices/#the-ehf-document)). Et sluttoppgjør nevner hver
a konto-faktura det trekker fra som en tidligere faktura (BG-3, én
`cac:BillingReference` hver) og fører fradragene som linjer med antall -1 og negativt
beløp, aldri som et forhåndsbetalt beløp, så kjøperens system ser hva som ble fakturert
før, og mvaen går opp per sats. En faktura for strømforbruk navngir på hver linje i en
avregning målepunktet som en vareegenskap (`cac:AdditionalItemProperty`, navn
«Målepunkt-ID», med målepunkt-ID-en som verdi), fører den avregnede perioden som
fakturaperiode (`cac:InvoicePeriod`), skriver enhetspriser med fire til åtte desimaler så
hver linjes beløp er nøyaktig antall × pris, og fører en korreksjon som gir penger
tilbake som en linje med antall -1
([fakturering av forbruk](/en/reference/invoices/#on-the-documents)).

Pliktene: offentlige virksomheter har krevd EHF fra leverandørene sine siden 2019, og fra
**1. januar 2027** skal en norsk virksomhet sende fakturaene sine til andre norske
virksomheter som e-faktura — en PDF på e-post oppfyller ikke lenger plikten. Å motta
e-faktura blir en plikt i 2030 og er ikke en del av Vantigo ennå. En avsender trenger
ingen egen registrering i Peppol-registeret (ELMA) for å sende; den trenger en konto hos
et aksesspunkt. Denne siden er driftssiden av saken: kontoen, innstillingene,
bakgrunnsjobbene og hva du gjør når noe går galt. Den som sender fakturaer, har
[brukerveiledningen](/nb/user/invoices/#sende-som-ehf); reglene står i
[referansen](/en/reference/invoices/#sending-as-ehf).

## Før du begynner

- Fakturaer-modulen er slått på (`MODULES`, sammen med `customers`), og
  selgeropplysningene i **Fakturainnstillinger** har organisasjonsnummeret.
- **`PEPPOL_LOOKUP_ENABLED=1`**, standardverdien. Hver sending spør Peppol-nettverket på
  nytt om mottakeren tar imot dokumentet, så med oppslaget slått av er også EHF slått av.
  Oppslaget trenger utgående DNS og HTTPS til registerets SMP-servere
  ([Peppol-innstillingene](/nb/admin/authentication/#transport-lagring-og-moduler)).
- **`INVOICES_EHF_ENABLED=1`**, standardverdien. `0` slår av sending som EHF: en sending
  svarer 503 `ehf_unavailable`, bakgrunnsjobbene nedenfor starter ikke, og
  innstillingssiden sier at EHF ikke er tilgjengelig.
- **`INVOICES_STORECOVE_BASE_URL`**, som standard Storecoves produksjons-API
  `https://api.storecove.com/api/v2/`. Det er driftsansvarliges innstilling, aldri en
  brukers: ingen i appen kan peke adapteren et annet sted. Utgående HTTPS til den verten
  trengs, og til nedlastingslenkene Storecoves kvittering peker på.
- **Et objektlager** ([Objektlagring](/nb/admin/object-storage/)). Utstedelse trenger
  allerede ett; EHF-filer og kvitteringer lagres ved siden av PDF-ene.
- **Bakgrunnsjobbene kjører** — i modusen `api` med `WORKERS_IN_PROCESS=1`
  (standardverdien) eller i en `worker`-container ([Bakgrunnsjobber og
  skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)). Uten dem blir et
  sendt dokument stående i kø.
- **Selgerens Peppol-ID.** Den er som standard `0192:` og organisasjonsnummeret, som er
  det en norsk virksomhet bruker; sjekk den på kortet **E-faktura**.
- Noen med `invoices:manage` som legger inn påloggingsdataene.

## Valg av aksesspunkt

Vantigo snakker med én leverandør, **Storecove**, valgt fordi API-et dekker det en
avsender uten tilsyn trenger: en overlevering med en nøkkel som gjør at et nytt forsøk
aldri blir en faktura til, en kø av leveringshendelser, og en kvittering med kopien som
ble levert. Andre kan komme bak den samme porten; ingen støttes i dag. Storecove selger
kontoer gjennom en salgskontakt, ikke et registreringsskjema, og priser dem etter avtale.

## 1. Opprett en konto hos Storecove

1. Be Storecove om en konto gjennom kontaktskjemaet på
   [storecove.com](https://www.storecove.com/). Du får først en **testkonto i tretti
   dager**: den samme API-verten, med en sandkassenøkkel. Bruk den til å prøve hele
   kjeden før du ber om produksjon.
2. Opprett selskapet som en **juridisk enhet** (legal entity) hos Storecove — navn,
   adresse, land NO — og gi den **Peppol-identifikatoren** sin: skjemaet `NO:ORG`
   (superskjema `iso6523-actorid-upis`) med organisasjonsnummeret.
3. **Hold de to identifikatorene like.** Storecoves `NO:ORG` med organisasjonsnummeret er
   den samme deltakeren som Vantigos Peppol-ID `0192:<organisasjonsnummer>`. Storecove
   bygger EHF-en den sender, på nytt ut fra den Vantigo overleverer, og avsenderen i
   nettverket kan bli den juridiske enhetens identifikator; er de to ulike, navngir
   dokumentet Vantigo registrerer og det kunden mottar, forskjellige avsendere. Endrer du
   den ene, endrer du begge.
4. Noter den juridiske enhetens numeriske **ID**, og lag en **API-nøkkel** for kontoen.

**Én Storecove-konto per installasjon.** Utfallet av en levering kommer fra kontoens
hendelseskø, som Vantigo tømmer og kvitterer for hendelse for hendelse — hver hendelse
den leser, sin egen eller ikke, siden den ikke kan la en ligge først i køen. Et annet
system som leser den samme kontoen, eller en annen Vantigo-installasjon, ville mistet
hendelsene sine til denne, og denne til det. Gi produksjon, test- og akseptansemiljøet og hver
testinstallasjon en egen konto.

## 2. Lagre påloggingsdataene

I appen, med `invoices:manage`: **Fakturainnstillinger** → **E-faktura** →
**Aksesspunkt**, fyll inn **ID for juridisk enhet** og **API-nøkkel**, og klikk **Lagre
aksesspunkt**. Eller gjennom API-et:

```http
PUT /api/v1/invoices/settings/access-point
Content-Type: application/json

{"provider": "storecove", "legalEntityId": 12345, "apiKey": "…"}
```

Nøkkelen forsegles med en nøkkel utledet fra `APP_SECRET` og lagres i sin egen rad,
`invoices.access_point_credentials`; den vises, logges eller returneres aldri igjen —
svaret sier bare `hasCredentials`. Utelates `apiKey`, beholdes den lagrede, så en ny ID
for den juridiske enheten trenger ingen nøkkel. **Endres `APP_SECRET`, blir den lagrede
nøkkelen uleselig**: neste bruk logger en feil, innstillingssiden viser *Aksesspunktet
avviste nøkkelen*, og nøkkelen må legges inn på nytt
([påloggingsdataene](/en/reference/invoices/#the-access-points-credentials)).

Å fjerne påloggingsdataene (**Fjern påloggingsdataene**, eller `DELETE` på den samme
stien) og å bytte til en annen leverandør avvises med 409 `transmissions_active` mens en
sending står i kø, er overlevert eller er ubekreftet: leverandøren har fortsatt det de
trenger. Å bytte nøkkel avvises aldri, så en avvist nøkkel kan rettes mens dokumenter
venter.

## 3. Kontroller

Klikk **Kontroller** på kortet (eller `POST /api/v1/invoices/settings/access-point/verify`).
Vantigo leser den juridiske enheten fra Storecove med den lagrede nøkkelen og svarer:

| Svar | Betydning |
| --- | --- |
| *Aksesspunktet godtok nøkkelen.* (`ok`) | Nøkkelen virker for den juridiske enheten; et flagg om avvist nøkkel fjernes. |
| *Aksesspunktet avviste nøkkelen.* (`unauthorized`) | Storecove svarte 401 eller 403: en feil, tilbakekalt eller utløpt nøkkel. Flagget settes. |
| *Aksesspunktet kunne ikke nås, eller nøkkelen gir ikke tilgang til denne juridiske enheten.* (`unreachable`) | Nettverket, et tidsavbrudd, en serverfeil hos Storecove, eller en ID for en juridisk enhet nøkkelen ikke dekker. |
| *Ingen nøkkel er lagret, eller den lagrede nøkkelen kan ikke lenger leses her.* (503 `ehf_unavailable`) | Vantigo kan ikke lese den lagrede nøkkelen — `APP_SECRET` er endret, eller raden er endret. Flagget settes og en feil logges; legg inn nøkkelen på nytt. Uten lagrede påloggingsdata svarer Kontroller 409 `ehf_unavailable`. |

Sjekk så at kortets **Hva e-faktura trenger** sier *Sending som EHF er tilgjengelig*. Den
første virkelige sendingen er det endelige beviset: send én faktura til en kunde som
venter EHF, og se kortet komme til **Levert til mottakerens aksesspunkt**.

## Hva bakgrunnsjobbene gjør

To bakgrunnsjobber bærer et sendt dokument; de kjører der denne installasjonen kjører
bakgrunnsjobber, og bare mens `INVOICES_EHF_ENABLED` er på. Med Peppol-oppslaget slått av
følger de fortsatt opp det som allerede er overlevert, men overleverer ikke noe nytt.

- **`invoices-ehf`** tar hvert femte sekund én sending som står for tur og holder en lås
  på den i 60 sekunder, så to replikaer aldri håndterer den samme, og gjør **ett kall til
  Storecove per runde**, begrenset til 30 sekunder.
  - Et dokument **i kø**: er mottakeren sist slått opp for mer enn 24 timer siden, kjøres
    oppslaget på nytt (en mottaker som har forlatt nettverket, gir sendingen feilet med
    `receiver_not_receivable`); så overleveres dokumentet under sendingens egen
    idempotensnøkkel, så et nytt forsøk aldri blir en faktura til. En nettverksfeil eller
    en serverfeil prøves igjen, med dobbelt så lang ventetid hver gang, opp til en time;
    en 429 venter så lenge Storecove ber om; en avvist nøkkel venter en time, setter
    flagget om avvist nøkkel og logger en feil. Et dokument som fortsatt står i kø
    **48 timer** etter at det ble sendt, gis opp: som **ubekreftet** når Storecove kan ha
    det, som **feilet** når det aldri kom så langt.
  - Et **overlevert** dokument: Storecoves kvittering etterspørres etter 5 minutter,
    igjen etter 15, og så hver time, i tilfelle hendelseskøen aldri nevner det; er det
    fortsatt overlevert **sju dager** etter overleveringen, blir det **ubekreftet**.
  - Et **levert** dokument: kvitteringen og den leverte kopien hentes én gang og lagres
    (nedenfor).
  - Et **ubekreftet** dokument med en referanse fra Storecove: etterspørres én gang i
    døgnet i tretti dager, og merkes levert om Storecove til slutt har en kvittering;
    etter det venter det på en person. Uten referanse venter det på en person med en
    gang.
- **`invoices-ehf-events`** kjører hvert 30. sekund, under en rådgivende lås (advisory
  lock, `pg_try_advisory_lock`) i PostgreSQL, så bare én replika tømmer køen om gangen:
  mens en sending er overlevert, ubekreftet og fortsatt etterspørres eller nylig satt i
  kø uten referanse, eller står i kø etter at en overlevering er forsøkt, leser den
Storecoves hendelseskø til den er tom — høyst 500
  hendelser per runde — merker hvert dokument levert (Storecoves `succeeded`:
  mottakeraksesspunktets kvittering) eller feilet (`failed`, `no_action_taken`), også et
  ubekreftet, og kvitterer for hver hendelse. En hendelse databasen avviser helt,
  logges som feil og kvitteres likevel, så den ikke kan holde igjen køen bak seg.

**Levert** betyr at mottakerens aksesspunkt har kvittert for meldingen — ikke noe
sterkere: ikke at kundens system har godtatt fakturaen, eller at noen har lest den.

## Ubekreftede og feilede sendinger

**Ubekreftet** betyr at Vantigo ikke kan vite om dokumentet kom fram: Storecove kan ha det
uten å ha bekreftet det. Maskinen gjetter ikke, for en gjetning ville enten sendt en
faktura til eller mistet en. En ny sending av dokumentet venter til noen med
`invoices:issue` avklarer den på dokumentets kort **E-faktura (EHF)** med **Avklar**,
etter å ha sjekket med Storecove — kortet viser **Leverandørens referanse**, Storecoves
ID for overleveringen, til den som har `invoices:issue`, så den kan slås opp. Som den som
har Storecove-kontoen, må du regne med å bli spurt. Avklares den som feilet, har neste
sending nøyaktig den samme EHF-en, så et dokument som likevel kom fram, i verste fall
mottas to ganger, aldri som to forskjellige dokumenter. Kommer Storecoves kvittering
eller hendelse først, avklarer Vantigo sendingen selv, med en merknad om at
leverandøren gjorde det.

**Feilet** betyr at dokumentet ikke ble levert: Storecove avviste det (årsaken, med
Storecoves ord, vises til den som har `invoices:issue`), mottakeren forlot nettverket,
eller det nådde aldri Storecove innen 48 timer. Dokumentet kan sendes på nytt, eller på
e-post.

## Når sendingen svikter

- **Flagget om avvist nøkkel.** Innstillingskortet viser *Aksesspunktet avviste
  nøkkelen*, `GET /api/v1/invoices/meta` svarer `accessPointCredentialsRejected: true`,
  og loggen har en feil. Nøkkelen er tilbakekalt eller feilskrevet, eller `APP_SECRET` er
  endret. Lagre en gyldig nøkkel og klikk **Kontroller**. Lagringen fremskynder ikke
  køen: hvert dokument den avviste nøkkelen holdt igjen, går ut neste gang det står for
  tur, innen en time.
- **Oppslaget.** En sending avvist med 502 `peppol_lookup_failed` betyr at
  Peppol-registeret ikke kunne spørres: sjekk utgående DNS og HTTPS, og
  `PEPPOL_DNS_SERVER`. `peppol_not_receivable` er ingen feil: mottakeren er ikke
  registrert for den dokumenttypen, og dokumentet sendes på e-post.
- **Påloggingsdataene.** Svarer **Kontroller** at aksesspunktet *ikke kunne nås*, peker
  det på nettverket til `INVOICES_STORECOVE_BASE_URL`, eller på en ID for en juridisk
  enhet nøkkelen ikke dekker.
- **503 `ehf_unavailable`.** En av forutsetningene mangler: bryteren, oppslaget,
  påloggingsdataene eller selgerens Peppol-ID. Kortet **E-faktura** viser hvilken linje
  som ikke er klar.
- **Dokumentene blir stående i kø.** Bakgrunnsjobbene kjører ikke: sjekk
  `WORKERS_IN_PROCESS`, eller at `worker`-containeren er oppe.
- **Feilet med valideringsmeldinger fra Storecove.** Storecove avviste dokumentet.
  Årsaken navngir regelen; send dokumentet på e-post i mellomtiden, og meld fra — Vantigos
  egne kontroller burde ha fanget det
  ([E-invoice validation](/en/contributing/e-invoice-validation/)).

## Objektene som skrives

Ved siden av PDF-en, i objektlagerets omfang `invoices`
([Objektlagring](/nb/admin/object-storage/#modulomfang-og-den-fysiske-nøkkelen)):

| Nøkkel | Hva |
| --- | --- |
| `invoices/documents/<id>/<number>-<sha256>.xml` | EHF-en slik Vantigo overleverte den, lagret én gang etter hashen når dokumentet sendes — **Last ned EHF (XML)** leverer den. |
| `invoices/documents/<id>/<number>-<transmission>-receipt.json` | Storecoves bevis på leveringen: mottakerens aksesspunkt, meldings-ID-en og kvitteringen. |
| `invoices/documents/<id>/<number>-<transmission>-delivered.xml` | EHF-en Storecove faktisk leverte, som den bygde på nytt ut fra Vantigos. |

Ingenting her slettes eller overskrives noensinne: som PDF-en er de
bokføringsmateriale som oppbevares fem år etter regnskapsårets slutt, så ta
sikkerhetskopi av dem sammen med resten av lageret.

## KID-avtalen med banken

En KID er referansen på en innbetaling som lar banken, og Vantigos import av bankfiler, koble den til
én faktura. Be banken om en **KID-avtale (OCR giro)** på kontoen i selgeropplysningene;
banken registrerer en **lengde** og en **metode for kontrollsifferet**, og avviser eller
flagger en innbetaling med en KID som ikke passer med dem.

- **Lengden** teller med kontrollsifferet, 4 til 25. Vantigos KID er fakturanummeret
  fylt ut med nuller foran til lengden minus én, så kontrollsifferet, så velg en lengde
  med rom for vekst: sifrene i fakturanumrene, ett for kontrollsifferet og to i reserve.
  Innstillingene avviser en lengde neste nummer ikke får plass i, og advarer når færre
  enn to sifre er igjen.
- **Metoden**, MOD10 eller MOD11. Be om MOD10: med MOD11 får enkelte nummer `-` som
  kontrollsiffer, noe betalere snubler i.
- Én avtale per konto; banken kan holde inntil tre lengder gyldige på den.

Legg inn paret på kortet **KID** i **Fakturainnstillinger**
([brukerveiledningen](/nb/user/invoices/#avtal-kid-med-banken)). Hver faktura som
utstedes fra da av, har en KID på PDF-en, i e-posten og i EHF-en; tidligere fakturaer har
ingen. **Endres avtalen** senere, gjelder det bare nye fakturaer: åpne fakturaer beholder
KID-ene de ble utstedt med, så be banken holde den gamle lengden gyldig til de er betalt
— det er det de ekstra lengdene er til for. En lengde neste nummer ikke får plass i,
avvises når den lagres (400 på `kidLength`); utstedelsen stopper (409
`kid_length_exceeded`) bare når numrene vokser forbi en lengde som passet da den ble
lagret, til avtalen gjøres lengre.

## Test mot Storecoves sandkasse

Med en sandkassenøkkel kan adapteren kjøres mot Storecove selv. Fra `apps/server`:

```bash
STORECOVE_SANDBOX_API_KEY=… STORECOVE_SANDBOX_LEGAL_ENTITY_ID=… \
  mise exec -- go test -tags storecove ./internal/invoices/accesspoint/
```

Den overleverer én faktura og én kreditnota til Storecoves norske testmottaker
(`NO:ORG` `010101018`), tømmer kontoens hendelseskø til utfallene kommer — og kvitterer
for hver hendelse, så kjør den på en konto ingenting annet leser — og sjekker at PDF-en
som er innebygd i den overleverte EHF-en, finnes i kopien Storecove leverte.
`INVOICES_STORECOVE_BASE_URL` overstyrer verten, og `STORECOVE_SANDBOX_SELLER_ORG`
selgerens organisasjonsnummer i testdokumentene (standard `974760673`; hold det lik den
juridiske enhetens eget). Uten de to variablene hoppes testene over
([testen](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/invoices/accesspoint/storecove_sandbox_test.go)).
En installasjon kan på samme måte pekes mot en etterligning av Storecove med
`INVOICES_STORECOVE_BASE_URL`.

## eFaktura og AvtaleGiro gjennom Mastercard Payment Services

eFaktura legger en faktura i nettbanken til en privatkunde, og med AvtaleGiro trekker
banken den på forfallsdatoen. Vantigo sender begge deler direkte gjennom **Mastercard
Payment Services (MPS)**, uten noen mellomledd: Vantigo er **innsender**, og hver
leietaker er **utsteder**, kjent ved organisasjonsnummeret sitt. Funksjonen leveres
**avslått**; ingenting sendes før stegene under er gjort og bryteren er slått på.

### Hva hver part ordner

- **Vantigo, én gang per installasjon:** en innsenderavtale med MPS. MPS utsteder et
  klientsertifikat ut fra Vantigos sertifikatforespørsel, oppretter en
  WS-Security-bruker og hvitelister installasjonens utgående IP-adresse i hvert miljø —
  test og produksjon. Spør Support.Norway@mastercard.com. MPS hvitelister ikke private,
  svært store eller delte skyområder: installasjonen trenger en egen, fast utgående
  adresse.
- **Hver leietaker, med sin egen bank:** en **eFaktura-avtale** og, for AvtaleGiro, en
  **AvtaleGiro-avtale** på kontoen sin, registrert hos MPS med Vantigo som innsender.
  Bankens gebyrer er leietakerens. For AvtaleGiro trenger banken også leietakerens
  KID-avtale (OCR giro).

### Konfigurer installasjonen

Legg innsenderens filer der serveren kan lese dem, og sett:

| Variabel | Betydning |
| --- | --- |
| `INVOICES_EFAKTURA_ENABLED` | Bryteren, standard `0`. Av svarer en sending *eFaktura er ikke tilgjengelig*, og arbeideren `invoices-efaktura` kjører ikke. |
| `INVOICES_MPS_EFAKTURA_BASE_URL` | Endepunktet til Kombo-tjenesten for innsendere, https. Standard er MPS' produksjonsendepunkt. |
| `INVOICES_MPS_MANDATE_BASE_URL` | Grunnadressen til Mandate-API-et for kreditorer, https. Standard `https://payments.mastercard.no/creditor-mandate`. |
| `INVOICES_MPS_CLIENT_CERT_FILE`, `INVOICES_MPS_CLIENT_KEY_FILE` | Klientsertifikatet MPS utstedte og nøkkelen, PEM, absolutte stier. |
| `INVOICES_MPS_USERNAME`, `INVOICES_MPS_PASSWORD_FILE` | WS-Security-brukeren MPS opprettet, og en fil med passordet. |

Med bryteren på nekter serveren å starte uten brukeren og de tre filene, satt, absolutte
og lesbare. Bare stiene er konfigurasjon: nøkkelen og passordet leses fra filene når
Vantigo kaller MPS, skrives aldri ut og lagres aldri i databasen.

### Lagre og kontroller leietakerens innstillinger

Noen med `invoices:manage` åpner **Fakturainnstillinger › eFaktura og AvtaleGiro** og
legger inn tilstanden til avtalene, navnet banken viser, og for AvtaleGiro kortnavnet og
kreditorkontoen ([brukerveiledningen](/nb/user/invoices/#sett-opp-efaktura-og-avtalegiro)).
**Kontroller** spør MPS med innsenderens sertifikat og bruker for denne utstederen — et
oppslag, og med AvtaleGiro en dekningskontroll som ikke leser noen avtale — og svarer
*Kontrollert*, *Avvist* (sertifikatet, brukeren eller IP-adressen) eller *Ikke nådd*. Har
banken gitt leietakeren et eget sertifikat for Mandate-API-et, tar API-et det som
`mandateCertificate` (PEM, sertifikat og nøkkel), forseglet av hemmelighetsboksen under
`invoices/consumer-provider-credential` og aldri vist igjen.

### Slå på AvtaleGiro

**AvtaleGiro** på det samme kortet krever KID-avtalen og en aktiv AvtaleGiro-avtale med
konto. Den reserverer det første sifferet i KID-en til kunde-KID-ene, så den nektes så
lenge neste fakturanummer ikke får plass i lengden minus to sifre, eller når en utstedt
KID allerede begynner med 9 ([KID-en](/nb/user/invoices/#avtalegiro-og-kid)). Avtal en
lengre KID med banken først hvis den nektes.

### Hva arbeideren gjør

`invoices-efaktura` kjører hvert 30. sekund mens bryteren er på:

1. **Sender** hver eFaktura i kø: én forsendelse med én transaksjon og den lagrede
   PDF-en, under forsendelses- og transaksjons-ID-ene som ble lagret før første kall, så
   et nytt forsøk er den samme forsendelsen.
2. **Spør** om det som er sendt, etter 1, 5, 15 og 30 minutter, deretter hver time.
   Godtatt eller behandlet er **I nettbanken**; avvist, stoppet eller ikke funnet er
   **Feilet**; uten utfall etter sju dager spørres en person (**Venter på bekreftelse**).
   AvtaleGiro-delen flytter trekket; det siste svaret lagres som bevis når trekket er
   avgjort.
3. **Stopper** et trekk en kreditnota ba om å stoppe, fram til klokka 14.00 dagen før
   forfall; etter det settes det tilbake som godtatt.
4. **Én gang i døgnet**, under en lease så bare én arbeider gjør det: henter
   AvtaleGiro-avtalene MPS har opprettet, endret eller slettet siden forrige kall, og slår
   opp personene som er forfalt for et nytt oppslag.

Den holder ingen databaseforbindelse mens den kaller MPS. Et avvist sertifikat eller en
avvist bruker setter varselet på kortet, og eFakturaen venter en time.

### Test mot MPS' testmiljø

Pek de to grunnadressene mot MPS' testmiljø, konfigurer testsertifikatet og
testbrukeren, og kjør adaptertesten fra en hvitelistet adresse:

```
go test -tags mps -run TestMpsTestEnv ./internal/consumerinvoice/mps/
```

med variablene den nevner (`MPS_TEST_EFAKTURA_URL`, `MPS_TEST_MANDATE_URL`,
`MPS_TEST_CERT_FILE`, `MPS_TEST_KEY_FILE`, `MPS_TEST_USERNAME`, `MPS_TEST_PASSWORD`,
`MPS_TEST_ISSUER` og, for AvtaleGiro, `MPS_TEST_CREDITOR_ACCOUNT`). Testdataene deles av
alle som bruker testmiljøet, og MPS krever en formell test før produksjon.

### Bryteren for produksjon

Å gå i produksjon er: innsenderavtalen på plass, produksjonssertifikatet og
produksjonsbrukeren konfigurert, den utgående adressen hvitelistet for produksjon, de to
grunnadressene på produksjonsstandardene, og `INVOICES_EFAKTURA_ENABLED=1`. Slås den av
igjen, stopper arbeideren; eFakturaer i kø venter til den er på.

### Kjente begrensninger for eFaktura

- En person finnes uten fødselsnummer, ved telefon, e-post, fødselsdato, navn og adresse.
  Noen personer blir ikke funnet; de får e-post eller papir.
- Nettverket fører ikke kreditnotaer; de går på e-post eller i posten.
- Noen detaljer i MPS' tjeneste står ikke i den offentlige dokumentasjonen —
  XML-navnerommet til tjenesten, om et oppslag svarer i samme rekkefølge som nøklene, og
  hvordan dekningskontrollen oppgir KID-en — og bekreftes i testmiljøet før produksjon.
- Et AvtaleGiro-trekk som avvises etter at eFakturaen er lagret som bevis, følges ikke
  videre; fakturaen følger da de vanlige purringene.

## Kjente begrensninger

- **Den innebygde PDF-en.** Storecove bygger EHF-en den sender på nytt, og dokumentasjonen
  deres sier ikke om PDF-en Vantigo bygger inn, overlever. Sandkassetesten over sjekker
  det; til den har bestått mot Storecove, åpner du den leverte kopien
  (`…-delivered.xml`) av en første virkelig sending og ser etter vedlegget.
- **Peppol BIS Billing 3.0.21.** Vantigos EHF valideres mot Peppol-reglene slik de er
  merket `v3.0.20`; reglene i 3.0.21 tas i bruk den dagen OpenPEPPOL merker dem. Til da
  kan en regel som er ny i 3.0.21, avvise et dokument hos Storecove, og det vises som
  **Feilet** med Storecoves årsak.
- **Hvor lenge Storecove husker en overleveringsnøkkel**, er ikke dokumentert; Vantigo
  gir opp et dokument i kø etter 48 timer for å holde seg godt innenfor et slikt vindu.
- **Støttes ikke:** å motta e-faktura, Peppol Invoice Response, en annen leverandør,
  Storecoves push-webhooks, å kjøre Vantigo som eget aksesspunkt, eFaktura og
  AvtaleGiro, og linjer med mva-kategori K (innen EØS), som sendes på e-post.
