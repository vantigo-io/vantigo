// Package saft writes and reads Skatteetaten's SAF-T Financial file
// (Norwegian SAF-T Financial data, schema 1.40 for the writer, 1.30 and 1.40
// for the reader). It knows nothing of the ledger: the accounting module
// hands the writer the header, the master files and one transaction at a
// time, and the writer streams the document to an io.Writer as it goes: the
// transactions are never held, the master files only while they are written. The rules the XSD does not
// express are Skatteetaten's technical description's (v1.7, 31 August 2026):
// no signs on amounts — debit and credit are separate elements; "NA" for a
// mandatory string with no value; UTF-8.
package saft

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

// Namespace is the Norwegian SAF-T Financial namespace, the same for 1.30
// and 1.40.
const Namespace = "urn:StandardAuditFile-Taxation-Financial:NO"

// Version is the schema version the writer writes.
const Version = "1.40"

// The schema's text lengths (SAFshorttextType and its siblings).
const (
	codeLen   = 9
	shortLen  = 18
	middle1   = 35
	middle2   = 70
	longLen   = 256
	notInData = "NA"
)

// Address is an AddressStructure.
type Address struct {
	Street, Number, Additional, City, PostalCode, Country string
	// Type is StreetAddress, PostalAddress … or "".
	Type string
}

// Company is the header's company or a party's own details.
type Company struct {
	RegistrationNumber, Name string
	Address                  *Address
	Telephone, Email         string
	// VATNumber is the company's VAT (MVA) registration, "" when it has none.
	VATNumber              string
	BankAccount, IBAN, BIC string
	BankLedgerAccount      string
}

// Header is the file's Header.
type Header struct {
	Created                                      time.Time
	SoftwareCompany, SoftwareID, SoftwareVersion string
	Company                                      Company
	// From and To are the selection's first and last days, written as
	// SelectionStartDate and SelectionEndDate: unlike periods, dates say the
	// same thing whatever month a fiscal year starts in.
	From, To time.Time
	Comment  string
	UserID   string
}

// Account is a general ledger account with its balances, debit positive.
type Account struct {
	ID, Description                string
	GroupingCategory, GroupingCode string
	Created                        *time.Time
	Opening, Closing               *big.Rat
}

// BalanceAccount is a party's balance on one control account.
type BalanceAccount struct {
	AccountID        string
	Opening, Closing *big.Rat
}

// Party is a customer or a supplier.
type Party struct {
	ID string
	Company
	Balances    []BalanceAccount
	PaymentDays *int
	// Type is Private or Company; Active false writes Status Passive.
	Type   string
	Active bool
}

// TaxCode is one TaxCodeDetails of the TaxTable.
type TaxCode struct {
	Code, Description string
	// Percentage is the rate's decimal text, "" for a code with none.
	Percentage   string
	Effective    *time.Time
	StandardCode string
	// BaseRate is the deductible share, "100" or "0".
	BaseRate string
}

// AnalysisEntry is one AnalysisTypeTableEntry.
type AnalysisEntry struct {
	Type, TypeDescription, ID, Description string
	Status                                 string
}

// Journal is one Journal's header.
type Journal struct {
	ID, Description, Type string
}

// Transaction is one voucher.
type Transaction struct {
	ID                              string
	Period, PeriodYear              int
	Date                            time.Time
	VoucherType, VoucherDescription string
	Description                     string
	SystemEntry, GLPosting          time.Time
	SystemID                        string
	Lines                           []Line
}

// Line is one posting; Amount is signed, debit positive.
type Line struct {
	RecordID, AccountID string
	// AnalysisType and AnalysisID name the line's project; "" for none.
	AnalysisType, AnalysisID string
	SourceDocumentID         string
	CustomerID, SupplierID   string
	Description              string
	Amount                   *big.Rat
	Currency                 string
	CurrencyAmount           *big.Rat
	ExchangeRate             string
	Tax                      *TaxInformation
	ReferenceNumber          string
	DueDate                  *time.Time
}

