package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// This file is the Vipps MobilePay credentials (invoices payments and
// reminders design D14): one row of their own,
// invoices.payment_provider_credentials, the access point's shape
// (credentials.go). The sales unit (MSN) is stored in clear in
// settings_json; the three keys are sealed together by the secrets box under
// their own purpose and are never answered, logged or sent anywhere but to
// Vipps. One sales unit per installation.

const (
	// paymentProviderCredentialPurpose is the secrets box purpose the keys
	// are sealed under (plan reading 3: the caller's constant). The webhook
	// secret's purpose, invoices/payment-webhook-secret, arrives with the
	// webhook registration.
	paymentProviderCredentialPurpose = "invoices/payment-provider-credential"
	// providerVipps is the one payment provider.
	providerVipps = "vipps"
	// maxVippsKeyLength bounds each key a PUT takes, in characters.
	maxVippsKeyLength = 1000

	invalidVippsTitle        = "Invalid Vipps credentials"
	paymentsUnavailableTitle = "Vipps payments are unavailable"
	paymentAttemptsTitle     = "Vipps payments are in flight"
	vippsResultOK            = gen.Ok
	vippsResultRefused       = gen.Unauthorized
	vippsResultUnreached     = gen.Unreachable
)

// msnPattern is a Merchant Serial Number: 1-10 digits.
var msnPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

// vippsSettings is settings_json: the sales unit, not secret.
type vippsSettings struct {
	MSN string `json:"msn"`
}

// vippsKeys is the sealed JSON: the sales unit's three keys.
type vippsKeys struct {
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	SubscriptionKey string `json:"subscriptionKey"`
}

var (
	// errPaymentsSwitchedOff is paymentProvider's answer when
	// INVOICES_VIPPS_ENABLED is off: no client is made (m12).
	errPaymentsSwitchedOff = errors.New("invoices: Vipps payments are switched off")
	// errNoPaymentCredentials is paymentProvider's answer when no
	// credentials are stored.
	errNoPaymentCredentials = errors.New("invoices: no Vipps credentials are stored")
	// errPaymentKeysUnreadable is stored keys the secrets box cannot open:
	// APP_SECRET changed, or the row was altered.
	errPaymentKeysUnreadable = errors.New("invoices: the stored Vipps keys cannot be opened")
)

// paymentsUnavailable is the problem an operation answers when paymentProvider
// could not make a provider: 503 when Vipps is switched off on this
// installation or the stored keys cannot be opened, 409 when none are stored.
// It answers false for any other error.
func paymentsUnavailable(err error) (gen.InvoicesConflictProblem, bool) {
	var detail string
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, errPaymentsSwitchedOff):
		detail = "Vipps payments are switched off on this installation (INVOICES_VIPPS_ENABLED)."
	case errors.Is(err, errPaymentKeysUnreadable):
		detail = "The stored Vipps keys cannot be read on this server. Enter the keys again."
	case errors.Is(err, errNoPaymentCredentials):
		detail, status = "No Vipps credentials are stored.", http.StatusConflict
	default:
		return gen.InvoicesConflictProblem{}, false
	}
	c := conflict(codePaymentsUnavailable, paymentsUnavailableTitle, detail)
	c.Status = ptr(int32(status))
	return c, true
}

// paymentAttemptsActive is the 409 a sales-unit change or a DELETE answers
// while an attempt is live (I20). The keys of the same sales unit may
// change at any time: attempts are read under their MSN, and refusing a
// rotation would hold keys Vipps refuses — and every live attempt — for ever.
func paymentAttemptsActive() gen.InvoicesConflictProblem {
	return conflict(codePaymentAttemptsActive, paymentAttemptsTitle,
		"A Vipps payment is being created, awaits the payer, is authorized, is being captured or released, or its outcome is unknown. "+
			"Wait until it is finished, or abandon it, before changing the sales unit (MSN) or removing the keys. New keys of the same sales unit may be stored at any time.")
}

// vippsRequest is a PUT body that passed: a nil key keeps the stored one.
type vippsRequest struct {
	msn                                  string
	clientID, clientSecret, subscription *string
}

