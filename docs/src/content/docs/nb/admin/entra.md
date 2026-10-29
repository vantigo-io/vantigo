---
title: Microsoft Entra ID
description: Å koble Vantigo til en Microsoft Entra ID-leietaker slik at kontoene Ansatte gir, lages der — appregistreringen, klienthemmeligheten, de åtte applikasjonstillatelsene og rollen User Administrator, administratorsamtykket, policyen for midlertidig tilgangspass, kontoreglene, innlogging og klargjøring; Outlook-tilkoblingen — omdirigerings-URI-en på APP_URL, de delegerte tillatelsene og administratorsamtykket, bryteren, å verifisere ved å koble til selv, å slå den av, hva som skjer med en persons tilkobling og entra-outlook-sweep, og hva som kan gå galt; varselet om tillatelser Vantigo ikke bruker; kalendersynkronisering — Calendars.ReadWrite, samtykket til den, og å begrense den til enkelte postbokser med Exchange RBAC for Applications; hva stegene for en som slutter gjør, og hva sletting mister; Ansattes grupper i leietakeren, applikasjonen som eier, å bekrefte en gruppe Vantigo ikke opprettet og hva en arkivert gruppe blir til; rotasjon av hemmeligheter; den merkede testen mot en ekte leietaker; og hva hver årsakskode betyr.
sidebar:
  order: 46
sources:
  - apps/server/internal/entra/module.go
  - apps/server/internal/entra/graph.go
  - apps/server/internal/entra/credentials.go
  - apps/server/internal/entra/status.go
  - apps/server/internal/entra/lifecycle_activate.go
  - apps/server/internal/entra/lifecycle_deactivate.go
  - apps/server/internal/entra/groups.go
  - apps/server/internal/entra/tenant_test.go
  - apps/server/internal/entra/mailbox.go
  - apps/server/internal/entra/mailbox_writeback.go
  - apps/server/internal/entra/mailbox_notifications.go
  - apps/server/internal/entra/mailbox_notifications_endpoint.go
  - apps/server/internal/entra/outlook.go
  - apps/server/internal/entra/outlook_sweep.go
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

