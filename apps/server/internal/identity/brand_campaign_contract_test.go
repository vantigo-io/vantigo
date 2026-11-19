package identity_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// Documents print the plain brand (brands design B24, B27): contracts.Brands
// never carries a campaign's colour, title or logos, even on the campaign's days.
func TestBrandsContract_NeverAppliesACampaignOverlay(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withBrandStorage(t))
	owner, _ := h.bootstrapOwner(t)
	brands := identity.Module(h.access).Brands(h.deps)
	ctx := context.Background()
	def := defaultBrandID(t, owner)

	c := addCampaign(t, owner, def, withOverlay(campaignInputs("Pride", "2026-09-01", "2026-09-30", false),
		map[string]any{"primary": amberPrimary, "title": "Pride title"}))
	var with campaignBody
	r := owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID+"/logos/mark_light", nil, logoUpload(t, pngOf(t, 64, 32)))
	r.json(&with)
	sha := with.Overlay.Logos["mark_light"].Sha256
	if r.status != http.StatusOK || sha == "" {
		t.Fatalf("campaign logo: %d %s", r.status, r.body)
	}
	if got := lookOf(t, owner); got.Campaign == nil || got.Title != "Pride title" {
		t.Fatalf("the campaign is not in force: %+v", got)
	}

	got, err := brands.Get(ctx, uuid.MustParse(def))
	if err != nil || got == nil {
		t.Fatalf("Get the default brand = %+v, %v", got, err)
	}
	if got.Primary == amberPrimary || got.Title == "Pride title" || len(got.Logos) != 0 {
		t.Errorf("contracts.Brands carries the overlay: %+v", got)
	}
	list, err := brands.List(ctx)
	if err != nil || len(list) != 1 || list[0].Primary == amberPrimary || list[0].Title == "Pride title" {
		t.Errorf("List = %+v, %v, want the plain brand", list, err)
	}
	if _, err := brands.LogoBytes(ctx, sha); !errors.Is(err, contracts.ErrBrandLogoNotFound) {
		t.Errorf("LogoBytes of a campaign's logo: %v, want ErrBrandLogoNotFound", err)
	}
}

// The public look by domain (brands design B25) wears the campaign of the brand
// it names, like the plain public look.
func TestCampaign_DomainMatchedPublicLookCarriesTheCampaign(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	anon := h.client(t)
	nordlys := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{BrandID: nordlys.ID, Kind: "domain", TargetRef: "nordlys.example"})
	addCampaign(t, owner, nordlys.ID, withOverlay(campaignInputs("Pride", "2026-09-01", "2026-09-30", false),
		map[string]any{"title": "Nordlys Pride"}))

	var look struct {
		lookBody
		Matched bool `json:"matched"`
	}
	anon.do(http.MethodGet, brandPublicPath+"?domain=nordlys.example", nil).json(&look)
	if !look.Matched || look.Campaign == nil || look.Title != "Nordlys Pride" || look.ValidUntil == nil {
		t.Errorf("domain-matched public look = %+v", look)
	}
	var plain lookBody
	anon.do(http.MethodGet, brandPublicPath+"?domain=other.example", nil).json(&plain)
	if plain.Campaign != nil {
		t.Errorf("an unmatched domain wears a campaign: %+v", plain)
	}
}
