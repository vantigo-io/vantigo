package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/net/idna"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The assignment rules (brands design B8, B33): one ordered list across all
// brands, read and replaced whole with the list's revision as If-Match, so no
// two admins interleave half-edits.

const (
	auditBrandRulesChanged = "brand_rules.changed"

	brandRulesMax = 200

	codeRuleDuplicate      = "rule_duplicate"
	codeDomainTaken        = "domain_taken"
	codeDomainInvalid      = "domain_invalid"
	codeRuleTargetNotFound = "rule_target_not_found"
	codeRuleBrandInvalid   = "rule_brand_invalid"
	codeStaffDisabled      = "staff_disabled"
	codeTooManyRules       = "too_many_rules"
)

// permStaffView is the Staff permission that reads employment details (staff
// catalogue: "See everyone's work details, employments and terms"), the one
// that widens the lookup of who sees what (B11). It is a key, not an import.
const permStaffView = "staff:view"

// normalizeBrandDomain writes a domain as a rule stores it: lower case, in
// punycode, without a trailing dot. It refuses an address, a path, a wildcard
// and anything that is not a dotted name.
func normalizeBrandDomain(raw string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimSuffix(d, ".")
	if d == "" || len(d) > 253 || strings.ContainsAny(d, "@/\\:*?#% \t") {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil {
		return "", false
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return "", false
		}
	}
	return ascii, true
}

// ruleTargets are the names the rule editor and the staleness flags are checked
// against: the access groups, and the legal entities while Staff is enabled.
type ruleTargets struct {
	groups   map[string]store.ListBrandRuleGroupsRow
	entities map[string]contracts.LegalEntityTarget
	staffOn  bool
}

// readTargets reads the targets. It must not run inside a transaction: the
// contract takes a connection of its own.
func (s *server) readTargets(ctx context.Context, needEntities bool) (ruleTargets, error) {
	t := ruleTargets{groups: map[string]store.ListBrandRuleGroupsRow{}, entities: map[string]contracts.LegalEntityTarget{}, staffOn: s.deps.BrandTargets != nil}
	groups, err := s.q.ListBrandRuleGroups(ctx)
	if err != nil {
		return t, fmt.Errorf("identity: list the access groups: %w", err)
	}
	for _, g := range groups {
		t.groups[g.ID.String()] = g
	}
	if t.staffOn && needEntities {
		list, err := s.deps.BrandTargets.LegalEntities(ctx)
		if err != nil {
			return t, fmt.Errorf("identity: list the legal entities: %w", err)
		}
		for _, e := range list {
			t.entities[e.ID] = e
		}
	}
	return t, nil
}

// label and staleness of a rule's target.
func (t ruleTargets) describe(kind, ref string) (label string, stale bool, reason gen.BrandRuleStaleReason) {
	switch kind {
	case ruleKindDomain:
		return ref, false, ""
	case ruleKindAccessGroup:
		g, ok := t.groups[ref]
		switch {
		case !ok:
			return ref, true, gen.BrandRuleStaleReasonMissing
		case !g.IsActive:
			return g.DisplayName, true, gen.BrandRuleStaleReasonInactive
		}
		return g.DisplayName, false, ""
	case ruleKindLegalEntity:
		if !t.staffOn {
			return ref, true, gen.BrandRuleStaleReasonStaffDisabled
		}
		e, ok := t.entities[ref]
		switch {
		case !ok:
			return ref, true, gen.BrandRuleStaleReasonMissing
		case e.Archived:
			return e.Name, true, gen.BrandRuleStaleReasonArchived
		}
		return e.Name, false, ""
	}
	return ref, true, gen.BrandRuleStaleReasonMissing
}

