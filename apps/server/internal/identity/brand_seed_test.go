package identity_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

func TestSeedBrand_NamesTheDefaultBrandWithMarksAndIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withBrandStorage(t))
	owner, _ := h.bootstrapOwner(t)
	seed := identity.BrandSeed{Name: "Vantigo Demo AS", Title: "Vantigo Demo", Marks: map[string][]byte{
		"mark_light": pngOf(t, 120, 40), "mark_dark": pngOf(t, 120, 41),
	}}
	applied, err := identity.SeedBrand(context.Background(), h.deps, seed)
	if err != nil || !applied {
		t.Fatalf("SeedBrand = %v, %v, want applied", applied, err)
	}
	b := getBrand(t, owner)
	if b.Published.Name != "Vantigo Demo AS" || b.Published.Title != "Vantigo Demo" || len(b.Published.Logos) != 2 || b.PublishedVersion != 2 {
		t.Errorf("brand = %+v", b)
	}
	applied, err = identity.SeedBrand(context.Background(), h.deps, seed)
	if err != nil || applied {
		t.Errorf("a second SeedBrand = %v, %v, want a no-op", applied, err)
	}
}

func TestSeedBrand_WithoutAnObjectStoreNamesTheBrandOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	seed := identity.BrandSeed{Name: "Vantigo Demo AS", Title: "Vantigo Demo", Marks: map[string][]byte{"mark_light": pngOf(t, 120, 40)}}
	if applied, err := identity.SeedBrand(context.Background(), h.deps, seed); err != nil || !applied {
		t.Fatalf("SeedBrand = %v, %v", applied, err)
	}
	if b := getBrand(t, owner); b.Published.Title != "Vantigo Demo" || len(b.Published.Logos) != 0 {
		t.Errorf("brand = %+v", b.Published)
	}
	if applied, _ := identity.SeedBrand(context.Background(), h.deps, seed); applied {
		t.Error("a second SeedBrand wrote again")
	}
}
