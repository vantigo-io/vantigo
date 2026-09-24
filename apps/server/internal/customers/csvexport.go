package customers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is GET /customers/export and GET /customers/import/template
// (customers import/export design D1, D2): the customers file (csvfile.go) on
// its way out. The export is the list a person is looking at — its filters,
// its sort, and its permission shape: the legal identity's four columns are
// absent for a caller without customers:legal-identity-view, as the identity
// is absent from their list rows. Every value a row needs beyond the list's own
// columns is read in bulk before a byte is written — one decoration (owners,
// tags, groups), one billing read, one address read — so the file costs a fixed
// number of reads however many rows it has.

// tooManyCustomersToExportTitle is the cap's 400 (design D2), the expenses
// payroll file's refusal in this module's words: a bare problem, no errors
// object, asking for a narrower filter.
const tooManyCustomersToExportTitle = "Too many customers to export"

func tooManyCustomersToExport() apicommon.ProblemDetails {
	return apicommon.Problem(tooManyCustomersToExportTitle, fmt.Sprintf(
		"This export would hold more than %d customers, which is more than one file should. Narrow it with a filter or a search, and export the rest in slices.",
		customersFileMaxRows))
}

// csvDownload writes a customers file: the generated response types hard-code
// a bare text/csv and name no file, and this answer has to say which encoding
// it is in, what to call it and that nobody may cache it — the payroll
// export's csvDownload, answering both of this module's file operations.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A customer file names who the business deals with and on what terms. It
	// belongs in nobody's cache, and least of all in a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

func (d csvDownload) VisitGetCustomersExportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d csvDownload) VisitGetCustomersImportTemplateResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// exportColumns is the export's header for a caller: every column, less the
// legal identity's four without legal-identity-view — absent, not blank, so
// the file re-imports without touching an identity (design D2).
func exportColumns(includeIdentity bool) []csvColumn {
	out := make([]csvColumn, 0, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		if c.Group == csvGroupIdentity && !includeIdentity {
			continue
		}
		out = append(out, c)
	}
	return out
}

// importTemplateColumns is every column an import writes: the table less the
// export-only four.
func importTemplateColumns() []csvColumn {
	out := make([]csvColumn, 0, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		if c.Group != csvGroupExportOnly {
			out = append(out, c)
		}
	}
	return out
}

// GetCustomersExport Export customers as CSV
// (GET /api/v1/customers/export)
func (s *server) GetCustomersExport(ctx context.Context, req gen.GetCustomersExportRequestObject) (gen.GetCustomersExportResponseObject, error) {
	p := req.Params
	filter, detail := s.customerListFilterFor(ctx, gen.GetCustomersParams{
		SortBy: p.SortBy, SortDirection: p.SortDirection, IncludeArchived: p.IncludeArchived,
		Search: p.Search, Status: p.Status, Type: p.Type, OwnerId: p.OwnerId, TagId: p.TagId, GroupId: p.GroupId,
	})
	if detail != "" {
		return gen.GetCustomersExport400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", detail)), nil
	}

	q := store.New(s.deps.Pool)
	// One row more than the cap, so "the whole file" and "more than a file may
	// hold" are told apart without a count of their own.
	list, err := q.ListCustomers(ctx, filter.listParams(customersFileMaxRows+1, 0))
	if err != nil {
		return nil, fmt.Errorf("customers: list customers for export: %w", err)
	}
	if len(list) > customersFileMaxRows {
		return gen.GetCustomersExport400ApplicationProblemPlusJSONResponse(tooManyCustomersToExport()), nil
	}
	rows := make([]customerRow, 0, len(list))
	ids := make([]int32, 0, len(list))
	for _, r := range list {
		rows = append(rows, fromListRow(r))
		ids = append(ids, r.ID)
	}

	dec, err := s.decorate(ctx, q, rows...)
	if err != nil {
		return nil, err
	}
	profiles := make(map[int32]billingProfile, len(rows))
	addresses := make(map[int32]map[string]store.CustomersCustomerAddress, len(rows))
	if len(ids) > 0 {
		billing, err := q.CustomerBillingProfilesForCustomers(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("customers: read billing profiles for export: %w", err)
		}
		for _, b := range billing {
			rate, err := floatPtrFromNumeric(b.DefaultBillRate)
			if err != nil {
				return nil, err
			}
			profiles[b.ID] = billingProfileFromRow(b.InvoiceEmail, b.ReminderEmail, b.PaymentTermsDays, b.Currency, b.Language,
				b.InvoiceDelivery, b.ReminderDelivery, b.PeppolID, b.Gln, b.BuyerReference, rate)
		}
		primaries, err := q.PrimaryAddressesForCustomers(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("customers: read addresses for export: %w", err)
		}
		for _, a := range primaries {
			if addresses[a.CustomerID] == nil {
				addresses[a.CustomerID] = map[string]store.CustomersCustomerAddress{}
			}
			addresses[a.CustomerID][a.Type] = a
		}
	}

	return csvDownload{
		body:     customersCSV(exportColumns(filter.searchIdentity), rows, dec, profiles, addresses),
		fileName: fmt.Sprintf("customers-%s.csv", s.deps.Clock().UTC().Format(time.DateOnly)),
	}, nil
}

