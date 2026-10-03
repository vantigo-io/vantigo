---
title: Prosjekter
description: Prosjekter per kunde, deltakerne, oppgavene, milepælene og økonomien.
sidebar:
  order: 20
sources:
  - apps/projects/frontend
---

Appen **Prosjekter** er der arbeidet organiseres: et prosjekt er et kodet stykke
arbeid for én kunde (eller et internt), personene som kan handle på det, oppgavene
dets, og faktureringsreglene appene Timer, Utlegg og Fakturaer leser senere. De tre
områdene — **Prosjekter**, **Mine oppgaver** og **Prosjektøkonomi** — krever alle
rettigheten `projects:access`. Inne i et prosjekt avgjøres hva du kan gjøre av
prosjektrollen din (**Prosjektleder**, **Deltaker** eller **Leser**) og noen få
globale rettigheter, listet i
[Roles and permissions](/en/reference/projects/#roles-and-permissions).

## Finn et prosjekt

Åpne **Prosjekter** i sidemenyen. Siden åpner med tre tellere — **Aktive**,
**Planlagte** og **På vent** — og en tabell med **Kode**, **Navn**, **Kunde**,
**Status**, **Prosjektledere** og **Datoer**. Et internt prosjekt viser merket
**Internt** i stedet for en kunde. Klikk på en kode for å åpne prosjektet.

Verktøylinjen avgrenser listen, og hvert valg havner i adressefeltet, så en filtrert
liste overlever en oppfrisking og kan sendes som lenke: søkefeltet (**Søk etter kode
eller navn…**), **Status** (**Alle statuser** når det er tomt), **Prosjekttype**
(**Kundeprosjekter** eller **Interne prosjekter**), **Kunde**, søkt mens du skriver,
og bryteren **Mine prosjekter**, som bare beholder prosjektene du har en rolle i.

Du ser bare prosjektene du har en rolle i, med mindre `projects:view-all` eller
`projects:manage-all` viser deg alle; en lenke til et annet svarer **Fant ikke prosjektet**.

## Opprett et prosjekt

Klikk **Nytt prosjekt** på Prosjekter-siden (det krever `projects:create`;
hurtighandlingen **Opprett prosjekt** i søket åpner det samme skjemaet). Fyll inn:

- **Kunde** — søk og velg én, eller velg **Internt prosjekt**, det første
  alternativet. En tom velger avvises med *Velg en kunde, eller velg Internt
  prosjekt*: et internt prosjekt er alltid et uttrykkelig valg.
- **Prosjektnavn**, påkrevd.
- **Prosjektkode** — 2–20 bokstaver og tall, gjort om til store bokstaver mens du
  skriver, unik i hele installasjonen. Mens du skriver navnet foreslår skjemaet en
  kode av kundens og prosjektets forbokstaver og et løpenummer; klikk **Bruk
  forslaget** for å ta det, eller skriv din egen (se
  [The suggestion](/en/reference/projects/#the-suggestion)). En kode som er i bruk
  avvises på feltet.
- **Beskrivelse**, **Startdato** og **Sluttdato**; slutten kan ikke være før starten.
- **Faktureringstype**: **Løpende timer**, **Fastpris** eller **Ikke fakturerbart**.
  **Internt prosjekt** låser den til **Ikke fakturerbart** — ingen kan faktureres.
- **Budsjetterte timer**, planleggingsbudsjettet alle på prosjektet kan se.
- Beløpene bare prosjektledere og de med `projects:view-financials` eller
  `projects:manage-all` kan lese: **Fastprisbeløp** (vises og kreves bare på et
  fastprisprosjekt), **Budsjettbeløp**, **Standard timepris** og **Valuta** (tre
  bokstaver, NOK som standard, påkrevd så snart et beløp er satt).

Klikk **Opprett**. Du blir prosjektets **Prosjektleder**, det dukker opp med statusen
**Planlagt**, og tidslinjen noterer *Prosjektet ble opprettet som KODE*
(se [Domain model](/en/reference/projects/#domain-model)).

## Les prosjektsiden

Et prosjekt åpner på toppfeltet sitt — kode og navn, et statusmerke og en lenke til
kunden eller merket **Internt** — over en rad med faner. Toppfeltet står på alle
fanene; en prosjektleder finner også **Rediger** og **Endre status** der.

| Fane | Hva den viser | Hvem ser den |
| --- | --- | --- |
| **Oversikt** | **Prosjektdetaljer** (beskrivelse, datoer, faktureringstype, budsjetterte timer, prosjektledere) og **Tidslinje** over hver endring, aldri med et beløp | alle som ser prosjektet |
| **Oppgaver** | prosjektets oppgaver som liste eller tavle | alle som ser prosjektet |
| **Deltakere** | hvem som er på prosjektet og i hvilken rolle | alle som ser prosjektet |
| **Fakturering** | det økonomiske sammendraget, fakturalinjer og arbeidstyper | prosjektledere, `projects:view-financials`, `projects:manage-all` |
| **Økonomi** | budsjett mot ført arbeid, kostnader og faktureringsplanen | alle; beløp bare med økonomiske rettigheter |
| **Timer** | timene som er ført på prosjektet, fra Timer-appen | `time:access`, når Timer er aktivert |
| **Utlegg** | utleggene som er ført på prosjektet, fra Utlegg-appen | `expenses:access`, når Utlegg er aktivert |

## Rediger et prosjekt

Klikk **Rediger** i toppfeltet (prosjektledere og `projects:manage-all`). Skjemaet er
det du opprettet prosjektet med; **Lagre endringer** erstatter hvert felt med det
skjemaet inneholder. Endrer du **Prosjektkode**, spør skjemaet først *Vil du endre
prosjektkoden?*, fordi timelister og fakturalinjer viser til den; den gamle står på tidslinjen.

**Valuta** kan ikke fjernes så lenge et beløp, en linje med fast beløp, et
linjebudsjett eller en milepæl er satt, og kan ikke byttes så lenge en linje med fast
beløp, et budsjettbeløp på en linje eller en åpen milepæl finnes; lagringen avvises på
feltet til disse er priset om, fjernet eller avlyst (se
[Domain model](/en/reference/projects/#domain-model)). Har noen lagret prosjektet mens
skjemaet ditt sto åpent, avvises lagringen med *Prosjektet ble endret av noen andre.
Last det inn på nytt og prøv igjen.*

## Endre status, avslutt eller avbryt et prosjekt

Klikk **Endre status** i toppfeltet (prosjektledere og `projects:manage-all`) og velg
**Planlagt**, **Aktivt**, **På vent**, **Fullført** eller **Avbrutt**; den gjeldende
er grået ut. Alle overganger er tillatt, gjenåpning inkludert, og hver skrives til
tidslinjen. Bare et **Aktivt** prosjekt er åpent for arbeid: Timer-appen avviser timer
på enhver annen status, og budsjettvarsler reises bare for aktive prosjekter. Oppgaver
kan fortsatt redigeres uansett status. Prosjekter slettes eller arkiveres aldri: å
avslutte ett er **Fullført**, å gi det opp er **Avbrutt**, og begge beholder
prosjektet og alt som er ført mot det lesbart, fordi andre apper viser til dem.

## Administrer deltakerne på et prosjekt

Åpne fanen **Deltakere**. **Deltakere på prosjektet** lister hver person med sin
**Rolle**; en deaktivert konto bærer merket **Inaktiv**. En prosjektleder (eller
`projects:manage-all`) får i tillegg **Legg til deltaker** — velg en **Person** (søk
etter navn eller e-post blant aktive brukere som ikke er på prosjektet) og en
**Rolle** — rollevelgeren på hver rad, som endrer rollen med én gang, og fjern-ikonet,
som spør *Vil du fjerne navn?* og advarer om at tilgangen går tapt mens ført arbeid står.

Rollene utvides i rekkefølge: en **Leser** ser prosjektet, deltakerne, oppgavene og
tidslinjen; en **Deltaker** skriver i tillegg oppgaver, sjekklistepunkter og
kommentarer, og kan føre timer; en **Prosjektleder** ser dessuten beløpene, redigerer
prosjektet, endrer statusen og administrerer deltakere, linjer, arbeidstyper og
milepæler. Én rolle per person per prosjekt.

## Arbeid med oppgaver

Åpne fanen **Oppgaver**. Bryteren **Visning** viser oppgavene som en **Liste** —
gruppene **Å gjøre**, **Pågår** og **Ferdig**, hver en tabell med **Tittel**,
**Ansvarlig**, **Frist**, **Estimat**, **Sjekkliste** og **Kommentarer**, med
deloppgavene foldet inn under den overordnede — eller som en **Tavle** med tre
kolonner der deloppgaver er egne kort. Valget huskes i nettleseren din. Deltakere og
prosjektledere ser **Ny oppgave**; en leser får de samme visningene uten handlingene.

**Ny oppgave** åpner skjemaet **Ny oppgave**: **Tittel** (påkrevd, høyst 200 tegn),
**Beskrivelse**, **Status**, **Ansvarlig** (hvem som helst på prosjektet),
**Startdato**, **Frist** (ikke før starten), **Estimat (timer)** (over 0 når det er
satt) og **Overordnet oppgave**, som gjør den til en deloppgave (ett nivå ned).

Klikk på en oppgave for å åpne skuffen **Oppgave**: blyanten redigerer tittelen på
stedet, søppelkurven sletter oppgaven etter *Vil du slette tittel?* — deloppgaver,
sjekkliste og kommentarer slettes med den, og det kan ikke angres. Detaljskjemaet
(**Status**, **Ansvarlig**, **Startdato**, **Frist**, **Estimat (timer)**,
**Beskrivelse**) lagres med **Lagre endringer**. **Deloppgaver** har en
avkrysningsboks per deloppgave som setter den til **Ferdig** eller tilbake til **Å
gjøre**, og **Legg til deloppgave**. **Sjekkliste** er de små stegene inne i oppgaven
(**Legg til punkt**, kryss av, slett). **Kommentarer** er en tråd med **Legg inn
kommentar** og **Last inn flere**; du redigerer og sletter dine egne, en
prosjektleder sletter alles. På tavlen tilbyr kortets meny **Flytt til Å gjøre**,
**Flytt til Pågår** og **Flytt til Ferdig**; **Ferdig** stempler fullføringen, flytting
tilbake fjerner den. En oppgave noen andre har lagret imens avvises med *Oppgaven ble
endret av noen andre. Last den inn på nytt og prøv igjen.* Oppgaver følger prosjektets
roller, ikke en rettighet, og låses aldri av prosjektets status — se
[Tasks](/en/reference/projects/#tasks).

## Se dine egne oppgaver

**Mine oppgaver** i sidemenyen lister hver åpen oppgave som er tildelt deg på tvers
av prosjektene du kan se, etter frist og deretter prosjekt: **Prosjekt**, **Tittel**,
**Status**, **Frist** og **Estimat**. Koden åpner prosjektet, tittelen åpner oppgaven
i skuffen. **Status**-velgeren på en rad endrer statusen direkte — og siden
**Ferdig** ikke er åpen, er det slik en oppgave forlater listen. Er ingenting
tildelt, sier siden **Ingenting er tildelt deg**. Hurtighandlingen **Opprett
oppgave** i søket lander her: den spør først etter **Prosjekt**, og åpner så det
vanlige skjemaet **Ny oppgave** på det.

## Sett opp fakturalinjer

Åpne fanen **Fakturering**. Den krever økonomiske rettigheter på prosjektet —
prosjektleder, `projects:view-financials` eller `projects:manage-all`; alle andre
leser *Du kan ikke se beløpene på dette prosjektet*. Den åpner med **Økonomisk
sammendrag** (**Faktureringstype**, **Fastprisbeløp**, **Budsjettbeløp**, **Standard
timepris**, **Valuta**) og kortet **Fakturalinjer**, med **Sporingskode**,
**Produkt**, **Enhet**, **Prisregel**, **Listepris**, **Budsjett** og **Status**.

En prosjektleder klikker **Legg til fakturalinje** og fyller inn:

- **Linjekode** — 1–10 bokstaver og tall, unik i prosjektet; sporingskoden
  timelistene viser til er `PROSJEKTKODE-LINJEKODE`.
- **Produktvariant** — et tjenesteprodukt fra Produkter-appen, søkt mens du skriver.
  Uten rettighet til å se produkter sier skjemaet *Du trenger tilgang til produkter
  for å velge en variant*.
- **Prisregel**: **Listepris**, **Fast beløp** (i prosjektets valuta) eller
  **Rabatt** (**Rabatt (%)**, over 0 og høyst 100). Et fast beløp krever at prosjektet
  har en valuta.
- **Budsjetterte timer** og **Budsjettbeløp** for linjen, valgfrie og over 0; beløpet
  krever også en valuta.

Linjer slettes aldri: blyanten på en rad åpner linjen med en bryter **Aktiv**; en
linje som er slått av viser **Inaktiv** og kan slås på igjen. Uten Produkter-appen
står det *Fakturalinjer krever produktmodulen, som ikke er aktivert*; når katalogen
ikke kan leses, listes linjene under en advarsel og er uendret. Se
[Billing lines and the optional Products dependency](/en/reference/projects/#billing-lines-and-the-optional-products-dependency).

## Definer arbeidstyper

Under linjene på fanen **Fakturering** lister **Arbeidstyper** overtid og andre slags
timer, som hver ganger opp satsen en timeføring ellers ville fakturert og kostet:
**Navn**, **Faktureringsfaktor**, **Kostnadsfaktor** og **Status**, med eller uten Produkter.

En prosjektleder klikker **Legg til arbeidstype**: et **Navn** (unikt i prosjektet,
uten hensyn til store og små bokstaver, høyst 100 tegn), en **Faktureringsfaktor** og
en **Kostnadsfaktor** i prosent — 150 % er halvannen gang satsen, 100 % satsen slik
den er; hver over 0, høyst 1000, høyst to desimaler. Et opptatt navn avvises med
*Prosjektet har allerede en arbeidstype med det navnet*. Arbeidstyper slettes aldri:
bryteren **Aktiv** i redigeringsskjemaet deaktiverer en, så ingen ny timeføring kan
velge den, og aktiverer den igjen senere. Å endre en faktor flytter ingenting som
allerede er sendt inn — se [Work types](/en/reference/projects/#work-types).

## Planlegg faktureringen med milepæler

På fanen **Økonomi** er **Faktureringsplan** hva prosjektet faktureres i, i den
rekkefølgen milepælene faktureres. Den er økonomiske data tvers igjennom: uten
økonomiske rettigheter leser du *Faktureringsplanen er for dem som kan se prosjektets
økonomi*. Over kortet står **Planlagt**, **Klar til fakturering** og **Fakturert**;
under det, på et fastprisprosjekt, sier en merknad hvor mye *av fastprisen som ikke
er planlagt ennå* eller at *planen er … mer enn fastprisen*; ingen av delene hindrer lagring.

Milepæler faktureres i prosjektets valuta; til prosjektet har en, sier kortet det og
tilbyr en prosjektleder **Rediger prosjektet**. En prosjektleder (eller
`projects:manage-all`) klikker **Ny milepæl**: **Navn**, **Beskrivelse**, **Planlagt
dato**, og **Prises som** enten **Et beløp** eller **En andel av fastprisen** — bare
tilbudt på et fastprisprosjekt, med omtrent hva den utgjør. En andel følger en senere
endring av fastprisen til milepælen er fakturert.

Hver rad viser **Milepæl**, **Planlagt dato** (med **Forfalt** når datoen har passert
og milepælen fortsatt er planlagt eller klar), **Beløp** og **Status**, og en meny med
overgangene den tillater:

| Handling | Hvem | Hva som skjer |
| --- | --- | --- |
| **Rediger**, **Flytt opp**, **Flytt ned** | prosjektleder | så lenge milepælen er **Planlagt** eller **Klar til fakturering** |
| **Merk som klar til fakturering** / **Sett tilbake til planlagt** | prosjektleder | stempler hvem og når |
| **Merk som fakturert** | økonomiske rettigheter | spør etter en valgfri **Fakturareferanse** og **Fakturadato**, og låser beløpet |
| **Angre faktureringen** | økonomiske rettigheter | tilbake til klar; referanse, dato og låst beløp nullstilles |
| **Avlys milepælen** | prosjektleder | den blir stående sist i planen med strek over, og faktureres ikke |
| **Gjenåpne milepælen** | prosjektleder | en avlyst milepæl går tilbake til planlagt |
| **Slett milepælen** | prosjektleder | bare så lenge den fortsatt er planlagt og aldri flyttet; ellers avlys den |

En overgang avvises når prosjektet ikke lenger støtter milepælen — ingen valuta,
ingen fastpris bak en andel, eller et beløp i en valuta prosjektet har forlatt;
meldingen oppgir grunnen (full tabell:
[Billing milestones and the invoice plan](/en/reference/projects/#billing-milestones-and-the-invoice-plan)).
Med Utlegg aktivert sier en linje under tabellen hvor mange *utlegg klare til
fakturering* det er, med **Se utleggene** når du kan åpne Utlegg-fanen.

## Følg budsjettet og det førte arbeidet

Øverst på fanen **Økonomi** står **Budsjett og ført arbeid**. Alle som ser prosjektet
ser det; beløpene vises bare med økonomiske rettigheter og en prosjektvaluta,
**Margin** bare med `projects:view-costs` i tillegg.

- **Budsjett brukt** er en prosent av ett grunnlag, i tjenerens rekkefølge:
  **Budsjettbeløp**, ellers **Fastpris** på et fastprisprosjekt, ellers
  **Budsjetterte timer** — det ene grunnlaget en deltaker ser. Uten noe står det
  **Ingen budsjett satt**. Det røde merket **Over budsjett** følger det nøyaktige
  forholdet, ikke den avrundede prosenten.
- En stolpe deler det førte arbeidet i **Godkjent**, **Sendt inn** og **Utkast**, de
  tre gruppene Timer-appens statuser samles i; avviste føringer ligger i Utkast.
- **Verdi av arbeidet** er hva de førte timene fakturerer; **Margin** er verdien av
  arbeidet pluss hva utleggene fakturerer, minus hva begge koster. Merknader sier hva
  tallene utelater: timer uten sats, timer uten kostnad, og oppgavenes samlede estimat.
- En tabell per fakturalinje — **Linje**, **Budsjett**, **Ført**, **Brukt**,
  **Gjenstår** — tar med inaktive linjer og en rad **Ingen fakturalinje** når noe er
  ført uten linje. **Timer per arbeidstype** følger når noen føring valgte en type.

Timene kommer fra Timer-appen; uten den står det *Timer dukker opp her når Timeføring
er slått på*. Utleggene kommer fra Utlegg-appen inn i en egen seksjon **Kostnader**:
radene **Godkjent**, **Sendt inn — venter på godkjenning** og **Utkast** med
**Linjer**, **Kostnad** og **Til kunden**, en rad **Totalt**, **Herav
leverandørfakturaer** når det finnes noen, og merknader om uprisede utlegg og andre
valutaer. Utlegg måles aldri mot budsjettet, og seksjonen sier det. Definisjonene
står i [Project economy](/en/reference/projects/#project-economy).

## Sammenlign prosjekter i Prosjektøkonomi

**Prosjektøkonomi** i sidemenyen lister hvert prosjekt du kan se økonomien på — dem
du leder, eller alle med `projects:view-financials` eller `projects:manage-all` — som
**Prosjekt**, **Kunde**, **Status**, **Budsjett brukt**, **Verdi av arbeidet**,
**Timer til behandling**, **Neste milepæl** og **Klar til fakturering**. Tre kort
teller **Prosjekter**, dem som er **Over budsjett**, og hva som er **Klar til
fakturering** per valuta, delt i milepæler og utleggslinjer.

Verktøylinjen har søkefeltet, **Status** (**Aktivt** som standard; **Alle statuser**
opphever det), **Kunde**, **Sorter etter** (**Mest av budsjettet brukt**, **Mest
klart til fakturering**, **Neste milepæl først**, **Prosjektkode**), og bryterne
**Bare over budsjett** og **Bare med noe klart til fakturering**. En rad med utlegg
klare i en annen valuta sier **Mer klart i en annen valuta**; prosjektets egen
Økonomi-fane viser dem. Er det ingenting å vise, står det *Prosjekter du kan se
økonomien på dukker opp her* (se [The portfolio](/en/reference/projects/#the-portfolio)).

## Rettigheter i korte trekk

| Skjermbilde eller handling | Krever |
| --- | --- |
| Appen Prosjekter: Prosjekter, Mine oppgaver, Prosjektøkonomi | `projects:access` |
| **Nytt prosjekt** | `projects:create` |
| Å se alle prosjekter, ikke bare dine egne | `projects:view-all` eller `projects:manage-all` |
| **Rediger**, **Endre status**, Deltakere, fakturalinjer, arbeidstyper, milepæler | rollen **Prosjektleder**, eller `projects:manage-all` |
| Oppgaver, sjekklistepunkter og kommentarer | rollen **Deltaker** eller **Prosjektleder** |
| Fanen **Fakturering**, faktureringsplanen og beløpene på **Økonomi** | rollen **Prosjektleder**, `projects:view-financials` eller `projects:manage-all` |
| **Merk som fakturert** og **Angre faktureringen** | økonomiske rettigheter på prosjektet |
| **Margin** og kostnadstallene | økonomiske rettigheter og `projects:view-costs` |
| Fanen **Timer** | `time:access`, Timer aktivert |
| Fanen **Utlegg** | `expenses:access`, Utlegg aktivert |
