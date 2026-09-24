package customers_test

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// importAtTheCapBudget is what a real run of maxImportRows rows may take
// (design D3's "measure it and say what it takes"). Sixty seconds, well inside
// the ~100 s after which the proxy in front of a hosted installation
// (cloudflared) gives up on the request. A run cut off there does not go on:
// the server cancels a request whose connection went, and the import stops
// before its next row (importRows) — but every row before it stays committed,
// the caller gets no result saying which, and sending the file again creates
// those customers a second time (docs/customers.md, "A run cut off"). The run
// logs what it actually took, and that number is what docs/customers.md quotes.
const importAtTheCapBudget = 60 * time.Second

// importCapSlice is how many rows one request of this test carries. modtest's
// client gives every request a hard 30 s (modtest/client.go, clientTimeout),
// so the 5000 rows go as five files of 1000 and the five times are summed: the
// per-row cost is the same, since each row is its own transaction either way.
const importCapSlice = 1000

// TestPostCustomersImport_AtTheCap is a real run of 5000 creates, every row
// carrying contact info, a postal address, a billing profile and a tag — the
// most a first onboarding does per row. Not parallel, so the timing is this
// test's alone. A dry run is the same transactions rolled back, so it costs the
// same and is not timed separately.
//
// A measurement, not a regression test, so it is opt-in: CI runs every package
// under -race on a shared runner, where a Go-side slowdown of several times
// would fail a 60 s budget that says nothing about the import. Run it
// deliberately, on the CI runner's four CPUs and without -race, against the
// 60 s budget:
//
//	VANTIGO_IMPORT_TIMING=1 taskset -c 0-3 go test -count=1 -v -run TestPostCustomersImport_AtTheCap ./internal/customers/
func TestPostCustomersImport_AtTheCap(t *testing.T) {
	if os.Getenv("VANTIGO_IMPORT_TIMING") == "" {
		t.Skip("a timing run: set VANTIGO_IMPORT_TIMING=1 and run with taskset -c 0-3, without -race (60 s budget)")
	}
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createTag(t, c, map[string]any{"name": "VIP"})
	header := cells([]string{"name"}, contactHeader, postalHeader, billingHeader, []string{"tags"})

	var took time.Duration
	created := 0
	for slice := 0; slice < 5000/importCapSlice; slice++ {
		rows := [][]string{header}
		for i := slice*importCapSlice + 1; i <= (slice+1)*importCapSlice; i++ {
			rows = append(rows, cells(
				[]string{fmt.Sprintf("Kunde %04d AS", i)},
				[]string{fmt.Sprintf("kunde%d@example.no", i), "", ""},
				[]string{fmt.Sprintf("Gate %d", i), "", "0155", "Oslo", "", "no"},
				[]string{"", "", "30", "NOK", "nb", "email", "", "", "", "", "1250,00"},
				[]string{"VIP"},
			))
		}
		start := time.Now()
		result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(rows...)))
		took += time.Since(start)
		if result.Failed != 0 {
			t.Fatalf("slice %d: result = %+v, want every row created", slice, result)
		}
		created += result.Created
	}
	t.Logf("5000 rows as %d requests of %d: %s in all", 5000/importCapSlice, importCapSlice, took.Round(time.Millisecond))

	if created != 5000 {
		t.Errorf("created %d, want 5000", created)
	}
	if took > importAtTheCapBudget {
		t.Errorf("5000 rows took %s; want within %s — see maxImportRows's comment (import.go) for lowering the cap", took, importAtTheCapBudget)
	}
}
