---
title: Kunnskap
description: Kunnskap-appen — hvem som kan bruke den, sidepanelets deler Favoritter, Arbeidsområde, Delt og Privat, å opprette og organisere sider, å skrive med redigeringsverktøyet, automatisk lagring og hva som skjer når noen andre har endret siden, å dele og låse en side, å skjule den for assistenten, historikk, papirkurv, favoritter, søk og å se hvem andre som er på siden.
sidebar:
  order: 57
sources:
  - apps/knowledge/frontend
  - apps/host/frontend/src/routes/knowledge
  - apps/host/frontend/src/lib/knowledge-access.ts
---

Kunnskap-appen er bedriftens egen håndbok: rutiner, retningslinjer og veiledninger, skrevet
som sider som kan ligge inni hverandre. En side kan være åpen for alle i bedriften, delt med
navngitte personer eller privat for deg. Reglene bak hver skjerm — rettigheten hver forespørsel
kontrolleres mot og hva et avslag betyr — står i referansen for Kunnskap; denne siden handler
om hva du gjør på hver skjerm.

## Hvem som kan bruke Kunnskap

Flisen **Kunnskap** i appvelgeren vises for alle som har en rolle med **Bruke Kunnskap**
(`knowledge:articles`). **Ingen rolle har den som standard**: en administrator gir den til en
rolle under **Roller og tilgang** ([Arbeidsområdeadministrasjon](/nb/user/workspace-administration/)).
**Administrere Kunnskap** (`knowledge:manage`) er for administratorer: den lar dem se og endre
tilgangen til alle sider som ikke er private, gjenopprette andres sider fra papirkurven og tømme
papirkurven.

