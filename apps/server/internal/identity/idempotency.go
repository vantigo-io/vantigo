package identity

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The router's Idempotency-Key store (idempotency design I11, I12, I20, I21,
// I26, I27): identity.idempotency_keys, reached by the router through a type
// assertion on Access. Every method is one short transaction of its own; the
// router runs them on a context of their own, so a request's cancellation
// never leaves a claim behind.

const (
	// idempotencyPurpose is the secrets.Box purpose a stored answer is sealed
	// under, bound to its row's scope (I20).
	idempotencyPurpose = "identity/idempotency"
	// idempotencyFingerprintPurpose is the MAC purpose a request's digest is
	// keyed under before it is stored or compared (I24): a database reader
	// cannot test a guessed request against it. A MAC purpose, never a Seal's.
	idempotencyFingerprintPurpose = "identity/idempotency-fingerprint"
	// idempotencyLease is how long a claim holds its key before a retry is
	// told the outcome is unknown (I5.1).
	idempotencyLease = 2 * time.Minute
	// idempotencyRetention is how long a key lives from its claim (I8).
	idempotencyRetention = 24 * time.Hour
	// idempotencyMaxKeys and idempotencyMaxBytes are a principal's quota of
	// live keys and of stored answer bytes, a running claim reserving the
	// largest answer (I21).
	idempotencyMaxKeys  = 2000
	idempotencyMaxBytes = 16 << 20
	// idempotencyMaxHeaders bounds a stored answer's encoded headers; the
	// migration's check allows it.
	idempotencyMaxHeaders = 8 << 10
	// idempotencyPruneBatch is how many expired keys one prune statement
	// deletes; the prune loops until a batch comes back short.
	idempotencyPruneBatch = 1000
)

// errIdempotencyHeadersTooLarge is an answer whose headers the store will not
// keep: its key is left unknown.
var errIdempotencyHeadersTooLarge = errors.New("identity: the answer's headers are too large to keep")

var _ contracts.IdempotencyStore = (*Access)(nil)

// IdempotencyEnabled is contracts.IdempotencyStore's switch:
// IDEMPOTENCY_ENABLED, and an Access with a database and a secrets box to seal
// with.
func (a *Access) IdempotencyEnabled() bool {
	return a.cfg != nil && a.cfg.IdempotencyEnabled && a.box != nil && a.dbPool != nil
}

// ClaimIdempotencyKey claims k (I21, I27) in one transaction under the
// principal's advisory lock: a live key is answered as it stands, whatever the
// quota; a new one past the quota is contracts.ErrIdempotencyQuota; else the
// key is inserted in_progress, or an expired row taken over, under a fresh
// lease.
func (a *Access) ClaimIdempotencyKey(ctx context.Context, k contracts.IdempotencyKey, digest []byte) (contracts.IdempotencyClaim, error) {
	fingerprint, err := a.box.MAC(idempotencyFingerprintPurpose, digest)
	if err != nil {
		return contracts.IdempotencyClaim{}, fmt.Errorf("identity: key the idempotency fingerprint: %w", err)
	}
	now := a.now()
	var claim contracts.IdempotencyClaim
	err = pgx.BeginFunc(ctx, a.dbPool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockIdempotencyPrincipal(ctx, k.Principal.String()); err != nil {
			return fmt.Errorf("identity: lock the idempotency principal: %w", err)
		}
		row, err := q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{
			PrincipalID: k.Principal, Via: k.Via, OperationID: k.Operation, Key: k.Key, Now: now,
		})
		switch {
		case err == nil:
			claim.Existing = a.idempotencyRecord(ctx, k, row, fingerprint, now)
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("identity: read the idempotency key: %w", err)
		}
		usage, err := q.IdempotencyUsage(ctx, store.IdempotencyUsageParams{PrincipalID: k.Principal, Now: now})
		if err != nil {
			return fmt.Errorf("identity: read the idempotency quota: %w", err)
		}
		if usage.Keys >= idempotencyMaxKeys || usage.Bytes >= idempotencyMaxBytes {
			return contracts.ErrIdempotencyQuota
		}
		lease, err := q.ClaimIdempotencyKey(ctx, store.ClaimIdempotencyKeyParams{
			PrincipalID: k.Principal,
			Via:         k.Via,
			OperationID: k.Operation,
			Key:         k.Key,
			Fingerprint: fingerprint,
			LeaseID:     uuid.New(),
			LeaseUntil:  now.Add(idempotencyLease),
			CustomerID:  k.CustomerID,
			Now:         now,
			ExpiresAt:   now.Add(idempotencyRetention),
		})
		if err != nil {
			// No row cannot happen under the lock (the key was absent or
			// expired); any error is the claim's.
			return fmt.Errorf("identity: claim the idempotency key: %w", err)
		}
		claim.Claimed, claim.Lease = true, lease
		return nil
	})
	if err != nil {
		return contracts.IdempotencyClaim{}, err
	}
	return claim, nil
}

