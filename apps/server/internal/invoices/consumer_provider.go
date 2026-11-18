package invoices

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the consumer provider's settings (invoices phase 6 design I29,
// plan D3, readings 5-7): Mastercard Payment Services for eFaktura and
// AvtaleGiro. Vantigo is the submitter — its certificate, key and
// WS-Security user are the operator's files (Config) — and the tenant is the
// issuer, named by the seller's organisation number. This row holds what is
// the tenant's: its agreements' states, the AvtaleGiro creditor account, the
// names the bank shows, and optionally a Mandate API certificate of its own,
// sealed under its own purpose and never answered. The AvtaleGiro switch and
// its KID check live here too.

const (
	// consumerProviderPurpose is the secrets box purpose the mandate
	// certificate is sealed under.
	consumerProviderPurpose = "invoices/consumer-provider-credential"
	// providerMPS is the one consumer provider so far.
	providerMPS = "mps"

	agreementNone    = "none"
	agreementPending = "pending"
	agreementActive  = "active"

	codeEfakturaUnavailable       = "efaktura_unavailable"
	codeKidAgreementMissing       = "kid_agreement_missing"
	codeAvtaleGiroAgreementAbsent = "avtalegiro_agreement_missing"
	codeAvtaleGiroKidSpaceTaken   = "avtalegiro_kid_space_taken"
	efakturaUnavailableTitle      = "eFaktura is unavailable"
	invalidConsumerProviderTitle  = "Invalid eFaktura settings"
	avtaleGiroRefusedTitle        = "AvtaleGiro cannot be turned on"

	maxBrandName       = 40
	maxAbbreviatedName = 10
	maxMandateCertPEM  = 20000
)

// consumerProviderSettings is settings_json: the tenant's, none of it secret.
type consumerProviderSettings struct {
	EfakturaAgreement   string `json:"efakturaAgreement"`
	AvtalegiroAgreement string `json:"avtalegiroAgreement"`
	CreditorAccount     string `json:"creditorAccount,omitempty"`
	BrandName           string `json:"brandName"`
	AbbreviatedName     string `json:"abbreviatedName,omitempty"`
}

var (
	// errNoConsumerProvider is consumerProvider's answer without a row.
	errNoConsumerProvider = errors.New("invoices: no consumer provider settings are stored")
	// errEfakturaOff is INVOICES_EFAKTURA_ENABLED off.
	errEfakturaOff = errors.New("invoices: eFaktura is switched off on this installation")
	// errSubmitterUnreadable is an operator file that cannot be read, or a
	// sealed mandate certificate the box cannot open.
	errSubmitterUnreadable = errors.New("invoices: the submitter's credentials cannot be read")
)

func consumerProviderSettingsOf(row store.InvoicesConsumerProviderCredential) (consumerProviderSettings, error) {
	var s consumerProviderSettings
	if err := json.Unmarshal([]byte(row.SettingsJson), &s); err != nil {
		return consumerProviderSettings{}, fmt.Errorf("invoices: decode the consumer provider settings: %w", err)
	}
	return s, nil
}

// efakturaSwitchedOn is the operator's live switch.
func (s *server) efakturaSwitchedOn() bool {
	return s.deps.Config != nil && s.deps.Config.InvoicesEfakturaEnabled
}

// efakturaAvailable is whether a document can be sent as eFaktura (plan
// D3): the switch on, the settings stored and their eFaktura agreement
// active. rejected is whether the provider refused the submitter, reported
// beside. Nothing here touches the network.
func (s *server) efakturaAvailable(ctx context.Context, q *store.Queries) (available, rejected bool, settings consumerProviderSettings, err error) {
	row, err := q.GetConsumerProviderCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, consumerProviderSettings{}, nil
	case err != nil:
		return false, false, consumerProviderSettings{}, fmt.Errorf("invoices: read the consumer provider settings: %w", err)
	}
	settings, err = consumerProviderSettingsOf(row)
	if err != nil {
		return false, false, consumerProviderSettings{}, err
	}
	return s.efakturaSwitchedOn() && settings.EfakturaAgreement == agreementActive, row.RejectedAt != nil, settings, nil
}

