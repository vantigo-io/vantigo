package identity

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topics of brands (brands design B18). Events carry no data: a
// session that hears one refetches GET /brand, which is checked, so a person
// never receives a brand they do not resolve to.
const (
	// liveBrand is the workspace-wide topic, keyless: a publish, a change of
	// the default, an archive, a restore, a delete or a change of the rules.
	// With a key it is a person's own, identity.brand:{userId}: their personal
	// switch, heard by their other sessions only.
	liveBrand = "identity.brand"

	eventBrandChanged = "brand.changed"
	eventRulesChanged = "rules.changed"
	eventBrandChosen  = "brand.chosen"
)

// liveTopics is the module's LiveTopics. Both forms of the topic are open to
// any signed-in person, and the keyed one to its own id only.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveBrand, Authorize: func(ctx context.Context, key string) error {
			userID, ok := contracts.AsUserFrom(ctx)
			if !ok || userID == uuid.Nil {
				return live.ErrForbidden
			}
			if key == "" {
				return nil
			}
			if own, err := uuid.Parse(key); err != nil || own != userID {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifyBrands tells every open session that what brands people see may have
// changed. It runs on q, the transaction that wrote (live design L6), so it is
// heard only if that commits.
func notifyBrands(ctx context.Context, q live.Execer, kind string) error {
	return live.Notify(ctx, q, liveBrand, kind, "")
}

// notifyBrandChosen tells the person's other sessions that they chose a brand.
func notifyBrandChosen(ctx context.Context, q live.Execer, userID uuid.UUID) error {
	return live.Notify(ctx, q, liveBrand+":"+userID.String(), eventBrandChosen, "")
}
