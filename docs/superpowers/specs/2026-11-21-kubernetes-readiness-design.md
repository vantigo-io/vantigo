# Kubernetes readiness — design

Revision 3, 2026-11-21. Drafted from the owner's five points (S3, replicas, a migration job,
OpenTelemetry with the tenant on every signal, a private usage endpoint) and the follow-up
conversation (revision 1), then corrected after an adversarial review against the code
(revision 2: three wrong claims about the code, four design flaws and the smaller items are folded
into the text), then given the owner's answers to §10 (revision 3, which adds KR12a, KR42 and KR43 where they belong rather than renumbering). Decisions are numbered KR*n*.
Code references are against `f0c6b4579`. `srv/` is `apps/server/internal/`; `cmd/` is
`apps/server/cmd/vantigo/`; `host/` is `apps/host/frontend/src/`.

## 1. Purpose

One Vantigo installation is one tenant: its own namespace, database and bucket, run by the
operator (`vantigo-io/vantigo-operator`). This design makes the app a good citizen of that
cluster: it keeps serving while its pods and the cluster's nodes are replaced, a release is
migrated once and rolls out only when the migration succeeded, its files live in the tenant's
bucket, every trace, metric and log says which tenant it came from, browser errors reach the same
stack, and the operator can read the tenant's consumption without touching its database or signing
in.

### What already exists

The design is smaller than the five points suggest, because much of it is in place:

| Need | Already there | Gap |
| --- | --- | --- |
| Migration only once | `vantigo migrate` on `MIGRATIONS_DATABASE_URL`, serialised by an advisory lock (`srv/db/migrate.go` `MigrationLockKey`); `vantigo server` never migrates and never runs workers (`cmd/main.go` `runWorkers`) | Readiness does not know the schema version; the compatibility rule is unwritten |
| Several replicas | Workers take an advisory lock or a per-row lease (`admin/installation.md` "Background workers and scaling"); rate limits live in PostgreSQL (`srv/ratelimit`); live updates fan out over `LISTEN/NOTIFY` (`srv/live`) | Probes, drain on SIGTERM, lease deadlines from each pod's own clock |
| Object storage | A provider-neutral port, `ObjectStore` (`srv/storage/storage.go`), with one driver, `fs`; ten modules each call `storage.New` | No S3 driver; no storage readiness check, although `admin/installation.md` says there is one |
| OpenTelemetry | OTLP/HTTP per signal, the slog bridge, HTTP and pgx instrumentation, `OTEL_RESOURCE_ATTRIBUTES` merged (`srv/telemetry`) | No tenant attribute by default, outbound HTTP and workers untraced, no worker metrics, nothing from the browser |
| Private endpoint | `MANAGEMENT_PORT` + `MANAGEMENT_TOKEN` (required together, token ≥ 32 characters, `srv/config/config.go` around line 2381), no host filter, `GET /management/status` with users and database bytes (`srv/management`) | Served only in `api`/`server`, not `worker`; the bearer check wraps the whole mux; no usage counters |

### Deliveries

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **A** | Probes on the management port in every command; readiness on the schema version; drain on SIGTERM; the database-anchored clock; docs | — |
| **B** | The S3 driver, its readiness check, `vantigo storage copy`, writable temp dir; docs | — |
| **C** | The tenant on every signal; traced outbound calls and worker cycles; worker and pool metrics; log–trace correlation; the attribute policy; docs | — |
| **D1** | Browser errors and failed requests to the same stack, linked to the backend trace, with minified stacks and the release version; docs | C |
| **D2** | Server-side symbolication of browser stacks from source maps embedded in the binary | D1 |
| **E** | `GET /management/usage`, the usage counters, the usage token, token rotation; docs | A (management in every command) |
| **F** | The schema compatibility rule and its CI lint; contributing docs | — |
| **Operator** | Everything in §9, in `vantigo-operator` | A, B, E for the app side |

A, B, C and F touch different files and can run in parallel. E leads with seats and active users
(KR43), then AI usage and invoices sent (§10, Q6).

**Not in this phase:** backup and restore of one tenant (database and bucket at one point in
time); suspending and deprovisioning a tenant; data export on exit; presigned downloads; a
"previous binary on the new schema" CI job (KR39 names it as the next step); session-replay or
RUM beyond errors and failed requests.

