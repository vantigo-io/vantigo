---
title: Kalender
description: Slå på Kalender og gi folk de to rettighetene; hva invitasjoner til folk utenfor bedriften trenger — SMTP-driveren, et avsenderdomene med SPF, DKIM og DMARC, den offentlige verten og de tre rutene dens; avlysningsarbeideren og de to innstillingene dens; hvordan du sjekker at en invitasjon gikk, og hva som kan gå galt.
sidebar:
  order: 45
sources:
  - apps/server/internal/calendar/invitations.go
  - apps/server/internal/calendar/rsvp.go
  - apps/server/internal/calendar/cancellations_worker.go
  - apps/server/internal/calendar/module.go
---

Kalender er én oversikt over dagene til hver person: avtalene de lager, med kolleger,
kunders kontaktpersoner og andre på, ved siden av det de andre modulene vet om dem.
Denne siden er driftssiden: å slå modulen på, hva invitasjonene til folk utenfor
bedriften trenger, og arbeideren som rydder opp etter en avlysning. Hva modulen gjør og
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
- Kalender har ingen egne innstillinger. De to rettighetene er `calendar:access`, for å
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

### De tre rutene på den offentlige verten

På den offentlige verten serverer Vantigo svarsiden ved siden av betalingssiden, og
ingenting annet av kalenderen:

| Metode | Sti |
| --- | --- |
| `GET`, `HEAD` | `/rsvp` (svarsiden; hvilken som helst spørrestreng) |
| `POST` | `/api/v1/calendar/rsvp/read` (sidens lesing; tokenet i kroppen) |
| `POST` | `/api/v1/calendar/rsvp` (svaret) |

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

## Nøkkelmateriale

Svarlenkene lagres forseglet under en nøkkel avledet fra `APP_SECRET` (formålet
`calendar/rsvp-link`), så **Send på nytt** bærer samme lenke som den første e-posten. Å
rotere `APP_SECRET` gjør alle lagrede lenker uåpnelige: en lenke som alt er sendt,
svarer fortsatt — tokenet lagres som en vanlig hash, som ikke trenger noen nøkkel — men
en oppdatering eller **Send på nytt** til en deltaker som ble lagt til før rotasjonen,
feiler og viser **Sending mislyktes**; ta dem av avtalen og legg dem til igjen for en ny
lenke
([Nøkkelmateriale](/nb/admin/authentication/#nøkkelmateriale)).

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
