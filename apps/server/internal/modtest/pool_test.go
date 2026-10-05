package modtest

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// WithPoolMaxConns sizes the installation's own pool — the one every module's
// Deps carries and the harness's helpers use — so a third connection wanted
// while two are held waits: what makes the integration races starve when a
// call takes a pool connection under a lock (invoices work design D1).
func TestModtest_WithPoolMaxConnsSizesTheInstallationsPool(t *testing.T) {
	t.Parallel()
	var got module.Deps
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d
			return http.NotFoundHandler(), nil
		},
	}
	h := New(t, WithRecorder(contracttest.NewForModule("customers")), WithModule(capture), WithPoolMaxConns(2))
	if got.Pool != h.Pool() || h.Pool().Config().MaxConns != 2 {
		t.Fatalf("Deps.Pool = %p of %d, the harness's %p; want one pool of 2", got.Pool, got.Pool.Config().MaxConns, h.Pool())
	}

	ctx := context.Background()
	for range 2 {
		conn, err := h.Pool().Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer conn.Release()
	}
	wait, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if conn, err := h.Pool().Acquire(wait); !errors.Is(err, context.DeadlineExceeded) {
		if conn != nil {
			conn.Release()
		}
		t.Errorf("a third connection = %v, want it to wait to the deadline", err)
	}
}
