package customers

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file tests stats.go's attentionItemsFrom, the pure half of
// GET .../stats/attention (Brreg in full design D4, task 3): the four
// attention rules and their precedence, table-tested the
// registry_diff_test.go way, since everything here is unexported and needs
// neither a harness nor a database.

func attentionRow(customerID int32, name string, legalName *string, recordName string, bankrupt, underLiquidation, underForcedLiquidation bool, deletedOn *string, fetchedAt time.Time) store.RegistryAttentionCandidatesRow {
	d := pgtype.Date{}
	if deletedOn != nil {
		parsed, err := time.Parse("2006-01-02", *deletedOn)
		if err != nil {
			panic(err)
		}
		d = pgtype.Date{Time: parsed, Valid: true}
	}
	return store.RegistryAttentionCandidatesRow{
		CustomerID: customerID, Name: name, LegalName: legalName, RecordName: recordName,
		Bankrupt: bankrupt, UnderLiquidation: underLiquidation, UnderForcedLiquidation: underForcedLiquidation,
		DeletedOn: d, FetchedAt: fetchedAt,
	}
}

func strPtr(s string) *string { return &s }

var attentionFetchedAt = time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)

// TestAttentionItemsFrom_OneItemPerRule pins each of the four types on its
// own, the shape of the item it produces (id, title, occurredAt, entityId),
// and that a row satisfying none of the four rules produces nothing at all
// — a customer whose record simply matches everything on file, or one with
// no record (never in RegistryAttentionCandidates' result set to start
// with).
func TestAttentionItemsFrom_OneItemPerRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  store.RegistryAttentionCandidatesRow
		want *gen.CustomerStatsAttentionItem
	}{
		{
			name: "bankrupt",
			row:  attentionRow(7, "Acme AS", strPtr("Acme AS"), "Acme AS", true, false, false, nil, attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryBankrupt/7", Type: "registryBankrupt", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "7"},
		},
		{
			name: "underLiquidation",
			row:  attentionRow(8, "Acme AS", strPtr("Acme AS"), "Acme AS", false, true, false, nil, attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryLiquidation/8", Type: "registryLiquidation", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "8"},
		},
		{
			name: "underForcedLiquidation",
			row:  attentionRow(9, "Acme AS", strPtr("Acme AS"), "Acme AS", false, false, true, nil, attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryLiquidation/9", Type: "registryLiquidation", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "9"},
		},
		{
			name: "deleted",
			row:  attentionRow(10, "Acme AS", strPtr("Acme AS"), "Acme AS", false, false, false, strPtr("2026-09-21"), attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryDeleted/10", Type: "registryDeleted", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "10"},
		},
		{
			name: "renamed",
			row:  attentionRow(11, "Acme AS", strPtr("Acme Holding AS"), "Acme AS", false, false, false, nil, attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryRenamed/11", Type: "registryRenamed", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "11"},
		},
		{
			name: "renamed is trimmed, not case-folded",
			row:  attentionRow(12, "Acme AS", strPtr("  Acme AS  "), "Acme AS", false, false, false, nil, attentionFetchedAt),
			want: nil,
		},
		{
			name: "a case difference still counts as a rename",
			row:  attentionRow(13, "Acme AS", strPtr("ACME AS"), "Acme AS", false, false, false, nil, attentionFetchedAt),
			want: &gen.CustomerStatsAttentionItem{Id: "registryRenamed/13", Type: "registryRenamed", Title: "Acme AS", OccurredAt: attentionFetchedAt, EntityId: "13"},
		},
		{
			name: "no legal name at all yields no rename item, however different the record's name",
			row:  attentionRow(14, "Acme AS", nil, "Something Else AS", false, false, false, nil, attentionFetchedAt),
			want: nil,
		},
		{
			name: "nothing notable yields nothing",
			row:  attentionRow(15, "Acme AS", strPtr("Acme AS"), "Acme AS", false, false, false, nil, attentionFetchedAt),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attentionItemsFrom([]store.RegistryAttentionCandidatesRow{tc.row})
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("attentionItemsFrom(%q) = %+v, want no items", tc.name, got)
				}
				return
			}
			if len(got) != 1 || got[0] != *tc.want {
				t.Fatalf("attentionItemsFrom(%q) = %+v, want [%+v]", tc.name, got, *tc.want)
			}
		})
	}
}

// TestAttentionItemsFrom_Precedence pins the controller ruling: one item
// per customer, deleted > bankrupt > liquidation > renamed — a struck-off
// company's most useful single sentence is that it is deleted, even when it
// was also bankrupt, under liquidation and renamed all at once.
func TestAttentionItemsFrom_Precedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  store.RegistryAttentionCandidatesRow
		want string
	}{
		{
			name: "bankrupt beats liquidation",
			row:  attentionRow(20, "Acme AS", strPtr("Acme AS"), "Acme AS", true, true, false, nil, attentionFetchedAt),
			want: "registryBankrupt",
		},
		{
			name: "bankrupt beats renamed",
			row:  attentionRow(21, "Acme AS", strPtr("Something Else AS"), "Acme AS", true, false, false, nil, attentionFetchedAt),
			want: "registryBankrupt",
		},
		{
			name: "liquidation beats renamed",
			row:  attentionRow(22, "Acme AS", strPtr("Something Else AS"), "Acme AS", false, true, false, nil, attentionFetchedAt),
			want: "registryLiquidation",
		},
		{
			name: "deleted beats bankrupt, liquidation and renamed all at once",
			row:  attentionRow(23, "Acme AS", strPtr("Something Else AS"), "Acme AS", true, true, true, strPtr("2026-09-21"), attentionFetchedAt),
			want: "registryDeleted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attentionItemsFrom([]store.RegistryAttentionCandidatesRow{tc.row})
			if len(got) != 1 {
				t.Fatalf("attentionItemsFrom(%q) = %+v, want exactly one item", tc.name, got)
			}
			if got[0].Type != tc.want {
				t.Errorf("Type = %q, want %q", got[0].Type, tc.want)
			}
		})
	}
}

// TestAttentionItemsFrom_OrdersByOccurredAtDescendingThenID pins the
// ordering rule: newest fetch first, ties broken by id ascending.
func TestAttentionItemsFrom_OrdersByOccurredAtDescendingThenID(t *testing.T) {
	t.Parallel()
	older := attentionFetchedAt.Add(-24 * time.Hour)
	rows := []store.RegistryAttentionCandidatesRow{
		attentionRow(31, "B AS", nil, "", true, false, false, nil, older),
		attentionRow(30, "A AS", nil, "", true, false, false, nil, attentionFetchedAt),
		attentionRow(29, "C AS", nil, "", true, false, false, nil, attentionFetchedAt),
	}
	got := attentionItemsFrom(rows)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	wantOrder := []string{"registryBankrupt/29", "registryBankrupt/30", "registryBankrupt/31"}
	for i, id := range wantOrder {
		if got[i].Id != id {
			t.Errorf("got[%d].Id = %q, want %q (full order %+v)", i, got[i].Id, id, got)
		}
	}
}