// TaxInformation is a line's tax: its code and rate, the base it was
// computed from, and its amount, signed, debit positive.
type TaxInformation struct {
	Code, Percentage string
	Base             *big.Rat
	Amount           *big.Rat
	Currency         string
	CurrencyAmount   *big.Rat
}

// Writer streams one SAF-T Financial document.
type Writer struct {
	w   *bufio.Writer
	err error
	// lines and the running sums, for the caller's checks.
	debit, credit *big.Rat
	entries       int64
}

// NewWriter writes to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: bufio.NewWriterSize(w, 64<<10), debit: new(big.Rat), credit: new(big.Rat)}
}

// Err is the first error the writer met.
func (w *Writer) Err() error { return w.err }

// Written is the transactions written and the sums of their debits and
// credits, which the caller holds to the totals it wrote first.
func (w *Writer) Written() (int64, *big.Rat, *big.Rat) {
	return w.entries, new(big.Rat).Set(w.debit), new(big.Rat).Set(w.credit)
}

func (w *Writer) raw(s string) {
	if w.err == nil {
		_, w.err = w.w.WriteString(s)
	}
}

// clip is s cut to max characters, "NA" when empty and mandatory.
func clip(s string, max int, mandatory bool) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	if s == "" && mandatory {
		return notInData
	}
	return s
}

func (w *Writer) open(name string)  { w.raw("<" + name + ">") }
func (w *Writer) close(name string) { w.raw("</" + name + ">\n") }

// text writes <name>value</name>, value clipped to max; an empty optional
// value writes nothing (the technical description: no empty elements).
func (w *Writer) text(name, value string, max int, mandatory bool) {
	value = clip(value, max, mandatory)
	if value == "" {
		return
	}
	w.raw("<" + name + ">")
	if w.err == nil {
		w.err = xml.EscapeText(w.w, []byte(value))
	}
	w.raw("</" + name + ">")
}

func (w *Writer) date(name string, t time.Time) {
	w.raw("<" + name + ">" + t.Format(time.DateOnly) + "</" + name + ">")
}

func (w *Writer) number(name string, n int) { w.raw(fmt.Sprintf("<%s>%d</%s>", name, n, name)) }

// money is |r| at two decimals.
func money(r *big.Rat) string {
	if r == nil {
		return "0.00"
	}
	return new(big.Rat).Abs(r).FloatString(2)
}

func (w *Writer) amount(name string, r *big.Rat) {
	w.raw("<" + name + ">" + money(r) + "</" + name + ">")
}

// balance writes the choice of a debit or a credit balance: a debit (or zero)
// as <prefix>DebitBalance, a credit as <prefix>CreditBalance.
func (w *Writer) balance(prefix string, r *big.Rat) {
	if r != nil && r.Sign() < 0 {
		w.amount(prefix+"CreditBalance", r)
		return
	}
	w.amount(prefix+"DebitBalance", r)
}

func (w *Writer) address(a *Address) {
	if a == nil {
		return
	}
	w.open("Address")
	w.text("StreetName", a.Street, longLen, false)
	w.text("Number", a.Number, middle2, false)
	w.text("AdditionalAddressDetail", a.Additional, longLen, false)
	w.text("City", a.City, longLen, false)
	w.text("PostalCode", a.PostalCode, middle2, false)
	if len(a.Country) == 2 {
		w.text("Country", strings.ToUpper(a.Country), 2, false)
	}
	w.text("AddressType", a.Type, 20, false)
	w.close("Address")
}

func (w *Writer) bank(c Company) {
	if c.IBAN == "" && c.BankAccount == "" {
		return
	}
	w.open("BankAccount")
	if c.BankAccount != "" {
		w.text("BankAccountNumber", c.BankAccount, middle1, true)
	} else {
		w.text("IBANNumber", c.IBAN, middle1, true)
	}
	w.text("BIC", c.BIC, shortLen, false)
	w.text("GeneralLedgerAccountID", c.BankLedgerAccount, middle2, false)
	w.close("BankAccount")
}

