# Hosted mail service — research before a build decision

Date: 2026-11-01. For the owner's deferred question (answer 7 of
[the integrations design](../specs/2026-10-31-integrations-and-configuration-tiers-design.md),
"we need to think very thoroughly through this if we are to build such a service"): a
shared service of ours in front of a mail provider, so that a tenant with no Microsoft 365
or SMTP account can still send invoices, reminders and calendar invitations to its
customers, with consumption tracked per tenant and test tenants held to a list of
recipients, and the same service carrying the system mail so a new instance needs one
service key instead of SMTP credentials. Read with the spec's T1 and T4–T10 (T8–T10 carry
the *deferred* banner and are the sketch this document tests), its Open questions Q1 and
Q6, [the C′ brief](../plans/2026-10-31-integrations-briefs/c-prime-hosted-mail-deferred.md),
[the spot-price service design](../specs/2026-10-26-energy-spot-price-service-design.md)
(S3, S7, S9, "S" below) and
[the Outlook connection and mail research](2026-10-29-outlook-connection-and-mail.md),
whose shape this follows.

**The short answer.** Build it, but later and thinner than T10 sketches, and not on
`<tenant>.vantigo.cloud`. Three things in the sketch do not survive the readings below:
the sending domain collides with the tunnel's wildcard DNS record (§5.2); a hosted channel
can send but not receive, which breaks calendar replies and turns Communications'
free-text channel into a cold-mail vector (§6.3); and Mailgun has no idempotency and no
subaccounts below Enterprise, so the service must carry its own key store and its own
reputation guard and keep system mail on a separate Mailgun account (§3.4, §6.1). Until
the first tenant without a mail account exists, the system mail can run on the host-wide
SMTP relay that `vantigo-infra` already templates, which needs no code (§10).

