---
title: Meta
description: Å koble Vantigo til Facebook Messenger gjennom din egen Meta-app — hva du trenger først (meta i MODULES ved siden av communications, den offentlige verten, objektlageret), å opprette appen i Metas utviklerportal, App Review, Business Verification og Advanced Access til pages_messaging (må bekreftes), URL-ene til personvernerklæringen og slettingsinstruksjonene Meta krever, Meta-siden under Integrasjoner (app-ID, kryptert apphemmelighet, verifiseringstokenet og Lag nytt, webhook-adressen, avkryssingen for Human Agent), å sette webhooken og abonnere på sidens felter i Metas portal, å legge til en Facebook-side med tokenet til en systembruker som ikke utløper, å fjerne en side og et avvist token, å verifisere med statuslinjen og en første melding, oppbevaring og personopplysninger (rå hendelser 7 dager, bekreftede rader 7 dager, aldri bekreftede rader 30 dager, navn 30 dager), jobbene meta-split og meta-retention, de to webhook-rutene på den offentlige verten, og hva som kan gå galt.
sidebar:
  order: 50
sources:
  - apps/server/internal/meta/module.go
  - apps/server/internal/meta/core.go
  - apps/server/internal/meta/server.go
  - apps/server/internal/meta/errors.go
  - apps/server/internal/meta/messenger.go
  - apps/server/internal/meta/settings.go
  - apps/server/internal/meta/settings_ops.go
  - apps/server/internal/meta/transport.go
  - apps/server/internal/meta/graph.go
  - apps/server/internal/meta/webhook.go
  - apps/server/internal/meta/inbound.go
  - apps/server/internal/meta/inbound_worker.go
  - apps/server/internal/meta/retention.go
  - apps/server/internal/meta/send.go
  - apps/server/internal/meta/pages.go
  - apps/server/internal/meta/profile.go
---

