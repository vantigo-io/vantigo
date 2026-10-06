package bankfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// This file is the camt.054 parser (D3 step 3, R4 §3.2): a bank's
// debit/credit notification, ISO 20022 BkToCstmrDbtCdtNtfctn in version
// .001.02 or .001.08,
//
//	Document/BkToCstmrDbtCdtNtfctn
//	  GrpHdr/{MsgId, CreDtTm}           the file's identity
//	  Ntfctn (1..n)                     one per account
//	    Id, Acct/Id/{IBAN|Othr/Id}, TxsSummry
//	    Ntry (0..n)                     a booking: a lump sum
//	      Amt, CdtDbtInd, RvslInd, Sts, BookgDt, ValDt, BkTxCd
//	      NtryDtls/Btch/NbOfTxs
//	      NtryDtls/TxDtls (0..n)        the payments the lump sum is made of
//
// read in two passes, after a scan of the raw bytes that refuses a tag
// longer than 4 096 bytes (so no element carries a million attributes or
// namespace declarations). The first walks the tokens and keeps nothing: no
// DOCTYPE, at most 64 levels, at most 10 000 + 100 per transaction found
// elements, every booked amount in NOK — so a hostile file is refused
// before any of it is kept (D3, M9, m4). The second decodes the paths of R4 §3.2's table
// for the file's version and checks the file all or nothing: every entry's
// transactions against its amount and its batch count, the summary against
// the entries, every amount, date and reference; the first failure refuses
// the file, naming its element as Where: "Ntry[2]/NtryDtls/TxDtls[1]/…",
// prefixed "Ntfctn[k]/" from a file's second notification on.

// The hardening's limits (D3).
const (
	camtMaxDepth           = 64
	camtBaseElements       = 10_000
	camtElementsPerLine    = 100
	camtVersion08          = "camt.054.001.08"
	camtMaxSum             = maxAmountMinor * MaxTransactions
	camtRemittanceTextSize = 1000 // remittance_text varchar(1000)
	camtNameSize           = 140  // debtor_name varchar(140)
	camtMaxTag             = 4096
	kidSize                = 25 // kid varchar(25), and kid.Parse's longest
)

// ParseCamt054 parses a camt.054 notification, .001.02 or .001.08, every
// booking date on or before today.
func ParseCamt054(b []byte, today time.Time) (*File, error) {
	if len(b) > MaxBytes {
		return nil, &Error{Where: "file", Message: tooLarge}
	}
	b = bytes.TrimPrefix(b, bom)
	if err := checkTags(b); err != nil {
		return nil, err
	}
	version, err := guardCamt(b)
	if err != nil {
		return nil, err
	}
	var doc camtDocument
	d, encoding := camtDecoder(b)
	if err := d.Decode(&doc); err != nil {
		return nil, xmlRefusal(err, *encoding)
	}
	p := &camtParser{v08: version == camtVersion08, today: dateOf(today), file: newFile(FormatCamt054)}
	p.file.Version = version
	if err := p.document(&doc); err != nil {
		return nil, err
	}
	p.file.finish()
	return p.file, nil
}

// camtDecoder is a strict decoder of b that reads UTF-8 and ISO-8859-1,
// and the encoding b declares when it is neither, for the refusal.
func camtDecoder(b []byte) (*xml.Decoder, *string) {
	unsupported := new(string)
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf8":
			return r, nil
		case "iso-8859-1", "iso8859-1", "iso_8859-1", "latin1", "latin-1", "l1", "us-ascii", "ascii":
			raw, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			out := make([]byte, 0, len(raw))
			for _, c := range raw {
				out = utf8.AppendRune(out, rune(c))
			}
			return bytes.NewReader(out), nil
		}
		*unsupported = label
		return nil, errors.New("unsupported encoding")
	}
	return d, unsupported
}

