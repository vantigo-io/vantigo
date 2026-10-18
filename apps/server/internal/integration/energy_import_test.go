package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestEnergyImport_ResolvesTheRealCustomers is the metering-point import's
// customer link (energy import design D4) against the real customers module:
// Compose hands energy the customers module's own directory, and the import
// resolves the file's organisation numbers through its CustomersByLegalID —
// the duplicate-identity guard's comparison, whitespace and all. One holder is
// assigned or left pending, two holders are the row's error naming both, a
// customer merged away is refused naming the survivor, and a number nobody
// holds is refused; the pending row is then attached, and the row names its
// customer through the same directory. The worker is built the way worker
// mode builds it, so this also proves module.Workers hands it the directory.
func TestEnergyImport_ResolvesTheRealCustomers(t *testing.T) {
	t.Parallel()
	// A manual identity never asks Brreg; anything that did would fail here
	// rather than reach the network.
	h := newInstallationWith(t, []modtest.Option{modtest.WithTransport(refuseNetwork{})}, modCustomers, modEnergy)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:legal-identity-manage", "customers:merge",
		"energy:import", "energy:metering-points-view", "energy:metering-points-manage",
		"energy:meters-view", "energy:meters-manage", "energy:supply-periods-view", "energy:supply-periods-manage",
	)
	type created struct {
		Id int32 `json:"id"`
	}
	customer := func(name, orgnr string, allowDuplicate bool) int32 {
		var c created
		okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{
			"name": name, "allowDuplicateIdentity": allowDuplicate,
			"identity": map[string]any{"country": "NO", "type": "business", "id": orgnr, "name": name, "source": "manual"},
		}, &c)
		return c.Id
	}
	aurora := customer("Aurora Kraft AS", "910000004", false)
	twinA := customer("Fjord Tvilling AS", "910000055", false)
	twinB := customer("Fjord Tvilling Avd AS", "910000055", true)
	eiker := customer("Eiker AS", "910000047", false)
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", aurora), map[string]any{"sourceId": eiker}, nil)

	file := strings.Join([]string{
		"gsrn;meterNumber;streetAddress;postalCode;city;priceArea;customerLegalId;supplyStart",
		"707057599000000012;M1;Storgata 1;0150;Oslo;NO1;910 000 004;01.01.2026",
		"707057599000000029;M2;Storgata 2;0150;Oslo;NO1;910000004;",
		"707057599000000036;M3;Storgata 3;0150;Oslo;NO1;910000055;01.01.2026",
		"707057599000000043;M4;Storgata 4;0150;Oslo;NO1;910000047;01.01.2026",
		"707057599000000050;M5;Storgata 5;0150;Oslo;NO1;910000063;01.01.2026",
	}, "\r\n") + "\r\n"
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "points.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(file)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := admin.Do(http.MethodPost, "/api/v1/energy/imports", nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
	if r.Status != http.StatusCreated {
		t.Fatalf("upload: status %d body %s", r.Status, r.Body)
	}
	type importJSON struct {
		ID        int32          `json:"id"`
		State     string         `json:"state"`
		Suggested map[string]any `json:"suggested"`
		Created   int            `json:"created"`
		Failed    int            `json:"failed"`
		Switched  int            `json:"switched"`
		Pending   int            `json:"pending"`
		Attached  int            `json:"attached"`
	}
	var imp importJSON
	r.JSON(&imp)
	path := fmt.Sprintf("/api/v1/energy/imports/%d", imp.ID)
	// The worker as worker mode builds it: module.Workers over the modules,
	// which hands it the customers module's own directory — the harness's
	// Deps carry only the fake every installation here is preset with.
	var worker *energy.ImportWorker
	for _, w := range module.Workers(h.Deps(), customers.Module(), energy.Module()) {
		if iw, ok := w.(*energy.ImportWorker); ok {
			worker = iw
		}
	}
	if worker == nil {
		t.Fatal("module.Workers built no energy import worker")
	}
	cycle := func() {
		t.Helper()
		if ran, err := worker.RunCycle(context.Background()); err != nil || !ran {
			t.Fatalf("RunCycle = %v, %v", ran, err)
		}
	}
	okJSON(t, admin, http.MethodPut, path+"/mapping", imp.Suggested, nil)
	okJSON(t, admin, http.MethodPost, path+"/check", nil, nil)
	cycle()
	okJSON(t, admin, http.MethodPost, path+"/run", map[string]any{"confirmSwitches": 0}, nil)
	cycle()
	okJSON(t, admin, http.MethodGet, path, nil, &imp)
	if imp.State != "done" || imp.Created != 2 || imp.Failed != 3 || imp.Pending != 1 {
		t.Fatalf("done = %+v, want two created, three refused and one pending", imp)
	}

	var rows struct {
		Data []struct {
			Row          int    `json:"row"`
			Link         string `json:"link"`
			CustomerID   *int32 `json:"customerId"`
			CustomerName string `json:"customerName"`
			Errors       []struct {
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"data"`
	}
	okJSON(t, admin, http.MethodGet, path+"/rows", nil, &rows)
	want := map[int]string{
		3: fmt.Sprintf("2 customers have organisation number 910000055 (%d Fjord Tvilling AS, %d Fjord Tvilling Avd AS); the file cannot say which one is meant", twinA, twinB),
		4: fmt.Sprintf("Customer %d Eiker AS was merged into customer %d and takes no more supply periods", eiker, aurora),
		5: "No customer has organisation number 910000063",
	}
	for _, row := range rows.Data {
		switch {
		case row.Row == 1 && (row.Link != "assigned" || row.CustomerID == nil || *row.CustomerID != aurora || row.CustomerName != "Aurora Kraft AS"):
			t.Errorf("row 1 = %+v, want assigned to Aurora Kraft AS", row)
		case row.Row == 2 && row.Link != "pending":
			t.Errorf("row 2 = %+v, want pending", row)
		case want[row.Row] != "" && (len(row.Errors) != 1 || row.Errors[0].Message != want[row.Row]):
			t.Errorf("row %d errors %+v, want %q", row.Row, row.Errors, want[row.Row])
		}
	}

	okJSON(t, admin, http.MethodPost, path+"/attach", map[string]any{"start": time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), "confirmSwitches": 0}, nil)
	cycle()
	okJSON(t, admin, http.MethodGet, path, nil, &imp)
	if imp.State != "done" || imp.Attached != 1 || imp.Pending != 0 {
		t.Errorf("after the attach = %+v, want the pending point attached", imp)
	}
	var periods []struct {
		CustomerID int32     `json:"customerId"`
		Start      time.Time `json:"start"`
	}
	var points struct {
		Data []struct {
			Id   int32  `json:"id"`
			Gsrn string `json:"gsrn"`
		} `json:"data"`
	}
	okJSON(t, admin, http.MethodGet, "/api/v1/energy/metering-points?search=707057599000000029", nil, &points)
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", points.Data[0].Id), nil, &periods)
	if len(periods) != 1 || periods[0].CustomerID != aurora || !periods[0].Start.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("periods = %+v, want Aurora Kraft AS from 1 July", periods)
	}
}

// refuseNetwork is a transport that answers every request with an error.
type refuseNetwork struct{}

func (refuseNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("integration: no network")
}
