package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// DemoCampaign is a campaign of the development seed (brands design B-D): a
// dated overlay on a demo brand. It is added disabled when Enabled is false, so
// a demo is not seasonal by accident.
type DemoCampaign struct {
	// Brand is the brand's name; empty is the default brand.
	Brand, Name string
	// StartsOn and EndsOn are dates written 2006-01-02.
	StartsOn, EndsOn string
	Yearly, Enabled  bool
	// Primary and Title are the overlay's; either may be empty.
	Primary, Title string
}

// SeedDemoCampaign adds c to its brand unless the brand has a campaign of the
// name already (it is idempotent by brand and name). It reports whether it
// wrote. d needs Pool, Config, Logger and Clock only.
func SeedDemoCampaign(ctx context.Context, d module.Deps, c DemoCampaign) (bool, error) {
	if _, err := ensureDefaultBrand(ctx, d, config.Branding{}); err != nil {
		return false, err
	}
	q := store.New(d.Pool)
	var brandID uuid.UUID
	if c.Brand == "" {
		def, err := q.GetDefaultBrand(ctx)
		if err != nil {
			return false, fmt.Errorf("identity: read the default brand: %w", err)
		}
		brandID = def.ID
	} else {
		id, err := q.GetBrandIDByNameKey(ctx, nameKey(c.Brand))
		if errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("identity: the demo campaign %s names a brand that does not exist: %s", c.Name, c.Brand)
		}
		if err != nil {
			return false, fmt.Errorf("identity: look up a demo brand: %w", err)
		}
		brandID = id
	}
	existing, err := q.ListBrandCampaigns(ctx, brandID)
	if err != nil {
		return false, fmt.Errorf("identity: list the campaigns of a demo brand: %w", err)
	}
	for _, e := range existing {
		if e.Name == c.Name {
			return false, nil
		}
	}
	from, err := time.Parse(time.DateOnly, c.StartsOn)
	if err != nil {
		return false, fmt.Errorf("identity: the demo campaign %s starts on %q: %w", c.Name, c.StartsOn, err)
	}
	to, err := time.Parse(time.DateOnly, c.EndsOn)
	if err != nil {
		return false, fmt.Errorf("identity: the demo campaign %s ends on %q: %w", c.Name, c.EndsOn, err)
	}
	if c.Primary != "" {
		if refusal := checkOverlayPrimary(c.Primary); refusal != nil {
			return false, fmt.Errorf("identity: the demo campaign %s is invalid: %s", c.Name, refusal.message)
		}
	}
	if _, err := q.InsertBrandCampaign(ctx, store.InsertBrandCampaignParams{
		ID: uuid.New(), BrandID: brandID, Name: c.Name, StartsOn: pgDate(from), EndsOn: pgDate(to), Yearly: c.Yearly,
		Overlay: marshalOverlay(campaignOverlay{Primary: c.Primary, Title: c.Title}), Enabled: c.Enabled, Now: d.Clock(),
	}); err != nil {
		return false, fmt.Errorf("identity: add the demo campaign %s: %w", c.Name, err)
	}
	return true, nil
}
