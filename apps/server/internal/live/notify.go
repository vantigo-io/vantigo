package live

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
)

// Channel is the one Postgres notification channel every hub listens on.
const Channel = "vantigo_live"

// maxPayload is the largest payload Notify sends: NOTIFY refuses one over
// 8000 bytes, and an id is free text (L6).
const maxPayload = 7 * 1024

// Execer is the one method Notify needs, which pgx.Tx, *pgxpool.Pool, *pgx.Conn
// and a sqlc DBTX all have.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// payload is a notification on Channel: an event (T set), or a hub's presence
// batch (K "presence", H set, T empty).
type payload struct {
	T  string `json:"t,omitempty"`
	K  string `json:"k"`
	ID string `json:"id,omitempty"`
	// H, D and F are a presence batch's: the hub that sent it, its deltas,
	// and whether it is the hub's full set (a heartbeat).
	H string          `json:"h,omitempty"`
	D []presenceDelta `json:"d,omitempty"`
	F bool            `json:"f,omitempty"`
	// A is a document topic's batch of awareness frames, base64 (K30); H is
	// the hub that sent it. On a document's update (K27), H is the hub that
	// appended it, which skips its own notification.
	A []string `json:"a,omitempty"`
}

// presenceKind is a presence batch's K.
const presenceKind = "presence"

// Notify tells every hub that kind happened on topic, to the thing id names
// ("" for none). It runs pg_notify on q: on a transaction, Postgres delivers
// it only when the transaction commits and drops it on rollback, so a module
// notifies on the same transaction that writes, once per changed aggregate,
// never in a loop over many rows (L6). It refuses a topic outside the grammar,
// an empty or malformed kind, and a payload over 7 KiB.
func Notify(ctx context.Context, q Execer, topic, kind, id string) error {
	body, err := eventPayload(topic, kind, id)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, body); err != nil {
		return fmt.Errorf("live: notify: %w", err)
	}
	return nil
}

// Event is one notification of NotifyAll: kind happened on Topic, to the thing
// ID names ("" for none).
type Event struct {
	Topic, Kind, ID string
}

// NotifyAll is Notify for several events at once, in one statement on q — one
// round trip, however many — for a write that changes several aggregates of
// one kind together, a posting's lots. It refuses the whole batch when any
// event fails Notify's rules, and sends nothing for none.
func NotifyAll(ctx context.Context, q Execer, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	bodies := make([]string, 0, len(events))
	for _, e := range events {
		body, err := eventPayload(e.Topic, e.Kind, e.ID)
		if err != nil {
			return err
		}
		bodies = append(bodies, body)
	}
	if _, err := q.Exec(ctx, "SELECT pg_notify($1, p) FROM unnest($2::text[]) AS p", Channel, bodies); err != nil {
		return fmt.Errorf("live: notify: %w", err)
	}
	return nil
}

// eventPayload is an event's payload, or why Notify refuses it.
func eventPayload(topic, kind, id string) (string, error) {
	if !ValidTopic(topic) {
		return "", fmt.Errorf("live: notify: topic %q does not follow the grammar", topic)
	}
	if !eventKindPattern.MatchString(kind) {
		return "", fmt.Errorf("live: notify: kind %q is not an event kind", kind)
	}
	body, err := json.Marshal(payload{T: topic, K: kind, ID: id})
	if err != nil {
		return "", fmt.Errorf("live: notify: %w", err)
	}
	if len(body) > maxPayload {
		return "", fmt.Errorf("live: notify: the payload is %d bytes, over %d", len(body), maxPayload)
	}
	return string(body), nil
}

// The document notifications (K27, K32): kinds on a document topic.
const (
	docUpdateKind    = "update"
	docResetKind     = "reset"
	docAwarenessKind = "awareness"
)

// NotifyDoc tells every hub that a document topic has a new row at seq,
// appended through the hub named origin (DocWrite.Hub). A DocStore calls it on
// the transaction that stores the row.
func NotifyDoc(ctx context.Context, q Execer, topic string, seq int64, origin string) error {
	return notifyDoc(ctx, q, payload{T: topic, K: docUpdateKind, ID: strconv.FormatInt(seq, 10), H: origin})
}

// NotifyDocReset tells every hub that a document's log was deleted (K32) and is
// now at epoch: the subscribers discard their copy. It runs on the transaction
// that deletes it.
func NotifyDocReset(ctx context.Context, q Execer, topic string, epoch int64) error {
	return notifyDoc(ctx, q, payload{T: topic, K: docResetKind, ID: strconv.FormatInt(epoch, 10)})
}

func notifyDoc(ctx context.Context, q Execer, p payload) error {
	if !ValidTopic(p.T) {
		return fmt.Errorf("live: notify: topic %q does not follow the grammar", p.T)
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("live: notify: %w", err)
	}
	if _, err := q.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, string(body)); err != nil {
		return fmt.Errorf("live: notify: %w", err)
	}
	return nil
}