// xmlRefusal is a decoder's error as the file's refusal.
func xmlRefusal(err error, unsupported string) *Error {
	if unsupported != "" {
		return &Error{Where: "file", Message: refusalText("the file declares the encoding %q; only UTF-8, ISO-8859-1 and US-ASCII are read", unsupported)}
	}
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		return &Error{Where: "file", Message: refusalText("not well-formed XML at line %d: %s", syntax.Line, syntax.Msg)}
	}
	return &Error{Where: "file", Message: refusalText("not well-formed XML: %v", err)}
}

// refusalText is a refusal's message, valid UTF-8 whatever the file held.
func refusalText(format string, args ...any) string {
	return strings.ToValidUTF8(fmt.Sprintf(format, args...), "�")
}

// camtFrame is an open element of the first pass: its segment of a Where,
// and the children it has counted.
type camtFrame struct {
	name, seg          string
	notifications, txs int // Ntfctn under the message, TxDtls under an entry
	entries            int // Ntry under a notification
	hasTx              bool
}

// guardCamt is the first pass: the whole file walked as tokens, nothing
// kept but the counts. It answers the version the root names.
func guardCamt(b []byte) (string, error) {
	d, encoding := camtDecoder(b)
	var stack []camtFrame
	version := ""
	elements, lines := 0, 0
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", xmlRefusal(err, *encoding)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return "", &Error{Where: "file", Message: "A DOCTYPE or other declaration is not allowed in a camt.054 file"}
		case xml.StartElement:
			elements++
			if len(stack) == 0 {
				if version != "" {
					return "", &Error{Where: "file", Message: "a second root element after the Document"}
				}
				v, ok := camtNamespaces[t.Name.Space]
				if t.Name.Local != "Document" || !ok {
					return "", &Error{Where: "Document", Message: refusalText(
						"the root is %s in the namespace %q, not a camt.054.001.02 or camt.054.001.08 Document", t.Name.Local, t.Name.Space)}
				}
				version = v
			}
			if len(stack) >= camtMaxDepth {
				return "", &Error{Where: "file", Message: refusalText(
					"the element %s is nested %d levels deep; at most %d are read", t.Name.Local, len(stack)+1, camtMaxDepth)}
			}
			if allowed := camtBaseElements + camtElementsPerLine*min(lines, MaxTransactions); elements > allowed {
				return "", &Error{Where: "file", Message: fmt.Sprintf(
					"more than %d elements for the %d transactions found so far", allowed, lines)}
			}
			frame := camtFrame{name: t.Name.Local, seg: t.Name.Local}
			n := len(stack)
			switch {
			case frame.name == "Ntfctn" && n > 0 && stack[n-1].name == "BkToCstmrDbtCdtNtfctn":
				stack[n-1].notifications++
				frame.seg = ntfctnWhere(stack[n-1].notifications)
			case frame.name == "Ntry" && n > 0 && stack[n-1].name == "Ntfctn":
				stack[n-1].entries++
				frame.seg = "Ntry[" + strconv.Itoa(stack[n-1].entries) + "]"
			case frame.name == "TxDtls" && n > 1 && stack[n-2].name == "Ntry":
				stack[n-2].txs++
				stack[n-2].hasTx = true
				frame.seg = "TxDtls[" + strconv.Itoa(stack[n-2].txs) + "]"
				lines++
			}
			stack = append(stack, frame)
			if camtBookedAmount(stack) {
				ccy := ""
				for _, a := range t.Attr {
					if a.Name.Local == "Ccy" {
						ccy = a.Value
					}
				}
				if ccy != "NOK" {
					return "", &Error{Where: framesWhere(stack), Message: refusalText(
						"the currency is %q; a file is read only when every booked amount is in NOK", ccy)}
				}
			}
		case xml.EndElement:
			if top := stack[len(stack)-1]; top.name == "Ntry" && !top.hasTx {
				lines++ // an entry without TxDtls is one transaction
			}
			stack = stack[:len(stack)-1]
		}
	}
	if version == "" {
		return "", &Error{Where: "file", Message: notABankFile}
	}
	return version, nil
}

