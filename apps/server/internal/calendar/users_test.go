package calendar_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

func TestUsers_SearchesActiveUsersByNameForAPlainCalendarUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	ann := directoryUser(t, h, "Annika Zeta", true)
	directoryUser(t, h, "Annika Gone", false)
	directoryUser(t, h, "Bjørn Zeta", true)

	var found []gen.CalendarUserRef
	do(t, c, http.MethodGet, usersPath+"?query="+url.QueryEscape("annika"), nil, http.StatusOK, &found)
	if len(found) != 1 || found[0].Id != ann || found[0].DisplayName != "Annika Zeta" {
		t.Errorf("search = %+v, want Annika Zeta alone", found)
	}
	do(t, c, http.MethodGet, usersPath+"?limit=2", nil, http.StatusOK, &found)
	if len(found) != 2 {
		t.Errorf("first page of 2 = %+v", found)
	}
	for _, limit := range []string{"0", "51"} {
		fieldRefused(t, c.Do(http.MethodGet, usersPath+"?limit="+limit, nil), "limit")
	}
}