Facts about outside systems carry the URL and the date read (**2026-11-01** for every one
unless another is stated). Where a page could not be read, or only a search snippet or a
secondary source says it, the text says so: **UNVERIFIED** marks a fact that should be
probed before it is built on (§10.4 lists the probes). `srv/` abbreviates
`apps/server/internal/`, `boundaries.md` the module-boundaries page, **T**/**Q** the
integrations design's decisions and questions, **S** the spot-price design's. Code
references are against `main` at `280f8c66`, which holds delivery B
(`contracts.OutboundMail`, the end of the SMTP fall-back), delivery C's greyed entry and
delivery D (the AI platform).

## 1. What the service is for, and what it is not

### 1.1 The tiers it sits in

T1 (`spec:109-130`) puts every setting in one of two tiers by owner. Hosted mail is the one
feature that straddles them, and that is why it needs care:

| Concern | Tier | Who decides | What follows |
| --- | --- | --- | --- |
| That an instance can send *any* mail to its own staff (invitations, password resets, Staff and Calendar notices) | **Instance**: `MAIL_DRIVER`, `SMTP_*`, and with the service `MAIL_SERVICE_*` | Vantigo | Provisioned by `vantigo add`; never edited in the interface |
| That the tenant writes to *its customers in its name* | **Workspace**: a Communications channel, chosen per module in Invoice settings and Calendar settings (T6) | The tenant's Owner | The hosted provider is one choice among Microsoft 365, SMTP and disabled (T7) |
| The sending domain, the test list, the allowance | **Instance facts shown, not edited** (T1, third bullet; T8) | Vantigo | The channel card reads them from the service; no field |

So the service is two products with one engine. As **system mail** it replaces the SMTP
relay: the recipients are the tenant's own employees, the volume is tiny, and the failure
that matters is "nobody can be invited and nobody can reset a password" (`vantigo-infra`
README, "What the application requires of a host", `README.md:88`). As **customer mail**
it is a feature the tenant buys: the recipients are strangers to us, the content is the
tenant's, and the failure that matters is reputation. The two have different risk and
should not share an account at the provider (§3.4).

### 1.2 What it is not

- **Not a mail server.** It sends; it does not keep mailboxes. T8 says "inbound later" and
  T10 reserves `inbound: false`; §6.3 shows that calendar invitations make that
  reservation load-bearing earlier than the spec assumed.
- **Not a marketing or bulk platform.** Every message the product sends today is
  transactional (§2). The service's allowance, caps and templates should say so and refuse
  the rest; a customer newsletter belongs to the tenant's own tool.
- **Not a replacement for the tenant's own channel.** A Microsoft 365 shared mailbox keeps
  Sent Items, threads and replies (Outlook research §4); hosted mail keeps none of them.
  The pitch is "works with no setup", not "better".
- **Not the customers' data store.** §6.4 recommends that the service keeps no message
  body and no recipient address at rest.
- **Not needed to launch the SaaS.** A tenant without hosted mail can still use Microsoft
  365, SMTP, or send nothing (T6's *disabled*); invoices can be downloaded and, for
  Norwegian businesses, sent as EHF (§2.3). Whether enough tenants lack all of those is a
  market question the owner can answer and this document cannot (§11.1).

## 2. What Vantigo sends today, and the volume shape per tenant

### 2.1 The senders

| Sender | Recipient | Path today | Source | Volume driver |
| --- | --- | --- | --- | --- |
| Identity: invitation, password reset | the tenant's staff | `Deps.Mail` → `mail.Sender` → SMTP (`MAIL_DRIVER`) | `srv/mail/mail.go:30-46`, `srv/identity/invitations.go:143`, `srv/identity/recovery.go:52` | people joining; recoveries |
| Staff notices, Calendar colleague notices | the tenant's staff | `Config.Mail` → `mail.SendOutbound` | `srv/staff/notifications.go:294` | HR events; appointment changes |
| Invoices: document send | the tenant's **customer** | `OutboundMail.Send` over the chosen channel (B) | `srv/invoices/send.go:316-335`, envelope `:385-415` | invoices issued |
| Invoices: reminder letters | the customer | the same, from the reminder worker | `srv/invoices/reminder_worker.go:319` | overdue invoices × reminder steps |
| Calendar: invitations, updates, cancellations | outside attendees | the same, one message per attendee | `srv/calendar/invitations.go:15-45` | appointments with outsiders |
| Communications: conversation replies | whoever the tenant writes to | the outbox through the channel's provider | `srv/communications/outbox.go` | free text from users |

Everything but the last row is generated from a template with a bounded vocabulary. The
last is free text to any address, and T8 puts the hosted provider among the channels it
may send (§6.3).

### 2.2 How each sender sends: the facts the service inherits

- **Synchronous, at least once.** `OutboundMail.Send` is "at least once, as SMTP: a failure
  after the provider took the message may send it again"
  (`srv/contracts/outbound_mail.go:44-52`). Invoices sends under a 30 s budget
  (`invoices/send.go:55`) and answers 502 to the person on failure; Calendar marks the
  attendee for *Resend*; only the reminder worker and the Communications outbox retry by
  themselves.
- **The retry horizon is short.** The outbox makes at most 8 attempts (`outbox.go:76`) with
  `min(3600, 2^attempts)` seconds between them, which in practice is at most 128 s
  (`backoff.go:8-52`): all eight attempts fit in about four minutes. The reminder worker
  fails a letter after 48 h (`reminder_worker.go:95`) and keeps its `Message-ID` across
  tries (`:319`). Invoices mints a fresh `uuid@vantigo.invalid` per envelope
  (`send.go:66`), so a person's second click is a new message. **The idempotency window the
  service needs is therefore 72 h, not T10's 7 days** (§6.1).
- **Bcc and the envelope.** `mail.Outbound` has `To`, `Cc` and `Bcc`
  (`srv/mail/outbound.go:63-80`); the MIME writer leaves the Bcc header out unless asked
  (`WithBcc`, `outbound.go:134`, `RenderMIME` `:144`). A service that takes "a whole
  RFC 5322 message" (T10) cannot know the blind recipients from the message alone. The
  contract needs an explicit envelope (§6.2).
- **Reply-To is the seller's mailbox.** Invoices and reminders set
  `ReplyTo: settings.Email` (`send.go:413`, `reminder_worker.go:319`), so replies to an
  invoice reach a human even if the From address has no mailbox. Calendar does **not** do
  this: the iCalendar `ORGANIZER` is the channel's From address (`calendar/ics.go:232`,
  `invitations.go:255`), so an attendee's *Accept* from Outlook replies to the channel
  address (§6.3).
- **Size.** A channel provider declares `maxMessageBytes` (`channel_providers.go:34-45`);
  SMTP has none, Microsoft 365 3 MB inline. A PDF invoice is typically tens to a few
  hundred KB (estimate), base64-grown by 37 %.
- **Message-ID.** Every module mints `…@vantigo.invalid`; the domain is not a real one.
  Harmless over SMTP, but a provider that rewrites Message-ID would break correlation
  (§3.2).
- **The system mailbox is best-effort.** Password-reset failures are logged and the answer
  is "accepted" regardless (`identity/recovery.go:52-58`); the application *requires*
  `SMTP_HOST` and `SMTP_FROM` at startup (`srv/config/config.go:1533-1540`), and infra
  fills them from a host-wide template (`vantigo-infra/template/.env.example:60-75`).

### 2.3 Volume per tenant: an estimate, with its assumptions

No installation sends hosted mail, and no measurements of real tenants exist in the
repository, so this is a model from the features. The unit is one message to one
recipient, which is what a provider bills (Mailgun's per-recipient counting is
**UNVERIFIED**; every Vantigo sender addresses one recipient per message anyway).

| Per month | Small (5 users, ~30 invoiced customers) | Medium (25 users, ~250) | Large (100 users, ~1,200) | Assumption |
| --- | --- | --- | --- | --- |
| Invoices and credit notes by e-mail | 30 | 250 | 1,200 | one per invoiced customer per month |
| Reminder letters | 6 | 50 | 250 | 20 % of invoices overdue, about one letter each |
| Calendar mail to outsiders | 20 | 120 | 500 | request, update and cancel per outside attendee |
| System mail | 15 | 100 | 400 | invitations, resets, Staff and colleague notices |
| **Total** | **~70** | **~520** | **~2,350** | |

Shape, not just size:

- **Bursty at month end.** Invoicing is clustered on a few days; a Large tenant may send
  600 invoices inside an hour, and across a fleet the peaks coincide. Mailgun publishes no
  sending rate limit (§3.1), so the service needs its own per-tenant and global pacing.
- **Small attachments.** Invoice and reminder PDFs dominate bytes; a Large tenant's month
  is about 300 MB of mail. Not a cost or limit concern at any provider below.
- **Falling PDF volume to Norwegian businesses.** From 2027-01-01 a PDF by e-mail to a
  Norwegian business is not a lawful e-invoice (`invoices/send.go:46-51`, Lov 19. juni 2026
  nr. 39); those invoices move to EHF over Peppol. The model does not discount for it; the
  real emailed-invoice count may be lower, concentrated on consumers and foreign buyers.
  The audience of hosted *invoice* mail is therefore B2C and exceptions, and the volume per
  tenant may be the smaller of the numbers above.
- **A fleet.** 50 tenants as 30 S + 15 M + 5 L ≈ 21,650 messages a month; 200 tenants as
  120 S + 60 M + 20 L ≈ 86,600. Both are small by provider standards (§7.4).

## 3. Mailgun, as found

Mailgun is a Sinch company (Sinch Email); the contracting entity and the DPA are covered in
§8. Everything below was read on 2026-11-01. Mailgun's documentation site serves each page
also as `.md`; several help-centre articles answer 403 to a script, and those are marked.

### 3.1 The facts

| Topic | Finding | Source |
| --- | --- | --- |
| **Regions** | Two base URLs, `https://api.mailgun.net` (US) and `https://api.eu.mailgun.net` (EU); one account manages domains in both; the region is chosen **per domain** at creation and cannot be changed after | [API overview](https://documentation.mailgun.com/docs/mailgun/api-reference/api-overview.md) |
| **What stays in the EU** | "Message data never leaves the region in which it is processed." Region-bound: domain metadata (SMTP credentials), **messages, event logs, suppressions**, lists, tags, statistics, routes, IPs. **Replicated globally: account information, user accounts, billing, API keys and domain names.** The domain names will be our tenant slugs | same |
| **Sending** | `POST /v3/{domain}/messages` (parts) and `/messages.mime` (a ready MIME); maximum message size **25 MB** uncompressed; "send options" (`o:`, `h:`, `v:`, `t:`) limited to 16 KB; 1,000 recipients per batch only for `recipient-variables` | [sending](https://documentation.mailgun.com/docs/mailgun/user-manual/sending-messages/send-http), [messages](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages), [mime](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages.mime) |
| **Answer** | `200 {id, message: "Queued. Thank you."}`: queued, not delivered | messages |
| **Idempotency** | **None.** No page mentions an idempotency key or duplicate suppression. A repeated POST sends again | API overview, sending |
| **Our own Message-ID** | Mailgun "will automatically include a unique Message-ID or you can set your own" ([glossary](https://www.mailgun.com/glossary/message-id/), via search); the `h:Message-Id` form is customary, but the reference pages I read do not list it, and whether `/messages.mime` keeps a Message-ID inside the MIME is **UNVERIFIED** | search snippet |
| **Per-message options** | `o:testmode=yes` (processed, not delivered), `o:require-tls`, `o:skip-verification`, `o:tracking*`, `o:dkim`, `o:tag`, `o:deliverytime` (3 days, 7 on some plans), `o:require-tls` | messages |
| **Domains API** | `POST /v4/domains` with `name`, `web_scheme`, `dkim_key_size` (1024/2048), `dkim_selector`, `force_dkim_authority`, `use_automatic_sender_security`, `ips`, `pool_id`, `web_prefix` (default `email`), `wildcard`, `message_ttl`, `webhooks_redact_pii`, `skip_verification`; the answer carries `sending_dns_records` and `receiving_dns_records`; `PUT /v4/domains/{name}/verify`; list is paged to 1,000 | [create a domain](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/domains/post-v4-domains), [domains](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/domains) |
| **DKIM authority** | `force_dkim_authority=false` (the default) makes a subdomain use **the root domain's DKIM authority when the root is on the same account**; `true` makes each domain its own. Per-tenant domains want `true` | create a domain |
| **Automatic Sender Security** | `use_automatic_sender_security=true` swaps DKIM TXT records for **CNAMEs**: records stay constant while Mailgun rotates keys. Plan availability **UNVERIFIED** | create a domain |
| **Which records** | Sending: SPF TXT, DKIM (TXT or CNAME), optional tracking CNAME (`email.<domain>`); receiving: MX. The exact record names and values are the API's answer, not documented as static; whether a sending-only domain needs the MX for bounces is **UNVERIFIED** | create a domain, search |
| **Domain limits** | Free and Basic **1** custom sending domain; Foundation and Scale **1,000** | [pricing](https://www.mailgun.com/pricing/) |
| **Webhooks** | Account- or domain-level; events `accepted`, `delivered`, `temporary_fail`, `permanent_fail`, `opened`, `clicked`, `unsubscribed`, `complained`; JSON POST | [webhooks](https://documentation.mailgun.com/docs/mailgun/user-manual/webhooks/webhooks) |
| **Signing** | HMAC-SHA256 of `timestamp + token` under **one account-wide webhook signing key** (not the API key); the page advises caching tokens against replay and a loose timestamp check; a subaccount's events carry a `parent-signature` | [securing webhooks](https://documentation.mailgun.com/docs/mailgun/user-manual/webhooks/securing-webhooks) |
| **Retries** | A 200 is success, a **406 is rejection without retry**, anything else retries at 5, 10, 15 min, 1, 2, 4 h (about 8 h), "other than for delivery notification" webhooks, for which the page says nothing | [webhook retries](https://documentation.mailgun.com/docs/mailgun/user-manual/webhooks/webhook-retries) |
| **Events API** | Events retained **up to 30 days**, plan-dependent, pageable. Filtering by message-id and the lag before an event is queryable are **UNVERIFIED** | [events](https://documentation.mailgun.com/docs/mailgun/user-manual/events/events) |
| **Suppressions** | Bounces, complaints, unsubscribes, and an allow-list, **organised per domain**; a complaint or unsubscribe is honoured even for an allow-listed address; copying lists to a new domain is common practice | [help centre](https://help.mailgun.com/hc/en-us/articles/360012287493), via search |
| **Rate limits** | The API answers 429 with `X-RateLimit-Limit/-Remaining/-Reset`; "time windows vary by API"; **no figure is published**, and the sending page says to ask support to raise them | API overview |
| **Message storage** | Message bodies are kept per domain: Free 0 days, Foundation 1, Scale and Enterprise up to 7 (default 3); the send page says 72 h by default and that a stored MIME can be re-POSTed to new recipients | [help centre](https://help.mailgun.com/hc/en-us/articles/8841411163035) (403 to a script; search snippet), sending |
| **Logs** | Free and Basic 1 day, Foundation 5, Scale 30 | pricing |
| **Keys** | A **domain sending key** (`POST /v1/keys`, `kind=domain`, `role=sending`) can only call `/messages` and `/messages.mime` for its domain; Mailgun recommends it for send-only applications | [authentication](https://documentation.mailgun.com/docs/mailgun/api-reference/authentication/), [security](https://documentation.mailgun.com/docs/mailgun/user-manual/mg_security/), via search |

### 3.2 What follows for a service

- **The service must be the idempotency layer** (§6.1). Mailgun will not de-duplicate, and
  the outbox contract is at-least-once.
- **Correlate events by Mailgun's `id`, not by our Message-ID alone.** Keep the mapping
  `mailgun_id ↔ idempotency key ↔ tenant` in the service; if Mailgun keeps our Message-ID
  the second correlation comes free, if it rewrites it nothing breaks. Probe first.
- **Region is a per-domain choice made at creation**: the service creates every domain with
  the EU base URL, and a domain created in the wrong region is deleted and recreated, with
  its suppressions lost.
- **The signing key is account-wide.** One service holding it is fine; sixty tenant
  instances each holding it (the "direct" option, §10.2) is not.
- **406 is a useful lever**: a webhook consumer that cannot process an event for good
  (unknown tenant) returns 406 and stops the retry chain.
- **Turn tracking off.** Open and click tracking rewrites links and needs the `email.<domain>`
  CNAME; for invoices and invitations it adds spam-filter risk and a consent question for
  nothing the product uses. `o:tracking=no` on every message removes the CNAME from the
  DNS the service must publish and the tracking events from the data it receives.

### 3.3 Pricing (USD, [mailgun.com/pricing](https://www.mailgun.com/pricing/), read 2026-11-01)

| Plan | Per month | Included | Overage per 1,000 | Custom domains | Logs |
| --- | --- | --- | --- | --- | --- |
| Free | $0 | 100 a day | — | 1 | 1 day |
| Basic | $15 | 10,000 | from $1.80 | 1 | 1 day |
| Foundation | $35 (first month free) | 50,000 | from $1.30 | 1,000 | 5 days |
| Scale | $90 (first month free) | 100,000 | from $1.10 | 1,000 | 30 days |

A dedicated IP is $59 a month (the page's FAQ, flattened table: **partly UNVERIFIED**
which plan includes one); the page lists "Subaccounts" and SAML SSO among features without
a legible per-plan mark. The page prices in USD only; NOK and EUR are the owner's exchange
exposure.

### 3.4 Subaccounts, and what Mailgun does to a sender that spams

- **Subaccounts** keep separate domains, keys, SMTP credentials and statistics under one
  primary account and share its plan; a primary admin can set a **custom monthly sending
  limit** per subaccount (soft, "not enforced in real time"), and *disable* one (API
  suspended, UI read-only) or *close* it (data deleted, unrecoverable)
  ([terms](https://documentation.mailgun.com/docs/mailgun/user-manual/subaccounts/subaccounts-terms.md),
  help-centre article via search). **Mailgun's help centre says the feature is limited to
  Enterprise customers** (search snippet of the 403 article; the pricing page does not show
  a legible per-plan mark): treat subaccounts as *not available* on the plans of §3.3 until
  sales confirms otherwise. That is the feature that would have made the tenant the
  isolation unit at the provider.
- **Without subaccounts the unit of enforcement is the domain, inside one account.**
  Mailgun "has thresholds in place that, if exceeded, will result in a domain being
  temporarily disabled", unpublished, to protect reputation "in case your system is
  compromised by a spammer"; the usual cause is a bad list; the way back is support
  ([help centre](https://help.mailgun.com/hc/en-us/articles/202051664), 403 to a script,
  via search). Reports of account *probation* with an hourly cap exist from users
  ([Courier](https://www.courier.com/error-solutions/mailgun-account-probation), a forum
  post), not from Mailgun. The `0.1 %` complaint figure in a vendor-comparison blog is not
  Mailgun's and is not relied on.
- **The blast radius is the account.** A tenant that spams can be contained by disabling
  its domain, *if* Mailgun acts at domain level; if it acts at account level, every tenant
  sharing the account stops, **including the system mail that lets staff reset passwords**.
  Two cheap mitigations follow: **two Mailgun accounts**, one for system mail (employees
  only, never at risk) and one for tenant customer mail; and the service's **own guard**
  (§6.5) that pauses a tenant on its own bounce and complaint rates *before* Mailgun's
  unpublished thresholds do.
- **What the service cannot do at Mailgun** is read the reputation: shared IPs are
  Mailgun's pool, and the domain-level signals a mailbox provider holds are visible only in
  Google Postmaster Tools for domains we verify (§5.5).

## 4. Alternatives, fairly

Read 2026-11-01. The pages are the vendors' own where a URL is given; "via search" means a
search summary or a secondary site and **UNVERIFIED** against the vendor.

| | **Mailgun** (Sinch) | **Amazon SES** | **Postmark** | **Resend** | **Scaleway TEM** | **Brevo** | **Cloudflare Email Service** |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **EU story** | EU region per domain, message data stays; **account data, API keys and domain names replicated globally**; US contracting entity ([overview](https://documentation.mailgun.com/docs/mailgun/api-reference/api-overview.md)) | EU regions (Frankfurt, Stockholm, Ireland and others: general knowledge, not re-read); US company | **US only**: "we currently don't have plans to add servers in the EU" ([postmarkapp.com/euprivacy](https://postmarkapp.com/euprivacy), via search) | `eu-west-1` is where mail is *sent from*; **account data stays in the US** ([regions](https://resend.com/docs/dashboard/domains/regions), via search) | **French**, Paris `fr-par`; EU-native ([docs](https://www.scaleway.com/en/docs/transactional-email/quickstart/), via search) | French company, EU hosting (OVH, Google Belgium) with some US sub-processors (via search) | Cloudflare, a US company; data location not stated on the limits page |
| **Per-tenant isolation** | A domain each (suppressions, DKIM, stats per domain); **subaccounts Enterprise-only** | **Tenants** (Aug 2025): per-tenant identities, reputation metrics, **automatic pause**, optional **tenant-level suppression list**; 10,000 tenants default ([SES tenants](https://docs.aws.amazon.com/ses/latest/dg/tenants.html)) | Servers (account API) and message streams; domains 5–unlimited by plan | Domains; 10 on Pro, 1,000 on Scale | Domains; 5 on the entry tier, unlimited on the €80 Scale tier | Sub-accounts, tied to higher tiers (via search) | Domains per zone: **30** ([limits](https://developers.cloudflare.com/email-service/platform/limits/)) |
| **Domain API** | Yes, with verify | Yes (identities, Easy DKIM) | Account API: servers, domains, DKIM verify and rotate (via search) | Yes (`region` on create) | Yes, returns `dkim_config` | Yes (via search) | Onboarding in the dashboard; API **UNVERIFIED** |
| **Idempotency** | **None** | **None documented** (general knowledge, not found in search) | Not found | **Yes**: `Idempotency-Key` header, 24 h, 256 chars, 409 on payload mismatch or concurrency ([docs](https://resend.com/docs/dashboard/emails/idempotency-keys), via search) | Not found | Not found | Not found |
| **Events** | HMAC webhooks, 8 h retries, Events API 30 d | SNS or EventBridge via configuration sets | Webhooks | Webhooks | Webhooks **in beta**, via SNS topics | Webhooks | **UNVERIFIED** |
| **Price at ~20k / ~90k messages a month** | Foundation $35 / Scale $90 | à la carte $0.10 per 1,000 → ~$2 / ~$9, plus $0.005 per tenant per month and per 1,000 tenant-tagged ([pricing](https://aws.amazon.com/ses/pricing/)); plans add $105–$500 a month | Page read as Pro $16.50 + $1.30 per 1,000 over 10k → ~$30 / ~$120 (the page's table is garbled: **partly UNVERIFIED**); dedicated IP only for 300k+ senders | Pro $20 (50k) / Scale $90 (100k) | €0.25 per 1,000 → ~€5 / ~€22; Scale tier €80 with 100k and a dedicated IP | **UNVERIFIED** (pricing page 403 to a script) | $0.35 per 1,000 above 3,000 plus the $5 Workers Paid plan → ~$11 / ~$35 ([pricing](https://developers.cloudflare.com/email-service/platform/pricing/)) |
| **What I could not settle** | `h:Message-Id` in MIME, bounce MX, event lag | Production-access process; per-region tenants | Whether an EU region exists (sources conflict) | The US account data | Per-tenant reputation features; limits page (403) | Everything price-related | Whether it is GA; the page does not say |

### 4.1 Reading the table

- **SES has the best fit to the *shape* of the problem** — a tenant is a first-class object
  with its own reputation, an automatic pause policy, EventBridge events when status changes,
  and optionally its own suppression list. It is also the cheapest per message by an order
  of magnitude. What it costs: no idempotency, an AWS account and IAM to run, bounce and
  complaint plumbing through SNS or EventBridge instead of one webhook, and a US parent.
- **Scaleway has the best sovereignty story** and a per-message price close to SES, but a
  smaller feature set: webhooks in beta, per-tenant guard features absent from what I read.
  It is the fallback if tenants or the owner decide that "EU region of a US group" is not
  enough (§8).
- **Resend has the one feature Mailgun lacks that the sketch leans on** — idempotency keys
  — but its EU region moves only the dispatch, not the data.
- **Postmark** is excluded by its own words on residency; **Brevo** is a marketing
  platform first and could not be priced from its own page; **Cloudflare Email Service** is
  attractive because Cloudflare already hosts the zone (§7.2) but is new, caps a zone at 30
  domains and states no region.
- **None of them removes the need for a service of ours** that applies the test list,
  counts usage per tenant, and holds the credentials once: that is T10's job and it is
  provider-independent. What the provider changes is how much of the reputation guard (§6.5)
  we write ourselves.

### 4.2 Recommendation on the provider

**Mailgun EU for the first build, behind a provider-neutral service contract**, because the
owner chose it, its domain API and HMAC webhooks are mature, its pricing is flat and low
at our volume, and a domain per tenant fits. Accept its two weaknesses with the two
mitigations of §3.4. **Keep SES (tenants) as the planned alternate** and Scaleway as the
sovereignty fallback; the contract of §6.2 hides the provider from the instances, so
switching is a service release, not an instance upgrade (`vantigo upgrade` is explicit and
one tenant at a time). Run the §10.4 probes before committing; if the idempotency, bounce
or domain-disable probes go badly, SES tenants is the better first provider.

## 5. Deliverability and DNS

### 5.1 What alignment asks for

DMARC passes when SPF **or** DKIM passes *and aligns* with the visible From domain
([RFC 7489](https://www.rfc-editor.org/rfc/rfc7489)). Alignment is relaxed by default: the
two domains need to share an organisational domain, not be equal. Google's FAQ says the
same: a DMARC record of at least `p=none`, the From domain aligned with the SPF or the
DKIM domain, "only one needs to align", and aligning both is recommended
([Google, sender guidelines FAQ](https://support.google.com/a/answer/14229414)).

| From domain | What must be published | Who can do it | What breaks |
| --- | --- | --- | --- |
| **Ours**, a name under a zone we control (`acme.<zone>`) | SPF TXT, DKIM at `<selector>._domainkey.acme.<zone>`, DMARC inherited from the zone's `_dmarc` | The service, through our DNS API (§7.2) | Name collisions in our own zone (§5.2) |
| **The tenant's own** (`faktura@acme.no`) | Mailgun's DKIM record (TXT or CNAME), SPF `include:` merged into the **existing** SPF record, optionally the tracking CNAME; DMARC is the tenant's own | The tenant's DNS administrator, by hand, verified by `PUT /v4/domains/{name}/verify` | A second SPF record (two make SPF fail); SPF's ten-lookup limit; a tenant policy of `p=reject` with a DKIM record that does not yet verify rejects *every* invoice; a domain the tenant does not control cannot verify, which is the proof of ownership |
| **A shared domain of ours** with the tenant as display name and Reply-To (Xero's model) | nothing per tenant | none | Every tenant shares one reputation; replies go to the From address ([forum](https://productideas.xero.com/forums/939198/suggestions/44960536), "a third of invoices marked as spam", anecdotal) |

The shared-domain row is what the large invoicing products do: Xero sends from
`post.xero.com` and has not offered a custom sending domain
([Xero ideas forum](https://productideas.xero.com/forums/939198/suggestions/44960536));
Stripe sends from `stripe.com` by default and offers a custom domain **that must carry a
DMARC policy** ([Stripe](https://docs.stripe.com/email-domain)). Vantigo chose a domain per
tenant (T10) for reputation isolation, which is the stronger design and the one this
document keeps; the cost is the DNS work of §5.2 and §7.2.

### 5.2 `<tenant>.vantigo.cloud` collides with the tunnel's wildcard

T1 and T10 put the system mailbox and the hosted sending domain at `<tenant>.vantigo.cloud`.
That name is also the tenant's web host. `vantigo-infra` publishes **one wildcard DNS
record** pointing every `<name>.<domain>` at the Cloudflare tunnel, and no per-tenant record
(`README.md:247-276`: `cf tunnel route dns vantigo '*.example.com'`, "nothing is ever
clicked in Cloudflare per tenant"; the zone is on Cloudflare because `route dns` exists only
for Cloudflare zones; the registrar and the record's exact type are not visible from the
repositories). Cloudflare's rule for wildcards, read 2026-11-01
([wildcard DNS records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/)):

> a wildcard "applies only when no exact record exists at the queried name"; a record at
> `abc` also keeps the wildcard from covering `123.abc.example.com`.

and, for a proxied record, queries for other record types on that name are not supported
([record types](https://developers.cloudflare.com/dns/manage-dns-records/reference/dns-record-types/)).
Mailgun needs an **SPF TXT at the sending domain itself** (verification wants valid SPF and
DKIM). Publishing it at `acme.<zone>` creates an exact record at the web host's name: the
wildcard stops answering for `acme.<zone>` and the tenant's site goes dark, and a TXT
cannot be added beside an explicit proxied CNAME either. The page also notes that on
Enterprise "advanced nameservers", which follow RFC 4592, a wildcard may not cover even an
*empty* name above a deeper record, so a record at `smtp._domainkey.acme.<zone>` could do
the same there.

Three ways out:

| Option | Sending address | For | Against |
| --- | --- | --- | --- |
| **(a) a label below the tenant** | `faktura@mail.acme.vantigo.cloud` | one zone; the brand stays | works only while the zone is a standard Cloudflare zone (the RFC 4592 caveat); the app zone's token edits mail records and the other way round; mail reputation shares the registrable domain with the product and the company's own mail |
| **(b) a separate registrable zone for mail** | `faktura@acme.<mail-zone>` | no collision with the tunnel, ever; a Cloudflare token scoped to **the mail zone only**; `p=reject` on the mail zone can never hurt company mail; a spamming tenant cannot damage `vantigo.cloud` | one more domain to own, renew and publish DMARC for; the From domain does not show the brand (the display name and Reply-To do) |
| **(c) explicit per-tenant records for the web host** | `faktura@acme.vantigo.cloud` | the address the spec describes | infra gives up its wildcard for per-tenant records and a CNAME and a TXT still cannot coexist at the web host: this does not work at all unless the web host moves |

**Recommendation: (b), a separate registrable zone for mail**, with (a) as the fallback if
a second domain is refused. This amends T1 ("the system mailbox at `<tenant>.vantigo.cloud`")
and T10 ("`<tenant>.vantigo.cloud` for every tenant"). It also fits the infra convention
that the narrow credential is kept and the broad one deleted (`cert.pem` "can create
tunnels and DNS records in the whole account", `README.md:274`): a DNS token for the
service that edits one throwaway-able zone is that convention applied.

### 5.3 DMARC on `vantigo.cloud`, and what `p=reject` means for subdomains

- With **no** `sp=` tag, the organisational domain's `p` applies to its subdomains; a
  subdomain with its own `_dmarc` record overrides it. A `p=reject; sp=reject` on
  `vantigo.cloud` makes receivers reject mail that fails DMARC *claiming* to be from
  `anything.vantigo.cloud`, including every tenant host name, which send no mail at all.
  That is protective and costs nothing for tenants, **but** it applies to every other sender
  of `vantigo.cloud` mail: staff mailboxes, the company's support address, GitHub or CI
  notifications. Inventory those before the switch, and publish `rua=` aggregate reports to
  watch for a month under `p=none` first.
- Tenant web hosts publish no SPF (a TXT there would break the wildcard, §5.2), so DMARC
  rests on DKIM alone for anything that ever sends as them; with `p=reject` that is fine:
  nothing legitimate does.
- Under option (b) the mail zone carries its own `_dmarc` with `p=reject; adkim=r;
  aspf=r; rua=…`, from the first day: only the service sends from it.

### 5.4 Reputation isolation

| Layer | Isolated per tenant? | How |
| --- | --- | --- |
| IP address | **No** on shared pools; yes only with a dedicated IP, which is per account or pool, not per tenant | Mailgun dedicated IP $59 a month; its pricing page offers dedicated IPs from 50k volume; a dedicated IP at ~100k messages a month in total has no warm reputation of its own |
| Sending domain | **Yes**, one Mailgun domain and one DKIM key each | §5.2(b) |
| Suppression lists | **Yes**, per domain at Mailgun | but a hard bounce at one tenant is not known to the next tenant |
| Mailbox providers' view | Gmail judges domain reputation; Google counts volume **by primary domain**, so "volume from subdomains adds to the parent domain's total" and a sender who reaches 5,000 a day to Gmail **once** is a bulk sender for good ([Google FAQ](https://support.google.com/a/answer/14229414)) | whether `<mail-zone>` can be listed on the Public Suffix List so that each tenant is its own primary domain is a **question to put to the PSL maintainers**, not a fact |

**Warm-up.** Shared pools need none; a brand-new domain starts with no history. The cheap
mitigation is the service's own ramp (§6.5): a tenant's first 14 days are capped at tens of
messages a day, and the first messages are the tenant's own staff invitations, which are
recipients likely to open and not complain. A dedicated IP **would** need a four to
eight-week warm-up (general practice, not a Mailgun claim) and is not recommended at this
volume (§10).

### 5.5 The 2024-onward bulk-sender rules, and transactional mail

| Rule | Gmail | Outlook.com (Microsoft) |
| --- | --- | --- |
| Threshold | close to 5,000 messages in 24 h to personal Gmail accounts; **permanent** once met | 5,000 or more a day to Outlook.com, Hotmail, Live and MSN |
| Authentication | SPF and DKIM for everyone; DMARC `p=none` minimum for bulk senders | SPF, DKIM and DMARC aligned, in force since 5 May 2025; non-compliant mail rejected with `550 5.7.515` ([Microsoft](https://support.microsoft.com/en-us/outlook/fix-ndr-error-550-5-7-515-in-outlook-com), search snippet only) |
| One-click unsubscribe (RFC 8058) | **Marketing and promotional messages only; "transactional messages, such as password resets, reservation confirmations … are exempt"** | not a requirement of that rule (**UNVERIFIED**) |
| Complaint rate | keep user-reported spam below **0.1 %**, never reach **0.3 %** | not published as a number in what I read |
| Enforcement | gradual; temporary 4.7.x then permanent 5.7.x errors; ramping from November 2025 | rejection |

So **invoices, reminders, invitations and password resets are transactional and exempt from
one-click unsubscribe**, but are *not* exempt from authentication and the spam-rate ceiling,
and "recipients, not Google, decide what counts as promotional". Three consequences:

1. Do not add a List-Unsubscribe header or an unsubscribe footer to these; do honour a
   recipient's request to stop through Communications' suppression list (the user guide's
   *Block an address*).
2. **Business recipients are mostly on Exchange Online, not Outlook.com**, and judge by
   DMARC: a Norwegian customer behind Microsoft 365 will quarantine or reject a message that
   fails a published `p=reject`. Alignment matters for B2B more than the consumer bulk rules.
3. A fleet of 200 tenants, ~87,000 messages a month, is ~3,000 a day on average and could
   pass 5,000 a day on a month-end peak, all of it under one primary domain (§5.4). The
   authentication and complaint rules therefore apply from the start; build for them now.

Gmail Postmaster Tools and Microsoft's SNDS give the domain-level reading; both need the
sending domain verified, and a low-volume domain may show no data (general knowledge).

## 6. The service's shape: T10 as sketched, and what the research changes

T10 (`spec:373-399`) fixes four things: `GET /api/v1/tenant`; `POST /api/v1/messages` with
a whole RFC 5322 body, `Idempotency-Key: <Message-ID>`, `X-Vantigo-Purpose`, answers 202,
403, 413, 429, 401, and "the same key within 7 days answers the first outcome"; a signed
push of delivery events to `POST <APP_URL>/api/v1/communications/hosted-mail-events`; a
Mailgun domain per tenant at `<tenant>.vantigo.cloud`. The consumer side (T8, T9) is the
platform client `srv/hostedmail` under S7's guard, the `hosted` channel provider, and
`MAIL_DRIVER=vantigo`.

### 6.1 Idempotency: the service must keep its own key store

Mailgun has none (§3.1), the contract is at-least-once (§2.2), and T10 promises a repeat
"answers the first outcome". That promise is the service's to keep. Design:

| State of a key (`tenant_id`, `key`) | A repeat with the same body hash | A repeat with another body hash |
| --- | --- | --- |
| absent | the first request: insert `pending` with a lease (60 s), call the provider | — |
| `pending`, lease alive | `409 in_progress` with `Retry-After` (the Resend model) | `422 key_reused` |
| `accepted` | the stored answer, `202` with the original `id`, **no send, no usage** | `422 key_reused` |
| `refused` (policy: test list, suspended) | the stored 403 | `422 key_reused` |
| `pending`, lease expired | **ambiguous**: the provider may have accepted | — |

The ambiguous row is the honest hard case: a crash between Mailgun's `200` and the write of
`accepted`. Resolution: ask Mailgun's Events API for an `accepted` event for that message
(by our Message-ID if Mailgun kept it, otherwise there is nothing to search for, which is
why the probe in §10.4 comes first), wait out the unknown lag, and **resend only if none
shows**. The residual window is small and the contract already allows it ("a failure after
the provider took the message may send it again"). What the store buys is that a retry
after a *lost response*, the common case, neither sends nor bills twice.

Sizing: Vantigo's own retries are minutes (outbox) to 48 h (reminders), so **keep keys
72 h**, not 7 days; a row is a few hundred bytes, so even 200 tenants × 3,000 messages is
negligible; expire with a nightly delete.

### 6.2 The contract, revised

| T10 says | The research says | Revised |
| --- | --- | --- |
| Body is a whole RFC 5322 message | `mail.Outbound` carries `Bcc`, which MIME rendering drops by default (`outbound.go:134`); the service must know the real recipients and must not trust headers for policy | `POST /v1/messages` takes `multipart/mixed` or JSON `{envelope:{from, to[], cc[], bcc[]}, mime}`; the service builds the provider call from the **envelope**, rejects a MIME whose To/Cc differ from it, and strips a Bcc header before sending |
| 202 with `{id}` | Mailgun answers "Queued", not delivered | keep 202, name it: `{id, acceptedAt, providerId}`; "accepted" never means "delivered" and the channel card says so |
| `403 recipient_not_allowed {recipients[]}` in test mode | the list must cover **every** envelope recipient, including Cc/Bcc, and the system mail (T9) | keep; check envelope recipients only; an address is compared case-insensitively on the normalised form |
| 413, 429, 401 | add what the new states need | `409 in_progress`, `422 key_reused`, `403 tenant_suspended` (a reason code), `503 provider_unavailable` (retry), `409 domain_not_verified` while DNS is pending; maximum message **20 MB** of MIME (Mailgun's 25 MB is uncompressed MIME, and receiving servers commonly cap near it; a limit under both is a service decision) |
| Idempotency 7 days | outbox horizon is minutes, reminders 48 h | 72 h |
| `GET /api/v1/tenant` returns mode, domain, list, usage, `inbound:false` | the domain has a lifecycle | add `status` (`provisioning`, `active`, `paused`, `suspended`), `domain.state` (`pending_dns`, `verifying`, `verified`, `failed`) and `senders` (the From addresses allowed) |
| Events pushed, HMAC, 5-minute window, retried for a day | a push needs a public endpoint on every tenant, retry bookkeeping on our side, and a replay cache; the instances already poll their own sources (spot prices, mailbox sync) | **pull**: `GET /v1/events?after=<cursor>` returning `{messageId, event, reason, at}` for the tenant; the Communications worker polls each minute and marks messages; a hard bounce adds the Suppression as T8 says. No tenant endpoint, no signature on the tenant side, no replay window. Keep the push as an option only if latency under a minute is ever required |
| Usage `{sent, allowance, periodEnd}` | counted when? | counted at **accept**, per recipient, from the key store (so a repeat is free); `bySource` (`system` and `channel`, T9's header) |
| Domain `<tenant>.vantigo.cloud` | §5.2 | `<tenant>.<mail-zone>` |

The headers and the policy rules (the test list, pacing, suspension) are the service's;
nothing about them is the instance's, which is the point of putting them in one place.

### 6.3 The hosted channel can send but not receive

Two consequences T8 does not say.

**Calendar replies.** The invitation's `ORGANIZER` is the channel's From address
(`calendar/ics.go:232`). When an attendee clicks *Accept* in Outlook, the reply goes there.
A hosted address has no mailbox and, without an MX record, no way to receive (bounces
included, §10.4), so the attendee's client reports a failure or the answer vanishes. The
invitation also carries an RSVP link (`invitations.go:255`), which is the real path for
Vantigo, but the mail-client path will be tried first by many. Options, in order of cost:
(1) ORGANIZER stays the owner's own address with `SENT-BY` naming the channel (RFC 5545
supports it; how Outlook and Gmail treat it is **UNVERIFIED**); (2) the channel's domain
gets an **inbound route** at the provider that forwards replies to the Reply-To (Mailgun
Routes, "forward"), which is the `inbound: true` T10 reserved, built for one purpose; (3)
accept the loss and say so in the user guide. This is a design decision for the Calendar
module and the owner (§11.5), not for the service alone.

**Free text.** T8 makes `hosted` a Communications channel provider, so a user can start a
conversation and write to *any address* in free text from our domain and our reputation.
Every other sender in §2.1 is templated. A send-only channel has no inbound to teach us
which addresses are real, and a tenant's own SMTP channel sends the same text on the
*tenant's* reputation. Recommendation: **in the first release the hosted provider is
reachable only through `OutboundMail`** (Invoices, Calendar, and later modules), not as a
conversation channel in Communications; the channel form shows it for those two settings
and refuses it for a conversation. Free text can come later with per-tenant daily caps and
a content rule, after the abuse data of §9.

### 6.4 What the service stores

| Data | Kept? | Why |
| --- | --- | --- |
| Message bodies, attachments | **No** | the service is a pass-through; Mailgun holds them 0–7 days by plan (§8) |
| Recipient addresses | **No**; keep a domain-level count (`gmail.com`: 41) and, for the key store, a hash | a tenant's customer addresses would make the service a second personal-data store that the tenant's *erase* (customers' anonymisation) cannot reach |
| Per tenant: mode, test list, allowance, state, domain, provider ids | Yes | policy and billing |
| Key store (72 h) | Yes | §6.1 |
| Usage counters per tenant and day | Yes, 13 months | billing and abuse |
| Events | Cursor-addressed, 14 days, `messageId`, event, reason (no address) | the instance pulls them |

The test list is the exception: a test tenant's list is addresses by definition, but they
are the operator's own testers, not customers.

### 6.5 Test mode, usage, and the reputation guard

**What the test-recipient list buys.** A test or demo instance (`vantigo add --seed`, a
staging restored from a production dump, a training tenant) with real-looking customers
cannot mail them: reminders and invoices from a restored copy are the classic incident, and
the list is enforced *outside* the instance, so a test tenant's Owner cannot lift it. It
protects our reputation from our own mistakes. **What it does not do:**

- It binds **only the hosted provider.** A test tenant can still create an SMTP or
  Microsoft 365 channel and write to anyone. If test tenants must be harmless, the instance
  needs a tier-1 switch that withholds the other providers (an `MAIL_TEST_MODE` that makes
  the channel form offer hosted-only), or the infra smoke test must not create such tenants.
  This is a gap in T7, not in the service.
- It does nothing about a **paying** tenant that spams. That is the guard below.
- A list of addresses at one domain (`@acme.no`) is a wildcard; allow exact addresses and
  whole domains separately, and default to exact.
- Mailgun's own `o:testmode=yes` (process, don't deliver) is a second, different layer:
  useful for a "dry run" mode that proves the pipeline without a recipient, but silent,
  which T8 rejects.

**The guard.** The service computes, per tenant, rolling hard-bounce and complaint rates
from the events it receives and acts before the provider does: warn above 2 % hard bounces
or 0.05 % complaints, **pause** above 5 % or 0.1 % (numbers are starting points taken from
Google's 0.1 % and the SES finding of a bounce rate over 15 %, to be tuned with data), with
a ramp (§5.4) and a daily cap for new tenants. A pause is `403 tenant_suspended`, the
channel card says "sending is paused; contact support", and the pause lifts by an operator
action, never by the tenant. This is SES tenants' automatic policy, reimplemented; with SES
as the provider it would be configuration.

### 6.6 The platform client and the system mailbox (T9)

Sound as written, with these amendments: `MAIL_DRIVER=vantigo` should be a **separate
Mailgun account** (§3.4); its tenant `mode=test` list applies to invitations, which blocks
inviting a real colleague on a test tenant on purpose; and the **service being down must
not be silent for system mail**. `identity/recovery.go:52` swallows the failure and answers
"accepted" (by design, so an answer never tells whether an account exists); a failed
invitation is **revoked** at once and the failure returned (`identity/invitations.go:132-150`),
so a service outage means no invitation can be issued and no password recovered, on every
tenant at once. Either the driver retries for a short while, or
`MAIL_DRIVER=smtp` stays available as the break-glass path for an operator (the tiers allow
it: the choice is the operator's, in the environment). T9's `Driver != "log"` change
(`spec:365`) must treat `vantigo` as a real driver everywhere `Driver == "smtp"` is read
today; the brief lists the greps.

## 7. Operations

### 7.1 Deployment beside `vantigo-spotprices`

What exists. `vantigo-spotprices` is one Go binary (`api`, `worker`, `migrate`, `backfill`,
`keys`, `healthcheck`) with its own PostgreSQL, goose migrations under an advisory lock,
bearer keys stored as SHA-256 and created by a CLI "never over HTTP", a per-key limiter
(600 a minute, 429 with `Retry-After`), problem+json errors, OpenAPI-first with generated
servers, about **6,100 lines of production Go and 5,500 of tests**
(`vantigo-spotprices/{cmd,internal,openapi}`, README "The API"). In `vantigo-infra` it is
on the branch **`feat/spotprices-service`**, *not on `main` at the time of reading*: a
second Docker network `spotprices` created `--internal` that every tenant joins, its
database on the shared PostgreSQL over the socket, and `vantigo spotprices init|key|…`
(`README.md` on that branch, lines 68–83, 360–383; compose in `services/spotprices/`).

What a mail service needs that spotprices does not:

| Need | spotprices | `vantigo-mail` |
| --- | --- | --- |
| Reached by tenants over a private network | yes | yes (the same internal network, S7's `http`-to-private exception) |
| Outbound internet | to Nord Pool, ENTSO-E, Norges Bank | to Mailgun and the DNS provider: so **two networks**, the internal one for tenants and an egress one; `--internal` cannot be its only network |
| Inbound internet | none | none, **if events are pulled** (§6.2); a push from Mailgun would need a tunnel hostname and signature handling |
| Secrets | an optional ENTSO-E token | the Mailgun API keys (two accounts), the Mailgun webhook signing key if used, the DNS API token: three classes, held in one place, which is the main argument for a service over putting them in every tenant `.env` |
| State | public data; key table | tenants, keys, key store, usage, events: small, but **billing evidence** |
| Availability | tenants copy and keep working | **sending stops** (and password resets with it) |

Size, as a judgment from spotprices: the service is **smaller** than spotprices in domain
logic and larger in policy; plan on the same order, roughly 3,000–4,500 lines of Go plus
tests, 6–8 working weeks for one developer including infra, and 2–3 weeks for the consumer
side (the C′ brief's list: config, `hostedmail` package, provider, events worker, form,
docs in both languages). Nothing here is measured; it is the owner's call whether that
weighs against §10.

### 7.2 DNS automation

- **Provider.** The zone is on Cloudflare (inferred: `cf tunnel route dns` is Cloudflare's
  command and the README builds the whole tenant lifecycle on one wildcard record,
  `README.md:247-276`). The registrar and plan are not visible from the repositories.
- **API.** Cloudflare's REST API creates and deletes records per zone with a scoped token
  (*Zone → DNS → Edit* on one zone); 1,200 requests per five minutes per token, 429 with
  `retry-after`, 50 user and 500 account tokens
  ([API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/)).
  A new tenant costs about four calls (SPF, DKIM, optionally a CNAME), so rate is not a
  constraint for hundreds of tenants.
- **Flow, as a reconcile loop in the service**, not a script in `vantigo add`:
  1. create the Mailgun domain (EU, `force_dkim_authority=true`, tracking off, 2048-bit DKIM,
     Automatic Sender Security if the plan allows, so DKIM is a stable CNAME);
  2. publish the records the answer lists;
  3. call `PUT /v4/domains/{name}/verify` with backoff until `verified` or 24 h, then
     `failed` and an alert;
  4. mark the tenant's domain `verified`; until then `POST /messages` answers
     `domain_not_verified`.
  Each step is idempotent and the loop survives a restart. `vantigo add` only asks the
  service to create the tenant.
- **Failure modes** worth a test: a record that already exists with a different value; a
  Cloudflare 5xx mid-way; Mailgun's verification latency; the tenant deleted while DNS is
  pending. **Deletion keeps the domain disabled for 30 days** rather than deleting it,
  because deleting at Mailgun removes its suppression list (UNVERIFIED how, but the lists
  are per domain).
- **A tenant's own domain** (spec Q1) swaps step 2 for "show the Owner the records" and step 3
  for the Owner's *Verify* button; the risk list of §5.1 applies.

### 7.3 Key provisioning in `vantigo-infra`

Follow the spot-price pattern (S9; branch README `:360-383`): `vantigo add` creates the
tenant in the service and writes `MAIL_SERVICE_URL` and `MAIL_SERVICE_KEY` into the
tenant's `.env` (mode 600) next to the spot-price pair; `vantigo mail key <name>` rotates
gap-free (write the new key, restart the tenant, revoke the old one); `vantigo mail mode
<name> test|production` and `vantigo mail list|pause|resume` are the operator's levers and
change the service only; `vantigo remove` revokes. Keys are `mk_…`, prefixed so a leak is
greppable; stored as SHA-256; the CLI creates them inside the service container, never over
HTTP. The tenant template's host-wide `SMTP_*` block stays for hosts that have not moved
and as the break-glass path (§6.6).

### 7.4 Monitoring

| What | How | Alert when |
| --- | --- | --- |
| Service up, provider reachable | `/health/ready` as spotprices; a synthetic send to a test tenant every 15 min | two failures |
| Per tenant bounce and complaint rate | from the events | warn/pause thresholds of §6.5 |
| Domain lifecycle | count of tenants not `verified` for more than an hour | any |
| Mailgun account standing | the account's domain states via API; the sending counts against the plan | any `disabled` domain; 80 % of the plan |
| Cloudflare token | a daily read of the zone | failure |
| Mailbox providers | Google Postmaster Tools on the mail zone, DMARC aggregate reports (`rua`) into a reader | a reputation drop; any DMARC failure from our zone |
| Instance side | the existing OpenTelemetry export; a metric per hosted send outcome | failure rate above 2 % |
| Cost | monthly usage against the plan's inclusion | 90 % |

### 7.5 Cost model

Assumes §2.3's volumes and Mailgun's marginal overage of $1.30 per 1,000 (Foundation).
USD as the vendor prices; infrastructure on the existing host is treated as free.

| | Messages / month | Provider cost, marginal | Share of a plan |
| --- | --- | --- | --- |
| **Small** tenant | ~70 | $0.09 | — |
| **Medium** tenant | ~520 | $0.68 | — |
| **Large** tenant | ~2,350 | $3.06 | — |
| **50-tenant fleet** (30 S + 15 M + 5 L) | ~21,650 | Foundation $35 flat (50k included) | $0.70 per tenant |
| **200-tenant fleet** (120 S + 60 M + 20 L) | ~86,600 | Foundation $35 + 36.6k × $1.30 = ~$83, or Scale $90 flat | ~$0.45 per tenant |
| Alternatives at the 200-tenant fleet | | SES ~$9 + tenants; Scaleway ~€22; Resend Scale $90; Cloudflare ~$35 | |
| Optional | | dedicated IP $59 a month; DMARC reader tool; second Mailgun account (another $35) | |

**Sending is not the cost.** At every fleet size the provider bill is lower than one hour
of engineering a month. The costs are building the service (§7.1), operating it, the second
Mailgun account for system mail, and the support burden of a tenant whose domain will not
verify or whose invoices land in spam. Whether hosted mail is sold, metered or
included in a plan is §11.6; at these costs any usage price above a few kroner per hundred
messages already carries a margin.

## 8. Legal and data protection

This section states what the sources say and what follows structurally; it is not legal
advice, and the DPA wording, the sub-processor notice and the transfer analysis need a
lawyer's reading before a tenant is told anything.

### 8.1 Who is what

| Party | Role for a customer's data in a mail | Basis |
| --- | --- | --- |
| The **tenant** (the company using Vantigo) | **Controller** of its customers' names, addresses, invoice contents | it decides to write to them |
| **Vantigo** (us), running the instance and the hosted service | **Processor** for the tenant | Art. 28; the service is part of the processing we already do for the tenant (hosting the instance) |
| **Mailgun** (Sinch Email, contracting entity Mailgun Technologies Inc.) | **Sub-processor** of ours | Mailgun's DPA makes the client the controller and Sinch Email the processor, "except where the customer itself acts as a processor, in which case Sinch Email is a sub-processor" ([DPA §4(a)](https://www.mailgun.com/legal/dpa/), an archived version revised 2023-01-16: **the current text must be re-read**) |
| **Cloudflare** | Sub-processor for DNS names only, which hold tenant slugs, not personal data; already in the chain for the tunnel | `vantigo-infra` README |
| The hosting provider of the service | Sub-processor of ours, as for the instances | — |

What a mail contains: a recipient's name and e-mail address, an invoice PDF (the buyer's
address, line items, amounts, sometimes a KID or personal identifier), a reminder letter,
an appointment (time, place, attendees, sometimes a description). These are ordinary
personal data; invoices to consumers are the sensitive end because the buyer is a person.
The tenant's customers have not chosen Vantigo or Mailgun; the tenant's privacy statement
and our sub-processor list are how they would learn of either.

### 8.2 What Mailgun's terms and documentation say

| Topic | Finding | Source |
| --- | --- | --- |
| EU region | Message data, event logs and suppressions stay in the region; account data, billing, API keys and **domain names replicate globally** | [API overview](https://documentation.mailgun.com/docs/mailgun/api-reference/api-overview.md) |
| Infrastructure location | For European customers: Google Cloud data centres in **Germany and Belgium**, Rackspace in Germany | DPA Annex 3 (2023 text) |
| Transfers | EU SCCs (Decision 2021/914) and the UK Addendum, with encryption and minimisation as supplementary measures; the 2023 text does **not** mention the EU–US Data Privacy Framework. Sinch's trust page says Mailgun Technologies has self-certified; whether it is on the active list is **UNVERIFIED** and is checked at dataprivacyframework.gov | DPA §12(c); [trust.sinch.com](https://trust.sinch.com/) (search snippets, wording dated) |
| Sub-processors | Annex 3 lists them; the client has **10 business days** after a notice to object, after which either party may terminate | DPA §13(b) |
| Retention after termination | deleted or returned within **90 days**; backups that cannot reasonably be deleted stay un-processed | DPA §11(b) |
| Security | AES-256 at rest, TLS 1.2 in transit, MFA on support tools, ISO 27001 and 27701, SOC 2 | DPA Annex 2 |
| Audit | on request, certificates and reports first; an audit only if those are not sufficient, at the client's cost | DPA §10 |
| Breach notice | "without undue delay", no fixed hours | DPA §8 |
| US parent | Sinch is Swedish; the operating entity is American and account data replicates globally; commentators argue the CLOUD Act can reach data an American company controls wherever it sits (a vendor-comparison blog, not relied on) | search |

What follows:

- **We can offer tenants a sub-processor list that includes Mailgun, EU region,** provided
  the DPA is re-read in its current form and the transfer mechanism confirmed. A tenant
  whose own policy bars US-group processors will say no; that is the lever for Scaleway or
  another EU-native provider (§4, §11.7).
- **Our promise to tenants about sub-processor changes must be at least as long as
  Mailgun's to us.** Mailgun's 10 business days is their window to object; ours to a
  tenant should add time for the tenant's own notice. Also decide who contacts whom.
- **Breach chain.** Art. 33(2) obliges us to tell the controller "without undue delay"
  after we become aware; Mailgun's duty to us is the same open phrase. Our notice does not
  wait for theirs to be precise.
- **The service itself, if it stores no body and no address (§6.4), is not a second
  personal-data store.** If a future version queues messages, it becomes one, with
  retention and erasure of its own to document.

### 8.3 Retention at the provider, and what can be turned off

| Data at Mailgun | Retention | Can we shorten or disable? |
| --- | --- | --- |
| Message bodies and headers (the MIME) | **0–7 days** by plan, set **per domain** in domain settings (Foundation 1 day, Scale and Enterprise up to 7, default 3; Free 0) ([help centre](https://help.mailgun.com/hc/en-us/articles/8841411163035), 403 to a script, via search); the send page says 72 h by default | Yes: set the minimum per domain, and `message_ttl` on domain creation. The service sets the shortest the plan allows |
| Event logs (recipient, subject, status, timings) | 1 to 30 days by plan (Free/Basic 1, Foundation 5, Scale 30) | Not disable; the plan chooses. **Foundation's five days is the lighter footprint** |
| Webhook payloads | carry recipient addresses | Yes: `webhooks_redact_pii`, account-level, cascades to domains ([release note](https://www.mailgun.com/?p=13430), via search). With **pull** events (§6.2) we do not use Mailgun webhooks for tenant data at all |
| Suppression lists | until removed | per domain; contain addresses of people who bounced or complained; an erase request must reach them |
| Statistics | aggregated, no content | — |

**Erasure.** Vantigo's customer anonymisation (invoices design D6) rewrites our rows. It
cannot reach a Mailgun log: the data ages out in 1–30 days and a suppression entry stays.
The tenant's records of processing should say so, and the operator needs a documented way to
delete one address from a tenant's suppression list at Mailgun on request.

### 8.4 Other obligations touched

- **Transactional versus marketing.** Invoices, reminders and calendar invitations are not
  marketing; no consent or unsubscribe is required under the rules of §5.5 and (as I read
  Norwegian practice, **not checked**) markedsføringsloven's e-mail rule. That is a second
  reason to refuse promotional content in the hosted service's terms (§1.2).
- **Bookkeeping.** An e-mailed PDF stored as proof of delivery is the tenant's evidence;
  the service's usage and acceptance record is not a substitute, and the product already
  logs deliveries on the document (`invoices.deliveries`, `send.go:340-347`).
- **The Norwegian B2B e-invoicing duty from 2027-01-01** shrinks the legitimate use of
  e-mailed PDF invoices between businesses (§2.3). The service should not be sold as the
  way to send invoices to Norwegian companies.

## 9. Risks, and how each is mitigated or accepted

### 9.1 How other products handle a tenant that spams

Sourced from vendor documentation where cited; the rest is general practice and says so.

- **The platforms' own pattern is isolation plus suspension per customer**: SES tenants
  pause one tenant automatically on reputation findings ([SES](https://docs.aws.amazon.com/ses/latest/dg/tenants.html));
  Mailgun subaccounts can be disabled or capped monthly (Enterprise); other transactional
  vendors (AhaSend, MailChannels, Mailjet) describe per-customer sub-accounts with caps and
  suspension for the same reason (vendor pages, via search).
- **Invoicing products avoid per-customer reputation by owning the domain**: Xero and
  Stripe send from their own domain by default (§5.1), accept a bounded reputation risk,
  and Stripe makes a custom domain opt-in with DMARC required.
- **General practice (not sourced here)**: verify domain ownership before sending; start
  every new sender at a low daily cap and raise it on clean metrics; throttle first and
  suspend second on bounce, complaint and spam-trap thresholds; keep an appeal path; forward
  complaints to the customer; ban promotional content in the terms.

### 9.2 Register

| # | Risk | L | I | Handling |
| --- | --- | --- | --- | --- |
| R1 | One tenant spams or mails a bad list; Mailgun disables the **account** and every tenant stops | M | High | Two Mailgun accounts (system apart from tenant mail); own guard (§6.5); per-tenant caps and ramp; AUP; accept the residual |
| R2 | Reputation of the shared zone drops (Gmail counts by primary domain) | M | High | Separate registrable mail zone (§5.2b) so the product's domain and company mail are untouched; Postmaster Tools; PSL question; accept residual |
| R3 | Service outage: no invitations or resets fleet-wide | L | High | Break-glass `MAIL_DRIVER=smtp`; short retry in the driver; two replicas; alert on synthetic send |
| R4 | A crash between Mailgun's accept and our record sends twice | L | Low | Key store; Events-API check; the contract already allows at-least-once; **accept** |
| R5 | Mailgun rewrites Message-ID; events cannot be correlated | ? | Med | Probe first; map by Mailgun `id` anyway |
| R6 | Tenant web host breaks when DNS records are added at its name | H *if* `<tenant>.vantigo.cloud` | High | Do not; separate zone (§5.2) |
| R7 | The Cloudflare token leaks and rewrites DNS | L | High | A token scoped to the mail zone only; kept in the service only; rotate; the broad credential stays unused (infra convention) |
| R8 | Calendar RSVP by mail bounces on hosted channels | H | Med | §6.3 options; decide before launch |
| R9 | Free-text channel used for cold mail | M | High | Hosted only through `OutboundMail` in v1; caps; content rules later |
| R10 | Test tenant mails real people through its SMTP or M365 channel | M | Med | Instance switch withholding other providers on test tenants; the smoke test |
| R11 | A tenant's own domain with a wrong SPF or a premature `p=reject` | M | Med | Verify before enabling; provide the exact record; keep own-domain as a later delivery (spec Q1) |
| R12 | Deliverability to Microsoft 365 recipients (B2B) is worse than expected from new domains | M | Med | DMARC aligned from day one; ramp; test sends to M365 and Gmail in the smoke test; recommend the tenant's own M365 channel where they have one |
| R13 | Provider concentration and a US parent; a tenant refuses Mailgun | M | Med | Provider hidden behind the service; SES or Scaleway as alternates; the sub-processor list |
| R14 | Mailgun changes plans, prices or EU terms | M | Low | Flat low cost; the contract is provider-neutral |
| R15 | The service becomes a personal-data store by accident (logging bodies, addresses, queues) | M | Med | §6.4 as a rule in the service's AGENTS.md, as spotprices' "never log a body"; a test that logs are free of addresses |
| R16 | Idempotency window too short for a long retry | L | Low | 72 h covers the reminder worker's 48 h |
| R17 | The 25 MB limit meets a large attachment set | L | Low | 20 MB service limit, `maxMessageBytes` on the channel |
| R18 | Support load: domains that will not verify, mail in spam | H | Med | A status page of the domain in the channel card; operator tooling (`vantigo mail …`); documentation `admin/hosted-mail.md` |
| R19 | We build it and few tenants lack their own mail | M | Med | §10: a first stage that costs no code; build when evidence arrives |

## 10. The decision

### 10.1 The options

| | A. Build `vantigo-mail` as T10 sketches | B. Build it thinner, as §6 revises | C. Buy and configure: per-tenant Mailgun domain and key, driven by `vantigo-infra`, no service | D. Do not build |
| --- | --- | --- | --- | --- |
| What | the full service, `<tenant>.vantigo.cloud`, push events, 7-day keys | pass-through service, separate mail zone, key store, pull events, own guard, no bodies at rest | `vantigo add` creates a Mailgun domain and a **domain sending key**, writes it to the tenant `.env`; the instance calls Mailgun directly (a fifth provider) | tenants bring Microsoft 365, Google (later) or SMTP; system mail on a host-wide SMTP relay |
| Test list, quotas, suspension | in the service | in the service | **in the instance** (an env list) or Mailgun's per-domain tools; a tenant's own container could be altered, the threat is a mistake not an adversary | not applicable |
| Secrets | in the service | in the service | a domain key per tenant in each `.env` (narrow, per-domain); the **webhook signing key is account-wide**, so events go by polling Mailgun's API from the instance or the shared key sits in every instance | none new |
| Provider switch | a service release | a service release | an application release and an upgrade of every tenant | — |
| Idempotency | service | service | instance (the outbox's own); same ambiguity | — |
| DNS | the service | the service | the infra CLI, in bash, with a Cloudflare token on the host | — |
| Cost to build | highest | 6–8 weeks + 2–3 | 2–3 weeks (provider) + infra | zero |
| Fails when | §5.2 collision on day one | provider outage affects all (R3) | a leaked or changed instance sends to anyone; no central view of usage; Mailgun subaccounts are Enterprise, so no account-level split | the tenant has no mail at all |

### 10.2 Why not C, though it is the cheapest build

C moves the policy to where it can be changed by the thing it polices, spreads three
credential classes over every `.env`, ties a provider change to an upgrade of the whole
fleet, and leaves no place to count usage, hold the key store or pause a tenant centrally.
It would be the right answer for **three tenants**. It is the wrong shape for **three
hundred**, and the work is half-done by the time the difference matters.

### 10.3 Recommendation

**B, staged, with D until there is demand.**

1. **Now (zero code).** Keep the greyed entry (delivery C). For the system mail of hosted
   tenants, point the host-wide `SMTP_*` block of `vantigo-infra`'s tenant template at a
   transactional provider's SMTP relay on a domain we own (it is what the template already
   expects, `template/.env.example:60-75`), with SPF, DKIM and DMARC on that domain. This
   ships invitations and resets for every new tenant with no service key, no new service
   and no new failure domain beyond the relay. Register the mail zone and park the
   Mailgun accounts; both have lead time.
2. **Spike (about three days)**, before any commitment: the §10.4 probes against a real
   Mailgun EU domain on the mail zone, plus SES tenants in eu-north-1 or eu-central-1 for
   comparison. If Mailgun's Message-ID, bounce or domain-disable behaviour disappoints, the
   first provider is SES.
3. **Build B** when the owner has evidence of tenants that need it (§11.1): the service, then
   `vantigo-infra`, then the consumer side (C′ brief, amended by §6.2–§6.6), in that order
   and in three pull requests, as the spot-price work was.
4. **Own domains later** (Q1 of the spec), as a separate delivery after B has run a quarter.

**Reasons.** The architecture of the spec is right: one place for credentials, policy, usage
and DNS, and a provider the instances never see. What is wrong is the sending domain, the
absent inbound, the missing idempotency layer and the account-level blast radius, all of
which the revisions fix at a modest cost. The sending itself costs almost nothing (§7.5),
so the decision is about engineering time and operational risk, not about the bill. And the
largest unknown is not technical: whether tenants without any mail account exist in
numbers (§1.2, §11.1). Spending six to eight weeks to find out would be the wrong order.

### 10.4 Probes before building (each a day or less)

| # | Probe | Settles |
| --- | --- | --- |
| P1 | Send a MIME with `Message-ID: <x@vantigo.invalid>` through `/messages.mime` on an EU domain; read the delivered header and the `accepted` event | R5, the correlation design |
| P2 | Does the Events API filter by message-id; how soon is an `accepted` event queryable | §6.1's ambiguous row |
| P3 | Create a domain on the mail zone with `force_dkim_authority=true`, tracking off, Automatic Sender Security; record exactly which DNS records it asks for; does a sending-only domain need MX; where do bounces go | §5.2, §7.2, R8 |
| P4 | Add the records through Cloudflare's API to a throwaway zone; time to `verified` | §7.2 |
| P5 | Add a TXT at a name covered by a proxied wildcard CNAME on a test zone and query both | §5.2's inference |
| P6 | Disable one domain through the API while another sends; is the account affected | R1 |
| P7 | Ask Mailgun sales in writing: subaccounts below Enterprise; EU DPA and sub-processor list; DPF listing; the thresholds for domain disabling and probation | §3.4, §8 |
| P8 | Send invitations and an invoice PDF from a new domain to Gmail, Outlook.com and a Microsoft 365 tenant; read the headers and the folder | R12 |
| P9 | Send an `.ics` invitation from a hosted address; reply with Accept from Outlook and Gmail | R8 |
| P10 | Whether `<mail-zone>` can be listed on the Public Suffix List for this use | §5.4 |

## 11. Questions for the owner

1. **Who is the tenant without a mail account?** A small firm with a webhotel mailbox can
   already use the SMTP channel; one on Microsoft 365 uses Entra; Google Workspace is a
   planned fifth provider. Do you expect enough tenants with *none of these* to justify
   the build now, or is hosted mail chiefly an onboarding convenience (no SMTP credentials
   to find)? This decides whether §10.3 stops at step 1.
2. **A separate mail domain.** Do you accept `<tenant>.<mail-zone>` (a second registrable
   domain you own, the From domain unrelated to `vantigo.cloud`) in place of
   `<tenant>.vantigo.cloud`, given §5.2? If not, do you accept `mail.<tenant>.vantigo.cloud`
   with its caveat? Which name for the zone?
3. **Free-text mail.** May the first release restrict hosted mail to Invoices and Calendar
   (and system mail) and keep it out of Communications conversations (§6.3)?
4. **Test tenants.** Should a test or demo instance be unable to use SMTP or Microsoft 365
   channels at all, so that the test list really holds (§6.5)?
5. **Calendar replies.** For a hosted channel, do you want (a) the owner's own address as
   ORGANIZER with `SENT-BY`, (b) an inbound route forwarding to the owner, or (c) the RSVP
   link only and a documented limitation (§6.3)?
6. **Money.** Is hosted mail included in the plan, metered, or sold as an add-on? Does the
   tenant see usage (T8's `usage`) and an allowance, or only the operator? In which
   currency do you want to carry the provider's USD pricing?
7. **Sovereignty.** Is an EU region of a US-owned provider acceptable to you and to the
   tenants you expect (public sector, health, law)? If not, the first provider is
   Scaleway, with its smaller feature set (§4, §8).
8. **System mail.** Do you want the system mailbox on the service from the first release,
   or on the host-wide relay of §10.3 step 1 until the service has run for a while? Is a
   break-glass `MAIL_DRIVER=smtp` acceptable on a hosted tenant (§6.6)?
9. **Mailgun accounts.** Two accounts (system, customer mail), each on Foundation at $35 a
   month, from day one? Who owns and pays for them, and who receives the provider's
   abuse notices?
10. **Abuse policy.** Who decides to pause a tenant, with what thresholds (§6.5's are
    starting points), and what is the appeal path? Is a tenant told before its mail stops?
    Which terms (acceptable use, no promotional mail) does the hosted service add to the
    customer agreement?
11. **Own domains.** After B, should a tenant's own sending domain (spec Q1) be a paid
    option, and do you accept that the tenant's DNS administrator, not Vantigo, publishes
    the records (§5.1)?
12. **Legal.** Who reviews Mailgun's current DPA, sub-processor list and DPF status, and
    updates the sub-processor notice that tenants receive? What notice period do you
    promise tenants before a provider change (§8.2)?
13. **Dedicated IP.** Stay on shared IPs until the fleet reaches a sustained 100,000+
    messages a month, as recommended, or buy one now for the reputation story (§5.4)?
14. **Where it runs.** On the same host as the tenants and spotprices, or on its own host
    so that a tenant host's outage does not stop everyone's password resets? Is the
    infra branch `feat/spotprices-service` merging before this work starts, since the
    mail service follows its network pattern (§7.1)?
15. **Who opens the accounts for the probes?** The spike of §10.4 needs a Mailgun account in the
    owner's name, a throwaway domain and a Cloudflare token; the probe scripts are small,
    but the accounts are the owner's to open.

## Appendix. What could not be read

Read on 2026-11-01. A fact in this document that rests on one of these is marked
**UNVERIFIED** or "via search".

| Source | What happened | What depends on it |
| --- | --- | --- |
| Mailgun help centre: Subaccounts (`/articles/16380043681435`), "Why has my domain been disabled" (`/202051664`), message retention (`/8841411163035`) | HTTP 403 to a script; read only as search snippets | "subaccounts are Enterprise only", the domain-disable behaviour, the 0–7 day body retention (§3.4, §8.3) |
| Mailgun sub-processor page (`/legal/sub-processors/`) | HTTP 404 | The sub-processor list: only the 2023 DPA's Annex 3 (Google Cloud DE/BE, Rackspace DE) was read; the current list must be requested (§10.4 P7) |
| Mailgun DPA (`/legal/dpa/`) | Read, but it is an **archived version revised 2023-01-16** | Transfer mechanism, sub-processor notice, breach wording (§8.2) |
| Mailgun pricing page | Read; the plan table is flattened, so dedicated-IP and subaccount cells could not be assigned to plans | §3.3 |
| Mailgun `messages.mime` reference, user-manual "send MIME" | page had no parameter detail / HTTP 404 | Whether Message-ID in the MIME is kept (§3.1, P1) |
| Microsoft's Outlook.com sender-requirements pages | only search snippets | The Outlook.com column of §5.5 |
| Brevo pricing page | HTTP 403 to a script | Brevo's column of §4 (search snippets only) |
| Scaleway TEM limits page | HTTP 403 | Scaleway's limits and domain counts (search snippets) |
| Postmark pricing page | read; the table read oddly (Pro $16.50, Platform $18) | Postmark's price in §4 |
| Cloudflare Email Service | pricing and limits pages read; no statement of beta or general availability, data location, or a domain-management API | §4 |
| Amazon SES regions, production access, idempotency | not re-read; general knowledge, and a search that found no idempotency parameter | §4 |
| Xero and Stripe | forum and docs pages via search | §5.1's "what others do" |
| Whether `vantigo.cloud`'s zone is on Cloudflare, its plan, and its registrar | not visible in the repositories; inferred from `cf tunnel route dns` | §5.2, §7.2 |
| `vantigo-infra`'s spot-price service | exists on branch `feat/spotprices-service`, not on `main` when read; the checked-out branch was `feat/staff-and-seed` | §7.1 |

Primary sources used, for the next reader to re-check: Mailgun documentation at
`documentation.mailgun.com` (API overview, domains, messages, webhooks, events, keys,
subaccounts terms); Google's [email sender guidelines FAQ](https://support.google.com/a/answer/14229414);
Cloudflare's [wildcard DNS](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/),
[API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/) and
[Email Service](https://developers.cloudflare.com/email-service/platform/pricing/) pages;
AWS's [SES tenants](https://docs.aws.amazon.com/ses/latest/dg/tenants.html) and
[pricing](https://aws.amazon.com/ses/pricing/); Resend's
[idempotency keys](https://resend.com/docs/dashboard/emails/idempotency-keys); Scaleway's
[pricing](https://www.scaleway.com/en/pricing/managed-services); Postmark's
[EU privacy page](https://postmarkapp.com/euprivacy); RFC 7489 (DMARC), RFC 7208 (SPF),
RFC 8058 (one-click unsubscribe), RFC 5545 (iCalendar) and RFC 4592 (wildcards).
