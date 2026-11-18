package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is finding the consumer (invoices phase 6 design I30, plan D5):
// a person is looked up at the provider by the billing profile's phone,
// e-mail, date of birth, name and address — never a fødselsnummer, which
// this module never holds — and the answer kept in invoices.consumer_lookups,
// Invoices' own, due again in seven days. Nothing of the person is copied:
// the lookup's input is read from the profile each time. The provider is
// called outside any transaction.

const (
	// lookupStatusNotChecked is a customer never looked up, on the wire only.
	lookupStatusNotChecked = "not_checked"
	lookupFound            = "found"
	lookupNotFound         = "not_found"
	lookupBlocked          = "blocked"
	lookupAmbiguous        = "ambiguous"

	// lookupMaxAge is how old a lookup may be before a send or the worker
	// asks again.
	lookupMaxAge = 7 * 24 * time.Hour

	codeEfakturaPersonOnly   = "efaktura_person_only"
	codeEfakturaLookupFailed = "efaktura_lookup_failed"
)

// sendableLookup is whether a stored lookup lets an eFaktura go.
func sendableLookup(l store.InvoicesConsumerLookup) bool {
	return l.Status == lookupFound && l.EfakturaIdentifier != nil && (l.AutoAccept || l.HasAgreement)
}

// consumerOf is a person's lookup key from the billing profile: the phone
// with its country code, the e-mail, the date of birth, the name split at its
// last word, and the invoice address.
func consumerOf(p *contracts.CustomerBillingProfile) consumerinvoice.Consumer {
	c := consumerinvoice.Consumer{Email: strings.TrimSpace(p.InvoiceEmail), DateOfBirth: p.DateOfBirth, Phone: phoneWithCountry(p.Phone)}
	name := strings.Fields(p.Name)
	if len(name) >= 2 {
		c.FirstName, c.LastName = strings.Join(name[:len(name)-1], " "), name[len(name)-1]
	}
	if a := p.InvoiceAddress; a != nil {
		for _, l := range []string{a.Line1, a.Line2} {
			if strings.TrimSpace(l) != "" {
				c.AddressLines = append(c.AddressLines, strings.TrimSpace(l))
			}
		}
		c.PostalCode, c.City, c.Country = strings.TrimSpace(a.PostalCode), strings.TrimSpace(a.City), strings.ToUpper(strings.TrimSpace(a.Country))
	}
	return c
}

// phoneWithCountry is a phone as the network's alias takes it: the digits
// with a leading +, a Norwegian eight-digit number given +47.
func phoneWithCountry(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	switch {
	case digits == "":
		return ""
	case strings.HasPrefix(strings.TrimSpace(raw), "+"):
		return "+" + digits
	case strings.HasPrefix(digits, "00"):
		return "+" + digits[2:]
	case len(digits) == 8:
		return "+47" + digits
	default:
		return "+" + digits
	}
}

// lookupStatusOf folds an address onto the stored status.
func lookupStatusOf(a consumerinvoice.Address) string {
	switch {
	case a.Status == consumerinvoice.LookupFound && a.Blocked:
		return lookupBlocked
	case a.Status == consumerinvoice.LookupFound:
		return lookupFound
	case a.Status == consumerinvoice.LookupAmbiguous:
		return lookupAmbiguous
	default:
		return lookupNotFound
	}
}

