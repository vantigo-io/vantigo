package identity_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The harness clock starts at 2026-09-11 12:00 UTC: 14:00 on 11 September in
// Oslo, the default business time zone.

// lookBody is GET /brand as the campaign tests read it.
type lookBody struct {
	BrandID  string `json:"brandId"`
	Version  string `json:"version"`
	Title    string `json:"title"`
	Campaign *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"campaign"`
	ValidUntil *time.Time        `json:"validUntil"`
	Logos      map[string]string `json:"logos"`
	Tokens     struct {
		Light struct {
			Shades []string `json:"shades"`
		} `json:"light"`
	} `json:"tokens"`
}

type campaignBody struct {
	ID           string `json:"id"`
	BrandID      string `json:"brandId"`
	Name         string `json:"name"`
	StartsOn     string `json:"startsOn"`
	EndsOn       string `json:"endsOn"`
	Yearly       bool   `json:"yearly"`
	Enabled      bool   `json:"enabled"`
	AffectsEmail bool   `json:"affectsEmail"`
	Status       string `json:"status"`
	Revision     int    `json:"revision"`
	Overlay      struct {
		Primary string `json:"primary"`
		Title   string `json:"title"`
		Logos   map[string]struct {
			Sha256 string `json:"sha256"`
		} `json:"logos"`
	} `json:"overlay"`
}

func campaignsPath(brandID string) string { return fmt.Sprintf("%s/%s/campaigns", brandsPath, brandID) }

func campaignInputs(name, from, to string, yearly bool) map[string]any {
	return map[string]any{"name": name, "startsOn": from, "endsOn": to, "yearly": yearly}
}

func withOverlay(in map[string]any, overlay map[string]any) map[string]any {
	in["overlay"] = overlay
	return in
}

func addCampaign(t *testing.T, c *client, brandID string, in map[string]any) campaignBody {
	t.Helper()
	r := c.do(http.MethodPost, campaignsPath(brandID), in)
	if r.status != http.StatusCreated {
		t.Fatalf("create campaign %v: status %d body %s", in["name"], r.status, r.body)
	}
	var out campaignBody
	r.json(&out)
	return out
}

func listCampaigns(t *testing.T, c *client, brandID string) []campaignBody {
	t.Helper()
	r := c.do(http.MethodGet, campaignsPath(brandID), nil)
	if r.status != http.StatusOK {
		t.Fatalf("list campaigns: status %d body %s", r.status, r.body)
	}
	var l struct {
		Items []campaignBody `json:"items"`
	}
	r.json(&l)
	return l.Items
}

func lookOf(t *testing.T, c *client) lookBody {
	t.Helper()
	r := c.do(http.MethodGet, brandPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /brand: status %d body %s", r.status, r.body)
	}
	var b lookBody
	r.json(&b)
	return b
}

func TestCampaign_IsLiveOnSaveFoldsIntoEveryLookAndEndsWhenDisabledOrDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	anon := h.client(t)
	def := defaultBrandID(t, owner)
	plain := lookOf(t, owner)

	c := addCampaign(t, owner, def, withOverlay(campaignInputs("Pride", "2026-09-10", "2026-09-20", false),
		map[string]any{"primary": amberPrimary, "title": "Vantigo Pride"}))
	if c.Status != "active" || !c.Enabled || c.Overlay.Primary != amberPrimary || c.AffectsEmail {
		t.Fatalf("created campaign = %+v, want an enabled, active one with the overlay and no e-mail tick", c)
	}

	// No draft and no publish: the person sees it at once, tokens recomputed.
	got := lookOf(t, owner)
	if got.Campaign == nil || got.Campaign.Name != "Pride" || got.Title != "Vantigo Pride" || got.Tokens.Light.Shades[6] == plain.Tokens.Light.Shades[6] {
		t.Errorf("GET /brand during the campaign = %+v", got)
	}
	if got.Version == plain.Version {
		t.Errorf("the version %q did not change when the campaign started", got.Version)
	}
	// validUntil is the start of the day after the last one, in Oslo (UTC+2 in September).
	if want := time.Date(2026, time.September, 20, 22, 0, 0, 0, time.UTC); got.ValidUntil == nil || !got.ValidUntil.Equal(want) {
		t.Errorf("validUntil = %v, want %v", got.ValidUntil, want)
	}
	css := string(anon.do(http.MethodGet, brandCSSPath, nil).body)
	if !strings.Contains(css, "--mantine-color-vantigo-6:"+got.Tokens.Light.Shades[6]) {
		t.Errorf("brand.css during the campaign lacks the overlay's shade %s:\n%s", got.Tokens.Light.Shades[6], css)
	}
	var public lookBody
	anon.do(http.MethodGet, brandPublicPath, nil).json(&public)
	if public.Campaign == nil || public.Title != "Vantigo Pride" || public.ValidUntil == nil {
		t.Errorf("GET /brand/public during the campaign = %+v", public)
	}

	// The brand's own radius, name and support stay the brand's (B24); the
	// editor's published look is untouched.
	b := getBrand(t, owner)
	if b.Published.Primary == amberPrimary || b.Published.Name != "Vantigo" {
		t.Errorf("the campaign changed the brand: %+v", b.Published)
	}

	// Disabled: the brand's own look, and the campaign is listed as disabled.
	var off campaignBody
	body := withOverlay(campaignInputs("Pride", "2026-09-10", "2026-09-20", false), map[string]any{"primary": amberPrimary, "title": "Vantigo Pride"})
	body["enabled"] = false
	r := owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID, body)
	r.json(&off)
	if r.status != http.StatusOK || off.Status != "disabled" || off.Revision != c.Revision+1 {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	if got := lookOf(t, owner); got.Campaign != nil || got.Version != plain.Version || got.ValidUntil != nil {
		t.Errorf("GET /brand with the campaign disabled = %+v, want the brand's own", got)
	}

	// Enabled again, then deleted: back to the brand at once.
	body["enabled"] = true
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID, body); r.status != http.StatusOK {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	if lookOf(t, owner).Campaign == nil {
		t.Error("the re-enabled campaign is not in force")
	}
	if r := owner.do(http.MethodDelete, campaignsPath(def)+"/"+c.ID, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, campaignsPath(def)+"/"+c.ID, nil); r.status != http.StatusNotFound {
		t.Errorf("delete twice: %d, want 404", r.status)
	}
	if got := lookOf(t, owner); got.Campaign != nil || got.Version != plain.Version {
		t.Errorf("GET /brand after the delete = %+v", got)
	}
	if n := len(listCampaigns(t, owner, def)); n != 0 {
		t.Errorf("%d campaigns after the delete, want none", n)
	}
}

func TestCampaign_DatesAreEvaluatedInTheBusinessTimeZone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	def := defaultBrandID(t, owner)

	// Ends today, 11 September. The clock reads 12:00 UTC, 14:00 in Oslo.
	addCampaign(t, owner, def, withOverlay(campaignInputs("Today", "2026-09-01", "2026-09-11", false), map[string]any{"title": "Today"}))
	if lookOf(t, owner).Campaign == nil {
		t.Fatal("not in force on its last day")
	}
	// 23:30 in Oslo is 21:30 UTC: still the 11th.
	h.advance(9*time.Hour + 30*time.Minute)
	owner = h.signIn(t, ownerID, false)
	if lookOf(t, owner).Campaign == nil {
		t.Error("not in force at 23:30 local on its last day")
	}
	// 00:30 on the 12th in Oslo is 22:30 UTC on the 11th: over, though UTC says otherwise.
	h.advance(time.Hour)
	owner = h.signIn(t, ownerID, false)
	if got := lookOf(t, owner); got.Campaign != nil {
		t.Errorf("still in force at 00:30 local on the day after: %+v", got.Campaign)
	}

	// The same instant in UTC is still the 11th.
	if r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": "UTC"}); r.status != http.StatusOK {
		t.Fatalf("set the zone: %d %s", r.status, r.body)
	}
	if lookOf(t, owner).Campaign == nil {
		t.Error("in the UTC zone the 11th is not over at 22:30 UTC")
	}
	if cs := listCampaigns(t, owner, def); len(cs) != 1 || cs[0].Status != "active" {
		t.Errorf("campaigns in UTC = %+v", cs)
	}
}

func TestCampaign_ValidUntilIsTheNextChangeAndAYearlyCampaignWrapsTheYearEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	def := defaultBrandID(t, owner)

	jul := addCampaign(t, owner, def, withOverlay(campaignInputs("Jul", "2026-12-01", "2027-01-06", true), map[string]any{"primary": tealPrimary}))
	if jul.Status != "upcoming" {
		t.Errorf("status in September = %q, want upcoming", jul.Status)
	}
	// Nothing is in force: validUntil is the start of 1 December in Oslo (UTC+1).
	got := lookOf(t, owner)
	if want := time.Date(2026, time.November, 30, 23, 0, 0, 0, time.UTC); got.Campaign != nil || got.ValidUntil == nil || !got.ValidUntil.Equal(want) {
		t.Errorf("before the campaign = campaign %+v validUntil %v, want none and %v", got.Campaign, got.ValidUntil, want)
	}
	// The wrapping range is in force from 1 December, over New Year, until 6 January.
	h.advance(time.Hour * 24 * 82) // 2 December, noon UTC
	owner = h.signIn(t, ownerID, false)
	if got := lookOf(t, owner); got.Campaign == nil || got.Campaign.Name != "Jul" {
		t.Fatalf("2 December: %+v", got)
	}
	h.advance(time.Hour * 24 * 36) // 7 January
	owner = h.signIn(t, ownerID, false)
	if got := lookOf(t, owner); got.Campaign != nil {
		t.Errorf("7 January: %+v, want the brand's own", got.Campaign)
	}
	h.advance(time.Hour * 24 * -2) // 5 January
	owner = h.signIn(t, ownerID, false)
	got = lookOf(t, owner)
	if want := time.Date(2027, time.January, 6, 23, 0, 0, 0, time.UTC); got.Campaign == nil || got.ValidUntil == nil || !got.ValidUntil.Equal(want) {
		t.Errorf("5 January = campaign %+v validUntil %v, want Jul until %v", got.Campaign, got.ValidUntil, want)
	}
	// It returns the next year.
	h.advance(time.Hour * 24 * 360) // 31 December 2027
	owner = h.signIn(t, ownerID, false)
	if got := lookOf(t, owner); got.Campaign == nil {
		t.Errorf("31 December 2027: not in force")
	}
}

func TestCampaign_TwoCampaignsOfABrandMayNotBeActiveOnTheSameDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	def := defaultBrandID(t, owner)
	other := publishedBrand(t, owner, "Nordlys", tealPrimary)

	jul := addCampaign(t, owner, def, campaignInputs("Jul", "2026-12-01", "2027-01-06", true))
	refused := func(name string, in map[string]any) {
		t.Helper()
		if r := owner.do(http.MethodPost, campaignsPath(def), in); r.status != http.StatusConflict || r.code() != "campaign_overlap" {
			t.Errorf("%s: status %d code %q body %s, want 409 campaign_overlap", name, r.status, r.code(), r.body)
		}
	}
	refused("a dated one across the year end, years later", campaignInputs("Nyttår", "2031-12-31", "2032-01-02", false))
	refused("a yearly one in January against the wrapping one", campaignInputs("Januar", "2026-01-05", "2026-01-20", true))
	refused("a yearly one on the first day", campaignInputs("Første", "2026-11-20", "2026-12-01", true))
	refused("a dated one inside it", campaignInputs("Inne", "2026-12-24", "2026-12-26", false))

	// Close but clear: the day after it ends, and before it starts.
	addCampaign(t, owner, def, campaignInputs("Vinter", "2026-01-07", "2026-02-28", true))
	addCampaign(t, owner, def, campaignInputs("Før jul", "2026-11-01", "2026-11-30", true))
	// Before the yearly one's first year it is not in force: a dated campaign then is fine.
	addCampaign(t, owner, def, campaignInputs("Gammel jul", "2025-12-24", "2025-12-26", false))
	// Another brand has its own campaigns.
	addCampaign(t, owner, other.ID, campaignInputs("Jul", "2026-12-01", "2027-01-06", true))

	// A disabled campaign takes no dates, and cannot be enabled onto others'.
	off := campaignInputs("Pause", "2026-12-10", "2026-12-12", false)
	off["enabled"] = false
	paused := addCampaign(t, owner, def, off)
	off["enabled"] = true
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/"+paused.ID, off); r.status != http.StatusConflict || r.code() != "campaign_overlap" {
		t.Errorf("enabling an overlapping campaign: status %d code %q, want 409 campaign_overlap", r.status, r.code())
	}
	// A campaign does not overlap itself when it is saved.
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/"+jul.ID, campaignInputs("Jul", "2026-12-02", "2027-01-05", true)); r.status != http.StatusOK {
		t.Errorf("saving a campaign over its own dates: %d %s", r.status, r.body)
	}
	// Moving one onto another is refused.
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/"+jul.ID, campaignInputs("Jul", "2026-12-01", "2027-01-08", true)); r.status != http.StatusConflict || r.code() != "campaign_overlap" {
		t.Errorf("stretching into Vinter: status %d code %q, want 409 campaign_overlap", r.status, r.code())
	}
}

func TestCampaign_InputsAreValidatedAndTokensAreNeverAccepted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	def := defaultBrandID(t, owner)

	type tc struct {
		name   string
		body   map[string]any
		status int
		code   string
	}
	for _, c := range []tc{
		{"no name", campaignInputs(" ", "2026-06-01", "2026-06-30", true), 400, "invalid_request"},
		{"ends before it starts", campaignInputs("X", "2026-06-30", "2026-06-01", false), 400, "invalid_request"},
		{"a year out of range", campaignInputs("X", "0001-06-01", "0001-06-30", false), 400, "invalid_request"},
		{"a yearly range of 366 days", campaignInputs("X", "2026-03-01", "2027-03-01", true), 422, "campaign_too_long"},
		{"a bad colour", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"primary": "pink"}), 400, "invalid_request"},
		{"white", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"primary": "#ffffff"}), 422, "brand_primary_range"},
		{"black", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"primary": "#000000"}), 422, "brand_primary_range"},
		{"a long title", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"title": strings.Repeat("x", 61)}), 400, "invalid_request"},
		{"tokens", func() map[string]any {
			b := campaignInputs("X", "2026-06-01", "2026-06-30", true)
			b["tokens"] = map[string]any{}
			return b
		}(), 400, "invalid_request"},
		{"tokens in the overlay", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"primary": tealPrimary, "shades": []string{"#000000"}}), 400, "invalid_request"},
		{"derived in the overlay", withOverlay(campaignInputs("X", "2026-06-01", "2026-06-30", true), map[string]any{"derived": map[string]any{}}), 400, "invalid_request"},
	} {
		if r := owner.do(http.MethodPost, campaignsPath(def), c.body); r.status != c.status || r.code() != c.code {
			t.Errorf("%s: status %d code %q body %s, want %d %s", c.name, r.status, r.code(), r.body, c.status, c.code)
		}
	}
	// A yearly range of exactly 365 days, and a leap-year pair of dates, are fine.
	addCampaign(t, owner, def, campaignInputs("Hele året", "2026-03-02", "2027-03-01", true))
	if n := len(listCampaigns(t, owner, def)); n != 1 {
		t.Errorf("%d campaigns, want just the valid one", n)
	}
	if r := owner.do(http.MethodPost, campaignsPath("00000000-0000-0000-0000-000000000000"), campaignInputs("X", "2026-06-01", "2026-06-30", true)); r.status != http.StatusNotFound {
		t.Errorf("a campaign on no brand: %d, want 404", r.status)
	}
	if r := owner.do(http.MethodGet, campaignsPath("00000000-0000-0000-0000-000000000000"), nil); r.status != http.StatusNotFound {
		t.Errorf("campaigns of no brand: %d, want 404", r.status)
	}

	// An archived brand's campaigns are not edited.
	b := publishedBrand(t, owner, "Nordlys", tealPrimary)
	c := addCampaign(t, owner, b.ID, campaignInputs("Jul", "2026-12-01", "2027-01-06", true))
	archiveBrand(t, owner, b.ID)
	if r := owner.do(http.MethodPost, campaignsPath(b.ID), campaignInputs("Pride", "2026-06-01", "2026-06-30", true)); r.status != http.StatusConflict || r.code() != "brand_archived" {
		t.Errorf("a campaign on an archived brand: status %d code %q, want 409 brand_archived", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, campaignsPath(b.ID)+"/"+c.ID, campaignInputs("Jul", "2026-12-01", "2027-01-06", true)); r.status != http.StatusConflict {
		t.Errorf("saving a campaign of an archived brand: %d, want 409", r.status)
	}
}

func TestCampaign_ListsWhereEachStandsAndNeedsTheBrandsPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	plain := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)
	def := defaultBrandID(t, owner)

	addCampaign(t, owner, def, campaignInputs("Nå", "2026-09-01", "2026-09-30", false))
	addCampaign(t, owner, def, campaignInputs("Senere", "2027-05-01", "2027-05-17", false))
	addCampaign(t, owner, def, campaignInputs("Ferdig", "2026-05-01", "2026-05-17", false))
	paused := campaignInputs("Pause", "2026-10-01", "2026-10-10", false)
	paused["enabled"] = false
	addCampaign(t, owner, def, paused)
	addCampaign(t, owner, def, campaignInputs("Jul", "2026-12-01", "2027-01-06", true))

	got := map[string]string{}
	for _, c := range listCampaigns(t, owner, def) {
		got[c.Name] = c.Status
	}
	want := map[string]string{"Nå": "active", "Senere": "upcoming", "Ferdig": "ended", "Pause": "disabled", "Jul": "upcoming"}
	for name, status := range want {
		if got[name] != status {
			t.Errorf("%s is %q, want %q (all: %v)", name, got[name], status, got)
		}
	}

	for _, r := range []*resp{
		plain.do(http.MethodGet, campaignsPath(def), nil),
		plain.do(http.MethodPost, campaignsPath(def), campaignInputs("X", "2026-06-01", "2026-06-02", false)),
		h.client(t).do(http.MethodGet, campaignsPath(def), nil),
	} {
		if r.status != http.StatusForbidden && r.status != http.StatusUnauthorized {
			t.Errorf("a campaign operation without identity:brands: %d, want 403 or 401", r.status)
		}
	}
}

func TestCampaign_LogosAreServedAnonymouslyOnlyWhileTheCampaignIsInForce(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withBrandStorage(t))
	owner, ownerID := h.bootstrapOwner(t)
	anon := h.client(t)
	def := defaultBrandID(t, owner)

	c := addCampaign(t, owner, def, campaignInputs("Pride", "2026-09-11", "2026-09-12", false))
	slotPath := campaignsPath(def) + "/" + c.ID + "/logos/mark_light"
	r := owner.do(http.MethodPut, slotPath, nil, logoUpload(t, pngOf(t, 64, 32)))
	var with campaignBody
	r.json(&with)
	sha := with.Overlay.Logos["mark_light"].Sha256
	if r.status != http.StatusOK || sha == "" {
		t.Fatalf("PUT campaign logo: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID+"/logos/icon", nil, logoUpload(t, pngOf(t, 64, 32))); r.status != http.StatusBadRequest || r.code() != "logo_not_square" {
		t.Errorf("a wide icon: status %d code %q, want 400 logo_not_square", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, slotPath, nil, logoUpload(t, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))); r.status != http.StatusBadRequest || r.code() != "logo_unsupported_type" {
		t.Errorf("an SVG: status %d code %q, want 400 logo_unsupported_type", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, campaignsPath(def)+"/00000000-0000-0000-0000-000000000000/logos/mark_light", nil, logoUpload(t, pngOf(t, 64, 32))); r.status != http.StatusNotFound {
		t.Errorf("a logo on no campaign: %d, want 404", r.status)
	}

	// The editor reads it back; the brand's own published logos are untouched.
	if g := owner.do(http.MethodGet, slotPath, nil); g.status != http.StatusOK || g.headers.Get("Content-Type") != "image/png" || g.headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("GET campaign logo: status %d headers %v", g.status, g.headers)
	}
	if g := owner.do(http.MethodGet, campaignsPath(def)+"/"+c.ID+"/logos/wordmark", nil); g.status != http.StatusNotFound {
		t.Errorf("GET an empty slot: %d, want 404", g.status)
	}
	if b := getBrand(t, owner); len(b.Published.Logos) != 0 {
		t.Errorf("a campaign logo reached the brand: %+v", b.Published.Logos)
	}

	// In force: the payload names it and the anonymous route serves it.
	got := lookOf(t, owner)
	if !strings.HasSuffix(got.Logos["mark_light"], sha+".png") {
		t.Errorf("GET /brand logos = %v, want the campaign's mark", got.Logos)
	}
	if a := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil); a.status != http.StatusOK {
		t.Errorf("anonymous asset while in force: %d, want 200", a.status)
	}

	// Disabled, or over: the same bare 404.
	off := campaignInputs("Pride", "2026-09-11", "2026-09-12", false)
	off["enabled"] = false
	owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID, off)
	if a := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil); a.status != http.StatusNotFound {
		t.Errorf("anonymous asset of a disabled campaign: %d, want 404", a.status)
	}
	off["enabled"] = true
	owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID, off)
	if a := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil); a.status != http.StatusOK {
		t.Fatalf("anonymous asset after enabling: %d, want 200", a.status)
	}
	h.advance(48 * time.Hour)
	owner = h.signIn(t, ownerID, false)
	if a := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil); a.status != http.StatusNotFound {
		t.Errorf("anonymous asset after the campaign: %d, want 404", a.status)
	}

	// Removing the logo clears the slot.
	var cleared campaignBody
	d := owner.do(http.MethodDelete, slotPath, nil)
	d.json(&cleared)
	if d.status != http.StatusOK || len(cleared.Overlay.Logos) != 0 {
		t.Errorf("DELETE campaign logo: status %d body %s", d.status, d.body)
	}
	if g := owner.do(http.MethodGet, slotPath, nil); g.status != http.StatusNotFound {
		t.Errorf("GET a removed logo: %d, want 404", g.status)
	}
}

func TestCampaign_EveryWriteIsAuditedAndTellsTheOpenSessions(t *testing.T) {
	h := newHarness(t, withBrandStorage(t))
	owner, _ := h.bootstrapOwner(t)
	def := defaultBrandID(t, owner)
	l := listenLive(t, h)
	defer l.done()

	c := addCampaign(t, owner, def, campaignInputs("Jul", "2026-12-01", "2027-01-06", true))
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand brand.changed" {
		t.Errorf("create notified %v, want one identity.brand brand.changed", got)
	}
	// A refused write is a rollback: nothing is heard, nothing is audited.
	if r := owner.do(http.MethodPost, campaignsPath(def), campaignInputs("Nyttår", "2026-12-31", "2027-01-01", false)); r.status != http.StatusConflict {
		t.Fatalf("overlap: %d", r.status)
	}
	if got := l.drain(); len(got) != 0 {
		t.Errorf("a refused create notified %v", got)
	}
	owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID, campaignInputs("Jul 2", "2026-12-01", "2027-01-06", true))
	if got := l.drain(); len(got) != 1 {
		t.Errorf("update notified %v, want one", got)
	}
	owner.do(http.MethodPut, campaignsPath(def)+"/"+c.ID+"/logos/mark_light", nil, logoUpload(t, pngOf(t, 64, 32)))
	owner.do(http.MethodDelete, campaignsPath(def)+"/"+c.ID+"/logos/mark_light", nil)
	l.drain()
	owner.do(http.MethodDelete, campaignsPath(def)+"/"+c.ID, nil)
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand brand.changed" {
		t.Errorf("delete notified %v, want one identity.brand brand.changed", got)
	}

	for action, want := range map[string]int{
		"brand.campaign_created": 1, "brand.campaign_updated": 1, "brand.campaign_deleted": 1,
		"brand.campaign_logo_set": 1, "brand.campaign_logo_removed": 1,
	} {
		if got := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = $1`, action); got != want {
			t.Errorf("audit %s = %d rows, want %d", action, got, want)
		}
	}
}
