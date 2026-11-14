---
title: Regnskap
description: Regnskap-appen — å sette opp regnskapet, kontoplanen med grupperingskoder og systemkontoer, mva-kodene og satsene deres, å bokføre en postering netto eller brutto med mva-linjene, å kopiere et bilag, å legge ved filer, å tilbakeføre et bilag, å sperre en periode og flytte sperren tilbake, inngående balanse og de åpne fakturaene fra Fakturaer, hvordan fakturaer, innbetalinger, purrekostnader og avskrivninger bokføres av seg selv, leverandører og oppslaget i Brønnøysundregisteret, å føre, bokføre og betale en leverandørfaktura, annen valuta, omvendt avgiftsplikt og innførsel av varer, kreditnotaer, Tilbakefør og kopier, hvordan utgifter kommer inn fra Utgifter, posteringsreglene for salg og utgiftskategorier, Trenger oppmerksomhet med Prøv igjen, beløpet i NOK og Lag leverandørfaktura, åpne poster og å registrere en innbetaling, utbetaling eller tilbakebetaling, mva-terminen — å kontrollere tallene, lukke den, laste ned mva-meldingen, levere den hos Skatteetaten og merke den som levert, åpne den igjen, rette etter levering og betale mvaen — banken med bankkontoer, å importere en kontoutskrift i camt.053 eller CSV med oppsettet, bankintegrasjonen som kilde, å avstemme med hvert forslag, utbetalinger fra Vipps, å betale mvaen fra banken, bankregler, hvorfor innbetalinger fra kunder registreres i Fakturaer og å sperre en måned med linjer som ikke er avstemt — rapportene og CSV-filene deres, SAF-T-filen en kontroll ber om og grupperingskodene den trenger, å komme fra et annet system med SAF-T-filen derfra, og å invitere en regnskapsfører.
sidebar:
  order: 67
sources:
  - apps/accounting/frontend
  - apps/host/frontend/src/routes/accounting
---

Regnskap-appen fører selskapets regnskap slik bokføringsloven krever. På engelsk heter
appen **Accounting**. Hver føring i regnskapet er et **bilag**: en nummerert og datert
oppføring med sine **posteringer** — beløpene på hver konto, debet eller kredit, som til
sammen blir null. Et bokført bilag endres eller slettes aldri; en feil rettes ved å
**tilbakeføre** det. **Kontoplanen** viser kontoene regnskapet bokfører på. **Åpne
poster** er det hver kunde skylder, dokument for dokument. **Sperredatoen** lukker
månedene bak seg, og **rapportene** er spesifikasjonene loven krever at et selskap kan
legge fram.

