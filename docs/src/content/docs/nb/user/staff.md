---
title: Ansatte
description: Personregisteret — Personer, personsiden med dokumentene, slettesperren, en slettet person og nyansettelsen av den, Ny person, Min side, fraværskalenderen og godkjenningene, fravær og feriekontoen, og Ansatteinnstillinger med selskapets struktur, dokumentkategoriene, import og eksport av registeret, lønnseksporten, oppbevaring og tersklene.
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

Ansatte-flisen vises for alle med `staff:access`; appen åpner på Personer for dem som
ser den, og på Min side for alle andre. Sidemenyen viser områdene rettighetene dine gir
deg:

| Område i sidemenyen | Hvem ser det |
| --- | --- |
| **Min side** | alle med `staff:access` |
| **Ansattoversikt** (skjermbildet Personer) | `staff:view`, eller `staff:manage` — en HR-rolle uten `staff:view` ser også listen og strukturen |
| **Fravær** (kalenderen og godkjenningene) | `staff:view`, `staff:approve`, `staff:manage` eller `staff:personal-data`; en leder uten disse åpner den fra lenken på dashbordet, og avgjør under [Mitt team](#mitt-team) |
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

En persons side har fem faner — **Profil**, **Arbeidsforhold**, **Fravær**, **Historikk** og **Dokumenter** — og
fanen du står på, ligger i sidens adresse. Siden viser det du kan se av personen og
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
- **Selvbetjening** — brukeren personen logger inn med, hvis HR har koblet en.
- **Konto** — om Ansatte gir personen en bruker å logge inn med, fra hvilken dag, og på
  jobb-e-posten eller den private e-posten. Kontoen klargjøres, deaktivert, et antall
  dager før den dagen og aktiveres på den; uten en dag HR har satt selv er det første
  dag i personens tidligste arbeidsforhold som ikke har sluttet. Personen, HR og
  `staff:personal-data` ser den; bare HR (`staff:manage`) endrer den. En bruker Ansatte forvalter slik, kobles ikke til
  eller fra under **Selvbetjening**.
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

## Min side

**Min side** (`staff:access`) er din egen oppføring i Ansatte, funnet gjennom brukeren
HR koblet den til, og viser det du ser av deg selv — uansett hvilke andre rettigheter du
har. Den har de fire fanene en persons side har, og så **Fravær** og, når du er leder for
noen, **Mitt team**:

- **Profil** — jobbopplysningene, de private opplysningene, fødselsnummeret som
  **Registrert** og de fire siste sifrene i bankkontoen, de pårørende, rollene og hvor
  du hører hjemme i organisasjonen. Trykk **Endre opplysningene mine** for å endre
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
  dine registrerer ingenting.
- **Fravær** — feriekontoen din, egenmeldingsstatusen, omsorgsdagene og fraværene
  dine, med **Be om fravær** (se [Be om fravær](#be-om-fravær-fra-mobilen) nedenfor).
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

Et punkt forsvinner når det det ber om, er gjort. Punktet for en terskel står til HR
bekrefter terskelen — én gang, for godt — under [Terskler](#terskler) i
Ansatteinnstillinger.

## Endre innstillingene

**Ansatteinnstillinger** (`staff:manage`) har de generelle innstillingene først, så
selskapets struktur, dokumentkategoriene, tre kort for fravær — **Fraværstyper**,
**Ferieordninger** og **Egenmelding og overføring** — og fire kort for dataene:
**Importer og eksporter personer**, **Lønnseksport**, **Oppbevaring** og **Terskler**. Den
som ikke har `staff:manage` og åpner siden med adressen, ser ingen av dem.

### Generelt

- **Virksomhetens tidssone** — hver dato i Ansatte, også «i dag», er en kalenderdag i
  denne sonen.
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
- **Ammefri** er **Lønnet**, og forblir det: loven (aml. § 12-8 (2)) gir lønn for inntil
  én time per dag i barnets første leveår ved arbeidsdag på sju timer eller mer.
  Skjemaet sier det, og nekter å lagre den ulønnet. Lønn utover det — mer enn en time,
  eller etter det første året — er virksomhetens: bruk en egen type, eller fraværets
  notat.
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
svangerskap og helsetimer, hver med et antall — og hvor mange filer som ble slettet fra
lageret.
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
