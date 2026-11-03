package energy_test

import (
	"fmt"
	"net/http"
	"testing"
)

// listedPointJSON is a list item as these tests read it: the point and
// its customer now.
type listedPointJSON struct {
	Id              int32  `json:"id"`
	Gsrn            string `json:"gsrn"`
	PriceArea       string `json:"priceArea"`
	CurrentCustomer *struct {
		Id   int32   `json:"id"`
		Name *string `json:"name"`
	} `json:"currentCustomer"`
}

// listPoints is GET /energy/metering-points with query, its items and total,
// failing on anything but 200.
func listPoints(t *testing.T, do func(string) (int, []listedPointJSON, int), query string) []listedPointJSON {
	t.Helper()
	status, items, total := do(query)
	if status != http.StatusOK {
		t.Fatalf("%s: status %d, want 200", query, status)
	}
	if total != len(items) {
		t.Errorf("%s: total %d, %d listed", query, total, len(items))
	}
	return items
}

func pointIDs(items []listedPointJSON) []int32 {
	out := make([]int32, 0, len(items))
	for _, p := range items {
		out = append(out, p.Id)
	}
	return out
}

func TestGetMeteringPoints_SortsFiltersAndNamesTheCustomerNow(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	now := h.Now()
	a := createPointByHand(t, c, gsrn3, "M-B")
	b := createPointByHand(t, c, gsrn1, "M-C")
	d := createPointByHand(t, c, gsrn2, "M-A")
	h.Exec(t, `UPDATE energy.metering_points SET price_area = 'NO4', connection_status = 'New' WHERE id = $1`, d)
	assignByHand(t, c, a, 1001, now.AddDate(0, -1, 0))
	assignByHand(t, c, b, 1002, now.AddDate(0, 1, 0)) // starts later: no customer now

	do := func(query string) (int, []listedPointJSON, int) {
		r := c.Do(http.MethodGet, "/api/v1/energy/metering-points"+query, nil)
		var page struct {
			Data       []listedPointJSON `json:"data"`
			Pagination struct {
				TotalCount int `json:"totalCount"`
			} `json:"pagination"`
		}
		if r.Status == http.StatusOK {
			r.JSON(&page)
		}
		return r.Status, page.Data, page.Pagination.TotalCount
	}
	for _, tc := range []struct {
		query string
		want  []int32
	}{
		{"", []int32{a, b, d}},
		{"?sortBy=gsrn", []int32{b, d, a}},
		{"?sortBy=gsrn&sortDirection=desc", []int32{a, d, b}},
		{"?sortBy=meterNumber", []int32{d, a, b}},
		{"?sortBy=priceArea&sortDirection=desc", []int32{d, b, a}}, // ties: the id, descending too
		{"?priceArea=NO4", []int32{d}},
		{"?connectionStatus=Connected", []int32{a, b}},
		{"?customerId=1001", []int32{a}},
		{"?customerId=1002", nil},
	} {
		if got := pointIDs(listPoints(t, do, tc.query)); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%q = %v, want %v", tc.query, got, tc.want)
		}
	}

	items := listPoints(t, do, "")
	if cc := items[0].CurrentCustomer; cc == nil || cc.Id != 1001 || cc.Name == nil || *cc.Name != "Aurora Kraft AS" {
		t.Errorf("point a's customer now = %+v, want 1001 named Aurora Kraft AS", cc)
	}
	if items[1].CurrentCustomer != nil || items[2].CurrentCustomer != nil {
		t.Errorf("points without a customer now carried one: %+v, %+v", items[1].CurrentCustomer, items[2].CurrentCustomer)
	}

	for _, query := range []string{"?sortBy=city", "?sortDirection=up", "?priceArea=north", "?connectionStatus=connected"} {
		if status, _, _ := do(query); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", query, status)
		}
	}
}

// TestGetMeteringPoints_TheCustomerNeedsSupplyPeriodsView pins that who a
// point supplies is a supply period's fact: without
// energy:supply-periods-view the list leaves the customer out, and the
// customer filter answers 403.
func TestGetMeteringPoints_TheCustomerNeedsSupplyPeriodsView(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	point := createPointByHand(t, c, gsrn1, "M1")
	assignByHand(t, c, point, 1001, h.Now().AddDate(0, -1, 0))

	viewer := h.SignIn(t, "energy:metering-points-view", "energy:meters-view")
	r := viewer.Do(http.MethodGet, "/api/v1/energy/metering-points", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list: status %d body %s", r.Status, r.Body)
	}
	var page struct {
		Data []listedPointJSON `json:"data"`
	}
	r.JSON(&page)
	if len(page.Data) != 1 || page.Data[0].CurrentCustomer != nil {
		t.Errorf("list without supply-periods-view = %+v, want the point without its customer", page.Data)
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/energy/metering-points?customerId=1001", nil); r.Status != http.StatusForbidden {
		t.Errorf("customerId without supply-periods-view: status %d, want 403", r.Status)
	}
}
