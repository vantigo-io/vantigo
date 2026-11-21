---
title: Configuration
description: The two tiers every setting belongs to — the instance's, which Vantigo or the operator owns and which lives in the environment, and the workspace's, which the tenant owns and an Owner edits under Workspace admin — the rule that a setting lives in exactly one, every instance setting grouped (process and security, mail, storage, workers, shared services) with its default and meaning, and for each workspace setting the page that edits it.
sidebar:
  order: 11
sources:
  - apps/server/internal/config
---

Every setting Vantigo has belongs to one of two tiers, decided by **who owns it**, and
the screens follow. This page says which is which, lists the instance's settings, and
says where each of the workspace's is edited.

| Tier | Owner | Where it lives | Edited by | Examples |
| --- | --- | --- | --- | --- |
| **Instance** | Vantigo, or the operator who runs the installation | The environment (`vantigo.env`), validated when the process starts | Nobody in the interface: the operator changes the environment and restarts | `DATABASE_URL`, `APP_SECRET`, `APP_URL`, `MODULES`, `STORAGE_*`, `MAIL_DRIVER`, `SMTP_*` and `CLOUDFLARE_EMAIL_*`, `TRUSTED_PROXY_*`, every worker's poll, `ENERGY_SPOT_PRICE_SERVICE_*`, `AI_*` |
| **Workspace** | The tenant — the company that uses the installation | The database | An Owner, or whoever holds the permission, under **Workspace admin** or in the module's own settings | Single sign-on, the Microsoft Entra ID connection and its rules, the Google Workspace connection, Communications' channels, the model each AI task uses, each module's settings |

**A setting lives in exactly one tier**, so there is no precedence rule to learn: no
workspace setting is read from the environment, and no instance setting is offered for
editing in the interface. What the instance makes possible bounds what the workspace
may choose, and the interface may *show* an instance fact that bears on a page — as
a read-only line, never a field. A secret — a key, a password, a token — is never
shown at all, in any form: the page says only that one is set.