Hva du kan gjøre med en side, avhenger så av sidens egen tilgang: om du kan lese den og om du kan
redigere den ([Del en side](#del-en-side)). En side du ikke har lov til å lese, vises ikke for deg.

## Sidepanelet

Sidepanelet i appen viser sidene som et tre, i disse delene:

| Del | Hva som står der |
| --- | --- |
| **Favoritter** | Sidene du har merket med **Legg til i favoritter**. Vises bare når du har noen. |
| **Arbeidsområde** | Sidene på øverste nivå som tilhører bedriften, med undersidene sine. |
| **Delt** | Sider andre har delt med deg ved navn, og som ikke ligger inni en side du allerede ser i Arbeidsområde. |
| **Privat** | Dine egne private sider, med undersidene sine. Bare du ser sidene i denne delen. |

En side på øverste nivå ligger i **Arbeidsområde** når den er åpen for alle i bedriften, og i
**Privat** når den bare er for deg. Når du deler en privat side med en person, havner den i
personens **Delt**. Ved siden av hver side åpner og lukker **Vis innholdet i …** og **Skjul
innholdet i …** undersidene.

Sidepanelet har også søkefeltet **Søk i Kunnskap** ([Søk](#søk)) og, under delene, **Papirkurv**
([Papirkurven](#papirkurven)).

## Opprett en side

Trykk **Ny side i Arbeidsområde** for å lage en side hele bedriften kan redigere, eller **Ny
privat side** for en som bare du ser. I dialogen **Ny artikkel** velger du **Hvor**
(Arbeidsområde eller Privat; dialogen sier *Alle i bedriften kan redigere den.* eller *Bare du ser
den, til du deler den.*), gir den en **Tittel** og trykker **Opprett**.

For å lage en side inne i en annen trykker du **+** (*Ny side i …*) ved siden av den i sidepanelet.
En underside starter med tilgangen til sin overordnede side ([Del en side](#del-en-side)). Sider
kan ligge høyst 10 nivåer dypt.

En side uten tittel vises som **Uten tittel**. Gi siden et **Ikon** med **Legg til ikon**; **Bytt
ikon** og **Fjern ikon** endrer det senere.

## Skriv på en side

Klikk i siden og skriv. Marker tekst, så kommer en liten meny med formateringen: **Fet**,
**Kursiv**, **Understreket**, **Gjennomstreket**, **Kode**, **Lenke**, overskriftene **Overskrift
1**, **Overskrift 2** og **Overskrift 3**, **Punktliste**, **Nummerert liste**, **Oppgaveliste** og
**Sitat**.

Markdown-snarveiene virker mens du skriver, i begynnelsen av en linje:

| Skriv | Du får |
| --- | --- |
| `#`, `##`, `###` og mellomrom | Overskrift 1, 2 eller 3 |
| `-` og mellomrom | En punktliste |
| `1.` og mellomrom | En nummerert liste |
| `[ ]` og mellomrom | En oppgaveliste: kryss av hver oppgave når den er gjort |
| `>` og mellomrom | Et sitat |
| tre bakover-apostrofer (backticks) | En kodeblokk |

En **Lenke** spør etter **Lenkeadresse**. Å klikke på en lenke i en side åpner en ekstern adresse
i en ny fane (**Åpne i ny fane**) og en adresse i Vantigo i samme fane (**Åpne i denne fanen**);
**Fjern lenke** tar den bort. En side kan være høyst 2 MiB.

## Lagring, og når noen andre har endret siden

Det finnes ingen Lagre-knapp. Siden lagrer seg selv et øyeblikk etter at du slutter å skrive, og
statusen ved siden av tittelen sier hvor det står: **Ulagrede endringer**, **Lagrer …**,
**Lagret** eller **Ikke lagret**. Under tittelen står *Sist endret av …*, med hvem som lagret sist
og når.

**Ikke lagret** betyr at lagringen mislyktes; teksten står fortsatt på skjermen, og Vantigo prøver
på nytt. To tilfeller forklares på siden: *Siden er for stor til å lagres* (over 2 MiB; gjør den
kortere) og *Siden kunne ikke lagres*, som nevner noe redigeringsverktøyet ikke støtter — angre den
siste endringen.

Hvis noen andre lagret siden mens du redigerte, sier en dialog **… endret siden mens du
redigerte**, med to valg:

- **Last inn på nytt** viser deres versjon og forkaster endringene dine.
- **Behold mine** lagrer teksten din over deres. Deres versjon går ikke tapt: den ligger i
  [historikken](#historikk-og-gjenoppretting).

Mister du forbindelsen eller lukker fanen før en endring er lagret, tas teksten vare på i denne
nettleseren. Neste gang du åpner siden, står det **Ulagrede endringer i denne nettleseren**, med
klokkeslettet, og du kan velge **Gjenopprett dem** eller **Forkast**.

## Organiser sider ved å dra

Dra en side i sidepanelet og slipp den:

- **på** en annen side, for å gjøre den til en underside av den (inni);
- **over** eller **under** en annen side, for å sette den før eller etter, på samme nivå.

En side kan ikke flyttes inn i en av sine egne undersider, og du må kunne redigere en side for å
flytte den. Når et flytt endrer hvem som kan lese siden — en side flyttes mellom **Privat** og
Arbeidsområde, eller under en side med annen tilgang — spør en bekreftelse **Endre hvem som kan
lese siden?** og sier at andre får eller mister tilgang til den og undersidene. Trykk **Endre
tilgang** for å fortsette.

## Del en side

Trykk **Del** øverst på siden. Dialogen **Del** viser hvem som har tilgang; bare de som kan
redigere siden, kan endre den.

**Alle i bedriften** settes til ett av:

- **Kan redigere** — alle med Kunnskap kan lese og redigere siden;
- **Kan lese** — alle kan lese den, bare personene som er nevnt under, kan redigere den;
- **Ingen tilgang** — bare personene som er nevnt under, ser den.

En underside kan i stedet settes til **Som den overordnede siden**, som følger den overordnede
siden og viser resultatet, for eksempel *Kan redigere, fra …*. En side på øverste nivå har alltid
sin egen innstilling.

En side med egen innstilling — en side på øverste nivå, eller en underside som ikke er satt til
**Som den overordnede siden** — beholder sin egen tilgang: *Denne siden har egen tilgang.
Personer lagt til på overordnede sider gjelder ikke her.* Den du legger til på en overordnet side,
kommer derfor ikke inn på denne siden eller sidene under den; legg dem til på selve siden. En side
satt til **Som den overordnede siden** følger den overordnede siden på begge måter.

Under **Personer** legger du til navngitte personer med **Legg til en person**: søk etter navn,
velg **Kan redigere** eller **Kan lese** for dem og trykk **Legg til**. Endre en persons nivå i
listen, eller fjern dem med **Fjern …**. En persons tilgang gjelder også undersidene som følger
den overordnede siden. Noen må alltid kunne redigere siden: du kan ikke fjerne eller senke den
siste som kan redigere den, med mindre alle har **Kan redigere**.

**Private sider er private også for administratorer.** **Administrere Kunnskap** viser ikke en
privat side, og en administrator kan verken lese eller gjenopprette den; bare du, og de du deler
den med, ser den.

## Lås en side

Åpne **Sidemeny** og velg **Lås siden** for å hindre at siden redigeres, for alle, til den låses
opp. En låst side viser *Siden er låst.*; de som kan redigere den, ser *Lås den opp fra sidemenyen
for å redigere.* og bruker **Lås opp siden**. Å låse endrer ingenting om hvem som kan lese siden.

## Skjul en side for assistenten

[Assistenten](/nb/user/assistant/) kan lese sidene du kan lese, for å svare på spørsmål. Slå på
**Skjult for assistenten** i **Sidemeny** for å holde en side utenfor rekkevidde. Det skjuler
siden **og alt inni den**: en underside viser **Skjult gjennom en overordnet side**.

Dette er ikke en tilgangsregel. De som har lov til å lese siden, leser den fortsatt; det hindrer
bare assistenten i å bruke den.

## Historikk og gjenoppretting

Hver lagring etterlater en versjon. Åpne **Historikk** fra **Sidemeny**: den lister versjonene
(*Lagret av …, når*), med **Gjeldende** øverst. Åpne en for å lese den; **Alle versjoner** går
tilbake til listen. **Gjenopprett denne versjonen** gjør den til sidens tekst igjen — versjonen du
erstatter, ligger også i historikken. Er siden endret i mellomtiden, sier dialogen det; åpne
historikken igjen og prøv på nytt.

## Papirkurven

**Flytt til papirkurven** i **Sidemeny** flytter siden, med undersidene, til **Papirkurv**, etter
en bekreftelse. En side i papirkurven tas vare på i 30 dager og slettes så for godt, med
historikken.

**Papirkurv** lister sidene med når de ble kastet og når de slettes for godt (**Slettes for godt**).
En side som ble kastet sammen med undersidene, viser *og 3 undersider*. **Gjenopprett** henter
siden tilbake, og en gruppe som ble kastet sammen, gjenopprettes sammen. Finnes den gamle
overordnede siden ikke lenger, havner siden øverst i sin del. Du ser sidene du kunne lese; med
**Administrere Kunnskap** ser og gjenoppretter du alle andres som ikke er private.

**Tøm papirkurven**, for administratorer med **Administrere Kunnskap**, sletter alt i papirkurven
for godt, med historikken. Det kan ikke angres.

## Favoritter

Trykk **Legg til i favoritter** ved siden av en side i sidepanelet for å legge den under
**Favoritter**, øverst; **Fjern fra favoritter** tar den bort. Favorittene er bare dine.

## Søk

Skriv i **Søk i Kunnskap** øverst i sidepanelet for å liste sidene som passer, under
**Søkeresultater**; *Ingen sider passer.* betyr at ingenting ble funnet. Søket leser titler og
tekst, og finner både norske og engelske ordformer — *rutine* finner også *rutinene*, *contract*
finner *contracts*. Det viser bare sider du har lov til å lese.

Søket i toppfeltet (Ctrl+K, eller Cmd+K på Mac) finner også Kunnskap-sider, ved siden av sidene i
de andre appene, og åpner dem direkte.

## Se hvem andre som er på siden

Når noen andre redigerer siden du har åpen, står det øverst på siden *Kari redigerer* (eller *Kari
og Ola redigerer*); andre som har siden åpen, vises under **Også her**, som redigerer eller ser
på. Siden oppdaterer seg også selv når noen andre endrer den, og sidepanelet når sider opprettes,
flyttes eller kastes — du trenger ikke laste inn på nytt. Er du midt i å skrive når noen andre
lagrer, får du dialogen som er beskrevet under [Lagring](#lagring-og-når-noen-andre-har-endret-siden).
