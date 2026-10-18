package energy_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is what every test of the metering-point import shares (energy
// import design D6, D7): the wire shapes, a file written the supplier's way,
// the customer directory the import resolves organisation numbers through,
// the upload, the mapping, and the worker driven through RunCycle — nothing
// waits on a real interval.

// importPermissions is what a person who imports metering points and
// attaches them holds: the import's door, and every key a mapping may need.
var importPermissions = []string{
	"energy:import", "energy:metering-points-view", "energy:metering-points-manage",
	"energy:meters-view", "energy:meters-manage",
	"energy:supply-periods-view", "energy:supply-periods-manage",
}

// Synthetic, check-digit-valid organisation numbers and GSRNs: no real
// company, no real metering point.
const (
	orgA = "910000004"
	orgB = "910000012"
	orgC = "910000020"
	orgD = "910000039"
	orgE = "910000047"
	orgF = "910000055"

	gsrn1 = "707057599000000012"
	gsrn2 = "707057599000000029"
	gsrn3 = "707057599000000036"
	gsrn4 = "707057599000000043"
	gsrn5 = "707057599000000050"
	gsrn6 = "707057599000000067"
	gsrn7 = "707057599000000074"
)

// importDirectory is contracts.CustomerDirectory for the import's tests: a
// handful of customers, each holding an organisation number, and the count
// of CustomersByLegalID calls, so a test can prove a run resolves its numbers
// once. Customer answers the same customers, so the supply-period endpoints
// accept them too.
type importDirectory struct {
	mu        sync.Mutex
	customers map[int32]contracts.CustomerEntry
	legal     map[string][]int32
	lookups   atomic.Int32
}

var _ contracts.CustomerDirectory = (*importDirectory)(nil)

// newImportDirectory is the directory every import test starts from:
// customers 1001 (orgA), 1002 (orgB) and 1003 (orgC) active; 1004 (orgD)
// archived; 1005 (orgE) merged into 1001; 1006 and 1007 both holding orgF.
func newImportDirectory() *importDirectory {
	into := int32(1001)
	d := &importDirectory{customers: map[int32]contracts.CustomerEntry{
		1001: {ID: 1001, Name: "Aurora Kraft AS"},
		1002: {ID: 1002, Name: "Bølge Energi AS"},
		1003: {ID: 1003, Name: "Cirrus Drift AS"},
		1004: {ID: 1004, Name: "Dvale Holding AS", Archived: true},
		1005: {ID: 1005, Name: "Eiker Sammenslått AS", Archived: true, MergedInto: &into},
		1006: {ID: 1006, Name: "Fjord Tvilling AS"},
		1007: {ID: 1007, Name: "Fjord Tvilling Avd AS"},
	}, legal: map[string][]int32{
		orgA: {1001}, orgB: {1002}, orgC: {1003}, orgD: {1004}, orgE: {1005}, orgF: {1006, 1007},
	}}
	return d
}

func (d *importDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.customers[id]; ok {
		return &c, nil
	}
	return nil, nil
}

func (d *importDirectory) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []contracts.CustomerEntry{}
	seen := map[int32]bool{}
	for _, id := range ids {
		if c, ok := d.customers[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, c)
		}
	}
	return out, nil
}

func (d *importDirectory) CustomersByLegalID(_ context.Context, country string, ids []string) (map[string][]contracts.CustomerEntry, error) {
	d.lookups.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string][]contracts.CustomerEntry{}
	if !strings.EqualFold(country, "no") {
		return out, nil
	}
	for _, id := range ids {
		for _, customerID := range d.legal[strings.ReplaceAll(id, " ", "")] {
			out[id] = append(out[id], d.customers[customerID])
		}
	}
	return out, nil
}

func (d *importDirectory) archive(id int32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.customers[id]
	c.Archived = true
	d.customers[id] = c
}