// company writes a CompanyStructure's elements, or with header the
// CompanyHeaderStructure's, whose RegistrationNumber and Contact are
// mandatory.
func (w *Writer) company(c Company, header bool) {
	w.text("RegistrationNumber", c.RegistrationNumber, middle1, header)
	w.text("Name", c.Name, longLen, true)
	w.address(c.Address)
	if header || c.Telephone != "" || c.Email != "" {
		w.open("Contact")
		// No contact person is kept: the technical description's empty
		// value for a mandatory string.
		w.raw("<ContactPerson><FirstName>NA</FirstName><LastName>NA</LastName></ContactPerson>")
		w.text("Telephone", c.Telephone, shortLen, false)
		w.text("Email", c.Email, middle2, false)
		w.close("Contact")
	}
	if c.VATNumber != "" {
		w.open("TaxRegistration")
		w.text("TaxRegistrationNumber", c.VATNumber, middle1, true)
		w.text("TaxAuthority", "Skatteetaten", middle1, true)
		w.close("TaxRegistration")
	}
	w.bank(c)
}

// Start writes the declaration, the root and the Header.
func (w *Writer) Start(h Header) {
	w.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	w.raw(`<AuditFile xmlns="` + Namespace + `">` + "\n")
	w.open("Header")
	w.text("AuditFileVersion", Version, codeLen, true)
	w.text("AuditFileCountry", "NO", 2, true)
	w.date("AuditFileDateCreated", h.Created)
	w.text("SoftwareCompanyName", h.SoftwareCompany, middle2, true)
	w.text("SoftwareID", h.SoftwareID, longLen, true)
	w.text("SoftwareVersion", h.SoftwareVersion, shortLen, true)
	w.open("Company")
	w.company(h.Company, true)
	w.close("Company")
	w.text("DefaultCurrencyCode", "NOK", 3, true)
	w.open("SelectionCriteria")
	w.date("SelectionStartDate", h.From)
	w.date("SelectionEndDate", h.To)
	w.close("SelectionCriteria")
	w.text("HeaderComment", h.Comment, longLen, false)
	w.text("TaxAccountingBasis", "A", shortLen, true)
	w.text("UserID", h.UserID, longLen, false)
	w.close("Header")
}

// MasterFiles writes the accounts, the customers, the suppliers, the tax
// table and the analysis types, each section only when it has an entry (a
// heading must hold one).
func (w *Writer) MasterFiles(accounts []Account, customers, suppliers []Party, taxes []TaxCode, analysis []AnalysisEntry) {
	w.open("MasterFiles")
	if len(accounts) > 0 {
		w.open("GeneralLedgerAccounts")
		for _, a := range accounts {
			w.open("Account")
			w.text("AccountID", a.ID, middle2, true)
			w.text("AccountDescription", a.Description, longLen, true)
			w.text("GroupingCategory", a.GroupingCategory, longLen, true)
			w.text("GroupingCode", a.GroupingCode, middle1, true)
			w.text("AccountType", "GL", shortLen, true)
			if a.Created != nil {
				w.date("AccountCreationDate", *a.Created)
			}
			w.balance("Opening", a.Opening)
			w.balance("Closing", a.Closing)
			w.close("Account")
		}
		w.close("GeneralLedgerAccounts")
	}
	w.parties("Customers", "Customer", "CustomerID", customers)
	w.parties("Suppliers", "Supplier", "SupplierID", suppliers)
	if len(taxes) > 0 {
		w.open("TaxTable")
		w.open("TaxTableEntry")
		w.text("TaxType", "MVA", codeLen, true)
		w.text("Description", "Merverdiavgift", longLen, true)
		for _, t := range taxes {
			w.open("TaxCodeDetails")
			w.text("TaxCode", t.Code, middle2, true)
			if t.Effective != nil {
				w.date("EffectiveDate", *t.Effective)
			}
			w.text("Description", t.Description, longLen, false)
			if t.Percentage != "" {
				w.raw("<TaxPercentage>" + t.Percentage + "</TaxPercentage>")
			}
			w.text("Country", "NO", 2, true)
			w.text("StandardTaxCode", t.StandardCode, 10, true)
			base := t.BaseRate
			if base == "" {
				base = "100"
			}
			w.raw("<BaseRate>" + base + "</BaseRate>")
			w.close("TaxCodeDetails")
		}
		w.close("TaxTableEntry")
		w.close("TaxTable")
	}
	if len(analysis) > 0 {
		w.open("AnalysisTypeTable")
		for _, a := range analysis {
			w.open("AnalysisTypeTableEntry")
			w.text("AnalysisType", a.Type, codeLen, true)
			w.text("AnalysisTypeDescription", a.TypeDescription, longLen, true)
			w.text("AnalysisID", a.ID, middle1, true)
			w.text("AnalysisIDDescription", a.Description, longLen, true)
			w.text("Status", a.Status, middle1, false)
			w.close("AnalysisTypeTableEntry")
		}
		w.close("AnalysisTypeTable")
	}
	w.close("MasterFiles")
}

