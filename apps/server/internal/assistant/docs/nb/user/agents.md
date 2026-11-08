---
title: Virtuelle ansatte
description: Appen Virtuelle ansatte — hva en agent er, agenten i arbeid (når den svarer, hva den leser, hva den kan sende selv, åpningslinjen, overleveringen og å gi en samtale tilbake), gjennomgangskøen (hvorfor en sak venter, å godkjenne, redigere og sende, forkaste, hvor lenge den venter), kalibrering, hva en agent husker, bruk og aktivitet, å opprette en agent, Persona (navn, stil, språk, modell, bilde) og åpningslinjen med de tre formuleringene, å sette på pause, gjenoppta og slette, Roller (malen Skranke, instruksjoner, kanaler, den som overtar, Svarmodus og grenser, og de ukentlige forslagene til instruksjonene), Tillatelser (alt er Av til Eieren hever det) og hvor KI-merket vises.
sidebar:
  order: 75
sources:
  - apps/agents/frontend/src
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
  - apps/server/internal/db/migrations/00147_agents_review.sql
  - apps/server/internal/db/migrations/00148_agents_memory.sql
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

- I **Gjennomgang** venter hvert svar på en person i [gjennomgangskøen](#gjennomgangskøen). Et internt
  notat i samtalen sier at et svar venter, med en lenke til det, og rollens overtaker får en e-post.
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
ikke tillater. Merkelappene som betyr klage settes på rollen ([Roller](#roller)); det daglige
tokenbudsjettet på agentens [Persona](#persona).

### Overlevering

En agent som ikke vet, ikke kan avgjøre, blir bedt om en person, når grensene sine eller ikke når
KI-modellen **leverer samtalen videre**: den skriver et internt notat med det kunden vil, og tildeler
samtalen til rollens overtaker, eller til ingen (Innboksen). Hvis den skrev et svar den ikke kunne
sende, står det i notatet. Etter det **lar agenten samtalen være**, også når kunden skriver igjen, til en
person **svarer** i den eller **tildeler** den, eller den **gis tilbake til agenten**. I
[Kommunikasjon](/nb/user/communications/#samtaler-agenten-har-overlevert) sier et felt på samtalen at agenten
overleverte den, og hvorfor, og **Gi tilbake til den ansatte** gir den tilbake.

Tar en kollega samtalen mens agenten skriver, sendes ikke agentens svar: samtalen er kollegaens.

## Hvem som kan bruke den

Flisen **Virtuelle ansatte** i appvelgeren vises for alle med en rolle som har **Se
virtuelle ansatte** (`agents:view`). **Administrere virtuelle ansatte** (`agents:manage`)
lar deg opprette, endre, sette på pause og slette agenter. Det er en sensitiv tillatelse
som bare en Eier kan dele ut. Noe er fortsatt Eierens selv med `agents:manage`: å heve en
tillatelse, å velge **Auto** eller heve den, og å knytte en kanal. Å lese en agents
kjøringer krever **Lese virtuelle ansattes aktivitet** (`agents:activity`), og å godkjenne
det en agent foreslår krever **Gjennomgå virtuelle ansattes arbeid** (`agents:review`). En som
gjennomgår, men ikke kan se agentene, ser likevel **Gjennomgangskø** i sidemenyen. Å lese en kjøring eller
en sak krever mer enn sin egen tillatelse, for den viser det agenten leste
([Gjennomgangskøen](#gjennomgangskøen)).

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
| **Daglig tokenbudsjett** | Flest tokens (lest og skrevet) agenten kan bruke på en virkedag, minst 1 000; tomt for ingen. Ved budsjettet starter ingen ny kjøring, og det som venter gis til en kollega med notatet *Dagsbudsjettet er nådd*. Neste dag starter den på nytt, bare på nye meldinger. |

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
| **Overtas av** | Hvem en samtale gis til når agenten overleverer den. Tom betyr at den går til Innboksen, uten ansvarlig. Samme person får e-post når en sak legges igjen til gjennomgang. |
| **Klagemerkelapper** | Merkelappene i Kommunikasjon som betyr en klage (høyst 20). En samtale med en av dem holdes igjen til gjennomgang i Gjennomgang og Auto over X %. |
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

## Gjennomgangskøen

Åpne **Virtuelle ansatte ▸ Gjennomgangskø**. Den lister, på tvers av alle agenter, alle svar og endringer en
agent har lagt igjen til en person: de som utløper først øverst, og (velg **Avgjort**) de som allerede er
avgjort, trukket tilbake eller utløpt. Et tall på flisen **Virtuelle ansatte** på oversikten teller det som
venter. Det interne notatet i en samtale (*Hanna har et svar som venter på gjennomgang*) lenker til saken.

Hver sak viser:

- **Hvorfor den venter.** Reglene som holdt den igjen, i ord: en manglende kilde, en første kontakt, en
  klage, et løfte instruksjonene ikke tillater, en kundes data på vei til noen som ikke er kundens kontakt,
  en tekst som sier at skribenten er et menneske, og så videre. En sak ingen regel holdt igjen venter fordi
  rollen er i Gjennomgang, eller fordi poengsummen er under terskelen.
- **Poengsummen**, og agentens egen når kildene den faktisk hadde lest senket den, og **hva den bygger på**:
  kildene, for eksempel `getInvoicesById 2026-0931`.
- **Hvor lenge den har.** En sak utløper etter 24 timer. Et Messenger-svar utløper 15 minutter før
  Messenger-vinduet stenger, og saken teller ned til det.
- **Svaret**, med åpningslinjen låst over, eller **endringen**: hva agenten ber om å få gjøre, i én setning,
  og verdiene.

Du kan **Sende** et svar (eller **Godkjenne** en endring), **Redigere og sende** et svar, eller **Forkaste**
en sak.

- **Send** sender svaret som agenten, med åpningslinjen, og meldingen lagres og merkes som agentens.
  **Godkjenn** gjør endringen som agenten. Begge gjøres på din fullmakt, så **du må selv ha lov til å gjøre
  det saken gjør**: for å sende et svar trenger du `communications:conversations-reply`, for å godkjenne en
  endring tillatelsen til operasjonen den kaller (for å lukke en samtale
  `communications:conversations-manage`). Ellers svarer køen at du ikke har lov.
- **Rediger og send** åpner teksten så du kan endre den. Åpningslinjen står låst over; skriver du en selv,
  legger systemet likevel til sin egen, så en melding går aldri ut uten den. En tekst som sier eller
  antyder at skribenten er et menneske holdes igjen, også etter at du har redigert den, og saken blir i køen
  med regelen nevnt.
- **Forkast** ber om en årsak: *Feil fakta*, *Feil tone*, *Skulle ikke svart på dette* eller *Annet*, med en
  valgfri tekst. Å forkaste et svar **gir samtalen til en kollega**: agenten lar den være, og overtakeren
  (eller Innboksen) tar den.

Før noe sendes ser køen etter igjen. Har en kollega tatt samtalen, har en nyere melding kommet, er
Messenger-vinduet lukket (eller bare «menneskelig agent»-utvidelsen igjen), er agentens nivåer senket i
mellomtiden, eller er rollen eller agenten borte, **sendes ingenting**: saken *trekkes tilbake* og samtalen
leveres videre, eller, når en nyere melding kom, svarer en ny kjøring samtalen slik den nå er. En endring
sjekkes på samme måte: en der agenten ikke lenger kan kalle operasjonen, trekkes tilbake og gjøres ikke.

To som godkjenner samme svar samtidig, sender det én gang; den andre får beskjed om at det er avgjort. Et
valg navngir versjonen det leste: en sak som endret seg under deg lastes på nytt og sier fra. En sak som
venter mer enn et døgn *utløper*, det samme gjør en der Messenger-vinduet snart stenger; en utløpt sak
leverer samtalen videre, som en tilbaketrukket.

**Hva du får se.** Køen viser det agenten leste, så å se en sak krever mer enn *Gjennomgå virtuelle ansattes
arbeid*: også `communications:conversations-view` og lesetillatelsene Skranken trenger (kunder, kontakter,
ordrer, fakturaer og produkter, og Kunnskap). Uten dem svarer køen at du ikke har lov. Å lese en
[kjøring](#aktivitet) krever det samme i tillegg til `agents:activity`.

### Foreslåtte endringer i instruksjonene

Én gang i uken, mandag morgen i firmaets tidssone, leses det folk har rettet de siste ukene: svarene de
**forkastet**, med årsaken de oppga, og svarene de **redigerte mer enn 20 %**, med teksten før og etter. Når
minst tre rettelser har kommet inn, foreslår systemet **høyst tre** endringer i rollens instruksjoner.
Rollesiden viser da **Foreslåtte endringer i instruksjonene** over skjemaet, hver med begrunnelsen, passasjen
den ville erstatte eller avsnittet den ville legge til, og rettelsene den bygger på (tallene åpner dem).

- **Bruk i instruksjonene** gjør endringen med en gang. Det er en vanlig endring av rollen: den tar rollens
  versjon, så hvis noen lagret rollen i mellomtiden blir du bedt om å lese den på nytt, den føres i
  endringshistorikken, og den krever `agents:manage` som enhver endring av instruksjonene. Et forslag
  hvis passasje ikke lenger står i instruksjonene kan ikke brukes; avvis det.
- **Avvis** lar instruksjonene stå som de er.

Ingenting endrer noen gang instruksjonene av seg selv. Kontrollmodellen får se årsakene og tekstene i
rettelsene og de gjeldende instruksjonene, aldri samtalen, og får beskjed om å skrive regler, ikke eksempler.
Les et forslag som du ville lest en kollegas: det kan være feil.

## Kalibrering

Rollesiden viser, under **Hvordan den svarer**, hva rollens poengsummer er verdt. Hvert svar en person har
avgjort er en merkelapp: sendt uendret er bra, redigert og sendt er bra med endringer, forkastet er dårlig.
Over rollens siste 200 merkede svar, flytt glidebryteren til en terskel X fra 50 til 100, og panelet sier:

- hvor mange svar, og hvor stor andel, som **ville gått ut av seg selv** (poengsum minst X og ingen regel som
  holdt dem);
- av dem, hvor mange en person **redigerte mer enn 20 % eller forkastet**;
- hvor mange en regel holdt igjen **uansett poengsum**.

Et svar agenten sendte **av seg selv** (Auto) er også en merkelapp: når en person svarer i samme samtale
innen 24 timer, regnes agentens svar som **dårlig**, siden en kollega mente de måtte svare over det; et som
står ubesvart i 24 timer regnes som bra, som er svakt belegg (kunden trengte kanskje bare ikke mer), så les
*bra*-siden av tallene med det i tankene. Du trenger ikke gjøre noe for dette.

To linjer og en markør ved X tegner det samme. Under 50 merkede svar sier panelet *Ikke nok historikk ennå* og
hvor mange det er. Tallene kommer fra rollens egen historikk, så en rolle som har stått en stund i Gjennomgang
er den hvis poengsummer kan stoles på når du velger *Auto over X %*.

## Bruk

Agentens fane **Bruk** viser de siste 30 dagene: kjøringer, tokens (lest og skrevet), og hvor mange svar som
ble sendt, holdt igjen for en person og forkastet, dag for dag, med summer øverst og dagens tokens mot det
daglige budsjettet når det finnes. Dagen er virkedagen. Kortene på siden **Virtuelle ansatte** viser dagens
tall. Tallene beholdes i 24 måneder og inneholder ikke noe innhold.

## Hva en agent husker

En agent kan ta vare på **korte notater** om en kunde til senere: et varig faktum den lærte i kundens samtale,
som *foretrekker e-post, aldri telefon*, eller en avtale. Et notat er på høyst 500 tegn, og en agent skriver
høyst tre i en kjøring.

- **Hvem notatene gjelder.** Et notat gjelder kjøringens kunde. Er samtalen ikke knyttet til noen kunde, tar
  agenten vare på ingenting: den får beskjed om det, og skriver heller et internt notat til kollegene. En agent
  leser en kundes notater bare i en samtale med den kunden, aldri i en annens, og leser høyst de 20 nyeste
  når en kjøring begynner.
- **Fullt minne.** En agent beholder høyst 50 notater om én kunde. Når det er fullt tar den vare på ingen
  flere, og ingenting slettes bak ryggen din: kundesiden sier at minnet er fullt, og en person sletter et
  utdatert notat for å gjøre plass.
- **Leses som fakta, ikke ordre.** Notatene kommer til agenten som data, som kundens meldinger: agenten
  veier dem, men en instruks i et notat endrer ingenting, og et svar kan ikke vise til et som kilde.
- **Hvor du ser dem.** På kundens side er **Hva Hanna husker** en sammenfoldet del under vedleggene, vist for
  dem som har `customers:view` og `agents:view`. Åpne den for å lese notatene med datoer. Den som har
  `agents:manage` har en **Slett** ved hvert notat, etter en bekreftelse; slett et notat som er feil eller
  ikke bør beholdes. Agentens fane **Bruk** teller dem.
- **Hvor lenge.** Et notat utløper sammen med kjøringene, etter oppbevaringstiden administratoren satte, og
  slettes sammen med kunden eller kontakten det handler om
  ([Virtuelle ansatte](/nb/admin/agents/#oppbevaring-og-sletting)). Å slette en agent sletter notatene dens.

## Aktivitet

Agentens fane **Aktivitet** lister kjøringene, de nyeste først, med tilstanden (ferdig, venter på
gjennomgang, overlevert, feilet …), hva de endte i og tokenene de brukte, og endringene Eieren har gjort i
innstillingene, med hvem som gjorde dem: en agent, en rolle, en modus, et nivå, en modell eller
innstillingene. En kjøring åpnes på sin egen side:

- **Tidslinje.** Hvert verktøykall, i rekkefølge, med status og hvor lang tid det tok. Ingen forespørsler
  eller svar i sin helhet beholdes.
- **Vurderingen og modusens avgjørelse.** Poengsummen (og agentens egen), kildene, rollens modus og terskel,
  hvilke regler som holdt eller stoppet svaret, og om det ble sendt eller lagt igjen til en person.
- **Hva revisjonssporet sier.** Hvert kall som *av Hanna (KI)*, og *godkjent av en gjennomgåer* når en
  person godkjente det.
- **Hva kjøringen la igjen til gjennomgang**, med køens knapper, og **utskriften**: hva modellen ble fortalt
  og svarte, med kundens meldinger, skjult til du åpner den.

En kjøring krever `agents:activity`, `communications:conversations-view` og lesetillatelsene over.
Kjøringer og utskrifter slettes etter oppbevaringstiden administratoren satte, og slettes sammen med kunden
de hører til ([Virtuelle ansatte](/nb/admin/agents/#oppbevaring-og-sletting)).

## Hvor du ser KI-merket

En agents avatar har et lite **KI**-merke overalt den vises, så ingen tar den for en
person: i valget av ansvarlig for en samtale i [Kommunikasjon](/nb/user/communications/), i
samtalelisten og blant brukerne i
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#agenter-blant-brukerne).
