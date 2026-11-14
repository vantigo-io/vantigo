package saft

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupportedVersion is a file of a version the reader does not take:
// 1.20 or older, or one it cannot name.
var ErrUnsupportedVersion = errors.New("saft: unsupported version")

// InvalidError is a file that is not a SAF-T Financial file the reader can
// read: not XML, not the schema's root, a DOCTYPE or an entity, or a value
// of the wrong shape.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "saft: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}

// File is what an import takes from a SAF-T file: the header, the master
// files with their balances, and how many transactions it holds — never the
// transactions themselves.
type File struct {
	Version        string
	CompanyName    string
	CompanyNumber  string
	SelectionStart time.Time
	SelectionEnd   time.Time
	// SelectionByPeriod is true when the file named its selection by
	// periods, which the reader takes as calendar months.
	SelectionByPeriod bool
	Accounts          []FileAccount
	Customers         []FileParty
	Suppliers         []FileParty
	TaxCodes          []FileTaxCode
	Entries           int64
}

// FileAccount is an account with its signed balances, debit positive.
type FileAccount struct {
	ID, Description                string
	StandardAccountID              string
	GroupingCategory, GroupingCode string
	Opening, Closing               string
}

// FileBalance is a party's balance on one account.
type FileBalance struct {
	AccountID        string
	Opening, Closing string
}

// FileParty is a customer or a supplier.
type FileParty struct {
	ID, Name, RegistrationNumber string
	Street, Number, City         string
	PostalCode, Country          string
	Email                        string
	BankAccount, IBAN, BIC       string
	Balances                     []FileBalance
}

// FileTaxCode is one TaxCodeDetails.
type FileTaxCode struct {
	Code, Description, Percentage, StandardCode string
}

type xmlBalances struct {
	OpeningDebitBalance  string `xml:"OpeningDebitBalance"`
	OpeningCreditBalance string `xml:"OpeningCreditBalance"`
	ClosingDebitBalance  string `xml:"ClosingDebitBalance"`
	ClosingCreditBalance string `xml:"ClosingCreditBalance"`
}

type xmlAccount struct {
	AccountID          string `xml:"AccountID"`
	AccountDescription string `xml:"AccountDescription"`
	StandardAccountID  string `xml:"StandardAccountID"`
	GroupingCategory   string `xml:"GroupingCategory"`
	GroupingCode       string `xml:"GroupingCode"`
	xmlBalances
}

type xmlAddress struct {
	StreetName string `xml:"StreetName"`
	Number     string `xml:"Number"`
	City       string `xml:"City"`
	PostalCode string `xml:"PostalCode"`
	Country    string `xml:"Country"`
}

type xmlParty struct {
	RegistrationNumber string       `xml:"RegistrationNumber"`
	Name               string       `xml:"Name"`
	Address            []xmlAddress `xml:"Address"`
	Contact            []struct {
		Email string `xml:"Email"`
	} `xml:"Contact"`
	BankAccount []struct {
		IBANNumber        string `xml:"IBANNumber"`
		BankAccountNumber string `xml:"BankAccountNumber"`
		BIC               string `xml:"BIC"`
	} `xml:"BankAccount"`
	CustomerID     string `xml:"CustomerID"`
	SupplierID     string `xml:"SupplierID"`
	BalanceAccount []struct {
		AccountID string `xml:"AccountID"`
		xmlBalances
	} `xml:"BalanceAccount"`
}

type xmlTaxTable struct {
	Entries []struct {
		Details []struct {
			TaxCode         string `xml:"TaxCode"`
			Description     string `xml:"Description"`
			TaxPercentage   string `xml:"TaxPercentage"`
			StandardTaxCode string `xml:"StandardTaxCode"`
		} `xml:"TaxCodeDetails"`
	} `xml:"TaxTableEntry"`
}

type xmlHeader struct {
	AuditFileVersion string `xml:"AuditFileVersion"`
	Company          struct {
		RegistrationNumber string `xml:"RegistrationNumber"`
		Name               string `xml:"Name"`
	} `xml:"Company"`
	SelectionCriteria struct {
		SelectionStartDate string `xml:"SelectionStartDate"`
		SelectionEndDate   string `xml:"SelectionEndDate"`
		PeriodStart        string `xml:"PeriodStart"`
		PeriodStartYear    string `xml:"PeriodStartYear"`
		PeriodEnd          string `xml:"PeriodEnd"`
		PeriodEndYear      string `xml:"PeriodEndYear"`
	} `xml:"SelectionCriteria"`
}

