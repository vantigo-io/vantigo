package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// APIReasonIdempotentReplay is the request log's reason for an answer the
// router replayed for an Idempotency-Key instead of running the operation
// again (idempotency design I10, I22).
const APIReasonIdempotentReplay = "idempotent_replay"

// The states of a stored Idempotency-Key (idempotency design I5, I23).
const (
	// IdempotencyInProgress: the first request holds the key under its lease.
	IdempotencyInProgress = "in_progress"
	// IdempotencyDone: the first request's answer is stored and is replayed.
	IdempotencyDone = "done"
	// IdempotencyUnknown: whether the first request was applied cannot be
	// told; the key is never run again.
	IdempotencyUnknown = "unknown"
)

// ErrIdempotencyQuota is a claim of a new key refused because the principal
// holds as many live keys, or as many stored bytes, as it may (I21): 429
// idempotency_quota. A key that already exists is answered whatever the quota.
var ErrIdempotencyQuota = errors.New("idempotency quota reached")

// IdempotencyKey is the scope of one key (I3, I28): the principal — an
// integration for an API key, the person or agent otherwise — the way in, the
// operation and the key the caller sent. CustomerID is the customer an
// agent's run is scoped to, kept so the customer's anonymisation removes it.
type IdempotencyKey struct {
	Principal  uuid.UUID
	Via        string
	Operation  string
	Key        string
	CustomerID *int32
}

// IdempotencyResponse is a stored answer (I6): its status, the headers a
// replay carries and its body.
type IdempotencyResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// IdempotencyRecord is a live key as the store holds it.
type IdempotencyRecord struct {
	// SameRequest is whether the key was claimed for a request of the
	// fingerprint asked about.
	SameRequest bool
	State       string
	// LeaseLive is whether an in_progress key's lease has not yet run out.
	LeaseLive bool
	// Response is set for a done key.
	Response IdempotencyResponse
}

// IdempotencyClaim is a claim's answer: the lease when the key was free (or
// expired) and is now the caller's, else the live key as it stands.
type IdempotencyClaim struct {
	Claimed  bool
	Lease    uuid.UUID
	Existing IdempotencyRecord
}

// IdempotencyStore keeps the router's Idempotency-Key claims (idempotency
// design I12). identity's Access implements it beside Access; the router finds
// it by a type assertion on the Access it holds, as it finds the
// APIRequestRecorder (K35), so no module's RouterOptions names it. Each method
// is its own short transaction.
type IdempotencyStore interface {
	// IdempotencyEnabled is IDEMPOTENCY_ENABLED: false, the router installs no
	// middleware and the header is ignored (I9).
	IdempotencyEnabled() bool
	// ClaimIdempotencyKey claims k for a request of digest, the request's
	// SHA-256 fingerprint (I24, I27), or answers the live key as it stands. A
	// new key past the principal's quota is ErrIdempotencyQuota (I21); the
	// quota and the claim are one decision.
	ClaimIdempotencyKey(ctx context.Context, k IdempotencyKey, digest []byte) (IdempotencyClaim, error)
	// CompleteIdempotencyKey stores the answer of the claim lease holds.
	CompleteIdempotencyKey(ctx context.Context, k IdempotencyKey, lease uuid.UUID, resp IdempotencyResponse) error
	// MarkIdempotencyKeyUnknown records that the claim's outcome cannot be told.
	MarkIdempotencyKeyUnknown(ctx context.Context, k IdempotencyKey, lease uuid.UUID) error
	// ReleaseIdempotencyKey deletes the claim, so an identical retry runs
	// again: the answer of a request whose refusal is not kept (I23).
	ReleaseIdempotencyKey(ctx context.Context, k IdempotencyKey, lease uuid.UUID) error
}
