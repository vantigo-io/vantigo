package calendar_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func contactsOf(id int32) string { return contactsPath + "?customerId=" + strconv.Itoa(int(id)) }

func TestContacts_AnswersTheCustomersContactsWithAnAddressUnderThePair(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithDirectory(newDirectory()))
	both, _ := signIn(t, h, "customers:contacts-view", "customers:associations-view")
	var got []gen.CalendarContact
	do(t, both, http.MethodGet, contactsOf(customer), nil, http.StatusOK, &got)
	want := []gen.CalendarContact{{Id: olaID, Name: "Ola Hansen", Email: olaMail}, {Id: siriID, Name: "Siri Dahl", Email: siriMail}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("contacts = %+v, want %+v", got, want)
	}
	if r := both.Do(http.MethodGet, contactsOf(99), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("an unknown customer = %d %s, want the bare 404", r.Status, r.Body)
	}
	for name, keys := range map[string][]string{
		"neither":                 nil,
		"contacts-view alone":     {"customers:contacts-view"},
		"associations-view alone": {"customers:associations-view"},
	} {
		c, _ := signIn(t, h, keys...)
		if r := c.Do(http.MethodGet, contactsOf(customer), nil); r.Status != http.StatusForbidden {
			t.Errorf("with %s = %d %s, want 403", name, r.Status, r.Body)
		}
	}

	off := newHarness(t)
	c, _ := signIn(t, off, "customers:contacts-view", "customers:associations-view")
	do(t, c, http.MethodGet, contactsOf(customer), nil, http.StatusOK, &got)
	if len(got) != 0 {
		t.Errorf("with Customers off = %+v, want none", got)
	}
}
