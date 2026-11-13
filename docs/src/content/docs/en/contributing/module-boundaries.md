---
title: "Module boundaries"
description: "Implementation ownership and the conventions every module follows."
sources:
  - apps/server/internal/module
  - apps/server/internal/contracts
  - apps/server/internal/agentloop
  - apps/server/internal/modtest
  - apps/server/.golangci.yml
  - apps/server/generate.go
  - apps/server/internal/openapi/openapi.go
  - apps/server/internal/openapi/lint.go
  - apps/server/internal/openapi/gen
  - apps/calendar/frontend/eslint.config.js
  - apps/communications/frontend/eslint.config.js
  - apps/customers/frontend/eslint.config.js
  - apps/energy/frontend/eslint.config.js
  - apps/entra/frontend/eslint.config.js
  - apps/google/frontend/eslint.config.js
  - apps/meta/frontend/eslint.config.js
  - apps/expenses/frontend/eslint.config.js
  - apps/invoices/frontend/eslint.config.js
  - apps/planning/frontend/eslint.config.js
  - apps/inventory/frontend/eslint.config.js
  - apps/orders/frontend/eslint.config.js
  - apps/knowledge/frontend/eslint.config.js
  - apps/assistant/frontend/eslint.config.js
  - apps/products/frontend/eslint.config.js
  - apps/projects/frontend/eslint.config.js
  - apps/staff/frontend/eslint.config.js
  - apps/time/frontend/eslint.config.js
---
Vantigo is a modular monolith: one Go binary, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted into
its own deployable without a rewrite. Customers, Products, Energy, Communications,
Projects, Time, Expenses, Invoices, Staff, Calendar, Entra, Google, Meta, Planning, Inventory, Orders and Assistant are vertical-slice modules composed only
by `cmd/vantigo`, on the Identity platform.

