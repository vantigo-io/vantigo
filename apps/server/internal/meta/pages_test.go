package meta_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestGraphPages_AddAPage(t *testing.T) {
	e := newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken, "MANAGE", "MESSAGING")
	r := e.addPage("  " + grPageToken + "  ")
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var page gen.MetaPage
	r.JSON(&page)
	if page.PageId != grPageID || page.Name != grPageName || page.State != "active" {
		t.Fatalf("page: %+v", page)
	}
	if strings.Contains(string(r.Body), grPageToken) {
		t.Fatal("the token is answered")
	}
	fields, ok := e.fake.Subscribed(grPageID)
	if !ok || fields != "messages,messaging_postbacks,messaging_referrals,message_reads,message_deliveries,message_echoes" {
		t.Fatalf("subscribed_fields = %q (%v)", fields, ok)
	}
	sealed := modtest.One[[]byte](t, e.h.Harness, `SELECT token_sealed FROM meta.pages WHERE page_id = $1`, grPageID)
	if len(sealed) == 0 || strings.Contains(string(sealed), grPageToken) {
		t.Fatalf("the token is not sealed: %q", sealed)
	}
	if s := getSettings(t, e.owner); len(s.Pages) != 1 || s.Pages[0].PageId != grPageID {
		t.Fatalf("settings pages: %+v", s.Pages)
	}
}

const grSysToken = "EAAB-system-user"

func (e *grEnv) addPageChoosing(token, pageID string) *modtest.Response {
	return e.owner.Do(http.MethodPost, pagesPath, map[string]any{"accessToken": token, "pageId": pageID})
}

func grNoPageKept(t *testing.T, e *grEnv) {
	t.Helper()
	if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 0 {
		t.Fatalf("%d Pages kept", n)
	}
	if len(e.fake.CallsTo("subscribed_apps")) != 0 {
		t.Fatal("subscribed_apps was called")
	}
}

func TestGraphPages_ASystemUserTokenConnectsThePagesOwnToken(t *testing.T) {
	e := newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken, "MANAGE", "MESSAGING")
	e.fake.AddUserToken(grSysToken, grPageID)
	r := e.addPage(grSysToken)
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if calls := e.fake.CallsTo("POST /" + grPageID + "/subscribed_apps"); len(calls) != 1 || calls[0].Token != grPageToken {
		t.Fatalf("subscribe calls: %+v", calls)
	}
	// The stored token is the Page's own: Check reads /me with it.
	e.fake.ResetCalls()
	if ep, err := e.m.Check(context.Background(), grPageID); err != nil || !ep.Available {
		t.Fatalf("check: %+v %v", ep, err)
	}
	if calls := e.fake.CallsTo("GET /me"); len(calls) != 1 || calls[0].Token != grPageToken {
		t.Fatalf("check calls: %+v", calls)
	}
	sealed := modtest.One[[]byte](t, e.h.Harness, `SELECT token_sealed FROM meta.pages WHERE page_id = $1`, grPageID)
	if strings.Contains(string(sealed), grSysToken) || strings.Contains(string(sealed), grPageToken) {
		t.Fatalf("a token is stored in the clear: %q", sealed)
	}
	if strings.Contains(string(r.Body), grSysToken) || strings.Contains(string(r.Body), grPageToken) {
		t.Fatal("a token is answered")
	}
}

func TestGraphPages_SeveralPagesAskForAChoice(t *testing.T) {
	e := newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken, "MESSAGING")
	e.fake.AddPage("200", "Bakeri", "EAAB-b", "MESSAGING")
	e.fake.AddPage("300", "Uten melding", "EAAB-c", "MANAGE")
	e.fake.AddUserToken(grSysToken, "300", grPageID, "200")

	r := e.addPage(grSysToken)
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_choice_required" {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var p gen.MetaProblem
	r.JSON(&p)
	if p.Choices == nil || len(*p.Choices) != 2 {
		t.Fatalf("choices: %s", r.Body)
	}
	got := map[string]string{}
	for _, c := range *p.Choices {
		got[c.Id] = c.Name
	}
	if got[grPageID] != grPageName || got["200"] != "Bakeri" {
		t.Fatalf("choices: %+v", got)
	}
	grNoPageKept(t, e)

	r = e.addPageChoosing(grSysToken, "200")
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var page gen.MetaPage
	r.JSON(&page)
	if page.PageId != "200" || page.Name != "Bakeri" {
		t.Fatalf("page: %+v", page)
	}
	if calls := e.fake.CallsTo("subscribed_apps"); len(calls) != 1 || calls[0].Token != "EAAB-b" {
		t.Fatalf("subscribe calls: %+v", calls)
	}
}

