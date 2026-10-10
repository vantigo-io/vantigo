package invoices

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is a reminder letter's PDF (invoices payments and reminders
// design D10): its model — every word it prints, in order, built from the
// letter's own row and its invoice's snapshots — and the renderer, the
// invoice PDF's maroto layout and font (pdf.go). The model is a function of
// the row: the facts written when the letter was sent or printed, the
// invoice's snapshots, and the credit notes issued by the letter's day, so
// a row renders the same bytes every time — the creation date is the
// letter's sent_on, pdf.go's init fixes the rest (plan reading 15) — and a
// print batch can render it again. What the letter says:
//
//   - the seller and the buyer, the letter's day, the invoice it concerns —
//     number, issue date, due date — and the deadline;
//   - the claim with every amount apart (R13): the invoice's total, credited,
//     paid, the principal open; the earlier fees and compensation still
//     outstanding (charges_earlier), worded as a credit when the charges paid
//     exceed them (plan reading 55); this letter's fee or compensation; the
//     interest accrued to its day with its rates, days and base and its
//     from-date, what of it is waived and what is paid; the amount to pay;
//   - the account and the KID, or the invoice number to quote without one;
//   - the sentences the law asks for (remindertext.go).

// reminderModel is every word a letter prints, in the order it prints them,
// and its mail's subject, cover and attachment name.
type reminderModel struct {
	title         string
	seller, buyer []string
	meta          [][2]string
	intro         string
	amountHeader  string
	// amounts are the claim's rows, each a label and an amount; an amount
	// left empty is a detail row (an interest segment).
	amounts   [][2]string
	total     [2]string
	payBy     string
	payment   [][2]string
	sentences []string
	created   time.Time

	subject, cover, fileName string
}

// reminderModelBuilt, when a test sets it (export_test.go), is told every
// letter laid out: its id and its model. nil in production.
var reminderModelBuilt func(reminderID int64, m reminderModel)

// errLetterWithoutFacts is a letter rendered before its facts were written:
// a bug of the caller, never a letter.
var errLetterWithoutFacts = errors.New("invoices: a letter without facts has no PDF")

