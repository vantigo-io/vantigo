package orders

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the sales warnings (O33): one warning per customer and one per
// product, kept in Orders' own tables — a warning is a selling rule, and
// neither Customers nor Products needs it. An info warning is shown on every
// quote and order it applies to; a block warning also refuses send, mark as
// sent, confirm and accept into a confirmed order (blocked_by_warning), unless
// the request overrides it with a reason and the caller holds orders:settings.
// The override is kept: on an order's change log, or in a quote's internal
// note, since a quote has no change log.

// The warning levels and scopes.
const (
	warningInfo   = "info"
	warningBlock  = "block"
	scopeCustomer = "customer"
	scopeProduct  = "product"
)

// maxWarningText is how long a warning's text may be.
const maxWarningText = 500

// warningsOrEmpty is w, an empty list for none: the contract's warnings is
// required, never null.
func warningsOrEmpty(w []gen.OrdersWarning) []gen.OrdersWarning {
	if w == nil {
		return []gen.OrdersWarning{}
	}
	return w
}

func customerWarningOut(w store.OrdersCustomerWarning) gen.OrdersWarning {
	return gen.OrdersWarning{
		Scope: gen.OrdersWarningScopeCustomer, Id: w.CustomerID, Name: w.SubjectName, Level: gen.OrdersWarningLevel(w.Level),
		Text: w.Text, UpdatedAt: w.UpdatedAt, UpdatedBy: personOut(w.UpdatedByUserID, w.UpdatedByDisplay),
	}
}

func productWarningOut(w store.OrdersProductWarning) gen.OrdersWarning {
	return gen.OrdersWarning{
		Scope: gen.OrdersWarningScopeProduct, Id: w.ProductID, Name: w.SubjectName, Level: gen.OrdersWarningLevel(w.Level),
		Text: w.Text, UpdatedAt: w.UpdatedAt, UpdatedBy: personOut(w.UpdatedByUserID, w.UpdatedByDisplay),
	}
}

// documentWarnings are the warnings that apply to a document now, on q: its
// customer's, then the products' on its product lines, by product id.
func documentWarnings(ctx context.Context, q *store.Queries, customerID int32, lines []store.OrdersLine) ([]gen.OrdersWarning, error) {
	out := []gen.OrdersWarning{}
	cw, err := q.CustomerWarning(ctx, customerID)
	switch {
	case err == nil:
		out = append(out, customerWarningOut(cw))
	case !noRows(err):
		return nil, fmt.Errorf("orders: read customer %d's warning: %w", customerID, err)
	}
	var products []int32
	for _, l := range lines {
		if l.Kind == kindProduct && l.ProductID != nil && !slices.Contains(products, *l.ProductID) {
			products = append(products, *l.ProductID)
		}
	}
	if len(products) == 0 {
		return out, nil
	}
	rows, err := q.ProductWarningsOf(ctx, products)
	if err != nil {
		return nil, fmt.Errorf("orders: read the products' warnings: %w", err)
	}
	for _, w := range rows {
		out = append(out, productWarningOut(w))
	}
	return out, nil
}

// warningOverride is what a send or a confirm asks of the block warnings: to
// override them, whether the caller may (orders:settings, read before any
// transaction), and why.
type warningOverride struct {
	requested bool
	allowed   bool
	reason    string
}

// readOverride judges a request's overrideWarnings and overrideReason into f:
// an override names its reason, of at most 500 characters.
func (s *server) readOverride(ctx context.Context, f *fieldErrors, override *bool, reason *string) warningOverride {
	var o warningOverride
	if reason != nil {
		o.reason = strings.TrimSpace(*reason)
		if utf8.RuneCountInString(o.reason) > maxWarningText {
			f.add("overrideReason", fmt.Sprintf("A reason is at most %d characters", maxWarningText))
		}
	}
	if override != nil && *override {
		o.requested = true
		if o.reason == "" {
			f.add("overrideReason", "An override names its reason")
		}
		o.allowed = s.has(ctx, permSettings)
	}
	return o
}

