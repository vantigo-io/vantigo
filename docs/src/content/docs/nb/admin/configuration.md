---
title: Konfigurasjon
description: De to nivåene hver innstilling hører til — installasjonens, som Vantigo eller driftsansvarlig eier og som ligger i miljøet, og arbeidsområdets, som leietakeren eier og en eier redigerer under Administrer arbeidsområde — regelen om at en innstilling bor på ett nivå, hver innstilling på installasjonsnivå gruppert (prosess og sikkerhet, e-post, lagring, arbeidere, delte tjenester) med standardverdi og betydning, og for hver innstilling på arbeidsområdenivå siden som redigerer den.
sidebar:
  order: 11
sources:
  - apps/server/internal/config
---

Hver innstilling Vantigo har, hører til ett av to nivåer, avgjort av **hvem som eier
den**, og skjermbildene følger etter. Denne siden sier hva som er hva, lister
installasjonens innstillinger, og sier hvor hver av arbeidsområdets redigeres.

| Nivå | Eier | Hvor den bor | Redigeres av | Eksempler |
| --- | --- | --- | --- | --- |
| **Installasjon** | Vantigo, eller driftsansvarlig som kjører installasjonen | Miljøet (`vantigo.env`), validert når prosessen starter | Ingen i grensesnittet: driftsansvarlig endrer miljøet og starter på nytt | `DATABASE_URL`, `APP_SECRET`, `APP_URL`, `MODULES`, `STORAGE_*`, `MAIL_DRIVER` og `SMTP_*`, `TRUSTED_PROXY_*`, hver arbeiders poll, `ENERGY_SPOT_PRICE_SERVICE_*` |
| **Arbeidsområde** | Leietakeren — selskapet som bruker installasjonen | Databasen | En eier, eller den som har rettigheten, under **Administrer arbeidsområde** eller i modulens egne innstillinger | Single sign-on, tilkoblingen til Microsoft Entra ID og reglene for den, kanalene i Kommunikasjon, hver moduls innstillinger |

**En innstilling bor på ett nivå**, så det er ingen presedensregel å lære: ingen
innstilling på arbeidsområdenivå leses fra miljøet, og ingen innstilling på
installasjonsnivå tilbys for redigering i grensesnittet. Det installasjonen gjør
mulig, avgrenser det arbeidsområdet kan velge, og grensesnittet kan *vise* et faktum
fra installasjonen som angår en side — som en skrivebeskyttet linje, aldri et felt. En
hemmelighet — en nøkkel, et passord, et token — vises aldri i noen form: siden sier bare
at en er satt.

