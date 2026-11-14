package bankstmt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
)

// camt.053 (ISO 20022 BankToCustomerStatement). Norwegian banks send
// camt.053.001.02 and the newer versions; every version from .02 to .13 is
// read, by the elements the versions share: a statement's account, period
// and balances, and its entries — booked only, each with its transactions'
// details when the bank gives them, so a KID batch keeps its payments.
//
// The file is read as a stream of tokens: a DOCTYPE, and with it every
// entity declaration and external reference, is refused before anything is
// read; each statement is decoded alone; the bytes, the statements and the
// lines are bounded.

const camt053Namespace = "urn:iso:std:iso:20022:tech:xsd:camt.053.001."

type camtAmount struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type camtAccountID struct {
	IBAN string `xml:"IBAN"`
	Othr string `xml:"Othr>Id"`
}

type camtDate struct {
	Dt   string `xml:"Dt"`
	DtTm string `xml:"DtTm"`
}

type camtParty struct {
	Nm    string `xml:"Nm"`
	PtyNm string `xml:"Pty>Nm"`
}

func (p camtParty) name() string {
	if p.Nm != "" {
		return p.Nm
	}
	return p.PtyNm
}

type camtPartyAccount struct {
	ID camtAccountID `xml:"Id"`
}

type camtTx struct {
	EndToEndID  string           `xml:"Refs>EndToEndId"`
	AcctSvcrRef string           `xml:"Refs>AcctSvcrRef"`
	TxID        string           `xml:"Refs>TxId"`
	Amt         camtAmount       `xml:"Amt"`
	TxAmt       camtAmount       `xml:"AmtDtls>TxAmt>Amt"`
	CdtDbtInd   string           `xml:"CdtDbtInd"`
	Dbtr        camtParty        `xml:"RltdPties>Dbtr"`
	DbtrAcct    camtPartyAccount `xml:"RltdPties>DbtrAcct"`
	Cdtr        camtParty        `xml:"RltdPties>Cdtr"`
	CdtrAcct    camtPartyAccount `xml:"RltdPties>CdtrAcct"`
	Ustrd       []string         `xml:"RmtInf>Ustrd"`
	CdtrRef     []string         `xml:"RmtInf>Strd>CdtrRefInf>Ref"`
	AddtlTxInf  string           `xml:"AddtlTxInf"`
}

type camtEntry struct {
	NtryRef   string     `xml:"NtryRef"`
	Amt       camtAmount `xml:"Amt"`
	CdtDbtInd string     `xml:"CdtDbtInd"`
	RvslInd   string     `xml:"RvslInd"`
	Sts       struct {
		Text string `xml:",chardata"`
		Cd   string `xml:"Cd"`
	} `xml:"Sts"`
	BookgDt      camtDate `xml:"BookgDt"`
	ValDt        camtDate `xml:"ValDt"`
	AcctSvcrRef  string   `xml:"AcctSvcrRef"`
	Domain       string   `xml:"BkTxCd>Domn>Cd"`
	Family       string   `xml:"BkTxCd>Domn>Fmly>Cd"`
	SubFamily    string   `xml:"BkTxCd>Domn>Fmly>SubFmlyCd"`
	Proprietary  string   `xml:"BkTxCd>Prtry>Cd"`
	AddtlNtryInf string   `xml:"AddtlNtryInf"`
	Details      []struct {
		Txs []camtTx `xml:"TxDtls"`
	} `xml:"NtryDtls"`
}

type camtBalance struct {
	Code      string     `xml:"Tp>CdOrPrtry>Cd"`
	Amt       camtAmount `xml:"Amt"`
	CdtDbtInd string     `xml:"CdtDbtInd"`
	Dt        camtDate   `xml:"Dt"`
}

