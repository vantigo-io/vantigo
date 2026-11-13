---
title: Ordre
description: Ordre-appen — lag et tilbud, fra appen, kundesiden eller en mal; legg til produkt-, fritekst-, seksjons- og merknadslinjer, valgfrie linjer og rabatter; send det eller marker det som sendt, revider, aksepter, avslå, kanseller og kopier det; lag en ordre, bekreft den, endre den etter bekreftelse, sett den på vent, lås den, kanseller den eller resten, registrer hva som er levert, send ordrebekreftelsen og skriv ut en proforma; reserver varer på et lagersted, lag, plukk og bokfør leveranser med plukkliste og pakkseddel, plukk varer som holdes i partier eller serienumre og bytt parti, og følg restordrene; send en leveranse med Bring — kolli, priser, bestilling, etiketter, fraktbrev, sporing og kansellering — og legg til en fraktlinje med transportørens estimat; fakturer den, se fakturaene dens og frigjør det fakturautkast holder; lag et abonnement, aktiver, endre, pause, gjenoppta, avslutt og kanseller det, aksepter et tilbud som abonnement og se periodene; endringsloggen; linjer i forpakninger og fra variantrutenettet; kunde- og produktadvarsler og overstyring av en sperre; salgsanalysen; kundens Ordre-fane; og hva en avvisning betyr.
sidebar:
  order: 55
sources:
  - apps/orders/frontend
  - apps/host/frontend/src/routes/orders
  - apps/host/frontend/src/routes/customers/-customer-orders-tab.tsx
---

I Ordre-appen lager du tilbud til kundene dine og tar imot ordrene deres. Et **tilbud** er
et pristilbud kunden kan akseptere; en **ordre** er det kunden har kjøpt, som du bekrefter,
leverer og fakturerer.

## Appen og sidemenyen

Sidemenyen har **Tilbud**, **Ordre**, **Leveranser** og **Abonnement** for alle med tilgang til appen, **Analyse** for dem
som kan se salgsanalysen, og **Tilbudsmaler** og **Ordreinnstillinger** for dem som
håndterer innstillingene for Ordre. Hva du kan gjøre
i hver, avhenger av rettighetene dine: å lage og endre tilbud, å lage og endre ordrer og å
bekrefte ordrer er tre ulike rettigheter, så en kollega kan se knapper du ikke ser.

**Tilbud** viser tilbudene med de nyeste først, med nummer, kunde, status, selger og sum.
Avgrens med søket (nummer, kunde eller referanse), **Status**, **Kunde**, **Selger** og
**Bare mine**. **Ordre** viser ordrene på samme måte, med **Åpne**, **Alle** eller
**Ferdige** og filtrene **Levering** og **Fakturering**; en ordre er ferdig når den er
bekreftet og hver produktlinje er fakturert eller kansellert fullt ut. En ordre der alt som
gjensto, er kansellert, er ferdig; en som er levert, men ikke fakturert ennå, er fortsatt
åpen.

## Lag et tilbud

1. I **Tilbud** velger du **Nytt tilbud**. Du kan også starte fra **Nytt tilbud** i søket
   (spotlight) eller på kundens fane **Ordre**, som fyller inn kunden.
2. Velg **Kunde**. Valuta, om prisene vises inkludert mva, betalingsbetingelsene og
   **Deres referanse** hentes fra kunden.
3. For å starte fra ferdige tekster og linjer velger du en under **Start fra en mal**.
4. Velg **Opprett tilbudet**. Tilbudet får neste nummer i tilbudsserien og åpnes som
   utkast.

Å velge kunde krever tilgang til kundene; uten den starter du fra kundens side i stedet.
En deaktivert, arkivert eller sammenslått kunde kan ikke få et nytt tilbud.

### Hodet

Så lenge tilbudet er et utkast, kan du endre **Leveringsadresse** (som standard kundens
primære leveringsadresse; alle kundens adresser kan velges, og en som ikke er en
leveringsadresse på kunden — for eksempel en besøks- eller postadresse — godtas med den gule
merknaden *Dette er ikke en leveringsadresse på kunden.*), **Kontaktperson**, **Deres referanse**, **Selger** (**Gjør meg til selger**
setter deg), **Prosjekt**, **Valuta**, **Priser** (**eks. mva** eller **inkl. mva**),
**Betalingsbetingelser**, **Dokumentspråk**, **Gyldig til**, **Leveringsdato** og **Vis
rabatten på PDF-en**, og under **Tekster** **Innledning**, **Avslutning** og et **Internt
notat**, som aldri skrives ut. Et utkast følger kunden: endres kundens adresse eller navn i
Kunder, tas det med ved neste lagring.

### Legg til linjer

Under **Linjer** legger du til:

- **Produkt** — søk i katalogen på varenummer eller navn. Linjen får produktets navn,
  enhet, katalogpris i tilbudets valuta og mva-kode. Har produktet ingen pris i denne
  valutaen, står det **Ingen pris i denne valutaen** på linjen; skriv inn en.
- **Fritekstlinje** — noe som ikke er i katalogen: gi den et navn (**Hva som selges**),
  antall, pris og, hvis det ikke er standardsatsen, en mva-kode.
- **Seksjon** — en overskrift som samler linjene under seg.
- **Merknad** — en tekst som skrives ut mellom linjene.
- **Frakt** (*Legg til frakt*, på et tilbud eller en ordre) — frakten kunden betaler:
  antall 1, en pris og en mva-kode, standardsatsen med mindre du velger en annen.
  **Estimer** spør transportøren om en pris for det lagrede dokumentets leveringsadresse og
  varenes vekt, legger til påslaget fra innstillingene for Ordre og fyller inn prisen, merket
  **Fraktestimat**; når innstillingene har et fraktprodukt, får linjen navnet derfra.
  Mva-koden følger varene: deres når de har samme, ellers den som størstedelen av varenes
  beløp har. Skriv over prisen for å gjøre den til din egen. Estimer krever at dokumentet er
  lagret, at det finnes en fraktkonto (se [Frakt](/nb/admin/shipping/)) og tillatelsen til å
  håndtere tilbudet eller ordren. Linjen regnes som
  levert når ordrens første sending er bestilt, og faktureres som alle andre linjer.

Gi hver produktlinje et **Antall**, og en **Rabatt** i prosent om det er noen. En pris du
skriver inn selv, vises som **Manuell pris** og blir stående slik du skrev den; en
katalogpris oppdateres hvis du endrer valuta eller lagrer på en senere dag. Kryss av for
**Valgfritt** på en linje kunden kan velge å ta med: den skrives ut, merket som valgfri,
og holdes utenfor summene. Flytt linjer med pilene og fjern dem med fjern-knappen.

