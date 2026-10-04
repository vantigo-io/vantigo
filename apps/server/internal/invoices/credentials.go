package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the access point's credentials (EHF and KID design D7): one
// row of their own, invoices.access_point_credentials, off the settings row
// every issue reads FOR SHARE. The provider and its legal entity are stored
// in clear; the API key is sealed by the secrets box under its own purpose
// and is never answered, logged or sent anywhere but to the provider.

const (
	// accessPointCredentialPurpose is the secrets box purpose the key is
	// sealed under.
	accessPointCredentialPurpose = "invoices/access-point-credential"
	// providerStorecove is the one provider so far.
	providerStorecove = "storecove"
	// maxAccessPointKeyLength bounds the key a PUT takes, in characters.
	maxAccessPointKeyLength = 1000

	codeTransmissionsActive    = "transmissions_active"
	codeEhfUnavailable         = "ehf_unavailable"
	invalidAccessPointTitle    = "Invalid access point credentials"
	transmissionsActiveTitle   = "Transmissions are in flight"
	ehfUnavailableTitle        = "E-invoicing is unavailable"
	accessPointResultOK        = "ok"
	accessPointResultRefused   = "unauthorized"
	accessPointResultUnreached = "unreachable"
)

// accessPointSettings is settings_json: what the provider needs beside the
// key, none of it secret.
type accessPointSettings struct {
	LegalEntityID int64 `json:"legalEntityId"`
}

var (
	// errNoAccessPoint is accessPoint's answer when no credentials are
	// stored.
	errNoAccessPoint = errors.New("invoices: no access point credentials are stored")
	// errAccessPointKeyUnreadable is a stored key the secrets box cannot
	// open: APP_SECRET changed, or the row was altered.
	errAccessPointKeyUnreadable = errors.New("invoices: the stored access point key cannot be opened")
)

// transmissionsActive is the 409 a DELETE or a provider switch answers while
// the credentials still serve a transmission.
func transmissionsActive() gen.InvoicesConflictProblem {
	return conflict(codeTransmissionsActive, transmissionsActiveTitle,
		"A document is queued, submitted or awaiting confirmation through this access point. Wait until it is delivered or failed, or resolve it, before removing or switching the credentials.")
}

// accessPointKeyUnreadable is the 503 an operation answers when the stored key
// cannot be opened.
func accessPointKeyUnreadable() gen.InvoicesConflictProblem {
	c := conflict(codeEhfUnavailable, ehfUnavailableTitle,
		"The stored access point key cannot be read on this server. Enter the key again.")
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// accessPointRequest is a PUT body that passed: key is nil when the stored
// key is to be kept.
type accessPointRequest struct {
	provider      string
	legalEntityID int64
	key           *string
	settingsJSON  string
}

// parseAccessPointRequest checks a PUT body field by field.
func parseAccessPointRequest(body gen.InvoicesAccessPointRequest) (accessPointRequest, map[string][]string) {
	var errs map[string][]string
	in := accessPointRequest{provider: strings.TrimSpace(body.Provider), legalEntityID: body.LegalEntityId}
	if in.provider != providerStorecove {
		errs = withFieldError(errs, "provider", "Must be storecove.")
	}
	// NewStorecove takes an int, and Storecove's ids are 32-bit.
	if in.legalEntityID < 1 || in.legalEntityID > math.MaxInt32 {
		errs = withFieldError(errs, "legalEntityId", "Must be a positive integer.")
	}
	if body.ApiKey != nil {
		key := strings.TrimSpace(*body.ApiKey)
		switch {
		case key == "":
			errs = withFieldError(errs, "apiKey", "Must not be empty. Leave it out to keep the stored key.")
		case utf8.RuneCountInString(key) > maxAccessPointKeyLength:
			errs = withFieldError(errs, "apiKey", fmt.Sprintf("At most %d characters.", maxAccessPointKeyLength))
		default:
			in.key = &key
		}
	}
	// A struct of one int64 always encodes.
	settings, _ := json.Marshal(accessPointSettings{LegalEntityID: in.legalEntityID})
	in.settingsJSON = string(settings)
	return in, errs
}

// accessPointResponse is the row as a client may see it: never the key.
func accessPointResponse(row store.InvoicesAccessPointCredential) (gen.InvoicesAccessPointResponse, error) {
	settings, err := accessPointSettingsOf(row)
	if err != nil {
		return gen.InvoicesAccessPointResponse{}, err
	}
	return gen.InvoicesAccessPointResponse{
		Provider: row.Provider, LegalEntityId: settings.LegalEntityID,
		HasCredentials: row.SecretCiphertext != "", RejectedAt: row.RejectedAt,
	}, nil
}

func accessPointSettingsOf(row store.InvoicesAccessPointCredential) (accessPointSettings, error) {
	var settings accessPointSettings
	if err := json.Unmarshal([]byte(row.SettingsJson), &settings); err != nil {
		return accessPointSettings{}, fmt.Errorf("invoices: decode the access point settings: %w", err)
	}
	return settings, nil
}

// flagUnreadableAccessPointKey records a key the box could not open: logged
// at error — the operator must enter it again — and rejected_at set, which
// meta reports. It writes on the pool, never inside a caller's transaction,
// so a rolled-back operation still leaves the flag.
func (s *server) flagUnreadableAccessPointKey(ctx context.Context) {
	s.deps.Logger.ErrorContext(ctx, "invoices: the stored access point key cannot be opened; it must be entered again")
	if err := store.New(s.deps.Pool).MarkAccessPointRejected(ctx, s.deps.Clock()); err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: flag the access point key as rejected", "error", err.Error())
	}
}

