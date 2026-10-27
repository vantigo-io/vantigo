package energy

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestTariffFKRefusal pins the assignment's foreign-key backstop: a 23503 on
// the write's own constraint answers the field's "does not exist"; another
// constraint, another code, no tariff or no error answers nothing.
func TestTariffFKRefusal(t *testing.T) {
	t.Parallel()
	id := int32(1004)
	fk := func(code, constraint string) error {
		return fmt.Errorf("energy: create: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	for _, tc := range []struct {
		name string
		err  error
		id   *int32
		want map[string][]string
	}{
		{"the write's foreign key", fk("23503", supplyPeriodTariffFK), &id, map[string][]string{"tariffId": {"Tariff 1004 does not exist."}}},
		{"another foreign key", fk("23503", "supply_periods_metering_point_id_fkey"), &id, nil},
		{"another code", fk("23505", supplyPeriodTariffFK), &id, nil},
		{"no tariff", fk("23503", supplyPeriodTariffFK), nil, nil},
		{"no error", nil, &id, nil},
		{"a plain error", errors.New("boom"), &id, nil},
	} {
		if got := tariffFKRefusal(tc.err, supplyPeriodTariffFK, "tariffId", tc.id); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
