---
title: "Management-lytter"
description: "Det private, bærerbeskyttede statusendepunktet et kontrollplan spør, leveringene i hendelsesutboksen som står fast og hvordan de prøves på nytt eller hoppes over, og hvordan den første eieren settes inn ved invitasjon."
sidebar:
  order: 40
sources:
  - apps/server/internal/management
  - apps/server/internal/events
---
Et kontrollplan som kjører mange Vantigo-instanser trenger å spørre hver av dem om
tre ting uten å holde en brukerøkt: hvilken versjon som kjører, om den første
eieren er satt inn, og hvor mye den brukes. Management-lytteren svarer nøyaktig
på det — og lar operatøren se og rydde leveringene i hendelsesutboksen som står fast
([Hendelsesutboksen](#hendelsesutboksen)) — og ikke noe annet.

| Variabel | Formål | Standard |
| --- | --- | --- |
| `MANAGEMENT_PORT` | Port for den private lytteren (må være en annen enn `PORT`) | ikke satt (slått av) |
| `MANAGEMENT_TOKEN` | Bærertoken, minst 32 tegn, ingen blanktegn | ikke satt (slått av) |

De to er én bryter: sett begge eller ingen — én alene er en konfigurasjonsfeil.
Lytteren kjører i kommandoene `api` og `server`; `worker` åpner den aldri, og
den offentlige lytteren betjener heller aldri `/management/status` (en sti som
ikke treffer noe der, faller gjennom til SPA-skallet, nøyaktig som enhver annen
ukjent sti gjør).

## Endepunktet

`GET /management/status` med `Authorization: Bearer <MANAGEMENT_TOKEN>`:

```json
{
  "version": "1.4.2",
  "bootstrap": "invited",
  "usage": { "users": 12, "activeUsers": 9, "databaseBytes": 734003200 }
}
```

| Felt | Betydning |
| --- | --- |
| `version` | Byggversjonen, den samme strengen `/health/ready` rapporterer |
| `bootstrap` | `pending`: ingen eier og ingen invitasjon som kan aksepteres. `invited`: en eierinvitasjon venter. `completed`: en eier finnes |
| `usage.users` | Alle kontoer |
| `usage.activeUsers` | Kontoer som kan logge inn: ikke deaktivert og ikke utestengt, og, mens SCIM er slått på, ikke avklargjort oppstrøms — unntatt en eier, som fortsatt telles som nødkontoen (break-glass) selv da. Det sier ingenting om hvor nylig de har vært aktive |
| `usage.databaseBytes` | `pg_database_size` for instansens database |

Det finnes ikke noe `usage.storageBytes`-felt — bruk er bare brukere, aktive brukere
og databasestørrelse.

Et feil eller manglende token er `401` med `WWW-Authenticate: Bearer`, for
`/management/status` og for alle andre stier på denne lytteren: bærerkontrollen
pakker inn hele handleren, foran rutingen, slik at en uautentisert kaller ikke
kan finne ut hvilke ruter som finnes. En avhengighet som feiler er `503`; årsaken
logges, aldri returneres. Et tomt konfigurert token — ikke mulig å nå gjennom
vanlig konfigurasjon, siden `MANAGEMENT_TOKEN` må være minst 32 tegn, men lytteren
stoler ikke på den oppstrøms kontrollen — stenger endepunktet i stedet for å åpne
det: hver forespørsel avvises da, `401`, uansett hva som presenteres.

## Hendelsesutboksen

Modulene forteller hverandre hva som har skjedd gjennom plattformens hendelsesutboks:
en hendelse skrives sammen med endringen som førte til den, og arbeideren
`events.dispatch` leverer den til hver modul som tar imot den — én **levering** per
hendelse og mottaker
([The domain-events outbox](/en/contributing/platform-packages/#the-domain-events-outbox)).
En levering som feiler, prøves på nytt etter 1 minutt, 5 minutter, 30 minutter,
2 timer og 12 timer, og deretter hver 12. time; etter det åttende forsøket som feiler,
er den **død** og prøves aldri igjen av seg selv. En feilet eller død levering holder
også tilbake de senere leveringene til samme mottaker for samme dokument, så de holder
rekkefølgen. Å rydde den er operatørens jobb, med tre kall på denne lytteren, bak det
samme bærertokenet. Ingen bruker i arbeidsområdet og ingen tillatelse når dem.

Utsenderen kjører der arbeiderne kjører — kommandoen `worker`, eller `api` med
`WORKERS_IN_PROCESS=1` — og har ingen innstilling av sin egen. Den logger det du skal
se etter:

| Logglinje | Nivå | Betydning |
| --- | --- | --- |
| `events delivery failed` | warn | Ett forsøk feilet; det prøves igjen etter ventetiden |
| `events delivery is dead` | error | Det åttende forsøket feilet; leveringen venter på et nytt forsøk eller at den hoppes over |
| `events deliveries wait for handlers that are not composed` | warn | Leveringer navngir en mottaker denne versjonen ikke lenger har, som regel etter en nedgradering eller en fjernet modul; hopp over dem |
| `events dispatch failed` | error | En runde i utsenderen feilet, oftest databasen; neste runde prøver igjen |

### List leveringene som står fast

`GET /management/events/deliveries?state=dead` (eller `state=failed`; uten `state`
begge) svarer med høyst 200 leveringer, de sist endrede først, med
`Cache-Control: no-store`. Enhver annen `state` er `400`.

```json
{
  "deliveries": [
    {
      "id": 812,
      "eventId": 5531,
      "event": "orders.sales_order.confirmed",
      "version": 1,
      "module": "orders",
      "aggregateId": "1042",
      "handler": "customers.timeline_orders",
      "state": "dead",
      "attempts": 8,
      "lastError": "…",
      "note": null,
      "nextAttemptAt": "2026-11-05T21:14:03Z",
      "occurredAt": "2026-11-02T09:12:44Z",
      "createdAt": "2026-11-02T09:12:47Z",
      "updatedAt": "2026-11-05T09:14:03Z"
    }
  ]
}
```

| Felt | Betydning |
| --- | --- |
| `id` | Leveringens id, som de to kallene under tar |
| `eventId`, `event`, `version`, `module`, `aggregateId` | Hendelsen: id-en, navnet og versjonen av innholdet, modulen som publiserte den, og id-en til det den gjelder |
| `handler` | Mottakeren den leveres til, `<modul>.<hva>` |
| `state` | `failed` (prøves igjen) eller `dead` |
| `attempts` | Forsøk som har feilet så langt |
| `lastError` | Den siste feilen, høyst 2 000 tegn |
| `note` | Operatørens merknad, satt når leveringen hoppes over |
| `nextAttemptAt` | Når en feilet levering prøves neste gang |
| `occurredAt` | Når hendelsen skjedde |
| `createdAt`, `updatedAt` | Når leveringen ble skrevet og sist endret |

Innholdet i en hendelse vises ikke. Det bærer bare id-er, tall, beløp og tilstander,
aldri en persons navn, adresse, e-post eller telefon.

### Prøv én på nytt

`POST /management/events/deliveries/{id}/retry` setter en `failed` eller `dead`
levering tilbake til ventende, forfallen med en gang: `204`. Forsøkene beholdes, så en
død levering som feiler igjen, er død igjen med en gang — rett årsaken først, prøv så
igjen. Ingen slik levering er `404`; én i en annen tilstand (ventende eller ferdig) er
`409`.

### Hopp over én

`POST /management/events/deliveries/{id}/skip` med `{"note": "…"}` markerer en levering
som ikke er ferdig, som ferdig uten å behandle den, beholder merknaden, og slipper
dermed leveringene som venter bak den: `204`. Merknaden er påkrevd, høyst 500 tegn
(ellers `400`). Ingen slik levering er `404`; én som alt er ferdig, er `409`. Å hoppe
over betyr at behandlingen aldri skjer — det mottakeren ville ha skrevet (en
bekreftelse lagt i kø, en linje på en tidslinje), blir ikke skrevet.

### Kontroller

List igjen: leveringen er borte fra `state=dead`. En som er prøvd på nytt, vises som
`failed` om den feiler igjen, eller `dead` med en gang om den var død før; en som lyktes,
listes ikke lenger.

Hendelser der alle leveringene er ferdige, slettes etter 30 dager.

## Hold den privat

Den offentlige lytteren avviser enhver forespørsel der `Host` ikke er `APP_URL`
sin. Management-lytteren har bevisst **ingen vertsfilter**, slik at den kan nås
via et tjenestenavn eller en pod-adresse. Bærertokenet er derfor dens eneste
beskyttelse: publiser aldri porten, rut den aldri fra internett, og begrens den
på nettverkslaget (i Kubernetes, en NetworkPolicy som bare slipper inn
kontrollplanets navnerom).

Å rotere tokenet betyr å starte prosessen på nytt med en ny verdi.

Hvis management-lytteren selv feiler etter oppstart — porten den bandt slutter
for eksempel å ta imot — logges feilen, og instansen fortsetter å betjene den
offentlige lytteren uansett: et kontrollplan ser «connection refused» mot en
ellers frisk pod, ikke et krasj.

## Sette inn den første eieren uten `/setup`

Kombiner lytteren med `BOOTSTRAP_OWNER_EMAIL`. Den anonyme `/setup`-flyten er da
stengt, og `bootstrap` går `pending` → `invited` → `completed` etter hvert som en
invitasjon sendes til den adressen og aksepteres.

Invitasjonen følger den konfigurerte adressen, ikke en fast: ved hver oppstart,
så lenge ingen eier finnes, trekker prosessen tilbake enhver ventende
eierinvitasjon adressert til en *annen* e-post enn den som er konfigurert nå, og
inviterer den nåværende adressen i stedet. Det er dette som lar en operatør som
skrev adressen feil rette den — start på nytt med rettelsen, og tokenet til den
feilskrevne mottakeren slutter å virke; neste oppstart sender til den rettede
adressen.

En ventende invitasjon til den nåværende adressen sendes ikke på nytt så lenge
den fortsatt er gyldig — en omstart sender den ikke igjen. Utløper den før noen
bruker den, utsteder neste oppstart en fersk en (et nytt token; det gamle godtas
ikke lenger). Hvis `BOOTSTRAP_OWNER_EMAIL` peker på en adresse en konto allerede
holder, utstedes aldri noen invitasjon: `/setup` forblir stengt og `bootstrap`
forblir `pending`, advarselen logges ved hver oppstart, ikke bare den første, og
den eneste utveien er å peke variabelen på en annen adresse. Hvis selve
e-postsendingen feiler, trekkes invitasjonen tilbake og feilen logges uten at
oppstarten stopper; neste oppstart prøver på nytt. En ventende invitasjon kan
ikke tvinges til å sendes på nytt før den utløper på annen måte enn ved å rette
adressen, siden både `/setup` og endepunktene for administrasjon av
eierinvitasjoner er stengt mens `BOOTSTRAP_OWNER_EMAIL` er satt og ingen eier
finnes. For å få en fersk invitasjon før den utløper — e-post som gikk tapt
underveis, eller en prosess som ble drept mellom utstedelse og sending — setter
du `BOOTSTRAP_OWNER_EMAIL` til en annen adresse du kontrollerer, starter på nytt
(det trekker den ventende invitasjonen tilbake), setter den tilbake til den
tiltenkte adressen og starter på nytt igjen; en vanlig omstart sender ikke på
nytt.

Så lenge ingen eier finnes, forsøker hver oppstart å sende invitasjonsmeldingen
før prosessen begynner å svare på forespørsler: med et SMTP-relé som ikke kan nås,
viser det seg som en treg oppstart, ikke som en e-postfeil noe sted i
svarveien.

Uansett hvilken vei som setter den inn — `/setup` eller en akseptert invitasjon
— får installasjonens første eier alltid både `Owner` og `SystemAdmin` og
skriver den samme bootstrap-markøren, slik at en installasjon der den første
eieren ble satt inn ved invitasjon er bootstrappet nøyaktig som en som ble satt
opp gjennom `/setup`.
