package energy_test

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestConsumption_WritesAsTheRuntimeRole writes consumption with the
// installation connected as the least-privilege role the installation guide
// prescribes, which owns no table and may create none in the energy schema.
// Every write first makes sure its month's partition exists, and that is DDL:
// energy.ensure_consumption_partition is SECURITY DEFINER (00220) so it runs
// as the owner. A manual interval in a month the baseline made, and an
// import into months it did not, both land.
func TestConsumption_WritesAsTheRuntimeRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithRuntimeRole())
	c := h.SignIn(t, consumptionImportPermissions...)
	point := createMeteringPoint(t, c)

	start := time.Now().UTC().Truncate(time.Hour).Add(-24 * time.Hour)
	addConsumption(t, c, point.Id, start, start.Add(time.Hour), 1.5)

	first := time.Date(time.Now().Year()-3, time.January, 10, 0, 0, 0, 0, time.UTC)
	data := fileOf(
		[]string{"fra", "kwh"},
		[]string{first.Format(time.RFC3339), "1"},
		[]string{first.AddDate(0, 1, 0).Format(time.RFC3339), "2"},
	)
	importConsumption(t, c, point.Id, data, map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1})

	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id = $1`, point.Id); got != 3 {
		t.Errorf("%d intervals, want 3", got)
	}
}
