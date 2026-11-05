package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the settings side of orders design O7: each Products tax
// category maps to one of this module's VAT codes, and contracts.SalesTax's
// ForTaxCategories (sales_tax.go) answers it. Invoices keeps the tax
// category's id only — the catalog contract carries no category names — so
// it cannot say whether the id exists in Products; the settings page lists
// the categories from the products API and sends their ids.

const invalidTaxCategoryVatCodeTitle = "Invalid tax category VAT code"

// GetInvoicesSettingsTaxCategoryVatCodes List the tax categories' VAT codes
// (GET /api/v1/invoices/settings/tax-category-vat-codes)
func (s *server) GetInvoicesSettingsTaxCategoryVatCodes(ctx context.Context, _ gen.GetInvoicesSettingsTaxCategoryVatCodesRequestObject) (gen.GetInvoicesSettingsTaxCategoryVatCodesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).TaxCategoryVatCodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the tax categories' VAT codes: %w", err)
	}
	out := make([]gen.InvoicesTaxCategoryVatCode, 0, len(rows))
	for _, r := range rows {
		out = append(out, gen.InvoicesTaxCategoryVatCode{TaxCategoryId: r.TaxCategoryID, VatCodeId: r.VatCodeID})
	}
	return gen.GetInvoicesSettingsTaxCategoryVatCodes200JSONResponse(out), nil
}

// PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId Set a tax category's VAT code
// (PUT /api/v1/invoices/settings/tax-category-vat-codes/{taxCategoryId})
func (s *server) PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId(ctx context.Context, req gen.PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryIdRequestObject) (gen.PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryIdResponseObject, error) {
	refuse := func(field, msg string) gen.PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId400ApplicationProblemPlusJSONResponse {
		return gen.PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId400ApplicationProblemPlusJSONResponse(
			invalid(invalidTaxCategoryVatCodeTitle, fieldError(field, msg)))
	}
	if req.TaxCategoryId <= 0 {
		return refuse("taxCategoryId", "A tax category id is a positive number"), nil
	}
	var refusal string
	var saved store.PutTaxCategoryVatCodeRow
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// FOR SHARE holds the code's active flag until the mapping commits.
		active, err := txq.ShareVatCode(ctx, req.Body.VatCodeId)
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = fmt.Sprintf("No VAT code has id %d", req.Body.VatCodeId)
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: read VAT code %d: %w", req.Body.VatCodeId, err)
		}
		if !active {
			refusal = "The VAT code is inactive; choose an active one"
			return errRefused
		}
		saved, err = txq.PutTaxCategoryVatCode(ctx, store.PutTaxCategoryVatCodeParams{
			TaxCategoryID: req.TaxCategoryId, VatCodeID: req.Body.VatCodeId, Now: s.deps.Clock(),
		})
		return err
	})
	switch {
	case refusal != "":
		return refuse("vatCodeId", refusal), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: map tax category %d to a VAT code: %w", req.TaxCategoryId, err)
	}
	return gen.PutInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId200JSONResponse{
		TaxCategoryId: saved.TaxCategoryID, VatCodeId: saved.VatCodeID,
	}, nil
}

// DeleteInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId Remove a tax category's VAT code
// (DELETE /api/v1/invoices/settings/tax-category-vat-codes/{taxCategoryId})
func (s *server) DeleteInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId(ctx context.Context, req gen.DeleteInvoicesSettingsTaxCategoryVatCodesByTaxCategoryIdRequestObject) (gen.DeleteInvoicesSettingsTaxCategoryVatCodesByTaxCategoryIdResponseObject, error) {
	if err := store.New(s.deps.Pool).DeleteTaxCategoryVatCode(ctx, req.TaxCategoryId); err != nil {
		return nil, fmt.Errorf("invoices: remove tax category %d's VAT code: %w", req.TaxCategoryId, err)
	}
	return gen.DeleteInvoicesSettingsTaxCategoryVatCodesByTaxCategoryId204Response{}, nil
}
