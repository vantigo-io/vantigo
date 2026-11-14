---
title: Administrasjon
description: Installere, konfigurere og drifte Vantigo selv.
sidebar:
  order: 0
  label: Oversikt
---

Denne delen er for den som drifter Vantigo: installerer det, konfigurerer det, holder
det oppe og oppgraderer det. Vantigo er ett container-image og én PostgreSQL-database;
hvilke forretningsmoduler en installasjon tilbyr velges med innstillingen `MODULES`,
og identitet (kontoer, innlogging, MFA, RBAC, OIDC og SCIM) er alltid en del av
applikasjonen.

## Hvor du begynner

- **Installer.** Raskeste vei er den ferdige Docker Compose-stacken: ferdigbygde images,
  en engangsjobb for migrering og applikasjonen på port 8080.
  [Installasjonsveiledningen](/nb/admin/installation/) tar deg fra første oppstart til
  produksjon: omvendt proxy, helsesjekker, databasens tilkoblingsbudsjett,
  bakgrunnsjobber og oppgradering.
- **Identitet og innlogging.** [Identitet, autentisering og utrulling](/nb/admin/authentication/)
  forklarer lokale kontoer, OIDC-leverandøren som konfigureres i miljøet, e-post,
  proxy-tillit og nøkkelmaterialet som utledes fra `APP_SECRET`.
  [SSO- og SCIM-drift](/nb/admin/sso-scim/) er produksjonsveiledningen for statisk OIDC og
  SCIM: konfigurasjon, rotasjon, migrering, livssyklus og gjenoppretting.
- **Sikkerhet.** [Transportsikkerhet og herding av nettleseren](/nb/admin/transport-security/)
  går gjennom https, PostgreSQL-TLS, SMTP-TLS, HSTS, vertsfiltrering og
  innholdssikkerhetspolicyen, og hva hvert valg koster.
- **Drift.** [Management-lytteren](/nb/admin/management-listener/) er det private
  statusendepunktet et kontrollplan spør, og måten den første eieren settes inn ved
  invitasjon. [Objektlagring](/nb/admin/object-storage/) beskriver hvor kvitteringer,
  PDF-er og vedlegg ligger.
- **E-faktura.** [E-faktura](/nb/admin/e-invoicing/) setter opp sending av fakturaer som
  EHF i Peppol-nettverket: kontoen hos Storecove og påloggingsdataene, bryterne, de to
  bakgrunnsjobbene som bærer hvert dokument, hva du gjør når en sending er ubekreftet
  eller feilet, og KID-avtalen du ber banken om.
- **Innbetalinger.** [Innbetalinger fra banken](/nb/admin/payments/) er bankavtalen som
  gir deg OCR-giro- eller camt.054-filer med innbetalinger, hvor hver bank lar deg laste
  dem ned, og formatet hver kontos filer importeres i.
- **Ansatte.** [Ansatte](/nb/admin/staff/) slår på registeret over de ansatte, gir folk
  tilgang til det med de fem rettighetene, og setter ansattnummerserien.
  Forretningstidssonen hver modul dømmer en dag etter, er arbeidsområdets, satt under
  [Administrasjon av arbeidsområdet](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet).
- **Ordre.** [Ordre](/nb/admin/orders/) er på når `orders` står i `MODULES` ved siden av
  `products`, `customers` og `invoices`, som den krever; de ni rettighetene,
  nummerseriene, tilbudenes gyldighet, betalingsbetingelser, avsenderkanalen,
  e-posttekstene og tilbudsmalene. Koble avgiftskategoriene i Produkter til mva-koder i
  Fakturaer før noen lager tilbud.
- **Produksjon.** [Produksjon](/nb/admin/manufacturing/) er på når `manufacturing` står i
  `MODULES` ved siden av `products` og `inventory`, som den krever; de fem rettighetene,
  der **Se produksjonskostnader** er sensitiv og ikke kan delegeres.
