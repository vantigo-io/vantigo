package vipps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/qrsvg"
)

// Vipps' limits on a create (R4 §4.4).
const (
	minAmount      = 100        // 1.00 NOK
	maxAmount      = 65_000_000 // 650 000.00 NOK
	minDescription = 3
	maxDescription = 100
	// maxURLChars is Vipps' limit on returnUrl (R4 §4.3), held for the legal
	// links too.
	maxURLChars = 2500
)

// createBody is ePayment's CreatePaymentRequest as the port sends it.
type createBody struct {
	Amount        amount `json:"amount"`
	PaymentMethod struct {
		Type string `json:"type"`
	} `json:"paymentMethod"`
	Reference           string `json:"reference"`
	PaymentDescription  string `json:"paymentDescription"`
	UserFlow            string `json:"userFlow"`
	ReturnURL           string `json:"returnUrl,omitempty"`
	CustomerInteraction string `json:"customerInteraction"`
	QRFormat            *struct {
		Format string `json:"format"`
	} `json:"qrFormat,omitempty"`
	MerchantLegalLinks *legalLinks `json:"merchantLegalLinks,omitempty"`
}

type legalLinks struct {
	TermsOfSaleURL   string `json:"termsOfSaleUrl,omitempty"`
	PrivacyPolicyURL string `json:"privacyPolicyUrl,omitempty"`
}

// checkCreate refuses, before any request, what Vipps documents it would
// refuse.
func checkCreate(r payments.CreateRequest) error {
	if err := checkReference(r.Reference); err != nil {
		return err
	}
	if err := checkKey(r.IdempotencyKey); err != nil {
		return err
	}
	if r.Amount.Currency != "NOK" || r.Amount.Minor < minAmount || r.Amount.Minor > maxAmount {
		return fmt.Errorf("%w: a payment is 1.00–650 000.00 NOK", payments.ErrInvalidRequest)
	}
	if n := utf8.RuneCountInString(r.Description); n < minDescription || n > maxDescription {
		return fmt.Errorf("%w: a description is 3–100 characters", payments.ErrInvalidRequest)
	}
	switch r.Flow {
	case payments.FlowRedirect:
		if r.ReturnURL == "" {
			return fmt.Errorf("%w: a redirect needs a return URL", payments.ErrInvalidRequest)
		}
	case payments.FlowQR:
	default:
		return fmt.Errorf("%w: an unknown flow %q", payments.ErrInvalidRequest, r.Flow)
	}
	for _, link := range []string{r.ReturnURL, r.TermsURL, r.PrivacyURL} {
		if link != "" && !httpsURL(link) {
			return fmt.Errorf("%w: a return URL and the legal links are https URLs of at most %d characters", payments.ErrInvalidRequest, maxURLChars)
		}
	}
	return nil
}

