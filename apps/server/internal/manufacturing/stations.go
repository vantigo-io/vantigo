package manufacturing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// This file is Manufacturing's side of the shared shop-floor stations
// (manufacturing phase 2 M131, M135, M137). Identity owns the station and its
// operators; the router admits an operator only to the operations marked
// x-vantigo-station: allowed, with manufacturing:access and
// manufacturing:shop-floor at most. Here the station's work centers bound
// what those operations reach: stationScope, in front of every handler,
// refuses any request of a station's operator that names a work center, a
// work order or an order outside them, and the lists are cut down to them.

// codeStationScope is the 403 of a station's operator outside the station's
// work centers (M131), or at an operation this file does not know.
const codeStationScope = "station_scope"

// maxStationWorkCenters bounds a station's work centers.
const maxStationWorkCenters = 100

// stationOf is the station the request's operator signs in at, nil for a
// person's own browser.
func stationOf(ctx context.Context) *uuid.UUID {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	id, ok := p.Station()
	if !ok {
		return nil
	}
	return &id
}

// stationCenters is the work centers the request's station serves, and
// whether the request is a station's at all.
func (s *server) stationCenters(ctx context.Context) ([]int64, bool, error) {
	station := stationOf(ctx)
	if station == nil {
		return nil, false, nil
	}
	ids, err := store.New(s.deps.Pool).StationWorkCenterIDs(ctx, *station)
	return ids, true, err
}

// stationScopeProblem is the 403 station_scope.
func stationScopeProblem() gen.ManufacturingProblem {
	return problem(http.StatusForbidden, codeStationScope, "Not this station's",
		"This station does not serve the work center. An administrator sets which work centers a station serves.")
}

// stationScope is the module's strict middleware for station requests
// (M131): a request of a station's operator goes on to its handler only when
// stationAllows says it stays within the station's work centers; otherwise it
// is answered 403 station_scope here. An unknown id is station_scope too, so
// a station learns nothing of what lies outside its centers.
func (s *server) stationScope(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		station := stationOf(ctx)
		if station == nil {
			return f(ctx, w, r, request)
		}
		ok, err := s.stationAllows(ctx, *station, request)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: judge the station's scope of %s: %w", operationID, err)
		}
		if !ok {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(stationScopeProblem())
			return nil, nil
		}
		return f(ctx, w, r, request)
	}
}

// stationAllows decides a station request by what it names: a work center
// directly (the queue, a block); a work order — where it runs for an action,
// where it runs or one of its group's members for a read and a start without
// a member, the member named for a start that names one (M119); an order's
// production through its last work order; a deviation through the work order
// it names, which a station must name. The lists (work centers, my work) and
// the meta name nothing and are cut down by their handlers. Any other request
// is refused: an operation opened to stations without a rule here reaches
// nothing.
func (s *server) stationAllows(ctx context.Context, station uuid.UUID, request any) (bool, error) {
	q := store.New(s.deps.Pool)
	center := func(id int64) (bool, error) {
		return q.StationServesWorkCenter(ctx, store.StationServesWorkCenterParams{StationID: station, WorkCenterID: id})
	}
	workOrder := func(id int64, fixedOnly bool) (bool, error) {
		return q.StationServesWorkOrder(ctx, store.StationServesWorkOrderParams{StationID: station, WorkOrderID: id, FixedOnly: fixedOnly})
	}
	switch req := request.(type) {
	case gen.ListManufacturingWorkCentersRequestObject, gen.GetManufacturingMyWorkRequestObject, gen.GetManufacturingMetaRequestObject:
		return true, nil
	case gen.GetManufacturingWorkCenterQueueRequestObject:
		return center(req.Id)
	case gen.BlockManufacturingWorkCenterRequestObject:
		return center(req.Id)
	case gen.UnblockManufacturingWorkCenterRequestObject:
		return center(req.Id)
	case gen.GetManufacturingWorkOrderRequestObject:
		return workOrder(req.Id, false)
	case gen.StartManufacturingWorkOrderRequestObject:
		if req.Body != nil && req.Body.WorkCenterId != nil {
			ok, err := center(*req.Body.WorkCenterId)
			if err != nil || !ok {
				return false, err
			}
			return workOrder(req.Id, false)
		}
		return workOrder(req.Id, true)
	case gen.PauseManufacturingWorkOrderRequestObject:
		return workOrder(req.Id, true)
	case gen.FinishManufacturingWorkOrderRequestObject:
		return workOrder(req.Id, true)
	case gen.RegisterManufacturingWorkOrderQuantityRequestObject:
		return workOrder(req.Id, true)
	case gen.ReportManufacturingWorkOrderScrapRequestObject:
		return workOrder(req.Id, true)
	case gen.RecordManufacturingCheckResultRequestObject:
		return workOrder(req.Id, true)
	case gen.UploadManufacturingCheckPhotoRequestObject:
		return workOrder(req.Id, true)
	case gen.GetManufacturingProductionDraftRequestObject:
		return q.StationServesOrderOutput(ctx, store.StationServesOrderOutputParams{StationID: station, OrderID: req.Id})
	case gen.PostManufacturingProductionRequestObject:
		return q.StationServesOrderOutput(ctx, store.StationServesOrderOutputParams{StationID: station, OrderID: req.Id})
	case gen.CreateManufacturingDeviationRequestObject:
		if req.Body == nil || req.Body.WorkOrderId == nil {
			return false, nil
		}
		return workOrder(*req.Body.WorkOrderId, false)
	default:
		return false, nil
	}
}