func (w *Writer) parties(section, element, idName string, parties []Party) {
	if len(parties) == 0 {
		return
	}
	w.open(section)
	for _, p := range parties {
		w.open(element)
		w.company(p.Company, false)
		w.text(idName, p.ID, middle1, true)
		for _, b := range p.Balances {
			w.open("BalanceAccount")
			w.text("AccountID", b.AccountID, middle2, false)
			w.balance("Opening", b.Opening)
			w.balance("Closing", b.Closing)
			w.close("BalanceAccount")
		}
		w.open("PartyInfo")
		if p.PaymentDays != nil {
			w.raw(fmt.Sprintf("<PaymentTerms><Days>%d</Days></PaymentTerms>", *p.PaymentDays))
		}
		w.text("Type", p.Type, middle1, false)
		status := "Active"
		if !p.Active {
			status = "Passive"
		}
		w.text("Status", status, middle1, true)
		w.close("PartyInfo")
		w.close(element)
	}
	w.close(section)
}

// BeginEntries writes GeneralLedgerEntries' header: the number of
// transactions and the sums of all debits and credits.
func (w *Writer) BeginEntries(entries int64, debit, credit *big.Rat) {
	w.open("GeneralLedgerEntries")
	w.raw(fmt.Sprintf("<NumberOfEntries>%d</NumberOfEntries>", entries))
	w.amount("TotalDebit", debit)
	w.amount("TotalCredit", credit)
	w.raw("\n")
}

// BeginJournal opens a Journal.
func (w *Writer) BeginJournal(j Journal) {
	w.open("Journal")
	w.text("JournalID", j.ID, shortLen, true)
	w.text("Description", j.Description, longLen, true)
	w.text("Type", j.Type, codeLen, true)
	w.raw("\n")
}

// EndJournal closes it.
func (w *Writer) EndJournal() { w.close("Journal") }

// Transaction writes one voucher with its lines.
func (w *Writer) Transaction(t Transaction) {
	w.entries++
	w.open("Transaction")
	w.text("TransactionID", t.ID, middle2, true)
	w.number("Period", t.Period)
	w.number("PeriodYear", t.PeriodYear)
	w.date("TransactionDate", t.Date)
	w.text("VoucherType", t.VoucherType, middle2, false)
	w.text("VoucherDescription", t.VoucherDescription, middle2, false)
	w.text("Description", t.Description, longLen, true)
	w.date("SystemEntryDate", t.SystemEntry)
	w.date("GLPostingDate", t.GLPosting)
	w.text("SystemID", t.SystemID, shortLen, false)
	for _, l := range t.Lines {
		w.line(l)
	}
	w.close("Transaction")
}

