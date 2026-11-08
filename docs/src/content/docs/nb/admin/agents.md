---
title: Virtuelle ansatte
description: Å slå på Virtuelle ansatte — modulene og KI-leverandøren den trenger, de fire tillatelsene og hvilke endringer som er Eierens, firmanavnet og oppbevaringen, å opprette den første agenten, modellen per agent, å gi agenten Identitet-roller, nivåene (Av som standard, hva skranken trenger, hva som er låst for alle agenter), tillatelseslisten og kundeavgrensningen, hva som bør holdes utenfor Kunnskap, personvernerklæringen, hvem som gjennomgår, den ukentlige kontrollen av instruksjonene, oppbevaring og sletting, å sette en agent på pause og slette den; hvordan du kontrollerer det, og hva som kan gå galt.
sidebar:
  order: 52
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
  - apps/server/internal/db/migrations/00147_agents_review.sql
  - apps/server/internal/db/migrations/00148_agents_memory.sql
  - apps/server/internal/config
  - apps/agents/frontend/src
---

Modulen Virtuelle ansatte lar virksomheten sette KI-agenter i arbeid under egne navn. Denne
siden dekker å slå den på og sette den opp; hvordan folk bruker den, står i
[Virtuelle ansatte](/nb/user/agents/), og alle reglene i
[referansen](/en/reference/agents/). En agent som er satt opp svarer på samtalene på kanalene
sine; køskjermene for gjennomgang, med kalibreringen og skjermene Bruk og Aktivitet, kommer i
neste utgivelse.

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
3. Sett **Oppbevaring**: hvor mange måneder en kjøring beholdes, fra 1 til 60. Standard er 6. Den er aldri
   lenger enn Kommunikasjon beholder meldingene sine (`COMMUNICATIONS_RETENTION_DAYS`, 365 dager som
   standard, altså 12 måneder): en lengre verdi senkes til det når du lagrer, og igjen hver natt hvis
   installasjonen senere forkorter Kommunikasjons egen. Feltet viser det lengste du kan skrive inn.
4. Lagre.