// judgeWarnings refuses a send or a confirm that a block warning stops
// (blocked_by_warning, with the warnings' texts), on q, and answers the block
// warnings an allowed override overrides — none when nothing blocks. An info
// warning never refuses.
func judgeWarnings(ctx context.Context, q *store.Queries, customerID int32, lines []store.OrdersLine, o warningOverride) ([]gen.OrdersWarning, error) {
	all, err := documentWarnings(ctx, q, customerID, lines)
	if err != nil {
		return nil, err
	}
	var blocks []gen.OrdersWarning
	for _, w := range all {
		if w.Level == gen.OrdersWarningLevelBlock {
			blocks = append(blocks, w)
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	if o.requested && o.allowed {
		return blocks, nil
	}
	texts := make([]string, 0, len(blocks))
	for _, w := range blocks {
		texts = append(texts, warningLabel(w))
	}
	detail := strings.Join(texts, " ")
	if o.requested {
		detail += " Only a holder of Manage Orders settings may override a block."
	}
	return nil, refuse(problem(http.StatusConflict, codeBlockedByWarning, "A warning blocks this", detail))
}

// warningLabel is a warning as a refusal and the change log name it: its
// subject, then its text.
func warningLabel(w gen.OrdersWarning) string {
	subject := "Customer"
	if w.Scope == gen.OrdersWarningScopeProduct {
		subject = "Product"
	}
	if w.Name != "" {
		subject += " " + w.Name
	}
	return subject + ": " + w.Text
}

// overrideActions are an order's change log actions for the block warnings an
// override overrode: the warning, and the reason.
func overrideActions(overridden []gen.OrdersWarning, reason string) []changeAction {
	out := make([]changeAction, 0, len(overridden))
	for _, w := range overridden {
		out = append(out, changeAction{Kind: actionWarningOverridden, From: ptr(warningLabel(w)), To: ptr(reason)})
	}
	return out
}

// overrideNote is a quote's internal note with an override appended (O33): a
// quote has no change log, so the note keeps who overrode which warning, when
// and why, in the quote's language, cut to the note's 4000 characters.
func overrideNote(note string, overridden []gen.OrdersWarning, reason string, by actor, at time.Time, zone *time.Location, lang string) string {
	labels := make([]string, 0, len(overridden))
	for _, w := range overridden {
		labels = append(labels, warningLabel(w))
	}
	who := deref(by.display)
	day := formatDate(at.In(zone), lang)
	line := fmt.Sprintf("Advarsel overstyrt %s av %s (%s): %s", day, who, strings.Join(labels, " "), reason)
	if lang == "en" {
		line = fmt.Sprintf("Warning overridden on %s by %s (%s): %s", day, who, strings.Join(labels, " "), reason)
	}
	if strings.TrimSpace(note) != "" {
		line = note + "\n\n" + line
	}
	return truncate(line, 4000)
}

// readWarningInput judges a warning's level and text.
func readWarningInput(ctx context.Context, s *server, b gen.OrdersWarningInput) (string, string, fieldErrors) {
	f := s.unknownProperties(ctx, warningInputSchema)
	level := string(b.Level)
	if level != warningInfo && level != warningBlock {
		f.add("level", "A warning is info or block")
	}
	text := strings.TrimSpace(b.Text)
	switch {
	case text == "":
		f.add("text", "A warning has a text")
	case utf8.RuneCountInString(text) > maxWarningText:
		f.add("text", fmt.Sprintf("A warning's text is at most %d characters", maxWarningText))
	}
	return level, text, f
}

// ListOrdersWarnings List the sales warnings
// (GET /api/v1/orders/warnings)
func (s *server) ListOrdersWarnings(ctx context.Context, req gen.ListOrdersWarningsRequestObject) (gen.ListOrdersWarningsResponseObject, error) {
	q := store.New(s.deps.Pool)
	out := gen.OrdersWarningList{Items: []gen.OrdersWarning{}}
	scope := ""
	if req.Params.Scope != nil {
		scope = string(*req.Params.Scope)
	}
	if scope == "" || scope == scopeCustomer {
		rows, err := q.ListCustomerWarnings(ctx)
		if err != nil {
			return nil, fmt.Errorf("orders: list the customer warnings: %w", err)
		}
		for _, w := range rows {
			out.Items = append(out.Items, customerWarningOut(w))
		}
	}
	if scope == "" || scope == scopeProduct {
		rows, err := q.ListProductWarnings(ctx)
		if err != nil {
			return nil, fmt.Errorf("orders: list the product warnings: %w", err)
		}
		for _, w := range rows {
			out.Items = append(out.Items, productWarningOut(w))
		}
	}
	return gen.ListOrdersWarnings200JSONResponse(out), nil
}

// GetOrdersCustomerWarning Get a customer's warning
// (GET /api/v1/orders/warnings/customers/{customerId})
func (s *server) GetOrdersCustomerWarning(ctx context.Context, req gen.GetOrdersCustomerWarningRequestObject) (gen.GetOrdersCustomerWarningResponseObject, error) {
	w, err := store.New(s.deps.Pool).CustomerWarning(ctx, req.CustomerId)
	if noRows(err) {
		return gen.GetOrdersCustomerWarning404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read customer %d's warning: %w", req.CustomerId, err)
	}
	return gen.GetOrdersCustomerWarning200JSONResponse(customerWarningOut(w)), nil
}

// PutOrdersCustomerWarning Set a customer's warning
// (PUT /api/v1/orders/warnings/customers/{customerId})
//
// The customer is read through the directory before the write, which keeps
// its name beside the warning; a customer that does not exist is a 404.
func (s *server) PutOrdersCustomerWarning(ctx context.Context, req gen.PutOrdersCustomerWarningRequestObject) (gen.PutOrdersCustomerWarningResponseObject, error) {
	level, text, f := readWarningInput(ctx, s, *req.Body)
	if f.failed() {
		return gen.PutOrdersCustomerWarning400ApplicationProblemPlusJSONResponse(invalid(invalidWarningTitle, f)), nil
	}
	profile, err := s.billingProfile(ctx, req.CustomerId)
	if err != nil {
		if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
			return gen.PutOrdersCustomerWarning503ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	if profile == nil {
		return gen.PutOrdersCustomerWarning404Response{}, nil
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	row, err := store.New(s.deps.Pool).UpsertCustomerWarning(ctx, store.UpsertCustomerWarningParams{
		CustomerID: req.CustomerId, Level: level, Text: text, SubjectName: truncate(profile.Name, 255),
		UpdatedByUserID: by.id, UpdatedByDisplay: by.display, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("orders: set customer %d's warning: %w", req.CustomerId, err)
	}
	return gen.PutOrdersCustomerWarning200JSONResponse(customerWarningOut(row)), nil
}

// DeleteOrdersCustomerWarning Remove a customer's warning
// (DELETE /api/v1/orders/warnings/customers/{customerId})
func (s *server) DeleteOrdersCustomerWarning(ctx context.Context, req gen.DeleteOrdersCustomerWarningRequestObject) (gen.DeleteOrdersCustomerWarningResponseObject, error) {
	n, err := store.New(s.deps.Pool).DeleteCustomerWarning(ctx, req.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("orders: remove customer %d's warning: %w", req.CustomerId, err)
	}
	if n == 0 {
		return gen.DeleteOrdersCustomerWarning404Response{}, nil
	}
	return gen.DeleteOrdersCustomerWarning204Response{}, nil
}

// GetOrdersProductWarning Get a product's warning
// (GET /api/v1/orders/warnings/products/{productId})
func (s *server) GetOrdersProductWarning(ctx context.Context, req gen.GetOrdersProductWarningRequestObject) (gen.GetOrdersProductWarningResponseObject, error) {
	w, err := store.New(s.deps.Pool).ProductWarning(ctx, req.ProductId)
	if noRows(err) {
		return gen.GetOrdersProductWarning404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read product %d's warning: %w", req.ProductId, err)
	}
	return gen.GetOrdersProductWarning200JSONResponse(productWarningOut(w)), nil
}

// PutOrdersProductWarning Set a product's warning
// (PUT /api/v1/orders/warnings/products/{productId})
//
// The product is read through the catalog before the write, which keeps its
// name beside the warning; a product with no variants is a 404.
func (s *server) PutOrdersProductWarning(ctx context.Context, req gen.PutOrdersProductWarningRequestObject) (gen.PutOrdersProductWarningResponseObject, error) {
	level, text, f := readWarningInput(ctx, s, *req.Body)
	if f.failed() {
		return gen.PutOrdersProductWarning400ApplicationProblemPlusJSONResponse(invalid(invalidWarningTitle, f)), nil
	}
	variants, err := s.variantsOfProduct(ctx, req.ProductId)
	if err != nil {
		if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
			return gen.PutOrdersProductWarning503ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	if len(variants) == 0 {
		return gen.PutOrdersProductWarning404Response{}, nil
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	row, err := store.New(s.deps.Pool).UpsertProductWarning(ctx, store.UpsertProductWarningParams{
		ProductID: req.ProductId, Level: level, Text: text, SubjectName: truncate(variants[0].ProductName, 255),
		UpdatedByUserID: by.id, UpdatedByDisplay: by.display, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("orders: set product %d's warning: %w", req.ProductId, err)
	}
	return gen.PutOrdersProductWarning200JSONResponse(productWarningOut(row)), nil
}

// DeleteOrdersProductWarning Remove a product's warning
// (DELETE /api/v1/orders/warnings/products/{productId})
func (s *server) DeleteOrdersProductWarning(ctx context.Context, req gen.DeleteOrdersProductWarningRequestObject) (gen.DeleteOrdersProductWarningResponseObject, error) {
	n, err := store.New(s.deps.Pool).DeleteProductWarning(ctx, req.ProductId)
	if err != nil {
		return nil, fmt.Errorf("orders: remove product %d's warning: %w", req.ProductId, err)
	}
	if n == 0 {
		return gen.DeleteOrdersProductWarning404Response{}, nil
	}
	return gen.DeleteOrdersProductWarning204Response{}, nil
}
