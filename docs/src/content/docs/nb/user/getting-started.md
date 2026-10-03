---
title: Kom i gang
description: Godta en invitasjon, logg inn, profilen og sikkerhetsinnstillingene dine, og logg ut.
sidebar:
  order: 1
sources:
  - apps/host/frontend
  - apps/server/internal/identity
---

Denne siden handler om å komme inn i Vantigo og ta vare på din egen konto. Appene,
sidemenyen og søket når du først er inne, er beskrevet i
[Finn fram i Vantigo](/nb/user/).

## Godta en invitasjon og opprett kontoen din

Den som administrerer arbeidsområdet, inviterer deg på e-post. Meldingen inneholder
en lenke til siden **Bli med i Vantigo**, som viser **Invitasjon for** adressen din
og ber om to ting:

- **Visningsnavn** — slik kollegene ser deg. Lar du feltet stå tomt, brukes navnet
  den som inviterte deg skrev inn, eller e-postadressen din om de ikke skrev noe.
- **Passord** — minst 12 tegn med en stor bokstav, en liten bokstav og et siffer.

Velg **Opprett konto**. Du blir logget inn med en gang og havner på kontrollpanelet;
krever arbeidsområdet tofaktorautentisering for rollen din, havner du i stedet på
sikkerhetsinnstillingene (se
[varselet når arbeidsområdet krever det](#varselet-når-arbeidsområdet-krever-det)).

En invitasjon gjelder én adresse og virker til den utløper — sju dager etter at den
ble sendt, med mindre installasjonen er satt opp annerledes. **Denne invitasjonen er
ugyldig eller utløpt** betyr at lenken er utløpt, tilbakekalt eller allerede brukt;
be den som inviterte deg om å sende den på nytt. En adresse som allerede har en
konto, kan ikke inviteres.

En administrator kan også opprette kontoen din med et passord de velger selv og gi
det til deg direkte. Bytt i så fall passord under [Sikkerhet](#bytt-passord) første
gang du logger inn.

## Logg inn

Åpne Vantigo, og siden **Velkommen tilbake** ber om **E-post** og **Passord**. Velg
**Logg inn**.

- **Logg inn med en passnøkkel** logger deg inn med en passnøkkel du har lagt til
  under Sikkerhet, uten passordet. Skriv inn e-postadressen først; knappen er
  deaktivert til du gjør det. Nettleseren eller enheten ber deg så bekrefte.
- **Fortsett med _leverandør_** vises når installasjonen er koblet til
  organisasjonens identitetsleverandør (single sign-on). Den sender deg til
  leverandøren, som sender deg tilbake innlogget. Kan ikke leverandøren fullføre
  innloggingen, sier siden fra, og du kan prøve igjen eller bruke en annen metode.
- **Glemt passordet?** fører til tilbakestillingen nedenfor.

Har kontoen din en autentiseringsapp aktivert, ber en side til, **Bekreft
innloggingen**, om en **Sikkerhetskode**: den sekssifrede koden fra appen, eller en
av gjenopprettingskodene dine. Velg **Bekreft**. **Bruk en annen innloggingsmetode**
tar deg tilbake til første side. Innlogging med passnøkkel teller som det andre
trinnet, så den ber aldri om kode.

Det femte feil passordet på rad låser kontoen i 15 minutter. Vent, og prøv så igjen;
en feil autentiseringskode teller på samme måte.

## Tilbakestill et glemt passord

1. Velg **Glemt passordet?** på innloggingssiden.
2. På **Tilbakestill passordet** skriver du inn **E-post** og velger **Send lenke for
   tilbakestilling**. Siden svarer **Sjekk innboksen for neste steg** enten en konto
   samsvarer eller ikke, så den røper aldri hvilke adresser som finnes.
3. Åpne lenken i e-posten. På **Velg et nytt passord** skriver du inn et **Nytt
   passord** som oppfyller kravene over, og velger **Tilbakestill passord**.
4. **Passordet er tilbakestilt. Du kan logge inn nå.** Gå tilbake til innloggingen.

**Denne lenken for tilbakestilling er ugyldig eller utløpt** betyr at lenken
allerede er brukt eller ble sendt for lenge siden; be om en ny. En administrator
kan også sende deg en e-post for tilbakestilling fra arbeidsområdets brukerliste.

Logger kontoen din inn gjennom organisasjonens identitetsleverandør, ligger
passordet der, ikke i Vantigo.

## Når økten din utløper

En økt avsluttes av seg selv etter en periode uten aktivitet, og uansett noen timer
etter at du logget inn; administratorers økter er kortere enn alle andres. Det neste
du gjør, viser da **Økten din har utløpt** med knappen **Logg inn igjen**. Ingenting
du allerede hadde lagret, går tapt.

Du ser den samme siden når en administrator deaktiverer kontoen din, endrer
opplysningene eller rollen din, eller logger deg ut overalt, siden hver av disse
avslutter øktene dine med en gang. De nøyaktige grensene settes av den som drifter
installasjonen; se øktlevetid og tilbakekalling i
[Identitet, autentisering og distribusjon](/nb/admin/authentication/).

## Rediger profilen din

Åpne avatar-menyen øverst til høyre og velg **Innstillinger**. Siden **Profil**
("Navn, bilde og språk.") inneholder:

- **Profilbilde** — **Velg et bilde** (JPEG eller PNG, opptil 5 MB) og **Last opp
  bilde**. **Fjern** tar det bort igjen. Bildet vises i avatar-menyen og der kolleger
  ser deg.
- **Navn** — visningsnavnet ditt.
- **E-post** — vises, men kan ikke endres her: "E-postadressen din administreres av
  innloggingsleverandøren." En eier endrer den fra arbeidsområdets brukerliste.
- **Foretrukket språk** — **Automatisk**, **Norsk** eller **Engelsk**.

Velg **Lagre profil**. Språkvalget endrer hele grensesnittet med en gang, og datoer,
tall og valuta følger det. **Automatisk** følger nettleserens språk. Valget lagres
med kontoen din, så det gjelder på alle enheter du logger inn fra.

## Bytt passord

Under **Innstillinger → Sikkerhet** ber kortet **Passord** om **Nåværende passord**,
et **Nytt passord** og **Bekreft nytt passord**; velg **Endre passord**. Det nye
passordet må ha minst 12 tegn, med en stor bokstav, en liten bokstav og et siffer,
og de to feltene må samsvare.

Sier kortet **Lokalt passord er ikke tilgjengelig**, logger kontoen din inn med
organisasjonens identitetsleverandør; administrer passord og sikkerhet der.
Autentiseringskontrollene på siden er utilgjengelige av samme grunn.

## Sett opp en autentiseringsapp

Tofaktorautentisering legger en kode fra en autentiseringsapp (for eksempel Google
Authenticator, Microsoft Authenticator eller 1Password) til hver innlogging med
passord.

1. Under **Innstillinger → Sikkerhet**, i kortet **Autentiseringsapp**, velger du
   **Konfigurer autentiseringsapp**, skriver inn passordet under **Nåværende passord
   for å starte oppsett** og velger **Start oppsett**.
2. En **Oppsetts-URI** vises. Legg til kontoen i autentiseringsappen med den: åpne
   den på telefonen, eller kopier den inn i appens valg for å legge til konto.
3. Skriv inn **Nåværende passord** en gang til sammen med den sekssifrede
   **Autentiseringskode**-n appen nå viser, og velg **Aktiver**.
4. **Lagre gjenopprettingskodene nå.** Kodene vises bare denne ene gangen. Hver av
   dem logger deg inn én gang om du mister telefonen, så oppbevar dem trygt og
   atskilt fra telefonen.

Kortet viser så **Aktivert**, med to handlinger:

- **Generer nye gjenopprettingskoder** ber om nåværende passord og en
  autentiseringskode, og viser så et nytt sett under **Lagre gjenopprettingskodene
  nå**. De gamle kodene slutter å virke.
- **Deaktiver** slår det andre trinnet av. Den ber om nåværende passord: "Dette er
  en sensitiv endring."

### Varselet når arbeidsområdet krever det

Installasjonen kan kreve tofaktorautentisering for administratorkontoer. Har du en
slik konto og ingen autentiseringsapp ennå, holder Vantigo deg på
sikkerhetsinnstillingene med et gult varsel, **Sett opp tofaktorautentisering for å
fortsette**, som lister trinnene over. Til du er ferdig med dem, sender alle andre
sider deg tilbake hit, og sidemenyen, app-velgeren og søket er skjult; avatar-menyen
virker fortsatt, så du kan logge ut. Tilgangen gjenopprettes i det øyeblikket du
velger **Aktiver** — du trenger ikke å logge inn på nytt.

## Legg til eller fjern en passnøkkel

En passnøkkel lar en enhet eller sikkerhetsnøkkel logge deg inn i stedet for
passordet, og den teller også som det andre trinnet ditt. Kortet **Passnøkler** under
**Innstillinger → Sikkerhet** lister dem du har ("Ingen passnøkler er registrert."
til du legger til en).

For å legge til en skriver du inn et **Navn på passnøkkel** som forteller deg hvilken
enhet det er (for eksempel "MacBook"), **Nåværende passord**, og velger **Legg til
passnøkkel**. Nettleseren eller enheten ber deg så bekrefte — med fingeravtrykket,
ansiktet, PIN-koden eller sikkerhetsnøkkelen den bruker. **Passnøkler støttes ikke på
denne enheten eller i denne nettleseren** betyr at denne nettleseren ikke kan
opprette en; prøv en annen.

For å fjerne en velger du **Fjern** ved siden av den. Dialogen **Fjern passnøkkel**
advarer om at det ikke kan angres, og ber om nåværende passord. En fjernet
passnøkkel kan ikke lenger logge deg inn; legg den til på nytt om du trenger den
igjen.

## Logg ut

Åpne avatar-menyen øverst til høyre og velg **Logg ut**. Du sendes tilbake til
innloggingssiden. Utlogging avslutter økten bare i denne nettleseren; andre
nettlesere eller enheter der du er logget inn, forblir innlogget til deres egne
økter utløper.

## Kontrollpanelet og app-velgeren

Etter innlogging havner du på kontrollpanelet — **Hjem** i app-velgeren øverst på
siden. Det hilser deg med navn og viser et kort for hver app du har tilgang til, med
nøkkeltallene for valgt datoperiode (**7d**, **30d**, **90d**, **12m** eller
**Tilpasset**), listen **Trenger oppfølging** med det som venter på deg, og
**Hurtighandlinger** for det du gjør oftest. Et nytt arbeidsområde viser også
**Fullfør oppsettet**, en kort liste over de første stegene (den første kunden, en
postkasse, et produkt, et prosjekt …) som forsvinner når alle er gjort.

App-velgeren lister alle appene du kan åpne; en app installasjonen ikke har slått
på, vises som **Ikke aktivert**. En systemadministrator havner på
**Systemadministrasjon** i stedet for kontrollpanelet. Hvordan appene, sidemenyen og
søket henger sammen, er beskrevet i [Finn fram i Vantigo](/nb/user/).
