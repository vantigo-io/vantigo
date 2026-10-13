package invoices_test

import (
	"maps"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const (
	paymentSettingsPath = "/api/v1/invoices/settings/payments"
	// publicBaseURL is the PUBLIC_BASE_URL a harness offering pay links sets.
	publicBaseURL = "https://pay.example.com"
	termsURL      = "https://seller.example.com/terms"
	privacyURL    = "https://seller.example.com/privacy"
)

// paymentSettingsJSON is the payment settings as a client reads them.
type paymentSettingsJSON struct {
	ReferencePrefix           string     `json:"referencePrefix"`
	PayLinksEnabled           bool       `json:"payLinksEnabled"`
	TermsURL                  *string    `json:"termsUrl"`
	PrivacyURL                *string    `json:"privacyUrl"`
	OnSitePaymentsEnabled     bool       `json:"onSitePaymentsEnabled"`
	KontantsalgAcknowledgedBy *uuid.UUID `json:"kontantsalgAcknowledgedBy"`
	KontantsalgAcknowledgedAt *time.Time `json:"kontantsalgAcknowledgedAt"`
	Revision                  int32      `json:"revision"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
	UpdatedBy                 *uuid.UUID `json:"updatedBy"`
}

// paymentSettingsBody is a PUT body at revision with everything off and no
// URLs, changed by with.
func paymentSettingsBody(revision int32, with map[string]any) map[string]any {
	body := map[string]any{
		"payLinksEnabled": false, "termsUrl": nil, "privacyUrl": nil,
		"onSitePaymentsEnabled": false, "revision": revision,
	}
	maps.Copy(body, with)
	return body
}

// everythingOn is what turns pay links and on-site payment on.
func everythingOn() map[string]any {
	return map[string]any{
		"payLinksEnabled": true, "termsUrl": termsURL, "privacyUrl": privacyURL,
		"onSitePaymentsEnabled": true, "acknowledgeKontantsalg": true,
	}
}

func getPaymentSettings(t *testing.T, h *harness) paymentSettingsJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, paymentSettingsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", paymentSettingsPath, res.Status, res.Body)
	}
	var out paymentSettingsJSON
	res.JSON(&out)
	return out
}

// putPaymentSettings changes the settings as a manager, failing unless 200.
func putPaymentSettings(t *testing.T, h *harness, body map[string]any) paymentSettingsJSON {
	t.Helper()
	return putPaymentSettingsAs(t, h.SignIn(t, "invoices:access", "invoices:manage"), body)
}

func putPaymentSettingsAs(t *testing.T, c *modtest.Client, body map[string]any) paymentSettingsJSON {
	t.Helper()
	res := c.Do(http.MethodPut, paymentSettingsPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s = %d %s, want 200", paymentSettingsPath, res.Status, res.Body)
	}
	var out paymentSettingsJSON
	res.JSON(&out)
	return out
}

// enablePayments turns pay links and on-site payment on.
func enablePayments(t *testing.T, h *harness) paymentSettingsJSON {
	t.Helper()
	return putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, everythingOn()))
}

// paymentMetaJSON is meta's payment fields.
type paymentMetaJSON struct {
	PayLinksAvailable        bool `json:"payLinksAvailable"`
	OnSitePaymentsAvailable  bool `json:"onSitePaymentsAvailable"`
	VippsCredentialsRejected bool `json:"vippsCredentialsRejected"`
	Capabilities             struct {
		CanTakeOnSitePayment bool `json:"canTakeOnSitePayment"`
	} `json:"capabilities"`
}

func paymentMeta(t *testing.T, h *harness, permissions ...string) paymentMetaJSON {
	t.Helper()
	res := h.SignIn(t, permissions...).Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	var out paymentMetaJSON
	res.JSON(&out)
	return out
}

// The payment settings' rules (D14): each 400 on its field, the stale
// revision, public_url_missing and payments_unavailable when a feature is
// turned on without what it needs, and the kontantsalg acknowledgement
// recorded with the caller and the time when on-site payment is turned on,
// kept while it stays on, and cleared when it is turned off.
func TestPaymentSettings_Rules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
	manager, managerID := h.SignInUser(t, "invoices:access", "invoices:manage")

	fresh := getPaymentSettings(t, h)
	if !regexp.MustCompile(`^[0-9a-z]{6}$`).MatchString(fresh.ReferencePrefix) || fresh.Revision != 1 ||
		fresh.PayLinksEnabled || fresh.OnSitePaymentsEnabled || fresh.TermsURL != nil || fresh.KontantsalgAcknowledgedAt != nil {
		t.Fatalf("a fresh installation's settings = %+v", fresh)
	}
	if res := h.SignIn(t, "invoices:access", "invoices:issue").Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(1, nil)); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:manage = %d, want 403", res.Status)
	}

	without := func(field string) map[string]any {
		body := paymentSettingsBody(1, nil)
		delete(body, field)
		return body
	}
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"an http terms URL":                   {paymentSettingsBody(1, map[string]any{"termsUrl": "http://seller.example.com/terms"}), "termsUrl"},
		"a relative privacy URL":              {paymentSettingsBody(1, map[string]any{"privacyUrl": "/privacy"}), "privacyUrl"},
		"a URL too long":                      {paymentSettingsBody(1, map[string]any{"termsUrl": "https://seller.example.com/" + strings.Repeat("t", 480)}), "termsUrl"},
		"pay links without terms":             {paymentSettingsBody(1, map[string]any{"payLinksEnabled": true, "privacyUrl": privacyURL}), "termsUrl"},
		"pay links without privacy":           {paymentSettingsBody(1, map[string]any{"payLinksEnabled": true, "termsUrl": termsURL}), "privacyUrl"},
		"on-site without the acknowledgement": {paymentSettingsBody(1, map[string]any{"onSitePaymentsEnabled": true}), "acknowledgeKontantsalg"},
		"on-site acknowledged false": {paymentSettingsBody(1, map[string]any{"onSitePaymentsEnabled": true, "acknowledgeKontantsalg": false}),
			"acknowledgeKontantsalg"},
		"no revision":        {without("revision"), "revision"},
		"no termsUrl":        {without("termsUrl"), "termsUrl"},
		"no payLinksEnabled": {without("payLinksEnabled"), "payLinksEnabled"},
		"a null on-site":     {paymentSettingsBody(1, map[string]any{"onSitePaymentsEnabled": nil}), "onSitePaymentsEnabled"},
	} {
		res := manager.Do(http.MethodPut, paymentSettingsPath, c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s: PUT = %d %s, want 400 on %s", name, res.Status, res.Body, c.field)
		}
	}

	// Without credentials neither feature can be turned on; the URLs alone can be set.
	for name, with := range map[string]map[string]any{
		"pay links": {"payLinksEnabled": true, "termsUrl": termsURL, "privacyUrl": privacyURL},
		"on-site":   {"onSitePaymentsEnabled": true, "acknowledgeKontantsalg": true},
	} {
		res := manager.Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(1, with))
		if res.Status != http.StatusConflict || problemOf(t, res).Code != "payments_unavailable" {
			t.Errorf("%s on without credentials = %d %s, want 409 payments_unavailable", name, res.Status, res.Body)
		}
	}
	urls := putPaymentSettings(t, h, paymentSettingsBody(1, map[string]any{"termsUrl": termsURL, "privacyUrl": privacyURL}))
	if urls.Revision != 2 || *urls.TermsURL != termsURL || *urls.PrivacyURL != privacyURL || *urls.UpdatedBy == uuid.Nil {
		t.Errorf("after setting the URLs = %+v", urls)
	}
	if res := manager.Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(1, nil)); res.Status != http.StatusConflict ||
		problemOf(t, res).Code != "" || !strings.Contains(problemOf(t, res).Detail, "revision 2") {
		t.Errorf("a stale revision = %d %s, want 409 naming both revisions", res.Status, res.Body)
	}

	putVipps(t, h, vippstestKeys())
	on := putPaymentSettingsAs(t, manager, paymentSettingsBody(2, everythingOn()))
	if !on.PayLinksEnabled || !on.OnSitePaymentsEnabled || on.KontantsalgAcknowledgedBy == nil || *on.KontantsalgAcknowledgedBy != managerID ||
		on.KontantsalgAcknowledgedAt == nil || !on.KontantsalgAcknowledgedAt.Equal(h.Now()) {
		t.Fatalf("turned on = %+v, want the acknowledgement by %s at %s", on, managerID, h.Now())
	}
	// Staying on needs no new acknowledgement and keeps the first.
	other := h.SignIn(t, "invoices:access", "invoices:manage")
	kept := putPaymentSettingsAs(t, other, paymentSettingsBody(on.Revision, map[string]any{
		"payLinksEnabled": true, "termsUrl": termsURL, "privacyUrl": privacyURL, "onSitePaymentsEnabled": true,
	}))
	if *kept.KontantsalgAcknowledgedBy != managerID || !kept.KontantsalgAcknowledgedAt.Equal(*on.KontantsalgAcknowledgedAt) {
		t.Errorf("staying on = %+v, want the first acknowledgement kept", kept)
	}
	off := putPaymentSettings(t, h, paymentSettingsBody(kept.Revision, map[string]any{"termsUrl": termsURL, "privacyUrl": privacyURL}))
	if off.OnSitePaymentsEnabled || off.KontantsalgAcknowledgedBy != nil || off.KontantsalgAcknowledgedAt != nil {
		t.Errorf("turned off = %+v, want the acknowledgement cleared", off)
	}
	if res := manager.Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(off.Revision, map[string]any{"onSitePaymentsEnabled": true})); res.Status != http.StatusBadRequest {
		t.Errorf("on again without an acknowledgement = %d %s, want 400", res.Status, res.Body)
	}
	// A stale revision and a missing acknowledgement: the 400 comes first.
	res := manager.Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(1, map[string]any{"onSitePaymentsEnabled": true}))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["acknowledgeKontantsalg"]) == 0 {
		t.Errorf("a stale revision without an acknowledgement = %d %s, want 400 on acknowledgeKontantsalg", res.Status, res.Body)
	}
	// Refused credentials do not stop a setting from being turned on: meta
	// says it is unavailable until the keys are good again.
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = now()`)
	putPaymentSettings(t, h, paymentSettingsBody(off.Revision, map[string]any{
		"termsUrl": termsURL, "privacyUrl": privacyURL, "onSitePaymentsEnabled": true, "acknowledgeKontantsalg": true,
	}))

	// Nor does the switch: an installation with Vipps off may be set up ahead.
	switchedOff := newHarness(t, modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL), modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"))
	putVipps(t, switchedOff, vippstestKeys())
	if got := putPaymentSettings(t, switchedOff, paymentSettingsBody(1, everythingOn())); !got.PayLinksEnabled || !got.OnSitePaymentsEnabled {
		t.Errorf("turned on with the switch off = %+v, want both on", got)
	}

	// Pay links need PUBLIC_BASE_URL; on-site payment does not.
	noPublic := newHarness(t)
	putVipps(t, noPublic, vippstestKeys())
	res = vippsManager(t, noPublic).Do(http.MethodPut, paymentSettingsPath, paymentSettingsBody(1, everythingOn()))
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "public_url_missing" {
		t.Errorf("pay links on without PUBLIC_BASE_URL = %d %s, want 409 public_url_missing", res.Status, res.Body)
	}
	putPaymentSettings(t, noPublic, paymentSettingsBody(1, map[string]any{"onSitePaymentsEnabled": true, "acknowledgeKontantsalg": true}))
}