// Read reads a SAF-T Financial 1.30 or 1.40 file as a stream. It refuses a
// DOCTYPE — and with it every entity declaration and external reference, so
// no entity is ever expanded (XXE, the billion laughs) — and any entity
// reference beyond XML's five; the transactions are counted and skipped.
func Read(r io.Reader) (*File, error) {
	d := xml.NewDecoder(r)
	d.Strict = true
	d.Entity = nil
	d.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
			return in, nil
		}
		return nil, invalid("the file is %s; SAF-T is UTF-8", charset)
	}
	f := &File{}
	var path []string
	sawRoot, sawHeader := false, false
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var inv *InvalidError
			if errors.As(err, &inv) {
				return nil, err
			}
			return nil, invalid("not readable XML: %v", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, invalid("a DOCTYPE or other declaration is not allowed")
		case xml.StartElement:
			name := t.Name.Local
			if len(path) == 0 {
				if name != "AuditFile" || t.Name.Space != Namespace {
					return nil, invalid("not a SAF-T Financial file: the root is %s", name)
				}
				sawRoot = true
				path = append(path, name)
				continue
			}
			parent := path[len(path)-1]
			switch {
			case parent == "AuditFile" && name == "Header":
				var h xmlHeader
				if err := d.DecodeElement(&h, &t); err != nil {
					return nil, invalid("the Header cannot be read: %v", err)
				}
				if err := f.header(h); err != nil {
					return nil, err
				}
				sawHeader = true
				continue
			case parent == "AuditFile" && (name == "MasterFiles" || name == "GeneralLedgerEntries"):
				if !sawHeader {
					return nil, invalid("the Header comes first")
				}
				path = append(path, name)
				continue
			case parent == "MasterFiles" && (name == "GeneralLedgerAccounts" || name == "Customers" || name == "Suppliers"):
				path = append(path, name)
				continue
			case parent == "GeneralLedgerAccounts" && name == "Account":
				var a xmlAccount
				if err := d.DecodeElement(&a, &t); err != nil {
					return nil, invalid("an Account cannot be read: %v", err)
				}
				acc, err := account(a)
				if err != nil {
					return nil, err
				}
				f.Accounts = append(f.Accounts, acc)
				continue
			case (parent == "Customers" && name == "Customer") || (parent == "Suppliers" && name == "Supplier"):
				var p xmlParty
				if err := d.DecodeElement(&p, &t); err != nil {
					return nil, invalid("a %s cannot be read: %v", name, err)
				}
				party, err := partyOf(p, name == "Customer")
				if err != nil {
					return nil, err
				}
				if name == "Customer" {
					f.Customers = append(f.Customers, party)
				} else {
					f.Suppliers = append(f.Suppliers, party)
				}
				continue
			case parent == "MasterFiles" && name == "TaxTable":
				var tt xmlTaxTable
				if err := d.DecodeElement(&tt, &t); err != nil {
					return nil, invalid("the TaxTable cannot be read: %v", err)
				}
				for _, e := range tt.Entries {
					for _, c := range e.Details {
						f.TaxCodes = append(f.TaxCodes, FileTaxCode{Code: strings.TrimSpace(c.TaxCode),
							Description: strings.TrimSpace(c.Description), Percentage: strings.TrimSpace(c.TaxPercentage),
							StandardCode: strings.TrimSpace(c.StandardTaxCode)})
					}
				}
				continue
			case parent == "GeneralLedgerEntries" && name == "NumberOfEntries":
				var n string
				if err := d.DecodeElement(&n, &t); err != nil {
					return nil, invalid("NumberOfEntries cannot be read: %v", err)
				}
				f.Entries, _ = strconv.ParseInt(strings.TrimSpace(n), 10, 64)
				continue
			}
			// Anything else is read past, its contents unread.
			if err := d.Skip(); err != nil {
				return nil, invalid("not readable XML: %v", err)
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		}
	}
	if !sawRoot || !sawHeader {
		return nil, invalid("not a SAF-T Financial file: no Header")
	}
	return f, nil
}

