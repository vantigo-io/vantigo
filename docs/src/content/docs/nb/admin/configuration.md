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
| **Installasjon** | Vantigo, eller driftsansvarlig som kjører installasjonen | Miljøet (`vantigo.env`), validert når prosessen starter | Ingen i grensesnittet: driftsansvarlig endrer miljøet og starter på nytt | `DATABASE_URL`, `APP_SECRET`, `APP_URL`, `MODULES`, `STORAGE_*`, `MAIL_DRIVER`, `SMTP_*` og `CLOUDFLARE_EMAIL_*`, `TRUSTED_PROXY_*`, hver arbeiders poll, `ENERGY_SPOT_PRICE_SERVICE_*`, `AI_*` |
| **Arbeidsområde** | Leietakeren — selskapet som bruker installasjonen | Databasen | En eier, eller den som har rettigheten, under **Administrer arbeidsområde** eller i modulens egne innstillinger | Single sign-on, tilkoblingen til Microsoft Entra ID og reglene for den, tilkoblingen til Google Workspace, kanalene i Kommunikasjon, modellen hver KI-oppgave bruker, hver moduls innstillinger |

**En innstilling bor på ett nivå**, så det er ingen presedensregel å lære: ingen
innstilling på arbeidsområdenivå leses fra miljøet, og ingen innstilling på
installasjonsnivå tilbys for redigering i grensesnittet. Det installasjonen gjør
mulig, avgrenser det arbeidsområdet kan velge, og grensesnittet kan *vise* et faktum
fra installasjonen som angår en side — som en skrivebeskyttet linje, aldri et felt. En
hemmelighet — en nøkkel, et passord, et token — vises aldri i noen form: siden sier bare
at en er satt.

