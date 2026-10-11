package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the two many-provider slots every module holding customer ids
// implements (D10). Both run whether or not the module is enabled — every
// schema is migrated whatever MODULES says — so their constructors need
// nothing a disabled module's Deps lacks, and neither reads a contract.

// The kinds this module reports, "<module>.<what>" in the API's camelCase.
const (
	kindInvoicesInvoices      = "invoices.invoices"
	kindInvoicesDrafts        = "invoices.drafts"
	kindInvoicesDocuments     = "invoices.documents"
	kindInvoicesPayments      = "invoices.payments"
	kindInvoicesDeliveries    = "invoices.deliveries"
	kindInvoicesTransmissions = "invoices.transmissions"
	// kindInvoicesReminderPolicies is the customers' reminder policies
	// (invoices payments and reminders design D7): the one row keyed by
	// customer, re-pointed by a merge and deleted by an erase.
	kindInvoicesReminderPolicies = "invoices.customerReminderPolicies"
	// The receivables an erase changes (invoices payments and reminders
	// design D19), reported after the five above, the policy last.
	kindInvoicesReminders          = "invoices.reminders"
	kindInvoicesChargePayments     = "invoices.chargePayments"
	kindInvoicesChargeWaivers      = "invoices.chargeWaivers"
	kindInvoicesManualDeliveries   = "invoices.manualDeliveries"
	kindInvoicesInvoiceHolds       = "invoices.invoiceHolds"
	kindInvoicesCollectionHandoffs = "invoices.collectionHandoffs"
	kindInvoicesBankTransactions   = "invoices.bankTransactions"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder:
// when two customers are merged, every document of the absorbed one — draft
// and issued — names the survivor from then on. An issued document keeps its
// buyer snapshot: the id is not printed, the snapshot is.
type customerReferenceHolder struct {
	clock func() time.Time
}

var _ contracts.CustomerReferenceHolder = (*customerReferenceHolder)(nil)

// newCustomerReferenceHolder is Module's CustomerReferences. It takes the
// clock, as projects' does, for a draft's updated_at.
func newCustomerReferenceHolder(d module.Deps) contracts.CustomerReferenceHolder {
	return &customerReferenceHolder{clock: d.Clock}
}

// RepointCustomer moves every document of from to into, inside the caller's
// transaction. from == into writes nothing and reports zeros. It locks the
// documents newest first before it writes them: the order a credit note's
// issue takes them in (LockCustomerDocuments), so the two never deadlock.
// Then the reminder policies (invoices payments and reminders design D7,
// D18): both rows by customer id ascending, after the documents — the order
// a policy PUT takes, documents first — and from's row moved, or merged into
// into's at the stricter mode with the notes joined (repointPolicy).
func (h *customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{
			{Kind: kindInvoicesInvoices, Count: 0}, {Kind: kindInvoicesReminderPolicies, Count: 0},
		}, nil
	}
	q := store.New(tx)
	if err := q.LockCustomerDocuments(ctx, store.LockCustomerDocumentsParams{FromCustomerID: from, IntoCustomerID: into}); err != nil {
		return nil, fmt.Errorf("invoices: lock customer %d's and %d's documents: %w", from, into, err)
	}
	n, err := q.RepointCustomer(ctx, store.RepointCustomerParams{
		FromCustomerID: from, IntoCustomerID: into, Now: h.clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: re-point customer %d's documents to %d: %w", from, into, err)
	}
	policies, err := repointPolicy(ctx, q, from, into, h.clock())
	if err != nil {
		return nil, err
	}
	return []contracts.RepointedReferences{
		{Kind: kindInvoicesInvoices, Count: n}, {Kind: kindInvoicesReminderPolicies, Count: policies},
	}, nil
}

// customerPersonalData is this module's contracts.CustomerPersonalData: what
// was invoiced to a private person, handed over, and on anonymisation the
// drafts erased while the issued documents stay — their payments with the
// notes blanked, their deliveries with the address blanked (D6), their
// letters withdrawn while in flight and every one's address blanked, the
// notes of their receivables and of the resolved bank lines their money
// came from blanked (invoices payments and reminders design D19) — and the
// customer marked erased, so no later send or letter reaches them.
type customerPersonalData struct {
	pool *pgxpool.Pool
	// clock is Deps.Clock, for the erased-customer marker's erased_at
	// (payments and delivery design D6).
	clock func() time.Time
	// logger is Deps.Logger, where the erase names the letters it leaves
	// (invoices payments and reminders design D19); slog's default when
	// the module is built without one.
	logger *slog.Logger
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return customerPersonalData{pool: d.Pool, clock: d.Clock, logger: logger}
}

type invoicesSection struct {
	Documents []exportedDocument `json:"documents"`
	Drafts    []exportedDocument `json:"drafts"`
	// ReminderPolicy is the customer's reminder policy (invoices payments and
	// reminders design D7, D19) — staff's decision and note about them —
	// absent when there is none.
	ReminderPolicy *exportedReminderPolicy `json:"reminderPolicy,omitempty"`
}

// exportedReminderPolicy is a customer's reminder policy as the export
// writes it: the mode, the note and when it was set.
type exportedReminderPolicy struct {
	Mode      string    `json:"mode"`
	Note      string    `json:"note"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type exportedDocument struct {
	Number          *int64           `json:"number,omitempty"`
	Kind            string           `json:"kind"`
	Credits         *exportedCredits `json:"credits,omitempty"`
	IssueDate       *string          `json:"issueDate,omitempty"`
	DeliveryDate    *string          `json:"deliveryDate,omitempty"`
	DeliveryFrom    *string          `json:"deliveryFrom,omitempty"`
	DeliveryTo      *string          `json:"deliveryTo,omitempty"`
	DeliveryAddress *exportedAddress `json:"deliveryAddress,omitempty"`
	DueDate         *string          `json:"dueDate,omitempty"`
	Currency        string           `json:"currency"`
	NetTotal        string           `json:"netTotal"`
	VatTotal        string           `json:"vatTotal"`
	GrossTotal      string           `json:"grossTotal"`
	Buyer           *exportedBuyer   `json:"buyer,omitempty"`
	YourReference   string           `json:"yourReference,omitempty"`
	OurReference    string           `json:"ourReference,omitempty"`
	OrderReference  string           `json:"orderReference,omitempty"`
	// ProjectReference is the project's code as the document snapshotted it
	// (invoices work design D9) — what its PDF and its EHF print — absent
	// when its work spans two projects or it has none.
	ProjectReference string         `json:"projectReference,omitempty"`
	Note             string         `json:"note,omitempty"`
	InternalNote     string         `json:"internalNote,omitempty"`
	Lines            []exportedLine `json:"lines"`
	// Payments, Deliveries and Transmissions are an issued document's,
	// empty when it has none; a draft has none of them, and nil leaves the
	// key out (payments and delivery design D6, EHF and KID design D12).
	Payments      []exportedPayment      `json:"payments,omitzero"`
	Deliveries    []exportedDelivery     `json:"deliveries,omitzero"`
	Transmissions []exportedTransmission `json:"transmissions,omitzero"`
	// ChargePayments, ChargeWaivers, ManualDeliveries, Reminders, Holds and
	// CollectionHandoffs are an issued document's receivables (invoices
	// payments and reminders design D19), empty when it has none; a draft
	// has none of them.
	ChargePayments     []exportedChargePayment  `json:"chargePayments,omitzero"`
	ChargeWaivers      []exportedChargeWaiver   `json:"chargeWaivers,omitzero"`
	ManualDeliveries   []exportedManualDelivery `json:"manualDeliveries,omitzero"`
	Reminders          []exportedReminder       `json:"reminders,omitzero"`
	Holds              []exportedHold           `json:"holds,omitzero"`
	CollectionHandoffs []exportedHandoff        `json:"collectionHandoffs,omitzero"`
	// Timesheet is the document's timesheet as printed (invoices work design
	// D5), an issued document's and a draft's alike — the customer received,
	// or would receive, it; nil leaves the key out of a document without one.
	Timesheet []exportedTimesheetRow `json:"timesheet,omitzero"`
}

// exportedTimesheetRow is one timesheet row as the PDF prints it: the
// person as labelled, never the time entry's note.
type exportedTimesheetRow struct {
	Position    int32  `json:"position"`
	PersonLabel string `json:"personLabel"`
	Date        string `json:"date"`
	Hours       string `json:"hours"`
	WorkType    string `json:"workType,omitempty"`
	Description string `json:"description"`
}

// exportedPayment is one registration of money received, as it was
// registered, and its removal when it was removed: a bank reference often
// names the payer, and a note is staff free text about them.
type exportedPayment struct {
	PaidOn   string `json:"paidOn"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	// Source is how it was registered — manual, ocr or camt054 — and
	// BankLine, for an imported one, what the bank said of the payer
	// (invoices payments and reminders design D19).
	Source        string            `json:"source"`
	Reference     string            `json:"reference,omitempty"`
	Note          string            `json:"note,omitempty"`
	RegisteredAt  time.Time         `json:"registeredAt"`
	RemovedAt     *time.Time        `json:"removedAt,omitempty"`
	RemovalReason string            `json:"removalReason,omitempty"`
	BankLine      *exportedBankLine `json:"bankLine,omitempty"`
}

// exportedBankLine is the bank line an imported payment or charge payment
// came from, as the bank wrote it: the booking day, the debtor's name and
// account and the remittance text (D19).
type exportedBankLine struct {
	BookedOn      string `json:"bookedOn"`
	DebtorName    string `json:"debtorName,omitempty"`
	DebtorAccount string `json:"debtorAccount,omitempty"`
	Text          string `json:"text,omitempty"`
}

// exportedChargePayment is one payment of an invoice's reminder charges, as
// registered, and its removal when it was removed (D9, D19).
type exportedChargePayment struct {
	PaidOn        string            `json:"paidOn"`
	Amount        string            `json:"amount"`
	Currency      string            `json:"currency"`
	Source        string            `json:"source"`
	Reference     string            `json:"reference,omitempty"`
	Note          string            `json:"note,omitempty"`
	RegisteredAt  time.Time         `json:"registeredAt"`
	RemovedAt     *time.Time        `json:"removedAt,omitempty"`
	RemovalReason string            `json:"removalReason,omitempty"`
	BankLine      *exportedBankLine `json:"bankLine,omitempty"`
}

// exportedChargeWaiver is one charge a letter claimed and staff released
// (D9, D19), naming its letter by sequence.
type exportedChargeWaiver struct {
	ReminderSequence int16     `json:"reminderSequence"`
	Kind             string    `json:"kind"`
	Amount           string    `json:"amount"`
	InterestThrough  *string   `json:"interestThrough,omitempty"`
	Reason           string    `json:"reason"`
	Note             string    `json:"note,omitempty"`
	WaivedAt         time.Time `json:"waivedAt"`
}

// exportedManualDelivery is one delivery recorded by hand, and its removal
// when it was removed (D8, D19).
type exportedManualDelivery struct {
	Kind          string     `json:"kind"`
	DeliveredOn   string     `json:"deliveredOn"`
	Note          string     `json:"note,omitempty"`
	RecordedAt    time.Time  `json:"recordedAt"`
	RemovedAt     *time.Time `json:"removedAt,omitempty"`
	RemovalReason string     `json:"removalReason,omitempty"`
}

// exportedReminder is one letter (D10, D19) — its level, channel, recipient
// ("" for paper and once the customer is anonymised), status, dates,
// amounts (what credit notes had taken off it among them) and regime — never its PDF's key, its Message-ID or an SMTP
// error, which may quote the address.
type exportedReminder struct {
	Sequence            int16      `json:"sequence"`
	Level               string     `json:"level"`
	AnnouncesCollection bool       `json:"announcesCollection"`
	Channel             string     `json:"channel"`
	Recipient           string     `json:"recipient"`
	Language            string     `json:"language"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"createdAt"`
	SentOn              *string    `json:"sentOn,omitempty"`
	Deadline            *string    `json:"deadline,omitempty"`
	Regime              string     `json:"regime,omitempty"`
	PrincipalOpen       *string    `json:"principalOpen,omitempty"`
	Credited            *string    `json:"credited,omitempty"`
	FeeKind             string     `json:"feeKind,omitempty"`
	Fee                 *string    `json:"fee,omitempty"`
	Compensation        *string    `json:"compensation,omitempty"`
	ChargesEarlier      *string    `json:"chargesEarlier,omitempty"`
	Interest            *string    `json:"interest,omitempty"`
	InterestWaived      *string    `json:"interestWaived,omitempty"`
	InterestPaid        *string    `json:"interestPaid,omitempty"`
	InterestFrom        *string    `json:"interestFrom,omitempty"`
	Total               *string    `json:"total,omitempty"`
	SentAt              *time.Time `json:"sentAt,omitempty"`
	FailedAt            *time.Time `json:"failedAt,omitempty"`
	WithdrawnAt         *time.Time `json:"withdrawnAt,omitempty"`
	WithdrawalReason    string     `json:"withdrawalReason,omitempty"`
}

// exportedHold is one hold of a disputed invoice and its lift (D11, D19).
type exportedHold struct {
	Kind           string     `json:"kind"`
	Note           string     `json:"note,omitempty"`
	PlacedAt       time.Time  `json:"placedAt"`
	LiftedAt       *time.Time `json:"liftedAt,omitempty"`
	LiftNote       string     `json:"liftNote,omitempty"`
	ChargesAllowed *bool      `json:"chargesAllowed,omitempty"`
}

// exportedHandoff is one hand-off to a collection agency and its withdrawal
// (D11, D19).
type exportedHandoff struct {
	HandedOn         string    `json:"handedOn"`
	Agency           string    `json:"agency"`
	AgencyReference  string    `json:"agencyReference,omitempty"`
	Note             string    `json:"note,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	WithdrawnOn      *string   `json:"withdrawnOn,omitempty"`
	WithdrawalReason string    `json:"withdrawalReason,omitempty"`
}

// exportedDelivery is one e-mail that handed a document over: to whom, when
// and with what subject. The recipient is "" once the customer was
// anonymised.
type exportedDelivery struct {
	Recipient string    `json:"recipient"`
	SentAt    time.Time `json:"sentAt"`
	Subject   string    `json:"subject"`
}

// exportedTransmission is one EHF transmission of an issued document (EHF
// and KID design D12): the provider, the document type, the state and the
// time of each, the receiver's Peppol id, the idempotency key and the
// submitted UBL's hash, and a resolution's note — no bytes, no object key,
// no provider reference. The reason is the wire's: the provider's or the
// receiver's words redacted, never last_error as stored.
type exportedTransmission struct {
	ID                  int64      `json:"id"`
	DocumentType        string     `json:"documentType"`
	Status              string     `json:"status"`
	Provider            string     `json:"provider"`
	IdempotencyKey      string     `json:"idempotencyKey"`
	ReceiverParticipant string     `json:"receiverParticipant"`
	UblSha256           string     `json:"ublSha256"`
	QueuedAt            time.Time  `json:"queuedAt"`
	SubmittedAt         *time.Time `json:"submittedAt,omitempty"`
	DeliveredAt         *time.Time `json:"deliveredAt,omitempty"`
	FailedAt            *time.Time `json:"failedAt,omitempty"`
	CancelledAt         *time.Time `json:"cancelledAt,omitempty"`
	ResolutionNote      string     `json:"resolutionNote,omitempty"`
	Reason              string     `json:"reason,omitempty"`
}

// utcOf is a nullable time in UTC, nil when unset.
func utcOf(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return ptr(t.UTC())
}

// transmissionExported is one transmission row as the export writes it.
func transmissionExported(t store.InvoicesTransmission) exportedTransmission {
	e := exportedTransmission{
		ID: t.ID, DocumentType: t.DocumentType, Status: t.Status, Provider: t.Provider,
		IdempotencyKey: t.IdempotencyKey.String(), ReceiverParticipant: t.ReceiverParticipant, UblSha256: t.UblSha256,
		QueuedAt: t.QueuedAt.UTC(), SubmittedAt: utcOf(t.SubmittedAt), DeliveredAt: utcOf(t.DeliveredAt),
		FailedAt: utcOf(t.FailedAt), CancelledAt: utcOf(t.CancelledAt), ResolutionNote: orEmpty(t.ResolutionNote),
	}
	if t.LastError != nil && strings.TrimSpace(*t.LastError) != "" {
		e.Reason = redactReason(*t.LastError)
	}
	return e
}

// exportedCredits is the issued invoice a credit note credits, as it was
// printed: its number and issue date.
type exportedCredits struct {
	Number    *int64  `json:"number,omitempty"`
	IssueDate *string `json:"issueDate,omitempty"`
}

// exportedBuyer is an issued document's whole buyer snapshot — what it
// printed about the person, and so what is held about them for as long as
// the document is kept. An unset optional field is omitted, as the wire
// omits it.
type exportedBuyer struct {
	CustomerNumber     *int64           `json:"customerNumber,omitempty"`
	Type               string           `json:"type,omitempty"`
	Name               string           `json:"name,omitempty"`
	OrganisationNumber string           `json:"organisationNumber,omitempty"`
	ForeignID          string           `json:"foreignId,omitempty"`
	Address            *exportedAddress `json:"address,omitempty"`
	PeppolID           string           `json:"peppolId,omitempty"`
	GLN                string           `json:"gln,omitempty"`
	Language           string           `json:"language,omitempty"`
}

// exportedAddress is the buyer's address or the place of delivery, each part
// in its own field; a place of delivery has no region.
type exportedAddress struct {
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	PostalCode string `json:"postalCode,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"`
}

// orEmpty is an optional column's value verbatim, "" when unset (optionalText
// trims, which an export must not).
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// addressOf is an address from its columns, nil when every one is unset.
func addressOf(line1, line2, postalCode, city, region, country *string) *exportedAddress {
	a := exportedAddress{
		Line1: orEmpty(line1), Line2: orEmpty(line2), PostalCode: orEmpty(postalCode),
		City: orEmpty(city), Region: orEmpty(region), Country: orEmpty(country),
	}
	if a == (exportedAddress{}) {
		return nil
	}
	return &a
}

// buyerOf is a document's buyer snapshot: an issued document's, written at
// issue (D4), and a credit-note draft's, the one it copied from its original
// (D8); nil on an invoice draft, which has none yet.
func buyerOf(d store.InvoicesInvoice) *exportedBuyer {
	b := exportedBuyer{
		CustomerNumber: d.BuyerCustomerNumber, Type: orEmpty(d.BuyerType), Name: orEmpty(d.BuyerName),
		OrganisationNumber: orEmpty(d.BuyerOrganisationNumber), ForeignID: orEmpty(d.BuyerForeignID),
		Address: addressOf(d.BuyerAddressLine1, d.BuyerAddressLine2, d.BuyerPostalCode,
			d.BuyerCity, d.BuyerRegion, d.BuyerCountry),
		PeppolID: orEmpty(d.BuyerPeppolID), GLN: orEmpty(d.BuyerGln), Language: orEmpty(d.BuyerLanguage),
	}
	if b == (exportedBuyer{}) {
		return nil
	}
	return &b
}

type exportedLine struct {
	Description     string  `json:"description"`
	Quantity        string  `json:"quantity"`
	Unit            string  `json:"unit,omitempty"`
	UnitPrice       string  `json:"unitPrice"`
	DiscountPercent string  `json:"discountPercent"`
	VatRatePercent  *string `json:"vatRatePercent,omitempty"`
	LineNet         string  `json:"lineNet"`
}

// dateText is a date column as the API writes one, or nil.
func dateText(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	return ptr(d.Time.Format(time.DateOnly))
}

// decimalOf is a numeric column as exact decimal text — an export is read by
// people and programs alike, and no float rounds its money.
func decimalOf(n pgtype.Numeric, places int) (string, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return r.FloatString(places), nil
}

// ExportCustomerData answers nil for a customer with nothing here — no
// document and no reminder policy. Otherwise every issued document and every
// draft, with the buyer snapshot, the place
// of delivery and the internal note: the customers export treats
// staff-written notes as data held about the person. An issued document
// carries its payments, removed ones with their removal, its deliveries
// (payments and delivery design D6) and its EHF transmissions (EHF and KID
// design D12); every document its project's code as snapshotted (invoices
// work design D9) and its timesheet as printed (D5); an issued document its
// receivables — charge payments, waivers, manual deliveries, letters, holds
// and hand-offs — and each payment its source and an imported one its bank
// line (invoices payments and reminders design D19); and the section the
// customer's reminder policy (D7, D19). Every read is in one REPEATABLE READ, READ ONLY transaction,
// as the customers module reads its own part of the export: a payment or a
// send landing midway cannot make the file disagree with itself.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	var section any
	err := readTx(ctx, p.pool, func(ctx context.Context, q *store.Queries) error {
		var err error
		section, err = exportCustomerData(ctx, q, customerID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return section, nil
}

// exportCustomerData is ExportCustomerData's reads and its file, with q bound
// to the export's transaction.
func exportCustomerData(ctx context.Context, q *store.Queries, customerID int32) (any, error) {
	docs, err := q.CustomerDocuments(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's documents: %w", customerID, err)
	}
	var policy *exportedReminderPolicy
	switch row, err := q.GetPolicy(ctx, customerID); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("invoices: read customer %d's reminder policy: %w", customerID, err)
	default:
		policy = &exportedReminderPolicy{Mode: row.Mode, Note: row.Note, UpdatedAt: row.UpdatedAt.UTC()}
	}
	if len(docs) == 0 && policy == nil {
		return nil, nil
	}
	ids := make([]int64, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	lines, err := q.LinesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's lines: %w", customerID, err)
	}
	byDoc := map[int64][]store.InvoicesLine{}
	for _, l := range lines {
		byDoc[l.InvoiceID] = append(byDoc[l.InvoiceID], l)
	}
	recv, err := readReceivables(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's receivables: %w", customerID, err)
	}
	payments, err := q.PaymentsOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's payments: %w", customerID, err)
	}
	paymentsOf := map[int64][]exportedPayment{}
	for _, r := range payments {
		amount, err := decimalOf(r.Amount, 2)
		if err != nil {
			return nil, err
		}
		e := exportedPayment{
			PaidOn: orEmpty(dateText(r.PaidOn)), Amount: amount, Currency: r.Currency, Source: r.Source,
			Reference: r.Reference, Note: r.Note, RegisteredAt: r.RegisteredAt.UTC(),
			RemovalReason: orEmpty(r.RemovalReason), BankLine: recv.lineOf(r.BankTransactionID),
		}
		if r.RemovedAt != nil {
			e.RemovedAt = ptr(r.RemovedAt.UTC())
		}
		paymentsOf[r.InvoiceID] = append(paymentsOf[r.InvoiceID], e)
	}
	deliveries, err := q.DeliveriesOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's deliveries: %w", customerID, err)
	}
	deliveriesOf := map[int64][]exportedDelivery{}
	for _, d := range deliveries {
		deliveriesOf[d.InvoiceID] = append(deliveriesOf[d.InvoiceID],
			exportedDelivery{Recipient: d.Recipient, SentAt: d.SentAt.UTC(), Subject: d.Subject})
	}
	transmissions, err := q.EveryTransmissionOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's transmissions: %w", customerID, err)
	}
	transmissionsOf := map[int64][]exportedTransmission{}
	for _, t := range transmissions {
		transmissionsOf[t.InvoiceID] = append(transmissionsOf[t.InvoiceID], transmissionExported(t))
	}
	sheets, err := q.TimesheetRowsOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's timesheets: %w", customerID, err)
	}
	timesheetOf := map[int64][]exportedTimesheetRow{}
	for _, r := range sheets {
		hours, err := decimalOf(r.Hours, 2)
		if err != nil {
			return nil, err
		}
		timesheetOf[r.InvoiceID] = append(timesheetOf[r.InvoiceID], exportedTimesheetRow{
			Position: r.Position, PersonLabel: r.PersonLabel, Date: orEmpty(dateText(r.EntryDate)), Hours: hours,
			WorkType: orEmpty(r.WorkType), Description: r.Description,
		})
	}
	// A credit note's customer is its original's (credits.go), so the
	// original is almost always among docs; one that is not is read.
	byID := make(map[int64]store.InvoicesInvoice, len(docs))
	for _, d := range docs {
		byID[d.ID] = d
	}
	section := invoicesSection{Documents: []exportedDocument{}, Drafts: []exportedDocument{}, ReminderPolicy: policy}
	for _, d := range docs {
		e := exportedDocument{
			Number: d.Number, Kind: d.Kind, IssueDate: dateText(d.IssueDate), DeliveryDate: dateText(d.DeliveryDate),
			DeliveryFrom: dateText(d.DeliveryFrom), DeliveryTo: dateText(d.DeliveryTo), DueDate: dateText(d.DueDate),
			DeliveryAddress: addressOf(d.DeliveryAddressLine1, d.DeliveryAddressLine2, d.DeliveryPostalCode,
				d.DeliveryCity, nil, d.DeliveryCountry),
			Currency: d.Currency, Buyer: buyerOf(d), YourReference: d.YourReference, OurReference: d.OurReference,
			OrderReference: d.OrderReference, ProjectReference: orEmpty(d.ProjectReference), Note: d.Note,
			InternalNote: d.InternalNote, Lines: []exportedLine{},
			Timesheet: timesheetOf[d.ID],
		}
		if d.CreditsInvoiceID != nil {
			original, ok := byID[*d.CreditsInvoiceID]
			if !ok {
				if original, err = q.GetInvoice(ctx, *d.CreditsInvoiceID); err != nil {
					return nil, fmt.Errorf("invoices: read credit note %d's original: %w", d.ID, err)
				}
			}
			e.Credits = &exportedCredits{Number: original.Number, IssueDate: dateText(original.IssueDate)}
		}
		for _, c := range []struct {
			dst *string
			n   pgtype.Numeric
		}{{&e.NetTotal, d.NetTotal}, {&e.VatTotal, d.VatTotal}, {&e.GrossTotal, d.GrossTotal}} {
			if *c.dst, err = decimalOf(c.n, 2); err != nil {
				return nil, err
			}
		}
		for _, l := range byDoc[d.ID] {
			line := exportedLine{Description: l.Description, Unit: l.Unit}
			for _, c := range []struct {
				dst    *string
				n      pgtype.Numeric
				places int
			}{{&line.Quantity, l.Quantity, 3}, {&line.UnitPrice, l.UnitPrice, 4}, {&line.DiscountPercent, l.DiscountPercent, 2}, {&line.LineNet, l.LineNet, 2}} {
				if *c.dst, err = decimalOf(c.n, c.places); err != nil {
					return nil, err
				}
			}
			if l.VatRatePercent.Valid {
				rate, err := decimalOf(l.VatRatePercent, 2)
				if err != nil {
					return nil, err
				}
				line.VatRatePercent = &rate
			}
			e.Lines = append(e.Lines, line)
		}
		if d.Status == statusIssued {
			e.Payments = append([]exportedPayment{}, paymentsOf[d.ID]...)
			e.Deliveries = append([]exportedDelivery{}, deliveriesOf[d.ID]...)
			e.Transmissions = append([]exportedTransmission{}, transmissionsOf[d.ID]...)
			e.ChargePayments = append([]exportedChargePayment{}, recv.chargePayments[d.ID]...)
			e.ChargeWaivers = append([]exportedChargeWaiver{}, recv.waivers[d.ID]...)
			e.ManualDeliveries = append([]exportedManualDelivery{}, recv.manualDeliveries[d.ID]...)
			e.Reminders = append([]exportedReminder{}, recv.reminders[d.ID]...)
			e.Holds = append([]exportedHold{}, recv.holds[d.ID]...)
			e.CollectionHandoffs = append([]exportedHandoff{}, recv.handoffs[d.ID]...)
			section.Documents = append(section.Documents, e)
		} else {
			section.Drafts = append(section.Drafts, e)
		}
	}
	return section, nil
}

// exportedReceivables is every receivable of a person's documents as the
// export writes it (invoices payments and reminders design D19), by
// document, and the bank lines their money came from, by id.
type exportedReceivables struct {
	lines            map[int64]exportedBankLine
	chargePayments   map[int64][]exportedChargePayment
	waivers          map[int64][]exportedChargeWaiver
	manualDeliveries map[int64][]exportedManualDelivery
	reminders        map[int64][]exportedReminder
	holds            map[int64][]exportedHold
	handoffs         map[int64][]exportedHandoff
}

// lineOf is the bank line id names, nil for none.
func (r exportedReceivables) lineOf(id *int64) *exportedBankLine {
	if id == nil {
		return nil
	}
	if l, ok := r.lines[*id]; ok {
		return &l
	}
	return nil
}

// decimalOrNil is a nullable numeric column as exact decimal text, nil when
// unset.
func decimalOrNil(n pgtype.Numeric) (*string, error) {
	if !n.Valid {
		return nil, nil
	}
	d, err := decimalOf(n, 2)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// readReceivables reads every receivable of the documents ids with q, the
// export's transaction.
func readReceivables(ctx context.Context, q *store.Queries, ids []int64) (exportedReceivables, error) {
	r := exportedReceivables{
		lines: map[int64]exportedBankLine{}, chargePayments: map[int64][]exportedChargePayment{},
		waivers: map[int64][]exportedChargeWaiver{}, manualDeliveries: map[int64][]exportedManualDelivery{},
		reminders: map[int64][]exportedReminder{}, holds: map[int64][]exportedHold{}, handoffs: map[int64][]exportedHandoff{},
	}
	lines, err := q.BankLinesOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, l := range lines {
		r.lines[l.ID] = exportedBankLine{
			BookedOn: orEmpty(dateText(l.BookedOn)), DebtorName: l.DebtorName, DebtorAccount: l.DebtorAccount, Text: l.RemittanceText,
		}
	}
	chargePayments, err := q.ChargePaymentsOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, c := range chargePayments {
		amount, err := decimalOf(c.Amount, 2)
		if err != nil {
			return r, err
		}
		r.chargePayments[c.InvoiceID] = append(r.chargePayments[c.InvoiceID], exportedChargePayment{
			PaidOn: orEmpty(dateText(c.PaidOn)), Amount: amount, Currency: c.Currency, Source: c.Source, Reference: c.Reference,
			Note: c.Note, RegisteredAt: c.RegisteredAt.UTC(), RemovedAt: utcOf(c.RemovedAt), RemovalReason: orEmpty(c.RemovalReason),
			BankLine: r.lineOf(c.BankTransactionID),
		})
	}
	letters, err := q.RemindersOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	sequenceOf := map[int64]int16{}
	for _, l := range letters {
		sequenceOf[l.ID] = l.Sequence
		e := exportedReminder{
			Sequence: l.Sequence, Level: l.Level, AnnouncesCollection: l.AnnouncesCollection, Channel: l.Channel,
			Recipient: l.Recipient, Language: l.Language, Status: l.Status, CreatedAt: l.CreatedAt.UTC(),
			SentOn: dateText(l.SentOn), Deadline: dateText(l.Deadline), Regime: orEmpty(l.Regime), FeeKind: orEmpty(l.FeeKind),
			InterestFrom: dateText(l.InterestFrom), SentAt: utcOf(l.SentAt), FailedAt: utcOf(l.FailedAt),
			WithdrawnAt: utcOf(l.WithdrawnAt), WithdrawalReason: orEmpty(l.WithdrawalReason),
		}
		for _, c := range []struct {
			dst **string
			n   pgtype.Numeric
		}{
			{&e.PrincipalOpen, l.PrincipalOpen}, {&e.Credited, l.Credited}, {&e.Fee, l.Fee}, {&e.Compensation, l.Compensation},
			{&e.ChargesEarlier, l.ChargesEarlier}, {&e.Interest, l.Interest}, {&e.InterestWaived, l.InterestWaived},
			{&e.InterestPaid, l.InterestPaid}, {&e.Total, l.Total},
		} {
			if *c.dst, err = decimalOrNil(c.n); err != nil {
				return r, err
			}
		}
		r.reminders[l.InvoiceID] = append(r.reminders[l.InvoiceID], e)
	}
	waivers, err := q.WaiversOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, w := range waivers {
		amount, err := decimalOf(w.Amount, 2)
		if err != nil {
			return r, err
		}
		r.waivers[w.InvoiceID] = append(r.waivers[w.InvoiceID], exportedChargeWaiver{
			ReminderSequence: sequenceOf[w.ReminderID], Kind: w.Kind, Amount: amount, InterestThrough: dateText(w.InterestThrough),
			Reason: w.Reason, Note: w.Note, WaivedAt: w.WaivedAt.UTC(),
		})
	}
	deliveries, err := q.ManualDeliveriesOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, d := range deliveries {
		r.manualDeliveries[d.InvoiceID] = append(r.manualDeliveries[d.InvoiceID], exportedManualDelivery{
			Kind: d.Kind, DeliveredOn: orEmpty(dateText(d.DeliveredOn)), Note: d.Note, RecordedAt: d.RecordedAt.UTC(),
			RemovedAt: utcOf(d.RemovedAt), RemovalReason: orEmpty(d.RemovalReason),
		})
	}
	holds, err := q.HoldsOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, h := range holds {
		r.holds[h.InvoiceID] = append(r.holds[h.InvoiceID], exportedHold{
			Kind: h.Kind, Note: h.Note, PlacedAt: h.PlacedAt.UTC(), LiftedAt: utcOf(h.LiftedAt), LiftNote: orEmpty(h.LiftNote),
			ChargesAllowed: h.ChargesAllowed,
		})
	}
	handoffs, err := q.HandoffsOfDocuments(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, h := range handoffs {
		r.handoffs[h.InvoiceID] = append(r.handoffs[h.InvoiceID], exportedHandoff{
			HandedOn: orEmpty(dateText(h.HandedOn)), Agency: h.Agency, AgencyReference: h.AgencyReference, Note: h.Note,
			CreatedAt: h.CreatedAt.UTC(), WithdrawnOn: dateText(h.WithdrawnOn), WithdrawalReason: orEmpty(h.WithdrawalReason),
		})
	}
	return r, nil
}

// EraseCustomerData anonymises the person in this module (payments and
// delivery design D6), in this order: it locks their documents FOR UPDATE
// newest first (the merge's statement, the issue's order), so a delivery
// insert — whose trigger takes the document FOR SHARE — waits for this
// transaction; writes the erased-customer marker, which that trigger reads
// after its wait and which refuses any later send; blanks every delivery's
// recipient; blanks every payment's note, live and removed; cancels every
// queued EHF transmission of theirs that was never attempted, leased or not
// (EHF and KID design D12); deletes the drafts — their line sources and
// timesheet rows with them, by the cascade (invoices work design D2, D5),
// while an issued document's timesheet stays with it. Then the receivables
// (invoices payments and reminders design D19), in D19's order: every letter
// in flight withdrawn customer_anonymised (withdrawInFlight, each document
// already held) — a printed letter left to the posting's re-judge and one
// being sent left to become sent (plan readings 9, 45), both named in a
// warning, since the report is counts; every letter's recipient blanked, in
// every status (B1); the notes of the charge payments, the waivers, the
// manual deliveries, the holds and their lifts, and the hand-offs blanked;
// the resolution note of every resolved bank line the person's payments or
// charge payments came from, and its events' notes, blanked — the lock
// order's one named exception (D18, plan reading 54: no path locks a
// resolved line before an invoice, and a line that is not resolved is
// skipped, never waited for); and last the customer's reminder policy
// deleted (D7) — staff's decision and note about the person, which no
// retention rule keeps, reported as invoices.customerReminderPolicies. A
// draft is not a salgsdokument, so it has no retention basis and GDPR art.
// 17 applies; an issued document, its buyer snapshot, its payments, its
// letters, charge payments, waivers, deliveries, holds and hand-offs, and
// the bank's lines with their payer data are bookkeeping material kept
// under bokføringsloven § 13 — five years after the end of the financial
// year — and a sent letter is also the claim's documentation and the
// bad-debt relief's evidence (FMVA § 4-7-1); which is why invoices.documents
// reports 0 and the receivables' kinds report the rows whose address or
// note was blanked, or whose letter was withdrawn. A payment's note is staff
// free text about the person, which no retention rule needs:
// invoices.payments reports the notes blanked. A delivery is kept as the
// record of when the claim was handed to the mail server, its address gone.
// A transmission is kept whole — its UBL is the sales document as the PDF
// is, and its receiver is an organisation's id or the snapshot's own —
// except a queued one never attempted: it has sent nothing, so it is
// cancelled rather than sent after the person is gone, and
// invoices.transmissions reports those. A worker holding one stamps its
// marker only on a row still queued, so it finds the row cancelled and makes
// no call. One whose crash marker is set may already be with the provider
// and is left to the worker. One clock read for the whole erase.
// contracts.ErasedData carries no reason;
// docs/src/content/docs/en/reference/invoices.md and the anonymisation table
// in docs/src/content/docs/en/reference/customers.md say it. Run twice, it
// reports zeros and the marker keeps its first time.
func (p customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	now := p.clock()
	q := store.New(tx)
	if err := q.LockCustomerDocuments(ctx, store.LockCustomerDocumentsParams{FromCustomerID: customerID, IntoCustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("invoices: lock customer %d's documents: %w", customerID, err)
	}
	if err := q.MarkCustomerErased(ctx, store.MarkCustomerErasedParams{CustomerID: customerID, ErasedAt: now}); err != nil {
		return nil, fmt.Errorf("invoices: mark customer %d erased: %w", customerID, err)
	}
	blanked, err := q.BlankCustomerDeliveries(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's deliveries: %w", customerID, err)
	}
	notes, err := q.BlankCustomerPaymentNotes(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's payment notes: %w", customerID, err)
	}
	cancelled, err := q.CancelCustomerUnattemptedTransmissions(ctx, store.CancelCustomerUnattemptedTransmissionsParams{
		CustomerID: customerID, Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: cancel customer %d's unattempted transmissions: %w", customerID, err)
	}
	drafts, err := q.DeleteCustomerDrafts(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: erase customer %d's drafts: %w", customerID, err)
	}
	letters, err := p.eraseLetters(ctx, q, customerID, now)
	if err != nil {
		return nil, err
	}
	erased := []contracts.ErasedData{
		{Kind: kindInvoicesDrafts, Count: drafts},
		{Kind: kindInvoicesDocuments, Count: 0},
		{Kind: kindInvoicesPayments, Count: notes},
		{Kind: kindInvoicesDeliveries, Count: blanked},
		{Kind: kindInvoicesTransmissions, Count: cancelled},
		{Kind: kindInvoicesReminders, Count: letters},
	}
	for _, step := range []struct {
		kind  string
		blank func(context.Context, int32) (int64, error)
	}{
		{kindInvoicesChargePayments, q.BlankCustomerChargePaymentNotes},
		{kindInvoicesChargeWaivers, q.BlankCustomerWaiverNotes},
		{kindInvoicesManualDeliveries, q.BlankCustomerManualDeliveryNotes},
		{kindInvoicesInvoiceHolds, q.BlankCustomerHoldNotes},
		{kindInvoicesCollectionHandoffs, q.BlankCustomerHandoffNotes},
	} {
		n, err := step.blank(ctx, customerID)
		if err != nil {
			return nil, fmt.Errorf("invoices: blank customer %d's notes of %s: %w", customerID, step.kind, err)
		}
		erased = append(erased, contracts.ErasedData{Kind: step.kind, Count: n})
	}
	lines, err := q.BlankCustomerBankLineNotes(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's bank lines' notes: %w", customerID, err)
	}
	events, err := q.BlankCustomerBankEventNotes(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's bank lines' event notes: %w", customerID, err)
	}
	touched := slices.Compact(slices.Sorted(slices.Values(append(lines, events...))))
	policies, err := q.DeletePolicy(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: delete customer %d's reminder policy: %w", customerID, err)
	}
	return append(erased,
		contracts.ErasedData{Kind: kindInvoicesBankTransactions, Count: int64(len(touched))},
		contracts.ErasedData{Kind: kindInvoicesReminderPolicies, Count: policies},
	), nil
}

// eraseLetters is the erase's letters (D19), its documents already held FOR
// UPDATE: each document's letters in flight withdrawn customer_anonymised by
// withdrawInFlight — the hold's and the hand-off's own write — then every
// letter's recipient blanked; the printed letters and those being sent that
// withdrawInFlight left are named at warn, so a person can pull them from
// the post or know one went. It answers how many letters it changed.
func (p customerPersonalData) eraseLetters(ctx context.Context, q *store.Queries, customerID int32, now time.Time) (int64, error) {
	invoiceIDs, err := q.CustomerLetterInvoices(ctx, customerID)
	if err != nil {
		return 0, fmt.Errorf("invoices: read customer %d's documents with letters: %w", customerID, err)
	}
	var changed, left []int64
	for _, id := range invoiceIDs {
		withdrawn, kept, err := withdrawInFlight(ctx, q, id, withdrawnAnonymised, now)
		if err != nil {
			return 0, err
		}
		changed = append(changed, withdrawn...)
		for _, l := range kept {
			left = append(left, l.ID)
		}
	}
	blanked, err := q.BlankCustomerLetterRecipients(ctx, customerID)
	if err != nil {
		return 0, fmt.Errorf("invoices: blank customer %d's letters' recipients: %w", customerID, err)
	}
	if len(left) > 0 {
		slices.Sort(left)
		p.logger.WarnContext(ctx, "invoices: the anonymisation left letters printed for the posting or being sent; "+
			"pull a printed one from the post and withdraw it by hand", "customerId", customerID, "reminderIds", left)
	}
	return int64(len(slices.Compact(slices.Sorted(slices.Values(append(changed, blanked...)))))), nil
}
