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

1. **Velg verten.** Anbefalt: en egen vert, for eksempel `pay.example.com`, med **samme
   basissti** som `APP_URL` — `PUBLIC_BASE_URL=https://pay.example.com` når
   `APP_BASE_PATH` ikke er satt, `https://pay.example.com/vantigo` når den er `/vantigo`.
   På den verten serverer Vantigo bare det betalingssiden trenger, og svarer 404 på alt
   annet. Alternativet er opprinnelsen til `APP_URL` selv: da finnes det ingen egen
   vert og ingenting filtreres, så å publisere betalingssiden betyr å publisere hele
   Vantigo — innloggingen, API-et og dokumentasjonen — på internett.
   Konfigurasjonen avviser ved oppstart en `PUBLIC_BASE_URL` som ikke er `https`, som
   bærer en bruker, en spørring eller et fragment, som har en annen sti enn
   `APP_BASE_PATH`, eller som angir vertsnavnet til `APP_URL` på en annen port
   (vertsfilteret og sperren for den offentlige verten sammenligner vertsnavn uten
   port, så de to kunne ikke skilles fra hverandre).
2. **Rut den til Vantigo med `Host` bevart.** Proxyen sender den offentlige verten videre
   til den samme Vantigo-prosessen som `APP_URL`, og beholder `Host`-hodet:
   `X-Forwarded-Host` respekteres aldri, og betalingssidens forespørsler er fra samme
   opprinnelse bare fordi `Host` er den offentlige verten. Vertsfilteret slipper inn den
   offentlige verten ved siden av vertsnavnet til `APP_URL` og loopback, og ingenting
   annet ([Filtrering av Host-hode](/nb/admin/transport-security/#filtrering-av-host-hode)).
3. **Stol på proxyen.** Sett `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` til proxyen
   foran ([Videresendte headere og HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).
   Betalingssidens operasjoner er anonyme og hastighetsbegrenset etter klientens adresse;
   uten de to innstillingene kommer hver betaler fra proxyens adresse og deler én bøtte,
   så en håndfull kunder som betaler samtidig, blir avvist.
4. **Hold spørringen til `/pay` ute av proxyens tilgangslogg.** Betalingslenken er
   `PUBLIC_BASE_URL/pay?token=…`, og tokenet er det som lar hvem som helst som har det,
   betale fakturaen og se beløpet. Vantigos egen forespørselslogg skriver stien og aldri
   spørringen, og hvert svar har `Referrer-Policy: no-referrer`; betalingssiden leser så
   fakturaen med `POST /api/v1/invoices/pay/read`, med tokenet i innholdet, så det når
   aldri en API-URL. Det som gjenstår, er proxyens tilgangslogg for selve
   sideforespørselen. Med nginx logger du stien uten spørringen for akkurat den
   plasseringen:

   ```nginx
   log_format vantigo_pay '$remote_addr [$time_local] "$request_method $uri $server_protocol" '
                          '$status $body_bytes_sent "$http_user_agent"';

   server {
       server_name pay.example.com;
       # ...listen, ssl_certificate...
       location = /pay {
           access_log /var/log/nginx/vantigo-pay.log vantigo_pay;
           proxy_set_header Host $host;
           proxy_pass http://vantigo:8080;
       }
       location / {
           proxy_set_header Host $host;
           proxy_pass http://vantigo:8080;
       }
   }
   ```

   (`$uri` er stien alene; `$request` ville tatt med spørringen. Under en basissti er
   plasseringen `= /vantigo/pay`.) Med Caddy sletter du tokenet fra den loggede URI-en:

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

5. **Start Vantigo på nytt** med `PUBLIC_BASE_URL` satt; den leses ved oppstart.

**Tillatelseslisten.** På den offentlige verten svarer en forespørsel hvis metode og sti
ikke står på denne listen, 404 — et problemdokument under `/api/`, et tomt innhold ellers
— før noen ruting (`security.PublicHostGate`); den samme forespørselen på verten til
`APP_URL` betjenes som før. Stiene er relative til basisstien.

| Metode | Sti |
| --- | --- |
| `GET`, `HEAD` | `/pay` (betalingssiden; enhver spørring) |
| `GET`, `HEAD` | `/assets/…` (sidens kode), `/favicon.png` |
| `POST` | `/api/v1/invoices/pay/read` (sidens lesing; tokenet i innholdet) |
| `POST` | `/api/v1/invoices/pay/attempts` (en betaling startet) |
| `GET` | `/api/v1/invoices/pay/attempts/{reference}` (statusen dens) |
| `POST` | `/api/v1/invoices/vipps/webhooks` (varslene fra Vipps) |
| `GET` | `/api/v1/identity/system/status` (sidens vedlikeholdssjekk) |
| `GET`, `HEAD` | `/health/…` (prober) |

**Kontroller** utenfra, mot den offentlige verten:

```sh
curl -i https://pay.example.com/sign-in            # 404
curl -i https://pay.example.com/api/v1/identity/me # 404, et problemdokument
curl -i 'https://pay.example.com/pay?token=x'      # 200, betalingssiden
curl -i https://vantigo.example.com/sign-in        # 200 på verten til APP_URL
```

Åpne deretter en betalingslenke og se i proxyens tilgangslogg: linjen for `/pay` har ikke
noe `token`.

**Hva som kan gå galt:**

- **Hver forespørsel til den offentlige verten svarer 400** «The request host is not
  allowed»: `PUBLIC_BASE_URL` er ikke satt eller angir en annen vert enn den proxyen
  sender videre — eller proxyen skriver om `Host`.
- **Betalingssiden er tom eller forespørslene dens svarer 404**: proxyen serverer den
  offentlige verten under en annen sti enn `APP_BASE_PATH`, eller fjerner basisstien selv.
- **Betalere avvises med 429 mens få betaler**: `TRUSTED_PROXY_HOPS` og
  `TRUSTED_PROXY_CIDRS` er ikke satt, så hver betaler deler proxyens bøtte.
- **`/sign-in` svarer 200 på den offentlige verten**: `PUBLIC_BASE_URL` angir verten til
  `APP_URL` selv, så det finnes ingen sperre — eller proxyen sender den offentlige vertens
  forespørsler med `Host` fra `APP_URL`.
