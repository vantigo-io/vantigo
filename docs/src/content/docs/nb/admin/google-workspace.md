---
title: Google Workspace
description: Å koble Vantigo til en Google Workspace-organisasjon gjennom en tjenestekonto med domenedekkende delegering — Google Cloud-prosjektet og Admin SDK API, tjenestekontoen og JSON-nøkkelen, å oppheve iam.disableServiceAccountKeyCreation for prosjektet, å laste opp nøkkelen, å delegere tilgangsområdene i administrasjonskonsollen og vente på dem, administratoren som skal etterlignes, den interne nettklienten og de to omdirigerings-URI-ene, å verifisere med Test tilkoblingen — deretter kontoene Ansatte holder i Google, kontoreglene (adressen, organisasjonsenheten, det første passordet og levetiden til det), lisenser, Ansattes grupper og de tre innstillingene deres, bilder i 96 px, hva stegene for en som slutter gjør, slett etter N dager, å bekrefte en konto eller en gruppe Vantigo ikke opprettet, å rotere en nøkkel, å bytte tjenestekonto, og hva som kan gå galt.
sidebar:
  order: 47
sources:
  - apps/server/internal/google/module.go
  - apps/server/internal/google/client.go
  - apps/server/internal/google/token.go
  - apps/server/internal/google/credentials.go
  - apps/server/internal/google/connection.go
  - apps/server/internal/google/test.go
  - apps/server/internal/google/status.go
  - apps/server/internal/google/provisioner.go
  - apps/server/internal/google/directory.go
  - apps/server/internal/google/lifecycle_prepare.go
  - apps/server/internal/google/lifecycle_activate.go
  - apps/server/internal/google/lifecycle_deactivate.go
  - apps/server/internal/google/licences.go
  - apps/server/internal/google/groups.go
  - apps/server/internal/google/groups_traits.go
  - apps/server/internal/google/photo.go
  - apps/server/internal/google/accounts.go
---

