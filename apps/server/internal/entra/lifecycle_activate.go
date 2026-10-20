package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is Activate and the pass (Entra design D13, research §3.2,
// §3.4): the user enabled with the hire date, the licences the spec names,
// and a Temporary Access Pass sealed under entra/pass and shown once.

// passPurpose is the secrets box purpose a pass is sealed under.
const passPurpose = "entra/pass"

// The hours of the day the lifecycle's instants fall on, in the business
// zone (the plan's "Exact values").
const (
	hireHour  = 1
	leaveHour = 23
)

// Activate enables the user (contracts.IdentityProvisioner.Activate):
// accountEnabled with the hire day's 01:00 when write_hire_date — a refusal
// of the hire date alone retried without it and reported
// hire_date_not_written —, the spec's licences not yet assigned, then a pass
// when this call enabled the user and HR asked for a fresh one or no pass
// waits unrevealed — never on a user enabled already, whatever the flag: a
// Temporary Access Pass over a working account would be a way into it. A
// 403 on the pass alone is reported pass_forbidden, the user enabled all the
// same. Idempotent: an enabled user with nothing missing is answered as it
// is. An unconfirmed adoption is answered as it is, nothing written.
func (p *provisioner) Activate(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, spec, true)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	refuse := func(r *refusedError) (contracts.ExternalAccount, error) {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserActivated, &id, u.UserPrincipalName, r)
	}
	if u.synced() {
		return refuse(syncedRefusal())
	}
	if !trusted(row) {
		return p.unconfirmed(ctx, s, actor, actionUserActivated, id, u, row)
	}
	wasEnabled := u.AccountEnabled
	var reported, codes []string

	body := map[string]any{}
	if !wasEnabled {
		body["accountEnabled"] = true
	}
	if s.conn.WriteHireDate && spec.HireDate != nil {
		at, err := p.instant(ctx, *spec.HireDate, hireHour)
		if err != nil {
			return contracts.ExternalAccount{}, err
		}
		if u.EmployeeHireDate == nil || !u.EmployeeHireDate.Equal(at) {
			body["employeeHireDate"] = at.UTC().Format(time.RFC3339)
		}
	}
	if len(body) > 0 {
		err := p.client.patch(ctx, s.cred, userPath(id), body)
		if _, hire := body["employeeHireDate"]; hire && (isStatus(err, http.StatusBadRequest) || isStatus(err, http.StatusForbidden)) {
			// research §3.1's UNCERTAIN: an app-only caller may not write it.
			delete(body, "employeeHireDate")
			reported = append(reported, contracts.ReportedHireDateNotWritten)
			err = nil
			if len(body) > 0 {
				err = p.client.patch(ctx, s.cred, userPath(id), body)
			}
		} else if hire && err == nil {
			codes = append(codes, "hire_date_written")
		}
		switch {
		case err != nil && !wasEnabled && isStatus(err, http.StatusForbidden):
			return refuse(privilegedRefusal())
		case err != nil:
			return contracts.ExternalAccount{}, err
		}
	}
	if !wasEnabled {
		codes = append([]string{"enabled"}, codes...)
		if err := p.sensitiveSucceeded(ctx, s); err != nil {
			return contracts.ExternalAccount{}, err
		}
		if _, err := s.q.SetAccountState(ctx, store.SetAccountStateParams{
			State: stateEnabled, Reported: orEmpty(row.Reported), Now: p.client.now(), ObjectID: id,
		}); err != nil {
			return contracts.ExternalAccount{}, fmt.Errorf("entra: record the account enabled: %w", err)
		}
		u.AccountEnabled = true
	}

	licences, assigned, err := p.assignLicences(ctx, s, id, u, spec.Licences)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	reported = append(reported, licences...)
	if assigned > 0 {
		codes = append(codes, fmt.Sprintf("licences_assigned:%d", assigned))
	}

	if !wasEnabled && (spec.Flags.Fresh || !livePass(row, p.client.now())) {
		code, err := p.issuePass(ctx, s, actor, id, u.UserPrincipalName)
		if err != nil {
			return contracts.ExternalAccount{}, err
		}
		if code != "" {
			reported = append(reported, code)
		} else {
			codes = append(codes, "pass_issued")
		}
	}

	reported = mergeCodes(row.Reported, activateJudges, reported)
	row, err = p.settle(ctx, s, actor, actionUserActivated, id, u.UserPrincipalName, stateEnabled, reported, append(codes, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.client.now()), nil
}