// httpsURL reports whether raw is an absolute https URL Vipps accepts.
func httpsURL(raw string) bool {
	if len(raw) > maxURLChars {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// Create is POST /epayment/v1/payments under the caller's key: a WALLET
// payment, WEB_REDIRECT back to ReturnURL, or a QR. For a QR Vipps is asked
// for the code's target URL (TEXT/TARGETURL), and the image is rendered here
// by qrsvg — never fetched from a URL an answer names.
//
// A create refused because the reference or the key is already in use (4150,
// 4020) may be this very request, made by an earlier try whose answer was
// lost: the payment is then read, and answered as created when it is the
// one asked for (see existing); otherwise the outcome is unknown.
func (c *Client) Create(ctx context.Context, r payments.CreateRequest) (payments.Created, error) {
	if err := checkCreate(r); err != nil {
		return payments.Created{}, err
	}
	body := createBody{
		Amount: amountOf(r.Amount), Reference: r.Reference, PaymentDescription: r.Description,
		CustomerInteraction: "CUSTOMER_NOT_PRESENT",
	}
	body.PaymentMethod.Type = "WALLET"
	if r.CustomerPresent {
		body.CustomerInteraction = "CUSTOMER_PRESENT"
	}
	if r.Flow == payments.FlowQR {
		body.UserFlow = "QR"
		body.QRFormat = &struct {
			Format string `json:"format"`
		}{Format: "TEXT/TARGETURL"}
	} else {
		body.UserFlow, body.ReturnURL = "WEB_REDIRECT", r.ReturnURL
	}
	if r.TermsURL != "" || r.PrivacyURL != "" {
		body.MerchantLegalLinks = &legalLinks{TermsOfSaleURL: r.TermsURL, PrivacyPolicyURL: r.PrivacyURL}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return payments.Created{}, fmt.Errorf("vipps: encode the payment: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "create", http.MethodPost, "/epayment/v1/payments", raw, r.IdempotencyKey)
	if err != nil {
		return payments.Created{}, err
	}
	if err := c.classify("create", a); err != nil {
		if conflict, ok := errors.AsType[*payments.ErrConflict](err); ok && (conflict.Code == codeReferenceExists || conflict.Code == codeKeyExists) {
			return c.existing(ctx, r)
		}
		return payments.Created{}, err
	}
	var created struct {
		Reference   string `json:"reference"`
		RedirectURL string `json:"redirectUrl"`
	}
	if err := json.Unmarshal(a.body, &created); err != nil || created.Reference != r.Reference || created.RedirectURL == "" {
		return payments.Created{}, errors.New("vipps: Vipps accepted the payment but answered no reference and redirect URL")
	}
	return createdOf(r, created.RedirectURL)
}

// existing answers the payment already held under r's reference as created
// when it is the one r asks for — the same amount and currency, still CREATED
// or AUTHORIZED; anything else is an unknown outcome. A QR is never read
// back: Vipps does not promise that a read's redirectUrl is the QR's target
// URL, so the caller polls the reference instead (and may abandon an on-site
// QR and make a new one).
func (c *Client) existing(ctx context.Context, r payments.CreateRequest) (payments.Created, error) {
	if r.Flow == payments.FlowQR {
		return payments.Created{}, errors.New("vipps: the QR's reference is in use; its code is not read back")
	}
	a, err := c.call(ctx, "get", http.MethodGet, paymentPath(r.Reference), nil, "")
	if err != nil {
		return payments.Created{}, err
	}
	if a.status != http.StatusOK {
		return payments.Created{}, fmt.Errorf("vipps: the reference is in use, and reading it answered %d", a.status)
	}
	var held paymentBody
	if err := json.Unmarshal(a.body, &held); err != nil || held.Reference != r.Reference || held.Amount == nil {
		return payments.Created{}, errors.New("vipps: the reference is in use, and its payment does not decode")
	}
	if *held.Amount != amountOf(r.Amount) {
		return payments.Created{}, errors.New("vipps: the reference is in use by a payment of another amount")
	}
	if s := payments.State(held.State); s != payments.StateCreated && s != payments.StateAuthorized {
		return payments.Created{}, fmt.Errorf("vipps: the reference is in use by a payment that is %s", held.State)
	}
	if held.RedirectURL == "" {
		return payments.Created{}, errors.New("vipps: the reference is in use, and its payment answers no redirect URL")
	}
	return createdOf(r, held.RedirectURL)
}

// createdOf is the answer to r: the redirect URL, or the target URL Vipps
// answered to its create and the QR rendered from it. Either is where a payer
// is sent, so anything but an absolute https URL is no answer — an unknown
// outcome the caller polls, never an address handed on.
func createdOf(r payments.CreateRequest, redirectURL string) (payments.Created, error) {
	if !httpsURL(redirectURL) {
		return payments.Created{}, errors.New("vipps: Vipps answered an address that is not an absolute https URL")
	}
	if r.Flow != payments.FlowQR {
		return payments.Created{Reference: r.Reference, RedirectURL: redirectURL}, nil
	}
	svg, err := qrsvg.Render(redirectURL)
	if err != nil {
		return payments.Created{}, fmt.Errorf("vipps: render the QR: %w", err)
	}
	return payments.Created{Reference: r.Reference, QRTarget: redirectURL, QRSVG: svg}, nil
}