func (f *File) header(h xmlHeader) error {
	v := strings.TrimSpace(h.AuditFileVersion)
	switch v {
	case "1.30", "1.3":
		f.Version = "1.30"
	case "1.40", "1.4":
		f.Version = "1.40"
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedVersion, v)
	}
	f.CompanyName = strings.TrimSpace(h.Company.Name)
	f.CompanyNumber = strings.TrimSpace(h.Company.RegistrationNumber)
	sc := h.SelectionCriteria
	if sc.SelectionStartDate != "" || sc.SelectionEndDate != "" {
		start, err1 := parseDate(sc.SelectionStartDate)
		end, err2 := parseDate(sc.SelectionEndDate)
		if err1 != nil || err2 != nil || end.Before(start) {
			return invalid("the SelectionCriteria's dates cannot be read")
		}
		f.SelectionStart, f.SelectionEnd = start, end
		return nil
	}
	ps, e1 := strconv.Atoi(strings.TrimSpace(sc.PeriodStart))
	py, e2 := strconv.Atoi(strings.TrimSpace(sc.PeriodStartYear))
	pe, e3 := strconv.Atoi(strings.TrimSpace(sc.PeriodEnd))
	ey, e4 := strconv.Atoi(strings.TrimSpace(sc.PeriodEndYear))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || py < 1970 || ey > 2100 {
		return invalid("the SelectionCriteria name no dates and no periods")
	}
	ps, pe = max(1, min(ps, 12)), max(1, min(pe, 12))
	f.SelectionStart = time.Date(py, time.Month(ps), 1, 0, 0, 0, 0, time.UTC)
	f.SelectionEnd = time.Date(ey, time.Month(pe)+1, 0, 0, 0, 0, 0, time.UTC)
	f.SelectionByPeriod = true
	if f.SelectionEnd.Before(f.SelectionStart) {
		return invalid("the SelectionCriteria end before they start")
	}
	return nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10] // a time zone after the date
	}
	return time.Parse(time.DateOnly, s)
}

// Amount is a file's decimal text as an exact amount: a point or a comma,
// no thousands separator; "" is zero.
func Amount(s string) (*big.Rat, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return new(big.Rat), true
	}
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}

// signed is a debit and a credit element as one signed amount's text.
func signed(debit, credit string) (string, error) {
	d, ok1 := Amount(debit)
	c, ok2 := Amount(credit)
	if !ok1 || !ok2 {
		return "", invalid("an amount cannot be read: %q %q", debit, credit)
	}
	return new(big.Rat).Sub(d, c).FloatString(2), nil
}

func account(a xmlAccount) (FileAccount, error) {
	out := FileAccount{ID: strings.TrimSpace(a.AccountID), Description: strings.TrimSpace(a.AccountDescription),
		StandardAccountID: strings.TrimSpace(a.StandardAccountID), GroupingCategory: strings.TrimSpace(a.GroupingCategory),
		GroupingCode: strings.TrimSpace(a.GroupingCode)}
	if out.ID == "" {
		return out, invalid("an Account has no AccountID")
	}
	var err error
	if out.Opening, err = signed(a.OpeningDebitBalance, a.OpeningCreditBalance); err != nil {
		return out, err
	}
	if out.Closing, err = signed(a.ClosingDebitBalance, a.ClosingCreditBalance); err != nil {
		return out, err
	}
	return out, nil
}

func partyOf(p xmlParty, customer bool) (FileParty, error) {
	out := FileParty{Name: strings.TrimSpace(p.Name), RegistrationNumber: strings.TrimSpace(p.RegistrationNumber)}
	if customer {
		out.ID = strings.TrimSpace(p.CustomerID)
	} else {
		out.ID = strings.TrimSpace(p.SupplierID)
	}
	if out.ID == "" {
		return out, invalid("a party has no id")
	}
	if len(p.Address) > 0 {
		a := p.Address[0]
		out.Street, out.Number, out.City = strings.TrimSpace(a.StreetName), strings.TrimSpace(a.Number), strings.TrimSpace(a.City)
		out.PostalCode, out.Country = strings.TrimSpace(a.PostalCode), strings.TrimSpace(a.Country)
	}
	for _, c := range p.Contact {
		if out.Email == "" {
			out.Email = strings.TrimSpace(c.Email)
		}
	}
	if len(p.BankAccount) > 0 {
		b := p.BankAccount[0]
		out.BankAccount, out.IBAN, out.BIC = strings.TrimSpace(b.BankAccountNumber), strings.TrimSpace(b.IBANNumber), strings.TrimSpace(b.BIC)
	}
	for _, b := range p.BalanceAccount {
		o, err := signed(b.OpeningDebitBalance, b.OpeningCreditBalance)
		if err != nil {
			return out, err
		}
		c, err := signed(b.ClosingDebitBalance, b.ClosingCreditBalance)
		if err != nil {
			return out, err
		}
		out.Balances = append(out.Balances, FileBalance{AccountID: strings.TrimSpace(b.AccountID), Opening: o, Closing: c})
	}
	return out, nil
}
