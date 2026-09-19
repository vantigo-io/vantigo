package expenses

import (
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file renders stored rows onto the wire. The shaping rule throughout:
// what is not there is absent, never null — a nil pointer on an omitempty
// field, so a client can tell "no lock is set" from "the lock is null".

// settingsResponse renders the installation's settings row.
func settingsResponse(row store.ExpensesSetting) (gen.ExpensesSettingsResponse, error) {
	markup, err := floatFromNumeric(row.DefaultMarkupPercent)
	if err != nil {
		return gen.ExpensesSettingsResponse{}, err
	}
	threshold, err := floatPtrFromNumeric(row.ReceiptRequiredOver)
	if err != nil {
		return gen.ExpensesSettingsResponse{}, err
	}
	resp := gen.ExpensesSettingsResponse{
		DefaultCurrency:      row.DefaultCurrency,
		DefaultMarkupPercent: markup,
		ReceiptRequiredOver:  threshold,
	}
	if row.LockedBefore.Valid {
		resp.LockedBefore = &openapi_types.Date{Time: row.LockedBefore.Time}
	}
	return resp, nil
}

// rateResponse renders one dated rate row.
func rateResponse(row store.ExpensesRate) (gen.ExpensesRateResponse, error) {
	value, err := floatFromNumeric(row.Value)
	if err != nil {
		return gen.ExpensesRateResponse{}, err
	}
	return gen.ExpensesRateResponse{
		Id:        row.ID,
		Kind:      row.Kind,
		ValidFrom: openapi_types.Date{Time: row.ValidFrom.Time},
		Value:     value,
		Currency:  row.Currency,
		Source:    row.Source,
	}, nil
}

// rateResponses renders rows in the order they were read.
func rateResponses(rows []store.ExpensesRate) ([]gen.ExpensesRateResponse, error) {
	out := make([]gen.ExpensesRateResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := rateResponse(row)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

// categoryResponse renders one category row.
func categoryResponse(row store.ExpensesCategory) gen.ExpensesCategoryResponse {
	return gen.ExpensesCategoryResponse{
		Id:       row.ID,
		Name:     row.Name,
		Active:   row.Active,
		Position: row.Position,
	}
}

// categoryResponses renders rows in the order they were read.
func categoryResponses(rows []store.ExpensesCategory) []gen.ExpensesCategoryResponse {
	out := make([]gen.ExpensesCategoryResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryResponse(row))
	}
	return out
}