func (*importDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (*importDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

func (*importDirectory) BillingProfile(context.Context, int32) (*contracts.CustomerBillingProfile, error) {
	return nil, nil
}

// importHarness is an energy installation with the import's directory, and
// a client holding every key the import may need.
func importHarness(t *testing.T, opts ...modtest.Option) (*modtest.Harness, *modtest.Client, *importDirectory) {
	t.Helper()
	dir := newImportDirectory()
	h := newHarness(t, append([]modtest.Option{modtest.WithDirectory(dir)}, opts...)...)
	return h, h.SignIn(t, importPermissions...), dir
}

// energyImportJSON is EnergyImport as a test reads it.
type energyImportJSON struct {
	ID              int32                       `json:"id"`
	State           string                      `json:"state"`
	FileName        string                      `json:"fileName"`
	Encoding        string                      `json:"encoding"`
	Separator       string                      `json:"separator"`
	Rows            int                         `json:"rows"`
	Header          []string                    `json:"header"`
	Sample          []energyImportSampleRowJSON `json:"sample"`
	Mapping         *energyMappingJSON          `json:"mapping"`
	Suggested       energyMappingJSON           `json:"suggested"`
	Processed       int                         `json:"processed"`
	Created         int                         `json:"created"`
	Updated         int                         `json:"updated"`
	Failed          int                         `json:"failed"`
	Skipped         int                         `json:"skipped"`
	Switched        int                         `json:"switched"`
	Pending         int                         `json:"pending"`
	PendingSwitches int                         `json:"pendingSwitches"`
	Attached        int                         `json:"attached"`
	StopRequested   bool                        `json:"stopRequested"`
	Error           string                      `json:"error"`
	CreatedByName   string                      `json:"createdByName"`
	AttachStart     *time.Time                  `json:"attachStart"`
	RequestedAt     *string                     `json:"requestedAt"`
	StartedAt       *string                     `json:"startedAt"`
	FinishedAt      *string                     `json:"finishedAt"`
}

type energyImportSampleRowJSON struct {
	Row    int                        `json:"row"`
	Cells  []string                   `json:"cells"`
	Errors []energyImportRowErrorJSON `json:"errors"`
}

type energyImportRowErrorJSON struct {
	Column  *int   `json:"column"`
	Message string `json:"message"`
}

type energyMappingJSON struct {
	Mode    string                    `json:"mode"`
	Columns []energyMappingColumnJSON `json:"columns"`
}

type energyMappingColumnJSON struct {
	Field  string  `json:"field"`
	Column *int    `json:"column,omitempty"`
	Value  *string `json:"value,omitempty"`
}

type energyImportRowJSON struct {
	Row                    int                        `json:"row"`
	Cells                  []string                   `json:"cells"`
	Outcome                string                     `json:"outcome"`
	Link                   string                     `json:"link"`
	MeteringPointID        *int32                     `json:"meteringPointId"`
	Gsrn                   string                     `json:"gsrn"`
	CustomerID             *int32                     `json:"customerId"`
	CustomerName           string                     `json:"customerName"`
	SwitchFromCustomerID   *int32                     `json:"switchFromCustomerId"`
	SwitchFromCustomerName string                     `json:"switchFromCustomerName"`
	SupplyStart            *time.Time                 `json:"supplyStart"`
	Errors                 []energyImportRowErrorJSON `json:"errors"`
}

type energyImportRowsJSON struct {
	Data       []energyImportRowJSON `json:"data"`
	Pagination struct {
		TotalCount int `json:"totalCount"`
	} `json:"pagination"`
}

// fileOf is a file written the supplier's way: the byte order mark,
// semicolons, CRLF, a cell quoted when it has to be.
func fileOf(rows ...[]string) []byte {
	var b strings.Builder
	b.WriteString("\ufeff")
	for _, cells := range rows {
		for i, cell := range cells {
			if i > 0 {
				b.WriteByte(';')
			}
			if strings.ContainsAny(cell, ";\"\r\n") {
				cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
			}
			b.WriteString(cell)
		}
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// postEnergyUpload POSTs data as the upload's one file part.
func postEnergyUpload(t *testing.T, c *modtest.Client, data []byte) *modtest.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "points.csv")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return c.Do(http.MethodPost, "/api/v1/energy/imports", nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
}

// energyImportOf decodes an import answer of the status wanted.
func energyImportOf(t *testing.T, r *modtest.Response, want int) energyImportJSON {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d body %s, want %d", r.Status, r.Body, want)
	}
	var imp energyImportJSON
	r.JSON(&imp)
	return imp
}

func uploadEnergyImport(t *testing.T, c *modtest.Client, data []byte) energyImportJSON {
	t.Helper()
	return energyImportOf(t, postEnergyUpload(t, c, data), http.StatusCreated)
}

func energyImportPath(id int32, suffix string) string {
	return fmt.Sprintf("/api/v1/energy/imports/%d%s", id, suffix)
}

// energyConflictCode is a 409's code, failing the test on another status.
func energyConflictCode(t *testing.T, r *modtest.Response) string {
	t.Helper()
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem struct {
		Code string `json:"code"`
	}
	r.JSON(&problem)
	return problem.Code
}

// energyMappingRefusal is a mapping PUT's 400 field map.
func energyMappingRefusal(t *testing.T, r *modtest.Response) map[string][]string {
	t.Helper()
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	return problem.Errors
}

// mappingByFields maps each named field to the header column of the same
// name, in mode.
func mappingByFields(header []string, mode string, fields ...string) energyMappingJSON {
	m := energyMappingJSON{Mode: mode, Columns: []energyMappingColumnJSON{}}
	for _, f := range fields {
		for i, h := range header {
			if h == f {
				col := i
				m.Columns = append(m.Columns, energyMappingColumnJSON{Field: f, Column: &col})
			}
		}
	}
	return m
}

func putEnergyMapping(c *modtest.Client, id int32, mapping any) *modtest.Response {
	return c.Do(http.MethodPut, energyImportPath(id, "/mapping"), mapping)
}

func postEnergyCheck(c *modtest.Client, id int32) *modtest.Response {
	return c.Do(http.MethodPost, energyImportPath(id, "/check"), nil)
}

// postEnergyRun asks for the run, confirming switches when not nil.
func postEnergyRun(c *modtest.Client, id int32, confirm *int) *modtest.Response {
	var body any
	if confirm != nil {
		body = map[string]any{"confirmSwitches": *confirm}
	}
	return c.Do(http.MethodPost, energyImportPath(id, "/run"), body)
}

func postEnergyAttach(c *modtest.Client, id int32, start time.Time, confirm int) *modtest.Response {
	return c.Do(http.MethodPost, energyImportPath(id, "/attach"), map[string]any{"start": start, "confirmSwitches": confirm})
}

func getEnergyImport(t *testing.T, c *modtest.Client, id int32) energyImportJSON {
	t.Helper()
	return energyImportOf(t, c.Do(http.MethodGet, energyImportPath(id, ""), nil), http.StatusOK)
}

// energyImportRows is one import's rows narrowed by query ("" for every
// row), the first page of 500.
func energyImportRows(t *testing.T, c *modtest.Client, id int32, query string) []energyImportRowJSON {
	t.Helper()
	r := c.Do(http.MethodGet, energyImportPath(id, "/rows")+"?pageSize=500"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("rows: status %d body %s, want 200", r.Status, r.Body)
	}
	var page energyImportRowsJSON
	r.JSON(&page)
	return page.Data
}

// runEnergyImportWorker is one cycle of the import worker, which must run.
func runEnergyImportWorker(t *testing.T, h *modtest.Harness) {
	t.Helper()
	if ran, err := energy.NewImportWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
}

// checkEnergyImport uploads data, saves mapping and runs the check, answering
// the checked import.
func checkEnergyImport(t *testing.T, h *modtest.Harness, c *modtest.Client, data []byte, mapping energyMappingJSON) energyImportJSON {
	t.Helper()
	imp := uploadEnergyImport(t, c, data)
	energyImportOf(t, putEnergyMapping(c, imp.ID, mapping), http.StatusOK)
	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	runEnergyImportWorker(t, h)
	checked := getEnergyImport(t, c, imp.ID)
	if checked.State != "checked" {
		t.Fatalf("after the check the import is %q (%s), want checked", checked.State, checked.Error)
	}
	return checked
}

// runEnergyImport is the run of a checked import, its switches confirmed,
// answering the finished import.
func runEnergyImport(t *testing.T, h *modtest.Harness, c *modtest.Client, checked energyImportJSON) energyImportJSON {
	t.Helper()
	switches := checked.Switched
	energyImportOf(t, postEnergyRun(c, checked.ID, &switches), http.StatusOK)
	runEnergyImportWorker(t, h)
	done := getEnergyImport(t, c, checked.ID)
	if done.State != "done" {
		t.Fatalf("after the run the import is %q (%s), want done", done.State, done.Error)
	}
	return done
}

// importEnergyNow is checkEnergyImport followed by the run.
func importEnergyNow(t *testing.T, h *modtest.Harness, c *modtest.Client, data []byte, mapping energyMappingJSON) energyImportJSON {
	t.Helper()
	return runEnergyImport(t, h, c, checkEnergyImport(t, h, c, data, mapping))
}

// pointHeader is the plain file most tests import: every point field and the
// link's two, under their JSON names, which map themselves.
var pointHeader = []string{"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea", "customerLegalId", "supplyStart"}

// pointRow is one row under pointHeader.
func pointRow(gsrn, legalID, start string) []string {
	return []string{gsrn, "M-" + gsrn[len(gsrn)-4:], "Storgata 1", "0150", "Oslo", "NO1", legalID, start}
}

// fullMapping maps every column of pointHeader in mode.
func fullMapping(mode string) energyMappingJSON {
	return mappingByFields(pointHeader, mode, pointHeader...)
}

// pointByGsrn is the metering point holding a GSRN, by SQL.
func pointByGsrn(t *testing.T, h *modtest.Harness, gsrn string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT id FROM energy.metering_points WHERE gsrn = $1`, gsrn)
}

// activeCustomer is the customer of a point's open Active period, 0 for none.
func activeCustomer(t *testing.T, h *modtest.Harness, gsrn string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT COALESCE((SELECT p.customer_id FROM energy.supply_periods p
		JOIN energy.metering_points mp ON mp.id = p.metering_point_id
		WHERE mp.gsrn = $1 AND p.status = 'Active' AND p."end" IS NULL), 0)`, gsrn)
}

// assignByHand creates a supply period through the endpoint.
func assignByHand(t *testing.T, c *modtest.Client, pointID, customerID int32, start time.Time) {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", pointID),
		map[string]any{"customerId": customerID, "start": start})
	if r.Status != http.StatusCreated {
		t.Fatalf("assign by hand: status %d body %s, want 201", r.Status, r.Body)
	}
}

// createPointByHand creates a metering point through the endpoint.
func createPointByHand(t *testing.T, c *modtest.Client, gsrn, meterNumber string) int32 {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", newMeteringPointBody(gsrn, meterNumber))
	if r.Status != http.StatusCreated {
		t.Fatalf("create by hand: status %d body %s, want 201", r.Status, r.Body)
	}
	var point meteringPointJSON
	r.JSON(&point)
	return point.Id
}

// rowNamed is the row numbered n among rows.
func rowNamed(t *testing.T, rows []energyImportRowJSON, n int) energyImportRowJSON {
	t.Helper()
	for _, r := range rows {
		if r.Row == n {
			return r
		}
	}
	t.Fatalf("no row %d among %d", n, len(rows))
	return energyImportRowJSON{}
}

func ptr[T any](v T) *T { return &v }

// waitForALockWait waits until some other session of the harness's database
// is waiting on a lock — the request or the worker a test started in a
// goroutine has reached the statement the test's open transaction holds up.
func waitForALockWait(t *testing.T, h *modtest.Harness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.Count(t, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no session came to wait on a lock")
}
