package identity

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// BrandSeed is the development seed's default brand: a name and title, and PNG
// marks by slot ("mark_light", "mark_dark"), if there is an object store.
type BrandSeed struct {
	Name, Title string
	Marks       map[string][]byte
}

// SeedBrand applies s to the default brand, published, creating the brand when
// the API has not mounted yet. It is idempotent: a brand that already carries
// the name, title and every mark is left alone. Marks are skipped, with a log
// line, when the installation has no object store. It reports whether it wrote.
// d needs Pool, Config, Logger and Clock only.
func SeedBrand(ctx context.Context, d module.Deps, s BrandSeed) (bool, error) {
	if _, err := ensureDefaultBrand(ctx, d, config.Branding{}); err != nil {
		return false, err
	}
	q := store.New(d.Pool)
	raw, err := q.GetDefaultBrand(ctx)
	if err != nil {
		return false, fmt.Errorf("identity: read the default brand: %w", err)
	}
	b, err := loadBrandRow(raw)
	if err != nil {
		return false, err
	}
	cur := b.editing()
	objects, err := newBrandObjects(d)
	if err != nil {
		return false, err
	}
	marks := s.Marks
	if len(marks) > 0 && !objects.configured {
		d.Logger.InfoContext(ctx, "development seed: no object store is configured, so the demo brand has no logos")
		marks = nil
	}
	done := cur.Name == s.Name && cur.Title == s.Title && b.draft == nil
	for slot := range marks {
		if _, has := cur.Logos[slot]; !has {
			done = false
		}
	}
	if done {
		return false, nil
	}

	logos := map[string]brandLogo{}
	for k, l := range cur.Logos {
		logos[k] = l
	}
	for slot, data := range marks {
		logo, refusal, err := storeBrandLogo(ctx, objects, slot, data)
		if refusal != nil {
			return false, fmt.Errorf("identity: the demo %s logo was refused: %s", slot, refusal.message)
		}
		if err != nil {
			return false, err
		}
		logos[slot] = logo
	}
	v, refusal := newBrandVersion(brandInput{
		Name: s.Name, Title: s.Title, Primary: cur.Primary, Radius: cur.Radius, Support: cur.Support,
	}, logos)
	if refusal != nil {
		return false, fmt.Errorf("identity: the demo brand is invalid: %s", refusal.message)
	}
	now := d.Clock()
	err = db.WithTx(ctx, d.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		for _, l := range logos {
			if err := registerLogo(ctx, tq, l, now); err != nil {
				return err
			}
		}
		return tq.PublishBrand(ctx, store.PublishBrandParams{Published: v.marshal(), NameKey: nameKey(v.Name), Now: now, ID: raw.ID})
	})
	if err != nil {
		return false, fmt.Errorf("identity: publish the demo brand: %w", err)
	}
	return true, nil
}
