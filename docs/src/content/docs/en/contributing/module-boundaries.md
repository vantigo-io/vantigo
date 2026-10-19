---
title: "Module boundaries"
description: "Implementation ownership and the conventions every module follows."
sources:
  - apps/server/internal/module
  - apps/server/internal/contracts
  - apps/server/internal/modtest
---
Vantigo is a modular monolith: one Go binary, one container, one PostgreSQL
database — with strict module boundaries so any module can later be extracted into
its own deployable without a rewrite. Customers, Products, Energy, Communications,
Projects, Time, Expenses, Invoices, Staff and Calendar are vertical-slice modules composed only
by `cmd/vantigo`, on the Identity platform.

Modules never reference each other directly. Synchronous cross-module needs go
through the in-process contracts in `internal/contracts`; asynchronous ones use the
transactional-outbox pattern described in [`ROADMAP.md`](https://github.com/vantigo-io/vantigo/blob/main/ROADMAP.md), of which
Communications' outbox is the working example.

## The rules

1. **No module→module dependencies.** A package under `internal/<module>/…` may
   import platform packages (`internal/config`, `internal/db`, `internal/httpx`,
   `internal/module`, `internal/contracts`, `internal/peppol` — the Peppol
   SML/SMP lookup, shared by Customers and Invoices — `internal/holidays` — the
   Norwegian public-holiday calendar, by which Invoices moves its due dates, Staff
   counts working days and Calendar marks the holidays beside a person's items — `internal/netguard`
   — the shared table of addresses the guarded outbound clients refuse to
   dial (today `internal/mail`'s SMTP guard and `internal/peppol`'s SMP
   client; Brreg, OIDC and the AI client use `http.DefaultTransport`) —
   `internal/payments` — the payments port, shared by Invoices and a later Point of
   sale, with its Vipps MobilePay adapter `internal/payments/vipps`; interfaces,
   values and the adapter only, no SQL, so each consumer keeps its own tables
   (rule 4) — `internal/fileimport` — the reader of an uploaded import file
   (encoding, separator, quoting, the formula guard, header keys), shared by the
   Customers and Energy imports; no SQL, each module keeps its own imports, worker
   and caps — …) and its own subpackages, never another module's.
   `internal/payments/vipps/vippstest`, the adapter's fake, is imported by tests
   only.

   `internal/mail` is such a platform import, and a module that sends mail goes through
   its guarded path (`mail.SendOutbound`, or the test seam `Deps.SMTPSend`).
   `mail.Outbound` and `mail.SendOutbound` were made for Communications, which sends
   with each channel's own stored credentials; Invoices sends with the installation's
   `SMTP_*` configuration, as identity's mail does. Invoices' one change to the package
   is `ReplyTo`: `mail.Outbound` carries a `ReplyTo`, written as the `Reply-To` header
   when set and absent when empty, so a document sent from the installation's
   `SMTP_FROM` can have its replies reach the seller's own mailbox.
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
   `projects`, `time`, `expenses`, `invoices`, `staff`, `calendar`), and no module's migrations or queries reference
   another's. **No cross-schema foreign keys or joins** — reference other modules'
   data by opaque ID only. That is what keeps a future "move this schema to its own
   server" a connection-string change instead of a data migration.