// assignLicences assigns the part numbers the spec names that the user does
// not hold yet, directly (research §3.2): resolved through subscribedSkus —
// an unknown part number reported licence_unavailable:<part> —, then one
// assignLicense, after which a licenseAssignmentStates error on a SKU (a
// CountViolation) is reported for it while the rest stay assigned. A refusal
// of the whole call reports every part it carried. It answers the codes and
// how many SKUs it asked for.
func (p *provisioner) assignLicences(ctx context.Context, s session, id uuid.UUID, u graphUser, parts []string) ([]string, int, error) {
	var wanted []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" && !slices.Contains(wanted, part) {
			wanted = append(wanted, part)
		}
	}
	if len(wanted) == 0 {
		return nil, 0, nil
	}
	var skus subscribedSkus
	if err := p.client.get(ctx, s.cred, "/subscribedSkus", &skus); err != nil {
		return nil, 0, err
	}
	held := map[string]bool{}
	for _, l := range u.AssignedLicenses {
		held[strings.ToLower(l.SkuID)] = true
	}
	var reported []string
	partOf := map[string]string{}
	var add []map[string]any
	for _, part := range wanted {
		i := -1
		for j, v := range skus.Value {
			if strings.EqualFold(v.SkuPartNumber, part) {
				i = j
				break
			}
		}
		if i < 0 {
			reported = append(reported, licenceCode(part))
			continue
		}
		sku := strings.ToLower(skus.Value[i].SkuID)
		if held[sku] || partOf[sku] != "" {
			continue
		}
		partOf[sku] = part
		add = append(add, map[string]any{"skuId": skus.Value[i].SkuID, "disabledPlans": []string{}})
	}
	if len(add) == 0 {
		return reported, 0, nil
	}
	var after graphUser
	err := p.client.post(ctx, s.cred, userPath(id, "/assignLicense"), map[string]any{"addLicenses": add, "removeLicenses": []string{}}, &after)
	if g, ok := asGraphError(err); ok && g.Status >= 400 && g.Status < 500 {
		for _, l := range add {
			reported = append(reported, licenceCode(partOf[strings.ToLower(l["skuId"].(string))]))
		}
		return reported, len(add), nil
	} else if err != nil {
		return nil, 0, err
	}
	for _, st := range after.LicenseAssignmentStates {
		part := partOf[strings.ToLower(st.SkuID)]
		if part != "" && st.AssignedByGroup == nil && st.Error != "" && st.Error != "None" {
			reported = append(reported, licenceCode(part))
		}
	}
	return reported, len(add), nil
}

