---
title: Bemanning
description: Hvem som jobber med hva — Planen med personer mot uker, fargene, grupperingene og filtrene, allokeringsskjemaet med varslene og avvisningene, prosjektplanen, en persons tidslinje med merknadene, listen Ubemannet, Min bemanning og e-posten personen får, Bemanningsinnstillinger, kortet på dashbordet og punktene i Trenger oppfølging, og bemanningsfanen på personsiden i Ansatte.
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

Dette er Bemannings første levering: allokeringene, planen, listene og personens egen
visning. Forespørsler etter folk et prosjekt trenger, oppdrag med kontraktsvilkårene og
personens ønsker kommer i senere leveringer.

## Appen og sidemenyen

**Bemanning**-flisen vises for alle med `planning:access`. Sidemenyen viser områdene
rettighetene dine gir deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Plan** (planen) | `planning:view` |
| **Ubemannet** | `planning:view` |
| **Min bemanning** | alle med `planning:access` |
| **Bemanningsinnstillinger** | `planning:manage` |

Appen åpner på **Plan** for dem som kan se den, og på **Min bemanning** for alle andre.
Prosjektplanen og en persons tidslinje har ingen egen oppføring i sidemenyen: du åpner
dem fra planen, fra listen Ubemannet, fra dashbordet eller fra en persons side i
Ansatte.

Planen bygger på personene Ansatte viser deg, så en planlegger trenger også **Se
ansattoversikten** (`staff:view`) fra Ansatte. Uten den later ikke planen som om den er
tom: den sier **Ansatte viser deg ingen** og ber deg få en administrator til å gi deg
rettigheten.

Søket i toppfeltet kjenner skjermbildene over, og tilbyr **Ny allokering** til en
planlegger som har `planning:view` og `planning:manage`: det åpner planen med
allokeringsskjemaet.

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
  personene som er satt opp på ett prosjekt.
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

Bare bekreftede allokeringer veies: en tentativ allokering overbooker aldri noen.

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

## Prosjektplanen

Prosjektplanen (`planning:view`) åpnes fra prosjektets navn på planen gruppert etter
**Prosjekter**. Overskriften viser prosjektet med statusen, kunden og datoene.

- **Timer per uke** — prosjektets **Bekreftet**- og **Tentativt**-timer hver uke i tolv
  uker fra denne uken, med allokeringene som stolper under, med personens navn.
- **Allokeringer** — hver allokering på prosjektet i de ukene: personen (med
  planleggernes notat under navnet, for en planlegger), rollen, perioden, belastningen
  og statusen. En planleggers handlingsmeny på hver rad har **Endre**, **Bekreft** (på en
  tentativ) og **Slett**.

En planlegger setter noen på prosjektet med **Ny allokering** i overskriften.

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

En planlegger åpner en stolpe for å endre allokeringen. Med `planning:view` ser du hele
tidslinjen til hvem som helst, tentative allokeringer medregnet. Din egen tidslinje, med
bare `planning:access`, viser de bekreftede allokeringene dine og ikke noe annet;
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
- **Varsel for åpne forespørsler (dager)** — 0 til 365, 14 som standard: for
  forespørslene som kommer i en senere levering. Den gjør ingenting ennå.

## Dashbordet

**Hjem**, dashbordet, har et **Bemanning**-kort for alle med `planning:access`; trykk det
for å åpne Bemanning. Med `planning:view` har det tallene:
hvor mange personer som har en bekreftet allokering denne uken, den bekreftede
belastningen deres denne uken med den tentative ved siden av, og hvor mange som er
ubemannet.

Punktene fra Bemanning kommer sammen med de andre appenes i **Trenger oppfølging**. De
tre første er for `planning:view`, det siste for `planning:manage`:

| Punkt | Det betyr | Åpner |
| --- | --- | --- |
| Ubemannet | En person er under terskelen for ubemannet i horisonten — ett punkt per person, datert dagen hen blir ledig, eller i dag. | Ubemannet |
| Overbooket | En persons bekreftede allokeringer er større enn kapasiteten i en uke fra denne uken til slutten av horisonten, med den første slike uken i navnet. | Personens tidslinje |
| Sertifikat utløper under en allokering | Et sertifikat personen har, går ut under en av de bekreftede allokeringene deres i horisonten — ett punkt per allokering og sertifikat, datert dagen det går ut. | Personens tidslinje |
| Slutter uten oppfølging | En bekreftet allokering slutter innen ukene innstillingene oppgir, og personen har ingen bekreftet allokering som fortsetter innen sju dager etter slutten. Med personen og prosjektets kode i navnet. | Plan |

Et punkt forsvinner når det det teller, gjør det: en allokering satt opp, en
overbelastning rettet, et sertifikat fornyet i Ansatte.

## På personsiden i Ansatte

Når Bemanning-modulen er på, har en persons side i Ansatte en **Bemanning**-fane etter
**Konto**, for dem som har `planning:access` og enten `planning:view` eller er personen
selv. Den viser personens [tidslinje](#en-persons-tidslinje): med `planning:view` hele,
og på din egen side med bare `planning:access` de bekreftede allokeringene dine. En
planlegger endrer en allokering derfra som fra tidslinjen.

## Hvem ser hva

| Hva | `planning:access` | `planning:view` | `planning:manage` |
| --- | --- | --- | --- |
| Appen, **Min bemanning**, dine egne bekreftede allokeringer og e-posten om dem | ✓ | ✓ | ✓ |
| **Plan**, hvert prosjekts plan, hver persons tidslinje med tentative allokeringer, **Ubemannet**, tallene og punktene på dashbordet | – | ✓ | ✓ |
| Allokeringsskjemaet, planleggernes notater, **Bemanningsinnstillinger**, punktet *Slutter uten oppfølging* | – | – | ✓ |

**Se planen** (`planning:view`) og **Planlegge** (`planning:manage`) er sensitive: planen
viser hvert prosjekts kode, navn, kunde, status og datoer — tilbud medregnet, som
Prosjekter ellers bare viser til dem som kan se alle prosjekter. Har du
`planning:manage` uten `planning:view`, får du skjemaet, men ikke planen, så en
planleggerrolle har begge. Hele tabellen står under
[rettighetene](/en/reference/planning/#permissions) i referansen.

En person Ansatte ikke lenger viser deg — slettet, eller en du ikke kan se — vises som
*Slettet person* eller *En kollega*; allokeringene deres blir stående i planen.
