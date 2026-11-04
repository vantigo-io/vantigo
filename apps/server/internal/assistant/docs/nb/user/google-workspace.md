---
title: Google Workspace
description: Å koble til selskapets Google Workspace-organisasjon på siden Google Workspace under Integrasjoner i Administrer arbeidsområde — de fire fanene Status, Tilkobling, Kontoer og Kalender og Gmail, hvor siden er, de seks stegene på Tilkobling og hva Ferdig betyr på hvert, nøkkelen som aldri vises igjen, klient-ID-en og tilgangsområdene som skal kopieres, administratoren som etterlignes, Test tilkoblingen, den interne nettklienten og omdirigerings-URI-ene, fanen Status med tilgangsområder og hendelser, varselet om tilgangsområder Vantigo ikke bruker — og hvem som ser hva.
sidebar:
  order: 48
sources:
  - apps/google/frontend
  - apps/host/frontend/src/routes/workspace/google
  - apps/host/frontend/src/routes/workspace/google.tsx
---

Siden **Google Workspace**, under **Integrasjoner** i Administrer arbeidsområde, kobler
Vantigo til selskapets Google Workspace-organisasjon gjennom en tjenestekonto med
domenedekkende delegering. I denne versjonen leser Vantigo organisasjonen og de
bekreftede domenene gjennom tilkoblingen; å holde personenes Google-kontoer og grupper,
hver persons egen Google-tilkobling, kalendersynkronisering og Gmail kommer senere, på den
samme tilkoblingen. Driftsprosedyren, med hva du gjør i Googles konsoller, står i
[administrasjonsveiledningen](/nb/admin/google-workspace/); reglene står i
[referansen](/en/reference/google/).

## Hvem ser hva

| Hva | Hvem |
| --- | --- |
| **Google Workspace** under **Administrer arbeidsområde** › **Integrasjoner**, fanen **Status** med tilgangsområder og **Siste hendelser**, og alle stegene skrivebeskyttet | `google:manage` |
| **Last opp nøkkel**, **Trekk tilbake**, å lagre administratoren som etterlignes, **Test tilkoblingen** og å lagre nettklienten | en **eier**, innlogget med en andre faktor når eiere må bruke en; for alle andre med `google:manage` vises stegene med *En eier gjør dette steget. Du kan se hva som er satt, men ikke endre det.* |

