package module

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// apiPolicyName is the rate limit every API key request is counted under,
// keyed by its integration (API access design K24).
const apiPolicyName = "api"

// keyGate is what every API key request goes through once Access.Check has
// judged it (API access design K23, K24): its integration's rate limit and
// the request log. The router's wrap and GET /api/openapi.json both use it,
// so a key is counted and logged the same on every path it can reach.
type keyGate struct {
	limiter *ratelimit.Limiter
	// recorder is the API request log, when Access implements it (K35):
	// found by a type assertion, as the live hub finds StationAccess.
	recorder contracts.APIRequestRecorder
}

func newKeyGate(access contracts.Access, limiter *ratelimit.Limiter) keyGate {
	recorder, _ := access.(contracts.APIRequestRecorder)
	return keyGate{limiter: limiter, recorder: recorder}
}

// refuse answers err, an ErrUnauthenticated or ErrForbidden from Check, with
// access.Reject, and records the refusal when identity found the key: a
// contracts.APIKeyRefusal. A key identity did not find is not recorded:
// there is nothing to attribute it to.
func (g keyGate) refuse(w http.ResponseWriter, req *http.Request, access contracts.Access, rule contracts.Rule, err error, operationID string, start time.Time) {
	var refusal *contracts.APIKeyRefusal
	if g.recorder != nil && errors.As(err, &refusal) {
		rec := httpx.NewStatusRecorder(w)
		access.Reject(rec, req, rule, err)
		g.record(req, operationID, refusal.KeyID, refusal.ConnectionID, refusal.IntegrationID, rec.Status(), refusal.Reason, start)
		return
	}
	access.Reject(w, req, rule, err)
}

// serve runs next for p, an admitted API key's principal: it counts the
// request against the integration's rate_limit_per_minute, across every key
// of the integration, writes the RateLimit-* headers, answers one over the
// limit with ratelimit.Reject instead, and records the answer once given —
// a panic's as 500, before the panic goes on to the recovery further out.
func (g keyGate) serve(w http.ResponseWriter, req *http.Request, p contracts.Principal, operationID string, start time.Time, next func(http.ResponseWriter, *http.Request)) {
	g.run(w, req, p, operationID, start, true, next)
}

// recordOnly runs next for p as serve does, recording the answer, but counts
// nothing against the integration's limit: the MCP endpoint's envelope of a
// tool call whose looped-back request is counted itself (MCP server design
// M22), so the call costs one request.
func (g keyGate) recordOnly(w http.ResponseWriter, req *http.Request, p contracts.Principal, operationID string, start time.Time, next func(http.ResponseWriter, *http.Request)) {
	g.run(w, req, p, operationID, start, false, next)
}

func (g keyGate) run(w http.ResponseWriter, req *http.Request, p contracts.Principal, operationID string, start time.Time, count bool, next func(http.ResponseWriter, *http.Request)) {
	rec := httpx.NewStatusRecorder(w)
	// reason is what the log records: a refusal's, or a replay's, which next
	// sets through the holder on its request's context (idempotency design
	// I22).
	reason := new(string)
	if g.recorder != nil {
		defer func() {
			if v := recover(); v != nil {
				g.record(req, operationID, p.Via.KeyID, p.Via.ConnectionID, p.UserID, http.StatusInternalServerError, *reason, start)
				panic(v)
			}
			g.record(req, operationID, p.Via.KeyID, p.Via.ConnectionID, p.UserID, rec.Status(), *reason, start)
		}()
	}
	if count && g.limiter != nil && p.Via.RateLimitPerMinute > 0 {
		policy := ratelimit.Policy{Name: apiPolicyName, Limit: p.Via.RateLimitPerMinute, Window: time.Minute}
		d, err := g.limiter.Count(req.Context(), policy, p.UserID.String())
		if err != nil {
			httpx.WriteError(rec, req, err)
			return
		}
		ratelimit.WriteHeaders(rec, policy, d)
		if !d.Allowed {
			*reason = contracts.APIReasonRateLimited
			ratelimit.Reject(rec, req, policy, d)
			return
		}
	}
	next(rec, req.WithContext(withAPIReason(req.Context(), reason)))
}

// record hands one answered key request to the request log: the key's, or a
// connection's (OAuth connections design A18), whichever presented itself. It
// carries the operation, never the path, whose parameters can be personal
// data, nor the query string.
func (g keyGate) record(req *http.Request, operationID string, keyID, connectionID, integrationID uuid.UUID, status int, reason string, start time.Time) {
	g.recorder.Record(context.WithoutCancel(req.Context()), contracts.APIRequest{
		At:            start,
		KeyID:         keyID,
		ConnectionID:  connectionID,
		IntegrationID: integrationID,
		Method:        req.Method,
		OperationID:   operationID,
		Status:        status,
		Reason:        reason,
		Duration:      time.Since(start),
		ClientIP:      httpx.ClientIP(req),
		TraceID:       httpx.TraceID(req),
	})
}
