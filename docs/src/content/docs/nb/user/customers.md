---
title: Kunder
description: Kundekort, kontakter, oppfølginger og kunde 360-visningen.
sidebar:
  order: 10
sources:
  - apps/customers/frontend
---

Appen **Kunder** holder orden på selskapene og personene du gjør forretninger med,
menneskene du snakker med hos hver av dem, og det som har skjedd mellom dere.
Sidemenyen har tre områder: **Kunder** (krever `customers:view`), **Kontakter** (krever
`customers:contacts-view` eller `customers:associations-view`) og **Oppfølginger**
(krever `customers:timeline-view`). Hva hvert skjermbilde lar deg endre, avhenger av
flere rettigheter, nevnt ved oppgaven de gjelder og samlet under [Rettigheter](#rettigheter).

## Finne en kunde

**Kunder** lister alle kundene i en tabell, med kort over den: **Kunder totalt**,
**Aktive** og **Nye siste 30 dager**, og — med `customers:legal-identity-view` —
**Virksomheter**, **Privatpersoner**, **Mangler juridisk identitet** og **Land**.

- **Søket** treffer navnet, kundenummeret og kundens egen e-post og telefon. Med
  `customers:legal-identity-view` treffer det også juridisk navn og
  organisasjonsnummer; med både `customers:contacts-view` og
  `customers:associations-view` treffer det også navnet og e-posten til en tilknyttet
  kontakt. Se [listeendepunktet og søk](/en/reference/customers/#the-list-endpoint-and-search).
- **Status** står på **Alle åpne** som standard: alle aktive og deaktiverte kunder.
  Velg **Arkivert** for å se arkiverte kunder; ellers er de skjult.
- **Type** avgrenser til **Bedrift** eller **Privat**; **Eier** til **Mine** eller
  **Uten eier**; **Merkelapp** til én merkelapp; **Gruppe** til én gruppe eller
  **Ingen gruppe**.
- Klikk på overskriften **Nummer**, **Navn** eller **Opprettet** for å sortere etter
  den: én gang stigende, én gang til synkende, en tredje gang for å slå sorteringen av.

Alle filtrene og sorteringen ligger i sideadressen, så en filtrert liste kan bokmerkes
eller sendes til en kollega. Klikk på en rad for å åpne kunden; blyanten ytterst på
raden åpner redigeringsskjemaet direkte. **Fant ingen kunder.** betyr at ingenting traff.

## Opprette en kunde

Trykk **Opprett ny kunde** (`customers:create`; også en hurtighandling i søket).
Skjemaet spør om:

- **Kundetype**: **Bedrift** eller **Privat**. Den kan ikke endres senere i dette
  skjemaet — se [Endre type](#endre-type).
- **Navn**. For en bedrift søker du i Brønnøysundregistrene etter selskapsnavn eller
  organisasjonsnummer mens du skriver; velger du et treff, får kunden det juridiske
  navnet og organisasjonsnummeret som juridisk identitet, hentet fra
  Brønnøysundregistrene. Skriver du videre i stedet, lagres navnet alene. Oppslaget
  krever `customers:lookup-view`, og å lagre en identitet `customers:legal-identity-manage`;
  skjemaet sier det under feltet. For en privatperson er det personens fulle navn.
- **E-post** og **Telefon**, kundens egne; redigeres etterpå fra kundesiden.
- **Status**: **Aktiv**, **Deaktivert** eller **Arkivert**.

Mens du skriver, viser **Eksisterende kunder med lignende navn** opptil tre kunder med
navn som inneholder det du skrev — et hint om å se etter før du oppretter et duplikat.

Tilhører organisasjonsnummeret allerede en annen kunde, avvises lagringen med
**Juridisk identitet er allerede i bruk**, som navngir kundene som har den (når du
har `customers:view`). Åpne en av dem i stedet, eller trykk **Opprett likevel** når to
kunder virkelig deler en identitet — to avdelinger i samme selskap, for eksempel. Med
`customers:merge` peker advarselen også på **Slå sammen…** som måten å samle to
oppføringer på. Regelen er
[duplikatvernet for juridisk identitet](/en/reference/customers/#the-duplicate-identity-guard).

## Kundesiden

Toppen viser navnet, den juridiske identiteten som merker (juridisk navn,
organisasjonsnummer, land, type og kilde — eller *Juridisk identitet er ikke
tilgjengelig for denne kontoen* uten `customers:legal-identity-view`), statusen,
typen og kundenummeret. Ved siden av står **Rediger kunde**, **Endre type**, **Slå
sammen…**, **Arkiver kunde** eller **Gjenopprett kunde**, **Personopplysninger** (bare
privatpersoner) og **Åpne i innboksen**, hver vist bare med rettigheten den krever.

En rad med faner — **Oversikt**, **Energi**, **Prosjekter** og **Fakturaer** — vises
når mer enn én er tilgjengelig for deg. De tre modulfanene viser modulens eget panel
for denne kunden og krever at modulen er på og en rettighet i den
(`energy:metering-points-view`, `projects:access`, `invoices:access`).

**Oversikt** åpner med panelet **Kunde 360**: **Åpne prosjekter**, **Ufakturerte
timer**, **Utlegg klare til fakturering** og **Siste aktivitet** — hver flis vises
bare når modulen er på og for deg — pluss en tabell over åpne prosjekter (**Kode**,
**Navn**, **Status**, **Sist ført**) og **Se alle … åpne prosjekter** når det finnes
flere; se [Customer 360](/en/reference/customers/#customer-360). Under det kommer
kortene **Kunderelasjon**, **Kontakt og adresser**, **Register**, **Fakturering**,
**Kontakter** og **Tidslinje**, beskrevet i avsnittene som følger.

Et banner øverst sier fra når kunden er arkivert, slått sammen med en annen, planlagt
anonymisert eller anonymisert; en sammenslått eller anonymisert kunde er
skrivebeskyttet, og alle redigeringshandlinger forsvinner fra siden.

## Redigere, arkivere og gjenopprette

**Rediger kunde** (`customers:update`) åpner opprettelsesskjemaet redusert til **Navn**
og **Status**. **Deaktivert** beholder kunden i listene, men sperrer den for
fakturering — se [Statuses](/en/reference/customers/#statuses). Advarselen om duplikat
identitet gjelder her også, med **Lagre likevel** som overstyring.

Har noen andre lagret kunden mens skjemaet ditt var åpent, avvises lagringen med
**Kunden er endret**: trykk **Last inn på nytt** for å hente deres versjon — det du
skrev, forkastes — og lagre igjen. Det samme varselet dukker opp i alle skjemaer som
redigerer kunderaden (kontaktdetaljer, eier, gruppe, fakturering).

**Arkiver kunde** (`customers:delete`) spør *Arkivere {navn}? Kunden beholdes, men
skjules fra de fleste lister til den gjenopprettes.* En arkivert kunde nås fortsatt
fra andre moduler og nektes ny faktura. **Gjenopprett kunde** (`customers:update`)
setter den aktiv igjen uten bekreftelse.

## Endre type

**Endre type** (`customers:update`) gjør en bedrift om til en privatperson eller
omvendt, gjennom en bekreftelse som forklarer hva som skjer: kontakter, tidslinjen
og alt annet knyttet til kunden beholdes; en juridisk identitet fjernes, siden den
tilhører den gamle typen; skjemaer og nøkkeltall behandler kunden som den nye typen
fra nå av. Bekreft med **Endre til privat** eller **Endre til bedrift**. Regelen er
[Customer type vs. legal identity type](/en/reference/customers/#customer-type-vs-legal-identity-type).

## Eier, gruppe og merkelapper

Kortet **Kunderelasjon** viser hvem som eier relasjonen, hvilken gruppe kunden er i og
hvilke merkelapper den har. Med `customers:update` har hver av dem en kontroll:

- **Eier**: søk etter en kollega og velg vedkommende, eller **Fjern eier**.
- **Gruppe**: en av installasjonens grupper, eller **Ingen gruppe**. En gruppe kan ha
  standard betalingsbetingelser, som faktureringskortet viser at kunden arver.
- **Merkelapper**: velg blant eksisterende, eller skriv et nytt navn og velg
  **Opprett «…»** for å opprette og sette den på i ett.

Ordforrådene administreres fra kundelisten (`customers:update`). **Administrer
merkelapper** ved merkelappfilteret gir en merkelapp nytt navn, en **Farge** eller
sletter den, som fjerner den fra alle kundene som hadde den. **Administrer grupper**
ved gruppefilteret oppretter en gruppe med **Navn på gruppe** og valgfrie **Standard
betalingsbetingelser (dager)**, redigerer eller sletter den — avvist så lenge noen
kunde er i den. Reglene er
[Owner and tags](/en/reference/customers/#owner-and-tags) og
[Groups](/en/reference/customers/#groups).

## Kontaktdetaljer og adresser

**Kontakt og adresser** viser kundens egen **E-post**, **Telefon** og **Nettside**;
**Rediger kontaktdetaljer** (`customers:update`) endrer dem.

Adressene listes under etter type — **Fakturaadresse**, **Postadresse**,
**Leveringsadresse**, **Besøksadresse** — og den primære av hver type har merket
**Primær**. **Legg til adresse** spør om **Adressetype**, en valgfri **Merkelapp**,
**Adresselinje 1** og **2**, **Postnummer**, **Poststed**, **Region** og **Land**; en
norsk adresse trenger et firesifret postnummer og et poststed. Kryss av
**Primæradresse** for å gjøre den til primær av sin type. Den første adressen av en
type er primær uansett, og du kan ikke fjerne krysset på den eneste eller primære —
trykk **Gjør til primær** på en annen rad i stedet. **Slett adresse** spør først og
kan ikke angres.

Når registerkortet har opplysninger og kunden ennå ikke har en adresse av den typen,
åpner **Bruk forretningsadressen fra registeret** og **Bruk postadressen fra
registeret** skjemaet ferdig utfylt fra Brønnøysundregistrene, som besøks- eller
postadresse; ingenting lagres uten et klikk. Se [Addresses](/en/reference/customers/#addresses).

## Registerkortet

For en bedriftskunde med norsk organisasjonsnummer som juridisk identitet, og med
`customers:legal-identity-view`, viser kortet **Register** hva Brønnøysundregistrene
sier: **Organisasjonsform**, **Næringskode**, **Ansatte**, **Registrert i
Merverdiavgiftsregisteret**, **Stiftet**, kontaktopplysninger, **Overordnet enhet**,
**Forretningsadresse** og **Postadresse**, og når opplysningene ble hentet. Røde merker
flagger et selskap som er **Konkurs**, **Under avvikling**, **Under tvangsavvikling**
eller **Slettet**.

**Oppdater** (`customers:legal-identity-manage`) leser registeret på nytt; hver
forskjell skrives til tidslinjen som hendelsen **Registerendring**, og kortet sier
hvor mange endringer du bør se etter. Kaller registeret selskapet noe annet, tilbyr
**Registeret har et annet navn** valget **Oppdater juridisk navn**. Se
[Registry record](/en/reference/customers/#registry-record).

## Fakturering

Kortet **Fakturering** viser faktureringsprofilen: **E-post for faktura**, **E-post
for purring**, **Betalingsbetingelser**, **Valuta**, **Standard timepris**,
**Dokumentspråk**, **Fakturalevering**, **Purrelevering**, **Peppol-ID**, **GLN** og
**Deres referanse**. Et tomt felt viser *Ikke satt — standard for fakturering
gjelder*, med et hint om hva som brukes i stedet (kundens egen e-post, gruppens
betalingsbetingelser, en EHF-mottaker utledet fra organisasjonsnummeret). **Før du
fakturerer denne kunden** lister det som ville stoppet en faktura: EHF uten Peppol-ID
eller organisasjonsnummer, e-post uten noen adresse, eFaktura på en bedrift, ingen
fakturaadresse.

Redigering krever `customers:billing-manage`. Leveringsmåtene er **E-post**, **EHF**,
**eFaktura** (bare privatkunder) og **Papir**. **Sjekk EHF** spør Peppol-nettverket om
kunden kan motta EHF-fakturaer og beholder svaret med dato; kan den det, og EHF ennå
ikke er valgt, tilbyr **Denne kunden kan motta EHF-fakturaer** valget **Bruk EHF**. Se
[Billing profile](/en/reference/customers/#billing-profile) og
[Peppol lookup](/en/reference/customers/#peppol-lookup).

## Kontakter og rollene deres

En kontakt er en person. Kundens kort **Kontakter** lister menneskene som er knyttet
til den, med **Roller**, **E-post** og **Telefon** — en nedtonet verdi er kontaktens
egen, arvet fordi ingen er satt for denne kunden.

**Legg til kontakt** søker blant eksisterende kontakter etter navn, telefon eller
e-post. Velg en, eller velg **Fant ingen kontakt — opprett "…" som ny kontakt** og fyll
inn **Fornavn** og **Etternavn** (pluss **Mellomnavn**, **Prefiks**, **Suffiks**,
**Telefon**, **E-post**). Beskriv deretter koblingen:

- **Tittel**, for eksempel *CEO*.
- **Roller**: **Faktura** (hvem som får fakturaen og purringen), **Prosjekt** (hvem
  dere snakker med til daglig) og **Beslutningstaker** (hvem som godkjenner), hver med
  en **Primær**-bryter. Den første kontakten som får en rolle, er primær for den; slår
  du **Primær** på for en annen kontakt, flyttes den. Den kan ikke slås av på den
  primære innehaveren — gjør en annen kontakt primær i stedet.
- **Telefon hos denne kunden** og **E-post hos denne kunden**, brukt for denne kunden
  i stedet for kontaktens egne.

En kobling trenger en tittel eller minst én rolle. Blyanten på en rad åpner
**Rediger kobling — {navn}**; fjern-ikonet kobler kontakten fra etter en bekreftelse,
og begge beholdes. Se
[Contacts and associations](/en/reference/customers/#contacts-and-associations).

**Kontakter** i sidemenyen lister alle kontakter med **Navn**, **Telefon**, **E-post**
og **Kunder**, søkbare etter navn, telefon eller e-post; **Opprett ny kontakt**
oppretter en uten kunde, blyanten redigerer den, søppelkassen sletter den direkte.
Kontaktens egen side har **Rediger kontakt** og et kort **Kunder** som speiler kundens
kontaktkort, med **Legg til kunde**. Kontakter krever `customers:contacts-view` og
`customers:contacts-manage`; koblingene `customers:associations-view` og
`customers:associations-manage`.

## Tidslinjen

Kortet **Tidslinje** er kundens historikk, nyeste først: automatiske hendelser
systemet skriver (kunde opprettet, eier endret, registerendring, sammenslått og så
videre) og manuelle hendelser du legger til. Hver hendelse viser typen, merket
**Manuell** eller **Automatisk**, når den skjedde, hvem som registrerte den, og
detaljene; **Last inn flere** henter eldre hendelser. Lesing krever
`customers:timeline-view`. Avgrens med bryteren **Kilde** (**Alle**, **Manuell**,
**Automatisk**), **Hendelsestyper** og et **Tidspunkt**-intervall; **Nullstill** tømmer dem.

**Legg til hendelse** (`customers:timeline-manage`) registrerer et øyeblikk: **Type**
(**Samtale**, **Møte**, **E-post**, **Notat**, **Annet** eller **Registerendring**),
**Dato** (ikke i fremtiden), valgfri **Tid (UTC)**, en **Beskrivelse**, en valgfri
**Kilde-URL** vist som **Åpne kilde** på hendelsen, og en oppfølging (neste avsnitt).
Menyen på en manuell hendelse tilbyr **Rediger**, **Revisjonshistorikk** og **Slett**.
Alle lagrede versjoner beholdes, og **Revisjonshistorikk** lister dem som **Revisjon
1**, **Revisjon 2** og så videre, med hvem som endret og når. Er en hendelse endret
under deg, avvises lagringen med **Denne hendelsen er endret**. Se [The timeline](/en/reference/customers/#the-timeline).

## Oppfølginger

En oppfølging er en dato på en manuell tidslinjehendelse: det du har lovet å gjøre
videre, og eventuelt hvem som skal gjøre det. I hendelsesskjemaet setter du **Følg
opp** — den kan være fram i tid — og deretter **Tildelt**; den tildelte kan ikke
velges før datoen, og fjerner du datoen, fjernes tildelingen.

Hendelsen viser så **Følg opp {dato}**, den tildelte eller **Ikke tildelt**, og
**forfalt** i rødt når datoen er passert. **Merk som utført** krysser den av, og
deretter står det **Fulgt opp {dato}**; **Åpne igjen** tar det tilbake (begge krever
`customers:timeline-manage`).

**Oppfølginger** i sidemenyen er det du har på bordet på tvers av alle kunder, eldste
frist først, med **Frist**, **Kunde**, **Beskrivelse** og **Tildelt**. **Tildelt**
filtrerer til **Meg** eller **Ikke tildelt**; **Status** til **Åpne**, **Forfalt**,
**Utført** eller **Alle**; arkiverte kunders oppfølginger vises bare under **Utført**
og **Alle**. Med `customers:timeline-manage` har hver åpen rad **Merk som utført**.
Siden krever `customers:view` i tillegg til `customers:timeline-view`, fordi hver rad
navngir en kunde. Se [Follow-ups](/en/reference/customers/#follow-ups).

## Eksport og import

**Eksporter** (`customers:view`) laster ned listen slik du ser den — søk, filtre og
sortering, alle sider — som semikolonseparert CSV. Identitetskolonnene krever
`customers:legal-identity-view`; over 5000 kunder avvises — snevre inn filteret.

**Importer** krever `customers:create`, `customers:update` og `customers:view`, og
åpner **Importer kunder**:

1. **Last ned mal** gir deg overskriftsraden importen din kan skrive. En fil på høyst
   5 MB, i UTF-8, med malens kolonner — eller en eksports, om du kan skrive alle
   kolonnene den har. En rad med kundenummer oppdaterer den kunden; en rad uten
   oppretter en ny kunde.
2. Slipp filen eller **Velg CSV-fil**. Kryss av **Tillat at en kunde har samme
   juridiske identitet som en annen kunde** bare om det er meningen.
3. **Kontroller** kjører importen uten å lagre noe og rapporterer **Rader**, **Ville
   blitt opprettet**, **Ville blitt oppdatert** og **Har feil**, hvert problem etter
   **Rad** og **Kolonne**. Rett filen og kontroller igjen til noe kan importeres.
4. **Importer** lagrer radene og rapporterer **Opprettet**, **Oppdatert** og
   **Feilet**. Feilet noen rader, gir **Last ned radene som feilet** deg akkurat de
   radene med en feilkolonne først, til å rette og importere for seg.

Én import kjører om gangen; en til avvises med **En import kjører allerede**. Filformatet
og alle reglene står i [CSV import and export](/en/reference/customers/#csv-import-and-export).

## Slå sammen duplikater

Når to oppføringer er samme kunde, åpner du den du vil beholde og trykker **Slå
sammen…** (`customers:merge`). I **Slå et duplikat sammen med {navn}** søker du etter
duplikatet etter navn eller nummer. Dialogen forklarer hva som skjer: duplikatets
kontakter, adresser, tidslinje og merkelapper — og det Prosjekter, Energi og
Kommunikasjon har for det — flyttes hit; opplysningene om det blir stående lesbare i
denne kundens tidslinje; og det arkiveres. Denne kunden beholder alle sine egne
opplysninger.

**Slå sammen** holdes tilbake, og dialogen sier hvorfor, når de to er av ulik type
(endre den ene først) eller denne kunden er arkivert (gjenopprett den først);
serveren avviser også et duplikat som allerede er slått sammen. Etterpå lister
dialogen det som ble flyttet. Den sammenslåtte kunden nås fortsatt under sitt gamle
nummer med banneret **Slått sammen inn i #… …**, som lenker til denne kunden; den
kan ikke lenger redigeres eller gjenopprettes. Se
[Merging duplicates](/en/reference/customers/#merging-duplicates).

## Personopplysninger

For en privatperson samler **Personopplysninger** (`customers:personal-data`)
handlingene personvernforordningen ber om:

- **Eksporter personopplysninger** laster ned alt som er lagret om personen som én JSON-fil.
- **Planlegg anonymisering…** velger dagen da personens navn, juridiske identitet,
  kontaktopplysninger, adresser, kontakter, korrespondanse og innholdet i hver
  tidslinjehendelse fjernes; kundenummeret, datoene og hva som skjedde når, beholdes
  for regnskapet. Det finnes ingen standarddato: velg en etter at hver
  oppbevaringsperiode etter bokføringsreglene er over. Kunden må arkiveres først —
  valget er nedtonet med *Arkiver kunden først* til den er det — og en sammenslått
  person planlegges på kunden som tok den opp i seg.
- Når den er planlagt, viser siden **Anonymisering planlagt til {dato}**, og menyen
  tilbyr **Endre anonymiseringsdato…** og **Avbryt anonymisering**; gjenoppretter du
  kunden, avbrytes den også.

En anonymisering kan ikke angres. Etterpå viser siden **Anonymisert {dato}**, ingenting
på den kan endres, og bare eksporten står igjen. Se
[Personal data and anonymisation](/en/reference/customers/#personal-data-and-anonymisation).

## Rettigheter

| For å | Trenger du |
| --- | --- |
| Se listen, en kundeside, faktureringsprofilen og 360-panelet; eksportere | `customers:view` |
| Opprette en kunde | `customers:create` |
| Redigere, gjenopprette, endre type, sette eier, gruppe og merkelapper, administrere merkelapper og grupper, redigere kontaktdetaljer og adresser | `customers:update` |
| Arkivere en kunde | `customers:delete` |
| Se den juridiske identiteten og registerkortet | `customers:legal-identity-view` |
| Lagre en juridisk identitet, oppdatere registeropplysningene, oppdatere juridisk navn | `customers:legal-identity-manage` |
| Slå opp et selskap i Brønnøysundregistrene | `customers:lookup-view` |
| Se kontakter / opprette, redigere og slette dem | `customers:contacts-view` / `customers:contacts-manage` |
| Se en kundes kontakter / koble til, redigere og koble fra | `customers:associations-view` / `customers:associations-manage` |
| Lese tidslinjen og oppfølginger | `customers:timeline-view` |
| Legge til, redigere og slette tidslinjehendelser; sette og krysse av oppfølginger | `customers:timeline-manage` |
| Redigere faktureringsprofilen, sjekke og bruke EHF | `customers:billing-manage` |
| Importere kunder | `customers:create`, `customers:update` og `customers:view` |
| Slå sammen et duplikat | `customers:merge` |
| Eksportere personopplysninger og planlegge en anonymisering | `customers:personal-data` |

Hele listen, med hva hver nøkkel beskytter, står under [Permissions](/en/reference/customers/#permissions).
