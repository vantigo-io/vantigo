# Google Workspace module — research and a plan before a design

Research for Vantigo's second external directory: a module of its own, `google`, that
does for a company on Google Workspace what `entra` does for a company on Microsoft 365.
It covers account lifecycle from Staff, groups, photos, sign-in, the directory picker,
a per-person connection, calendar sync and mail channels. The integrations design
already reserves the place (`2026-10-31-integrations-and-configuration-tiers-design.md`
T2: "Google Workspace … is a sibling module filling the same slots … same four tabs").
There are three inputs:

- what the repository holds now (§1);
- what Google's APIs offer and demand (§2–§3);
- what has to change in the neutral modules before a second provider fits (§4).

The proposed deliveries are §6, and the owner's decisions §7.

Google facts were checked on 2026-11-03 against developers.google.com,
knowledge.workspace.google.com (where support.google.com/a now redirects) and
docs.cloud.google.com, and each names its page. Items marked **UNVERIFIED** rest on
community sources or inference. Each needs a test in a real Workspace domain before a
design depends on it. `srv/` abbreviates `apps/server/internal/`, `mig/`
`srv/db/migrations/`, `host/` `apps/host/frontend/src/`. **D**, **M**, **O** and **T**
are the decisions of the Entra, Outlook connection, Outlook sync and integrations
designs.

## 1. What Vantigo does with Microsoft today (`main` at `f3b58a30`)

