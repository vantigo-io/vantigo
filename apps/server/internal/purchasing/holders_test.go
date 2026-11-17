package purchasing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// inTx runs fn on a transaction of the harness's pool, rolled back after
// unless commit is set.
func inTx(t *testing.T, h *harness, commit bool, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	_ = tx.Rollback(ctx)
}

// The variant holder: any item or line holds a delete; an open goods line a
// change to a service; a tracking change is never held.
func TestHolders_Variant(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	holder := purchasing.Module().VariantReferences(h.Deps())
	held := func(v int32, purpose contracts.VariantHoldPurpose) *contracts.VariantHold {
		var out *contracts.VariantHold
		inTx(t, h, false, func(tx pgx.Tx) {
			var err error
			if out, err = holder.VariantHeld(context.Background(), tx, v, purpose); err != nil {
				t.Fatal(err)
			}
		})
		return out
	}
	if held(varBolt, contracts.VariantHoldDelete) != nil {
		t.Fatal("a variant nothing names is held")
	}
	mustStatus(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varBolt}), http.StatusCreated, "item")
	if hd := held(varBolt, contracts.VariantHoldDelete); hd == nil || hd.Kind != contracts.VariantHoldKindOpenDocuments || hd.Documents != 1 {
		t.Errorf("held by an item = %+v", hd)
	}
	if held(varBolt, contracts.VariantHoldTypeChange) != nil {
		t.Error("an item holds a type change")
	}
	o := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	if hd := held(varBolt, contracts.VariantHoldTypeChange); hd == nil || hd.Documents != 1 {
		t.Errorf("an open goods line = %+v", hd)
	}
	if held(varBolt, contracts.VariantHoldTrackingChange) != nil {
		t.Error("a tracking change is held")
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "close"), version(o)), http.StatusOK, "close")
	if held(varBolt, contracts.VariantHoldTypeChange) != nil {
		t.Error("a closed line holds a type change")
	}
	if hd := held(varBolt, contracts.VariantHoldDelete); hd == nil || hd.Documents != 2 {
		t.Errorf("a closed order still holds a delete: %+v", hd)
	}
	if n := eventsNamed(t, h, "purchasing.order.closed", o.Id); n != 1 {
		t.Errorf("closed events = %d", n)
	}
}