// reminderModelOf is letter r's model: r carries its facts; inv is its
// invoice; credited is what the invoice's issued credit notes took off it
// by r's day (CreditedOn).
func reminderModelOf(r store.InvoicesReminder, inv store.InvoicesInvoice, credited *big.Rat) (reminderModel, error) {
	if !r.SentOn.Valid || !r.Deadline.Valid || r.FeeKind == nil {
		return reminderModel{}, errLetterWithoutFacts
	}
	lang := strings.TrimSpace(r.Language)
	if lang != "en" {
		lang = "nb"
	}
	words := reminderText(lang, reminderrules.Level(r.Level), r.AnnouncesCollection)
	l := labels[lang]
	currency := inv.Currency
	if currency == "" {
		currency = "NOK"
	}
	number := int64(0)
	if inv.Number != nil {
		number = *inv.Number
	}
	day := func(d time.Time) string { return formatDate(d, lang) }
	amount := func(v *big.Rat) string { return money(v, lang) }
	sentOn, deadline := utcDay(r.SentOn.Time), utcDay(r.Deadline.Time)

	read := func(n numericField) (*big.Rat, error) {
		v, err := ratOrNil(n.value)
		if err != nil {
			return nil, fmt.Errorf("invoices: read letter %d's %s: %w", r.ID, n.name, err)
		}
		if v == nil {
			v = new(big.Rat)
		}
		return v, nil
	}
	f := map[string]*big.Rat{}
	for _, n := range []numericField{
		{"principal_open", r.PrincipalOpen}, {"fee", r.Fee}, {"compensation", r.Compensation},
		{"charges_earlier", r.ChargesEarlier}, {"interest", r.Interest}, {"interest_waived", r.InterestWaived},
		{"interest_paid", r.InterestPaid}, {"total", r.Total},
	} {
		v, err := read(n)
		if err != nil {
			return reminderModel{}, err
		}
		f[n.name] = v
	}
	gross, err := ratFromNumeric(inv.GrossTotal)
	if err != nil {
		return reminderModel{}, fmt.Errorf("invoices: read document %d's gross: %w", inv.ID, err)
	}
	paid := new(big.Rat).Sub(gross, credited)
	paid.Sub(paid, f["principal_open"])

	m := reminderModel{
		title: words.heading, seller: sellerLines(sellerParty(inv), l), buyer: buyerLines(buyerParty(inv), l),
		created: sentOn, amountHeader: fmt.Sprintf(words.amountHeader, currency),
	}
	m.meta = [][2]string{{words.date, day(sentOn)}, {words.invoiceNumber, fmt.Sprint(number)}}
	if inv.IssueDate.Valid {
		m.meta = append(m.meta, [2]string{words.invoiceDate, day(inv.IssueDate.Time)})
	}
	due := ""
	if inv.DueDate.Valid {
		due = day(inv.DueDate.Time)
		m.meta = append(m.meta, [2]string{words.dueDate, due})
	}
	m.meta = append(m.meta, [2]string{words.deadline, day(deadline)})
	m.intro = fmt.Sprintf(words.intro, number, due)

	m.amounts = [][2]string{
		{words.invoiceTotal, amount(gross)}, {words.credited, amount(credited)}, {words.paid, amount(paid)},
		{words.principalOpen, amount(f["principal_open"])},
	}
	if earlier := f["charges_earlier"]; earlier.Sign() < 0 {
		// Charge payments beyond the earlier charges pay this letter's own
		// fee or compensation: a credit, worded as one (plan reading 55).
		m.amounts = append(m.amounts, [2]string{words.chargesCredit, amount(earlier)})
	} else {
		m.amounts = append(m.amounts, [2]string{words.chargesEarlier, amount(earlier)})
	}
	switch reminderrules.FeeKind(*r.FeeKind) {
	case reminderrules.FeeReminder:
		m.amounts = append(m.amounts, [2]string{words.fee, amount(f["fee"])})
	case reminderrules.FeeCompensation:
		m.amounts = append(m.amounts, [2]string{words.compensation, amount(f["compensation"])})
	}
	if interest := f["interest"]; interest.Sign() > 0 {
		from := ""
		if r.InterestFrom.Valid {
			from = day(r.InterestFrom.Time)
		}
		m.amounts = append(m.amounts, [2]string{fmt.Sprintf(words.interestFrom, from), amount(interest)})
		var segs []interestSegment
		if len(r.InterestSegments) > 0 {
			if err := json.Unmarshal(r.InterestSegments, &segs); err != nil {
				return reminderModel{}, fmt.Errorf("invoices: read letter %d's interest segments: %w", r.ID, err)
			}
		}
		for _, s := range segs {
			seg, err := segmentWords(s, words.interestSegment, lang)
			if err != nil {
				return reminderModel{}, fmt.Errorf("invoices: read letter %d's interest segments: %w", r.ID, err)
			}
			m.amounts = append(m.amounts, [2]string{seg, ""})
		}
	}
	if waived := f["interest_waived"]; waived.Sign() > 0 {
		m.amounts = append(m.amounts, [2]string{words.interestWaived, amount(new(big.Rat).Neg(waived))})
	}
	if paidInterest := f["interest_paid"]; paidInterest.Sign() > 0 {
		m.amounts = append(m.amounts, [2]string{words.interestPaid, amount(new(big.Rat).Neg(paidInterest))})
	}
	total := currency + " " + amount(f["total"])
	m.total = [2]string{words.total, total}
	m.payBy = fmt.Sprintf(words.payBy, total, day(deadline))

	m.payment = [][2]string{{words.account, deref(inv.SellerBankAccount)}}
	if inv.Kid != nil {
		m.payment = append(m.payment, [2]string{words.kid, *inv.Kid})
	}
	if iban := deref(inv.SellerIban); iban != "" {
		m.payment = append(m.payment, [2]string{words.iban, iban})
	}
	if bic := deref(inv.SellerBic); bic != "" {
		m.payment = append(m.payment, [2]string{words.bic, bic})
	}
	quote := fmt.Sprintf(words.quoteNumber, number)
	if inv.Kid != nil {
		quote = fmt.Sprintf(words.quoteKID, *inv.Kid)
	}
	m.sentences = []string{quote}
	switch {
	case r.Level == string(reminderrules.LevelNotice):
		m.sentences = append(m.sentences, fmt.Sprintf(words.notice, day(deadline)), words.noticeCosts)
	case words.announcement != "":
		m.sentences = append(m.sentences, fmt.Sprintf(words.announcement, day(deadline)))
	}
	m.sentences = append(m.sentences, words.paidMeanwhile, words.objection)

	seller := deref(inv.SellerLegalName)
	m.subject = fmt.Sprintf(words.subject, number)
	m.cover = strings.Join([]string{
		words.greeting,
		fmt.Sprintf(words.coverIntro, number, seller) + " " + fmt.Sprintf(words.cover, total, day(deadline)),
		words.coverMoot,
		words.closing + "\n" + seller,
	}, "\n\n") + "\n"
	m.fileName = letterFileName(r, inv)
	if reminderModelBuilt != nil {
		reminderModelBuilt(r.ID, m)
	}
	return m, nil
}

// numericField is a nullable numeric column of a letter and its name.
type numericField struct {
	name  string
	value pgtype.Numeric
}

