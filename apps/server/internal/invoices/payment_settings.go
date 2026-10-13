package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the payment settings (invoices payments and reminders design
// D14): one row, made by the migration with the installation's reference
// prefix, which every Vipps payment reference starts with and which never
// changes. Pay links need PUBLIC_BASE_URL, Vipps credentials and the seller's
// two URLs; on-site payment is kontantsalg on a kontantfaktura (D17), and
// turning it on needs the caller to acknowledge the kontantsalg notice, which
// is recorded with who and when and cleared when it is turned off.

const (
	invalidPaymentSettingsTitle = "Invalid payment settings"
	publicURLMissingTitle       = "PUBLIC_BASE_URL is not set"
	// maxPaymentSettingsURLLength is the URLs' column width.
	maxPaymentSettingsURLLength = 500
)

// paymentSettingsWire is the row on the wire.
func paymentSettingsWire(row store.InvoicesPaymentSetting) gen.InvoicesPaymentSettings {
	return gen.InvoicesPaymentSettings{
		ReferencePrefix: row.ReferencePrefix, PayLinksEnabled: row.PayLinksEnabled,
		TermsUrl: row.TermsUrl, PrivacyUrl: row.PrivacyUrl, OnSitePaymentsEnabled: row.OnSitePaymentsEnabled,
		KontantsalgAcknowledgedBy: row.KontantsalgAcknowledgedByUserID, KontantsalgAcknowledgedAt: row.KontantsalgAcknowledgedAt,
		Revision: row.Revision, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedByUserID,
	}
}

// paymentSettingsRequest is a PUT body that passed.
type paymentSettingsRequest struct {
	payLinks, onSite, acknowledged bool
	termsURL, privacyURL           *string
	revision                       int32
}

// parsePaymentSettings checks a PUT body, every failure collected. Every
// field is required — absent is a 400 on it, and so is null but for the two
// URLs — so a client can never reset one by leaving it out.
func parsePaymentSettings(body gen.InvoicesPaymentSettingsRequest) (paymentSettingsRequest, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }
	decode := func(field string, raw json.RawMessage, v any, wrongType string) {
		switch {
		case len(raw) == 0 || string(raw) == "null":
			add(field, field+" is required")
		case json.Unmarshal(raw, v) != nil:
			add(field, wrongType)
		}
	}
	link := func(field string, raw json.RawMessage) *string {
		if len(raw) == 0 {
			add(field, field+" is required; send null while there is none")
			return nil
		}
		if string(raw) == "null" {
			return nil
		}
		var text string
		if json.Unmarshal(raw, &text) != nil || !isHTTPSURL(text) {
			add(field, field+" is an absolute https URL")
			return nil
		}
		if utf8.RuneCountInString(text) > maxPaymentSettingsURLLength {
			add(field, fmt.Sprintf("%s is at most %d characters", field, maxPaymentSettingsURLLength))
			return nil
		}
		return &text
	}
	var in paymentSettingsRequest
	decode("payLinksEnabled", body.PayLinksEnabled, &in.payLinks, "payLinksEnabled is true or false")
	decode("onSitePaymentsEnabled", body.OnSitePaymentsEnabled, &in.onSite, "onSitePaymentsEnabled is true or false")
	decode("revision", body.Revision, &in.revision, "revision is the whole number the settings were read at")
	in.termsURL = link("termsUrl", body.TermsUrl)
	in.privacyURL = link("privacyUrl", body.PrivacyUrl)
	in.acknowledged = body.AcknowledgeKontantsalg != nil && *body.AcknowledgeKontantsalg
	if in.payLinks {
		if in.termsURL == nil && len(errs["termsUrl"]) == 0 {
			add("termsUrl", "Pay links need the sales terms' URL")
		}
		if in.privacyURL == nil && len(errs["privacyUrl"]) == 0 {
			add("privacyUrl", "Pay links need the privacy notice's URL")
		}
	}
	return in, errs
}

// isHTTPSURL is an absolute https URL with a host and nothing but a path,
// query and fragment after it.
func isHTTPSURL(text string) bool {
	u, err := url.Parse(text)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Opaque == ""
}

