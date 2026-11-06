---
title: Assistent
description: Spør assistenten hvordan du gjør noe, om dataene dine, eller be den endre noe — åpne den fra toppfeltet, med Cmd/Ctrl+J eller fra søket, panelet på siden og kontekstbrikken, appen Assistent med alle samtalene, endringene den foreslår og du bekrefter, en endring i mange poster på én gang (forhåndsvisning, de fem første, resten i bakgrunnen, elementene som mislyktes), hva som er privat og hvor lenge det beholdes, hva som sendes til AI-leverandøren, og hvorfor den kanskje ikke er der.
sidebar:
  order: 3
sources:
  - apps/assistant/frontend
  - packages/frontend-shell/src/side-panel.tsx
  - apps/host/frontend/src/routes/__root.tsx
  - apps/host/frontend/src/routes/assistant
  - apps/host/frontend/src/routes/assistant.tsx
---

**Assistenten** svarer på «hvordan gjør jeg…» fra dokumentasjonen til Vantigo og viser
deg veien, og den leser dataene du har lov til å lese og svarer med en tabell du kan
klikke deg videre fra. Den gjør aldri noe du ikke selv kunne gjort i grensesnittet, og den
endrer aldri noe på egen hånd: ber du den opprette eller endre noe, viser den deg endringen på
et kort først, og **ingenting lagres før du trykker Bekreft**. De nøyaktige reglene bak — hvem som har
den, hva den kan bruke, hva den beholder — står i
[referansen for Assistent](/en/reference/assistant/) og, for en eier, i
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#assistent).

## Åpne den

Det er tre måter, og de to første virker på alle skjermbilder:

- **Assistent-knappen** i toppfeltet (den heter *Åpne assistenten*). Et merke på knappen
  forteller at assistenten jobber med et svar, **venter på at du bekrefter en endring**, eller
  har svart mens panelet var lukket. Foreslår den en endring mens panelet er lukket, sier et
  varsel fra også om det.
- **Cmd+J** på Mac, **Ctrl+J** ellers. Tasten ignoreres mens du skriver i en editor som
  bruker den selv.
- **Søk.** Skriv et spørsmål i søkefeltet i toppfeltet. Når ingenting passer godt, slutter
  listen med **Spør assistenten: «…»**; velg den, og panelet åpnes med spørsmålet allerede
  sendt.

