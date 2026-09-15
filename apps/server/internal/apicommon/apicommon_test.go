package apicommon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteDecodeError_IsABare400ProblemThatEchoesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteDecodeError(rec, httptest.NewRequest(http.MethodPost, "/api/v1/things", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if strings.Contains(rec.Body.String(), "not json") {
		t.Errorf("body echoes the undecodable request: %s", rec.Body.String())
	}
}

func TestProblem_DefaultsTo400AndProblemStatusOverridesIt(t *testing.T) {
	p := Problem("Invalid thing", "Because.")
	if *p.Status != 400 || *p.Title != "Invalid thing" || *p.Detail != "Because." {
		t.Errorf("Problem = %+v, want status 400 with the given title and detail", p)
	}
	if got := *ProblemStatus("Conflict", "Taken.", http.StatusConflict).Status; got != 409 {
		t.Errorf("ProblemStatus status = %d, want 409", got)
	}
}

func TestValidationProblem_CarriesTheFieldMapUnder400(t *testing.T) {
	errs := map[string][]string{"name": {"A name is required."}}
	v := ValidationProblem("Invalid thing", errs)
	if *v.Status != 400 || *v.Title != "Invalid thing" {
		t.Errorf("ValidationProblem = %+v, want status 400 with the given title", v)
	}
	if got := (*v.Errors)["name"]; len(got) != 1 || got[0] != "A name is required." {
		t.Errorf("Errors = %v, want the given map", *v.Errors)
	}
}

func TestForbiddenBody_IsTheAccessLayersShape(t *testing.T) {
	raw, err := json.Marshal(ForbiddenBody())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"error":{"code":"forbidden","message":"You do not have permission to access this resource."}}`
	if string(raw) != want {
		t.Errorf("ForbiddenBody = %s, want %s", raw, want)
	}
}

func TestPtr_PointsAtACopy(t *testing.T) {
	v := 3
	p := Ptr(v)
	if p == &v || *p != 3 {
		t.Errorf("Ptr(3) = %p (source %p) pointing at %d, want a distinct pointer to 3", p, &v, *p)
	}
}

func TestPagination_DerivesTotalsAndFlags(t *testing.T) {
	cases := []struct {
		name               string
		page, size, total  int32
		wantPages          int32
		wantNext, wantPrev bool
	}{
		{"first of three", 1, 25, 60, 3, true, false},
		{"middle", 2, 25, 60, 3, true, true},
		{"last", 3, 25, 60, 3, false, true},
		{"empty list", 1, 25, 0, 0, false, false},
		{"page past the end of an empty list", 2, 25, 0, 0, false, false},
		{"exact multiple", 2, 10, 20, 2, false, true},
		{"zero page size", 1, 0, 5, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Pagination(tc.page, tc.size, tc.total)
			if m.Page != tc.page || m.PageSize != tc.size || m.TotalCount != tc.total {
				t.Errorf("echoed inputs = %+v", m)
			}
			if m.TotalPages != tc.wantPages || m.HasNextPage != tc.wantNext || m.HasPreviousPage != tc.wantPrev {
				t.Errorf("Pagination(%d, %d, %d) = pages %d next %v prev %v, want pages %d next %v prev %v",
					tc.page, tc.size, tc.total, m.TotalPages, m.HasNextPage, m.HasPreviousPage, tc.wantPages, tc.wantNext, tc.wantPrev)
			}
		})
	}
}

func TestNormalizePeriod(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	at := func(day int) *time.Time {
		d := time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC)
		return &d
	}

	t.Run("defaults to the last 30 days ending now", func(t *testing.T) {
		from, to, prev, ok := NormalizePeriod(nil, nil, now)
		if !ok || !to.Equal(now) || !from.Equal(now.AddDate(0, 0, -30)) {
			t.Errorf("= %v..%v ok %v, want %v..%v", from, to, ok, now.AddDate(0, 0, -30), now)
		}
		if !prev.Equal(now.AddDate(0, 0, -60)) {
			t.Errorf("previousFrom = %v, want %v", prev, now.AddDate(0, 0, -60))
		}
	})

	t.Run("previous window has the requested length", func(t *testing.T) {
		from, to, prev, ok := NormalizePeriod(at(10), at(14), now)
		if !ok || !from.Equal(*at(10)) || !to.Equal(*at(14)) {
			t.Fatalf("= %v..%v ok %v", from, to, ok)
		}
		if !prev.Equal(*at(6)) {
			t.Errorf("previousFrom = %v, want %v", prev, *at(6))
		}
	})

	t.Run("from equal to to is valid", func(t *testing.T) {
		if _, _, _, ok := NormalizePeriod(at(10), at(10), now); !ok {
			t.Error("from == to rejected, want accepted")
		}
	})

	t.Run("from after to is not", func(t *testing.T) {
		if _, _, _, ok := NormalizePeriod(at(11), at(10), now); ok {
			t.Error("from > to accepted, want rejected")
		}
	})

	t.Run("the invalid-period problem is a 400 with the fixed text", func(t *testing.T) {
		p := InvalidPeriod()
		if *p.Status != 400 || *p.Title != InvalidPeriodTitle || *p.Detail != InvalidPeriodDetail {
			t.Errorf("InvalidPeriod = %+v", p)
		}
	})
}