Med **Priser** satt til **inkl. mva** skriver og ser du prisene inkludert mva; de lagres
uten. Velg **Lagre**: serveren priser linjene og beregner summene — **Netto**, mva per sats
og **Totalt**.

En lagret linje beholder typen sin: for å gjøre en merknad om til en produktlinje, for
eksempel, fjerner du den og legger til en ny linje.

### Selg i forpakninger

Et produkt som selges i forpakninger — en eske à 15, en pall med 40 esker — får en
forpakningsvelger under antallet på linjen når du har valgt det. Velg forpakningen og skriv
hvor mange: **3** av **Eske à 15** lagres som 45 av produktets enhet, og linjen viser
«= 45 stk» mens du skriver. PDF-en skriver det som «3 × Eske à 15 (45 stk)». Velg enheten
igjen for å skrive antallet i produktets egen enhet. Et antall forpakninger som gir et
antall med mer enn tre desimaler, avvises; skriv antallet i enheten i stedet. En lagret
linje beholder forpakningen den ble lagt inn i selv om produktet ikke lenger har den, og
tilbyr produktets nåværende forpakninger ved siden av.

### Legg til fra rutenett

For et produkt med varianter — størrelser og farger, for eksempel — sparer **Legg til fra
rutenett** deg for å skrive én linje per variant:

1. Velg **Legg til fra rutenett** under linjene, og søk opp produktet med en av variantene
   under **Produkt (søk etter en variant)**.
2. Variantene vises i et rutenett, én egenskap nedover og den andre bortover. Skriv antall
   i hver rute du vil ha; en rute med strek er en kombinasjon produktet ikke har.
3. Velg **Legg til linjene**. Hvert antall legger til variantens linje, eller endrer
   antallet på linjen den allerede har.

Et produkt med mer enn to egenskaper, en egenskap med mer enn 30 verdier eller en variant
uten verdi på en av dem vises som en liste i stedet, med antall per variant.

### Når noen andre har endret det

Hver endring du gjør — lagre, sende, akseptere, bekrefte, sette på vent, låse, registrere
levering, slette — kontrolleres mot tilbudet eller ordren slik du åpnet det. Har noen andre
endret det i mellomtiden, selv bare ved å sette det på vent eller låse det, eller kunden er
slått sammen eller slettet, avvises endringen, og redigeringen viser **Endret av noen
andre**: «Dette er endret siden du åpnet det. Last inn på nytt for å se siste versjon;
endringer du ikke har lagret, forkastes.» Velg **Last inn på nytt** og gjør endringen
igjen.

### Bruk en mal på et tilbud

På et utkast legger **Flere handlinger** → **Bruk en mal** til malens linjer etter
tilbudets egne, eller med **Erstatt tilbudets linjer** i stedet for dem, og fyller inn tomme
tekster. En mallinje der produktet er utgått eller slettet, utelates, og appen navngir den.

## Advarsler på en kunde eller et produkt

En kunde eller et produkt kan ha en **advarsel**, som alle tilbud og ordrer med den viser
over hodet:

- en **Informasjon**-advarsel, i gult — «Ring før levering», for eksempel — vises bare;
- en **Sperre**, i rødt — «Betaler ikke», «Tilbakekalt» — stopper også **Send**,
  **Marker som sendt**, **Bekreft** og aksept av et tilbud til en bekreftet ordre: appen
  sier «En advarsel sperrer dette».

Den som håndterer innstillingene for Ordre, ser **Overstyr advarselen** under en sperre.
Skriv en **Begrunnelse** og velg **Overstyr advarselen**: banneret viser **Advarsel
overstyrt** med begrunnelsen din, og neste sending, markering som sendt, bekreftelse eller
aksept går gjennom. Begrunnelsen lagres — i ordrens **Endringslogg** som **Advarsel
overstyrt**, eller øverst i tilbudets **Internt notat**. **Angre** fjerner overstyringen
før du bruker den; når den er brukt, er den borte, og neste sending eller bekreftelse
trenger en ny. Et tilbud som er sendt på e-post, forblir sendt selv om en sperre ble satt
mens det ble sendt.

