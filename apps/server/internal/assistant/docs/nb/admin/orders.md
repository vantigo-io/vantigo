---
title: Ordre
description: Slå på Ordre — modulene den krever, koblingen av avgiftskategoriene i Produkter til mva-koder, de ni rettighetene og rollene du bygger av dem — og hold innstillingene — nummerseriene, tilbudenes gyldighet, betalingsbetingelser, avsenderkanalen, ordrebekreftelse ved bekreftelse, e-posttekstene, et abonnements siste periode, standard lagersted, fraktproduktet, påslaget og sendingsvarselet — Lagers del i reservasjoner og leveranser, tilbudsmalene og salgsadvarslene, hvem som ser salgsanalysen, hvem som holder abonnementene, og hvem som fakturerer ordre og abonnement; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 48
sources:
  - apps/server/internal/orders/module.go
  - apps/server/internal/orders/settings.go
  - apps/server/internal/orders/settingsrow.go
  - apps/server/internal/orders/mailtext.go
  - apps/orders/frontend/src/pages/settings.tsx
  - apps/orders/frontend/src/pages/templates.tsx
  - apps/orders/frontend/src/components/warnings-card.tsx
  - apps/orders/frontend/src/pages/analysis.tsx
---

Ordre-modulen (**Orders** på engelsk) er der virksomheten lager tilbud til kundene sine og
tar imot ordrene deres. Denne siden handler om å slå den på og holde innstillingene; hvordan
den brukes, står i [Ordre](/nb/user/orders/), og hver regel i
[referansen](/en/reference/orders/).

## Før du starter

- **Produkter, Kunder og Fakturaer** må være slått på: linjene på en ordre er katalogens
  produkter, dokumentene tilhører en kunde, og mva-kodene og selgeren som skrives på hver
  PDF, er Fakturaers.
- **Selgeropplysningene i Fakturaer** — firmanavn, organisasjonsnummer og adresse —
  skrives på hvert tilbud og hver ordrebekreftelse. Fyll dem ut i **Fakturainnstillinger**
  først.
- **Kommunikasjon** er valgfri. Uten den, eller uten en kanal å sende fra, lastes tilbud og
  ordrebekreftelser ned og leveres på annen måte.
- **Lager** er valgfri. Når den er på, har en ordre et **lagersted** den leveres fra,
  reserverer varene der når den bekreftes, og leveres med leveranser som trekker varene fra
  lagerstedets lokasjoner. Uten Lager reserveres ingenting, og hver linje leveres for hånd. Å
  slå av Lager senere etterlater ingenting: det ordrene har reservert, frigjøres fortsatt når de
  kanselleres, endres eller leveres for hånd. Et lagersted leverer ordrer når **Leverer ordrer**
  er satt på det i Lager — et lager gjør det om ikke endret, en butikk gjør det ikke.

## Slå på modulen

1. Når `MODULES` ikke er satt, er alle moduler på, også Ordre. Når `MODULES` er satt, list
   `orders` ved siden av `products`, `customers` og `invoices`, for eksempel
   `MODULES=customers,products,invoices,orders` ([Konfigurasjon](/nb/admin/configuration/)).
   Mangler én av de tre, nekter serveren å starte og navngir den — `orders requires
   invoices`.
