---
title: Virtuelle ansatte
description: Appen Virtuelle ansatte — hva en agent er, agenten i arbeid (når den svarer, hva den leser, hva den kan sende selv, åpningslinjen, overleveringen og å gi en samtale tilbake), å opprette en agent, Persona (navn, stil, språk, modell, bilde) og åpningslinjen med de tre formuleringene, å sette på pause, gjenoppta og slette, Roller (malen Skranke, instruksjoner, kanaler, den som overtar, Svarmodus og grenser), Tillatelser (alt er Av til Eieren hever det) og hvor KI-merket vises.
sidebar:
  order: 75
sources:
  - apps/agents/frontend/src
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
---

En **virtuell ansatt** er en KI-bruker i Vantigo med eget navn og et tydelig KI-merke. Den
gjør en del av en persons arbeid: den leser det en person med rollene dens kunne lest,
og skriver der den har fått lov. I denne utgivelsen har den én rolle, **Skranke**, som
svarer på samtalene som kommer inn på kanalene Eieren knytter til den. Reglene bak hver
skjerm står i [referansen](/en/reference/agents/); hvordan en administrator slår
modulen på, står i [Virtuelle ansatte](/nb/admin/agents/).

## Agenten i arbeid

Når en agent har en rolle knyttet til en kanal, og Eieren har hevet tillatelsene rollen trenger
([Tillatelser](#tillatelser)), svarer den på samtalene som kommer inn på kanalen, på e-post og på
Messenger, som seg selv.

- **Når den starter.** Litt etter kundens melding: den venter 20 sekunder, slik at tre korte
  meldinger gir ett svar, og aldri mer enn to minutter til sammen. En agent på pause starter ingenting.
  En automatisk melding (et fraværssvar) blir aldri besvart.
- **Hvem sin samtale.** Den svarer på en samtale som er **uten ansvarlig eller tildelt den selv**. En
  samtale tildelt en kollega overlater den til kollegaen. **Tildel en samtale til agenten**, så svarer
  den på den, også etter en overlevering.
- **Hva den leser.** Samtalen, kundens ordrer, fakturaer og kontakter, produktkatalogen og artiklene i
  [Kunnskap](/nb/user/knowledge/) som agenten har lov til å lese. Av en kundes data leser den bare
  kunden samtalen hører til. Er kunden koblet av kundekoblingen og ikke av en person, må avsenderen i
  tillegg være en av kundens kontakter; ellers har agenten **ingen kunde** i den samtalen og svarer fra
  Kunnskap og produktkatalogen, spør hvem kunden er, eller leverer samtalen videre.
- **Hva den skriver.** Et svar til den som skrev sist og til ingen andre: **aldri svar til alle**, og en
  kopimottaker blir aldri kopiert. Den kan også skrive et internt notat, merke samtalen og levere den
  videre. En endring av en post, som å lukke samtalen, blir **alltid gjennomgått først**.
- **Poengsum og kilder.** Hvert svar har en poengsum fra 0 til 100 og kildene agenten leste for det. Et
  svar uten en kilde agenten faktisk leste får poengsummen 0, og selve samtalen teller aldri som kilde.
- **Åpningslinjen.** Hver melding agenten sender begynner med en linje som sier hvem som skrev den og
  at det er en KI ([åpningslinjen](#åpningslinjen)). Den kan ikke fjernes.

### Hva som skjer med et svar

**Svarmodusen** til rollen ([under](#svarmodus)) avgjør, når agenten har skrevet og reglene er sjekket:

- I **Gjennomgang** venter hvert svar på en person. Et internt notat i samtalen sier at et svar venter
  på gjennomgang. Køskjermene for å godkjenne, redigere og forkaste kommer i neste utgivelse; fram til
  da sender ingenting et svar som venter.
- I **Auto over X %** sendes svaret når poengsummen er minst X og ingen regel holdt det igjen.
- I **Auto** sendes det med mindre en av reglene som alltid gjelder stoppet det.

Et svar **holdes for en person i alle moduser** når det ville sende en kundes data til noen som ikke er
kundens kontakt, legge ved en annen kundes dokument, eller si at agenten er et menneske. Det
**sendes ikke i det hele tatt, og samtalen leveres videre**, når Messenger-vinduet er lukket eller bare
«menneskelig agent»-utvidelsen er igjen (agenten sender aldri en melding som menneskelig agent), når
meldingen den svarer på er automatisk eller kommer fra en av dine egne kanaler, når agenten allerede har
sendt **rollens antall svar i dag** i samtalen, eller etter seks kjøringer på en time. I Gjennomgang og
Auto over X % holdes et svar også tilbake når det ikke har noen kilde, når det er første melding fra en
ny avsender, når samtalen har en klagemerkelapp og når en andre kontroll finner et løfte instruksjonene
ikke tillater. Merkelappene som betyr klage og det daglige tokenbudsjettet settes foreløpig gjennom
API-et; skjermene for dem kommer i neste utgivelse.

### Overlevering

En agent som ikke vet, ikke kan avgjøre, blir bedt om en person, når grensene sine eller ikke når
KI-modellen **leverer samtalen videre**: den skriver et internt notat med det kunden vil, og tildeler
samtalen til rollens overtaker, eller til ingen (Innboksen). Hvis den skrev et svar den ikke kunne
sende, står det i notatet. Etter det **lar agenten samtalen være**, også når kunden skriver igjen, til en
person **svarer** i den eller **tildeler** den, eller den **gis tilbake til agenten**. Å gi tilbake er
foreløpig `DELETE /api/v1/agents/conversations/{id}/hand-off`; knappen **Gi tilbake til agenten** i
tråden kommer i neste utgivelse.

Tar en kollega samtalen mens agenten skriver, sendes ikke agentens svar: samtalen er kollegaens.

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

Å velge **Auto** eller **Auto over X %**, og å heve modusen eller senke grensen senere, er
Eierens alene.
Å gå tilbake til **Gjennomgang** kan alle med `agents:manage`. Uansett modus blir
endringer agenten gjør i Vantigo alltid gjennomgått først, og reglene i
[Hva som skjer med et svar](#hva-som-skjer-med-et-svar) gjelder.

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
