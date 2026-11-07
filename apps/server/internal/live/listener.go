package live

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dial opens one of the hub's two dedicated connections, never from the pool
// (L7): a held pool connection would starve it. The URL is parsed as the pool
// parses it, so a DATABASE_URL carrying pool_max_conns and its siblings —
// which a plain pgx.Connect would send to the server as runtime parameters,
// and fail on — still connects.
func (h *Hub) dial(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := pgxpool.ParseConfig(h.opts.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("live: parse the connection string: %w", err)
	}
	// TCP keepalive, so a peer that vanished without a FIN (a NAT, a
	// failover) is noticed by the kernel too, beside the listener's own
	// idle probe. connect_timeout is kept.
	dialer := &net.Dialer{
		Timeout:         cfg.ConnConfig.ConnectTimeout,
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Idle: 15 * time.Second, Interval: 5 * time.Second, Count: 3},
	}
	cfg.ConnConfig.DialFunc = dialer.DialContext
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return nil, fmt.Errorf("live: connect: %w", err)
	}
	return conn, nil
}

// listenOnce connects and LISTENs, and marks the listener up.
func (h *Hub) listenOnce(ctx context.Context) (*pgx.Conn, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := h.connect(connectCtx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(connectCtx, "LISTEN "+Channel); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("live: listen: %w", err)
	}
	h.setListener(true, conn.PgConn().PID())
	return conn, nil
}

// listen loops on WaitForNotification until ctx ends, reconnecting with
// backoff — 1 s doubling to 30 s, with jitter — whenever the connection is
// lost. While it is down every socket is degraded; its return resyncs them.
// A wait is bounded by listenIdle: a connection quiet that long is probed
// with SELECT 1, and one that does not answer within probe is treated as
// lost, so a stalled connection (no FIN, no error) cannot leave the hub deaf
// while it believes itself live.
func (h *Hub) listen(ctx context.Context, conn *pgx.Conn) {
	backoff := h.limits.backoffMin
	for {
		if conn == nil {
			var err error
			conn, err = h.listenOnce(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				h.logger.Warn("live: the listener could not reconnect", "error", err, "retry_in", backoff)
				if !sleep(ctx, jitter(backoff)) {
					return
				}
				backoff = min(backoff*2, h.limits.backoffMax)
				continue
			}
			backoff = h.limits.backoffMin
			h.logger.Info("live: the listener is up")
		}
		wctx, cancel := context.WithTimeout(ctx, h.limits.listenIdle)
		n, err := conn.WaitForNotification(wctx)
		cancel()
		if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) && !conn.IsClosed() {
			pctx, cancel := context.WithTimeout(ctx, h.limits.probe)
			_, perr := conn.Exec(pctx, "SELECT 1")
			cancel()
			if perr == nil {
				continue
			}
			err = fmt.Errorf("live: the listener's connection did not answer a probe: %w", perr)
		}
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = conn.Close(closeCtx)
			cancel()
			conn = nil
			if ctx.Err() != nil {
				return
			}
			h.logger.Warn("live: the listener lost its connection; sockets are degraded", "error", err)
			h.setListener(false, 0)
			continue
		}
		h.dispatch(n.Payload)
	}
}

// jitter is d give or take a fifth.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()*0.4-0.2)*float64(d))
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// publish sends one presence batch on the publisher connection, connecting
// it first when it is not open.
func (h *Hub) publish(ctx context.Context, body string) {
	h.pubMu.Lock()
	defer h.pubMu.Unlock()
	conn, err := h.publisherLocked(ctx)
	if err != nil {
		h.logger.Warn("live: the presence publisher could not connect", "error", err)
		return
	}
	if _, err := conn.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, body); err != nil {
		h.logger.Warn("live: could not publish presence", "error", err)
		h.dropPublisherLocked()
	}
}

// publisherLocked is the open publisher connection, opened on demand. h.pubMu
// is held.
func (h *Hub) publisherLocked(ctx context.Context) (*pgx.Conn, error) {
	if h.pub != nil && !h.pub.IsClosed() {
		return h.pub, nil
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := h.connect(connectCtx)
	if err != nil {
		return nil, err
	}
	h.pub = conn
	return conn, nil
}

func (h *Hub) dropPublisherLocked() {
	if h.pub == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = h.pub.Close(closeCtx)
	cancel()
	h.pub = nil
}