// efakturaUnavailable is the 503 of an installation that cannot send eFaktura.
func efakturaUnavailable() gen.InvoicesConflictProblem {
	c := conflict(codeEfakturaUnavailable, efakturaUnavailableTitle,
		"This installation cannot send eFaktura: it is switched off, no eFaktura settings are stored, or the eFaktura agreement is not active.")
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// consumerProvider is the provider the stored settings and the operator's
// files open (plan D3), dialling the operator's base URLs through
// Deps.HTTPTransport. Every call on it is reported as ConsumerInvoice.<Method>.
// It reads the row on the pool: it is never called inside withLockedTx, and
// no call on what it answers may be.
func (s *server) consumerProvider(ctx context.Context) (consumerinvoice.Provider, consumerProviderSettings, error) {
	if !s.efakturaSwitchedOn() {
		return nil, consumerProviderSettings{}, errEfakturaOff
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetConsumerProviderCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, consumerProviderSettings{}, errNoConsumerProvider
	case err != nil:
		return nil, consumerProviderSettings{}, fmt.Errorf("invoices: read the consumer provider settings: %w", err)
	}
	if row.Provider != providerMPS {
		return nil, consumerProviderSettings{}, fmt.Errorf("invoices: unknown consumer provider %q", row.Provider)
	}
	settings, err := consumerProviderSettingsOf(row)
	if err != nil {
		return nil, consumerProviderSettings{}, err
	}
	seller, err := q.GetSettings(ctx)
	if err != nil {
		return nil, consumerProviderSettings{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	cfg := s.deps.Config
	cert, err := tls.LoadX509KeyPair(cfg.InvoicesMpsClientCertFile, cfg.InvoicesMpsClientKeyFile)
	if err != nil {
		s.flagConsumerProviderRejected(ctx, "the submitter's certificate or key cannot be read")
		return nil, consumerProviderSettings{}, errSubmitterUnreadable
	}
	password, err := os.ReadFile(cfg.InvoicesMpsPasswordFile)
	if err != nil {
		s.flagConsumerProviderRejected(ctx, "the submitter's password file cannot be read")
		return nil, consumerProviderSettings{}, errSubmitterUnreadable
	}
	var mandateCert *tls.Certificate
	if row.SecretCiphertext != "" {
		pemText, err := s.deps.Secrets.OpenString(consumerProviderPurpose, row.SecretCiphertext)
		if err != nil {
			s.flagConsumerProviderRejected(ctx, "the stored mandate certificate cannot be opened")
			return nil, consumerProviderSettings{}, errSubmitterUnreadable
		}
		c, err := tls.X509KeyPair([]byte(pemText), []byte(pemText))
		if err != nil {
			s.flagConsumerProviderRejected(ctx, "the stored mandate certificate does not parse")
			return nil, consumerProviderSettings{}, errSubmitterUnreadable
		}
		mandateCert = &c
	}
	creditor := ""
	if settings.AvtalegiroAgreement != agreementNone {
		creditor = settings.CreditorAccount
	}
	p, err := mps.New(mps.Config{
		EfakturaBaseURL: cfg.InvoicesMpsEfakturaBaseURL, MandateBaseURL: cfg.InvoicesMpsMandateBaseURL,
		Certificate: cert, MandateCertificate: mandateCert,
		Username: cfg.InvoicesMpsUsername, Password: strings.TrimSpace(string(password)),
		Issuer: seller.OrganisationNumber, CreditorAccount: creditor,
		Transport: s.deps.HTTPTransport, Clock: s.deps.Clock,
	})
	if err != nil {
		return nil, consumerProviderSettings{}, err
	}
	return notedConsumerProvider{p}, settings, nil
}

// flagConsumerProviderRejected records a submitter the box or the files
// could not open: logged at error, without the file's name or content, and
// rejected_at set, on the pool.
func (s *server) flagConsumerProviderRejected(ctx context.Context, what string) {
	s.deps.Logger.ErrorContext(ctx, "invoices: the consumer provider cannot be opened: "+what)
	if err := store.New(s.deps.Pool).MarkConsumerProviderRejected(ctx, s.deps.Clock()); err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: flag the consumer provider as rejected", "error", err.Error())
	}
}

// notedConsumerProvider reports every call before making it.
type notedConsumerProvider struct{ p consumerinvoice.Provider }

func (n notedConsumerProvider) Lookup(ctx context.Context, c []consumerinvoice.Consumer) ([]consumerinvoice.Address, error) {
	noteContractCall(ctx, "ConsumerInvoice.Lookup")
	return n.p.Lookup(ctx, c)
}

func (n notedConsumerProvider) Send(ctx context.Context, sh consumerinvoice.Shipment) (consumerinvoice.Ref, error) {
	noteContractCall(ctx, "ConsumerInvoice.Send")
	return n.p.Send(ctx, sh)
}

func (n notedConsumerProvider) Status(ctx context.Context, ref consumerinvoice.Ref) ([]consumerinvoice.TransactionStatus, error) {
	noteContractCall(ctx, "ConsumerInvoice.Status")
	return n.p.Status(ctx, ref)
}

func (n notedConsumerProvider) Cancel(ctx context.Context, ref consumerinvoice.Ref, transactionID string) (consumerinvoice.CancelOutcome, error) {
	noteContractCall(ctx, "ConsumerInvoice.Cancel")
	return n.p.Cancel(ctx, ref, transactionID)
}

func (n notedConsumerProvider) Mandates(ctx context.Context, requestID string) (consumerinvoice.MandatePage, error) {
	noteContractCall(ctx, "ConsumerInvoice.Mandates")
	return n.p.Mandates(ctx, requestID)
}

func (n notedConsumerProvider) Covers(ctx context.Context, k string, amount consumerinvoice.Money) (bool, error) {
	noteContractCall(ctx, "ConsumerInvoice.Covers")
	return n.p.Covers(ctx, k, amount)
}

func (n notedConsumerProvider) Verify(ctx context.Context) error {
	noteContractCall(ctx, "ConsumerInvoice.Verify")
	return n.p.Verify(ctx)
}

// consumerProviderRequest is a PUT body that passed; certificate is nil to
// keep the stored one, "" to remove it.
type consumerProviderRequest struct {
	settings    consumerProviderSettings
	certificate *string
}

func validAgreement(v string) bool {
	return v == agreementNone || v == agreementPending || v == agreementActive
}

// allDigits is whether s is n digits.
func allDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseConsumerProviderRequest checks a PUT body field by field.
func parseConsumerProviderRequest(body gen.InvoicesConsumerProviderRequest) (consumerProviderRequest, map[string][]string) {
	var errs map[string][]string
	in := consumerProviderRequest{settings: consumerProviderSettings{
		EfakturaAgreement: strings.TrimSpace(body.EfakturaAgreement), AvtalegiroAgreement: strings.TrimSpace(body.AvtalegiroAgreement),
		BrandName: strings.TrimSpace(body.BrandName),
	}}
	if strings.TrimSpace(body.Provider) != providerMPS {
		errs = withFieldError(errs, "provider", "Must be mps.")
	}
	if !validAgreement(in.settings.EfakturaAgreement) {
		errs = withFieldError(errs, "efakturaAgreement", "Must be none, pending or active.")
	}
	if !validAgreement(in.settings.AvtalegiroAgreement) {
		errs = withFieldError(errs, "avtalegiroAgreement", "Must be none, pending or active.")
	}
	if in.settings.BrandName == "" || utf8.RuneCountInString(in.settings.BrandName) > maxBrandName {
		errs = withFieldError(errs, "brandName", fmt.Sprintf("Required, at most %d characters.", maxBrandName))
	}
	if body.AbbreviatedName != nil {
		in.settings.AbbreviatedName = strings.TrimSpace(*body.AbbreviatedName)
		if utf8.RuneCountInString(in.settings.AbbreviatedName) > maxAbbreviatedName {
			errs = withFieldError(errs, "abbreviatedName", fmt.Sprintf("At most %d characters.", maxAbbreviatedName))
		}
	}
	if body.CreditorAccount != nil {
		in.settings.CreditorAccount = strings.ReplaceAll(strings.TrimSpace(*body.CreditorAccount), " ", "")
		in.settings.CreditorAccount = strings.ReplaceAll(in.settings.CreditorAccount, ".", "")
	}
	if in.settings.AvtalegiroAgreement != agreementNone && !allDigits(in.settings.CreditorAccount, 11) {
		errs = withFieldError(errs, "creditorAccount", "Required for AvtaleGiro: 11 digits.")
	}
	if in.settings.AvtalegiroAgreement != agreementNone && in.settings.AbbreviatedName == "" {
		errs = withFieldError(errs, "abbreviatedName", "Required for AvtaleGiro.")
	}
	if body.MandateCertificate != nil {
		pemText := strings.TrimSpace(*body.MandateCertificate)
		switch {
		case pemText == "":
		case len(pemText) > maxMandateCertPEM:
			errs = withFieldError(errs, "mandateCertificate", "Too long.")
		default:
			if _, err := tls.X509KeyPair([]byte(pemText), []byte(pemText)); err != nil {
				errs = withFieldError(errs, "mandateCertificate", "Must be a PEM certificate and its private key.")
			}
		}
		in.certificate = &pemText
	}
	return in, errs
}

// consumerProviderResponse is the row as a client may see it: never the
// certificate.
func (s *server) consumerProviderResponse(ctx context.Context, q *store.Queries, row *store.InvoicesConsumerProviderCredential) (gen.InvoicesConsumerProviderResponse, error) {
	settingsRow, err := q.GetSettings(ctx)
	if err != nil {
		return gen.InvoicesConsumerProviderResponse{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return gen.InvoicesConsumerProviderResponse{}, err
	}
	since := s.deps.Clock().In(loc).AddDate(-1, 0, 0)
	months, err := q.EfakturaSentPerMonth(ctx, store.EfakturaSentPerMonthParams{Zone: loc.String(), Since: since})
	if err != nil {
		return gen.InvoicesConsumerProviderResponse{}, fmt.Errorf("invoices: count the eFaktura sent: %w", err)
	}
	out := gen.InvoicesConsumerProviderResponse{
		OperatorReady: s.efakturaSwitchedOn(), AvtalegiroEnabled: settingsRow.AvtalegiroEnabled,
		SentPerMonth: make([]gen.InvoicesMonthCount, 0, len(months)),
	}
	for _, m := range months {
		out.SentPerMonth = append(out.SentPerMonth, gen.InvoicesMonthCount{Month: m.Month, Count: m.Sent})
	}
	if row == nil {
		return out, nil
	}
	settings, err := consumerProviderSettingsOf(*row)
	if err != nil {
		return gen.InvoicesConsumerProviderResponse{}, err
	}
	out.HasCredentials, out.HasMandateCertificate = true, row.SecretCiphertext != ""
	out.Provider, out.RejectedAt = ptr(row.Provider), row.RejectedAt
	out.EfakturaAgreement, out.AvtalegiroAgreement = ptr(settings.EfakturaAgreement), ptr(settings.AvtalegiroAgreement)
	out.BrandName = ptr(settings.BrandName)
	if settings.CreditorAccount != "" {
		out.CreditorAccount = ptr(settings.CreditorAccount)
	}
	if settings.AbbreviatedName != "" {
		out.AbbreviatedName = ptr(settings.AbbreviatedName)
	}
	return out, nil
}

// GetInvoicesSettingsConsumerProvider Get the consumer provider settings
// (GET /api/v1/invoices/settings/consumer-provider)
func (s *server) GetInvoicesSettingsConsumerProvider(ctx context.Context, _ gen.GetInvoicesSettingsConsumerProviderRequestObject) (gen.GetInvoicesSettingsConsumerProviderResponseObject, error) {
	q := store.New(s.deps.Pool)
	var stored *store.InvoicesConsumerProviderCredential
	row, err := q.GetConsumerProviderCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("invoices: read the consumer provider settings: %w", err)
	default:
		stored = &row
	}
	resp, err := s.consumerProviderResponse(ctx, q, stored)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSettingsConsumerProvider200JSONResponse(resp), nil
}

// PutInvoicesSettingsConsumerProvider Store the consumer provider settings
// (PUT /api/v1/invoices/settings/consumer-provider)
//
// The access point's PUT's shape: a new certificate is sealed before the
// transaction; a kept one is opened and sealed again from the FOR UPDATE
// read inside it.
func (s *server) PutInvoicesSettingsConsumerProvider(ctx context.Context, req gen.PutInvoicesSettingsConsumerProviderRequestObject) (gen.PutInvoicesSettingsConsumerProviderResponseObject, error) {
	in, errs := parseConsumerProviderRequest(*req.Body)
	if len(errs) > 0 {
		return gen.PutInvoicesSettingsConsumerProvider400ApplicationProblemPlusJSONResponse(invalid(invalidConsumerProviderTitle, errs)), nil
	}
	sealed := ""
	if in.certificate != nil && *in.certificate != "" {
		var err error
		if sealed, err = s.deps.Secrets.SealString(consumerProviderPurpose, *in.certificate); err != nil {
			return nil, fmt.Errorf("invoices: seal the mandate certificate: %w", err)
		}
	}
	// A struct of strings always encodes.
	settingsJSON, _ := json.Marshal(in.settings)

	var unreadable bool
	var saved store.InvoicesConsumerProviderCredential
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockConsumerProviderCredentials(ctx)
		stored := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("invoices: lock the consumer provider settings: %w", err)
		}
		if in.certificate == nil && stored && current.SecretCiphertext != "" {
			pemText, err := s.deps.Secrets.OpenString(consumerProviderPurpose, current.SecretCiphertext)
			if err != nil {
				unreadable = true
				return errRefused
			}
			if sealed, err = s.deps.Secrets.SealString(consumerProviderPurpose, pemText); err != nil {
				return fmt.Errorf("invoices: seal the mandate certificate: %w", err)
			}
		}
		saved, err = txq.UpsertConsumerProviderCredentials(ctx, store.UpsertConsumerProviderCredentialsParams{
			Provider: providerMPS, SettingsJson: string(settingsJSON), SecretCiphertext: sealed, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: store the consumer provider settings: %w", err)
		}
		return nil
	})
	switch {
	case unreadable:
		s.flagConsumerProviderRejected(ctx, "the stored mandate certificate cannot be opened")
		c := conflict(codeEfakturaUnavailable, efakturaUnavailableTitle,
			"The stored mandate certificate cannot be read on this server. Send it again.")
		c.Status = ptr(int32(http.StatusServiceUnavailable))
		return gen.PutInvoicesSettingsConsumerProvider503ApplicationProblemPlusJSONResponse(c), nil
	case err != nil:
		return nil, err
	}
	resp, err := s.consumerProviderResponse(ctx, store.New(s.deps.Pool), &saved)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettingsConsumerProvider200JSONResponse(resp), nil
}

