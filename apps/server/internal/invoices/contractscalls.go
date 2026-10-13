package invoices

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is the whole of this module's reach outside its own schema: the
// customer, project and user directories (deps.Directory, deps.Projects,
// deps.Users), the source modules' billable reads (deps.BillableHours,
// BillableExpenses, BillableMilestones) and their invoiced-work holders
// (deps.InvoicedWork), the object store (server.objects), the SMTP seam
// (deps.SMTPSend), the Peppol network (server.peppolLookup), the access
// point (accessPoint) and the payment provider (paymentProvider). Every call is made through one of the thin accessors
// below and through nowhere else, so "what does Invoices ask of its
// neighbours, and when" has one place to read the answer and one place to
// check it from.
//
// What is checked is the lock rule (module-boundaries rule 10, restated): no
// call that takes its own connection or leaves the process while a
// transaction holds locks (withLockedTx). A directory reads through the same
// connection pool, and a slow store, mail server or provider under a row lock
// is the same hazard by another route. noteContractCall pins it on every path,
// at a production cost of one nil comparison per call. A holder's command is
// the one call made under a lock — it runs on the caller's transaction and
// does neither — and noteTxCommand reports it as bound to that transaction,
// so a test can tell it is made under one, and only under one.

// contractCallHook is handed the context and the name of every call out of the
// module, and whether it is a transaction-bound command (noteTxCommand). It is
// nil in production and installed once, before any test runs, by this
// package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string, txBound bool)

// noteContractCall reports one call out of the module that takes its own
// connection or leaves the process: never under a lock.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method, false)
	}
}

// noteTxCommand reports one command run on this module's own transaction by
// another module's holder, named "InvoicedWork.<kind>.Mark|Release": only
// ever under a lock.
func noteTxCommand(ctx context.Context, name string) {
	if contractCallHook != nil {
		contractCallHook(ctx, name, true)
	}
}

// customerProfile is the billing profile, the one read an invoice's gates and
// its buyer snapshot are made from (D4, D10).
func (s *server) customerProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	noteContractCall(ctx, "Directory.BillingProfile")
	return s.deps.Directory.BillingProfile(ctx, id)
}

// projectEntries is the projects a draft's held work belongs to, read before
// the issue's transaction (invoices work design D1): whom each still bills
// and how. An empty batch asks nobody; a caller checks deps.Projects first —
// nil is the module switched off.
func (s *server) projectEntries(ctx context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	noteContractCall(ctx, "Projects.Projects")
	return s.deps.Projects.Projects(ctx, ids)
}

// userEntry is one user, the issuer whose name a holder's timeline event
// carries, read before the issue's transaction (plan reading 20). nil for a
// user the directory does not know.
func (s *server) userEntry(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.User")
	return s.deps.Users.User(ctx, id)
}

// projectEntry is the one project the uninvoiced view is asked about
// (invoices work design D3): its customer, billing type and currency. nil for
// a project the directory does not know; a caller checks deps.Projects first.
func (s *server) projectEntry(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	noteContractCall(ctx, "Projects.Project")
	return s.deps.Projects.Project(ctx, id)
}

// projectsForCustomer is every project a customer is billed for, the
// uninvoiced view's projects (D3), at most contracts.MaxActualsRequests of
// them — one billable read's batch. A caller checks deps.Projects first.
func (s *server) projectsForCustomer(ctx context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	noteContractCall(ctx, "Projects.ProjectsForCustomer")
	return s.deps.Projects.ProjectsForCustomer(ctx, customerID)
}

// userEntries names the people whose hours the uninvoiced view lists, and
// whom the wizard's lines name (D3, D4), in one round trip. An empty batch
// asks nobody; a user the directory does not know is absent.
func (s *server) userEntries(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	noteContractCall(ctx, "Users.Users")
	return s.deps.Users.Users(ctx, ids)
}

// markInvoiced and releaseInvoiced are a holder's two commands, run on the
// issue's own transaction (rule 10) and reported as bound to it.
func markInvoiced(ctx context.Context, tx pgx.Tx, kind contracts.WorkSourceKind, h contracts.InvoicedWorkHolder,
	ref contracts.InvoiceRef, sources []contracts.WorkSource,
) error {
	noteTxCommand(ctx, "InvoicedWork."+string(kind)+".Mark")
	return h.MarkInvoiced(ctx, tx, ref, sources)
}

