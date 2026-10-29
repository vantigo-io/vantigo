---
title: Microsoft Entra ID
description: Å koble Vantigo til en Microsoft Entra ID-leietaker slik at kontoene Ansatte gir, lages der — appregistreringen, klienthemmeligheten, de åtte applikasjonstillatelsene og rollen User Administrator, administratorsamtykket, policyen for midlertidig tilgangspass, kontoreglene, innlogging og klargjøring; Outlook-tilkoblingen — omdirigerings-URI-en på APP_URL, de delegerte tillatelsene og administratorsamtykket, bryteren, å verifisere ved å koble til selv, å slå den av, hva som skjer med en persons tilkobling og entra-outlook-sweep, og hva som kan gå galt; varselet om tillatelser Vantigo ikke bruker; kalendersynkronisering på hver persons egen Outlook-tilkobling — forutsetningene, å verifisere med en tilkoblet person, hvilke postbokser, å fjerne en tidligere versjons applikasjonstillatelse Calendars.ReadWrite og tildelingen i RBAC for Applications, bryteren av, en frakobling og kopiene til en som slutter som blir liggende i postboksen, og endringsvarsler; e-postkanaler — bryteren, kommandoene for RBAC for Applications som siden fyller inn, å fjerne en Mail-tillatelse for hele leietakeren, å verifisere med Test-ServicePrincipalAuthorization og en kanal, hurtigbufferen, å slå dem av og hva som kan gå galt; hva stegene for en som slutter gjør, og hva sletting mister; Ansattes grupper i leietakeren, applikasjonen som eier, å bekrefte en gruppe Vantigo ikke opprettet og hva en arkivert gruppe blir til; rotasjon av hemmeligheter; den merkede testen mot en ekte leietaker; og hva hver årsakskode betyr.
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
  - apps/server/internal/entra/channel_mailbox.go
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

   Disse åtte er alle: [kalendersynkronisering](#kalendersynkronisering) trenger ingen
   applikasjonstillatelse, siden den kjører på hver persons egen Outlook-tilkobling, og
   [e-postkanaler](#mail-channels) får sine i Exchange Online, avgrenset til
   postboksene sine, ikke her.
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
åtte, viser kortet **Status** et oransje varsel, *Applikasjonen
har tillatelser Vantigo ikke bruker:*, med listen. En applikasjonstillatelse når hver
postboks eller konto i leietakeren, så en ingen trenger, er best fjernet: med mindre et
annet produkt bruker denne registreringen, fjern den under **API permissions** og test
igjen. En applikasjonstillatelse `Calendars.ReadWrite` igjen fra en tidligere versjon,
da kalendersynkroniseringen skrev med den, er blant dem
([kalendersynkronisering](#kalendersynkronisering)). Det er bare et varsel —
tilkoblingen forblir **Tilkoblet** — og testen
registrerer hver som *en tillatelse Vantigo ikke bruker* i de siste hendelsene.
E-posttillatelsene (`Mail.*`, og `MailboxSettings.*`, `MailboxItem.*` og
`MailboxFolder.*`) er blant dem mens [e-postkanaler](#mail-channels) er av;
mens de er på, viser linjen for **E-postkanaler** dem i stedet, som tilgang til alle
postbokser i leietakeren.

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
den kontoen. Kalenderens synkronisering kjører på den
([kalendersynkronisering](#kalendersynkronisering)): hver person som kobler til, blir
synkronisert med sin egen pålogging, og ingen andre. Hvordan det virker, står i [referansen](/en/reference/entra/#the-outlook-connection).

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
   Vantigo** og velger **Lagre kontoreglene** (`entra:manage`). Kalendersynkronisering
   er da tilgjengelig for alle som kobler til ([under](#kalendersynkronisering)); steg 3
   ber ikke om noe mer.

### Verifiser Outlook-tilkoblingen

Koble til selv: åpne **Innstillinger → Tilkoblinger** og velg **Koble til Outlook**. Du
er tilbake på siden i løpet av sekunder, og den sier **Tilkoblet som** og kontoen din. På
modulsiden har linjen under bryteren gått fra **Ingen har koblet til ennå: …** til
**Klar**, *Personer tilkoblet:* teller deg, og de siste hendelsene på kortet **Status**
viser *En person koblet til Outlook*.

### Slå av Outlook-tilkoblingen

Slå av bryteren og velg **Lagre kontoreglene**. Er noen tilkoblet eller venter på ny
tilkobling, spør siden først — *Alle må koble til Outlook på nytt.* — og **Slå av og
lagre** lagrer. Hver persons tilkobling lukkes i den samme lagringen, hver registrert som
*En Outlook-tilkobling ble lukket* (*Outlook-tilkoblingen ble slått av*);
kalendersynkroniseringen stopper for alle, og kopiene fjernes, hver persons med sin egen
tilkobling som lukkes. Slås den på igjen, kobler hver person til på nytt, og ingens
kalendersynkronisering fortsetter før de har gjort det.

### Hva som skjer med en persons tilkobling

- **Microsoft avviser den.** Et tilbakestilt passord, tilbakekalte økter, en policy for
  betinget tilgang, et nytt krav om flerfaktorautentisering eller en pålogging som har
  utløpt, setter tilkoblingen i *Må kobles til på nytt*, med årsaken, på den personens
  side; *Venter på ny tilkobling:* teller dem, og hendelsen sier *Microsoft avviste en
  persons Outlook-tilkobling*. Vantigo oppgir klientegenskapen `cp1`, så Microsoft sier
  fra med en gang når en konto deaktiveres eller øktene tilbakekalles.
  Kalendersynkroniseringen deres tar en pause, kopiene blir som de var, og den fortsetter
  når de kobler til på nytt; ingenting annet stopper.
- **De slutter.** Når Ansatte deaktiverer kontoen, lukkes tilkoblingen og det lagrede
  tokenet glemmes i det samme steget, før de andre stegene for en som slutter kjører.
  **Kopiene kalendersynkroniseringen la inn i postboksen, blir liggende der**, i den
  deaktiverte postboksen: det er ikke noe igjen som kan fjerne dem, så Vantigo glemmer
  dem.
- **Kontoen deres slettes eller deaktiveres** i Vantigo eller i leietakeren.
  Tilkoblingen lukkes, og det lagrede tokenet glemmes, neste gang den brukes, eller — for en som venter på ny tilkobling
  — når arbeideren `entra-outlook-sweep` leser kontoen i leietakeren, høyst én gang i
  døgnet. Kalenderkopiene deres blir liggende, som for en som slutter.
- **De kobler fra.** Vantigo lukker tilkoblingen; den logger dem aldri ut av Microsoft.
  **Kalendersynkroniseringen fjerner kopiene deres** med tilkoblingen som lukkes, og
  venter så til de kobler til igjen.

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
appene, til Exchange Online-postboksen deres, og leser etter hver synkronisering
**standardkalenderen** deres tilbake — tidene for hendelsene som gjør dem opptatt, aldri
titlene — for å vise den opptatte tiden i Vantigo. Før hver skriving leser den også
Vantigos **egne kopier** — og, for en person som har valgt en egen *Vantigo*-kalender,
hver hendelse i den kalenderen — i sin helhet, for å ta med tilbake endringene,
flyttingene og slettingene personen gjorde i Outlook på sine egne avtaler
([brukerveiledningen](/nb/user/calendar/#endre-avtalene-dine-i-outlook)); personens egne
hendelser leser den aldri i sin helhet.

**Den kjører på hver persons egen [Outlook-tilkobling](#outlook-tilkobling)**: hver
skriving og lesing i en postboks bruker den personens delegerte pålogging, og den
delegerte `Calendars.ReadWrite` dekker alt. Applikasjonen får ikke noe for den — ingen
applikasjonstillatelse, ingen Exchange-rolle — så Vantigo når bare postboksene til dem
som har koblet til. Den er av som standard, og klargjøringen virker likt med den av eller
på. Hva den skriver og leser, og hvordan, står i
[referansen](/en/reference/entra/#the-mailbox-calendar).

### Forutsetninger

- Tilkoblingen er satt opp og **Tilkoblet** ([over](#koble-til-leietakeren)), og
  Kalender-modulen er slått på ([Kalender](/nb/admin/calendar/)).
- [Outlook-tilkoblingen](#slå-på-outlook-tilkoblingen) er satt opp og slått på:
  omdirigerings-URI-en og de delegerte tillatelsene med administratorsamtykke.
- De som skal synkronisere, har kontotypen **Microsoft Entra ID** i Ansatte, og kontoen
  deres er en Vantigo opprettet, eller som er bekreftet adoptert
  ([under](#kontoer-vantigo-ikke-opprettet)): Vantigo skriver bare til en postboks den
  vet er personens, aldri en som er funnet ut fra adressen.
- Kontoene deres har en Exchange Online-postboks (en lisens som omfatter Exchange
  Online).
- **Hver person kobler til sin egen Outlook** — under **Innstillinger → Tilkoblinger**,
  eller med **Koble til Outlook** på kortet **Outlook** i kalenderen.

### Slå den på

Det er ingenting å slå på utover [Outlook-tilkoblingen](#slå-på-outlook-tilkoblingen):
med bryteren dens på er kalendersynkronisering tilgjengelig for alle som kobler til.
Ingen applikasjonstillatelse og ingen administratorsteg utover tilkoblingens delegerte
`Calendars.ReadWrite` og samtykket til den.

Verifiser med en person som har koblet til: personen åpner **Kalender → Min kalender** og
slår på **Synkroniser til Outlook-kalenderen min**; innen et minutt sier kortet deres
**Sist synkronisert**, og avtalene står i Outlook. En person som slår den på før hen har
koblet til, ser **Koble til Outlook ovenfor for å starte synkroniseringen.**:
synkroniseringen venter, og starter innen et minutt eller så etter at hen har koblet til.
Resten av modulen virker som før.

**Hvilke postbokser.** Bare de som kobler til sin egen Outlook, blir synkronisert, og
bare sin egen postboks: tilkoblingen logger på som dem, og Microsoft nekter den enhver
annen postboks. Det er ikke noe omfang å sette opp og ingen Exchange RBAC å konfigurere.
For å holde noen utenfor kalendersynkroniseringen lar du dem være uten tilkobling —
eller stopper tilkoblingen for dem med en policy for betinget tilgang på Vantigos
applikasjon, som kortet deres da viser som *Må kobles til på nytt*.

**Et oppsett fra en tidligere versjon.** Kalendersynkroniseringen skrev før med
**applikasjonstillatelsen** `Calendars.ReadWrite`, av og til begrenset med Exchange RBAC
for Applications. Ingen av delene brukes lenger:

1. I registreringens **API permissions** fjerner du `Calendars.ReadWrite` av typen
   *Application* (behold den av typen *Delegated*), og velger **Test tilkoblingen** i
   steg 3: til du gjør det, viser kortet **Status** den under *Applikasjonen har
   tillatelser Vantigo ikke bruker:*.
2. Har du laget en tildeling i RBAC for Applications, fjerner du den i Exchange Online
   PowerShell (`Connect-ExchangeOnline`): list tildelingene av rollen, og fjern Vantigos
   ved navnet.

   ```powershell
   Get-ManagementRoleAssignment -Role "Application Calendars.ReadWrite" | Format-Table Name, RoleAssignee, CustomResourceScope
   Remove-ManagementRoleAssignment "<tildelingens navn>"
   ```

   Deretter, hvis ingenting annet bruker dem, omfanget (`Remove-ManagementScope "Vantigo
   calendar sync"`) og gruppen.
3. Be dem som synkroniserte før, om å koble til Outlook: til de gjør det, ber kortet dem
   om det, og kopiene deres blir som de var.

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

**Slår du av bryteren**, stopper synkroniseringen for alle, og hver persons
[Outlook-tilkobling](#slå-av-outlook-tilkoblingen) lukkes; arbeideren fjerner så kopiene
den skrev, hver persons med sin egen tilkobling som lukkes, så lenge en klienthemmelighet
virker. **Slår du den på igjen, fortsetter den ikke for noen av seg selv**: hver
tilkobling ble lukket, så hver person kobler til på nytt, og synkroniseringen deres
fortsetter når de har gjort det.

**En person som kobler fra**, får kopiene sine fjernet på samme måte, og synkroniseringen
venter til hen kobler til igjen. **En person som venter på ny tilkobling**, får en pause,
og kopiene blir som de var, til hen kobler til på nytt. **Kopiene til en som slutter, blir
liggende i postboksen**: Vantigo glemmer påloggingen til den som slutter når kontoen
deaktiveres, før noe annet, så det er ikke noe igjen som kan fjerne dem, og Vantigo
glemmer dem i stedet; postboksen deaktiveres sammen med kontoen. Det samme gjelder en
konto som deaktiveres eller slettes et annet sted. Fjern dem i Exchange selv om du
trenger det — de bærer merket som er beskrevet i
[referansen](/en/reference/entra/#the-account-check).

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
  ingen ekstra tillatelse: hver persons abonnement lages med personens egen tilkobling,
  og den delegerte `Calendars.ReadWrite` de har samtykket til, dekker det.
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
- **Overvåker færre postbokser enn personer.** En person som ikke har koblet til
  Outlook, eller som venter på ny tilkobling, har ingen synkronisering som virker, og
  dermed ikke noe abonnement — et Microsoft ber om å fornye mens hen venter på ny
  tilkobling, får utløpe, og lages på nytt når hen kobler til igjen. En postboks
  Microsoft avviser, avvises for varsler slik den er for synkroniseringen. En person hvis synkronisering ennå ikke har skrevet, abonneres etter sin første
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

<a id="mail-channels"></a>

## E-postkanaler

**E-postkanaler** lar Kommunikasjon sende et teams e-post fra en **delt postboks** i
Exchange Online — `post@`, `support@`, `faktura@` — som en kanal ([brukerveiledningen](/nb/user/communications/#sett-opp-en-kanal)).
Vantigo når disse postboksene med **applikasjonens egen tilgang**, ikke en persons, og
den tilgangen gis i **Exchange Online med RBAC for Applications**, avgrenset til
postboksene du merker — ikke med `Mail.*`-tillatelser i appregistreringen, som ville nådd
hver postboks i leietakeren. Det er av som standard.

I denne versjonen **sender en kanal bare**: meldingen sendes fra den delte postboksen og
ligger i **Sendte elementer** der, men ingenting i postboksen leses inn i Vantigo ennå;
mottak kommer senere. Hvordan
modulen sender, står i [referansen](/en/reference/entra/#mail-channels).

### Forutsetninger for e-postkanaler

- Tilkoblingen er satt opp og **Tilkoblet** ([ovenfor](#koble-til-leietakeren)): testen
  dens leser applikasjonens **objekt-ID** under **Enterprise applications**, som
  kommandoene trenger. Kommunikasjon-modulen er slått på.
- Postboksene er **delte postbokser** i Exchange Online. En persons postboks avvises som
  kanal.
- Du kan kjøre **Exchange Online PowerShell** (`Connect-ExchangeOnline`, fra modulen
  `ExchangeOnlineManagement`) som **Exchange-administrator** i Entra, eller som medlem av
  **Organization Management** i Exchange.
- `CustomAttribute10` er ledig på disse postboksene — kommandoene merker
  kanalpostboksene med den. Bruker noe annet den, velg et annet av de femten, og endre
  både `Set-Mailbox` og filteret i omfanget.

### Slå på e-postkanaler

1. På modulsiden, i steg **4 Sett kontoreglene**, under **E-postkanaler**, slå på
   **E-postkanaler: la Kommunikasjon sende gjennom delte postbokser i Exchange
   Online**, og velg **Lagre kontoreglene**. Alle med `entra:manage` kan gjøre det.
2. Under bryteren viser **Kommandoer for Exchange Online PowerShell** seks kommandoer med
   applikasjonens **applikasjons-ID** og **objekt-ID** fylt inn. Står det *Test
   tilkoblingen først*, velg **Test tilkoblingen** i steg 3 (en eier) og kom tilbake:
   plassholderne `<applikasjons-ID>` og `<objekt-ID>` fylles inn når en test har lest
   dem. Kopier kommandoene med kopieringsknappen ved siden av (*Kopier Kommandoer for
   Exchange Online PowerShell* for en skjermleser).
3. Kjør dem i rekkefølge i Exchange Online PowerShell, og bytt ut `post@firma.no` med
   adressen til en delt postboks:

   ```powershell
   New-ServicePrincipal -AppId <applikasjons-ID> -ObjectId <objekt-ID> -DisplayName "Vantigo"
   Set-Mailbox post@firma.no -CustomAttribute10 "Vantigo"
   New-ManagementScope -Name "Vantigo mail channels" -RecipientRestrictionFilter "CustomAttribute10 -eq 'Vantigo'"
   New-ManagementRoleAssignment -App <applikasjons-ID> -Role "Application Mail Full Access" -CustomResourceScope "Vantigo mail channels"
   New-ManagementRoleAssignment -App <applikasjons-ID> -Role "Application MailboxSettings.Read" -CustomResourceScope "Vantigo mail channels"
   Test-ServicePrincipalAuthorization -Identity <applikasjons-ID> -Resource post@firma.no
   ```

   - `New-ServicePrincipal` gjør applikasjonen kjent for Exchange. `-ObjectId` er
     objekt-ID-en under **Enterprise applications**, ikke den under **App
     registrations** — siden fyller inn den riktige.
   - `Set-Mailbox … -CustomAttribute10 "Vantigo"` merker én postboks. **Kjør den én gang
     for hver delte postboks** en kanal skal bruke, nå eller senere; omfanget følger
     merket.
   - `New-ManagementScope` er settet av merkede postbokser.
   - De to `New-ManagementRoleAssignment` gir, bare innenfor det omfanget, **Application
     Mail Full Access** (lese, skrive og sende) og **Application MailboxSettings.Read**
     (for å skille en delt postboks fra en persons).
4. **Fjern alle e-posttillatelser for hele leietakeren.** I registreringens **API
   permissions**, fjern hver applikasjonstillatelse `Mail.Read`, `Mail.ReadBasic`,
   `Mail.ReadBasic.All`, `Mail.ReadWrite`, `Mail.Send`, `MailboxSettings.Read`,
   `MailboxSettings.ReadWrite`, `MailboxItem.Read.All`, `MailboxItem.ImportExport.All`,
   `MailboxFolder.Read.All` eller `MailboxFolder.ReadWrite.All` (og `full_access_as_app`
   under *Office 365 Exchange Online*), og velg så **Test tilkoblingen** i steg 3. Exchange
   legger omfangets tildelinger sammen med Entras: et samtykke i registreringen når hver
   postboks uansett hva omfanget sier.

### Verifiser e-postkanaler

1. `Test-ServicePrincipalAuthorization` (den siste kommandoen) svarer én linje per rolle:
   **`InScope` er `True`** for både *Application Mail Full Access* og *Application
   MailboxSettings.Read* på postboksen. Den leser Exchanges egne tildelinger, ikke
   samtykkene i registreringen, og forsinkes ikke av hurtigbufferen nedenfor.
2. På modulsiden sier linjen under bryteren **Klar: ved siste test hadde applikasjonen
   ingen e-posttillatelse i Microsoft Graph for hele leietakeren. Tilgang gitt i Exchange
   vises ikke her – sjekk den med Test-ServicePrincipalAuthorization.** Den gjelder bare
   tillatelsene i registreringen; punkt 1 er sjekken av Exchanges.
3. I Kommunikasjon, legg til postboksen som kanal: **Kanaler** › **Legg til kanal**,
   leverandøren **Microsoft 365**, adressen ([brukerveiledningen](/nb/user/communications/#sett-opp-en-kanal)).
   Vantigo sjekker den da — adressen slått opp, postboksen delt, innboksen nåbar — og
   kanalen opprettes aktiv. **Bekreft** på raden sjekker igjen når som helst.

**Exchange bruker fra 30 minutter til 2 timer** på å ta i bruk en ny tildeling eller en
nymerket postboks i applikasjonens tilgang. Til da avvises kanalen som utenfor omfanget,
selv om testen i punkt 1 sier `InScope`; vent og prøv igjen.

### Slå av e-postkanaler

Slå av bryteren og lagre. Ingenting slettes: kanalene blir stående, Microsoft 365 tilbys
ikke lenger for nye, og e-post som venter på å sendes fra en Microsoft 365-kanal,
**holdes igjen**: den bruker ingen av de åtte forsøkene og prøves igjen med høyst 5
minutters mellomrom, til **24 timer etter at den ble lagt i kø**, da den gis opp som
`provider_unavailable` og telles som feilet. Slås bryteren på igjen innen det døgnet,
sendes e-posten. Det samme gjelder mens tilkoblingen feiler eller Entra-modulen er av.
Tildelingene i Exchange blir stående til du fjerner dem
(`Remove-ManagementRoleAssignment`, `Remove-ManagementScope` og `Set-Mailbox …
-CustomAttribute10 $null`).

### Hva som kan gå galt med e-postkanaler

- **Linjen under bryteren sier *Applikasjonen har tilgang til alle postboksene i
  leietakeren. Begrens den med RBAC for Applications, og fjern samtykket som gjelder hele
  leietakeren. Så lenge denne advarselen vises, kan alle som administrerer kanaler i
  Kommunikasjon, sende som hvilken som helst delt postboks i leietakeren.***
  (`tenant_wide`, oransje, også på siden **Kanaler** i Kommunikasjon). Tokenet fra siste
  test hadde en e-posttillatelse for hele leietakeren, gitt i registreringen (listen i
  punkt 4). Kanalene virker — det er et varsel — men omfanget begrenser ingenting så lenge
  samtykket står: en kanal kan legges til for hvilken som helst delt postboks. Fjern det
  (punkt 4) og test igjen. Testen kan ikke se `full_access_as_app` eller en
  tildeling i Exchange, så sjekk dem selv.
- **Å legge til en kanal sier at postboksen er utenfor det applikasjonen har tilgang
  til** (`mailbox_denied`). Postboksen er ikke merket, filteret i omfanget passer ikke
  merket, en av de to rollene mangler, eller endringen er ikke tatt i bruk ennå. Kjør
  `Test-ServicePrincipalAuthorization -Identity <applikasjons-ID> -Resource <adresse>`:
  er begge rollene `InScope`, vent på hurtigbufferen; ellers merk postboksen eller legg
  til tildelingen som mangler.
- **Å legge til en kanal sier at det er en personlig postboks** (`personal_mailbox`).
  Bare delte postbokser kan være kanaler. Gjør postboksen om til delt i Exchange, eller
  bruk en delt.
- **Å legge til en kanal sier at Microsoft 365 ikke har noen postboks med adressen**
  (`mailbox_unknown`). Ingen bruker har den som brukerhovednavn eller primær
  e-postadresse — et alias blir kanskje ikke funnet — eller postboksen er borte. Bruk
  postboksens primære adresse.
- **Microsoft 365 tilbys ikke i Kommunikasjon**, eller å legge til sier *E-postkanaler
  gjennom Microsoft 365 er ikke tilgjengelige. En Entra-administrator slår på
  E-postkanaler i Entra-innstillingene.* (`mail_channels_off`). Bryteren er av, tilkoblingen er ikke
  **Tilkoblet** (linjen under bryteren sier *Venter på tilkoblingen* med årsaken), eller
  Entra-modulen er ikke slått på.
- **En melding på en Microsoft 365-kanal ble gitt opp med en gang.** Utboksen stopper
  etter ett forsøk når postboksen er utenfor omfanget, borte eller ikke lenger kjent for
  Vantigo, meldingen er større enn Graph eller postboksen tar imot, eller en blindkopi
  (Bcc) ville gått tapt; koden står i meldingens feilhendelse og i loggen
  (`mailbox_denied`, `mailbox_gone`, `mailbox_unknown`, `too_large`, `bcc_unsupported`).
  Rett omfanget eller postboksen, og send på nytt.
- **En melding ble gitt opp som `provider_unavailable`.** E-postkanaler var av, eller
  tilkoblingen feilet, i 24 timer etter at meldingen ble lagt i kø. Rett bryteren eller
  tilkoblingen, og send på nytt.
- **Utkast blir liggende i mappen Utkast i en delt postboks.** En melding med blindkopi
  sendes gjennom et utkast; stopper Vantigos prosess mellom at utkastet lages og at det
  sendes, blir utkastet liggende, og det nye forsøket lager et nytt. Slett slike utkast i
  Outlook nå og da; ingenting annet fjerner dem.
- **En kanal byttet fra SMTP sender fra et alias.** En byttet kanal beholder sin egen
  adresse, som kan være et alias for den delte postboksen og ikke den primære adressen.
  Om Exchange sender `From:` aliaset, skriver det om eller avviser det, er ikke
  verifisert ennå; sjekk den første meldingen, eller legg til kanalen på nytt med den
  primære adressen.
- **Eksklusive administrasjonsomfang** i Exchange begrenser ikke en applikasjons tilgang,
  og et omfang filtrert på gruppemedlemskap (`MemberOfGroup`) teller bare direkte
  medlemmer; attributtfilteret ovenfor unngår begge deler.
- **Ikke verifisert mot en ekte leietaker ennå**: at Exchange beholder Vantigos egen
  `Message-ID` på en melding sendt på denne måten, at en blindkopi (Bcc) når
  mottakeren, og hvordan det sendes fra et alias. De står på versjonens sjekkliste; til da, sjekk overskriftene i én
  melding og én blindkopi etter at den første kanalen er satt opp.

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
| `Calendars.ReadWrite` under *Applikasjonen har tillatelser Vantigo ikke bruker:* på kortet **Status** | Applikasjonstillatelsen igjen fra en tidligere versjon; kalendersynkroniseringen bruker den ikke lenger. | Fjern den, og en eventuell tildeling i RBAC for Applications ([et oppsett fra en tidligere versjon](#slå-den-på)), og test igjen. |
| *Koble til Outlook ovenfor for å starte synkroniseringen.* på en persons kort | Personen har synkroniseringen på, men har ikke koblet til Outlook, eller tilkoblingen er lukket (`not_connected`). Spørres igjen én gang i døgnet, og med en gang hen kobler til. | Personen velger **Koble til Outlook** på kortet eller under **Innstillinger → Tilkoblinger**. |
| *Microsoft vil at du logger på igjen: velg Koble til Outlook på nytt ovenfor.* på en persons kort | Microsoft avviste personens tilkobling (`reconnect`): et endret passord, tilbakekalte økter, en regel for betinget tilgang eller flerfaktorautentisering, en utløpt pålogging. | Personen velger **Koble til Outlook på nytt**; kommer det stadig tilbake, sjekk policyen, som for [tilkobling](#når-tilkoblingen-feiler). |
| *Venter på tilkoblingen* under bryteren | Tilkoblingen er ikke tilkoblet; årsaken står under linjen. | Rett tilkoblingen slik tabellen over sier. |
| *Organisasjonen din har ikke gitt Vantigo tilgang til postboksen din.* på en persons kort | Graph svarte 403 for postboksen (`mailbox_denied`): Exchange avviser den, for eksempel en postboks uten Exchange Online. Prøves igjen hver sjette time. | Sjekk kontoens postboks og lisens; personen trykker **Synkroniser nå** for å prøve med en gang. |
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
