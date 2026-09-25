package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is GET /customers/{id}/personal-data (customers GDPR design D3): a
// private person's data, all of it, in one file — what this module holds, read
// in one snapshot, and what every other module holds, through
// contracts.CustomerPersonalData (docs/module-boundaries.md rule 9). It is
// shaped by nothing but customers:personal-data: that key means "may hand this
// person their data", so the legal identity and the contacts are in the file
// whether or not the caller could read them one by one. It is built in memory
// and streamed from the request, the CSV export's shape — no object store, no
// job: one person's data is one response.

// personalDataNotAPerson is the export's and the scheduling's refusal for a
// business (design D3): a company is not a data subject, and its contacts are
// people handled through customers of their own if they are anything here.
func personalDataNotAPerson(number int64, name string) *gen.CustomerConflictProblem {
	return mergeConflict("Customer is not a private person", "personal_data_not_a_person", fmt.Sprintf(
		"%s is a business, and personal data is a private person's. A business's contacts are handled through customers of their own, if they are customers at all.",
		customerLabel(number, name)))
}

// personalDataDownload writes the file: the generated 200 would be a bare
// application/json that names no file, and this answer has to say what to call
// it and that nobody may cache it — csvDownload's reasoning, and its headers.
// The body is marshalled before a header is written, so a failure is a 500
// rather than half a file.
type personalDataDownload struct {
	body     gen.CustomerPersonalData
	fileName string
}