// GetInvoicesSettingsPayments Get the payment settings
// (GET /api/v1/invoices/settings/payments)
func (s *server) GetInvoicesSettingsPayments(ctx context.Context, _ gen.GetInvoicesSettingsPaymentsRequestObject) (gen.GetInvoicesSettingsPaymentsResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetPaymentSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the payment settings: %w", err)
	}
	return gen.GetInvoicesSettingsPayments200JSONResponse(paymentSettingsWire(row)), nil
}

// PutInvoicesSettingsPayments Change the payment settings
// (PUT /api/v1/invoices/settings/payments)
//
// The row is taken FOR UPDATE — its only lock — and the revision, PUBLIC_BASE_URL
// and the credentials' presence are judged under it; the credentials row is
// read, never locked. A feature already on is not judged again: turning
// something on is what needs what it needs, so a manager can still change
// the URLs of an installation that lost its credentials.
func (s *server) PutInvoicesSettingsPayments(ctx context.Context, req gen.PutInvoicesSettingsPaymentsRequestObject) (gen.PutInvoicesSettingsPaymentsResponseObject, error) {
	in, errs := parsePaymentSettings(*req.Body)
	if errs != nil {
		return gen.PutInvoicesSettingsPayments400ApplicationProblemPlusJSONResponse(invalid(invalidPaymentSettingsTitle, errs)), nil
	}
	now, by := s.deps.Clock(), callerID(ctx)
	publicURL := s.deps.Config != nil && s.deps.Config.PublicBaseURL != ""

	var refusal *gen.InvoicesConflictProblem
	var needsAcknowledgement bool
	var saved store.InvoicesPaymentSetting
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := lockPaymentSettings(ctx, txq)
		if err != nil {
			return fmt.Errorf("invoices: lock the payment settings: %w", err)
		}
		turnsOnLinks := in.payLinks && !current.PayLinksEnabled
		turnsOnSite := in.onSite && !current.OnSitePaymentsEnabled
		if turnsOnSite && !in.acknowledged {
			needsAcknowledgement = true
			return errRefused
		}
		if current.Revision != in.revision {
			refusal = ptr(revisionConflict("Payment settings", current.Revision, in.revision))
			return errRefused
		}
		if turnsOnLinks && !publicURL {
			refusal = ptr(conflict(codePublicURLMissing, publicURLMissingTitle,
				"Pay links need the address the pay page is reached at from the internet. Ask the operator to set PUBLIC_BASE_URL."))
			return errRefused
		}
		if turnsOnLinks || turnsOnSite {
			if _, err := txq.GetPaymentCredentials(ctx); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("invoices: read the Vipps credentials: %w", err)
				}
				refusal = ptr(conflict(codePaymentsUnavailable, paymentsUnavailableTitle,
					"No Vipps credentials are stored. Store the sales unit and its keys first."))
				return errRefused
			}
		}
		params := store.UpdatePaymentSettingsParams{
			PayLinksEnabled: in.payLinks, TermsUrl: in.termsURL, PrivacyUrl: in.privacyURL,
			OnSitePaymentsEnabled: in.onSite, Now: now, By: by, Revision: in.revision,
		}
		// Turned on: this caller acknowledged now; kept on: the first
		// acknowledgement stays; off: none.
		switch {
		case turnsOnSite:
			params.AcknowledgedBy, params.AcknowledgedAt = &by, &now
		case in.onSite:
			params.AcknowledgedBy, params.AcknowledgedAt = current.KontantsalgAcknowledgedByUserID, current.KontantsalgAcknowledgedAt
		}
		if saved, err = txq.UpdatePaymentSettings(ctx, params); err != nil {
			return fmt.Errorf("invoices: change the payment settings: %w", err)
		}
		return nil
	})
	switch {
	case needsAcknowledgement:
		return gen.PutInvoicesSettingsPayments400ApplicationProblemPlusJSONResponse(invalid(invalidPaymentSettingsTitle,
			fieldError("acknowledgeKontantsalg", "Turning on-site payment on needs the kontantsalg notice acknowledged: send acknowledgeKontantsalg true."))), nil
	case refusal != nil:
		return gen.PutInvoicesSettingsPayments409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.PutInvoicesSettingsPayments200JSONResponse(paymentSettingsWire(saved)), nil
}