## 2. Probes and drain (A)

- **KR1 — Probes on the management port.** The management listener serves `GET /health/live` and
  `GET /health/ready` without the bearer token, next to its token-protected routes. They are the
  same handlers as the public ones (`srv/health.Handler`), the same checks. The public listener
  keeps serving them too, so Compose and the `healthcheck` command are unchanged. Kubernetes
  `httpGet` probes point at the management port, where no host filter rejects the pod address
  (today they get 400, `admin/installation.md` "Health probes").
- **KR2 — The management listener in every command.** `serve` opens it when `cfg.Management` is
  set, whatever the command: today it is opened only `withAPI` (`cmd/main.go` around line 488).
  A `worker` pod already answers `/health/*` on `PORT` without a host filter (its handler is the
  bare health handler, `cmd/main.go` around line 452), but the operator should probe every pod
  the same way. In `worker` mode the management listener serves the probes, `status` and
  `usage` (§6); the outbox routes stay too, since the worker is where the dispatcher runs. `Live`
  stays nil there.
- **KR3 — Probes without a token.** The probe routes are mounted outside the bearer check, which
  today wraps the whole management mux (`srv/management/management.go`). Config is relaxed so
  that `MANAGEMENT_PORT` may be set without `MANAGEMENT_TOKEN` (today a startup error): the
  listener then serves the two probes, and every other route answers 401. A probe reveals the
  version and the names of failing checks, nothing else.
- **KR4 — The process waits for the schema.** Readiness alone is not enough: `serve` runs
  `identity.RunStartup` and the branding import, which read and write, and starts the workers,
  whatever readiness says (`cmd/main.go` around lines 546, 555, 631). So `serve`, in `server` and
  `worker` mode, first waits until the schema is at least the highest migration embedded in the
  binary: `SELECT max(version_id) FROM goose_db_version WHERE is_applied`, polled every 2
  seconds, logged every 30. While it waits, the management listener is already up and readiness
  answers 503 with `schema` in `failing`; nothing else runs. A new pod that starts before the
  migration Job finishes therefore does nothing until it has, and the rollout stops by itself if
  the Job fails. An old pod on a newer schema passes at once (KR36). Once passed, the check is not
  re-read: the schema does not go backwards. `api` mode is unchanged, since it migrates first.
- **KR5 — Drain on SIGTERM.** On the first signal the process (a) flips readiness to 503 with
  `draining` in `failing`, (b) waits `SHUTDOWN_DRAIN_DELAY` (new; default `0s`, which is today's
  behaviour; the operator sets `10s`), so endpoints controllers and the ingress take the pod out
  of rotation, (c) closes every live socket with 1001, so browsers reconnect, with the shell's
  existing ±20% jitter (`packages/frontend-shell/src/live/client.ts`), to another pod, (d) then
  shuts down. Today the hub's context and `srv.Shutdown` derive from the signal context
  (`cmd/main.go` around lines 574, 583), so sockets close at the signal — before any drain — and
  browsers can reconnect to the draining pod. Delivery A gives the hub and the HTTP server a
  context of their own, cancelled after the delay. The whole shutdown tail after the delay —
  HTTP drain, workers, the management listener, `hub.Wait`, the rate-limit flush, `closePool`,
  which today each take their own bound in sequence — runs under one deadline,
  `SHUTDOWN_TIMEOUT`. The documented rule is then
  `terminationGracePeriodSeconds ≥ SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT + 5s`.
- **KR6 — A startup probe.** The docs recommend a `startupProbe` on `/health/ready` with a long
  failure threshold, rather than a large `initialDelaySeconds`, because KR4 can hold a pod for
  as long as a migration takes.

## 3. The clock (A)

Lease deadlines are computed in Go from each process's clock and compared in SQL against a
deadline another process wrote: dozens of queries, in sqlc query files and in hand-written SQL, take the
time as a parameter (`claimed_until < $n`, `lease_until <= $n`). The rate limiter's windows do
the same with `time.Now` (`srv/ratelimit/ratelimit.go` around line 103). A pod whose clock runs ahead takes over a claim
another pod still holds.