`google:manage` (*Administrere Google Workspace*) er i kategorien Administrasjon og kan
ikke delegeres: bare en eier gir den til en rolle. Eieren har den allerede. En som har
den uten å være eier, ser bare delen **Integrasjoner** av Administrer arbeidsområde
([Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#integrasjoner)),
og finner **Google Workspace** i avatarmenyen også.

## Hvor den er, og de fire fanene

Åpne avatarmenyen øverst til høyre, velg **Administrer arbeidsområde**, og velg **Google
Workspace** under **Integrasjoner** i sidemenyen. Siden er ikke i appvelgeren; adressen er
`/workspace/google`. Den er der bare mens Google Workspace-modulen er slått på i denne
installasjonen.

Siden har fire faner, hver med sin egen adresse, slik at det kan lenkes til en fane:

| Fane | Hva den har |
| --- | --- |
| **Status** | Kortet **Status** — statusen, organisasjonen, tilgangsområdene — og **Siste hendelser** |
| **Tilkobling** | De seks stegene som setter opp tilkoblingen |
| **Kontoer** | *Kontoer kommer*: Ansatte skal opprette og vedlikeholde Google-kontoene og -gruppene til personene deres her, og koble eksisterende brukere til Google-innloggingen sin |
| **Kalender og Gmail** | *Kalender og Gmail kommer*: hver persons egen Google-tilkobling, kalendersynkronisering, e-postkanaler i Gmail og sending fra en persons Gmail-adresse skal settes opp her |

Mens ingenting er satt opp og ingen nøkkel er lastet opp, åpner siden på **Tilkobling**.
Deretter åpner den på **Status**.

## Koble til organisasjonen

Fanen **Tilkobling** er seks kort, ett per steg, i den rekkefølgen du gjør dem. Et kort
sier hvor du skal gå i Googles konsoller og hva du skal kopiere. Fire av dem har et
merke — *Ferdig* når Vantigo har det steget trenger, *Ikke ferdig* til da; steg 1 og 3
skjer hos Google og har ikke noe eget merke: testen er beviset for dem.

1. **Opprett prosjektet, tjenestekontoen og nøkkelen** — i Google Cloud-konsollen, under
   **IAM & Admin** › **Service accounts**. Kortet advarer om at policyen i en ny
   organisasjon kan forby nøkler for tjenestekontoer, og lenker til **Les veiledningen
   for å koble til Google Workspace**.
2. **Last opp nøkkelen** — lim inn **Innholdet i nøkkelfilen** eller **Velg
   nøkkelfilen**, og velg **Last opp nøkkel**. Filen leses i nettleseren din; Vantigo
   forsegler den private nøkkelen og viser den aldri igjen. Nøklene står under **Nøkler
   for tjenestekontoen** med **Tjenestekonto**, **Klient-ID**, **Nøkkel-ID**, når den ble
   **Lagt til** og **Tilstand**: **I bruk** for den Vantigo bruker, *Google avviste den*
   og datoen for en Google avviste, *Trukket tilbake* og datoen. **Trekk tilbake** gjør at
   Vantigo slutter å bruke en nøkkel med en gang; det endrer ingenting hos Google, så slett
   nøkkelen i Google Cloud-konsollen også. *Ferdig* når en nøkkel er lastet opp.
3. **Deleger tilgangsområdene** — i administrasjonskonsollen, under **Security** ›
   **Access and data control** › **API controls** › **Domain-wide delegation**. Kortet
   viser tjenestekontoens **Klient-ID** (når en nøkkel er lastet opp) og
   **OAuth-tilgangsområder (kommadelt)**, hver med **Kopier**. Delegeringen kan ta opptil
   24 timer før den virker, og med godkjenning fra flere parter må en annen
   superadministrator godkjenne den.
4. **Oppgi administratoren som skal etterlignes** — **Administratorens adresse**: en
   superadministrator, eller en administrator med en rolle som kan administrere brukere
   og grupper. Vantigo leser organisasjonen som denne administratoren. **Lagre**;
   *Ferdig* når en administrator er oppgitt. Lagres en annen administrator, går statusen
   tilbake til **Ikke satt opp**, og tilgangsområdene den siste testen fant, glemmes, til
   neste test.
5. **Test tilkoblingen** — **Test tilkoblingen** ber Google om et token for hvert
   tilgangsområde som administratoren, leser deretter organisasjonen og domenene, og viser
   **Testresultat**: statusen, **Organisasjon**, **Kunde-ID**, **Primærdomene** og
   **Bekreftede domener**. Den trenger en nøkkel og en administrator først. *Ferdig* når
   den siste testen fant tilkoblingen **Tilkoblet** med alle tilgangsområdene gitt.
6. **Sett opp den interne nettklienten** — i Google Cloud-konsollen, under **Google Auth
   Platform** › **Clients**: en OAuth-klient av typen Web application, med samtykkeskjermens
   målgruppe satt til *Internal*, og begge **Godkjente omdirigerings-URI-er** kortet viser,
   hver med **Kopier**. Skriv inn **Klient-ID** og **Klienthemmelighet** og velg
   **Lagre**. Google viser en klienthemmelighet bare én gang, når den opprettes; Vantigo
   forsegler den og viser den aldri igjen. La hemmeligheten stå tom for å beholde den som
   er der; en ny klient trenger hemmeligheten sin. *Ferdig* når en klient og
   hemmeligheten er lagret. Ingenting bruker nettklienten ennå: hver persons
   Google-tilkobling og innloggingen med Google kommer til å gjøre det.

Har noen andre lagret tilkoblingen mens du hadde den åpen, sier Vantigo fra, laster den
inn på nytt, og du gjør endringen din igjen.

## Fanen Status

Kortet **Status** viser:

- statusen: **Ikke satt opp** (ingen nøkkel, eller ingen administrator), **Venter på
  delegering** (Google har ikke gitt tilgangsområdene ennå), **Tilkoblet** eller
  **Feiler**, og årsaken i en setning når det finnes en — Google avviste alle nøklene
  eller kjenner ikke administratoren, administratoren har ikke lov til å lese
  organisasjonen, eller Google svarte ikke;
- **Organisasjon**, **Kunde-ID**, **Primærdomene**, **Bekreftede domener** og **Sist
  testet**, slik den siste testen leste dem;
- **Tilgangsområder**: *Alle tilgangsområdene Vantigo trenger, er gitt.*, eller
  *Tilgangsområder Vantigo trenger som Google avviste* med listen, og **Gitte
  tilgangsområder**. Før den første testen sier den at tilgangsområdene sjekkes når
  tilkoblingen testes;
- **Tilgangsområder Vantigo ikke bruker**, en oransje melding når delegeringen har
  tilgangsområder Vantigo aldri ber om. Delegeringen når alle kontoene i organisasjonen,
  så fjern dem ingen trenger, i administrasjonskonsollen. Det er bare et varsel:
  tilkoblingen forblir **Tilkoblet**.

Mens ingenting er satt opp, viser en melding, *Ikke tilkoblet ennå*, til fanen
**Tilkobling**.

**Siste hendelser** viser hva som ble gjort, nyeste først, og av hvem — *av Vantigo* for
det Vantigo gjorde selv: administratoren endret, tilkoblingen testet, nettklienten endret,
en nøkkel lastet opp, en nøkkel eller en klienthemmelighet trukket tilbake, og at Google
avviste en nøkkel.
