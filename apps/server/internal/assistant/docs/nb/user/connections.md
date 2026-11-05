---
title: Tilkoblinger
description: Å koble din egen Outlook eller Google-konto til Vantigo under Innstillinger → Tilkoblinger — hvem som ser hvert kort, hvordan påloggingen hos Microsoft eller Google ser ut, funksjonene, Tilkoblet som, å koble fra, Må kobles til på nytt og årsakene, og hva hver melding betyr når en tilkobling feiler.
sidebar:
  order: 2
sources:
  - apps/host/frontend/src/routes/settings/connections.tsx
  - apps/host/frontend/src/lib/connections.tsx
  - apps/host/frontend/src/components/connection-cards.tsx
  - apps/host/frontend/src/components/entra-connection-card.tsx
  - apps/entra/frontend/src/components/outlook-connection.tsx
  - apps/google/frontend/src/components/google-connection.tsx
---

**Innstillinger → Tilkoblinger** viser kontoene andre steder som du kobler til Vantigo,
ett kort for hver leverandør organisasjonen din kjører: **Outlook**-en din, fra Microsoft
Entra ID-modulen, og **Google**-kontoen din, fra Google Workspace-modulen
([Koble til Google-kontoen din](#koble-til-google-kontoen-din)). Outlook kobler du til én gang ved å logge på Microsoft, og
Vantigo beholder tilkoblingen til du kobler den fra, slutter, eller administratoren din
slår den av. Kalenderens
[Outlook-synkronisering](/nb/user/calendar/#synkroniser-med-outlook) bruker den: Vantigo
skriver kopiene av kalenderen din til postboksen din, og leser opptatt-tiden din tilbake.
Kommunikasjon bruker den også for å la deg sende meldinger fra din egen adresse
([Send fra din egen adresse](/nb/user/communications/#personlig-sending-og-ny-prøving)),
mens kundens svar havner i den delte kanalen.

## Hvem som ser den

**Tilkoblinger** står under **Innstillinger** i avatar-menyen, ved siden av **Profil**
og **Sikkerhet**, så lenge organisasjonen kjører minst én modul som tilbyr en tilkobling —
Microsoft Entra ID-modulen eller Google Workspace-modulen; uten en slik finnes ikke
siden. Siden viser ett kort for
hver slik modul som er slått på, i samme rekkefølge hver gang. Alle som er pålogget, ser
sine egne tilkoblinger og ingen andres; ingen tillatelse trengs.

Kortet **Outlook** tilbyr en tilkobling bare når to ting stemmer:

- en eier har slått på **Outlook-tilkobling** på Microsoft Entra ID-siden
  ([Microsoft Entra ID](/nb/user/entra/#outlook-tilkobling)); og
- HR har koblet personen din i Ansatte til en Microsoft-konto Vantigo forvalter — en den
  opprettet, eller som noen har bekreftet adoptert. Vantigo gjetter aldri kontoen din ut
  fra adressen.

Ellers vises ikke kortet **Outlook**. Når ingen leverandørs kort har noe å tilby deg,
sier siden *Ingenting å koble til: ingen tilkobling er slått på for deg, eller Vantigo
kjenner ingen konto av deg å koble til.*

## Koble til Outlook-en din

1. Åpne **Innstillinger → Tilkoblinger**. Kortet **Outlook** sier **Ikke tilkoblet**.
2. Velg **Koble til Outlook**. Nettleseren går til Microsofts pålogging, med jobbkontoen
   din fylt inn. Er du allerede logget på Microsoft 365, kommer den som regel rett
   tilbake uten å vise noen side; ellers logger du på som du pleier, med
   flerfaktorautentisering hvis organisasjonen ber om det.
3. Tilbake i Vantigo sier kortet *Fullfører tilkoblingen til Outlook …*, så **Outlook er
   koblet til**, og sender deg tilbake dit du startet.

Du kan også koble til fra kortet **Outlook** under **Kalender → Min kalender**, eller
direkte fra skrivefeltet i **Kommunikasjon → Innboks**, som viser den samme knappen —
én for hver leverandør som tilbyr deg en tilkobling — og sender deg tilbake dit.

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

## Koble til Google-kontoen din

Kortet **Google** er det samme for et selskap på Google Workspace: du kobler din egen
Google-konto til én gang, og Vantigo beholder tilkoblingen til du kobler den fra,
slutter, eller administratoren din slår den av. To funksjoner bruker den, hver med sitt
eget samtykke på Googles side: **Kalender** — kalenderens
[synkronisering med Google Kalender](/nb/user/calendar/#synkroniser-med-google-kalender) — og
**Gmail** — å sende en melding i Kommunikasjon fra din egen Gmail-adresse
([Send fra din egen adresse](/nb/user/communications/#personlig-sending-og-ny-prøving)), mens
kundens svar havner i den delte kanalen. Gmail lar Vantigo **sende** som deg og ingenting
annet: Vantigo leser aldri postboksen din.

### Legg Gmail til tilkoblingen

Å koble til fra kalenderen ber bare om kalenderen. For å sende fra Gmail-adressen din velger
du **Koble til Google** der Kommunikasjon tilbyr det — ved **Send fra min adresse** i
skrivefeltet, eller i innboksen — og tillater sending på Googles side. Google spør om
kalenderen på nytt også, så tilkoblingen beholder begge; fjerner du haken for et punkt, gis
ikke den funksjonen, og knappen blir stående. Når Gmail er gitt, viser kortets **Brukes av:**
*Gmail*, og skrivefeltet tilbyr **Send fra min Gmail-adresse (`du@dittfirma.no`)**.

Kortet **Google** tilbyr en tilkobling bare når to ting stemmer:

- en eier har slått på **Tilkobling for personer** på fanen **Kalender og Gmail** på
  Google Workspace-siden ([Google Workspace](/nb/user/google-workspace/#fanen-kalender-og-gmail)),
  og organisasjonens nettklient er satt opp; og
- HR har koblet personen din i Ansatte til en Google-konto Vantigo forvalter — en den
  opprettet, eller som noen har bekreftet adoptert — og den kontoen er ikke suspendert.

Ellers vises ikke kortet **Google**.

1. Åpne **Innstillinger → Tilkoblinger**. Kortet **Google** sier **Ikke tilkoblet**.
2. Velg **Koble til Google**. Nettleseren går til Google, med jobbkontoen din fylt inn.
   Logg på hvis Google ber om det, og tillat det Vantigo ber om på Googles side. Google
   viser alltid denne siden, også når du har koblet til før; for organisasjonens egen app
   er det ett klikk. Lister Google punktene ett for ett, **tillat hvert av dem**: Vantigo
   ber bare om det funksjonen trenger.
3. Tilbake i Vantigo fullfører kortet tilkoblingen og sender deg tilbake dit du startet.

Du kan også koble til fra komponisten eller kalenderen der en **Koble til Google**-knapp
står ved siden av **Koble til Outlook**; den bringer deg tilbake dit.

Logg på med **kontoen Vantigo kjenner deg som**: den HR koblet til personen din. Logger
du på som noen annen — en annen konto i organisasjonen, en privat Gmail-konto, en konto
fra en annen organisasjon — avvises det, og ingenting kobles til.

### Tilkoblet til Google

Kortet sier **Tilkoblet som** og adressen til kontoen din, **Siden** dagen du koblet til,
og **Brukes av:** funksjonene tilkoblingen tjener: *Kalender*, *Gmail* eller begge. Velg **Koble fra** og bekreft for å stoppe:
Vantigo slutter å bruke Google-kontoen din til du kobler til igjen, og ber senere, når
ingenting trenger den, Google glemme tilkoblingen. Å koble fra logger deg ikke ut av
Google eller Vantigo. Du kan også avslutte tilkoblingen selv på Google-kontoens
sikkerhetsside, under appene med tilgang til kontoen; Vantigo viser da **Må kobles til
på nytt** neste gang den bruker tilkoblingen.

### Må kobles til på nytt (Google)

| Kortet sier | Hvorfor |
| --- | --- |
| *Google godtar ikke lenger tilkoblingen: den ble trukket tilbake, den utløp, eller du byttet passord. Koble til på nytt.* | Du fjernet Vantigos tilgang i Google-kontoen din, tilkoblingen var ubrukt i seks måneder, eller et passordbytte avsluttet den. |
| *Organisasjonens Google-regler blokkerer tilkoblingen. Spør Google Workspace-administratoren din.* | En administrator begrenset Vantigos nettklient i administrasjonskonsollen, eller kontoen din er utenfor organisasjonen appen er for. |
| *Google ga ikke alt Vantigo ba om. Koble til på nytt og tillat hvert punkt på Googles side.* | Du, eller en endring hos Google, fjernet en del av det tilkoblingen trenger. En melding du holdt på å sende fra Gmail, mislykkes da med *Sendingen mislyktes fordi e-posttilkoblingen din må kobles til på nytt.*; koble til på nytt og velg **Prøv å sende på nytt**. |
| *Logg på Google igjen for å beholde tilkoblingen.* | Google avviste tilkoblingen av en annen grunn. |

Bare du ser dette. Administratoren din ser hvor mange som venter på å koble til på nytt
på Google Workspace-siden. Slutter du, eller blir Google-kontoen din suspendert eller
slettet, lukkes tilkoblingen for deg og glemmes.

### Når tilkoblingen til Google feiler

Kortet sier **Google kunne ikke kobles til** og hvorfor:

| Kortet sier | Gjør |
| --- | --- |
| *Du avbrøt på Googles side.* | Koble til på nytt, og tillat på Googles side. |
| *Du logget på med en konto utenfor organisasjonens Google Workspace.* | Velg jobbkontoen din på Googles side, ikke en privat Gmail-konto. |
| *Google ga ikke alt Vantigo ba om. Koble til på nytt og tillat hvert punkt på Googles side.* | Koble til på nytt og la alle punktene være krysset av. |
| *Tilkoblingen tok for lang tid, eller ble startet i en annen økt. Koble til på nytt.* | Koble til på nytt, og bli ferdig innen ti minutter i samme nettleser. |
| *Svaret fra Google kunne ikke brukes. Prøv igjen om litt.* | Prøv igjen; skjer det igjen, si fra til administratoren din. |
| *Du logget på Google med en annen konto enn den Vantigo kjenner deg som. Koble til på nytt og velg den kontoen.* | Velg jobbkontoen din på Googles side; spør HR om Vantigo kjenner deg som feil konto. |
| *En annen person i Vantigo har allerede koblet til denne Google-kontoen.* | Si fra til administratoren din: kontoen er koblet til en annens bruker. |
| *Vantigo kjenner ingen Google-konto av deg som den forvalter, så det er ingenting å koble til.* | Be HR koble personen din i Ansatte til Google-kontoen din. |
| *Tilkobling til Google er slått av for dette arbeidsområdet.* | Be en eier slå på **Tilkobling for personer**. |

Viser Google sin egen feilside i stedet for å komme tilbake — for eksempel
`redirect_uri_mismatch` eller *Access blocked* — er organisasjonens nettklient ikke satt
opp for Vantigo: si fra til administratoren din.
