package accounting

// The VAT specification (A21, R4, R10): per tax code, rate and specification
// line, the base and the VAT — output, calculated (reverse charge's tax_reverse),
// deducted, not deducted, and a loss's tapPåKrav — computed from the postings
// by voucher date and never stored; with the reconciliation of the VAT
// accounts' movement against the VAT the return counts, and the return laid
// out from it (internal/accounting/mva).
//
// A posting's part (vatPart):
//
//   - base: a tax code's base line (tax_role base);
//   - output: a tax line of an output code;
//   - calculated: a reverse charge's calculated side (tax_reverse);
//   - deducted: a tax line of an input code on a VAT account — a tax code's
//     VAT or counter account;
//   - not_deducted: a tax line of an input code on another account, a cost
//     (non-deductible VAT, the reverse-charge codes without deduction);
//   - loss and loss_base: a write-off's VAT taken back and its share on
//     bad_debt (tax_specification tapPåKrav);
//   - untaxed: no tax code, on a VAT account.
//
// The return counts output, calculated, deducted and loss of the reported
// codes. The VAT accounts reconcile when, account by account, their movement
// is what the return counts; the settlement voucher then moves exactly that.

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/mva"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// The parts of the specification a posting belongs to.
const (
	partBase        = "base"
	partOutput      = "output"
	partCalculated  = "calculated"
	partDeducted    = "deducted"
	partNotDeducted = "not_deducted"
	partLoss        = "loss"
	partLossBase    = "loss_base"
	partUntaxed     = "untaxed"
	partOther       = "other"
)

// vatPart is a posting group's part; VatPostings' CASE says the same in SQL.
func vatPart(r store.VatPeriodGroupsRow) string {
	switch {
	case r.TaxCodeID == 0:
		return partUntaxed
	case r.TaxSpecification == mva.TapPaaKrav && r.TaxRole == "base":
		return partLossBase
	case r.TaxSpecification == mva.TapPaaKrav:
		return partLoss
	case r.TaxRole == "base":
		return partBase
	case r.TaxRole == roleTaxReverse:
		return partCalculated
	case r.TaxDirection == "output":
		return partOutput
	case r.TaxDirection == "input" && r.OnVatAccount:
		return partDeducted
	case r.TaxDirection == "input":
		return partNotDeducted
	}
	return partOther
}

// counted reports whether the return counts a posting of part on a code that
// is reported.
func counted(part string, reported bool) bool {
	return reported && (part == partOutput || part == partCalculated || part == partDeducted || part == partLoss)
}

// salesSide reports whether a code's base is a sale's, credited: its base is
// shown with the sign turned.
func salesSide(direction, code string) bool { return direction == "output" || code == "7" }

// figureKey tells one figure from another: a code at a rate, a loss apart.
type figureKey struct {
	taxCodeID     int32
	rate, special string
}

// figure is one code's figures as they are summed, in the return's sign.
type figure struct {
	mva.Figure
	reported     bool
	notDeducted  *big.Rat
	postings     int32
	groupOrder   int
	order        int
	lossOfOutput bool
}

// reconAccount is one account's line of the reconciliation.
type reconAccount struct {
	id                int32
	number, name      string
	vatAccount        bool
	movement, counted *big.Rat
}

// specification is a period's specification as computed.
type specification struct {
	from, to time.Time
	figures  []*figure
	accounts []*reconAccount
	// movement is the VAT accounts' movement, counted what the return counts,
	// both in the ledger's sign.
	movement, counted *big.Rat
	ret               mva.Return
}

