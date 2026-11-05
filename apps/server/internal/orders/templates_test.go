package orders_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// templateLine is a template's product line.
func templateLine(variant int32, qty float64) gen.OrdersTemplateLineInput {
	return gen.OrdersTemplateLineInput{Kind: gen.OrdersTemplateLineInputKindProduct, VariantId: &variant, Quantity: &qty}
}

// A template is made, read, listed, saved at its revision and deleted
// (O23); its name is unique.
func TestQuoteTemplates_CRUD(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	days := int32(14)
	price := 80.0
	bolt := templateLine(varBolt, 4)
	bolt.UnitPrice = &price
	res := admin.Do(http.MethodPost, templatesPath, gen.OrdersTemplateInput{Name: "Standard", IntroText: str("Takk for forespørselen."),
		ValidityDays: &days, Lines: &[]gen.OrdersTemplateLineInput{
			{Kind: gen.OrdersTemplateLineInputKindSection, Name: str("Hardware")}, bolt, templateLine(varFood, 1),
		}})
	mustStatus(t, res, http.StatusCreated, "create")
	var tpl gen.OrdersTemplate
	res.JSON(&tpl)
	if len(tpl.Lines) != 3 || tpl.Lines[1].Sku == nil || *tpl.Lines[1].Sku != "BOLT-M8" || *tpl.Lines[1].UnitPrice != 80 || tpl.Revision != 1 {
		t.Errorf("template %+v", tpl)
	}
	expectRefusal(t, admin.Do(http.MethodPost, templatesPath, gen.OrdersTemplateInput{Name: "standard"}), http.StatusConflict, "template_name_taken", "a taken name")
	expectRefusal(t, admin.Do(http.MethodPost, templatesPath, gen.OrdersTemplateInput{Name: "Bad", Lines: &[]gen.OrdersTemplateLineInput{templateLine(999, 1)}}),
		http.StatusBadRequest, "variant_not_found", "an unknown variant")

	res = admin.Do(http.MethodGet, templatePath(tpl.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	rev := int32(1)
	keepID := tpl.Lines[1].Id
	res = admin.Do(http.MethodPut, templatePath(tpl.Id), gen.OrdersTemplateInput{Name: "Standard 2", Revision: &rev,
		Lines: &[]gen.OrdersTemplateLineInput{{Id: &keepID, Kind: gen.OrdersTemplateLineInputKindProduct, VariantId: i32(varBolt), Quantity: f64(5)}}})
	mustStatus(t, res, http.StatusOK, "save")
	var saved gen.OrdersTemplate
	res.JSON(&saved)
	if saved.Revision != 2 || len(saved.Lines) != 1 || saved.Lines[0].Id != keepID || saved.Name != "Standard 2" {
		t.Errorf("saved %+v; want revision 2 with the kept line", saved)
	}
	expectRefusal(t, admin.Do(http.MethodPut, templatePath(tpl.Id), gen.OrdersTemplateInput{Name: "X", Revision: &rev}), http.StatusConflict, "version_conflict", "stale")

	var list gen.OrdersTemplateList
	res = admin.Do(http.MethodGet, templatesPath, nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&list)
	if len(list.Items) != 1 || list.Items[0].LineCount != 1 {
		t.Errorf("list %+v", list)
	}
	mustStatus(t, admin.Do(http.MethodDelete, templatePath(tpl.Id), nil), http.StatusNoContent, "delete")
	mustStatus(t, admin.Do(http.MethodGet, templatePath(tpl.Id), nil), http.StatusNotFound, "get the deleted")
	mustStatus(t, admin.Do(http.MethodDelete, templatePath(tpl.Id), nil), http.StatusNotFound, "delete again")
	mustStatus(t, admin.Do(http.MethodPut, templatePath(tpl.Id), gen.OrdersTemplateInput{Name: "X", Revision: &rev}), http.StatusNotFound, "save the deleted")
}

// Applying a template appends its lines, priced in the quote's currency with
// a price override kept, fills the empty texts, and skips the lines whose
// variant was deleted or discontinued, naming them (O23). A quote created
// from a template takes them too, and its validity.
func TestApplyQuoteTemplate_SkipsTheVariantsThatWentAway(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	price := 80.0
	bolt := templateLine(varBolt, 4)
	bolt.UnitPrice = &price
	optional := templateLine(varFood, 2)
	yes := true
	optional.Optional = &yes
	days := int32(10)
	res := admin.Do(http.MethodPost, templatesPath, gen.OrdersTemplateInput{Name: "Kit", IntroText: str("Intro from the template"),
		ValidityDays: &days, Lines: &[]gen.OrdersTemplateLineInput{bolt, optional, templateLine(varOld, 1), templateLine(varUnmapped, 1)}})
	mustStatus(t, res, http.StatusCreated, "create the template")
	var tpl gen.OrdersTemplate
	res.JSON(&tpl)
	h.catalog.remove(varUnmapped)

	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	res = c.Do(http.MethodPost, quotePath(q.Id, "apply-template"), gen.OrdersApplyTemplateInput{Revision: 1, TemplateId: tpl.Id})
	mustStatus(t, res, http.StatusOK, "apply")
	var applied gen.OrdersTemplateApplied
	res.JSON(&applied)
	got := applied.Quote
	if len(got.Lines) != 3 || got.Lines[0].Id != q.Lines[0].Id || *got.Lines[1].UnitPrice != 80 || *got.Lines[1].PriceSource != gen.OrdersLinePriceSourceManual ||
		!got.Lines[2].Optional || *got.Lines[2].UnitPrice != 33.335 || got.IntroText != "Intro from the template" {
		t.Errorf("applied %+v; want the quote's line, the override, the optional coffee at its list price, the intro", got)
	}
	if len(applied.Skipped) != 2 || applied.Skipped[0].Reason != gen.Archived || applied.Skipped[1].Reason != gen.Deleted || applied.Skipped[1].VariantId != varUnmapped {
		t.Errorf("skipped %+v; want the discontinued and the deleted", applied.Skipped)
	}
	if got.Totals.Net != 420 {
		t.Errorf("net %v, want 420 (100 + 4 × 80; the optional line left out)", got.Totals.Net)
	}
	res = c.Do(http.MethodPost, quotePath(q.Id, "apply-template"), gen.OrdersApplyTemplateInput{Revision: 1, TemplateId: tpl.Id, Replace: &yes})
	mustStatus(t, res, http.StatusOK, "replace")
	res.JSON(&applied)
	if len(applied.Quote.Lines) != 2 {
		t.Errorf("replaced lines %d, want the template's two", len(applied.Quote.Lines))
	}
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "apply-template"), gen.OrdersApplyTemplateInput{Revision: 1, TemplateId: 999999}), http.StatusNotFound, "no template")

	res = c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, TemplateId: &tpl.Id})
	mustStatus(t, res, http.StatusCreated, "create from the template")
	fromTpl := quoteOf(t, res)
	if len(fromTpl.Lines) != 2 || fromTpl.IntroText != "Intro from the template" {
		t.Errorf("created from the template %+v", fromTpl)
	}
	res = c.Do(http.MethodPost, quotePath(fromTpl.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1})
	mustStatus(t, res, http.StatusOK, "mark sent")
	if s := quoteOf(t, res); s.ValidUntil == nil || s.ValidUntil.Time.Sub(h.Now()).Hours() > 11*24 {
		t.Errorf("valid until %v; want the template's 10 days", s.ValidUntil)
	}
}