// DeleteInvoicesSettingsConsumerProvider Remove the consumer provider settings
// (DELETE /api/v1/invoices/settings/consumer-provider)
func (s *server) DeleteInvoicesSettingsConsumerProvider(ctx context.Context, _ gen.DeleteInvoicesSettingsConsumerProviderRequestObject) (gen.DeleteInvoicesSettingsConsumerProviderResponseObject, error) {
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := holidays.DayIn(s.deps.Clock(), loc)
	var refused bool
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockConsumerProviderCredentials(ctx); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("invoices: lock the consumer provider settings: %w", err)
		}
		n, err := txq.EfakturaInFlight(ctx, pgDate(today))
		if err != nil {
			return fmt.Errorf("invoices: count the eFaktura in flight: %w", err)
		}
		if n > 0 {
			refused = true
			return errRefused
		}
		if _, err := txq.DeleteConsumerProviderCredentials(ctx); err != nil {
			return fmt.Errorf("invoices: remove the consumer provider settings: %w", err)
		}
		return nil
	})
	if refused {
		return gen.DeleteInvoicesSettingsConsumerProvider409ApplicationProblemPlusJSONResponse(conflict(codeTransmissionsActive, transmissionsActiveTitle,
			"An eFaktura is queued, submitted or awaiting confirmation, or an AvtaleGiro claim is live. Wait until they are settled before removing the settings.")), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInvoicesSettingsConsumerProvider204Response{}, nil
}

