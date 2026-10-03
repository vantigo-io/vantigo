---
title: "Management-lytter"
description: "Det private, bærerbeskyttede statusendepunktet et kontrollplan spør, og hvordan den første eieren settes inn ved invitasjon."
sidebar:
  order: 40
sources:
  - apps/server/internal/management
---
Et kontrollplan som kjører mange Vantigo-instanser trenger å spørre hver av dem om
tre ting uten å holde en brukerøkt: hvilken versjon som kjører, om den første
eieren er satt inn, og hvor mye den brukes. Management-lytteren svarer nøyaktig
på det, og ikke noe annet.

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