// rulesView is the list as GET answers it, with the revision it is at.
func (s *server) rulesView(ctx context.Context) (gen.BrandRulesResponse, error) {
	rev, err := s.q.GetBrandRulesRevision(ctx)
	if err != nil {
		return gen.BrandRulesResponse{}, fmt.Errorf("identity: read the brand rules' revision: %w", err)
	}
	rules, err := s.q.ListBrandRules(ctx)
	if err != nil {
		return gen.BrandRulesResponse{}, fmt.Errorf("identity: list the brand rules: %w", err)
	}
	brands, err := s.q.ListBrands(ctx)
	if err != nil {
		return gen.BrandRulesResponse{}, fmt.Errorf("identity: list the brands: %w", err)
	}
	names := make(map[uuid.UUID]string, len(brands))
	for _, raw := range brands {
		b, err := loadBrandRow(raw)
		if err != nil {
			return gen.BrandRulesResponse{}, err
		}
		names[b.ID] = b.editing().Name
	}
	needEntities := false
	for _, r := range rules {
		needEntities = needEntities || r.Kind == ruleKindLegalEntity
	}
	t, err := s.readTargets(ctx, needEntities)
	if err != nil {
		return gen.BrandRulesResponse{}, err
	}
	items := make([]gen.BrandRule, 0, len(rules))
	for i, r := range rules {
		label, stale, reason := t.describe(r.Kind, r.TargetRef)
		item := gen.BrandRule{
			Id: r.ID, BrandId: r.BrandID, BrandName: names[r.BrandID], Position: int32(i + 1),
			Kind: gen.BrandRuleKind(r.Kind), TargetRef: r.TargetRef, TargetLabel: label, Stale: stale,
		}
		if stale {
			item.StaleReason = &reason
		}
		items = append(items, item)
	}
	return gen.BrandRulesResponse{Revision: rev, StaffEnabled: t.staffOn, Items: items}, nil
}

// GetIdentityBrandRules List the brand rules (GET /api/v1/identity/brand-rules)
func (s *server) GetIdentityBrandRules(ctx context.Context, _ gen.GetIdentityBrandRulesRequestObject) (gen.GetIdentityBrandRulesResponseObject, error) {
	view, err := s.rulesView(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentityBrandRules200JSONResponse(view), nil
}

// GetIdentityBrandRuleTargets List the targets a brand rule can name
// (GET /api/v1/identity/brand-rule-targets)
func (s *server) GetIdentityBrandRuleTargets(ctx context.Context, _ gen.GetIdentityBrandRuleTargetsRequestObject) (gen.GetIdentityBrandRuleTargetsResponseObject, error) {
	t, err := s.readTargets(ctx, true)
	if err != nil {
		return nil, err
	}
	out := gen.BrandRuleTargetsResponse{StaffEnabled: t.staffOn, AccessGroups: []gen.BrandRuleTarget{}, LegalEntities: []gen.BrandRuleTarget{}}
	groups, err := s.q.ListBrandRuleGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list the access groups: %w", err)
	}
	for _, g := range groups {
		out.AccessGroups = append(out.AccessGroups, gen.BrandRuleTarget{Id: g.ID.String(), Name: g.DisplayName, Available: g.IsActive})
	}
	if t.staffOn {
		list, err := s.deps.BrandTargets.LegalEntities(ctx)
		if err != nil {
			return nil, fmt.Errorf("identity: list the legal entities: %w", err)
		}
		for _, e := range list {
			out.LegalEntities = append(out.LegalEntities, gen.BrandRuleTarget{Id: e.ID, Name: e.Name, Available: !e.Archived})
		}
	}
	return gen.GetIdentityBrandRuleTargets200JSONResponse(out), nil
}

// ruleKey is a rule's identity in the list: the pair that appears once.
func ruleKey(kind, ref string) string { return kind + "\x00" + ref }

