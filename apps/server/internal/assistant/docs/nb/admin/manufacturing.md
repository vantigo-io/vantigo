---
title: Produksjon
description: Slå på Produksjon — de to modulene den krever, migreringen og skjemaet manufacturing, og å gi det til en rolle med færrest mulige rettigheter — de seks rettighetene og hva hver gir, kvalitetsrettigheten som lukker avvik, den sensitive kostnadsrettigheten og hvem som bør ha den, hvem som administrerer produksjonsordrer og hvem som registrerer produksjon, hvem som arbeider i verkstedet og hvem som korrigerer tidsregistreringer, hvem som planlegger produksjonsplanen, rollene du bygger av dem; virksomhetens tidssone som arbeidsstasjonenes kalendere går i; å sette opp nettbrett i verkstedet — en konto per operatør, en oppdatert nettleser, å holde skjermen våken, arbeidsstasjonen hvert nettbrett husker, kameraet og nettverket; lagringen bildene av kontrollene tar; den nattlige påfyllingsjobben og automatisk oppretting, av som standard; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 51
sources:
  - apps/server/internal/manufacturing/module.go
  - apps/server/internal/db/migrations/00156_manufacturing_baseline.sql
  - apps/server/internal/db/migrations/00157_manufacturing_orders.sql
  - apps/server/internal/db/migrations/00158_manufacturing_work_orders.sql
  - apps/server/internal/db/migrations/00159_manufacturing_schedule.sql
  - apps/server/internal/db/migrations/00160_manufacturing_mto.sql
  - apps/server/internal/db/migrations/00168_manufacturing_quality.sql
  - apps/server/internal/db/migrations/00171_manufacturing_replenishment.sql
  - apps/server/internal/manufacturing/replenishment.go
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
  de som ser kostnader, skal få en fullstendig samlet kostnad, og et fullstendig estimat
  og faktisk kostnad på hver produksjonsordre.
- Sjekk **Virksomhetens tidssone** under **Innstillinger for arbeidsområdet**
  ([veiledning](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet)).
  Ukemønsteret til en arbeidsstasjon leses som lokale tider i den sonen, så
  produksjonsplanen planlegger i den, også dagene rundt overgangen til og fra sommertid.

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
   skjemaet, `00158_manufacturing_work_orders.sql` verkstedets arbeidsordrer og
   tidsregistreringer, og `00159_manufacturing_schedule.sql` produksjonsplanens tabell
   `work_order_slots` og ordrenes ønskede start, estimat og faktiske kostnad, og
   `00160_manufacturing_mto.sql` forespørslene om produksjon mot ordre (`mto_requests`)
   og ordrenes merke for kansellert opphav. Standardrettighetene under Databaseroller dekker dem; en rolle som
   har fått rettigheter tabell for tabell, trenger at `GRANT`-setningene kjøres på nytt
   etter hver oppgradering.

3. **Produksjon**-flisen vises for alle med `manufacturing:access`.

Modulen har ingen egne innstillinger. Den starter sine bakgrunnsjobber, og ingen av dem
trenger noe oppsett:

- `manufacturing-erasure`, som én gang i døgnet tømmer fritekstnotatene på
  tidsregistreringer skrevet av personer identitetsmodulen har slettet — en arbeidsleders
  korrigering eller tillegg, eller notatet til en blokkering
  ([personopplysninger](/en/reference/manufacturing/#personal-data));
- `manufacturing.mto-worker`, som hvert 15. sekund lager produksjonsordrene for
  salgsordrene som bekreftes i Ordre: én bekreftet ordre per varelinje der produktets
  standard stykkliste er **Produser mot ordre**
  ([produksjon mot ordre](/en/reference/manufacturing/#make-to-order-and-sub-assemblies)).
  Den trenger at **Ordre** er slått på, og at utboksens fordeler kjører i en
  arbeidsprosess for å gi den de bekreftede ordrene; med Ordre av lager den ingen, og alt
  annet i Produksjon virker som før. Forespørsler som fortsetter å feile, blir stående
  som `failed` i `manufacturing.mto_requests` med den siste feilen, etter åtte forsøk.
- `manufacturing.replenishment`, påfyllingsjobben: hver natt **kl. 02.00 i virksomhetens
  tidssone** (**Innstillinger for arbeidsområdet**), og hver gang noen trykker **Kjør nå**
  på **Forslag**, erstatter den hvert aktive lagersteds forslag om hva som bør produseres
  eller kjøpes ([påfylling](/en/reference/manufacturing/#replenishment)). Den leser
  bestillingspunktene, maksbeholdningene og multiplumene som er satt på påfyllinger i
  Lager, og, med Ordre slått på, salgsordrenes ureserverte etterspørsel — uten Ordre sier
  siden *Salgsordrer er ikke regnet med*. En kjøring er leaset, så to arbeidsprosesser
  kjører aldri én to ganger, og en kjøring der arbeideren stoppet, tas opp igjen etter ti
  minutter. Kjøringene beholdes i 90 dager i `manufacturing.replenishment_runs`.

Migreringen `00171_manufacturing_replenishment.sql` legger kjøringene, forslagene og
reglene for automatisk oppretting til i skjemaet.

## Gi folk tilgang

Ingen andre enn Owner kan bruke appen før en rolle gir rettighetene, under
**Administrasjon av arbeidsområdet** → **Roller og tilgang**
([Roller](/nb/user/workspace-administration/#roller)). Hver rettighet i Produksjon krever
**Bruke Produksjon** (`manufacturing:access`) ved siden av seg.

| Rettighet | Nøkkel | Gir |
| --- | --- | --- |
| Bruke Produksjon | `manufacturing:access` | Appen, og å lese arbeidsstasjoner, deres stengninger, stykklister og oversikten over dem, produksjonsordrer med produksjonene og historikken deres, produksjonsplanen, og rapportene om utnyttelse og tidsavvik |
| Administrere stykklister og arbeidsstasjoner | `manufacturing:boms-manage` | Å opprette, redigere, arkivere, gjenaktivere og slette arbeidsstasjoner og deres stengninger; å opprette, redigere, arkivere og gjenaktivere stykklister og gjøre en til standard |
| Administrere produksjonsordrer | `manufacturing:orders-manage` | Å opprette, endre, bekrefte, reservere, lukke og kansellere produksjonsordrer; å legge til og endre komponentene deres; å registrere en produksjon; alt i verkstedet; å korrigere en tidsregistrering og legge til en som mangler; å planlegge ordrer i produksjonsplanen og flytte arbeidsordrene deres |
| Arbeide i verkstedet | `manufacturing:shop-floor` | I verkstedet: å starte, sette på pause og fullføre arbeidsordrer, å registrere en produksjon, å melde svinn, og å blokkere og oppheve blokkeringen av en arbeidsstasjon; å registrere en produksjon på en produksjonsordre |
| Se produksjonskostnader | `manufacturing:costs-view` | Alle kostnadstall: en arbeidsstasjons maskin- og arbeidssats, og å sette dem; standardkostene, verdiene, operasjonskostnadene og den samlede kostnaden i oversikten; **Kostnad**-panelet på en produksjonsordre; rapporten **Produksjonskostnad**. Sensitiv, og kan ikke delegeres |
| Administrere kvalitet | `manufacturing:quality-manage` | Å lukke et avvik med disponering — som opphever karantenesperrene det satte på partier, med mindre det lukkes som kassering eller retur — og å oppheve sperrene til et som er lukket slik |

**Se produksjonskostnader** er sensitiv: den viser hva hvert produkt koster å lage, og hva
en time på hver maskin og en arbeidstime koster. Gi den til dem som priser og kalkulerer —
produksjonsledelsen, økonomi — heller enn til alle som holder stykklistene. Uten den
redigerer en person fortsatt en arbeidsstasjon; satsene blir stående som de var, og verken
vises eller sendes.

Beholdningstallene i oversikten er Produksjons egne oppslag i Lager, så de krever ingen
rettighet i Lager. Å velge et produkt eller en komponent krever heller ingen rettighet i
Produkter.

Å registrere en produksjon, og hver handling i verkstedet, krever enten **Arbeide i
verkstedet** eller **Administrere produksjonsordrer** ved siden av Bruke Produksjon, så en
operatør som melder hva som ble laget, ikke trenger å kunne opprette, bekrefte eller
kansellere ordrer. Å korrigere en tidsregistrering, eller legge til en som mangler, krever
Administrere produksjonsordrer: en operatør kan ikke endre sin egen registrerte tid.
Assistenten utfører ingen handlinger i verkstedet, uansett personens rolle: en operatør
stempler inn selv.

Å registrere en kvalitetskontroll og å melde et avvik hører til verkstedet: Arbeide i
verkstedet eller Administrere produksjonsordrer. Å endre kontrollene på en operasjon er å
endre stykklisten. **Administrere kvalitet** bestemmer hva som skjer med det et avvik
holder: gi den til kvalitetslederen eller produksjonslederen, siden det å lukke et avvik
som *bruk som den er* lar de holdte partiene leveres igjen.

Et vanlig oppsett: alle i produksjonen får Bruke Produksjon; operatørene får i tillegg
Arbeide i verkstedet; planleggere og arbeidsledere får i tillegg Administrere
produksjonsordrer; den som holder oppskriftene og maskinene, får i tillegg Administrere
stykklister og arbeidsstasjoner; produksjonsledelsen får i tillegg Se
produksjonskostnader.

## Nettbrett i verkstedet

Verkstedet (**Produksjon** → **Verksted**) er laget for et nettbrett ved hver
arbeidsstasjon. Hver handling registreres på den som er logget inn, så sett opp
nettbrettene etter det.

1. **Gi hver operatør sin egen konto.** Operatøren er den innloggede brukeren, og å bytte
   operatør betyr å bytte konto: logge ut og inn igjen. Det finnes ikke noe delt
   nettbrett med en PIN-kode per operatør i denne versjonen. La ikke et skift dele én
   konto — tidsregistreringene, produksjonene og ordrens historikk ville alle navngi den
   kontoen.
2. **Gi rollene.** Gi hver operatør Bruke Produksjon (`manufacturing:access`) og Arbeide i
   verkstedet (`manufacturing:shop-floor`), og ikke mer med mindre de også planlegger.
   Arbeidsledere som korrigerer tid, trenger Administrere produksjonsordrer.
3. **Bruk en oppdatert nettleser** — en oppdatert Safari, Chrome, Edge eller Firefox.
   Verkstedet er en side i Vantigo og trenger ingen app eller utvidelse.
4. **Hold skjermen våken.** Et nettbrett som låser seg, slutter å vise tidtakeren og køen.
   Sett tidsavbruddet for skjermen i enhetens innstillinger til lengden av et skift, eller
   kjør nettleseren i enhetens kioskmodus (Veiledet tilgang på en iPad, skjermfesting
   eller en kioskapp på Android) så den blir stående i verkstedet.
5. **Velg arbeidsstasjonen på hvert nettbrett.** Åpne **Verksted** og velg
   arbeidsstasjonen under **Velg arbeidsstasjon**. Nettbrettet husker den i nettleseren
   og tilbyr den først neste gang, som **Fortsett ved** med koden og navnet; å tømme
   nettleserens data, eller et privat vindu, glemmer den, og operatøren velger på nytt.
6. **Kontroller nettverket.** Køen og visningen av arbeidsordren oppdateres direkte over
   nettbrettets forbindelse til serveren, og en start, en pause eller en fullføring er en
   forespørsel til den: på et nettbrett uten forbindelse blir den ikke registrert. Dekk
   verkstedet med Wi-Fi der arbeidsstasjonene står.
7. **Tillat kameraet.** En bildekontroll åpner nettbrettets kamera gjennom nettleseren.
   Første gang spør nettleseren om lov til å bruke det: tillat det for Vantigos adresse.
   Uten kamera kan operatøren velge et bilde som er lagret på nettbrettet i stedet.

Tidtakeren teller på nettbrettet fra arbeidet startet, så et nettbrett som kobler seg til
igjen, viser riktig tid uten at serveren sender den.

## Bilder av kontrollene

En bildekontroll lagrer bildet i installasjonens [objektlagring](/nb/admin/object-storage/)
under omfanget `manufacturing-quality`. Uten objektlagring satt opp nektes det å ta bilde
(*storage_not_configured*), og en bildekontroll kan ikke registreres; alle andre
kontroller virker.

Hvert bilde er en JPEG eller PNG på høyst 10 MB, uten det som står i det om hvor og når
det ble tatt. Verkstedet gjør et stort bilde mindre før det sendes — høyst 2560 piksler på
den lengste siden, som JPEG — så de fleste bildene er godt under det. Opplastingen er den
ene forespørselen i Produksjon som slipper forbi plattformens grense på 1 MB: en omvendt
proxy foran Vantigo må slippe en forespørsel på litt over 10 MB gjennom til
`/api/v1/manufacturing/work-orders/*/photos`, ellers nektes et stort bilde der. Et bilde
er en kvalitetsregistrering: det beholdes like lenge som arbeidsordren, også når
resultatet registreres på nytt. Regn med omtrent en megabyte per bildekontroll per
arbeidsordre. Et bilde som er lastet opp, men aldri
registrert på en kontroll — operatøren tok det og gikk — fjernes etter et døgn av
arbeideren `manufacturing.object-deletions`, som går i arbeiderprosessen sammen med de
andre og prøver igjen til lageret bekrefter.

## Automatisk påfylling

Automatisk oppretting er **av** overalt til noen slår den på, per produkt og lagersted,
under **Automatisk påfylling** på produktets standardstykkliste (det krever Administrere
produksjonsordrer). Når den er på, oppretter nattkjøringen selv produksjonsordren et
forslag ber om — som utkast, eller også bekreftet med **Bekreft også** — uten at noen ser
på den først. Slå den bare på for produkter med jevn etterspørsel, og gi Administrere
produksjonsordrer til dem som skal avgjøre det.

Kjøringen oppretter aldri en ordre til for et produkt mens en fra et tidligere forslag
fortsatt er et utkast eller åpen på lagerstedet. For å se hva den gjorde, les de nyeste
kjøringene som databaseeier:

```sql
SELECT kind, site_id, status, requested_at, finished_at, suggestions, orders_created, sales_counted, error
FROM manufacturing.replenishment_runs
ORDER BY requested_at DESC
LIMIT 10;
```

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
8. Bekreft en ny ordre for produktet. Logg inn på et nettbrett som en operatør med Bruke
   Produksjon og Arbeide i verkstedet, åpne **Verksted** og velg arbeidsstasjonen: ordrens
   første arbeidsordre står i **Kø**. Åpne den og velg **Start**: tidtakeren løper. Velg
   **Ferdig**: arbeidsordren forsvinner fra køen.
9. Som arbeidsleder åpner du fanen **Arbeidsordrer** på ordren: operatørens tid står under
   **Tidsregistreringer**, og **Korriger** tilbys.
10. Bekreft en tredje ordre og velg **Planlegg** på den. Åpne **Produksjonsplan** og velg
    lagerstedet: ordrens arbeidsordrer er stolper på arbeidsstasjonens rad. Med Se
    produksjonskostnader viser ordren **Kostnad**-panelet, og **Oversikt** →
    **Rapporter** tilbyr **Produksjonskostnad**.
11. Med Ordre slått på merker du produktets standard stykkliste **Produser mot ordre**, og
    bekrefter en salgsordre med det i Ordre. Innen et minutt lister
    **Produksjonsordrer** en bekreftet ordre med opphav **Salgsordre** og salgsordrens
    nummer, og salgsordrens linje viser den.
12. Gi produktet et bestillingspunkt i Lager som er høyere enn det som er på lager på
    lagerstedet. Åpne **Produksjon** → **Forslag** og velg **Kjør nå**: innen et minutt
    er produktet listet som **Produsere** med begrunnelsen. Kryss det av og velg
    **Opprett utkast**: et utkast med opphav **Forslag** listes under
    **Produksjonsordrer**.

### Se nærmere på produksjon mot ordre

Hver bekreftede salgsordre etterlater én rad i `manufacturing.mto_requests`. Når en
salgsordrelinje sier **Ingen produksjonsordre laget**, eller ikke viser noe i det hele
tatt, leser du raden som databaseeier:

```sql
SELECT status, reasons, attempts, last_error, next_attempt_at, done_at
FROM manufacturing.mto_requests
WHERE sales_order_id = <salgsordrens id>;
```

- `pending` — ikke behandlet ennå; `attempts` og `last_error` sier om den har feilet og
  venter til `next_attempt_at`. En rad som blir stående som pending, betyr at ingen
  arbeidsprosess kjører.
- `done` eller `skipped` — behandlet; `reasons` lister hver linje som ble utelatt, med kode
  og grunn.
- `failed` — åtte forsøk feilet; `last_error` sier hvorfor. Rett årsaken og opprett
  produksjonsordrene manuelt fra salgsordrens linjer.
- `cancelled` — salgsordren ble kansellert før ordrene ble laget.
- Ingen rad — bekreftelsen ble ikke behandlet: Produksjon var av da salgsordren ble
  bekreftet, eller utboksens fordeler kjører ikke.

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
| Ingen **Start**, **Pause** eller **Ferdig** i verkstedet | Operatøren har verken Arbeide i verkstedet eller Administrere produksjonsordrer | Gi Arbeide i verkstedet |
| Ingen **Start**, og arbeidsordren sier at arbeidsstasjonen er **Blokkert** | Noen har blokkert den | **Opphev blokkering** når den går igjen |
| Ingen **Korriger** på en tidsregistrering | Personen mangler Administrere produksjonsordrer | Gi den til arbeidslederne |
| Å korrigere en tidsregistrering nektes | Registreringen er mer enn 60 dager gammel, ordren er ferdig eller kansellert, eller tidene overlapper en annen registrering for personen | Korriger innen 60 dager og før ordren lukkes; rett den andre registreringen først |
| Nettbrettet tilbyr aldri **Fortsett ved** | Nettleseren glemmer dataene sine: et privat vindu, eller dataene tømmes | Bruk et vanlig nettleservindu og behold nettstedsdataene |
| Tidtakeren stopper eller skjermen blir mørk | Nettbrettet låser seg | Forleng tidsavbruddet for skjermen, eller bruk kioskmodus |
| `permission denied for table work_orders` i loggen etter oppgraderingen | Rollen fikk rettigheter tabell for tabell, før `00158` | Kjør `GRANT`-setningene på nytt ([Databaseroller](/nb/admin/installation/#databaseroller)) |
| `permission denied for table work_order_slots` i loggen etter oppgraderingen | Rollen fikk rettigheter tabell for tabell, før `00159` | Kjør `GRANT`-setningene på nytt ([Databaseroller](/nb/admin/installation/#databaseroller)) |
| Ingen **Planlegg**, **Fjern plan** eller **Planlegg alle**, og stolpene kan ikke flyttes | Personen mangler Administrere produksjonsordrer | Gi den til planleggerne |
| Planleggingen nektes: en arbeidsordre får ikke plass innen 365 dager | Arbeidsstasjonen er stengt, eller fullbooket, det neste året: intet mønster, en lang stengning | Rett ukemønsteret eller stengningene, eller fjern planen på andre ordrer |
| Produksjonsplanen planlegger på feil klokkeslett | Virksomhetens tidssone er ikke fabrikkens | Sett den under **Innstillinger for arbeidsområdet** |
| Ingen **Kostnad**-panel på en ordre, ingen rapport **Produksjonskostnad** | Personen mangler Se produksjonskostnader | Gi den til dem som skal se kostnader |
| En produksjon nektes for beholdning | Lagerstedet har ikke nok av en komponent, eller lagerstedet er arkivert i Lager | Ta imot eller flytt varene, gjenåpne lagerstedet, eller rett med en justering i Lager |
| En bekreftet salgsordre får ingen produksjonsordre | Produktets standard stykkliste er ikke **Produser mot ordre**, Ordre eller arbeidsprosessen kjører ikke, linjen har allerede en ordre, eller stykklisten har ingen operasjoner og salgsordren navngir ikke noe lagersted | Merk stykklisten, start arbeidsprosessene, gi salgsordren et lagersted; grunnen for hver linje som ble utelatt, står i `manufacturing.mto_requests.reasons` |
| `permission denied for table mto_requests` i loggen etter oppgraderingen | Rollen fikk rettigheter tabell for tabell, før `00160` | Kjør `GRANT`-setningene på nytt ([Databaseroller](/nb/admin/installation/#databaseroller)) |
| **Forslag** er tom | Ingen påfylling i Lager har et bestillingspunkt, ingenting er lavt, eller ingen kjøring er gjort | Sett bestillingspunkter; velg **Kjør nå** |
| **Forslag** sier *Ikke kjørt ennå* etter en natt | Ingen arbeidsprosess kjører | Start arbeidsprosessen ([Installasjon](/nb/admin/installation/)) |
| **Forslag** sier *Salgsordrer er ikke regnet med* | Ordre er ikke slått på | Slå på Ordre, eller godta at etterspørselen fra salg holdes utenfor |
| En kjøring i `replenishment_runs` er `failed` | `error` sier hvorfor — Lager svarer ikke, eller et lagersted har mer enn 5000 påfyllinger med bestillingspunkt | Rett årsaken; neste kjøring lager forslagene på nytt |
| Automatisk oppretting laget ingen ordre | En ordre fra et tidligere forslag for produktet er fortsatt et utkast eller åpen, eller ordren ble nektet (logget av `manufacturing.replenishment`) | Fullfør eller kanseller den forrige ordren; les arbeiderens logg |
| `permission denied for table suggestions` i loggen etter oppgraderingen | Rollen fikk rettigheter tabell for tabell, før `00171` | Kjør `GRANT`-setningene på nytt ([Databaseroller](/nb/admin/installation/#databaseroller)) |