- **KR7 — `Deps.Clock` is anchored to the database.** `cmd/main.go` builds `Deps.Clock` (today
  `time.Now`, line 347) as the local monotonic clock plus an offset measured against
  PostgreSQL: `SELECT clock_timestamp()`, corrected by half the round trip, taken at start and
  every 60 seconds, the median of the last five kept. Every lease in every pod is then on the
  database's clock without changing a query, and tests keep injecting their own clock as they
  do today. The rate limiter is built with `ratelimit.NewWithClock(pool, deps.Clock)`.
- **KR8 — The offset moves gently, after the first.** The first measurement applies in full,
  before any worker starts. Later ones move the offset by at most 100 ms per minute, so one slow
  round trip cannot jump the clock. Time never runs backwards: the clock is clamped to the last
  value it returned, held in an atomic, so the clamp costs nothing measurable on a call made per
  query.
- **KR9 — Skew is visible.** The measured offset is a gauge, `vantigo.clock.offset` (seconds).
  An offset above `CLOCK_MAX_SKEW` (new, default `5s`) logs a warning once per hour. It does not
  fail readiness: the anchored clock is already correct, and the warning tells the operator the
  node's NTP is broken.
- **KR10 — Direct `time.Now()` on a lease path is a bug.** 44 non-test, non-store files call
  `time.Now()`; most are `if deps.Clock == nil` fallbacks (for example
  `srv/customers/peppol_recheck_worker.go` around line 336). Delivery A moves every call that
  computes or compares a lease, a claim, a window or a retry deadline onto `Deps.Clock`; a
  `forbidigo` rule in `apps/server/.golangci.yml` then rejects `time.Now` in worker files, with an
  allow-list that names the nil-clock fallbacks. Timestamps for display and logs may stay.
- **KR42 — Every database session is UTC.** All stored times are `timestamptz`, which is an
  instant whatever the zone, so the clock offset (KR7) is a question of skew, never of time zone.
  But about 110 query files cast to `date` or call `date_trunc`, and those answers depend on the
  session's `TimeZone`, which the pool does not set today (`srv/db/db.go`): a managed PostgreSQL
  whose default is not UTC would shift every day boundary. The runtime pool, the migration
  connection and the live hub's connections all set `TimeZone=UTC` as a runtime parameter, and
  Go converts to UTC before it formats a day (`time.Time.UTC()`). A workspace's own time zone
  (`schema_workspace_time_zone`) stays what it is: it converts at the edge, for display and for
  the business rules that name it, never by changing the session.

