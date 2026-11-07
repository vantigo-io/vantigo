---
title: Kommunikasjon
description: Delte postkasser, samtaler med kunder, mottak og sending av e-post og blokkeringer — kanaler på SMTP eller på en delt postboks i Microsoft 365, e-post mottatt i en Microsoft 365-kanal, svar og svar alle, sending fra din egen Outlook- eller Gmail-adresse, å bytte en kanal til Microsoft 365, og størrelsen en Microsoft 365-kanal sender — og chat-samtaler på en Facebook Messenger-kanal, med svaralternativer og vinduet et svar kan sendes i.
sidebar:
  order: 80
sources:
  - apps/communications/frontend
  - apps/host/frontend/src/routes/communications
  - apps/host/frontend/src/lib/product-messaging.ts
---

Kommunikasjon-appen er en delt innboks: alle e-postsamtaler — og, der en Facebook
Messenger-kanal er satt opp, alle chatter — organisasjonen din har med kundene sine, på
ett sted, besvart fra delte postkasser som kalles **kanaler**.
Sidemenyen har tre områder: **Innboks**, der samtaler leses og besvares; **Kanaler**,
der postkassene settes opp; og **Blokkeringer**, listen over adresser Vantigo nekter å
skrive til. Åpner du appen, havner du i Innboks, og dashbordet Hjem viser et
Kommunikasjon-kort med åpne, nye og lukkede samtaler.

