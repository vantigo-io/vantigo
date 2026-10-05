package invoices

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is the whole of this module's reach outside its own schema: the
// customer directory (deps.Directory), the source modules' billable reads
// (deps.BillableHours, BillableExpenses, BillableMilestones), the object store
// (server.objects), the SMTP seam (deps.SMTPSend), the Peppol network
// (server.peppolLookup) and the access point (accessPoint).
// Every call is made through one of the thin accessors below and through
// nowhere else, so "what does Invoices ask of its neighbours, and when" has one
// place to read the answer and one place to check it from.
//
// What is checked is the rule of D6 and D7: no directory call, no
// object-store call, no lookup and no send is ever made inside a transaction holding locks
// (withLockedTx). The directory reads through the same connection pool, and a
// slow store under a row lock is the same hazard by another route.
// noteContractCall pins the rule on every path, at a production cost of one nil
// comparison per call.

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs, by
// this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call out of the module.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
}

// customerProfile is the billing profile, the one read an invoice's gates and
// its buyer snapshot are made from (D4, D10).
func (s *server) customerProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	noteContractCall(ctx, "Directory.BillingProfile")
	return s.deps.Directory.BillingProfile(ctx, id)
}

// customerEntries names a page of drafts' customers in one round trip. An
// empty batch asks nobody.
func (s *server) customerEntries(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	noteContractCall(ctx, "Directory.Customers")
	return s.deps.Directory.Customers(ctx, ids)
}

// billableHours, billableExpenses and billableMilestones are the source
// modules' line-level reads (invoices work design D3), made on the pool and
// never inside withLockedTx: a held source's freshness on GET and the reads
// refreshSources makes before its save's transaction. A caller checks the
// slot is composed first — nil is the module switched off.
func (s *server) billableHours(ctx context.Context, req contracts.BillableRequest) (contracts.BillableHoursPage, error) {
	noteContractCall(ctx, "BillableHours.BillableHours")
	return s.deps.BillableHours.BillableHours(ctx, req)
}

func (s *server) billableExpenses(ctx context.Context, req contracts.BillableRequest) (contracts.BillableExpensesPage, error) {
	noteContractCall(ctx, "BillableExpenses.BillableExpenses")
	return s.deps.BillableExpenses.BillableExpenses(ctx, req)
}

func (s *server) billableMilestones(ctx context.Context, req contracts.BillableRequest) (contracts.BillableMilestonesPage, error) {
	noteContractCall(ctx, "BillableMilestones.BillableMilestones")
	return s.deps.BillableMilestones.BillableMilestones(ctx, req)
}

// objectPut, objectGet and objectExists are the object store.
func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	noteContractCall(ctx, "ObjectStore.Put")
	return s.objects.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	noteContractCall(ctx, "ObjectStore.Get")
	return s.objects.Get(ctx, key)
}

func (s *server) objectExists(ctx context.Context, key string) (bool, error) {
	noteContractCall(ctx, "ObjectStore.Exists")
	return s.objects.Exists(ctx, key)
}

// smtpSend hands one mail to the installation's SMTP server (payments and
// delivery design D4): Deps.SMTPSend when a harness set one, otherwise
// mail.SendOutbound, the platform's guarded path.
func (s *server) smtpSend(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error {
	noteContractCall(ctx, "SMTPSend")
	if s.deps.SMTPSend != nil {
		return s.deps.SMTPSend(ctx, cfg, out)
	}
	return mail.SendOutbound(ctx, cfg, out)
}

// errPeppolLookupDisabled is lookupReceiver's answer when PEPPOL_LOOKUP_ENABLED
// is off. A caller judges ehfAvailable first and never gets here; the error
// keeps a slip from being a nil call.
var errPeppolLookupDisabled = errors.New("invoices: the peppol lookup is disabled")

// lookupReceiver asks the Peppol network whether participant can receive an
// invoice or a credit note (EHF and KID design D6): the send's re-check, made
// at request time and never read from the customers module's stored answer.
// A failure is logged by its kind only — its message can carry the
// organisation number — and returned for the caller to answer.
func (s *server) lookupReceiver(ctx context.Context, participant string) (peppol.Result, error) {
	if s.peppolLookup == nil {
		return peppol.Result{}, errPeppolLookupDisabled
	}
	noteContractCall(ctx, "Peppol.Lookup")
	res, err := s.peppolLookup(ctx, participant)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: peppol lookup failed", "errorKind", peppolErrorKind(err))
	}
	return res, err
}

