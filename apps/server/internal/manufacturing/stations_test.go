package manufacturing_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// A shared shop-floor station (manufacturing phase 2 M131, M135, M137): what
// its operators reach is the operations marked x-vantigo-station, at the
// station's work centers only, and what they record carries the station.

// stationToken is a fresh token and the hash identity stores for it.
func stationToken(t *testing.T) (string, []byte) {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:]
}

// atStation is an operator signed in at a paired station: a client carrying
// the station's cookie and the operator's token on every request. The rows
// are identity's, written as identity writes them, as modtest's SignIn writes
// a session.
type atStation struct {
	*modtest.Client
	station uuid.UUID
	user    uuid.UUID
	token   string
}

func (h *harness) atStation(t *testing.T, permissions ...string) atStation {
	t.Helper()
	_, user := h.SignInUser(t, permissions...)
	station := uuid.New()
	cookie, cookieHash := stationToken(t)
	now := h.Now()
	h.Exec(t, `INSERT INTO identity.stations (id, name, token_hash, token_expires_at, paired_at, created_at)
	           VALUES ($1, 'Station', $2, $3, $4, $4)`, station, cookieHash, now.Add(90*24*time.Hour), now)
	token, tokenHash := stationToken(t)
	h.Exec(t, `INSERT INTO identity.station_operator_sessions (id, station_id, user_id, token_hash, started_at, last_activity_at)
	           VALUES ($1, $2, $3, $4, $5, $5)`, uuid.New(), station, user, tokenHash, now)
	c := h.Client(t)
	c.SetCookie("vantigo.station", cookie)
	return atStation{Client: c, station: station, user: user, token: token}
}

// do sends a request as the operator.
func (s atStation) do(method, path string, body any, opts ...modtest.RequestOption) *modtest.Response {
	return s.Do(method, path, body, append(opts, modtest.Header("Authorization", "Station "+s.token))...)
}

// serve sets the station's work centers through the endpoint.
func serve(t *testing.T, admin *modtest.Client, station uuid.UUID, centers ...int64) {
	t.Helper()
	res := admin.Do(http.MethodPut, base+"/stations/"+station.String()+"/work-centers", gen.ManufacturingStationWorkCenters{WorkCenterIds: centers})
	mustStatus(t, res, http.StatusOK, "station work centers")
}

// refusalCode is a refusal's code in either shape: the access layer's
// {"error":{"code"}} or a problem's code.
func refusalCode(res *modtest.Response) string {
	if code := res.Code(); code != "" {
		return code
	}
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(res.Body, &p)
	return p.Code
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// TestStation_EveryOperationOutsideTheScopeIsRefused walks the contracts the
// harness serves — Manufacturing's and Identity's — as a station's operator
// whose station serves no work center and who holds every key: an operation
// not marked x-vantigo-station is 403 station_scope at the router, and a
// marked one that names a work center, a work order or an order is 403
// station_scope at the module. Only the lists and the meta answer.
func TestStation_EveryOperationOutsideTheScopeIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	op := h.atStation(t, append(slices.Clone(everything), "identity:manage")...)
	open := map[string]bool{"listManufacturingWorkCenters": true, "getManufacturingMyWork": true, "getManufacturingMeta": true}
	walked := 0
	for _, name := range []string{"manufacturing", "identity"} {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			if strings.HasPrefix(path, "/api/v1/identity/scim/") {
				continue // the SCIM ingress answers before the router; a station has no bearer token there
			}
			for method, o := range item.Operations() {
				concrete := pathParam.ReplaceAllStringFunc(path, func(p string) string {
					if strings.Contains(strings.ToLower(p), "id") && name == "identity" || p == "{stationId}" || p == "{sessionId}" {
						return uuid.NewString()
					}
					return "1001"
				})
				opts := []modtest.RequestOption{modtest.SkipContract("a station's operator walking the contract")}
				var body any
				if method != http.MethodGet && method != http.MethodDelete {
					body = map[string]any{}
				}
				if o.OperationID == "uploadManufacturingCheckPhoto" {
					body = nil
					opts = append(opts, modtest.RawBody("multipart/form-data; boundary=x", []byte("--x--\r\n")))
				}
				res := op.do(method, concrete, body, opts...)
				walked++
				if open[o.OperationID] {
					if res.Status != http.StatusOK {
						t.Errorf("%s %s (%s): %d %s, want 200", method, concrete, o.OperationID, res.Status, res.Body)
					}
					continue
				}
				if res.Status != http.StatusForbidden || refusalCode(res) != "station_scope" {
					t.Errorf("%s %s (%s): %d %s, want 403 station_scope", method, concrete, o.OperationID, res.Status, res.Body)
				}
			}
		}
	}
	if walked < 100 {
		t.Fatalf("walked %d operations; the walk is broken", walked)
	}
}