// The settings (§9): the defaults until saved, a series start that may only
// rise above the highest number used (O12), a send channel Communications
// knows (O25), and the mail texts.
func TestOrdersSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	admin := h.SignIn(t, settingsKey...)
	res := admin.Do(http.MethodGet, settingsPath, nil)
	mustStatus(t, res, http.StatusOK, "get")
	var st gen.OrdersSettings
	res.JSON(&st)
	if st.Revision != 0 || st.QuoteValidityDays != 30 || st.DefaultPaymentTermsDays != 14 || st.NextQuoteNumber != 1 || !st.SendingAvailable {
		t.Errorf("defaults %+v", st)
	}
	c := h.SignIn(t, quoter...)
	createQuote(t, c, custBusiness)
	createQuote(t, c, custBusiness)

	input := func(rev int32, quoteStart int64) gen.OrdersSettingsInput {
		return gen.OrdersSettingsInput{Revision: rev, QuoteSeriesStart: quoteStart, OrderSeriesStart: 1, QuoteValidityDays: 20,
			DefaultPaymentTermsDays: 10, MailTexts: []gen.OrdersMailText{{Kind: gen.OrdersMailTextKindQuote, Language: gen.OrdersMailTextLanguageNb,
				Subject: "Tilbud {number} fra oss", Body: "Hei! Her er tilbudet."}}}
	}
	expectRefusal(t, admin.Do(http.MethodPut, settingsPath, input(0, 2)), http.StatusBadRequest, "series_start_too_low", "a start at a used number")
	res = admin.Do(http.MethodPut, settingsPath, input(0, 1000))
	mustStatus(t, res, http.StatusOK, "save")
	res.JSON(&st)
	if st.Revision != 1 || st.NextQuoteNumber != 1000 || st.QuoteValidityDays != 20 || len(st.MailTexts) != 1 {
		t.Errorf("saved %+v", st)
	}
	if q := createQuote(t, c, custBusiness); q.Number != 1000 {
		t.Errorf("the next quote's number = %d, want 1000", q.Number)
	}
	expectRefusal(t, admin.Do(http.MethodPut, settingsPath, input(0, 1000)), http.StatusConflict, "version_conflict", "stale")
	unknown := uuid.New()
	bad := input(1, 1000)
	bad.SendChannelId = &unknown
	expectRefusal(t, admin.Do(http.MethodPut, settingsPath, bad), http.StatusBadRequest, "send_channel_unknown", "an unknown channel")
	good := input(1, 1000)
	good.SendChannelId = &h.channel
	res = admin.Do(http.MethodPut, settingsPath, good)
	mustStatus(t, res, http.StatusOK, "choose the channel")
	res.JSON(&st)
	if st.SendChannelId == nil || *st.SendChannelId != h.channel {
		t.Errorf("channel %v, want %s", st.SendChannelId, h.channel)
	}

	// The override is the quote's mail text.
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{Recipient: "kari@acme.example", Revision: &q.Revision}), http.StatusOK, "send")
	if mails := h.mail.Sent(); len(mails) != 1 || mails[0].Out.Subject != "Tilbud 1001 fra oss" {
		t.Errorf("mails %+v; want the override's subject", mails)
	}
}

