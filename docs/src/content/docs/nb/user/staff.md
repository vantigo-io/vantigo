---
title: Ansatte
description: Personregisteret — Personer, personsiden med dokumentene, Ny person, Min side og Ansatteinnstillinger med selskapets struktur og dokumentkategoriene.
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
| **Ansatteinnstillinger** | `staff:manage` |

Hva et skjermbilde tilbyr, følger det serveren sier at du kan gjøre: knappen **Ny
person**, **Status**-filteret og statuskolonnen finnes bare for `staff:manage`. Hele
tabellen står under [rettighetene](/en/reference/staff/#permissions) i referansen.

## Finn noen under Personer

**Personer** (i sidemenyen: **Ansattoversikt**) lister selskapets personer etter navn, med ansattnummer, tittel, avdeling,
leder og e-post på jobb. Tittelen, avdelingen og lederen er de som står i vilkårene som
gjelder i dag; en strek betyr at det ikke finnes noen.

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
arbeidsdag, eller **Ikke ansatt**. «I dag» er dagen i virksomhetens tidssone (se
[Endre innstillingene](#endre-innstillingene)), så listen skifter ved midnatt der, ikke
i nettleseren din.

Trykk på et navn for å åpne [personsiden](#personsiden).

## Personsiden

En persons side har fire faner — **Profil**, **Arbeidsforhold**, **Historikk** og **Dokumenter** — og
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
- **Organisasjon** — **Ledere** oppover fra personens leder i dag, og de **Direkte
  underordnede**. Trykk på et navn for å åpne siden deres.

En leder ser jobbgruppen, rollene og organisasjonen til dem de leder; en kollega det
samme for alle som er aktive i dag.

#### Endre opplysningene (HR)

1. Trykk **Endre opplysninger** i gruppen Jobb. Skjemaet har jobbgruppen, den private
   gruppen og HRs notater. Ansattnummeret vises, men endres aldri.
2. Endre det du trenger, og trykk **Lagre**. En avvisning havner på feltet den gjelder
   — for eksempel en e-post på jobb som en annen person har.

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

Med `staff:manage` tilbyr kortet og radene:

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
  **Virksomhet**, typen arbeidsforhold og **Avtale signert**. Arbeidsforholdsnummeret
  endres aldri.
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
**Fjern** på et helsedokument — heller ikke på sitt eget.

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

**Legg det til senere** lukker skjemaet med personen lagret og åpner siden deres.

## Min side

**Min side** (`staff:access`) er din egen oppføring i Ansatte, funnet gjennom brukeren
HR koblet den til, og viser det du ser av deg selv — uansett hvilke andre rettigheter du
har. Den har de samme fire fanene som en persons side:

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

Har HR ikke koblet brukeren din til en oppføring ennå, sier Min side *Du har ingen
oppføring i Ansatte ennå* og forklarer hvordan den kommer på plass; spør HR hvis du
mener din mangler.

Min side er laget for mobilen først: på en smal skjerm står alt i én kolonne, og
skjemaet fyller hele skjermen.

## Dashbordet

**Hjem**, dashbordet, har et **Ansatte**-kort for den som har `staff:view` eller
`staff:manage`: hvor mange som er ansatt i dag, og under det årsverkene i dag, hvor
mange som begynner og hvor mange som slutter innen 30 dager, og — når det finnes noen —
oppføringene uten noe arbeidsforhold. Trykk på kortet for å åpne Ansatte. Med bare
`staff:access` har du Ansatte-appen under **Moduler** og ikke noe kort.

Punktene fra Ansatte kommer i **Trenger oppfølging** sammen med de andre appenes, hvert
formulert på ditt språk. De fleste er HRs (`staff:manage`); en leder ser punktet om
prøvetid for sine egne folk, og den nye vurderingen er `staff:personal-data` sin:

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

Et punkt forsvinner når det det ber om, er gjort. Punktet for en terskel står til HR
bekrefter terskelen — én gang, for godt; hvordan står i
[administrasjonsveiledningen](/nb/admin/staff/#tersklene).

## Endre innstillingene

**Ansatteinnstillinger** (`staff:manage`) har de generelle innstillingene først, så
selskapets struktur, og til sist dokumentkategoriene.

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