func (d personalDataDownload) VisitGetCustomersByIdPersonalDataResponse(w http.ResponseWriter) error {
	body, err := json.Marshal(d.body)
	if err != nil {
		return fmt.Errorf("customers: encode the personal data file: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A person's whole file: in nobody's cache, least of all a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

// GetCustomersByIdPersonalData Export a private person's data
// (GET /api/v1/customers/{id}/personal-data)
//
// Order: (1) this module's own rows in one read-only REPEATABLE READ snapshot —
// the contacts list's reason: several reads answering one file must see one
// instant — its 404, and the refusal for a business, read from the same row;
// (2) the decoration and the follow-up assignees from the pool, after the
// snapshot, since both ask the user directory; (3) each module's section,
// outside any transaction of this module's (rule 9), a nil one leaving its key
// out. One module failing fails the whole export with a 500: a file with that
// module's section quietly missing would claim to be complete. An anonymised
// customer's file is what is left of it.
func (s *server) GetCustomersByIdPersonalData(ctx context.Context, req gen.GetCustomersByIdPersonalDataRequestObject) (gen.GetCustomersByIdPersonalDataResponseObject, error) {
	var (
		row       store.CustomerForPersonalDataRow
		customer  customerRow
		addresses []store.CustomersCustomerAddress
		contacts  []store.ListContactAssociationsForCustomerRow
		roleRows  []store.ContactRolesForCustomerRow
		entries   []store.CustomersCustomersTimelineEntry
		revisions []store.CustomersCustomersTimelineEntriesRevision
		merged    []store.CustomersMergedIntoForPersonalDataRow
	)
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		if row, err = txq.CustomerForPersonalData(ctx, req.Id); err != nil || row.Type != "person" {
			return err
		}
		plain, err := txq.GetCustomer(ctx, req.Id)
		if err != nil {
			return err
		}
		summary, err := txq.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return err
		}
		customer = fromCustomerRow(plain, summary)
		if addresses, err = txq.ListCustomerAddresses(ctx, req.Id); err != nil {
			return err
		}
		if contacts, err = txq.ListContactAssociationsForCustomer(ctx, req.Id); err != nil {
			return err
		}
		if roleRows, err = txq.ContactRolesForCustomer(ctx, req.Id); err != nil {
			return err
		}
		if merged, err = txq.CustomersMergedIntoForPersonalData(ctx, req.Id); err != nil {
			return err
		}
		if entries, err = txq.ListTimelineEntriesForExport(ctx, req.Id); err != nil {
			return err
		}
		revisions, err = txq.ListTimelineRevisionsForExport(ctx, req.Id)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetCustomersByIdPersonalData404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: read customer %d's personal data: %w", req.Id, err)
	case row.Type != "person":
		return gen.GetCustomersByIdPersonalData409ApplicationProblemPlusJSONResponse(*personalDataNotAPerson(row.CustomerNumber, row.Name)), nil
	}

	q := store.New(s.deps.Pool)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return nil, err
	}
	var assignees []uuid.UUID
	for _, e := range entries {
		if e.FollowUpAssigneeUserID != nil {
			assignees = append(assignees, *e.FollowUpAssigneeUserID)
		}
	}
	for _, r := range revisions {
		if r.FollowUpAssigneeUserID != nil {
			assignees = append(assignees, *r.FollowUpAssigneeUserID)
		}
	}
	followUps, err := s.decorateFollowUpAssignees(ctx, assignees)
	if err != nil {
		return nil, err
	}

	sections := map[string]interface{}{}
	for _, holder := range s.deps.CustomerPersonalData {
		section, err := holder.Data.ExportCustomerData(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: export %s's data about customer %d: %w", holder.Module, req.Id, err)
		}
		if section != nil {
			sections[holder.Module] = section
		}
	}

	rate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	mergedFrom, err := mergedFromResponse(merged)
	if err != nil {
		return nil, err
	}
	exportedAddresses := make([]gen.CustomerAddress, 0, len(addresses))
	for _, a := range addresses {
		exportedAddresses = append(exportedAddresses, addressResponse(a))
	}
	// Grouped the way GetCustomersByIdContacts groups them: the rows arrive in
	// the fixed role order already.
	byContact := make(map[int32][]contactRole, len(contacts))
	for _, r := range roleRows {
		byContact[r.ContactID] = append(byContact[r.ContactID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}
	exportedContacts := make([]gen.CustomerContactResponse, 0, len(contacts))
	for _, r := range contacts {
		exportedContacts = append(exportedContacts, gen.CustomerContactResponse{
			Contact: gen.ContactResponse{
				Id: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				MiddleName: r.MiddleName, Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail,
			},
			Title: r.Title, Roles: genContactRoles(byContact[r.ID]),
			Phone: r.AssociationPhone, Email: r.AssociationEmail,
		})
	}
	// The rows arrive grouped by entry, oldest revision first.
	earlier := make(map[int32][]gen.TimelineRevisionResponse)
	for _, r := range revisions {
		earlier[r.CustomerTimelineEntryID] = append(earlier[r.CustomerTimelineEntryID], timelineRevisionResponse(r, followUps))
	}
	exportedTimeline := make([]gen.TimelineResponse, 0, len(entries))
	for _, e := range entries {
		entry := timelineResponse(e, followUps)
		if revs, ok := earlier[e.ID]; ok {
			entry.Revisions = &revs
		}
		exportedTimeline = append(exportedTimeline, entry)
	}

	return personalDataDownload{
		fileName: fmt.Sprintf("customer-%d-personal-data.json", row.CustomerNumber),
		body: gen.CustomerPersonalData{
			ExportedAt: s.deps.Clock().UTC(),
			Customer: gen.CustomerPersonalDataCustomer{
				Id: row.ID, CustomerNumber: row.CustomerNumber, Name: row.Name, Type: row.Type, Status: row.Status,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
				Identity:    personalDataIdentity(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType),
				ContactInfo: gen.CustomerContactInfo{Email: row.Email, Phone: row.Phone, Website: row.Website},
				Addresses:   exportedAddresses,
				BillingProfile: gen.CustomerPersonalDataBillingProfile{
					InvoiceEmail: row.InvoiceEmail, ReminderEmail: row.ReminderEmail, PaymentTermsDays: row.PaymentTermsDays,
					Currency: row.Currency, Language: row.Language, InvoiceDelivery: row.InvoiceDelivery,
					ReminderDelivery: row.ReminderDelivery, PeppolId: row.PeppolID, Gln: row.Gln,
					BuyerReference: row.BuyerReference, DefaultBillRate: rate,
				},
				Owner:         dec.owner(customer.OwnerUserID),
				Group:         dec.group(row.ID),
				Tags:          dec.tagsFor(row.ID),
				MergedInto:    dec.merged(row.ID),
				MergedFrom:    mergedFrom,
				Anonymisation: dec.anonymisationOf(row.ID),
			},
			Contacts: exportedContacts,
			Timeline: exportedTimeline,
			Modules:  sections,
		},
	}, nil
}

// personalDataIdentity is a row's legal identity as the file carries it: in
// full, whatever the caller's legal-identity keys (design D3), absent when the
// row has none.
func personalDataIdentity(country, id, name, source, typ *string) *gen.LegalIdentityResponse {
	identity := identityFromRow(country, id, name, source, typ)
	if identity == nil {
		return nil
	}
	answer := legalIdentityResponse(*identity)
	return &answer
}

// mergedFromResponse is the file's mergedFrom: each duplicate merged into the
// customer, its own row as it stands — the merge moved what hung off it, not
// the row's values, and they are the same person's data. nil when nothing was
// merged in, so the key is left out.
func mergedFromResponse(rows []store.CustomersMergedIntoForPersonalDataRow) (*[]gen.CustomerPersonalDataMergedCustomer, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	merged := make([]gen.CustomerPersonalDataMergedCustomer, 0, len(rows))
	for _, r := range rows {
		rate, err := floatPtrFromNumeric(r.DefaultBillRate)
		if err != nil {
			return nil, err
		}
		merged = append(merged, gen.CustomerPersonalDataMergedCustomer{
			Id: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name, Status: r.Status,
			Identity:    personalDataIdentity(r.LegalCountry, r.LegalID, r.LegalName, r.LegalSource, r.LegalType),
			ContactInfo: gen.CustomerContactInfo{Email: r.Email, Phone: r.Phone, Website: r.Website},
			BillingProfile: gen.CustomerPersonalDataBillingProfile{
				InvoiceEmail: r.InvoiceEmail, ReminderEmail: r.ReminderEmail, PaymentTermsDays: r.PaymentTermsDays,
				Currency: r.Currency, Language: r.Language, InvoiceDelivery: r.InvoiceDelivery,
				ReminderDelivery: r.ReminderDelivery, PeppolId: r.PeppolID, Gln: r.Gln,
				BuyerReference: r.BuyerReference, DefaultBillRate: rate,
			},
		})
	}
	return &merged, nil
}