// camtBookedAmount reports whether the element on top of stack is a
// booked amount, whose currency decides the file (research case m): an
// entry's Ntry/Amt, a transaction's own Amt (.08) or its
// AmtDtls/TxAmt/Amt. An instructed, counter-value or remitted amount, a
// charge and an entry's own AmtDtls say what was sent or agreed, not what
// was booked, and are not judged; TxsSummry's sums are the account's,
// whose Ntfctn/Acct/Ccy the second pass judges.
func camtBookedAmount(stack []camtFrame) bool {
	n := len(stack)
	if n < 2 || stack[n-1].name != "Amt" {
		return false
	}
	switch stack[n-2].name {
	case "Ntry", "TxDtls":
		return true
	case "TxAmt":
		return n >= 4 && stack[n-3].name == "AmtDtls" && stack[n-4].name == "TxDtls"
	}
	return false
}

// checkTags scans b's raw bytes once and refuses a tag — from its '<' to
// its '>', quoted values honoured, comments and CDATA passed over — longer
// than camtMaxTag bytes. The decoder reads a start element's attributes
// and namespace declarations all at once, so this is what bounds one
// element's cost before the decoder sees it.
func checkTags(b []byte) error {
	for i := 0; i < len(b); {
		j := bytes.IndexByte(b[i:], '<')
		if j < 0 {
			return nil
		}
		i += j
		rest := b[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return nil // unterminated: the decoder refuses it
			}
			i += 4 + end + 3
			continue
		case bytes.HasPrefix(rest, []byte("<![CDATA[")):
			end := bytes.Index(rest[9:], []byte("]]>"))
			if end < 0 {
				return nil
			}
			i += 9 + end + 3
			continue
		}
		var quote byte
		k := 1
		for ; k < len(rest); k++ {
			c := rest[k]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
			} else if c == '"' || c == '\'' {
				quote = c
			} else if c == '>' {
				break
			}
			if k >= camtMaxTag {
				return &Error{Where: "file", Message: fmt.Sprintf("a tag longer than %d bytes", camtMaxTag)}
			}
		}
		i += k + 1
	}
	return nil
}

// framesWhere is the open elements as a Where, from below the message.
func framesWhere(stack []camtFrame) string {
	var segs []string
	for i := 2; i < len(stack); i++ {
		segs = append(segs, stack[i].seg)
	}
	if len(segs) > 1 && segs[0] == "Ntfctn" && strings.HasPrefix(segs[1], "Ntry[") {
		segs = segs[1:]
	}
	if len(segs) == 0 {
		return "file"
	}
	return strings.Join(segs, "/")
}

// ntfctnWhere names a file's k-th notification; entryWhere its i-th entry,
// which in the first notification is named alone.
func ntfctnWhere(k int) string {
	if k == 1 {
		return "Ntfctn"
	}
	return "Ntfctn[" + strconv.Itoa(k) + "]"
}

func entryWhere(k, i int) string {
	s := "Ntry[" + strconv.Itoa(i) + "]"
	if k > 1 {
		s = ntfctnWhere(k) + "/" + s
	}
	return s
}