// parseVippsRequest checks a PUT body field by field.
func parseVippsRequest(body gen.InvoicesVippsSettingsRequest) (vippsRequest, map[string][]string) {
	var errs map[string][]string
	in := vippsRequest{msn: strings.TrimSpace(body.Msn)}
	if !msnPattern.MatchString(in.msn) {
		errs = withFieldError(errs, "msn", "The sales unit's MSN is 1-10 digits.")
	}
	key := func(field string, raw *string) *string {
		if raw == nil {
			return nil
		}
		v := strings.TrimSpace(*raw)
		switch {
		case v == "":
			errs = withFieldError(errs, field, "Must not be empty. Leave it out to keep the stored one.")
		case utf8.RuneCountInString(v) > maxVippsKeyLength:
			errs = withFieldError(errs, field, fmt.Sprintf("At most %d characters.", maxVippsKeyLength))
		default:
			return &v
		}
		return nil
	}
	in.clientID = key("clientId", body.ClientId)
	in.clientSecret = key("clientSecret", body.ClientSecret)
	in.subscription = key("subscriptionKey", body.SubscriptionKey)
	return in, errs
}

// vippsResponse is the row as a client may see it: never a key.
func vippsResponse(row store.InvoicesPaymentProviderCredential) (gen.InvoicesVippsSettings, error) {
	settings, err := vippsSettingsOf(row)
	if err != nil {
		return gen.InvoicesVippsSettings{}, err
	}
	return gen.InvoicesVippsSettings{Msn: &settings.MSN, HasCredentials: row.SecretCiphertext != "", RejectedAt: row.RejectedAt}, nil
}

func vippsSettingsOf(row store.InvoicesPaymentProviderCredential) (vippsSettings, error) {
	var settings vippsSettings
	if err := json.Unmarshal([]byte(row.SettingsJson), &settings); err != nil {
		return vippsSettings{}, fmt.Errorf("invoices: decode the Vipps settings: %w", err)
	}
	return settings, nil
}

// openVippsKeys opens the sealed keys; any failure, an empty key included,
// is errPaymentKeysUnreadable.
func (s *server) openVippsKeys(ciphertext string) (vippsKeys, error) {
	clear, err := s.deps.Secrets.OpenString(paymentProviderCredentialPurpose, ciphertext)
	if err != nil {
		return vippsKeys{}, errPaymentKeysUnreadable
	}
	var keys vippsKeys
	if json.Unmarshal([]byte(clear), &keys) != nil || keys.ClientID == "" || keys.ClientSecret == "" || keys.SubscriptionKey == "" {
		return vippsKeys{}, errPaymentKeysUnreadable
	}
	return keys, nil
}

// flagPaymentCredentialsRejected records keys Vipps refused or the box could
// not open, against the credentials' version the caller read them from
// (their updated_at): a refusal of keys a later PUT replaced flags nothing.
// Unreadable keys are also logged at error — the operator must enter them
// again. It writes on the pool, never inside a caller's transaction, so a
// rolled-back operation still leaves the flag.
func (s *server) flagPaymentCredentialsRejected(ctx context.Context, unreadable bool, version time.Time) {
	if unreadable {
		s.deps.Logger.ErrorContext(ctx, "invoices: the stored Vipps keys cannot be opened; they must be entered again")
	}
	err := store.New(s.deps.Pool).MarkPaymentCredentialsRejected(ctx, store.MarkPaymentCredentialsRejectedParams{
		Now: s.deps.Clock(), Version: version,
	})
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: flag the Vipps credentials as rejected", "error", err.Error())
	}
}

// GetInvoicesSettingsVipps Get the Vipps credentials
// (GET /api/v1/invoices/settings/vipps)
func (s *server) GetInvoicesSettingsVipps(ctx context.Context, _ gen.GetInvoicesSettingsVippsRequestObject) (gen.GetInvoicesSettingsVippsResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetPaymentCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetInvoicesSettingsVipps200JSONResponse(gen.InvoicesVippsSettings{HasCredentials: false}), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read the Vipps credentials: %w", err)
	}
	resp, err := vippsResponse(row)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSettingsVipps200JSONResponse(resp), nil
}

// vippsPutAfterWrite, when a test installs it, runs inside a credentials
// PUT's transaction right after the row is written; nil in production.
var vippsPutAfterWrite func(ctx context.Context, tx pgx.Tx)

// vippsPutOutcome is what one run of a credentials PUT's transaction decided.
type vippsPutOutcome struct {
	missing    map[string][]string
	active     bool
	unreadable bool
	// raced is a first PUT whose insert met a row another first PUT wrote
	// meanwhile: nothing was written, and the PUT runs again on the locked
	// path.
	raced bool
	// version is the unreadable row's updated_at.
	version time.Time
	saved   store.InvoicesPaymentProviderCredential
}

