package expenses_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestExpenses_AnAPIKeyRecordsForAPersonItNames is API access design K56: an
// integration has no expenses or travel claims of its own, so a key must name
// the person in userId. Leaving it out, or naming the integration, is the
// module's 400 on userId; naming a person records it as theirs.
func TestExpenses_AnAPIKeyRecordsForAPersonItNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, personID := signIn(t, h)
	key, integration := h.SignInAPIKey(t, modtest.APIKey{}, "expenses:access", "expenses:manage")

	for _, c := range []struct {
		name, path, title string
		body              func(map[string]any) map[string]any
	}{
		{"an expense", entriesPath, invalidEntryTitle, outlayBody},
		{"a travel claim", claimsPath, invalidClaimTitle, claimBody},
	} {
		for name, overrides := range map[string]map[string]any{
			"no userId":       nil,
			"the integration": {"userId": integration.String()},
		} {
			errs := refused(t, key, http.MethodPost, c.path, c.body(overrides), c.title)
			if len(errs["userId"]) == 0 || !strings.Contains(errs["userId"][0], "name them in userId") {
				t.Errorf("%s, %s: errors = %v, want the key's userId refusal", c.name, name, errs)
			}
		}
	}

	entry := createEntry(t, key, outlayBody(map[string]any{"userId": personID.String()}))
	if entry.Owner.UserId != personID {
		t.Errorf("the key's expense belongs to %v, want the person it named", entry.Owner.UserId)
	}
	claim := createClaim(t, key, map[string]any{"userId": personID.String()})
	if claim.Owner.UserId != personID {
		t.Errorf("the key's claim belongs to %v, want the person it named", claim.Owner.UserId)
	}
}