// The paths the second pass reads, both versions' in one shape; the
// parser picks a version's own where they differ (R4 §3.2's table).
type (
	camtDocument struct {
		Message *struct {
			GrpHdr struct {
				MsgID   string `xml:"MsgId"`
				CreDtTm string `xml:"CreDtTm"`
			} `xml:"GrpHdr"`
			Notifications []camtNotification `xml:"Ntfctn"`
		} `xml:"BkToCstmrDbtCdtNtfctn"`
	}
	camtNotification struct {
		ID   string `xml:"Id"`
		Acct struct {
			ID  camtAccountID `xml:"Id"`
			Ccy string        `xml:"Ccy"`
		} `xml:"Acct"`
		Summary *struct {
			All *struct {
				camtCount
				NetAmount02 string `xml:"TtlNetNtryAmt"`
				Direction02 string `xml:"CdtDbtInd"`
				Net08       struct {
					Amount    string `xml:"Amt"`
					Direction string `xml:"CdtDbtInd"`
				} `xml:"TtlNetNtry"`
			} `xml:"TtlNtries"`
			Credits *camtCount `xml:"TtlCdtNtries"`
			Debits  *camtCount `xml:"TtlDbtNtries"`
		} `xml:"TxsSummry"`
		Entries []camtEntry `xml:"Ntry"`
	}
	camtCount struct {
		Number string `xml:"NbOfNtries"`
		Sum    string `xml:"Sum"`
	}
	camtAccountID struct {
		IBAN  string `xml:"IBAN"`
		Other string `xml:"Othr>Id"`
	}
	camtAmount struct {
		Value string `xml:",chardata"`
	}
	camtDate struct {
		Date     string `xml:"Dt"`
		DateTime string `xml:"DtTm"`
	}
	camtEntry struct {
		Amount    *camtAmount `xml:"Amt"`
		Direction string      `xml:"CdtDbtInd"`
		Reversal  string      `xml:"RvslInd"`
		Status    struct {
			Text  string `xml:",chardata"` // .02
			Code  string `xml:"Cd"`        // .08
			Prtry string `xml:"Prtry"`     // .08
		} `xml:"Sts"`
		Booked *camtDate `xml:"BookgDt"`
		Value  *camtDate `xml:"ValDt"`
		BankTx struct {
			Domain struct {
				Code   string `xml:"Cd"`
				Family struct {
					Code    string `xml:"Cd"`
					SubCode string `xml:"SubFmlyCd"`
				} `xml:"Fmly"`
			} `xml:"Domn"`
			Proprietary string `xml:"Prtry>Cd"`
		} `xml:"BkTxCd"`
		Details []struct {
			Batch *struct {
				Number string `xml:"NbOfTxs"`
			} `xml:"Btch"`
			Txs []camtTx `xml:"TxDtls"`
		} `xml:"NtryDtls"`
		AddtlInfo string `xml:"AddtlNtryInf"`
	}
	camtTx struct {
		ArchiveRef string      `xml:"Refs>AcctSvcrRef"`
		Amount08   *camtAmount `xml:"Amt"`       // .08
		Direction  string      `xml:"CdtDbtInd"` // .08
		Amount     *camtAmount `xml:"AmtDtls>TxAmt>Amt"`
		Debtor     struct {
			Name02 string `xml:"Nm"`     // .02
			Name08 string `xml:"Pty>Nm"` // .08
		} `xml:"RltdPties>Dbtr"`
		DebtorAccount camtAccountID `xml:"RltdPties>DbtrAcct>Id"`
		Unstructured  []string      `xml:"RmtInf>Ustrd"`
		Structured    []struct {
			Type string `xml:"CdtrRefInf>Tp>CdOrPrtry>Cd"`
			Ref  string `xml:"CdtrRefInf>Ref"`
		} `xml:"RmtInf>Strd"`
	}
)

// camtParser is the second pass over a decoded file.
type camtParser struct {
	v08   bool
	today time.Time
	file  *File
}

func (p *camtParser) document(doc *camtDocument) error {
	m := doc.Message
	if m == nil {
		return &Error{Where: "Document", Message: "the Document holds no BkToCstmrDbtCdtNtfctn"}
	}
	id, err := reference("GrpHdr/MsgId", m.GrpHdr.MsgID, 35)
	if err != nil {
		return err
	}
	if id == "" {
		return &Error{Where: "GrpHdr/MsgId", Message: "the message has no identification"}
	}
	created, err := reference("GrpHdr/CreDtTm", m.GrpHdr.CreDtTm, 35)
	if err != nil {
		return err
	}
	if _, ok := camtDateTime(created); !ok {
		return &Error{Where: "GrpHdr/CreDtTm", Message: refusalText("%q is not a date and time", created)}
	}
	p.file.Identity = id + "|" + created
	if len(m.Notifications) == 0 {
		return &Error{Where: "file", Message: "The file holds no notification (Ntfctn)"}
	}
	entries := 0
	for k := range m.Notifications {
		if err := p.notification(k+1, &m.Notifications[k]); err != nil {
			return err
		}
		entries += len(m.Notifications[k].Entries)
	}
	if entries == 0 {
		return &Error{Where: "file", Message: "The file holds no entries (Ntry)"}
	}
	return nil
}

