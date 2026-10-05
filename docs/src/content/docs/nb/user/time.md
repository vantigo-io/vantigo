---
title: Timer
description: Føre timer på prosjekter, sende inn uken, godkjenne og låse perioder.
sidebar:
  order: 30
sources:
  - apps/time/frontend
---

Timer-appen er der du fører timene dine på prosjekter, sender en uke til godkjenning og
— om det er din jobb — godkjenner andres timer, følger ukene til alle og stenger
perioder. Sidemenyen har inntil fire områder: **Min uke** for alle med `time:access`,
**Godkjenning** for `time:approve`, **Personer** for `time:view-all` og
**Timeinnstillinger** for `time:manage`. Reglene bak hvert skjermbilde står i
[referansen for Timer](/en/reference/time/); denne siden sier hvor du skal klikke.

En føring er alltid én persons timer på ett prosjekt på én dag, eventuelt på en av
prosjektets faktureringslinjer og en av oppgavene dine. Den går gjennom fem statuser:
**Utkast**, **Sendt inn**, **Godkjent**, **Avvist** og **Fakturert**. Du kan bare endre
et utkast eller en avvist føring; se
[tilstandsmaskinen](/en/reference/time/#the-state-machine).

## Før timer på uken din

Åpne **Timer → Min uke**. Siden viser én uke, mandag til søndag, med en rad for hvert
prosjekt, hver linje og hver oppgave du fører timer på, og en kolonne for hver dag. Bruk
pilene for å gå til **Forrige uke** eller **Neste uke**, og **Denne uken** for å komme
tilbake; uken ligger også i adresselinjen, så en lenke til en hvilken som helst dato
åpner uken den hører til.

En uke uten noe ført sier «Ingenting ført denne uken». Legg til en rad først:

- **Legg til rad** åpner *Legg til en rad*: velg et **Prosjekt** (et aktivt prosjekt du
  har en rolle på — «Du har ingen rolle på et aktivt prosjekt.» betyr at det ikke er noe
  å velge), eventuelt en av de aktive linjene under **Linje**, og eventuelt en av de
  åpne oppgavene dine på det under **Oppgave**. Så **Legg til**.
- **Fra mine oppgaver** legger til en rad for hver åpne oppgave du har på et aktivt
  prosjekt du fører timer på, i én operasjon. Finnes det ingen, sier siden «Ingen
  oppgaver å legge til».

En rad du legger til finnes bare på siden til en av dagene får timer. Det finnes ingen
ukemaler eller kopiering av føringer; **Fra mine oppgaver** er snarveien.

### Skriv timer inn på en dag

Klikk på cellen til en rad under en dag og skriv varigheten som `7.5`, `7,5` eller
`7:30` — mer enn 0 og høyst 24 — og trykk Enter eller forlat cellen. Føringen lagres
som utkast med en gang; Escape setter den lagrede verdien tilbake, og tømmer du cellen
slettes føringen. En verdi som ikke er en varighet avvises med «Ikke en varighet», og
cellen går tilbake til det som var lagret. Summene for raden og dagen, og **Sum for
uken**, følger med.

En celle farges etter statusen til føringen så snart den er forbi utkast, og holder du
pekeren over, får du vite hvorfor en celle ikke kan skrives i:

- «Denne dagen er låst.» — dagen er før [periodelåsen](#steng-en-periode).
- «N føringer denne dagen — endre dem i dagsvisningen.» — rutenettet viser summen av
  flere føringer; klikk på cellen for å åpne dagen.
- «08:00–16:00 — en føring med start- og sluttid endres i dagsvisningen.» — det samme
  for en føring ført etter klokken.
- «Sendt inn» eller «Godkjent» — føringen er ute av dine hender til en godkjenner
  handler.
- «Avvist: …» — begrunnelsen godkjenneren ga; se
  [rette opp avviste timer](#rett-opp-avviste-timer).

Tjeneren avviser timer som ville tatt én dag over 24, og alt som er datert før
periodelåsen; avvisningen vises som «Kunne ikke lagre timene» med grunnen.

### Før timer med start og slutt, notat eller arbeidstype

Klikk på overskriften til en dag i rutenettet (eller åpne **Timer → Min uke** og følg
kolonneoverskriften) for å åpne **dagsvisningen**: alt du har ført den dagen som en
liste, med start- og sluttider og notater, en **Sum for dagen**, og piler for
**Forrige dag**, **Neste dag** og **I dag**. **Uke** tar deg tilbake til rutenettet.

**Legg til føring** åpner *Før timer*:

| Felt | Hva du fyller inn |
| --- | --- |
| **Prosjekt** | Påkrevd. De aktive prosjektene du har en rolle på. |
| **Linje** | En av prosjektets aktive faktureringslinjer, eller «Ingen linje». |
| **Oppgave** | En av de åpne oppgavene dine på det prosjektet, eller «Ingen oppgave». |
| **Arbeidstype** | Vises bare når prosjektet har en aktiv arbeidstype; «Ordinære timer» er det tomme valget. Bytter du prosjekt, nullstilles den. |
| **Dato** | Påkrevd; forhåndsutfylt med dagen du åpnet. |
| **Start** og **Slutt** | Begge eller ingen. Slutt må være etter start, samme dag. |
| **Timer** | Påkrevd med mindre du oppga start og slutt — da er de «Regnet ut fra start- og sluttid.» og kan ikke skrives inn. |
| **Notat** | Fritekst, høyst 2000 tegn. |
| **Fakturerbar** | Vises bare på et prosjekt som fakturerer; på som standard for en ny føring. |

**Lagre** lagrer føringen og viser «Timene er lagret». En avvisning tjeneren knytter til
et felt — en arbeidstype som ikke lenger er aktiv, en dag over 24 timer — havner på det
feltet; alt annet vises som «Kunne ikke lagre timene».

**Hva fakturerbar betyr.** En fakturerbar føring prises av
[priskjeden](/en/reference/time/#the-rate-chain) — linjens regel, prosjektets
standardpris, kundens eller ditt eget priskort — og prisen fryses når du sender inn. En
ikke-fakturerbar føring får ingen fakturapris, bare kostprisen sin. På et prosjekt som
ikke fakturerer, er hver føring ikke-fakturerbar uansett hva du velger. En
**arbeidstype** beholder sin egen faktor ved siden av den prisen, vist som et merke
etter radens kode og, der du får se pengene, som en linje som «900 × 150 % = 1 350,00»;
se [arbeidstypens faktor](/en/reference/time/#the-work-types-multiplier). Arbeidstyper
defineres på prosjektet, i Prosjekter, ikke her.

### Rediger eller slett en føring

I dagsvisningen er hver føring et kort med timene, statusmerket, arbeidstypen, tidene,
notatet og — når den er avvist — begrunnelsen i rødt. Så lenge føringen er et utkast
eller avvist og dagen ikke er låst, åpner blyanten (**Rediger føringen**) *Rediger
timer* med de samme feltene som over, og søppelkassen (**Slett føringen**) spør «Slette
føringen?» før den fjernes: «Det kan ikke angres.» I rutenettet redigerer du føringen
ved å skrive over cellen og sletter den ved å tømme den, men bare for en dag med én
føring uten klokkeslett.

## Send inn uken din

Når uken er komplett, trykker du **Send inn uken** øverst i **Min uke**. Dialogen
*Sende inn uken?* sier hva som skjer: «Alle utkast fra mandag til søndag går til
godkjenning. En innsendt føring kan bare endres igjen hvis den blir avvist.» Bekreft
med **Send inn**.

Hvert utkast i uken blir **Sendt inn**, prisene fryses, og uken viser et merke «Sendt
inn …» med dato og klokkeslett. Avviste føringer røres ikke — de trenger en redigering
først — og en tom uke kan også sendes inn, for å si at du ikke jobbet noe. Et utkast
datert før periodelåsen avviser hele uken med «Kunne ikke sende inn uken», og ingenting
sendes inn.

Fører du flere timer i en innsendt uke, varsler siden «Endret etter at du sendte den
inn»: de utkastene var ikke med i innsendingen, så trykk **Send inn uken** igjen for å
sende dem. Se [ukentlig innsending](/en/reference/time/#weekly-submission).

**Du kan ikke trekke tilbake en innsending selv.** Når en føring er sendt inn, er den
ute av dine hender til en godkjenner handler: be dem avvise den (som gir den tilbake til
deg med en begrunnelse) eller, når den er godkjent, trekke tilbake godkjenningen. Begge
deler er beskrevet under [tilstandsmaskinen](/en/reference/time/#the-state-machine).

## Rett opp avviste timer

En avvist føring vises rød i rutenettet, med «Avvist: …» og godkjennerens begrunnelse
når du holder pekeren over, og i dagsvisningen. Rediger den — i rutenettet eller med
**Rediger føringen** — så blir den et utkast igjen; uken viser da «Endret etter at du
sendte den inn», og du trykker **Send inn uken** en gang til. Å slette den er den andre
veien ut.

## Godkjenn andres timer

Åpne **Timer → Godkjenning**. Sidemenyen tilbyr den til alle med `time:approve`, som
godkjenner timene på alle prosjekter. Styrer du et prosjekt, godkjenner du timene på det
gjennom rollen alene: du når den samme siden fra oppmerksomhetslisten på dashbordet
eller på `/time/approvals`. Den som ikke godkjenner noe, ser «Du godkjenner ingens
timer».

Køen er ett kort per person og uke, eldste uke først, med personens navn, ukens mandag
og timene. «Ingenting venter på godkjenning» betyr at køen er tom. Føringer datert før
periodelåsen holdes utenfor, så siden tilbyr aldri noe tjeneren ville avvist.

- **Vis føringene** åpner uken: en rad per føring med **Dato**, prosjekt, linje og
  oppgave, merke for arbeidstype, **Timer**, **Fakturabeløp** (prisen ganger timene,
  om du får se prosjektets penger; «—» når det ikke finnes noen pris; «Ikke
  fakturerbar» for en ikke-fakturerbar føring) og **Notat**.
- **Godkjenn** eller **Avvis** på et kort virker på hele uken; de samme knappene på en
  rad virker på én føring.
- Huk av boksen på et kort eller en rad for å bygge et utvalg på tvers av uker, og
  trykk **Godkjenn N valgte** eller **Avvis N valgte**; **Nullstill utvalget** tømmer
  det.

**Å godkjenne** merker føringene **Godkjent** og viser «Timene er godkjent» med
antallet. **Å avvise** åpner *Avvise timene?* og ber om en **Begrunnelse** — påkrevd,
høyst 1000 tegn: «Personen ser dette sammen med hver føring du avviser.» Bekreft med
**Avvis**.

Hver bunt er alt eller ingenting: kan én føring i den ikke lenger godkjennes — låsen er
flyttet, noen andre handlet først — endres ingenting, og «Kunne ikke godkjenne timene»
lister hver føring som ble avvist og hvorfor.

**Hva personen ser etterpå.** En godkjent føring blir grønn og skrivebeskyttet i
rutenettet deres. En avvist blir rød, bærer begrunnelsen din overalt der den vises, og
kan redigeres igjen; når de redigerer den, er den et utkast, og de sender inn uken på
nytt, hvorpå den er tilbake i køen din.

**Trekke tilbake en godkjenning.** Godkjente føringer ligger ikke lenger i køen. I
dagsvisningen viser en godkjent føring du har lov til å trekke tilbake **Trekk tilbake
godkjenningen**; den går tilbake til et nytt utkast, og uken dens melder om endringer
som ikke er sendt inn. Periodelåsen holder dette tilbake for alle unntatt
`time:manage`, og en fakturert føring kan ikke røres i det hele tatt.

**Fakturerte timer.** En godkjent føring blir **Fakturert** når en faktura som
fakturerer den, utstedes i Fakturaer, og fra da av kan ingen redigere, godkjenne eller
trekke den tilbake her. Bare en kreditnota som tar tilbake fakturalinjen den ble
fakturert på, gjør den **Godkjent** igjen, med godkjenningen som den var; å trekke
tilbake godkjenningen er ingen vei rundt det.

## Se ukene til alle

Åpne **Timer → Personer** (`time:view-all`). Tabellen har en rad per person som har
ført en time eller sendt inn en uke i vinduet, og en kolonne per uke, nyeste sist. Hver
celle viser timene og hvor langt uken har kommet: et merke **Sendt inn**, «N t
godkjent» og «N avvist». Velg vinduet under **Uker**, fra «1 uke» til «12 uker»; fire
er standard, og valget blir liggende i adresselinjen, så en lenke viser de samme ukene.

«Ingen har ført timer disse ukene» betyr at vinduet er tomt. Uten rettigheten sier
siden «Du kan ikke se timene til alle».

## Steng en periode

Åpne **Timer → Timeinnstillinger** (`time:manage`). Kortet **Periodelås** har én dato,
**Låst før**: hver dag strengt før den er stengt, datoen selv er åpen, og «La den stå
tom for ingen lås.»

1. Skriv eller velg datoen, eller trykk **Fjern låsen** i feltet for å fjerne den.
2. **Lagre låsen** blir aktiv når datoen skiller seg fra den lagrede. Den spør *Endre
   låsen?* og staver ut virkningen: «Alle dager før … stenges: ingen andre enn en
   timeansvarlig kan føre, endre, sende inn, godkjenne eller avvise timer datert før
   den.» — eller «Låsen fjernes og alle dager er åpne igjen.»
3. Bekreft med **Lagre**; siden viser «Låsen er lagret».

Fra da av sier **Min uke** «Dager før … er låst og kan ikke lenger endres.» på en uke
som begynner før låsen, dagsvisningen skjuler **Legg til føring** på en låst dag, og
godkjenningskøen holder de føringene utenfor. Bare `time:manage` kan fortsatt endre
timer datert før låsen. Se [periodelåsen](/en/reference/time/#the-period-lock).

## Sett timeprisene til en person

Kortet **Timepriser** på den samme siden lister hver person som har et kort, med
kortene deres nyeste først: **Gjelder fra**, **Fakturapris**, **Kostpris** og
**Valuta**. «Ingen timepriser ennå» betyr at ingen er priset.

- **Legg til pris** åpner *Legg til en timepris*. Søk opp **Person** etter navn — «Søk
  opp hvem som helst som jobber her, uansett om de har ført en time ennå.» — og velg
  første dag kortet gjelder under **Gjelder fra**. Oppgi en **Fakturapris**, en
  **Kostpris** eller begge, hver større enn null, og en **Valuta** på tre bokstaver
  (NOK er forhåndsutfylt). **Lagre**.
- **Legg til en pris for …** på gruppen til en person åpner det samme skjemaet med
  personen fylt inn.
- Blyanten (**Rediger prisen**) endrer kortets dato, priser og valuta; personen kan
  ikke endres.
- Søppelkassen (**Slett prisen**) spør «Slette prisen?»: «Timer som allerede er sendt
  inn beholder prisen de ble lagret med; et utkast prises på nytt neste gang det
  lagres.»

Et kort priser personens timer fra datoen sin til neste kort overtar, og bare mens en
føring er et utkast eller avvist; når den er sendt inn, er prisen frosset. Et kort i en
valuta prosjektet ikke fakturerer i gir ingen fakturapris. Se
[priskjeden](/en/reference/time/#the-rate-chain). Én person kan bare ha ett kort per
dato; et nummer to avvises på **Gjelder fra**.

## Timer andre steder i Vantigo

Siden til et prosjekt har en fane **Timer** med timene som er ført på det etter status,
linje og person — med fakturert beløp for dem som får se prosjektets penger — og en
lenke **Før timer** til Min uke. Timer-kortet på dashbordet viser timene dine denne
uken og hva som venter på din godkjenning; søket har en hurtighandling **Før timer**.

## Hvem kan gjøre hva

| Rettighet | Hva den åpner |
| --- | --- |
| `time:access` | Appen og **Min uke**: dine egne timer på prosjektene du er medlem av eller styrer. Alt annet krever den også. |
| `time:approve` | **Godkjenning** i sidemenyen, for alle prosjekter. En prosjektleder godkjenner sine egne prosjekter uten den. |
| `time:view-all` | **Personer**, alles føringer, og kostpriser. |
| `time:manage` | **Timeinnstillinger**: timepriser, periodelåsen, å trekke tilbake godkjenninger, og å endre timer etter låsen. |

Hele tabellen, og hvem som får se fakturapriser og kostpriser, står under
[synlighet og rettigheter](/en/reference/time/#visibility-and-permissions).
