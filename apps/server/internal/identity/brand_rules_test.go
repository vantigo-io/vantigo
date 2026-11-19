package identity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/live"
)

const (
	rulesPath       = "/api/v1/identity/brand-rules"
	targetsPath     = "/api/v1/identity/brand-rule-targets"
	resolutionPath  = "/api/v1/identity/brands/resolution"
	accountBrandURL = "/api/v1/identity/account/brand"

	tealPrimary  = "#0f766e"
	amberPrimary = "#b45309"
)

// fakeBrandTargets is Staff's contracts.BrandTargets, with the calls it
// answered counted.
type fakeBrandTargets struct {
	mu       sync.Mutex
	entities []contracts.LegalEntityTarget
	of       map[uuid.UUID][]string
	fail     bool
	delay    time.Duration
	ofCalls  atomic.Int32
}

func (f *fakeBrandTargets) LegalEntities(context.Context) ([]contracts.LegalEntityTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.LegalEntityTarget(nil), f.entities...), nil
}

func (f *fakeBrandTargets) LegalEntitiesOf(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	f.ofCalls.Add(1)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, fmt.Errorf("staff is unavailable")
	}
	out := map[uuid.UUID][]string{}
	for _, id := range ids {
		if e := f.of[id]; len(e) > 0 {
			out[id] = e
		}
	}
	return out, nil
}

func (f *fakeBrandTargets) setOf(id uuid.UUID, entities ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.of == nil {
		f.of = map[uuid.UUID][]string{}
	}
	f.of[id] = entities
}

// ruleBody is a rule as the list answers it.
type ruleBody struct {
	ID          string `json:"id"`
	BrandID     string `json:"brandId"`
	BrandName   string `json:"brandName"`
	Position    int    `json:"position"`
	Kind        string `json:"kind"`
	TargetRef   string `json:"targetRef"`
	TargetLabel string `json:"targetLabel"`
	Stale       bool   `json:"stale"`
	StaleReason string `json:"staleReason"`
}

type rulesList struct {
	Revision     int        `json:"revision"`
	StaffEnabled bool       `json:"staffEnabled"`
	Items        []ruleBody `json:"items"`
}

type ruleItem struct {
	BrandID   string `json:"brandId"`
	Kind      string `json:"kind"`
	TargetRef string `json:"targetRef"`
}

type candidateBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type myBrand struct {
	BrandID       string          `json:"brandId"`
	Name          string          `json:"name"`
	Source        string          `json:"source"`
	Candidates    []candidateBody `json:"candidates"`
	ChoiceIgnored bool            `json:"choiceIgnored"`
	Tokens        struct {
		Light struct {
			Shades []string `json:"shades"`
		} `json:"light"`
	} `json:"tokens"`
}

func createBrand(t *testing.T, c *client, name, primary string) brandBody {
	t.Helper()
	r := c.do(http.MethodPost, brandsPath, inputs(name, primary))
	if r.status != http.StatusCreated {
		t.Fatalf("create brand %s: status %d body %s", name, r.status, r.body)
	}
	var b brandBody
	r.json(&b)
	return b
}

// publishedBrand creates a brand and publishes it.
func publishedBrand(t *testing.T, c *client, name, primary string) brandBody {
	t.Helper()
	b := createBrand(t, c, name, primary)
	r := publish(c, b)
	if r.status != http.StatusOK {
		t.Fatalf("publish brand %s: status %d body %s", name, r.status, r.body)
	}
	r.json(&b)
	return b
}

func getRules(t *testing.T, c *client) rulesList {
	t.Helper()
	r := c.do(http.MethodGet, rulesPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET rules: status %d body %s", r.status, r.body)
	}
	var l rulesList
	r.json(&l)
	return l
}

func putRules(c *client, revision int, items ...ruleItem) *resp {
	if items == nil {
		items = []ruleItem{}
	}
	return c.do(http.MethodPut, rulesPath, map[string]any{"items": items}, header("If-Match", fmt.Sprint(revision)))
}

// setRules replaces the list at its current revision and fails the test on a refusal.
func setRules(t *testing.T, c *client, items ...ruleItem) rulesList {
	t.Helper()
	r := putRules(c, getRules(t, c).Revision, items...)
	if r.status != http.StatusOK {
		t.Fatalf("PUT rules: status %d body %s", r.status, r.body)
	}
	var l rulesList
	r.json(&l)
	return l
}

func myBrandOf(t *testing.T, c *client) myBrand {
	t.Helper()
	r := c.do(http.MethodGet, brandPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /brand: status %d body %s", r.status, r.body)
	}
	var b myBrand
	r.json(&b)
	return b
}

// archiveBrand archives a brand at its current revision.
func archiveBrand(t *testing.T, c *client, id string) {
	t.Helper()
	var fresh brandBody
	c.do(http.MethodGet, brandsPath+"/"+id, nil).json(&fresh)
	if r := c.do(http.MethodPost, fmt.Sprintf("%s/%s/archive", brandsPath, id), nil, header("If-Match", fmt.Sprint(fresh.Revision))); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
}

// defaultBrandID is the id of the workspace's default brand.
func defaultBrandID(t *testing.T, c *client) string {
	t.Helper()
	var l struct {
		Items []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"items"`
	}
	c.do(http.MethodGet, brandsPath, nil).json(&l)
	for _, it := range l.Items {
		if it.IsDefault {
			return it.ID
		}
	}
	t.Fatal("no default brand")
	return ""
}

func (h *harness) insertGroup(t *testing.T, name string, active bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
	        VALUES ($1, $2, 'local', $3, $4, $5, $5)`, id, name, active, uuid.New(), h.now())
	return id
}

