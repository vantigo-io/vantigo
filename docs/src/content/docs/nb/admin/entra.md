---
title: Microsoft Entra ID
description: Å koble Vantigo til en Microsoft Entra ID-leietaker slik at kontoene Ansatte gir, lages der — appregistreringen, klienthemmeligheten, de åtte applikasjonstillatelsene og rollen User Administrator, administratorsamtykket, policyen for midlertidig tilgangspass, kontoreglene, innlogging og klargjøring; hva stegene for en som slutter gjør, og hva sletting mister; rotasjon av hemmeligheter; den merkede testen mot en ekte leietaker; og hva hver årsakskode betyr.
sidebar:
  order: 46
sources:
  - apps/server/internal/entra/module.go
  - apps/server/internal/entra/graph.go
  - apps/server/internal/entra/credentials.go
  - apps/server/internal/entra/status.go
  - apps/server/internal/entra/lifecycle_activate.go
  - apps/server/internal/entra/lifecycle_deactivate.go
  - apps/server/internal/entra/tenant_test.go
---

Microsoft Entra ID-modulen lager kontoene til personene Ansatte registrerer, i
selskapets Microsoft Entra ID-leietaker: opprettet deaktivert før starten, aktivert
første dag med et midlertidig tilgangspass, holdt i takt når personen flytter seg,
deaktivert dagen etter slutt, slettet senere eller aldri. Ansatte bestemmer; modulen
utfører det gjennom Microsoft Graph. Denne siden er driftssiden: hva leietakeren
trenger, de seks stegene i modulens veiviser, hva stegene for en som slutter gjør, og
hva du gjør når noe feiler. Hva modulen gjør og reglene den sjekker, står i
[referansen](/en/reference/entra/); skjermbildene i veiviseren står i
[brukerveiledningen](/nb/user/entra/).

## Før du begynner

- **En Microsoft Entra ID-leietaker**, og noen der som kan registrere en applikasjon,
  gi administratorsamtykke til Microsoft Graphs applikasjonstillatelser og tildele en
  katalogrolle — en Global Administrator eller en Privileged Role Administrator.
- **En eier** i Vantigo, innlogget med en andre faktor mens `OWNERS_REQUIRE_MFA` er på —
  uten den avvises også en eier med 403. Registreringen, hemmelighetene,
  tilkoblingstesten og de to siste stegene er bare en eiers; en person med
  `entra:manage` ser dem skrivebeskyttet.
- **Ansatte på.** Modulen er Ansattes leverandør og gjør ingenting uten den: `MODULES`
  avviser `entra` uten `staff` ved oppstart (`entra requires staff`).
- **Kortet Single sign-on**, under administrasjon av arbeidsområdet, for de to siste
  stegene: modulens veiviser bygger inn skjemaene for innlogging og klargjøring
  ([SSO- og SCIM-drift](/nb/admin/sso-scim/)).
- **Utgående https** fra hver prosess som kjører API-et eller bakgrunnsjobbene, til
  `login.microsoftonline.com` og `graph.microsoft.com`, uten proxy: modulen bruker
  ingen og følger ingen omdirigering.
