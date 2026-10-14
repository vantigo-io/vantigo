package staff_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// People's "on leave today" (design §7, plan M3): a listed person carries
// onLeaveToday when an approved or registered day row of any kind covers
// today — the sykmelding as well as the ferie, so the flag says no more than
// the calendar's "absent" bar shows a colleague — and never for a request.
// It is the same for every reader of the list. The harness's today is
// Saturday 12 September 2026.
func TestPeopleList_OnLeaveToday(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2026-09-10", "2026-09-16"))
	approveRow(t, h, holiday.Id)
	register(t, w.hr, w.boss.Id, absence(t, h, "sykmelding", "2026-09-11", "2026-09-14"))
	waiting, _ := employed(t, w.hr, "Venter", "2020-01-01", terms())
	register(t, w.hr, waiting.Id, absence(t, h, "velferdspermisjon", "2026-09-11", "2026-09-14")) // requested
	past, _ := employed(t, w.hr, "Tidligere", "2020-01-01", terms())
	register(t, w.hr, past.Id, absence(t, h, "sykmelding", "2026-09-01", "2026-09-04"))

	want := map[int64]bool{w.kari.Id: true, w.boss.Id: true, waiting.Id: false, past.Id: false}
	for name, c := range map[string]*modtest.Client{"staff:manage": w.hr, "staff:view": w.viewer} {
		got := map[int64]bool{}
		for _, p := range listPeople(t, c, "").Data {
			got[p.Id] = p.OnLeaveToday
		}
		for id, on := range want {
			if got[id] != on {
				t.Errorf("%s: person %d onLeaveToday = %v, want %v", name, id, got[id], on)
			}
		}
	}
}