func (h *harness) addMember(t *testing.T, group, user uuid.UUID, upstream bool, override *string) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
	        VALUES ($1, $2, 'local', $3, $4)`, group, user, upstream, override)
}

func (h *harness) personAt(t *testing.T, email string) (uuid.UUID, *client) {
	t.Helper()
	id := h.insertUser(t, email, identity.RoleUserID)
	return id, h.signIn(t, id, false)
}

func TestBrandLifecycle_CreateDuplicateMakeDefaultArchiveRestoreDeleteAreAuditedAndNameChecked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	def := getBrand(t, owner)

	a := createBrand(t, owner, "Nordlys", tealPrimary)
	if a.State != "draft" || a.Published != nil || a.Draft == nil || a.IsDefault {
		t.Fatalf("a created brand = %+v, want a draft that was never published", a)
	}
	if r := owner.do(http.MethodPost, brandsPath, inputs("nordlys", tealPrimary)); r.status != http.StatusConflict || r.code() != "brand_name_taken" {
		t.Errorf("a name differing only in case: status %d code %q, want 409 brand_name_taken", r.status, r.code())
	}
	if r := saveDraft(owner, a, inputs("Vantigo", tealPrimary)); r.status != http.StatusConflict || r.code() != "brand_name_taken" {
		t.Errorf("saving another brand's name: status %d code %q, want 409 brand_name_taken", r.status, r.code())
	}
	if r := owner.do(http.MethodPost, brandsPath, map[string]any{"name": "X", "primary": tealPrimary, "radius": "md", "tokens": map[string]any{}}); r.status != http.StatusBadRequest {
		t.Errorf("a create carrying tokens: %d, want 400", r.status)
	}

	// A draft cannot be the default; only a published brand can.
	mk := func(b brandBody) *resp {
		return owner.do(http.MethodPost, fmt.Sprintf("%s/%s/make-default", brandsPath, b.ID), nil, header("If-Match", fmt.Sprint(b.Revision)))
	}
	if r := mk(a); r.status != http.StatusConflict || r.code() != "brand_not_published" {
		t.Errorf("make-default of a draft: status %d code %q", r.status, r.code())
	}

	// Duplicate copies the version and finds a free name.
	d := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/duplicate", brandsPath, a.ID), nil, header("If-Match", fmt.Sprint(a.Revision)))
	var dup brandBody
	d.json(&dup)
	if d.status != http.StatusCreated || dup.Draft == nil || dup.Draft.Name != "Nordlys (copy)" || dup.Draft.Primary != tealPrimary {
		t.Fatalf("duplicate: status %d body %s", d.status, d.body)
	}
	// A never-published brand is deleted; the default is not.
	del := func(b brandBody) *resp {
		return owner.do(http.MethodDelete, fmt.Sprintf("%s/%s", brandsPath, b.ID), nil, header("If-Match", fmt.Sprint(b.Revision)))
	}
	if r := del(dup); r.status != http.StatusNoContent {
		t.Fatalf("delete a never-published brand: %d %s", r.status, r.body)
	}
	if r := del(def); r.status != http.StatusConflict || r.code() != "brand_in_use" {
		t.Errorf("delete the default: status %d code %q, want 409 brand_in_use", r.status, r.code())
	}

	// Publish, make default, archive the old default.
	var pub brandBody
	publish(owner, a).json(&pub)
	if r := del(pub); r.status != http.StatusConflict || r.code() != "brand_in_use" {
		t.Errorf("delete a published brand: status %d code %q, want 409 brand_in_use (archive it)", r.status, r.code())
	}
	r := mk(pub)
	var nowDefault brandBody
	r.json(&nowDefault)
	if r.status != http.StatusOK || !nowDefault.IsDefault {
		t.Fatalf("make-default: status %d body %s", r.status, r.body)
	}
	var oldDef brandBody
	owner.do(http.MethodGet, brandsPath+"/"+def.ID, nil).json(&oldDef)
	if oldDef.IsDefault {
		t.Error("the old default is still the default")
	}
	if p := myBrandOf(t, owner); p.Name != "Nordlys" {
		t.Errorf("after make-default a person with no rule sees %q, want Nordlys", p.Name)
	}
	arch := func(b brandBody) *resp {
		return owner.do(http.MethodPost, fmt.Sprintf("%s/%s/archive", brandsPath, b.ID), nil, header("If-Match", fmt.Sprint(b.Revision)))
	}
	if r := arch(nowDefault); r.status != http.StatusConflict || r.code() != "brand_in_use" {
		t.Errorf("archive the default: status %d code %q, want 409 brand_in_use", r.status, r.code())
	}
	if r := arch(oldDef); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	var archived brandBody
	owner.do(http.MethodGet, brandsPath+"/"+def.ID, nil).json(&archived)
	if archived.State != "archived" {
		t.Fatalf("state = %q, want archived", archived.State)
	}
	if r := arch(archived); r.status != http.StatusConflict {
		t.Errorf("archiving an archived brand: %d, want 409", r.status)
	}
	rr := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/restore", brandsPath, archived.ID), nil, header("If-Match", fmt.Sprint(archived.Revision)))
	var restored brandBody
	rr.json(&restored)
	if rr.status != http.StatusOK || restored.State != "published" {
		t.Errorf("restore: status %d state %q, want published again", rr.status, restored.State)
	}

	for action, want := range map[string]int{
		"brand.created": 2, "brand.published": 1, "brand.default_changed": 1, "brand.archived": 1, "brand.restored": 1, "brand.deleted": 1,
	} {
		if got := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = $1`, action); got != want {
			t.Errorf("audit %s = %d rows, want %d", action, got, want)
		}
	}
}