// PutInvoicesSettingsVipps Store the Vipps credentials
// (PUT /api/v1/invoices/settings/vipps)
//
// The row is read FOR UPDATE — the one lock this takes — and an omitted key
// is opened and sealed again from that read, so a concurrent PUT's rotated
// key is never written back to the old one. A change of the sales unit, the
// client id or the subscription key is judged against the same read and
// refused while an attempt is live, counted under the lock: an attempt's
// create shares the row after its invoice (plan reading 8), so no attempt can
// be made under the old sales unit after the count. Stored keys that cannot
// be opened while all three are given are counted as a change: nothing proves
// they are the same.
//
// With no row there is nothing to lock: the first PUT inserts, and does
// nothing when a concurrent first PUT's row is there — its insert waits for
// that transaction — and runs again, once, on the locked path, so the second
// of two first PUTs is judged like any change and never writes over the
// first unjudged. A row gone again by then is a 409 with no code.
func (s *server) PutInvoicesSettingsVipps(ctx context.Context, req gen.PutInvoicesSettingsVippsRequestObject) (gen.PutInvoicesSettingsVippsResponseObject, error) {
	in, errs := parseVippsRequest(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesSettingsVipps400ApplicationProblemPlusJSONResponse(invalid(invalidVippsTitle, errs)), nil
	}
	var out vippsPutOutcome
	var err error
	for run := 0; run < 2; run++ {
		out, err = s.putVippsOnce(ctx, in)
		if !out.raced {
			break
		}
	}
	switch {
	case out.missing != nil:
		return gen.PutInvoicesSettingsVipps400ApplicationProblemPlusJSONResponse(invalid(invalidVippsTitle, out.missing)), nil
	case out.active:
		return gen.PutInvoicesSettingsVipps409ApplicationProblemPlusJSONResponse(paymentAttemptsActive()), nil
	case out.raced:
		title, detail := "Vipps credentials changed meanwhile",
			"The Vipps credentials were stored and removed again while this request ran. Read them and try again."
		return gen.PutInvoicesSettingsVipps409ApplicationProblemPlusJSONResponse(gen.InvoicesConflictProblem{
			Title: &title, Detail: &detail, Status: ptr(int32(http.StatusConflict)),
		}), nil
	case out.unreadable:
		s.flagPaymentCredentialsRejected(ctx, true, out.version)
		problem, _ := paymentsUnavailable(errPaymentKeysUnreadable)
		return gen.PutInvoicesSettingsVipps503ApplicationProblemPlusJSONResponse(problem), nil
	case err != nil:
		return nil, err
	}
	resp, err := vippsResponse(out.saved)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettingsVipps200JSONResponse(resp), nil
}

// putVippsOnce is one run of a credentials PUT's transaction.
func (s *server) putVippsOnce(ctx context.Context, in vippsRequest) (vippsPutOutcome, error) {
	// A struct of one string always encodes.
	settingsJSON, _ := json.Marshal(vippsSettings{MSN: in.msn})
	var out vippsPutOutcome
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		current, err := lockPaymentCredentials(ctx, txq)
		stored := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("invoices: lock the Vipps credentials: %w", err)
		}
		var keys vippsKeys
		// changed is a change of sales unit: only that is refused while an
		// attempt is live. New keys of the same MSN are stored whatever is
		// live — the version moves, so a create built from the old keys
		// refuses on the mismatch, and a refusal stays the old version's.
		changed := false
		if stored {
			settings, err := vippsSettingsOf(current)
			if err != nil {
				return err
			}
			changed = settings.MSN != in.msn
			if keys, err = s.openVippsKeys(current.SecretCiphertext); err != nil {
				if in.clientID == nil || in.clientSecret == nil || in.subscription == nil {
					out.unreadable, out.version = true, current.UpdatedAt
					return errRefused
				}
			}
		} else {
			for field, v := range map[string]*string{"clientId": in.clientID, "clientSecret": in.clientSecret, "subscriptionKey": in.subscription} {
				if v == nil {
					out.missing = withFieldError(out.missing, field, "Required: no Vipps keys are stored yet.")
				}
			}
			if out.missing != nil {
				return errRefused
			}
		}
		if in.clientID != nil {
			keys.ClientID = *in.clientID
		}
		if in.subscription != nil {
			keys.SubscriptionKey = *in.subscription
		}
		if in.clientSecret != nil {
			keys.ClientSecret = *in.clientSecret
		}
		if stored && changed {
			live, err := txq.LivePaymentAttemptsCount(ctx)
			if err != nil {
				return fmt.Errorf("invoices: count the live payment attempts: %w", err)
			}
			if live > 0 {
				out.active = true
				return errRefused
			}
		}
		// Three strings always encode.
		clear, _ := json.Marshal(keys)
		sealed, err := s.deps.Secrets.SealString(paymentProviderCredentialPurpose, string(clear))
		if err != nil {
			return fmt.Errorf("invoices: seal the Vipps keys: %w", err)
		}
		if stored {
			out.saved, err = txq.UpdatePaymentCredentials(ctx, store.UpdatePaymentCredentialsParams{
				Provider: providerVipps, SettingsJson: string(settingsJSON), SecretCiphertext: sealed, Now: s.deps.Clock(),
			})
		} else {
			out.saved, err = txq.InsertPaymentCredentials(ctx, store.InsertPaymentCredentialsParams{
				Provider: providerVipps, SettingsJson: string(settingsJSON), SecretCiphertext: sealed, Now: s.deps.Clock(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				out.raced = true
				return errRefused
			}
		}
		if err != nil {
			return fmt.Errorf("invoices: store the Vipps credentials: %w", err)
		}
		if hook := vippsPutAfterWrite; hook != nil {
			hook(ctx, tx)
		}
		return nil
	})
	return out, err
}