// GetManufacturingStationWorkCenters is the work centers a station serves.
// (GET /api/v1/manufacturing/stations/{stationId}/work-centers)
func (s *server) GetManufacturingStationWorkCenters(ctx context.Context, req gen.GetManufacturingStationWorkCentersRequestObject) (gen.GetManufacturingStationWorkCentersResponseObject, error) {
	ids, err := store.New(s.deps.Pool).StationWorkCenterIDs(ctx, req.StationId)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return gen.GetManufacturingStationWorkCenters200JSONResponse{WorkCenterIds: ids}, nil
}

// PutManufacturingStationWorkCenters replaces the work centers a station
// serves, in one transaction; an id that names no work center is
// work_center_unknown.
// (PUT /api/v1/manufacturing/stations/{stationId}/work-centers)
func (s *server) PutManufacturingStationWorkCenters(ctx context.Context, req gen.PutManufacturingStationWorkCentersRequestObject) (gen.PutManufacturingStationWorkCentersResponseObject, error) {
	var ids []int64
	if req.Body != nil {
		ids = slices.Clone(req.Body.WorkCenterIds)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	const title = "The station's work centers were not saved"
	if len(ids) > maxStationWorkCenters {
		return gen.PutManufacturingStationWorkCenters400ApplicationProblemPlusJSONResponse(invalid(title, fieldErrorsOf("workCenterIds", "",
			fmt.Sprintf("At most %d work centers.", maxStationWorkCenters)))), nil
	}
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		n, err := txq.CountWorkCenters(ctx, ids)
		if err != nil {
			return err
		}
		if int(n) != len(ids) {
			return refuseField(title, "workCenterIds", codeWorkCenterUnknown, "A work center is not known.")
		}
		if err := txq.DeleteStationWorkCenters(ctx, req.StationId); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		return txq.InsertStationWorkCenters(ctx, store.InsertStationWorkCentersParams{StationID: req.StationId, WorkCenterIds: ids})
	})
	if err != nil {
		return answer(err, answers[gen.PutManufacturingStationWorkCentersResponseObject]{
			badRequest: func(p gen.ManufacturingProblem) gen.PutManufacturingStationWorkCentersResponseObject {
				return gen.PutManufacturingStationWorkCenters400ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if ids == nil {
		ids = []int64{}
	}
	return gen.PutManufacturingStationWorkCenters200JSONResponse{WorkCenterIds: ids}, nil
}

// fieldErrorsOf is one field's error, under code when it has one.
func fieldErrorsOf(field, code, message string) fieldErrors {
	var f fieldErrors
	if code == "" {
		f.add(field, message)
	} else {
		f.addCoded(field, code, message)
	}
	return f
}
