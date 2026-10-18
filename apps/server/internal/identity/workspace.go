package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the workspace's own settings (time zones design D3): today
// the business time zone alone, the one calendar every bookkeeping day in
// the installation is judged in, which every module reads through
// contracts.BusinessCalendar (businesscalendar.go). An Owner sets it; the
// change is audited, since the day every module judges just moved.

// workspaceTimeZoneChanged is the audit action of a changed business time
// zone, with the zone before and after.
const workspaceTimeZoneChanged = "workspace.business_time_zone_changed"

// workspaceTimeZoneAudit is the before and after of workspaceTimeZoneChanged.
type workspaceTimeZoneAudit struct {
	BusinessTimeZone string `json:"businessTimeZone"`
}

// invalidParameterValue is the SQLSTATE Postgres raises for a time zone name
// it does not know.
const invalidParameterValue = "22023"

// GetIdentityOwnerWorkspace answers the workspace's settings.
// (GET /api/v1/identity/owner/workspace)
func (s *server) GetIdentityOwnerWorkspace(ctx context.Context, _ gen.GetIdentityOwnerWorkspaceRequestObject) (gen.GetIdentityOwnerWorkspaceResponseObject, error) {
	name, err := (&businessCalendar{q: s.q}).zoneName(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentityOwnerWorkspace200JSONResponse{BusinessTimeZone: name}, nil
}

// PutIdentityOwnerWorkspace replaces the workspace's settings.
// (PUT /api/v1/identity/owner/workspace)
//
// The zone is judged by Go, then by the database (zoneDisagreement), before
// the transaction; the singleton row is ensured and locked inside it, so two
// replaces at once queue and each audits the change it made. The value the
// row already holds answers 200 with nothing written and nothing audited.
func (s *server) PutIdentityOwnerWorkspace(ctx context.Context, req gen.PutIdentityOwnerWorkspaceRequestObject) (gen.PutIdentityOwnerWorkspaceResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	var body gen.WorkspaceSettingsRequest
	if req.Body != nil {
		body = *req.Body
	}
	now := s.deps.Clock()
	zone := strings.TrimSpace(body.BusinessTimeZone)
	if msg := zoneUnknownToGo(zone); msg != "" {
		return gen.PutIdentityOwnerWorkspace400JSONResponse(authErrorBody("invalid_request", msg, map[string][]string{"businessTimeZone": {msg}})), nil
	}
	msg, err := zoneDisagreement(ctx, s.q, zone, now)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return gen.PutIdentityOwnerWorkspace400JSONResponse(authErrorBody("invalid_request", msg, map[string][]string{"businessTimeZone": {msg}})), nil
	}

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.EnsureSystemSettings(ctx, now); err != nil {
			return fmt.Errorf("identity: ensure the system settings: %w", err)
		}
		current, err := q.LockBusinessTimeZone(ctx)
		if err != nil {
			return fmt.Errorf("identity: lock the business time zone: %w", err)
		}
		if current == zone {
			return nil
		}
		if err := q.SetBusinessTimeZone(ctx, store.SetBusinessTimeZoneParams{
			BusinessTimeZone: zone, Now: now, UpdatedByUserID: &p.UserID,
		}); err != nil {
			return fmt.Errorf("identity: set the business time zone: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: workspaceTimeZoneChanged,
			before: workspaceTimeZoneAudit{BusinessTimeZone: current},
			after:  workspaceTimeZoneAudit{BusinessTimeZone: zone},
			mfa:    p.MFAVerified,
		})
	})
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityOwnerWorkspace200JSONResponse{BusinessTimeZone: zone}, nil
}

// zoneUnknownToGo is why Go refuses a zone name, "" when it loads: an empty
// name and "Local" say nothing about the company's calendar (Go's
// LoadLocation answers UTC for "" without an error, and "Local" is whatever
// zone the server process runs in), and a name Go's tzdata lacks is not one
// a day can be derived in.
func zoneUnknownToGo(name string) string {
	switch _, err := time.LoadLocation(name); {
	case name == "" || name == "Local":
		return "A time zone is an IANA name, such as 'Europe/Oslo'"
	case err != nil:
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name)
	}
	return ""
}

// zoneDisagreement is why a zone name may not be stored, "" when it may. It
// asks the question that matters: not "does Postgres know this name" but
// "do Postgres and Go mean the same thing by it". Postgres resolves
// pg_timezone_abbrevs before pg_timezone_names, so 'CET' is a fixed +01:00
// to it and a zone with summer time to Go — and so for 'EET', 'MET' and
// 'WET'; a trip departing at 00:30 local on 1 July would then be dated
// 1 July by every rule written in Go and 30 June by every filter written in
// SQL, the exact month boundary the setting exists to get right. The
// comparison is of UTC offsets at a winter instant and a summer one of the
// request's year: the disagreements are about summer time, and a name the
// two agree about in January and in July they agree about all year. The
// message names a city zone, because that is what to write instead.
func zoneDisagreement(ctx context.Context, q *store.Queries, name string, now time.Time) (string, error) {
	winter := time.Date(now.Year(), time.January, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(now.Year(), time.July, 15, 12, 0, 0, 0, time.UTC)
	offsets, err := q.ResolveTimeZone(ctx, store.ResolveTimeZoneParams{Name: name, Winter: winter, Summer: summer})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == invalidParameterValue {
			return fmt.Sprintf("'%s' is not a time zone this installation's database knows", name), nil
		}
		return "", fmt.Errorf("identity: check a time zone: %w", err)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// zoneUnknownToGo already refused this; answered rather than asserted.
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name), nil
	}
	for _, probe := range []struct {
		at     time.Time
		inSQL  int64
		season string
	}{
		{winter, offsets.WinterOffset, "January"},
		{summer, offsets.SummerOffset, "July"},
	} {
		if _, inGo := probe.at.In(loc).Zone(); int64(inGo) != probe.inSQL {
			return fmt.Sprintf(
				"'%s' means one thing to this installation and another to its database in %s, "+
					"so the two would not agree which day a date falls on — use a city zone such as 'Europe/Oslo'",
				name, probe.season), nil
		}
	}
	return "", nil
}