// idempotencyRecord is a live row as the router reads it. A done answer that
// does not open — sealed under another APP_SECRET, or altered — or that was
// claimed before the last customer anonymisation reads as unknown, with no
// answer: it is never given again.
func (a *Access) idempotencyRecord(ctx context.Context, k contracts.IdempotencyKey, row store.GetIdempotencyKeyRow, fingerprint []byte, now time.Time) contracts.IdempotencyRecord {
	rec := contracts.IdempotencyRecord{
		SameRequest: secrets.MACEqual(row.Fingerprint, fingerprint),
		State:       row.State,
		LeaseLive:   row.LeaseUntil.After(now),
	}
	if row.State != contracts.IdempotencyDone {
		return rec
	}
	if row.Erased {
		rec.State = contracts.IdempotencyUnknown
		return rec
	}
	plain, err := a.box.OpenBound(idempotencyPurpose, idempotencyBound(k), row.Answer)
	if err == nil {
		rec.Response, err = decodeIdempotencyAnswer(plain)
	}
	if err != nil {
		if a.log != nil {
			a.log.WarnContext(ctx, "identity: an idempotency key's answer does not open", "operation", k.Operation, "error", err)
		}
		rec.State = contracts.IdempotencyUnknown
		rec.Response = contracts.IdempotencyResponse{}
	}
	return rec
}

// CompleteIdempotencyKey stores resp for the claim lease holds, sealed as one
// plaintext — status, headers and body — bound to the row (I20, I26). A claim
// no longer held stores nothing; headers too large to keep leave the key
// unknown.
func (a *Access) CompleteIdempotencyKey(ctx context.Context, k contracts.IdempotencyKey, lease uuid.UUID, resp contracts.IdempotencyResponse) error {
	plain, err := encodeIdempotencyAnswer(resp)
	if errors.Is(err, errIdempotencyHeadersTooLarge) {
		return a.MarkIdempotencyKeyUnknown(ctx, k, lease)
	}
	if err != nil {
		return err
	}
	answer, err := a.box.SealBound(idempotencyPurpose, idempotencyBound(k), plain)
	if err != nil {
		return fmt.Errorf("identity: seal the idempotency key's answer: %w", err)
	}
	if _, err := a.q.CompleteIdempotencyKey(ctx, store.CompleteIdempotencyKeyParams{
		Answer: answer, PrincipalID: k.Principal, Via: k.Via, OperationID: k.Operation, Key: k.Key, LeaseID: lease,
	}); err != nil {
		return fmt.Errorf("identity: store the idempotency key's answer: %w", err)
	}
	return nil
}

// MarkIdempotencyKeyUnknown records that the claim's outcome cannot be told.
func (a *Access) MarkIdempotencyKeyUnknown(ctx context.Context, k contracts.IdempotencyKey, lease uuid.UUID) error {
	if _, err := a.q.MarkIdempotencyKeyUnknown(ctx, store.MarkIdempotencyKeyUnknownParams{
		PrincipalID: k.Principal, Via: k.Via, OperationID: k.Operation, Key: k.Key, LeaseID: lease,
	}); err != nil {
		return fmt.Errorf("identity: mark the idempotency key unknown: %w", err)
	}
	return nil
}

// ReleaseIdempotencyKey deletes the claim lease holds.
func (a *Access) ReleaseIdempotencyKey(ctx context.Context, k contracts.IdempotencyKey, lease uuid.UUID) error {
	if _, err := a.q.ReleaseIdempotencyKey(ctx, store.ReleaseIdempotencyKeyParams{
		PrincipalID: k.Principal, Via: k.Via, OperationID: k.Operation, Key: k.Key, LeaseID: lease,
	}); err != nil {
		return fmt.Errorf("identity: release the idempotency key: %w", err)
	}
	return nil
}