Ansattes kontoarbeider, `staff-accounts`, kaller modulen
([Ansatte](/nb/admin/staff/#kontoarbeideren)). Modulens egen ene arbeider,
`entra-outlook-sweep`, rydder i folks [Outlook-tilkoblinger](#outlook-tilkobling) hver
time; den har ingen innstilling.

## Koble til leietakeren

Åpne **Microsoft Entra ID** fra appvelgeren. Siden er veiviseren: seks steg, hvert et
kort med stien i portalen, hva du skal kopiere, og *Ferdig* når Vantigo har det det
trenger (*Valgfritt* på de to siste); et kort **Status** står over dem når tilkoblingen
er testet. Gå gjennom dem i rekkefølge, med portalen i en annen fane.

### 1. Registrer applikasjonen

1. I **Microsoft Entra admin center**, åpne **App registrations** › **New
   registration**.
2. Gi den et navn (for eksempel `Vantigo`), velg **Accounts in this organizational
   directory only**, og la **Redirect URI** stå tom inntil videre —
   [Outlook-tilkoblingen](#outlook-tilkobling) legger til en senere. Velg **Register**.
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

   En niende, `Calendars.ReadWrite`, trengs bare for
   [kalendersynkronisering](#kalendersynkronisering), og veiviseren lister den mens
   bryteren [Outlook-tilkobling](#outlook-tilkobling) er på.
2. Velg **Grant admin consent for** organisasjonen deres, og bekreft. Statusen til hver
   tillatelse blir *Granted*. `User.ReadWrite.All` dekker også personens bilde, som
   Vantigo skriver til kontoen: ingen ytterligere tillatelse eller samtykke.
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

**Tillatelser Vantigo ikke bruker.** Når tokenet har applikasjonstillatelser utover de
åtte og `Calendars.ReadWrite`, viser kortet **Status** et oransje varsel, *Applikasjonen
har tillatelser Vantigo ikke bruker:*, med listen. En applikasjonstillatelse når hver
postboks eller konto i leietakeren, så en ingen trenger, er best fjernet: med mindre et
annet produkt bruker denne registreringen, fjern den under **API permissions** og test
igjen. Det er bare et varsel — tilkoblingen forblir **Tilkoblet** — og testen
registrerer hver som *en tillatelse Vantigo ikke bruker* i de siste hendelsene. I denne
versjonen er e-posttillatelsene (`Mail.*`) og `full_access_as_app` blant dem.

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

## Outlook-tilkobling

**Outlook-tilkobling** lar hver person koble sin egen Outlook til Vantigo én gang, under
**Innstillinger → Tilkoblinger**, ved å logge på Microsoft — som regel uten noen side,
når enkel pålogging svarer ([brukerveiledningen](/nb/user/connections/)). Vantigo holder
så personens delegerte pålogging til postboksen, forseglet, og viser *Koble til Outlook
på nytt* bare til den personen når Microsoft avviser den. Bare en person som
Ansatte-oppføringen knytter til en konto Vantigo forvalter, kan koble til, og bare som
den kontoen. **I denne versjonen bruker ingenting tilkoblingen ennå**: kalenderens
synkronisering kjører fortsatt på applikasjonstillatelsen
([kalendersynkronisering](#kalendersynkronisering)), og eierens bryter er den begge kjører
bak. Hvordan det virker, står i [referansen](/en/reference/entra/#the-outlook-connection).

### Det Outlook-tilkoblingen trenger

- Tilkoblingen er satt opp og **Tilkoblet** ([over](#koble-til-leietakeren)).
- De som skal koble til, har kontotypen **Microsoft Entra ID** i Ansatte, og kontoen
  deres er en Vantigo opprettet, eller som er bekreftet adoptert
  ([under](#kontoer-vantigo-ikke-opprettet)).
- `APP_URL`, med `APP_BASE_PATH` når Vantigo serveres under en sti, er adressen folk når
  Vantigo på: omdirigerings-URI-en lages av dem, aldri av `PUBLIC_BASE_URL`
  ([Installasjon](/nb/admin/installation/#basesti-og-omvendt-proxy)). Endrer du en av dem
  senere, endres URI-en med.

### Slå på Outlook-tilkoblingen

1. På modulsiden, i steg **4 Sett kontoreglene**, under **Outlook-tilkobling**, kopierer
   du **Omdirigerings-URI** — `APP_URL`, basestien og `/api/v1/entra/outlook/callback`,
   for eksempel `https://vantigo.example.com/api/v1/entra/outlook/callback`.
2. I registreringen, åpne **Authentication**. Under **Web**-plattformen — den
   innloggingen i steg 5 la til, eller **Add a platform** › **Web** hvis det ikke finnes
   noen — legger du til URI-en som omdirigerings-URI, og velger **Save**. Det må være en
   **Web**-plattform, ikke en enkeltsideapplikasjon: Vantigo løser inn koden på serveren
   med klienthemmeligheten sin.
3. Åpne **API permissions** › **Add a permission** › **Microsoft Graph** › **Delegated
   permissions**, og legg til `openid`, `profile`, `offline_access` og
   `Calendars.ReadWrite`. Dette er de delegerte tillatelsene, ved siden av
   applikasjonstillatelsene fra steg 3.
4. Velg **Grant admin consent for** organisasjonen deres, og bekreft, så ingen blir bedt
   om samtykke — eller avvist fordi godkjenning mangler — når de kobler til.
5. Tilbake på modulsiden slår du på **Outlook-tilkobling: la folk koble Outlook til
   Vantigo** og velger **Lagre kontoreglene** (`entra:manage`). Steg 3 lister nå
   `Calendars.ReadWrite` som den niende applikasjonstillatelsen, for
   kalendersynkronisering ([under](#kalendersynkronisering)).

### Verifiser Outlook-tilkoblingen

Koble til selv: åpne **Innstillinger → Tilkoblinger** og velg **Koble til Outlook**. Du
er tilbake på siden i løpet av sekunder, og den sier **Tilkoblet som** og kontoen din. På
modulsiden har linjen under bryteren gått fra **Ingen har koblet til ennå: …** til
**Klar**, *Personer tilkoblet:* teller deg, og de siste hendelsene på kortet **Status**
viser *En person koblet til Outlook*. Så lenge den siste testens token mangler
applikasjonstillatelsen `Calendars.ReadWrite`, sier linjen i stedet
*Kalendersynkroniseringen trenger applikasjonstillatelsen Calendars.ReadWrite, …*, før
og etter — [kalendersynkronisering](#slå-den-på) sier hvorfor det bare er til
orientering.

### Slå av Outlook-tilkoblingen

Slå av bryteren og velg **Lagre kontoreglene**. Er noen tilkoblet eller venter på ny
tilkobling, spør siden først — *Alle må koble til Outlook på nytt.* — og **Slå av og
lagre** lagrer. Hver persons tilkobling lukkes i den samme lagringen, hver registrert som
*En Outlook-tilkobling ble lukket* (*Outlook-tilkoblingen ble slått av*);
kalendersynkroniseringen stopper for alle, og kopiene fjernes. Slås den på igjen, kobler
hver person til på nytt.

### Hva som skjer med en persons tilkobling

- **Microsoft avviser den.** Et tilbakestilt passord, tilbakekalte økter, en policy for
  betinget tilgang, et nytt krav om flerfaktorautentisering eller en pålogging som har
  utløpt, setter tilkoblingen i *Må kobles til på nytt*, med årsaken, på den personens
  side; *Venter på ny tilkobling:* teller dem, og hendelsen sier *Microsoft avviste en
  persons Outlook-tilkobling*. Vantigo oppgir klientegenskapen `cp1`, så Microsoft sier
  fra med en gang når en konto deaktiveres eller øktene tilbakekalles. Ingenting annet
  stopper.
- **De slutter.** Når Ansatte deaktiverer kontoen, lukkes tilkoblingen og det lagrede
  tokenet glemmes i det samme steget, før de andre stegene for en som slutter kjører.
- **Kontoen deres slettes eller deaktiveres** i Vantigo eller i leietakeren.
  Tilkoblingen lukkes, og det lagrede tokenet glemmes, neste gang den brukes, eller — for en som venter på ny tilkobling
  — når arbeideren `entra-outlook-sweep` leser kontoen i leietakeren, høyst én gang i
  døgnet.
- **De kobler fra.** Vantigo lukker tilkoblingen; den logger dem aldri ut av Microsoft.

En lukket tilkobling slettes av `entra-outlook-sweep`, modulens arbeider, én gang i
timen: sju dager etter at den ble lukket, eller når det har gått et døgn både siden den
sist ble brukt og siden den ble lukket, og det ikke er noe igjen for den å rydde. Arbeideren har ingen innstilling og kjører der
modulens arbeidere kjører
([Installasjon](/nb/admin/installation/#bakgrunnsjobber-og-skalering)).

### Når tilkoblingen feiler

| Hvor | Betyr | Gjør |
| --- | --- | --- |
| Microsofts side viser `AADSTS50011` (omdirigerings-URI-en stemmer ikke) | Registreringens **Web**-plattform mangler omdirigerings-URI-en nøyaktig slik Vantigo viser den — aldri lagt til, en skrivefeil, en plattform for enkeltsideapplikasjoner, eller `APP_URL` eller `APP_BASE_PATH` er endret siden. | Kopier URI-en fra modulsiden på nytt og legg den til **Web**-plattformen. |
| Microsoft ber personen om samtykke, eller sier *Approval required* / *Need admin approval* | Administratorsamtykke er ikke gitt for de delegerte tillatelsene. | **Grant admin consent** ([over](#slå-på-outlook-tilkoblingen)) og la dem koble til på nytt. |
| *Den siste tilkoblingen manglet en delegert tillatelse: …* under bryteren; personen ser *Organisasjonen har ikke gitt samtykke til tillatelsene tilkoblingen trenger. …* | Microsoft godtok påloggingen uten `Calendars.ReadWrite` (delegert). Ingenting ble lagret for personen. | Legg til den delegerte tillatelsen, gi administratorsamtykke og koble til på nytt; linjen forsvinner ved første tilkobling som fullføres. |
| Folk i *Må kobles til på nytt* etter en endring i betinget tilgang eller flerfaktorautentisering | Som ventet: Microsoft vil ha en ny pålogging. *Stoppet* betyr at en policy avviste den. | De velger **Koble til Outlook på nytt**. Når betinget tilgang stoppet den, sjekk policyens betingelser (enhet, sted, risiko) for Vantigos applikasjon. |
| Personen ser *Du logget på Microsoft med en annen konto enn den Vantigo kjenner deg ved. …* | De logget på som en annen konto enn den Ansatte knytter til personen deres (`account_mismatch`). | De kobler til på nytt som den kontoen. Knytter Ansatte feil konto, retter HR det på personens fane Konto. |
| *En annen person i Vantigo har allerede koblet til denne Microsoft-kontoen.* | En annen bruker har en levende tilkobling til den kontoen (`account_taken`). | Finn hvem i de siste hendelsene (*En person koblet til Outlook*, med kontoen); den personen kobler fra, eller HR retter kontokoblingen. |
| *Ingenting å koble til: …* på en persons side | Bryteren er av, eller personen har ingen konto Vantigo forvalter. | Slå på bryteren, eller gi personen kontotypen Microsoft Entra ID og bekreft kontoen. |
| *Tilkoblingen tok for lang tid, eller ble startet i en annen økt.* | Mer enn ti minutter hos Microsoft, eller nettleseren mistet informasjonskapselen for tilkoblingen — en annen nettleser, et privat vindu som ble lukket, eller informasjonskapsler blokkert for Vantigo — eller en nyere tilkobling ble startet før denne var fullført, for eksempel i en annen fane, og den avbryter denne. | Koble til på nytt i den samme nettleseren. |

## Kalendersynkronisering

**Kalendersynkronisering** lar folk ha en kopi av Vantigo-kalenderen sin i Outlook
([brukerveiledningen](/nb/user/calendar/#synkroniser-med-outlook)): kalenderens arbeider
`calendar-mailbox-sync` skriver hver persons avtaler, og det de velger fra de andre
appene, til Exchange Online-postboksen deres gjennom denne modulens tilkobling, og leser
etter hver synkronisering **standardkalenderen** deres tilbake — tidene for hendelsene
som gjør dem opptatt, aldri titlene — for å vise den opptatte tiden i Vantigo. Før hver
skriving leser den også Vantigos **egne kopier** — og, for en person som har valgt en egen
*Vantigo*-kalender, hver hendelse i den kalenderen — i sin helhet, for å ta med tilbake
endringene, flyttingene og slettingene personen gjorde i Outlook på sine egne avtaler
([brukerveiledningen](/nb/user/calendar/#endre-avtalene-dine-i-outlook)); personens egne
hendelser leser den aldri i sin helhet. `Calendars.ReadWrite` dekker begge lesingene:
det er ingenting å legge til eller samtykke til. Den er av som standard, og
klargjøringen virker likt med den av eller på. Den kjører bak bryteren
[Outlook-tilkobling](#outlook-tilkobling), på applikasjonstillatelsen: i denne versjonen
bruker den ikke folks egne tilkoblinger. Hva den skriver
og leser, og hvordan, står i [referansen](/en/reference/entra/#the-mailbox-calendar).

### Forutsetninger

- Tilkoblingen er satt opp og **Tilkoblet** ([over](#koble-til-leietakeren)), og
  Kalender-modulen er slått på ([Kalender](/nb/admin/calendar/)).
- De som skal synkronisere, har kontotypen **Microsoft Entra ID** i Ansatte, og kontoen
  deres er en Vantigo opprettet, eller som er bekreftet adoptert
  ([under](#kontoer-vantigo-ikke-opprettet)): Vantigo skriver bare til en postboks den
  vet er personens, aldri en som er funnet ut fra adressen.
- Kontoene deres har en Exchange Online-postboks (en lisens som omfatter Exchange
  Online).

### Slå den på

1. På modulsiden, i steg **4 Sett kontoreglene**, slår du på **Outlook-tilkobling: la
   folk koble Outlook til Vantigo** under **Outlook-tilkobling**, og velger **Lagre
   kontoreglene** (`entra:manage`) — [Outlook-tilkoblingen](#slå-på-outlook-tilkoblingen)
   har det den ellers trenger. Steg 3 lister nå `Calendars.ReadWrite` som den niende
   tillatelsen.
2. I registreringen, åpne **API permissions** › **Add a permission** › **Microsoft
   Graph** › **Application permissions**, og legg til `Calendars.ReadWrite`.
3. Velg **Grant admin consent for** organisasjonen deres, og bekreft.
4. I steg 3 velger en eier **Test tilkoblingen**.

Verifiser: kortet **Status** viser ikke lenger `Calendars.ReadWrite` som manglende. En
person som åpner **Kalender → Min kalender**, kan slå på **Synkroniser til
Outlook-kalenderen min**; innen et minutt sier kortet deres **Sist synkronisert**, og
avtalene står i Outlook. Til en test ser tillatelsen, viser kortet **Status** den som
manglende — men det er bare til orientering, og steg 3 venter ikke på den: folk kan slå
på synkroniseringen sin likevel, og en postboks tillatelsen ikke når, avvises bare for
den personen. I mellomtiden sier linjen under bryteren *Kalendersynkroniseringen
trenger applikasjonstillatelsen Calendars.ReadWrite, som ikke er i tokenet. …* i stedet
for [Outlook-tilkoblingens](#verifiser-outlook-tilkoblingen) egen, like mye bare til
orientering. Resten av modulen virker som før.

**Hva en endring i Outlook gjør.** En persons endring eller flytting av kopien av sin
egen avtale endrer avtalen i Vantigo ved neste synkronisering, med de samme reglene og
e-postene som en endring gjort i Vantigo. **Sletter personen kopien i Outlook — eller
flytter den til en annen kalender eller mappe — slettes avtalen i Vantigo**, eller den
avlyses, med en avlysning på e-post til folk utenfor bedriften, når de er invitert; for
én forekomst av en gjentakende avtale avlyses den forekomsten. Mer enn ti slike
slettinger av én persons avtaler i én synkronisering tas ingen av med — kopiene legges
tilbake — så en kalender som tømmes ved en feil, tømmer ikke Vantigo. En endring i noe annet —
en avtale personen deltar i, et element fra en annen app — legges tilbake, og en kopi
personen legger til deltakere på, overlates til dem. Vantigo tar med høyst 20 slike
endringer per person per synkronisering; resten følger ved neste.

**Slår du av bryteren**, stopper synkroniseringen for alle, og arbeideren fjerner kopiene den
skrev, med tilkoblingen så lenge en hemmelighet virker; den lukker også hver persons
[Outlook-tilkobling](#slå-av-outlook-tilkoblingen). Slår du den på igjen, fortsetter
den for alle som har sin egen bryter på.

### Begrens den til enkelte postbokser

`Calendars.ReadWrite` som applikasjonstillatelse når **hver postboks i leietakeren**.
Vantigo skriver bare til og leser bare postboksene til kontoene den forvalter, for dem
som slår på synkroniseringen, men legitimasjonen kan nå flere. Et omfang begrenser
lesingen akkurat som det begrenser skrivingen: en postboks utenfor det avvises for
begge. For å begrense hva selve
legitimasjonen når, bruk Exchange Onlines **RBAC for Applications**, i Exchange Online
PowerShell, som Exchange-administrator:

1. Lag en e-postaktivert sikkerhetsgruppe (eller en Microsoft 365-gruppe) med de
   personene som kan få kalenderen sin synkronisert. Nestede medlemmer teller ikke.
2. Koble til: `Connect-ExchangeOnline`.
3. Registrer applikasjonens tjenestekontohaver i Exchange, med **Application (client)
   ID** og **Object ID** til bedriftsapplikasjonen (ikke til appregistreringen):

   ```powershell
   New-ServicePrincipal -AppId <klient-ID> -ObjectId <bedriftsapplikasjonens objekt-ID> -DisplayName "Vantigo"
   ```

4. Lag et omfang av gruppens medlemmer, med gruppens unike navn
   (`Get-Group <gruppe> | Select DistinguishedName`):

   ```powershell
   New-ManagementScope -Name "Vantigo calendar sync" -RecipientRestrictionFilter "MemberOfGroup -eq '<gruppens unike navn>'"
   ```

5. Gi kalenderrollen i dette omfanget:

   ```powershell
   New-ManagementRoleAssignment -App <klient-ID> -Role "Application Calendars.ReadWrite" -CustomResourceScope "Vantigo calendar sync"
   ```

6. Sjekk én postboks innenfor omfanget og én utenfor:

   ```powershell
   Test-ServicePrincipalAuthorization -Identity <klient-ID> -Resource <postboksadresse>
   ```

7. **Fjern den leietakeromfattende tildelingen i Entra**: i registreringens **API
   permissions**, fjern `Calendars.ReadWrite` (eller trekk tilbake
   administratorsamtykket). Exchange legger de to tildelingene sammen, så mens samtykket
   for hele leietakeren står, begrenser omfanget ingenting.

Endringene tar **30 minutter til 2 timer** å slå gjennom. En tildeling gjort med RBAC for
Applications vises aldri i tokenet, så når samtykket for hele leietakeren er fjernet,
viser neste **Test tilkoblingen** `Calendars.ReadWrite` som manglende (steg 3 står fortsatt som
*Ferdig*), og linjen under bryteren sier *Kalendersynkroniseringen trenger
applikasjonstillatelsen Calendars.ReadWrite, …* — det er som ventet, og synkroniseringen fortsetter. Verifiser med en person innenfor omfanget: kortet deres
sier **Sist synkronisert**. En person utenfor omfanget ser **Organisasjonen din har ikke
gitt Vantigo tilgang til postboksen din.** på Outlook-kortet sitt: Vantigo prøver dem
igjen hver sjette time, og leser det aldri som at tilkoblingen feiler.

### Endringsvarsler

Som standard tar Vantigo en endring gjort i Outlook ved neste synkronisering, hvert 15.
minutt. Med **endringsvarsler** sier Microsoft fra til Vantigo når noe endres i en
synkronisert postboks, og personens synkronisering blir forfalt med en gang: endringen
tas på omtrent ett minutt. Vantigo ber om ett varselabonnement per synkronisert
postboks, på postboksens hendelser, og fornyer det før det går ut. **Et varsel er et puff
og ingenting mer**: det inneholder ingen avtale, og Vantigo leser postboksen som før;
pollingen hvert 15. minutt går enten varslene virker eller ikke, så ingenting avhenger av
dem. Hva Vantigo ber om og svarer, står i [referansen](/en/reference/entra/#change-notifications).

#### Forutsetninger

- Kalendersynkronisering er slått på og virker ([over](#slå-den-på)). Varslene trenger
  ingen ekstra tillatelse: `Calendars.ReadWrite` dekker dem.
- **`PUBLIC_BASE_URL` er satt**, og Microsoft kan nå den fra internett over https: et
  vertsnavn med et sertifikat en offentlig utsteder har signert. Sett opp den offentlige
  verten som for betalingssiden ([Den offentlige
  verten](/nb/admin/payments/#den-offentlige-verten)); de samme proxy-reglene gjelder. Den
  må slippe `POST /api/v1/entra/mailbox-notifications` gjennom til Vantigo — den ene stien
  Entra legger på den offentlige vertens liste, anonym fordi Microsoft ikke sender noen
  økt.
- `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` er ikke `0` (standarden er på).

#### Slå dem på

1. Sett `PUBLIC_BASE_URL` i `vantigo.env` (en omstart leser den), og la
   `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` stå usatt, eller `1`.
2. Sjekk utenfra at stien kommer gjennom. Et varsel uten innhold godtas, og Microsofts
   håndtrykk — som Vantigo bare svarer på mens det oppretter et abonnement — gjør det ikke:

   ```sh
   curl -i -X POST https://pay.example.com/api/v1/entra/mailbox-notifications \
     -H 'Content-Type: application/json' -d '{"value":[]}'                    # 202
   curl -i -X POST 'https://pay.example.com/api/v1/entra/mailbox-notifications?validationToken=x'   # 404
   ```

3. Vent på neste synkronisering for en person som har den på: etter en som virket,
   abonnerer Vantigo på postboksen.

Verifiser: på modulsiden, under **Kalendersynkronisering**, står **Endringsvarsler** som
**På, overvåker 1 postboks. Siste varsel: ingen ennå.** (tallet vokser med postboksene).
Endre kopien av en avtale i personens Outlook: innen et minutt er endringen i Vantigo, og
**Siste varsel** viser tidspunktet.

#### Slå dem av

Sett `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED=0` og start på nytt. Ingenting nytt
abonneres, og bare pollingen går; linjen sier **Slått av av driftsansvarlig …**.
Abonnementer som finnes, slettes ikke av dette — de går ut innen sju dager, og et varsel
om ett gjør bare en synkronisering forfalt — men hvert slettes hos Microsoft når
personens kopier ryddes bort (et som Microsoft nekter å slette, får gå ut på samme måte). En tom `PUBLIC_BASE_URL` gir samme virkning, og linjen sier
**Krever PUBLIC_BASE_URL …**.

#### Hva som kan gå galt

- **Microsoft har ikke nådd adressen siden …** Når Microsoft oppretter et abonnement,
  sender det først en valideringsforespørsel til adressen, og den feilet. Vantigo oppretter
  ingen abonnement på **en time**, slik at én gal adresse ikke koster ett forsøk per
  person, og prøver så på nytt. Sjekk utenfra at `PUBLIC_BASE_URL` slår opp, at
  sertifikatet er gyldig og kjeden komplett, at proxyen slipper stien gjennom med `Host`
  satt til den offentlige verten, og at ingen brannmur eller webapplikasjonsfilter stopper
  forespørsler fra Microsoft. Linjen forsvinner ved det første abonnementet som virker.
  I mellomtiden tar pollingen endringene.
- **Overvåker færre postbokser enn personer.** En postboks Microsoft avviser — en utenfor
  et Exchange RBAC-omfang ([over](#begrens-den-til-enkelte-postbokser)) — avvises for
  varsler slik den er for synkroniseringen, og den personens endringer kommer ved
  pollingen. En person hvis synkronisering ennå ikke har skrevet, abonneres etter sin første
  som gjør det.
- **Et varsel kommer, men ingenting endres.** Synkroniseringen det gjør forfalt, leser
  postboksen; hvis den lesingen avvises eller feiler, sier kortet det, som for enhver
  synkronisering.
- **Et annet programs abonnement på samme postboks.** Vantigo sletter bare et abonnement
  som ligger på denne postboksens hendelser og sender til denne installasjonens egen
  adresse, aldri et annets. Hvis Microsoft avviser et nytt på grunn av et abonnement som
  tilhører **en annen installasjon** — to Vantigo-installasjoner som deler én
  appregistrering — logger Vantigo en advarsel med leietakeren, prøver ikke den
  postboksen igjen på **24 timer**, og bare pollingen betjener den.
- **`PUBLIC_BASE_URL` er endret.** Et abonnement laget for den gamle adressen slettes ved
  personens neste synkronisering og lages på nytt for den nye, slik at den gamle adressen
  aldri varsles igjen. Hvis Microsoft avviser slettingen, prøver neste synkronisering den
  på nytt.
- **En synkronisering som feiler, haster ikke.** Et varsel gjør en persons synkronisering
  forfalt med en gang bare når den ikke venter på en ny prøve etter en feil; en person
  hvis synkronisering stadig feiler, venter på sine egne tidspunkter.
- **Ingen varsler fra en travel leietaker.** Webhooken godtar 600 forespørsler i minuttet
  per klientadresse; Microsoft prøver en avvist på nytt i timevis, og pollingen fanger det
  som mangler. Bak en proxy, sett `TRUSTED_PROXY_HOPS` og `TRUSTED_PROXY_CIDRS` slik at
  Microsofts adresser skilles fra proxyens ([Videresendte headere og
  HTTPS](/nb/admin/authentication/#videresendte-headere-og-https)).

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
| Forlat gruppene | **Alle**: hver gruppe unntatt dynamiske, rolletildelbare og rene e-postgrupper (distribusjonslister), også grupper Vantigo aldri laget. **Administrerte**: bare gruppene Vantigo laget eller tok over for Ansattes grupper, som tabellen **Grupper** på modulens side viser. **Ingen**: alle medlemskap blir stående. |

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

## Ansattes grupper i leietakeren

HR holder grupper i Ansatte og slår på Microsoft Entra ID for en gruppe på gruppens side
([Grupper](/nb/user/staff/#grupper)). Ansattes kontoarbeider får da modulen til å lage
gruppen i leietakeren — en sikkerhetsgruppe eller en Microsoft 365-gruppe, etter
gruppens **Gruppetype** — med gruppens eiere som eiere, og holde navnet, beskrivelsen og
medlemmene i samsvar med Ansatte ved hver runde. En Microsoft 365-gruppe kan gjøres til et
team. Ingenting annet trengs i leietakeren: `Group.ReadWrite.All`, en av de åtte
tillatelsene, dekker gruppene, eierne, medlemmene og teamet.

**Vantigos applikasjon eier hver gruppe den lager.** Modulen setter sin egen
tjenestekontohaver (service principal) — applikasjonen fra steg 1, slik **Enterprise
applications** viser den — som eier av hver gruppe, ved siden av personene HR har angitt.
Microsoft lar ikke en applikasjon endre en gruppe den opprettet uten eier, så **ikke fjern
applikasjonen fra en gruppes eiere i portalen**: Vantigo fjerner den aldri, og en gruppe
uten den kan det hende Vantigo ikke lenger kan endre. En eier HR angir, blir eier i
leietakeren først når kontoen deres der er aktivert.

**En gruppe som allerede finnes i leietakeren, tas over uten å være klarert.** Før
modulen lager en gruppe, ser den etter en som har e-postaliaset (mail nickname) — en
laget for hånd tidligere med samme alias — og kobler den til Ansatte-gruppen, men
**skriver ingenting til den før en med `entra:manage` bekrefter den**: ikke noe navn,
ingen eier, ingen medlemmer, ingen sletting. Ellers kunne alle som holder grupper i
Ansatte, ta over hvilken som helst gruppe i leietakeren ved å gi en Ansatte-gruppe
aliaset dens. Til da sier gruppens side i Ansatte *Funnet i Microsoft Entra ID og venter
på bekreftelse der* — ingen feil, og ikke noe varsel på dashbordet i Ansatte — og
tabellen **Grupper** på modulens side merker gruppen *Overtatt – bekreft*. Modulen
rører aldri en gruppe med aliaset som er dynamisk, rolletildelbar, en
distribusjonsliste eller en e-postaktivert sikkerhetsgruppe (som Exchange holder), eller
som en annen Ansatte-gruppe alt bruker, heller ikke et alias to grupper har; og den
avviser en gruppe av en annen type enn Ansatte-gruppens **Gruppetype**. Gruppens side i
Ansatte sier da hvorfor: gi den gruppen et annet alias i portalen, eller gi
Ansatte-gruppen et annet **E-postalias** før den lages.

Slik bekrefter du en overtatt gruppe:

1. Åpne **Microsoft Entra ID**. I tabellen **Grupper** står gruppen som *Overtatt –
   bekreft*.
2. Sjekk i portalen at det er gruppen som er ment for den Ansatte-gruppen — navnet,
   eierne, medlemmene.
3. Velg **Bekreft** ved siden av den, og **Bekreft** igjen i *Bekrefte overtakelsen av
   …?*.

Fra Ansattes neste kontokjøring forvalter Vantigo gruppen: Ansattes navn, beskrivelse og
synlighet skrives til den, **eierne blir nøyaktig HRs og applikasjonen — alle andre
fjernes —** og medlemmene nøyaktig Ansattes. Bekreft aldri en gruppe du ikke har
sjekket. Er det ikke gruppen som er ment, lar du den være ubekreftet — Vantigo skriver
ingenting til den — og gir personene en annen Ansatte-gruppe med en annen nøkkel:
Ansatte-gruppen forblir koblet til gruppen den fant, til den bekreftes eller gis slipp
på. **Vantigo sletter aldri en gruppe den ikke har opprettet**, bekreftet eller ikke.

**Endringer i portalen** varer ikke: navnet, beskrivelsen og synligheten skrives på
nytt, og eierne settes til Ansattes, når HR endrer gruppens navn, beskrivelse,
innstillinger eller eiere; medlemmene settes til nøyaktig Ansattes hver gang Ansattes
medlemmer endres, og minst én gang i døgnet — et medlem lagt til i portalen, eller en
som meldte seg inn i en offentlig gruppe selv, er fjernet innen neste dag. En nestet
gruppe eller en enhet du legger til som medlem, blir stående. Hold gruppens medlemskap og eiere i Ansatte, ikke i
portalen. En gruppe som slettes i portalen, lages på nytt neste gang Ansatte sender
innstillingene; til da kan gruppens side si at katalogen har en gruppe Vantigo ikke får
skrive til — lagre gruppens innstillinger på nytt for å få den laget.

**Lisensene kommer fra gruppene.** En person med aktivert konto tildeles direkte
lisensene til hver gruppe de er med i som har Microsoft Entra ID slått på, og en lisens
Vantigo har tildelt, fjernes igjen neste gang Ansatte bringer kontoen deres i samsvar og
ingen av gruppene deres lenger nevner den. **Vantigo fjerner bare lisensene den selv
har tildelt**: en lisens en administrator har tildelt direkte, en personen hadde før en
gruppe nevnte den, og en som kommer fra en gruppe i leietakeren (gruppebasert
lisensiering), blir stående. Vantigo fører listen per konto fra denne versjonen av; en
konto den aktiverte tidligere, starter med en tom liste, så ingenting som ble tildelt
den før, fjernes av en gruppeendring. Steget **Fjern lisensene** for en som slutter, er
unntaket: det fjerner hver lisens som er tildelt personen direkte, Vantigos eller ikke.
Valgene en gruppes
**Lisenser** gir, er leietakerens lisenser slik den siste tilkoblingstesten, eller det
siste blikket på **Leietakerens lisenser** i steg 4, leste dem: test igjen etter at du
har kjøpt en ny lisens.

**En arkivert gruppe, eller en HR har slått av Microsoft Entra ID for**, følger Ansattes
**Slett en ekstern konto (dager etter slutt)**:

| Innstillingen | Hva som skjer med gruppen i leietakeren |
| --- | --- |
| Et antall dager | Så mange dager etter arkiveringen (eller etter at bryteren ble slått av) gir Vantigo slipp på gruppen, etter typen og etter hvem som laget den. **En Microsoft 365-gruppe Vantigo laget**, slettes — en myk sletting: den ligger i leietakerens **Deleted groups** i 30 dager og kan gjenopprettes der. **En sikkerhetsgruppe Vantigo laget**, slettes aldri, siden Microsoft sletter en sikkerhetsgruppe for godt: medlemmene og eierne som er personer, fjernes (applikasjonen blir stående som eier), den får ` (archived)` etter navnet og beholdes. **En gruppe Vantigo ikke opprettet**, blir stående i leietakeren som den er. Til dagene er gått, blir gruppen stående som den var. |
| Tom (aldri) | Ved neste runde får gruppen ` (archived)` etter navnet og beholdes, med medlemmene slik de var; Vantigo skriver ingenting til den etterpå. |

En slettet Microsoft 365-gruppe tar med seg postboksen, SharePoint-området og teamet når
de 30 dagene er omme. Tabellen **Grupper** på modulens side viser en beholdt
sikkerhetsgruppe som *Tømt og beholdt*, og en gruppe Vantigo ikke opprettet, som
*Slettet* — Vantigo har gitt slipp på den, men leietakeren har den fortsatt. Slår HR på
Microsoft Entra ID igjen for Ansatte-gruppen, finnes en beholdt sikkerhetsgruppe igjen
på aliaset og er Vantigos egen igjen, uten bekreftelse; slett den i portalen når den
ikke lenger trengs.

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
gjenopprettes under **Deleted users** i 30 dager. Den lager også en gruppe med
applikasjonen og brukeren som eiere (en sikkerhetsgruppe med e-postalias som begynner
med `vt-test-`), lager den igjen for å sjekke at den finner den samme gruppen, legger
brukeren til som medlem og fjerner dem igjen i en `$batch`, og gir slipp på den —
teamet prøves ikke, siden en gruppe først må være femten minutter gammel. Som
sikkerhetsgruppe slettes den ikke, men tømmes, får ` (archived)` etter navnet og
beholdes, så hver kjøring etterlater én `vt-test-`-gruppe i leietakeren: slett dem i
portalen. Den
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

Problemer med kalendersynkroniseringen vises på kortet **Status**, på linjen under bryteren, og på hver
persons **Outlook**-kort i Kalender (Outlook-tilkoblingens egne står [over](#når-tilkoblingen-feiler)):

| Hvor | Betyr | Gjør |
| --- | --- | --- |
| `Calendars.ReadWrite` vist som manglende på kortet **Status**, og *Kalendersynkroniseringen trenger applikasjonstillatelsen Calendars.ReadWrite, …* under bryteren | Bryteren er på, og den siste testens token manglet applikasjonstillatelsen. Som ventet med RBAC for Applications; synkroniseringen kjører uansett. | Ingenting med RBAC for Applications. Ellers legg til tillatelsen, gi samtykke og test igjen ([kalendersynkronisering](#slå-den-på)); til da avvises hver persons synkronisering som at postboksen deres er nektet. |
| *Venter på tilkoblingen* under bryteren | Tilkoblingen er ikke tilkoblet; årsaken står under linjen. | Rett tilkoblingen slik tabellen over sier. |
| *Organisasjonen din har ikke gitt Vantigo tilgang til postboksen din.* på en persons kort | Graph svarte 403 for postboksen (`mailbox_denied`): utenfor et omfang i RBAC for Applications. Prøves igjen hver sjette time. | Legg personen til i omfangets gruppe, eller godta det; personen trykker **Synkroniser nå** for å prøve med en gang. |
| *Postboksen din ble ikke funnet.* på en persons kort | Brukeren har ingen Exchange Online-postboks, eller finnes ikke lenger (`mailbox_gone`). Prøves igjen hver sjette time. | Gi kontoen en lisens med Exchange Online. |
| *En endring fra Outlook ble ikke tatt med* på en persons kort | Personen endret noe i Outlook som Vantigo ikke tar med — en avtale som også ble endret i Vantigo, en endring reglene avviser, en annens avtale, eller feltene i en forekomst eller i et element fra en annen app — og Vantigo la kopien tilbake. Setningen under overskriften sier hvilket. | Ingenting: personen gjør den endringen i Vantigo, eller ber arrangøren av avtalen om det. |
| En persons endring i Outlook kom ikke til Vantigo | Den gjaldt ikke personens egen avtale; det var en tittel, beskrivelse eller et sted med **Bare opptatt**; kopien var blitt et møte (personen la til deltakere), som Vantigo lar være; eller synkroniseringen står ikke som **Sist synkronisert** — en mislykket lesing viser *Den siste synkroniseringen mislyktes; Vantigo prøver igjen.*, og skrivingen i den synkroniseringen kan skrive Vantigos versjon over en endring i en kopi Vantigo også endret. | Sjekk personens kort; personen trykker **Synkroniser nå**. En endring som gikk tapt ved en mislykket lesing, gjøres på nytt i Vantigo. |
| *Vantigo-brukeren din er ikke koblet til en Microsoft 365-konto som Vantigo styrer.* på en persons kort | Ansatte kobler brukeren til ingen konto i denne modulen, til en Vantigo ikke opprettet og ingen har bekreftet, eller til en slettet (`no_account`). | Gi personen kontotypen Microsoft Entra ID i Ansatte, eller [bekreft kontoen](#kontoer-vantigo-ikke-opprettet). |

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
| Bildet ikke skrevet (*Katalogen avviste bildet.*) | Graph avviste personens bilde, eller fjerningen av det (`photo_not_written`): en 400, 403 eller 413 — eller en 404, for en bruker uten postboks — eller bildet kunne ikke gjøres om til en JPEG under 4 MB. Vantigo prøver ikke det samme bildet igjen. | Ingenting trengs for innloggingen. HR kan legge inn et annet bilde, eller personen velge et annet, som Vantigo prøver ved neste kjøring; se modulens siste hendelser (*Bildet til en konto ble satt*) for kontoen. |
| *Et avslutningssteg i katalogen ble ikke fullført: …* | Et steg for en som slutter feilet etter deaktiveringen, og stegene etter det ble ikke prøvd; kontoen er deaktivert. | Vanligvis ingenting: Ansatte prøver igjen neste dag. Feiler det fortsatt, se modulens siste hendelser og loggen, og gjør steget i portalen. |

På en gruppes side i Ansatte sier kortet **Microsoft Entra ID** hvordan gruppen står i
leietakeren:

| På kortet | Betyr | Gjør |
| --- | --- | --- |
| *Mislyktes: dette kan ikke endres når gruppen er laget* ved **Gruppetype** eller **E-postalias** | Verdien er en annen enn den gruppen ble laget eller tatt over med (`trait_immutable`). | Sett den tilbake; en gruppe av en annen type er en ny Ansatte-gruppe. |
| *Mislyktes: den trenger en person som eier* ved **Team i Microsoft Teams** | Et team trenger en person blant eierne (`trait_needs_owner`). | Legg til en eier på gruppens side; Ansatte sender den ved neste runde. |
| *Mislyktes: verdien er ikke en katalogen godtar* ved en innstilling | `trait_invalid` — for eksempel en synlighet eller et team på en sikkerhetsgruppe. | Endre verdien og lagre. |
| *Funnet i Microsoft Entra ID og venter på bekreftelse der* | `group_unconfirmed`: en gruppe som allerede finnes i leietakeren, har e-postaliaset; Vantigo koblet den og skriver ingenting til den. | [Bekreft den](#ansattes-grupper-i-leietakeren) på modulens side etter å ha sjekket at det er gruppen som er ment. |
| *Mislyktes: katalogen har en gruppe med denne nøkkelen som Ansatte ikke får skrive …* | `group_not_manageable`: en gruppe med e-postaliaset er dynamisk, rolletildelbar eller Exchanges, en annen Ansatte-gruppes, har aliaset sammen med en annen, eller er borte fra leietakeren. | Gi den andre gruppen et annet alias i portalen, eller gi Ansatte-gruppen et annet **E-postalias** før den lages; for en gruppe slettet i portalen, lagre innstillingene på nytt. |
| *Mislyktes: katalogen kunne ikke nås; arbeideren prøver igjen* | Tre kall på rad mislyktes (`call_failed`). | Se tilkoblingens kort **Status** og loggen. |
| *… medlemmer kunne ikke endres; de prøves igjen ved neste synkronisering* | Leietakeren avviste å legge til eller fjerne noen medlemmer (`members_failed`). | Vanligvis ingenting: Ansatte prøver dem igjen ved neste runde. |
