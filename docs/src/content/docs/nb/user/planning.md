---
title: Bemanning
description: Hvem som jobber med hva — Planen med personer mot uker, fargene, grupperingene og filtrene, allokeringsskjemaet med varslene og avvisningene, et oppdrags vilkår med tidligst ledig, overlappingen og karantenen, Behov og behovsskjemaet, å fylle et behov fra kandidatene, Inntak med de planlagte prosjektene, prosjektplanen med etterspørselen, en persons tidslinje med merknadene og markeringene, listen Ubemannet, personenes ønsker i Min bemanning og planleggernes Ønsker, Min bemanning og e-posten personen får, Bemanningsinnstillinger, kortet på dashbordet og punktene i Trenger oppfølging, bemanningsfanene på personsiden i Ansatte og prosjektsiden i Prosjekter, og allokeringene dine i Kalender.
sidebar:
  order: 46
sources:
  - apps/planning/frontend
  - apps/host/frontend/src/routes/planning
---

I Bemanning-appen setter planleggerne folk på prosjekter, uke for uke, og der ser alle
hva de selv er satt opp på. En slik oppsetting er en **allokering**: én person, ett
prosjekt, en periode, en belastning — timer per uke eller prosent av stillingen — og om
den er **Tentativ**, planleggernes utkast, eller **Bekreftet**, planen. Personene kommer
fra [Ansatte](/nb/user/staff/), med ansettelsen, kapasiteten og fraværsdagene; prosjektene
kommer fra [Prosjekter](/nb/user/projects/). Bemanning kopierer ingenting fra noen av
dem: et navn du endrer i Ansatte eller Prosjekter, er navnet planen viser. Reglene bak
hvert skjermbilde — kapasitetsberegningen, varslene, avvisningene og rettigheten hver
forespørsel sjekkes mot — står i [referansen for Bemanning-modulen](/en/reference/planning/);
denne siden handler om hva du gjør i hvert skjermbilde.

