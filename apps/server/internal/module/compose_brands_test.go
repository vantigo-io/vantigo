package module

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// contracts.Brands (brands design B29) resolves like the other single-provider
// slots: from the one module that declares it, onto every module's Deps; two
// providers are a compose error naming both.
func TestCompose_InjectsTheBrandsProvider(t *testing.T) {
	brands := &stubBrands{}
	var got Deps
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "beta", Brands: func(Deps) contracts.Brands { return brands }, Mount: staticHandler("beta")},
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.Brands != brands {
		t.Errorf("Deps.Brands = %v, want the provider's brands", got.Brands)
	}
}

func TestCompose_DuplicateBrandsProvider_Fails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Brands: func(Deps) contracts.Brands { return &stubBrands{} }, Mount: staticHandler("alpha")},
		Module{Name: "beta", Brands: func(Deps) contracts.Brands { return &stubBrands{} }, Mount: staticHandler("beta")},
	)
	if err == nil || !strings.Contains(err.Error(), "brands") || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("compose error = %v, want one naming brands and both modules", err)
	}
}

type stubBrands struct{}

func (*stubBrands) List(context.Context) ([]contracts.BrandEntry, error) { return nil, nil }
func (*stubBrands) Get(context.Context, uuid.UUID) (*contracts.BrandEntry, error) {
	return nil, nil
}
func (*stubBrands) LogoBytes(context.Context, string) (contracts.BrandLogo, error) {
	return contracts.BrandLogo{}, contracts.ErrBrandLogoNotFound
}
