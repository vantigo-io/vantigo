package modtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakePurchaseSupply is a contracts.PurchaseSupply a test shapes, for a module
// that reads Purchasing's open purchases — Inventory's low-stock list and
// stock card, Manufacturing's replenishment — to be tested without composing
// Purchasing beside it (purchasing design P4, A3). Set replaces what is on
// order; OpenByVariants answers the rows of the variants asked, at the site
// when one is given, and OpenAll every row. Every call is counted in Calls,
// and SetError makes every call fail. It is safe for concurrent use.
type FakePurchaseSupply struct {
	mu    sync.Mutex
	rows  []contracts.OpenPurchase
	err   error
	calls int
}

var _ contracts.PurchaseSupply = (*FakePurchaseSupply)(nil)

// NewFakePurchaseSupply is a provider with nothing on order.
func NewFakePurchaseSupply() *FakePurchaseSupply { return &FakePurchaseSupply{} }

// Set replaces what is on order.
func (f *FakePurchaseSupply) Set(rows ...contracts.OpenPurchase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = slices.Clone(rows)
}

// SetError makes every call answer err (nil to answer again).
func (f *FakePurchaseSupply) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Calls is how many calls the fake answered or refused.
func (f *FakePurchaseSupply) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// OpenByVariants answers the rows of the variants asked, at siteID when set.
func (f *FakePurchaseSupply) OpenByVariants(_ context.Context, siteID *int64, variantIDs []int32) ([]contracts.OpenPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(variantIDs) > contracts.MaxStockVariants {
		return nil, fmt.Errorf("modtest: OpenByVariants of %d variants", len(variantIDs))
	}
	var out []contracts.OpenPurchase
	for _, r := range f.rows {
		if slices.Contains(variantIDs, r.VariantID) && (siteID == nil || r.SiteID == *siteID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// OpenAll answers every row.
func (f *FakePurchaseSupply) OpenAll(context.Context) ([]contracts.OpenPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return slices.Clone(f.rows), nil
}
