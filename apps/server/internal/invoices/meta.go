package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /meta: the one read every Invoices page makes before it
// draws anything (D2). It answers what this installation can do (an object
// store or not), what the seller still lacks, whether the series has started,
// the VAT codes a new line may take today, and what the caller may do — so no
// client re-derives a rule this module owns.

// GetInvoicesMeta Get the Invoices metadata
// (GET /api/v1/invoices/meta)
func (s *server) GetInvoicesMeta(ctx context.Context, _ gen.GetInvoicesMetaRequestObject) (gen.GetInvoicesMetaResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	issued, err := anythingIssued(ctx, q)
	if err != nil {
		return nil, err
	}
	today := businessDay(s.deps.Clock())
	codes, err := q.VatCodesInForce(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes in force: %w", err)
	}
	inForce := make([]gen.InvoicesVatCodeInForce, 0, len(codes))
	for _, c := range codes {
		rate, err := floatFromNumeric(c.RatePercent)
		if err != nil {
			return nil, err
		}
		inForce = append(inForce, gen.InvoicesVatCodeInForce{
			Id: c.ID, Code: c.Code, Name: c.Name, SafTCode: c.SafTCode, EhfCategory: c.EhfCategory,
			ExemptionReason: c.ExemptionReason, RatePercent: rate,
		})
	}
	missing := sellerMissingFields(row)
	return gen.GetInvoicesMeta200JSONResponse(gen.InvoicesMetaResponse{
		Currency:                row.DefaultCurrency,
		DefaultPaymentTermsDays: row.DefaultPaymentTermsDays,
		SellerComplete:          len(missing) == 0,
		MissingSellerFields:     missing,
		AnythingIssued:          issued,
		SeriesStart:             row.SeriesStart,
		StorageAvailable:        s.storageConfigured,
		Today:                   wireDate(today),
		VatCodes:                inForce,
		Capabilities: gen.InvoicesMetaCapabilities{
			CanCreate: s.has(ctx, "invoices:create"),
			CanIssue:  s.has(ctx, "invoices:issue"),
			CanManage: s.has(ctx, "invoices:manage"),
		},
	}), nil
}

// anythingIssued is "the counter row exists" (D2), never a count of documents.
func anythingIssued(ctx context.Context, q *store.Queries) (bool, error) {
	_, err := q.CounterNextValue(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: read the document counter: %w", err)
	}
	return true, nil
}