De ser også **Legg til kundeadvarsel** (eller **Endre kundeadvarsel**) der, for å sette
kundens advarsel uten å forlate dokumentet. Alle advarslene ligger under
**Ordreinnstillinger** → **Salgsadvarsler** ([administrasjon](/nb/admin/orders/#hold-salgsadvarslene-ved-like)).

## Send et tilbud

**Send** åpner **Send tilbudet**: **Mottaker** (kontaktpersonens e-post, ellers kundens
faktura-e-post), opptil ti **Kopi**-adresser, **Gyldig til** og en **Melding** som legges
til e-postteksten. **Send** sender PDF-en på e-post, lagrer den og setter tilbudet til
**Sendt**. Et sendt tilbud kan ikke lenger endres.

Utsending krever Kommunikasjon og en avsenderkanal valgt i innstillingene for Ordre. Når
den er av, sier knappen det: last ned PDF-en i stedet (**PDF** → **Last ned PDF-en**), lever
den, og velg **Marker som sendt** — PDF-en lagres, tilbudet kan ikke lenger endres, og det
er gyldig til datoen som vises. **Dokumenter og utsendelser** på tilbudet viser PDF-en til
hver sendt revisjon og hver e-post med status: **I kø**, **Sendt** eller **Feilet**.

**PDF** har også **Forhåndsvis PDF-en** (et utkasts er merket som utkast) og **Proforma**,
en utskrift merket som ikke en faktura, som aldri lagres eller sendes.

## Svar på et tilbud

På et sendt eller utløpt tilbud:

- **Aksepter** åpner **Aksepter tilbudet**. Kryss av **Valgfrie linjer kunden tok**; de som
  ikke er krysset av, tas ikke med. Under **Lag** velger du **Et ordreutkast**, til
  kontroll før den bekreftes, eller **En bekreftet ordre**, bekreftet med en gang med
  tilbudets priser — som krever rettigheten til å bekrefte ordrer — eller **Et
  abonnement**, et abonnementsutkast på planen du oppgir, som krever rettigheten til å
  administrere abonnement og et tilbud med bare tjenester ([Abonnement](#abonnement)).
  Ordren eller abonnementet åpnes; tilbudet viser **Akseptert som** med en lenke til det.
  Et tilbud aksepteres én gang.
- **Avslå** registrerer at kunden sa nei, med en **Begrunnelse** om du vil.
- **Revider** gjør tilbudet til et utkast igjen, som ny revisjon, for å endre og sende det
  på nytt. PDF-ene fra tidligere revisjoner beholdes.

Et sendt tilbud der dagen i **Gyldig til** er passert, blir **Utløpt** dagen etter. Det kan
likevel aksepteres — dialogen ber deg krysse av for at du aksepterer det likevel — avslås
eller revideres.

**Flere handlinger** har også **Kopier**, som lager et nytt tilbudsutkast av et hvilket
som helst tilbud med kundens opplysninger og katalogprisene hentet på nytt, **Kanseller
tilbudet** for et utkast eller et sendt tilbud, og **Slett tilbudet** for et utkast som
aldri er sendt. Nummeret til et slettet tilbud brukes ikke igjen.

## Lag en ordre

Når et tilbud aksepteres, lages en ordre. For å lage en direkte velger du **Ny ordre** i
**Ordre** eller på kundens fane **Ordre**, velger **Kunde** og velger **Opprett ordren**.
En ordre har samme hode og linjer som et tilbud, bortsett fra at den ikke har valgfrie
linjer eller gyldighetsdato. Kommer du til siden fra **Opprett ordre** på et *Vil kjøpe*-svar
i en Messenger-samtale, åpnes den med kunden og produktet kunden ba om: produktets eneste
variant blir ordrens første linje (antall 1), og for et produkt med flere varianter velger du
én — eller ingen — før du velger **Opprett ordren**.

Når Lager fører beholdning, får en ny ordre også et **Lagersted** — lageret varene
reserveres på og leveres fra — fra **Standard lagersted** i innstillingene for Ordre. Endre
det i ordrehodet mens ordren er et utkast; på en bekreftet ordre kan lagerstedet byttes til
et annet til ordren har en leveranse, og reservasjonene flyttes med.

## Selg et sett

Når [Produksjon](/nb/user/manufacturing/#lag-et-sett) er på, selges et produkt med et
**sett** som standardstykkliste som én linje og leveres som komponentene.

1. Legg settets produkt til på ordren som enhver produktlinje, med antall og pris.
2. Lagre. Under settets linje viser ordren en linje per komponent — antallet, settets
   ganger hvor mange ett sett holder, og hvor mange ett sett holder. Komponentene kopieres
   til ordren når settets linje legges til: en senere endring av settet i Produksjon når
   den ikke.
3. Endre settets antall og lagre: komponentene følger med. Fjern settets linje, så går de
   med den. Komponentene redigeres ikke selv.

Settets linje bærer prisen; komponentene har pris 0, faktureres ikke for seg og skrives
ikke ut med priser på ordrebekreftelsen. Hver komponent reserveres, plukkes — med partier,
når den holdes i partier — og leveres som enhver varelinje; selve settet gjør aldri det.
På en bekreftet ordre med lagersted er settets **Disponibelt** hvor mange sett
komponentene gir av det som er disponibelt: det minste, over komponentene, av det som er
disponibelt av hver delt på hvor mange ett sett holder. Settet regnes som levert med de hele
settene komponentenes leveranser gir, og tilbys fakturert alene; fakturaen dekker
komponentene, og ordren regnes som fakturert og ferdig ut fra settets linje. Plukklisten
skriver bare ut komponentene; pakkseddelen skriver ut settet med komponentene under.

**Registrer levert** behandler et sett etter hvordan ordren leveres. På en ordre som leveres
med leveranser tilbys ikke settets linje — komponentene gjør det, og settet regnes som
levert med de hele settene de gir. Å ta tilbake en komponent avvises når det ville gi færre
hele sett levert enn det som er fakturert eller ligger i et fakturautkast. På en ordre som registreres levert for hånd (uten
lagersted, eller når Lager ikke fører beholdning) tilbys settets linje og ikke
komponentene: å registrere settet registrerer hver komponent med det, settets antall
ganger hvor mange ett sett holder, og å ta det tilbake tar dem tilbake.

Når komponentene er levert ujevnt — mer av én enn av en annen — og du velger **Kanseller
rest**, beholder settet de hele settene som er levert, og resten av det kanselleres. En
komponent som er levert utover de hele settene, forblir levert og faktureres aldri, siden
bare hele sett har en pris: endringsloggen sier **Levert utover hele sett, ikke fakturert**
på den komponenten, med hvor mye. Ordren er ferdig når settene som beholdes, er fakturert.

Når noe avvises, er linjen som nevnes settets linje, også når årsaken er en av
komponentene — for eksempel en komponent det ikke er nok av på lagerstedet ved **Bekreft**.

Uten Produksjon selges et setts produkt som en vanlig varelinje, som reserveres og leveres
selv.

## Bekreft en ordre

**Bekreft** på et ordreutkast låser kundeopplysningene og prisene, lagrer
ordrebekreftelsen og gjør den **Bekreftet**. Det krever rettigheten til å bekrefte ordrer.
Det avvises når kunden er deaktivert, ordren ikke har produktlinjer, en linje ikke har pris
i ordrens valuta, eller et produkts avgiftskategori ikke har mva-kode — koble den i
Fakturaer først ([Avgiftskategoriene i Produkter](/nb/user/invoices/#avgiftskategoriene-i-produkter)).
En sperre på kunden eller et produkt stopper den også, til den som håndterer innstillingene
for Ordre, overstyrer den ([Advarsler](#advarsler-på-en-kunde-eller-et-produkt)).

På en ordre med lagersted reserverer **Bekreft** også hver varelinjes antall på lagerstedet,
så ingen annen ordre kan ta det. Når lagerstedet ikke har nok disponibelt, avvises
bekreftelsen, og **Ikke nok disponibelt** viser linjene det mangler på, med hva som er
disponibelt av hver. Velg **Bekreft uten å reservere** for å bekrefte ordren likevel —
endringsloggen viser det — og reserver senere med **Reserver nå** når varene er kommet.

Når innstillingene for Ordre sier det, sendes ordrebekreftelsen av seg selv på e-post til
kontaktpersonen, ellers til kundens faktura-e-post. For å sende den selv velger du **Send
ordrebekreftelsen**. **Ordrebekreftelse** laster den ned, og **Proforma** skriver ut en
proforma.

Et ordreutkast kan slettes med **Flere handlinger** → **Slett ordren**; nummeret brukes
ikke igjen. Ble utkastet laget ved å akseptere et tilbud, går tilbudet tilbake til
**Sendt** og kan aksepteres på nytt; er dagen i **Gyldig til** passert, blir det **Utløpt**
som vanlig.

Sendes ordrebekreftelsen av seg selv, og ordren endres igjen før e-posten går, sendes
bekreftelsen for ordren slik den er da, og **Dokumenter og utsendelser** viser den
versjonen. En ordre som kanselleres før bekreftelsen er sendt, får ingen.

## Endre en bekreftet ordre

En bekreftet ordre kan endres i det som ikke er levert eller fakturert: legg til linjer;
senk antallet på en linje, men ikke under det som er levert, fakturert eller står på et
fakturautkast; endre pris, rabatt eller tekst på en linje så lenge ingenting av den er
fakturert eller står på et fakturautkast; fjern en linje uten leveranser, fakturaer eller
fakturautkast; og endre leveringsdato, leveringsadresse, kontaktperson, Deres
referanse og internt notat. Kunde, valuta, mva-visning, betalingsbetingelser, språk,
tekster, prosjekt og selger ligger fast når den er bekreftet.

En linje som allerede står på ordren, beholder produktet, navnet, enheten og mva-koden den
ble bekreftet med, også om produktet endres eller slettes i katalogen senere. Bare antall,
pris, rabatt og tekst kan endres; for en annen enhet eller mva-kode fjerner du linjen (hvis
ingenting av den er levert eller fakturert) og legger til en ny.

Hver lagret endring øker ordrens **versjon**, og ordrebekreftelsen du sender neste gang,
har tittelen **Revidert ordrebekreftelse**. **Endringslogg** viser hver endring siden
ordren ble bekreftet: når, av hvem og hva — **Linje lagt til**, **Antall endret**, **Satt
på vent**, **Registrert levert** og så videre.

På en ordre som holder reservasjoner, følger reservasjonene endringen: et større antall
reserverer differansen — avvist når lagerstedet ikke har den disponibel — et mindre gir resten
tilbake, en ny varelinje reserverer sitt antall, og en fjernet linje gir reservasjonen tilbake.
En linje som får et annet produkt, gir tilbake det den holdt av det gamle produktet og
reserverer antallet av det nye — avvist når lagerstedet ikke har det disponibelt. Når Lager
ikke fører beholdning, reserverer en endring ikke mer, men beholder det ordren holder, og gir
bare tilbake det et mindre antall ikke lenger trenger.

## Sett på vent, lås og kanseller

- **Sett på vent**, med en **Begrunnelse**: en ordre på vent faktureres ikke og tar ikke
  imot levering — verken registrering eller angring — men kan fortsatt endres. **Ta av
  vent** tar den av vent.
- **Flere handlinger** → **Lås** (rettigheten til å bekrefte): en låst ordre kan ikke
  endres, men leveres og faktureres fortsatt. **Lås opp** gjør at den kan endres igjen.
- **Flere handlinger** → **Kanseller ordren**, så lenge ingenting er levert eller
  fakturert. En låst ordre må låses opp først. Dette kan ikke angres.
- **Flere handlinger** → **Kanseller rest**, når noe er det: det som ikke er levert eller
  fakturert på hver linje, kanselleres. Dette kan ikke angres.

Ingen av dem tas mens et fakturautkast holder deler av ordren: utsted eller slett utkastet
først, eller [frigjør det det holder](#frigjør-det-fakturautkast-holder).

Å sette på vent, låse og registrere leveranser endrer ikke versjonen.

**Kanseller ordren** gir tilbake alt ordren har reservert, og kansellerer leveranseutkastene.
**Kanseller resten** gir tilbake det som ikke lenger gjenstår å levere, og tar linjene uten noe
igjen å levere ut av leveranseutkastene — et utkast som blir tomt, kanselleres. En linje som er
fakturert ut over det som er levert, har fortsatt så mye igjen å levere: den beholder
reservasjonen for det og blir stående på utkastene.

## Registrer hva som er levert

Når ordren ikke har lagersted, eller Lager ikke fører beholdning, og for tjenester og
linjer uten produkt, registrerer du leveranser for hånd på en bekreftet ordre som ikke er på
vent. Varer på en ordre med lagersted leveres med en [leveranse](#leveranser-og-lager); å
registrere dem for hånd virker fortsatt, som en korreksjon, og gir tilbake like mye av
linjens reservasjon som du registrerer.
**Registrer levert** spør hvor mye av hver linje som er levert nå (**Lever nå**); en linje
med 0 endres ikke, og du kan ikke levere mer enn det som gjenstår. **Flere handlinger** →
**Angre levert** tar antall tilbake (**Ta tilbake**); på en linje som faktureres ved levering,
ikke under det som er fakturert eller ligger på et fakturautkast. Hver linje viser fremdriften — «3 av 5
levert» — og ordren leveringsstatusen: **Ikke levert**, **Delvis levert** eller **Levert**.

## Leveranser og lager

Når Lager fører beholdning, leveres en ordre med lagersted fra det lagerstedet med en
**leveranse**. Å lage, plukke, bokføre og kansellere leveranser krever tilgangen til å
håndtere leveranser.

### Hva hver linje holder

På en bekreftet ordre med lagersted viser hver varelinje **Reservert** — det som er reservert
for den på lagerstedet — og **Disponibelt** — det av produktet som er på lagerstedet og ikke
reservert for noen ordre. Disponibelt kan bli negativt etter en lagerkorreksjon i Lager. Når en
linje holder mindre enn det som gjenstår å levere, reserverer **Reserver nå** resten; det
avvises når lagerstedet ikke har det disponibelt.

### Varer som produseres mot ordre

Når [Produksjon](/nb/user/manufacturing/#produser-mot-ordre) er på og du har Bruke
Produksjon, viser en varelinje der produktet har en stykkliste hva som lages for den: hver
**produksjonsordre** — nummeret, som åpner den, statusen, hvor mye av antallet som er
produsert, og når den er planlagt ferdig. Et produkt som produseres mot ordre, får
produksjonsordren sin av seg selv når du bekrefter ordren; for et annet produkt som lages
etter en stykkliste, åpner **Opprett produksjonsordre** på linjen en ny ordre i
Produksjon, fylt ut fra linjen. Den krever rettigheten til å administrere
produksjonsordrer, og tilbys mens linjen ikke har noen. Når et produkt som produseres mot
ordre, ikke fikk noen produksjonsordre, sier linjen **Ingen produksjonsordre laget:** og
hvorfor — stykklisten har ingen operasjoner og ordren angir ikke noe lagersted, for
eksempel, eller produksjonen mot ordre feilet og produksjonsordren må opprettes manuelt.

Når en produksjonsordre laget for linjen lukkes, sier linjen hvor mye som er **produsert,
klar til reservering**: velg **Reserver nå** for å reservere det. Når varene ble laget på
et annet lagersted enn ordrens, sier linjen det: flytt dem til ordrens lagersted i Lager
først. Å kansellere ordren kansellerer ikke produksjonsordrene: de merkes i Produksjon for
at noen skal avgjøre.

### Lag en leveranse

1. På den bekreftede ordren viser kortet **Leveranser** ordrens leveranser. Velg **Lag
   leveranse**. Den er deaktivert mens ordren er på vent, når ingenting gjenstår å levere, og
   mens du har endringer som ikke er lagret.
2. Det nye leveranseutkastet åpnes med hver linje som gjenstår å levere, med det som kan gå fra
   lagerstedet: det linjen har reservert, og det som er disponibelt i tillegg. Hver linje
   plukkes fra den første lokasjonen som har hele antallet — varens
   [hovedlokasjon](/nb/user/inventory/#angi-hovedlokasjon-og-bestillingspunkt-for-en-vare)
   på lagerstedet først, når den har en.
3. Endre en linjes **Antall**, eller sett det til 0 for å utelate den, og velg **Fra lokasjon**
   blant lagerstedets lokasjoner som har produktet — hovedlokasjonen først — og velg så
   **Lagre**.
4. **Plukkliste** skriver ut linjene sortert etter lokasjon, med alle lokasjoner som har hvert
   produkt, hovedlokasjonen først, for den som plukker varene.
5. **Bokfør leveranse** trekker varene fra de valgte lokasjonene i Lager, legger antallene til
   ordrens leverte antall og lagrer **Pakkseddel** — linjene, hva som ble bestilt, hva denne
   leveransen tar, hva som står igjen som restordre og leveringsadressen. Alle linjer må være
   plukket, og den avvises når en lokasjon ikke lenger har det den skal ta.

En bokført leveranse endres ikke. En feil rettes med en lagerkorreksjon i Lager og **Angre
levert** på ordrelinjen. Et leveranseutkast kan kanselleres; antallene står da åpne på ordren
igjen.

### Lever varer som holdes i partier

Et produkt som holdes i [partier eller serienumre](/nb/user/inventory/#partier-og-serienumre)
plukkes per parti i stedet for per lokasjon: linjen sier **Plukkes per parti**, og partiene
den tas fra, står under den med partiets **Utløper**, **Lokasjon** og **Antall**.

1. Når leveransen lages, velges partiene for deg: det som utløper først, så det eldste,
   uten partier som er sperret eller utløpt; et serienummer tas helt. En linje kan ta flere
   partier, på ulike lokasjoner.
2. Når partiene ikke har nok, beholder linjen antallet sitt og sier **Mangler** det som
   mangler. **Bokfør leveranse** venter til partiene går opp i linjens antall: sett ned
   antallet, eller velg flere.
3. For å ta et annet parti velger du det i **Bytt parti**: lagerstedets partier av
   produktet, det som utløper først øverst, hvert med utløpsdato, lokasjon og hvor mye som
   ligger der. På en linje som mangler noe, dekker partiet det som mangler; ellers erstatter
   det partiene. Endre et partis antall i raden, eller fjern det med søppelbøtta. Et parti
   i karantene eller blokkert kan ikke velges.
4. Et utløpt parti kan bare leveres av noen med rettigheten **Bruke utgåtte partier**
   (`inventory:expired-override`): kryss av for **Bruk utløpt parti** under linjen, og velg
   det så i **Bytt parti**; det merkes **Utløpt**. Uten rettigheten vises det, men kan ikke
   velges, og en leveranse som tar et, avvises. Den som ikke har rettigheten, kan likevel
   lagre leveransen med partiet slik det ble valgt, men ikke endre antallet.
5. Et serienummer leveres ett om gangen: antallet er 1.
6. **Lagre**. Endrer du bare linjens antall, blir partiene du valgte stående: linjen sier
   **Mangler** eller at partiene tar mer enn antallet, til du får dem til å gå opp.
   Partier som ble valgt for deg, velges på nytt for det nye antallet.
7. **Foreslå på nytt** under en linje velger partiene for deg på nytt — det som utløper
   først, fra det som ligger der nå — og lagrer leveransen.

**Plukkliste** har en rad per parti, under lokasjonen, med partinummer og utløpsdato i
kolonnen **Parti**. **Pakkseddel** viser partiene — og serienumrene — under hver linje, med
utløpsdato, hvert parti én gang med hvor mye linjen tar av det: den er kundens oversikt
over hvilke partier de fikk. Kortet **Leveranser** på ordren viser partiene til hver
bokførte leveranse og til hvert utkast slik de er valgt så langt; en kansellert leveranse
viser ingen.

Kan du se beholdning i Lager — både **Se lagersteder** og **Se beholdning** — lenker hvert
parti på leveransen til siden sin der, og **Spor** ved siden av åpner
[sporingen](/nb/user/inventory/#spor-et-parti): hvor partiet kom fra og hvem andre som fikk
det. Uten dem nevnes partiene uten lenker.

Når du bokfører, kan noen andre ha tatt et parti i mellomtiden, eller sperret det. Har du
ikke endret partiene selv, velges de på nytt fra det som ligger der nå, og leveransen
bokføres med dem; leveransen viser partiene den tok. Valgte du partiene selv, eller er det
ingenting å velge fra, avvises bokføringen og partiet nevnes i raden sin.

### Restordre

Det som ikke er levert, står igjen på ordren som restordre; det lages ikke noe eget dokument.
**Leveranser** → **Til levering** viser de bekreftede ordrene som har noe igjen å levere; lag
neste leveranse fra ordren når varene er kommet. **Leveranser** → **Leveranser** viser
leveransene, med utkastene først.

### Send en leveranse

En bokført leveranse sendes med en transportør fra kortet **Sendinger**. Det krever
tillatelsen til å håndtere leveranser, og en fraktkonto satt opp under Arbeidsområde ›
Integrasjoner › Frakt ([Frakt](/nb/admin/shipping/)).

1. På den bokførte leveransen velger du **Send**. Et sendingsutkast lages med ett kolli som
   veier de leverte varene (ett kilo når produktene ikke har vekt), til ordrens
   leveringsadresse, på lagerstedets standard fraktkonto og standardtjeneste.
2. Endre **Tjeneste** — for eksempel *Pakke til hentested* eller *Pakke til bedrift* —,
   mottakerens telefon og e-post, som transportøren sender varslene sine til, og **Kolli**:
   vekten på hvert i gram og lengde, bredde og høyde i millimeter. Legg til et kolli for hver
   pakke, og velg **Lagre sendingen**.
3. **Vis priser** viser hva transportøren tar for hver av tjenestene sine, billigste først.
   Brings priser er estimater: transportørens faktura er fasiten.
4. **Bestill frakt** bestiller sendingen. Sendingen viser sendingsnummeret, en lenke
   **Spor** og en **Etikett** å skrive ut; en godstjeneste har også et **Fraktbrev** — for de
   andre gjelder etiketten. Når innstillingene for Ordre sier det, får kunden en e-post med
   sporingslenken. Ordrens fraktlinjer regnes som levert. Å skrive ut etiketten og
   fraktbrevet krever tillatelsen til å håndtere leveranser.

En testkonto bestiller i Brings testmodus, merket **Test**. Bring sender likevel varslene sine
til ekte telefonnumre og e-postadresser i testmodus, så bruk oppdiktede kontakter der.

Når bestillingen feiler fordi transportøren ikke svarte, venter du et par minutter og velger
**Bestill frakt** igjen: Vantigo spør transportøren først og bestiller én gang. Til da kan
sendingen ikke endres eller kanselleres, og mens en bestilling pågår, sier et nytt
**Bestill frakt** fra om det. Transportøren kan bruke opptil et kvarter på å vise en
bestilling den har gjort: til da bestiller Vantigo ikke mer og sier fra — sjekk Mybring, eller
vent og bestill igjen. En bestilling som finnes igjen slik, har ingen etikett i Vantigo:
sendingen sier **skriv ut etiketten i Mybring**, og du skriver den ut der. Når transportøren
avviser, sier dens egne ord hvorfor — et feil postnummer, for eksempel — og sendingen forblir
et utkast du kan endre.

### Følg og kanseller en sending

Sendingens status endrer seg av seg selv etter hvert som transportøren melder den —
**Bestilt**, **Underveis**, **Levert** — hver halvtime, med hver **sporingshendelse** listet
under. Et avvik eller en retur merker den med **Avvik**, og det gjør også en kansellering Bring
melder: Vantigo kansellerer ikke sendingen av seg selv — se på den, og kanseller den her om
det stemmer. Kortet **Sendinger** på ordren viser
hver bestilte sending med sporingslenken.

**Kanseller sendingen** kansellerer et utkast. En bestilt Bring-sending kan ikke kanselleres
hos Bring: den **ugyldiggjøres** i Vantigo, og etiketten må ikke brukes — Bring tar bare betalt
for pakker som er levert inn. En pakke transportøren alt har, kan ikke kanselleres.

## Fakturer en ordre

En ordre faktureres i Fakturaer, fra linjene: du velger hvor mye av hver linje som skal
faktureres, og utstedelsen av fakturaen markerer det som fakturert på ordren. Linjene
faktureres **ved ordre** — det som er bestilt, kan faktureres så snart ordren er bekreftet,
levert eller ikke — eller **ved levering**: bare det som er levert, kan faktureres. Varer på en
ordre med lagersted faktureres ved levering hvis du ikke velger noe annet; alt annet ved
ordre. Endre en linjes regel under navnet i linjene mens ingenting av den er fakturert eller
på et fakturautkast.

1. På en bekreftet ordre velger du **Fakturer**. Den krever **Bruke Fakturaer**
   (`invoices:access`) og **Lage fakturaer** (`invoices:create`) i Fakturaer, og vises
   ikke uten dem. Den er deaktivert, med begrunnelse, mens ordren er på vent («En ordre på
   vent faktureres ikke. Ta den av vent først.»), når ingenting gjenstår å fakturere
   («Ingenting gjenstår å fakturere.») og mens du har endringer som ikke er lagret
   («Lagre endringene først»).
2. **Fakturer fra ordre** i Fakturaer åpnes med ordren valgt. Velg hvor mye av hver linje
   som skal faktureres — alt som gjenstår, eller mindre for å fakturere deler av en linje —
   og velg **Lag utkastene**
   ([Fakturer fra ordre](/nb/user/invoices/#fakturere-ordre)).
3. Sjekk utkastet i Fakturaer og utsted det. Til da **holder** utkastet antallene det
   fakturerer: fremdriften på hver linje viser «2 på fakturautkast», og de tilbys ikke til
   fakturering igjen.

Når fakturaen utstedes, øker det fakturerte antallet på linjen med det den fakturerte, og
faktureringsstatusen på ordren blir **Delvis fakturert** eller **Fakturert**. En ordre er
ferdig når den er bekreftet og hver produktlinje er fakturert eller kansellert fullt ut —
registrer leveransene også, for **Levering**-statusen og ordrereserven.

Sletter du utkastet i Fakturaer, gis antallene tilbake med en gang: de tilbys til
fakturering igjen. Det samme gjør en kreditnota som returnerer en fakturalinje fullt ut —
det fakturerte antallet på ordren går ned igjen, og linjen kan faktureres på nytt.

### Mens et fakturautkast holder deler av ordren

Et fakturautkast er et løfte om hva som skal faktureres, så ordren holder seg til det:

- antallet på en linje kan ikke bli lavere enn det som er levert, fakturert eller står på
  et fakturautkast;
- pris og rabatt på en linje kan ikke endres mens deler av den står på et fakturautkast;
- en linje på et fakturautkast kan ikke fjernes;
- ordren kan ikke kanselleres, heller ikke resten av den.

Utsted eller slett utkastet i Fakturaer, eller frigjør det det holder.

### Frigjør det fakturautkast holder

Når et fakturautkast likevel ikke skal utstedes — noen laget det og glemte det, for
eksempel — velger du **Flere handlinger** → **Frigjør fra fakturautkast** på ordren. Alt
fakturautkast holder av ordren, tilbys til fakturering igjen med en gang. Et fakturautkast
som fortsatt holder deler av den, avvises da når noen utsteder det; slett det og fakturer
ordren på nytt. **Endringslogg** viser det som **Frigjort fra fakturautkast**.

Et fakturautkast holder sin del av ordren så lenge utkastet finnes, uansett hvor lenge:
ingenting gir det tilbake av seg selv. Det et slettet utkast holdt, gis tilbake med en
gang — eller, om Ordre ikke fikk beskjed, av Fakturaer i løpet av den neste timen eller
så.

### Kortet Fakturaer

Siden til en bekreftet ordre har kortet **Fakturaer**, som viser hver faktura utstedt fra
den: **Fakturanr.**, **Fakturadato**, **Beløp eks. mva** — det fakturaen fortsatt
fakturerer av denne ordren, med det kreditnotaer har gitt tilbake av den under, *NOK 250,00
kreditert* — og **Status**, **Fakturert** eller, når kreditnotaer har returnert alt,
**Kreditert**. Med **Bruke Fakturaer** lenker nummeret til fakturaen. Til noe er fakturert,
sier kortet «Ingenting er fakturert ennå.»

## Abonnement

Et **abonnement** fakturerer tjenester per periode — support, drift, en lisens — måned
etter måned til det avsluttes. Abonnement selger bare tjenester: en produktlinje må være
en tjeneste i katalogen, og fritekstlinjer, seksjoner og merknader er tillatt.

**Abonnement** i sidemenyen viser dem med de nyeste først, med status, plan, start og
slutt, **Fakturert til og med** og hva en hel periode er verdt eksklusiv mva. Avgrens med
status, kunde og søket (nummer, kunde eller referanse). Alle med tilgang til appen ser
dem; å lage og endre dem krever rettigheten til å administrere abonnement.

### Lag et abonnement

1. Velg **Nytt abonnement**.
2. Velg **Kunde** og planen: **Intervall** (**Månedlig**, **Kvartalsvis**, **Halvårlig**
   eller **Årlig**), **Fakturering** — **Forskuddsvis**, en periode fakturert fra første
   dag, eller **Etterskuddsvis**, fra siste dag — **Start**, en **Slutt** om det har en,
   **Oppsigelse (måneder)**, **Fakturadag** og **Siste periode ved avslutning**
   ([Avslutt](#pause-gjenoppta-avslutt-og-kanseller)); står den tom, gjelder den
   innstillingene for Ordre gir. Oppsigelsestiden og fakturadagen skrives ut for kunden; de
   endrer ikke hva som faktureres når.
3. Velg **Opprett abonnement**. Utkastet åpnes; legg til linjene som på et tilbud, og
   **Lagre**.
4. Velg **Aktiver**. Abonnementet kontrolleres som en ordre ved bekreftelse — hver linje
   priset og med mva-kode, kunden aktiv — og fra nå av faktureres periodene.

Et tilbud kan også bli et abonnement: i **Aksepter tilbudet** velger du **Et abonnement**
under **Lag** og oppgir planen ([Svar på et tilbud](#svar-på-et-tilbud)).

Periodene går fra den første i startmåneden: et månedlig abonnements perioder er
kalendermånedene, et kvartalsvis tre måneder om gangen fra startmåneden. Når starten ikke
er den første i en måned, går første periode fra starten til periodens slutt og
**fordeles forholdsmessig** etter dager — et månedlig abonnement startet 31. januar
fakturerer 1/31 av januarprisen for 31. januar, deretter hele måneder fra februar.

### Periodene

Abonnementets side viser **Perioder**, hver med beløp og status:

- **Fakturert** — på en utstedt faktura, med nummeret;
- **På fakturautkast** — tatt av et utkast i Fakturaer, ikke utstedt ennå;
- **Til fakturering** — kan faktureres i dag, ikke fakturert ennå;
- **Kommende** — ikke til fakturering ennå;
- **Pauset** og **Hoppet over** — se under.

**Fakturert til og med** er siste dag i periodene som er fakturert uten opphold fra
starten. Periodene faktureres i Fakturaer, med **Fakturer fra ordre** eller en
faktureringskjøring av ordre ([Fakturer ordre](/nb/user/invoices/#fakturere-ordre)), og
aldri mer enn tolv måneder fram. Når en kreditnota gir en fakturert periode tilbake i sin
helhet, er den perioden til fakturering igjen — og bare den.

### Endre et aktivt abonnement

Endre linjene — antallet, prisen, en linje lagt til eller fjernet — og velg **Lagre**. En
endring rører aldri en periode som alt er på en faktura eller et fakturautkast: den
gjelder fra **Endringene gjelder fra**, den første perioden som ikke er fakturert, eller
en senere periodestart du velger. En endret eller fjernet linje slutter dagen før; den nye
versjonen starter den dagen. Linjene en endring avsluttet, står under **Tidligere
linjer**. En endring fordeles aldri forholdsmessig, og **Endringsloggen** viser den med
perioden den gjelder fra.

Gjelder endringen fra en tidligere periode enn en endring du lagret før, erstatter den nye
den gamle fra sin egen dato: en linje faktureres aldri to ganger i en periode.

Kunden, valutaen og planens intervall, fakturering og start ligger fast når abonnementet
er aktivt; **Siste periode ved avslutning** kan fortsatt endres.

### Pause, gjenoppta, avslutt og kanseller

- **Pause** stopper faktureringen fra en periodestart — den neste som ikke er fakturert,
  med mindre du velger en senere. Periodene før faktureres fortsatt.
- **Gjenoppta** starter faktureringen igjen fra en periodestart du velger. Periodene
  mellom pausen og den **hoppes over** og faktureres aldri.
- **Avslutt** setter sluttdatoen: perioden den faller i, er den siste som faktureres. Når
  sluttdatoen ikke er periodens siste dag, velger du under **Siste periode ved
  avslutning** hvordan den faktureres, forhåndsvalgt fra abonnementets egen innstilling:
  - **Full periode** fakturerer hele perioden;
  - **Forholdsmessig til sluttdato** fakturerer perioden fra første dag til og med
    sluttdatoen, som en andel av dagene i hele perioden — et månedlig abonnement på
    310,00 avsluttet 15. august fakturerer 15/31 av august, 150,00, for 1.–15. august.

  Sluttdatoen kan ikke ligge før en periode som alt er på en faktura eller et
  fakturautkast — krediter den først. En siste periode som alt er på en faktura eller et
  fakturautkast, kan bare faktureres i sin helhet: krediter den først for å fordele den
  forholdsmessig. Valget lagres på abonnementet og vises i **Endringsloggen**.
- **Kanseller abonnement** kansellerer et utkast, eller et aktivt abonnement der ingenting
  er fakturert ennå. Et med fakturerte perioder avsluttes i stedet.

## Salgsanalyse

**Analyse** (rettigheten til å se salgsanalysen) har tre visninger, hver med et diagram, en
tabell og **Last ned CSV**, som lagrer tabellen som en fil et regneark åpner. Beløpene er
eks. mva og legges aldri sammen på tvers av valutaer: hvert diagram viser én valuta, og med
flere velger du den over diagrammet.

- **Treffprosent på tilbud** — tilbudene sendt mellom **Fra** og **Til**, og hvor mange av
  dem som er **Akseptert**, **Avslått**, **Utløpt**, **Kansellert** eller fortsatt
  **Åpne**, telt og summert per selger; **Treffprosent** er aksepterte av sendte. Avgrens
  til én **Selger**. Et tilbud som sendes på nytt etter en revisjon, telles én gang, den
  dagen det sist ble sendt.
- **Ordreinngang** — ordrene bekreftet i perioden og ikke kansellert, gruppert etter
  **Måned**, **Selger**, **Kunde**, **Produkt** eller **Kategori** under **Grupper etter**.
  Fritekstlinjer er én gruppe, og det samme er produkter uten kategori.
- **Ordrereserve** — i dag: hva bekreftede ordrer har **Bestilt, ikke levert**, og hva de
  har **Levert, ikke fakturert**, fordelt på 0–7 dager, 8–30 dager og over 30 dager siden
  leveringen ble registrert. En faktura skal sendes innen en måned etter levering, så alt
  over 30 dager vises i rødt.

## Kundens Ordre-fane

På kundesiden viser fanen **Ordre** fire tall per valuta, eksklusiv mva — **Åpne tilbud**,
**Bestilt, ikke levert**, **Levert, ikke fakturert** og **Abonnement, per år**, de aktive
og pausede abonnementenes verdi over et år — og under dem kundens åpne tilbud, ordrene
deres, **Åpne** eller **Alle**, og abonnementene med hvert abonnements verdi per periode. **Nytt tilbud** og **Ny ordre** starter et for
denne kunden. Fanen krever tilgang til Ordre-appen; knappene krever rettigheten til å lage
tilbud eller ordrer, og vises ikke på en sammenslått eller anonymisert kunde.

Kundens **Tidslinje** viser når et tilbud ble sendt og akseptert og en ordre bekreftet
eller kansellert ([tidslinjen](/nb/user/customers/#tidslinjen)).

## Når noe avvises

| Appen sier | Hva du gjør |
| --- | --- |
| Endret av noen andre / Det er endret av noen andre. Last inn på nytt og prøv igjen. | Noen har endret det siden du åpnet det — lagret, satt på vent, låst, registrert levering — eller kunden er slått sammen eller slettet. **Last inn på nytt**; endringer du ikke har lagret, forkastes. |
| En linje beholder typen sin. Fjern den og legg til en ny linje i stedet. | Fjern linjen og legg til en ny av typen du vil ha. |
| En bekreftet ordres linje beholder enheten (eller mva-koden) | Fjern linjen og legg til en ny. |
| En linje har ingen pris i denne valutaen | Skriv inn en pris på linjen. |
| Et produkts avgiftskategori har ingen mva-kode | Be den som håndterer fakturering om å koble den under mva-kodene i Fakturaer. |
| Ordren har ingen produktlinjer | Legg til minst én produktlinje før du bekrefter. |
| Kunden er deaktivert eller arkivert | Aktiver kunden i Kunder, eller bruk en annen. |
| Prosjektet tilhører en annen kunde | Velg et av denne kundens prosjekter, eller ingen. |
| Et antall kan ikke bli lavere enn det som er levert, fakturert eller står på et fakturautkast. | Kanseller resten i stedet, angre leveringen først, eller utsted eller slett fakturautkastet. |
| Prisen på en fakturert linje kan ikke endres | Linjen er fakturert: la den stå. |
| En linje med leveranser, fakturaer eller et fakturautkast kan ikke fjernes. | Senk antallet, eller kanseller resten. |
| Deler av linjen står på et fakturautkast: pris og rabatt kan ikke endres før utkastet er utstedt eller slettet. | Utsted eller slett fakturautkastet i Fakturaer, eller **Frigjør fra fakturautkast**, og endre så prisen. |
| Deler av ordren står på et fakturautkast. Utsted eller slett utkastet, eller velg Frigjør fra fakturautkast i ordrens meny. | Gjør som det står: en ordre kanselleres ikke mens et fakturautkast holder deler av den. |
| Det er mer enn det som gjenstår å fakturere. | Noen har fakturert linjen i mellomtiden. Last inn på nytt og fakturer det som gjenstår. |
| Linjen kan ikke faktureres nå: ordren er et utkast, på vent eller kansellert. | Bekreft ordren, eller ta den av vent, og fakturer på nytt. |
| Noe er levert eller fakturert: kanseller resten i stedet | Bruk **Kanseller rest**. |
| Ordren er låst. | Noen med rettigheten til å bekrefte må **Lås opp** — også før den kan kanselleres. |
| Ordren er satt på vent. Ta den av vent før du registrerer levering. | Velg **Ta av vent**, og registrer eller angre leveringen. |
| Utsending er av | Last ned PDF-en og **Marker som sendt**, eller be en administrator velge en avsenderkanal. |
| E-posten kunne ikke sendes. Ingenting ble endret; prøv igjen, eller last ned PDF-en og merk den som sendt. | E-postleverandøren tok ikke imot e-posten: **Dokumenter og utsendelser** viser sendingen som **Feilet**. Prøv igjen, eller last ned PDF-en og marker som sendt. |
| Et dokument kan ha høyst 500 linjer | Del det i to. |
| Det er ikke nok disponibelt på lagerstedet. | Bekreft uten å reservere og reserver senere, sett ned antallet, eller lever fra et annet lagersted. |
| Lokasjonen har ikke så mye. | Noen har tatt varer i mellomtiden: velg en annen lokasjon, sett ned antallet, eller korriger beholdningen i Lager. |
| Velg lokasjonen hver linje tas fra. | Velg **Fra lokasjon** for hver linje, og bokfør så. |
| Partiene går ikke opp i linjens antall. | Velg flere partier i **Bytt parti**, endre et partis antall, eller sett ned linjens antall. |
| Velg partiene linjen tas fra. | Velg minst ett parti i **Bytt parti**, og lagre og bokfør så. |
| Partiet er utløpt. | Velg et annet parti, eller be noen med **Bruke utgåtte partier** bokføre leveransen med **Bruk utløpt parti**. |
| Partiet er sperret. | Partiet er i karantene eller blokkert i Lager: velg et annet parti. |
| Det partiet har ikke varen på den lokasjonen. | Noen har tatt partiet i mellomtiden: last inn på nytt og velg et annet. |
| Bare den som har lov til å bruke utløpte partier, kan velge et utløpt parti. | Velg et annet parti, eller be noen med **Bruke utgåtte partier** velge det. |
| Ingenting av ordren er åpent og disponibelt på lagerstedet. | Vent på varene, reserver dem, eller gjør ferdig leveranseutkastet som alt holder dem. |
| Ordren har leveranser; lagerstedet kan ikke lenger endres. | La lagerstedet være som det er. |
| En bokført leveranse kan ikke endres. | Korriger beholdningen i Lager og bruk **Angre levert** på ordren. |
| En advarsel sperrer dette | En kunde eller et produkt på dokumentet har en sperre. Den som håndterer innstillingene for Ordre, kan **Overstyr advarselen** med en begrunnelse, eller endre advarselen. |
| Forpakningen hører ikke til produktet | Velg produktet på nytt og en av forpakningene, eller skriv antallet i enheten. |
| Siste periode er alt på en faktura eller et fakturautkast og kan ikke gjøres forholdsmessig. | Velg **Full periode**, eller krediter perioden — eller slett fakturautkastet — først. |
| Ingen fraktkonto å bruke | Be en administrator legge til en, eller velge lagerstedets standardkonto, under Integrasjoner › Frakt. |
| Transportøren avviste forespørselen. … | Transportørens egne ord følger: rett det den nevner — adressen, vekten — og bestill igjen. |
| Transportøren svarte ikke. | Prøv igjen om et par minutter. En bestilling der svaret gikk tapt, finnes igjen: **Bestill frakt** bestiller én gang. |
| Sendingen bestilles akkurat nå. | Vent et par minutter og se igjen. |
| Transportøren viser ikke den tidligere bestillingen ennå. | Sjekk bestillingen i Mybring, eller vent et kvarter og bestill igjen. |
| Utfallet av en bestilling er ukjent: bestill igjen for å avklare det før sendingen endres. | Velg **Bestill frakt**; endre sendingen når den er bestilt eller avvist. |
| Transportøren har pakken; den kan ikke lenger kanselleres. | Avtal en retur med transportøren i stedet. |
| Dokumentet har ingen adresse å sende til. | Velg en leveringsadresse på dokumentet, lagre det og estimer igjen. |