// PostInvoicesSettingsConsumerProviderVerify Verify the consumer provider settings
// (POST /api/v1/invoices/settings/consumer-provider/verify)
func (s *server) PostInvoicesSettingsConsumerProviderVerify(ctx context.Context, _ gen.PostInvoicesSettingsConsumerProviderVerifyRequestObject) (gen.PostInvoicesSettingsConsumerProviderVerifyResponseObject, error) {
	p, _, err := s.consumerProvider(ctx)
	switch {
	case errors.Is(err, errNoConsumerProvider):
		return gen.PostInvoicesSettingsConsumerProviderVerify409ApplicationProblemPlusJSONResponse(conflict(codeEfakturaUnavailable, efakturaUnavailableTitle,
			"No eFaktura settings are stored, so there is nothing to verify.")), nil
	case errors.Is(err, errEfakturaOff), errors.Is(err, errSubmitterUnreadable):
		return gen.PostInvoicesSettingsConsumerProviderVerify503ApplicationProblemPlusJSONResponse(efakturaUnavailable()), nil
	case err != nil:
		return nil, err
	}
	q := store.New(s.deps.Pool)
	result := accessPointResultOK
	switch err := p.Verify(ctx); {
	case err == nil:
		if err := q.ClearConsumerProviderRejected(ctx); err != nil {
			return nil, fmt.Errorf("invoices: clear the consumer provider refusal: %w", err)
		}
	case errors.Is(err, consumerinvoice.ErrUnauthorized):
		result = accessPointResultRefused
		if err := q.MarkConsumerProviderRejected(ctx, s.deps.Clock()); err != nil {
			return nil, fmt.Errorf("invoices: flag the consumer provider as rejected: %w", err)
		}
	default:
		result = accessPointResultUnreached
		s.deps.Logger.WarnContext(ctx, "invoices: the consumer provider could not be verified", "error", redactReason(err.Error()))
	}
	return gen.PostInvoicesSettingsConsumerProviderVerify200JSONResponse(gen.InvoicesAccessPointVerifyResponse{Result: result}), nil
}

