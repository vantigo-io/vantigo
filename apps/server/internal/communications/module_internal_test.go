package communications

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// This file is task 5 fix round 1 item 1's teeth check: a white-box test,
// package communications rather than communications_test, because proving
// "the body was never read" needs a body whose Read method can record
// whether it was ever called — a level of control communications_test's
// black-box modtest.Client (JSON-marshaled bodies only) cannot give a test,
// and needs mount and patchBodyMux, both unexported. channels_internal_test.go
// (task 4 fix round 1) is the precedent for this shape of test in this
// module.

// denyingAccess is a contracts.Access that forbids every request without
// inspecting anything about it — the minimum needed to prove a request
// never reaches past Access.Check.
type denyingAccess struct{}

func (denyingAccess) Check(*http.Request, contracts.Rule) (contracts.Principal, error) {
	return contracts.Principal{}, contracts.ErrForbidden
}

func (denyingAccess) Reject(w http.ResponseWriter, _ *http.Request, _ contracts.Rule, _ error) {
	w.WriteHeader(http.StatusForbidden)
}

// trackingReader records whether Read was ever called on it, so a test can
// tell "the body was read" from "the body was merely present."
type trackingReader struct{ read bool }

func (r *trackingReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// TestMount_ForbiddenPatchNeverReadsTheBody is task 5 fix round 1's item 1
// teeth check.
//
// Before the fix, withRawPatchBody wrapped mount's *returned handler* —
// outside module.Router entirely (module.go's old `return
// withRawPatchBody(handler), nil`) — so it ran before rate limiting, before
// Access.Check and before the operation's MaxBytesReader cap, contradicting
// router.go's own wrap ordering (rate limit -> Access.Check -> the body cap
// -> the handler). A forbidden PATCH still buffered its whole body before
// anyone checked whether the caller could do anything at all.
//
// patchBodyMux fixes this by injecting the capture at HandleFunc-registration
// time instead: gen.HandlerWithOptions calls BaseRouter.HandleFunc(pattern, h)
// once per operation, and Router.HandleFunc wraps whatever h it receives with
// router.wrap before registering it. Intercepting h *before* it reaches
// Router.HandleFunc (patchBodyMux.HandleFunc) means withRawPatchBody ends up
// *inside* wrap's own returned closure — reached only after wrap's rate-limit,
// Access.Check and MaxBytesReader steps have already run.
//
// This test proves the fixed ordering directly, black-box status code alone
// cannot: a forbidden caller gets 403 whether or not the body was read first
// (Access.Check rejects regardless, in both the old and new code), so the
// only way to distinguish "read early" from "never read" is to ask the body
// itself. denyingAccess refuses every request; trackingReader remembers
// whether anything ever called Read on it. If they disagree — 403 but the
// body was read — the capture ran before Access.Check.
func TestMount_ForbiddenPatchNeverReadsTheBody(t *testing.T) {
	doc, err := openapi.Load(context.Background(), "communications")
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	catalog := map[string]contracts.Permission{}
	for _, p := range permissions {
		catalog[p.Key] = p
	}

	handler, err := mount(module.Deps{Doc: doc, Access: denyingAccess{}, Catalog: catalog})
	if err != nil {
		t.Fatalf("mount: %v", err)
	}

	body := &trackingReader{}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/communications/conversations/"+uuid.NewString(), body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (from denyingAccess)", rec.Code)
	}
	if body.read {
		t.Error("the request body was read despite Access.Check refusing the request — withRawPatchBody ran before Access.Check")
	}
}