Regnskap krever at [Fakturaer](/nb/user/invoices/) er slått på. Du bokfører posteringer for
hånd og registrerer inngående balanse; det Fakturaer utsteder og får betalt — fakturaer,
kreditnotaer, innbetalinger, purrekostnader og avskrivninger — bokføres av seg selv
([Slik kommer salget inn](#slik-kommer-salget-inn-fra-fakturaer)), og det samme gjør det
[Utgifter](/nb/user/expenses/) godkjenner og refunderer
([Slik kommer utgiftene inn](#slik-kommer-utgiftene-inn-fra-utgifter)). Det leverandørene
fakturerer, fører du som [leverandørfakturaer](#leverandørfakturaer) og registrerer betalingene
av. Hver [mva-termin](#mva-terminen) kontrolleres og lukkes her, og mva-meldingen leveres hos
Skatteetaten. [Banken](#banken) avstemmes mot kontoutskriftene, og
[SAF-T-filen](#filen-en-kontroll-ber-om-saf-t) er det en kontroll ber om og veien inn fra et annet system.
Reglene bak hvert skjermbilde — hva som avvises og hvorfor, statuskodene
og tillatelsen hver forespørsel sjekkes mot — står i
[referansen for Regnskap-modulen](/en/reference/accounting/) (på engelsk).

## Sidemenyen

| Menypunkt | Hva det er |
| --- | --- |
| **Oversikt** | Om regnskapet er satt opp, bilagene i regnskapsåret, de åpne kundepostene, sperredatoen, det som trenger oppmerksomhet og snarveier. |
| **Ny postering** | Skjemaet for en postering. Vises for dem som kan bokføre bilag. |
| **Bilag** | Alle bilag, nyeste først. |
| **Trenger oppmerksomhet** | Det regnskapet ikke kunne bokføre, med årsaken og hva du gjør: **Prøv igjen**, beløpet i NOK, **Lag leverandørfaktura**; og det som er bokført, men som du bør vite om. |
| **Leverandørfakturaer** | Fakturaer og kreditnotaer fra leverandører: utkast, bokført, betalt, tilbakeført. |
| **Leverandører** | Selskapene og personene dere kjøper fra. |
| **Åpne poster** | Det kundene skylder og det dere skylder leverandørene, dokument for dokument. |
| **Bank** | Bankkontoene, kontoutskriftene importert eller lest fra banken, og hver linje avstemt mot regnskapet. |
| **Mva** | Mva-terminene: hver termins mva-melding, lukkingen, XML-filen og leveringsmerket. Vises ikke når selskapet ikke er mva-registrert. |
| **Rapporter** | Bokføringsspesifikasjon, kontospesifikasjon, kunder, leverandører, saldobalanse, resultatregnskap, balanse og merverdiavgiftsspesifikasjon. |
| **SAF-T** | Filen Skatteetaten ber om ved kontroll, og alle eksporter som er laget. Vises for dem som kan eksportere regnskapet. |
| **Kontoplan** | Kontoene, grupperingskodene deres og systemkontoene. |
| **Mva-koder** | Standard mva-kodene, kontoene deres og satsene. |
| **Posteringsregler** | Hvilken inntektskonto hver salgslinje føres på, og hvilken kostnadskonto hver utgiftskategori føres på. |
| **Innstillinger** | Regnskapsinnstillingene og sperredatoen. |

## Å sette opp regnskapet

Til regnskapet er satt opp, viser appen bare veien dit: **Oversikt** sier *Regnskapet er
ikke satt opp*, og alle andre sider sier det samme. En med **Forvalte regnskapet**
(`accounting:close`) klikker **Sett opp regnskapet** og velger, én gang:

1. **Regnskapet starter** — første dag regnskapet gjelder fra. Ingenting datert før denne
   dagen bokføres. Et selskap som har ført regnskapet et annet sted til nå, velger første
   dag i en måned etter siste avsluttede periode der. Tidligst er 1. januar 2022:
   mva-satsene regnskapet starter med, er kjent fra da.
2. **Regnskapsåret starter i** — januar for de fleste.
3. **Mva-termin** — *Annenhver måned* for de fleste, *Årlig* for et selskap skattekontoret
   gir lov til det, *Ikke mva-registrert* for et selskap utenfor Merverdiavgiftsregisteret.
4. **Navn i kontoplanen** — om kontoplanen og mva-kodene får norske eller engelske navn. De
   kan få nye navn én og én senere.

**Sett opp regnskapet** lager standardkontoplanen, Skatteetatens standard mva-koder med
satsene — 25, 15, 12 og 11,11 %, uendret siden 2022 — og systemkontoene, og åpner **Inngående balanse**. De fire valgene kan
ikke endres etterpå; **Innstillinger** viser dem.

Et selskap som kommer fra et annet regnskapssystem, setter i stedet opp regnskapet fra
SAF-T-filen fra det systemet, på samme side: se [Kommer du fra et annet system?](#kommer-du-fra-et-annet-system).

## Kommer du fra et annet system?

Et selskap som har ført regnskapet i Fiken, Tripletex, Visma eller et annet system, tar med
seg kontoplanen, leverandørene og saldoene med **SAF-T Financial**-filen det systemet
eksporterer. Gjør det **før** regnskapet er satt opp: det er den andre måten å sette det
opp på, og det krever **Forvalte regnskapet**.

1. Avslutt den siste perioden du fører i det gamle systemet, og eksporter SAF-T
   Financial-filen for den — versjon 1.30 eller 1.40, for regnskapsåret eller frem til
   siste dag i en måned. Regnskapet her starter dagen etter at filens periode slutter.
2. Åpne **Regnskap**, **Sett opp regnskapet** i Vantigo, velg filen under **Kommer du fra
   et annet system?** og klikk **Les filen**. Ingenting er skrevet ennå: du ser en
   forhåndsvisning.
3. Les forhåndsvisningen:
   - **Kontoplanen** — hver konto i filen med nummeret den får her, arten, grupperingskoden
     og utgående saldo. Et nummer som ikke passer kontoplanen her (4 til 6 sifre som
     begynner med 1 til 8), vises med løsningen: tatt med uten punktum eller bindestrek,
     utelatt når kontoen ikke har saldo, eller *Trenger et nummer* når den har det.
   - **Kontoer lagt til fra standard kontoplan** — kontoene regnskapet trenger og filen ikke
     har, som motkontoen for inngående balanse eller de enkelte mva-kontoene.
   - **Systemroller** — hvilken konto som er kundefordringer, leverandørgjeld, bank og så
     videre. Kundenes og leverandørenes konto i filen avgjør de to første.
   - **Mva-koder** — filens koder og standardkoden hver tilsvarer. Regnskapet her bruker
     Skatteetatens standardkoder.
   - **Leverandører** — opprettes fra filen med organisasjonsnummer, adresse og
     bankkonto. Et nummer som ikke er gyldig, utelates, og det står hvorfor.
   - **Kunder** — funnet blant kundene i **Kunder** på organisasjonsnummer når filen leses.
     Regnskap oppretter ikke kunder: en kunde med saldo som Kunder ikke har, eller har to
     ganger, må opprettes eller finnes der først; søk den så opp i feltet under listen, på
     navn eller nummer, og velg den. Listen viser kunden den er koblet til med
     organisasjonsnummeret: sjekk at det er riktig kunde. **Søk i Kunder på nytt** slår opp
     organisasjonsnumrene igjen, for kunder som er opprettet siden.
   - **Åpningsbalansen** — bilaget regnskapet starter med: hver balansekontos utgående
     saldo og én åpen post per kunde og leverandør med saldo. Når regnskapet starter et
     regnskapsår, går filens resultat til motkontoen for inngående balanse i egenkapitalen.
     Når det starter midt i et — en fil til utgangen av juni, for eksempel — åpner
     resultatkontoene med saldoene så langt i året, så årets resultat blir hele årets. Mva-kontoenes saldoer føres samlet på kontoen for
     mva-oppgjør (2740) — *Skyldig mva ved start* — fordi det er det som gjenstår å betale
     eller få tilbake.
4. Sjekk **Regnskapsåret starter i**. En fil som oppgir perioden med månedsnumre i stedet
   for datoer (noen systemer gjør det), trenger den for å vite dagene; forhåndsvisningen
   sier fra.
5. Gjelder filen et annet selskap enn det i innstillingene i Fakturaer, sier
   forhåndsvisningen det i rødt: kryss av for at regnskapet likevel skal starte fra den,
   eller last opp riktig fil.
6. Velg **Mva-termin** og språket for navnene, og klikk **Sett opp regnskapet fra filen**.
   Alt skrives på én gang; det kan ikke angres.

Det avvises mens en konto trenger et nummer, en kunde med saldo ikke finnes i Kunder, eller
kundenes eller leverandørenes saldoer ikke stemmer med kontoens saldo i filen (differansen
vises): rett det i det gamle systemet og eksporter på nytt.

**Transaksjonene importeres ikke.** Bilagene fra det gamle systemet blir i filen derfra:
loven krever at du oppbevarer regnskapet for tidligere år i fem år, og filen er den
dokumentasjonen. Vantigo oppbevarer den opplastede filen sammen med regnskapet —
**Innstillinger** viser den under *Satt opp fra en SAF-T-fil*, til nedlasting — men ta også
vare på din egen kopi fra det gamle systemet. En
kundes åpne saldo kommer som én post per kunde: når den betales, registrerer du
innbetalingen under **Åpne poster**, eller deler den opp der.

## Kontoplanen

**Kontoplan** viser alle kontoer etter nummer med type (eiendel, gjeld, egenkapital,
inntekt eller kostnad), standard mva-kode, grupperingskode og, for kontoene regnskapet
bokfører på av seg selv, systemrollen. Søk på nummer eller navn. Klikk på en konto for å
åpne den.

Standardkontoplanen har rundt 130 kontoer i nummereringen norske regnskapsførere kjenner:
1500 kundefordringer, 1920 bank, 2400 leverandørgjeld, 2700–2740 merverdiavgift, 3000 salg,
4005 varekostnad, 5000 lønn, 6000–7000 andre kostnader, 8000 finans. Navnene og
grupperingskodene kommer fra Skatteetatens liste for næringsspesifikasjonen.

- **Ny konto** legger til en: et nummer på 4 til 6 sifre som ingen annen konto har, et
  navn, en type, en valgfri standard mva-kode og en grupperingskode. Typen følger
  nummerets klasse, og skjemaet fyller den inn mens du skriver: 1 eiendel, 20
  egenkapital, 21–29 gjeld, 3 inntekt, 4–7 kostnad, og 8 inntekt eller kostnad — velg
  hvilken. Et nummer begynner med 1 til 8; en annen type enn klassens avvises.
- **Grupperingskode** er koden SAF-T rapporterer kontoen under, valgt fra Skatteetatens
  liste for året. En konto uten kode rapporteres som *NA*.
- **Navnet** på en konto kan alltid endres. **Nummeret** og **typen** ligger fast når den
  har en postering, og en slik konto kan ikke slettes — gjør den inaktiv i stedet. En
  inaktiv konto tar ikke imot nye posteringer.
- **Systemkontoer** er kontoene regnskapet bokfører på av seg selv: kundefordringer,
  leverandørgjeld, bank, oppgjør merverdiavgift, øreavrunding, tap på fordringer, skyldig
  til ansatte, inngående balanse, årets resultat, Vipps-oppgjør, gebyrer og renter,
  valutagevinst og -tap og betalingsgebyrer. **Gjør den til systemkonto** på en konto
  flytter en rolle dit; rollen forlater kontoen som hadde den. En rolle flyttes bare til en
  konto av sin type — banken til en eiendelskonto, oppgjør merverdiavgift til gjeld,
  øreavrunding til en kostnadskonto — og kundefordringer og leverandørgjeld blir på
  kontoen sin når den har posteringer, siden det hver kunde og leverandør skylder, er de
  posteringene. En rolle fjernes aldri, så en konto med en rolle kan ikke slettes eller
  gjøres inaktiv før rollen er flyttet.

Å endre kontoplanen krever **Forvalte regnskapet**.

## Mva-koder

**Mva-koder** viser Skatteetatens standardkoder med retning (utgående, inngående eller
ingen), satsen som gjelder i dag, mva-kontoen de bokfører på og om mva-meldingen tar dem
med — kodene 0, 7, 20, 21 og 22 rapporteres ikke. Koder med omvendt avgiftsplikt (tjenester
kjøpt fra utlandet, innførsel) bokfører på to kontoer: den fradragsberettigede og den
beregnede merverdiavgiften.

Klikk på en kode for å endre navnet, mva-kontoen (og, ved omvendt avgiftsplikt,
motkontoen) eller om den tilbys. Når noe er bokført med en kode, kan mva-kontoen og
motkontoen ikke lenger endres — mva-meldingen leser fradraget fra der kodens mva ligger —
men navnet og om den tilbys, kan fortsatt endres. **Legg til sats** starter en ny sats fra en
dato; den gjeldende satsen slutter dagen før. Et bilag får satsen som gjelder på bilagets
egen dato.

## Å bokføre en postering

**Ny postering** er skjemaet for alt regnskapet ikke bokfører av seg selv: lønn fra et
lønnssystem, husleie, et lån, en korrigering.

1. Velg **Bilagsdato** og skriv en **Beskrivelse**.
2. Velg **Konto** på hver linje og skriv beløpet under **Debet** eller **Kredit**. Kontoens
   standard mva-kode fylles inn, og følger kontoen når du velger en annen — til du velger
   en mva-kode selv, som da blir stående.
3. En linje på kundefordringer oppgir **Kunde**. En linje med **Prosjekt** vises i
   prosjektets tall når Prosjekter er slått på.
4. Velg **Netto** eller **Brutto**. Med *Netto* er beløpet på en linje uten merverdiavgift,
   og avgiften legges til på en egen linje; med *Brutto* er avgiften med i beløpet, og
   linjen deles i netto og merverdiavgift. En kode med omvendt avgiftsplikt (tjenester
   kjøpt fra utlandet, innførsel) føres alltid netto: fakturaen har ingen norsk
   merverdiavgift, så *Brutto* med en slik kode avvises.
5. **Posteringer** viser, mens du skriver, nøyaktig hva som blir bokført: hver linje,
   mva-linjene serveren regner ut, og om bilaget går i balanse.
6. Legg ved kvitteringen eller dokumentet under **Vedlegg**, og klikk **Bokfør**. En fil
   over 20 MB, eller enhver fil når installasjonen ikke har fillagring, påpekes før du
   bokfører, og **Bokfør** venter til den er fjernet.

Bilaget nummereres uten hull innenfor regnskapsåret (2026-000123) og åpnes. Det finnes
ikke noe utkast: bokført er bokført. Et bilag datert på eller før sperredatoen, eller før
regnskapet starter, avvises. Kan en fil ikke lagres etter at bilaget er bokført, åpnes
bilaget likevel med en melding som oppgir filen: legg den ved igjen der med **Legg ved en
fil**.

Merverdiavgiften regnes ut linje for linje og avrundes til nærmeste øre, med halve ører
bort fra null: 1 000,00 netto med 25 % gir 250,00; 1 000,00 brutto med 25 % gir 800,00 og
200,00.

**Kopier fra et bilag.** For posteringene som kommer igjen hver måned åpner du et bilag du
har bokført for hånd og klikker **Kopier**. Skjemaet åpnes med linjene, ført netto —
mva-linjene regnes ut på nytt — så du endrer datoen og beløpene og bokfører.

Å bokføre krever **Bokføre bilag** (`accounting:post`).

## Bilag

**Bilag** viser alle bilag, nyeste først. Filtrer på periode og type, eller søk etter en
tekst eller et nummer. Klikk på et bilag for å åpne det.

Bilagssiden viser datoene — **Bilagsdato** det bokføres på og **Dokumentdato** for det det
gjelder — typen, når det ble bokført, hver postering med konto, tekst, mva-kode, kunde og
prosjekt, debet og kredit, og vedleggene. **Legg ved en fil** legger til et vedlegg; et
vedlegg fjernes aldri. Et bilag systemet har bokført for noe en annen app har registrert,
sier det, oppgir kilden og regelen som laget det med versjonen, og lenker til fakturaen i
Fakturaer det hører til.

## Å tilbakeføre et bilag

En feil redigeres aldri bort. Klikk **Tilbakefør** på bilaget, oppgi dato og begrunnelse,
og klikk **Tilbakefør**: et nytt bilag med alle fortegn snudd bokføres og viser til det
opprinnelige, og begge viser koblingen — *Tilbakefører* og *Tilbakeført av*. Et bilag
tilbakeføres bare én gang, på de samme kontoene, også en som er gjort inaktiv siden.
Bokfør så det riktige, gjerne med **Kopier**.

Et bilag systemet har bokført kan ikke tilbakeføres her: kilden tilbakefører det — en
kreditnota i Fakturaer, en betaling som fjernes der. Ellers ville regnskapet og den andre
appen være uenige.

## Å sperre en periode

**Innstillinger** viser **Sperredato**: alle bilag på eller før den ligger fast. Velg
måneden under **Sperr til og med slutten av** og klikk **Sperr**. Sperr en måned når
bilagene er på plass og kontrollert — senest når mva-terminen er rapportert. Sperren går
høyst til slutten av inneværende måned.

En måned som er sperret for tidlig kan åpnes igjen: velg en tidligere måned, oppgi
**Begrunnelse**, og klikk **Flytt sperren tilbake** (eller **Opphev sperren**). Hver
flytting lagres i sperrens historikk med hvem som gjorde det og hvorfor, fordi loven krever
at det å åpne lukkede perioder kan gjøres rede for.

Å lukke en [mva-termin](#å-lukke-terminen) flytter sperren til terminens siste dag, og å
åpne en igjen flytter den tilbake. Sperren flyttes aldri tilbake bak en lukket mva-termin:
åpne terminen igjen i stedet ([Å åpne en termin igjen](#å-åpne-en-termin-igjen)), eller, når
den er levert hos Skatteetaten, rett den i den åpne terminen.

Mens en måned er sperret, avvises en postering datert i den. Det systemet bokfører av seg
selv for en sperret dag, bokføres på første åpne dag og beholder dokumentdatoen.

En bankkonto med linjer i måneden som ikke er avstemt, eller der saldoen etter
kontoutskriften er en annen enn hovedbokens, nevnes før sperren flyttes, og du bekrefter med
**Sperr likevel** ([Å sperre en måned](#å-sperre-en-måned)).

Å flytte sperren krever **Forvalte regnskapet**.

## Inngående balanse

**Inngående balanse**, som åpnes etter oppsettet og fra oversikten, registrerer saldoene
den dagen regnskapet starter, som ett bilag datert den dagen.

1. Skriv inn hver saldo: kontoen og beløpet under **Debet** eller **Kredit** — banken,
   aksjekapitalen, lån, anleggsmidler.
2. Registrer det kundene skylder **med én linje per åpent dokument** på kundefordringer, med
   kunde, dokumentnummer, dokumentdato og forfall, slik at hvert dokument blir en åpen post.
3. Når selskapet allerede fakturerer i Vantigo, tilbyr **Fakturaer har … åpne fakturaer ved
   regnskapets start** dem: **Legg til de åpne fakturaene** legger til en linje per faktura
   med kunde, nummer, datoer og beløpet som var åpent dagen før regnskapet starter.
4. Det linjene ikke går i balanse med, føres på motkontoen for inngående balanse; skjemaet
   viser hvor mye. Klikk **Bokfør inngående balanse**.

En faktura lagt til fra Fakturaer beholder kunden sin og beløpet som var åpent den dagen;
en linje som oppgir den med en annen kunde eller et annet beløp, avvises.

**Mva ved regnskapets start.** Mva som var skyldig eller til gode før regnskapet starter —
den siste terminen i det gamle regnskapet, ennå ikke betalt — føres på **2740**,
oppgjørskontoen for mva, ikke på en mva-konto som 2700 eller 2710: en linje for inngående
balanse på en mva-konto avvises. Før mva å betale som kredit på 2740 og mva til gode som
debet; betalingen til eller fra Skatteetaten gjør den så opp ([Å betale mvaen](#å-betale-mvaen)).

Inngående balanse kan tilbakeføres fra bilaget sitt og registreres på nytt til første
måned er sperret; etter det ligger den fast, også om sperren senere flyttes tilbake eller
oppheves. Å registrere den, og å tilbakeføre den, krever **Forvalte regnskapet**: den som
bare kan bokføre bilag, ser ingen **Tilbakefør** på bilaget for inngående balanse.

## Slik kommer salget inn fra Fakturaer

Når regnskapet er satt opp, bokføres alt Fakturaer utsteder og får betalt av seg selv, ett
bilag for hvert, som regel i løpet av sekunder og senest innen fem minutter. Ingen skriver
dem inn, og en faktura holdes aldri igjen på grunn av regnskapet: kan noe ikke bokføres,
venter det under [Trenger oppmerksomhet](#trenger-oppmerksomhet) mens resten går videre.

| I Fakturaer | Bilaget |
| --- | --- |
| En faktura utstedes | Kundefordringer debiteres for totalen på fakturaens egen åpne post; hver linjes beløp krediteres inntektskontoen sin ([Posteringsregler](#posteringsregler)); hver sats' merverdiavgift krediteres mva-kontoen til mva-koden. En linje på null utelates, og en faktura med bare nuller bokfører ingenting. |
| En kreditnota utstedes | Det samme med alle fortegn snudd, på den åpne posten til fakturaen den krediterer. |
| En betaling registreres | Bankkontoen debiteres — Vipps-mellomkontoen for en Vipps-betaling — og kundefordringer krediteres på fakturaens post. |
| En betaling fjernes | Det samme snudd tilbake, datert dagen den ble fjernet. |
| Purrekostnader betales | Banken debiteres og kontoen for purreinntekter krediteres, med mva-kode 7 (uten mva): gebyr, erstatning og renter er ikke omsetning. Fjernes betalingen, snus det tilbake. |
| En faktura avskrives | Tap på fordringer debiteres og kundefordringer krediteres for det som var åpent; tilbakeføres merverdiavgiften, føres mva-andelen tilbake på hver sats' mva-konto, merket for mva-meldingen — fordelt på det som er igjen av fakturaen når kreditnotaene er trukket fra, så en sats som er kreditert bort, tar ingen merverdiavgift tilbake. En avrunding føres på avrundingskontoen. Angres avskrivningen, snus bilaget som ble bokført for den, tilbake linje for linje, selv om en konto er endret siden. |
| Det kommer penger etter en avskrivning | Banken debiteres og tap på fordringer krediteres — og den tilbakeførte merverdiavgiften føres tilbake, forholdsmessig. Flere delbetalinger som til sammen betaler det som ble avskrevet, fører tilbake nøyaktig det avskrivningen tok, på øret; en øre i avrunding havner på tap på fordringer, aldri på merverdiavgiften. |

Hvordan du [avskriver en faktura](/nb/user/invoices/#å-avskrive-en-faktura) i Fakturaer, står på den siden.

Regnskapets start og sperren bestemmer datoen:

- En faktura utstedt før regnskapet starter, bokføres ikke: den hører hjemme i inngående
  balanse.
- En betaling på en faktura fra før regnskapet starter, bokføres på fakturaens post i
  inngående balanse. En som er datert før regnskapet starter, men registrert etter at du
  la inn inngående balanse, bokføres på regnskapets startdato, så den telles én gang; en som
  inngående balanse allerede tar med, bokføres ikke igjen.
- Et bilag datert i en sperret måned bokføres på første åpne dag, med sin egen dato som
  dokumentdato.

**Salg fra Fakturaer** på oversikten viser hvor langt regnskapet har lest, og om noe venter.
Et langt etterslep — etter at regnskapet ble satt opp på en travel installasjon, for eksempel —
leses videre hvert femtende sekund til det er tatt igjen.

## Leverandører

**Leverandører** viser selskapene og personene dere kjøper fra, etter nummer: hver ny
leverandør får neste nummer. **Ny leverandør** åpner skjemaet. Skriv et organisasjonsnummer
(ni sifre) eller et navn i **Slå opp i Brønnøysundregisteret** og velg enheten: navn,
organisasjonsnummer og adresse fylles inn. Resten oppgir du selv: e-post, kontonummer
(elleve sifre — kontrollert, så et feilskrevet avvises), eller IBAN og BIC for en utenlandsk
konto, betalingsfristen i dager, valutaen fakturaene kommer i, og kontoen og mva-koden en ny
fakturalinje foreslår.

Et organisasjonsnummer hører til én leverandør per land: samme nummer to ganger i Norge
avvises, mens en utenlandsk enhet kan dele det.

En leverandør regnskapet nevner — med et bilag eller en faktura — slettes aldri: fjern haken
for **Aktiv** i stedet, så tilbys den ikke lenger for nye fakturaer. En som ennå ikke er i
regnskapet, har **Slett**.

**Leverandøren er en person** merker et enkeltpersonforetak: opplysningene er
personopplysninger. Bokføringen oppbevarer dem — som hvert bilag — i fem år etter utløpet av
regnskapsåret for det siste bilaget, og leverandørens side sier til når. **Eksporter
leverandørens opplysninger** laster ned alt regnskapet har om vedkommende — opplysningene,
fakturaene med linjer og betalinger, og hver postering som nevner dem — for å svare på en
persons innsynsbegjæring. Det krever **Eksportere regnskapet**.

Når den datoen er passert, tilbyr personens side **Anonymiser**: navnet blir *Anonymised
supplier* og nummeret, organisasjonsnummer, adresse, e-post og bankopplysninger slettes, og
leverandøren gjøres inaktiv. Bilagene står som de er og nevner fortsatt leverandøren med
nummeret. Det kan ikke angres, og en anonymisert leverandør endres ikke mer. Før datoen
avvises det: bokføringen trenger fortsatt opplysningene. Et selskap anonymiseres ikke —
opplysningene om det er ikke personopplysninger.

Å føre leverandørene krever **Bokføre bilag**.

## Leverandørfakturaer

**Leverandørfakturaer** viser det leverandørene har fakturert, nyeste først, filtrert på
status, leverandør og forfallsdato, eller **Bare forfalte**. En faktura er et **Utkast** til
den bokføres, deretter **Bokført** mens noe skyldes, **Betalt** når ingenting gjenstår, og
**Tilbakeført** når den er rettet med Tilbakefør og kopier.

### Å føre og bokføre en leverandørfaktura

1. Klikk **Ny leverandørfaktura**. Velg **Leverandør** (**+** ved siden av legger til en ny),
   **Faktura** eller **Kreditnota**, og skriv **Leverandørens nummer** — nummeret på
   fakturaen. Samme leverandørs nummer to ganger avvises: skjemaet advarer mens du skriver,
   med en lenke til den som allerede er ført.
2. Oppgi **Fakturadato**, **Forfallsdato** (tomt er leverandørens betalingsfrist etter
   fakturadatoen), **KID** om fakturaen har en, og **Sum** slik fakturaen oppgir den.
3. Legg til en linje per konto: **Konto**, **Mva-kode**, beløpet som **Brutto** (med mva-en
   i, slik de fleste fakturaer viser det) eller **Netto**, med Prosjekter slått på
   **Prosjekt**, og en tekst. Hver linje viser netto og mva slik regnskapet vil bokføre dem,
   og under linjene summerer skjemaet linjene og mva-en. Så lenge linjene ikke går opp i
   summen, sier det hvor mye som gjenstår.
4. **Lagre utkast**. Et utkast kan endres og slettes fritt; ingenting er i regnskapet ennå.
5. Under **Filer** legger du ved fakturaen slik du mottok den — en PDF eller en skanning. Et
   utkast må ha en for å bokføres.
6. **Bokfør**. Bilaget debiterer kontoen på hver linje med netto og mva-koden, debiterer
   mva-en på kodens mva-konto, og krediterer leverandørgjeld med summen på en åpen post for
   fakturaen. Filene blir bilagets vedlegg.

Fakturaen bokføres på fakturadatoen: en dato før regnskapet starter, eller i en sperret
måned, avvises — dater den i en åpen måned, eller flytt sperren tilbake.

Mva-koden på en linje bestemmer mva-en:

| Kode | På fakturaen | Bilaget |
| --- | --- | --- |
| 1, 11, 12, 13 | Norsk mva med 25, 15, 11,11 eller 12 % | mva-en på kodens konto for inngående mva (2710–2713) |
| 0 | ingen mva | netto alene, med koden |
| 86–89, 91, 92 | ingen mva: omvendt avgiftsplikt | mva-en dere beregner selv, begge sider (se under) |

En kode for utgående mva (3, 31, …) hører til salg og avvises på et innkjøp. Innførselskodene
— 14, 15, 20, 21, 22 og 81–85 — avvises også på en leverandørfaktura: mva ved innførsel
beregnes fra tolldeklarasjonen, ikke fra en faktura, og føres som en postering
([Innførsel av varer](#innførsel-av-varer)).

### Omvendt avgiftsplikt — tjenester kjøpt fra utlandet

En leverandør i utlandet fakturerer uten norsk mva, og dere beregner den selv: regnskapet
bokfører den to ganger, som mva dere skylder og — når den er fradragsberettiget — som mva dere
trekker fra. Leverandørgjelden blir det leverandøren fakturerer. Før linjen **Netto**:
fakturaen har ingen mva å trekke ut.

*Eksempel.* Et rådgivningsfirma i Sverige fakturerer 1 000 kroner for en gjennomgang av
systemene deres; dere fører én linje på 1 000,00 på 6700 med kode **86** (tjenester fra
utlandet, med fradrag). Bilaget:

| Konto | Debet | Kredit |
| --- | --- | --- |
| 6700 Regnskapstjenester, rådgivning med mer (kode 86, grunnlag) | 1 000,00 | |
| 2715 Inngående merverdiavgift, omvendt avgiftsplikt (kode 86) | 250,00 | |
| 2705 Beregnet merverdiavgift, omvendt avgiftsplikt og innførsel (kode 86) | | 250,00 |
| 2400 Gjeld til leverandører | | 1 000,00 |

Med kode **87** (tjenester fra utlandet *uten* fradrag — for en kostnad der mva-en ikke kan
trekkes fra) debiteres 250,00 på 6700 selv i stedet for 2715: mva-en blir en del av
kostnaden. Kode 88 og 89 er det samme med lav sats, 91 og 92 klimakvoter og gull.

På bilaget er den beregnede siden — 2705-linjen — merket **Beregnet mva (omvendt
avgiftsplikt)**, atskilt fra mva-en dere trekker fra, så mva-meldingen teller grunnlaget én
gang.

### Innførsel av varer

Når dere innfører varer, er grunnlaget for mva-en tolldeklarasjonens, ikke leverandørens
faktura: tollverdien, med frakt og toll. Derfor føres de to hver for seg:

1. Leverandørens faktura, som leverandørfaktura, med varelinjen på varekontoen og uten
   mva-kode (eller kode **0**): kostnaden for innførte varer, uten egen mva.
2. Innførselsmva-en, som en [postering](#å-bokføre-en-postering) fra tolldeklarasjonen: en
   linje på varekontoen med kode **81** (83 med middels sats; 82 og 84 når mva-en ikke er
   fradragsberettiget) og deklarasjonens grunnlag, ført **Netto**, og samme beløp kreditert
   tilbake på samme konto uten kode. Mva-linjene legges til for deg.

*Eksempel.* Deklarasjonen verdsetter varene til 10 000 kroner med 25 %:

| Konto | Debet | Kredit |
| --- | --- | --- |
| 4005 Varekostnader (kode 81, grunnlag) | 10 000,00 | |
| 4005 Varekostnader | | 10 000,00 |
| 2714 Inngående merverdiavgift, innførsel (kode 81) | 2 500,00 | |
| 2705 Beregnet merverdiavgift, omvendt avgiftsplikt og innførsel (kode 81) | | 2 500,00 |

De to grunnlagslinjene opphever hverandre: varene står allerede på leverandørens faktura, og
posteringen legger bare til mva-en, skyldig og trukket fra. Innførselsmva en speditør eller
tollvesenet fakturerer dere for (kode 14 og 15), føres på samme måte, som en postering fra
deklarasjonen.

### Annen valuta

Velg fakturaens **Valuta** (leverandørens foreslås). Regnskapet føres i NOK, så oppgi enten
kursen — **Etter kurs**, NOK per enhet — eller **Etter sum i NOK**, summen i kroner; skjemaet
viser den andre ved siden av, og ingenting foreslås: ta den fra banken eller tollen. Gjennom
API-et kan begge oppgis, og de må stemme: summen etter kursen, avrundet til øre, innenfor en
øre per linje av summen i NOK — ellers avvises fakturaen som feilskrevet. Et utkast laget fra
en utgift i annen valuta har ingen kurs ennå: **Bokfør** ber om den. Hver
postering er linjens beløp etter kursen, avrundet til øre, med beløpet i valuta og kursen
lagret ved siden av; en øre avrundingen etterlater, havner på den største linjen, så bilaget
beholder fakturaens egen sum. Mva ved omvendt avgiftsplikt beregnes av kronebeløpet.

### Å betale en leverandør

På en bokført faktura spør **Registrer en betaling** om dato, **Beløp i NOK** slik banken
trakk det, og **Bankkonto** (tomt er bankkontoen; en annen eiendelskonto, som en valutakonto,
kan velges). Bilaget debiterer leverandørgjeld på fakturaens post og krediterer banken. En
delbetaling lar resten stå; fakturaen er **Betalt** når ingenting gjenstår.

For en faktura i annen valuta oppgir du også **Andel gjort opp** i valutaen — tomt gjør opp
alt som gjenstår. Andelen er verdt beløpet sitt etter fakturaens kurs; trakk banken flere
kroner enn det, er forskjellen agiotap (8160), trakk den færre, agiogevinst (8060), bokført på
samme bilag.

**Fjern** på en betaling bokfører den snudd, datert dagen den ble betalt — avvist når den
måneden er sperret. Fakturaen skyldes igjen.

En leverandørfakturas post betales her, aldri med **Registrer** under Åpne poster.

### Kreditnotaer

En kreditnota føres som en faktura, med **Kreditnota** valgt og, under **Krediterer
faktura**, den bokførte fakturaen den krediterer. Den bokføres med alle fortegn snudd —
kostnaden og mva-en kreditert, leverandørgjeld debitert — på den åpne posten til fakturaen den
krediterer, som da skylder resten. En kreditnota som ikke gjelder en bestemt faktura, står på
sin egen post; et beløp leverandøren betaler tilbake, registreres på den som en betaling.

**Bokfør** holder en kreditnota opp mot fakturaen slik den står da: fakturaen må fortsatt
være bokført, kreditnotaen må være i fakturaens valuta, og den kan ikke kreditere mer enn det
som gjenstår på fakturaen — i NOK, og i valutaen for en faktura i annen valuta. En kreditnota
og en betaling som bokføres i samme øyeblikk på samme faktura, venter på hverandre, så den
andre ser det den første lot stå igjen.

### Å rette en bokført faktura: Tilbakefør og kopier

Et bokført bilag endres aldri. **Tilbakefør og kopier** spør om dato og årsak, bokfører
fakturaens bilag tilbakeført, linje for linje, merker fakturaen **Tilbakeført**, og åpner et
nytt utkast med samme leverandør, nummer, linjer og filer, som du retter og bokfører. En
faktura med en betaling stående tilbakeføres ikke: fjern betalingen først. Det gjør heller
ikke en med en kreditnota, eller en betaling gjort gjennom en kreditnota, stående på seg:
tilbakefør kreditnotaen først (Tilbakefør og kopier på kreditnotaen), deretter fakturaen.

Å føre, bokføre og betale leverandørfakturaer krever **Bokføre bilag**.

## Slik kommer utgiftene inn fra Utgifter

Med [Utgifter](/nb/user/expenses/) slått på bokføres det som godkjennes og refunderes der, av
seg selv, slik salget gjør — et bilag hver, i løpet av sekunder eller minutter:

| I Utgifter | Bilaget |
| --- | --- |
| En utgift den ansatte betalte (et utlegg, kjøring, diett) godkjennes | Kontoen til utgiftens kategori debiteres ([Posteringsregler](#posteringsregler)); 2910 *Gjeld til ansatte* krediteres. |
| En utgift selskapet betalte, godkjennes | Kategoriens konto debiteres; banken krediteres. |
| En reiseregning godkjennes | Hver linje som over, på ett bilag. |
| En utgift eller reiseregning refunderes | 2910 debiteres og banken krediteres med det godkjenningen skyldte, datert refusjonsdagen. |
| En godkjenning eller refusjon angres | Bilaget snus, linje for linje. |

Å godkjenne, angre og godkjenne igjen gir altså tre bilag: kostnaden, tilbakeføringen og
kostnaden igjen. Prosjektet på en utgift blir posteringens prosjekt. En utgift datert før
regnskapet starter, bokføres ikke.

**Mva.** En utgift har et mva-beløp, men ingen mva-kode. Regnskapet trekker det fra når
kategoriens regel sier at mva-en er fradragsberettiget, utgiften er et utlegg i NOK, og
mva-en passer en av de inngående satsene — 25, 15, 12 eller 11,11 % av resten, med en øre per
hundre kroner å gå på: kostnaden bokføres da netto med den satsens kode, og mva-en på kontoen
for inngående mva. Ellers bokføres utgiften brutto, uten fradrag; når kategorien tillater
fradrag, men mva-en ikke passet noen sats, sier Trenger oppmerksomhet *Mva ikke trukket fra*
— til orientering: den er bokført. Kjøring og diett har ikke mva.

**I annen valuta.** Utgifter lagrer ingen kurs, så en utgift i annen valuta venter under
Trenger oppmerksomhet på beløpet i NOK. Bilaget som bokføres med beløpet du fører inn, er
ditt, slik en postering ville vært, og linjen sier hva som ble ført inn: *NOK 921.50 ført inn
for EUR 80.00*.

**Leverandørfakturaer fra Utgifter.** En godkjent utgift av typen leverandørfaktura bokføres
ikke av seg selv — den har ingen leverandør i regnskapet og ingen betaling. Den venter under
Trenger oppmerksomhet på **Lag leverandørfaktura**.

## Posteringsregler

**Posteringsregler** bestemmer hvilken inntektskonto hver fakturalinje krediteres. En regel
treffer på én ting, og den første som treffer, gjelder:

1. linjens **type** — timer, utlegg, milepæler, energioppgjør, ordrelinjer,
   abonnementsperioder, fradrag av a konto, linjer skrevet inn for hånd;
2. produktets **avgiftskategori**, for en linje fra en ordre;
3. linjens **mva-kode**;
4. **standardregelen** for mva-koden, satt opp med regnskapet: avgiftspliktig salg på 3000
   (3010 middels sats, 3020 lav sats), avgiftsfritt salg på 3100, utførsel på 3150,
   innenlands omvendt avgiftsplikt på 3160, salg utenfor merverdiavgiftsloven på 3200.

**Legg til en regel** med navn, hva den treffer, og kontoen — en inntektskonto: velgeren
tilbyr ingen andre. Klikk på navnet til en regel for å endre navn eller konto: begge gir en
ny **versjon** av regelen, og bare bilag som bokføres etter det, følger den — det som er
bokført, blir stående. Hver inntektslinje i et bilag oppgir regelen og versjonen som valgte
kontoen, under navnet regelen hadde da. **Arkiver** tar en regel ut av bruk: den treffer
ikke lenger, navnet og det den treffer, er ledig for en ny regel, og bilag den har plassert,
oppgir den fortsatt. En standardregel arkiveres aldri; pek den til en annen konto i stedet.

**Utgiftsregler.** **Legg til en regel** med **Utgifter** valgt oppgir en utgiftskategori fra
Utgifter, en kostnadskonto, og om kategoriens mva er **fradragsberettiget** — la den være av
for representasjon og velferd, der merverdiavgiftsloven aldri gir fradrag. En utgift i en
kategori ingen regel nevner, bokføres på 7700 Andre kostnader, uten mva-fradrag. Å endre
kontoen eller fradragsretten gir en ny versjon, som for en salgsregel.

Å endre reglene krever **Forvalte regnskapet**.

## Trenger oppmerksomhet

**Trenger oppmerksomhet** viser hvert faktum regnskapet ikke kunne bokføre, med årsaken: en
mva-kode regnskapet ikke har, eller som er inaktiv; en konto som er inaktiv, eller en regel som
peker til en konto av feil type; en type faktum denne versjonen ikke kjenner; en utgift i
annen valuta; en utgift av typen leverandørfaktura. Oversikten og dashbordet teller dem.

Det som kommer etter for samme faktura — eller samme utgift eller reiseregning — venter bak,
så regnskapet tar aldri en betaling før fakturaen den betaler, eller en refusjon før
utgiften: en slik linje sier at den **venter** på den tidligere.

Rett årsaken — aktiver kontoen, legg til mva-koden, endre regelen — og klikk **Prøv igjen** på
linjen: faktumet leses på nytt og bokføres, og linjen viser deretter bilaget. Når den
bokføres, bokføres det som ventet bak den også, i rekkefølge. Prøver du igjen en linje som
venter, venter den fortsatt: prøv igjen den den venter på. Et faktum avvises aldri. **Vis også
dem som er bokført siden** viser dem et nytt forsøk har bokført.

To årsaker har sin egen handling:

- **I annen valuta: beløpet i NOK trengs.** Klikk **Før inn beløpet i NOK**, oppgi hva hver
  utgift kostet i kroner — vanligvis fra bank- eller kortutskriften — og **Før inn og
  bokfør**. Det som ventet bak, refusjonen for eksempel, bokføres etter.
- **En leverandørfaktura fra Utgifter.** Klikk **Lag leverandørfaktura**. Dialogen spør om en
  ny leverandør er en person (et enkeltpersonforetak) — huk av om den er det: opplysningene om
  en person oppbevares og anonymiseres senere slik [Leverandører](#leverandører) sier. Et
  utkast åpnes så med utgiftens beløp, mva-kode, dato, nummer, forfallsdato, prosjekt og
  kvitteringer, og leverandøren — leverandøren med det navnet, eller en ny. Kontroller det,
  og bokfør det som en hvilken som helst leverandørfaktura. Sletter du utkastet — eller
  kopien en Tilbakefør og kopier av det laget — kommer utgiften tilbake på listen. Blir
  utgiften ikke lenger godkjent i Utgifter før det, forsvinner linjen av seg selv. En utgift
  gjøres om til én leverandørfaktura: en til avvises.

Blir en utgift ikke lenger godkjent i Utgifter *etter* at den ble gjort om til en
leverandørfaktura, snur regnskapet ingenting — fakturaen er deres — og Trenger oppmerksomhet
sier det på en linje som nevner fakturaen: *Utgiften bak en leverandørfaktura ble ikke lenger
godkjent*. Mens fakturaen er et utkast, venter linjen: slett utkastet, eller bokfør det om
fakturaen likevel skyldes, og klikk **Prøv igjen**. Når fakturaen er bokført, er linjen til
orientering; tilbakefør fakturaen om den ikke skyldes.

En linje merket **Til orientering** — *Mva ikke trukket fra* — er bokført; den telles ikke med
det som venter. **Kvitter ut** tar den av listen, og den forsvinner av seg selv når utgiften
ikke lenger er godkjent i Utgifter og bilaget er snudd. Skulle mva-en vært trukket fra,
tilbakefører du utgiftens bilag med en postering og fører den på nytt med riktig kode.

Å prøve igjen og handlingene krever **Bokføre bilag**.

## Åpne poster

**Åpne poster** viser, per kunde og per leverandør, hvert dokument som ikke er gjort opp: nummer, datoer,
opprinnelig beløp og saldo, 200 om gangen med **Forrige** og **Neste**. **Vis oppgjorte**
viser de oppgjorte også. En postering på
kundefordringer som ikke oppgir noe dokument, havner på kundens **Uspesifisert**-post.

**Registrer** på en post registrerer penger som er flyttet: en innbetaling fra kunden,
eller en tilbakebetaling av et tilgodehavende. Oppgi dato, beløp (høyst saldoen) og, når
det ikke er bankkontoen, kontoen pengene gikk gjennom. Bilaget bokføres med en gang.

En post merket **I Fakturaer** står for en faktura der: betalingen registreres på fakturaen
i Fakturaer, aldri her. En leverandørfakturas post betales på leverandørfakturaen.

Å registrere krever **Bokføre bilag**.

## Mva-terminen

Merverdiavgiften rapporteres til Skatteetaten én **termin** om gangen. **Mva** i
sidemenyen viser terminene fra den regnskapet starter i til den inneværende, nyeste først:

- Regnskap med **annenhver måned** har seks tomånedersterminer hvert kalenderår —
  januar–februar, mars–april og så videre til november–desember — uansett regnskapsår.
  Regnskap med **årlig** termin har én termin per kalenderår.
- Hver termin viser perioden, statusen — **Åpen**, **Lukket** eller **Levert** — fristen og
  summen i mva-meldingen, **Å betale** eller **Til gode**. En tomånederstermin har frist den
  10. i den andre måneden etter (termin 1 den 10. april), unntatt termin 3, som har frist
  31. august; en årlig termin 10. mars året etter.
- **Klar til å lukkes** viser en termin som er slutt og ikke lukket; **Fristen er
  passert** en termin der fristen er passert uten at den er merket som levert. Regnskap-kortet
  på startsiden og **Oversikt** sier det samme, med neste termin som forfaller.

Regnskap som er satt opp som **Ikke mva-registrert**, har ingen mva-terminer: Mva-siden sier
at selskapet ikke er mva-registrert, og viser ikke noe annet. Når regnskapets mva-termin og
selgerens mva-registrering i Fakturaer ikke stemmer overens, sier **Oversikt** fra.

### Å kontrollere tallene for en termin

Klikk på en termin. Siden viser **mva-meldingen** satt opp slik Skatteetatens er:

- kodene i meldingens grupper og rekkefølge — salg i Norge, salg til utlandet, kjøp i
  Norge, varer og tjenester fra utlandet, fisk, klimakvoter og gull — hver med grunnlag,
  sats og mva;
- omvendt avgiftsplikt og innførsel som to linjer på samme kode: den beregnede mvaen og
  fradraget;
- et tap på krav som er avskrevet med mva-korreksjon, som en egen linje, *Tap på krav*, på
  inngående kode med samme sats (kode 1 for tap på salg med 25 %);
- hele kroner: Skatteetaten tar ikke imot desimaler, så ørene kuttes på hver linje. Under
  meldingen viser siden hva regnskapet sier på øret, og differansen, som lukkingen fører på
  øreavrundingskontoen.

En åpen termin viser meldingen slik regnskapet står nå; en lukket termin meldingen slik den
ble lukket. Under den står **merverdiavgiftsspesifikasjonen**: per kode grunnlaget,
utgående mva, beregnet mva, fradraget, tap på krav og mvaen som ikke er fradragsført (en
kostnad, som på tjenester fra utlandet uten fradragsrett). Kode 0, 7, 20, 21 og 22 er ikke
med i meldingen og står for seg. Klikk på et beløp for å se posteringene bak det.

**Avstemming** sammenligner bevegelsen på mva-kontoene i terminen med mvaen meldingen
teller. De er ulike når noe er ført rett på en mva-konto uten mva-kode; siden viser da de
posteringene. Tilbakefør hver av dem og før dem på nytt med riktig mva-kode før terminen
lukkes. Den samme spesifikasjonen, for hvilken som helst periode, er
**Merverdiavgiftsspesifikasjon** under [Rapporter](#rapporter), med CSV-fil.

### Å lukke terminen

Når terminen er slutt og tallene stemmer, klikker du **Lukk terminen**. Dialogen sier hva
lukkingen gjør og alt som stopper den nå:

- Terminen må være slutt, og terminene før den må være lukket: terminene lukkes i
  rekkefølge.
- Ingenting datert i terminen kan vente under [Trenger oppmerksomhet](#trenger-oppmerksomhet).
- Regnskapet kan ikke være sperret til og med terminens siste dag: flytt sperren tilbake
  først ([Å sperre en periode](#å-sperre-en-periode)).
- Hver lukket termin må fortsatt ha tallene den ble lukket med.
- Hver kode i mva-meldingen må ha en sats på posteringenes datoer.
- Mva-kontoene må stemme.

En termin som starter før regnskapet — regnskap som starter 15. august i en årlig termin,
for eksempel — har en mva-melding som bare dekker dagene fra regnskapets start. Siden sier
det, og dialogen ber deg bekrefte det når du lukker.

Lukkingen

1. lagrer mva-meldingen slik den står;
2. fører **oppgjørsbilaget** på terminens siste dag: bevegelsen på hver mva-konto i
   terminen flyttes til oppgjørskontoen for mva (2740), meldingens hele kroner der, og
   ørene meldingen kuttet til øreavrundingskontoen (7790);
3. sperrer regnskapet til og med terminens siste dag;
4. gjør meldingens XML-fil klar til å lastes ned. Ingenting sendes noe sted.

I en lukket termin føres ingenting mer: det Fakturaer eller Utgifter bokfører med en dato i
den, bokføres på første dag etter den og teller i neste termin, og en postering datert i den
avvises. Å lukke krever **Forvalte regnskapet**.

### Å levere mva-meldingen hos Skatteetaten

Vantigo sender ikke mva-meldingen til Skatteetaten i denne fasen: den leveres hos
Skatteetaten, for hånd.

1. Klikk **Last ned XML** på siden til den lukkede terminen: meldingen i Skatteetatens eget
   format (*skattemelding for merverdiavgift*), til å oppbevare med regnskapet.
2. Logg inn hos Skatteetaten og lever mva-meldingen for terminen. Skriv inn tallene fra
   terminens side — kode, grunnlag, sats og mva linje for linje — eller sammenlign dem med
   det du fører inn der.
3. Tilbake i Vantigo klikker du **Merk som levert**, fører inn dagen du leverte og
   Skatteetatens referanse fra kvitteringen, og klikker **Merk som levert**. Det krever
   **Bokføre bilag**.

En levert termin kan ikke åpnes igjen. Er en
termin merket som levert ved en feil, klikker du **Fjern leveringsmerket** og oppgir en
begrunnelse; det krever **Forvalte regnskapet**, og terminens historikk tar vare på hvem som
gjorde det og hvorfor.

### Å åpne en termin igjen

En lukket termin som ikke er levert, kan åpnes igjen — bare den sist lukkede. Klikk **Åpne
igjen** og si hvorfor. Sperren flyttes tilbake til dagen før terminens første dag (for
regnskapets første termin oppheves den), og oppgjørsbilaget tilbakeføres på terminens siste
dag. Rett det som var feil, og lukk terminen igjen. Gjenåpningen lagres i sperrens og
terminens historikk. Å åpne igjen krever **Forvalte regnskapet**.

Ingenting en lukking laget, går tapt: **Lukkinger** på terminens side viser hver lukking,
summen, hvilken som gjelder og hvilken som ble levert, hver med **Last ned XML**.

### Å rette en termin etter levering

Når en termin er levert, forblir regnskapet for den lukket. Før rettelsen i den åpne
terminen — en postering, en kreditnota, en tilbakeført og ny leverandørfaktura — datert i
dag: den teller i terminen den er datert i, og slik rettes en liten feil i praksis. Å
erstatte en levert melding venter til Vantigo leverer meldingene selv.

### Å betale mvaen

Etter lukkingen står terminens mva på 2740 Skyldig merverdiavgift, oppgjørskontoen for mva: kredit når det er
mva å betale, debet når det er mva til gode. Når du betaler Skatteetaten innen fristen,
fører du betalingen som en postering datert dagen banken betalte: debet 2740 og kredit
bankkontoen (1920), med Skatteetatens KID i teksten. En tilbakebetaling går motsatt vei:
debet banken, kredit 2740. Når 2740 er tilbake på null, er terminen gjort opp. Med bankens
kontoutskrift i **Bank** foreslås betalingens linje som **Merverdiavgift betalt eller
tilbakebetalt** og fører det samme bilaget for deg ([Å betale mvaen fra
banken](#å-betale-mvaen-fra-banken)).

## Banken

**Bank** er der regnskapet møter banken: hver av selskapets bankkontoer, kontoutskriften —
importert som fil eller lest fra banken — og hver linje i den avstemt mot det regnskapet
har. Når alle linjene er avstemt og saldoen etter kontoutskriften er hovedbokens, er
kontoen avstemt.

Å se banken krever **Bruke Regnskap**; å importere en kontoutskrift og avstemme krever
**Bokføre bilag**; å legge til en bankkonto, velge hvor kontoutskriften kommer fra,
CSV-oppsettet og bankreglene krever **Forvalte regnskapet**.

### Bankkontoer

Hver bankkonto er kontoutskriften for én konto i kontoplanen: driftskontoen på 1920, en
sparekonto på 1950, en kortkonto på sin egen eiendelskonto. Én hovedbokskonto hører til én
bankkonto.

Første gang du åpner **Bank**, foreslås kontoen Fakturaer skriver på fakturaene som den
første bankkontoen, på 1920: klikk **Legg den til**. For å legge til en annen, klikk **Legg
til bankkonto** og oppgi navnet, **Kontonummer** (elleve sifre; mellomrom, punktum eller et
norsk IBAN går fint), IBAN og BIC om du vil, og **Hovedbokskonto**. Bankkontoer er i NOK i
denne versjonen.

Fra bankkontoen er lagt til, bokføres innbetalingene Fakturaer registrerer fra en
bankfil eller bankintegrasjonen på hovedbokskontoen til bankkontoen de kom inn på — en
innbetaling til sparekontoen havner på 1950, ikke 1920. En innbetaling ført for hånd i
Fakturaer har ingen konto og går til bankkontoen i kontoplanen (1920). Innbetalinger som
er bokført før, blir stående som de er.

En bankkontos **innstillinger** (tannhjulet i listen) endrer opplysningene, hvor
kontoutskriften kommer fra og CSV-oppsettet, og viser kontoutskriftene med filene. Når en
bankkonto har kontoutskriftslinjer, står kontonummeret og hovedbokskontoen som de er —
linjene er avstemt mot posteringer på den hovedbokskontoen; legg til en ny bankkonto i
stedet. En bankkonto med kontoutskrifter slettes ikke: gjør den inaktiv. En inaktiv konto
som fortsatt har linjer som ikke er avstemt, teller med når du sperrer en måned.

### Å importere en kontoutskrift

Klikk **Importer kontoutskrift**, velg filen og klikk **Importer**.

- **camt.053** er kontoutskriften alle norske banker tilbyr i nettbanken, ofte under
  *Kontoutskrift* eller *regnskapsfiler* som *camt.053* eller *ISO 20022 kontoutskrift*. Én
  fil kan ha flere kontoer: hver utskrift går til bankkontoen med det nummeret. En konto som
  ikke er en bankkonto her, nevnes i svaret og utelates — legg den til og importer filen på
  nytt.
- **CSV** er det en mindre bank kan eksportere i stedet. Velg bankkontoen filen gjelder:
  filen leses med kontoens **CSV-oppsett**.

Svaret viser for hver konto perioden, hvor mange linjer som ble importert, hvor mange som
fantes fra før, og om utskriften henger sammen med den forrige. En linje som er importert,
importeres aldri to ganger: den samme filen igjen, eller en senere utskrift som overlapper
en tidligere, legger bare til det som er nytt — og to virkelige like linjer samme dag blir
begge stående. Når banken oppgir saldo, vises et hull mellom to utskrifter, en overlapp
eller en inngående saldo som ikke er forrige utgående saldo, uten at noe avvises. Poster
som ikke er bokført i banken ennå, utelates til de er bokført. Selve filen tas vare på
sammen med regnskapet.

En CSV-eksport og en camt.053-fil for de samme dagene har de samme bevegelsene, uten noe som
skiller dem. En linje som har samme dag og beløp som en linje som allerede er importert fra
en annen type fil, eller fra bankintegrasjonen, og samme KID, motpartskonto eller tekst,
importeres derfor merket **Mulig duplikat**: svaret teller dem, og i avstemmingen får den
ingen forslag før du klikker **Legg til side som duplikat** (den teller da i ingen saldo)
eller **Behold den, den er ikke et duplikat**.

For en bankkonto som leses fra bankintegrasjonen, bringer en fil som importeres etterpå
bare dagene før dagen integrasjonen leser fra; svaret teller linjene som ble utelatt, og som
integrasjonen henter.

### CSV-oppsettet

I en bankkontos innstillinger sier **CSV-oppsett** hvordan bankens CSV-eksport leses:
skilletegnet, tegnsettet (UTF-8, eller ISO-8859-1 for de fleste norske bankers eksporter),
hvor mange overskriftsrader som hoppes over, datoformatet, desimaltegn og tusenskille, og
kolonnenumrene (fra 1) for bokført dato og enten ett beløp med fortegn eller egne kolonner
for *ut* og *inn* — med tekst, referanse, KID og motpart hvis filen har dem. Velg en fil
under **En fil å prøve** og klikk **Forhåndsvis** for å se de første radene slik de vil bli
importert, og deretter **Lagre oppsettet**.

### Bankintegrasjonen som kilde

Når Fakturaer er koblet til banken (Administrer arbeidsområde › **Integrasjoner** › **Bank**, se
[Fakturaer](/nb/user/invoices/)), kan en bankkonto banken tilbyr, få kontoutskriften fra
banken selv. I bankkontoens innstillinger, under **Kontoutskrift fra**, velger du
**Bankintegrasjon** og klikker **Lagre kilden**. Integrasjonen leser fra dagen etter den
siste kontoutskriften du importerte for kontoen; du kan oppgi en senere dag under **Les
fra**, men ikke en tidligere, så ingen dag leses to ganger (og høyst 13 måneder tilbake).
Kontoen leses da omtrent hver halvtime.

Fakturaer leser bare pengene som kommer inn, for å registrere innbetalinger fra kunder.
Regnskapet leser hele bevegelsen på kontoen — betalinger til leverandører, lønn, gebyrer,
kortkjøp og overføringer mellom egne kontoer — så også en kortkonto eller en
skattetrekkskonto kan leses. Det er én tilkobling og én nøkkel, Fakturaerens; ingenting
legges inn to ganger.

Innstillingene viser hvordan integrasjonen står: når banken sist ble lest, når den leses
neste gang, og hva som gikk galt — **banken avviste nøkkelen** (lagre en ny nøkkel under
Bankintegrasjon i Fakturaer), **banken ba om å vente** (den leses igjen etter bankens
ventetid), **bankens svar er for stort** (banken klarer ikke å svare for én enkelt dag av
kontoen i ett svar; det prøves igjen, og vedvarer det, spør banken) eller **ingen bank
koblet til**. En startdag langt tilbake leses noen uker om gangen, så de eldste linjene kan
bruke noen minutter på å komme. Oversikten og dashbordet sier det også.

### Å avstemme en bankkonto

Klikk en bankkonto for å avstemme den. Velg perioden (inneværende måned som standard) og,
under **Vis**, alle linjer, de som er **Ikke avstemt**, de som er **Avstemt**, eller de som
er **Lagt til side** som duplikater. Hver linje viser det banken oppga — dagen, teksten,
motparten, KID-en — og beløpet. En lang periode kommer i sider på 200 linjer.

En linje som ikke er avstemt, viser **forslagene**, det sikreste først, hvert med hvorfor
det foreslås og, når det fører et bilag, hva det vil føre. Klikk **Godta** for å ta et;
**Konter** for å føre linjen selv; eller la den stå til senere. En avstemt linje viser hva
den er avstemt mot, og **Angre** så lenge måneden ikke er sperret. En linje som var avstemt
mot en betaling som er fjernet siden — i Fakturaer eller på leverandørfakturaen — eller mot
et bilag som er tilbakeført, er åpen igjen, med en merknad om hvorfor.

Forslagene:

| Forslag | Når | Hva det å godta gjør |
| --- | --- | --- |
| **Innbetalingen Fakturaer registrerte** | Fakturaer registrerte en innbetaling fra akkurat denne banklinjen — fra en bankfil eller bankintegrasjonen | knytter linjen til innbetalingens postering |
| **En postering med samme beløp** | regnskapet har en postering på kontoen med samme beløp, innen ti dager | knytter dem sammen |
| **En bunt med innbetalinger fra Fakturaer** | flere KID-innbetalinger kommer som én linje: bankens bunt, eller innbetalingene Fakturaer registrerte én dag | knytter linjen til alle |
| **Betal en leverandørfaktura** | penger ut med en leverandørfakturas KID, nummeret i teksten, eller beløpet til leverandørens konto | registrerer betalingen på leverandørfakturaen, som **Registrer betaling** ville gjort; for en faktura i annen valuta, se under |
| **En utbetaling fra Vipps** | penger inn fra Vipps | flytter den fra Vipps-oppgjørskontoen til banken, med Vipps' gebyr til bank- og betalingsgebyrer (7770): se under |
| **Merverdiavgift betalt eller tilbakebetalt** | penger til eller fra Skatteetaten som er beløpet for en lukket mva-termin, ikke betalt ennå, som fortsatt står på 2740 | debiterer eller krediterer 2740 mot banken |
| **En overføring mellom kontoene dine** | samme beløp går ut av én av bankkontoene og inn på en annen, og ingen overføring er ført for det ennå | ett bilag mellom hovedbokskontoene, avstemt på begge utskriftene |
| **En bankregel** | teksten eller motparten inneholder en regels ord — aldri penger inn med KID eller med et beløp en kunde skylder | fører linjen på regelens konto og mva-kode |
| **Registrer den i Fakturaer** | penger inn fra en kunde som Fakturaer ikke har registrert | ingenting her: se under |

Flere forslag merket **Sikker** — én postering med samme beløp samme dag, eller samme
bankreferanse — kan godtas samtidig: klikk **Godta de sikre** og bekreft.

**Konter** fører et bankbilag: bankkontoens side legges til av seg selv, og du oppgir
konto, beløp og mva-kode for resten, som til sammen må gå i balanse med linjen. Med en
mva-kode er beløpet brutto: et kortkjøp på 349,00 på 6300 med kode 1 fører 279,20 på 6300 og
69,80 på 2710. En linje kan gjøre opp en åpen post hos en kunde eller leverandør som ikke
står for en faktura i Fakturaer eller en leverandørfaktura — en inngående saldo, for
eksempel; en linje mot kundefordringer eller leverandørgjeld må oppgi den åpne posten den
gjør opp.

En leverandørfaktura i annen valuta gjøres opp i sin valuta. Når det banken trakk, ligger nær
det som gjenstår etter fakturaens kurs — innenfor 5 % — gjør betalingen opp alt, og
differansen er en valutagevinst eller et valutatap. Når det ikke gjør det, er det en
delbetaling: **Godta** spør om **Beløp gjort opp i** fakturaens valuta, med det betalingen er
verdt etter fakturaens kurs som forslag, og fakturaen skylder fortsatt resten.

### Hvorfor innbetalinger fra kunder registreres i Fakturaer

En kundes betaling av en faktura hører hjemme på fakturaen, i Fakturaer: det er der
fakturaen merkes betalt, der purringene stopper, og der den bokføres fra. Penger inn med
KID, eller en bunt med innbetalinger, konteres derfor aldri her, og heller ikke penger inn
kontert på en salgskonto. Når Fakturaer ikke har registrert den ennå, er forslaget
**Registrer den i Fakturaer**: importer bankfilen der, la bankintegrasjonen gjøre det,
eller registrer betalingen på fakturaen — så avstemmes posteringen mot linjen her.

### Utbetalinger fra Vipps

Vipps betaler ut salget for hver oppgjørsdag minus gebyrene, en dag eller to senere.
Vipps-betalingene Fakturaer registrerer, står på Vipps-oppgjørskontoen (1580). Når salget
for en dag — eller for noen dager på rad — som ikke er utbetalt ennå, stemmer med
utbetalingen eller er inntil 3 % mer, sier forslaget hvilke dager, flytter det salget fra
oppgjørskontoen, utbetalingen til banken og differansen — Vipps' gebyr — til 7770. Neste
utbetaling starter da fra dagen etter. Når ingen rekke dager passer, gjettes ikke noe gebyr:
utbetalingen alene flyttes fra oppgjørskontoen til banken. **Godta** viser gebyret og lar deg
endre det til det oppgjørsrapporten fra Vipps sier, høyst 5 % av utbetalingen; det som står
igjen på 1580, er salg som ikke er utbetalt ennå.

### Å betale mvaen fra banken

Når mva-terminen er lukket, står merverdiavgiften på 2740. Banklinjen for betalingen til
Skatteetaten — eller for tilbakebetalingen — foreslås som **Merverdiavgift betalt eller
tilbakebetalt**: å godta den debiterer 2740 (en tilbakebetaling krediterer den) mot banken,
og 2740 er tilbake på null. En termin som er betalt gjennom banken, foreslås ikke igjen.

### Bankregler

**Bankregler** sparer deg for å kontere den samme typen linje hver måned: en regel sier
hvilke ord linjens tekst eller motpart inneholder, hvilken vei pengene går, og kontoen og
mva-koden den føres på — *Gebyr* til 7770, bankens renter til 8050. Regelen vises da som et
forslag på hver linje den passer på. En endring er en ny versjon; en regel som er brukt,
gjøres inaktiv i stedet for å slettes. En regel for penger inn kan ikke gå til en salgskonto
eller til kundefordringer, og penger inn med KID eller med et beløp en kunde skylder, får
ikke forslag fra noen regel: det er innbetalinger fra kunder, som registreres i Fakturaer.

### Å angre en avstemming

**Angre** på en avstemt linje, så lenge måneden ikke er sperret, tar avstemmingen tilbake:
et bilag avstemmingen førte, tilbakeføres (en leverandørbetaling fjernes), en overføring
åpner begge linjene — også når den andre siden var avstemt mot overføringens postering — og
en avstemming mot posteringer som fantes, slipper dem. Ingenting slettes.

### Å sperre en måned

Når du sperrer en måned (**Innstillinger** › **Sperredato**) mens en bankkonto har linjer i
den som ikke er avstemt, eller saldoen etter kontoutskriften er en annen enn hovedbokens,
får du vite hvilke og blir bedt om å klikke **Sperr likevel**. Det kan du: sperrehistorikken tar
vare på det. Å lukke en mva-termin viser den samme advarselen og går videre.

## Rapporter

**Rapporter** lager spesifikasjonene for hvilken som helst periode, hver med når den ble
laget:

- **Bokføringsspesifikasjon** — alle bilagenes posteringer i nummerrekkefølge.
- **Kontospesifikasjon** — én kontos inngående saldo, posteringene og utgående saldo.
- **Kunder** og **Leverandører** — per part, postene med saldo eller bevegelse i perioden,
  og saldoen fordelt på forfall: ikke forfalt, 1–30, 31–60, 61–90 og over 90 dager. En post
  for en faktura lenker til den i Fakturaer; en leverandør lenker til siden sin og en post til
  leverandørfakturaen. Leverandørenes sum er saldoen på leverandørgjeld.
- **Saldobalanse**, **Resultatregnskap** og **Balanse**. **Sammenlign med året før** legger
  til samme periode året før (29. februar sammenlignes med 28. februar). Balansen viser
  resultatet som ikke er avsluttet mot egenkapitalen som to linjer i egenkapitalen:
  *Tidligere års resultat, ikke avsluttet* og *Årets resultat, ikke avsluttet*. Hver konto
  står på siden til sin type; en utenfor de vanlige nummergruppene står under *Andre
  eiendeler*, *Annen gjeld* eller *Andre inntekter og kostnader*, så balansen alltid går
  opp.
- **Merverdiavgiftsspesifikasjon** — per mva-kode grunnlaget, utgående mva, beregnet mva,
  fradrag, tap på krav og mva som ikke er fradragsført, kodene meldingen ikke tar med for
  seg, og bevegelsen på mva-kontoene ved siden av. En kode lenker til posteringene sine.

Perioden starter første dag i inneværende regnskapsår. Velg rapport, periode og, for
**Kontospesifikasjon**, kontoen; med Prosjekter slått på begrenser **Prosjekt** rapporten
til det prosjektets posteringer. En bokføringsspesifikasjon eller en konto med mer enn
50 000 posteringer i perioden vises ikke avkortet: velg en kortere periode. **Last ned CSV** gir
filen et regneark åpner direkte: semikolon og desimalkomma. Å laste ned krever **Eksportere
regnskapet** (`accounting:export`), fordi filene har kundenes navn.

## Filen en kontroll ber om (SAF-T)

Når Skatteetaten ber om regnskapet ved et bokettersyn, ber den om en **SAF-T
Financial**-fil: alle bilag i perioden med linjene, kontoplanen med hver kontos saldoer,
kundene, leverandørene og mva-kodene. Vantigo skriver versjon 1.40, den som kreves for
perioder fra 2027. Eksport krever **Eksportere regnskapet** (`accounting:export`), siden
filen navngir kunder og leverandører — personer blant dem.

1. Åpne **Regnskap**, **SAF-T**.
2. Velg **Regnskapsår**, og for å sende en del av det, **Fra periode** og **Til periode**.
   Skatteetaten ber som regel om et helt år; en fil inneholder høyst ett regnskapsår.
3. Les varslene:
   - *kontoer har ingen grupperingskode* — de eksporteres med koden **NA**. Åpne
     kontoplanen, gi hver sin **Grupperingskode** for året, og kom tilbake.
   - *Selskapets opplysninger mangler* — fyll inn selskapets navn og organisasjonsnummer i
     innstillingene i Fakturaer: filen navngir selskapet med nummeret.
4. Klikk **Eksporter SAF-T**. Filen skrives i bakgrunnen; listen under viser den som *I
   kø*, *Skrives* og så *Ferdig*, med hvem som bestilte den og når. En eksport som feilet,
   sier hvorfor.
5. Klikk filnavnet for å laste den ned: `SAF-T Financial_<organisasjonsnummer>_<dato og
   tid>_1_1.xml`. Et svært stort år — over 2 GB — kommer som flere filer, delt etter
   periode, hver fullstendig for seg: `…_1_2.xml`, `…_2_2.xml`.

**Hvor lenge filene oppbevares.** Filene inneholder personopplysninger, så Vantigo sletter
hver av dem 90 dager etter at den ble skrevet (en administrator kan endre det). Listen
beholder eksporten — hvem, når, perioden og filens kontrollsum — og viser *Slettet etter
oppbevaringstiden*; eksporter på nytt om du trenger filen igjen.

**Å levere den.** Last opp filen i kontrollsaken på skatteetaten.no, slik saksbehandleren
ber om; ikke send den på e-post. For å sjekke en fil først tar Skatteetatens
testinnsending for SAF-T imot den uten å åpne en sak. Hver eksport står i listen med
filens kontrollsum, som dokumentasjon på hva som ble levert.

### Grupperingskoder

Hver konto i en SAF-T-fil har en **grupperingskode** fra Skatteetatens liste for
næringsspesifikasjonen — 1920 bankinnskudd, 3000 salgsinntekt og så videre.
Standardkontoplanen har en på hver konto. En konto du legger til uten, eller en importert
konto med en kode som ikke står på årets liste, eksporteres som *NA*: åpne den i
kontoplanen og velg koden i **Grupperingskode**. Koden lagres per år, så årets liste kan
endre den uten å endre filer for tidligere år.

## Å invitere en regnskapsfører

En regnskapsfører er en vanlig bruker. Inviter vedkommende under
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/) og gi en rolle med
tillatelsene som trengs:

| Tillatelse | Hva den gir |
| --- | --- |
| **Bruke Regnskap** (`accounting:read`) | Alle skjermbilder og rapporter. |
| **Bokføre bilag** (`accounting:post`) | Posteringer, tilbakeføringer, vedlegg, oppgjør, leverandører, leverandørfakturaer og betalingene av dem, handlingene under Trenger oppmerksomhet, og å merke en mva-termin som levert. |
| **Forvalte regnskapet** (`accounting:close`) | Å sette opp regnskapet (også fra en SAF-T-fil), kontoplanen, mva-kodene, posteringsreglene, inngående balanse, sperren, og å lukke og åpne igjen mva-terminer og fjerne et leveringsmerke. Bare en eier kan gi den. |
| **Eksportere regnskapet** (`accounting:export`) | Rapportene som CSV, en leverandørs opplysninger, og SAF-T-filen. |

Når oppdraget er over, fjerner du rollen eller deaktiverer brukeren: alt vedkommende har
bokført, blir stående i regnskapet med navnet sitt.
