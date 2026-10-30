---
title: Kommunikasjon
description: Delte postkasser, samtaler med kunder, mottak og sending av e-post og blokkeringer — kanaler på SMTP eller på en delt postboks i Microsoft 365, e-post mottatt i en Microsoft 365-kanal, svar og svar alle, å bytte en kanal til Microsoft 365, og størrelsen en Microsoft 365-kanal sender.
sidebar:
  order: 80
sources:
  - apps/communications/frontend
  - apps/host/frontend/src/routes/communications
---

Kommunikasjon-appen er en delt innboks: alle e-postsamtaler organisasjonen din har
med kundene sine, på ett sted, besvart fra delte postkasser som kalles **kanaler**.
Sidemenyen har tre områder: **Innboks**, der samtaler leses og besvares; **Kanaler**,
der postkassene settes opp; og **Blokkeringer**, listen over adresser Vantigo nekter å
skrive til. Åpner du appen, havner du i Innboks, og dashbordet Hjem viser et
Kommunikasjon-kort med åpne, nye og lukkede samtaler.

E-post som kommer til den delte postboksen til en **Microsoft 365**-kanal, kommer inn i
Innboks av seg selv, som en samtale du kan lese og svare på. En **SMTP**-kanal sender
bare: ingen e-post kommer inn gjennom den, så samtalene dens inneholder bare det som er
sendt fra Vantigo og notatene som er skrevet i dem, og de kan ikke besvares. Hvordan
mottaket virker, er beskrevet under
[Receiving](/en/reference/communications/#receiving) i referansen.

## Finn en samtale

Åpne **Innboks**. Den venstre ruten lister samtalene, nyeste aktivitet først, med et
antall øverst. Hver rad viser motparten (navnet, eller adressen når navnet er ukjent),
når noe sist skjedde, emnet, en forhåndsvisning av den siste teksten, samtalens tagger
og statusen dens. En ulest samtale vises i fet skrift.

Verktøylinjen filtrerer listen:

- **Alle / Åpne / Lukkede / Arkiverte** velger en status; Alle er standard.
- **Uleste** beholder bare samtaler med noe du ikke har lest.
- Taggvelgeren (plassholder **Alle tagger**) beholder samtaler med én bestemt tagg.

Endrer du et filter, forsvinner den valgte samtalen, siden den kanskje ikke lenger er i
listen, og filtrene ligger i adressefeltet slik at en filtrert visning kan bokmerkes
eller deles med en kollega. Når ingenting samsvarer, ser du «Ingen samtaler samsvarer
med disse filtrene.» Feltet **Søk i samtaler** i verktøylinjen er ikke i bruk ennå.
Hakeikonet ved siden av antallet laster listen på nytt.

Når du åpner Innboks, velges den første samtalen i listen; klikk på en rad for å lese
en annen. Innboks krever `communications:conversations-view`.

## Les en samtale

En samtale samler alt som er utvekslet med én kunde om ett emne: deltakerne og emnet i
toppen, deretter meldingene i rekkefølge. Hver melding er merket **You** for en e-post
sendt fra Vantigo, **Internal note** for et notat en kollega har skrevet, og
avsenderens navn for en melding fra kunden (disse merkelappene vises på engelsk).
Klokkeslettet på hver melding, og på hver samtale i listen, er i din egen tidssone
([Velg tidssonen din](/nb/user/getting-started/#velg-tidssonen-din)).

**E-post fra kunder.** En e-post sendt til den delte postboksen til en Microsoft
365-kanal vises i Innboks innen omtrent ett minutt når endringsvarsler er på, eller innen
omtrent fem minutter ellers. Et svar på en tidligere melding
havner i samtalen til den meldingen; alt annet starter en ny samtale. En lukket samtale
kunden skriver til igjen, åpnes igjen; en arkivert samtale blir stående som den er, og
e-posten starter en ny samtale. Så lenge samtalen er åpen, blir meldingen liggende i den
delte postboksen i Outlook som den var; når du lukker samtalen, lar Vantigo den stå
uendret, markerer den som lest eller flytter den til en mappe i Outlook etter kanalens
innstilling **Når en samtale lukkes**.

- En melding skrevet som HTML vises formatert, i en sandkasse som blokkerer skript,
  skjemaer og nettverkstilgang. Bilder fra internett fjernes; bilder meldingen selv har
  med, vises. **Vis som tekst** viser meldingens rene tekst i stedet, og **Vis formatert**
  går tilbake.
- Et automatisk svar — et fraværssvar, for eksempel — er merket **Autosvar**, og åpner
  ikke en lukket samtale igjen.
- En melding Vantigo ikke kunne lese, sier *Vantigo kunne ikke lese denne meldingen.*;
  åpne den i e-postprogrammet ditt med **Last ned originalen**. **Meldingshandlinger** på
  enhver melding fra en kunde tilbyr også **Last ned originalen**, e-posten nøyaktig slik
  den kom, som en `.eml`-fil.

Et vedlegg vises som en lenke du kan laste ned. Et som kom med en e-post fra en kunde, er
merket *Ikke skannet. Åpne det bare hvis du stoler på avsenderen.* — Vantigo sjekker ikke
filer for virus — og lastes alltid ned som en fil, åpnes aldri i nettleseren. Et vedlegg
som venter på å bli sjekket, vises som **Skanner**, og et som ikke kan leveres, som
**Vedlegg utilgjengelig**.

Fra toppen kan du:

- endre **Status** (`open`, `closed` eller `archived`);
- åpne **Handlinger** → **Marker som lest**, som fjerner ulest-markeringen, eller
  **Arkiver**;
- legge til en tagg fra velgeren **Legg til tagg**, eller fjerne en med × på den. Bare
  tagger som allerede finnes, tilbys; Innboks oppretter ikke nye.

Å lese krever `communications:conversations-view`; å endre status og tagger krever i
tillegg `communications:conversations-manage`.

## Knytt en samtale til en kunde

Når en e-post fra en kunde kommer inn, slår Vantigo opp avsenderens adresse blant
kundenes kontakter. Hører den til **én kunde**, knyttes samtalen til den kunden av seg
selv; hører den til **flere**, listes de som kandidater du kan velge mellom. En samtale som
allerede er knyttet — for hånd eller av en tidligere e-post — beholder kunden sin.

Toppen av samtalen forteller hvordan den henger sammen med en kunde:

- **Kunde nr. id** — samtalen er knyttet, og merket sier hvordan koblingen ble laget.
- **Foreslått kunde nr. id**, med en prosent for sikkerhet — KI-assistenten har et
  forslag, vist med begrunnelsen sin. **Bekreft kunde** gjør det til koblingen.
- **N kundekandidater** — flere kunder kan passe. **Foreslå kunde** ber
  KI-assistenten velge én, som så vises som et forslag du kan bekrefte.
- **Ingen kundetilknytning** — ingen av delene.

Å foreslå og bekrefte krever `communications:conversations-manage`. Når assistenten
ikke er satt opp, ser du «KI-kundeforslag er ikke tilgjengelige nå.» —
administratorens side av det står under
[AI draft and customer suggestion](/en/reference/communications/#ai-draft-and-customer-suggestion).

## Start en samtale

Velg **Ny samtale** i verktøylinjen for å starte en ny kundesamtale: skriv inn mottakerens
e-postadresse i **Til**, **Emne**, og skriv meldingen din.

Dersom du har koblet til din egen Outlook under **Innstillinger → Tilkoblinger** (med
tillatelsen E-post innvilget), kan du huke av for **Send fra min adresse (`du@dittfirma.no`)**.
E-posten sendes gjennom din personlige Microsoft 365-postboks, slik at den vises i dine egne
sendte elementer. Kundens svar sendes likevel til kanalens delte postkasse (`Reply-To`), slik
at samtalen holdes samlet i den delte innboksen for teamet ditt.

## Svar en kunde

Skrivefeltet nederst i samtalen har overskriften **Svar**. Det forteller hvem svaret
går til («Svarer til …»): avsenderen av kundens siste e-post. Når den e-posten også ble
sendt til andre i kopi (Cc), tilbyr velgeren **Svarmodus** valgene **Svar**, bare til
avsenderen, og **Svar alle · N**, som tar dem med i kopi også — uten den delte postboksen
selv. Som standard sendes svaret fra kanalen samtalen hører til. Dersom du har koblet til
din egen Outlook med e-posttillatelse, kan du også huke av for **Send fra min adresse (`du@dittfirma.no`)**
for å sende fra din egen postboks i stedet, mens kundens svar havner i den delte kanalen. Det sendes
som et svar på kundens e-post, så det havner i samme tråd i kundens e-postprogram.

### Personlig sending og ny prøving

Når **Send fra min adresse** er valgt:
- `From`-adressen er din e-postadresse og visningsnavn fra katalogen, og e-posten havner i
  dine egne sendte elementer i Microsoft 365.
- `Reply-To`-adressen er kanalens adresse og visningsnavn, slik at svar fra kunden kommer
  tilbake til den delte kanalen og trådes inn i samtalen.
- Dersom Outlook-tilkoblingen din må kobles til på nytt, tilbyr skrivefeltet en integrert knapp
  for **Koble til Outlook** eller **Koble til Outlook på nytt**.
- Dersom en personlig sending mislykkes ved utsending fordi Microsoft avviste påloggingen din
  eller krevde ny flerfaktorautentisering, viser meldingskortet:
  *Sendingen mislyktes fordi Outlook-tilkoblingen din må kobles til på nytt.* med en knapp for
  **Koble til Outlook på nytt** og en knapp for **Prøv å sende på nytt**. Når du har koblet
  til Outlook på nytt, trykker du på **Prøv å sende på nytt** for å legge meldingen i kø igjen.

1. Skriv meldingen i editoren; fet, kursiv og punktlister er tilgjengelige.
2. **Legg ved fil** klargjør en fil. Den vises som et merke med navnet sitt: grønt når
   den er klar, **Skanner** mens den sjekkes, **Utilgjengelig** når den ble avvist. En
   fil som ikke kunne klargjøres, lar utkastet ditt stå uendret. Størrelsesgrensen
   settes av administratoren (se
   [Attachments](/en/reference/communications/#attachments)). På en **Microsoft
   365**-kanal sier skrivefeltet også *Vedlegg på opptil omtrent 2,1 MB til sammen på
   denne kanalen*: Microsoft 365 tar bare imot en melding opp til en fast størrelse,
   vedlegg og tekst sammen. En større fil avvises med en gang med *Vedlegget er større enn
   denne kanalen kan sende (omtrent 2,1 MB).*
3. **Send svar**. Knappen er deaktivert til det finnes tekst, og viser **Venter på
   vedleggsskanning** mens et vedlegg ennå ikke er klart. I en samtale uten e-post fra
   kunden forblir den deaktivert, med *Det finnes ingen melding fra kunden å svare på
   ennå.*

Å sende krever `communications:conversations-reply`. Svaret legges i kø i stedet for å
sendes på flekken — se [Etter at du har sendt](#etter-at-du-har-sendt) — og det avvises
når:

- samtalens kanal er deaktivert under Kanaler;
- samtalen ikke har noen kundemelding å svare på — en samtale startet fra Vantigo som
  kunden ikke har svart på ennå, eller en på en SMTP-kanal;
- et vedlegg ikke er klart;
- en mottaker står på blokkeringslisten;
- på en Microsoft 365-kanal er meldingen og vedleggene til sammen større enn kanalen
  sender: *Meldingen og vedleggene er større enn denne kanalen kan sende.* Fjern et
  vedlegg, eller send det på en annen måte.

## Skriv et utkast med KI

Over skrivefeltet skriver **Skriv utkast med KI** et første utkast for deg. Velg en tone
(`concise`, `friendly` eller `formal`), skriv hva svaret skal dekke i tekstfeltet — det
er obligatorisk — og klikk **Skriv utkast med KI**. Utkastet havner i editoren, med
overskriften «KI-utkast — se gjennom før sending. Ingenting er sendt.» Les det, rediger
det og send det som ethvert annet svar; **Lag utkast på nytt** ber om et nytt. Når
assistenten ikke er satt opp, sier panelet fra, og du skriver svaret selv.

## Legg til et internt notat

**Legg til notat** bytter skrivefeltet til **Internt notat**. Et notat ligger i samtalen
for kollegene dine og sendes aldri til kunden. Skriv det og klikk **Legg til notat**;
knappen **Svar** bytter tilbake. Notater krever `communications:conversations-manage`.

## Etter at du har sendt

Et sendt svar legges i en utboks og leveres av en bakgrunnsjobb — gjennom kanalens
SMTP-server, eller, på en Microsoft 365-kanal, fra den delte postboksen gjennom
Microsoft 365 — så det kan ta et øyeblikk før det går ut. E-post sendt fra en Microsoft
365-kanal ligger også i **Sendte elementer** i den delte postboksen, der teamet ditt ser
den i Outlook. Et mislykket forsøk prøves igjen
med økende ventetid, inntil åtte forsøk i alt; etter det gis meldingen opp og telles på
en måling administratoren kan varsle på. På en Microsoft 365-kanal gis en melding
postboksen aldri kan sende — postboksen utenfor det Vantigo har tilgang til, borte,
meldingen for stor, eller en blindkopi (Bcc) som ville gått tapt — opp etter første
forsøk. Mens Microsoft 365 er utilgjengelig for Vantigo (**E-postkanaler** slått av,
eller Entra-tilkoblingen feiler), venter meldingen uten å bruke opp forsøkene, og gis
først opp 24 timer etter at den ble sendt. Rett før hvert forsøk sjekker jobben
blokkeringslisten på nytt og avbryter levering til enhver adresse på den. I sjeldne
tilfeller kan en melding leveres to ganger etter et krasj; reglene står under
[Delivery semantics](/en/reference/communications/#delivery-semantics).

Innboks viser ennå ikke en leveringsstatus per melding; det den viser, er at meldingen
ble sendt fra Vantigo.

## Sett opp en kanal

En kanal er en delt postkasse: adressen svarene dine sendes fra. Åpne **Kanaler** for
tabellen over kanaler med **Adresse** (visningsnavnet, med adressen under),
**Leverandør**, **Status** (**Aktiv** eller **Inaktiv**) og **Handlinger**. Kanalen
merket **Standard** er den en samtale opprettes på når ingen er oppgitt; den første
kanalen du oppretter, blir standard.

Kolonnen **Mottaksstatus** sier om en kanal mottar e-post: **Mottar**, med *Sist
synkronisert …*; **Venter på første synkronisering** rett etter at kanalen ble lagt til
eller byttet; **Feilet: …** med årsaken når Vantigo ikke kan lese postboksen —
*Applikasjonen kan ikke lenger lese postboksen. Gi den tilgang i Exchange Online.*,
*Postboksen finnes ikke lenger i Microsoft 365.* eller *Postboksen er ikke kjent for denne
kanalen. Bekreft kanalen.* — eller **Siste synkronisering feilet: …** når ett forsøk
feilet og Vantigo prøver igjen (*E-postkanaler gjennom Microsoft 365 er ikke
tilgjengelige.* mens **E-postkanaler** er av eller Entra-tilkoblingen feiler; e-posten
venter i postboksen). En SMTP-kanal viser **Sender bare**.

En kanal sender på en av to måter, sin **Leverandør**:

- **SMTP** — gjennom en e-postserver, med en SMTP-konto. Administratoren må ha
  applikasjonshemmeligheten på plass og kjenne SMTP-vert, port og konto; passordet
  lagres kryptert og vises aldri igjen, og hemmeligheten det krypteres under, er
  forklart under [SMTP](/en/reference/communications/#smtp). E-postserverinnstillingene en
  administrator setter opp for Vantigos egne invitasjoner og passordtilbakestillinger er
  noe annet og brukes ikke her.
- **Microsoft 365** — fra en **delt postboks** i Exchange Online, gjennom Microsoft 365,
  uten passord å legge inn. Den **mottar** også: e-post som kommer til innboksen i den
  delte postboksen fra den dagen kanalen legges til, kommer inn i Innboks — ikke e-posten
  som lå der fra før. Det tilbys først når noen har slått på **E-postkanaler** i
  appen Microsoft Entra ID og en Exchange-administrator har gitt Vantigo tilgang til
  postboksen ([administrasjonsveiledningen](/nb/admin/entra/#mail-channels)). Når
  applikasjonens tilgang når alle postboksene i selskapet, sier et oransje varsel over
  tabellen fra, *E-posttilgangen gjelder alle postbokser*: *Applikasjonen har tilgang
  til alle postboksene i leietakeren. Begrens den med RBAC for Applications, og fjern
  samtykket som gjelder hele leietakeren. Så lenge denne advarselen vises, kan alle som
  administrerer kanaler i Kommunikasjon, sende som hvilken som helst delt postboks i
  leietakeren.* Kanalene virker likevel.

### Legg til en SMTP-kanal

Klikk **Legg til kanal**. Dialogen **Legg til e-postkanal** spør etter:

- **E-postadresse** — avsenderadressen, obligatorisk.
- **Visningsnavn** — navnet mottakerne ser; vises i tabellen i stedet for adressen når
  det er satt.
- **Leverandør** — **SMTP**.
- **SMTP-vert** og **SMTP-port** (587 er fylt inn), begge obligatoriske, og kontoens
  **Brukernavn** og **Passord**.

**Opprett kanal** lukker dialogen og bekrefter med «Kanal opprettet — E-postkanalen er
klar.» En adresse som allerede har en kanal, avvises.

### Legg til en Microsoft 365-kanal

Klikk **Legg til kanal**, og velg **Microsoft 365** som **Leverandør**. Dialogen spør da
etter **E-postadresse** til den delte postboksen, et **Visningsnavn** og to innstillinger
for postboksen, og sier *Postboksen må være en delt postboks som applikasjonen har tilgang
til.*:

- **Importer historikk** — **Fra nå av** (standard), **Siste 30 dager** eller **Siste 90
  dager**.
- **Når en samtale lukkes** — **La stå uendret i Outlook** (standard), **Marker som lest i
  Outlook** eller **Flytt til mappe i Outlook** (som spør etter **Outlook-mappe**, fylt inn
  med `Handled in Vantigo`, opprettet i den delte postboksen hvis den ikke finnes fra før).

**Opprett
kanal** sjekker postboksen med Microsoft 365 først; kanalen opprettes aktiv, med
postboksens egen primære adresse, og kan sende med en gang. Sjekken kan avvise den:

| Dialogen sier | Betyr | Hvem retter det |
| --- | --- | --- |
| *Dette er en personlig postboks. Bare en delt postboks kan være en kanal.* | Adressen er en persons postboks. | Bruk en delt postboks; en Exchange-administrator kan gjøre om en. |
| *Postboksen er utenfor det applikasjonen har tilgang til. Gi applikasjonen tilgang til den i Exchange Online.* | Vantigo har ikke fått tilgang til postboksen, eller tilgangen virker ikke ennå — det kan ta opptil 2 timer. | En Exchange-administrator, med [Slik gir du applikasjonen tilgang](/nb/admin/entra/#mail-channels). |
| *Microsoft 365 har ingen postboks med denne adressen.* | Ingen postboks har adressen — eller den er et alias. | Bruk postboksens primære adresse. |
| *E-postkanaler gjennom Microsoft 365 er ikke tilgjengelige. En Entra-administrator slår på E-postkanaler i Entra-innstillingene.* | **E-postkanaler** er av, eller Entra-tilkoblingen virker ikke. | Den som administrerer appen Microsoft Entra ID. |
| *En annen kanal bruker allerede denne postboksen.* | En annen kanal bruker den samme postboksen, kanskje med en annen av adressene. | Bruk den kanalen. |
| *Det finnes allerede en kanal med denne adressen.* | En annen kanal har adressen. | Bruk den kanalen. |
| *Microsoft 365 svarte ikke. Prøv igjen om litt.* | Microsoft 365 var opptatt eller utilgjengelig. | Prøv igjen. |

Å legge til, bytte og bekrefte kanaler er begrenset til 30 i minuttet; over det,
*For mange forsøk. Vent et minutt og prøv igjen.*

### Bytt en SMTP-kanal til Microsoft 365

Når postboksen en SMTP-kanal sender fra er en delt postboks i Exchange Online, kan
kanalen sende gjennom Microsoft 365 i stedet, og beholder samtalene, adressen og merket
**Standard**. På raden, velg **Bytt til Microsoft 365**; dialogen advarer *SMTP-innstillingene
slettes. For å gå tilbake må du legge dem inn på nytt.*, og tilbyr de samme valgene for
**Importer historikk** og **Når en samtale lukkes** som når du legger til en Microsoft
365-kanal — velg **Bytt**. Vantigo sjekker
postboksen som når du legger til en, med de samme meldingene, og leverandøren på raden
blir **Microsoft 365**: «Kanalen er byttet — Kanalen sender og mottar nå gjennom
Microsoft 365.» Fra da av kommer e-posten i postboksen fra det valgte importvinduet inn i
Innboks. Handlingen vises bare mens Microsoft 365 er tilgjengelig.

På raden til en Microsoft 365-kanal åpner **Outlook ved lukking** en dialog der du kan
endre **Når en samtale lukkes** (*La stå uendret i Outlook*, *Marker som lest i Outlook*
eller *Flytt til mappe i Outlook* med **Outlook-mappe**).

Kanalen beholder adressen den hadde. Er adressen et alias for den delte postboksen og
ikke den primære adressen, sjekk at den første meldingen går ut som forventet: hvordan
Microsoft 365 sender fra et alias, er ikke verifisert ennå. Å legge til kanalen på nytt
med postboksens primære adresse unngår spørsmålet.

**Det finnes ingen knapp for å gå tilbake.** Siden Kanaler kan ikke bytte en kanal
tilbake til SMTP; en administrator kan, gjennom API-et, ved å sende kanalens
SMTP-innstillinger på nytt med passordet ([Switching provider](/en/reference/communications/#switching-provider)).

### Bekreft og deaktiver

Per kanal sjekker **Bekreft** at kanalen kan sende: for SMTP kobler den til serveren med
de lagrede opplysningene, for Microsoft 365 sjekker den postboksen igjen, og den melder
«Kanal bekreftet — Kanaltilkoblingen er gyldig.» Når det feiler, sier *Kanalen ble ikke
bekreftet* hvorfor — for Microsoft 365 med meldingene ovenfor, eller *Adressen hører nå
til en annen postboks i Microsoft 365.* **Deaktiver** beholder kanalen, men stopper
sending: et svar i en samtale på en inaktiv kanal avvises til **Aktiver** slår den på
igjen.

Det finnes ingen begrensning per bruker på en kanal. Alle som kan svare i Innboks,
svarer fra kanalen samtalen hører til, så hvem som kan bruke en kanal, avgjøres av hvem
som har `communications:conversations-reply`. Å administrere kanaler krever
`communications:channels-manage`.

## Blokker en adresse

En blokkering er en adresse Vantigo ikke vil sende til — en person som har bedt om å
ikke bli skrevet til, eller en adresse som er kjent død. Åpne **Blokkeringer** for
listen, med hver adresses **E-post**, **Årsak** og **Opprettet**-dato, og et
**Søk**-felt som treffer e-posten eller årsaken.

For å legge til en, fyll inn **E-postadresse** (en ugyldig adresse avvises med «Skriv
inn en gyldig e-postadresse») og eventuelt en **Årsak**, og klikk **Legg til
blokkering**. Du ser «Blokkering lagt til — Adressen vil ikke motta meldinger.» Legger
du til en adresse som allerede står på listen, beholdes den eksisterende oppføringen
med den opprinnelige årsaken.

For å fjerne en, klikk **Slett** på raden og bekreft med **Slett blokkering** i dialogen
som spør «Tillat denne adressen å motta meldinger igjen?» Bekreftelsen lyder
«Blokkering slettet — Adressen kan motta meldinger igjen.»

Blokkeringer sammenlignes uten hensyn til store og små bokstaver. Et svar til en
blokkert adresse avvises når det sendes, og utboksen sjekker listen en gang til før
hvert leveringsforsøk. Vantigo legger ikke til blokkeringer på egen hånd: en
returmelding («bounce») som når en Microsoft 365-kanal, kommer inn i Innboks som enhver
annen e-post, og adressen må legges til her for hånd. Blokkeringer krever
`communications:suppressions-manage`.

## Rettigheter

| For å | Trenger du |
| --- | --- |
| Åpne Innboks, lese samtaler, markere dem som lest, laste ned vedlegg og originaler | `communications:conversations-view` |
| Svare, legge ved filer, skrive utkast med KI | `communications:conversations-reply`, sammen med view |
| Endre status, tagger og kundekobling, legge til notater, be om et kundeforslag | `communications:conversations-manage`, sammen med view |
| Se og administrere Kanaler | `communications:channels-manage` |
| Se og administrere Blokkeringer | `communications:suppressions-manage` |

Et område du mangler rettighet til, vises ikke i sidemenyen. Rettigheter gis gjennom
roller under Administrasjon av arbeidsområdet — se
[Finn fram i Vantigo](/nb/user/).
