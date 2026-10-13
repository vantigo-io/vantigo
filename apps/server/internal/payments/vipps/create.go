package vipps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// Vipps' limits on a create (R4 §4.4).
const (
	minAmount      = 100        // 1.00 NOK
	maxAmount      = 65_000_000 // 650 000.00 NOK
	minDescription = 3
	maxDescription = 100
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
	return nil
}

// Create is POST /epayment/v1/payments under the caller's key: a WALLET
// payment, WEB_REDIRECT back to ReturnURL or a QR as an SVG. For a QR, Vipps'
// redirectUrl links to the image, which is fetched at once — over https,
// without the keys — and answered as QRSVG.
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
		}{Format: "IMAGE/SVG+XML"}
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
		return payments.Created{}, err
	}
	var created struct {
		Reference   string `json:"reference"`
		RedirectURL string `json:"redirectUrl"`
	}
	if err := json.Unmarshal(a.body, &created); err != nil || created.Reference != r.Reference || created.RedirectURL == "" {
		return payments.Created{}, errors.New("vipps: Vipps accepted the payment but answered no reference and redirect URL")
	}
	if r.Flow != payments.FlowQR {
		return payments.Created{Reference: created.Reference, RedirectURL: created.RedirectURL}, nil
	}
	svg, err := c.fetchQR(ctx, created.RedirectURL)
	if err != nil {
		return payments.Created{}, err
	}
	return payments.Created{Reference: created.Reference, QRSVG: svg}, nil
}

// fetchQR downloads the QR image Vipps links to. Its errors never carry the
// URL, which grants the payment to whoever holds it.
func (c *Client) fetchQR(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("vipps: the QR Vipps linked to is not an https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", errors.New("vipps: the QR's URL does not parse")
	}
	req.Header.Set("Accept", "image/svg+xml")
	a, err := c.do(req, "qr")
	if err != nil {
		return "", err
	}
	if a.status != http.StatusOK || !strings.Contains(string(a.body), "<svg") {
		return "", fmt.Errorf("vipps: the QR image answered %d and no SVG", a.status)
	}
	return string(a.body), nil
}