func TestGraphPages_RefuseWhatTheTokenDoesNotManage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tasks  []string
		pageID string
	}{
		{"a Page without MESSAGING", []string{"MANAGE", "CREATE_CONTENT"}, ""},
		{"a Page with no tasks", nil, ""},
		{"a pageId the token does not manage", []string{"MESSAGING"}, "999"},
		{"a pageId of a Page that cannot be messaged", []string{"MESSAGING"}, "300"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGrEnv(t)
			e.fake.AddPage(grPageID, grPageName, grPageToken, tc.tasks...)
			e.fake.AddPage("300", "Uten melding", "EAAB-c", "MANAGE")
			e.fake.AddUserToken(grSysToken, grPageID, "300")
			r := e.addPageChoosing(grSysToken, tc.pageID)
			if tc.pageID == "" {
				r = e.addPage(grSysToken)
			}
			if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
				t.Fatalf("status %d body %s", r.Status, r.Body)
			}
			grNoPageKept(t, e)
		})
	}
}

func TestGraphPages_APageTokenIsCheckedBySubscribing(t *testing.T) {
	e := newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken)
	r := e.addPageChoosing(grPageToken, grPageID)
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if calls := e.fake.CallsTo("subscribed_apps"); len(calls) != 1 || calls[0].Token != grPageToken {
		t.Fatalf("subscribe calls: %+v", calls)
	}
	for _, c := range e.fake.CallsTo("GET /me") {
		if q := c.Query.Get("fields"); strings.Contains(q, "tasks") && c.Path == "/me" {
			t.Fatalf("tasks read from /me: %v", c.Query)
		}
	}

	e = newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken)
	r = e.addPageChoosing(grPageToken, "999")
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
		t.Fatalf("a pageId mismatch: status %d body %s", r.Status, r.Body)
	}
	grNoPageKept(t, e)

	e = newGrEnv(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken)
	e.fake.FailNext("POST /"+grPageID+"/subscribed_apps", metatest.Failure{Status: 403, Code: 200, Message: "no permission"})
	r = e.addPage(grPageToken)
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
		t.Fatalf("a subscription refused: status %d body %s", r.Status, r.Body)
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 0 {
		t.Fatalf("%d Pages kept", n)
	}
}

func TestGraphPages_AUserTokenWithoutAPageIsRefused(t *testing.T) {
	e := newGrEnv(t)
	e.fake.AddUserToken(grSysToken)
	r := e.addPage(grSysToken)
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	grNoPageKept(t, e)
}

func TestGraphPages_RefuseAPageIdThatIsNotDigits(t *testing.T) {
	e := newGrEnv(t)
	r := e.addPageChoosing(grSysToken, "abc")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var p gen.MetaProblem
	r.JSON(&p)
	if p.Errors == nil || len((*p.Errors)["pageId"]) == 0 {
		t.Fatalf("no field error: %s", r.Body)
	}
}

