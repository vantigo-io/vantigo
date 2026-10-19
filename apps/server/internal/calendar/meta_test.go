package calendar_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

func TestMeta_AnswersTheBusinessZoneTodayTheColleagueKeyAndEverySource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.SetBusinessTimeZone(t, "Pacific/Auckland")
	plain, _ := signIn(t, h)
	viewer, _ := signIn(t, h, "calendar:view-colleagues")

	var meta gen.CalendarMeta
	do(t, plain, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	loc, _ := time.LoadLocation("Pacific/Auckland")
	if meta.TimeZone != "Pacific/Auckland" || meta.Today.String() != h.Now().In(loc).Format(time.DateOnly) || meta.CanViewColleagues {
		t.Errorf("meta = %+v", meta)
	}
	if len(meta.Sources) != 6 || meta.Sources[0] != (gen.CalendarSourceKind{Kind: "customers.follow-up", Module: "customers"}) ||
		meta.Sources[4] != (gen.CalendarSourceKind{Kind: "time.entry", Module: "time"}) {
		t.Errorf("sources = %+v", meta.Sources)
	}
	do(t, viewer, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if !meta.CanViewColleagues {
		t.Error("canViewColleagues is false for a holder of calendar:view-colleagues")
	}
}
