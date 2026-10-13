---
title: Innbetalinger fra banken
description: Bankavtalen som gir deg OCR-giro- eller camt.054-filer med innbetalinger, hvor du laster dem ned, og formatet hver kontos filer importeres i.
sidebar:
  order: 43
sources:
  - apps/server/internal/invoices/bankfile
  - apps/server/internal/invoices/bankimport.go
  - apps/server/internal/invoices/bankaccounts.go
  - apps/server/internal/invoices/reminder_worker.go
  - apps/server/internal/invoices/vipps_settings.go
  - apps/server/internal/invoices/payment_settings.go
  - apps/server/internal/invoices/payment_worker.go
  - apps/server/internal/payments
  - apps/server/internal/security/publichost.go
---

Vantigo leser bankens egen oversikt over pengene som kom inn på selgerens konto: en fil
med innbetalinger, lastet ned fra nettbanken og importert i Fakturaer av noen med
`invoices:payments`. Hver innbetaling i den bærer KID-en som står på fakturaen, og det er
slik en innbetaling finner fakturaen sin: rett etter importen registreres hver innbetaling
som bærer KID-en til en utstedt faktura, betalt til kontoen den fakturaen viste, mot den,
av den som importerte filen; resten holdes tilbake for en person. Denne siden er driftssiden: avtalen du ber
banken om, hvor filene ligger, formatet hver konto importeres i, og hva som kan gå galt.
Reglene for importen står i
[referansen](/en/reference/invoices/#bank-files-and-the-exception-queue).

Vantigo tar to formater:

- **OCR-giro** — 80-tegnspostene fra en OCR/KID-avtale, laget av Mastercard Payment
  Services for alle norske banker. Den bærer innbetalingene som er gjort **med gyldig
  KID**, og ingenting annet.
- **camt.054** — ISO 20022-meldingen om innbetalinger (versjon `.001.02` og `.001.08`),
  under en avtale for alle innbetalinger. Den bærer hver kreditering av kontoen, med eller
  uten KID.

camt.053, kontoutskriften, er ikke nok: med en betalingsavtale viser den samlebeløp, ikke
hvem som betalte hva.

## Bankavtalen

1. **Avtal KID med banken.** Be om en OCR/KID-avtale — «Fakturere med KID» eller en
   «innbetalingsavtale» — på kontoen i **Fakturainnstillinger**. Banken registrerer en
   KID-lengde og en kontrollsiffermetode (MOD10 eller MOD11) hos Mastercard Payment
   Services; legg inn nøyaktig det på kortet **KID** i **Fakturainnstillinger**
   ([KID-avtalen](/nb/user/invoices/#avtal-kid-med-banken)), så hver faktura får en KID
   banken godtar.
2. **Velg filen du skal importere.** OCR-avtalen gir deg OCR-giro-filer med
   KID-innbetalingene. For hver innbetaling — også en kunde som betaler uten KID, eller med
   feil KID — ber du i stedet om en eGiro-/camt.054-avtale for innbetalinger («Innbetaling
   Total» i noen banker), som gir camt.054-filer. Én av de to er nok per konto; å importere
   begge for samme konto ville lese hver KID-innbetaling to ganger.
3. **Vurder tvungen KID** — et eget valg på OCR-avtalen der banken avviser en innbetaling
   uten KID eller med ugyldig KID (unntatt papirgiro). Kundene dine kan da ikke betale uten
   KID, så OCR-filen går ikke glipp av noe en kunde betalte; uten det kommer en innbetaling
   uten gyldig KID inn på kontoen, men **ikke i OCR-filen**, og bare kontoutskriften eller
   en camt.054-fil viser den.

Bankenes priser på disse avtalene varierer; DNB og Nordea tar et etableringsgebyr, et
månedsgebyr og et gebyr per KID-innbetaling.

## Hente filene

Last ned filen for dagene du vil ha fra nettbanken, og importer den i Fakturaer. Vantigo
henter ikke filer fra banken av seg selv i denne versjonen.

- **DNB**: filoverføringen i bedriftsnettbanken tilbyr OCR- og eGiro-filene, og en fil
  kan bestilles på nytt under «Filoverføring – Bestill filer» (mot gebyr).
- **Nordea**: «hente fil» i nettbanken lar deg hente OCR-filen.
- **SpareBank 1**: «Meny → Filer → Hent filer».

Om nettbankene til Nordea og SpareBank 1 tilbyr camt.054 som manuell nedlasting, er
**usikkert**; spør banken. Handelsbanken er ikke undersøkt.

Samme fil kan bare importeres én gang, og det samme gjelder en fil med samme identitet
(OCR-filens avsender, forsendelsesnummer og mottaker; camt.054-filens meldings-id og
opprettelsestid) — den andre avvises med navn på den første importen, hvem som gjorde den
og når. En fil som overlapper en som allerede er importert — to nedlastinger som dekker de
samme dagene, en kopi banken sender på nytt — importeres, og innbetalingene den deler med
den tidligere, beholdes som duplikater av dem og leses aldri to ganger.

## Formatet til hver konto

Hver mottakerkonto importeres i ett format. **Den første filen som importeres for en
konto, bestemmer det**: en OCR-giro-fil gjør kontoen til `ocr`, en camt.054-fil til
`camt054`. En senere fil i det andre formatet for den kontoen avvises med
`bank_import_format_mismatch`, med kontoen og formatet nevnt, og ingenting av den
importeres.

For å bytte format på en konto — for eksempel fra OCR til camt.054 når du tar avtalen for
alle innbetalinger — endrer noen med `invoices:manage` kontoens format: i
**Fakturaer** → **Innbetalinger** (som bare åpnes med `invoices:payments` i tillegg), under
**Bankkontoer**, **Endre formatet** ved siden av kontoen (eller `PUT /api/v1/invoices/bank-accounts/{account}/format`). Vantigo registrerer da **overgangsdagen**: den siste bokføringsdagen for
kontoens innbetalinger lest i det gamle formatet. Avstemmingen holder da tilbake en
innbetaling i det nye formatet som er bokført på eller før overgangsdagen, som et mulig
duplikat, og overlater den til en person, fordi det gamle formatet kan ha tatt den inn allerede. Bytt når den siste filen i
det gamle formatet er importert, og start det nye formatets filer fra dagen etter. Bytte
av bank er en ny konto, med sitt eget format.

**Bankkontoer** på samme side — eller `GET /api/v1/invoices/bank-accounts` — lister hver
konto med formatet, det forrige formatet og overgangsdagen, og den siste filen.

## Før du importerer

- **Kontoene.** Hver konto en fil nevner, må være selgerens **Bankkonto** i
  **Fakturainnstillinger**, eller en som en utstedt faktura har skrevet ut (så
  innbetalinger til en konto selgeren hadde før, fortsatt leses); en norsk IBAN leses som
  kontonummeret sitt. Ellers avvises filen med `bank_account_unknown`, med kontoens fire
  siste sifre nevnt — sjekk innstillingene, eller at filen er dette selskapets.
- **Et objektlager** ([Objektlagring](/nb/admin/object-storage/)). Hver importert fil
  oppbevares slik den ble lastet opp, under `bank-files/<sha256>.ocr` eller `.xml`, fordi
  den er dokumentasjonen for innbetalingene som bokføres fra den (bokføringsloven § 10).
  Uten et lager avvises en import med `storage_unavailable`, og ingenting leses.

## Hva som kan gå galt

- **400 på filen.** Den er ikke en OCR-giro- eller camt.054-fil, den er større enn 10 MiB
  eller har mer enn 5 000 innbetalinger, eller den bryter sine egne regler — en kontrollsum
  som ikke stemmer, en bokføringsdag etter i dag, et beløp som ikke er i NOK. Meldingen
  nevner posten eller elementet. Ingenting importeres: last ned filen på nytt, og ikke
  rediger den for hånd.
- **`bank_account_unknown`**, **`bank_file_duplicate`**, **`bank_import_format_mismatch`**:
  over.
- **Det OCR utelater.** En OCR-giro-fil har bare innbetalinger med gyldig KID; en kunde som
  betalte uten, eller med en KID banken avviste, er ikke med. Uten tvungen KID ser du etter
  slike innbetalinger på kontoutskriften, eller importerer camt.054 i stedet.
  Kortbetalinger (OCR-filens kortinformasjon) telles og hoppes over.
- **Innbetalinger som står igjen.** Avstemmingen kjører rett etter importen, én
  innbetaling om gangen; stopper den tidlig — en databasefeil, eller at opplastingens
  forespørsel avsluttes — står filen likevel importert, importens svar teller resten som
  `pending`, og stoppet logges som en advarsel med filen og innbetalingen. Noen med
  `invoices:payments` avstemmer resten med **Avstem resten** på filen under
  **Innbetalinger** (eller `POST /api/v1/invoices/bank-files/{id}/match`), og er da den som
  registrerer dem.
- **Et negativt oppdrag.** Et OCR-oppdrag der innbetalingene summerer seg under null — en
  tilbakeføring større enn dagens innbetalinger — kan ikke skrives i formatets sluttpost;
  om Mastercard Payment Services i det hele tatt kan sende et slikt, er **usikkert**. En
  negativ linje i et oppdrag leses og holdes til side for en person.
- **Innbetalinger som holdes tilbake for en person.** Det avstemmingen ikke kunne
  plassere, venter i avvikskøen, nederst på **Innbetalinger** (eller
  `GET /api/v1/invoices/bank-transactions?status=exception`), hver med sin årsak og, der den mangler en KID-faktura, forslag. Noen med
  `invoices:payments` fører den mot fakturaer, avviser den med et notat, bekrefter et
  duplikat eller beholder den som en egen betaling
  ([avvikskøen](/en/reference/invoices/#the-exception-queue)). En **tilbakeføring** — at
  banken tar en innbetaling tilbake — rettes aldri opp av seg selv: håndter den ved å
  oppgi betalingen den gjelder, som da fjernes, eller ved å skrive hvorfor ingen gjør det.
  Etter at en kontos format er byttet, bekreftes innbetalingene som skjæringsdagen holdt
  tilbake, som duplikater der, eller føres når de likevel ikke var med i det gamle
  formatets filer.

## Purrejobben og e-post

Purrebrev til kunder som får dem på e-post, sendes av bakgrunnsjobben
`invoices-reminders` ([jobben](/en/reference/invoices/#the-worker)). Den kjører på hver
installasjon, i API-prosessen eller i den dedikerte worker-containeren
([Bakgrunnsjobber og skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)),
ett brev om gangen og høyst ett i sekundet per instans; flere instanser deler brevene
under en lås på 60 sekunder og sender aldri et brev to ganger med vilje.

- **Den trenger SMTP-innstillingene.** Brevene går gjennom de samme innstillingene
  `MAIL_DRIVER=smtp` og `SMTP_*` som alt annet Vantigo sender på e-post, fra `SMTP_FROM`
  under selgerens navn, med svar til e-postadressen i **Fakturainnstillinger**. Uten
  e-post (`MAIL_DRIVER` er ikke `smtp`) gjør en purrekjøring **hvert brev til et
  papirbrev**, med varselet `mail_unavailable`, og ingenting sendes på e-post.
- **Papirbrev trenger verken jobben eller e-post.** En person skriver dem ut i en bunke
  for dagen de skal postlegges, og bekrefter postleggingen; bunken lagrer PDF-en til hvert
  brev i det samme objektlageret, på samme måte, og en PDF den ikke kunne lagre da,
  lagres neste gang bunken bekreftes postlagt eller lastes ned
  ([papir og postlegging](/en/reference/invoices/#paper-and-posting)).
- **Den trenger et objektlager.** PDF-en til hvert brev lagres én gang, før det sendes,
  under `reminders/` ([Objektlagring](/nb/admin/object-storage/)); et lager som ikke kan
  skrives til, gir et mislykket forsøk.
- **Et mislykket forsøk** — e-postserveren avviser, lageret svarer ikke, en sending som
  ikke ville rekke å bli ferdig innenfor låsen — prøves igjen etter 2, 4, 8 … sekunder,
  høyst en time imellom, med årsaken i brevets `lastError`. Et brev som fortsatt ikke er
  sendt **48 timer etter første forsøk**, blir **`failed`**: sjekk e-postserveren og
  lageret, og la så noen med `invoices:payments` trykke **Send på nytt** på kortet
  **Purringer** på fakturaen eller på kjøringens side, eller trekke brevet tilbake. Hvert
  brev som feilet, er et punkt i listen **Trenger oppfølging** på dashbordet;
  `GET /api/v1/invoices/reminders?status=failed` lister dem.
- **Et brev som venter.** Et brev der satsene mangler et halvår, eller som ville
  hatt et gebyr etter at regelordningen sist ble gjennomgått, forsøkes ikke: det venter
  en time om gangen, med `heldReason` som forklaring, uten noen gang å feile. Legg inn
  satsen, eller gjennomgå regelordningen, i purreinnstillingene, så går det.
- **Minst én gang.** Stopper prosessen mellom e-postserveren tar imot et brev og Vantigo
  merker det sendt, sender neste forsøk det igjen, med samme Message-ID, så kundens
  e-postprogram kan se at det er det samme brevet. Henger prosessen i stedet lenger enn
  brevets lås på 60 sekunder i det tidsrommet, kan noen trekke brevet tilbake imens: det
  ble sendt, men står som `withdrawn`, og jobben logger en advarsel som navngir det.

## Vipps

Fakturaer tar imot Vipps MobilePay-betalinger gjennom
[betalingsporten](/en/contributing/platform-packages/#the-payments-port):
betalingslenker på utstedte fakturaer, og betaling på stedet. Dette er driftssiden av
det; reglene står i [referansen](/en/reference/invoices/#credentials-and-payment-settings).

**Forutsetninger:**

- Firmaet har en avtale med Vipps MobilePay om integrert betaling (*Integrert betaling*,
  ePayment-API-et) og en **salgsenhet** for den i Vipps' bedriftsportal.
  Én salgsenhet betjener hele installasjonen.
- Salgsenhetens API-nøkler, fra portalens side for API-nøkler under *For developers*: klient-ID,
  klienthemmelighet og abonnementsnøkkel, sammen med salgsenhetens nummer (MSN).
- For betalingslenker en offentlig vert for betalingssiden
  ([Den offentlige verten](#den-offentlige-verten)). Betaling på stedet trenger ingen.

**Fremgangsmåte:**

1. La `INVOICES_VIPPS_ENABLED` stå på standardverdien, på. `0` slår av Vipps på denne
   installasjonen: ingen forespørsel sendes noen gang til Vipps, nøklene kan ikke
   bekreftes, og ingenting Vipps tilbyr er tilgjengelig — mens de lagrede nøklene og
   innstillingene beholdes.
2. La `INVOICES_VIPPS_BASE_URL` stå på standardverdien, `https://api.vipps.no`, for
   ekte betalinger. For å prøve hele flyten først, pek den på Vipps' testmiljø,
   `https://apitest.vipps.no`, med nøklene til en **test**-salgsenhet — aldri
   produksjonsnøkler mot testmiljøet eller testnøkler mot produksjon; en salgsenhets
   nøkler virker bare i sitt eget miljø. Start på nytt etter å ha endret en av dem.
3. En leder (`invoices:manage`) fyller inn MSN og de tre nøklene i innstillingene for
   Fakturaer og bekrefter dem. Nøklene forsegles av hemmelighetsboksen under
   `invoices/payment-provider-credential`; de avhenger av `APP_SECRET`, som alle
   forseglede hemmeligheter.
4. En leder setter betalingsinnstillingene: betalingslenker (med URL-ene til selgerens
   salgsvilkår og personvernerklæring) og betaling på stedet.

**Kontroll:** bekreftelsen svarer *ok*. Den henter et tilgangstoken med nøklene og leser
salgsenhetens webhook-registreringer, noe som krever nøklene og MSN sammen.

**Rotere en nøkkel.** En ny klienthemmelighet for samme salgsenhet kan lagres når som
helst; fyll bare inn hemmeligheten, så beholdes de to andre nøklene. En endring av MSN,
klient-ID eller abonnementsnøkkel — en annen salgsenhet — avvises mens en
Vipps-betaling pågår, fordi den må fullføres med nøklene den ble laget med: vent til
betalingene som pågår er ferdige. Å fjerne nøklene avvises av samme grunn.

**Å lagre nøklene på nytt setter det som var på, i drift igjen.** Så lenge Vipps avviser
nøklene, eller de ikke kan åpnes, er ingenting Vipps tilbyr tilgjengelig, men
betalingsinnstillingene blir stående som de var. Å lagre gyldige nøkler på nytt — eller
en bekreftelse som lykkes — setter betalingslenker og betaling på stedet som fortsatt er
slått på, i drift igjen med en gang, uten at noen blir bedt om å slå dem på igjen;
betaling på stedet beholder bekreftelsen av kontantsalg som ble gitt da den først ble
slått på. Slå en innstilling av først hvis den ikke skal komme tilbake.

**Betaling på stedet er kontantsalg.** En betaling fra en kunde som står ved disken, er
kontantsalg, og fakturaen er da en kontantfaktura med reglene som følger med — kjøperen
identifisert på den, og salget i dagens kontantoppgjør. Når den slås på, må lederen
bekrefte merknaden om dette, og hvem som gjorde det og når, registreres. La regnskapsfører
eller revisor lese merknaden før betaling på stedet slås på i produksjon.

**Hva som kan gå galt:**

- *Avvist* ved bekreftelse: Vipps godtar ikke nøklene eller MSN — en feilskrevet nøkkel,
  en nøkkel fra det andre miljøet, eller en salgsenhet uten ePayment-produktet. Fyll inn
  nøklene på nytt. Så lenge Vipps avviser dem, rapporterer meta det, og ingenting Vipps
  tilbyr er tilgjengelig.
- *Utilgjengelig* ved bekreftelse: nettverket, et tidsavbrudd, et Vipps-avbrudd eller en
  struping. Kontroller at serveren når `INVOICES_VIPPS_BASE_URL` over https, og prøv
  igjen.
- Nøklene kan ikke åpnes: `APP_SECRET` er endret siden de ble lagret. Fakturaer logger
  det som feil, markerer nøklene som avvist og svarer *betalinger utilgjengelig*; fyll
  inn alle tre nøklene på nytt.

## Betalingsjobben

Jobben `invoices-payments` følger hver Vipps-betaling fra betaleren sendes til Vipps til
den er ferdig: den leser betalingen til betaleren godkjenner eller Vipps avslutter den,
trekker det som er åpent på fakturaen, registrerer betalingen, og frigir det som ble
reservert og ikke trukket. Den kjører når `INVOICES_VIPPS_ENABLED` er på, i API-prosessen
eller i worker-containeren ([Bakgrunnsjobber og
skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)), ser etter arbeid hvert
sekund og behandler alle betalinger som er klare, i hver syklus. Reglene står i
[referansen](/en/reference/invoices/#capture-and-the-reservation).

**Webhooks støttes ikke ennå.** Vantigo følger hver Vipps-betaling ved å lese den
(polling); den registrerer ingen webhook hos Vipps, og ingenting fra Vipps trenger å nå
den. Den offentlige vertens tillatelsesliste har ingen sti for webhooks: en slik sti
legges til sammen med selve webhooken, aldri før.

**Mens et trekk fullføres, er beløpet reservert** på fakturaen: en manuell betaling, en
bankfil eller en purring kan ikke ta det, og en avvisning sier at en Vipps-betaling
fullføres. Med `INVOICES_VIPPS_ENABLED=0` kjører ikke jobben og ingen betaling kommer
videre, men reservasjonene står, hver vist på dashbordet som en strandet reservasjon —
slå Vipps på igjen, eller la en leder forlate hver betaling der trekket aldri ble sendt,
etter å ha sjekket den i portalen. En betaling der trekket ble sendt, kan forlates først
når Vipps kan leses igjen.

**Å forlate en betaling** (*Forlat* på fakturaens Vipps-betaling, `invoices:manage`)
krever en begrunnelse og en bekreftelse på at portalen ikke viser noe trukket. Vantigo
tar ikke bekreftelsen for god fisk: der den kan lese Vipps, leser den betalingen først.
Penger Vipps viser som trukket, registreres som en betaling av fakturaen, og betalingen
forlates ikke; en betaling Vipps fortsatt kan trekke, avvises til den er frigitt i
portalen; en betalingsforespørsel Vipps kjenner og ikke har avsluttet, gis tilbake til
jobben. Først da forlates forsøket: det reserverte beløpet frigis, og hvem, når og
hvorfor lagres. Svarer Vipps for sent, eller ga en annen leder betalingen tilbake til
jobben i mellomtiden, kan jobben ha tatt den over: da forlates den ikke — les betalingen
på nytt. Et trekk som oppdages senere, registreres for hånd. Reglene står i
[referansen](/en/reference/invoices/#abandon).

**Kontroller:** en testbetaling av en liten faktura, godkjent i Vipps-appen (eller
testmiljøets), registreres på fakturaen i løpet av sekunder som en Vipps-betaling.

**Hva som kan gå galt:**

- *Et trekk med ukjent utfall* (`capture_unknown`). Vipps har i sju dager ikke svart på
  trekket slik at det går an å si om pengene ble tatt — et avbrudd, avviste nøkler, en
  struping. Beløpet forblir reservert, og jobben leser betalingen hver time i opptil 180
  dager. Slå opp betalingen i Vipps-portalen på referansen
  (`<prefiks>-<fakturanummer>-<forsøks-id>`, vist på fakturaen): er den trukket der,
  registreres den ved neste lesing når Vipps svarer igjen; er den ikke trukket, frigis
  reservasjonen i portalen, og en leder forlater forsøket, slik at reservasjonen frigis
  også her. Sjekk nøklene først — en avvist nøkkel (*Avvist* ved bekreftelse) gir ukjent
  utfall på hvert trekk. Den vises på dashbordet som et trekk med ukjent utfall.
- *Et mislykket trekk* (`capture_failed`). Vipps avviste trekket for manglende dekning
  eller som mislykket, og fortsatte å avvise det til reservasjonen gikk ut, eller Vipps
  avsluttet betalingen. Ingenting ble tatt; fakturaen er åpen igjen, og kunden betaler
  på annen måte. Viser pengene seg likevel trukket hos Vipps — eller trekkes betalingen
  i portalen — registrerer jobben betalingen så snart den leser den.
- *En opprettelse som aldri avklares.* Når opprettelsen av en betaling aldri ble
  besvart og det å sende den på nytt fortsetter å gi konflikt, gir jobben opp etter ti
  forsøk, og betalingens siste feil sier at referansen må slås opp i Vipps-portalen. Den
  vises på dashbordet som strandet, og på stedet holder den fakturaens ene levende
  betalingsforespørsel, så ingen ny kan lages: en leder forlater den. Vantigo leser Vipps
  først — en betaling Vipps ikke kjenner, eller har avsluttet, forlates; en den kjenner
  og ikke har avsluttet, gis tilbake til jobben, som fullfører den og registrerer det som
  er trukket.
- *Et trekk som ikke kan registreres.* Pengene er trukket, men betalingen kunne ikke
  skrives; forsøket beholder reservasjonen, den siste feilen sier hvorfor, og jobben
  prøver igjen. Den vises på dashbordet som et trekk med ukjent utfall til den er
  registrert.
- *En frigivelse som aldri blir ferdig.* Når Vipps for godt nekter å frigi et beløp som
  ble reservert og ikke trukket, slutter jobben å spørre: betalingens siste feil sier at
  det må frigis i Vipps-portalen. En slik betaling teller fortsatt som pågående, så
  salgsenheten og nøklene kan ikke endres før den er ferdig, og den vises på dashbordet
  som strandet. Når resten er frigitt i portalen, gir *Forlat* den tilbake til jobben —
  selve betalingen er registrert og forlates aldri — som leser Vipps igjen og lagrer
  frigivelsen.
- *Salgsenheten ble endret.* En betaling laget under en annen salgsenhet enn den som er
  lagret, sendes aldri til den nye; den venter, med beløpet reservert og vist på
  dashbordet som strandet, til en leder forlater den — bare så lenge trekket aldri ble
  sendt, siden Vantigo ikke kan lese den under den nye salgsenheten.
- *En refusjon gjort i Vipps-portalen.* I 30 dager etter et trekk leser jobben
  betalingen én gang i døgnet; en refusjon gjort i portalen lagres og vises på
  dashbordet til noen med `invoices:payments` fjerner betalingen (og registrerer den
  delen som ble beholdt, om noen). Vantigo fjerner aldri en betaling på egen hånd, og en
  lesing som feiler, endrer ingenting.

## Den offentlige verten

En kunde betaler en faktura med Vipps MobilePay fra en **betalingsside**: lenken som er
skrevet ut på fakturaen åpner den, og siden oppretter betalingen først når kunden trykker
på knappen, fordi en betalingsforespørsel i Vipps lever i ti minutter og betalingsfristen
på en faktura er uker. Kunden er på internett, ikke logget inn i Vantigo, så
betalingssiden må kunne nås fra internett — mens resten av Vantigo, innloggingen
medregnet, ikke trenger det. `PUBLIC_BASE_URL` angir hvor betalingssiden kan nås; uten
den (standard) finnes det ingen betalingslenker. QR-betaling på stedet trenger ingen
offentlig vert: nettbrettet som viser QR-koden, er logget inn på `APP_URL`.

**Forutsetninger:** en omvendt proxy som terminerer TLS foran Vantigo
([Transportsikkerhet](/nb/admin/transport-security/)), og et vertsnavn for
betalingssiden med sertifikat.

**Sperren styres av `Host`.** Vantigo kan ikke se hvilken lytter eller hvilket navn en
forespørsel kom inn på, bare `Host`-hodet. På den offentlige verten — en forespørsel der
`Host`, uten port, er vertsnavnet i `PUBLIC_BASE_URL` — serveres bare
tillatelseslisten nedenfor. En forespørsel med en annen `Host` som vertsfilteret slipper
inn — vertsnavnet til `APP_URL`, eller loopback (`localhost`, `127.0.0.1`, `::1`), som
fortsatt slippes inn for containerens egen helsesjekk — får alt. Derfor **må proxyen som
publiserer den offentlige verten aldri sende videre noen annen `Host` enn den offentlige
verten**: sendte den videre klientens egen `Host`, kunne hvem som helst på internett
sende `Host: localhost` eller `Host: vantigo.example.com` til betalingssidens adresse og
nå hele Vantigo, innloggingen medregnet. Den proxyregelen er det som gjør den offentlige
verten trygg; stegene nedenfor setter den.

1. **Velg verten.** Anbefalt: en egen vert, for eksempel `pay.example.com`, med **samme
   basissti** som `APP_URL` — `PUBLIC_BASE_URL=https://pay.example.com` når
   `APP_BASE_PATH` ikke er satt, `https://pay.example.com/vantigo` når den er `/vantigo`.
   På den verten serverer Vantigo bare det betalingssiden trenger, og svarer 404 på alt
   annet. Alternativet er opprinnelsen til `APP_URL` selv: da finnes det ingen egen
   vert og ingenting filtreres, så å publisere betalingssiden betyr å publisere hele
   Vantigo — innloggingen, API-et og dokumentasjonen — på internett.
   Konfigurasjonen avviser ved oppstart en `PUBLIC_BASE_URL` som ikke er `https`; som
   bærer en bruker, en spørring eller et fragment; som har en annen sti enn
   `APP_BASE_PATH`; der vertsnavnet slutter på punktum eller ikke er ASCII (skriv et
   internasjonalisert navn i punycode-form, `xn--…`); eller som angir vertsnavnet til
   `APP_URL` på en annen port (vertsfilteret og sperren sammenligner vertsnavn uten
   port, så de to kunne ikke skilles fra hverandre).
2. **Publiser den med en fast `Host`.** Proxyen sender den offentlige vertens
   forespørsler til den samme Vantigo-prosessen som `APP_URL`, med **`Host` satt til
   selve det offentlige vertsnavnet** — en konstant, aldri klientens hode (i nginx
   `proxy_set_header Host pay.example.com;`, aldri `$host` eller `$http_host`).
   `X-Forwarded-Host` respekteres aldri, og betalingssidens forespørsler er fra samme
   opprinnelse bare fordi `Host` er den offentlige verten. Gi proxyen en oppsamler for
   alle andre navn, så en forespørsel som ikke angir noen av vertene, stoppes der i
   stedet for å sendes videre. Vertsfilteret slipper inn den offentlige verten ved siden
   av vertsnavnet til `APP_URL` og loopback, og ingenting annet
   ([Filtrering av Host-hode](/nb/admin/transport-security/#filtrering-av-host-hode)).
3. **Stol på proxyen.** Sett `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` til proxyen
   foran ([Videresendte headere og HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).
   Betalingssidens operasjoner er anonyme og hastighetsbegrenset etter klientens adresse;
   uten de to innstillingene kommer hver betaler fra proxyens adresse og deler én bøtte,
   så en håndfull kunder som betaler samtidig, blir avvist.
4. **Hold tokenet i betalingslenken ute av proxyens logger.** Betalingslenken er
   `PUBLIC_BASE_URL/pay?token=…`, og tokenet er det som lar hvem som helst som har det,
   betale fakturaen og se beløpet. Vantigos egen forespørselslogg skriver stien og aldri
   spørringen, og hvert svar har `Referrer-Policy: no-referrer`; betalingssiden leser så
   fakturaen med `POST /api/v1/invoices/pay/read`, med tokenet i innholdet, så det når
   aldri en API-URL. Det som gjenstår, er proxyens egen logging av sideforespørselen.
   Ingenting på den offentlige verten trenger spørringen sin logget, så logg bare stien
   for hele verten.

   Med nginx:

   ```nginx
   log_format vantigo_pay '$remote_addr [$time_local] "$request_method $uri $server_protocol" '
                          '$status $body_bytes_sent "$http_user_agent"';

   # Alle andre navn enn de to publiserte vertene: lukkes uten svar.
   server {
       listen 443 ssl default_server;
       server_name _;
       ssl_reject_handshake on;
       return 444;
   }
   server {
       listen 80 default_server;
       server_name _;
       return 444;
   }

   server {
       listen 443 ssl;
       server_name pay.example.com;
       # ...ssl_certificate...
       access_log /var/log/nginx/vantigo-pay.log vantigo_pay;
       error_log  /var/log/nginx/vantigo-pay.error.log crit;
       location / {
           proxy_set_header Host pay.example.com;   # en konstant, aldri $host
           proxy_pass http://vantigo:8080;
       }
   }
   ```

   `$uri` er stien alene; `$request` ville tatt med spørringen. nginx' **feillogg**
   skriver også forespørselslinjen, med spørringen, når en oppstrøms feiler
   ("connect() failed … request: "GET /pay?token=…""), på nivået `error`: `crit` ovenfor
   holder de linjene ute, på bekostning av selve oppstrømsfeilene på denne verten — les
   dem i feilloggen til verten for `APP_URL`, eller behold `error` og behandle den
   loggen som en som inneholder betalingstokener (begrens hvem som leser den, hold den
   kort). `ssl_reject_handshake` krever nginx 1.19.4 eller nyere; på en eldre gir du
   oppsamleren et eget sertifikat i stedet. Oppsamleren på port 80 betyr noe når proxyen
   også lytter på 80.

   **Nettstedet til `APP_URL` må ikke kunne nås på den offentlige adressen.** Lytter
   `server`-blokken dens på samme adresse og port i den samme nginx, kan en klient åpne
   TLS for `pay.example.com` og sende `Host: vantigo.example.com`: nginx velger
   `server`-blokk etter `Host`, så forespørselen når blokken til `APP_URL` og hele
   Vantigo gjennom betalingssidens adresse — oppsamleren hjelper ikke. La blokken til
   `APP_URL` lytte bare på en intern adresse (`listen 10.0.0.5:443 ssl;`), eller begrens
   den til egne nett med `allow` og `deny`, eller publiser den fra en annen proxy.

   Med Caddy svarer en nettstedsblokk bare for navnene den lister opp — et annet navn
   finner ikke noe nettsted og avvises — og `reverse_proxy` sender oppstrøms
   nettstedets egen vert, så ingenting mer trengs for `Host`; slett tokenet fra den
   loggede URI-en:

   ```text
   pay.example.com {
       log {
           format filter {
               request>uri query {
                   delete token
               }
           }
       }
       reverse_proxy vantigo:8080
   }
   ```

   Det samme gjelder nettstedet til `APP_URL` i den samme Caddy: Caddy ruter etter
   `Host`, så en klient som åpner TLS for `pay.example.com` med
   `Host: vantigo.example.com`, når det. Begrens det nettstedet til egne nett med en
   `remote_ip`-matcher, eller slå på `strict_sni_host` i den globale innstillingen
   `servers`, som svarer `421 Misdirected Request` når `Host` ikke stemmer med
   TLS-servernavnet. Direktivet `log` setter bare opp tilgangsloggen; dokumentasjonen til
   Caddy sier ikke at en oppstrømsfeil holdes ute av kjøretidsloggen (loggeren `default`,
   på stderr), så behandle også den loggen som en som inneholder betalingstokener: hev
   nivået, eller begrens hvem som leser den, og hold den kort.
5. **Start Vantigo på nytt** med `PUBLIC_BASE_URL` satt; den leses ved oppstart.

**Tillatelseslisten.** På den offentlige verten svarer en forespørsel hvis metode og sti
ikke står på denne listen, 404 — et problemdokument under `/api/`, et tomt innhold ellers
— før noen ruting (`security.PublicHostGate`); den samme forespørselen på verten til
`APP_URL` betjenes som før. Stiene er relative til basisstien. En sti som ikke allerede er
ren (`..`, `.`, et tomt segment), eller som er kodet på annen måte enn den kanoniske (en
kodet `/`, `%2F`), står ikke på noen liste.

| Metode | Sti |
| --- | --- |
| `GET`, `HEAD` | `/pay` (betalingssiden; enhver spørring) |
| `GET`, `HEAD` | `/assets/…` (sidens kode), `/favicon.png` |
| `POST` | `/api/v1/invoices/pay/read` (sidens lesing; tokenet i innholdet) |
| `POST` | `/api/v1/invoices/pay/attempts` (en betaling startet) |
| `GET` | `/api/v1/invoices/pay/attempts/{reference}` (statusen dens) |
| `GET` | `/api/v1/identity/system/status` (sidens vedlikeholdssjekk) |
| `GET`, `HEAD` | `/health/…` (prober) |

**Kontroller** utenfra, mot den offentlige verten:

```sh
curl -i https://pay.example.com/sign-in            # 404
curl -i https://pay.example.com/api/v1/identity/me # 404, et problemdokument
curl -i 'https://pay.example.com/pay?token=x'      # 200, betalingssiden
curl -i https://vantigo.example.com/sign-in        # 200 på verten til APP_URL
# Proxyen må ikke sende videre en annen Host: ingen av disse kan svare 200.
curl -i https://pay.example.com/sign-in -H 'Host: localhost'
curl -i https://pay.example.com/sign-in -H 'Host: vantigo.example.com'
```

De to siste må svare 404 (proxyen sendte den offentlige verten likevel) eller avvises av
proxyen — aldri 200 og innloggingssiden. Kontrollen med `Host: vantigo.example.com`
beviser bare noe når nettstedet til `APP_URL` betjenes av den samme proxyen; der er den
nettopp forespørselen som ville nådd nettstedet til `APP_URL` gjennom den offentlige
adressen. Åpne deretter en betalingslenke og se i proxyens
tilgangslogg: linjen for `/pay` har ikke noe `token`. I nettleseren viser betalingssiden
fakturaen uten innlogging, adresselinjen viser ikke lenger `token`, og nettverkslisten
i utviklerverktøyene har bare forespørsler fra tabellen over — ingen
`/api/v1/identity/session`.

**Hva som kan gå galt:**

- **`/sign-in` svarer 200 med `Host: localhost` eller verten til `APP_URL`** sendt til
  den offentlige adressen: proxyen sender videre klientens `Host` (`$host`,
  `$http_host`). Sett `Host` til det offentlige vertsnavnet som en konstant. Vantigo selv
  avviser ikke loopback på den offentlige lytteren — den kan ikke skille lyttere fra
  hverandre, og loopback må fortsatt slippes inn for containerens helsesjekk, som prober
  `127.0.0.1` innenfra — så denne proxyregelen er det eneste som holder resten av
  Vantigo borte fra den offentlige adressen.
- **Hver forespørsel til den offentlige verten svarer 400** «The request host is not
  allowed»: `PUBLIC_BASE_URL` er ikke satt eller angir en annen vert enn den proxyen
  sender.
- **Betalingssiden er tom eller forespørslene dens svarer 404**: proxyen serverer den
  offentlige verten under en annen sti enn `APP_BASE_PATH`, eller fjerner basisstien selv.
- **Betalere avvises med 429 mens få betaler**: `TRUSTED_PROXY_HOPS` og
  `TRUSTED_PROXY_CIDRS` er ikke satt, så hver betaler deler proxyens bøtte.
- **`/sign-in` svarer 200 på den offentlige verten** uten noe `Host`-triks i det hele
  tatt: `PUBLIC_BASE_URL` angir verten til `APP_URL` selv, så det finnes ingen sperre —
  eller proxyen sender den offentlige vertens forespørsler med `Host` fra `APP_URL`.
