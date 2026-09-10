// Command vantigo is the container's entrypoint and the application's only
// composition root: the one place that reads the environment, opens the pool
// and assembles every collaborator. Nothing below it reads os.Getenv.
//
// It is also the dispatch table — one image, several commands:
//
//	api          migrate under the advisory lock, then serve SPA + API + health
//	server       serve only; never migrates (what replicas run)
//	worker       background work plus health (health only until workers exist)
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
	"syscall"
	"time"

	// The distroless image has no zoneinfo; embed it so time.LoadLocation
	// works for every zone, not just UTC.
	_ "time/tzdata"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/telemetry"
	"github.com/vantigo-io/vantigo/server/internal/web"
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
		logger.Info("nothing to seed yet: no modules are installed")
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

	signals, shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Options{Version: buildinfo.Version, Environment: string(cfg.Env)})
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
	return serve(ctx, logger, cfg, ln, m != modeWorker)
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

// serve builds the handler for this command and runs it on ln until ctx is
// cancelled. withAPI=false is worker mode: health only.
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, ln net.Listener, withAPI bool) int {
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
	command := "worker"
	if withAPI {
		command = "api"
		assets := web.Assets()
		index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
		if err != nil {
			logger.Error("startup failed", "error", err)
			return 1
		}
		handler = telemetry.HTTPHandler(server.New(server.Options{
			Config: cfg,
			Logger: logger,
			Index:  index,
			Assets: assets,
			Health: healthHandler,
		}), cfg.BasePath)
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
	return serveUntilDone(ctx, logger, srv, ln, cfg.ShutdownTimeout)
}

// serveUntilDone serves until ctx is cancelled, then drains in-flight
// requests for up to timeout. The orchestrator's termination grace period
// must exceed it.
func serveUntilDone(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, timeout time.Duration) int {
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
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("in-flight requests did not finish in time", "error", err)
		return 1
	}
	logger.Info("shutdown complete")
	return 0
}