Knappen og appen vises bare for de som har assistenten
([hvorfor den kanskje ikke er der](#når-den-ikke-er-der)).

## Panelet

Panelet er sidepanelet Vantigo deler mellom to ting: **Assistenten** og, for de som kan lese
samtaler, [Samtaler-panelet](/nb/user/communications/#samtaler-panelet). Det åpnes til høyre og
**skyver siden til side** i stedet for å dekke den, så du kan lese et skjermbilde mens du spør
om det. Dra i kanten, eller sett fokus på kanten og bruk venstre- og høyrepiltasten, for å gjøre
det bredere eller smalere. På en smal skjerm fyller det hele skjermen. Det er lukket hver gang du
åpner Vantigo, og det **blir stående åpent når du går mellom apper**.

**Ett panel eller begge.** Har du begge panelene, viser toppen av sidepanelet en fane for hvert,
og ett panel om gangen fyller det. Knappen **Vis begge panelene** i den raden (i et vindu på rundt
990 piksler eller mer) stabler dem i stedet: Assistenten øverst, Samtaler under, med en skillelinje
mellom. Dra i skillelinjen, eller sett fokus på den og bruk opp- og nedpiltasten; hvert panel beholder
minst 200 piksler. **Vis ett panel om gangen** tar deg tilbake til fanene. I et smalere vindu er det
alltid ett panel om gangen. Assistent-knappen og **Cmd/Ctrl+J** åpner sidepanelet på Assistenten, eller
åpner begge panelene når det er modusen du har valgt; samme tast lukker det igjen.

Et panel lastes første gang du viser det og blir så liggende, uansett hvilken fane som er foran og
hvor du går i Vantigo, så et halvskrevet svar, stedet du hadde rullet til og samtalen du hadde åpen
er der fortsatt når du kommer tilbake. Bredden, modusen, skillelinjens plass og panelet du brukte sist
huskes i denne nettleseren; om panelet er åpent huskes ikke. Sidepanelet lukkes av seg selv på
Assistent-appens egen side og, for Samtaler, i Innboksen, som viser det samme.

- **Det åpnes på den siste samtalen din**, på hvilken som helst enhet: den du hadde åpen
  sist. **Ny samtale** er den eneste måten å begynne på en fersk.
- **Toppen** har samtalens tittel, **Siste samtaler**, **Ny samtale** og **Åpne i
  Assistent**, som åpner den samme samtalen i hele appen.
- **Kontekstbrikken** over meldingsfeltet sier hva du ser på — *Ser på: Kunde Ola
  Nordmann* — slik at «denne kunden» og «her» betyr noe. Brikken følger deg når du
  navigerer. Assistenten får vite appen, skjermbildet og navnet på posten, aldri hva som
  står på skjermen. Trykk på krysset på brikken, **Ikke send det jeg ser på**, så går den
  neste meldingen uten.
- **Stopp** avslutter et svar som er på vei. Du kan også lukke panelet eller forlate
  siden: assistenten gjør svaret ferdig på serveren, og det står i samtalen når du kommer
  tilbake.

## Appen Assistent

Velg **Assistent** i app-velgeren, eller **Åpne i Assistent** i panelet, for appen på
`/assistant`. Den er lagt opp som en chat: en sidemeny med samtalene dine og den åpne
samtalen ved siden av.

- **Hver samtale du har hatt, fra hvor som helst i Vantigo.** De er gruppert **Festet**,
  **I dag**, **I går**, **Siste 7 dager** og **Eldre**, og hver sier hvor den begynte —
  *Startet i Energi*.
- **Søk i samtaler** finner en samtale på tittelen eller på ord i den.
- **Gi nytt navn**, **Fest** / **Løsne**, **Arkiver** og **Slett** ligger i handlingsmenyen
  til hver samtale. Arkiverte samtaler forsvinner fra listen; bryteren **Arkivert** viser
  dem, og **Hent tilbake fra arkivet** henter en tilbake. **Slett** spør først og fjerner
  meldingene for godt.
- **Fortsett en hvilken som helst samtale** ved å åpne den og skrive: den tar opp tråden
  der den slapp. Assistenten svarer på én melding om gangen; mens den fortsatt svarer, kan
  du ikke sende en til.

En samtale får navn etter det første spørsmålet ditt til du gir den nytt navn.

## Hva den kan

**Svare på hvordan-spørsmål.** Spør «hvordan legger jeg til et målepunkt?», og
assistenten søker i dokumentasjonen, på ditt språk, og svarer med nummererte trinn med
navnene grensesnittet bruker, en lenke til dokumentasjonssiden den brukte og, når et
skjermbilde er svaret, en knapp **Ta meg dit** som åpner det. Knappen åpner skjermbildet
når du trykker på den; assistenten flytter aldri skjermen selv. For de viktigste
opprettelsesflytene legger den også til en knapp **Vis meg** som starter en omvisning
([nedenfor](#vis-meg-hvordan)).

**Lese dataene dine.** Spør «hvilke fakturaer er forfalt?» eller «vis målepunktene til Ola
Nordmann», og den leser det du har lov til å lese og svarer med en tabell der hver post er
en lenke til skjermbildet sitt. Den ser nøyaktig det du ville sett: en post eller et helt
område du ikke har rettighet til, er heller ikke tilgjengelig for den, og en eier kan
snevre den inn ytterligere
([Hva assistenten kan bruke](/nb/user/workspace-administration/#hva-assistenten-kan-bruke)).
Får den ikke lov til å lese noe, sier den det; den prøver ikke å komme rundt det.

**Ta feil.** Assistenten er en språkmodell, og den kan misforstå et spørsmål eller en
tabell. Kontroller det som betyr noe mot skjermen: hver post i et svar lenker til den.

## Vis meg hvordan

Under de skrevne trinnene i et hvordan-svar kan assistenten legge til en knapp **Vis meg**. Den
starter en omvisning: skjermen dimmes, én knapp eller ett felt lyses opp om gangen, og et kort
notat på ditt språk sier hva du skal gjøre med det. «Hvordan legger jeg til et målepunkt?» lyser
opp **Nytt målepunkt**, så feltene som betyr noe, så **Opprett målepunkt**.

- **Den starter bare når du trykker på knappen.** Aldri av seg selv, og de skrevne trinnene
  kommer alltid først.
- **Den åpner riktig skjermbilde.** Ligger første knapp på et annet skjermbilde, åpner omvisningen
  det, og panelet blir stående ved siden av. Noe som ligger på én posts skjermbilde (en kunde, et
  målepunkt, en faktura) kan bare pekes på mens posten er åpen, så spør fra posten.
- **Neste, Tilbake og Escape.** **Neste** og **Tilbake**, eller piltastene, flytter deg mellom
  stegene; **Lukk** eller Escape avslutter omvisningen, og fokus går tilbake til knappen **Vis
  meg**. På en smal skjerm, der panelet dekker siden, lukkes panelet når omvisningen starter.
- **Skjemaer.** Et felt i et skjema er på skjermen først når skjemaet er åpent. Notatet sier det:
  trykk selv på den opplyste knappen, og så **Neste**. Det er mulig å klikke på det opplyste
  elementet gjennom det dimmede laget. Omvisningen klikker, skriver eller lagrer aldri noe for deg.
- **En knapp som ikke er der.** Mangler du rettigheten til den, eller har den ikke dukket opp
  innen tre sekunder, vises steget midt på skjermen med samme notat.
- **Hvor den virker.** De viktigste opprettelsesflytene: kunder og kontakter, målepunkter og
  leveringsperioder, fakturaer, timeføringer, utlegg og reiseregninger, og prosjekter. Andre steder
  får du bare de skrevne trinnene.

## Endringer, med din bekreftelse

Be om «sett betalingsbetingelsene til Ola Nordmann til 30 dager» eller «opprett et prosjekt som
heter Utrulling for Acme», så finner assistenten riktig operasjon og **foreslår** endringen. Den
gjør den ikke. Et **bekreftelseskort** vises i samtalen:

- **Hva som skjer**, i én setning, og posten det gjelder som en lenke.
- **Feltene** den vil sette, hver med verdien nå og den nye — *14 → 30*. Verdien nå vises når en
  lesing av den samme posten kan si hva som står der; for en ny post, eller en du ikke har lov til
  å lese, vises bare de nye verdiene.
- **Bekreft** og **Avbryt**. Ingenting er lagret ennå. Fokus havner på kortet, ikke på
  **Bekreft**, så Enter bekrefter ikke ved et uhell.

Trykk **Bekreft**, og endringen gjøres **som deg**, etter de samme reglene som om du hadde gjort
den selv på skjermbildet sitt: har du ikke lov, blir den avvist. Assistenten leser så resultatet
og forteller deg hva som skjedde, og **skjermbildet du er på, oppdateres** så det viser
endringen. Trykk **Avbryt**, og ingenting endres; assistenten svarer med én linje.

- **Bekreftelsen kan vente.** Kortet blir stående i samtalen. Last siden på nytt, kom tilbake
  fra en annen enhet, eller bekreft en halvtime senere: det er der fortsatt og virker. Knappen
  til assistenten viser at en endring venter, og meldingsfeltet er av til du har bestemt deg.
  **Etter 24 timer** utløper kortet: det merkes *Utløpt*, og ingenting endres.
- **Én endring om gangen.** Ber du om flere, foreslår assistenten den første og tilbyr den neste
  når du har avgjort den.
- **Hvis posten ble endret i mellomtiden**, fordi noen andre redigerte den etter at kortet ble
  laget, gjøres ikke endringen over deres arbeid. Kortet sier *Mislyktes*, assistenten forteller
  deg at posten ble endret, og du kan be om det på nytt.
- **Når kortet er avgjort, viser det hvordan det endte** — *Endret*, *Avbrutt*, *Utløpt* eller
  *Mislyktes*, med årsak — og navnene på feltene. Verdiene du bekreftet, beholdes ikke i
  samtalen; modulens egen historikk viser endringen som gjort av deg.
- **En eier kan slå endringer av** for hele arbeidsområdet, eller for deler av det, under
  **Administrer arbeidsområde** › **Assistent**; et kort som venter når det skjer, kan ikke
  lenger bekreftes.

Noen operasjoner er aldri tilgjengelige for assistenten, for eksempel en eiers innstillinger,
innlogging og passord, og filopplastinger, uansett hva eieren setter.

## Masseendringer

Be om "gi alle målepunkter i prisområde NO5 produktet *Spot Bergen*" eller "sett betalingsvilkårene til
alle kunder med etiketten *Grossist* til 30 dager", så foreslår assistenten **den samme endringen for
alle på én gang**. Den gjør den ikke. Den leser listen du beskrev, som deg, og et kort som heter
**Masseendring** dukker opp i samtalen. En masseendring har fire trinn, og du avgjør hvert av dem.

1. **Forhåndsvisningen.** Kortet sier **hvor mange elementer som ble funnet** og viser **de 10 første**,
   hver med verdien den har nå og den nye. Det sier hvor mange som **hoppes over**, og hvorfor: et element
   som listen ga uten verdien endringen trenger, eller som står to ganger. Det sier også at Vantigo sjekker
   hver oppføring når endringen kjøres, så noen kan likevel bli avvist. Ingenting er endret. **Start** og
   **Avbryt** er de eneste knappene.
2. **Start** endrer **de 5 første elementene**, på ekte, som deg. Kortet viser hvordan hvert av dem gikk.
   Var det ikke flere enn 5, er det alt.
3. **Fortsett** endrer resten **i bakgrunnen, 100 om gangen**. Kortet viser en fremdriftsstolpe og hvor
   mange som har lyktes, mislyktes og blitt hoppet over, og en **Stopp**-knapp. Du kan lukke panelet og
   gjøre noe annet; jobben fortsetter.
4. **Slutten.** Kortet sier hvor mange som ble endret, og lister **elementene som mislyktes**, hvert med
   avslaget Vantigo ga (statusen og tittelen). **Last ned mislykkede elementer (CSV)** lagrer hele listen
   som en fil du kan åpne i et regneark. Skjermen du er på oppdateres, og assistenten oppsummerer
   resultatet i én kort melding.

- **Det er en forhåndsvisning av endringen, ikke en øvelse.** Vantigos skjermbilder skriver direkte, og
  noen endringer gjør også noe utenfor databasen, som å sende en melding, så en endring kan ikke prøves og
  tas tilbake. Kortet kaller seg aldri en prøve: det **Start** endrer, er endret, og å stoppe angrer det
  ikke. Derfor kommer bare 5 elementer først, og derfor avgjør du om det skal fortsette.
- **Opptil 2 000 elementer.** Passer flere, sier assistenten hvor mange og ber deg snevre inn filteret,
  for eksempel med et prisområde eller en etikett.
- **Elementene er faste når du ser dem.** Jobben endrer nøyaktig de elementene du så telt. En oppføring
  som legges til eller begynner å passe etterpå, berøres ikke.
- **Stopp.** **Stopp** virker **mellom bolker på 100**, så opptil 100 elementer til kan bli endret etter at
  du har trykket. Kortet sier så *Stoppet etter … av …*. Det som allerede er endret, angres ikke.
- **Mislykkede elementer.** En oppføring kan avvises etter sine egne regler (du har ikke lov til å endre
  den kunden, noen andre endret den i mellomtiden). Det elementet mislykkes, og de andre fortsetter.
  Kortet lister de første 50, og CSV-filen har alle.
- **Elementer som kanskje er endret.** Hvis serveren som gjør endringene stopper mens den holder på med et
  element (en omstart, et krasj), tar en annen server over jobben og går videre med det neste. Den
  **kjører aldri det elementet på nytt**, for den kan ikke vite om endringen ble gjort. Kortet merker det
  **Ukjent**, viser slike element først blant dem som ikke gikk gjennom, med setningen *Denne endringen kan
  være gjort eller ikke; sjekk den*, og CSV-filen har dem med statusen `unknown`. Åpne hvert enkelt og se
  etter: er endringen der, trengs ikke mer; ellers gjør du den selv eller ber assistenten foreslå den på nytt.
- **Hvorfor en lang jobb ber deg om å fortsette.** Assistenten handler med din tillatelse, som hører til
  økten din og varer en time som lengst. En jobb som ville vart lenger, **settes på pause**, og kortet
  sier *Bekreft for å fortsette*: **Fortsett** gir den en ny time. **Logger du ut, stopper jobben** ved
  neste element, fordi assistenten ikke lenger har din tillatelse.
- **Det Eieren bestemmer, gjelder hvert element.** Innskrenker en Eier hva assistenten kan gjøre mens en
  jobb kjører, stopper jobben på slutten av bolken den er i
  ([Hva assistenten kan bruke](/nb/user/workspace-administration/#hva-assistenten-kan-bruke)).
- **Venting.** En plan ingen starter, og en jobb som venter på **Fortsett**, utløper etter 24 timer. Mens
  en masseendring venter eller kjører, er meldingsfeltet av, som for en enkelt endring; **Stopp** på
  kortet, eller stoppknappen ved meldingsfeltet, stopper jobben.

## Hva den ikke kan

**Den fyller ikke ut skjemaet på skjermen din.** Den oppretter eller endrer posten gjennom
Vantigos eget grensesnitt bak skjermbildene, med din bekreftelse, og tilbyr så å åpne den.

## Lenker i svar

En lenke i et svar åpner bare et skjermbilde i Vantigo eller dokumentasjonen til denne
installasjonen. Enhver annen adresse — en nettside, `mailto:`, `tel:` — er ikke en lenke: den
vises som tekst med adressen synlig, så du ser hvor den ville ført før du bestemmer deg for å
kopiere den.

## Personvern

- **Samtalene dine er bare dine.** Ingen andre kan åpne, søke i eller eksportere dem —
  ikke en annen bruker, ikke en kollega med samme rolle, ikke en eier eller en
  administrator. Det finnes verken skjermbilde eller API for det.
- **Hvor lenge de beholdes.** En samtale slettes, hel, når siste aktivitet er eldre enn
  oppbevaringstiden til arbeidsområdet: **12 måneder** med mindre en eier har satt en
  annen tid (1 til 60 måneder). Du kan slette en selv når som helst. Slettes kontoen din,
  forsvinner samtalene dine med den innen et døgn.
- **Hva som sendes til AI-leverandøren.** For å svare sender assistenten leverandøren
  spørsmålene dine og samtalen så langt, utdragene fra dokumentasjonen den finner, dataene
  den leser for deg, skjermbildet brikken din nevner, og navnet og språket ditt. Ingenting
  sendes før du skriver en melding. Kjører modellen på organisasjonens eget nettverk,
  forlater ingenting av det nettverket; ellers er en eier gjort kjent med, under
  **Administrer arbeidsområde** › **Assistent**, hvor det går (*Spørsmål og dataene
  assistenten leser, sendes til …*). Spør administratoren din hvilket som gjelder for deg.
- **Det den gjør for deg, er begrenset til det du selv kan gjøre, og logges.** Hver gang
  assistenten bruker et verktøy, leser fra en operasjon eller gjør en endring du bekreftet,
  noterer samtalen hvilken, i hvilken modus (for en endring: at du bekreftet den) og hvilken
  status som kom tilbake. Den oversikten har ingen data. Det assistenten leste, forkortet, ligger i selve samtalen så den kan fortsette derfra,
  og forsvinner når samtalen gjør det.
- **Hva eieren ser.** En eier kan se, per person og dag, hvor mange samtaler, kjøringer,
  modelltokens og bekreftede endringer det var — et tall for kostnad og misbruk. Aldri hva som
  ble sagt, og aldri hva som ble endret.
- **En kundes data og assistenten.** Har du spurt den om en kunde, står svaret i samtalen
  din til oppbevaringstiden sletter den. Sletting av en kunde etter GDPR søker ikke i
  samtaler; oppbevaringstiden er svaret.

## Når den ikke er der

Assistent-knappen og appen vises bare for den som har assistenten. Ser du dem ikke, er en
av disse grunnen:

- **Installasjonen har ingen AI-leverandør.** Ingen har assistenten før administratoren
  som driver installasjonen har satt opp en
  ([Konfigurasjon](/nb/admin/configuration/#ki)).
- **En eier har slått den av.** Under **Administrer arbeidsområde** › **Assistent** kan
  innstillingen være *Slått av for alle*.
- **Den er per person, og bryteren din er av.** Med *Per person* har bare de som har sin
  egen bryter på, assistenten. Be en eier slå den på under **Administrer arbeidsområde** ›
  **Brukere**.
- **Din egen tilgang til Vantigo er avsluttet.** En deaktivert eller utestengt konto har
  ingenting.

Er assistenten der, men et svar feiler med *AI-tjenesten svarte ikke*, er modellen bak
travel eller nede; prøv igjen om litt. Står det *Du har brukt opp assistentens tokenbudsjett for i
dag*, har en eier satt en daglig grense per person, og du har nådd den; assistenten er tilgjengelig
igjen neste forretningsdag. Et langt svar fra en modell på beskjeden maskinvare
kan ta minutter.
