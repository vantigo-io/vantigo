package integration_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// stationOpen are the operations a station's operator reaches when its
// station serves no work center: the lists and the meta, which name none.
var stationOpen = map[string]bool{
	"listManufacturingWorkCenters": true,
	"getManufacturingMyWork":       true,
	"getManufacturingMeta":         true,
}

// TestStation_TheWholeContract walks every operation of every module the
// server mounts as an operator at a shared shop-floor station (manufacturing
// phase 2 M135, §20), the operator an Owner: every operation not marked
// x-vantigo-station is 403 station_scope, and every marked one that names a
// work center, a work order or an order is 403 station_scope too, the station
// serving none. Nothing else answers.
func TestStation_TheWholeContract(t *testing.T) {
	mods := everyModule()
	opts := []modtest.Option{modtest.WithRecorder(contracttest.NewForModule("customers"))}
	for _, m := range mods {
		opts = append(opts, modtest.WithModule(m))
	}
	h := modtest.New(t, opts...)

	// The rows identity writes when a station pairs and an Owner switches in.
	_, user := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, user)
	token := func() (string, []byte) {
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		sum := sha256.Sum256(raw)
		return base64.RawURLEncoding.EncodeToString(raw), sum[:]
	}
	station := uuid.New()
	cookie, cookieHash := token()
	operator, operatorHash := token()
	now := h.Now()
	h.Exec(t, `INSERT INTO identity.stations (id, name, token_hash, token_expires_at, paired_at, created_at)
	           VALUES ($1, 'Station', $2, $3, $4, $4)`, station, cookieHash, now.Add(90*24*time.Hour), now)
	h.Exec(t, `INSERT INTO identity.station_operator_sessions (id, station_id, user_id, token_hash, started_at, last_activity_at)
	           VALUES ($1, $2, $3, $4, $5, $5)`, uuid.New(), station, user, operatorHash, now)
	c := h.Client(t)
	c.SetCookie("vantigo.station", cookie)

	walked := 0
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			if strings.HasPrefix(path, "/api/v1/identity/scim/") {
				continue // the SCIM ingress answers before the router, to a bearer token only
			}
			for method, op := range item.Operations() {
				concrete := concretePath(path, append(item.Parameters, op.Parameters...))
				reqOpts := []modtest.RequestOption{
					modtest.SkipContract("a station's operator walking the contract"),
					modtest.Header("Authorization", "Station "+operator),
				}
				var body any
				if method != http.MethodGet && method != http.MethodDelete && method != http.MethodHead {
					body = map[string]any{}
				}
				if op.RequestBody != nil && op.RequestBody.Value != nil && op.RequestBody.Value.Content.Get("multipart/form-data") != nil {
					body = nil
					reqOpts = append(reqOpts, modtest.RawBody("multipart/form-data; boundary=x", []byte("--x--\r\n")))
				}
				res := c.Do(method, concrete, body, reqOpts...)
				walked++
				if stationOpen[op.OperationID] {
					if res.Status != http.StatusOK {
						t.Errorf("%s %s (%s): %d %s, want 200", method, concrete, op.OperationID, res.Status, res.Body)
					}
					continue
				}
				// A HEAD answers with no body: the status is the refusal.
				if res.Status != http.StatusForbidden || (method != http.MethodHead && codeOf(res) != "station_scope") {
					t.Errorf("%s %s (%s): %d %s, want 403 station_scope", method, concrete, op.OperationID, res.Status, truncate(res.Body))
				}
			}
		}
	}
	if walked < 1000 {
		t.Fatalf("walked %d operations; the walk is broken", walked)
	}
}

// concretePath fills a contract path's parameters: a uuid where the schema
// says so, else an id.
func concretePath(path string, params openapi3.Parameters) string {
	for _, ref := range params {
		if ref == nil || ref.Value == nil || ref.Value.In != "path" {
			continue
		}
		value := "1001"
		if s := ref.Value.Schema; s != nil && s.Value != nil && s.Value.Format == "uuid" {
			value = uuid.NewString()
		}
		path = strings.ReplaceAll(path, "{"+ref.Value.Name+"}", value)
	}
	return path
}

// codeOf is a refusal's code in either shape: the access layer's
// {"error":{"code"}} or a problem's code.
func codeOf(res *modtest.Response) string {
	if code := res.Code(); code != "" {
		return code
	}
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(res.Body, &p)
	return p.Code
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}
