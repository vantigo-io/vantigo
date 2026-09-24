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
// out. An anonymised customer's file is what is left of it.
func (s *server) GetCustomersByIdPersonalData(ctx context.Context, req gen.GetCustomersByIdPersonalDataRequestObject) (gen.GetCustomersByIdPersonalDataResponseObject, error) {
	var (
		row       store.CustomerForPersonalDataRow
		customer  customerRow
		addresses []store.CustomersCustomerAddress
		contacts  []store.ListContactAssociationsForCustomerRow
		roleRows  []store.ContactRolesForCustomerRow
		entries   []store.CustomersCustomersTimelineEntry
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
		entries, err = txq.ListTimelineEntriesForExport(ctx, req.Id)
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
	var identity *gen.LegalIdentityResponse
	if id := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType); id != nil {
		answer := legalIdentityResponse(*id)
		identity = &answer
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
	exportedTimeline := make([]gen.TimelineResponse, 0, len(entries))
	for _, e := range entries {
		exportedTimeline = append(exportedTimeline, timelineResponse(e, followUps))
	}

	return personalDataDownload{
		fileName: fmt.Sprintf("customer-%d-personal-data.json", row.CustomerNumber),
		body: gen.CustomerPersonalData{
			ExportedAt: s.deps.Clock().UTC(),
			Customer: gen.CustomerPersonalDataCustomer{
				Id: row.ID, CustomerNumber: row.CustomerNumber, Name: row.Name, Type: row.Type, Status: row.Status,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
				Identity:    identity,
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
				Anonymisation: dec.anonymisationOf(row.ID),
			},
			Contacts: exportedContacts,
			Timeline: exportedTimeline,
			Modules:  sections,
		},
	}, nil
}