| # | Feature | Auth with Microsoft | Contract it fills | Provider-neutral? |
| --- | --- | --- | --- | --- |
| 1 | Connection: tenant, client id, rotating secrets, *Test connection*, unused-permission warning (M19) | app-only, client credentials | — (module's own) | entra-only, by design |
| 2 | Account lifecycle from Staff: prepare, activate (licences, Temporary Access Pass), update profile and manager, deactivate with leaver steps, delete; adoption confirmed | app-only | `contracts.IdentityProvisioner` (many providers, keyed by `Key()`) | **yes**, except the account kind (§4.3) |
| 3 | Groups from Staff: security or M365 groups, Teams, licences by group traits | app-only | `IdentityProvisioner` (`GroupTraits`, `EnsureGroup`, `SetMembers`, `DeleteGroup`) | **yes**: the provider declares its traits |
| 4 | Photo push (HR photo or avatar to the directory) | app-only | `IdentityProvisioner.SetPhoto/ClearPhoto` | **yes** |
| 5 | Workforce SSO (OIDC) and inbound SCIM | own OIDC client in `identity.sso_settings` | — (identity) | OIDC **already accepts `google`** (`hd`, allowed domains); SCIM generic |
| 6 | Connect existing users: announced links, directory picker, exact matches | app-only | `contracts.SSOConnections`; `/entra/directory/*` | matching is `oid`-shaped; the picker is an entra endpoint imported by the host |
| 7 | Personal Outlook connection (Settings › Connections), incremental consent, the "certain account" (M6), CAE | delegated, per person | `ErrConnectionNeeded`, `ConnectionChange` | contract yes; host page imports `@vantigo/entra-ui` |
| 8 | Calendar ↔ Outlook: push of appointments, busy read-back, write-back, change notifications | delegated (M9) | `MailboxCalendar`, `CalendarBusy` | contract yes, **single-provider slot** |
| 9 | Microsoft 365 mail channels: shared mailbox send and receive, marks on close, notifications | app-only, Exchange RBAC for Applications | `ChannelMailbox` | contract yes, **single-provider slot**; Communications hard-codes `entra` |
| 10 | Personal sending ("send from my address", Reply-To the channel) | delegated `Mail.Send` | `PersonalMailbox` | contract yes, single-provider slot |
| 11 | Customer mail through an Owner-chosen channel | via 9 | `OutboundMail` | `Provider` is `smtp` or `entra` |
| 12 | Anonymous webhook `POST /entra/mailbox-notifications` | clientState hash | — | entra's own, which is right |

These patterns carry over unchanged into a `google` module:

- the guarded client (`srv/entra/graph.go`: fixed hosts, netguard dialler, no
  redirects, a budget per call, one retry, capped responses, provider error text
  never logged);
- sealed credentials with a purpose per secret (`srv/secrets`);
- a single-row connection with If-Match;
- the append-only events table;
- the revision-guarded refresh token;
- claim-based workers that return the connection before calling out. This is the
  pool-starvation lesson of 00087: a lease worker that holds a pooled connection
  while it calls out starves the pool.

The latest migration is `00107`, and Inventory reserves `00108`. Renumber at rebase,
as usual.

## 2. Google's equivalents

| Vantigo feature | Google API | Minimum scopes | Auth |
| --- | --- | --- | --- |
| Sign-in restricted to the company | OIDC at `accounts.google.com`; **`hd` checked server-side** (the `hd` request parameter is only a UI hint) | `openid email profile` | per person; no verification needed |
| Stable person key (Entra `oid`) | the ID token's `sub`, which equals the Directory user `id` (**UNVERIFIED**, test it) | — | — |
| Tenant id (Entra `tid`) | Workspace customer id `C0…` from `customers.get(my_customer)`; not in the ID token | `admin.directory.customer.readonly` | DWD |
| Accounts: create, suspend, delete, profile, manager | Admin SDK Directory `users.insert/patch/delete`; `suspended`, `archived`; `relations[type=manager]`, `organizations[]`, `phones[]`, `externalIds[type=organization]` for the employee number | `admin.directory.user` | DWD as an admin, or an admin role on the service account |
| Licences | Enterprise License Manager `licenseAssignments` | `apps.licensing` | DWD |
| First credential (Entra TAP) | **none.** A generated password with `changePasswordAtNextLogin`, revealed once, or nothing when the company signs in to Google through a third-party IdP | — | — |
| Groups and members | Directory `groups`, `members`; group settings through the Groups Settings API | `admin.directory.group` (+ `apps.groups.settings` for settings) | DWD |
| Photo push | Directory `users.photos.update`: **96×96**, Google's own web-safe base64 (`/`→`_`, `+`→`-`, `=`→`*`) | `admin.directory.user` | DWD |
| Directory picker | `users.list?query=name:Ann*` and `groups.list?query=` | `admin.directory.user.readonly` | DWD |
| Directory changes (Entra delta) | **no delta.** `users.watch` (users only, no group push) plus a periodic reconcile | as above | DWD |
| Free/busy | Calendar `freebusy.query`, 50 calendars per call | `calendar.freebusy` | either |
| Event sync | `events.list` with `syncToken` (a 410 means a full resync); markers in `extendedProperties.private`; etag in place of the change key | `calendar.events` (sensitive) | either |
| Change notifications | `events.watch` channels: no body, headers only; never auto-renewed (read `expiration`); "not 100% reliable", so keep polling. Search Console domain verification is **no longer required**, only HTTPS | `calendar.events` | either |
| Shared mailbox | **no such object.** A licensed user mailbox (`post@firma.no`) read through DWD. A Collaborative Inbox group has **no read API** | `gmail.modify` (**restricted**) | DWD impersonating the mailbox |
| Inbound sync | `history.list(startHistoryId, labelId=INBOX)`, which is cheap (2 units); a 404 means a full resync. Push only through **Cloud Pub/Sub** (`users.watch`, re-watch at least weekly, at most 1 notification/s/user) | as above | DWD |
| Threading | `threadId` (the analogue of Graph's `conversationId`) | — | — |
| Sending as the shared mailbox | `users.messages.send` impersonating the mailbox | `gmail.send` (sensitive, not restricted) | DWD |
| Personal sending | `users.messages.send` on the person's token | `gmail.send` | delegated |
| System mail | `smtp-relay.gmail.com` needs an IP allowlist or SMTP AUTH with a password; **less secure apps ended 14 March 2025**. Not worth a driver | — | — |
| Inbound SCIM to Vantigo | Google pushes SCIM only to catalogue apps; custom SCIM is **beta, limited participants**. Do not build on it | — | — |

Quotas worth designing for:

- **Directory:** 2,400 queries/min/user/project.
- **Calendar:** 600/min per user, 10,000/min per project. With DWD the usage is charged
  to the service account, so set `quotaUser` to the impersonated person.
- **Gmail:** 6,000 units/min per user, 1.2 M/min per project; `messages.send` costs 100.
- **Sending limit:** about 2,000 messages a day per Workspace user.

Go libraries: `google.golang.org/api/{admin/directory/v1, calendar/v3, gmail/v1,
licensing/v1, people/v1}` with `option.WithHTTPClient` (our guarded client) and
`option.WithTokenSource`. For DWD with a key, `golang.org/x/oauth2/google.JWTConfigFromJSON`
plus `.Subject`. For DWD without a key, `google.golang.org/api/impersonate.CredentialsTokenSource`
with `Subject`, which goes through IAM Credentials `signJwt`. Our own guarded client
must be passed to the token exchange as well as the API calls, through the context's
`oauth2.HTTPClient`.

## 3. The auth model: the central decision

Entra combines two things: the tenant's **own app registration** (application
permissions, granted by the tenant's admin) and **delegated per-person tokens**
(calendar since M9, `Mail.Send`). Google offers two ways to get the first.

**(a) The tenant's own service account.** The company creates a Cloud project in its
own Google organisation, a service account in it, and an *Internal* OAuth web client.
It uploads the service account's JSON key, which is sealed under
`google/service-account-key`. Its super admin then authorises the service account's
numeric client id for domain-wide delegation (DWD) under *Security › Access and data
control › API controls › Domain-wide delegation*, pasting the scopes Vantigo lists.

- **It matches T1's rule.** The tenant owns what it brings, at the workspace tier,
  exactly like the Entra registration. Self-hosted and hosted installations work the
  same way.
- **No verification and no CASA.** Because the OAuth client is *Internal* to the
  customer's organisation, Google's verification and the annual CASA assessment for
  restricted Gmail scopes do not apply.
- **The weak point is the key.** Google discourages service-account keys. Organisations
  created after May 2024 have the org policy `iam.disableServiceAccountKeyCreation`
  enforced by default (**UNVERIFIED** in a fresh org), so the admin may have to lift
  it for the project. The setup guide must say so, and the wizard must handle a key
  rotation the way Entra's handles secrets.
- **Gmail push needs a topic in the customer's project.** The topic must live in the
  same project as the client that calls `watch` (**UNVERIFIED**). That is one more
  setup step, so the first version polls instead (§6 F).

**(b) One Vantigo-owned service account for every tenant.** Vantigo keeps a dedicated
project and one service account. It mints tokens without a key, through IAM
Credentials `signJwt` with Workload Identity Federation from where Vantigo runs.
Each customer only authorises our client id and scopes for DWD, and Vantigo stores the
customer domain and the admin to impersonate. Later, an *Admin Only Install*
Marketplace listing can replace the paste step: an admin domain install should grant
the listed scopes to the project's service account. Only Google's Classroom guide says
this (**UNVERIFIED**).

- **Lighter for the customer.** There is no project to create and no key.
- **Vantigo becomes the External app.** That means OAuth verification, and for Gmail
  reading probably CASA every year. A DWD token is issued without a consent screen, so
  nothing technically blocks it, but Google's policy text expects verification. That is
  a compliance risk.
- **It is instance tier.** It only fits the hosted service, and a self-hosted
  installation would need (a) anyway.

**Recommendation: (a) first, with (b) designed for later.** Model it as
`google.connection.credential_kind ∈ {service_account_key, vantigo_hosted}` so the
hosted path is a second credential kind, not a rewrite. The token source stays behind
one interface. The per-person connection (§6 D) uses the tenant's Internal web client
in (a) and Vantigo's verified client in (b).

**Least privilege compared with Entra.** Two differences need to be stated plainly in
the admin guide:

- **DWD cannot be scoped to some mailboxes**, unlike Exchange RBAC for Applications
  (M10). A `gmail.modify` grant reaches every mailbox in the domain. The mitigations:
  - ask for Gmail scopes only when the Owner turns *Mail channels* on;
  - list exactly the scopes the switches need;
  - have *Test connection* request a token per scope, so the screen shows granted,
    missing and **unexpected** scopes, the M19 analogue. An unexpected scope is
    detected only by trying it.
- **Every DWD token impersonates one user.** Admin SDK calls run as an admin the Owner
  names (`admin_subject`). Alternatively, a custom read/write admin role can be
  assigned to the service account itself, which avoids depending on a named admin who
  might leave (**UNVERIFIED**; test it, and prefer it if it works).

## 4. What must change outside the new module

These are prerequisites, and the plan makes them their own delivery (§6 delivery 0):

1. **Single-provider slots.**
   - **The problem:** `soleProvider` (`srv/module/workers.go:177-215`) refuses two
     enabled modules filling `MailboxCalendar`, `CalendarBusy`, `ChannelMailbox` or
     `PersonalMailbox`.
   - **Option 1, many providers:** make these slots many-provider and keyed by
     `Key()`, as `IdentityProvisioners` already is. Calendar already compares
     `DirectoryAccount.Provider` with `Key()`. Communications stores a provider per
     channel, so a map lookup is natural.
   - **Option 2, exclusive modules:** declare `entra` and `google` mutually exclusive
     in `MODULES` validation.
   - **Recommendation:** option 1. Companies in migration do run both, and the cost is
     small.
2. **Communications names `entra`.** These need to change:
   - the `providerEntra` constant and the `Key() != providerEntra` checks
     (`srv/communications/contractscalls.go:40,96`, `channels.go`);
   - the partial unique index `… WHERE provider='entra'` from 00091, to become
     `(provider, settings->>'mailbox')`;
   - `graphConversationId` and `FindConversationByGraphConversation`, to become a
     neutral `providerThreadId`, with a data migration of the JSON key;
   - "Microsoft 365" literals, to become `DisplayName()`;
   - `GET /channel-providers`, which lists only smtp and entra;
   - `OutboundChannel.Provider`, which has the same smtp/entra problem;
   - `ChannelForm.tsx`, which hard-codes the `entra` option and should be driven by
     the providers list.
3. **The account kind is the provider key.** `contracts.AccountKindEntra`,
   `ValidAccountKind` (`srv/contracts/accounts.go:42`), the special case at
   `srv/identity/provisioning.go:527`, the Staff OpenAPI enum and the frontend's
   `ENTRA_KIND` all need to change. Generalise them to "a provisioner key" (any
   registered `IdentityProvisioner.Key()`), with identity's rule applied to every
   provider kind.
4. **Identity matching is `oid`-shaped.**
   - **The gap:** expected accounts and links match on the `oid` claim, and Google's
     ID token has none.
   - **The fix:** for the Google issuer, match the object id against `sub`. The
     columns are already varchar(200).
   - **Contracts:** `SSOConnections.DirectoryKey` stays and is documented as
     provider-scoped. `normalizeExternalID` must not uppercase or GUID-parse a numeric
     Google id.
5. **The directory picker and matches are entra endpoints.** `host/routes/workspace/users.tsx`
   imports them from `@vantigo/entra-ui`. Either:
   - a neutral `contracts.DirectorySearch` slot, with the host calling a neutral
     endpoint; or
   - per-provider components chosen by the configured SSO provider.

   The former is less code in the host.
6. **The host is Entra-wired.**
   - Settings › Connections imports `OutlookConnectionCard` and is gated on `entra`.
     It becomes a list of connection cards, one per enabled provider module.
   - Calendar's "Outlook" section and its i18n need neutral copy, with the provider's
     name passed in.
   - In `apps.ts`, `ModuleWithApp = Exclude<ModuleKey,"entra">` must also exclude
     `google`.
   - Add a sibling `/workspace/google/*` with the four tabs.
7. **Calendar's busy vocabulary.**
   - `showAs` (`oof`, `workingElsewhere`) and the item kind `calendar.outlook`
     (`srv/calendar/mailbox_busy.go:30`) need neutral names: `out_of_office`,
     `working_elsewhere`, and an item kind `calendar.mailbox`, migrated.
   - Google maps cleanly: `transparency`, `eventType=outOfOffice|workingLocation`.

Docs-Impact: 1–3 change no behaviour a user sees, apart from error strings that name
the provider. Items 6 and 7 change user-visible labels, so the user guides change in
`en` and `nb`.

## 5. Features and their verdicts

| Entra feature | Google verdict | Notes |
| --- | --- | --- |
| Connection, test, unexpected-permission warning | **Parity** | per-scope token test; the DWD propagation delay ("up to 24 hours") and multi-party approval shown as a state, not an error |
| Account lifecycle | **Parity, different first credential** | no TAP; a once-revealed password with change at next sign-in, or none when the company federates Google itself. A leaver is **suspended** (Google keeps billing a suspended user; archive is a paid licence), and transfer of Drive/Calendar data to the manager through the Data Transfer API is a later option |
| Licences by group trait | **Parity** | Licence Manager SKUs (`Google-Apps`/`1010020027` …) as a trait |
| Groups | **Parity, different traits** | the group email is required, so a trait `email` (default derived like `mailNickname`); visibility through Groups Settings; no "team" trait |
| Photo push | **Parity, low resolution** | 96×96; say so in the guide |
| SSO | **Already there** | add matching on `sub` (§4.4); allowed domains refreshed from `domains.list` once the connection exists |
| SCIM | **Not offered by Google** | the module's reconcile replaces it |
| Directory picker and matches | **Parity** | `users.list` with a prefix query |
| Personal connection | **Parity** | "Connect Google", `calendar.events` and `gmail.send` with incremental consent (`include_granted_scopes`); granular consent means the granted scopes must be read back; the "certain account" check is `sub` against Staff's directory account |
| Calendar push, busy, write-back | **Parity** | `syncToken` in place of the delta link, etag in place of the change key, `extendedProperties.private` marker |
| Change notifications | **Parity** | `events.watch` → `POST /google/calendar-notifications` (anonymous, channel token hashed like `clientState`); renew from `expiration` |
| Mail channels | **Parity on a licensed user mailbox, polling first** | Pub/Sub push later; Collaborative Inbox unsupported and documented as such |
| Personal sending | **Parity** | `gmail.send` on the person's token, Reply-To the channel (Q5) |
| Teams, OneDrive | out of scope, as for Entra | — |

## 6. Plan: the deliveries

The shape follows Entra's: one PR per delivery, each against `main` after the previous
lands, each with its docs in `en` and `nb`, and each through `server:check`,
`server:test`, `frontend:check` and `docs:check`. Every delivery begins from a design
written from this research once the owner has answered §7.

**0 — Neutral seams.** No new behaviour; can land before the Google work starts.

- many-provider mailbox slots (§4.1);
- Communications' provider generalisation and the thread-id migration (§4.2);
- the account kind (§4.3);
- identity's `sub` matching for the Google issuer (§4.4);
- a neutral directory-search contract (§4.5);
- the host's Connections page as a list (§4.6);
- the calendar busy vocabulary (§4.7).

It may be split into two PRs: server, then host. Docs: `reference/communications.md`,
`reference/calendar.md` and `reference/identity.md` where the rule is visible, and
`user/calendar.md` and `user/connections.md` (`en` and `nb`) for the renamed labels.

**A — Module foundation.**

- **The module:** `srv/google` with schema `google`. Tables: `connection` (one row:
  customer domain, customer id, `admin_subject`, `credential_kind`, switches), sealed
  `credentials` (the service-account key, with rotation), and `events`.
- **The client:** a guarded Google client and a fake (`srv/google/googletest`).
- **The endpoints:** `GET/PUT /google/connection`, credentials, and *Test connection*
  (the token per scope, `customers.get`, `domains.list`).
- **The screens:** Workspace admin › Integrations › Google Workspace, four tabs (status,
  connection, accounts, Google).
- **Permissions and wiring:** `google:manage`; `MODULES` requires `staff`.
- **Docs:**
  - `admin/google-workspace.md`, the set-up procedure (`en`, `nb`): project, service
    account, key and org policy, DWD scopes, propagation;
  - `reference/google.md`;
  - the integrations overview pages.

**B — Accounts, groups, photos.**

- **The provisioner:** `IdentityProvisioner` for `google`, covering prepare, activate,
  update, deactivate and delete.
  - Correlation is on `externalIds[type=organization]` = the employee number, as Entra
    correlates on `employeeId`.
  - Primary email follows the same address rules as Entra (`address.go`, shared or
    copied).
  - Licence assignment; the password revealed once.
- **Groups:** group traits (email, visibility, licences).
- **Photos:** the 96 px push.
- **Reconcile:** a reconcile worker in place of delta; `users.watch` is a later
  accelerator, not v1.
- **Docs:** `admin/google-workspace.md` accounts section, `user/staff.md` where the
  provider is named (`en`, `nb`), `reference/google.md`.

**C — Connect existing users.**

- The directory picker and exact matches through the neutral contract.
- Announced links with the `sub`/id.
- The SSO card offering "Google" with the connection's domains prefilled.
- Docs: `admin/sso-scim.md` and `admin/authentication.md` (`en`, `nb`).

**D — The personal Google connection.**

- `google.connections` (sealed refresh token, revision, state, scopes).
- `POST /google/connect`, the callback and completion; the "certain account" check;
  the sweep worker; the Connections card.
- The OAuth client is the tenant's Internal web client (credential kind (a)).
- Docs: `user/connections.md` and `admin/google-workspace.md` (`en`, `nb`).

**E — Calendar.**

- `MailboxCalendar` and `CalendarBusy` for `google`: push, busy read-back, write-back
  with the mass-delete guard.
- `events.watch` notifications on an anonymous endpoint allowlisted in
  `srv/security/publichost.go`.
- Time zones are IANA natively, so no `windowszones.go`.
- Docs: `user/calendar.md`, `admin/calendar.md` and `admin/google-workspace.md`
  (`en`, `nb`), `reference/calendar.md`.

**F — Mail channels.**

- `ChannelMailbox` for `google` on a licensed user mailbox through DWD.
- Send with `messages.send`; receive by polling `history.list` per channel from the
  existing `communications-mailbox-sync` claim worker.
- Marks on close: remove the `UNREAD` label, or apply a "Vantigo/Closed" label as the
  move.
- Threading by `threadId`; the mailbox refused if it is a group address.
- Docs: `user/communications.md` and `admin/google-workspace.md` (`en`, `nb`),
  `reference/communications.md`.

**G — Personal sending.** `PersonalMailbox` for `google` with `gmail.send`, applying
Q5–Q7 unchanged. Docs as F.

**Later, not planned:**

- Gmail push through Pub/Sub, needing a topic in the customer's project plus an
  authenticated push to `/google/gmail-notifications`;
- `users.watch` for faster directory changes;
- credential kind (b) with key-less `signJwt` and an *Admin Only Install*
  Marketplace listing for the hosted service;
- the Data Transfer API for leavers;
- the Reports API for group changes.

Before A, there is a **test domain**. A Google Workspace Business Starter trial costs
nothing for 14 days, and a paid seat is needed for longer. It needs at least: a super
admin, a second user, a licensed `post@` mailbox, and a Cloud project under its
organisation. The **UNVERIFIED** items (§2, §3) are each settled there first:

- `sub` equals the Directory id;
- the default key-creation policy;
- an admin role on a service account;
- `hd` for users on a secondary domain;
- the `events.watch` channel expiry.

## 7. Questions for the owner

1. **Credential model.** (a) the tenant's own service account and Internal client first,
   (b) a Vantigo-owned account and Marketplace for the hosted service later. Is that
   the order? (b) brings Google verification and probably a yearly CASA assessment for
   the Gmail scopes.
2. **Entra and Google together.** Should an installation be able to run both modules
   (many-provider slots, §4.1), or should they be exclusive?
3. **Calendar access.** Parity with M9 (per-person delegated connection, consent
   visible to the person)? The alternative is DWD with `calendar.events`, where no one
   clicks *Connect* but the module can reach every calendar in the domain.
4. **The first credential.** With no TAP, should activation set a generated password
   revealed once (as the pass is today), or leave sign-in to the company's own Google
   SSO and set nothing?
5. **Leavers.** Suspend only (Google keeps billing suspended users), or offer delete
   after N days, and/or archive (a paid *Archived User* licence)?
6. **The mail channel's mailbox.** Is a licensed user mailbox, an extra seat for the
   customer, acceptable as the "shared mailbox", with Collaborative Inbox groups
   unsupported?
7. **Gmail push.** Polling in v1, with Pub/Sub only when a customer asks?

## 8. The owner's answers (2026-11-03)

- **Q1 — Credentials: the tenant's own first.** (a) a service account with a key and an
  Internal client in the customer's own project; (b) Vantigo-owned and Marketplace for
  the hosted service later, behind `credential_kind`.
- **Q2 — Both modules may run together.** The mailbox slots become many-provider (§4.1).
- **Q3 — Calendar on a per-person connection,** as M9. No DWD calendar scope.
- **Q4 — A generated password revealed once,** changed at first sign-in, delivered as
  the pass is today.
- **Q5 — Suspend on the leave day; the Owner may set a delete after N days.** Archive is
  not offered.
- **Q6 — A licensed user mailbox is the channel's mailbox.** Group addresses are
  refused, and the guide says why.
- **Q7 — Polling in v1.** Pub/Sub when a customer asks.

The owner has a Google Workspace tenant for testing; the **UNVERIFIED** items in §6 are
settled there before delivery A.
