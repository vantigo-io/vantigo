---
title: Kalender
description: Slå på Kalender og gi folk de to rettighetene; hva invitasjoner til folk utenfor bedriften trenger — SMTP-driveren, et avsenderdomene med SPF, DKIM og DMARC, den offentlige verten og rutene dens; kalenderstrømmen på den offentlige verten, grensen for den og proxyens logg; avlysnings-, abonnements- og postbokssynkroniseringsarbeideren og innstillingene deres, og hva Vantigo henter fra internett; hvordan du sjekker at en invitasjon gikk og at strømmen svarer, og hva som kan gå galt.
sidebar:
  order: 45
sources:
  - apps/server/internal/calendar/invitations.go
  - apps/server/internal/calendar/rsvp.go
  - apps/server/internal/calendar/cancellations_worker.go
  - apps/server/internal/calendar/module.go
  - apps/server/internal/calendar/feed.go
  - apps/server/internal/calendar/feed_tokens.go
  - apps/server/internal/calendar/subscriptions_worker.go
  - apps/server/internal/calendar/subscriptions_fetch.go
  - apps/server/internal/calendar/mailbox_worker.go
---

Kalender er én oversikt over dagene til hver person: avtalene de lager, med kolleger,
kunders kontaktpersoner og andre på, ved siden av det de andre modulene vet om dem.
Denne siden er driftssiden: å slå modulen på, hva invitasjonene til folk utenfor
bedriften trenger, arbeideren som rydder opp etter en avlysning, kalenderstrømmen en
mobil abonnerer på, arbeideren som henter kalenderne folk abonnerer på fra andre
steder, og arbeideren som holder kopiene deres i Outlook. Hva modulen gjør og
reglene den sjekker, står i [referansen](/en/reference/calendar/); hva en person gjør på
skjermbildene, står i [brukerveiledningen](/nb/user/calendar/).

## Før du begynner

- Kalender trenger ingen annen forretningsmodul. Er `MODULES` ikke satt, er den på
  sammen med alle de andre modulene; en installasjon som lister `MODULES`, legger
  `calendar` til i listen, alene om den vil (`MODULES=calendar`). Med Kunder også på kan
  en avtale lages fra en kunde og kontaktpersonene inviteres.
- Migreringene lager `calendar`-skjemaet enten modulen er på eller ikke, så å slå den på
  senere trenger ingen migrering. En applikasjonsrolle med minst mulig rettigheter
  trenger `calendar`-skjemaet tildelt som alle de andre
  ([Installasjon](/nb/admin/installation/)).