// amountStructure writes an AmountStructure: the NOK amount, and the
// currency's when the posting has one.
func (w *Writer) amountStructure(name string, amount *big.Rat, currency string, currencyAmount *big.Rat, rate string) {
	w.open(name)
	w.amount("Amount", amount)
	if currency != "" && currencyAmount != nil {
		w.text("CurrencyCode", currency, 3, true)
		w.raw("<CurrencyAmount>" + money(currencyAmount) + "</CurrencyAmount>")
		if rate != "" {
			w.raw("<ExchangeRate>" + rate + "</ExchangeRate>")
		}
	}
	w.close(name)
}

func (w *Writer) line(l Line) {
	if l.Amount.Sign() > 0 {
		w.debit.Add(w.debit, l.Amount)
	} else {
		w.credit.Sub(w.credit, l.Amount)
	}
	debit := l.Amount.Sign() > 0
	w.open("Line")
	w.text("RecordID", l.RecordID, shortLen, true)
	w.text("AccountID", l.AccountID, middle2, true)
	if l.AnalysisType != "" {
		w.open("Analysis")
		w.text("AnalysisType", l.AnalysisType, codeLen, true)
		w.text("AnalysisID", l.AnalysisID, longLen, true)
		if debit {
			w.amountStructure("DebitAnalysisAmount", l.Amount, "", nil, "")
		} else {
			w.amountStructure("CreditAnalysisAmount", l.Amount, "", nil, "")
		}
		w.close("Analysis")
	}
	w.text("SourceDocumentID", l.SourceDocumentID, middle1, false)
	w.text("CustomerID", l.CustomerID, middle1, false)
	w.text("SupplierID", l.SupplierID, middle1, false)
	w.text("Description", l.Description, longLen, true)
	if debit {
		w.amountStructure("DebitAmount", l.Amount, l.Currency, l.CurrencyAmount, l.ExchangeRate)
	} else {
		w.amountStructure("CreditAmount", l.Amount, l.Currency, l.CurrencyAmount, l.ExchangeRate)
	}
	if t := l.Tax; t != nil {
		w.open("TaxInformation")
		w.text("TaxType", "MVA", codeLen, true)
		w.text("TaxCode", t.Code, middle2, true)
		if t.Percentage != "" {
			w.raw("<TaxPercentage>" + t.Percentage + "</TaxPercentage>")
		}
		w.text("Country", "NO", 2, true)
		if t.Base != nil {
			w.raw("<TaxBase>" + money(t.Base) + "</TaxBase>")
		}
		// A zero tax amount (a base line's) takes the line's side.
		if t.Amount.Sign() < 0 || (t.Amount.Sign() == 0 && !debit) {
			w.amountStructure("CreditTaxAmount", t.Amount, t.Currency, t.CurrencyAmount, "")
		} else {
			w.amountStructure("DebitTaxAmount", t.Amount, t.Currency, t.CurrencyAmount, "")
		}
		w.close("TaxInformation")
	}
	w.text("ReferenceNumber", l.ReferenceNumber, middle1, false)
	if l.DueDate != nil {
		w.date("DueDate", *l.DueDate)
	}
	w.close("Line")
}

// End closes GeneralLedgerEntries when begun, then the document, and flushes.
func (w *Writer) End(entriesBegun bool) error {
	if entriesBegun {
		w.close("GeneralLedgerEntries")
	}
	w.raw("</AuditFile>\n")
	if w.err == nil {
		w.err = w.w.Flush()
	}
	return w.err
}

// FileName is the name Skatteetaten recommends:
// SAF-T Financial_<orgnr>_<yyyymmddhhmmss>_<part>_<parts>.xml.
func FileName(organisationNumber string, at time.Time, part, parts int) string {
	return fmt.Sprintf("SAF-T Financial_%s_%s_%d_%d.xml", organisationNumber, at.Format("20060102150405"), part, parts)
}
