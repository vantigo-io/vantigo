package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const taxCategoryVatCodesPath = "/api/v1/invoices/settings/tax-category-vat-codes"

type taxCategoryVatCodeJSON struct {
	TaxCategoryID int32 `json:"taxCategoryId"`
	VatCodeID     int32 `json:"vatCodeId"`
}

// The tax categories' VAT codes (orders design O7): a manager maps, remaps and
// removes, the list and contracts.SalesTax answer what is stored, and an
// unknown or inactive code is refused on vatCodeId.
func TestTaxCategoryVatCodes_MapRemapAndRemove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	m := manager(t, h)
	put := func(category int32, code int32) (int, problemJSON) {
		res := m.Do(http.MethodPut, fmt.Sprintf("%s/%d", taxCategoryVatCodesPath, category), map[string]any{"vatCodeId": code})
		var p problemJSON
		if res.Status != http.StatusOK && len(res.Body) > 0 {
			res.JSON(&p)
		}
		return res.Status, p
	}
	list := func() []taxCategoryVatCodeJSON {
		res := h.SignIn(t, "invoices:access").Do(http.MethodGet, taxCategoryVatCodesPath, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", taxCategoryVatCodesPath, res.Status, res.Body)
		}
		var out []taxCategoryVatCodeJSON
		res.JSON(&out)
		return out
	}

	if got := list(); len(got) != 0 {
		t.Fatalf("before any mapping = %+v, want none", got)
	}
	if status, _ := put(41, vat25); status != http.StatusOK {
		t.Fatalf("map 41 → 25 %% = %d, want 200", status)
	}
	if status, _ := put(42, vatZero); status != http.StatusOK {
		t.Fatalf("map 42 → 0 %% = %d, want 200", status)
	}
	if status, _ := put(41, vat15); status != http.StatusOK {
		t.Fatalf("remap 41 → 15 %% = %d, want 200", status)
	}
	if got := list(); len(got) != 2 || got[0] != (taxCategoryVatCodeJSON{41, vat15}) || got[1] != (taxCategoryVatCodeJSON{42, vatZero}) {
		t.Errorf("the mappings = %+v, want 41 → %d and 42 → %d", got, vat15, vatZero)
	}
	at := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	answered, err := salesTaxOf(t, h).ForTaxCategories(ctx, []int32{41, 42}, at)
	if err != nil || answered[41].ID != vat15 || answered[42].ID != vatZero {
		t.Errorf("ForTaxCategories = %+v, %v, want 41 → %d and 42 → %d", answered, err, vat15, vatZero)
	}

	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", taxCategoryVatCodesPath, 42), nil); res.Status != http.StatusNoContent {
		t.Errorf("remove 42 = %d, want 204", res.Status)
	}
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", taxCategoryVatCodesPath, 42), nil); res.Status != http.StatusNoContent {
		t.Errorf("remove 42 again = %d, want 204", res.Status)
	}
	if got := list(); len(got) != 1 || got[0].TaxCategoryID != 41 {
		t.Errorf("after the removal = %+v, want 41 only", got)
	}

	if status, p := put(43, vatUnknownID); status != http.StatusBadRequest || len(p.Errors["vatCodeId"]) == 0 {
		t.Errorf("an unknown code = %d %+v, want 400 on vatCodeId", status, p)
	}
	if _, err := h.Pool().Exec(ctx, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vatOutside); err != nil {
		t.Fatalf("deactivate a code: %v", err)
	}
	if status, p := put(43, vatOutside); status != http.StatusBadRequest || len(p.Errors["vatCodeId"]) == 0 {
		t.Errorf("an inactive code = %d %+v, want 400 on vatCodeId", status, p)
	}
	if status, p := put(0, vat25); status != http.StatusBadRequest || len(p.Errors["taxCategoryId"]) == 0 {
		t.Errorf("tax category 0 = %d %+v, want 400 on taxCategoryId", status, p)
	}
	if got := list(); len(got) != 1 {
		t.Errorf("after the refusals = %+v, want 41 only", got)
	}
}

// Reading takes invoices:access; changing takes invoices:manage too.
func TestTaxCategoryVatCodes_ChangingNeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	reader := h.SignIn(t, "invoices:access")
	if res := reader.Do(http.MethodPut, taxCategoryVatCodesPath+"/41", map[string]any{"vatCodeId": vat25}); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:manage = %d, want 403", res.Status)
	}
	if res := reader.Do(http.MethodDelete, taxCategoryVatCodesPath+"/41", nil); res.Status != http.StatusForbidden {
		t.Errorf("DELETE without invoices:manage = %d, want 403", res.Status)
	}
	if res := h.SignIn(t).Do(http.MethodGet, taxCategoryVatCodesPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET without invoices:access = %d, want 403", res.Status)
	}
}