// Meta's payment availability (D1): each field true only when every
// condition holds, each false with any one missing; canTakeOnSitePayment is
// invoices:issue and on-site availability, nothing else.
func TestMeta_PaymentFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got != (paymentMetaJSON{}) {
		t.Errorf("meta before anything = %+v, want nothing available", got)
	}
	putVipps(t, h, vippstestKeys())
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got.PayLinksAvailable || got.OnSitePaymentsAvailable {
		t.Errorf("meta with the settings off = %+v, want nothing available", got)
	}
	settings := enablePayments(t, h)
	all := paymentMetaJSON{PayLinksAvailable: true, OnSitePaymentsAvailable: true}
	all.Capabilities.CanTakeOnSitePayment = true
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got != all {
		t.Errorf("meta with everything = %+v, want %+v", got, all)
	}
	if got := paymentMeta(t, h, "invoices:access", "invoices:manage", "invoices:payments", "invoices:create"); got.Capabilities.CanTakeOnSitePayment {
		t.Error("canTakeOnSitePayment without invoices:issue = true, want false")
	}

	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = now()`)
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got.PayLinksAvailable || got.OnSitePaymentsAvailable ||
		got.Capabilities.CanTakeOnSitePayment || !got.VippsCredentialsRejected {
		t.Errorf("meta with rejected credentials = %+v, want nothing available and the refusal reported", got)
	}
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = NULL`)

	settings = putPaymentSettings(t, h, paymentSettingsBody(settings.Revision, map[string]any{
		"termsUrl": termsURL, "privacyUrl": privacyURL, "onSitePaymentsEnabled": true,
	}))
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got.PayLinksAvailable || !got.OnSitePaymentsAvailable {
		t.Errorf("meta with pay links off = %+v, want on-site only", got)
	}
	putPaymentSettings(t, h, paymentSettingsBody(settings.Revision, map[string]any{
		"payLinksEnabled": true, "termsUrl": termsURL, "privacyUrl": privacyURL,
	}))
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); !got.PayLinksAvailable || got.OnSitePaymentsAvailable || got.Capabilities.CanTakeOnSitePayment {
		t.Errorf("meta with on-site off = %+v, want pay links only", got)
	}

	if res := vippsManager(t, h).Do(http.MethodDelete, vippsSettingsPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if got := paymentMeta(t, h, "invoices:access", "invoices:issue"); got != (paymentMetaJSON{}) {
		t.Errorf("meta without credentials = %+v, want nothing available", got)
	}

	// Without PUBLIC_BASE_URL there are no pay links, whatever the settings say.
	noPublic := newHarness(t)
	putVipps(t, noPublic, vippstestKeys())
	noPublic.Exec(t, `UPDATE invoices.payment_settings SET pay_links_enabled = true, terms_url = $1, privacy_url = $2,
		on_site_payments_enabled = true, kontantsalg_acknowledged_at = now(), kontantsalg_acknowledged_by_user_id = gen_random_uuid()`,
		termsURL, privacyURL)
	if got := paymentMeta(t, noPublic, "invoices:access", "invoices:issue"); got.PayLinksAvailable || !got.OnSitePaymentsAvailable {
		t.Errorf("meta without PUBLIC_BASE_URL = %+v, want on-site only", got)
	}
}