// PutIdentityBrandRules Replace the brand rules (PUT /api/v1/identity/brand-rules)
func (s *server) PutIdentityBrandRules(ctx context.Context, req gen.PutIdentityBrandRulesRequestObject) (gen.PutIdentityBrandRulesResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PutIdentityBrandRulesResponseObject, error) {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PutIdentityBrandRules400JSONResponse(r.body()), nil
		case http.StatusConflict:
			return gen.PutIdentityBrandRules409JSONResponse(r.body()), nil
		default:
			return gen.PutIdentityBrandRules422JSONResponse(r.body()), nil
		}
	}
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return reply(errIfMatch)
	}
	if req.Body == nil {
		return reply(refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, "The request needs a body.", nil))
	}
	items := req.Body.Items
	if len(items) > brandRulesMax {
		return reply(refuseBrand(http.StatusUnprocessableEntity, codeTooManyRules, "A workspace has at most 200 brand rules.", nil))
	}
	c, err := s.brandCall(ctx)
	if err != nil {
		return nil, err
	}
	needEntities := false
	for _, it := range items {
		needEntities = needEntities || it.Kind == ruleKindLegalEntity
	}
	// The targets are read before the transaction: the Staff contract takes a
	// connection of its own.
	targets, err := s.readTargets(ctx, needEntities)
	if err != nil {
		return nil, err
	}
	var refusal *brandRefusal
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.LockBrandRulesRevision(ctx)
		if err != nil {
			return fmt.Errorf("identity: lock the brand rules: %w", err)
		}
		if cur != rev {
			refusal = staleRevision
			return nil
		}
		existing, err := q.ListBrandRules(ctx)
		if err != nil {
			return fmt.Errorf("identity: list the brand rules: %w", err)
		}
		brands, err := q.ListBrands(ctx)
		if err != nil {
			return fmt.Errorf("identity: list the brands: %w", err)
		}
		brandState := make(map[uuid.UUID]string, len(brands))
		for _, b := range brands {
			brandState[b.ID] = b.State
		}
		kept := make(map[string]uuid.UUID, len(existing))
		for _, r := range existing {
			kept[ruleKey(r.Kind, r.TargetRef)] = r.ID
		}
		ids, refs, bad := validateRules(items, kept, brandState, targets)
		if bad != nil {
			refusal = bad
			return nil
		}
		if err := q.DeleteAllBrandRules(ctx); err != nil {
			return fmt.Errorf("identity: replace the brand rules: %w", err)
		}
		for i, it := range items {
			if err := q.InsertBrandRule(ctx, store.InsertBrandRuleParams{
				ID: ids[i], BrandID: uuid.UUID(it.BrandId), Position: int32(i), Kind: string(it.Kind), TargetRef: refs[i], Now: c.now,
			}); err != nil {
				return fmt.Errorf("identity: write a brand rule: %w", err)
			}
		}
		if _, err := q.BumpBrandRulesRevision(ctx); err != nil {
			return fmt.Errorf("identity: move the brand rules' revision: %w", err)
		}
		after := make([]map[string]any, len(items))
		for i, it := range items {
			after[i] = map[string]any{"id": ids[i], "brandId": it.BrandId, "kind": it.Kind, "target": refs[i]}
		}
		before := make([]map[string]any, len(existing))
		for i, r := range existing {
			before[i] = map[string]any{"id": r.ID, "brandId": r.BrandID, "kind": r.Kind, "target": r.TargetRef}
		}
		if err := writeAudit(ctx, q, c.r, c.now, auditEvent{
			actor: &c.p.UserID, action: auditBrandRulesChanged, before: before, after: after, mfa: c.p.MFAVerified,
		}); err != nil {
			return err
		}
		return notifyBrands(ctx, tx, eventRulesChanged)
	})
	switch {
	case err != nil:
		return nil, err
	case refusal != nil:
		return reply(refusal)
	}
	s.metrics.ruleChanges.Add(ctx, 1)
	s.deps.Logger.InfoContext(ctx, "identity: the brand rules were replaced", "rules", len(items))
	view, err := s.rulesView(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityBrandRules200JSONResponse(view), nil
}

