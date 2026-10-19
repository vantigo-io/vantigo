---
title: Kalender
description: Avtalene dine og det de andre appene har lagt inn i dagene dine, å invitere kolleger, kunders kontaktpersoner og hvem som helst med en adresse, hva en invitert får og hvordan de svarer, avlysning, når kolleger er opptatt, gjentakende avtaler og agendaen på dashbordet.
sidebar:
  order: 35
sources:
  - apps/calendar/frontend
  - apps/host/frontend/src/routes/rsvp.tsx
  - apps/host/frontend/src/lib/public-paths.ts
---

Kalender-appen er én oversikt over dagene dine: avtalene du lager selv, med kolleger på,
ved siden av det de andre appene allerede vet om deg — oppfølgingene dine i Kunder,
fraværet ditt i Ansatte, oppgavene dine i Prosjekter, timene dine i Timer og reisene
dine i Utlegg. Ingenting kopieres: en oppfølging du huker av i Kunder, er avhuket i
kalenderen med en gang, fordi kalenderen spør Kunder hver gang den åpnes. Reglene bak
hvert steg står i [referansen for Kalender-modulen](/en/reference/calendar/); denne
siden handler om hva du gjør på hvert skjermbilde.

## Hvem ser hva

| Hva | Hvem ser det |
| --- | --- |
| **Kalender** i sidemenyen, din egen kalender, avtalene dine og svarene dine på invitasjoner, og **I dag og denne uken** på dashbordet | alle med `calendar:access` |
| **En kollegas kalender** | `calendar:access` og `calendar:view-colleagues` |
| **Kontakter** under **Deltakere**, for å invitere en kundes kontaktpersoner | `calendar:access`, `customers:contacts-view` og `customers:associations-view` |
| Svarsiden en invitasjon lenker til | alle med lenken, uten innlogging |

Det de andre appene legger i kalenderen din, følger deres egne rettigheter: du ser
oppfølgingene dine bare hvis du kan lese tidslinjen i Kunder, fraværet ditt bare hvis
brukeren din er koblet til personen din i Ansatte, og så videre. En app du ikke har
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
- Legger du til noen, får bare de invitasjonen; tar du noen av, får de en avlysning.
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
den i stedet: hver av dem får **Avlyst: …**, som e-postprogrammet deres bruker til å ta
den ut av kalenderen, og kollegene dine på den får beskjed på e-post. Avtalen forsvinner
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
- ingenting for de ledige avtalene deres, og ingenting fra de andre appene — oppgavene,
  timene og oppfølgingene deres blir værende bak de appenes egne dører.

Forklaringen krymper til **Avtaler** og **Helligdager**, og **Ny avtale** skjules. Tøm
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

## Hva som kommer

En kalenderadresse mobilen din kan abonnere på, så Vantigo-kalenderen din vises i
mobilens egen kalenderapp; og kalendere fra andre steder, abonnert på og vist ved siden
av din egen. Ingen av dem er bygget ennå.
