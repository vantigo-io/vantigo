---
title: Kommunikasjon
description: Delte postkasser, samtaler med kunder, sending av e-post og blokkeringer.
sidebar:
  order: 80
sources:
  - apps/communications/frontend
---

Kommunikasjon-appen er en delt innboks: alle e-postsamtaler organisasjonen din har
med kundene sine, på ett sted, besvart fra delte postkasser som kalles **kanaler**.
Sidemenyen har tre områder: **Innboks**, der samtaler leses og besvares; **Kanaler**,
der postkassene settes opp; og **Blokkeringer**, listen over adresser Vantigo nekter å
skrive til. Åpner du appen, havner du i Innboks, og dashbordet Hjem viser et
Kommunikasjon-kort med åpne, nye og lukkede samtaler.

Vantigo sender e-post, men mottar den ikke ennå: ingen e-post kommer inn gjennom
Vantigo, så en samtale inneholder bare det som er sendt fra den og notatene som er
skrevet i den. Hva det betyr for svar, er beskrevet under
[The outbound-only consequence](/en/reference/communications/#the-outbound-only-consequence)
i referansen, og nevnt nedenfor der du støter på det.

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
avsenderens navn for en melding fra kunden (disse merkelappene vises på engelsk). En
melding skrevet som HTML vises i en sandkasse som blokkerer skript, skjemaer og
nettverkstilgang.

Et vedlegg vises som en lenke du kan laste ned; mens det venter på å bli sjekket, vises
det som **Skanner**, og et som ikke kan leveres, som **Vedlegg utilgjengelig**.

Fra toppen kan du:

- endre **Status** (`open`, `closed` eller `archived`);
- åpne **Handlinger** → **Marker som lest**, som fjerner ulest-markeringen, eller
  **Arkiver**;
- legge til en tagg fra velgeren **Legg til tagg**, eller fjerne en med × på den. Bare
  tagger som allerede finnes, tilbys; Innboks oppretter ikke nye.

Å lese krever `communications:conversations-view`; å endre status og tagger krever i
tillegg `communications:conversations-manage`.

## Knytt en samtale til en kunde

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

## Svar en kunde

Skrivefeltet nederst i samtalen har overskriften **Svar**. Det forteller hvem svaret
går til («Svarer til …»), og når samtalen har flere mottakere, tilbyr velgeren
**Svarmodus** valgene **Svar** og **Svar alle · N**. Svaret sendes fra kanalen samtalen
hører til; du velger ikke avsender.

1. Skriv meldingen i editoren; fet, kursiv og punktlister er tilgjengelige.
2. **Legg ved fil** klargjør en fil. Den vises som et merke med navnet sitt: grønt når
   den er klar, **Skanner** mens den sjekkes, **Utilgjengelig** når den ble avvist. En
   fil som ikke kunne klargjøres, lar utkastet ditt stå uendret. Størrelsesgrensen
   settes av administratoren (se
   [Attachments](/en/reference/communications/#attachments)).
3. **Send svar**. Knappen er deaktivert til det finnes tekst, og viser **Venter på
   vedleggsskanning** mens et vedlegg ennå ikke er klart.

Å sende krever `communications:conversations-reply`. Svaret legges i kø i stedet for å
sendes på flekken — se [Etter at du har sendt](#etter-at-du-har-sendt) — og det avvises
når:

- samtalens kanal er deaktivert under Kanaler;
- samtalen ikke har noen kundemelding å svare på. Siden Vantigo ikke mottar e-post
  ennå, gjelder det alle samtaler i dag, så et svar kan foreløpig ikke sendes;
  skrivefeltet er komplett og begynner å virke når innkommende e-post finnes;
- et vedlegg ikke er klart;
- en mottaker står på blokkeringslisten.

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

Et sendt svar legges i en utboks og leveres av en bakgrunnsjobb gjennom kanalens
SMTP-server, så det kan ta et øyeblikk før det går ut. Et mislykket forsøk prøves igjen
med økende ventetid, inntil åtte forsøk i alt; etter det gis meldingen opp og telles på
en måling administratoren kan varsle på. Rett før hvert forsøk sjekker jobben
blokkeringslisten på nytt og avbryter levering til enhver adresse på den. I sjeldne
tilfeller kan en melding leveres to ganger etter et krasj; reglene står under
[Delivery semantics](/en/reference/communications/#delivery-semantics).

Innboks viser ennå ikke en leveringsstatus per melding; det den viser, er at meldingen
ble sendt fra Vantigo.

## Sett opp en kanal

En kanal er en delt postkasse: adressen svarene dine sendes fra, med SMTP-kontoen som
sender dem. Åpne **Kanaler** for tabellen over kanaler med **Adresse** (visningsnavnet,
med adressen under), **Leverandør**, **Status** (**Aktiv** eller **Inaktiv**) og
**Handlinger**. Kanalen merket **Standard** er den en samtale opprettes på når ingen
er oppgitt; den første kanalen du oppretter, blir standard.

Før en kanal opprettes, må administratoren ha applikasjonshemmeligheten på plass og
kjenne SMTP-vert, port og konto; passordet lagres kryptert og vises aldri igjen, og
hemmeligheten det krypteres under, er forklart under
[SMTP](/en/reference/communications/#smtp). Merk at e-postserverinnstillingene en
administrator setter opp for Vantigos egne invitasjoner og passordtilbakestillinger er
noe annet og ikke brukes her.

For å opprette en, klikk **Legg til kanal**. Dialogen **Legg til e-postkanal** spør
etter:

- **E-postadresse** — avsenderadressen, obligatorisk.
- **Visningsnavn** — navnet mottakerne ser; vises i tabellen i stedet for adressen når
  det er satt.
- **Leverandør** — la den stå på **SMTP**. **Mailgun** står i listen, men tjeneren
  godtar bare SMTP-kanaler og avviser forespørselen; kanalen blir da ikke opprettet.
- **SMTP-vert** og **SMTP-port** (587 er fylt inn), begge obligatoriske, og kontoens
  **Brukernavn** og **Passord**.

**Opprett kanal** lukker dialogen og bekrefter med «Kanal opprettet — E-postkanalen er
klar.» En adresse som allerede har en kanal, avvises.

Per kanal kobler **Bekreft** til SMTP-serveren med de lagrede opplysningene og melder
«Kanal bekreftet — Kanaltilkoblingen er gyldig.»; når tilkoblingen feiler, vises ingen
bekreftelse. **Deaktiver** beholder kanalen, men stopper sending: et svar i en samtale
på en inaktiv kanal avvises til **Aktiver** slår den på igjen.

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
hvert leveringsforsøk. Vantigo legger ikke til blokkeringer på egen hånd: siden ingen
e-post mottas, når en returmelding («bounce») det aldri, så en adresse som returnerer
e-post må legges til her for hånd. Blokkeringer krever
`communications:suppressions-manage`.

## Rettigheter

| For å | Trenger du |
| --- | --- |
| Åpne Innboks, lese samtaler, markere dem som lest, laste ned vedlegg | `communications:conversations-view` |
| Svare, legge ved filer, skrive utkast med KI | `communications:conversations-reply`, sammen med view |
| Endre status, tagger og kundekobling, legge til notater, be om et kundeforslag | `communications:conversations-manage`, sammen med view |
| Se og administrere Kanaler | `communications:channels-manage` |
| Se og administrere Blokkeringer | `communications:suppressions-manage` |

Et område du mangler rettighet til, vises ikke i sidemenyen. Rettigheter gis gjennom
roller under Administrasjon av arbeidsområdet — se
[Finn fram i Vantigo](/nb/user/).