Modules never reference each other directly. Synchronous cross-module needs go
through the in-process contracts in `internal/contracts`; asynchronous ones — what may
lag, never what must refuse — go through the platform's domain-events outbox,
`internal/events`, a transactional outbox whose handlers are a many-provider slot
([rule 23](#the-rules),
[The domain-events outbox](/en/contributing/platform-packages/#the-domain-events-outbox)).

## The rules

1. **No module→module dependencies.** A package under `internal/<module>/…` may
   import platform packages (`internal/config`, `internal/db`, `internal/httpx`,
   `internal/module`, `internal/contracts`, `internal/peppol` — the Peppol
   SML/SMP lookup, shared by Customers and Invoices — `internal/holidays` — the
   Norwegian public-holiday calendar, by which Invoices moves its due dates, Staff
   counts working days and Calendar marks the holidays beside a person's items — `internal/netguard`
   — the shared table of addresses the guarded outbound clients refuse to
   dial (today `internal/mail`'s SMTP guard, `internal/peppol`'s SMP
   client, the calendar's fetch of the calendars a person subscribes to and the
   energy module's spot-price feed; Brreg and OIDC use `http.DefaultTransport`, and
   the AI client dials through `Deps.HTTPTransport`, `http.DefaultTransport` in production), and its one exception, `DialContextAllowingPrivate`,
   which also admits RFC 1918 and unique-local addresses and has exactly one
   caller, the energy module's client for the shared spot-price service
   ([The service client and its guard](/en/reference/energy/#the-service-client-and-its-guard)) —
   `internal/ai` — the AI client, the one place the provider's key lives, called with a task
   key and never with a model ([The AI platform](#the-ai-platform)) —
   `internal/payments` — the payments port, shared by Invoices and a later Point of
   sale, with its Vipps MobilePay adapter `internal/payments/vipps`; interfaces,
   values and the adapter only, no SQL, so each consumer keeps its own tables
   (rule 4) — `internal/shipping` — the carrier port, Orders' and a later webshop's,
   with its Bring adapter `internal/shipping/bring`; interfaces, values and the adapter
   only, no SQL ([Shipping carriers](/en/contributing/shipping-carriers/)) —
   `internal/fileimport` — the reader of an uploaded import file
   (encoding, separator, quoting, the formula guard, header keys), shared by the
   Customers and Energy imports; no SQL, each module keeps its own imports, worker
   and caps — `internal/events` — the domain-events outbox, `events.Publish` and the
   handler type of rule 23 — `internal/docmath` — a sales document's arithmetic,
   shared by Invoices and Orders
   ([The document arithmetic](/en/contributing/platform-packages/#the-document-arithmetic)) —
   …) and its own subpackages, never another module's.
   `internal/payments/vipps/vippstest` and `internal/shipping/bring/bringtest`, the
   adapters' fakes, are imported by tests only.

   `internal/mail` is such a platform import, and a module that sends mail goes through
   its guarded path (`mail.SendOutbound`, or the test seam `Deps.SMTPSend`).
   `mail.Outbound` and `mail.SendOutbound` were made for Communications, which sends
   with each channel's own stored credentials, and which Invoices and Calendar hand to
   `OutboundMail` ([rule 17](#the-rules)) to send from a channel an Owner chose; the
   installation's own `SMTP_*` configuration is the system mailbox's, for the tenant's
   employees (identity's and Staff's mail), and no customer's mail takes it. Invoices'
   one change to the package is `ReplyTo`: `mail.Outbound` carries a `ReplyTo`, written
   as the `Reply-To` header when set and absent when empty, so a document sent from a
   channel can have its replies reach the seller's own mailbox.
2. **The platform imports no module.** `internal/module` and `internal/contracts` —
   the platform modules mount through — may not import any business module, so the
   composition machinery never depends on what it composes.
3. **Contracts stay pure.** `internal/contracts` holds cross-module interfaces,
   permissions and access rules as DTOs only, never store types, so any module or a
   future extracted service can implement or consume them.
   The one non-DTO type is `pgx.Tx`, in `contracts.CustomerReferenceHolder`,
   `contracts.CustomerPersonalData` and `contracts.InvoicedWorkHolder` — a platform
   type, never a store type, and the reason is rules 8, 9 and 10's.
4. **One schema per module.** Each module maps tables only into its own PostgreSQL
   schema (`identity`, `customers`, `products`, `energy`, `communications`,
   `projects`, `time`, `expenses`, `invoices`, `staff`, `calendar`, `entra`, `google`, `meta`, `planning`, `inventory`, `orders`, `manufacturing`, `knowledge`, `assistant`), and no module's migrations or queries reference
   another's. **No cross-schema foreign keys or joins** — reference other modules'
   data by opaque ID only. That is what keeps a future "move this schema to its own
   server" a connection-string change instead of a data migration.
5. **One module, one mount.** A module exposes one `Module()` returning a
   `module.Module`: its name, its `Mount`, the permissions it contributes, the
   background workers it contributes, and the cross-module contracts it *provides*.
   There are twenty-two single-provider slots, each filled by at most one enabled module:
   `Directory` (`contracts.CustomerDirectory`, customers), `Users`
   (`contracts.UserDirectory`, identity), `Calendar` (`contracts.BusinessCalendar`,
   identity — the workspace's business time zone and the day an instant falls on in
   it, which every module judges "today" by), `Accounts`
   (`contracts.AccountProvisioning`, identity — rule 11), `SSOConnections`
   (`contracts.SSOConnections`, identity — the users not yet connected to the
   workforce provider and who holds a directory account's keys, for Entra's exact
   matches; provider-gated, rule 12), `Products` (`contracts.ProductCatalog`,
   products; a `VariantEntry` carries its product's tax category, description, category id and
   name, and its option values, so Orders prices, groups and builds the variant grid without a
   second read; `ProductImages(productID, use)` and `OpenProductImage(imageID)` read a
   product's images — a `ProductImage` names a picture and never where it is kept — and,
   like the rest of the catalog, check no user permission: the caller's own picker does),
   `Projects` (`contracts.ProjectDirectory`, projects, incl. the
   consumer-gated `ProjectsByStatus`), `Actuals`
   (`contracts.ProjectActuals`, time), `Timesheets` (`contracts.Timesheets`, time — the
   hours a user logged in a week, consumer-gated), `Expenses`
   (`contracts.ProjectExpenses`, expenses), `Receivables`
   (`contracts.CustomerReceivables`, invoices — what a customer was invoiced and
   owes, every state judged on the provider's own business day), `SalesTax`
   (`contracts.SalesTax`, invoices — its VAT codes with the rate valid on a day, the VAT
   code each of Products' tax categories maps to, and the seller block it prints; nil
   when Invoices is off, and read as always present by Orders, which requires it), and the three line-level reads an
   invoice is built from — `BillableHours` (`contracts.BillableHours`, time),
   `BillableExpenses` (`contracts.BillableExpenses`, expenses) and
   `BillableMilestones` (`contracts.BillableMilestones`, projects) — `Consumption`
   (`contracts.ConsumptionProvider`, energy — the settlements a customer owes for its
   metering points, what a consumption invoice is built from; optional for its
   consumer, nil when Energy is off; its `Settle` is a provider command, below),
   `BillableOrders` (`contracts.BillableOrders`, orders — the order lines and the
   subscription periods due to be invoiced, a subscription a `DueOrder` of kind
   `subscription` keyed with its kind beside its id, and the allocations an invoice line
   is built from; optional for its
   consumer, nil when Orders is off; its `Allocate` and `ReleaseAllocations` are
   provider commands, below), `SalesOrderLines` (`contracts.SalesOrderLines`, orders —
   its sales orders' goods lines with their open quantity, which Manufacturing makes
   orders from; consumer-gated, rule 12; optional, nil when Orders is off) and
   `Production` (`contracts.Production`, manufacturing — the manufacturing orders made
   for sales order lines, which variants have a bill of materials, and why make-to-order
   made no order for a sales order's lines, which Orders' order page shows;
   provider-gated, rule 12; optional, nil when Manufacturing is off) and
   `DeliveryTrace` (`contracts.DeliveryTrace`, orders — its posted deliveries with their
   order and customer id, which Inventory's trace shows as who received a lot;
   provider-gated, rule 12, refusing with `ErrForbidden`; optional, nil when Orders is
   off) — all three resolved from the enabled modules by `withProduction`, after the
   stock slots, by Compose and `module.Workers` alike, no provider reading another while
   it is built; `modtest.WithSalesOrderLines`, `modtest.WithProduction` and
   `modtest.WithDeliveryTrace` preset them —, and `Workforce`
   (`contracts.Workforce`, staff — the competence catalogue, who holds which competence
   valid on a date, the people in employment and their capacity over a window, the
   caller's own person, its own account in an external directory, and how a proposed
   booking fits what each person wants; rule 12),
   `OutboundMail` (`contracts.OutboundMail`, communications — its channels as Invoices and
   Calendar send customer mail through them; rule 17; optional for its consumers, nil when
   Communications is off, which they read as "sending is disabled"),
   `CalendarBusy` (`contracts.CalendarBusy`, calendar — people's busy
   time, which Planning's board shows; provider-gated, rule 12; optional for its
   consumer, nil when the calendar is off),
   `ContactChannels` (`contracts.ContactChannels`, communications — a contact's Messenger
   channels, which Customers' contact rows show: one entry per active channel, linked or not,
   with the participant's id in Communications, the newest message and when the reply window
   closes; consumer-gated — Customers asks for `communications:conversations-view` before it
   calls; it never carries the person's page-scoped id, which belongs to a Page; optional for
   its consumer, nil when Communications is off, which it reads as "no channels"; resolved by
   Compose after the channel messengers its provider reads, never by `module.Workers`;
   `modtest.WithContactChannels` presets one), `AgentMemory` (`contracts.AgentMemory`, agents — the
   notes the virtual employees keep about a customer, which Customers' page shows in a collapsed section:
   `AboutCustomer` answers one entry per agent with its notes newest first; consumer-gated — Customers asks
   for `agents:view` before it calls; optional for its consumer, nil when Agents is off, which it reads as
   "nothing is remembered"; resolved by Compose after `ContactChannels`, never by `module.Workers`;
   `modtest.WithAgentMemory` presets one), `AgentColleagues` (`contracts.AgentColleagues`, agents — who the
   Assistant's panel may let a person talk to and what each may do itself: `Agents`, `Agent`, `Names`, `Operations`;
   consumer-gated — the Assistant asks for `agents:view` before it calls; optional for its consumer, nil when Agents is
   off, which it reads as "no agent to talk to"; resolved by `Compose` once the operations dispatcher exists, with the
   dispatcher on the provider's Deps, never by `module.Workers`; `modtest.WithAgentColleagues` presets one), and `AIModels` (`contracts.AIModels`,
   identity — the model an Owner chose for each AI task, which the platform AI client asks
   and no module calls; [The AI platform](#the-ai-platform)), and `AssistantGrants`
   (`contracts.AssistantGrants`, identity — issues and ends the grants the assistant acts
   under; rule 18; resolved by `Compose` after the AI slot, not by `Workers`),
   `AgentAccounts` (`contracts.AgentAccounts`, identity — the one way a user of kind `agent`
   is made, renamed, given a picture, disabled and enabled: `Create`, `Rename`, `SetAvatar`,
   `Disable`, `Enable`; the `agents` module is its only caller, an agent's user is never
   deleted, and identity refuses such a user every credential, session, link and the
   Owner and SystemAdmin roles) and `AgentGrants` (`contracts.AgentGrants`, identity —
   issues and ends the grants an agent acts under: `Issue`, `Revoke`, `RevokeForUser`;
   unlike `AssistantGrants.Issue` it needs no session, and refuses a user that is not an
   active agent; its grants live ten minutes; the `agents` module is its only caller);
   `Compose` resolves both beside `AssistantGrants`, and `module.Workers` resolves neither.
   **A provider's read may carry commands.** A single-provider read contract may hold
   methods that write — **provider commands** — when each writes only the provider's
   own schema, on the provider's own transaction, and is named a command for it; the
   caller makes it on the provider's pool before it opens a transaction of its own, as
   any contract call (rule 21), and nothing of the caller's is written by it. Energy's
   `ConsumptionProvider.Settle` is the first; Orders' `BillableOrders.Allocate` (a
   new draft allocation per order line, and per due period and line of a subscription,
   under the orders', subscriptions' and lines' locks on Orders' own transaction) and `ReleaseAllocations` (the named drafts released under their
   orders', lines' and own locks, in that order, a repeat harmless) are the second and
   third. The caller owns the cleanup of what a command of this kind made for it: Invoices
   records every allocation `Allocate` answers in a ledger of its own at once, and its
   `invoices.allocation_reconcile` worker releases what no draft holds — the provider
   expires nothing by age. A write into another module's schema
   is never a provider command: it is a sanctioned direction of its own, with its own rule here.
   `module.Compose`
   mounts each module at `/api/v1/<name>/` and builds every provider before any
   `Mount` runs, so a module's `Deps` already carries what it consumes; `Calendar`,
   then `Accounts` and then `SSOConnections` are resolved right after `Users`, so the
   accounts provider may read the user directory while it is built (`module.Workers`
   leaves `SSOConnections` nil: no worker connects anyone); `Products` comes next, before the
   stock slots, whose ledger reserves through it — by Compose and `module.Workers` alike
   (`withProducts`), since Manufacturing's make-to-order worker locks the variants of the
   orders it makes through it; `Actuals`, `Timesheets` (right after
   `Actuals`, the same module owning both),
   `Expenses`, `Receivables`, `SalesTax` (right after `Receivables`, the same module
   owning both, from its own tables only) and the three `Billable*` are resolved last, after
   `Projects` and `Directory`, so those providers' *constructors* are allowed to read
   the project directory or the customer directory (none does, but a future
   provider could), and `Consumption` right after the three `Billable*`, then
   `BillableOrders` (their providers never read a directory while they serve). `Workforce` is resolved after the business calendar — by Compose
   after the line-level reads, by `module.Workers` after `Users` and `Accounts`
   (`withWorkforce`) — so its provider reads `Deps.Calendar` while it is built, and a
   worker-mode `Deps` answers who holds what as a composed one does. `OutboundMail` is
   resolved after the three mailbox slots below, since its provider may read
   `Deps.ChannelMailboxes` while it is built. `CalendarBusy` is resolved by
   Compose last of all, after the mailbox slots, since its provider — the calendar's
   server — reads the rest of `Deps` while it is built; `module.Workers` leaves it nil,
   as no worker reads it; nor does it resolve `Receivables`, which no worker reads
   either. It does resolve `SalesTax` (`withSalesTax`, after the line-level reads): Orders'
   mail worker prints the seller block into every mail it sends. Two providers are a
   compose error naming both, and a panic in Workers.
   Twelve slots are *many-provider* instead — any number of modules may fill them,
   and Compose collects them in module order: `Workers` (background work, from
   the enabled modules), `CalendarSources` (`contracts.CalendarSource`, rule 12,
   from the enabled modules too), `IdentityProvisioners`
   (`contracts.IdentityProvisioner`, rule 13, from the enabled modules too), the three
   mailbox slots — `MailboxCalendars` (`contracts.MailboxCalendar`, entra — a provider's
   mailbox calendars, which the calendar's mailbox sync writes people's copies to and
   reads their busy time from; rule 14), `ChannelMailboxes` (`contracts.ChannelMailbox`,
   entra — a provider's mailboxes as Communications' channels send from; rule 15) and
   `PersonalMailboxes` (`contracts.PersonalMailbox`, entra — a person's delegated
   mailbox to send from in Communications; rule 16) —, `ChannelMessengers`
   (`contracts.ChannelMessenger` — a messaging provider as Communications' Messenger
   channels send and receive through; rule 15, the messenger slot; no provider fills
   it yet but the tests' fake) with the same keyed collection, whose key no mailbox
   provider may also claim, `DirectorySearches`
   (`contracts.DirectorySearch`, entra — a directory's accounts as identity offers them
   to an Owner connecting existing users; rule 22; Compose only, as no worker searches),
   all five keyed and from the enabled modules too,
   `CustomerReferences`
   (`contracts.CustomerReferenceHolder`, rule 8), `CustomerPersonalData`
   (`contracts.CustomerPersonalData`, rule 9; a holder of contact ids may also implement the optional
   `contracts.ContactDataEraser`, which Customers calls in the same transaction with the contacts the
   anonymisation deleted), `InvoicedWork`
   (`contracts.InvoicedWorkHolder`, rule 10), `VariantReferences`
   (`contracts.VariantReferenceHolder`, rule 20) and `EventHandlers` (`events.Handler`,
   the domain-events outbox's handlers, rule 23), the last five from every module
   Compose is given, enabled or not — see [Turning a module off](#turning-a-module-off).
   `EventHandlers` is collected by `module.EventHandlers`, in module order, each
   module's own in the order it returns them; a handler with no name, no events or no
   `Handle`, and a name two handlers share, is refused, naming both modules. `Compose`
   calls it only to refuse such a binary at startup — the API runs no handler — and the
   process that runs workers calls it to build the dispatcher, `events.dispatch`, since
   worker mode never composes.
   `CalendarSources` is collected from enabled modules only, by Compose and by
   Workers alike, because a source is a read under the module's own permission: a
   disabled module's permissions are not in the catalog, so its gate could only fail
   closed, and its tables may be empty — whereas a customer holder must run whether
   or not its module is on, because a switched-off module still holds rows naming the
   customer. The slot is not called `Calendar`, which is `Deps.Calendar`, the business
   calendar identity provides. `IdentityProvisioners` is collected the same way, from
   enabled modules only, by Compose and by Workers (`withIdentityProvisioners`, after
   `withCalendarSources`, so after `Users` and `Accounts` and a provider may read the
   directory while it is built), because a disabled provider module has no connection
   to drive and Staff's accounts worker drives the providers in worker mode as the API
   does. Each provider is keyed by `Key()`, answered with no context and no network;
   **a key two providers claim fails composition**, naming both — `Compose` answers the
   error, `Workers` panics, as for `InvoicedWork` — and so does a **reserved key**:
   `""`, `identity` (the provider Staff's own identity link names in
   `staff.identity_links`), `local` and `external` (the two account kinds that name no
   provider), naming the module; a nil provider is dropped.
   Providers preset on `Deps` (a test's fake, through
   `modtest.WithIdentityProvisioners`, which `modtest.FakeIdentityProvisioner` fills)
   come first and keep their place. The three mailbox slots and `DirectorySearches` are
   collected by the same rule — one generic helper, `collectKeyed`, behind
   `withMailboxCalendars`, `withChannelMailboxes`, `withChannelMessengers`,
   `withPersonalMailboxes` (Compose and
   Workers, after the identity provisioners) and the directory searches (Compose) —:
   presets first (`modtest.WithMailboxCalendar`, `WithChannelMailbox`,
   `WithChannelMessenger`, `WithPersonalMailbox`, `WithDirectorySearch` append), a duplicate
   or reserved key refused — and, for the messengers, a key also filled in
   `ChannelMailboxes`, since a channel's provider names one kind of provider. A consumer never iterates them to call out except where its rule says: it
   picks the provider by a key it already holds — the calendar by the person's
   `DirectoryAccount.Provider`, Communications by the channel's `provider` and by the
   `send_provider` it recorded, identity by the configured OIDC provider — through one
   lookup per slot in its own `contractscalls.go` that makes no network call. A provider's gate is asked for a user with no request
   behind them only through `contracts.AsUser`, and only by a module that has matched a
   token to that user — the calendar feed's, whose address names one person — or that
   acts on a person's own standing setting — the calendar's Outlook sync worker, whose
   row is the person's: `HasPermission` then asks `Access.UserHolds` for that user
   instead of `Check` for a session (rule 12).
   Beside the slots a module fills for another, `Module.LiveTopics func(Deps) []live.Topic`
   declares the live topics the module serves ([Live updates](/en/contributing/platform-packages/#live-updates)).
   It is read by `Compose`, not by a consumer: `Compose` collects the enabled modules' topics,
   refuses a duplicate kind, a kind whose prefix is not the module's name and a topic
   without `Authorize`, and hands the result to the hub as `*live.Registry` in `Deps.Live`.
   A topic may also set `Topic.Doc`, a `live.DocStore` the module implements over its own table,
   which makes it a document topic that carries binary frames ([Binary frames and documents](/en/contributing/platform-packages/#binary-frames-and-documents));
   `srv/live` then knows no table, and the module's store notifies through `live.NotifyDoc` on the
   transaction that stores a row.
   A module needs neither the hub nor the registry to publish: `live.Notify` is a free
   function that takes the transaction, and a worker-only process, which builds no hub, calls
   it the same way. The `Authorize` of a topic runs under `contracts.AsUser`, so
   `HasPermission` answers through `Access.UserHolds`. The hub re-checks a socket's session
   every 60 seconds through `contracts.Access.SessionLive(ctx, sessionID)`, a read that does
   not slide the session's idle window and that only the hub calls; `Access.Check` is still
   the call that signs a request in.
   No two invoiced-work holders may claim one source kind. `module.Workers`
   collects `CustomerPersonalData` and `InvoicedWork` as well, because worker mode
   never composes: the anonymisation worker is where rule 9's erase runs, and the
   EHF workers build an Invoices server from worker-mode `Deps`, which must never
   read an empty slot as "nothing to stamp". It resolves `Directory` too, as
   Compose does — from the one enabled module that declares it, the caller's own
   left in place when none is enabled — because energy's import worker resolves a
   file's organisation numbers through the customer directory; and then `Users` and
   `Accounts`, from the enabled modules, in Compose's order, keeping a value preset
   on `Deps` when no enabled module provides one: a worker reads identity's users and
   the accounts worker writes them. Workers has no error to return, so two providers
   of `Directory`, `Users` or `Accounts` — a bug in the binary's module list, which
   Compose refuses — panic. After `Workforce` it resolves `Projects`, then the three
   `Billable*` reads and `Consumption`, through the very helpers Compose calls
   (`withProjects`, then `withBillables`), in that order as Compose does (Compose
   resolves them before `Workforce`), keeping a preset
   value when no enabled module provides one: Invoices' `invoices-billing` worker
   drafts and issues through the From consumption wizard's and the issue's own code
   paths, which read them ([Billing runs](/en/reference/invoices/#billing-runs)). Two
   providers of any of them panic, as above.
   One order is not the module order: both Compose and Workers put the
   `CustomerReferences` and `CustomerPersonalData` of the modules that declare
   `InvoicedWork` **after every other module's**, each side in module order (a
   stable partition). A merge or an anonymisation then locks the invoices
   documents before any row of a module invoices are built from — the order an
   invoice's issue takes them in (rule 10) — so the two can never wait on each
   other.
6. **Never reach around the boundary.** Do not call another module's HTTP endpoints
   from inside the process (the one sanctioned exception is the Assistant's dispatcher, rule
   18), and do not reach into another module's schema.

   **The public host is the platform's.** When `PUBLIC_BASE_URL` names a host of its
   own, `security.PublicHostGate` (`internal/security/publichost.go`) answers 404 on it
   to every method and path not on `security.PayRoutes()`, before any module's router
   sees the request. A module's anonymous operation is reachable on the public host only
   when that platform list names it — today the Invoices pay page's three operations and
   the Calendar answer page's two (`POST /api/v1/calendar/rsvp/read` and
   `POST /api/v1/calendar/rsvp`, beside the page's own `GET /rsvp`) and the calendar
   feed's `GET /api/v1/calendar/feed/{token}.ics`, the one anonymous `GET` with a token in
   the path, argued in the calendar feed design D2 — and nothing reserved
   ahead of what is served — so a module adds a public operation by changing the platform's
   list, in the same change and with its test (`TestPublicHostGate`), never by mounting
   anything of its own there.
7. **Frontend packages are isolated too.** A module frontend package (for instance
   `@vantigo/products-ui`) never imports from another module's package or from the
   host app; only the host composes them. Shared UI lives in
   `@vantigo/frontend-shell` and the generated API types and client in
   `@vantigo/frontend-api-client`. A module package may open a *slot* — a prop the host
   fills with another package's component, as `@vantigo/staff-ui`'s `PersonPage` does
   with `extraTabs`, through which the host adds Planning's tab to the person page —
   and the host composes it; the package that opens the slot never names what fills it.
   The Assistant's tour targets are the same kind of composition: each package exports
   `assistTargets`, the host joins them and hands them to the assistant package, and
   `tools/assist/gen-targets.ts` (run by `go generate`) merges the same files into the
   registry the assistant module embeds ([frontend architecture](/en/contributing/frontend-architecture/#tour-targets-for-the-assistant)).
   `@vantigo/calendar-ui`'s `SettingsPage` opens `connectionButton` (the slot
   `connection.button`, called with `{feature: "calendar", returnTo:
   "/calendar/settings"}`) in its Outlook card, which the host fills with
   `@vantigo/entra-ui`'s `OutlookConnectButton` while the Entra module is on, and draws
   nothing without. A page may also be the host's own, composed from a module's
   package: **Settings → Connections** (`routes/settings/connections.tsx`) renders
   `@vantigo/entra-ui`'s `OutlookConnectionCard`, its sidebar entry and its route shown
   only while `entra` is enabled (the route answers not found otherwise). The same goes
   for the organisation's side of that line: the workspace area's second sidebar
   section, **Integrations** (`apps.ts`, `areas`), is a slot a module may take an item
   in. Entra's item, **Microsoft Entra ID** at `/workspace/entra` (`routes/workspace/entra.tsx`),
   carries `module: "entra"` and `requiredPermissions: ["entra:manage"]`, and Google's,
   **Google Workspace** at `/workspace/google` (`routes/workspace/google.tsx`),
   `module: "google"` and `requiredPermissions: ["google:manage"]`, and Meta's, **Meta** at
   `/workspace/meta` (`routes/workspace/meta.tsx`), `module: "meta"` and an Owner's
   alone (the module has no permission of its own); the area's
   layout (`routes/workspace.tsx`) admits a holder of that permission while the module is
   on, the item's route asks the module and the permission again, and the section hides
   itself when none of its items shows. A module that takes such an item owns no app: it
   has no tile and no `AppKey`, and the places that resolve an app by module key treat it
   as not listed. The package exports the page's tabs, and the host mounts them as child
   routes of its layout.
8. **One sanctioned cross-module write.** A module never writes another's data —
   with one exception, made for merging customers:
   `contracts.CustomerReferenceHolder`. A module that stores customer ids in its
   own schema declares `Module.CustomerReferences`, and the customers module's
   merge calls every holder **inside its own transaction**, which already holds
   both customer rows locked. The holder runs its own SQL, on its own schema,
   from its own package — so rules 1, 4 and 6 hold as they do for a read — never
   begins or ends a transaction, never reads a directory (no in-process lookup
   happens under a lock anywhere in this codebase but the variant's, rules 20 and
   21), and tolerates a reference
   that already points at the surviving customer. Today's holders are projects,
   energy, communications and invoices ([Merging duplicates](/en/reference/customers/#merging-duplicates)).
   Another write direction needs a design of its own, not a second holder-shaped
   interface — rule 9 is that design for the second, rule 10 for the third,
   rule 11 for the fourth, rule 13 for the fifth, rule 14 for the sixth and rule 15 for
   the seventh.
9. **A person's data, handed over and taken out.** The second sanctioned
   cross-module direction, made for the GDPR of private-person customers:
   `contracts.CustomerPersonalData`. A module that holds anything about a
   customer *as a person* declares `Module.CustomerPersonalData`. Its
   `ExportCustomerData` answers the module's section of that person's export,
   outside any transaction of the customers module's; its `EraseCustomerData`
   runs **inside** the customers module's anonymisation transaction, which holds
   the customer row locked, and removes or blanks what the module holds on its
   own schema, in its own package — the holder's rules exactly: it never begins
   or ends a transaction, never reads a directory, reports what it did kind by
   kind, and runs whether or not its module is enabled. It is not a method on the
   merge holder because it is not the same promise: a merge moves references and
   keeps everything, an anonymisation keeps the references and takes the person
   out of them. Today's implementations are communications (the correspondence,
   handed over and deleted), energy and projects (handed over, and kept:
   a supply period is the metering point's history, invoiced work stays) and
   invoices (handed over; drafts deleted, issued documents and their payments kept
   as bookkeeping material with the payments' notes blanked, the deliveries kept
   with the recipient blanked)
   ([Personal data and anonymisation](/en/reference/customers/#personal-data-and-anonymisation)).
10. **Work marked invoiced, inside the issue.** The third sanctioned cross-module
    direction, made for invoicing work: `contracts.InvoicedWorkHolder`. A module whose
    rows an invoice is built from — Time's entries, Expenses' lines, Projects' billing
    milestones, Energy's settlements, Orders' invoice allocations — declares `Module.InvoicedWork`, and Invoices calls the holders
    **inside its issue's transaction**, which already holds the document, the settings
    row, the number counter and, for a credit note, its original locked: after every
    check and the number, before the document is written. `MarkInvoiced` judges each
    source as it stands under the holder's own lock — already invoiced first
    (`source_already_invoiced`), then still approved, ready or billable
    (`source_not_invoiceable`), then unchanged since the draft took it — the revision
    and amount for Time and Projects, the billing facts for Expenses, the settlement
    recomputed and then its revision, currency and amount for Energy, the revision,
    currency and amount for Orders (`source_changed`), amounts compared exactly, by value — and stamps it with the
    invoice's id and number at the issue's time; `ReleaseInvoiced` takes the stamp back
    in the issue of the credit note that returns the source's line in full. A source a holder will
    not stamp is answered as a `*contracts.WorkSourceRefusal`, and the issue refuses
    with its code and the line's position — the number, and every holder's write, roll
    back with it; any other error is a failure. A holder keeps rule 8's rules: it runs
    its own SQL on its own schema from its own package, on the caller's `pgx.Tx`; it
    never begins or ends a transaction and never reads a directory, any other contract
    or a clock — every timestamp it writes is the issue's own (`InvoiceRef.IssuedAt`),
    and the issuer's name arrives in `InvoiceRef.IssuedByDisplay`; it may publish a
    domain event on the issue's transaction with `events.Publish`, its `occurredAt`
    `InvoiceRef.IssuedAt` — a row of the platform's outbox, not of another module, that
    commits or rolls back with the issue (rule 23); the period lock
    applies to neither direction, a stamp not being an edit of the work; its release
    takes the same locks as its mark and tolerates a source that no longer carries the
    stamp, writing nothing for it and logging a warning, because a credit note must
    never be blocked; and it runs whether or not its module is enabled, its constructor
    needing nothing but `Deps.Logger`. **The cross-module lock order**
    (`contracts.InvoicedWorkOrder`) is Invoices' own first — the document, the settings
    row, the counter, then a credit note's original — and then the source modules in one
    fixed order: Projects (the project rows, then their milestones), Expenses (the
    claims, then the lines, by id), Time (the entries, by id), Energy (the settlements,
    by id — an energy row never shares a transaction with work, so its place only has
    to be fixed), Orders (the orders, then their lines, then the allocations, each by
    id — last, after every kind of work, since one invoice may bill a project's hours
    and an order's lines). No source module ever
    locks a row of `invoices`, and a transaction that locks both an invoices document
    and a source module's row takes the document first — which is why the customers
    merge and anonymisation call the holders of the modules that provide invoiced work
    after every other module's, Invoices' included (rule 5's partition).
    The rule every locked transaction keeps is restated for it: **no call that takes
    its own connection or leaves the process while a transaction holds locks**. A
    directory read takes a second connection from the pool and an object-store call
    leaves the process, so neither is ever made under a lock; a holder's command runs
    on the caller's transaction and does neither, which is what rules 8, 9 and 10 have
    in common.
    Invoices' half: before the issue's transaction it reads the draft's work, the
    projects it belongs to (`ProjectDirectory.Projects` — a project that no longer bills
    the draft's customer is `source_customer_changed`, and with Projects switched off a
    draft that bills work fails closed with `projects_unavailable`, both before a number
    exists) and the issuer's name (`UserDirectory.User`); a kind no holder claims is a
    composition bug, an error log and a 500, never a skipped stamp. Under the lock, after
    every check and the number, it reads the draft's work again — any difference, a save
    slipped in between, is `invoice_changed` — applies the projects' billing types as
    read before the transaction (`source_not_selectable`), calls each kind's holder **once, in
    `contracts.InvoicedWorkOrder`**, with `InvoiceRef{ID, Number, IssueDate, IssuedAt,
    IssuedBy, IssuedByDisplay}` — `IssuedAt` the issue's own clock, read once and also
    the document's `issued_at` — and only then moves its `line_sources` from held to
    invoiced and writes the document. A refusal names the first line holding the source
    and the source (`linePosition`, `sourceKind`, `sourceId`). Today's holders are time,
    expenses and projects ([Invoicing work](/en/reference/invoices/#invoicing-work)), and
    energy (`energy.settlement`,
    [The invoiced-work holder](/en/reference/energy/#the-invoiced-work-holder)), whose
    holder runs its own recomputation of a settlement under its lock with
    `InvoiceRef.IssuedAt` as now — a read of its own schema on the caller's
    transaction, not a clock or a contract, and orders (`orders.allocation`,
    [The invoiced-work holder](/en/reference/orders/#the-invoiced-work-holder)), the
    one holder that publishes — `orders.sales_order.invoiced`, once per order, on the
    issue's transaction. An order allocation's project is not judged by the issue: only
    work's is, so a draft of orders alone needs no project directory.
11. **Staff writes identity's accounts.** The fourth sanctioned cross-module
    direction, staff → identity, made for the accounts of the people Staff
    registers: `contracts.AccountProvisioning`, in the single-provider slot
    `Accounts` (rule 5), which identity provides. Through it a caller prepares a
    Staff-managed user (`Prepare`), enables it (`Activate`), disables it
    (`Deactivate`), updates it (`Update`), erases it once the person is erased in Staff
    (`Erase`), reads it (`Account`) and reads the roles an
    Owner named for Staff to give (`ProvisioningRoles`), and announces the account an
    external provider will create for a person (`Expect`) or withdraws that
    announcement (`Unexpect`), so the user the provider's SCIM push or the person's
    first sign-in creates is adopted as Staff-managed and numbered on arrival, with no
    Owner in between — and sets or clears a linked user's **managed avatar**
    (`SetManagedAvatar`), HR's photo standing in front of the user's own avatar, which
    identity keeps aside and shows again on the clear; it applies whether or not Staff
    manages the account, is never set for an Owner or a SystemAdmin
    (`ErrAccountPrivileged`; a clear is always allowed), and identity judges the image as
    an upload (`ErrAvatarInvalid`). It stays the one
    write direction into identity: the avatar travels on it, not on a contract of its
    own. **The kind travels on the call**: `AccountSpec.Kind` and
    `ActivateOpts.Kind` carry the person's account kind — `contracts.AccountKindLocal`,
    `AccountKindExternal` or a provider's key (`entra`; `contracts.ValidAccountKind`
    admits any `^[a-z][a-z0-9]{1,9}$`, `contracts.IsProviderKind` tells it from the
    two) — and that, never the installation's
    mode alone, decides what identity creates, gives and invites: `Prepare` creates a
    user only for `local` and only finds for the two link-only kinds (`ErrAccountNotFound`
    when none holds the number); `Activate` invites and gives the roles for `local`,
    only re-enables for `external`, and for a provider's kind re-enables and gives the roles once,
    on the first activation of the adopted user (`users.provisioning_roles_at`). `Mode`
    says whether identity holds the credentials (`local`) or an identity provider does
    (`federated`), derived by every process from `identity.sso_settings`, the row the
    Single sign-on card writes, so an API process and a worker never disagree; it is
    what a call naming no kind reads its kind from (a caller of the previous release)
    and what Staff seeds its people's kinds from, once. Unlike
    rules 8–10 it is **not** a holder that runs on the caller's transaction: every
    method runs on identity's own pool, in identity's own transactions, and mails after
    its own commit. So the rule every locked transaction keeps is strict here — **a
    caller never calls it inside one of its own transactions or read-only snapshots,
    and holds no connection or lease across a call**; a worker claims its row in one
    short transaction, commits, calls identity holding nothing, and writes the result
    in a second. Every write names its `contracts.Actor` — exactly one of a user and a
    known system actor (`system:staff-accounts` or `system:staff-retention`), which
    identity writes to its audit;
    a provider refuses an actor that is not `Actor.Valid` with `ErrInvalidActor`
    before it writes anything. The spec
    carries no roles: identity assigns the set an Owner named, only on the `Activate`
    that enables a user Staff disabled (`local`) or once for an adopted user (`entra`),
    so `staff:manage` can never mint an account holding a role it chose, nor give back
    one an Owner removed. `Erase`, `Expect` and `Unexpect`, like `Deactivate`, read no
    mode. `Erase` refuses a user that is not disabled (`ErrAccountEnabled`), not
    Staff-managed (`ErrAccountNotManaged`), privileged (`ErrAccountPrivileged` or
    `ErrLastOwner`) or gone (`ErrUserGone`), deletes the disabled Staff-managed user and
    their dependent rows, and pseudonymises the snapshots of identity's audit rows naming
    them (`user.staff-erased`). `Expect` refuses an announcement that is not valid — no object id and no principal
    name, or a `ReplaceUserID` that is not the managed, pending, credential-less user
    holding the number — as an invalid spec, a number an unmanaged user holds
    (`ErrEmployeeNumberTaken`) or a managed Owner holds (`ErrAccountPrivileged`), and
    records nothing for a number whose managed user has already arrived; one recording a
    `ReplaceUserID` revokes that user's open invitations `replaced` in the same
    transaction. `ActivateOpts.FreshInvitation` never re-sends an open invitation, and no
    `Activate` invites within ten minutes of the last invitation naming the user. The employee number decides whose
    account a user is: `Prepare` finds by it, and a caller acts on a user only when
    identity's number is the same as its record's by `contracts.SameEmployeeNumber`
    — the same `contracts.NormalizeEmployeeNumber` key (trimmed, NFC-normalised,
    upper-cased by Go), which identity stores as `users.employee_number_key` and keeps
    unique, so no database collation decides which two numbers are one. **The identity
    address never crosses into the caller**: `Account(ctx, userID, wantEmail)` takes
    the address the caller would set and answers only `AccountRef.EmailDiffers`, judged
    by identity's own address normalisation and false for a placeholder no mail
    reaches; `AccountRef.EmailFollows` says whether `Update` would still write it (the
    user pending and no invitation naming them delivered yet). `AccountRef.Privileged` marks an Owner or a SystemAdmin, managed or not
    (a managed user promoted to Owner is unmanaged by the promotion), whom every write
    refuses with `ErrAccountPrivileged`, so a caller reports them without calling.
    `AccountRef.Created` is true only on the `Prepare` that created the user, so a
    caller undoing its own creation (its record gone while `Prepare` ran) deactivates
    only a user it created, never one it found.
    A directory-deactivated non-Owner reads `disabled` by `directory`
    (`contracts.DisabledByDirectory`) while SCIM is configured; the writes still act
    on identity's record of such a user, and `Deactivate` — which reads no
    authority mode — disables a leaver whatever the directory says. Each refusal is a
    sentinel error (`ErrAccountNotManaged`, `ErrAccountPrivileged`,
    `ErrBlockedByOwner`, `ErrAccountEmailTaken`, `ErrEmployeeNumberTaken`,
    `ErrInvalidActor`, `ErrEmployeeRoleMissing`, `ErrEmployeeRoleUnsafe` (a role in
    the set has come to carry an administrative key), `ErrAccountNotFound`,
    `ErrUserGone`, `ErrLastOwner`; `ErrModeMismatch` is declared but answered by
    nobody since the settings moved into the database, and goes next release);
    anything else is a failure to retry. The provider is stateless — api mode with `WORKERS_IN_PROCESS` builds it
    twice, once for Compose and once for Workers, and identity's tests pin that two
    instances answer the same and that `module.Workers` hands a worker identity's own
    provider. With no provider `Deps.Accounts` is nil, which a
    caller answers as "accounts are unavailable".
12. **The provider is the gatekeeper.** A module that answers a many-provider *read*
    on behalf of a person — today `contracts.CalendarSource`, the rows it projects onto
    that person's calendar — applies its own visibility rule to the principal in the
    request's `ctx` (`contracts.HasPermission` reads the request from it) and answers
    **nothing, not an error**, when the caller lacks its permission. The consumer never
    re-states the rule, which already exists in the provider as a query the provider's
    module owns — Projects' `visible()`, Time's "mine", Staff's self-service link,
    Planning's `planning:access` with the person Staff's `Self` answers — and
    a copy would drift; it shows what comes back and never says what was withheld. A
    provider that **fails** returns an error, and the consumer fails the whole request
    rather than show the rest: absent must never also mean "could not be read",
    Customer 360's rule. The consumer calls a provider with the request's context and
    never inside one of its own transactions, through one accessor in its
    `contractscalls.go` that refuses such a call; a provider called without a request —
    from a worker — answers nothing. The one exception is a read on one user's behalf
    where something the user made names them — a token the consumer has already matched
    to that user (the calendar feed), or **the person's own standing setting** (the
    calendar's Outlook sync worker, which reads the providers and Staff's
    `DirectoryAccount` for the user whose `calendar.mailbox_sync` row it syncs, after
    `UserHolds` has said they still hold `calendar:access`): the consumer asks under `contracts.AsUser(ctx, userID)`, on a context that
    carries no request, and `HasPermission` answers through `Access.UserHolds` — whether
    that user may act at all, by the same person checks a session is resolved under (not
    disabled, not pending, not deactivated by the directory while SCIM is configured
    unless an Owner) and not locked out, and holds the keys, an Owner holding all — so the
    provider's gate is still its own and still identity's to answer, with
    no change to the provider. `AsUser` never widens a request: a request in the context
    is always checked through `Check` first. A provider reads its own tables only
    ([Calendar](/en/reference/calendar/#the-sources-contract),
    [the feed's read](/en/reference/calendar/#reading-the-providers-for-the-feed)).
    The single-provider `contracts.Workforce` is bound by the same rule: Staff, its
    sole provider, applies the Colleague rule — `staff:view` (or, for the catalogue
    alone, `staff:manage`), and the person in employment on today or on a later day of
    the window asked — to the principal in `ctx`, under a request or `AsUser`, and
    answers nothing, not an error, to anyone else or to a context with neither; a
    declared certificate is not held. `Self`, the caller's own person, is read under
    Staff's Self rule instead, for `staff:access`; and `DirectoryAccount`, the caller's
    own account in an external directory, under Staff's own principal alone — it needs
    no key, being the principal's own account id and nothing more, and never answers for
    another user. The window of `People` and
    `Capacity` is bounded — from the Monday thirteen weeks before today to three years
    after it — and their figures are derived: hours a week and whole days away, **never
    a share**, a type or a terms row. The fit for a proposed booking —
    `PreferenceFits`, a score and the kinds of preference that lowered it, never a
    preference's value — is a batch over the candidates under the same rule, judged on
    the booking's own facets alone. Planning, its consumer, never re-states the rule
    ([Staff](/en/reference/staff/#the-workforce-contract)).
    The single-provider `contracts.SSOConnections` is provider-gated too: identity, its
    sole provider, checks the request in `ctx` against `policy:Owner+OwnerManagement`
    through its own access and answers nothing, not an error, to anyone else or to a
    context with no request; it requires a second factor only when Owners must use one
    (`OWNERS_REQUIRE_MFA`), as every Owner endpoint does. `Unconnected(ctx, limit)`
    lists at most `limit` users not yet connected under the configured issuer, with the
    address and employee number each is matched by, and
    answers an Owner `contracts.ErrSSONotConfigured` while OIDC is off — not
    configured is not "nobody left to connect"; `KeyHolders` answers who holds a
    directory account's object id or principal name (`expected`, `mapped` or
    `account`) — `contracts.DirectoryKey`, whose keys are the configured provider's:
    the object id is the directory's immutable id (Entra's object id, Google's user id,
    which identity reads from a Google token's `sub`), the principal name its sign-in
    name (Entra's UPN, Google's primary email). The address is for the match alone and never leaves Entra, its consumer,
    which calls the port only through the accessors in `internal/entra/contractscalls.go`
    that refuse a call made inside one of its transactions
    ([Microsoft Entra ID](/en/reference/entra/#what-the-module-reads-of-identity)).
    The single-provider `contracts.CalendarBusy` is provider-gated too: the calendar,
    its sole provider, answers the principal of the request in `ctx` by its own
    colleague rule — their own user id under `calendar:access`, anyone else's under
    `calendar:view-colleagues` — and nothing, not an error, for an id the caller may not
    see or a user who is not active, and for every id when `ctx` carries no request or
    carries the `AsUser` mark: unlike the many-provider sources, it never answers a read
    on someone's behalf; the window is bounded (400 days,
    500 users) and the figures merged instants and dates, never an appointment's title
    or an Outlook event's. Planning, its consumer, calls it with the request's context,
    never inside a transaction, and shows what comes back without re-stating the rule
    ([Calendar](/en/reference/calendar/#the-busy-time-contract)).
    The single-provider `contracts.Production` is provider-gated too: Manufacturing, its
    sole provider, checks `manufacturing:access` against the principal in `ctx` — a
    request, or `AsUser` — and answers nothing, not an error, without it or with neither
    (manufacturing design M46a). `ForOrderLines` answers the manufacturing orders made
    for sales order lines, backorders included, with their number, status, quantities,
    site and dates; `HasBOM` which variants have a default active bill of materials — ids,
    figures and states, never a cost. Orders, its consumer, calls it with the request's
    context on the pool, after its own transaction, and checks nothing itself
    ([Manufacturing](/en/reference/manufacturing/#the-two-contracts)).
    The single-provider `contracts.DeliveryTrace` is provider-gated as well, and is the
    first to **refuse** rather than answer nothing: Orders, its sole provider, checks
    `orders:access` against the principal in `ctx` — a request, or `AsUser` — and answers
    `contracts.ErrForbidden` without it or with neither (manufacturing phase 2 M90), so
    its consumer can tell *no deliveries* from *not allowed to see them* and say so.
    `Deliveries` answers posted deliveries with their order's number and the customer's
    id, never a name. Inventory, its consumer, calls it once per trace with the request's
    context, on the pool and never inside a transaction, shows a delivery as its movement
    alone on the refusal, and reads the customers' names through
    `contracts.CustomerDirectory` only for a caller holding `customers:view`, which it
    checks itself ([Inventory](/en/reference/inventory/#who-received-it)).

    Four single-provider reads are the other class, **consumer-gated**:
    `contracts.ProjectActuals` (Time → Projects), `contracts.Timesheets` (Time →
    Staff), `contracts.ProjectDirectory.ProjectsByStatus` (Projects → Planning) and
    `contracts.SalesOrderLines` (Orders → Manufacturing).
    Their provider performs no authorization at all, because the rule that decides who
    may see the figures is the consumer's and the provider cannot know it — Projects'
    financial rights, Staff's manager rule, Planning's `planning:view`, Manufacturing's
    `manufacturing:orders-manage` for its new-order form and its make-to-order worker,
    which calls with no principal. `SalesOrderLines` carries the customer's id and never
    their name: Manufacturing shows the name only to a caller who also holds
    `orders:access`, read through `contracts.CustomerDirectory` (manufacturing design
    M43a). Its third call, `Unreserved(variantIDs, siteID)` (manufacturing phase 2
    M107a), carries no customer at all — order and line ids, the variant and the open
    quantity a line does not hold reserved — and its consumer, the replenishment job,
    calls it with no principal and shows only the sum. Orders reads the reservations it
    subtracts through `StockLedger.ReservationsFor` on its own read-only transaction:
    a provider may call the ledger on a transaction of its own, never on the caller's. The consumer decides before it
    calls, with the request's context and never inside a transaction, and shows only
    what its own rule allows; the provider answers whatever was logged. A contract is
    one class or the other, said in its doc comment: a provider-gated read is never
    called without a principal, and a consumer-gated one never trusted to filter.
13. **Staff writes a provider's directory.** The fifth sanctioned cross-module
    direction, Staff → a provider module, made for the accounts and groups of the
    people Staff registers: `contracts.IdentityProvisioner`, in the many-provider slot
    `IdentityProvisioners` (rule 5), which a module offering an external identity
    provider fills. The first filler is `internal/entra`, under the key `entra`
    ([Microsoft Entra ID](/en/reference/entra/)); another directory's comes later.
    Its tests run against a fake Graph, `internal/entra/graphtest` — an
    `http.RoundTripper` wired through `modtest.WithTransport` in the guarded
    transport's place, so the module's real client runs against it — kept under the module's own package rather
    than in `modtest`, because it is Entra's shape alone: only the module's tests import
    it (depguard lets a module import its own subpackages, and no other module's). Its
    `Provider` reads the module's own rows and makes no network call, since Staff asks it
    on every `GET /meta`, and neither does `RevealPass`, which takes the sealed pass
    from the module's own row, nor `GroupTraits`, which declares a group's traits from
    the module's own state. Staff is the authority: it
    decides what exists and when, from the register, and the provider executes against
    its directory and answers what it did — `Prepare` finds the account holding the
    employee number or creates it disabled — an account it did not create is adopted
    untrusted, written to only once its adoption is confirmed on the provider's side,
    and answered `ErrExternalUnconfirmed` until then —, `Activate`, `Deactivate`, `Update`,
    `Delete` (a soft delete; `ErrExternalEnabled` on an enabled account), `Account`
    (a read that writes nothing), `RevealPass` (once, then `ErrPassGone`), `SetPhoto`
    and `ClearPhoto` — the account's photo, which the provider converts to what its
    directory takes and answers a refusal of — the picture's or its removal's — as the
    reported `ReportedPhotoNotWritten`, not an error, and whose clear is done when there
    is none — and the group calls — `GroupTraits(ctx)`, the traits a group may carry, which Staff renders
    and stores without naming one; `EnsureGroup`, which makes or adopts the group with
    the provider's own identity among its owners and answers a refused value as a
    `GroupTraitError` keyed by the trait, and what it could not do yet as
    `ExternalGroup.Pending` — a group it found rather than made is adopted untrusted,
    as an account is, and it and `SetMembers` answer `ErrGroupUnconfirmed`, writing
    nothing, until the adoption is confirmed on the provider's side; `SetMembers`, which
    reconciles the members and answers the ones it could not change; and `DeleteGroup`,
    which deletes the group where the directory can restore it, retires it where a
    delete could not be undone, and leaves a group the provider did not create to its
    directory;
    `Provider` names its key — never a reserved one (rule 5) — display name and status
    (`unconfigured`, `connected`, `failing`). It is held to rule 11's lock discipline: every other call reaches the network
    on the provider's own pool, so **Staff never calls it inside a transaction or
    holding a connection**, only through accessors in its `contractscalls.go` that
    refuse such a call, keyed by the provider. Every write names its `Actor` and is
    idempotent. The provider answers only facts about the external object — its id,
    principal name, state, whether a pass waits, and `Reported` codes — and **never an
    address Staff did not give it, except the one it made** for a person who had none.
    Staff bounds what it answers before storing it: a code or a status reason outside
    `^[a-z_]{1,40}(:[A-Za-z0-9_.-]{1,64})?$` is dropped, a principal name over 254
    characters is not recorded, and the made address must pass Staff's email rule —
    each drop logged by field name, never by value; a group's refused value is cut,
    its code at 200 characters and its field at 40.
    Each refusal is a sentinel (`ErrExternalAmbiguous`, `ErrExternalAddressTaken`,
    `ErrExternalSyncedOnPremises`, `ErrExternalPrivileged`, `ErrExternalEnabled`,
    `ErrExternalUnconfirmed`, `ErrPassGone`, `ErrProviderUnavailable`,
    `ErrGroupNotManageable`, `ErrGroupUnconfirmed` — no failure: Staff asks again) and a
    reported
    fact a code that never carries an address; anything else is a failure to retry.
    `Deactivate` — and the `Account` read Staff makes before it — runs whatever
    `Provider`'s status says, as long as a credential works, while the other writes
    answer `ErrProviderUnavailable` unless the status is `connected`; so Staff writes
    nothing but a `Deactivate` to a provider that is not `connected`.
    Staff writes the provider first and identity second, under the one claim it holds
    on the person's identity link — the provider link is never claimed itself — so the
    account is announced (rule 11's `Expect`) before it arrives
    ([the provider link](/en/reference/staff/#the-provider-link)).
14. **The calendar writes a provider's mailbox.** The sixth sanctioned cross-module
    direction, the calendar → a provider module, made for the copies of a person's
    calendar in their mailbox (the calendar's mailbox sync):
    `contracts.MailboxCalendar`, in the many-provider slot `MailboxCalendars` (rule 5),
    keyed by `Key`, which each module offering people's mail calendars fills. **The
    calendar picks a provider by the person's account**: the one whose `Key` is the
    `Provider` of the person's `Workforce.DirectoryAccount`, never another — a person
    whose account no filled provider answers is `no_account`; it asks each provider's
    `Notified` and `Connections` in turn, with a cursor per key. `DisplayName` names the
    calendar service in the calendar's labels ("Outlook"). The first filler is
    `internal/entra`, under the key `entra`, writing to Exchange Online through Graph
    ([Microsoft Entra ID](/en/reference/entra/#the-mailbox-calendar)); the interface
    names no vendor — a mailbox is an account's external id, a calendar an opaque id, an
    event a `MailboxEvent` — so another directory's could fill it later. The calendar is
    the authority: it decides what a person's copies are, from the appointments and the
    projected items, keeps the map of what was written and the person's setting in its own
    schema, and the provider writes and answers what it did. It is held to **rule 13's
    lock discipline**: `Key` and `Status` read the provider's own state with no network,
    and every other call reaches the network on the provider's own pool, so **the
    calendar never calls it inside a transaction or holding a connection** — only through
    the accessors in its `contractscalls.go` that refuse such a call, between the short
    transactions its worker writes the map in. Every call is **idempotent or answers what
    it did per operation**: `EnsureCalendar` answers the calendar it made, or adopts
    one only when it carries the installation's marker, `DeleteCalendar` deletes only a
    calendar carrying it (`ErrCalendarNotOurs` otherwise) and is done for one already
    gone, `Find` answers only the events carrying it, and `Apply` answers one result
    per op, in order — done, `Gone`, or the op's refusal — so a slice the network cut
    short is judged op by op and a create whose outcome is unknown is found again by its
    key (`Find`). The provider writes **only to accounts it manages** — an object id it
    made, or whose adoption is confirmed, and not deleted, else `ErrMailboxUnknown` —
    and the calendar names the mailbox only from Staff's enabled `DirectoryAccount` under the
    person's own principal (rule 12), matched against the provider's `Key`, so neither
    side guesses a person's mailbox. **The provider acts on the person's own delegated
    connection** — their Outlook connection, made by them, its token the provider's to
    hold — never on a permission granted to the application over every mailbox (Outlook
    connection design M9). Its refusals are sentinels (`ErrMailboxUnavailable`,
    `ErrMailboxUnknown`, `ErrMailboxDenied` — a mailbox the directory refuses, not a
    permission scope —, `ErrMailboxGone`, `ErrCalendarNotOurs`, and
    `ErrConnectionNeeded`, whose `*ConnectionNeededError` carries the reason: the person
    never connected or their connection is gone, it is closing, or the code the
    directory's refusal gave); anything else is a failure to retry. `ErrConnectionNeeded`
    is a person-level stop the calendar parks on, never a failure. **The removals** —
    `DeleteCalendar`, `Find`, `Unwatch`, and `Apply` when every op is a delete — are
    served by a closing connection while it holds its token, and answer `ErrMailboxGone`
    once nothing can remove them, so a disconnect removes the copies and a leaver's,
    whose token is forgotten first, are forgotten. A provider in `cleanup` — its switch
    off, a credential still working — takes deletes only, on each person's closing
    connection, so a person's copies can always be removed while a token can
    ([Calendar](/en/reference/calendar/#outlook-sync)).
    The same slot carries the calendar's **reads** of the mailbox, made for the busy
    time it shows: `Delta`, the default calendar's changes a page at a time, and
    `Marked`, which of a list of events carry the installation's marker. They keep the
    same discipline — `Status` first, the network on the provider's pool, never called
    inside a transaction, each page written by the calendar in its own short transaction
    after the call — and are **refused under `cleanup`** (`ErrMailboxUnavailable`): a
    read is never needed to remove copies. A delta's link is the provider's opaque
    string, **held to the mailbox it was answered for**: the provider follows only a
    link of that mailbox's delta and refuses any other, so a stored link can never
    point it elsewhere. A link the directory no longer takes is
    `ErrMailboxDeltaExpired`, and the calendar starts again. Nothing of what an event is
    about crosses the contract — its times and how it shows the person's time alone
    ([Microsoft Entra ID](/en/reference/entra/#the-reads)).
    The **write-back** adds two reads under the same discipline, both refused under
    `cleanup`: `Versions`, the copies' ids and change keys — nothing of what the events
    say — and `Events`, events read whole by id through their calendar's own path, with
    the ids the directory answered "not found" for in that calendar apart (`Gone`), so
    only the directory's own answer makes a delete and an id read neither way infers
    nothing; `Find` answers whether an event has become a meeting, by its attendees'
    count alone. It opens **no new direction**: the
    calendar writes its own appointments from what the provider read, through its own
    write paths on the person's behalf (rule 12), with no transaction open across a
    call. **What an event says crosses the contract only for the copies and the
    provider's own calendar** (the *Vantigo* calendar), **and only for the write it
    feeds**: the subject, the body and the location are compared and taken into the
    appointment, never stored elsewhere, and neither side logs a word of them; an
    event's attendees cross as a count alone. An update that names a key writes it as
    the marker, the same value a copy already carries
    ([Calendar](/en/reference/calendar/#the-write-back),
    [Microsoft Entra ID](/en/reference/entra/#the-write-backs-reads)).
    The **change notifications** add three calls to the same slot, in the same
    direction — calendar into provider, never the reverse: `Watch`, which makes sure a
    mailbox's changes are notified (nothing, a renewal or a create), `Unwatch`, which
    deletes the subscription, and `Notified`, which answers the mailboxes notified since a
    time. They keep the same discipline: the first two reach the network on the provider's
    pool through the calendar's accessors, never inside a transaction, and `Notified` reads
    the provider's own rows with no network. `Watch` is refused under `cleanup`;
    **`Unwatch` is allowed there**, so a clean-up can still remove a subscription, and
    answers nil for one already gone, so a refusal is the only thing that leaves the delete
    owed. `Notified` answers only a mailbox and a time: **a notification is a nudge and
    nothing more**, and what changed is read by the calls above. The provider's webhook,
    anonymous and on the public host, writes the provider's own rows and **never calls the
    calendar** ([Calendar](/en/reference/calendar/#change-notifications),
    [Microsoft Entra ID](/en/reference/entra/#change-notifications)). **`Connections`**,
    beside `Notified` and under the same rule — the provider's own rows, no network —
    answers the people's connections that moved since a time (a mailbox, the state it
    moved to, when), so the calendar wakes a person who connected, reconnected or lost
    their connection within a cycle, still in the same direction
    ([Calendar](/en/reference/calendar/#parking-and-waking)).

15. **Communications writes through, and reads from, a provider's mailbox.** The seventh sanctioned
    cross-module direction, Communications → a provider module, made for a team's shared
    mailbox as a channel (Outlook connection and Microsoft 365 mail design M11):
    `contracts.ChannelMailbox`, in the many-provider slot `ChannelMailboxes` (rule 5),
    keyed by `Key`, which each module offering mailboxes to send from fills. The first filler is
    `internal/entra`, under the key `entra`, sending from Exchange Online shared mailboxes
    through Graph on the application's own token ([Microsoft Entra
    ID](/en/reference/entra/#mail-channels)); the interface names no vendor — a mailbox is
    an opaque id, a message whole MIME — so an IMAP or Gmail provider could fill it later.
    **Communications is the authority on which mailboxes are channels**: a channel's
    `provider` is a provider's `Key`, its `settings` the mailbox id `Check` answered, and
    a channel whose provider no filled provider answers is unavailable, **never sent
    through another provider**; a channel moves between two mailbox providers only by way
    of SMTP. `Send` answers a `SentMessage` — the Message-ID the message left with and the
    provider's thread id, each empty when unknown — which Communications records so a
    reply threads either way. **The provider writes to, and reads from, only a mailbox its `Check`
    resolved** and recorded on its own side (`ErrMailboxUnknown` otherwise, with no call) —
    Entra records only a shared one — and `Forget` drops one. **The reads are under the
    same gate as `Send`**: `Delta` reads one page of a folder's changes (`inbox`, a link
    the provider issued, or afresh from an instant) and `Raw` one message whole, under a
    cap **the caller** sets; neither reads a message's content into the provider's logs or
    answers, and the provider follows only links that are this very mailbox's
    ([Microsoft Entra ID](/en/reference/entra/#delta-and-raw)).
    `Key`, `DisplayName`, `Status`, `MaxMessage` and `Notified` make no network call;
    `Check`, `Forget`, `Send`, `Delta`, `Raw`, `Watch`, `Unwatch`, `Mark` and `EnsureFolder`
    do, on the provider's own pool, and **nothing is called inside a
    transaction or holding a connection** — only through the accessors in
    `internal/communications/contractscalls.go`, which refuse a call made inside one of the
    module's transactions (marked by its `withTx`) as the calendar's and Entra's do: a
    channel's create, switch and update call `Status`, `Check` and `EnsureFolder` before
    their transaction, the way back to SMTP and channel deactivation call `Unwatch` and
    `Forget` after their commit, the outbox calls `Send` between
    its stamp and its completion, outside both, and the mailbox sync calls `Notified`,
    `Status`, `Delta`, `Raw`, `Watch`, `EnsureFolder` and `Mark` outside any transaction.
    The import's one other call
    out of the module, the customer directory's `ContactsByEmail` for the sender (the
    sanctioned directory read), goes through an accessor there too, so it is refused inside the
    transaction as well. **Every write is idempotent or reports its
    outcome**: `Check` rewrites its record, `Forget` and `Unwatch` are done for a mailbox
    not recorded or a subscription already gone, `Watch` renews or creates, `EnsureFolder`
    returns an existing folder or creates one, `Mark` is idempotent on a message already
    read, moved or deleted (`ErrMailItemGone`),
    and `Send` is at least once, as SMTP is — acceptance is not delivery, and a failure
    after the provider took the message may send it again on the outbox's retry, never on
    the provider's own. Its refusals are rule 14's sentinels — `ErrMailboxUnavailable`,
    `ErrMailboxUnknown`, `ErrMailboxDenied` (here: outside the provider's scope),
    `ErrMailboxGone` — `ErrMailTooLarge`, `ErrBccUnsupported` when the provider would
    not keep every blind copy and sent nothing, `ErrMailboxDeltaExpired` (rule 14's) when
    the provider no longer takes a `Delta` link, and `ErrMailItemGone` when the message
    `Raw` or `Mark` was asked for is no longer there; `Status` answers `available` with the reason
    `tenant_wide` when the provider's grant reaches every mailbox, a warning. Anything else
    is a failure to retry. The interface declares only what Communications calls —
    `Key`, `DisplayName`, `Status`, `Check`, `Forget`, `MaxMessage`, `Send`, `Delta`,
    `Raw`, `Watch`, `Unwatch`, `Notified`, `Mark`, `EnsureFolder`
    ([Communications](/en/reference/communications/#receiving)). The provider's ids are
    opaque and case-sensitive; Communications keeps them in its own receipts.

    **The messenger slot.** A second slot of the same direction serves a channel of type
    `messenger` (Messenger design N1, N2): `contracts.ChannelMessenger`, in the many-provider
    slot `ChannelMessengers` (rule 5), collected through `module.Module.ChannelMessenger` by
    `withChannelMessengers` — which refuses a key filled in both this slot and
    `ChannelMailboxes`, so a provider key names one kind. Where a mailbox is read as a delta of
    mail, a messenger is a chat: the provider queues what it receives under its own insertion
    id, which Communications reads from a cursor (`Pending`) and acknowledges (`Ack`), copies
    attachments through `OpenAttachment` and never as a storage key, learns of new events from
    `Notified`, and sends one part of a message at a time (`Send`) — every call but `Key`,
    `DisplayName`, `Status` and `Capabilities`, which make no network call, a provider call,
    made outside any transaction through an accessor in `contractscalls.go` that refuses one
    inside. `Capabilities` carries the limits of a message's components and the reply window,
    which Communications checks a reply against, and `PublicMedia`, whether the provider
    fetches a card's image from the public host: a `MessengerCard` then carries `ImageURL`,
    an address on the public media route (Messenger design N18), and otherwise `Image`, the
    bytes. Communications also reads the product catalog's images (`ProductImages`,
    `OpenProductImage`) to copy a card's picture into its own storage, before the reply's
    transaction. A channel names its provider by `Key` and its
    Page by address, and is never served by a provider under another key. Communications'
    tests run against a fake (`fake_messenger_test.go`), set by
    `modtest.WithChannelMessenger`; Meta fills the slot under the key `meta` and Compose tests pin that
    a key two modules claim, a reserved one and one filled in both slots are refused.

16. **Communications sends through a person's delegated mailbox.** The eighth sanctioned
    cross-module direction, Communications → a provider module, made for personal sending
    (Outlook connection and Microsoft 365 mail design M20): `contracts.PersonalMailbox`, in
    the many-provider slot `PersonalMailboxes` (rule 5), keyed by `Key`, which each module
    offering a person's delegated mailbox to send from fills. **Communications asks every
    provider's `Status`** — each answers `no_account` for a caller whose directory account
    is not its own — and takes the first available answer, else the first whose reason is
    not `no_account`; it records the provider's key with the message (`send_provider`)
    and the outbox sends through that provider only, answering `connection_needed` when it
    is gone. The first filler is `internal/entra`, sending through
    Graph on the caller's or author's delegated token ([Microsoft Entra ID](/en/reference/entra/#personal-mailbox)).
    **Status and Send are guarded by contractscalls.go**: `PersonalMailbox.Status` is checked
    outside transactions before opening one for new conversations or replies; the outbox calls
    `PersonalMailbox.Send` outside any transaction under `contracts.AsUser(ctx, authorUserID)`
    so the message is sent from their account and appears in their Sent Items. On
    `contracts.ErrConnectionNeeded`, the outbox fails immediately with terminal code
    `connection_needed` after 1 attempt, never falling back to the shared channel mailbox.
    `modtest.FakePersonalMailbox` backs tests.
17. **Customer mail goes through a channel the Owner chose.** The ninth sanctioned
    cross-module direction, Invoices and Calendar → Communications, made so that no mail to a
    *customer* leaves on the installation's `SMTP_*` credentials (integrations design T4–T6):
    `contracts.OutboundMail`, in the single-provider slot `OutboundMail` (rule 5), which
    Communications fills through `Module.OutboundMail`; Compose and Workers resolve it into
    `Deps.OutboundMail` after the channel and personal mailboxes, refusing two. A consumer
    names a channel **by id** — `invoices.settings.send_channel`, `calendar.settings.invitation_channel`,
    each with no foreign key across schemas (rule 4) — and never reads `communications.*`;
    Communications is the authority on channels. `Channels` and `Channel` make no network call
    and answer to any module without a Communications permission (names and addresses
    only); `Send` is synchronous under the caller's context, keeps the caller's `Reply-To`
    and `Message-ID` (the idempotency key a provider honours), sends From the channel's
    address, and answers `ErrChannelUnknown`, `ErrChannelUnavailable` (its provider is off
    or its credential missing — nothing was sent), `ErrMailTooLarge`, `ErrRecipientRefused`
    (reserved for a provider that holds a tenant to a list of addresses) or a failure to
    retry, at least once. **Nothing is queued on a consumer's behalf and nothing is called
    inside a transaction**: each consumer's accessor in `contractscalls.go` reports the call
    and refuses one made inside a lock, and each keeps its own evidence and retry (Invoices
    the delivery row and the letter's attempts, Calendar the attendee's *Resend*). A null
    setting is *disabled*, as is a nil slot; a channel that goes away leaves the setting
    as it was, shown as gone, and Communications never writes a consumer's settings. The
    system mailbox (`Deps.Mail`) keeps its one job, writing to the tenant's own employees.
    `modtest.FakeOutboundMail` backs tests.

18. **The assistant and the agents call the contract as their principal.** The tenth
    sanctioned exception to rule 6, and the only one that reaches an HTTP endpoint (the
    Assistant design S2–S4; virtual employees design V8): the platform's **`Operations`
    dispatcher** (`internal/operations`) may call any operation of the merged contract of the
    enabled modules *in-process, through the composed router*, for two principals and no
    one else: the assistant (its person, through an assistant grant) and an agent (the
    agent's own user, through an agent grant). No other module learns either exists: the
    coupling is to the public contract, which is the most stable surface there is.

    - **Only the modules named `assistant` and `agents` receive it.** `Compose` builds one
      dispatcher unbound before any module mounts, sets `Deps.Operations` on those two
      modules' `Deps` alone (every other module's is nil, whatever the caller preset, and so
      is a worker's) and binds it, once the router and the merged contract exist, to the
      contract, the composed catalog, `Deps.Access` and the composed handler. It is for use
      while serving, never while mounting. depguard forbids every other import of
      `internal/operations` — only `internal/assistant`, `internal/agents`,
      `internal/agentloop` (the model loop they run on, which reaches the dispatcher through
      an interface), `internal/module`, `internal/server` and the test harnesses
      `internal/modtest` and `internal/integration` may — and a test composes modules
      called `alpha`, `assistant` and `agents` and proves which of them are handed it.
    - **A call is the person's own.** The assistant module asks identity for a *grant*
      (`Deps.AssistantGrants.Issue`, from the request of the person's message) and puts it on
      the call's context with `contracts.WithAssistantGrant`; the dispatcher builds an
      in-process request carrying nothing else of the caller's. The grant is a context value:
      no header, cookie or query parameter can present one, so a network request cannot.
      `Access.Check` answers the grant's session principal — same user, session, roles and
      MFA state — with `Principal.Via` set, so the rate limit, the access rule, the body cap,
      the handler's row-level rules and every refusal behave exactly as they do for the
      browser. A 4xx or 5xx is a *result*, so the model can read the refusal. The grant never
      satisfies `scim` or a `policy:` rule naming `Owner`, `OwnerManagement`,
      `AuthorizationManagement` or `SystemAdmin`, and it ends with its time, its revocation, its
      session (a sign-out, an expiry) or its user (disabled, locked out).
    - **The grant on the context is of a kind.** `contracts.WithGrant(ctx, GrantRef{Kind, ID})`
      carries `assistant` or `agent`; `WithAssistantGrant` and `AssistantGrantFrom` are the
      assistant's wrappers, and the latter answers false for an agent's grant. For an
      `agent` grant `Access.Check` answers the agent user's own principal — its roles, no
      session, no second factor — with `Via{Kind: "agent", AgentID, RunID, CustomerID,
      ApprovedBy}` (identity's `AgentGrants`), admitted by a permission rule alone: every
      `session`, `scim` and `policy:` rule is refused. `Dispatcher.Call` accepts either
      grant and refuses a call with none; the in-process request's `RemoteAddr` is
      `assistant-<user>` or `agent-<user>`, so the router's rate limits are per principal. The
      dispatcher reads an agent's run from **the grant itself**: it asks identity's
      `AgentGrants.Describe` (injected at `Bind`) for the agent, the run and the run's customer
      (nil for a run with none), once per call, and refuses with `no_grant` a grant that is
      expired, revoked or whose user is not an active agent. The result is on the context it
      hands the gate (`operations.AgentRunFrom`), which only the dispatcher can set, so there is
      one source of the customer. Every method (`List`, `Search`, `Effective`, `Validate`,
      `Call`) resolves the principal first; a context with no grant is only the Assistant's own
      confirm and bulk paths.
    - **Every operation has a contract mode** (`auto`, `confirm`, `never`), computed once by
      `Compose`: the operation's `x-vantigo-assistant` when it has one (the OpenAPI lint
      checks the values, and refuses `auto` or `confirm` behind a rule an assistant is never
      admitted to); otherwise `never` for an `anonymous`, `scim` or `session` rule, an Owner-tier
      policy, a permission missing from the catalog, a file upload or download, or the
      assistant's own API; otherwise `auto` for `GET` and `confirm` for every other method.
      The contract's mode is the widest an operation can ever be. **A gate narrows it for the
      workspace**: there is one gate **per kind of principal**, installed with
      `Operations.SetGate(kind, gate)` while the module mounts, the kind being the grant's
      (`assistant` or `agent`). The `assistant` module installs the first, which applies the Owner's
      permission level of each permission the rule names (*Off* makes it `never`, *Read* turns
      `confirm` into `never`); the `agents` module installs the second, which applies the agent's
      own levels and its role's allowlist. `List`, `Search`, `Effective`, `Validate` and `Call`
      take the gate from the kind of grant on the context (a context with no grant, such as a
      person confirming a change in a request of their own, is the assistant's). The dispatcher
      keeps the narrower of the contract's mode and the gate's, so a gate can never widen the
      contract. `Info` carries the contract's `Mode`,
      the `Permissions` the rule names and whether any is `Sensitive`; `List`, `Search`, `Effective`
      and `Call` answer the effective mode. **Without a gate the dispatcher fails closed**: for the
      assistant, an operation whose rule names a `Sensitive` permission is `never`, which is how the
      platform behaved before the Assistant module existed; for an agent, **every** operation is
      `never` (an agent has no default gate). A gate that cannot decide answers `never`
      ([the reference](/en/reference/assistant/#permission-levels-and-the-gate)).
      `never` operations are not offered to the model and `Call` refuses them; `confirm`
      ones are refused unless the call says the person confirmed. A `POST` that only reads is
      marked `auto` by hand; an operation that would pass a secret through the model is marked
      `never`. `TestOperations_TheRealContract` derives every mode again from the contracts and
      pins the hand-written marks, so changing one is a decision.
    - **An operation that returns a customer's data declares which customer.** The OpenAPI
      extension `x-vantigo-customer-scope` is either `{param: <name>}` — a path or query
      parameter that carries the customer's id — or `{resource: <field>}` — a field of the JSON
      answer (a dotted path) that holds it. `Info.CustomerScope` carries it to a gate. For an
      *agent's* call the dispatcher enforces it **before and after** the call, from the run's
      customer in the grant: it fills a `param` the agent left out, refuses a
      different value, and refuses an answer whose `resource` field is not the run's customer
      (the body is dropped, so another customer's data never reaches the agent); a run with no
      customer is refused every scoped operation; and a malformed scope refuses always. All
      are `customer_scope` errors. The assistant is not scoped: its reach is its person's. The
      agents' gate allows only the operations of its role's allowlist, so an operation that
      returns customer data and declares no scope is never on one; a contract test keeps a
      table of the customer-data operations the front desk uses and fails for one without the
      extension. The marked set is small on purpose (Communications' conversations, Customers'
      customer and contacts, Orders' sales orders, Invoices' list and invoice); an operation
      that can name no customer, such as a delivery or a shipment of an order, is not marked and
      so cannot be allowlisted.
    - **Modules do nothing for it.** No operation is written, annotated or exposed for the
      assistant but the optional `x-vantigo-assistant`; a module that grows an operation grows
      the assistant's reach with it, behind the same rule. The contract's `summary` is what the
      assistant searches by, so the OpenAPI lint requires one on every operation.
    - **It is bound where the API is served, so a job that calls it runs there.** `Compose` builds
      and binds the dispatcher in a process that serves the API; `module.Workers`, which worker
      mode and the workers of an API process use, never composes, so the `Deps` a worker's
      constructor gets carry no dispatcher (a test pins it). A long job that must call operations
      — the assistant's bulk changes ([the reference](/en/reference/assistant/#bulk-changes)) — is
      therefore a goroutine of the API replica that the person's own request started, under a
      claim on its database row that another API replica takes over when it lapses; never a
      worker. **The assistant keeps its own runner** rather than a platform job helper: the
      design (S13) expected the imports' runner to become a shared helper, but this job is driven
      by a dispatcher, claimed and resumed rather than leased, and counts per item with its claim,
      so a shared helper would carry a parameter for every difference. The imports' and billing
      runs' own runners are untouched. The one rule the new runner shares with theirs is the lease
      rule: **it holds no transaction, advisory lock or connection across a dispatcher call**, and a
      test runs a whole job on a pool of one.
    - **Work that needs the dispatcher and is not a person's request is an API runner.**
      `Module.APIRunners func(Deps) []Runner` (a `Runner` is a `Name()` and a `Run(ctx)` that returns
      when the context ends) is called by `module.ComposeAPI` for every enabled module, **after**
      the dispatcher is bound, so a module that receives `Deps.Operations` can call through it from
      the first tick. `cmd/vantigo` starts the runners with `module.StartRunners` in an API process
      (`api`, or `server` mode) on the context that stops the workers, and waits for them on
      shutdown; worker mode never starts one, because it has no dispatcher. `Compose`, for a caller
      that has no process of its own, drops them; the `modtest` harness starts them and stops them
      with the test. The agents' run poller is the first. It follows the lease rule above, and a
      panic or an error in one runner is logged and does not touch the others.
    `modtest.Harness.Operations` and `AssistantContext` bind a dispatcher to a harness and
    issue a grant for a signed-in client.

19. **A module that holds a person's data learns of their erasure by asking.** When identity erases
    a person it deletes the user and tells no module, and no module may reach into identity's
    schema or be called by it. A module that keeps data about a user, and has no contract that is
    told of the erasure, **sweeps against `contracts.UserDirectory`**: on a schedule, in chunks, it
    asks `Users(ids)` about the user ids it holds — "an id that does not exist is simply absent" —
    and deletes the rows of those that are gone. The first is the Assistant's conversations, runs,
    actions and switches, swept by its daily retention worker
    ([erasure](/en/reference/assistant/#erasure)). The price is written down in the module's
    reference: the data of an erased person can outlive the erasure by one cycle, and a disabled
    user is still known and is kept. A module that is handed the erasure instead — a customer's,
    through `contracts.CustomerPersonalData` (rule 9) — keeps rule 9's discipline. Tested with a user
    identity does not know and the sweep run on the module's own worker.
    Not every row naming a user is swept: Inventory's movements are immutable records of the
    stock ledger and keep the poster's user id and display name after the person is erased,
    which its reference states ([Inventory](/en/reference/inventory/)). Manufacturing's
    productions, its orders' events and its time logs are such records beside Inventory's:
    they keep the poster's, the actor's and the operator's user id and display name. A time
    log's free-text note is not a record and is swept: Manufacturing's daily
    `manufacturing-erasure` worker asks `Users(ids)` about the operators with a noted log
    and blanks the notes of those that are gone
    ([Manufacturing](/en/reference/manufacturing/#personal-data)).

20. **Stock a variant holds is asked for inside Products' transaction.** Rule 8's
    holders write under the caller's lock; this one only answers, and is made for
    deleting a variant: `contracts.VariantReferenceHolder`. A module that keeps stock, or
    another fact, of a product variant in its own schema declares
    `Module.VariantReferences`, and the products module calls every holder **inside its
    own transaction** — the one that deletes a variant, and the one that turns a Goods
    product into a Service — after it has locked the variants `FOR UPDATE` (the one
    variant; every variant of the product, in id order, for the type change) and before
    it writes. `VariantHeld(ctx, tx, variantID, purpose)` is told which write asks —
    `contracts.VariantHoldDelete` (`delete`), `contracts.VariantHoldTypeChange`
    (`type_change`) or, for a variant `PUT` that changes the variant's tracking mode,
    `contracts.VariantHoldTrackingChange` (`tracking_change`, manufacturing phase 2 M51)
    — because a holder may stop one and not another: open orders stop a delete, never a
    change to Service (orders design O30), and open documents never stop a tracking
    change. It answers nil or a
    `contracts.VariantHold` whose `Kind` says what holds it: `stock`, a quantity in the
    variant's own unit as a decimal and the number of places it lies in, or
    `open_documents`, the number of open quotes, orders and subscriptions that name it,
    or `reserved`, a quantity orders have reserved (rule 24) and, in `Locations`, the
    number of sites, or `in_transit` (`contracts.VariantHoldKindInTransit`), a quantity on
    its way between two sites on an Inventory transfer and, in `Locations`, the number of
    transfers — Inventory answers it after `stock` and `reserved`, for both purposes.
    The first hold refuses the write with 409 — *Variant has stock* and *Product has
    stock*, the unit in the refusal from Products' own row, or *Variant is on open
    documents* and *Product is on open documents*, naming how many. The holder keeps
    rule 8's discipline: it runs its own SQL, on its own schema, from its own package —
    so rules 1, 4 and 6 hold as they do for any read — on the caller's `tx`, never on a
    pool; never begins, commits or rolls back a transaction; never reads a directory or
    any other contract, and never calls back into Products; and locks nothing of its own
    beyond what its read takes, because every writer of its rows holds `FOR SHARE` on the
    variant (rule 21) and so has committed, or waits, before the answer is read. It is
    collected from every module Compose is given, enabled or not, as rule 8's holders
    are: every schema is migrated whatever `MODULES` says, and stock in a disabled
    module's schema is still stock. Only products calls the slot. Inventory holds a
    variant by its balances and answers the same for every purpose, stock stopping
    all three; a tracking change it refuses is 409 *Tracking held*. Manufacturing holds one by its active bills of materials, which name it as
    their product or on a line, and by its manufacturing orders not done or cancelled,
    which name it as their product or on a component line, and answers
    `open_documents` with their count for the
    type change as well as the delete: a service is neither consumed nor produced
    (manufacturing design M16a), which departs from Orders' O30 on purpose
    ([Products](/en/reference/products/#contracts-for-other-modules),
    [Inventory](/en/reference/inventory/),
    [Manufacturing](/en/reference/manufacturing/#the-variant-holder)). Another question a variant's write must
    ask is a purpose on this holder, not a second slot: the tracking mode, which may
    change only while nothing holds the variant, is the third (`tracking_change`).
21. **One locking read, on the caller's transaction.** Every other contract call is
    made on the provider's pool, with the request's context, **before** the caller opens
    a transaction (rule 8's reason: no in-process lookup under a lock), through an
    accessor in the caller's `contractscalls.go` that refuses one made inside.
    `contracts.ProductCatalog.LockVariants(ctx, tx, ids)` is the one exception (the
    Inventory design I22): it takes `FOR SHARE OF` the variants' rows **and** their
    products' rows — where type and status live — in variant id order, and answers the
    entries, an id that does not exist simply absent. Each entry carries the variant's
    packagings (I24), read under the same locks: a packaging write locks its variant
    `FOR UPDATE` first, so the packagings a posting converts what was entered with stand
    still until it commits. Its rules:
    - **It runs on the caller's `tx`, never on the pool.** It never begins, commits or
      rolls back the transaction, so it takes no second connection under the caller's
      locks. `tx` is a platform type (`pgx.Tx`), as in `contracts.InvoicedWorkHolder`, so
      `internal/contracts` still carries no store type.
    - **Products' rows are locked before the caller's, everywhere.** A posting calls it
      first, before it touches a row of its own; Products' variant delete and type change
      lock the variant rows first too and ask the holders (rule 20) after. A writer of a
      variant and a writer of its stock can therefore only wait on each other in one
      direction, and the share lock is what makes rule 20's answer true: a receipt has
      either committed before the delete reads the holders or waits behind it.
    - **The provider never calls back.** Products reads no contract and calls no module
      while it holds the lock it took for the caller.
    - **It is the one contract call a module may make inside its own transaction.**
      Inventory routes it through its own accessor, `lockVariants`, apart from every
      other call, which still goes before the transaction; its test harness, which
      fails a test on any call out of the module made inside one of its transactions,
      exempts exactly `Products.LockVariants`. Manufacturing is the second caller inside
      its own transaction: every structural bill-of-materials write — create, update,
      archive, reactivate, make default — calls its `lockVariants` first, with the
      product and every component, judges goods from that answer, and only then takes
      the BoM graph's advisory lock and its own rows (manufacturing design M10a); its
      harness exempts the same one call
      ([Manufacturing](/en/reference/manufacturing/#locks)). A second locking read needs a design of its
      own and a rule here.

22. **Identity searches a provider's directory.** The eleventh sanctioned cross-module
    direction, identity → a provider module, made for an Owner connecting existing users
    to their directory accounts (Google Workspace design G8): `contracts.DirectorySearch`,
    in the many-provider slot `DirectorySearches` (rule 5), keyed by `Key` — the OIDC
    provider it serves —, which each directory module fills; Compose collects it, Workers
    does not, as no worker searches. **Identity serves it** on its own Owner SSO endpoints
    (`GET /api/v1/identity/owner/sso/directory/users` and `…/matches`, under
    `policy:Owner+OwnerManagement`), asking only the provider whose `Key` is the configured
    OIDC provider (`no_directory` otherwise), and the host's picker calls identity, never a
    provider module. `Key` and `DisplayName` make no network call; `Users` and `Matches`
    do, on the provider's own pool, and are never called inside a transaction. The
    provider still reads `SSOConnections` for the candidates and the holders (rule 12), so
    identity's gatekeeping is unchanged. The first filler is `internal/entra`
    ([Microsoft Entra ID](/en/reference/entra/)).
23. **An event handler writes its own schema, inside the platform's transaction.** The
    twelfth sanctioned cross-module direction, made for what one module must learn of
    another's change when it may learn of it a little later (orders design O1–O5):
    the domain-events outbox, `internal/events`. A publisher calls
    `events.Publish(ctx, tx, module, ev, occurredAt)` on its own transaction, so the event
    commits with the change that caused it or not at all, and **publishes an aggregate's
    events only while it holds that aggregate's row lock**, so their ids follow commit
    order per aggregate; the event is a DTO of `internal/contracts` implementing
    `contracts.Event`, whose payload carries ids, numbers, amounts and states and never
    personal data. A module that reacts declares `Module.EventHandlers` (rule 5), and the
    platform's dispatcher, `events.dispatch`, runs each handler on **a delivery's own
    transaction**, which the platform opens and commits. The handler:
    - writes only its own module's schema on that `tx`, so a handler whose effects stay
      in the database runs exactly once;
    - **never leaves the process** — no `OutboundMail`, no HTTP, no object store inside
      `tx` (rule 10's restatement, and rule 17's); a handler that must cause mail queues
      it in its own table for its module's own worker to send;
    - locks only its own rows, never another module's, and reads no contract while
      `tx` holds locks — what it needs of another module it reads after, or from its own
      copy;
    - never begins, commits or rolls back `tx`; an error rolls the delivery back and it
      is retried, with backoff, until the eighth failure makes it dead for the operator
      ([The events outbox](/en/admin/management-listener/#the-events-outbox));
    - runs whatever `MODULES` says, as rule 8's holders do: a handler that needs its
      module on checks the setting itself and does nothing.

    **The outbox carries what may lag, never what must refuse.** Anything that must be
    able to refuse the caller's transaction — a stock reservation, the stamp of invoiced
    work (rule 10) — stays a synchronous contract call on that transaction. The first
    publisher and handlers come with Orders; delivery 0 built the outbox, its dispatcher
    and its operator calls with none composed.

    **A handler that needs a contract queues for a worker.** A reaction that must read
    another module, lock another's rows or call a ledger is not a handler's: the handler
    inserts a request into its own schema, and a worker of its module does the rest,
    reading the contract on the pool and only then opening its own transaction. The
    first is Manufacturing's make-to-order (manufacturing design M44a):
    `manufacturing.mto` on `orders.sales_order.confirmed` only inserts an
    `mto_requests` row, unique by the sales order, and `manufacturing.mto-worker` reads
    `SalesOrderLines`, then per sales order takes `LockVariants`, creates and confirms
    the orders and reserves through `ReserveForProduction`. Orders' `orders.mto-reserve`
    on `manufacturing.order.done` likewise only marks the line in its own schema: the
    reserve stays the person's **Reserve now** (M48a). `manufacturing.mto-cancelled` on
    `orders.sales_order.cancelled` sets a flag on Manufacturing's own orders and nothing
    else.
24. **Inventory reserves and issues on the caller's transaction.** The thirteenth
    sanctioned cross-module direction, a module → Inventory, made for an order that
    must refuse to promise stock it cannot have (orders design O45):
    `contracts.StockLedger` in `Module.StockLedger`, which Inventory fills. A caller
    names its document by a `contracts.StockSource{Module, Kind, ID}` — the source
    reference Inventory's movements already hold — and calls, on **its own
    transaction**, `Reserve` (a quantity per document line at a site, checked against
    the site's available: usable on hand — no lot on hold or expired — less what is
    reserved), `Release` (a line's
    reservation, part of it, or all of the document's), `Issue` (one movement of kind
    `issue`, taking stock off its locations and consuming the lines' reservations,
    idempotent by the document) and `ReservationsFor`. Manufacturing calls two more
    (manufacturing design M2–M3): `ReserveForProduction` (`Reserve` at a site that need
    not fulfil orders) and `Produce` (one movement of kind `production`, consume lines
    from-only and produce lines to-only in one site, consuming the reservations as
    `Issue` does, idempotent by the production record). Manufacturing phase 2 makes the
    ledger lot-aware (M67): issue, consume and produce lines name a `LotID` (or an
    issue or consume line asks for `AutoLot`, Inventory taking the lots FEFO under its
    own locks, the picks answered in `StockPosted.Picks`); `Issue` and `Produce` answer
    a `StockPosted`; and three calls join — `EnsureLots` (find or make lots by number
    before a `Produce`), `HoldLots` and `ReleaseLotHolds` (a document's own holds on
    lots). Its rules:
    - **The lock order is Products, then the caller, then Inventory.** The caller calls
      `ProductCatalog.LockVariants` (rule 21) first, then locks its own rows, then calls
      the ledger, which locks Inventory's rows in Inventory's order: the lot counter,
      new lots, the lots named in id order, a movement's header, the locations, the
      balances in (variant, location, lot) order with no lot first, and then the
      reservations. A caller's `EnsureLots` runs the first three, its `Produce` the
      rest; a hold takes the lots `FOR NO KEY UPDATE` in id order. Inventory takes
      `LockVariants` again for the variants' types, already held; it takes no lock of a
      location or a site a tree write could hold first, and an issue follows Inventory's
      own posting order. The caller never locks Inventory's rows another way.
    - **It runs on the caller's `tx`, never on a pool**, writes only Inventory's schema
      with Inventory's own SQL, and never begins, commits or rolls back the
      transaction, as rule 8's holders do. It reads no directory and calls no contract
      but `LockVariants`.
    - **No ledger call makes a contract call inside the caller's transaction.** What it
      needs from outside Inventory — today's date in the workspace zone, whether the
      principal may use an expired lot — the caller reads **on the pool before** it opens
      its transaction and passes as data: `Today` on `StockIssue` and `StockProduction`,
      `today` on `Reserve`, `ReserveForProduction` and `EnsureLots`, and
      `ExpiredOverride`, the caller's `inventory:expired-override`, on an issue and a
      production (manufacturing phase 2 M62). A zero today is a programming error.
      Inventory's tests prove every call records no contract call; Orders' and
      Manufacturing's fake ledgers refuse a call without today.
    - **It refuses only through a typed `*contracts.StockRefusal`**, naming every
      failing line — `insufficient_available`, `insufficient_stock`,
      `site_not_fulfilling`, `variant_not_stocked`, `site_unknown`, `location_unknown`,
      `document_reused`, a production's `site_archived`, `quantity_invalid` and
      `nothing_to_post`, and the lot rules' `lot_required`, `lot_not_allowed`,
      `lot_unknown`, `serial_quantity`, `lot_expired`, `lot_blocked`,
      `lot_dates_differ` and `EnsureLots`' `lot_exists` (a `NewLot` that `MustCreate`
      names a number taken). Each line carries its `Index` in the call's lines and the
      `LotID` and `LocationID` it failed at, so the caller maps it to its own row. `Reserve`, `ReserveForProduction` and `Release` write nothing
      when they refuse; after a refusal from `Issue` or `Produce` the caller must roll
      back its transaction, or a savepoint it took before the call, because the posting
      may already have claimed its movement and created balances. Anything else is a
      failure.
    - **It is composed whatever `MODULES` says**, as rule 8's holders are: a
      reservation made while Inventory was on is still released by a cancel, an edit or
      a manual marking after it is turned off. Whether to reserve and issue at all the
      caller reads from `Deps.Stock`, the read of on hand, available, the lots, the sites,
      the placements and the locations holding a variant (`contracts.Stock`, a single-provider slot set only when
      Inventory is enabled), on the pool and never inside the caller's transaction. Each
      `contracts.StockLocation` that `Stock.Locations` answers carries **`Primary`**
      (`contracts/stock.go`, Inventory phase 2 I55): whether the location is the variant's
      primary location at its site. It is a suggestion, never a rule — Orders sorts it
      first in the pick list and the delivery's proposal, and nothing refuses taking stock
      from another location; a consumer must not treat it as more.
    - **Inventory's own corrections ignore reservations.** Its adjustments, moves and
      counts may take on hand below what is reserved, and available shows negative; an
      issue still refuses beyond on hand.

    Compose and Workers resolve both slots by one helper, `withStock`, after the product
    catalog ([Inventory](/en/reference/inventory/#reservations-and-issues)).

## The AI platform

AI is a platform provider, like mail: the provider, its key and the models the plan allows are
the instance's configuration (`AI_*`, [Configuration](/en/admin/configuration/#ai)), and the
workspace's only choice is which allowed model each job uses. Three pieces make that work, and
none is a numbered rule above because no module reaches into another.

- **`Deps.AI`** is the platform's client ([the AI client](/en/contributing/platform-packages/#the-ai-client)),
  built once by `module.Compose` from `Config`, `Deps.HTTPTransport` and the `AIModels` slot,
  and put on every module's `Deps`; nil when no provider is configured, which a consumer answers
  as unavailable. A module calls `Deps.AI.Complete(ctx, taskKey, ai.Request{…})` for one prompt
  and one answer, or `Deps.AI.Chat(ctx, taskKey, ai.ChatRequest{…}, onDelta)` for a conversation
  with tools whose answer streams. It never names a model, holds a key or builds a client of its
  own, and never calls `Deps.AIModels`. The provider may be a model running on the
  installation's own network (`AI_PROVIDER=openai-compatible`, [Configuration](/en/admin/configuration/#ai)) or Anthropic's (`AI_PROVIDER=anthropic`); the module is told neither, and passes the provider's own record of an assistant turn (`ai.Message.Provider`) back unread. A module's tests choose the provider's wire with `modtest.FakeChatModel` (OpenAI-compatible) or `modtest.FakeClaude` (Anthropic's Messages stream) through `WithTransport`.
- **`Module.AITasks`** declares the jobs a module performs with a model, the way
  `Module.Permissions` declares what a person may do: a `contracts.AITask` has a `Key`
  (`<module>.<task-name>`, lower case, always the declaring module's own prefix, and the primary
  key of an Owner's choice, so it never changes once shipped), the declaring `Module`, a
  `Display`, a `Description` of what the model is asked and with what data, and an optional
  recommended `Default` model. `Compose` validates each (`contracts.ValidateAITask`), refuses a
  key declared twice and collects the **enabled** modules' into `Deps.AITasks`, so a disabled
  module's tasks vanish with its permissions. A task is declared by the delivery that performs it:
  one nothing calls is a row on a page that does nothing.
- **`Module.AIModels`** is the single-provider slot for the choice of model per task. Identity
  fills it: its table `identity.ai_task_models`, read through a short cache, resolved as the
  Owner's choice if the plan still allows it, else the task's `Default` if allowed, else the first
  of `AI_MODELS`. `Compose` resolves it after every other slot, with `Deps.AITasks` already set,
  refuses two providers, and hands it to the client as its resolver. `module.Workers` resolves
  neither the tasks nor the client: no worker performs a task yet. A test pins the choice with
  `modtest.WithAIModels`, which replaces identity's provider in the composition the way
  `WithAccounts` replaces its accounts.

`internal/ai` and `internal/operations` are on depguard's `platform` list, so they can import no business module.

## The model loop

`internal/agentloop` is the model loop a module runs a conversation with, extracted from the
Assistant so that a second caller does not copy it. It is a platform package: it is on
depguard's `platform` list, imports no business module and knows no table. It holds the turns and
the tool calls with their caps (`Caps`: tool calls, turns, ceiling), the final turn without
tools, the Anthropic branch that keeps the history append-only, the compaction of what the
model is sent (`CompactBody`, `FitContext` and the summary request), `Wrap`, which hands every tool
result to the model as data and not as instructions, and the three dispatcher-backed tools
`search_operations`, `describe_operation` and `call_operation` (`Dispatch`), which reach the
contract's operations through an interface that `*operations.Dispatcher` satisfies.

What differs from one module to another is behind four seams the caller fills, and stays in
the module: its tables (`Store` appends and reads the transcript and the run's progress), its
stream (`Emit` hears the deltas and the tool events; a module without one passes nil), its own
tools (`Tool`, whose `Call` runs them), and what a change becomes (`Dispatch.Confirm`: the
Assistant turns a `confirm` operation into a pending action for the person, and a call without
a `Confirm` is refused). The grants, the triggers and the budget stay with the module as well.
The Assistant is the first caller ([The model loop](/en/reference/assistant/#the-model-loop)).

## How they are enforced

- **Rules 1–3**: [depguard](https://github.com/OpenPeeDeeB/depguard) in
  [`apps/server/.golangci.yml`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/.golangci.yml), run by
  `mise run server:check` and in CI. There is one rule set per boundary, scoped to
  its directory, and each forbidden module gets two deny entries — an exact,
  `$`-anchored match so it can never prefix-match a sibling, and a `…/<mod>/` match
  covering every subpackage including generated ones.
  **`apps/server/internal/integration` is the second place, after `cmd/vantigo`,
  that may import several business modules at once** — and the second deliberate
  exception in `.golangci.yml`, after `internal/modtest`. It is absent from every
  `files:` scope there — which is what lets one test import `projects`, `time` and
  `expenses` together — because it is the one place several *real* modules are
  composed, so that a cross-module promise ("two modules reading the same data
  report the same figures") is pinned against the other side itself rather than
  against a hand-written imitation of it. The exemption holds only while the
  package stays **test-only**: it has no production code and nothing imports it,
  so it buys a test and costs no coupling. Anything non-test added there needs a
  depguard rule of its own; see its `doc.go`.
  A rule set of a different kind, `test-fakes`, covers every non-test file and
  refuses a provider's fake — `internal/payments/vipps/vippstest` — outside
  `_test.go` files, so a fake can serve every package's tests and never ship.
- **Rule 4**: `apps/server/internal/db/schema_test.go` scans every migration and
  query file and fails when one module's SQL names another module's schema.
- **Rule 5**: `module.Compose` itself. It fails on a duplicate module name, an
  invalid or duplicate permission key, a `Mount` error, a path two modules both
  declare, a component two modules declare differently under the same name, or two
  modules both declaring the same provider — a customer directory, a user directory,
  a product catalog, a project directory, project actuals, project expenses,
  billable hours, billable expenses, billable milestones or consumption — naming both, or a
  source kind two invoiced-work holders both claim (`module: two modules both stamp
  "time.entry": a, b`), naming the kind and both — a holder a caller preset on
  `Deps.InvoicedWork` (a test harness's fake) claims its kinds first and is named
  `Deps.InvoicedWork`, and a nil one is dropped from the slot, which never hands an
  issue a nil holder. `module.Workers`, which has no error to return, panics on the
  same kind claimed twice, and on two enabled modules both declaring a customer
  directory.
- **Rule 7**: `no-restricted-imports` in each module frontend's `eslint.config.js`,
  run by `bun run frontend:lint` locally and in CI.
- **Rule 8**: by shape and by test. `RepointCustomer` is handed the caller's
  `pgx.Tx`; its constructor sees `Deps`, and each holder's rollback test fails if
  it writes through anything but the transaction. Its SQL lives in its own
  `queries/`, so rule 4's scan covers it; each holder's own package test
  re-points real rows through a real transaction, and the customers module's
  merge tests prove that a holder's error rolls the whole merge back.
- **Rule 9**: the same way. `EraseCustomerData` is handed the caller's `pgx.Tx`
  and writes through it alone (communications' implementation holds the pool, for
  its export's own read-only snapshot); its SQL is in its own `queries/`, so rule
  4's scan covers it; each module's package test erases real rows through a real
  transaction it rolls back first, and the customers module's anonymisation tests
  prove a module's error rolls that customer's whole anonymisation back.
- **Rule 10**: by shape and by test, as rules 8 and 9. `MarkInvoiced` and
  `ReleaseInvoiced` are handed the caller's `pgx.Tx`; their SQL is in each module's
  `queries/`, so rule 4's scan covers it; depguard keeps Invoices and the three
  source modules apart, tests included. Each holder's package test builds it from
  `Deps` with `Pool` nil — so it can only write through the transaction — and stamps
  and releases real rows through one it rolls back first; each holder marks `ctx`
  with its own module's locked-transaction flag, so that module's own contract-call
  hook catches a directory read inside it. `module.Compose` and `module.Workers`
  collect the slot from every module given, and both put the invoiced-work
  providers' `CustomerReferences` and `CustomerPersonalData` after every other
  module's — `internal/module`'s tests walk the composed slots as a merge and an
  anonymisation do and pin that order. On Invoices' side the holder's command goes
  through one accessor reporting `InvoicedWork.<kind>.Mark|Release` by
  `noteTxCommand`, beside `noteContractCall`; the tests' contract-call hook records
  both with a `txBound` flag, and every invoices harness fails its test on a call
  out of the module made inside any of the module's transactions — under a lock,
  or in a read-only snapshot (`withReadTx`), which holds a pool connection a
  directory's own read could starve for — **and** on a transaction-bound command
  made outside a lock. The tests run with `modtest.WithInvoicedWork` fakes that read
  `pg_current_xact_id()` through the `pgx.Tx` they are handed; the issue's
  `issueAfterAllocation(ctx, tx, invoiceID)` hook reads it through the issue's own,
  and the two must be equal — the holder rode the issue's transaction. The races are
  the integration package's to prove against the real modules
  (`work_races_test.go`), on an installation whose pool is `MaxConns = 2`
  (`modtest.WithPoolMaxConns(2)`) serving only the two racing writers, so any call
  that takes a second pool connection under a lock starves and fails the test. Each
  race is held at a lock: a **raw transaction on a connection of its own**, opened
  with `pgx.Connect` outside the pool, locks the contested row — the expense line, the
  project row, the time entry, or the number counter; the first writer is started and
  seen waiting on it, then the second, seen waiting behind the first; the raw
  transaction commits, and Postgres hands the row on. **Who waits on whom is read with
  `pg_blocking_pids`**, and **a row's lock is proved with a `NOWAIT` probe** — `FOR NO
  KEY UPDATE NOWAIT` on the project, `FOR UPDATE NOWAIT` on the document; SQLSTATE 55P03
  when another transaction holds it — each on a connection of its own; no `pg_locks` read stands as proof that a row is or is not
  locked. After each race the database's **`pg_stat_database.deadlocks`** must still be
  0, so a deadlock a loser retried away — the customers merge retries 40P01 — is caught
  too. An issue races each writer of the same rows — an expense's manual mark and a
  batch reimbursement, a milestone move and a fixed-price project's price edit, a time
  unapprove, a customers merge — both must finish within their deadline, and the
  loser's refusal is the named one ([the races](/en/reference/invoices/#the-races)).
- **Rule 12**: by test. Each calendar source's package test, through its own module's
  harness, proves it answers the caller's rows with its gate held and nothing without
  it; the calendar's own tests run fake sources per kind, prove a failing one fails
  `GET /items`, and `internal/module`'s tests pin that the slot is collected from
  enabled modules only.
- **Rule 15**: by test. Communications' tests run against `modtest.FakeChannelMailbox`
  (its mailboxes, status and refusals set by the test, every call recorded), set by
  `modtest.WithChannelMailbox`; the contract-call hook in `contractscalls.go` records
  whether each slot call was made inside one of the module's transactions, and the tests
  assert none was. Entra's `channel_mailbox_test.go` proves `Send` refuses a mailbox no
  `Check` recorded without a call, and `TestCompose_CollectsTheMailboxSlots` (with
  `TestWorkers_CollectTheMailboxSlots`) that two providers under two keys are both kept
  while a key two claim, or a reserved one, is refused. Communications' tests run two
  fakes under two keys, so a channel is proven served by its own provider only. The
  messenger slot is held to the same tests (`TestCompose_CollectsTheMailboxSlots` runs it
  too, and `TestCompose_RefusesAKeyInBothChannelSlots` pins the cross-slot refusal), and
  Communications' Messenger tests run a fake `ChannelMessenger` whose Profile, Send and
  OpenAttachment calls the same hook proves were never made inside a transaction.
- **Rule 18**: by depguard and by test. depguard forbids every import of `internal/operations`
  but `internal/assistant`, `internal/agents`, `internal/agentloop`, `internal/module`,
  `internal/server` and the test harnesses; a `module` test composes modules called `alpha`,
  `assistant` and `agents` and proves which are handed the dispatcher (the last two, the same
  one), and another that runners start after the dispatcher is bound and stop on cancel; the
  dispatcher's own tests bind it to a small synthetic contract and prove each mode, one gate
  per kind (an assistant with no gate falls back to the closed default, an agent with none may call nothing, and one kind's gate never
  answers for the other), a call under an agent grant, the customer scope (a parameter filled
  or refused, an answer of another customer dropped, a run with no customer refused) and that a
  call carries no value of the caller's; and
  `TestOperations_TheRealContract` derives the contract's mode and the default gate's mode of every
  operation of the shipped contract again from the catalog, pinning the hand-written marks. The
  assistant module's tests run every level, the switch and its overrides through the real
  dispatcher and the real router.
- **Rule 19**: by test. The Assistant's erasure test seeds conversations, runs and a switch for a
  user identity does not know, runs the module's retention cycle, and proves they are gone while a
  living user's and a disabled user's are not.
- **Rule 20**: by shape and by test, as rule 8. `VariantHeld` is handed the caller's
  `pgx.Tx` and its builder sees only `Deps`; its SQL is in the holder's own `queries/`,
  so rule 4's scan covers it. Products' tests ask a fake holder set by
  `modtest.WithVariantReferenceHolders` and prove the delete and the Goods→Service change
  are refused while it holds, and pass once it does not, that the last-variant 409 comes
  before any holder is asked, that a type change keeping or gaining Goods never asks, that
  the holder is told the purpose, and that an `open_documents` hold refuses with its own
  words (`variant_holds_test.go`).
- **Rule 21**: by test. Products' `TestCatalog_LockVariants_AnswersAndHoldsOnTheCallersTransaction`
  proves the share lock is taken on the transaction it is handed and held until that
  transaction ends; `TestDeleteVariant_WaitsForAPostingHoldingTheVariant` holds it in a
  transaction of its own and proves a delete waits behind it and then sees what the
  posting left. Inventory's contract-call hook (`SetContractCallHook`, installed once in
  `main_test.go`) records every call out of the module made inside one of its
  transactions, and the harness fails the test on any but `Products.LockVariants`.
- **Rule 23**: by shape and by test. `events.Handler.Handle` is handed the delivery's
  `pgx.Tx` and never a pool, and `events.Publish` only the publisher's; `internal/events`
  is a platform package and imports no module (rule 2, the depguard island). Its tests
  prove a publish commits and rolls back with its caller's transaction, a handler's
  error rolls its delivery back, the per-(handler, aggregate) order and the block behind
  a failed or dead delivery, the backoff steps and the death on the eighth failure, a
  skip that unblocks, the prune, two dispatchers never handling one delivery twice, and
  a cycle on a pool of one (`events_test.go`, `dispatch_test.go`);
  `internal/module/events_test.go` proves the slot is collected from disabled modules
  too and that a duplicate name is refused by `EventHandlers` and by `Compose`.
- **The harness**, `apps/server/internal/modtest`, is what these tests mount their
  modules through: an in-process installation on a real, migrated PostgreSQL database,
  the real server stack over `module.Compose` with identity beside the module under
  test, a settable clock, and a client whose every exchange is validated against the
  module's contract. A module's collaborators are composed for real or replaced by
  contract fakes through its `With*` options — `WithProjects`, `WithBillableHours`,
  `WithInvoicedWork` and the rest — and `WithPoolMaxConns` sets the size of the
  installation's database pool, which the races above hold at two. The calendar's
  mailbox sync and subscriptions workers are held at four with three taken by the test,
  and the last must stay free while a sync waits on the provider (the mailbox fake's
  `BeforeStatus` holds it there) or a fetch runs: **no worker holds a pooled connection
  while it asks the pool for another or calls out**, which on pgxpool's default — four,
  or one a CPU on a larger machine — starved every worker and the readiness probe. A
  worker under an advisory lease runs its cycle on the connection the lease holds, and
  calls another module only before the lease is taken or after it is released. The lease
  workers that broke that rule once — customers' registry feed, Peppol re-check and
  anonymisation, communications' retention, energy's import, invoices' EHF events and
  staff's accounts — each have a test that runs a cycle on a pool of one, where a reach
  for a second connection waits to the test's deadline, and `cmd/vantigo`'s
  `TestServe_APIIsReadyOnAPoolOfFourWithEveryWorker` boots the api with every worker it
  runs by default on a pool of four and asks for readiness while their first cycles
  run. Identity is always
  composed and always provides `Accounts`, and Compose overwrites a preset
  `Deps.Accounts`, so `WithAccounts(p)` replaces identity's `Module.Accounts` in the
  composition — `p` is what every module's `Deps.Accounts` resolves to — and
  `WithoutAccounts()` drops it, for the "accounts are unavailable" path; a test of
  identity's real provider uses neither. Users is identity's own in every harness —
  a test shapes what the directory answers through identity's tables — but
  `WithUsers(wrap)` wraps identity's `Module.Users` in the composition, for the one
  answer those tables cannot give: a lookup that fails (Staff's holders read with the
  directory down). `Harness.Workers(d)` builds the composed
  modules' background workers from `d` as worker mode does (`module.Workers`, which
  resolves `Users` and `Accounts` onto them — `Deps()` alone carries neither), so a
  worker test runs the worker production starts, on the harness's `Deps()` or on a pool
  of its own.

## Turning a module off

`MODULES` is a **positive allowlist** — a comma-separated list of the business
modules a deployment serves, parsed once at startup:

```text
MODULES=customers,products
```

Unset enables every module this binary can mount
(`customers,products,energy,communications,projects,time,expenses,invoices,staff,planning,inventory,calendar,entra,google,meta,assistant`). Identity is always mounted and
is never listed. Entries are trimmed and lower-cased, empty entries are ignored, and a
name the binary does not know fails startup naming both the value and the known set.

A disabled module contributes no route, no permission, no contract path and no
background worker; its paths fall through to the `/api` catch-all and answer the 404
problem. The decision is taken once, before anything mounts, so a module can never
end up with endpoints mapped whose permissions were never contributed.

**Every schema is migrated regardless of what `MODULES` enables**, so enabling a
module later needs no migration. For the same reason a disabled module still
contributes its `CustomerReferenceHolder` (rule 8): a module that was on once and is
off now still has rows naming customers, and a merge that skipped them would leave them
on the absorbed customer for good. Its `VariantReferenceHolder` (rule 20) is collected
the same way: stock in a switched-off Inventory's schema is still stock, and a variant
deleted under it would leave balances naming nothing. A holder needs only the caller's transaction and its
own schema, both there whatever `MODULES` says. Its `CustomerPersonalData` (rule 9) is
collected the same way and for the same reason: a switched-off module still holds what
it held about a person, and an anonymisation must still take it out. Its event handlers
(rule 23) are composed the same way: an event already published still has to reach
the module that takes it, and a handler that needs its module on checks the setting
itself and does nothing.

Some modules cannot be hosted alone. Energy, Communications and Projects read
customer data through `contracts.CustomerDirectory`, which only Customers implements,
so enabling any of them without `customers` is rejected at startup with a message
naming both (`projects requires customers`). Time hangs its hours off projects and
reads them through `contracts.ProjectDirectory`, so `time` without `projects` is
rejected the same way (`time requires projects`). Entra reads of Staff only the
caller's own directory account, for the Outlook connection, and is
called only by Staff's accounts worker, the slot's one consumer, so `entra` without
`staff` would hold a tenant's credential nothing uses and is rejected too (`entra
requires staff`). Google is rejected without `staff` for the same reason (`google
requires staff`): the accounts it will keep are Staff's decisions, made by its accounts
worker. Meta hands Facebook Messenger to Communications' channels and is useless without
them, so `meta` without `communications` is rejected (`meta requires communications`).
Planning books Staff's people on Projects' projects, so `planning`
requires both `projects` and `staff` (`planning requires projects`, `planning requires
staff`). Inventory keeps stock of the catalog's variants and reads and locks them
through `contracts.ProductCatalog`, so `inventory` without `products` is rejected
(`inventory requires products`), and its `Mount` refuses a nil catalog besides. Assistant requires nobody: it reaches the other modules only through the dispatcher, which
lists whichever are enabled, and it is of use only while `AI_PROVIDER` is set. A module that starts requiring
another module's contract adds its own check in `internal/config`.

A contract can also be **optional**. `contracts.ProductCatalog` is the first:
Projects prices its billing lines through it, but when `products` is disabled
`Deps.Products` is simply nil and Projects answers 409 on its billing-line operations
instead of failing startup. That is the shape to copy — a required dependency gets a
config check, an optional one leaves the `Deps` field nil and **the consumer must
handle nil**. The two exceptions are `contracts.UserDirectory` and
`contracts.BusinessCalendar`: identity is always mounted, so `Deps.Users` and
`Deps.Calendar` are always set once composed — and, since `module.Workers` resolves
them as well, in worker mode too. A module reads the calendar **before** it opens a
transaction or takes a connection of its own, never while holding either: the read
takes a pool connection, which a transaction holding locks — or a worker holding its
advisory lease's connection — must not wait for (rule 7). A worker that did would
wait for itself on a pool of one, and wedged the image smoke test's pool of four at
startup beside the other workers' first cycles. `modtest.SetBusinessTimeZone` sets
the zone for a test whose subject is another zone, and `modtest.DepsOn(pool)` is
`Deps` on a pool of a test's own with the calendar built on that pool, for the test
that starves a worker to prove it never asks for a second connection.

`UserDirectory.PermissionHolders(key)` names the **active users holding a
permission**, ordered by display name, in one query of identity's: a user holds the
key through a role they hold directly, through a role mapped to an active access group
they are effectively a member of (forced in, or present upstream and not overridden
out — exactly the rule a permission check applies), or as an Owner, who holds every
key; a delegation grants authorization management only, never a key. Active means
what a session needs: not disabled, not pending (a Staff-managed user who has not yet
chosen a credential), and, while SCIM is configured, not deactivated by the directory
unless an Owner; a lockout is temporary and ignored. Each `contracts.Holder` carries
the user's address for **mailing that holder and nothing else**: a consumer never puts
it on its own wire, in a log, an event or an error, and a directory's or identity
provider's placeholder address, which no mail reaches, is answered empty. That one
address stays for Staff's one use; **no other method hands an address over**.

`UserDirectory.SearchUsers` and `PermissionHolders` **leave out users of kind `agent`**
unless the call passes `contracts.IncludeAgents()`, and each `UserEntry` and `Holder`
carries its `Kind` (`contracts.UserKindPerson`, `UserKindAgent`) for the AI badge. The
default is false for every caller: a customer's owner, calendar, projects, a rate card
and Staff never meet an agent; Communications' assignee select asks for them. A fake of
the interface in a test takes the trailing `...contracts.DirectoryOption`.

`UserDirectory.Avatar(userID)` reads a user's **effective avatar** — the managed one
while it stands, else the user's own, `nil` when there is none — with its type, its
bytes, `ManagedBy` (`staff` or empty), `UpdatedAt` and a `Stamp`, `managed:<stamp>` or
`own:<version>@<epoch>`, by which a caller tells whether the picture changed without
comparing bytes; `UserDirectory.AvatarInfo(userID)` answers the same without the bytes,
for a caller that only compares or shows when it changed. Staff is its consumer: it shows the user's own avatar on the person's page when
HR has set no photo, and pushes the effective picture to the provider's directory. A
consumer streams the bytes to a caller already allowed the picture and never logs them.
**The picture flows one way** — Staff's photo → identity's managed avatar → the
provider's directory — and is never read back from the directory.

**Mail them, do not hand the value over.** A module that must mail a colleague calls
`UserDirectory.Notify(ctx, userID, contracts.Notice)`: the module composes the mail in
the languages it has (`Notice.Texts`, keyed `en` and `nb`, each a `Subject` and a plain
`Body`), and identity delivers it in the user's own language — their preferred one, then
`en`, then any — at the address identity holds, from `SMTP_FROM` through the
installation's mail driver. The address never leaves identity. A user who is not active
in `PermissionHolders`' sense, an unknown one, an agent (whose address never routes),
one whose only address is a placeholder, and an installation with no mail driver are not mailed and are not errors; a send that
fails is an error naming the user's id and no address. Calendar is the first consumer: it
tells the colleagues on an appointment, and its owner when an outside attendee answers
([what a colleague receives](/en/reference/calendar/#what-a-colleague-receives)). The
non-sensitive facts a module needs to write *for* a user stay on `contracts.UserEntry`:
`PreferredLanguage` (`en`, `nb`, or empty when they chose none) is there so Calendar can
write an outside invitation in its organiser's language.

The customer directory follows the same line for a contact, with one difference: a
contact's address is Customers' to guard, behind its sensitive `customers:contacts-view`,
so `CustomerDirectory.Contact` and `CustomerDirectory.ContactsForCustomer` do answer it —
the latter the contacts of one customer that have an address
([`ContactsForCustomer`](/en/reference/customers/#contactsforcustomer--a-customers-contacts))
— and the consumer that shows it requires Customers' keys first and never copies it:
Calendar reads a contact attendee's address each time it mails them and puts it on no
appointment.

**Identity owns the state of the users Staff manages.** `identity.users.managed_by`
(`staff` or null), `employee_number`, `pending_since` and `disabled_by`,
`identity.invitations.user_id` and `revoked_reason`, the recorded
`system_settings.authority_mode` and the set of roles Staff may give
(`identity.provisioning_roles`) are identity's columns, in identity's migration
(`00050_identity_accounts.sql`), written by identity's own handlers — an Owner's
adoption, unmanage, disable and employee-role endpoints — and read by nobody else's
SQL. Staff reaches them only through a contract identity provides, never through its
own queries — `contracts.AccountProvisioning` (rule 11) — and Staff's register keeps
only the opaque user id of the link. In
identity's Go code these are `staffManaged` names: "managed" there already means an
Owner's User/Owner role management (`isManagedRole`, `ListManagedUsers`).

`contracts.ProjectActuals` is the second optional contract, and the first to run in
the *opposite* direction from the module that needs it: **Time provides it, Projects
optionally consumes it.** Time's own dependency on Projects is required — it hangs
hours off `contracts.ProjectDirectory` and cannot start without it (`time requires
projects`, below) — but Projects' economy view does not require Time back: with
`time` disabled `Deps.Actuals` is nil, Projects answers `timeTracking: false` and
shows budgets with nothing to compare them against, and nothing fails startup. Both
directions are resolved by `Compose` before any module mounts, so there is no runtime
call in either direction that could cycle — `internal/projects` still imports no
other module (depguard) and no SQL of either module crosses into the other's schema.

`contracts.ProjectExpenses` is the third optional contract and the mirror of the
second: **Expenses provides it, Projects optionally consumes it** — what a
project's expenses cost and bill, beside the hours Time already reports, and, per
currency, the part of it that is supplier invoices (`CurrencyExpenses.SupplierInvoices`,
an `ExpenseSplit` of the same buckets, nil when there are none: a sub-figure, never a
split, so every existing figure still means everything). It is the
first contract whose provider requires *nothing* in return: Expenses depends on
nobody but identity, so a `MODULES=expenses` installation provides a contract
nothing consumes, and a `MODULES=customers,projects,time` one consumes nothing of
it. Where `ProjectActuals` takes the project's currency in on the request, this one reports
**per currency** and never converts: an expense carries its own currency per line,
and a provider that folded them into the project's would have had to ask the
project directory while serving — the module cycle at request time both contracts
are shaped to avoid. A consumer with `Deps.Expenses` nil must say "expense
tracking is off" the way it says `timeTracking: false` today — never zeroes
standing in for "not enabled".

A consumed contract can also narrow **who** may reach the data it exposes, not only
which module can. Projects' economy reads (`GET /{id}/economy`, the portfolio, the
dashboard's budget alerts) sit behind Projects' own `projects:access` and, for
amounts, its own financial-rights rule — never behind `time:access` — because the
contract hands the caller's currency and rights decision to Time and gets back only
what those already allow: **aggregate hours and amounts per project and per billing
line, never per person or per entry.** That is deliberately less than Time's own
project summary already gives a project member (which is per-status, per-line
**and** per-person), so a caller who could never open Time tracking at all still
gets a strict subset of it through Projects. Cost and margin sit behind a second,
Projects-owned gate on top of that (`projects:view-costs`, sensitive, granted to no
default role) — a permission the consuming module defines and enforces itself,
which a provider contract cannot see or grant on its behalf. See
[`docs/projects.md`](/en/reference/projects/#project-economy) for what each shaping level
returns.

Time consumes four contracts and provides three, and fills one many-provider slot:
`contracts.ProjectDirectory` (required — hence the config check — including
`WorkType`, the project's work type an entry picked, whose multipliers the rate
chain's last step applies and which is read before a save's transaction opens like
every other directory read; `WorkTypes` lists a project's types for a consumer that
offers a choice),
`contracts.UserDirectory` (always there, for names and for the rate card's user
search), `contracts.ProductCatalog` (optional — with products disabled a `list` or
`discount` billing line simply has no price, and the rate chain falls through to the
project default), `contracts.CustomerDirectory` (required transitively — projects
requires customers — for the rate chain's customer step, which reads a customer's
default bill rate from `BillingProfile`; the chain still checks it for nil, a
defensive floor rather than a mode) — and it provides
`contracts.ProjectActuals` (optional for its
consumer, above), reading its own `time` tables and never calling back into
`contracts.ProjectDirectory` to serve it: the currency an amount is measured in
arrives in the request, because Projects is the module that owns that fact. Beside
that aggregate it provides `contracts.BillableHours` — the approved, billable,
priced entries not yet invoiced, row by row, read on the pool and never under a
lock — and `contracts.Timesheets`, a user's hours logged and approved in a week, for
Staff's hours card at a glance: consumer-gated as `ProjectActuals` is, read on the pool
from its own `time.entries` alone, nil when `time` is disabled — and it fills
`InvoicedWork` with the holder that stamps an entry invoiced
inside an invoice's issue and releases it in a credit note's (rule 10). It also
keeps the rule every module keeps (rule 10's restatement): **no call that takes its
own connection or leaves the process while a transaction holds locks**, enforced by
fakes that record any call made under one — the economy reads on both sides take no
lock at all.

**Expenses depends on nobody but identity.** Unlike every other business module,
it needs no config-checked dependency at all: `MODULES=expenses` alone is a valid
installation, and so is `MODULES=customers,expenses`. It fills two provider slots —
`Expenses` (`contracts.ProjectExpenses`, above), served from its own tables by
`internal/expenses/projectexpenses.go`, which takes the pool and nothing else and
asks the project directory nothing while it serves, and beside it
`BillableExpenses` (`contracts.BillableExpenses`, the lines ready to invoice and not
yet invoiced, row by row, read on the pool and never under a lock) — and it fills
`InvoicedWork` with the holder that stamps a line invoiced inside an invoice's issue
and releases it in a credit note's (rule 10). None of that is a dependency either
way: an installation with no `projects` or no `invoices` provides them to nobody, and
one without `expenses` leaves the consumers' slots nil. `contracts.ProjectDirectory` is
read, but purely *optionally* — `Deps.Projects` is nil when `projects` is not
enabled, `GET /meta` answers `projectsAvailable: false`, and every project-shaped
request field (a project id, a billing line, `billable`, a markup, a customer rate
per kilometre) is refused on its own field rather than silently accepted. A stored
project id an expense already carries is not cleared when the module is switched
off — the column is data, not a fact this module owns the right to delete — but
nothing can be judged against a project nobody can ask about any more, so a save on
such a row carries those six columns through untouched instead of refusing the
edit. See [Expenses](/en/reference/expenses/) for the model, and for what changes with and
without Projects.

**Invoices requires customers.** `MODULES` refuses `invoices` without `customers`
(`internal/config`): the buyer, its billing profile and its invoice address come
from `contracts.CustomerDirectory.BillingProfile`, read before any issue or save
takes a lock and never under one — as is a send's recipient, the customer's current
invoice e-mail. A reminder run reads each item's customer's billing profile the same
way — `ReminderEmail` and `ReminderDelivery`, where its letters go — once per customer
on the pool before any item's lock, and the collection export reads the customers'
reminder addresses for the agency's file, on the pool, a failed read leaving the
address blank and logged; the reminder worker reads no directory, the run having fixed
each letter's channel and recipient. Beside it, and each optionally, it reads the project directory
(`contracts.ProjectDirectory`: the projects a draft's work belongs to, before an
issue's transaction; with `projects` disabled a draft that bills work cannot be
issued, `projects_unavailable`), the user directory (the issuer's name a holder's
timeline event carries) and the three billable reads (`BillableHours`,
`BillableExpenses`, `BillableMilestones`: whether a draft's work is still billable as
it was taken, on `GET` and before a refresh — a kind whose module is off is not
judged), and Orders' `BillableOrders` (the order lines due, and the allocations its
From orders wizard has Orders write and release with the provider commands
`Allocate` and `ReleaseAllocations` — every allocation recorded in its own
`order_allocations_taken` ledger, which its `invoices.allocation_reconcile` worker
reconciles), all on the pool and never under a lock; products
it does not read. Inside an issue's transaction it calls the invoiced-work holders —
time, expenses, projects, energy and orders, in `contracts.InvoicedWorkOrder` (rule
10) — the one call it makes under a lock. It sends mail
only through the platform's SMTP seam, never under a lock, and provides two
single-provider contracts, `Receivables` and `SalesTax` — its VAT codes, the VAT code
each of Products' tax categories maps to, and its seller block, each read from its own
tables on the pool ([What Invoices reports to other modules](/en/reference/invoices/#what-invoices-reports-to-other-modules)). It fills both many-provider slots: as a `CustomerReferenceHolder` it re-points every document of a
merged-away customer, drafts and issued alike (`invoices.invoices`), the immutability
trigger allowing exactly `customer_id` to change on an issued one; as
`CustomerPersonalData` it exports a person's documents and drafts with their payments,
deliveries and EHF transmissions — and an issued document's charge payments, waivers,
manual deliveries, letters, holds and hand-offs, each payment's source and an imported
one's bank line — and, on anonymisation, locks the person's documents,
writes a module-private marker that refuses every later send, blanks every delivery's
recipient (`invoices.deliveries`), blanks every payment's note (`invoices.payments`,
the notes blanked), cancels every queued transmission never attempted
(`invoices.transmissions`), deletes the drafts (`invoices.drafts`); withdraws the
letters in flight and blanks every letter's address (`invoices.reminders`), blanks the
notes of the receivables (`invoices.chargePayments`, `invoices.chargeWaivers`,
`invoices.manualDeliveries`, `invoices.invoiceHolds`, `invoices.collectionHandoffs`)
and of the resolved bank lines their money came from (`invoices.bankTransactions` —
the lock order's one named exception, the [reference](/en/reference/invoices/#issuing)
says why it cannot cycle), revokes every live pay link of the person's documents
(`invoices.payLinks`) and leaves every Vipps payment attempt as it is, deletes the reminder policy
(`invoices.customerReminderPolicies`), and keeps the issued documents, their payments,
letters and every other transmission under bokføringsloven § 13
(`invoices.documents`, at 0). Every write runs on the customers module's transaction
it is handed and makes no call out of it.
For those gates `contracts.CustomerBillingProfile` carries the customer's `Status`
(`active`, `disabled`, `archived`) and `MergedInto`, the one change to the
customers contract Invoices made: `disabled` is "blocked for invoicing", and an
archived or merged-away customer still resolves, so a past invoice can be shown and
credited. The customer page's Invoices tab is host-owned composition, as its Projects
tab is: the host's customer-detail layout registers the tab, gated on the enabled module
and `invoices:access`, and renders `CustomerInvoicesPanel` from `@vantigo/invoices-ui`,
which reads only the invoices API (`GET /invoices?customerId=`); the customers package
and the invoices package never import each other (rule 7). See [Invoices](/en/reference/invoices/).

**Staff depends on identity, and optionally on Time through `contracts.Timesheets`.**
`MODULES=staff` alone is a valid installation: with `time` disabled `Deps.Timesheets` is
nil, the hours card on a person's glance answers `{timeTracking: false}` — "time
tracking is off", never zero — and nothing else changes; with it on, Staff asks Time for
the week's hours of the one user a person's self-service link names, after deciding by
its own rule (the person, their manager today, `staff:manage`, `staff:personal-data`)
that the caller may see them — the contract is consumer-gated (after rule 12) — through
its accessor `timesheetsWeek`, with the request's context, before the glance's snapshot
opens. Beyond that no other module's state changes what Staff answers. It reaches
three further things outside its schema: identity's `Deps.Users` (`contracts.UserDirectory`,
always set: `User`, `Users`, `SearchUsers`) for names — whoever writes a history event,
the user a self-service link names — and the picker HR links a person with; the
platform's mail seam (`Deps.SMTPSend`, or `mail.SendOutbound` when unset) for the mails
it sends — the notice of a changed bank account, and the absence notices (a request or
a registration to the person's managers, none for an HR-only type; a decision, a
decider's cancel or a correction to the person) —
each only to an address in its own register, a person's work or private email, never
one the user directory would have to name; and the object store
(`Deps.ObjectStore` when a harness sets one, else its own `staff`-scoped store built
from the configuration, in the API and a worker alike, with a construction error kept
for the calls that need a store rather than failing the process) for the bytes of
people's documents and photos. It counts the working days an absence takes by the platform
calendar (`internal/holidays`), never a holiday list of its own. It reads no business module but Time's hours, optionally. It provides one contract,
`contracts.Workforce` — the catalogue, the competences people hold, valid on a date, the
people in employment and their capacity over a window, the caller's own person, and the
fit of a booking against what a person wants, for Planning, its consumer — as that slot's sole provider, and fills
the many-provider `CalendarSources` slot with the caller's own absences; with Staff
switched off `Deps.Workforce` is nil, which a consumer must handle, and nothing else in
another module is stranded — but it does strand its
own objects and the rows of its ledger of objects still to remove
(`staff.object_deletions`), which wait untouched until the module is on again. It keeps
rule 10's restatement strictly: all three, and Time's hours, are reached **before a transaction opens or
after it commits, never inside one** — neither under a person's lock nor inside a
read-only snapshot, which holds a pool connection just the same. Each goes through one
accessor — in `internal/staff/contractscalls.go`, or `internal/staff/objects.go` for the
store (`objectPut`, `objectGet`, `objectDelete`) — that refuses a call made inside one of
the module's transactions (the marking Invoices introduced, copied rather than
imported), and the harness records every call and fails a test that makes one under an
open transaction. So a document's object is written before its row's transaction and
removed after a refusal, read after the row's snapshot, and removed after the delete
commits. Its background workers are contributed through `Workers` — `staff-retention`
behind `STAFF_RETENTION_ENABLED`, `staff-accounts` behind `STAFF_ACCOUNTS_ENABLED` and
`staff-competence` behind `STAFF_COMPETENCE_ENABLED`, which stamps each row in a short
transaction of its own and mails only after it has closed — and the retention worker keeps two connection disciplines: the erase runs on
the advisory lease's own connection — the due queries and every person's transaction,
the person's lock included, never asking the pool for a second, which is how a lease
holder deadlocked the customers worker with a pool of four (PR #124) — and the ledger
pass over `staff.object_deletions` takes no lease and holds no connection across
`objectDelete`. See [Staff](/en/reference/staff/#locks).

**Planning depends on Projects and Staff.** It is the first module to consume two
business modules at once: it reads `contracts.ProjectDirectory` for the projects a
person is booked on — `ProjectsByStatus`, consumer-gated, for the plannable list —
`contracts.Workforce` for the people in employment, their capacity, the caller's own
person, the competence catalogue and the competences held, and the fit Staff scores for
a request's booking, optionally `contracts.CalendarBusy` for the busy hours and days its
board shows (provider-gated; nil with `calendar` off, and the board shows none), and
`contracts.CustomerDirectory` for the customers' names — a
project's, and those an assignment's non-compete names, each id checked on the terms'
write; identity's `Deps.Users` gives it the actors' names, `PermissionHolders` for the
planners a new wish is mailed to, and `Notify`, its mail: to the person whose row was
confirmed, changed or deleted, and to the planners of a wish. `MODULES` refuses it
without `projects` or `staff`, so `Deps.Projects`, `Deps.Workforce` and `Deps.Directory`
are never nil in a running `planning`; it still answers `dependency_unavailable` (503)
rather than panic if one is. Every contract call goes through its accessor in
`internal/planning/contractscalls.go`, with the request's context, **before** a
transaction opens, never inside one, and the harness fails a test that makes one under
an open transaction. It provides nothing yet: no other module reads it; the host
composes its Planning tab on Staff's person page through the slot `@vantigo/staff-ui`
opens (rule 7) and its Planning tab on the project page in the host's own tab list, as
Time's and Expenses' tabs are. It fills the many-provider `CalendarSources` slot with
the caller's own confirmed bookings, the kind `planning.allocation`, when `calendar` is
composed — its builder answers nil otherwise, since Planning does not require the
calendar and nothing would read the source. It has no background worker. With `planning` off
nothing else changes. See [Planning](/en/reference/planning/#contracts-consumed).

**Inventory depends on Products.** It consumes `contracts.ProductCatalog` (products,
**required**): `Variants`, `VariantsOfProduct`, `LookupCode`, `LookupSKUs` and `SearchVariants` on
the pool, through its accessors in `internal/inventory/contractscalls.go`, **before** a
transaction opens, and `LockVariants` on the posting's own transaction, first, through
an accessor of its own (rule 21). Identity's `Deps.Users` gives it the poster's name and
`Deps.Calendar` the business time zone its movement filters and its week are judged in,
both before a transaction. It fills the many-provider `VariantReferences` slot (rule 20)
with its balances, so Products refuses to delete a variant, or to turn its product into
a service, while it lies anywhere. It provides no single-provider contract and has no
background worker. The host composes its Stock card on Products' product page through
the `extraPanels` slot `@vantigo/products-ui` opens (rule 7). With `inventory` off
nothing else changes, but its holder still answers for the stock in its schema. See
[Inventory](/en/reference/inventory/).

**Manufacturing depends on Products and Inventory.** It consumes `contracts.ProductCatalog`
(products, **required**) — `SearchVariants` and its variant reads on the pool, before a
transaction opens, and `LockVariants` first on every bill-of-materials and
manufacturing-order write's own transaction, whose answer is where it judges a variant
goods and takes an order line's snapshot (rule 21) — and `contracts.Stock` (inventory,
**required**): `Sites`, `SiteLocations`, `Locations` and `Available` — the site a work
center stands at and an order's locations, where a production's components lie, and
the availability of the BoM overview and the order page — and `Placements` (manufacturing
phase 2 M106), the placements with a reorder point and their usable on hand, reserved
and incoming, which the replenishment job projects from — all on the pool, before or
after a transaction. Its orders call `contracts.StockLedger` (rule 24) on the order
write's own transaction, after `LockVariants` and the order's row: `ReserveForProduction`,
`Release`, `Produce` and `ReservationsFor`. Identity's `Deps.Users`
(`contracts.UserDirectory`) gives it an order's responsible and a production's poster,
and `Deps.Calendar` (`contracts.BusinessCalendar`) the business day "today" and "late"
are judged in, both before a transaction. It declares four live topics through
`Module.LiveTopics` — `manufacturing.orders`, `manufacturing.order:<id>`,
`manufacturing.work-center:<id>` and `manufacturing.work-order:<id>`, the last with
presence — notified on the writing transaction. It fills the many-provider `VariantReferences` slot (rule 20)
with its active bills of materials and its manufacturing orders not done or cancelled,
so Products refuses to delete a variant, or to turn its product into a service, while
one names it as its product or a component. Orders is **optional**: Manufacturing reads
`contracts.SalesOrderLines` (consumer-gated, rule 12) for make-to-order and its
new-order form, and `contracts.CustomerDirectory` for a sales order's customer shown to
a holder of `orders:access`; it provides `contracts.Production` (provider-gated) to
Orders' order page; it handles `orders.sales_order.confirmed` and
`orders.sales_order.cancelled` and publishes `manufacturing.order.done` (rule 23). It
writes nothing outside the `manufacturing` schema, and has two background workers:
`manufacturing-erasure`, the rule-19 sweep that blanks the notes of erased operators' time
logs, `manufacturing.mto-worker`, which makes the orders the make-to-order requests
ask for, holding no connection while it reads a contract, and the lease worker
`manufacturing.replenishment` (phase 2 M109), which reads `Stock.Placements` and
`SalesOrderLines.Unreserved` the same way before it replaces a site's suggestions in one
transaction of its own; it is no handler (rule 23).
`MODULES` refuses it without `products` or `inventory`. See
[Manufacturing](/en/reference/manufacturing/).

**The Assistant reads identity and calls the contract.** It is the one module that receives
`Deps.Operations`, the platform's dispatcher (rule 18): its tools search, describe and call the
operations of the enabled modules as the signed-in person, so it imports none of them and none
knows it exists. From identity it takes `Deps.AssistantGrants` (the grant each run acts under, issued
from the person's own request and revoked when the run ends) and `Deps.Users`
(`contracts.UserDirectory`: the person's language, whether a user exists for the Owner's switch, and
the erasure sweep, rule 19); from the platform `Deps.AI`, whose `Chat` it calls with the AI task
`assistant.chat` (`Module.AITasks`, an Owner's choice under **AI models**), and `Deps.Catalog`, the
permission catalog its Owner-level table lists. It declares **no permissions**: whether a person has
the assistant is the Owner's mode and the person's own switch, and what it may use is the Owner's
permission levels, applied through `SetGate`. It provides no contract, fills no slot and requires no
other module, so `MODULES=assistant` alone is valid; with no AI provider `Deps.AI` is nil and nobody has
the assistant. Its workers are `assistant-runs` and `assistant-retention`. The host mounts its panel in
the app layout; the module frontend never imports another's, and a record's label for the panel's
chip comes from resolvers the host registers (rule 7). See
[Assistant](/en/reference/assistant/).

**Entra reads identity and Staff, and is called by Staff.** It reads of identity only
`Deps.SSOConnections` (`contracts.SSOConnections`, rule 12), for the directory's exact
matches and the holders the search marks, and of Staff only `Deps.Workforce`'s
`DirectoryAccount` (rule 12), the caller's own directory account under the request's
principal, for the person's Outlook connection — both through the accessors in
`internal/entra/contractscalls.go` and never inside a transaction; the Owner's and
`entra:manage`'s checks are the router's — and writes nothing outside the `entra`
schema. It fills six
slots under the key `entra`, `IdentityProvisioner` (rule 13), `MailboxCalendar` (rule 14),
`ChannelMailbox` (rule 15), `PersonalMailbox` (rule 16) and `DirectorySearch` (rule 22):
Staff's accounts worker, the calendar's mailbox sync worker, Communications — its channel
handlers and its outbox — and identity's directory search call it, in the API and in
worker mode alike, never inside a transaction. With `entra` off, no provider answers
`entra` in `Deps.ChannelMailboxes`:
Communications offers SMTP and any other provider, and a Microsoft 365 channel's mail is held as
unavailable, spending no attempt, and failed as `provider_unavailable` 24 hours after it
was queued. Its one worker, `entra-outlook-sweep`, deletes the
closed Outlook connections nothing needs and reads the accounts of those waiting to
reconnect ([Microsoft Entra ID](/en/reference/entra/#entra-outlook-sweep)). It reaches the
network in one place, its Graph client, through `internal/netguard` with no proxy and
no redirect, to two fixed hosts no setting changes; the client's transport and its
token cache are built once per process, and `Deps.HTTPTransport` is the test seam. See [Microsoft Entra
ID](/en/reference/entra/#the-graph-client).

**Google imports nothing.** It writes nothing outside the `google` schema; it fills
`IdentityProvisioner` and `DirectorySearch` under the key `google`, reads identity's
`SSOConnections` for the directory search and Staff's `Workforce.DirectoryAccount` for a
person's own connection, never inside one of its transactions; and its one worker,
`google-sweep`, claims its rows in one statement each and holds no pooled connection
across a call to Google. The Owner's and `google:manage`'s checks are the router's. It reaches the network in one place, its
guarded Google client, through `internal/netguard` with no proxy and no redirect, to
fixed Google hosts no setting changes; the token exchange goes through the same
client, the transport and the token cache are built once per process, and
`Deps.HTTPTransport` is the test seam, which its fake Google, `internal/google/googletest`,
comes in through — only `_test.go` files may import it. Every later delivery takes its
token from the one seam, `tokenSource`. See [Google Workspace](/en/reference/google/#the-guarded-client).

**Meta imports nothing.** It writes nothing outside the `meta` schema; it fills
`ChannelMessenger` under the key `meta` and reads no other module's data. Its two
workers, `meta-split` and `meta-retention`, claim their rows under a lease or in one
statement each and call Meta, or write to the object store, outside any transaction. The
Owner policy is the router's; its one anonymous pair, the webhook's `GET` and `POST`, is
on the public host's list (`security.PayRoutes()`). It reaches the network in one place,
its guarded client, through `internal/netguard` with no proxy and no redirect, to
`graph.facebook.com` and the hosts of Meta's file servers, and `Deps.HTTPTransport` is the
test seam, which its fake Meta, `internal/meta/metatest`, comes in through — only
`_test.go` files may import it. See [Meta](/en/reference/meta/).

## Adding a module

**Backend**

1. Create `internal/<name>` exposing `Module()`, and add it to `businessModules` in
   `cmd/vantigo/main.go`.
2. Add its contract as `openapi/<name>.yaml`; the name is also its mount prefix and
   its schema name. Add it to `Modules` in `internal/openapi/openapi.go` — the list
   the merge, the lint and the coverage report iterate.
3. Add `internal/openapi/gen/cfg-<name>.yaml` (the oapi-codegen config, which names
   the generated package `internal/<name>/gen`) and the two matching
   `//go:generate` lines in `apps/server/generate.go`: one oapi-codegen line and one
   `sqlc generate -f internal/<name>/sqlc.yaml`.
4. Add a `NNNNN_<name>_baseline.sql` migration owning only its own schema.
5. Add a depguard rule set for it in `apps/server/.golangci.yml`, and add its import
   paths to every other module's deny list, so every pairwise isolation rule covers
   it.
6. Add its schema to `moduleSchemas` in `internal/db/schema_test.go`, and its name to
   the known set and any `MODULES` dependency check in `internal/config` — with the
   known and default lists in `config_test.go` — and to the `Modules` literal in
   `internal/module/compose_test.go`.

A module that fills a many-provider slot sets the field on its `module.Module` and
keeps that slot's rule: `CustomerReferences`, `CustomerPersonalData` and `InvoicedWork`
run on the caller's transaction (rules 8–10) and are collected whether or not the
module is on, and so does `VariantReferences`, for a module keeping stock or another fact
of a product variant, which only answers what it holds (rule 20), and `EventHandlers`, for a
module that reacts to another's domain events, which writes only its own schema on the
delivery's transaction and never leaves the process (rule 23), each with a name no
other handler has; `CalendarSource`, for a module with time-bound rows about a person, is
its own gatekeeper (rule 12), is collected from enabled modules only, and needs a
`calendar_source_test.go` in the module proving it answers the caller's rows with its
gate held and nothing without it; `IdentityProvisioner`, for a module driving an
external identity provider's directory for Staff, keeps rule 13, is collected from
enabled modules only, answers a `Key()` no other provider claims, and is tested against
Staff's expectations through `modtest.FakeIdentityProvisioner`'s rules. The three
mailbox slots — `MailboxCalendar`, `ChannelMailbox`, `PersonalMailbox` (rules 14–16) —
and `DirectorySearch` (rule 22) are many-provider slots of the same shape: a module
offering a second mail or directory service fills the builder field with a provider
under its own `Key()` — the key a person's directory account, a channel's `provider`
and an OIDC provider name — and no consumer changes; collected from enabled modules
only, a key no other provider claims, tested through the consumers' own suites with two
`modtest` fakes under two keys.

A module contributing no recorded exchanges needs nothing in
`openapi/testdata/exchanges/`: that corpus is frozen evidence from the retired .NET
suites, and the coverage tool treats a module with no corpus file as zero recorded
exchanges. The module's own `contracttest.RequireCoverage` gate is what proves its
operations are exercised.

**Frontend**

7. Add the package under `apps/<name>/frontend` (`@vantigo/<name>-ui`), copy the
   `no-restricted-imports` block into its `eslint.config.js`, and add it to every
   other module package's deny list.
8. Add `{ module: "<name>", output: "apps/<name>/frontend/src/api-schema.d.ts" }` to
   `tools/openapi/gen-client.ts`, then run `bun run gen:client`.
9. Add the module key to `moduleKeys` in `apps/host/frontend/src/navigation.ts`
   (plus a `searchStrategy` and its `navSearchFor` case if the app has a searchable
   list), register the app (label, icon, home path, sidebar entries) in
   `apps/host/frontend/src/apps.ts`, and add a layout route
   `apps/host/frontend/src/routes/<name>.tsx` that renders
   `createFileRoute("/<name>")(appLayoutOptions("<name>"))`. The route guard derives
   its rules from those same sidebar entries, so a page that more callers may open
   than the entry is offered to says so with `guardPermissions` (Time's approval
   queue: the entry needs `time:approve`, the URL needs only `time:access`).
10. Import the package's catalog in `apps/host/frontend/src/i18n.ts`
    (`import "@vantigo/<name>-ui/i18n";`), and add the host-owned labels in every
    language: navigation and dashboard strings, and **one entry per permission key in
    `apps/host/frontend/src/catalogs/admin.ts`** — without it the admin role editor
    shows a raw key. Register the package's API client in `sessionClients` in
    `apps/host/frontend/src/api/session-handlers.ts`
    (`import * as <name> from "@vantigo/<name>-ui/api/request";`), so a 401 from the
    module's endpoints clears the session and signs the person out like every other
    client's; `session-handlers.test.ts` fails while a package's `api/request.ts`
    exports `setUnauthorizedHandler` and the map lacks it.
11. Wire the dashboard (`routes/dashboard.tsx`: the module card, its metric and its
    attention items, all gated on the module and its permissions) and the spotlight
    (`components/app-spotlight.tsx`: navigation comes from the registry, but a result
    group and any quick action are hand-added). An administration module with nothing
    to count — Entra — adds no dashboard card; it still joins the spotlight test's list.

**Documentation and deployment**

12. Add the module to `contracts` in `docs/astro.config.mjs`, so its API reference is
    generated, and write its pages ([documentation](/en/contributing/documentation/)).
13. Add its name and schema where installations are told about modules: the
    `MODULES` line in `deploy/compose/vantigo.env.example`, the module list and the
    `GRANT` lines in `deploy/compose/README.md`, the module tables, layout trees and `MODULES` default in
    `README.md` and `CONTRIBUTING.md`, and the [installation
    guide's](/en/admin/installation/#database-roles) `GRANT` lines.

The lists above are enumerated by tests that run against the module's base branch. A
module's pull request is checked against the `main` it was opened from, so when another
pull request has touched one of these registries since, rebase onto the latest `main`
and let CI run again before merging: a registry added after the branch point does not
know the new module, and its guard only fails once both have landed.