// accessPoint is the provider the stored credentials name, its key opened
// (EHF and KID design D7), dialling Config.InvoicesStorecoveBaseURL through
// Deps.HTTPTransport. Every call on it is reported as AccessPoint.<Method>.
// It answers errNoAccessPoint without a credentials row, and
// errAccessPointKeyUnreadable for a key the secrets box cannot open — which
// it logs at error and flags as rejected, for every caller alike (D9). It
// reads the row on the pool: it is never called inside withLockedTx, and no
// call on what it answers may be.
func (s *server) accessPoint(ctx context.Context) (accesspoint.AccessPoint, error) {
	return s.accessPointOn(ctx, s.deps.Pool)
}

// accessPointOn is accessPoint reading the credentials, and flagging an
// unreadable key, through db: the events worker passes the connection its
// advisory lease holds, so its cycle never asks the pool for a second one.
func (s *server) accessPointOn(ctx context.Context, db store.DBTX) (accesspoint.AccessPoint, error) {
	row, err := store.New(db).GetAccessPointCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, errNoAccessPoint
	case err != nil:
		return nil, fmt.Errorf("invoices: read the access point credentials: %w", err)
	}
	settings, err := accessPointSettingsOf(row)
	if err != nil {
		return nil, err
	}
	key, err := s.deps.Secrets.OpenString(accessPointCredentialPurpose, row.SecretCiphertext)
	if err != nil || key == "" {
		s.flagUnreadableAccessPointKeyOn(ctx, db)
		return nil, errAccessPointKeyUnreadable
	}
	if row.Provider != providerStorecove {
		return nil, fmt.Errorf("invoices: unknown access point provider %q", row.Provider)
	}
	baseURL := ""
	if s.deps.Config != nil {
		baseURL = s.deps.Config.InvoicesStorecoveBaseURL
	}
	return notedAccessPoint{accesspoint.NewStorecove(baseURL, key, int(settings.LegalEntityID), s.deps.HTTPTransport, s.deps.Clock)}, nil
}

// notedAccessPoint reports every call on an access point before making it.
type notedAccessPoint struct {
	ap accesspoint.AccessPoint
}

func (n notedAccessPoint) Submit(ctx context.Context, sub accesspoint.Submission) (accesspoint.SubmissionRef, error) {
	noteContractCall(ctx, "AccessPoint.Submit")
	return n.ap.Submit(ctx, sub)
}

func (n notedAccessPoint) NextEvent(ctx context.Context) (accesspoint.Event, bool, error) {
	noteContractCall(ctx, "AccessPoint.NextEvent")
	return n.ap.NextEvent(ctx)
}

func (n notedAccessPoint) AckEvent(ctx context.Context, eventID string) error {
	noteContractCall(ctx, "AccessPoint.AckEvent")
	return n.ap.AckEvent(ctx, eventID)
}

func (n notedAccessPoint) Evidence(ctx context.Context, ref accesspoint.SubmissionRef) (accesspoint.Evidence, error) {
	noteContractCall(ctx, "AccessPoint.Evidence")
	return n.ap.Evidence(ctx, ref)
}

func (n notedAccessPoint) Verify(ctx context.Context) error {
	noteContractCall(ctx, "AccessPoint.Verify")
	return n.ap.Verify(ctx)
}
