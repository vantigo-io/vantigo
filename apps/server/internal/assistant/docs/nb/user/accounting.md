---
title: Regnskap
description: Regnskap-appen — å sette opp regnskapet, kontoplanen med grupperingskoder og systemkontoer, mva-kodene og satsene deres, å bokføre en postering netto eller brutto med mva-linjene, å kopiere et bilag, å legge ved filer, å tilbakeføre et bilag, å sperre en periode og flytte sperren tilbake, inngående balanse og de åpne fakturaene fra Fakturaer, hvordan fakturaer, innbetalinger, purrekostnader og avskrivninger bokføres av seg selv, leverandører og oppslaget i Brønnøysundregisteret, å føre, bokføre og betale en leverandørfaktura, annen valuta, omvendt avgiftsplikt og innførsel av varer, kreditnotaer, Tilbakefør og kopier, hvordan utgifter kommer inn fra Utgifter, posteringsreglene for salg og utgiftskategorier, Trenger oppmerksomhet med Prøv igjen, beløpet i NOK og Lag leverandørfaktura, åpne poster og å registrere en innbetaling, utbetaling eller tilbakebetaling, rapportene og CSV-filene deres, og å invitere en regnskapsfører.
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
av. Mva-melding, SAF-T og bank kommer i de neste utgavene. Reglene bak hvert skjermbilde — hva som avvises og hvorfor, statuskodene
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
| **Rapporter** | Bokføringsspesifikasjon, kontospesifikasjon, kunder, leverandører, saldobalanse, resultatregnskap og balanse. |
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
motkontoen) eller om den tilbys. **Legg til sats** starter en ny sats fra en dato; den
gjeldende satsen slutter dagen før. Et bilag får satsen som gjelder på bilagets egen dato.

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

Mens en måned er sperret, avvises en postering datert i den. Det systemet bokfører av seg
selv for en sperret dag, bokføres på første åpne dag og beholder dokumentdatoen.

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

En leverandør regnskapet nevner — med et bilag eller en faktura — slettes aldri: fjern haken
for **Aktiv** i stedet, så tilbys den ikke lenger for nye fakturaer. En som ennå ikke er i
regnskapet, har **Slett**.

**Leverandøren er en person** merker et enkeltpersonforetak: opplysningene er
personopplysninger. Bokføringen oppbevarer dem — som hvert bilag — i fem år etter utløpet av
regnskapsåret for det siste bilaget, og leverandørens side sier til når. **Eksporter
leverandørens opplysninger** laster ned alt regnskapet har om vedkommende — opplysningene,
fakturaene med linjer og betalinger, og hver postering som nevner dem — for å svare på en
persons innsynsbegjæring. Det krever **Eksportere regnskapet**.

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
| 14, 15 | innførselsmva dere har betalt (en speditørs eller tollvesenets faktura) | mva-en på 2714 |
| 0, 20, 21, 22 | ingen mva | netto alene, med koden |
| 81–89, 91, 92 | ingen mva: omvendt avgiftsplikt | mva-en dere beregner selv, begge sider (se under) |

En kode for utgående mva (3, 31, …) hører til salg og avvises på et innkjøp.

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
kostnaden. Kode 88 og 89 er det samme med lav sats, 91 og 92 klimakvoter og gull. 85 er
innførsel uten mva å beregne.

### Innførsel av varer

Når dere innfører varer, er grunnlaget for mva-en tolldeklarasjonens, ikke leverandørens
faktura: tollverdien, med frakt og toll. Derfor føres de to hver for seg:

1. Leverandørens faktura, som leverandørfaktura, med varelinjen kodet **21** (22 med middels
   sats): kostnaden for innførte varer, uten egen mva.
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
posteringen legger bare til mva-en, skyldig og trukket fra.

### Annen valuta

Velg fakturaens **Valuta** (leverandørens foreslås). Regnskapet føres i NOK, så oppgi enten
kursen — **Etter kurs**, NOK per enhet — eller **Etter sum i NOK**, summen i kroner; skjemaet
viser den andre ved siden av, og ingenting foreslås: ta den fra banken eller tollen. Hver
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

### Å rette en bokført faktura: Tilbakefør og kopier

Et bokført bilag endres aldri. **Tilbakefør og kopier** spør om dato og årsak, bokfører
fakturaens bilag tilbakeført, linje for linje, merker fakturaen **Tilbakeført**, og åpner et
nytt utkast med samme leverandør, nummer, linjer og filer, som du retter og bokfører. En
faktura med en betaling stående tilbakeføres ikke: fjern betalingen først.

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
Trenger oppmerksomhet på beløpet i NOK.

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
- **En leverandørfaktura fra Utgifter.** Klikk **Lag leverandørfaktura**: et utkast åpnes med
  utgiftens beløp, mva-kode, dato, nummer, forfallsdato, prosjekt og kvitteringer, og
  leverandøren — leverandøren med det navnet, eller en ny. Kontroller det, og bokfør det som
  en hvilken som helst leverandørfaktura. Sletter du utkastet, kommer utgiften tilbake på
  listen. Blir utgiften ikke lenger godkjent i Utgifter før det, forsvinner linjen av seg
  selv.

En linje merket **Til orientering** — *Mva ikke trukket fra* — er bokført; den telles ikke med
det som venter. **Kvitter ut** tar den av listen. Skulle mva-en vært trukket fra, tilbakefører
du utgiftens bilag med en postering og fører den på nytt med riktig kode.

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

Perioden starter første dag i inneværende regnskapsår. Velg rapport, periode og, for
**Kontospesifikasjon**, kontoen; med Prosjekter slått på begrenser **Prosjekt** rapporten
til det prosjektets posteringer. En bokføringsspesifikasjon eller en konto med mer enn
50 000 posteringer i perioden vises ikke avkortet: velg en kortere periode. **Last ned CSV** gir
filen et regneark åpner direkte: semikolon og desimalkomma. Å laste ned krever **Eksportere
regnskapet** (`accounting:export`), fordi filene har kundenes navn.

## Å invitere en regnskapsfører

En regnskapsfører er en vanlig bruker. Inviter vedkommende under
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/) og gi en rolle med
tillatelsene som trengs:

| Tillatelse | Hva den gir |
| --- | --- |
| **Bruke Regnskap** (`accounting:read`) | Alle skjermbilder og rapporter. |
| **Bokføre bilag** (`accounting:post`) | Posteringer, tilbakeføringer, vedlegg, oppgjør, leverandører, leverandørfakturaer og betalingene av dem, og handlingene under Trenger oppmerksomhet. |
| **Forvalte regnskapet** (`accounting:close`) | Å sette opp regnskapet, kontoplanen, mva-kodene, posteringsreglene, inngående balanse og sperren. Bare en eier kan gi den. |
| **Eksportere regnskapet** (`accounting:export`) | Rapportene som CSV, og en leverandørs opplysninger. |

Når oppdraget er over, fjerner du rollen eller deaktiverer brukeren: alt vedkommende har
bokført, blir stående i regnskapet med navnet sitt.