- Kalender har ingen egne innstillinger for arbeidsområdet: kalenderadressene og
  abonnementene til hver person er deres egne, på skjermbildet **Min kalender**. De to
  rettighetene er `calendar:access`, for å
  bruke den, og `calendar:view-colleagues`, for å se når kolleger er opptatt og ledige,
  som et arbeidsområde som vil at alle skal se hverandres, gir til ansattrollen
  ([Permissions](/en/reference/calendar/#permissions)).

## Slå på modulen

1. Legg `calendar` til i `MODULES` i `vantigo.env`, eller la `MODULES` være usatt.
2. Start Vantigo på nytt (`docker compose up -d`).
3. Kontroller: `GET /api/v1/calendar/meta` svarer 401 uten en økt, og ikke 404, som en
   avslått moduls sti svarer.

En avslått modul bidrar ikke med noen rute, rettighet eller arbeider; dataene den har,
blir liggende urørt i databasen.

## Invitasjoner til folk utenfor bedriften

En avtale kan ha folk på seg som ikke er brukere: en kundes kontaktperson, eller en
adresse som er skrevet inn. Hver av dem får en invitasjon på e-post, én melding per
person, i oppsettet Gmail, Outlook og Apple Mail viser sine egne **Ja / Kanskje / Nei**-
knapper for, med en lenke å svare med som lander svaret i Vantigo
([invitations](/en/reference/calendar/#invitations)). Kolleger på en avtale får i stedet
en kort melding gjennom identitetsmodulen. Begge går gjennom installasjonens egen
e-postkonfigurasjon; ingenting her har en egen innstilling.

### Forutsetninger

1. **SMTP-driveren.** `MAIL_DRIVER=smtp` med `SMTP_HOST`, `SMTP_FROM` og resten av
   [E-postlevering](/nb/admin/authentication/#e-postlevering). Med en annen driver
   lagres en deltaker utenfra, men får aldri e-post: arket viser dem som
   **Ikke sendt**, med merknaden «Invitasjoner på e-post er ikke satt opp på denne
   installasjonen», loggen sier `calendar: invitations not sent: the mail driver is not
   smtp`, `GET /api/v1/calendar/meta` svarer
   `invitationsAvailable: false`, og **Send på nytt** svarer 503
   `invitations_unavailable`.
2. **Et avsenderdomene med SPF, DKIM og DMARC.** Hver invitasjon sendes **fra
   `SMTP_FROM`**, under arrangørens navn, og oppgir `SMTP_FROM` som arrangør
   (`ORGANIZER`) — det ene oppsettet ingen e-postklient avviser; arrangørens egen
   adresse brukes aldri. Derfor må `SMTP_FROM` være en adresse på et domene du styrer,
   der SPF-oppføringen nevner SMTP-serveren din, der serveren signerer e-posten med
   DKIM, og som publiserer en DMARC-policy de to stemmer med. Grunnen er Googles: siden
   2022 legger Google Kalender som standard en invitasjon inn i mottakerens kalender
   **bare når avsenderen er kjent**, og en uautentisert avsender er ikke det —
   invitasjonen venter da i e-posten til personen klikker i den, eller havner i
   søppelposten, og knappene vises kanskje ikke i det hele tatt. Navnet på en
   e-postserver, eller en gratis e-postadresse, som `SMTP_FROM` holder ikke.
3. **En postkasse bak `SMTP_FROM`**, eller et sluk. Når en mottaker trykker på en av
   klientens egne knapper, sender klienten svaret sitt (`METHOD:REPLY`) til arrangøren —
   altså til `SMTP_FROM`. Vantigo leser ikke den postkassen: svaret som teller, er det
   som gis gjennom lenken, og det er det invitasjonen ber om. La postkassen ta imot og
   kaste, eller la noen kikke i den; ikke la den sende e-posten i retur.
4. **`PUBLIC_BASE_URL`, for folk utenfor nettverket ditt.** Svarlenken er
   `PUBLIC_BASE_URL/rsvp?token=…` når `PUBLIC_BASE_URL` er satt, og `APP_URL` (med
   `APP_BASE_PATH`)`/rsvp?token=…` ellers. Siden trenger ingen innlogging i noen av
   tilfellene, men uten en offentlig vert kan bare den som når `APP_URL`, åpne den — nok
   for mottakere på bedriftens eget nettverk, ikke for en kunde. Sett opp den offentlige
   verten som for betalingssiden i Faktura
   ([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten)): det er én vert
   for begge, og stegene der — en fast `Host` i proxyen, proxyen klarert,
   spørrestrengen holdt utenfor proxyens logger — gjelder for svarlenkens token slik de
   gjør for betalingslenkens. En lenke er fast fra personen legges til på en avtale, så
   sett `PUBLIC_BASE_URL` før folk begynner å invitere.

### Rutene på den offentlige verten

På den offentlige verten serverer Vantigo svarsiden ved siden av betalingssiden, og
[kalenderstrømmen](#kalenderstrømmen-på-den-offentlige-verten), og ingenting annet av
kalenderen:

| Metode | Sti |
| --- | --- |
| `GET`, `HEAD` | `/rsvp` (svarsiden; hvilken som helst spørrestreng) |
| `POST` | `/api/v1/calendar/rsvp/read` (sidens lesing; tokenet i kroppen) |
| `POST` | `/api/v1/calendar/rsvp` (svaret) |
| `GET` | `/api/v1/calendar/feed/{token}.ics` (en kalenderadresse; nøyaktig ett segment) |

De to operasjonene er anonyme og begrenset per klientadresse — 60 lesinger og 30 svar
på ti minutter — så bak en proxy må `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` nevne
den, ellers deler alle mottakere proxyens ene bøtte
([Videresendte headere og HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).
Lenkens token står bare i spørrestrengen til forespørselen etter siden: Vantigos egen
forespørselslogg skriver stien og aldri spørrestrengen, og siden poster tokenet i stedet
for å legge det i en API-adresse. Hold proxyens tilgangslogg til stien for hele den
offentlige verten, slik stegene for betalingssiden viser.

### Kontroller

1. **Send deg selv en invitasjon.** Lag en avtale i morgen, og legg til en adresse du
   har hos Gmail eller Outlook.com under **Deltakere → Med e-post** — utenfor ditt eget
   domene, så sjekken er det en kunde ser. Lagre.
2. **Deltakerraden.** Åpne avtalen igjen: raden viser **Invitert** og dagens dato
   (**Invitert 3. nov. 2026**). **Sending mislyktes** betyr at SMTP-serveren avviste eller ikke svarte; se under.
3. **E-posten.** Den kommer fra `SMTP_FROM` under navnet ditt, med emnet
   `Invitasjon: <tittel> · <dag>` (eller `Invitation: …` når foretrukket språk i profilen
   din ikke er norsk), med klientens egne knapper og et `invite.ics`-vedlegg. I Gmail skal
   **Vis original** si `SPF: PASS`, `DKIM: PASS` og `DMARC: PASS`.
4. **Lenken.** Åpne lenken i e-posten fra en telefon utenfor bedriftens nettverk: siden
   viser avtalen uten innlogging, og adressefeltet viser ikke lenger tokenet. Svar
   **Godta**; deltakerraden i arket viser svaret, og du får en e-post, «… takket ja: …».
5. **En avlysning.** Slett avtalen: mottakeren får `Avlyst: …`, og lenken sier nå at
   avtalen er avlyst.

### Følg med

En sending som mislyktes, står to steder: på deltakerraden, som **Sending mislyktes**,
med **Send på nytt** ved siden av, som arrangøren ser; og i loggen, som advarselen
`calendar: an invitation could not be sent` med `appointment_id`, `attendee_id`,
`method` og feilen, med adressen i den byttet ut med `<recipient>`. En kontaktperson
uten adresse logges på info-nivå, `calendar: invitation not sent: the contact has no
address the directory answers`; en melding til en kollega som feiler,
`calendar: a notice could not be sent`.
Ingenting legges i kø og ingenting prøves på nytt av seg selv: arrangøren trykker
**Send på nytt** når årsaken er rettet. E-postene for én lagring — invitasjoner og
meldinger til kolleger — går den ene etter den andre etter at den er lagret, innen
tretti sekunder til sammen; invitasjonene som ikke er nådd da, merkes
**Sending mislyktes**, og selve lagringen feiler aldri på grunn av e-posten. En melding
til arrangøren om et svar har ti sekunder.

## Kalenderstrømmen på den offentlige verten

Hver person kan lage en **kalenderadresse** på skjermbildet **Min kalender** — en full og
en med bare opptatt — som mobilen, Google Kalender eller Outlook abonnerer på
([the feed](/en/reference/calendar/#the-feed)). Et kalenderprogram tar én adresse og
gjør en `GET` mot den, uten noe fra Vantigo, så dette er den ene anonyme `GET`-en i
Vantigo med tokenet i stien:

| Metode | Sti |
| --- | --- |
| `GET` | `/api/v1/calendar/feed/{token}.ics` |

Den står på listen til den offentlige verten ved siden av rutene til svarsiden
([rutene på den offentlige verten](#rutene-på-den-offentlige-verten)), bare under `GET` og
med nøyaktig ett segment etter `/feed/`. Et token ingen har — aldri utstedt,
tilbakestilt eller slått av — er en naken 404.

Det er også tokenet til en person som ikke lenger kan bruke Kalender: **å deaktivere en
bruker, eller ta `calendar:access` fra dem, gjør alle adressene deres døde med en
gang**, uten noe steg å ta. Ved hver forespørsel spør strømmen identitetsmodulen om
adressens person kan handle — ikke deaktivert, utestengt eller ventende, og, mens SCIM
er konfigurert, ikke deaktivert av katalogen med mindre de er Owner — og har
`calendar:access`, de samme kontrollene en økt holdes til
([the feed](/en/reference/calendar/#the-one-anonymous-get)). Adressen slettes ikke: blir
personen aktivert igjen, med rettigheten, oppdaterer mobilen deres igjen.

### Hva den trenger

1. **Basen.** Adressen er `PUBLIC_BASE_URL/api/v1/calendar/feed/<token>.ics` når
   `PUBLIC_BASE_URL` er satt, og på `APP_URL` (med `APP_BASE_PATH`) ellers. En mobil
   utenfor bedriftens nettverk trenger den offentlige verten; en bærbar på nettverket
   gjør ikke det. Uten en offentlig vert sier skjermbildet til personen *«Denne adressen
   virker bare på bedriftens nettverk.»* En adresse er fast fra den lages, så sett
   `PUBLIC_BASE_URL` før folk lager sine; en som ble laget før, må **Tilbakestilles** for
   å flytte til den offentlige verten. Merknaden følger innstillingen, ikke adressen: så
   snart `PUBLIC_BASE_URL` er satt, er den borte for alle, også ved en adresse som
   fortsatt står på `APP_URL` — les selve adressen.
2. **Proxyen, som for svarsiden.** Strømmen er begrenset per klientadresse —
   `calendar-feed`, **120 forespørsler per 10 minutter** — som en mobil som spør hver
   time, eller et kontor fullt av mobiler bak én NAT, holder seg godt under. Bak en proxy
   må `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` nevne den, ellers deler alle mobiler
   proxyens ene bøtte ([Videresendte headere og
   HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).
3. **Proxyens tilgangslogg.** Tokenet er stiens siste segment, ikke en spørrestreng:
   Vantigo logger stien til hver forespørsel, og det gjør en proxy også. **Hold proxyens
   tilgangslogg på den offentlige verten til stien uten strømmens siste segment, eller
   til ingenting**, og begrens hvem som leser Vantigos egen forespørselslogg, som skriver
   stien. Et token i en logg er lesetilgang til den personens kalender til de
   tilbakestiller det.

### Kontroller strømmen

1. **Lag en adresse.** Logg inn, åpne **Kalender → Min kalender**, trykk **Lag adresse**
   under **Hele kalenderen**, og **Kopier**.
2. **Hent den** fra utenfor bedriftens nettverk:

   ```sh
   curl -i 'https://pay.example.com/api/v1/calendar/feed/<token>.ics'
   ```

   Den svarer 200 med `Content-Type: text/calendar; charset=utf-8`, en `ETag` og
   `Cache-Control: private, no-cache`, og en kropp som begynner med `BEGIN:VCALENDAR`.
3. **Hent den igjen med taggen:**

   ```sh
   curl -i -H 'If-None-Match: "<ETag-en>"' 'https://pay.example.com/api/v1/calendar/feed/<token>.ics'
   ```

   Den svarer 304 uten kropp. Skjermbildet viser nå når adressen sist ble lest.
4. **Et feil token** — endre ett tegn — svarer 404, og det gjør adressen også etter en
   **Tilbakestill**.
5. **Proxyens logg** inneholder ikke noe token for disse forespørslene.

## Avlysningsarbeideren

Når en avtale som noen utenfor bedriften har fått invitasjon til, slettes, fjernes den
ikke: den **avlyses** — beholdes, skrivebeskyttet og borte fra alles kalendere, så
avlysningen kan si hva den avlyser, og en lenke som alt er sendt, fortsatt sier det
([cancellation](/en/reference/calendar/#cancellation)). Arbeideren
`calendar-cancellations` sletter en slik avtale når den siste dagen er passert:

- en enkeltavtale, etter at den er slutt;
- en serie med en siste dag, etter den dagen — eller etter slutten på en forekomst som
  er flyttet forbi den, det som kommer sist;
- en serie som aldri slutter, nitti dager etter at den ble avlyst.

Når den er slettet, sier mottakernes lenker **Denne lenken er ikke gyldig**.

Arbeideren kjører med mindre `CALENDAR_CANCELLATIONS_ENABLED=0`, hver
`CALENDAR_WORKERS_POLL` (standard `1h`, minst `1m`) — i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1` ([Bakgrunnsjobber og
skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)). Den sletter høyst 200
avtaler per runde og resten ved neste. Flere replikaer bytter på gjennom en
rådgivende lås; én sletter om gangen. Slått av blir avlyste avtaler liggende til den
er på igjen — i ingens kalender, men én rad hver.

| Variabel | Formål | Standard |
| --- | --- | --- |
| `CALENDAR_CANCELLATIONS_ENABLED` | Av/på-bryter for arbeideren `calendar-cancellations` | `1` |
| `CALENDAR_WORKERS_POLL` | Hvor ofte kalenderens arbeidere ser etter; en varighet på minst `1m` | `1h` |

## Abonnementsarbeideren

En person kan abonnere på kalendere fra andre steder på skjermbildet **Min kalender** —
høyst ti hver — og se hendelsene deres ved siden av sine egne
([subscriptions](/en/reference/calendar/#subscriptions)). Arbeideren
`calendar-subscriptions` henter dem. Den kjører med mindre
`CALENDAR_SUBSCRIPTIONS_ENABLED=0`, i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1`. Den **våkner hvert femte minutt** — ikke en innstilling — tar en
rådgivende lås så flere replikaer bytter på, og henter hvert abonnement som er forfalt,
høyst 200 per runde, ett om gangen. Hvert abonnement forfaller
`CALENDAR_SUBSCRIPTIONS_POLL` (standard `30m`, minst `5m`) etter forrige forsøk, eller —
etter en henting som svarte med kalenderen — senere når kalenderen selv ber om å bli lest
sjeldnere (høyst én gang i døgnet), pluss litt tilfeldig slingring. En henting som feiler, beholder kalenderens siste gode kopi og
skriver ned grunnen, og neste forfalte henting prøver igjen, uten å vente lenger. Slått
av hentes ingenting i bakgrunnen, og hvert abonnement viser det det sist leste; å lagre
et, eller **Oppdater nå**, henter det likevel én gang.

Arbeideren henter ingenting for en person som ikke lenger kan bruke Kalender —
deaktivert, utestengt, ventende, deaktivert av katalogen, eller uten `calendar:access`.
Den spør én gang per person per runde, og utsetter personens forfalte abonnementer med
intervallet, pluss slingring, og lar den siste kopien og grunnen stå som de var; de hentes
igjen når personen kan bruke Kalender igjen.

### Hva Vantigo henter fra internett

Arbeideren, en lagring og **Oppdater nå** henter **adressene folk abonnerer på**, og
ingenting annet: én `GET` hver, `User-Agent: Vantigo-Calendar`, sendt rett fra
Vantigo-containeren, gjennom den samme adressevakten som e-post og Peppol-oppslaget:

- verten slås opp og hver adresse kontrolleres: **ingen privat adresse, loopback,
  link-local, carrier-grade NAT eller sky-metadataadresse** ringes opp, og tilkoblingen
  går til adressen som ble kontrollert, så DNS rebinding ikke kan snu den innover;
- **ingen videresending følges** — en 3xx er en feil personen ser;
- **høyst 1 MiB** leses, og hentingen har **20 sekunder**;
- det er **ingen utgående proxy**: en `HTTPS_PROXY` brukes ikke, siden en proxy ville
  sendt forespørselen til en adresse vakten aldri så. En installasjon der containerne
  bare når internett gjennom en proxy, kan ikke hente abonnementer;
- **ingen tilkobling holdes åpen** mellom hentinger: hver av dem kobler opp på nytt,
  gjennom vakten;
- en lagring, en endring og **Oppdater nå** deler én hastighetsgrense,
  `calendar-subscription-fetch`, **30 forespørsler per klientadresse per 10 minutter**,
  besvart med 429 og `Retry-After` — langt mer enn en person som steller sine egne
  kalendere klikker, og langt mindre enn et skript som gjør serveren til en henter,
  trenger. Den gjelder per klientadresse, så bak en proxy må `TRUSTED_PROXY_HOPS` og
  `TRUSTED_PROXY_CIDRS` nevne den, ellers deler alle proxyens ene bøtte ([Videresendte
  headere og HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).
  Arbeideren er ikke begrenset.

En adresse en person skriver inn, avvises ved lagring når verten er en adresse vakten
forbyr, eller når den har med et brukernavn eller passord.

### Hva loggen sier

Hver henting som feiler, logges som advarsel med abonnementets id og grunnen, og
ingenting av adressen utover verten; personen ser den samme grunnen på raden sin.
Grunnene er én setning hver, på engelsk:

| Grunn | Betyr |
| --- | --- |
| the address redirects; subscribe to the address it points at | nettstedet svarte 3xx; personen må bruke adressen den peker på |
| the calendar is larger than 1 MiB | kalenderen er for stor til å beholdes; den siste gode kopien blir stående |
| the calendar has more than 10,000 events | kalenderen leses hver gang personens kalender lastes, så en så stor beholdes ikke; den siste gode kopien blir stående |
| the address did not answer a calendar | en 2xx som ikke er en iCalendar-fil — som regel en nettside |
| the address answered <status> (som `404 Not Found`) | enhver annen status: 404 for en fjernet kalender, 401 eller 403 for en som krever en innlogging Vantigo ikke gjør |
| the address is not one Vantigo may fetch | verten slås opp til en adresse vakten forbyr |
| the address did not answer in time | ikke noe svar innen de 20 sekundene en henting har, eller ingen tilkobling innen 10 eller ingen headere innen 15 |
| the address could not be reached | navnet kunne ikke slås opp, tilkoblingen ble avvist, eller det er ingen vei ut |

Å lese en lagret kalender, hver gang personens kalender lastes, er også begrenset: per
abonnement per lesing høyst 10 000 hendelser, 200 000 arbeidssteg og ett sekund, og de
5 000 elementene nærmest starten av vinduet som vises, beholdes. En kalender som når en grense, viser det som
var lest til da, og loggen sier det på info-nivå med abonnementets id. Ingenting må
gjøres; personen kan be den som publiserer kalenderen, om en mindre en.

| Variabel | Formål | Standard |
| --- | --- | --- |
| `CALENDAR_SUBSCRIPTIONS_ENABLED` | Av/på-bryter for arbeideren `calendar-subscriptions` | `1` |
| `CALENDAR_SUBSCRIPTIONS_POLL` | Hvor ofte hvert abonnement hentes; en varighet på minst `5m` | `30m` |

## Postbokssynkroniseringen

En person kan la Vantigo holde en kopi av kalenderen sin i Microsoft 365-postboksen sin,
på skjermbildet **Min kalender** ([Synkroniser med
Outlook](/nb/user/calendar/#synkroniser-med-outlook)), når en eier har slått på
**Kalendersynkronisering** i Microsoft Entra ID-modulen og gitt tillatelsen den trenger
([Microsoft Entra ID](/nb/admin/entra/#kalendersynkronisering)). Arbeideren
`calendar-mailbox-sync` skriver kopiene gjennom Entra-modulens tilkobling
([arbeideren](/en/reference/calendar/#the-mailbox-sync-worker)). Den kjører med mindre
`CALENDAR_MAILBOX_SYNC_ENABLED=0`, i worker-modus, og i api-modus med
`WORKERS_IN_PROCESS=1`. Den **våkner hvert minutt** — ikke en innstilling — tar en
rådgivende lås så flere replikaer bytter på, og synkroniserer hver person som står for
tur, høyst 50 per runde, én om gangen, hver med tre minutter til det synkroniseringen
spør Microsoft og de andre modulene om, så én treg postboks ikke holder igjen resten. En
person står for tur
`CALENDAR_MAILBOX_SYNC_POLL` (standard `15m`, minst `1m`) etter forrige synkronisering,
og med en gang når en av avtalene deres endres, når de endrer innstillingen sin, eller
når de trykker **Synkroniser nå** — så en avtale når Outlook innen et minutt. En
synkronisering som feiler, prøves igjen etter 1, 5, 15 og 60 minutter, deretter hver
time, og vises som feilet fra den femte på rad; en postboks leietakeren avviser, prøves
igjen om seks timer; en tilkobling som ikke virker, om en time.

Før hver skriving **tar arbeideren også med tilbake det folk har endret i Outlook**: en
endring, flytting eller sletting av en kopi av deres egen avtale endrer avtalen med de
samme reglene — og de samme e-postene til kolleger og til folk utenfor bedriften — som
en endring gjort i Vantigo, og en hendelse laget i en egen *Vantigo*-kalender blir til en
avtale ([tilbakeskrivingen](/en/reference/calendar/#the-write-back)). **En kopi som
slettes i Outlook, sletter eller avlyser avtalen i Vantigo.** Alt annet som endres i en
kopi, legges tilbake. Den skriver høyst 20 avtaler per person per synkronisering, på
vegne av den personen. En lesing av kopiene som feiler, lar skrivingen fortsette og
prøves igjen etter feilens ventetid; kortet til personen sier da at den siste
synkroniseringen mislyktes.

Arbeideren fjerner kopiene til en person når de slår synkroniseringen av, når de ikke
lenger kan bruke Kalender — deaktivert, utestengt, deaktivert av katalogen, eller uten
`calendar:access` — når Ansatte ikke lenger kobler dem til en Microsoft Entra ID-konto,
og for alle når **Kalendersynkronisering** slås av. **Slått av skriver og fjerner
arbeideren ingenting**: kopiene blir liggende i postboksene slik de sist var, også
kopiene til en som har sluttet, til den slås på igjen.

Den når nettet bare gjennom Entra-modulens Graph-klient — de to faste vertene til
Microsoft, med modulens egne grenser — og logger en person bare med bruker-id-en: aldri
postboksen, tittelen til en avtale eller en hendelse eller Graphs ord. En synkronisering som feilet, logges på
warning-nivå med steget som feilet; en avvist postboks på info-nivå med koden.

| Variabel | Formål | Standard |
| --- | --- | --- |
| `CALENDAR_MAILBOX_SYNC_ENABLED` | Av/på-bryter for arbeideren `calendar-mailbox-sync` | `1` |
| `CALENDAR_MAILBOX_SYNC_POLL` | Hvor ofte hver persons kopier synkroniseres når ingenting har gjort dem forfalt tidligere; en varighet på minst `1m` | `15m` |

## Nøkkelmateriale

Svarlenkene lagres forseglet under en nøkkel avledet fra `APP_SECRET` (formålet
`calendar/rsvp-link`), så **Send på nytt** bærer samme lenke som den første e-posten. Å
rotere `APP_SECRET` gjør alle lagrede lenker uåpnelige: en lenke som alt er sendt,
svarer fortsatt — tokenet lagres som en vanlig hash, som ikke trenger noen nøkkel — men
en oppdatering eller **Send på nytt** til en deltaker som ble lagt til før rotasjonen,
feiler og viser **Sending mislyktes**; ta dem av avtalen og legg dem til igjen for en ny
lenke
([Nøkkelmateriale](/nb/admin/authentication/#nøkkelmateriale)).

Kalenderadressene lagres forseglet på samme måte, under formålet
`calendar/feed-address`, så skjermbildet **Min kalender** kan vise en persons adresse
igjen. Etter en rotasjon kan skjermbildet ikke åpne dem og viser ingen adresse, men hver
adresse virker fortsatt — en forespørsel matches mot tokenets vanlige hash, som ikke
trenger noen nøkkel — så mobilene fortsetter å oppdatere. En person som trenger å se
adressen sin igjen, trykker **Lag adresse**, som skjermbildet viser i stedet: den
tilbakestiller adressen, den gamle slutter å virke, og de gir programmene sine den nye.

Hendelsene i adressen med bare opptatt har også identifikatorer avledet fra `APP_SECRET`
(formålet `calendar/feed-busy-uid`), så de sier ingenting om posten bak dem. En rotasjon
endrer hver av dem: et program som abonnerer på en adresse med bare opptatt, fjerner
blokkene som kommer fra de andre appene, og legger dem til på nytt ved neste
oppdatering — én gang, uten at noe går tapt. Avtaler beholder identifikatorene sine.

## Hva som kan gå galt

- **Mottakeren ser en vanlig e-post uten knapper.** Klienten kjente ikke igjen
  invitasjonen: den ble videresendt (en videresendt invitasjon nevner en annen), eller
  en port på veien fjernet `text/calendar`-delen. Be dem svare med lenken, som virker i
  alle klienter.
- **Invitasjonen står ikke i mottakerens Google-kalender før de klikker i e-posten,
  eller havner i søppelposten.** Domenet til `SMTP_FROM` feiler SPF, DKIM eller DMARC,
  eller er ukjent for mottakeren. Sjekk **Vis original** i Gmail og rett domenets
  oppføringer.
- **Outlook forkaster invitasjonen uten spor.** Mottakerens adresse er `SMTP_FROM`
  selv: Outlook forkaster en invitasjon der arrangøren er mottakeren. Inviter en annen
  adresse.
- **Alle deltakere viser Ikke sendt.** `MAIL_DRIVER` er ikke `smtp`; arket sier det.
  Sett den, start på nytt, og trykk så **Send på nytt** på hver, eller endre avtalens
  tidspunkt eller sted, som sender til alle.
- **Sending mislyktes** på en deltaker. SMTP-serveren avviste eller svarte ikke —
  loggen har grunnen — eller deltakeren er en kontaktperson som har fått adressen
  fjernet i Kunder, eller er anonymisert der (raden viser da **En kontakt**). Rett
  årsaken og trykk **Send på nytt**; en kontaktperson uten adresse må få en i Kunder
  først.
- **Lenken åpner ingenting for en kunde, men virker på kontoret.** `PUBLIC_BASE_URL` er
  ikke satt, så lenken står på `APP_URL`, som kunden ikke når. Sett opp den offentlige
  verten. Lenker som alt er sendt, beholder den gamle adressen; en deltaker som tas av
  avtalen og legges til igjen, får en avlysning og så en ny invitasjon med en lenke på
  den offentlige verten.
- **Svarsiden er blank, eller forespørslene dens svarer 404, på den offentlige
  verten.** Proxyen serverer den under en annen sti enn `APP_BASE_PATH`, eller fjerner
  basestien selv.
- **Mottakere avvises med 429.** `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` er ikke
  satt, så alle mottakere deler proxyens adresse og dens ene bøtte.
- **«Jeg svarte med knappene, og arrangøren ser ikke noe svar.»** Knappene sender
  e-post til `SMTP_FROM`, som Vantigo ikke leser. Svar med lenken.
- **En avlyst avtale er der fortsatt.** Den siste dagen er ikke passert, eller
  arbeideren er av (`CALENDAR_CANCELLATIONS_ENABLED=0`).
- **`CALENDAR_WORKERS_POLL` avvist ved oppstart.** Den er ikke en varighet, eller er
  under ett minutt.
- **En mobil oppdaterer aldri.** Klienten bestemmer når den oppdaterer — Apple Kalender
  slik det er stilt inn på enheten, Outlook omtrent hver tredje time, Google Kalender
  opptil et døgn — så vent før du mistenker Vantigo. Ellers: adressen er
  **Tilbakestilt** eller slått av, og mobilen har den døde (en 404 i proxyens logg);
  eller den står på `APP_URL`, som mobilen ikke når utenfor bedriftens nettverk —
  skjermbildet sier *«Denne adressen virker bare på bedriftens nettverk»*; sett opp den
  offentlige verten, og personen tilbakestiller adressen for å få en der. En 429 i loggen
  betyr at proxyen ikke er klarert, og at alle mobiler deler én bøtte.
- **Å legge til et abonnement eller Oppdater nå avvises med 429.** Tretti på ti minutter
  fra én klientadresse er grensen; bak en proxy som ikke er klarert
  (`TRUSTED_PROXY_HOPS`, `TRUSTED_PROXY_CIDRS`), deler alle proxyens ene bøtte.
- **En strøm svarer 503.** En av modulene som har elementer i den, kunne ikke leses;
  loggen nevner den. Strømmen serveres aldri med noe som mangler.
- **En persons kalender er over 1 MiB.** Strømmen dropper de projiserte elementene
  lengst fra i dag, de fjerneste først, til den får plass, logger det på info-nivå med
  personens id, og dropper aldri en avtale. Ingenting må gjøres.
- **Et abonnement viser en feil.** Les setningen på raden
  ([hva loggen sier](#hva-loggen-sier)). «Not one Vantigo may fetch» for en offentlig
  kalender betyr at verten slås opp til en privat adresse fra containeren — delt DNS;
  «could not be reached» eller «did not answer in time» for alle abonnementer betyr at containerne ikke har noen vei
  til internett, eller bare når det gjennom en proxy, som hentingen ikke bruker.
- **`CALENDAR_SUBSCRIPTIONS_POLL` avvist ved oppstart.** Den er ikke en varighet, eller
  er under fem minutter.
- **En persons Outlook-kort kan ikke slås på, eller sier at synkroniseringen har
  stoppet.** Det sier hvorfor i en setning; hva hver betyr for Entra-tilkoblingen, og hva
  du gjør, står i [Microsoft Entra ID](/nb/admin/entra/#hva-som-kan-gå-galt).
- **Avtaler kommer ikke til Outlook.** Arbeideren er av
  (`CALENDAR_MAILBOX_SYNC_ENABLED=0`), eller ingen prosess kjører arbeidere
  (worker-modus, eller `WORKERS_IN_PROCESS=1`); kortet til personen blir da stående på
  **Venter på den første synkroniseringen …**.
- **`CALENDAR_MAILBOX_SYNC_POLL` avvist ved oppstart.** Den er ikke en varighet, eller er
  under ett minutt.
