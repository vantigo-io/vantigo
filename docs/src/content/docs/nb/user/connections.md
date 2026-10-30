---
title: Tilkoblinger
description: Å koble din egen Outlook til Vantigo under Innstillinger → Tilkoblinger — hvem som ser det, hvordan påloggingen hos Microsoft ser ut, Tilkoblet som, å koble fra, Må kobles til på nytt og årsakene, og hva hver melding betyr når en tilkobling feiler.
sidebar:
  order: 2
sources:
  - apps/host/frontend/src/routes/settings/connections.tsx
  - apps/entra/frontend/src/components/outlook-connection.tsx
---

**Innstillinger → Tilkoblinger** viser kontoene andre steder som du kobler til Vantigo.
I dag er det **Outlook**-en din: du kobler den til én gang ved å logge på Microsoft, og
Vantigo beholder tilkoblingen til du kobler den fra, slutter, eller administratoren din
slår den av. Kalenderens
[Outlook-synkronisering](/nb/user/calendar/#synkroniser-med-outlook) bruker den: Vantigo
skriver kopiene av kalenderen din til postboksen din, og leser opptatt-tiden din tilbake.
Kommunikasjon bruker den også for å la deg sende meldinger fra din egen adresse
([Send fra din egen adresse](/nb/user/communications/#personlig-sending-og-ny-prøving)),
mens kundens svar havner i den delte kanalen.

## Hvem som ser den

**Tilkoblinger** står under **Innstillinger** i avatar-menyen, ved siden av **Profil**
og **Sikkerhet**, så lenge organisasjonen kjører Microsoft Entra ID-modulen; uten den
finnes ikke siden. Alle som er pålogget, ser sin egen tilkobling og ingen andres; ingen
tillatelse trengs.

Kortet **Outlook** tilbyr en tilkobling bare når to ting stemmer:

- en eier har slått på **Outlook-tilkobling** på Microsoft Entra ID-siden
  ([Microsoft Entra ID](/nb/user/entra/#outlook-tilkobling)); og
- HR har koblet personen din i Ansatte til en Microsoft-konto Vantigo forvalter — en den
  opprettet, eller som noen har bekreftet adoptert. Vantigo gjetter aldri kontoen din ut
  fra adressen.

Ellers sier kortet *Ingenting å koble til: Outlook-tilkoblingen er slått av, eller
Vantigo kjenner ingen Microsoft-konto av deg.*

## Koble til Outlook-en din

1. Åpne **Innstillinger → Tilkoblinger**. Kortet **Outlook** sier **Ikke tilkoblet**.
2. Velg **Koble til Outlook**. Nettleseren går til Microsofts pålogging, med jobbkontoen
   din fylt inn. Er du allerede logget på Microsoft 365, kommer den som regel rett
   tilbake uten å vise noen side; ellers logger du på som du pleier, med
   flerfaktorautentisering hvis organisasjonen ber om det.
3. Tilbake i Vantigo sier kortet *Fullfører tilkoblingen til Outlook …*, så **Outlook er
   koblet til**, og sender deg tilbake dit du startet.

Du kan også koble til fra kortet **Outlook** under **Kalender → Min kalender**, eller
direkte fra skrivefeltet i **Kommunikasjon → Innboks**, som viser den samme knappen
og sender deg tilbake dit.

Logg på med **kontoen Vantigo kjenner deg ved**: den HR har koblet til personen din.
Logger du på som noen annen — en annen konto, en personlig Microsoft-konto, en konto fra
en annen organisasjon — avvises det, og ingenting kobles til.

## Tilkoblet

Kortet sier **Tilkoblet som** og kontoen din, **Siden** dagen du koblet til, og
**Brukes av:** funksjonene tilkoblingen tjener, for eksempel *Kalender* og *E-post*.

## Koble fra

Velg **Koble fra**, og så **Koble fra** i *Koble fra Outlook?*. Vantigo slutter å bruke
Outlook-en din til du kobler den til igjen, og kortet sier **Ikke tilkoblet**. Å koble
fra logger deg ikke ut av Microsoft eller Vantigo. **Vantigo fjerner det kalenderens
Outlook-synkronisering la inn i Outlook-en din**, med tilkoblingen du nettopp lukket, og
synkroniseringen venter til du kobler til igjen; bryteren dens blir stående slik du
forlot den.

## Må kobles til på nytt

Microsoft kan avvise en tilkobling senere — påloggingen utløp, passordet ble endret,
organisasjonens regler ble endret. Kortet sier da **Må kobles til på nytt** med årsaken,
og knappen **Koble til Outlook på nytt** kobler til igjen på samme måte:

| Kortet sier | Hvorfor |
| --- | --- |
| *Påloggingen din hos Microsoft har utløpt.* | Tilkoblingens pålogging hos Microsoft utløp — ubrukt for lenge, eller avsluttet av Microsoft. |
| *Microsoft ber deg logge på igjen.* | Passordet ble endret, øktene dine ble tilbakekalt, eller Microsoft vil ha en ny pålogging av en annen grunn. |
| *Microsoft ber deg fullføre flerfaktorautentisering.* | Organisasjonen ber nå om en faktor til. |
| *Organisasjonens betingede tilgang (Conditional Access) stoppet tilkoblingen. Spør administratoren din om det skjer igjen.* | En policy for betinget tilgang avviste den — en regel for enhet, sted eller risiko. |
| *Organisasjonen har ikke gitt samtykke til en tillatelse tilkoblingen trenger. Spør administratoren din.* | Administratorsamtykke for tilkoblingens tillatelser mangler. |

Bare du ser dette; det er ikke administratorens feil. **Kalenderens
Outlook-synkronisering tar en pause** til du kobler til på nytt — det den la inn i
Outlook, blir som det var — og fortsetter der den slapp når du har gjort det; ingenting
annet i Vantigo stopper. Administratoren ser hvor mange som venter på å koble til på nytt, på Microsoft
Entra ID-siden.

Slutter du, eller blir Microsoft-kontoen din deaktivert eller slettet, lukkes
tilkoblingen for deg. Det kalenderens Outlook-synkronisering la inn i Outlook-en din,
blir da liggende i postboksen din, som deaktiveres sammen med kontoen din.

## Når tilkoblingen feiler

Når en tilkobling ikke kan fullføres, sier kortet **Outlook kunne ikke kobles til** og
hvorfor:

| Kortet sier | Gjør |
| --- | --- |
| *Påloggingen hos Microsoft ble avbrutt eller avvist. Ingenting ble koblet til.* | Koble til på nytt, og godta på Microsofts side. |
| *Du logget på med en konto fra en annen organisasjon. Koble til på nytt og logg på med jobbkontoen din.* | Logg ut av den andre kontoen hos Microsoft, eller velg jobbkontoen din. |
| *Tilkoblingen tok for lang tid, eller ble startet i en annen økt. Koble til på nytt.* | Koble til på nytt, og fullfør innen ti minutter i den samme nettleseren og den samme fanen: en ny tilkobling som startes, avbryter en som ikke er fullført. |
| *Svaret fra Microsoft kunne ikke brukes. Prøv igjen om litt.* | Prøv igjen; skjer det stadig, si fra til administratoren. |
| *Organisasjonen har ikke gitt samtykke til tillatelsene tilkoblingen trenger. Be administratoren din gi administratorsamtykke.* | Administratoren gir administratorsamtykke ([administrasjonsveiledningen](/nb/admin/entra/#outlook-tilkobling)). |
| *Du logget på Microsoft med en annen konto enn den Vantigo kjenner deg ved. Koble til på nytt og logg på med den kontoen.* | Logg på med jobbkontoen din; spør HR hvis Vantigo kjenner deg ved feil konto. |
| *En annen person i Vantigo har allerede koblet til denne Microsoft-kontoen.* | Si fra til administratoren: kontoen er koblet til en annens bruker. |
| *Vantigo kjenner ingen Microsoft-konto av deg som den forvalter, så det er ingenting å koble til.* | Be HR koble personen din i Ansatte til Microsoft-kontoen din. |
| *Outlook-tilkoblingen er slått av for dette arbeidsområdet.* | Be en eier slå den på. |

Viser Microsoft sin egen feilside i stedet for å komme tilbake — for eksempel en som
nevner `AADSTS50011` eller sier at godkjenning kreves — er ikke Vantigos registrering i
organisasjonen ferdig: si fra til administratoren.
