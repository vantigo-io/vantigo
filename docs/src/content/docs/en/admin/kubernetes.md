---
title: Kubernetes
description: "Running Vantigo on Kubernetes: the probes on the management port and a startup probe, the drain on SIGTERM and the grace period it needs, how many server and worker replicas, the database-anchored clock, UTC database sessions, and rolling upgrades with server, never api."
sidebar:
  order: 12
sources:
  - apps/server/internal/dbclock
  - apps/server/internal/health
  - apps/server/internal/db/db.go
  - apps/server/internal/db/migrate.go
---

Vantigo runs on Kubernetes as the same image it runs as under Compose: one tenant is one
installation, with its own database, and the pods are the commands of the one binary. This
page is what a Deployment needs from the application to keep serving while its pods and the
cluster's nodes are replaced. The [installation guide](/en/admin/installation/) remains the
reference for every setting; this page adds what is particular to a cluster.

## Preconditions

- A release is migrated once, by a Job that runs `vantigo migrate` with
  `MIGRATIONS_DATABASE_URL`, the schema owner's role ([Database
  roles](/en/admin/installation/#database-roles)).
- The serving pods run `server`, the workers run `worker`, and nothing runs `api`
  ([Rolling upgrades](#rolling-upgrades)).
- `MANAGEMENT_PORT` is set on every pod, so each one has the private listener the probes
  use ([Management listener](/en/admin/management-listener/)).

## Probes on the management port

The public listener only answers a `Host` that is `APP_URL`'s, so a kubelet's `httpGet`
probe, which sends the pod's address, gets `400` there. The management listener has no host
filter and serves `GET /health/live` and `GET /health/ready` without a token, with the same
checks as the public listener ([Health probes](/en/admin/installation/#health-probes)), in
every command — `server` and `worker` alike. Set `MANAGEMENT_PORT` (for example `9090`); a
`MANAGEMENT_TOKEN` is needed only for the control plane's own routes, not for the probes.

```yaml
ports:
  - name: management
    containerPort: 9090
startupProbe:
  httpGet: { path: /health/ready, port: management }
  periodSeconds: 5
  failureThreshold: 720 # an hour: as long as a migration may take
readinessProbe:
  httpGet: { path: /health/ready, port: management }
  periodSeconds: 5
  failureThreshold: 2
livenessProbe:
  httpGet: { path: /health/live, port: management }
  periodSeconds: 10
  failureThreshold: 6
```

Use a `startupProbe` with a long failure threshold rather than a large
`initialDelaySeconds`. A `server` or `worker` pod that starts before the migration Job has
finished waits for the schema and does nothing else ([Rolling upgrades](#rolling-upgrades)):
it is alive, and its readiness fails with `schema` for as long as the migration takes. The
liveness probe does not touch the database, so a slow database never restarts a pod.

Never publish the management port and never route it from an Ingress. Admit it in a
NetworkPolicy only from the control plane's namespace; the kubelet's probes come from the
node.

## Drain and the grace period

On SIGTERM a pod:

1. fails its readiness at once, with `draining` in `failing`;
2. keeps serving for `SHUTDOWN_DRAIN_DELAY`, so the endpoints controller and the ingress take
   it out of rotation while it still answers what reaches it;
3. closes every live-update socket with `1001`, so each browser reconnects — with a random
   delay of its own — to another pod;
4. stops its workers and drains its in-flight requests, then closes its connections, all
   within one `SHUTDOWN_TIMEOUT`.

`SHUTDOWN_DRAIN_DELAY` is `0s` by default, which suits Compose; set it to about `10s` on
Kubernetes. Then give the pod enough time for both:

```text
terminationGracePeriodSeconds ≥ SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT + 5s
```

With `SHUTDOWN_DRAIN_DELAY=10s` and the default `SHUTDOWN_TIMEOUT=30s`, that is `45`. A
shorter grace period lets the kubelet's SIGKILL cut an operation short, which the next
attempt then repeats. A second SIGTERM during the drain ends the process at once.

To verify, delete a pod while you follow its log: `shutdown signal received` comes first,
then `draining` after the delay, then `shutdown complete`.

## Replicas

- **`server`**, the serving pods: at least two for a tenant that must stay up while a node
  is replaced, spread across nodes and zones, with a PodDisruptionBudget of
  `minAvailable: 1`. `server` never migrates and never runs a worker. Live updates reach
  every replica through PostgreSQL, so a browser can be on any of them.
- **`worker`**, the background workers: two is safe. Every worker either takes an advisory
  lock, so it runs on one pod per cycle, or takes one row at a time under a time-limited
  lease, so any number of pods share the work without doing it twice ([Background workers
  and scaling](/en/admin/installation/#background-workers-and-scaling)).

Each pod opens its own database pool, sized with `pool_max_conns` on `DATABASE_URL`; count
every replica in the [connection budget](/en/admin/installation/#database-connection-budget).

## The clock

Leases, claims and rate-limit windows are computed in each process and compared with ones
another process wrote, so every pod must read the same time. Each process therefore follows
the database's clock, not its node's: it measures the difference with
`SELECT clock_timestamp()`, corrected for half the round trip, before any worker starts and
every minute after. The first measurement applies in full; later ones move the clock by at
most 100 milliseconds a minute, toward the median of the last five, so one slow round trip
cannot jump it, and the clock never runs backwards.

The difference is the metric `vantigo.clock.offset`, in seconds, exported with the other
metrics ([Email and observability](/en/admin/installation/#email-and-observability)). A
difference beyond `CLOCK_MAX_SKEW` (`5s` by default) logs
`clock: this host's clock is off from the database's` at warning, at most once an hour. It
does not fail readiness — the pod is already on the right time — but it says that the node's
time synchronisation is broken. If the first measurement fails, the process starts on its
own clock, logs it, and takes the database's as soon as a measurement succeeds.

## UTC database sessions

Every connection the application opens — the pool, the migration connection and the live
updates' own connections — sets the session's `TimeZone` to `UTC`, replacing any
`TimeZone` the connection string sets. Stored times are instants whatever the zone, but a
day boundary a query draws depends on the session's zone, and a managed PostgreSQL may
default to another one. A workspace's own time zone, set under Workspace admin, is applied
by the application where a rule or a screen names it, never by the session. There is
nothing to configure.

## Rolling upgrades

1. Run the release's migration Job, `vantigo migrate`, and wait for it to succeed.
2. Move the `server` and `worker` Deployments to the new image.

A new pod that starts before the Job has finished waits for it: until the database holds at
least the migrations the binary carries, it runs nothing but its probes, polls every 2 seconds
and logs `waiting for the schema to be migrated` every 30. Its readiness fails with `schema`
meanwhile, so the rollout stops by itself, and does not go on if the Job fails. A pod of the
previous release on the newer schema starts at once: a release's schema works with the
previous release's binary.

Run `server`, never `api`, for every replica that may be restarted during a rollout. `api`
migrates before it serves, and an old `api` pod restarted on a newer schema would ask to
migrate a database with versions it does not know.

## What can go wrong

| Symptom | Cause |
| --- | --- |
| Every probe answers `400` | The probes point at `PORT`, the public listener, which refuses the pod's address. Point them at the management port |
| A new pod never becomes ready, its readiness failing with `schema` | The migration Job has not run or has failed. Its log says why |
| Pods never become ready, readiness failing with `storage` | The bucket does not exist or the credentials are refused ([Object storage](/en/admin/object-storage/#the-readiness-check)). The operator creates the bucket; the app never does |
| Users see a dropped connection on every rollout | `SHUTDOWN_DRAIN_DELAY` is `0s`, or the grace period is shorter than the drain needs |
| `clock: this host's clock is off from the database's` in the log | The node's NTP is broken. The pod keeps the database's time; fix the node |
