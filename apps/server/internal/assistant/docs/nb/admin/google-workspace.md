---
title: Google Workspace
description: Å koble Vantigo til en Google Workspace-organisasjon gjennom en tjenestekonto med domenedekkende delegering — Google Cloud-prosjektet og Admin SDK API, tjenestekontoen og JSON-nøkkelen, å oppheve iam.disableServiceAccountKeyCreation for prosjektet, å laste opp nøkkelen, å delegere tilgangsområdene i administrasjonskonsollen og vente på dem, administratoren som skal etterlignes, den interne nettklienten og de to omdirigerings-URI-ene, å verifisere med Test tilkoblingen, å rotere en nøkkel, å bytte tjenestekonto, og hva som kan gå galt.
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
---

Google Workspace-modulen kobler Vantigo til selskapets Google Workspace-organisasjon.
Den logger inn som en **tjenestekonto** med **domenedekkende delegering**: Google lar
tjenestekontoen opptre som en administrator i organisasjonen, for de tilgangsområdene
en superadministrator delegerer til den, og ingenting annet. Denne versjonen er
grunnmuren: tilkoblingen, nøkkelen, testen og den interne nettklienten. Vantigo leser
organisasjonen og de bekreftede domenene; den holder ingen kontoer ennå. Å holde
personenes kontoer og grupper, hver persons egen Google-tilkobling,
kalendersynkronisering og Gmail kommer i senere versjoner, på den samme tilkoblingen.

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
  ingen og følger ingen omdirigering. Denne versjonen kaller bare de to første.
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
   **OAuth-tilgangsområder (kommadelt)**; kopier begge. Denne versjonen trenger to:
   - `https://www.googleapis.com/auth/admin.directory.customer.readonly`
   - `https://www.googleapis.com/auth/admin.directory.domain.readonly`
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
delegeringen.

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
hendelsen *Google avviste en nøkkel*, av Vantigo, og bruker den eldre; når Google
avviser alle, **Feiler** tilkoblingen.

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
| **Feiler** (`credential_invalid`) | Google avviste alle nøklene — slettet eller deaktivert i Cloud-konsollen — eller kjenner ikke administratoren som etterlignes: adressen er feilskrevet eller ikke i organisasjonen. En nøkkel forseglet under en annen `APP_SECRET` kan heller ikke åpnes. | Sjekk adressen i steg 4; last opp en ny nøkkel hvis den gamle er borte; test igjen. |
| **Feiler** (`admin_refused`) | Tokenet ble gitt, men Admin SDK svarte 403: administratoren har ikke lov til å lese organisasjonen, eller Admin SDK API er ikke slått på i prosjektet. | Oppgi en superadministrator, eller gi administratorens rolle rett til å lese brukere og domener; slå på **Admin SDK API**; test igjen. |
| **Feiler** (`google_unreachable`) | Google svarte ikke, eller svarte med feil to ganger. | Sjekk utgående https til vertene [over](#før-du-begynner) fra hver prosess, og test igjen. |
| *Denne nøkkelen tilhører en annen tjenestekonto* (`client_mismatch`) | Den opplastede nøkkelen tilhører en annen tjenestekonto enn de aktive nøklene. | Last opp en nøkkel til den nåværende kontoen, eller [bytt tjenestekonto](#bytt-tjenestekonto). |
| *Tilgangsområder Vantigo ikke bruker* | Delegeringen har tilgangsområder Vantigo aldri ber om. | Fjern dem fra delegeringen med mindre et annet produkt trenger dem. |
| Konsollen sier at det er slått av å lage nøkler | Organisasjonen håndhever `iam.disableServiceAccountKeyCreation`. | [Opphev den for dette prosjektet](#opprett-tjenestekontoen-og-nøkkelen). |

Vantigo tar aldri vare på Googles egen feiltekst, bare statusen og Googles årsakskode;
årsaken på fanen **Status** er Vantigos tolkning av dem.
