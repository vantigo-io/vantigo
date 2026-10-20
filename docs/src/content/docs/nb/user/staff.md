---
title: Ansatte
description: Personregisteret — Personer, personsiden med dokumentene og kontoen, slettesperren, en slettet person og nyansettelsen av den, Ny person, Min side, fraværskalenderen og godkjenningene, fravær og feriekontoen, Grupper og plassen deres i katalogen, og Ansatteinnstillinger med selskapets struktur, dokumentkategoriene, import og eksport av registeret, lønnseksporten, oppbevaring, tersklene og kontoene.
sidebar:
  order: 45
sources:
  - apps/staff/frontend
  - apps/host/frontend/src/routes/staff
---

I Ansatte-appen holder HR oversikt over selskapets personer: hvem de er, hvordan de
nås, arbeidsforholdene deres med vilkårene arbeidsavtalen fastsetter, og selskapets
struktur som arbeidsforholdene hører til i. Reglene bak hvert skjermbilde — hvem som
leser hvilket felt, hva en avvisning betyr — står i
[referansen for Ansatte-modulen](/en/reference/staff/); denne siden handler om hva du
gjør i hvert skjermbilde.

## Hvem ser hva

Ansatte-flisen vises for alle med `staff:access`; appen åpner på Personer for dem som
ser den, og på Min side for alle andre. Sidemenyen viser områdene rettighetene dine gir
deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Min side** | alle med `staff:access` |
| **Ansattoversikt** (skjermbildet Personer) | `staff:view`, eller `staff:manage` — en HR-rolle uten `staff:view` ser også listen og strukturen |
| **Fravær** (kalenderen og godkjenningene) | `staff:view`, `staff:approve`, `staff:manage` eller `staff:personal-data`; en leder uten disse åpner den fra lenken på dashbordet, og avgjør under [Mitt team](#mitt-team) |
| **Grupper** | `staff:manage` |
| **Ansatteinnstillinger** | `staff:manage` |

Hva et skjermbilde tilbyr, følger det serveren sier at du kan gjøre: knappen **Ny
person**, **Status**-filteret og statuskolonnen finnes bare for `staff:manage`. Hele
tabellen står under [rettighetene](/en/reference/staff/#permissions) i referansen.

## Finn noen under Personer

**Personer** (i sidemenyen: **Ansattoversikt**) lister selskapets personer etter navn, med ansattnummer, tittel, avdeling,
leder og e-post på jobb. Tittelen og avdelingen er de som står i vilkårene som gjelder i
dag, og lederen er den som leder personen i dag: den vilkårene oppgir, eller, når de ikke
oppgir noen eller den som er oppgitt har sluttet, avdelingens leder eller nærmeste leder
over som fortsatt er ansatt; en strek betyr at det ikke finnes noen.

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

**Fraværende i dag** ved et navn betyr at et godkjent eller registrert fravær gjelder i
dag — ferie, permisjon eller en sykedag. Alle som ser listen, ser det samme merket, og
det sier aldri hva slags fravær det er; en søknad som venter, og en legetime på noen
timer, teller ikke.

### Status, for HR

Med `staff:manage` ser du også **Status**-valget og en statuskolonne:

- **Nåværende** (standard) lister dem som er aktive i dag, dem som begynner senere og
  dem som er **Ikke ansatt** ennå — personen som ble lagret i første steg av
  [Ny person](#legg-til-en-ny-person), og som fortsatt mangler arbeidsforholdet.
- **Sluttet** lister dem der hvert arbeidsforhold sluttet før i dag.
- **Alle** lister alle.

Kolonnen sier **Aktiv**, **Begynner** med første dag, **Sluttet** med siste
arbeidsdag, eller **Ikke ansatt**. En tidligere ansatt som oppbevaringskjøringen har
slettet, står under **Sluttet** og **Alle** med **Slettet** ved siden av statusen — se
[En slettet person](#en-slettet-person). «I dag» er dagen i virksomhetens tidssone (se
[Endre innstillingene](#endre-innstillingene)), så listen skifter ved midnatt der, ikke
i nettleseren din.

Trykk på et navn for å åpne [personsiden](#personsiden).

## Personsiden

En persons side har seks faner — **Profil**, **Arbeidsforhold**, **Fravær**, **Historikk**, **Dokumenter** og
**Konto** — og fanen du står på, ligger i sidens adresse (`?tab=account` åpner fanen
Konto, som er dit dashbordets kontopunkter fører). Siden viser det du kan se av personen og
ikke noe annet: en gruppe felt som ikke er din å lese, står ikke på siden i det hele
tatt, heller ikke som en tom overskrift. Personen selv, en leder av dem, en kollega med
`staff:view` og HR ser hver sin side; hele tabellen står under
[feltgrupper og målgrupper](/en/reference/staff/#field-groups-and-audiences) i
referansen. En person du ikke kan se, svarer akkurat som en som ikke finnes.

### Profil

- **Jobb** — navnene, ansattnummeret, e-post og telefon på jobb, tittelen, avdelingen,
  virksomheten, arbeidsstedet og lederen som gjelder i dag, språket og arbeidslandet.
  Alle som kan åpne siden, ser den.
- **Privat** — fødselsdato, kjønn, mobil og privat telefon, privat e-post og
  hjemmeadresse. Personen selv og HR.
- **HRs notater** — bare HR (`staff:manage`, `staff:personal-data`).
- **Identifikatorer** — fødselsnummeret som **Registrert** eller **Ikke registrert**,
  med typen, og bankkontoen som **Slutter på** og de fire siste sifrene. Siden viser
  aldri mer enn det, bortsett fra gjennom **Vis** nedenfor.
- **Pårørende** og **Roller** (verneombud, medlem av AMU, HMS-ansvarlig, hver med
  dagene de har rollen).
- **Selvbetjening** — brukeren personen logger inn med, hvis HR har koblet en, eller —
  når Ansattes kontoer styrer personens bruker — *… — styrt av Ansatte; se fanen
  Konto*, uten noe å koble til eller fra. Selve kontoen står på fanen
  [Konto](#konto).
- **Barn og omsorgsdager** — **Barn under 12**, **Aleneomsorg** og **Barn med ekstra
  omsorgsdager**, det personens omsorgsdager telles ut fra. Personen, HR og
  `staff:personal-data`, og bare når ett av dem er registrert.
- **Organisasjon** — **Ledere** oppover fra personens leder i dag, og de **Direkte
  underordnede**. Trykk på et navn for å åpne siden deres.

En leder ser jobbgruppen, rollene og organisasjonen til dem de leder; en kollega det
samme for alle som er aktive i dag. En leder som har sluttet, leder ingen: folkene deres
går til avdelingens leder, eller nærmeste leder over.

#### Endre opplysningene (HR)

1. Trykk **Endre opplysninger** i gruppen Jobb. Skjemaet har jobbgruppen, den private
   gruppen og HRs notater. Ansattnummeret vises, men endres aldri.
2. Endre det du trenger, og trykk **Lagre**. En avvisning havner på feltet den gjelder
   — for eksempel en e-post på jobb som en annen person har.

Skjemaet har også de tre **omsorgsdagfeltene** under **Barn og omsorgsdager**: **Barn
under 12** (uten NAVs vedtak om ekstra dager), **Aleneomsorg** og **Barn med ekstra
omsorgsdager** (med vedtaket) — det omsorgsdagene for sykt barn telles ut fra
([omsorgsdagfeltene](/nb/admin/staff/#omsorgsdagfeltene)). Et felt som står tomt, er
ikke registrert; et du ikke rører, lagres som det var. Personen, HR og
`staff:personal-data` ser dem; en leder og en kollega gjør det aldri, og personens egen
**Endre opplysningene mine** endrer dem aldri.

Har noen andre lagret personen etter at du åpnet skjemaet, lukkes skjemaet, siden lastes
inn på nytt og sier *Noen andre lagret dette først; det er lastet inn på nytt*. Gjør
endringen din på nytt ut fra det som står der nå.

#### Pårørende og roller (HR)

- **Endre pårørende** åpner listen: opptil tre, hver med **Navn** og **Relasjon**
  (begge må oppgis), **Telefon** og **Merknad**. **Legg til pårørende** legger til én,
  søppelbøtten fjerner én, og **Lagre** erstatter hele listen.
- **Endre roller** virker på samme måte: **Legg til rolle**, velg **Rolle**, og oppgi
  **Fra** og **Til** hvis du vet dem (slutten ikke før starten).

#### Vis eller registrer fødselsnummeret og bankkontoen

Med `staff:personal-data` tilbyr gruppen Identifikatorer **Vis** og **Registrer**. Alene
viser `staff:personal-data` hver gruppe av personen, men endrer ikke noe annet:
endreknappene hører til `staff:manage`.

- **Vis** spør først, og viser så nummeret i 30 sekunder med nedtelling (*Skjules om
  … s*); **Skjul** skjuler det før. Hver visning registreres i personens historikk med
  navnet ditt. Nummeret lagres verken av siden eller nettleseren.
- **Registrer** spør etter **Type** — fødselsnummer, d-nummer eller et utenlandsk
  identitetsnummer med **Land** — og **Nummer**, eller for bankkontoen et norsk
  kontonummer eller et IBAN. Et fødselsnummer som viser en annen fødselsdato enn
  personens, avvises på nummeret: ett av dem er feil, så rett det som er feil. Når en
  bankkonto registreres, får personen en e-post som nevner de fire siste sifrene.

`staff:manage` alene ser **Registrert** og de fire siste sifrene, og ikke noe mer. Se
[de forseglede identifikatorene](/en/reference/staff/#the-sealed-identifiers) i
referansen.

#### Koble personen til brukeren sin (HR)

Gruppen **Selvbetjening** kobler personen til brukeren de logger inn med, så de ser sin
egen oppføring på [Min side](#min-side). Skriv et navn i **Bruker**, velg brukeren og
trykk **Koble**. Siden sier da *… — koblet, ikke styrt av Ansatte*: koblingen gir
brukeren Min side og ikke noe mer, gir ingen rettigheter, og Ansatte endrer aldri
brukerens konto. En deaktivert bruker, eller en som allerede er koblet til en annen
person, avvises på feltet. **Koble fra** spør først og fjerner så koblingen; brukerens
konto blir som den er.

En bruker Ansattes kontoer styrer, kobles aldri til eller fra her: siden sier *Denne
brukerens konto styres av Ansattes kontoer, så den kobles ikke til eller fra for hånd.
Bruk fanen Konto.* Når Ansatte ikke kan spørre identitet om en bruker er styrt, sier
siden *Identitet kunne ikke nås, eller ingen modul gir kontoer*, og ingenting endres;
prøv igjen.

### Arbeidsforhold

Hvert arbeidsforhold er et kort med starten, slutten og årsaken, virksomheten, typen
arbeidsforhold, dagen avtalen ble signert, og dagene stillingsprosenten
(**Stillingsprosent gjeldende fra**) og lønnen (**Lønn gjeldende fra**) gjaldt fra,
med et statusmerke. Under det står **Vilkår** som en tidslinje, nyeste først, og raden
som gjelder i dag er merket **Gjeldende**. Hver rad viser feltene slik du kan lese dem:
personen og HR ser alle feltene og **Lønnselementer**; en leder alle feltene unntatt
lønnen; en kollega bare dagene, ansettelsestypen og stillingsprosenten.

Med `staff:manage` tilbyr fanen og kortene:

- **Nytt arbeidsforhold** (over kortene) — et arbeidsforhold med de første vilkårene, i
  skjemaet fra [steg 2 under Ny person](#steg-2-det-første-arbeidsforholdet): en stilling
  til, en som har sluttet og ansettes igjen, eller det første arbeidsforholdet du ventet
  med. Nummeret er ansattnummeret og neste ledige `-n` med mindre du velger et eget. På
  en slettet person heter knappen **Ansett på nytt** — se [Ansett på nytt](#ansett-på-nytt).
- **Nye vilkår** — en ny rad fra dagen den **Gjelder fra**, forhåndsutfylt fra raden som
  gjelder i dag. Endre det som endres, og trykk **Lagre vilkårene**; et felt du tømmer,
  fjernes fra den dagen. **Flere vilkår** har resten av § 14-6 og lønnen: **Legg til
  lønnselement** (navn, beløp, valuta og periode, høyst tjue), **Ansiennitetsdato**,
  **Lønnstrinn**, og — når prøvetiden er satt — om en forlengelse av den er avtalt
  skriftlig, til hvilken dag og hvorfor.
- **Korriger** på hvilken som helst rad — skriver om raden slik den faktisk ble avtalt;
  historikken viser hva som ble endret. Den første raden gjelder fra starten: for å
  flytte den, endre starten under **Endre arbeidsforholdet**.
- **Slett** bare på den siste raden, aldri den første, etter at du har bekreftet. Raden
  før gjelder da igjen.
- **Endre arbeidsforholdet** — **Startdato** (de første vilkårene flyttes med),
  **Virksomhet**, typen arbeidsforhold, **Avtale signert** og **Ferieordning**
  ([ferieordninger](/nb/admin/staff/#ferieordninger)). Arbeidsforholdsnummeret endres
  aldri.
- **Avslutt arbeidsforholdet** — **Siste arbeidsdag**, i dag i virksomhetens tidssone
  med mindre du endrer den, og gjerne fram i tid, og **Sluttårsak**, en av
  a-meldingens seks; skjemaet ber om årsaken før noe sendes. **Gjenåpne** fjerner en
  slutt som ble registrert ved en feil.

En avvisning havner på feltet den gjelder: en dag en annen rad allerede har, på
**Gjelder fra**; en leder som ville endt opp som sin egen leder, på **Leder**; en for
lang prøvetid på **Prøvetiden slutter**; timer som ikke passer ordningen, på timene; og
vilkår datert etter slutten på **Siste arbeidsdag**.

#### Avtalekontrollen

**Avtalekontroll** lister de sytten punktene aml. § 14-6 krever at en skriftlig
arbeidsavtale inneholder, a) til q), hvert som **Finnes**, **Mangler** eller **Gjelder
ikke** — vurdert ut fra vilkårene som gjelder i dag — og sier om en signert avtale er
registrert. Den informerer; den stopper aldri en lagring. Personen, en leder av dem og
HR ser den; en leder, som ikke ser lønnen, ser den uten punkt i), lønnen. Se [§ 14-6-kontrollen](/en/reference/staff/#the--14-6-check) i referansen for hvor
hvert punkt leses fra.

### Fravær

**Fravær** er personens fravær slik du har lov til å se det: personen, en leder av
personen, den som avgjør alles fravær (`staff:approve`) og HR. Andre ser ikke fanen.
Ovenfra og ned:

- **Feriekontoen** for et år — ferierett, overført, tatt, søkt om og igjen — med
  **Forrige år** og **Neste år** for å bla mellom årene.
- **Feriejusteringer** (HR): overføringene og rettingene, nyeste år først. **Legg til
  justering** registrerer en for året som vises — en **Retting** (med en **Merknad** om
  hvorfor), eller en **Avtalt** eller **Tvungen overføring** fra et tidligere år — og
  søppelbøtta sletter en etter å ha spurt. **Overfør fra** *år* (HR) overfører årets
  ferie som ikke er tatt, til neste år: velg **Type** — *Avtalt*, innenfor
  ferieordningens maksimum, eller *Tvungen*, utover det med en merknad — la **Dager** stå
  tomt for det som er igjen, og trykk **Overfør**. En ny overføring fra samme år avvises:
  *En overføring fra det året er allerede registrert*.
- **Egenmelding** — personens stilling etter selskapets regler. HR har **Trekk tilbake
  egenmelding**: velg **Til og med** — en dag etter i dag, høyst ett år frem — skriv
  **Grunn** og trykk **Trekk tilbake**. **Opphev tilbaketrekkingen** avslutter den
  tidligere. En tilbaketrekking vises bare for personen, HR og `staff:personal-data`.
- **Omsorgsdager** for i år (årsknappene endrer det ikke), for personen, HR og `staff:personal-data`.
- **Sykefraværssak fra** *dato* — den åpne saken: siste dag så langt, 52-ukersgrensen,
  milepælene og tilretteleggingsnotatet. En leder, en godkjenner og HR har **Marker som
  gjort** og **Frafall** på en milepæl; lederen og HR har **Endre
  tilretteleggingsnotatet** (en godkjenner får ikke se det). HR har også **Endre
  anslaget** — eller **Tilbakestill til første dag + 364** — og **Lukk saken**. En
  milepæls notat, og hvorfor en ble frafalt, vises bare for personen og `staff:personal-data`, som
  har **Endre notatet** på hver milepæl.
- **Fravær** — personens rader i året som vises, slik du har lov til å se dem, en side
  om gangen når de er mange. En type som avslører helse-
  eller familieforhold, vises for en leder og for HR uten `staff:personal-data` som en
  godkjent *Permisjon*, uten noe å endre. HR har **Registrer fravær** — en sykmelding
  personen kommer med, for eksempel — og på en rad:
  - **Endre** på en søknad eller en registrering (en sykmelding som sluttet tidligere),
  - **Rett** på et godkjent fravær der dagene eller typen var feil — en type som røper
    helse eller familieforhold rettes aldri til en som ikke gjør det, eller omvendt:
    trekk fraværet tilbake og legg inn det andre,
  - **Trekk tilbake fraværet** på et godkjent fravær (også for den som avgjorde det)
    eller en søknad, etter å ha spurt,
  - **Utsett ferie** på en registrert 100 % sykmelding: arbeidstakerens krav om å utsette innvilget
    ferie etter ferieloven § 9. Sett **Krevd den** — aldri etter i dag, aldri før
    sykmeldingen begynte — og trykk **Registrer kravet**; ferien den ligger over, deles,
    og dagene går tilbake til kontoen. Et krav som kom etter siste arbeidsdag før ferien,
    for sykdom som begynte før den, avvises med ord.

En endring sender med det raden har for å vokte den: har noen endret raden imens,
lukkes skjemaet, fanen lastes på nytt og sier fra — gjør endringen igjen på det som står
der nå. En rad som er borte imens, sier *Dette finnes ikke lenger.*

### Historikk

**Historikk** lister hver endring av personen, nyeste først og sidevis: hva som ble
endret, hvem som endret det og når. Et felt viser verdien før og etter (*E-post på
jobb: gammel → ny*); et felt i en gruppe du ikke kan lese, bare at det ble endret
(*Fødselsdato endret*); en lønnsendring holdes utenfor for en leder, så en
lønnskorrigering vises bare som typen endring, merket *(detaljene er ikke synlige for
deg)*. Personen selv, en leder av dem og HR ser
fanen Historikk; en kollega gjør det ikke.

Kontoens endringer står der også, bare for personen, HR og `staff:personal-data`:
*Kontoflagget endret* med feltene før og etter (*Ansatte gir en konto: Nei → Ja*,
**Konto fra**, **Adresse**), og kontoarbeiderens steg — *Konto forberedt*, *Konto
aktivert*, *Konto deaktivert*, *Konto brakt i samsvar*, *Konto stoppet*, *Kontosteg
mislyktes* og *Kontoen hører til et annet ansattnummer* — hvert med **Steg** det tok,
**Tilstand** det lot kontoen stå i, hvorfor det stoppet eller mislyktes,
**Invitasjon**, og om kontoflagget ble slått av i mellomtiden, alt med ord.

Når personen kommer inn i eller går ut av en [gruppe](#grupper), sier historikken *Ble
med i en gruppe* eller *Gikk ut av en gruppe*, av HR — eller av kontoarbeideren for en
avledet gruppe — med gruppens navn og hvordan personen kom inn, *Oppført* eller
*Regelen* (*Salg (Regelen)*). Gruppens navn og hvordan vises for personen, HR og
`staff:personal-data`; en leder ser bare at personen kom inn i eller gikk ut av en
gruppe.

### Dokumenter

**Dokumenter** er personens arkiv: arbeidsavtalen, attester, sertifikater, brev og
lignende, hver en PDF-, JPEG-, PNG- eller HEIC-fil. Personen selv, HR
(`staff:manage`) og `staff:personal-data` ser fanen; en leder og en kollega gjør det
ikke.

Dokumentene står etter kategori, hver kategori med sin **klasse**, som avgjør når
filene slettes etter at personen har sluttet — hold pekeren over klassen for regelen:

- **A · Lønn og avtale** — fem år etter året arbeidsforholdet dokumentet er arkivert
  på, sluttet.
- **D · Personal** — sammen med resten av personalmappen, en frist etter at siste
  arbeidsforhold sluttet.
- **E · Helse** — en fastsatt tid etter at siste arbeidsforhold sluttet.

Reglene i sin helhet står under [oppbevaring](/en/reference/staff/#retention) i
referansen. Hver rad viser filen, størrelsen, **Gyldig til** — merket **Utløpt** når
den dagen er passert — **Signert** og når den ble **Lagt til**.

**Helsedokumenter** — de i en kategori i klasse E, som **Helse** — ser bare personen
selv og `staff:personal-data`. HR uten `staff:personal-data` ser dem ikke i listen og
får ikke tilbud om en helsekategori.

#### Arkiver et dokument (HR)

Med `staff:manage` eller `staff:personal-data`:

1. Trykk **Legg til dokument**.
2. Slipp en fil i boksen, eller trykk **Velg en fil**. På mobilen åpner **Ta et bilde**
   kameraet.
3. Velg **Kategori**, og fyll ut **Gyldig til** — siste dag et sertifikat gjelder — og
   **Signert** hvis de passer.
4. Trykk **Last opp**. Dokumentet står under kategorien sin.

Én fil om gangen: en PDF-, JPEG-, PNG- eller HEIC-fil på høyst 10 MiB, og høyst 200
dokumenter per person. En fil som er større eller av en annen type, avvises før den
sendes. Ansatte leser også selve innholdet i filen, og en fil som ikke er det navnet
sier, avvises på filen. De 200 regner med helsedokumenter du ikke kan se, så HR kan få
beskjed om at en person har 200 og selv se færre.

En **arbeidsavtale** spør etter **Signert** først, og sier hva den gjør: når nøyaktig
ett arbeidsforhold dekker den dagen og ikke har noen signeringsdato ennå, får
arbeidsforholdet denne, og [avtalekontrollen](#avtalekontrollen) regner avtalen som
signert. Når to arbeidsforhold dekker dagen, settes ingenting — skriv datoen på
arbeidsforholdet i stedet.

#### Last ned, endre eller fjern et dokument

- **Last ned** (pilen) henter filen og lagrer den under navnet sitt. Alle som ser et
  dokument, kan laste det ned.
- **Endre** (blyanten) endrer **Kategori**, **Gyldig til** og **Signert**; filen endres
  aldri. En dato du tømmer, fjernes. Har noen andre endret dokumentet i mellomtiden,
  lukkes skjemaet på det som står der nå, og siden sier fra.
- **Fjern** (søppelbøtta) spør først. Filen fjernes for godt og kan ikke gjenopprettes;
  Historikk beholder en linje om at den ble fjernet.

Å endre eller fjerne et helsedokument, og å flytte et dokument inn i eller ut av en
helsekategori, er `staff:personal-data` sin oppgave: HR uten den får ikke **Endre** og
**Fjern** på et helsedokument — heller ikke på sitt eget. Når `staff:personal-data`
endrer et helsedokument, velger hen også **Sykefraværssak** — en av personens saker,
åpen eller lukket — så dokumentet slettes sammen med sakens helseopplysninger; tøm
feltet for å ta det ut av saken. Et dokument i en annen kategori hører ikke til noen sak.

### Konto

Fanen **Konto** er HRs (`staff:manage`). Personen selv og den som har
`staff:personal-data`, leser den og endrer ingenting. En leder, `staff:approve` og en
kollega har den ikke. Den har to grupper — tre for en person av typen Microsoft Entra
ID, der **Katalogprofil** følger etter.

**Kontoflagg** sier om Ansatte gir personen en bruker å logge inn med, fra hvilken dag,
på hvilken adresse og hvem som har kontoen. HR endrer det:

1. Slå på **Ansatte gir en konto**. Ansatte oppretter da personens bruker, deaktivert,
   det antallet dager før kontoens dag som **Forbered kontoen (dager før)** under
   [Kontoer](#kontoer) sier, aktiverer den den dagen og deaktiverer den dagen etter at
   siste arbeidsforhold slutter. Den slettes aldri. Slår du av flagget, slutter Ansatte
   å opprette og aktivere kontoen, men en som slutter, mister den likevel: dagen etter
   siste arbeidsforhold deaktiverer Ansatte en konto den har laget, uansett hva flagget
   sier (en konto HR koblet for hånd, blir latt være).
2. **Konto fra** er dagen kontoen aktiveres. Står den tom, er det første dag i
   personens tidligste arbeidsforhold som ikke er avsluttet, og feltet sier hvilken dag
   det er — eller at det ikke finnes ennå, så det er ingen dag å aktivere kontoen på.
   En dag du skriver inn, går foran arbeidsforholdene; tøm feltet for å følge dem igjen.
3. Velg **Adresse**: **E-post på jobb** eller **Privat e-post**. Har personen ikke den
   som er valgt, advarer fanen *… har ingen e-post på jobb registrert: kontoen venter
   til en legges inn*.
4. Velg **Kontotype** — hvem som har personens konto:
   - **Lokal** — Vantigo har passordet: Ansatte oppretter brukeren og inviterer personen
     til å velge et.
   - **Ekstern identitetsleverandør** — selskapets identitetsleverandør eier kontoen:
     Ansatte finner brukeren den opprettet når en eier har tatt den inn, aktiverer det
     Ansatte deaktiverte, og deaktiverer, men oppretter og inviterer aldri.
   - **Microsoft Entra ID** — tilbys bare så lenge en aktivert modul tilbyr den: Ansatte lar
     Entra opprette personens konto, aktiverer og deaktiverer den der, og ber Vantigo
     vente den, så brukeren den bringer, er personens uten en eier imellom.

   En ny person får **Standard kontotype** fra [Kontoer](#kontoer). Å flytte en person bort
   fra Microsoft Entra ID mens Entra har en konto for dem — aktivert, deaktivert eller
   stoppet, til Entra har slettet den eller meldt den borte — avvises — *Kontotypen kan ikke forlate Microsoft Entra ID mens Microsoft Entra ID har en
   konto for personen. Personen forlater katalogen med Deaktiver nå eller når
   arbeidsforholdet slutter.* En person med en
   Vantigo-bruker som fortsatt venter på invitasjonen, kan flyttes fra Lokal til
   Microsoft Entra ID: kontoen som kommer fra Entra, overtar den brukeren. Til Ansatte
   har satt alles type ved oppgraderingen som innførte typene, lagres ikke en endring av
   type: *Kunne ikke lagre* — *Identitet kunne ikke nås, eller ingen modul gir kontoer.
   Ingenting ble endret; …* — prøv igjen senere.
5. Trykk **Lagre kontoflagget**. Feltene lagres sammen. Har noen andre endret
   flagget siden du åpnet fanen, lastes fanen inn på nytt og sier *Noen andre lagret
   dette først; det er lastet inn på nytt*.

**Brukerkonto** sier først personens kontotype i en setning — *Lokal konto — Ansatte
oppretter brukeren og inviterer personen til å velge et passord.*, *Ekstern
identitetsleverandør — leverandøren eier kontoen: …* eller *Microsoft Entra ID — Ansatte
oppretter personens konto i katalogen, …* — eller *Kontoer er ikke tilgjengelige — …*
når ingen modul gir dem. For en person av typen Microsoft Entra ID viser den så
personens kontoer som kort side om side: **Vantigo**, brukeren personen logger inn i
Vantigo med, og et kort med leverandørens navn, **Microsoft Entra ID**, for kontoen
deres i selskapets katalog. For alle andre viser den Vantigo-brukeren alene.

Til Ansatte har opprettet eller funnet en bruker, sier Vantigo-delen *Ingen bruker ennå:
Ansatte har ikke opprettet eller funnet en for denne personen.* Deretter:

- **Tilstand** — *Forberedes — Ansatte oppretter eller finner brukeren*, *Venter — på
  at personen velger et passord*, *Aktiv*, *Deaktivert av Ansatte* eller *Deaktivert av
  en eier*, *Mislyktes*, *Stoppet*, eller *Koblet, ikke styrt av Ansatte* for en bruker
  HR koblet for hånd.
- **Bruker**, og **Styrt av Ansatte** — ja eller nei.
- Hvorfor kontoen står fast, i en setning, når den gjør det: ikke noe arbeidsforhold å gi en
  konto fra, adressen flagget angir mangler, en eier deaktiverte brukeren (eller den
  ventende brukeren, som Ansatte da aldri aktiverer), brukeren er borte, kontoen hører
  til et annet ansattnummer (den er noen andres og blir latt være), en ventende bruker
  opprettet for en lokal konto mens personens type ikke lenger inviterer, adressen i identitet er en annen
  enn personens etter at invitasjonen ble sendt (en eier endrer den i identitet), en
  invitasjon trukket tilbake av en eier eller tre ubesvarte, brukeren styres ikke lenger
  av Ansatte, en eier eller en systemadministrator, ingen bruker med ansattnummeret ennå
  hos identitetsleverandøren, adressen eller ansattnummeret holdt av en bruker Ansatte
  ikke styrer, ingen roller angitt for Ansatte — eller en som gir administrasjon — og en
  arbeider med en annen modus enn installasjonens. *Mislykkede forsøk på rad: 3* sier at
  identitet ikke kunne nås; arbeideren prøver igjen.
- **Invitasjon** — om en er åpen og kan godtas, den siste gikk ut ubesvart, en eier
  trakk den tilbake, eller tre ble ikke besvart, med hvor mange som er sendt.
- **Neste forsøk** og **Sist synkronisert**.

For en bruker Ansatte ikke styrer — en HR koblet for hånd, når flagget er på, eller en en
eier sluttet å styre — forteller fanen HR at en eier lar Ansatte styre brukeren på identitets side
**Brukere** (**La Ansatte styre**), med personens ansattnummer, og **Åpne Brukere** går
dit. En slettet person viser ikke dette steget.

Kortet **Microsoft Entra ID** viser personens konto i katalogen — eller *Ingen konto i
katalogen ennå: Ansatte har ikke opprettet eller funnet en for denne personen.*:

- **Tilstand** — *Forberedes — Ansatte har bedt katalogen opprette eller finne
  kontoen*, *Aktivert*, *Deaktivert*, *Slettet — kan gjenopprettes i katalogen i tretti
  dager*, *Mislyktes* eller *Stoppet*.
- **Brukerhovednavn (UPN)** — navnet personen logger inn hos Microsoft med, når Entra
  har laget kontoen — og **Aktivert siden** mens den er aktivert, eller **Sist
  synkronisert** mens den ikke er det.
- **Pass utstedt** — når Entra utstedte et midlertidig tilgangspass ingen har vist ennå
  — **Kunngjort** — når Ansatte ba Vantigo vente kontoen — og **Neste forsøk**.
- Hvorfor kontoen står fast, i en setning, når den gjør det: Entra ikke tilkoblet eller
  feiler, to kontoer i Entra med personens ansattnummer, e-posten på jobb er allerede en
  annen kontos der, en konto synkronisert fra en lokal katalog, en konto med en
  administrativ rolle i Entra, en konto Vantigo ikke opprettet og som ingen har
  bekreftet overtakelsen av på siden for Microsoft Entra ID (Ansatte skriver ingenting
  til den før noen gjør det), en konto som ikke ble slettet fordi den fortsatt er
  aktivert der, kontoen borte fra Entra (Ansatte lager aldri en til); og, én setning
  hver, hva Entra meldte og ikke fikk gjort: ansettelsesdatoen ikke skrevet, ingen
  lisens igjen (lisensen navngitt i parentes), et pass ikke tillatt eller avvist, et
  avslutningssteg ikke fullført (steget navngitt — Ansatte prøver igjen neste dag), lederens konto ikke
  funnet, personens leder ikke i Entra. *Mislykkede forsøk på rad: 3. Katalogen kunne
  ikke nås; arbeideren prøver igjen.* sier at Entra ikke kunne nås.

Under den er **Katalogprofil** en skrivebeskyttet tabell, **Felt**, **Skrives** og
**Verdi i dag**: hvert felt Ansatte skriver på kontoen, og verdien det har fra registeret
i dag. Navnene, ansattnummeret, brukerhovednavnet og bruksstedet skrives alltid og vises
grå, **Skrives** *Alltid*; de andre følger installasjonens [Katalogprofil](#katalogprofil),
**Skrives** *Ja*, eller *Av — sendes ikke* for et felt som er slått av der, som katalogen
beholder som det er, og som Ansatte
ikke lenger skriver. Personen ser tabellen på sin egen fane også: det er deres
opplysninger slik katalogen ser dem.

**Vis passet** (HR) tilbys mens Entra har et pass ingen har vist. Det viser passet Entra
utstedte for personens første innlogging i et vindu, **Midlertidig tilgangspass**, med
*… skriver inn dette passet ved første pålogging.*, *Virker til …*, **Kopier** og
**Lukk**: *Det vises ikke igjen.* Det vises **én gang**: når du lukker vinduet, kan ingen
se det igjen, og knappen er borte. Hvor lenge et pass virker, og om det bare virker for
én innlogging, er en innstilling i tilkoblingen til Microsoft Entra ID — åtte timer om
ikke endret — under [steget **Sett kontoreglene**](/nb/user/entra/#4-sett-kontoreglene). Gi det til personen selv; det sendes aldri på
e-post. Et pass som har gått ut, eller som allerede er vist, er borte — fanen sier
*Passet er allerede vist, har utløpt, eller ble aldri utstedt. Kjør nå (nytt pass)
utsteder et nytt.* — så bruk **Kjør nå (nytt pass)**.

**Kjør nå** (HR, mens flagget er på) gjør kontoens neste steg med en gang, med en ny
invitasjon til en ventende bruker hvis siste invitasjon ikke kan godtas. Fanen sier
da *Ferdig: en ny invitasjon ble sendt*, eller *Ferdig: ingen invitasjon ble sendt* —
ingen var forfalt, en kan fortsatt godtas, eller den siste gikk for under ti minutter
siden; kjøringen er gjort likevel. For en person av typen Microsoft Entra ID heter den
**Kjør nå (nytt pass)**: steget kjøres i Entra først, så i Vantigo, og et nytt pass
utstedes bare når kjøringen aktiverer kontoen — på personens første dag, eller når de
ansettes på nytt; en konto som allerede er aktivert, får ikke noe nytt pass, bare
opplysningene sine brakt i samsvar. Fanen sier da *Ferdig: når kontoen er aktivert, har katalogen
utstedt et nytt pass — Vis passet viser det én gang.* Når Entra avviser steget, sier
fanen *Identitetsleverandøren avviste steget; ingenting mer ble gjort der.* og hvorfor, i
en setning.

**Deaktiver nå** (HR) spør først — *Dette slår også av kontoflagget, så kontoen forblir
deaktivert til HR slår flagget på igjen* — deaktiverer så brukeren — for en person av
typen Microsoft Entra ID først Entra-kontoen, så Vantigo-brukeren — og sier *Kontoen er
deaktivert og kontoflagget er av.*, *Kontoen og kontoen i katalogen er deaktivert, og
kontoflagget er av.* når begge ble det, *Kontoflagget er av og kontoen i katalogen er
deaktivert.* når det ikke fantes noen Vantigo-bruker ennå, eller *Kontoflagget er av; det
fantes ingen konto å deaktivere.* Den tilbys
ikke for en bruker HR koblet for hånd under Selvbetjening, som ikke er Ansattes å
deaktivere: en eier deaktiverer den kontoen i identitet. Når brukeren ikke kan deaktiveres fra Ansatte, sier
fanen hvorfor, med flagget allerede av: Ansatte styrer ikke brukeren, brukeren er en
eier eller en systemadministrator (en eier deaktiverer dem på siden Brukere), eller
kontoen har et annet ansattnummer — eller, for Entra-kontoen, at Entra avviste. Når
identitet eller Entra ikke kan nås, er flagget av, og kontoarbeideren deaktiverer
kontoen så snart den kan. For en person av typen Microsoft Entra ID tilbys Deaktiver nå
også mens Entra-kontoen er aktivert, selv med flagget allerede av. Mens arbeideren jobber med
kontoen, sier Kjør nå og Deaktiver nå fra og endrer ingenting: prøv igjen om noen
minutter. Etter hver avvisning leser fanen kontoen på nytt.

På en [slettet person](#en-slettet-person) er fanen skrivebeskyttet, bortsett fra
**Deaktiver nå**: personens tilgang må også ta slutt.

Et kort **Grupper** viser [gruppene](#grupper) personen er med i — *gruppene personen
er med i, og dermed gruppene og lisensene en katalog gir dem*, arkiverte grupper
unntatt — med **Navn**, **Kom inn ved** (*Oppført* av HR, eller *Regelen*) og
**Siden**, eller sier *Ikke med i noen gruppe.* Det er skrivebeskyttet her: med
`staff:manage` åpner navnet til en gruppe gruppens side, der HR endrer medlemmene.

### En slettesperre

Med `staff:personal-data` har personsiden **Sett slettesperre** øverst. En slettesperre
holder opplysningene hele — ingen sletting etter oppbevaringsreglene rører dem, og
personen kan ikke slettes — så lenge en tvist, et krav eller en undersøkelse trenger det.

1. Trykk **Sett slettesperre**.
2. Skriv **Grunn** — påkrevd, opptil 500 tegn: hva sperren gjelder og, om du kan, til
   når.
3. Trykk **Sett slettesperren**. Siden viser **Under slettesperre** med dagen den ble
   satt og grunnen, og Historikk har **Slettesperre satt**.

For å oppheve den trykker du **Opphev slettesperren** og bekrefter. Neste
oppbevaringskjøring kan da slette det som har forfalt i mellomtiden; Historikk beholder
sperren og grunnen.

HR med bare `staff:manage` ser **Under slettesperre** og grunnen, men kan ikke sette
eller oppheve den. En leder og en kollega får ikke se sperren.

### En slettet person

Når en tidligere ansatts siste arbeidsforhold er slutt og fristen er gått, sletter
[oppbevaringskjøringen](#oppbevaring) personaladministrasjonen deres. HR
(`staff:manage`, `staff:personal-data`) ser da **Slettet** ved siden av personens status
og en melding, **Denne personen ble slettet** og dagen: kontaktopplysningene, adressen,
pårørende, fødselsnummeret, bankkontoen, HRs notater og personaldokumentene er borte;
arbeidsforholdene, historikken og lønns- og avtaledokumentene er igjen.

Personen er skrivebeskyttet, og siden tilbyr ingen av endringene — **Endre
opplysninger**, pårørende og rollene, å registrere fødselsnummeret eller bankkontoen, å
koble til en bruker, handlingene på arbeidsforholdene og vilkårene, og å arkivere, endre
eller fjerne et dokument er ikke der, fordi Ansatte ville avvist dem. Dette kan
fortsatt gjøres:

- [slettesperren](#en-slettesperre) og [den nye vurderingen](#den-nye-vurderingen);
- **Koble fra** under Selvbetjening, så brukeren kan kobles til en annen;
- **Deaktiver nå** på fanen [Konto](#konto);
- lesing: Historikk, Dokumenter og nedlastingene, og
  [personopplysningsfilen](#last-ned-en-persons-opplysninger);
- [Ansett på nytt](#ansett-på-nytt).

#### Ansett på nytt

Når en slettet tidligere ansatt kommer tilbake, ansetter du dem på nytt på deres egen
oppføring i stedet for å legge til en ny person, så historikken blir samlet på ett sted.
Med `staff:manage`:

1. Åpne personen, gå til **Arbeidsforhold** og trykk **Ansett på nytt**.
2. Fyll inn arbeidsforholdet som i [steg 2 under Ny person](#steg-2-det-første-arbeidsforholdet)
   og trykk **Lagre arbeidsforholdet**.

Personen åpnes igjen med samme ansattnummer: meldingen forsvinner, og Historikk har
**Ansatt på nytt** før det nye arbeidsforholdet. Det slettingen fjernet, forblir tomt —
skriv inn kontaktopplysningene, adressen og pårørende igjen, og, med
`staff:personal-data`, fødselsnummeret og bankkontoen.

#### Den nye vurderingen

Tolv måneder etter at en slettet persons siste arbeidsforhold sluttet, viser
[dashbordet](#dashbordet) **Ny vurdering** for `staff:personal-data`, og punktet åpner
personsiden. Meldingen der sier om den nye vurderingen er registrert.

1. Gå gjennom det som er igjen av oppføringen.
2. Trykk **Registrer ny vurdering** i meldingen og bekreft.

Meldingen sier da **Ny vurdering registrert** og dagen, og punktet på dashbordet
forsvinner. Før den har forfalt, eller når den er registrert, avviser Ansatte den, og
siden sier at den nye vurderingen registreres én gang, fra tolv måneder etter at siste
arbeidsforhold sluttet. En nyansettelse fjerner den nye vurderingen sammen med
slettingen. Med bare `staff:manage` ser du om den er registrert, men kan ikke registrere
den.

### Last ned en persons opplysninger

Med `staff:personal-data` lagrer **Last ned personopplysninger** øverst på personsiden
alt Ansatte har om personen i én fil, `staff-<ansattnummer>-personal-data.json`:
oppføringen med fødselsnummeret og bankkontoen i sin helhet, HRs notater, pårørende,
rollene, hvert arbeidsforhold med vilkårene, brukerkoblingen, listen over dokumenter og
hele historikken. Den er svaret på en innsynsbegjæring — se
[Svar på en innsynsbegjæring](/nb/admin/staff/#svar-på-en-innsynsbegjæring).

Siden spør først, fordi filen åpner de forseglede verdiene: nedlastingen føres i
personens historikk som **Personopplysninger eksportert**, med navnet ditt.

## Legg til en ny person

Med `staff:manage` trykker du **Ny person** under Personer, eller velger **Ny person**
blant søkets hurtighandlinger, som åpner Personer med skjemaet allerede åpent. Skjemaet har to steg, slik
en ansettelse har: personen, så det første arbeidsforholdet. Personen lagres i første
steg, så du kan stoppe der og legge til arbeidsforholdet senere — personen står da
under **Nåværende** i Personer med statusen **Ikke ansatt**.

### Steg 1: personen

1. Fyll inn **Fornavn** og **Etternavn**. De er påkrevd (opptil 100 tegn hver).
   **Visningsnavn** er valgfritt; tomt blir det de to navnene.
2. Ansattnummeret **lages automatisk** som standard: skjemaet sier hvilket nummer som
   er neste — prefikset og neste nummer fra innstillingene — med mindre noen allerede
   har det, og da brukes det neste ledige. Vil du gi det selv, slår du på **Velg eget
   ansattnummer** og skriver det inn: opptil 16 bokstaver, sifre eller
   bindestreker, unikt uansett store og små bokstaver. Nummeret endres aldri senere.
3. Legg inn **E-post på jobb**, **Jobbtelefon**, **Språk** og **Arbeidsland** når
   du har dem. En e-post på jobb som en annen person har, avvises på feltet.
4. **Private opplysninger (valgfritt)** åpner fødselsdato, kjønn, mobiltelefon og privat
   telefon, privat e-post og hjemmeadresse. Ingen av dem kreves for å opprette
   personen.
5. Trykk **Lagre og fortsett**. En avvisning havner på feltet den gjelder.

Når fornavn, etternavn og fødselsdato er fylt inn, spør **Lagre og fortsett** først om
en tidligere ansatt med samme navn og fødselsdato er [slettet](#en-slettet-person) —
Ansatte kan ikke kjenne dem igjen på fødselsnummeret, som slettingen fjernet. Er det
noen, sier skjemaet **Noen med disse navnene og denne fødselsdatoen er slettet**, lister
dem med ansattnummer og dagen de ble slettet, og venter:

- **Åpne** med ansattnummeret åpner den slettede oppføringen; er det samme person,
  [ansetter du dem på nytt](#ansett-på-nytt) der.
- **Opprett likevel** legger til den nye personen med et nytt ansattnummer.

Uten fødselsdato spørres det ikke. Kan spørsmålet ikke besvares, opprettes personen uten
det.

### Steg 2: det første arbeidsforholdet

Steget åpner med en påminnelse, og den er grunnen til at to felter er merket som
påkrevd: etter aml. § 14-6 skal arbeidsavtalen si om ansettelsen er fast eller
midlertidig, og hvor stor **stillingsprosenten** er. Mangler en av dem, legges
arbeidstakerens påstand til grunn — derfor fyller Ansatte aldri inn disse for deg, og
avviser vilkår uten dem.

1. **Virksomhet** er forhåndsvalgt til standardvirksomheten; velg en annen hvis
   arbeidsforholdet rapporteres et annet sted. **Startdato** er forhåndsutfylt med i dag
   i virksomhetens tidssone.
2. Arbeidsforholdsnummeret — a-meldingens arbeidsforholdID — lages fra ansattnummeret
   (`E-8-1` for ansatt `E-8`). Slå på **Velg eget arbeidsforholdsnummer** for å skrive
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

**Legg det til senere** lukker skjemaet med personen lagret og åpner siden deres, der
**Nytt arbeidsforhold** under Arbeidsforhold legger det til.

## Grupper

**Grupper** (`staff:manage`) er der HR holder selskapets grupper: sett av personer, som
holdes i selskapets katalog — Microsoft Entra ID — når du slår den på for en gruppe, der
de blir en sikkerhetsgruppe eller en Microsoft 365-gruppe (og et team, om du ber om det).
En gruppes lisenser er hvordan medlemmene får sine i Microsoft 365. Det finnes to typer:

- **Oppført** — *Du oppgir medlemmene.*
- **Avledet** — *En regel gir medlemmene, oppdatert hver virkedag*: hvem som kommer inn
  og hvem som går ut, skrives i hver persons historikk.

### Listen

**Grupper** viser gruppene etter **Navn**, med **Nøkkel**, **Type**, **Medlemmer** (hvor
mange), **Leverandører** — et merke for hver katalog HR har slått på for gruppen, for
eksempel *Microsoft Entra ID: Synkronisert*, *Microsoft Entra ID: Mislyktes* eller
*Microsoft Entra ID: Av*, eller *—* når det ikke finnes noen — og **Status**, *Arkivert*
for en arkivert gruppe. Arkiverte grupper er skjult, og nedtonet, til du slår på **Vis
arkiverte**. Trykk på navnet til en gruppe for å åpne den. Uten noen gruppe ennå sier
siden *Ingen grupper ennå* — *Legg til en gruppe med Ny gruppe.*

### Legg til en gruppe

1. Trykk **Ny gruppe**.
2. Fyll ut **Nøkkel** — små bokstaver, sifre og bindestreker, høyst 60, som begynner med
   en bokstav eller et siffer, unik. Det er slik katalogen kjenner gruppen (e-postaliaset,
   med mindre du setter et annet), og den **kan ikke endres senere**. Så **Navn** og, om
   du vil, en **Beskrivelse**.
3. Velg **Type**, **Oppført** eller **Avledet**. For **Avledet** velger du **Regel**:
   - **Alle ansatte** — *Alle med et arbeidsforhold som gjelder i dag.*;
   - **Avdeling** — de hvis vilkår plasserer dem i avdelingen du velger; slå på **Ta med
     avdelingene under** for å ta med *også personene i hver avdeling under denne*;
   - **Virksomhet** — de som er ansatt i den;
   - **Arbeidssted** — de hvis vilkår plasserer dem der;
   - **Ansattype** — de av typen du velger;
   - **Rapporterer til** — velg **Leder**: *lederens direkte underordnede i dag, ett nivå
     ned*.

   En person med flere arbeidsforhold vurderes etter det som begynte først.
4. Trykk **Lagre**. En nøkkel som allerede er tatt, eller en regel uten det den trenger,
   markeres på feltet. Siden sier *… er lagret* og åpner den nye gruppen. En avledet
   gruppe fylles ved kontoarbeiderens neste kjøring, innen en time; en oppført gruppe
   starter tom.

### Gruppens side

Toppen av siden viser gruppens navn, type, nøkkel og regel med ord — *avdelingen Salg og
avdelingene under*, *rapporterer til Kari Nordmann* — og beskrivelsen. **Endre** åpner
skjemaet, som viser *Nøkkel: … — den endres aldri.* i stedet for nøkkelen; typen kan
endres der også. Har noen andre lagret gruppen etter at du åpnet den, sier siden fra og
leser den på nytt. **Arkiver** spør først. **Slett** finnes bare så lenge ingen katalog
har gruppen eller noen gang har synkronisert den, og ingen er slått på for den, arkivert
eller ikke; ellers arkiverer du den — eller, når ingen katalog har laget den ennå, slår
du av katalogene først. En katalog som sender gruppen akkurat da, avviser likevel
slettingen. Den spør *Slette …?* — *Gruppen, medlemmene, eierne og innstillingene slettes.
Ingen katalog har den ennå.* — og hvert medlem får det registrert i historikken sin at
de gikk ut av gruppen.

- **Medlemmer** — for en oppført gruppe, *Personene du oppgir, fra dagen du lagrer dem*:
  velg dem under **Medlemmene** og trykk **Lagre medlemmene**; hver som kommer inn eller
  går ut, skrives i historikken deres. For en avledet gruppe er listen skrivebeskyttet,
  *Hentes daglig fra regelen*. Hvert medlem vises med **Ansattnummer**, **Kom inn ved** —
  *Oppført* eller *Regelen* — og **Siden**; en tom gruppe sier *Ingen er med i
  gruppen.*
- **Eiere** — *Personene som eier gruppen i hver katalog, ved siden av Ansatte selv*:
  velg dem under **Eierne** og trykk **Lagre eierne**, for en oppført eller en avledet
  gruppe. En eier trenger en aktivert konto i katalogen for å være det der; endringen
  kommer dit ved kontoarbeiderens neste kjøring.

Under dem står et kort for hver katalog Ansatte kan holde grupper i, med katalogens navn —
**Microsoft Entra ID**. Til katalogen er koblet til, sier kortet *Microsoft Entra ID er
ikke koblet til ennå; gruppen lages der når den er det.* Slå på **I Microsoft Entra
ID**, så viser kortet gruppens innstillinger der, som katalogen selv beskriver: hver med
etiketten på ditt språk, *Om denne innstillingen* som lenker til Microsofts side, de
påkrevde merket og standardverdiene fylt ut. For Microsoft Entra ID er det
**Gruppetype** — *Sikkerhet* eller *Microsoft 365*, som ikke kan endres når gruppen er
laget — **E-postalias**, som heller ikke kan endres, **Synlighet** for en Microsoft
365-gruppe, **Beskrivelse i leietakeren**, **Team i Microsoft Teams** for en Microsoft
365-gruppe, og **Lisenser** medlemmene får, hver med setene som er ledige. Trykk
**Lagre**: en verdi som ikke passer, markeres ved feltet, og ellers sier siden *Lagret
for Microsoft Entra ID; gruppen følger ved kontoarbeiderens neste kjøring*.

Merket på kortet sier så hvordan gruppen står: *Venter* til den er sendt, *Funnet i
Microsoft Entra ID og venter på bekreftelse der*, *Synkronisert siden …* med *Gruppen
finnes også i Microsoft Entra ID*, *Mislyktes*, *Beholdt, omdøpt* eller *Slettet*. Når
katalogen avviste en innstilling — en type eller et e-postalias endret etter at gruppen
ble laget, et team uten noen person blant eierne — sier kortet *Mislyktes:* og hvorfor
ved den innstillingen; en avvisning av selve gruppen — *katalogen har en gruppe med
denne nøkkelen som Ansatte ikke får skrive* — står øverst på kortet. Medlemmer katalogen
ikke kunne endre, står der også, og prøves igjen ved neste kjøring. Endre verdien og
lagre igjen — eller endre gruppens navn, beskrivelse eller eiere — for å få den prøvd på
nytt. I mellomtiden holdes medlemmene i en gruppe katalogen allerede har, i samsvar
likevel: en person som går ut av gruppen i Ansatte, går ut av den der også. Én gang i
døgnet setter Ansatte medlemmene i katalogen til gruppens igjen, så et medlem noen la
til der for hånd, fjernes.

**En gruppe funnet i katalogen.** Når Microsoft Entra ID allerede hadde en gruppe med
e-postaliaset som Vantigo ikke laget, kobler Vantigo den, men skriver ingenting til den
før noen som administrerer Microsoft Entra ID, bekrefter den på siden for Microsoft
Entra ID ([Bekreft en gruppe Vantigo ikke opprettet](/nb/user/entra/#bekreft-en-gruppe-vantigo-ikke-opprettet)).
Til da sier merket *Funnet i Microsoft Entra ID og venter på bekreftelse der* og kortet
*katalogen hadde en gruppe med denne nøkkelen som Ansatte ikke laget; den skrives til
først når noen bekrefter den der*. Det er ingen feil: arbeideren spør igjen ved hver
kjøring, og gruppen følger ved kjøringen etter bekreftelsen. Et team lages når gruppen er femten minutter
gammel, så det kommer en kjøring etter gruppen. Når katalogen ikke kunne si hva en
gruppe kan ha, sier kortet *Microsoft Entra ID kunne ikke si hva en gruppe kan ha der.
Prøv igjen senere.*

**Å slå av bryteren** spør *Slå av Microsoft Entra ID for …?* og sier hva som skjer, etter
**Slett en ekstern konto (dager etter slutt)** under [Kontoer](#kontoer): *Gruppen
slettes i Microsoft Entra ID etter N dager.*, eller, når den står tom, *Gruppen beholdes i
Microsoft Entra ID, omdøpt til «… (archived)».* **Slå av** bekrefter, og siden sier
*Microsoft Entra ID er slått av for gruppen*. Innstillingene beholdes. En katalog du slo
på, men aldri lagret for, slås bare av igjen, uten å spørre.

**Slettet i Microsoft Entra ID** gjelder en Microsoft 365-gruppe Vantigo laget, som
leietakeren kan gjenopprette i tretti dager. En sikkerhetsgruppe Vantigo laget, slettes
aldri der, siden Microsoft ikke kan gjenopprette en: når dagene er gått, fjernes
personene fra den, den omdøpes «… (archived)» og beholdes. En gruppe Vantigo fant i
stedet for å lage, blir stående i Microsoft Entra ID som den er. Kortet sier *Slettet* i
hvert tilfelle; [administrasjonsveiledningen](/nb/admin/entra/#ansattes-grupper-i-leietakeren)
har detaljene.

**Arkiver** spør *Arkivere …?* — *Gruppen beholder medlemmene og blir skrivebeskyttet. I
hver katalog slettes den etter dagene innstillingene i Ansatte sier, eller omdøpes
«… (archived)» og beholdes når de sier aldri.* — og gjør det for hver katalog. Siden sier
så *Gruppen er arkivert: den beholder medlemmene slik de var og kan ikke lenger
endres.*, viser eierne som en liste, og tilbyr verken **Endre**, **Arkiver**, **Lagre**
eller bryteren. Ingenting slettes i Ansatte.

## Min side

**Min side** (`staff:access`) er din egen oppføring i Ansatte, funnet gjennom brukeren
HR koblet den til, og viser det du ser av deg selv — uansett hvilke andre rettigheter du
har. Den har de fire fanene en persons side har, og så **Fravær**, **Konto** og, når du er
leder for noen, **Mitt team**:

- **Profil** — jobbopplysningene, de private opplysningene, fødselsnummeret som
  **Registrert** og de fire siste sifrene i bankkontoen, de pårørende, rollene og hvor
  du hører hjemme i organisasjonen, og under dem **Gruppene dine**: ved navn, gruppene
  du er med i, slik HR holder dem (*Du er ikke med i noen gruppe.* når det ikke finnes
  noen). Trykk **Endre opplysningene mine** for å endre
  mobil og privat telefon, privat e-post, hjemmeadresse og pårørende, og så **Lagre**.
  Alt annet — navnet, jobbopplysningene, fødselsdatoen og kjønnet, fødselsnummeret og
  bankkontoen — endres av HR: spør HR. Har HR lagret oppføringen din mens du endret,
  lukkes skjemaet på det som står der nå, og siden sier fra.
- **Arbeidsforhold** — arbeidsforholdene dine og vilkårene, lønnen medregnet: det er
  din arbeidsavtale.
- **Historikk** — endringene i oppføringen din, uten HRs notater.
- **Dokumenter** — dokumentene dine, helsedokumenter medregnet, til nedlasting; HR
  arkiverer og endrer dem. Under **Mine data** lagrer **Last ned mine data** en fil
  (JSON) med oppføringen din, pårørende, roller, arbeidsforhold og vilkår,
  opplysninger om dokumentene dine og historikken din — uten verdiene HR holder
  forseglet (hele fødselsnummeret og bankkontoen) og uten HRs notater. Filen lister
  dokumentene uten innholdet: last ned hvert av dem fra listen. Å laste ned dataene
  dine registrerer ingenting. Når Ansatte ga deg kontoen, deaktiverer den kontoen dagen
  etter siste arbeidsforhold, så Min side og **Last ned mine data** forsvinner med den:
  be HR om dataene dine, så laster de dem ned for deg med eksporten av
  personopplysninger.
- **Fravær** — feriekontoen din, egenmeldingsstatusen, omsorgsdagene og fraværene
  dine, med **Be om fravær** (se [Be om fravær](#be-om-fravær-fra-mobilen) nedenfor).
- **Konto** — om Ansatte gir deg en bruker å logge inn med, fra hvilken dag og på
  hvilken adresse, og hvordan kontoen din står: venter på passordet ditt, aktiv,
  deaktivert, med invitasjonen din, og kortet **Grupper**: gruppene du er med i, hvordan
  du kom inn og siden når. Den er skrivebeskyttet: spør HR om å endre den.
- **Mitt team** — bare når du er leder for noen i dag: søknadene du avgjør, fraværet
  denne måneden til dem du er leder for, og de åpne sykefraværssakene deres (se
  [Mitt team](#mitt-team) nedenfor).

Har HR ikke koblet brukeren din til en oppføring ennå, sier Min side *Du har ingen
oppføring i Ansatte ennå* og forklarer hvordan den kommer på plass; spør HR hvis du
mener din mangler.

Min side er laget for mobilen først: på en smal skjerm står alt i én kolonne, skjemaet
fyller hele skjermen, og søknadsarket åpnes nedenfra.

### Fraværet ditt

Fanen **Fravær** på Min side viser, slik Ansatte teller dem:

- **Feriekonto** for i år, i arbeidsdager: **Ferierett**, **Overført** (og **Overført
  automatisk**, når selskapet overfører ubrukte dager av seg selv), **Tatt ut**,
  **Søkt om, venter** og **Igjen**. Vil det du har søkt om, ta mer enn det som står igjen,
  sier kortet fra — søknadene sendes likevel.
- **Egenmelding** — selskapets regler (*Lovens minimum*, *Utvidet ordning* eller
  selskapets egne grenser), hvor mange kalenderdager du kan melde selv om gangen, hva du
  har brukt de siste 12 månedene mot grensen, og fra når du kan. Har HR trukket tilbake
  retten din til egenmelding, står det til når og hvorfor.
- **Omsorgsdager** for i år — **Rett**, **Brukt** og **Igjen** — ut fra opplysningene HR
  har registrert om barna dine; er de ikke registrert ennå, står det.
- **Mitt fravær**, nyeste først, hvert med type, dager, status (*Søkt om*, *Godkjent*,
  *Avslått*, *Trukket tilbake* eller *Registrert*), arbeidsdager eller timer, merknaden
  din og merknaden til avgjørelsen. En søknad som venter, har **Endre** og **Trekk
  tilbake**; en søknad som er trukket tilbake, blir stående i listen.

### Be om fravær fra mobilen

1. På Min side åpner du **Fravær** og trykker **Be om fravær**. På mobilen — en skjerm
   smalere enn 768 piksler — åpnes arket fra bunnen; bredere er det en dialog.
2. Velg **Type**. Arket spør etter det typen tar:
   - en dagtype: **Første dag** og, for mer enn én dag, **Siste dag**;
   - en timetype, som lege- eller tannlegebesøk: **Dag** og **Timer**;
   - en type som tillater det: **Prosent av hver dag** (står den tom, hele dagen);
   - en sykmelding: **Grad** (står den tom, 100 %);
   - en **Merknad**, unntatt på sykefravær og sykt barn, som ikke har noen.
3. For en dagtype anslår arket arbeidsdagene — mandag til fredag, uten helligdagene —
   og, for ferie, omtrent hvor mange dager du har igjen etterpå. For en egenmelding
   viser det hva reglene tillater deg i dag før du sender.
4. Trykk **Send søknad** for en type som trenger en avgjørelse, eller **Registrer** for en
   som er en melding. Bekreftelsen gir arbeidsdagene og feriekontoen slik Ansatte talte
   dem.

Du kan også begynne hvor som helst: søk etter **Be om fravær** (Ctrl+K eller ⌘K), så
åpnes arket på fanen **Fravær** på Min side.

Avviser Ansatte det, sier arket hvorfor ved feltet det gjelder — et annet fravær på en av
dagene ved **Første dag**; en egenmelding utover det reglene tillater, før du har vært
ansatt lenge nok, eller mens retten din er trukket tilbake ved **Type**, med beskjed om å
registrere en sykmelding i stedet. For å endre en søknad som venter, trykker du **Endre**,
retter og trykker **Lagre**; har noen endret den imens, lastes den inn på nytt, og siden
sier fra. En endring beholder hva slags fravær det er: en søknad forblir en søknad, og en type som
røper helse eller familieforhold byttes ikke mot en som ikke gjør det, eller omvendt — de
avgjøres av forskjellige personer. Trykk **Trekk tilbake** og søk om det andre i stedet.

### Mitt team

**Mitt team** er en fane på Min side for alle som er leder for noen i dag etter vilkårene —
en teamleder trenger ingen annen rettighet for dem hen er leder for. Den viser:

- **Godkjenninger** — søknadene du avgjør, eldste først, hver med personen, typen,
  dagene, arbeidsdagene og personens merknad. Trykk **Godkjenn**, eller **Avslå**, skriv **Begrunnelse** og
  trykk **Avslå søknaden**: et avslag sier alltid hvorfor. Har noen andre avgjort søknaden
  først, eller personen trukket den tilbake, sier siden fra og leser innboksen på nytt.
- **Fravær i** inneværende måned — fraværet til hver av dem du er leder for, slik du kan
  se det.
- **Åpne sykefraværssaker** for dem du er leder for — oppfølgingsmilepælene med datoene:
  **Marker som gjort**, eller **Frafall** med hvorfor den ikke trengs (aldri en diagnose);
  slutten på arbeidsgiverperioden følger av fraværene og er ingen av delene. **Endre
  tilretteleggingsnotatet** skriver notatene om tilrettelegging, som personen også leser.

## Fraværssiden

**Fravær** i sidemenyen har to faner. Den åpner på **Godkjenninger** når du avgjør
søknader, og ellers på **Kalender**; søknadspunktene på dashbordet åpner den også — en
leder uten annen rettighet enn `staff:access` havner der på **Godkjenninger**, med de
samme søknadene som under [Mitt team](#mitt-team).

### Kalender

**Kalender** viser en måned: en rad per person og fraværene deres som stolper over
dagene, med helger og helligdager skyggelagt. Bla med **Forrige måned** og **Neste
måned**; med tilgang til ansattoversikten eller HR-rettigheten kan du avgrense med
**Avdeling** (eller **Alle avdelinger**). En stiplet stolpe er en søknad som venter. Hvem
som vises, følger rettighetene dine: med HR-rettigheten, godkjennerrettigheten eller
personopplysningsrettigheten alle som er ansatt i måneden; med bare ansattoversikten
de av dem som er aktive i dag; en leder uten disse, dem hen er leder for og seg selv.

Hver stolpe viser bare det du har lov til å se: typen og statusen, en prosent av dagen
eller timene for HR og personens leder; *Permisjon* for en type som røper helse eller
familieforhold, når den er godkjent; og *Fraværende* — bare dagene, uten type eller status,
en persons dager samlet i én stolpe og ingen besøk på noen timer — for en kollega som ser
ansattoversikten. Pek på en stolpe, eller les den med skjermleser,
for dagene og detaljene. På mobilen ruller måneden sidelengs inne i rammen sin.

### Godkjenninger

**Godkjenninger** viser søknadene du avgjør, eldste først — den samme innboksen som under
[Mitt team](#mitt-team), for dem som avgjør i kraft av rettighet: godkjenneren
(`staff:approve`) og HR (`staff:manage`) avgjør alles søknader av en vanlig type, og den
som har `staff:personal-data`, søknadene av en type som røper helse eller
familieforhold, som ikke når noen andres innboks. Hver viser personen, typen, dagene,
arbeidsdagene og personens merknad. Trykk **Godkjenn**, eller **Avslå**, skriv
**Begrunnelse** og trykk **Avslå søknaden**. Har noen andre avgjort den først, eller
personen trukket den tilbake, sier siden fra og leser innboksen på nytt; har personen
allerede fravær på en av dagene, kan søknaden ikke godkjennes slik den er. Er du den
eneste som har `staff:personal-data`, står også din egen søknad av en slik type i
innboksen din, og du godkjenner den selv; alle som har den rettigheten, ser den da på
dashbordet til fraværet er over.

#### E-postene den som avgjør, får

- **En leder** får e-post til jobb-e-posten (ellers den private) når noen de avgjør for,
  søker om fravær — personen, typen (*fravær* for sykdom) og dagene — eller registrerer
  det — personen, *fravær* og dagene. En leder får aldri e-post om en type som røper
  helse eller familieforhold.
- **Godkjenneren og HR** får e-post bare om en person uten noen leder som kan avgjøre —
  ingen i arbeidsvilkårene eller avdelingen, eller ingen koblet til en aktiv bruker — og
  bare om søknader, til adressen brukeren deres har; det samme gjør en **Owner**.
  E-posten nevner personen, typen og dagene og lenker til **Fravær**.
- **Den som har `staff:personal-data`**, får e-post om hver søknad av en type som røper
  helse eller familieforhold, fra noen andre: «En fraværssøknad venter på avgjørelsen
  din», uten navn, type eller dager — åpne **Godkjenninger** for å se den.

## Fravær og ferien din

Reglene bak skjermbildene over:

- **Å be om fri.** En type som trenger en avgjørelse — ferie, en permisjon, avspasering —
  blir *søkt om*; en type som er en melding — egenmelding, sykmelding, sykt barn, lege-
  eller tannlegebesøk — blir *registrert* med en gang. Du legger inn ditt eget; HR legger
  inn på dine vegne. Lederen din legger ikke inn for deg. Et legebesøk er timer på én
  dag; alt annet er hele dager, noen typer som en prosent av hver dag. Sykefravær har
  ingen merknad.
- **To fravær samme dag** kan stå sammen bare når begge typene tillater en prosent og
  prosentene til sammen er høyst 100 % — men en sykmelding av hvilken som helst grad kan
  stå ved siden av en permisjon som lar en del av dagen stå igjen, siden legen graderer
  den mot arbeidet som er igjen. En søknad du har sendt teller som om den var
  innvilget, så du kan ikke søke to ganger om samme dag. Et besøk på noen timer, som
  hos legen, kan stå ved siden av hvilket som helst dagfravær; besøk samme dag kan til
  sammen være høyst 24 timer.
- **Syk i ferien.** En sykmelding endrer ikke den innvilgede ferien din av seg selv. Er
  du helt arbeidsufør (100 % sykmelding), kan du kreve at ferien utsettes — med
  legeerklæring, og senest siste arbeidsdag før ferien når du blir syk før den, og uten
  ugrunnet opphold etter at du er tilbake i arbeid når du blir syk i den; si fra til HR,
  som registrerer det. Ferien deles da rundt sykmeldingen, og dagene går tilbake
  til kontoen din. En sykmelding over en egenmelding erstatter dagene den dekker.
- **Egenmelding** følger selskapets regler: med lovens minimum tre kalenderdager om
  gangen og fire ganger på tolv måneder. Dager på rad er én egenmelding, og en helg
  mellom to teller med — egenmelding fredag og igjen mandag er fire dager; én sykdom
  rundt en sykmelding er én egenmelding. Den avvises,
  med begrunnelse, når du har brukt det reglene tillater, før du har vært ansatt de
  månedene de krever, etter sekstende dag av et sykefravær, eller mens HR har trukket
  tilbake retten din; da trenger du sykmelding. Egenmeldingsstatusen din viser hva du
  har brukt de siste tolv månedene.
- **Sykefraværssaken din.** Sykefraværene dine samles i en sak — en ny når det har gått
  mer enn seksten dager siden forrige. Oppfølgingen — slutten på arbeidsgiverens
  seksten dager (regnet fra din første arbeidsdag borte), oppfølgingsplanen etter fire uker, dialogmøtene etter sju og tjueseks,
  NAVs aktivitetskrav etter åtte — markerer lederen din og HR. En sak har aldri
  en diagnose: det finnes ikke noe sted å skrive en.
- **Omsorgsdager** for sykt barn teller mot dagene dine for året — 10 for ett eller to
  barn, 15 for tre eller flere, flere når du er alene om omsorgen eller har et kronisk
  sykt barn — ut fra opplysningene HR har registrert. Går du over, vises det, men det
  avvises aldri.
- **Når du ombestemmer deg.** Mens en søknad venter, kan du endre den eller trekke den
  tilbake (en tilbaketrukket søknad blir stående i historikken); for å gjøre den om til
  en registrering — eller omvendt — trekk den tilbake og legg inn den andre. Et registrert
  fravær — også sykefravær — er HRs å endre eller fjerne: slutter sykmeldingen tidligere,
  si fra til HR.
- **Hvem som avgjør.** Lederen din, godkjenneren eller HR godkjenner eller avslår
  søknaden din; et avslag sier hvorfor. Ingen godkjenner sitt eget: en leders søknad går
  til lederens egen leder. Når den er godkjent, kan den som avgjør fraværet ditt, trekke
  den tilbake til fraværet begynner, og deretter bare HR; HR retter et avgjort eller registrert fravær når dagene var feil. Du
  får e-post når søknaden din er avgjort, trukket tilbake eller rettet, og lederen din når du
  søker om eller registrerer fravær — e-posten nevner aldri sykdom, en HR-type, en
  prosent eller en merknad, og en registrering når lederen din bare som «fravær».
- **Permisjon som røper helse eller familieforhold** (en HR-type, som
  svangerskapspermisjon) avgjøres av den som har rettigheten for personopplysninger, ikke
  av lederen din, og lederen din får ikke e-post om den; lederen ser den først når den er
  godkjent, som *permisjon*.
- **Å avgjøre, for ledere.** Søknadene du avgjør, venter i godkjenningsinnboksen din,
  eldste først — under [Mitt team](#mitt-team), og på [fraværssiden](#fraværssiden) for
  dem som avgjør i kraft av rettighet; fraværskalenderen viser en måned for teamet ditt —
  eller, med tilgang til ansattoversikten, for alle, der kolleger bare ser dagene noen er borte.
- **Feriekontoen din** teller i arbeidsdager, mandag til fredag uten helligdager:
  retten din etter selskapets ferieordning (25 virkedager er 21 arbeidsdager; mer fra
  året du fyller 60; færre når du begynner etter 30. september), det HR overførte fra i
  fjor, det du har tatt ut, det du har søkt om og det som står igjen. En søknad som tar
  deg forbi det som står igjen, sendes likevel — kontoen viser den som overtrukket.

Hvem ser fraværet ditt: du, lederen din og HR ser datoene, statusen, typen og detaljene;
kolleger med tilgang til ansattoversikten ser bare dagene du er borte — aldri om det var
en søknad eller en registrering, typen, en prosent eller en merknad, og dagene dine
flyter sammen til ett spenn, så en sykedag ser ut som en fridag og en sykmelding over en
ferie aldri synes. Et besøk på noen timer vises ikke for kolleger i det hele tatt. En
type som avslører helse eller familieforhold (som svangerskapspermisjon eller en
religiøs høytid) viser lederen din bare *permisjon*, som all annen permisjon — en
svangerskapskontroll, som er timer, vises ikke for lederen i det hele tatt — og
historikken for den vises ikke for lederen, så ingenting ved hvordan den ble lagt inn,
røper den.

## Dashbordet

**Hjem**, dashbordet, har et **Ansatte**-kort for den som har `staff:view` eller
`staff:manage`: hvor mange som er ansatt i dag, og under det årsverkene i dag, hvor
mange som begynner og hvor mange som slutter innen 30 dager, hvor mange som er borte i
dag — på godkjent eller registrert fravær i hele eller deler av dager, uansett type (et
besøk på noen timer teller ikke) — og, når det finnes noen,
oppføringene uten noe arbeidsforhold. Trykk på kortet for å åpne Ansatte. Med bare
`staff:access` har du Ansatte-appen under **Moduler** og ikke noe kort. De åpne
sykefraværssakene og sykefraværet de siste tolv månedene leses gjennom API-et bare av
`staff:manage`, `staff:approve` og `staff:personal-data`, aldri av `staff:view`.

Punktene fra Ansatte kommer i **Trenger oppfølging** sammen med de andre appenes, hvert
formulert på ditt språk. De fleste er HRs (`staff:manage`); en leder ser punktene om
prøvetid, søknader og sykefraværsoppfølging for sine egne folk, `staff:approve`
søknadene og oppfølgingen for alle, og den nye vurderingen og de fortrolige søknadene
er `staff:personal-data` sine. En søknad om en fortrolig (bare HR) fraværstype telles
bare for `staff:personal-data`:

| Punkt | Det betyr | Åpner |
| --- | --- | --- |
| Ingen signert arbeidsavtale | Et arbeidsforhold begynte for sju dager siden — eller i dag, for en som leies ut eller et midlertidig arbeidsforhold som ved starten var avtalt å vare under en måned — uten signeringsdato og uten signert arbeidsavtale arkivert for det. Arkiver den signerte avtalen på personsiden, eller skriv inn datoen den ble signert. | Personen |
| Prøvetiden går ut | Prøvetiden, eller forlengelsen av den, går ut innen 30 dager. En leder ser dette for arbeidsforholdene vedkommende er leder for. | Personen |
| Midlertidig i et år | Et midlertidig arbeidsforhold har vart et år: personen skal ha skriftlig varsel en måned før det slutter. | Personen |
| Midlertidig i tre år | En rekke midlertidige arbeidsforhold på grunnlag a eller b har vart tre år, og personen kan ha rett til fast ansettelse. | Personen |
| Ingen leder | En som er ansatt i dag har ingen leder i arbeid: ingen er oppgitt, eller den som er oppgitt har sluttet, og ingen i arbeid leder avdelingen eller en avdeling over den. En leder som har sluttet, hoppes over: folkene går til nærmeste leder over, som også ser punktene om prøvetiden deres. | Personen |
| Ny vurdering | Det siste arbeidsforholdet til en slettet person sluttet for et år siden: registrer den nye vurderingen. | Personen |
| Dokumenter ikke slettet | Dokumentlageret har nektet å slette dokumenter i over et døgn. | Ansatteinnstillinger |
| OTP-terskelen | Etter stillingsprosentene i dag skal virksomheten ha tjenestepensjon — med mindre den eneste det gjelder er eieren. | Ansatteinnstillinger |
| 5, 30 eller flere enn 50 ansatte | Antallet ansatte har passert en terskel som gir en plikt: varslingsrutine og verneombud fra 5, arbeidsmiljøutvalg fra 30, aktivitets- og redegjørelsesplikten over 50. | Ansatteinnstillinger |
| Fraværssøknader venter på din avgjørelse | Søknader du avgjør, venter, talt opp. | Fravær |
| Fortrolige søknader har ventet over en uke | Søknader om en fortrolig fraværstype har ventet i mer enn sju dager — også dine egne, så den eneste med `staff:personal-data` ser sin egen søknad vente på noen som kan avgjøre den. | Fravær |
| Oppfølging av sykefravær forfaller | Et oppfølgingspunkt i en åpen sykefraværssak — planen, et dialogmøte, NAVs aktivitetskrav — forfaller innen en uke eller har passert, og er verken gjort eller fraveket. En kort sak gir ingen. | Personen |
| Ferie igjen fra i fjor | I januar: noen har ferie igjen fra i fjor, og ingen overføring er registrert. Registrer overføringen, eller gjør den opp. | Personen |
| For lite ferie i hovedferien | Fra april til september: noen har færre godkjente feriedager mellom 1. juni og 30. september enn ferieordningens hovedferie (tre uker etter ferieloven). | Personen |
| Godkjente sitt eget fravær | Noen godkjente sitt eget fravær, som den eneste som kunne avgjøre det. | Personen |
| Syk i godkjent ferie | En 100 % sykmelding ligger over godkjent ferie: den ansatte kan kreve å få den utsatt. | Personen |
| Gruppen … er ikke synkronisert med katalogen | En gruppe kunne ikke holdes i katalogen — en innstilling katalogen avviste, en gruppe der med nøkkelen som Vantigo ikke får endre, eller en katalog som ikke kunne nås tre ganger på rad; gruppesiden forteller hvorfor. Én per gruppe og katalog, for HR. | Gruppen |

Et punkt forsvinner når det det ber om, er gjort. Punktet for en terskel står til HR
bekrefter terskelen — én gang, for godt — under [Terskler](#terskler) i
Ansatteinnstillinger.

## Endre innstillingene

**Ansatteinnstillinger** (`staff:manage`) har de generelle innstillingene først, så
selskapets struktur, dokumentkategoriene, tre kort for fravær — **Fraværstyper**,
**Ferieordninger** og **Egenmelding og overføring** — fire kort for dataene —
**Importer og eksporter personer**, **Lønnseksport**, **Oppbevaring** og **Terskler** — og
**Kontoer** og **Katalogprofil**. Den som ikke har `staff:manage` og åpner siden med adressen, ser ingen av dem.

### Generelt

- **Virksomhetens tidssone** — hver dato i Ansatte, også «i dag», er en kalenderdag i
  denne sonen. Den vises, men endres ikke, her: den er arbeidsområdets, og en eier
  endrer den under [Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet).
- **Prefiks for ansattnummer** og **Neste ansattnummer** — et nummer som lages
  automatisk, er prefikset og neste nummer skrevet sammen uten utfylling: `E-` og 8 blir
  `E-8`. Linjen under feltene viser neste nummer og lengden. Et ansattnummer har høyst
  16 tegn, så et prefiks og et nummer som sammen blir lengre, avvises på begge feltene
  før noe lagres. Et selskap som vil ha `EMP007`, setter prefikset til `EMP00` så lenge
  det varer.
- **Pensjonsleverandør** — leverandøren av tjenestepensjon som avtalekontrollen leser
  når en vilkårsrad ikke har noen tekst om trygd og pensjon.
- **Yrkesskadeforsikrer**.

Trykk **Lagre innstillingene**. Har noen andre lagret innstillingene siden du åpnet
siden, lastes de inn på nytt og siden sier fra; gjør endringen din på nytt. Det samme
gjelder en rad i strukturen: skjemaet lukkes over den oppdaterte tabellen.

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

### Dokumentkategorier

**Dokumentkategorier** lister det et dokument kan arkiveres under: **Nøkkel**,
**Engelsk navn** og **Norsk navn**, **Klasse**, om den er **Begrenset** — klasse E:
dokumentene er helsedokumenter — og status. De seks kategoriene merket
**Forhåndsdefinert** (arbeidsavtale, attest, sertifikat, korrespondanse, helse og annet)
endres aldri og har ingen handlinger.

Slik legger du til en egen:

1. Trykk **Legg til kategori**.
2. Skriv en **Nøkkel** — 1 til 40 tegn av a–z, 0–9 og bindestrek — **Engelsk navn** og
   **Norsk navn**.
3. Velg **Klasse**. Skjemaet sier om kategorien blir begrenset.
4. Trykk **Lagre**. Kategorien står etter de andre og tilbys når et dokument arkiveres.

Nøkkelen og klassen kan ikke endres senere: klassen avgjør når hvert dokument i
kategorien slettes. På en egen kategori endrer blyanten navnene, **Arkiver** slutter å
tilby den for nye dokumenter, og **Gjenopprett** tilbyr den igjen. En kategori det er
arkivert dokumenter under, arkiveres ikke — siden sier fra: flytt dem til en annen
kategori først.

### Fraværstyper

**Fraværstyper** viser alle typene folk kan velge, og de skjulte: navnet, **Art**,
**Hvem ser typen** — *Leder og HR*, eller *Bare HR* for en type som avslører helse- eller
familieforhold — hva den **Rapporteres i a-meldingen som**, reglene (**Må avgjøres**
eller **Melding**, **Trekkes fra feriekontoen**, **Kan være del av dagen**, **Lønnet**
eller **Ulønnet**) og om den er **Forhåndsdefinert** eller **Skjult**.

- **Ny fraværstype** legger til en egen: en **Nøkkel**, **Navn på engelsk** og **Navn på
  norsk**, **Art** og hva den **Telles i** — som ikke endres senere — **Hvem ser
  typen**, permisjonsbeskrivelsen i a-meldingen og bryterne. Trykk **Legg til**.
- Blyanten endrer en type. En forhåndsdefinert type endrer bare navnene, **Rekkefølge**,
  om den er **Skjult** og om den er **Lønnet**; de andre reglene er lovens og vises
  nedtonet. Slå **Lønnet** på for en forhåndsdefinert type når virksomheten betaler for
  fraværet utover det loven krever — flagget er til deres oversikt, og lønnseksporten
  leser det ikke.
- **Ammefri** er **Lønnet**, og forblir det: den er lønnet innenfor grensene i aml. § 12-8 (2) — inntil én time per dag i barnets første leveår ved arbeidsdag på sju timer eller mer; ammefri utover det er ulønnet etter loven med mindre annet er avtalt — registrer den med en egen type. **Svangerskapskontroll** er også **Lønnet** og forblir det: aml. § 12-1 gir fri med lønn til svangerskapskontroller. Skjemaet sier det,
  og nekter å lagre den ulønnet.
- **Skjul** slutter å tilby en type for nytt fravær; **Tilby** tar den tilbake. Fravær
  som har typen, beholder den.

En egen type som bare er for HR, åpnes ikke for kolleger mens fravær har den — skjemaet
sier fra: legg til en annen type i stedet.

### Ferieordninger

**Ferieordninger** viser selskapets ferieregler med hva hver blir — «25 virkedager =
21 dager i en femdagersuke» — og hvilken som er **Standard**, ordningen for alle
arbeidsforhold som ikke har noen.

- **Ny ferieordning**: **Navn**, **Ferierett (virkedager)**, **Ekstra fra 60 år
  (virkedager)**, **Hovedferie (virkedager)**, **Etter september (virkedager)** og
  **Mest overført (virkedager)**, hver med hva den blir i en femdagersuke, og
  **Standard**. Skjemaet holder ferielovens gulv: en ferierett på 25 til 60, en
  hovedferie på minst 18, minst 6 ved start etter september, og høyst 12 overført pluss
  feriedagene over 25.
- Blyanten endrer en ordning; det du ikke rører, beholdes. **Arkiver** slutter å tilby
  den, **Gjenopprett** tar den tilbake — standarden, eller en ordning et arbeidsforhold
  følger, arkiveres ikke, og siden sier fra.
- Et arbeidsforhold settes på en ordning under personens **Arbeidsforhold**: **Endre
  arbeidsforholdet**, **Ferieordning**. *Standard* følger den ordningen som til enhver
  tid er standard.

### Egenmelding og overføring

Velg **Regler**: *Lovens minimum (ftrl. § 8-23)* — 3 dager om gangen, 4 ganger på 12
måneder — *Utvidet (IA)* — 8 dager om gangen, 24 dager i året — eller *Egne tall*, med
**Dager per tilfelle** (3 til 16), **Tilfeller per år** (4 til 52) og **Dager per år**
(12 til 365); skjemaet avviser tall under lovens gulv. **Måneder i arbeid først** (0 til
2) er hvor lenge noen må ha jobbet før egenmelding. **Overfør ferie som ikke er tatt,
automatisk** overfører årets gjenstående ferie til neste år uten at HR gjør noe, fra året
du slår det på. Trykk **Lagre**; de andre innstillingene beholdes som de er.

### Importer og eksporter personer

Registeret reiser som én semikolonseparert CSV-fil, formen et norsk Excel åpner og
lagrer som den er. Kolonnene og reglene for hver av dem står i
[referansen](/en/reference/staff/#the-registers-import-and-export).

- **Eksporter personer** laster ned hver person — også slettede — med jobb- og
  privatopplysninger og hvor hver enkelt står i dag, som `staff-people-<i dag>.csv`.
  Fødselsnummeret og kontonummeret er aldri med, bare om et nummer er registrert. Hver
  eksport føres i historikken med navnet ditt. Et register som er større enn én fil kan
  romme, avvises, og siden sier fra: administratoren kan øke grensen.
- **Last ned mal** laster ned bare overskriftsraden, med hver kolonne du kan importere:
  de forseglede kolonnene er bare med når du også har `staff:personal-data`.

Slik importerer du:

1. Trykk **Importer personer**, deretter **Velg CSV-fil** — semikolonseparert, høyst
   5 MiB. En større fil avvises med en gang. Hver rad trenger `employeeNumber`: et
   nummer ingen har, legger til personen, et nummer en person har, endrer vedkommende.
2. Trykk **Kontroller**. Hver rad kjøres og ingenting lagres: siden viser **Rader** og
   hvor mange som **Blir opprettet**, **Blir oppdatert** og **Har feil**, og under dem
   **Problemer i filen, rad for rad**, hvert med rad, kolonne og serverens egen setning.
   Rett filen og kontroller på nytt til du er fornøyd.
3. Trykk **Importer**. Den samme filen kjøres på ordentlig; radene med feil hoppes over,
   og siden viser hva som ble **Opprettet**, **Oppdatert** og hva som **Feilet**. Hold
   vinduet åpent til den svarer — det kan ikke lukkes mens importen kjører.
4. Når rader feilet, trykker du **Last ned feilede rader**. Filen inneholder de radene
   akkurat slik du skrev dem, bak en `error`-kolonne som sier hva som er galt med hver.
   Rett dem og importer den filen som den er: importen ser bort fra `error`-kolonnen.

En fil avvises i sin helhet — ingenting i den importeres — når den er kommaseparert,
har flere rader enn én import tar, eller har en kolonne importen ikke kjenner (siden
navngir den). De **forseglede kolonnene** — `nationalIdKind`, `nationalIdCountry`,
`nationalId`, `bankAccountKind` og `bankAccount` — krever i tillegg
`staff:personal-data`: uten den avvises en fil med noen av dem i sin helhet, og siden
sier fra før du velger fil. Fjern de kolonnene, eller be noen som har tillatelsen.

Stopper importen før den svarer — forbindelsen brytes, eller serveren feiler — sier
siden fra: radene som ble importert før den stoppet, forblir importert. Trykk
**Kontroller** igjen, deretter **Importer**; radene som allerede er inne, telles som
oppdatert og endrer ingenting.

### Eksporter en måned til lønn

**Lønnseksport** gir lønnssystemet en måneds arbeidsforhold. Den vises for HR som også
har `staff:personal-data`, siden filen navngir HR-permisjon med a-meldingens
beskrivelse.

1. Velg **Måned**. Måneden før i dag er valgt når du åpner siden.
2. Trykk **Eksporter til lønn**. Filen lastes ned som `staff-payroll-<ÅÅÅÅ-MM>.csv`.

Filen har én rad per arbeidsforhold som var i måneden, med vilkårene som gjelder
månedens siste dag; kolonnene og kodene står i
[referansen](/en/reference/staff/#the-payroll-export). Den inneholder ingen
fødselsnumre og ingen kontonumre. Hver eksport føres i historikken med navnet ditt og
filens SHA-256-sjekksum, så filen lønn fikk, kan kjennes igjen senere. Kodene for de tre
ansettelsesformene som kom i 2025 (utleid, og tilkallingsvikar) er ikke kontrollert mot
Skatteetatens kodeliste — kortet sier fra: sjekk dem med lønnssystemet før du stoler på
dem. Sjekk på samme måte de sju permisjonsbeskrivelsene fraværskolonnene har, mot
kodelisten lønnssystemet ditt validerer med.

Til høyre for kolonnene om arbeidsforholdet har filen månedens fravær: ferie,
egenmelding, sykmelding, sykt barn (og dagene av det utover arbeidsgiverens ti),
permisjon og andre dager, og timene med avspasering. Etter et arbeidsforholds rad
kommer **permisjonsradene**, én per permisjon a-meldingen skal ha — en permisjon over
fjorten dager, kortere permisjoner som til sammen passerer fjorten dager på tolv
måneder, og all permittering — med permisjonens id (en kode som er den samme hver
måned), dager, beskrivelse og prosent. Ferie er ferie i filen når personen er syk i
den, til du registrerer kravet om å utsette den.

### Oppbevaring

Ansatte oppbevarer en tidligere ansatts opplysninger bare så lenge loven tillater, og
sletter resten av seg selv hver time; hva hver sletting fjerner, står i
[administrasjonsveiledningen](/nb/admin/staff/#oppbevaring). **Oppbevaring** viser:

- **Frist (dager)** — hvor lenge etter at en persons siste arbeidsforhold sluttet
  personaladministrasjonen slettes: 0 til 3650 dager, 90 om ikke annet er satt.
- **Helsedokumenter (dager)** — hvor lenge etter at siste arbeidsforhold sluttet
  helsedokumentene slettes: 30 til 3650 dager, 365 om ikke annet er satt. Like lenge
  etter at en sykefraværssak ble avsluttet, slettes sykefraværsopplysningene, uansett
  arbeidsforhold ([administrasjonsveiledningen](/nb/admin/staff/#oppbevaring)).

- **Slett svangerskap og helsetimer** — av med mindre den slås på. På blir rader med
  svangerskapskontroll, svangerskapspermisjon, fødselspermisjon og lege- eller
  tannlegetimer *Fravær* helsefristen etter slutten av året sitt, med dagene og timene
  beholdt, og historikken sier ikke lenger hvilken type de var.
- **Slett sykefraværssakens datoer og milepæler** — av med mindre den slås på. På
  fjerner slettingen av en avsluttet sak selve saken: datoene, milepælene og
  historikken; fraværet står igjen som *Fravær*-dager.

  Hver bryter er forklart under seg. Slått på sletter neste kjøring alt som allerede er
  forfalt; å slå den av gjenoppretter ingenting
  ([administrasjonsveiledningen](/nb/admin/staff/#slett-svangerskapsrader-og-sykefraværssakers-datoer)).

  Endre noe av dette og trykk **Lagre fristene**. En frist utenfor grensene avvises på
  feltet før noe lagres. Har noen andre lagret innstillingene siden du åpnet siden,
  lastes de inn på nytt og siden sier fra.
- **Ventende slettinger** og **Feilede slettinger** — filer slettingene og
  dokumentslettingene har satt i kø for fjerning fra objektlageret, og de lageret har
  nektet minst én gang. Så lenge noen feiler, ber kortet deg sjekke at objektlageret er
  satt opp og kan nås; de prøves igjen til det bekrefter.
- **Eldste ventende siden** og **Sist slettet** — når den eldste filen ble satt i kø, og
  når en sletting sist ble kjørt (**Aldri** når ingen er det).

**Kjør sletting nå** kjører timeslettingen nå, etter å ha spurt: den sletter for godt
det som er forfalt, og kan ikke angres. Personer med slettesperre hoppes over. Kortet
sier så hva som ble slettet — personaladministrasjon, helsedokumenter, lønn og
avtale, sykefraværssaker, dager med sykt barn, tilbaketrekkinger av egenmelding og
svangerskap og helsetimer, hver med et antall; helsetypene bare om du også har
rettigheten for personopplysninger — og hvor mange filer som ble slettet fra lageret.
Slettingene stopper etter 30 sekunder: det som ikke er ferdig, gjør
oppbevaringsarbeideren ferdig neste gang den ser etter. Kjører arbeideren allerede, sier
siden fra: prøv igjen om et øyeblikk.

### Terskler

**Terskler** lister de fire tersklene som gir arbeidsgiveren en plikt — **5 eller flere
ansatte**, **30 eller flere ansatte**, **Flere enn 50 ansatte** og **Tjenestepensjon
(OTP)** — hver med plikten på én linje. Så lenge en er nådd og ikke bekreftet, viser
dashbordet et punkt for den. Selve antallet ansatte står på dashbordet, ikke her.

Når plikten er ivaretatt — eller ikke gjelder — trykker du **Bekreft** på raden og
bekrefter. En bekreftelse er **permanent**: den kan ikke angres, og terskelens punkt
kommer aldri tilbake, uansett hva antallet ansatte gjør senere. Raden sier da
**Bekreftet** med datoen. En terskel kan bekreftes før den er nådd.

### Kontoer

**Kontoer** er hvordan Ansatte gir folk en bruker å logge inn med — slått på for hver
person på fanen [Konto](#konto). Kortet viser hver identitetsleverandør en installert
modul tilbyr, med status — *Microsoft Entra ID: tilkoblet.*, *Microsoft Entra ID: ikke
tilkoblet ennå.* eller *Microsoft Entra ID: feiler — Ansatte skriver ingenting til
katalogen utenom deaktiveringer før det er rettet.*, med årsaken i en setning; uten noen,
*Ingen identitetsleverandør tilbys: ingen aktivert modul gir en.* Det sier også om en
eier har angitt rollene Ansatte kan gi. Før en eier har gjort det, advarer kortet *Ingen eier har angitt
rollene Ansatte kan gi, så ingen lokal konto kan aktiveres*, og **Åpne Roller og
tilgang** går til identitets side **Roller og tilgang**, der en eier angir dem under
**Roller Ansatte kan gi**.

- **Standard kontotype** — kontotypen en ny person får: **Lokal**, **Ekstern
  identitetsleverandør** eller, når Entra-modulen er installert, **Microsoft Entra ID**.
  Personer som allerede er registrert, beholder sin; endre dem én og én på fanen Konto.
  Ved oppgraderingen som innførte typene, ble den satt ut fra installasjonen: Lokal,
  eller Ekstern identitetsleverandør der single sign-on var på.
- **Forbered kontoen (dager før)** — hvor mange dager før kontoens dag Ansatte
  oppretter brukeren, deaktivert: 0 til 90, 14 om ikke endret.
- **Slett en ekstern konto (dager etter slutt)** — hvor mange dager etter at siste
  arbeidsforhold sluttet Ansatte sletter personens konto i Entra, når den er deaktivert
  og ingen slettesperre er på personen: 1 til 3650, eller tom for aldri. Entra holder en
  slettet konto gjenopprettbar i tretti dager. Like mange dager etter at en
  [gruppe](#grupper) er arkivert, eller Microsoft Entra ID er slått av for den, slettes
  gruppen i Entra — en sikkerhetsgruppe tømmes og beholdes i stedet, og en Vantigo ikke
  laget, blir stående, slik [Grupper](#grupper) sier; står feltet tomt, omdøpes den
  «… (archived)» og beholdes.

Trykk **Lagre kontoinnstillingene**. Et tall utenfor grensene avvises på feltet før noe
lagres; de andre innstillingene lagres som de var.

**Kjør kontoer nå** kjører én runde av kontoarbeideren med en gang, for hver konto som
er forfalt, slik arbeideren ville gjort. En runde kan ta minutter når identitet eller
e-postserveren er treg. Kortet sier så, med ett tall for hvert, hvor mange kontoer den behandlet, hvor mange som
ble ferdige, hvor mange som mislyktes og prøves igjen av arbeideren, og hvor mange som
er igjen til arbeiderens neste runde.

### Katalogprofil

**Katalogprofil** (`staff:manage`) velger hvilke felt Ansatte skriver på en persons konto
i selskapets katalog — Microsoft Entra ID — for hver person av den typen. Hvert felt har
en bryter:

- På som standard: **Ansettelsesdato**, **Sluttdato**, **Ansattype**, **Stillingstittel**,
  **Avdeling**, **Selskapsnavn** (den juridiske enheten), **Kostnadssted og divisjon**,
  **Kontorsted**, **Foretrukket språk** og **Leder**.
- Av som standard: **Arbeidsadresse** (stedets gateadresse, postnummer, poststed og
  land) og **Telefoner** (jobbtelefonen og mobilen).

Navnene, ansattnummeret, brukerhovednavnet og bruksstedet skrives alltid, slik kortet
sier; de har ingen bryter, og hver persons fane Konto viser dem grå. Private
kontaktopplysninger skrives aldri.

1. Slå bryterne på eller av.
2. Trykk **Lagre katalogprofilen**. En melding sier *Katalogprofilen er lagret*; har noen
   andre lagret innstillingene siden du åpnet dem, lastes de inn på nytt i stedet, og
   meldingen sier *Noen andre lagret dette først; det er lastet inn på nytt*.

Et felt du slår på, skrives ved hver persons neste oppdatering. Et felt du slår av,
skrives ikke lenger: Ansatte slutter å sende det, og katalogen beholder verdien den sist
hadde. Hver persons fane Konto viser resultatet som katalogprofilen deres.