2. Start serveren på nytt. Skjemaet `orders` migreres uansett hva `MODULES` sier, så å slå
   den på senere krever ingen migrering; en databaserolle må ha tilgang til skjemaet
   ([Databaseroller](/nb/admin/installation/#databaseroller)).
3. Flisen **Ordre** vises for alle med `orders:access`, og kundesiden får fanen **Ordre**.

## Koble avgiftskategoriene i Produkter til mva-koder

Hver produktlinje får mva-koden produktets avgiftskategori er koblet til i Fakturaer. Et
tilbud med en linje der kategorien ikke er koblet, kan ikke sendes, og en ordre med en slik
linje kan ikke bekreftes («Et produkts avgiftskategori har ingen mva-kode»). Koble hver
avgiftskategori dere selger i, under Fakturainnstillinger → **Mva-koder** →
**Avgiftskategoriene i Produkter**
([Avgiftskategoriene i Produkter](/nb/user/invoices/#avgiftskategoriene-i-produkter)) før
noen begynner å lage tilbud. En fritekstlinje får standard mva-kode med høyest sats, med
mindre personen velger en annen.

## Gi folk tilgang

Ingen andre enn eieren kan bruke appen før en rolle gir rettighetene, under
**Administrasjon av arbeidsområdet** → **Roller og tilgang**
([Roller](/nb/user/workspace-administration/#roller)). Hver rettighet i Ordre krever
**Bruke Ordre** (`orders:access`) i tillegg.

| Rettighet | Nøkkel | Gir |
| --- | --- | --- |
| Bruke Ordre | `orders:access` | Appen, og å lese tilbud, ordrer, abonnement og maler |
| Håndtere tilbud | `orders:quotes-manage` | Lage, sende, besvare og kopiere tilbud |
| Håndtere ordrer | `orders:sales-manage` | Lage og endre ordrer, sette på vent, kansellere, registrere levert, sende ordrebekreftelsen |
| Bekrefte ordrer | `orders:sales-confirm` | Bekrefte, låse og låse opp; akseptere et tilbud rett til en bekreftet ordre |
| Håndtere leveranser | `orders:deliveries-manage` | Lage, plukke, bokføre og kansellere leveranser; å lese dem krever bare Bruke Ordre |
| Håndtere abonnementer | `orders:subscriptions-manage` | Lage, aktivere, endre, pause, gjenoppta, avslutte og kansellere abonnement; akseptere et tilbud som abonnement |
| Se salgsanalyse | `orders:analysis-view` | **Analyse**: treffprosenten på tilbud, ordreinngangen og ordrereserven, med CSV-filene |
| Håndtere innstillinger for Ordre | `orders:settings` | Innstillingene, tilbudsmalene og salgsadvarslene, og overstyring av en sperre; kan ikke delegeres |

**Håndtere fraktselskaper** (sensitiv, kan ikke delegeres) er allerede listet og gjør
ingenting ennå: den hører til leveransen som bringer frakt. Den som pakker og sender, får
**Håndtere leveranser** sammen med Bruke Ordre; **Reserver nå** på en ordre krever **Håndtere
ordrer**.

Den som holder kundenes abonnement — supportavtaler, drift, lisenser — får **Håndtere
abonnementer**; listen **Abonnement** og hvert abonnements side er åpne for alle med
**Bruke Ordre**. Et abonnements perioder faktureres som ordre, med veiviseren **Fra ordre**
i Fakturaer og fakturakjøringer av ordre, med rettighetene i Fakturaer.

**Se salgsanalyse** viser tallene til alle selgere og ordreinngangen fra alle kunder, så gi
den til salgsledelsen heller enn til hver selger.

Et vanlig oppsett: selgere får Bruke Ordre, Håndtere tilbud og Håndtere ordrer; den som
godkjenner ordrer, får i tillegg Bekrefte ordrer; den som driver salgsadministrasjonen, får
i tillegg Håndtere innstillinger for Ordre. Selgere trenger også **Se kunder** i Kunder for
å velge kunde, **Se kontakter** og **Se kundetilknytninger** for å velge kontaktperson, og
**Bruke Fakturaer** for å velge mva-kode på en fritekstlinje; å velge et produkt krever
ingen rettighet i Produkter.

**Å fakturere en ordre** er Fakturaers arbeid, ikke en rettighet i Ordre: knappen
**Fakturer** på ordren og veiviseren **Fra ordre** krever **Bruke Fakturaer** og **Lage
fakturaer**, og å utstede utkastet **Utstede fakturaer** — et abonnements perioder det
samme. Den som fakturerer, trenger ingen rettighet i Ordre for det; kortet **Fakturaer** på ordren lenker til fakturaene for dem
som har **Bruke Fakturaer**. **Frigjør fra fakturautkast** på en ordre krever **Håndtere
ordrer**.

## Hold innstillingene

**Ordreinnstillinger** (`orders:settings`) har:

- **Nummerserier** — **Tilbud starter på** og **Ordrer starter på**, hver med neste nummer.
  Et nummer tas når et dokument opprettes, så et slettet utkast etterlater et hull, og det
  er tillatt: verken tilbud eller ordrer er salgsdokumenter. En serie kan bare starte over
  det høyeste nummeret som er brukt; sett starten før det første dokumentet hvis dere
  fortsetter en serie fra et tidligere system.
- **Standardverdier** — **Tilbud er gyldige i** (dager, 30 om ikke endret; en mal kan si
  noe annet), **Betalingsbetingelser** (dager, 14 om ikke endret, brukt når kunden ikke har
  egne) og **Samle ordrer på én faktura som standard**, som ikke har noen virkning ennå:
  veiviseren **Fra ordre** i Fakturaer starter med én faktura per ordre, og en
  fakturakjøring av ordre samler slik kjøringen sier. **Siste periode ved avslutning** er
  det et nytt abonnement får for perioden sluttdatoen faller i, når sluttdatoen ikke er
  periodens siste dag: **Full periode** (om ikke endret) fakturerer hele perioden,
  **Forholdsmessig til sluttdato** fakturerer den etter dagene fram til sluttdatoen. Hvert
  abonnement har sin egen, som kan endres på abonnementet og når det avsluttes; å endre
  standarden endrer ingen eksisterende abonnement.
- **Standard lagersted** — vises når Lager fører beholdning: lagerstedet en ny ordre leveres
  fra. Det må være et aktivt lagersted som leverer ordrer. Hver ordre har sitt eget, som kan
  endres på ordren; å endre standarden endrer ingen eksisterende ordre.
- **Utsending** — **Avsenderkanal**: postkassen i Kommunikasjon som tilbud og
  ordrebekreftelser sendes fra, eller **Ingen — ingenting sendes**. **Send
  ordrebekreftelsen når en ordre bekreftes** sender den av seg selv til ordrens
  kontaktperson, ellers til kundens faktura-e-post; en ordre uten noen av dem sendes ikke.
  E-posten har den lagrede ordrebekreftelsen for versjonen ordren ble bekreftet i; endres
  ordren igjen før e-posten går, sendes gjeldende versjon i stedet, og **Dokumenter og
  utsendelser** på ordren viser hvilken. En ordre som kanselleres før bekreftelsen er
  sendt, får ingen.
- **Frakt** — **Fraktprodukt**: et tjenesteprodukt som en fraktlinje tar navn, pris og
  mva-kode fra, eller ingen for en fritekstlinje; **Påslag på transportørens estimat** i
  prosent (0 om ikke endret), som **Estimer** på en fraktlinje legger til; og **Send kunden
  e-post når frakten er bestilt** (på om ikke endret), som sender sendingsvarselet med
  sporingslenken på avsenderkanalen. Selve fraktkontoene ligger under Administrer
  arbeidsområde › Integrasjoner › [Frakt](/nb/admin/shipping/).
- **E-posttekster** — et emne og en tekst per type (**Tilbud**, **Ordrebekreftelse**,
  **Sendingsvarsel**) og språk (norsk, engelsk). La en stå tom for å bruke den innebygde
  teksten. En tekst kan bruke `{number}`, `{seller}`, `{total}` og `{validUntil}` —
  sendingsvarselet `{number}`, `{seller}`, `{carrier}`, `{consignment}` og `{trackingUrl}` —
  som fylles inn; personens egen melding fra sendedialogen legges til før hilsenen.

Velg **Lagre**. Har noen andre lagret innstillingene siden du åpnet dem, blir du bedt om å
laste inn på nytt.

## Hold tilbudsmalene

**Tilbudsmaler** (`orders:settings`) har ferdige tilbud å starte fra: **Ny mal**, et
**Navn** (unikt), **Gyldig i** dager (tomt bruker innstillingen), innlednings- og
avslutningstekst, om rabatten vises, og linjer — produkter med antall, en valgfri pris
(eksklusiv mva) og valgfri-merket, seksjoner og merknader. En mal kan arkiveres (**Vis
arkiverte** viser dem) eller slettes; tilbud som allerede er laget fra den, beholder
linjene sine. En mallinje der produktet senere er utgått eller slettet, utelates når malen
brukes, og personen får vite hvilke.

## Hold salgsadvarslene ved like

**Ordreinnstillinger** → **Salgsadvarsler** (`orders:settings`) viser alle advarsler på en
kunde eller et produkt, med nivå, tekst og hvem som endret dem sist.

1. Velg **Legg til kundeadvarsel** og velg kunden, eller **Legg til produktadvarsel** og
   søk opp produktet med en av variantene. Å velge en kunde krever **Se kunder**.
2. Velg nivå: **Informasjon** vises bare på kundens eller produktets tilbud og ordrer;
   **Sperre** stopper også sending, markering som sendt, bekreftelse og aksept av et tilbud
   til en bekreftet ordre.
3. Skriv **Tekst** (høyst 500 tegn) og velg **Lagre advarselen**.

**Endre** endrer en advarsel eller, med **Fjern advarselen**, fjerner den. En advarsel kan
også settes fra et tilbud eller en ordre (**Legg til kundeadvarsel**).

Bare den som har **Håndtere innstillinger for Ordre**, kan overstyre en sperre, og må oppgi
en begrunnelse, som lagres i ordrens endringslogg eller øverst i tilbudets interne notat. En
advarsel følger kunden når kunder slås sammen — den gjenværende kundens egen advarsel vinner
hvis begge har en, bortsett fra at en sperre på kunden som slås inn gjør den gjenværende
kundens informasjonsadvarsel til en sperre med begge tekstene — og slettes når kunden
slettes. En flyttet advarsel vises med kundenummeret til den lagres igjen.

En produktadvarsel overlever et slettet produkt: listen merker den **Produktet er slettet**,
og **Fjern** er alt den tilbyr.

## Kontroller

1. Som selger åpner du **Ordre** → **Tilbud** → **Nytt tilbud**, velger en kunde, legger til
   et katalogprodukt og velger **Lagre**: linjen har en pris, og summene vises.
2. **PDF** → **Forhåndsvis PDF-en** viser firmaets opplysninger og linjen.
3. Med en avsenderkanal valgt **Send** du tilbudet til deg selv; det kommer fram, og
   tilbudet er **Sendt**. Uten kanal fungerer **Marker som sendt** i stedet.
4. **Aksepter** det til en ordre og **Bekreft** ordren: den er **Bekreftet**, og kundens
   **Tidslinje** viser tilbudet sendt og akseptert og ordren bekreftet.
5. Sett en **Sperre** på kunden og åpne et nytt ordreutkast for den: det røde banneret
   vises, og **Bekreft** avvises med «En advarsel sperrer dette». Som den som har
   innstillingene, velger du **Overstyr advarselen** med en begrunnelse og bekrefter;
   **Endringslogg** viser **Advarsel overstyrt**.
6. Med **Se salgsanalyse** viser **Analyse** tilbudet under **Treffprosent på tilbud** og
   ordren under **Ordreinngang**.
7. Med **Lage fakturaer** og **Utstede fakturaer** velger du **Fakturer** på den bekreftede
   ordren, **Lag utkastene** og utsteder utkastet i Fakturaer: kortet **Fakturaer** på
   ordren viser fakturaen, og faktureringsstatusen er **Fakturert**.

8. Med Lager på setter du et **Standard lagersted**, tar imot et produkt der i Lager og
   bekrefter en ordre på det: linjen viser **Reservert**. **Lag leveranse**, velg lokasjonen og
   **Bokfør leveranse**: bevegelsene i Lager viser et **Uttak**, og ordren er **Levert**.

## Hva som kan gå galt

| Symptom | Årsak | Løsning |
| --- | --- | --- |
| Serveren nekter å starte: `orders requires …` | `MODULES` lister `orders` uten products, customers eller invoices | Legg til modulen som nevnes |
| «Et produkts avgiftskategori har ingen mva-kode» ved sending eller bekreftelse | En avgiftskategori er ikke koblet i Fakturaer | Koble den under **Avgiftskategoriene i Produkter** |
| «En linje har ingen pris i denne valutaen» | Produktet har ingen listepris i dokumentets valuta | Gi produktet en pris i Produkter, eller skriv en på linjen |
| **Send** er deaktivert: «Utsending er av» | Kommunikasjon er av, eller ingen avsenderkanal er valgt eller kan sende | Slå på Kommunikasjon og velg en kanal; inntil da bruk **Marker som sendt** |
| En sending vises som **Feilet** | E-postleverandøren tok ikke imot e-posten, eller en ordrebekreftelse i kø feilet åtte ganger | Kontroller kanalen i Kommunikasjon; send på nytt fra ordren |
| En ordrebekreftelse i kø **Feilet** med en gang | Sendingen hadde ingen mottaker, eller ordren ble kansellert før den gikk | Ingenting å rette for en kansellert ordre; ellers fyll inn en e-post og send ordrebekreftelsen fra ordren |
| En automatisk ordrebekreftelse ble aldri sendt | Ordren har ingen kontakt-e-post og kunden ingen faktura-e-post | Fyll inn en og send ordrebekreftelsen fra ordren |
| «En serie kan bare starte over det høyeste nummeret som er brukt» | Den nye starten er under neste nummer | Velg en start på eller over neste nummer som vises |
| Tilbudsmaler og Ordreinnstillinger mangler i sidemenyen | Personen mangler Håndtere innstillinger for Ordre | Gi rettigheten |
| Analyse mangler i sidemenyen | Personen mangler Se salgsanalyse | Gi den sammen med Bruke Ordre |
| **Fakturer** mangler på en bekreftet ordre | Personen mangler Bruke Fakturaer eller Lage fakturaer, eller Fakturaer er slått av | Gi begge i Fakturaer |
| «Deler av ordren står på et fakturautkast» ved kansellering | Et fakturautkast holder deler av ordren | Utsted eller slett utkastet i Fakturaer, eller velg **Frigjør fra fakturautkast** på ordren |
| Utstedelsen av en faktura avvises: «Arbeid på linje … kan ikke lenger faktureres» | Det fakturautkastet holdt av ordren, ble frigjort på ordren, eller ordren ble satt på vent | Slett utkastet og fakturer ordren på nytt |
| En ordre er grået ut i **Fakturer fra ordre**: «i en annen valuta enn fakturaene lages i» | Ordrens valuta er ikke standardvalutaen i fakturainnstillingene (NOK) | Fakturer den utenfor Vantigo, eller endre ordrens valuta mens den fortsatt er et utkast |
| «En advarsel sperrer dette», og ingen **Overstyr advarselen** | En sperre gjelder, og personen mangler Håndtere innstillinger for Ordre | Be noen med innstillingene overstyre den, eller endre advarselen |
| Ingen **Lagersted** på ordrene, ingen **Standard lagersted** i innstillingene | Lager er av | Slå på Lager, eller lever for hånd |
| «Lagerstedet er arkivert eller leverer ikke ordrer.» når standard lagersted lagres | Lagerstedet er en butikk, er arkivert, eller **Leverer ordrer** er av | Sett **Leverer ordrer** på lagerstedet i Lager, eller velg et annet |
| «Det er ikke nok disponibelt på lagerstedet» ved bekreftelse | Andre ordrer holder varene, eller de er ikke tatt imot ennå | Ta imot varene, eller **Bekreft uten å reservere** og **Reserver nå** senere |
| «Lokasjonen har ikke så mye» når en leveranse bokføres | Varer er flyttet, korrigert eller tatt ut siden leveransen ble plukket | Velg en annen lokasjon, eller korriger beholdningen i Lager |
| **Leveranser** har ingenting å lage | Ordren er på vent, har ikke lagersted, eller varene er levert eller ligger i et leveranseutkast | Frigi den, sett et lagersted, eller gjør ferdig leveranseutkastet |
| «Fraktproduktet må være et tjenesteprodukt» | Produktet som er valgt som fraktprodukt, er en vare | Lag et tjenesteprodukt for frakt i Produkter, og velg det |
| **Estimer** sier «Ingen fraktkonto å bruke» | Ingen fraktkonto er satt opp, eller ingen er lagerstedets standard mens det finnes flere | Sett opp en, og lagerstedets standard, under Integrasjoner › [Frakt](/nb/admin/shipping/) |
| Kunden får ikke noe sendingsvarsel | **Send kunden e-post når frakten er bestilt** er av, ingen avsenderkanal er valgt, eller sendingen og ordren har ingen e-post | Slå det på, velg en kanal, og fyll inn mottakerens e-post før bestilling |
