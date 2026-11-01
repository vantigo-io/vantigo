---
title: Assistent
description: Spør assistenten hvordan du gjør noe, eller om dataene dine — åpne den fra toppfeltet, med Cmd/Ctrl+J eller fra søket, panelet på siden og kontekstbrikken, appen Assistent med alle samtalene, hva den kan og ikke kan ennå, hva som er privat og hvor lenge det beholdes, hva som sendes til AI-leverandøren, og hvorfor den kanskje ikke er der.
sidebar:
  order: 3
sources:
  - apps/assistant/frontend
  - apps/host/frontend/src/routes/assistant
  - apps/host/frontend/src/routes/assistant.tsx
---

**Assistenten** svarer på «hvordan gjør jeg…» fra dokumentasjonen til Vantigo og viser
deg veien, og den leser dataene du har lov til å lese og svarer med en tabell du kan
klikke deg videre fra. Den gjør aldri noe du ikke selv kunne gjort i grensesnittet, og i
denne versjonen gjør den ingen endringer i det hele tatt: ber du den opprette eller endre
noe, sier den fra om det og gir deg trinnene. De nøyaktige reglene bak — hvem som har
den, hva den kan bruke, hva den beholder — står i
[referansen for Assistent](/en/reference/assistant/) og, for en eier, i
[Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#assistent).

## Åpne den

Det er tre måter, og de to første virker på alle skjermbilder:

- **Assistent-knappen** i toppfeltet (den heter *Åpne assistenten*). Et merke på knappen
  forteller at assistenten jobber med et svar, eller har svart mens panelet var lukket.
- **Cmd+J** på Mac, **Ctrl+J** ellers. Tasten ignoreres mens du skriver i en editor som
  bruker den selv.
- **Søk.** Skriv et spørsmål i søkefeltet i toppfeltet. Når ingenting passer godt, slutter
  listen med **Spør assistenten: «…»**; velg den, og panelet åpnes med spørsmålet allerede
  sendt.

Knappen og appen vises bare for de som har assistenten
([hvorfor den kanskje ikke er der](#når-den-ikke-er-der)).

## Panelet

Panelet åpnes til høyre og **skyver siden til side** i stedet for å dekke den, så du kan
lese et skjermbilde mens du spør om det. Dra i kanten for å gjøre det bredere eller
smalere; bredden huskes i denne nettleseren. På en smal skjerm fyller det hele skjermen.
Det er lukket hver gang du åpner Vantigo, og det **blir stående åpent når du går mellom
apper**.

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
når du trykker på den; assistenten flytter aldri skjermen selv.

**Lese dataene dine.** Spør «hvilke fakturaer er forfalt?» eller «vis målepunktene til Ola
Nordmann», og den leser det du har lov til å lese og svarer med en tabell der hver post er
en lenke til skjermbildet sitt. Den ser nøyaktig det du ville sett: en post eller et helt
område du ikke har rettighet til, er heller ikke tilgjengelig for den, og en eier kan
snevre den inn ytterligere
([Hva assistenten kan bruke](/nb/user/workspace-administration/#hva-assistenten-kan-bruke)).
Får den ikke lov til å lese noe, sier den det; den prøver ikke å komme rundt det.

**Ta feil.** Assistenten er en språkmodell, og den kan misforstå et spørsmål eller en
tabell. Kontroller det som betyr noe mot skjermen: hver post i et svar lenker til den.

## Hva den ikke kan ennå

**Den gjør ingen endringer.** Ber du den opprette en faktura, godkjenne timer eller endre
en kunde, sier den tydelig at den ikke kan endre noe ennå, og gir deg trinnene så du kan
gjøre det selv. Endringer — hver av dem forelagt deg først — kommer i en senere versjon.
Den fyller ikke ut skjemaet på skjermen din.

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
  assistenten bruker et verktøy eller leser fra en operasjon på dine vegne, noterer
  samtalen hvilken, i hvilken modus og hvilken status som kom tilbake. Den oversikten har ingen
  data. Det assistenten leste, forkortet, ligger i selve samtalen så den kan fortsette derfra,
  og forsvinner når samtalen gjør det.
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
travel eller nede; prøv igjen om litt. Et langt svar fra en modell på beskjeden maskinvare
kan ta minutter.