type camtStatement struct {
	ID      string        `xml:"Id"`
	Acct    camtAccountID `xml:"Acct>Id"`
	AcctCcy string        `xml:"Acct>Ccy"`
	FrDtTm  string        `xml:"FrToDt>FrDtTm"`
	ToDtTm  string        `xml:"FrToDt>ToDtTm"`
	Bal     []camtBalance `xml:"Bal"`
	Ntry    []camtEntry   `xml:"Ntry"`
}

// IsXML reports whether data looks like an XML document: its first byte
// that is not a byte order mark or white space is '<'.
func IsXML(data []byte) bool {
	s := strings.TrimLeft(strings.TrimPrefix(string(data[:min(len(data), 512)]), "\xef\xbb\xbf"), " \t\r\n")
	return strings.HasPrefix(s, "<")
}

// ReadCamt053 reads a camt.053 file into its statements. It refuses, as an
// *InvalidError, a file that is not camt.053 — another camt message names
// itself — one with a DOCTYPE, a statement in a currency other than NOK, an
// amount that is not a decimal with at most two places, or an entry without
// a booking day; and answers ErrTooLarge past lim.
func ReadCamt053(r io.Reader, lim Limits) ([]Statement, error) {
	if lim.Bytes > 0 {
		r = &limitedReader{r: r, left: lim.Bytes}
	}
	dec := xml.NewDecoder(r)
	dec.Strict = true
	dec.CharsetReader = charsetReader
	var out []Statement
	lines := 0
	root := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, ErrTooLarge) {
			return nil, ErrTooLarge
		}
		if err != nil {
			return nil, invalid("not readable XML (" + err.Error() + ")")
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, invalid("a DOCTYPE or other declaration is not allowed")
		case xml.StartElement:
			if !root {
				root = true
				if t.Name.Local != "Document" {
					return nil, invalid("the root element is " + t.Name.Local + ", not a camt.053 Document")
				}
				if !strings.HasPrefix(t.Name.Space, camt053Namespace) {
					if strings.Contains(t.Name.Space, "camt.") {
						return nil, invalid("this is " + strings.TrimPrefix(t.Name.Space, "urn:iso:std:iso:20022:tech:xsd:") +
							", not camt.053: download the account statement (camt.053) from the bank")
					}
					return nil, invalid("the document is not camt.053")
				}
				version := strings.TrimPrefix(t.Name.Space, camt053Namespace)
				if version < "02" || len(version) != 2 {
					return nil, invalid("camt.053.001." + version + " is not a version read here; .02 and newer are")
				}
				continue
			}
			if t.Name.Local != "Stmt" {
				continue
			}
			if lim.Statements > 0 && len(out) >= lim.Statements {
				return nil, ErrTooLarge
			}
			var st camtStatement
			if err := dec.DecodeElement(&st, &t); err != nil {
				if errors.Is(err, ErrTooLarge) {
					return nil, ErrTooLarge
				}
				return nil, invalid("a statement is not readable (" + err.Error() + ")")
			}
			s, err := st.statement(len(out) + 1)
			if err != nil {
				return nil, err
			}
			lines += len(s.Lines)
			if lim.Lines > 0 && lines > lim.Lines {
				return nil, ErrTooLarge
			}
			out = append(out, s)
		}
	}
	if !root {
		return nil, invalid("the file holds no XML document")
	}
	if len(out) == 0 {
		return nil, invalid("the file holds no statement")
	}
	return out, nil
}

// charsetReader reads a file declared ISO-8859-1 (Latin-1) byte for rune;
// UTF-8 needs none, and any other encoding is refused.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "latin-1", "iso8859-1":
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		text, _ := decode(data, "iso-8859-1")
		return strings.NewReader(text), nil
	}
	return nil, invalid("the encoding " + charset + " is not read here; UTF-8 and ISO-8859-1 are")
}

// limitedReader answers ErrTooLarge once more than left bytes are read.
type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, ErrTooLarge
	}
	return n, err
}