**Alternative not taken:** rewriting the 43 queries to use `now()` in SQL. It removes the
dependency on the Go clock entirely, but breaks every test that advances an injected clock to
expire a lease, and those tests are already the fragile part of the suite (PR #308). KR7 gets the same effect with one change in one place.

## 4. S3 object storage (B)

- **KR11 — The driver.** `STORAGE_PROVIDER=s3` selects `srv/storage/s3.go`, which implements
  `ObjectStore` against any S3-compatible service. Settings:

  | Variable | Required | Meaning |
  | --- | --- | --- |
  | `STORAGE_S3_ENDPOINT` | yes | `https://…`; `http` only with `APP_ENV=development` |
  | `STORAGE_S3_REGION` | yes | The provider's region name |
  | `STORAGE_S3_BUCKET` | yes | The tenant's bucket; never created by the app |
  | `STORAGE_S3_PREFIX` | no | A key prefix inside the bucket, canonical like a scope; default none |
  | `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | yes | Static credentials; `_FILE` variants read a mounted secret |
  | `STORAGE_S3_FORCE_PATH_STYLE` | no | Default `1`; UpCloud and MinIO need path-style |

  The physical key is `{prefix/}{scope}/{key}`, the same as the `fs` driver's relative path, so
  `vantigo storage copy` (KR15) maps one to the other with no renaming.
- **KR12 — The client library is `minio-go`.** It is written for S3-compatible services first,
  streams uploads of unknown length as multipart, and is one module with few dependencies. The AWS
  SDK v2 works too, but its S3 client has sent CRC integrity checksums by default since early
  2025, which several S3-compatible services reject unless the client is configured back to
  "when required", and it brings a much larger dependency tree for nothing Vantigo uses. The
  driver stays behind `ObjectStore`, so a later AWS-only feature could add an SDK-based driver
  without touching a module.
- **KR12a — UpCloud is assumed to scope credentials to a bucket.** The design assumes UpCloud
  Managed Object Storage can issue credentials that reach one bucket only and encrypts at rest.
  Delivery B starts by verifying both against UpCloud's documentation and a test bucket; if
  per-bucket credentials do not exist, the operator's isolation (§9, item 5) needs another answer
  before a second tenant shares an object storage instance.
- **KR13 — Behaviour matches the port.** `Put` streams (multipart above 16 MiB, part size
  16 MiB), sets `Content-Type`, and overwrites; `Get` maps `NoSuchKey` to `ErrNotExist`;
  `Exists` is a `HEAD`; `Delete` of a missing key succeeds (S3 already does). Key validation stays
  in the scope wrapper, before any driver. Downloads still stream through the authorised
  endpoint: no presigned URLs, the bucket is never public.
- **KR14 — A storage readiness check.** None exists today: the only check is `postgres`
  (`cmd/main.go` around line 451), and `serve` builds no store — ten modules call `storage.New`
  themselves. Delivery B builds the store once in `serve` for a check named `storage`: `HeadBucket`
  for `s3`, a stat of the root for `fs`, nothing when storage is unset. A missing bucket or refused
  credentials fail readiness; they do not stop the process (the fail-closed rule in
  `srv/storage/storage.go` stays). B also corrects `admin/installation.md`, which already claims
  the check.
- **KR15 — `vantigo storage copy`.** A new command copies every object from the `fs` root to the
  configured `s3` bucket (or the reverse with `--to fs`), skipping objects that exist with the
  same size and the `fs` driver's `.<name>.<suffix>.tmp` leftovers (`srv/storage/fs.go` around
  line 145), and prints counts. It is idempotent. The documented cutover: copy while running, stop
  writes (scale to zero), copy again, switch `STORAGE_PROVIDER`, start.
- **KR16 — A writable temp dir.** `TMPDIR` is honoured, the docs require it to be writable, and
  the Kubernetes guidance mounts an `emptyDir` at `/tmp` so the root filesystem can be read-only.
  Today `srv/accounting/saft_import.go` writes a temp file; delivery B lists every such user in
  the docs.

## 5. Observability (C, D1, D2)

### The tenant on every signal (C)

- **KR17 — `VANTIGO_TENANT_ID`.** `OTEL_RESOURCE_ATTRIBUTES` can carry `vantigo.tenant.id`
  already; a first-class setting adds validation and reaches what the resource does not (KR18). It
  is set by the operator: 1–63 characters, `[a-z0-9-]`, not starting or ending with `-`, so it
  doubles as a namespace name. Names Kubernetes or the operator use are refused: `default`,
  anything starting `kube-`, and `vantigo-system` (the operator's own namespace), so no tenant can
  be called `kube-system`; the operator applies the same rule before it creates a namespace. It
  becomes the resource attribute `vantigo.tenant.id` on traces,
  metrics and logs. Unset (Compose, development), the attribute is absent.
  `OTEL_RESOURCE_ATTRIBUTES` still overrides, as today.
- **KR18 — Stdout logs carry it too.** When logs are not exported over OTLP, a cluster's log
  agent reads stdout and never sees the resource. The JSON logger then adds `tenant` (and, KR22,
  `trace_id`/`span_id`) to every record.
- **KR19 — The backend's part ends at the resource.** Promoting `vantigo.tenant.id` to a metric
  label (Mimir/Prometheus keep resource attributes in `target_info`) is the collector's or the
  backend's configuration; the admin docs show the collector `transform` that does it.

### Deeper backend telemetry (C)

- **KR20 — Outbound calls are traced.** There is no single outbound client today: about 18
  constructors build their own `http.Client` (for example `srv/entra/graph.go`,
  `srv/google/client.go`, `srv/meta/transport.go`, `srv/peppol/lookup.go`, `srv/mail/cloudflare.go`,
  `srv/identity/webhooks_client.go`), and Vipps, Folio, Bring, Brreg and the AI providers use
  `Deps.HTTPTransport`, which is `http.DefaultTransport` in production (`srv/module/module.go`
  around line 511). Delivery C adds `telemetry.OutboundTransport(base)` and applies it in each of
  those constructors and to `Deps.HTTPTransport`; a lint then rejects `&http.Client{` in
  non-test code without it. Each call is a client span with method, host and status. The
  wrapper injects no `traceparent` (its propagator is a no-op), because `otelhttp.NewTransport`
  would otherwise hand our trace IDs to every provider; an allow-list of hosts that may receive
  it can come later.
- **KR21 — Workers are traced and measured.** The worker runner (`srv/worker/runner.go`) starts a
  span per cycle, `worker <name>`, with the error recorded and a recovered panic as an exception
  event. Metrics: `vantigo.worker.cycles` (counter; `worker`, `outcome`=`ok|error|panic`),
  `vantigo.worker.cycle.duration` (histogram), and `vantigo.events.backlog` (gauge: undelivered
  outbox rows and the age of the oldest). Lease-taking workers add `vantigo.worker.claims`.
- **KR22 — Logs link to traces.** A record logged inside a span carries its `trace_id` and
  `span_id`, in OTLP and in stdout JSON.
- **KR23 — HTTP and pool metrics.** Server spans and metrics use the route pattern
  (`GET /api/invoices/{id}`), never the raw path. `pgxpool` stats (acquired, idle, waiting, wait
  time) become gauges, so a too-small `DATABASE_POOL_SIZE` is visible before it is an outage.
- **KR24 — What an attribute may hold.** Allowed: tenant, request ID, operation ID, module,
  route pattern, worker, status, an opaque user ID (UUID) and its kind (person, agent, integration).
  Never: names, email addresses, request or response bodies, query strings, file names, free text
  a user typed. Error messages are recorded as written by our code; an error that wraps a
  provider's response body is trimmed to its status and code. The rule is written down in
  `contributing/platform-packages.md` and a test greps exported span attributes in the
  integration suite for an email-shaped value.
- **KR25 — Sampling, and who decides it.** `OTEL_TRACES_SAMPLER`/`_ARG` as the SDK reads them.
  The public listener does not let an incoming `traceparent` decide sampling: any internet client
  (and the browser, KR29) could otherwise force every trace. The server's root sampler there is
  `TraceIDRatioBased`, and an incoming context is recorded as a span link rather than a parent.
  Keeping every error trace while sampling the rest needs tail sampling, which belongs in the
  collector; the admin docs show it.

### The browser (D1, D2)

- **KR26 — What is captured.** Uncaught errors (`error`), unhandled rejections, errors caught by
  the host's `AppErrorBoundary` and by the router's `defaultErrorComponent` (`RouterError`), which
  catches render errors before the boundary does (`host/main.tsx` around lines 43, 65), and API
  responses of 5xx or network failure. Not captured: 4xx, which are ordinary answers, clicks, page
  views, or anything typed.
- **KR27 — Same origin, through the server.** The host shell batches events (at most 20 per
  batch, every 10 seconds or on `pagehide` with `sendBeacon`) to `POST /api/telemetry/browser`.
  CSP's `connect-src 'self'` stays as it is, the collector is never exposed to the internet, and
  the server adds the tenant. The server re-emits each event as an OTLP log record
  (`vantigo.browser.error`) with the route pattern, app, release version, browser family and the
  opaque user ID; signed-out pages (sign-in, setup) may send too.
- **KR28 — Abuse limits.** The endpoint takes at most 64 KiB per request, truncates messages to
  1 KiB and stacks to 8 KiB, and is limited per client by `srv/ratelimit` (30 batches per 10
  minutes signed in, 5 signed out). Because signed-out sending is a write path from the open
  internet into the tenant's log pipeline, each pod also caps what it re-emits: at most 50 events
  per second in all and 10 per second from signed-out pages, dropping the rest and counting the
  drops in `vantigo.browser.dropped`. Over any limit it answers 204, so a broken page cannot retry
  in a loop. `BROWSER_TELEMETRY_ENABLED` (default on when logs are exported) turns it off; off,
  the shell does not send at all (the setting reaches the shell in its boot config).
- **KR29 — A browser error links to the backend.** `createApiClient`
  (`packages/frontend-api-client/src/index.ts`), from which each module's client is built, sends a
  `traceparent` on every request and records it on a failed request's event. The server records it
  as a link (KR25), so a 5xx in the browser leads to the server's trace without the browser
  deciding sampling. About 30 frontend files call `fetch` directly; delivery D1 moves the ones that
  call `/api` onto `createApiClient` or a shared wrapper, and lists the rest (static assets) as
  out of scope.
- **KR30 — Stack traces are symbolicated by the server, in a second step.** D1 ships minified
  stacks with the release version, which already groups errors and says where they come from. D2
  keeps the source maps out of the served assets, embeds them in the binary, and maps frames to
  source in the endpoint before export. Doing it in the server keeps the source private (nothing
  public serves the maps) and works with any observability backend, where uploading maps to one
  vendor's symbolicator would tie us to it.

## 6. The usage endpoint (E)

- **KR31 — `GET /management/usage`.** On the management listener, behind a token (KR40). It
  answers:

  ```json
  {
    "tenant": "acme",
    "version": "1.42.0",
    "generatedAt": "2026-11-21T10:00:00Z",
    "gauges": {
      "seats": 38, "activeUsers30d": 30, "agents": 2, "integrations": 3,
      "modules": ["customers", "invoices"], "databaseBytes": 123456789
    },
    "counters": [
      { "metric": "seats.max", "day": "2026-11-20", "value": 38, "final": true },
      { "metric": "users.active", "day": "2026-11-20", "value": 27, "final": true },
      { "metric": "invoices.sent", "day": "2026-11-20", "value": 118, "final": true }
    ]
  }
  ```

  `?from=` and `?to=` take UTC dates (default the last 31 days, at most 400). Counters are per
  UTC day, and the caller bills by summing final days (KR32), so a retry, a restart or a second
  replica cannot double-count.
- **KR32 — Where counters come from.** One table, `platform.usage_counters (metric text, day date,
  value bigint, primary key (metric, day))`. Three paths feed it:
  - **Domain events**, for metrics that have one (`invoices.document.issued`,
    `orders.sales_order.confirmed`): a handler increments in the delivery's own transaction, which
    is what makes it once (`srv/events/events.go`, the Handler doc). The `day` is the event's
    `OccurredAt`, never the delivery time, so a delivery retried days later lands on its own day.
  - **`contracts.Usage.Count(ctx, tx, metric, n)`**, in the caller's transaction, for business
    actions with no event (`mail.sent`, `sms.sent`, `invoices.ehf.sent`).
  - **An in-process buffer**, flushed every 30 seconds like the rate limiter's flusher
    (`cmd/main.go` around line 468), for high-volume or transactionless counts (`api.requests`,
    `mcp.requests`, AI tokens): one row per request would be a hot row that serialises all API
    traffic. A crash loses at most 30 seconds of these, an under-count, never an over-count.

  Counting is never derived from the business tables, because retention and erasure delete rows
  the bill was already based on. A day is final 72 hours after it ends (the settle window, covering
  outbox retries); the endpoint marks later days `"final": false`. The usage module's own retention
  pass, added to the events dispatcher's prune (`srv/events/dispatch.go` around line 434), keeps
  400 days.
- **KR43 — Seats and active users come first.**
  - A **seat** is a user of kind `person` that can sign in: not disabled, not locked out. Agents
    and integrations are not seats; agents are counted as `agents`. (Today's `activeUsers` in
    `/management/status` counts accounts that can sign in, of every kind, and says nothing about
    use: `srv/identity/installation.go`.) `seats.max` is the day's highest seat count, sampled
    every 15 minutes and on every change to a user's kind or state, written with
    `value = GREATEST(value, $n)`, so a seat added and removed the same day is still billed.
  - An **active user** is a seat that made at least one authenticated request that day, by
    session, never by API key or connection. Sessions cannot tell this, since they are deleted
    when they expire (`identity.sessions`). A new table, `platform.usage_active_users (day date,
    user_id uuid, primary key (day, user_id))`, gets one row per seat per day: the session
    middleware remembers, in process, who it has already recorded today and inserts with
    `ON CONFLICT DO NOTHING` otherwise, so the cost is one insert per person per day per pod.
    `users.active` is the day's distinct count, and `activeUsers30d` the distinct count over the
    trailing 30 days.
  - The rows hold the opaque user ID and nothing else, have no foreign key, and are kept 400 days
    as a billing record; erasing a person leaves them (§10, item 8).
- **KR33 — The metric names are a contract.** They are listed, with their unit and source, in a
  new section of the reference (`reference/api-compatibility.md` gets a link) and only ever added
  to; a renamed metric is a new name and the old one stops growing. The first set is in §10, Q6.
- **KR34 — Storage bytes are the operator's.** The app does not list a bucket to sum its size:
  the provider reports a bucket's usage more cheaply and more truthfully, and the operator reads
  it there. `databaseBytes` stays, as today.
- **KR35 — Gauges are also OTel metrics.** Users, active users and database bytes are exported
  as gauges too, for dashboards. Billing reads the endpoint, never the metrics: metrics may be
  sampled, dropped or aggregated on the way.

## 7. Schema compatibility (F)

- **KR36 — The rule.** The schema a release migrates to must work with the previous release's
  binary. A change that removes or narrows is split across two releases: expand (add the new
  column, write both, read the new) in one, contract (drop the old) in the next. Migrations stay
  forward-only.
- **KR37 — The lint.** A CI step, `tools/db/check-migrations.ts`, reads migrations added by the
  pull request and fails on `DROP TABLE`, `DROP COLUMN`, `RENAME`, `ALTER COLUMN … TYPE`,
  `SET NOT NULL`, `DROP DEFAULT`, `ADD COLUMN … NOT NULL` without a `DEFAULT`, a new `CHECK`,
  `UNIQUE` or foreign-key constraint on an existing table, and a new trigger on an existing table,
  since old inserts may violate them — unless the statement's file has a line
  `-- vantigo:contract <reason>`, which a reviewer checks against the previous release. It runs in
  `mise run server:check`.
- **KR38 — Old pods must run `server`.** The rolling update keeps old pods on `server`, which
  never migrates: an old `api` would ask goose to migrate a schema with versions it does not
  know. The admin docs say so, and the operator never runs `api`.
- **KR39 — Next step, not this phase.** A CI job that migrates with the new binary, starts the
  previous release's image in `server` mode and runs a smoke test against it.

## 8. The management token (E)

- **KR40 — A usage-only token.** `MANAGEMENT_USAGE_TOKEN` (new) reaches `GET /management/usage`
  and `GET /management/status` only; `MANAGEMENT_TOKEN` reaches everything. A billing collector
  then holds a token that cannot retry or skip outbox deliveries. Either may be unset.
- **KR41 — Rotation without downtime.** Each token variable takes up to two values separated by a
  comma; either is accepted, and each value must meet today's rule (no whitespace, at least 32
  characters). The operator adds the new token, rolls out, switches callers, removes the old one.
  Both are compared in constant time, as today.

## 9. What the operator owns

The app cannot enforce these; they go to `vantigo-operator`'s spec as requirements.

1. **Release order.** A `migrate` Job per release, with `MIGRATIONS_DATABASE_URL` (the schema
   owner role); the `server` and `worker` Deployments move to the new image only after the Job
   succeeds. KR4 is the safety net if they move early. A failed Job leaves the old release
   serving.
2. **Shape.** `server` with at least 2 replicas for HA tenants, `worker` with 2 (the existing locks and
   leases, on KR7's clock, make that safe); topology spread across nodes and zones; a PodDisruptionBudget of `minAvailable: 1` on
   each; `terminationGracePeriodSeconds` per KR5; probes on the management port (KR1, KR6).
3. **Pods.** `runAsNonRoot`, `readOnlyRootFilesystem` with an `emptyDir` at `/tmp` (KR16), no
   service-account token mounted — except a projected token volume when single sign-on uses
   workload identity (`OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`) — `GOMEMLIMIT` from the memory limit through the downward API,
   requests and limits from measured use.
4. **NetworkPolicy, default deny both ways** in each tenant namespace, then allow: ingress to the
   app port from the ingress controller only; ingress to the management port from the operator's
   and the billing collector's namespaces only (kubelet probes come from the node; whether the
   CNI lets them past a default-deny policy must be verified for UpCloud's CNI); egress to DNS, the
   tenant's database and, when set, the separate `LIVE_DATABASE_URL` endpoint, the object storage
   endpoint, the OTel collector, SMTP (`SMTP_PORT`, 587 by default) when mail uses it, an external
   `PEPPOL_DNS_SERVER` when set, and the internet on 443 for the modules' providers. Egress to the cluster's pod and
   service ranges is denied except those named, so a compromised pod cannot reach another
   tenant's pods, and egress to the cloud metadata address (`169.254.169.254`) is denied. If the
   CNI supports FQDN rules (Cilium's `toFQDNs`), the 443 egress narrows to the providers' hosts
   for the enabled modules.
5. **Isolation beyond the network.** A database and roles per tenant; S3 credentials that reach
   only the tenant's bucket; a Secret per tenant.
6. **Secrets.** `APP_SECRET` generated once and never regenerated by a reconcile — losing it makes
   every stored TOTP secret unrecoverable; `VANTIGO_TENANT_ID`, both management tokens, the S3
   credentials and the database URLs set by the operator.
7. **Usage.** The operator (or a billing collector it runs) polls `/management/usage` and reads
   bucket usage from the provider (KR34).

## 10. Owner decisions

All answered on 2026-11-21:

1. **The clock.** Anchor `Deps.Clock` to the database (KR7), and keep every session in UTC
   (KR42).
2. **S3 library.** `minio-go` (KR12), on the reasoning there.
3. **UpCloud.** Assumed to give per-bucket credentials and encryption at rest; delivery B
   verifies it first (KR12a).
4. **Tenant ID.** A slug equal to the namespace, with Kubernetes' and the operator's names
   refused (KR17).
5. **Source maps.** Minified stacks in D1, symbolication in the server in D2 (KR30).
6. **Usage metrics, in this order.** `seats.max` and `users.active` (KR43); then AI usage,
   `ai.tokens.input.<model>`, `ai.tokens.output.<model>` and `ai.requests.<model>`; then
   `invoices.sent` by channel (`email`, `ehf`, `print`), counted when the send worker hands the
   invoice to the channel; then the rest: `invoices.issued`, `orders.confirmed`,
   `purchasing.orders.sent`, `mail.sent`, `sms.sent`, `api.requests`, `mcp.requests`,
   `webhooks.delivered`, `accounting.vouchers.posted`.
7. **A usage-only token.** Yes (KR40). `MANAGEMENT_TOKEN` can also retry and skip outbox
   deliveries, which changes what the tenant's integrations receive. A billing system needs only
   to read, so it holds `MANAGEMENT_USAGE_TOKEN`, which reaches `status` and `usage` and nothing
   else, and a leaked billing credential cannot touch the tenant's data flow.

8. **Active-user rows and erasure (KR43).** Kept after a person is erased, so past bills stay
   reproducible; the rows say only that an opaque ID was active on a day. Delivery E says so in the
   privacy documentation, and the erasure code leaves `platform.usage_active_users` alone on
   purpose, with a test that proves it.

## 11. Documentation and the public site

Every delivery lands with its pages, in English and Norwegian bokmål where the section is
bilingual:

| Delivery | Pages |
| --- | --- |
| A | `admin/management-listener.md` (probes, every command, the token rule), `admin/installation.md` (Health probes, Background workers and scaling, Upgrading) and a new `admin/kubernetes.md` with the probes, drain, grace period and clock — both in `en/` and `nb/`, the new page listed in the admin overview; `admin/configuration.md` for `SHUTDOWN_DRAIN_DELAY`, `CLOCK_MAX_SKEW` |
| B | `admin/installation.md` (Health probes: the storage check), `admin/object-storage.md` (replaces "The provider set is a regression…" with the S3 driver, its settings, UpCloud, `vantigo storage copy`), `admin/configuration.md`, `admin/kubernetes.md` (temp dir) |
| C | `admin/installation.md` "Email and observability", `admin/kubernetes.md` (collector transform, tail sampling), `admin/configuration.md` (`VANTIGO_TENANT_ID`); `contributing/platform-packages.md` (attribute policy) |
| D1, D2 | `admin/installation.md` "Email and observability" (browser errors, `BROWSER_TELEMETRY_ENABLED`); `contributing/frontend-architecture.md` |
| E | `admin/management-listener.md` (usage, the usage token, rotation); the metric list in the reference |
| F | `contributing/` — a new section on schema changes in `platform-packages.md` or its own page |

**The public site.** None of this is an app capability, so no app page changes. Whether "high
availability" and "hosted in Norway on UpCloud" belong on the site's hosting or pricing copy is the
owner's call once the operator offers them.