Overlappene i historien er tre engangsimporter. Variablene `OIDC_*` og `SCIM_*`
ble lest én gang, inn i arbeidsområdets innstillinger for single sign-on, og blir
ignorert siden
([OIDC og SCIM for arbeidsstyrken](/nb/admin/authentication/#oidc-og-scim-for-arbeidsstyrken)).
`COMMUNICATIONS_AI_MODEL` leses én gang, inn i modellen som er valgt for hver av
Kommunikasjons KI-oppgaver, og blir ignorert siden ([importen](#importen-av-communications_ai_model)).
`APP_TITLE`, `APP_LOGO_URL` og `APP_SUPPORT_*` leses én gang, inn i standardmerkevaren, og blir
ignorert siden ([Merkevare](/nb/admin/branding/)). Kan logoen ikke importeres, forblir importen ufullført og variablene
gjelder til en logo er publisert
([importen](/nb/admin/branding/#engangsimporten-av-app_title-app_logo_url-og-app_support_)).

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
| `MODULES` | alle modulene binæren kjenner | Forretningsmodulene denne installasjonen betjener: `customers`, `products`, `energy`, `communications`, `projects`, `time`, `expenses`, `invoices`, `staff`, `planning`, `inventory`, `orders`, `manufacturing`, `accounting`, `purchasing`, `knowledge`, `calendar`, `entra`, `google`, `assistant` og `agents`. `inventory` krever `products`; `orders` krever `products`, `customers` og `invoices` (`orders requires invoices`, som navngir den som mangler); `manufacturing` krever `products` og `inventory` (`manufacturing requires inventory`); `accounting` krever `invoices` (`accounting requires invoices`); `purchasing` krever `accounting`, `inventory` og `products` (`purchasing requires accounting`, som navngir den som mangler); `entra` og `google` krever hver `staff` (`google requires staff`); `agents` krever `communications` (`agents requires communications`). `assistant` har ingen egne rettigheter og trenger ingen annen modul, men har bare nytte av seg mens `AI_PROVIDER` er satt ([KI](#ki)) |
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
| `LIVE_ENABLED` | `1` | Om `api`- og `server`-modus serverer endepunktet for sanntidsoppdateringer, `GET /api/v1/live`. `0` svarer `404`, og hvert skjermbilde oppdaterer seg omtrent én gang i minuttet i stedet |
| `MCP_ENABLED` | `1` | Om `api`- og `server`-modus serverer MCP-endepunktet, `POST /api/mcp`, som AI-klienter kobler seg til med en integrasjons nøkkel når en eier har slått på MCP for den ([API-tilgang › MCP](/nb/admin/api-access/#mcp)). `0` svarer `404` der for hver nøkkel |
| `OAUTH_ENABLED` | `1` | Om en MCP-klient eller app kan koble seg til ved at en eier logger inn og godkjenner den på samtykkesiden, i stedet for med en nøkkel: OAuth-oppdagelsesdokumentene under `/.well-known/`, token-endepunktet `POST /api/oauth/token`, samtykke- og tilkoblingsoperasjonene og tilgangstokenene de utsteder ([API-tilgang › Koble til ved å logge inn](/nb/admin/api-access/#koble-til-ved-å-logge-inn), [OAuth connections](/en/reference/oauth/)). `0` svarer `404` på hver av dem, samtykkesiden sier at det er slått av, integrasjonens side viser ingen **Tilkoblede apper**, et tilgangstoken avvises med `401`, og tilkoblingene som alt er laget, beholdes. Utstederen er `APP_URL` uten standardport, fulgt av `APP_BASE_PATH` |
| `IDEMPOTENCY_ENABLED` | `1` | Om en opprettende `POST` sendt med en `Idempotency-Key`-header utføres én gang og svaret gis igjen til hvert nytt forsøk med samme nøkkel, i 24 timer ([API-tilgang › Prøv på nytt uten å gjøre noe to ganger](/nb/admin/api-access/#prøv-på-nytt-uten-å-gjøre-noe-to-ganger)); arbeideren `identity.api-keys` sletter de utløpte nøklene hver syklus uansett hva denne sier. `0` ignorerer headeren overalt — operasjonene som har sin egen nøkkel, beholder den — og lagrer ingenting |
| `LIVE_DATABASE_URL` | ikke satt | Databaseadressen for de to tilkoblingene sanntidsoppdateringer holder per HTTP-replika. Ikke satt bruker de `DATABASE_URL`. Pek den på databasens direkteport når `DATABASE_URL` går gjennom en pooler i transaksjonsmodus, der `LISTEN` ikke fungerer. Sladdes når den skrives ut |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | ikke satt | Profileringen applikasjonen viste. **Importeres én gang** inn i standardmerkevaren; settes og redigeres under **Administrer arbeidsområde › Merkevare** fra da av, og ignoreres når importen er fullført ([Merkevare](/nb/admin/branding/#engangsimporten-av-app_title-app_logo_url-og-app_support_)) |

Se [Installasjon](/nb/admin/installation/) (blant annet [Sanntidsoppdateringer og proxyen din](/nb/admin/installation/#sanntidsoppdateringer-og-proxyen-din)), [Identitet, autentisering og
utrulling](/nb/admin/authentication/), [Transportsikkerhet](/nb/admin/transport-security/)
og [administrasjonslytteren](/nb/admin/management-listener/).

### E-post

**Systempostboksen**: avsenderen Vantigo selv skriver fra.

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `MAIL_DRIVER` | `log` i utvikling, ellers `smtp` | `smtp`, `cloudflare` (Cloudflare Email Service), eller `log` (bare utvikling: ingenting sendes) |
| `SMTP_HOST`, `SMTP_PORT` | påkrevd for `smtp`, `587` | SMTP-tjeneren |
| `SMTP_FROM` | påkrevd for `smtp` | Avsenderadressen; returpost går hit |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | ikke satt | Valgfri legitimasjon |
| `SMTP_TLS` | `starttls` | `starttls`, `implicit` eller `none` |
| `CLOUDFLARE_EMAIL_ACCOUNT_ID`, `CLOUDFLARE_EMAIL_API_TOKEN` | påkrevd for `cloudflare` | Cloudflare-kontoen og et API-token som får sende ([Cloudflare Email Service](/nb/admin/authentication/#cloudflare-email-service)) |
| `CLOUDFLARE_EMAIL_FROM` | påkrevd for `cloudflare` | Avsenderadressen, på et domene tilføyd Email Sending |

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
Alle er på som standard unntatt arbeideren for spotpriser. Plattformens
hendelsesutsender, `events.dispatch`, har ingen av delene: den kjører der arbeiderne
kjører, hvert 5. sekund ([Bakgrunnsjobber og skalering](/nb/admin/installation/#bakgrunnsjobber-og-skalering)).

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `IDENTITY_API_KEYS_ENABLED`, `IDENTITY_API_KEYS_POLL` | `1`, `1h` | Identitys arbeider `identity.api-keys`: den registrerer `api_key.expired` én gang for hver integrasjonsnøkkel som passerer utløpet, og sletter en nøkkels rad 90 dager etter at den utløp; revisjonsradene blir værende. Den holder partisjonene til API-forespørselsloggen — flytter det som havnet i DEFAULT-partisjonen, til dagens partisjon, lager partisjonene for i dag og de neste 6 dagene, og sletter det som er eldre enn de 30 dagene som beholdes, i dag medregnet — og sender varslene om utløp 14 dager og 3 dager før en nøkkel utløper og på dagen. Av avvises en utløpt nøkkel likevel, den blir bare ikke revidert eller slettet; ingen partisjon lages, så hver rad i forespørselsloggen havner i DEFAULT og blir der, uten å slettes; og ingen varsler sendes. Slås den på igjen, flytter første syklus DEFAULTs siste 30 dager til partisjonene sine og sletter resten ([API-tilgang](/nb/admin/api-access/#forespørselsloggen)). Med `IDEMPOTENCY_ENABLED` eller `OAUTH_ENABLED` på kjører arbeideren også når denne bryteren er av; når den kjører, sletter den de utløpte `Idempotency-Key`-ene, og OAuth-kodene, -tokenene, -tilkoblingene og -klientdokumentene som har passert tiden de beholdes, hver syklus, hver under en egen lease |
| `IDENTITY_WEBHOOKS_ENABLED`, `IDENTITY_WEBHOOKS_POLL` | `1`, `2s` | Identitys arbeider `identity.webhook-sender`, som slås av og på uavhengig av `identity.api-keys`: ved hver avspørring — og straks igjen så lenge mer er klart — sender den integrasjonenes webhook-meldinger som er klare, høyst 4 underveis per endepunkt og 16 per prosess, prøver en som feiler, på nytt i omtrent 45 timer, deaktiverer et endepunkt som svarer `410 Gone` eller har feilet i 5 dager med minst 5 meldinger gitt opp etter alle nye forsøk, og sender varselet om det, og én gang i minuttet utløper den en melding som fortsatt venter etter 3 dager, og sletter dem som er eldre enn 30 dager. Flere arbeiderprosesser kan kjøre den samtidig. Av legges hendelser fortsatt i kø for endepunktene, men ingenting sendes; slås den på igjen, utløper det som har ventet mer enn 3 dager, og resten sendes ([Webhooks](/en/reference/webhooks/#sending), på engelsk) |
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
| `ACCOUNTING_SAFT_IMPORT_MAX_BYTES` | 512 MiB, fra 1 MiB til 4 GiB | Den største SAF-T-filen Regnskap tar imot for å sette opp regnskapet ([Kommer du fra et annet system?](/nb/user/accounting/#kommer-du-fra-et-annet-system)) |
| `ACCOUNTING_SAFT_EXPORT_RETENTION_DAYS` | `90`, fra `0` til `3650` | Hvor mange dager filene fra en SAF-T-eksport oppbevares før arbeideren sletter dem — de inneholder personopplysninger; `0` beholder dem. Eksportens oppføring og kontrollsum blir stående |
| `ACCOUNTING_SAFT_SPLIT_BYTES` | 2 000 000 000, fra 1024 | Størrelsen der en SAF-T-eksport deles etter periode i filer som hver er gyldige alene; Skatteetaten tar imot filer på opptil 2 GB |
| `PEPPOL_LOOKUP_ENABLED`, `PEPPOL_SML_ZONE`, `PEPPOL_DNS_SERVER`, `PEPPOL_TIMEOUT` | `1`, produksjonssonen, ikke satt, `10s` | Oppslaget av Peppol-evne |
| `INVOICES_EHF_ENABLED`, `INVOICES_STORECOVE_BASE_URL` | `1`, Storecoves API | Å sende fakturaer som EHF ([E-faktura](/nb/admin/e-invoicing/)) |
| `INVOICES_EFAKTURA_ENABLED`, `INVOICES_MPS_EFAKTURA_BASE_URL`, `INVOICES_MPS_MANDATE_BASE_URL` | `0`, MPS' produksjonsendepunkter | eFaktura og AvtaleGiro gjennom Mastercard Payment Services, av til driftsansvarlig slår det på ([eFaktura og AvtaleGiro](/nb/admin/e-invoicing/#efaktura-og-avtalegiro-gjennom-mastercard-payment-services)) |
| `INVOICES_MPS_CLIENT_CERT_FILE`, `INVOICES_MPS_CLIENT_KEY_FILE`, `INVOICES_MPS_USERNAME`, `INVOICES_MPS_PASSWORD_FILE` | ikke satt; påkrevd mens `INVOICES_EFAKTURA_ENABLED` er på | Innsenderens klientsertifikat, nøkkelen, WS-Security-brukeren og filen med passordet — absolutte, lesbare stier |
| `INVOICES_VIPPS_ENABLED`, `INVOICES_VIPPS_BASE_URL` | `1`, Vipps' API | Betaling med Vipps MobilePay ([Betalinger](/nb/admin/payments/)) |
| `INVOICES_BANK_FEED_ENABLED`, `INVOICES_FOLIO_BASE_URL`, `INVOICES_BANK_FEED_INTERVAL` | `1`, `https://api.folio.no/v2`, `30m` | Bankintegrasjonen for innbetalinger og jobben dens, API-et til Folio, bare https, og hvor ofte hver konto leses, `5m` til `24h` ([Bankintegrasjonen](/nb/admin/payments/#bankintegrasjonen)) |
| `INVOICES_RATES_ENABLED`, `INVOICES_RATES_URL` | `1`, `https://data.norges-bank.no/api/data` | Norges Banks valutakurser for fakturaer i en annen valuta og jobben `invoices-rates`, som henter dem hver virkedag etter kl. 16.30 gjennom den beskyttede hentklienten — utgående vert `data.norges-bank.no` må kunne nås; adressen er bare https, uten skråstrek til slutt. Av (`0`) kalles Norges Bank aldri, og et dokument i en annen valuta utstedes bare med en kurs en leder har lagt inn. En henting som feiler, vises på kortet Valutaer i innstillingene og stopper trygt: ingen kurs, ingen utstedelse ([Valutaer og kurser](/nb/user/invoices/#valutaer-og-kurser)) |
| `SHIPPING_CARRIERS`, `SHIPPING_BRING_BASE_URL` | `bring`, `https://api.bring.com` | Transportøradapterne installasjonen tillater, kommaseparert blant de kjente (`bring`); tom holder tilbake alle transportører — ingen fraktkonto kan legges til, bestilles på eller spores. Bring-adapterens grunn-URL, bare https, for en testvert; Brings testmodus er en fraktkontos testflagg, ikke denne ([Frakt](/nb/admin/shipping/)) |
| `ENERGY_SPOT_PRICE_SERVICE_URL`, `ENERGY_SPOT_PRICE_SERVICE_KEY`, `ENERGY_SPOT_PRICE_HISTORY_FROM` | ikke satt | Den delte spotpristjenesten Vantigo setter en installasjon opp med, bærernøkkelen til den (en hemmelighet, vist ingen steder) og eldste dag som kopieres |
| `AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS`, `AI_TIMEOUT`, `AI_CONTEXT_TOKENS`, `AI_EFFORT`, `AI_FALLBACKS` | ikke satt, ikke satt, ikke satt, ikke satt, `120s`, `16000` (`150000` for `anthropic`), `medium`, `on` | KI-abonnementet: leverandøren, nøkkelen (en hemmelighet, vist ingen steder; valgfri for en modell på eget nettverk), et valgfritt kompatibelt endepunkt, modellene abonnementet tillater, hvor lenge en samtale kan ta, hvor mye av en samtale assistenten sender modellen og, for Anthropic, hvor hardt modellen tenker og om en avvist forespørsel prøves på nytt på en reservemodell. Ikke satt er alle KI-funksjoner utilgjengelige ([KI](#ki)) |
| `AGENTS_CONCURRENCY` | `4` | Hvor mange kjøringer for [virtuelle ansatte](/nb/admin/agents/) én API-replika bærer samtidig, fra `1` til `32`; en verdi utenfor det avvises ved oppstart. Hver kjøring er én agent på én samtale, i høyst fem minutter, og holder ett modellkall om gangen; hev den når samtaler venter, og senk den når modellserveren din ikke tåler så mange samtidig. Hver replika har sitt eget antall |

### KI

KI-abonnementet er Vantigos: leverandøren, nøkkelen og modellene det tillater hører til
installasjonen, leses én gang når prosessen starter, og blir bare *vist* i
grensesnittet. Hvilken av de tillatte modellene hver KI-oppgave bruker, er
arbeidsområdets valg.

| Innstilling | Standard | Betydning |
| --- | --- | --- |
| `AI_PROVIDER` | ikke satt | Leverandøren: `openai`, `openai-compatible` for alt som snakker OpenAIs protokoll, for eksempel en modell du kjører selv ([en lokal modell](#en-lokal-modell)), eller `anthropic` for Claude ([Anthropic](#anthropic)). Den vises slik du skrev den. Ikke satt er ingen KI-funksjon tilgjengelig: KI-utkastet og kundeforslaget i Kommunikasjon svarer at KI ikke er tilgjengelig, og **KI-modeller** vises ikke under Integrasjoner |
| `AI_API_KEY` | påkrevd sammen med leverandøren, med mindre `AI_BASE_URL` er satt; alltid påkrevd for `anthropic` | Leverandørens nøkkel, en hemmelighet: den vises ingen steder, i ingen form, og **KI-modeller** sier bare om en er satt. Den kan være tom når `AI_BASE_URL` peker på et endepunkt, siden en modell på eget nettverk ikke trenger noen; da sendes aldri en nøkkel |
| `AI_BASE_URL` | leverandørens egen adresse | En absolutt adresse til et OpenAI-kompatibelt endepunkt, med versjonsstien (`https://api.openai.com/v1`); uten bruker, spørring eller fragment. Med `anthropic` er det adressen til en gateway foran Anthropics API (standarden er `https://api.anthropic.com`), og bare `https`. Ellers `https`, eller `http` når verten er en loopback-adresse, en privat adresse (`10.x`, `172.16`–`172.31`, `192.168.x`, `fc00::/7`) eller et navn uten punktum som `ollama`. En offentlig vert over `http` avvises, og det samme gjelder enhver `http`-adresse mens `AI_API_KEY` er satt: en nøkkel sendes aldri i klartekst |
| `AI_MODELS` | påkrevd sammen med leverandøren | Modellene abonnementet tillater, kommaseparert, der den første er abonnementets standard: `gpt-4o-mini,gpt-4o`. Minst én, ingen gjentatt, hver på opptil 150 tegn uten mellomrom |
| `AI_TIMEOUT` | `120s` | Hvor lenge ett samtalekall kan ta, som en Go-varighet over null og høyst `10m`. Lokale modeller er trege; et kall med én enkelt forespørsel beholder sine egne 30 sekunder |
| `AI_CONTEXT_TOKENS` | `16000`; `150000` for `anthropic` | Hvor mye av en samtale [Assistenten](/nb/user/assistant/) sender modellen, som et antall tokens fra `2000` til `1000000`; en verdi utenfor det området avvises ved oppstart. Sett den til kontekstvinduet til modellen du kjører, eller litt under. Den anslås til omtrent fire tegn per token, så den er en rettesnor og ikke et mål; over den erstatter assistenten først de eldre resultatene fra verktøyene sine med en enlinjes merknad, og dropper så de eldste rundene i samtalen, slik at det siste spørsmålet alltid sendes. Lokale modeller har små vinduer; en hostet modell kan ta langt mer. For `anthropic` forkorter ikke assistenten eldre resultater på stedet; over denne størrelsen [oppsummerer den samtalen](#anthropic) i stedet, og derfor er standarden der `150000`, en god del av modellens vindu |
| `AI_EFFORT` | `medium` | Hvor hardt en tenkende modell tenker: `low`, `medium`, `high`, `xhigh` eller `max`. Brukes av `anthropic` som forespørselens innsats; `openai` og `openai-compatible` ignorerer den. Høyere er tregere og dyrere; senk den først, før du ber modellen tenke mindre i ord |
| `AI_FALLBACKS` | `on` | `on` eller `off`. Med `anthropic` kjøres en forespørsel som Anthropics sikkerhetsfiltre avviser, på nytt på en reservemodell hos Anthropic; `off` slår det av. Modeller uten reservemodell (Haiku) får aldri forespørselen. `openai` og `openai-compatible` ignorerer den |

`AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS` og `AI_TIMEOUT` uten `AI_PROVIDER` avvises ved oppstart, og det
samme gjelder en leverandør uten modell, eller uten verken nøkkel eller basisadresse (`anthropic`: uten nøkkel, eller med en `http`-basisadresse); en verdi i `AI_EFFORT` eller `AI_FALLBACKS` som ikke er en av de oppgitte, avvises uansett leverandør; en leverandør som ikke er satt, er den
vanlige tilstanden «ingen KI» og aldri en feil.

#### Anthropic

`AI_PROVIDER=anthropic` kjører alle KI-oppgaver på Claude, gjennom Anthropics Messages API:
utkast til svar og kundeforslag i Kommunikasjon, og [Assistenten](/nb/user/assistant/) med
strømming, verktøy, bekreftelser, masseendringer og omvisninger. Slik ser det ut i `vantigo.env`:

```
AI_PROVIDER=anthropic
AI_API_KEY=sk-ant-...
AI_MODELS=claude-opus-5-5,claude-sonnet-5-5,claude-haiku-5-5
AI_EFFORT=medium
AI_CONTEXT_TOKENS=150000
```

- **`AI_API_KEY`** er en API-nøkkel fra Anthropic og er alltid påkrevd, også ved siden av
  `AI_BASE_URL`. Den er en hemmelighet, vises ingen steder, og Vantigo leser ingen
  `ANTHROPIC_*`-variabel i prosessen.
- **`AI_MODELS`** tar Claude-modell-id-er nøyaktig slik Anthropic navngir dem. Et godt abonnement
  har en sterk modell til Assistenten (`claude-opus-5-5` eller `claude-sonnet-5-5`) og en liten til
  korte jobber (`claude-haiku-5-5`); en eier velger så én per oppgave under **KI-modeller**. Den
  første er standard. Modell-id-er som Anthropic tar ut av drift slutter å virke: oppdater listen.
- **`AI_EFFORT`** og **`AI_FALLBACKS`** er beskrevet over. Claude-modeller tenker før de svarer, og
  Vantigo overlater det til modellen; innsatsen er den ene knappen.
- **Vantigos klient prøver ikke kall på nytt.** En grense for forespørsler eller en overbelastning
  når personen som «KI-tjenesten svarte ikke»; personen spør på nytt.
- **Assistenten holder starten av hver samtale uendret**, slik at Claudes tenking forblir gyldig og
  Anthropics promptbuffer brukes: instruksjonene låses når samtalen begynner, og når den vokser forbi
  `AI_CONTEXT_TOKENS` oppsummeres den én gang og fortsetter fra oppsummeringen. Oppsummeringen koster
  ett ekstra kall til samme modell.

**Hvor dataene går.** Spørsmål til Assistenten, dokumentasjonen den finner, dataene den leser
gjennom API-et og teksten i meldingene Kommunikasjon utarbeider svar på, sendes til Anthropic,
utenfor installasjonen din. Innstillingssiden til Assistenten sier det til eieren
(**Administrasjon av arbeidsområdet** › **Assistent**). Før du slår den på, sørg for at avtalen din
med Anthropic, inkludert databehandleravtalen, dekker disse dataene, og si fra til kundene dine i
personvernerklæringen som for enhver annen databehandler. Anthropic er aldri en lokal leverandør,
uansett hva `AI_BASE_URL` er.

#### En lokal modell

Spørsmålene som stilles til KI-en, og dataene den leser for å svare, går til leverandøren du
setter opp. Med en modell som kjører på ditt eget nettverk blir de på installasjonen: ingenting
forlater den. Enhver tjener som snakker OpenAIs samtaleprotokoll fungerer — Ollama, vLLM,
LM Studio. Slik ser en Ollama-tjener ved siden av Vantigo ut i `vantigo.env`:

```
AI_PROVIDER=openai-compatible
AI_BASE_URL=http://ollama:11434/v1
AI_MODELS=qwen3:14b
AI_TIMEOUT=180s
```

Det er ingen `AI_API_KEY`, og ingen sendes. `http://ollama:11434/v1` godtas fordi `ollama` er et
navn uten punktum, som bare kan slås opp på ditt eget nettverk; den samme adressen med et
offentlig vertsnavn avvises når prosessen starter, med en melding som nevner `AI_BASE_URL`.
Modellen må støtte **verktøykall** (en modell med funksjonskall: for Ollama en som er merket
*tools* i biblioteket), og `AI_MODELS` må nevne den nøyaktig slik tjeneren gjør. Hent modellen
til tjeneren først. Velg `AI_TIMEOUT` etter det tregeste svaret du vil godta; en liten modell på
en prosessor kan trenge minutter.

Hver KI-jobb får fortsatt en modell per oppgave under **KI-modeller**, blant dem `AI_MODELS`
lister.

#### Assistentens modell

[Assistenten](/nb/user/assistant/) er den ene KI-funksjonen som fører en samtale og lar modellen be
om verktøy: den søker i dokumentasjonen og leser data gjennom API-et, som den som spør, ved å
returnere verktøykall. **Den trenger en modell som støtter verktøykall** (funksjonskall). En modell
som ikke gjør det, blir enten avvist av tjeneren når assistenten spør den, eller svarer uten
dokumentasjonen og personens data og blir dårligere av det; prøv modellen du velger ved å stille
assistenten et hvordan-spørsmål og et spørsmål om data. Assistenten er oppgaven **Assistant chat**
(`assistant.chat`), og en eier velger modellen under **KI-modeller** som for alle andre. Hvert svar
kan ta flere kall til modellen — den leser, så svarer den — så `AI_TIMEOUT` er grensen for hvert
kall, ikke for hele svaret. `AI_CONTEXT_TOKENS` begrenser hva hvert kall sendes. Spørsmålene,
dokumentasjonen den finner og dataene den leser går til leverandøren; en eier ser hvor under
**Administrer arbeidsområde** › **Assistent**
([veiledning](/nb/user/workspace-administration/#assistent)). Assistenten er tilgjengelig for ingen
mens `AI_PROVIDER` ikke er satt.

Hver KI-jobb en modul utfører, er en **oppgave** som modulen deklarerer, som *Draft a
reply* eller *Suggest the customer* i Kommunikasjon (oppgavenavnene vises på engelsk). En eier velger modellen hver oppgave
bruker blant `AI_MODELS`, under **Administrer arbeidsområde** › **Integrasjoner** ›
**KI-modeller** ([veiledning](/nb/user/workspace-administration/#ki-modeller)). En oppgave
ingen har valgt for, bruker modellen modulen anbefaler hvis abonnementet tillater den, og
ellers den første i `AI_MODELS`. En ny `AI_MODELS` gjelder når prosessen starter på nytt; et
valg av en modell listen ikke lenger har, blir ignorert, og oppgaven faller tilbake som over,
til en eier velger på nytt. Oppgavene deklareres av modulene, i koden: en modul som er av, har
ingen, og en oppgave kommer sammen med funksjonen som utfører den.

En oppgave kan også deklareres **per emne**: modellen kan da velges for ett emne, som én
virtuell ansatt (agent), i tillegg til oppgavens egen modell. Emnets modell kommer først, så
oppgavens, så modulens anbefaling og den første i `AI_MODELS`; en modell abonnementet ikke
lenger tillater, blir ignorert på samme måte. Valget for et emne gjøres der emnet administreres,
ikke under **KI-modeller**, som fortsatt viser oppgavens eget valg.

#### De omdøpte variablene

Kommunikasjons fire KI-variabler ble omdøpt da KI ble en plattforminnstilling. De gamle
navnene leses som alias i én utgivelse til, og prosessen nevner dem du fortsatt har satt,
i én advarsel ved hver oppstart.

| Var | Er | Slik leses den gamle |
| --- | --- | --- |
| `COMMUNICATIONS_AI_ENABLED=1` | `AI_PROVIDER` | Slår leverandøren på når `AI_PROVIDER` ikke er satt |
| `COMMUNICATIONS_AI_PROVIDER` | `AI_PROVIDER` | Leverandøren (`openai` når den ikke er satt); en annen verdi gjør KI utilgjengelig, som før |
| `COMMUNICATIONS_AI_API_KEY` | `AI_API_KEY` | Nøkkelen, når `AI_API_KEY` ikke er satt; slått på uten nøkkel gjør KI utilgjengelig, som før |
| `COMMUNICATIONS_AI_MODEL` | `AI_MODELS` | Så lenge leverandøren kommer fra de gamle variablene og `AI_MODELS` ikke er satt, den ene modellen abonnementet tillater (`gpt-4o-mini` når den ikke er satt); og importert én gang, under |

En `AI_*`-variabel som er satt, vinner alltid, og den gamle ved siden av blir nevnt som
ignorert. Sett de nye navnene og fjern de gamle før neste utgivelse, som slutter å lese dem.

#### Importen av `COMMUNICATIONS_AI_MODEL`

Kommunikasjon brukte én modell til begge KI-operasjonene sine. Ved første oppstart av API-et
som ikke finner noen modell valgt for noen oppgave, mens `COMMUNICATIONS_AI_MODEL` er satt,
et KI-abonnement er satt opp og abonnementet tillater modellen, skrives modellen som valget
for hver av Kommunikasjons oppgaver, revisjonslogges som `ai-task-models.imported` uten
aktør, og loggen advarer om at variabelen nå blir ignorert og bør fjernes. Oppførselen
endres ikke: begge operasjonene bruker den modellen til en eier velger en annen.

- En modell `AI_MODELS` ikke har, blir **ikke** importert, og loggen sier fra om det; en eier
  velger under **KI-modeller**.
- Uten KI-abonnement, eller med Kommunikasjon av, importeres ingenting og ingenting sies:
  variabelen venter på at det ene eller det andre kommer.
- Så snart en oppgave har et valg, blir variabelen ignorert ved hver oppstart, med en
  advarsel som nevner den til du fjerner den. Den leses aldri igjen, så en endret verdi
  endrer ingenting.

## Arbeidsområdets innstillinger, og hvor hver redigeres

| Innstilling | Redigeres på | Hvem |
| --- | --- | --- |
| Single sign-on og SCIM-klargjøring | **Administrer arbeidsområde** › **Oversikt** › kortet **Single sign-on** ([veiledning](/nb/user/workspace-administration/#sett-opp-single-sign-on)) | en eier |
| Arbeidsområdets egne innstillinger, som forretningstidssonen | **Administrer arbeidsområde** › **Oversikt** › **Innstillinger for arbeidsområdet** ([veiledning](/nb/user/workspace-administration/#innstillinger-for-arbeidsområdet)) | en eier |
| Roller, tildelinger og delegeringer | **Administrer arbeidsområde** › **Roller og tilgang** | en eier, eller den rolleadministrasjonen er delegert til |
| Tilkoblingen til Microsoft Entra ID: leietaker, applikasjon, klienthemmeligheter og tillatelser | **Administrer arbeidsområde** › **Integrasjoner** › **Microsoft Entra ID** › **Tilkobling** ([veiledning](/nb/user/entra/#hvor-den-er-og-de-fire-fanene)) | en eier |
| Kontoreglene, innlogging og klargjøring, gruppene Vantigo holder i leietakeren | **Integrasjoner** › **Microsoft Entra ID** › **Kontoer** | `entra:manage` (innlogging og klargjøring: en eier) |
| Outlook-tilkoblingen, kalendersynkronisering på den, og e-postkanaler | **Integrasjoner** › **Microsoft Entra ID** › **Outlook og e-post** | `entra:manage` |
| Tilkoblingen til Google Workspace: tjenestekontoens nøkler, administratoren som etterlignes, den interne nettklienten og testen | **Administrer arbeidsområde** › **Integrasjoner** › **Google Workspace** › **Tilkobling** ([veiledning](/nb/user/google-workspace/#hvor-den-er-og-de-fire-fanene)) | en eier (`google:manage` leser den) |
| Meta-appen, webhookens verifiseringstoken, innstillingen for Human Agent og Facebook-sidene | **Administrer arbeidsområde** › **Integrasjoner** › **Meta** ([veiledning](/nb/admin/meta/)) | en eier |
| Modellen hver KI-oppgave bruker, blant modellene abonnementet tillater | **Administrer arbeidsområde** › **Integrasjoner** › **KI-modeller** ([veiledning](/nb/user/workspace-administration/#ki-modeller)) | en eier |
| Hvem som har assistenten, hvor lenge samtaler beholdes og hva assistenten kan bruke | **Administrer arbeidsområde** › **Integrasjoner** › **Assistent** ([veiledning](/nb/user/workspace-administration/#assistent)); hver persons egen bryter under **Brukere** › **Rediger detaljer** | en eier |
| Postboksene Kommunikasjon sender fra, og SMTP-kontoene den bruker | **Kommunikasjon** › **Kanaler** | `communications:channels-manage` |
| En moduls egne regler: Fakturainnstillinger, Ansatteinnstillinger, Bemanningsinnstillinger, Timeinnstillinger, Utleggsinnstillinger | Modulens innstillingsside i sidemenyen | modulens rettighet |

*Integrasjoner* er en seksjon i Administrer arbeidsområde: stedet et selskaps
tilkoblinger til systemer andre steder styres. En persons egne tilkoblinger er den andre
siden av samme linje, under **Innstillinger** › **Tilkoblinger**
([Tilkoblinger](/nb/user/connections/)).