// PutInvoicesSettingsAvtalegiro Turn AvtaleGiro on or off
// (PUT /api/v1/invoices/settings/avtalegiro)
//
// On needs the KID agreement, an active AvtaleGiro agreement with its
// creditor account, and room for the customer KIDs (reading 12): the next
// number within the length less two, and no issued KID beginning with 9.
// Judged under the settings lock every issue reads, so no issue slips a
// number past the cap between the check and the switch.
func (s *server) PutInvoicesSettingsAvtalegiro(ctx context.Context, req gen.PutInvoicesSettingsAvtalegiroRequestObject) (gen.PutInvoicesSettingsAvtalegiroResponseObject, error) {
	enabled := req.Body.Enabled
	// AvtaleGiro rides on eFaktura: with the live switch off nothing would
	// carry a claim, and the PDF would invite mandates no one collects.
	if enabled && !s.efakturaSwitchedOn() {
		return gen.PutInvoicesSettingsAvtalegiro503ApplicationProblemPlusJSONResponse(efakturaUnavailable()), nil
	}
	var refusal *gen.InvoicesConflictProblem
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		settings, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		if enabled && !settings.AvtalegiroEnabled {
			if settings.KidLength == nil || settings.KidAlgorithm == nil {
				refusal = ptr(conflict(codeKidAgreementMissing, avtaleGiroRefusedTitle, "AvtaleGiro needs the KID agreement with the bank. Set it first."))
				return errRefused
			}
			row, err := txq.GetConsumerProviderCredentials(ctx)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("invoices: read the consumer provider settings: %w", err)
			}
			var cp consumerProviderSettings
			if err == nil {
				if cp, err = consumerProviderSettingsOf(row); err != nil {
					return err
				}
			}
			if cp.AvtalegiroAgreement != agreementActive || cp.CreditorAccount == "" {
				refusal = ptr(conflict(codeAvtaleGiroAgreementAbsent, avtaleGiroRefusedTitle,
					"AvtaleGiro needs an active AvtaleGiro agreement and its creditor account in the eFaktura settings."))
				return errRefused
			}
			next, _, err := nextNumber(ctx, txq, settings.SeriesStart)
			if err != nil {
				return err
			}
			if fits, _ := kid.FitsReserved(next, int(*settings.KidLength)); !fits {
				refusal = ptr(conflict(codeAvtaleGiroKidSpaceTaken, avtaleGiroRefusedTitle, fmt.Sprintf(
					"AvtaleGiro reserves the KID's first digit for the customer KIDs, and the next invoice number, %d, needs it. Agree a longer KID with the bank, or a second agreement.", next)))
				return errRefused
			}
			taken, err := txq.AnyIssuedKidInCustomerSpace(ctx)
			if err != nil {
				return fmt.Errorf("invoices: read the issued KIDs: %w", err)
			}
			if taken {
				refusal = ptr(conflict(codeAvtaleGiroKidSpaceTaken, avtaleGiroRefusedTitle,
					"An issued invoice's KID already begins with 9, the digit AvtaleGiro reserves for the customer KIDs. Agree a longer KID with the bank, or a second agreement."))
				return errRefused
			}
		}
		if err := txq.SetAvtaleGiroEnabled(ctx, enabled); err != nil {
			return fmt.Errorf("invoices: set AvtaleGiro: %w", err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PutInvoicesSettingsAvtalegiro409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	s.deps.Logger.InfoContext(ctx, "invoices: AvtaleGiro is switched", "enabled", enabled)
	return gen.PutInvoicesSettingsAvtalegiro200JSONResponse(gen.InvoicesAvtaleGiroResponse{Enabled: enabled}), nil
}

// efakturaDayWindow is the due-date window a send judges (I31): from today +
// 3 to today + 12 months, in the business time zone.
func efakturaDayWindow(today time.Time) (earliest, latest time.Time) {
	return today.AddDate(0, 0, 3), today.AddDate(1, 0, 0)
}
