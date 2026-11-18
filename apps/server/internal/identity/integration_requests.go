package identity

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The request log's screen (API access design K23): the last 30 days, or
// one UTC day of them.
const (
	requestLogTopOperations = 10
	requestLogLatest        = 200
)

const (
	requestLogDayMessage   = "The day must be one of the last 30."
	requestLogClassMessage = "The status class must be 2, 3, 4 or 5."
)

// GetIdentityIntegrationsByIdRequests reads an integration's request log:
// the requests per day and status class, the operations called most and the
// latest 200, under the filters given.
// (GET /api/v1/identity/integrations/{id}/requests)
func (s *server) GetIdentityIntegrationsByIdRequests(ctx context.Context, req gen.GetIdentityIntegrationsByIdRequestsRequestObject) (gen.GetIdentityIntegrationsByIdRequestsResponseObject, error) {
	now := s.deps.Clock()
	today := utcDay(now)
	oldest := today.AddDate(0, 0, -apiRequestRetentionDays+1)
	from, to := oldest, now
	p := req.Params
	if p.Day != nil {
		day := utcDay(p.Day.Time)
		if day.Before(oldest) || day.After(today) {
			return refuse(http.StatusBadRequest, "invalid_request", requestLogDayMessage,
				map[string][]string{"day": {requestLogDayMessage}}), nil
		}
		from, to = day, day.AddDate(0, 0, 1)
	}
	var class *int32
	if p.StatusClass != nil {
		if *p.StatusClass < 2 || *p.StatusClass > 5 {
			return refuse(http.StatusBadRequest, "invalid_request", requestLogClassMessage,
				map[string][]string{"statusClass": {requestLogClassMessage}}), nil
		}
		c := int32(*p.StatusClass)
		class = &c
	}
	var reason *string
	if p.Reason != nil {
		r := string(*p.Reason)
		reason = &r
	}
	var key *uuid.UUID
	if p.Key != nil {
		k := *p.Key
		key = &k
	}

	exists, err := s.q.IntegrationExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("identity: read the integration: %w", err)
	}
	if !exists {
		return refusalOr[gen.GetIdentityIntegrationsByIdRequestsResponseObject](integrationNotFound)
	}
	days, err := s.q.APIRequestDailyCounts(ctx, store.APIRequestDailyCountsParams{
		IntegrationID: req.Id, FromAt: from, ToAt: to, KeyID: key, StatusClass: class, Reason: reason,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: read the request log's days: %w", err)
	}
	ops, err := s.q.APIRequestTopOperations(ctx, store.APIRequestTopOperationsParams{
		IntegrationID: req.Id, FromAt: from, ToAt: to, KeyID: key, StatusClass: class, Reason: reason, Top: requestLogTopOperations,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: read the request log's operations: %w", err)
	}
	latest, err := s.q.APIRequestsLatest(ctx, store.APIRequestsLatestParams{
		IntegrationID: req.Id, FromAt: from, ToAt: to, KeyID: key, StatusClass: class, Reason: reason, Latest: requestLogLatest,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: read the request log's latest requests: %w", err)
	}

	out := gen.IntegrationRequestLog{
		From:       from,
		To:         to,
		Days:       make([]gen.IntegrationRequestDay, 0, len(days)),
		Operations: make([]gen.IntegrationRequestOperation, 0, len(ops)),
		Latest:     make([]gen.IntegrationRequest, 0, len(latest)),
	}
	for _, d := range days {
		out.Days = append(out.Days, gen.IntegrationRequestDay{
			Day: dateOnly(d.Day.Time), StatusClass: int(d.StatusClass), Requests: int(d.Requests),
		})
	}
	for _, o := range ops {
		out.Operations = append(out.Operations, gen.IntegrationRequestOperation{OperationId: o.OperationID, Requests: int(o.Requests)})
	}
	for _, r := range latest {
		out.Latest = append(out.Latest, gen.IntegrationRequest{
			At: r.At.UTC(), KeyId: r.KeyID, KeyName: r.KeyName, Method: r.Method, OperationId: r.OperationID,
			Status: int(r.Status), Reason: r.Reason, DurationMs: int(r.DurationMs), ClientIp: r.ClientIp, TraceId: r.TraceID,
		})
	}
	return gen.GetIdentityIntegrationsByIdRequests200JSONResponse(out), nil
}

func (r refusal) VisitGetIdentityIntegrationsByIdRequestsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// dateOnly is t's UTC date as the contract's date.
func dateOnly(t time.Time) openapi_types.Date { return openapi_types.Date{Time: utcDay(t)} }
