---
title: Kalender
description: Avtalene dine og det de andre appene har lagt inn i dagene dine, å invitere kolleger, kunders kontaktpersoner og hvem som helst med en adresse, hva en invitert får og hvordan de svarer, avlysning, når kolleger er opptatt, gjentakende avtaler, agendaen på dashbordet, kalenderen din på mobilen, kalendere fra andre steder ved siden av din egen, en kopi av kalenderen din i Outlook, å endre dine egne avtaler fra Outlook, og når du er opptatt i Outlook, vist i Vantigo.
sidebar:
  order: 35
sources:
  - apps/calendar/frontend
  - apps/host/frontend/src/routes/rsvp.tsx
  - apps/host/frontend/src/lib/public-paths.ts
  - apps/host/frontend/src/routes/calendar
  - apps/host/frontend/src/routes/calendar.tsx
---

Kalender-appen er én oversikt over dagene dine: avtalene du lager selv, med kolleger på,
ved siden av det de andre appene allerede vet om deg — oppfølgingene dine i Kunder,
fraværet ditt i Ansatte, oppgavene dine i Prosjekter, timene dine i Timer, reisene
dine i Utlegg og allokeringene dine i Bemanning. Ingenting kopieres: en oppfølging du huker av i Kunder, er avhuket i
kalenderen med en gang, fordi kalenderen spør Kunder hver gang den åpnes. Reglene bak
hvert steg står i [referansen for Kalender-modulen](/en/reference/calendar/); denne
siden handler om hva du gjør på hvert skjermbilde.

## Hvem ser hva

| Hva | Hvem ser det |
| --- | --- |
| **Kalender** i sidemenyen, din egen kalender, avtalene dine og svarene dine på invitasjoner, og **I dag og denne uken** på dashbordet | alle med `calendar:access` |
| **En kollegas kalender** | `calendar:access` og `calendar:view-colleagues` |
| **Kontakter** under **Deltakere**, for å invitere en kundes kontaktpersoner | `calendar:access`, `customers:contacts-view` og `customers:associations-view` |
| **Min kalender** i sidemenyen: kalenderadressene dine og kalenderne du abonnerer på fra andre steder | alle med `calendar:access` |
| Svarsiden en invitasjon lenker til | alle med lenken, uten innlogging |
| Det en kalenderadresse inneholder | ethvert program som får adressen, uten innlogging — så hold den for deg selv |

