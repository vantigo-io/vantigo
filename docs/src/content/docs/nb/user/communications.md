---
title: Kommunikasjon
description: Delte postkasser, samtaler med kunder, sending av e-post og blokkeringer — kanaler på SMTP eller på en delt postboks i Microsoft 365, å bytte en kanal til Microsoft 365, og størrelsen en Microsoft 365-kanal sender.
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
nettverkstilgang. Klokkeslettet på hver melding, og på hver samtale i listen, er i din
egen tidssone ([Velg tidssonen din](/nb/user/getting-started/#velg-tidssonen-din)).

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
   [Attachments](/en/reference/communications/#attachments)). På en **Microsoft
   365**-kanal sier skrivefeltet også *Vedlegg på opptil omtrent 3 MB til sammen på denne
   kanalen*: Microsoft 365 tar bare imot en melding opp til den størrelsen, vedlegg og
   tekst sammen. En større fil avvises med en gang med *Vedlegget er større enn denne
   kanalen kan sende (omtrent 3 MB).*
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
postboksen aldri kan sende — postboksen utenfor det Vantigo har tilgang til, borte, eller
meldingen for stor — opp etter første forsøk. Rett før hvert forsøk sjekker jobben
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

En kanal sender på en av to måter, sin **Leverandør**:

- **SMTP** — gjennom en e-postserver, med en SMTP-konto. Administratoren må ha
  applikasjonshemmeligheten på plass og kjenne SMTP-vert, port og konto; passordet
  lagres kryptert og vises aldri igjen, og hemmeligheten det krypteres under, er
  forklart under [SMTP](/en/reference/communications/#smtp). E-postserverinnstillingene en
  administrator setter opp for Vantigos egne invitasjoner og passordtilbakestillinger er
  noe annet og brukes ikke her.
- **Microsoft 365** — fra en **delt postboks** i Exchange Online, gjennom Microsoft 365,
  uten passord å legge inn. Det tilbys først når noen har slått på **E-postkanaler** i
  appen Microsoft Entra ID og en Exchange-administrator har gitt Vantigo tilgang til
  postboksen ([administrasjonsveiledningen](/nb/admin/entra/#mail-channels)). Når
  applikasjonens tilgang når alle postboksene i selskapet, sier et oransje varsel over
  tabellen fra, *E-posttilgangen gjelder alle postbokser*; kanalene virker likevel.

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
bare etter **E-postadresse** til den delte postboksen og et **Visningsnavn**, og sier
*Postboksen må være en delt postboks som applikasjonen har tilgang til.* **Opprett
kanal** sjekker postboksen med Microsoft 365 først; kanalen opprettes aktiv, med
postboksens egen primære adresse, og kan sende med en gang. Sjekken kan avvise den:

| Dialogen sier | Betyr | Hvem retter det |
| --- | --- | --- |
| *Dette er en personlig postboks. Bare en delt postboks kan være en kanal.* | Adressen er en persons postboks. | Bruk en delt postboks; en Exchange-administrator kan gjøre om en. |
| *Postboksen er utenfor det applikasjonen har tilgang til. Gi applikasjonen tilgang til den i Exchange Online.* | Vantigo har ikke fått tilgang til postboksen, eller tilgangen virker ikke ennå — det kan ta opptil 2 timer. | En Exchange-administrator, med [Slik gir du applikasjonen tilgang](/nb/admin/entra/#mail-channels). |
| *Microsoft 365 har ingen postboks med denne adressen.* | Ingen postboks har adressen — eller den er et alias. | Bruk postboksens primære adresse. |
| *E-postkanaler gjennom Microsoft 365 er slått av i Entra-innstillingene.* | **E-postkanaler** er av, eller Entra-tilkoblingen virker ikke. | Den som administrerer appen Microsoft Entra ID. |
| *Denne postboksen har allerede en kanal.* | En annen kanal bruker den samme postboksen, kanskje med en annen av adressene. | Bruk den kanalen. |
| *Microsoft 365 svarte ikke. Prøv igjen om litt.* | Microsoft 365 var opptatt eller utilgjengelig. | Prøv igjen. |

Å legge til, bytte og bekrefte kanaler er begrenset til 30 i minuttet; over det,
*For mange forsøk. Vent et minutt og prøv igjen.*

### Bytt en SMTP-kanal til Microsoft 365

Når postboksen en SMTP-kanal sender fra er en delt postboks i Exchange Online, kan
kanalen sende gjennom Microsoft 365 i stedet, og beholder samtalene, adressen og merket
**Standard**. På raden, velg **Bytt til Microsoft 365**; dialogen advarer *SMTP-innstillingene
slettes. For å gå tilbake må du legge dem inn på nytt.* — velg **Bytt**. Vantigo sjekker
postboksen som når du legger til en, med de samme meldingene, og leverandøren på raden
blir **Microsoft 365**: «Kanalen er byttet — Kanalen sender nå gjennom Microsoft 365.»
Handlingen vises bare mens Microsoft 365 er tilgjengelig.

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
