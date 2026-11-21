// Command vantigo is the container's entrypoint and the application's only
// composition root: the one place that reads the environment, opens the pool
// and assembles every collaborator. Nothing below it reads os.Getenv.
//
// It is also the dispatch table — one image, several commands:
//
//	api          migrate under the advisory lock, then serve SPA + API + health,
//	             plus every enabled module's background workers when
//	             WORKERS_IN_PROCESS=1 (the default)
//	server       serve only; never migrates and never runs workers (what replicas run)
//	worker       every enabled module's background workers, plus health
//	migrate      apply migrations and exit 0/1
//	seed         development data (APP_ENV=development only)
//	healthcheck  probe this container's /health/ready and exit 0/1
//	storage copy copy every object between an fs root and the s3 bucket, then exit
//	openapi integrator
//	             write the integrator's API document, every module enabled, to
//	             stdout (CI's breaking-change check); reads no environment
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	// The distroless image has no zoneinfo; embed it so time.LoadLocation
	// works for every zone, not just UTC.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/dbclock"
	"github.com/vantigo-io/vantigo/server/internal/docs"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/planning"
	"github.com/vantigo-io/vantigo/server/internal/products"
	"github.com/vantigo-io/vantigo/server/internal/projects"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/telemetry"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
	"github.com/vantigo-io/vantigo/server/internal/web"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const usage = "usage: vantigo <api|server|worker|migrate|seed|healthcheck|storage copy|openapi integrator>"

type mode int

const (
	modeUsage mode = iota
	modeAPI
	modeServer
	modeWorker
	modeMigrate
	modeSeed
	modeHealthcheck
	modeOpenAPIIntegrator
	modeStorageCopy
)

// readHeaderTimeout is the slowloris brake. There is deliberately no
// whole-request read or write timeout: attachment uploads and downloads
// stream, and the overall budget belongs to the proxy in front.
const (
	readHeaderTimeout = 15 * time.Second
	idleTimeout       = 120 * time.Second
)

// listenManagement opens the management listener. A variable so a test can
// bind an ephemeral loopback port instead of MANAGEMENT_PORT.
var listenManagement = func(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf(":%d", port))
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// parseArgs reads nothing but its arguments, which is what makes the
// dispatch table testable. It returns the raw command for error messages.
func parseArgs(args []string) (mode, string) {
	if len(args) == 0 {
		return modeUsage, ""
	}
	switch args[0] {
	case "api":
		return modeAPI, args[0]
	case "server":
		return modeServer, args[0]
	case "worker":
		return modeWorker, args[0]
	case "migrate":
		return modeMigrate, args[0]
	case "seed":
		return modeSeed, args[0]
	case "healthcheck":
		return modeHealthcheck, args[0]
	case "storage":
		if len(args) >= 2 && args[1] == "copy" {
			return modeStorageCopy, args[0]
		}
		return modeUsage, strings.Join(args, " ")
	case "openapi":
		if len(args) == 2 && args[1] == "integrator" {
			return modeOpenAPIIntegrator, args[0]
		}
		return modeUsage, strings.Join(args, " ")
	default:
		return modeUsage, args[0]
	}
}