- **Regnskap.** Regnskap er på når `accounting` står i `MODULES` ved siden av `invoices`,
  som den krever. Den gjør ingenting før en med **Forvalte regnskapet**
  (`accounting:close`) har satt opp regnskapet; de fire tillatelsene, der **Forvalte
  regnskapet** ikke kan delegeres og **Eksportere regnskapet** er sensitiv. Hvor regnskapet
  kan lagres og den årlige kontrollen av sikkerhetskopien står under
  [Installasjon](/nb/admin/installation/#regnskapet). Se [Regnskap](/nb/user/accounting/).
- **Kunnskap.** Kunnskap er på når `knowledge` står i `MODULES` (eller `MODULES` ikke er satt);
  den krever ingen annen modul. Ingen har tilgang før du gir en rolle **Bruke Kunnskap**
  (`knowledge:articles`); **Administrere Kunnskap** (`knowledge:manage`) gir tilgang til
  alle sider som ikke er private, og til papirkurven. Se [Kunnskap](/nb/user/knowledge/).
- **Kalender.** [Kalender](/nb/admin/calendar/) er på når `calendar` står i `MODULES`
  (eller `MODULES` ikke er satt), med tre rettigheter — `calendar:access` for å bruke den,
  `calendar:view-colleagues` for å se når kolleger er opptatt og ledige, og `calendar:manage`
  for å velge kanalen invitasjoner sendes fra. Invitasjoner til folk utenfor bedriften
  trenger at kanalen er valgt, et avsenderdomene med SPF, DKIM og DMARC, og — for mottakere utenfor nettverket ditt — den offentlige verten;
  arbeideren `calendar-cancellations` sletter en avlyst avtale når den siste dagen er
  passert. En persons kalenderadresse — strømmen en mobil abonnerer på — står på listen
  til den offentlige verten, med en grense, og med tokenet i stien; arbeideren
  `calendar-subscriptions` henter kalenderne folk abonnerer på fra internett, gjennom
  vakten for utgående adresser.
- **Microsoft Entra ID.** [Microsoft Entra ID](/nb/admin/entra/) kobler Vantigo til
  selskapets leietaker slik at kontoene Ansatte gir, lages der: `entra` i `MODULES` ved
  siden av `staff`, som den krever; en appregistrering med åtte
  applikasjonstillatelser, administratorsamtykke og rollen User Administrator;
  klienthemmeligheter som en eier legger til og roterer; policyen for midlertidig
  tilgangspass; og hva stegene for en som slutter og en sletting gjør i leietakeren.
  Ingenting av det står i `vantigo.env`; siden ligger under **Integrasjoner** i
  Administrer arbeidsområde.
- **Google Workspace.** [Google Workspace](/nb/admin/google-workspace/) kobler Vantigo
  til selskapets Google Workspace-organisasjon gjennom en tjenestekonto med
  domenedekkende delegering: `google` i `MODULES` ved siden av `staff`, som den krever;
  et Google Cloud-prosjekt med Admin SDK API, tjenestekontoens JSON-nøkkel som en eier
  laster opp og roterer, tilgangsområdene delegert i administrasjonskonsollen,
  administratoren som etterlignes, og den interne nettklienten. Ansatte holder kontoene og
  gruppene der, hver person kobler til sin egen Google-konto for kalendersynkronisering og
  sending fra Gmail-adressen sin, og kanalene i Kommunikasjon kan bruke lisensierte
  Gmail-postbokser. Ingenting av det står i
  `vantigo.env`; siden ligger under **Integrasjoner** i Administrer arbeidsområde.
- **Virtuelle ansatte.** [Virtuelle ansatte](/nb/admin/agents/) er på når `agents` står i
  `MODULES` ved siden av `communications`, som den krever, og en KI-leverandør med en modell
  som støtter verktøykall er satt opp; de fire tillatelsene, firmanavnet og oppbevaringen,
  modellen per agent, Identitet-rollene og nivåene Eieren gir en agent (Av til de heves, med
  tillatelsene som er låst for alle agenter), hva som bør holdes utenfor Kunnskap og
  personvernerklæringen. I denne utgivelsen settes agenter opp og forberedes; de svarer
  ikke ennå.
- **Meta.** [Meta](/nb/admin/meta/) kobler Vantigo til Facebook Messenger gjennom din egen
  Meta-app: `meta` i `MODULES` ved siden av `communications`, som den krever; en offentlig
  vert for Metas webhook; appen opprettet i Metas utviklerportal, med App Review og
  Business Verification der Meta ber om det; app-ID, hemmelighet, verifiseringstoken og
  innstillingen for Human Agent som en eier skriver inn; og hver Facebook-side lagt til med
  et token som ikke utløper. Ingenting av det står i `vantigo.env`; siden ligger under
  **Integrasjoner** i Administrer arbeidsområde.
- **Frakt.** [Frakt](/nb/admin/shipping/) setter opp fraktkontoene Ordre bestiller sendinger
  med: Mybring-brukeren og API-nøkkelen hos Bring — med økonomirollen for priser —,
  kundenumrene, testmodus, standardkontoen per lagersted, fraktproduktet og påslaget.
  `SHIPPING_CARRIERS` i `vantigo.env` holder tilbake en transportør; kontoene ligger under
  **Integrasjoner** i Administrer arbeidsområde.

## Konfigurasjon

[Konfigurasjon](/nb/admin/configuration/) forklarer de to nivåene hver innstilling
hører til — installasjonens, i miljøet, som du eier, og arbeidsområdets, i databasen,
som leietakerens eier redigerer under Administrer arbeidsområde — lister installasjonens
innstillinger etter hva de avgjør, og sier hvor hver av arbeidsområdets redigeres.

## Den komplette konfigurasjonsreferansen

Hver innstilling applikasjonen leser er deklarert ett sted,
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
som validerer hele miljøet ved oppstart og rapporterer alle problemer samtidig.
Eksempelfilen i
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lister dem med standardverdiene sine.