Bemanning har allokeringene, planen, listene og personens egen visning, og **behov** —
det et prosjekt trenger før noen er navngitt — med dem som kunne fylt dem, og inntaket av
planlagte prosjekter. En allokering som også er et oppdrag for en kunde, har
kontraktens **vilkår** — oppsigelsestiden, bindingen, overlappingen og karantenen — og
av dem leser Bemanning dagen personen tidligst er ledig. Personene sier hva de
**ønsker**, av et prosjekt eller noe nytt, og planleggerne leser det. Og dine egne
bekreftede allokeringer vises i [Kalender](#i-kalender).

## Appen og sidemenyen

**Bemanning**-flisen vises for alle med `planning:access`. Sidemenyen viser områdene
rettighetene dine gir deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Plan** (planen) | `planning:view` |
| **Behov** | `planning:view` |
| **Inntak** | `planning:view` |
| **Ubemannet** | `planning:view` |
| **Ønsker** | `planning:manage` |
| **Min bemanning** | alle med `planning:access` |
| **Bemanningsinnstillinger** | `planning:manage` |

Appen åpner på **Plan** for dem som kan se den, og på **Min bemanning** for alle andre.
Prosjektplanen og en persons tidslinje har ingen egen oppføring i sidemenyen: du åpner
prosjektplanen fra planen, Behov eller Inntak, eller som
[Bemanning-fanen](#på-prosjektsiden) på prosjektets side i Prosjekter, og en persons
tidslinje fra planen, listen Ubemannet, dashbordet eller personens side i Ansatte.

Planen bygger på personene Ansatte viser deg, så en planlegger trenger også **Se
ansattoversikten** (`staff:view`) fra Ansatte. Uten den later ikke planen som om den er
tom: den sier **Ansatte viser deg ingen** og ber deg få en administrator til å gi deg
rettigheten.

Søket i toppfeltet kjenner skjermbildene over, og tilbyr **Ny allokering** og **Nytt
behov** til en planlegger som har `planning:view` og `planning:manage`: det første
åpner planen med allokeringsskjemaet, det andre Behov med behovsskjemaet.

## Planen

**Plan** (`planning:view`; skjermbildet heter **Planen**) viser hvem som er satt opp på
hva: én rad per person som er ansatt i ukene som vises, én kolonne per uke, nummerert
(**U42**), med inneværende uke markert.

Hver celle er personens uke:

- Det store tallet er **belastningen** — ukens bekreftede timer i prosent av personens
  kapasitet — og under det timene som er satt opp, med de tentative timene etter et
  pluss når det finnes noen. Kapasiteten er personens stilling slik Ansatte har den —
  timene i full stilling ganger stillingsprosenten, fem dager i uken — minus helger og
  helligdager. **Ingen kapasitet** betyr at uken ikke har noen: personen er ikke ansatt
  da, eller uken er bare helligdager.
- Fargen sier hvor full uken er: grå er **ledig** (under 50 %), grønn **passe** (50 til
  90 %), blå **full** (90 til 100 %) og oransje **over** (mer enn kapasiteten).
- En skravert stripe nederst er den **tentative** delen. Tentative timer teller ikke i
  noe tall — verken belastningen eller de ledige timene — de vises bare for seg.
- En celle blir **rød** når personen er satt opp i en uke der hen er borte en av dagene.
  Bemanning krymper aldri en allokering på grunn av fravær: en uke borte under en
  allokering er en konflikt du må løse, ikke en stille endring av planen. Planen sier
  aldri hva slags fravær det er, eller hvor mye av dagen.

Hold pekeren over en celle for å lese tallene: timene satt opp av kapasiteten, de
tentative timene, de ledige timene, og **Borte deler av uken** eller **Satt opp i en uke
med fravær**.

Over rutenettet:

- Pilene flytter bakover eller fremover like mange uker som vises, og **Denne uken**
  kommer tilbake. Planen går høyst tretten uker tilbake, og tre år frem.
- **Uker vist** — 4, 8, 12 (standard) eller 26 uker.
- **Rader** — **Personer**, eller **Prosjekter**: hvert prosjekt med en allokering i
  ukene som vises, med timene summert, fulgt av en rad for hver person som er satt opp
  på det, der timene er dem på det prosjektet.
- **Avdeling** snevrer planen inn til personene i én avdeling; **Prosjekt** til
  personene som er satt opp på ett prosjekt; **Kompetanse** til personene som har én
  kompetanse fra katalogen i Ansatte — en ferdighet på hvilket som helst nivå, eller et
  sertifikat bekreftet og gyldig — på den første dagen som vises, eller i dag når planen
  begynner i fortiden.
- **Vis tentative** (på) tegner de tentative timene; slå den av for å se bare den
  bekreftede planen.
- **Ta med planlagte prosjekter** (på) teller allokeringene på prosjekter med statusen
  **Planlagt** — tilbudene. Slå den av for å lese planen for det som er sikkert: kan vi
  ta det tilbudet, eller det?

Hvert valg ligger i sidens adresse, så en lenke viser den samme planen.

Trykk pilen foran et navn for å åpne raden: allokeringene vises som stolper over ukene,
hver med prosjektets navn (eller, under **Prosjekter**, personens). Trykk selve navnet
for å åpne personens [tidslinje](#en-persons-tidslinje) eller prosjektets
[plan](#prosjektplanen).

En planlegger (`planning:manage`) kan trykke en celle for å sette opp den personen,
eller det prosjektet, den uken, trykke en stolpe for å endre allokeringen, og trykke
**Ny allokering** øverst på siden.

## Sett opp en allokering

Allokeringsskjemaet åpnes på siden — nedenfra på mobilen — med **Ny allokering** eller
**Endre allokering** øverst. Det er `planning:manage` sitt; ingen andre får det.

1. **Person** — skriv et navn eller et ansattnummer. Bare personer Ansatte viser deg,
   tilbys.
2. **Prosjekt** — skriv en kode eller et navn. Prosjekter som er **Planlagt**,
   **Aktivt** eller **På vent**, tilbys.
3. **Periode** — første og siste dag, begge medregnet.
4. **Belastning** — velg **Prosent av stillingen** (mer enn 0 og høyst 100) eller
   **Timer per uke** (mer enn 0 og høyst 80), og skriv tallet. En prosent følger
   personens stilling: når stillingsprosenten endres, følger timene med. Timer per uke
   blir det du skrev. Skjemaet starter med enheten [innstillingene](#bemanningsinnstillinger)
   oppgir.
5. **Status** — **Tentativ** eller **Bekreftet**. En allokering på et prosjekt med
   statusen **Planlagt** starter tentativ, på alle andre bekreftet, til du velger selv.
   *Tentativ* er planleggernes utkast: vises for seg, teller ikke i noen hovedtall og
   vises ikke for personen. *Bekreftet* er planen: personen ser den under **Min
   bemanning** og får beskjed på e-post.
6. **Rolle** (valgfri, høyst 100 tegn) — rollen slik prosjektet kaller den, for eksempel
   *Frontendutvikler*. Alle som ser planen, leser den, og det gjør personen også.
7. **Notat** (valgfritt, høyst 500 tegn) — bare for planleggerne. Det vises aldri for
   personen og sendes aldri på e-post, og historikken noterer at det ble endret, aldri
   teksten.

Trykk **Lagre**. Når ingenting trenger å sies, lukkes skjemaet med *Allokeringen er
lagret*.

### Varsler

Noen følger stopper ikke lagringen; skjemaet sier **Lagret — men se på dette** og lister
dem:

- *Det er satt opp mer enn personens kapasitet i* … — ukene der personens bekreftede
  allokeringer nå er større enn kapasiteten.
- *Personen er satt opp mens hen er borte i* … — ukene der allokeringene passer i
  kapasiteten, men ikke i dagene personen er på jobb.
- *Perioden går utenfor prosjektets start- eller sluttdato.*
- *Personen har karantene overfor denne kunden til og med* … — personen har
  [karantene](#karantenen) overfor prosjektets kunde, og allokeringen er tentativ, så den
  lagres som planleggernes utkast; den avvises i det øyeblikket du bekrefter den. En
  bekreftet allokering som alt ligger på den kundens prosjekt, får det samme varselet
  når du bare endrer rollen, belastningen eller den siste dagen.

Bare bekreftede allokeringer veies for overbelastning: en tentativ allokering
overbooker aldri noen.

### Avvisninger

Noe lagres ikke i det hele tatt. Skjemaet sier **Kunne ikke lagre** med grunnen, eller
merker feltet:

- *En dag i perioden ligger utenfor personens ansettelse.* — ingen kan settes opp før de
  begynner eller etter at de slutter.
- *Prosjektet er fullført eller kansellert: det tar ingen nye allokeringer og ingen
  endringer.* — allokeringene som allerede ligger der, blir stående og kan slettes.
- *Høyst tre år (1096 dager)* på **Periode**.
- *Dette ville satt opp mer enn personens kapasitet, og innstillingene avviser
  overbelastning.* — bare når **Overbelastning** står på **Avvis** i
  [innstillingene](#bemanningsinnstillinger). En endring som beholder eller minsker en
  overbelastning som allerede finnes, lagres fortsatt, med varselet, så en plan kan
  rettes én allokering om gangen.
- *Personen har karantene overfor denne kunden.* — en bekreftet allokering som plasseres
  på et prosjekt for en kunde personens [karantene](#karantenen) gjelder. Den vurderes før
  overbelastningen, så når begge gjelder, er det denne grunnen som gis.
- *Noen andre lagret dette først; det er lastet inn på nytt* — en annen planlegger endret
  den samme allokeringen først. Se hva som står der nå, og gjør endringen din på nytt.

### Endre, bekrefte eller slette en allokering

Åpne en allokering fra stolpen på planen, i prosjektplanen eller på en persons
tidslinje, eller med **Endre** i handlingsmenyen i prosjektets liste over allokeringer.
Endre det du trenger, og trykk **Lagre**.

- **Bekreft** (på en tentativ allokering, i skjemaet eller handlingsmenyen) gjør den til
  en del av planen. Den vurderes som enhver endring: et avsluttet prosjekt avviser den,
  og en overbelastning gir varsel — eller avvises, når innstillingene sier det.
- For å sette en bekreftet allokering tilbake til tentativ velger du **Tentativ** og
  lagrer.
- **Slett** spør *Slette denne allokeringen?* og fjerner allokeringen fra planen.

Personen får **e-post**, på sitt eget språk, hver gang en allokering for dem blir
bekreftet, når perioden, belastningen eller prosjektet i en bekreftet allokering endres,
når en bekreftet allokering settes tilbake til tentativ, og når den slettes. E-posten
oppgir prosjektets kode og navn og datoene — med belastningen når personen blir satt
opp eller allokeringen endres — og lenker til **Min bemanning**; den har aldri med
notatet. Flytter du en bekreftet allokering til en annen person, får den første beskjed
om at hen ikke lenger er satt opp, og den andre at hen er det. Det sendes aldri e-post om
en tentativ allokering, og en person uten bruker får aldri e-post.

### Vilkår

Når en allokering er et oppdrag for en kunde, hører kontraktens vilkår hjemme på den.
Åpne allokeringen (`planning:manage`): under allokeringens eget skjema har skjemaet en
egen del, **Vilkår**: *Oppdragets vilkår med kunden. Vilkårene gjelder først når
allokeringen er bekreftet; bare planleggerne ser dem.* Den finnes bare på en allokering
som alt er lagret — personen ser aldri vilkårene, og **Se planen** viser bare de to
datoene de gir.

- **Oppsigelsestid** — et antall fra 1 til 365 og en enhet, **dager**, **uker** eller
  **måneder**; oppgi begge eller ingen av dem.
- **Oppsigelse gitt** — dagen oppsigelsen ble gitt, av dere eller av kunden. Den krever
  en oppsigelsestid.
- **Bundet til** — dagen kontrakten binder personen til, minst.
- **Erstatning krever kundens godkjenning** — kunden må godkjenne en erstatter før
  personen kan gå tidlig.
- **Overlappingsdager** — 0 til 90: hvor mange dager en etterfølger bør overlappe før
  personen tidligst er ledig.
- **Karantene til** og **Kunder** — personen kan ikke bekreftes mot kundene som står her,
  høyst 20, før den dagen. Oppgi begge eller ingen av dem.
- **Referanse** (høyst 200 tegn) — kontraktens eller bestillingens referanse.

Trykk **Lagre vilkår**; skjemaet blir stående åpent med *Vilkårene er lagret*, så du kan
lese datoene de gir. **Fjern vilkår** spør *Fjerne vilkårene?* og fjerner alle. Vilkårene
lagres for seg, ikke sammen med allokeringen, og alltid hele: et felt du tømmer, blir
fjernet.

Vilkår kan settes på en tentativ allokering, og **binder når den er bekreftet**: før det
teller verken tidligst ledig eller karantenen noe sted.

Når vilkårene er lagret, viser overskriften i delen de to datoene Bemanning leser av
dem, og som ingen skriver inn:

- **Tidligst ledig** — dagen personen tidligst kan bli ledig: den seneste av
  allokeringens siste dag, **Bundet til**, og dagen oppsigelsen ble gitt pluss
  oppsigelsestiden. Uten vilkår, eller uten noen som går lenger, er det allokeringens
  siste dag; fjerner du vilkårene, blir det den igjen.
- **Overlapping fra** — tidligst ledig minus overlappingsdagene: dagen en etterfølger bør
  begynne, så de to overlapper. Vises bare når **Overlappingsdager** er oppgitt.

Begge vises på personens [tidslinje](#en-persons-tidslinje), og begge blir merknader på
personen når hen er [kandidat](#fyll-et-behov) til et behov som begynner før hen er
ledig.

For å legge til kunder under **Kunder** søker du på navnet; det krever **Se kunder**
(`customers:view`) i Kunder. Uten den sier delen *Du kan ikke søke i kundene (det krever
Se kunder). Kundene som står her, kan fortsatt fjernes.* — kundene som alt står der,
blir stående, og hver kan fjernes, og en kunde kunderegisteret ikke lenger kjenner, står
som *Kunde* og nummeret.

#### Karantenen

Karantenen er det ene vilkåret Bemanning håndhever. Når en bekreftet allokering for
personen har vilkår med en karantene mot en kunde, gjelder den mot hver annen allokering
for personen som begynner på eller før dagen i **Karantene til**:

- en allokering som ville **plassere** personen, bekreftet, på et prosjekt for den
  kunden, avvises med *Personen har karantene overfor denne kunden.*: en ny bekreftet
  allokering, å [fylle et behov](#fyll-et-behov) som bekreftet, å bekrefte en tentativ
  allokering, og en endring av en bekreftet allokering som flytter den til personen
  eller til prosjektet, eller flytter den første dagen tidligere;
- en **tentativ** allokering lagres, med varselet som oppgir dagen karantenen varer til
  og med — og det gjør også en endring av en bekreftet allokering som alt ligger på den
  kundens prosjekt, når den bare gjelder rollen, belastningen eller den siste dagen;
- personen tilbys ikke i det hele tatt som kandidat til den kundens behov.

Et prosjekt uten kunde treffes aldri av en karantene, og allokeringen som har vilkårene,
vurderes aldri mot seg selv.

## Behov

Et **behov** er det et prosjekt trenger før noen er navngitt: en tittel som
*Backendutvikler*, en periode, timene per uke og kompetansen personen må ha. Det er
**Åpent** til noen fyller det, **Fylt** mens en allokering fyller det, eller **Trukket
tilbake**. Et åpent behov er prosjektets etterspørsel: det vises i prosjektplanen som
raden **Etterspurt** og i [inntaket](#inntak).

**Behov** (`planning:view`) lister dem etter første dag: tittelen med kravene under,
prosjektet — trykk det for å åpne prosjektets [plan](#prosjektplanen) — perioden, timene
per uke og statusen, med *Fylt av* og personens navn på et fylt behov. Listen åpner på de
**åpne** behovene; **Status** bytter til de fylte eller de tilbaketrukne, eller til
**Alle statuser**, og **Prosjekt** snevrer inn til ett prosjekt. Begge ligger i sidens
adresse, så en lenke viser den samme listen. En liste er høyst 500 behov, de eldste
først — siden sier fra når den er kuttet.

En planlegger (`planning:manage`) trykker tittelen på et behov for å åpne det,
**Kandidater** for å [fylle det](#fyll-et-behov), og **Trekk tilbake** på et åpent
behov, og registrerer et nytt med **Nytt behov** øverst på siden.

### Behovsskjemaet

Behovsskjemaet åpnes på siden — nedenfra på mobilen — med **Nytt behov** eller **Endre
behov** øverst. Det er `planning:manage` sitt.

1. **Prosjekt** — skriv en kode eller et navn. Prosjekter som er **Planlagt**,
   **Aktivt** eller **På vent**, tilbys.
2. **Tittel** — hva prosjektet trenger, høyst 100 tegn.
3. **Periode** — første og siste dag, begge medregnet, høyst tre år.
4. **Timer per uke** — mer enn 0 og høyst 80. Et behov føres alltid i timer per uke: det
   har ingen person med en stilling en prosent kunne regnes mot.
5. **Krav** (valgfritt, høyst 10) — trykk **Legg til krav** og velg en kompetanse fra
   katalogen i Ansatte, med ferdigheter og sertifikater hver for seg. Hver kompetanse
   kan bare nevnes én gang. På en kompetanse med nivåer ber **Laveste nivå** om minst
   *kjenner til*, *grunnleggende*, *selvstendig*, *erfaren* eller *ekspert*; står det
   på **Hvilket som helst nivå**, er det nok å ha den. En person oppfyller et krav når
   Ansatte har personen med kompetansen på behovets første dag — en ferdighet på eller
   over nivået, et sertifikat bekreftet og gyldig. Fjern et krav med søppelbøtta.
6. **Notat** (valgfritt, høyst 500 tegn) — bare for planleggerne.

Trykk **Lagre**. Når ingenting trenger å sies, lukkes skjemaet med *Behovet er lagret*;
går perioden utenfor prosjektets start- eller sluttdato, lagres det, og skjemaet sier det
under **Lagret — men se på dette**.

Skjemaet avviser det allokeringsskjemaet avviser for et prosjekt: et prosjekt som er
fullført eller kansellert, tar ingen nye behov og ingen endringer, og *Noen andre lagret
dette først; det er lastet inn på nytt* betyr at en annen planlegger endret behovet
først.

Et fylt eller tilbaketrukket behov åpnes i skjemaet for å leses, ikke endres: *Et fylt
eller tilbaketrukket behov kan ikke endres.* Endre allokeringen som fyller det i stedet,
eller registrer et nytt behov.

### Trekk tilbake et behov

**Trekk tilbake** på et åpent behov spør *Trekke tilbake dette behovet?* og lukker det:
det slutter å telle som etterspørsel, og det åpnes aldri igjen. Et fylt behov kan ikke
trekkes tilbake; for å angre en fylling sletter du allokeringen som fyller det, og
behovet er åpent igjen.

## Fyll et behov

**Kandidater**, på et behov i Behov, i Inntak eller i prosjektplanen, åpner personene som
kunne fylt det, på siden (`planning:manage`). Øverste linje gjentar behovets prosjekt,
periode og timer per uke.

En kandidat er en person Ansatte viser deg, som er ansatt gjennom hele perioden og har
alle kravene. Hvert kort viser:

- navnet, med ansattnummeret og avdelingen;
- **Har tid** når personens ledige timer i perioden dekker det behovet ville satt opp,
  eller **Delvis ledig** når de ikke gjør det — og under, *28 t ledig av 74 t som
  trengs*. Ledige timer er personens kapasitet i perioden minus fraværsdagene og de
  bekreftede allokeringene; tentative teller ikke, som på planen. Det behovet trenger,
  er timene per uke fordelt på personens arbeidsdager, så en helligdag trenger ingenting;
- **Passer ønskene**, et tall av 100 for hvor godt allokeringen passer preferansene
  personen har i Ansatte ([Det jeg ønsker](/nb/user/staff/#det-jeg-ønsker)). Hver
  preferanse behovet går imot, trekker det ned, mer jo mer den betyr for personen, og
  *Trukket ned av* oppgir typene — aldri hva personen skrev. Et behov oppgir bare timene
  per uke, så i dag er det **Flest timer i uken** som kan trekke det ned;
- **Dekker behovet** når personen har tid, har alle kravene og ingen merknad;
- *Sertifikatet utløper* med dagen og sertifikatet, når et sertifikat som kreves, går ut
  før behovets siste dag;
- *På PROSJEKT i N dager* — prosjektet personen har en bekreftet allokering på i dag, og
  hvor lenge hen har vært på det;
- *Tidligst ledig DATO* — en bekreftet allokering for personen der
  [vilkårene](#vilkår) først gjør hen ledig etter behovets første dag, og ved siden av
  *Overlapping fra DATO* når vilkårene oppgir overlappingsdager: dagen hen bør begynne å
  overlappe med en etterfølger. Personen tilbys likevel — det kan forhandles om når hen
  blir ledig;
- *Erstatning krever kundens godkjenning* — ved siden av en ledig dag etter starten, når
  vilkårene på den allokeringen sier det;
- *Ønsker seg av prosjektet fra DATO* eller *Ønsker seg noe nytt fra DATO* — et åpent
  [ønske](#mine-ønsker) fra personen, fra behovets første dag eller før: om noe nytt,
  eller om å gå av prosjektet hen er på i dag.

De som har tid, kommer først, så de som har ønsket seg en endring, så etter hvor godt
det passer ønskene, så etter ledige timer, og blant like den som har vært lengst på sitt
nåværende prosjekt først, så planleggeren kan la noen gå videre.

En person med [karantene](#karantenen) mot prosjektets kunde fra behovets første dag
vises ikke i det hele tatt, heller ikke under **Vis delvise treff**.

**Vis delvise treff** viser også dem som mangler et krav, med *Mangler* og kompetansene
i rødt. Når ingen passer, sier panelet **Ingen passer til dette behovet**.

Slik fyller du behovet:

1. Velg **Fyll som** — **Tentativ** eller **Bekreftet**. Det starter tentativt på et
   prosjekt med statusen **Planlagt**, bekreftet på alle andre.
2. Trykk **Fyll** på personens kort.

Å fylle setter personen opp på prosjektet i behovets periode med timene per uke, med
behovets tittel som rolle, og merker behovet **Fylt**. Allokeringen er som alle andre:
den vurderes slik allokeringsskjemaet vurderer den, gir varsel — *Fylt — men se på
dette* — eller avvises under **Kunne ikke fylle behovet** av de samme grunnene, og er den
bekreftet, får personen e-post. Endre allokeringen som enhver annen, og behovet forblir
fylt. **Slett** allokeringen, og behovet er åpent igjen.

Et fylt eller tilbaketrukket behov viser fortsatt kandidatene — hvem andre som kunne ha
fylt det — men tilbyr ingen **Fyll**.

## Inntak

**Inntak** (`planning:view`) er pipelinen: hvert prosjekt med statusen **Planlagt**,
hvert med kunden og datoene — trykk navnet for å åpne prosjektplanen — og tre tall i
timer per uke: **Etterspurt** av de åpne behovene, **Tentativt** og **Bekreftet** av
allokeringene.
Under dem står de åpne og fylte behovene, hvert med perioden, timene per uke og
**Dekning**:

- **Dekket** — behovet er fylt, eller minst én person dekker det: har tid, har alle
  kravene og ingen merknad.
- **Delvis dekket** — det finnes folk som har alle kravene, men ingen dekker det: de har
  for lite tid, et sertifikat som kreves, går ut i perioden, eller de har en annen
  merknad — ledig først etter at behovet begynner, eller et ønske.
- **Udekket** — ingen som er ansatt gjennom hele perioden, har alle kravene.

**Nøkkelperson** markerer et åpent behov nøyaktig én person dekker: blir den personen
tatt, kan ingen andre. På et åpent behov oppgir *Kandidater som dekker det* hvor mange.
En planlegger trykker **Kandidater** for å fylle det herfra.

Et prosjekt **På vent** er ikke i pipelinen, og et tilbaketrukket behov vises ikke. Når
ingen prosjekter er planlagt, sier siden **Ingen planlagte prosjekter**.

## Prosjektplanen

Prosjektplanen (`planning:view`) åpnes fra prosjektets navn på planen gruppert etter
**Prosjekter**, i Behov eller i Inntak. Overskriften viser prosjektet med statusen,
kunden og datoene.

- **Timer per uke** — prosjektets **Bekreftet**- og **Tentativt**-timer hver uke i tolv
  uker fra denne uken, og **Etterspurt**: timene de åpne behovene fortsatt ber om. Et
  behov teller timene per uke i hver uke det dekker, og en uke det dekker delvis etter
  arbeidsdagene — et behov fra en onsdag teller tre femtedeler av timene den første
  uken, og en helligdag teller ingenting, akkurat som en allokering i timer per uke
  ikke booker noe på en.
  Under tallene står allokeringene som stolper med personens navn, og de åpne behovene
  som oransje stolper med tittelen.
- **Allokeringer** — hver allokering på prosjektet i de ukene: personen (med
  planleggernes notat under navnet, for en planlegger), rollen, perioden, belastningen
  og statusen. En planleggers handlingsmeny på hver rad har **Endre**, **Bekreft** (på en
  tentativ) og **Slett**.
- **Behov** — prosjektets åpne og fylte behov i de ukene: tittelen med kravene (og
  planleggernes notat, for en planlegger), perioden, timene per uke og statusen, med hvem
  som fyller et fylt behov. En planlegger trykker **Kandidater** for å
  [fylle et](#fyll-et-behov), og handlingsmenyen har **Endre** på et åpent behov — **Åpne** på
  et fylt eller tilbaketrukket, som viser det uten å kunne endre — og **Trekk tilbake** på
  et åpent.

En planlegger setter noen på prosjektet med **Ny allokering**, og registrerer hva det
trenger med **Nytt behov**, som åpner [behovsskjemaet](#behovsskjemaet) med prosjektet
og ukene som vises som periode.

## En persons tidslinje

En persons tidslinje (**Tidslinje** og navnet) åpnes fra navnet på planen eller i listen
Ubemannet, fra et punkt på dashbordet, eller som **Bemanning**-fanen på personens side i
Ansatte. Den ser fremover: i dag og de 26 ukene etter.

- **Ansettelse** — personens ansettelse som et bånd, med slutten når den er kjent.
- **Fraværsdager** — periodene personen er borte, fra godkjent og registrert fravær i
  Ansatte, uten type og uten andel: en dag delvis borte teller som borte.
- **Allokeringer** — hver allokering som en stolpe, og under stolpene en liste med
  prosjektet og rollen, perioden, belastningen, statusen og **Merknader**:
  - **Etter at ansettelsen slutter** (med siste dag) — allokeringen varer lenger enn
    personens ansettelse. En allokering blir stående når en ansettelse slutter etter at
    den ble satt opp; denne merknaden er hvordan du finner den.
  - **Sertifikatet utløper** (med dagen) — et av personens sertifikater i Ansatte går ut
    under allokeringen. Forny det i Ansatte, eller planlegg for det.

  Listen har også kolonnene **Tidligst ledig** og **Overlapping fra** — datoene
  [vilkårene](#vilkår) på en allokering gir, eller en strek: **Tidligst ledig** vises bare
  når vilkårene flytter den forbi allokeringens siste dag.
- **Tidligst ledig og overlapping** — en rad under stolpene, som finnes når vilkårene på
  en allokering gir en av datoene: **Tidligst ledig** markert på sin dag, og **Overlapping
  fra** på sin. Hold pekeren over en markering for å lese den med prosjektet først —
  *P-100 Havneappen: Tidligst ledig* og datoen.

Under stolpene viser **Åpne ønsker** personens åpne ønsker — *Av PROSJEKT fra DATO*
eller *Noe nytt fra DATO* — hvert med *Sett* og dagen en planlegger merket det, eller
*Ikke sett ennå*. En planlegger (`planning:manage`) ser det på alles tidslinje, og du ser
det på din egen; **Se planen** alene viser ikke en annen persons ønsker.

En planlegger åpner en stolpe for å endre allokeringen. Med `planning:view` ser du hele
tidslinjen til hvem som helst, tentative allokeringer medregnet. Din egen tidslinje, med
bare `planning:access`, viser de bekreftede allokeringene dine og de åpne ønskene dine,
ikke noe annet;
ansettelsen og fraværet ditt står på [Min side](/nb/user/staff/#min-side) i Ansatte.

## Ubemannet

**Ubemannet** (`planning:view`) lister personene med for lite bekreftet arbeid fremover:
alle i ansettelse med bekreftet belastning under terskelen i ukene som kommer. Terskelen
(50 % som standard) og hvor mange uker frem listen ser (seks som standard) står i
[innstillingene](#bemanningsinnstillinger), og sidens beskrivelse oppgir begge med
datoene.

Hver rad har personen (åpne tidslinjen fra navnet) med ansattnummeret, avdelingen,
**Belastning** over de ukene, **Ledige timer** og **Blir ledig** — siste dag i den
seneste bekreftede allokeringen som slutter i de ukene, eller **Ledig nå** når de ikke
har noen. De som er ledige nå, kommer først, deretter etter dagen de blir ledige, så
etter belastning. Den som er borte hele perioden, eller begynner etter den, står ikke i
listen: de er ikke ubemannet, de er borte. Når ingen er under terskelen, sier siden
**Alle er satt opp**.

## Ønsker

**Ønsker** (`planning:manage`) er der planleggerne leser hva personene
[ønsker](#mine-ønsker): hvert åpent ønske, eldste først, med **Person** — trykk navnet for
å åpne tidslinjen — **Type**, **Av** eller **Noe nytt**, **Prosjekt** et ønske om å gå av
prosjektet gjelder (trykk det for å åpne prosjektplanen; en strek på noe nytt), dagen det
gjelder **Fra**, dagen det ble **Registrert**, og **Sett**: dagen en planlegger merket
det, eller et oransje **Nytt**-merke på et ønske ingen har lest.

- **Merk som sett** (på et nytt ønske) merker det som sett — personen ser *Sett av en
  planlegger* og datoen på sin egen side. Det er planleggernes kvittering, ikke et svar.
- **Avslutt** spør *Avslutte ønsket?* og sletter det, når ønsket er håndtert: *Det
  slettes, og loggen beholder typen, prosjektet og datoen.* Personen ser det ikke lenger
  heller.

En planlegger uten **Se ansattoversikten** (`staff:view`) ser merknaden **Ansatte viser
deg ingen** øverst, som i Ubemannet, og hver person står som *En kollega*. Når ingen har
et åpent ønske, sier siden **Ingen åpne ønsker**. Hver planlegger får
e-post når noen registrerer et ønske, og dashbordet teller ønskene ingen har sett
([Usette ønsker](#dashbordet)).

## Min bemanning

**Min bemanning** er hver brukers egen visning, med `planning:access`: det du er satt
opp på fra i dag, slik planleggerne har bekreftet det — ett kort per allokering med
prosjektet, belastningen, rollen din og datoene. Tentative allokeringer er
planleggernes utkast og vises ikke her, og heller ingen notater fra planleggerne. Når en
planlegger bekrefter, endrer eller fjerner en allokering for deg, får du beskjed på
e-post, og e-posten lenker hit.

Sier siden **Ingen person er knyttet til deg**, har Ansatte ingen person knyttet til
brukeren din, så det er ingen å vise allokeringer for. Be HR knytte brukeren din til
personen din.

### Mine ønsker

Under allokeringene dine er **Mine ønsker** der du sier fra til planleggerne at du
gjerne vil ha en endring. Det er to knapper:

- **Jeg ønsker meg av dette prosjektet fra…** åpner *Jeg ønsker meg av et prosjekt*: velg
  **Prosjekt** — en av de bekreftede allokeringene over som du ikke alt ønsker deg av;
  er det bare én, er den valgt for deg — og dagen det gjelder **Fra**, i dag eller
  senere. Når det ikke er noe slikt prosjekt igjen, er knappen grå med *Du har ingen
  bekreftet allokering å ønske deg av.*
- **Jeg ønsker noe nytt fra…** åpner *Jeg ønsker noe nytt*: bare dagen det gjelder
  **Fra**. Du trenger ingen allokering for det.

Trykk **Send**: *Ønsket ditt er sendt til planleggerne*. På mobilen åpnes dialogen
nedenfra.

Det finnes ikke noe felt for hvorfor. Et ønske er et prosjekt og en dato, ikke noe mer:
planleggerne leser hva du vil og fra når, og grunnene er dine å fortelle dem — Bemanning
tar ikke vare på noe du skriver.

Du kan ha høyst fem åpne ønsker, og ett om å gå av hvert prosjekt. Ved fem er begge
knappene grå med *Du har allerede fem åpne ønsker. Trekk tilbake ett før du sender et
nytt.* Endrer noe seg mens dialogen er åpen — en allokering fjernet, et ønske sendt fra
en annen enhet — sier den det med egne ord: *Du ønsker deg allerede av det prosjektet.*
eller *Du har ingen bekreftet allokering på det prosjektet.*

Hvert åpent ønske står i listen — *Av PROSJEKT fra DATO* eller *Noe nytt fra DATO* — med
*Sett av en planlegger* og datoen når en planlegger har lest det, eller *Ikke sett
ennå*. **Trekk tilbake** spør *Trekke tilbake dette ønsket?* og sletter det; planleggerne
ser det ikke lenger. Et ønske en planlegger har avsluttet, er også borte fra listen, og
et som blir avsluttet mens du trekker det tilbake, sier *Ønsket finnes ikke lenger: det
er trukket tilbake eller avsluttet i mellomtiden.* — slik siden **Ønsker** gjør når to
planleggere handler på det samme ønsket.

Når du sender et ønske, får hver planlegger (`planning:manage`) én e-post, på sitt eget
språk, med navnet ditt, prosjektet og dagen, og en lenke til siden **Ønsker**; du får
ingen e-post om ditt eget ønske. Når du er kandidat til et behov, ser planleggeren som
fyller det, ønsket ditt ved navnet ditt.

## Bemanningsinnstillinger

**Bemanningsinnstillinger** (`planning:manage`) har seks felt. Trykk **Lagre
innstillingene** når du er ferdig; lagret en annen planlegger først, lastes siden inn på
nytt, og du gjør endringen din igjen.

- **Overbelastning** — **Varsle** (standard): en allokering som tar en uke over
  personens kapasitet, lagres, og planleggeren varsles om ukene. **Avvis**: en
  allokering som gjør en ukes overbelastning større, avvises, mens en endring som
  beholder eller minsker en som allerede finnes, lagres med varselet.
- **Enhet for en ny allokering** — **Prosent av stillingen** (standard) eller **Timer per
  uke**: enheten skjemaet starter med. Hver allokering kan føres i begge.
- **Ubemannet under (%)** — 1 til 100, 50 som standard: en person med bekreftet
  belastning under dette i horisonten står i listen Ubemannet.
- **Horisont for ubemannede (uker)** — 1 til 52, 6 som standard: hvor mange uker frem
  listen Ubemannet og dashbordets punkter for ubemannet, overbooket og sertifikater ser.
- **Slutter uten oppfølging (uker)** — 1 til 26, 4 som standard: en bekreftet allokering
  som slutter så snart, uten noe bekreftet etter, er et punkt på dashbordet.
- **Varsel for åpne behov (dager)** — 0 til 365, 14 som standard: et åpent behov som
  starter så snart, eller alt har startet, er et punkt på dashbordet for planleggerne.

## Dashbordet

**Hjem**, dashbordet, har et **Bemanning**-kort for alle med `planning:access`; trykk det
for å åpne Bemanning. Med `planning:view` har det tallene:
hvor mange personer som har en bekreftet allokering denne uken, den bekreftede
belastningen deres denne uken med den tentative ved siden av, hvor mange behov som er
åpne, og hvor mange som er ubemannet.

Punktene fra Bemanning kommer sammen med de andre appenes i **Trenger oppfølging**. De
tre første er for `planning:view`, de tre siste for `planning:manage`:

| Punkt | Det betyr | Åpner |
| --- | --- | --- |
| Ubemannet | En person er under terskelen for ubemannet i horisonten — ett punkt per person, datert dagen hen blir ledig, eller i dag. | Ubemannet |
| Overbooket | En persons bekreftede allokeringer er større enn kapasiteten i en uke fra denne uken til slutten av horisonten, med den første slike uken i navnet. | Personens tidslinje |
| Sertifikat utløper under en allokering | Et sertifikat personen har, går ut under en av de bekreftede allokeringene deres i horisonten — ett punkt per allokering og sertifikat, datert dagen det går ut. | Personens tidslinje |
| Slutter uten oppfølging | En bekreftet allokering slutter innen ukene innstillingene oppgir, og personen har ingen bekreftet allokering som fortsetter innen sju dager etter slutten. Med personen og prosjektets kode i navnet. | Plan |
| Åpent behov | Et åpent behov starter innen dagene innstillingene oppgir, eller har alt startet — ett punkt per behov, med prosjektets kode og behovets tittel i navnet, datert første dag. | Behov, filtrert til de åpne |
| Usette ønsker | Ønsker ingen planlegger har merket som sett — ett punkt for alle, *Usette ønsker:* og antallet, datert dagen det eldste av dem ble registrert. | Ønsker |

Et punkt forsvinner når det det teller, gjør det: en allokering satt opp, en
overbelastning rettet, et sertifikat fornyet i Ansatte, et behov fylt eller trukket
tilbake, hvert ønske merket som sett eller avsluttet.

## På personsiden i Ansatte

Når Bemanning-modulen er på, har en persons side i Ansatte en **Bemanning**-fane etter
**Konto**, for dem som har `planning:access` og enten `planning:view` eller er personen
selv. Den viser personens [tidslinje](#en-persons-tidslinje): med `planning:view` hele,
og på din egen side med bare `planning:access` de bekreftede allokeringene dine. En
planlegger endrer en allokering derfra som fra tidslinjen.

## På prosjektsiden

Når Bemanning-modulen er på, har et prosjekts side i Prosjekter en **Bemanning**-fane
etter **Fakturagrunnlag**, for dem som har `planning:access` og `planning:view`. Den
viser prosjektets [plan](#prosjektplanen) — timene per uke med etterspørselen,
allokeringene og behovene — under prosjektsidens egen overskrift, og en planlegger
arbeider derfra som fra prosjektplanen: **Ny allokering**, **Nytt behov**,
**Kandidater**, **Trekk tilbake**. Har du bare `planning:manage`, vises ikke fanen.

## I Kalender

Når Kalender-appen er på, vises dine egne bekreftede allokeringer i
[kalenderen](/nb/user/calendar/) din: hver som et heldagselement gjennom allokeringens
periode, med prosjektets kode og navn som tittel og belastningen når du holder pekeren
over det. De hører til brikken **Allokeringer** i forklaringen, og et klikk på en åpner
**Min bemanning**. De kommer også med i kalenderadressen din, så telefonen viser dem.

De følger Bemannings egen rettighet: du trenger `planning:access` og en person i Ansatte
som er knyttet til brukeren din. Tentative allokeringer, planleggernes utkast, vises
aldri, og heller ikke andres allokeringer — en kollegas kalender viser ingen av deres.

## Hvem ser hva

| Hva | `planning:access` | `planning:view` | `planning:manage` |
| --- | --- | --- | --- |
| Appen, **Min bemanning**, dine egne bekreftede allokeringer og e-posten om dem, **Mine ønsker** og de åpne ønskene dine på din egen tidslinje, allokeringene dine i Kalender | ✓ | ✓ | ✓ |
| **Plan**, hvert prosjekts plan og **Bemanning**-fanen på prosjektsiden, hver persons tidslinje med tentative allokeringer, **Behov**, **Inntak**, **Ubemannet**, tallene og punktene på dashbordet | – | ✓ | ✓ |
| Allokeringsskjemaet med **Vilkår**, behovsskjemaet, **Kandidater** og **Fyll**, **Trekk tilbake**, planleggernes notater, **Ønsker** og de åpne ønskene på alles tidslinje, e-posten om et nytt ønske, **Bemanningsinnstillinger**, punktene *Slutter uten oppfølging*, *Åpent behov* og *Usette ønsker* | – | – | ✓ |

**Se planen** (`planning:view`) og **Planlegge** (`planning:manage`) er sensitive: planen
viser hvert prosjekts kode, navn, kunde, status og datoer — tilbud medregnet, som
Prosjekter ellers bare viser til dem som kan se alle prosjekter. Har du
`planning:manage` uten `planning:view`, får du skjemaene, men ikke planen, Behov eller
Inntak, så en planleggerrolle har begge. Hele tabellen står under
[rettighetene](/en/reference/planning/#permissions) i referansen.

En person Ansatte ikke lenger viser deg — slettet, eller en du ikke kan se — vises som
*Slettet person* eller *En kollega*; allokeringene deres blir stående i planen.