// GetCustomersImportTemplate Download the customers import template
// (GET /api/v1/customers/import/template)
func (s *server) GetCustomersImportTemplate(_ context.Context, _ gen.GetCustomersImportTemplateRequestObject) (gen.GetCustomersImportTemplateResponseObject, error) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(importTemplateColumns()))
	return csvDownload{body: b.Bytes(), fileName: "customers-import-template.csv"}, nil
}

// customersCSV is the file: the byte order mark, the header, then one row per
// customer with its cells in columns' order.
func customersCSV(columns []csvColumn, rows []customerRow, dec customerDecoration, profiles map[int32]billingProfile, addresses map[int32]map[string]store.CustomersCustomerAddress) []byte {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(columns))
	cells := make([]string, len(columns))
	for _, row := range rows {
		values := exportValues(row, dec, profiles[row.ID], addresses[row.ID])
		for i, c := range columns {
			cells[i] = values[c.Name]
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes()
}

// exportValues is one customer's cells by column name. A value there is none
// of is the empty cell, never a dash or a zero; money carries the decimal
// comma; the two instants are UTC, RFC 3339.
func exportValues(row customerRow, dec customerDecoration, profile billingProfile, addresses map[string]store.CustomersCustomerAddress) map[string]string {
	values := map[string]string{
		"customerNumber":   strconv.FormatInt(row.CustomerNumber, 10),
		"name":             row.Name,
		"type":             row.Type,
		"status":           row.Status,
		"legalCountry":     deref(row.LegalCountry),
		"legalType":        deref(row.LegalType),
		"legalId":          deref(row.LegalID),
		"legalName":        deref(row.LegalName),
		"email":            deref(row.Email),
		"phone":            deref(row.Phone),
		"website":          deref(row.Website),
		"invoiceEmail":     deref(profile.InvoiceEmail),
		"reminderEmail":    deref(profile.ReminderEmail),
		"currency":         deref(profile.Currency),
		"language":         deref(profile.Language),
		"invoiceDelivery":  deref(profile.InvoiceDelivery),
		"reminderDelivery": deref(profile.ReminderDelivery),
		"peppolId":         deref(profile.PeppolID),
		"gln":              deref(profile.Gln),
		"buyerReference":   deref(profile.BuyerReference),
		"defaultBillRate":  formatCSVDecimal(profile.DefaultBillRate),
		"id":               strconv.FormatInt(int64(row.ID), 10),
		"createdAt":        row.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":        row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if profile.PaymentTermsDays != nil {
		values["paymentTermsDays"] = strconv.Itoa(int(*profile.PaymentTermsDays))
	}
	if group := dec.group(row.ID); group != nil {
		values["group"] = group.Name
	}
	tags := dec.tagsFor(row.ID)
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	values["tags"] = strings.Join(names, csvTagSeparator)
	if owner := dec.owner(row.OwnerUserID); owner != nil {
		values["ownerName"] = owner.DisplayName
	}
	for _, prefix := range []string{"postal", "invoice"} {
		address, ok := addresses[prefix]
		if !ok {
			continue
		}
		values[prefix+"Line1"] = address.Line1
		values[prefix+"Line2"] = deref(address.Line2)
		values[prefix+"PostalCode"] = deref(address.PostalCode)
		values[prefix+"City"] = deref(address.City)
		values[prefix+"Region"] = deref(address.Region)
		values[prefix+"Country"] = address.Country
	}
	return values
}