// run is main's testable body: it returns the exit code instead of exiting.
func run(args []string, stdout, stderr io.Writer) int {
	m, raw := parseArgs(args)
	switch m {
	case modeUsage:
		// An unknown command must fail loudly: a typo in a Kubernetes Job must
		// not quietly become a web server that never completes.
		if raw != "" {
			_, _ = fmt.Fprintf(stderr, "unknown command %q\n", raw)
		}
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	case modeHealthcheck:
		// Constructs nothing: a liveness probe must not fail because
		// DATABASE_URL is wrong — that is /health/ready's job to report.
		return healthcheck(os.Getenv("PORT"), stderr)
	case modeOpenAPIIntegrator:
		// Constructs nothing either: CI runs it with no database and no
		// configuration, and the document depends on neither.
		return openAPIIntegrator(stdout, stderr)
	}

	cfg, err := config.FromOS()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := telemetry.NewLogger(stdout, cfg.LogLevel, false, cfg.TenantID)
	slog.SetDefault(logger)
	logRenamedVariables(logger, cfg)

	// Installed before migrate() runs so a SIGTERM that arrives mid-migration
	// waits for it to finish instead of killing the process outright (Go's
	// default disposition for an unhandled SIGTERM is immediate termination).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// The first signal requests a graceful stop; once it arrives the default
	// disposition is restored so a second signal terminates immediately — an
	// operator's way out of a slow drain or a long migration.
	go func() {
		<-ctx.Done()
		stop()
	}()

	switch m {
	case modeMigrate:
		return migrate(logger, cfg)
	case modeSeed:
		if !cfg.IsDevelopment() {
			_, _ = fmt.Fprintln(stderr, "the seed command is only available with APP_ENV=development")
			return 2
		}
		return seed(ctx, logger, cfg, stdout)
	case modeStorageCopy:
		return storageCopy(ctx, cfg, args[2:], stdout, stderr)
	case modeAPI:
		if code := migrate(logger, cfg); code != 0 {
			return code
		}
		if ctx.Err() != nil {
			logger.Info("shutdown requested during migration; exiting without serving")
			return 0
		}
	}

	// raw is "api", "server" or "worker" here: every other command has returned.
	signals, shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Options{Version: buildinfo.Version, Environment: string(cfg.Env), Command: raw, TenantID: cfg.TenantID})
	if err != nil {
		logger.Error("telemetry setup failed", "error", err)
		return 1
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry did not flush", "error", err)
		}
	}()
	if signals.Logs {
		logger = telemetry.NewLogger(stdout, cfg.LogLevel, true, cfg.TenantID)
		slog.SetDefault(logger)
	}
	logger.Info("telemetry", "traces", signals.Traces, "metrics", signals.Metrics, "logs", signals.Logs)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		logger.Error("cannot listen", "port", cfg.Port, "error", err)
		return 1
	}
	return serve(ctx, logger, cfg, ln, m)
}