Det eneste overlappet i historien er en engangsimport: variablene `OIDC_*` og `SCIM_*`
ble lest én gang, inn i arbeidsområdets innstillinger for single sign-on, og blir
ignorert siden
([OIDC og SCIM for arbeidsstyrken](/nb/admin/authentication/#oidc-og-scim-for-arbeidsstyrken)).

## Installasjonens innstillinger

Hver innstilling applikasjonen leser er deklarert i
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
som validerer hele miljøet ved oppstart og rapporterer alle problemer samtidig.
Eksempelfilen
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lister dem med standardverdier og kommentarer. Her står de etter hva de avgjør;
veiledningene det lenkes til fra hver gruppe, sier hvordan du velger.

### Prosess og sikkerhet

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `APP_ENV` | `production` | `development` letter noen få kontroller; ingenting annet gjør det |
| `APP_URL` | påkrevd | Den offentlige opprinnelsen: lenker i e-post, godtatte `Host`-verdier, cookiens `Secure`-attributt og omdirigerings-URI-en for Outlook-tilkoblingen utledes av den |
| `APP_BASE_PATH` | tom | Et stiprefiks på et delt domene |
| `PUBLIC_BASE_URL` | ikke satt | `https`-adressen betalingssiden, kalenderens svarside og Microsofts endringsvarsler nås på fra internett |
| `PORT` | `8080` | Porten det lyttes på |
| `DATABASE_URL`, `MIGRATIONS_DATABASE_URL` | påkrevd; den andre er som den første | Kjøretidsrollens tilkobling og eierrollen migreringene bruker |
| `MODULES` | alle modulene binæren kjenner | Forretningsmodulene denne installasjonen betjener |
| `WORKERS_IN_PROCESS` | `1` | Om API-prosessen også kjører bakgrunnsarbeiderne |
| `SHUTDOWN_TIMEOUT`, `LOG_LEVEL` | `30s`, `info` | Hvor lenge et stopp tømmer pågående arbeid; loggens detaljnivå |
| `APP_SECRET` | påkrevd | Nøkkelmaterialet for hele prosessen: cookies, CSRF, forseglede hemmeligheter |
| `OWNERS_REQUIRE_MFA`, `OWNERS_ALLOW_INSECURE_NO_MFA`, `MFA_ISSUER` | på utenfor utvikling, `0`, `Vantigo` | Om eiere og systemadministratorer trenger en andre faktor, bryteren som lar prosessen starte uten, og merkelappen i autentiseringsappen |
| `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_LIFETIME` | `8h`, `24h` | Hvor lenge en vanlig økt kan stå ubrukt og leve |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT`, `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | `2h`, `8h` | Det samme for en eier eller systemadministrator |
| `INVITATION_LIFETIME`, `INVITATION_ACCEPT_URL`, `PASSWORD_RESET_URL` | `168h`, utledet, utledet | Hvor lenge en invitasjon varer, og lenkene som sendes i invitasjoner og passordtilbakestillinger |
| `BOOTSTRAP_OWNER_EMAIL` | ikke satt | Stenger `/setup` og setter den første eieren inn med en invitasjon på e-post |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` | ikke satt | Katalogen en tokenfil for arbeidslastidentitet som er oppgitt på kortet **Single sign-on**, må ligge i |
| `TRUSTED_PROXY_HOPS`, `TRUSTED_PROXY_CIDRS` | `0`, ikke satt | Mellomtjenerne foran, hvis videresendte adresser det stoles på |
| `CSP_REPORT_ONLY` | `0` | Send innholdssikkerhetspolicyen som bare rapportering |
| `MANAGEMENT_PORT`, `MANAGEMENT_TOKEN` | ikke satt | Den private lytteren for kontrollplanet og dens bærertoken |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | ikke satt | Profileringen applikasjonen viser |

Se [Installasjon](/nb/admin/installation/), [Identitet, autentisering og
utrulling](/nb/admin/authentication/), [Transportsikkerhet](/nb/admin/transport-security/)
og [administrasjonslytteren](/nb/admin/management-listener/).

### E-post

**Systempostboksen**: avsenderen Vantigo selv skriver fra.

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `MAIL_DRIVER` | `log` i utvikling, ellers `smtp` | `smtp`, eller `log` (bare utvikling: ingenting sendes) |
| `SMTP_HOST`, `SMTP_PORT` | påkrevd for `smtp`, `587` | SMTP-tjeneren |
| `SMTP_FROM` | påkrevd for `smtp` | Avsenderadressen; returpost går hit |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | ikke satt | Valgfri legitimasjon |
| `SMTP_TLS` | `starttls` | `starttls`, `implicit` eller `none` |

Den skriver bare til folkene som arbeider i installasjonen: invitasjoner og gjenoppretting
av passord, Ansattes varsler og Kalenders varsler til kolleger. Ingenting som skriver til
*kunder* bruker den. Postboksene et selskap sender fra i eget navn, hører til
arbeidsområdet: [kanalene i Kommunikasjon](/nb/user/communications/#sett-opp-en-kanal),
med en Microsoft 365-kanal satt opp under
[Microsoft Entra ID](/nb/admin/entra/#e-postkanaler). Fakturaers dokumentutsendelser og
purrebrev går fra kanalen som er valgt i fakturainnstillingene
([Sende et dokument på e-post](/nb/user/invoices/#sende-et-dokument-på-e-post)),
Kalenders invitasjoner til folk utenfor bedriften fra den som er valgt i
kalenderinnstillingene ([Inviter folk utenfor bedriften](/nb/user/calendar/#inviter-folk-utenfor-bedriften)),
og en SMTP-kanal uten egen legitimasjon sender ingenting.

### Lagring

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `STORAGE_PROVIDER` | ikke satt | `fs`, eller ikke satt: ingenting som trenger objektlageret virker, og sier fra om det |
| `STORAGE_FS_ROOT` | ikke satt | `fs`-driverens absolutte rotkatalog |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | `0` | Letter kontrollen av rotens tillatelser (bare utvikling) |

Se [Objektlagring](/nb/admin/object-storage/).

### Arbeidere

Hver bakgrunnsarbeider har en av/på-bryter og, der den ser etter på en tidsplan, en poll.
Alle er på som standard unntatt arbeideren for spotpriser.

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `STAFF_ACCOUNTS_ENABLED`, `STAFF_RETENTION_ENABLED`, `STAFF_COMPETENCE_ENABLED` | `1` | Arbeiderne i Ansatte for kontoer, oppbevaring og kompetanse |
| `STAFF_WORKERS_POLL`, `STAFF_IMPORT_MAX_ROWS` | `1h`, fra `5000` til `20000` | Hvor ofte de ser etter; personene én import tar og én eksport skriver |
| `CALENDAR_CANCELLATIONS_ENABLED`, `CALENDAR_WORKERS_POLL` | `1`, `1h` | Sletting av avlyste avtaler |
| `CALENDAR_SUBSCRIPTIONS_ENABLED`, `CALENDAR_SUBSCRIPTIONS_POLL` | `1`, `30m` | Henting av kalenderne folk abonnerer på |
| `CALENDAR_MAILBOX_SYNC_ENABLED`, `CALENDAR_MAILBOX_SYNC_POLL`, `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` | `1`, `15m`, `1` | Å holde kopiene i folks Outlook, og å be Microsoft si fra til Vantigo om endringer |
| `CUSTOMERS_IMPORT_ENABLED`, `CUSTOMERS_IMPORT_POLL`, `CUSTOMERS_IMPORT_RETENTION` | `1`, `5s`, `720h` | Importarbeideren for kunder og hvor lenge en ferdig import beholdes |
| `CUSTOMERS_REGISTRY_FEED_ENABLED`, `CUSTOMERS_REGISTRY_FEED_POLL` | `1`, `15m` | Å følge endringene i foretaksregisteret |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED`, `CUSTOMERS_PEPPOL_RECHECK_POLL`, `CUSTOMERS_PEPPOL_RECHECK_AGE` | `1`, `24h`, `720h` | Ny kontroll av kunders evne til å motta e-faktura |
| `CUSTOMERS_ANONYMISATION_ENABLED`, `CUSTOMERS_ANONYMISATION_POLL` | `1`, `24h` | Arbeideren som anonymiserer kunder |
| `ENERGY_IMPORT_ENABLED`, `ENERGY_IMPORT_POLL`, `ENERGY_IMPORT_RETENTION` | `1`, `5s`, `720h` | Importarbeideren for målepunkter |
| `ENERGY_SPOT_PRICE_FEED_ENABLED`, `ENERGY_SPOT_PRICE_FEED_POLL`, `ENERGY_SPOT_PRICE_FEED_URL` | `0`, `15m`, den offentlige strømmen | Arbeideren for spotpriser og kilden dens |
| `COMMUNICATIONS_RETENTION_DAYS`, `COMMUNICATIONS_RETENTION_BATCH_SIZE`, `COMMUNICATIONS_RETENTION_POLL` | `365`, `100`, `1h` | Hvor gammel avsluttet historikk må være før den slettes, og hvor raskt |
| `COMMUNICATIONS_ATTACHMENT_MAX_BYTES`, `COMMUNICATIONS_INBOUND_MAX_BYTES`, `COMMUNICATIONS_UPLOAD_EXPIRY` | 10 MiB, 25 MiB, `24h` | Kommunikasjons størrelsesgrenser og hvor lenge et klargjort vedlegg er gyldig |

### Delte tjenester og eksterne systemer

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `BRREG_BASE_URL`, `BRREG_TIMEOUT` | `https://data.brreg.no`, `15s` | Oppslaget i foretaksregisteret |
| `PEPPOL_LOOKUP_ENABLED`, `PEPPOL_SML_ZONE`, `PEPPOL_DNS_SERVER`, `PEPPOL_TIMEOUT` | `1`, produksjonssonen, ikke satt, `10s` | Oppslaget av Peppol-evne |
| `INVOICES_EHF_ENABLED`, `INVOICES_STORECOVE_BASE_URL` | `1`, Storecoves API | Å sende fakturaer som EHF ([E-faktura](/nb/admin/e-invoicing/)) |
| `INVOICES_VIPPS_ENABLED`, `INVOICES_VIPPS_BASE_URL` | `1`, Vipps' API | Betaling med Vipps MobilePay ([Betalinger](/nb/admin/payments/)) |
| `ENERGY_SPOT_PRICE_SERVICE_URL`, `ENERGY_SPOT_PRICE_SERVICE_KEY`, `ENERGY_SPOT_PRICE_HISTORY_FROM` | ikke satt | Den delte spotpristjenesten Vantigo setter en installasjon opp med, bærernøkkelen til den (en hemmelighet, vist ingen steder) og eldste dag som kopieres |
| `COMMUNICATIONS_AI_ENABLED`, `COMMUNICATIONS_AI_PROVIDER`, `COMMUNICATIONS_AI_MODEL`, `COMMUNICATIONS_AI_API_KEY` | av, `openai`, `gpt-4o-mini`, ikke satt | KI-utkastet og kundeforslaget i Kommunikasjon; ikke satt svarer begge at funksjonen ikke er tilgjengelig. Nøkkelen er en hemmelighet |

## Arbeidsområdets innstillinger, og hvor hver redigeres

| Innstilling | Redigeres på | Hvem |
| --- | --- | --- |
| Single sign-on og SCIM-klargjøring | **Administrer arbeidsområde** › **Oversikt** › kortet **Single sign-on** ([veiledning](/nb/user/workspace-administration/#sett-opp-single-sign-on)) | en eier |
| Arbeidsområdets egne innstillinger, som forretningstidssonen | **Administrer arbeidsområde** › **Oversikt** › **Innstillinger for arbeidsområdet** ([veiledning](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet)) | en eier |
| Roller, tildelinger og delegeringer | **Administrer arbeidsområde** › **Roller og tilgang** | en eier, eller den rolleadministrasjonen er delegert til |
| Tilkoblingen til Microsoft Entra ID: leietaker, applikasjon, klienthemmeligheter og tillatelser | **Administrer arbeidsområde** › **Integrasjoner** › **Microsoft Entra ID** › **Tilkobling** ([veiledning](/nb/user/entra/#hvor-den-er-og-de-fire-fanene)) | en eier |
| Kontoreglene, innlogging og klargjøring, gruppene Vantigo holder i leietakeren | **Integrasjoner** › **Microsoft Entra ID** › **Kontoer** | `entra:manage` (innlogging og klargjøring: en eier) |
| Outlook-tilkoblingen, kalendersynkronisering på den, og e-postkanaler | **Integrasjoner** › **Microsoft Entra ID** › **Outlook og e-post** | `entra:manage` |
| Postboksene Kommunikasjon sender fra, og SMTP-kontoene den bruker | **Kommunikasjon** › **Kanaler** | `communications:channels-manage` |
| En moduls egne regler: Fakturainnstillinger, Ansatteinnstillinger, Bemanningsinnstillinger, Timeinnstillinger, Utleggsinnstillinger | Modulens innstillingsside i sidemenyen | modulens rettighet |

*Integrasjoner* er en seksjon i Administrer arbeidsområde: stedet et selskaps
tilkoblinger til systemer andre steder styres. En persons egne tilkoblinger er den andre
siden av samme linje, under **Innstillinger** › **Tilkoblinger**
([Tilkoblinger](/nb/user/connections/)).
