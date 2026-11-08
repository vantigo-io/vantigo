---
title: Virtuelle ansatte
description: Å slå på Virtuelle ansatte — modulene og KI-leverandøren den trenger, de fire tillatelsene og hvilke endringer som er Eierens, firmanavnet og oppbevaringen, å opprette den første agenten, modellen per agent, å gi agenten Identitet-roller, nivåene (Av som standard, hva skranken trenger, hva som er låst for alle agenter), tillatelseslisten og kundeavgrensningen, hva som bør holdes utenfor Kunnskap, personvernerklæringen, å sette en agent på pause og slette den; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 52
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/agents/frontend/src
---

Modulen Virtuelle ansatte lar virksomheten sette KI-agenter i arbeid under egne navn. Denne
siden dekker å slå den på og sette den opp; hvordan folk bruker den, står i
[Virtuelle ansatte](/nb/user/agents/), og alle reglene i
[referansen](/en/reference/agents/). I denne utgivelsen kan en agent settes opp og
forberedes. Utløseren som får den til å svare på en melding, oversikten over kjøringene
og gjennomgangskøen kommer i en senere utgivelse.

## Før du begynner

- **`agents` i `MODULES`, sammen med `communications`.** `MODULES` avviser `agents` uten
  `communications` ved oppstart (`agents requires communications`): en agent svarer på
  samtaler. Når `MODULES` ikke er satt, er alle moduler på.
- **En KI-leverandør med en modell som støtter verktøykall.** Sett `AI_PROVIDER` og resten
  som beskrevet i [Konfigurasjon](/nb/admin/configuration/). En modell som ikke kan
  returnere verktøykall, kan ikke arbeide slik en agent gjør. Uten leverandør viser appen
  *KI er ikke satt opp*, og ingen agent kan kjøres.
- **En Eier.** Å heve et nivå, velge Auto og knytte en kanal er Eierens.
- **Minst én kanal i Kommunikasjon** som agenten kan svare på, satt opp som beskrevet i
  [Kommunikasjon](/nb/user/communications/).

## Tillatelser

Gi rollene tillatelsene under **Roller og tilgang**. Ingen har dem som standard.

| Tillatelse | Hva den åpner | Hvem som kan dele den ut |
| --- | --- | --- |
| `agents:view` | Se agentene, personaene, rollene og innstillingene. | Alle som kan redigere roller. |
| `agents:manage` | Opprette agenter, redigere personaer og instruksjoner, sette dem på pause og slette dem. | Sensitiv: bare en Eier. |
| `agents:review` | Godkjenne eller forkaste det en agent foreslår i gjennomgangskøen. | Alle som kan redigere roller. |
| `agents:activity` | Lese en agents kjøringer og utskrifter, som inneholder kunders data. | Sensitiv: bare en Eier. |

Tre endringer er fortsatt **Eierens**, også for den som har `agents:manage`, fordi en person
med smale roller selv ellers kunne styre en agent med brede: å heve et nivå i fanen
Tillatelser, å velge **Auto** eller **Auto over X %** (eller heve det), og å knytte en
kanal. Å senke og å løse en kanal er ikke det.

## Sett firmaets navn

Agenten presenterer seg med firmanavnet, så ingen agent kan opprettes før det er satt.

1. Åpne **Virtuelle ansatte** og innstillingene.
2. Skriv inn **Firmanavn** (1 til 120 tegn) slik kundene kjenner virksomheten.
3. Sett **Oppbevaring**: hvor mange måneder en kjøring beholdes, fra 1 til 60. Standard er 6.
4. Lagre.

Kjøringer registreres ikke i denne utgivelsen, så oppbevaringen har ingenting å fjerne ennå;
den begynner å gjelde med utgivelsen som registrerer dem.

## Opprett den første agenten

1. Kontroller at innstillingene over er lagret.
2. Trykk **Ny virtuell ansatt**, fyll ut **Persona** og trykk **Opprett**.
3. Under **Roller**, trykk **Legg til rolle**, velg malen **Skranke**, skriv eller behold
   instruksjonene og velg hvem som overtar.