// Every operation is behind its access rule (§8): reading needs
// orders:access, writing a quote orders:quotes-manage, the settings and the
// templates orders:settings.
func TestAccessRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	none := h.SignIn(t)
	r := h.SignIn(t, reader...)
	quoterClient := h.SignIn(t, quoter...)
	q := createQuote(t, quoterClient, custBusiness)
	mustStatus(t, none.Do(http.MethodGet, quotesPath, nil), http.StatusForbidden, "list without access")
	mustStatus(t, r.Do(http.MethodGet, quotesPath, nil), http.StatusOK, "list with access")
	mustStatus(t, r.Do(http.MethodGet, quotePath(q.Id), nil), http.StatusOK, "get with access")
	mustStatus(t, r.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness}), http.StatusForbidden, "create without quotes-manage")
	mustStatus(t, r.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1}), http.StatusForbidden, "mark sent without quotes-manage")
	mustStatus(t, quoterClient.Do(http.MethodGet, settingsPath, nil), http.StatusForbidden, "settings without orders:settings")
	mustStatus(t, quoterClient.Do(http.MethodPost, templatesPath, gen.OrdersTemplateInput{Name: "X"}), http.StatusForbidden, "a template without orders:settings")
	mustStatus(t, r.Do(http.MethodGet, templatesPath, nil), http.StatusOK, "templates with access")
	mustStatus(t, h.Client(t).Do(http.MethodGet, quotesPath, nil), http.StatusUnauthorized, "anonymous")
}
