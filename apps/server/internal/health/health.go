// Package health serves the two container probes and the client the
// `healthcheck` command uses to call them.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Check is one readiness dependency. pgxpool.Pool.Ping fits Run as is.
type Check struct {
	Name string
	Run  func(context.Context) error
}

// checkTimeout bounds each readiness check so a wedged dependency answers 503
// instead of hanging the probe. A variable so tests can shorten it.
var checkTimeout = 3 * time.Second

type report struct {
	Status  string   `json:"status"`
	Version string   `json:"version"`
	Failing []string `json:"failing,omitempty"`
}

// Handler serves GET /health/live — 200 whenever the process can answer, with
// no dependency at all — and GET /health/ready — 200 when every check passes,
// 503 naming the ones that failed. Both are anonymous and uncached.
func Handler(logger *slog.Logger, version string, checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, report{Status: "healthy", Version: version})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if failing := run(r.Context(), logger, checks); len(failing) > 0 {
			write(w, http.StatusServiceUnavailable, report{Status: "unhealthy", Version: version, Failing: failing})
			return
		}
		write(w, http.StatusOK, report{Status: "healthy", Version: version})
	})
	return mux
}

func run(ctx context.Context, logger *slog.Logger, checks []Check) []string {
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(checks))
	for _, c := range checks {
		go func() {
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			results <- result{c.Name, c.Run(cctx)}
		}()
	}
	var failing []string
	for range checks {
		if res := <-results; res.err != nil {
			logger.WarnContext(ctx, "readiness check failed", "check", res.name, "error", res.err)
			failing = append(failing, res.name)
		}
	}
	slices.Sort(failing)
	return failing
}

func write(w http.ResponseWriter, status int, body report) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Probe requests baseURL/health/ready once and returns nil for a 2xx. The
// image has no shell or curl, so its HEALTHCHECK runs `vantigo healthcheck`,
// which calls this against the process in the same container.
func Probe(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health: %s/health/ready answered %d", baseURL, resp.StatusCode)
	}
	return nil
}
