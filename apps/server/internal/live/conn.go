package live

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// maxClientFrame is the largest text frame a client may send (L4); a larger
// one closes the socket with 1009. The socket's read limit is maxBinaryFrame
// (K25), so the hub checks text frames itself.
const maxClientFrame = 4096

// writeTimeout bounds one frame's write.
const writeTimeout = 10 * time.Second

// serveSocket owns ws until it closes (L10, L12): one goroutine writes, this
// one reads, a third pings every 25 s, drops the socket when nothing has been
// heard for 60 s, and re-checks the session and the subscriptions every 60 s.
// The socket's context is the hub's, without its cancellation: the hub closes
// every socket with 1001 itself when the process shuts down, and a cancelled
// read would cut the socket without that close.
func (h *Hub) serveSocket(ws *websocket.Conn, c *client) {
	ctx, cancel := context.WithCancel(h.base)
	ws.SetReadLimit(maxBinaryFrame)

	var lastHeard atomic.Int64
	heard := func() { lastHeard.Store(time.Now().UnixNano()) }
	heard()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		h.writeLoop(ctx, ws, c)
	}()
	keepAliveDone := make(chan struct{})
	go func() {
		defer close(keepAliveDone)
		h.keepAlive(ctx, ws, c, &lastHeard, heard)
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			c.close(closeNormal, "")
			break
		}
		heard()
		if typ == websocket.MessageBinary {
			h.handleBinary(ctx, c, data)
			continue
		}
		if len(data) > maxClientFrame {
			c.close(closeTooBig, "frame too big")
			break
		}
		h.handleFrame(ctx, c, data)
	}
	// Every goroutine of the socket has returned before it is detached, so
	// none of them — a re-check reading the pool — outlives the hub's Wait.
	<-writerDone
	<-keepAliveDone
	cancel()
	h.detach(c)
}

// writeLoop writes c's queue until c is closed, then closes ws with c's code.
func (h *Hub) writeLoop(ctx context.Context, ws *websocket.Conn, c *client) {
	for {
		select {
		case frame := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			typ := websocket.MessageText
			if isBinaryFrame(frame) {
				typ = websocket.MessageBinary
			}
			err := ws.Write(wctx, typ, frame)
			cancel()
			if err != nil {
				c.close(closeDead, "")
			}
		case <-c.done:
			if c.code == closeDead {
				_ = ws.CloseNow()
			} else {
				_ = ws.Close(websocket.StatusCode(c.code), c.reason)
			}
			return
		}
	}
}

// keepAlive pings, drops a socket gone silent, and re-checks, until ctx ends
// or c closes; it returns only once its pings have returned too.
func (h *Hub) keepAlive(ctx context.Context, ws *websocket.Conn, c *client, lastHeard *atomic.Int64, heard func()) {
	ctx, cancel := context.WithCancel(ctx)
	var pings sync.WaitGroup
	defer func() {
		cancel()
		pings.Wait()
	}()
	ping := time.NewTicker(h.limits.ping)
	defer ping.Stop()
	recheck := time.NewTicker(h.limits.recheck)
	defer recheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ping.C:
			if time.Since(time.Unix(0, lastHeard.Load())) >= h.limits.dead {
				c.close(closeDead, "")
				return
			}
			pings.Add(1)
			go func() {
				defer pings.Done()
				pctx, cancel := context.WithTimeout(ctx, h.limits.dead)
				defer cancel()
				if ws.Ping(pctx) == nil {
					heard()
				}
			}()
		case <-recheck.C:
			rctx, stop := context.WithTimeout(ctx, h.limits.recheckBound)
			h.recheck(rctx, c)
			stop()
		}
	}
}