E-post som kommer til den delte postboksen til en **Microsoft 365**-kanal, kommer inn i
Innboks av seg selv, som en samtale du kan lese og svare på. En **SMTP**-kanal sender
bare: ingen e-post kommer inn gjennom den, så samtalene dens inneholder bare det som er
sendt fra Vantigo og notatene som er skrevet i dem, og de kan ikke besvares. Hvordan
mottaket virker, er beskrevet under
[Receiving](/en/reference/communications/#receiving) i referansen.

En **Facebook Messenger**-kanal er en Facebook-side virksomheten din eier, og samtalene
på den er chatter: de ser annerledes ut og besvares annerledes enn e-post, som beskrevet
under [Messenger-samtaler](#messenger-samtaler).

## Finn en samtale

Åpne **Innboks**. Den venstre ruten lister samtalene, nyeste aktivitet først, med et
antall øverst. Hver rad viser motparten (navnet, eller adressen når navnet er ukjent) ved siden av et
lite ikon for kanalen — en konvolutt for e-post, Messenger-logoen for en chat —, når noe
sist skjedde, emnet (en chat har ikke noe, og viser **Facebook Messenger** i stedet),
en forhåndsvisning av den siste teksten, samtalens tagger og statusen dens. En ulest samtale vises i fet skrift.

Verktøylinjen filtrerer listen:

- **Alle / Åpne / Lukkede / Arkiverte** velger en status; Alle er standard.
- **Uleste** beholder bare samtaler med noe du ikke har lest.
- Taggvelgeren (plassholder **Alle tagger**) beholder samtaler med én bestemt tagg.

Endrer du et filter, forsvinner den valgte samtalen, siden den kanskje ikke lenger er i
listen, og filtrene ligger i adressefeltet slik at en filtrert visning kan bokmerkes
eller deles med en kollega. Når ingenting samsvarer, ser du «Ingen samtaler samsvarer
med disse filtrene.» Feltet **Søk i samtaler** i verktøylinjen er ikke i bruk ennå.
Hakeikonet ved siden av antallet laster listen på nytt.

Når du åpner Innboks, velges den første samtalen i listen; klikk på en rad for å lese
en annen. Innboks krever `communications:conversations-view`.

### Innboks oppdaterer seg selv

Du trenger ikke laste siden på nytt. Når det kommer inn e-post eller en chattmelding, eller
en kollega svarer, lukker, merker eller tildeler en samtale, endrer listen og samtalen du har
åpen seg i løpet av et øyeblikk. Har ikke administratoren gjort sanntidsoppdateringer
tilgjengelige, eller mistes forbindelsen en stund, oppdateres listen i stedet av seg selv
hvert minutt, og hakeikonet ved siden av antallet laster den fortsatt på nytt med en gang.

## Les en samtale

En samtale samler alt som er utvekslet med én kunde om ett emne: deltakerne og emnet i
toppen, deretter meldingene i rekkefølge. Hver melding er merket **You** for en e-post
sendt fra Vantigo, **Internal note** for et notat en kollega har skrevet, og
avsenderens navn for en melding fra kunden (disse merkelappene vises på engelsk).
Klokkeslettet på hver melding, og på hver samtale i listen, er i din egen tidssone
([Velg tidssonen din](/nb/user/getting-started/#velg-tidssonen-din)).

**E-post fra kunder.** En e-post sendt til den delte postboksen til en Microsoft
365-kanal vises i Innboks innen omtrent ett minutt når endringsvarsler er på, eller innen
omtrent fem minutter ellers. Et svar på en tidligere melding
havner i samtalen til den meldingen; alt annet starter en ny samtale. En lukket samtale
kunden skriver til igjen, åpnes igjen; en arkivert samtale blir stående som den er, og
e-posten starter en ny samtale. Så lenge samtalen er åpen, blir meldingen liggende i den
delte postboksen i Outlook som den var; når du lukker samtalen, lar Vantigo den stå
uendret, markerer den som lest eller flytter den til en mappe i Outlook etter kanalens
innstilling **Når en samtale lukkes**.

- En melding skrevet som HTML vises formatert, i en sandkasse som blokkerer skript,
  skjemaer og nettverkstilgang. Bilder fra internett fjernes; bilder meldingen selv har
  med, vises. **Vis som tekst** viser meldingens rene tekst i stedet, og **Vis formatert**
  går tilbake.
- Et automatisk svar — et fraværssvar, for eksempel — er merket **Autosvar**, og åpner
  ikke en lukket samtale igjen.
- En melding Vantigo ikke kunne lese, sier *Vantigo kunne ikke lese denne meldingen.*;
  åpne den i e-postprogrammet ditt med **Last ned originalen**. **Meldingshandlinger** på
  enhver melding fra en kunde tilbyr også **Last ned originalen**, e-posten nøyaktig slik
  den kom, som en `.eml`-fil.

Et vedlegg vises som en lenke du kan laste ned. Et som kom med en e-post fra en kunde, er
merket *Ikke skannet. Åpne det bare hvis du stoler på avsenderen.* — Vantigo sjekker ikke
filer for virus — og lastes alltid ned som en fil, åpnes aldri i nettleseren. Et vedlegg
som venter på å bli sjekket, vises som **Skanner**, og et som ikke kan leveres, som
**Vedlegg utilgjengelig**.

Fra toppen kan du:

- endre **Status** (`open`, `closed` eller `archived`);
- åpne **Handlinger** → **Marker som lest**, som fjerner ulest-markeringen, eller
  **Arkiver**;
- legge til en tagg fra velgeren **Legg til tagg**, eller fjerne en med × på den. Bare
  tagger som allerede finnes, tilbys; Innboks oppretter ikke nye.

Å lese krever `communications:conversations-view`; å endre status og tagger krever i
tillegg `communications:conversations-manage`.

### Se hvem andre som er i en samtale

Under samtalens topptekst ser du kollegene som har den samme samtalen åpen, som små runde
avatarer. Ved siden av står:

- **Også her: Kari** lister dem som bare ser på.
- **Kari svarer …** vises uthevet når en kollega har klikket inn i svarfeltet og skrevet
  noe. Er det flere, står det **Kari, Ola svarer …**.

Bruk det for å unngå å svare den samme kunden to ganger: ser du at noen svarer, avtal med
dem før du sender. Det hindrer deg aldri i å sende, og sier ingenting om folk som har
samtalen lukket. Du vises ikke for deg selv, og en kollega som har samtalen åpen i to faner
vises én gang.

## Knytt en samtale til en kunde

Når en e-post fra en kunde kommer inn, slår Vantigo opp avsenderens adresse blant
kundenes kontakter. Hører den til **én kunde**, knyttes samtalen til den kunden av seg
selv; hører den til **flere**, listes de som kandidater du kan velge mellom. En samtale som
allerede er knyttet — for hånd eller av en tidligere e-post — beholder kunden sin.

Toppen av samtalen forteller hvordan den henger sammen med en kunde:

- **Kunde nr. id** — samtalen er knyttet, og merket sier hvordan koblingen ble laget.
- **Foreslått kunde nr. id**, med en prosent for sikkerhet — KI-assistenten har et
  forslag, vist med begrunnelsen sin. **Bekreft kunde** gjør det til koblingen.
- **N kundekandidater** — flere kunder kan passe. **Foreslå kunde** ber
  KI-assistenten velge én, som så vises som et forslag du kan bekrefte.
- **Ingen kundetilknytning** — ingen av delene.

Å foreslå og bekrefte krever `communications:conversations-manage`. Når assistenten
ikke er satt opp, ser du «KI-kundeforslag er ikke tilgjengelige nå.» —
administratorens side av det står under
[AI draft and customer suggestion](/en/reference/communications/#ai-draft-and-customer-suggestion).

## Start en samtale

Velg **Ny samtale** i verktøylinjen for å starte en ny kundesamtale: skriv inn mottakerens
e-postadresse i **Til**, **Emne**, og skriv meldingen din.

Dersom du har koblet til din egen postboks under **Innstillinger → Tilkoblinger** — Outlook
med tillatelsen E-post innvilget, eller Google med **Gmail** blant funksjonene —, kan du huke
av for **Send fra min Outlook-adresse (`du@dittfirma.no`)** eller **Send fra min
Gmail-adresse (`du@dittfirma.no`)**: avkrysningsboksen navngir tjenesten din egen adresse
sendes gjennom. E-posten sendes gjennom din egen postboks, slik at den vises i dine egne
**sendte elementer** i Microsoft 365 eller under **Sendt** i Gmail. Kundens svar sendes
likevel til kanalens delte postkasse (`Reply-To`), slik at samtalen holdes samlet i den delte
innboksen for teamet ditt. Dette virker på alle kanaler, uansett hvilken tjeneste kanalen
selv bruker.

## Svar en kunde

Skrivefeltet nederst i samtalen har overskriften **Svar**. Det forteller hvem svaret
går til («Svarer til …»): avsenderen av kundens siste e-post. Når den e-posten også ble
sendt til andre i kopi (Cc), tilbyr velgeren **Svarmodus** valgene **Svar**, bare til
avsenderen, og **Svar alle · N**, som tar dem med i kopi også — uten den delte postboksen
selv. Som standard sendes svaret fra kanalen samtalen hører til. Dersom du har koblet til
din egen Outlook med e-posttillatelse, eller din egen Google-konto med Gmail, kan du også huke
av for **Send fra min Outlook-adresse (`du@dittfirma.no`)** eller **Send fra min
Gmail-adresse (`du@dittfirma.no`)** for å sende fra din egen postboks i stedet, mens kundens
svar havner i den delte kanalen. Det sendes
som et svar på kundens e-post, så det havner i samme tråd i kundens e-postprogram.

### Personlig sending og ny prøving

Når **Send fra min …-adresse** er valgt:
- `From`-adressen er din e-postadresse og visningsnavn fra katalogen, og e-posten havner i
  dine egne sendte elementer i Microsoft 365, eller under **Sendt** i din egen Gmail.
- `Reply-To`-adressen er kanalens adresse og visningsnavn, slik at svar fra kunden kommer
  tilbake til den delte kanalen og trådes inn i samtalen.
- Meldingen sendes aldri fra kanalen i stedet: kan ikke din egen postboks sende den,
  mislykkes den, og du velger selv hva du gjør.
- Dersom tilkoblingen din ennå ikke tillater sending, eller må kobles til på nytt, tilbyr
  skrivefeltet en integrert knapp: **Koble til Outlook** eller **Koble til Outlook på nytt**,
  eller **Koble til Google** eller **Koble til Google på nytt**. En Google-tilkobling laget
  bare for kalenderen viser også **Koble til Google** her: den ber Google om samtykke til å
  sende som deg (Gmail), og kalenderen beholdes. Når Microsoft eller Google har svart, er du
  tilbake i den samme samtalen, med filtrene du hadde.
- Dersom en personlig sending mislykkes ved utsending fordi tilkoblingen din er utløpt, ble
  avvist, krever ny flerfaktorautentisering eller ikke lenger tillater sending, viser
  meldingskortet: *Sendingen mislyktes fordi e-posttilkoblingen din må kobles til på nytt.*
  med en knapp for **Koble til Outlook på nytt** eller **Koble til Google på nytt** og en knapp
  for **Prøv å sende på nytt**. Når du har koblet til på nytt, trykker du på **Prøv å sende på
  nytt** for å legge meldingen i kø igjen.
- En melding sendt fra din egen adresse kan være omtrent 3 MB i alt, vedlegg medregnet,
  uansett hvilken tjeneste som sender den.
- **Gmail og tråder.** Gmail kan gi en melding den sender sin egen Message-ID, og Vantigo
  kan ikke lese den tilbake fra postboksen din — Vantigo har bare lov til å sende. Kundens
  svar på et **svar** du sendte fra Gmail, trådes likevel, gjennom de tidligere meldingene
  det viser til. Et svar på en **ny samtale** du startet fra Gmail, trådes bare dersom Gmail
  beholdt Vantigos Message-ID; ellers kommer det som en ny samtale i kanalen.

1. Skriv meldingen i editoren; fet, kursiv og punktlister er tilgjengelige.
2. **Legg ved fil** klargjør en fil. Den vises som et merke med navnet sitt: grønt når
   den er klar, **Skanner** mens den sjekkes, **Utilgjengelig** når den ble avvist. En
   fil som ikke kunne klargjøres, lar utkastet ditt stå uendret. Størrelsesgrensen
   settes av administratoren (se
   [Attachments](/en/reference/communications/#attachments)). På en **Microsoft
   365**-kanal sier skrivefeltet også *Vedlegg på opptil omtrent 2,1 MB til sammen på
   denne kanalen*: Microsoft 365 tar bare imot en melding opp til en fast størrelse,
   vedlegg og tekst sammen. En større fil avvises med en gang med *Vedlegget er større enn
   denne kanalen kan sende (omtrent 2,1 MB).*
3. **Send svar**. Knappen er deaktivert til det finnes tekst, og viser **Venter på
   vedleggsskanning** mens et vedlegg ennå ikke er klart. I en samtale uten e-post fra
   kunden forblir den deaktivert, med *Det finnes ingen melding fra kunden å svare på
   ennå.*

Å sende krever `communications:conversations-reply`. Svaret legges i kø i stedet for å
sendes på flekken — se [Etter at du har sendt](#etter-at-du-har-sendt) — og det avvises
når:

- samtalens kanal er deaktivert under Kanaler;
- samtalen ikke har noen kundemelding å svare på — en samtale startet fra Vantigo som
  kunden ikke har svart på ennå, eller en på en SMTP-kanal;
- et vedlegg ikke er klart;
- en mottaker står på blokkeringslisten;
- på en Microsoft 365-kanal er meldingen og vedleggene til sammen større enn kanalen
  sender: *Meldingen og vedleggene er større enn denne kanalen kan sende.* Fjern et
  vedlegg, eller send det på en annen måte.

## Skriv et utkast med KI

Over skrivefeltet skriver **Skriv utkast med KI** et første utkast for deg. Velg en tone
(`concise`, `friendly` eller `formal`), skriv hva svaret skal dekke i tekstfeltet — det
er obligatorisk — og klikk **Skriv utkast med KI**. Utkastet havner i editoren, med
overskriften «KI-utkast — se gjennom før sending. Ingenting er sendt.» Les det, rediger
det og send det som ethvert annet svar; **Lag utkast på nytt** ber om et nytt. Når
assistenten ikke er satt opp, sier panelet fra, og du skriver svaret selv. Hvilken
KI-modell som skriver utkastet, og hvilken som foreslår kunder, er en eiers valg under
**Administrer arbeidsområde** › **Integrasjoner** › **KI-modeller**
([KI-modeller](/nb/user/workspace-administration/#ki-modeller)); valget endrer ingenting
av det du gjør her.

## Messenger-samtaler

En samtale på en Messenger-kanal begynner når en person skriver til Facebook-siden din; du
kan ikke begynne en selv. **Start en samtale** bruker alltid en e-postkanal. Personens
meldinger kommer inn i Innboks av seg selv, noen sekunder etter at de er sendt, og
samtalens tittel er personens navn — Messenger gir verken emne eller e-postadresse. En
ny melding fra personen havner i den åpne samtalen deres; har du lukket samtalen, begynner
neste melding en ny.

Tråden er en chat. Hver melding er en boble, kundens til venstre og din til høyre. Bilder
kunden sender, vises i boblen, og andre filer listes under den, som nedlastinger. En
melding du har sendt, sier hvor den står — **Sender**, **Sendt**, **Levert**, **Lest** —
eller **Ikke sendt**, med årsaken når det finnes en: *kunden kan ikke nås* (de har
blokkert siden), *kunden må skrive først*, *Human Agent er ikke gitt til appen* eller
*Messenger avviste meldingen*. En melding sendt fra Meta Business Suite eller en annen app
på siden vises også, merket **Sendt fra Meta**, så tråden er hel. Knytt samtalen til en
kunde med de samme knappene som for en e-postsamtale; Vantigo kan ikke gjøre det selv,
siden Messenger ikke gir en e-postadresse.

### Svar en kunde i en chat

Skrivefeltet er et chat-felt: ren tekst, uten emne, uten Cc og uten formatering, og
**Legg ved fil**. Et svar kan være en fil alene, uten tekst; en fil som er større enn
Messenger tar (25 MB), avvises. En teller under feltet viser tegnene mot Messengers grense på 2000; over
den er **Send svar** deaktivert. Sending krever `communications:conversations-reply`.

**Svarvinduet.** Messenger lar deg bare svare en som har skrevet til deg de siste 24
timene. Skrivefeltet viser hvor vinduet står:

- *Du kan svare til …* mens det er åpent.
- **Mer enn 24 timer siden kunden skrev**, når meldingsleverandøren har fått Metas
  **Human Agent**-tillatelse: svaret sendes med Messengers Human Agent-merke, og du kan
  svare i opptil 7 dager.
- **Samtalen er stengt for svar**, med *Kunden må skrive på nytt før du kan svare her*,
  når vinduet er over. **Send svar** er deaktivert. Du kan fortsatt skrive et internt
  notat. Når kunden skriver igjen, åpnes vinduet på nytt. En melding sendt fra Meta
  Business Suite åpner det ikke.

### Legg til svaralternativer

En melding kan ha svaralternativer kunden trykker på i stedet for å skrive. Under
tekstfeltet har **Svaralternativer**:

- **Legg til Ja / Nei** legger til to alternativer, **Ja** og **Nei** på språket ditt. Gi
  dem andre navn om du vil.
- **Legg til alternativ** legger til ett tomt alternativ. En melding tar høyst 13.
- Hvert alternativ har et felt for tittelen, en teller mot grensen på 20 tegn og en **×**
  for å fjerne det. Alternativene sendes i den rekkefølgen de ble lagt til. Et alternativ
  uten tittel, eller med mer enn 20 tegn, hindrer at meldingen sendes.
- **Be om e-postadresse** legger til en knapp som lar kunden dele e-postadressen sin med
  ett trykk. Den er ett av Messengers 13 hurtigsvar, så med den på kan du legge til 12
  alternativer.

Når kunden trykker på et alternativ, kommer svaret som en melding vist som en brikke med
hake — **✓ Ja** — og spørsmålet markerer alternativet de valgte. En e-postadresse kunden
deler, kommer som en melding som sier **Delte en e-postadresse**; har en kontakt i Kunder
den adressen, tilbys kontaktens kunder som kandidater for samtalen, og du avgjør hvilken
som knyttes. Et alternativ det er trykket på som Vantigo ikke kan plassere — det hører til
en annen samtale — vises som vanlig tekst.

### Legg ved et produkt

**Legg ved produkt** i chat-skrivefeltet viser kunden et produkt som en rad med kort de kan
bla gjennom. Knappen er der når Produkter er på og du har lov til å se produkter
(`products:products-view`); uten dem finnes den ikke.

1. Velg **Legg ved produkt** og søk etter produktet på navn. Hvert produkt i listen sier
   hvor mange av bildene som kan vises. Et produkt som ikke har noe bilde merket **Vis i
   Kommunikasjon** på produktsiden, kan ikke legges ved, og listen sier det: åpne produktet
   og merk et bilde først (se [Bilder](/nb/user/products/#bilder)).
2. Velg produktet. Bildene merket for Kommunikasjon blir kortene, i den rekkefølgen de har
   på produktet, høyst 10. Hvert kort har produktets navn som tittel, bildet og en
   **undertittel** — med `products:pricing-view` starter den som produktets pris ekskl. mva,
   slik produktsiden viser den, og uten den tillatelsen starter den tom. Rediger
   undertittelen eller tøm den; den er tekst på høyst 80 tegn, og Vantigo regner ikke ut noen
   pris selv.
3. Fjern et kort med **×** for å utelate det bildet; fjerner du det siste, fjernes produktet.
   **Bytt produkt** bytter produktet, siden en melding viser ett produkt.
4. Hvert kort har en **Kjøp-knapp**, med tittelen **Kjøp** på norsk og **Buy** ellers. Du kan
   gi den et annet navn og legge til en **Ekstra knapp** av ditt eget, for eksempel *Fortell
   meg mer*; en knapps tittel er 1 til 20 tegn.
5. Skriv meldingsteksten som vanlig og velg **Send svar**. Svaralternativer kan legges til
   samme melding.

Meldingen beholder bildene slik de var da den ble sendt: endres eller slettes produktets
bilder etterpå, endres ingenting i samtalen. Er et bilde fjernet eller avmerket siden du
valgte produktet, sier sendingen fra om det, og du legger ved produktet på nytt. Messenger
henter bildene fra installasjonens offentlige adresse, så for å sende kort må en være satt
opp ([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten)); uten den avvises
sendingen med *Instansen har ingen offentlig vert*.

Når kunden trykker **Kjøp** (eller **Buy**) på et kort, kommer svaret som **Vil kjøpe:
&lt;produkt&gt;** med kortets bilde. Er Ordre på og du har lov til å lage ordrer
(`orders:access` og `orders:sales-manage`), har svaret en lenke **Opprett ordre**. Den åpner
siden for en ny ordre med samtalens kunde, når en er knyttet, og med produktet: dets eneste
variant blir ordrens første linje, og har produktet flere varianter, velger du hvilken.
Ingenting opprettes før du velger **Opprett ordren**. En ekstra knapp det trykkes på, kommer
som en vanlig svarbrikke.

### Sett opp en Messenger-kanal

En kanal på Messenger trenger en meldingsleverandør som er tilgjengelig på installasjonen,
med en Facebook-side koblet til. Først da åpnes **Legg til kanal** med et valg først:
**E-post** eller **Facebook Messenger**; uten en er det bare e-postdialogen. Med
**Facebook Messenger** tilbys når Meta er satt opp. Dialogen spør etter **Facebook-side** —
valgt blant sidene som er koblet til under **Integrasjoner** › **Meta** (en eier kobler dem
til; se [Meta](/nb/admin/meta/)), der en side som ikke kan brukes nå, står deaktivert med
årsaken, og en side Meta har avvist tokenet til, vises som **Utilgjengelig** — og et valgfritt
**Visningsnavn**, som er sidens navn når det ikke fylles ut. **Opprett kanal** legger til
kanalen. Å legge den til krever `communications:channels-manage`, som for en e-postkanal.
En Messenger-kanal har ingen egen legitimasjon og ingen postboks: raden i **Kanaler** sier
**Mottar** eller hvorfor ikke, og en side eller leverandør som svikter, viser **Utilgjengelig**.
Messenger-kanaler har sin egen standard: den første Messenger-kanalen er standard
Messenger-kanal, og å gjøre en annen kanal til standard endrer aldri standarden for den
andre typen.

## Legg til et internt notat

**Legg til notat** bytter skrivefeltet til **Internt notat**. Et notat ligger i samtalen
for kollegene dine og sendes aldri til kunden. Skriv det og klikk **Legg til notat**;
knappen **Svar** bytter tilbake. Notater krever `communications:conversations-manage`.

## Etter at du har sendt

Et sendt svar legges i en utboks og leveres av en bakgrunnsjobb — gjennom kanalens
SMTP-server, eller, på en Microsoft 365-kanal, fra den delte postboksen gjennom
Microsoft 365 — så det kan ta et øyeblikk før det går ut. E-post sendt fra en Microsoft
365-kanal ligger også i **Sendte elementer** i den delte postboksen, der teamet ditt ser
den i Outlook. Et mislykket forsøk prøves igjen
med økende ventetid, inntil åtte forsøk i alt; etter det gis meldingen opp og telles på
en måling administratoren kan varsle på. På en Microsoft 365-kanal gis en melding
postboksen aldri kan sende — postboksen utenfor det Vantigo har tilgang til, borte,
meldingen for stor, eller en blindkopi (Bcc) som ville gått tapt — opp etter første
forsøk. Mens Microsoft 365 er utilgjengelig for Vantigo (**E-postkanaler** slått av,
eller Entra-tilkoblingen feiler), venter meldingen uten å bruke opp forsøkene, og gis
først opp 24 timer etter at den ble sendt. Rett før hvert forsøk sjekker jobben
blokkeringslisten på nytt og avbryter levering til enhver adresse på den. I sjeldne
tilfeller kan en melding leveres to ganger etter et krasj; reglene står under
[Delivery semantics](/en/reference/communications/#delivery-semantics).

Innboks viser ennå ikke en leveringsstatus per melding; det den viser, er at meldingen
ble sendt fra Vantigo.

## Sett opp en kanal

En kanal er en delt postkasse: adressen svarene dine sendes fra. Åpne **Kanaler** for
tabellen over kanaler med **Adresse** (visningsnavnet, med adressen under),
**Leverandør**, **Status** (**Aktiv** eller **Inaktiv**) og **Handlinger**. Kanalen
merket **Standard** er den en samtale opprettes på når ingen er oppgitt; den første
kanalen du oppretter, blir standard — for sin type: e-postkanaler og Messenger-kanaler har
hver sin standard.

Kolonnen **Mottaksstatus** sier om en kanal mottar e-post: **Mottar**, med *Sist
synkronisert …*; **Venter på første synkronisering** rett etter at kanalen ble lagt til
eller byttet; **Feilet: …** med årsaken når Vantigo ikke kan lese postboksen —
*Applikasjonen kan ikke lenger lese postboksen. Gi den tilgang i Exchange Online.*,
*Postboksen finnes ikke lenger i Microsoft 365.* eller *Postboksen er ikke kjent for denne
kanalen. Bekreft kanalen.* — eller **Siste synkronisering feilet: …** når ett forsøk
feilet og Vantigo prøver igjen (*E-postkanaler gjennom Microsoft 365 er ikke
tilgjengelige.* mens **E-postkanaler** er av eller Entra-tilkoblingen feiler; e-posten
venter i postboksen). Hver årsak navngir kanalens egen leverandør. En SMTP-kanal viser **Sender bare**.

En kanal som ikke kan sende, viser et oransje merke **Utilgjengelig** i kolonnen
**Status**, med årsaken under: *Kanalen har ingen lagret legitimasjon og sender derfor
ingenting.* for en SMTP-kanal som mangler legitimasjon, eller *E-postkanaler gjennom
Microsoft 365 er ikke tilgjengelige, så kanalen sender ingenting.* for en
Microsoft 365-kanal mens leverandøren er av. **En SMTP-kanal uten legitimasjon sender
ingenting**: Vantigo faller aldri tilbake på e-postserverinnstillingene administratoren
har satt opp for sin egen e-post. En melding i kø på en slik kanal feiler med en gang, og
en kanal som er utilgjengelig tilbys ikke Fakturering eller Kalender som sted å sende fra.

En kanal sender gjennom sin **Leverandør**: SMTP, eller en postbokstjeneste Vantigo er
koblet til. Listen **Leverandør** i dialogen **Legg til e-postkanal** viser **SMTP**
først, deretter hver postbokstjeneste administratoren har slått på, med navnet sitt —
**Microsoft 365** og **Google Workspace** — og en tjeneste som er slått på, men ikke kan brukes akkurat nå, er
nedtonet, med *Ikke tilgjengelig nå* og årsaken ved siden av:

- **SMTP** — gjennom en e-postserver, med en SMTP-konto. Administratoren må ha
  applikasjonshemmeligheten på plass og kjenne SMTP-vert, port og konto; passordet
  lagres kryptert og vises aldri igjen, og hemmeligheten det krypteres under, er
  forklart under [SMTP](/en/reference/communications/#smtp). E-postserverinnstillingene en
  administrator setter opp for Vantigos egne invitasjoner og passordtilbakestillinger er
  noe annet: de skriver til dine egne ansatte, og ingen kanal, faktura eller invitasjon
  bruker dem noen gang.
- **Microsoft 365** — fra en **delt postboks** i Exchange Online, gjennom Microsoft 365,
  uten passord å legge inn. Den **mottar** også: e-post som kommer til innboksen i den
  delte postboksen fra den dagen kanalen legges til, kommer inn i Innboks — ikke e-posten
  som lå der fra før. Den står i listen når appen Microsoft Entra ID er på, og kan velges
  først når noen har slått på **E-postkanaler** i
  appen Microsoft Entra ID og en Exchange-administrator har gitt Vantigo tilgang til
  postboksen ([administrasjonsveiledningen](/nb/admin/entra/#mail-channels)). Når
  applikasjonens tilgang når alle postboksene i selskapet, sier et oransje varsel over
  tabellen fra, *E-posttilgangen gjelder alle postbokser*: *Applikasjonen har tilgang
  til alle postboksene i leietakeren. Begrens den med RBAC for Applications, og fjern
  samtykket som gjelder hele leietakeren. Så lenge denne advarselen vises, kan alle som
  administrerer kanaler i Kommunikasjon, sende som hvilken som helst delt postboks i
  leietakeren.* Kanalene virker likevel.
- **Google Workspace** — fra en lisensiert Gmail-postboks i organisasjonen, som `post@`,
  gjennom Google Workspace, uten passord å legge inn. Den **mottar** også, slik
  Microsoft 365 gjør. Den står i listen når appen Google Workspace er på, og kan velges
  først når en eier har slått på **E-postkanaler** på siden Google Workspace og en
  superadministrator i Google har delegert Gmail-tilgangsområdene
  ([administrasjonsveiledningen](/nb/admin/google-workspace/#e-postkanaler)). Google kan
  ikke begrense Vantigos tilgang til noen postbokser, så et oransje varsel
  *E-posttilgangen gjelder alle postbokser* vises over tabellen så lenge Google Workspace
  kan brukes: *E-posttilgangen gjennom Google Workspace gjelder alle postboksene i domenet:
  delegering på domenenivå kan ikke begrenses til noen postbokser. Så lenge denne
  advarselen vises, kan alle som administrerer kanaler i Kommunikasjon, sende som hvilken
  som helst postboks i domenet som ikke er en persons egen.* Vantigo bruker bare
  postboksene som er kanaler, og avviser en persons egen. Når begge tjenestene når alle
  postbokser, har hver sitt varsel.

Leverandørlisten i dialogen **Legg til e-postkanal** slutter med **Vantigo-driftet
e-post**, nedtonet og med *Ikke tilgjengelig ennå*: den tilbys ikke ennå og kan ikke
velges.

Fakturaer og Kalender sender til kundene dine **gjennom en kanal du velger**:
fakturainnstillingene har et kort **Utsending** og kalenderinnstillingene et kort
**Invitasjoner til personer utenfor selskapet**, hver med kanalene her og leverandøren
deres, eller **Avslått**
([Sende et dokument på e-post](/nb/user/invoices/#sende-et-dokument-på-e-post),
[Inviter folk utenfor bedriften](/nb/user/calendar/#inviter-folk-utenfor-bedriften)). Å
deaktivere en kanal de bruker, gjør at de sier at kanalen ikke finnes lenger, og de
sender ingenting før noen velger en annen.

### Legg til en SMTP-kanal

Klikk **Legg til kanal**. Dialogen **Legg til e-postkanal** spør etter:

- **E-postadresse** — avsenderadressen, obligatorisk.
- **Visningsnavn** — navnet mottakerne ser; vises i tabellen i stedet for adressen når
  det er satt.
- **Leverandør** — **SMTP**.
- **SMTP-vert** og **SMTP-port** (587 er fylt inn), begge obligatoriske, og kontoens
  **Brukernavn** og **Passord**.

**Opprett kanal** lukker dialogen og bekrefter med «Kanal opprettet — E-postkanalen er
klar.» En adresse som allerede har en kanal, avvises.

### Legg til en Microsoft 365-kanal

Klikk **Legg til kanal**, og velg **Microsoft 365** som **Leverandør**. Dialogen spør da
etter **E-postadresse** til den delte postboksen, et **Visningsnavn** og to innstillinger
for postboksen, og sier *Postboksen må være en delt postboks som applikasjonen har tilgang
til.*:

- **Importer historikk** — **Fra nå av** (standard), **Siste 30 dager** eller **Siste 90
  dager**.
- **Når en samtale lukkes** — **La stå uendret i postboksen** (standard), **Marker som lest
  i postboksen** eller **Flytt til en mappe i postboksen** (som spør etter **Mappe i
  postboksen**, fylt inn med `Handled in Vantigo`, opprettet i den delte postboksen hvis den
  ikke finnes fra før).

**Opprett
kanal** sjekker postboksen med Microsoft 365 først; kanalen opprettes aktiv, med
postboksens egen primære adresse, og kan sende med en gang. Sjekken kan avvise den:

| Dialogen sier | Betyr | Hvem retter det |
| --- | --- | --- |
| *Dette er en persons egen postboks. Bare en delt postboks kan være en kanal.* | Adressen er en persons postboks. | Bruk en delt postboks; en Exchange-administrator kan gjøre om en. |
| *Adressen er en gruppe. En gruppe har ingen postboks Vantigo kan lese; bruk en lisensiert postboks.* | Adressen hører til en gruppe, ikke en postboks (en e-posttjeneste med egne gruppeadresser sier fra; Microsoft 365 gjør det ikke). | Bruk adressen til en postboks. |
| *Postboksen er utenfor det applikasjonen har tilgang til. Gi applikasjonen tilgang til den i Exchange Online.* | Vantigo har ikke fått tilgang til postboksen, eller tilgangen virker ikke ennå — det kan ta opptil 2 timer. | En Exchange-administrator, med [Slik gir du applikasjonen tilgang](/nb/admin/entra/#mail-channels). |
| *Microsoft 365 har ingen postboks med denne adressen.* | Ingen postboks har adressen — eller den er et alias. | Bruk postboksens primære adresse. |
| *E-postkanaler gjennom Microsoft 365 er ikke tilgjengelige. En administrator slår dem på under Integrasjoner.* | **E-postkanaler** er av, eller Entra-tilkoblingen virker ikke. | Den som administrerer appen Microsoft Entra ID. |
| *En annen kanal bruker allerede denne postboksen.* | En annen kanal bruker den samme postboksen, kanskje med en annen av adressene. | Bruk den kanalen. |
| *Det finnes allerede en kanal med denne adressen.* | En annen kanal har adressen. | Bruk den kanalen. |
| *Microsoft 365 svarte ikke. Prøv igjen om litt.* | Microsoft 365 var opptatt eller utilgjengelig. | Prøv igjen. |

Hver melding navngir leverandøren du valgte. Å legge til, bytte og bekrefte kanaler er begrenset til 30 i minuttet; over det,
*For mange forsøk. Vent et minutt og prøv igjen.*

### Legg til en Google Workspace-kanal

Klikk **Legg til kanal**, og velg **Google Workspace** som **Leverandør**. Dialogen spør
etter det samme som for Microsoft 365: **E-postadresse** til postboksen, et
**Visningsnavn**, **Importer historikk** og **Når en samtale lukkes**. Merknaden sier
*Postboksen må være en lisensiert bruker i organisasjonen i Google Workspace som ikke er
noen persons egen, for eksempel post@. En gruppe kan ikke være en kanal.*: Google
Workspace har ingen egen delt postboks, så postboksen er en bruker laget for formålet, og
den tar en lisens.

**Opprett kanal** sjekker adressen med Google Workspace først. Et alias tas som
postboksen sin, og kanalen opprettes aktiv med postboksens primære adresse. Sjekken
avviser, med meldingene i tabellen ovenfor:

- **En gruppes adresse** — *Adressen er en gruppe. En gruppe har ingen postboks Vantigo kan
  lese; bruk en lisensiert postboks.* En Google-gruppe, også en Collaborative Inbox, tar
  imot e-post for medlemmene sine, men har ingen egen postboks. Bruk adressen til en
  lisensiert bruker, og la gruppen sende videre til den hvis du trenger begge.
- **En persons egen postboks** — *Dette er en persons egen postboks i Google Workspace.
  Bare en postboks som ikke er noen persons egen, for eksempel post@, kan være en kanal.*
  når adressen hører til en som Vantigo holder en Google-konto for gjennom Ansatte, eller
  som har koblet sin egen Google-konto til Vantigo. En kanal skal aldri lese eller sende
  fra en kollegas egen e-post.
- **En adresse Google Workspace ikke kjenner** — *Google Workspace har ingen postboks med
  denne adressen.*: ingen bruker har den, brukeren er suspendert, arkivert eller har ingen
  Gmail-lisens, eller adressen er ikke i et av organisasjonens bekreftede domener.
- **En postboks Google avviser** — *Google Workspace avviste postboksen av en annen grunn
  enn delegeringen, for eksempel en regel for brukeren. Sjekk brukeren i Google
  Admin-konsollen.*, med en lenke, **E-postkanaler i Google Workspace**, til
  [administrasjonsveiledningen](/nb/admin/google-workspace/#e-postkanaler).
- *E-postkanaler gjennom Google Workspace er ikke tilgjengelige …* mens **E-postkanaler**
  er av på siden Google Workspace, eller Gmail-tilgangsområdene ikke er delegert ennå.

Hva som er annerledes enn for Microsoft 365, når kanalen virker:

- **Størrelse.** En melding på kanalen kan være opptil 25 MB — Gmails grense — så
  skrivevinduet tar vedlegg på opptil omtrent **18,2 MB** til sammen.
- **Mottak** hentes, det varsles ikke: Vantigo ser etter ny e-post omtrent **hvert femte
  minutt**, og en melding kommer innen det.
- **Sendt e-post** ligger i postboksens **Sent** i Gmail. Gmail kan gi en sendt melding sin
  egen Message-ID; Vantigo tar vare på begge, og på Gmails tråd, så et svar fra kunden
  havner i samtalen likevel.
- **Når en samtale lukkes**: Gmail har etiketter, ikke mapper. **Marker som lest i
  postboksen** tar bort meldingens *Ulest*; **Flytt til en mappe i postboksen** gir den
  etiketten som står i **Mappe i postboksen** (`Handled in Vantigo` med mindre du endrer
  det), opprettet hvis den ikke finnes, og tar den ut av **innboksen**.
- **Å arkivere eller slette** en melding i Gmail etter at Vantigo har lest den, endrer
  ingenting i Vantigo; e-post et Gmail-filter holder unna innboksen, leses aldri.
- **En postboks som blir en persons egen** stopper: holder Ansatte senere en Google-konto
  for brukeren til postboksen, kobler brukeren sin egen Google-konto til Vantigo, eller
  får postboksens adresse nytt navn eller gis til en annen, feiler kanalen med *Postboksen
  finnes ikke lenger i Google Workspace.* og sender og leser ikke mer. En avvist postboks
  viser *Feilet: Google Workspace avviser postboksen. Sjekk brukeren i Google
  Admin-konsollen.*
- **En mappe eller kategori med navnet til en systemetikett i Gmail** — *Inbox*, *Sent*,
  *Trash*, *Spam*, *Drafts*, *Starred*, *Important*, *Unread*, *Chat* — avvises: velg et
  annet navn i **Mappe i postboksen**.

### Bytt en SMTP-kanal til en postbokstjeneste

Når postboksen en SMTP-kanal sender fra er en delt postboks i Exchange Online, kan
kanalen sende gjennom Microsoft 365 i stedet, og beholder samtalene, adressen og merket
**Standard**. På raden, velg **Bytt til Microsoft 365** — det er én **Bytt til …** for hver
postbokstjeneste som kan brukes nå, **Bytt til Google Workspace** for en Gmail-postboks; dialogen advarer *SMTP-innstillingene
slettes. For å gå tilbake må du legge dem inn på nytt.*, og tilbyr de samme valgene for
**Importer historikk** og **Når en samtale lukkes** som når du legger til en Microsoft
365-kanal — velg **Bytt**. Vantigo sjekker
postboksen som når du legger til en, med de samme meldingene, og leverandøren på raden
blir **Microsoft 365**: «Kanalen er byttet — Kanalen sender og mottar nå gjennom
Microsoft 365.» Fra da av kommer e-posten i postboksen fra det valgte importvinduet inn i
Innboks. Handlingen vises bare mens Microsoft 365 er tilgjengelig. En kanal som allerede
er på én postbokstjeneste, kan ikke byttes direkte til en annen: *Kanalen bruker en annen
postboksleverandør. Bytt den tilbake til SMTP først, og deretter til den nye
leverandøren.*

På raden til en Microsoft 365- eller Google Workspace-kanal åpner **Postboksen ved lukking** en dialog der du kan
endre **Når en samtale lukkes** (*La stå uendret i postboksen*, *Marker som lest i
postboksen* eller *Flytt til en mappe i postboksen* med **Mappe i postboksen**).

Kanalen beholder adressen den hadde. Er adressen et alias for den delte postboksen og
ikke den primære adressen, sjekk at den første meldingen går ut som forventet: hvordan
Microsoft 365 sender fra et alias, er ikke verifisert ennå. Å legge til kanalen på nytt
med postboksens primære adresse unngår spørsmålet.

**Det finnes ingen knapp for å gå tilbake.** Siden Kanaler kan ikke bytte en kanal
tilbake til SMTP; en administrator kan, gjennom API-et, ved å sende kanalens
SMTP-innstillinger på nytt med passordet ([Switching provider](/en/reference/communications/#switching-provider)).

### Bekreft og deaktiver

Per kanal sjekker **Bekreft** at kanalen kan sende: for SMTP kobler den til serveren med
de lagrede opplysningene, for Microsoft 365 og Google Workspace sjekker den postboksen igjen, og den melder
«Kanal bekreftet — Kanaltilkoblingen er gyldig.» Når det feiler, sier *Kanalen ble ikke
bekreftet* hvorfor — for Microsoft 365 med meldingene ovenfor, eller *Adressen hører nå
til en annen postboks i Microsoft 365.* **Deaktiver** beholder kanalen, men stopper
sending: et svar i en samtale på en inaktiv kanal avvises til **Aktiver** slår den på
igjen.

Det finnes ingen begrensning per bruker på en kanal. Alle som kan svare i Innboks,
svarer fra kanalen samtalen hører til, så hvem som kan bruke en kanal, avgjøres av hvem
som har `communications:conversations-reply`. Å administrere kanaler krever
`communications:channels-manage`.

## Blokker en adresse

En blokkering er en adresse Vantigo ikke vil sende til — en person som har bedt om å
ikke bli skrevet til, eller en adresse som er kjent død. Åpne **Blokkeringer** for
listen, med hver adresses **E-post**, **Årsak** og **Opprettet**-dato, og et
**Søk**-felt som treffer e-posten eller årsaken.

For å legge til en, fyll inn **E-postadresse** (en ugyldig adresse avvises med «Skriv
inn en gyldig e-postadresse») og eventuelt en **Årsak**, og klikk **Legg til
blokkering**. Du ser «Blokkering lagt til — Adressen vil ikke motta meldinger.» Legger
du til en adresse som allerede står på listen, beholdes den eksisterende oppføringen
med den opprinnelige årsaken.

For å fjerne en, klikk **Slett** på raden og bekreft med **Slett blokkering** i dialogen
som spør «Tillat denne adressen å motta meldinger igjen?» Bekreftelsen lyder
«Blokkering slettet — Adressen kan motta meldinger igjen.»

Blokkeringer sammenlignes uten hensyn til store og små bokstaver. Et svar til en
blokkert adresse avvises når det sendes, og utboksen sjekker listen en gang til før
hvert leveringsforsøk. Vantigo legger ikke til blokkeringer på egen hånd: en
returmelding («bounce») som når en Microsoft 365-kanal, kommer inn i Innboks som enhver
annen e-post, og adressen må legges til her for hånd. Blokkeringer krever
`communications:suppressions-manage`.

## Rettigheter

| For å | Trenger du |
| --- | --- |
| Åpne Innboks, lese samtaler, markere dem som lest, laste ned vedlegg og originaler | `communications:conversations-view` |
| Svare, legge ved filer, skrive utkast med KI | `communications:conversations-reply`, sammen med view |
| Endre status, tagger og kundekobling, legge til notater, be om et kundeforslag | `communications:conversations-manage`, sammen med view |
| Se og administrere Kanaler | `communications:channels-manage` |
| Se og administrere Blokkeringer | `communications:suppressions-manage` |

Et område du mangler rettighet til, vises ikke i sidemenyen. Rettigheter gis gjennom
roller under Administrasjon av arbeidsområdet — se
[Finn fram i Vantigo](/nb/user/).