4. Knytt kanalene rollen skal svare på. En kanal hører til én rolle om gangen.
5. Gi agentens bruker Identitet-roller under **Roller og tilgang**
   ([under](#gi-agenten-identitet-roller)).
6. Under **Tillatelser**, trykk på hev-knappene under **Trenger** til listen er oppfylt
   ([nivåene](#nivåene)).
7. La Svarmodus stå på **Gjennomgang**.

## Modellen per agent

Under **Arbeidsområde** › **Integrasjoner** › **KI-modeller** står to oppgaver fra denne
modulen:

- **Agent i arbeid** (`agents.run`) kjører en agent på en samtale. Den velges **per
  agent**: oppgavens rad der beholder arbeidsområdets valg, og hver agent kan ha en egen
  modell i fanen **Persona**, som går foran ([Konfigurasjon](/nb/admin/configuration/)). Den
  trenger en modell som støtter verktøykall.
- **Agentens innholdskontroll** (`agents.check`) kontrollerer et utkast mot et fast
  kriteriesett før det sendes. Den bruker arbeidsområdets valg og får bare instruksjonene og
  utkastet, aldri samtalen.

## Gi agenten Identitet-roller

En agent er en bruker, og den har Identitet-roller som en person. Gi brukeren under **Roller
og tilgang** rollene som har tillatelsene den skal bruke, for eksempel å lese kunder og
svare i Kommunikasjon. Et nivå kan ikke gi en tillatelse agentens roller ikke har. En agent
kan aldri ha **Eier**, **SystemAdmin**, en delegering eller et forvalteransvar; skjermen
avviser dem
([Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#agenter-blant-brukerne)).

## Nivåene

Hver tillatelse for en agent er **Av** til Eieren hever den til **Les** eller **Endre**.
Listen **Trenger** i en agents fane Tillatelser viser hva rollene trenger. **Skranken**
trenger:

| Område | Tillatelse | Nivå |
| --- | --- | --- |
| Kommunikasjon | Samtaler: se, svare og administrere | Endre |
| Kunder | Se, se kontakter, se tilknytninger | Les |
| Ordre | Tilgang | Les |
| Fakturaer | Tilgang | Les |
| Produkter | Se produkter, varianter, priser, kategorier og avgiftskategorier | Les |
| Kunnskap | Bruke Kunnskap (artikler) | Les |

En skriving som ber om bekreftelse, avvises når nivået bare er **Les**, og derfor er
samtaletillatelsene **Endre**.

### Hva som er låst for alle agenter

Ingen nivåer kan gis for disse; skjermen viser dem som låst, og serveren avviser med
`level_locked`:

- Ansatte: godkjenne, administrere og personopplysninger.
- Kunder: personopplysninger.
- Identitet: administrere.
- Alle godkjenninger i Timer, Utlegg og Ansatte.
- Fakturaer: utstede og betalinger.
- Agentenes egne tillatelser.

### Tillatelseslisten og kundeavgrensningen

En rolle kan bare kalle operasjonene i malen sin, uansett nivå. Skranken kan arbeide med sin
egen samtale, lese samtalens kunde, ordre, fakturaer og kontakter, lese Produkter og søke i
Kunnskap; den kommer ikke til Ansatte, Timer eller resten av arbeidsområdet. Alt som
returnerer en kundes data, er knyttet til samtalens kunde: **en agent leser aldri en annen
kundes data**. Hele listen står i
[referansen](/en/reference/agents/#the-front-desk-template).

## Hva som bør holdes utenfor agentens rekkevidde

**Kunnskap:** en agent kan søke i artikler den har lov til å lese. Ikke del artikler som
inneholder kundedata med en agent; merk dem **Skjult for assistenten**, så finner ikke en agent dem
([Kunnskap](/nb/user/knowledge/)).

## Personvernerklæringen

Fortell kundene i personvernerklæringen at en KI-assistent svarer på disse kanalene, og at
innholdet i samtalen, instruksjonene og det agenten leser om kunden sendes til
KI-leverandøren, som til enhver databehandler. Agenten sier det også selv i åpningslinjen i
hver melding. Hvor dataene går for din installasjon, står i
[Konfigurasjon](/nb/admin/configuration/); en modell på eget nett sender ingenting ut av det.

## Sett en agent på pause og slett den

- **Pause** stopper nye kjøringer; roller, kanaler og nivåer blir stående. **Gjenoppta**
  starter dem igjen.
- **Slett** stopper rollene, frigjør kanalene for en annen rolle, avslutter agentens tilganger
  og deaktiverer brukeren. Brukeren beholdes så tidligere historikk fortsatt stemmer. Det kan
  ikke angres.

## Kontroller

1. Agenten står på **Virtuelle ansatte** som aktiv, med bilde og KI-merke.
2. Listen **Trenger** viser hver rad som oppfylt, og oppsettet er klart.
3. I Kommunikasjon står agenten med KI-merket i valget av ansvarlig.
4. Rollesiden viser ingen advarsel ved den som overtar.

## Hva som kan gå galt

| Du ser | Hvorfor | Hva du gjør |
| --- | --- | --- |
| `company_name_required` (*Sett firmaets navn i innstillingene først*) | Du oppretter en agent før firmanavnet er satt. | Lagre navnet i innstillingene, og opprett så agenten. |
| `channel_bound` | En kanal besvares allerede av en annen rolle. | Løs den der først, eller velg en annen kanal. |
| `owner_required` | Du hevet et nivå, valgte Auto eller knyttet en kanal uten å være Eier. | Be en Eier. |
| `level_locked` | Tillatelsen er låst for alle agenter. | Det finnes ikke noe nivå; la den stå Av. |
| `version_conflict` | Noen lagret samme agent, rolle eller innstillinger etter at du åpnet den. | Last på nytt og gjør endringen igjen. |
| *KI er ikke satt opp* | `AI_PROVIDER` er ikke satt, eller modellen kan ikke nås. | Sett opp leverandøren ([Konfigurasjon](/nb/admin/configuration/)). |
| En advarsel ved den som overtar | Personen er deaktivert eller har ikke lenger lov til å se samtaler. | Velg noen andre. Fram til da går overleverte samtaler til Innboksen, uten ansvarlig. |