// PutInvoicesSettingsAccessPoint Store the access-point credentials
// (PUT /api/v1/invoices/settings/access-point)
//
// The row is read FOR UPDATE inside the transaction, and a kept key is
// opened and sealed again from that read: a key sealed from a read before the
// transaction would write a concurrent PUT's rotated key back to the old one
// (communications' channel credentials learned it). A new key is sealed
// before the transaction — sealing reads nothing.
func (s *server) PutInvoicesSettingsAccessPoint(ctx context.Context, req gen.PutInvoicesSettingsAccessPointRequestObject) (gen.PutInvoicesSettingsAccessPointResponseObject, error) {
	in, errs := parseAccessPointRequest(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesSettingsAccessPoint400ApplicationProblemPlusJSONResponse(invalid(invalidAccessPointTitle, errs)), nil
	}
	var sealed string
	if in.key != nil {
		var err error
		if sealed, err = s.deps.Secrets.SealString(accessPointCredentialPurpose, *in.key); err != nil {
			return nil, fmt.Errorf("invoices: seal the access point key: %w", err)
		}
	}

	var refusal *gen.InvoicesConflictProblem
	var missingKey, unreadable bool
	var saved store.InvoicesAccessPointCredential
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockAccessPointCredentials(ctx)
		stored := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("invoices: lock the access point credentials: %w", err)
		}
		if stored && current.Provider != in.provider {
			active, err := txq.ActiveTransmissionsCount(ctx)
			if err != nil {
				return fmt.Errorf("invoices: count the transmissions in flight: %w", err)
			}
			if active > 0 {
				refusal = ptr(transmissionsActive())
				return errRefused
			}
		}
		if in.key == nil {
			if !stored {
				missingKey = true
				return errRefused
			}
			key, err := s.deps.Secrets.OpenString(accessPointCredentialPurpose, current.SecretCiphertext)
			if err != nil {
				unreadable = true
				return errRefused
			}
			if sealed, err = s.deps.Secrets.SealString(accessPointCredentialPurpose, key); err != nil {
				return fmt.Errorf("invoices: seal the access point key: %w", err)
			}
		}
		saved, err = txq.UpsertAccessPointCredentials(ctx, store.UpsertAccessPointCredentialsParams{
			Provider: in.provider, SettingsJson: in.settingsJSON, SecretCiphertext: sealed, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: store the access point credentials: %w", err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PutInvoicesSettingsAccessPoint409ApplicationProblemPlusJSONResponse(*refusal), nil
	case missingKey:
		return gen.PutInvoicesSettingsAccessPoint400ApplicationProblemPlusJSONResponse(invalid(invalidAccessPointTitle,
			fieldError("apiKey", "Required: no key is stored yet."))), nil
	case unreadable:
		s.flagUnreadableAccessPointKey(ctx)
		return gen.PutInvoicesSettingsAccessPoint503ApplicationProblemPlusJSONResponse(accessPointKeyUnreadable()), nil
	case err != nil:
		return nil, err
	}
	resp, err := accessPointResponse(saved)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettingsAccessPoint200JSONResponse(resp), nil
}

