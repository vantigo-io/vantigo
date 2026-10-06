package bankfile

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// This file is the OCR giro parser (D3 step 3, R4 §3.1): Mastercard
// Payment Services' fixed-width transmission of 80-character records,
//
//	NY000010                   the transmission's start
//	  NY090020                 an assignment's start, one per account
//	    NY09tt30 NY09tt31      amount items 1 and 2 of one transaction
//	    [NY09tt32]             amount item 3, types 20 and 21 only
//	  NY090088                 the assignment's end: its counts and sums
//	NY000089                   the transmission's end: the file's
//
// read all or nothing: every record, every count, every sum and every date
// is checked before the file is answered, and the first failure refuses it,
// naming its record (its line in the file).

// maxAmountMinor is the largest amount numeric(14,2) holds, in øre. With
// at most MaxTransactions amount items in a file, no sum leaves int64.
const maxAmountMinor = 99_999_999_999_999

// ocrParser walks a file's records once, in order.
type ocrParser struct {
	rest  []byte // what is left of the file after the record last read
	line  int    // the record last read's line in the file, 1-based
	today time.Time
	file  *File
	items int // amount items read so far: the format's transactions
}

// ParseOCR parses an OCR giro file, every booking date on or before today.
func ParseOCR(b []byte, today time.Time) (*File, error) {
	if len(b) > MaxBytes {
		return nil, &Error{Where: "file", Message: tooLarge}
	}
	p := &ocrParser{rest: bytes.TrimPrefix(b, bom), today: dateOf(today), file: newFile(FormatOCR)}
	if err := p.transmission(); err != nil {
		return nil, err
	}
	p.file.finish()
	return p.file, nil
}

// fail is a refusal at the record last read. A message quotes the file's
// own bytes, ISO-8859-1, so it is made valid UTF-8 for the response.
func (p *ocrParser) fail(format string, args ...any) *Error {
	return &Error{Where: "record " + strconv.Itoa(p.line), Message: strings.ToValidUTF8(fmt.Sprintf(format, args...), "\uFFFD")}
}

// atEnd reports whether only blank lines are left. It reads from the left
// and stops at the first record's first character, so a file padded with
// megabytes of white space is not read again for every record.
func (p *ocrParser) atEnd() bool {
	for _, c := range p.rest {
		switch c {
		case ' ', '\t', '\r', '\n', '\v', '\f':
		default:
			return false
		}
	}
	return true
}

// read is the next record, CR/LF stripped: 80 characters of ISO-8859-1
// beginning NY, of a record type the format has and, below the
// transmission, of service 09. A line that is valid UTF-8 of 80 characters
// is a file re-encoded on its way here, and is read back to ISO-8859-1
// (R4 §3.1: decode leniently). The caller asks atEnd before it reads.
func (p *ocrParser) read() ([]byte, error) {
	line, rest, _ := bytes.Cut(p.rest, []byte("\n"))
	p.rest = rest
	p.line++
	return p.check(bytes.TrimRight(line, "\r"))
}

// skipBlank passes over blank lines: those before the first record, and
// those after the last.
func (p *ocrParser) skipBlank() {
	for len(p.rest) > 0 {
		line, rest, _ := bytes.Cut(p.rest, []byte("\n"))
		if len(bytes.TrimSpace(line)) > 0 {
			return
		}
		p.rest = rest
		p.line++
	}
}

