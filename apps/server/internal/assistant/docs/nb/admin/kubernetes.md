---
title: Kubernetes
description: "Å kjøre Vantigo på Kubernetes: probene på management-porten og en oppstartsprobe, tømmingen ved SIGTERM og avslutningsfristen den trenger, hvor mange replikaer av server og worker, klokken som følger databasen, databaseøkter i UTC, og rullerende oppgraderinger med server, aldri api."
sidebar:
  order: 12
sources:
  - apps/server/internal/dbclock
  - apps/server/internal/health
  - apps/server/internal/db/db.go
  - apps/server/internal/db/migrate.go
---

Vantigo kjører på Kubernetes som det samme imaget det kjører som under Compose: én
leietaker er én installasjon, med sin egen database, og podene er kommandoene i den ene
binæren. Denne siden er det en Deployment trenger fra applikasjonen for å fortsette å
betjene brukerne mens podene og klyngens noder byttes ut.
[Installasjonsveiledningen](/nb/admin/installation/) er fortsatt referansen for hver
innstilling; denne siden legger til det som er særegent for en klynge.

## Forutsetninger

- En utgivelse migreres én gang, av en Job som kjører `vantigo migrate` med
  `MIGRATIONS_DATABASE_URL`, rollen som eier skjemaet
  ([Databaseroller](/nb/admin/installation/#databaseroller)).
- Podene som betjener brukerne kjører `server`, bakgrunnsjobbene kjører `worker`, og
  ingenting kjører `api` ([Rullerende oppgraderinger](#rullerende-oppgraderinger)).
- `MANAGEMENT_PORT` er satt på hver pod, slik at hver av dem har den private lytteren
  probene bruker ([Management-lytter](/nb/admin/management-listener/)).

## Probene på management-porten

Den offentlige lytteren svarer bare på en `Host` som er verten i `APP_URL`, så en
`httpGet`-probe fra kubelet, som sender podens adresse, får `400` der. Management-lytteren
har ikke noe vertsfilter og betjener `GET /health/live` og `GET /health/ready` uten token,
med de samme sjekkene som den offentlige lytteren
([Helseprober](/nb/admin/installation/#helseprober)), i alle kommandoer — `server` og
`worker` likt. Sett `MANAGEMENT_PORT` (for eksempel `9090`); et `MANAGEMENT_TOKEN` trengs
bare for kontrollplanets egne ruter, ikke for probene.

```yaml
ports:
  - name: management
    containerPort: 9090
startupProbe:
  httpGet: { path: /health/ready, port: management }
  periodSeconds: 5
  failureThreshold: 720 # en time: så lenge en migrering kan ta
readinessProbe:
  httpGet: { path: /health/ready, port: management }
  periodSeconds: 5
  failureThreshold: 2
livenessProbe:
  httpGet: { path: /health/live, port: management }
  periodSeconds: 10
  failureThreshold: 6
```

Bruk en `startupProbe` med en høy feilterskel i stedet for en stor
`initialDelaySeconds`. En pod med `server` eller `worker` som starter før migreringsjobben
er ferdig, venter på skjemaet og gjør ikke noe annet
([Rullerende oppgraderinger](#rullerende-oppgraderinger)): den lever, og
beredskapssjekken feiler med `schema` så lenge migreringen tar. Liveness-proben rører
ikke databasen, så en treg database starter aldri en pod på nytt.

Publiser aldri management-porten, og rut den aldri fra en Ingress. Slipp den inn i en
NetworkPolicy bare fra kontrollplanets navnerom; probene fra kubelet kommer fra noden.

## Tømming og avslutningsfristen

Ved SIGTERM gjør en pod dette:

1. Den lar beredskapssjekken feile med en gang, med `draining` i `failing`.
2. Den fortsetter å betjene forespørsler i `SHUTDOWN_DRAIN_DELAY`, slik at
   endpoints-kontrolleren og ingressen tar den ut av rotasjon mens den fortsatt svarer
   på det som når fram.
3. Den lukker hver forbindelse for sanntidsoppdateringer med `1001`, slik at hver
   nettleser kobler seg til en annen pod — etter en tilfeldig ventetid av sin egen.
4. Den stopper bakgrunnsjobbene og tømmer pågående forespørsler, og lukker så
   tilkoblingene sine, alt innenfor én `SHUTDOWN_TIMEOUT`.

`SHUTDOWN_DRAIN_DELAY` er `0s` som standard, som passer for Compose; sett den til rundt
`10s` på Kubernetes. Gi så poden nok tid til begge:

```text
terminationGracePeriodSeconds ≥ SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT + 5s
```

Med `SHUTDOWN_DRAIN_DELAY=10s` og standardverdien `SHUTDOWN_TIMEOUT=30s` blir det `45`.
En kortere frist lar SIGKILL fra kubelet avbryte en operasjon midt i, og neste forsøk
gjentar den da. En andre SIGTERM under tømmingen avslutter prosessen med en gang.

For å kontrollere: slett en pod mens du følger loggen dens. Først kommer
`shutdown signal received`, så `draining` etter ventetiden, så `shutdown complete`.

## Replikaer

- **`server`**, podene som betjener brukerne: minst to for en leietaker som må holde seg
  oppe mens en node byttes, spredt over noder og soner, med et PodDisruptionBudget på
  `minAvailable: 1`. `server` migrerer aldri og kjører aldri en bakgrunnsjobb.
  Sanntidsoppdateringer når hver replika gjennom PostgreSQL, så en nettleser kan være
  koblet til hvilken som helst av dem.
- **`worker`**, bakgrunnsjobbene: to er trygt. Hver bakgrunnsjobb tar enten en
  advisory lock, slik at den kjører på én pod per runde, eller tar én rad om gangen med
  en tidsbegrenset leieavtale, slik at et hvilket som helst antall poder deler arbeidet
  uten å gjøre det to ganger ([Bakgrunnsjobber og
  skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)).

Hver pod åpner sin egen databasepool, dimensjonert med `pool_max_conns` på
`DATABASE_URL`; tell med hver replika i
[tilkoblingsbudsjettet](/nb/admin/installation/#databasens-tilkoblingsbudsjett).

## Klokken

Leieavtaler, krav og vinduer for hastighetsgrenser regnes ut i hver prosess og
sammenlignes med dem en annen prosess har skrevet, så hver pod må lese samme tid. Hver
prosess følger derfor databasens klokke, ikke nodens: den måler forskjellen med
`SELECT clock_timestamp()`, korrigert for halve rundturen, før noen bakgrunnsjobb starter
og hvert minutt etterpå. Den første målingen tas i bruk fullt ut; senere målinger flytter
klokken høyst 100 millisekunder i minuttet, mot medianen av de fem siste, så én treg
rundtur ikke kan få den til å hoppe, og klokken går aldri bakover.

Forskjellen er metrikken `vantigo.clock.offset`, i sekunder, som eksporteres sammen med
de andre metrikkene ([E-post og
observerbarhet](/nb/admin/installation/#e-post-og-observerbarhet)). En forskjell utover
`CLOCK_MAX_SKEW` (`5s` som standard) logger
`clock: this host's clock is off from the database's` som advarsel, høyst én gang i
timen. Den får ikke beredskapssjekken til å feile — poden har allerede riktig tid — men
den sier at nodens tidssynkronisering ikke virker. Hvis den første målingen feiler,
starter prosessen på sin egen klokke, logger det, og tar i bruk databasens så snart en
måling lykkes.

## Databaseøkter i UTC

Hver tilkobling applikasjonen åpner — poolen, migreringstilkoblingen og
sanntidsoppdateringenes egne tilkoblinger — setter øktens `TimeZone` til `UTC`, og
erstatter en `TimeZone` som tilkoblingsstrengen eventuelt setter. Lagrede tidspunkter er
øyeblikk uansett sone, men en dagsgrense en spørring trekker, avhenger av øktens sone, og
en administrert PostgreSQL kan ha en annen sone som standard. Arbeidsområdets egen
tidssone, satt under Administrer arbeidsområde, brukes av applikasjonen der en regel
eller et skjermbilde nevner den, aldri av økten. Det er ingenting å konfigurere.

## Rullerende oppgraderinger

1. Kjør utgivelsens migreringsjobb, `vantigo migrate`, og vent til den har lyktes.
2. Flytt Deploymentene for `server` og `worker` over på det nye imaget.

En ny pod som starter før jobben er ferdig, venter på den: til databasen har minst
migreringene binæren har med seg, kjører den ikke annet enn probene sine, spør hvert 2.
sekund og logger `waiting for the schema to be migrated` hvert 30. Beredskapssjekken
feiler med `schema` så lenge, så utrullingen stopper av seg selv, og går ikke videre hvis
jobben feiler. En pod av forrige utgivelse på det nyere skjemaet starter med en gang: en
utgivelses skjema virker med forrige utgivelses binær.

Kjør `server`, aldri `api`, for hver replika som kan bli startet på nytt under en
utrulling. `api` migrerer før den betjener forespørsler, og en gammel `api`-pod som
startes på nytt på et nyere skjema, ville be om å migrere en database med versjoner den
ikke kjenner.

## Hva kan gå galt

| Symptom | Årsak |
| --- | --- |
| Hver probe svarer `400` | Probene peker mot `PORT`, den offentlige lytteren, som avviser podens adresse. Pek dem mot management-porten |
| En ny pod blir aldri klar, og beredskapssjekken feiler med `schema` | Migreringsjobben har ikke kjørt eller har feilet. Loggen dens sier hvorfor |
| Brukerne ser en brutt forbindelse ved hver utrulling | `SHUTDOWN_DRAIN_DELAY` er `0s`, eller fristen er kortere enn tømmingen trenger |
| `clock: this host's clock is off from the database's` i loggen | NTP på noden virker ikke. Poden holder databasens tid; rett opp noden |
