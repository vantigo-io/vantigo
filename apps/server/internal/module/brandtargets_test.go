package module

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

type fakeBrandTargets struct{}

func (*fakeBrandTargets) LegalEntities(context.Context) ([]contracts.LegalEntityTarget, error) {
	return nil, nil
}

func (*fakeBrandTargets) LegalEntitiesOf(context.Context, []uuid.UUID) (map[uuid.UUID][]string, error) {
	return nil, nil
}

// BrandTargets (brands design B9) is a sole-provider slot like Timesheets:
// resolved before any Mount runs, onto every module's Deps.
func TestCompose_InjectsBrandTargetsProvider(t *testing.T) {
	targets := &fakeBrandTargets{}
	var got Deps
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "beta", BrandTargets: func(Deps) contracts.BrandTargets { return targets }, Mount: staticHandler("beta")},
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.BrandTargets != targets {
		t.Errorf("Deps.BrandTargets = %v, want the provider's", got.BrandTargets)
	}
}

// Two providers is a compose error naming the slot and both modules.
func TestCompose_DuplicateBrandTargetsProvider_Fails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", BrandTargets: func(Deps) contracts.BrandTargets { return &fakeBrandTargets{} }, Mount: staticHandler("alpha")},
		Module{Name: "beta", BrandTargets: func(Deps) contracts.BrandTargets { return &fakeBrandTargets{} }, Mount: staticHandler("beta")},
	)
	if err == nil || !strings.Contains(err.Error(), "brand targets") || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("two providers: error %v, want one naming the slot and both modules", err)
	}
}

// With the provider's module disabled the slot is nil, which identity reads as
// "no legal-entity targets"; a preset value survives when no module provides it.
func TestCompose_BrandTargetsNilWithoutAProviderAndAPresetSurvives(t *testing.T) {
	var got Deps
	mount := func(d Deps) (http.Handler, error) {
		got = d
		return staticHandler("alpha")(d)
	}
	if _, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}), Module{Name: "alpha", Mount: mount}); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.BrandTargets != nil {
		t.Errorf("Deps.BrandTargets = %v, want nil with no provider", got.BrandTargets)
	}
	preset := &fakeBrandTargets{}
	if _, err := compose(Deps{Access: &fakeAccess{}, BrandTargets: preset}, fakeLoad(map[string]string{"alpha": alphaContract}), Module{Name: "alpha", Mount: mount}); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.BrandTargets != preset {
		t.Errorf("Deps.BrandTargets = %v, want the preset to survive", got.BrandTargets)
	}
}