// issuePass creates a Temporary Access Pass with the connection's lifetime
// and single use, and keeps it sealed under entra/pass with its expiry
// (startDateTime + lifetime) until RevealPass. A 400 is the tenant's
// authentication methods policy refusing it: pass_not_allowed, answered as a
// code. A 403 is pass_forbidden, a code too: the enable before it was
// allowed, so it is no privileged refusal and lengthens no streak. The
// create is made once (noRetry): a second would replace a pass the first
// may have issued. The pass is never logged, and never written anywhere but
// the sealed column.
func (p *provisioner) issuePass(ctx context.Context, s session, actor contracts.Actor, id uuid.UUID, upn string) (string, error) {
	if p.deps.Secrets == nil {
		return "", errors.New("entra: no secrets box to seal the pass with")
	}
	var made struct {
		TemporaryAccessPass string     `json:"temporaryAccessPass"`
		StartDateTime       *time.Time `json:"startDateTime"`
		LifetimeInMinutes   int        `json:"lifetimeInMinutes"`
	}
	err := p.client.post(ctx, s.cred, userPath(id, "/authentication/temporaryAccessPassMethods"), map[string]any{
		"lifetimeInMinutes": s.conn.PassLifetimeMinutes, "isUsableOnce": s.conn.PassUsableOnce,
	}, &made, noRetry)
	switch {
	case isStatus(err, http.StatusBadRequest):
		return contracts.ReportedPassNotAllowed, nil
	case isStatus(err, http.StatusForbidden):
		return reportedPassForbidden, nil
	case err != nil:
		return "", err
	case made.TemporaryAccessPass == "":
		return "", fmt.Errorf("%w: the pass's create answered no pass", errTenantUnreachable)
	}
	now := p.client.now()
	start, lifetime := now, int(s.conn.PassLifetimeMinutes)
	if made.StartDateTime != nil {
		start = *made.StartDateTime
	}
	if made.LifetimeInMinutes > 0 {
		lifetime = made.LifetimeInMinutes
	}
	expires := start.Add(time.Duration(lifetime) * time.Minute)
	sealed, err := p.deps.Secrets.Seal(passPurpose, []byte(made.TemporaryAccessPass))
	if err != nil {
		return "", fmt.Errorf("entra: seal the pass: %w", err)
	}
	err = db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.SetAccountPass(ctx, store.SetAccountPassParams{PassSealed: sealed, PassExpiresAt: &expires, Now: now, ObjectID: id}); err != nil {
			return fmt.Errorf("entra: keep the pass: %w", err)
		}
		if err := q.ResetSensitiveDenied(ctx); err != nil {
			return fmt.Errorf("entra: end the sensitive streak: %w", err)
		}
		return recordEvent(ctx, q, now, actor, actionPassIssued, &id, upn, []string{"expires_at:" + expires.UTC().Format(time.RFC3339)})
	})
	return "", err
}

// RevealPass answers the user's pass once (contracts.IdentityProvisioner.
// RevealPass): taken and cleared in one statement (TakeAccountPass), so two
// reveals cannot both have it; opened and answered with its expiry; audited
// entra.pass.revealed with the actor and the object id, never the pass.
// ErrPassGone when none waits, or when it has expired — cleared all the
// same. It reads the module's own row alone: no call to the tenant, so it
// needs no connection.
func (p *provisioner) RevealPass(ctx context.Context, actor contracts.Actor, externalID string) (contracts.Pass, error) {
	if !actor.Valid() {
		return contracts.Pass{}, contracts.ErrInvalidActor
	}
	id, ok := objectID(externalID)
	if !ok {
		return contracts.Pass{}, contracts.ErrPassGone
	}
	if p.deps.Secrets == nil {
		return contracts.Pass{}, errors.New("entra: no secrets box to open the pass with")
	}
	now := p.client.now()
	var (
		pass    contracts.Pass
		expired bool
	)
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		taken, err := q.TakeAccountPass(ctx, store.TakeAccountPassParams{Now: now, ObjectID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return contracts.ErrPassGone
		} else if err != nil {
			return fmt.Errorf("entra: take the pass: %w", err)
		}
		if taken.TakenExpiresAt == nil || !taken.TakenExpiresAt.After(now) {
			expired = true // cleared by the take; committed so it stays so
			return nil
		}
		value, err := p.deps.Secrets.Open(passPurpose, taken.TakenSealed)
		if err != nil {
			return fmt.Errorf("entra: open the pass: %w", err)
		}
		row, err := q.GetAccount(ctx, id)
		if err != nil {
			return fmt.Errorf("entra: read the account: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionPassRevealed, &id, row.UserPrincipalName,
			[]string{"expires_at:" + taken.TakenExpiresAt.UTC().Format(time.RFC3339)}); err != nil {
			return err
		}
		pass = contracts.Pass{Value: string(value), ExpiresAt: *taken.TakenExpiresAt}
		return nil
	})
	switch {
	case err != nil:
		return contracts.Pass{}, err
	case expired:
		return contracts.Pass{}, contracts.ErrPassGone
	}
	return pass, nil
}