The overlaps in history are three one-time imports. The `OIDC_*` and `SCIM_*` variables
were read once, into the workspace's single sign-on settings, and are ignored since
([Workforce OIDC and SCIM](/en/admin/authentication/#workforce-oidc-and-scim)).
`COMMUNICATIONS_AI_MODEL` is read once, into the model chosen for each of Communications'
AI tasks, and is ignored since ([the import](#the-import-of-communications_ai_model)). `APP_TITLE`, `APP_LOGO_URL` and `APP_SUPPORT_*`
are read once, into the default brand, and are ignored since ([Branding](/en/admin/branding/)). If the logo cannot be
imported, the import stays incomplete and the variables stay in effect until a logo is published
([the import](/en/admin/branding/#the-one-time-import-of-app_title-app_logo_url-and-app_support_)).

## The instance's settings

Every setting the application reads is declared in
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
which validates the whole environment at start and reports every problem at once. The
example file
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lists them with their defaults and their comments. Here they are by what they decide;
the guides linked from each group say how to choose.

### Process and security

| Setting | Default | Meaning |
| --- | --- | --- |
| `APP_ENV` | `production` | `development` relaxes a few checks; nothing else does |
| `APP_URL` | required | The public origin: mailed links, accepted `Host` values, the cookie `Secure` attribute and the Outlook connection's redirect URI derive from it |
| `APP_BASE_PATH` | empty | A path prefix on a shared domain |
| `PUBLIC_BASE_URL` | unset | The `https` address the pay page, the calendar answer page and Microsoft's change notifications are reached at from the internet |
| `PORT` | `8080` | The listen port |
| `DATABASE_URL`, `MIGRATIONS_DATABASE_URL` | required; the second defaults to the first | The runtime role's connection and the owner role the migrations use |
| `MODULES` | every module the binary knows | The business modules this installation serves: `customers`, `products`, `energy`, `communications`, `projects`, `time`, `expenses`, `invoices`, `staff`, `planning`, `inventory`, `orders`, `manufacturing`, `accounting`, `purchasing`, `knowledge`, `calendar`, `entra`, `google`, `assistant` and `agents`. `inventory` requires `products`; `orders` requires `products`, `customers` and `invoices` (`orders requires invoices`, naming the one missing); `manufacturing` requires `products` and `inventory` (`manufacturing requires inventory`); `accounting` requires `invoices` (`accounting requires invoices`); `purchasing` requires `accounting`, `inventory` and `products` (`purchasing requires accounting`, naming the one missing); `entra` and `google` each require `staff` (`google requires staff`); `agents` requires `communications` (`agents requires communications`). The `assistant` has no permissions of its own and needs no other module, but is of use only while `AI_PROVIDER` is set ([AI](#ai)) |
| `WORKERS_IN_PROCESS` | `1` | Whether the API process also runs the background workers |
| `SHUTDOWN_TIMEOUT`, `LOG_LEVEL` | `30s`, `info` | How long a stop, after its drain delay, drains in-flight work, the workers and the connections, all together; the log's verbosity |
| `SHUTDOWN_DRAIN_DELAY` | `0s` | How long a stop keeps serving with readiness failing before it drains, so a load balancer can take the replica out of rotation; from `0s` to `5m` ([Kubernetes](/en/admin/kubernetes/#drain-and-the-grace-period)) |
| `CLOCK_MAX_SKEW` | `5s` | How far this host's clock may be from the database's before a warning is logged, at most once an hour; the process follows the database's clock either way ([Kubernetes](/en/admin/kubernetes/#the-clock)) |
| `VANTIGO_TENANT_ID` | unset | The installation's name, set by whoever runs it — the namespace's name in Kubernetes: the `vantigo.tenant.id` attribute on every exported trace, metric and log, and `tenant` on every JSON log line. 1–63 characters of `a`–`z`, `0`–`9` and `-`, not starting or ending with `-`, and not `default`, `vantigo-system` or a name starting `kube-` ([Telemetry](/en/admin/installation/#telemetry)) |
| `APP_SECRET` | required | The process-wide key material: cookies, CSRF, sealed secrets |
| `OWNERS_REQUIRE_MFA`, `OWNERS_ALLOW_INSECURE_NO_MFA`, `MFA_ISSUER` | on outside development, `0`, `Vantigo` | Whether Owners and system administrators need a second factor, the switch that lets the process start without it, and the authenticator label |
| `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_LIFETIME` | `8h`, `24h` | How long a standard session may idle and live |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT`, `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | `2h`, `8h` | The same for an Owner or system administrator |
| `INVITATION_LIFETIME`, `INVITATION_ACCEPT_URL`, `PASSWORD_RESET_URL` | `168h`, derived, derived | How long an invitation lasts and the links mailed in invitations and password resets |
| `BOOTSTRAP_OWNER_EMAIL` | unset | Closes `/setup` and seats the first Owner by emailed invitation |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` | unset | The directory a workload identity token file named on the **Single sign-on** card must lie in |
| `TRUSTED_PROXY_HOPS`, `TRUSTED_PROXY_CIDRS` | `0`, unset | The proxies in front, whose forwarded addresses are believed |
| `CSP_REPORT_ONLY` | `0` | Emit the content security policy as report-only |
| `MANAGEMENT_PORT`, `MANAGEMENT_TOKEN` | unset | The private control-plane listener, in every command, and its bearer token; the port alone serves only the health probes ([Management listener](/en/admin/management-listener/)) |
| `LIVE_ENABLED` | `1` | Whether `api` and `server` mode serve the live-updates endpoint, `GET /api/v1/live`. `0` answers `404` and every screen refreshes about once a minute instead |
| `MCP_ENABLED` | `1` | Whether `api` and `server` mode serve the MCP endpoint, `POST /api/mcp`, that AI clients connect to with an integration's key once an Owner turns MCP on for it ([API access › MCP](/en/admin/api-access/#mcp)). `0` answers `404` there for every key |
| `OAUTH_ENABLED` | `1` | Whether an MCP client or app may connect by having an Owner sign in and approve it on the consent page, instead of with a key: the OAuth discovery documents under `/.well-known/`, the token endpoint `POST /api/oauth/token`, the consent and connection operations and the access tokens they issue ([API access › Connecting by signing in](/en/admin/api-access/#connecting-by-signing-in), [OAuth connections](/en/reference/oauth/)). `0` answers `404` at each, the consent page says it is switched off, an integration's page shows no **Connected apps**, an access token is refused `401`, and the connections already made are kept. The issuer is `APP_URL` with its default port dropped and `APP_BASE_PATH` |
| `IDEMPOTENCY_ENABLED` | `1` | Whether a creating `POST` sent with an `Idempotency-Key` header is carried out once and its answer given again to every retry with the same key, for 24 hours ([API access › Retrying safely](/en/admin/api-access/#retrying-safely)); the `identity.api-keys` worker deletes the expired keys every cycle whatever this says. `0` ignores the header everywhere — the operations that declare a key of their own keep theirs — and stores nothing |
| `LIVE_DATABASE_URL` | unset | The database address for the two connections live updates hold per HTTP replica. Unset, they use `DATABASE_URL`. Point it at the database's direct port when `DATABASE_URL` goes through a transaction-mode pooler, where `LISTEN` does not work. Redacted when printed |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | unset | The branding the application showed. **Imported once** into the default brand; set and edited under **Workspace admin › Brand** from then on, and ignored once the import has completed ([Branding](/en/admin/branding/#the-one-time-import-of-app_title-app_logo_url-and-app_support_)) |

See [Installation](/en/admin/installation/) (including [Live updates and your proxy](/en/admin/installation/#live-updates-and-your-proxy)), [Identity, authentication and
deployment](/en/admin/authentication/), [Transport security](/en/admin/transport-security/)
and the [management listener](/en/admin/management-listener/).

### Mail

The **system mailbox**: the sender Vantigo itself writes from.

| Setting | Default | Meaning |
| --- | --- | --- |
| `MAIL_DRIVER` | `log` in development, else `smtp` | `smtp`, `cloudflare` (Cloudflare Email Service), or `log` (development only: nothing is sent) |
| `SMTP_HOST`, `SMTP_PORT` | required for `smtp`, `587` | The SMTP server |
| `SMTP_FROM` | required for `smtp` | The sender address; bounces go here |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | unset | Optional credentials |
| `SMTP_TLS` | `starttls` | `starttls`, `implicit` or `none` |
| `CLOUDFLARE_EMAIL_ACCOUNT_ID`, `CLOUDFLARE_EMAIL_API_TOKEN` | required for `cloudflare` | The Cloudflare account and an API token that may send ([Cloudflare Email Service](/en/admin/authentication/#cloudflare-email-service)) |
| `CLOUDFLARE_EMAIL_FROM` | required for `cloudflare` | The sender address, on a domain onboarded to Email Sending |

It writes only to the people who work in the installation: invitations and password
recovery, Staff's notices and Calendar's notices to colleagues. Nothing that writes to
*customers* uses it. The mailboxes a company sends from in its own name are the
workspace's: [Communications' channels](/en/user/communications/#set-up-a-channel),
with a Microsoft 365 channel set up under
[Microsoft Entra ID](/en/admin/entra/#mail-channels). Invoices' document sends and
reminder letters go from the channel chosen in Invoice settings
([Sending a document by e-mail](/en/user/invoices/#sending-a-document-by-e-mail)),
Calendar's invitations to people outside the company from the one chosen in Calendar
settings ([Invite people outside the company](/en/user/calendar/#invite-people-outside-the-company)),
and an SMTP channel without credentials of its own sends nothing.

### Storage

| Setting | Default | Meaning |
| --- | --- | --- |
| `STORAGE_PROVIDER` | unset | `fs`, `s3`, or unset: nothing that needs the object store works, and says so |
| `STORAGE_FS_ROOT` | unset | The `fs` driver's absolute root directory |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | `0` | Relaxes the root's permission check (development only) |
| `STORAGE_S3_ENDPOINT` | unset | The S3-compatible service, `https://host[:port]` (`http` in development only) |
| `STORAGE_S3_REGION` | unset | The provider's region name, `europe-1` on UpCloud |
| `STORAGE_S3_BUCKET` | unset | The installation's bucket, which the app never creates |
| `STORAGE_S3_PREFIX` | none | A key prefix inside the bucket |
| `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | unset | Static credentials; the `_FILE` variants read them from a mounted file |
| `STORAGE_S3_FORCE_PATH_STYLE` | `1` | Bucket in the path rather than the host name; UpCloud wants `1` |
| `TMPDIR` | `/tmp` | Where the SAF-T import keeps an upload while it reads it; must be writable |

See [Object storage](/en/admin/object-storage/).

### Workers

Each background worker has an on/off switch and, where it looks on a schedule, a
poll. All are on by default except the spot-price worker. The platform's events
dispatcher, `events.dispatch`, has neither: it runs wherever the workers run, every
5 seconds ([Background workers and scaling](/en/admin/installation/#background-workers-and-scaling)).

| Setting | Default | Meaning |
| --- | --- | --- |
| `IDENTITY_API_KEYS_ENABLED`, `IDENTITY_API_KEYS_POLL` | `1`, `1h` | Identity's `identity.api-keys` worker: it records `api_key.expired` once for each integration key that passes its expiry, and deletes a key's row 90 days after it expired; the audit rows stay. It keeps the API request log's partitions — moves what landed in the DEFAULT partition into its day's, makes today's and the next 6 days', drops what is older than the 30 days kept, today included — and mails the expiry notices 14 days and 3 days before a key expires and on the day. Off, an expired key is still refused, only not audited or deleted; no partition is made, so every request log row lands in DEFAULT and stays there, never deleted; and no notice is sent. Switched on again, its first cycle moves DEFAULT's last 30 days into their partitions and deletes the rest ([API access](/en/admin/api-access/#the-request-log)). With `IDEMPOTENCY_ENABLED` or `OAUTH_ENABLED` on, the worker runs even when this switch is off; whenever it runs it deletes the expired `Idempotency-Key`s, and the OAuth codes, tokens, connections and client documents past their keeping, every cycle, each under a lease of its own |
| `IDENTITY_WEBHOOKS_ENABLED`, `IDENTITY_WEBHOOKS_POLL` | `1`, `2s` | Identity's `identity.webhook-sender` worker, switched on and off independently of `identity.api-keys`: every poll — and at once again while more is due — it sends the integrations' webhook messages that are due, at most 4 in flight per endpoint and 16 per process, retries a failure for about 45 hours, disables an endpoint that answers `410 Gone` or has failed for 5 days with at least 5 messages given up after every retry, and mails its notice, and once a minute expires a message still pending after 3 days and deletes those older than 30 days. Several worker processes may run it at once. Off, events are still queued for the endpoints but nothing is sent; switched on again, what waited more than 3 days expires and the rest is sent ([Webhooks](/en/reference/webhooks/#sending)) |
| `STAFF_ACCOUNTS_ENABLED`, `STAFF_RETENTION_ENABLED`, `STAFF_COMPETENCE_ENABLED` | `1` | The Staff accounts, retention and competence workers |
| `STAFF_WORKERS_POLL`, `STAFF_IMPORT_MAX_ROWS` | `1h`, from `5000` to `20000` | How often they look; the people one import takes and one export writes |
| `CALENDAR_CANCELLATIONS_ENABLED`, `CALENDAR_WORKERS_POLL` | `1`, `1h` | Erasing cancelled appointments |
| `CALENDAR_SUBSCRIPTIONS_ENABLED`, `CALENDAR_SUBSCRIPTIONS_POLL` | `1`, `30m` | Fetching the calendars people subscribe to |
| `CALENDAR_MAILBOX_SYNC_ENABLED`, `CALENDAR_MAILBOX_SYNC_POLL`, `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` | `1`, `15m`, `1` | Keeping the copies in people's Outlook, and asking Microsoft to tell Vantigo of changes |
| `CUSTOMERS_IMPORT_ENABLED`, `CUSTOMERS_IMPORT_POLL`, `CUSTOMERS_IMPORT_RETENTION` | `1`, `5s`, `720h` | The customers import worker and how long a finished import is kept |
| `CUSTOMERS_REGISTRY_FEED_ENABLED`, `CUSTOMERS_REGISTRY_FEED_POLL` | `1`, `15m` | Following the company register's changes |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED`, `CUSTOMERS_PEPPOL_RECHECK_POLL`, `CUSTOMERS_PEPPOL_RECHECK_AGE` | `1`, `24h`, `720h` | Re-checking customers' e-invoicing capability |
| `CUSTOMERS_ANONYMISATION_ENABLED`, `CUSTOMERS_ANONYMISATION_POLL` | `1`, `24h` | The customers anonymisation worker |
| `ENERGY_IMPORT_ENABLED`, `ENERGY_IMPORT_POLL`, `ENERGY_IMPORT_RETENTION` | `1`, `5s`, `720h` | The metering-point import worker |
| `ENERGY_SPOT_PRICE_FEED_ENABLED`, `ENERGY_SPOT_PRICE_FEED_POLL`, `ENERGY_SPOT_PRICE_FEED_URL` | `0`, `15m`, the public feed | The spot-price worker and its source |
| `COMMUNICATIONS_RETENTION_DAYS`, `COMMUNICATIONS_RETENTION_BATCH_SIZE`, `COMMUNICATIONS_RETENTION_POLL` | `365`, `100`, `1h` | How old closed history must be before deletion, and how fast |
| `COMMUNICATIONS_ATTACHMENT_MAX_BYTES`, `COMMUNICATIONS_INBOUND_MAX_BYTES`, `COMMUNICATIONS_UPLOAD_EXPIRY` | 10 MiB, 25 MiB, `24h` | Communications' size limits and how long a staged attachment stays valid |

### Shared services and outside systems

| Setting | Default | Meaning |
| --- | --- | --- |
| `BRREG_BASE_URL`, `BRREG_TIMEOUT` | `https://data.brreg.no`, `15s` | The company register lookup |
| `ACCOUNTING_SAFT_IMPORT_MAX_BYTES` | 512 MiB, from 1 MiB to 4 GiB | The largest SAF-T file Accounting takes to set up the books from ([Coming from another system](/en/user/accounting/#coming-from-another-system)) |
| `ACCOUNTING_SAFT_EXPORT_RETENTION_DAYS` | `90`, from `0` to `3650` | How many days a SAF-T export's files are kept before the worker deletes them — they carry personal data; `0` keeps them. The export's record and checksum stay |
| `ACCOUNTING_SAFT_SPLIT_BYTES` | 2 000 000 000, from 1024 | The size past which a SAF-T export is split by period into files each valid alone; Skatteetaten takes files up to 2 GB |
| `PEPPOL_LOOKUP_ENABLED`, `PEPPOL_SML_ZONE`, `PEPPOL_DNS_SERVER`, `PEPPOL_TIMEOUT` | `1`, the production zone, unset, `10s` | The Peppol capability lookup |
| `INVOICES_EHF_ENABLED`, `INVOICES_STORECOVE_BASE_URL` | `1`, Storecove's API | Sending invoices as EHF ([E-invoicing](/en/admin/e-invoicing/)) |
| `INVOICES_EFAKTURA_ENABLED`, `INVOICES_MPS_EFAKTURA_BASE_URL`, `INVOICES_MPS_MANDATE_BASE_URL` | `0`, MPS's production endpoints | eFaktura and AvtaleGiro through Mastercard Payment Services, off until the operator turns it on ([eFaktura and AvtaleGiro](/en/admin/e-invoicing/#efaktura-and-avtalegiro-through-mastercard-payment-services)) |
| `INVOICES_MPS_CLIENT_CERT_FILE`, `INVOICES_MPS_CLIENT_KEY_FILE`, `INVOICES_MPS_USERNAME`, `INVOICES_MPS_PASSWORD_FILE` | unset; required while `INVOICES_EFAKTURA_ENABLED` is on | The submitter's client certificate, its key, its WS-Security user and the file holding its password — absolute, readable paths |
| `INVOICES_VIPPS_ENABLED`, `INVOICES_VIPPS_BASE_URL` | `1`, Vipps' API | Vipps MobilePay payments ([Payments](/en/admin/payments/)) |
| `INVOICES_BANK_FEED_ENABLED`, `INVOICES_FOLIO_BASE_URL`, `INVOICES_BANK_FEED_INTERVAL` | `1`, `https://api.folio.no/v2`, `30m` | The bank feed of incoming payments and its worker, Folio's API, https only, and how often each account is read, `5m` to `24h` ([The bank feed](/en/admin/payments/#the-bank-feed)) |
| `INVOICES_RATES_ENABLED`, `INVOICES_RATES_URL` | `1`, `https://data.norges-bank.no/api/data` | Norges Bank's exchange rates for invoices in another currency and the `invoices-rates` worker, which fetches them each business day after 16:30 through the guarded fetch client — the outbound host `data.norges-bank.no` must be reachable; the base URL is https only, with no trailing slash. Off (`0`), Norges Bank is never called, and a document in another currency is issued only at a rate a manager entered. A fetch that fails is shown on the settings' Currencies card and fails closed: no rate, no issue ([Currencies and exchange rates](/en/user/invoices/#currencies-and-exchange-rates)) |
| `SHIPPING_CARRIERS`, `SHIPPING_BRING_BASE_URL` | `bring`, `https://api.bring.com` | The carrier adapters the installation allows, comma-separated among the known ones (`bring`); empty withholds every carrier — no carrier account can be added, booked on or tracked. The Bring adapter's base URL, https only, for a mock host; Bring's test mode is a carrier account's test flag, not this ([Shipping](/en/admin/shipping/)) |
| `ENERGY_SPOT_PRICE_SERVICE_URL`, `ENERGY_SPOT_PRICE_SERVICE_KEY`, `ENERGY_SPOT_PRICE_HISTORY_FROM` | unset | The shared spot-price service Vantigo provisions an installation with, its bearer key (a secret, shown nowhere) and the oldest day copied |
| `AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS`, `AI_TIMEOUT`, `AI_CONTEXT_TOKENS`, `AI_EFFORT`, `AI_FALLBACKS` | unset, unset, unset, unset, `120s`, `16000` (`150000` for `anthropic`), `medium`, `on` | The AI plan: the provider, its key (a secret, shown nowhere; optional for a model on your own network), an optional compatible endpoint, the models the plan allows, how long a chat may take, how much of a conversation the assistant sends the model, and, for Anthropic, how hard the model thinks and whether a declined request is retried on a fallback model. Unset, every AI feature is unavailable ([AI](#ai)) |
| `AGENTS_CONCURRENCY` | `4` | How many runs of [virtual employees](/en/admin/agents/) one API replica carries at once, from `1` to `32`; a value outside that range is refused at start. Each run is one agent working on one conversation, for at most five minutes, and holds one model call at a time; raise it when conversations wait in the queue, and lower it when your model server cannot take that many at once. Each replica has its own count |

### AI

The AI plan is Vantigo's: the provider, the key and the models it allows belong to the
installation, are read once when the process starts, and are only *shown* in the
interface. Which of the allowed models each AI job uses is the workspace's choice.

| Setting | Default | Meaning |
| --- | --- | --- |
| `AI_PROVIDER` | unset | The provider: `openai`, `openai-compatible` for anything that speaks OpenAI's protocol, such as a model you run yourself ([a local model](#a-local-model)), or `anthropic` for Claude ([Anthropic](#anthropic)). It is shown as you wrote it. Unset, no AI feature is available: Communications' AI draft and customer suggestion answer that AI is unavailable, and **AI models** does not appear under Integrations |
| `AI_API_KEY` | required with the provider, unless `AI_BASE_URL` is set; always required for `anthropic` | The provider's key, a secret: it is shown nowhere, in no form, and **AI models** says only whether one is configured. It may be empty when `AI_BASE_URL` names an endpoint, since a model on your own network takes none; then no key is ever sent |
| `AI_BASE_URL` | the provider's own address | An absolute URL for an OpenAI-compatible endpoint, up to and including the version path (`https://api.openai.com/v1`); no user, query or fragment. With `anthropic`, the address of a gateway in front of Anthropic's API (the default is `https://api.anthropic.com`), and `https` only. Otherwise `https`, or `http` when the host is a loopback address, a private address (`10.x`, `172.16`–`172.31`, `192.168.x`, `fc00::/7`) or a name with no dot such as `ollama`. A public host over `http` is refused, and so is any `http` address while `AI_API_KEY` is set: a key is never sent in the clear |
| `AI_MODELS` | required with the provider | The models the plan allows, comma-separated, the first being the plan's default: `gpt-4o-mini,gpt-4o`. At least one, none repeated, each up to 150 characters with no space |
| `AI_TIMEOUT` | `120s` | How long one chat call may take, as a Go duration above zero and at most `10m`. Local models are slow; a single-prompt call keeps its own 30 seconds |
| `AI_CONTEXT_TOKENS` | `16000`; `150000` for `anthropic` | How much of a conversation the [Assistant](/en/user/assistant/) sends the model, as a number of tokens from `2000` to `1000000`; a value outside that range is refused at start. Set it to the context window of the model you run, or a little below. It is estimated at about four characters to a token, so it is a guide and not a measure; past it, the assistant first replaces the older results of its tools with a one-line note, and then drops the oldest turns of the conversation, so the latest question is always sent. Local models have small windows; a hosted model may take far more. For `anthropic` the assistant does not shorten older results in place; past this size it [summarises the conversation](#anthropic) instead, which is why its default there is `150000`, a good part of the model's window |
| `AI_EFFORT` | `medium` | How hard a thinking model thinks: `low`, `medium`, `high`, `xhigh` or `max`. Used by `anthropic` as the request's effort; `openai` and `openai-compatible` ignore it. Higher is slower and costs more; lower first, before you ask the model to think less in words |
| `AI_FALLBACKS` | `on` | `on` or `off`. With `anthropic`, a request Anthropic's safeguards decline is run again on a fallback model on Anthropic's side; `off` turns that off. Models without fallbacks (Haiku) are never sent the request. `openai` and `openai-compatible` ignore it |

`AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS` and `AI_TIMEOUT` without `AI_PROVIDER` are refused at start, as
is a provider with no model, or with neither a key nor a base URL (`anthropic`: with no key, or with an `http` base URL); a value of `AI_EFFORT` or `AI_FALLBACKS` that is not one of the listed is refused whatever the provider; an unset provider is the ordinary "no AI" state
and never an error.

#### Anthropic

`AI_PROVIDER=anthropic` runs every AI task on Claude, through Anthropic's Messages API: Communications'
reply drafts and customer suggestions, and the [Assistant](/en/user/assistant/) with its
streaming, tools, confirmations, bulk changes and tours. This is what it looks like in `vantigo.env`:

```
AI_PROVIDER=anthropic
AI_API_KEY=sk-ant-...
AI_MODELS=claude-opus-5-5,claude-sonnet-5-5,claude-haiku-5-5
AI_EFFORT=medium
AI_CONTEXT_TOKENS=150000
```

- **`AI_API_KEY`** is an Anthropic API key and is always required, also beside `AI_BASE_URL`.
  It is a secret, shown nowhere, and Vantigo reads no `ANTHROPIC_*` variable of the process.
- **`AI_MODELS`** takes Claude model ids exactly as Anthropic names them. A good plan lists a
  strong model for the Assistant (`claude-opus-5-5` or `claude-sonnet-5-5`) and a small one for
  short jobs (`claude-haiku-5-5`); an Owner then picks one per task under **AI models**.
  The first is the default. Model ids that Anthropic retires stop working: update the list.
- **`AI_EFFORT`** and **`AI_FALLBACKS`** are described above. Claude models think before they answer,
  and Vantigo leaves that to the model; the effort is the one control.
- **Calls are not retried by Vantigo's client.** A rate limit or an overload reaches the person as
  "the AI service did not answer"; the person asks again.
- **The Assistant keeps each conversation's start unchanged** so that Claude's thinking stays valid
  and Anthropic's prompt cache is used: the instructions are fixed when the conversation begins,
  and when it grows past `AI_CONTEXT_TOKENS` it is summarised once and continues from the summary.
  The summary costs one extra call to the same model.

**Where the data goes.** Questions asked of the Assistant, the documentation it finds, the data it
reads through the API and the text of the messages that Communications drafts a reply to are sent
to Anthropic, outside your installation. The Assistant's settings page says so to the Owner
(**Workspace admin** › **Assistant**). Before you switch it on, make sure your agreement with
Anthropic, including its data processing agreement, covers that data, and tell your
customers in your privacy notice as you would for any processor. Anthropic is never a
local provider, whatever `AI_BASE_URL` is.

#### A local model

Questions asked of the AI, and the data it reads to answer them, go to the provider you
configure. With a model that runs on your own network, they stay on the installation: nothing
leaves it. Any server that speaks OpenAI's chat protocol works — Ollama, vLLM, LM Studio.
This is what an Ollama server next to Vantigo looks like in `vantigo.env`:

```
AI_PROVIDER=openai-compatible
AI_BASE_URL=http://ollama:11434/v1
AI_MODELS=qwen3:14b
AI_TIMEOUT=180s
```

There is no `AI_API_KEY`, and none is sent. `http://ollama:11434/v1` is accepted because
`ollama` is a name with no dot, which only resolves inside your own network; the same
address with a public host name is refused when the process starts, with a message naming
`AI_BASE_URL`. The model must support **tool calling** (a function-calling model: for Ollama,
one tagged *tools* in its library), and `AI_MODELS` must name it exactly as the server does.
Pull the model on the server first. Choose `AI_TIMEOUT` for the slowest answer you will accept;
a small model on a CPU can need minutes.

Every AI job is still assigned a model per task under **AI models**, among the ones
`AI_MODELS` lists.

#### The assistant's model

The [Assistant](/en/user/assistant/) is the one AI feature that holds a conversation and lets the
model ask for tools: it searches the documentation and reads data through the API, as the person
asking, by returning tool calls. **It needs a model that supports tool calls** (function calling). A
model that does not is either refused by the server when the assistant asks it, or answers without
the documentation and the person's data and is the worse for it; test the model you choose by
asking the assistant a how-to question and a question about data. The assistant is the task
**Assistant chat** (`assistant.chat`), and an Owner chooses its model under **AI models** like any
other. Each answer may take several calls to the model — it reads, then answers — so `AI_TIMEOUT` is
the limit of each, not of the whole answer. `AI_CONTEXT_TOKENS` bounds what each call is sent.
Questions, the documentation it finds and the data it reads go to the provider; an Owner sees
where under **Workspace admin** › **Assistant** ([guide](/en/user/workspace-administration/#assistant)).
The assistant is available to nobody while `AI_PROVIDER` is unset.

Every AI job a module performs is a **task** that the module declares, such as *Draft a
reply* or *Suggest the customer* in Communications. An Owner chooses the model each task
uses among `AI_MODELS`, under **Workspace admin** › **Integrations** › **AI models**
([guide](/en/user/workspace-administration/#ai-models)). A task nobody has chosen for uses
the model its module recommends if the plan allows it, and otherwise the first of
`AI_MODELS`. A new `AI_MODELS` takes effect when the process restarts; a choice for a
model the list no longer holds is ignored, and the task falls back as above, until an
Owner chooses again. Tasks are declared by the modules, in the code: a module that is off
has none, and a task is added with the feature that performs it.

A task can also be declared **per subject**: the model can then be chosen for one subject, such
as one virtual employee (agent), besides the task's own choice. The subject's model comes first,
then the task's, then the module's recommendation and the first of `AI_MODELS`; a model the plan
no longer allows is ignored the same way. The choice for a subject is made where the subject is
managed, not on **AI models**, which keeps showing the task's own choice.

#### The renamed variables

Communications' four AI variables were renamed when AI became a platform setting. The
old names are read as aliases for one more release, and the process names the ones you
still set in a single warning at every start.

| Was | Is | How the old one is read |
| --- | --- | --- |
| `COMMUNICATIONS_AI_ENABLED=1` | `AI_PROVIDER` | Turns the provider on when `AI_PROVIDER` is unset |
| `COMMUNICATIONS_AI_PROVIDER` | `AI_PROVIDER` | The provider (`openai` when unset); another value leaves AI unavailable, as before |
| `COMMUNICATIONS_AI_API_KEY` | `AI_API_KEY` | The key, when `AI_API_KEY` is unset; enabled without a key leaves AI unavailable, as before |
| `COMMUNICATIONS_AI_MODEL` | `AI_MODELS` | While the provider comes from the old variables and `AI_MODELS` is unset, the one model the plan allows (`gpt-4o-mini` when unset); and imported once, below |

An `AI_*` variable that is set always wins, and the old one beside it is named as
ignored. Set the new names and remove the old before the next release, which stops
reading them.

#### The import of `COMMUNICATIONS_AI_MODEL`

Communications used one model for both of its AI operations. At the first start of the
API that finds no model chosen for any task, while `COMMUNICATIONS_AI_MODEL` is set, an AI
plan is configured and the plan allows that model, the model is written as the choice for
each of Communications' tasks, audited `ai-task-models.imported` with no actor, and the
log warns that the variable is now ignored and should be removed. The behaviour does not
change: both operations keep using that model until an Owner chooses another.

- A model that `AI_MODELS` does not hold is **not** imported, and the log says so; an Owner
  chooses under **AI models**.
- With no AI plan, or with Communications off, nothing is imported and nothing is said:
  the variable waits for either to arrive.
- Once any task has a choice the variable is ignored at every start, with a warning naming
  it until you remove it. It is never read again, so a changed value changes nothing.

## The workspace's settings, and where each is edited

| Setting | Edited at | Who |
| --- | --- | --- |
| Single sign-on and SCIM provisioning | **Workspace admin** › **Overview** › the **Single sign-on** card ([guide](/en/user/workspace-administration/#set-up-single-sign-on)) | an Owner |
| The workspace's own settings, such as the business time zone | **Workspace admin** › **Overview** › **Workspace settings** ([guide](/en/user/workspace-administration/#workspace-settings)) | an Owner |
| Roles, assignments and delegations | **Workspace admin** › **Roles & access** | an Owner, or whoever role administration is delegated to |
| The Microsoft Entra ID connection: tenant, application, client secrets and permissions | **Workspace admin** › **Integrations** › **Microsoft Entra ID** › **Connection** ([guide](/en/user/entra/#where-it-is-and-the-four-tabs)) | an Owner |
| The account rules, sign-in and provisioning, the groups Vantigo keeps in the tenant | **Integrations** › **Microsoft Entra ID** › **Accounts** | `entra:manage` (sign-in and provisioning: an Owner) |
| The Outlook connection, calendar sync on it, and Mail channels | **Integrations** › **Microsoft Entra ID** › **Outlook and mail** | `entra:manage` |
| The Google Workspace connection: the service account's keys, the admin to impersonate, the Internal web client and the test | **Workspace admin** › **Integrations** › **Google Workspace** › **Connection** ([guide](/en/user/google-workspace/#where-it-is-and-the-four-tabs)) | an Owner (`google:manage` reads it) |
| The Meta app, the webhook's verify token, the Human Agent setting and the Facebook Pages | **Workspace admin** › **Integrations** › **Meta** ([guide](/en/admin/meta/)) | an Owner |
| The model each AI task uses, among the models the plan allows | **Workspace admin** › **Integrations** › **AI models** ([guide](/en/user/workspace-administration/#ai-models)) | an Owner |
| Who has the assistant, how long conversations are kept and what the assistant may use | **Workspace admin** › **Integrations** › **Assistant** ([guide](/en/user/workspace-administration/#assistant)); each person's own switch in **Users** › **Edit details** | an Owner |
| The mailboxes Communications sends from, and the SMTP accounts it uses | **Communications** › **Channels** | `communications:channels-manage` |
| A module's own rules: Invoice settings, Staff settings, Planning settings, Time settings, Expense settings | The module's settings page in its sidebar | the module's permission |

*Integrations* is a section of Workspace admin: the one place a company's connections to
systems elsewhere are managed. A person's own connections are the other side of the same
line, under **Settings** › **Connections**
([Connections](/en/user/connections/)).