func TestGraphPages_RefuseWhatMetaRefuses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		match     string
		failure   metatest.Failure
		wantCode  string
		wantState int
	}{
		{"an unknown token (190)", "GET /me", metatest.Failure{Status: 400, Code: 190, Message: "bad"}, "page_token_insufficient", 422},
		{"a forbidden read", "GET /me", metatest.Failure{Status: 403, Code: 200, Message: "no"}, "page_token_insufficient", 422},
		{"Meta is unreachable", "GET /me", metatest.Failure{Network: true}, "meta_unreachable", 503},
		{"Meta fails", "GET /me", metatest.Failure{Status: 500, Code: 2, Message: "oops"}, "meta_unreachable", 503},
		{"Meta throttles", "GET /me", metatest.Failure{Status: 400, Code: 4, Message: "slow"}, "meta_unreachable", 503},
		{"the subscription is forbidden", "POST /100200300/subscribed_apps", metatest.Failure{Status: 403, Code: 10, Message: "no"}, "page_token_insufficient", 422},
		{"the subscription fails", "POST /100200300/subscribed_apps", metatest.Failure{Status: 502}, "meta_unreachable", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGrEnv(t)
			e.fake.AddPage(grPageID, grPageName, grPageToken, "MESSAGING")
			e.fake.FailAll(tc.match, tc.failure)
			r := e.addPage(grPageToken)
			if r.Status != tc.wantState || grProblemCode(t, r) != tc.wantCode {
				t.Fatalf("status %d body %s", r.Status, r.Body)
			}
			if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 0 {
				t.Fatalf("%d Pages kept", n)
			}
		})
	}
}

func TestGraphPages_NeedAConfiguredApp(t *testing.T) {
	e := newGrEnvUnconfigured(t)
	e.fake.AddPage(grPageID, grPageName, grPageToken, "MESSAGING")
	r := e.addPage(grPageToken)
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "not_configured" {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("Meta was called")
	}
}

func TestGraphPages_ValidateTheBody(t *testing.T) {
	e := newGrEnv(t)
	for _, token := range []string{"", "   "} {
		r := e.addPage(token)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("token %q: status %d body %s", token, r.Status, r.Body)
		}
		var p gen.MetaProblem
		r.JSON(&p)
		if p.Errors == nil || len((*p.Errors)["accessToken"]) == 0 {
			t.Fatalf("no field error: %s", r.Body)
		}
	}
}

func TestGraphPages_AddingAgainReplacesTheTokenAndClearsARejection(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	before := modtest.One[[]byte](t, e.h.Harness, `SELECT token_sealed FROM meta.pages WHERE page_id = $1`, grPageID)
	e.fake.Revoke(grPageToken)
	if ep, err := e.m.Check(context.Background(), grPageID); err != nil || ep.Available {
		t.Fatalf("check with a revoked token: %+v %v", ep, err)
	}
	if grPageState(t, e) != "token_rejected" {
		t.Fatal("the Page is not rejected")
	}

	e.fake.AddPage(grPageID, grPageName, "EAAB-new-token", "MESSAGING")
	r := e.addPage("EAAB-new-token")
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var page gen.MetaPage
	r.JSON(&page)
	if page.State != "active" || page.LastError != nil {
		t.Fatalf("page after re-adding: %+v", page)
	}
	after := modtest.One[[]byte](t, e.h.Harness, `SELECT token_sealed FROM meta.pages WHERE page_id = $1`, grPageID)
	if string(before) == string(after) {
		t.Fatal("the token was not replaced")
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 1 {
		t.Fatalf("%d Pages", n)
	}
	if ep, err := e.m.Check(context.Background(), grPageID); err != nil || !ep.Available {
		t.Fatalf("check with the new token: %+v %v", ep, err)
	}
}

// TestGraphPages_ResubscribeAddsTheFieldsASubscribedPageLacks: a Page connected
// before messaging_referrals joined the list (delivery D) is subscribed again
// with the stored token, and the list Meta holds then has the field. A repeat is
// harmless; a Page that is not connected is 404; a token Meta refuses marks the
// Page and answers 422.
func TestGraphPages_ResubscribeAddsTheFieldsASubscribedPageLacks(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	path := pagesPath + "/" + grPageID + "/resubscribe"
	e.fake.ResetCalls()
	for range 2 {
		r := e.owner.Do(http.MethodPost, path, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("resubscribe: status %d body %s", r.Status, r.Body)
		}
		var page gen.MetaPage
		r.JSON(&page)
		if page.PageId != grPageID || page.State != "active" {
			t.Errorf("page = %+v", page)
		}
	}
	fields, ok := e.fake.Subscribed(grPageID)
	if !ok || !strings.Contains(fields, "messaging_referrals") {
		t.Errorf("subscribed_fields = %q (%v), want messaging_referrals among them", fields, ok)
	}
	if calls := e.fake.CallsTo("POST /" + grPageID + "/subscribed_apps"); len(calls) != 2 || calls[0].Token != grPageToken {
		t.Errorf("subscription calls = %+v, want two with the stored token", calls)
	}

	if r := e.owner.Do(http.MethodPost, pagesPath+"/424242/resubscribe", nil); r.Status != http.StatusNotFound {
		t.Errorf("unknown Page: status %d, want 404", r.Status)
	}
	e.fake.FailNext("POST /"+grPageID+"/subscribed_apps", metatest.Failure{Status: 400, Code: 190, Message: "bad"})
	r := e.owner.Do(http.MethodPost, path, nil)
	if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
		t.Errorf("rejected token: status %d body %s, want 422 page_token_insufficient", r.Status, r.Body)
	}
	if grPageState(t, e) != "token_rejected" {
		t.Error("the Page is not marked rejected")
	}
	e.fake.FailNext("POST /"+grPageID+"/subscribed_apps", metatest.Failure{Status: 502})
	if r := e.owner.Do(http.MethodPost, path, nil); r.Status != http.StatusServiceUnavailable {
		t.Errorf("Meta down: status %d, want 503", r.Status)
	}
	if r := e.h.SignIn(t, "communications:channels-manage").Do(http.MethodPost, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("not the Owner: status %d, want 403", r.Status)
	}
}