// camtTotals are a notification's entries as its summary counts them.
type camtTotals struct {
	credits, debits int
	credited        int64
	debited         int64
}

func (p *camtParser) notification(k int, n *camtNotification) error {
	where := ntfctnWhere(k)
	id, err := reference(where+"/Id", n.ID, 35)
	if err != nil {
		return err
	}
	if id == "" {
		return &Error{Where: where + "/Id", Message: "the notification has no identification"}
	}
	raw := n.Acct.ID.IBAN
	if raw == "" {
		raw = n.Acct.ID.Other
	}
	account, ok := NormaliseAccount(strings.TrimSpace(raw))
	if !ok {
		return &Error{Where: where + "/Acct/Id", Message: refusalText("%q is not a Norwegian account number", strings.TrimSpace(raw))}
	}
	if ccy := strings.TrimSpace(n.Acct.Ccy); ccy != "" && ccy != "NOK" {
		return &Error{Where: where + "/Acct/Ccy", Message: refusalText("the account's currency is %s; only NOK is read", ccy)}
	}
	p.file.Accounts = append(p.file.Accounts, account)

	var totals camtTotals
	for i := range n.Entries {
		amount, debit, err := p.entry(k, i+1, &n.Entries[i], id, account)
		if err != nil {
			return err
		}
		if debit {
			totals.debits++
			totals.debited += amount
		} else {
			totals.credits++
			totals.credited += amount
		}
		if totals.credited > camtMaxSum || totals.debited > camtMaxSum {
			return &Error{Where: entryWhere(k, i+1), Message: "the notification's entries sum to more than a file can hold"}
		}
	}
	return p.summary(where, n, totals)
}

// summary checks TxsSummry, when the bank wrote one, against the entries:
// the number and sum of all of them, of the credits and of the debits,
// and the net (R4 §3.6's pre-check 2).
func (p *camtParser) summary(where string, n *camtNotification, t camtTotals) error {
	s := n.Summary
	if s == nil {
		return nil
	}
	where += "/TxsSummry"
	if s.All != nil {
		if err := checkCount(where+"/TtlNtries", s.All.camtCount, t.credits+t.debits, t.credited+t.debited); err != nil {
			return err
		}
		amount, direction := s.All.NetAmount02, s.All.Direction02
		path := where + "/TtlNtries/TtlNetNtryAmt"
		if p.v08 {
			amount, direction = s.All.Net08.Amount, s.All.Net08.Direction
			path = where + "/TtlNtries/TtlNetNtry"
		}
		if strings.TrimSpace(amount) != "" {
			net, msg := camtAmountMinor(amount)
			if msg != "" {
				return &Error{Where: path, Message: msg}
			}
			switch strings.TrimSpace(direction) {
			case "DBIT":
				net = -net
			case "CRDT":
			default:
				if net != 0 {
					return &Error{Where: path, Message: refusalText("the net's direction %q is neither CRDT nor DBIT", direction)}
				}
			}
			if want := t.credited - t.debited; net != want {
				return &Error{Where: path, Message: fmt.Sprintf("the summary's net is %s; the entries' is %s",
					signedAmount(net), signedAmount(want))}
			}
		}
	}
	if s.Credits != nil {
		if err := checkCount(where+"/TtlCdtNtries", *s.Credits, t.credits, t.credited); err != nil {
			return err
		}
	}
	if s.Debits != nil {
		if err := checkCount(where+"/TtlDbtNtries", *s.Debits, t.debits, t.debited); err != nil {
			return err
		}
	}
	return nil
}

func checkCount(where string, c camtCount, entries int, sum int64) error {
	if number := strings.TrimSpace(c.Number); number != "" {
		if n, err := strconv.Atoi(number); err != nil || n != entries {
			return &Error{Where: where + "/NbOfNtries", Message: refusalText("the summary counts %s entries; the notification holds %d", number, entries)}
		}
	}
	if text := strings.TrimSpace(c.Sum); text != "" {
		got, msg := camtAmountMinor(text)
		if msg != "" {
			return &Error{Where: where + "/Sum", Message: msg}
		}
		if got != sum {
			return &Error{Where: where + "/Sum", Message: fmt.Sprintf("the summary sums to %s; the entries to %s", signedAmount(got), signedAmount(sum))}
		}
	}
	return nil
}

