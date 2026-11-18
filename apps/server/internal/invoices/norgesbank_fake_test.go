package invoices_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNorgesBank is Norges Bank's data API as the rates client calls it
// (plan C12): GET {base}/EXR/B.{CUR+CUR}.NOK.SP?format=sdmx-json&startPeriod
// &endPeriod, answered in SDMX-JSON with the two traps of the real one — the
// series' BASE_CUR values in another order than the request's, and
// UNIT_MULT's values [2, 0], so a currency quoted per 100 points at index 0.
// A range with no rows answers the real 404 body. FailOn makes every request
// naming a currency answer 500 until it is cleared, never only the next one
// (memory: Google fake FailNext vs token cache).
type fakeNorgesBank struct {
	srv *httptest.Server
	mu  sync.Mutex
	// rates is NOK per one unit, as text, by currency and day.
	rates    map[string]map[string]string
	failOn   map[string]int
	requests []string
}

// perHundred are the currencies Norges Bank quotes per 100 units.
var perHundred = []string{"DKK", "JPY", "SEK"}

func newFakeNorgesBank(t *testing.T) *fakeNorgesBank {
	t.Helper()
	f := &fakeNorgesBank{rates: map[string]map[string]string{}, failOn: map[string]int{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the base INVOICES_RATES_URL points at.
func (f *fakeNorgesBank) URL() string { return f.srv.URL + "/api/data" }

// Transport trusts the fake's certificate.
func (f *fakeNorgesBank) Transport() http.RoundTripper { return f.srv.Client().Transport }

// Set publishes currency's rate (NOK per one unit, "11.7234") on day.
func (f *fakeNorgesBank) Set(currency, day, rate string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rates[currency] == nil {
		f.rates[currency] = map[string]string{}
	}
	f.rates[currency][day] = rate
}

// SetRange publishes rate for currency on every weekday from from to to.
func (f *fakeNorgesBank) SetRange(currency, from, to, rate string) {
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			f.Set(currency, d.Format(time.DateOnly), rate)
		}
	}
}

// FailOn makes requests naming currency answer status until cleared with 0.
func (f *fakeNorgesBank) FailOn(currency string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if status == 0 {
		delete(f.failOn, currency)
		return
	}
	f.failOn[currency] = status
}

// Requests is every request's path and query, in order.
func (f *fakeNorgesBank) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeNorgesBank) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.RequestURI())
	key := strings.TrimPrefix(r.URL.Path, "/api/data/EXR/")
	parts := strings.Split(key, ".")
	if len(parts) != 4 || parts[0] != "B" || parts[2] != "NOK" || parts[3] != "SP" || r.URL.Query().Get("format") != "sdmx-json" ||
		r.URL.Query().Has("detail") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	currencies := strings.Split(parts[1], "+")
	for _, c := range currencies {
		if status := f.failOn[c]; status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"errors":[{"code":500,"message":"Internal error"}]}`))
			return
		}
	}
	from, _ := time.Parse(time.DateOnly, r.URL.Query().Get("startPeriod"))
	to, _ := time.Parse(time.DateOnly, r.URL.Query().Get("endPeriod"))
	// The answer's order is not the request's (trap 1): reversed.
	slices.Sort(currencies)
	slices.Reverse(currencies)
	var days []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		for _, c := range currencies {
			if _, ok := f.rates[c][day]; ok {
				days = append(days, day)
				break
			}
		}
	}
	if len(days) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":404,"message":"No data for data query against the dataflow: urn:sdmx:org.sdmx.infomodel.datastructure.Dataflow=NB:EXR(1.0)"}]}`))
		return
	}
	type value struct {
		ID string `json:"id"`
	}
	baseValues := []value{}
	series := map[string]any{}
	for i, c := range currencies {
		baseValues = append(baseValues, value{ID: c})
		mult, scale := 1, "1"
		if slices.Contains(perHundred, c) {
			mult, scale = 0, "100"
		}
		obs := map[string][]string{}
		for di, day := range days {
			if rate, ok := f.rates[c][day]; ok {
				obs[nbIndex(di)] = []string{scaled(rate, scale)}
			}
		}
		series["0:"+nbIndex(i)+":0:0"] = map[string]any{"attributes": []int{0, 0, mult, 0}, "observations": obs}
	}
	dayValues := []value{}
	for _, d := range days {
		dayValues = append(dayValues, value{ID: d})
	}
	body := map[string]any{
		"meta": map[string]any{},
		"data": map[string]any{
			"dataSets": []any{map[string]any{"series": series}},
			"structure": map[string]any{
				"dimensions": map[string]any{
					"series": []any{
						map[string]any{"id": "FREQ", "values": []value{{ID: "B"}}},
						map[string]any{"id": "BASE_CUR", "values": baseValues},
						map[string]any{"id": "QUOTE_CUR", "values": []value{{ID: "NOK"}}},
						map[string]any{"id": "TENOR", "values": []value{{ID: "SP"}}},
					},
					"observation": []any{map[string]any{"id": "TIME_PERIOD", "values": dayValues}},
				},
				"attributes": map[string]any{
					"series": []any{
						map[string]any{"id": "DECIMALS", "values": []value{{ID: "4"}}},
						map[string]any{"id": "CALCULATED", "values": []value{{ID: "false"}}},
						map[string]any{"id": "UNIT_MULT", "values": []value{{ID: "2"}, {ID: "0"}}},
						map[string]any{"id": "COLLECTION", "values": []value{{ID: "C"}}},
					},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/vnd.sdmx.data+json; version=1.0")
	_ = json.NewEncoder(w).Encode(body)
}

func nbIndex(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// scaled is rate as quoted per scale units: "0.059691" per 100 is
// "5.9691".
func scaled(rate, scale string) string {
	if scale == "1" {
		return rate
	}
	whole, frac, _ := strings.Cut(rate, ".")
	frac += "00"
	digits := strings.TrimLeft(whole+frac[:2], "0")
	if digits == "" {
		digits = "0"
	}
	if rest := strings.TrimRight(frac[2:], "0"); rest != "" {
		return digits + "." + rest
	}
	return digits
}