func TestBrandRules_ReplaceTheWholeListAtTheRevisionAndAuditIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	b := publishedBrand(t, owner, "Fjordkraft", amberPrimary)
	group := h.insertGroup(t, "Oslo office", true)

	start := getRules(t, owner)
	if len(start.Items) != 0 || start.StaffEnabled {
		t.Fatalf("the list starts as %+v, want empty, Staff off", start)
	}
	l := setRules(t, owner,
		ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "  Nordlys.Example. "},
		ruleItem{BrandID: b.ID, Kind: "access_group", TargetRef: group.String()},
	)
	if len(l.Items) != 2 || l.Items[0].TargetRef != "nordlys.example" || l.Items[0].Position != 1 ||
		l.Items[1].TargetLabel != "Oslo office" || l.Items[1].BrandName != "Fjordkraft" || l.Items[0].Stale || l.Items[1].Stale {
		t.Fatalf("rules = %+v", l.Items)
	}
	keptID := l.Items[1].ID

	// Reordering keeps a rule's identity.
	l2 := setRules(t, owner,
		ruleItem{BrandID: b.ID, Kind: "access_group", TargetRef: group.String()},
		ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "nordlys.example"},
	)
	if l2.Items[0].ID != keptID || l2.Items[0].Position != 1 || l2.Revision != l.Revision+1 {
		t.Errorf("after a reorder = %+v", l2)
	}

	// A stale If-Match is refused and changes nothing.
	if r := putRules(owner, l.Revision, ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "other.example"}); r.status != http.StatusConflict || r.code() != "stale_revision" {
		t.Errorf("stale PUT: status %d code %q, want 409 stale_revision", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, rulesPath, map[string]any{"items": []any{}}, header("If-Match", "x")); r.status != http.StatusBadRequest {
		t.Errorf("If-Match x: %d, want 400", r.status)
	}

	// A refused list is atomic: the list stays as it was.
	cur := getRules(t, owner)
	bad := putRules(owner, cur.Revision,
		ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "fresh.example"},
		ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "FRESH.example"},
	)
	if bad.status != http.StatusUnprocessableEntity || bad.code() != "rule_duplicate" {
		t.Errorf("the same domain twice on one brand: status %d code %q, want 422 rule_duplicate", bad.status, bad.code())
	}
	taken := putRules(owner, cur.Revision,
		ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "fresh.example"},
		ruleItem{BrandID: b.ID, Kind: "domain", TargetRef: "fresh.example"},
	)
	if taken.status != http.StatusUnprocessableEntity || taken.code() != "domain_taken" {
		t.Errorf("a domain on two brands: status %d code %q, want 422 domain_taken", taken.status, taken.code())
	}
	for _, d := range []string{"acme", "a@acme.no", "*.acme.no", "acme.no/x", ""} {
		if r := putRules(owner, cur.Revision, ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: d}); r.status != http.StatusUnprocessableEntity || r.code() != "domain_invalid" {
			t.Errorf("domain %q: status %d code %q, want 422 domain_invalid", d, r.status, r.code())
		}
	}
	if r := putRules(owner, cur.Revision, ruleItem{BrandID: a.ID, Kind: "access_group", TargetRef: uuid.NewString()}); r.status != http.StatusUnprocessableEntity || r.code() != "rule_target_not_found" {
		t.Errorf("an unknown group: status %d code %q, want 422 rule_target_not_found", r.status, r.code())
	}
	if r := putRules(owner, cur.Revision, ruleItem{BrandID: a.ID, Kind: "legal_entity", TargetRef: "1001"}); r.status != http.StatusUnprocessableEntity || r.code() != "staff_disabled" {
		t.Errorf("a legal entity with Staff off: status %d code %q, want 422 staff_disabled", r.status, r.code())
	}
	if r := putRules(owner, cur.Revision, ruleItem{BrandID: uuid.NewString(), Kind: "domain", TargetRef: "x.example"}); r.status != http.StatusUnprocessableEntity || r.code() != "rule_brand_invalid" {
		t.Errorf("an unknown brand: status %d code %q, want 422 rule_brand_invalid", r.status, r.code())
	}
	if again := getRules(t, owner); again.Revision != cur.Revision || len(again.Items) != 2 {
		t.Errorf("a refused list changed the list: %+v", again)
	}

	// A rule never points at an archived brand; archiving removes the brand's rules.
	archiveBrand(t, owner, a.ID)
	after := getRules(t, owner)
	if len(after.Items) != 1 || after.Items[0].BrandID != b.ID {
		t.Errorf("after archiving the first brand the list is %+v, want only the second brand's rule", after.Items)
	}
	if r := putRules(owner, after.Revision, ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "later.example"}); r.status != http.StatusUnprocessableEntity || r.code() != "rule_brand_invalid" {
		t.Errorf("a rule on an archived brand: status %d code %q, want 422 rule_brand_invalid", r.status, r.code())
	}

	if got := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = 'brand_rules.changed'`); got < 3 {
		t.Errorf("audit brand_rules.changed = %d rows, want at least 3 (two replacements and the archive)", got)
	}

	// Permission.
	plain := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)
	for _, p := range []string{rulesPath, targetsPath, resolutionPath + "?userId=" + uuid.NewString()} {
		if r := plain.do(http.MethodGet, p, nil); r.status != http.StatusForbidden {
			t.Errorf("a plain user: GET %s = %d, want 403", p, r.status)
		}
	}
	if r := putRules(plain, 1); r.status != http.StatusForbidden {
		t.Errorf("a plain user: PUT rules = %d, want 403", r.status)
	}
}

func TestBrandResolution_FirstMatchWinsAndFollowsTheDraggedOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	b := publishedBrand(t, owner, "Fjordkraft", amberPrimary)
	group := h.insertGroup(t, "Oslo office", true)
	id, user := h.personAt(t, "ola@nordlys.example")
	h.addMember(t, group, id, true, nil)

	byDomain := ruleItem{BrandID: a.ID, Kind: "domain", TargetRef: "nordlys.example"}
	byGroup := ruleItem{BrandID: b.ID, Kind: "access_group", TargetRef: group.String()}

	setRules(t, owner, byDomain, byGroup)
	got := myBrandOf(t, user)
	if got.Name != "Nordlys" || got.Source != "rule" || len(got.Candidates) != 2 || got.Candidates[0].Name != "Nordlys" || got.Candidates[1].Name != "Fjordkraft" {
		t.Errorf("domain first: %+v, want Nordlys by rule with both candidates", got)
	}
	if got.Tokens.Light.Shades[6] != tealPrimary {
		t.Errorf("the payload carries %v, not the matched brand's colours", got.Tokens.Light.Shades)
	}
	setRules(t, owner, byGroup, byDomain)
	got = myBrandOf(t, user)
	if got.Name != "Fjordkraft" || got.Source != "rule" || got.Candidates[0].Name != "Fjordkraft" {
		t.Errorf("group first: %+v, want Fjordkraft", got)
	}

	// Nobody else matches: the default, which is then the only candidate.
	_, other := h.personAt(t, "kari@elsewhere.example")
	o := myBrandOf(t, other)
	if o.Name != "Vantigo" || o.Source != "default" || len(o.Candidates) != 1 || o.Candidates[0].Name != "Vantigo" {
		t.Errorf("a person in no rule: %+v, want the default alone", o)
	}
	if got := myBrandOf(t, user); got.ChoiceIgnored {
		t.Error("choiceIgnored without a choice")
	}
}

func TestBrandResolution_EachTargetKindMatchesOnlyWhatItSays(t *testing.T) {
	t.Parallel()
	targets := &fakeBrandTargets{entities: []contracts.LegalEntityTarget{
		{ID: "1001", Name: "Nordlys AS"}, {ID: "1002", Name: "Gammel AS", Archived: true},
	}}
	h := newHarness(t, withBrandTargets(targets))
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)

	forceOut := "force_non_member"
	forceIn := "force_member"
	active := h.insertGroup(t, "Active", true)
	inactive := h.insertGroup(t, "Inactive", false)
	upstream := h.insertGroup(t, "Upstream", true)
	forced := h.insertGroup(t, "Forced", true)

	type tc struct {
		name  string
		item  ruleItem
		email string
		setup func(id uuid.UUID)
		want  bool
	}
	cases := []tc{
		{"domain exact", ruleItem{a.ID, "domain", "acme.no"}, "a@acme.no", nil, true},
		{"domain is not a subdomain match", ruleItem{a.ID, "domain", "acme.no"}, "a@mail.acme.no", nil, false},
		{"domain is not a suffix match", ruleItem{a.ID, "domain", "acme.no"}, "a@notacme.no", nil, false},
		{"domain is case-insensitive", ruleItem{a.ID, "domain", "acme.no"}, "b@ACME.no", nil, true},
		{"a member counts", ruleItem{a.ID, "access_group", active.String()}, "m1@x.example", func(id uuid.UUID) { h.addMember(t, active, id, true, nil) }, true},
		{"a member of an inactive group does not", ruleItem{a.ID, "access_group", inactive.String()}, "m2@x.example", func(id uuid.UUID) { h.addMember(t, inactive, id, true, nil) }, false},
		{"upstream-absent with no override does not", ruleItem{a.ID, "access_group", upstream.String()}, "m3@x.example", func(id uuid.UUID) { h.addMember(t, upstream, id, false, nil) }, false},
		{"forced out does not", ruleItem{a.ID, "access_group", forced.String()}, "m4@x.example", func(id uuid.UUID) { h.addMember(t, forced, id, true, &forceOut) }, false},
		{"forced in counts without upstream", ruleItem{a.ID, "access_group", forced.String()}, "m5@x.example", func(id uuid.UUID) { h.addMember(t, forced, id, false, &forceIn) }, true},
		{"a legal entity the person works under today", ruleItem{a.ID, "legal_entity", "1001"}, "e1@x.example", func(id uuid.UUID) { targets.setOf(id, "1001") }, true},
		{"another legal entity", ruleItem{a.ID, "legal_entity", "1001"}, "e2@x.example", func(id uuid.UUID) { targets.setOf(id, "1003") }, false},
		{"no employment in force", ruleItem{a.ID, "legal_entity", "1001"}, "e3@x.example", nil, false},
		{"two employments in two entities", ruleItem{a.ID, "legal_entity", "1001"}, "e4@x.example", func(id uuid.UUID) { targets.setOf(id, "1003", "1001") }, true},
	}
	for _, c := range cases {
		id, user := h.personAt(t, c.email)
		if c.setup != nil {
			c.setup(id)
		}
		setRules(t, owner, c.item)
		got := myBrandOf(t, user)
		if matched := got.Name == "Nordlys"; matched != c.want {
			t.Errorf("%s: sees %q, matched = %v, want %v", c.name, got.Name, matched, c.want)
		}
	}

	// An archived entity is flagged, kept and matches no one.
	l := setRules(t, owner, ruleItem{a.ID, "legal_entity", "1001"})
	h.exec(t, `INSERT INTO identity.brand_rules (id, brand_id, position, kind, target_ref, created_at) VALUES ($1, $2, 1, 'legal_entity', '1002', $3)`, uuid.New(), a.ID, h.now())
	cur := getRules(t, owner)
	flagged := ""
	for _, r := range cur.Items {
		if r.TargetRef == "1002" {
			flagged = r.StaleReason
		}
	}
	if flagged != "archived" || len(cur.Items) != len(l.Items)+1 {
		t.Errorf("an archived entity's rule: stale reason %q, rules %+v", flagged, cur.Items)
	}
	// A kept stale rule survives a save of the list, while a new rule on it is refused.
	if r := putRules(owner, cur.Revision, ruleItem{a.ID, "legal_entity", "1001"}, ruleItem{a.ID, "legal_entity", "1002"}); r.status != http.StatusOK {
		t.Errorf("keeping a stale rule: %d %s", r.status, r.body)
	}
	if r := putRules(owner, getRules(t, owner).Revision, ruleItem{a.ID, "legal_entity", "1002"}, ruleItem{a.ID, "legal_entity", "9999"}); r.status != http.StatusUnprocessableEntity || r.code() != "rule_target_not_found" {
		t.Errorf("a new rule on an unknown legal entity: status %d code %q, want 422 rule_target_not_found", r.status, r.code())
	}

	// The picker lists groups and, with Staff, legal entities.
	var pick struct {
		StaffEnabled  bool                    `json:"staffEnabled"`
		AccessGroups  []struct{ Name string } `json:"accessGroups"`
		LegalEntities []struct {
			ID, Name  string
			Available bool
		} `json:"legalEntities"`
	}
	owner.do(http.MethodGet, targetsPath, nil).json(&pick)
	if !pick.StaffEnabled || len(pick.AccessGroups) != 4 || len(pick.LegalEntities) != 2 || pick.LegalEntities[1].Available {
		t.Errorf("targets = %+v", pick)
	}
}

func TestBrandResolution_WithStaffDisabledEntityRulesAreInertAndTheOthersWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	_, user := h.personAt(t, "ola@nordlys.example")
	// A rule from the time Staff was on stays in the list, inert.
	h.exec(t, `INSERT INTO identity.brand_rules (id, brand_id, position, kind, target_ref, created_at) VALUES ($1, $2, 0, 'legal_entity', '1001', $3)`, uuid.New(), a.ID, h.now())
	l := getRules(t, owner)
	if len(l.Items) != 1 || !l.Items[0].Stale || l.Items[0].StaleReason != "staff_disabled" || l.StaffEnabled {
		t.Errorf("the list = %+v, want the rule flagged staff_disabled", l)
	}
	if got := myBrandOf(t, user); got.Name != "Vantigo" || got.Source != "default" {
		t.Errorf("entity rule with Staff off: %+v, want the default", got)
	}
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"})
	if got := myBrandOf(t, user); got.Name != "Nordlys" {
		t.Errorf("a domain rule with Staff off: %q, want Nordlys", got.Name)
	}
}

func TestBrandResolution_StaffFailingFailsTheRequestNeverUnderReports(t *testing.T) {
	t.Parallel()
	targets := &fakeBrandTargets{fail: true}
	h := newHarness(t, withBrandTargets(targets))
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	targets.entities = []contracts.LegalEntityTarget{{ID: "1001", Name: "Nordlys AS"}}
	setRules(t, owner, ruleItem{a.ID, "legal_entity", "1001"})
	_, user := h.personAt(t, "ola@x.example")
	// The contract documents no 500, so the exchange is made without the recorder.
	res, err := (&http.Client{Jar: user.http.Jar, Transport: h.srv.Client().Transport}).Get(h.url + brandPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("GET /brand while Staff fails: %d, want 500 (not the default)", res.StatusCode)
	}
}

func TestBrandResolution_NonPeopleGetTheDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{a.ID, "domain", "agents.invalid"})
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, kind, version, created_at, updated_at)
	        VALUES ($1, 'agent-1@agents.invalid', 'agent-1@agents.invalid', 'Agent', 'agent', $2, $3, $3)`, id, uuid.New(), h.now())
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleUserID)
	// An agent has no session, so the lookup is where its brand is seen.
	r := owner.do(http.MethodGet, resolutionPath+"?userId="+id.String(), nil)
	var got struct {
		Source string `json:"source"`
		Brand  struct{ Name string }
	}
	r.json(&got)
	if r.status != http.StatusOK || got.Brand.Name != "Vantigo" || got.Source != "default" {
		t.Errorf("an agent: %d %s, want the default though its domain matches", r.status, r.body)
	}
}