// DeleteInvoicesSettingsAccessPoint Remove the access-point credentials
// (DELETE /api/v1/invoices/settings/access-point)
func (s *server) DeleteInvoicesSettingsAccessPoint(ctx context.Context, _ gen.DeleteInvoicesSettingsAccessPointRequestObject) (gen.DeleteInvoicesSettingsAccessPointResponseObject, error) {
	var refused bool
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := txq.LockAccessPointCredentials(ctx); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("invoices: lock the access point credentials: %w", err)
		}
		active, err := txq.ActiveTransmissionsCount(ctx)
		if err != nil {
			return fmt.Errorf("invoices: count the transmissions in flight: %w", err)
		}
		if active > 0 {
			refused = true
			return errRefused
		}
		if _, err := txq.DeleteAccessPointCredentials(ctx); err != nil {
			return fmt.Errorf("invoices: remove the access point credentials: %w", err)
		}
		return nil
	})
	if refused {
		return gen.DeleteInvoicesSettingsAccessPoint409ApplicationProblemPlusJSONResponse(transmissionsActive()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInvoicesSettingsAccessPoint204Response{}, nil
}

// PostInvoicesSettingsAccessPointVerify Verify the access-point credentials
// (POST /api/v1/invoices/settings/access-point/verify)
//
// One call to the provider, outside any transaction. An ok answer clears a
// refusal on record; an unauthorized one records it — the same fact the
// worker records from a 401 or 403 (D9).
func (s *server) PostInvoicesSettingsAccessPointVerify(ctx context.Context, _ gen.PostInvoicesSettingsAccessPointVerifyRequestObject) (gen.PostInvoicesSettingsAccessPointVerifyResponseObject, error) {
	ap, err := s.accessPoint(ctx)
	switch {
	case errors.Is(err, errNoAccessPoint):
		return gen.PostInvoicesSettingsAccessPointVerify409ApplicationProblemPlusJSONResponse(conflict(codeEhfUnavailable, ehfUnavailableTitle,
			"No access point credentials are stored, so there is nothing to verify.")), nil
	case errors.Is(err, errAccessPointKeyUnreadable):
		return gen.PostInvoicesSettingsAccessPointVerify503ApplicationProblemPlusJSONResponse(accessPointKeyUnreadable()), nil
	case err != nil:
		return nil, err
	}
	q := store.New(s.deps.Pool)
	result := accessPointResultOK
	switch err := ap.Verify(ctx); {
	case err == nil:
		if err := q.ClearAccessPointRejected(ctx); err != nil {
			return nil, fmt.Errorf("invoices: clear the access point refusal: %w", err)
		}
	case errors.Is(err, accesspoint.ErrUnauthorized):
		result = accessPointResultRefused
		if err := q.MarkAccessPointRejected(ctx, s.deps.Clock()); err != nil {
			return nil, fmt.Errorf("invoices: flag the access point key as rejected: %w", err)
		}
	default:
		result = accessPointResultUnreached
		s.deps.Logger.WarnContext(ctx, "invoices: the access point could not be verified", "error", err.Error())
	}
	return gen.PostInvoicesSettingsAccessPointVerify200JSONResponse(gen.InvoicesAccessPointVerifyResponse{Result: result}), nil
}
