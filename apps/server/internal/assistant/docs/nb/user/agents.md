---
title: Virtuelle ansatte
description: Appen Virtuelle ansatte — hva en agent er og hva som virker i denne utgivelsen, å opprette en agent, Persona (navn, stil, språk, modell, bilde) og åpningslinjen med de tre formuleringene, å sette på pause, gjenoppta og slette, Roller (malen Skranke, instruksjoner, kanaler, den som overtar, Svarmodus og grenser), Tillatelser (alt er Av til Eieren hever det) og hvor KI-merket vises.
sidebar:
  order: 75
sources:
  - apps/agents/frontend/src
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
---

En **virtuell ansatt** er en KI-bruker i Vantigo med eget navn og et tydelig KI-merke. Den
gjør en del av en persons arbeid: den leser det en person med rollene dens kunne lest,
og skriver der den har fått lov. I denne utgivelsen har den én rolle, **Skranke**, som
svarer på samtalene som kommer inn på kanalene Eieren knytter til den. Reglene bak hver
skjerm står i [referansen](/en/reference/agents/); hvordan en administrator slår
modulen på, står i [Virtuelle ansatte](/nb/admin/agents/).

## Hva som virker i dag

Denne utgivelsen lar deg sette opp og forberede agenter: opprette en agent, gi den en
persona, legge til roller, knytte kanaler, velge Svarmodus og bestemme hva den får se og
endre. Delen som **starter en kjøring når en melding kommer**, oversikten over hver
kjøring og **gjennomgangskøen** for det en agent foreslår, kommer i en senere utgivelse.
Fram til da svarer ikke en agent noen; fanene **Bruk** og **Aktivitet** har ingenting å
vise, og Svarmodusen du velger lagres og tas i bruk når kjøringer finnes.

## Hvem som kan bruke den

Flisen **Virtuelle ansatte** i appvelgeren vises for alle med en rolle som har **Se
virtuelle ansatte** (`agents:view`). **Administrere virtuelle ansatte** (`agents:manage`)
lar deg opprette, endre, sette på pause og slette agenter. Det er en sensitiv tillatelse
som bare en Eier kan dele ut. Noe er fortsatt Eierens selv med `agents:manage`: å heve en
tillatelse, å velge **Auto** eller heve den, og å knytte en kanal. Å lese en agents
kjøringer krever **Lese virtuelle ansattes aktivitet** (`agents:activity`), og å godkjenne
det en agent foreslår krever **Gjennomgå virtuelle ansattes arbeid** (`agents:review`).

## Opprett en agent

