---
title: Produksjon
description: Slå på Produksjon — de to modulene den krever, migreringen og skjemaet manufacturing, og å gi det til en rolle med færrest mulige rettigheter — de fem rettighetene og hva hver gir, den sensitive kostnadsrettigheten og hvem som bør ha den, hvem som administrerer produksjonsordrer og hvem som registrerer produksjon, rollene du bygger av dem; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 51
sources:
  - apps/server/internal/manufacturing/module.go
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
---

Produksjon-modulen (**Manufacturing** på engelsk) beskriver hvordan virksomheten lager
produktene sine: arbeidsstasjonene der arbeidet gjøres, og stykklistene som sier hva som
går inn i hvert produkt, og produksjonsordrene som lager dem. Denne siden handler om å slå den på og gi folk tilgang; hvordan
den brukes, står i [Produksjon](/nb/user/manufacturing/), og hver regel i
[referansen](/en/reference/manufacturing/).

## Før du starter

- **Produkter** må være slått på: en stykkliste lager én av katalogens varer av andre, og
  standardkostene oversikten ruller opp, er Produkters.
- **Lager** må være slått på: hver arbeidsstasjon står på et lagersted i Lager, og
  oversikten viser hva som er tilgjengelig der. Opprett minst ett lagersted i Lager først
  ([Opprett et lagersted](/nb/user/inventory/#opprett-et-lagersted)).
- Gi produktene du lager og komponentene du bruker en **standardkost** i Produkter hvis
  de som ser kostnader, skal få en fullstendig samlet kostnad.

## Slå på modulen

1. Når `MODULES` ikke er satt, er hver modul på, også Produksjon. Når `MODULES` er satt,
   lister du `manufacturing` ved siden av `products` og `inventory`, for eksempel
   `MODULES=products,inventory,manufacturing` ([Konfigurasjon](/nb/admin/configuration/)).
   Uten en av de to nekter serveren å starte og navngir den — `manufacturing requires
   inventory`.
2. Start serveren på nytt. Migreringen `00156_manufacturing_baseline.sql` oppretter
   skjemaet `manufacturing`, tomt, uansett hva `MODULES` sier, så å slå på modulen senere
   krever ingen migrering. En databaserolle med færrest mulige rettigheter trenger
   skjemaet — legg `manufacturing` til de tre `GRANT`-setningene under
   [Databaseroller](/nb/admin/installation/#databaseroller):

   ```sql
   GRANT USAGE ON SCHEMA manufacturing TO "vantigo_app";
   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA manufacturing TO "vantigo_app";
   GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA manufacturing TO "vantigo_app";
   ```

   Migreringen `00157_manufacturing_orders.sql` legger produksjonsordrenes tabeller til i
   skjemaet. Standardrettighetene under Databaseroller dekker dem; en rolle som har fått
   rettigheter tabell for tabell, trenger at `GRANT`-setningene kjøres på nytt etter
   oppgraderingen.

3. **Produksjon**-flisen vises for alle med `manufacturing:access`.

Modulen starter ingen bakgrunnsjobb og har ingen egne innstillinger.

## Gi folk tilgang

Ingen andre enn Owner kan bruke appen før en rolle gir rettighetene, under
**Administrasjon av arbeidsområdet** → **Roller og tilgang**
([Roller](/nb/user/workspace-administration/#roller)). Hver rettighet i Produksjon krever
**Bruke Produksjon** (`manufacturing:access`) ved siden av seg.

| Rettighet | Nøkkel | Gir |
| --- | --- | --- |
| Bruke Produksjon | `manufacturing:access` | Appen, og å lese arbeidsstasjoner, deres stengninger, stykklister og oversikten over dem, og produksjonsordrer med produksjonene og historikken deres |
| Administrere stykklister og arbeidsstasjoner | `manufacturing:boms-manage` | Å opprette, redigere, arkivere, gjenaktivere og slette arbeidsstasjoner og deres stengninger; å opprette, redigere, arkivere og gjenaktivere stykklister og gjøre en til standard |
| Administrere produksjonsordrer | `manufacturing:orders-manage` | Å opprette, endre, bekrefte, reservere, lukke og kansellere produksjonsordrer; å legge til og endre komponentene deres; å registrere en produksjon |
| Arbeide i verkstedet | `manufacturing:shop-floor` | Å registrere en produksjon på en produksjonsordre; arbeidsordrene og timeføringen kommer med verkstedet i en senere leveranse |
| Se produksjonskostnader | `manufacturing:costs-view` | Alle kostnadstall: en arbeidsstasjons maskin- og arbeidssats, og å sette dem; standardkostene, verdiene, operasjonskostnadene og den samlede kostnaden i oversikten. Sensitiv, og kan ikke delegeres |

**Se produksjonskostnader** er sensitiv: den viser hva hvert produkt koster å lage, og hva
en time på hver maskin og en arbeidstime koster. Gi den til dem som priser og kalkulerer —
produksjonsledelsen, økonomi — heller enn til alle som holder stykklistene. Uten den
redigerer en person fortsatt en arbeidsstasjon; satsene blir stående som de var, og verken
vises eller sendes.

Beholdningstallene i oversikten er Produksjons egne oppslag i Lager, så de krever ingen
rettighet i Lager. Å velge et produkt eller en komponent krever heller ingen rettighet i
Produkter.

Å registrere en produksjon krever enten **Arbeide i verkstedet** eller **Administrere
produksjonsordrer** ved siden av Bruke Produksjon, så en operatør som melder hva som ble
laget, ikke trenger å kunne opprette, bekrefte eller kansellere ordrer.

Et vanlig oppsett: alle i produksjonen får Bruke Produksjon; operatørene får i tillegg
Arbeide i verkstedet; planleggere og arbeidsledere får i tillegg Administrere
produksjonsordrer; den som holder oppskriftene og maskinene, får i tillegg Administrere
stykklister og arbeidsstasjoner; produksjonsledelsen får i tillegg Se
produksjonskostnader.

## Kontroller

1. Som en med Administrere stykklister og arbeidsstasjoner åpner du **Produksjon** →
   **Arbeidsstasjoner** → **Ny arbeidsstasjon**, gir den en kode, et navn og et lagersted
   i Lager, og velger **Opprett arbeidsstasjon**. Den listes, åpen mandag til fredag 07:00–15:00.
2. Åpne **Stykklister** → **Ny stykkliste**, velg et vareprodukt, legg til én operasjon på
   den nye arbeidsstasjonen og én komponent, og velg **Opprett stykkliste**. Stykklisten
   listes som produktets **Standard**.
3. Åpne **Oversikt** for den og velg lagerstedet: komponenten listes med det som er
   tilgjengelig der, og operasjonen med sine forventede minutter.
4. Med Se produksjonskostnader viser arbeidsstasjonen satsene sine og oversikten
   kostnadene. Uten den vises ingen av dem.
5. Som en med Administrere produksjonsordrer åpner du **Produksjonsordrer** → **Ny
   produksjonsordre**, velger produktet, et antall og lagerstedet, og velger **Opprett
   produksjonsordre** og deretter **Bekreft**. Ordren er **Bekreftet**, og enten er
   komponenten reservert, eller siden navngir den som manglende.
6. Velg **Registrer produksjon** og **Registrer**. En **Produksjon**-bevegelse listes under
   **Bevegelser** i Lager, lenket til ordren, og ordren **Pågår**.
7. Prøv å slette komponenten i Produkter: det nektes, og en stykkliste og den åpne ordren
   navngis.

## Hva som kan gå galt

| Symptom | Årsak | Løsning |
| --- | --- | --- |
| Serveren nekter å starte: `manufacturing requires …` | `MODULES` lister `manufacturing` uten `products` eller `inventory` | Legg til modulen som navngis |
| `permission denied for schema manufacturing` i loggen | Databaserollen har ikke fått det nye skjemaet | Legg `manufacturing` til `GRANT`-setningene ([Databaseroller](/nb/admin/installation/#databaseroller)) |
| Flisen mangler | Personen mangler Bruke Produksjon | Gi den |
| Ingen **Ny arbeidsstasjon** eller **Ny stykkliste** | Personen mangler Administrere stykklister og arbeidsstasjoner | Gi den ved siden av Bruke Produksjon |
| Ingen **Lagersted** å velge på en arbeidsstasjon | Lager har ikke noe aktivt lagersted | Opprett et lagersted i Lager |
| Ingen satser på en arbeidsstasjon, ingen kostnader i oversikten | Personen mangler Se produksjonskostnader | Gi den til dem som skal se kostnader |
| Å lagre en sats nektes | Personen mangler Se produksjonskostnader | Gi den, eller la en som har den sette satsene |
| Oversikten sier **Kostnaden er ufullstendig** | En innkjøpt komponent har ingen standardkost i Produkter | Gi den en i Produkter |
| **Gjenaktiver** på en arbeidsstasjon nektes | Lagerstedet er arkivert i Lager | Gjenåpne lagerstedet i Lager, eller bruk en arbeidsstasjon på et annet lagersted |
| Produkter nekter å slette en variant eller gjøre produktet til en tjeneste | En aktiv stykkliste eller en åpen produksjonsordre navngir den | Arkiver stykklisten, eller endre linjene; lukk eller kanseller ordren |
| Ingen **Ny produksjonsordre**, **Bekreft** eller **Lukk** | Personen mangler Administrere produksjonsordrer | Gi den ved siden av Bruke Produksjon |
| Ingen **Registrer produksjon** | Personen har verken Arbeide i verkstedet eller Administrere produksjonsordrer | Gi en av dem |
| `permission denied for table orders` i loggen etter oppgraderingen | Rollen fikk rettigheter tabell for tabell, før `00157` | Kjør `GRANT`-setningene på nytt ([Databaseroller](/nb/admin/installation/#databaseroller)) |
| En produksjon nektes for beholdning | Lagerstedet har ikke nok av en komponent, eller lagerstedet er arkivert i Lager | Ta imot eller flytt varene, gjenåpne lagerstedet, eller rett med en justering i Lager |
