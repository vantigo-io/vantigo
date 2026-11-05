package orders

// The quote template operations (orders design O23), and applying a template
// to a quote. A template holds a name, the texts, a validity and the
// discount's visibility, and lines: product lines with a variant, a quantity,
// an optional flag and an optional price override; sections; notes. Its lines
// are orders.lines with owner_kind template, priced only when applied.

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// productDiscontinued is Products' status of a product no longer sold: a
// template's line for it is skipped as archived (O23).
const productDiscontinued = "Discontinued"

// templateView is a template with its lines as the API answers it.
func templateView(t store.OrdersTemplate, lines []store.OrdersLine) gen.OrdersTemplate {
	out := gen.OrdersTemplate{
		Id: t.ID, Name: t.Name, IntroText: t.IntroText, ClosingText: t.ClosingText, ValidityDays: t.ValidityDays,
		DiscountVisible: t.DiscountVisible, Archived: t.Archived, Revision: t.Revision, UpdatedAt: t.UpdatedAt,
		Lines: []gen.OrdersTemplateLine{},
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, gen.OrdersTemplateLine{
			Id: l.ID, Position: l.Position, Kind: gen.OrdersTemplateLineKind(l.Kind), Description: l.Description,
			Name: l.Name, Optional: l.Optional, Quantity: wirePtr(ratOf(l.Quantity), quantityPlaces), Sku: l.Sku,
			UnitPrice: wirePtr(ratOf(l.UnitPrice), pricePlaces), VariantId: l.VariantID,
		})
	}
	return out
}

// readTemplate reads a template and its lines on the pool, found false when
// there is none.
func (s *server) readTemplate(ctx context.Context, id int64) (store.OrdersTemplate, []store.OrdersLine, bool, error) {
	q := store.New(s.deps.Pool)
	t, err := q.GetTemplate(ctx, id)
	if noRows(err) {
		return t, nil, false, nil
	}
	if err != nil {
		return t, nil, false, fmt.Errorf("orders: read template %d: %w", id, err)
	}
	lines, err := linesOf(ctx, q, ownerTemplate, id)
	return t, lines, true, err
}

// applyTemplateTexts fills a quote's empty texts from a template (O23), and
// its discount's visibility when asked.
func applyTemplateTexts(h *header, t store.OrdersTemplate, intro, closing, discount bool) {
	if intro && strings.TrimSpace(h.IntroText) == "" {
		h.IntroText = t.IntroText
	}
	if closing && strings.TrimSpace(h.ClosingText) == "" {
		h.ClosingText = t.ClosingText
	}
	if discount {
		h.DiscountVisible = t.DiscountVisible
	}
}

// templateLineInputs are a template's lines as a quote takes them: priced as
// O16 prices them in the quote's currency, a price override kept as the net
// it is, a variant gone or discontinued skipped.
func templateLineInputs(lines []store.OrdersLine) []lineInput {
	out := make([]lineInput, 0, len(lines))
	for _, l := range lines {
		in := lineInput{
			kind: l.Kind, variantID: l.VariantID, description: ptr(l.Description), optional: l.Optional,
			skippable: true, skipDiscontinued: true,
		}
		switch l.Kind {
		case kindProduct:
			if l.VariantID == nil {
				in.name, in.unit = l.Name, ptr(l.Unit)
			}
			in.quantity = wirePtr(ratOf(l.Quantity), quantityPlaces)
			if in.quantity == nil {
				in.quantity = ptr(1.0)
			}
			in.unitPriceNet = ratOf(l.UnitPrice)
		default:
			in.name = l.Name
		}
		out = append(out, in)
	}
	return out
}