// segmentWords is one stored interest segment as the letter prints it.
func segmentWords(s interestSegment, format, lang string) (string, error) {
	from, err := time.Parse(time.DateOnly, s.From)
	if err != nil {
		return "", err
	}
	to, err := time.Parse(time.DateOnly, s.To)
	if err != nil {
		return "", err
	}
	rate, ok := new(big.Rat).SetString(s.Rate)
	base, ok2 := new(big.Rat).SetString(s.Base)
	if !ok || !ok2 {
		return "", errors.New("not a decimal")
	}
	return fmt.Sprintf(format, formatDecimal(rate, 2, 4, lang), formatDate(from, lang), formatDate(to, lang), money(base, lang)), nil
}

// text is the model as lines of text, in the order it prints: what the
// letter goldens hold (plan reading 15), the mail's subject, attachment and
// cover after it.
func (m reminderModel) text() []string {
	out := []string{"# " + m.title}
	out = append(out, "## seller")
	out = append(out, m.seller...)
	out = append(out, "## buyer")
	out = append(out, m.buyer...)
	out = append(out, "## meta")
	for _, kv := range m.meta {
		out = append(out, kv[0]+": "+kv[1])
	}
	out = append(out, "## claim", m.intro, m.amountHeader)
	for _, kv := range m.amounts {
		if kv[1] == "" {
			out = append(out, "  "+kv[0])
			continue
		}
		out = append(out, kv[0]+": "+kv[1])
	}
	out = append(out, m.total[0]+": "+m.total[1], "## payment", m.payBy)
	for _, kv := range m.payment {
		out = append(out, kv[0]+": "+kv[1])
	}
	out = append(out, m.sentences...)
	out = append(out, "## mail", "Subject: "+m.subject, "Attachment: "+m.fileName)
	out = append(out, strings.Split(strings.TrimRight(m.cover, "\n"), "\n")...)
	return out
}

// renderReminderPDF lays a letter's model out on A4 and answers the bytes:
// the invoice PDF's font and margins, its creation date the letter's day
// (pdf.go's layoutPDF's shape), so a row renders the same bytes every time.
func renderReminderPDF(m reminderModel) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(fontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(fontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("invoices: load the PDF font: %w", err)
	}
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: fontFamily, Size: 9}).
		WithSequentialMode().
		WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).
		WithCreationDate(m.created).
		WithTitle(m.title, true).
		Build()
	doc := maroto.New(cfg)
	bold := props.Text{Style: fontstyle.Bold}
	right := props.Text{Align: align.Right}
	boldRight := props.Text{Style: fontstyle.Bold, Align: align.Right}

	doc.AddRows(text.NewRow(12, m.title, props.Text{Style: fontstyle.Bold, Size: 18}))
	for i := range max(len(m.seller), len(m.buyer)) {
		cell := func(lines []string) string {
			if i < len(lines) {
				return lines[i]
			}
			return ""
		}
		style := props.Text{}
		if i == 0 {
			style = bold
		}
		doc.AddRow(4.5, text.NewCol(6, cell(m.seller), style), text.NewCol(6, cell(m.buyer), style))
	}
	doc.AddRows(line.NewRow(4))
	for _, kv := range m.meta {
		doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	doc.AddRows(line.NewRow(4))
	doc.AddAutoRow(text.NewCol(12, m.intro, props.Text{Bottom: 2}))
	doc.AddRow(5, col.New(6), text.NewCol(6, m.amountHeader, boldRight))
	for _, kv := range m.amounts {
		if kv[1] == "" {
			doc.AddAutoRow(col.New(1), text.NewCol(11, kv[0], props.Text{Size: 8}))
			continue
		}
		doc.AddAutoRow(text.NewCol(8, kv[0]), text.NewCol(4, kv[1], right))
	}
	doc.AddRows(line.NewRow(3))
	doc.AddRow(6, text.NewCol(8, m.total[0], bold), text.NewCol(4, m.total[1], boldRight))
	doc.AddRows(line.NewRow(4))
	doc.AddAutoRow(text.NewCol(12, m.payBy, props.Text{Style: fontstyle.Bold, Bottom: 2}))
	for _, kv := range m.payment {
		doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	doc.AddRows(line.NewRow(4))
	for _, s := range m.sentences {
		doc.AddAutoRow(text.NewCol(12, s, props.Text{Bottom: 1.5}))
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("invoices: render the letter's PDF: %w", err)
	}
	return out.GetBytes(), nil
}

// reminderKey is where a letter's PDF is stored (plan reading 35): beside
// its invoice's documents, named by the letter, its day and the hash of its
// bytes, so a different render — a same-day retry after the principal
// moved, a reprint for the same day — never lands on a key that holds
// another object, and Exists-before-Put never records a new hash over an
// old object. The module deletes no object.
func reminderKey(invoiceID, reminderID int64, sentOn time.Time, sha string) string {
	return fmt.Sprintf("reminders/%d/%d-%s-%s.pdf", invoiceID, reminderID, sentOn.Format(time.DateOnly), sha)
}
