package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a competence profile's validation (staff competence design
// S12, §6; the plan's readings 26 and 28): its items — at most fifty, each
// competence once, a minimum level only where the competence takes one — and
// the profiles assigned to a person — at most twenty, each once. The rules
// that read the catalogue run under the write's lock (judgeProfileItems,
// the assignment's own loop); the rest before it.

// The profiles' bounds (the plan's exact values).
const (
	profileItemsMax   = 50
	personProfilesMax = 20
)

// profileItem is one item of a profile as a write carries it: mustBeValid
// left out is true.
type profileItem struct {
	CompetenceID int64
	MinLevel     *int16
	MustBeValid  bool
}

// sameItem is whether an item asks what the stored row on the same
// competence asks: the same minimum level and the same validity rule.
func (p profileItem) sameItem(row store.StaffCompetenceProfileItem) bool {
	return equalInt16(p.MinLevel, row.MinLevel) && p.MustBeValid == row.MustBeValid
}

// itemPath is the field path of item i's property, as the contract's items
// name it (items[3].competenceId).
func itemPath(i int, property string) string { return fmt.Sprintf("items[%d].%s", i, property) }

// parseProfileItems is a profile's items judged on their own: at most fifty
// (items), a competence named twice refused on the later index, a minimum
// level of 0 to 4. Whether each competence exists, is offered and takes a
// level is judgeProfileItems', under the lock.
func parseProfileItems(items []gen.StaffCompetenceProfileItemRequest, f *fieldErrors) []profileItem {
	if len(items) > profileItemsMax {
		f.add("items", fmt.Sprintf("A profile has at most %d items", profileItemsMax))
		return nil
	}
	out := make([]profileItem, 0, len(items))
	seen := make(map[int64]bool, len(items))
	for i, it := range items {
		if seen[it.CompetenceId] {
			f.add(itemPath(i, "competenceId"), fmt.Sprintf("Competence %d is already an item of this profile", it.CompetenceId))
		}
		seen[it.CompetenceId] = true
		p := profileItem{CompetenceID: it.CompetenceId, MustBeValid: it.MustBeValid == nil || *it.MustBeValid}
		if it.MinLevel != nil {
			if *it.MinLevel < 0 || *it.MinLevel > competenceLevelMax {
				f.add(itemPath(i, "minLevel"), fmt.Sprintf("A minimum level is 0 to %d", competenceLevelMax))
			} else {
				v := int16(*it.MinLevel)
				p.MinLevel = &v
			}
		}
		out = append(out, p)
	}
	return out
}

// judgeProfileItems judges items against the catalogue under the write's
// lock, sharing each competence (ShareCompetence) so a change of its level
// and the item naming it are judged one after the other: an unknown
// competence is competence_unavailable on its index, as a hidden or archived
// one is on a new or changed item, while an item unchanged
// against before — the profile's stored items by competence, nil for a new
// profile — is kept (reading 28); a minimum level on a competence without a
// level is level_not_allowed.
func judgeProfileItems(ctx context.Context, txq *store.Queries, items []profileItem, before map[int64]store.StaffCompetenceProfileItem) error {
	var f fieldErrors
	for i, it := range items {
		c, err := txq.ShareCompetence(ctx, it.CompetenceID)
		if errors.Is(err, pgx.ErrNoRows) {
			f.addCoded(itemPath(i, "competenceId"), codeCompetenceUnavailable,
				fmt.Sprintf("Competence %d is unknown, hidden or archived; a new or changed item cannot name it", it.CompetenceID))
			continue
		} else if err != nil {
			return fmt.Errorf("staff: read competence %d: %w", it.CompetenceID, err)
		}
		stored, kept := before[it.CompetenceID]
		if (!kept || !it.sameItem(stored)) && !competenceAvailable(c) {
			f.addCoded(itemPath(i, "competenceId"), codeCompetenceUnavailable,
				fmt.Sprintf("Competence %d is hidden or archived; a new or changed item cannot name it", it.CompetenceID))
		}
		if it.MinLevel != nil && !c.HasLevel {
			f.addCoded(itemPath(i, "minLevel"), codeLevelNotAllowed, "This competence takes no level")
		}
	}
	if f.failed() {
		return refuseInvalid(invalidCompetenceProfileTitle, f)
	}
	return nil
}

// parseProfileIDs is the profiles assigned to a person judged on their own:
// at most twenty (profileIds), a profile named twice refused on the later
// index. Whether each exists and is not archived is judged under the
// person's lock.
func parseProfileIDs(ids []int64) fieldErrors {
	var f fieldErrors
	if len(ids) > personProfilesMax {
		f.add("profileIds", fmt.Sprintf("A person is assigned at most %d profiles", personProfilesMax))
		return f
	}
	seen := make(map[int64]bool, len(ids))
	for i, id := range ids {
		if seen[id] {
			f.add(fmt.Sprintf("profileIds[%d]", i), fmt.Sprintf("Profile %d is already in the list", id))
		}
		seen[id] = true
	}
	return f
}