// openAPIIntegrator writes the integrator's document (API access design K37)
// for every module this binary knows, all of them enabled.
func openAPIIntegrator(stdout, stderr io.Writer) int {
	body, err := module.IntegratorDocument(businessModules(nil)...)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	out.WriteByte('\n')
	if _, err := out.WriteTo(stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func healthcheck(port string, stderr io.Writer) int {
	if port == "" {
		port = "8080"
	}
	if err := health.Probe(context.Background(), "http://127.0.0.1:"+port); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// logRenamedVariables names, in one line, the variables the operator still
// sets under a name that was renamed: those read as aliases for one release,
// and those ignored because the new name is set as well. It says nothing when
// only current names are in use.
func logRenamedVariables(logger *slog.Logger, cfg *config.Config) {
	if len(cfg.Renamed) == 0 {
		return
	}
	names := make([]string, 0, len(cfg.Renamed))
	for _, r := range cfg.Renamed {
		note := r.Old + " -> " + r.New
		if r.Ignored {
			note += " (ignored)"
		}
		names = append(names, note)
	}
	logger.Warn("config: these variables were renamed and are read as aliases for one more release; set the new names instead", "variables", names)
}

// migrate runs on context.Background: aborting DDL halfway on SIGTERM is
// worse than being killed after the grace period. run installs the signal
// handler before calling this precisely so that SIGTERM, instead of the
// unhandled-signal default of killing the process immediately, waits for
// this call to return.
func migrate(logger *slog.Logger, cfg *config.Config) int {
	if err := db.ApplyMigrations(context.Background(), cfg.MigrationsDatabaseURL); err != nil {
		logger.Error("migration failed", "error", err)
		return 1
	}
	return 0
}

// commandName is m's label in logs and in the "worker"/"api" mode this file
// otherwise only tracks as a bool: server always builds the same handler as
// api (withAPI below), so the label is the one place they are told apart.
func commandName(m mode) string {
	switch m {
	case modeAPI:
		return "api"
	case modeServer:
		return "server"
	default:
		return "worker"
	}
}

// runWorkers reports whether m should start every enabled module's
// background workers: always in worker mode, in api mode only when
// WORKERS_IN_PROCESS=1, and never in server mode regardless of that
// setting — server is "what replicas run" (design §3.2), and a fleet of
// server replicas all claiming the same outbox row would defeat the
// conditional-update claim Tasks 11-13 build on.
func runWorkers(m mode, cfg *config.Config) bool {
	switch m {
	case modeWorker:
		return true
	case modeAPI:
		return cfg.WorkersInProcess
	default:
		return false
	}
}

// moduleDeps assembles the module.Deps and identity's Access every command
// that needs a module — Compose (api, server) or the worker runner (worker,
// or api with WORKERS_IN_PROCESS=1) — shares. Building it is cheap: neither
// mail.New nor secrets.New dials out, so worker-only mode pays no more at
// startup than api mode already did. clock is Deps.Clock, and the rate
// limiter's windows follow it too: serve passes the database-anchored clock
// (Kubernetes readiness design KR7), the seed and the tests time.Now.
func moduleDeps(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger, clock func() time.Time) (module.Deps, *identity.Access, error) {
	sender, err := mail.New(cfg, logger)
	if err != nil {
		return module.Deps{}, nil, err
	}
	box, err := secrets.New(cfg.AppSecret)
	if err != nil {
		return module.Deps{}, nil, err
	}
	deps := module.Deps{
		Config:  cfg,
		Pool:    pool,
		Logger:  logger,
		Clock:   clock,
		Mail:    sender,
		Secrets: box,
		Limiter: ratelimit.NewWithClock(pool, clock),
	}
	access := identity.NewAccess(deps)
	deps.Access = access
	return deps, access, nil
}

// businessModules is every module this binary knows, identity included —
// the same list Compose mounts and module.Workers resolves background
// workers from. A disabled module (MODULES) contributes no route, no
// permission, no contract path and, as of this task, no worker.
func businessModules(access *identity.Access) []module.Module {
	return []module.Module{
		identity.Module(access),
		customers.Module(),
		products.Module(),
		energy.Module(),
		communications.Module(),
		projects.Module(),
		timetracking.Module(),
		expenses.Module(),
		invoices.Module(),
		staff.Module(),
		planning.Module(),
		inventory.Module(),
		orders.Module(),
		manufacturing.Module(),
		accounting.Module(),
		purchasing.Module(),
		knowledge.Module(),
		calendar.Module(),
		entra.Module(),
		google.Module(),
		meta.Module(),
		assistant.Module(),
		agents.Module(),
	}
}

// runBounded calls fn and reports whether it returned within timeout. There
// is no way to cancel an arbitrary func, so fn keeps running in the
// background if it does not return in time; runBounded only stops whoever
// is waiting on it from hanging forever, it does not stop fn itself.
func runBounded(fn func(), timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// closeFloor is the least time a deferred close gets, even once the shared
// shutdown budget is spent: closing the pool and the rate limiter's last
// flush are worth a moment more, and a zero bound would only log a timeout
// and skip them.
const closeFloor = 500 * time.Millisecond

// closePool closes pool but never blocks the process past timeout.
// pgxpool.Pool.Close has no context parameter and blocks until every
// checked-out connection is returned to the pool. serve already cancels and
// (bounded, by this same timeout) waits for every worker before this defer
// runs, so by the time it does, the only way a connection could still be
// checked out is a worker that ignored cancellation entirely — a bug worker
// code must not have, but not one this must hang the whole process over:
// the close keeps running in the background, and the process is exiting
// regardless.
func closePool(pool *pgxpool.Pool, timeout time.Duration, logger *slog.Logger) {
	if !runBounded(pool.Close, timeout) {
		logger.Error("database pool did not close in time; a worker likely outlived the shutdown timeout and is still using it", "timeout", timeout)
	}
}

// serve builds the handler for this command and runs it on ln until ctx is
// cancelled, starting every enabled module's background workers first when
// runWorkers(m, cfg) says to. extraModules is appended to businessModules
// only when resolving workers (module.Workers) — never passed to Compose,
// which would fail on the duplicate module name. Production code always
// passes none; a test passes a module that declares a worker, so it is
// resolved through the same module.Workers call serve itself makes rather
// than a bypass that only proves a worker can run once one is already in
// hand.
//
// In server and worker mode it first waits for the schema (Kubernetes
// readiness design KR4): nothing that reads or writes the database's tables
// runs until the database is at least at the binary's latest migration,
// while the management listener already answers the probes.
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, ln net.Listener, m mode, extraModules ...module.Module) int {
	defer func() { _ = ln.Close() }()

	// lc's budget is the one SHUTDOWN_TIMEOUT every deferred close below
	// shares (KR5). Deferred first, its release runs last.
	lc := newLifecycle(cfg.ShutdownDrainDelay, cfg.ShutdownTimeout)
	defer lc.release()

	// Startup is not cancelled by the shutdown signal: a SIGTERM during boot
	// should produce a process that came up and then drained, not a half-built one.
	pool, err := db.Open(context.WithoutCancel(ctx), cfg.DatabaseURL)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return 1
	}
	// closePool (not a bare pool.Close) is deferred here, before
	// cancelWorkers below, so — deferred calls run in reverse order —
	// cancelWorkers and the bounded worker wait it triggers always run
	// BEFORE this one, on every return path out of serve, early failures
	// included. Closing the pool out from under a worker mid-query is
	// exactly the failure that looks like an unrelated driver bug later;
	// ordering this by defer, rather than by hand in every branch that
	// returns, keeps the ordering true regardless of how serve returns. It
	// gets what is left of the shutdown budget.
	defer func() { closePool(pool, max(lc.remaining(), closeFloor), logger) }()
	// The pool's gauges (Kubernetes readiness KR23); a failure costs only them.
	if stopPoolMetrics, err := telemetry.ObservePool(pool); err != nil {
		logger.Warn("the database pool metrics are not reported", "error", err)
	} else {
		defer stopPoolMetrics()
	}

	// Deps.Clock is anchored to the database's clock (KR7, KR8). It is built
	// here and synced once the management listener is up, below: until the
	// sync nothing that reads it runs except the listener's own routes.
	clock := dbclock.New(dbclock.Options{Measure: dbclock.PoolMeasurer(pool), MaxSkew: cfg.ClockMaxSkew, Logger: logger})

	storageCheck, err := storageReadiness(cfg)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return 1
	}
	checks := append(append([]health.Check{{Name: "postgres", Run: pool.Ping}}, storageCheck...), lc.checks()...)
	healthHandler := health.Handler(logger, buildinfo.Version, checks...)
	handler := healthHandler
	command := commandName(m)

	withAPI := m != modeWorker
	wantWorkers := runWorkers(m, cfg)

	// Every mode that reaches serve needs the modules: api and server to
	// compose them, worker to resolve their background workers. Building
	// deps is cheap (moduleDeps), so it is built once, unconditionally,
	// rather than in whichever branch first turns out to need it.
	deps, access, err := moduleDeps(cfg, pool, logger, clock.Now)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return 1
	}
	mods := businessModules(access)
	// The rate limiter's flusher adds what it counted in process to the
	// shared rows once more before the pool closes (API access design K24).
	// Deferred after closePool's, it runs before it, and after the requests
	// have drained, within what is left of the shutdown budget.
	defer func() {
		if !runBounded(deps.Limiter.Close, max(lc.remaining(), closeFloor)) {
			logger.Warn("the rate limiter's last flush did not finish within the shutdown timeout")
		}
	}()

	// The live hub (live design L1), in api and server mode only: a worker
	// notifies through Postgres and needs none. liveHub is what the
	// management status reads: the listener is up before the hub exists.
	var hub *live.Hub
	var liveHub atomic.Pointer[live.Hub]

	// The management listener opens in every command (KR2) as soon as the
	// database answers — before the clock sync, the schema wait and any
	// worker — and serves at once: its
	// probes answer while the process waits (KR4), its readiness failing
	// with "schema" and then "startup" until serve is really serving. A bind
	// failure (the port is taken) returns while nothing is running yet.
	// It is private and separate on purpose: it has no host filter, so a
	// control plane can reach it by Service name and a kubelet by pod
	// address, and the public listener never serves it. Registered after
	// closePool's defer, its shutdown runs first: its handlers use the pool.
	if cfg.Management != nil {
		mgmtLn, err := listenManagement(cfg.Management.Port)
		if err != nil {
			logger.Error("cannot listen on the management port", "port", cfg.Management.Port, "error", err)
			return 1
		}
		var mgmtLive func(context.Context) management.Live
		if withAPI && cfg.LiveEnabled {
			mgmtLive = liveHealth(&liveHub)
		}
		mgmt := &http.Server{
			Handler: management.Handler(management.Options{
				Logger:  logger,
				Version: buildinfo.Version,
				Token:   cfg.Management.Token,
				Health:  healthHandler,
				Installation: func(ctx context.Context) (management.Installation, error) {
					st, err := identity.ReadInstallationStatus(ctx, deps)
					return management.Installation{Bootstrap: st.Bootstrap, Users: st.Users, ActiveUsers: st.ActiveUsers}, err
				},
				DatabaseBytes: management.DatabaseBytes(pool),
				Deliveries:    events.NewOperator(pool, deps.Clock),
				Live:          mgmtLive,
			}),
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		}
		go func() {
			if err := mgmt.Serve(mgmtLn); !errors.Is(err, http.ErrServerClosed) {
				logger.Error("management listener stopped unexpectedly", "error", err)
			}
		}()
		defer func() {
			if err := mgmt.Shutdown(lc.budget()); err != nil {
				logger.Warn("management listener did not drain", "error", err)
			}
		}()
		logger.Info("management listener is up", "addr", mgmtLn.Addr().String())
	}

	// The first measurement applies in full here, before anything else reads
	// the clock, and later ones every minute. A first measurement that fails
	// leaves the local clock until one succeeds; it is no reason not to
	// start. Registered after the management listener's defer, the clock
	// stops before it.
	if err := clock.Sync(context.WithoutCancel(ctx)); err != nil {
		logger.Warn("clock: could not measure the database's clock; using this host's until it can", "error", err)
	}
	clockCtx, stopClock := context.WithCancel(context.WithoutCancel(ctx))
	clockDone := make(chan struct{})
	go func() {
		defer close(clockDone)
		clock.Run(clockCtx)
	}()
	defer func() {
		stopClock()
		<-clockDone
	}()

	// api has just migrated under the advisory lock; server and worker wait
	// for whoever migrates (KR4). The wait honours the signal: a pod stopped
	// while it waits exits without having started anything.
	if m == modeAPI {
		lc.schema.Store(true)
	} else {
		want, err := db.LatestMigration()
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		if err := db.WaitForSchema(ctx, db.SchemaWait{
			Read:   func(ctx context.Context) (int64, error) { return db.SchemaVersion(ctx, pool) },
			Want:   want,
			Logger: logger,
		}); err != nil {
			if ctx.Err() != nil {
				logger.Info("shutdown requested while waiting for the schema; exiting without serving")
				return 0
			}
			logger.Error("startup failed", "error", err)
			return 1
		}
		lc.schema.Store(true)
	}

	// Registered after closePool's defer, the hub's wait runs first:
	// Authorize reads the pool.
	defer func() {
		if hub == nil {
			return
		}
		if err := hub.Wait(lc.budget()); err != nil {
			logger.Error("the live hub did not shut down in time", "error", err)
		}
	}()

	// closeSockets stops the hub, which closes every live socket with 1001.
	// serveUntilDone calls it after the drain delay (KR5), so browsers
	// reconnect to another pod only once this one is out of rotation.
	closeSockets := func() {}

	var apiRunners []module.Runner
	if withAPI {
		assets := web.Assets()
		access.UseBrandAssets(assets)

		// Every module this binary knows is passed to Compose, which keeps
		// identity — always mounted, never listed in MODULES — plus whichever
		// of the rest MODULES enables, and injects customers' customer
		// directory into every enabled module's Deps. A disabled module
		// contributes no route, no permission and no contract path — only its
		// customer reference holder, since its schema is there regardless.
		if cfg.LiveEnabled {
			deps.Live = live.NewRegistry() // Compose fills it with the enabled modules' topics
		}
		api, runners, err := module.ComposeAPI(deps, mods...)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		apiRunners = runners
		// The API request log's writer runs in the API process only (API
		// access design K35). Deferred after closePool's, it stops first:
		// once serveUntilDone has drained the requests, it writes what they
		// queued while the pool is still open.
		defer access.StartRequestLog()()
		// identity.RunStartup runs on every host start (api after migrating,
		// server on each replica), so it gets the same "finish, don't abort"
		// treatment as opening the pool above: context.WithoutCancel(ctx), so a
		// SIGTERM during boot cannot leave the bootstrap grant half-applied.
		if err := identity.RunStartup(context.WithoutCancel(ctx), deps); err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}

		// The index is templated once the API is composed, because what it
		// injects depends on the default brand: the environment's branding
		// stays honoured until the brand has imported it, and is dropped from
		// the page once it has (brands design B17, B30).
		imported, err := identity.BrandingImported(context.WithoutCancel(ctx), deps)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		var index *web.Index
		if imported {
			index, err = web.NewImportedIndex(assets, cfg.BasePath, cfg.Modules)
		} else {
			index, err = web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)
		}
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}

		// The hub runs on a context of its own, not a child of the shutdown
		// signal's (KR5): the signal flips readiness, and the sockets stay
		// open through the drain delay, until serveUntilDone calls
		// closeSockets; srv.Shutdown does not touch hijacked connections
		// (L12). The deferred stopHub stops it on a return before serving
		// too, or the deferred Wait above would wait for nothing.
		var liveHandler http.Handler
		if cfg.LiveEnabled {
			liveURL := cfg.LiveDatabaseURL
			if liveURL == "" {
				liveURL = cfg.DatabaseURL
			}
			hubCtx, stopHub := context.WithCancel(context.WithoutCancel(ctx))
			defer stopHub()
			closeSockets = stopHub
			hub = live.New(live.Options{
				DatabaseURL: liveURL,
				Registry:    deps.Live,
				Access:      access,
				Limiter:     deps.Limiter,
				AppOrigin:   cfg.AppOrigin,
				Logger:      logger,
				Clock:       deps.Clock,
			})
			hub.Start(hubCtx)
			liveHub.Store(hub)
			liveHandler = hub
		}

		handler = telemetry.HTTPHandler(server.New(server.Options{
			Config: cfg,
			Logger: logger,
			Docs:   docs.Assets(),
			Index:  index,
			Assets: assets,
			Health: healthHandler,
			API:    api,
			Live:   liveHandler,
		}), cfg.BasePath)
	}

	// workerCtx is cancelled explicitly (cancelWorkers), never by the signal
	// itself: on the graceful path serveUntilDone cancels it after the drain
	// delay (KR5), and on an early srv.Serve failure, which no signal
	// accompanies, it cancels it at once, so every worker is told to stop
	// before the deferred closePool runs.
	workerCtx, cancelWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWorkers()

	// The API-process runners start here, after Compose has bound the
	// operations dispatcher they call through, on the same context as the
	// workers; the deferred wait cancels it first, so an early return cannot
	// strand them. serveUntilDone cancels and waits for the workers on the
	// normal path, and the runners return when the same context ends.
	waitRunners := module.StartRunners(workerCtx, logger, apiRunners)
	defer func() {
		cancelWorkers()
		waitRunners()
	}()

	var runner *worker.Runner
	if wantWorkers {
		workers := module.Workers(deps, append(mods, extraModules...)...)
		// The domain-events dispatcher (orders design O3) is the platform's
		// own worker, over every module's handlers, enabled or not.
		handlers, err := module.EventHandlers(deps, append(mods, extraModules...)...)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		workers = append(workers, events.NewDispatcher(pool, handlers, logger, deps.Clock))
		runner = worker.NewRunner(logger)
		runner.Start(workerCtx, workers)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	logger.Info("vantigo is listening",
		"version", buildinfo.Version, "command", command, "addr", ln.Addr().String(),
		"app_url", cfg.AppOrigin, "base_path", cfg.BasePath, "env", cfg.Env)
	lc.started.Store(true)
	return serveUntilDone(ctx, logger, srv, ln, lc, runner, cancelWorkers, closeSockets)
}