// camtLine is one line of an entry: a TxDtls, or the entry itself when it
// has none.
type camtLine struct {
	where  string
	tx     *camtTx
	amount int64
}

// entry checks the i-th entry of the k-th notification and keeps what of
// it becomes transactions (D3): every line of a booked credit; every line
// of a booked debit that is a reversal, as a debit; a 0.00 line, a debit
// that is no reversal and an entry not booked counted, not kept. It
// answers the entry's amount and whether it is a debit, for the summary.
func (p *camtParser) entry(k, i int, e *camtEntry, notification, account string) (int64, bool, error) {
	where := entryWhere(k, i)
	direction := strings.TrimSpace(e.Direction)
	if direction != "CRDT" && direction != "DBIT" {
		return 0, false, &Error{Where: where + "/CdtDbtInd", Message: refusalText("%q is neither CRDT nor DBIT", direction)}
	}
	if e.Amount == nil {
		return 0, false, &Error{Where: where + "/Amt", Message: "the entry has no amount"}
	}
	amount, msg := camtAmountMinor(e.Amount.Value)
	if msg != "" {
		return 0, false, &Error{Where: where + "/Amt", Message: msg}
	}
	status := strings.TrimSpace(e.Status.Text)
	if p.v08 {
		status = strings.TrimSpace(e.Status.Code)
		if status == "" {
			status = strings.TrimSpace(e.Status.Prtry)
		}
	}
	if status == "" {
		return 0, false, &Error{Where: where + "/Sts", Message: "the entry has no status"}
	}
	booked := status == "BOOK"
	var bookedOn time.Time
	if booked {
		if e.Booked == nil {
			return 0, false, &Error{Where: where + "/BookgDt", Message: "a booked entry without a booking date"}
		}
		d, ok := e.Booked.day()
		if !ok {
			return 0, false, &Error{Where: where + "/BookgDt", Message: refusalText("%q is not a date", e.Booked.text())}
		}
		if msg := checkBooked(d, p.today); msg != "" {
			return 0, false, &Error{Where: where + "/BookgDt", Message: msg}
		}
		bookedOn = d
	}
	var valueOn *time.Time
	if e.Value != nil {
		d, ok := e.Value.day()
		if !ok {
			return 0, false, &Error{Where: where + "/ValDt", Message: refusalText("%q is not a date", e.Value.text())}
		}
		valueOn = &d
	}
	domain := e.BankTx.Domain
	bankCode := strings.TrimSpace(e.BankTx.Proprietary)
	if domain.Code != "" {
		bankCode = strings.TrimSpace(domain.Code) + "/" + strings.TrimSpace(domain.Family.Code) + "/" + strings.TrimSpace(domain.Family.SubCode)
	}
	bankCode, err := reference(where+"/BkTxCd", bankCode, 35)
	if err != nil {
		return 0, false, err
	}
	reversal := strings.TrimSpace(e.Reversal) == "true" || strings.TrimSpace(e.Reversal) == "1" ||
		(strings.TrimSpace(domain.Code) == "PMNT" && strings.TrimSpace(domain.Family.SubCode) == "RRTN" &&
			(strings.TrimSpace(domain.Family.Code) == "ICDT" || strings.TrimSpace(domain.Family.Code) == "RCDT"))

	lines, err := p.lines(where, e, direction, amount)
	if err != nil {
		return 0, false, err
	}
	for j, l := range lines {
		switch {
		case !booked:
			p.file.Ignored[IgnoredNotBooked]++
			continue
		case direction == "DBIT" && !reversal:
			p.file.Ignored[IgnoredDebit]++
			continue
		case l.amount == 0:
			p.file.Ignored[IgnoredZeroAmount]++
			continue
		}
		if len(p.file.Transactions) == MaxTransactions {
			return 0, false, &Error{Where: l.where, Message: fmt.Sprintf("more than %d transactions in one file", MaxTransactions)}
		}
		tx := Transaction{
			LineRef:        notification + "/" + strconv.Itoa(i) + "/" + strconv.Itoa(j+1),
			Account:        account,
			Debit:          direction == "DBIT",
			BookedOn:       bookedOn,
			AmountMinor:    l.amount,
			Currency:       "NOK",
			RemittanceText: cut(text(e.AddtlInfo), camtRemittanceTextSize),
			BankCode:       bankCode,
		}
		if valueOn != nil {
			v := *valueOn
			tx.ValueOn = &v
		}
		if l.tx != nil {
			if err := p.details(l.where, l.tx, &tx); err != nil {
				return 0, false, err
			}
		}
		p.file.Transactions = append(p.file.Transactions, tx)
	}
	return amount, direction == "DBIT", nil
}