// refreshLookups looks the customers up and stores each answer (plan D5):
// a person whose keys the matching rule cannot use is stored not_found
// with no call; a business is skipped. At most MaxLookupBatch a call. It
// answers the stored rows by customer.
func (s *server) refreshLookups(ctx context.Context, p consumerinvoice.Provider, customerIDs []int32) (map[int32]store.InvoicesConsumerLookup, error) {
	q := store.New(s.deps.Pool)
	now := s.deps.Clock()
	out := make(map[int32]store.InvoicesConsumerLookup, len(customerIDs))
	type pending struct {
		id int32
		c  consumerinvoice.Consumer
	}
	var asks []pending
	store1 := func(id int32, status string, a consumerinvoice.Address) error {
		row, err := q.UpsertConsumerLookup(ctx, store.UpsertConsumerLookupParams{
			CustomerID: id, Status: status, EfakturaIdentifier: identifierOf(status, a),
			AutoAccept: a.AutoAccept, HasAgreement: a.HasAgreement, Now: now, NextCheckAt: now.Add(lookupMaxAge),
		})
		if err != nil {
			return fmt.Errorf("invoices: store customer %d's lookup: %w", id, err)
		}
		out[id] = row
		return nil
	}
	for _, id := range customerIDs {
		profile, err := s.customerProfile(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", id, err)
		}
		if profile == nil || profile.Type != "person" {
			if _, err := q.DeleteConsumerLookup(ctx, id); err != nil {
				return nil, fmt.Errorf("invoices: drop customer %d's lookup: %w", id, err)
			}
			continue
		}
		c := consumerOf(profile)
		if !c.MatchableKeys() {
			if err := store1(id, lookupNotFound, consumerinvoice.Address{}); err != nil {
				return nil, err
			}
			continue
		}
		asks = append(asks, pending{id: id, c: c})
	}
	for start := 0; start < len(asks); start += consumerinvoice.MaxLookupBatch {
		batch := asks[start:min(start+consumerinvoice.MaxLookupBatch, len(asks))]
		keys := make([]consumerinvoice.Consumer, 0, len(batch))
		for _, a := range batch {
			keys = append(keys, a.c)
		}
		addrs, err := p.Lookup(ctx, keys)
		if err != nil {
			return nil, err
		}
		for i, a := range batch {
			if err := store1(a.id, lookupStatusOf(addrs[i]), addrs[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// identifierOf is the identifier stored with a status: only found keeps one.
func identifierOf(status string, a consumerinvoice.Address) *string {
	if status != lookupFound || a.Identifier == "" {
		return nil
	}
	return ptr(a.Identifier)
}

// lookupResponse is a stored lookup on the wire: no identifier.
func lookupResponse(customerID int32, row *store.InvoicesConsumerLookup) gen.InvoicesConsumerLookup {
	if row == nil {
		return gen.InvoicesConsumerLookup{CustomerId: customerID, Status: lookupStatusNotChecked}
	}
	return gen.InvoicesConsumerLookup{
		CustomerId: customerID, Status: row.Status, Sendable: sendableLookup(*row),
		AutoAccept: row.AutoAccept, HasAgreement: row.HasAgreement,
		CheckedAt: ptr(row.CheckedAt), NextCheckAt: ptr(row.NextCheckAt),
	}
}

// GetInvoicesCustomersByCustomerIdEfakturaLookup Get a customer's eFaktura lookup
// (GET /api/v1/invoices/customers/{customerId}/efaktura-lookup)
func (s *server) GetInvoicesCustomersByCustomerIdEfakturaLookup(ctx context.Context, req gen.GetInvoicesCustomersByCustomerIdEfakturaLookupRequestObject) (gen.GetInvoicesCustomersByCustomerIdEfakturaLookupResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetConsumerLookup(ctx, req.CustomerId)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetInvoicesCustomersByCustomerIdEfakturaLookup200JSONResponse(lookupResponse(req.CustomerId, nil)), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read customer %d's lookup: %w", req.CustomerId, err)
	}
	return gen.GetInvoicesCustomersByCustomerIdEfakturaLookup200JSONResponse(lookupResponse(req.CustomerId, &row)), nil
}

// PostInvoicesCustomersByCustomerIdEfakturaLookup Look a customer up for eFaktura now
// (POST /api/v1/invoices/customers/{customerId}/efaktura-lookup)
func (s *server) PostInvoicesCustomersByCustomerIdEfakturaLookup(ctx context.Context, req gen.PostInvoicesCustomersByCustomerIdEfakturaLookupRequestObject) (gen.PostInvoicesCustomersByCustomerIdEfakturaLookupResponseObject, error) {
	available, _, _, err := s.efakturaAvailable(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !available {
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup503ApplicationProblemPlusJSONResponse(efakturaUnavailable()), nil
	}
	profile, err := s.customerProfile(ctx, req.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", req.CustomerId, err)
	}
	if profile == nil {
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup404Response{}, nil
	}
	if profile.Type != "person" {
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup409ApplicationProblemPlusJSONResponse(conflict(codeEfakturaPersonOnly,
			"The customer is not looked up", "eFaktura is for persons; a business receives EHF.")), nil
	}
	p, _, err := s.consumerProvider(ctx)
	switch {
	case errors.Is(err, errNoConsumerProvider), errors.Is(err, errEfakturaOff), errors.Is(err, errSubmitterUnreadable):
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup503ApplicationProblemPlusJSONResponse(efakturaUnavailable()), nil
	case err != nil:
		return nil, err
	}
	rows, err := s.refreshLookups(ctx, p, []int32{req.CustomerId})
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: an eFaktura lookup failed", "customer_id", req.CustomerId, "error", redactReason(err.Error()))
		c := conflict(codeEfakturaLookupFailed, "The eFaktura network could not be asked", "The provider could not answer. Try again.")
		c.Status = ptr(int32(http.StatusBadGateway))
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup502ApplicationProblemPlusJSONResponse(c), nil
	}
	row, ok := rows[req.CustomerId]
	if !ok {
		return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup200JSONResponse(lookupResponse(req.CustomerId, nil)), nil
	}
	return gen.PostInvoicesCustomersByCustomerIdEfakturaLookup200JSONResponse(lookupResponse(req.CustomerId, &row)), nil
}