func (p *ocrParser) check(rec []byte) ([]byte, error) {
	if len(rec) != 80 {
		n := len(rec)
		if utf8.Valid(rec) {
			n = utf8.RuneCount(rec)
		}
		if n != 80 {
			return nil, p.fail("%d characters, but every record is 80", n)
		}
		latin := make([]byte, 0, 80)
		for _, r := range string(rec) {
			if r > 0xFF {
				return nil, p.fail("the character %q is not ISO-8859-1", r)
			}
			latin = append(latin, byte(r))
		}
		rec = latin
	}
	if string(rec[:2]) != "NY" {
		return nil, p.fail("the record begins %q, not NY", rec[:2])
	}
	switch rt := string(rec[6:8]); rt {
	case "10", "89":
		if string(rec[2:6]) != "0000" {
			return nil, p.fail("the transmission's records begin NY0000, not %q", rec[:6])
		}
	case "20", "30", "31", "32", "88":
		if service := string(rec[2:4]); service != "09" {
			return nil, p.fail("service code %s: only 09, OCR giro, is read", service)
		}
		tt := string(rec[4:6])
		if rt == "20" || rt == "88" {
			if tt != "00" {
				return nil, p.fail("assignment type %s: only 00 is read", tt)
			}
		} else if n, err := strconv.Atoi(tt); err != nil || n < 10 || n > 21 || !allDigits(tt) {
			return nil, p.fail("transaction type %s is not one of OCR giro's, 10–21", tt)
		}
	default:
		return nil, p.fail("record type %s is not one of OCR giro's", rt)
	}
	return rec, nil
}

// name is a record's kind as a message names it.
func name(rec []byte) string {
	switch string(rec[6:8]) {
	case "10":
		return "the transmission's start (NY000010)"
	case "20":
		return "an assignment's start (NY090020)"
	case "30":
		return fmt.Sprintf("amount item 1 (%s)", rec[:8])
	case "31":
		return fmt.Sprintf("amount item 2 (%s)", rec[:8])
	case "32":
		return fmt.Sprintf("amount item 3 (%s)", rec[:8])
	case "88":
		return "an assignment's end (NY090088)"
	}
	return "the transmission's end (NY000089)"
}