// lines are an entry's TxDtls, each with its amount, checked against the
// entry: their count against each batch's NbOfTxs, their sum against its
// amount. An entry without TxDtls is one line of its own amount.
func (p *camtParser) lines(where string, e *camtEntry, direction string, amount int64) ([]camtLine, error) {
	var lines []camtLine
	var sum int64
	for _, details := range e.Details {
		if details.Batch != nil && len(details.Txs) > 0 {
			if number := strings.TrimSpace(details.Batch.Number); number != "" {
				if n, err := strconv.Atoi(number); err != nil || n != len(details.Txs) {
					return nil, &Error{Where: where + "/NtryDtls/Btch/NbOfTxs", Message: refusalText(
						"the batch counts %s transactions; its details hold %d TxDtls", number, len(details.Txs))}
				}
			}
		}
		for t := range details.Txs {
			tx := &details.Txs[t]
			l := camtLine{where: where + "/NtryDtls/TxDtls[" + strconv.Itoa(len(lines)+1) + "]", tx: tx}
			var err error
			if l.amount, err = p.txAmount(l.where, tx, direction); err != nil {
				return nil, err
			}
			sum += l.amount
			lines = append(lines, l)
			if sum > maxAmountMinor {
				break // more than any entry's amount: refused below
			}
		}
		if sum > maxAmountMinor {
			break
		}
	}
	if len(lines) == 0 {
		return []camtLine{{where: where, amount: amount}}, nil
	}
	if sum != amount {
		return nil, &Error{Where: where, Message: fmt.Sprintf("the entry's TxDtls sum to %s; its Amt is %s",
			signedAmount(sum), signedAmount(amount))}
	}
	return lines, nil
}

// txAmount is a TxDtls's amount: AmtDtls/TxAmt/Amt, or in .08 its own Amt
// (which, beside AmtDtls, must agree with it); in .08 its own CdtDbtInd,
// when written, must be its entry's.
func (p *camtParser) txAmount(where string, tx *camtTx, direction string) (int64, error) {
	var amount int64 = -1
	if tx.Amount != nil {
		a, msg := camtAmountMinor(tx.Amount.Value)
		if msg != "" {
			return 0, &Error{Where: where + "/AmtDtls/TxAmt/Amt", Message: msg}
		}
		amount = a
	}
	if p.v08 {
		if tx.Amount08 != nil {
			a, msg := camtAmountMinor(tx.Amount08.Value)
			if msg != "" {
				return 0, &Error{Where: where + "/Amt", Message: msg}
			}
			if amount >= 0 && a != amount {
				return 0, &Error{Where: where + "/Amt", Message: fmt.Sprintf("the transaction's Amt is %s; its AmtDtls/TxAmt/Amt is %s",
					signedAmount(a), signedAmount(amount))}
			}
			amount = a
		}
		if d := strings.TrimSpace(tx.Direction); d != "" && d != direction {
			return 0, &Error{Where: where + "/CdtDbtInd", Message: refusalText("the transaction is %s in an entry that is %s", d, direction)}
		}
	}
	if amount < 0 {
		return 0, &Error{Where: where, Message: "the transaction has no amount"}
	}
	return amount, nil
}