Det de andre appene legger i kalenderen din, følger deres egne rettigheter: du ser
oppfølgingene dine bare hvis du kan lese tidslinjen i Kunder, fraværet ditt bare hvis
brukeren din er koblet til personen din i Ansatte, allokeringene dine bare hvis du kan
bruke Bemanning (`planning:access`) og brukeren din er koblet til personen din i
Ansatte, og så videre. En app du ikke har
tilgang til, eller som installasjonen ikke har slått på, legger rett og slett ikke til
noe. Hele tabellen står under [Permissions](/en/reference/calendar/#permissions).

## Kalendersiden

Åpne **Kalender** fra app-velgeren. Siden viser én av fire visninger, valgt under
**Visning** i verktøylinjen:

- **Måned** — måneden som et rutenett av uker.
- **Uke** — sju dager med en tidsakse, og en rad øverst for det som varer hele dagen.
- **Dag** — én dag på samme måte.
- **Liste** — en uke som en agenda, én linje per element. På mobilen, der rutenettene
  ikke får plass, åpner kalenderen i **Liste**.

**I dag** tar deg tilbake til i dag, og pilene (**Forrige** og **Neste**) blar en måned,
en uke eller en dag om gangen, etter visningen. Hver dato og hvert klokkeslett vises i
din egen tidssone, den under **Tidssone** i profilen din
([Velg tidssonen din](/nb/user/getting-started/#velg-tidssonen-din)), så et møte satt
til 09:00 i Oslo vises 08:00 for en kollega som har valgt London. Adressen til siden
husker visningen og datoen, så en lenke eller en oppdatering åpner samme sted.

Over kalenderen lister en rad med brikker — forklaringen — hver type element
installasjonen din kan vise, hver i sin egen farge. Klikk en brikke for å skjule den
typen eller vise den igjen; nettleseren din husker valget. Hva hver type er, og hvor et
klikk på et element av den tar deg:

| Brikke | Hva det er | Et klikk på det |
| --- | --- | --- |
| **Avtaler** | En avtale du har laget eller er invitert til | åpner avtalen |
| **Oppfølginger** | En kundeoppfølging tildelt deg, på forfallsdagen; strøket over når den er gjort | åpner kunden |
| **Kundeaktivitet** | En samtale, et møte eller en e-post du har ført på tidslinjen til en kunde | åpner kunden |
| **Oppgaver** | En oppgave tildelt deg, på forfallsdatoen; strøket over når den er gjort | åpner oppgaven på prosjektet |
| **Timer** | Timer du har ført, med prosjektet som tittel, på start- og sluttiden når du oppga dem, ellers hele dagen; strøket over når de er godkjent | åpner dagen i Timer |
| **Reiser** | En av reiseregningene dine, fra avreise til hjemkomst; strøket over når den er godkjent | åpner reiseregningen |
| **Fravær** | Ditt eget fravær, søkt, godkjent eller registrert; et fravær du har søkt om, viser **Søkt om** under navnet til det er avgjort. Navnet og det ordet står på språket som er satt under **Språk** på personen din i Ansatte | åpner **Min side** på fanen **Fravær** |
| **Allokeringer** | Dine egne bekreftede allokeringer i Bemanning, hele dagen gjennom allokeringens periode, med prosjektets kode og navn som tittel; hold pekeren over en for å lese belastningen. En tentativ allokering, planleggernes utkast, vises aldri | åpner **Min bemanning** i Bemanning |
| **Abonnementer** | En hendelse i en kalender du abonnerer på fra et annet sted, i fargen du ga den ([Kalendere fra andre steder](#kalendere-fra-andre-steder)) | — |
| **Outlook** | Tid du er opptatt i hovedkalenderen din i Outlook, tegnet skravert uten tittel: **Opptatt i Outlook**, **Foreløpig i Outlook**, **Ikke til stede** eller **Arbeider et annet sted**, etter hvordan hendelsen viser tiden din i Outlook. Bare mens [Synkroniser med Outlook](#synkroniser-med-outlook) er slått på | — |
| **Helligdager** | Norges helligdager, skyggelagt på dagene sine | — |

Avtaler har sitt eget utseende: en som ikke gjør deg opptatt (**Ledig**), tegnes med
bare omriss, en du har avslått, er strøket over, og en gjentakende har et
gjentakelsesmerke (↻).

## Lag en avtale

1. Trykk **Ny avtale** øverst på siden, eller dra over tiden du vil ha i **Uke** eller
   **Dag** (eller over dager i **Måned**) for å åpne arket med det tidsrommet allerede
   fylt inn.
2. Gi den en **Tittel**.
3. Velg hele dagen eller et klokkeslett. Med **Hele dagen** på velger du **Første dag**
   og **Siste dag**; med den av velger du **Startdato** og **Starttid** og **Sluttdato**
   og **Sluttid**. Klokkeslett leses i din egen tidssone — arket sier hvilken, under
   klokkeslettene — og avtalen husker sonen den ble laget i.
4. Legg til **Sted** og **Beskrivelse** om du vil.
5. Under **Synlighet** velger du **Delt** eller **Privat**. En kollega som kan se
   kalenderen din, ser tittelen på en delt avtale; en privat vises for dem bare som
   **Opptatt**.
6. Under **Vis meg som** velger du **Opptatt** eller **Ledig**. En ledig avtale — en
   påminnelse til deg selv, for eksempel — vises ikke i kalenderen din for kolleger i
   det hele tatt.
7. Under **Deltakere** søker du etter en kollega på navn under **Kolleger** og velger
   dem. Hver av dem ser den i sin egen kalender, kan svare på den og får beskjed på
   e-post. Du kan også invitere folk utenfor bedriften her, under **Kontakter** og
   **Med e-post** ([Inviter folk utenfor bedriften](#inviter-folk-utenfor-bedriften)).
   Opptil 50 personer kan stå på én avtale.
8. Trykk **Lagre**. Avtalen står i kalenderen din med en gang, og i kollegenes.

Klikk en av avtalene dine for å åpne den igjen, endre den og **Lagre**, eller **Slett**
den — når noen utenfor bedriften er invitert, blir en sletting en
[avlysning](#slett-en-avtale-du-har-invitert-folk-til). Drar du en av dine egne avtaler til et annet tidspunkt eller en annen dag i
**Måned**, **Uke** eller **Dag**, eller strekker den fra kanten, lagres det nye
tidspunktet med en gang. Elementer fra de andre appene kan ikke dras.

**Fra en kunde eller et prosjekt.** Handlingen **Ny avtale** på **Oversikt** hos en
kunde eller i toppfeltet på et prosjekt åpner det samme arket med kunden eller prosjektet
allerede fylt inn, vist som et merke under tittelen. Avtalen er fortsatt din — den
listes ikke på kunden — men den sier hvor den kom fra: navnet på kunden eller
prosjektet for den som kan se den kunden eller det prosjektet, og ellers bare nummeret
(**Kunde: nr. 42**). Søket i toppfeltet tilbyr også
**Ny avtale**.

Når du endrer **når** en avtale er — datoene eller klokkeslettene, hele dagen eller
ikke, eller hvordan den gjentas — går svaret til hver deltaker tilbake til
**Ikke svart**, fordi det de svarte på, ikke lenger er det som spørres om. Endrer du
tittelen, beskrivelsen eller stedet, blir svarene stående. Et svar på en invitasjon
endrer ingenting ved selve avtalen, så arrangøren kan fortsette å endre den mens svarene
kommer inn.

## Inviter folk utenfor bedriften

En avtale kan ha folk på seg som ikke bruker Vantigo: en kontaktperson hos en av
kundene dine, eller hvem som helst med en e-postadresse. De får en invitasjon på e-post,
som deres eget e-postprogram — Gmail, Outlook, Apple Mail — viser som en invitasjon, og
de svarer med en lenke i e-posten. Svaret deres vises på avtalen i Vantigo.

Under **Deltakere** i arket er det tre måter å legge til noen på, alle i samme liste:

- **Kolleger** — søk etter en kollega på navn, som før.
- **Kontakter** — velg én eller flere av kundens kontaktpersoner. Dette vises på en
  avtale som er laget fra en kunde (handlingen **Ny avtale** der), og bare for den som kan se
  kundens kontaktpersoner og adressene deres (`customers:contacts-view` og
  `customers:associations-view`). Bare kontaktpersoner med en e-postadresse tilbys,
  siden poenget er å sende dem e-post.
- **Med e-post** — skriv inn en adresse, og et navn om du vil, og legg den til.

Opptil 50 personer av alle de tre slagene til sammen kan stå på én avtale. Trykk
**Lagre**: hver person du la til utenfra, får invitasjonen sin med en gang, én e-post
hver. Lagringen venter aldri på e-posten.

Hver rad i listen viser hva slags deltaker det er, navnet og svaret. En kontaktperson
eller en adresse viser også hvordan det gikk med invitasjonen:

- **Invitert 3. nov. 2026** — den siste invitasjonen ble sendt den dagen.
- **Ikke sendt** — ingenting er sendt. Når installasjonen din ikke kan sende e-post,
  viser alle kontaktpersoner og adresser dette, og arket sier «Invitasjoner på e-post er
  ikke satt opp på denne installasjonen». Personen blir stående på avtalen; når e-post
  er satt opp, trykker du **Send på nytt** (eller endrer tidspunktet eller stedet).
- **Sending mislyktes** — den siste e-posten til dem gikk ikke ut. Trykk
  **Send på nytt** for å prøve igjen. Feiler den fortsatt, spør administratoren din; for
  en kontaktperson, sjekk at de fortsatt har en adresse i Kunder.

**Send på nytt** står ved hver kontaktperson og adresse på en lagret avtale, så lenge
installasjonen din kan sende e-post.

En kontaktperson Kunder har anonymisert, vises som **En kontakt**, og får ikke mer
sendt.

### Hva personen får

Én e-post, fra bedriftens egen avsenderadresse, under navnet ditt, med emnet
**Invitasjon:** fulgt av tittelen og dagen, og skrevet på språket ditt — **Foretrukket
språk** i [profilen din](/nb/user/getting-started/#rediger-profilen-din): norsk når det er
**Norsk**, ellers engelsk, også når det er **Automatisk**. Den sier hva
avtalen er, når (i tidssonen du laget den i), hvor, at det er du som arrangerer den, og
beskrivelsen din, og ber dem svare med lenken i e-posten så du ser svaret i Vantigo.
Avtalen er lagt ved, så e-postprogrammet deres kan vise sine egne svarknapper og legge
den i kalenderen deres.

Svaret som når deg, er det som gis **gjennom lenken**. Bruker de e-postprogrammets egne
knapper i stedet, legges avtalen i kalenderen deres, men svaret går til bedriftens
avsenderadresse og ikke til deg — e-posten sier det.

Gjennom lenken ser de aldri beskrivelsen din, og heller ikke hvem andre som er
invitert; selve e-posten lister de andre utenfra, slik e-postprogrammer venter, men
aldri kollegene dine.

### Se svarene deres

Åpne avtalen: hver rad viser **Godtatt**, **Kanskje**, **Avslått** eller
**Ikke svart**. Når noen utenfra svarer gjennom lenken, får du også en kort e-post —
«Ola Hansen takket ja: Kundemøte», «… takket nei: …» eller «… svarte kanskje: …» — med en
lenke til avtalen.

### Når du endrer avtalen

- Endrer du **når** (datoene, klokkeslettene, hele dagen eller ikke, hvordan den
  gjentas) eller **hvor**, får alle utenfra en oppdatert invitasjon, med emnet
  **Oppdatert: …**. Som for kolleger går svarene deres tilbake til **Ikke svart**,
  bortsett fra at en endring av stedet alene lar dem stå.
- Flytter, avlyser eller legger du tilbake én forekomst av en gjentakende avtale, får
  alle den oppdaterte serien.
- Legger du til noen, får bare de invitasjonen; tar du noen av, får de en avlysning,
  hvis de hadde fått en invitasjon. En adresse du skriver inn som alt står i listen som
  kontaktperson, beholdes én gang, som kontaktpersonen.
- Endrer du bare tittelen, beskrivelsen eller hvem som kan se den, sendes ingenting.

### Send en invitasjon på nytt

**Send på nytt** på raden til en kontaktperson eller en adresse sender den samme
invitasjonen igjen, med den samme lenken, så en tidligere e-post fortsatt virker — med
emnet **Oppdatert: …** hvis de har fått en før. Bruk den når en sending mislyktes, eller
når personen ikke finner e-posten. Meldingen «Invitasjonen er sendt» bekrefter at den
gikk.

## Slett en avtale du har invitert folk til

Så lenge ingen invitasjon har gått ut til noen utenfor bedriften, fjerner **Slett**
avtalen som den alltid har gjort, og kollegene dine på den får beskjed på e-post om at
den er avlyst. Når noen utenfra har fått en, **avlyser** slettingen
den i stedet: hver av dem som fikk en invitasjon, får **Avlyst: …**, som
e-postprogrammet deres bruker til å ta den ut av kalenderen — den som aldri fikk
invitasjonen sin, får ingenting — og kollegene dine på den får beskjed på e-post. Avtalen forsvinner
fra alle kalendere i Vantigo, men du og deltakerne kan fortsatt åpne den fra en lenke:
den åpnes skrivebeskyttet, med banneret **Avlyst 3. nov. 2026** og ingenting å endre. En tid
etter den siste dagen fjernes den for godt.

## Gjentakende avtaler

I arket velger du under **Gjenta** hvordan avtalen gjentas:

- **Daglig**, **Ukentlig** på dagene du huker av, eller **Månedlig** — enten på en dag i
  måneden (**Dag 21**) eller på en ukedag i måneden, for eksempel den andre tirsdagen
  eller den siste fredagen;
- **Hver** dag, uke eller måned, eller med et lengre mellomrom, opptil 99;
- under **Slutter**: **Aldri**, **På en dato** (den **Siste dag** den kan falle på),
  eller **Etter et antall** **Ganger**.

Avtalens første dag er alltid den første forekomsten, og teller med i **Ganger**, så
den må passe i mønsteret: en ukentlig avtale på mandager kan ikke starte på en tirsdag —
arket avviser den under **Gjenta** til du flytter starten til en mandag.

En gjentakende avtale beholder klokkeslettet i tidssonen den ble laget i, også over
skiftet til og fra sommertid: et møte hver mandag klokken 09:00 blir værende klokken
09:00.

Når du lagrer eller sletter en forekomst av en av dine gjentakende avtaler — i arket
eller ved å dra den — spør kalenderen om du mener **Denne forekomsten** eller
**Serien**:

- **Denne forekomsten** flytter eller avlyser den ene dagen og lar resten være. Bare
  tidspunktet endres: annet du har endret i arket, lagres ikke på denne måten. En flyttet
  forekomst vises der du flyttet den, også før serien begynner eller etter at den slutter.
- **Serien** endrer eller sletter alle forekomstene; et nytt tidspunkt flytter dem alle
  like mye. Som spørsmålet advarer om, kan dette fjerne endringene du har gjort på
  enkeltforekomster: en ny start, et bytte til eller fra hele dagen eller en endring av
  hvordan den gjentas, setter dem tilbake til seriens mønster, så gjør de som betyr noe,
  på nytt. Et nytt sluttidspunkt alene beholder dem.

Når du åpner en av dine gjentakende avtaler, lister arket **Endrede forekomster** under
**Gjenta** — «Avlyst …» eller «Flyttet fra … til …» — hver med **Legg tilbake**, som
angrer den ene endringen og henter seriens egen forekomst tilbake.

## Svar på en invitasjon

En avtale en kollega har satt deg på, vises i kalenderen din så snart den er laget, og
du får en kort e-post om den — «Kari Nordmann inviterte deg: Kundemøte, tir. 3. nov.
09:00» — og igjen når tidspunktet eller stedet endres, når du tas av den, og når den
avlyses eller slettes (en endring av én forekomst av en gjentakende avtale sender
ingen). E-posten er på ditt eget språk og lenker til avtalen («Åpne den i Vantigo»); du
svarer i Vantigo.
Klikk den for å åpne den: du ser hva den er, når, **Arrangør** og de andre
**Deltakere** med svarene sine, og **Ditt svar**. Svar med **Godta**, **Kanskje** eller
**Avslå**. Du kan endre svaret når som helst. En avtale du har avslått, blir stående i
kalenderen din, strøket over, så du fortsatt kan ombestemme deg. Bare den som laget
avtalen, kan endre eller slette den.

## En kollegas kalender

Med `calendar:view-colleagues` viser verktøylinjen en kollegavelger, som står på
**Min kalender** mens du ser din egen. Søk etter en kollega på navn og velg dem for å se
kalenderen deres i stedet for din, uten å kunne endre noe:

- avtalene som gjør dem opptatt, med tittel og sted når avtalen er delt;
- en privat som et skravert **Opptatt**, uten noe mer;
- mens Outlook-synkroniseringen deres er slått på, tiden de er opptatt eller ikke til
  stede i hovedkalenderen sin i Outlook, som skraverte blokker uten tittel (**Opptatt i
  Outlook**, **Ikke til stede**) — aldri hva hendelsen er, og ikke det som bare er
  foreløpig eller arbeid et annet sted;
- ingenting for de ledige avtalene deres, og ingenting fra de andre appene — oppgavene,
  timene, oppfølgingene og allokeringene deres blir værende bak de appenes egne dører.

Forklaringen krymper til **Avtaler**, **Outlook** når de har opptatt-tid fra Outlook i
visningen, og **Helligdager**, og **Ny avtale** skjules. Tøm
velgeren for å komme tilbake til din egen kalender.

## Dashbordet

Med Kalender på åpner dashbordet med **I dag og denne uken**: dagens helligdager,
avtaler og elementer i rekkefølge med klokkeslett — **Hele dagen** for det som varer
dagen — hver med lenke til oppføringen sin eller til avtalen i kalenderen; under, de
neste seks dagene på én linje hver med hvor mange oppføringer som står på dem, som du
klikker for å se dem; **Ny avtale**; og **Åpne kalenderen**. Listen
**Trenger oppfølging** under beholder oppfølgingene og oppgavene som forfaller: den sier
hva som trenger deg, agendaen sier når.

## Hvis du har fått en invitasjon

Denne delen er for personen utenfor bedriften som har fått en invitasjon fra noen som
bruker Vantigo.

1. Åpne e-posten, **Invitasjon: …**. Den sier hva avtalen er, når og hvor, og hvem som
   arrangerer den.
2. Klikk lenken i e-posten. En side åpnes i nettleseren — ingen konto eller innlogging
   trengs — med avtalen, tidspunktet i avtalens egen tidssone og, hvis din er en annen,
   i din, hvor den er, og hvem som arrangerer den. Den er på språket invitasjonen ble
   skrevet på.
3. Under **Kommer du?** svarer du **Godta**, **Kanskje** eller **Avslå**. Siden sier
   «Takk. Svaret ditt er sendt til arrangøren.», og arrangøren ser det. Du kan åpne lenken igjen og endre svaret når som helst.

E-postprogrammets egne svarknapper legger avtalen i kalenderen din, men sier ikke fra
til arrangøren: bruk lenken til det. Endres avtalen, får du en oppdatert invitasjon med
den samme lenken; avlyses den, får du en avlysning, og siden sier
**Denne avtalen er avlyst**. En side som sier **Denne lenken er ikke gyldig**, betyr at
du er tatt av avtalen, eller at den er lenge siden.

## Kalenderen din på telefonen

Vantigo kan gi deg en **kalenderadresse**: en lenke som kalenderappen på mobilen — eller
Google Kalender, Outlook eller et annet program som abonnerer på kalendere — leser for å
vise Vantigo-kalenderen din ved siden av de andre kalenderne dine. Der kan den bare
leses: du endrer avtaler i Vantigo, og mobilen viser endringen neste gang den
oppdaterer.

Åpne **Kalender** og velg **Min kalender** i sidemenyen. Under **Kalenderen din på
telefonen** er det to adresser, hver med en setning om hva den inneholder:

- **Hele kalenderen** — kalenderen din slik du ser den i Vantigo: avtalene dine, med
  tittel, sted og beskrivelse, og timene, reisene, oppgavene, oppfølgingene, fraværet og
  allokeringene dine fra de andre appene. Hver hendelse lenker tilbake til Vantigo.
- **Bare opptatt** — tidene du er opptatt, hver med tittelen **Opptatt**, og ingenting
  annet. Gi denne til den som skal se når du er ledig, men ikke hva du gjør — en
  resepsjonist, eller kalenderen til partneren din.

For hver av dem du vil ha:

1. Trykk **Lag adresse**. Adressen vises i et felt, med når den ble laget og når et
   program sist leste den (**aldri lest** til et har gjort det).
2. Trykk **Kopier**, eller **Kopier som webcal** for den samme adressen med `webcal://`
   foran, som Apple Kalender og Outlook abonnerer på med ett klikk.
3. Legg den til i kalenderprogrammet ditt, som under.

Skjermbildet viser adressen igjen hver gang du kommer tilbake, så du kan legge den til på
den bærbare neste måned uten å endre noe.

**Tilbakestill** gir adressen en ny lenke, og den gamle slutter å virke med en gang — bruk
den hvis adressen har kommet på avveie. Skjermbildet spør først, siden mobilen slutter å
oppdatere til du gir den den nye adressen. **Slå av** fjerner adressen helt, også etter å
ha spurt; det som abonnerte på den, slutter å oppdatere.

En adresse virker bare så lenge eieren kan bruke Kalender. Når en kollega slutter og
brukeren deres deaktiveres, eller `calendar:access` tas fra dem, slutter adressene deres å svare med en gang, og hver mobil eller hvert program som
abonnerer på dem, slutter å oppdatere — ingen trenger å tilbakestille noe.

### Legg den til i kalenderprogrammet ditt

- **iPhone eller iPad:** **Innstillinger → Kalender → Kontoer → Legg til konto → Annet →
  Legg til abonnert kalender**, lim inn adressen, og trykk **Neste** og **Arkiver**.
- **Kalender på en Mac:** **Arkiv → Nytt kalenderabonnement**, lim inn adressen, og
  **Abonner**.
- **Google Kalender**, i en nettleser: ved siden av **Andre kalendere** trykker du **+**,
  så **Fra nettadresse**, limer inn adressen og trykker **Legg til kalender**. Det kan
  ta opptil et døgn før hendelsene vises.
- **Outlook:** **Legg til kalender → Abonner fra nettet**, lim inn adressen, gi den et
  navn og importer den.

Mobilen bestemmer selv hvor ofte den oppdaterer: Apple Kalender slik du har stilt den
inn, Outlook omtrent hver tredje time, Google Kalender opptil et døgn. En endring du gjør
i Vantigo, når derfor mobilen senere, ikke med en gang.

Når skjermbildet sier **Denne adressen virker bare på bedriftens nettverk**, har
installasjonen din ingen offentlig adresse: en mobil leser den på kontornettet eller
gjennom VPN, men ikke ute på farten. Spør administratoren din om du trenger mer.

Dette er ikke med: avtaler du har avslått, avlyste avtaler, kalenderne du abonnerer på
fra andre steder ([under](#kalendere-fra-andre-steder)) og helligdager — mobilen har sine
egne. Den sier heller ikke hvem andre som er på en avtale; åpne den i Vantigo for det.
Adressen **Bare opptatt** utelater også det som ikke gjør deg opptatt: avtaler vist som
**Ledig**, heldagselementene fra de andre appene, som en oppgave som forfaller, en
oppfølging eller en fraværsdag, og elementene som er et øyeblikk og ikke et tidsrom, som
en kundeinteraksjon registrert på et klokkeslett. Hendelsene i den bærer ingenting som
sier hvilken app eller hvilken post de kom fra.

## Kalendere fra andre steder

Du kan vise andre kalendere ved siden av din egen: skoleruta, kampene til en klubb, den
private Google-kalenderen din. De vises på kalendersiden din, i fargen du velger, og for
ingen andre.

1. Åpne **Kalender → Min kalender**, og trykk **Legg til kalender** under **Kalendere fra
   andre steder**.
2. Gi den et **Navn**, og lim inn **Adresse**: adressen et kalenderprogram gir deg å
   abonnere på — den slutter som regel på `.ics`, og en som begynner med `webcal://`,
   virker også.
3. Velg en **Farge**, og huk av for **Vis bare som opptatt** hvis du vil se når, ikke hva.
4. Lagre. Vantigo henter kalenderen med en gang, og hendelsene står på kalendersiden din
   neste gang den lastes. En adresse Vantigo ikke kan bruke, avvises på feltet, med
   grunnen. En som lagres, men ikke kan hentes, beholdes: skjermbildet sier **Lagret, men
   kalenderen kunne ikke hentes**, med grunnen, som blir stående på raden.

En hendelse fra en kalender du abonnerer på, tegnes i fargen sin, med navnet på
kalenderen under tittelen, og merkes **Opptatt** når du valgte bare opptatt.
Forklaringen får en brikke, **Abonnementer**, som skjuler alle på en gang. Du kan ikke
dra dem, og et klikk på en åpner ingenting: de hører til den andre kalenderen. Hendelser
den andre kalenderen har avlyst, vises ikke.

Tabellen lister hver kalender med fargen, navnet, nettstedet adressen er på, om den er
bare opptatt, og når den sist ble oppdatert (**Oppdatert for 10 minutter siden**).
Vantigo henter hver av dem igjen omtrent hver halvtime; **Oppdater nå** på raden henter
den med en gang. **Rediger** endrer den, og **Fjern** tar den ut av kalenderen din. Du kan
ha høyst ti. Å legge til, lagre en endring og **Oppdater nå** er begrenset til sammen:
Vantigo henter høyst tretti ganger på ti minutter fra én adresse — datamaskinen din,
eller kontornettet ditt — så hvis den avviser, vent noen minutter og prøv igjen.

Når en henting feiler, vises grunnen i rødt under raden, og kalenderen fortsetter å vise
det den sist leste. Grunnen står på engelsk, slik serveren skriver den:

- **the address redirects; subscribe to the address it points at** — adressen sender
  videre: åpne den i en nettleser og bruk den du ender opp på;
- **the address did not answer a calendar** — det er en nettside, ikke kalenderen: se
  etter abonnements- eller `.ics`-adressen på den siden;
- **the address answered 404 Not Found** (eller et annet tall) — kalenderen er flyttet
  eller fjernet, eller adressen er skrevet feil;
- **the address is not one Vantigo may fetch** — den er på et privat nettverk Vantigo ikke
  henter fra;
- **the calendar is larger than 1 MiB**, **the calendar has more than 10,000 events**
  eller **the address did not answer in time** — kalenderen er for stor, eller
  nettstedet for tregt; spør den som publiserer den;
- **the address could not be reached** — nettstedet svarer ikke, eller navnet finnes
  ikke; sjekk adressen.

Disse kalenderne er bare dine: en kollega som ser på kalenderen din, ser dem ikke, og de
er aldri med i din egen kalenderadresse, så en mobil som abonnerer på Vantigo-kalenderen
din, får dem ikke to ganger.

## Synkroniser med Outlook

Vantigo kan holde en kopi av kalenderen din i Microsoft 365-postboksen din, så avtalene
dine vises i Outlook — på datamaskinen og mobilen — ved siden av alt det andre. I
hovedkalenderen viser de deg også som opptatt for kolleger som booker møter i Outlook.
Kopiene sier **Holdes av Vantigo — endre det der:** med en lenke tilbake, men du kan
også [endre, flytte og slette dine egne avtaler i
Outlook](#endre-avtalene-dine-i-outlook): endringen kommer tilbake til Vantigo.

Vantigo skriver til postboksen din med **din egen Outlook-tilkobling**, så du kobler til
Outlook én gang først. Det kan du gjøre fra selve kortet.

1. Åpne **Kalender → Min kalender**. Kortet **Outlook** står under kalenderadressene
   dine.
2. Under beskrivelsen på kortet velger du **Koble til Outlook** og logger på Microsoft. Du
   kommer tilbake til denne siden, og kortet sier **Tilkoblet som** og kontoen din.
3. Slå på **Synkroniser til Outlook-kalenderen min**.
4. Velg:
   - **Hvor** — **Hovedkalenderen min**, som viser tiden som opptatt for kolleger, eller
     **En egen Vantigo-kalender**, som er lett å skjule, men ikke gjør deg opptatt for
     andre.
   - **Hva** — **Avtaler** og det du vil ha med fra de andre appene: **Fravær**,
     **Oppgaver**, **Timer**, **Reiser**, **Oppfølginger**, **Allokeringer** og
     lignende, så langt du ser dem i Vantigo. Avtaler og fravær er krysset av første
     gang.
   - **Titler** — **Fulle titler**, eller **Bare opptatt**: alle titler blir *Opptatt*,
     med ikke noe annet enn lenken.

Kortet viser **Outlook-tilkoblingen** din under beskrivelsen: **Ikke tilkoblet** med
**Koble til Outlook**, **Tilkoblet som** og kontoen din, eller **Må kobles til på nytt**
med årsaken og **Koble til Outlook på nytt**. Knappen tar deg til Microsofts pålogging og
tilbake til denne siden ([Tilkoblinger](/nb/user/connections/)).

Du kan slå på synkroniseringen før du kobler til: den venter på deg. Til du har koblet
til, sier kortet **Koble til Outlook ovenfor for å starte synkroniseringen.** Når du har
gjort det, synkroniserer Vantigo innen et par minutter, eller med en gang med
**Synkroniser nå**; kortet merker det selv, uten at du laster siden på nytt.

**Koble til på nytt.** Microsoft ber deg av og til logge på igjen — passordet ditt er
endret, organisasjonens påloggingsregler krevde det, eller det har gått lang tid. Kortet
sier da **Microsoft vil at du logger på igjen: velg Koble til Outlook på nytt ovenfor.**
Synkroniseringen tar en pause, og det som ligger i Outlook, blir som det var, til du
velger **Koble til Outlook på nytt** og logger på; så fortsetter den der den slapp. Slår
du av synkroniseringen mens den venter på at du kobler til på nytt, viser kortet fortsatt
**Koble til Outlook på nytt**: Vantigo trenger at du kobler til én gang til for å fjerne
det den har lagt i Outlook.

Hver endring lagres med en gang. Kortet sier så **Venter på den første
synkroniseringen …**, og snart **Sist synkronisert** og når. Vantigo synkroniserer hvert
kvarter, og innen et minutt når en av avtalene dine endres; **Synkroniser nå** ber om en
synkronisering med en gang.

Dette får Outlook: avtalene dine fra 30 dager tilbake til 180 dager frem — de du har
laget, og de du er invitert til og ikke har takket nei til, hver forekomst av en
gjentakende avtale som sin egen hendelse — og det du valgte fra de andre appene, på ditt
språk. En avtale du har svart *kanskje* på, vises som foreløpig, en som er merket ledig,
som ledig, og fravær som fraværende. En privat avtale er privat i Outlook, og en en
kollega har gjort privat, vises som *Opptatt*. Det følger ingen deltakere og ingen
påminnelser med. Aldri med: en kollegas kalender, kalenderne du abonnerer på fra andre
steder, og helligdager.

**Når du er opptatt i Outlook, vist i Vantigo.** Mens synkroniseringen er slått på,
leser Vantigo også hovedkalenderen din i Outlook — bare tidene, aldri titlene — og viser
tiden du er opptatt der:

- i din egen kalender, under brikken **Outlook**, som skraverte blokker merket **Opptatt
  i Outlook**, **Foreløpig i Outlook**, **Ikke til stede** eller **Arbeider et annet
  sted**;
- som opptatt for kolleger som ser kalenderen din (bare opptatt og ikke til stede);
- i de opptatte timene og dagene på **Planen** i Bemanning
  ([Bemanning](/nb/user/planning/#planen)).

Ledige og avlyste hendelser er ikke med, og heller ikke Vantigos egne kopier. Kortet
sier det — **Tiden du er opptatt i hovedkalenderen din i Outlook vises i
Vantigo-kalenderen din, og som opptatt for kollegene dine og på Bemanning-tavlen.
Titlene blir aldri lest.** — og, når den er lest, **Opptatt-tid lest** og når. En
endring i Outlook vises etter neste synkronisering. Dine egne titler fra Outlook vises
ikke i Vantigo.

**Slå den av** med samme bryter: Vantigo fjerner det den la inn i Outlook — **Fjerner det
Vantigo la inn i Outlook …** — og kortet sier **Synkroniserer ikke**. Opptatt-tiden din
fra Outlook glemmes og vises ikke lenger noe sted. Velger du det andre
**Hvor**, fjernes også kopiene fra det første stedet før de skrives til det nye. Alt
Vantigo la inn, forsvinner, også eldre kopier; en egen Vantigo-kalender fjernes i sin
helhet — med mindre en kopi i den har blitt [ditt eget
Outlook-møte](#endre-avtalene-dine-i-outlook): da forsvinner bare Vantigos kopier, og
kalenderen og møtet blir stående — og en kalender du har laget selv, røres aldri.

**Koble fra** Outlook under **Innstillinger → Tilkoblinger**, så fjerner Vantigo det den
la inn i Outlook, som når du slår av synkroniseringen; kortet ber deg så koble til igjen.

Når bryteren ikke kan slås på, sier kortet hvorfor:

- **Administratoren din har ikke slått på Outlook-tilkoblingen.** — en eier slår på
  **Outlook-tilkobling** på Microsoft Entra ID-siden ([Microsoft Entra
  ID](/nb/user/entra/#outlook-tilkobling)).
- **Kalendersynkronisering krever modulen Microsoft Entra ID.** — installasjonen kjører
  den ikke.
- **Tilkoblingen til Microsoft 365 virker ikke akkurat nå.**
- **Vantigo-brukeren din er ikke koblet til en Microsoft 365-konto som Vantigo
  styrer.** — HR kobler personen din i Ansatte til Microsoft-kontoen din; Vantigo
  gjetter den aldri ut fra adressen din.

Når synkroniseringen har stoppet, sier kortet hvorfor i stedet for når den sist kjørte:
**Koble til Outlook ovenfor for å starte synkroniseringen.**, **Microsoft vil at du
logger på igjen: velg Koble til Outlook på nytt ovenfor.**, **Organisasjonen din har ikke
gitt Vantigo tilgang til postboksen din.** (Microsoft 365 avviste postboksen din — spør
administratoren din), **Postboksen din ble ikke funnet.**, **Tilkoblingen til Microsoft
365 virker ikke akkurat nå.** eller **Den siste synkroniseringen mislyktes; Vantigo
prøver igjen.** Det siste trenger du ikke gjøre noe med: Vantigo prøver igjen, sjeldnere
for hver gang. Mister du tilgangen til Kalender, fjernes kopiene dine. Slutter du, blir
de liggende i postboksen din, som deaktiveres sammen med kontoen din.

### Endre avtalene dine i Outlook

Mens synkroniseringen er på, sier kortet det: **Endringer, flyttinger og slettinger du
gjør i Outlook på dine egne avtaler kommer tilbake til Vantigo. Endringer i avtaler du
deltar i, og i de andre elementene, blir lagt tilbake.** Med **En egen Vantigo-kalender**
legger det til at nye hendelser du oppretter i den kalenderen, blir til avtaler.

Dine **egne** avtaler — dem du har laget i Vantigo — kan du endre i Outlook, på
datamaskinen eller mobilen. Vantigo tar med endringen ved neste synkronisering, innen
et kvarter, som om du hadde gjort den i Vantigo:

- **Endre** tittelen, beskrivelsen, stedet, tidene, hele dagen, **Vis som** (ledig er
  ledig i Vantigo, alt annet opptatt) eller **Privat**. Avtalen endres i Vantigo. Det
  du skriver under linjen **Holdes av Vantigo — endre det der:**, tas ikke med, og den
  linjen og lenken kommer tilbake. Med **Bare opptatt** tas bare tidene, **Vis som** og
  **Privat** med; en tittel, en beskrivelse eller et sted du skriver inn, skrives
  bare over igjen. Alt annet blir stående som i Vantigo: de som er med, hvordan den
  gjentas, kunden og prosjektet.
- **Flytt** den, til et annet tidspunkt eller en annen dag: avtalen flyttes.
- **Slett** den: avtalen slettes i Vantigo, slik **Slett** gjør der. Har du invitert
  folk utenfor bedriften, blir den [avlyst](#slett-en-avtale-du-har-invitert-folk-til),
  og de får **Avlyst: …** på e-post. Flytter du kopien til en annen kalender eller mappe
  i Outlook, regnes det som å slette den. Er avtalen endret i Vantigo siden forrige
  synkronisering, tas slettingen ikke med: kopien kommer tilbake, slik en endring gjort
  begge steder gjør.

Alle på avtalen får høre om endringen slik de ville fått fra Vantigo: folk utenfra får
**Oppdatert: …** når tiden eller stedet er endret, og kollegene dine får beskjed
([når du endrer avtalen](#når-du-endrer-avtalen)).

**En gjentakende avtale** er én hendelse i Outlook for hver forekomst. Flytt én, og den
forekomsten flyttes i Vantigo; slett én, og den forekomsten avlyses. Alle andre
endringer i én forekomst — tittelen, for eksempel — legges tilbake: endre serien i
Vantigo. En forekomst av en serie med klokkeslett kan ikke bli hele dagen der, og heller
ikke omvendt.

**Hva som legges tilbake.** En endring Vantigo ikke kan ta med, gjøres om i Outlook ved
den samme synkroniseringen, og kortet sier hva som skjedde under **En endring fra
Outlook ble ikke tatt med**, med når:

- **Du endret «…» i Outlook mens den også ble endret i Vantigo; Vantigos versjon ble
  beholdt.** — avtalen er endret i Vantigo siden forrige synkronisering.
- **Endringen din av «…» i Outlook brøt en av Vantigos regler og ble lagt tilbake.** —
  en tom tittel, for eksempel.
- **«…» er en annen sin avtale; endre den i Vantigo eller be arrangøren om det.
  Endringen din i Outlook ble lagt tilbake.** — du er invitert til den; bare arrangøren
  endrer den.
- **Fra Outlook kan du bare endre tidspunktene til en enkelt forekomst av en avtale, og
  Vantigos andre elementer kan ikke endres der; endringen din ble lagt tilbake.** — de
  andre feltene i en forekomst, eller et element fra en annen app (fravær, en oppgave,
  timer og lignende).
- **Mange av avtalene dine ble slettet i Outlook på én gang, så Vantigo beholdt dem og
  la dem tilbake. Hvis du mente å slette dem, slett dem i Vantigo.** — mer enn ti av
  dine egne avtaler ble slettet i Outlook før én synkronisering, som når en hel kalender
  tømmes: ingen av de slettingene tas med.

Kortet viser bare den siste av dem, og glemmer den når du slår av synkroniseringen. En
kopi av en avtale du deltar i, eller av et element fra en annen app, som du sletter i
Outlook, kommer tilbake.

**Legg til folk på en kopi i Outlook**, og den blir ditt eget Outlook-møte: Outlook
sender dem invitasjonen, og Vantigo lar den kopien være fra da av — den oppdaterer eller
sletter den ikke lenger, og endringene dine i den kommer ikke lenger tilbake. Avtalen
blir stående i Vantigo som den var.

**Nye hendelser i den egne Vantigo-kalenderen.** Med **En egen Vantigo-kalender**, og
**Avtaler** huket av under **Hva**, blir en hendelse du oppretter i den kalenderen i Outlook, til en avtale som er din i Vantigo ved
neste synkronisering — tittelen (**Uten tittel** når den ikke har noen), beskrivelsen,
stedet, tidene, **Vis som** og **Privat** — og fra da av er den en kopi som de andre. En
gjentakende hendelse, eller en med deltakere, blir liggende bare i Outlook. Hendelser du
oppretter i hovedkalenderen, forblir dine i Outlook og tas aldri med.

## Hva som kommer

Kalenderadressen til telefonen, kalendere fra andre steder og kopien i Outlook er
bygget, når du er opptatt i Outlook, vises i Vantigo, og endringene du gjør i dine egne
avtaler i Outlook, kommer tilbake. Neste for Outlook: raskere oppdateringer. Lenger
frem: synkronisering med Google, bookingsider, rom og ressurser, påminnelser i appen og
på e-post, CalDAV og teamkalendere.