// statement is one Stmt as a Statement; n is its position, for a refusal.
func (st camtStatement) statement(n int) (Statement, error) {
	where := fmt.Sprintf("statement %d", n)
	if st.ID != "" {
		where = "statement " + st.ID
	}
	raw := st.Acct.IBAN
	if raw == "" {
		raw = st.Acct.Othr
	}
	out := Statement{Raw: strings.TrimSpace(raw), Ref: clip(st.ID, 140), Currency: strings.TrimSpace(st.AcctCcy)}
	out.Account, _ = NormaliseAccount(raw)
	if out.Currency == "" {
		out.Currency = "NOK"
	}
	if out.Currency != "NOK" {
		return Statement{}, invalid(where + " is in " + out.Currency + "; only NOK accounts are read here")
	}
	for _, b := range st.Bal {
		amount, err := signed(b.Amt, b.CdtDbtInd, where+"'s balance")
		if err != nil {
			return Statement{}, err
		}
		switch b.Code {
		case "OPBD", "PRCD":
			if out.Opening == nil || b.Code == "OPBD" {
				out.Opening = amount
			}
		case "CLBD":
			out.Closing = amount
		}
	}
	if (out.Opening == nil) != (out.Closing == nil) {
		out.Opening, out.Closing = nil, nil
	}
	for i, e := range st.Ntry {
		status := strings.TrimSpace(e.Sts.Cd)
		if status == "" {
			status = strings.TrimSpace(e.Sts.Text)
		}
		if status != "BOOK" {
			out.NotBooked++
			continue
		}
		l, err := e.line(fmt.Sprintf("%s, entry %d", where, i+1))
		if err != nil {
			return Statement{}, err
		}
		out.Lines = append(out.Lines, l)
	}
	// The period is the statement's own; without one, its entries' days.
	from, okFrom := camtDay(st.FrDtTm)
	to, okTo := camtDay(st.ToDtTm)
	givenFrom, givenTo := okFrom, okTo
	for _, l := range out.Lines {
		if !givenFrom && (!okFrom || l.BookedOn.Before(from)) {
			from, okFrom = l.BookedOn, true
		}
		if !givenTo && (!okTo || l.BookedOn.After(to)) {
			to, okTo = l.BookedOn, true
		}
	}
	if !okFrom || !okTo {
		for _, b := range st.Bal {
			if d, ok := b.Dt.day(); ok {
				if !okFrom || d.Before(from) {
					from, okFrom = d, true
				}
				if !okTo || d.After(to) {
					to, okTo = d, true
				}
			}
		}
	}
	if !okFrom || !okTo {
		return Statement{}, invalid(where + " has no period and no entry")
	}
	if to.Before(from) {
		to = from
	}
	out.From, out.To = from, to
	return out, nil
}

// line is one booked entry as a Line.
func (e camtEntry) line(where string) (Line, error) {
	amount, err := signed(e.Amt, e.CdtDbtInd, where)
	if err != nil {
		return Line{}, err
	}
	if amount.Sign() == 0 {
		return Line{}, invalid(where + " has an amount of zero")
	}
	booked, ok := e.BookgDt.day()
	if !ok {
		return Line{}, invalid(where + " has no booking day")
	}
	l := Line{BookedOn: booked, Amount: amount, BankID: clip(strings.TrimSpace(e.AcctSvcrRef), 140),
		Reversal: strings.EqualFold(strings.TrimSpace(e.RvslInd), "true"), Kind: e.kind()}
	if v, ok := e.ValDt.day(); ok {
		l.ValueOn = &v
	}
	var txs []camtTx
	for _, d := range e.Details {
		txs = append(txs, d.Txs...)
	}
	credit := amount.Sign() > 0
	text := clip(e.AddtlNtryInf, 1000)
	switch {
	case len(txs) == 1:
		tx := txs[0]
		l.Reference = reference(tx.EndToEndID)
		l.KID = kid(tx.CdtrRef)
		l.CounterpartyName, l.CounterpartyAccount = tx.counterparty(credit)
		l.Text = clip(firstOf(strings.Join(tx.Ustrd, " "), tx.AddtlTxInf, e.AddtlNtryInf), 1000)
		l.TxReference = clip(strings.TrimSpace(tx.AcctSvcrRef), 140)
		if l.BankID == "" {
			// The transaction's own servicer's reference is the bank's id of
			// an entry that has none; NtryRef and TxId are not unique.
			l.BankID = l.TxReference
		}
	case len(txs) > 1:
		l.Text = text
		if l.Text == "" {
			l.Text = fmt.Sprintf("Batch of %d payments", len(txs))
		}
		for i, tx := range txs {
			a := tx.Amt
			if a.Value == "" {
				a = tx.TxAmt
			}
			ind := tx.CdtDbtInd
			if ind == "" {
				ind = e.CdtDbtInd
			}
			v, err := signed(a, ind, fmt.Sprintf("%s, transaction %d", where, i+1))
			if err != nil {
				return Line{}, err
			}
			name, account := tx.counterparty(credit)
			l.Batch = append(l.Batch, BatchItem{Amount: v, KID: kid(tx.CdtrRef), Reference: reference(tx.EndToEndID),
				Name: name, Account: account, Text: clip(strings.Join(tx.Ustrd, " "), 140), BankID: clip(tx.AcctSvcrRef, 140)})
		}
	default:
		l.Text = text
	}
	return l, nil
}