func TestBrandChoice_BeatsTheRulesWhileACandidateAndIsRefusedOtherwise(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	b := publishedBrand(t, owner, "Fjordkraft", amberPrimary)
	c := publishedBrand(t, owner, "Elsewhere", "#7c3aed")
	group := h.insertGroup(t, "Oslo office", true)
	id, user := h.personAt(t, "ola@nordlys.example")
	h.addMember(t, group, id, true, nil)
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"}, ruleItem{b.ID, "access_group", group.String()})

	choose := func(brandID any) *resp {
		return user.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": brandID})
	}

	r := choose(b.ID)
	var got myBrand
	r.json(&got)
	if r.status != http.StatusOK || got.Name != "Fjordkraft" || got.Source != "choice" {
		t.Fatalf("choose a candidate: status %d body %s", r.status, r.body)
	}
	if again := myBrandOf(t, user); again.Name != "Fjordkraft" || again.Source != "choice" {
		t.Errorf("GET /brand after the choice = %+v", again)
	}
	if css := string(user.do(http.MethodGet, brandCSSPath, nil).body); !strings.Contains(css, "--mantine-color-vantigo-6:"+amberPrimary+";") {
		t.Errorf("the stylesheet does not follow the choice:\n%s", css)
	}
	// The choice beats a rule that now puts someone else first.
	setRules(t, owner, ruleItem{b.ID, "access_group", group.String()}, ruleItem{a.ID, "domain", "nordlys.example"})
	if again := myBrandOf(t, user); again.Name != "Fjordkraft" {
		t.Errorf("after reordering = %q", again.Name)
	}

	// A brand that is not a candidate, a draft, an unknown id and the default
	// (which is a candidate only when no rule matched) are refused.
	var def brandBody
	for _, cand := range myBrandOf(t, user).Candidates {
		if cand.Name == "Vantigo" {
			t.Error("the default is a candidate though rules matched")
		}
	}
	for name, id := range map[string]any{"not assigned": c.ID, "unknown": uuid.NewString()} {
		if r := choose(id); r.status != http.StatusConflict || r.code() != "brand_not_candidate" {
			t.Errorf("%s: status %d code %q, want 409 brand_not_candidate", name, r.status, r.code())
		}
	}
	list := owner.do(http.MethodGet, brandsPath, nil)
	var l struct {
		Items []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"items"`
	}
	list.json(&l)
	for _, it := range l.Items {
		if it.IsDefault {
			def.ID = it.ID
		}
	}
	if r := choose(def.ID); r.status != http.StatusConflict || r.code() != "brand_not_candidate" {
		t.Errorf("the default while rules match: status %d code %q, want 409 brand_not_candidate", r.status, r.code())
	}

	// When rules change so that the choice is no longer a candidate, it is ignored and reported.
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"})
	ig := myBrandOf(t, user)
	if ig.Name != "Nordlys" || ig.Source != "rule" || !ig.ChoiceIgnored {
		t.Errorf("after the choice stopped being a candidate: %+v, want Nordlys by rule with choiceIgnored", ig)
	}
	// And it comes back when the rules do (it is kept, not cleared).
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"}, ruleItem{b.ID, "access_group", group.String()})
	if back := myBrandOf(t, user); back.Name != "Fjordkraft" || back.ChoiceIgnored {
		t.Errorf("after the rules came back: %+v", back)
	}

	// "Automatic" clears it.
	if r := choose(nil); r.status != http.StatusOK {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if auto := myBrandOf(t, user); auto.Name != "Nordlys" || auto.Source != "rule" {
		t.Errorf("automatic = %+v", auto)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND preferred_brand_id IS NULL`, id); n != 1 {
		t.Error("automatic left a choice stored")
	}

	// A person in no rule may choose the default (their only candidate); the choice is not audited.
	_, nobody := h.personAt(t, "kari@elsewhere.example")
	if r := nobody.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": def.ID}); r.status != http.StatusOK {
		t.Errorf("a person in no rule choosing the default: %d %s", r.status, r.body)
	}
	if r := nobody.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": a.ID}); r.status != http.StatusConflict {
		t.Errorf("a person in no rule choosing another brand: %d, want 409", r.status)
	}
	if r := h.client(t).do(http.MethodPut, accountBrandURL, map[string]any{"brandId": nil}); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action LIKE 'brand%choice%' OR action LIKE 'account.brand%'`); n != 0 {
		t.Errorf("a personal choice was audited (%d rows)", n)
	}
}

func TestBrandChoice_DeletingOrArchivingABrandClearsIt_AndAUsersRowKeepsItThroughAProfileChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	group := h.insertGroup(t, "Oslo office", true)
	id, user := h.personAt(t, "ola@nordlys.example")
	h.addMember(t, group, id, true, nil)
	d := createBrand(t, owner, "Draft only", tealPrimary)
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"}, ruleItem{d.ID, "access_group", group.String()})

	choose := func(id string) *resp { return user.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": id}) }
	if r := choose(a.ID); r.status != http.StatusOK {
		t.Fatalf("choose: %d %s", r.status, r.body)
	}
	// The choice is a preference of the user, kept through a profile change.
	if r := user.do(http.MethodPut, "/api/v1/identity/account/profile", map[string]any{"displayName": "Ola N", "preferredLanguage": "nb"}); r.status != http.StatusOK {
		t.Fatalf("profile: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND preferred_brand_id = $2`, id, a.ID); n != 1 {
		t.Error("a profile change cleared the brand choice")
	}

	// Deleting a never-published brand removes its rules and bumps the list.
	var fresh brandBody
	owner.do(http.MethodGet, brandsPath+"/"+d.ID, nil).json(&fresh)
	before := getRules(t, owner)
	if r := owner.do(http.MethodDelete, brandsPath+"/"+d.ID, nil, header("If-Match", fmt.Sprint(fresh.Revision))); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	after := getRules(t, owner)
	if len(after.Items) != 1 || after.Revision != before.Revision+1 {
		t.Errorf("after deleting the draft brand: %+v (was revision %d)", after, before.Revision)
	}

	// Archiving a chosen brand clears the choice of everyone who made it.
	owner.do(http.MethodGet, brandsPath+"/"+a.ID, nil).json(&fresh)
	if r := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/archive", brandsPath, a.ID), nil, header("If-Match", fmt.Sprint(fresh.Revision))); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND preferred_brand_id IS NULL`, id); n != 1 {
		t.Error("archiving the chosen brand left the choice")
	}
	// A brand deleted straight in the database nulls the reference (ON DELETE SET NULL).
	other := createBrand(t, owner, "Gone", amberPrimary)
	h.exec(t, `UPDATE identity.users SET preferred_brand_id = $2 WHERE id = $1`, id, other.ID)
	h.exec(t, `DELETE FROM identity.brands WHERE id = $1`, other.ID)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND preferred_brand_id IS NULL`, id); n != 1 {
		t.Error("deleting a brand did not null the choice")
	}
}

func TestBrandCSS_IsPerUserWithPrivateNoStoreAndVaryCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{a.ID, "domain", "nordlys.example"})
	_, ola := h.personAt(t, "ola@nordlys.example")
	_, kari := h.personAt(t, "kari@elsewhere.example")
	anon := h.client(t)

	css := func(c *client) *resp { return c.do(http.MethodGet, brandCSSPath, nil) }
	o, k, n := css(ola), css(kari), css(anon)
	if !strings.Contains(string(o.body), "--mantine-color-vantigo-6:"+tealPrimary+";") {
		t.Errorf("Ola's stylesheet:\n%s", o.body)
	}
	if !strings.Contains(string(k.body), "--mantine-color-vantigo-6:#4f46e5;") || !strings.Contains(string(n.body), "--mantine-color-vantigo-6:#4f46e5;") {
		t.Error("a person in no rule, and no one, should see Vantigo's own")
	}
	for name, r := range map[string]*resp{"Ola": o, "Kari": k} {
		if r.headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(r.headers.Get("Vary"), "Cookie") {
			t.Errorf("%s: Cache-Control %q Vary %q, want private, no-store and Vary: Cookie", name, r.headers.Get("Cache-Control"), r.headers.Get("Vary"))
		}
	}
	if n.headers.Get("Cache-Control") != "public, max-age=60" || !strings.Contains(n.headers.Get("Vary"), "Cookie") {
		t.Errorf("anonymous: %v", n.headers)
	}
	if my := ola.do(http.MethodGet, brandPath, nil); my.headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(my.headers.Get("Vary"), "Cookie") {
		t.Errorf("GET /brand headers = %v", my.headers)
	}
	// The public look is the default's whatever cookie comes with it.
	var p myBrand
	ola.do(http.MethodGet, brandPublicPath, nil).json(&p)
	if p.Name != "Vantigo" {
		t.Errorf("the public look for a signed-in person = %q, want the default's", p.Name)
	}
}

func TestBrandResolution_LookupShowsTheMatchedRuleAndEmploymentOnlyToAStaffViewer(t *testing.T) {
	t.Parallel()
	targets := &fakeBrandTargets{entities: []contracts.LegalEntityTarget{{ID: "1001", Name: "Nordlys AS"}}}
	staffView := contracts.Permission{Key: "staff:view", Display: "View the staff directory", Description: "See everyone's work details.", Category: "Staff", Delegable: true}
	h := newHarness(t, withBrandTargets(targets), withPermissions(staffView))
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	group := h.insertGroup(t, "Oslo office", true)
	id, _ := h.personAt(t, "ola@nordlys.example")
	h.addMember(t, group, id, true, nil)
	targets.setOf(id, "1001")
	setRules(t, owner, ruleItem{a.ID, "legal_entity", "1001"})

	brandsOnly := createRole(t, owner, "Brand editors", "identity:brands")
	both := createRole(t, owner, "Brand editors with staff", "identity:brands", "staff:view")
	editorID := h.insertUser(t, "editor@example.test", identity.RoleUserID)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, editorID, brandsOnly.ID)
	editor := h.signIn(t, editorID, false)
	bothID := h.insertUser(t, "both@example.test", identity.RoleUserID)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, bothID, both.ID)
	bothClient := h.signIn(t, bothID, false)

	type lookup struct {
		Source string `json:"source"`
		Brand  struct{ Name string }
		Rule   *struct {
			Kind        string
			TargetLabel string
			Position    int
		}
		Candidates []candidateBody
		Details    *struct {
			EmailDomain   string
			AccessGroups  []string
			LegalEntities []string
		}
	}
	get := func(c *client, user uuid.UUID) (lookup, *resp) {
		r := c.do(http.MethodGet, resolutionPath+"?userId="+user.String(), nil)
		var l lookup
		if r.status == http.StatusOK {
			r.json(&l)
		}
		return l, r
	}

	l, r := get(editor, id)
	if r.status != http.StatusOK || l.Brand.Name != "Nordlys" || l.Source != "rule" || l.Rule == nil || l.Rule.Kind != "legal_entity" || l.Rule.TargetLabel != "Nordlys AS" || l.Rule.Position != 1 {
		t.Fatalf("lookup = %+v (%d %s)", l, r.status, r.body)
	}
	if l.Details != nil || strings.Contains(string(r.body), "Oslo office") || strings.Contains(string(r.body), "details") {
		t.Errorf("a caller without staff:view got employment detail: %s", r.body)
	}
	l, r = get(bothClient, id)
	if l.Details == nil || l.Details.EmailDomain != "nordlys.example" || len(l.Details.AccessGroups) != 1 || l.Details.AccessGroups[0] != "Oslo office" ||
		len(l.Details.LegalEntities) != 1 || l.Details.LegalEntities[0] != "Nordlys AS" {
		t.Errorf("a caller with staff:view got %s", r.body)
	}
	if _, r := get(owner, uuid.New()); r.status != http.StatusNotFound {
		t.Errorf("an unknown person: %d, want 404", r.status)
	}
	if r := owner.do(http.MethodGet, resolutionPath+"?userId=nope", nil); r.status != http.StatusBadRequest {
		t.Errorf("a bad id: %d, want 400", r.status)
	}
	// The person may be named by address, in any case, and exactly one of the two is needed.
	if r := editor.do(http.MethodGet, resolutionPath+"?email=Ola%40Nordlys.Example", nil); r.status != http.StatusOK || !strings.Contains(string(r.body), id.String()) {
		t.Errorf("by address: %d %s", r.status, r.body)
	}
	if r := editor.do(http.MethodGet, resolutionPath+"?email=nobody%40nordlys.example", nil); r.status != http.StatusNotFound {
		t.Errorf("an unknown address: %d, want 404", r.status)
	}
	for name, q := range map[string]string{"neither": "", "both": "?userId=" + id.String() + "&email=ola%40nordlys.example"} {
		if r := editor.do(http.MethodGet, resolutionPath+q, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s of userId and email: %d, want 400", name, r.status)
		}
	}
}

func TestBrandResolution_WithStaffOffTheLookupNeverCarriesDetails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id, _ := h.personAt(t, "ola@nordlys.example")
	r := owner.do(http.MethodGet, resolutionPath+"?userId="+id.String(), nil)
	if r.status != http.StatusOK || strings.Contains(string(r.body), "details") {
		t.Errorf("lookup with Staff off: %d %s", r.status, r.body)
	}
}

func TestBrandResolution_StaffIsAskedOncePerPersonWhateverTheBurstAndAgainAfterThirtySeconds(t *testing.T) {
	t.Parallel()
	targets := &fakeBrandTargets{entities: []contracts.LegalEntityTarget{{ID: "1001", Name: "Nordlys AS"}}, delay: 100 * time.Millisecond}
	h := newHarness(t, withBrandTargets(targets))
	owner, _ := h.bootstrapOwner(t)
	a := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{a.ID, "legal_entity", "1001"})
	id, user := h.personAt(t, "ola@x.example")
	targets.setOf(id, "1001")

	burst := func(n int) {
		var wg sync.WaitGroup
		inFlight := make(chan struct{}, 20) // the listener's backlog is not what is under test
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				inFlight <- struct{}{}
				defer func() { <-inFlight }()
				if got := myBrandOf(t, user); got.Name != "Nordlys" {
					t.Errorf("the burst saw %q", got.Name)
				}
			}()
		}
		wg.Wait()
	}
	burst(200)
	if n := targets.ofCalls.Load(); n != 1 {
		t.Errorf("200 simultaneous GET /brand made %d calls to Staff, want 1", n)
	}
	// A publish does not become a Staff query for every open session.
	var cur brandBody
	owner.do(http.MethodGet, brandsPath+"/"+a.ID, nil).json(&cur)
	var drafted brandBody
	saveDraft(owner, cur, inputs("Nordlys", "#0e7490")).json(&drafted)
	if r := publish(owner, drafted); r.status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	burst(50)
	if n := targets.ofCalls.Load(); n != 1 {
		t.Errorf("after a publish %d calls to Staff, want still 1", n)
	}
	// The change reaches the person at once (the brand itself is not cached) ...
	if got := myBrandOf(t, user); got.Tokens.Light.Shades[6] != "#0e7490" {
		t.Errorf("the published change did not reach the person: %v", got.Tokens.Light.Shades)
	}
	// ... and what Staff says is read again after the cache's 30 seconds.
	targets.setOf(id)
	h.advance(31 * time.Second)
	if got := myBrandOf(t, user); got.Name != "Vantigo" {
		t.Errorf("after 31 s and a change of employment: %q, want the default", got.Name)
	}
	if n := targets.ofCalls.Load(); n != 2 {
		t.Errorf("calls to Staff = %d, want 2", n)
	}
}

// liveListener LISTENs on the channel every hub does, to see what a write sends.
type liveListener struct {
	wait func(context.Context) (string, error)
	done func()
}

func listenLive(t *testing.T, h *harness) *liveListener {
	t.Helper()
	ctx := context.Background()
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatal(err)
	}
	return &liveListener{done: conn.Release, wait: func(ctx context.Context) (string, error) {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return "", err
		}
		return n.Payload, nil
	}}
}

// drain is every event heard within a short wait, as "topic kind".
func (l *liveListener) drain() []string {
	var out []string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		p, err := l.wait(ctx)
		cancel()
		if err != nil {
			return out
		}
		var e struct {
			T string `json:"t"`
			K string `json:"k"`
		}
		if json.Unmarshal([]byte(p), &e) == nil && e.T != "" {
			out = append(out, e.T+" "+e.K)
		}
	}
}

func TestBrandLive_WorkspaceChangesNotifyTheTopicOnCommitOnly_AndAPersonalSwitchOnlyTheirOwn(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	l := listenLive(t, h)
	defer l.done()

	a := createBrand(t, owner, "Nordlys", tealPrimary)
	if got := l.drain(); len(got) != 0 {
		t.Errorf("creating a draft notified %v", got)
	}
	// A refused publish (nothing to publish) is a rollback: nothing is heard.
	var pub brandBody
	publish(owner, a).json(&pub)
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand brand.changed" {
		t.Errorf("publish notified %v, want one identity.brand brand.changed", got)
	}
	if r := publish(owner, pub); r.status != http.StatusConflict {
		t.Fatalf("second publish: %d", r.status)
	}
	if got := l.drain(); len(got) != 0 {
		t.Errorf("a refused publish notified %v", got)
	}

	mk := func(path string, b brandBody) {
		var fresh brandBody
		owner.do(http.MethodGet, brandsPath+"/"+b.ID, nil).json(&fresh)
		if r := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/%s", brandsPath, b.ID, path), nil, header("If-Match", fmt.Sprint(fresh.Revision))); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
	}
	oldDefault := brandBody{ID: defaultBrandID(t, owner)}
	for _, step := range []struct {
		path  string
		brand brandBody
	}{{"make-default", pub}, {"archive", oldDefault}, {"restore", oldDefault}} {
		mk(step.path, step.brand)
		step := step.path
		if got := l.drain(); len(got) != 1 || got[0] != "identity.brand brand.changed" {
			t.Errorf("%s notified %v", step, got)
		}
	}
	// Delete.
	n := createBrand(t, owner, "Gone", amberPrimary)
	owner.do(http.MethodDelete, brandsPath+"/"+n.ID, nil, header("If-Match", fmt.Sprint(n.Revision)))
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand brand.changed" {
		t.Errorf("delete notified %v", got)
	}

	// Rules: a replacement notifies; a stale one does not.
	cur := getRules(t, owner)
	if r := putRules(owner, cur.Revision, ruleItem{pub.ID, "domain", "nordlys.example"}); r.status != http.StatusOK {
		t.Fatalf("rules: %d %s", r.status, r.body)
	}
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand rules.changed" {
		t.Errorf("a rule change notified %v", got)
	}
	putRules(owner, cur.Revision)
	if got := l.drain(); len(got) != 0 {
		t.Errorf("a stale rule change notified %v", got)
	}

	// A personal switch notifies only the person's keyed topic.
	id, user := h.personAt(t, "ola@nordlys.example")
	if r := user.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": pub.ID}); r.status != http.StatusOK {
		t.Fatalf("choose: %d %s", r.status, r.body)
	}
	if got := l.drain(); len(got) != 1 || got[0] != "identity.brand:"+id.String()+" brand.chosen" {
		t.Errorf("a personal switch notified %v", got)
	}
	if r := user.do(http.MethodPut, accountBrandURL, map[string]any{"brandId": uuid.NewString()}); r.status != http.StatusConflict {
		t.Fatalf("a refused choice: %d", r.status)
	}
	if got := l.drain(); len(got) != 0 {
		t.Errorf("a refused choice notified %v", got)
	}
}

func TestBrandLive_TheKeyedTopicIsAuthorisedForItsOwnerOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	topics := identity.Module(h.access).LiveTopics(h.deps)
	if len(topics) != 1 || topics[0].Kind != "identity.brand" || topics[0].Station || topics[0].Presence || topics[0].Doc != nil {
		t.Fatalf("topics = %+v, want only identity.brand", topics)
	}
	me, other := uuid.New(), uuid.New()
	authorize := func(ctx context.Context, key string) error { return topics[0].Authorize(ctx, key) }
	asMe := contracts.AsUser(context.Background(), me)

	if err := authorize(asMe, ""); err != nil {
		t.Errorf("the workspace topic for a signed-in person: %v", err)
	}
	if err := authorize(asMe, me.String()); err != nil {
		t.Errorf("my own keyed topic: %v", err)
	}
	if err := authorize(asMe, other.String()); err == nil {
		t.Error("someone else's keyed topic was authorised")
	}
	if err := authorize(asMe, "not-a-uuid"); err == nil {
		t.Error("a key that is no user id was authorised")
	}
	for _, key := range []string{"", me.String()} {
		if err := authorize(context.Background(), key); err == nil {
			t.Errorf("an anonymous context was authorised for %q", key)
		}
	}
	if !live.ValidTopic("identity.brand") || !live.ValidTopic("identity.brand:"+me.String()) {
		t.Error("the topics do not follow the live grammar")
	}
}