1. Åpne **Virtuelle ansatte** og trykk **Ny virtuell ansatt**.
2. Fyll ut **Persona** ([under](#persona)) og trykk **Opprett**.

Firmanavnet må være satt først, fordi agenten presenterer seg med det
([Åpningslinjen](#åpningslinjen)). Er det ikke satt, avviser skjermen med *Sett firmaets navn
i innstillingene først*; en administrator setter det i innstillingene for agenter
([Virtuelle ansatte](/nb/admin/agents/#sett-firmaets-navn)). Den nye agenten har ingen
roller, ingen kanaler og alle tillatelser **Av**, så den kan ikke gjøre noe før du har
fullført oppsettet.

## Persona

Åpne agenten og velg fanen **Persona**.

| Felt | Hva det er |
| --- | --- |
| **Navn** | Det kunder og kolleger ser, opptil 60 tegn. |
| **Stil** | Hvordan agenten skriver: tone, formalitet, ord som skal brukes eller unngås. Opptil 1 500 tegn. |
| **Språk** | **Kundens** svarer på kundens språk og faller tilbake til bokmål; **Norsk bokmål** og **Engelsk** svarer alltid på det språket. |
| **Modell** | KI-modellen som kjører agenten. Tom bruker arbeidsområdets valg for oppgaven *Agent i arbeid* ([Konfigurasjon](/nb/admin/configuration/)). |
| **Bilde** | En PNG eller JPEG på høyst 5 MB, vist på agentens avatar. |

Hvis du endrer navnet, får agentens bruker nytt navn overalt det vises.

### Åpningslinjen

Hver melding agenten sender åpner med en linje som sier at den er en KI, og hvem den
jobber for. Du velger en av tre formuleringer; firmanavnet fra innstillingene settes inn.

| Formulering | Norsk | Engelsk |
| --- | --- | --- |
| **Assistent** | «Hanna» er «Hansen Rør» sin virtuelle assistent (KI). | «Hanna» is «Hansen Rør»'s virtual assistant (AI). |
| **Samtale** | Du snakker med «Hanna», en KI-assistent hos «Hansen Rør». | You are talking to «Hanna», an AI assistant at «Hansen Rør». |
| **Kompakt** | «Hanna» (KI) · virtuell assistent, «Hansen Rør». | «Hanna» (KI) · virtuell assistent, «Hansen Rør». |

Den kompakte linjen er lik på begge språk. Linjen legges til av Vantigo, ikke skrevet av
modellen, så instruksjonene kan ikke fjerne den.

## Sett på pause, gjenoppta og slett

- **Pause** hindrer agenten i å starte nye kjøringer. Trykk **Gjenoppta** for at den skal
  jobbe igjen.
- **Slett** avslutter agenten for godt: rollene stopper, kanalene frigjøres så en annen
  rolle kan få dem, tilgangen tas bort og brukeren deaktiveres. Brukeren beholdes, så
  tidligere historikk fortsatt viser hvem som skrev. En slettet agent kan ikke hentes
  tilbake; opprett en ny.

## Roller

En rolle er en jobb agenten gjør. Åpne fanen **Roller** og trykk **Legg til rolle**. I denne
utgivelsen er den eneste malen **Skranke**, som starter med instruksjoner om å være
høflig, kort og konkret, og å overlate til en kollega når agenten ikke vet eller ikke
kan avgjøre.

| Innstilling | Hva den gjør |
| --- | --- |
| **Navn** | Opptil 80 tegn. |
| **Instruksjoner** | Hva agenten skal og ikke skal gjøre, opptil 8 000 tegn. Står de tomme, brukes malens instruksjoner. |
| **Kanaler** | Kanalene i Kommunikasjon rollen svarer på. En kanal hører til høyst én rolle, så to agenter svarer aldri på samme kanal. Å knytte en kanal er Eierens. |
| **Overtas av** | Hvem en samtale gis til når agenten overleverer den. Tom betyr at den går til Innboksen, uten ansvarlig. |
| **Svarmodus** | Hvordan agentens svar avgjøres ([under](#svarmodus)). |
| **Grenser** | Flest verktøykall og runder én kjøring kan bruke, og flest svar rollen sender per dag. |

Endringer i en rolle gjelder fra agentens neste skritt.

Er den som overtar deaktivert eller ikke lenger har lov til å se samtaler, går overleveringen
til Innboksen, uten ansvarlig, og rollesiden viser en advarsel ved personen. Velg noen andre
for å få advarselen bort.

### Svarmodus

**Svarmodus** bestemmer hva som skjer med et svar agenten har skrevet:

- **Gjennomgang** (standard): en person godkjenner hvert svar før det sendes.
- **Auto over X %**: agenten sender svaret når sikkerheten er over grensen, ellers
  gjennomgår en person det. Grensen er fra 50 til 100 prosent.
- **Auto**: agenten sender svarene selv.

Modusen lagres nå og tas i bruk når kjøringer finnes, i neste utgivelse. Å velge **Auto**
eller **Auto over X %**, og å heve modusen eller senke grensen senere, er Eierens alene.
Å gå tilbake til **Gjennomgang** kan alle med `agents:manage`. Uansett modus blir
endringer agenten gjør i Vantigo alltid gjennomgått først.

## Tillatelser

Åpne fanen **Tillatelser**. **Hver tillatelse for en agent er Av til Eieren hever den**,
uansett hvilke roller agentens bruker har. Hver tillatelse kan være **Av**, **Les** eller
**Endre**, og agenten får aldri mer enn det laveste av
[Identitet-rollene](/nb/user/workspace-administration/#agenter-blant-brukerne) sine,
nivået og det malen for rollen tillater.

Fanen begynner med **Trenger**: det rollene du la til trenger for å virke, hver med nivået
den vil ha og en knapp **Hev** når agenten har mindre. Bare Eieren kan heve; alle med
`agents:manage` kan senke. Noen tillatelser har ikke noe nivå og vises som låst: de kan
aldri gis til en agent ([Virtuelle ansatte](/nb/admin/agents/#nivåene)).

Agenter har Identitet-roller som en person og får dem under **Roller og tilgang**, men
aldri **Eier** eller **SystemAdmin**.

## Hvor du ser KI-merket

En agents avatar har et lite **KI**-merke overalt den vises, så ingen tar den for en
person: i valget av ansvarlig for en samtale i [Kommunikasjon](/nb/user/communications/), i
samtalelisten og blant brukerne i
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#agenter-blant-brukerne).