5. **One module, one mount.** A module exposes one `Module()` returning a
   `module.Module`: its name, its `Mount`, the permissions it contributes, the
   background workers it contributes, and the cross-module contracts it *provides*.
   There are twelve single-provider slots, each filled by at most one enabled module:
   `Directory` (`contracts.CustomerDirectory`, customers), `Users`
   (`contracts.UserDirectory`, identity), `Calendar` (`contracts.BusinessCalendar`,
   identity — the workspace's business time zone and the day an instant falls on in
   it, which every module judges "today" by), `Accounts`
   (`contracts.AccountProvisioning`, identity — rule 11), `Products` (`contracts.ProductCatalog`,
   products), `Projects` (`contracts.ProjectDirectory`, projects), `Actuals`
   (`contracts.ProjectActuals`, time), `Expenses`
   (`contracts.ProjectExpenses`, expenses), `Receivables`
   (`contracts.CustomerReceivables`, invoices — what a customer was invoiced and
   owes, every state judged on the provider's own business day), and the three line-level reads an
   invoice is built from — `BillableHours` (`contracts.BillableHours`, time),
   `BillableExpenses` (`contracts.BillableExpenses`, expenses) and
   `BillableMilestones` (`contracts.BillableMilestones`, projects). `module.Compose`
   mounts each module at `/api/v1/<name>/` and builds every provider before any
   `Mount` runs, so a module's `Deps` already carries what it consumes; `Calendar`
   and then `Accounts` are resolved right after `Users`, so the accounts provider may
   read the user directory while it is built; `Actuals`,
   `Expenses`, `Receivables` and the three `Billable*` are resolved last, after
   `Projects` and `Directory`, so those providers' *constructors* are allowed to read
   the project directory or the customer directory (none does, but a future
   provider could).
   Five slots are *many-provider* instead — any number of modules may fill them,
   and Compose collects them in module order: `Workers` (background work, from
   the enabled modules), `CalendarSources` (`contracts.CalendarSource`, rule 12,
   from the enabled modules too), `CustomerReferences`
   (`contracts.CustomerReferenceHolder`, rule 8), `CustomerPersonalData`
   (`contracts.CustomerPersonalData`, rule 9) and `InvoicedWork`
   (`contracts.InvoicedWorkHolder`, rule 10), the last three from every module
   Compose is given, enabled or not — see [Turning a module off](#turning-a-module-off).
   `CalendarSources` is collected from enabled modules only, by Compose and by
   Workers alike, because a source is a read under the module's own permission: a
   disabled module's permissions are not in the catalog, so its gate could only fail
   closed, and its tables may be empty — whereas a customer holder must run whether
   or not its module is on, because a switched-off module still holds rows naming the
   customer. The slot is not called `Calendar`, which is `Deps.Calendar`, the business
   calendar identity provides.
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
   Compose refuses — panic.
   One order is not the module order: both Compose and Workers put the
   `CustomerReferences` and `CustomerPersonalData` of the modules that declare
   `InvoicedWork` **after every other module's**, each side in module order (a
   stable partition). A merge or an anonymisation then locks the invoices
   documents before any row of a module invoices are built from — the order an
   invoice's issue takes them in (rule 10) — so the two can never wait on each
   other.
6. **Never reach around the boundary.** Do not call another module's HTTP endpoints
   from inside the process, and do not reach into another module's schema.

   **The public host is the platform's.** When `PUBLIC_BASE_URL` names a host of its
   own, `security.PublicHostGate` (`internal/security/publichost.go`) answers 404 on it
   to every method and path not on `security.PayRoutes()`, before any module's router
   sees the request. A module's anonymous operation is reachable on the public host only
   when that platform list names it — today the Invoices pay page's three operations, and
   nothing reserved ahead of what is served — so a module adds a public operation by changing the platform's
   list, in the same change and with its test (`TestPublicHostGate`), never by mounting
   anything of its own there.
7. **Frontend packages are isolated too.** A module frontend package (for instance
   `@vantigo/products-ui`) never imports from another module's package or from the
   host app; only the host composes them. Shared UI lives in
   `@vantigo/frontend-shell` and the generated API types and client in
   `@vantigo/frontend-api-client`.
8. **One sanctioned cross-module write.** A module never writes another's data —
   with one exception, made for merging customers:
   `contracts.CustomerReferenceHolder`. A module that stores customer ids in its
   own schema declares `Module.CustomerReferences`, and the customers module's
   merge calls every holder **inside its own transaction**, which already holds
   both customer rows locked. The holder runs its own SQL, on its own schema,
   from its own package — so rules 1, 4 and 6 hold as they do for a read — never
   begins or ends a transaction, never reads a directory (no in-process lookup
   happens under a lock anywhere in this codebase), and tolerates a reference
   that already points at the surviving customer. Today's holders are projects,
   energy, communications and invoices ([Merging duplicates](/en/reference/customers/#merging-duplicates)).
   Another write direction needs a design of its own, not a second holder-shaped
   interface — rule 9 is that design for the second, rule 10 for the third and
   rule 11 for the fourth.
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
    milestones — declares `Module.InvoicedWork`, and Invoices calls the holders
    **inside its issue's transaction**, which already holds the document, the settings
    row, the number counter and, for a credit note, its original locked: after every
    check and the number, before the document is written. `MarkInvoiced` judges each
    source as it stands under the holder's own lock — already invoiced first
    (`source_already_invoiced`), then still approved, ready or billable
    (`source_not_invoiceable`), then unchanged since the draft took it — the revision
    and amount for Time and Projects, the billing facts for Expenses
    (`source_changed`), amounts compared exactly, by value — and stamps it with the
    invoice's id and number at the issue's time; `ReleaseInvoiced` takes the stamp back
    in the issue of the credit note that returns the source's line in full. A source a holder will
    not stamp is answered as a `*contracts.WorkSourceRefusal`, and the issue refuses
    with its code and the line's position — the number, and every holder's write, roll
    back with it; any other error is a failure. A holder keeps rule 8's rules: it runs
    its own SQL on its own schema from its own package, on the caller's `pgx.Tx`; it
    never begins or ends a transaction and never reads a directory, any other contract
    or a clock — every timestamp it writes is the issue's own (`InvoiceRef.IssuedAt`),
    and the issuer's name arrives in `InvoiceRef.IssuedByDisplay`; the period lock
    applies to neither direction, a stamp not being an edit of the work; its release
    takes the same locks as its mark and tolerates a source that no longer carries the
    stamp, writing nothing for it and logging a warning, because a credit note must
    never be blocked; and it runs whether or not its module is enabled, its constructor
    needing nothing but `Deps.Logger`. **The cross-module lock order**
    (`contracts.InvoicedWorkOrder`) is Invoices' own first — the document, the settings
    row, the counter, then a credit note's original — and then the source modules in one
    fixed order: Projects (the project rows, then their milestones), Expenses (the
    claims, then the lines, by id), Time (the entries, by id). No source module ever
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
    expenses and projects ([Invoicing work](/en/reference/invoices/#invoicing-work)).
11. **Staff writes identity's accounts.** The fourth sanctioned cross-module
    direction, staff → identity, made for the accounts of the people Staff
    registers: `contracts.AccountProvisioning`, in the single-provider slot
    `Accounts` (rule 5), which identity provides. Through it a caller prepares a
    Staff-managed user (`Prepare`), enables it (`Activate`), disables it
    (`Deactivate`), updates it (`Update`), reads it (`Account`) and reads the roles an
    Owner named for Staff to give (`ProvisioningRoles`); `Mode` says whether identity
    holds the credentials (`local`) or an identity provider does (`federated`). Unlike
    rules 8–10 it is **not** a holder that runs on the caller's transaction: every
    method runs on identity's own pool, in identity's own transactions, and mails after
    its own commit. So the rule every locked transaction keeps is strict here — **a
    caller never calls it inside one of its own transactions or read-only snapshots,
    and holds no connection or lease across a call**; a worker claims its row in one
    short transaction, commits, calls identity holding nothing, and writes the result
    in a second. Every write names its `contracts.Actor` — exactly one of a user and a
    known system actor (`system:staff-accounts`), which identity writes to its audit;
    a provider refuses an actor that is not `Actor.Valid` with `ErrInvalidActor`
    before it writes anything. The spec
    carries no roles: identity assigns the set an Owner named, only on the `Activate`
    that enables a user Staff disabled, so `staff:manage` can never mint an account
    holding a role it chose, nor give back one an Owner removed.
    `ActivateOpts.FreshInvitation` never re-sends an open invitation, and no
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
    on identity's record of such a user, and `Deactivate` — which alone reads no
    authority mode, so it never answers `ErrModeMismatch` — disables a leaver
    whatever the directory says. Each refusal is a
    sentinel error (`ErrAccountNotManaged`, `ErrAccountPrivileged`,
    `ErrBlockedByOwner`, `ErrAccountEmailTaken`, `ErrEmployeeNumberTaken`,
    `ErrInvalidActor`, `ErrEmployeeRoleMissing`, `ErrEmployeeRoleUnsafe` (a role in
    the set has come to carry an administrative key), `ErrAccountNotFound`,
    `ErrUserGone`, `ErrModeMismatch`, `ErrLastOwner`); anything else is a failure to
    retry. The provider is stateless — api mode with `WORKERS_IN_PROCESS` builds it
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
    module owns — Projects' `visible()`, Time's "mine", Staff's self-service link — and
    a copy would drift; it shows what comes back and never says what was withheld. A
    provider that **fails** returns an error, and the consumer fails the whole request
    rather than show the rest: absent must never also mean "could not be read",
    Customer 360's rule. The consumer calls a provider with the request's context and
    never inside one of its own transactions, through one accessor in its
    `contractscalls.go` that refuses such a call; a provider called without a request —
    from a worker — answers nothing. A provider reads its own tables only
    ([Calendar](/en/reference/calendar/#the-sources-contract)).

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
  billable hours, billable expenses or billable milestones — naming both, or a
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
- **The harness**, `apps/server/internal/modtest`, is what these tests mount their
  modules through: an in-process installation on a real, migrated PostgreSQL database,
  the real server stack over `module.Compose` with identity beside the module under
  test, a settable clock, and a client whose every exchange is validated against the
  module's contract. A module's collaborators are composed for real or replaced by
  contract fakes through its `With*` options — `WithProjects`, `WithBillableHours`,
  `WithInvoicedWork` and the rest — and `WithPoolMaxConns` sets the size of the
  installation's database pool, which the races above hold at two. Identity is always
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
(`customers,products,energy,communications,projects,time,expenses,invoices,staff,calendar`). Identity is always mounted and
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
on the absorbed customer for good. A holder needs only the caller's transaction and its
own schema, both there whatever `MODULES` says. Its `CustomerPersonalData` (rule 9) is
collected the same way and for the same reason: a switched-off module still holds what
it held about a person, and an anonymisation must still take it out.

Some modules cannot be hosted alone. Energy, Communications and Projects read
customer data through `contracts.CustomerDirectory`, which only Customers implements,
so enabling any of them without `customers` is rejected at startup with a message
naming both (`projects requires customers`). Time hangs its hours off projects and
reads them through `contracts.ProjectDirectory`, so `time` without `projects` is
rejected the same way (`time requires projects`). A module that starts requiring
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
provider's placeholder address, which no mail reaches, is answered empty.

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

Time consumes four contracts and provides two, and fills one many-provider slot:
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
lock — and it fills `InvoicedWork` with the holder that stamps an entry invoiced
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
judged), all on the pool and never under a lock; products and energy it does not
read. Inside an issue's transaction it calls the invoiced-work holders of time,
expenses and projects (rule 10) — the one call it makes under a lock. It sends mail
only through the platform's SMTP seam, never under a lock, and provides no
single-provider contract. It fills both many-provider slots: as a `CustomerReferenceHolder` it re-points every document of a
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

**Staff depends on nobody but identity**, as Expenses does: `MODULES=staff` alone is a
valid installation, and no other module's state changes what Staff answers. It reaches
three things outside its schema: identity's `Deps.Users` (`contracts.UserDirectory`,
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
people's documents. It counts the working days an absence takes by the platform
calendar (`internal/holidays`), never a holiday list of its own. It reads no business module, provides no contract and fills no
slot, so switching it off strands nothing in another module — but it does strand its
own objects and the rows of its ledger of objects still to remove
(`staff.object_deletions`), which wait untouched until the module is on again. It keeps
rule 10's restatement strictly: all three are reached **before a transaction opens or
after it commits, never inside one** — neither under a person's lock nor inside a
read-only snapshot, which holds a pool connection just the same. Each goes through one
accessor — in `internal/staff/contractscalls.go`, or `internal/staff/objects.go` for the
store (`objectPut`, `objectGet`, `objectDelete`) — that refuses a call made inside one of
the module's transactions (the marking Invoices introduced, copied rather than
imported), and the harness records every call and fails a test that makes one under an
open transaction. So a document's object is written before its row's transaction and
removed after a refusal, read after the row's snapshot, and removed after the delete
commits. Its one background worker, `staff-retention`, is contributed through `Workers`
behind `STAFF_RETENTION_ENABLED` and keeps two connection disciplines: the erase runs on
the advisory lease's own connection — the due queries and every person's transaction,
the person's lock included, never asking the pool for a second, which is how a lease
holder deadlocked the customers worker with a pool of four (PR #124) — and the ledger
pass over `staff.object_deletions` takes no lease and holds no connection across
`objectDelete`. See [Staff](/en/reference/staff/#locks).

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
   the known set and any `MODULES` dependency check in `internal/config`.

A module that fills a many-provider slot sets the field on its `module.Module` and
keeps that slot's rule: `CustomerReferences`, `CustomerPersonalData` and `InvoicedWork`
run on the caller's transaction (rules 8–10) and are collected whether or not the
module is on; `CalendarSource`, for a module with time-bound rows about a person, is
its own gatekeeper (rule 12), is collected from enabled modules only, and needs a
`calendar_source_test.go` in the module proving it answers the caller's rows with its
gate held and nothing without it.

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
    shows a raw key.
11. Wire the dashboard (`routes/dashboard.tsx`: the module card, its metric and its
    attention items, all gated on the module and its permissions) and the spotlight
    (`components/app-spotlight.tsx`: navigation comes from the registry, but a result
    group and any quick action are hand-added).