Google Workspace-modulen kobler Vantigo til selskapets Google Workspace-organisasjon.
Den logger inn som en **tjenestekonto** med **domenedekkende delegering**: Google lar
tjenestekontoen opptre som en administrator i organisasjonen, for de tilgangsområdene
en superadministrator delegerer til den, og ingenting annet. Gjennom den tilkoblingen
holder Ansatte personenes Google-kontoer og gruppene sine i organisasjonen: den
oppretter en persons konto før den første dagen, åpner den på dagen med et første
passord HR gir videre, holder profilen og lederen i samsvar, tildeler lisensene til
gruppene deres, skriver bildet deres, og suspenderer kontoen når de slutter
([Kontoene](#kontoene)). Hver persons egen Google-tilkobling, kalendersynkronisering og
Gmail kommer i senere versjoner, på den samme tilkoblingen.

Denne siden er driftssiden: hva Google trenger, stegene i Google Cloud-konsollen og i
administrasjonskonsollen, og hva du gjør når noe feiler. Siden i Vantigo er **Google
Workspace** under **Integrasjoner** i Administrer arbeidsområde; skjermbildene står i
[brukerveiledningen](/nb/user/google-workspace/), og reglene modulen sjekker, står i
[referansen](/en/reference/google/).

## Før du begynner

- **En Google Workspace-organisasjon**, og en **superadministrator** i den:
  domenedekkende delegering gis i administrasjonskonsollen (Admin console), og bare en
  superadministrator kan gi den.
- **Et Google Cloud-prosjekt under organisasjonen**, eller retten til å opprette ett, og
  noen som kan opprette tjenestekontoer og nøkler i det. Forbyr organisasjonen nøkler
  for tjenestekontoer, trengs noen med **Organization Policy Administrator** i
  organisasjonen ([nedenfor](#opprett-tjenestekontoen-og-nøkkelen)).
- **En eier** i Vantigo, innlogget med en andre faktor mens `OWNERS_REQUIRE_MFA` er på.
  Nøkkelen, administratoren som etterlignes, nettklienten og testen er bare en eiers; en
  person med `google:manage` ser dem skrivebeskyttet.
- **Ansatte på.** `MODULES` avviser `google` uten `staff` ved oppstart
  (`google requires staff`): kontoene Vantigo skal holde i Google, er Ansattes
  avgjørelse, slik de er for Microsoft Entra ID.
- **Utgående https** fra hver prosess som kjører API-et eller bakgrunnsjobbene, til
  Googles verter — `oauth2.googleapis.com`, `admin.googleapis.com`,
  `licensing.googleapis.com`, `www.googleapis.com`, `gmail.googleapis.com`,
  `groupssettings.googleapis.com` og `accounts.google.com` — uten proxy: modulen bruker
  ingen og følger ingen omdirigering. Denne versjonen kaller de fire første.
- Migreringene lager skjemaet `google` enten modulen er slått på eller ikke; en
  applikasjonsrolle med minste privilegium trenger tilgang til det som til alle andre
  ([Installasjon](/nb/admin/installation/#databaseroller)).

Ingenting ved modulen står i `vantigo.env`: det finnes ingen `GOOGLE_*`-variabel.
Nøkkelen, administratoren og nettklienten ligger i databasen, satt på modulens side — en
innstilling på arbeidsområdenivå, ikke på installasjonsnivå
([Konfigurasjon](/nb/admin/configuration/)).

## Slå på modulen

1. La `MODULES` være usatt, eller legg til `google` ved siden av `staff`.
2. Start Vantigo på nytt (`docker compose up -d`).
3. Verifiser: **Google Workspace** står i sidemenyen til **Administrer arbeidsområde**,
   under **Integrasjoner**, for en eier (modulen har ingen flis i appvelgeren), og
   `GET /api/v1/google/connection` svarer 401 uten økt i stedet for 404-en en avslått
   moduls sti svarer.

Åpne avatarmenyen, velg **Administrer arbeidsområde**, og velg **Google Workspace** under
**Integrasjoner** (`/workspace/google`). Fanen **Tilkobling** har seks steg, hvert et kort
i rekkefølgen nedenfor; til en nøkkel er lastet opp, åpner siden på den. Ha Googles
konsoller åpne i en annen fane.

## Opprett prosjektet

1. I **Google Cloud-konsollen** oppretter du et prosjekt under organisasjonen (for
   eksempel `vantigo`), eller velger ett dere bruker til integrasjoner. Det må ligge
   under organisasjonen for at samtykkeskjermen kan være *Internal*.
2. I prosjektet åpner du **APIs & Services** › **Library**, finner **Admin SDK API** og
   velger **Enable**. Uten den avviser Google testens lesing av organisasjonen.

## Opprett tjenestekontoen og nøkkelen

1. I prosjektet åpner du **IAM & Admin** › **Service accounts** › **Create service
   account**. Gi den et navn (for eksempel `vantigo-workspace`) og velg **Done**. Den
   trenger ingen rolle i prosjektet: hva den kan gjøre i Workspace, er delegeringen, ikke
   IAM.
2. Åpne tjenestekontoen, deretter **Keys** › **Add key** › **Create new key**, velg
   **JSON** og **Create**. Nettleseren laster ned nøkkelfilen én gang; Google har ingen
   kopi du kan laste ned igjen. Ta vare på den til den er lastet opp i Vantigo, og slett
   den så.

**Hvis Google ikke vil lage nøkkelen.** Organisasjoner opprettet fra 2024 kan håndheve
policyen `iam.disableServiceAccountKeyCreation`, som forbyr alle nøkler for
tjenestekontoer, og konsollen sier da at det er slått av å lage nøkler. Opphev den bare
for dette prosjektet:

1. Noen med **Organization Policy Administrator** i organisasjonen åpner prosjektet,
   deretter **IAM & Admin** › **Organization policies**, og finner **Disable service
   account key creation** (`iam.disableServiceAccountKeyCreation`).
2. **Manage policy** › **Override parent's policy**, legg til en regel med
   **Enforcement** *Off* (*Not enforced*), og velg **Set policy**. Resten av
   organisasjonen beholder policyen.
3. Lag nøkkelen som over. Policyen kan settes tilbake etterpå: den stopper nye nøkler,
   ikke dem som allerede er laget — men en [rotasjon](#roter-en-nøkkel) trenger den
   opphevet igjen.

Om alle nye organisasjoner håndhever policyen, er ikke bekreftet ennå; steget trengs
bare når konsollen avviser.

## Last opp nøkkelen

1. I steg **2 Last opp nøkkelen** limer du inn innholdet i nøkkelfilen i **Innholdet i
   nøkkelfilen**, eller velger **Velg nøkkelfilen**. Filen leses i nettleseren din.
2. Velg **Last opp nøkkel**.

Vantigo sjekker filen — høyst 8 KiB, av typen `service_account`, med `token_uri`
`https://oauth2.googleapis.com/token` og en privat nøkkel som er en RSA-nøkkel i PKCS #8
PEM — forsegler den private nøkkelen med en gang og viser den aldri igjen. Den beholder
tjenestekontoens adresse, den numeriske **Klient-ID**-en, **Nøkkel-ID**-en og
prosjektet, og viser nøkkelen under **Nøkler for tjenestekontoen** som **I bruk**. Slett
den nedlastede filen nå.

## Deleger tilgangsområdene

1. Steg **3 Deleger tilgangsområdene** viser tjenestekontoens numeriske **Klient-ID** og
   **OAuth-tilgangsområder (kommadelt)**; kopier begge. Denne versjonen trenger seks:
   - `https://www.googleapis.com/auth/admin.directory.customer.readonly` —
     organisasjonen;
   - `https://www.googleapis.com/auth/admin.directory.domain.readonly` — domenene;
   - `https://www.googleapis.com/auth/admin.directory.user` — kontoene, bildene deres,
     og å logge ut en som slutter;
   - `https://www.googleapis.com/auth/admin.directory.group` — Ansattes grupper og
     medlemmene deres;
   - `https://www.googleapis.com/auth/apps.groups.settings` — hvem som ser en gruppe;
   - `https://www.googleapis.com/auth/apps.licensing` — lisensene.
2. Som superadministrator åpner du **Security** › **Access and data control** › **API
   controls** › **Manage domain-wide delegation** i **Google Admin console**, og velger
   **Add new**.
3. Lim inn **Client ID** og tilgangsområdene, og velg **Authorise**.

Deleger bare tilgangsområdene siden viser. Delegeringen når alle kontoene i
organisasjonen; en senere versjon viser flere tilgangsområder på siden når den trenger
dem, og da redigerer dere den samme klientens oppføring.

**Det kan ta opptil 24 timer** før Google tar i bruk en ny delegering; vanligvis tar det
minutter. Bruker organisasjonen **godkjenning fra flere parter** (multi-party approval)
for sensitive administratorhandlinger, venter endringen til en annen superadministrator
har godkjent den.

## Oppgi administratoren som skal etterlignes

Domenedekkende delegering opptrer *som* noen: hvert kall modulen gjør mot Admin SDK,
gjøres som denne administratoren, gjennom delegeringen.

1. I steg **4 Oppgi administratoren som skal etterlignes** skriver du inn
   **Administratorens adresse**: en superadministrator, eller en administrator med en
   rolle som kan lese organisasjonens brukere og domener (og, for kontoene som kommer,
   administrere brukere og grupper).
2. Velg **Lagre**.

Lagres en annen adresse, går tilkoblingen tilbake til *Ikke satt opp*, og
tilgangsområdene den siste testen fant, glemmes, til den testes igjen.

## Verifiser

1. I steg **5 Test tilkoblingen** velger du **Test tilkoblingen**. Vantigo ber Google om
   et nytt token for hvert tilgangsområde som administratoren, og leser deretter
   organisasjonen og de bekreftede domenene.
2. Forvent under **Testresultat**: **Tilkoblet**, **Organisasjon**, **Kunde-ID** (`C0…`),
   **Primærdomene** og **Bekreftede domener**. Fanen **Status** viser da det samme,
   *Alle tilgangsområdene Vantigo trenger, er gitt.*, og steget får *Ferdig*.

**Venter på delegering** betyr at Google avviste alle tilgangsområdene: delegeringen er
ikke lagt inn, eller ikke tatt i bruk ennå. Vent, og test igjen. **Tilkoblet** med
*Tilgangsområder Vantigo trenger som Google avviste* viser dem som må legges til i
delegeringen. Ansatte holder ingen konto i Google før den siste testen fikk de fire
tilgangsområdene for kontoene, gruppene, gruppeinnstillingene og lisensene: til da viser
kortet **Kontoer** i Ansatte at Google Workspace feiler (`scopes_missing`). En
installasjon oppgradert fra versjonen før trenger de fire lagt til i delegeringen og en
ny test.

**Tilgangsområder Vantigo ikke bruker.** Testen ber også om tilgangsområder Vantigo
aldri bruker, men som en delegering kopiert fra et annet produkt ofte har med —
`https://mail.google.com/`, `gmail.readonly`, `gmail.modify`, `gmail.send`, `calendar`,
`calendar.events`, `admin.directory.user.security`, `drive` og `cloud-platform` (alle
under `https://www.googleapis.com/auth/` unntatt det første). De Google gir, vises i en
oransje melding på fanen **Status**. Tilkoblingen forblir **Tilkoblet**; fjern disse
tilgangsområdene fra klientens delegering i administrasjonskonsollen, med mindre et
annet produkt bruker den samme tjenestekontoen.

## Sett opp den interne nettklienten

Hver persons egen Google-tilkobling og innloggingen med Google, i senere versjoner,
bruker en OAuth-nettklient i det samme prosjektet. Legg den inn nå, slik at prosjektet
settes opp i én omgang; ingenting bruker den ennå.

1. I prosjektet åpner du **Google Auth Platform** › **Branding** (samtykkeskjermen for
   OAuth) og setter den opp med **Audience** *Internal*, slik at bare kontoer i
   organisasjonen kan bruke den.
2. Åpne **Clients** › **Create client**, velg **Web application**, og legg til begge
   **Authorised redirect URIs** som steg **6 Sett opp den interne nettklienten** viser:
   - `APP_URL` + `APP_BASE_PATH` + `/api/v1/google/connect/callback`
   - `APP_URL` + `APP_BASE_PATH` + `/api/v1/identity/oidc/callback`
3. Velg **Create**, og kopier **Client ID** og **Client secret** med en gang: Google
   viser en klienthemmelighet bare nå. Går den tapt, legger dere til en ny hemmelighet på
   klienten.
4. I steg 6 skriver du inn **Klient-ID** (den slutter på `.apps.googleusercontent.com`)
   og **Klienthemmelighet**, og velger **Lagre**. Hemmeligheten forsegles og vises aldri
   igjen.

For å rotere hemmeligheten legger du til en ny på klienten i konsollen, skriver den inn
i steg 6 med den samme klient-ID-en og velger **Lagre**: den eldre trekkes tilbake i
Vantigo. Slett så den gamle i konsollen. En ny klient-ID trenger alltid hemmeligheten
sin.

## Kontoene

Folk kommer til Google gjennom Ansatte. HR gir en person kontotypen **Google
Workspace** på Konto-fanen deres i Ansatte, eller gjør den til standard for nye personer
([Ansatte](/nb/admin/staff/#kontotypen)); kontoarbeideren i Ansatte styrer så personens
konto i Google, gjennom denne modulen, slik den styrer kontoene i Microsoft Entra ID:

1. **Før den første dagen** (Ansattes *Forbered kontoen (dager før)*), finnes eller
   opprettes kontoen **suspendert**: Google holder den, og ingen kan logge inn. Vantigo finner en persons
   konto etter **ansattnummeret**, som holdes som kontoens eksterne ID av typen
   *Organization* — det samme feltet som fylles ut for eksisterende kontoer, under
   **User information** › **Employee information** › **Employee ID** i
   administrasjonskonsollen.
2. **Den første dagen** oppheves suspenderingen, Google får et første passord — når
   ingen har logget inn på kontoen ennå — og lisensene til personens grupper tildeles.
3. **Mens de jobber**, når en endring av navnet, stillingstittelen, avdelingen,
   kostnadsstedet, telefonene, kontorstedet, lederen eller språket Google ved neste
   kjøring.
4. **Dagen etter at de slutter**, suspenderes kontoen, og stegene for en som slutter,
   kjøres.
5. **Etter Ansattes slettedager** slettes kontoen.

Modulen kjører ingenting selv: kontoarbeideren i Ansatte leser hver konto fra Google ved
hver kjøring, og det er slik en suspendering eller sletting gjort i
administrasjonskonsollen blir sett.

### Sett kontoreglene

På fanen **Kontoer** på siden for Google Workspace setter en med `google:manage`
**Kontoregler**, og velger så **Lagre kontoreglene**:

- **Adressedomene** — et av organisasjonens bekreftede domener, som testen leste; tomt
  for primærdomenet.
- **Adressemønster** — for en person hvis e-post på jobb ikke kan være adressen:
  `given.family`, `g.family` eller `given`. En e-post på jobb på et bekreftet domene
  brukes som den er. Bokstaver foldes (`ø` → `o`, `æ` → `ae`, `å` → `a`, aksenter
  fjernes); en adresse som er tatt, får 2, 3, … 99 etter seg. En e-post på jobb en annen
  konto allerede har, får ikke tall etter seg: personens konto holdes igjen, *adressen er
  tatt*. Adressen er kontoens **primære e-postadresse** i Google, og Vantigo gir den
  aldri nytt navn.
- **Organisasjonsenhet** — der nye kontoer lages, `/` for toppen, for eksempel
  `/Ansatte`. Enheten må finnes i administrasjonskonsollen; en konto modulen fant, blir
  stående der den er.
- **Første passord** — levetiden i dager (1 til 30, sju om ikke endret) og hvem som gir
  det videre. HR viser det én gang på personens Konto-fane i Ansatte; innstillingen for
  hvem som gir det videre, tas vare på til en senere versjon og sender ikke lederen noe
  ennå.
- **Når noen slutter** — fjern lisensene, hvilke grupper de tas ut av (*alle*, *gruppene
  Vantigo har laget* eller *ingen*), og logg dem ut av alle økter; hvert steg er på med
  mindre det slås av ([hva de gjør](#hva-stegene-for-en-som-slutter-gjør)).

Fanen **Kontoer** viser også kontoene og gruppene Vantigo holder, og organisasjonens
**Lisenser**.

### Det første passordet

Google har ikke noe midlertidig tilgangspass. Når suspenderingen oppheves den første
dagen, setter Vantigo et nytt passord på 16 tegn — uten bokstavene og sifrene som lett
forveksles — som personen **må bytte** ved første innlogging. Det holdes forseglet til
HR viser det, **én gang**, på personens Konto-fane i Ansatte, og glemmes når levetiden
er ute. HRs **Kjør nå** setter et nytt.

Vantigo setter et passord **bare så lenge ingen har logget inn på kontoen ennå** — aldri
over en konto noen har brukt, fordi et nytt passord der ville være en vei inn i noens
postkasse. Det gjelder også når suspenderingen som oppheves, ikke er en nyansatts: en
konto en administrator suspenderte i administrasjonskonsollen, den gamle kontoen til en
som kommer tilbake, eller en konto Vantigo fant og noen bekreftet. Vantigo opphever da
suspenderingen uten passord, og personens Konto-fane sier det (`pass_not_set_used`):
trenger personen et nytt passord, tilbakestiller en administrator det i
administrasjonskonsollen.

Avviser organisasjonens passordregler passordet, åpnes kontoen uten et, og personens
Konto-fane sier det (`pass_not_allowed`): tilbakestill passordet i
administrasjonskonsollen, eller lemp på reglene for organisasjonsenheten.

### Lisenser

En lisens er en SKU i Google Workspace, skrevet `productId/skuId` (for eksempel
`Google-Apps/1010020028`). På fanen **Kontoer** spør **Les lisensene** Google hvilke
lisenser organisasjonen har — for Google Workspace, Cloud Identity, Google Voice og
lisenser for arkiverte brukere, dem minst én bruker har — og tar vare på dem: de er
valgene i en Ansatte-gruppes **Lisenser**. En person tildeles alle lisensene gruppene
deres navngir, den første dagen, og senere når gruppene deres endres; en ingen gruppe
navngir lenger, fjernes — men bare en Vantigo tildelte selv. En lisens en administrator
tildelte i administrasjonskonsollen, blir stående. Når det ikke er noen igjen å tildele,
sier personens Konto-fane det (`licence_unavailable`, med SKU-en), og Ansatte prøver
igjen ved neste kjøring.

### Ansattes grupper i Google Workspace

HR slår på Google Workspace for en gruppe i Ansatte på gruppens side, med tre
innstillinger:

| Innstilling | Regel |
| --- | --- |
| **Gruppeadresse** | Gruppens e-postadresse, på et av de bekreftede domenene; tom for gruppens nøkkel på adressedomenet. Den kan ikke endres når gruppen finnes. |
| **Hvem ser gruppen** | *Alle i organisasjonen* — alle i organisasjonen ser gruppen og medlemmene, og finner den i katalogen — eller *Bare medlemmene*. Skrives gjennom Groups Settings. |
| **Lisenser** | Lisensene hvert medlem med en Google-konto får, så lenge de er i gruppen. |

Gruppen lages ved neste kjøring, med navnet og beskrivelsen fra Ansatte; eierne er
gruppens eiere i Ansatte, som medlemmer med rollen *Owner*; medlemmene er gruppens
personer som har en aktiv Google-konto. Et medlem noen legger til i
administrasjonskonsollen, fjernes ved neste daglige synkronisering. Når Ansatte slipper
gruppen, slettes en gruppe Vantigo laget, i Google; en gruppe den fant, overlates til
organisasjonen.

### Bilder

Ansattes bilde av en person — HRs, eller personens eget — skrives til kontoen, beskåret
til kvadratet i midten og skalert til **96 × 96 piksler**, størrelsen Google beholder. Et
bilde Google avviser, eller et som ikke kan leses, er ingen feil: personens Konto-fane
sier *Katalogen avviste bildet.* og dashbordet sier fra til HR, til et annet bilde er
skrevet.

## Hva stegene for en som slutter gjør

Dagen etter at en persons siste arbeidsforhold slutter, lar kontoarbeideren i Ansatte
Vantigo **suspendere** kontoen først — alltid, uansett hva kontoreglene sier — og glemme
et første passord ingen har vist. Deretter, i rekkefølge, hvert steg kontoreglene slår
på:

1. **Fjern lisensene** — hver lisens Vantigo tildelte, og hver av organisasjonens
   lisenser (slik de sist ble lest) personen har. Google fakturerer lisensen til en
   suspendert bruker, så dette er på med mindre det slås av.
2. **Ta dem ut av grupper** — *Gruppene Vantigo har laget* (standard), *Alle grupper*
   personen er i, eller *Ingen grupper*.
3. **Logg dem ut av alle økter** — Googles *sign out*. Avviser Google det, sier
   personens Konto-fane at personen som sluttet, ikke ble logget ut; suspenderingen
   holder dem ute allerede.

Et steg som feiler, overlater resten til neste kjøring, og personens Konto-fane
navngir det. En som ansettes på nytt senere, åpner den samme kontoen igjen.

## Slett etter N dager

Vantigo sletter Google-kontoen til en som har sluttet, bare når Ansattes **Slett en
ekstern konto (dager etter slutt)** sier det — den samme innstillingen som for Microsoft
Entra ID, under `staff:manage` i Ansattes innstillinger › **Kontoer**, tom for aldri
([Ansatte](/nb/admin/staff/#kontoarbeideren)). Kontoen må være suspendert. Google holder
en slettet bruker gjenopprettbar fra administrasjonskonsollen i 20 dager. Modulen har
ingen egen sletteinnstilling.

## Kontoer og grupper Vantigo ikke opprettet

En konto Google allerede har under en persons ansattnummer, og som Vantigo ikke
opprettet — en person som hadde Google før Vantigo — kobles til personen, men **skrives
ikke til**: ingen profil, intet passord, ingen suspendering, ingen sletting. Raden på
fanen **Kontoer** sier *Funnet – bekreft*, og dashbordet i Ansatte sier *Katalogen har en
konto under ansattnummeret til … som Vantigo ikke opprettet*.

1. På fanen **Kontoer** sjekker du at adressen og ansattnummeret er personens.
2. Velg **Bekreft** ved siden av kontoen, og bekreft.

Fra da av forvalter Vantigo kontoen som en den laget, og Ansattes neste kjøring bringer
den i samsvar med personen. En konto med en administratorrolle i Google skrives aldri
til, bekreftet eller ikke: gi personen en egen konto, eller håndter deres for hånd.

En gruppe Google allerede har på adressen en gruppe i Ansatte ville fått, og som Vantigo
ikke laget, venter på samme måte: **Bekreft** den ved siden av gruppen på fanen
**Kontoer**. Å bekrefte gir gruppen til Ansatte, og Vantigo **avstemmer** den ved neste
kjøring: navnet, beskrivelsen og synligheten overskrives med dem fra Ansatte, eiere
Ansatte ikke nevner, gjøres til medlemmer, og medlemmer gruppen i Ansatte ikke har,
fjernes. Bekreft bare en gruppe som skal være denne gruppen i Ansatte. En bekreftet
gruppe skrives til, men slettes aldri.

## Roter en nøkkel

Nøkler for tjenestekontoer utløper ikke. Roter hvert år, og med en gang hvis en nøkkel
kan ha lekket.

1. På tjenestekontoen: **Keys** › **Add key** › **Create new key** › **JSON**.
2. Last den opp i steg 2. Den nyeste aktive nøkkelen brukes, så **I bruk** flytter seg
   til den.
3. Test tilkoblingen.
4. Velg **Trekk tilbake** på den gamle nøkkelen, bekreft, og slett nøkkelen i Google
   Cloud-konsollen: å trekke den tilbake gjør at Vantigo slutter å bruke den, men endrer
   ingenting hos Google.

Hver nøkkel Vantigo har lagret, er aktiv til den trekkes tilbake. Når Google avviser den
nyeste (slettet i konsollen, for eksempel), merker Vantigo den *Google avviste den* og
hendelsen *Google avviste en nøkkel*, av Vantigo — én gang, til nøkkelen virker igjen — og
bruker den eldre; når Google avviser alle, **Feiler** tilkoblingen. En nøkkel som virker
igjen, mister merket. En adresse Google ikke kjenner, holdes ikke mot nøklene.

## Bytt tjenestekonto

Delegeringen er gitt til én tjenestekontos klient-ID, så Vantigo avviser en nøkkel til
en annen tjenestekonto mens en av den nåværendes er aktiv (*Denne nøkkelen tilhører en
annen tjenestekonto*, `client_mismatch`).

1. Opprett den nye tjenestekontoen og nøkkelen, og legg i administrasjonskonsollen til
   en oppføring for domenedekkende delegering for klient-ID-en dens med de samme
   tilgangsområdene.
2. I steg 2 velger du **Trekk tilbake** på hver nøkkel til den nåværende tjenestekontoen.
   Tilkoblingen er *Ikke satt opp* til en nøkkel er lastet opp.
3. Last opp den nye nøkkelen, og test tilkoblingen.
4. Fjern den gamle klientens delegering i administrasjonskonsollen, og slett den gamle
   tjenestekontoen eller nøklene dens.

## Hva som kan gå galt

Fanen **Status** viser statusen og årsaken; hendelsene under den sier hva som ble gjort,
og av hvem.

| Hva du ser | Betyr | Gjør |
| --- | --- | --- |
| **Ikke satt opp** | Ingen aktiv nøkkel, eller ingen administrator å etterligne — eller administratoren ble nettopp endret. | Fullfør steg 2 og 4, og test. |
| **Venter på delegering** (`not_delegated`) | Google avviste alle tilgangsområdene: delegeringen er ikke lagt inn, er lagt inn for en annen klient-ID, eller er ikke tatt i bruk ennå. | Sjekk **Client ID** og tilgangsområdene i administrasjonskonsollen; vent opptil 24 timer, og på en annen superadministrators godkjenning ved godkjenning fra flere parter; test igjen. |
| **Tilkoblet**, med tilgangsområder oppført som avvist | Noen av tilgangsområdene er delegert, andre ikke. | Legg de manglende til i klientens delegering, og test igjen. |
| **Feiler** (`credential_invalid`) | Google avviste alle nøklene — slettet eller deaktivert i Cloud-konsollen. En nøkkel forseglet under en annen `APP_SECRET` kan heller ikke åpnes. | Last opp en ny nøkkel hvis den gamle er borte; test igjen. |
| **Feiler** (`admin_refused`) | Google kjenner ikke administratoren som etterlignes — adressen er feilskrevet, ikke i organisasjonen, eller brukeren er suspendert — eller tokenet ble gitt, men Admin SDK svarte 403: administratoren har ikke lov til å lese organisasjonen, eller Admin SDK API er ikke slått på i prosjektet. Nøklene merkes ikke. | Sjekk adressen i steg 4; oppgi en superadministrator, eller gi administratorens rolle rett til å lese brukere og domener; slå på **Admin SDK API**; test igjen. |
| **Feiler** (`google_unreachable`) | Google svarte ikke, eller svarte med feil to ganger. | Sjekk utgående https til vertene [over](#før-du-begynner) fra hver prosess, og test igjen. |
| *Denne nøkkelen tilhører en annen tjenestekonto* (`client_mismatch`) | Den opplastede nøkkelen tilhører en annen tjenestekonto enn de aktive nøklene. | Last opp en nøkkel til den nåværende kontoen, eller [bytt tjenestekonto](#bytt-tjenestekonto). |
| *Tilgangsområder Vantigo ikke bruker* | Delegeringen har tilgangsområder Vantigo aldri ber om. | Fjern dem fra delegeringen med mindre et annet produkt trenger dem. |
| Konsollen sier at det er slått av å lage nøkler | Organisasjonen håndhever `iam.disableServiceAccountKeyCreation`. | [Opphev den for dette prosjektet](#opprett-tjenestekontoen-og-nøkkelen). |
| Kortet **Kontoer** i Ansatte: Google Workspace feiler (`scopes_missing`) | Den siste testen ble nektet et av tilgangsområdene for kontoene, gruppene, gruppeinnstillingene eller lisensene, eller ble gjort før de trengtes. | Legg dem til i delegeringen, og test igjen. |

**Årsakene på en persons konto.** Ansattes Konto-fane og dashbord sier hva Google ikke
fikk gjort, med disse kodene:

| Kode | Betyr | Gjør |
| --- | --- | --- |
| `external_ambiguous` | To kontoer i Google har personens ansattnummer. | Fjern nummeret fra den som ikke er deres, i administrasjonskonsollen. |
| `external_address_taken` | Personens e-post på jobb er en annen kontos, eller en gruppes. | Endre e-posten på jobb i Ansatte, eller frigjør adressen. |
| `external_unconfirmed`, `adoption_unconfirmed` | En konto Vantigo ikke opprettet, har nummeret. | [Bekreft den](#kontoer-og-grupper-vantigo-ikke-opprettet). |
| `external_privileged`, `privileged` | Kontoen har en administratorrolle i Google. | Håndter den for hånd, eller gi personen en annen konto. |
| `external_enabled` | En sletting ble bedt om for en konto som ikke er suspendert. | Ingenting: suspenderingen av en som slutter, kommer først, og slettingen følger. |
| `pass_not_allowed` | Organisasjonens passordregler avviste det første passordet. | Tilbakestill passordet i administrasjonskonsollen, eller lemp på reglene. |
| `pass_not_set_used` | Noen har logget inn på kontoen, så det ble ikke satt noe nytt passord da suspenderingen ble opphevet. | Ingenting, om personen kan passordet sitt; ellers tilbakestill det i administrasjonskonsollen. |
| `licence_unavailable` | Det er ingen lisens igjen av den SKU-en å tildele. | Kjøp flere, eller frigjør en; Ansatte prøver igjen. |
| `manager_not_found` | Lederen Ansatte navngir, har ingen konto i Google. | Gi lederen typen Google Workspace. |
| `photo_not_written` | Google avviste bildet, eller det kunne ikke leses. | Legg inn et annet bilde. |
| `sign_out_refused` | Google nektet å logge ut personen som sluttet. | Ingenting for suspenderingen; logg dem ut i administrasjonskonsollen om nødvendig. |
| `leaver_step_failed` | Et steg for en som slutter, ble ikke fullført (`remove_licences`, `remove_groups`, `sign_out`). | Ansatte prøver det igjen; sjekk tilkoblingen hvis det fortsetter å feile. |

Vantigo tar aldri vare på Googles egen feiltekst, bare statusen og Googles årsakskode;
årsaken på fanen **Status** er Vantigos tolkning av dem.
