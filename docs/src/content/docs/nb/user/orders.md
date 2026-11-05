---
title: Ordre
description: Ordre-appen — lag et tilbud, fra appen, kundesiden eller en mal; legg til produkt-, fritekst-, seksjons- og merknadslinjer, valgfrie linjer og rabatter; send det eller marker det som sendt, revider, aksepter, avslå, kanseller og kopier det; lag en ordre, bekreft den, endre den etter bekreftelse, sett den på vent, lås den, kanseller den eller resten, registrer hva som er levert, send ordrebekreftelsen og skriv ut en proforma; endringsloggen; linjer i forpakninger og fra variantrutenettet; kunde- og produktadvarsler og overstyring av en sperre; salgsanalysen; kundens Ordre-fane; og hva en avvisning betyr.
sidebar:
  order: 55
sources:
  - apps/orders/frontend
  - apps/host/frontend/src/routes/orders
  - apps/host/frontend/src/routes/customers/-customer-orders-tab.tsx
---

I Ordre-appen lager du tilbud til kundene dine og tar imot ordrene deres. Et **tilbud** er
et pristilbud kunden kan akseptere; en **ordre** er det kunden har kjøpt, som du bekrefter,
leverer og — når fakturering fra ordre kommer — fakturerer.

## Appen og sidemenyen

Sidemenyen har **Tilbud** og **Ordre** for alle med tilgang til appen, **Analyse** for dem
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
åpen. Til fakturering fra ordre kommer, er bare en ordre der alle linjene er kansellert,
ferdig.

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
adresse), **Kontaktperson**, **Deres referanse**, **Selger** (**Gjør meg til selger**
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
igjen for å skrive antallet i produktets egen enhet.

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
overstyrt**, eller nederst i tilbudets **Internt notat**. **Angre** fjerner overstyringen
før du bruker den.

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
  tilbudets priser — som krever rettigheten til å bekrefte ordrer. Ordren åpnes; tilbudet
  viser **Akseptert som** med en lenke til den. Et tilbud aksepteres én gang.
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
linjer eller gyldighetsdato.

## Bekreft en ordre

**Bekreft** på et ordreutkast låser kundeopplysningene og prisene, lagrer
ordrebekreftelsen og gjør den **Bekreftet**. Det krever rettigheten til å bekrefte ordrer.
Det avvises når kunden er deaktivert, ordren ikke har produktlinjer, en linje ikke har pris
i ordrens valuta, eller et produkts avgiftskategori ikke har mva-kode — koble den i
Fakturaer først ([Avgiftskategoriene i Produkter](/nb/user/invoices/#avgiftskategoriene-i-produkter)).
En sperre på kunden eller et produkt stopper den også, til den som håndterer innstillingene
for Ordre, overstyrer den ([Advarsler](#advarsler-på-en-kunde-eller-et-produkt)).

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
senk antallet på en linje, men ikke under det som er levert eller fakturert; endre pris,
rabatt eller tekst på en linje så lenge ingenting av den er fakturert; fjern en linje uten
leveranser eller fakturaer; og endre leveringsdato, leveringsadresse, kontaktperson, Deres
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

Å sette på vent, låse og registrere leveranser endrer ikke versjonen.

## Registrer hva som er levert

Til leveranser fra lager kommer, registrerer du leveranser for hånd på en bekreftet ordre
som ikke er på vent.
**Registrer levert** spør hvor mye av hver linje som er levert nå (**Lever nå**); en linje
med 0 endres ikke, og du kan ikke levere mer enn det som gjenstår. **Flere handlinger** →
**Angre levert** tar antall tilbake (**Ta tilbake**). Hver linje viser fremdriften — «3 av 5
levert» — og ordren leveringsstatusen: **Ikke levert**, **Delvis levert** eller **Levert**.

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

På kundesiden viser fanen **Ordre** tre tall per valuta, eksklusiv mva — **Åpne tilbud**,
**Bestilt, ikke levert** og **Levert, ikke fakturert** — og under dem kundens åpne tilbud
og ordrene deres, **Åpne** eller **Alle**. **Nytt tilbud** og **Ny ordre** starter et for
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
| Et antall kan ikke bli lavere enn det som er levert eller fakturert | Kanseller resten i stedet, eller angre leveringen først. |
| Prisen på en fakturert linje kan ikke endres | Linjen er fakturert: la den stå. |
| En linje med leveranser eller fakturaer kan ikke fjernes | Senk antallet, eller kanseller resten. |
| Noe er levert eller fakturert: kanseller resten i stedet | Bruk **Kanseller rest**. |
| Ordren er låst. | Noen med rettigheten til å bekrefte må **Lås opp** — også før den kan kanselleres. |
| Ordren er satt på vent. Ta den av vent før du registrerer levering. | Velg **Ta av vent**, og registrer eller angre leveringen. |
| Utsending er av | Last ned PDF-en og **Marker som sendt**, eller be en administrator velge en avsenderkanal. |
| E-posten kunne ikke sendes. Ingenting ble endret; prøv igjen, eller last ned PDF-en og merk den som sendt. | E-postleverandøren tok ikke imot e-posten: **Dokumenter og utsendelser** viser sendingen som **Feilet**. Prøv igjen, eller last ned PDF-en og marker som sendt. |
| Et dokument kan ha høyst 500 linjer | Del det i to. |
| En advarsel sperrer dette | En kunde eller et produkt på dokumentet har en sperre. Den som håndterer innstillingene for Ordre, kan **Overstyr advarselen** med en begrunnelse, eller endre advarselen. |
| Forpakningen hører ikke til produktet | Velg produktet på nytt og en av forpakningene, eller skriv antallet i enheten. |
