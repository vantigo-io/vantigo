package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
)

// The first server-sent events endpoint in the server (the Assistant design
// S10): sendAssistantMessage answers text/event-stream, one frame per event,
// flushed as it is written.
//
// The run is detached from the connection. The run's goroutine writes frames
// into an eventQueue that never blocks it; the response drains the queue into
// the connection for as long as there is one. A client that disconnects stops
// hearing, not the run, which has persisted every message and action as it
// happened and can be followed by polling the conversation.

// The events of a run's stream.
const (
	eventRun     = "run"
	eventDelta   = "delta"
	eventTool    = "tool"
	eventMessage = "message"
	// eventConfirmation is a proposed change's card, or the same card decided.
	eventConfirmation = "confirmation"
	eventDone         = "done"
	eventError        = "error"
)

// keepAlive is how often an idle stream writes a comment frame, so a proxy
// that closes idle connections does not close a run that is thinking.
const keepAlive = 15 * time.Second

// eventQueue is an unbounded queue of encoded frames: emit never blocks, and
// the reader waits for the next frame.
type eventQueue struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool // the run is over: no more frames will come
	gone   bool // the reader is gone: frames are dropped
	wake   chan struct{}
}

func newEventQueue() *eventQueue { return &eventQueue{wake: make(chan struct{}, 1)} }

// emit queues one event; a no-op once the reader is gone.
func (q *eventQueue) emit(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	frame := append(append(append([]byte("event: "), event...), "\ndata: "...), payload...)
	frame = append(frame, '\n', '\n')
	q.mu.Lock()
	if !q.gone && !q.closed {
		q.frames = append(q.frames, frame)
	}
	q.mu.Unlock()
	q.signal()
}

// close ends the stream after the frames already queued.
func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// abandon drops what is queued and ignores what follows: the reader is gone.
func (q *eventQueue) abandon() {
	q.mu.Lock()
	q.gone, q.frames = true, nil
	q.mu.Unlock()
}

func (q *eventQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// next waits for the next frame: ok is false when the stream is over, and a
// keep-alive comment is returned after keepAlive of silence. ctx is the
// connection's.
func (q *eventQueue) next(ctx context.Context) (frame []byte, ok bool) {
	timer := time.NewTimer(keepAlive)
	defer timer.Stop()
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			frame, q.frames = q.frames[0], q.frames[1:]
			q.mu.Unlock()
			return frame, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, false
		}
		select {
		case <-q.wake:
		case <-timer.C:
			return []byte(": keep-alive\n\n"), true
		case <-ctx.Done():
			return nil, false
		}
	}
}

// streamResponse is the 200 of sendAssistantMessage, confirm and cancel: it satisfies the generated
// response interface and writes the stream itself, because the generated
// event-stream response flushes only through a ResponseWriter that is itself an
// http.Flusher, and the server's logging and recovery wrappers are not (they
// expose Unwrap, which http.ResponseController follows).
type streamResponse struct {
	ctx   context.Context
	queue *eventQueue
}

var (
	_ gen.SendAssistantMessageResponseObject          = streamResponse{}
	_ gen.ConfirmAssistantPendingActionResponseObject = streamResponse{}
	_ gen.CancelAssistantPendingActionResponseObject  = streamResponse{}
)

func (r streamResponse) VisitSendAssistantMessageResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r streamResponse) VisitConfirmAssistantPendingActionResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r streamResponse) VisitCancelAssistantPendingActionResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r streamResponse) write(w http.ResponseWriter) error {
	defer r.queue.abandon()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	for {
		frame, ok := r.queue.next(r.ctx)
		if !ok {
			return nil
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
		// A writer that cannot flush still delivered the frame; the stream just
		// arrives in larger pieces.
		_ = rc.Flush()
	}
}
