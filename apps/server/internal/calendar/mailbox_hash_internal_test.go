package calendar

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestNewMailboxItem_HashesAnAbsenceAsBefore00110: the neutral show-as words
// hash as Graph's did before migration 00110, so the hashes stored then still
// match and no pushed absence is rewritten for the rename alone.
func TestNewMailboxItem_HashesAnAbsenceAsBefore00110(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ showAs, want string }{
		// The hashes the code before 00110 made of "oof" and "workingElsewhere".
		{contracts.ShowAsOutOfOffice, "b6e62f087b6464394f466071763e44e4334f3a86b79d094ce4e8aa9dd934fe8f"},
		{contracts.ShowAsWorkingElsewhere, "a7e175904ea94807dbcb715bc4c722b0569594c40d37c48f8306c474da370b46"},
	} {
		ev := contracts.MailboxEvent{
			Subject: "Ferie", AllDay: true, ShowAs: tc.showAs, TimeZone: "Europe/Oslo",
			StartDate: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		}
		it, err := newMailboxItem("absence:1", ev, ev.StartDate, "default")
		if err != nil {
			t.Fatal(err)
		}
		if it.hash != tc.want {
			t.Errorf("%s hashes %s, want the pre-00110 %s", tc.showAs, it.hash, tc.want)
		}
		if it.event.ShowAs != tc.showAs {
			t.Errorf("the copy's show-as = %q, want %q: the legacy word is the hash's alone", it.event.ShowAs, tc.showAs)
		}
	}
}
