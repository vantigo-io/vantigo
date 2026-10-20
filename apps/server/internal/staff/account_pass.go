package staff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the pass (Entra design D7): the Temporary Access Pass a
// provider issued for a person's external account, shown to HR once. The
// provider keeps it sealed (RevealPass) and answers it a single time; Staff
// never stores, logs or puts it on an event — this handler's response is
// the one place it passes through the module, with Cache-Control no-store.
// The link's pass_issued_at is cleared after, and the reveal is a
// pass_revealed event naming the viewer and the provider, never the pass.

// passCacheControl keeps the pass out of every cache between the provider
// and the screen.
const passCacheControl = "no-store"

// PostStaffPeopleByIdAccountPass Show a person's pass
// (POST /api/v1/staff/people/{id}/account/pass)
//
// staff:manage (the contract's access rule), rate-limited with Run now and
// Disable now (policyAccountAction). The person's provider link that names
// an external account, else 404; no enabled module offering its provider,
// or the provider failing, 503; the pass gone, 409 pass_gone. The provider
// is called with the caller as the actor, on no connection, under the call
// timeout.
func (s *server) PostStaffPeopleByIdAccountPass(ctx context.Context, req gen.PostStaffPeopleByIdAccountPassRequestObject) (gen.PostStaffPeopleByIdAccountPassResponseObject, error) {
	run, err := s.callerRun(ctx)
	if err != nil {
		return nil, err
	}
	key, externalID, err := s.passLink(ctx, req.Id)
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountPass404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if externalID == "" {
		return gen.PostStaffPeopleByIdAccountPass404Response{}, nil
	}
	provider, ok := s.provisioner(key)
	if !ok {
		return gen.PostStaffPeopleByIdAccountPass503ApplicationProblemPlusJSONResponse(providerUnavailable()), nil
	}
	pass, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.Pass, error) {
		return s.providerRevealPass(ctx, provider, run.actor, externalID)
	})
	// The write after a reveal goes on if the client goes away: the provider
	// has spent the pass, so its event is recorded whatever happens to the
	// response.
	work := context.WithoutCancel(ctx)
	switch {
	case errors.Is(err, contracts.ErrPassGone):
		if err := s.clearPassIssued(work, req.Id, key); err != nil {
			return nil, err
		}
		return gen.PostStaffPeopleByIdAccountPass409ApplicationProblemPlusJSONResponse(passGone()), nil
	case err != nil:
		s.logger().WarnContext(ctx, "staff: reveal a person's pass",
			slog.Int64("person_id", req.Id), slog.String("provider", key), slog.String("error", err.Error()))
		return gen.PostStaffPeopleByIdAccountPass503ApplicationProblemPlusJSONResponse(providerUnavailable()), nil
	}
	err = s.withPersonLock(work, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.ClearPassIssued(ctx, store.ClearPassIssuedParams{Now: s.deps.Clock(), PersonID: req.Id, Provider: key}); err != nil {
			return fmt.Errorf("staff: clear person %d's pass: %w", req.Id, err)
		}
		return s.recordEvent(ctx, txq, &req.Id, eventPassRevealed, run.who, map[string]any{"provider": key}, accountEventClasses)
	})
	if err != nil {
		return nil, err
	}
	cacheControl := passCacheControl
	return gen.PostStaffPeopleByIdAccountPass200JSONResponse{
		Body:    gen.StaffAccountPass{Pass: pass.Value, ExpiresAt: pass.ExpiresAt},
		Headers: gen.PostStaffPeopleByIdAccountPass200ResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

// passLink is the provider key and the external account of the person's
// provider link whose pass may be shown — the first of providerKeys that
// names an account — read on no transaction; externalID "" when there is
// none, errPersonNotFound when there is no such person.
func (s *server) passLink(ctx context.Context, personID int64) (key, externalID string, err error) {
	q := store.New(s.deps.Pool)
	p, err := readAccountPerson(ctx, q, personID)
	if err != nil {
		return "", "", err
	}
	for _, key := range s.providerKeys(p) {
		row, err := q.PersonProviderLink(ctx, store.PersonProviderLinkParams{PersonID: personID, Provider: key})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("staff: read person %d's %s link: %w", personID, key, err)
		}
		if row.ExternalID != nil && *row.ExternalID != "" {
			return key, *row.ExternalID, nil
		}
	}
	return "", "", nil
}

// clearPassIssued clears pass_issued_at on the person's link under key when
// the provider says the pass is gone, so the Account tab stops offering it.
func (s *server) clearPassIssued(ctx context.Context, personID int64, key string) error {
	if _, err := store.New(s.deps.Pool).ClearPassIssued(ctx, store.ClearPassIssuedParams{
		Now: s.deps.Clock(), PersonID: personID, Provider: key,
	}); err != nil {
		return fmt.Errorf("staff: clear person %d's pass: %w", personID, err)
	}
	return nil
}