Hva oppbevaringen fjerner står under [Oppbevaring og sletting](#oppbevaring-og-sletting).

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
- Alle andre tillatelser som bare en eier kan gi videre: administrasjon av Entra og Google, administrasjon av Kunnskap, og fraktselskaper og innstillinger i Ordre.
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

## Hvordan en agent kjører

Kjøringer skjer i **API-prosessen**, ikke i arbeiderne, fordi de kaller applikasjonens egne
operasjoner som agenten. En ny melding på en knyttet kanal gir en forespørsel, og en replika tar den
etter 20 sekunders ventetid (høyst to minutter etter den første meldingen i en rekke). Se etter
følgende.

- **`AGENTS_CONCURRENCY`** (standard `4`, fra `1` til `32`) er hvor mange kjøringer én replika
  bærer samtidig ([Konfigurasjon](/nb/admin/configuration/)). En kjøring er én agent på én samtale, i
  høyst fem minutter, og holder ett modellkall om gangen. Hev den når samtaler venter; senk den når
  modellserveren din ikke tåler så mange kall samtidig.
- **En replika som stopper** midt i en kjøring lar den stå som tatt; innen få minutter overtas kjøringen
  én gang, og andre gang leveres samtalen til overtakeren med et notat.
- **Bare én kjøring** arbeider på en samtale om gangen. En melding som kommer under en kjøring gir en ny
  forespørsel, som kjører etter den og ser begge meldingene.
- **Uten KI** (ingen leverandør, eller en modell som ikke kan kalle verktøy) feiler en kjøring som
  `ai_unavailable`, og samtalen leveres videre med et notat; forespørsler som har ventet mer enn et
  døgn leveres også videre.

### Det daglige tokenbudsjettet

En agent kan ha et **daglig tokenbudsjett**, satt på agenten (`dailyTokenBudget`; 0 fjerner det, og en
verdi under 1 000 avvises). Det teller tokenene agentens modell og innholdskontrollen bruker i
forretningsdøgnet. Ved budsjettet starter ingen ny kjøring, hver forespørsel som venter leveres videre med
notatet *Daglig budsjett er nådd.*, og neste dag starter agenten igjen, bare på nye meldinger. Budsjettet er
et felt på agentens Persona-fane (tomt for ingen); fanen **Bruk** viser de siste 30 dagene mot det.

### Hvilke kanaler

Kanalene agenten svarer på er de du knytter til rollen. En agent svarer aldri på en melding fra en annen
kanal i installasjonen din, så to agenter på to kanaler kan ikke svare hverandre; modulen lærer en kanals
adresse første gang en kjøring leser en samtale på den. Svar på Messenger følger 24-timersvinduet: når det
er lukket, skriver en person svaret.

### Svarmodusene, kort

La hver rolle begynne i **Gjennomgang**. Flytt en rolle til **Auto over X %** eller **Auto** først når
svarene har vært riktige en stund. Reglene som beskytter en kundes data og åpningslinjen som sier at
agenten er en KI gjelder i **alle** moduser, også Auto
([referansen](/en/reference/agents/#the-pipeline)).

## Hvem som gjennomgår

Et svar eller en endring en agent legger igjen til en person venter i **Gjennomgangskøen** (tillatelsen
*Gjennomgå virtuelle ansattes arbeid*, `agents:review`) i 24 timer; et Messenger-svar ikke lenger enn til 15
minutter før vinduet stenger. Velg rollenes **overtaker** med omhu: når en sak legges igjen til gjennomgang,
får den personen en e-post (gjennom installasjonens e-postdriver; ingenting sendes der ingen er satt opp) og
et notat dukker opp i samtalen, og en rolle uten overtaker varsler ingen, så noen må se på køen, hvis antall
står på oversiktens flis *Virtuelle ansatte*.

Gi `agents:review` til folk du stoler på med agentens arbeid. En godkjenning gjøres **som agenten, på
gjennomgåerens fullmakt**: gjennomgåeren må selv ha lov til å gjøre det saken gjør (for å sende et svar
`communications:conversations-reply`; for å godkjenne en endring tillatelsen til operasjonen, med
Eier-nivåets policyer sjekket også), så en gjennomgåer kan ikke gi agentens tilgang til en person som mangler
den. Å lese en sak eller en kjøring krever også `communications:conversations-view` og lesetillatelsene
Skranken trenger, for de viser det agenten leste: kunder, kontakter, ordrer, fakturaer, produkter og Kunnskap.
Gi dem til gjennomgåerne; holder du en tilbake, skjules køen for den gjennomgåeren, og ingenting annet.

Å lese en agents kjøringer og utskrifter er en egen, sensitiv tillatelse (`agents:activity`), fordi en
utskrift inneholder kundenes meldinger.

## Minne og den ukentlige kontrollen

To ting beholdes utover en kjøring, og begge går til modellen.

- **Notater.** En agent skriver korte notater om en kunde (ingen når samtalen er uten kunde) og leser de 20
  nyeste når neste kjøring begynner. De når modellen som data sammen med samtalen. Et notat er det en modell
  skrev etter å ha lest en kundes melding, så det kan være feil eller plantet; det vises for folk på kundens
  side, der den som har `agents:manage` kan slette det, og det er aldri en instruks eller en kilde for et
  svar. En agent beholder høyst 50 per kunde; er det fullt avviser den nye notater, og en person sletter et,
  kundesiden sier fra.
- **Den ukentlige kontrollen.** Én gang i uken ber en API-prosess modellen til KI-oppgaven
  **Agentens innholdskontroll** (`agents.check`) om å lese det folk rettet for hver rolle: årsakene til at de
  forkastet svar, og teksten før og etter redigeringer over 20 %. Den får ingen samtale og ingen kunde-id, men **teksten i svarene og notatene gjennomgåere skrev da de forkastet et svar sendes til modellen**, og de kan nevne en kunde, så personvernerklæringen ovenfor gjelder også her. Den foreslår høyst
  tre endringer i rollens instruksjoner, som en Eier eller den som har `agents:manage` bruker eller avviser på
  rollesiden. Uten KI-leverandør hoppes kontrollen over. Modellen kan byttes på siden for KI-modeller.

## Oppbevaring og sletting

- **Kjøringer.** En kjøring, med utskrift, handlingslogg, saker til gjennomgang og overleveringsmarkørene til
  samtalen, slettes etter oppbevaringsmånedene, og beholdes aldri lenger enn Kommunikasjon beholder
  meldingene sine, fordi en utskrift kopierer meldingstekster. Arbeideren `agents-retention` gjør det hver
  time, i porsjoner. Dagens **bruk** (bare tall) beholdes i 24 måneder.
- **Notater og forslag.** Notatene en agent husker og forslagene fra den ukentlige kontrollen utløper
  sammen med kjøringene, etter samme oppbevaring. Å slette en agent sletter notatene dens.
- **Saker som venter** slettes aldri av oppbevaringen; de utløper først (24 timer), og en utløpt sak leverer
  samtalen videre. Arbeideren `agents-reviews` gjør det hvert minutt.
- **Sletting av en kunde.** Når en privatkunde anonymiseres i Kunder, sletter modulen kundens notater, forslagene som bygger på svarene deres, og kjøringene,
  utskriftene og sakene til gjennomgang, etter kunden og etter **hver samtale av dem den har sett knyttet til
  dem**, så en samtale som ble knyttet etter at kjøringen begynte, er dekket; og den visker kundens id i
  handlingsloggen. En slettet **kontakt** tar med seg samtalene de deltok i, og notatene skrevet i dem. Eksporten av en kundes
  personopplysninger lister kjøringene og svarene som er lagret for dem. En samtale som ble knyttet til kunden
  først etter at modulen sist så på den, er ikke kjent for modulen: den fjernes av oppbevaringen i stedet.
- **Sletting av en person** (en gjennomgåer eller en Eier) beholder id-en til avgjørelsene som en
  ugjennomsiktig id, så historikken fortsatt sier at en person avgjorde; ingenting om personen beholdes med den.

## Sett en agent på pause og slett den

- **Pause** stopper nye kjøringer; roller, kanaler og nivåer blir stående. **Gjenoppta**
  starter dem igjen.
- **Slett** trekker tilbake det som venter på gjennomgang og leverer de samtalene videre, stopper rollene,
  frigjør kanalene for en annen rolle, avslutter agentens tilganger og deaktiverer brukeren. Brukeren beholdes så tidligere historikk fortsatt stemmer. Det kan
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
| En agent svarer ikke på en samtale | Samtalen er tildelt en person, agenten er på pause, samtalen ble levert videre, meldingen var automatisk, eller tillatelsene rollen trenger er fortsatt Av. | Tildel den til agenten, eller gi den tilbake ([Virtuelle ansatte](/nb/user/agents/#overlevering)); gjenoppta agenten; hev Trenger. |
| Hver kjøring ender i en overlevering med *KI-modellen svarte ikke* | Leverandøren er nede, eller agentens modell kan ikke kalle verktøy. | Sjekk `AI_PROVIDER` og velg en annen modell på fanen Persona. |
| *Daglig budsjett er nådd.* | Agenten brukte det daglige tokenbudsjettet sitt. | Hev budsjettet, eller vent til neste forretningsdøgn. |
| Køen sier at du ikke kan se en sak | Å lese en sak krever `agents:review` og også `communications:conversations-view` og lesetillatelsene Skranken trenger. | Gi gjennomgåeren den manglende tillatelsen. |
| `reviewer_not_permitted` ved Send eller Godkjenn | Saksbehandleren har ikke lov til å gjøre det saken gjør (for et svar `communications:conversations-reply`). | Gi tillatelsen, eller la en kollega avgjøre. |
| En sak er *trukket tilbake* og samtalen overlevert | En kollega tok samtalen, en nyere melding kom, Messenger-vinduet lukket seg, eller agentens nivå ble senket siden. | Ingenting er galt; en person svarer, eller en ny kjøring svarer den nyere meldingen. |
| En godkjenning sier at en regel holder den igjen | Teksten sier nå at skribenten er et menneske, eller kundens data ville gått til noen som ikke er kundens kontakt. | Rediger teksten, eller forkast den. |
| En lavere oppbevaring enn du lagret | Kommunikasjon beholder meldingene sine kortere. | Ingenting: oppbevaringen følger den. |