// idempotencyBound is what a stored answer is bound to (I20): the row's scope,
// each part separated by a NUL, which none of them contains (the key is
// printable ASCII, the operation an operationId, via the router's own).
func idempotencyBound(k contracts.IdempotencyKey) []byte {
	return []byte(strings.Join([]string{k.Principal.String(), k.Via, k.Operation, k.Key}, "\x00"))
}

// encodeIdempotencyAnswer is the plaintext a stored answer is sealed as: the
// status and the headers' JSON as uvarint-length-prefixed parts, then the body.
func encodeIdempotencyAnswer(resp contracts.IdempotencyResponse) ([]byte, error) {
	headers, err := json.Marshal(resp.Headers)
	if err != nil {
		return nil, fmt.Errorf("identity: encode the idempotency key's headers: %w", err)
	}
	if len(headers) > idempotencyMaxHeaders {
		return nil, errIdempotencyHeadersTooLarge
	}
	out := make([]byte, 0, 2*binary.MaxVarintLen64+len(headers)+len(resp.Body))
	out = binary.AppendUvarint(out, uint64(resp.Status))
	out = binary.AppendUvarint(out, uint64(len(headers)))
	out = append(out, headers...)
	return append(out, resp.Body...), nil
}

// decodeIdempotencyAnswer reads what encodeIdempotencyAnswer wrote.
func decodeIdempotencyAnswer(plain []byte) (contracts.IdempotencyResponse, error) {
	bad := errors.New("identity: the idempotency key's answer is malformed")
	status, n := binary.Uvarint(plain)
	if n <= 0 || status < 100 || status > 599 {
		return contracts.IdempotencyResponse{}, bad
	}
	plain = plain[n:]
	size, n := binary.Uvarint(plain)
	if n <= 0 || size > uint64(len(plain)-n) {
		return contracts.IdempotencyResponse{}, bad
	}
	plain = plain[n:]
	resp := contracts.IdempotencyResponse{Status: int(status)}
	if err := json.Unmarshal(plain[:size], &resp.Headers); err != nil {
		return contracts.IdempotencyResponse{}, bad
	}
	if body := plain[size:]; len(body) > 0 {
		resp.Body = append([]byte(nil), body...)
	}
	return resp, nil
}

// pruneIdempotencyKeys deletes the expired keys at now, a batch at a time
// until a batch comes back short (I21), and answers how many.
func pruneIdempotencyKeys(ctx context.Context, q *store.Queries, now time.Time) (int, error) {
	deleted := 0
	for {
		n, err := q.DeleteExpiredIdempotencyKeys(ctx, store.DeleteExpiredIdempotencyKeysParams{Now: now, BatchSize: idempotencyPruneBatch})
		if err != nil {
			return deleted, fmt.Errorf("identity: prune the idempotency keys: %w", err)
		}
		deleted += int(n)
		if n < idempotencyPruneBatch || ctx.Err() != nil {
			return deleted, nil
		}
	}
}

// erasedIdempotencyKeys is the kind a customer's anonymisation reports.
const erasedIdempotencyKeys = "identity.idempotencyKeys"

// idempotencyPersonalData is identity's contracts.CustomerPersonalData (I20).
// The anonymisation deletes the Idempotency-Keys of agent runs scoped to the
// customer, and raises the erasure watermark: any other stored answer claimed
// before it may name the customer too, and is never replayed (it reads as
// unknown, and is pruned within 24 hours). It exports nothing: a stored answer
// is a copy of what the module that made it exports.
type idempotencyPersonalData struct{ clock func() time.Time }

var _ contracts.CustomerPersonalData = idempotencyPersonalData{}

func newIdempotencyPersonalData(d module.Deps) contracts.CustomerPersonalData {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return idempotencyPersonalData{clock: clock}
}

func (idempotencyPersonalData) ExportCustomerData(context.Context, int32) (any, error) {
	return nil, nil
}

func (d idempotencyPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	n, err := q.DeleteCustomerIdempotencyKeys(ctx, &customerID)
	if err != nil {
		return nil, fmt.Errorf("identity: delete the customer's idempotency keys: %w", err)
	}
	if err := q.RaiseIdempotencyErasure(ctx, d.clock()); err != nil {
		return nil, fmt.Errorf("identity: raise the idempotency erasure watermark: %w", err)
	}
	return []contracts.ErasedData{{Kind: erasedIdempotencyKeys, Count: n}}, nil
}