// number is the digits at positions from–to (1-based, inclusive, as the
// format's tables number them).
func (p *ocrParser) number(rec []byte, from, to int, what string) (int64, error) {
	s := string(rec[from-1 : to])
	if !allDigits(s) {
		return 0, p.fail("positions %d–%d (%s) are %q, not digits", from, to, what, s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, p.fail("positions %d–%d (%s) are %q, not a number", from, to, what, s)
	}
	return n, nil
}

// date is the DDMMYY at positions from–to, the century 2000–2099; zero
// when blank is true and the field is 000000.
func (p *ocrParser) date(rec []byte, from int, what string, blank bool) (time.Time, error) {
	s := string(rec[from-1 : from+5])
	if blank && s == "000000" {
		return time.Time{}, nil
	}
	if allDigits(s) {
		d, _ := strconv.Atoi(s[0:2])
		m, _ := strconv.Atoi(s[2:4])
		y, _ := strconv.Atoi(s[4:6])
		t := time.Date(2000+y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if m >= 1 && m <= 12 && t.Day() == d && t.Month() == time.Month(m) {
			return t, nil
		}
	}
	return time.Time{}, p.fail("the %s %s is not a date (DDMMYY)", what, s)
}

// transmission reads the whole file: the start record, the assignments,
// the end record checked against them, and nothing after it.
func (p *ocrParser) transmission() error {
	if p.atEnd() {
		return &Error{Where: "file", Message: "The file holds no records"}
	}
	p.skipBlank()
	rec, err := p.read()
	if err != nil {
		return err
	}
	if string(rec[:8]) != "NY000010" {
		return p.fail("the file begins with %s, not the transmission's start (NY000010)", name(rec))
	}
	for _, f := range [][2]int{{9, 16}, {17, 23}, {24, 31}} {
		if _, err := p.number(rec, f[0], f[1], "the transmission's identity"); err != nil {
			return err
		}
	}
	p.file.Identity = string(rec[8:16]) + ":" + string(rec[16:23]) + ":" + string(rec[23:31])

	var count, records int64 = 0, 2
	var sum int64
	assignments := 0
	for {
		if p.atEnd() {
			return &Error{Where: "file", Message: "The file ends before the transmission's end (NY000089)"}
		}
		rec, err := p.read()
		if err != nil {
			return err
		}
		switch string(rec[6:8]) {
		case "20":
			n, r, s, err := p.assignment(rec)
			if err != nil {
				return err
			}
			count, records, sum = count+n, records+r, sum+s
			assignments++
			continue
		case "89":
			if assignments == 0 {
				return p.fail("a transmission with no assignments")
			}
			if err := p.totals(rec, "transmission", count, records, sum); err != nil {
				return err
			}
			if !p.atEnd() {
				p.skipBlank()
				p.line++
				return p.fail("a record after the transmission's end")
			}
			return nil
		}
		return p.fail("%s where an assignment's start (NY090020) or the transmission's end (NY000089) belongs", name(rec))
	}
}

// totals checks an end record's transaction count, record count and signed
// sum, at positions 9–16, 17–24 and 25–41 of both end records.
func (p *ocrParser) totals(rec []byte, of string, count, records, sum int64) error {
	for _, c := range []struct {
		from, to  int
		what      string
		want      int64
		disagrees string
	}{
		{9, 16, "the number of transactions", count, "counts %d transactions; the %s holds %d"},
		{17, 24, "the number of records", records, "counts %d records; the %s has %d"},
		{25, 41, "the sum", sum, "sums to %d øre; the %s's amounts sum to %d"},
	} {
		got, err := p.number(rec, c.from, c.to, c.what)
		if err != nil {
			return err
		}
		if got != c.want {
			return p.fail("the %s's end "+c.disagrees, of, got, of, c.want)
		}
	}
	return nil
}

// assignment reads one assignment after its start record: its account, its
// transactions, and its end record checked against them. It answers the
// transactions, the records (start and end included) and the signed sum.
func (p *ocrParser) assignment(start []byte) (count, records int64, sum int64, err error) {
	account, ok := NormaliseAccount(string(start[24:35]))
	if !ok {
		return 0, 0, 0, p.fail("the assignment's account %q is not an 11-digit account number", start[24:35])
	}
	number, err := p.number(start, 18, 24, "the assignment's number")
	if err != nil {
		return 0, 0, 0, err
	}
	p.file.Accounts = append(p.file.Accounts, account)

	records = 2
	var first, last time.Time
	for {
		if p.atEnd() {
			return 0, 0, 0, &Error{Where: "file", Message: "The file ends inside an assignment"}
		}
		rec, err := p.read()
		if err != nil {
			return 0, 0, 0, err
		}
		switch string(rec[6:8]) {
		case "30":
			r, amount, settled, err := p.transaction(rec, account, number)
			if err != nil {
				return 0, 0, 0, err
			}
			if count == 0 || settled.Before(first) {
				first = settled
			}
			if count == 0 || settled.After(last) {
				last = settled
			}
			count, records, sum = count+1, records+r, sum+amount
			continue
		case "88":
			if count == 0 {
				return 0, 0, 0, p.fail("an assignment with no transactions")
			}
			if err := p.totals(rec, "assignment", count, records, sum); err != nil {
				return 0, 0, 0, err
			}
			for _, d := range []struct {
				from int
				what string
				want time.Time
			}{{48, "first", first}, {54, "last", last}} {
				if got, want := string(rec[d.from-1:d.from+5]), d.want.Format("020106"); got != want {
					return 0, 0, 0, p.fail("the assignment's end gives %s as its %s settlement date; its transactions' is %s",
						got, d.what, want)
				}
			}
			return count, records, sum, nil
		}
		return 0, 0, 0, p.fail("%s where amount item 1 (NY09tt30) or the assignment's end (NY090088) belongs", name(rec))
	}
}

// transaction reads one transaction from its amount item 1: item 2, and
// item 3 for types 20 and 21. It answers the records read and the amount
// as the control sums count it — a '-' subtracts, but a type 18 or 20
// reversal is added whatever its sign (R4 §3.1) — and its settlement date.
// Types 10–17 become transactions; 18–21, card information, are counted.
func (p *ocrParser) transaction(item1 []byte, account string, assignment int64) (records, signed int64, settled time.Time, err error) {
	p.items++
	if p.items > MaxTransactions {
		return 0, 0, time.Time{}, p.fail("more than %d transactions in one file", MaxTransactions)
	}
	tt := string(item1[4:6])
	number, err := p.number(item1, 9, 15, "the transaction number")
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if settled, err = p.date(item1, 16, "settlement date", false); err != nil {
		return 0, 0, time.Time{}, err
	}
	if msg := checkBooked(settled, p.today); msg != "" {
		return 0, 0, time.Time{}, p.fail("%s", msg)
	}
	sign := item1[31]
	if sign != '0' && sign != '-' {
		return 0, 0, time.Time{}, p.fail("the sign %q is neither '-' nor '0'", sign)
	}
	amount, err := p.number(item1, 33, 49, "the amount")
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if amount > maxAmountMinor {
		return 0, 0, time.Time{}, p.fail("the amount of %d øre is more than an amount can be", amount)
	}
	kid := strings.Trim(string(item1[49:74]), " ")
	if !kidShaped(kid) {
		return 0, 0, time.Time{}, p.fail("the KID %q is not digits, with only the last allowed to be '-'", kid)
	}

	item2, err := p.next(item1, "amount item 2 (NY09"+tt+"31)", "31")
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	ordered, err := p.date(item2, 42, "order date", true)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	records = 2
	if tt == "20" || tt == "21" {
		// Item 3's text, ISO-8859-1, belongs to card information only,
		// which is counted, not kept: it is read for the grammar's sake.
		if _, err := p.next(item1, "amount item 3 (NY09"+tt+"32): a type "+tt+" carries a text", "32"); err != nil {
			return 0, 0, time.Time{}, err
		}
		records++
	}

	signed = amount
	if sign == '-' && tt != "18" && tt != "20" {
		signed = -amount
	}
	switch {
	case tt >= "18":
		p.file.Ignored[IgnoredCardInformation]++
	case amount == 0:
		p.file.Ignored[IgnoredZeroAmount]++
	default:
		tx := Transaction{
			LineRef:       strconv.FormatInt(assignment, 10) + "/" + strconv.FormatInt(number, 10),
			Account:       account,
			Negative:      sign == '-',
			BookedOn:      settled,
			AmountMinor:   amount,
			Currency:      "NOK",
			KID:           kid,
			DebtorAccount: field(item2[47:58]),
			ArchiveRef:    field(item2[25:34]),
			BankCode:      tt,
		}
		if !ordered.IsZero() {
			tx.OrderedOn = &ordered
		}
		p.file.Transactions = append(p.file.Transactions, tx)
	}
	return records, signed, settled, nil
}

// next reads the record that must follow item1 — item 2 or item 3, rt — of
// the same transaction type and number.
func (p *ocrParser) next(item1 []byte, want, rt string) ([]byte, error) {
	if p.atEnd() {
		return nil, &Error{Where: "file", Message: "The file ends where " + want + " belongs"}
	}
	rec, err := p.read()
	if err != nil {
		return nil, err
	}
	if string(rec[6:8]) != rt {
		return nil, p.fail("%s where %s belongs", name(rec), want)
	}
	if string(rec[4:6]) != string(item1[4:6]) {
		return nil, p.fail("transaction type %s, but its amount item 1's is %s", rec[4:6], item1[4:6])
	}
	if string(rec[8:15]) != string(item1[8:15]) {
		return nil, p.fail("transaction number %s, but its amount item 1's is %s", rec[8:15], item1[8:15])
	}
	return rec, nil
}

// kidShaped is a KID field's rule (D3): empty, or digits of which the last
// may be MOD11's '-'.
func kidShaped(s string) bool {
	if s == "" {
		return true
	}
	return allDigits(strings.TrimSuffix(s, "-"))
}

// field is a free field of a record — a debtor account, an archive
// reference — as text that can be stored: ISO-8859-1 read as such, control
// characters made spaces, trimmed, and "" when it is only zeros (none).
func field(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c < 0x20 || (c >= 0x7F && c < 0xA0) {
			c = ' '
		}
		s.WriteRune(rune(c))
	}
	out := strings.TrimSpace(s.String())
	if strings.Trim(out, "0") == "" {
		return ""
	}
	return out
}
