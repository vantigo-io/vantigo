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
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	// The distroless image has no zoneinfo; embed it so time.LoadLocation
	// works for every zone, not just UTC.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/telemetry"
	"github.com/vantigo-io/vantigo/server/internal/web"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const usage = "usage: vantigo <api|server|worker|migrate|seed|healthcheck>"

type mode int

const (
	modeUsage mode = iota
	modeAPI
	modeServer
	modeWorker
	modeMigrate
	modeSeed
	modeHealthcheck
)

// readHeaderTimeout is the slowloris brake. There is deliberately no
// whole-request read or write timeout: attachment uploads and downloads
// stream, and the overall budget belongs to the proxy in front.
const (
	readHeaderTimeout = 15 * time.Second
	idleTimeout       = 120 * time.Second
)

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
	}

	cfg, err := config.FromOS()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := telemetry.NewLogger(stdout, cfg.LogLevel, false)
	slog.SetDefault(logger)

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
		logger.Info("nothing to seed: identity has no development seed")
		return 0
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
	signals, shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Options{Version: buildinfo.Version, Environment: string(cfg.Env), Command: raw})
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
		logger = telemetry.NewLogger(stdout, cfg.LogLevel, true)
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
// startup than api mode already did.
func moduleDeps(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) (module.Deps, *identity.Access, error) {
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
		Clock:   time.Now,
		Mail:    sender,
		Secrets: box,
		Limiter: ratelimit.New(pool),
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
	}
}

// serve builds the handler for this command and runs it on ln until ctx is
// cancelled, starting every enabled module's background workers first when
// runWorkers(m, cfg) says to. extraWorkers is appended to whatever
// module.Workers resolves from the real module list; production code always
// passes nil — it exists so a test can prove serve really starts and stops
// what it is handed, without a real module implementing Worker yet (nothing
// does before Tasks 11-13).
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, ln net.Listener, m mode, extraWorkers ...worker.Worker) int {
	defer func() { _ = ln.Close() }()

	// Startup is not cancelled by the shutdown signal: a SIGTERM during boot
	// should produce a process that came up and then drained, not a half-built one.
	pool, err := db.Open(context.WithoutCancel(ctx), cfg.DatabaseURL)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return 1
	}
	defer pool.Close()

	healthHandler := health.Handler(logger, buildinfo.Version, health.Check{Name: "postgres", Run: pool.Ping})
	handler := healthHandler
	command := commandName(m)

	withAPI := m != modeWorker
	wantWorkers := runWorkers(m, cfg)

	var deps module.Deps
	var access *identity.Access
	var haveDeps bool

	if withAPI {
		assets := web.Assets()
		index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}

		deps, access, err = moduleDeps(cfg, pool, logger)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		haveDeps = true

		// Every module this binary knows is passed to Compose, which keeps
		// identity — always mounted, never listed in MODULES — plus whichever
		// of the rest MODULES enables, and injects customers' customer
		// directory into every enabled module's Deps. A disabled module
		// contributes no route, no permission and no contract path.
		api, err := module.Compose(deps, businessModules(access)...)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		// identity.RunStartup runs on every host start (api after migrating,
		// server on each replica), so it gets the same "finish, don't abort"
		// treatment as opening the pool above: context.WithoutCancel(ctx), so a
		// SIGTERM during boot cannot leave the bootstrap grant half-applied.
		if err := identity.RunStartup(context.WithoutCancel(ctx), deps); err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}

		handler = telemetry.HTTPHandler(server.New(server.Options{
			Config: cfg,
			Logger: logger,
			Index:  index,
			Assets: assets,
			Health: healthHandler,
			API:    api,
		}), cfg.BasePath)
	}

	var runner *worker.Runner
	if wantWorkers {
		if !haveDeps {
			deps, access, err = moduleDeps(cfg, pool, logger)
			if err != nil {
				logger.Error("startup failed", "error", err)
				return 1
			}
		}
		workers := append(module.Workers(deps, businessModules(access)...), extraWorkers...)
		runner = worker.NewRunner(logger)
		runner.Start(ctx, workers)
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
	if cfg.AllowInsecureTransport {
		logger.Warn("ALLOW_INSECURE_TRANSPORT=1: plaintext HTTP, database and SMTP transport are accepted; local and evaluation use only")
	}
	return serveUntilDone(ctx, logger, srv, ln, cfg.ShutdownTimeout, runner)
}

// serveUntilDone serves until ctx is cancelled, then drains in-flight
// requests and waits for runner's workers (if any) to stop, both bounded by
// the same timeout — the orchestrator's termination grace period must
// exceed it — and run concurrently rather than one after the other, so a
// slow drain and a slow worker cannot each eat into the other's share of
// the budget. runner may be nil (server mode, or api with
// WORKERS_IN_PROCESS=0): Runner.Wait on a nil *Runner returns immediately.
func serveUntilDone(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, timeout time.Duration, runner *worker.Runner) int {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
		}
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received; draining", "timeout", timeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var wg sync.WaitGroup
	var httpErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		httpErr = srv.Shutdown(shutdownCtx)
	}()
	workersErr := runner.Wait(shutdownCtx)
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