// DeleteInvoicesSettingsVipps Remove the Vipps credentials
// (DELETE /api/v1/invoices/settings/vipps)
func (s *server) DeleteInvoicesSettingsVipps(ctx context.Context, _ gen.DeleteInvoicesSettingsVippsRequestObject) (gen.DeleteInvoicesSettingsVippsResponseObject, error) {
	var active bool
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := lockPaymentCredentials(ctx, txq); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("invoices: lock the Vipps credentials: %w", err)
		}
		live, err := txq.LivePaymentAttemptsCount(ctx)
		if err != nil {
			return fmt.Errorf("invoices: count the live payment attempts: %w", err)
		}
		if live > 0 {
			active = true
			return errRefused
		}
		if _, err := txq.DeletePaymentCredentials(ctx); err != nil {
			return fmt.Errorf("invoices: remove the Vipps credentials: %w", err)
		}
		return nil
	})
	if active {
		return gen.DeleteInvoicesSettingsVipps409ApplicationProblemPlusJSONResponse(paymentAttemptsActive()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInvoicesSettingsVipps204Response{}, nil
}

// vippsVerifyBeforeClear, when a test installs it, runs after Vipps accepted
// a verify and before the refusal is cleared; nil in production.
var vippsVerifyBeforeClear func()

// PostInvoicesSettingsVippsVerify Verify the Vipps credentials
// (POST /api/v1/invoices/settings/vipps/verify)
//
// One provider call, outside any transaction, through paymentProvider: with
// the switch off nothing is sent. An ok answer clears a refusal on record;
// an unauthorized one is recorded by the provider's wrapper
// (contractscalls.go), as every 401 or 403 is.
func (s *server) PostInvoicesSettingsVippsVerify(ctx context.Context, _ gen.PostInvoicesSettingsVippsVerifyRequestObject) (gen.PostInvoicesSettingsVippsVerifyResponseObject, error) {
	provider, creds, err := s.paymentProvider(ctx)
	if err != nil {
		problem, ok := paymentsUnavailable(err)
		switch {
		case !ok:
			return nil, err
		case *problem.Status == http.StatusConflict:
			return gen.PostInvoicesSettingsVippsVerify409ApplicationProblemPlusJSONResponse(problem), nil
		default:
			return gen.PostInvoicesSettingsVippsVerify503ApplicationProblemPlusJSONResponse(problem), nil
		}
	}
	result := vippsResultOK
	switch err := provider.Verify(ctx); {
	case err == nil:
		if hook := vippsVerifyBeforeClear; hook != nil {
			hook()
		}
		// Only the version verified: keys a PUT replaced meanwhile keep
		// whatever was recorded against them.
		if err := store.New(s.deps.Pool).ClearPaymentCredentialsRejected(ctx, creds.version); err != nil {
			return nil, fmt.Errorf("invoices: clear the Vipps refusal: %w", err)
		}
	case errors.Is(err, payments.ErrUnauthorized):
		result = vippsResultRefused
	default:
		result = vippsResultUnreached
		s.deps.Logger.WarnContext(ctx, "invoices: the Vipps credentials could not be verified", "error", err.Error())
	}
	return gen.PostInvoicesSettingsVippsVerify200JSONResponse(gen.InvoicesVippsVerifyResponse{Result: result}), nil
}