// dropGone leaves out of in every skippable line whose variant the catalog no
// longer has — or, for a template's line, has discontinued — and names each
// in *skipped when skipped is not nil.
func dropGone(in []lineInput, r pricingReads, skipped *[]gen.OrdersSkippedLine) []lineInput {
	out := make([]lineInput, 0, len(in))
	for _, l := range in {
		if l.skippable && l.variantID != nil {
			v, ok := r.variants[*l.variantID]
			reason := gen.OrdersSkippedLineReason("")
			switch {
			case !ok:
				reason = gen.Deleted
			case l.skipDiscontinued && v.ProductStatus == productDiscontinued:
				reason = gen.Archived
			}
			if reason != "" {
				if skipped != nil {
					line := gen.OrdersSkippedLine{VariantId: *l.variantID, Reason: reason}
					if ok {
						line.Sku = textPtr(v.SKU)
					}
					*skipped = append(*skipped, line)
				}
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

// templateFields are a template's request judged.
type templateFields struct {
	name, intro, closing string
	validityDays         *int32
	discountVisible      bool
	archived             bool
	lines                []gen.OrdersTemplateLineInput
}

// readTemplateFields judges a template's fields into f.
func readTemplateFields(f *fieldErrors, b gen.OrdersTemplateInput) templateFields {
	out := templateFields{name: strings.TrimSpace(b.Name), validityDays: b.ValidityDays, discountVisible: true}
	if n := utf8.RuneCountInString(out.name); n == 0 || n > 200 {
		f.add("name", "A template's name is 1 to 200 characters")
	}
	if b.IntroText != nil {
		out.intro = *b.IntroText
	}
	if b.ClosingText != nil {
		out.closing = *b.ClosingText
	}
	if utf8.RuneCountInString(out.intro) > 4000 {
		f.add("introText", "A text is at most 4000 characters")
	}
	if utf8.RuneCountInString(out.closing) > 4000 {
		f.add("closingText", "A text is at most 4000 characters")
	}
	if out.validityDays != nil && (*out.validityDays < 1 || *out.validityDays > 365) {
		f.add("validityDays", "A validity is 1 to 365 days")
	}
	if b.DiscountVisible != nil {
		out.discountVisible = *b.DiscountVisible
	}
	if b.Archived != nil {
		out.archived = *b.Archived
	}
	if b.Lines != nil {
		out.lines = *b.Lines
	}
	if len(out.lines) > maxLines {
		f.addCoded("lines", codeTooManyLines, fmt.Sprintf("A template has at most %d lines", maxLines))
	}
	return out
}

// templateLines are a template's lines as stored, or every failing line: a
// variant the catalog has, a quantity, a price override and the texts.
func (s *server) templateLines(ctx context.Context, in []gen.OrdersTemplateLineInput, existing map[int64]store.OrdersLine) ([]pricedLine, error) {
	var ids []int32
	for _, l := range in {
		if l.VariantId != nil {
			ids = append(ids, *l.VariantId)
		}
	}
	variants, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return nil, err
	}
	var failures []lineFailure
	out := make([]pricedLine, 0, len(in))
	seen := map[int64]bool{}
	for i, l := range in {
		fail := func(code, detail string) {
			failures = append(failures, lineFailure{index: i, code: code, detail: detail})
		}
		p := pricedLine{kind: string(l.Kind), discount: new(big.Rat), optional: l.Optional != nil && *l.Optional}
		if l.Id != nil {
			if _, ok := existing[*l.Id]; !ok || seen[*l.Id] {
				fail(codeLineInvalid, fmt.Sprintf("Line %d is not one of this template's lines.", *l.Id))
				continue
			}
			seen[*l.Id] = true
			p.id = l.Id
		}
		if l.Description != nil {
			p.description = strings.TrimSpace(*l.Description)
		}
		if utf8.RuneCountInString(p.description) > 2000 {
			fail(codeLineInvalid, "The description is at most 2000 characters.")
			continue
		}
		if l.Name != nil {
			if utf8.RuneCountInString(*l.Name) > 255 {
				fail(codeLineInvalid, "The name is at most 255 characters.")
				continue
			}
			p.name = textPtr(*l.Name)
		}
		if p.kind != kindProduct {
			if p.optional {
				fail(codeOptionalNotAllowed, "Only a product line can be optional.")
				continue
			}
			p.computeAmounts()
			out = append(out, p)
			continue
		}
		if l.VariantId != nil {
			v, ok := variants[*l.VariantId]
			if !ok {
				fail(codeVariantNotFound, fmt.Sprintf("Variant %d is not in the catalog.", *l.VariantId))
				continue
			}
			p.variantID, p.sku, p.name, p.unit = &v.ID, textPtr(v.SKU), textPtr(v.ProductName), v.Unit
		} else if p.name == nil && p.description == "" {
			fail(codeLineInvalid, "A free-text line needs a name or a description.")
			continue
		}
		if l.Quantity != nil {
			q, msg := parseQuantity(*l.Quantity)
			if msg != "" {
				fail(codeQuantityInvalid, msg)
				continue
			}
			p.quantity = q
		}
		if l.UnitPrice != nil {
			v := *l.UnitPrice
			if !finite(v) || v < 0 || decimalPlaces(v) > entryPlaces || ratFromFloat(v).Cmp(maxPrice) > 0 {
				fail(codePriceInvalid, "A unit price is 0 or more, with at most four decimals.")
				continue
			}
			p.unitPrice, p.priceSource = ratFromFloat(v), ptr(priceManual)
		}
		p.computeAmounts()
		out = append(out, p)
	}
	if len(failures) > 0 {
		return nil, refuseLines(invalidTemplateTitle, failures)
	}
	return out, nil
}

// templateNameTaken is the 409 of a name another template has.
func templateNameTaken(name string) error {
	return conflict(codeTemplateNameTaken, "Template name taken", fmt.Sprintf("Another template is named %s.", name))
}

// ListQuoteTemplates List quote templates
// (GET /api/v1/orders/templates)
func (s *server) ListQuoteTemplates(ctx context.Context, req gen.ListQuoteTemplatesRequestObject) (gen.ListQuoteTemplatesResponseObject, error) {
	include := req.Params.IncludeArchived != nil && *req.Params.IncludeArchived
	out := gen.OrdersTemplateList{Items: []gen.OrdersTemplateSummary{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListTemplates(ctx, include)
		if err != nil {
			return fmt.Errorf("orders: list templates: %w", err)
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		counts, err := q.LineCounts(ctx, store.LineCountsParams{OwnerKind: ownerTemplate, OwnerIds: ids})
		if err != nil {
			return fmt.Errorf("orders: count the templates' lines: %w", err)
		}
		byOwner := map[int64]int32{}
		for _, c := range counts {
			byOwner[c.OwnerID] = c.Lines
		}
		for _, r := range rows {
			out.Items = append(out.Items, gen.OrdersTemplateSummary{
				Id: r.ID, Name: r.Name, Archived: r.Archived, LineCount: byOwner[r.ID], ValidityDays: r.ValidityDays, UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListQuoteTemplates200JSONResponse(out), nil
}

// GetQuoteTemplate Get a quote template
// (GET /api/v1/orders/templates/{id})
func (s *server) GetQuoteTemplate(ctx context.Context, req gen.GetQuoteTemplateRequestObject) (gen.GetQuoteTemplateResponseObject, error) {
	t, lines, found, err := s.readTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetQuoteTemplate404Response{}, nil
	}
	return gen.GetQuoteTemplate200JSONResponse(templateView(t, lines)), nil
}

// CreateQuoteTemplate Create a quote template
// (POST /api/v1/orders/templates)
func (s *server) CreateQuoteTemplate(ctx context.Context, req gen.CreateQuoteTemplateRequestObject) (gen.CreateQuoteTemplateResponseObject, error) {
	resp := answers[gen.CreateQuoteTemplateResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.CreateQuoteTemplateResponseObject {
			return gen.CreateQuoteTemplate400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.CreateQuoteTemplateResponseObject {
			return gen.CreateQuoteTemplate409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, templateInputSchema)
	in := readTemplateFields(&f, *req.Body)
	if f.failed() {
		return gen.CreateQuoteTemplate400ApplicationProblemPlusJSONResponse(invalid(invalidTemplateTitle, f)), nil
	}
	lines, err := s.templateLines(ctx, in.lines, nil)
	if err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersTemplate
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidTemplateTitle, lines); err != nil {
			return err
		}
		t, err := txq.InsertTemplate(ctx, store.InsertTemplateParams{
			Name: in.name, IntroText: in.intro, ClosingText: in.closing, ValidityDays: in.validityDays,
			DiscountVisible: in.discountVisible, Archived: in.archived, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, "ux_templates_name") {
			return templateNameTaken(in.name)
		}
		if err != nil {
			return fmt.Errorf("orders: insert template: %w", err)
		}
		rows, err := writeLines(ctx, txq, ownerTemplate, t.ID, lines)
		if err != nil {
			return err
		}
		out = templateView(t, rows)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CreateQuoteTemplate201JSONResponse(out), nil
}

// UpdateQuoteTemplate Save a quote template
// (PUT /api/v1/orders/templates/{id})
func (s *server) UpdateQuoteTemplate(ctx context.Context, req gen.UpdateQuoteTemplateRequestObject) (gen.UpdateQuoteTemplateResponseObject, error) {
	resp := answers[gen.UpdateQuoteTemplateResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.UpdateQuoteTemplateResponseObject {
			return gen.UpdateQuoteTemplate400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateQuoteTemplateResponseObject { return gen.UpdateQuoteTemplate404Response{} },
		conflict: func(p gen.OrdersProblem) gen.UpdateQuoteTemplateResponseObject {
			return gen.UpdateQuoteTemplate409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, templateInputSchema)
	in := readTemplateFields(&f, *req.Body)
	if req.Body.Revision == nil {
		f.add("revision", "A template's save names the revision it was read at")
	}
	if f.failed() {
		return gen.UpdateQuoteTemplate400ApplicationProblemPlusJSONResponse(invalid(invalidTemplateTitle, f)), nil
	}
	_, existing, found, err := s.readTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.UpdateQuoteTemplate404Response{}, nil
	}
	lines, err := s.templateLines(ctx, in.lines, byID(existing))
	if err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersTemplate
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidTemplateTitle, lines); err != nil {
			return err
		}
		cur, err := txq.LockTemplate(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: lock template %d: %w", req.Id, err)
		}
		if cur.Revision != *req.Body.Revision {
			return versionConflict("template's revision", cur.Revision, *req.Body.Revision)
		}
		t, err := txq.UpdateTemplate(ctx, store.UpdateTemplateParams{
			ID: cur.ID, Name: in.name, IntroText: in.intro, ClosingText: in.closing, ValidityDays: in.validityDays,
			DiscountVisible: in.discountVisible, Archived: in.archived, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, "ux_templates_name") {
			return templateNameTaken(in.name)
		}
		if err != nil {
			return fmt.Errorf("orders: save template %d: %w", cur.ID, err)
		}
		rows, err := writeLines(ctx, txq, ownerTemplate, t.ID, lines)
		if err != nil {
			return err
		}
		out = templateView(t, rows)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateQuoteTemplate200JSONResponse(out), nil
}

// DeleteQuoteTemplate Delete a quote template
// (DELETE /api/v1/orders/templates/{id})
//
// Quotes made from it keep their lines; their template id names nothing
// after.
func (s *server) DeleteQuoteTemplate(ctx context.Context, req gen.DeleteQuoteTemplateRequestObject) (gen.DeleteQuoteTemplateResponseObject, error) {
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockTemplate(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("orders: lock template %d: %w", req.Id, err)
		}
		if err := txq.DeleteLinesOf(ctx, store.DeleteLinesOfParams{OwnerKind: ownerTemplate, OwnerID: req.Id}); err != nil {
			return fmt.Errorf("orders: delete template %d's lines: %w", req.Id, err)
		}
		if _, err := txq.DeleteTemplate(ctx, req.Id); err != nil {
			return fmt.Errorf("orders: delete template %d: %w", req.Id, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.DeleteQuoteTemplateResponseObject]{
			notFound: func() gen.DeleteQuoteTemplateResponseObject { return gen.DeleteQuoteTemplate404Response{} },
		})
	}
	return gen.DeleteQuoteTemplate204Response{}, nil
}

// ApplyQuoteTemplate Apply a template to a quote
// (POST /api/v1/orders/quotes/{id}/apply-template)
//
// The template's lines are appended to the draft's, or replace them; its
// texts fill the draft's empty ones; its lines are priced as O16 prices them
// in the quote's currency, and a line whose variant went away is skipped and
// named (O23). The quote's own lines keep their prices.
func (s *server) ApplyQuoteTemplate(ctx context.Context, req gen.ApplyQuoteTemplateRequestObject) (gen.ApplyQuoteTemplateResponseObject, error) {
	resp := answers[gen.ApplyQuoteTemplateResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.ApplyQuoteTemplateResponseObject {
			return gen.ApplyQuoteTemplate400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.ApplyQuoteTemplateResponseObject { return gen.ApplyQuoteTemplate404Response{} },
		conflict: func(p gen.OrdersProblem) gen.ApplyQuoteTemplateResponseObject {
			return gen.ApplyQuoteTemplate409ApplicationProblemPlusJSONResponse(p)
		},
	}
	if f := s.unknownProperties(ctx, applyTemplateInputSchema); f.failed() {
		return gen.ApplyQuoteTemplate400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	cur, curLines, err := s.draftQuote(ctx, req.Id, req.Body.Revision, req.Body.ExpectedUpdatedAt)
	if err != nil {
		return answer(err, resp)
	}
	t, tlines, found, err := s.readTemplate(ctx, req.Body.TemplateId)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.ApplyQuoteTemplate404Response{}, nil
	}
	h := headerOfQuote(cur)
	applyTemplateTexts(&h, t, true, true, false)
	var inputs []lineInput
	replace := req.Body.Replace != nil && *req.Body.Replace
	if !replace {
		inputs = keptInputs(curLines)
	}
	inputs = append(inputs, templateLineInputs(tlines)...)
	skipped := []gen.OrdersSkippedLine{}
	priced, err := s.priceInputs(ctx, &inputs, &skipped, curLines, h, false)
	if err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersTemplateApplied
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		row, lines, err := s.saveDraftQuote(ctx, tx, txq, cur.ID, req.Body.Revision, req.Body.ExpectedUpdatedAt, h, priced, cur.ValidUntil, &t.ID)
		if err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, row.CustomerID, lines)
		if err != nil {
			return err
		}
		out = gen.OrdersTemplateApplied{Quote: quoteView(row, lines, nil, warnings), Skipped: skipped}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ApplyQuoteTemplate200JSONResponse(out), nil
}

// keptInputs are a document's lines handed in again as they are: each names
// its id and only what a save would not otherwise keep.
func keptInputs(lines []store.OrdersLine) []lineInput {
	out := make([]lineInput, 0, len(lines))
	for _, l := range lines {
		id := l.ID
		in := lineInput{id: &id, kind: l.Kind, variantID: l.VariantID, optional: l.Optional}
		if l.PackagingID != nil && l.PackagingQuantity.Valid {
			in.packagingID, in.packagingQuantity = l.PackagingID, wirePtr(ratOf(l.PackagingQuantity), quantityPlaces)
		}
		if l.Kind != kindProduct {
			in.name = l.Name
		}
		out = append(out, in)
	}
	return out
}