func releaseInvoiced(ctx context.Context, tx pgx.Tx, kind contracts.WorkSourceKind, h contracts.InvoicedWorkHolder,
	ref contracts.InvoiceRef, sources []contracts.WorkSource,
) error {
	noteTxCommand(ctx, "InvoicedWork."+string(kind)+".Release")
	return h.ReleaseInvoiced(ctx, tx, ref, sources)
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
// never inside withLockedTx: the uninvoiced view, the wizard's reads before
// its transaction, a held source's freshness on GET and the reads
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

// providerCreds is what a caller of the payment provider needs of the
// credentials beside the provider itself: the sales unit an attempt is
// recorded under.
type providerCreds struct {
	msn string
}

// paymentProvider is Vipps for the stored sales unit, its keys opened
// (invoices payments and reminders design D13, D14), dialling
// Config.InvoicesVippsBaseURL through Deps.HTTPTransport on the module's
// clock. Every call on it is reported as payments.<Method>, and a 401 or 403
// it answers flags the credentials as rejected. With INVOICES_VIPPS_ENABLED
// off it makes no client and answers errPaymentsSwitchedOff (m12), so a
// verify, a webhook registration, an attempt's create, an on-site request
// and the worker all stop here; without a credentials row it answers
// errNoPaymentCredentials, and for keys the secrets box cannot open
// errPaymentKeysUnreadable — logged at error and flagged as rejected. It
// reads the row on the pool: it is never called inside withLockedTx, and no
// call on what it answers may be.
func (s *server) paymentProvider(ctx context.Context) (payments.Provider, providerCreds, error) {
	cfg := s.deps.Config
	if cfg == nil || !cfg.InvoicesVippsEnabled {
		return nil, providerCreds{}, errPaymentsSwitchedOff
	}
	row, err := store.New(s.deps.Pool).GetPaymentCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, providerCreds{}, errNoPaymentCredentials
	case err != nil:
		return nil, providerCreds{}, fmt.Errorf("invoices: read the Vipps credentials: %w", err)
	}
	settings, err := vippsSettingsOf(row)
	if err != nil {
		return nil, providerCreds{}, err
	}
	keys, err := s.openVippsKeys(row.SecretCiphertext)
	if err != nil {
		s.flagPaymentCredentialsRejected(ctx, true)
		return nil, providerCreds{}, err
	}
	if row.Provider != providerVipps {
		return nil, providerCreds{}, fmt.Errorf("invoices: unknown payment provider %q", row.Provider)
	}
	client, err := vipps.New(vipps.Options{
		BaseURL: cfg.InvoicesVippsBaseURL, ClientID: keys.ClientID, ClientSecret: keys.ClientSecret,
		SubscriptionKey: keys.SubscriptionKey, MSN: settings.MSN,
		Transport: s.deps.HTTPTransport, Now: s.deps.Clock,
	})
	if err != nil {
		return nil, providerCreds{}, fmt.Errorf("invoices: build the Vipps client: %w", err)
	}
	return notedProvider{p: client, s: s}, providerCreds{msn: settings.MSN}, nil
}

// notedProvider reports every call on the payment provider before making it,
// and records a 401 or 403 as the credentials' refusal, on the pool.
type notedProvider struct {
	p payments.Provider
	s *server
}

var _ payments.Provider = notedProvider{}

// flagged records an ErrUnauthorized as the credentials' refusal and
// answers err as it was.
func (n notedProvider) flagged(ctx context.Context, err error) error {
	if errors.Is(err, payments.ErrUnauthorized) {
		n.s.flagPaymentCredentialsRejected(ctx, false)
	}
	return err
}

func (n notedProvider) Create(ctx context.Context, r payments.CreateRequest) (payments.Created, error) {
	noteContractCall(ctx, "payments.Create")
	created, err := n.p.Create(ctx, r)
	return created, n.flagged(ctx, err)
}

func (n notedProvider) Get(ctx context.Context, reference string) (payments.Payment, error) {
	noteContractCall(ctx, "payments.Get")
	p, err := n.p.Get(ctx, reference)
	return p, n.flagged(ctx, err)
}

func (n notedProvider) Capture(ctx context.Context, reference string, amount payments.Money, idempotencyKey string) (payments.Payment, error) {
	noteContractCall(ctx, "payments.Capture")
	p, err := n.p.Capture(ctx, reference, amount, idempotencyKey)
	return p, n.flagged(ctx, err)
}

func (n notedProvider) Cancel(ctx context.Context, reference string, idempotencyKey string) (payments.Payment, error) {
	noteContractCall(ctx, "payments.Cancel")
	p, err := n.p.Cancel(ctx, reference, idempotencyKey)
	return p, n.flagged(ctx, err)
}

func (n notedProvider) Refund(ctx context.Context, reference string, amount payments.Money, idempotencyKey string) (payments.Payment, error) {
	noteContractCall(ctx, "payments.Refund")
	p, err := n.p.Refund(ctx, reference, amount, idempotencyKey)
	return p, n.flagged(ctx, err)
}

func (n notedProvider) CaptureTime(ctx context.Context, reference string) (time.Time, bool, error) {
	noteContractCall(ctx, "payments.CaptureTime")
	t, ok, err := n.p.CaptureTime(ctx, reference)
	return t, ok, n.flagged(ctx, err)
}

// VerifyWebhook is computed here, from the request and the secret: it leaves
// the process for nothing, so it is not a contract call.
func (n notedProvider) VerifyWebhook(r payments.WebhookRequest, secret string, registeredURL *url.URL) (payments.WebhookEvent, error) {
	return n.p.VerifyWebhook(r, secret, registeredURL)
}

func (n notedProvider) Verify(ctx context.Context) error {
	noteContractCall(ctx, "payments.Verify")
	return n.flagged(ctx, n.p.Verify(ctx))
}