// computeSpecification reads the period's postings and sums them (A21).
func computeSpecification(ctx context.Context, q *store.Queries, from, to time.Time) (*specification, error) {
	rows, err := q.VatPeriodGroups(ctx, store.VatPeriodGroupsParams{FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	spec := &specification{from: from, to: to, movement: new(big.Rat), counted: new(big.Rat)}
	figures := map[figureKey]*figure{}
	accounts := map[int32]*reconAccount{}
	for _, r := range rows {
		amount := mustRat(r.Amount)
		part := vatPart(r)
		isCounted := counted(part, r.Reported)
		if r.OnVatAccount || isCounted {
			a, ok := accounts[r.AccountID]
			if !ok {
				a = &reconAccount{id: r.AccountID, number: r.AccountNumber, name: r.AccountName, vatAccount: r.OnVatAccount,
					movement: new(big.Rat), counted: new(big.Rat)}
				accounts[r.AccountID] = a
			}
			if r.OnVatAccount {
				a.movement.Add(a.movement, amount)
				spec.movement.Add(spec.movement, amount)
			}
			if isCounted {
				a.counted.Add(a.counted, amount)
				spec.counted.Add(spec.counted, amount)
			}
		}
		if r.TaxCodeID == 0 {
			continue
		}
		key := figureKey{taxCodeID: r.TaxCodeID, rate: r.RatePercent}
		if part == partLoss || part == partLossBase {
			key.special = mva.TapPaaKrav
		}
		f, ok := figures[key]
		if !ok {
			f = &figure{Figure: mva.Figure{TaxCodeID: r.TaxCodeID, Code: r.TaxCode, Name: r.TaxName, Direction: r.TaxDirection,
				ReverseCharge: r.ReverseCharge, RatePercent: r.RatePercent, Specification: key.special, Base: new(big.Rat),
				Output: new(big.Rat), Calculated: new(big.Rat), Deducted: new(big.Rat), Loss: new(big.Rat)},
				reported: r.Reported, notDeducted: new(big.Rat)}
			f.groupOrder, f.order = mva.Order(r.TaxCode)
			if key.special != "" {
				f.groupOrder, f.order = mva.Order(mva.LossCode(r.TaxCode))
				f.lossOfOutput = true
			}
			figures[key] = f
		}
		f.postings += r.Postings
		switch part {
		case partBase:
			if salesSide(r.TaxDirection, r.TaxCode) {
				f.Base.Sub(f.Base, amount)
			} else {
				f.Base.Add(f.Base, amount)
			}
		case partLossBase:
			f.Base.Add(f.Base, amount)
		case partOutput:
			f.Output.Sub(f.Output, amount)
		case partCalculated:
			f.Calculated.Sub(f.Calculated, amount)
		case partDeducted:
			f.Deducted.Sub(f.Deducted, amount)
		case partLoss:
			f.Loss.Sub(f.Loss, amount)
		case partNotDeducted:
			f.notDeducted.Add(f.notDeducted, amount)
		}
	}
	for _, f := range figures {
		spec.figures = append(spec.figures, f)
	}
	slices.SortFunc(spec.figures, func(a, b *figure) int {
		for _, d := range []int{a.groupOrder - b.groupOrder, a.order - b.order, boolOrder(a.lossOfOutput) - boolOrder(b.lossOfOutput)} {
			if d != 0 {
				return d
			}
		}
		if a.RatePercent != b.RatePercent {
			if a.RatePercent < b.RatePercent {
				return -1
			}
			return 1
		}
		return int(a.TaxCodeID - b.TaxCodeID)
	})
	for _, a := range accounts {
		spec.accounts = append(spec.accounts, a)
	}
	slices.SortFunc(spec.accounts, func(a, b *reconAccount) int {
		if a.number < b.number {
			return -1
		}
		if a.number > b.number {
			return 1
		}
		return 0
	})
	var reported []mva.Figure
	for _, f := range spec.figures {
		if f.reported {
			reported = append(reported, f.Figure)
		}
	}
	spec.ret = mva.Build(reported)
	return spec, nil
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

// reconciled reports whether every account's movement is what the return
// counts on it: then the settlement voucher moves exactly the return's VAT.
func (s *specification) reconciled() bool {
	for _, a := range s.accounts {
		if a.vatAccount && a.movement.Cmp(a.counted) != 0 {
			return false
		}
		if !a.vatAccount && a.counted.Sign() != 0 {
			return false
		}
	}
	return true
}

// settlement is the settlement voucher of term t (A22, rule vat.settlement
// v1): each VAT account's movement in the term moved to vat_settlement; on
// vat_settlement the return's total in whole kroner, credited when it is to
// pay; and on the rounding account the øre the return cut. No lines when
// nothing moved.
func (s *specification) settlement(t vatTerm, language string, termID int64, closings int32, now time.Time) Voucher {
	v := Voucher{Kind: "vat_settlement", Date: t.End, Description: fmt.Sprintf(vatWord(language, "settlement"), t.label()),
		Source: &Source{Module: moduleName, Kind: "vat_settlement", ID: termID, Part: fmt.Sprint(closings)},
		Rule:   ruleVatSettlement, RuleVersion: 1, At: now}
	exact := new(big.Rat)
	for _, a := range s.accounts {
		if !a.vatAccount || a.movement.Sign() == 0 {
			continue
		}
		v.Lines = append(v.Lines, Line{AccountID: a.id, Amount: neg(a.movement), Text: a.number + " " + a.name})
		exact.Sub(exact, a.movement)
	}
	if len(v.Lines) == 0 {
		return Voucher{}
	}
	total := new(big.Rat).SetInt(s.ret.Total)
	v.Lines = append(v.Lines, Line{Role: "vat_settlement", Amount: neg(total)})
	if diff := sub(total, exact); diff.Sign() != 0 {
		v.Lines = append(v.Lines, Line{Role: "rounding", Amount: diff})
	}
	return v
}

// view is the specification as the contract answers it, with the postings
// that keep it from reconciling.
func (s *specification) view(ctx context.Context, q *store.Queries, producedAt time.Time) (gen.AccountingVatSpecification, error) {
	out := gen.AccountingVatSpecification{From: apiDate(s.from), To: apiDate(s.to), ProducedAt: producedAt,
		Codes: []gen.AccountingVatCodeFigures{}, NotReported: []gen.AccountingVatCodeFigures{}}
	for _, f := range s.figures {
		vat := add(add(f.Output, f.Calculated), add(f.Deducted, f.Loss))
		c := gen.AccountingVatCodeFigures{TaxCodeId: f.TaxCodeID, Code: f.Code, Name: f.Name,
			Direction: gen.AccountingTaxDirection(f.Direction), ReverseCharge: f.ReverseCharge, Reported: f.reported,
			Group: gen.AccountingVatGroup(mva.GroupOf(f.Code)), Base: money(f.Base), OutputVat: money(f.Output),
			CalculatedVat: money(f.Calculated), DeductedVat: money(f.Deducted), NotDeductedVat: money(f.notDeducted),
			LossVat: money(f.Loss), Vat: money(vat), Postings: f.postings}
		if f.lossOfOutput {
			c.Group = gen.AccountingVatGroup(mva.GroupOf(mva.LossCode(f.Code)))
		}
		if f.RatePercent != "" {
			c.RatePercent = ptr(f.RatePercent)
		}
		if f.Specification != "" {
			c.Specification = ptr(f.Specification)
		}
		if f.reported {
			out.Codes = append(out.Codes, c)
		} else {
			c.Group = gen.NotReported
			out.NotReported = append(out.NotReported, c)
		}
	}
	r := gen.AccountingVatReconciliation{Accounts: []gen.AccountingVatReconciliationAccount{}, Movement: money(s.movement),
		Counted: money(s.counted), Difference: money(sub(s.movement, s.counted)), Reconciled: s.reconciled(),
		Postings: []gen.AccountingVatPosting{}}
	for _, a := range s.accounts {
		r.Accounts = append(r.Accounts, gen.AccountingVatReconciliationAccount{AccountId: a.id, AccountNumber: a.number,
			AccountName: a.name, VatAccount: a.vatAccount, Movement: money(a.movement), Counted: money(a.counted),
			Difference: money(sub(a.movement, a.counted))})
	}
	if !r.Reconciled {
		rows, err := q.VatPostings(ctx, store.VatPostingsParams{FromDate: s.from, ToDate: s.to, Unreconciled: true, RowLimit: 200})
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			r.Postings = append(r.Postings, vatPostingView(row))
		}
	}
	out.Reconciliation = r
	return out, nil
}

// vatPostingView is a drill-down row as the contract answers it.
func vatPostingView(r store.VatPostingsRow) gen.AccountingVatPosting {
	p := gen.AccountingVatPosting{AccountId: r.AccountID, AccountNumber: r.AccountNumber, AccountName: r.AccountName, Amount: r.Amount,
		Description: r.Description, LineNo: r.LineNo, Part: gen.AccountingVatPart(r.Part), TaxCode: r.TaxCode, TaxCodeId: r.TaxCodeID,
		TaxSpecification: r.TaxSpecification, VoucherDate: apiDate(r.VoucherDate), VoucherDescription: r.VoucherDescription,
		Voucher: gen.AccountingVoucherRef{Id: r.VoucherID, Display: display(r.FiscalYear, r.Number)}}
	if r.TaxBase != "" {
		p.TaxBase = ptr(r.TaxBase)
	}
	if r.TaxRole != nil {
		p.TaxRole = ptr(gen.AccountingTaxRole(*r.TaxRole))
	}
	return p
}

// vatPeriod is a period's days, held to from on or before to.
func vatPeriod(from, to time.Time) error {
	if from.After(to) {
		return invalidField("The specification cannot be made", "from", codePeriodInvalid, "from is on or before to.")
	}
	return nil
}

// GetAccountingVatSpecification is a period's VAT specification (A21).
func (s *server) GetAccountingVatSpecification(ctx context.Context, req gen.GetAccountingVatSpecificationRequestObject) (gen.GetAccountingVatSpecificationResponseObject, error) {
	from, to := day(req.Params.From), day(req.Params.To)
	if err := vatPeriod(from, to); err != nil {
		return nil, err
	}
	var out gen.AccountingVatSpecification
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		spec, err := computeSpecification(ctx, q, from, to)
		if err != nil {
			return err
		}
		out, err = spec.view(ctx, q, s.now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingVatSpecification200JSONResponse(out), nil
}

// ListAccountingVatPostings is the postings behind a figure (A21).
func (s *server) ListAccountingVatPostings(ctx context.Context, req gen.ListAccountingVatPostingsRequestObject) (gen.ListAccountingVatPostingsResponseObject, error) {
	p := req.Params
	from, to := day(p.From), day(p.To)
	if err := vatPeriod(from, to); err != nil {
		return nil, err
	}
	offset, limit := int32(0), int32(100)
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	if p.Limit != nil && *p.Limit > 0 {
		limit = min(*p.Limit, 500)
	}
	params := store.VatPostingsParams{FromDate: from, ToDate: to, TaxCodeID: p.TaxCodeId, Unreconciled: deref(p.Unreconciled),
		RowOffset: offset, RowLimit: limit}
	if p.Part != nil {
		params.Part = ptr(string(*p.Part))
	}
	rows, err := store.New(s.deps.Pool).VatPostings(ctx, params)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingVatPostings200JSONResponse{Items: []gen.AccountingVatPosting{}}
	for _, r := range rows {
		out.Items = append(out.Items, vatPostingView(r))
		out.Total = r.Total
	}
	if len(rows) == 0 && offset > 0 {
		// A page past the end still says how many there are.
		all, err := store.New(s.deps.Pool).VatPostings(ctx, store.VatPostingsParams{FromDate: from, ToDate: to, TaxCodeID: p.TaxCodeId,
			Part: params.Part, Unreconciled: params.Unreconciled, RowLimit: 1})
		if err != nil {
			return nil, err
		}
		if len(all) > 0 {
			out.Total = all[0].Total
		}
	}
	return out, nil
}

// vatReport is the specification as a report (A11): the reported codes in
// the return's order, then those it leaves out, then the reconciliation's
// total.
func (t *table) vatReport(ctx context.Context, q *store.Queries, in reportInput) error {
	spec, err := computeSpecification(ctx, q, in.from, in.to)
	if err != nil {
		return err
	}
	t.column("code", t.w("taxCode"), gen.Text)
	t.column("name", t.w("name"), gen.Text)
	t.column("rate", t.w("vatRate"), gen.Text)
	t.column("base", t.w("vatBase"), gen.Amount)
	t.column("output", t.w("vatOutput"), gen.Amount)
	t.column("calculated", t.w("vatCalculated"), gen.Amount)
	t.column("deducted", t.w("vatDeducted"), gen.Amount)
	t.column("loss", t.w("vatLoss"), gen.Amount)
	t.column("notDeducted", t.w("vatNotDeducted"), gen.Amount)
	t.column("vat", t.w("vatTotal"), gen.Amount)
	section := func(reported bool, heading string) {
		first := true
		for _, f := range spec.figures {
			if f.reported != reported {
				continue
			}
			if first {
				t.row(gen.Heading, 0, map[string]string{"code": heading})
				first = false
			}
			name := f.Name
			if f.Specification != "" {
				name += " · " + f.Specification
			}
			vat := add(add(f.Output, f.Calculated), add(f.Deducted, f.Loss))
			row := t.row(gen.Line, 1, map[string]string{"code": f.Code, "name": name, "rate": f.RatePercent, "base": money(f.Base),
				"output": money(f.Output), "calculated": money(f.Calculated), "deducted": money(f.Deducted), "loss": money(f.Loss),
				"notDeducted": money(f.notDeducted), "vat": money(vat)})
			row.TaxCodeId = ptr(f.TaxCodeID)
		}
	}
	section(true, t.w("vatReported"))
	section(false, t.w("vatNotReported"))
	t.row(gen.Total, 0, map[string]string{"code": t.w("total"), "vat": money(neg(spec.counted))})
	t.row(gen.Subtotal, 0, map[string]string{"code": t.w("vatMovement"), "vat": money(neg(spec.movement))})
	return nil
}
