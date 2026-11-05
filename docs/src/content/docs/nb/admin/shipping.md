---
title: Frakt
description: Sett opp fraktkontoene Ordre bestiller sendinger med — Mybring-brukeren og API-nøkkelen hos Bring, økonomirollen prisene krever, kundenumrene, testmodus og testkundenumrene, avsenderen, standardtjenesten og standardkontoen per lagersted — Bekreft, fraktproduktet og påslaget, bryteren SHIPPING_CARRIERS og sporingsarbeideren; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 49
sources:
  - apps/server/internal/orders/carriers.go
  - apps/orders/frontend/src/pages/shipping-settings.tsx
  - apps/host/frontend/src/routes/workspace/shipping.tsx
  - apps/server/internal/shipping/bring/products.json
---

Ordre sender en bokført leveranse med en transportør: den spør transportøren om priser,
bestiller sendingen, skriver ut transportørens etikett og fraktbrev og følger sporingen. Denne
versjonen har én transportør, **Bring**, som bestilles direkte gjennom Brings eget API med
selskapets Mybring-bruker. Denne siden er administratorens del — fraktkontoene, testmodus og
standardvalgene; hvordan folk sender, står i [Ordre](/nb/user/orders/#send-en-leveranse), og
hver regel i [referansen](/en/reference/orders/#shipping).

## Før du starter

- Modulen **Ordre** er på, og **Lager** fører beholdning: en sending sender en bokført
  leveranse ([Ordre](/nb/admin/orders/)).
- `SHIPPING_CARRIERS` tillater Bring. Det gjør den med mindre driftsansvarlig har endret den
  ([Konfigurasjon](/nb/admin/configuration/)).
- En **Mybring**-konto for selskapet, med et **kundenummer** for pakker og, hvis dere sender
  paller eller stykkgods, ett for gods. Bring gir kundenumrene med avtalen.
- En Mybring-**bruker** for Vantigo — en persons innlogging, eller en laget for integrasjonen —
  med tilgang til disse kundenumrene, og dens **API-nøkkel**: i Mybring, *Innstillinger* →
  *API*. Nøkkelen tilhører brukeren, ikke selskapet.
- For priser trenger brukeren **økonomirollen** på kundenumrene. Uten den svarer Bring bare
  med listepriser, og Bekreft sier fra.
- Serveren når `api.bring.com` over https. Bring bruker sertifikater fra Let's Encrypt;
  ingenting er festet.

## Legg til en fraktkonto

Siden er Administrer arbeidsområde › **Integrasjoner** › **Frakt**, for den som har
**Håndtere transportører** (`orders:carriers-manage`), en sensitiv rettighet som ikke kan
delegeres. Gi den til den som har ansvaret for selskapets fraktavtaler.

1. Velg **Legg til fraktkonto**.
2. Gi den et **Navn** — *Bring*, eller *Bring test* for en testkonto.
3. **Kundenumre**: kundenummeret for pakker først, deretter, når dere har det, kundenummeret
   for gods. Brings paller, stykkgods og partigods (5100, 5300, 5400) bestilles på det andre.
4. **Mybring-bruker** (e-postadressen du logger inn med) og **API-nøkkel**. De forsegles med
   installasjonens `APP_SECRET` når du lagrer, og vises aldri igjen; for å endre dem skriver
   du inn nye.
5. **Testmodus**: på for en testkonto ([Testmodus](#testmodus)).
6. **Avsender**: navnet, adressen, telefonen og e-posten Bring skriver ut som avsender. Et tomt
   felt tar selgerens fra innstillingene i Fakturaer.
7. **Lagre**. Åpne kontoen igjen for å velge **Standardtjeneste** — produktet en ny sending og
   et estimat bruker, for eksempel *5800 Pakke til hentested* eller *5000 Pakke til bedrift*.

Velg **Bekreft** på kontoen. Vantigo spør Bring om en pris med kontoens nøkler og første
kundenummer:

- *Transportøren godtok nøklene* — og tjenestene kontoen kan bestille. Står det også *Prisene
  er listepriser*, mangler brukeren økonomirollen: gi den i Mybring og bekreft igjen.
- *Transportøren avviste nøklene* — brukeren eller nøkkelen er feil, eller nøkkelen er fornyet.
- *Transportøren avviste forespørselen* — med Brings ord, typisk et kundenummer brukeren ikke
  har tilgang til.
- *Transportøren svarte ikke* — prøv igjen; sjekk den utgående forbindelsen til
  `api.bring.com`.

Det kan finnes flere kontoer — én per lagersted, eller én for test. **Arkiver** en du ikke
bruker lenger: sendingene dens viser fortsatt til den.

## Testmodus

En testkonto bestiller i Brings testmodus: hvert kall har Brings testtopptekst, ingenting
hentes og ingenting faktureres. Bring gir testkundenumre på forespørsel — **5** (pakker i
Norge), **6** (Norden) og **7** (gods) — og fra 12. oktober 2026 er topplinjen den eneste
måten å teste på et ekte kundenummer.

**Bring sender likevel varslene sine til ekte telefonnumre og e-postadresser i testmodus.**
Test med oppdiktede kontakter — mottakerens telefon og e-post på sendingen kan endres før
bestilling — og aldri med en ekte kundes.

## Velg standardkonto per lagersted

Under **Standardkonto per lagersted** velger du en konto for hvert lagersted som leverer
ordrer, og **Lagre**; fjern et lagersteds valg for å ta bort standardkontoen. En lagring endrer
bare lagerstedene på siden, så to administratorer som lagrer samtidig, opphever ikke
hverandre. Et lagersteds nye sendinger og fraktestimatene for ordrene dets bruker
den; et tilbuds estimat bruker standard lagersteds. Et lagersted uten bruker den eneste aktive
kontoen når det finnes nøyaktig én; ellers velger sendingen konto.

## Sett fraktprodukt og påslag

I **Innstillinger for Ordre** › **Frakt**:

- **Fraktprodukt** — et tjenesteprodukt, for eksempel *Frakt*, som en fraktlinje tar navn og
  pris fra. Mva-koden følger varene på tilbudet eller ordren: deres når de har samme, ellers
  den som størstedelen av varenes beløp har; fraktproduktets egen mva-kode gjelder bare et
  dokument uten varer. Uten et fraktprodukt er en fraktlinje fritekst.
- **Påslag på transportørens estimat** — prosenten **Estimer** legger til Brings pris.
- **Send kunden e-post når frakten er bestilt** — på som standard: sendingsvarselet, med
  ordrenummeret, Bring, sendingsnummeret og sporingslenken, sendt fra avsenderkanalen i Ordre.
  Teksten kan endres under **E-posttekster** › **Sendingsvarsel**.

## Sporingsarbeideren

`orders.shipment_tracking` spør Bring hvert 30. minutt om hver sending som er bestilt eller
underveis de siste 60 dagene, lagrer hendelsene og flytter sendingen — underveis, levert,
avvik. En kansellering Bring melder, vises som et avvik og gjøres ikke noe med: en person
kansellerer sendingen i Vantigo. Hver sending spørres om innen 10 sekunder, og en som henger,
holder ikke de andre tilbake; en runde slutter å spørre etter 10 minutter, og sendingene den
ikke rakk, kommer først i neste. Den kjører i arbeiderprosessen med de andre og trenger
ingenting eget; et hvilket som helst antall instanser deler den. Brings push-varsler (Event
Cast) brukes ikke.

## Kontroller

1. Under Integrasjoner › Frakt sier **Bekreft** at nøklene er godtatt og prisene tilgjengelige.
2. Lag en ordre på et produkt med vekt, bekreft den, bokfør en leveranse, velg **Send** på
   kortet Sendinger på leveransen, **Vis priser**, og så **Bestill frakt** med en testkonto og
   oppdiktede kontakter.
3. Sendingen viser et sendingsnummer, en lenke **Spor** og en **Etikett** å skrive ut.
4. Innen en halvtime viser sendingen Brings første sporingshendelse.

## Hva som kan gå galt

| Symptom | Årsak | Løsning |
| --- | --- | --- |
| **Frakt** mangler under Integrasjoner | Ordre er av, eller personen mangler Håndtere transportører | Slå på Ordre, eller gi `orders:carriers-manage` |
| «Denne transportøren er ikke tillatt på denne installasjonen» | `SHIPPING_CARRIERS` holder tilbake `bring` | Be driftsansvarlig tillate den |
| Bekreft: *Transportøren avviste nøklene* | Mybring-brukeren eller API-nøkkelen er feil, eller nøkkelen er fornyet | Skriv inn brukeren og en ny nøkkel, lagre og bekreft igjen |
| Bekreft: *Prisene er listepriser* | Mybring-brukeren mangler økonomirollen på kundenummeret | Gi brukeren rollen i Mybring |
| «Fraktkontoens nøkler kan ikke leses på denne serveren» | `APP_SECRET` er endret siden nøklene ble lagret | Skriv inn nøklene igjen |
| «Ingen fraktkonto å bruke» ved sending | Ingen konto er lagerstedets standard, og det finnes ikke nøyaktig én aktiv konto | Velg lagerstedets standardkonto |
| Transportøren avviser en bestilling og nevner et postnummer eller en adresse | Ordrens leveringsadresse er ufullstendig | Rett leveringsadressen på ordren, lag en ny sending og bestill |
| En bestilling ender med «Transportøren svarte ikke» | Bring svarte ikke i tide; den kan ha bestilt | Vent to minutter og bestill igjen: Vantigo slår opp sendingen hos Bring først og bestiller én gang. Til da kan sendingen ikke endres |
| «Sendingen bestilles akkurat nå» (`booking_in_progress`) | Noen bestilte den for under to minutter siden, og Bring kan fortsatt holde på | Vent et par minutter og se igjen; den bestilles én gang |
| «Transportøren viser ikke den tidligere bestillingen ennå» (`booking_outcome_unknown`) | En bestilling fikk ikke svar for under et kvarter siden, og Bring viser den ikke på referanse ennå | Slå opp sendingen i Mybring blant ordrens sendinger; eller vent et kvarter og bestill igjen — da bestiller Vantigo på nytt om Bring fortsatt ikke viser noen |
| Sendingen sier «skriv ut etiketten i Mybring» | Bestillingen ble funnet igjen etter et tapt svar, og Bring gir ingen etikettlenke for den | Skriv ut etiketten fra sendingen i Mybring |
| En testbestilling sendte SMS til en kunde | Bring varsler ekte kontakter i testmodus | Bruk oppdiktede kontakter på testsendinger |
| Sendinger blir stående *Bestilt* | Sporingsarbeideren kjører ikke, eller serveren når ikke Bring | Sjekk at arbeiderprosessen kjører og at `api.bring.com` kan nås |
| En kansellert bestilt sending viser fortsatt etiketten | Bring har ingen kansellering: sendingen er ugyldiggjort i Vantigo | Ikke bruk etiketten; Bring fakturerer bare pakker som er levert inn |