// details fills a transaction from its TxDtls: the KID — the structured
// creditor reference of type SCOR, whatever the bank transaction code
// says (R4 §3.2) — the unstructured lines joined, else the entry's
// additional information, the debtor, the archive reference. A SCOR
// reference longer than a KID can be is no KID: it leads the text as
// "SCOR <ref>", and the line reaches the queue as a payment without one.
func (p *camtParser) details(where string, tx *camtTx, out *Transaction) error {
	scor := ""
	for _, s := range tx.Structured {
		if strings.TrimSpace(s.Type) != "SCOR" {
			continue
		}
		if ref := text(s.Ref); len(ref) > kidSize {
			scor = ref
			break
		} else if ref != "" {
			out.KID = ref
			break
		}
	}
	var lines []string
	for _, l := range tx.Unstructured {
		if l = text(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		out.RemittanceText = strings.Join(lines, " ")
	}
	if scor != "" {
		out.RemittanceText = strings.TrimSpace("SCOR " + scor + " " + out.RemittanceText)
	}
	out.RemittanceText = cut(out.RemittanceText, camtRemittanceTextSize)
	name := tx.Debtor.Name02
	if p.v08 {
		name = tx.Debtor.Name08
	}
	out.DebtorName = cut(text(name), camtNameSize)
	account := tx.DebtorAccount.IBAN
	if account == "" {
		account = tx.DebtorAccount.Other
	}
	var err error
	if out.DebtorAccount, err = reference(where+"/RltdPties/DbtrAcct/Id", account, 34); err != nil {
		return err
	}
	out.ArchiveRef, err = reference(where+"/Refs/AcctSvcrRef", tx.ArchiveRef, 35)
	return err
}

// text is a free text of the file as it is stored: every control
// character a space, trimmed.
func text(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7F && r < 0xA0) {
			return ' '
		}
		return r
	}, s))
}

// cut is s cut to at most n characters.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// reference is a reference of the file — an identification, a KID, an
// account, a code — as text, refused rather than cut when it is longer
// than its column holds: a reference cut would be another reference. It
// is measured in characters, as varchar(n) measures it.
func reference(where, s string, n int) (string, error) {
	s = text(s)
	if c := utf8.RuneCountInString(s); c > n {
		return "", &Error{Where: where, Message: refusalText("%q is %d characters; at most %d are read", s, c, n)}
	}
	return s, nil
}

// camtAmountMinor reads a camt amount as øre: digits, a point and at most
// two decimals that are not zeros, never more than numeric(14,2) holds.
func camtAmountMinor(s string) (int64, string) {
	s = strings.TrimSpace(s)
	whole, frac, _ := strings.Cut(s, ".")
	if !allDigits(whole) || (frac != "" && !allDigits(frac)) || (strings.Contains(s, ".") && frac == "") {
		return 0, refusalText("%q is not an amount", s)
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, refusalText("the amount %s has more than two decimals", s)
		}
		frac = frac[:2]
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole = strings.TrimLeft(whole, "0")
	if len(whole) > 12 {
		return 0, refusalText("the amount %s is more than an amount can be", s)
	}
	n, _ := strconv.ParseInt(whole+frac, 10, 64)
	return n, ""
}

// signedAmount is øre as a message writes an amount.
func signedAmount(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

// day is a date element's day, UTC midnight: Dt as an ISO date, or DtTm's
// date as the bank wrote it, in its own offset.
func (d *camtDate) day() (time.Time, bool) {
	if s := strings.TrimSpace(d.Date); s != "" {
		t, err := time.Parse("2006-01-02", s)
		return t, err == nil
	}
	t, ok := camtDateTime(strings.TrimSpace(d.DateTime))
	return dateOf(t), ok
}

func (d *camtDate) text() string {
	if d.Date != "" {
		return strings.TrimSpace(d.Date)
	}
	return strings.TrimSpace(d.DateTime)
}

// camtDateTime reads an ISO date and time, with or without its offset and
// its fraction of a second.
func camtDateTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