// kind is what moved the money, as far as the bank's transaction code says:
// a card purchase, a transfer between the customer's own accounts, or a
// transfer.
func (e camtEntry) kind() string {
	switch {
	case e.Family == "CCRD" || e.Family == "DCRD" || e.Family == "POSD" || e.Family == "CWDL":
		return KindCard
	case e.SubFamily == "BOOK" || e.SubFamily == "ACCT" || strings.EqualFold(e.Proprietary, "INTERNAL"):
		return KindInternal
	}
	return KindTransfer
}

// counterparty is the payer of money in, the payee of money out.
func (tx camtTx) counterparty(credit bool) (string, string) {
	party, account := tx.Cdtr, tx.CdtrAcct
	if credit {
		party, account = tx.Dbtr, tx.DbtrAcct
	}
	raw := firstOf(account.ID.IBAN, account.ID.Othr)
	if bban, ok := NormaliseAccount(raw); ok {
		raw = bban
	}
	return clip(party.name(), 140), clip(raw, 34)
}

// signed is an amount with the sign its indicator gives: DBIT negative.
func signed(a camtAmount, indicator, where string) (*big.Rat, error) {
	text := strings.TrimSpace(a.Value)
	r, ok := new(big.Rat).SetString(text)
	if !ok || text == "" || strings.ContainsAny(text, "eE/") || !new(big.Rat).Mul(r, big.NewRat(100, 1)).IsInt() || r.Sign() < 0 {
		return nil, invalid(where + " has an amount that is not a decimal with at most two places")
	}
	if a.Ccy != "" && a.Ccy != "NOK" {
		return nil, invalid(where + " is in " + a.Ccy + "; only NOK is read here")
	}
	switch strings.TrimSpace(indicator) {
	case "DBIT":
		r.Neg(r)
	case "CRDT":
	default:
		return nil, invalid(where + " says neither CRDT nor DBIT")
	}
	return r, nil
}

func (d camtDate) day() (time.Time, bool) {
	if d.Dt != "" {
		return camtDay(d.Dt)
	}
	return camtDay(d.DtTm)
}

// camtDay is the calendar day of an ISO date or date-time, at UTC midnight:
// the day the bank wrote, whatever its offset.
func camtDay(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.DateOnly, s[:10])
	return t, err == nil
}

func reference(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NOTPROVIDED") {
		return ""
	}
	return clip(s, 140)
}

// kid is the first creditor reference that reads as a KID: 2 to 25 digits,
// with a dash allowed for the check digit.
func kid(refs []string) string {
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if n := len(r); n >= 2 && n <= 25 && allDigits(strings.ReplaceAll(r, "-", "")) {
			return r
		}
	}
	return ""
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
