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
**Fakturaer** → **Innbetalinger**, under **Bankkontoer**, **Endre formatet** ved siden av
kontoen (eller `PUT /api/v1/invoices/bank-accounts/{account}/format`). Vantigo registrerer da **overgangsdagen**: den siste bokføringsdagen for
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
- **Den trenger et objektlager.** PDF-en til hvert brev lagres én gang, før det sendes,
  under `reminders/` ([Objektlagring](/nb/admin/object-storage/)); et lager som ikke kan
  skrives til, gir et mislykket forsøk.
- **Et mislykket forsøk** — e-postserveren avviser, lageret svarer ikke, en sending som
  ikke ville rekke å bli ferdig innenfor låsen — prøves igjen etter 2, 4, 8 … sekunder,
  høyst en time imellom, med årsaken i brevets `lastError`. Et brev som fortsatt ikke er
  sendt **48 timer etter første forsøk**, blir **`failed`**: sjekk e-postserveren og
  lageret, og la så noen med `invoices:payments` prøve igjen
  (`POST /api/v1/invoices/reminders/{id}/retry`, til skjermbildet for det kommer) eller
  trekke brevet tilbake. List dem med `GET /api/v1/invoices/reminders?status=failed`.
- **Et brev som venter.** Et brev der inkassosatsene mangler et halvår, eller som ville
  hatt et gebyr etter at regelordningen sist ble gjennomgått, forsøkes ikke: det venter
  en time om gangen, med `heldReason` som forklaring, uten noen gang å feile. Legg inn
  satsen, eller gjennomgå regelordningen, i purreinnstillingene, så går det.
- **Minst én gang.** Stopper prosessen mellom e-postserveren tar imot et brev og Vantigo
  merker det sendt, sender neste forsøk det igjen, med samme Message-ID, så kundens
  e-postprogram kan se at det er det samme brevet.