// The supplier holder answers documents for a delete and open documents for
// an anonymisation; the personal data's export answers the snapshots and the
// erase blanks them, writes the PDFs to the ledger, and the deletions worker
// takes them out of the store.
func TestHolders_SupplierAndPersonalData(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	m := purchasing.Module()
	holder := m.SupplierReferences(h.Deps())
	data := m.SupplierPersonalData(h.Deps())
	held := func(purpose contracts.SupplierHoldPurpose) *contracts.SupplierHold {
		var out *contracts.SupplierHold
		inTx(t, h, false, func(tx pgx.Tx) {
			var err error
			if out, err = holder.SupplierHeld(context.Background(), tx, supPerson, purpose); err != nil {
				t.Fatal(err)
			}
		})
		return out
	}
	if held(contracts.SupplierHoldDelete) != nil || held(contracts.SupplierHoldAnonymise) != nil {
		t.Fatal("a supplier nothing names is held")
	}
	o := createOrder(t, c, supPerson, variantLine(varBolt, "1"))
	if hd := held(contracts.SupplierHoldAnonymise); hd == nil || hd.Kind != contracts.SupplierHoldKindOpenDocuments {
		t.Errorf("a draft holds the anonymisation: %+v", hd)
	}
	res := c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt, Recipient: ptrEmail("ola@snekker.example")})
	mustStatus(t, res, http.StatusOK, "send")
	o = sendResultOf(t, res).Order
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: o.UpdatedAt, NotifySupplier: new(bool)})
	mustStatus(t, res, http.StatusOK, "cancel")
	if held(contracts.SupplierHoldAnonymise) != nil {
		t.Error("a cancelled order holds the anonymisation")
	}
	if hd := held(contracts.SupplierHoldDelete); hd == nil || hd.Kind != contracts.SupplierHoldKindDocuments || hd.Documents != 1 {
		t.Errorf("the delete = %+v", hd)
	}

	exported, err := data.ExportSupplierData(context.Background(), supPerson)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(exported)
	if want := `"supplierName":"Ola Snekker"`; !strings.Contains(string(body), want) || !strings.Contains(string(body), "ola@snekker.example") {
		t.Errorf("export = %s", body)
	}
	if none, _ := data.ExportSupplierData(context.Background(), supNorsk); none != nil {
		t.Errorf("an export of a supplier with nothing = %v", none)
	}
	stored := h.objects.count()
	var erased []contracts.ErasedData
	inTx(t, h, true, func(tx pgx.Tx) {
		if erased, err = data.EraseSupplierData(context.Background(), tx, supPerson); err != nil {
			t.Fatal(err)
		}
	})
	counts := map[string]int64{}
	for _, e := range erased {
		counts[e.Kind] = e.Count
	}
	if counts["purchasing.orders"] != 1 || counts["purchasing.documents"] != 1 || counts["purchasing.mailings"] != 1 {
		t.Errorf("erased = %+v", erased)
	}
	inTx(t, h, true, func(tx pgx.Tx) {
		again, err := data.EraseSupplierData(context.Background(), tx, supPerson)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range again {
			if e.Count != 0 {
				t.Errorf("a second erase = %+v", again)
			}
		}
	})
	if h.objects.count() != stored {
		t.Error("the erase deleted from the store inside the transaction")
	}
	if n := workerNamed(t, h, "purchasing.deletions")(); n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	if h.objects.count() != stored-1 {
		t.Error("the PDF is still stored")
	}
	if got := getOrder(t, c, o.Id); got.SupplierName != "" || got.SupplierEmail != nil {
		t.Errorf("after the erase = %q %v", got.SupplierName, got.SupplierEmail)
	}
	expectRefusal(t, c.Do(http.MethodGet, orderPath(o.Id, "pdf"), nil), http.StatusNotFound, "not_found", "an erased PDF")
}

// PurchaseSupply answers what is ordered and not received on sent orders'
// open goods lines; drafts, closed and cancelled orders count nothing.
func TestPurchaseSupply(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	supply := purchasing.Module().PurchaseSupply(h.Deps())
	createOrder(t, c, supNorsk, variantLine(varBolt, "100"))
	sent := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "3"), variantLine(varNut, "2"), variantLine(varService, "5")))
	h.Exec(t, `UPDATE purchasing.order_lines SET received = 1 WHERE id = $1`, sent.Lines[0].Id)
	closed := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "50")))
	mustStatus(t, c.Do(http.MethodPost, orderPath(closed.Id, "close"), version(closed)), http.StatusOK, "close")
	cancelled := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "70")))
	mustStatus(t, c.Do(http.MethodPost, orderPath(cancelled.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: cancelled.UpdatedAt}), http.StatusOK, "cancel")
	o := getOrder(t, c, sent.Id)
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[1].Id), "close"), version(o)), http.StatusOK, "close the nut line")

	all, err := supply.OpenAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].VariantID != varBolt || all[0].SiteID != siteMain || all[0].Quantity != "2" {
		t.Fatalf("open = %+v", all)
	}
	some, err := supply.OpenByVariants(context.Background(), i64(siteSecond), []int32{varBolt})
	if err != nil || len(some) != 0 {
		t.Errorf("at another site = %+v %v", some, err)
	}
	some, err = supply.OpenByVariants(context.Background(), nil, []int32{varBolt, varNut})
	if err != nil || len(some) != 1 {
		t.Errorf("by variants = %+v %v", some, err)
	}
}

func ptrEmail(s string) *openapi_types.Email { e := openapi_types.Email(s); return &e }