// validateRules checks a whole list against the brands and the targets and
// answers, for each item, the id it keeps or gets and its normalised target.
// A rule already in the list is kept as it is even when its target has gone
// since (B8: such a rule is flagged, not refused); a new one must name a target
// that exists.
func validateRules(items []gen.BrandRuleInput, kept map[string]uuid.UUID, brandState map[uuid.UUID]string, t ruleTargets) ([]uuid.UUID, []string, *brandRefusal) {
	ids := make([]uuid.UUID, len(items))
	refs := make([]string, len(items))
	seenBrand := map[string]uuid.UUID{}
	field := func(i int, name, msg string) map[string][]string {
		return map[string][]string{fmt.Sprintf("items[%d].%s", i, name): {msg}}
	}
	for i, it := range items {
		ref := strings.TrimSpace(it.TargetRef)
		switch it.Kind {
		case ruleKindDomain:
			d, ok := normalizeBrandDomain(ref)
			if !ok {
				return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeDomainInvalid,
					"A domain is written like acme.no: letters, digits and hyphens in dotted labels, no address and no wildcard.",
					field(i, "targetRef", "Not a domain."))
			}
			ref = d
		case ruleKindAccessGroup:
			id, err := uuid.Parse(ref)
			if err != nil {
				return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeRuleTargetNotFound, "There is no such access group.", field(i, "targetRef", "There is no such access group."))
			}
			ref = id.String()
		case ruleKindLegalEntity:
		default:
			return nil, nil, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, "A rule's kind is domain, legal_entity or access_group.", field(i, "kind", "Not a kind of rule."))
		}
		refs[i] = ref
		key := ruleKey(string(it.Kind), ref)
		if other, dup := seenBrand[key]; dup {
			if it.Kind == ruleKindDomain && other != uuid.UUID(it.BrandId) {
				return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeDomainTaken,
					"The domain "+ref+" already belongs to another brand.", field(i, "targetRef", "The domain belongs to another brand."))
			}
			return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeRuleDuplicate,
				"The same target appears twice in the list.", field(i, "targetRef", "Already in the list."))
		}
		seenBrand[key] = uuid.UUID(it.BrandId)
		state, known := brandState[uuid.UUID(it.BrandId)]
		if !known || state == "archived" {
			return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeRuleBrandInvalid,
				"A rule needs a brand that exists and is not archived.", field(i, "brandId", "Choose a brand that is not archived."))
		}
		id, existing := kept[key]
		if !existing {
			id = uuid.New()
			if bad := checkNewTarget(string(it.Kind), ref, t); bad != nil {
				bad.fields = field(i, "targetRef", bad.message)
				return nil, nil, bad
			}
		}
		ids[i] = id
	}
	return ids, refs, nil
}

// checkNewTarget refuses a new rule whose target does not exist.
func checkNewTarget(kind, ref string, t ruleTargets) *brandRefusal {
	switch kind {
	case ruleKindAccessGroup:
		if _, ok := t.groups[ref]; !ok {
			return refuseBrand(http.StatusUnprocessableEntity, codeRuleTargetNotFound, "There is no such access group.", nil)
		}
	case ruleKindLegalEntity:
		if !t.staffOn {
			return refuseBrand(http.StatusUnprocessableEntity, codeStaffDisabled, "Legal entities are Staff's; Staff is not enabled.", nil)
		}
		e, ok := t.entities[ref]
		if !ok || e.Archived {
			return refuseBrand(http.StatusUnprocessableEntity, codeRuleTargetNotFound, "There is no such legal entity, or it is archived.", nil)
		}
	}
	return nil
}

// groupNamesOf is the names of the active groups a user is a member of.
func (s *server) groupNamesOf(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.q.ListUserActiveGroupNames(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read a user's groups: %w", err)
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.DisplayName)
	}
	return names, nil
}
