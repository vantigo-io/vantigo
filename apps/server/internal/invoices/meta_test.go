package invoices_test

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// metaJSON is GET /meta as a client reads it.
type metaJSON struct {
	Currency                string   `json:"currency"`
	DefaultPaymentTermsDays int32    `json:"defaultPaymentTermsDays"`
	SellerComplete          bool     `json:"sellerComplete"`
	MissingSellerFields     []string `json:"missingSellerFields"`
	AnythingIssued          bool     `json:"anythingIssued"`
	SeriesStart             int64    `json:"seriesStart"`
	StorageAvailable        bool     `json:"storageAvailable"`
	Today                   string   `json:"today"`
	VatCodes                []struct {
		ID              int32   `json:"id"`
		Code            string  `json:"code"`
		Name            string  `json:"name"`
		SafTCode        string  `json:"safTCode"`
		EhfCategory     string  `json:"ehfCategory"`
		ExemptionReason *string `json:"exemptionReason"`
		RatePercent     float64 `json:"ratePercent"`
	} `json:"vatCodes"`
	Capabilities struct {
		CanCreate bool `json:"canCreate"`
		CanIssue  bool `json:"canIssue"`
		CanManage bool `json:"canManage"`
	} `json:"capabilities"`
}

const metaPath = "/api/v1/invoices/meta"

func getMeta(t *testing.T, h *harness, permissions ...string) metaJSON {
	t.Helper()
	res := h.SignIn(t, permissions...).Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	var meta metaJSON
	res.JSON(&meta)
	return meta
}

// A fresh installation: NOK, fourteen days, a seller with nothing filled in,
// nothing issued, the series starting at 1, and the nine seeded codes at their
// 2026 rates — 6 as E and 7 as O (D3).
func TestMeta_AnswersAFreshInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	meta := getMeta(t, h, "invoices:access")

	if meta.Currency != "NOK" || meta.DefaultPaymentTermsDays != 14 {
		t.Errorf("currency, terms = %q, %d, want NOK, 14", meta.Currency, meta.DefaultPaymentTermsDays)
	}
	wantMissing := []string{"legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"}
	if meta.SellerComplete || !slices.Equal(meta.MissingSellerFields, wantMissing) {
		t.Errorf("seller = complete %v missing %v, want incomplete missing %v", meta.SellerComplete, meta.MissingSellerFields, wantMissing)
	}
	if meta.AnythingIssued || meta.SeriesStart != 1 || !meta.StorageAvailable {
		t.Errorf("issued %v, start %d, storage %v; want false, 1, true", meta.AnythingIssued, meta.SeriesStart, meta.StorageAvailable)
	}
	if meta.Today != "2026-09-12" {
		t.Errorf("today = %q, want the harness's 2026-09-12 in Oslo", meta.Today)
	}
	type code struct {
		code, category, safT string
		rate                 float64
	}
	var got []code
	for _, c := range meta.VatCodes {
		got = append(got, code{c.Code, c.EhfCategory, c.SafTCode, c.RatePercent})
	}
	want := []code{
		{"3", "S", "3", 25}, {"31", "S", "31", 15}, {"32", "S", "32", 11.11}, {"33", "S", "33", 12},
		{"5", "Z", "5", 0}, {"51", "AE", "51", 0}, {"52", "G", "52", 0}, {"6", "E", "6", 0}, {"7", "O", "7", 0},
	}
	if !slices.Equal(got, want) {
		t.Errorf("VAT codes = %+v, want %+v", got, want)
	}
	for _, c := range meta.VatCodes {
		if (c.EhfCategory == "S") != (c.ExemptionReason == nil) {
			t.Errorf("code %s: exemption reason %v, want one exactly when the category is not S", c.Code, c.ExemptionReason)
		}
	}
	if meta.Capabilities.CanCreate || meta.Capabilities.CanIssue || meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v for invoices:access alone, want none", meta.Capabilities)
	}
}

// Each capability is its permission, beside invoices:access.
func TestMeta_CapabilitiesAreThePermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	meta := getMeta(t, h, "invoices:access", "invoices:create", "invoices:issue", "invoices:manage")
	if !meta.Capabilities.CanCreate || !meta.Capabilities.CanIssue || !meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v, want all three", meta.Capabilities)
	}
	if meta := getMeta(t, h, "invoices:access", "invoices:issue"); meta.Capabilities.CanCreate || !meta.Capabilities.CanIssue || meta.Capabilities.CanManage {
		t.Errorf("capabilities = %+v for invoices:issue, want only canIssue", meta.Capabilities)
	}
	if res := h.SignIn(t, "invoices:create").Do(http.MethodGet, metaPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /meta without invoices:access = %d, want 403", res.Status)
	}
}

// "Today" is the calendar day in Oslo, never UTC's: at 22:30 UTC on 12
// September it is already 00:30 on the 13th there (D1).
func TestMeta_TodayIsTheBusinessDayInOslo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(10*time.Hour + 30*time.Minute)

	if meta := getMeta(t, h, "invoices:access"); meta.Today != "2026-09-13" {
		t.Errorf("today at %s = %q, want 2026-09-13", h.Now().Format(time.RFC3339), meta.Today)
	}
}

// Without an object store nothing can be issued, and meta says so; "anything
// issued" is the counter row, never a count of documents (D2).
func TestMeta_StorageAndTheCounter(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	if meta := getMeta(t, h, "invoices:access"); meta.StorageAvailable {
		t.Error("storageAvailable = true with no object store configured")
	}

	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 2)`)
	if meta := getMeta(t, h, "invoices:access"); !meta.AnythingIssued {
		t.Error("anythingIssued = false with the counter row present")
	}
}