// TestStation_TheStationsWorkCenters is M131 and M137: an operator at a
// station reaches the station's work centers and nothing else — the queue,
// the work order, the actions and the production — the lists are cut down to
// them, no cost is answered even to an operator who holds costs-view, and the
// time logs carry the station.
func TestStation_TheStationsWorkCenters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, everything...)
	f := newFloor(t, h, admin)
	o, wos := openOrder(t, admin, "1")
	op := h.atStation(t, everything...)

	// A station that serves nothing reaches nothing.
	if res := op.do(http.MethodGet, workCenterPath(f.saw.Id, "queue"), nil); res.Status != http.StatusForbidden || refusalCode(res) != "station_scope" {
		t.Fatalf("queue before serving: %d %s", res.Status, res.Body)
	}

	var served gen.ManufacturingStationWorkCenters
	res := admin.Do(http.MethodGet, base+"/stations/"+op.station.String()+"/work-centers", nil)
	mustStatus(t, res, http.StatusOK, "station work centers")
	res.JSON(&served)
	if len(served.WorkCenterIds) != 0 {
		t.Fatalf("served = %v, want none", served.WorkCenterIds)
	}
	serve(t, admin, op.station, f.saw.Id)
	if res := admin.Do(http.MethodPut, base+"/stations/"+op.station.String()+"/work-centers", gen.ManufacturingStationWorkCenters{WorkCenterIds: []int64{999999}}); res.Status != http.StatusBadRequest {
		t.Fatalf("an unknown work center: %d %s", res.Status, res.Body)
	}
	if res := h.SignIn(t, operator...).Do(http.MethodPut, base+"/stations/"+op.station.String()+"/work-centers", gen.ManufacturingStationWorkCenters{WorkCenterIds: []int64{f.saw.Id}}); res.Status != http.StatusForbidden {
		t.Fatalf("setting them without boms-manage: %d %s", res.Status, res.Body)
	}

	// The lists are the station's; no cost is answered.
	var centers gen.ManufacturingWorkCenterList
	res = op.do(http.MethodGet, base+"/work-centers", nil)
	mustStatus(t, res, http.StatusOK, "work centers at the station")
	res.JSON(&centers)
	if len(centers.Items) != 1 || centers.Items[0].Id != f.saw.Id {
		t.Fatalf("work centers at the station = %+v, want SAW alone", centers.Items)
	}
	if len(centers.Items[0].MachineCostPerHour) != 0 || len(centers.Items[0].LabourCostPerHour) != 0 {
		t.Fatalf("costs answered at a station: %+v", centers.Items[0])
	}
	if res := op.do(http.MethodGet, base+"/meta", nil); res.Status != http.StatusOK {
		t.Fatalf("meta: %d %s", res.Status, res.Body)
	}

	// The SAW work order is the station's; the PAINT one is not.
	mustStatus(t, op.do(http.MethodGet, workCenterPath(f.saw.Id, "queue"), nil), http.StatusOK, "SAW queue")
	if res := op.do(http.MethodGet, workCenterPath(f.paint.Id, "queue"), nil); refusalCode(res) != "station_scope" {
		t.Fatalf("PAINT queue: %d %s", res.Status, res.Body)
	}
	mustStatus(t, op.do(http.MethodGet, workOrderPath(wos[0].Id), nil), http.StatusOK, "SAW work order")
	if res := op.do(http.MethodGet, workOrderPath(wos[1].Id), nil); refusalCode(res) != "station_scope" {
		t.Fatalf("PAINT work order: %d %s", res.Status, res.Body)
	}
	if res := op.do(http.MethodPost, workOrderPath(wos[1].Id, "start"), force); refusalCode(res) != "station_scope" {
		t.Fatalf("start at PAINT: %d %s", res.Status, res.Body)
	}
	// The production goes through the order's last work order, at PAINT.
	if res := op.do(http.MethodGet, orderPath(o.Id, "production-draft"), nil); refusalCode(res) != "station_scope" {
		t.Fatalf("production draft: %d %s", res.Status, res.Body)
	}
	// A deviation must name a work order of the station.
	if res := op.do(http.MethodPost, base+"/deviations", map[string]any{"title": "Scratch", "orderId": o.Id}); refusalCode(res) != "station_scope" {
		t.Fatalf("a deviation naming no work order: %d %s", res.Status, res.Body)
	}

	// Start and pause at SAW: the log carries the operator and the station.
	res = op.do(http.MethodPost, workOrderPath(wos[0].Id, "start"), nil)
	mustStatus(t, res, http.StatusOK, "start at SAW")
	var station *uuid.UUID
	if err := h.Pool().QueryRow(context.Background(), `SELECT station_id FROM manufacturing.time_logs WHERE work_order_id = $1 AND user_id = $2`,
		wos[0].Id, op.user).Scan(&station); err != nil {
		t.Fatal(err)
	}
	if station == nil || *station != op.station {
		t.Fatalf("time log station = %v, want %v", station, op.station)
	}
	mustStatus(t, op.do(http.MethodPost, workOrderPath(wos[0].Id, "pause"), nil), http.StatusOK, "pause at SAW")

	// My work at the station is the station's.
	var mine gen.ManufacturingMyWork
	res = op.do(http.MethodGet, base+"/shop-floor/mine", nil)
	mustStatus(t, res, http.StatusOK, "my work")
	res.JSON(&mine)
	for _, w := range mine.Today {
		if w.WorkCenter.Id != f.saw.Id {
			t.Fatalf("my work at the station names %d", w.WorkCenter.Id)
		}
	}

	// A person's own browser records no station.
	person := h.SignIn(t, everything...)
	act(t, person, wos[0].Id, "start", nil)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE work_order_id = $1 AND station_id IS NULL AND user_id IS NOT NULL`, wos[0].Id); n != 1 {
		t.Fatalf("%d logs without a station, want the person's one", n)
	}
}

// TestStation_LiveTopics is §17's station path through Manufacturing's
// topics: a station hears its own work center and a work order at it, and no
// other — nor any topic not marked for stations.
func TestStation_LiveTopics(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, everything...)
	f := newFloor(t, h, admin)
	_, wos := openOrder(t, admin, "1")
	op := h.atStation(t, everything...)
	serve(t, admin, op.station, f.saw.Id)

	topics := map[string]live.Topic{}
	for _, topic := range manufacturing.Module().LiveTopics(h.Deps()) {
		topics[topic.Kind] = topic
	}
	ctx := context.Background()
	key := func(id int64) string { return strconv.FormatInt(id, 10) }
	if err := live.AuthorizeStation(ctx, h.Deps().Access, op.station, topics["manufacturing.work-center"], key(f.saw.Id)); err != nil {
		t.Errorf("own work center: %v", err)
	}
	if err := live.AuthorizeStation(ctx, h.Deps().Access, op.station, topics["manufacturing.work-order"], key(wos[0].Id)); err != nil {
		t.Errorf("own work order: %v", err)
	}
	for _, refused := range []struct {
		kind, key string
	}{
		{"manufacturing.work-center", key(f.paint.Id)},
		{"manufacturing.work-order", key(wos[1].Id)},
		{"manufacturing.work-center", "abc"},
		{"manufacturing.orders", ""},
		{"manufacturing.order", key(wos[0].OrderId)},
		{"manufacturing.schedule", key(siteMain)},
		{"manufacturing.deviations", ""},
	} {
		if err := live.AuthorizeStation(ctx, h.Deps().Access, op.station, topics[refused.kind], refused.key); err == nil {
			t.Errorf("%s:%s admitted a station", refused.kind, refused.key)
		}
	}
	// Another station hears none of this one's.
	other := h.atStation(t, everything...)
	if err := live.AuthorizeStation(ctx, h.Deps().Access, other.station, topics["manufacturing.work-center"], key(f.saw.Id)); err == nil {
		t.Error("another station heard SAW")
	}
}