func TestGraphPages_RemoveAPage(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	const other = "999"
	e.fake.AddPage(other, "Annen side", "EAAB-other", "MESSAGING")
	if r := e.addPage("EAAB-other"); r.Status != http.StatusCreated {
		t.Fatalf("add: %d %s", r.Status, r.Body)
	}
	e.fake.AddUser(grPSID, "Kari Nordmann")
	if _, err := e.m.Profile(context.Background(), grPageID, grPSID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = e.store.Put(ctx, "k/mine", strings.NewReader("x"), "text/plain")
	_ = e.store.Put(ctx, "k/theirs", strings.NewReader("y"), "text/plain")
	insert := `INSERT INTO meta.inbound (page_id, kind, dedup_key, participant, occurred_at, payload, storage_keys, created_at)
		VALUES ($1, 'message', $2, 'u', now(), '{}', $3, now())`
	e.h.Exec(t, insert, grPageID, "d1", []string{"k/mine", "k/gone"})
	e.h.Exec(t, insert, other, "d2", []string{"k/theirs"})

	r := e.owner.Do(http.MethodDelete, pagesPath+"/"+grPageID, nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if _, ok := e.fake.Subscribed(grPageID); ok {
		t.Fatal("the subscription was not withdrawn")
	}
	if calls := e.fake.CallsTo("DELETE /" + grPageID + "/subscribed_apps"); len(calls) != 1 || calls[0].Token != grPageToken {
		t.Fatalf("withdraw calls: %+v", calls)
	}
	for _, q := range []string{
		`SELECT count(*) FROM meta.pages WHERE page_id = '` + grPageID + `'`,
		`SELECT count(*) FROM meta.inbound WHERE page_id = '` + grPageID + `'`,
		`SELECT count(*) FROM meta.profiles WHERE page_id = '` + grPageID + `'`,
	} {
		if n := e.h.Count(t, q); n != 0 {
			t.Errorf("%s: %d rows left", q, n)
		}
	}
	if ok, _ := e.store.Exists(ctx, "k/mine"); ok {
		t.Error("the Page's file is kept")
	}
	if ok, _ := e.store.Exists(ctx, "k/theirs"); !ok {
		t.Error("another Page's file was deleted")
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.inbound WHERE page_id = $1`, other); n != 1 {
		t.Errorf("another Page's rows: %d", n)
	}

	r = e.owner.Do(http.MethodDelete, pagesPath+"/"+grPageID, nil)
	if r.Status != http.StatusNotFound || grProblemCode(t, r) != "page_not_found" {
		t.Fatalf("second delete: status %d body %s", r.Status, r.Body)
	}
}

func TestGraphPages_RemovalIgnoresAFailedWithdrawal(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	e.fake.FailAll("DELETE", metatest.Failure{Network: true})
	if r := e.owner.Do(http.MethodDelete, pagesPath+"/"+grPageID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 0 {
		t.Fatalf("%d Pages left", n)
	}
}

func TestGraphPages_EndpointsListEveryPageWithItsState(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	e.fake.AddPage("200", "Bakeri", "EAAB-b", "MESSAGING")
	if r := e.addPage("EAAB-b"); r.Status != http.StatusCreated {
		t.Fatal(r.Status)
	}
	e.h.Exec(t, `UPDATE meta.pages SET state = 'token_rejected' WHERE page_id = '200'`)
	e.fake.ResetCalls()

	eps, err := e.m.Endpoints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []contracts.MessengerEndpoint{
		{Address: "200", DisplayName: "Bakeri", Available: false, Reason: "token_rejected"},
		{Address: grPageID, DisplayName: grPageName, Available: true},
	}
	if len(eps) != 2 || eps[0] != want[0] || eps[1] != want[1] {
		t.Fatalf("endpoints: %+v", eps)
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("Endpoints called Meta")
	}
}

func TestGraphPages_EndpointsNeedAConfiguredApp(t *testing.T) {
	e := newGrEnvUnconfigured(t)
	if _, err := e.m.Endpoints(context.Background()); !isUnavailable(err) {
		t.Fatalf("err = %v", err)
	}
}

func isUnavailable(err error) bool { return errors.Is(err, contracts.ErrMessengerUnavailable) }

func TestGraphPages_CheckIsLive(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	ctx := context.Background()

	e.fake.RenamePage(grPageID, "Nordlys Bil AS")
	e.h.Exec(t, `UPDATE meta.pages SET state = 'token_rejected', last_error = 'token_rejected' WHERE page_id = $1`, grPageID)
	ep, err := e.m.Check(ctx, grPageID)
	if err != nil || ep != (contracts.MessengerEndpoint{Address: grPageID, DisplayName: "Nordlys Bil AS", Available: true}) {
		t.Fatalf("check: %+v %v", ep, err)
	}
	if name := modtest.One[string](t, e.h.Harness, `SELECT name FROM meta.pages WHERE page_id = $1`, grPageID); name != "Nordlys Bil AS" {
		t.Fatalf("stored name %q", name)
	}
	if grPageState(t, e) != "active" {
		t.Fatal("the Page is not active again")
	}

	e.fake.Revoke(grPageToken)
	ep, err = e.m.Check(ctx, grPageID)
	if err != nil || ep.Available || ep.Reason != "token_rejected" || ep.Address != grPageID {
		t.Fatalf("check with a rejected token: %+v %v", ep, err)
	}
	if grPageState(t, e) != "token_rejected" {
		t.Fatal("the rejection is not kept")
	}
	if eps, _ := e.m.Endpoints(ctx); len(eps) != 1 || eps[0].Available {
		t.Fatalf("endpoints after rejection: %+v", eps)
	}
}

func TestGraphPages_CheckRefusals(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	ctx := context.Background()
	if _, err := e.m.Check(ctx, "404"); !errors.Is(err, contracts.ErrMessengerEndpointUnknown) {
		t.Fatalf("unknown Page: %v", err)
	}
	e.fake.FailNext("GET /me", metatest.Failure{Network: true})
	ep, err := e.m.Check(ctx, grPageID)
	if err == nil || ep.Available || errors.Is(err, contracts.ErrMessengerEndpointUnknown) || errors.Is(err, contracts.ErrMessengerUnavailable) {
		t.Fatalf("network failure: %+v %v", ep, err)
	}
	e.fake.FailNext("GET /me", metatest.Failure{Status: 500})
	if _, err := e.m.Check(ctx, grPageID); err == nil {
		t.Fatal("a 500 is not an error")
	}
	if grPageState(t, e) != "active" {
		t.Fatal("a failed check changed the Page's state")
	}

	un := newGrEnvUnconfigured(t)
	if _, err := un.m.Check(ctx, grPageID); !errors.Is(err, contracts.ErrMessengerUnavailable) {
		t.Fatalf("unconfigured: %v", err)
	}
}