// serveUntilDone serves until ctx is cancelled or srv.Serve fails on its
// own, then either way cancels every worker (cancelWorkers) and waits for
// them (runner.Wait) before returning — so serve's deferred pool close never
// races a worker still using it.
//
// The graceful path (ctx cancelled) is the drain (Kubernetes readiness
// design KR5): readiness fails with "draining" at once; the process keeps
// serving for SHUTDOWN_DRAIN_DELAY, so endpoints controllers and the ingress
// take it out of rotation; then it closes the live sockets (closeSockets)
// and stops the workers, and drains in-flight HTTP requests concurrently
// with the worker wait. From there on everything — these two waits and
// serve's deferred closes after them — shares lc's one SHUTDOWN_TIMEOUT
// budget, so the orchestrator's grace period must exceed SHUTDOWN_DRAIN_DELAY
// plus SHUTDOWN_TIMEOUT. A second signal during any of it terminates at once
// (run restores the default disposition). runner may be nil (server mode, or
// api with WORKERS_IN_PROCESS=0): Runner.Wait on a nil *Runner returns
// immediately.
func serveUntilDone(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, lc *lifecycle, runner *worker.Runner, cancelWorkers, closeSockets func()) int {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
		}
		// srv.Serve failed on its own, not because ctx was cancelled: unlike
		// the graceful path below, nothing has told the workers to stop yet.
		// Do that now and wait (bounded) before returning, so serve's
		// deferred pool close does not run out from under a worker that is
		// still mid-query — the exact bug this branch used to leave open.
		closeSockets()
		cancelWorkers()
		if werr := runner.Wait(lc.budget()); werr != nil {
			logger.Error("workers did not finish before shutdown", "error", werr)
		}
		return 1
	case <-ctx.Done():
	}

	lc.draining.Store(true)
	logger.Info("shutdown signal received; readiness fails while the drain delay runs", "drain_delay", lc.delay, "timeout", lc.timeout)
	if lc.delay > 0 {
		time.Sleep(lc.delay)
	}

	logger.Info("draining")
	closeSockets()
	cancelWorkers()
	budget := lc.budget()

	var wg sync.WaitGroup
	var httpErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		httpErr = srv.Shutdown(budget)
	}()
	workersErr := runner.Wait(budget)
	wg.Wait()

	if httpErr != nil {
		logger.Error("in-flight requests did not finish in time", "error", httpErr)
		return 1
	}
	if workersErr != nil {
		logger.Error("workers did not finish in time", "error", workersErr)
		return 1
	}
	logger.Info("shutdown complete")
	return 0
}

// liveHealth is the hub's state for the management status. The listener
// opens before the hub starts, so until it has, the status says the hub's
// listener is down and its queue usage unknown.
func liveHealth(hub *atomic.Pointer[live.Hub]) func(context.Context) management.Live {
	return func(ctx context.Context) management.Live {
		h := hub.Load()
		if h == nil {
			return management.Live{Listener: "down", QueueUsage: -1}
		}
		st := h.Health(ctx)
		return management.Live{Listener: st.Listener, QueueUsage: st.QueueUsage, Connections: st.Connections}
	}
}