Meta-modulen kobler Vantigo til **Facebook Messenger**. Du tar med din egen Meta-app og
én eller flere Facebook-sider virksomheten din eier; en person som skriver til en side,
starter da en samtale i Kommunikasjon, og svarene dine går tilbake gjennom samme side.
Modulen eier forbindelsen (appen, sidenes tokener, webhooken); samtalene tilhører
Kommunikasjon, og en side blir en kanal der
([Sett opp en Messenger-kanal](/nb/user/communications/#sett-opp-en-messenger-kanal)).

Denne siden er driftsansvarliges side: hva Meta trenger, trinnene i Metas utviklerportal
og i Vantigo, og hva du gjør når noe feiler. Siden i Vantigo heter **Meta** under
**Integrasjoner** i Administrer arbeidsområde, og den er kun for en **eier**. Reglene modulen
sjekker står i [referansen](/en/reference/meta/).

## Før du begynner

- **`meta` i `MODULES`, ved siden av `communications`.** `MODULES` avviser `meta` uten
  `communications` ved oppstart (`meta requires communications`): en side er bare nyttig
  som kanal.
- **En offentlig vert.** Meta sender hver melding til instansen, så `PUBLIC_BASE_URL`
  må være satt og nåbar fra internett over https med et sertifikat Meta stoler på.
  Innstillingene **kan ikke lagres** uten den, og siden sier fra om det. Å publisere
  verten er en egen prosedyre ([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten)).
- **Objektlageret.** Filer folk sender, oppbevares der en kort stund
  ([Objektlagring](/nb/admin/object-storage/)).
- **Utgående https** fra hver prosess som kjører API-et eller jobbene til
  `graph.facebook.com` og til Metas filtjenere, uten proxy: modulen bruker ingen og
  følger ingen omdirigering.
- **En eier** i Vantigo, innlogget med andre faktor så lenge `OWNERS_REQUIRE_MFA` er på.
- **En Meta-utviklerkonto**, en Facebook-side virksomheten eier, og en Meta-
  virksomhetsportefølje (Business Manager) som eier siden.
- Migreringene oppretter skjemaet `meta` enten modulen er på eller ikke; en
  applikasjonsrolle med minste rettigheter må få det innvilget som alle de andre.

## Opprett Meta-appen

1. Velg **Create app** i [Metas utviklerportal](https://developers.facebook.com/apps/), og
   gi appen typen **Business**.
2. Legg til produktet **Messenger**.
3. Noter **App ID** og **App secret** under appens innstillinger. Begge skriver du inn i
   Vantigo nedenfor.

## App Review og Business Verification

**Må bekreftes.** Etter det Metas dokumentasjon tilsier, trenger en app som sender
meldinger til folk uten rolle i appen, **Advanced Access** til tillatelsen
`pages_messaging`, og Advanced Access krever **App Review** og **Business Verification**.
Så lenge appen ikke har det, kan bare folk med en rolle i appen (administratorer,
utviklere og testere) skrive til siden og få svar. Vantigo kan ikke endre dette.
Oppførselen blir verifisert mot en testapp og en testside før denne siden fjerner
forbeholdet.

Ta høyde for dette før du lover kundene en kanal: søk tidlig, test med folk som har en
rolle i appen, og les Metas gjeldende krav.

## Hva Meta krever av appen

Meta krever at virksomheten som driver appen, publiserer en **URL til en
personvernerklæring** og instruksjoner for **sletting av data** (eller en URL for
tilbakekall om sletting) før appen kan gå live. Virksomheten leverer begge selv; Vantigo
skriver dem ikke. Vantigo oppbevarer personopplysninger i denne modulen bare i periodene
under [Oppbevaring og personopplysninger](#oppbevaring-og-personopplysninger), og
erklæringen din må beskrive det.

## Skriv appen inn i Vantigo

1. Åpne avatarmenyen, **Administrer arbeidsområde**, og så **Integrasjoner** › **Meta**.
2. Under **Meta-app** skriver du inn **App-ID** (bare sifre) og **Apphemmelighet**.
   Hemmeligheten lagres kryptert og vises aldri igjen; la feltet stå tomt ved en senere
   lagring for å beholde den. **Lagre**.
3. Under **Webhook** kopierer du **Callback-URL**,
   `https://<offentlig vert>/api/v1/meta/webhook`, og **Verifiseringstoken**.
   Verifiseringstokenet lages for deg. **Lag nytt** lager et nytt, og Metas håndtrykk
   avvises helt til det nye tokenet står i appens webhook-innstillinger.
4. La **Meta har gitt denne appen tillatelsen Human Agent** stå uten hake foreløpig. Med
   tillatelsen kan et svar komme opptil sju dager etter personens siste melding i stedet
   for 24 timer; Vantigo sender det da med Metas tagg `HUMAN_AGENT`. Huk av først når Meta
   har godkjent tillatelsen for appen, noe du søker om hos Meta. Avviser Meta en sending
   med tagg, feiler meldingen som *Human Agent er ikke gitt til appen*, og statuslinjen ber
   deg fjerne haken.

## Sett webhooken i Metas portal

1. I appens **Messenger**-innstillinger åpner du webhook-delen og legger til en
   callback-URL. Lim inn **Callback-URL** og **Verifiseringstoken** fra Vantigo.
2. Meta sender en `GET`-forespørsel til adressen med tokenet (håndtrykket). Vantigo svarer
   når tokenet er riktig. Melder Meta at adressen ikke kunne verifiseres, se [Hva som kan
   gå galt](#hva-som-kan-gå-galt).
3. Abonner appen på sidefeltene **messages**, **messaging_postbacks**, **message_reads**,
   **message_deliveries** og **message_echoes**. Vantigo abonnerer også hver side på disse
   når du legger den til.

## Legg til en side

Et **token for en systembruker** som ikke utløper, er den anbefalte måten. Et token fra en
persons egen innlogging slutter å virke når personens passord eller tillatelser endres.

1. Opprett en **systembruker** i Metas **Business Settings** (**Users** › **System
   users**), og gi den siden med oppgaven å administrere den og sende meldinger.
2. Lag et token for systembrukeren for appen, med tillatelsen `pages_messaging`, og med
   **Never** som utløp der Meta tilbyr det.
3. Lim tokenet inn i **Tilgangstoken** under **Sider** i Vantigo og velg **Legg til
   side**.

Vantigo spør Meta hvilke sider tokenet forvalter, og beholder de hvis oppgaver omfatter
**MESSAGING**. Er det én, kobles den siden til. Er det flere, ber Vantigo deg om å **Velge
siden som skal kobles til**: velg en og velg **Legg til side** på nytt. Så abonnerer
Vantigo appen på sidens `messages`, `messaging_postbacks`, `message_reads`,
`message_deliveries` og `message_echoes`, og lagrer **sidens eget token** kryptert, ikke
tokenet du limte inn. Siden vises med navn og ID, og ingen token vises igjen.

Et tilgangstoken for selve siden, limt inn direkte, fungerer også. Meta lister ikke sider
for et slikt token, så Vantigo leser siden fra tokenet og sjekker at det har lov til å
handle for den ved å abonnere appen; avviser Meta det, avvises siden.

**Fjern** en side for å koble den fra. Tokenet, de køede hendelsene og filene deres
slettes, og kanaler på siden blir utilgjengelige. Vantigo trekker også appens abonnement
tilbake fra siden så langt Meta svarer; et token Meta har avvist, eller at Meta er nede,
hindrer ikke at siden fjernes, og da blir abonnementet stående hos Meta til du fjerner
appen fra siden der.

**Et avvist token.** Når Meta svarer et kall med feil 190 (tokenet er utløpt eller
trukket tilbake, eller personens tillatelser er endret), viser siden **Token avvist** og
kanalene dens viser **Utilgjengelig**; ingenting sendes til den. Siden friskner ikke til
av seg selv. **Legg til side** på nytt med et nytt token for den samme siden.

## Verifiser

1. Se på **Status** på Meta-siden: **Siste webhook mottatt** viser når Meta sist sendte
   en melding med gyldig signatur (tiden oppdateres høyst hvert 30. sekund), **Siste
   innsending med feil signatur** den siste avvisningen, og **Siste feil fra Meta** den
   siste avvisningen av et kall til Meta.
2. Legg til en kanal på **Facebook Messenger** for siden i Kommunikasjon.
3. Skriv til siden fra en Facebook-konto som har en **rolle i appen**. Samtalen dukker
   opp i Kommunikasjon i løpet av noen sekunder; svar på den og sjekk at svaret kommer
   fram i Messenger.

## Oppbevaring og personopplysninger

Modulen oppbevarer en persons sidespesifikke ID (PSID), navnet, teksten i meldingene og
filene de sender, og bare i disse periodene:

| Hva | Oppbevares |
| --- | --- |
| Rå webhook-hendelser | 7 dager |
| Innboksrader Kommunikasjon har bekreftet, og filene deres | 7 dager |
| Innboksrader som aldri er bekreftet, og filene deres | 30 dager |
| Et mellomlagret personnavn | 30 dager |

`meta-retention` sletter det som er forbi perioden sin én gang i timen. Modulen kjenner
ingen kunde-ID-er, så den kan ikke slette én kundes data på forespørsel; oppbevaringen er
den eneste grensen. Kommunikasjons egne kopier av en samtale dekkes av slettingen der
([Kommunikasjon](/en/reference/communications/)). En deltakerrad der overlever en kundes
sletting til foreldreløs-opprydningen fjerner den.

## Jobber

Modulen kjører to jobber, i API-prosessen og i jobbmodus på samme måte
([Jobber](/nb/admin/installation/)):

- `meta-split` gjør hver lagrede webhook-hendelse om til innboksrader, og henter filene
  en person sendte fra Metas tjenere med en gang, fordi adressene utløper. Den tar
  hendelser under en lease og prøver en som feiler, på nytt med økende ventetid.
- `meta-retention` sletter det som er forbi [perioden sin](#oppbevaring-og-personopplysninger).

## Rutene på den offentlige verten

Den offentlige verten serverer tre anonyme ruter for Meta, nøyaktig disse stiene
([Den offentlige verten](/nb/admin/payments/#den-offentlige-verten)); proxyen må slippe
alle gjennom:

| Metode | Sti | Gjør |
| --- | --- | --- |
| `GET` | `/api/v1/meta/webhook` | Metas håndtrykk: svarer `hub.challenge` når verifiseringstokenet er riktig |
| `POST` | `/api/v1/meta/webhook` | Metas hendelser, opptil 1 MiB |
| `GET`, `HEAD` | `/api/v1/communications/public-media/{token}` | Et korts bilde, som Meta henter når Kommunikasjon sender et produkt som kort; ett stisegment, signert av tokenet, gyldig i 7 dager |

Den tredje hører til Kommunikasjon, ikke til denne modulen: bildene til et produkt som sendes
i en melding må kunne nås fra internett, mens installasjonens lagring ikke har noen offentlig
adresse. Den serverer ikke annet enn kortbilder i meldinger sendt på Messenger, og alt annet er
en tom 404. En proxy som mellomlagrer den, kan beholde et bilde i opptil ett døgn
(`Cache-Control: private, max-age=86400`). Tokenet er en del av stien, og Vantigos egen
forespørselslogg skriver hver sti slik den er, som for Kalenders feed; proxyens tilgangslogg bør
derfor føre `public-media`-stien uten tokenet, eller ikke føre den i det hele tatt.

Hver innsending sjekkes med `X-Hub-Signature-256`, en HMAC-SHA256 over **råbytene** i
kroppen med apphemmeligheten. En proxy som skriver om kroppen (komprimering,
omkoding, et kroppsfilter) ødelegger signaturen, og hver innsending avvises med 401.

## Hva som kan gå galt

| Symptom | Årsak og tiltak |
| --- | --- |
| Meta kan ikke verifisere callback-URL-en | Verifiseringstokenet er et annet enn Vantigos, eller ble laget på nytt: kopier det igjen fra Meta-siden. Eller proxyen slipper ikke `GET /api/v1/meta/webhook` gjennom på den offentlige verten. |
| Metas leveringslogg viser 401 | Apphemmeligheten i Vantigo er ikke appens gjeldende hemmelighet, eller noe mellom Meta og Vantigo endrer kroppen. **Siste innsending med feil signatur** på Meta-siden viser når det sist skjedde. |
| Metas leveringslogg viser 503 eller 5xx | Databasen var treg, eller innstillingene er ufullstendige (ingen apphemmelighet). Meta prøver på nytt; lagre apphemmeligheten. |
| Ingenting kommer fra en person | Appen er i utviklingsmodus og personen har ingen rolle i appen; Advanced Access til `pages_messaging` mangler ([ovenfor](#app-review-og-business-verification)); eller siden er ikke abonnert på feltene. |
| Et svar feiler, *svarvinduet er stengt* | Messenger tillater svar bare innen 24 timer etter personens siste melding, sju dager med Human Agent. |
| Et svar feiler, *mottakeren kan ikke nås* | Meta kan ikke levere til personen (de har blokkert siden eller kan ikke nås gjennom den). |
| Et svar feiler, *Human Agent er ikke gitt til appen* | Meta avviste en sending med tagg. Fjern haken på Meta-siden, eller få tillatelsen fra Meta. |
| En side viser **Token avvist** | Se [Et avvist token](#legg-til-en-side). |
| Innstillingene kan ikke lagres | Instansen har ingen offentlig vert. Sett `PUBLIC_BASE_URL` og start serveren på nytt. |
| En fil mangler i en samtale | Metas filadresser utløper; `meta-split` henter filen med en gang, og gir opp den etter gjentatte feil (utgående https til Metas filtjenere trengs). |
| Å legge til en side sier *Tokenet er verken tilgangstokenet til en side eller et token for en bruker eller systembruker…* | Tokenet er utløpt eller trukket tilbake, tilhører en bruker eller systembruker som ikke forvalter noen side med oppgaven **MESSAGING**, eller er et sidetoken Meta avviste abonnementet for. Gi systembrukeren siden med oppgaven å sende meldinger, lag tokenet på nytt, eller lim inn et gyldig sidetoken. |
| Å legge til en side sier *Tokenet forvalter flere sider* | Velg siden som skal kobles til i listen som vises, og velg **Legg til side** på nytt. Legg til hver side for seg. |
| Å legge til en side sier at Meta ikke svarte | `graph.facebook.com` kan ikke nås fra serveren; prøv igjen om litt. |