- Migreringene lager skjemaet `entra` enten modulen er slått på eller ikke; en
  applikasjonsrolle med minste privilegium trenger tilgang til det som til alle andre
  ([Installasjon](/nb/admin/installation/#databaseroller)).

Ingenting ved modulen står i `vantigo.env`: det finnes ingen `ENTRA_*`-variabel.
Leietakeren, klienten, hemmelighetene og kontoreglene ligger i databasen, satt på
modulens side.

## Slå på modulen

1. La `MODULES` være usatt, eller legg til `entra` ved siden av `staff`.
2. Start Vantigo på nytt (`docker compose up -d`).
3. Verifiser: flisen **Microsoft Entra ID** står i appvelgeren for en eier, og
   `GET /api/v1/entra/connection` svarer 401 uten økt i stedet for 404-en en avslått
   moduls sti svarer.

Modulen har ingen bakgrunnsjobb selv: Ansattes kontoarbeider, `staff-accounts`, kaller
den ([Ansatte](/nb/admin/staff/#kontoarbeideren)).

## Koble til leietakeren

Åpne **Microsoft Entra ID** fra appvelgeren. Siden er veiviseren: seks steg, hvert et
kort med stien i portalen, hva du skal kopiere, og *Ferdig* når Vantigo har det det
trenger (*Valgfritt* på de to siste); et kort **Status** står over dem når tilkoblingen
er testet. Gå gjennom dem i rekkefølge, med portalen i en annen fane.

### 1. Registrer applikasjonen

1. I **Microsoft Entra admin center**, åpne **App registrations** › **New
   registration**.
2. Gi den et navn (for eksempel `Vantigo`), velg **Accounts in this organizational
   directory only**, og la **Redirect URI** stå tom inntil videre. Velg **Register**.
3. På registreringens **Overview**, kopier **Directory (tenant) ID** og
   **Application (client) ID**.
4. I steg 1 i veiviseren, lim dem inn i de to feltene og velg **Lagre**.

Én registrering kan tjene både Vantigos Graph-tilkobling og innloggingen (steg 5); du
kan bruke to om du vil.

### 2. Legg til en klienthemmelighet

1. I registreringen, åpne **Certificates & secrets** › **Client secrets** › **New
   client secret**. Gi den en beskrivelse og en utløpstid: høyst 24 måneder, og
   Microsoft anbefaler under 12.
2. Kopier hemmelighetens **Value** med en gang — ikke **Secret ID**. Portalen viser den
   bare nå.
3. I skjemaet **Legg til en hemmelighet** i steg 2, fyll ut **Beskrivelse**
   (beskrivelsen du ga den, valgfri), **Utløper** (dagen portalen viser — Vantigo tar
   starten av den dagen i nettleserens tidssone) og **Verdi**, og velg **Legg til
   hemmeligheten**. Vantigo forsegler verdien med en gang og viser den aldri igjen.

### 3. Gi tillatelsene og rollen

1. I registreringen, åpne **API permissions** › **Add a permission** › **Microsoft
   Graph** › **Application permissions**, og legg til disse åtte — veiviseren lister dem
   til kopiering:
   - `User.ReadWrite.All`
   - `User-LifeCycleInfo.ReadWrite.All`
   - `User.RevokeSessions.All`
   - `UserAuthMethod-TAP.ReadWrite.All`
   - `LicenseAssignment.ReadWrite.All`
   - `Group.ReadWrite.All`
   - `Domain.Read.All`
   - `Organization.Read.All`
2. Velg **Grant admin consent for** organisasjonen deres, og bekreft. Statusen til hver
   tillatelse blir *Granted*.
3. **Tildel rollen User Administrator** til applikasjonen. Åpne **Roles & admins** ›
   **User Administrator** › **Add assignments**, og velg applikasjonen etter navnet
   (tjenestekontohaveren, listet under bedriftsapplikasjoner). Uten rollen avviser Graph
   å aktivere, deaktivere eller slette en konto, eller å utstede et midlertidig
   tilgangspass, selv med tillatelsene — steget et halvferdig oppsett feiler på første
   dag til den første personen.
4. I steg 3, velg **Test tilkoblingen**. Vantigo henter et nytt token med
   hemmeligheten, leser organisasjonen og de verifiserte domenene, og sammenligner
   tokenets tillatelser med de åtte.

Verifiser: testen sier *Tilkoblet* organisasjonen, viser de verifiserte domenene og sier
*Alle tillatelsene modulen trenger, er gitt.*; hver tillatelse i listen er merket gitt.
En som mangler, navngis: legg den til, gi samtykke igjen og test på nytt. Testen kan
ikke se katalogrollen — Vantigo kan ikke lese den uten en ytterligere tillatelse — så
sjekk tildelingen selv under **Roles & admins**.

### 4. Sett kontoreglene

Kontoreglene, som en med `entra:manage` kan sette i tillegg til en eier:

- **Domenet** brukerhovednavnene lages under, valgt blant leietakerens verifiserte
  domener når testen har lest dem (skrevet inn før det). Skjemaet krever et; var det
  usatt, ville modulen brukt det første verifiserte domenet som ikke slutter på
  `.onmicrosoft.com`.
- **Mønsteret** for en person hvis e-post på jobb ikke kan være brukerhovednavnet —
  `given.family`, `g.family` eller `given`. En e-post på jobb på et verifisert domene
  brukes som den er.
- **Bruksstedet**, landet som skrives når Ansatte ikke har noe arbeidsland for
  personen, eller ett som ikke er en landkode med to bokstaver (`NO` om ikke endret).
  Microsoft nekter en bruker uten bruksted en lisens.
- **Stegene for en som slutter** — tilbakekall øktene, skriv sluttdatoen, fjern
  lisensene, og hvilke grupper personen skal forlate — og om ansettelsesdatoen skal
  skrives; hvert er på om du ikke slår det av ([hva de gjør](#hva-stegene-for-en-som-slutter-gjør)).
- **Det midlertidige tilgangspasset**: levetiden (10 minutter til 30 dager, 8 timer om
  ikke endret), om det virker én gang, og hvem som gir det videre. HR viser det én gang,
  på personens fane Konto i Ansatte; innstillingen for hvem som gir det videre lagres for
  en senere utgivelse og sender ennå ingenting til lederen.

Velg **Lagre kontoreglene**. Når tilkoblingen er tilkoblet, viser kortet også
**Leietakerens lisenser**, hver med setene som er tildelt og kjøpt — delenumrene en
Ansatte-gruppes lisenser kan peke på.

**Passet trenger leietakerens policy.** Vantigo kan lage et pass for hvilken som helst
bruker, men personen kan logge inn med det bare om leietakerens **policy for
autentiseringsmetoder** slår det på for dem: i **Microsoft Entra admin center**, åpne
**Authentication methods** › **Policies** › **Temporary Access Pass**, slå det på, og
rett det mot alle brukere eller en gruppe de nye personene er i. Policyen har sin egen
lengste levetid (åtte timer som standard): hold modulens levetid innenfor den. Uten
policyen avviser Graph passet, og personens fane Konto i Ansatte sier *Katalogen
tillater ikke et midlertidig tilgangspass for denne kontoen.*

### 5. Innlogging

Valgfritt, og en eiers. Veiviseren bygger inn leverandørskjemaet fra kortet **Single
sign-on** — når steg 1 er lagret — fylt ut med leietakerens autoritet
(`https://login.microsoftonline.com/<tenant id>/v2.0`) og klient-ID-en når skjemaet
ennå er tomt, slik at folk logger inn i Vantigo med Microsoft-kontoen sin:

1. I registreringen, åpne **Authentication** › **Add a platform** › **Web**, og lim inn
   **Tilbakekalls-URL**-en skjemaet viser.
2. Skjemaet trenger også en klienthemmelighet fra registreringen — en ny, siden portalen
   viser en verdi bare én gang — eller en tokenfil.
3. Fyll ut skjemaet og velg **Lagre**, slik [SSO- og
   SCIM-drift](/nb/admin/sso-scim/#microsoft-entra-id-klienthemmelighet) beskriver.

Uten dette lager Ansatte fortsatt Microsoft-kontoene, og ingen logger inn i Vantigo med
dem.

### 6. Klargjøring

Valgfritt, og en eiers. Veiviseren bygger inn kortets SCIM-del: velg **Nytt token**, og
i portalen,
åpne **Enterprise applications** › applikasjonen deres › **Provisioning**, sett
**Provisioning Mode** til **Automatic**, lim inn **Endepunkt-URL** som **Tenant URL** og
tokenet som **Secret Token**, test og lagre ([SCIM](/nb/admin/sso-scim/#scim)). Vantigo
godtar attributtene i virksomhetsutvidelsen som Entra tilordner som standard,
ansattnummeret blant dem
([virksomhetsutvidelsen](/nb/admin/sso-scim/#virksomhetsutvidelsen-og-ansattnummeret)).

Uten dette dukker personens Vantigo-bruker opp ved første innlogging i stedet for innen
Entras provisjoneringssyklus etter at kontoen er aktivert. Uansett blir brukeren som
kommer, tatt opp for Ansatte som kontoen Ansatte kunngjorde.

Steget tilbyr også **Åpne innstillingene til Ansatte**, der **Standard kontotype** kan
settes til **Microsoft Entra ID** for nye personer ([Kontoer](/nb/user/staff/#kontoer)).
Modulen setter den aldri selv.

### Verifiser tilkoblingen

- Kortet **Status** viser **Tilkoblet**, med organisasjonen, *Alle tillatelsene modulen
  trenger, er gitt.*, og hemmeligheten **I bruk** med utløpet dens.
- Ansatteinnstillinger, **Kontoer**, sier *Microsoft Entra ID: tilkoblet.*
- Gi én testperson kontotypen **Microsoft Entra ID** og kontoflagget, og velg **Kjør
  nå** på fanen Konto: kortet **Microsoft Entra ID** viser kontoen med brukerhovednavnet,
  og modulens siste hendelser viser `entra.user.prepared`. Hadde leietakeren allerede en
  bruker med det ansattnummeret, bekreft den først ([under](#kontoer-vantigo-ikke-opprettet)).

## Kontoer Vantigo ikke opprettet

Når Ansatte klargjør en person og leietakeren allerede har en bruker med personens
ansattnummer — laget for hånd, av et annet verktøy eller før Vantigo — kobler modulen
den brukeren, men **skriver ingenting til den før en med `entra:manage` bekrefter at
den er personens**: ingen opplysninger, ingen aktivering, ikke noe pass, ingen
deaktivering og ingen sletting. Ellers kunne alle som kan registrere en person i
Ansatte, ta over en annens konto ved å gi den nye personen kontoens ansattnummer.

Til da sier personens fane Konto i Ansatte *Katalogen har en konto under personens
ansattnummer som Vantigo ikke opprettet. Ansatte skriver ingenting til den før noen som
administrerer Microsoft Entra ID, bekrefter overtakelsen på siden for Microsoft Entra
ID.*, og dashbordet viser et varsel som lenker til siden.

Slik bekrefter du:

1. Åpne **Microsoft Entra ID**. I listen over modulens kontoer på kortet **Status**
   står brukeren som *Overtatt – bekreft*.
2. Sjekk i portalen at brukeren er personens — navnet, brukerhovednavnet, hvem som
   bruker den.
3. Velg **Bekreft** ved siden av den, og **Bekreft** igjen i *Bekrefte overtakelsen av
   …?*.

Vantigo forvalter så kontoen fra Ansattes neste kontokjøring: skriver personens
opplysninger, aktiverer den første dag og deaktiverer og sletter den når personen
slutter. En som har sluttet og hvis konto ventet på bekreftelsen, deaktiveres innen én
kjøring etter den. Bekreft aldri en konto du ikke har sjekket: å bekrefte gir den til
den som er registrert under det nummeret i Ansatte.

## Hva stegene for en som slutter gjør

Dagen etter at en persons siste arbeidsforhold slutter, får Ansatte modulen til å
deaktivere kontoen, uansett tilkoblingens status så lenge en hemmelighet virker — med
mindre kontoen er en Vantigo ikke opprettet og ingen har bekreftet. I denne
rekkefølgen, alle unntatt det første slått av eller på i steg 4:

| Steg | Hva som skjer i leietakeren |
| --- | --- |
| Deaktiver | Alltid, først: kontoen kan ikke lenger logge inn. Apper som støtter kontinuerlig tilgangsevaluering, reagerer innen minutter. |
| Tilbakekall øktene | Hvert oppdateringstoken og hver nettleserøkt slutter innen noen minutter; et tilgangstoken som allerede er utstedt, lever ut timen sin. |
| Skriv sluttdatoen | `employeeLeaveDateTime` settes til siste dag kl. 23.00 i forretningstidssonen — det en utløser for Lifecycle Workflows leser. Hoppes over når Ansatte ikke sender noen sluttdag. |
| Fjern lisensene | Hver lisens tildelt personen direkte fjernes, og plassen frigjøres. En lisens som kommer fra en gruppe, går med gruppen. |
| Forlat gruppene | **Alle**: hver gruppe unntatt dynamiske, rolletildelbare og rene e-postgrupper (distribusjonslister), også grupper Vantigo aldri laget. **Administrerte**: bare Vantigos egne grupper — ingen før gruppene kommer. **Ingen**: alle medlemskap blir stående. |

Kontoen deaktiveres først uansett hva annet som feiler, og et pass som ikke er vist
ennå, forkastes med en gang. Et steg som feiler, stopper stegene etter det og meldes
som `leaver_step_failed:` og stegets navn (`revoke_sessions`, `leave_date`,
`strip_licences`, `remove_groups`), som personens fane Konto i Ansatte sier som *Et
avslutningssteg i katalogen ble ikke fullført: å logge personen ut overalt.* (eller
steget det var). Ansatte deaktiverer kontoen igjen én gang om dagen så lenge koden
står, og modulen kjører stegene på nytt fra det første; koden forsvinner når alle
lykkes, og et steg som feiler igjen, venter til neste dag.

**Sletting** kommer senere, når Ansattes **Slett en ekstern konto (dager etter slutt)**
sier det, og aldri mens kontoen er aktivert eller personen har en slettesperre. Det er
en myk sletting: kontoen ligger i leietakerens **Deleted users** i 30 dager og kan
gjenopprettes der. **Etter de 30 dagene er den borte, med postboksen og OneDrive.** For
å beholde e-posten til en som har sluttet, gjør postboksen om til en delt postboks eller
overlever OneDrive i Microsoft 365 admin center før det — Vantigo gjør ingen av delene —
eller la innstillingen stå tom, som aldri sletter. En person som ansettes igjen etter en
sletting, får en ny konto.

## Roter en hemmelighet

1. I registreringen, legg til en ny klienthemmelighet, og kopier verdien.
2. I steg 2, fyll ut **Legg til en hemmelighet** med den og velg **Legg til
   hemmeligheten**. Den nyeste gyldige hemmeligheten prøves først, så den nye tar over
   med en gang; **I bruk** flytter seg til den.
3. Velg **Trekk tilbake** på den gamle, og slett den så i portalen. Vantigo nekter å
   trekke tilbake den eneste gyldige hemmeligheten mens modulen holder kontoer
   (`credential_in_use`), siden ingenting da kunne deaktivere en som slutter.

Tretti dager før den siste gyldige hemmeligheten som utløper, går ut, viser Ansattes
dashbord `secretExpiring`; når alle har utløpt, `secretExpired`, og tilkoblingen
feiler: Ansatte skriver da ingenting til leietakeren før en ny hemmelighet er lagt til —
ikke engang en deaktivering. En hemmelighet leietakeren avviser (`invalid_client`, for
eksempel en som er slettet i portalen), merkes som feilet — tabellen sier *Avvist av
leietakeren* og datoen — og prøves etter de andre til den får et token igjen. **Sist
brukt** er når en hemmelighet sist hentet et token.

## Bytt leietaker eller applikasjon

**Lagre** i steg 1 med en annen leietaker- eller klient-ID sender tilkoblingen tilbake
til *ikke tilkoblet* — kortet **Status** forsvinner, og Ansattes innstillinger sier
*Microsoft Entra ID: ikke tilkoblet ennå.* — til den er testet igjen; de verifiserte
domenene og organisasjonen glemmes med den. Et bytte av leietaker forkaster også alle
midlertidige tilgangspass som ikke er vist ennå; et bytte av bare applikasjonen beholder
dem. De samme ID-ene igjen endrer ingenting. Mens modulen holder kontoer, spør Vantigo
først, **Bytte leietaker eller applikasjon?** (`connection_in_use`), og **Bytt**
lagrer: kontoene ble laget i den gamle leietakeren, modulen vil lete etter folk i den
nye, og **den gamle leietakerens kontoer blir ikke lenger deaktivert eller slettet av
Vantigo** når personene slutter — ta deg av dem i den gamle leietakeren selv. Ingenting
slettes i noen av leietakerne.

## Den merkede testen

For en bidragsyter: modulens tester kjører mot en falsk Graph, og én merket test kjører
det ekte mot en egen leietaker, for å avgjøre det den falske ikke kan vite — om en
applikasjon kan skrive `employeeHireDate`, om passet utstedes med tillatelsen sin, og
hva modulen svarer for en bruker synkronisert fra lokal katalog.

**Leietakeren trenger:**

- En egen leietaker — Entra ID Free dekker brukere og grupper gjennom Graph. (Om
  sandkassen i Microsoft 365 Developer Program fortsatt er tilgjengelig uten et Visual
  Studio-abonnement, er usikkert; en prøveleietaker eller en med betaling per bruk
  holder.)
- En appregistrering med de åtte applikasjonstillatelsene, med administratorsamtykke,
  og **rollen User Administrator tildelt tjenestekontohaveren** (steg 1 til 3).
- Midlertidig tilgangspass slått på i policyen for autentiseringsmetoder for alle
  brukere.
- En klienthemmelighet, leietaker-ID-en, klient-ID-en og et verifisert domene
  (`<navn>.onmicrosoft.com` holder).
- For det tredje faktumet, en bruker synkronisert fra lokal katalog: objekt-ID-en og
  ansattnummeret (modulen finner en bruker etter ansattnummeret).

**Kjør den** fra `apps/server`:

```sh
ENTRA_TEST_TENANT_ID=<tenant id> \
ENTRA_TEST_CLIENT_ID=<client id> \
ENTRA_TEST_CLIENT_SECRET=<secret value> \
ENTRA_TEST_DOMAIN=<verified domain> \
go test -tags entra -run TestEntraTenant -v ./internal/entra/
```

`ENTRA_TEST_SYNCED_USER_ID` og `ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER` legger sammen til
sjekken av den synkroniserte brukeren, som ikke skriver noe til den brukeren. Uten de
fire variablene hoppes testen over, med navnet på den som mangler. Den tester
tilkoblingen, setter kontoreglene, og klargjør så én bruker — ansattnummer `VT-` og seks
tilfeldige tegn, fornavn `Vantigo`, etternavn `Test` og de samme tegnene, så
brukerhovednavnet begynner med `vantigo.test` — aktiverer den med ansettelsesdatoen og
et pass, viser passet én gang, oppdaterer den, deaktiverer den med sluttdatoen og
sletter den mykt. Den logger hva Graph svarte for de tre faktaene (`UNCERTAIN FACT 1`
til `3`) og modulens hendelser, aldri hemmeligheten eller et pass. Feiler et steg først,
deaktiverer og sletter oppryddingen brukeren den laget; en slettet bruker kan
gjenopprettes under **Deleted users** i 30 dager. Grupper testes ikke før de kommer. Den
går gjennom den samme bevoktede transporten som produksjon. Hold hemmeligheten utenfor
skallhistorikken.

## Hva som kan gå galt

Kortet **Status** navngir årsaken; Ansattes innstillinger og dashbord sier det samme med
Ansattes ord.

| Årsak | Betyr | Gjør |
| --- | --- | --- |
| `credential_invalid` | Ingen hemmelighet fikk et token: verdien ble skrevet feil, hemmeligheten ble slettet i portalen, klient-ID-en er feil, eller `APP_SECRET` er endret så de lagrede hemmelighetene ikke kan åpnes. | Legg til riktig hemmelighet (steg 2), sjekk ID-ene (steg 1), og test igjen. |
| `tenant_unreachable` | Leietakeren svarte ikke. | Sjekk utgående https til `login.microsoftonline.com` og `graph.microsoft.com` fra hver prosess, og test igjen. |
| `consent_missing` | Tokenet bærer ingen tillatelse — administratorsamtykke ble aldri gitt, eller ble trukket tilbake — eller Graph nektet å lese organisasjonen. | **Grant admin consent** (steg 3) og test igjen. |
| `secret_expired` | Alle hemmelighetene har utløpt. | Legg til en ny (steg 2). |
| `secret_expiring` | Den siste gyldige hemmeligheten som utløper, går ut innen 30 dager. | [Roter](#roter-en-hemmelighet). |
| `role_missing` | Tre opprettelser, aktiveringer, deaktiveringer, telefonendringer eller slettinger på rad ble avvist, uten at noen har lyktes siden: rollen User Administrator er mest sannsynlig ikke tildelt. | Tildel rollen (steg 3), og **Kjør nå** på en av personene som står fast; et vellykket steg fjerner årsaken. |
| Tillatelser mangler | Testens token mangler én eller flere av de åtte. | Legg dem til, gi samtykke igjen, test igjen. |

På en persons fane Konto i Ansatte sier kortet **Microsoft Entra ID** hva leietakeren
meldte for dem:

| På fanen | Betyr | Gjør |
| --- | --- | --- |
| To kontoer med ansattnummeret | To brukere i leietakeren har det som ansatt-ID. | Fjern det fra den gale i portalen. |
| E-posten på jobb er allerede en annen kontos | En annen bruker har den som brukerhovednavn. | Endre personens e-post på jobb i Ansatte, eller rydd opp i den andre kontoen. |
| Synkronisert fra en lokal katalog | Brukeren styres av Active Directory, som Graph ikke vil skrive. | Administrer kontoen lokalt; hybride leietakere støttes ikke. |
| En administrativ rolle i Entra | Brukeren har en rolle (eller er i en rolletildelbar gruppe) applikasjonen ikke får røre — eller applikasjonen mangler rollen. | En administrator håndterer kontoen for hånd; se `role_missing` over. |
| Ansettelsesdatoen ikke skrevet | Graph avviste `employeeHireDate`. | Ingenting trengs for innloggingen; slå av skrivingen av ansettelsesdatoen om det fortsetter å feile. |
| Ingen lisens igjen (lisensen navngitt) | Ingen plass var ledig. | Kjøp plasser eller frigjør én, og **Kjør nå**. |
| Et pass ikke tillatt | Leietakerens policy for autentiseringsmetoder slår ikke på passet for personen; kontoen er aktivert uten. | [Slå på policyen](#4-sett-kontoreglene). Vantigo utsteder et pass bare i kallet som aktiverer en konto, så for denne personen lager du det i portalen (brukerens **Authentication methods**). |
| *Katalogen avviste det midlertidige tilgangspasset; kontoen er aktivert uten et.* | Leietakeren avviste passet med 403 (`pass_forbidden`) — applikasjonen mangler rollen eller tillatelsen for passet, eller brukeren er en den ikke får røre. | Sjekk rollen User Administrator og `UserAuthMethod-TAP.ReadWrite.All`; lag passet i portalen for denne personen. |
| *Katalogen har en konto under personens ansattnummer som Vantigo ikke opprettet …* | `external_unconfirmed`: modulen fant brukeren og skriver ingenting til den. | [Bekreft den](#kontoer-vantigo-ikke-opprettet) når du har sjekket at den er personens; er den ikke det, rett ansattnummeret der det er feil. |
| Lederens konto ikke funnet | Lederen har ingen konto i leietakeren. | Gi lederen kontotypen Microsoft Entra ID, eller godta det. |
| *Et avslutningssteg i katalogen ble ikke fullført: …* | Et steg for en som slutter feilet etter deaktiveringen, og stegene etter det ble ikke prøvd; kontoen er deaktivert. | Vanligvis ingenting: Ansatte prøver igjen neste dag. Feiler det fortsatt, se modulens siste hendelser og loggen, og gjør steget i portalen. |
