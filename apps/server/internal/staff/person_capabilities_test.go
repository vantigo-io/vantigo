package staff_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The person's capabilities say who the caller is to them (plan Task 7c's
// review, L4): self when the caller's link names the person, isManager when
// the caller manages them today by the one manager rule — and neither for HR,
// an Approver or a colleague — so the page offers a manager's writes on the
// server's word, not a guess.
func TestPerson_CapabilitiesSayWhoTheCallerIs(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	approver, _ := signIn(t, w.h, "staff:view", "staff:approve")
	for _, tc := range []struct {
		name          string
		c             *modtest.Client
		id            int64
		self, manager bool
	}{
		{"Kari on herself", w.kariC, w.kari.Id, true, false},
		{"Bjørn on Kari, his report", w.bossC, w.kari.Id, false, true},
		{"Bjørn on himself", w.bossC, w.boss.Id, true, false},
		{"HR on Kari", w.hr, w.kari.Id, false, false},
		{"an Approver on Kari", approver, w.kari.Id, false, false},
		{"a colleague on Kari", w.viewer, w.kari.Id, false, false},
	} {
		c := getPerson(t, tc.c, tc.id).Capabilities
		if c.Self != tc.self || c.IsManager != tc.manager {
			t.Errorf("%s: self = %v, isManager = %v; want %v, %v", tc.name, c.Self, c.IsManager, tc.self, tc.manager)
		}
	}
}
