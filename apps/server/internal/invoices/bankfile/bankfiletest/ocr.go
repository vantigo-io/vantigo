// Package bankfiletest builds bank files for tests: well-formed ones whose
// counts and control totals are computed, which a test then corrupts to
// make a broken one, so no broken file is committed (reading 42). The
// parser's tests, the invoices module's and the integration story share it.
package bankfiletest

import (
	"fmt"
	"strings"
	"time"
)

// OCRPayment is one transaction of an OCR giro file: Type is the
// transaction type, 10–21; Account the assignment's 11-digit account;
// Text is item 3's, written only for types 20 and 21. A zero Ordered is
// written as 000000.
type OCRPayment struct {
	Type                           int
	Account                        string
	Settled, Ordered               time.Time
	AmountMinor                    int64
	Negative                       bool
	KID, ArchiveRef, DebtorAccount string
	Text                           string
}

// The fixed fields of every built file: Mastercard Payment Services as the
// sender, a recipient and an agreement id — R4 §3.1's example's.
const (
	ocrSender    = "00008080"
	ocrRecipient = "00012345"
	ocrAgreement = "001234567"
)

// OCR is an OCR giro transmission numbered transmission (seven digits) of
// payments, one assignment per account in the order the accounts are first
// seen, the payments in the order given; every count, sum and date of the
// end records computed. A type 18 or 20 is added to the sums whatever its
// sign, as the specification has it. Each record ends in "\n".
func OCR(transmission string, payments ...OCRPayment) []byte {
	var accounts []string
	byAccount := map[string][]OCRPayment{}
	for _, p := range payments {
		if _, seen := byAccount[p.Account]; !seen {
			accounts = append(accounts, p.Account)
		}
		byAccount[p.Account] = append(byAccount[p.Account], p)
	}

	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	line("NY000010" + ocrSender + fmt.Sprintf("%07s", transmission) + ocrRecipient + zeros(49))

	var fileTxs, fileRecords int
	var fileSum int64
	var fileLast time.Time
	for n, account := range accounts {
		line("NY090020" + ocrAgreement + fmt.Sprintf("%07d", n+1) + fmt.Sprintf("%-11s", account) + zeros(45))
		records := 2
		var sum int64
		var first, last time.Time
		for i, p := range byAccount[account] {
			tt := fmt.Sprintf("%02d", p.Type)
			tx := fmt.Sprintf("%07d", i+1)
			card := p.Type >= 18
			partial, sign := "1", "0"
			if card {
				partial = "0"
			}
			if p.Negative {
				sign = "-"
			}
			line("NY09" + tt + "30" + tx + ddmmyy(p.Settled) + "01" + fmt.Sprintf("%02d", p.Settled.Day()) +
				partial + "00001" + sign + fmt.Sprintf("%017d", p.AmountMinor) + fmt.Sprintf("%25s", p.KID) + "00" + zeros(4))
			line("NY09" + tt + "31" + tx + zeros(10) + fmt.Sprintf("%09s", p.ArchiveRef) + zeros(7) +
				ddmmyy(p.Ordered) + fmt.Sprintf("%011s", p.DebtorAccount) + zeros(22))
			records += 2
			if p.Type == 20 || p.Type == 21 {
				line("NY09" + tt + "32" + tx + latin1(p.Text, 40) + zeros(25))
				records++
			}
			if p.Negative && p.Type != 18 && p.Type != 20 {
				sum -= p.AmountMinor
			} else {
				sum += p.AmountMinor
			}
			if i == 0 || p.Settled.Before(first) {
				first = p.Settled
			}
			if i == 0 || p.Settled.After(last) {
				last = p.Settled
			}
		}
		n := len(byAccount[account])
		line("NY090088" + fmt.Sprintf("%08d%08d%017d", n, records, sum) + ddmmyy(last) + ddmmyy(first) + ddmmyy(last) + zeros(21))
		fileTxs += n
		fileRecords += records
		fileSum += sum
		if last.After(fileLast) {
			fileLast = last
		}
	}
	line("NY000089" + fmt.Sprintf("%08d%08d%017d", fileTxs, fileRecords+2, fileSum) + ddmmyy(fileLast) + zeros(33))
	return []byte(b.String())
}

func zeros(n int) string { return strings.Repeat("0", n) }

// ddmmyy is t as the format writes a date, 000000 for the zero time.
func ddmmyy(t time.Time) string {
	if t.IsZero() {
		return "000000"
	}
	return t.Format("020106")
}

// latin1 is s in ISO-8859-1, '?' for what it cannot hold, cut or
// blank-filled to n bytes.
func latin1(s string, n int) string {
	out := make([]byte, 0, n)
	for _, r := range s {
		if len(out) == n {
			break
		}
		if r > 0xFF {
			r = '?'
		}
		out = append(out, byte(r))
	}
	for len(out) < n {
		out = append(out, ' ')
	}
	return string(out)
}
