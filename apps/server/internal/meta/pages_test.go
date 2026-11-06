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
	if !ok || fields != "messages,messaging_postbacks,message_reads,message_deliveries,message_echoes" {
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

func TestGraphPages_RefuseATokenThatCannotMessage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks []string
	}{
		{"a user's token answers no tasks", nil},
		{"tasks without MESSAGING", []string{"MANAGE", "CREATE_CONTENT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGrEnv(t)
			e.fake.AddPage(grPageID, grPageName, grPageToken, tc.tasks...)
			r := e.addPage(grPageToken)
			if r.Status != http.StatusUnprocessableEntity || grProblemCode(t, r) != "page_token_insufficient" {
				t.Fatalf("status %d body %s", r.Status, r.Body)
			}
			if n := e.h.Count(t, `SELECT count(*) FROM meta.pages`); n != 0 {
				t.Fatalf("%d Pages kept", n)
			}
			if _, ok := e.fake.Subscribed(grPageID); ok {
				t.Fatal("the Page was subscribed")
			}
			if len(e.fake.CallsTo("subscribed_apps")) != 0 {
				t.Fatal("subscribed_apps was called")
			}
		})
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
			e.fake.FailNext(tc.match, tc.failure)
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
