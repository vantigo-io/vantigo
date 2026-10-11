---
title: Ansatte
description: Personregisteret — Personer, Ny person og Ansatteinnstillinger med selskapets struktur.
sidebar:
  order: 45
sources:
  - apps/staff/frontend
---

I Ansatte-appen holder HR oversikt over selskapets personer: hvem de er, hvordan de
nås, arbeidsforholdene deres med vilkårene arbeidsavtalen fastsetter, og selskapets
struktur som arbeidsforholdene hører til i. Reglene bak hvert skjermbilde — hvem som
leser hvilket felt, hva en avvisning betyr — står i
[referansen for Ansatte-modulen](/en/reference/staff/); denne siden handler om hva du
gjør i hvert skjermbilde.

## Hvem ser hva

Sidemenyen viser områdene rettighetene dine gir deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Min side** | alle med `staff:access` |
| **Personer** | `staff:view` — en HR-rolle med `staff:manage` uten den åpner likevel siden via adressen og ser listen |
| **Ansatteinnstillinger** | `staff:manage` |

Hva et skjermbilde tilbyr, følger det serveren sier at du kan gjøre: knappen **Ny
person**, **Status**-filteret og statuskolonnen finnes bare for `staff:manage`. Hele
tabellen står under [rettighetene](/en/reference/staff/#permissions) i referansen.

## Finn noen under Personer

**Personer** lister selskapets personer etter navn, med ansattnummer, avdeling, leder
og e-post på jobb. Avdelingen og lederen er de som står i vilkårene som gjelder i dag;
en strek betyr at det ikke finnes noen.

- **Søk** matcher hvert ord du skriver mot navnene, ansattnummeret og e-posten på
  jobb. Listen oppdateres et øyeblikk etter at du slutter å skrive.
- **Virksomhet**, **Avdeling** og **Arbeidssted** snevrer listen inn til personene
  hvis arbeidsforhold i dag — eller, for den som begynner senere, de første vilkårene
  — hører hjemme der. Arkiverte rader tilbys også, merket «(arkivert)», så du fortsatt
  finner hvem som var der.
- Hvert filter ligger i sidens adresse, så en oppdatert side eller en lenke du limer
  inn viser den samme listen.

En kollega med `staff:view` ser bare personene som er **aktive i dag**: den som ikke
har begynt ennå og den som har sluttet, står ikke i listen deres.

### Status, for HR

Med `staff:manage` ser du også **Status**-valget og en statuskolonne:

- **Nåværende** (standard) lister dem som er aktive i dag, dem som begynner senere og
  dem som er **Ikke ansatt** ennå — personen som ble lagret i første steg av
  [Ny person](#legg-til-en-ny-person), og som fortsatt mangler arbeidsforholdet.
- **Sluttet** lister dem der hvert arbeidsforhold sluttet før i dag.
- **Alle** lister alle.

Kolonnen sier **Aktiv**, **Begynner** med første dag, **Sluttet** med siste
arbeidsdag, eller **Ikke ansatt**. «I dag» er dagen i forretningstidssonen (se
[Endre innstillingene](#endre-innstillingene)), så listen skifter ved midnatt der, ikke
i nettleseren din.

Trykk på et navn for å åpne personens side.

## Legg til en ny person

Med `staff:manage` trykker du **Ny person** under Personer. Skjemaet har to steg, slik
en ansettelse har: personen, så det første arbeidsforholdet. Personen lagres i første
steg, så du kan stoppe der og legge til arbeidsforholdet senere — personen står da
under **Nåværende** i Personer med statusen **Ikke ansatt**.

### Steg 1: personen

1. Fyll inn **Fornavn** og **Etternavn**. De er påkrevd (opptil 100 tegn hver).
   **Visningsnavn** er valgfritt; tomt blir det de to navnene.
2. Ansattnummeret **lages automatisk** som standard: skjemaet sier hvilket nummer som
   er neste — prefikset og neste nummer fra innstillingene — med mindre noen allerede
   har det, og da brukes det neste ledige. Vil du gi det selv, slår du på **Gi
   ansattnummer selv** og skriver det inn: opptil 16 bokstaver, sifre eller
   bindestreker, unikt uansett store og små bokstaver. Nummeret endres aldri senere.
3. Legg inn **E-post på jobb**, **Telefon på jobb**, **Språk** og **Arbeidsland** når
   du har dem. En e-post på jobb som en annen person har, avvises på feltet.
4. **Private opplysninger (valgfritt)** åpner fødselsdato, kjønn, mobiltelefon og privat
   telefon, privat e-post og hjemmeadresse. Ingen av dem kreves for å opprette
   personen.
5. Trykk **Lagre og fortsett**. En avvisning havner på feltet den gjelder.

### Steg 2: det første arbeidsforholdet

Steget åpner med en påminnelse, og den er grunnen til at to felter er merket som
påkrevd: etter aml. § 14-6 skal arbeidsavtalen si om ansettelsen er fast eller
midlertidig, og hvor stor **stillingsprosenten** er. Mangler en av dem, legges
arbeidstakerens påstand til grunn — derfor fyller Ansatte aldri inn disse for deg, og
avviser vilkår uten dem.

1. **Virksomhet** er forhåndsvalgt til standardvirksomheten; velg en annen hvis
   arbeidsforholdet rapporteres et annet sted. **Startdato** er forhåndsutfylt med i dag
   i forretningstidssonen.
2. Arbeidsforholdsnummeret — a-meldingens arbeidsforholdID — lages fra ansattnummeret
   (`E-8-1` for ansatt `E-8`). Slå på **Gi arbeidsforholdsnummer selv** for å skrive
   inn et eget (opptil 40 bokstaver, sifre eller bindestreker).
3. Velg **Ansettelsestype**: **Fast** eller **Midlertidig**. En midlertidig ansettelse
   ber om **Grunnlag for midlertidig ansettelse** (aml. § 14-9 (2) a–e, eller grunnlag
   i tariffavtale) og, hvis du vet den, **Forventet slutt**.
4. **Ansettelsesform** følger ansettelsestypen og tilbyr bare formene som hører til
   den; en form der personen leies ut, ber om hvem de er **Leid ut til**.
5. Skriv inn **Stillingsprosent** — mer enn 0, høyst 100.
6. **Arbeidstidsordning** starter på **Ikke skift**, og **Timer per uke i full
   stilling** følger ordningen (37,5; 36,5 for skift; 33,6 offshore og helkontinuerlig
   skift; 35,5 døgnkontinuerlig) til du endrer timene selv. Timer som ikke passer
   ordningen, markeres med en gang — nøyaktig 36,5 eller 35,5 for de to ordningene, 28
   til 33,6 for de to 33,6-ordningene — og ingenting sendes før de passer. Se
   [timeregelen](/en/reference/staff/#the-hours-rule).
7. Legg inn **Avdeling**, **Arbeidssted**, **Leder**, **Tittel** og **Yrkeskode**
   (STYRK-08, sju sifre) når du har dem. Tom leder betyr at avdelingens leder er
   lederen.
8. **Flere vilkår** åpner resten av det § 14-6 krever at en arbeidsavtale inneholder —
   type arbeidstaker, prøvetiden, oppsigelsestidene, arbeidstid og pauser, en særlig
   arbeidstidsordning, overtid, tariffavtalen, ferie, kompetanseutvikling, trygd og
   pensjon, kostnadssted og divisjon, avlønning, utbetalingsmåte og lønningsdag, og
   HRs notat — i tillegg til type arbeidsforhold og datoen avtalen ble signert.
9. Trykk **Lagre arbeidsforholdet**. Personens side åpnes. Avviser serveren et felt —
   en stillingsprosent utenfor området, en arkivert avdeling — havner avvisningen på
   det feltet; en som gjelder hele arbeidsforholdet, vises som en melding.

**Legg det til senere** lukker skjemaet med personen lagret og åpner siden deres.

## Endre innstillingene

**Ansatteinnstillinger** (`staff:manage`) har de generelle innstillingene først, så
selskapets struktur.

### Generelt

- **Forretningstidssone** — hver dato i Ansatte, også «i dag», er en kalenderdag i
  denne sonen.
- **Prefiks for ansattnummer** og **Neste ansattnummer** — et nummer som lages
  automatisk, er prefikset og neste nummer skrevet sammen uten utfylling: `E-` og 8 blir
  `E-8`. Linjen under feltene viser neste nummer og lengden. Et ansattnummer har høyst
  16 tegn, så et prefiks og et nummer som sammen blir lengre, avvises på begge feltene
  før noe lagres. Et selskap som vil ha `EMP007`, setter prefikset til `EMP00` så lenge
  det varer.
- **Pensjonsleverandør** — leverandøren av tjenestepensjon som avtalekontrollen leser
  når en vilkårsrad ikke har noen tekst om trygd og pensjon.
- **Yrkesskadeforsikring**.

Trykk **Lagre innstillingene**. Har noen andre lagret innstillingene siden du åpnet
siden, avvises lagringen med en melding; last siden på nytt og lagre igjen.

### Strukturen

Fire tabeller, hver med en **Legg til**-knapp, en blyant for å endre en rad, og
**Arkiver**:

- **Juridiske enheter** — den juridiske enheten med organisasjonsnummer (ni sifre;
  mellomrom ignoreres).
- **Virksomheter** — virksomheten a-meldingen rapporterer per, hver vist med den
  juridiske enheten den hører under.
- **Avdelinger** — en kode, et navn, en **Leder** (en person i Ansatte: skriv et navn
  eller et ansattnummer for å finne dem), en **Overordnet avdeling**, et kostnadssted og
  en divisjon. Lederen for en avdeling er leder for dem hvis vilkår nevner avdelingen
  og ingen egen leder.
- **Arbeidssteder** — arbeidsstedet med adresse; landet er Norge når det står tomt.

Nøyaktig én juridisk enhet og én virksomhet har merket **Standard**; et nytt
arbeidsforhold legges i standardvirksomheten når det ikke nevner noen. For å flytte
standarden trykker du **Gjør til standard** på en annen rad — merket forlater den
gamle i samme steg. Standarden selv har ingen bryter for å slå det av.

**Arkiver** spør først, og tar så raden ut av valglistene mens den blir stående på alt
som allerede nevner den, merket **Arkivert** og uten handlinger. Ingenting slettes.
Noen arkiveringer avvises, og meldingen forklarer hvorfor med serverens egne ord —
standarden arkiveres for eksempel aldri: gjør en annen rad til standard først. Reglene
står under [strukturen](/en/reference/staff/#the-structure) i referansen.
